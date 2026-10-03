// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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

// Package rest is the HTTP call the providers share: a JSON POST through an
// *http.Client the provider owns or borrows, bounded by the provider's
// timeout and the call's context, whose response body is read up to a fixed
// size and whose failures are returned as the error types of package llm.
//
// It does at the boundary what map_provider_error of
// system-one-adapter-python v0.2.1 does for the vendors' Python SDKs
// (src/system_one_adapter/_utils/error_handling.py:43-72): a response with
// a status outside 200 to 299 is an *llm.StatusError that keeps the status
// and the body, a timeout an *llm.TimeoutError, and a request that failed
// without a response an *llm.ConnectionError.
//
// No error of this package prints a credential. A URL appears in an error
// text only as its scheme, host and path, without userinfo, query or
// fragment; a request header's value appears in none; and the error the
// HTTP client returned, which prints the whole URL, is never kept in a
// chain: a timeout or connection error wraps a fixed text and at most one
// well-known error value such as context.DeadlineExceeded or a
// syscall.Errno. A provider that puts a credential into the path of its URL
// is not covered.
package rest

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// DefaultTimeout bounds a request of a Client whose Config sets no Timeout:
// 600 s, the read timeout of the vendors' Python SDKs.
const DefaultTimeout = 600 * time.Second

// maxBodyBytes is how many bytes of a response body a Client reads: 64 MiB.
const maxBodyBytes = 64 << 20

// keptHeaders are the response headers an *llm.StatusError keeps, in the
// canonical form of http.CanonicalHeaderKey: the two a retry policy reads
// the provider's wait from, and the request id a status error's text ends
// with.
var keptHeaders = [...]string{"Retry-After", "Retry-After-Ms", "X-Typesafe-Request-Id"}

// Config configures a Client.
type Config struct {
	// HTTPClient performs the requests when it is not nil. The Client
	// borrows it: it never writes to it, and Close leaves it alone. Its
	// settings are read once, when New is called: the Client calls through
	// a copy of the client value, which shares the Transport and the Jar,
	// so a field of the caller's client that is changed afterwards is not
	// seen.
	//
	// A borrowed client whose CheckRedirect is nil follows no redirect
	// either: the copy has the policy of an owned client. A borrowed client
	// whose CheckRedirect is set keeps that policy on the copy, and a
	// caller who sets one decides where the header that carries the
	// provider's key may go: net/http sends a request's headers on to the
	// host a redirect names.
	//
	// When HTTPClient is nil the Client owns a client of its own over a
	// clone of http.DefaultTransport, which follows no redirect.
	HTTPClient *http.Client
	// Timeout bounds each request, from its start to the end of the
	// response body. Zero or less means DefaultTimeout.
	Timeout time.Duration
	// BlankErrorBodyIsNone decides what an *llm.StatusError holds for a
	// response body that is empty or only white space: a nil Body when
	// true, and a Body that is empty and not nil when false. The vendors'
	// Python SDKs differ here, and the Adapter's text for a status error
	// differs with them: OpenAI's and Anthropic's hand over the empty
	// string, Gemini's hands over no body.
	BlankErrorBodyIsNone bool
}

// Client performs the JSON POST of one provider. It is safe for concurrent
// use.
type Client struct {
	http        *http.Client
	owned       bool
	timeout     time.Duration
	blankIsNone bool
	maxBody     int64
}

// New returns a Client configured by cfg.
func New(cfg Config) *Client {
	c := &Client{
		http:        cfg.HTTPClient,
		timeout:     cfg.Timeout,
		blankIsNone: cfg.BlankErrorBodyIsNone,
		maxBody:     maxBodyBytes,
	}
	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}
	if c.http == nil {
		c.http, c.owned = &http.Client{Transport: ownedTransport(), CheckRedirect: noRedirect}, true
		return c
	}
	// The caller's client is not written: the Client keeps a copy of it,
	// and the policy of an owned client goes into the copy when the caller
	// set none.
	borrowed := *c.http
	if borrowed.CheckRedirect == nil {
		borrowed.CheckRedirect = noRedirect
	}
	c.http = &borrowed
	return c
}

// ownedTransport returns the transport of an owned client: a clone of
// http.DefaultTransport, so that it has the default's proxy and HTTP/2
// settings and a connection pool of its own, or a transport with those
// settings when a program has replaced the default by another kind.
func ownedTransport() *http.Transport {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true}
}

