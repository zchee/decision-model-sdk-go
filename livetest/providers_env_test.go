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

package livetest

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	decision "github.com/zchee/decision-model-sdk-go"
	"github.com/zchee/decision-model-sdk-go/provider"
)

// The variables TestLiveProviders reads beside DECISION_MODEL_LIVE_TESTS.
const (
	// liveProvidersEnv names the providers to call, comma-separated; each
	// is billed, so none is called unless named.
	liveProvidersEnv = "DECISION_MODEL_LIVE_PROVIDERS"

	// liveModelEnvPrefix starts the variable that names the model a
	// provider is asked: DECISION_MODEL_LIVE_MODEL_ and the provider's name
	// in upper case with each "-" written "_" (modelEnvName).
	liveModelEnvPrefix = "DECISION_MODEL_LIVE_MODEL_"
)

// providerKind is how a provider answers the SDK's own System One request.
type providerKind int

const (
	// kindSystemOne answers in the System One response shape.
	kindSystemOne providerKind = iota
	// kindEnvelope accepts the request and wraps the System One answer in
	// an envelope of its own, which the SDK's decoder refuses.
	kindEnvelope
	// kindRefused refuses the request with status 400: its wire is not
	// System One's.
	kindRefused
)

// liveProvider is a provider TestLiveProviders can call, under the name
// DECISION_MODEL_LIVE_PROVIDERS gives it, which also names its fixture
// directory under testdata/live/providers.
type liveProvider struct {
	name string
	p    decision.Provider
	kind providerKind
}

// liveProviders is every provider the live harness knows, in the order it
// calls them. TypeSafe AI, Codiv and Perplexity are the provider package's
// presets. Decisions API and OpenAI are written here as literals: their
// presets land in the provider package together with the translation of
// their wire, which the SDK does not have yet, so the harness records what
// each returns to the SDK's System One request. Decisions API's variable is
// DECISIONS_API_KEY, apart from DECISIONAPI_API_KEY, which holds the key of
// another vendor (decisionapi.net) that the harness does not call.
var liveProviders = []liveProvider{
	{name: "typesafe", p: provider.TypeSafe(), kind: kindSystemOne},
	{name: "codiv", p: provider.Codiv(), kind: kindSystemOne},
	{name: "perplexity", p: provider.Perplexity(), kind: kindSystemOne},
	{name: "decisions-api", p: decision.Provider{
		Name:          "Decisions API",
		BaseURL:       "https://decisions-api.dev",
		SystemOnePath: "/v1/systemone",
		APIKeyEnv:     "DECISIONS_API_KEY",
	}, kind: kindEnvelope},
	{name: "openai", p: decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
		Name:          "OpenAI",
		BaseURL:       "https://api.openai.com",
		SystemOnePath: "/v1/decisions",
		APIKeyEnv:     "OPENAI_API_KEY",
		DefaultModel:  "gpt-6-luna",
	}, kind: kindRefused},
}

// scrubOnlyKeyEnvs name the variables that may hold a key of a vendor the
// harness does not call through a provider. The recorder scrubs their values
// all the same, so a key exported for another vendor cannot reach a fixture.
var scrubOnlyKeyEnvs = []string{decision.APIKeyEnv, "DECISIONAPI_API_KEY", "TYPELLM_API_KEY"}

// providerNames returns the names DECISION_MODEL_LIVE_PROVIDERS accepts.
func providerNames() []string {
	names := make([]string, 0, len(liveProviders))
	for _, lp := range liveProviders {
		names = append(names, lp.name)
	}
	return names
}

