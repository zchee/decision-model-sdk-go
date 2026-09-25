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
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// The proxy tests use example.com as the API host (the test certificate
// covers it): the proxy's Routes resolve it to the loopback server, and the
// client dials only the proxy, a loopback literal. The proxy is selected
// with http.ProxyURL, because ProxyFromEnvironment never proxies loopback
// targets; the two hops then have different effective SNI (plan W2.2).

// exampleURL is the API URL of the proxy tests.
const exampleURL = "https://example.com"

// answerExample answers 200 "h2 example.com" to an HTTP/2 request for
// example.com and 400 otherwise.
func answerExample(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor != 2 || r.Host != "example.com" {
		http.Error(w, "not an HTTP/2 request for example.com", http.StatusBadRequest)
		return
	}
	_, _ = io.WriteString(w, "h2 example.com")
}

// hops records the ConnectionState of every TLS handshake the client ran,
// through the caller VerifyConnection hook the ALPN check runs after.
type hops struct {
	mu sync.Mutex
	cs []tls.ConnectionState
}

// verify is a VerifyConnection hook that records and accepts.
func (h *hops) verify(cs tls.ConnectionState) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cs = append(h.cs, cs)
	return nil
}

// seen returns the ServerName and NegotiatedProtocol of every handshake.
func (h *hops) seen() [][2]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out [][2]string
	for _, cs := range h.cs {
		out = append(out, [2]string{cs.ServerName, cs.NegotiatedProtocol})
	}
	return out
}

// proxiedTransport builds the default transport for api through proxy p,
// recording every handshake in h.
func proxiedTransport(t *testing.T, api string, p *testsupport.Proxy, h *hops, log Logger) *Transport {
	t.Helper()
	return newTestTransport(t, Config{
		APIURL:      mustURL(t, api),
		Proxy:       http.ProxyURL(p.URL()),
		DialContext: testsupport.Routes{}.DialContext, // loopback literals only: the proxy
		TLSConfig:   &tls.Config{VerifyConnection: h.verify},
		Logger:      log,
	})
}

