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

package decision

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
)

// The provider variable of the tests' providers and the key it holds. The
// tests read a map through strictEnv, never the process environment; the
// name is kept apart from every real vendor's variable so that a strict
// getenv that allows it cannot also allow a real one.
const (
	testProviderKeyEnv = "DECISION_MODEL_TEST_PROVIDER_KEY"
	testProviderKey    = "sk-provider-test-key-0123456789"
)

// codivLike is a provider shaped as the provider package's Codiv preset:
// both paths, no default model. The root package's tests cannot import the
// provider package, which imports this one.
func codivLike() Provider {
	return Provider{
		Name:          "Codiv",
		BaseURL:       "https://api.codiv.ai",
		SystemOnePath: "/v1/systemone",
		ModelsPath:    "/v1/models",
		APIKeyEnv:     testProviderKeyEnv,
	}
}

// perplexityLike is a provider shaped as the provider package's Perplexity
// preset: its own System One path and no listing.
func perplexityLike() Provider {
	return Provider{
		Name:          "Perplexity",
		BaseURL:       "https://api.perplexity.ai",
		SystemOnePath: "/v1/decisions",
		APIKeyEnv:     testProviderKeyEnv,
	}
}

// strictEnv is a getenv over env that records every name it is asked for
// and fails the test, naming the variable, when it is asked for a name not
// in allowed. A variable a row does not allow is one the client must not
// read, whether or not env holds it.
type strictEnv struct {
	t       *testing.T
	env     map[string]string
	allowed []string

	mu   sync.Mutex
	read []string
}

func newStrictEnv(t *testing.T, env map[string]string, allowed ...string) *strictEnv {
	return &strictEnv{t: t, env: env, allowed: allowed}
}

func (e *strictEnv) getenv(name string) string {
	e.mu.Lock()
	e.read = append(e.read, name)
	e.mu.Unlock()
	if !slices.Contains(e.allowed, name) {
		e.t.Errorf("the client read the environment variable %s; it may read only %q", name, e.allowed)
	}
	return e.env[name]
}

// names returns the names read, in order.
func (e *strictEnv) names() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.read)
}

