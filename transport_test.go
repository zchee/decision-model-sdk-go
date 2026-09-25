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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/h2gate"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// testKey is the API key of the transport tests, which never reach an API.
const testKey = "test-key"

// getResult is what a GET through a client's transport produced.
type getResult struct {
	status, protoMajor int
	err                error
}

// getVia sends GET rawURL through tr with the attempt timeout timeout, reads
// the whole response body and closes it.
func getVia(ctx context.Context, tr *transport, rawURL string, timeout time.Duration) getResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return getResult{err: err}
	}
	resp, err := tr.roundTrip(req, timeout)
	if err != nil {
		return getResult{err: err}
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return getResult{status: resp.StatusCode, protoMajor: resp.ProtoMajor, err: err}
}

// getWithin is getVia under a deadline of d.
func getWithin(t *testing.T, tr *transport, rawURL string, timeout, d time.Duration) getResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	defer cancel()
	return getVia(ctx, tr, rawURL, timeout)
}

// loopbackConfig resolves a configuration whose API is srv, trusted through
// the test CA and reached without a proxy, with opts after those.
func loopbackConfig(t *testing.T, srv *testsupport.LoopbackServer, opts ...ClientOption) *config {
	t.Helper()
	base := []ClientOption{WithAPIKey(testKey), WithBaseURL(srv.URL()), WithRootCAs(testsupport.RootCAs(t)), WithProxy(nil)}
	return mustResolve(t, noEnv, append(base, opts...)...)
}

// closedAddr returns a loopback address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

// TestHTTPVersionString pins the names of the policies.
func TestHTTPVersionString(t *testing.T) {
	tests := map[string]struct {
		v    HTTPVersion
		want string
	}{
		"success: HTTP2Only":      {v: HTTP2Only, want: "HTTP2Only"},
		"success: HTTPAuto":       {v: HTTPAuto, want: "HTTPAuto"},
		"success: the zero value": {v: 0, want: "HTTPVersion(0)"},
		"success: an unknown one": {v: 7, want: "HTTPVersion(7)"},
		"success: a negative one": {v: -1, want: "HTTPVersion(-1)"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.v.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestHTTPVersionDefaults pins section 6.3's defaults: an http base URL is
// HTTPAuto, which speaks HTTP/1.1 to a plain server, and an https base URL is
// HTTP2Only, which refuses a server that negotiates no protocol;
// WithHTTPVersion overrides either.
func TestHTTPVersionDefaults(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(plain.Close)
	noALPN := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: testsupport.ALPNNone})
	cas := WithRootCAs(testsupport.RootCAs(t))
	tests := map[string]struct {
		baseURL   string
		opts      []ClientOption
		wantProto int // the response's major version; 0 when the call fails
	}{
		"success: http defaults to HTTPAuto, which speaks HTTP/1.1":           {baseURL: plain.URL, wantProto: 1},
		"error: http under HTTP2Only speaks HTTP/2 with prior knowledge only": {baseURL: plain.URL, opts: []ClientOption{WithHTTPVersion(HTTP2Only)}},
		"error: https defaults to HTTP2Only, which refuses no ALPN":           {baseURL: noALPN.URL(), opts: []ClientOption{cas}},
		"success: https under HTTPAuto speaks HTTP/1.1":                       {baseURL: noALPN.URL(), opts: []ClientOption{cas, WithHTTPVersion(HTTPAuto)}, wantProto: 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := mustResolve(t, noEnv, append([]ClientOption{WithAPIKey(testKey), WithBaseURL(tt.baseURL)}, tt.opts...)...)
			r := getWithin(t, c.transport, tt.baseURL+"/v1/models", 0, 10*time.Second)
			if tt.wantProto == 0 {
				if r.err == nil {
					t.Fatalf("GET = %d HTTP/%d, want an error", r.status, r.protoMajor)
				}
				if strings.HasPrefix(tt.baseURL, "https:") && !errors.Is(r.err, ErrHTTP2NotNegotiated) {
					t.Errorf("error = %v, want ErrHTTP2NotNegotiated", r.err)
				}
				return
			}
			if r.err != nil || r.status != http.StatusOK || r.protoMajor != tt.wantProto {
				t.Errorf("GET = %d HTTP/%d %v, want 200 over HTTP/%d", r.status, r.protoMajor, r.err, tt.wantProto)
			}
		})
	}
}