// TestProxy covers CONNECT through a plain HTTP/1.1 proxy (S-T5) and the
// build-time ALPN scope of section 6.3: a caller Proxy func may apply a
// proxy, so the check recognises the API hop by its SNI; the handshake the
// check sees through a plain proxy is the API hop's alone.
func TestProxy(t *testing.T) {
	t.Run("success: a 16-way cold burst through a plain proxy: 1 CONNECT, 1 connection, the API hop checked", func(t *testing.T) {
		const n = 16
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(answerExample)})
		p := testsupport.NewProxy(t, testsupport.ProxyPlain, testsupport.Routes{"example.com:443": srv.Addr()})
		var h hops
		tr := proxiedTransport(t, exampleURL, p, &h, nil)
		if tr.scope != scopeSNI {
			t.Fatalf("scope %v, want sni", tr.scope)
		}
		calls := fanOut(n, func(i int) result { return get(t.Context(), tr, exampleURL+"/"+strconv.Itoa(i)) })
		for i, r := range calls {
			if r.Err != nil || r.Status != http.StatusOK || r.Body != "h2 example.com" {
				t.Errorf("call %d: %d %q %v", i, r.Status, r.Body, r.Err)
			}
		}
		if diff := gocmp.Diff([]testsupport.ProxyConnect{{Conn: 0, Target: "example.com:443", Status: http.StatusOK}}, p.Connects()); diff != "" {
			t.Errorf("CONNECTs (-want +got):\n%s", diff)
		}
		if diff := gocmp.Diff([][2]string{{"example.com", "h2"}}, h.seen()); diff != "" {
			t.Errorf("handshakes (-want +got):\n%s", diff)
		}
		if srv.Accepts() != 1 || p.Accepts() != 1 {
			t.Errorf("server accepts %d, proxy accepts %d; want 1 and 1", srv.Accepts(), p.Accepts())
		}
		want := 2*DefaultConnectTimeout + proxyConnectLimit + DefaultConnectTimeout
		if tr.waitBound != want || tr.holdBound != 2*DefaultConnectTimeout {
			t.Errorf("wait bound %v, hold bound %v; want %v (with the CONNECT limit and a second handshake) and %v",
				tr.waitBound, tr.holdBound, want, 2*DefaultConnectTimeout)
		}
	})

	t.Run("error: an API host without h2 behind the proxy is refused on the API hop", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: testsupport.ALPNNone, Handler: http.HandlerFunc(answerExample)})
		p := testsupport.NewProxy(t, testsupport.ProxyPlain, testsupport.Routes{"example.com:443": srv.Addr()})
		var h hops
		tr := proxiedTransport(t, exampleURL, p, &h, nil)
		r := get(t.Context(), tr, exampleURL+"/")
		want := map[string]bool{"proxy": false, "timeout": false, "not_negotiated": true}
		if diff := gocmp.Diff(want, dialFlags(r.Err)); diff != "" {
			t.Errorf("classification of %v (-want +got):\n%s", r.Err, diff)
		}
		if len(srv.Requests()) != 0 || len(p.Connects()) != 1 {
			t.Errorf("server requests %d, CONNECTs %d; want 0 and 1", len(srv.Requests()), len(p.Connects()))
		}
	})

	t.Run("error: an unreachable proxy is a proxy failure", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		dead := &url.URL{Scheme: "http", Host: ln.Addr().String()}
		_ = ln.Close()
		tr := newTestTransport(t, Config{APIURL: mustURL(t, exampleURL), Proxy: http.ProxyURL(dead), DialContext: testsupport.Routes{}.DialContext})
		r := get(t.Context(), tr, exampleURL+"/")
		want := map[string]bool{"proxy": true, "timeout": false, "not_negotiated": false}
		if diff := gocmp.Diff(want, dialFlags(r.Err)); diff != "" {
			t.Errorf("classification of %s (-want +got):\n%s", chain(r.Err), diff)
		}
		if oe, ok := errors.AsType[*net.OpError](r.Err); !ok || oe.Op != "proxyconnect" {
			t.Errorf("chain %s, want a proxyconnect *net.OpError outermost", chain(r.Err))
		}
	})

	t.Run("error: a CONNECT the proxy refuses is a plain dial failure", func(t *testing.T) {
		// The stock transport returns the status text of a failed CONNECT
		// without the proxyconnect wrapper (transport.go:2036-2043), so the
		// classification cannot tell it from a failure past the proxy. The
		// same holds for a caller TLS dialer's unfinished handshake with an
		// https proxy, which customDialTLS completes and returns unwrapped
		// (:1905-1910); W7 records both under K16.
		p := testsupport.NewProxy(t, testsupport.ProxyPlain, testsupport.Routes{})
		var h hops
		tr := proxiedTransport(t, exampleURL, p, &h, nil)
		r := get(t.Context(), tr, exampleURL+"/")
		want := map[string]bool{"proxy": false, "timeout": false, "not_negotiated": false}
		if diff := gocmp.Diff(want, dialFlags(r.Err)); diff != "" {
			t.Errorf("classification of %s (-want +got):\n%s", chain(r.Err), diff)
		}
		if diff := gocmp.Diff([]testsupport.ProxyConnect{{Conn: 0, Target: "example.com:443", Status: http.StatusBadGateway}}, p.Connects()); diff != "" {
			t.Errorf("CONNECTs (-want +got):\n%s", diff)
		}
	})

	t.Run("success: the build-time ALPN scope", func(t *testing.T) {
		api := mustURL(t, exampleURL)
		ipAPI := mustURL(t, "https://127.0.0.1:8443")
		byURL := http.ProxyURL(&url.URL{Scheme: "http", Host: "127.0.0.1:1"})
		//nolint:gocritic // unlambda: the closure is the point; it is another func than ProxyFromEnvironment
		closure := func(r *http.Request) (*url.URL, error) { return http.ProxyFromEnvironment(r) }
		// ProxyFromEnvironment reads the environment once per process, so
		// the expectation asks it rather than assuming an empty environment.
		envProxy, err := http.ProxyFromEnvironment(&http.Request{URL: api, Header: http.Header{}})
		if err != nil {
			t.Fatal(err)
		}
		envScope := scopeEvery
		if envProxy != nil {
			envScope = scopeSNI
		}
		tests := map[string]struct {
			cfg  Config
			want alpnScope
		}{
			"nil proxy: every handshake":             {cfg: Config{APIURL: api}, want: scopeEvery},
			"ProxyFromEnvironment: decided at build": {cfg: Config{APIURL: api, Proxy: http.ProxyFromEnvironment}, want: envScope},
			"ProxyURL: the SNI rule":                 {cfg: Config{APIURL: api, Proxy: byURL}, want: scopeSNI},
			"a closure calling ProxyFromEnvironment": {cfg: Config{APIURL: api, Proxy: closure}, want: scopeSNI},
			"IP-literal API host behind a proxy": {
				cfg: Config{APIURL: ipAPI, Proxy: byURL}, want: scopePostCheck,
			},
			"ServerName override behind a proxy": {
				cfg: Config{APIURL: api, Proxy: byURL, TLSConfig: &tls.Config{ServerName: "example.com"}}, want: scopePostCheck,
			},
			"IP-literal API host without a proxy": {cfg: Config{APIURL: ipAPI}, want: scopeEvery},
			"HTTPAuto checks nothing":             {cfg: Config{APIURL: api, Mode: HTTPAuto}, want: scopeNone},
			"http checks nothing":                 {cfg: Config{APIURL: mustURL(t, "http://example.com")}, want: scopeNone},
		}
		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				tr, err := NewTransport(tt.cfg)
				if err != nil {
					t.Fatal(err)
				}
				if tr.scope != tt.want {
					t.Errorf("scope %v, want %v", tr.scope, tt.want)
				}
				hooked := tr.base.TLSClientConfig.VerifyConnection != nil
				if wantHook := tt.want == scopeEvery || tt.want == scopeSNI; hooked != wantHook {
					t.Errorf("VerifyConnection installed %t, want %t", hooked, wantHook)
				}
			})
		}
	})

	t.Run("success: ProxyFromEnvironment is recognised by function identity only", func(t *testing.T) {
		clone := (&http.Transport{Proxy: http.ProxyFromEnvironment}).Clone()
		held := http.ProxyFromEnvironment
		//nolint:gocritic // unlambda: the closure is the point; it is another func than ProxyFromEnvironment
		closure := func(r *http.Request) (*url.URL, error) { return http.ProxyFromEnvironment(r) }
		got := map[string]bool{
			"ProxyFromEnvironment":    isProxyFromEnvironment(http.ProxyFromEnvironment),
			"DefaultTransport.Proxy":  isProxyFromEnvironment(http.DefaultTransport.(*http.Transport).Proxy),
			"Transport.Clone().Proxy": isProxyFromEnvironment(clone.Proxy),
			"a func variable":         isProxyFromEnvironment(held),
			"ProxyURL":                isProxyFromEnvironment(http.ProxyURL(&url.URL{Host: "127.0.0.1:1"})),
			"a closure calling it":    isProxyFromEnvironment(closure),
			"nil":                     isProxyFromEnvironment(nil),
		}
		want := map[string]bool{
			"ProxyFromEnvironment": true, "DefaultTransport.Proxy": true, "Transport.Clone().Proxy": true, "a func variable": true,
			"ProxyURL": false, "a closure calling it": false, "nil": false,
		}
		if diff := gocmp.Diff(want, got); diff != "" {
			t.Errorf("identity (-want +got):\n%s", diff)
		}
	})

	t.Run("success: the SNI rule compares the SNI form, which an ECH handshake does not report", func(t *testing.T) {
		// With ECH accepted, ConnectionState.ServerName is the configured
		// name as it is (crypto/tls/handshake_client_tls13.go:102,277), so a
		// trailing-dot API host would otherwise skip the check.
		check := alpnCheck(scopeSNI, eff("", "example.com."), nil)
		if err := check(tls.ConnectionState{ServerName: "example.com.", NegotiatedProtocol: "http/1.1"}); !errors.Is(err, ErrNotNegotiated) {
			t.Errorf("API hop reported as %q: %v, want ErrNotNegotiated", "example.com.", err)
		}
		if err := check(tls.ConnectionState{ServerName: "example.com", NegotiatedProtocol: "h2"}); err != nil {
			t.Errorf("API hop with h2: %v", err)
		}
		if err := check(tls.ConnectionState{}); err != nil {
			t.Errorf("proxy hop (no SNI): %v, want it unchecked", err)
		}
	})

	t.Run("success: eff is crypto/tls's SNI form", func(t *testing.T) {
		got := map[string]string{
			"example.com":           eff("", "example.com"),
			"example.com.":          eff("", "example.com."),
			"127.0.0.1":             eff("", "127.0.0.1"),
			"[::1]":                 eff("", "[::1]"),
			"::1":                   eff("", "::1"),
			"fe80::1%en0":           eff("", "fe80::1%en0"),
			"override over literal": eff("api.internal", "127.0.0.1"),
		}
		want := map[string]string{
			"example.com": "example.com", "example.com.": "example.com", "127.0.0.1": "", "[::1]": "", "::1": "",
			"fe80::1%en0": "", "override over literal": "api.internal",
		}
		if diff := gocmp.Diff(want, got); diff != "" {
			t.Errorf("eff (-want +got):\n%s", diff)
		}
	})
}

