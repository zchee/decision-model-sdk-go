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
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// The proxy tests use example.com as the API host (the test certificate
// covers it): the proxy's Routes resolve it to the loopback server, and the
// client dials only the proxy, a loopback literal. The proxy is selected with
// http.ProxyURL, because ProxyFromEnvironment never proxies loopback targets;
// the two hops then have different effective SNI.

// exampleURL is the API URL of the proxy tests.
const exampleURL = "https://example.com"

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
func proxiedTransport(t *testing.T, api string, p *testsupport.Proxy, h *hops) *Transport {
	t.Helper()
	return newTestTransport(t, Config{
		APIURL:      mustURL(t, api),
		Proxy:       http.ProxyURL(p.URL()),
		DialContext: testsupport.Routes{}.DialContext, // loopback literals only: the proxy
		TLSConfig:   &tls.Config{VerifyConnection: h.verify},
	})
}

// TestProxy covers CONNECT through a plain HTTP/1.1 proxy and the build-time
// ALPN scope: a caller Proxy func may apply a proxy, so the check recognises
// the API hop by its SNI; the handshake the check sees through a plain proxy
// is the API hop's alone.
func TestProxy(t *testing.T) {
	//nolint:gocritic // unlambda: the closure is the point; it is another func than ProxyFromEnvironment
	closure := func(r *http.Request) (*url.URL, error) { return http.ProxyFromEnvironment(r) }

	t.Run("success: a 16-way cold burst through a plain proxy: 1 CONNECT, 1 connection, the API hop checked", func(t *testing.T) {
		const n = 16
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(testsupport.AnswerH2ExampleCom)})
		p := testsupport.NewProxy(t, testsupport.ProxyPlain, testsupport.Routes{"example.com:443": srv.Addr()})
		var h hops
		tr := proxiedTransport(t, exampleURL, p, &h)
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
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: testsupport.ALPNNone, Handler: http.HandlerFunc(testsupport.AnswerH2ExampleCom)})
		p := testsupport.NewProxy(t, testsupport.ProxyPlain, testsupport.Routes{"example.com:443": srv.Addr()})
		var h hops
		tr := proxiedTransport(t, exampleURL, p, &h)
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
		dead := &url.URL{Scheme: "http", Host: testsupport.RefusedAddr(t)}
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

	t.Run("error: a CONNECT the proxy refuses is a proxy failure (K16)", func(t *testing.T) {
		// The stock transport returns the status text of a failed CONNECT
		// without the proxyconnect wrapper (transport.go:2036-2043), which
		// cannot be told from a failure past the proxy; NewTransport's
		// OnProxyConnectResponse (refusedConnect) wraps the whole status
		// line. A caller TLS dialer's unfinished handshake with an https
		// proxy, which customDialTLS completes and returns unwrapped
		// (:1905-1910), stays an API-hop failure.
		p := testsupport.NewProxy(t, testsupport.ProxyPlain, testsupport.Routes{})
		var h hops
		tr := proxiedTransport(t, exampleURL, p, &h)
		r := get(t.Context(), tr, exampleURL+"/")
		want := map[string]bool{"proxy": true, "timeout": false, "not_negotiated": false}
		if diff := gocmp.Diff(want, dialFlags(r.Err)); diff != "" {
			t.Errorf("classification of %s (-want +got):\n%s", chain(r.Err), diff)
		}
		if oe, ok := errors.AsType[*net.OpError](r.Err); !ok || oe.Op != "proxyconnect" || oe.Err.Error() != "502 Bad Gateway" {
			t.Errorf("chain %s, want a proxyconnect *net.OpError around the status line", chain(r.Err))
		}
		if diff := gocmp.Diff([]testsupport.ProxyConnect{{Conn: 0, Target: "example.com:443", Status: http.StatusBadGateway}}, p.Connects()); diff != "" {
			t.Errorf("CONNECTs (-want +got):\n%s", diff)
		}
	})

	t.Run("error: a caller transport's refused CONNECT stays as the stock transport returns it (K16)", func(t *testing.T) {
		// Wrap never installs refusedConnect: a caller's transport keeps its
		// own OnProxyConnectResponse, or none.
		p := testsupport.NewProxy(t, testsupport.ProxyPlain, testsupport.Routes{})
		var called atomic.Int64
		base := &http.Transport{
			Proxy:       http.ProxyURL(p.URL()),
			DialContext: testsupport.Routes{}.DialContext,
			OnProxyConnectResponse: func(context.Context, *url.URL, *http.Request, *http.Response) error {
				called.Add(1)
				return nil
			},
		}
		tr, err := Wrap(base, Config{APIURL: mustURL(t, exampleURL), Mode: HTTPAuto})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(tr.CloseIdleConnections)
		r := get(t.Context(), tr, exampleURL+"/")
		want := map[string]bool{"proxy": false, "timeout": false, "not_negotiated": false}
		if diff := gocmp.Diff(want, dialFlags(r.Err)); diff != "" {
			t.Errorf("classification of %s (-want +got):\n%s", chain(r.Err), diff)
		}
		if n := called.Load(); n != 1 {
			t.Errorf("the caller's OnProxyConnectResponse ran %d times, want 1", n)
		}
	})

	t.Run("success: the build-time ALPN scope", func(t *testing.T) {
		api := mustURL(t, exampleURL)
		ipAPI := mustURL(t, "https://127.0.0.1:8443")
		byURL := http.ProxyURL(&url.URL{Scheme: "http", Host: "127.0.0.1:1"})
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
		check := alpnCheck(scopeSNI, effectiveSNI("", "example.com."), nil)
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
			"example.com":           effectiveSNI("", "example.com"),
			"example.com.":          effectiveSNI("", "example.com."),
			"127.0.0.1":             effectiveSNI("", "127.0.0.1"),
			"[::1]":                 effectiveSNI("", "[::1]"),
			"::1":                   effectiveSNI("", "::1"),
			"fe80::1%en0":           effectiveSNI("", "fe80::1%en0"),
			"override over literal": effectiveSNI("api.internal", "127.0.0.1"),
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

// TestProxyTLS covers CONNECT through a TLS proxy: the proxy hop's handshake
// is not checked and the API hop's is; a strict proxy's alert 120 is a proxy
// failure (proxyconnect first); a proxy that negotiates h2 on its own hop is
// recorded; and where the hops cannot be told apart by SNI the response's
// protocol is checked after the fact.
func TestProxyTLS(t *testing.T) {
	t.Run("success: lenient proxy: the proxy hop is not checked, the API hop is", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(testsupport.AnswerH2ExampleCom)})
		p := testsupport.NewProxy(t, testsupport.ProxyTLSLenient, testsupport.Routes{"example.com:443": srv.Addr()})
		var h hops
		tr := proxiedTransport(t, exampleURL, p, &h)
		r := get(t.Context(), tr, exampleURL+"/")
		if r.Err != nil || r.Status != http.StatusOK || r.Body != "h2 example.com" {
			t.Errorf("%d %q %v, want 200 h2 example.com", r.Status, r.Body, r.Err)
		}
		if diff := gocmp.Diff([][2]string{{"", ""}, {"example.com", "h2"}}, h.seen()); diff != "" {
			t.Errorf("handshakes, proxy hop first (-want +got):\n%s", diff)
		}
	})

	t.Run("error: strict proxy refuses h2 with alert 120: a proxy failure, not a negotiation failure", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(testsupport.AnswerH2ExampleCom)})
		p := testsupport.NewProxy(t, testsupport.ProxyTLSStrict, testsupport.Routes{"example.com:443": srv.Addr()})
		var h hops
		tr := proxiedTransport(t, exampleURL, p, &h)
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
		// negotiating it would fail: not supported (docs/deviations.md, "the
		// ALPN check applies to the API hop").
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(testsupport.AnswerH2ExampleCom)})
		p := testsupport.NewProxy(t, testsupport.ProxyTLSOfferH2, testsupport.Routes{"example.com:443": srv.Addr()})
		var h hops
		tr := proxiedTransport(t, exampleURL, p, &h)
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
	// ProtoMajor is: an HTTP/1.1 answer is refused with
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
		tr := proxiedTransport(t, srv.URL(), p, &h)
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
		if el := time.Since(start); el < connect-coarseClock || el > connect+time.Second {
			t.Errorf("elapsed %v, want about the %v handshake timeout", el, connect)
		}
		record(t, "case", "tls-silent-proxy", "chain", chain(r.Err))
	})
}

