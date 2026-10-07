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

package gemini

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// recordedRequest is what a transport received of one request.
type recordedRequest struct {
	method string
	url    string
	header http.Header
	body   []byte
	// traced is the request body the transport's trace held when the
	// request reached the transport, and traceAPI its API name; nil and ""
	// when it held none or the transport has no trace.
	traced   []byte
	traceAPI string
}

// transport is an http.RoundTripper of the test's own: it records each
// request and answers with respond, or when respond is nil with status 200
// and the body reply. When trace is set it also records what trace held at
// the moment each request arrived. No request leaves the process.
type transport struct {
	mu       sync.Mutex
	requests []recordedRequest
	reply    string
	respond  func(*http.Request) (*http.Response, error)
	trace    *llm.Trace
}

func (tr *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	rec := recordedRequest{method: req.Method, url: req.URL.String(), header: req.Header.Clone(), body: body}
	if tr.trace != nil {
		rec.traced, _ = tr.trace.Request()
		rec.traceAPI = tr.trace.API()
	}
	tr.mu.Lock()
	tr.requests = append(tr.requests, rec)
	tr.mu.Unlock()
	if tr.respond == nil {
		return response(req, http.StatusOK, http.Header{"Content-Type": {"application/json"}}, tr.reply), nil
	}
	return tr.respond(req)
}

// recorded returns a copy of the requests received.
func (tr *transport) recorded() []recordedRequest {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]recordedRequest(nil), tr.requests...)
}

// response returns a response to req with status, header and body.
func response(req *http.Request, status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// envNames are the variables New reads.
var envNames = [...]string{"GOOGLE_API_KEY", "GEMINI_API_KEY", "GOOGLE_GEMINI_BASE_URL"}

// clearEnv unsets the variables New reads for the rest of t, restoring
// them when t ends.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range envNames {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("Unsetenv(%s): %v", name, err)
		}
	}
}

// newProvider returns New("gemini-3.8-flash", opts...), closed when t ends.
func newProvider(t *testing.T, opts ...Option) *Provider {
	t.Helper()
	p, err := New("gemini-3.8-flash", opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// prompted is a prompted request of testMessages.
func prompted() *llm.Request {
	return &llm.Request{Messages: testMessages, Schema: []byte(testSchema)}
}

// TestBaseURLFromEnvironment checks where a request goes: to
// GOOGLE_GEMINI_BASE_URL with v1beta/interactions appended after one slash
// when the variable is set and not empty, as google-genai 2.24.0 honours
// it, and to https://generativelanguage.googleapis.com/ otherwise;
// WithBaseURL wins over the variable unless it is empty. The base URL's
// query is kept and its fragment dropped.
func TestBaseURLFromEnvironment(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		opts []Option
		want string
	}{
		"set":                     {env: map[string]string{"GOOGLE_GEMINI_BASE_URL": "http://example.test/base"}, want: "http://example.test/base/v1beta/interactions"},
		"set with a final slash":  {env: map[string]string{"GOOGLE_GEMINI_BASE_URL": "http://example.test/base/"}, want: "http://example.test/base/v1beta/interactions"},
		"set to a host":           {env: map[string]string{"GOOGLE_GEMINI_BASE_URL": "http://example.test"}, want: "http://example.test/v1beta/interactions"},
		"set with a query":        {env: map[string]string{"GOOGLE_GEMINI_BASE_URL": "http://example.test/base?x=madeupword"}, want: "http://example.test/base/v1beta/interactions?x=madeupword"},
		"set with a fragment":     {env: map[string]string{"GOOGLE_GEMINI_BASE_URL": "http://example.test/base#frag"}, want: "http://example.test/base/v1beta/interactions"},
		"set with an escape":      {env: map[string]string{"GOOGLE_GEMINI_BASE_URL": "http://example.test/a%2Fb/"}, want: "http://example.test/a%2Fb/v1beta/interactions"},
		"set with final slashes":  {env: map[string]string{"GOOGLE_GEMINI_BASE_URL": "http://example.test/base///"}, want: "http://example.test/base/v1beta/interactions"},
		"set to a host and //":    {env: map[string]string{"GOOGLE_GEMINI_BASE_URL": "http://example.test//"}, want: "http://example.test/v1beta/interactions"},
		"unset":                   {want: "https://generativelanguage.googleapis.com/v1beta/interactions"},
		"empty":                   {env: map[string]string{"GOOGLE_GEMINI_BASE_URL": ""}, want: "https://generativelanguage.googleapis.com/v1beta/interactions"},
		"WithBaseURL wins":        {env: map[string]string{"GOOGLE_GEMINI_BASE_URL": "http://env.test"}, opts: []Option{WithBaseURL("http://option.test/p")}, want: "http://option.test/p/v1beta/interactions"},
		"an empty WithBaseURL":    {env: map[string]string{"GOOGLE_GEMINI_BASE_URL": "http://env.test"}, opts: []Option{WithBaseURL("")}, want: "http://env.test/v1beta/interactions"},
		"WithBaseURL without env": {opts: []Option{WithBaseURL("https://proxy.test:8443/")}, want: "https://proxy.test:8443/v1beta/interactions"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			tr := &transport{reply: completedBody}
			p := newProvider(t, append([]Option{WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: tr})}, tt.opts...)...)
			if _, err := p.Do(t.Context(), prompted()); err != nil {
				t.Fatalf("Do: %v", err)
			}
			if reqs := tr.recorded(); len(reqs) != 1 || reqs[0].url != tt.want {
				t.Errorf("requests = %+v, want one to %s", reqs, tt.want)
			}
		})
	}
}

