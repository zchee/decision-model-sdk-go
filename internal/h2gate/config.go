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
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

// Mode is the HTTP version policy of a [Transport].
type Mode int

const (
	// HTTP2Only speaks HTTP/2 alone: negotiated by ALPN on https, with prior
	// knowledge on http. One connection per API host (MaxConnsPerHost 1),
	// and an API hop that does not negotiate h2 is refused with
	// [ErrNotNegotiated] before any byte of a request.
	HTTP2Only Mode = iota
	// HTTPAuto lets ALPN choose between HTTP/2 and HTTP/1.1 (plain HTTP/1.1 on
	// http), with no connection cap and no refusal. The gate and the token
	// stay. The token is taken before the protocol is known and given back
	// at GotConn on an HTTP/1.1 connection, so it spans each new HTTP/1.1
	// dial and handshake: new HTTP/1.1 connections are dialled one at a
	// time, and a burst that needs n of them pays about n dial-and-handshake
	// latencies (8 calls at a 50 ms dial: 366 ms against 160 ms for the
	// stock transport, review W2.2A; on loopback the cost is a few ms).
	HTTPAuto
)

// String returns the mode's name.
func (m Mode) String() string {
	switch m {
	case HTTP2Only:
		return "HTTP2Only"
	case HTTPAuto:
		return "HTTPAuto"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// DefaultConnectTimeout is the connect timeout of a [Config] that sets none.
const DefaultConnectTimeout = 10 * time.Second

// connectTimeout returns cfg's connect timeout, the default when unset.
func connectTimeout(cfg Config) time.Duration {
	if cfg.ConnectTimeout <= 0 {
		return DefaultConnectTimeout
	}
	return cfg.ConnectTimeout
}

// The stock transport's settings that [NewTransport] fixes (port plan section
// 6.3).
const (
	sendPingTimeout = 30 * time.Second
	pingTimeout     = 15 * time.Second
	idleConnTimeout = 90 * time.Second
	// proxyConnectLimit is the stock transport's bound on a CONNECT exchange
	// (GOROOT/src/net/http/transport.go:1995).
	proxyConnectLimit = time.Minute
)

// Logger receives the transport's events: DEBUG "h2: dial", "h2: gate
// release", "h2: gate error" (with a reason), "h2: redial error", "h2:
// token wait expired", "h2: dial bound expired" and "h2: connection wait
// expired" (each with its bound; the dial bound's is the connect timeout
// plus a grace of 100 ms), and WARN "h2: response not HTTP/2". A
// *log/slog.Logger satisfies it.
//
// A Logger that also has the method Enabled(context.Context, slog.Level)
// bool, as a *slog.Logger has, is asked before an event that prints an
// error, and the error is rendered ([Config.ErrorText]) only when it keeps
// DEBUG events; a Logger without the method gets every event rendered.
type Logger interface {
	DebugContext(ctx context.Context, msg string, args ...any)
	WarnContext(ctx context.Context, msg string, args ...any)
}

// levelEnabler is the optional method of a [Logger] that says whether it
// keeps events at a level.
type levelEnabler interface {
	Enabled(ctx context.Context, level slog.Level) bool
}

// Config configures [NewTransport] and [Wrap].
type Config struct {
	// APIURL is the API's base URL. Its scheme (http or https) and host
	// decide the protocols, the address the thin dialer check applies to and
	// the proxy decision. Required.
	APIURL *url.URL
	// Mode is the HTTP version policy.
	Mode Mode
	// ConnectTimeout bounds the TCP dial and, as TLSHandshakeTimeout, every
	// TLS handshake of the transport NewTransport builds; zero or less means
	// DefaultConnectTimeout. Wrap keeps a caller transport's own dial bound
	// and uses ConnectTimeout for the TLSHandshakeTimeout it fills in, the
	// thin check's bound and the gate's bounds.
	ConnectTimeout time.Duration
	// Logger receives the transport's events; nil discards them.
	Logger Logger
	// ErrorText renders the error that a DEBUG event prints, "h2: gate
	// error" and "h2: redial error", for req, the request whose connection
	// failed; nil renders err.Error(). The text is written by code the
	// transport does not control (a caller's dialer, a proxy, net/http) and
	// may repeat a credential of req's header, so the root package scrubs
	// it here (ruling R84). It runs only for an event the Logger keeps:
	// never with a nil Logger, nor when the Logger's Enabled method leaves
	// DEBUG out.
	ErrorText func(req *http.Request, err error) string

	// The fields below configure the transport NewTransport builds. Wrap
	// keeps the caller transport's own dialer, TLS configuration and proxy,
	// and refuses a Config that sets any of them.

	// RootCAs, when set, replaces TLSConfig's RootCAs.
	RootCAs *x509.CertPool
	// TLSConfig is the base TLS configuration; it is cloned, and its
	// MinVersion is raised to TLS 1.2. Its VerifyConnection, if any, runs
	// before the ALPN check.
	TLSConfig *tls.Config
	// Proxy selects a proxy per request, as http.Transport.Proxy; nil uses
	// none. http.ProxyFromEnvironment (recognised by function identity) and
	// nil are decided once, at build; any other func may apply a proxy, and
	// the ALPN check then recognises the API hop by its SNI.
	Proxy func(*http.Request) (*url.URL, error)
	// OnProxy, when set, is called with every URL Proxy returns without an
	// error, each time the stock transport asks Proxy: once per attempt at a
	// connection (GOROOT/src/net/http/transport.go:1051-1058), never for a
	// request sent on an HTTP/2 connection the stock transport holds, and
	// never for a waiter at the gate, which shares its leader's dial. The
	// root package records the proxy's credential there for its scrub
	// instead of asking Proxy again (review W6.2 MIN-4, P6 critic m-2). It
	// runs on the goroutine of RoundTrip, and must be safe for concurrent
	// use.
	OnProxy func(proxy *url.URL)
	// DialContext dials TCP connections; nil uses a net.Dialer. Either way
	// the dial is bounded by ConnectTimeout.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Build-time refusals. Every error NewTransport or Wrap returns is a
// configuration error. The exported ones are those a caller's settings can
// cause, which the root package words for the option that caused them; the
// others follow from a Config the root package never builds.
var (
	// ErrNonASCIIHost refuses an API host with a non-ASCII byte under
	// HTTP2Only: the API hop's SNI and the dial key are built from its IDNA
	// form, which this package cannot compute without x/net/idna.
	ErrNonASCIIHost = errors.New("h2gate: HTTP2Only needs an ASCII API host")
	// ErrCallerHTTP2 refuses, under HTTP2Only, a caller transport whose
	// TLSNextProto carries its own "h2" entry (an x/net install), which the
	// stock h2 selection and the ALPN check would not see.
	ErrCallerHTTP2 = errors.New("h2gate: HTTP2Only cannot use a transport whose TLSNextProto carries its own h2")
	// ErrProxyEnvironment reports that http.ProxyFromEnvironment failed for
	// the API URL at build: a proxy variable holds an unusable URL. The
	// error wraps the failure, whose text may hold that URL.
	ErrProxyEnvironment = errors.New("h2gate: the proxy environment failed for the API URL")

	errBadURL     = errors.New("h2gate: the API URL needs an http or https scheme and a host")
	errWrapConfig = errors.New("h2gate: Wrap keeps the caller transport's dialer, TLS configuration and proxy")
)

// alpnScope says which TLS handshakes the ALPN check applies to.
type alpnScope int

const (
	// scopeNone checks nothing: HTTPAuto, or an http API URL.
	scopeNone alpnScope = iota
	// scopeEvery checks every handshake: no proxy can apply to the API URL.
	scopeEvery
	// scopeSNI checks a handshake iff its ServerName is eff(apiHost): a
	// proxy may apply, and the two hops have different effective SNI.
	scopeSNI
	// scopePostCheck checks no handshake: a proxy may apply and the API
	// host's effective SNI is empty (an IP literal) or overridden by the TLS
	// configuration's ServerName, so the hops cannot be told apart; only the
	// response's ProtoMajor is checked (K16).
	scopePostCheck
)

// String returns the scope's name, as the spike's decision table spells it.
func (s alpnScope) String() string {
	return [...]string{"none", "every-handshake", "sni", "post-check-only"}[s]
}

// target is what a build knows about the API URL.
type target struct {
	scheme string
	host   string // Hostname(), without brackets
	addr   string // the transport's dial key: host:port, the default port filled in
}

// resolveTarget validates cfg's API URL and mode.
func resolveTarget(cfg Config) (target, error) {
	u := cfg.APIURL
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return target{}, errBadURL
	}
	if cfg.Mode != HTTP2Only && cfg.Mode != HTTPAuto {
		return target{}, fmt.Errorf("h2gate: unknown mode %v", cfg.Mode)
	}
	host := u.Hostname()
	if cfg.Mode == HTTP2Only && !isASCII(host) {
		// The API hop's SNI and the transport's dial key are built from the
		// IDNA form (transport.go:2217-2219, :3187-3202); this package does
		// not import x/net/idna, so the SNI rule and the thin check could
		// never match the host.
		return target{}, fmt.Errorf("%w: %q", ErrNonASCIIHost, host)
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	return target{scheme: u.Scheme, host: host, addr: net.JoinHostPort(host, port)}, nil
}

// isASCII reports whether s has no byte outside ASCII.
func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// protocols returns the Protocols of mode for scheme.
func protocols(mode Mode, scheme string) *http.Protocols {
	var p http.Protocols
	switch {
	case mode == HTTPAuto:
		p.SetHTTP1(true)
		p.SetHTTP2(true)
	case scheme == "http":
		p.SetUnencryptedHTTP2(true)
	default:
		p.SetHTTP2(true)
	}
	return &p
}

// strictHTTP2 returns the HTTP/2 settings of section 6.3.
func strictHTTP2() *http.HTTP2Config {
	return &http.HTTP2Config{
		StrictMaxConcurrentRequests: true,
		SendPingTimeout:             sendPingTimeout,
		PingTimeout:                 pingTimeout,
	}
}

// isProxyFromEnvironment reports whether p is http.ProxyFromEnvironment, by
// function identity: func values are not comparable, and a closure that
// calls it, or http.ProxyURL, is another func.
func isProxyFromEnvironment(p func(*http.Request) (*url.URL, error)) bool {
	return p != nil && reflect.ValueOf(p).Pointer() == reflect.ValueOf(http.ProxyFromEnvironment).Pointer()
}

// hostnameInSNI is crypto/tls's hostnameInSNI
// (GOROOT/src/crypto/tls/handshake_client.go:1305-1318): empty for an IP
// literal, trailing dots stripped. ConnectionState.ServerName holds this form
// of the configured name.
func hostnameInSNI(name string) string {
	host := name
	if len(host) > 0 && host[0] == '[' && host[len(host)-1] == ']' {
		host = host[1 : len(host)-1]
	}
	if i := strings.LastIndex(host, "%"); i > 0 {
		host = host[:i]
	}
	if net.ParseIP(host) != nil {
		return ""
	}
	for len(name) > 0 && name[len(name)-1] == '.' {
		name = name[:len(name)-1]
	}
	return name
}

// eff is the SNI a handshake for host carries: the configured ServerName
// applies to both hops (transport.go:1783-1786).
func eff(serverName, host string) string { return hostnameInSNI(cmp.Or(serverName, host)) }

// proxyMayApply is the build-time proxy decision of section 6.3: with Proxy
// nil or http.ProxyFromEnvironment it is process-constant (the environment is
// read once, transport.go:1032-1042), so it is decided now for the API URL;
// any other func may return a proxy for any request.
func proxyMayApply(proxy func(*http.Request) (*url.URL, error), api *url.URL) (bool, error) {
	switch {
	case proxy == nil:
		return false, nil
	case isProxyFromEnvironment(proxy):
		u, err := proxy(&http.Request{URL: api, Header: http.Header{}})
		if err != nil {
			return false, fmt.Errorf("%w: %w", ErrProxyEnvironment, err)
		}
		return u != nil, nil
	default:
		return true, nil
	}
}

// scopeFor decides the ALPN check's scope at build.
func scopeFor(mode Mode, tg target, mayProxy bool, serverName string) alpnScope {
	switch {
	case mode != HTTP2Only || tg.scheme != "https":
		return scopeNone
	case !mayProxy:
		return scopeEvery
	case eff(serverName, tg.host) == "" || serverName != "":
		return scopePostCheck
	default:
		return scopeSNI
	}
}

// alpnCheck returns the VerifyConnection hook: the caller's hook first, then
// the refusal of an API-hop handshake that did not negotiate h2.
// VerifyConnection runs after the protocol is negotiated and before any
// application byte, in TLS 1.2 and 1.3 (handshake_client.go:588-589,
// :1198-1199; handshake_client_tls13.go:599-600); ConnectionState's
// HandshakeComplete is always false there (R20), so it is not read.
func alpnCheck(scope alpnScope, want string, next func(tls.ConnectionState) error) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if next != nil {
			if err := next(cs); err != nil {
				return err
			}
		}
		// An accepted ECH handshake reports the configured ServerName as it
		// is, not its hostnameInSNI form (handshake_client_tls13.go:102,277),
		// so the name is normalised before the comparison.
		apiHop := scope == scopeEvery || (scope == scopeSNI && hostnameInSNI(cs.ServerName) == want)
		if apiHop && cs.NegotiatedProtocol != "h2" {
			return fmt.Errorf("%w: the API host's TLS handshake negotiated %q", ErrNotNegotiated, cs.NegotiatedProtocol)
		}
		return nil
	}
}

// installALPNCheck sets the VerifyConnection hook on tr's TLS configuration
// (which must be tr's own) when the scope checks any handshake.
func installALPNCheck(tr *http.Transport, scope alpnScope, tg target) {
	if scope != scopeEvery && scope != scopeSNI {
		return
	}
	c := tr.TLSClientConfig
	c.VerifyConnection = alpnCheck(scope, eff(c.ServerName, tg.host), c.VerifyConnection)
}

// waitBound is the gate's bound on a waiter: the dial and the handshake,
// plus the CONNECT exchange and the proxy's own handshake when a proxy may
// apply. An upper bound only.
func waitBound(connect, handshake time.Duration, mayProxy bool) time.Duration {
	d := connect + handshake
	if mayProxy {
		d += proxyConnectLimit + handshake
	}
	return d
}

// dialGrace is how long, past the connect timeout, boundedDial waits for a
// dial whose context the timeout has ended before it abandons the dial. A
// dialer that honours its context, as net.Dialer does, answers within it
// with its own error, such as the *net.OpError of a host that does not
// answer, which the transport returns as it is and neither counts nor logs;
// only a dial still running then, which a caller's DNS or connect hook
// holds, is abandoned (review W6.6 MINOR 1, ruling D-W6.6-dialbound-grace).
// Without it the bound raced the dialer's own timeout, which fires at the
// same instant, and won most of the time. 100 ms is far above the time a
// dialer takes to see its deadline and small next to the connect timeout,
// 10 s by default.
const dialGrace = 100 * time.Millisecond

// boundedDial bounds dial by timeout, as net.Dialer.Timeout does, and runs
// it in its own goroutine, so that a dial that has not returned when the
// bound ends still ends for the transport: a caller's DNS or connect trace
// hook runs inside dial and can block it past its context (risk K28d).
// When timeout ends dial's context, the transport waits dialGrace more for
// dial's own answer, which a dialer that honours its context gives. A dial
// still running then fails with a timeout, which frees the connection
// permit (MaxConnsPerHost 1) for the next dial, and the transport abandons
// the goroutine rather than stopping it: dial's context has ended, so the
// dialer returns as soon as the hook does, and the goroutine closes a
// connection the dialer returns after it was abandoned. A dial that
// net/http cancels ends at once, with the context's error.
func (t *Transport) boundedDial(dial func(context.Context, string, string) (net.Conn, error), timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	type dialed struct {
		conn net.Conn
		err  error
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		// got is unbuffered: the goroutine hands a connection over only while
		// the transport still waits for it, and closes it otherwise.
		got := make(chan dialed)
		abandoned := make(chan struct{})
		go func() {
			conn, err := dial(ctx, network, addr)
			select {
			case got <- dialed{conn, err}:
			case <-abandoned:
				if conn != nil {
					_ = conn.Close()
				}
			}
		}()
		select {
		case d := <-got:
			return d.conn, d.err
		case <-ctx.Done():
		}
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			close(abandoned)
			return nil, ctx.Err()
		}
		grace := time.NewTimer(dialGrace)
		defer grace.Stop()
		select {
		case d := <-got:
			return d.conn, d.err
		case <-grace.C:
		}
		close(abandoned)
		bound := timeout + dialGrace
		t.dialExpiries.Add(1)
		t.log.DebugContext(ctx, "h2: dial bound expired", "bound", bound)
		return nil, &boundError{what: "dial " + network + " " + addr, bound: bound}
	}
}

