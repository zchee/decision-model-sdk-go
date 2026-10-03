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

package rest

import (
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// The tests of this file talk to an httptest.Server of their own on the
// loopback interface; no request leaves the process. None of them waits
// for a timeout: those are in error_test.go, on a fake clock.

// userinfo is a made-up user and password, with the "@" that ends them in
// a URL. The tests join it into their URLs.
const userinfo = "made-up-user:made-up-word@"

// echoStatusHeader names the request header that tells the echo server
// which status to answer with.
const echoStatusHeader = "X-Echo-Status"

// newEchoServer starts a server that answers every request with the status
// its X-Echo-Status header names and the request's own body, and with the
// response headers given.
func newEchoServer(t *testing.T, header http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, err := strconv.Atoi(r.Header.Get(echoStatusHeader))
		if err != nil {
			http.Error(w, "the test did not name a status", http.StatusTeapot)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "the request body could not be read", http.StatusTeapot)
			return
		}
		maps.Copy(w.Header(), header)
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newOwned returns a Client that owns its HTTP client, closed when the test
// ends.
func newOwned(t *testing.T, cfg Config) *Client {
	t.Helper()
	cfg.HTTPClient = nil
	c := New(cfg)
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	})
	return c
}

// echo posts body to srv and asks for status back.
func echo(t *testing.T, c *Client, srv *httptest.Server, status int, body string) ([]byte, error) {
	t.Helper()
	header := http.Header{echoStatusHeader: {strconv.Itoa(status)}}
	return c.Post(t.Context(), mustURL(t, srv.URL+"/v1/call"), header, []byte(body))
}

// statusError posts body to srv, asks for status back, and returns the
// *llm.StatusError Post must return for it.
func statusError(t *testing.T, c *Client, srv *httptest.Server, status int, body string) *llm.StatusError {
	t.Helper()
	got, err := echo(t, c, srv, status, body)
	if got != nil {
		t.Errorf("Post() body = %q beside an error", got)
	}
	se, ok := err.(*llm.StatusError) //nolint:errorlint // Post returns the status error itself, not a wrapper of it.
	if !ok {
		t.Fatalf("status %d, body %q: Post() error = %#v, want an *llm.StatusError itself", status, body, err)
	}
	return se
}

// TestStatusErrorKeepsStatusAndBody ports the provider half of
// test_status_errors_map_and_preserve_status_and_body of
// system-one-adapter-python v0.2.1 (tests/utils/test_error_handling.py:
// 46-67), with its parameter ids: for each of its six statuses and each
// provider, a response of that status is an *llm.StatusError that keeps
// the status and the body. Upstream uses one body for all three providers;
// here each provider's case sends the error body that provider documents,
// and the body is kept byte for byte.
//
// Each case also checks what the status error holds for a body that is
// empty or only white space: a Body that is empty and not nil for OpenAI
// and Anthropic, whose Python SDKs hand upstream the empty string, and a
// nil Body for Gemini, whose SDK hands over None; and that a body that only
// looks blank is kept as it is.
func TestStatusErrorKeepsStatusAndBody(t *testing.T) {
	statuses := map[string]struct {
		status int
	}{
		"bad-request":  {status: http.StatusBadRequest},
		"auth":         {status: http.StatusUnauthorized},
		"permission":   {status: http.StatusForbidden},
		"rate-limit":   {status: http.StatusTooManyRequests},
		"server":       {status: http.StatusInternalServerError},
		"other-status": {status: http.StatusTeapot},
	}
	providers := map[string]struct {
		body        string
		blankIsNone bool
	}{
		"openai": {
			body: `{"error": {"message": "boom", "type": "invalid_request_error", "param": null, "code": null}}`,
		},
		"anthropic": {
			body: `{"type": "error", "error": {"type": "invalid_request_error", "message": "boom"}, "request_id": "req_made_up"}`,
		},
		"gemini": {
			body:        "{\n  \"error\": {\n    \"code\": \"invalid_request\",\n    \"message\": \"boom\"\n  }\n}\n",
			blankIsNone: true,
		},
	}
	// blank are bodies Python's str.strip leaves nothing of; kept are
	// bodies it leaves something of, which stay as they are.
	blank := map[string]string{
		"empty":                    "",
		"three spaces":             "   ",
		"newline":                  "\n",
		"tab, CR, LF":              "\t\r\n",
		"file separator":           "\x1c",
		"no-break space":           "\u00a0",
		"line separator and space": "\u2028 ",
		"ideographic space":        "\u3000",
	}
	kept := map[string]string{
		"a word between spaces":       "  oops  ",
		"a byte that is not UTF-8":    "\xff",
		"a space and a broken rune":   " \xe2\x80",
		"zero width space":            "\u200b",
		"the JSON string of no text":  `""`,
		"the JSON null between lines": "\nnull\n",
	}

	srv := newEchoServer(t, nil)
	for sname, st := range statuses {
		for pname, p := range providers {
			t.Run(sname+"-"+pname, func(t *testing.T) {
				c := newOwned(t, Config{BlankErrorBodyIsNone: p.blankIsNone})

				se := statusError(t, c, srv, st.status, p.body)
				if se.StatusCode != st.status {
					t.Errorf("StatusCode = %d, want %d", se.StatusCode, st.status)
				}
				if diff := cmp.Diff([]byte(p.body), se.Body); diff != "" {
					t.Errorf("Body mismatch (-want +got):\n%s", diff)
				}

				for name, body := range blank {
					se := statusError(t, c, srv, st.status, body)
					if se.StatusCode != st.status {
						t.Errorf("blank body %s: StatusCode = %d, want %d", name, se.StatusCode, st.status)
					}
					switch {
					case p.blankIsNone && se.Body != nil:
						t.Errorf("blank body %s: Body = %q, want nil: this provider's SDK hands over no body", name, se.Body)
					case !p.blankIsNone && (se.Body == nil || len(se.Body) != 0):
						t.Errorf("blank body %s: Body = %#v, want empty and not nil: this provider's SDK hands over the empty string", name, se.Body)
					}
				}
				for name, body := range kept {
					se := statusError(t, c, srv, st.status, body)
					if diff := cmp.Diff([]byte(body), se.Body); diff != "" {
						t.Errorf("body %s: Body mismatch (-want +got):\n%s", name, diff)
					}
				}
			})
		}
	}
}

