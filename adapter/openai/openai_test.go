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
	"io"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
	"github.com/zchee/decision-model-sdk-go/adapter/openai"
)

// The tests of this file and of nonanswer_test.go port upstream's OpenAI
// transport and non-answer tests through the Adapter and the root SDK, as
// upstream's go through its client: a real *openai.Provider talks to a
// round tripper of the test, and no request leaves the process. Upstream's
// sync and async cases are one case each.

// testKey is the key the tests give a Provider: a plain word, not a
// credential.
const testKey = "not-a-key"

// validAnswer is a model output that answers the question positive.
const validAnswer = `{"answers":{"positive":true}}`

// clearEnv unsets the variables openai.New reads for the rest of the test;
// t.Setenv restores each one when the test ends.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unsetting %s: %v", name, err)
		}
	}
}

// rtFunc is an http.RoundTripper of a test.
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// exchange records the request bodies a transport received, in order.
type exchange struct {
	mu     sync.Mutex
	bodies [][]byte
}

// add records body and returns how many bodies are recorded.
func (e *exchange) add(body []byte) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.bodies = append(e.bodies, body)
	return len(e.bodies)
}

// all returns the bodies recorded so far.
func (e *exchange) all() [][]byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.bodies)
}

// readBody reads and closes the request's body.
func readBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

// jsonResponse returns a 200 response to req whose body is v written by
// jsonx.
func jsonResponse(req *http.Request, v jsonx.Value) (*http.Response, error) {
	body, err := jsonx.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}

// member is a shorthand for a member of a jsonx object.
func member(name string, v jsonx.Value) jsonx.Member { return jsonx.Member{Name: name, Value: v} }

// messageOutput is a Responses output item of type message with one
// output_text part holding text.
func messageOutput(text string) jsonx.Value {
	return jsonx.Object(
		member("type", jsonx.String("message")),
		member("role", jsonx.String("assistant")),
		member("content", jsonx.Array(jsonx.Object(member("type", jsonx.String("output_text")), member("text", jsonx.String(text))))),
	)
}

// usageValue is a usage member of the two given names with upstream's
// counts 12 and 7 and a total of 19.
func usageValue(in, out string) jsonx.Value {
	return jsonx.Object(member(in, jsonx.Number("12")), member(out, jsonx.Number("7")), member("total_tokens", jsonx.Number("19")))
}

// read parses b with jsonx, failing the test when it is not JSON.
func read(t *testing.T, b []byte) jsonx.Node {
	t.Helper()
	n, err := jsonx.Read(b)
	if err != nil {
		t.Fatalf("reading %q: %v", b, err)
	}
	return n
}

// at returns the value at path in n, where a path element is a member name
// or, for an array, an index written as a decimal string ("-1" is the
// last); it fails the test when the path does not exist.
func at(t *testing.T, n jsonx.Node, path ...string) jsonx.Node {
	t.Helper()
	for _, p := range path {
		if n.Kind() == jsonx.KindArray {
			i := 0
			for _, c := range strings.TrimPrefix(p, "-") {
				i = i*10 + int(c-'0')
			}
			if strings.HasPrefix(p, "-") {
				i = n.Len() - i
			}
			if i < 0 || i >= n.Len() {
				t.Fatalf("index %s out of range of an array of %d", p, n.Len())
			}
			n = n.Index(i)
			continue
		}
		v, ok := n.Member(p)
		if !ok {
			t.Fatalf("no member %q", p)
		}
		n = v
	}
	return n
}

// has reports whether the object n has a member name.
func has(n jsonx.Node, name string) bool {
	_, ok := n.Member(name)
	return ok
}

// encode writes n back as JSON, failing the test on an error.
func encode(t *testing.T, n jsonx.Node) []byte {
	t.Helper()
	b, err := n.PydanticJSON()
	if err != nil {
		t.Fatalf("writing a value back: %v", err)
	}
	return b
}

// questions returns upstream's one question, a Noul named positive.
func questions(t *testing.T) *decision.Prepared {
	t.Helper()
	q, err := decision.NewQuestions().Noul("positive", decision.Noul{Instructions: decision.Text("The review is positive.")}).Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return q
}

