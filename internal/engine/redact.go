// Copyright 2026 The decision-model-sdk-go Authors.
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

package engine

import (
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/zchee/decision-model-sdk-go/internal/wire"
)

// Redacted is what a credential is printed as.
const Redacted = "***"

// secretHeaderNames are the header names, in lower case, whose values are
// credentials (py:_core/constants.py:21).
var secretHeaderNames = []string{"authorization", "proxy-authorization", "x-api-key", "api-key", "cookie", "set-cookie"}

// IsSecretHeader reports whether the value of the header name is a
// credential by its name, compared without regard to case: one of
// secretHeaderNames, or a name that contains "token" or "secret"
// (py:_core/logging.py:32-34). The rule is strings.ToLower's: an ASCII
// name, as every name on the wire is (net/http refuses others), is
// compared with its letters folded in place, which allocates nothing,
// where lower-casing a mixed-case name such as "Content-Type" copies it;
// any other name is lower-cased with strings.ToLower.
func IsSecretHeader(name string) bool {
	for i := range len(name) {
		if name[i] >= utf8.RuneSelf {
			lower := strings.ToLower(name)
			return slices.Contains(secretHeaderNames, lower) || strings.Contains(lower, "token") || strings.Contains(lower, "secret")
		}
	}
	for _, secret := range secretHeaderNames {
		if strings.EqualFold(name, secret) {
			return true
		}
	}
	return containsFoldASCII(name, "token") || containsFoldASCII(name, "secret")
}

// containsFoldASCII reports whether the ASCII string s contains sub, which is
// lower case, with the letters of s compared in lower case.
func containsFoldASCII(s, sub string) bool {
	for i := range len(s) - len(sub) + 1 {
		j := 0
		for j < len(sub) && lowerASCII(s[i+j]) == sub[j] {
			j++
		}
		if j == len(sub) {
			return true
		}
	}
	return false
}

// lowerASCII returns c in lower case when it is an ASCII capital letter.
func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// MinKeyNeedleBytes is the shortest API key the SDK looks for inside other
// text: a header value it redacts, and a WithHeader name it refuses. A
// shorter key, such as a test's "test" or "k", occurs in ordinary
// names and values, and looking for it would hide or refuse them; real keys
// are far longer. Redaction by header name does not depend on the key and
// always applies.
const MinKeyNeedleBytes = 8

// KeyNeedle reports whether key is long enough to be looked for inside other
// text ([MinKeyNeedleBytes]).
func KeyNeedle(key string) bool {
	return len(key) >= MinKeyNeedleBytes
}

// isCredential reports whether the values of the header name must not be
// printed: the name marks them as credentials ([IsSecretHeader]), or one of
// them holds the API key, under whatever name the caller sent it, when the
// key is at least [MinKeyNeedleBytes] long. The second test goes past
// typesafe-sdk-python, which redacts by name alone.
func isCredential(name string, values []string, apiKey string) bool {
	if IsSecretHeader(name) {
		return true
	}
	return KeyNeedle(apiKey) && slices.ContainsFunc(values, func(v string) bool { return strings.Contains(v, apiKey) })
}

// HeaderRedactor redacts the response header that the error types keep
// and the log records print: a header is a credential by its name always, by
// holding the client's API key when the key is at least [MinKeyNeedleBytes]
// long ([isCredential]), and, in the response to a plain-HTTP request through
// a proxy, by holding a whole credential of the proxies the SDK's transport
// chose, from [MinKeyNeedleBytes] too ([ProxyCreds.inHeader]), which such a
// proxy may repeat in the header of its own answer. Its zero value redacts by
// name alone, for an error built without a client's key, such as
// UnmarshalJSON's. Build a client's with [NewHeaderRedactor], and a
// response's with [HeaderRedactor.WithProxies].
type HeaderRedactor struct {
	// key is the API key when it is long enough to look for, else empty.
	key string
	// proxies are the client's transport's proxy credentials for a response
	// through a proxy to a plain-HTTP request, nil otherwise.
	proxies *ProxyCreds
}

// NewHeaderRedactor returns the redactor for a client whose API key is
// apiKey: by name and by apiKey when apiKey is at least
// [MinKeyNeedleBytes] long, by name alone otherwise.
func NewHeaderRedactor(apiKey string) HeaderRedactor {
	if !KeyNeedle(apiKey) {
		return HeaderRedactor{}
	}
	return HeaderRedactor{key: apiKey}
}

// WithProxies returns r looking also for the whole credentials of proxies,
// for the header of a response that a proxy may have written itself:
// [Transport.ResponseRedactor] gives it to a response to a plain-HTTP
// request through a proxy alone.
func (r HeaderRedactor) WithProxies(proxies *ProxyCreds) HeaderRedactor {
	r.proxies = proxies
	return r
}