// TestRefusedConnectScrubsProxyCredential pins, at its source, that a proxy's
// refusal whose status line repeats the credential the CONNECT carried
// becomes an error that holds "***" in its place, the Basic token net/http
// sends for the proxy URL's userinfo as well as the password, raw and as the
// URL escapes it. The token is replaced before the password, which a password
// that is part of its own token shows; a URL without userinfo, and a 200,
// change nothing.
func TestRefusedConnectScrubsProxyCredential(t *testing.T) {
	token := func(user, password string) string {
		return base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
	}
	tests := map[string]struct {
		proxy  *url.URL
		status int
		reason string
		want   string // the error's text; empty for no error
	}{
		"success: a 200 opens the tunnel": {
			proxy: &url.URL{Scheme: "http", User: url.UserPassword("proxy-user", "hunter2-proxy"), Host: "127.0.0.1:1"}, status: http.StatusOK, reason: "Connection established",
		},
		"success: no userinfo, nothing to replace": {
			proxy: &url.URL{Scheme: "http", Host: "127.0.0.1:1"}, status: http.StatusProxyAuthRequired, reason: "denied hunter2-proxy",
			want: "proxyconnect tcp: 407 denied hunter2-proxy",
		},
		"error: the password repeated": {
			proxy: &url.URL{Scheme: "http", User: url.UserPassword("proxy-user", "hunter2-proxy"), Host: "127.0.0.1:1"}, status: http.StatusProxyAuthRequired, reason: "denied hunter2-proxy for proxy-user",
			want: "proxyconnect tcp: 407 denied *** for proxy-user",
		},
		"error: the Basic token repeated": {
			proxy: &url.URL{Scheme: "http", User: url.UserPassword("proxy-user", "hunter2-proxy"), Host: "127.0.0.1:1"}, status: http.StatusForbidden,
			reason: "bad Proxy-Authorization: Basic " + token("proxy-user", "hunter2-proxy"),
			want:   "proxyconnect tcp: 403 bad Proxy-Authorization: Basic ***",
		},
		"error: a password that is part of its own token": {
			proxy: &url.URL{Scheme: "http", User: url.UserPassword("user", "dXNl"), Host: "127.0.0.1:1"}, status: http.StatusProxyAuthRequired,
			reason: "denied dXNl, token " + token("user", "dXNl"), // dXNlcjpkWE5s
			want:   "proxyconnect tcp: 407 denied ***, token ***",
		},
		"error: the password as the URL escapes it": {
			proxy: &url.URL{Scheme: "http", User: url.UserPassword("proxy-user", "p@ss w/rd:x"), Host: "127.0.0.1:1"}, status: http.StatusProxyAuthRequired,
			reason: "denied p%40ss%20w%2Frd%3Ax, sent p@ss w/rd:x",
			want:   "proxyconnect tcp: 407 denied ***, sent ***",
		},
		"error: the token of a user without a password": {
			proxy: &url.URL{Scheme: "http", User: url.User("only-user"), Host: "127.0.0.1:1"}, status: http.StatusProxyAuthRequired,
			reason: "denied only-user, token " + token("only-user", ""),
			want:   "proxyconnect tcp: 407 denied only-user, token ***",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tt.status, Status: strconv.Itoa(tt.status) + " " + tt.reason}
			err := refusedConnect(t.Context(), tt.proxy, nil, resp)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("refusedConnect = %v, want nil", err)
				}
				return
			}
			oe, ok := errors.AsType[*net.OpError](err)
			if !ok || oe.Op != "proxyconnect" {
				t.Fatalf("refusedConnect = %T %v, want a proxyconnect *net.OpError", err, err)
			}
			if got := err.Error(); got != tt.want {
				t.Errorf("refusedConnect(%q) = %q, want %q", resp.Status, got, tt.want)
			}
		})
	}
}