// providerClient builds a client from opts as NewClient does, against the
// environment getenv reads instead of the process's, and closes it when the
// test ends.
func providerClient(t *testing.T, getenv func(string) string, opts ...ClientOption) *Client {
	t.Helper()
	o := collectOptions(opts)
	cfg, err := o.resolve(getenv)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	c, err := buildClient(&o, cfg)
	if err != nil {
		t.Fatalf("buildClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// failingTransport fails the test when a request reaches it: the call must
// fail before anything is sent.
type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.t.Errorf("a request reached the transport: %s %s", req.Method, req.URL.Redacted())
	return nil, errors.New("no request may be sent")
}

// genericEnv holds every generic variable, each set to a value that differs
// from what a provider's client resolves, and the provider's variable.
var genericEnv = map[string]string{
	APIKeyEnv:          "generic-key-from-the-environment",
	BaseURLEnv:         "https://generic.example.test",
	DefaultModelEnv:    "generic-model",
	testProviderKeyEnv: "  " + testProviderKey + "\n",
}

// TestProviderPrecedence pins where a provider's client takes each setting
// from: the key from WithAPIKey, else the provider's variable; the base URL
// and the paths from the provider, or from WithBaseURL only beside
// WithAPIKey; the model from WithModel, else the provider's default model.
// Each row lists every variable the client may read, and the strict getenv
// fails the row on any other: DECISION_MODEL_API_KEY, DECISION_MODEL_BASE_URL
// and DECISION_MODEL_DEFAULT_MODEL are never read with a provider, although
// every row's environment sets them.
func TestProviderPrecedence(t *testing.T) {
	withDefaultModel := codivLike()
	withDefaultModel.DefaultModel = "gpt-6-luna"
	type want struct {
		Key, SystemOne, Models, Model string
	}
	tests := map[string]struct {
		provider Provider
		opts     []ClientOption
		env      map[string]string
		allowed  []string // the variables the client may read
		wantRead []string // the variables it reads, in order
		want     want
		wantErr  string
	}{
		"success: the key from the provider's variable, trimmed": {
			provider: codivLike(), env: genericEnv,
			allowed: []string{testProviderKeyEnv}, wantRead: []string{testProviderKeyEnv},
			want: want{Key: testProviderKey, SystemOne: "https://api.codiv.ai/v1/systemone", Models: "https://api.codiv.ai/v1/models"},
		},
		"success: WithAPIKey wins over the provider's variable, which is not read": {
			provider: codivLike(), env: genericEnv, opts: []ClientOption{WithAPIKey("explicit-key")},
			want: want{Key: "explicit-key", SystemOne: "https://api.codiv.ai/v1/systemone", Models: "https://api.codiv.ai/v1/models"},
		},
		"success: WithBaseURL beside WithAPIKey sends to the given URL under the provider's paths": {
			provider: perplexityLike(), env: genericEnv,
			opts: []ClientOption{WithAPIKey("explicit-key"), WithBaseURL("http://127.0.0.1:8080/gateway/")},
			want: want{Key: "explicit-key", SystemOne: "http://127.0.0.1:8080/gateway/v1/decisions"},
		},
		"success: the provider's default model": {
			provider: withDefaultModel, env: genericEnv,
			allowed: []string{testProviderKeyEnv}, wantRead: []string{testProviderKeyEnv},
			want: want{Key: testProviderKey, SystemOne: "https://api.codiv.ai/v1/systemone", Models: "https://api.codiv.ai/v1/models", Model: "gpt-6-luna"},
		},
		"success: WithModel wins over the provider's default model": {
			provider: withDefaultModel, env: genericEnv, opts: []ClientOption{WithModel("x")},
			allowed: []string{testProviderKeyEnv}, wantRead: []string{testProviderKeyEnv},
			want: want{Key: testProviderKey, SystemOne: "https://api.codiv.ai/v1/systemone", Models: "https://api.codiv.ai/v1/models", Model: "x"},
		},
		"success: no model without WithModel or a default model": {
			provider: codivLike(), env: genericEnv,
			allowed: []string{testProviderKeyEnv}, wantRead: []string{testProviderKeyEnv},
			want: want{Key: testProviderKey, SystemOne: "https://api.codiv.ai/v1/systemone", Models: "https://api.codiv.ai/v1/models"},
		},
		"success: no ModelsPath, no models URL": {
			provider: perplexityLike(), env: genericEnv,
			allowed: []string{testProviderKeyEnv}, wantRead: []string{testProviderKeyEnv},
			want: want{Key: testProviderKey, SystemOne: "https://api.perplexity.ai/v1/decisions"},
		},
		"success: the provider's base URL loses a trailing slash and a default port": {
			provider: func() Provider { p := codivLike(); p.BaseURL = "https://api.codiv.ai:443/"; return p }(),
			env:      genericEnv, allowed: []string{testProviderKeyEnv}, wantRead: []string{testProviderKeyEnv},
			want: want{Key: testProviderKey, SystemOne: "https://api.codiv.ai/v1/systemone", Models: "https://api.codiv.ai/v1/models"},
		},
		"error: WithBaseURL without WithAPIKey, before any variable is read": {
			provider: codivLike(), env: genericEnv, opts: []ClientOption{WithBaseURL("https://elsewhere.example.test")},
			wantErr: "WithBaseURL and WithProvider can be used together only with WithAPIKey: the key in the " + testProviderKeyEnv +
				" environment variable is sent to the provider Codiv's base URL alone.",
		},
		"error: the provider's variable unset": {
			provider: codivLike(), env: map[string]string{APIKeyEnv: "generic-key"},
			allowed: []string{testProviderKeyEnv}, wantRead: []string{testProviderKeyEnv},
			wantErr: "No API key was provided. Pass WithAPIKey or set the " + testProviderKeyEnv + " environment variable.",
		},
		"error: the provider's variable blank": {
			provider: codivLike(), env: map[string]string{testProviderKeyEnv: " \t\n"},
			allowed: []string{testProviderKeyEnv}, wantRead: []string{testProviderKeyEnv},
			wantErr: "No API key was provided. Pass WithAPIKey or set the " + testProviderKeyEnv + " environment variable.",
		},
		"error: the provider's variable holds a space": {
			provider: codivLike(), env: map[string]string{testProviderKeyEnv: "sk-two words"},
			allowed: []string{testProviderKeyEnv}, wantRead: []string{testProviderKeyEnv},
			wantErr: "The API key in the " + testProviderKeyEnv + " environment variable must contain only printable ASCII characters without whitespace.",
		},
		"error: an empty WithAPIKey names the provider's variable": {
			provider: codivLike(), env: genericEnv, opts: []ClientOption{WithAPIKey(" ")},
			wantErr: "The API key passed to WithAPIKey is empty; the " + testProviderKeyEnv + " environment variable is not read when WithAPIKey is given.",
		},
		"error: an empty WithModel names the provider's default model": {
			provider: codivLike(), env: genericEnv, opts: []ClientOption{WithModel("")},
			allowed: []string{testProviderKeyEnv}, wantRead: []string{testProviderKeyEnv},
			wantErr: "The model passed to WithModel is empty; leave WithModel out to use the provider's default model, or name the model on each call with Model.",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			env := newStrictEnv(t, tt.env, tt.allowed...)
			opts := append([]ClientOption{WithProvider(tt.provider)}, tt.opts...)
			if tt.wantErr != "" {
				err := resolveError(t, env.getenv, opts...)
				if got := err.Error(); got != tt.wantErr {
					t.Errorf("Error() = %q, want %q", got, tt.wantErr)
				}
				if strings.Contains(err.Error(), testProviderKey) {
					t.Errorf("Error() repeats the key: %q", err.Error())
				}
			} else {
				c := mustResolve(t, env.getenv, opts...)
				got := want{Key: c.APIKey, SystemOne: c.SystemOneURL.String(), Model: c.Model}
				if c.ModelsURL != nil {
					got.Models = c.ModelsURL.String()
				}
				if diff := gocmp.Diff(tt.want, got); diff != "" {
					t.Errorf("resolved settings (-want +got):\n%s", diff)
				}
				if c.Provider != tt.provider.Name {
					t.Errorf("config's provider = %q, want %q", c.Provider, tt.provider.Name)
				}
			}
			if diff := gocmp.Diff(tt.wantRead, env.names()); diff != "" {
				t.Errorf("variables read (-want +got):\n%s", diff)
			}
		})
	}
}

// TestNoProviderReadsTheGenericVariables pins the other side of
// TestProviderPrecedence: without WithProvider the client reads the three
// generic variables, in the order key, base URL, model, and no provider's
// variable, and uses the two fixed paths.
func TestNoProviderReadsTheGenericVariables(t *testing.T) {
	env := newStrictEnv(t, genericEnv, APIKeyEnv, BaseURLEnv, DefaultModelEnv)
	c := mustResolve(t, env.getenv)
	if diff := gocmp.Diff([]string{APIKeyEnv, BaseURLEnv, DefaultModelEnv}, env.names()); diff != "" {
		t.Errorf("variables read (-want +got):\n%s", diff)
	}
	got := []string{c.APIKey, c.SystemOneURL.String(), c.ModelsURL.String(), c.Model, c.Provider}
	want := []string{"generic-key-from-the-environment", "https://generic.example.test/v1/systemone", "https://generic.example.test/v1/models", "generic-model", ""}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("resolved settings (-want +got):\n%s", diff)
	}
}

