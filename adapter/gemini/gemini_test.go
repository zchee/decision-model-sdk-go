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

package gemini_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter"
	"github.com/zchee/decision-model-sdk-go/adapter/gemini"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// recorder is an http.RoundTripper of the test's own that keeps the body
// of each request and answers with respond. No request leaves the process.
type recorder struct {
	mu      sync.Mutex
	bodies  [][]byte
	respond func(req *http.Request, n int) (*http.Response, error)
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	_ = req.Body.Close()
	r.mu.Lock()
	r.bodies = append(r.bodies, body)
	n := len(r.bodies)
	r.mu.Unlock()
	return r.respond(req, n)
}

// sent returns the bodies received, in order.
func (r *recorder) sent() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.bodies...)
}

// interaction is upstream's _interaction_payload
// (tests/test_gemini_transports.py:20-36): a completed interaction, or one
// of status, whose one model_output step holds text.
func interaction(t *testing.T, text, status string) string {
	t.Helper()
	b, err := jsonx.Marshal(jsonx.Object(
		jsonx.Member{Name: "id", Value: jsonx.String("interaction-test")},
		jsonx.Member{Name: "status", Value: jsonx.String(status)},
		jsonx.Member{Name: "model", Value: jsonx.String("gemini-3.8-flash")},
		jsonx.Member{Name: "steps", Value: jsonx.Array(jsonx.Object(
			jsonx.Member{Name: "type", Value: jsonx.String("model_output")},
			jsonx.Member{Name: "content", Value: jsonx.Array(jsonx.Object(
				jsonx.Member{Name: "type", Value: jsonx.String("text")},
				jsonx.Member{Name: "text", Value: jsonx.String(text)},
			))},
		))},
		jsonx.Member{Name: "usage", Value: jsonx.Object(
			jsonx.Member{Name: "total_input_tokens", Value: jsonx.Number("12")},
			jsonx.Member{Name: "total_output_tokens", Value: jsonx.Number("7")},
			jsonx.Member{Name: "total_tokens", Value: jsonx.Number("19")},
		)},
	))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(b)
}

