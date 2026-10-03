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
	"errors"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// requestSchema and requestMessages are the schema and the messages of
// tests/test_provider_requests.py:39-43.
const requestSchema = `{"type":"object","properties":{"answers":{"type":"object"}}}`

var requestMessages = []llm.Message{{Role: "system", Content: "system prompt"}, {Role: "user", Content: "the document"}}

// chatPrefix is the start of every Chat Completions body built from
// requestMessages for test-model.
const chatPrefix = `{"model":"test-model","messages":[{"role":"system","content":"system prompt"},{"role":"user","content":"the document"}]`

// wrappedFormat is the response_format of a structured Chat Completions
// request for requestSchema.
const wrappedFormat = `{"type":"json_schema","json_schema":{"name":"evaluation","schema":` + requestSchema + `,"strict":true}}`

// TestChatResponseFormatWrapsSchema ports
// tests/test_provider_requests.py::test_openai_native_response_format_wraps_schema:
// in structured mode, response_format wraps the schema as a strict
// json_schema named evaluation; the body is model, messages and
// response_format, in that order.
func TestChatResponseFormatWrapsSchema(t *testing.T) {
	got, err := chatBody("test-model", &llm.Request{Messages: requestMessages, Schema: []byte(requestSchema), Structured: true}, FormatNull)
	if err != nil {
		t.Fatalf("chatBody: %v", err)
	}
	want := chatPrefix + `,"response_format":` + wrappedFormat + `}`
	if diff := gocmp.Diff(want, string(got)); diff != "" {
		t.Errorf("body mismatch (-want +got):\n%s", diff)
	}
}

// TestChatPromptedSendsNullResponseFormat ports
// tests/test_provider_requests.py::test_openai_prompted_sends_no_response_format:
// in prompted mode the body carries "response_format": null, as upstream
// sends it.
func TestChatPromptedSendsNullResponseFormat(t *testing.T) {
	got, err := chatBody("test-model", &llm.Request{Messages: requestMessages, Schema: []byte(requestSchema)}, FormatNull)
	if err != nil {
		t.Fatalf("chatBody: %v", err)
	}
	want := chatPrefix + `,"response_format":null}`
	if diff := gocmp.Diff(want, string(got)); diff != "" {
		t.Errorf("body mismatch (-want +got):\n%s", diff)
	}
}