// TestProviderRefused pins the Provider values WithProvider refuses: each is
// a *ConfigError of the build, reported before any variable is read and
// before WithAPIKey is looked at, and no message repeats a value of the
// Provider.
func TestProviderRefused(t *testing.T) {
	const secret = "sk-secret-in-a-url-0123456789" //nolint:gosec // G101: a made-up key the errors must not repeat.
	with := func(edit func(*Provider)) Provider {
		p := codivLike()
		edit(&p)
		return p
	}
	tests := map[string]struct {
		provider Provider
		want     string
	}{
		"error: empty Name":                    {provider: with(func(p *Provider) { p.Name = "" }), want: "has an empty Name"},
		"error: empty APIKeyEnv":               {provider: with(func(p *Provider) { p.APIKeyEnv = "" }), want: "has an empty APIKeyEnv"},
		"error: empty BaseURL":                 {provider: with(func(p *Provider) { p.BaseURL = "" }), want: "has an empty BaseURL"},
		"error: empty SystemOnePath":           {provider: with(func(p *Provider) { p.SystemOnePath = "" }), want: "has an empty SystemOnePath"},
		"error: relative SystemOnePath":        {provider: with(func(p *Provider) { p.SystemOnePath = "v1/systemone" }), want: "has a SystemOnePath that does not start with '/'"},
		"error: relative ModelsPath":           {provider: with(func(p *Provider) { p.ModelsPath = "v1/models" }), want: "has a ModelsPath that does not start with '/'"},
		"error: SystemOnePath with a query":    {provider: with(func(p *Provider) { p.SystemOnePath = "/v1/systemone?key=" + secret }), want: "has a SystemOnePath that must not carry a query ('?...')"},
		"error: SystemOnePath with a fragment": {provider: with(func(p *Provider) { p.SystemOnePath = "/v1/systemone#" + secret }), want: "has a SystemOnePath that must not carry a fragment ('#...')"},
		"error: ModelsPath with a query":       {provider: with(func(p *Provider) { p.ModelsPath = "/v1/models?key=" + secret }), want: "has a ModelsPath that must not carry a query ('?...')"},
		"error: ModelsPath with a fragment":    {provider: with(func(p *Provider) { p.ModelsPath = "/v1/models#" + secret }), want: "has a ModelsPath that must not carry a fragment ('#...')"},
		"error: blank DefaultModel":            {provider: with(func(p *Provider) { p.DefaultModel = " 　" }), want: "has a blank DefaultModel"},
		"error: DefaultModel not UTF-8":        {provider: with(func(p *Provider) { p.DefaultModel = "jev-\xff" }), want: "has a DefaultModel that is not valid UTF-8"},
		"error: BaseURL without a scheme":      {provider: with(func(p *Provider) { p.BaseURL = "api.codiv.ai" }), want: "has a BaseURL that must be absolute, with a scheme and a host, such as https://api.typesafe.ai"},
		"error: BaseURL not http":              {provider: with(func(p *Provider) { p.BaseURL = "ftp://api.codiv.ai" }), want: "has a BaseURL that must use http or https"},
		"error: BaseURL with credentials":      {provider: with(func(p *Provider) { p.BaseURL = "https://user:" + secret + "@api.codiv.ai" }), want: "has a BaseURL that must not carry credentials; pass the API key with WithAPIKey instead"},
		"error: BaseURL with a query":          {provider: with(func(p *Provider) { p.BaseURL = "https://api.codiv.ai?key=" + secret }), want: "has a BaseURL that must not carry a query ('?...')"},
		"error: BaseURL with a fragment":       {provider: with(func(p *Provider) { p.BaseURL = "https://api.codiv.ai#" + secret }), want: "has a BaseURL that must not carry a fragment ('#...')"},
		"error: BaseURL without a host":        {provider: with(func(p *Provider) { p.BaseURL = "https:///v1" }), want: "has a BaseURL that has an empty host"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			env := newStrictEnv(t, genericEnv)
			// WithAPIKey and WithBaseURL beside the refused provider: the
			// provider is checked first, so neither decides the error.
			err := resolveError(t, env.getenv, WithProvider(tt.provider), WithAPIKey(secret), WithBaseURL("https://other.example.test"))
			want := "The Provider passed to WithProvider " + tt.want + "."
			if got := err.Error(); got != want {
				t.Errorf("Error() = %q, want %q", got, want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("Error() repeats a value of the Provider: %q", err.Error())
			}
			if names := env.names(); len(names) != 0 {
				t.Errorf("variables read before the provider was checked: %q", names)
			}
		})
	}
}

