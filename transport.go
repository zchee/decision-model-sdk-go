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
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/h2gate"
)

// HTTPVersion is the HTTP version policy of a client's transport
// ([WithHTTPVersion]). Its zero value is not a policy.
type HTTPVersion int

const (
	// HTTP2Only speaks HTTP/2 alone, on one connection per API host: ALPN
	// must choose h2 on https, and http uses HTTP/2 with prior knowledge. An
	// API host that does not negotiate HTTP/2 is refused with a
	// [*ConfigError] wrapping [ErrHTTP2NotNegotiated], before any byte of
	// the request is sent. It is the default for an https base URL.
	HTTP2Only HTTPVersion = iota + 1
	// HTTPAuto lets ALPN choose between HTTP/2 and HTTP/1.1 (HTTP/1.1 on
	// http), with no ALPN refusal and no cap on connections; a cold start
	// still dials once before the requests waiting on it go out. It is the
	// default for an http base URL.
	HTTPAuto
)

// String returns the policy's name.
func (v HTTPVersion) String() string {
	switch v {
	case HTTP2Only:
		return "HTTP2Only"
	case HTTPAuto:
		return "HTTPAuto"
	default:
		return "HTTPVersion(" + strconv.Itoa(int(v)) + ")"
	}
}

// ErrHTTP2NotNegotiated reports that the API host did not speak HTTP/2
// under [HTTP2Only]: its TLS handshake chose another protocol or none, it
// refused h2, a caller TLS dialer returned a connection without h2, or a
// response came back over HTTP/1.1. The SDK returns it wrapped in a
// [*ConfigError], which retrying cannot fix: errors.Is(err,
// ErrHTTP2NotNegotiated) tells it apart. [HTTPAuto] allows HTTP/1.1.
var ErrHTTP2NotNegotiated = errors.New("typesafe: the API host did not negotiate HTTP/2")

// ErrClientClosed reports a call on a client that was closed. The SDK
// returns it wrapped in a [*ConfigError]: errors.Is(err, ErrClientClosed)
// tells it apart.
var ErrClientClosed = errors.New("typesafe: the client is closed")

// newClientClosedError returns the error a call on a closed client gets: a
// fresh *ConfigError wrapping [ErrClientClosed].
func newClientClosedError() *ConfigError {
	return newConfigError("The client is closed.", ErrClientClosed)
}

// transportOptions is what the transport options recorded. A flag records
// that an option was given, whatever its value, for the rules that forbid
// two options together.
type transportOptions struct {
	version *HTTPVersion

	rootCAs    *x509.CertPool
	rootCAsSet bool

	tlsConfig    *tls.Config
	tlsConfigSet bool

	proxy    func(*http.Request) (*url.URL, error)
	proxySet bool

	httpTransport    *http.Transport
	httpTransportSet bool

	roundTripper    http.RoundTripper
	roundTripperSet bool

	trace *httptrace.ClientTrace
}

// WithHTTPVersion sets the HTTP version policy: [HTTP2Only], the default for
// an https base URL, or [HTTPAuto], the default for an http one. Any other
// value is refused when the client is built.
func WithHTTPVersion(v HTTPVersion) ClientOption {
	return func(o *options) { o.transport.version = &v }
}

// WithRootCAs sets the certificate authorities the client trusts for the
// API host and an https proxy. A non-nil pool replaces the RootCAs of
// [WithTLSConfig]'s configuration; nil keeps that configuration's RootCAs,
// which are the system's when it sets none or when there is no
// [WithTLSConfig]. It configures the SDK's own transport, so it cannot be
// combined with [WithHTTPTransport] or [WithRoundTripper].
func WithRootCAs(pool *x509.CertPool) ClientOption {
	return func(o *options) { o.transport.rootCAs, o.transport.rootCAsSet = pool, true }
}

// WithTLSConfig sets the TLS configuration the client starts from; it is
// cloned when the option is applied. The SDK raises MinVersion to TLS 1.2 and
// runs its own ALPN check after the configuration's VerifyConnection; a
// non-nil pool given to [WithRootCAs] replaces RootCAs. It configures the
// SDK's own transport, so it cannot be combined with [WithHTTPTransport] or
// [WithRoundTripper].
func WithTLSConfig(cfg *tls.Config) ClientOption {
	return func(o *options) { o.transport.tlsConfig, o.transport.tlsConfigSet = cfg.Clone(), true }
}

