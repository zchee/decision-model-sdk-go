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
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/fake"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// noulBody is a System One body that asks noulQuestions' question with
// model "fake".
const noulBody = `{"state":"state","model":"fake","questions":{"answer":{"type":"noul","instructions":"The review is positive."}}}`

// rawRequest returns a request as the SDK would send it to the Adapter,
// with body as its body, or no body when body is empty.
func rawRequest(ctx context.Context, method, path, body string) *http.Request {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.typesafe.ai"+path, rd)
	if err != nil {
		panic(err)
	}
	return req
}

// send calls ad.RoundTrip with req and returns the status and the body read
// to its end, or RoundTrip's error. It fails the test when RoundTrip breaks
// the http.RoundTripper contract: a response and an error, neither, a nil
// body, or a Request other than req.
func send(t testing.TB, ad *Adapter, req *http.Request) (int, []byte, error) {
	t.Helper()
	resp, err := ad.RoundTrip(req)
	switch {
	case err != nil && resp != nil:
		t.Fatalf("RoundTrip returned a response and the error %v", err)
	case err != nil:
		return 0, nil, err
	case resp == nil:
		t.Fatal("RoundTrip returned no response and no error")
	case resp.Body == nil:
		t.Fatal("RoundTrip returned a response without a body")
	case resp.Request != req:
		t.Fatal("the response's Request is not the request")
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the response body: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("closing the response body: %v", err)
	}
	if got, want := resp.Header.Get("Content-Length"), strconv.Itoa(len(b)); got != want || resp.ContentLength != int64(len(b)) {
		t.Errorf("Content-Length %q, ContentLength %d; the body has %d bytes", got, resp.ContentLength, len(b))
	}
	return resp.StatusCode, b, nil
}

// detailOf returns detail.message and detail.error_type of an error body.
func detailOf(t testing.TB, body []byte) (message, errorType string) {
	t.Helper()
	root, err := jsonx.Read(body)
	if err != nil {
		t.Fatalf("the body is not JSON: %v: %s", err, body)
	}
	detail, ok := root.Member("detail")
	if !ok {
		t.Fatalf("the body has no detail: %s", body)
	}
	m, _ := detail.Member("message")
	e, _ := detail.Member("error_type")
	return m.Text(), e.Text()
}

// customError is a provider error of a type the Adapter does not know.
type customError struct{ text string }

func (e *customError) Error() string { return e.text }

// statusFailure is a provider status error with upstream's error body of
// the status tests (tests/utils/test_error_handling.py:60).
func statusFailure(status int) error {
	return &llm.StatusError{StatusCode: status, Body: []byte(`{"error":{"message":"boom"}}`)}
}

// classWant is what a caller sees of one failure class.
type classWant struct {
	// errType is the dynamic type of the error SystemOne returns.
	errType string
	// status, kind, message and errorType are those of an *APIError or a
	// *ResponseValidationError; zero when the error is neither.
	status    int
	kind      decision.APIErrorKind
	message   string
	errorType string
	// adapterKind is the Kind of the *Error errors.As reaches; 0 for none.
	adapterKind ErrorKind
	// is is a sentinel the error matches with errors.Is.
	is error
	// report says ReportFromError returns a Report; attemptClass is its
	// first attempt's error_type.
	report       bool
	attemptClass string
	// retried is Stats().Attempts on a client with the SDK's DefaultRetry.
	retried uint64
}

// classCase is one failure class: the Adapter, the call, and what the
// caller sees.
type classCase struct {
	// outcome is what the provider answers; ignored when build is set.
	outcome fake.Outcome
	// build returns the Adapter and the number of provider requests made.
	build     func(t testing.TB) (*Adapter, func() int)
	call      []decision.CallOption
	questions func(t testing.TB) *decision.Prepared
	state     any
	closed    bool
	// perAttempt is the provider requests one RoundTrip makes.
	perAttempt int
	want       classWant
}

// statusCase is the classCase of a provider status that the Adapter
// answers with answered, and whose attempt records class.
func statusCase(status, answered int, kind decision.APIErrorKind, class string, retried uint64) classCase {
	return classCase{
		outcome:    fake.Error(statusFailure(status)),
		perAttempt: 1,
		want: classWant{
			errType: "*decision.APIError", status: answered, kind: kind, message: strconv.Itoa(status) + " boom", errorType: "provider_status",
			report: true, attemptClass: class, retried: retried,
		},
	}
}

// deepState returns a value nested n arrays deep around an empty array.
func deepState(n int) any {
	var v any = []any{}
	for range n - 1 {
		v = []any{v}
	}
	return v
}

