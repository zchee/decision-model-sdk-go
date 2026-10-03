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

package openai_test

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
	"github.com/zchee/decision-model-sdk-go/adapter/openai"
)

// fixedServer is a transport that answers every request with the next of
// its bodies (the last one again once they run out) and records the
// request bodies.
type fixedServer struct {
	ex      exchange
	answers [][]byte
}

func (s *fixedServer) RoundTrip(r *http.Request) (*http.Response, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	n := s.ex.add(body)
	return jsonResponse(r, jsonx.Raw(s.answers[min(n, len(s.answers))-1]))
}

// marshal writes v, failing the test on an error.
func marshal(t *testing.T, v jsonx.Value) []byte {
	t.Helper()
	b, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatalf("jsonx.Marshal: %v", err)
	}
	return b
}

// nonAnswerRetry is upstream's RetryPolicy(max_retries=2,
// backoff_initial=0) of the non-answer tests, with the Python SDK's
// defaults for the rest: a maximum backoff of 5 s and a jitter of 0.25.
var nonAnswerRetry = adapter.DefaultRetry().MaxRetries(2).Backoff(0, 5*time.Second, 0.25)

// evaluate runs upstream's _evaluate of tests/test_provider_nonanswers.py
// through the Adapter and the SDK: one call in discrete mode with two
// corrective retries and nonAnswerRetry, its Provider a real
// *openai.Provider for api over srv. It returns the response, or the error
// and the Report it carries.
func evaluate(t *testing.T, srv *fixedServer, api openai.API, structured bool) (*decision.SystemOneResponse, *adapter.Report, error) {
	t.Helper()
	clearEnv(t)
	mode := adapter.Prompted
	if structured {
		mode = adapter.Structured
	}
	ad, err := adapter.New(adapter.Discrete, mode, adapter.WithMalformedRetries(2), adapter.WithRetry(nonAnswerRetry), adapter.WithFactory("openai", factory(srv, openai.WithAPI(api))))
	if err != nil {
		t.Fatalf("adapter.New: %v", err)
	}
	resp, err := newClient(t, ad).SystemOne(t.Context(), "A delightful book.", questions(t), decision.Model(modelID))
	if err != nil {
		rep, ok := adapter.ReportFromError(err)
		if !ok {
			t.Fatalf("SystemOne: %v, with no Report", err)
		}
		return nil, rep, err
	}
	rep, err := adapter.ReportOf(resp)
	if err != nil {
		t.Fatalf("ReportOf: %v", err)
	}
	return resp, rep, nil
}

// checkNonAnswer checks that err is the Adapter's answer to a non-answer:
// status 424, error_type non_answer and the whole message want.
func checkNonAnswer(t *testing.T, err error, want string) {
	t.Helper()
	ae, ok := errors.AsType[*decision.APIError](err)
	if !ok {
		t.Fatalf("error %v (%T) holds no *decision.APIError", err, err)
	}
	if ae.StatusCode != http.StatusFailedDependency || ae.ErrorType != "non_answer" || ae.Message != want {
		t.Errorf("error = %d %q %q, want 424 non_answer %q", ae.StatusCode, ae.ErrorType, ae.Message, want)
	}
}

// positive returns the answer to the question positive, failing the test
// when the response has none.
func positive(t *testing.T, resp *decision.SystemOneResponse) float64 {
	t.Helper()
	a, ok := resp.Answers().Noul("positive")
	if !ok {
		t.Fatal("the response has no answer positive")
	}
	return a.Noul()
}

