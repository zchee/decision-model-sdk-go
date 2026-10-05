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

package anthropic

import (
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// The fixtures of upstream's tests/test_provider_requests.py:39-43.
const testSchema = `{"type":"object","properties":{"answers":{"type":"object"}}}`

var testMessages = []llm.Message{
	{Role: "system", Content: "system prompt"},
	{Role: "user", Content: "the document"},
}

// TestRequestPutsSchemaInOutputConfig ports
// tests/test_provider_requests.py::test_anthropic_request_puts_schema_in_output_config_when_structured:
// in structured mode the schema travels as output_config.format, of type
// json_schema, after model, max_tokens, system and messages. The Go test
// compares the whole body, member order included, where upstream compares
// the members one by one.
func TestRequestPutsSchemaInOutputConfig(t *testing.T) {
	clearEnv(t)
	p, err := New("claude-haiku-4-5", WithAPIKey("not-a-key"), WithMaxTokens(4096))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body, err := p.requestBody(&llm.Request{Messages: testMessages, Schema: []byte(testSchema), Structured: true})
	if err != nil {
		t.Fatalf("requestBody: %v", err)
	}
	want := `{"model":"claude-haiku-4-5","max_tokens":4096,"system":"system prompt","messages":[{"role":"user","content":"the document"}],` +
		`"output_config":{"format":{"type":"json_schema","schema":` + testSchema + `}}}`
	if diff := gocmp.Diff(want, string(body)); diff != "" {
		t.Errorf("structured request body (-want +got):\n%s", diff)
	}
}

// TestRequestOmitsOutputConfigWhenPrompted ports
// tests/test_provider_requests.py::test_anthropic_request_omits_output_config_when_prompted:
// in prompted mode the body has no output_config member; the schema is in
// the system prompt the Adapter wrote.
func TestRequestOmitsOutputConfigWhenPrompted(t *testing.T) {
	clearEnv(t)
	p, err := New("claude-haiku-4-5", WithAPIKey("not-a-key"), WithMaxTokens(4096))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body, err := p.requestBody(&llm.Request{Messages: testMessages, Schema: []byte(testSchema), Structured: false})
	if err != nil {
		t.Fatalf("requestBody: %v", err)
	}
	want := `{"model":"claude-haiku-4-5","max_tokens":4096,"system":"system prompt","messages":[{"role":"user","content":"the document"}]}`
	if diff := gocmp.Diff(want, string(body)); diff != "" {
		t.Errorf("prompted request body (-want +got):\n%s", diff)
	}
}

// TestResultJoinsTextBlocks ports
// tests/test_provider_requests.py::test_anthropic_result_joins_text_blocks_and_reads_usage:
// the text is the text of every block of type text, joined in order, and a
// block of another type is skipped even when it carries a text member of
// its own; the counts are usage.input_tokens and usage.output_tokens.
func TestResultJoinsTextBlocks(t *testing.T) {
	body := `{"stop_reason":"end_turn","content":[` +
		`{"type":"text","text":"{\"answers\":"},` +
		`{"type":"thinking","text":"ignored"},` +
		`{"type":"text","text":" {}}"}` +
		`],"usage":{"input_tokens":20,"output_tokens":5}}`
	got, err := result([]byte(body), nil)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	want := &llm.Result{
		Text:         `{"answers": {}}`,
		InputTokens:  llm.Count{N: 20, Known: true},
		OutputTokens: llm.Count{N: 5, Known: true},
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("result (-want +got):\n%s", diff)
	}
}

// TestNewRejectsNonpositiveMaxTokens ports
// tests/test_provider_requests.py::test_anthropic_rejects_nonpositive_output_limit:
// New refuses an output limit of 0 or less with upstream's ValueError text,
// byte for byte (providers/anthropic.py:85-86). The environment holds no
// credential, so the case also pins that the limit is checked first, as
// upstream checks it before it builds its client.
func TestNewRejectsNonpositiveMaxTokens(t *testing.T) {
	tests := map[string]struct {
		maxTokens int
	}{
		"error: zero":      {maxTokens: 0},
		"error: minus one": {maxTokens: -1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			p, err := New("claude-haiku-4-5", WithMaxTokens(tt.maxTokens))
			if p != nil || err == nil {
				t.Fatalf("New(WithMaxTokens(%d)) = %T (nil %t), %v; want nil and an error", tt.maxTokens, p, p == nil, err)
			}
			if diff := gocmp.Diff("max_tokens must be > 0", err.Error()); diff != "" {
				t.Errorf("error text (-want +got):\n%s", diff)
			}
		})
	}
}
