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
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
)

// redacted is what a credential is printed as.
const redacted = "***"

// secretHeaderNames are the header names, in lower case, whose values are
// credentials (py:_core/constants.py:21).
var secretHeaderNames = []string{"authorization", "proxy-authorization", "x-api-key", "api-key", "cookie", "set-cookie"}

// isSecretHeader reports whether the value of the header name is a
// credential by its name, compared without regard to case: one of
// secretHeaderNames, or a name that contains "token" or "secret"
// (py:_core/logging.py:32-34).
func isSecretHeader(name string) bool {
	lower := strings.ToLower(name)
	return slices.Contains(secretHeaderNames, lower) || strings.Contains(lower, "token") || strings.Contains(lower, "secret")
}

// minKeyNeedleBytes is the shortest API key the SDK looks for inside other
// text: a header value it redacts, and a WithHeader name it refuses (ruling
// R68). A shorter key, such as a test's "test" or "k", occurs in ordinary
// names and values, and looking for it would hide or refuse them; real keys
// are far longer. Redaction by header name does not depend on the key and
// always applies.
const minKeyNeedleBytes = 8

// keyNeedle reports whether key is long enough to be looked for inside other
// text ([minKeyNeedleBytes]).
func keyNeedle(key string) bool {
	return len(key) >= minKeyNeedleBytes
}

// isCredential reports whether the values of the header name must not be
// printed: the name marks them as credentials ([isSecretHeader]), or one of
// them holds the API key, under whatever name the caller sent it, when the
// key is at least [minKeyNeedleBytes] long. The second test goes past
// typesafe-sdk-python, which redacts by name alone.
func isCredential(name string, values []string, apiKey string) bool {
	if isSecretHeader(name) {
		return true
	}
	return keyNeedle(apiKey) && slices.ContainsFunc(values, func(v string) bool { return strings.Contains(v, apiKey) })
}

// headerRedactor redacts the response header that the error types keep
// (rulings R87, R93): a header is a credential by its name always, and by
// holding the client's API key when the key is at least
// [minKeyNeedleBytes] long ([isCredential]). Its zero value redacts by name
// alone, for an error built without a client's key, such as UnmarshalJSON's.
// Build a client's with [newHeaderRedactor] or [config.redactor].
type headerRedactor struct {
	// key is the API key when it is long enough to look for, else empty.
	key string
}

// newHeaderRedactor returns the redactor for a client whose API key is
// apiKey: by name and by apiKey when apiKey is at least
// [minKeyNeedleBytes] long (ruling R68), by name alone otherwise.
func newHeaderRedactor(apiKey string) headerRedactor {
	if !keyNeedle(apiKey) {
		return headerRedactor{}
	}
	return headerRedactor{key: apiKey}
}

// redactor returns the redactor for the client c configures.
func (c *config) redactor() headerRedactor { return newHeaderRedactor(c.apiKey) }

// text returns s, text the server wrote into an error response, with each
// form of the client's API key replaced by "***" (ruling R103): as it is,
// Go-quoted and JSON-escaped ([credentials.add]). The zero redactor, and a
// client's whose key is shorter than [minKeyNeedleBytes] (R68), return s as
// it is.
func (r headerRedactor) text(s string) string {
	if r.key == "" {
		return s
	}
	out, _ := credentials(nil).add(r.key).longestFirst().replace(s)
	return out
}

// header returns h's headers in a new map in which every value of each
// header that is a credential is replaced by "***", one "***" per value, and
// every other header shares its value slice with h; nil stays nil. The error
// types that keep a response's header store this map, so no rendering of
// them, and no caller that dumps their Header, shows a credential the server
// sent.
func (r headerRedactor) header(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	out := make(http.Header, len(h))
	for name, values := range h {
		if !isCredential(name, values, r.key) {
			out[name] = values
			continue
		}
		masked := make([]string, len(values))
		for i := range masked {
			masked[i] = redacted
		}
		out[name] = masked
	}
	return out
}

// redactedHeaders is a header map as a log record shows it: a group with one
// attribute per header, in name order, whose value is the header's values
// joined by ", ", or "***" when they are a credential ([isCredential]). Build
// it with [newRedactedHeaders].
//
// It is a [slog.LogValuer], so the map is walked and its values joined only
// when a handler keeps the record. Boxing the value into an attribute still
// allocates, so a request path that must not allocate with debug records off
// checks [slog.Logger.Enabled] before it builds the attribute.
//
// No rendering prints the map or the key. Every fmt verb prints the redacted
// form ([redactedHeaders.Format]), and so do an unresolved [slog.Value] and
// [slog.Attr], whose String methods print a LogValuer through fmt. The map
// and the key sit behind the one pointer field, so the two renderings fmt
// makes without calling Format, the %p verb and a redactedHeaders held in an
// unexported field of another value, print an address.
type redactedHeaders struct {
	p *headerLog
}

// headerLog is what a [redactedHeaders] renders.
type headerLog struct {
	header http.Header
	apiKey string
}

// newRedactedHeaders returns header as a log record shows it, with any value
// that holds apiKey redacted whatever its header's name, when apiKey is at
// least [minKeyNeedleBytes] long.
func newRedactedHeaders(header http.Header, apiKey string) redactedHeaders {
	return redactedHeaders{p: &headerLog{header: header, apiKey: apiKey}}
}

// LogValue returns the redacted headers as a group value; the zero
// redactedHeaders is an empty group.
func (r redactedHeaders) LogValue() slog.Value {
	if r.p == nil {
		return slog.GroupValue()
	}
	attrs := make([]slog.Attr, 0, len(r.p.header))
	for _, name := range slices.Sorted(maps.Keys(r.p.header)) {
		values := r.p.header[name]
		value := redacted
		if !isCredential(name, values, r.p.apiKey) {
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
func (r redactedHeaders) Format(f fmt.State, _ rune) {
	fmt.Fprint(f, r.LogValue().String())
}
