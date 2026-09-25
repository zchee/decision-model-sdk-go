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

package h2gate

import (
	"context"
	"fmt"
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

// The fake-network tests (S-T2) run the transport over an h2c server on the
// in-memory network inside a testing/synctest bubble, so its timers (ping,
// idle, the gate's bounds) run on fake time. Every call carries a deadline:
// a strict-mode stall is a durable block (cc.cond.Wait,
// internal/http2/transport.go:1555), and with pings on, a stall without a
// deadline spins until the test binary's timeout instead of failing (W0.4).
//
// The fake client transport's DialContext targets the in-memory listener
// (httptest/server.go:439-444), so NewTransport cannot build it; the tests
// wrap it directly, as the W0.4 spike did. W2.2 Part B re-runs them through
// the root package's WithHTTPTransport.

// fakeDeadline bounds every call on fake time.
const fakeDeadline = 2 * time.Minute

// fakeTransport sets the section 6.3 HTTP/2 settings on a fake server's
// client transport and wraps it with the default bounds (20 s each).
func fakeTransport(t *testing.T, rt http.RoundTripper) *Transport {
	t.Helper()
	base, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("fake client transport is %T", rt)
	}
	if p := base.Protocols; p == nil || !p.UnencryptedHTTP2() || p.HTTP1() || p.HTTP2() {
		t.Fatalf("fake client Protocols %v, want unencrypted HTTP/2 only", p)
	}
	base.MaxConnsPerHost = 1
	base.IdleConnTimeout = idleConnTimeout
	base.HTTP2 = strictHTTP2()
	bound := 2 * DefaultConnectTimeout
	return newTransport(base, settings{mode: HTTP2Only, waitBound: bound, holdBound: bound})
}

// fakeGet sends a GET for http://example.com+path with the fake deadline.
func fakeGet(t *testing.T, tr http.RoundTripper, path string) result {
	ctx, cancel := context.WithTimeout(t.Context(), fakeDeadline)
	defer cancel()
	return get(ctx, tr, "http://example.com"+path)
}

// limitedFake is a fake-network h2c server advertising limit concurrent
// streams whose handler takes service on fake time. The server's HTTP2
// config must be set before Client starts the in-memory network.
func limitedFake(t *testing.T, limit int, service time.Duration) (*httptest.Server, *atomic.Int64) {
	var accepts atomic.Int64
	srv := httptest.NewTestServer(t, serviceHandler(service))
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = &p
	srv.Config.HTTP2 = &http.HTTP2Config{MaxConcurrentStreams: limit}
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			accepts.Add(1)
		}
	}
	base, ok := srv.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("fake client transport is %T", srv.Client().Transport)
	}
	var cp http.Protocols
	cp.SetUnencryptedHTTP2(true)
	base.Protocols = &cp
	return srv, &accepts
}