// ResponseFunc returns the func a [Response] keeps to redact its header
// when the call has returned ([Response.SetRedactor]), for r, the redactor
// its call used ([Transport.ResponseRedactor]). When r looks for no
// proxy's credentials, which is every response but one to a plain-HTTP
// request through a proxy, it returns keyOnly, the client's own method value
// ([Config.RedactorFunc]), which costs nothing. Otherwise it returns a func
// that gives r with its proxies' credentials as they are now
// ([ProxyCreds.Snapshot]): the client's set holds the most recent
// [MaxProxyUserinfos] and forgets the oldest, so a response kept while the
// client goes through that many other proxies would otherwise lose its own
// proxy's credential. That path costs two allocations, the snapshot and the
// func.
func (r HeaderRedactor) ResponseFunc(keyOnly func() HeaderRedactor) func() HeaderRedactor {
	if r.proxies == nil {
		return keyOnly
	}
	r.proxies = r.proxies.Snapshot()
	return func() HeaderRedactor { return r }
}

// credential reports whether the values of the header name must not be
// printed: [isCredential]'s test for r's key, or one of them holds a
// credential of r's proxies ([ProxyCreds.inHeader]).
func (r HeaderRedactor) credential(name string, values []string) bool {
	return isCredential(name, values, r.key) || r.proxies.inHeader(values)
}

// Header returns h's headers in a new map in which every value of each
// header that is a credential is replaced by "***", one "***" per value, and
// every other header shares its value slice with h; nil stays nil. The error
// types that keep a response's header store this map, so no rendering of
// them, and no caller that dumps their Header, shows a credential the server
// sent.
func (r HeaderRedactor) Header(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	out := make(http.Header, len(h))
	for name, values := range h {
		if !r.credential(name, values) {
			out[name] = values
			continue
		}
		masked := make([]string, len(values))
		for i := range masked {
			masked[i] = Redacted
		}
		out[name] = masked
	}
	return out
}

// RequestID returns the request id in h as the error types show it from the
// header r redacted ([HeaderRedactor.Header]): the x-typesafe-request-id
// values, or the x-request-id ones when those are absent
// ([wire.RequestIDValues]), joined by ", ", each "***" when one of them
// holds the client's API key or a credential of r's proxies, and whether the
// header was present.
// The INFO "response" record reads the id through it, so the record shows
// "***" where Error does. The header's name is not a credential's
// ([IsSecretHeader]), so only the key and the proxies' credentials are
// looked for, and the name is not tested. h is not copied: one value costs
// no allocation, and several cost the one string they are joined into.
func (r HeaderRedactor) RequestID(h http.Header) (string, bool) {
	values := wire.RequestIDValues(h)
	if len(values) == 0 {
		return "", false
	}
	if r.key != "" && slices.ContainsFunc(values, func(v string) bool { return strings.Contains(v, r.key) }) || r.proxies.inHeader(values) {
		return strings.Repeat(", "+Redacted, len(values))[len(", "):], true
	}
	return strings.Join(values, ", "), true
}

// RedactedHeaders is a header map as a log record shows it: a group with one
// attribute per header, in name order, whose value is the header's values
// joined by ", ", or "***" when they are a credential
// ([HeaderRedactor.credential]). Build it with [NewRedactedHeaders].
//
// It is a [slog.LogValuer], so the map is walked and its values joined only
// when a handler keeps the record. Boxing the value into an attribute still
// allocates, so a request path that must not allocate with debug records off
// checks [slog.Logger.Enabled] before it builds the attribute.
//
// No rendering prints the map or a credential. Every fmt verb prints the
// redacted form ([RedactedHeaders.Format]), and so do an unresolved
// [slog.Value] and [slog.Attr], whose String methods print a LogValuer
// through fmt. The map and the redactor sit behind the one pointer field, so
// the two renderings fmt makes without calling Format, the %p verb and a
// RedactedHeaders held in an unexported field of another value, print an
// address.
type RedactedHeaders struct {
	p *headerLog
}

// headerLog is what a [RedactedHeaders] renders.
type headerLog struct {
	header   http.Header
	redactor HeaderRedactor
}

// NewRedactedHeaders returns header as a log record shows it, each header r
// counts as a credential redacted ([HeaderRedactor.credential]): by its
// name, by a value that holds the client's API key, and, in the response to
// a plain-HTTP request through a proxy, by a value that holds the proxy's
// credential.
func NewRedactedHeaders(header http.Header, r HeaderRedactor) RedactedHeaders {
	return RedactedHeaders{p: &headerLog{header: header, redactor: r}}
}

// LogValue returns the redacted headers as a group value; the zero
// RedactedHeaders is an empty group.
func (r RedactedHeaders) LogValue() slog.Value {
	if r.p == nil {
		return slog.GroupValue()
	}
	attrs := make([]slog.Attr, 0, len(r.p.header))
	for _, name := range slices.Sorted(maps.Keys(r.p.header)) {
		values := r.p.header[name]
		value := Redacted
		if !r.p.redactor.credential(name, values) {
			value = strings.Join(values, ", ")
		}
		attrs = append(attrs, slog.String(name, value))
	}
	return slog.GroupValue(attrs...)
}

// Format writes the redacted form, LogValue().String(), such as
// "[Accept=application/json Authorization=***]", whatever the verb and its
// flags. A String method alone would not do: %d and %x bypass it and print
// the fields.
func (r RedactedHeaders) Format(f fmt.State, _ rune) {
	fmt.Fprint(f, r.LogValue().String())
}