// classCases returns every row of the Adapter's failure classes, keyed by
// the class.
func classCases() map[string]classCase {
	noDefault := func(t testing.TB) (*Adapter, func() int) {
		p := fake.New(fake.Text(noulAnswer))
		ad, err := New(Probabilities, Structured, WithProvider("fake", p))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return ad, p.Calls
	}
	cases := map[string]classCase{
		"invalid_body: model is not a string": {
			outcome: fake.Text(noulAnswer),
			call:    []decision.CallOption{decision.ExtraBody("model", 5)},
			want:    classWant{errType: "*decision.APIError", status: 400, kind: decision.APIErrorBadRequest, message: "The request body's model is not a string.", errorType: "invalid_body", retried: 1},
		},
		"invalid_body: questions is null": {
			outcome: fake.Text(noulAnswer),
			call:    []decision.CallOption{decision.ExtraBody("questions", nil)},
			want:    classWant{errType: "*decision.APIError", status: 400, kind: decision.APIErrorBadRequest, message: "The request body's questions is not a JSON object.", errorType: "invalid_body", retried: 1},
		},
		"model_required": {
			build: noDefault,
			want:  classWant{errType: "*decision.APIError", status: 400, kind: decision.APIErrorBadRequest, message: "An LLM model is required on the client or call.", errorType: "model_required", retried: 1},
		},
		"provider_required": {
			build: noDefault,
			call:  []decision.CallOption{decision.Model("gpt-4o-mini")},
			want:  classWant{errType: "*decision.APIError", status: 400, kind: decision.APIErrorBadRequest, message: "A provider is required: set provider='openai', 'anthropic', or 'gemini', or pass a provider instance as the model.", errorType: "provider_required", retried: 1},
		},
		"provider_config": {
			build: func(t testing.TB) (*Adapter, func() int) {
				ad, err := New(Probabilities, Structured, WithDefaultModel("openai:gpt-x"), WithFactory("openai", func(string) (llm.Provider, error) {
					return nil, errors.New("no API key is set")
				}))
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				return ad, func() int { return 0 }
			},
			want: classWant{errType: "*decision.APIError", status: 400, kind: decision.APIErrorBadRequest, message: "no API key is set", errorType: "provider_config", retried: 1},
		},
		"invalid_questions": {
			outcome: fake.Text(noulAnswer),
			questions: func(t testing.TB) *decision.Prepared {
				t.Helper()
				q, err := decision.NewQuestions().Score("stars", decision.Score{Instructions: decision.Text("Rating."), Levels: []decision.Content{decision.Text("Good.")}}).Prepare()
				if err != nil {
					t.Fatalf("Prepare: %v", err)
				}
				return q
			},
			want: classWant{errType: "*decision.APIError", status: 422, kind: decision.APIErrorUnprocessableEntity, message: "Score and choice questions require at least two criteria.", errorType: "invalid_questions", retried: 1},
		},
		"invalid_state: nested too deeply": {
			outcome: fake.Text(noulAnswer),
			state:   deepState(256),
			want:    classWant{errType: "*decision.APIError", status: 422, kind: decision.APIErrorUnprocessableEntity, message: "State is nested too deeply to be written into the prompt.", errorType: "invalid_state", retried: 1},
		},
		"provider status 400": statusCase(400, 400, decision.APIErrorBadRequest, "TypeSafeBadRequestError", 1),
		"provider status 401": statusCase(401, 401, decision.APIErrorAuthentication, "TypeSafeAuthenticationError", 1),
		"provider status 403": statusCase(403, 403, decision.APIErrorPermissionDenied, "TypeSafePermissionDeniedError", 1),
		"provider status 404": statusCase(404, 404, decision.APIErrorNotFound, "TypeSafeNotFoundError", 1),
		"provider status 408": statusCase(408, 408, decision.APIErrorOther, "TypeSafeAPIError", 3),
		"provider status 418": statusCase(418, 418, decision.APIErrorOther, "TypeSafeAPIError", 1),
		"provider status 422": statusCase(422, 422, decision.APIErrorUnprocessableEntity, "TypeSafeUnprocessableEntityError", 1),
		"provider status 429": statusCase(429, 429, decision.APIErrorRateLimit, "TypeSafeRateLimitError", 3),
		"provider status 500": statusCase(500, 500, decision.APIErrorInternalServer, "TypeSafeInternalServerError", 3),
		"provider status 503": statusCase(503, 503, decision.APIErrorInternalServer, "TypeSafeInternalServerError", 3),
		"provider status 599": statusCase(599, 599, decision.APIErrorInternalServer, "TypeSafeInternalServerError", 3),
		"provider status 399": statusCase(399, 424, decision.APIErrorOther, "TypeSafeAPIError", 1),
		"provider status 302": statusCase(302, 424, decision.APIErrorOther, "TypeSafeAPIError", 1),
		"provider status 200": statusCase(200, 424, decision.APIErrorOther, "TypeSafeAPIError", 1),
		"provider status 0":   statusCase(0, 424, decision.APIErrorOther, "TypeSafeAPIError", 1),
		"provider status 600": statusCase(600, 424, decision.APIErrorOther, "TypeSafeInternalServerError", 1),
		"provider timeout": {
			outcome:    fake.Error(&llm.TimeoutError{}),
			perAttempt: 1,
			want:       classWant{errType: "*decision.TimeoutError", adapterKind: KindTimeout, is: context.DeadlineExceeded, report: true, attemptClass: "TypeSafeAPITimeoutError", retried: 3},
		},
		"provider connection failure": {
			outcome:    fake.Error(&llm.ConnectionError{Err: syscall.ECONNREFUSED}),
			perAttempt: 1,
			want:       classWant{errType: "*decision.ConnectionError", adapterKind: KindConnection, is: syscall.ECONNREFUSED, report: true, attemptClass: "TypeSafeAPIConnectionError", retried: 3},
		},
		"non_answer": {
			outcome:    fake.Error(&llm.NonAnswerError{Message: "The model stopped before it finished its answer."}),
			perAttempt: 1,
			want:       classWant{errType: "*decision.APIError", status: 424, kind: decision.APIErrorOther, message: "The model stopped before it finished its answer.", errorType: "non_answer", report: true, attemptClass: "TypeSafeError", retried: 1},
		},
		"provider_error": {
			outcome:    fake.Error(&customError{text: "custom provider failure"}),
			perAttempt: 1,
			want:       classWant{errType: "*decision.APIError", status: 424, kind: decision.APIErrorOther, message: "custom provider failure", errorType: "provider_error", report: true, attemptClass: "TypeSafeError", retried: 1},
		},
		"malformed output after the corrective retries": {
			outcome:    fake.Text("this is not JSON"),
			perAttempt: 1,
			want:       classWant{errType: "*decision.ResponseValidationError", status: 200, report: true, retried: 1},
		},
		"closed Adapter": {
			outcome: fake.Text(noulAnswer),
			closed:  true,
			want:    classWant{errType: "*decision.ConnectionError", adapterKind: KindClosed, is: ErrClosed, retried: 3},
		},
	}
	return cases
}

