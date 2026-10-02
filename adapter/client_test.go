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

package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/fake"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// ownClientKey is the API key of a client a test builds itself: a plain
// word, not a credential.
const ownClientKey = "caller-own-key"

// ownClientBaseURL is the base URL of a client a test builds itself with
// its own transport. The root SDK has no default base URL; this names
// TypeSafe AI's, which the transport never dials.
const ownClientBaseURL = "https://api.typesafe.ai"

// noulAnswer is a model output that answers noulQuestions.
const noulAnswer = `{"answers":{"answer":0.75}}`

// sdkClient returns a root SDK client whose requests ad answers, closed
// when the test ends; every test that goes through the SDK builds its
// client here. With own false it is NewClient(ad, opts...). With own true it
// is a client of the caller's own, decision.NewClient with a plain key,
// WithRoundTripper(ad), WithBaseURL(placeholderBaseURL) and WithModel(noModel)
// before opts, as NewClient sets them, so that it has the SDK's default retry
// policy and timeout unless opts change them, and so that no base URL or
// model of the environment reaches it.
func sdkClient(t testing.TB, ad *Adapter, own bool, opts ...decision.ClientOption) *decision.Client {
	t.Helper()
	var (
		c   *decision.Client
		err error
	)
	if own {
		all := append([]decision.ClientOption{decision.WithAPIKey(ownClientKey), decision.WithRoundTripper(ad), decision.WithBaseURL(placeholderBaseURL), decision.WithModel(noModel)}, opts...)
		c, err = decision.NewClient(all...)
	} else {
		c, err = NewClient(ad, opts...)
	}
	if err != nil {
		t.Fatalf("building the client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// noulQuestions returns one Noul question named answer.
func noulQuestions(t testing.TB) *decision.Prepared {
	t.Helper()
	q, err := decision.NewQuestions().Noul("answer", decision.Noul{Instructions: decision.Text("The review is positive.")}).Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return q
}

// fakeAdapter returns an Adapter in probabilities, structured mode whose
// default model is the WithProvider name "fake", served by p, with opts
// after those.
func fakeAdapter(t testing.TB, p llm.Provider, opts ...Option) *Adapter {
	t.Helper()
	ad, err := New(Probabilities, Structured, append([]Option{WithProvider("fake", p), WithDefaultModel("fake")}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ad
}

// funcProvider is a provider of a test: Do calls do, and Model returns
// model, or "func-model" when it is empty.
type funcProvider struct {
	model string
	do    func(ctx context.Context, req *llm.Request) (*llm.Result, error)
}

func (p *funcProvider) Model() string {
	if p.model == "" {
		return "func-model"
	}
	return p.model
}

func (p *funcProvider) Do(ctx context.Context, req *llm.Request) (*llm.Result, error) {
	return p.do(ctx, req)
}

// rtFunc is an http.RoundTripper of a test.
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// textResult returns a result with text and known counts.
func textResult(text string) *llm.Result {
	return &llm.Result{Text: text, InputTokens: llm.Count{N: 11, Known: true}, OutputTokens: llm.Count{N: 7, Known: true}}
}

// recordHandler is a slog.Handler that keeps every record with its
// attributes resolved, groups flattened to dotted keys.
type recordHandler struct {
	mu      sync.Mutex
	records []loggedRecord
	attrs   []slog.Attr
	group   string
	level   slog.Level
}

// loggedRecord is one record as recordHandler keeps it.
type loggedRecord struct {
	Level   slog.Level
	Message string
	Attrs   map[string]string
}

func (h *recordHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *recordHandler) Handle(_ context.Context, r slog.Record) error {
	rec := loggedRecord{Level: r.Level, Message: r.Message, Attrs: map[string]string{}}
	for _, a := range h.attrs {
		flatten(rec.Attrs, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		flatten(rec.Attrs, h.group, a)
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, rec)
	return nil
}

func (h *recordHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	return &recordHandler{attrs: append(append([]slog.Attr{}, h.attrs...), attrs...), group: h.group, level: h.level}
}

func (h *recordHandler) WithGroup(name string) slog.Handler {
	return &recordHandler{attrs: h.attrs, group: h.group + name + ".", level: h.level}
}

// all returns a copy of the records kept.
func (h *recordHandler) all() []loggedRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]loggedRecord{}, h.records...)
}

// flatten adds a, resolved, to out under prefix, a group's members under
// "<group>.<name>".
func flatten(out map[string]string, prefix string, a slog.Attr) {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, m := range v.Group() {
			flatten(out, p, m)
		}
		return
	}
	out[prefix+a.Key] = v.String()
}

// TestNewClientSettings checks the settings NewClient gives the SDK: its key
// is PlaceholderAPIKey (the SDK masks a header value that holds the
// client's key in its debug records, and a header of the test holds it),
// the SDK makes one attempt while the Adapter retries the provider, the
// request's context has no deadline (a provider that answers after a
// minute still answers), and the SDK does not retry a provider status the
// Adapter passed through.
func TestNewClientSettings(t *testing.T) {
	t.Run("one SDK attempt while the Adapter retries", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var calls atomic.Int32
			var deadlines []bool
			var mu sync.Mutex
			p := &funcProvider{do: func(ctx context.Context, _ *llm.Request) (*llm.Result, error) {
				_, has := ctx.Deadline()
				mu.Lock()
				deadlines = append(deadlines, has)
				mu.Unlock()
				if calls.Add(1) <= 2 {
					return nil, &llm.StatusError{StatusCode: http.StatusServiceUnavailable}
				}
				time.Sleep(time.Minute)
				return textResult(noulAnswer), nil
			}}
			h := &recordHandler{level: slog.LevelDebug}
			ad := fakeAdapter(t, p, WithRetry(DefaultRetry()))
			c := sdkClient(t, ad, false, decision.WithLogger(slog.New(h)), decision.WithHeader("X-Probe", PlaceholderAPIKey), decision.WithHeader("X-Control", "plain"))
			resp, err := c.SystemOne(t.Context(), "state", noulQuestions(t))
			if err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			if got := c.Stats().Attempts; got != 1 {
				t.Errorf("Stats().Attempts = %d, want 1", got)
			}
			if got := calls.Load(); got != 3 {
				t.Errorf("provider calls = %d, want 3", got)
			}
			if diff := gocmp.Diff([]bool{false, false, false}, deadlines); diff != "" {
				t.Errorf("provider context has a deadline (-want +got):\n%s", diff)
			}
			r, err := ReportOf(resp)
			if err != nil {
				t.Fatalf("ReportOf: %v", err)
			}
			if r.Usage.Retries != 2 {
				t.Errorf("n_retries = %d, want 2", r.Usage.Retries)
			}
			var headers map[string]string
			for _, rec := range h.all() {
				if rec.Message == "request" {
					headers = rec.Attrs
				}
			}
			for key, want := range map[string]string{"headers.X-Probe": "***", "headers.X-Control": "plain", "headers.Authorization": "***"} {
				if got, ok := headers[key]; !ok || got != want {
					t.Errorf("request record %s = %q (present %v), want %q; record %v", key, got, ok, want, headers)
				}
			}
		})
	})
	t.Run("a provider status is not retried by the SDK", func(t *testing.T) {
		p := fake.New(fake.Error(&llm.StatusError{StatusCode: 503}))
		c := sdkClient(t, fakeAdapter(t, p), false)
		_, err := c.SystemOne(t.Context(), "state", noulQuestions(t))
		apiErr, ok := errors.AsType[*decision.APIError](err)
		if !ok || apiErr.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("SystemOne error = %v, want a 503 *decision.APIError", err)
		}
		if got := c.Stats().Attempts; got != 1 {
			t.Errorf("Stats().Attempts = %d, want 1", got)
		}
		if got := p.Calls(); got != 1 {
			t.Errorf("provider calls = %d, want 1", got)
		}
	})
	t.Run("a key of the test is not masked", func(t *testing.T) {
		h := &recordHandler{level: slog.LevelDebug}
		c := sdkClient(t, fakeAdapter(t, fake.New(fake.Text(noulAnswer))), true, decision.WithLogger(slog.New(h)), decision.WithHeader("X-Probe", PlaceholderAPIKey))
		if _, err := c.SystemOne(t.Context(), "state", noulQuestions(t)); err != nil {
			t.Fatalf("SystemOne: %v", err)
		}
		found := false
		for _, rec := range h.all() {
			if v, ok := rec.Attrs["headers.X-Probe"]; ok && rec.Message == "request" {
				found = true
				if v != PlaceholderAPIKey {
					t.Errorf("a client with another key masked X-Probe as %q; the masking does not tell the key", v)
				}
			}
		}
		if !found {
			t.Error("no request record holds X-Probe")
		}
	})
}