// WithProxy sets the func that chooses a proxy for each request, as
// http.Transport.Proxy does. The default is http.ProxyFromEnvironment
// (HTTPS_PROXY, HTTP_PROXY, NO_PROXY); nil disables proxies. It configures
// the SDK's own transport, so it cannot be combined with [WithHTTPTransport]
// or [WithRoundTripper].
func WithProxy(proxy func(*http.Request) (*url.URL, error)) ClientOption {
	return func(o *options) { o.transport.proxy, o.transport.proxySet = proxy, true }
}

// WithHTTPTransport makes the client use a clone of t, which keeps t's
// dialers, TLS configuration and proxy; t itself is not used or changed,
// except that cloning runs t's own first-use setup as t's first request
// would. Where t leaves them zero, the SDK sets the protocols of the
// [HTTPVersion] policy, one connection per host under [HTTP2Only], the
// HTTP/2 ping timeouts and the TLS handshake timeout ([WithConnectTimeout]);
// under HTTP2Only it always sets strict HTTP/2 stream accounting
// (HTTP2Config.StrictMaxConcurrentRequests), checks ALPN on the API host's
// handshake, and checks a connection from t's TLS dialer for h2. The
// cold-start gate and the header-write token apply.
//
// It cannot be combined with [WithRoundTripper], [WithProxy], [WithRootCAs]
// or [WithTLSConfig]; [WithConnectTimeout] still applies to what the SDK
// sets. A nil t is refused.
func WithHTTPTransport(t *http.Transport) ClientOption {
	return func(o *options) { o.transport.httpTransport, o.transport.httpTransportSet = t, true }
}

// WithRoundTripper makes the client send every request through rt as it is:
// no connection policy, no ALPN check, no cold-start gate. The client's
// per-attempt deadline and response size limit still apply. Closing the
// client closes rt's idle connections when rt has a CloseIdleConnections
// method, as an [*http.Transport] has, and then closes rt when it is an
// [io.Closer], each once. rt owns its connection timeouts, so it cannot be
// combined with [WithHTTPTransport], [WithHTTPVersion], [WithRootCAs],
// [WithTLSConfig], [WithProxy] or [WithConnectTimeout]. A nil rt is refused.
//
// rt must not modify a request, as the [net/http.RoundTripper] contract
// already requires: the first attempt of every call hands it the client's
// own header map, shared by every call and never copied, so a RoundTripper
// that writes to req.Header changes the headers of later calls and races
// with concurrent ones. Each request carries its own copy of the endpoint
// URL.
func WithRoundTripper(rt http.RoundTripper) ClientOption {
	return func(o *options) { o.transport.roundTripper, o.transport.roundTripperSet = rt, true }
}

// WithClientTrace sets the net/http/httptrace hooks every request of the
// client reports to; nil removes them. The hooks are copied when the option
// is applied and run after the SDK's own, and before those of a trace the
// call's context carries (httptrace.WithClientTrace).
//
// A hook that panics, whether given here or carried by the call's context,
// does not unwind through net/http, which calls some hooks with its own
// locks held or a stream reserved: the panic is recovered inside the hook
// and raised again, with the same value, from the call that made the
// request once net/http has returned (any response body is closed first). A
// hook that panics after the call has returned is recovered and logged at
// WARN.
//
// Hooks must return promptly. The SDK's own transport (the default one and
// [WithHTTPTransport]'s clone) lets one request at a time write its headers,
// and a request's hooks run while it holds that turn, the first request on
// a new connection until its response headers arrive: a hook that blocks,
// in GotConn for example, stalls every other call on the client until each
// call's own deadline, and without a bound under [WithNoTimeout]. The
// shield above covers a panic, not a hook that does not return.
func WithClientTrace(trace *httptrace.ClientTrace) ClientOption {
	return func(o *options) {
		if trace == nil {
			o.transport.trace = nil
			return
		}
		c := *trace
		o.transport.trace = &c
	}
}

// transport is a client's HTTP transport, built once by resolve and shared
// by every request.
type transport struct {
	// rt is what every request goes through: gate, or the caller's
	// WithRoundTripper.
	rt http.RoundTripper
	// gate is the SDK's transport (the default one or WithHTTPTransport's
	// clone), nil under WithRoundTripper.
	gate *h2gate.Transport
	// idler is WithRoundTripper's rt when it has CloseIdleConnections, as
	// an *http.Transport has.
	idler interface{ CloseIdleConnections() }
	// closer is WithRoundTripper's rt when it is an io.Closer.
	closer io.Closer
	// trace is WithClientTrace's hooks, shielded per request.
	trace  *httptrace.ClientTrace
	logger *slog.Logger

	closeOnce sync.Once
	closeErr  error
}

