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

package openai

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// testKey is the key the tests give a Provider: a plain word, not a
// credential.
const testKey = "not-a-key"

// envNames are the variables New reads.
var envNames = [...]string{envAPIKey, envBaseURL, envOrg, envProject}

// clearEnv unsets the variables New reads for the rest of the test, so that
// the environment the tests run in reaches no Provider; t.Setenv restores
// each one when the test ends.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range envNames {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unsetting %s: %v", name, err)
		}
	}
}

// rtFunc is an http.RoundTripper of a test.
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// sent is one request a recorder received.
type sent struct {
	url    string
	header http.Header
	body   []byte
}

// recorder is a transport that records every request and answers each with
// status and body, or with err when err is not nil.
type recorder struct {
	status int
	header http.Header
	body   string
	err    error

	mu   sync.Mutex
	reqs []sent
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		body = b
		_ = req.Body.Close()
	}
	r.mu.Lock()
	r.reqs = append(r.reqs, sent{url: req.URL.String(), header: req.Header.Clone(), body: body})
	r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	return response(req, r.status, r.header, r.body), nil
}

// requests returns what r received so far.
func (r *recorder) requests() []sent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sent(nil), r.reqs...)
}

// response returns a response to req with status, header and body; a status
// of 0 is 200.
func response(req *http.Request, status int, header http.Header, body string) *http.Response {
	if status == 0 {
		status = http.StatusOK
	}
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		Status:        http.StatusText(status),
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

// newProvider returns a Provider for test-model over rt with the test key,
// the environment cleared first, and opts after those.
func newProvider(t *testing.T, rt http.RoundTripper, opts ...Option) *Provider {
	t.Helper()
	clearEnv(t)
	p, err := New("test-model", append([]Option{WithHTTPClient(&http.Client{Transport: rt}), WithAPIKey(testKey)}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

// chatAnswer is a Chat Completions body that answers with text "{}".
const chatAnswer = `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{}"}}]}`

// responsesAnswer is a Responses body that answers with text "{}".
const responsesAnswer = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{}"}]}]}`

// request is a request of two messages in prompted mode.
func request() *llm.Request {
	return &llm.Request{
		Messages: []llm.Message{{Role: "system", Content: "system prompt"}, {Role: "user", Content: "the document"}},
		Schema:   []byte(`{"type":"object"}`),
		Trace:    new(llm.Trace),
	}
}

// TestEnvironmentBaseURLSelectsChat ports
// tests/test_openai_transports.py::test_custom_endpoint_from_environment_defaults_to_chat:
// a base URL from OPENAI_BASE_URL whose host is not api.openai.com selects
// Chat Completions.
func TestEnvironmentBaseURLSelectsChat(t *testing.T) {
	clearEnv(t)
	t.Setenv(envBaseURL, "https://compatible.test/v1")
	t.Setenv(envAPIKey, testKey)
	p, err := New("test-model")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if got := p.API(); got != ChatCompletions {
		t.Errorf("API() = %d, want ChatCompletions (%d)", got, ChatCompletions)
	}
	if got := p.Model(); got != "test-model" {
		t.Errorf("Model() = %q, want %q", got, "test-model")
	}
}

// TestNewReadsTheEnvironmentOnce pins that New reads its four variables
// when it is called and Do reads none: values changed after New do not
// reach the request.
func TestNewReadsTheEnvironmentOnce(t *testing.T) {
	clearEnv(t)
	t.Setenv(envAPIKey, "first-word")
	t.Setenv(envBaseURL, "https://first.test/v1")
	t.Setenv(envOrg, "first-org")
	t.Setenv(envProject, "first-project")
	rec := &recorder{body: chatAnswer}
	p, err := New("test-model", WithHTTPClient(&http.Client{Transport: rec}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Setenv(envAPIKey, "second-word")
	t.Setenv(envBaseURL, "https://api.openai.com/v1")
	t.Setenv(envOrg, "second-org")
	t.Setenv(envProject, "second-project")
	if _, err := p.Do(t.Context(), request()); err != nil {
		t.Fatalf("Do: %v", err)
	}
	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("the transport received %d requests, want 1", len(reqs))
	}
	got := reqs[0]
	if got.url != "https://first.test/v1/chat/completions" {
		t.Errorf("URL = %q, want the base URL New read", got.url)
	}
	want := http.Header{
		"Authorization":       {"Bearer first-word"},
		"Content-Type":        {"application/json"},
		"Openai-Organization": {"first-org"},
		"Openai-Project":      {"first-project"},
	}
	if diff := gocmp.Diff(want, got.header); diff != "" {
		t.Errorf("header mismatch (-want +got):\n%s", diff)
	}
}

// TestNewRefuses pins every failure of New with its fixed text, which
// names the option or the variable and never a value.
func TestNewRefuses(t *testing.T) {
	const secret = "secret-word"
	tests := map[string]struct {
		env  map[string]string
		opts []Option
		want string
	}{
		"error: no key in the environment": {
			want: "openai: no API key: set OPENAI_API_KEY or pass WithAPIKey",
		},
		"error: OPENAI_API_KEY set and empty": {
			env:  map[string]string{envAPIKey: ""},
			want: "openai: no API key: set OPENAI_API_KEY or pass WithAPIKey",
		},
		"error: WithAPIKey empty without the variable": {
			opts: []Option{WithAPIKey("")},
			want: "openai: no API key: set OPENAI_API_KEY or pass WithAPIKey",
		},
		"error: WithAPIKey only white space without the variable": {
			opts: []Option{WithAPIKey(" \t\n ")},
			want: "openai: no API key: set OPENAI_API_KEY or pass WithAPIKey",
		},
		"error: OPENAI_API_KEY only white space": {
			env:  map[string]string{envAPIKey: "   "},
			opts: []Option{WithAPIKey("  ")},
			want: "openai: no API key: set OPENAI_API_KEY or pass WithAPIKey",
		},
		"error: OPENAI_BASE_URL not a URL": {
			env:  map[string]string{envAPIKey: testKey, envBaseURL: "::" + secret},
			want: "openai: the base URL of OPENAI_BASE_URL is not an absolute http or https URL with a host",
		},
		"error: OPENAI_BASE_URL of another scheme": {
			env:  map[string]string{envAPIKey: testKey, envBaseURL: "ftp://" + secret + "@files.test/v1"},
			want: "openai: the base URL of OPENAI_BASE_URL is not an absolute http or https URL with a host",
		},
		"error: WithBaseURL without a host": {
			opts: []Option{WithAPIKey(testKey), WithBaseURL("/v1?key=" + secret)},
			want: "openai: the base URL of WithBaseURL is not an absolute http or https URL with a host",
		},
		"error: WithBaseURL empty and OPENAI_BASE_URL not a URL": {
			env:  map[string]string{envAPIKey: testKey, envBaseURL: "notaurl"},
			opts: []Option{WithBaseURL("")},
			want: "openai: the base URL of OPENAI_BASE_URL is not an absolute http or https URL with a host",
		},
		"error: WithHTTPClient nil": {
			opts: []Option{WithAPIKey(testKey), WithHTTPClient(nil)},
			want: "openai: WithHTTPClient: the client is nil",
		},
		"error: WithTimeout zero": {
			opts: []Option{WithAPIKey(testKey), WithTimeout(0)},
			want: "openai: WithTimeout: the timeout is not positive",
		},
		"error: WithTimeout negative": {
			opts: []Option{WithAPIKey(testKey), WithTimeout(-time.Second)},
			want: "openai: WithTimeout: the timeout is not positive",
		},
		"error: WithAPI outside its constants": {
			opts: []Option{WithAPIKey(testKey), WithAPI(API(9))},
			want: "api must be 'responses' or 'chat_completions'",
		},
		"error: WithAPI outside its constants before a missing key": {
			opts: []Option{WithAPI(ChatCompletions + 1)},
			want: "api must be 'responses' or 'chat_completions'",
		},
		"error: WithPromptedResponseFormat outside its constants": {
			opts: []Option{WithAPIKey(testKey), WithPromptedResponseFormat(PromptedFormat(9))},
			want: "openai: WithPromptedResponseFormat: the format is neither FormatNull nor FormatOmit",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			p, err := New("test-model", tt.opts...)
			if err == nil {
				t.Fatalf("New() = %+v, nil; want the error %q", p, tt.want)
			}
			if p != nil {
				t.Errorf("New() returned a Provider with its error: %+v", p)
			}
			if got := err.Error(); got != tt.want {
				t.Errorf("error = %q, want %q", got, tt.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error %q holds the value it refused", err)
			}
		})
	}
}

// TestNewAcceptsTheDefaults pins the settings that are not errors: a base
// URL variable that is set and empty is none, so the default applies, and a
// positive WithTimeout and every API and format constant are accepted.
func TestNewAcceptsTheDefaults(t *testing.T) {
	tests := map[string]struct {
		env     map[string]string
		opts    []Option
		wantAPI API
	}{
		"success: OPENAI_BASE_URL set and empty is the default base URL": {
			env:     map[string]string{envAPIKey: testKey, envBaseURL: ""},
			wantAPI: Responses,
		},
		"success: WithBaseURL empty without the variable is the default base URL": {
			env:     map[string]string{envAPIKey: testKey},
			opts:    []Option{WithBaseURL("")},
			wantAPI: Responses,
		},
		"success: WithAPIKey without the variable": {
			opts:    []Option{WithAPIKey(testKey), WithTimeout(time.Nanosecond), WithPromptedResponseFormat(FormatOmit)},
			wantAPI: Responses,
		},
		"success: WithAPI Auto": {
			opts:    []Option{WithAPIKey(testKey), WithAPI(Auto), WithPromptedResponseFormat(FormatNull)},
			wantAPI: Responses,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			p, err := New("test-model", tt.opts...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			t.Cleanup(func() { _ = p.Close() })
			if got := p.API(); got != tt.wantAPI {
				t.Errorf("API() = %d, want %d", got, tt.wantAPI)
			}
			if got := p.endpoint.String(); got != "https://api.openai.com/v1/responses" {
				t.Errorf("endpoint = %q, want the default base URL's", got)
			}
		})
	}
}

// TestEmptyOptionIsNotGiven pins that an option given with the empty
// string counts as not given, so its variable applies as if the option were
// absent: the key, the base URL, the organization and the project. A key
// is trimmed first, from the option or the variable, so a key of white
// space is empty.
func TestEmptyOptionIsNotGiven(t *testing.T) {
	tests := map[string]struct {
		env     map[string]string
		opt     Option
		wantURL string
		want    http.Header
	}{
		"success: WithAPIKey empty": {
			env:     map[string]string{envAPIKey: "env-word"},
			opt:     WithAPIKey(""),
			wantURL: "https://compatible.test/v1/chat/completions",
			want:    http.Header{"Authorization": {"Bearer env-word"}, "Content-Type": {"application/json"}},
		},
		"success: WithAPIKey only white space": {
			env:     map[string]string{envAPIKey: "env-word"},
			opt:     WithAPIKey("  \t "),
			wantURL: "https://compatible.test/v1/chat/completions",
			want:    http.Header{"Authorization": {"Bearer env-word"}, "Content-Type": {"application/json"}},
		},
		"success: the key from WithAPIKey trimmed": {
			env:     map[string]string{envAPIKey: "env-word"},
			opt:     WithAPIKey(" option-word\n"),
			wantURL: "https://compatible.test/v1/chat/completions",
			want:    http.Header{"Authorization": {"Bearer option-word"}, "Content-Type": {"application/json"}},
		},
		"success: the key from OPENAI_API_KEY trimmed": {
			env:     map[string]string{envAPIKey: "\tenv-word \r\n"}, //nolint:gosec // G101: a made-up word around white space, not a credential.
			opt:     WithAPIKey(""),
			wantURL: "https://compatible.test/v1/chat/completions",
			want:    http.Header{"Authorization": {"Bearer env-word"}, "Content-Type": {"application/json"}},
		},
		"success: WithBaseURL empty": {
			env:     map[string]string{envAPIKey: testKey, envBaseURL: "https://env.test/v1"},
			opt:     WithBaseURL(""),
			wantURL: "https://env.test/v1/chat/completions",
			want:    http.Header{"Authorization": {"Bearer " + testKey}, "Content-Type": {"application/json"}},
		},
		"success: WithOrganization empty": {
			env:     map[string]string{envAPIKey: testKey, envOrg: "org-env"},
			opt:     WithOrganization(""),
			wantURL: "https://compatible.test/v1/chat/completions",
			want:    http.Header{"Authorization": {"Bearer " + testKey}, "Content-Type": {"application/json"}, "Openai-Organization": {"org-env"}},
		},
		"success: WithProject empty": {
			env:     map[string]string{envAPIKey: testKey, envProject: "project-env"},
			opt:     WithProject(""),
			wantURL: "https://compatible.test/v1/chat/completions",
			want:    http.Header{"Authorization": {"Bearer " + testKey}, "Content-Type": {"application/json"}, "Openai-Project": {"project-env"}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(envBaseURL, "https://compatible.test/v1")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			rec := &recorder{body: chatAnswer}
			p, err := New("test-model", WithHTTPClient(&http.Client{Transport: rec}), tt.opt)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := p.Do(t.Context(), request()); err != nil {
				t.Fatalf("Do: %v", err)
			}
			reqs := rec.requests()
			if len(reqs) != 1 {
				t.Fatalf("the transport received %d requests, want 1", len(reqs))
			}
			if reqs[0].url != tt.wantURL {
				t.Errorf("URL = %q, want %q", reqs[0].url, tt.wantURL)
			}
			if diff := gocmp.Diff(tt.want, reqs[0].header); diff != "" {
				t.Errorf("header mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestOptionsAreSafeToShare pins that one slice of Options can be applied
// by several New calls at once, as a factory closure shared by two Adapters
// does: no Option writes what it captured, which -race would report, and
// each Provider gets the settings the Options give.
func TestOptionsAreSafeToShare(t *testing.T) {
	clearEnv(t)
	const n = 8
	rec := &recorder{body: chatAnswer}
	opts := []Option{
		WithAPIKey("  shared-word \n"),
		WithBaseURL("https://compatible.test/v1"),
		WithOrganization("org"),
		WithProject("project"),
		WithAPI(ChatCompletions),
		WithPromptedResponseFormat(FormatOmit),
		WithHTTPClient(&http.Client{Transport: rec}),
		WithTimeout(time.Minute),
	}
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			<-start
			p, err := New("test-model", opts...)
			if err == nil {
				_, err = p.Do(t.Context(), request())
			}
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("New or Do with the shared Options: %v", err)
		}
	}
	reqs := rec.requests()
	if len(reqs) != n {
		t.Fatalf("the transport received %d requests, want %d", len(reqs), n)
	}
	want := http.Header{
		"Authorization":       {"Bearer shared-word"},
		"Content-Type":        {"application/json"},
		"Openai-Organization": {"org"},
		"Openai-Project":      {"project"},
	}
	for i, r := range reqs {
		if r.url != "https://compatible.test/v1/chat/completions" {
			t.Errorf("request %d: URL = %q", i, r.url)
		}
		if diff := gocmp.Diff(want, r.header); diff != "" {
			t.Errorf("request %d: header mismatch (-want +got):\n%s", i, diff)
		}
	}
}

// TestAPIChoice pins how Auto chooses the API, by the base URL's host name
// compared with api.openai.com without regard to case and without its
// port (providers/openai.py:122, where httpx gives the host in lower case),
// and that an explicit choice wins; Do then posts to that API's path.
func TestAPIChoice(t *testing.T) {
	tests := map[string]struct {
		base    string
		api     API
		wantAPI API
		wantURL string
	}{
		"success: the default base URL is Responses": {
			wantAPI: Responses,
			wantURL: "https://api.openai.com/v1/responses",
		},
		"success: api.openai.com is Responses": {
			base:    "https://api.openai.com/v1",
			wantAPI: Responses,
			wantURL: "https://api.openai.com/v1/responses",
		},
		"success: the host in upper case is Responses": {
			base:    "https://API.OPENAI.COM/v1",
			wantAPI: Responses,
			wantURL: "https://API.OPENAI.COM/v1/responses",
		},
		"success: the host with a port is Responses": {
			base:    "https://api.openai.com:443/v1",
			wantAPI: Responses,
			wantURL: "https://api.openai.com:443/v1/responses",
		},
		"success: a host that ends with api.openai.com is Chat Completions": {
			base:    "https://proxy.api.openai.com/v1",
			wantAPI: ChatCompletions,
			wantURL: "https://proxy.api.openai.com/v1/chat/completions",
		},
		"success: a host that starts with api.openai.com is Chat Completions": {
			base:    "https://api.openai.com.proxy.test/v1",
			wantAPI: ChatCompletions,
			wantURL: "https://api.openai.com.proxy.test/v1/chat/completions",
		},
		"success: the host with a trailing dot is Chat Completions": {
			base:    "https://api.openai.com./v1",
			wantAPI: ChatCompletions,
			wantURL: "https://api.openai.com./v1/chat/completions",
		},
		"success: another host is Chat Completions": {
			base:    "https://compatible.test/v1",
			wantAPI: ChatCompletions,
			wantURL: "https://compatible.test/v1/chat/completions",
		},
		"success: Responses chosen for another host": {
			base:    "https://proxy.test/v1",
			api:     Responses,
			wantAPI: Responses,
			wantURL: "https://proxy.test/v1/responses",
		},
		"success: Chat Completions chosen for api.openai.com": {
			api:     ChatCompletions,
			wantAPI: ChatCompletions,
			wantURL: "https://api.openai.com/v1/chat/completions",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := chatAnswer
			if tt.wantAPI == Responses {
				body = responsesAnswer
			}
			rec := &recorder{body: body}
			opts := []Option{WithAPI(tt.api)}
			if tt.base != "" {
				opts = append(opts, WithBaseURL(tt.base))
			}
			p := newProvider(t, rec, opts...)
			if got := p.API(); got != tt.wantAPI {
				t.Errorf("API() = %d, want %d", got, tt.wantAPI)
			}
			req := request()
			if _, err := p.Do(t.Context(), req); err != nil {
				t.Fatalf("Do: %v", err)
			}
			if reqs := rec.requests(); len(reqs) != 1 || reqs[0].url != tt.wantURL {
				t.Errorf("requests = %+v, want one to %s", reqs, tt.wantURL)
			}
			wantName := map[API]string{Responses: "responses", ChatCompletions: "chat_completions"}[tt.wantAPI]
			if got := req.Trace.API(); got != wantName {
				t.Errorf("the trace's api = %q, want %q", got, wantName)
			}
		})
	}
}

// TestEndpoint pins how the endpoint is built from the base URL: the
// operation's path after one slash whatever slashes the base path ends
// with, an escaped path segment kept as it is escaped, the query and the
// userinfo kept, the fragment dropped.
func TestEndpoint(t *testing.T) {
	tests := map[string]struct {
		base string
		want string
	}{
		"success: a path without a trailing slash": {
			base: "https://compatible.test/v1",
			want: "https://compatible.test/v1/chat/completions",
		},
		"success: a path with a trailing slash": {
			base: "https://compatible.test/v1/",
			want: "https://compatible.test/v1/chat/completions",
		},
		"success: a path with two trailing slashes": {
			base: "https://compatible.test/v1//",
			want: "https://compatible.test/v1/chat/completions",
		},
		"success: no path": {
			base: "https://compatible.test",
			want: "https://compatible.test/chat/completions",
		},
		"success: the root path": {
			base: "https://compatible.test/",
			want: "https://compatible.test/chat/completions",
		},
		"success: an escaped slash in a segment": {
			base: "https://compatible.test/a%2Fb/v1/",
			want: "https://compatible.test/a%2Fb/v1/chat/completions",
		},
		"success: the query is kept": {
			base: "https://compatible.test/v1?key=word&x=1",
			want: "https://compatible.test/v1/chat/completions?key=word&x=1",
		},
		"success: the fragment is dropped": {
			base: "https://compatible.test/v1#part",
			want: "https://compatible.test/v1/chat/completions",
		},
		"success: the userinfo is kept": { //nolint:gosec // G101: a made-up user and password the URL keeps.
			base: "http://user:word@compatible.test:8080/v1",
			want: "http://user:word@compatible.test:8080/v1/chat/completions",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{body: chatAnswer}
			p := newProvider(t, rec, WithBaseURL(tt.base))
			if _, err := p.Do(t.Context(), request()); err != nil {
				t.Fatalf("Do: %v", err)
			}
			if reqs := rec.requests(); len(reqs) != 1 || reqs[0].url != tt.want {
				t.Errorf("requests = %+v, want one to %s", reqs, tt.want)
			}
		})
	}
}

// TestOrganizationAndProjectHeaders pins OpenAI-Organization and
// OpenAI-Project: sent from the variable or the option, the option winning,
// and not sent when empty.
func TestOrganizationAndProjectHeaders(t *testing.T) {
	tests := map[string]struct {
		env         map[string]string
		opts        []Option
		wantOrg     []string
		wantProject []string
	}{
		"success: from the environment": {
			env:         map[string]string{envOrg: "org-env", envProject: "project-env"},
			wantOrg:     []string{"org-env"},
			wantProject: []string{"project-env"},
		},
		"success: the options win over the environment": {
			env:         map[string]string{envOrg: "org-env", envProject: "project-env"},
			opts:        []Option{WithOrganization("org-option"), WithProject("project-option")},
			wantOrg:     []string{"org-option"},
			wantProject: []string{"project-option"},
		},
		"success: empty options leave the variables in effect": {
			env:         map[string]string{envOrg: "org-env", envProject: "project-env"},
			opts:        []Option{WithOrganization(""), WithProject("")},
			wantOrg:     []string{"org-env"},
			wantProject: []string{"project-env"},
		},
		"success: empty variables send neither": {
			env: map[string]string{envOrg: "", envProject: ""},
		},
		"success: no variable sends neither": {},
		"success: an organization alone": {
			opts:    []Option{WithOrganization("org-option")},
			wantOrg: []string{"org-option"},
		},
		"success: a project alone": {
			env:         map[string]string{envProject: "project-env"},
			wantProject: []string{"project-env"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			rec := &recorder{body: chatAnswer}
			p, err := New("test-model", append([]Option{WithHTTPClient(&http.Client{Transport: rec}), WithAPIKey(testKey), WithAPI(ChatCompletions)}, tt.opts...)...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := p.Do(t.Context(), request()); err != nil {
				t.Fatalf("Do: %v", err)
			}
			reqs := rec.requests()
			if len(reqs) != 1 {
				t.Fatalf("the transport received %d requests, want 1", len(reqs))
			}
			h := reqs[0].header
			if diff := gocmp.Diff(tt.wantOrg, h.Values("OpenAI-Organization")); diff != "" {
				t.Errorf("OpenAI-Organization mismatch (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(tt.wantProject, h.Values("OpenAI-Project")); diff != "" {
				t.Errorf("OpenAI-Project mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRequestHeaders pins the whole header set a request carries at the
// transport for both APIs: Authorization: Bearer and Content-Type, plus
// the organization and project when set, and nothing else (net/http's own
// User-Agent and Accept-Encoding are written later, by http.Transport).
func TestRequestHeaders(t *testing.T) {
	tests := map[string]struct {
		opts []Option
		body string
		want http.Header
	}{
		"success: Chat Completions": {
			opts: []Option{WithAPI(ChatCompletions)},
			body: chatAnswer,
			want: http.Header{"Authorization": {"Bearer " + testKey}, "Content-Type": {"application/json"}},
		},
		"success: Responses": {
			opts: []Option{WithAPI(Responses)},
			body: responsesAnswer,
			want: http.Header{"Authorization": {"Bearer " + testKey}, "Content-Type": {"application/json"}},
		},
		"success: with an organization and a project": {
			opts: []Option{WithAPI(Responses), WithOrganization("org"), WithProject("project")},
			body: responsesAnswer,
			want: http.Header{
				"Authorization":       {"Bearer " + testKey},
				"Content-Type":        {"application/json"},
				"Openai-Organization": {"org"},
				"Openai-Project":      {"project"},
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{body: tt.body}
			p := newProvider(t, rec, tt.opts...)
			if _, err := p.Do(t.Context(), request()); err != nil {
				t.Fatalf("Do: %v", err)
			}
			reqs := rec.requests()
			if len(reqs) != 1 {
				t.Fatalf("the transport received %d requests, want 1", len(reqs))
			}
			if diff := gocmp.Diff(tt.want, reqs[0].header); diff != "" {
				t.Errorf("header mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// canary returns a key made at run time from the prefix ts-canary- and
// random hex digits, which no fixed text of the module holds.
func canary(t *testing.T) string {
	t.Helper()
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("crypto/rand: %v", err)
	}
	return "ts-canary-" + hex.EncodeToString(b)
}

// TestKeyTravelsOnlyInItsHeader pins that the key reaches the transport only
// in Authorization: Bearer, never in the URL (its path, query or
// userinfo), the body, another header, an error text or the trace, for
// both APIs and for a success, a status error, a non-answer and a
// connection failure. The base URL carries userinfo and a query: they reach
// the transport in the request's URL as they are, and net/http adds no
// Authorization: Basic header from the userinfo, since the request already
// carries Authorization.
func TestKeyTravelsOnlyInItsHeader(t *testing.T) {
	// origin is a made-up user and password before the host.
	const origin = "https://user:word@compatible.test" //nolint:gosec // G101: a made-up user and password the URL keeps.
	const base = origin + "/v1?q=1"
	tests := map[string]struct {
		api     API
		rec     *recorder
		wantErr bool
		wantURL string
	}{
		"success: Chat Completions": {
			api:     ChatCompletions,
			rec:     &recorder{body: chatAnswer},
			wantURL: origin + "/v1/chat/completions?q=1",
		},
		"success: Responses": {
			api:     Responses,
			rec:     &recorder{body: responsesAnswer},
			wantURL: origin + "/v1/responses?q=1",
		},
		"error: a status error": {
			api:     ChatCompletions,
			rec:     &recorder{status: http.StatusUnauthorized, body: `{"error":{"message":"Incorrect API key provided."}}`},
			wantErr: true,
			wantURL: origin + "/v1/chat/completions?q=1",
		},
		"error: a non-answer": {
			api:     Responses,
			rec:     &recorder{body: `{"status":"failed","error":{"message":"generation failed"}}`},
			wantErr: true,
			wantURL: origin + "/v1/responses?q=1",
		},
		"error: a connection failure": {
			api:     Responses,
			rec:     &recorder{err: errors.New("made-up transport failure")},
			wantErr: true,
			wantURL: origin + "/v1/responses?q=1",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			key := canary(t)
			clearEnv(t)
			p, err := New("test-model", WithHTTPClient(&http.Client{Transport: tt.rec}), WithAPIKey(key), WithBaseURL(base), WithAPI(tt.api))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			req := request()
			res, err := p.Do(t.Context(), req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Do() = %+v, %v; want an error: %t", res, err, tt.wantErr)
			}
			if err != nil {
				for _, text := range []string{err.Error(), strings.Join(errorChain(err), " | ")} {
					if strings.Contains(text, key) {
						t.Errorf("the error text holds the key: %q", text)
					}
				}
			}
			reqs := tt.rec.requests()
			if len(reqs) != 1 {
				t.Fatalf("the transport received %d requests, want 1", len(reqs))
			}
			got := reqs[0]
			if got.url != tt.wantURL {
				t.Errorf("URL = %q, want %q", got.url, tt.wantURL)
			}
			if strings.Contains(got.url, key) {
				t.Errorf("the URL holds the key: %q", got.url)
			}
			if bytes.Contains(got.body, []byte(key)) {
				t.Errorf("the body holds the key: %s", got.body)
			}
			if v := got.header.Values("Authorization"); len(v) != 1 || v[0] != "Bearer "+key {
				t.Errorf("Authorization = %q, want only the Bearer key", v)
			}
			for name, values := range got.header {
				if name == "Authorization" {
					continue
				}
				for _, v := range values {
					if strings.Contains(v, key) {
						t.Errorf("header %s holds the key: %q", name, v)
					}
				}
			}
			reqBody, _ := req.Trace.Request()
			respBody, _ := req.Trace.Response()
			if bytes.Contains(reqBody, []byte(key)) || bytes.Contains(respBody, []byte(key)) {
				t.Errorf("the trace holds the key: request %s, response %s", reqBody, respBody)
			}
		})
	}
}

// errorChain returns the text of every error in err's chain.
func errorChain(err error) []string {
	var texts []string
	for ; err != nil; err = errors.Unwrap(err) {
		texts = append(texts, err.Error())
	}
	return texts
}

// TestProviderErrorClassification pins that a failure of the request
// reaches the Adapter as the type of package llm it classifies: a status as
// an *llm.StatusError that keeps the body (empty and not nil for a blank
// one, as OpenAI's Python SDK hands over the empty string) and only the
// response headers a retry reads; a redirect as a status error with no
// request sent to its target; a timeout as an *llm.TimeoutError; a failure
// without a response as an *llm.ConnectionError; a cancellation as the
// context's own error; and a 2xx body that is no response of the API as a
// plain error, none of those types.
func TestProviderErrorClassification(t *testing.T) {
	t.Run("error: a status with a blank body", func(t *testing.T) {
		header := http.Header{"Retry-After": {"7"}, "X-Request-Id": {"req_vendor"}, "X-Typesafe-Request-Id": {"adp_1"}, "Set-Cookie": {"c=1"}}
		rec := &recorder{status: http.StatusServiceUnavailable, header: header, body: " \n\t "}
		p := newProvider(t, rec, WithAPI(ChatCompletions))
		_, err := p.Do(t.Context(), request())
		var se *llm.StatusError
		if !errors.As(err, &se) {
			t.Fatalf("Do() error = %v (%T), want an *llm.StatusError", err, err)
		}
		if se.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("StatusCode = %d, want 503", se.StatusCode)
		}
		if se.Body == nil || len(se.Body) != 0 {
			t.Errorf("Body = %#v, want empty and not nil", se.Body)
		}
		want := http.Header{"Retry-After": {"7"}, "X-Typesafe-Request-Id": {"adp_1"}}
		if diff := gocmp.Diff(want, se.Header); diff != "" {
			t.Errorf("Header mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("error: a status with a body", func(t *testing.T) {
		const body = `{"error":{"message":"bad request","type":"invalid_request_error"}}`
		rec := &recorder{status: http.StatusBadRequest, body: body}
		p := newProvider(t, rec, WithAPI(Responses))
		_, err := p.Do(t.Context(), request())
		var se *llm.StatusError
		if !errors.As(err, &se) {
			t.Fatalf("Do() error = %v (%T), want an *llm.StatusError", err, err)
		}
		if se.StatusCode != http.StatusBadRequest || string(se.Body) != body {
			t.Errorf("StatusError = %d %q, want 400 %q", se.StatusCode, se.Body, body)
		}
	})

	t.Run("error: a redirect is not followed", func(t *testing.T) {
		var calls atomic.Int64
		rt := rtFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			if r.URL.Host != "compatible.test" {
				t.Errorf("a request went to the redirect's target: %s", r.URL)
			}
			return response(r, http.StatusTemporaryRedirect, http.Header{"Location": {"https://elsewhere.test/v1/chat/completions"}}, ""), nil
		})
		p := newProvider(t, rt, WithBaseURL("https://compatible.test/v1"))
		_, err := p.Do(t.Context(), request())
		var se *llm.StatusError
		if !errors.As(err, &se) || se.StatusCode != http.StatusTemporaryRedirect {
			t.Fatalf("Do() error = %v, want an *llm.StatusError of status 307", err)
		}
		if n := calls.Load(); n != 1 {
			t.Errorf("the transport received %d requests, want 1", n)
		}
	})

	t.Run("error: a timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rt := rtFunc(func(r *http.Request) (*http.Response, error) {
				<-r.Context().Done()
				return nil, r.Context().Err()
			})
			p := newProvider(t, rt, WithTimeout(time.Second))
			start := time.Now()
			_, err := p.Do(t.Context(), request())
			if _, ok := errors.AsType[*llm.TimeoutError](err); !ok {
				t.Fatalf("Do() error = %v (%T), want an *llm.TimeoutError", err, err)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("errors.Is(%v, context.DeadlineExceeded) = false", err)
			}
			if d := time.Since(start); d != time.Second {
				t.Errorf("the request ended after %v, want the timeout of 1s", d)
			}
		})
	})

	t.Run("error: a connection failure", func(t *testing.T) {
		rec := &recorder{err: errors.New("made-up connection refused")}
		p := newProvider(t, rec)
		_, err := p.Do(t.Context(), request())
		if _, ok := errors.AsType[*llm.ConnectionError](err); !ok {
			t.Fatalf("Do() error = %v (%T), want an *llm.ConnectionError", err, err)
		}
	})

	t.Run("error: a cancellation is the context's own error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		rt := rtFunc(func(r *http.Request) (*http.Response, error) {
			cancel()
			<-r.Context().Done()
			return nil, r.Context().Err()
		})
		p := newProvider(t, rt)
		_, err := p.Do(ctx, request())
		if err != context.Canceled { //nolint:errorlint // the context's own error value is returned, unwrapped.
			t.Errorf("Do() error = %v (%T), want context.Canceled itself", err, err)
		}
	})

	t.Run("error: a 2xx body that is no response is none of the llm types", func(t *testing.T) {
		for _, api := range []API{ChatCompletions, Responses} {
			rec := &recorder{body: "<html>not JSON</html>"}
			p := newProvider(t, rec, WithAPI(api))
			_, err := p.Do(t.Context(), request())
			if err == nil {
				t.Fatalf("API %d: Do() error = nil, want an error", api)
			}
			var (
				se *llm.StatusError
				te *llm.TimeoutError
				ce *llm.ConnectionError
				ne *llm.NonAnswerError
			)
			if errors.As(err, &se) || errors.As(err, &te) || errors.As(err, &ce) || errors.As(err, &ne) {
				t.Errorf("API %d: Do() error = %v (%T), want none of package llm's types", api, err, err)
			}
		}
	})
}

// TestRequestBodyCannotBeWritten pins that a schema that is not one JSON
// value fails Do in structured mode before anything is recorded or sent.
func TestRequestBodyCannotBeWritten(t *testing.T) {
	for name, api := range map[string]API{"error: Chat Completions": ChatCompletions, "error: Responses": Responses} {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{body: chatAnswer}
			p := newProvider(t, rec, WithAPI(api))
			req := request()
			req.Structured, req.Schema = true, []byte(`{"type":`)
			_, err := p.Do(t.Context(), req)
			if err == nil || !strings.HasPrefix(err.Error(), "openai: the request body could not be written: ") {
				t.Errorf("Do() error = %v, want the body error", err)
			}
			if n := len(rec.requests()); n != 0 {
				t.Errorf("the transport received %d requests, want 0", n)
			}
			if _, ok := req.Trace.Request(); ok {
				t.Error("the trace recorded a request")
			}
		})
	}
}

// TestDoWithoutTrace pins that a request without a trace is performed and
// records nothing, as upstream records nothing outside an attempt.
func TestDoWithoutTrace(t *testing.T) {
	for name, tt := range map[string]struct {
		api  API
		body string
	}{
		"success: Chat Completions": {api: ChatCompletions, body: chatAnswer},
		"success: Responses":        {api: Responses, body: responsesAnswer},
	} {
		t.Run(name, func(t *testing.T) {
			p := newProvider(t, &recorder{body: tt.body}, WithAPI(tt.api))
			req := request()
			req.Trace = nil
			res, err := p.Do(t.Context(), req)
			if err != nil || res.Text != "{}" {
				t.Errorf("Do() = %+v, %v; want the text {}", res, err)
			}
		})
	}
}

// closeCounter counts the CloseIdleConnections calls that reach it.
type closeCounter struct {
	http.RoundTripper
	closed atomic.Int64
}

func (c *closeCounter) CloseIdleConnections() { c.closed.Add(1) }

// TestClose pins that Close returns nil and leaves a borrowed client's idle
// connections alone, and that an owned Provider closes too.
func TestClose(t *testing.T) {
	counter := &closeCounter{RoundTripper: &recorder{body: chatAnswer}}
	p := newProvider(t, counter)
	if err := p.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	if n := counter.closed.Load(); n != 0 {
		t.Errorf("Close closed a borrowed client's idle connections %d times, want 0", n)
	}
	owned, err := New("test-model", WithAPIKey(testKey))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := owned.Close(); err != nil {
		t.Errorf("Close() of an owned Provider = %v, want nil", err)
	}
}