// TestIsBlank checks isBlank against Python's str.strip: each of the 29
// characters str.isspace accepts is blank alone, and their neighbours and
// the characters Go's unicode.IsSpace or JSON count differently are not.
func TestIsBlank(t *testing.T) {
	pySpaces := []rune{
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x1c, 0x1d, 0x1e, 0x1f, 0x20, 0x85, 0xa0, 0x1680,
		0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a,
		0x2028, 0x2029, 0x202f, 0x205f, 0x3000,
	}
	if len(pySpaces) != 29 {
		t.Fatalf("the test lists %d characters, want the 29 of str.isspace", len(pySpaces))
	}
	for _, r := range pySpaces {
		if !isBlank([]byte(string(r))) {
			t.Errorf("isBlank(%U) = false, want true", r)
		}
	}
	if !isBlank([]byte(string(pySpaces))) {
		t.Error("isBlank of the 29 characters together = false, want true")
	}

	tests := map[string]struct {
		body string
		want bool
	}{
		"success: nil body":                           {body: "", want: true},
		"success: backspace before tab":               {body: "\x08"},
		"success: shift out after CR":                 {body: "\x0e"},
		"success: escape before the separators":       {body: "\x1b"},
		"success: exclamation mark after space":       {body: "!"},
		"success: NUL":                                {body: "\x00"},
		"success: U+0084 before next line":            {body: "\u0084"},
		"success: U+180E, a space until Unicode 6.3":  {body: "\u180e"},
		"success: zero width space":                   {body: "\u200b"},
		"success: word joiner":                        {body: "\u2060"},
		"success: byte order mark":                    {body: "\ufeff"},
		"success: replacement character":              {body: "\ufffd"},
		"success: a byte that is not UTF-8":           {body: "\xa0"},
		"success: a truncated line separator":         {body: "\xe2\x80"},
		"success: spaces around a letter":             {body: " \n a \n "},
		"success: spaces then a byte that is no rune": {body: "  \xff"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := isBlank([]byte(tt.body)); got != tt.want {
				t.Errorf("isBlank(%q) = %t, want %t", tt.body, got, tt.want)
			}
		})
	}
}

// TestPostSendsJSONAndReturnsTheBody checks the request Post sends and what
// it returns for a status of 200 to 299.
func TestPostSendsJSONAndReturnsTheBody(t *testing.T) {
	type seen struct {
		Method      string
		Path        string
		Query       string
		ContentType string
		Provider    string
		User        string
		Password    string
		Body        string
	}
	requests := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		user, password, _ := r.BasicAuth()
		requests <- seen{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, ContentType: r.Header.Get("Content-Type"),
			Provider: r.Header.Get("X-Provider-Header"), User: user, Password: password, Body: string(body),
		}
		status, _ := strconv.Atoi(r.URL.Query().Get("status"))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"output":"made-up"}`)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	tests := map[string]struct {
		url      string
		header   http.Header
		want     seen
		wantBody string
	}{
		"success: nil header gets the JSON content type": {
			url:      srv.URL + "/v1/responses?status=200",
			want:     seen{Method: http.MethodPost, Path: "/v1/responses", Query: "status=200", ContentType: "application/json", Body: `{"model":"made-up"}`},
			wantBody: `{"output":"made-up"}`,
		},
		"success: the provider's headers are sent": {
			url:      srv.URL + "/v1/messages?status=200",
			header:   http.Header{"X-Provider-Header": {"made-up-value"}},
			want:     seen{Method: http.MethodPost, Path: "/v1/messages", Query: "status=200", ContentType: "application/json", Provider: "made-up-value", Body: `{"model":"made-up"}`},
			wantBody: `{"output":"made-up"}`,
		},
		"success: a content type the provider names is kept": {
			url:      srv.URL + "/v1/call?status=201",
			header:   http.Header{"Content-Type": {"application/json; charset=utf-8"}},
			want:     seen{Method: http.MethodPost, Path: "/v1/call", Query: "status=201", ContentType: "application/json; charset=utf-8", Body: `{"model":"made-up"}`},
			wantBody: `{"output":"made-up"}`,
		},
		"success: userinfo and query of the URL are sent": {
			url:      "http://" + userinfo + host + "/v1/call?status=299&alt=json",
			want:     seen{Method: http.MethodPost, Path: "/v1/call", Query: "status=299&alt=json", ContentType: "application/json", User: "made-up-user", Password: "made-up-word", Body: `{"model":"made-up"}`},
			wantBody: `{"output":"made-up"}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := newOwned(t, Config{})
			before := tt.header.Clone()

			got, err := c.Post(t.Context(), mustURL(t, tt.url), tt.header, []byte(`{"model":"made-up"}`))
			// The handler records a request before it answers, so the
			// record is here once Post has returned. It is taken before
			// any check can end the case: a record left behind would make
			// the next case's handler wait, and Post with it.
			var sent seen
			select {
			case sent = <-requests:
			default:
				t.Error("the server recorded no request")
			}
			if err != nil {
				t.Fatalf("Post() error = %v, want nil", err)
			}
			if diff := cmp.Diff(tt.wantBody, string(got)); diff != "" {
				t.Errorf("Post() body mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.want, sent); diff != "" {
				t.Errorf("request mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(before, tt.header); diff != "" {
				t.Errorf("Post changed the caller's header (-before +after):\n%s", diff)
			}
		})
	}
}