// defaultHTTPVersion is the policy for a base URL scheme with no
// [WithHTTPVersion].
func defaultHTTPVersion(scheme string) HTTPVersion {
	if scheme == "http" {
		return HTTPAuto
	}
	return HTTP2Only
}

// conflict returns the message refusing an option given with another that
// excludes it, or "" when the options go together. Each option counts once
// given, whatever its value: WithProxy(nil) excludes WithRoundTripper as
// WithProxy(fn) does.
func (t *transportOptions) conflict(connectTimeoutSet bool) string {
	type option struct {
		given bool
		name  string
	}
	switch {
	case t.roundTripperSet && t.httpTransportSet:
		return "WithRoundTripper and WithHTTPTransport cannot be combined: each supplies the client's transport."
	case t.roundTripperSet:
		for _, o := range [...]option{
			{t.version != nil, "WithHTTPVersion"},
			{t.rootCAsSet, "WithRootCAs"},
			{t.tlsConfigSet, "WithTLSConfig"},
			{t.proxySet, "WithProxy"},
			{connectTimeoutSet, "WithConnectTimeout"},
		} {
			if o.given {
				return "WithRoundTripper cannot be combined with " + o.name +
					": the round tripper replaces the SDK's transport, which " + o.name + " configures."
			}
		}
	case t.httpTransportSet:
		for _, o := range [...]option{
			{t.rootCAsSet, "WithRootCAs"},
			{t.tlsConfigSet, "WithTLSConfig"},
			{t.proxySet, "WithProxy"},
		} {
			if o.given {
				return "WithHTTPTransport cannot be combined with " + o.name +
					": the caller's transport keeps its own TLS configuration and proxy."
			}
		}
	}
	return ""
}

// build checks what t recorded and builds the client's transport for the API
// endpoint api, with the connect timeout and the logger resolve settled on.
// Every error is a *ConfigError.
func (t *transportOptions) build(api *url.URL, connectTimeout time.Duration, connectTimeoutSet bool, logger *slog.Logger) (*transport, error) {
	if msg := t.conflict(connectTimeoutSet); msg != "" {
		return nil, newConfigError(msg)
	}
	tr := &transport{logger: logger}
	if t.trace != nil {
		c := *t.trace
		tr.trace = &c
	}
	if t.roundTripperSet {
		if t.roundTripper == nil {
			return nil, newConfigError("The round tripper passed to WithRoundTripper must not be nil.")
		}
		tr.rt = t.roundTripper
		tr.idler, _ = t.roundTripper.(interface{ CloseIdleConnections() })
		tr.closer, _ = t.roundTripper.(io.Closer)
		return tr, nil
	}
	mode := defaultHTTPVersion(api.Scheme)
	if t.version != nil {
		if *t.version != HTTP2Only && *t.version != HTTPAuto {
			return nil, newConfigError("The policy passed to WithHTTPVersion must be HTTP2Only or HTTPAuto.")
		}
		mode = *t.version
	}
	// The gate's DEBUG records print the transport's errors through the
	// credential scrub (ruling R84).
	cfg := h2gate.Config{APIURL: api, Mode: h2gate.HTTP2Only, ConnectTimeout: connectTimeout, Logger: logger, ErrorText: logErrorText}
	if mode == HTTPAuto {
		cfg.Mode = h2gate.HTTPAuto
	}
	var (
		gate *h2gate.Transport
		err  error
	)
	if t.httpTransportSet {
		if t.httpTransport == nil {
			return nil, newConfigError("The transport passed to WithHTTPTransport must not be nil.")
		}
		gate, err = h2gate.Wrap(t.httpTransport, cfg)
	} else {
		cfg.RootCAs, cfg.TLSConfig = t.rootCAs, t.tlsConfig
		cfg.Proxy = http.ProxyFromEnvironment
		if t.proxySet {
			cfg.Proxy = t.proxy
		}
		gate, err = h2gate.NewTransport(cfg)
	}
	if err != nil {
		return nil, buildError(err)
	}
	tr.rt, tr.gate = gate, gate
	return tr, nil
}

