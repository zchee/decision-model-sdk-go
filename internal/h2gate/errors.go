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
	"errors"
	"net"
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
	// (GOROOT/src/net/http/transport.go:1871-1874).
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
