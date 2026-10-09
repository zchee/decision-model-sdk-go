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

//go:build live

package livetest

import (
	"net/http"
	"os"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter"
	"github.com/zchee/decision-model-sdk-go/adapter/anthropic"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
	"github.com/zchee/decision-model-sdk-go/adapter/openai"
)

// variantRoundTripper checks request paths or header names before forwarding,
// without keeping or reporting credential values.
type variantRoundTripper func(*http.Request) (*http.Response, error)

func (f variantRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestLiveOpenAIChatPrompted(t *testing.T) {
	skipUnlessLive(t, "OPENAI_API_KEY")
	liveChatProbabilities(t, adapter.Prompted, "openai-chat-probabilities-prompted")
}

func TestLiveOpenAIChatStructured(t *testing.T) {
	skipUnlessLive(t, "OPENAI_API_KEY")
	liveChatProbabilities(t, adapter.Structured, "openai-chat-probabilities-structured")
}

func liveChatProbabilities(t *testing.T, output adapter.OutputMode, name string) {
	t.Helper()
	next := recordingTransport(t, name)
	if next == nil {
		next = http.DefaultTransport
	}
	capture := variantRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.Host != "api.openai.com" || req.URL.Path != "/v1/chat/completions" {
			t.Fatal("the request did not select the public Chat Completions endpoint")
		}
		return next.RoundTrip(req)
	})
	factory := func(model string) (llm.Provider, error) {
		return openai.New(model, openai.WithAPI(openai.ChatCompletions), openai.WithBaseURL("https://api.openai.com/v1"), openai.WithPromptedResponseFormat(openai.FormatNull), openai.WithHTTPClient(&http.Client{Transport: capture}))
	}
	variantProbabilities(t, "openai", "gpt-4o-mini", output, factory)
}

// credentialIsolationReason requires the other variable to be absent, rather
// than empty: an exported empty value must not count as an isolated variant.
func credentialIsolationReason(present func(string) bool, own, other string) string {
	if !present(own) {
		return own + " is not set"
	}
	if present(other) {
		return other + " must be unset for the isolated credential variant"
	}
	return ""
}

// environmentPresent discards the value and keeps only LookupEnv's presence bit.
func environmentPresent(name string) bool {
	_, present := os.LookupEnv(name)
	return present
}

func TestAnthropicCredentialIsolation(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		env        map[string]string
		own, other string
		want       string
	}{
		"success: key only":          {env: map[string]string{"ANTHROPIC_API_KEY": "synthetic"}, own: "ANTHROPIC_API_KEY", other: "ANTHROPIC_AUTH_TOKEN"},
		"success: token only":        {env: map[string]string{"ANTHROPIC_AUTH_TOKEN": "synthetic"}, own: "ANTHROPIC_AUTH_TOKEN", other: "ANTHROPIC_API_KEY"},
		"success: own presence only": {env: map[string]string{"ANTHROPIC_API_KEY": ""}, own: "ANTHROPIC_API_KEY", other: "ANTHROPIC_AUTH_TOKEN"},
		"skip: key absent":           {own: "ANTHROPIC_API_KEY", other: "ANTHROPIC_AUTH_TOKEN", want: "ANTHROPIC_API_KEY is not set"},
		"skip: token absent":         {own: "ANTHROPIC_AUTH_TOKEN", other: "ANTHROPIC_API_KEY", want: "ANTHROPIC_AUTH_TOKEN is not set"},
		"skip: both set for key":     {env: map[string]string{"ANTHROPIC_API_KEY": "synthetic", "ANTHROPIC_AUTH_TOKEN": "synthetic"}, own: "ANTHROPIC_API_KEY", other: "ANTHROPIC_AUTH_TOKEN", want: "ANTHROPIC_AUTH_TOKEN must be unset for the isolated credential variant"},
		"skip: both set for token":   {env: map[string]string{"ANTHROPIC_API_KEY": "synthetic", "ANTHROPIC_AUTH_TOKEN": "synthetic"}, own: "ANTHROPIC_AUTH_TOKEN", other: "ANTHROPIC_API_KEY", want: "ANTHROPIC_API_KEY must be unset for the isolated credential variant"},
		"skip: empty other token":    {env: map[string]string{"ANTHROPIC_API_KEY": "synthetic", "ANTHROPIC_AUTH_TOKEN": ""}, own: "ANTHROPIC_API_KEY", other: "ANTHROPIC_AUTH_TOKEN", want: "ANTHROPIC_AUTH_TOKEN must be unset for the isolated credential variant"},
		"skip: empty other key":      {env: map[string]string{"ANTHROPIC_API_KEY": "", "ANTHROPIC_AUTH_TOKEN": "synthetic"}, own: "ANTHROPIC_AUTH_TOKEN", other: "ANTHROPIC_API_KEY", want: "ANTHROPIC_API_KEY must be unset for the isolated credential variant"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			present := func(name string) bool {
				_, exists := tt.env[name]
				return exists
			}
			if diff := gocmp.Diff(tt.want, credentialIsolationReason(present, tt.own, tt.other)); diff != "" {
				t.Errorf("credential isolation (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLiveAnthropicKeyOnly(t *testing.T) {
	liveAnthropicProbabilities(t, "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "anthropic-key-only-probabilities-prompted", "X-Api-Key")
}

func TestLiveAnthropicTokenOnly(t *testing.T) {
	liveAnthropicProbabilities(t, "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "anthropic-token-only-probabilities-prompted", "Authorization")
}

func liveAnthropicProbabilities(t *testing.T, own, other, name, header string) {
	t.Helper()
	if os.Getenv(liveTestsVar) != "1" {
		t.Skip(liveTestsVar + " is not 1")
	}
	if reason := credentialIsolationReason(environmentPresent, own, other); reason != "" {
		t.Skip(reason)
	}
	next := recordingTransport(t, name)
	if next == nil {
		next = http.DefaultTransport
	}
	capture := variantRoundTripper(func(req *http.Request) (*http.Response, error) {
		_, keyPresent := req.Header["X-Api-Key"]
		_, tokenPresent := req.Header["Authorization"]
		if keyPresent != (header == "X-Api-Key") || tokenPresent != (header == "Authorization") {
			t.Fatal("the request credential header names do not match the isolated variant")
		}
		return next.RoundTrip(req)
	})
	factory := func(model string) (llm.Provider, error) {
		return anthropic.New(model, anthropic.WithBaseURL("https://api.anthropic.com"), anthropic.WithHTTPClient(&http.Client{Transport: capture}))
	}
	variantProbabilities(t, "anthropic", "claude-haiku-4-5", adapter.Prompted, factory)
}

func variantProbabilities(t *testing.T, provider, model string, output adapter.OutputMode, factory llm.Factory) {
	t.Helper()
	ad, err := adapter.New(adapter.Probabilities, output, adapter.WithFactory(provider, factory))
	if err != nil {
		t.Fatal(err)
	}
	client, err := adapter.NewClient(ad)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	resp, err := client.SystemOne(t.Context(), liveState, liveQuestions(t), decision.Model(adapter.ModelID(provider, model)))
	if err != nil {
		t.Fatalf("SystemOne against the live provider failed: %v", err)
	}
	checkExpectedProbabilities(t, resp.Answers())
}
