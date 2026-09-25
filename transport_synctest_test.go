// Copyright 2026 The typesafe-sdk-go Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package typesafe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// The S-T2 fake-network tests again, as internal/h2gate's
// TestSynctestFanOut runs them, but through the option a caller uses: the
// fake server's client transport goes to WithHTTPTransport, and resolve
// builds the gate from its clone. The fake transport dials the in-memory
// listener (httptest/server.go:439-444), so the default transport cannot be
// tested there. Every call carries a deadline on fake time: a strict-mode
// stall is a durable block (internal/http2/transport.go:1555).
const (
	// fakeDeadline bounds every call on fake time.
	fakeDeadline = 2 * time.Minute
	// fakeFanOut is the cold fan-out width (h2gate's fanN).
	fakeFanOut = 64
	// fakeSendPing is the HTTP/2 ping interval the SDK sets when the
	// caller's transport leaves it zero (section 6.3).
	fakeSendPing = 30 * time.Second
	// fakeIdle is the idle timeout the test sets on the fake transport as a
	// caller would; the SDK leaves a caller's IdleConnTimeout alone.
	fakeIdle = 90 * time.Second
)

// fakeClientTransport returns srv's client transport, which speaks HTTP/2
// with prior knowledge to the in-memory listener.
func fakeClientTransport(t *testing.T, srv *httptest.Server) *http.Transport {
	t.Helper()
	tr, ok := srv.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("fake client transport is %T", srv.Client().Transport)
	}
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	tr.Protocols = &p
	return tr
}

// pingConn counts the HTTP/2 PING frames the client writes on a connection,
// acknowledgements aside: it reads the header of each frame the client
// writes after its 24-byte connection preface (RFC 9113, sections 3.4, 4.1
// and 6.7).
type pingConn struct {
	net.Conn
	pings *atomic.Int64

	mu   sync.Mutex
	skip int    // bytes of the preface or of a frame payload still to pass
	head []byte // the part read so far of a frame header
}

// Write implements net.Conn.
func (c *pingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.scan(p)
	c.mu.Unlock()
	return c.Conn.Write(p)
}

// scan reads the frame headers in p, which continues the bytes written so
// far.
func (c *pingConn) scan(p []byte) {
	const (
		headerLen = 9
		typePing  = 0x6
		flagAck   = 0x1
	)
	for len(p) > 0 {
		if c.skip > 0 {
			n := min(c.skip, len(p))
			c.skip -= n
			p = p[n:]
			continue
		}
		n := min(headerLen-len(c.head), len(p))
		c.head = append(c.head, p[:n]...)
		p = p[n:]
		if len(c.head) < headerLen {
			return
		}
		if c.head[3] == typePing && c.head[4]&flagAck == 0 {
			c.pings.Add(1)
		}
		c.skip = int(c.head[0])<<16 | int(c.head[1])<<8 | int(c.head[2])
		c.head = c.head[:0]
	}
}

// countPings makes base's connections count the PINGs the client sends into
// pings. The clone WithHTTPTransport makes keeps base's DialContext.
func countPings(base *http.Transport, pings *atomic.Int64) {
	const prefaceLen = 24 // "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
	dial := base.DialContext
	base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &pingConn{Conn: conn, pings: pings, skip: prefaceLen}, nil
	}
}

// fakeConfig resolves a configuration whose API is the fake server, over
// HTTP2Only through WithHTTPTransport(base), and closes its transport when
// the test ends, before the server's own cleanup.
func fakeConfig(t *testing.T, base *http.Transport) *config {
	t.Helper()
	c := mustResolve(t, noEnv, WithAPIKey(testKey), WithBaseURL("http://example.com"), WithHTTPVersion(HTTP2Only), WithHTTPTransport(base))
	t.Cleanup(func() { _ = c.transport.close() })
	return c
}

// fakeFan sends n GETs at once through tr, each within d, and returns them in
// order.
func fakeFan(t *testing.T, tr *transport, n int, prefix string, d time.Duration) []getResult {
	t.Helper()
	out := make([]getResult, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { out[i] = getWithin(t, tr, "http://example.com"+prefix+strconv.Itoa(i), 0, d) })
	}
	wg.Wait()
	return out
}