// TestSynctestFanOut is S-T2 as a test: the cold fan-out, the warm burst,
// the ping timer and the idle timer on fake time, and F1's 16 calls against
// a limit of 4, which strict mode alone stalls and the token completes.
func TestSynctestFanOut(t *testing.T) {
	t.Run("success: cold 64 on 1 connection, warm 64 on none, then the ping and idle timers", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			srv := testsupport.NewFakeH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
			tr := fakeTransport(t, srv.Client().Transport)
			cold := fanOut(fanN, func(i int) result { return fakeGet(t, tr, "/cold/"+strconv.Itoa(i)) })
			accCold := srv.Accepts()
			warm := fanOut(fanN, func(i int) result { return fakeGet(t, tr, "/warm/"+strconv.Itoa(i)) })
			accWarm := srv.Accepts()
			for i, r := range append(cold, warm...) {
				if r.Err != nil || r.Status != http.StatusOK || r.ProtoMajor != 2 {
					t.Errorf("call %d: %d HTTP/%d %v", i, r.Status, r.ProtoMajor, r.Err)
				}
			}
			time.Sleep(sendPingTimeout + time.Second) // the client pings an idle connection; the server answers
			synctest.Wait()
			afterPing := fakeGet(t, tr, "/after-ping")
			accPing := srv.Accepts()
			time.Sleep(idleConnTimeout + time.Second) // the client closes the idle connection
			synctest.Wait()
			afterIdle := fakeGet(t, tr, "/after-idle")
			accIdle := srv.Accepts()
			st := tr.Stats()
			record(t, "case", "st2-fanout+timers", "accepts_cold", accCold, "accepts_warm", accWarm, "accepts_after_ping", accPing,
				"accepts_after_idle", accIdle, "stats", fmt.Sprintf("%+v", st))
			if accCold != 1 || accWarm != 1 || accPing != 1 || st.Leaders != 1 || st.FirstHolds < 1 {
				t.Errorf("accepts cold %d, warm %d, after the ping %d; stats %+v; want 1 connection throughout, 1 leader", accCold, accWarm, accPing, st)
			}
			if afterPing.Err != nil || afterIdle.Err != nil || accIdle > 2 {
				t.Errorf("after the ping: %v; after the idle close: %v, accepts %d; want both ok and at most one re-dial", afterPing.Err, afterIdle.Err, accIdle)
			}
		})
	})

	t.Run("success: 16 calls against a limit of 4 all succeed on 1 connection", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			srv, accepts := limitedFake(t, 4, f1Service)
			tr := fakeTransport(t, srv.Client().Transport)
			calls := fanOut(16, func(i int) result {
				ctx, cancel := context.WithTimeout(t.Context(), f1Timeout)
				defer cancel()
				return get(ctx, tr, "http://example.com/"+strconv.Itoa(i))
			})
			if cl := classes(calls); cl["ok"] != 16 || accepts.Load() != 1 {
				t.Errorf("classes %v (first error %v), accepts %d; want 16 ok on 1 connection", cl, firstErr(calls), accepts.Load())
			}
		})
	})
}

// testHoldBoundOnFakeTime is TestWaiterFallThrough's case at the default
// bounds: the first response hangs 60 s with the connection kept alive by
// pings (the server answers them), and the waiters go out when the 20 s hold
// bound ends, on the same connection, while the leader is still waiting.
func testHoldBoundOnFakeTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const hang = time.Minute
		var mu sync.Mutex
		held := ""
		srv := testsupport.NewFakeH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			first := held == ""
			if first {
				held = r.URL.Path
			}
			mu.Unlock()
			if first {
				tm := time.NewTimer(hang)
				defer tm.Stop()
				select {
				case <-tm.C:
				case <-r.Context().Done():
					return
				}
			}
			w.WriteHeader(http.StatusOK)
		}))
		tr := fakeTransport(t, srv.Client().Transport)
		start := time.Now()
		calls := fanOut(fanN, func(i int) result { return fakeGet(t, tr, "/hold/"+strconv.Itoa(i)) })
		leader := -1
		for i, r := range calls {
			if r.Err != nil || r.Status != http.StatusOK {
				t.Fatalf("call %d: %d %v", i, r.Status, r.Err)
			}
			if !r.Reused {
				leader = i
			}
		}
		if leader < 0 {
			t.Fatal("no leader")
		}
		l := calls[leader]
		for i, r := range calls {
			if i == leader {
				continue
			}
			if gap := r.WroteHeaders.Sub(l.WroteHeaders); gap < tr.holdBound || gap > tr.holdBound+time.Second {
				t.Errorf("call %d wrote HEADERS %v after the leader, want the %v hold bound", i, gap, tr.holdBound)
				break
			}
		}
		st := tr.Stats()
		if got := l.Done.Sub(start); got < hang || srv.Accepts() != 1 || st.HoldExpiries != 1 {
			t.Errorf("leader done after %v, accepts %d, stats %+v; want at least %v, 1 connection, 1 hold expiry", got, srv.Accepts(), st, hang)
		}
	})
}