// TestNewClientIgnoresDefaultModelEnv checks that
// DECISION_MODEL_DEFAULT_MODEL, which the SDK reads when no model is set,
// never reaches a provider, the Report or the response: NewClient always
// sets the model, the Adapter's default or the name the Adapter reads as
// no model, which selects the Adapter's default or, without one, fails as
// upstream fails without a model.
func TestNewClientIgnoresDefaultModelEnv(t *testing.T) {
	const envModel = "model-from-the-environment"
	tests := map[string]struct {
		withDefault bool
		call        []decision.CallOption
		wantModel   string
		wantErrType string
	}{
		"with a default model": {
			withDefault: true,
			wantModel:   "fake-model",
		},
		"without a default and without a per-call model": {
			wantErrType: "model_required",
		},
		"without a default and with a per-call model": {
			call:      []decision.CallOption{decision.Model("fake")},
			wantModel: "fake-model",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(decision.DefaultModelEnv, envModel)
			p := fake.New(fake.Text(noulAnswer))
			opts := []Option{WithProvider("fake", p)}
			if tt.withDefault {
				opts = append(opts, WithDefaultModel("fake"))
			}
			ad, err := New(Probabilities, Structured, opts...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			c := sdkClient(t, ad, false)
			resp, err := c.SystemOne(t.Context(), "state", noulQuestions(t), tt.call...)
			if tt.wantErrType != "" {
				apiErr, ok := errors.AsType[*decision.APIError](err)
				if !ok || apiErr.ErrorType != tt.wantErrType || apiErr.StatusCode != http.StatusBadRequest {
					t.Fatalf("SystemOne error = %v, want 400 %s", err, tt.wantErrType)
				}
				if strings.Contains(string(apiErr.Body), envModel) {
					t.Errorf("the error body holds the environment's model: %s", apiErr.Body)
				}
				if p.Calls() != 0 {
					t.Errorf("provider calls = %d, want 0", p.Calls())
				}
				return
			}
			if err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			if got := resp.Model(); got != tt.wantModel {
				t.Errorf("response model = %q, want %q", got, tt.wantModel)
			}
			if strings.Contains(string(resp.Meta().RawBody()), envModel) {
				t.Errorf("the response body holds the environment's model")
			}
			for _, req := range p.Requests() {
				for _, m := range req.Messages {
					if strings.Contains(m.Content, envModel) {
						t.Errorf("a provider message holds the environment's model: %q", m.Content)
					}
				}
			}
		})
	}
}

