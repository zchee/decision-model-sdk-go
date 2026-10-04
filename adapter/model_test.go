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
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter/gemini"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/fake"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// factoryLog is a set of test factories that build fake providers and
// record the model each build was asked for, per factory name.
type factoryLog struct {
	mu     sync.Mutex
	builds map[string][]string
	// fail, when not nil, is returned by the next build instead of a
	// provider, once.
	fail error
	// steps are the outcomes each built provider answers with.
	steps []fake.Outcome
	// built holds every provider built, in build order.
	built []*fake.Provider
}

// factory returns the factory registered under name.
func (l *factoryLog) factory(name string) llm.Factory {
	return func(model string) (llm.Provider, error) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.builds == nil {
			l.builds = make(map[string][]string)
		}
		l.builds[name] = append(l.builds[name], model)
		if err := l.fail; err != nil {
			l.fail = nil
			return nil, err
		}
		p := fake.New(l.steps...).WithModel(model)
		l.built = append(l.built, p)
		return p, nil
	}
}

// snapshot returns a copy of the builds per factory name.
func (l *factoryLog) snapshot() map[string][]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string][]string, len(l.builds))
	for name, models := range l.builds {
		out[name] = slices.Clone(models)
	}
	return out
}

// providers returns the providers built, in build order.
func (l *factoryLog) providers() []*fake.Provider {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.built)
}