// endlessBodyStop is how many bytes an endlessBody gives out before it
// stops a reader that has not stopped by itself.
const endlessBodyStop = 1 << 20

// errEndlessBodyReadOn is what an endlessBody returns to a reader that read
// endlessBodyStop bytes of it.
var errEndlessBodyReadOn = errors.New("made-up error: the endless body was read without a limit")

// endlessBody is a response body that does not end: every Read fills the
// whole buffer. It counts the bytes it gave out and the Close calls. A
// reader that goes on for endlessBodyStop bytes gets an error from then
// on, so that a read without a limit fails its test instead of using up
// the memory.
type endlessBody struct {
	read   atomic.Int64
	closed atomic.Int64
}

// Read fills p and counts its length.
func (b *endlessBody) Read(p []byte) (int, error) {
	if b.read.Load() >= endlessBodyStop {
		return 0, errEndlessBodyReadOn
	}
	for i := range p {
		p[i] = 'a'
	}
	b.read.Add(int64(len(p)))
	return len(p), nil
}

// Close counts the call.
func (b *endlessBody) Close() error {
	b.closed.Add(1)
	return nil
}

// TestBodyCapAtItsBoundary checks the limit of a response body with a small
// limit put in place of the 64 MiB: a body of exactly the limit is read
// whole and one of a byte more is refused for a status of 200 to 299 and
// cut for any other; and of a body that does not end, one byte more than
// the limit is read and no more, and the body is closed once.
func TestBodyCapAtItsBoundary(t *testing.T) {
	if maxBodyBytes != 64<<20 {
		t.Errorf("maxBodyBytes = %d, want 64 MiB", maxBodyBytes)
	}
	if got := New(Config{}).maxBody; got != maxBodyBytes {
		t.Errorf("New(Config{}).maxBody = %d, want maxBodyBytes", got)
	}

	const limit = 16
	atLimit := strings.Repeat("a", limit)
	tests := map[string]struct {
		status   int
		body     string
		wantBody string
		wantErr  error
		// wantStatusBody is the Body of the *llm.StatusError expected when
		// the status is not 200 to 299.
		wantStatusBody string
	}{
		"success: 200, one byte below the limit": {status: http.StatusOK, body: atLimit[1:], wantBody: atLimit[1:]},
		"success: 200, exactly the limit":        {status: http.StatusOK, body: atLimit, wantBody: atLimit},
		"error: 200, one byte past the limit":    {status: http.StatusOK, body: atLimit + "b", wantErr: ErrBodyTooLarge},
		"error: 200, far past the limit":         {status: http.StatusOK, body: strings.Repeat(atLimit, 4096), wantErr: ErrBodyTooLarge},
		"error: 503, exactly the limit":          {status: http.StatusServiceUnavailable, body: atLimit, wantStatusBody: atLimit},
		"error: 503, one byte past the limit":    {status: http.StatusServiceUnavailable, body: atLimit + "b", wantStatusBody: atLimit},
		"error: 503, far past the limit":         {status: http.StatusServiceUnavailable, body: strings.Repeat(atLimit, 4096), wantStatusBody: atLimit},
	}
	srv := newEchoServer(t, nil)
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := newOwned(t, Config{})
			c.maxBody = limit

			got, err := echo(t, c, srv, tt.status, tt.body)
			switch {
			case tt.wantErr != nil:
				if err != tt.wantErr { //nolint:errorlint // Post returns the error value itself.
					t.Fatalf("Post() error = %#v, want %v itself", err, tt.wantErr)
				}
				if got != nil {
					t.Errorf("Post() body = %q beside the error", got)
				}
			case tt.status == http.StatusOK:
				if err != nil {
					t.Fatalf("Post() error = %v, want nil", err)
				}
				if diff := cmp.Diff(tt.wantBody, string(got)); diff != "" {
					t.Errorf("Post() body mismatch (-want +got):\n%s", diff)
				}
			default:
				se, ok := errors.AsType[*llm.StatusError](err)
				if !ok {
					t.Fatalf("Post() error = %#v, want an *llm.StatusError", err)
				}
				if se.StatusCode != tt.status {
					t.Errorf("StatusCode = %d, want %d", se.StatusCode, tt.status)
				}
				if diff := cmp.Diff(tt.wantStatusBody, string(se.Body)); diff != "" {
					t.Errorf("Body mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}

	// A body that does not end, handed over by a transport of the test's
	// own, which counts what Post reads of it and how often Post closes it.
	endless := map[string]struct {
		status int
		// wantErr is the error expected for a status of 200 to 299; any
		// other status gives an *llm.StatusError with the body cut at the
		// limit.
		wantErr error
	}{
		"error: 200, a body that does not end": {status: http.StatusOK, wantErr: ErrBodyTooLarge},
		"error: 299, a body that does not end": {status: 299, wantErr: ErrBodyTooLarge},
		"error: 302, a body that does not end": {status: http.StatusFound},
		"error: 503, a body that does not end": {status: http.StatusServiceUnavailable},
	}
	for name, tt := range endless {
		t.Run(name, func(t *testing.T) {
			body := &endlessBody{}
			c := New(Config{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return respond(req, tt.status, nil, body), nil
			})}})
			c.maxBody = limit

			got, err := c.Post(t.Context(), mustURL(t, "https://provider.example.test/v1/call"), nil, []byte(`{}`))
			if got != nil {
				t.Errorf("Post() body = %d bytes beside the error", len(got))
			}
			if tt.wantErr != nil {
				if err != tt.wantErr { //nolint:errorlint // Post returns the error value itself.
					t.Errorf("Post() error = %#v, want %v itself", err, tt.wantErr)
				}
			} else {
				se, ok := err.(*llm.StatusError) //nolint:errorlint // Post returns the status error itself.
				if !ok {
					t.Fatalf("Post() error = %#v, want an *llm.StatusError itself", err)
				}
				if se.StatusCode != tt.status {
					t.Errorf("StatusCode = %d, want %d", se.StatusCode, tt.status)
				}
				if diff := cmp.Diff(atLimit, string(se.Body)); diff != "" {
					t.Errorf("Body mismatch (-want +got):\n%s", diff)
				}
			}
			if got := body.read.Load(); got != limit+1 {
				t.Errorf("Post read %d bytes of a body that does not end, want %d: the limit and one byte", got, limit+1)
			}
			if got := body.closed.Load(); got != 1 {
				t.Errorf("Post closed the response body %d times, want 1", got)
			}
		})
	}
}