// NewTransport builds the SDK's default transport for cfg (section 6.3): the
// stock *http.Transport with the mode's Protocols, MaxConnsPerHost 1 under
// HTTP2Only (0 under HTTPAuto), strict HTTP/2 stream accounting with a 30 s
// ping and a 15 s ping timeout, a 90 s idle timeout, ConnectTimeout as the
// dial timeout and TLSHandshakeTimeout, cfg's proxy, and a TLS configuration
// with TLS 1.2 at least and the ALPN check, wrapped in the gate and the token.
// Every error it returns is a configuration error.
func NewTransport(cfg Config) (*Transport, error) {
	tg, err := resolveTarget(cfg)
	if err != nil {
		return nil, err
	}
	connect := connectTimeout(cfg)
	mayProxy, err := proxyMayApply(cfg.Proxy, cfg.APIURL)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{}
	if cfg.TLSConfig != nil {
		tlsConfig = cfg.TLSConfig.Clone()
	}
	tlsConfig.MinVersion = max(tlsConfig.MinVersion, tls.VersionTLS12)
	if cfg.RootCAs != nil {
		tlsConfig.RootCAs = cfg.RootCAs
	}
	dial := cfg.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	tr := &http.Transport{
		Protocols:           protocols(cfg.Mode, tg.scheme),
		MaxConnsPerHost:     1,
		HTTP2:               strictHTTP2(),
		IdleConnTimeout:     idleConnTimeout,
		TLSHandshakeTimeout: connect,
		Proxy:               observeProxy(cfg.Proxy, cfg.OnProxy),
		TLSClientConfig:     tlsConfig,
		// A proxy's refusal of the CONNECT is a proxy failure (K16).
		OnProxyConnectResponse: refusedConnect,
	}
	if cfg.Mode == HTTPAuto {
		tr.MaxConnsPerHost = 0
	}
	scope := scopeFor(cfg.Mode, tg, mayProxy, tlsConfig.ServerName)
	installALPNCheck(tr, scope, tg)
	t := newTransport(tr, settings{
		mode:      cfg.Mode,
		scope:     scope,
		waitBound: waitBound(connect, connect, mayProxy),
		holdBound: connect + connect,
		log:       cfg.Logger,
		errorText: cfg.ErrorText,
	})
	// Set once t exists, which counts and logs the dials the bound ends;
	// the stock transport reads it only when it first dials.
	tr.DialContext = t.boundedDial(dial, connect)
	return t, nil
}