// TestNewClientRefusesASecondClient checks that an Adapter belongs to one
// client: a second NewClient fails, a NewClient that fails in the SDK
// leaves the Adapter free, and of many concurrent NewClient calls on one
// Adapter exactly one succeeds.
func TestNewClientRefusesASecondClient(t *testing.T) {
	t.Run("sequential", func(t *testing.T) {
		ad := fakeAdapter(t, fake.New(fake.Text(noulAnswer)))
		if _, err := NewClient(ad, decision.WithConnectTimeout(time.Second)); !errors.As(err, new(*decision.ConfigError)) {
			t.Fatalf("NewClient with an option WithRoundTripper excludes: error = %v, want a *decision.ConfigError", err)
		}
		c := sdkClient(t, ad, false)
		if _, err := c.SystemOne(t.Context(), "state", noulQuestions(t)); err != nil {
			t.Fatalf("SystemOne after a failed NewClient: %v", err)
		}
		if _, err := NewClient(ad); err == nil || err.Error() != "adapter: NewClient: the Adapter already belongs to a client" {
			t.Fatalf("second NewClient error = %v", err)
		}
		if _, err := NewClient(nil); err == nil || err.Error() != "adapter: NewClient: the Adapter is nil" {
			t.Fatalf("NewClient(nil) error = %v", err)
		}
	})
	t.Run("concurrent", func(t *testing.T) {
		ad := fakeAdapter(t, fake.New(fake.Text(noulAnswer)))
		const n = 64
		var (
			start = make(chan struct{})
			wg    sync.WaitGroup
			won   atomic.Int32
			mu    sync.Mutex
			got   []*decision.Client
		)
		for range n {
			wg.Go(func() {
				<-start
				c, err := NewClient(ad)
				if err != nil {
					return
				}
				won.Add(1)
				mu.Lock()
				got = append(got, c)
				mu.Unlock()
			})
		}
		close(start)
		wg.Wait()
		if won.Load() != 1 {
			t.Fatalf("%d of %d concurrent NewClient calls succeeded, want 1", won.Load(), n)
		}
		if _, err := got[0].SystemOne(t.Context(), "state", noulQuestions(t)); err != nil {
			t.Errorf("SystemOne on the winning client: %v", err)
		}
		if err := got[0].Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
}

// TestNewClientCloseClosesAdapter checks that closing a client from
// NewClient closes the Adapter once: its owned providers are closed, a
// cleanup error is returned by the client's first Close, and the Adapter
// refuses calls afterwards.
func TestNewClientCloseClosesAdapter(t *testing.T) {
	var log factoryLog
	log.steps = []fake.Outcome{fake.Text(noulAnswer)}
	ad, err := New(Probabilities, Structured, WithFactory("openai", log.factory("openai")), WithDefaultModel("openai:gpt-x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c, err := NewClient(ad)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.SystemOne(t.Context(), "state", noulQuestions(t)); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	built := log.providers()
	if len(built) != 1 {
		t.Fatalf("providers built = %d, want 1", len(built))
	}
	closeErr := errors.New("close failed")
	built[0].WithCloseError(closeErr)
	if err := c.Close(); !errors.Is(err, closeErr) {
		t.Errorf("first client Close = %v, want the provider's close error", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second client Close = %v, want nil", err)
	}
	if got := built[0].Closes(); got != 1 {
		t.Errorf("provider closes = %d, want 1", got)
	}
	if _, err := c.SystemOne(t.Context(), "state", noulQuestions(t)); !errors.Is(err, decision.ErrClientClosed) {
		t.Errorf("SystemOne after Close = %v, want ErrClientClosed", err)
	}
	if !ad.closed {
		t.Error("the Adapter is not closed")
	}
	built[0].WithCloseError(nil)
	if err := ad.Close(); err != nil {
		t.Errorf("Adapter Close after the failed cleanup = %v, want nil", err)
	}
	if got := built[0].Closes(); got != 2 {
		t.Errorf("provider closes = %d, want 2: the failed one is closed again", got)
	}
}

// TestNewClientTransportIsTheAdapter checks that the Adapter is the
// transport of every client NewClient returns: a WithRoundTripper among the
// caller's options has no effect (the SDK keeps the last one and NewClient
// applies the Adapter after the caller's options), and closing that client
// closes the Adapter. The SDK's transport options, which cannot be combined
// with a round tripper, make NewClient fail with a *decision.ConfigError
// and leave the Adapter free for another NewClient call; options that do
// not configure the transport are accepted.
func TestNewClientTransportIsTheAdapter(t *testing.T) {
	t.Run("a caller's round tripper has no effect", func(t *testing.T) {
		var log factoryLog
		log.steps = []fake.Outcome{fake.Text(noulAnswer)}
		ad := newAdapter(t, WithFactory("openai", log.factory("openai")), WithDefaultModel("openai:gpt-x"))
		var other atomic.Int32
		c, err := NewClient(ad, decision.WithRoundTripper(rtFunc(func(*http.Request) (*http.Response, error) {
			other.Add(1)
			return nil, errors.New("the caller's round tripper was called")
		})))
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		if _, err := c.SystemOne(t.Context(), "state", noulQuestions(t)); err != nil {
			t.Fatalf("SystemOne: %v", err)
		}
		built := log.providers()
		if other.Load() != 0 || len(built) != 1 || built[0].Calls() != 1 {
			t.Fatalf("the caller's round tripper got %d calls; providers built %d", other.Load(), len(built))
		}
		if err := c.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if built[0].Closes() != 1 || !ad.closed {
			t.Errorf("after the client's Close: provider closes %d, Adapter closed %v", built[0].Closes(), ad.closed)
		}
	})
	tests := map[string]struct {
		opt     decision.ClientOption
		refused bool
	}{
		"WithHTTPTransport":   {opt: decision.WithHTTPTransport(&http.Transport{}), refused: true},
		"WithHTTPVersion":     {opt: decision.WithHTTPVersion(decision.HTTP2Only), refused: true},
		"WithRootCAs":         {opt: decision.WithRootCAs(x509.NewCertPool()), refused: true},
		"WithTLSConfig":       {opt: decision.WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS13}), refused: true},
		"WithProxy":           {opt: decision.WithProxy(http.ProxyFromEnvironment), refused: true},
		"WithConnectTimeout":  {opt: decision.WithConnectTimeout(time.Second), refused: true},
		"WithCompression":     {opt: decision.WithCompression(true), refused: true},
		"WithClientTrace":     {opt: decision.WithClientTrace(nil)},
		"WithLogEndpointHost": {opt: decision.WithLogEndpointHost(true)},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := fake.New(fake.Text(noulAnswer))
			ad := fakeAdapter(t, p)
			c, err := NewClient(ad, tt.opt)
			if !tt.refused {
				if err != nil {
					t.Fatalf("NewClient: %v", err)
				}
				defer c.Close()
				if _, err := c.SystemOne(t.Context(), "state", noulQuestions(t)); err != nil || p.Calls() != 1 {
					t.Errorf("SystemOne: %v; provider calls %d", err, p.Calls())
				}
				return
			}
			if _, ok := errors.AsType[*decision.ConfigError](err); !ok {
				t.Fatalf("NewClient error = %v, want a *decision.ConfigError", err)
			}
			c = sdkClient(t, ad, false)
			if _, err := c.SystemOne(t.Context(), "state", noulQuestions(t)); err != nil || p.Calls() != 1 {
				t.Errorf("after the refused option: SystemOne %v; provider calls %d", err, p.Calls())
			}
		})
	}
}

// TestPlaceholderKeyNeverForwarded checks that the SDK's key never reaches
// a provider, the Report or an error: neither NewClient's placeholder nor a
// canary key of a client of the caller's own appears in any provider
// request, response body or error text, on success and on failure. The
// provider sees no header at all; its request holds only the messages and
// the schema.
func TestPlaceholderKeyNeverForwarded(t *testing.T) {
	canary := canaryKey(t)
	for name, outcome := range map[string]fake.Outcome{
		"an answer":        fake.Text(noulAnswer),
		"a status":         fake.Error(&llm.StatusError{StatusCode: http.StatusBadRequest, Body: []byte(`{"error":{"message":"bad"}}`)}),
		"a timeout":        fake.Error(&llm.TimeoutError{}),
		"malformed output": fake.Text("not JSON"),
	} {
		for _, own := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, own client %v", name, own), func(t *testing.T) {
				p := fake.New(outcome)
				var opts []decision.ClientOption
				if own {
					opts = []decision.ClientOption{decision.WithAPIKey(canary), decision.WithRetry(decision.NoRetry())}
				}
				c := sdkClient(t, fakeAdapter(t, p), own, opts...)
				resp, err := c.SystemOne(t.Context(), "state", noulQuestions(t))
				texts := []string{}
				if err != nil {
					texts = append(texts, err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err), string(apiErrorBody(err)))
				}
				if resp != nil {
					texts = append(texts, string(resp.Meta().RawBody()))
				}
				if r, ok := ReportFromError(err); ok {
					b, merr := r.MarshalJSON()
					if merr != nil {
						t.Fatal(merr)
					}
					texts = append(texts, string(b))
				}
				for _, req := range p.Requests() {
					texts = append(texts, string(req.Schema))
					for _, m := range req.Messages {
						texts = append(texts, m.Content)
					}
				}
				for _, s := range texts {
					for _, key := range []string{PlaceholderAPIKey, canary} {
						if strings.Contains(s, key) {
							t.Errorf("a text holds the key %q: %.200s", key, s)
						}
					}
				}
			})
		}
	}
}

