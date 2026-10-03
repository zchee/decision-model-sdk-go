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

package anthropic_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"
	"github.com/zchee/decision-model-sdk-go/adapter"
	"github.com/zchee/decision-model-sdk-go/adapter/anthropic"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// The non-answer texts of upstream's _result, byte for byte
// (providers/anthropic.py:57-63).
const truncatedText = "Anthropic response was truncated at the output token limit. Increase max_tokens on AnthropicProvider or AsyncAnthropicProvider, or request fewer questions."

// answerText is the model's answer in every reply that answers: the one
// question, positive, answered true.
const answerText = `{"answers":{"positive":true}}`

// transport is an http.RoundTripper that records each request body and
// answers the n-th request with replies[n], the last reply again once they
// are spent. No request leaves the process.
type transport struct {
	replies []string

	mu     sync.Mutex
	bodies [][]byte
}

func (tr *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	tr.mu.Lock()
	n := len(tr.bodies)
	tr.bodies = append(tr.bodies, body)
	tr.mu.Unlock()
	reply := tr.replies[min(n, len(tr.replies)-1)]
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(strings.NewReader(reply)),
		ContentLength: int64(len(reply)),
		Request:       req,
	}, nil
}

// requests returns a copy of the request bodies the transport received.
func (tr *transport) requests() [][]byte {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([][]byte(nil), tr.bodies...)
}