// observeProxy returns proxy wrapped so that onProxy, when set, sees every
// URL it returns without an error (Config.OnProxy), and the request's call
// is marked as one the func chose a proxy for ([Proxied]); nil stays nil.
func observeProxy(proxy func(*http.Request) (*url.URL, error), onProxy func(*url.URL)) func(*http.Request) (*url.URL, error) {
	if proxy == nil {
		return nil
	}
	return func(req *http.Request) (*url.URL, error) {
		u, err := proxy(req)
		if err == nil && u != nil {
			if c, ok := req.Context().Value(callKey{}).(*call); ok {
				c.seen.Or(callProxied)
			}
			if onProxy != nil {
				onProxy(u)
			}
		}
		return u, err
	}
}

// Wrap builds the SDK's transport over a clone of base (the WithHTTPTransport
// option). The clone keeps base's dialer, TLS configuration and proxy; Wrap
// sets Protocols, MaxConnsPerHost (HTTP2Only only), the HTTP2 ping timeouts
// and TLSHandshakeTimeout (to ConnectTimeout) where base leaves them zero,
// forces HTTP2.StrictMaxConcurrentRequests under HTTP2Only (under HTTPAuto
// it is set only when base's HTTP2Config is empty) and keeps base's other
// HTTP2 fields, and, under HTTP2Only on https:
//
//   - refuses a transport whose TLSNextProto carries its own "h2" entry right
//     after Clone (an x/net ConfigureTransports install): the stock h2
//     selection would then require a *tls.Conn and skip the checks below.
//     The stub entry the stock transport writes on first use is not copied
//     by Clone (transport.go:380-386, :440), so a used transport is accepted;
//   - adds the ALPN check to the clone's VerifyConnection, after base's own;
//   - wraps base's DialTLSContext (or DialTLS) thinly: for the API address
//     it bounds the dial and the handshake by ConnectTimeout +
//     TLSHandshakeTimeout, requires ConnectionState(), completes an
//     unfinished handshake with HandshakeContext, and closes a connection
//     that did not negotiate h2 with ErrNotNegotiated. A connection that
//     passes is served as HTTP/2 by the stock selection, whatever its type
//     (transport.go:2058-2074). Other addresses (an https proxy) pass
//     through unchecked.
//
// Clone runs base's own first-use setup (transport.go:345), which may give
// base an empty TLSClientConfig and HTTP2Config and fill its NextProtos, as
// base's first request would. cfg's RootCAs, TLSConfig, Proxy, OnProxy
// and DialContext must be zero. Every error Wrap returns is a configuration
// error.
func Wrap(base *http.Transport, cfg Config) (*Transport, error) {
	if base == nil {
		return nil, errors.New("h2gate: Wrap needs a transport")
	}
	if cfg.RootCAs != nil || cfg.TLSConfig != nil || cfg.Proxy != nil || cfg.OnProxy != nil || cfg.DialContext != nil {
		return nil, errWrapConfig
	}
	tg, err := resolveTarget(cfg)
	if err != nil {
		return nil, err
	}
	connect := connectTimeout(cfg)
	tr := base.Clone()
	if _, ok := tr.TLSNextProto["h2"]; ok && cfg.Mode == HTTP2Only && tg.scheme == "https" {
		return nil, ErrCallerHTTP2
	}
	if tr.Protocols == nil {
		tr.Protocols = protocols(cfg.Mode, tg.scheme)
	}
	if tr.MaxConnsPerHost == 0 && cfg.Mode == HTTP2Only {
		tr.MaxConnsPerHost = 1
	}
	// Clone gave the clone its own copy of base's HTTP2Config
	// (transport.go:372-375), so these writes do not reach base.
	if tr.HTTP2 == nil {
		tr.HTTP2 = &http.HTTP2Config{}
	}
	h := tr.HTTP2
	// Strict stream accounting keeps one connection per host (non-strict,
	// a full connection leaves the per-host count and the transport dials
	// more: 21 connections in F1-c) and is what the token serialises, so
	// HTTP2Only forces it whatever base set (R71). Under HTTPAuto it is set
	// only on an empty config: the stock first-use setup, which Clone runs
	// on base, stores an empty one there (http2.go:288-290). HTTP2Config
	// holds a func field and is not comparable.
	if cfg.Mode == HTTP2Only || reflect.ValueOf(*h).IsZero() {
		h.StrictMaxConcurrentRequests = true
	}
	h.SendPingTimeout = cmp.Or(h.SendPingTimeout, sendPingTimeout)
	h.PingTimeout = cmp.Or(h.PingTimeout, pingTimeout)
	if tr.TLSHandshakeTimeout == 0 {
		tr.TLSHandshakeTimeout = connect
	}
	mayProxy, err := proxyMayApply(tr.Proxy, cfg.APIURL)
	if err != nil {
		return nil, err
	}
	var serverName string
	if tr.TLSClientConfig != nil {
		serverName = tr.TLSClientConfig.ServerName
	}
	scope := scopeFor(cfg.Mode, tg, mayProxy, serverName)
	if scope == scopeEvery || scope == scopeSNI {
		if tr.TLSClientConfig == nil {
			tr.TLSClientConfig = &tls.Config{}
		}
		installALPNCheck(tr, scope, tg)
	}
	if cfg.Mode == HTTP2Only && tg.scheme == "https" {
		dialTLS := tr.DialTLSContext
		// A caller may still set the deprecated DialTLS, which the stock
		// transport treats as a TLS dialer (hasCustomTLSDialer,
		// transport.go:431-433) and calls when DialTLSContext is nil
		// (customDialTLS, :1567-1572), so the check covers it too.
		//lint:ignore SA1019 the check must cover the deprecated field
		if legacy := tr.DialTLS; dialTLS == nil && legacy != nil { //nolint:staticcheck // SA1019: see above
			dialTLS = func(_ context.Context, network, addr string) (net.Conn, error) { return legacy(network, addr) }
		}
		if dialTLS != nil {
			tr.DialTLSContext = thinDialTLS(dialTLS, tg.addr, connect+tr.TLSHandshakeTimeout)
			//lint:ignore SA1019 DialTLSContext now carries the caller's dialer
			tr.DialTLS = nil //nolint:staticcheck // SA1019: DialTLSContext now carries the caller's dialer
		}
	}
	return newTransport(tr, settings{
		mode:      cfg.Mode,
		scope:     scope,
		waitBound: waitBound(connect, tr.TLSHandshakeTimeout, mayProxy),
		holdBound: connect + tr.TLSHandshakeTimeout,
		log:       cfg.Logger,
		errorText: cfg.ErrorText,
	}), nil
}