// TestKeyFromEnvironment checks which key is sent, as google-genai 2.24.0
// chooses it when upstream builds its client without a key (measured on
// the reference Python): GOOGLE_API_KEY, else GEMINI_API_KEY, each with the
// white space around it removed, and a variable that is empty or only white
// space counting as not set; WithAPIKey wins unless it is empty or only white
// space; with no key New fails.
func TestKeyFromEnvironment(t *testing.T) {
	tests := map[string]struct {
		env     map[string]string
		opts    []Option
		want    string
		wantErr bool
	}{
		"GOOGLE_API_KEY alone":                  {env: map[string]string{"GOOGLE_API_KEY": "googleword"}, want: "googleword"},
		"GEMINI_API_KEY alone":                  {env: map[string]string{"GEMINI_API_KEY": "geminiword"}, want: "geminiword"},
		"both: GOOGLE_API_KEY takes precedence": {env: map[string]string{"GOOGLE_API_KEY": "googleword", "GEMINI_API_KEY": "geminiword"}, want: "googleword"},
		"GOOGLE_API_KEY empty":                  {env: map[string]string{"GOOGLE_API_KEY": "", "GEMINI_API_KEY": "geminiword"}, want: "geminiword"},
		"GEMINI_API_KEY empty":                  {env: map[string]string{"GOOGLE_API_KEY": "googleword", "GEMINI_API_KEY": ""}, want: "googleword"},
		"both empty":                            {env: map[string]string{"GOOGLE_API_KEY": "", "GEMINI_API_KEY": ""}, wantErr: true},
		"neither":                               {wantErr: true},
		"WithAPIKey wins":                       {env: map[string]string{"GOOGLE_API_KEY": "googleword"}, opts: []Option{WithAPIKey("optionword")}, want: "optionword"},
		"an empty WithAPIKey":                   {env: map[string]string{"GEMINI_API_KEY": "geminiword"}, opts: []Option{WithAPIKey("")}, want: "geminiword"},
		"an empty WithAPIKey and no variable":   {opts: []Option{WithAPIKey("")}, wantErr: true},
		"GOOGLE_API_KEY only white space":       {env: map[string]string{"GOOGLE_API_KEY": "   ", "GEMINI_API_KEY": "geminiword"}, want: "geminiword"},
		"white space around the key":            {env: map[string]string{"GOOGLE_API_KEY": "  googleword\t"}, want: "googleword"},
		"both only white space":                 {env: map[string]string{"GOOGLE_API_KEY": " ", "GEMINI_API_KEY": "\t\n"}, wantErr: true},
		"a WithAPIKey of white space":           {env: map[string]string{"GEMINI_API_KEY": "geminiword"}, opts: []Option{WithAPIKey("  ")}, want: "geminiword"},
		"white space around WithAPIKey":         {opts: []Option{WithAPIKey(" optionword ")}, want: "optionword"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			tr := &transport{reply: completedBody}
			p, err := New("gemini-3.8-flash", append([]Option{WithHTTPClient(&http.Client{Transport: tr})}, tt.opts...)...)
			if tt.wantErr {
				if err == nil || err.Error() != "gemini: no API key: set GOOGLE_API_KEY or GEMINI_API_KEY, or pass WithAPIKey" || p != nil {
					t.Fatalf("New = %T (nil %t), %v; want the missing-key error", p, p == nil, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := p.Do(t.Context(), prompted()); err != nil {
				t.Fatalf("Do: %v", err)
			}
			if reqs := tr.recorded(); len(reqs) != 1 || reqs[0].header.Get("X-Goog-Api-Key") != tt.want {
				t.Errorf("requests = %+v, want one with x-goog-api-key %q", reqs, tt.want)
			}
		})
	}
}

// TestNewReadsTheEnvironmentOnce checks that New reads the variables when
// the Provider is built and Do reads them no more: a change after New does
// not reach the request.
func TestNewReadsTheEnvironmentOnce(t *testing.T) {
	clearEnv(t)
	t.Setenv("GEMINI_API_KEY", "firstword")
	t.Setenv("GOOGLE_GEMINI_BASE_URL", "http://first.test")
	tr := &transport{reply: completedBody}
	p := newProvider(t, WithHTTPClient(&http.Client{Transport: tr}))
	t.Setenv("GEMINI_API_KEY", "secondword")
	t.Setenv("GOOGLE_API_KEY", "thirdword")
	t.Setenv("GOOGLE_GEMINI_BASE_URL", "http://second.test")
	if _, err := p.Do(t.Context(), prompted()); err != nil {
		t.Fatalf("Do: %v", err)
	}
	reqs := tr.recorded()
	if len(reqs) != 1 || reqs[0].url != "http://first.test/v1beta/interactions" || reqs[0].header.Get("X-Goog-Api-Key") != "firstword" {
		t.Errorf("requests = %+v, want one to http://first.test with the key firstword", reqs)
	}
}

// TestNewRefuses checks New's refusals: each has a fixed text naming the
// option or the variable, and none prints a value.
func TestNewRefuses(t *testing.T) {
	const baseRefusal = " is not an absolute http or https URL with a host"
	tests := map[string]struct {
		env  map[string]string
		opts []Option
		want string
	}{
		"a nil client":                    {opts: []Option{WithHTTPClient(nil)}, want: "gemini: WithHTTPClient: the client is nil"},
		"a zero timeout":                  {opts: []Option{WithTimeout(0)}, want: "gemini: WithTimeout: the timeout is not positive"},
		"a negative timeout":              {opts: []Option{WithTimeout(-time.Second)}, want: "gemini: WithTimeout: the timeout is not positive"},
		"no key":                          {opts: []Option{WithBaseURL("http://example.test")}, want: "gemini: no API key: set GOOGLE_API_KEY or GEMINI_API_KEY, or pass WithAPIKey"},
		"a relative WithBaseURL":          {opts: []Option{WithAPIKey("not-a-key"), WithBaseURL("/relative?key=madeupword")}, want: "gemini: the base URL of WithBaseURL" + baseRefusal},
		"an ftp variable":                 {env: map[string]string{"GOOGLE_GEMINI_BASE_URL": "ftp://madeupword@example.test/"}, opts: []Option{WithAPIKey("not-a-key")}, want: "gemini: the base URL of GOOGLE_GEMINI_BASE_URL" + baseRefusal},
		"a URL without a host":            {opts: []Option{WithAPIKey("not-a-key"), WithBaseURL("https:///madeupword")}, want: "gemini: the base URL of WithBaseURL" + baseRefusal},
		"a URL that does not parse":       {opts: []Option{WithAPIKey("not-a-key"), WithBaseURL("http://example.test/%zz?madeupword")}, want: "gemini: the base URL of WithBaseURL" + baseRefusal},
		"the client is checked first":     {opts: []Option{WithHTTPClient(nil), WithTimeout(0)}, want: "gemini: WithHTTPClient: the client is nil"},
		"the options before the key":      {opts: []Option{WithTimeout(0)}, want: "gemini: WithTimeout: the timeout is not positive"},
		"the key before the base URL":     {opts: []Option{WithBaseURL("/relative")}, want: "gemini: no API key: set GOOGLE_API_KEY or GEMINI_API_KEY, or pass WithAPIKey"},
		"a later WithHTTPClient replaces": {opts: []Option{WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{}), WithHTTPClient(nil)}, want: "gemini: WithHTTPClient: the client is nil"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			p, err := New("gemini-3.8-flash", tt.opts...)
			if err == nil || p != nil {
				t.Fatalf("New = %T (nil %t), %v; want %q", p, p == nil, err, tt.want)
			}
			if err.Error() != tt.want {
				t.Errorf("New error = %q, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "madeupword") {
				t.Errorf("New error %q prints a value it was given", err)
			}
		})
	}
	t.Run("a positive timeout and a client are accepted", func(t *testing.T) {
		clearEnv(t)
		if _, err := New("m", WithAPIKey("not-a-key"), WithTimeout(time.Nanosecond), WithHTTPClient(&http.Client{})); err != nil {
			t.Fatalf("New: %v", err)
		}
	})
}

// TestRequestHeaders checks the whole header set a request carries at the
// transport: Content-Type: application/json and the key in x-goog-api-key,
// and nothing else of the provider's own. A base URL's userinfo is sent as
// it is, which net/http turns into Authorization: Basic beside the key.
func TestRequestHeaders(t *testing.T) {
	tests := map[string]struct {
		base string
		want http.Header
	}{
		"the default base URL": {
			want: http.Header{"Content-Type": {"application/json"}, "X-Goog-Api-Key": {"not-a-key"}},
		},
		"a base URL with userinfo": { //nolint:gosec // G101: a made-up word in a test URL, not a credential.
			base: "http://user:madeupword@example.test/base",
			want: http.Header{
				"Authorization":  {"Basic " + base64.StdEncoding.EncodeToString([]byte("user:madeupword"))},
				"Content-Type":   {"application/json"},
				"X-Goog-Api-Key": {"not-a-key"},
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			tr := &transport{reply: completedBody}
			p := newProvider(t, WithAPIKey("not-a-key"), WithBaseURL(tt.base), WithHTTPClient(&http.Client{Transport: tr}))
			if _, err := p.Do(t.Context(), prompted()); err != nil {
				t.Fatalf("Do: %v", err)
			}
			reqs := tr.recorded()
			if len(reqs) != 1 {
				t.Fatalf("requests = %d, want 1", len(reqs))
			}
			if diff := gocmp.Diff(tt.want, reqs[0].header); diff != "" {
				t.Errorf("request header (-want +got):\n%s", diff)
			}
		})
	}
}

// canary returns a key no test text holds: "ts-canary-" and 16 random
// bytes in hex.
func canary(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return "ts-canary-" + hex.EncodeToString(b)
}

// TestKeyTravelsOnlyInItsHeader checks that the key reaches the transport
// in x-goog-api-key and nowhere else: not in the URL's path, query or
// userinfo, another header, the body, the Trace, the text of any error Do
// returns under any fmt verb, or the model name; also with a base URL that
// carries its own userinfo and query.
func TestKeyTravelsOnlyInItsHeader(t *testing.T) {
	outcomes := map[string]struct {
		reply   string
		respond func(*http.Request) (*http.Response, error)
	}{
		"an answer":       {reply: completedBody},
		"a non-answer":    {reply: `{"status":"failed","errors":[{"code":"c","message":"m"}]}`},
		"a body not JSON": {reply: "not json"},
		"a status": {respond: func(req *http.Request) (*http.Response, error) {
			return response(req, http.StatusInternalServerError, nil, `{"error":{"message":"down"}}`), nil
		}},
		"a connection error": {respond: func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") }},
	}
	for name, outcome := range outcomes {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			key := canary(t)
			tr := &transport{reply: outcome.reply, respond: outcome.respond}
			p := newProvider(t, WithAPIKey(key), WithBaseURL("http://user:madeupword@example.test/base?x=madeupword"), WithHTTPClient(&http.Client{Transport: tr}))
			trace := new(llm.Trace)
			req := prompted()
			req.Trace = trace
			_, err := p.Do(t.Context(), req)
			reqs := tr.recorded()
			if len(reqs) != 1 {
				t.Fatalf("requests = %d, want 1", len(reqs))
			}
			r := reqs[0]
			if got := r.header.Get("X-Goog-Api-Key"); got != key {
				t.Errorf("x-goog-api-key = %q, want the key", got)
			}
			if strings.Contains(r.url, key) {
				t.Errorf("URL %s holds the key", r.url)
			}
			if !strings.Contains(r.url, "x=madeupword") || !strings.Contains(r.url, "user:madeupword@") {
				t.Errorf("URL %s lost the base URL's query or userinfo", r.url)
			}
			for name, values := range r.header {
				if name == "X-Goog-Api-Key" {
					continue
				}
				for _, v := range values {
					if strings.Contains(v, key) {
						t.Errorf("header %s holds the key", name)
					}
				}
			}
			if strings.Contains(string(r.body), key) {
				t.Errorf("body holds the key")
			}
			recReq, _ := trace.Request()
			recResp, _ := trace.Response()
			if strings.Contains(string(recReq), key) || strings.Contains(string(recResp), key) {
				t.Errorf("the Trace holds the key")
			}
			if err != nil {
				for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
					if strings.Contains(fmt.Sprintf(verb, err), key) {
						t.Errorf("error under %s holds the key", verb)
					}
				}
			}
			if strings.Contains(p.Model(), key) {
				t.Errorf("Model() holds the key")
			}
		})
	}
}

