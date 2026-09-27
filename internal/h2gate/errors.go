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
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNotNegotiated reports that the API hop did not speak HTTP/2 under
// [HTTP2Only]: its TLS handshake negotiated another protocol or none, the
// server refused h2 with TLS alert 120 (no_application_protocol), a caller's
// TLS dialer returned a connection without h2, or, where no handshake check
// could apply, the response was not HTTP/2. Every refusal but the last comes
// before any byte of the request is written.
var ErrNotNegotiated = errors.New("h2gate: HTTP/2 not negotiated")

// alertNoApplicationProtocol is the text of crypto/tls's alert 120. The alert
// type is unexported and does not convert to tls.AlertError
// (GOROOT/src/crypto/tls/alert.go), so the text is what identifies it.
const alertNoApplicationProtocol = "tls: no application protocol"

// DialError is a failure before the transport handed the request a
// connection: the dial, the proxy, the TLS handshake or the ALPN check. Each
// caller gets its own value; a waiter released by a failed leader shares the
// leader's Err (R19).
//
// The flags classify Err by walking its whole chain. Both can be set, as for
// a TLS handshake with a proxy that timed out; the root package maps that
// case to a timeout on the proxy hop (R67 Q3). A failure to negotiate
// HTTP/2 on the API hop is reported through errors.Is(err,
// [ErrNotNegotiated]), never together with Proxy: Proxy wins over
// not-negotiated (R20), so a proxy that refused h2 with alert 120 is a proxy
// failure.
type DialError struct {
	// Proxy is set when a *net.OpError with Op "proxyconnect" is in the
	// chain: the dial to the proxy or its TLS handshake failed
	// (GOROOT/src/net/http/transport.go:1871-1874), or, on a transport
	// NewTransport built, the proxy answered the CONNECT with a status other
	// than 200 (refusedConnect). A transport Wrap built reports that refusal
	// as the stock transport returns it, the status text alone, which is
	// not a proxy failure here.
	Proxy bool
	// Timeout is set when a net.Error in the chain reports Timeout(): the
	// dial timeout, the TLS handshake timeout or a context deadline.
	Timeout bool
	// Err is the transport's error, shared by a leader and its waiters.
	Err error

	// notNegotiated is set when Err carries ErrNotNegotiated or alert 120
	// and Proxy is not set.
	notNegotiated bool
}

// Error implements error.
func (e *DialError) Error() string {
	switch {
	case e.Proxy:
		return "h2gate: proxy connection failed: " + e.Err.Error()
	case e.notNegotiated:
		return "h2gate: HTTP/2 not negotiated: " + e.Err.Error()
	case e.Timeout:
		return "h2gate: dial timed out: " + e.Err.Error()
	default:
		return "h2gate: dial failed: " + e.Err.Error()
	}
}

// Unwrap returns the cause, preceded by [ErrNotNegotiated] when the failure
// is one, so errors.Is reaches both.
func (e *DialError) Unwrap() []error {
	if e.notNegotiated {
		return []error{ErrNotNegotiated, e.Err}
	}
	return []error{e.Err}
}

// clone returns a fresh value of the same class around the same cause.
func (e *DialError) clone() *DialError {
	c := *e
	return &c
}

// boundError is the cause of a dial, or of a request's wait for a
// connection, that one of the transport's bounds ended (risk K28d): a
// caller's trace hook that blocks a new connection's DNS, connect or TLS
// phase keeps either from ending on its own. It is a net.Error whose
// Timeout is true, so classify reports a timeout, and errors.Is matches it
// to context.DeadlineExceeded, as it matches a dial's own timeout.
type boundError struct {
	// what names what did not end: "dial tcp example.com:443" or "the wait
	// for a connection".
	what  string
	bound time.Duration
}

// Error implements error.
func (e *boundError) Error() string {
	return "h2gate: " + e.what + " did not end within its bound (" + e.bound.String() + ")"
}

// Timeout implements net.Error: the bound is a timeout.
func (*boundError) Timeout() bool { return true }

// Temporary implements net.Error.
func (*boundError) Temporary() bool { return true }

// Is reports whether target is context.DeadlineExceeded.
func (*boundError) Is(target error) bool {
	return target == context.DeadlineExceeded
}

