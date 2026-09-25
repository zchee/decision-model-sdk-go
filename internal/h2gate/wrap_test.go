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
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// countingTLS is a caller's metering wrapper around a *tls.Conn: its
// ConnectionState and HandshakeContext are the embedded conn's, and it
// counts the bytes the transport writes through it (after the handshake).
type countingTLS struct {
	*tls.Conn
	mu      sync.Mutex
	written int
}

// Write implements net.Conn.
func (c *countingTLS) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.mu.Lock()
	c.written += n
	c.mu.Unlock()
	return n, err
}

// Written returns the bytes written through the wrapper.
func (c *countingTLS) Written() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.written
}

// wrapped builds Wrap's transport over base for api, closing its idle
// connections when the test ends.
func wrapped(t *testing.T, base *http.Transport, api string, connect time.Duration) *Transport {
	t.Helper()
	tr, err := Wrap(base, Config{APIURL: mustURL(t, api), ConnectTimeout: connect})
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	t.Cleanup(tr.CloseIdleConnections)
	return tr
}

// tlsConfig returns a client configuration trusting the test certificate
// with the given ALPN offer.
func tlsConfig(t *testing.T, protos ...string) *tls.Config {
	t.Helper()
	c := testsupport.ClientTLSConfig(t)
	c.NextProtos = protos
	return c
}

// unfinished dials addr and returns a TLS client connection offering h2
// whose handshake has not run, as a caller's TLS dialer may.
func unfinished(ctx context.Context, t *testing.T, network, addr string) (net.Conn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	c := tlsConfig(t, "h2")
	c.ServerName, _, _ = net.SplitHostPort(addr)
	return tls.Client(raw, c), nil
}