// TestProviderErrorClassification checks the error Do returns for each
// failure the Adapter classifies, as upstream's map_provider_error maps the
// Gemini SDK's exceptions (_utils/error_handling.py:63-72): a status
// outside 200 to 299 is an *llm.StatusError that keeps the body, nil for a
// blank one as the Gemini SDK hands over no body, and only Retry-After,
// Retry-After-Ms and X-Typesafe-Request-Id of the headers; a redirect is
// not followed; a request without a response is an *llm.ConnectionError; a
// cancelled context's error is returned itself; and a 2xx body that is not
// JSON is none of package llm's types.
func TestProviderErrorClassification(t *testing.T) {
	statusTests := map[string]struct {
		status int
		header http.Header
		body   string
		want   *llm.StatusError
	}{
		"a blank body is no body": {
			status: 503,
			header: http.Header{"Retry-After": {"1"}, "X-Typesafe-Request-Id": {"req-1"}, "X-Goog-Request-Id": {"g-1"}, "Content-Type": {"application/json"}},
			body:   " \n\t ",
			want:   &llm.StatusError{StatusCode: 503, Header: http.Header{"Retry-After": {"1"}, "X-Typesafe-Request-Id": {"req-1"}}},
		},
		"an empty body is no body": {
			status: 500,
			want:   &llm.StatusError{StatusCode: 500, Header: http.Header{}},
		},
		"a body is kept as received": {
			status: 429,
			header: http.Header{"Retry-After-Ms": {"250"}},
			body:   ` {"error":{"code":429,"message":"slow down"}} `,
			want:   &llm.StatusError{StatusCode: 429, Header: http.Header{"Retry-After-Ms": {"250"}}, Body: []byte(` {"error":{"code":429,"message":"slow down"}} `)},
		},
		"a redirect is not followed": {
			status: 307,
			header: http.Header{"Location": {"http://elsewhere.test/v1beta/interactions"}},
			want:   &llm.StatusError{StatusCode: 307, Header: http.Header{}},
		},
	}
	for name, tt := range statusTests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			tr := &transport{respond: func(req *http.Request) (*http.Response, error) {
				return response(req, tt.status, tt.header, tt.body), nil
			}}
			p := newProvider(t, WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: tr}))
			trace := new(llm.Trace)
			req := prompted()
			req.Trace = trace
			_, err := p.Do(t.Context(), req)
			se, ok := errors.AsType[*llm.StatusError](err)
			if !ok {
				t.Fatalf("Do error = %#v, want an *llm.StatusError", err)
			}
			if diff := gocmp.Diff(tt.want, se); diff != "" {
				t.Errorf("StatusError (-want +got):\n%s", diff)
			}
			if (se.Body == nil) != (tt.want.Body == nil) {
				t.Errorf("Body nil = %v, want %v", se.Body == nil, tt.want.Body == nil)
			}
			if n := len(tr.recorded()); n != 1 {
				t.Errorf("requests = %d, want 1", n)
			}
			if trace.Responded() {
				t.Errorf("a status response was recorded as the response body")
			}
			if _, ok := trace.Request(); !ok || trace.API() != "interactions" {
				t.Errorf("request recorded %v with API %q, want it recorded with interactions", ok, trace.API())
			}
		})
	}
	t.Run("a connection failure", func(t *testing.T) {
		clearEnv(t)
		tr := &transport{respond: func(*http.Request) (*http.Response, error) { return nil, errors.New("dial: connection refused") }}
		p := newProvider(t, WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: tr}))
		_, err := p.Do(t.Context(), prompted())
		if _, ok := errors.AsType[*llm.ConnectionError](err); !ok {
			t.Fatalf("Do error = %#v, want an *llm.ConnectionError", err)
		}
	})
	t.Run("a timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			clearEnv(t)
			tr := &transport{respond: func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, req.Context().Err()
			}}
			p := newProvider(t, WithAPIKey("not-a-key"), WithTimeout(30*time.Second), WithHTTPClient(&http.Client{Transport: tr}))
			start := time.Now()
			_, err := p.Do(t.Context(), prompted())
			if _, ok := errors.AsType[*llm.TimeoutError](err); !ok {
				t.Fatalf("Do error = %#v, want an *llm.TimeoutError", err)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("the timeout's chain lacks context.DeadlineExceeded")
			}
			if d := time.Since(start); d != 30*time.Second {
				t.Errorf("Do returned after %v, want the 30s timeout", d)
			}
		})
	})
	t.Run("a cancellation is the context's error", func(t *testing.T) {
		clearEnv(t)
		ctx, cancel := context.WithCancel(t.Context())
		tr := &transport{respond: func(req *http.Request) (*http.Response, error) {
			cancel()
			<-req.Context().Done()
			return nil, req.Context().Err()
		}}
		p := newProvider(t, WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: tr}))
		_, err := p.Do(ctx, prompted())
		if err != context.Canceled { //nolint:errorlint // the context's error itself is the contract.
			t.Fatalf("Do error = %#v, want context.Canceled itself", err)
		}
	})
	t.Run("a 2xx body that is not JSON", func(t *testing.T) {
		clearEnv(t)
		tr := &transport{reply: "<html>not json</html>"}
		p := newProvider(t, WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: tr}))
		trace := new(llm.Trace)
		req := prompted()
		req.Trace = trace
		_, err := p.Do(t.Context(), req)
		assertShapeError(t, err, "gemini: the response body is not an Interaction: the body is not JSON")
		if body, ok := trace.Response(); !ok || string(body) != "<html>not json</html>" || trace.FinishReason() != nil {
			t.Errorf("recorded response %v %q finish %v, want the body with a null finish reason", ok, body, trace.FinishReason())
		}
	})
}