// TestChatFinishReason ports
// tests/test_provider_nonanswers.py::test_chat_completion_finish_reason: a
// Chat Completions finish reason of stop or null is an answer; any other
// is a non-answer with upstream's text that spends no retry, and its
// attempt keeps the request, the raw response and the finish reason.
func TestChatFinishReason(t *testing.T) {
	// row PN1 of docs/port-test-matrix.md: seven finish reasons in both output modes.
	reasons := []*string{new("stop"), nil, new("length"), new("content_filter"), new("tool_calls"), new("function_call"), new("unknown")}
	for _, reason := range reasons {
		for _, structured := range []bool{false, true} {
			label := "null"
			if reason != nil {
				label = *reason
			}
			answers := reason == nil || *reason == "stop"
			kind := "error"
			if answers {
				kind = "success"
			}
			t.Run(kind+": "+label+" "+modeName(structured), func(t *testing.T) {
				var fr jsonx.Value
				if reason != nil {
					fr = jsonx.String(*reason)
				}
				payload := marshal(t, jsonx.Object(
					member("choices", jsonx.Array(jsonx.Object(
						member("finish_reason", fr),
						member("message", jsonx.Object(member("role", jsonx.String("assistant")), member("content", jsonx.String(validAnswer)))),
					))),
					member("usage", usageValue("prompt_tokens", "completion_tokens")),
				))
				srv := &fixedServer{answers: [][]byte{payload}}
				resp, rep, err := evaluate(t, srv, openai.ChatCompletions, structured)
				if answers {
					if err != nil {
						t.Fatalf("SystemOne: %v", err)
					}
					if got := positive(t, resp); got != 1.0 {
						t.Errorf("the answer positive = %v, want 1.0", got)
					}
				} else {
					checkNonAnswer(t, err, "OpenAI chat completion did not complete: "+label+".")
					if len(rep.Debug.RetryReasons) != 0 {
						t.Errorf("retry_reasons = %+v, want none", rep.Debug.RetryReasons)
					}
				}
				bodies := srv.ex.all()
				if len(bodies) != 1 || len(rep.Debug.Attempts) != 1 {
					t.Fatalf("%d requests and %d attempts, want 1 and 1", len(bodies), len(rep.Debug.Attempts))
				}
				a := rep.Debug.Attempts[0]
				if !bytes.Equal(a.Request, bodies[0]) {
					t.Errorf("the attempt's request = %s, want the body sent %s", a.Request, bodies[0])
				}
				raw := read(t, a.Response)
				if got := at(t, raw, "choices", "0", "message", "content").Text(); got != validAnswer {
					t.Errorf("llm_response's content = %q, want %q", got, validAnswer)
				}
				rawReason := at(t, raw, "choices", "0", "finish_reason")
				if reason == nil && rawReason.Kind() != jsonx.KindNull || reason != nil && rawReason.Text() != *reason {
					t.Errorf("llm_response's finish_reason = %s, want %s", encode(t, rawReason), label)
				}
				if diff := gocmp.Diff(reason, a.Info.FinishReason); diff != "" {
					t.Errorf("debug_info.finish_reason mismatch (-want +got):\n%s", diff)
				}
				if (a.Info.Error != "") == answers {
					t.Errorf("debug_info.error = %q, want one exactly for a non-answer", a.Info.Error)
				}
				if _, err := rep.MarshalJSON(); err != nil {
					t.Errorf("Report.MarshalJSON: %v", err)
				}
			})
		}
	}
}

// modeName names an output mode in a case name.
func modeName(structured bool) string {
	if structured {
		return "structured"
	}
	return "prompted"
}

