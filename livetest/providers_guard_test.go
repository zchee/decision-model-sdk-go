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
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"
	"github.com/zchee/decision-model-sdk-go/provider"
)

// TestLiveProviderCatalog pins every provider the live harness can call: its
// name, its whole Provider (a dialect provider's is its preset, which the
// provider package's tests pin), how it answers the SDK's wire, which
// question set it is asked and whether its responses name the request.
// Decisions API reads DECISIONS_API_KEY, never DECISIONAPI_API_KEY, which
// belongs to a vendor the harness does not call; that one is only scrubbed.
// Both OpenRouter endpoints read OPENROUTER_API_KEY.
func TestLiveProviderCatalog(t *testing.T) {
	type entry struct {
		Name        string
		P           decision.Provider
		Kind        providerKind
		PlainNoul   bool
		NoRequestID bool
	}
	want := []entry{
		{Name: "typesafe", P: decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
			Name: "TypeSafe AI", BaseURL: "https://api.typesafe.ai", SystemOnePath: "/v1/systemone", ModelsPath: "/v1/models", APIKeyEnv: "TYPESAFE_API_KEY",
		}, Kind: kindSystemOne},
		{Name: "codiv", P: decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
			Name: "Codiv", BaseURL: "https://api.codiv.ai", SystemOnePath: "/v1/systemone", ModelsPath: "/v1/models", APIKeyEnv: "CODIV_API_KEY",
		}, Kind: kindSystemOne},
		{Name: "perplexity", P: decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
			Name: "Perplexity", BaseURL: "https://api.perplexity.ai", SystemOnePath: "/v1/decisions", APIKeyEnv: "PERPLEXITY_API_KEY",
		}, Kind: kindSystemOne},
		{Name: "decisions-api", P: provider.DecisionsAPI(), Kind: kindDialect, PlainNoul: true},
		{Name: "openai", P: provider.OpenAI(), Kind: kindDialect, PlainNoul: true},
		{Name: "openrouter", P: decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
			Name: "OpenRouter", BaseURL: "https://openrouter.ai", SystemOnePath: "/api/v1/systemone", APIKeyEnv: "OPENROUTER_API_KEY",
		}, Kind: kindSystemOne, PlainNoul: true, NoRequestID: true},
		{Name: "openrouter-decisions", P: decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
			Name: "OpenRouter Decisions", BaseURL: "https://openrouter.ai", SystemOnePath: "/api/alpha/decisions", APIKeyEnv: "OPENROUTER_API_KEY",
		}, Kind: kindSystemOne, PlainNoul: true, NoRequestID: true},
	}
	var got []entry
	for _, lp := range liveProviders {
		got = append(got, entry{Name: lp.name, P: lp.p, Kind: lp.kind, PlainNoul: lp.plainNoul, NoRequestID: lp.noRequestID})
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("liveProviders (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff(slices.Sorted(maps.Keys(recordedProviderBodies)), slices.Sorted(slices.Values(providerNames()))); diff != "" {
		t.Errorf("recordedProviderBodies and liveProviders name different providers (-bodies +providers):\n%s", diff)
	}
	if !slices.Contains(keyEnvNames(), "DECISIONAPI_API_KEY") || slices.ContainsFunc(liveProviders, func(lp liveProvider) bool { return lp.p.APIKeyEnv == "DECISIONAPI_API_KEY" }) {
		t.Error("DECISIONAPI_API_KEY must be scrubbed and read by no provider")
	}
	if got := modelEnvName("decisions-api"); got != "DECISION_MODEL_LIVE_MODEL_DECISIONS_API" {
		t.Errorf("modelEnvName(decisions-api) = %q, want DECISION_MODEL_LIVE_MODEL_DECISIONS_API", got)
	}
	if got := modelEnvName("openrouter-decisions"); got != "DECISION_MODEL_LIVE_MODEL_OPENROUTER_DECISIONS" {
		t.Errorf("modelEnvName(openrouter-decisions) = %q, want DECISION_MODEL_LIVE_MODEL_OPENROUTER_DECISIONS", got)
	}
	names := keyEnvNames()
	if n := len(names) - len(slices.Compact(slices.Sorted(slices.Values(names)))); n != 0 {
		t.Errorf("keyEnvNames() repeats %d variables, want each once", n)
	}
	if n := strings.Count(strings.Join(names, " "), "OPENROUTER_API_KEY"); n != 1 {
		t.Errorf("keyEnvNames() names OPENROUTER_API_KEY %d times, want once for the two endpoints", n)
	}
}

