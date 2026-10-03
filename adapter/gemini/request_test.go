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

package gemini

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// testSchema and testMessages are the SCHEMA and MESSAGES of upstream's
// tests/test_provider_requests.py:39-43.
const testSchema = `{"type":"object","properties":{"answers":{"type":"object"}}}`

var testMessages = []llm.Message{
	{Role: "system", Content: "system prompt"},
	{Role: "user", Content: "the document"},
}

// assertJSON fails t unless got and want are the same JSON value with the
// same member order at every level.
func assertJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	ok, err := jsonx.EqualOrdered(got, []byte(want))
	if err != nil {
		t.Fatalf("EqualOrdered(%s): %v", got, err)
	}
	if !ok {
		t.Errorf("body =\n%s\nwant\n%s", got, want)
	}
}

// TestRequestPutsSchemaInResponseFormat checks the structured request
// (tests/test_provider_requests.py::test_gemini_request_puts_schema_in_response_format_when_structured):
// the system message becomes system_instruction, the user message a
// user_input step, store is false and response_format wraps the schema,
// in upstream's member order. Do sends it as POST to /v1beta/interactions
// under the default base URL, the path of every recorded Gemini exchange.
func TestRequestPutsSchemaInResponseFormat(t *testing.T) {
	body, err := requestBody("gemini-3.8-flash", &llm.Request{Messages: testMessages, Schema: []byte(testSchema), Structured: true})
	if err != nil {
		t.Fatalf("requestBody: %v", err)
	}
	assertJSON(t, body, `{"model":"gemini-3.8-flash","input":[{"type":"user_input","content":[{"type":"text","text":"the document"}]}],"store":false,"system_instruction":"system prompt","response_format":{"type":"text","mime_type":"application/json","schema":`+testSchema+`}}`)

	clearEnv(t)
	tr := &transport{reply: completedBody}
	p := newProvider(t, WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: tr}))
	if _, err := p.Do(t.Context(), &llm.Request{Messages: testMessages, Schema: []byte(testSchema), Structured: true}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	reqs := tr.recorded()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if reqs[0].method != http.MethodPost || reqs[0].url != "https://generativelanguage.googleapis.com/v1beta/interactions" {
		t.Errorf("request = %s %s, want POST https://generativelanguage.googleapis.com/v1beta/interactions", reqs[0].method, reqs[0].url)
	}
	if string(reqs[0].body) != string(body) {
		t.Errorf("sent body =\n%s\nwant the built body\n%s", reqs[0].body, body)
	}
}

// TestRequestOmitsResponseFormatWhenPrompted checks the prompted request
// (tests/test_provider_requests.py::test_gemini_request_omits_response_format_when_prompted):
// no response_format member, and store false.
func TestRequestOmitsResponseFormatWhenPrompted(t *testing.T) {
	body, err := requestBody("gemini-3.8-flash", &llm.Request{Messages: testMessages, Schema: []byte(testSchema)})
	if err != nil {
		t.Fatalf("requestBody: %v", err)
	}
	assertJSON(t, body, `{"model":"gemini-3.8-flash","input":[{"type":"user_input","content":[{"type":"text","text":"the document"}]}],"store":false,"system_instruction":"system prompt"}`)
}

// TestRequestSendsCorrectionTurnsAsSteps checks that a corrective turn is
// sent as steps (tests/test_provider_requests.py::test_gemini_request_sends_correction_turns_as_steps):
// the assistant's answer as a model_output step and the correction as a
// user_input step, after the document.
func TestRequestSendsCorrectionTurnsAsSteps(t *testing.T) {
	messages := slices.Concat(testMessages, []llm.Message{
		{Role: "assistant", Content: `{"answers":`},
		{Role: "user", Content: "fix it"},
	})
	body, err := requestBody("gemini-3.8-flash", &llm.Request{Messages: messages, Schema: []byte(testSchema), Structured: true})
	if err != nil {
		t.Fatalf("requestBody: %v", err)
	}
	doc, err := jsonx.Read(body)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	input, _ := doc.Member("input")
	got, err := input.PydanticJSON()
	if err != nil {
		t.Fatalf("PydanticJSON: %v", err)
	}
	assertJSON(t, got, `[{"type":"user_input","content":[{"type":"text","text":"the document"}]},{"type":"model_output","content":[{"type":"text","text":"{\"answers\":"}]},{"type":"user_input","content":[{"type":"text","text":"fix it"}]}]`)
}