// heldProxy is a plain proxy on 127.0.0.1 that reads each request, a CONNECT
// or a forwarded one, and, once open has run, answers it with 407;
// requests counts the requests read.
type heldProxy struct {
	ln       net.Listener
	release  chan struct{}
	open     func()
	requests atomic.Int64
	wg       sync.WaitGroup
}

// newHeldProxy starts a heldProxy, open already when held is false. It stops
// when the test ends.
func newHeldProxy(t *testing.T, held bool) *heldProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := &heldProxy{ln: ln, release: make(chan struct{})}
	p.open = sync.OnceFunc(func() { close(p.release) })
	if !held {
		p.open()
	}
	p.wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			p.wg.Go(func() {
				defer conn.Close()
				if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
					return
				}
				p.requests.Add(1)
				<-p.release
				_, _ = io.WriteString(conn, "HTTP/1.1 407 denied\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			})
		}
	})
	t.Cleanup(func() {
		p.open()
		_ = ln.Close()
		p.wg.Wait()
	})
	return p
}

// url returns the proxy's URL with userinfo whose password is password.
func (p *heldProxy) url(password string) *url.URL {
	return &url.URL{Scheme: "http", User: url.UserPassword("proxy-user", password), Host: p.ln.Addr().String()}
}

// seenProxies collects the URLs an OnProxy hook got.
type seenProxies struct {
	mu   sync.Mutex
	urls []*url.URL
}