// TestProviderLogEndpoints pins how log records name a provider's endpoints:
// the URL by default and the provider's own path alone under
// WithLogEndpointHost(false), never the fixed /v1/systemone; a provider
// without a listing has no models name in either form.
func TestProviderLogEndpoints(t *testing.T) {
	tests := map[string]struct {
		provider                  Provider
		opts                      []ClientOption
		wantSystemOne, wantModels string
	}{
		"success: default names the URL": {
			provider: perplexityLike(), wantSystemOne: "https://api.perplexity.ai/v1/decisions",
		},
		"success: false names the provider's path": {
			provider: perplexityLike(), opts: []ClientOption{WithLogEndpointHost(false)}, wantSystemOne: "/v1/decisions",
		},
		"success: false names both of the provider's paths": {
			provider: codivLike(), opts: []ClientOption{WithLogEndpointHost(false)}, wantSystemOne: "/v1/systemone", wantModels: "/v1/models",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			env := newStrictEnv(t, genericEnv, testProviderKeyEnv)
			c := mustResolve(t, env.getenv, append([]ClientOption{WithProvider(tt.provider)}, tt.opts...)...)
			if c.SystemOneLog != tt.wantSystemOne || c.ModelsLog != tt.wantModels {
				t.Errorf("log endpoints = (%q, %q), want (%q, %q)", c.SystemOneLog, c.ModelsLog, tt.wantSystemOne, tt.wantModels)
			}
		})
	}

	t.Run("success: the records of a call name the provider's path alone", func(t *testing.T) {
		logs := testsupport.NewLogRecorder(slog.LevelDebug)
		rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"), "X-Request-Id", "req_from_x_request_id")
		env := newStrictEnv(t, genericEnv, testProviderKeyEnv)
		c := providerClient(t, env.getenv, WithProvider(perplexityLike()), WithModel(testModel), WithRoundTripper(rec),
			WithRetry(NoRetry()), WithLogger(logs.Logger()), WithLogEndpointHost(false))
		resp, err := c.SystemOne(t.Context(), "hello", noulQuestion(t))
		if err != nil {
			t.Fatalf("SystemOne: %v", err)
		}
		if id, ok := resp.Meta().RequestID(); id != "req_from_x_request_id" || !ok {
			t.Errorf("Meta().RequestID() = (%q, %t), want the x-request-id value", id, ok)
		}
		if got := onlyRequest(t, rec).URL; got != "https://api.perplexity.ai/v1/decisions" {
			t.Errorf("request URL = %q, want the provider's System One URL", got)
		}
		records := logs.Records()
		if len(records) == 0 {
			t.Fatal("no record was logged")
		}
		for _, r := range records {
			if v, ok := r.Attr("endpoint"); !ok || v.String() != "/v1/decisions" {
				t.Errorf("%s endpoint = %v, want %q", r.Message, v, "/v1/decisions")
			}
			if r.Message == "response" {
				if id, _ := r.Attr("request_id"); id.String() != "req_from_x_request_id" {
					t.Errorf("response record request_id = %q, want the x-request-id value", id.String())
				}
			}
		}
	})
}