// TestFailureClasses checks, through the SDK, every way a call fails: the
// error type the caller gets, its status, kind, message and error type, the
// Adapter's *Error behind a timeout, a connection failure or a closed
// Adapter, the Report and the class its attempt records, and the number of
// attempts, one on a client from NewClient and, on a client with the SDK's
// DefaultRetry, one for every failure upstream does not retry (DV11). The
// statuses 400, 401, 403, 429, 500 and 418 with upstream's body are those of
// tests/utils/test_error_handling.py::test_status_errors_map_and_preserve_status_and_body;
// a provider status outside 400 to 599 is answered 424.
func TestFailureClasses(t *testing.T) {
	for name, tt := range classCases() {
		t.Run(name, func(t *testing.T) {
			for _, own := range []bool{false, true} {
				synctest.Test(t, func(t *testing.T) {
					build := tt.build
					if build == nil {
						build = func(t testing.TB) (*Adapter, func() int) {
							p := fake.New(tt.outcome)
							return fakeAdapter(t, p), p.Calls
						}
					}
					ad, calls := build(t)
					var c *decision.Client
					if own {
						c = sdkClient(t, ad, true, decision.WithRetry(decision.DefaultRetry()), decision.WithNoTimeout())
					} else {
						c = sdkClient(t, ad, false)
					}
					if tt.closed {
						if err := ad.Close(); err != nil {
							t.Fatalf("Close: %v", err)
						}
					}
					questions := noulQuestions(t)
					if tt.questions != nil {
						questions = tt.questions(t)
					}
					state := tt.state
					if state == nil {
						state = "state"
					}
					_, err := c.SystemOne(t.Context(), state, questions, tt.call...)
					wantAttempts := uint64(1)
					if own {
						wantAttempts = tt.want.retried
					}
					checkClass(t, err, tt.want)
					if got, want := c.Stats().Attempts, wantAttempts; got != want {
						t.Errorf("own client %v: Stats().Attempts = %d, want %d", own, got, want)
					}
					if got, want := calls(), tt.perAttempt*int(wantAttempts); got != want {
						t.Errorf("own client %v: provider requests = %d, want %d", own, got, want)
					}
					if r, ok := ReportFromError(err); ok && own && wantAttempts > 1 && r.Debug.SDKRetryCount != int(wantAttempts)-1 {
						t.Errorf("SDKRetryCount = %d, want %d", r.Debug.SDKRetryCount, wantAttempts-1)
					}
				})
			}
		})
	}
}

// checkClass checks err against want.
func checkClass(t testing.TB, err error, want classWant) {
	t.Helper()
	if err == nil {
		t.Fatal("SystemOne succeeded")
	}
	if got := fmt.Sprintf("%T", err); got != want.errType {
		t.Errorf("error type %s (%v), want %s", got, err, want.errType)
	}
	if apiErr, ok := errors.AsType[*decision.APIError](err); ok {
		got := [4]any{apiErr.StatusCode, apiErr.Kind.String(), apiErr.Message, apiErr.ErrorType}
		if diff := gocmp.Diff([4]any{want.status, want.kind.String(), want.message, want.errorType}, got); diff != "" {
			t.Errorf("APIError status, kind, message, error type (-want +got):\n%s", diff)
		}
	}
	if invalid, ok := errors.AsType[*decision.ResponseValidationError](err); ok {
		if invalid.StatusCode != want.status || invalid.FieldPath != "answers" {
			t.Errorf("ResponseValidationError status %d field %q, want %d \"answers\"", invalid.StatusCode, invalid.FieldPath, want.status)
		}
	}
	ae, ok := errors.AsType[*Error](err)
	switch {
	case want.adapterKind == 0 && ok:
		t.Errorf("errors.As reached an *Error %v", ae)
	case want.adapterKind != 0 && !ok:
		t.Errorf("errors.As did not reach an *Error in %v", err)
	case ok && ae.Kind != want.adapterKind:
		t.Errorf("Error kind %v, want %v", ae.Kind, want.adapterKind)
	case ok && want.adapterKind != KindClosed && (ae.Provider == "" || ae.Model == ""):
		t.Errorf("Error %+v lacks its provider or model", *ae)
	}
	if want.is != nil && !errors.Is(err, want.is) {
		t.Errorf("errors.Is(%v, %v) = false", err, want.is)
	}
	r, ok := ReportFromError(err)
	if ok != want.report {
		t.Fatalf("ReportFromError ok = %v, want %v", ok, want.report)
	}
	if !ok {
		return
	}
	if len(r.Debug.Attempts) == 0 {
		t.Fatal("the Report has no attempt")
	}
	if got := r.Debug.Attempts[0].Info.ErrorType; got != want.attemptClass {
		t.Errorf("attempt error_type %q, want %q", got, want.attemptClass)
	}
}

