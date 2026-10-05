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

package adapter

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter/anthropic"
	"github.com/zchee/decision-model-sdk-go/adapter/gemini"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
	"github.com/zchee/decision-model-sdk-go/adapter/openai"
)

// vendorKey is the key the tests give a real provider: a plain word, not
// a credential.
const vendorKey = "provider-plain-word"

// providerEnv are the environment variables the three providers read when
// they are built.
var providerEnv = [...]string{
	"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID",
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
	"GOOGLE_API_KEY", "GEMINI_API_KEY", "GOOGLE_GEMINI_BASE_URL",
}

// clearProviderEnv sets every variable of providerEnv to the empty string
// for the rest of the test, which each of the three providers takes as not
// set, so that the environment the tests run in reaches no provider.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, name := range providerEnv {
		t.Setenv(name, "")
	}
}

// realProviders build each of the module's three providers for the model
// test-model over client with key, and with baseURL when it is not empty,
// so that a test drives the provider's own request, HTTP call and response
// reading against a transport of the test's own.
var realProviders = map[string]func(client *http.Client, key, baseURL string) (llm.Provider, error){
	"openai": func(client *http.Client, key, baseURL string) (llm.Provider, error) {
		p, err := openai.New("test-model", openai.WithHTTPClient(client), openai.WithAPIKey(key), openai.WithBaseURL(baseURL))
		if err != nil {
			return nil, err
		}
		return p, nil
	},
	"anthropic": func(client *http.Client, key, baseURL string) (llm.Provider, error) {
		p, err := anthropic.New("test-model", anthropic.WithHTTPClient(client), anthropic.WithAPIKey(key), anthropic.WithBaseURL(baseURL))
		if err != nil {
			return nil, err
		}
		return p, nil
	},
	"gemini": func(client *http.Client, key, baseURL string) (llm.Provider, error) {
		p, err := gemini.New("test-model", gemini.WithHTTPClient(client), gemini.WithAPIKey(key), gemini.WithBaseURL(baseURL))
		if err != nil {
			return nil, err
		}
		return p, nil
	},
}

// buildRealProvider returns the provider name of realProviders over rt, the
// environment cleared first, closed when the test ends.
func buildRealProvider(t *testing.T, name string, rt http.RoundTripper, key, baseURL string) llm.Provider {
	t.Helper()
	clearProviderEnv(t)
	p, err := realProviders[name](&http.Client{Transport: rt}, key, baseURL)
	if err != nil {
		t.Fatalf("building the %s provider: %v", name, err)
	}
	t.Cleanup(func() {
		if c, ok := p.(interface{ Close() error }); ok {
			_ = c.Close()
		}
	})
	return p
}

// providerRequest is one request a providerTransport received.
type providerRequest struct {
	url    string
	header http.Header
	body   []byte
}

// providerTransport stands for a provider's server: it records the URL, a
// copy of the header and the body of each request, and answers the request
// numbered n from 0 with answer(req, n).
type providerTransport struct {
	answer func(req *http.Request, n int) (*http.Response, error)

	mu   sync.Mutex
	reqs []providerRequest
}

func (p *providerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	p.mu.Lock()
	n := len(p.reqs)
	p.reqs = append(p.reqs, providerRequest{url: req.URL.String(), header: req.Header.Clone(), body: body})
	p.mu.Unlock()
	return p.answer(req, n)
}

// requests returns the requests p received so far.
func (p *providerTransport) requests() []providerRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]providerRequest(nil), p.reqs...)
}

// providerResponse returns a provider's response to req with status and a
// JSON body.
func providerResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		Status: strconv.Itoa(status) + " " + http.StatusText(status), StatusCode: status,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// TestRetryPolicyControlsHTTPAttempts checks that the Adapter's retry
// policy alone decides how many HTTP requests a real provider sends. Each
// of the three providers, over a transport that answers every request 503
// with the message "unavailable", sends the policy's retries plus one
// requests; the caller of a client from NewClient gets that 503, after one
// attempt of the SDK, with a Report that holds one attempt per HTTP request,
// in order: its recorded request is the body that was sent, its response
// is null, and its error is TypeSafeInternalServerError with a text naming
// "unavailable".
//
// Upstream's test, tests/test_provider_retries.py::
// test_retry_policy_controls_http_attempts, has twelve cases: six provider
// classes, a synchronous and an asynchronous one for each vendor, times
// the retry budgets 0 and 1. This module has no asynchronous providers, so
// its six cases are the three providers times the same two budgets.
// Upstream switches the vendor SDKs' own retries off; these providers have
// none to switch off, since each call is one HTTP request, so the count of
// HTTP requests is the count of the Adapter's attempts.
func TestRetryPolicyControlsHTTPAttempts(t *testing.T) {
	tests := map[string]struct {
		provider string
		retries  int
	}{
		"openai, no retry":     {provider: "openai", retries: 0},
		"openai, one retry":    {provider: "openai", retries: 1},
		"anthropic, no retry":  {provider: "anthropic", retries: 0},
		"anthropic, one retry": {provider: "anthropic", retries: 1},
		"gemini, no retry":     {provider: "gemini", retries: 0},
		"gemini, one retry":    {provider: "gemini", retries: 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rt := &providerTransport{answer: func(req *http.Request, _ int) (*http.Response, error) {
				return providerResponse(req, http.StatusServiceUnavailable, `{"error":{"message":"unavailable"}}`), nil
			}}
			p := buildRealProvider(t, tt.provider, rt, vendorKey, "")
			// Upstream's RetryPolicy(max_retries=n, backoff_initial=0): the
			// default policy of typesafe-sdk-python with no wait.
			policy := DefaultRetry().MaxRetries(tt.retries).Backoff(0, 5*time.Second, 0.25)
			ad, err := New(Discrete, Prompted, WithProvider(tt.provider, p), WithDefaultModel(tt.provider), WithRetry(policy))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			c := sdkClient(t, ad, false)
			_, err = c.SystemOne(t.Context(), "A delightful book.", noulQuestions(t))
			ae, ok := errors.AsType[*decision.APIError](err)
			if !ok || ae.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("SystemOne error = %T %v, want a *decision.APIError of status 503", err, err)
			}
			if got := c.Stats().Attempts; got != 1 {
				t.Errorf("the SDK made %d attempts, want 1", got)
			}
			sent := rt.requests()
			if got, want := len(sent), tt.retries+1; got != want {
				t.Fatalf("the provider sent %d HTTP requests, want %d", got, want)
			}
			r, ok := ReportFromError(err)
			if !ok {
				t.Fatalf("ReportFromError found no Report in %v", err)
			}
			if got := len(r.Debug.Attempts); got != len(sent) {
				t.Fatalf("the Report has %d attempts for %d HTTP requests", got, len(sent))
			}
			for i, a := range r.Debug.Attempts {
				if equal, err := jsonx.Equal(a.Request, sent[i].body); err != nil || !equal {
					t.Errorf("attempt %d: the recorded request %s is not the body sent %s (%v)", i, a.Request, sent[i].body, err)
				}
				if a.Response != nil {
					t.Errorf("attempt %d: llm_response = %s, want null", i, a.Response)
				}
				if a.Info.ErrorType != "TypeSafeInternalServerError" || !strings.Contains(a.Info.Error, "unavailable") {
					t.Errorf("attempt %d: debug_info error %q of type %q, want TypeSafeInternalServerError naming unavailable", i, a.Info.Error, a.Info.ErrorType)
				}
			}
			if _, err := r.MarshalJSON(); err != nil {
				t.Errorf("the Report does not encode: %v", err)
			}
		})
	}
}