// assertShapeError fails t unless err has the text want and is none of
// package llm's error types, so the Adapter neither retries it nor
// classifies it as a non-answer.
func assertShapeError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	var (
		se *llm.StatusError
		te *llm.TimeoutError
		ce *llm.ConnectionError
		ne *llm.NonAnswerError
	)
	if errors.As(err, &se) || errors.As(err, &te) || errors.As(err, &ce) || errors.As(err, &ne) {
		t.Errorf("error %#v is one of package llm's types", err)
	}
}

// TestResultShapeErrors checks the bodies whose shape the result rules
// cannot read: each is an error of a fixed text that quotes nothing of the
// body and is none of package llm's types, and the body is recorded first.
// Upstream, through google-genai 2.24.0, ends a body that is not JSON in the
// SDK's validation error, and a JSON body its model rejects in a raw
// exception without debug data (measured on the reference Python); it takes
// a count such as "3", 3.0, true, -3 or 2^64 as an int.
func TestResultShapeErrors(t *testing.T) {
	const prefix = "gemini: the response body is not an Interaction: "
	tests := map[string]struct {
		body string
		want string
	}{
		"not JSON":                         {body: "not json", want: "the body is not JSON"},
		"empty":                            {body: "", want: "the body is not JSON"},
		"a NaN count":                      {body: `{"status":"completed","usage":{"total_input_tokens":NaN,"total_output_tokens":1}}`, want: "the body is not JSON"},
		"invalid UTF-8":                    {body: "{\"status\":\"completed\",\"x\":\"\xff\"}", want: "the body is not JSON"},
		"an escaped lone surrogate":        {body: `{"status":"failed","errors":[{"code":"\ud800"}]}`, want: "the body is not JSON"},
		"an array":                         {body: `[1]`, want: "the body is not a JSON object"},
		"null":                             {body: `null`, want: "the body is not a JSON object"},
		"a status number":                  {body: `{"status":5}`, want: "status is neither a string nor null"},
		"a status object":                  {body: `{"status":{"s":"completed"}}`, want: "status is neither a string nor null"},
		"errors not a list":                {body: `{"status":"failed","errors":{"code":"x"}}`, want: "errors is neither a list nor null"},
		"a step's content not a list":      {body: `{"status":"completed","steps":[{"type":"model_output","content":"x"}],"usage":{"total_input_tokens":1,"total_output_tokens":1}}`, want: "the content of a step is neither a list nor null"},
		"an input step's content a number": {body: `{"status":"completed","steps":[{"type":"user_input","content":5}],"usage":{"total_input_tokens":1,"total_output_tokens":1}}`, want: "the content of a step is neither a list nor null"},
		"usage not an object":              {body: `{"status":"completed","usage":[1]}`, want: "usage is neither an object nor null"},
		"a fractional count":               {body: `{"status":"completed","usage":{"total_input_tokens":3.5,"total_output_tokens":1}}`, want: "usage.total_input_tokens is not an integer from 0 to 2^64-1"},
		"a count spelled as a float":       {body: `{"status":"completed","usage":{"total_input_tokens":3.0,"total_output_tokens":1}}`, want: "usage.total_input_tokens is not an integer from 0 to 2^64-1"},
		"a count in a string":              {body: `{"status":"completed","usage":{"total_input_tokens":"3","total_output_tokens":1}}`, want: "usage.total_input_tokens is not an integer from 0 to 2^64-1"},
		"a negative count":                 {body: `{"status":"completed","usage":{"total_input_tokens":3,"total_output_tokens":-1}}`, want: "usage.total_output_tokens is not an integer from 0 to 2^64-1"},
		"a count of 2^64":                  {body: `{"status":"completed","usage":{"total_input_tokens":18446744073709551616,"total_output_tokens":1}}`, want: "usage.total_input_tokens is not an integer from 0 to 2^64-1"},
		"a count true":                     {body: `{"status":"completed","usage":{"total_input_tokens":true,"total_output_tokens":1}}`, want: "usage.total_input_tokens is not an integer from 0 to 2^64-1"},
		"steps not a list":                 {body: `{"status":"completed","steps":{"type":"model_output"},"usage":{"total_input_tokens":1,"total_output_tokens":1}}`, want: "steps is neither a list nor null"},
		"a step not an object":             {body: `{"status":"completed","steps":["x"],"usage":{"total_input_tokens":1,"total_output_tokens":1}}`, want: "a step is not an object"},
		"a later step not an object":       {body: `{"status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"T"}]},1],"usage":{"total_input_tokens":1,"total_output_tokens":1}}`, want: "a step is not an object"},
		"a shape error before the counts":  {body: `{"status":"completed","usage":{"total_input_tokens":"3"}}`, want: "usage.total_input_tokens is not an integer from 0 to 2^64-1"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			trace := new(llm.Trace)
			_, err := result([]byte(tt.body), trace)
			assertShapeError(t, err, prefix+tt.want)
			if body, ok := trace.Response(); !ok || string(body) != tt.body {
				t.Errorf("recorded response %v %q, want the body", ok, body)
			}
		})
	}
	t.Run("errors nested deeper than the writer goes", func(t *testing.T) {
		body := `{"status":"failed","errors":[{"code":"x","d":` + strings.Repeat("[", 300) + strings.Repeat("]", 300) + `}]}`
		_, err := result([]byte(body), nil)
		assertShapeError(t, err, prefix+"errors is nested too deeply")
	})
}