// TestStatusErrorKeepsRetryAfter checks which response headers a status
// error keeps: Retry-After, Retry-After-Ms and X-Typesafe-Request-Id, each
// with all its values, in a header of its own, and no other.
func TestStatusErrorKeepsRetryAfter(t *testing.T) {
	tests := map[string]struct {
		response http.Header
		want     http.Header
	}{
		"success: Retry-After in seconds": {
			response: http.Header{"Retry-After": {"7"}},
			want:     http.Header{"Retry-After": {"7"}},
		},
		"success: Retry-After as a date": {
			response: http.Header{"Retry-After": {"Fri, 02 Oct 2026 12:00:00 GMT"}},
			want:     http.Header{"Retry-After": {"Fri, 02 Oct 2026 12:00:00 GMT"}},
		},
		"success: retry-after-ms beside Retry-After": {
			response: http.Header{"Retry-After-Ms": {"1500"}, "Retry-After": {"2"}},
			want:     http.Header{"Retry-After-Ms": {"1500"}, "Retry-After": {"2"}},
		},
		"success: two request ids": {
			response: http.Header{"X-Typesafe-Request-Id": {"req_made_up_1", "req_made_up_2"}},
			want:     http.Header{"X-Typesafe-Request-Id": {"req_made_up_1", "req_made_up_2"}},
		},
		"success: every other header is dropped": {
			response: http.Header{
				"Retry-After":           {"3"},
				"Set-Cookie":            {"session=made-up"},
				"X-Request-Id":          {"req_made_up"},
				"Request-Id":            {"req_made_up"},
				"X-Ratelimit-Remaining": {"0"},
				"Www-Authenticate":      {"Bearer"},
			},
			want: http.Header{"Retry-After": {"3"}},
		},
		"success: none of the three": {
			response: http.Header{"X-Request-Id": {"req_made_up"}},
			want:     http.Header{},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := newEchoServer(t, tt.response)
			se := statusError(t, newOwned(t, Config{}), srv, http.StatusTooManyRequests, `{"error":{"message":"slow down"}}`)
			if diff := cmp.Diff(tt.want, se.Header); diff != "" {
				t.Errorf("Header mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// The kept header is the status error's own: a later change of the
	// response's header map does not reach it.
	response := http.Header{"Retry-After": {"7"}}
	kept := keepHeaders(response)
	response["Retry-After"][0] = "9"
	response.Set("Retry-After-Ms", "1")
	if diff := cmp.Diff(http.Header{"Retry-After": {"7"}}, kept); diff != "" {
		t.Errorf("keepHeaders shares its values with the response (-want +got):\n%s", diff)
	}
}

// redirectCanary is a made-up word the redirect test sends in the three
// headers a provider's key travels in; it is not shaped like a key.
const redirectCanary = "canary-word-of-the-redirect-test"

// TestRedirectIsNotFollowed checks that a redirect to another host is
// reported as a status error and that the host it names receives no
// request at all, so neither the header that carries a provider's key nor
// the request body reaches it: for a Client that owns its HTTP client and
// for one that borrows a client without a redirect policy, which is called
// through a copy and is itself left as it was. A borrowed client that has
// a policy of its own keeps it, on a copy too: the request is followed,
// and net/http sends X-Api-Key and X-Goog-Api-Key on to the other host and
// removes Authorization, which is why nothing is followed by default.
func TestRedirectIsNotFollowed(t *testing.T) {
	type arrival struct {
		Method        string
		Authorization string
		APIKey        string
		GoogAPIKey    string
		Body          string
	}
	// The handler never blocks: a request that should not have come still
	// gets its answer, so that the test fails instead of waiting.
	var arrivals atomic.Int64
	var last atomic.Pointer[arrival]
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		arrivals.Add(1)
		last.Store(&arrival{
			Method: r.Method, Authorization: r.Header.Get("Authorization"), APIKey: r.Header.Get("X-Api-Key"),
			GoogAPIKey: r.Header.Get("X-Goog-Api-Key"), Body: string(body),
		})
		_, _ = io.WriteString(w, `{"from":"elsewhere"}`)
	}))
	t.Cleanup(elsewhere.Close)
	// The same server under another host name: net/http compares host
	// names, not ports, when it decides which headers to send on.
	if !strings.HasPrefix(elsewhere.URL, "http://127.0.0.1:") {
		t.Fatalf("the test server listens on %s, want 127.0.0.1", elsewhere.URL)
	}
	location := strings.Replace(elsewhere.URL, "127.0.0.1", "localhost", 1) + "/elsewhere"

	header := http.Header{
		"Authorization":  {"Bearer " + redirectCanary},
		"X-Api-Key":      {redirectCanary},
		"X-Goog-Api-Key": {redirectCanary},
	}
	const requestBody = `{"model":"made-up","input":"made-up state"}`

	statuses := map[string]struct {
		status int
		// wantMethod and wantBody are what the other host receives when a
		// client's own policy follows the redirect.
		wantMethod string
		wantBody   string
	}{
		"301": {status: http.StatusMovedPermanently, wantMethod: http.MethodGet},
		"302": {status: http.StatusFound, wantMethod: http.MethodGet},
		"303": {status: http.StatusSeeOther, wantMethod: http.MethodGet},
		"307": {status: http.StatusTemporaryRedirect, wantMethod: http.MethodPost, wantBody: requestBody},
		"308": {status: http.StatusPermanentRedirect, wantMethod: http.MethodPost, wantBody: requestBody},
	}
	for name, tt := range statuses {
		t.Run(name, func(t *testing.T) {
			var hits atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				http.Redirect(w, r, location, tt.status)
			}))
			t.Cleanup(srv.Close)
			target := mustURL(t, srv.URL+"/v1/call")

			// notFollowed posts through c and checks that the redirect came
			// back as a status error and reached nobody else.
			notFollowed := func(t *testing.T, kind string, c *Client) {
				t.Helper()
				hits.Store(0)
				arrivals.Store(0)
				last.Store(nil)
				_, err := c.Post(t.Context(), target, header, []byte(requestBody))
				se, ok := err.(*llm.StatusError) //nolint:errorlint // Post returns the status error itself.
				if !ok {
					t.Fatalf("%s: Post() error = %#v, want an *llm.StatusError", kind, err)
				}
				if se.StatusCode != tt.status {
					t.Errorf("%s: StatusCode = %d, want %d", kind, se.StatusCode, tt.status)
				}
				if len(se.Header) != 0 {
					t.Errorf("%s: Header = %v, want none: Location is not kept", kind, se.Header)
				}
				if got := hits.Load(); got != 1 {
					t.Errorf("%s: the server saw %d requests, want 1", kind, got)
				}
				if got := arrivals.Load(); got != 0 {
					t.Errorf("%s: the redirect's host saw %d requests, want 0; the last one: %+v", kind, got, last.Load())
				}
			}

			notFollowed(t, "owned client", newOwned(t, Config{}))

			// A borrowed client without a policy: the transport is the
			// caller's, the policy is not written into the caller's value.
			transport := &http.Transport{}
			t.Cleanup(transport.CloseIdleConnections)
			plain := &http.Client{Transport: transport, Timeout: time.Minute}
			notFollowed(t, "borrowed client without a policy", New(Config{HTTPClient: plain}))
			if plain.CheckRedirect != nil {
				t.Error("borrowed client without a policy: New or Post set the caller's CheckRedirect")
			}
			if plain.Transport != http.RoundTripper(transport) || plain.Timeout != time.Minute || plain.Jar != nil {
				t.Errorf("borrowed client without a policy: the caller's client was changed: %+v", plain)
			}

			// A borrowed client with a policy of its own, here one that
			// follows: the caller decides, and the key headers travel.
			var asked atomic.Int64
			own := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
				asked.Add(1)
				return nil
			}}
			hits.Store(0)
			arrivals.Store(0)
			last.Store(nil)
			got, err := New(Config{HTTPClient: own}).Post(t.Context(), target, header, []byte(requestBody))
			if err != nil {
				t.Fatalf("borrowed client with a policy: Post() error = %v, want nil", err)
			}
			if diff := cmp.Diff(`{"from":"elsewhere"}`, string(got)); diff != "" {
				t.Errorf("borrowed client with a policy: body mismatch (-want +got):\n%s", diff)
			}
			if asked.Load() != 1 || arrivals.Load() != 1 {
				t.Fatalf("borrowed client with a policy: its CheckRedirect was asked %d times and the redirect's host saw %d requests, want 1 and 1", asked.Load(), arrivals.Load())
			}
			want := &arrival{Method: tt.wantMethod, APIKey: redirectCanary, GoogAPIKey: redirectCanary, Body: tt.wantBody}
			if diff := cmp.Diff(want, last.Load()); diff != "" {
				t.Errorf("borrowed client with a policy: what the redirect's host received differs from the test's premise, that net/http sends the two key headers on and removes Authorization (-want +got):\n%s", diff)
			}
			if own.CheckRedirect == nil || own.Transport != http.RoundTripper(transport) || own.Timeout != 0 || own.Jar != nil {
				t.Errorf("borrowed client with a policy: the caller's client was changed: %+v", own)
			}
		})
	}
}

