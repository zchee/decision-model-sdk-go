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

package cassette

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
)

// SentRequest is one request the replay matched, kept in match order so a
// test can read what a provider sent.
type SentRequest struct {
	// URL is the sent request's URL as the request spelled it.
	URL string
	// Body is the sent request's body, read whole.
	Body []byte
}

// Transport replays a cassette's interactions as vcrpy replays them
// (tests/conftest.py of system-one-adapter-python v0.2.1): a request
// matches an unconsumed interaction on method, scheme, host, port, path,
// query and body, the body compared as a JSON value when the recorded body
// is one; each interaction is served at most once; a request that matches
// nothing fails the round trip. A test asserts Unconsumed is zero at its
// end, so a replay that served nothing cannot pass silently.
type Transport struct {
	mu       sync.Mutex
	pending  []Interaction
	consumed []bool
	sent     []SentRequest
}

// Transport returns a replayer over the cassette's interactions, each
// still unconsumed. The interaction slice and its structs are copied, but
// their body bytes, header maps and header-value slices are borrowed.
// Treat that nested data as immutable for the lifetime of the transport and
// its responses. Replacing or reordering Interactions elements is independent
// of the transport; changing their borrowed nested data is not.
func (c *Cassette) Transport() *Transport {
	return &Transport{pending: slices.Clone(c.Interactions), consumed: make([]bool, len(c.Interactions))}
}

// Unconsumed returns how many interactions have not been served.
func (t *Transport) Unconsumed() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, done := range t.consumed {
		if !done {
			n++
		}
	}
	return n
}

// Requests returns the matched requests in the order they were served.
// The returned slice and SentRequest values are independent copies, but
// their Body bytes are borrowed from the transport and must remain immutable.
// Replacing a returned element or its fields does not change retained requests;
// writing through its Body slice would change the shared bytes.
func (t *Transport) Requests() []SentRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.sent)
}

// RoundTrip serves the first unconsumed interaction the request matches,
// or fails with an error naming the request and the first difference from
// the nearest candidate. The error never quotes a query value, and never
// quotes a body beyond the first differing member's path: a differing
// value is named by its path and kind, not shown.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := readBody(req)
	if err != nil {
		return nil, fmt.Errorf("cassette: reading the request body: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	nearestKey := -1
	nearestDiff := ""
	anyPending := false
	for i, ix := range t.pending {
		if t.consumed[i] {
			continue
		}
		anyPending = true
		key, diff := matchInteraction(req, body, ix.Request)
		if diff == "" {
			t.consumed[i] = true
			t.sent = append(t.sent, SentRequest{URL: req.URL.String(), Body: body})
			return serve(ix.Response, req), nil
		}
		if key > nearestKey {
			nearestKey = key
			nearestDiff = diff
		}
	}
	if !anyPending {
		return nil, fmt.Errorf("cassette: no interaction matches %s %s: every recorded interaction was already served", req.Method, redactURL(req.URL))
	}
	return nil, fmt.Errorf("cassette: no interaction matches %s %s: the nearest candidate differs in %s", req.Method, redactURL(req.URL), nearestDiff)
}

// readBody returns the request's whole body and closes it; a request
// without one gives nil.
func readBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	defer req.Body.Close()
	return io.ReadAll(req.Body)
}

// serve rebuilds the recorded response for req.
func serve(r Response, req *http.Request) *http.Response {
	header := make(http.Header, len(r.Headers))
	for name, values := range r.Headers {
		for _, value := range values {
			header.Add(name, value)
		}
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", r.StatusCode, r.StatusMessage),
		StatusCode:    r.StatusCode,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(r.Body)),
		ContentLength: int64(len(r.Body)),
		Request:       req,
	}
}

// The match keys, in the order upstream matches them, for ranking how far
// a candidate got before its first difference.
const (
	keyMethod = iota
	keyScheme
	keyHost
	keyPort
	keyPath
	keyQuery
	keyBody
)

