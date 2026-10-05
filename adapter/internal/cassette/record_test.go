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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
)

// madeWord is the made-up credential word of these tests: never a real
// key, and asserted absent from every recorded byte.
const madeWord = "not-a-key"

// tripFunc is an in-process round tripper for the recorder's tests.
type tripFunc func(*http.Request) (*http.Response, error)

func (f tripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// jsonReply returns an in-process 200 response with a JSON body.
func jsonReply(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// recordOne round-trips one request through a recorder writing dir/name
// and returns the recorded file's bytes.
func recordOne(t *testing.T, dir, name string, req *http.Request, reply *http.Response) []byte {
	t.Helper()
	rec, err := NewRecorder(tripFunc(func(*http.Request) (*http.Response, error) { return reply, nil }), dir, name)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rec.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestRecorderScrubsBeforeWriting records one exchange whose request
// carries a made-up credential word in its userinfo, in every header name
// upstream's recording strips, and in both filtered query parameters, and
// whose response carries headers outside the allowlist. The written file
// must equal the expected bytes exactly: only the content-type headers
// kept, the filtered and undecodable query pairs gone, the userinfo gone,
// JSON bodies decoded, four-space indentation, non-ASCII raw, a trailing
// newline. No byte of the made-up word may appear anywhere in the file,
// and the outgoing request must still carry everything the scrub removes
// from the recording.
func TestRecorderScrubsBeforeWriting(t *testing.T) {
	t.Parallel()
	const requestBody = `{"model":"made-up-model","question":"こんにちは?","n":1.5}`
	const responseBody = `{"answers":{"spam":{"noul":0.25}},"note":"русский"}`
	target := "https://user:" + madeWord + "@api.example.test/v1/answers?api_key=madeupword&key=madeupword2&keep=yes&%zz=x"
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, name := range FilteredRequestHeaders {
		req.Header.Set(name, madeWord)
	}
	req.Header.Set("X-Stainless-Os", "MacOS")

	sent := 0
	reply := &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header: http.Header{
			"Content-Type": {"application/json"},
			"X-Request-Id": {"req-" + madeWord},
			"Set-Cookie":   {"sid=" + madeWord},
		},
		Body: io.NopCloser(strings.NewReader(responseBody)),
	}
	dir := t.TempDir()
	rec, err := NewRecorder(tripFunc(func(got *http.Request) (*http.Response, error) {
		sent++
		// The recording is a wiretap: the outgoing request still carries
		// everything the scrub removes from the recorded copy.
		if got.Header.Get("Authorization") != madeWord || got.Header.Get("X-Stainless-Os") != "MacOS" {
			t.Errorf("the outgoing request lost headers: %v", got.Header)
		}
		if !strings.Contains(got.URL.RawQuery, "api_key=madeupword") || got.URL.User == nil {
			t.Errorf("the outgoing request lost its query or userinfo: %s", got.URL)
		}
		body, err := io.ReadAll(got.Body)
		if err != nil || string(body) != requestBody {
			t.Errorf("the outgoing request body = %q, %v; want the sent body", body, err)
		}
		return reply, nil
	}), dir, "recorded.json")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rec.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	if sent != 1 {
		t.Fatalf("the wrapped transport was called %d times, want 1", sent)
	}
	read, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if string(read) != responseBody {
		t.Errorf("the caller read %q, want the response body unchanged", read)
	}

	data, err := os.ReadFile(filepath.Join(dir, "recorded.json"))
	if err != nil {
		t.Fatal(err)
	}
	const expected = `{
    "version": 1,
    "interactions": [
        {
            "request": {
                "method": "POST",
                "uri": "https://api.example.test/v1/answers?keep=yes",
                "body": {
                    "model": "made-up-model",
                    "question": "こんにちは?",
                    "n": 1.5
                },
                "headers": {
                    "content-type": [
                        "application/json"
                    ]
                }
            },
            "response": {
                "status": {
                    "code": 200,
                    "message": "OK"
                },
                "headers": {
                    "content-type": [
                        "application/json"
                    ]
                },
                "body": {
                    "string": {
                        "answers": {
                            "spam": {
                                "noul": 0.25
                            }
                        },
                        "note": "русский"
                    }
                }
            }
        }
    ]
}
`
	if diff := cmp.Diff(expected, string(data)); diff != "" {
		t.Errorf("recorded file (-want +got):\n%s", diff)
	}
	for _, absent := range []string{madeWord, "madeupword", "%zz"} {
		if bytes.Contains(data, []byte(absent)) {
			t.Errorf("the recorded file holds %q", absent)
		}
	}
	lower := strings.ToLower(string(data))
	for _, name := range FilteredRequestHeaders {
		if strings.Contains(lower, name) {
			t.Errorf("the recorded file holds the header name %q", name)
		}
	}
}

// TestRecorderOutputLoadsAndReplays closes the loop: a recorded file is
// read back by Load and its transport replays the recorded exchange for
// the scrubbed request.
func TestRecorderOutputLoadsAndReplays(t *testing.T) {
	t.Parallel()
	const requestBody = `{"model":"made-up-model","n":2}`
	const responseBody = `{"answers":{},"usage":{"input_tokens":3}}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example.test/v1/answers?keep=yes&key=madeupword", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	dir := t.TempDir()
	recordOne(t, dir, "loop.json", req, jsonReply(responseBody)) //nolint:bodyclose // jsonReply builds a response value; the recorder closes its body.

	c, err := Load(filepath.Join(dir, "loop.json"))
	if err != nil {
		t.Fatalf("Load() on the recorded file: %v", err)
	}
	tr := c.Transport()
	// The replayed request carries the scrubbed URI: the recording removed
	// the filtered parameter, so the recorded exchange answers the request
	// without it.
	s, payload, err := send(t, tr, http.MethodPost, "https://api.example.test/v1/answers?keep=yes", `{"n":2,"model":"made-up-model"}`)
	if err != nil {
		t.Fatalf("replaying the recorded exchange: %v", err)
	}
	if s.statusCode != http.StatusOK {
		t.Errorf("replayed status = %d, want 200", s.statusCode)
	}
	equal, err := jsonx.Equal(payload, []byte(responseBody))
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Errorf("replayed body = %s, want the recorded response body", payload)
	}
	if n := tr.Unconsumed(); n != 0 {
		t.Errorf("Unconsumed() = %d after the replay, want 0", n)
	}
}

// TestRecorderBodiesAndStatuses records several exchanges through one
// recorder and reads the file back: the interactions keep their order, a
// request without a body is recorded as the empty string, a body that is
// not a JSON object or array is recorded as a string, and a status line
// without a message falls back to the standard text.
func TestRecorderBodiesAndStatuses(t *testing.T) {
	t.Parallel()
	replies := []*http.Response{
		{
			StatusCode: http.StatusNotFound,
			Status:     "",
			Header:     http.Header{"Content-Type": {"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("plain text")),
		},
		{
			StatusCode: http.StatusTeapot,
			Status:     "418 short and stout",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("42")),
		},
		jsonReply(`[1,{}]`), //nolint:bodyclose // jsonReply builds a response value; the recorder closes its body.
	}
	calls := 0
	rec, err := NewRecorder(tripFunc(func(*http.Request) (*http.Response, error) {
		reply := replies[calls]
		calls++
		return reply, nil
	}), t.TempDir()+"/nested/cassettes-go", "many.json")
	if err != nil {
		t.Fatal(err)
	}

	get, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.example.test/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	post, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example.test/v1/echo", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example.test/v1/echo", strings.NewReader(`{"n":1}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []*http.Request{get, post, again} {
		resp, err := rec.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip() error = %v", err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}

	c, err := Load(filepath.Join(rec.dir, "many.json"))
	if err != nil {
		t.Fatalf("Load() on the recorded file: %v", err)
	}
	if len(c.Interactions) != 3 {
		t.Fatalf("len(Interactions) = %d, want 3", len(c.Interactions))
	}
	first, second, third := c.Interactions[0], c.Interactions[1], c.Interactions[2]
	if first.Request.Method != http.MethodGet || string(first.Request.Body) != "" || first.Request.BodyIsJSON {
		t.Errorf("first request = %q %q (JSON %t), want the bodyless GET as an empty string", first.Request.Method, first.Request.Body, first.Request.BodyIsJSON)
	}
	if first.Response.StatusCode != http.StatusNotFound || first.Response.StatusMessage != "Not Found" {
		t.Errorf("first response status = %d %q, want 404 with the standard text", first.Response.StatusCode, first.Response.StatusMessage)
	}
	if string(first.Response.Body) != "plain text" || first.Response.BodyIsJSON {
		t.Errorf("first response body = %q (JSON %t), want the text kept as a string", first.Response.Body, first.Response.BodyIsJSON)
	}
	if string(second.Request.Body) != "not json" || second.Request.BodyIsJSON {
		t.Errorf("second request body = %q (JSON %t), want the non-JSON text as a string", second.Request.Body, second.Request.BodyIsJSON)
	}
	if second.Response.StatusMessage != "short and stout" || string(second.Response.Body) != "42" || second.Response.BodyIsJSON {
		t.Errorf("second response = %q %q (JSON %t), want the status line's own text and the scalar as a string", second.Response.StatusMessage, second.Response.Body, second.Response.BodyIsJSON)
	}
	if len(second.Response.Headers) != 0 {
		t.Errorf("second response headers = %v, want none recorded", second.Response.Headers)
	}
	if !third.Request.BodyIsJSON || !third.Response.BodyIsJSON {
		t.Errorf("third interaction JSON flags = %t %t, want both decoded", third.Request.BodyIsJSON, third.Response.BodyIsJSON)
	}
	equal, err := jsonx.Equal(third.Response.Body, []byte(`[1,{}]`))
	if err != nil || !equal {
		t.Errorf("third response body = %s (%v), want the recorded array", third.Response.Body, err)
	}
}

// TestNewRecorderRefusals checks the constructor: the committed upstream
// fixture directories are refused in any spelling that reaches them, a
// name with a path in it is refused, and a missing transport is refused.
func TestNewRecorderRefusals(t *testing.T) {
	t.Parallel()
	next := tripFunc(func(*http.Request) (*http.Response, error) { return jsonReply("{}"), nil })
	tests := map[string]struct {
		next    http.RoundTripper
		dir     string
		name    string
		wantErr string
	}{
		"error: no transport": {
			next:    nil,
			dir:     t.TempDir(),
			name:    "a.json",
			wantErr: "needs the transport",
		},
		"error: the upstream cassette directory": {
			next:    next,
			dir:     "testdata/cassettes",
			name:    "a.json",
			wantErr: "is refused",
		},
		"error: under the upstream cassette directory": {
			next:    next,
			dir:     "testdata/cassettes/sub",
			name:    "a.json",
			wantErr: "is refused",
		},
		"error: the expected-response directory": {
			next:    next,
			dir:     "testdata/expected",
			name:    "a.json",
			wantErr: "is refused",
		},
		"error: an absolute path into the expected-response directory": {
			next:    next,
			dir:     filepath.Join(t.TempDir(), "testdata", "expected", "x"),
			name:    "a.json",
			wantErr: "is refused",
		},
		"error: a dot-dot spelling that cleans into the cassette directory": {
			next:    next,
			dir:     "testdata/other/../cassettes",
			name:    "a.json",
			wantErr: "is refused",
		},
		"error: an empty name": {
			next:    next,
			dir:     t.TempDir(),
			name:    "",
			wantErr: "not a bare file name",
		},
		"error: a name with a path in it": {
			next:    next,
			dir:     t.TempDir(),
			name:    "sub/a.json",
			wantErr: "not a bare file name",
		},
		"error: a dot-dot name": {
			next:    next,
			dir:     t.TempDir(),
			name:    "..",
			wantErr: "not a bare file name",
		},
		"success: the sibling directory for the port's own recordings": {
			next: next,
			dir:  "testdata/cassettes-go",
			name: "a.json",
		},
		"success: a scratch directory": {
			next: next,
			dir:  t.TempDir(),
			name: "a.json",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec, err := NewRecorder(tt.next, tt.dir, tt.name)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("NewRecorder() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewRecorder() = %v, %v; want an error containing %q", rec, err, tt.wantErr)
			}
		})
	}
}

// TestRecorderErrors checks the failure paths: a transport failure, an
// unreadable request or response body, and an unwritable target each
// return an error, and nothing is recorded for the failed exchange.
func TestRecorderErrors(t *testing.T) {
	t.Parallel()
	blockedDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blockedDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	blockedPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(blockedPath, "a.json"), 0o750); err != nil {
		t.Fatal(err)
	}
	// deep is a JSON value nested past what the ordered writer re-encodes,
	// so a decoded body cannot be rebuilt for the file.
	deep := strings.Repeat("[", 300) + strings.Repeat("]", 300)
	tests := map[string]struct {
		dir     string
		next    tripFunc
		body    io.Reader
		wantErr string
	}{
		"error: the wrapped transport fails": {
			dir:     t.TempDir(),
			next:    func(*http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF },
			wantErr: io.ErrUnexpectedEOF.Error(),
		},
		"error: the request body does not read": {
			dir:     t.TempDir(),
			next:    func(*http.Request) (*http.Response, error) { return jsonReply("{}"), nil },
			body:    failingReader{},
			wantErr: "reading the request body",
		},
		"error: the response body does not read": {
			dir: t.TempDir(),
			next: func(*http.Request) (*http.Response, error) {
				reply := jsonReply("{}")
				reply.Body = io.NopCloser(failingReader{})
				return reply, nil
			},
			wantErr: "reading the response body",
		},
		"error: the response body does not close": {
			dir: t.TempDir(),
			next: func(*http.Request) (*http.Response, error) {
				reply := jsonReply("{}")
				reply.Body = failingCloser{strings.NewReader("{}")}
				return reply, nil
			},
			wantErr: "reading the response body",
		},
		"error: a request body nested too deep to re-encode": {
			dir:     t.TempDir(),
			next:    func(*http.Request) (*http.Response, error) { return jsonReply("{}"), nil },
			body:    strings.NewReader(deep),
			wantErr: "re-encoding a recorded body",
		},
		"error: a response body nested too deep to re-encode": {
			dir:     t.TempDir(),
			next:    func(*http.Request) (*http.Response, error) { return jsonReply(deep), nil },
			wantErr: "re-encoding a recorded body",
		},
		"error: the target directory cannot be created": {
			dir:     filepath.Join(blockedDir, "sub"),
			next:    func(*http.Request) (*http.Response, error) { return jsonReply("{}"), nil },
			wantErr: "cassette:",
		},
		"error: the cassette path cannot be written": {
			dir:     blockedPath,
			next:    func(*http.Request) (*http.Response, error) { return jsonReply("{}"), nil },
			wantErr: "cassette:",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec, err := NewRecorder(tt.next, tt.dir, "a.json")
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example.test/v1/answers", tt.body)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := rec.RoundTrip(req) //nolint:bodyclose // the round trip must fail; a failed round trip returns no response to close.
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("RoundTrip() = %v, %v; want an error containing %q", resp, err, tt.wantErr)
			}
			if info, err := os.Stat(filepath.Join(tt.dir, "a.json")); err == nil && info.Mode().IsRegular() {
				t.Errorf("a failed exchange reached the disk: %d bytes", info.Size())
			}
		})
	}
}

