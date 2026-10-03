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

package openai

import (
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// TestUnfinishedResponsesAreNotAnswers ports
// tests/test_openai_transports.py::test_unfinished_responses_are_not_treated_as_answers:
// a Responses status other than completed is a non-answer whose reason is
// error.message for a failed response and incomplete_details.reason for an
// incomplete one, even when the output holds an answer.
func TestUnfinishedResponsesAreNotAnswers(t *testing.T) {
	const output = `"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"answers\":{\"positive\":true}}"}]}]`
	tests := map[string]struct {
		body string
		want string
	}{
		"error: failed": {
			body: `{"status":"failed","error":{"message":"generation failed"},"incomplete_details":null,` + output + `}`,
			want: "OpenAI response did not complete: generation failed.",
		},
		"error: incomplete": {
			body: `{"status":"incomplete","error":null,"incomplete_details":{"reason":"max_output_tokens"},` + output + `}`,
			want: "OpenAI response did not complete: max_output_tokens.",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := responsesResult([]byte(tt.body), nil)
			checkResult(t, got, err, nil, tt.want, true)
		})
	}
}

// TestResponsesResult pins the Responses result parser
// (providers/openai.py:72-90): the body is recorded with its status before
// any check; the text joins every output_text part of every message item
// in order and skips other item types; a refusal part of any message item
// is a non-answer with upstream's text, which ends without a period; a
// reason or refusal that is null or absent is written None; the counts are
// usage.input_tokens and usage.output_tokens; a status, reason or refusal
// that is null or absent is written None and an integer its digits; and a
// body that is not a Responses API response, or such a value of another
// kind, is a plain error of a fixed text.
func TestResponsesResult(t *testing.T) {
	const reasoning = `{"id":"reasoning-test","type":"reasoning","summary":[{"type":"summary_text","text":"Ignored summary."}]}`
	tests := map[string]struct {
		body       string
		want       *llm.Result
		wantErr    string
		nonAnswer  bool
		wantReason *string
	}{
		"success: a reasoning item is skipped": {
			body:       `{"status":"completed","output":[` + reasoning + `,{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":12,"output_tokens":7,"total_tokens":19}}`,
			want:       &llm.Result{Text: "answer", InputTokens: known(12), OutputTokens: known(7)},
			wantReason: new("completed"),
		},
		"success: every output_text part of every message, in order": {
			body: `{"status":"completed","output":[` +
				`{"type":"message","content":[{"type":"output_text","text":"one "},{"type":"output_text","text":"two "}]},` +
				`{"type":"function_call","content":[{"type":"output_text","text":"not read"}]},` +
				`{"type":"message","content":[{"type":"annotation","text":"not read"},{"text":"no type, not read"},{"type":"output_text","text":"three"}]}]}`,
			want:       &llm.Result{Text: "one two three"},
			wantReason: new("completed"),
		},
		"success: no message is the empty text": {
			body:       `{"status":"completed","output":[` + reasoning + `]}`,
			want:       &llm.Result{},
			wantReason: new("completed"),
		},
		"success: an item without a type is skipped": {
			body:       `{"status":"completed","output":[{"content":7},{"type":1},{"type":"message","content":[{"type":"output_text","text":"t"}]}]}`,
			want:       &llm.Result{Text: "t"},
			wantReason: new("completed"),
		},
		"success: usage null": {
			body:       `{"status":"completed","output":[],"usage":null}`,
			want:       &llm.Result{},
			wantReason: new("completed"),
		},
		"success: counts null or absent, total_tokens not read": {
			body:       `{"status":"completed","output":[],"usage":{"output_tokens":null,"total_tokens":19}}`,
			want:       &llm.Result{},
			wantReason: new("completed"),
		},
		"error: a refusal": {
			body:       `{"status":"completed","output":[` + reasoning + `,{"type":"message","content":[{"type":"refusal","refusal":"Cannot evaluate this request."}]}]}`,
			wantErr:    "OpenAI response was a refusal: Cannot evaluate this request.",
			nonAnswer:  true,
			wantReason: new("completed"),
		},
		"error: a refusal after an answer": {
			body:       `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"answer"}]},{"type":"message","content":[{"type":"refusal","refusal":"No."}]}]}`,
			wantErr:    "OpenAI response was a refusal: No.",
			nonAnswer:  true,
			wantReason: new("completed"),
		},
		"error: a refusal before an output_text part that cannot be read": {
			body:       `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":null}]},{"type":"message","content":[{"type":"refusal","refusal":"No."}]}]}`,
			wantErr:    "OpenAI response was a refusal: No.",
			nonAnswer:  true,
			wantReason: new("completed"),
		},
		"error: a refusal that is null": {
			body:       `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":null}]}]}`,
			wantErr:    "OpenAI response was a refusal: None",
			nonAnswer:  true,
			wantReason: new("completed"),
		},
		"error: a refusal that is absent": {
			body:       `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal"}]}]}`,
			wantErr:    "OpenAI response was a refusal: None",
			nonAnswer:  true,
			wantReason: new("completed"),
		},
		"error: in progress, the status is the reason": {
			body:       `{"status":"in_progress","error":null,"incomplete_details":null}`,
			wantErr:    "OpenAI response did not complete: in_progress.",
			nonAnswer:  true,
			wantReason: new("in_progress"),
		},
		"error: failed without error or details": {
			body:       `{"status":"failed"}`,
			wantErr:    "OpenAI response did not complete: failed.",
			nonAnswer:  true,
			wantReason: new("failed"),
		},
		"error: error before incomplete_details": {
			body:       `{"status":"incomplete","error":{"code":"server_error","message":"generation failed"},"incomplete_details":{"reason":"max_output_tokens"}}`,
			wantErr:    "OpenAI response did not complete: generation failed.",
			nonAnswer:  true,
			wantReason: new("incomplete"),
		},
		"error: an error message that is null": {
			body:       `{"status":"failed","error":{"message":null}}`,
			wantErr:    "OpenAI response did not complete: None.",
			nonAnswer:  true,
			wantReason: new("failed"),
		},
		"error: an error without a message": {
			body:       `{"status":"failed","error":{"code":"server_error"},"incomplete_details":{"reason":"max_output_tokens"}}`,
			wantErr:    "OpenAI response did not complete: None.",
			nonAnswer:  true,
			wantReason: new("failed"),
		},
		"error: an incomplete reason that is null": {
			body:       `{"status":"incomplete","incomplete_details":{"reason":null}}`,
			wantErr:    "OpenAI response did not complete: None.",
			nonAnswer:  true,
			wantReason: new("incomplete"),
		},
		"error: not JSON": {
			body:    `not JSON`,
			wantErr: errNotResponses.Error(),
		},
		"error: an array": {
			body:    `[]`,
			wantErr: errNotResponses.Error(),
		},
		"error: status absent is None": {
			body:      `{"output":[]}`,
			wantErr:   "OpenAI response did not complete: None.",
			nonAnswer: true,
		},
		"error: status null is None": {
			body:      `{"status":null,"output":[]}`,
			wantErr:   "OpenAI response did not complete: None.",
			nonAnswer: true,
		},
		"error: an empty object is None": {
			body:      `{}`,
			wantErr:   "OpenAI response did not complete: None.",
			nonAnswer: true,
		},
		"error: status an integer is its digits": {
			body:       `{"status":200,"output":[]}`,
			wantErr:    "OpenAI response did not complete: 200.",
			nonAnswer:  true,
			wantReason: new("200"),
		},
		"error: status an integer, error before it": {
			body:       `{"status":1,"error":{"message":"generation failed"}}`,
			wantErr:    "OpenAI response did not complete: generation failed.",
			nonAnswer:  true,
			wantReason: new("1"),
		},
		"error: status a fraction": {
			body:    `{"status":2.5,"output":[]}`,
			wantErr: errNotResponses.Error(),
		},
		"error: status false": {
			body:    `{"status":false,"output":[]}`,
			wantErr: errNotResponses.Error(),
		},
		"error: status an array": {
			body:    `{"status":["completed"],"output":[]}`,
			wantErr: errNotResponses.Error(),
		},
		"error: error a string": {
			body:       `{"status":"failed","error":"generation failed"}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("failed"),
		},
		"error: an error message that is an integer": {
			body:       `{"status":"failed","error":{"message":500}}`,
			wantErr:    "OpenAI response did not complete: 500.",
			nonAnswer:  true,
			wantReason: new("failed"),
		},
		"error: an error message that is a bool": {
			body:       `{"status":"failed","error":{"message":true}}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("failed"),
		},
		"error: an incomplete reason that is an integer": {
			body:       `{"status":"incomplete","incomplete_details":{"reason":7}}`,
			wantErr:    "OpenAI response did not complete: 7.",
			nonAnswer:  true,
			wantReason: new("incomplete"),
		},
		"error: an incomplete reason with an exponent": {
			body:       `{"status":"incomplete","incomplete_details":{"reason":1e2}}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("incomplete"),
		},
		"error: incomplete_details an array": {
			body:       `{"status":"incomplete","incomplete_details":["max_output_tokens"]}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("incomplete"),
		},
		"error: output absent": {
			body:       `{"status":"completed"}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("completed"),
		},
		"error: output an object": {
			body:       `{"status":"completed","output":{}}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("completed"),
		},
		"error: an item that is not an object": {
			body:       `{"status":"completed","output":["message"]}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("completed"),
		},
		"error: a message without content": {
			body:       `{"status":"completed","output":[{"type":"message"}]}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("completed"),
		},
		"error: a part that is not an object": {
			body:       `{"status":"completed","output":[{"type":"message","content":["text"]}]}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("completed"),
		},
		"error: a refusal that is an integer": {
			body:       `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":1}]}]}`,
			wantErr:    "OpenAI response was a refusal: 1",
			nonAnswer:  true,
			wantReason: new("completed"),
		},
		"error: a refusal that is an array": {
			body:       `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":["no"]}]}]}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("completed"),
		},
		"error: an output_text part whose text is null": {
			body:       `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":null}]}]}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("completed"),
		},
		"error: an output_text part without text": {
			body:       `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text"}]}]}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("completed"),
		},
		"error: a count that is a float": {
			body:       `{"status":"completed","output":[],"usage":{"input_tokens":1e3}}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("completed"),
		},
		"error: usage an array": {
			body:       `{"status":"completed","output":[],"usage":[]}`,
			wantErr:    errNotResponses.Error(),
			wantReason: new("completed"),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			trace := new(llm.Trace)
			got, err := responsesResult([]byte(tt.body), trace)
			checkResult(t, got, err, tt.want, tt.wantErr, tt.nonAnswer)
			checkRecorded(t, trace, tt.body, tt.wantReason)
		})
	}
}