// closeCounter is a transport that counts CloseIdleConnections calls.
type closeCounter struct {
	http.RoundTripper
	closed atomic.Int64
}

// CloseIdleConnections counts the call.
func (c *closeCounter) CloseIdleConnections() { c.closed.Add(1) }

// TestNewAndClose checks what New builds from a Config and what Close does:
// an owned client over a clone of http.DefaultTransport that follows no
// redirect and whose idle connections Close closes, a borrowed client that
// is left alone and called through a copy, which keeps the caller's
// redirect policy or, when the caller has none, follows no redirect, and
// the timeout's default.
func TestNewAndClose(t *testing.T) {
	if DefaultTimeout != 600*time.Second {
		t.Errorf("DefaultTimeout = %v, want 600s", DefaultTimeout)
	}

	t.Run("success: owned client", func(t *testing.T) {
		c := New(Config{})
		if !c.owned {
			t.Fatal("owned = false for a Config without an HTTPClient")
		}
		if c.timeout != DefaultTimeout {
			t.Errorf("timeout = %v, want DefaultTimeout", c.timeout)
		}
		tr, ok := c.http.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("Transport = %T, want an *http.Transport", c.http.Transport)
		}
		def, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			t.Fatalf("http.DefaultTransport = %T, want an *http.Transport", http.DefaultTransport)
		}
		if tr == def {
			t.Error("the owned client uses http.DefaultTransport itself, want a clone")
		}
		if tr.Proxy == nil || !tr.ForceAttemptHTTP2 {
			t.Errorf("the clone lost the default's settings: Proxy nil = %t, ForceAttemptHTTP2 = %t", tr.Proxy == nil, tr.ForceAttemptHTTP2)
		}
		if c.http.Timeout != 0 {
			t.Errorf("http.Client.Timeout = %v, want 0: the request's context carries the timeout", c.http.Timeout)
		}
		if c.http.CheckRedirect == nil {
			t.Fatal("CheckRedirect = nil, want the policy that follows no redirect")
		}
		if err := c.http.CheckRedirect(nil, nil); err != http.ErrUseLastResponse { //nolint:errorlint // the policy returns the value net/http compares by identity.
			t.Errorf("CheckRedirect() = %v, want http.ErrUseLastResponse", err)
		}

		counter := &closeCounter{RoundTripper: tr}
		c.http.Transport = counter
		if err := c.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
		if err := c.Close(); err != nil {
			t.Errorf("second Close() = %v, want nil", err)
		}
		if got := counter.closed.Load(); got != 2 {
			t.Errorf("CloseIdleConnections called %d times by two Close calls, want 2", got)
		}
	})

	t.Run("success: borrowed client with a redirect policy", func(t *testing.T) {
		counter := &closeCounter{RoundTripper: http.DefaultTransport}
		var asked atomic.Int64
		errPolicy := errors.New("made-up answer of the caller's redirect policy")
		policy := func(*http.Request, []*http.Request) error {
			asked.Add(1)
			return errPolicy
		}
		jar := http.CookieJar(nil)
		hc := &http.Client{Transport: counter, Timeout: 3 * time.Second, CheckRedirect: policy, Jar: jar}
		c := New(Config{HTTPClient: hc, Timeout: 5 * time.Second, BlankErrorBodyIsNone: true})
		if c.owned {
			t.Error("owned = true for a Config with an HTTPClient")
		}
		if c.http == hc {
			t.Fatal("the Client calls through the caller's client itself, want a copy")
		}
		if c.http.CheckRedirect == nil {
			t.Fatal("the copy has no CheckRedirect, want the caller's policy")
		}
		if err := c.http.CheckRedirect(nil, nil); err != errPolicy || asked.Load() != 1 { //nolint:errorlint // the caller's policy returns this value itself.
			t.Errorf("the copy's CheckRedirect() = %v after %d calls of the caller's policy, want the caller's policy called once", err, asked.Load())
		}
		if c.http.Transport != http.RoundTripper(counter) || c.http.Timeout != 3*time.Second || c.http.Jar != jar {
			t.Errorf("the copy does not share the caller's Transport, Timeout and Jar: %+v", c.http)
		}
		if hc.CheckRedirect == nil || hc.Timeout != 3*time.Second || hc.Transport != http.RoundTripper(counter) || hc.Jar != jar {
			t.Errorf("New changed the borrowed client: %+v", hc)
		}
		if c.timeout != 5*time.Second || !c.blankIsNone {
			t.Errorf("timeout = %v, blankIsNone = %t; want 5s, true", c.timeout, c.blankIsNone)
		}

		// The settings were read when New was called: what the caller
		// changes afterwards does not reach the Client.
		hc.CheckRedirect, hc.Timeout, hc.Transport = nil, 9*time.Second, nil
		if err := c.http.CheckRedirect(nil, nil); err != errPolicy || asked.Load() != 2 { //nolint:errorlint // the caller's policy returns this value itself.
			t.Errorf("after the caller's change: the copy's CheckRedirect() = %v after %d calls of the caller's first policy, want that policy called twice", err, asked.Load())
		}
		if c.http.Transport != http.RoundTripper(counter) || c.http.Timeout != 3*time.Second {
			t.Errorf("after the caller's change: the copy has Transport %v and Timeout %v, want the settings New read", c.http.Transport, c.http.Timeout)
		}
		if err := c.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
		if got := counter.closed.Load(); got != 0 {
			t.Errorf("Close closed the idle connections of a borrowed client %d times, want 0", got)
		}
	})

	t.Run("success: borrowed client without a redirect policy", func(t *testing.T) {
		counter := &closeCounter{RoundTripper: http.DefaultTransport}
		jar := http.CookieJar(nil)
		hc := &http.Client{Transport: counter, Timeout: 3 * time.Second, Jar: jar}
		c := New(Config{HTTPClient: hc})
		if c.owned {
			t.Error("owned = true for a Config with an HTTPClient")
		}
		if c.http == hc {
			t.Fatal("the Client calls through the caller's client itself, want a copy that follows no redirect")
		}
		if c.http.CheckRedirect == nil {
			t.Fatal("the copy has no CheckRedirect, want the policy that follows no redirect")
		}
		if err := c.http.CheckRedirect(nil, nil); err != http.ErrUseLastResponse { //nolint:errorlint // the policy returns the value net/http compares by identity.
			t.Errorf("the copy's CheckRedirect() = %v, want http.ErrUseLastResponse", err)
		}
		if c.http.Transport != http.RoundTripper(counter) || c.http.Timeout != 3*time.Second || c.http.Jar != jar {
			t.Errorf("the copy does not share the caller's Transport, Timeout and Jar: %+v", c.http)
		}
		if hc.CheckRedirect != nil || hc.Timeout != 3*time.Second || hc.Transport != http.RoundTripper(counter) {
			t.Errorf("New changed the borrowed client: %+v", hc)
		}
		if err := c.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
		if got := counter.closed.Load(); got != 0 {
			t.Errorf("Close closed the idle connections of a borrowed client %d times, want 0", got)
		}
	})

	t.Run("success: a default transport of another kind", func(t *testing.T) {
		def := http.DefaultTransport
		t.Cleanup(func() { http.DefaultTransport = def })
		http.DefaultTransport = roundTripFunc(block)

		c := New(Config{})
		tr, ok := c.http.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("Transport = %T, want an *http.Transport", c.http.Transport)
		}
		if tr.Proxy == nil || !tr.ForceAttemptHTTP2 {
			t.Errorf("Proxy nil = %t, ForceAttemptHTTP2 = %t; want the environment's proxy and HTTP/2", tr.Proxy == nil, tr.ForceAttemptHTTP2)
		}
	})
}

