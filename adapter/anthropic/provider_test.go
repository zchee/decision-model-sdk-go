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

package anthropic

import (
	"bytes"
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
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// okBody is a Messages response that completed with one text block.
const okBody = `{"id":"message-test","type":"message","role":"assistant","model":"test-model","stop_reason":"end_turn",` +
	`"content":[{"type":"text","text":"{\"answers\":{\"positive\":true}}"}],"usage":{"input_tokens":12,"output_tokens":7}}`

// clearEnv removes the variables New reads for the rest of the test and
// restores them afterwards: t.Setenv registers the restore, then the
// variable is unset, so the test sees it unset rather than empty.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{envAPIKey, envAuthToken, envBaseURL} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("Unsetenv(%s): %v", name, err)
		}
	}
}

// seen is one request the transport received, as the server would see it.
type seen struct {
	method string
	url    string
	header http.Header
	body   []byte
}

// transport is an http.RoundTripper that records every request and answers
// with respond. No request leaves the process.
type transport struct {
	respond func(*http.Request) (*http.Response, error)

	mu   sync.Mutex
	seen []seen
}

func (tr *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
	}
	tr.mu.Lock()
	tr.seen = append(tr.seen, seen{method: req.Method, url: req.URL.String(), header: req.Header.Clone(), body: body})
	tr.mu.Unlock()
	return tr.respond(req)
}

// requests returns a copy of what the transport received.
func (tr *transport) requests() []seen {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]seen(nil), tr.seen...)
}

// answer returns a transport that answers every request with status, the
// headers in header and body.
func answer(status int, header http.Header, body string) *transport {
	return &transport{respond: func(req *http.Request) (*http.Response, error) {
		return response(req, status, header, body), nil
	}}
}

// response returns a response to req.
func response(req *http.Request, status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		Status:        strconv.Itoa(status) + " " + http.StatusText(status),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// structuredRequest is a structured request of the upstream fixtures, with
// a Trace of its own.
func structuredRequest() *llm.Request {
	return &llm.Request{Messages: testMessages, Schema: []byte(testSchema), Structured: true, Trace: new(llm.Trace)}
}

// canary returns a key nobody issued: the fixed prefix ts-canary- and 16
// random bytes in hex, so that a test can look for it in every place it must
// not reach.
func canary(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return "ts-canary-" + hex.EncodeToString(b)
}

// TestRequestBody pins the parts of the request body the ported tests do
// not reach: every system message's content joined with two newlines; the
// system member sent even when it is empty, as upstream sends it
// (providers/anthropic.py:42-47); the other messages in their order, roles
// kept; and a schema that is not one JSON value refused before any request.
func TestRequestBody(t *testing.T) {
	tests := map[string]struct {
		req     *llm.Request
		want    string
		wantErr error
	}{
		"success: two system messages joined, roles and order kept": {
			req: &llm.Request{Messages: []llm.Message{
				{Role: "system", Content: "one"},
				{Role: "user", Content: "u1"},
				{Role: "system", Content: "two"},
				{Role: "assistant", Content: "a1"},
				{Role: "user", Content: "u2"},
			}},
			want: `{"model":"test-model","max_tokens":4096,"system":"one\n\ntwo","messages":[{"role":"user","content":"u1"},{"role":"assistant","content":"a1"},{"role":"user","content":"u2"}]}`,
		},
		"success: no system message sends an empty system": {
			req:  &llm.Request{Messages: []llm.Message{{Role: "user", Content: "u"}}},
			want: `{"model":"test-model","max_tokens":4096,"system":"","messages":[{"role":"user","content":"u"}]}`,
		},
		"success: no message at all": {
			req:  &llm.Request{},
			want: `{"model":"test-model","max_tokens":4096,"system":"","messages":[]}`,
		},
		"error: structured with a schema that is not JSON": {
			req:     &llm.Request{Messages: testMessages, Schema: []byte(`{`), Structured: true},
			wantErr: jsonx.ErrInvalid,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			p, err := New("test-model", WithAPIKey("not-a-key"))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			body, err := p.requestBody(tt.req)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("requestBody error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("requestBody: %v", err)
			}
			if diff := gocmp.Diff(tt.want, string(body)); diff != "" {
				t.Errorf("request body (-want +got):\n%s", diff)
			}
		})
	}
}

// TestDoRefusesABodyItCannotWrite pins that a request body that cannot be
// written fails Do with a plain error before anything is recorded or sent.
func TestDoRefusesABodyItCannotWrite(t *testing.T) {
	clearEnv(t)
	tr := answer(200, nil, okBody)
	p, err := New("test-model", WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: tr}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := &llm.Request{Messages: testMessages, Schema: []byte(`{"a":1,"a":2}`), Structured: true, Trace: new(llm.Trace)}
	res, err := p.Do(t.Context(), req)
	if res != nil || !errors.Is(err, jsonx.ErrInvalid) {
		t.Fatalf("Do = %v, %v; want nil and an error wrapping jsonx.ErrInvalid", res, err)
	}
	if text := err.Error(); !strings.HasPrefix(text, "anthropic: the request body could not be written: "+jsonx.ErrInvalid.Error()) {
		t.Errorf("error text = %q, want the fixed text with jsonx's refusal", text)
	}
	if n := len(tr.requests()); n != 0 {
		t.Errorf("the transport received %d requests, want 0", n)
	}
	if _, ok := req.Trace.Request(); ok || req.Trace.Responded() {
		t.Errorf("the Trace recorded something for a request that was never built")
	}
}