func (s *seenProxies) onProxy(u *url.URL) {
	s.mu.Lock()
	s.urls = append(s.urls, u)
	s.mu.Unlock()
}

func (s *seenProxies) got() []*url.URL {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.urls)
}

// TestOnProxySeesEveryConsult pins Config.OnProxy: the hook of a transport
// NewTransport built gets the very URL the Proxy func returned, each time the
// stock transport asks the func and only then. The request that dials asks
// it; requests sent on the HTTP/2 connection it made ask nothing; a cold
// burst's waiters, released by their leader's failed dial, ask nothing
// either, so one consult covers the burst and the hook is the one place its
// proxy's credential can be recorded; a func that fails reports nothing. A
// rotating func shows each consult reported in order.
func TestOnProxySeesEveryConsult(t *testing.T) {
	counting := func(calls *atomic.Int64, pick func(n int64) *url.URL) func(*http.Request) (*url.URL, error) {
		return func(*http.Request) (*url.URL, error) { return pick(calls.Add(1)), nil }
	}
	isProxyFailure := func(err error) bool {
		de, ok := errors.AsType[*DialError](err)
		return ok && de.Proxy
	}

	t.Run("success: the request that dials asks the func; those on its connection do not", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(testsupport.AnswerH2ExampleCom)})
		p := testsupport.NewProxy(t, testsupport.ProxyPlain, testsupport.Routes{"example.com:443": srv.Addr()})
		pu := p.URL()
		pu.User = url.UserPassword("proxy-user", "hunter2-proxy-password")
		var calls atomic.Int64
		var seen seenProxies
		tr := newTestTransport(t, Config{APIURL: mustURL(t, exampleURL), Proxy: counting(&calls, func(int64) *url.URL { return pu }), OnProxy: seen.onProxy, DialContext: testsupport.Routes{}.DialContext})
		for i := range 3 {
			if r := get(t.Context(), tr, exampleURL+"/"+strconv.Itoa(i)); r.Err != nil || r.Status != http.StatusOK {
				t.Fatalf("request %d: %d %v", i, r.Status, r.Err)
			}
		}
		// The stock transport sends a request on an HTTP/2 connection it
		// holds without looking for one (GOROOT/src/net/http/transport.go:643-646).
		if got := seen.got(); calls.Load() != 1 || len(got) != 1 || got[0] != pu {
			t.Errorf("func calls %d, OnProxy got %v; want 1 and [%v]", calls.Load(), got, pu)
		}
	})

	t.Run("error: a cold burst's waiters ask nothing: one consult for the leader's failed dial", func(t *testing.T) {
		const n = 12
		a := newHeldProxy(t, true)
		ua := a.url("alpha-proxy-password")
		var calls atomic.Int64
		var seen seenProxies
		tr := newTestTransport(t, Config{APIURL: mustURL(t, exampleURL), Proxy: counting(&calls, func(int64) *url.URL { return ua }), OnProxy: seen.onProxy, DialContext: testsupport.Routes{}.DialContext})
		results := make([]result, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() { results[i] = get(t.Context(), tr, exampleURL+"/"+strconv.Itoa(i)) })
		}
		testsupport.WaitUntil(t, "the leader's CONNECT and 11 parked waiters", func() bool { return a.requests.Load() == 1 && tr.Parked() == n-1 })
		a.open()
		wg.Wait()
		for i, r := range results {
			if !isProxyFailure(r.Err) {
				t.Errorf("request %d: error %s, want the leader's proxy failure", i, chain(r.Err))
			}
		}
		if got := seen.got(); calls.Load() != 1 || len(got) != 1 || got[0] != ua {
			t.Errorf("func calls %d, OnProxy got %v; want 1 and [%v]", calls.Load(), got, ua)
		}
	})

	t.Run("error: a rotating func: each failed dial's consult is reported in order", func(t *testing.T) {
		a, b := newHeldProxy(t, false), newHeldProxy(t, false)
		urls := [2]*url.URL{a.url("alpha-proxy-password"), b.url("bravo-proxy-password")}
		var calls atomic.Int64
		var seen seenProxies
		tr := newTestTransport(t, Config{APIURL: mustURL(t, exampleURL), Proxy: counting(&calls, func(n int64) *url.URL { return urls[(n-1)%2] }), OnProxy: seen.onProxy, DialContext: testsupport.Routes{}.DialContext})
		const n = 4
		for i := range n {
			if r := get(t.Context(), tr, exampleURL+"/"); !isProxyFailure(r.Err) {
				t.Fatalf("request %d: error %s, want a proxy failure", i, chain(r.Err))
			}
		}
		got := seen.got()
		ok := len(got) == n
		for i := range got {
			ok = ok && got[i] == urls[i%2]
		}
		if !ok || calls.Load() != n || a.requests.Load() != n/2 || b.requests.Load() != n/2 {
			t.Errorf("OnProxy got %v, func calls %d, CONNECTs %d and %d; want alpha, bravo, alpha, bravo, %d, %d and %d", got, calls.Load(), a.requests.Load(), b.requests.Load(), n, n/2, n/2)
		}
	})

	t.Run("error: a func that fails reports nothing", func(t *testing.T) {
		errNoProxy := errors.New("no proxy for this request")
		p := newHeldProxy(t, false)
		pu := p.url("alpha-proxy-password")
		var seen seenProxies
		tr := newTestTransport(t, Config{APIURL: mustURL(t, exampleURL), Proxy: func(*http.Request) (*url.URL, error) {
			return pu, errNoProxy // a URL with an error is not used
		}, OnProxy: seen.onProxy, DialContext: testsupport.Routes{}.DialContext})
		if r := get(t.Context(), tr, exampleURL+"/"); !errors.Is(r.Err, errNoProxy) {
			t.Errorf("error %s, want the func's", chain(r.Err))
		}
		if got := seen.got(); len(got) != 0 || p.requests.Load() != 0 {
			t.Errorf("OnProxy got %v, CONNECTs %d; want none", got, p.requests.Load())
		}
	})
}