// modelEnvName returns the variable that names the model provider name is
// asked, such as DECISION_MODEL_LIVE_MODEL_DECISIONS_API.
func modelEnvName(name string) string {
	return liveModelEnvPrefix + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// selectedProvider is one provider DECISION_MODEL_LIVE_PROVIDERS named, with
// the model the tests ask it.
type selectedProvider struct {
	liveProvider
	// model is the model the tests ask: the model variable's value, or the
	// provider's DefaultModel when modelSet is false.
	model    string
	modelSet bool
}

// providerEnv is what TestLiveProviders needs from the environment. The keys
// stay in memory: the scrubber needs them to find an echo of one in a body,
// and none is printed.
type providerEnv struct {
	providers []selectedProvider
	// secrets holds the value of every key variable the environment sets,
	// of the named providers and of the others alike.
	secrets []string
}

// providerEnvFrom reads TestLiveProviders' variables through getenv. It
// fails, naming every variable that is missing, unless
// DECISION_MODEL_LIVE_TESTS is 1, DECISION_MODEL_LIVE_PROVIDERS names one or
// more known providers, each once, and every named provider has its key
// variable and, unless it has a DefaultModel, its model variable set.
// DECISION_MODEL_BASE_URL and DECISION_MODEL_DEFAULT_MODEL are not read, and
// DECISION_MODEL_API_KEY is read only so the recorder can scrub its value:
// each provider's client reads its own key variable. No message repeats a
// key.
func providerEnvFrom(getenv func(string) string) (providerEnv, error) {
	var errs []error
	if strings.TrimSpace(getenv(liveTestsEnv)) != "1" {
		errs = append(errs, errors.New(liveTestsEnv+" is not 1: set it to 1 to allow the tests to call the billed API"))
	}
	var env providerEnv
	list := strings.TrimSpace(getenv(liveProvidersEnv))
	var named []string
	listed := false
	for raw := range strings.SplitSeq(list, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		listed = true
		i := slices.IndexFunc(liveProviders, func(lp liveProvider) bool { return lp.name == name })
		switch {
		case i < 0:
			errs = append(errs, fmt.Errorf("%s names %q, which is not one of %s", liveProvidersEnv, name, strings.Join(providerNames(), ", ")))
			continue
		case slices.Contains(named, name):
			errs = append(errs, fmt.Errorf("%s names %q twice", liveProvidersEnv, name))
			continue
		}
		named = append(named, name)
		sp := selectedProvider{liveProvider: liveProviders[i]}
		if strings.TrimSpace(getenv(sp.p.APIKeyEnv)) == "" {
			errs = append(errs, errors.New(sp.p.APIKeyEnv+" is unset or blank: set it to the "+name+" API key"))
		}
		modelEnv := modelEnvName(name)
		if m := strings.TrimSpace(getenv(modelEnv)); m != "" {
			sp.model, sp.modelSet = m, true
		} else if sp.p.DefaultModel != "" {
			sp.model = sp.p.DefaultModel
		} else {
			errs = append(errs, errors.New(modelEnv+" is unset or blank: set it to the model the tests ask "+name))
		}
		env.providers = append(env.providers, sp)
	}
	if !listed {
		errs = append(errs, errors.New(liveProvidersEnv+" is unset or blank: set it to one or more of "+strings.Join(providerNames(), ", ")+", comma-separated"))
	}
	for _, name := range keyEnvNames() {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			env.secrets = append(env.secrets, v)
		}
	}
	if len(errs) > 0 {
		return providerEnv{}, fmt.Errorf("live provider tests: %w", errors.Join(errs...))
	}
	return env, nil
}

// keyEnvNames returns every variable whose value the recorder scrubs: each
// provider's key variable, then scrubOnlyKeyEnvs.
func keyEnvNames() []string {
	names := make([]string, 0, len(liveProviders)+len(scrubOnlyKeyEnvs))
	for _, lp := range liveProviders {
		names = append(names, lp.p.APIKeyEnv)
	}
	return append(names, scrubOnlyKeyEnvs...)
}

// recordedProviderBodies are the bodies TestLiveProviders records per
// provider (-record) under testdata/live/providers/<name>: the listing where
// the provider has one, the answers to the System One call and to the typed
// call where the decoder reads them or refuses the envelope around them, the
// refusal of a provider whose wire is not System One's, and the answer to a
// key the provider did not issue.
var recordedProviderBodies = map[string][]string{
	"typesafe":      {"models.json", "questions.json", "typed-response.json", "wrong-key.json"},
	"codiv":         {"models.json", "questions.json", "typed-response.json", "wrong-key.json"},
	"perplexity":    {"questions.json", "typed-response.json", "wrong-key.json"},
	"decisions-api": {"questions.json", "typed-response.json", "wrong-key.json"},
	"openai":        {"refused.json", "wrong-key.json"},
}
