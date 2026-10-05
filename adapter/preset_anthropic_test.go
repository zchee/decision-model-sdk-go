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
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"
	"github.com/zchee/decision-model-sdk-go/adapter/anthropic"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// anthropicNoCredentialText is anthropic.New's error without a credential.
const anthropicNoCredentialText = "anthropic: no credential: set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN, or pass WithAPIKey or WithAuthToken" //nolint:gosec // G101: an error text that names where a credential comes from.

// clearAnthropicEnv removes the variables anthropic.New reads for the rest
// of the test and restores them afterwards.
func clearAnthropicEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("Unsetenv(%s): %v", name, err)
		}
	}
}

// TestAnthropicPreset pins the preset factory "anthropic": an Adapter built
// without WithFactory("anthropic", …) builds an *anthropic.Provider for the
// model of "anthropic:<model>" from the environment, WithFactory replaces
// the preset, and without a credential a call is refused with 400
// provider_config and anthropic.New's text before any provider exists, so
// no request is sent; the failing preset returns no provider at all, not a
// nil *anthropic.Provider the Adapter would keep; and the preset passes New
// no option, so an invalid ANTHROPIC_BASE_URL fails it with New's own text
// and nothing is built.
func TestAnthropicPreset(t *testing.T) {
	t.Run("success: the preset builds an anthropic Provider", func(t *testing.T) {
		clearAnthropicEnv(t)
		t.Setenv("ANTHROPIC_API_KEY", "not-a-key")
		ad, err := New(Discrete, Structured)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer ad.Close()
		p, err := ad.provider(target{name: "anthropic", model: "test-model"})
		if err != nil {
			t.Fatalf("provider: %v", err)
		}
		if _, ok := p.(*anthropic.Provider); !ok {
			t.Fatalf("provider = %T, want *anthropic.Provider", p)
		}
		if p.Model() != "test-model" {
			t.Errorf("Model = %q, want test-model", p.Model())
		}
		if got := providerName(p); got != "github.com/zchee/decision-model-sdk-go/adapter/anthropic.Provider" {
			t.Errorf("provider name = %q", got)
		}
	})
	t.Run("success: WithFactory replaces the preset", func(t *testing.T) {
		clearAnthropicEnv(t)
		ad, err := New(Discrete, Structured, WithFactory("anthropic", func(string) (llm.Provider, error) { return plainProvider{}, nil }))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer ad.Close()
		p, err := ad.provider(target{name: "anthropic", model: "test-model"})
		if err != nil {
			t.Fatalf("provider: %v", err)
		}
		if _, ok := p.(plainProvider); !ok {
			t.Errorf("provider = %T, want the WithFactory provider", p)
		}
	})
	t.Run("error: no credential is a 400 provider_config", func(t *testing.T) {
		clearAnthropicEnv(t)
		ad, err := New(Discrete, Structured)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		c, err := NewClient(ad)
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		defer c.Close()
		_, err = c.SystemOne(t.Context(), "state", noulQuestions(t), decision.Model(ModelID("anthropic", "test-model")))
		apiErr, ok := errors.AsType[*decision.APIError](err)
		if !ok {
			t.Fatalf("SystemOne error = %T %v, want *decision.APIError", err, err)
		}
		if apiErr.StatusCode != http.StatusBadRequest || apiErr.ErrorType != "provider_config" {
			t.Errorf("error = %d %s, want 400 provider_config", apiErr.StatusCode, apiErr.ErrorType)
		}
		if diff := gocmp.Diff(anthropicNoCredentialText, apiErr.Message); diff != "" {
			t.Errorf("message (-want +got):\n%s", diff)
		}
		ad.cacheMu.Lock()
		owned := len(ad.owned)
		ad.cacheMu.Unlock()
		if owned != 0 {
			t.Errorf("the Adapter holds %d providers, want none", owned)
		}
	})
	t.Run("error: the preset returns no provider", func(t *testing.T) {
		clearAnthropicEnv(t)
		p, err := anthropicPreset("test-model")
		if p != nil || err == nil || err.Error() != anthropicNoCredentialText {
			t.Errorf("anthropicPreset = %T (nil %t), %v; want a nil provider and anthropic.New's error", p, p == nil, err)
		}
	})
	t.Run("error: the preset passes no option, so ANTHROPIC_BASE_URL is read", func(t *testing.T) {
		clearAnthropicEnv(t)
		t.Setenv("ANTHROPIC_API_KEY", "not-a-key")
		t.Setenv("ANTHROPIC_BASE_URL", "notaurl")
		p, err := anthropicPreset("test-model")
		if err == nil || p != nil {
			t.Fatalf("anthropicPreset() = %T (nil %t), %v; want no provider and the base-URL error", p, p == nil, err)
		}
		const want = "anthropic: ANTHROPIC_BASE_URL: the base URL is not an absolute http or https URL with a host"
		if err.Error() != want {
			t.Errorf("anthropicPreset() error = %q, want %q", err, want)
		}
	})
}