// TestProviderClientSendsToTheVendor pins what a provider's client sends,
// with only the provider's variable set among those it may read: the System
// One call and the listing go to the vendor's host and paths with the
// variable's key as the bearer token, and nothing else is read.
func TestProviderClientSendsToTheVendor(t *testing.T) {
	result := testsupport.JSON(http.StatusOK, testsupport.Fixture(t, "result.json"))
	models := testsupport.JSON(http.StatusOK, testsupport.Fixture(t, "models.json"))
	rec := &testsupport.Recorder{Replies: []testsupport.Reply{result, models}}
	env := newStrictEnv(t, map[string]string{testProviderKeyEnv: testProviderKey}, testProviderKeyEnv)
	c := providerClient(t, env.getenv, WithProvider(codivLike()), WithRoundTripper(rec), WithRetry(NoRetry()))
	if _, err := c.SystemOne(t.Context(), "hello", noulQuestion(t), Model("openjev-latest")); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if _, err := c.Models().List(t.Context()); err != nil {
		t.Fatalf("List: %v", err)
	}
	type sent struct{ Method, URL, Host, Authorization string }
	var got []sent
	for _, r := range rec.Requests() {
		got = append(got, sent{Method: r.Method, URL: r.URL, Host: r.Host, Authorization: r.Header.Get("Authorization")})
	}
	want := []sent{
		{Method: http.MethodPost, URL: "https://api.codiv.ai/v1/systemone", Host: "api.codiv.ai", Authorization: "Bearer " + testProviderKey},
		{Method: http.MethodGet, URL: "https://api.codiv.ai/v1/models", Host: "api.codiv.ai", Authorization: "Bearer " + testProviderKey},
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("requests (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff([]string{testProviderKeyEnv}, env.names()); diff != "" {
		t.Errorf("variables read (-want +got):\n%s", diff)
	}
}

// TestProviderDefaultModelOnTheWire sends a System One call through a
// stand-in server for a provider whose default model is gpt-6-luna, with
// only the provider's variable readable: the request names the default
// model, WithModel replaces it, and Model on the call replaces both. The
// stand-in serves the provider's own path; any other path is a 404.
func TestProviderDefaultModelOnTheWire(t *testing.T) {
	result := testsupport.Fixture(t, "result.json")
	type seen struct{ Path, Authorization, Model string }
	var (
		mu  sync.Mutex
		got []seen
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}
		var req struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("the request body is not JSON: %v", err)
		}
		mu.Lock()
		got = append(got, seen{Path: r.URL.Path, Authorization: r.Header.Get("Authorization"), Model: req.Model})
		mu.Unlock()
		if r.URL.Path != "/v1/decisions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	}))
	t.Cleanup(srv.Close)
	p := Provider{
		Name:          "OpenAI stand-in",
		BaseURL:       srv.URL,
		SystemOnePath: "/v1/decisions",
		APIKeyEnv:     testProviderKeyEnv,
		DefaultModel:  "gpt-6-luna",
	}
	tests := map[string]struct {
		clientOpts []ClientOption
		callOpts   []CallOption
		want       string
	}{
		"success: the provider's default model":    {want: "gpt-6-luna"},
		"success: WithModel replaces the default":  {clientOpts: []ClientOption{WithModel("x")}, want: "x"},
		"success: Model on the call replaces both": {clientOpts: []ClientOption{WithModel("x")}, callOpts: []CallOption{Model("y")}, want: "y"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			mu.Lock()
			got = nil
			mu.Unlock()
			env := newStrictEnv(t, map[string]string{testProviderKeyEnv: testProviderKey}, testProviderKeyEnv)
			c := providerClient(t, env.getenv, append([]ClientOption{WithProvider(p), WithRetry(NoRetry())}, tt.clientOpts...)...)
			if _, err := c.SystemOne(t.Context(), "hello", noulQuestion(t), tt.callOpts...); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			want := []seen{{Path: "/v1/decisions", Authorization: "Bearer " + testProviderKey, Model: tt.want}}
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("what the stand-in received (-want +got):\n%s", diff)
			}
		})
	}
}