// TestTransportOptionsAreExclusive is F1's Go half (deviation "one
// transport option, two kinds"): Python refuses transport= together with
// http_client=; the port refuses WithHTTPTransport together with
// WithRoundTripper, and each of them together with the options that
// configure what it replaces. An option counts once given, whatever its
// value, and the order of the options never decides.
func TestTransportOptionsAreExclusive(t *testing.T) {
	rt := &testsupport.Recorder{}
	ht := &http.Transport{}
	pool := x509.NewCertPool()
	rtWith := func(name string) string {
		return "WithRoundTripper cannot be combined with " + name + ": the round tripper replaces the SDK's transport, which " + name + " configures."
	}
	htWith := func(name string) string {
		return "WithHTTPTransport cannot be combined with " + name + ": the caller's transport keeps its own TLS configuration and proxy."
	}
	const both = "WithRoundTripper and WithHTTPTransport cannot be combined: each supplies the client's transport."
	tests := map[string]struct {
		opts []ClientOption
		want string // the *ConfigError's text; "" when the client builds
	}{
		"error: WithHTTPTransport and WithRoundTripper":     {opts: []ClientOption{WithHTTPTransport(ht), WithRoundTripper(rt)}, want: both},
		"error: WithRoundTripper and WithHTTPTransport":     {opts: []ClientOption{WithRoundTripper(rt), WithHTTPTransport(ht)}, want: both},
		"error: both, each nil":                             {opts: []ClientOption{WithRoundTripper(nil), WithHTTPTransport(nil)}, want: both},
		"error: WithRoundTripper and WithHTTPVersion":       {opts: []ClientOption{WithHTTPVersion(HTTPAuto), WithRoundTripper(rt)}, want: rtWith("WithHTTPVersion")},
		"error: WithRoundTripper and WithRootCAs":           {opts: []ClientOption{WithRoundTripper(rt), WithRootCAs(pool)}, want: rtWith("WithRootCAs")},
		"error: WithRoundTripper and WithRootCAs(nil)":      {opts: []ClientOption{WithRoundTripper(rt), WithRootCAs(nil)}, want: rtWith("WithRootCAs")},
		"error: WithRoundTripper and WithTLSConfig(nil)":    {opts: []ClientOption{WithTLSConfig(nil), WithRoundTripper(rt)}, want: rtWith("WithTLSConfig")},
		"error: WithRoundTripper and WithProxy(nil)":        {opts: []ClientOption{WithRoundTripper(rt), WithProxy(nil)}, want: rtWith("WithProxy")},
		"error: WithRoundTripper and WithConnectTimeout":    {opts: []ClientOption{WithConnectTimeout(time.Second), WithRoundTripper(rt)}, want: rtWith("WithConnectTimeout")},
		"error: WithHTTPTransport and WithRootCAs":          {opts: []ClientOption{WithRootCAs(pool), WithHTTPTransport(ht)}, want: htWith("WithRootCAs")},
		"error: WithHTTPTransport and WithTLSConfig":        {opts: []ClientOption{WithHTTPTransport(ht), WithTLSConfig(&tls.Config{})}, want: htWith("WithTLSConfig")},
		"error: WithHTTPTransport and WithProxy":            {opts: []ClientOption{WithHTTPTransport(ht), WithProxy(http.ProxyFromEnvironment)}, want: htWith("WithProxy")},
		"error: WithHTTPTransport and WithProxy(nil)":       {opts: []ClientOption{WithProxy(nil), WithHTTPTransport(ht)}, want: htWith("WithProxy")},
		"error: WithRoundTripper(nil)":                      {opts: []ClientOption{WithRoundTripper(nil)}, want: "The round tripper passed to WithRoundTripper must not be nil."},
		"error: WithHTTPTransport(nil)":                     {opts: []ClientOption{WithHTTPTransport(nil)}, want: "The transport passed to WithHTTPTransport must not be nil."},
		"error: WithHTTPVersion(0)":                         {opts: []ClientOption{WithHTTPVersion(0)}, want: "The policy passed to WithHTTPVersion must be HTTP2Only or HTTPAuto."},
		"error: WithHTTPVersion(3)":                         {opts: []ClientOption{WithHTTPVersion(3)}, want: "The policy passed to WithHTTPVersion must be HTTP2Only or HTTPAuto."},
		"success: WithHTTPTransport and WithConnectTimeout": {opts: []ClientOption{WithHTTPTransport(ht), WithConnectTimeout(time.Second)}},
		"success: WithHTTPTransport and WithHTTPVersion":    {opts: []ClientOption{WithHTTPTransport(ht), WithHTTPVersion(HTTPAuto)}},
		"success: WithHTTPTransport and WithClientTrace":    {opts: []ClientOption{WithHTTPTransport(ht), WithClientTrace(&httptrace.ClientTrace{})}},
		"success: WithRoundTripper and WithClientTrace":     {opts: []ClientOption{WithRoundTripper(rt), WithClientTrace(&httptrace.ClientTrace{})}},
		"success: the default transport's options together": {opts: []ClientOption{WithRootCAs(pool), WithTLSConfig(&tls.Config{}), WithProxy(nil), WithConnectTimeout(time.Second), WithHTTPVersion(HTTP2Only)}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			opts := append([]ClientOption{WithAPIKey(testKey)}, tt.opts...)
			if tt.want == "" {
				c := mustResolve(t, noEnv, opts...)
				if c.transport == nil || c.transport.rt == nil {
					t.Fatalf("resolve built no transport")
				}
				return
			}
			ce := resolveError(t, noEnv, opts...)
			if diff := gocmp.Diff(tt.want, ce.Error()); diff != "" {
				t.Errorf("Error() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestTransportCheckedLast pins where the transport stands in resolve's
// order: after every other setting, so a conflict of transport options does
// not hide an unusable header.
func TestTransportCheckedLast(t *testing.T) {
	ce := resolveError(t, noEnv, WithAPIKey(testKey), WithRoundTripper(nil), WithHTTPTransport(nil), WithHeader("bad name", "v"))
	if strings.Contains(ce.Error(), "WithRoundTripper") {
		t.Errorf("Error() = %q, want the header's error first", ce.Error())
	}
}

// TestTransportKinds pins which transport resolve builds for each kind of
// option: the SDK's gate for the default and for WithHTTPTransport, the
// caller's round tripper as it is for WithRoundTripper.
func TestTransportKinds(t *testing.T) {
	rt := &testsupport.Recorder{}
	tests := map[string]struct {
		opts     []ClientOption
		wantGate bool
	}{
		"success: the default transport": {wantGate: true},
		"success: WithHTTPTransport":     {opts: []ClientOption{WithHTTPTransport(&http.Transport{})}, wantGate: true},
		"success: WithRoundTripper":      {opts: []ClientOption{WithRoundTripper(rt)}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := mustResolve(t, noEnv, append([]ClientOption{WithAPIKey(testKey)}, tt.opts...)...)
			if got := c.transport.gate != nil; got != tt.wantGate {
				t.Fatalf("gate built: %t, want %t", got, tt.wantGate)
			}
			if tt.wantGate {
				if c.transport.rt != c.transport.gate || c.transport.closer != nil {
					t.Errorf("rt %T, closer %T; want the gate and no closer", c.transport.rt, c.transport.closer)
				}
				return
			}
			if c.transport.rt != rt || c.transport.closer != rt {
				t.Errorf("rt %T, closer %T; want the Recorder for both", c.transport.rt, c.transport.closer)
			}
			if got := c.transport.stats(); got != (h2gate.Stats{}) {
				t.Errorf("stats() = %+v, want zero values without the SDK's transport", got)
			}
		})
	}
}

// TestTransportOptionsCopy pins that WithTLSConfig and WithClientTrace keep a
// copy of what they were given, so a caller changing its value afterwards
// changes nothing the client uses.
func TestTransportOptionsCopy(t *testing.T) {
	cfg := &tls.Config{ServerName: "before"}
	var calls []string
	trace := &httptrace.ClientTrace{GetConn: func(string) { calls = append(calls, "before") }}
	o := collectOptions([]ClientOption{WithTLSConfig(cfg), WithClientTrace(trace)})
	cfg.ServerName = "after"
	trace.GetConn = func(string) { calls = append(calls, "after") }
	if got := o.transport.tlsConfig.ServerName; got != "before" {
		t.Errorf("recorded ServerName %q, want %q", got, "before")
	}
	o.transport.trace.GetConn("h:443")
	if diff := gocmp.Diff([]string{"before"}, calls); diff != "" {
		t.Errorf("recorded GetConn (-want +got):\n%s", diff)
	}
	if o := collectOptions([]ClientOption{WithClientTrace(trace), WithClientTrace(nil)}); o.transport.trace != nil {
		t.Errorf("WithClientTrace(nil) after a trace kept %p, want none", o.transport.trace)
	}
}

// TestTransportBuildErrors pins the *ConfigError of each h2gate build
// failure, none of which repeats the host or a proxy URL.
func TestTransportBuildErrors(t *testing.T) {
	t.Run("error: a non-ASCII host under HTTP2Only", func(t *testing.T) {
		ce := resolveError(t, noEnv, WithAPIKey(testKey), WithBaseURL("https://bücher.example"))
		const want = "The base URL's host must be ASCII under HTTP2Only (write an internationalised name in its xn-- form), or use WithHTTPVersion(HTTPAuto)."
		if diff := gocmp.Diff(want, ce.Error()); diff != "" {
			t.Errorf("Error() (-want +got):\n%s", diff)
		}
		assertNotPrinted(t, ce, "bücher")
		mustResolve(t, noEnv, WithAPIKey(testKey), WithBaseURL("https://bücher.example"), WithHTTPVersion(HTTPAuto))
	})
	t.Run("error: a caller transport carrying its own h2 under HTTP2Only", func(t *testing.T) {
		ht := &http.Transport{TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{
			"h2": func(string, *tls.Conn) http.RoundTripper { return nil },
		}}
		ce := resolveError(t, noEnv, WithAPIKey(testKey), WithHTTPTransport(ht))
		const want = "The transport passed to WithHTTPTransport carries its own HTTP/2 implementation in TLSNextProto, which HTTP2Only cannot check; remove it or use WithHTTPVersion(HTTPAuto)."
		if diff := gocmp.Diff(want, ce.Error()); diff != "" {
			t.Errorf("Error() (-want +got):\n%s", diff)
		}
		mustResolve(t, noEnv, WithAPIKey(testKey), WithHTTPTransport(ht), WithHTTPVersion(HTTPAuto))
	})
	// http.ProxyFromEnvironment reads the environment once per process, so
	// the proxy-environment failure is pinned on buildError directly.
	t.Run("error: the proxy environment, whose text may hold a credential", func(t *testing.T) {
		cause := fmt.Errorf("%w: %w", h2gate.ErrProxyEnvironment, errors.New(`invalid proxy address "http://user:hunter2@proxy.test:3128"`))
		ce := buildError(cause)
		const want = "The proxy environment variables (HTTPS_PROXY, HTTP_PROXY) hold a proxy URL that cannot be used; fix them, or choose the proxy with WithProxy."
		if diff := gocmp.Diff(want, ce.Error()); diff != "" {
			t.Errorf("Error() (-want +got):\n%s", diff)
		}
		if len(ce.Unwrap()) != 0 {
			t.Errorf("Unwrap() = %v, want nothing", ce.Unwrap())
		}
		assertNotPrinted(t, ce, "hunter2")
	})
	t.Run("error: any other build failure keeps its cause", func(t *testing.T) {
		cause := errors.New("h2gate: unknown mode 9")
		ce := buildError(cause)
		if ce.Error() != "The client's transport cannot be built." || !errors.Is(ce, cause) {
			t.Errorf("Error() = %q, errors.Is(cause) = %t; want the fixed text wrapping the cause", ce.Error(), errors.Is(ce, cause))
		}
	})
}

// TestDialErrorsMapToSDKErrors pins the h2gate → SDK error mapping (section
// 6.3, R67 Q3): a proxy hop that timed out is a *TimeoutError naming the
// proxy hop; any other proxy failure is a *ConnectionError with Proxy(),
// even when the proxy refused h2 (R20); a failure to speak HTTP/2 is a
// *ConfigError wrapping ErrHTTP2NotNegotiated and the cause; a dial that
// timed out is a *TimeoutError; any other dial failure is a
// *ConnectionError. An error that is none of those is left to the attempt's
// classification (W2.5). The table drives the mapping with h2gate values;
// the loopback cases below reach it through the transports resolve builds.
func TestDialErrorsMapToSDKErrors(t *testing.T) {
	const attempt = 10 * time.Second
	timeoutCause := &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
	notNegotiated := fmt.Errorf("%w: the API host's TLS handshake negotiated %q", h2gate.ErrNotNegotiated, "http/1.1")
	type want struct {
		kind       string // "timeout", "connection", "config" or "" for unmapped
		text       string
		proxy      bool
		unwrapsErr bool // the SDK error unwraps to the transport's error
	}
	tests := map[string]struct {
		err  error
		want want
	}{
		"success: a proxy hop that timed out": {
			err:  &h2gate.DialError{Proxy: true, Timeout: true, Err: timeoutCause},
			want: want{kind: "timeout", text: "Request timed out on the proxy hop (timeout=10s).", proxy: true, unwrapsErr: true},
		},
		"success: a refused proxy": {
			err:  &h2gate.DialError{Proxy: true, Err: errors.New("proxyconnect tcp: dial tcp 127.0.0.1:9: connect: connection refused")},
			want: want{kind: "connection", text: "Connection error: proxyconnect tcp: dial tcp 127.0.0.1:9: connect: connection refused", proxy: true, unwrapsErr: true},
		},
		"success: a proxy that refused h2 is a proxy failure (R20)": {
			err:  &h2gate.DialError{Proxy: true, Err: fmt.Errorf("proxyconnect tcp: %w", notNegotiated)},
			want: want{kind: "connection", text: `Connection error: proxyconnect tcp: h2gate: HTTP/2 not negotiated: the API host's TLS handshake negotiated "http/1.1"`, proxy: true, unwrapsErr: true},
		},
		"success: a proxy URL's userinfo is scrubbed and the cause dropped": {
			err:  &h2gate.DialError{Proxy: true, Err: errors.New("proxyconnect tcp: http://user:hunter2@proxy.test:3128: refused")},
			want: want{kind: "connection", text: "Connection error: proxyconnect tcp: http://***@proxy.test:3128: refused", proxy: true},
		},
		"success: a proxy-hop timeout drops a cause holding a proxy password": {
			err:  &h2gate.DialError{Proxy: true, Timeout: true, Err: errors.New("proxyconnect tcp: http://user:hunter2@proxy.test:3128: i/o timeout")},
			want: want{kind: "timeout", text: "Request timed out on the proxy hop (timeout=10s).", proxy: true},
		},
		"success: a dial timeout drops a cause holding a password": {
			err:  &h2gate.DialError{Timeout: true, Err: errors.New("dial http://user:hunter2@proxy.test:3128: i/o timeout")},
			want: want{kind: "timeout", text: "Request timed out (timeout=10s)."},
		},
		"success: a not-negotiated detail has its userinfo scrubbed and its cause dropped": {
			err:  &h2gate.DialError{Err: fmt.Errorf("%w: via http://user:hunter2@proxy.test:3128", h2gate.ErrNotNegotiated)},
			want: want{kind: "config", text: "The API host did not negotiate HTTP/2, which HTTP2Only requires (via http://***@proxy.test:3128); WithHTTPVersion(HTTPAuto) allows HTTP/1.1."},
		},
		"success: a URL without userinfo keeps its text and cause": {
			err:  &h2gate.DialError{Err: errors.New("dial http://proxy.test:8080: refused")},
			want: want{kind: "connection", text: "Connection error: dial http://proxy.test:8080: refused", unwrapsErr: true},
		},
		"success: the API host's handshake chose HTTP/1.1": {
			err:  &h2gate.DialError{Err: notNegotiated},
			want: want{kind: "config", text: `The API host did not negotiate HTTP/2, which HTTP2Only requires (the API host's TLS handshake negotiated "http/1.1"); WithHTTPVersion(HTTPAuto) allows HTTP/1.1.`, unwrapsErr: true},
		},
		"success: not negotiated wins over a timeout": {
			err:  &h2gate.DialError{Timeout: true, Err: notNegotiated},
			want: want{kind: "config", text: `The API host did not negotiate HTTP/2, which HTTP2Only requires (the API host's TLS handshake negotiated "http/1.1"); WithHTTPVersion(HTTPAuto) allows HTTP/1.1.`, unwrapsErr: true},
		},
		"success: a response over HTTP/1.1 (the post-check)": {
			err:  fmt.Errorf("%w: the response is %s", h2gate.ErrNotNegotiated, "HTTP/1.1"),
			want: want{kind: "config", text: "The API host did not negotiate HTTP/2, which HTTP2Only requires (the response is HTTP/1.1); WithHTTPVersion(HTTPAuto) allows HTTP/1.1.", unwrapsErr: true},
		},
		"success: a dial that timed out": {
			err:  &h2gate.DialError{Timeout: true, Err: timeoutCause},
			want: want{kind: "timeout", text: "Request timed out (timeout=10s).", unwrapsErr: true},
		},
		"success: a refused dial": {
			err:  &h2gate.DialError{Err: errors.New("dial tcp 127.0.0.1:9: connect: connection refused")},
			want: want{kind: "connection", text: "Connection error: dial tcp 127.0.0.1:9: connect: connection refused", unwrapsErr: true},
		},
		"success: control characters in the cause are escaped": {
			err:  &h2gate.DialError{Err: errors.New("dial tcp: \x1b[31mred\u202e")},
			want: want{kind: "connection", text: "Connection error: dial tcp: \\x1b[31mred\\u202e", unwrapsErr: true},
		},
		"success: a stream error is left to the attempt's classification": {err: errors.New("http2: stream closed")},
		"success: the context's deadline is left to it too":               {err: context.DeadlineExceeded},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := transportError(tt.err, attempt)
			assertMapped(t, got, tt.err, tt.want.kind, tt.want.text, tt.want.proxy, tt.want.unwrapsErr)
			if tt.want.kind == "" && got != nil {
				t.Errorf("transportError = %T %v, want nil", got, got)
			}
			assertNotPrinted(t, got, "hunter2")
		})
	}
	t.Run("success: a dial error without an attempt timeout", func(t *testing.T) {
		got := transportError(&h2gate.DialError{Timeout: true, Err: timeoutCause}, 0)
		if got == nil || got.Error() != "Request timed out." {
			t.Errorf("transportError = %v, want %q", got, "Request timed out.")
		}
	})
	t.Run("success: an unmapped error comes back from roundTrip as it is", func(t *testing.T) {
		cause := errors.New("http2: stream closed")
		c := mustResolve(t, noEnv, WithAPIKey(testKey), WithRoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, cause })))
		if r := getWithin(t, c.transport, "https://api.typesafe.ai/v1/models", attempt, time.Second); r.err != cause { //nolint:errorlint // identity is the assertion
			t.Errorf("roundTrip error = %v, want the round tripper's own", r.err)
		}
	})
	t.Run("loopback", func(t *testing.T) { testDialErrorsOverLoopback(t) })
}