// newClient returns the root SDK client of ad, closed when the test ends.
func newClient(t *testing.T, ad *adapter.Adapter) *decision.Client {
	t.Helper()
	c, err := adapter.NewClient(ad)
	if err != nil {
		t.Fatalf("adapter.NewClient: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("closing the client: %v", err)
		}
	})
	return c
}

// factory returns a factory that builds a real *openai.Provider over rt
// with the test key and opts.
func factory(rt http.RoundTripper, opts ...openai.Option) llm.Factory {
	return func(model string) (llm.Provider, error) {
		p, err := openai.New(model, append([]openai.Option{openai.WithHTTPClient(&http.Client{Transport: rt}), openai.WithAPIKey(testKey)}, opts...)...)
		if err != nil {
			return nil, err
		}
		return p, nil
	}
}

// modelID is the model string that selects test-model on the factory
// named openai.
var modelID = adapter.ModelID("openai", "test-model")

// TestTransportPreservesCorrectionsAndUsage ports
// tests/test_openai_transports.py::test_openai_transport_preserves_corrections_and_usage:
// through both APIs, chosen by the base URL or explicitly, in both output
// modes, a malformed first answer is corrected once; the usage keeps the
// last attempt's counts and the totals of both; each attempt records the
// body sent, the raw response and the api name; and the request bodies
// carry upstream's members.
func TestTransportPreservesCorrectionsAndUsage(t *testing.T) {
	const malformed = `{"answers":`
	tests := map[string]struct {
		base       string
		api        openai.API
		endpoint   string
		structured bool
	}{
		"success: default base URL, auto, prompted":   {endpoint: "/v1/responses"},
		"success: default base URL, auto, structured": {endpoint: "/v1/responses", structured: true},
		"success: compatible host, auto, prompted":    {base: "https://compatible.test/v1", endpoint: "/v1/chat/completions"},
		"success: compatible host, auto, structured":  {base: "https://compatible.test/v1", endpoint: "/v1/chat/completions", structured: true},
		"success: proxy host, responses, prompted":    {base: "https://proxy.test/v1", api: openai.Responses, endpoint: "/v1/responses"},
		"success: proxy host, responses, structured":  {base: "https://proxy.test/v1", api: openai.Responses, endpoint: "/v1/responses", structured: true},
		"success: default base URL, chat, prompted":   {api: openai.ChatCompletions, endpoint: "/v1/chat/completions"},
		"success: default base URL, chat, structured": {api: openai.ChatCompletions, endpoint: "/v1/chat/completions", structured: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			responses := tt.endpoint == "/v1/responses"
			var ex exchange
			rt := rtFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != tt.endpoint {
					t.Errorf("request path = %q, want %q", r.URL.Path, tt.endpoint)
				}
				body, err := readBody(r)
				if err != nil {
					return nil, err
				}
				text := validAnswer
				if ex.add(body) == 1 {
					text = malformed
				}
				if !responses {
					return jsonResponse(r, jsonx.Object(
						member("choices", jsonx.Array(jsonx.Object(member("message", jsonx.Object(member("role", jsonx.String("assistant")), member("content", jsonx.String(text))))))),
						member("usage", usageValue("prompt_tokens", "completion_tokens")),
					))
				}
				req, err := jsonx.Read(body)
				if err != nil {
					return nil, err
				}
				format, _ := req.Member("text")
				echo, err := format.Value(jsonx.Repr)
				if err != nil {
					return nil, err
				}
				return jsonResponse(r, jsonx.Object(
					member("status", jsonx.String("completed")),
					member("text", echo),
					member("output", jsonx.Array(
						jsonx.Object(
							member("id", jsonx.String("reasoning-test")),
							member("type", jsonx.String("reasoning")),
							member("summary", jsonx.Array(jsonx.Object(member("type", jsonx.String("summary_text")), member("text", jsonx.String("Ignored summary."))))),
						),
						messageOutput(text),
					)),
					member("usage", usageValue("input_tokens", "output_tokens")),
				))
			})
			var opts []openai.Option
			if tt.base != "" {
				opts = append(opts, openai.WithBaseURL(tt.base))
			}
			if tt.api != openai.Auto {
				opts = append(opts, openai.WithAPI(tt.api))
			}
			mode := adapter.Prompted
			if tt.structured {
				mode = adapter.Structured
			}
			ad, err := adapter.New(adapter.Discrete, mode, adapter.WithMalformedRetries(1), adapter.WithFactory("openai", factory(rt, opts...)))
			if err != nil {
				t.Fatalf("adapter.New: %v", err)
			}
			resp, err := newClient(t, ad).SystemOne(t.Context(), "A delightful book.", questions(t), decision.Model(modelID))
			if err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			if a, ok := resp.Answers().Noul("positive"); !ok || a.Noul() != 1.0 {
				t.Errorf("the answer positive = %v (present %t), want 1.0", a.Noul(), ok)
			}
			rep, err := adapter.ReportOf(resp)
			if err != nil {
				t.Fatalf("ReportOf: %v", err)
			}
			wantUsage := [6]any{
				llm.Count{N: 12, Known: true},
				llm.Count{N: 7, Known: true},
				llm.Count{N: 24, Known: true},
				llm.Count{N: 14, Known: true},
				0, 1,
			}
			u := rep.Usage
			if diff := gocmp.Diff(wantUsage, [6]any{u.InputTokens, u.OutputTokens, u.InputTokensTotal, u.OutputTokensTotal, u.Retries, u.MalformedRetries}); diff != "" {
				t.Errorf("usage mismatch (-want +got):\n%s", diff)
			}

			bodies := ex.all()
			attempts := rep.Debug.Attempts
			if len(attempts) != 2 || len(bodies) != 2 {
				t.Fatalf("%d attempts and %d requests, want 2 and 2", len(attempts), len(bodies))
			}
			wantAPI := "chat_completions"
			if responses {
				wantAPI = "responses"
			}
			for i, a := range attempts {
				if !bytes.Equal(a.Request, bodies[i]) {
					t.Errorf("attempt %d: request = %s, want the body sent %s", i, a.Request, bodies[i])
				}
				if want := []int{2, 4}[i]; len(a.Messages) != want {
					t.Errorf("attempt %d: %d messages, want %d", i, len(a.Messages), want)
				}
				raw := read(t, a.Response)
				var text jsonx.Node
				if responses {
					sentFormat := at(t, read(t, a.Request), "text", "format")
					rawFormat := at(t, raw, "text", "format")
					for j := range sentFormat.Len() {
						key := sentFormat.Name(j)
						if eq, err := jsonx.Equal(encode(t, sentFormat.Index(j)), encode(t, at(t, rawFormat, key))); err != nil || !eq {
							t.Errorf("attempt %d: the raw response's text.format.%s differs from the request's", i, key)
						}
					}
					text = at(t, raw, "output", "1", "content", "0", "text")
				} else {
					text = at(t, raw, "choices", "0", "message", "content")
				}
				want := validAnswer
				if i == 0 {
					want = malformed
				}
				if text.Text() != want {
					t.Errorf("attempt %d: the raw response's text = %q, want %q", i, text.Text(), want)
				}
				if a.Info.API != wantAPI {
					t.Errorf("attempt %d: api = %q, want %q", i, a.Info.API, wantAPI)
				}
			}
			if _, err := rep.MarshalJSON(); err != nil {
				t.Errorf("Report.MarshalJSON: %v", err)
			}

			for i, b := range bodies {
				body := read(t, b)
				if responses {
					if store := at(t, body, "store"); store.Kind() != jsonx.KindFalse {
						t.Errorf("request %d: store is not false", i)
					}
					if has(body, "previous_response_id") {
						t.Errorf("request %d: previous_response_id is sent", i)
					}
					first := at(t, body, "input", "0", "content").Text()
					if ins, ok := body.Member("instructions"); ok {
						first = ins.Text()
					}
					if !strings.HasPrefix(first, "Evaluate every question") {
						t.Errorf("request %d: the system prompt starts %q", i, first[:min(len(first), 40)])
					}
					if !tt.structured && !bytes.Contains(encode(t, at(t, body, "input")), []byte("JSON")) {
						t.Errorf("request %d: input does not hold the word JSON in prompted mode", i)
					}
					format := at(t, body, "text", "format")
					wantType := "json_object"
					if tt.structured {
						wantType = "json_schema"
					}
					if got := at(t, format, "type").Text(); got != wantType {
						t.Errorf("request %d: text.format.type = %q, want %q", i, got, wantType)
					}
					if tt.structured {
						if at(t, format, "strict").Kind() != jsonx.KindTrue {
							t.Errorf("request %d: text.format.strict is not true", i)
						}
						if !has(at(t, format, "schema", "$defs", "TypeSafeAnswers", "properties"), "positive") {
							t.Errorf("request %d: the schema has no property positive", i)
						}
					}
					wantRole := "system"
					if tt.structured {
						wantRole = "user"
					}
					if got := at(t, body, "input", "0", "role").Text(); got != wantRole {
						t.Errorf("request %d: input[0].role = %q, want %q", i, got, wantRole)
					}
					continue
				}
				if got := at(t, body, "messages", "0", "role").Text(); got != "system" {
					t.Errorf("request %d: messages[0].role = %q, want system", i, got)
				}
				format := at(t, body, "response_format")
				if tt.structured && at(t, format, "json_schema", "strict").Kind() != jsonx.KindTrue {
					t.Errorf("request %d: response_format.json_schema.strict is not true", i)
				}
				if !tt.structured && format.Kind() != jsonx.KindNull {
					t.Errorf("request %d: response_format is not null in prompted mode", i)
				}
			}

			list := "messages"
			if responses {
				list = "input"
			}
			last := at(t, read(t, bodies[1]), list)
			if got := string(encode(t, at(t, last, "-2"))); got != `{"role":"assistant","content":"{\"answers\":"}` {
				t.Errorf("the corrective request's last-but-one message = %s, want the malformed answer", got)
			}
			if role := at(t, last, "-1", "role").Text(); role != "user" {
				t.Errorf("the corrective request's last message has role %q, want user", role)
			}
			if content := at(t, last, "-1", "content").Text(); !strings.Contains(content, "previous response did not match") {
				t.Errorf("the corrective request's last message = %q, want the correction prompt", content)
			}
		})
	}
}