// TestUsageCounts checks the counts a completed interaction gives: integers
// from 0 to 2^64-1, -0 read as 0; a count that is null or absent, or usage
// that is absent, is the omitted-usage non-answer
// (providers/gemini.py:67-71,83-85).
func TestUsageCounts(t *testing.T) {
	tests := map[string]struct {
		usage   string
		wantIn  uint64
		wantOut uint64
		wantNA  bool
	}{
		"zero and the largest":      {usage: `{"total_input_tokens":0,"total_output_tokens":18446744073709551615}`, wantIn: 0, wantOut: 18446744073709551615},
		"minus zero":                {usage: `{"total_input_tokens":-0,"total_output_tokens":7}`, wantIn: 0, wantOut: 7},
		"other members are ignored": {usage: `{"total_tokens":19,"total_input_tokens":12,"total_output_tokens":7,"total_thought_tokens":2}`, wantIn: 12, wantOut: 7},
		"the input count absent":    {usage: `{"total_output_tokens":4}`, wantNA: true},
		"the input count null":      {usage: `{"total_input_tokens":null,"total_output_tokens":4}`, wantNA: true},
		"the output count absent":   {usage: `{"total_input_tokens":3}`, wantNA: true},
		"the output count null":     {usage: `{"total_input_tokens":3,"total_output_tokens":null}`, wantNA: true},
		"usage empty":               {usage: `{}`, wantNA: true},
		"usage absent":              {wantNA: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := `{"status":"completed","steps":[]`
			if tt.usage != "" {
				body += `,"usage":` + tt.usage
			}
			body += "}"
			got, err := result([]byte(body), nil)
			if tt.wantNA {
				if na, ok := errors.AsType[*llm.NonAnswerError](err); !ok || na.Message != "Gemini response omitted usage." {
					t.Fatalf("result = %+v, %#v; want the omitted-usage non-answer", got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("result: %v", err)
			}
			want := &llm.Result{InputTokens: llm.Count{N: tt.wantIn, Known: true}, OutputTokens: llm.Count{N: tt.wantOut, Known: true}}
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("result (-want +got):\n%s", diff)
			}
		})
	}
}