// testDialErrorsOverLoopback drives each class of the mapping through the
// transport resolve builds, against loopback peers.
func testDialErrorsOverLoopback(t *testing.T) {
	const (
		attempt = 7 * time.Second
		connect = 300 * time.Millisecond
		within  = 10 * time.Second
	)
	t.Run("error: a server without ALPN under HTTP2Only", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: testsupport.ALPNNone})
		c := loopbackConfig(t, srv)
		r := getWithin(t, c.transport, srv.URL()+"/v1/models", attempt, within)
		ce := assertNotNegotiated(t, r.err)
		if ce != nil && !strings.Contains(ce.Error(), `negotiated ""`) {
			t.Errorf("Error() = %q, want the empty protocol named", ce.Error())
		}
		if n := len(srv.Requests()); n != 0 {
			t.Errorf("the server saw %d requests, want none: the refusal comes before any byte", n)
		}
	})
	t.Run("error: a server offering only http/1.1 answers alert 120", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: testsupport.ALPNHTTP1Only})
		c := loopbackConfig(t, srv)
		r := getWithin(t, c.transport, srv.URL()+"/v1/models", attempt, within)
		ce := assertNotNegotiated(t, r.err)
		if ce != nil && !strings.Contains(ce.Error(), "no application protocol") {
			t.Errorf("Error() = %q, want the alert named", ce.Error())
		}
	})
	t.Run("success: the same server under HTTPAuto speaks HTTP/1.1", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: testsupport.ALPNHTTP1Only})
		c := loopbackConfig(t, srv, WithHTTPVersion(HTTPAuto))
		if r := getWithin(t, c.transport, srv.URL()+"/v1/models", attempt, within); r.err != nil || r.status != http.StatusOK || r.protoMajor != 1 {
			t.Errorf("GET = %d HTTP/%d %v, want 200 over HTTP/1.1", r.status, r.protoMajor, r.err)
		}
	})
	t.Run("error: a TLS-silent API host", func(t *testing.T) {
		silent := testsupport.NewSilentListener(t)
		c := mustResolve(t, noEnv, WithAPIKey(testKey), WithBaseURL(silent.URL()), WithProxy(nil), WithConnectTimeout(connect))
		r := getWithin(t, c.transport, silent.URL()+"/v1/models", attempt, within)
		var te *TimeoutError
		if !errors.As(r.err, &te) || te.Proxy() || te.Timeout != attempt {
			t.Fatalf("error = %T %v, want an attempt *TimeoutError", r.err, r.err)
		}
		if te.Error() != "Request timed out (timeout=7s)." {
			t.Errorf("Error() = %q", te.Error())
		}
	})
	t.Run("error: a refused API host", func(t *testing.T) {
		addr := closedAddr(t)
		c := mustResolve(t, noEnv, WithAPIKey(testKey), WithBaseURL("https://"+addr), WithProxy(nil))
		r := getWithin(t, c.transport, "https://"+addr+"/v1/models", attempt, within)
		var ce *ConnectionError
		if !errors.As(r.err, &ce) || ce.Proxy() || !strings.HasPrefix(ce.Error(), "Connection error: dial tcp ") {
			t.Fatalf("error = %T %v, want an API-hop *ConnectionError", r.err, r.err)
		}
		if _, ok := errors.AsType[*h2gate.DialError](r.err); !ok {
			t.Errorf("error %v does not unwrap to the transport's DialError", r.err)
		}
	})
	t.Run("error: a refused proxy with a password", func(t *testing.T) {
		proxy := &url.URL{Scheme: "http", User: url.UserPassword("user", "hunter2"), Host: closedAddr(t)}
		c := mustResolve(t, noEnv, WithAPIKey(testKey), WithBaseURL("https://example.com"), WithProxy(http.ProxyURL(proxy)))
		r := getWithin(t, c.transport, "https://example.com/v1/models", attempt, within)
		var ce *ConnectionError
		if !errors.As(r.err, &ce) || !ce.Proxy() || !strings.HasPrefix(ce.Error(), "Connection error: proxyconnect tcp: ") {
			t.Fatalf("error = %T %v, want a proxy *ConnectionError", r.err, r.err)
		}
		assertNotPrinted(t, r.err, "hunter2")
	})
	t.Run("error: a TLS-silent proxy", func(t *testing.T) {
		silent := testsupport.NewSilentListener(t)
		proxy := &url.URL{Scheme: "https", Host: silent.Addr()}
		c := mustResolve(t, noEnv, WithAPIKey(testKey), WithBaseURL("https://example.com"), WithProxy(http.ProxyURL(proxy)), WithConnectTimeout(connect))
		r := getWithin(t, c.transport, "https://example.com/v1/models", attempt, within)
		var te *TimeoutError
		if !errors.As(r.err, &te) || !te.Proxy() || te.Error() != "Request timed out on the proxy hop (timeout=7s)." {
			t.Fatalf("error = %T %v, want a proxy-hop *TimeoutError", r.err, r.err)
		}
	})
	t.Run("error: a strict TLS proxy refusing h2 is a proxy failure (R20)", func(t *testing.T) {
		proxy := testsupport.NewProxy(t, testsupport.ProxyTLSStrict, nil)
		c := mustResolve(t, noEnv, WithAPIKey(testKey), WithBaseURL("https://example.com"), WithProxy(http.ProxyURL(proxy.URL())),
			WithRootCAs(testsupport.RootCAs(t)))
		r := getWithin(t, c.transport, "https://example.com/v1/models", attempt, within)
		var ce *ConnectionError
		if !errors.As(r.err, &ce) || !ce.Proxy() || errors.Is(r.err, ErrHTTP2NotNegotiated) {
			t.Fatalf("error = %T %v, want a proxy *ConnectionError that is not ErrHTTP2NotNegotiated", r.err, r.err)
		}
	})
}