// TestCallerDialTLSRefused covers the thin check Wrap puts around a caller's
// TLS dialer under HTTP2Only (section 6.3 mechanism (2)): a connection that
// negotiated HTTP/1.1, or that reports no TLS state, is closed with
// ErrNotNegotiated before the transport writes a byte; a metering wrapper
// that negotiated h2 is served as HTTP/2; a TLS-silent peer is refused after
// the connect + handshake bound whether the dialer handshakes itself or
// returns an unfinished handshake; another address passes unchecked; and a
// transport that installed its own h2 is refused at build.
func TestCallerDialTLSRefused(t *testing.T) {
	t.Run("error: an HTTP/1.1 connection is closed before any byte", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		var conns []*countingTLS
		var mu sync.Mutex
		base := &http.Transport{DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&tls.Dialer{Config: tlsConfig(t, "http/1.1")}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			cc := &countingTLS{Conn: c.(*tls.Conn)}
			mu.Lock()
			conns = append(conns, cc)
			mu.Unlock()
			return cc, nil
		}}
		tr := wrapped(t, base, srv.URL(), 0)
		r := get(t.Context(), tr, srv.URL()+"/")
		want := map[string]bool{"proxy": false, "timeout": false, "not_negotiated": true}
		if diff := gocmp.Diff(want, dialFlags(r.Err)); diff != "" {
			t.Errorf("classification of %s (-want +got):\n%s", chain(r.Err), diff)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(conns) != 1 || conns[0].Written() != 0 || len(srv.Requests()) != 0 {
			t.Errorf("dialed %d, written %v, server requests %d; want 1 connection, 0 bytes, no request", len(conns), conns, len(srv.Requests()))
		}
	})

	t.Run("error: the deprecated DialTLS is checked too", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		base := &http.Transport{}
		//lint:ignore SA1019 a caller may still set the deprecated field; the check must cover it
		base.DialTLS = func(network, addr string) (net.Conn, error) { //nolint:staticcheck // SA1019: see above
			return tls.Dial(network, addr, tlsConfig(t, "http/1.1"))
		}
		tr := wrapped(t, base, srv.URL(), 0)
		if r := get(t.Context(), tr, srv.URL()+"/"); !errors.Is(r.Err, ErrNotNegotiated) || len(srv.Requests()) != 0 {
			t.Errorf("error %s, server requests %d; want ErrNotNegotiated and no request", chain(r.Err), len(srv.Requests()))
		}
	})

	t.Run("error: a connection without TLS state is closed before any byte", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		var conn *countingConn
		base := &http.Transport{DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			conn = &countingConn{Conn: c}
			return conn, nil
		}}
		tr := wrapped(t, base, srv.URL(), 0)
		r := get(t.Context(), tr, srv.URL()+"/")
		if !errors.Is(r.Err, ErrNotNegotiated) {
			t.Errorf("error %s, want ErrNotNegotiated", chain(r.Err))
		}
		if conn == nil || conn.Written() != 0 || len(srv.Requests()) != 0 {
			t.Errorf("connection %v, server requests %d; want 0 bytes written, no request", conn, len(srv.Requests()))
		}
	})

	t.Run("success: a metering wrapper that negotiated h2 is served as HTTP/2", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		var conn *countingTLS
		base := &http.Transport{DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&tls.Dialer{Config: tlsConfig(t, "h2")}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			conn = &countingTLS{Conn: c.(*tls.Conn)}
			return conn, nil
		}}
		tr := wrapped(t, base, srv.URL(), 0)
		r := get(t.Context(), tr, srv.URL()+"/")
		if r.Err != nil || r.Status != http.StatusOK || r.ProtoMajor != 2 {
			t.Errorf("%d HTTP/%d %v, want 200 over HTTP/2", r.Status, r.ProtoMajor, r.Err)
		}
		if conn == nil || conn.Written() == 0 || tr.Stats().FirstHolds != 1 {
			t.Errorf("wrapper %v, stats %+v; want the request written through it and FirstHold engaged on it", conn, tr.Stats())
		}
	})

	t.Run("success: an unfinished handshake is completed by the check", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		base := &http.Transport{DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return unfinished(ctx, t, network, addr)
		}}
		tr := wrapped(t, base, srv.URL(), 0)
		r := get(t.Context(), tr, srv.URL()+"/")
		if r.Err != nil || r.ProtoMajor != 2 {
			t.Errorf("HTTP/%d %v, want HTTP/2", r.ProtoMajor, r.Err)
		}
	})

	for name, handshakes := range map[string]bool{"handshakes itself": true, "returns an unfinished handshake": false} {
		t.Run("error: a TLS-silent peer behind a dialer that "+name+" is refused after the bound", func(t *testing.T) {
			const connect = 250 * time.Millisecond // bound = connect + TLSHandshakeTimeout (= connect)
			l := testsupport.NewSilentListener(t)
			base := &http.Transport{DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if handshakes {
					return (&tls.Dialer{Config: tlsConfig(t, "h2")}).DialContext(ctx, network, addr)
				}
				return unfinished(ctx, t, network, addr)
			}}
			tr := wrapped(t, base, l.URL(), connect)
			start := time.Now()
			r := get(t.Context(), tr, l.URL()+"/")
			elapsed := time.Since(start)
			want := map[string]bool{"proxy": false, "timeout": true, "not_negotiated": false}
			if diff := gocmp.Diff(want, dialFlags(r.Err)); diff != "" {
				t.Errorf("classification of %s (-want +got):\n%s", chain(r.Err), diff)
			}
			if bound := 2 * connect; elapsed < bound || elapsed > bound+time.Second || l.Accepts() != 1 {
				t.Errorf("elapsed %v, accepts %d; want about the %v bound and 1 accept", elapsed, l.Accepts(), bound)
			}
		})
	}

	t.Run("success: another address, an https proxy hop, passes unchecked", func(t *testing.T) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
		dial := thinDialTLS(func(context.Context, string, string) (net.Conn, error) { return client, nil }, "example.com:443", time.Second)
		got, err := dial(t.Context(), "tcp", "127.0.0.1:3128")
		if err != nil || got != client {
			t.Errorf("dial of the proxy address: %v, %v; want the caller's connection untouched", got, err)
		}
		if _, err := dial(t.Context(), "tcp", "example.com:443"); !errors.Is(err, ErrNotNegotiated) {
			t.Errorf("dial of the API address with a pipe: %v, want ErrNotNegotiated", err)
		}
	})

	t.Run("error: a transport with its own TLSNextProto h2 is refused at build", func(t *testing.T) {
		base := &http.Transport{TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{
			"h2": func(string, *tls.Conn) http.RoundTripper { return nil },
		}}
		if _, err := Wrap(base, Config{APIURL: mustURL(t, exampleURL)}); !errors.Is(err, errCallerH2) {
			t.Errorf("Wrap under HTTP2Only: %v, want errCallerH2", err)
		}
		if _, err := Wrap(base, Config{APIURL: mustURL(t, exampleURL), Mode: HTTPAuto}); err != nil {
			t.Errorf("Wrap under HTTPAuto: %v, want no refusal", err)
		}
	})

	t.Run("success: a used http.DefaultTransport clone is accepted and serves HTTP/2", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		base := http.DefaultTransport.(*http.Transport).Clone()
		base.TLSClientConfig = testsupport.ClientTLSConfig(t)
		t.Cleanup(base.CloseIdleConnections)
		if r := get(t.Context(), base, srv.URL()+"/before"); r.Err != nil || r.ProtoMajor != 2 {
			t.Fatalf("the caller's own request: HTTP/%d %v", r.ProtoMajor, r.Err)
		}
		// The stock setup wrote its stub "h2" entry on first use; Clone does
		// not copy a map the caller never set.
		_, stub := base.TLSNextProto["h2"]
		tr := wrapped(t, base, srv.URL(), 0)
		r := get(t.Context(), tr, srv.URL()+"/after")
		if r.Err != nil || r.ProtoMajor != 2 || tr.scope != scopeEvery {
			t.Errorf("through Wrap: HTTP/%d %v, scope %v; want HTTP/2 and every handshake checked", r.ProtoMajor, r.Err, tr.scope)
		}
		record(t, "case", "used-default-transport", "stub_h2_on_original", stub)
	})
}