// TestIncompleteReasonTexts checks the reason of the did-not-complete
// non-answer (providers/gemini.py:76-82): the status; unknown for a null,
// absent or empty status; and, when errors is a list that is not empty,
// Python's str() of the SDK's list of Error objects, as measured on the
// reference Python (google-genai 2.24.0, CPython 3.14.3), when every
// element holds only code and message, each a string of printable ASCII or
// null; for any other list, the list as compact JSON. errors is not read
// for a completed interaction.
func TestIncompleteReasonTexts(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
		// wantList, when true, asks for the errors list as compact JSON:
		// the reason must be one JSON value equal, member order included,
		// to the body's errors member.
		wantList bool
	}{
		"code and message":                 {body: `{"status":"failed","errors":[{"code":"x","message":"y"}]}`, want: `[Error(code='x', message='y')]`},
		"message only":                     {body: `{"status":"failed","errors":[{"message":"y"}]}`, want: `[Error(code=None, message='y')]`},
		"code only":                        {body: `{"status":"failed","errors":[{"code":"x"}]}`, want: `[Error(code='x', message=None)]`},
		"an empty error":                   {body: `{"status":"failed","errors":[{}]}`, want: `[Error(code=None, message=None)]`},
		"two errors":                       {body: `{"status":"failed","errors":[{"code":"a","message":"b"},{"code":"c","message":"d"}]}`, want: `[Error(code='a', message='b'), Error(code='c', message='d')]`},
		"the fields in model order":        {body: `{"status":"failed","errors":[{"message":"y","code":"x"}]}`, want: `[Error(code='x', message='y')]`},
		"a null code":                      {body: `{"status":"failed","errors":[{"code":null,"message":"y"}]}`, want: `[Error(code=None, message='y')]`},
		"empty strings":                    {body: `{"status":"failed","errors":[{"code":"","message":""}]}`, want: `[Error(code='', message='')]`},
		"a repeated code":                  {body: `{"status":"failed","errors":[{"code":"x","code":"y","message":"m"}]}`, want: `[Error(code='y', message='m')]`},
		"a single quote":                   {body: `{"status":"failed","errors":[{"code":"it's","message":"say \"hi\""}]}`, want: `[Error(code="it's", message='say "hi"')]`},
		"both quotes and a backslash":      {body: `{"status":"failed","errors":[{"code":"it's \"x\"","message":"back\\slash"}]}`, want: `[Error(code='it\'s "x"', message='back\\slash')]`},
		"the ends of printable ASCII":      {body: `{"status":"failed","errors":[{"code":" ~","message":"!}"}]}`, want: `[Error(code=' ~', message='!}')]`},
		"errors on a null status":          {body: `{"status":null,"errors":[{"code":"x"}]}`, want: `[Error(code='x', message=None)]`},
		"an unknown member":                {body: `{"status":"failed","errors":[{"code":"x","message":"y","zeta":"z"}]}`, want: `[{"code":"x","message":"y","zeta":"z"}]`},
		"an unknown member first":          {body: `{"status":"failed","errors":[{"zeta":"z","code":"x","message":"y"}]}`, want: `[{"zeta":"z","code":"x","message":"y"}]`},
		"one element with another member":  {body: `{"status":"failed","errors":[{"code":"a"},{"code":"b","more":1}]}`, want: `[{"code":"a"},{"code":"b","more":1}]`},
		"an element that is not an object": {body: `{"status":"failed","errors":["x",{"code":"a"}]}`, want: `["x",{"code":"a"}]`},
		"a code that is a number":          {body: `{"status":"failed","errors":[{"code":5,"message":"y"}]}`, want: `[{"code":5,"message":"y"}]`},
		"a message that is a list":         {body: `{"status":"failed","errors":[{"code":"x","message":["y"]}]}`, want: `[{"code":"x","message":["y"]}]`},
		"a repeated unknown member":        {body: `{"status":"failed","errors":[{"message":"m","x":1,"x":2,"code":"c"}]}`, want: `[{"message":"m","x":2,"code":"c"}]`},
		"numbers as the body writes them":  {body: `{"status":"failed","errors":[{"code":"x","n":-0,"big":123456789012345678901234567890,"z":0.0,"m":-0.0,"exp":1E2,"f":0.1,"e":1e-05,"b":1e+16}]}`, want: `[{"code":"x","n":0,"big":123456789012345678901234567890,"z":0.0,"m":-0.0,"exp":100.0,"f":0.1,"e":1e-05,"b":1e+16}]`},
		"a control character":              {body: `{"status":"failed","errors":[{"code":"a\tb\nc\u0000d\u007f"}]}`, wantList: true},
		"a character past ASCII":           {body: `{"status":"failed","errors":[{"code":"caf\u00e9","message":"\ud83d\ude00 \u2028"}]}`, wantList: true},
		"an empty list":                    {body: `{"status":"failed","errors":[]}`, want: "failed"},
		"errors null":                      {body: `{"status":"failed","errors":null}`, want: "failed"},
		"errors absent":                    {body: `{"status":"incomplete"}`, want: "incomplete"},
		"a status of another word":         {body: `{"status":"weird"}`, want: "weird"},
		"a null status":                    {body: `{"status":null}`, want: "unknown"},
		"an empty status":                  {body: `{"status":""}`, want: "unknown"},
		"no status":                        {body: `{"steps":[]}`, want: "unknown"},
		"an empty list and no status":      {body: `{"errors":[]}`, want: "unknown"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			trace := new(llm.Trace)
			_, err := result([]byte(tt.body), trace)
			na, ok := errors.AsType[*llm.NonAnswerError](err)
			if !ok {
				t.Fatalf("result error = %#v, want a non-answer", err)
			}
			if !trace.Responded() {
				t.Errorf("the body was not recorded")
			}
			if !tt.wantList {
				if want := incompleteText(tt.want); na.Message != want {
					t.Errorf("non-answer =\n%s\nwant\n%s", na.Message, want)
				}
				return
			}
			reason, ok := strings.CutPrefix(na.Message, "Gemini response did not complete: ")
			reason, ok2 := strings.CutSuffix(reason, ".")
			if !ok || !ok2 {
				t.Fatalf("non-answer %q lacks upstream's frame", na.Message)
			}
			doc, err := jsonx.Read([]byte(tt.body))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			list, _ := doc.Member("errors")
			want, err := list.PydanticJSON()
			if err != nil {
				t.Fatalf("PydanticJSON: %v", err)
			}
			if same, err := jsonx.EqualOrdered([]byte(reason), want); err != nil || !same {
				t.Errorf("reason %s, want the errors list %s as compact JSON (%v)", reason, want, err)
			}
		})
	}
	t.Run("an empty status is recorded as itself", func(t *testing.T) {
		trace := new(llm.Trace)
		_, _ = result([]byte(`{"status":""}`), trace)
		if f := trace.FinishReason(); f == nil || *f != "" {
			t.Errorf("finish reason = %v, want the empty string, not null", f)
		}
	})
	t.Run("errors of a completed interaction are not read", func(t *testing.T) {
		got, err := result([]byte(`{"status":"completed","errors":{"not":"a list"},"steps":[],"usage":{"total_input_tokens":3,"total_output_tokens":4}}`), nil)
		if err != nil || got.InputTokens.N != 3 {
			t.Fatalf("result = %+v, %v; want the answer", got, err)
		}
	})
}