// TestRequestSystemInstruction checks how system messages become
// system_instruction (providers/gemini.py:42,56-57): joined with a blank
// line in their order, wherever they stand, and absent when their contents
// join to the empty string; a role other than system and assistant is a
// user_input step.
func TestRequestSystemInstruction(t *testing.T) {
	tests := map[string]struct {
		messages []llm.Message
		want     string
	}{
		"two system messages are joined with a blank line": {
			messages: []llm.Message{{Role: "system", Content: "a"}, {Role: "user", Content: "u"}, {Role: "system", Content: "b"}},
			want:     `{"model":"m","input":[{"type":"user_input","content":[{"type":"text","text":"u"}]}],"store":false,"system_instruction":"a\n\nb"}`,
		},
		"an empty system message sends no system_instruction": {
			messages: []llm.Message{{Role: "system", Content: ""}, {Role: "user", Content: "u"}},
			want:     `{"model":"m","input":[{"type":"user_input","content":[{"type":"text","text":"u"}]}],"store":false}`,
		},
		"two empty system messages join to a blank line, which is sent": {
			messages: []llm.Message{{Role: "system", Content: ""}, {Role: "system", Content: ""}},
			want:     `{"model":"m","input":[],"store":false,"system_instruction":"\n\n"}`,
		},
		"another role is a user_input step": {
			messages: []llm.Message{{Role: "tool", Content: "t"}},
			want:     `{"model":"m","input":[{"type":"user_input","content":[{"type":"text","text":"t"}]}],"store":false}`,
		},
		"no messages": {
			want: `{"model":"m","input":[],"store":false}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body, err := requestBody("m", &llm.Request{Messages: tt.messages})
			if err != nil {
				t.Fatalf("requestBody: %v", err)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

// TestRequestRefusesASchemaThatIsNotJSON checks that a structured request
// whose schema is not one JSON value is refused before anything is sent or
// recorded, with a text that quotes nothing of the schema.
func TestRequestRefusesASchemaThatIsNotJSON(t *testing.T) {
	tr := &transport{reply: completedBody}
	p := newProvider(t, WithAPIKey("not-a-key"), WithHTTPClient(&http.Client{Transport: tr}))
	trace := new(llm.Trace)
	_, err := p.Do(t.Context(), &llm.Request{Messages: testMessages, Schema: []byte(`{"secret-schema"`), Structured: true, Trace: trace})
	if err == nil || err.Error() != "gemini: the request's schema is not one JSON value" {
		t.Fatalf("Do error = %v, want the schema refusal", err)
	}
	if _, ok := trace.Request(); ok || len(tr.recorded()) != 0 {
		t.Errorf("recorded a request %v, sent %d; want neither", ok, len(tr.recorded()))
	}
}

// completedBody is a completed interaction with one text step and usage.
const completedBody = `{"status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"{\"answers\": {}}"}]}],"usage":{"total_input_tokens":20,"total_output_tokens":5}}`

// TestResultReadsOutputTextAndUsage checks the result of a completed
// interaction (tests/test_provider_requests.py::test_gemini_result_reads_output_text_and_usage):
// the text is google-genai's output_text and the counts are
// total_input_tokens and total_output_tokens. upstream's test gives the
// SDK's computed output_text; here it comes from the steps, so the cases
// after the first pin how it is built: the last run of consecutive text
// items of model_output steps, the walk stopping at a user_input step.
func TestResultReadsOutputTextAndUsage(t *testing.T) {
	tests := map[string]struct {
		steps string
		want  string
	}{
		"one text step, as upstream's test": {
			steps: `[{"type":"model_output","content":[{"type":"text","text":"{\"answers\": {}}"}]}]`,
			want:  `{"answers": {}}`,
		},
		"several text parts are joined in order": {
			steps: `[{"type":"model_output","content":[{"type":"text","text":"a"},{"type":"text","text":"b"},{"type":"text","text":"c"}]}]`,
			want:  "abc",
		},
		"a non-text part ends the run": {
			steps: `[{"type":"model_output","content":[{"type":"text","text":"early"},{"type":"image","data":"x"},{"type":"text","text":"late1"},{"type":"text","text":"late2"}]}]`,
			want:  "late1late2",
		},
		"several model_output steps join into one run": {
			steps: `[{"type":"model_output","content":[{"type":"text","text":"a"}]},{"type":"model_output","content":[{"type":"text","text":"b"}]}]`,
			want:  "ab",
		},
		"a user_input step stops the walk": {
			steps: `[{"type":"model_output","content":[{"type":"text","text":"before"}]},{"type":"user_input","content":[{"type":"text","text":"question"}]},{"type":"model_output","content":[{"type":"text","text":"after"}]}]`,
			want:  "after",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := `{"status":"completed","steps":` + tt.steps + `,"usage":{"total_input_tokens":20,"total_output_tokens":5}}`
			trace := new(llm.Trace)
			got, err := result([]byte(body), trace)
			if err != nil {
				t.Fatalf("result: %v", err)
			}
			want := &llm.Result{Text: tt.want, InputTokens: llm.Count{N: 20, Known: true}, OutputTokens: llm.Count{N: 5, Known: true}}
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("result (-want +got):\n%s", diff)
			}
			if rec, ok := trace.Response(); !ok || string(rec) != body {
				t.Errorf("recorded response %v %s, want the body", ok, rec)
			}
			if f := trace.FinishReason(); f == nil || *f != "completed" {
				t.Errorf("finish reason = %v, want completed", f)
			}
		})
	}
}

// TestOutputTextWalk pins the rest of google-genai 2.24.0's output_text
// rule, each case's text measured on the reference Python with upstream's
// GeminiProvider: a step of another type before the run is skipped and one
// after it ends the run; a model_output step with empty content does not
// end it; null content is skipped before the run and ends it after; an item
// that is not text is skipped before the run; a text item whose text is not
// a string adds nothing; no steps give "".
func TestOutputTextWalk(t *testing.T) {
	tests := map[string]struct {
		steps string
		want  string
	}{
		"a thought step between runs ends the run":      {steps: `[{"type":"model_output","content":[{"type":"text","text":"A"}]},{"type":"thought","signature":"s"},{"type":"model_output","content":[{"type":"text","text":"B"}]}]`, want: "B"},
		"a thought step after the run is skipped":       {steps: `[{"type":"model_output","content":[{"type":"text","text":"A"}]},{"type":"thought","signature":"s"}]`, want: "A"},
		"an empty model_output step does not end a run": {steps: `[{"type":"model_output","content":[{"type":"text","text":"A"}]},{"type":"model_output","content":[]},{"type":"model_output","content":[{"type":"text","text":"B"}]}]`, want: "AB"},
		"an unknown step type is skipped":               {steps: `[{"type":"mystery","content":[{"type":"text","text":"M"}]},{"type":"model_output","content":[{"type":"text","text":"T"}]}]`, want: "T"},
		"a step without a type is skipped":              {steps: `[{"content":[{"type":"text","text":"M"}]}]`, want: ""},
		"content null":                                  {steps: `[{"type":"model_output","content":null}]`, want: ""},
		"content absent":                                {steps: `[{"type":"model_output"}]`, want: ""},
		"content null after the run ends it":            {steps: `[{"type":"model_output","content":[{"type":"text","text":"A"}]},{"type":"model_output","content":null},{"type":"model_output","content":[{"type":"text","text":"B"}]}]`, want: "B"},
		"an unknown item after text ends the run":       {steps: `[{"type":"model_output","content":[{"type":"text","text":"A"},{"type":"mystery"},{"type":"text","text":"B"}]}]`, want: "B"},
		"an item that is not an object is not text":     {steps: `[{"type":"model_output","content":["x",{"type":"text","text":"B"}]}]`, want: "B"},
		"items that are not text before the run":        {steps: `[{"type":"model_output","content":[{"type":"text","text":"A"},{"type":"image"},{"type":"audio"}]}]`, want: "A"},
		"a text item whose text is null":                {steps: `[{"type":"model_output","content":[{"type":"text","text":null}]}]`, want: ""},
		"a text item without text":                      {steps: `[{"type":"model_output","content":[{"type":"text"}]}]`, want: ""},
		"a text item whose text is a number":            {steps: `[{"type":"model_output","content":[{"type":"text","text":5},{"type":"text","text":"B"}]}]`, want: "B"},
		"a user_input step last gives no text":          {steps: `[{"type":"model_output","content":[{"type":"text","text":"A"}]},{"type":"user_input","content":[{"type":"text","text":"U"}]}]`, want: ""},
		"no steps":                                      {steps: `[]`, want: ""},
		"steps null":                                    {steps: `null`, want: ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := `{"status":"completed","steps":` + tt.steps + `,"usage":{"total_input_tokens":3,"total_output_tokens":4}}`
			got, err := result([]byte(body), nil)
			if err != nil {
				t.Fatalf("result: %v", err)
			}
			if got.Text != tt.want {
				t.Errorf("text = %q, want %q", got.Text, tt.want)
			}
		})
	}
	t.Run("steps absent", func(t *testing.T) {
		got, err := result([]byte(`{"status":"completed","usage":{"total_input_tokens":3,"total_output_tokens":4}}`), nil)
		if err != nil || got.Text != "" {
			t.Errorf("result = %+v, %v; want the empty text", got, err)
		}
	})
}

// incompleteText returns upstream's non-answer text for reason.
func incompleteText(reason string) string {
	return "Gemini response did not complete: " + reason + "."
}

// TestIncompleteStatusIsNotAnAnswer checks that a status other than
// completed is a non-answer naming the status
// (tests/test_provider_requests.py::test_gemini_incomplete_status_is_not_treated_as_an_answer),
// with the whole text of providers/gemini.py:82, and that the body was
// recorded with the status as its finish reason before the check.
func TestIncompleteStatusIsNotAnAnswer(t *testing.T) {
	for _, status := range []string{"incomplete", "failed", "cancelled", "budget_exceeded"} {
		t.Run(status, func(t *testing.T) {
			body := `{"status":"` + status + `","steps":[{"type":"model_output","content":[{"type":"text","text":"{\"answers\":{\"positive\":true}}"}]}],"usage":{"total_input_tokens":20,"total_output_tokens":5},"errors":null}`
			trace := new(llm.Trace)
			_, err := result([]byte(body), trace)
			na, ok := errors.AsType[*llm.NonAnswerError](err)
			if !ok || na.Message != incompleteText(status) {
				t.Fatalf("result error = %#v, want the non-answer %q", err, incompleteText(status))
			}
			if f := trace.FinishReason(); f == nil || *f != status {
				t.Errorf("finish reason = %v, want %q", f, status)
			}
			if rec, _ := trace.Response(); string(rec) != body {
				t.Errorf("recorded response %s, want the body", rec)
			}
		})
	}
}

// TestOmittedUsageIsNotAnAnswer checks that a completed interaction without
// usage is the non-answer "Gemini response omitted usage."
// (tests/test_provider_requests.py::test_gemini_omitted_usage_is_not_treated_as_an_answer).
func TestOmittedUsageIsNotAnAnswer(t *testing.T) {
	_, err := result([]byte(`{"status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"{\"answers\":{}}"}]}],"usage":null,"errors":null}`), nil)
	if na, ok := errors.AsType[*llm.NonAnswerError](err); !ok || na.Message != "Gemini response omitted usage." {
		t.Fatalf("result error = %#v, want the omitted-usage non-answer", err)
	}
}