// matchInteraction compares the sent request against one recorded request
// and returns the first difference, "" when they match, with the index of
// the first differing key.
func matchInteraction(req *http.Request, body []byte, rec Request) (key int, diff string) {
	recURL, err := url.Parse(rec.URI)
	if err != nil {
		// url.Parse's own error quotes the whole URI, userinfo and query
		// values included, so the message never carries it.
		return keyMethod, "a recorded URI that does not parse"
	}
	if req.Method != rec.Method {
		return keyMethod, fmt.Sprintf("method (%q is not the recorded %q)", req.Method, rec.Method)
	}
	if req.URL.Scheme != recURL.Scheme {
		return keyScheme, fmt.Sprintf("scheme (%q is not the recorded %q)", req.URL.Scheme, recURL.Scheme)
	}
	// Hosts are compared without regard to case, as vcrpy compares them.
	if !strings.EqualFold(req.URL.Hostname(), recURL.Hostname()) {
		return keyHost, fmt.Sprintf("host (%q is not the recorded %q)", req.URL.Hostname(), recURL.Hostname())
	}
	if got, want := effectivePort(req.URL), effectivePort(recURL); got != want {
		return keyPort, fmt.Sprintf("port (%q is not the recorded %q)", got, want)
	}
	// Paths are compared in escaped form, as vcrpy compares them: a
	// recorded /a%2Fb is not a sent /a/b although both decode alike.
	if req.URL.EscapedPath() != recURL.EscapedPath() {
		return keyPath, fmt.Sprintf("path (%q is not the recorded %q)", req.URL.EscapedPath(), recURL.EscapedPath())
	}
	if diff := queryDiff(queryPairs(req.URL.RawQuery), queryPairs(recURL.RawQuery)); diff != "" {
		return keyQuery, diff
	}
	if diff := bodyDiff(body, rec); diff != "" {
		return keyBody, diff
	}
	return keyBody, ""
}

// effectivePort returns the URL's port, the scheme's default when none is
// spelled.
func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch u.Scheme {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}

// queryPairs follows urllib.parse.parse_qsl's defaults used by vcrpy:
// split on ampersands, discard blank values, and decode each component
// without rejecting semicolons or malformed percent escapes.
func queryPairs(raw string) url.Values {
	values := make(url.Values)
	for pair := range strings.SplitSeq(raw, "&") {
		name, value, _ := strings.Cut(pair, "=")
		if value == "" {
			continue
		}
		values.Add(unquoteQuery(name), unquoteQuery(value))
	}
	return values
}

// unquoteQuery is Python's unquote_plus with UTF-8 replacement decoding.
// QueryUnescape cannot be used directly because it rejects a malformed
// escape instead of preserving it alongside the valid escapes.
func unquoteQuery(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '+' {
			b.WriteByte(' ')
			continue
		}
		if s[i] == '%' && i+2 < len(s) {
			if value, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(value))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	decoded := b.String()
	if utf8.ValidString(decoded) {
		return decoded
	}
	var normalized strings.Builder
	for len(decoded) > 0 {
		r, size := utf8.DecodeRuneInString(decoded)
		if r == utf8.RuneError && size == 1 {
			// Python replaces an incomplete valid prefix once, without
			// consuming the byte that makes the sequence invalid.
			for size < len(decoded) && !utf8.FullRuneInString(decoded[:size+1]) {
				size++
			}
		}
		normalized.WriteRune(r)
		decoded = decoded[size:]
	}
	return normalized.String()
}

// queryDiff compares two parsed query strings and names the first
// differing parameter, without quoting any value.
func queryDiff(got, want url.Values) string {
	names := make([]string, 0, len(got)+len(want))
	for name := range got {
		names = append(names, name)
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		g, w := got[name], want[name]
		switch {
		case len(g) == 0:
			return fmt.Sprintf("query (the recorded parameter %q is not in the request)", name)
		case len(w) == 0:
			return fmt.Sprintf("query (the request parameter %q is not recorded)", name)
		case !valuesEqual(g, w):
			return fmt.Sprintf("query (the parameter %q differs)", name)
		}
	}
	return ""
}