// TestDoRecordsTheExchange checks what Do records in the Trace: the API
// name interactions and the body it sent, already recorded when the request
// reaches the transport, as upstream records before it sends
// (providers/gemini.py:137-138); the response body
// as received with its status as the finish reason; and with a nil Trace
// it records nothing and still answers.
func TestDoRecordsTheExchange(t *testing.T) {
	clearEnv(t)
	trace := new(llm.Trace)
	tr := &transport{reply: completedBody, trace: trace}
	p := newProvider(t, WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: tr}))
	req := prompted()
	req.Trace = trace
	res, err := p.Do(t.Context(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if res.Text != `{"answers": {}}` {
		t.Errorf("text = %q", res.Text)
	}
	first := tr.recorded()[0]
	sent := first.body
	if string(first.traced) != string(sent) || first.traceAPI != "interactions" {
		t.Errorf("at the transport the Trace held %q with API %q, want the sent body with interactions", first.traced, first.traceAPI)
	}
	if body, ok := trace.Request(); !ok || string(body) != string(sent) || trace.API() != "interactions" {
		t.Errorf("recorded request %v %s api %q, want the sent body with interactions", ok, body, trace.API())
	}
	if body, ok := trace.Response(); !ok || string(body) != completedBody {
		t.Errorf("recorded response %v %s, want the body as received", ok, body)
	}
	if f := trace.FinishReason(); f == nil || *f != "completed" {
		t.Errorf("finish reason = %v, want completed", f)
	}
	if _, err := p.Do(t.Context(), prompted()); err != nil {
		t.Errorf("Do with a nil Trace: %v", err)
	}
}

// TestProviderModelAndClose checks that Model returns the model New was
// given, and nothing of the base URL's userinfo or query, and that Close
// returns nil for an owned client and a borrowed one,
// leaving a borrowed client usable.
func TestProviderModelAndClose(t *testing.T) {
	clearEnv(t)
	owned, err := New("gemini-3.8-flash:with:colons", WithAPIKey("not-a-key"), WithBaseURL("https://alice:madeupword@api.example.test/p?tenant=one"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if owned.Model() != "gemini-3.8-flash:with:colons" {
		t.Errorf("Model() = %q", owned.Model())
	}
	if err := owned.Close(); err != nil {
		t.Errorf("Close of an owned client: %v", err)
	}
	tr := &transport{reply: completedBody}
	c := &http.Client{Transport: tr}
	borrowed := newProvider(t, WithAPIKey("not-a-key"), WithHTTPClient(c))
	if err := borrowed.Close(); err != nil {
		t.Errorf("Close of a borrowed client: %v", err)
	}
	if c.CheckRedirect != nil || c.Transport != tr {
		t.Errorf("the borrowed client was written")
	}
	if _, err := borrowed.Do(t.Context(), prompted()); err != nil {
		t.Errorf("Do after Close: %v", err)
	}
}

// TestProviderPrintsNoCredential checks all formatting verbs on values,
// pointers and interfaces, and both slog handlers. Fields remain private
// except for fmt's invalid value-%p diagnostic, which is detected separately.
// A nil pointer prints as <nil>.
func TestProviderPrintsNoCredential(t *testing.T) {
	clearEnv(t)
	planted := map[string]string{"key": "amberwhistle", "user": "cedarspark", "password": "violetmeadow", "query": "copperfern", "host": "willowharbor"}
	p, err := New("test-model", WithAPIKey(planted["key"]), WithBaseURL("https://"+planted["user"]+":"+planted["password"]+"@"+planted["host"]+".test/p?tenant="+planted["query"]))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	const (
		short    = "gemini.Provider(test-model)"
		goSyntax = "gemini.Provider{Model:test-model}"
		nilText  = "<nil>"
	)
	checkPlanted := func(t *testing.T, where, out string) {
		t.Helper()
		for what, value := range planted {
			if strings.Contains(out, value) {
				t.Errorf("%s holds the %s", where, what)
			}
		}
	}

	var boxed llm.Provider = p
	var boxedAny any = *p
	tests := map[string]struct {
		v          any
		nilPointer bool
		value      bool
	}{
		"success: value":              {v: *p, value: true},
		"success: pointer":            {v: p},
		"success: provider interface": {v: boxed},
		"success: any interface":      {v: boxedAny, value: true},
		"success: nil pointer":        {v: (*Provider)(nil), nilPointer: true},
	}
	// Each format is static so vet checks it, including unsupported verbs.
	formats := map[string]struct{ print func(any) string }{
		"v": {print: func(v any) string {
			return fmt.Sprintf("%v\n%20v\n%-20v\n%020v\n%+20v\n%#20v\n%.3v", v, v, v, v, v, v, v)
		}},
		"s": {print: func(v any) string {
			return fmt.Sprintf("%s\n%20s\n%-20s\n%020s\n%+20s\n%#20s\n%.3s", v, v, v, v, v, v, v)
		}},
		"q": {print: func(v any) string {
			return fmt.Sprintf("%q\n%20q\n%-20q\n%020q\n%+20q\n%#20q\n%.3q", v, v, v, v, v, v, v)
		}},
		"x": {print: func(v any) string {
			return fmt.Sprintf("%x\n%20x\n%-20x\n%020x\n%+20x\n%#20x\n%.3x", v, v, v, v, v, v, v)
		}},
		"X": {print: func(v any) string {
			return fmt.Sprintf("%X\n%20X\n%-20X\n%020X\n%+20X\n%#20X\n%.3X", v, v, v, v, v, v, v)
		}},
		"d": {print: func(v any) string {
			return fmt.Sprintf("%d\n%20d\n%-20d\n%020d\n%+20d\n%#20d\n%.3d", v, v, v, v, v, v, v)
		}},
		"t": {print: func(v any) string {
			return fmt.Sprintf("%t\n%20t\n%-20t\n%020t\n%+20t\n%#20t\n%.3t", v, v, v, v, v, v, v)
		}},
		"o": {print: func(v any) string {
			return fmt.Sprintf("%o\n%20o\n%-20o\n%020o\n%+20o\n%#20o\n%.3o", v, v, v, v, v, v, v)
		}},
		"O": {print: func(v any) string {
			return fmt.Sprintf("%O\n%20O\n%-20O\n%020O\n%+20O\n%#20O\n%.3O", v, v, v, v, v, v, v)
		}},
		"b": {print: func(v any) string {
			return fmt.Sprintf("%b\n%20b\n%-20b\n%020b\n%+20b\n%#20b\n%.3b", v, v, v, v, v, v, v)
		}},
		"c": {print: func(v any) string {
			return fmt.Sprintf("%c\n%20c\n%-20c\n%020c\n%+20c\n%#20c\n%.3c", v, v, v, v, v, v, v)
		}},
		"U": {print: func(v any) string {
			return fmt.Sprintf("%U\n%20U\n%-20U\n%020U\n%+20U\n%#20U\n%.3U", v, v, v, v, v, v, v)
		}},
		"e": {print: func(v any) string {
			return fmt.Sprintf("%e\n%20e\n%-20e\n%020e\n%+20e\n%#20e\n%.3e", v, v, v, v, v, v, v)
		}},
		"E": {print: func(v any) string {
			return fmt.Sprintf("%E\n%20E\n%-20E\n%020E\n%+20E\n%#20E\n%.3E", v, v, v, v, v, v, v)
		}},
		"f": {print: func(v any) string {
			return fmt.Sprintf("%f\n%20f\n%-20f\n%020f\n%+20f\n%#20f\n%.3f", v, v, v, v, v, v, v)
		}},
		"F": {print: func(v any) string {
			return fmt.Sprintf("%F\n%20F\n%-20F\n%020F\n%+20F\n%#20F\n%.3F", v, v, v, v, v, v, v)
		}},
		"g": {print: func(v any) string {
			return fmt.Sprintf("%g\n%20g\n%-20g\n%020g\n%+20g\n%#20g\n%.3g", v, v, v, v, v, v, v)
		}},
		"G": {print: func(v any) string {
			return fmt.Sprintf("%G\n%20G\n%-20G\n%020G\n%+20G\n%#20G\n%.3G", v, v, v, v, v, v, v)
		}},
		"z": {print: func(v any) string {
			return fmt.Sprintf("%z\n%20z\n%-20z\n%020z\n%+20z\n%#20z\n%.3z", v, v, v, v, v, v, v)
		}},
		"p": {print: func(v any) string {
			return fmt.Sprintf("%p\n%20p\n%-20p\n%020p\n%+20p\n%#20p\n%.3p", v, v, v, v, v, v, v)
		}},
		"T": {print: func(v any) string {
			return fmt.Sprintf("%T\n%20T\n%-20T\n%020T\n%+20T\n%#20T\n%.3T", v, v, v, v, v, v, v)
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			for modifier, out := range map[string]string{"plus": fmt.Sprintf("%+v", tt.v), "sharp": fmt.Sprintf("%#v", tt.v)} {
				want := short
				if modifier == "sharp" {
					want = goSyntax
				}
				if tt.nilPointer {
					want = nilText
				}
				checkPlanted(t, modifier, out)
				if diff := gocmp.Diff(want, out); diff != "" {
					t.Errorf("%s mismatch (-want +got):\n%s", modifier, diff)
				}
			}
			for verb, format := range formats {
				t.Run(verb, func(t *testing.T) {
					out := format.print(tt.v)
					if verb == "p" && tt.value {
						// A value's %p is fmt's bad-verb path, which bypasses
						// Format. This detects it; no safety is claimed for it.
						for item := range strings.SplitSeq(out, "\n") {
							if !strings.Contains(item, "%!p(gemini.Provider={") || !strings.HasSuffix(item, "})") {
								t.Error("missing fmt value-pointer diagnostic")
							}
						}
						return
					}
					checkPlanted(t, verb, out)
					if verb == "p" || verb == "T" {
						return
					}
					want := []string{short, short, short, short, short, short, short}
					if verb == "v" {
						want[5] = goSyntax
					}
					if tt.nilPointer {
						for i := range want {
							want[i] = nilText
						}
					}
					if diff := gocmp.Diff(want, strings.Split(out, "\n")); diff != "" {
						t.Errorf("plain/width/left/zero/plus/sharp/precision mismatch (-want +got):\n%s", diff)
					}
				})
			}
		})
	}
	for name, out := range map[string]string{
		"slice":  fmt.Sprintf("%v", []any{*p, boxed}),
		"struct": fmt.Sprintf("%#v", struct{ Value Provider }{Value: *p}),
	} {
		checkPlanted(t, name, out)
	}

	handlers := map[string]struct {
		handler func(io.Writer) slog.Handler
		want    string // a part of the record, "" for none
	}{
		"success: slog text handler logs the model only": {
			handler: func(w io.Writer) slog.Handler { return slog.NewTextHandler(w, nil) },
			want:    " pointer=" + short + " value=" + short + " interface=" + short + "\n",
		},
		"success: slog JSON handler logs no credential": {
			handler: func(w io.Writer) slog.Handler { return slog.NewJSONHandler(w, nil) },
		},
	}
	for name, tt := range handlers {
		t.Run(name, func(t *testing.T) {
			var buf strings.Builder
			slog.New(tt.handler(&buf)).LogAttrs(t.Context(), slog.LevelInfo, "provider", slog.Any("pointer", p), slog.Any("value", *p), slog.Any("interface", boxed))
			out := buf.String()
			if !strings.Contains(out, tt.want) {
				t.Errorf("the record does not hold %q", tt.want)
			}
			checkPlanted(t, "the record", out)
		})
	}
}