// TestClassificationOrder checks the order in which a failed evaluation is
// classified: the request's context first (a cancelled call returns
// ctx.Err() itself, a call whose deadline passed returns the timeout Error
// with the Report, whatever the provider returned), then output that does
// not match the schema, then the provider's typed errors, then any other
// error. A cancellation is read from the context, never from the error: a
// connection failure that wraps context.Canceled while the context is live
// is a connection failure. An attempt records its own error whatever the
// call's class.
func TestClassificationOrder(t *testing.T) {
	type want struct {
		ctxErr       bool
		kind         ErrorKind
		status       int
		errorType    string
		message      string
		attemptClass string
	}
	tests := map[string]struct {
		ctx      string // "live", "cancel" or "deadline"
		provider func(ctx context.Context, cancel context.CancelFunc) (*llm.Result, error)
		want     want
	}{
		"cancelled while the provider returns a typed timeout": {
			ctx: "cancel",
			provider: func(_ context.Context, cancel context.CancelFunc) (*llm.Result, error) {
				cancel()
				return nil, &llm.TimeoutError{}
			},
			want: want{ctxErr: true},
		},
		"deadline passed while the provider returns 503": {
			ctx: "deadline",
			provider: func(ctx context.Context, _ context.CancelFunc) (*llm.Result, error) {
				<-ctx.Done()
				return nil, &llm.StatusError{StatusCode: 503}
			},
			want: want{kind: KindTimeout, attemptClass: "TypeSafeInternalServerError"},
		},
		"deadline passed while the provider returns the context's error": {
			ctx: "deadline",
			provider: func(ctx context.Context, _ context.CancelFunc) (*llm.Result, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			want: want{kind: KindTimeout, attemptClass: "TypeSafeAPITimeoutError"},
		},
		"deadline passed while the output does not match the schema": {
			ctx: "deadline",
			provider: func(ctx context.Context, _ context.CancelFunc) (*llm.Result, error) {
				<-ctx.Done()
				return textResult("not JSON"), nil
			},
			want: want{kind: KindTimeout},
		},
		"live: output that does not match the schema": {
			ctx:      "live",
			provider: func(context.Context, context.CancelFunc) (*llm.Result, error) { return textResult("not JSON"), nil },
			want:     want{status: 200},
		},
		"live: a typed timeout": {
			ctx:      "live",
			provider: func(context.Context, context.CancelFunc) (*llm.Result, error) { return nil, &llm.TimeoutError{} },
			want:     want{kind: KindTimeout, attemptClass: "TypeSafeAPITimeoutError"},
		},
		"live: a connection failure wrapping context.Canceled": {
			ctx: "live",
			provider: func(context.Context, context.CancelFunc) (*llm.Result, error) {
				return nil, &llm.ConnectionError{Err: context.Canceled}
			},
			want: want{kind: KindConnection, attemptClass: "TypeSafeAPIConnectionError"},
		},
		"live: a status": {
			ctx: "live",
			provider: func(context.Context, context.CancelFunc) (*llm.Result, error) {
				return nil, &llm.StatusError{StatusCode: 503}
			},
			want: want{status: 503, errorType: "provider_status", message: "503 status code (no body)", attemptClass: "TypeSafeInternalServerError"},
		},
		"live: a status wrapped by the provider": {
			ctx: "live",
			provider: func(context.Context, context.CancelFunc) (*llm.Result, error) {
				return nil, fmt.Errorf("vendor: %w", &llm.StatusError{StatusCode: 429, Body: []byte(`{"message":"slow down"}`)})
			},
			want: want{status: 429, errorType: "provider_status", message: "429 slow down", attemptClass: "TypeSafeRateLimitError"},
		},
		"live: a non-answer": {
			ctx: "live",
			provider: func(context.Context, context.CancelFunc) (*llm.Result, error) {
				return nil, &llm.NonAnswerError{Message: "refused"}
			},
			want: want{status: 424, errorType: "non_answer", message: "refused", attemptClass: "TypeSafeError"},
		},
		"live: an error of the provider's own": {
			ctx: "live",
			provider: func(context.Context, context.CancelFunc) (*llm.Result, error) {
				return nil, &customError{text: "own failure"}
			},
			want: want{status: 424, errorType: "provider_error", message: "own failure", attemptClass: "TypeSafeError"},
		},
		"live: a bare context.DeadlineExceeded": {
			ctx:      "live",
			provider: func(context.Context, context.CancelFunc) (*llm.Result, error) { return nil, context.DeadlineExceeded },
			want:     want{status: 424, errorType: "provider_error", message: "context deadline exceeded", attemptClass: "TypeSafeError"},
		},
		"live: neither a result nor an error": {
			ctx:      "live",
			provider: func(context.Context, context.CancelFunc) (*llm.Result, error) { return nil, nil },
			want:     want{status: 424, errorType: "provider_error", message: "adapter: the provider returned no result and no error", attemptClass: "TypeSafeError"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if tt.ctx == "deadline" {
					ctx, cancel = context.WithTimeout(t.Context(), time.Second)
					defer cancel()
				}
				p := &funcProvider{do: func(pctx context.Context, _ *llm.Request) (*llm.Result, error) { return tt.provider(pctx, cancel) }}
				ad := fakeAdapter(t, p)
				status, body, err := send(t, ad, rawRequest(ctx, http.MethodPost, systemOnePath, noulBody))
				switch {
				case tt.want.ctxErr:
					if err == nil || err != ctx.Err() { //nolint:errorlint // RoundTrip returns the context's error itself, not a wrap of it.
						t.Fatalf("RoundTrip error = %v, want the context's own error %v", err, ctx.Err())
					}
					return
				case tt.want.kind != 0:
					ae, ok := errors.AsType[*Error](err)
					if !ok || ae.Kind != tt.want.kind || ae.Report == nil || ae.Provider != "fake" || ae.Model != "func-model" {
						t.Fatalf("RoundTrip error = %#v, want an Error of kind %v with the Report", err, tt.want.kind)
					}
					if tt.want.kind == KindConnection && !errors.Is(ae.Unwrap(), context.Canceled) {
						t.Errorf("connection Error cause = %v, want context.Canceled kept as its sentinel", ae.Unwrap())
					}
					if tt.want.attemptClass != "" || len(ae.Report.Debug.Attempts) > 0 {
						if got := ae.Report.Debug.Attempts[0].Info.ErrorType; got != tt.want.attemptClass {
							t.Errorf("attempt error_type %q, want %q", got, tt.want.attemptClass)
						}
					}
					return
				}
				if err != nil {
					t.Fatalf("RoundTrip error = %v", err)
				}
				if status != tt.want.status {
					t.Fatalf("status %d, want %d: %s", status, tt.want.status, body)
				}
				var r Report
				if err := r.UnmarshalJSON(body); err != nil {
					t.Fatalf("the body holds no Report: %v", err)
				}
				if tt.want.errorType != "" {
					message, errorType := detailOf(t, body)
					if message != tt.want.message || errorType != tt.want.errorType {
						t.Errorf("detail = %q, %q; want %q, %q", message, errorType, tt.want.message, tt.want.errorType)
					}
				}
				if got := r.Debug.Attempts[0].Info.ErrorType; got != tt.want.attemptClass {
					t.Errorf("attempt error_type %q, want %q", got, tt.want.attemptClass)
				}
			})
		})
	}
}