// syntheticOpenRouterKey has the shape of an OpenRouter key, sk-or-v1- and
// 64 hex digits, and is no key. The prefix and the digits are two literals
// so that the source holds no run of bytes with the whole shape, which a
// secret scanner on the remote refuses to accept in a push; the constant
// the compiler joins is what the rows below need.
const syntheticOpenRouterKey = "sk-or-v1-" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestLiveProviderEnvGuard checks TestLiveProviders' guard: without the
// switch, a provider list, and each named provider's key and model (unless
// it has a DefaultModel) it fails naming every missing variable, an unknown
// or repeated name is refused, and no message repeats a key. The generic
// DECISION_MODEL_ variables are never needed. The keys of every provider
// variable the environment sets, named or not, and of the scrub-only
// variables, are collected for the scrubber.
func TestLiveProviderEnvGuard(t *testing.T) {
	const (
		codivKey      = "sk-codiv-synthetic-key-0123456789"
		openAIKey     = "sk-openai-synthetic-key-0123456789"
		otherKey      = "sk_decisionapi-synthetic-key-0123456789"
		openRouterKey = syntheticOpenRouterKey
	)
	base := func(over map[string]string) map[string]string {
		env := map[string]string{liveTestsEnv: "1", liveProvidersEnv: "codiv", "CODIV_API_KEY": codivKey, "DECISION_MODEL_LIVE_MODEL_CODIV": "openjev-latest"}
		maps.Copy(env, over)
		return env
	}
	type got struct {
		Names, Models []string
		ModelSet      []bool
	}
	tests := map[string]struct {
		env         map[string]string
		wantErr     []string // parts the error must contain; nil: no error
		want        got
		wantSecrets []string
	}{
		"error: nothing set": {
			env:     map[string]string{},
			wantErr: []string{liveTestsEnv + " is not 1", liveProvidersEnv + " is unset or blank"},
		},
		"error: everything but the switch": {
			env:     base(map[string]string{liveTestsEnv: ""}),
			wantErr: []string{liveTestsEnv + " is not 1"},
		},
		"error: no provider named": {
			env:     base(map[string]string{liveProvidersEnv: " , "}),
			wantErr: []string{liveProvidersEnv + " is unset or blank"},
		},
		"error: an unknown provider": {
			env:     base(map[string]string{liveProvidersEnv: "codiv,typellm"}),
			wantErr: []string{liveProvidersEnv + ` names "typellm", which is not one of typesafe, codiv, perplexity, decisions-api, openai, openrouter, openrouter-decisions`},
		},
		"error: a provider named twice": {
			env:     base(map[string]string{liveProvidersEnv: "codiv, codiv"}),
			wantErr: []string{liveProvidersEnv + ` names "codiv" twice`},
		},
		"error: the provider's key variable missing, the generic key set": {
			env:     base(map[string]string{"CODIV_API_KEY": " ", decision.APIKeyEnv: codivKey}),
			wantErr: []string{"CODIV_API_KEY is unset or blank"},
		},
		"error: the model variable missing for a provider without a default model": {
			env:     base(map[string]string{"DECISION_MODEL_LIVE_MODEL_CODIV": "", decision.DefaultModelEnv: "jev-latest"}),
			wantErr: []string{"DECISION_MODEL_LIVE_MODEL_CODIV is unset or blank"},
		},
		"error: every missing variable is named at once": {
			env:     map[string]string{liveProvidersEnv: "codiv,decisions-api"},
			wantErr: []string{liveTestsEnv + " is not 1", "CODIV_API_KEY is unset or blank", "DECISION_MODEL_LIVE_MODEL_CODIV is unset or blank", "DECISIONS_API_KEY is unset or blank", "DECISION_MODEL_LIVE_MODEL_DECISIONS_API is unset or blank"},
		},
		"success: one provider": {
			env:         base(nil),
			want:        got{Names: []string{"codiv"}, Models: []string{"openjev-latest"}, ModelSet: []bool{true}},
			wantSecrets: []string{codivKey},
		},
		"success: a default model stands in for the model variable": {
			env:         base(map[string]string{liveProvidersEnv: "openai", "OPENAI_API_KEY": " " + openAIKey + "\n"}),
			want:        got{Names: []string{"openai"}, Models: []string{"gpt-6-luna"}, ModelSet: []bool{false}},
			wantSecrets: []string{codivKey, openAIKey},
		},
		"success: the model variable wins over a default model": {
			env:         base(map[string]string{liveProvidersEnv: "openai", "OPENAI_API_KEY": openAIKey, "DECISION_MODEL_LIVE_MODEL_OPENAI": "gpt-6-sol"}),
			want:        got{Names: []string{"openai"}, Models: []string{"gpt-6-sol"}, ModelSet: []bool{true}},
			wantSecrets: []string{codivKey, openAIKey},
		},
		"error: both OpenRouter endpoints need the shared key and each its model": {
			env:     map[string]string{liveTestsEnv: "1", liveProvidersEnv: "openrouter,openrouter-decisions"},
			wantErr: []string{"OPENROUTER_API_KEY is unset or blank", "DECISION_MODEL_LIVE_MODEL_OPENROUTER is unset or blank", "DECISION_MODEL_LIVE_MODEL_OPENROUTER_DECISIONS is unset or blank"},
		},
		"success: both OpenRouter endpoints, one key collected once": {
			env: base(map[string]string{
				liveProvidersEnv: "openrouter,openrouter-decisions", "OPENROUTER_API_KEY": openRouterKey,
				"DECISION_MODEL_LIVE_MODEL_OPENROUTER": "typesafe/jev-1.13", "DECISION_MODEL_LIVE_MODEL_OPENROUTER_DECISIONS": "jev-1.13",
			}),
			want:        got{Names: []string{"openrouter", "openrouter-decisions"}, Models: []string{"typesafe/jev-1.13", "jev-1.13"}, ModelSet: []bool{true, true}},
			wantSecrets: []string{codivKey, openRouterKey},
		},
		"success: several providers in the order named, every set key collected": {
			env: base(map[string]string{
				liveProvidersEnv: " openai , codiv ", "OPENAI_API_KEY": openAIKey,
				"DECISIONAPI_API_KEY": otherKey, decision.APIKeyEnv: "generic-synthetic-key-0123456789",
			}),
			want:        got{Names: []string{"openai", "codiv"}, Models: []string{"gpt-6-luna", "openjev-latest"}, ModelSet: []bool{false, true}},
			wantSecrets: []string{codivKey, openAIKey, "generic-synthetic-key-0123456789", otherKey},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			env, err := providerEnvFrom(func(k string) string { return tt.env[k] })
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("providerEnvFrom() succeeded with %d providers; want an error", len(env.providers))
				}
				for _, part := range tt.wantErr {
					if !strings.Contains(err.Error(), part) {
						t.Errorf("providerEnvFrom() error = %q, want it to contain %q", err, part)
					}
				}
				if n := strings.Count(err.Error(), liveTestsEnv+" is not 1"); n > 1 {
					t.Errorf("the switch's message appears %d times, want at most once", n)
				}
				for _, key := range []string{codivKey, openAIKey, otherKey, openRouterKey} {
					if strings.Contains(err.Error(), key) {
						t.Errorf("providerEnvFrom() error repeats a key")
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("providerEnvFrom() error = %v", err)
			}
			var g got
			for _, sp := range env.providers {
				g.Names = append(g.Names, sp.name)
				g.Models = append(g.Models, sp.model)
				g.ModelSet = append(g.ModelSet, sp.modelSet)
			}
			if diff := gocmp.Diff(tt.want, g); diff != "" {
				t.Errorf("providers (-want +got):\n%s", diff)
			}
			if !slices.Equal(slices.Sorted(slices.Values(env.secrets)), slices.Sorted(slices.Values(tt.wantSecrets))) {
				t.Errorf("collected %d secrets, want %d (the values are not shown)", len(env.secrets), len(tt.wantSecrets))
			}
		})
	}
}