// connectionStater is the interface the stock transport reads a caller TLS
// dialer's connection state through (transport.go:1892-1894).
type connectionStater interface {
	ConnectionState() tls.ConnectionState
}

// handshaker is the interface the stock transport completes a caller TLS
// dialer's handshake through (transport.go:1895-1897).
type handshaker interface {
	HandshakeContext(ctx context.Context) error
}

// thinDialTLS wraps a caller's TLS dialer with the h2 check for the API
// address apiAddr. The customDialTLS path never arms TLSHandshakeTimeout
// (transport.go:1886-1918 against :1794), so bound is what keeps a TLS-silent
// peer from holding the single connection permit (K19). A caller dialer that
// ignores its context is not bounded by it.
func thinDialTLS(dial func(context.Context, string, string) (net.Conn, error), apiAddr string, bound time.Duration) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != apiAddr {
			return dial(ctx, network, addr)
		}
		ctx, cancel := context.WithTimeout(ctx, bound)
		defer cancel()
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		cs, ok := conn.(connectionStater)
		if !ok {
			_ = conn.Close()
			return nil, fmt.Errorf("%w: the TLS dialer's %T reports no TLS connection state", ErrNotNegotiated, conn)
		}
		if h, ok := conn.(handshaker); ok && !cs.ConnectionState().HandshakeComplete {
			if err := h.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, err
			}
		}
		if p := cs.ConnectionState().NegotiatedProtocol; p != "h2" {
			_ = conn.Close()
			return nil, fmt.Errorf("%w: the TLS dialer's connection negotiated %q", ErrNotNegotiated, p)
		}
		return conn, nil
	}
}