// TestResolveModel checks how a call's model string selects its provider
// and model, one case per rule: a WithProvider name; "<name>:<model>" with a
// factory, split at the first colon; an empty model or the SDK's default
// model name, which select the Adapter's default; and any other string, a
// model name for the default's provider. It also checks the refusals of a
// call without a model and without a provider, and that a default model
// naming a provider without a factory fails New with upstream's text
// (tests/test_provider_requests.py::test_unknown_provider_is_rejected).
func TestResolveModel(t *testing.T) {
	mine := fake.New().WithModel("mine-model")
	tests := map[string]struct {
		defaultModel string
		model        string
		// want is the resolved provider name and model; borrowed says it is
		// the WithProvider provider.
		wantName     string
		wantModel    string
		wantBorrowed bool
		// wantBuilds is what the factories were asked to build.
		wantBuilds map[string][]string
		// wantRefusal is the call's refusal, nil when it resolves.
		wantRefusal *refusal
		// wantNewErr is New's error text, empty when New succeeds.
		wantNewErr string
		// setup, when not nil, runs before New: a preset's case sets the
		// provider's environment with it.
		setup func(t *testing.T)
		// wantType, when not nil, is the type of the owned provider a preset
		// builds; the case then also checks that a second resolution reuses
		// that provider and that Close closes it and drops it.
		wantType reflect.Type
		// wantBuildErr, when not empty, is the error text the owned
		// provider's build must fail with; nothing is then cached.
		wantBuildErr string
	}{
		"a WithProvider name is the borrowed provider and its model": {
			model:        "mine",
			wantName:     "mine",
			wantModel:    "mine-model",
			wantBorrowed: true,
		},
		"a factory prefix builds the owned provider for the rest": {
			model:      "openai:gpt-4o-mini",
			wantName:   "openai",
			wantModel:  "gpt-4o-mini",
			wantBuilds: map[string][]string{"openai": {"gpt-4o-mini"}},
		},
		"a model with colons after the prefix is kept whole": {
			model:      "openai:ft:gpt-4o-mini:org::id",
			wantName:   "openai",
			wantModel:  "ft:gpt-4o-mini:org::id",
			wantBuilds: map[string][]string{"openai": {"ft:gpt-4o-mini:org::id"}},
		},
		"an empty model is the default model": {
			defaultModel: "anthropic:claude-x",
			model:        "",
			wantName:     "anthropic",
			wantModel:    "claude-x",
			wantBuilds:   map[string][]string{"anthropic": {"claude-x"}},
		},
		"the no-model name is the default model": {
			defaultModel: "anthropic:claude-x",
			model:        noModel,
			wantName:     "anthropic",
			wantModel:    "claude-x",
			wantBuilds:   map[string][]string{"anthropic": {"claude-x"}},
		},
		"a default that is a WithProvider name": {
			defaultModel: "mine",
			model:        noModel,
			wantName:     "mine",
			wantModel:    "mine-model",
			wantBorrowed: true,
		},
		"a bare model name uses the default's provider": {
			defaultModel: "anthropic:claude-x",
			model:        "gpt-4o-mini",
			wantName:     "anthropic",
			wantModel:    "gpt-4o-mini",
			wantBuilds:   map[string][]string{"anthropic": {"gpt-4o-mini"}},
		},
		"a prefix without a factory is part of a model name": {
			defaultModel: "openai:gpt-x",
			model:        "nope:model",
			wantName:     "openai",
			wantModel:    "nope:model",
			wantBuilds:   map[string][]string{"openai": {"nope:model"}},
		},
		"a factory prefix with no model is a model name": {
			defaultModel: "anthropic:claude-x",
			model:        "openai:",
			wantName:     "anthropic",
			wantModel:    "openai:",
			wantBuilds:   map[string][]string{"anthropic": {"openai:"}},
		},
		"no default and no per-call model": {
			model:       noModel,
			wantRefusal: &refusal{status: 400, errorType: "model_required", message: "An LLM model is required on the client or call."},
		},
		"no default and an empty model": {
			model:       "",
			wantRefusal: &refusal{status: 400, errorType: "model_required", message: "An LLM model is required on the client or call."},
		},
		"a bare model name without a default": {
			model:       "gpt-4o-mini",
			wantRefusal: &refusal{status: 400, errorType: "provider_required", message: "A provider is required: set provider='openai', 'anthropic', or 'gemini', or pass a provider instance as the model."},
		},
		"a bare model name when the default is a WithProvider name": {
			defaultModel: "mine",
			model:        "gpt-4o-mini",
			wantRefusal:  &refusal{status: 400, errorType: "provider_required", message: "A provider is required: set provider='openai', 'anthropic', or 'gemini', or pass a provider instance as the model."},
		},
		"default model naming a provider without a factory": {
			defaultModel: "nope:model",
			wantNewErr:   "Unknown provider 'nope'. Use 'openai', 'anthropic', or 'gemini', or pass a provider instance as the model.",
		},
		// tests/test_provider_requests.py::test_build_providers_select_gemini:
		// the gemini preset builds a gemini Provider without WithFactory.
		"gemini prefix builds a gemini provider": {
			model:     "gemini:gemini-3.8-flash",
			wantName:  "gemini",
			wantModel: "gemini-3.8-flash",
			setup: func(t *testing.T) {
				for _, name := range []string{"GOOGLE_API_KEY", "GOOGLE_GEMINI_BASE_URL"} {
					t.Setenv(name, "")
					if err := os.Unsetenv(name); err != nil {
						t.Fatalf("Unsetenv(%s): %v", name, err)
					}
				}
				t.Setenv("GEMINI_API_KEY", "not-a-key")
			},
			wantType: reflect.TypeFor[*gemini.Provider](),
		},
		// The gemini preset passes no option to gemini.New, so the
		// provider's own base URL variable decides: a value gemini refuses
		// fails the build with gemini's text, before any request.
		"gemini preset reads the base URL variable": {
			model:     "gemini:gemini-3.8-flash",
			wantName:  "gemini",
			wantModel: "gemini-3.8-flash",
			setup: func(t *testing.T) {
				t.Setenv("GOOGLE_API_KEY", "")
				if err := os.Unsetenv("GOOGLE_API_KEY"); err != nil {
					t.Fatalf("Unsetenv(GOOGLE_API_KEY): %v", err)
				}
				t.Setenv("GEMINI_API_KEY", "not-a-key")
				t.Setenv("GOOGLE_GEMINI_BASE_URL", "notaurl")
			},
			wantBuildErr: "gemini: the base URL of GOOGLE_GEMINI_BASE_URL is not an absolute http or https URL with a host",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup(t)
			}
			var log factoryLog
			opts := []Option{
				WithFactory("openai", log.factory("openai")),
				WithFactory("anthropic", log.factory("anthropic")),
				WithProvider("mine", mine),
			}
			if tt.defaultModel != "" {
				opts = append(opts, WithDefaultModel(tt.defaultModel))
			}
			ad, err := New(Probabilities, Structured, opts...)
			if tt.wantNewErr != "" {
				if err == nil || err.Error() != tt.wantNewErr {
					t.Fatalf("New() error = %v, want %q", err, tt.wantNewErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			got, err := ad.resolve(tt.model)
			if tt.wantRefusal != nil {
				r, ok := errors.AsType[*refusal](err)
				if !ok {
					t.Fatalf("resolve(%q) = %+v, %v; want refusal %+v", tt.model, got, err, *tt.wantRefusal)
				}
				if diff := gocmp.Diff(*tt.wantRefusal, *r, gocmp.AllowUnexported(refusal{})); diff != "" {
					t.Errorf("refusal (-want +got):\n%s", diff)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve(%q) error = %v", tt.model, err)
			}
			if got.name != tt.wantName || got.model != tt.wantModel || (got.borrowed != nil) != tt.wantBorrowed {
				t.Fatalf("resolve(%q) = {%q, %q, borrowed %v}, want {%q, %q, borrowed %v}", tt.model, got.name, got.model, got.borrowed != nil, tt.wantName, tt.wantModel, tt.wantBorrowed)
			}
			if tt.wantBuildErr != "" {
				if _, err := ad.provider(got); err == nil || err.Error() != tt.wantBuildErr {
					t.Errorf("provider() error = %v, want %q", err, tt.wantBuildErr)
				}
				if len(ad.owned) != 0 {
					t.Errorf("a failed build left %d owned providers, want none", len(ad.owned))
				}
				return
			}
			p, err := ad.provider(got)
			if err != nil {
				t.Fatalf("provider() error = %v", err)
			}
			if tt.wantBorrowed && p != llm.Provider(mine) {
				t.Errorf("provider() = %v, want the WithProvider provider", p)
			}
			if p.Model() != tt.wantModel {
				t.Errorf("provider().Model() = %q, want %q", p.Model(), tt.wantModel)
			}
			if diff := gocmp.Diff(tt.wantBuilds, log.snapshot(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("factory builds (-want +got):\n%s", diff)
			}
			if tt.wantType != nil {
				if typ := reflect.TypeOf(p); typ != tt.wantType {
					t.Errorf("provider() type = %v, want %v", typ, tt.wantType)
				}
				if again, err := ad.provider(got); err != nil || again != p {
					t.Errorf("a second provider() = %v, %v; want the provider built first", again, err)
				}
				if owned := ad.owned[providerKey{name: got.name, model: got.model}]; owned != p {
					t.Errorf("owned provider = %v, want the provider built", owned)
				}
				if err := ad.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
				if len(ad.owned) != 0 {
					t.Errorf("Close left %d owned providers, want none", len(ad.owned))
				}
			}
		})
	}
}

// TestModelID checks that ModelID joins the provider and the model with one
// colon, and that the string resolves back to them.
func TestModelID(t *testing.T) {
	tests := map[string]struct {
		provider, model, want string
	}{
		"plain":                {provider: "openai", model: "gpt-4o-mini", want: "openai:gpt-4o-mini"},
		"a model with colons":  {provider: "openai", model: "ft:gpt-4o-mini:org::id", want: "openai:ft:gpt-4o-mini:org::id"},
		"a provider of a test": {provider: "anthropic", model: "claude-haiku-4-5", want: "anthropic:claude-haiku-4-5"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := ModelID(tt.provider, tt.model)
			if got != tt.want {
				t.Fatalf("ModelID(%q, %q) = %q, want %q", tt.provider, tt.model, got, tt.want)
			}
			var log factoryLog
			ad, err := New(Probabilities, Structured, WithFactory(tt.provider, log.factory(tt.provider)))
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			tgt, err := ad.resolve(got)
			if err != nil || tgt.name != tt.provider || tgt.model != tt.model {
				t.Errorf("resolve(%q) = {%q, %q}, %v; want {%q, %q}", got, tgt.name, tgt.model, err, tt.provider, tt.model)
			}
		})
	}
}

// TestNewRefuses checks every argument New refuses and the error it gives:
// upstream's texts for an answer mode and a corrective retry count out of
// range (src/system_one_adapter/_client.py:365-368), the policy's own
// message for a retry policy out of range, and the default model's texts.
func TestNewRefuses(t *testing.T) {
	ok := fake.New()
	factory := func(string) (llm.Provider, error) { return ok, nil }
	tests := map[string]struct {
		answer AnswerMode
		output OutputMode
		opts   []Option
		want   string
	}{
		"answer mode zero": {
			answer: 0, output: Structured,
			want: "llm_answer_mode must be 'probabilities' or 'discrete'",
		},
		"answer mode out of range": {
			answer: Discrete + 1, output: Structured,
			want: "llm_answer_mode must be 'probabilities' or 'discrete'",
		},
		"output mode zero": {
			answer: Probabilities, output: 0,
			want: "adapter: output mode 0 is neither Structured nor Prompted",
		},
		"output mode out of range": {
			answer: Probabilities, output: Prompted + 1,
			want: "adapter: output mode 3 is neither Structured nor Prompted",
		},
		"negative corrective retries": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithMalformedRetries(-1)},
			want: "n_retry_malformed_structure must be >= 0",
		},
		"negative max retries": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithRetry(DefaultRetry().MaxRetries(-1))},
			want: "max_retries must be a non-negative integer.",
		},
		"negative initial backoff": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithRetry(DefaultRetry().Backoff(-time.Second, time.Second, 0))},
			want: "backoff_initial must be a non-negative, finite number of seconds.",
		},
		"negative maximum backoff": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithRetry(DefaultRetry().Backoff(time.Second, -time.Second, 0))},
			want: "backoff_max must be a non-negative, finite number of seconds.",
		},
		"jitter above one": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithRetry(DefaultRetry().Backoff(time.Second, time.Second, 1.5))},
			want: "backoff_jitter must be between zero and one.",
		},
		"zero budget": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithRetry(DefaultRetry().Budget(0))},
			want: "timeout must be a positive, finite number of seconds.",
		},
		"empty provider name": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithProvider("", ok)},
			want: "adapter: WithProvider: the name is empty",
		},
		"nil provider": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithProvider("mine", nil)},
			want: `adapter: WithProvider "mine": the provider is nil`,
		},
		"empty factory name": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithFactory("", factory)},
			want: "adapter: WithFactory: the name is empty",
		},
		"nil factory": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithFactory("openai", nil)},
			want: `adapter: WithFactory "openai": the factory is nil`,
		},
		"the first invalid option wins": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithFactory("", factory), WithProvider("", ok)},
			want: "adapter: WithFactory: the name is empty",
		},
		"default model without a provider name": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithDefaultModel("gpt-4o-mini")},
			want: "A provider is required: set provider='openai', 'anthropic', or 'gemini', or pass a provider instance as the model.",
		},
		"default model with an empty provider name": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithDefaultModel(":gpt-4o-mini")},
			want: "A provider is required: set provider='openai', 'anthropic', or 'gemini', or pass a provider instance as the model.",
		},
		"default model that is the no-model name": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithDefaultModel(noModel)},
			want: "A provider is required: set provider='openai', 'anthropic', or 'gemini', or pass a provider instance as the model.",
		},
		"default model naming a provider with a quote": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithDefaultModel("it's:model")},
			want: `Unknown provider "it's". Use 'openai', 'anthropic', or 'gemini', or pass a provider instance as the model.`,
		},
		"default model naming a provider with a control character": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithDefaultModel("a\tb\x01:model")},
			want: `Unknown provider 'a\tb\x01'. Use 'openai', 'anthropic', or 'gemini', or pass a provider instance as the model.`,
		},
		"default model with a factory and no model": {
			answer: Probabilities, output: Structured,
			opts: []Option{WithFactory("openai", factory), WithDefaultModel("openai:")},
			want: `adapter: the default model "openai:" names no model after its provider`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ad, err := New(tt.answer, tt.output, tt.opts...)
			if err == nil {
				t.Fatalf("New() = %v, nil; want error %q", ad, tt.want)
			}
			if ad != nil {
				t.Errorf("New() returned an Adapter with error %v", err)
			}
			if got := err.Error(); got != tt.want {
				t.Errorf("New() error = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNewAccepts checks the settings New keeps: the answer and output
// modes, the normalisation, the corrective retries, the policy, and the
// providers in the order they were registered, a repeated name replacing
// the provider in its first place.
func TestNewAccepts(t *testing.T) {
	first, second, third := fake.New().WithModel("one"), fake.New().WithModel("two"), fake.New().WithModel("three")
	ad, err := New(Discrete, Prompted,
		WithNormalizeProbabilities(true),
		WithMalformedRetries(2),
		WithRetry(DefaultRetry()),
		WithProvider("b", first),
		WithProvider("a", second),
		WithProvider("b", third),
		WithDefaultModel("a"),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if ad.eval.answer != Discrete || ad.eval.output != Prompted || !ad.eval.normalize || ad.eval.malformedRetries != 2 || ad.eval.retry.maxRetries != 2 {
		t.Errorf("eval = %+v", ad.eval)
	}
	if diff := gocmp.Diff([]string{"b", "a"}, ad.providerList); diff != "" {
		t.Errorf("provider order (-want +got):\n%s", diff)
	}
	if ad.providers["b"] != llm.Provider(third) {
		t.Errorf("provider b = %v, want the later registration", ad.providers["b"])
	}
	if ad.defaultModel != "a" || ad.defaultName != "" {
		t.Errorf("default = %q, name %q; want \"a\", \"\"", ad.defaultModel, ad.defaultName)
	}
}

// TestMissingProviderIsRejected ports
// tests/test_client_with_fake_model.py::test_missing_provider_setting_is_rejected:
// a call that names a bare model on an Adapter without a default provider
// is refused with upstream's provider_required text, before any provider is
// built.
func TestMissingProviderIsRejected(t *testing.T) {
	var log factoryLog
	ad, err := New(Probabilities, Structured, WithFactory("openai", log.factory("openai")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = sdkClient(t, ad, false).SystemOne(t.Context(), "state", noulQuestions(t), decision.Model("gpt-4o-mini"))
	apiErr, ok := errors.AsType[*decision.APIError](err)
	if !ok || apiErr.StatusCode != http.StatusBadRequest || apiErr.ErrorType != "provider_required" || !strings.Contains(apiErr.Message, "provider") {
		t.Fatalf("SystemOne error = %v, want 400 provider_required naming the provider", err)
	}
	if len(log.snapshot()) != 0 {
		t.Errorf("factory builds = %v, want none", log.snapshot())
	}
}