// TestChatPromptedFormatOmit pins WithPromptedResponseFormat through Do:
// FormatOmit leaves response_format out in prompted mode only; structured
// mode keeps the wrapped schema whatever the format.
func TestChatPromptedFormatOmit(t *testing.T) {
	tests := map[string]struct {
		format     PromptedFormat
		structured bool
		want       string
	}{
		"success: FormatNull, prompted": {
			format: FormatNull,
			want:   chatPrefix + `,"response_format":null}`,
		},
		"success: FormatOmit, prompted": {
			format: FormatOmit,
			want:   chatPrefix + `}`,
		},
		"success: FormatNull, structured": {
			format:     FormatNull,
			structured: true,
			want:       chatPrefix + `,"response_format":` + wrappedFormat + `}`,
		},
		"success: FormatOmit, structured": {
			format:     FormatOmit,
			structured: true,
			want:       chatPrefix + `,"response_format":` + wrappedFormat + `}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{body: chatAnswer}
			p := newProvider(t, rec, WithAPI(ChatCompletions), WithPromptedResponseFormat(tt.format))
			req := &llm.Request{Messages: requestMessages, Schema: []byte(requestSchema), Structured: tt.structured, Trace: new(llm.Trace)}
			if _, err := p.Do(t.Context(), req); err != nil {
				t.Fatalf("Do: %v", err)
			}
			reqs := rec.requests()
			if len(reqs) != 1 {
				t.Fatalf("the transport received %d requests, want 1", len(reqs))
			}
			if diff := gocmp.Diff(tt.want, string(reqs[0].body)); diff != "" {
				t.Errorf("body sent mismatch (-want +got):\n%s", diff)
			}
			recorded, ok := req.Trace.Request()
			if !ok || string(recorded) != tt.want {
				t.Errorf("the trace's request = %s (recorded %t), want the body sent", recorded, ok)
			}
			if api := req.Trace.API(); api != "chat_completions" {
				t.Errorf("the trace's api = %q, want chat_completions", api)
			}
		})
	}
}

// TestChatResultReadsContentAndUsage ports
// tests/test_provider_requests.py::test_openai_result_reads_content_and_usage:
// the text is choices[0].message.content and the counts are
// usage.prompt_tokens and usage.completion_tokens.
func TestChatResultReadsContentAndUsage(t *testing.T) {
	const body = `{"choices":[{"message":{"content":"{\"answers\": {}}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`
	got, err := chatResult([]byte(body), nil)
	if err != nil {
		t.Fatalf("chatResult: %v", err)
	}
	want := &llm.Result{Text: `{"answers": {}}`, InputTokens: llm.Count{N: 12, Known: true}, OutputTokens: llm.Count{N: 3, Known: true}}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}
}

// known returns a known count of n.
func known(n uint64) llm.Count { return llm.Count{N: n, Known: true} }

// TestChatResult pins the Chat Completions result parser
// (providers/openai.py:44-53): the body is recorded with
// choices[0].finish_reason before any check; a finish reason other than
// "stop" or null is a non-answer with upstream's whole text, an integer
// written as its digits and any other kind a plain error; content null
// or absent is the empty text; a count null or absent is unknown and not
// total_tokens; and a body that is not a Chat Completions response is a
// plain error of a fixed text.
func TestChatResult(t *testing.T) {
	tests := map[string]struct {
		body       string
		want       *llm.Result
		wantErr    string
		nonAnswer  bool
		wantReason *string
	}{
		"success: stop": {
			body:       `{"choices":[{"finish_reason":"stop","message":{"content":"text"}}],"usage":{"prompt_tokens":5,"completion_tokens":6,"total_tokens":11}}`,
			want:       &llm.Result{Text: "text", InputTokens: known(5), OutputTokens: known(6)},
			wantReason: new("stop"),
		},
		"success: finish reason null": {
			body: `{"choices":[{"finish_reason":null,"message":{"content":"text"}}]}`,
			want: &llm.Result{Text: "text"},
		},
		"success: finish reason absent": {
			body: `{"choices":[{"message":{"content":"text"}}]}`,
			want: &llm.Result{Text: "text"},
		},
		"success: content null is the empty text": {
			body:       `{"choices":[{"finish_reason":"stop","message":{"content":null,"refusal":"I cannot help."}}]}`,
			want:       &llm.Result{},
			wantReason: new("stop"),
		},
		"success: content absent is the empty text": {
			body:       `{"choices":[{"finish_reason":"stop","message":{"role":"assistant"}}]}`,
			want:       &llm.Result{},
			wantReason: new("stop"),
		},
		"success: only choices[0] is read": {
			body:       `{"choices":[{"finish_reason":"stop","message":{"content":"first"}},{"finish_reason":"length","message":{"content":"second"}}]}`,
			want:       &llm.Result{Text: "first"},
			wantReason: new("stop"),
		},
		"success: counts zero, -0 and the largest": {
			body:       `{"choices":[{"finish_reason":"stop","message":{"content":""}}],"usage":{"prompt_tokens":-0,"completion_tokens":18446744073709551615}}`,
			want:       &llm.Result{InputTokens: known(0), OutputTokens: known(18446744073709551615)},
			wantReason: new("stop"),
		},
		"success: usage null": {
			body:       `{"choices":[{"finish_reason":"stop","message":{"content":"t"}}],"usage":null}`,
			want:       &llm.Result{Text: "t"},
			wantReason: new("stop"),
		},
		"success: counts null or absent, total_tokens not read": {
			body:       `{"choices":[{"finish_reason":"stop","message":{"content":"t"}}],"usage":{"prompt_tokens":null,"total_tokens":19}}`,
			want:       &llm.Result{Text: "t"},
			wantReason: new("stop"),
		},
		"error: finish reason length": {
			body:       `{"choices":[{"finish_reason":"length","message":{"content":"text"}}]}`,
			wantErr:    "OpenAI chat completion did not complete: length.",
			nonAnswer:  true,
			wantReason: new("length"),
		},
		"error: finish reason content_filter, before a missing message": {
			body:       `{"choices":[{"finish_reason":"content_filter"}]}`,
			wantErr:    "OpenAI chat completion did not complete: content_filter.",
			nonAnswer:  true,
			wantReason: new("content_filter"),
		},
		"error: finish reason empty": {
			body:       `{"choices":[{"finish_reason":"","message":{"content":"text"}}]}`,
			wantErr:    "OpenAI chat completion did not complete: .",
			nonAnswer:  true,
			wantReason: new(""),
		},
		"error: not JSON": {
			body:    `not JSON`,
			wantErr: errNotChat.Error(),
		},
		"error: empty": {
			body:    ``,
			wantErr: errNotChat.Error(),
		},
		"error: an array": {
			body:    `[{"choices":[]}]`,
			wantErr: errNotChat.Error(),
		},
		"error: no choices": {
			body:    `{"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
			wantErr: errNotChat.Error(),
		},
		"error: choices empty": {
			body:    `{"choices":[]}`,
			wantErr: errNotChat.Error(),
		},
		"error: choices an object": {
			body:    `{"choices":{"finish_reason":"stop"}}`,
			wantErr: errNotChat.Error(),
		},
		"error: choice not an object": {
			body:    `{"choices":["stop"]}`,
			wantErr: errNotChat.Error(),
		},
		"error: finish reason an integer is its digits": {
			body:       `{"choices":[{"finish_reason":1,"message":{"content":"t"}}]}`,
			wantErr:    "OpenAI chat completion did not complete: 1.",
			nonAnswer:  true,
			wantReason: new("1"),
		},
		"error: finish reason -0 is 0": {
			body:       `{"choices":[{"finish_reason":-0,"message":{"content":"t"}}]}`,
			wantErr:    "OpenAI chat completion did not complete: 0.",
			nonAnswer:  true,
			wantReason: new("0"),
		},
		"error: finish reason a fraction": {
			body:    `{"choices":[{"finish_reason":1.5,"message":{"content":"t"}}]}`,
			wantErr: errNotChat.Error(),
		},
		"error: finish reason an exponent": {
			body:    `{"choices":[{"finish_reason":1e2,"message":{"content":"t"}}]}`,
			wantErr: errNotChat.Error(),
		},
		"error: finish reason true": {
			body:    `{"choices":[{"finish_reason":true,"message":{"content":"t"}}]}`,
			wantErr: errNotChat.Error(),
		},
		"error: finish reason an object": {
			body:    `{"choices":[{"finish_reason":{"type":"stop"},"message":{"content":"t"}}]}`,
			wantErr: errNotChat.Error(),
		},
		"error: message absent": {
			body:       `{"choices":[{"finish_reason":"stop"}]}`,
			wantErr:    errNotChat.Error(),
			wantReason: new("stop"),
		},
		"error: message a string": {
			body:       `{"choices":[{"finish_reason":"stop","message":"text"}]}`,
			wantErr:    errNotChat.Error(),
			wantReason: new("stop"),
		},
		"error: content an array": {
			body:       `{"choices":[{"finish_reason":"stop","message":{"content":[{"type":"text","text":"t"}]}}]}`,
			wantErr:    errNotChat.Error(),
			wantReason: new("stop"),
		},
		"error: usage a number": {
			body:       `{"choices":[{"finish_reason":"stop","message":{"content":"t"}}],"usage":5}`,
			wantErr:    errNotChat.Error(),
			wantReason: new("stop"),
		},
		"error: a count with a fraction": {
			body:       `{"choices":[{"finish_reason":"stop","message":{"content":"t"}}],"usage":{"prompt_tokens":1.0,"completion_tokens":1}}`,
			wantErr:    errNotChat.Error(),
			wantReason: new("stop"),
		},
		"error: a count that is negative": {
			body:       `{"choices":[{"finish_reason":"stop","message":{"content":"t"}}],"usage":{"prompt_tokens":1,"completion_tokens":-1}}`,
			wantErr:    errNotChat.Error(),
			wantReason: new("stop"),
		},
		"error: a count past 2^64-1": {
			body:       `{"choices":[{"finish_reason":"stop","message":{"content":"t"}}],"usage":{"prompt_tokens":18446744073709551616}}`,
			wantErr:    errNotChat.Error(),
			wantReason: new("stop"),
		},
		"error: a count that is a string": {
			body:       `{"choices":[{"finish_reason":"stop","message":{"content":"t"}}],"usage":{"completion_tokens":"7"}}`,
			wantErr:    errNotChat.Error(),
			wantReason: new("stop"),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			trace := new(llm.Trace)
			got, err := chatResult([]byte(tt.body), trace)
			checkResult(t, got, err, tt.want, tt.wantErr, tt.nonAnswer)
			checkRecorded(t, trace, tt.body, tt.wantReason)
		})
	}
}

// checkResult compares a parser's result and error with the wanted ones: a
// wanted error text is compared whole, and nonAnswer says whether the error
// is an *llm.NonAnswerError.
func checkResult(t *testing.T, got *llm.Result, err error, want *llm.Result, wantErr string, nonAnswer bool) {
	t.Helper()
	if wantErr != "" {
		if err == nil {
			t.Fatalf("result = %+v, nil; want the error %q", got, wantErr)
		}
		if err.Error() != wantErr {
			t.Errorf("error = %q, want %q", err, wantErr)
		}
		var na *llm.NonAnswerError
		if errors.As(err, &na) != nonAnswer {
			t.Errorf("error %v (%T) is a non-answer: %t, want %t", err, err, !nonAnswer, nonAnswer)
		}
		if got != nil {
			t.Errorf("result = %+v with an error, want nil", got)
		}
		return
	}
	if err != nil {
		t.Fatalf("error = %v, want none", err)
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}
}

// checkRecorded checks that trace recorded body with the finish reason
// want, whatever the parser decided afterwards.
func checkRecorded(t *testing.T, trace *llm.Trace, body string, want *string) {
	t.Helper()
	if !trace.Responded() {
		t.Fatal("the trace recorded no response")
	}
	if got, _ := trace.Response(); string(got) != body {
		t.Errorf("the trace's response = %q, want the body %q", got, body)
	}
	if diff := gocmp.Diff(want, trace.FinishReason()); diff != "" {
		t.Errorf("the trace's finish reason mismatch (-want +got):\n%s", diff)
	}
}