// TestParseURL checks which base URLs ParseURL accepts and that its error
// is ErrURL itself, with no part of the input.
func TestParseURL(t *testing.T) {
	tests := map[string]struct {
		raw     string
		want    string
		wantErr bool
	}{
		"success: https with a path":            {raw: "https://provider.example.test/v1", want: "https://provider.example.test/v1"},
		"success: http, a port, no path":        {raw: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		"success: trailing slash":               {raw: "https://provider.example.test/", want: "https://provider.example.test/"},
		"success: userinfo and query are kept":  {raw: "https://" + userinfo + "provider.example.test/v1?alt=json", want: "https://" + userinfo + "provider.example.test/v1?alt=json"},
		"error: empty":                          {raw: "", wantErr: true},
		"error: no scheme":                      {raw: "provider.example.test/v1", wantErr: true},
		"error: scheme-relative":                {raw: "//provider.example.test/v1", wantErr: true},
		"error: another scheme":                 {raw: "ftp://provider.example.test/v1", wantErr: true},
		"error: no host":                        {raw: "https:///v1", wantErr: true},
		"error: opaque":                         {raw: "https:provider.example.test", wantErr: true},
		"error: a port that is no number":       {raw: "https://" + userinfo + "provider.example.test:port/v1?key=made-up-word", wantErr: true},
		"error: a control character":            {raw: "https://provider.example.test/v1?key=made-up-word\x7f", wantErr: true},
		"error: a bad escape in the host":       {raw: "https://made-up-word%zz.example.test/", wantErr: true},
		"error: a path only":                    {raw: "/v1/responses", wantErr: true},
		"success: upper-case scheme is lowered": {raw: "HTTPS://provider.example.test/v1", want: "https://provider.example.test/v1"},
		"error: space in the host":              {raw: "https://provider example.test/v1", wantErr: true},
		"success: IPv6 literal with a port":     {raw: "http://[::1]:8080/v1", want: "http://[::1]:8080/v1"},
		"success: query without a path is kept": {raw: "https://provider.example.test?alt=json", want: "https://provider.example.test?alt=json"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			u, err := ParseURL(tt.raw)
			if tt.wantErr {
				if err != ErrURL { //nolint:errorlint // ParseURL returns the error value itself, so no input text can ride on it.
					t.Fatalf("ParseURL(%q) error = %#v, want ErrURL itself", tt.raw, err)
				}
				if u != nil {
					t.Errorf("ParseURL(%q) = %v beside the error", tt.raw, u)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseURL(%q) error = %v, want nil", tt.raw, err)
			}
			if diff := cmp.Diff(tt.want, u.String()); diff != "" {
				t.Errorf("ParseURL(%q) mismatch (-want +got):\n%s", tt.raw, diff)
			}
		})
	}
}

// TestRedact checks what an error text shows of a URL: scheme, host and
// path, and nothing of userinfo, query or fragment.
func TestRedact(t *testing.T) {
	tests := map[string]struct {
		raw  string
		want string
	}{
		"success: nothing to remove":         {raw: "https://provider.example.test/v1/call", want: "https://provider.example.test/v1/call"},
		"success: userinfo":                  {raw: "https://" + userinfo + "provider.example.test/v1/call", want: "https://provider.example.test/v1/call"},
		"success: a user without a password": {raw: "https://made-up-word@provider.example.test/v1/call", want: "https://provider.example.test/v1/call"},
		"success: query":                     {raw: "https://provider.example.test/v1/call?key=made-up-word&alt=json", want: "https://provider.example.test/v1/call"},
		"success: fragment":                  {raw: "https://provider.example.test/v1/call#made-up-word", want: "https://provider.example.test/v1/call"},
		"success: all three and a port":      {raw: "http://" + userinfo + "127.0.0.1:8080/v1?key=made-up-word#f", want: "http://127.0.0.1:8080/v1"},
		"success: no path":                   {raw: "https://" + userinfo + "provider.example.test?key=made-up-word", want: "https://provider.example.test"},
		"success: a path that needs escapes": {raw: "https://provider.example.test/v1/a%20b?key=made-up-word", want: "https://provider.example.test/v1/a%20b"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, redact(mustURL(t, tt.raw))); diff != "" {
				t.Errorf("redact(%q) mismatch (-want +got):\n%s", tt.raw, diff)
			}
		})
	}
}

