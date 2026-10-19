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
	// kindDialect speaks a wire of its own, which its preset's
	// decision.Dialect translates, so that a call reads as on a System One
	// provider; the recorded bodies are the vendor's own.
	kindDialect
)

// liveProvider is a provider TestLiveProviders can call, under the name
// DECISION_MODEL_LIVE_PROVIDERS gives it, which also names its fixture
// directory under testdata/live/providers.
type liveProvider struct {
	name string
	p    decision.Provider
	kind providerKind
	// plainNoul reports whether the provider is asked liveTicket's three
	// questions, whose yes/no question has no outcomes, in place of the set
	// TestLiveQuestions asks, whose yes/no question names the outcome true
	// alone: Decisions API requires both outcomes, OpenAI's wire has no place
	// for one, and OpenRouter refuses a noul that has criteria.true without
	// criteria.false with status 400, its message asking for criteria.false.
	plainNoul bool
	// noRequestID reports whether the provider's responses carry no
	// identifier of the request in a header the SDK reads, so that the tests
	// do not expect one. OpenRouter sends x-generation-id, equal to the
	// body's id, and x-provider-name instead.
	noRequestID bool
}

// liveProviders is every provider the live harness knows, in the order it
// calls them, each the provider package's preset. Decisions API and OpenAI
// carry the dialect that translates their wire. Decisions API's variable is
// DECISIONS_API_KEY, apart from DECISIONAPI_API_KEY, which holds the key of
// another vendor (decisionapi.net) that the harness does not call. Both
// OpenRouter endpoints read OPENROUTER_API_KEY.
var liveProviders = []liveProvider{
	{name: "typesafe", p: provider.TypeSafe(), kind: kindSystemOne},
	{name: "codiv", p: provider.Codiv(), kind: kindSystemOne},
	{name: "perplexity", p: provider.Perplexity(), kind: kindSystemOne},
	{name: "decisions-api", p: provider.DecisionsAPI(), kind: kindDialect, plainNoul: true},
	{name: "openai", p: provider.OpenAI(), kind: kindDialect, plainNoul: true},
	{name: "openrouter", p: provider.OpenRouter(), kind: kindSystemOne, plainNoul: true, noRequestID: true},
	{name: "openrouter-decisions", p: provider.OpenRouterDecisions(), kind: kindSystemOne, plainNoul: true, noRequestID: true},
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

// keyEnvNames returns every variable whose value the recorder scrubs, each
// once: each provider's key variable, then scrubOnlyKeyEnvs. Two providers
// may read one variable, as the two OpenRouter endpoints do.
func keyEnvNames() []string {
	names := make([]string, 0, len(liveProviders)+len(scrubOnlyKeyEnvs))
	for _, name := range slices.Concat(providerKeyEnvs(), scrubOnlyKeyEnvs) {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// providerKeyEnvs returns each provider's key variable, in the order of
// liveProviders, a shared variable once for each provider that reads it.
func providerKeyEnvs() []string {
	names := make([]string, 0, len(liveProviders))
	for _, lp := range liveProviders {
		names = append(names, lp.p.APIKeyEnv)
	}
	return names
}

// recordedProviderBodies are the files under testdata/live/providers/<name>,
// in name order: the bodies TestLiveProviders records per provider (-record),
// each as the vendor sent it (the listing where the provider has one, the
// answers to the System One call and to the typed call, and the answer to a
// key the provider did not issue), and the files kept from earlier
// recordings. OpenAI's answers are not recorded: its dialect-request.json and
// dialect-response.json are the request its dialect writes and the answer to
// it, kept from the discovery run its dialect was written from, and
// refused.json is its 400 to the System One wire, which the SDK no longer
// sends it.
var recordedProviderBodies = map[string][]string{
	"typesafe":             {"models.json", "questions.json", "typed-response.json", "wrong-key.json"},
	"codiv":                {"models.json", "questions.json", "typed-response.json", "wrong-key.json"},
	"perplexity":           {"questions.json", "typed-response.json", "wrong-key.json"},
	"decisions-api":        {"questions.json", "typed-response.json", "wrong-key.json"},
	"openai":               {"dialect-request.json", "dialect-response.json", "refused.json", "wrong-key.json"},
	"openrouter":           {"questions.json", "typed-response.json", "wrong-key.json"},
	"openrouter-decisions": {"questions.json", "typed-response.json", "wrong-key.json"},
}

// recordsProviderBody reports whether TestLiveProviders records the body
// name for provider: whether it is one of recordedProviderBodies' files.
func recordsProviderBody(provider, name string) bool {
	return slices.Contains(recordedProviderBodies[provider], name)
}
