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
	"os"
	"testing"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/fake"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
	"github.com/zchee/decision-model-sdk-go/adapter/openai"
)

// clearOpenAIEnv unsets the variables openai.New reads for the rest of the
// test; t.Setenv restores each one when the test ends.
func clearOpenAIEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unsetting %s: %v", name, err)
		}
	}
}

// TestOpenAIPreset pins the factory preset under the name openai: without
// WithFactory, "openai:<model>" builds an *openai.Provider for the model
// from the environment, closed by Close; without a key the call fails with
// 400 provider_config and the provider's own text, before any provider
// exists to send a request; the preset passes New no option, so an invalid
// OPENAI_BASE_URL fails it with New's own text and nothing is built; and
// WithFactory replaces the preset.
func TestOpenAIPreset(t *testing.T) {
	t.Run("success: openai:<model> builds an *openai.Provider", func(t *testing.T) {
		clearOpenAIEnv(t)
		t.Setenv("OPENAI_API_KEY", "not-a-key")
		ad := newAdapter(t)
		tgt, err := ad.resolve(ModelID("openai", "gpt-test"))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		p, err := ad.provider(tgt)
		if err != nil {
			t.Fatalf("provider: %v", err)
		}
		op, ok := p.(*openai.Provider)
		if !ok {
			t.Fatalf("provider = %T, want *openai.Provider", p)
		}
		if op.Model() != "gpt-test" || op.API() != openai.Responses {
			t.Errorf("provider model %q, API %d; want gpt-test and Responses for the default base URL", op.Model(), op.API())
		}
		if err := ad.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	t.Run("error: no key is 400 provider_config", func(t *testing.T) {
		clearOpenAIEnv(t)
		ad := newAdapter(t, WithDefaultModel(ModelID("openai", "gpt-test")))
		c := sdkClient(t, ad, false)
		_, err := c.SystemOne(t.Context(), "state", noulQuestions(t))
		status, errorType, message, report := apiErrorParts(err)
		if status != 400 || errorType != "provider_config" || message != "openai: no API key: set OPENAI_API_KEY or pass WithAPIKey" || report {
			t.Errorf("SystemOne error: %d %q %q report %t; want 400 provider_config with the missing-key text and no Report", status, errorType, message, report)
		}
		if len(ad.owned) != 0 {
			t.Errorf("the Adapter cached %d providers after a failed build, want 0", len(ad.owned))
		}
	})

	t.Run("error: the preset passes no option, so OPENAI_BASE_URL is read", func(t *testing.T) {
		clearOpenAIEnv(t)
		t.Setenv("OPENAI_API_KEY", "not-a-key")
		t.Setenv("OPENAI_BASE_URL", "notaurl")
		p, err := openaiPreset("gpt-test")
		if err == nil || p != nil {
			t.Fatalf("openaiPreset() = %v, %v; want no provider and the base-URL error", p, err)
		}
		const want = "openai: the base URL of OPENAI_BASE_URL is not an absolute http or https URL with a host"
		if err.Error() != want {
			t.Errorf("openaiPreset() error = %q, want %q", err, want)
		}
	})

	t.Run("success: WithFactory replaces the preset", func(t *testing.T) {
		clearOpenAIEnv(t)
		p := fake.New(fake.Text(noulAnswer))
		ad := newAdapter(t, WithFactory("openai", func(string) (llm.Provider, error) { return p, nil }), WithDefaultModel(ModelID("openai", "gpt-test")))
		c := sdkClient(t, ad, false)
		if _, err := c.SystemOne(t.Context(), "state", noulQuestions(t), decision.Model(ModelID("openai", "gpt-test"))); err != nil {
			t.Fatalf("SystemOne: %v", err)
		}
	})
}