// TestRequestThatCannotBeBuilt checks that a URL value net/http refuses is
// reported with a fixed text that shows no userinfo and no query, as an
// error that is none of package llm's types, and that no request is sent.
func TestRequestThatCannotBeBuilt(t *testing.T) {
	var sent atomic.Int64
	c := New(Config{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sent.Add(1)
		return respond(req, http.StatusOK, nil, strings.NewReader(`{}`)), nil
	})}})
	u := &url.URL{Scheme: "https", User: url.UserPassword("made-up-user", "made-up-word"), Host: "provider.example.test:not a port", Path: "/v1/call", RawQuery: "key=made-up-word"}

	body, err := c.Post(t.Context(), u, nil, []byte(`{}`))
	if body != nil {
		t.Errorf("Post() body = %q, want none", body)
	}
	te, ok := err.(*transportError) //nolint:errorlint // Post returns the package's own error itself.
	if !ok {
		t.Fatalf("Post() error = %#v, want a *transportError itself", err)
	}
	if diff := cmp.Diff("rest: POST https://provider.example.test:not%20a%20port/v1/call: "+phraseBuild, te.Error()); diff != "" {
		t.Errorf("error text mismatch (-want +got):\n%s", diff)
	}
	if te.cause != nil || errors.Unwrap(err) != nil {
		t.Errorf("the error wraps %#v, want nothing", errors.Unwrap(err))
	}
	if got := sent.Load(); got != 0 {
		t.Errorf("%d requests were sent, want 0", got)
	}
}