// TestProxyTLS covers CONNECT through a TLS proxy (S-T5b): the proxy hop's
// handshake is not checked and the API hop's is; a strict proxy's alert 120
// is a proxy failure (proxyconnect first, R20); a proxy that negotiates h2
// on its own hop is recorded (K16); and where the hops cannot be told apart
// by SNI the response's protocol is checked after the fact.
func TestProxyTLS(t *testing.T) {
	t.Run("success: lenient proxy: the proxy hop is not checked, the API hop is", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(answerExample)})
		p := testsupport.NewProxy(t, testsupport.ProxyTLSLenient, testsupport.Routes{"example.com:443": srv.Addr()})
		var h hops
		tr := proxiedTransport(t, exampleURL, p, &h, nil)
		r := get(t.Context(), tr, exampleURL+"/")
		if r.Err != nil || r.Status != http.StatusOK || r.Body != "h2 example.com" {
			t.Errorf("%d %q %v, want 200 h2 example.com", r.Status, r.Body, r.Err)
		}
		if diff := gocmp.Diff([][2]string{{"", ""}, {"example.com", "h2"}}, h.seen()); diff != "" {
			t.Errorf("handshakes, proxy hop first (-want +got):\n%s", diff)
		}
	})

	t.Run("error: strict proxy refuses h2 with alert 120: a proxy failure, not a negotiation failure", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(answerExample)})
		p := testsupport.NewProxy(t, testsupport.ProxyTLSStrict, testsupport.Routes{"example.com:443": srv.Addr()})
		var h hops
		tr := proxiedTransport(t, exampleURL, p, &h, nil)
		r := get(t.Context(), tr, exampleURL+"/")
		want := map[string]bool{"proxy": true, "timeout": false, "not_negotiated": false}
		if diff := gocmp.Diff(want, dialFlags(r.Err)); diff != "" {
			t.Errorf("classification of %s (-want +got):\n%s", chain(r.Err), diff)
		}
		if oe, ok := errors.AsType[*net.OpError](r.Err); !ok || oe.Op != "proxyconnect" {
			t.Errorf("chain %s, want a proxyconnect *net.OpError outermost", chain(r.Err))
		}
		if len(p.Connects()) != 0 || srv.Accepts() != 0 || len(h.seen()) != 0 {
			t.Errorf("CONNECTs %d, server accepts %d, handshakes %v; want none", len(p.Connects()), srv.Accepts(), h.seen())
		}
	})

	t.Run("success: a proxy offering h2 on its own hop (K16)", func(t *testing.T) {
		// The stock transport writes an HTTP/1.1 CONNECT whatever the proxy
		// hop negotiated (transport.go:1985); this proxy reads HTTP/1.1 in
		// any case, so the call succeeds. A proxy that speaks h2 after
		// negotiating it would fail: not supported (Appendix B).
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(answerExample)})
		p := testsupport.NewProxy(t, testsupport.ProxyTLSOfferH2, testsupport.Routes{"example.com:443": srv.Addr()})
		var h hops
		tr := proxiedTransport(t, exampleURL, p, &h, nil)
		r := get(t.Context(), tr, exampleURL+"/")
		if r.Err != nil || r.Status != http.StatusOK {
			t.Errorf("%d %v, want 200", r.Status, r.Err)
		}
		if diff := gocmp.Diff([][2]string{{"", "h2"}, {"example.com", "h2"}}, h.seen()); diff != "" {
			t.Errorf("handshakes (-want +got):\n%s", diff)
		}
	})

	// The ambiguous cases: behind a proxy, an IP-literal API host has an
	// empty effective SNI like the IP-literal proxy, and a ServerName
	// override applies to both hops. No handshake is checked; the response's
	// ProtoMajor is (K16): an HTTP/1.1 answer is refused with
	// ErrNotNegotiated and a WARN, after the request was sent once.
	postChecks := map[string]struct {
		api, route, serverName string
	}{
		"error: behind a proxy, an IP-literal API host is checked after the response": {},
		"error: behind a proxy, a ServerName override is checked after the response": {
			api: exampleURL, route: "example.com:443", serverName: "example.com",
		},
	}
	for name, tc := range postChecks {
		t.Run(name, func(t *testing.T) {
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: testsupport.ALPNNone})
			api, routes := tc.api, testsupport.Routes{}
			if api == "" {
				api = srv.URL() // https://127.0.0.1:port; the proxy dials the literal itself
			} else {
				routes[tc.route] = srv.Addr()
			}
			p := testsupport.NewProxy(t, testsupport.ProxyTLSLenient, routes)
			logs := testsupport.NewLogRecorder(slog.LevelDebug)
			var h hops
			tr := newTestTransport(t, Config{
				APIURL:      mustURL(t, api),
				Proxy:       http.ProxyURL(p.URL()),
				DialContext: testsupport.Routes{}.DialContext,
				TLSConfig:   &tls.Config{VerifyConnection: h.verify, ServerName: tc.serverName},
				Logger:      logs.Logger(),
			})
			if tr.scope != scopePostCheck {
				t.Fatalf("scope %v, want post-check-only", tr.scope)
			}
			r := get(t.Context(), tr, api+"/billed")
			var de *DialError
			if !errors.Is(r.Err, ErrNotNegotiated) || errors.As(r.Err, &de) {
				t.Errorf("error %v, want ErrNotNegotiated from the response check, not a *DialError", r.Err)
			}
			if n := len(srv.Requests()); n != 1 {
				t.Errorf("the server saw %d requests, want the 1 the post-check cannot prevent", n)
			}
			warns := logs.At(slog.LevelWarn)
			if len(warns) != 1 || warns[0].Message != "h2: response not HTTP/2" {
				t.Errorf("WARN records %v, want one \"h2: response not HTTP/2\"", warns)
			}
		})
	}

	t.Run("success: behind a proxy, an IP-literal API host that speaks h2 passes the post-check", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		p := testsupport.NewProxy(t, testsupport.ProxyTLSLenient, testsupport.Routes{})
		var h hops
		tr := proxiedTransport(t, srv.URL(), p, &h, nil)
		r := get(t.Context(), tr, srv.URL()+"/")
		if r.Err != nil || r.Status != http.StatusOK || r.ProtoMajor != 2 {
			t.Errorf("%d HTTP/%d %v, want 200 over HTTP/2", r.Status, r.ProtoMajor, r.Err)
		}
	})

	t.Run("success: the proxy's own handshake timeout keeps the proxy flag", func(t *testing.T) {
		const connect = 250 * time.Millisecond
		l := testsupport.NewSilentListener(t) // a TLS proxy that never answers
		silent := &url.URL{Scheme: "https", Host: l.Addr()}
		tr := newTestTransport(t, Config{APIURL: mustURL(t, exampleURL), ConnectTimeout: connect, Proxy: http.ProxyURL(silent), DialContext: testsupport.Routes{}.DialContext})
		start := time.Now()
		r := get(t.Context(), tr, exampleURL+"/")
		want := map[string]bool{"proxy": true, "timeout": true, "not_negotiated": false}
		if diff := gocmp.Diff(want, dialFlags(r.Err)); diff != "" {
			t.Errorf("classification of %s (-want +got):\n%s", chain(r.Err), diff)
		}
		if el := time.Since(start); el < connect || el > connect+time.Second {
			t.Errorf("elapsed %v, want about the %v handshake timeout", el, connect)
		}
		record(t, "case", "tls-silent-proxy", "chain", chain(r.Err))
	})
}