// buildError words a failure of the h2gate build for the option that caused
// it. It never repeats the host or a proxy URL, which may carry
// credentials, and wraps nothing whose text could.
func buildError(err error) *ConfigError {
	switch {
	case errors.Is(err, h2gate.ErrNonASCIIHost):
		return newConfigError("The base URL's host must be ASCII under HTTP2Only (write an internationalised name in its xn-- form), or use WithHTTPVersion(HTTPAuto).")
	case errors.Is(err, h2gate.ErrCallerHTTP2):
		return newConfigError("The transport passed to WithHTTPTransport carries its own HTTP/2 implementation in TLSNextProto, which HTTP2Only cannot check; remove it or use WithHTTPVersion(HTTPAuto).")
	case errors.Is(err, h2gate.ErrProxyEnvironment):
		return newConfigError("The proxy environment variables (HTTPS_PROXY, HTTP_PROXY) hold a proxy URL that cannot be used; fix them, or choose the proxy with WithProxy.")
	default:
		// h2gate's other build errors carry no URL.
		return newConfigError("The client's transport cannot be built.", err)
	}
}

// roundTrip sends req through the transport. timeout is the attempt's
// deadline, which a *TimeoutError reports. A failure of the SDK's transport
// before a connection was had, and an API host that did not speak HTTP/2
// under HTTP2Only, come back as the SDK's error types (section 6.3,
// R67 Q3); any other error is returned as the transport gave it.
func (t *transport) roundTrip(req *http.Request, timeout time.Duration) (*http.Response, error) {
	var sh *shield
	if trace := t.callerTrace(req.Context()); trace != nil {
		// net/http composes every trace on the context into the one it calls,
		// so the context net/http sees carries the shielded trace alone
		// (K28c).
		sh = newShield(req.Context(), trace, t.logger)
		defer sh.markReturned()
		req = req.WithContext(httptrace.WithClientTrace(untracedContext{req.Context()}, &sh.trace))
	}
	resp, err := t.rt.RoundTrip(req)
	if sh != nil {
		sh.done(resp)
	}
	if err != nil {
		if mapped := transportError(err, timeout, req.Header); mapped != nil {
			return nil, mapped
		}
		return nil, err
	}
	return resp, nil
}

// callerTrace returns the caller's hooks that reach a request with context
// ctx: WithClientTrace's, then those of a trace on ctx, composed as
// httptrace composes them, or nil when there are none. It never changes
// t.trace or ctx's trace.
func (t *transport) callerTrace(ctx context.Context) *httptrace.ClientTrace {
	onCtx := httptrace.ContextClientTrace(ctx)
	switch {
	case onCtx == nil:
		return t.trace
	case t.trace == nil:
		return onCtx
	}
	// WithClientTrace composes the trace it is given with the one already on
	// the context, in place; composing into a copy of the option's trace
	// leaves both originals as they were.
	merged := *t.trace
	httptrace.WithClientTrace(httptrace.WithClientTrace(context.Background(), onCtx), &merged)
	return &merged
}

// untracedContext is a request context without its httptrace values: net/http
// finds neither the caller's trace nor the net-level hooks httptrace derived
// from it, and composes only the shielded trace roundTrip installs above it.
// Deadline, cancellation, cause and every other value are the wrapped
// context's; context.WithCancel on it still finds the wrapped context's
// cancellation through Value, so it starts no goroutine.
type untracedContext struct{ context.Context }

// traceKeys answers, with a non-nil value, for exactly the context keys
// httptrace.WithClientTrace sets: its client-event key and, for a trace
// with a net-level hook, the key of the net package's hooks.
var traceKeys = httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{ConnectStart: func(string, string) {}})

// Value returns nil for httptrace's keys and the wrapped context's value for
// every other key.
func (c untracedContext) Value(key any) any {
	if traceKeys.Value(key) != nil {
		return nil
	}
	return c.Context.Value(key)
}

// close releases the transport, once: the SDK's transport and a caller's
// WithHTTPTransport clone close their idle connections; a WithRoundTripper
// closes its idle connections when it has CloseIdleConnections, as an
// *http.Transport has, and is then closed when it is an io.Closer (AC-F9,
// R79). Later calls return the first call's result.
func (t *transport) close() error {
	t.closeOnce.Do(func() {
		if t.gate != nil {
			t.gate.CloseIdleConnections()
			return
		}
		if t.idler != nil {
			t.idler.CloseIdleConnections()
		}
		if t.closer != nil {
			t.closeErr = t.closer.Close()
		}
	})
	return t.closeErr
}

// stats returns the SDK transport's counters, or zero values under
// WithRoundTripper.
func (t *transport) stats() h2gate.Stats {
	if t.gate == nil {
		return h2gate.Stats{}
	}
	return t.gate.Stats()
}