// TestDeadlineThroughTheSDK checks the two deadlines of a caller's own
// client with the SDK's DefaultRetry, one at a time: the caller's context
// deadline under WithNoTimeout, and the SDK's per-attempt WithTimeout
// without a caller deadline. Either gives a *decision.TimeoutError through
// which errors.As reaches the Adapter's timeout Error and ReportFromError
// its Report, and the SDK makes as many attempts as for a RoundTripper that
// returns a bare context.DeadlineExceeded.
func TestDeadlineThroughTheSDK(t *testing.T) {
	tests := map[string]struct {
		callDeadline time.Duration
		opts         []decision.ClientOption
		wantTimeout  time.Duration
	}{
		"the caller's context deadline": {
			callDeadline: time.Second,
			opts:         []decision.ClientOption{decision.WithNoTimeout()},
		},
		"the SDK's per-attempt deadline": {
			opts:        []decision.ClientOption{decision.WithTimeout(time.Second)},
			wantTimeout: time.Second,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			call := func(t *testing.T, rt http.RoundTripper, ad *Adapter) (uint64, error) {
				ctx := t.Context()
				if tt.callDeadline > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tt.callDeadline)
					defer cancel()
				}
				var c *decision.Client
				if ad != nil {
					c = sdkClient(t, ad, true, append([]decision.ClientOption{decision.WithRetry(decision.DefaultRetry())}, tt.opts...)...)
				} else {
					var err error
					c, err = decision.NewClient(append([]decision.ClientOption{decision.WithAPIKey(ownClientKey), decision.WithRoundTripper(rt), decision.WithBaseURL(ownClientBaseURL), decision.WithModel("fake"), decision.WithRetry(decision.DefaultRetry())}, tt.opts...)...)
					if err != nil {
						t.Fatalf("NewClient: %v", err)
					}
					defer c.Close()
				}
				_, err := c.SystemOne(ctx, "state", noulQuestions(t))
				return c.Stats().Attempts, err
			}
			var bareAttempts uint64
			synctest.Test(t, func(t *testing.T) {
				bare := rtFunc(func(req *http.Request) (*http.Response, error) {
					<-req.Context().Done()
					return nil, context.DeadlineExceeded
				})
				bareAttempts, _ = call(t, bare, nil)
			})
			synctest.Test(t, func(t *testing.T) {
				p := &funcProvider{do: func(ctx context.Context, _ *llm.Request) (*llm.Result, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				}}
				attempts, err := call(t, nil, fakeAdapter(t, p))
				te, ok := errors.AsType[*decision.TimeoutError](err)
				if !ok || te.Timeout != tt.wantTimeout {
					t.Fatalf("SystemOne error = %v, want a *decision.TimeoutError with Timeout %v", err, tt.wantTimeout)
				}
				ae, ok := errors.AsType[*Error](err)
				if !ok || ae.Kind != KindTimeout {
					t.Fatalf("errors.As did not reach the timeout Error in %v", err)
				}
				r, ok := ReportFromError(err)
				if !ok || r != ae.Report || len(r.Debug.Attempts) != 1 {
					t.Fatalf("ReportFromError = %v, %v; want the Error's Report with one attempt", r, ok)
				}
				if attempts != bareAttempts {
					t.Errorf("Stats().Attempts = %d, a bare context.DeadlineExceeded gives %d", attempts, bareAttempts)
				}
				if r.Debug.SDKRetryCount != int(attempts)-1 { //nolint:gosec // G115: an attempt count of at most three.
					t.Errorf("SDKRetryCount = %d after %d attempts", r.Debug.SDKRetryCount, attempts)
				}
			})
		})
	}
}