// TestMissingUsage ports
// tests/test_provider_nonanswers.py::test_openai_missing_usage: a usage
// member that is omitted or null, or a count that is missing or null, is
// an unknown count of the call and of its totals, through both APIs in
// both output modes, with no retry; a count of 0 is a known 0.
func TestMissingUsage(t *testing.T) {
	// row PN2 of docs/port-test-matrix.md: eight usage shapes, both APIs, both output modes.
	usageCases := []string{"omitted", "null", "missing_input", "null_input", "missing_output", "null_output", "zero", "present"}
	apis := map[string]openai.API{"chat_completions": openai.ChatCompletions, "responses": openai.Responses}
	for _, usageCase := range usageCases {
		for apiName, api := range apis {
			for _, structured := range []bool{false, true} {
				t.Run("success: "+usageCase+" "+apiName+" "+modeName(structured), func(t *testing.T) {
					inField, outField := "prompt_tokens", "completion_tokens"
					members := []jsonx.Member{member("choices", jsonx.Array(jsonx.Object(
						member("finish_reason", jsonx.String("stop")),
						member("message", jsonx.Object(member("role", jsonx.String("assistant")), member("content", jsonx.String(validAnswer)))),
					)))}
					if api == openai.Responses {
						inField, outField = "input_tokens", "output_tokens"
						members = []jsonx.Member{member("status", jsonx.String("completed")), member("output", jsonx.Array(messageOutput(validAnswer)))}
					}
					counts := map[string]string{inField: "12", outField: "7", "total_tokens": "19"}
					if usageCase == "zero" {
						counts = map[string]string{inField: "0", outField: "0", "total_tokens": "0"}
					}
					var usage []jsonx.Member
					for _, field := range []string{inField, outField, "total_tokens"} {
						switch usageCase {
						case "missing_input", "missing_output":
							if field == map[string]string{"missing_input": inField, "missing_output": outField}[usageCase] {
								continue
							}
						case "null_input", "null_output":
							if field == map[string]string{"null_input": inField, "null_output": outField}[usageCase] {
								usage = append(usage, member(field, jsonx.Value{}))
								continue
							}
						}
						usage = append(usage, member(field, jsonx.Number(counts[field])))
					}
					switch usageCase {
					case "omitted":
					case "null":
						members = append(members, member("usage", jsonx.Value{}))
					default:
						members = append(members, member("usage", jsonx.Object(usage...)))
					}
					srv := &fixedServer{answers: [][]byte{marshal(t, jsonx.Object(members...))}}
					resp, rep, err := evaluate(t, srv, api, structured)
					if err != nil {
						t.Fatalf("SystemOne: %v", err)
					}
					if got := positive(t, resp); got != 1.0 {
						t.Errorf("the answer positive = %v, want 1.0", got)
					}
					wantIn, wantOut := llm.Count{N: 12, Known: true}, llm.Count{N: 7, Known: true}
					if usageCase == "zero" {
						wantIn, wantOut = llm.Count{Known: true}, llm.Count{Known: true}
					}
					switch usageCase {
					case "omitted", "null":
						wantIn, wantOut = llm.Count{}, llm.Count{}
					case "missing_input", "null_input":
						wantIn = llm.Count{}
					case "missing_output", "null_output":
						wantOut = llm.Count{}
					}
					u := rep.Usage
					if diff := gocmp.Diff([4]llm.Count{wantIn, wantIn, wantOut, wantOut}, [4]llm.Count{u.InputTokens, u.InputTokensTotal, u.OutputTokens, u.OutputTokensTotal}); diff != "" {
						t.Errorf("usage mismatch (-want +got):\n%s", diff)
					}
					if u.Retries != 0 || u.MalformedRetries != 0 {
						t.Errorf("n_retries = %d, n_retries_malformed_structure = %d; want 0 and 0", u.Retries, u.MalformedRetries)
					}
					sdkIn, inKnown := resp.Usage().InputTokens()
					sdkOut, outKnown := resp.Usage().OutputTokens()
					if diff := gocmp.Diff([2]llm.Count{wantIn, wantOut}, [2]llm.Count{{N: sdkIn, Known: inKnown}, {N: sdkOut, Known: outKnown}}); diff != "" {
						t.Errorf("the SDK response's usage mismatch (-want +got):\n%s", diff)
					}
					if len(rep.Debug.RetryReasons) != 0 {
						t.Errorf("retry_reasons = %+v, want none", rep.Debug.RetryReasons)
					}
					bodies := srv.ex.all()
					if len(bodies) != 1 || len(rep.Debug.Attempts) != 1 {
						t.Fatalf("%d requests and %d attempts, want 1 and 1", len(bodies), len(rep.Debug.Attempts))
					}
					a := rep.Debug.Attempts[0]
					if !bytes.Equal(a.Request, bodies[0]) {
						t.Errorf("the attempt's request = %s, want the body sent %s", a.Request, bodies[0])
					}
					if a.Response == nil {
						t.Fatal("llm_response is nil")
					}
					raw := read(t, a.Response)
					switch usageCase {
					case "omitted":
						// llm_response is the body as received, not the vendor
						// SDK's dump (DV7): an omitted usage is absent, where
						// upstream's dump writes None.
						if has(raw, "usage") {
							t.Errorf("llm_response has a usage member, want none")
						}
					case "null":
						if at(t, raw, "usage").Kind() != jsonx.KindNull {
							t.Errorf("llm_response's usage is not null")
						}
					default:
						rawUsage := at(t, raw, "usage")
						for _, m := range usage {
							want, _ := jsonx.Marshal(m.Value)
							if got := encode(t, at(t, rawUsage, m.Name)); !bytes.Equal(got, want) {
								t.Errorf("llm_response's usage.%s = %s, want %s", m.Name, got, want)
							}
						}
					}
					wantReason := "stop"
					if api == openai.Responses {
						wantReason = "completed"
					}
					if a.Info.FinishReason == nil || *a.Info.FinishReason != wantReason {
						t.Errorf("debug_info.finish_reason = %v, want %q", a.Info.FinishReason, wantReason)
					}
					if a.Info.Error != "" {
						t.Errorf("debug_info.error = %q, want none", a.Info.Error)
					}
					if _, err := rep.MarshalJSON(); err != nil {
						t.Errorf("Report.MarshalJSON: %v", err)
					}
				})
			}
		}
	}
}