// ok answers a request with status 200 and body.
func ok(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

// client returns a root SDK client from adapter.NewClient over an Adapter
// in discrete mode with output, whose gemini factory builds the real
// Provider over rt with a made-up key, and opts after those; it is closed
// when t ends.
func client(t *testing.T, output adapter.OutputMode, rt http.RoundTripper, opts ...adapter.Option) *decision.Client {
	t.Helper()
	factory := func(model string) (llm.Provider, error) {
		p, err := gemini.New(model, gemini.WithAPIKey("not-a-key"), gemini.WithHTTPClient(&http.Client{Transport: rt}))
		if err != nil {
			return nil, err
		}
		return p, nil
	}
	ad, err := adapter.New(adapter.Discrete, output, append([]adapter.Option{adapter.WithFactory("gemini", factory)}, opts...)...)
	if err != nil {
		t.Fatalf("adapter.New: %v", err)
	}
	c, err := adapter.NewClient(ad)
	if err != nil {
		t.Fatalf("adapter.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// questions is upstream's one noul question "positive".
func questions(t *testing.T) *decision.Prepared {
	t.Helper()
	q, err := decision.NewQuestions().Noul("positive", decision.Noul{Instructions: decision.Text("The review is positive.")}).Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return q
}

// model is the call option that selects the gemini provider.
var model = decision.Model(adapter.ModelID("gemini", "gemini-3.8-flash"))

// member returns the value at the path of names in the JSON text doc.
func member(t *testing.T, doc []byte, names ...string) (jsonx.Node, bool) {
	t.Helper()
	v, err := jsonx.Read(doc)
	if err != nil {
		t.Fatalf("Read(%s): %v", doc, err)
	}
	for _, name := range names {
		var ok bool
		if v, ok = v.Member(name); !ok {
			return v, false
		}
	}
	return v, true
}

// TestTransportPreservesCorrectionsAndUsage checks a corrective retry
// through the Adapter and the root SDK, in both output modes
// (tests/test_gemini_transports.py::test_gemini_transport_preserves_corrections_and_usage,
// its sync and async cases being one Go case): the first answer is cut off,
// the second is valid; the usage adds both attempts' counts; each attempt
// records the body the transport received; the first request has store
// false, the system instruction and, in structured mode only, the schema in
// response_format; the second request ends with the cut-off answer as a
// model_output step and the correction as a user_input step. llm_response
// is the body as received, not a vendor SDK's dump of it.
func TestTransportPreservesCorrectionsAndUsage(t *testing.T) {
	const malformed = `{"answers":`
	tests := map[string]struct {
		output     adapter.OutputMode
		structured bool
	}{
		"prompted":   {output: adapter.Prompted},
		"structured": {output: adapter.Structured, structured: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			replies := []string{interaction(t, malformed, "completed"), interaction(t, `{"answers":{"positive":true}}`, "completed")}
			rec := &recorder{respond: func(req *http.Request, n int) (*http.Response, error) {
				return ok(req, replies[min(n, 2)-1]), nil
			}}
			c := client(t, tt.output, rec, adapter.WithMalformedRetries(1))
			resp, err := c.SystemOne(t.Context(), "A delightful book.", questions(t), model)
			if err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			if noul, ok := resp.Answers().Noul("positive"); !ok || noul.Noul() != 1.0 {
				t.Errorf("noul positive = %v, %v; want 1.0", noul.Noul(), ok)
			}
			report, err := adapter.ReportOf(resp)
			if err != nil {
				t.Fatalf("ReportOf: %v", err)
			}
			u := report.Usage
			counts := [4]llm.Count{u.InputTokens, u.OutputTokens, u.InputTokensTotal, u.OutputTokensTotal}
			want := [4]llm.Count{{N: 12, Known: true}, {N: 7, Known: true}, {N: 24, Known: true}, {N: 14, Known: true}}
			if counts != want || u.Retries != 0 || u.MalformedRetries != 1 {
				t.Errorf("usage (input, output, input total, output total) = %v, retries %d, malformed retries %d; want %v, 0, 1", counts, u.Retries, u.MalformedRetries, want)
			}
			sent := rec.sent()
			attempts := report.Debug.Attempts
			if len(attempts) != 2 || len(sent) != 2 {
				t.Fatalf("attempts = %d, requests = %d; want 2 each", len(attempts), len(sent))
			}
			for i, a := range attempts {
				if string(a.Request) != string(sent[i]) {
					t.Errorf("attempt %d request =\n%s\nwant the body sent\n%s", i, a.Request, sent[i])
				}
				if string(a.Response) != replies[i] {
					t.Errorf("attempt %d llm_response =\n%s\nwant the body as received\n%s", i, a.Response, replies[i])
				}
				if a.Info.API != "interactions" || a.Info.ModelName != "gemini-3.8-flash" || a.Info.Provider != "github.com/zchee/decision-model-sdk-go/adapter/gemini.Provider" {
					t.Errorf("attempt %d debug_info = %+v", i, a.Info)
				}
			}
			if store, _ := member(t, sent[0], "store"); store.Kind() != jsonx.KindFalse {
				t.Errorf("store = %v, want false", store.Kind())
			}
			if si, _ := member(t, sent[0], "system_instruction"); !strings.HasPrefix(si.Text(), "Evaluate every question") {
				t.Errorf("system_instruction = %q, want it to start with Evaluate every question", si.Text())
			}
			_, hasFormat := member(t, sent[0], "response_format")
			if hasFormat != tt.structured {
				t.Errorf("response_format present = %v, want %v", hasFormat, tt.structured)
			}
			if tt.structured {
				if _, ok := member(t, sent[0], "response_format", "schema", "$defs", "TypeSafeAnswers", "properties", "positive"); !ok {
					t.Errorf("response_format.schema lacks $defs.TypeSafeAnswers.properties.positive")
				}
			}
			input, _ := member(t, sent[1], "input")
			n := input.Len()
			if n < 2 {
				t.Fatalf("second request input has %d steps", n)
			}
			last2, err := input.Index(n - 2).PydanticJSON()
			if err != nil {
				t.Fatalf("PydanticJSON: %v", err)
			}
			if want := `{"type":"model_output","content":[{"type":"text","text":"{\"answers\":"}]}`; string(last2) != want {
				t.Errorf("second-to-last step = %s, want %s", last2, want)
			}
			lastStep := input.Index(n - 1)
			kind, _ := lastStep.Member("type")
			content, _ := lastStep.Member("content")
			text, _ := content.Index(0).Member("text")
			if kind.Text() != "user_input" || !strings.Contains(text.Text(), "previous response did not match") {
				t.Errorf("last step = %s %q, want a user_input step with the correction", kind.Text(), text.Text())
			}
		})
	}
}

// TestIncompleteResponseIsNotAnAnswer checks that an interaction whose
// status is incomplete fails the call through the Adapter and the root SDK
// as a non-answer
// (tests/test_gemini_transports.py::test_gemini_incomplete_http_response_is_not_an_answer):
// status 424 with error_type non_answer and upstream's whole text, one
// request (a non-answer is never retried), and a Report whose attempt keeps
// the body and its status as the finish reason.
func TestIncompleteResponseIsNotAnAnswer(t *testing.T) {
	reply := interaction(t, `{"answers":{"positive":true}}`, "incomplete")
	rec := &recorder{respond: func(req *http.Request, _ int) (*http.Response, error) { return ok(req, reply), nil }}
	c := client(t, adapter.Structured, rec, adapter.WithRetry(adapter.NoRetry().MaxRetries(2).Backoff(0, 5*time.Second, 0.25)))
	_, err := c.SystemOne(t.Context(), "A delightful book.", questions(t), model)
	apiErr, ok := errors.AsType[*decision.APIError](err)
	if !ok {
		t.Fatalf("SystemOne error = %#v, want a *decision.APIError", err)
	}
	if apiErr.StatusCode != http.StatusFailedDependency || apiErr.ErrorType != "non_answer" || apiErr.Message != "Gemini response did not complete: incomplete." {
		t.Errorf("APIError = %d %q %q, want 424 non_answer with upstream's text", apiErr.StatusCode, apiErr.ErrorType, apiErr.Message)
	}
	if n := len(rec.sent()); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
	report, ok := adapter.ReportFromError(err)
	if !ok || len(report.Debug.Attempts) != 1 {
		t.Fatalf("ReportFromError = %v, %v; want one attempt", report, ok)
	}
	a := report.Debug.Attempts[0]
	if string(a.Response) != reply || a.Info.FinishReason == nil || *a.Info.FinishReason != "incomplete" {
		t.Errorf("attempt llm_response %s finish %v, want the body with finish reason incomplete", a.Response, a.Info.FinishReason)
	}
}

// timeoutError is a transport error that reports a timeout, as httpx's
// ReadTimeout does upstream: a net.Error whose Timeout is true.
type timeoutError struct{}

func (timeoutError) Error() string   { return "read timed out" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return false }

// TestTransportErrorsObeyRetryBudget checks that a transport failure is
// retried as the Adapter's policy allows and then fails the call with the
// root SDK's error of its kind, through which errors.As reaches the
// Adapter's *adapter.Error with the Report
// (tests/test_gemini_transports.py::test_gemini_transport_errors_obey_retry_budget,
// its sync and async cases being one Go case): with a budget of n retries
// the transport sees n+1 requests, and the Report has as many attempts. A
// connection error that is not a timeout stands for httpx's ConnectError
// and one that reports a timeout for its ReadTimeout. Each case runs in a
// synctest bubble, so the policy's waits pass on the bubble's clock.
func TestTransportErrorsObeyRetryBudget(t *testing.T) {
	tests := map[string]struct {
		budget   int
		err      error
		wantKind adapter.ErrorKind
	}{
		"a connection error, no retry":  {budget: 0, err: errors.New("unavailable"), wantKind: adapter.KindConnection},
		"a connection error, one retry": {budget: 1, err: errors.New("unavailable"), wantKind: adapter.KindConnection},
		"a timeout, no retry":           {budget: 0, err: timeoutError{}, wantKind: adapter.KindTimeout},
		"a timeout, one retry":          {budget: 1, err: timeoutError{}, wantKind: adapter.KindTimeout},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := &recorder{respond: func(*http.Request, int) (*http.Response, error) { return nil, tt.err }}
				retry := adapter.NoRetry().MaxRetries(tt.budget).Backoff(0, 5*time.Second, 0.25)
				c := client(t, adapter.Prompted, rec, adapter.WithRetry(retry))
				_, err := c.SystemOne(t.Context(), "A delightful book.", questions(t), model)
				switch tt.wantKind {
				case adapter.KindTimeout:
					if _, ok := errors.AsType[*decision.TimeoutError](err); !ok {
						t.Fatalf("SystemOne error = %#v, want a *decision.TimeoutError", err)
					}
				default:
					if _, ok := errors.AsType[*decision.ConnectionError](err); !ok {
						t.Fatalf("SystemOne error = %#v, want a *decision.ConnectionError", err)
					}
				}
				ae, ok := errors.AsType[*adapter.Error](err)
				if !ok || ae.Kind != tt.wantKind || ae.Provider != "gemini" || ae.Model != "gemini-3.8-flash" {
					t.Fatalf("errors.As(*adapter.Error) = %v %+v, want kind %v from gemini", ok, ae, tt.wantKind)
				}
				sent := rec.sent()
				if len(sent) != tt.budget+1 {
					t.Errorf("requests = %d, want %d", len(sent), tt.budget+1)
				}
				report, ok := adapter.ReportFromError(err)
				if !ok || len(report.Debug.Attempts) != len(sent) {
					t.Errorf("ReportFromError = %v, %v; want %d attempts", report, ok, len(sent))
				}
			})
		})
	}
}