// TestRefusalOrder checks the order of the refusals before an evaluation,
// one row per pair of adjacent steps, each body failing both and answered
// as the earlier step says: the closed Adapter; the route; the body; the
// model and the provider; the provider's construction; the state, then the
// questions; the call's retry policy; then the state's depth, where
// upstream writes the state into the prompt
// (src/system_one_adapter/_client.py:91, called from :444 after the
// questions are validated at :433). The provider
// is built before the questions are checked, as upstream builds it before
// it refuses an empty question set
// (tests/test_provider_lifecycle.py::test_exceptional_exit_closes_owned_sdks,
// case validation).
func TestRefusalOrder(t *testing.T) {
	deep := strings.Repeat("[", 300) + strings.Repeat("]", 300)
	badPolicy := DefaultRetry().MaxRetries(-1)
	tests := map[string]struct {
		closed     bool
		noDefault  bool
		failBuild  bool
		method     string
		path       string
		body       string
		policy     *RetryPolicy
		wantClosed bool
		wantStatus int
		wantType   string
		wantBuilds int
	}{
		"closed before the route": {
			closed: true, method: http.MethodGet, path: "/v1/unknown",
			wantClosed: true,
		},
		"the route before the body": {
			method: http.MethodPut, path: systemOnePath, body: "not JSON",
			wantStatus: 404,
		},
		"the body before the model": {
			noDefault: true, method: http.MethodPost, path: systemOnePath,
			body:       `{"state":"s","questions":null}`,
			wantStatus: 400, wantType: "invalid_body",
		},
		"the model before the provider's construction": {
			noDefault: true, failBuild: true, method: http.MethodPost, path: systemOnePath,
			body:       `{"state":"s","model":"gpt-4o-mini","questions":{"a":{"type":"noul"}}}`,
			wantStatus: 400, wantType: "provider_required",
		},
		"the provider's construction before the state": {
			failBuild: true, method: http.MethodPost, path: systemOnePath,
			body:       `{"state":null,"model":"openai:gpt-x","questions":{"a":{"type":"noul"}}}`,
			wantStatus: 400, wantType: "provider_config", wantBuilds: 1,
		},
		"the state before the questions": {
			method: http.MethodPost, path: systemOnePath,
			body:       `{"state":null,"model":"openai:gpt-x","questions":{}}`,
			wantStatus: 422, wantType: "invalid_state", wantBuilds: 1,
		},
		"the provider's construction before the questions": {
			method: http.MethodPost, path: systemOnePath,
			body:       `{"state":"s","model":"openai:gpt-x","questions":{}}`,
			wantStatus: 422, wantType: "invalid_questions", wantBuilds: 1,
		},
		"the questions before the call's retry policy": {
			method: http.MethodPost, path: systemOnePath, policy: &badPolicy,
			body:       `{"state":"s","model":"openai:gpt-x","questions":{}}`,
			wantStatus: 422, wantType: "invalid_questions", wantBuilds: 1,
		},
		"the provider's construction before the state's depth": {
			failBuild: true, method: http.MethodPost, path: systemOnePath,
			body:       `{"state":` + deep + `,"model":"openai:gpt-x","questions":{"a":{"type":"noul"}}}`,
			wantStatus: 400, wantType: "provider_config", wantBuilds: 1,
		},
		"the questions before the state's depth": {
			method: http.MethodPost, path: systemOnePath,
			body:       `{"state":` + deep + `,"model":"openai:gpt-x","questions":{"a":{"type":"riddle"}}}`,
			wantStatus: 422, wantType: "invalid_questions", wantBuilds: 1,
		},
		"the call's retry policy before the state's depth": {
			method: http.MethodPost, path: systemOnePath, policy: &badPolicy,
			body:       `{"state":` + deep + `,"model":"openai:gpt-x","questions":{"a":{"type":"noul"}}}`,
			wantStatus: 400, wantType: "invalid_retry", wantBuilds: 1,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var log factoryLog
			log.steps = []fake.Outcome{fake.Text(noulAnswer)}
			if tt.failBuild {
				log.fail = errors.New("constructor failed")
			}
			opts := []Option{WithFactory("openai", log.factory("openai"))}
			if !tt.noDefault {
				opts = append(opts, WithDefaultModel("openai:gpt-x"))
			}
			ad, err := New(Probabilities, Structured, opts...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if tt.closed {
				if err := ad.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
			}
			ctx := t.Context()
			if tt.policy != nil {
				ctx = ContextWithRetry(ctx, *tt.policy)
			}
			status, body, err := send(t, ad, rawRequest(ctx, tt.method, tt.path, tt.body))
			if tt.wantClosed {
				if ae, ok := errors.AsType[*Error](err); !ok || ae.Kind != KindClosed {
					t.Fatalf("RoundTrip = %d %s, %v; want the closed Error", status, body, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("RoundTrip error = %v", err)
			}
			if status != tt.wantStatus {
				t.Fatalf("status %d, want %d: %s", status, tt.wantStatus, body)
			}
			if tt.wantType != "" {
				if _, errorType := detailOf(t, body); errorType != tt.wantType {
					t.Errorf("error_type %q, want %q: %s", errorType, tt.wantType, body)
				}
			}
			if got := len(log.snapshot()["openai"]); got != tt.wantBuilds {
				t.Errorf("provider builds = %d, want %d", got, tt.wantBuilds)
			}
		})
	}
}

// TestInvalidStateIsRejected checks the state rules: a string, an object or
// an array is evaluated; null or an absent state is refused with upstream's
// text (src/system_one_adapter/_client.py:431-432); another kind is refused;
// a state nested deeper than the prompt writer allows (255 levels, a scalar
// counted as one) is refused with 422 before the evaluation, ending in a
// string, a literal, a number or an empty array; and a body that opens more
// than 10000 arrays and objects, a state opening 10000 inside it, is not a
// JSON body the Adapter reads.
func TestInvalidStateIsRejected(t *testing.T) {
	nest := func(containers int, inner string) string {
		return strings.Repeat("[", containers) + inner + strings.Repeat("]", containers)
	}
	const questions = `"questions":{"answer":{"type":"noul"}}`
	tests := map[string]struct {
		state       string // the state member's JSON; empty for none
		wantStatus  int
		wantType    string
		wantMessage string
	}{
		"a string":                          {state: `"text"`, wantStatus: 200},
		"an object":                         {state: `{"a":1}`, wantStatus: 200},
		"an array":                          {state: `[1,2]`, wantStatus: 200},
		"null":                              {state: `null`, wantStatus: 422, wantType: "invalid_state", wantMessage: "State must not be None."},
		"absent":                            {wantStatus: 422, wantType: "invalid_state", wantMessage: "State must not be None."},
		"a number":                          {state: `5`, wantStatus: 422, wantType: "invalid_state", wantMessage: "State must be a string, an object or an array."},
		"true":                              {state: `true`, wantStatus: 422, wantType: "invalid_state", wantMessage: "State must be a string, an object or an array."},
		"255 levels":                        {state: nest(255, ""), wantStatus: 200},
		"254 arrays around a string":        {state: nest(254, `"s"`), wantStatus: 200},
		"254 arrays around a literal":       {state: nest(254, `null`), wantStatus: 200},
		"256 levels":                        {state: nest(256, ""), wantStatus: 422, wantType: "invalid_state", wantMessage: "State is nested too deeply to be written into the prompt."},
		"255 arrays around a string":        {state: nest(255, `"s"`), wantStatus: 422, wantType: "invalid_state", wantMessage: "State is nested too deeply to be written into the prompt."},
		"255 arrays around a literal":       {state: nest(255, `false`), wantStatus: 422, wantType: "invalid_state", wantMessage: "State is nested too deeply to be written into the prompt."},
		"255 arrays around a number":        {state: nest(255, `1.5`), wantStatus: 422, wantType: "invalid_state", wantMessage: "State is nested too deeply to be written into the prompt."},
		"9999 levels":                       {state: nest(9999, ""), wantStatus: 422, wantType: "invalid_state", wantMessage: "State is nested too deeply to be written into the prompt."},
		"10000 levels: the body is refused": {state: nest(10000, ""), wantStatus: 400, wantType: "invalid_body", wantMessage: "The request body is not a JSON object."},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := fake.New(fake.Text(noulAnswer))
			ad := fakeAdapter(t, p)
			body := `{"model":"fake",` + questions + `}`
			if tt.state != "" {
				body = `{"state":` + tt.state + `,"model":"fake",` + questions + `}`
			}
			status, got, err := send(t, ad, rawRequest(t.Context(), http.MethodPost, systemOnePath, body))
			if err != nil {
				t.Fatalf("RoundTrip error = %v", err)
			}
			if status != tt.wantStatus {
				t.Fatalf("status %d, want %d: %.200s", status, tt.wantStatus, got)
			}
			wantCalls := 1
			if tt.wantType != "" {
				wantCalls = 0
				message, errorType := detailOf(t, got)
				if message != tt.wantMessage || errorType != tt.wantType {
					t.Errorf("detail = %q, %q; want %q, %q", message, errorType, tt.wantMessage, tt.wantType)
				}
				if _, ok := reportFromBody(got); ok {
					t.Errorf("a refusal before the evaluation carries a Report: %.200s", got)
				}
			}
			if p.Calls() != wantCalls {
				t.Errorf("provider calls = %d, want %d", p.Calls(), wantCalls)
			}
		})
	}
}