// bigTrace returns a provider whose every request records a response body
// of n bytes (valid JSON) and then answers with text, or fails with err
// when err is not nil.
func bigTrace(n int, text string, err error) *funcProvider {
	body := []byte(`{"x":"` + strings.Repeat("a", n) + `"}`)
	return &funcProvider{do: func(_ context.Context, req *llm.Request) (*llm.Result, error) {
		req.Trace.RecordResponse(body, nil)
		if err != nil {
			return nil, err
		}
		return textResult(text), nil
	}}
}

// TestReportLostOverTheLimit checks the size limit of a caller's own client
// with the SDK's default of 16 MiB: an error body over it arrives empty and
// a success body over it gives a *decision.ResponseTooLargeError, and in
// both cases ReportFromError returns false; a client from NewClient, whose
// limit is 1 GiB, keeps both.
func TestReportLostOverTheLimit(t *testing.T) {
	const n = 17 << 20
	tests := map[string]struct {
		err error
	}{
		"an error body":  {err: &llm.StatusError{StatusCode: http.StatusBadRequest}},
		"a success body": {},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			own := sdkClient(t, fakeAdapter(t, bigTrace(n, noulAnswer, tt.err)), true, decision.WithRetry(decision.NoRetry()))
			_, err := own.SystemOne(t.Context(), "state", noulQuestions(t))
			if _, ok := ReportFromError(err); ok || err == nil {
				t.Errorf("own client: error %v, ReportFromError %v; want an error without a Report", err, ok)
			}
			if tt.err == nil {
				if _, ok := errors.AsType[*decision.ResponseTooLargeError](err); !ok {
					t.Errorf("own client: %T, want *decision.ResponseTooLargeError", err)
				}
			} else if body := apiErrorBody(err); len(body) != 0 {
				t.Errorf("own client: the error body has %d bytes, want none", len(body))
			}
			c := sdkClient(t, fakeAdapter(t, bigTrace(n, noulAnswer, tt.err)), false)
			resp, err := c.SystemOne(t.Context(), "state", noulQuestions(t))
			var r *Report
			if tt.err == nil {
				if err != nil {
					t.Fatalf("NewClient: %v", err)
				}
				r, err = ReportOf(resp)
			} else {
				var ok bool
				if r, ok = ReportFromError(err); !ok {
					err = errors.New("no Report")
				} else {
					err = nil
				}
			}
			if err != nil || len(r.Debug.Attempts) != 1 || len(r.Debug.Attempts[0].Response) < n {
				t.Errorf("NewClient: Report %v, error %v", r != nil, err)
			}
		})
	}
}

