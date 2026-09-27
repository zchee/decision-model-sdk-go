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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/engine"
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

	// compression is WithCompression's value, nil when it was not given.
	compression *bool

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
// or [WithRoundTripper]. The SDK scrubs the credentials of every proxy the
// func returned, the 16 most recent, from the client's transport errors,
// whatever their length ([ConnectionError] says what a short one costs),
// and the whole ones of 8 bytes or more from the header of a response to a
// plain-HTTP request that went through one, which the proxy may have
// written itself ([APIError.Header]).
func WithProxy(proxy func(*http.Request) (*url.URL, error)) ClientOption {
	return func(o *options) { o.transport.proxy, o.transport.proxySet = proxy, true }
}

// WithHTTPTransport makes the client use a clone of t, which keeps t's
// dialers, TLS configuration, proxy and compression setting
// (DisableCompression); t itself is not used or changed, except that cloning
// runs t's own first-use setup as t's first request would. Where t leaves
// them zero, the SDK sets the protocols of the [HTTPVersion] policy, one
// connection per host under [HTTP2Only], the HTTP/2 ping timeouts and the TLS
// handshake timeout ([WithConnectTimeout]); under HTTP2Only it always sets
// strict HTTP/2 stream accounting (HTTP2Config.StrictMaxConcurrentRequests),
// checks ALPN on the API host's handshake, and checks a connection from t's
// TLS dialer for h2. The cold-start gate and the header-write token apply.
//
// It cannot be combined with [WithRoundTripper], [WithProxy], [WithRootCAs],
// [WithTLSConfig] or [WithCompression]; [WithConnectTimeout] still applies
// to what the SDK sets. A nil t is refused.
func WithHTTPTransport(t *http.Transport) ClientOption {
	return func(o *options) { o.transport.httpTransport, o.transport.httpTransportSet = t, true }
}

// WithCompression sets whether requests ask the API to compress its
// responses, with Accept-Encoding: gzip, as Go's transport and
// typesafe-sdk-python's httpx do by default. The default, true, keeps that:
// the transport undoes the API's gzip before the SDK reads the body, which
// then has no declared length. false sends no Accept-Encoding, so the API
// sends the body as it is with its length declared, which on this API's
// small bodies saves the client about 7 allocations and 10 KiB per call
// (docs/support.md). It configures the SDK's own transport, so, whatever
// its value, it cannot be combined with [WithHTTPTransport], whose
// transport keeps its own DisableCompression, or [WithRoundTripper].
func WithCompression(enabled bool) ClientOption {
	return func(o *options) { o.transport.compression = &enabled }
}

// WithRoundTripper makes the client send every request through rt as it is:
// no connection policy, no ALPN check, no cold-start gate. The client's
// per-attempt deadline and response size limit still apply. Closing the
// client closes rt's idle connections when rt has a CloseIdleConnections
// method, as an [*http.Transport] has, and then closes rt when it is an
// [io.Closer], each once. rt owns its connection timeouts, so it cannot be
// combined with [WithHTTPTransport], [WithHTTPVersion], [WithRootCAs],
// [WithTLSConfig], [WithProxy], [WithConnectTimeout] or [WithCompression]. A
// nil rt is refused.
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
// a new connection until its response headers arrive. How long a hook that
// blocks holds every other call on the client depends on where it blocks:
//
//   - In GotConn or a later hook, until its response headers: for up to
//     the connect timeout plus the TLS handshake timeout (20 s by default),
//     after which the waiting calls go out without their turn.
//   - In GetConn, while the client has no connection yet: for up to the
//     wait for its first connection (the same two timeouts, and a proxy's
//     CONNECT and handshake when a proxy may apply) plus the bound above.
//   - In GetConn, once the client has its connection: until the hook
//     returns, past the other calls' own deadlines. net/http calls GetConn
//     with its connection pool's lock held (risk K28), so a call that has
//     waited out the bound above then waits for that lock, which its
//     context cannot interrupt.
//   - In the DNS, connect or TLS handshake hooks of a new connection
//     (DNSStart, DNSDone, ConnectStart, ConnectDone, TLSHandshakeStart,
//     TLSHandshakeDone): for up to the bound above plus the wait for a
//     connection above, under [WithNoTimeout] too. The call that dials, and
//     every call that waits for a connection meanwhile, then fails with a
//     [*TimeoutError] while the hook runs on. The default transport's dial,
//     which runs the DNS and connect hooks, ends at the connect timeout
//     plus a grace of 100 ms, so the next call can dial again; a TLS
//     handshake hook, or a hook that blocks the dialer of
//     [WithHTTPTransport]'s transport, keeps the client's one connection to
//     the host from being made until it returns.
//
// A hook the bound leaves running goes on with its dial, beside the calls
// that come after it: the rest of that dial's hooks run once it returns,
// possibly at the same time as the hooks of a later call's new connection,
// as net/http/httptrace allows, and a panic the hook raises then is
// recovered and logged at WARN, as the panic of any hook that runs after
// its call has returned.
//
// The shield above covers a panic, and the bounds a hook that blocks,
// except GetConn on a client that has a connection.
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
			{t.compression != nil, "WithCompression"},
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
		if t.compression != nil {
			return "WithHTTPTransport cannot be combined with WithCompression: the caller's transport keeps its own DisableCompression."
		}
	}
	return ""
}

