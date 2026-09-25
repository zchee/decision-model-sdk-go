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
// API host and an https proxy; nil means the system's, the default. It
// configures the SDK's own transport, so it cannot be combined with
// [WithHTTPTransport] or [WithRoundTripper].
func WithRootCAs(pool *x509.CertPool) ClientOption {
	return func(o *options) { o.transport.rootCAs, o.transport.rootCAsSet = pool, true }
}

// WithTLSConfig sets the TLS configuration the client starts from; it is
// cloned when the option is applied. The SDK raises MinVersion to TLS 1.2 and
// runs its own ALPN check after the configuration's VerifyConnection;
// [WithRootCAs], when also given, replaces RootCAs. It configures the SDK's
// own transport, so it cannot be combined with [WithHTTPTransport] or
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
// per-attempt deadline and response size limit still apply, and closing the
// client closes rt, once, when it is an [io.Closer]. rt owns its connection timeouts, so it
// cannot be combined with [WithHTTPTransport], [WithHTTPVersion],
// [WithRootCAs], [WithTLSConfig], [WithProxy] or [WithConnectTimeout]. A nil
// rt is refused.
func WithRoundTripper(rt http.RoundTripper) ClientOption {
	return func(o *options) { o.transport.roundTripper, o.transport.roundTripperSet = rt, true }
}

// WithClientTrace sets the net/http/httptrace hooks every request of the
// client reports to; nil removes them. The hooks are copied when the option
// is applied and run after the SDK's own. A hook that panics does not
// unwind through net/http, which calls some hooks with its own locks held
// or a stream reserved: the panic is recovered inside the hook and raised
// again, with the same value, from the call that made the request once
// net/http has returned (any response body is closed first). A hook that
// panics after the call has returned is recovered and logged at WARN.
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
	cfg := h2gate.Config{APIURL: api, Mode: h2gate.HTTP2Only, ConnectTimeout: connectTimeout, Logger: logger}
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
	if t.trace != nil {
		sh = newShield(t.trace, t.logger)
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &sh.trace))
	}
	resp, err := t.rt.RoundTrip(req)
	if sh != nil {
		sh.done(resp)
	}
	if err != nil {
		if mapped := transportError(err, timeout); mapped != nil {
			return nil, mapped
		}
		return nil, err
	}
	return resp, nil
}

// close releases the transport, once: the SDK's transport and a caller's
// WithHTTPTransport clone close their idle connections, and a
// WithRoundTripper that is an io.Closer is closed. Later calls return the
// first call's result.
func (t *transport) close() error {
	t.closeOnce.Do(func() {
		switch {
		case t.gate != nil:
			t.gate.CloseIdleConnections()
		case t.closer != nil:
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
// classification (W2.5). The order is R67 Q3's: a proxy hop that timed out
// is a *TimeoutError naming the proxy hop; any other proxy failure is a
// *ConnectionError with Proxy() true, even when the proxy refused h2 (R20);
// a failure to speak HTTP/2 is a *ConfigError wrapping
// [ErrHTTP2NotNegotiated]; a dial or TLS handshake that timed out is a
// *TimeoutError; any other failure before a connection is a
// *ConnectionError.
func transportError(err error, timeout time.Duration) error {
	de, isDial := errors.AsType[*h2gate.DialError](err)
	switch {
	case isDial && de.Proxy && de.Timeout:
		return newProxyTimeoutError(timeout, err)
	case isDial && de.Proxy:
		text, cause := connectionText(de.Err, err)
		return newConnectionError(text, cause, true)
	case errors.Is(err, h2gate.ErrNotNegotiated):
		detail := err.Error()
		if isDial {
			detail = de.Err.Error()
		}
		detail = strings.TrimPrefix(detail, h2gate.ErrNotNegotiated.Error()+": ")
		return newConfigError("The API host did not negotiate HTTP/2, which HTTP2Only requires ("+
			safeMessage(detail)+"); WithHTTPVersion(HTTPAuto) allows HTTP/1.1.", ErrHTTP2NotNegotiated, err)
	case isDial && de.Timeout:
		return newTimeoutError(timeout, err)
	case isDial:
		text, cause := connectionText(de.Err, err)
		return newConnectionError(text, cause, false)
	default:
		return nil
	}
}

// connectionText returns the text a *ConnectionError shows for the
// transport's error inner, and the error it unwraps to: err, or nil when the
// text held a credential. A URL with userinfo (a proxy URL's
// user:password@) is the one credential a transport error's text can carry
// here, so its userinfo is replaced with "***"; the scrub of the rest of a
// transport error's text is the attempt classification's (W2.5).
func connectionText(inner, err error) (string, error) {
	text, scrubbed := scrubUserinfo(inner.Error())
	if scrubbed {
		return text, nil
	}
	return text, err
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

// shield wraps a caller's httptrace hooks for one request (K28, K28b): a
// hook that panics inside net/http, which may hold its connection pool's
// lock or a reserved stream there, is recovered in place, and done raises the
// panic again on the goroutine that called RoundTrip.
type shield struct {
	trace  httptrace.ClientTrace
	logger *slog.Logger

	mu       sync.Mutex
	returned bool
	panicked bool
	value    any
}

// newShield returns a shield whose trace calls every hook of c, recovered.
func newShield(c *httptrace.ClientTrace, logger *slog.Logger) *shield {
	s := &shield{logger: logger}
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

// recover is deferred by every wrapped hook: it stops a panic of the hook and
// keeps the first one for done, or logs one that comes after done.
func (s *shield) recover(hook string) {
	v := recover()
	if v == nil {
		return
	}
	s.mu.Lock()
	late := s.returned
	if !late && !s.panicked {
		s.panicked, s.value = true, v
	}
	s.mu.Unlock()
	if late {
		s.logger.LogAttrs(context.Background(), slog.LevelWarn, "transport: trace hook panic after the request returned, recovered",
			slog.String("hook", hook))
	}
}

// done ends the request's shield on the goroutine that called RoundTrip:
// when a hook panicked, it closes resp's body, if any, and panics again with
// the hook's value.
func (s *shield) done(resp *http.Response) {
	s.mu.Lock()
	s.returned = true
	panicked, v := s.panicked, s.value
	s.mu.Unlock()
	if !panicked {
		return
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	panic(v)
}
