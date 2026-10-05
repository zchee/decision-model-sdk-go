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
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
)

// recordedRequestHeaders holds the only request header names a recording
// of this package keeps, lowercase as a cassette spells them. Upstream's
// recording removes only [FilteredRequestHeaders]; this recorder keeps
// nothing but content-type, so every name of that list is dropped by
// construction and a recorded file cannot carry a header upstream's
// recording would have removed.
var recordedRequestHeaders = []string{"content-type"}

// Recorder is an http.RoundTripper that performs each round trip through
// the transport it wraps and records the exchanges as one vcrpy JSON
// cassette in the format [Load] reads: a document {"version": 1,
// "interactions": [...]}, each interaction a request {method, uri, body,
// headers} and a response {status{code, message}, headers, body{string}},
// indented by four spaces with non-ASCII raw and a trailing newline, and
// a body that parses as a JSON object or array stored decoded, so a
// recorded file diffs like a committed upstream cassette.
//
// Every exchange is scrubbed before any byte of it is serialized, so no
// unscrubbed byte ever reaches the disk: the recorded request keeps no
// header but the [recordedRequestHeaders] (every [FilteredRequestHeaders]
// name among them); the recorded URI drops its userinfo and every query
// parameter [FilteredQueryParameters] names, along with any pair whose
// name does not decode; and the recorded response keeps only the
// [AllowedResponseHeaders] headers. The bodies are recorded as sent and
// received. A request without a body is recorded as the empty string,
// which [Load] reads back, where upstream records null, which it refuses.
//
// The whole cassette is rewritten after each exchange, so the file always
// holds every exchange recorded so far. A Recorder is safe for concurrent
// use; the recorded order is the order the round trips complete. It
// writes nothing but the cassette file and returns every failure as an
// error.
type Recorder struct {
	next http.RoundTripper
	dir  string
	path string

	mu           sync.Mutex
	interactions []jsonx.Value
}

// NewRecorder returns a recorder over next that writes the cassette
// dir/name. It refuses a nil next, a dir in or under testdata/cassettes
// or testdata/expected (the committed upstream files, which a recording
// must never touch), and a name that is not a bare file name. The
// directory is created at the first recorded exchange.
func NewRecorder(next http.RoundTripper, dir, name string) (*Recorder, error) {
	if next == nil {
		return nil, errors.New("cassette: a recorder needs the transport it wraps")
	}
	if refusedRecordingDir(dir) {
		return nil, fmt.Errorf("cassette: recording under %s is refused: testdata/cassettes and testdata/expected hold the committed upstream files", dir)
	}
	if name == "" || name == "." || name == ".." || name != filepath.Base(name) {
		return nil, fmt.Errorf("cassette: the cassette name %q is not a bare file name", name)
	}
	return &Recorder{next: next, dir: dir, path: filepath.Join(dir, name)}, nil
}

// refusedRecordingDir reports whether dir, judged on the path as given,
// lies in or under a directory testdata/cassettes or testdata/expected.
// A sibling such as testdata/cassettes-go is allowed.
func refusedRecordingDir(dir string) bool {
	elems := strings.Split(filepath.ToSlash(filepath.Clean(dir)), "/")
	for i := 0; i+1 < len(elems); i++ {
		if elems[i] == "testdata" && (elems[i+1] == "cassettes" || elems[i+1] == "expected") {
			return true
		}
	}
	return false
}

// RoundTrip performs the round trip through the wrapped transport and, on
// success, appends the scrubbed exchange to the cassette and rewrites the
// file. The returned response carries the body read back whole, so the
// caller reads it as if the recorder were not there.
func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := readBody(req)
	if err != nil {
		return nil, fmt.Errorf("cassette: reading the request body: %w", err)
	}
	send := req.Clone(req.Context())
	if req.Body != nil {
		send.Body = io.NopCloser(bytes.NewReader(body))
	}
	resp, err := r.next.RoundTrip(send)
	if err != nil {
		return nil, err
	}
	respBody, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("cassette: reading the response body: %w", err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	interaction, err := recordedInteraction(send, body, resp, respBody)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.interactions = append(r.interactions, interaction)
	if err := r.write(); err != nil {
		return nil, err
	}
	return resp, nil
}

// write serializes every recorded interaction and rewrites the cassette
// file. The interactions it serializes were scrubbed as they were
// recorded, so no other representation of the exchanges ever exists on
// disk.
func (r *Recorder) write() error {
	doc := jsonx.Object(
		jsonx.Member{Name: "version", Value: jsonx.Number("1")},
		jsonx.Member{Name: "interactions", Value: jsonx.Array(r.interactions...)},
	)
	compact, err := jsonx.Marshal(doc)
	if err != nil {
		return fmt.Errorf("cassette: serializing the recording: %w", err)
	}
	if err := os.MkdirAll(r.dir, 0o750); err != nil {
		return fmt.Errorf("cassette: %w", err)
	}
	if err := os.WriteFile(r.path, indented(compact), 0o600); err != nil {
		return fmt.Errorf("cassette: %w", err)
	}
	return nil
}

// recordedInteraction builds one scrubbed interaction from a performed
// round trip.
func recordedInteraction(req *http.Request, reqBody []byte, resp *http.Response, respBody []byte) (jsonx.Value, error) {
	reqValue, err := requestValue(req, reqBody)
	if err != nil {
		return jsonx.Value{}, err
	}
	respValue, err := responseValue(resp, respBody)
	if err != nil {
		return jsonx.Value{}, err
	}
	return jsonx.Object(
		jsonx.Member{Name: "request", Value: reqValue},
		jsonx.Member{Name: "response", Value: respValue},
	), nil
}