// build checks what t recorded and builds the client's transport for the API
// endpoint api, with the connect timeout and the logger resolve settled on.
// Every error is a *ConfigError.
func (t *transportOptions) build(api *url.URL, connectTimeout time.Duration, connectTimeoutSet bool, logger *slog.Logger) (*engine.Transport, error) {
	if msg := t.conflict(connectTimeoutSet); msg != "" {
		return nil, newConfigError(msg)
	}
	tr := &engine.Transport{Logger: logger}
	if t.trace != nil {
		c := *t.trace
		tr.Trace = &c
	}
	if t.roundTripperSet {
		if t.roundTripper == nil {
			return nil, newConfigError("The round tripper passed to WithRoundTripper must not be nil.")
		}
		tr.RT = t.roundTripper
		tr.Idler, _ = t.roundTripper.(interface{ CloseIdleConnections() })
		tr.Closer, _ = t.roundTripper.(io.Closer)
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
	cfg := h2gate.Config{APIURL: api, Mode: h2gate.HTTP2Only, ConnectTimeout: connectTimeout, Logger: logger, ErrorText: tr.ErrorText}
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
		tr.Proxies = new(engine.ProxyCreds)
		cfg.OnProxy = tr.Proxies.Record
		cfg.DisableCompression = t.compression != nil && !*t.compression
		gate, err = h2gate.NewTransport(cfg)
	}
	if err != nil {
		return nil, buildError(err)
	}
	tr.RT, tr.Gate = gate, gate
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

// roundTrip sends req through t. timeout is the attempt's deadline, which a
// *TimeoutError reports. A failure of the SDK's transport before a
// connection was had, and an API host that did not speak HTTP/2 under
// HTTP2Only, come back as the SDK's error types (section 6.3, R67 Q3); any
// other error is returned as the transport gave it.
func roundTrip(t *engine.Transport, req *http.Request, timeout time.Duration) (*http.Response, error) {
	resp, err := t.RoundTrip(req)
	if err != nil {
		if mapped := transportError(err, timeout, t.Credentials(req)); mapped != nil {
			return nil, mapped
		}
		return nil, err
	}
	return resp, nil
}

// transportError maps an error of the SDK's transport to the SDK's error
// types, or returns nil for an error it leaves to the attempt's
// classification ([Client.attemptError]). creds are the credentials of the
// call that failed ([engine.Transport.Credentials]). The order is R67 Q3's:
// a proxy hop that timed out is a *TimeoutError naming the proxy hop; any
// other proxy failure is a *ConnectionError with Proxy() true, even when
// the proxy refused h2 (R20);
// a failure to speak HTTP/2 is a *ConfigError wrapping
// [ErrHTTP2NotNegotiated]; a dial or TLS handshake that timed out is a
// *TimeoutError; any other failure before a connection is a
// *ConnectionError. No text of a mapped error shows a credential of the
// request or a URL's userinfo ([engine.Credentials.Redact]), and each wraps the
// transport's error, or a stand-in for it when its chain printed one
// ([engine.Credentials.Cause]).
func transportError(err error, timeout time.Duration, creds engine.Credentials) error {
	de, isDial := errors.AsType[*h2gate.DialError](err)
	if !isDial && !errors.Is(err, h2gate.ErrNotNegotiated) {
		return nil
	}
	switch {
	case isDial && de.Proxy && de.Timeout:
		return newProxyTimeoutError(timeout, creds.Cause(err))
	case isDial && de.Proxy:
		text, _ := creds.Redact(de.Err.Error())
		return newConnectionError(text, creds.Cause(err), true)
	case errors.Is(err, h2gate.ErrNotNegotiated):
		detail := err.Error()
		if isDial {
			detail = de.Err.Error()
		}
		detail, _ = creds.Redact(strings.TrimPrefix(detail, h2gate.ErrNotNegotiated.Error()+": "))
		msg := "The API host did not negotiate HTTP/2, which HTTP2Only requires (" + safeMessage(detail) +
			"); WithHTTPVersion(HTTPAuto) allows HTTP/1.1."
		return newConfigError(msg, ErrHTTP2NotNegotiated, creds.Cause(err))
	case de.Timeout:
		return newTimeoutError(timeout, creds.Cause(err))
	default:
		text, _ := creds.Redact(de.Err.Error())
		return newConnectionError(text, creds.Cause(err), false)
	}
}