// valuesEqual reports whether two parameter value lists are equal
// regardless of order.
func valuesEqual(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// bodyDiff compares the sent body against the recorded one: as a JSON
// value when the recorded body was stored as one (vcrpy's body matcher
// parses an application/json body, so a difference of member order alone
// matches), byte for byte otherwise. jsonx.Equal decides; firstDiff only
// phrases the difference for the error.
func bodyDiff(body []byte, rec Request) string {
	if !rec.BodyIsJSON {
		if !bytes.Equal(body, rec.Body) {
			return "body (the bytes differ from the recorded non-JSON body)"
		}
		return ""
	}
	if _, err := jsonx.Read(body); err != nil {
		return "body (the request body is not a JSON text, the recorded body is one)"
	}
	equal, err := jsonx.Equal(body, rec.Body)
	if err != nil {
		return fmt.Sprintf("body (the recorded body does not read back: %v)", err)
	}
	if equal {
		return ""
	}
	got, _ := jsonx.Read(body)
	want, _ := jsonx.Read(rec.Body)
	if path, detail := firstDiff(got, want, "$"); path != "" {
		return fmt.Sprintf("body at %s (%s)", path, detail)
	}
	return "body (the values differ in a number beyond float64 precision)"
}

// firstDiff phrases a difference jsonx.Equal already found: it walks two
// read JSON values, member order ignored, and returns the path and a
// one-line description of the first difference, or "" when it locates
// none. It never decides a match, and it never quotes a value: the
// description names the path and the kind of difference only. Numeric
// differences use the same exact comparison as jsonx.Equal, so rounding
// cannot hide the differing member.
func firstDiff(got, want jsonx.Node, path string) (string, string) {
	bothNumbers := got.Kind() == jsonx.KindNumber && want.Kind() == jsonx.KindNumber
	if got.Kind() != want.Kind() && !bothNumbers {
		return path, fmt.Sprintf("kind %s is not the recorded %s", kindName(got), kindName(want))
	}
	switch want.Kind() {
	case jsonx.KindNumber:
		if numbersEqual(got, want) {
			return "", ""
		}
		return path, "the numbers differ"
	case jsonx.KindString:
		if got.Text() == want.Text() {
			return "", ""
		}
		return path, "the strings differ"
	case jsonx.KindArray:
		if got.Len() != want.Len() {
			return path, fmt.Sprintf("an array of %d elements is not the recorded %d", got.Len(), want.Len())
		}
		for i := range want.Len() {
			if p, d := firstDiff(got.Index(i), want.Index(i), fmt.Sprintf("%s[%d]", path, i)); p != "" {
				return p, d
			}
		}
		return "", ""
	case jsonx.KindObject:
		for i := range got.Len() {
			name := got.Name(i)
			if _, ok := want.Member(name); !ok {
				return memberPath(path, name), "a member the recorded body does not have"
			}
		}
		for i := range want.Len() {
			name := want.Name(i)
			gotMember, ok := got.Member(name)
			if !ok {
				return memberPath(path, name), "a recorded member the body does not have"
			}
			wantMember, _ := want.Member(name)
			if p, d := firstDiff(gotMember, wantMember, memberPath(path, name)); p != "" {
				return p, d
			}
		}
		return "", ""
	default:
		return "", ""
	}
}

// numbersEqual uses the matcher's exact integer/float comparison to locate
// a numeric difference. Both nodes already hold valid JSON number literals.
func numbersEqual(got, want jsonx.Node) bool {
	equal, err := jsonx.Equal([]byte(got.Text()), []byte(want.Text()))
	return err == nil && equal
}

// memberPath appends an object member to a diff path.
func memberPath(path, name string) string {
	for _, r := range name {
		plain := r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')
		if !plain {
			return fmt.Sprintf("%s[%q]", path, name)
		}
	}
	return path + "." + name
}

// kindName names a node's kind for a diff message.
func kindName(v jsonx.Node) string {
	switch v.Kind() {
	case jsonx.KindNull:
		return "null"
	case jsonx.KindTrue:
		return "true"
	case jsonx.KindFalse:
		return "false"
	case jsonx.KindNumber:
		return "number"
	case jsonx.KindString:
		return "string"
	case jsonx.KindArray:
		return "array"
	case jsonx.KindObject:
		return "object"
	default:
		return "unknown"
	}
}

// redactURL renders a URL for an error message: userinfo removed and every
// query value emptied, so only parameter names remain.
func redactURL(u *url.URL) string {
	redacted := *u
	redacted.User = nil
	if redacted.RawQuery != "" {
		names := make([]string, 0, len(redacted.Query()))
		for name := range redacted.Query() {
			names = append(names, name)
		}
		slices.Sort(names)
		for i, name := range names {
			names[i] = name + "="
		}
		redacted.RawQuery = strings.Join(names, "&")
	}
	return redacted.String()
}