// TestProviderNoModelFailsBeforeAnyIO pins the message of a System One call
// that names no model on a provider's client without one: it names the call
// option and the client option, and not DECISION_MODEL_DEFAULT_MODEL, which
// a provider's client never reads. Nothing reaches the transport.
func TestProviderNoModelFailsBeforeAnyIO(t *testing.T) {
	const want = "No model was named. Pass Model on the call, or set WithModel on the client."
	env := newStrictEnv(t, genericEnv, testProviderKeyEnv)
	c := providerClient(t, env.getenv, WithProvider(codivLike()), WithRoundTripper(failingTransport{t}), WithRetry(NoRetry()))
	_, err := c.SystemOne(t.Context(), "hello", noulQuestion(t))
	ce, ok := errors.AsType[*ConfigError](err)
	if !ok {
		t.Fatalf("SystemOne error = %T %v, want a *ConfigError", err, err)
	}
	if got := ce.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if strings.Contains(ce.Error(), DefaultModelEnv) {
		t.Errorf("Error() names %s, which a provider's client does not read", DefaultModelEnv)
	}
}

// TestProviderWithoutListing pins List and WarmUp on the client of a
// provider without a ModelsPath: both fail with a *ConfigError naming the
// provider before anything reaches the transport, which fails the test if
// called.
func TestProviderWithoutListing(t *testing.T) {
	const want = "The provider Perplexity lists no models: its Provider has no ModelsPath, so List sends nothing."
	tests := map[string]struct {
		call func(*Client) error
	}{
		"error: List": {call: func(c *Client) error {
			resp, err := c.Models().List(t.Context())
			if resp != nil {
				t.Errorf("List returned a response with its error")
			}
			return err
		}},
		"error: WarmUp": {call: func(c *Client) error { return c.WarmUp(t.Context()) }},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			env := newStrictEnv(t, genericEnv, testProviderKeyEnv)
			c := providerClient(t, env.getenv, WithProvider(perplexityLike()), WithRoundTripper(failingTransport{t}), WithRetry(NoRetry()))
			err := tt.call(c)
			ce, ok := errors.AsType[*ConfigError](err)
			if !ok {
				t.Fatalf("error = %T %v, want a *ConfigError", err, err)
			}
			if got := ce.Error(); got != want {
				t.Errorf("Error() = %q, want %q", got, want)
			}
			if got := c.Stats().Attempts; got != 0 {
				t.Errorf("attempts = %d, want 0", got)
			}
		})
	}
}