// TestSynctestThroughWithHTTPTransport is S-T2 through WithHTTPTransport:
// the cold fan-out on one connection, the warm burst on none, the ping timer
// (a PING the client sends on the idle connection, counted on its writes)
// and the idle timer on fake time, and F1's 16 calls against a limit of 4, which
// strict accounting alone stalls and the token completes. The SDK sets what
// the caller's transport leaves zero (one connection per host, strict
// accounting, the ping timeouts); the caller's own IdleConnTimeout is kept.
func TestSynctestThroughWithHTTPTransport(t *testing.T) {
	t.Run("success: cold 64 on 1 connection, warm 64 on none, then the ping and idle timers", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			srv := testsupport.NewFakeH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
			base := fakeClientTransport(t, srv.Server)
			base.IdleConnTimeout = fakeIdle
			var pings atomic.Int64
			countPings(base, &pings)
			c := fakeConfig(t, base)
			cold := fakeFan(t, c.transport, fakeFanOut, "/cold/", fakeDeadline)
			accCold := srv.Accepts()
			warm := fakeFan(t, c.transport, fakeFanOut, "/warm/", fakeDeadline)
			accWarm := srv.Accepts()
			for i, r := range append(cold, warm...) {
				if r.err != nil || r.status != http.StatusOK || r.protoMajor != 2 {
					t.Errorf("call %d: %d HTTP/%d %v", i, r.status, r.protoMajor, r.err)
				}
			}
			pingsBusy := pings.Load()
			time.Sleep(fakeSendPing + time.Second) // the client pings the idle connection; the server answers
			synctest.Wait()
			pingsIdle := pings.Load() - pingsBusy
			afterPing := getWithin(t, c.transport, "http://example.com/after-ping", 0, fakeDeadline)
			accPing := srv.Accepts()
			time.Sleep(fakeIdle + time.Second) // the client closes the idle connection
			synctest.Wait()
			afterIdle := getWithin(t, c.transport, "http://example.com/after-idle", 0, fakeDeadline)
			accIdle := srv.Accepts()
			st := c.transport.stats()
			t.Logf("accepts cold %d, warm %d, after the ping %d, after the idle close %d; PINGs while busy %d, while idle %d; stats %+v",
				accCold, accWarm, accPing, accIdle, pingsBusy, pingsIdle, st)
			if pingsIdle < 1 {
				t.Errorf("the client sent %d PINGs over %v of idle time, want at least 1: the SDK sets SendPingTimeout (%v) on the clone", pingsIdle, fakeSendPing+time.Second, fakeSendPing)
			}
			if accCold != 1 || accWarm != 1 || accPing != 1 || st.Leaders != 1 || st.FirstHolds < 1 {
				t.Errorf("accepts cold %d, warm %d, after the ping %d; stats %+v; want 1 connection throughout, 1 leader, a first hold", accCold, accWarm, accPing, st)
			}
			if afterPing.err != nil || afterIdle.err != nil || accIdle != 2 {
				t.Errorf("after the ping: %v; after the idle close: %v, accepts %d; want both ok and one re-dial after the idle close", afterPing.err, afterIdle.err, accIdle)
			}
		})
	})

	t.Run("success: 16 calls against a limit of 4 all succeed on 1 connection", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			const (
				limit   = 4
				calls   = 16
				service = 10 * time.Millisecond
				timeout = 2 * time.Second
			)
			var accepts atomic.Int64
			srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tm := time.NewTimer(service)
				defer tm.Stop()
				select {
				case <-tm.C:
					w.WriteHeader(http.StatusOK)
				case <-r.Context().Done():
				}
			}))
			var p http.Protocols
			p.SetUnencryptedHTTP2(true)
			srv.Config.Protocols = &p
			// The server's HTTP2 config must be set before Client starts the
			// in-memory network.
			srv.Config.HTTP2 = &http.HTTP2Config{MaxConcurrentStreams: limit}
			srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
				if st == http.StateNew {
					accepts.Add(1)
				}
			}
			c := fakeConfig(t, fakeClientTransport(t, srv))
			rs := fakeFan(t, c.transport, calls, "/", timeout)
			ok := 0
			for i, r := range rs {
				if r.err == nil && r.status == http.StatusOK {
					ok++
				} else {
					t.Errorf("call %d: %d %v", i, r.status, r.err)
				}
			}
			if ok != calls || accepts.Load() != 1 {
				t.Errorf("%d of %d calls ok, accepts %d; want all on 1 connection", ok, calls, accepts.Load())
			}
		})
	})

	t.Run("success: the caller's transport is cloned, not used", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			srv := testsupport.NewFakeH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
			base := fakeClientTransport(t, srv.Server)
			c := fakeConfig(t, base)
			if r := getWithin(t, c.transport, "http://example.com/clone", 0, fakeDeadline); r.err != nil || r.protoMajor != 2 {
				t.Fatalf("GET = HTTP/%d %v", r.protoMajor, r.err)
			}
			if base.MaxConnsPerHost != 0 || base.TLSHandshakeTimeout != 0 || base.HTTP2 != nil && base.HTTP2.StrictMaxConcurrentRequests {
				t.Errorf("the caller's transport changed: MaxConnsPerHost %d, TLSHandshakeTimeout %v, HTTP2 %+v", base.MaxConnsPerHost, base.TLSHandshakeTimeout, base.HTTP2)
			}
			ctx, cancel := context.WithTimeout(t.Context(), fakeDeadline)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com/direct", nil)
			if err != nil {
				t.Fatal(err)
			}
			// The caller's transport has its own pool: a request through it
			// dials a connection of its own.
			resp, err := base.RoundTrip(req)
			if err != nil {
				t.Fatalf("direct GET: %v", err)
			}
			_ = resp.Body.Close()
			if srv.Accepts() != 2 {
				t.Errorf("accepts %d, want 2: one for the clone, one for the caller's transport", srv.Accepts())
			}
			base.CloseIdleConnections()
		})
	})
}