// failingCloser is a response body whose Close fails after a clean read.
type failingCloser struct{ io.Reader }

func (failingCloser) Close() error { return io.ErrClosedPipe }

// TestScrubbedQuery pins the query scrub: filtered names removed in any
// position, other pairs kept in their order and spelling, an empty or
// undecodable pair dropped, and an empty query left alone.
func TestScrubbedQuery(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		raw  string
		want string
	}{
		"success: an empty query":                      {raw: "", want: ""},
		"success: nothing filtered":                    {raw: "a=1&b=two", want: "a=1&b=two"},
		"success: both filtered names removed":         {raw: "api_key=x&a=1&key=y", want: "a=1"},
		"success: a filtered name without a value":     {raw: "key&a=1", want: "a=1"},
		"success: an escaped filtered name is decoded": {raw: "api%5Fkey=x&a=1", want: "a=1"},
		"success: empty pairs are dropped":             {raw: "a=1&&b=2", want: "a=1&b=2"},
		"success: an undecodable name is dropped":      {raw: "%zz=x&a=1", want: "a=1"},
		"success: everything filtered leaves nothing":  {raw: "api_key=x", want: ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := scrubbedQuery(tt.raw); got != tt.want {
				t.Errorf("scrubbedQuery(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// TestIndentedMatchesPython pins the reformatter against the shapes
// Python's json.dumps(..., indent=4) writes: nesting, empty containers,
// separators, and bytes inside string literals left alone.
func TestIndentedMatchesPython(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		compact string
		want    string
	}{
		"success: an object of scalars": {
			compact: `{"a":1,"b":"x"}`,
			want:    "{\n    \"a\": 1,\n    \"b\": \"x\"\n}\n",
		},
		"success: empty containers stay on one line": {
			compact: `{"a":{},"b":[]}`,
			want:    "{\n    \"a\": {},\n    \"b\": []\n}\n",
		},
		"success: an array nesting an object": {
			compact: `[1,{"a":[2]}]`,
			want:    "[\n    1,\n    {\n        \"a\": [\n            2\n        ]\n    }\n]\n",
		},
		"success: brackets and separators inside strings are bytes of the string": {
			compact: `{"a":"{[,:]} \" \\","b":1}`,
			want:    "{\n    \"a\": \"{[,:]} \\\" \\\\\",\n    \"b\": 1\n}\n",
		},
		"success: a lone scalar": {
			compact: `true`,
			want:    "true\n",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, string(indented([]byte(tt.compact)))); diff != "" {
				t.Errorf("indented(%s) (-want +got):\n%s", tt.compact, diff)
			}
		})
	}
}