// TestRequestIDFallback pins where APIError.RequestID reads the server's
// identifier, through a stand-in that answers 400 with the headers of each
// row: x-typesafe-request-id when present, else x-request-id, else absent.
// The error's header keeps both headers as they arrived, and its text names
// the id it reports.
func TestRequestIDFallback(t *testing.T) {
	tests := map[string]struct {
		header map[string]string
		wantID string
		wantOK bool
	}{
		"success: x-typesafe-request-id alone": {
			header: map[string]string{"X-Typesafe-Request-Id": "req_typesafe"}, wantID: "req_typesafe", wantOK: true,
		},
		"success: x-request-id alone": {
			header: map[string]string{"X-Request-Id": "req_generic"}, wantID: "req_generic", wantOK: true,
		},
		"success: both, x-typesafe-request-id wins": {
			header: map[string]string{"X-Typesafe-Request-Id": "req_typesafe", "X-Request-Id": "req_generic"}, wantID: "req_typesafe", wantOK: true,
		},
		"success: neither, absent": {},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for k, v := range tt.header {
					w.Header().Set(k, v)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"detail":{"error_type":"api_usage_error","message":"Invalid request."}}`)
			}))
			t.Cleanup(srv.Close)
			clearEnv(t)
			c := mustClient(t, WithAPIKey(testKey), WithBaseURL(srv.URL), WithRetry(NoRetry()))
			_, err := c.SystemOne(t.Context(), "hello", noulQuestion(t))
			ae, isAPI := errors.AsType[*APIError](err)
			if !isAPI {
				t.Fatalf("SystemOne error = %T %v, want an *APIError", err, err)
			}
			id, ok := ae.RequestID()
			if id != tt.wantID || ok != tt.wantOK {
				t.Errorf("RequestID() = (%q, %t), want (%q, %t)", id, ok, tt.wantID, tt.wantOK)
			}
			for k, v := range tt.header {
				if got := ae.Header.Values(k); !slices.Equal(got, []string{v}) {
					t.Errorf("error header %s = %q, want [%q] as it arrived", k, got, v)
				}
			}
			wantSuffix := "Invalid request."
			if tt.wantOK {
				wantSuffix += " (request_id=" + tt.wantID + ")"
			}
			if !strings.HasSuffix(ae.Error(), wantSuffix) {
				t.Errorf("Error() = %q, want it to end with %q", ae.Error(), wantSuffix)
			}
		})
	}
}