// call evaluates one noul question through the Adapter and the SDK as
// upstream's tests evaluate through their client
// (tests/test_provider_nonanswers.py:45-68): discrete answers, two corrective
// retries unless malformed says otherwise, a retry policy of two retries
// without backoff, and the real Anthropic provider over tr, built by a
// factory for model test-model.
func call(t *testing.T, tr http.RoundTripper, model string, structured bool, malformed int, opts ...anthropic.Option) (*decision.SystemOneResponse, error) {
	t.Helper()
	output := adapter.Prompted
	if structured {
		output = adapter.Structured
	}
	factory := func(model string) (llm.Provider, error) {
		p, err := anthropic.New(model, append([]anthropic.Option{anthropic.WithHTTPClient(&http.Client{Transport: tr}), anthropic.WithAPIKey("not-a-key")}, opts...)...)
		if err != nil {
			return nil, err
		}
		return p, nil
	}
	ad, err := adapter.New(adapter.Discrete, output,
		adapter.WithMalformedRetries(malformed),
		adapter.WithRetry(adapter.RetryPolicy{}.MaxRetries(2).Backoff(0, 5*time.Second, 0.25)),
		adapter.WithFactory("anthropic", factory),
	)
	if err != nil {
		t.Fatalf("adapter.New: %v", err)
	}
	c, err := adapter.NewClient(ad)
	if err != nil {
		t.Fatalf("adapter.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	q, err := decision.NewQuestions().Noul("positive", decision.Noul{Instructions: decision.Text("The review is positive.")}).Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return c.SystemOne(t.Context(), "A delightful book.", q, decision.Model(adapter.ModelID("anthropic", model)))
}

// messageBody is the Messages response of upstream's tests: one text block
// holding answerText, unless content says otherwise, and the stop reason
// given (nil writes null).
func messageBody(stopReason *string, content string, input, output int) string {
	sr := "null"
	if stopReason != nil {
		sr = strconv.Quote(*stopReason)
	}
	return `{"id":"message-test","type":"message","role":"assistant","model":"test-model","stop_reason":` + sr +
		`,"content":` + content + `,"usage":{"input_tokens":` + strconv.Itoa(input) + `,"output_tokens":` + strconv.Itoa(output) + `}}`
}

// textContent is a content array of one text block holding answerText.
var textContent = `[{"type":"text","text":` + strconv.Quote(answerText) + `}]`

// reportOf returns the Report of a call: from the response when it
// answered, else from its error, which must carry one.
func reportOf(t *testing.T, resp *decision.SystemOneResponse, err error) *adapter.Report {
	t.Helper()
	if err == nil {
		r, rerr := adapter.ReportOf(resp)
		if rerr != nil {
			t.Fatalf("ReportOf: %v", rerr)
		}
		return r
	}
	r, ok := adapter.ReportFromError(err)
	if !ok {
		t.Fatalf("ReportFromError(%v) found no Report", err)
	}
	return r
}

// member reads the member name of the JSON object body.
func member(t *testing.T, body []byte, name string) jsonx.Node {
	t.Helper()
	doc, err := jsonx.Read(body)
	if err != nil {
		t.Fatalf("jsonx.Read(%q): %v", body, err)
	}
	v, ok := doc.Member(name)
	if !ok {
		t.Fatalf("body %q has no member %s", body, name)
	}
	return v
}

// wantNonAnswer checks that err is the SDK's error for the Adapter's 424
// non-answer with message, the whole text.
func wantNonAnswer(t *testing.T, err error, message string) {
	t.Helper()
	apiErr, ok := errors.AsType[*decision.APIError](err)
	if !ok {
		t.Fatalf("error = %T %v, want *decision.APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusFailedDependency || apiErr.ErrorType != "non_answer" {
		t.Errorf("error = %d %s, want 424 non_answer", apiErr.StatusCode, apiErr.ErrorType)
	}
	if diff := gocmp.Diff(message, apiErr.Message); diff != "" {
		t.Errorf("message (-want +got):\n%s", diff)
	}
}

// TestNonAnswers ports tests/test_provider_nonanswers.py::test_anthropic_nonanswers,
// one Go case for upstream's sync and async pair: a stop reason of
// end_turn, stop_sequence or null answers, max_tokens is the truncation
// non-answer, and any other is the non-answer naming it. Either way the
// call made one request and one attempt, which kept the request as sent,
// the response body as received (its content blocks and its stop_reason)
// and the finish reason, and a non-answer spent no retry.
func TestNonAnswers(t *testing.T) {
	str := func(s string) *string { return &s }
	reasons := map[string]*string{
		"end_turn":                      str("end_turn"),
		"stop_sequence":                 str("stop_sequence"),
		"null":                          nil,
		"refusal":                       str("refusal"),
		"model_context_window_exceeded": str("model_context_window_exceeded"),
		"pause_turn":                    str("pause_turn"),
		"tool_use":                      str("tool_use"),
		"unknown":                       str("unknown"),
		"max_tokens":                    str("max_tokens"),
	}
	type testCase struct {
		structured bool
		stopReason *string
	}
	tests := map[string]testCase{}
	for name, sr := range reasons {
		tests["prompted: "+name] = testCase{structured: false, stopReason: sr}
		tests["structured: "+name] = testCase{structured: true, stopReason: sr}
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			content, blocks := textContent, 1
			if tt.stopReason != nil && *tt.stopReason == "refusal" {
				content, blocks = `[]`, 0
			}
			tr := &transport{replies: []string{messageBody(tt.stopReason, content, 12, 7)}}
			resp, err := call(t, tr, "test-model", tt.structured, 2)
			succeeded := tt.stopReason == nil || *tt.stopReason == "end_turn" || *tt.stopReason == "stop_sequence"
			switch {
			case succeeded:
				if err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
				if a, ok := resp.Answers().Noul("positive"); !ok || a.Noul() != 1.0 {
					t.Errorf("noul positive = %v (present %v), want 1.0", a.Noul(), ok)
				}
			case *tt.stopReason == "max_tokens":
				wantNonAnswer(t, err, truncatedText)
			default:
				wantNonAnswer(t, err, "Anthropic response did not complete: "+*tt.stopReason+".")
			}
			report := reportOf(t, resp, err)
			if !succeeded && len(report.Debug.RetryReasons) != 0 {
				t.Errorf("retry reasons = %v, want none for a non-answer", report.Debug.RetryReasons)
			}
			requests := tr.requests()
			if len(requests) != 1 || len(report.Debug.Attempts) != 1 {
				t.Fatalf("%d requests and %d attempts, want 1 and 1", len(requests), len(report.Debug.Attempts))
			}
			attempt := report.Debug.Attempts[0]
			if !bytes.Equal(attempt.Request, requests[0]) {
				t.Errorf("attempt request = %s, want the body sent, %s", attempt.Request, requests[0])
			}
			if n := member(t, attempt.Response, "content").Len(); n != blocks {
				t.Errorf("llm_response content has %d blocks, want %d", n, blocks)
			}
			gotReason := member(t, attempt.Response, "stop_reason")
			switch {
			case tt.stopReason == nil && gotReason.Kind() != jsonx.KindNull:
				t.Errorf("llm_response stop_reason = %v, want null", gotReason.Text())
			case tt.stopReason != nil && gotReason.Text() != *tt.stopReason:
				t.Errorf("llm_response stop_reason = %q, want %q", gotReason.Text(), *tt.stopReason)
			}
			if !attempt.Info.Responded {
				t.Errorf("debug_info has no finish_reason")
			}
			if diff := gocmp.Diff(tt.stopReason, attempt.Info.FinishReason); diff != "" {
				t.Errorf("finish_reason (-want +got):\n%s", diff)
			}
			if (attempt.Info.Error != "") == succeeded {
				t.Errorf("debug_info error = %q, want one exactly when the call failed", attempt.Info.Error)
			}
			if _, err := report.MarshalJSON(); err != nil {
				t.Errorf("the Report does not encode: %v", err)
			}
		})
	}
}

// TestOutputLimit ports tests/test_provider_requests.py::test_anthropic_output_limit,
// one Go case for upstream's sync and async pair: a provider built with an
// output limit of 8192 sends it as max_tokens, and a response truncated at
// it fails with the truncation text even though its text is valid JSON,
// after one request and one attempt that kept the response.
func TestOutputLimit(t *testing.T) {
	tests := map[string]struct {
		structured, truncated bool
	}{
		"success: prompted":                 {structured: false, truncated: false},
		"success: structured":               {structured: true, truncated: false},
		"non-answer: prompted, truncated":   {structured: false, truncated: true},
		"non-answer: structured, truncated": {structured: true, truncated: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			stopReason, output := "end_turn", 10
			if tt.truncated {
				stopReason, output = "max_tokens", 8192
			}
			tr := &transport{replies: []string{messageBody(&stopReason, textContent, 20, output)}}
			resp, err := call(t, tr, "claude-haiku-4-5", tt.structured, 2, anthropic.WithMaxTokens(8192))
			if tt.truncated {
				wantNonAnswer(t, err, truncatedText)
			} else {
				if err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
				if a, ok := resp.Answers().Noul("positive"); !ok || a.Noul() != 1.0 {
					t.Errorf("noul positive = %v (present %v), want 1.0", a.Noul(), ok)
				}
			}
			report := reportOf(t, resp, err)
			requests := tr.requests()
			if len(requests) != 1 {
				t.Fatalf("%d requests, want 1", len(requests))
			}
			if got := member(t, requests[0], "max_tokens").Text(); got != "8192" {
				t.Errorf("request max_tokens = %s, want 8192", got)
			}
			if len(report.Debug.Attempts) != 1 {
				t.Fatalf("%d attempts, want 1", len(report.Debug.Attempts))
			}
			attempt := report.Debug.Attempts[0]
			if !bytes.Equal(attempt.Request, requests[0]) {
				t.Errorf("attempt request = %s, want the body sent, %s", attempt.Request, requests[0])
			}
			if got := member(t, attempt.Response, "content").Index(0); got.Kind() != jsonx.KindObject || memberText(got, "text") != answerText {
				t.Errorf("llm_response content[0].text = %q, want %q", memberText(got, "text"), answerText)
			}
			if diff := gocmp.Diff(&stopReason, attempt.Info.FinishReason); diff != "" {
				t.Errorf("finish_reason (-want +got):\n%s", diff)
			}
			if (attempt.Info.Error != "") != tt.truncated {
				t.Errorf("debug_info error = %q, want one exactly when truncated", attempt.Info.Error)
			}
		})
	}
}