// TestWrap checks the clone rule of WithHTTPTransport: zero fields are set,
// the caller's values are kept, the caller's transport is not changed, and
// the caller's VerifyConnection runs before the ALPN check.
func TestWrap(t *testing.T) {
	t.Run("success: zero fields are set on the clone only", func(t *testing.T) {
		base := &http.Transport{}
		tr := wrapped(t, base, exampleURL, 3*time.Second)
		c := tr.base
		if c == base {
			t.Fatal("Wrap used the caller's transport, want a clone")
		}
		if !c.Protocols.HTTP2() || c.Protocols.HTTP1() || c.MaxConnsPerHost != 1 || c.HTTP2 == nil || !c.HTTP2.StrictMaxConcurrentRequests ||
			c.TLSHandshakeTimeout != 3*time.Second || c.TLSClientConfig == nil || c.TLSClientConfig.VerifyConnection == nil {
			t.Errorf("clone: Protocols %v, MaxConnsPerHost %d, HTTP2 %+v, TLSHandshakeTimeout %v, TLS %v",
				c.Protocols, c.MaxConnsPerHost, c.HTTP2, c.TLSHandshakeTimeout, c.TLSClientConfig)
		}
		// Clone ran the stock first-use setup on base, which may give it an
		// empty HTTP2Config and TLS configuration; nothing of Wrap's own.
		if base.Protocols != nil || base.MaxConnsPerHost != 0 || base.TLSHandshakeTimeout != 0 ||
			(base.HTTP2 != nil && base.HTTP2.StrictMaxConcurrentRequests) ||
			(base.TLSClientConfig != nil && base.TLSClientConfig.VerifyConnection != nil) {
			t.Errorf("the caller's transport carries Wrap's settings: Protocols %v, MaxConnsPerHost %d, TLSHandshakeTimeout %v, HTTP2 %+v",
				base.Protocols, base.MaxConnsPerHost, base.TLSHandshakeTimeout, base.HTTP2)
		}
		if tr.holdBound != 6*time.Second || tr.waitBound != 6*time.Second {
			t.Errorf("hold bound %v, wait bound %v; want 6 s each (connect + handshake)", tr.holdBound, tr.waitBound)
		}
	})

	t.Run("success: the caller's values are kept", func(t *testing.T) {
		var p http.Protocols
		p.SetHTTP2(true)
		h2 := &http.HTTP2Config{MaxReceiveBufferPerStream: 1 << 20}
		base := &http.Transport{Protocols: &p, MaxConnsPerHost: 4, HTTP2: h2, TLSHandshakeTimeout: 7 * time.Second}
		tr := wrapped(t, base, exampleURL, time.Second)
		c := tr.base
		if c.MaxConnsPerHost != 4 || c.HTTP2.MaxReceiveBufferPerStream != 1<<20 || c.TLSHandshakeTimeout != 7*time.Second {
			t.Errorf("clone: MaxConnsPerHost %d, HTTP2 %+v, TLSHandshakeTimeout %v; want the caller's", c.MaxConnsPerHost, c.HTTP2, c.TLSHandshakeTimeout)
		}
		if tr.holdBound != 8*time.Second {
			t.Errorf("hold bound %v, want connect + the caller's handshake timeout", tr.holdBound)
		}
	})

	t.Run("success: an empty HTTP2Config left by the stock setup counts as unset", func(t *testing.T) {
		used := &http.Transport{}
		_ = used.Clone() // the stock first-use setup stores an empty HTTP2Config on used
		if used.HTTP2 == nil {
			t.Skip("the stock setup no longer stores an HTTP2Config; the rule is moot")
		}
		tr := wrapped(t, used, exampleURL, 0)
		if !tr.base.HTTP2.StrictMaxConcurrentRequests {
			t.Errorf("clone HTTP2 %+v, want the strict settings", tr.base.HTTP2)
		}
	})

	t.Run("success: HTTPAuto keeps the connection cap at zero and checks nothing", func(t *testing.T) {
		auto, err := Wrap(&http.Transport{}, Config{APIURL: mustURL(t, exampleURL), Mode: HTTPAuto})
		if err != nil {
			t.Fatal(err)
		}
		c := auto.base
		if c.MaxConnsPerHost != 0 || !c.Protocols.HTTP1() || !c.Protocols.HTTP2() || (c.TLSClientConfig != nil && c.TLSClientConfig.VerifyConnection != nil) {
			t.Errorf("HTTPAuto clone: MaxConnsPerHost %d, Protocols %v, TLS %v", c.MaxConnsPerHost, c.Protocols, c.TLSClientConfig)
		}
	})

	t.Run("error: the caller's VerifyConnection runs first and its refusal wins", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: testsupport.ALPNNone})
		errPinned := errors.New("certificate not pinned")
		calls := 0
		base := &http.Transport{TLSClientConfig: testsupport.ClientTLSConfig(t)}
		base.TLSClientConfig.VerifyConnection = func(tls.ConnectionState) error {
			calls++
			return errPinned
		}
		tr := wrapped(t, base, srv.URL(), 0)
		r := get(t.Context(), tr, srv.URL()+"/")
		if !errors.Is(r.Err, errPinned) || errors.Is(r.Err, ErrNotNegotiated) || calls != 1 {
			t.Errorf("error %s after %d caller calls, want the caller's refusal alone", chain(r.Err), calls)
		}
	})

	t.Run("error: Wrap refuses the default transport's options", func(t *testing.T) {
		for name, cfg := range map[string]Config{
			"RootCAs":     {RootCAs: testsupport.RootCAs(t)},
			"TLSConfig":   {TLSConfig: &tls.Config{}},
			"Proxy":       {Proxy: http.ProxyFromEnvironment},
			"DialContext": {DialContext: (&net.Dialer{}).DialContext},
		} {
			cfg.APIURL = mustURL(t, exampleURL)
			if _, err := Wrap(&http.Transport{}, cfg); !errors.Is(err, errWrapConfig) {
				t.Errorf("%s: %v, want errWrapConfig", name, err)
			}
		}
		if _, err := Wrap(nil, Config{APIURL: mustURL(t, exampleURL)}); err == nil {
			t.Error("Wrap(nil) = nil error")
		}
	})
}