// assertMapped checks the SDK error got that transportError returned for
// err against the kind of error, its text, its proxy flag and whether it
// unwraps to err.
func assertMapped(t *testing.T, got, err error, kind, text string, proxy, unwrapsErr bool) {
	t.Helper()
	if kind == "" {
		return
	}
	if got == nil {
		t.Fatalf("transportError = nil, want a %s error", kind)
	}
	if diff := gocmp.Diff(text, got.Error()); diff != "" {
		t.Errorf("Error() (-want +got):\n%s", diff)
	}
	var gotProxy bool
	switch kind {
	case "timeout":
		te, ok := errors.AsType[*TimeoutError](got)
		if !ok {
			t.Fatalf("transportError = %T, want *TimeoutError", got)
		}
		gotProxy = te.Proxy()
	case "connection":
		ce, ok := errors.AsType[*ConnectionError](got)
		if !ok {
			t.Fatalf("transportError = %T, want *ConnectionError", got)
		}
		gotProxy = ce.Proxy()
	case "config":
		ce, ok := errors.AsType[*ConfigError](got)
		if !ok {
			t.Fatalf("transportError = %T, want *ConfigError", got)
		}
		wantLen := 1 // the sentinel alone when the cause held a credential
		if unwrapsErr {
			wantLen = 2
		}
		if u := ce.Unwrap(); len(u) != wantLen || u[0] != ErrHTTP2NotNegotiated { //nolint:errorlint // the sentinel itself comes first
			t.Errorf("Unwrap() = %v, want ErrHTTP2NotNegotiated first, %d in all", u, wantLen)
		}
		if !errors.Is(got, ErrHTTP2NotNegotiated) || errors.Is(got, h2gate.ErrNotNegotiated) != unwrapsErr {
			t.Errorf("errors.Is: ErrHTTP2NotNegotiated %t, the cause's sentinel %t; want true, %t", errors.Is(got, ErrHTTP2NotNegotiated), errors.Is(got, h2gate.ErrNotNegotiated), unwrapsErr)
		}
	}
	if gotProxy != proxy {
		t.Errorf("Proxy() = %t, want %t", gotProxy, proxy)
	}
	if u := errors.Is(got, err); u != unwrapsErr {
		t.Errorf("errors.Is(mapped, transport error) = %t, want %t", u, unwrapsErr)
	}
	if _, ok := got.(Error); !ok { //nolint:errorlint // the mapped value itself must be an SDK error
		t.Errorf("transportError = %T, not a typesafe.Error", got)
	}
}