// concurrencyBound is how long TestConcurrentAttemptsAreIsolated waits for
// its two calls before it fails with every goroutine's stack: a lock that
// never opens would otherwise hang until go test's -timeout.
const concurrencyBound = 30 * time.Second

// TestConcurrentAttemptsAreIsolated ports
// tests/test_openai_transports.py::test_concurrent_attempts_are_isolated_and_preserve_failed_responses:
// two calls in flight at once through one Provider each keep their own
// attempt, request, raw response and finish reason, a failed or incomplete
// first call included; a later direct call of the Provider changes neither
// Report. The calls run on real goroutines, released together by the
// transport once both requests have arrived.
func TestConcurrentAttemptsAreIsolated(t *testing.T) {
	for _, firstStatus := range []string{"completed", "incomplete", "failed"} {
		t.Run("success: first call "+firstStatus, func(t *testing.T) {
			clearEnv(t)
			var ex exchange
			ready := make(chan struct{})
			var release sync.Once
			rt := rtFunc(func(r *http.Request) (*http.Response, error) {
				body, err := readBody(r)
				if err != nil {
					return nil, err
				}
				if ex.add(body) >= 2 {
					release.Do(func() { close(ready) })
				}
				select {
				case <-ready:
				case <-time.After(5 * time.Second):
					return nil, errors.New("the other request did not arrive within 5 s")
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
				req, err := jsonx.Read(body)
				if err != nil {
					return nil, err
				}
				input, _ := req.Member("input")
				document := ""
				if input.Len() > 0 {
					content, _ := input.Index(0).Member("content")
					document = content.Text()
				}
				status := "completed"
				if strings.Contains(document, "first document") {
					status = firstStatus
				}
				var errValue, details jsonx.Value
				switch status {
				case "failed":
					errValue = jsonx.Object(member("code", jsonx.String("server_error")), member("message", jsonx.String("generation failed")))
				case "incomplete":
					details = jsonx.Object(member("reason", jsonx.String("max_output_tokens")))
				}
				return jsonResponse(r, jsonx.Object(
					member("status", jsonx.String(status)),
					member("error", errValue),
					member("incomplete_details", details),
					member("output", jsonx.Array(messageOutput(validAnswer))),
					member("usage", usageValue("input_tokens", "output_tokens")),
				))
			})
			p, err := openai.New("test-model", openai.WithAPI(openai.Responses), openai.WithHTTPClient(&http.Client{Transport: rt}), openai.WithAPIKey(testKey))
			if err != nil {
				t.Fatalf("openai.New: %v", err)
			}
			ad, err := adapter.New(adapter.Discrete, adapter.Structured, adapter.WithProvider("openai", p), adapter.WithDefaultModel("openai"))
			if err != nil {
				t.Fatalf("adapter.New: %v", err)
			}
			c := newClient(t, ad)

			type outcome struct {
				report *adapter.Report
				err    error
			}
			documents := [2]string{"first document", "second document"}
			qs := [2]*decision.Prepared{questions(t), questions(t)}
			results := [2]chan outcome{make(chan outcome, 1), make(chan outcome, 1)}
			for i, document := range documents {
				go func() {
					resp, err := c.SystemOne(t.Context(), document, qs[i])
					if err != nil {
						rep, ok := adapter.ReportFromError(err)
						if !ok {
							results[i] <- outcome{err: err}
							return
						}
						results[i] <- outcome{report: rep}
						return
					}
					rep, err := adapter.ReportOf(resp)
					results[i] <- outcome{report: rep, err: err}
				}()
			}
			var reports [2]*adapter.Report
			deadline := time.After(concurrencyBound)
			for i := range results {
				select {
				case o := <-results[i]:
					if o.err != nil {
						t.Fatalf("call %q: %v", documents[i], o.err)
					}
					reports[i] = o.report
				case <-deadline:
					buf := make([]byte, 1<<20)
					n := runtime.Stack(buf, true)
					t.Fatalf("the two calls did not return within %v; every goroutine:\n%s", concurrencyBound, buf[:n])
				}
			}

			bodies := ex.all()
			for i, status := range []string{firstStatus, "completed"} {
				debug := reports[i].Debug
				if len(debug.Attempts) != 1 {
					t.Fatalf("call %q: %d attempts, want 1", documents[i], len(debug.Attempts))
				}
				a := debug.Attempts[0]
				if !strings.Contains(a.Messages[1].Content, documents[i]) {
					t.Errorf("call %q: messages[1] does not hold its document", documents[i])
				}
				if content := at(t, read(t, a.Request), "input", "0", "content").Text(); !strings.Contains(content, documents[i]) {
					t.Errorf("call %q: the request's input[0] does not hold its document", documents[i])
				}
				if !slices.ContainsFunc(bodies, func(b []byte) bool { return bytes.Equal(b, a.Request) }) {
					t.Errorf("call %q: the attempt's request is none of the bodies sent", documents[i])
				}
				if got := at(t, read(t, a.Response), "status").Text(); got != status {
					t.Errorf("call %q: llm_response.status = %q, want %q", documents[i], got, status)
				}
				if a.Info.FinishReason == nil || *a.Info.FinishReason != status {
					t.Errorf("call %q: finish_reason = %v, want %q", documents[i], a.Info.FinishReason, status)
				}
				if (a.Info.Error != "") != (status != "completed") {
					t.Errorf("call %q: error = %q, want one exactly when the status is not completed", documents[i], a.Info.Error)
				}
			}

			// A later direct call of the Provider must not change either
			// finished Report.
			before := marshalReports(t, reports)
			a := reports[1].Debug.Attempts[0]
			if _, err := p.Do(t.Context(), &llm.Request{Messages: a.Messages, Schema: a.Schema, Structured: a.Structured}); err != nil {
				t.Fatalf("the direct Do: %v", err)
			}
			if diff := gocmp.Diff(before, marshalReports(t, reports)); diff != "" {
				t.Errorf("a direct call changed the Reports (-before +after):\n%s", diff)
			}
		})
	}
}

// marshalReports returns the JSON of each Report.
func marshalReports(t *testing.T, reports [2]*adapter.Report) [2]string {
	t.Helper()
	var out [2]string
	for i, r := range reports {
		b, err := r.MarshalJSON()
		if err != nil {
			t.Fatalf("Report.MarshalJSON: %v", err)
		}
		out[i] = string(b)
	}
	return out
}