// TestAuthHeaders pins how the credential travels, as the anthropic SDK that
// upstream builds its client with sends it: an API key as x-api-key, an auth
// token as Authorization: Bearer, both headers when both are found, and New
// failing when neither is. Without a credential option New reads both
// variables; with either option it reads neither variable. A credential,
// given or read, is trimmed of surrounding white space, and one that is
// empty after that counts as not given: the vendor's SDK would send a key
// of spaces, which its HTTP library then refuses before any request.
func TestAuthHeaders(t *testing.T) {
	tests := map[string]struct {
		env      map[string]string
		opts     []Option
		want     http.Header
		wantText string
	}{
		"success: key only": {
			env:  map[string]string{envAPIKey: "not-a-key"},
			want: http.Header{"X-Api-Key": {"not-a-key"}},
		},
		"success: token only": {
			env:  map[string]string{envAuthToken: "madeupword"},
			want: http.Header{"Authorization": {"Bearer madeupword"}},
		},
		"success: both send both headers": {
			env:  map[string]string{envAPIKey: "not-a-key", envAuthToken: "madeupword"},
			want: http.Header{"X-Api-Key": {"not-a-key"}, "Authorization": {"Bearer madeupword"}},
		},
		"error: neither": {
			wantText: noCredentialText,
		},
		"success: a key option reads no token variable": {
			env:  map[string]string{envAuthToken: "madeupword"},
			opts: []Option{WithAPIKey("not-a-key")},
			want: http.Header{"X-Api-Key": {"not-a-key"}},
		},
		"success: a token option reads no key variable": {
			env:  map[string]string{envAPIKey: "not-a-key"},
			opts: []Option{WithAuthToken("madeupword")},
			want: http.Header{"Authorization": {"Bearer madeupword"}},
		},
		"success: both options send both headers": {
			env:  map[string]string{envAPIKey: "variable-key", envAuthToken: "variable-token"},
			opts: []Option{WithAuthToken("madeupword"), WithAPIKey("not-a-key")},
			want: http.Header{"X-Api-Key": {"not-a-key"}, "Authorization": {"Bearer madeupword"}},
		},
		"success: an empty key option counts as not given": {
			env:  map[string]string{envAPIKey: "not-a-key", envAuthToken: "madeupword"},
			opts: []Option{WithAPIKey("")},
			want: http.Header{"X-Api-Key": {"not-a-key"}, "Authorization": {"Bearer madeupword"}},
		},
		"success: an empty token option counts as not given": {
			env:  map[string]string{envAuthToken: "madeupword"},
			opts: []Option{WithAuthToken("")},
			want: http.Header{"Authorization": {"Bearer madeupword"}},
		},
		"error: empty options and no variable": {
			opts:     []Option{WithAPIKey(""), WithAuthToken("")},
			wantText: noCredentialText,
		},
		"success: an empty key variable is no key": {
			env:  map[string]string{envAPIKey: "", envAuthToken: "madeupword"},
			want: http.Header{"Authorization": {"Bearer madeupword"}},
		},
		"error: a key variable of white space only is no key": {
			env:      map[string]string{envAPIKey: "   "},
			wantText: noCredentialText,
		},
		"success: the variables are trimmed": {
			env:  map[string]string{envAPIKey: " not-a-key ", envAuthToken: "\tmadeupword\n"},
			want: http.Header{"X-Api-Key": {"not-a-key"}, "Authorization": {"Bearer madeupword"}},
		},
		"success: the options are trimmed": {
			opts: []Option{WithAPIKey("  not-a-key\t"), WithAuthToken("\nmadeupword ")},
			want: http.Header{"X-Api-Key": {"not-a-key"}, "Authorization": {"Bearer madeupword"}},
		},
		"success: a key option of white space only counts as not given": {
			env:  map[string]string{envAuthToken: "madeupword"},
			opts: []Option{WithAPIKey("   ")},
			want: http.Header{"Authorization": {"Bearer madeupword"}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			tr := answer(200, nil, okBody)
			p, err := New("test-model", append([]Option{WithHTTPClient(&http.Client{Transport: tr})}, tt.opts...)...)
			if tt.wantText != "" {
				if p != nil || err == nil {
					t.Fatalf("New = %T (nil %t), %v; want nil and an error", p, p == nil, err)
				}
				if diff := gocmp.Diff(tt.wantText, err.Error()); diff != "" {
					t.Errorf("error text (-want +got):\n%s", diff)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := p.Do(t.Context(), structuredRequest()); err != nil {
				t.Fatalf("Do: %v", err)
			}
			got := tr.requests()[0].header
			auth := http.Header{}
			for _, k := range []string{"X-Api-Key", "Authorization"} {
				if v, ok := got[k]; ok {
					auth[k] = v
				}
			}
			if diff := gocmp.Diff(tt.want, auth); diff != "" {
				t.Errorf("credential headers (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRequestHeaders pins the whole request the transport receives:
// POST to the default base URL's /v1/messages, the header set exactly
// Content-Type, anthropic-version 2023-06-01 and the credential's header,
// and nothing of the vendor SDK's own (no User-Agent of its own, no
// x-stainless headers).
func TestRequestHeaders(t *testing.T) {
	tests := map[string]struct {
		opt  Option
		want http.Header
	}{
		"success: API key": {
			opt: WithAPIKey("not-a-key"),
			want: http.Header{
				"Content-Type":      {"application/json"},
				"Anthropic-Version": {"2023-06-01"},
				"X-Api-Key":         {"not-a-key"},
			},
		},
		"success: auth token": {
			opt: WithAuthToken("madeupword"),
			want: http.Header{
				"Content-Type":      {"application/json"},
				"Anthropic-Version": {"2023-06-01"},
				"Authorization":     {"Bearer madeupword"},
			},
		},
		"success: API key and auth token": {
			opt: func(o *options) { WithAPIKey("not-a-key")(o); WithAuthToken("madeupword")(o) },
			want: http.Header{
				"Content-Type":      {"application/json"},
				"Anthropic-Version": {"2023-06-01"},
				"X-Api-Key":         {"not-a-key"},
				"Authorization":     {"Bearer madeupword"},
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			tr := answer(200, nil, okBody)
			p, err := New("test-model", tt.opt, WithHTTPClient(&http.Client{Transport: tr}))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := p.Do(t.Context(), structuredRequest()); err != nil {
				t.Fatalf("Do: %v", err)
			}
			got := tr.requests()
			if len(got) != 1 {
				t.Fatalf("the transport received %d requests, want 1", len(got))
			}
			if got[0].method != http.MethodPost || got[0].url != "https://api.anthropic.com/v1/messages" {
				t.Errorf("request = %s %s, want POST https://api.anthropic.com/v1/messages", got[0].method, got[0].url)
			}
			if diff := gocmp.Diff(tt.want, got[0].header); diff != "" {
				t.Errorf("request header (-want +got):\n%s", diff)
			}
		})
	}
}

// TestNewReadsTheEnvironmentOnce pins that New reads the variables once and
// Do none: values changed after New do not reach the transport.
func TestNewReadsTheEnvironmentOnce(t *testing.T) {
	tests := map[string]struct {
		before, after map[string]string
		wantURL       string
		wantHeader    string
		wantValue     string
	}{
		"success: API key and base URL": {
			before:     map[string]string{envAPIKey: "madeupword", envBaseURL: "https://first.example.test/base"},
			after:      map[string]string{envAPIKey: "not-a-key", envBaseURL: "https://second.example.test/other"},
			wantURL:    "https://first.example.test/base/v1/messages",
			wantHeader: "X-Api-Key",
			wantValue:  "madeupword",
		},
		"success: auth token": {
			before:     map[string]string{envAuthToken: "madeupword"},
			after:      map[string]string{envAuthToken: "not-a-key", envAPIKey: "not-a-key"},
			wantURL:    "https://api.anthropic.com/v1/messages",
			wantHeader: "Authorization",
			wantValue:  "Bearer madeupword",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.before {
				t.Setenv(k, v)
			}
			tr := answer(200, nil, okBody)
			p, err := New("test-model", WithHTTPClient(&http.Client{Transport: tr}))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			for k, v := range tt.after {
				t.Setenv(k, v)
			}
			if _, err := p.Do(t.Context(), structuredRequest()); err != nil {
				t.Fatalf("Do: %v", err)
			}
			got := tr.requests()[0]
			if got.url != tt.wantURL {
				t.Errorf("URL = %s, want %s", got.url, tt.wantURL)
			}
			if v := got.header.Get(tt.wantHeader); v != tt.wantValue {
				t.Errorf("%s = %q, want %q", tt.wantHeader, v, tt.wantValue)
			}
			if _, ok := got.header["X-Api-Key"]; ok && tt.wantHeader != "X-Api-Key" {
				t.Errorf("the request carries x-api-key from a variable set after New")
			}
		})
	}
}

// TestNewRefuses pins New's refusals, their fixed texts, which hold no value
// New was given or read, and their order: the output limit, an option's own
// value, the base URL, then the credential.
func TestNewRefuses(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		opts []Option
		want string
	}{
		"error: no credential": {
			want: noCredentialText,
		},
		"error: an empty API key variable": {
			env:  map[string]string{envAPIKey: ""},
			want: noCredentialText,
		},
		"error: an empty auth token variable": {
			env:  map[string]string{envAuthToken: ""},
			want: noCredentialText,
		},
		"error: an empty API key option": {
			opts: []Option{WithAPIKey("")},
			want: noCredentialText,
		},
		"error: an API key option of three spaces": {
			opts: []Option{WithAPIKey("   ")},
			want: noCredentialText,
		},
		"error: a base URL option that is not a URL": {
			opts: []Option{WithAPIKey("not-a-key"), WithBaseURL("https://madeupword host/")},
			want: optionURLText,
		},
		"error: a base URL option without a scheme": {
			opts: []Option{WithAPIKey("not-a-key"), WithBaseURL("api.example.test/v1")},
			want: optionURLText,
		},
		"error: a base URL option of another scheme": {
			opts: []Option{WithAPIKey("not-a-key"), WithBaseURL("ftp://api.example.test")},
			want: optionURLText,
		},
		"error: a base URL option without a host, userinfo not printed": {
			opts: []Option{WithAPIKey("not-a-key"), WithBaseURL("https://alice:madeupword@/v1?key=madeupword")},
			want: optionURLText,
		},
		"error: a base URL variable that is not a URL": {
			env:  map[string]string{envBaseURL: "relative/path?key=madeupword"},
			opts: []Option{WithAPIKey("not-a-key")},
			want: envURLText,
		},
		"error: a nil HTTP client": {
			opts: []Option{WithAPIKey("not-a-key"), WithHTTPClient(nil)},
			want: nilClientText,
		},
		"error: a zero timeout": {
			opts: []Option{WithAPIKey("not-a-key"), WithTimeout(0)},
			want: timeoutText,
		},
		"error: a negative timeout": {
			opts: []Option{WithAPIKey("not-a-key"), WithTimeout(-time.Second)},
			want: timeoutText,
		},
		"error: the output limit comes first": {
			opts: []Option{WithHTTPClient(nil), WithBaseURL("ftp://x"), WithMaxTokens(0)},
			want: maxTokensText,
		},
		"error: the first invalid option comes before the base URL": {
			opts: []Option{WithTimeout(0), WithHTTPClient(nil), WithBaseURL("ftp://x")},
			want: timeoutText,
		},
		"error: the base URL comes before the credential": {
			opts: []Option{WithBaseURL("ftp://x")},
			want: optionURLText,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			p, err := New("test-model", tt.opts...)
			if p != nil || err == nil {
				t.Fatalf("New = %T (nil %t), %v; want nil and an error", p, p == nil, err)
			}
			if diff := gocmp.Diff(tt.want, err.Error()); diff != "" {
				t.Errorf("error text (-want +got):\n%s", diff)
			}
		})
	}
}

// TestBaseURL pins the endpoint built from the base URL: the base path with
// its trailing slashes removed, then /v1/messages; an escaped path kept as
// it was written; the query kept; the fragment dropped; an empty variable
// taken as none.
func TestBaseURL(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		opts []Option
		want string
	}{
		"success: the default": {
			want: "https://api.anthropic.com/v1/messages",
		},
		"success: an empty variable is the default": {
			env:  map[string]string{envBaseURL: ""},
			want: "https://api.anthropic.com/v1/messages",
		},
		"success: the variable": {
			env:  map[string]string{envBaseURL: "http://127.0.0.1:8080"},
			want: "http://127.0.0.1:8080/v1/messages",
		},
		"success: an option replaces the variable": {
			env:  map[string]string{envBaseURL: "https://variable.example.test"},
			opts: []Option{WithBaseURL("https://option.example.test")},
			want: "https://option.example.test/v1/messages",
		},
		"success: an empty option counts as not given": {
			env:  map[string]string{envBaseURL: "https://variable.example.test"},
			opts: []Option{WithBaseURL("")},
			want: "https://variable.example.test/v1/messages",
		},
		"success: a trailing slash": {
			opts: []Option{WithBaseURL("https://api.example.test/")},
			want: "https://api.example.test/v1/messages",
		},
		"success: a path with two trailing slashes": {
			opts: []Option{WithBaseURL("https://api.example.test/proxy//")},
			want: "https://api.example.test/proxy/v1/messages",
		},
		"success: an escaped path": {
			opts: []Option{WithBaseURL("https://api.example.test/a%2Fb/")},
			want: "https://api.example.test/a%2Fb/v1/messages",
		},
		"success: the query kept and the fragment dropped": {
			opts: []Option{WithBaseURL("https://api.example.test/p?tenant=one#part")},
			want: "https://api.example.test/p/v1/messages?tenant=one",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			tr := answer(200, nil, okBody)
			p, err := New("test-model", append([]Option{WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: tr})}, tt.opts...)...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := p.Do(t.Context(), structuredRequest()); err != nil {
				t.Fatalf("Do: %v", err)
			}
			if got := tr.requests()[0].url; got != tt.want {
				t.Errorf("URL = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestKeyTravelsOnlyInItsHeader plants a canary credential and looks for it
// everywhere it must not be: the URL the provider builds (path, query,
// userinfo), the body, every other header, the Trace, the model name and
// the texts of the error Do returns in each of fmt's verbs. The base URL
// carries a userinfo and a query of its own: the userinfo reaches the
// server as Authorization: Basic when the key alone travels, in x-api-key,
// and not at all when an auth token takes the Authorization header.
func TestKeyTravelsOnlyInItsHeader(t *testing.T) {
	const base = "https://alice:madeupword@api.example.test/base?tenant=one" //nolint:gosec // G101: a made-up userinfo the test follows.
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:madeupword"))
	key := canary(t)
	tests := map[string]struct {
		opt        Option
		status     int
		body       string
		keyHeader  string
		keyValue   string
		wantHeader http.Header
	}{
		"success: API key": {
			opt: WithAPIKey(key), status: 200, body: okBody,
			keyHeader: "X-Api-Key", keyValue: key,
			wantHeader: http.Header{"Authorization": {basic}},
		},
		"error: API key with a status error": {
			opt: WithAPIKey(key), status: 500, body: `{"type":"error","error":{"type":"api_error","message":"boom"}}`,
			keyHeader: "X-Api-Key", keyValue: key,
			wantHeader: http.Header{"Authorization": {basic}},
		},
		"success: auth token": {
			opt: WithAuthToken(key), status: 200, body: okBody,
			keyHeader: "Authorization", keyValue: "Bearer " + key,
		},
		"success: API key and auth token": {
			opt: func(o *options) { WithAPIKey(key)(o); WithAuthToken("madeupword")(o) }, status: 200, body: okBody,
			keyHeader: "X-Api-Key", keyValue: key,
		},
		"error: auth token with a non-answer": {
			opt: WithAuthToken(key), status: 200, body: strings.Replace(okBody, `"end_turn"`, `"refusal"`, 1),
			keyHeader: "Authorization", keyValue: "Bearer " + key,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			tr := answer(tt.status, nil, tt.body)
			p, err := New("test-model", tt.opt, WithBaseURL(base), WithHTTPClient(&http.Client{Transport: tr}))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			req := structuredRequest()
			_, doErr := p.Do(t.Context(), req)
			if (doErr != nil) != (tt.status != 200 || strings.Contains(tt.body, "refusal")) {
				t.Fatalf("Do error = %v", doErr)
			}
			got := tr.requests()
			if len(got) != 1 {
				t.Fatalf("the transport received %d requests, want 1", len(got))
			}
			if got[0].url != "https://alice:madeupword@api.example.test/base/v1/messages?tenant=one" { //nolint:gosec // G101: the made-up userinfo of base.
				t.Errorf("URL = %s; want the userinfo and the query kept", got[0].url)
			}
			if strings.Contains(got[0].url, key) || bytes.Contains(got[0].body, []byte(key)) {
				t.Errorf("the canary is in the URL or the body")
			}
			if v := got[0].header.Get(tt.keyHeader); v != tt.keyValue {
				t.Errorf("%s = %q, want the canary credential", tt.keyHeader, v)
			}
			for k, vs := range got[0].header {
				if k == tt.keyHeader {
					continue
				}
				for _, v := range vs {
					if strings.Contains(v, key) {
						t.Errorf("header %s carries the canary", k)
					}
				}
			}
			if tt.wantHeader != nil {
				if v, ok := got[0].header["Authorization"]; !gocmp.Equal(tt.wantHeader["Authorization"], v) {
					t.Errorf("Authorization = %q (present %v), want %q from the userinfo", v, ok, tt.wantHeader["Authorization"])
				}
			} else if v := got[0].header.Values("Authorization"); len(v) != 1 || !strings.HasPrefix(v[0], "Bearer ") {
				t.Errorf("Authorization = %q, want the Bearer token alone and no Basic from the userinfo", v)
			}
			reqBody, _ := req.Trace.Request()
			respBody, _ := req.Trace.Response()
			if bytes.Contains(reqBody, []byte(key)) || bytes.Contains(respBody, []byte(key)) {
				t.Errorf("the Trace holds the canary")
			}
			if strings.Contains(p.Model(), key) {
				t.Errorf("the model name holds the canary")
			}
			if doErr != nil {
				for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
					if text := fmt.Sprintf(verb, doErr); strings.Contains(text, key) || strings.Contains(text, "madeupword") || strings.Contains(text, "tenant") {
						t.Errorf("the error printed with %s holds a credential or the query: %s", verb, text)
					}
				}
			}
		})
	}
}

// TestProviderErrorClassification pins the error Do returns for each way a
// request fails, which the Adapter classifies: a status error that keeps
// the status, the body (empty and not nil for a blank body, as the vendor
// SDK upstream hands over the empty string) and only the headers a retry
// reads; a redirect not followed; a connection failure; a timeout of the
// Provider's own and a deadline of the caller's, each in a synctest bubble;
// and a cancellation, which is the context's error itself. No response body
// is recorded for a failed request.
func TestProviderErrorClassification(t *testing.T) {
	statusHeader := http.Header{
		"Retry-After":           {"2"},
		"X-Typesafe-Request-Id": {"req-typesafe"},
		"Request-Id":            {"req-vendor"},
		"Set-Cookie":            {"session=madeupword"},
	}
	tests := map[string]struct {
		rt         *transport
		opts       []Option
		ctx        func(context.Context) (context.Context, context.CancelFunc)
		check      func(t *testing.T, err error)
		wantSeen   int
		inBubble   bool
		wantStatus int
	}{
		"error: a status with a blank body": {
			rt:       answer(503, statusHeader, " \n\t"),
			wantSeen: 1,
			check: func(t *testing.T, err error) {
				se, ok := errors.AsType[*llm.StatusError](err)
				if !ok {
					t.Fatalf("error = %T %v, want *llm.StatusError", err, err)
				}
				if se.StatusCode != http.StatusServiceUnavailable || se.Body == nil || len(se.Body) != 0 {
					t.Errorf("StatusError = %d, body %q (nil %v); want 503 with an empty body that is not nil", se.StatusCode, se.Body, se.Body == nil)
				}
				want := http.Header{"Retry-After": {"2"}, "X-Typesafe-Request-Id": {"req-typesafe"}}
				if diff := gocmp.Diff(want, se.Header); diff != "" {
					t.Errorf("kept headers (-want +got):\n%s", diff)
				}
			},
		},
		"error: a status with a JSON body": {
			rt:       answer(429, nil, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`),
			wantSeen: 1,
			check: func(t *testing.T, err error) {
				se, ok := errors.AsType[*llm.StatusError](err)
				if !ok || se.StatusCode != http.StatusTooManyRequests || string(se.Body) != `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}` {
					t.Errorf("error = %v, want a 429 *llm.StatusError with the body as received", err)
				}
			},
		},
		"error: a redirect is not followed": {
			rt:       answer(307, http.Header{"Location": {"https://elsewhere.example.test/v1/messages"}}, ""),
			wantSeen: 1,
			check: func(t *testing.T, err error) {
				if se, ok := errors.AsType[*llm.StatusError](err); !ok || se.StatusCode != http.StatusTemporaryRedirect {
					t.Errorf("error = %v, want a 307 *llm.StatusError", err)
				}
			},
		},
		"error: a connection failure": {
			rt: &transport{respond: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("connection refused by the test transport")
			}},
			wantSeen: 1,
			check: func(t *testing.T, err error) {
				if _, ok := errors.AsType[*llm.ConnectionError](err); !ok {
					t.Errorf("error = %T %v, want *llm.ConnectionError", err, err)
				}
			},
		},
		"error: the provider's timeout": {
			rt: &transport{respond: func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, req.Context().Err()
			}},
			opts:     []Option{WithTimeout(time.Second)},
			inBubble: true,
			wantSeen: 1,
			check: func(t *testing.T, err error) {
				if _, ok := errors.AsType[*llm.TimeoutError](err); !ok || !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("error = %T %v, want *llm.TimeoutError holding context.DeadlineExceeded", err, err)
				}
			},
		},
		"error: the caller's deadline": {
			rt: &transport{respond: func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, req.Context().Err()
			}},
			ctx: func(ctx context.Context) (context.Context, context.CancelFunc) {
				return context.WithTimeout(ctx, time.Second)
			},
			inBubble: true,
			wantSeen: 1,
			check: func(t *testing.T, err error) {
				if _, ok := errors.AsType[*llm.TimeoutError](err); !ok {
					t.Errorf("error = %T %v, want *llm.TimeoutError", err, err)
				}
			},
		},
		"error: a cancelled context": {
			rt: &transport{respond: func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, req.Context().Err()
			}},
			wantSeen: 1,
			ctx: func(ctx context.Context) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(ctx)
				cancel()
				return ctx, cancel
			},
			check: func(t *testing.T, err error) {
				if err != context.Canceled { //nolint:errorlint // the context's error itself, not one wrapping it.
					t.Errorf("error = %T %v, want context.Canceled itself", err, err)
				}
			},
		},
	}
	for name, tt := range tests {
		run := func(t *testing.T) {
			clearEnv(t)
			p, err := New("test-model", append([]Option{WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: tt.rt})}, tt.opts...)...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			ctx := t.Context()
			if tt.ctx != nil {
				var cancel context.CancelFunc
				ctx, cancel = tt.ctx(ctx)
				defer cancel()
			}
			req := structuredRequest()
			res, err := p.Do(ctx, req)
			if res != nil || err == nil {
				t.Fatalf("Do = %v, %v; want nil and an error", res, err)
			}
			tt.check(t, err)
			if n := len(tt.rt.requests()); n != tt.wantSeen {
				t.Errorf("the transport received %d requests, want %d", n, tt.wantSeen)
			}
			if req.Trace.Responded() {
				t.Errorf("a response body was recorded for a failed request")
			}
			if api := req.Trace.API(); api != "messages" {
				t.Errorf("Trace API = %q, want messages", api)
			}
		}
		t.Run(name, func(t *testing.T) {
			if tt.inBubble {
				synctest.Test(t, run)
				return
			}
			run(t)
		})
	}
}

// TestResult pins how Do reads a 2xx body, as upstream's _result does
// (providers/anthropic.py:55-69), and what it does with a body that is not
// a Messages response: the body is recorded with its stop_reason before
// anything is checked; max_tokens is the truncation non-answer and any
// stop_reason but end_turn, stop_sequence or null the non-answer naming
// it, both decided before the content and the usage are read; a body of
// another shape is a plain error whose text quotes nothing of it.
func TestResult(t *testing.T) {
	const usage = `"usage":{"input_tokens":12,"output_tokens":7}`
	const text = `"content":[{"type":"text","text":"hi"}]`
	str := func(s string) *string { return &s }
	tests := map[string]struct {
		body       string
		want       *llm.Result
		wantErr    error
		wantNon    string
		wantFinish *string
	}{
		"success: end_turn": {
			body: `{"stop_reason":"end_turn",` + text + `,` + usage + `}`, wantFinish: str("end_turn"),
			want: &llm.Result{Text: "hi", InputTokens: llm.Count{N: 12, Known: true}, OutputTokens: llm.Count{N: 7, Known: true}},
		},
		"success: stop_sequence": {
			body: `{"stop_reason":"stop_sequence",` + text + `,` + usage + `}`, wantFinish: str("stop_sequence"),
			want: &llm.Result{Text: "hi", InputTokens: llm.Count{N: 12, Known: true}, OutputTokens: llm.Count{N: 7, Known: true}},
		},
		"success: null stop_reason": {
			body: `{"stop_reason":null,` + text + `,` + usage + `}`,
			want: &llm.Result{Text: "hi", InputTokens: llm.Count{N: 12, Known: true}, OutputTokens: llm.Count{N: 7, Known: true}},
		},
		"success: absent stop_reason": {
			body: `{` + text + `,` + usage + `}`,
			want: &llm.Result{Text: "hi", InputTokens: llm.Count{N: 12, Known: true}, OutputTokens: llm.Count{N: 7, Known: true}},
		},
		"success: no text block": {
			body: `{"stop_reason":"end_turn","content":[{"type":"thinking","thinking":"x"}],` + usage + `}`, wantFinish: str("end_turn"),
			want: &llm.Result{InputTokens: llm.Count{N: 12, Known: true}, OutputTokens: llm.Count{N: 7, Known: true}},
		},
		"success: the largest counts and minus zero": {
			body: `{"stop_reason":"end_turn",` + text + `,"usage":{"input_tokens":18446744073709551615,"output_tokens":-0}}`, wantFinish: str("end_turn"),
			want: &llm.Result{Text: "hi", InputTokens: llm.Count{N: 18446744073709551615, Known: true}, OutputTokens: llm.Count{Known: true}},
		},
		"non-answer: max_tokens before the usage is read": {
			body: `{"stop_reason":"max_tokens",` + text + `}`, wantFinish: str("max_tokens"),
			wantNon: "Anthropic response was truncated at the output token limit. Increase max_tokens on AnthropicProvider or AsyncAnthropicProvider, or request fewer questions.",
		},
		"non-answer: refusal before the content is read": {
			body: `{"stop_reason":"refusal","content":5}`, wantFinish: str("refusal"),
			wantNon: "Anthropic response did not complete: refusal.",
		},
		"non-answer: an empty stop_reason": {
			body: `{"stop_reason":"",` + text + `,` + usage + `}`, wantFinish: str(""),
			wantNon: "Anthropic response did not complete: .",
		},
		"error: not JSON":      {body: `oops`, wantErr: errBodyNotJSON},
		"error: a NaN token":   {body: `{"stop_reason":"end_turn","x":NaN}`, wantErr: errBodyNotJSON},
		"error: an array":      {body: `[1]`, wantErr: errBodyNotJSON},
		"error: an empty body": {body: ``, wantErr: errBodyNotJSON},
		"non-answer: an integer stop_reason is formatted as its digits": {
			body: `{"stop_reason":5,` + text + `,` + usage + `}`, wantFinish: str("5"),
			wantNon: "Anthropic response did not complete: 5.",
		},
		"non-answer: a stop_reason of minus zero is 0": {
			body: `{"stop_reason":-0,` + text + `,` + usage + `}`, wantFinish: str("0"),
			wantNon: "Anthropic response did not complete: 0.",
		},
		"error: stop_reason a fraction": {body: `{"stop_reason":5.0,` + text + `,` + usage + `}`, wantErr: errStopReasonKind},
		"error: stop_reason a boolean":  {body: `{"stop_reason":true,` + text + `,` + usage + `}`, wantErr: errStopReasonKind},
		"error: content absent":         {body: `{"stop_reason":"end_turn",` + usage + `}`, wantErr: errContentKind, wantFinish: str("end_turn")},
		"error: content an object":      {body: `{"stop_reason":"end_turn","content":{},` + usage + `}`, wantErr: errContentKind, wantFinish: str("end_turn")},
		"error: a block not an object":  {body: `{"stop_reason":"end_turn","content":["hi"],` + usage + `}`, wantErr: errBlockKind, wantFinish: str("end_turn")},
		"error: a block without type":   {body: `{"stop_reason":"end_turn","content":[{"text":"hi"}],` + usage + `}`, wantErr: errBlockKind, wantFinish: str("end_turn")},
		"error: a block type not a string": {
			body: `{"stop_reason":"end_turn","content":[{"type":1,"text":"hi"}],` + usage + `}`, wantErr: errBlockKind, wantFinish: str("end_turn"),
		},
		"error: a text block's text not a string": {
			body: `{"stop_reason":"end_turn","content":[{"type":"text","text":null}],` + usage + `}`, wantErr: errTextKind, wantFinish: str("end_turn"),
		},
		"error: usage absent": {body: `{"stop_reason":"end_turn",` + text + `}`, wantErr: errUsageKind, wantFinish: str("end_turn")},
		"error: usage null":   {body: `{"stop_reason":"end_turn",` + text + `,"usage":null}`, wantErr: errUsageKind, wantFinish: str("end_turn")},
		"error: usage a number": {
			body: `{"stop_reason":"end_turn",` + text + `,"usage":5}`, wantErr: errUsageKind, wantFinish: str("end_turn"),
		},
		"success: input_tokens absent is unknown": {
			body: `{"stop_reason":"end_turn",` + text + `,"usage":{"output_tokens":7}}`, wantFinish: str("end_turn"),
			want: &llm.Result{Text: "hi", OutputTokens: llm.Count{N: 7, Known: true}},
		},
		"success: input_tokens null is unknown": {
			body: `{"stop_reason":"end_turn",` + text + `,"usage":{"input_tokens":null,"output_tokens":7}}`, wantFinish: str("end_turn"),
			want: &llm.Result{Text: "hi", OutputTokens: llm.Count{N: 7, Known: true}},
		},
		"success: an empty usage gives unknown counts": {
			body: `{"stop_reason":"end_turn",` + text + `,"usage":{}}`, wantFinish: str("end_turn"),
			want: &llm.Result{Text: "hi"},
		},
		"error: input_tokens negative": {
			body: `{"stop_reason":"end_turn",` + text + `,"usage":{"input_tokens":-1,"output_tokens":7}}`, wantErr: errInputTokensKind, wantFinish: str("end_turn"),
		},
		"error: input_tokens a fraction": {
			body: `{"stop_reason":"end_turn",` + text + `,"usage":{"input_tokens":1.5,"output_tokens":7}}`, wantErr: errInputTokensKind, wantFinish: str("end_turn"),
		},
		"error: input_tokens with an exponent": {
			body: `{"stop_reason":"end_turn",` + text + `,"usage":{"input_tokens":1e2,"output_tokens":7}}`, wantErr: errInputTokensKind, wantFinish: str("end_turn"),
		},
		"error: input_tokens past 2^64-1": {
			body: `{"stop_reason":"end_turn",` + text + `,"usage":{"input_tokens":18446744073709551616,"output_tokens":7}}`, wantErr: errInputTokensKind, wantFinish: str("end_turn"),
		},
		"error: input_tokens a string": {
			body: `{"stop_reason":"end_turn",` + text + `,"usage":{"input_tokens":"12","output_tokens":7}}`, wantErr: errInputTokensKind, wantFinish: str("end_turn"),
		},
		"success: output_tokens absent is unknown": {
			body: `{"stop_reason":"end_turn",` + text + `,"usage":{"input_tokens":12}}`, wantFinish: str("end_turn"),
			want: &llm.Result{Text: "hi", InputTokens: llm.Count{N: 12, Known: true}},
		},
		"success: output_tokens null is unknown": {
			body: `{"stop_reason":"end_turn",` + text + `,"usage":{"input_tokens":12,"output_tokens":null}}`, wantFinish: str("end_turn"),
			want: &llm.Result{Text: "hi", InputTokens: llm.Count{N: 12, Known: true}},
		},
		"error: output_tokens negative": {
			body: `{"stop_reason":"end_turn",` + text + `,"usage":{"input_tokens":12,"output_tokens":-7}}`, wantErr: errOutputTokensKind, wantFinish: str("end_turn"),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			p, err := New("test-model", WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: answer(200, nil, tt.body)}))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			req := structuredRequest()
			got, err := p.Do(t.Context(), req)
			switch {
			case tt.wantNon != "":
				na, ok := errors.AsType[*llm.NonAnswerError](err)
				if !ok || got != nil {
					t.Fatalf("Do = %v, %T %v; want an *llm.NonAnswerError", got, err, err)
				}
				if diff := gocmp.Diff(tt.wantNon, na.Message); diff != "" {
					t.Errorf("non-answer text (-want +got):\n%s", diff)
				}
			case tt.wantErr != nil:
				if err != tt.wantErr || got != nil { //nolint:errorlint // the package's own error value itself.
					t.Fatalf("Do = %v, %v; want %v", got, err, tt.wantErr)
				}
				for _, typed := range []bool{
					errors.As(err, new(*llm.StatusError)), errors.As(err, new(*llm.TimeoutError)),
					errors.As(err, new(*llm.ConnectionError)), errors.As(err, new(*llm.NonAnswerError)),
				} {
					if typed {
						t.Errorf("error %v is one of package llm's types; want a plain error", err)
					}
				}
			default:
				if err != nil {
					t.Fatalf("Do: %v", err)
				}
				if diff := gocmp.Diff(tt.want, got); diff != "" {
					t.Errorf("result (-want +got):\n%s", diff)
				}
			}
			body, ok := req.Trace.Response()
			if !ok || string(body) != tt.body {
				t.Errorf("recorded response = %q (recorded %v), want the body as received", body, ok)
			}
			if diff := gocmp.Diff(tt.wantFinish, req.Trace.FinishReason()); diff != "" {
				t.Errorf("recorded finish reason (-want +got):\n%s", diff)
			}
		})
	}
}

// TestDoRecordsTheExchange pins what Do records in the Trace for a request
// that is answered: the API name messages, the request body byte for byte
// as the transport received it, and the response body; and that a nil
// Trace records nothing and fails nothing.
func TestDoRecordsTheExchange(t *testing.T) {
	clearEnv(t)
	tr := answer(200, nil, okBody)
	p, err := New("test-model", WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: tr}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := structuredRequest()
	if _, err := p.Do(t.Context(), req); err != nil {
		t.Fatalf("Do: %v", err)
	}
	sent, ok := req.Trace.Request()
	if !ok || !bytes.Equal(sent, tr.requests()[0].body) {
		t.Errorf("recorded request = %q, want the body the transport received, %q", sent, tr.requests()[0].body)
	}
	if api := req.Trace.API(); api != "messages" {
		t.Errorf("Trace API = %q, want messages", api)
	}
	if got, _ := req.Trace.Response(); string(got) != okBody {
		t.Errorf("recorded response = %q, want %q", got, okBody)
	}
	untraced := structuredRequest()
	untraced.Trace = nil
	res, err := p.Do(t.Context(), untraced)
	if err != nil || res.Text != `{"answers":{"positive":true}}` {
		t.Errorf("Do without a Trace = %v, %v", res, err)
	}
}

// TestModelAndClose pins that Model is the model string New was given,
// whatever the base URL holds (with a userinfo and a query), and that Close
// returns nil for an owned and for a borrowed client.
func TestModelAndClose(t *testing.T) {
	tests := map[string]struct {
		opts []Option
	}{
		"success: an owned client":   {opts: []Option{WithAPIKey("not-a-key"), WithBaseURL("https://alice:madeupword@api.example.test/p?tenant=one")}},
		"success: a borrowed client": {opts: []Option{WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: answer(200, nil, okBody)})}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			p, err := New("claude:model:with:colons", tt.opts...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := p.Model(); got != "claude:model:with:colons" {
				t.Errorf("Model = %q, want the model string New was given", got)
			}
			if err := p.Close(); err != nil {
				t.Errorf("Close = %v, want nil", err)
			}
		})
	}
}

// TestProviderPrintsNoCredential pins what a caller sees when it prints a
// Provider: the model and nothing else. The fmt verbs %v, %+v, %s and %#v
// of the value and of the pointer, and slog's text and JSON handlers given
// both with slog.Any, hold no byte of the API key, the auth token or the
// base URL's user, password and query, each made at run time, which fmt's
// field-by-field form of the struct would print from the headers. A nil
// pointer prints as <nil>.
func TestProviderPrintsNoCredential(t *testing.T) {
	clearEnv(t)
	planted := map[string]string{"key": canary(t), "token": canary(t), "user": canary(t), "password": canary(t), "query": canary(t)}
	p, err := New("test-model", WithAPIKey(planted["key"]), WithAuthToken(planted["token"]), WithBaseURL("https://"+planted["user"]+":"+planted["password"]+"@api.example.test/p?tenant="+planted["query"]))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	const (
		short    = "anthropic.Provider(test-model)"
		goSyntax = "anthropic.Provider{Model:test-model}"
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

	tests := map[string]struct {
		v    any
		want map[string]string // by verb
	}{
		"success: value prints the model only": {
			v:    *p,
			want: map[string]string{"%v": short, "%+v": short, "%s": short, "%#v": goSyntax},
		},
		"success: pointer prints the model only": {
			v:    p,
			want: map[string]string{"%v": short, "%+v": short, "%s": short, "%#v": goSyntax},
		},
		"success: nil pointer prints <nil>": {
			v:    (*Provider)(nil),
			want: map[string]string{"%v": nilText, "%+v": nilText, "%s": nilText, "%#v": nilText},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			for verb, want := range tt.want {
				got := fmt.Sprintf(verb, tt.v)
				if diff := gocmp.Diff(want, got); diff != "" {
					t.Errorf("%s mismatch (-want +got):\n%s", verb, diff)
				}
				checkPlanted(t, verb, got)
			}
		})
	}

	handlers := map[string]struct {
		handler func(io.Writer) slog.Handler
		want    string // a part of the record, "" for none
	}{
		"success: slog text handler logs the model only": {
			handler: func(w io.Writer) slog.Handler { return slog.NewTextHandler(w, nil) },
			want:    " pointer=" + short + " value=" + short + "\n",
		},
		"success: slog JSON handler logs no credential": {
			handler: func(w io.Writer) slog.Handler { return slog.NewJSONHandler(w, nil) },
		},
	}
	for name, tt := range handlers {
		t.Run(name, func(t *testing.T) {
			var buf strings.Builder
			slog.New(tt.handler(&buf)).Info("provider", slog.Any("pointer", p), slog.Any("value", *p))
			out := buf.String()
			if !strings.Contains(out, tt.want) {
				t.Errorf("the record does not hold %q", tt.want)
			}
			checkPlanted(t, "the record", out)
		})
	}
}