// assertNotNegotiated checks that err, from the loopback, is a *ConfigError
// wrapping ErrHTTP2NotNegotiated, and returns it.
func assertNotNegotiated(t *testing.T, err error) *ConfigError {
	t.Helper()
	ce, ok := errors.AsType[*ConfigError](err)
	if !ok || !errors.Is(err, ErrHTTP2NotNegotiated) {
		t.Errorf("error = %T %v, want a *ConfigError wrapping ErrHTTP2NotNegotiated", err, err)
		return nil
	}
	if _, ok := errors.AsType[*h2gate.DialError](err); !ok {
		t.Errorf("error %v does not unwrap to the transport's DialError", err)
	}
	return ce
}

// TestScrubUserinfo pins the userinfo scrub of a *ConnectionError's text.
func TestScrubUserinfo(t *testing.T) {
	tests := map[string]struct {
		in, want string
		scrubbed bool
	}{
		"success: no URL":                        {in: "dial tcp 127.0.0.1:9: refused", want: "dial tcp 127.0.0.1:9: refused"},
		"success: a URL without userinfo":        {in: "proxy http://proxy.test:8080/x refused", want: "proxy http://proxy.test:8080/x refused"},
		"success: user and password":             {in: "proxy http://u:p@proxy.test:8080 refused", want: "proxy http://***@proxy.test:8080 refused", scrubbed: true}, //nolint:gosec // G101: made-up userinfo the scrub must replace.
		"success: a user alone":                  {in: "socks5://u@proxy.test", want: "socks5://***@proxy.test", scrubbed: true},
		"success: an @ in the password":          {in: "http://u:p@ss@proxy.test", want: "http://***@proxy.test", scrubbed: true},
		"success: a raw / in the password":       {in: "http://u:pa/ss@proxy.test refused", want: "http://***@proxy.test refused", scrubbed: true},
		"success: quoted":                        {in: `invalid proxy "http://u:p@proxy.test" given`, want: `invalid proxy "http://***@proxy.test" given`, scrubbed: true},
		"success: two URLs, the second with one": {in: "a http://x.test b https://u:p@y.test c", want: "a http://x.test b https://***@y.test c", scrubbed: true}, //nolint:gosec // G101: made-up userinfo the scrub must replace.
		"success: two URLs, the first with one":  {in: "a http://u:p@x.test b https://y.test/@z", want: "a http://***@x.test b https://***@z", scrubbed: true},   //nolint:gosec // G101: made-up userinfo the scrub must replace.
		"success: a scheme separator at the end": {in: "tail http://", want: "tail http://"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, scrubbed := scrubUserinfo(tt.in)
			if got != tt.want || scrubbed != tt.scrubbed {
				t.Errorf("scrubUserinfo(%q) = %q, %t; want %q, %t", tt.in, got, scrubbed, tt.want, tt.scrubbed)
			}
		})
	}
}