// TestResponsesBody pins the Responses request body
// (providers/openai.py:56-69): in structured mode model, input, text, store
// and instructions, in that order, input without the system messages and
// instructions holding their contents joined by a blank line; in prompted
// mode model, input, text and store, every message in input and a
// json_object format.
func TestResponsesBody(t *testing.T) {
	tests := map[string]struct {
		req  *llm.Request
		want string
	}{
		"success: structured": {
			req: &llm.Request{
				Messages: []llm.Message{
					{Role: "system", Content: "first system"},
					{Role: "user", Content: "the document"},
					{Role: "system", Content: "second system"},
					{Role: "assistant", Content: "{\"answers\":"},
				},
				Schema:     []byte(requestSchema),
				Structured: true,
			},
			want: `{"model":"test-model","input":[{"role":"user","content":"the document"},{"role":"assistant","content":"{\"answers\":"}],` +
				`"text":{"format":{"type":"json_schema","name":"evaluation","schema":` + requestSchema + `,"strict":true}},` +
				`"store":false,"instructions":"first system\n\nsecond system"}`,
		},
		"success: structured without a system message": {
			req:  &llm.Request{Messages: []llm.Message{{Role: "user", Content: "the document"}}, Schema: []byte(requestSchema), Structured: true},
			want: `{"model":"test-model","input":[{"role":"user","content":"the document"}],"text":{"format":{"type":"json_schema","name":"evaluation","schema":` + requestSchema + `,"strict":true}},"store":false,"instructions":""}`,
		},
		"success: prompted": {
			req:  &llm.Request{Messages: requestMessages, Schema: []byte(requestSchema)},
			want: `{"model":"test-model","input":[{"role":"system","content":"system prompt"},{"role":"user","content":"the document"}],"text":{"format":{"type":"json_object"}},"store":false}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{body: responsesAnswer}
			p := newProvider(t, rec, WithAPI(Responses))
			tt.req.Trace = new(llm.Trace)
			if _, err := p.Do(t.Context(), tt.req); err != nil {
				t.Fatalf("Do: %v", err)
			}
			reqs := rec.requests()
			if len(reqs) != 1 {
				t.Fatalf("the transport received %d requests, want 1", len(reqs))
			}
			if diff := gocmp.Diff(tt.want, string(reqs[0].body)); diff != "" {
				t.Errorf("body sent mismatch (-want +got):\n%s", diff)
			}
			if recorded, ok := tt.req.Trace.Request(); !ok || string(recorded) != tt.want {
				t.Errorf("the trace's request = %s (recorded %t), want the body sent", recorded, ok)
			}
			if api := tt.req.Trace.API(); api != "responses" {
				t.Errorf("the trace's api = %q, want responses", api)
			}
		})
	}
}