// TestResponsesRefusal ports
// tests/test_provider_nonanswers.py::test_openai_responses_refusal: a
// refusal part in a Responses message is a non-answer with its text, also
// next to a valid answer and without usage, that spends no retry and keeps
// the raw response.
func TestResponsesRefusal(t *testing.T) {
	// row PN4 of docs/port-test-matrix.md: with and without a valid answer, both output modes.
	const refusal = "Cannot evaluate this request."
	for _, withValidText := range []bool{false, true} {
		for _, structured := range []bool{false, true} {
			name := "error: refusal alone " + modeName(structured)
			if withValidText {
				name = "error: refusal after an answer " + modeName(structured)
			}
			t.Run(name, func(t *testing.T) {
				output := []jsonx.Value{jsonx.Object(member("type", jsonx.String("reasoning")), member("id", jsonx.String("reasoning-test")), member("summary", jsonx.Array()))}
				if withValidText {
					output = append(output, messageOutput(validAnswer))
				}
				output = append(output, jsonx.Object(
					member("type", jsonx.String("message")),
					member("role", jsonx.String("assistant")),
					member("content", jsonx.Array(jsonx.Object(member("type", jsonx.String("refusal")), member("refusal", jsonx.String(refusal))))),
				))
				srv := &fixedServer{answers: [][]byte{marshal(t, jsonx.Object(member("status", jsonx.String("completed")), member("output", jsonx.Array(output...))))}}
				_, rep, err := evaluate(t, srv, openai.Responses, structured)
				checkNonAnswer(t, err, "OpenAI response was a refusal: "+refusal)
				if len(rep.Debug.RetryReasons) != 0 {
					t.Errorf("retry_reasons = %+v, want none", rep.Debug.RetryReasons)
				}
				bodies := srv.ex.all()
				if len(bodies) != 1 || len(rep.Debug.Attempts) != 1 {
					t.Fatalf("%d requests and %d attempts, want 1 and 1", len(bodies), len(rep.Debug.Attempts))
				}
				a := rep.Debug.Attempts[0]
				if !bytes.Equal(a.Request, bodies[0]) {
					t.Errorf("the attempt's request = %s, want the body sent %s", a.Request, bodies[0])
				}
				if got := at(t, read(t, a.Response), "output", "-1", "content", "0", "refusal").Text(); got != refusal {
					t.Errorf("llm_response's last refusal = %q, want %q", got, refusal)
				}
				if a.Info.FinishReason == nil || *a.Info.FinishReason != "completed" {
					t.Errorf("debug_info.finish_reason = %v, want completed", a.Info.FinishReason)
				}
				if !strings.Contains(a.Info.Error, "refusal") {
					t.Errorf("debug_info.error = %q, want the refusal", a.Info.Error)
				}
				if _, err := rep.MarshalJSON(); err != nil {
					t.Errorf("Report.MarshalJSON: %v", err)
				}
			})
		}
	}
}

// TestChatRefusalIsMalformedNotRefusal pins upstream's Chat Completions
// rule (providers/openai.py:50), which does not read message.refusal: a
// refusal with content null is the empty text, which fails validation and
// spends a corrective retry instead of ending the call as a non-answer.
func TestChatRefusalIsMalformedNotRefusal(t *testing.T) {
	refused := []byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":null,"refusal":"I cannot help with that."}}]}`)
	answered := []byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{\"answers\":{\"positive\":true}}"}}]}`)
	for _, structured := range []bool{false, true} {
		t.Run("success: "+modeName(structured), func(t *testing.T) {
			srv := &fixedServer{answers: [][]byte{refused, answered}}
			resp, rep, err := evaluate(t, srv, openai.ChatCompletions, structured)
			if err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			if got := positive(t, resp); got != 1.0 {
				t.Errorf("the answer positive = %v, want 1.0", got)
			}
			if rep.Usage.MalformedRetries != 1 || rep.Usage.Retries != 0 {
				t.Errorf("n_retries_malformed_structure = %d, n_retries = %d; want 1 and 0", rep.Usage.MalformedRetries, rep.Usage.Retries)
			}
			if len(rep.Debug.RetryReasons) != 1 || rep.Debug.RetryReasons[0].Category != "malformed_structure" {
				t.Errorf("retry_reasons = %+v, want one malformed_structure", rep.Debug.RetryReasons)
			}
			if len(rep.Debug.Attempts) != 2 || len(srv.ex.all()) != 2 {
				t.Fatalf("%d attempts and %d requests, want 2 and 2", len(rep.Debug.Attempts), len(srv.ex.all()))
			}
			first := rep.Debug.Attempts[0]
			if first.Info.Error != "" || first.Info.FinishReason == nil || *first.Info.FinishReason != "stop" {
				t.Errorf("the refused attempt's debug_info = %+v, want finish_reason stop and no error", first.Info)
			}
			corrective := rep.Debug.Attempts[1].Messages
			if got := corrective[len(corrective)-2]; got != (llm.Message{Role: "assistant", Content: ""}) {
				t.Errorf("the corrective request repeats %+v, want the empty answer", got)
			}
		})
	}
}