// TestProviderCredentialShapes feeds the scrubber's shapes synthetic tokens
// with each vendor's key prefix (each must be found; OpenRouter's sk-or-v1-
// is found by the sk- shape), the two Perplexity model ids (neither may be),
// and masked echoes: of a key the recorder knows (found, without the key in
// the finding) and of the wrong key the tests send on purpose (not found). A
// refused body reaches no file.
func TestProviderCredentialShapes(t *testing.T) {
	const realKey = "sk-proj-synthetic-key-abcdefghij-wxyz"
	// longKey is as long as the longest real key (TypeSafe AI's are 107
	// bytes), and longEcho masks it as OpenAI's 401 answer to a wrong key
	// shows a key: the first 8 characters, a star for each of the len-12 in
	// between, and the last 4.
	longKey := "apikey_" + strings.Repeat("0123456789abcdef", 6) + "WXYZ"
	longEcho := longKey[:8] + strings.Repeat("*", len(longKey)-12) + longKey[len(longKey)-4:]
	tests := map[string]struct {
		body    string
		secrets []string
		want    []string // the findings
	}{
		"error: an apikey_ token":       {body: `{"echo":"apikey_0123456789abcdef"}`, want: []string{"a token with the API key prefix apikey_"}},
		"error: an APIKEY_ token":       {body: `{"echo":"APIKEY_0123456789ABCDEF"}`, want: []string{"a token with the API key prefix apikey_"}},
		"error: an sk- token":           {body: `{"echo":"sk-proj-0123456789"}`, want: []string{"a token with the API key prefix sk-"}},
		"error: an sk_ token":           {body: `{"echo":"sk_live_0123456789"}`, want: []string{"a token with the API key prefix sk_"}},
		"error: a pplx- token":          {body: `{"echo":"pplx-0123456789abcdef"}`, want: []string{"a token with the API key prefix pplx-"}},
		"error: an sk-or-v1- token":     {body: `{"echo":"` + syntheticOpenRouterKey + `"}`, want: []string{"a token with the API key prefix sk-"}},
		"success: a short sk- word":     {body: `{"note":"sk-learn"}`},
		"success: risk-assessment-tool": {body: `{"note":"risk-assessment-tool"}`},
		"success: the two Perplexity model ids": {
			body: `{"model":"pplx-decider-v1.1-27b","other":"pplx-decider-v1-27b","list":["pplx-decider-v1.1-27b"]}`,
		},
		"error: a key that starts with a model id's text": {
			body: `{"echo":"pplx-decider-v1-27bSECRETTAIL0000"}`, want: []string{"a token with the API key prefix pplx-"},
		},
		"error: a key that ends with a model id's text": {
			body: `{"echo":"sk-abc-pplx-decider-v1-27b"}`, want: []string{"a token with the API key prefix sk-", "a token with the API key prefix pplx-"},
		},
		"success: two model ids one character apart": {
			body: `{"note":"pplx-decider-v1-27b pplx-decider-v1.1-27b"}`,
		},
		"error: a model id beside a pplx- key": {
			body: `{"model":"pplx-decider-v1.1-27b","echo":"pplx-0123456789abcdef"}`, want: []string{"a token with the API key prefix pplx-"},
		},
		"error: a masked echo of a known key": {
			body:    `{"error":{"message":"Incorrect API key provided: sk-proj****************wxyz."}}`,
			secrets: []string{realKey},
			want:    []string{"a masked echo of secret 1 of 1"},
		},
		"error: a user_id member at the top level": {
			body: `{"error":{"message":"Invalid input","code":400},"user_id":"user_0123abcd"}`,
			want: []string{"an account identifier as a user_id member"},
		},
		"error: a user_id member inside an object": {
			body: `{"error":{"message":"Invalid input","code":400,"metadata":{"user_id" : "u"}}}`,
			want: []string{"an account identifier as a user_id member"},
		},
		"error: a user_id member spelled with a Unicode escape at the top level": {
			body: `{"error":{"message":"Invalid input","code":400},"\u0075ser_id":"user_0123abcd"}`,
			want: []string{"an account identifier as a user_id member"},
		},
		"error: a user_id member spelled with a Unicode escape inside an object": {
			body: `{"error":{"message":"Invalid input","code":400,"metadata":{"user\u005fid" : "u"}}}`,
			want: []string{"an account identifier as a user_id member"},
		},
		"error: a user_id member spelled with a Unicode escape after a duplicate name": {
			body: `{"code":400,"code":401,"\u0055SER_ID":"u"}`,
			want: []string{"an account identifier as a user_id member"},
		},
		"error: a user_id member in both spellings is reported once": {
			body: `{"user_id":"u","metadata":{"\u0075ser_id":"u"}}`,
			want: []string{"an account identifier as a user_id member"},
		},
		"error: a credential header member spelled with a Unicode escape": {
			body: `{"headers":{"\u0061uthorization":"Basic abc"}}`,
			want: []string{"a credential header as a member"},
		},
		"error: an escaped user_id member inside an object in a top-level array": {
			body: `[{"\u0075ser_id":"u"}]`,
			want: []string{"an account identifier as a user_id member"},
		},
		"error: an escaped user_id member after empty containers": {
			body: `{"a":{},"b":[],"\u0075ser_id":"u"}`,
			want: []string{"an account identifier as a user_id member"},
		},
		"error: an escaped user_id member after a string holding invalid UTF-8": {
			body: "{\"note\":\"\xff\",\"\\u0075ser_id\":\"u\"}",
			want: []string{"an account identifier as a user_id member"},
		},
		"error: an escaped user_id member after a number that overflows float64": {
			body: `{"n":1e1000,"\u0075ser_id":"u"}`,
			want: []string{"an account identifier as a user_id member"},
		},
		"success: an escaped user_id as a string value": {
			body: `{"note":"\u0075ser_id","list":["user_id"]}`,
		},
		"success: user_id inside a string": {
			body: `{"error":{"message":"the member user_id is required","code":400}}`,
		},
		"error: a masked echo of an OpenRouter key": {
			body:    `{"error":{"message":"No auth credentials found for sk-or-v1-****************cdef."}}`,
			secrets: []string{syntheticOpenRouterKey},
			want:    []string{"a masked echo of secret 1 of 1"},
		},
		"success: a masked echo of the wrong key the tests send": {
			body:    `{"error":{"message":"Incorrect API key provided: invalid*****************0000."}}`,
			secrets: []string{realKey, wrongLiveKey},
		},
		"error: OpenAI's mask of a key of 107 bytes": {
			body:    `{"error":{"message":"Incorrect API key provided: ` + longEcho + `. You can find your API key at https://platform.openai.com/account/api-keys."}}`,
			secrets: []string{longKey},
			want:    []string{"a masked echo of secret 1 of 1"},
		},
		"success: a secret of 11 bytes has no echo shape": {
			body:    `{"note":"abcdefg---wxyz"}`,
			secrets: []string{"abcdefgwxyz"},
		},
		"error: a secret of 12 bytes has an echo shape": {
			body:    `{"note":"abcdefg---wxyz"}`,
			secrets: []string{"abcdefgXwxyz"},
			want:    []string{"a masked echo of secret 1 of 1"},
		},
		"success: the ends of a known key in two JSON strings": {
			body:    `{"a":"sk-proj","b":"wxyz"}`,
			secrets: []string{realKey},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := credentialFindings([]byte(tt.body), tt.secrets...)
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("credentialFindings (-want +got):\n%s", diff)
			}
			for _, f := range got {
				if strings.Contains(f, "sk-proj") || strings.Contains(f, "wxyz") || strings.Contains(f, "0123456789") {
					t.Errorf("a finding repeats the matched text: %q", f)
				}
			}
			dir := t.TempDir()
			err := writeFixture(dir, "body.json", []byte(tt.body), tt.secrets...)
			_, readErr := os.ReadFile(filepath.Join(dir, "body.json"))
			switch {
			case len(tt.want) > 0 && (err == nil || !errors.Is(readErr, os.ErrNotExist)):
				t.Errorf("writeFixture() error = %v, read error = %v; want a refusal and no file", err, readErr)
			case len(tt.want) == 0 && err != nil:
				t.Errorf("writeFixture() error = %v, want the body written", err)
			}
		})
	}
}