// TestProxied pins Proxied: a response reports true when the Proxy func of
// the transport NewTransport built returned a proxy for its request, which
// the root package's response header redactor needs to know; false for a
// request sent on the HTTP/2 connection the transport held (the func is not
// asked), for a func that returned no proxy, for a transport with no func,
// and for a response without a request or with a request no call made.
func TestProxied(t *testing.T) {
	// proxied sends a GET through tr and returns Proxied of its response.
	proxied := func(t *testing.T, tr *Transport, rawURL string) bool {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := tr.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return Proxied(resp)
	}
	srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(testsupport.AnswerH2ExampleCom)})
	p := testsupport.NewProxy(t, testsupport.ProxyPlain, testsupport.Routes{"example.com:443": srv.Addr()})

	t.Run("success: the request the func chose a proxy for, then one on its connection", func(t *testing.T) {
		tr := newTestTransport(t, Config{APIURL: mustURL(t, exampleURL), Proxy: http.ProxyURL(p.URL()), DialContext: testsupport.Routes{}.DialContext})
		if !proxied(t, tr, exampleURL+"/1") {
			t.Error("Proxied = false for the request the func chose a proxy for")
		}
		if proxied(t, tr, exampleURL+"/2") {
			t.Error("Proxied = true for a request on the held HTTP/2 connection, which asks the func nothing")
		}
	})

	t.Run("success: a func that returns no proxy", func(t *testing.T) {
		direct := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(testsupport.AnswerH2ExampleCom)})
		tr := newTestTransport(t, Config{APIURL: mustURL(t, direct.URL()), Proxy: func(*http.Request) (*url.URL, error) { return nil, nil }})
		if proxied(t, tr, direct.URL()+"/") {
			t.Error("Proxied = true with no proxy returned")
		}
	})

	t.Run("success: no func", func(t *testing.T) {
		direct := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(testsupport.AnswerH2ExampleCom)})
		tr := newTestTransport(t, Config{APIURL: mustURL(t, direct.URL())})
		if proxied(t, tr, direct.URL()+"/") {
			t.Error("Proxied = true with no Proxy func")
		}
	})

	t.Run("success: no response, no request, a request no call made", func(t *testing.T) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, exampleURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		for name, resp := range map[string]*http.Response{"nil": nil, "no request": {}, "a plain request": {Request: req}} {
			if Proxied(resp) {
				t.Errorf("%s: Proxied = true", name)
			}
		}
	})
}