// TestNewTransport checks the default factory's build: the section 6.3
// settings, the refusals, and the mode's protocols.
func TestNewTransport(t *testing.T) {
	t.Run("success: the section 6.3 settings", func(t *testing.T) {
		pool := testsupport.RootCAs(t)
		low := &tls.Config{MinVersion: tls.VersionTLS10} //nolint:gosec // G402: the low floor is what NewTransport must raise
		tr, err := NewTransport(Config{APIURL: mustURL(t, exampleURL), RootCAs: pool, TLSConfig: low})
		if err != nil {
			t.Fatal(err)
		}
		b := tr.base
		if !b.Protocols.HTTP2() || b.Protocols.HTTP1() || b.MaxConnsPerHost != 1 || b.IdleConnTimeout != idleConnTimeout ||
			b.TLSHandshakeTimeout != DefaultConnectTimeout || b.Proxy != nil || b.DialContext == nil || b.DialTLSContext != nil {
			t.Errorf("transport %+v", b)
		}
		if diff := gocmp.Diff(http.HTTP2Config{StrictMaxConcurrentRequests: true, SendPingTimeout: sendPingTimeout, PingTimeout: pingTimeout}, *b.HTTP2); diff != "" {
			t.Errorf("HTTP2 (-want +got):\n%s", diff)
		}
		if c := b.TLSClientConfig; c.MinVersion != tls.VersionTLS12 || c.RootCAs != pool || c.VerifyConnection == nil {
			t.Errorf("TLS: MinVersion %#x, RootCAs %p, VerifyConnection set %t", c.MinVersion, c.RootCAs, c.VerifyConnection != nil)
		}
		if tr.holdBound != 2*DefaultConnectTimeout || tr.waitBound != 2*DefaultConnectTimeout || !tr.firstHold {
			t.Errorf("hold %v, wait %v, FirstHold %t; want 20 s, 20 s, on", tr.holdBound, tr.waitBound, tr.firstHold)
		}
	})

	t.Run("success: the mode's protocols", func(t *testing.T) {
		tests := map[string]struct {
			api              string
			mode             Mode
			h1, h2, h2c, h2m bool // HTTP1, HTTP2, UnencryptedHTTP2, the Transport's h2c flag
			conns            int
		}{
			"https HTTP2Only": {api: "https://example.com", mode: HTTP2Only, h2: true, conns: 1},
			"http HTTP2Only":  {api: "http://example.com", mode: HTTP2Only, h2c: true, h2m: true, conns: 1},
			"https HTTPAuto":  {api: "https://example.com", mode: HTTPAuto, h1: true, h2: true},
			"http HTTPAuto":   {api: "http://example.com", mode: HTTPAuto, h1: true, h2: true},
		}
		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				tr, err := NewTransport(Config{APIURL: mustURL(t, tt.api), Mode: tt.mode})
				if err != nil {
					t.Fatal(err)
				}
				p := tr.base.Protocols
				got := []bool{p.HTTP1(), p.HTTP2(), p.UnencryptedHTTP2(), tr.h2c}
				if diff := gocmp.Diff([]bool{tt.h1, tt.h2, tt.h2c, tt.h2m}, got); diff != "" {
					t.Errorf("HTTP1, HTTP2, UnencryptedHTTP2, h2c (-want +got):\n%s", diff)
				}
				if tr.base.MaxConnsPerHost != tt.conns {
					t.Errorf("MaxConnsPerHost %d, want %d", tr.base.MaxConnsPerHost, tt.conns)
				}
			})
		}
	})

	t.Run("error: refusals at build", func(t *testing.T) {
		tests := map[string]struct {
			cfg  Config
			want error
		}{
			"no URL":                    {cfg: Config{}, want: errBadURL},
			"ftp scheme":                {cfg: Config{APIURL: mustURL(t, "ftp://example.com")}, want: errBadURL},
			"no host":                   {cfg: Config{APIURL: mustURL(t, "https:///v1")}, want: errBadURL},
			"non-ASCII host, HTTP2Only": {cfg: Config{APIURL: mustURL(t, "https://bücher.example")}, want: errNonASCIIHost},
			"non-ASCII host over http":  {cfg: Config{APIURL: mustURL(t, "http://bücher.example")}, want: errNonASCIIHost},
		}
		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				if _, err := NewTransport(tt.cfg); !errors.Is(err, tt.want) {
					t.Errorf("NewTransport: %v, want %v", err, tt.want)
				}
			})
		}
		if _, err := NewTransport(Config{APIURL: mustURL(t, "https://example.com"), Mode: Mode(7)}); err == nil {
			t.Error("unknown mode accepted")
		}
		if _, err := NewTransport(Config{APIURL: mustURL(t, "https://bücher.example"), Mode: HTTPAuto}); err != nil {
			t.Errorf("non-ASCII host under HTTPAuto: %v, want it accepted", err)
		}
	})

	t.Run("success: the dial is bounded by the connect timeout", func(t *testing.T) {
		const connect = 100 * time.Millisecond
		block := make(chan struct{})
		t.Cleanup(func() { close(block) })
		gd := testsupport.NewGatedDialer(block, nil)
		tr := newTestTransport(t, Config{APIURL: mustURL(t, "https://127.0.0.1:1"), ConnectTimeout: connect, DialContext: gd.DialContext})
		start := time.Now()
		r := get(t.Context(), tr, "https://127.0.0.1:1/")
		if el := time.Since(start); !errors.Is(r.Err, context.DeadlineExceeded) || !dialFlags(r.Err)["timeout"] || el > connect+time.Second {
			t.Errorf("after %v: %s, want a timeout *DialError after about %v", el, chain(r.Err), connect)
		}
	})

	t.Run("success: Mode names", func(t *testing.T) {
		got := []string{HTTP2Only.String(), HTTPAuto.String(), Mode(9).String(), stateCold.String(), stateDialing.String(), stateWarm.String()}
		want := []string{"HTTP2Only", "HTTPAuto", "Mode(9)", "cold", "dialing", "warm"}
		if diff := gocmp.Diff(want, got); diff != "" {
			t.Errorf("names (-want +got):\n%s", diff)
		}
	})
}