// memberText returns the text of the member name of the object v.
func memberText(v jsonx.Node, name string) string {
	m, _ := v.Member(name)
	return m.Text()
}

// TestRefusalUnderEndTurnIsMalformed pins what upstream does with a refusal
// that arrives with stop_reason end_turn and refusal stop_details: it reads
// no stop_details, so the refusal sentence is the answer text, which fails
// validation and spends a malformed-structure retry; with a retry left the
// next reply answers, and with none the call ends as malformed output (200
// with no answers), not as a non-answer.
func TestRefusalUnderEndTurnIsMalformed(t *testing.T) {
	refusal := `{"id":"message-test","type":"message","role":"assistant","model":"test-model","stop_reason":"end_turn",` +
		`"stop_details":{"type":"refusal","category":"general_harms","explanation":null},` +
		`"content":[{"type":"text","text":"I can't help with that request."}],"usage":{"input_tokens":12,"output_tokens":9}}`
	endTurn := "end_turn"
	answer := messageBody(&endTurn, textContent, 12, 7)
	tests := map[string]struct {
		malformed    int
		wantRequests int
		wantAnswer   bool
	}{
		"success: the corrective retry answers":        {malformed: 2, wantRequests: 2, wantAnswer: true},
		"malformed: no corrective retry left to spend": {malformed: 0, wantRequests: 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tr := &transport{replies: []string{refusal, answer}}
			resp, err := call(t, tr, "test-model", true, tt.malformed)
			report := reportOf(t, resp, err)
			if tt.wantAnswer {
				if err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
				if a, ok := resp.Answers().Noul("positive"); !ok || a.Noul() != 1.0 {
					t.Errorf("noul positive = %v (present %v), want 1.0", a.Noul(), ok)
				}
				want := []adapter.RetryReason{{Category: "malformed_structure"}}
				if diff := gocmp.Diff(want, report.Debug.RetryReasons, gocmp.Transformer("category", func(r adapter.RetryReason) string { return r.Category })); diff != "" {
					t.Errorf("retry reasons (-want +got):\n%s", diff)
				}
			} else {
				rve, ok := errors.AsType[*decision.ResponseValidationError](err)
				if !ok || rve.StatusCode != http.StatusOK {
					t.Fatalf("error = %T %v, want a *decision.ResponseValidationError of status 200", err, err)
				}
				if _, ok := errors.AsType[*decision.APIError](err); ok {
					t.Errorf("the refusal ended as an API error, want malformed output")
				}
				if len(report.Debug.RetryReasons) != 0 {
					t.Errorf("retry reasons = %v, want none", report.Debug.RetryReasons)
				}
			}
			if n := len(tr.requests()); n != tt.wantRequests || len(report.Debug.Attempts) != tt.wantRequests {
				t.Fatalf("%d requests and %d attempts, want %d each", n, len(report.Debug.Attempts), tt.wantRequests)
			}
			first := report.Debug.Attempts[0]
			if first.Info.Error != "" || first.Info.FinishReason == nil || *first.Info.FinishReason != "end_turn" {
				t.Errorf("the refusal's attempt = error %q, finish_reason %v; want no error and end_turn", first.Info.Error, first.Info.FinishReason)
			}
		})
	}
}