// noRedirect is the redirect policy of an owned client and of the copy of a
// borrowed one that has none of its own: the response of status 300 to 399 is
// returned as it is, so Post reports it as an *llm.StatusError. A redirect
// is not followed because net/http would send the request's headers to the
// new host again. When that host is another one it removes only the
// credential headers it knows (Authorization, Www-Authenticate, Cookie,
// Cookie2, Proxy-Authorization and Proxy-Authenticate in go1.27.1,
// net/http/client.go:826-827), not the x-api-key or x-goog-api-key header
// a provider's credential travels in, and a 307 or 308 carries the request
// body along.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// Post sends body to u as an HTTP POST with the given header and returns
// the response body of a response whose status is 200 to 299.
//
// The request carries a copy of header, with Content-Type: application/json
// when header names no content type; header itself is not changed. u is
// sent as it is, userinfo and query included.
//
// It returns:
//
//   - ctx.Err() itself when ctx was cancelled, whatever else failed;
//   - an *llm.TimeoutError when the deadline of ctx or the Client's timeout
//     passed, or the HTTP client's error says it timed out, before the
//     response body was read to its end;
//   - an *llm.ConnectionError when the request or the reading of the
//     response body failed in any other way;
//   - an *llm.StatusError for a status outside 200 to 299, a redirect that
//     was not followed included, with the status,
//     the response headers Retry-After, Retry-After-Ms and
//     X-Typesafe-Request-Id when present and no other header, and the body
//     as received: its bytes unchanged, except that a body that is empty or
//     only white space is nil or empty as Config.BlankErrorBodyIsNone says,
//     and that a body longer than 64 MiB is cut there;
//   - ErrBodyTooLarge for a status of 200 to 299 whose body is longer than
//     64 MiB;
//   - an error of a fixed text when no request could be built from u.
//
// None of them holds the HTTP client's own error, u's userinfo or query, or
// a header value.
func (c *Client) Post(ctx context.Context, u *url.URL, header http.Header, body []byte) ([]byte, error) {
	target := redact(u)
	rctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(rctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		// err prints u whole; only the fixed text leaves.
		return nil, &transportError{text: errorText(target, phraseBuild)}
	}
	req.Header = header.Clone()
	if req.Header == nil {
		req.Header = make(http.Header, 1)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, classify(ctx, rctx, target, phraseTimeout, phraseConnection, err)
	}
	defer resp.Body.Close()

	// One byte past the limit tells a body of the limit from a longer one.
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return nil, classify(ctx, rctx, target, phraseBodyTimeout, phraseBody, err)
	}
	tooLarge := int64(len(data)) > c.maxBody
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if tooLarge {
			data = data[:c.maxBody]
		}
		return nil, &llm.StatusError{StatusCode: resp.StatusCode, Header: keepHeaders(resp.Header), Body: c.errorBody(data)}
	}
	if tooLarge {
		return nil, ErrBodyTooLarge
	}
	return data, nil
}

// Close closes the idle connections of a client the Client owns and does
// nothing to a borrowed one. It returns nil.
func (c *Client) Close() error {
	if c.owned {
		c.http.CloseIdleConnections()
	}
	return nil
}

// errorBody returns the Body of an *llm.StatusError for the response body
// data: data itself, or for a blank one nil or an empty slice that is not
// nil, as the Client's Config says.
func (c *Client) errorBody(data []byte) []byte {
	if !isBlank(data) {
		return data
	}
	if c.blankIsNone {
		return nil
	}
	return []byte{}
}

// keepHeaders returns a new header holding the keptHeaders that h has, each
// with a copy of its values.
func keepHeaders(h http.Header) http.Header {
	kept := make(http.Header, len(keptHeaders))
	for _, name := range keptHeaders {
		if values := h.Values(name); len(values) > 0 {
			kept[name] = slices.Clone(values)
		}
	}
	return kept
}

// isBlank reports whether b, read as UTF-8, is empty or holds only white
// space that Python's str.strip removes, which is how the vendors' Python
// SDKs decide that an error response has no text (response.text.strip()).
// A byte that is not UTF-8 is not white space.
func isBlank(b []byte) bool {
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if !isPySpace(r) {
			return false
		}
		b = b[n:]
	}
	return true
}

// isPySpace reports whether r is white space for CPython 3.14's
// str.isspace: the 29 characters U+0009 to U+000D, U+001C to U+0020,
// U+0085, U+00A0, U+1680, U+2000 to U+200A, U+2028, U+2029, U+202F, U+205F
// and U+3000. unicode.IsSpace lacks U+001C to U+001F.
func isPySpace(r rune) bool {
	switch {
	case r >= 0x09 && r <= 0x0d, r >= 0x1c && r <= 0x20, r == 0x85, r == 0xa0, r == 0x1680,
		r >= 0x2000 && r <= 0x200a, r == 0x2028, r == 0x2029, r == 0x202f, r == 0x205f, r == 0x3000:
		return true
	}
	return false
}

// ParseURL parses raw as the base URL of a provider: an absolute http or
// https URL with a host. It returns ErrURL for anything else; the error
// holds no part of raw, which may carry a credential in its userinfo or
// its query.
func ParseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, ErrURL
	}
	return u, nil
}

// redact returns u as an error text may show it: scheme, host and path,
// without userinfo, query or fragment.
func redact(u *url.URL) string {
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// Env returns the value of the environment variable name and whether it is
// set, as os.LookupEnv does. It is the one read of the environment a
// provider makes, when it is built; it prints and logs nothing.
//
// An empty name is reported as not set without asking the operating system.
// A variable that is set to the empty string is reported as set, with the
// empty value. Whether such a variable counts is the provider's rule,
// because the vendors' Python SDKs differ: Anthropic's and Gemini's take an
// empty value as none; OpenAI's takes an empty OPENAI_API_KEY as none and
// an empty OPENAI_BASE_URL, OPENAI_ORG_ID or OPENAI_PROJECT_ID as the value
// (openai 3.17.0, the version system-one-adapter-python v0.2.1 locks).
func Env(name string) (value string, set bool) {
	if name == "" {
		return "", false
	}
	return os.LookupEnv(name)
}