// transportError maps an error of the SDK's transport to the SDK's error
// types, or returns nil for an error it leaves to the attempt's
// classification ([Client.attemptError]). h is the header of the request
// that failed. The order is R67 Q3's: a proxy hop that timed out is a
// *TimeoutError naming the proxy hop; any other proxy failure is a
// *ConnectionError with Proxy() true, even when the proxy refused h2 (R20);
// a failure to speak HTTP/2 is a *ConfigError wrapping
// [ErrHTTP2NotNegotiated]; a dial or TLS handshake that timed out is a
// *TimeoutError; any other failure before a connection is a
// *ConnectionError. No text of a mapped error shows a credential of the
// request or a URL's userinfo ([credentials.redact]), and each wraps the
// transport's error, or a stand-in for it when its chain printed one
// ([credentials.cause]).
func transportError(err error, timeout time.Duration, h http.Header) error {
	de, isDial := errors.AsType[*h2gate.DialError](err)
	if !isDial && !errors.Is(err, h2gate.ErrNotNegotiated) {
		return nil
	}
	creds := requestCredentials(h)
	switch {
	case isDial && de.Proxy && de.Timeout:
		return newProxyTimeoutError(timeout, creds.cause(err))
	case isDial && de.Proxy:
		text, _ := creds.redact(de.Err.Error())
		return newConnectionError(text, creds.cause(err), true)
	case errors.Is(err, h2gate.ErrNotNegotiated):
		detail := err.Error()
		if isDial {
			detail = de.Err.Error()
		}
		detail, _ = creds.redact(strings.TrimPrefix(detail, h2gate.ErrNotNegotiated.Error()+": "))
		msg := "The API host did not negotiate HTTP/2, which HTTP2Only requires (" + safeMessage(detail) +
			"); WithHTTPVersion(HTTPAuto) allows HTTP/1.1."
		return newConfigError(msg, ErrHTTP2NotNegotiated, creds.cause(err))
	case de.Timeout:
		return newTimeoutError(timeout, creds.cause(err))
	default:
		text, _ := creds.redact(de.Err.Error())
		return newConnectionError(text, creds.cause(err), false)
	}
}

// scrubUserinfo replaces the userinfo of every URL in s ("scheme://user@" or
// "scheme://user:password@") with "***", and reports whether it replaced
// any. A URL's authority is taken to run to the next whitespace or quote, not
// to the next "/": a password written with a raw "/" is still scrubbed, at
// the cost of scrubbing a path that holds an "@".
func scrubUserinfo(s string) (string, bool) {
	var b strings.Builder
	rest, scrubbed := s, false
	for {
		i := strings.Index(rest, "://")
		if i < 0 {
			break
		}
		b.WriteString(rest[:i+3])
		rest = rest[i+3:]
		end := strings.IndexAny(rest, " \t\n\"'<>")
		if end < 0 {
			end = len(rest)
		}
		if at := strings.LastIndexByte(rest[:end], '@'); at >= 0 {
			b.WriteString("***")
			rest, scrubbed = rest[at:], true
		}
	}
	if !scrubbed {
		return s, false
	}
	b.WriteString(rest)
	return b.String(), true
}

// shield wraps a caller's httptrace hooks for one request (K28, K28b, K28c):
// a hook that panics inside net/http, which may hold its connection pool's
// lock or a reserved stream there, is recovered in place, and done raises the
// panic again on the goroutine that called RoundTrip. A panic it does not
// raise is logged at WARN with its hook and stack, never its value, which
// may hold data.
type shield struct {
	trace  httptrace.ClientTrace
	ctx    context.Context // the request's, for the log records
	logger *slog.Logger

	mu       sync.Mutex
	returned bool
	panicked bool
	hook     string // the hook of the kept panic
	value    any
	stack    []byte
}