// TestLargeReportThroughSDK checks that a body of more than 20 MiB, a large
// state written into three attempts, decodes through a client from
// NewClient and that ReportOf returns all three attempts.
func TestLargeReportThroughSDK(t *testing.T) {
	state := strings.Repeat("s", 4<<20)
	p := &funcProvider{do: func(_ context.Context, req *llm.Request) (*llm.Result, error) {
		var b strings.Builder
		for _, m := range req.Messages {
			b.WriteString(m.Content)
		}
		body, err := jsonx.Marshal(jsonx.Object(jsonx.Member{Name: "input", Value: jsonx.String(b.String())}))
		if err != nil {
			return nil, err
		}
		req.Trace.RecordRequest("responses", body)
		if len(req.Messages) < 6 {
			return textResult("not JSON"), nil
		}
		return textResult(noulAnswer), nil
	}}
	c := sdkClient(t, fakeAdapter(t, p, WithMalformedRetries(2)), false)
	resp, err := c.SystemOne(t.Context(), state, noulQuestions(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if n := len(resp.Meta().RawBody()); n < 20<<20 {
		t.Errorf("the body has %d bytes, want at least 20 MiB", n)
	}
	r, err := ReportOf(resp)
	if err != nil || len(r.Debug.Attempts) != 3 {
		t.Fatalf("ReportOf = %v attempts, %v; want 3", r, err)
	}
}

// TestHostileContentInDebug checks that a provider body or an LLM output
// holding text a JSON body cannot hold as it is (a raw control character,
// a byte that is not UTF-8, a NaN token, 5000 levels of nesting, a lone
// surrogate escape) or can (U+2028) never makes the answer undecodable:
// the call succeeds, and ReportOf returns the body as a JSON value or as a
// string of its text with the encoding member "text".
func TestHostileContentInDebug(t *testing.T) {
	tests := map[string]struct {
		body     string
		wantText bool
		// want is the body ReportOf gives back; empty means body itself.
		want string
	}{
		"a raw control character":  {body: "{\"a\":\"\x01\"}", wantText: true},
		"a byte that is not UTF-8": {body: "{\"a\":\"\xff\"}", wantText: true, want: "{\"a\":\"\ufffd\"}"},
		"a NaN token":              {body: `{"a":NaN}`, wantText: true},
		"5000 levels":              {body: strings.Repeat("[", 5000) + strings.Repeat("]", 5000), wantText: true},
		"a lone surrogate escape":  {body: `{"a":"\ud800"}`, wantText: true},
		"U+2028":                   {body: "{\"a\":\"\u2028\"}"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			calls := 0
			p := &funcProvider{do: func(_ context.Context, req *llm.Request) (*llm.Result, error) {
				calls++
				req.Trace.RecordResponse([]byte(tt.body), nil)
				if calls == 1 {
					return textResult(tt.body), nil
				}
				return textResult(noulAnswer), nil
			}}
			c := sdkClient(t, fakeAdapter(t, p, WithMalformedRetries(1)), false)
			resp, err := c.SystemOne(t.Context(), "state", noulQuestions(t))
			if err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			r, err := ReportOf(resp)
			if err != nil || len(r.Debug.Attempts) != 2 {
				t.Fatalf("ReportOf = %v, %v", r, err)
			}
			a := r.Debug.Attempts[0]
			want := tt.want
			if want == "" {
				want = tt.body
			}
			if tt.wantText {
				if a.Info.ResponseEncoding != "text" || string(a.Response) != want {
					t.Errorf("response %q encoding %q; want %q as text", a.Response, a.Info.ResponseEncoding, want)
				}
			} else if equal, err := jsonx.Equal(a.Response, []byte(want)); err != nil || !equal || a.Info.ResponseEncoding != "" {
				t.Errorf("response %q encoding %q; want the JSON value %q", a.Response, a.Info.ResponseEncoding, want)
			}
			if got := r.Debug.Attempts[1].Messages[2].Content; got != strings.ToValidUTF8(tt.body, "\ufffd") {
				t.Errorf("the corrective round's assistant message %q, want the output %q", got, tt.body)
			}
		})
	}
}

// TestSDKRetryCountRecorded checks that the SDK's retries are recorded and
// never refused: on a client of the caller's own with the SDK's
// DefaultRetry and a provider that answers 503 every time, the SDK makes
// three requests, whose bodies record no sdk_retry_count, then 1, then 2,
// and the caller's 503 carries a Report with SDKRetryCount 2. Through
// NewClient no body carries the member. A value of X-TypeSafe-Retry-Count
// is recorded only when it is one to nine ASCII digits without a sign or a
// leading zero.
func TestSDKRetryCountRecorded(t *testing.T) {
	t.Run("through the SDK", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ad := fakeAdapter(t, fake.New(fake.Error(&llm.StatusError{StatusCode: http.StatusServiceUnavailable})))
			var bodies [][]byte
			recording := rtFunc(func(req *http.Request) (*http.Response, error) {
				resp, err := ad.RoundTrip(req)
				if err == nil {
					b, _ := io.ReadAll(resp.Body)
					bodies = append(bodies, b)
					resp.Body = io.NopCloser(bytes.NewReader(b))
				}
				return resp, err
			})
			c := sdkClient(t, ad, true, decision.WithRetry(decision.DefaultRetry()), decision.WithRoundTripper(recording))
			_, err := c.SystemOne(t.Context(), "state", noulQuestions(t))
			apiErr, ok := errors.AsType[*decision.APIError](err)
			if !ok || apiErr.StatusCode != http.StatusServiceUnavailable || apiErr.Kind != decision.APIErrorInternalServer {
				t.Fatalf("SystemOne error = %v", err)
			}
			var counts []int
			for _, b := range bodies {
				var r Report
				if err := r.UnmarshalJSON(b); err != nil {
					t.Fatal(err)
				}
				counts = append(counts, r.Debug.SDKRetryCount)
			}
			if diff := gocmp.Diff([]int{0, 1, 2}, counts); diff != "" {
				t.Errorf("sdk_retry_count per body (-want +got):\n%s", diff)
			}
			if len(bodies) > 0 && bytes.Contains(bodies[0], []byte("sdk_retry_count")) {
				t.Error("the first body has the member")
			}
			if r, ok := ReportFromError(err); !ok || r.Debug.SDKRetryCount != 2 {
				t.Errorf("ReportFromError = %v, %v; want SDKRetryCount 2", r, ok)
			}
		})
	})
	t.Run("through NewClient", func(t *testing.T) {
		resp, err := sdkClient(t, fakeAdapter(t, fake.New(fake.Text(noulAnswer))), false).SystemOne(t.Context(), "state", noulQuestions(t))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(resp.Meta().RawBody(), []byte("sdk_retry_count")) {
			t.Error("a body through NewClient has sdk_retry_count")
		}
	})
	values := map[string]int{
		"": 0, "1": 1, "2": 2, "9": 9, "10": 10, "123456789": 123456789, "1234567890": 0,
		"+1": 0, "01": 0, "1.0": 0, " 1": 0, "1 ": 0, "0": 0, "-1": 0, "a": 0, "1e3": 0, "\u0663": 0,
	}
	for value, want := range values {
		t.Run(fmt.Sprintf("value %q", value), func(t *testing.T) {
			ad := fakeAdapter(t, fake.New(fake.Text(noulAnswer)))
			req := requestWith(t.Context(), http.MethodPost, systemOnePath, io.NopCloser(strings.NewReader(noulBody)), retryCountHeader(value))
			_, body, err := send(t, ad, req)
			if err != nil {
				t.Fatal(err)
			}
			var r Report
			if err := r.UnmarshalJSON(body); err != nil {
				t.Fatal(err)
			}
			if r.Debug.SDKRetryCount != want {
				t.Errorf("SDKRetryCount = %d, want %d", r.Debug.SDKRetryCount, want)
			}
		})
	}
}