// closerFunc is an http.RoundTripper whose Close counts its calls and
// returns err.
type closerFunc struct {
	http.RoundTripper
	calls int
	err   error
}

// Close implements io.Closer.
func (c *closerFunc) Close() error {
	c.calls++
	return c.err
}

// TestTransportClose pins close: the SDK's transport and a WithHTTPTransport
// clone close their idle connections, a WithRoundTripper that is an
// io.Closer is closed once, and every later call returns the first result.
func TestTransportClose(t *testing.T) {
	t.Run("success: an io.Closer round tripper is closed once", func(t *testing.T) {
		boom := errors.New("close failed")
		rt := &closerFunc{RoundTripper: &testsupport.Recorder{}, err: boom}
		c := mustResolve(t, noEnv, WithAPIKey(testKey), WithRoundTripper(rt))
		for i := range 3 {
			if err := c.transport.close(); err != boom { //nolint:errorlint // identity is the assertion
				t.Errorf("close #%d = %v, want %v", i+1, err, boom)
			}
		}
		if rt.calls != 1 {
			t.Errorf("Close called %d times, want 1", rt.calls)
		}
	})
	t.Run("success: a round tripper without Close", func(t *testing.T) {
		c := mustResolve(t, noEnv, WithAPIKey(testKey), WithRoundTripper(struct{ http.RoundTripper }{&testsupport.Recorder{}}))
		if err := c.transport.close(); err != nil {
			t.Errorf("close = %v, want nil", err)
		}
	})
	t.Run("success: the SDK's transport closes its idle connection", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		c := loopbackConfig(t, srv)
		if r := getWithin(t, c.transport, srv.URL()+"/v1/models", 0, 10*time.Second); r.err != nil || r.protoMajor != 2 {
			t.Fatalf("GET = HTTP/%d %v", r.protoMajor, r.err)
		}
		for range 2 {
			if err := c.transport.close(); err != nil {
				t.Errorf("close = %v, want nil", err)
			}
		}
		deadline := time.Now().Add(5 * time.Second)
		for len(srv.LiveH2Conns()) != 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if n := len(srv.LiveH2Conns()); n != 0 {
			t.Errorf("%d connections still open after close, want 0", n)
		}
	})
}

// TestClientClosedError pins the error a closed client returns: a fresh
// *ConfigError wrapping ErrClientClosed each time.
func TestClientClosedError(t *testing.T) {
	a, b := newClientClosedError(), newClientClosedError()
	if a == b {
		t.Error("two calls returned the same *ConfigError, want a fresh one each")
	}
	if !errors.Is(a, ErrClientClosed) || a.Error() != "The client is closed." {
		t.Errorf("error = %q, errors.Is(ErrClientClosed) = %t", a.Error(), errors.Is(a, ErrClientClosed))
	}
}