// requestValue builds the recorded request: the method, the scrubbed URI,
// the body, and the kept headers, in the member order upstream's
// cassettes use.
func requestValue(req *http.Request, body []byte) (jsonx.Value, error) {
	bodyValue, err := recordedBody(body)
	if err != nil {
		return jsonx.Value{}, err
	}
	return jsonx.Object(
		jsonx.Member{Name: "method", Value: jsonx.String(req.Method)},
		jsonx.Member{Name: "uri", Value: jsonx.String(recordedURI(req.URL))},
		jsonx.Member{Name: "body", Value: bodyValue},
		jsonx.Member{Name: "headers", Value: keptHeaders(req.Header, recordedRequestHeaders)},
	), nil
}

// responseValue builds the recorded response: the status, the kept
// headers, and the body under body.string.
func responseValue(resp *http.Response, body []byte) (jsonx.Value, error) {
	bodyValue, err := recordedBody(body)
	if err != nil {
		return jsonx.Value{}, err
	}
	status := jsonx.Object(
		jsonx.Member{Name: "code", Value: jsonx.Number(strconv.Itoa(resp.StatusCode))},
		jsonx.Member{Name: "message", Value: jsonx.String(statusMessage(resp))},
	)
	return jsonx.Object(
		jsonx.Member{Name: "status", Value: status},
		jsonx.Member{Name: "headers", Value: keptHeaders(resp.Header, AllowedResponseHeaders)},
		jsonx.Member{Name: "body", Value: jsonx.Object(jsonx.Member{Name: "string", Value: bodyValue})},
	), nil
}

// recordedBody returns a body's recorded form: a body that parses as a
// JSON object or array decoded, in the float spelling of a response body,
// any other body as the string of its bytes, and no body as the empty
// string.
func recordedBody(body []byte) (jsonx.Value, error) {
	if len(body) == 0 {
		return jsonx.String(""), nil
	}
	node, err := jsonx.Read(body)
	if err != nil || (node.Kind() != jsonx.KindObject && node.Kind() != jsonx.KindArray) {
		return jsonx.String(string(body)), nil
	}
	value, err := node.Value(jsonx.Repr)
	if err != nil {
		return jsonx.Value{}, fmt.Errorf("cassette: re-encoding a recorded body: %w", err)
	}
	return value, nil
}

// recordedURI renders a request URL for the cassette: the userinfo
// dropped, and the scrubbed query.
func recordedURI(u *url.URL) string {
	r := u.Clone()
	r.User = nil
	r.RawQuery = scrubbedQuery(r.RawQuery)
	return r.String()
}

// scrubbedQuery removes every query pair whose name
// [FilteredQueryParameters] lists from a raw query, keeping the remaining
// pairs in their order and spelling. A pair whose name does not decode
// could hide anything, so the scrub drops it rather than keep bytes it
// cannot judge.
func scrubbedQuery(raw string) string {
	if raw == "" {
		return ""
	}
	var kept []string
	for segment := range strings.SplitSeq(raw, "&") {
		if segment == "" {
			continue
		}
		name, _, _ := strings.Cut(segment, "=")
		decoded, err := url.QueryUnescape(name)
		if err != nil || slices.Contains(FilteredQueryParameters, decoded) {
			continue
		}
		kept = append(kept, segment)
	}
	return strings.Join(kept, "&")
}

// keptHeaders reduces headers to the kept names, recorded lowercase as
// upstream's cassettes spell them, each with its values in order.
func keptHeaders(h http.Header, kept []string) jsonx.Value {
	var members []jsonx.Member
	for _, name := range slices.Sorted(maps.Keys(h)) {
		lower := strings.ToLower(name)
		if !slices.Contains(kept, lower) {
			continue
		}
		values := make([]jsonx.Value, 0, len(h[name]))
		for _, value := range h[name] {
			values = append(values, jsonx.String(value))
		}
		members = append(members, jsonx.Member{Name: lower, Value: jsonx.Array(values...)})
	}
	return jsonx.Object(members...)
}

// statusMessage returns the response's status message: the text after the
// code in its status line, or the standard text for the code when the
// status line does not carry one.
func statusMessage(resp *http.Response) string {
	if message, ok := strings.CutPrefix(resp.Status, strconv.Itoa(resp.StatusCode)+" "); ok {
		return message
	}
	return http.StatusText(resp.StatusCode)
}

// indented reformats one compactly marshaled JSON document as Python's
// json.dumps(..., indent=4) formats it: four spaces per level, ": " after
// a member name, an empty object or array on one line, and a trailing
// newline. The walk is purely syntactic; it changes no byte inside a
// string literal.
func indented(compact []byte) []byte {
	var out bytes.Buffer
	out.Grow(2 * len(compact))
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(compact); i++ {
		c := compact[i]
		if inString {
			out.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
			out.WriteByte(c)
		case '{', '[':
			if i+1 < len(compact) && compact[i+1] == closingOf(c) {
				out.WriteByte(c)
				out.WriteByte(compact[i+1])
				i++
				continue
			}
			depth++
			out.WriteByte(c)
			newlineIndent(&out, depth)
		case '}', ']':
			depth--
			newlineIndent(&out, depth)
			out.WriteByte(c)
		case ',':
			out.WriteByte(c)
			newlineIndent(&out, depth)
		case ':':
			out.WriteString(": ")
		default:
			out.WriteByte(c)
		}
	}
	out.WriteByte('\n')
	return out.Bytes()
}

// closingOf returns the closing bracket of an opening one.
func closingOf(open byte) byte {
	if open == '{' {
		return '}'
	}
	return ']'
}

// newlineIndent writes a newline and depth levels of four spaces.
func newlineIndent(out *bytes.Buffer, depth int) {
	out.WriteByte('\n')
	for range depth {
		out.WriteString("    ")
	}
}