// TestEnv checks the environment lookup with made-up names and values: a
// variable that is set gives its value, one that is set and empty gives ""
// and true, and one that is not set, or an empty name, gives "" and false.
func TestEnv(t *testing.T) {
	const (
		setName   = "REST_TEST_MADE_UP_VARIABLE"
		emptyName = "REST_TEST_MADE_UP_EMPTY_VARIABLE"
		unsetName = "REST_TEST_MADE_UP_UNSET_VARIABLE"
	)
	t.Setenv(setName, "made-up words")
	t.Setenv(emptyName, "")
	// Setenv first, so that the test restores whatever the variable was.
	t.Setenv(unsetName, "made-up")
	if err := os.Unsetenv(unsetName); err != nil {
		t.Fatalf("os.Unsetenv(%s): %v", unsetName, err)
	}

	tests := map[string]struct {
		name    string
		want    string
		wantSet bool
	}{
		"success: set":           {name: setName, want: "made-up words", wantSet: true},
		"success: set and empty": {name: emptyName, want: "", wantSet: true},
		"success: not set":       {name: unsetName, want: "", wantSet: false},
		"success: empty name":    {name: "", want: "", wantSet: false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, set := Env(tt.name)
			if got != tt.want || set != tt.wantSet {
				t.Errorf("Env(%q) = %q, %t; want %q, %t", tt.name, got, set, tt.want, tt.wantSet)
			}
		})
	}
}