// newShield returns a shield for a request with context ctx whose trace
// calls every hook of c, recovered.
func newShield(ctx context.Context, c *httptrace.ClientTrace, logger *slog.Logger) *shield {
	s := &shield{ctx: ctx, logger: logger}
	if h := c.GetConn; h != nil {
		s.trace.GetConn = func(hostPort string) { defer s.recover("GetConn"); h(hostPort) }
	}
	if h := c.GotConn; h != nil {
		s.trace.GotConn = func(info httptrace.GotConnInfo) { defer s.recover("GotConn"); h(info) }
	}
	if h := c.PutIdleConn; h != nil {
		s.trace.PutIdleConn = func(err error) { defer s.recover("PutIdleConn"); h(err) }
	}
	if h := c.GotFirstResponseByte; h != nil {
		s.trace.GotFirstResponseByte = func() { defer s.recover("GotFirstResponseByte"); h() }
	}
	if h := c.Got100Continue; h != nil {
		s.trace.Got100Continue = func() { defer s.recover("Got100Continue"); h() }
	}
	if h := c.Got1xxResponse; h != nil {
		s.trace.Got1xxResponse = func(code int, header textproto.MIMEHeader) (err error) {
			defer s.recover("Got1xxResponse")
			return h(code, header)
		}
	}
	if h := c.DNSStart; h != nil {
		s.trace.DNSStart = func(info httptrace.DNSStartInfo) { defer s.recover("DNSStart"); h(info) }
	}
	if h := c.DNSDone; h != nil {
		s.trace.DNSDone = func(info httptrace.DNSDoneInfo) { defer s.recover("DNSDone"); h(info) }
	}
	if h := c.ConnectStart; h != nil {
		s.trace.ConnectStart = func(network, addr string) { defer s.recover("ConnectStart"); h(network, addr) }
	}
	if h := c.ConnectDone; h != nil {
		s.trace.ConnectDone = func(network, addr string, err error) { defer s.recover("ConnectDone"); h(network, addr, err) }
	}
	if h := c.TLSHandshakeStart; h != nil {
		s.trace.TLSHandshakeStart = func() { defer s.recover("TLSHandshakeStart"); h() }
	}
	if h := c.TLSHandshakeDone; h != nil {
		s.trace.TLSHandshakeDone = func(cs tls.ConnectionState, err error) { defer s.recover("TLSHandshakeDone"); h(cs, err) }
	}
	if h := c.WroteHeaderField; h != nil {
		s.trace.WroteHeaderField = func(key string, value []string) { defer s.recover("WroteHeaderField"); h(key, value) }
	}
	if h := c.WroteHeaders; h != nil {
		s.trace.WroteHeaders = func() { defer s.recover("WroteHeaders"); h() }
	}
	if h := c.Wait100Continue; h != nil {
		s.trace.Wait100Continue = func() { defer s.recover("Wait100Continue"); h() }
	}
	if h := c.WroteRequest; h != nil {
		s.trace.WroteRequest = func(info httptrace.WroteRequestInfo) { defer s.recover("WroteRequest"); h(info) }
	}
	return s
}

// recover is deferred by every wrapped hook: it stops a panic of the hook,
// keeps the first one before RoundTrip returned for done, and logs any
// other.
func (s *shield) recover(hook string) {
	v := recover()
	if v == nil {
		return
	}
	stack := debug.Stack()
	s.mu.Lock()
	late, first := s.returned, !s.panicked
	if !late && first {
		s.panicked, s.hook, s.value, s.stack = true, hook, v, stack
	}
	s.mu.Unlock()
	switch {
	case late:
		s.log("transport: trace hook panic after the request returned, recovered", hook, stack)
	case !first:
		s.log("transport: trace hook panic after an earlier one, recovered", hook, stack)
	}
}

// done ends the request's shield on the goroutine that called RoundTrip,
// once RoundTrip has returned: when a hook panicked, it closes resp's body,
// if any, logs the hook and its stack, and panics again with the hook's
// value.
func (s *shield) done(resp *http.Response) {
	s.mu.Lock()
	s.returned = true
	panicked, hook, v, stack := s.panicked, s.hook, s.value, s.stack
	s.mu.Unlock()
	if !panicked {
		return
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	s.log("transport: trace hook panic, raised again on the caller", hook, stack)
	panic(v)
}

// markReturned ends the request's shield when RoundTrip itself panicked, so
// done never ran; roundTrip defers it. A hook panic after it is logged, and
// one kept before it, which RoundTrip's panic replaces on the caller, is
// logged rather than lost. After done it does nothing.
func (s *shield) markReturned() {
	s.mu.Lock()
	if s.returned {
		s.mu.Unlock()
		return
	}
	s.returned = true
	panicked, hook, stack := s.panicked, s.hook, s.stack
	s.mu.Unlock()
	if panicked {
		s.log("transport: trace hook panic replaced by a RoundTrip panic, recovered", hook, stack)
	}
}

// log writes a WARN record about a hook's panic: the hook and the stack of
// the goroutine it panicked on.
func (s *shield) log(msg, hook string, stack []byte) {
	s.logger.LogAttrs(s.ctx, slog.LevelWarn, msg, slog.String("hook", hook), slog.String("stack", string(stack)))
}
