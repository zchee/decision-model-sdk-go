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

package provider_test

import (
	"errors"
	"net/http"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"
	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
	"github.com/zchee/decision-model-sdk-go/provider"
)

// presetKeyEnvs is every variable a preset reads; each test clears all of
// them, so a developer's exported key never reaches a test.
var presetKeyEnvs = []string{"TYPESAFE_API_KEY", "CODIV_API_KEY", "PERPLEXITY_API_KEY"}

// setEnv clears every variable a client may read, then sets the variables
// in set; t.Setenv restores them all when the test ends. The generic
// variables are set to values a preset's client must not use, so a client
// that read one would send elsewhere or with another key.
func setEnv(t *testing.T, set map[string]string) {
	t.Helper()
	for _, name := range presetKeyEnvs {
		t.Setenv(name, "")
	}
	t.Setenv(decision.APIKeyEnv, "generic-key-not-for-a-preset")
	t.Setenv(decision.BaseURLEnv, "https://generic.example.test")
	t.Setenv(decision.DefaultModelEnv, "generic-model")
	for name, value := range set {
		t.Setenv(name, value)
	}
}

// TestPresets pins each preset's whole Provider: the vendor's base URL, its
// paths, the variable its key is read from, and no default model.
func TestPresets(t *testing.T) {
	tests := map[string]struct {
		got  decision.Provider
		want decision.Provider
	}{
		"success: TypeSafe": {
			got: provider.TypeSafe(),
			want: decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
				Name: "TypeSafe AI", BaseURL: "https://api.typesafe.ai",
				SystemOnePath: "/v1/systemone", ModelsPath: "/v1/models", APIKeyEnv: "TYPESAFE_API_KEY",
			},
		},
		"success: Codiv": {
			got: provider.Codiv(),
			want: decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
				Name: "Codiv", BaseURL: "https://api.codiv.ai",
				SystemOnePath: "/v1/systemone", ModelsPath: "/v1/models", APIKeyEnv: "CODIV_API_KEY",
			},
		},
		"success: Perplexity": {
			got: provider.Perplexity(),
			want: decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
				Name: "Perplexity", BaseURL: "https://api.perplexity.ai",
				SystemOnePath: "/v1/decisions", APIKeyEnv: "PERPLEXITY_API_KEY",
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := gocmp.Diff(tt.want, tt.got); diff != "" {
				t.Errorf("preset (-want +got):\n%s", diff)
			}
		})
	}
}

// TestPresetClients builds a client of each preset with NewClient, with only
// the preset's variable set among the preset variables and the generic ones
// set to other values, and pins what it sends: the System One call and, where
// the vendor lists models, the listing go to the vendor's URLs with the
// preset variable's key; a preset without a listing refuses List before
// sending.
func TestPresetClients(t *testing.T) {
	const key = "sk-preset-test-key-0123456789"
	tests := map[string]struct {
		preset        decision.Provider
		env           string
		wantSystemOne string
		wantModels    string // "" when List must fail before sending
	}{
		"success: TypeSafe": {
			preset: provider.TypeSafe(), env: "TYPESAFE_API_KEY",
			wantSystemOne: "https://api.typesafe.ai/v1/systemone", wantModels: "https://api.typesafe.ai/v1/models",
		},
		"success: Codiv": {
			preset: provider.Codiv(), env: "CODIV_API_KEY",
			wantSystemOne: "https://api.codiv.ai/v1/systemone", wantModels: "https://api.codiv.ai/v1/models",
		},
		"success: Perplexity": {
			preset: provider.Perplexity(), env: "PERPLEXITY_API_KEY",
			wantSystemOne: "https://api.perplexity.ai/v1/decisions",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			setEnv(t, map[string]string{tt.env: key})
			result := testsupport.JSON(http.StatusOK, testsupport.Fixture(t, "result.json"))
			models := testsupport.JSON(http.StatusOK, testsupport.Fixture(t, "models.json"))
			rec := &testsupport.Recorder{Replies: []testsupport.Reply{result, models}}
			c, err := decision.NewClient(decision.WithProvider(tt.preset), decision.WithRoundTripper(rec), decision.WithRetry(decision.NoRetry()))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			t.Cleanup(func() { _ = c.Close() })
			qs, err := decision.NewQuestions().Noul("q", decision.Noul{Instructions: decision.Text("?")}).Prepare()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.SystemOne(t.Context(), "hello", qs, decision.Model("vendor-model")); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			_, err = c.Models().List(t.Context())
			if tt.wantModels == "" {
				if _, ok := errors.AsType[*decision.ConfigError](err); !ok {
					t.Fatalf("List error = %T %v, want a *ConfigError", err, err)
				}
			} else if err != nil {
				t.Fatalf("List: %v", err)
			}
			type sent struct{ URL, Authorization string }
			var got []sent
			for _, r := range rec.Requests() {
				got = append(got, sent{URL: r.URL, Authorization: r.Header.Get("Authorization")})
			}
			want := []sent{{URL: tt.wantSystemOne, Authorization: "Bearer " + key}}
			if tt.wantModels != "" {
				want = append(want, sent{URL: tt.wantModels, Authorization: "Bearer " + key})
			}
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("requests (-want +got):\n%s", diff)
			}
		})
	}
}

// TestPresetWithoutItsVariable pins that a preset's client is not built
// without its own variable, though DECISION_MODEL_API_KEY is set: the error
// names the preset's variable, and the generic key is never used.
func TestPresetWithoutItsVariable(t *testing.T) {
	setEnv(t, nil)
	c, err := decision.NewClient(decision.WithProvider(provider.Codiv()))
	if err == nil {
		_ = c.Close()
		t.Fatal("NewClient succeeded with only the generic key set")
	}
	const want = "No API key was provided. Pass WithAPIKey or set the CODIV_API_KEY environment variable."
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