// TestInvalidRetryPolicy checks that a call whose ContextWithRetry policy
// has a setting out of range is refused with 400 and the error type
// invalid_retry, the policy's own message, no provider request and no
// Report. Upstream refuses such a policy when it is built and so never
// meets it in a call; New refuses it as WithRetry (TestNewRefuses).
func TestInvalidRetryPolicy(t *testing.T) {
	tests := map[string]struct {
		policy RetryPolicy
		want   string
	}{
		"max retries":     {policy: DefaultRetry().MaxRetries(-1), want: "max_retries must be a non-negative integer."},
		"initial backoff": {policy: DefaultRetry().Backoff(-time.Second, time.Second, 0), want: "backoff_initial must be a non-negative, finite number of seconds."},
		"maximum backoff": {policy: DefaultRetry().Backoff(time.Second, -time.Second, 0), want: "backoff_max must be a non-negative, finite number of seconds."},
		"jitter":          {policy: DefaultRetry().Backoff(time.Second, time.Second, math.NaN()), want: "backoff_jitter must be between zero and one."},
		"budget":          {policy: DefaultRetry().Budget(-time.Second), want: "timeout must be a positive, finite number of seconds."},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := fake.New(fake.Text(noulAnswer))
			c := sdkClient(t, fakeAdapter(t, p), false)
			_, err := c.SystemOne(ContextWithRetry(t.Context(), tt.policy), "state", noulQuestions(t))
			apiErr, ok := errors.AsType[*decision.APIError](err)
			if !ok {
				t.Fatalf("SystemOne error = %v, want an *APIError", err)
			}
			got := [3]any{apiErr.StatusCode, apiErr.ErrorType, apiErr.Message}
			if diff := gocmp.Diff([3]any{400, "invalid_retry", tt.want}, got); diff != "" {
				t.Errorf("status, error type, message (-want +got):\n%s", diff)
			}
			if p.Calls() != 0 {
				t.Errorf("provider calls = %d, want 0", p.Calls())
			}
			if _, ok := ReportFromError(err); ok {
				t.Error("ReportFromError found a Report")
			}
		})
	}
}

// TestUnencodableBodyAnswers424 checks that a response body the Adapter
// cannot write, or that does not read back as JSON, is replaced by the 424
// adapter_internal body, so that the SDK never receives a body it cannot
// decode.
func TestUnencodableBodyAnswers424(t *testing.T) {
	tests := map[string]writer{
		"the writer fails":           func() (jsonx.Value, error) { return jsonx.Value{}, errors.New("cannot") },
		"a float that is not finite": func() (jsonx.Value, error) { return jsonx.Float(math.NaN(), jsonx.Repr), nil },
		"a body nested too deeply to write": func() (jsonx.Value, error) {
			return jsonx.Raw([]byte(strings.Repeat("[", 10001) + strings.Repeat("]", 10001))), nil
		},
		"a body that does not read back": func() (jsonx.Value, error) {
			return jsonx.Object(jsonx.Member{Name: "n", Value: jsonx.Number(strings.Repeat("1", 4301))}), nil
		},
	}
	for name, w := range tests {
		t.Run(name, func(t *testing.T) {
			r := bodyReply(http.StatusOK, w)
			if r.status != 424 || string(r.body) != `{"detail":{"message":"adapter: response encoding failed","error_type":"adapter_internal"}}` {
				t.Fatalf("bodyReply = %d %.200s", r.status, r.body)
			}
		})
	}
}

// TestErrorChainSurvivesTheSDK checks the route of the Report for a
// provider timeout and connection failure: the SDK wraps the Adapter's
// Error in a *decision.TimeoutError or *decision.ConnectionError and
// errors.As reaches it, also when the client's key, the provider's messages,
// a provider URL's query and a transport error's text all hold one canary,
// because no link of the chain prints any of them under Error, %+v or %#v.
// Under the SDK's DefaultRetry the caller's error is the third attempt's,
// whose Report records two earlier SDK attempts. An error type that prints
// the key and has no Format method is replaced by the SDK, so errors.As no
// longer reaches it.
func TestErrorChainSurvivesTheSDK(t *testing.T) {
	tests := map[string]struct {
		err      func(canary string) error
		wantType string
		wantKind ErrorKind
	}{
		"timeout": {
			err: func(canary string) error {
				return &llm.TimeoutError{Err: fmt.Errorf("Post %q: %w", "https://provider.invalid/v1?key="+canary, context.DeadlineExceeded)}
			},
			wantType: "*decision.TimeoutError",
			wantKind: KindTimeout,
		},
		"connection failure": {
			err: func(canary string) error {
				return &llm.ConnectionError{Err: fmt.Errorf("dial https://provider.invalid/v1?key=%s: %w", canary, syscall.ECONNREFUSED)}
			},
			wantType: "*decision.ConnectionError",
			wantKind: KindConnection,
		},
	}
	for name, tt := range tests {
		for _, retried := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, SDK retries %v", name, retried), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					canary := canaryKey(t)
					p := &funcProvider{do: func(_ context.Context, req *llm.Request) (*llm.Result, error) {
						req.Trace.RecordRequest("responses", []byte(`{"url":"https://provider.invalid/v1?key=`+canary+`"}`))
						return nil, tt.err(canary)
					}}
					policy := decision.NoRetry()
					if retried {
						policy = decision.DefaultRetry()
					}
					c := sdkClient(t, fakeAdapter(t, p), true, decision.WithAPIKey(canary), decision.WithRetry(policy), decision.WithNoTimeout())
					_, err := c.SystemOne(t.Context(), "state "+canary, noulQuestions(t))
					if got := fmt.Sprintf("%T", err); got != tt.wantType {
						t.Fatalf("SystemOne error %s %v, want %s", got, err, tt.wantType)
					}
					ae, ok := errors.AsType[*Error](err)
					if !ok || ae.Kind != tt.wantKind || ae.Report == nil {
						t.Fatalf("errors.As did not reach the Adapter's Error in %v", err)
					}
					for e := err; e != nil; e = errors.Unwrap(e) {
						for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
							if s := fmt.Sprintf(verb, e); strings.Contains(s, canary) {
								t.Errorf("%s of a link %T prints the canary: %s", verb, e, s)
							}
						}
					}
					r, ok := ReportFromError(err)
					want := 0
					if retried {
						want = 2
					}
					if !ok || r.Debug.SDKRetryCount != want {
						t.Errorf("ReportFromError = %v, %v; want SDKRetryCount %d", r, ok, want)
					}
				})
			})
		}
	}
	t.Run("an error printing the key without Format is replaced", func(t *testing.T) {
		key := canaryKey(t)
		rt := rtFunc(func(*http.Request) (*http.Response, error) { return nil, &noKeyFormatError{key: key} })
		c, err := decision.NewClient(decision.WithAPIKey(key), decision.WithRoundTripper(rt), decision.WithBaseURL(ownClientBaseURL), decision.WithRetry(decision.NoRetry()), decision.WithModel("fake"))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		_, err = c.SystemOne(t.Context(), "state", noulQuestions(t))
		if _, ok := errors.AsType[*noKeyFormatError](err); ok {
			t.Errorf("errors.As reached an error that prints the key: %v", err)
		}
		e := &Error{Kind: KindTimeout, Provider: "p", Model: key}
		if s := fmt.Sprintf("%#v", e); !strings.Contains(s, key) {
			t.Errorf("the Error's own %%#v hides its fields: %s", s)
		}
	})
}