// refusedConnect is the OnProxyConnectResponse of a transport NewTransport
// builds: a proxy's answer other than 200 to the CONNECT fails the dial as a
// proxyconnect *net.OpError around the whole status line, the form a failed
// dial to the proxy takes, so that classify reports a proxy failure. The
// stock transport returns the status text alone, unwrapped
// (GOROOT/src/net/http/transport.go:2036-2043), which cannot be told from a
// failure past the proxy (K16). Wrap leaves a caller's transport its own
// hook.
//
// The status line is the proxy's text, and a proxy may repeat in it the
// credential the CONNECT carried, which the request's own credentials do
// not name: the error holds it with that credential replaced by "***"
// (scrubProxyCredential; review W6.2 MIN-4), so neither the SDK's error nor
// a record that prints it shows the proxy's password.
func refusedConnect(_ context.Context, proxyURL *url.URL, _ *http.Request, resp *http.Response) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return &net.OpError{Op: "proxyconnect", Net: "tcp", Err: errors.New(scrubProxyCredential(resp.Status, proxyURL))}
}

// scrubProxyCredential returns s with the credential that a CONNECT to
// proxyURL carries replaced by "***": the token of the Basic
// Proxy-Authorization value net/http sends for a proxy URL with userinfo
// (base64 of "user:password", connectMethod.proxyAuth in
// GOROOT/src/net/http/transport.go), then the URL's password as it is and as
// the URL escapes it. The token goes first because it may hold the
// password's bytes, which replacing the password first would cut it at,
// leaving the rest of the token. A password of any length is replaced, even
// one that ordinary text may hold: the proxy's text is short, and a password
// it repeats would otherwise be shown whole.
func scrubProxyCredential(s string, proxyURL *url.URL) string {
	if proxyURL == nil || proxyURL.User == nil {
		return s
	}
	token, password, escaped := ProxyCredentialForms(proxyURL.User)
	s = strings.ReplaceAll(s, token, redacted)
	if password == "" {
		return s
	}
	s = strings.ReplaceAll(s, password, redacted)
	if escaped != password {
		s = strings.ReplaceAll(s, escaped, redacted)
	}
	return s
}

// ProxyCredentialForms returns the forms of the credential that a request
// through a proxy whose URL has the userinfo u carries: the token of the
// Basic Proxy-Authorization value net/http sends, the password as it is, and
// the password as the URL escapes it.
func ProxyCredentialForms(u *url.Userinfo) (token, password, escaped string) {
	password, _ = u.Password()
	token = base64.StdEncoding.EncodeToString([]byte(u.Username() + ":" + password))
	escaped = strings.TrimPrefix(url.UserPassword("", password).String(), ":")
	return token, password, escaped
}

// redacted stands for a credential in text, as the root package writes it.
const redacted = "***"

// classify builds the DialError for err. It walks the whole chain, because
// errors.As stops at the first match and a proxyconnect *net.OpError wraps
// the remote-error *net.OpError of an alert 120 from a strict proxy (R20).
func classify(err error) *DialError {
	d := &DialError{Err: err}
	notNegotiated := false
	walk(err, func(e error) {
		if oe, ok := e.(*net.OpError); ok { //nolint:errorlint // walk visits every node; each is tested as it is
			switch {
			case oe.Op == "proxyconnect":
				d.Proxy = true
			case oe.Op == "remote error" && oe.Err != nil && oe.Err.Error() == alertNoApplicationProtocol:
				notNegotiated = true
			}
		}
		if ne, ok := e.(net.Error); ok && ne.Timeout() { //nolint:errorlint // walk visits every node; each is tested as it is
			d.Timeout = true
		}
		if e == ErrNotNegotiated { //nolint:errorlint // walk visits every node; identity is the test
			notNegotiated = true
		}
	})
	d.notNegotiated = notNegotiated && !d.Proxy
	return d
}

// walk calls fn for err and every error it wraps, depth first, through both
// Unwrap() error and Unwrap() []error.
func walk(err error, fn func(error)) {
	if err == nil {
		return
	}
	fn(err)
	switch u := err.(type) { //nolint:errorlint // walking the tree by hand is the point
	case interface{ Unwrap() error }:
		walk(u.Unwrap(), fn)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			walk(e, fn)
		}
	}
}