// TestReportFromErrorEveryClass checks that ReportFromError returns the
// Report for every failure class whose evaluation started, and none for a
// refusal before it, on a client whose key is a canary that the providers'
// messages and bodies also hold: the Report travels in the error body,
// which the SDK keeps whatever its text, or in the Adapter's Error.
func TestReportFromErrorEveryClass(t *testing.T) {
	for name, tt := range classCases() {
		t.Run(name, func(t *testing.T) {
			canary := canaryKey(t)
			build := tt.build
			if build == nil {
				outcome := tt.outcome
				switch name {
				case "non_answer":
					outcome = fake.Error(&llm.NonAnswerError{Message: "refused " + canary})
				case "provider status 400", "provider status 503":
					outcome = fake.Error(&llm.StatusError{StatusCode: 400, Body: []byte(`{"error":{"message":"` + canary + `"}}`)})
				}
				build = func(t testing.TB) (*Adapter, func() int) {
					p := fake.New(outcome)
					return fakeAdapter(t, p), p.Calls
				}
			}
			ad, _ := build(t)
			if tt.closed {
				_ = ad.Close()
			}
			c := sdkClient(t, ad, true, decision.WithAPIKey(canary), decision.WithRetry(decision.NoRetry()), decision.WithNoTimeout())
			questions := noulQuestions(t)
			if tt.questions != nil {
				questions = tt.questions(t)
			}
			state := tt.state
			if state == nil {
				state = "state"
			}
			_, err := c.SystemOne(t.Context(), state, questions, tt.call...)
			r, ok := ReportFromError(err)
			if ok != tt.want.report {
				t.Fatalf("ReportFromError ok = %v, want %v (%v)", ok, tt.want.report, err)
			}
			if ok && len(r.Debug.Attempts) == 0 {
				t.Error("the Report has no attempt")
			}
		})
	}
}

// TestCancelledCallReturnsContextError checks that a call whose context is
// cancelled while the provider works returns the context's error itself,
// without a Report and without another attempt, on a client from NewClient
// and on one with the SDK's DefaultRetry (DV2).
func TestCancelledCallReturnsContextError(t *testing.T) {
	for _, own := range []bool{false, true} {
		t.Run(fmt.Sprintf("own client %v", own), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				p := &funcProvider{do: func(pctx context.Context, _ *llm.Request) (*llm.Result, error) {
					cancel()
					<-pctx.Done()
					return nil, &llm.TimeoutError{Err: pctx.Err()}
				}}
				var opts []decision.ClientOption
				if own {
					opts = []decision.ClientOption{decision.WithRetry(decision.DefaultRetry())}
				}
				c := sdkClient(t, fakeAdapter(t, p), own, opts...)
				_, err := c.SystemOne(ctx, "state", noulQuestions(t))
				if err != context.Canceled { //nolint:errorlint // the call returns the context's error itself.
					t.Fatalf("SystemOne error = %T %v, want context.Canceled itself", err, err)
				}
				if _, ok := ReportFromError(err); ok {
					t.Error("a cancelled call carries a Report")
				}
				if c.Stats().Attempts != 1 {
					t.Errorf("attempts %d, want 1", c.Stats().Attempts)
				}
			})
		})
	}
}

// TestProviderConfigFailure checks that a provider that cannot be built
// fails the call with 400 provider_config and the factory's text instead
// of raising (DV12), without a Report, and that the failure is not cached:
// the next call builds again and is answered once the factory succeeds.
func TestProviderConfigFailure(t *testing.T) {
	var log factoryLog
	log.steps = []fake.Outcome{fake.Text(noulAnswer)}
	log.fail = errors.New("OPENAI_API_KEY is not set")
	ad := newAdapter(t, WithFactory("openai", log.factory("openai")), WithFactory("nil", func(string) (llm.Provider, error) { return nil, nil }), WithDefaultModel("openai:gpt-x"))
	c := sdkClient(t, ad, false)
	_, err := c.SystemOne(t.Context(), "state", noulQuestions(t))
	status, errorType, message, report := apiErrorParts(err)
	if status != 400 || errorType != "provider_config" || message != "OPENAI_API_KEY is not set" || report {
		t.Fatalf("first call: %d %q %q report %v", status, errorType, message, report)
	}
	if _, err := c.SystemOne(t.Context(), "state", noulQuestions(t)); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := log.snapshot()["openai"]; len(got) != 2 {
		t.Errorf("builds %q, want two", got)
	}
	_, err = c.SystemOne(t.Context(), "state", noulQuestions(t), decision.Model("nil:m"))
	status, errorType, message, _ = apiErrorParts(err)
	if status != 400 || errorType != "provider_config" || message != `adapter: the factory "nil" returned no provider for model "m"` {
		t.Errorf("a factory returning no provider: %d %q %q", status, errorType, message)
	}
}
