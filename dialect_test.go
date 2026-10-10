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

package decision

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
)

// funcDialect is a Dialect whose Request is a func, for the tests to vary.
type funcDialect func(body []byte, questions []PreparedQuestion) ([]byte, ResponseFunc, error)

// Request implements Dialect.
func (f funcDialect) Request(body []byte, questions []PreparedQuestion) ([]byte, ResponseFunc, error) {
	return f(body, questions)
}

// dialectBody is a System One answer to noulQuestion's set.
const dialectBody = `{"model":"m","usage":{"input_tokens":1,"output_tokens":2},"answers":{"q":{"type":"noul","noul":0.25}}}`

// dialectClient returns a client of a stand-in provider whose dialect is d,
// sending through rec, with no retry unless opts set one.
func dialectClient(t *testing.T, d Dialect, rec *testsupport.Recorder, opts ...ClientOption) *Client {
	t.Helper()
	clearEnv(t)
	p := Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
		Name:          "Stand-in",
		BaseURL:       "https://vendor.example.test",
		SystemOnePath: "/v1/decide",
		APIKeyEnv:     "STAND_IN_API_KEY",
		DefaultModel:  "m",
		Dialect:       d,
	}
	base := []ClientOption{WithProvider(p), WithAPIKey("stand-in-key-0123456789"), WithRoundTripper(rec), WithRetry(NoRetry())}
	c, err := NewClient(append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// systemOneBodyOf returns the body a client without a dialect sends for
// state and noulQuestion's set, as the dialect tests' reference.
func systemOneBodyOf(t *testing.T, state any) []byte {
	t.Helper()
	rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, []byte(dialectBody))}}
	c := dialectClient(t, nil, rec)
	if _, err := c.SystemOne(t.Context(), state, noulQuestion(t)); err != nil {
		t.Fatalf("SystemOne without a dialect: %v", err)
	}
	return rec.Requests()[0].Body
}

// TestPreparedQuestions pins the view a dialect reads: every form of
// question with its kind and tables, a raw question marked Raw with no
// tables, and one view shared by every call.
func TestPreparedQuestions(t *testing.T) {
	qs, err := NewQuestions().
		Noul("billing", Noul{Instructions: Text("Billing?")}).
		Choice("tone", Choice{Options: Options{{Label: "calm"}, {Label: "angry"}}}).
		Score("urgency", Score{Levels: []Content{Text("low"), JSON([]byte(`{"when": "now"}`))}}).
		Raw("spam", RawQuestion{Type: "choice", Fields: map[string]any{"criteria": map[string]any{"yes": nil}}}).
		Raw("future", RawQuestion{Type: "ranking", Fields: map[string]any{"criteria": []any{"a"}}}).
		Prepare()
	if err != nil {
		t.Fatal(err)
	}
	type view struct {
		Name    string
		Kind    AnswerKind
		Options []string
		Levels  []string
		Raw     bool
	}
	var got []view
	for _, q := range qs.Questions() {
		v := view{Name: q.Name, Kind: q.Kind, Options: q.Options, Raw: q.Raw}
		for _, l := range q.Levels {
			if l.IsJSON() {
				v.Levels = append(v.Levels, "json:"+string(l.JSON()))
			} else {
				v.Levels = append(v.Levels, "text:"+l.Text())
			}
		}
		got = append(got, v)
	}
	want := []view{
		{Name: "billing", Kind: KindNoul},
		{Name: "tone", Kind: KindChoice, Options: []string{"calm", "angry"}},
		{Name: "urgency", Kind: KindScore, Levels: []string{"text:low", `json:{"when":"now"}`}},
		{Name: "spam", Kind: KindChoice, Raw: true},
		{Name: "future", Raw: true},
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("Questions() (-want +got):\n%s", diff)
	}
	if a, b := qs.Questions(), qs.Questions(); &a[0] != &b[0] {
		t.Error("Questions() built a second view: want one view shared by every call")
	}
	var nilSet *Prepared
	if v := nilSet.Questions(); v != nil {
		t.Errorf("(*Prepared)(nil).Questions() = %v, want nil", v)
	}
}

// TestOverlaps pins the alias check over the forms a dialect could return
// its view of the body in, the view starting at index 0 of the pooled
// buffer's array and capped at its length, as applyDialect hands it: the
// view itself, a slice inside it with or without its capacity cut, the
// view's empty prefix, an append to it, which reallocates, and a fresh
// slice.
func TestOverlaps(t *testing.T) {
	scratch := make([]byte, 16, 32)
	body := slices.Clip(scratch)
	tests := map[string]struct {
		out  []byte
		want bool
	}{
		"success: the body itself":                            {out: body, want: true},
		"success: a slice inside":                             {out: body[3:7], want: true},
		"success: a slice inside with its capacity cut":       {out: body[3:7:9], want: true},
		"success: the body's empty prefix":                    {out: body[:0], want: true},
		"success: the last byte":                              {out: body[15:], want: true},
		"success: append reallocates when the view is capped": {out: append(body, "vendor"...), want: false},
		"success: a fresh slice":                              {out: make([]byte, 16), want: false},
		"success: a copy":                                     {out: bytes.Clone(body), want: false},
		"success: nil":                                        {out: nil, want: false},
		"success: empty with no capacity":                     {out: []byte{}, want: false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := overlaps(tt.out, body); got != tt.want {
				t.Errorf("overlaps(out, body) = %t, want %t", got, tt.want)
			}
		})
	}
	fresh := make([]byte, 16)
	if n := testing.AllocsPerRun(100, func() { _ = overlaps(fresh, body) || overlaps(body[3:7], body) }); n != 0 {
		t.Errorf("overlaps allocates %v times per run, want 0", n)
	}
}

// TestDialectRequest pins what a call sends through a dialect: the body the
// dialect returned, or the SDK's own body when it returned nil or a body
// that shares the SDK body's memory; and that the dialect sees the SDK's
// body and the call's question set.
func TestDialectRequest(t *testing.T) {
	const state = "hello"
	sdkBody := systemOneBodyOf(t, state)
	tests := map[string]struct {
		out      func(body []byte) []byte
		wantSDK  bool   // the SDK's own body is sent
		wantBody string // otherwise this body
	}{
		"success: a body of the dialect's own": {
			out:      func([]byte) []byte { return []byte(`{"vendor":true}`) },
			wantBody: `{"vendor":true}`,
		},
		"success: nil sends the SDK's body": {
			out:     func([]byte) []byte { return nil },
			wantSDK: true,
		},
		"success: an empty body sends the SDK's body, as nil does": {
			out:     func([]byte) []byte { return []byte{} },
			wantSDK: true,
		},
		"success: an append to the body is a body of the dialect's own": {
			out:      func(body []byte) []byte { return append(body, ' ') },
			wantBody: string(sdkBody) + " ",
		},
		"success: the body itself is sent as the SDK's": {
			out:     func(body []byte) []byte { return body },
			wantSDK: true,
		},
		"success: a slice of the body is sent as the SDK's body": {
			out:     func(body []byte) []byte { return body[1:5] },
			wantSDK: true,
		},
		"success: a slice of the body with its capacity cut is sent as the SDK's body": {
			out:     func(body []byte) []byte { return body[1:5:6] },
			wantSDK: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var seenBody []byte
			var seenNames []string
			d := funcDialect(func(body []byte, qs []PreparedQuestion) ([]byte, ResponseFunc, error) {
				seenBody = bytes.Clone(body)
				for _, q := range qs {
					seenNames = append(seenNames, q.Name)
				}
				return tt.out(body), nil, nil
			})
			rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, []byte(dialectBody))}}
			c := dialectClient(t, d, rec)
			resp, err := c.SystemOne(t.Context(), state, noulQuestion(t))
			if err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			if !bytes.Equal(seenBody, sdkBody) {
				t.Errorf("the dialect saw body %s, want the SDK's %s", seenBody, sdkBody)
			}
			if diff := gocmp.Diff([]string{"q"}, seenNames); diff != "" {
				t.Errorf("the dialect's questions (-want +got):\n%s", diff)
			}
			want := []byte(tt.wantBody)
			if tt.wantSDK {
				want = sdkBody
			}
			sent := rec.Requests()[0]
			if !bytes.Equal(sent.Body, want) {
				t.Errorf("sent body %s, want %s", sent.Body, want)
			}
			if sent.ContentLength != int64(len(want)) || !sent.HasGetBody {
				t.Errorf("ContentLength = %d, HasGetBody = %t; want %d, true", sent.ContentLength, sent.HasGetBody, len(want))
			}
			if got := string(resp.Meta().RawBody()); got != dialectBody {
				t.Errorf("RawBody() = %s, want the response as it arrived", got)
			}
		})
	}
}

// TestDialectRefusal pins that a dialect's error stops the call before
// anything is sent, as a *DialectError: the one the dialect returned, or
// one that wraps any other error and names the provider.
func TestDialectRefusal(t *testing.T) {
	cause := errors.New("cannot write \x1b this")
	refusal := NewDialectError("Vendor", "a raw question")
	tests := map[string]struct {
		err      error
		want     string
		wantSame bool
	}{
		"error: a DialectError is returned as it is": {
			err:      refusal,
			want:     "The Vendor dialect cannot send this request: a raw question",
			wantSame: true,
		},
		"error: a wrapped DialectError is found in the chain": {
			err:      fmt.Errorf("wrapped: %w", refusal),
			want:     "The Vendor dialect cannot send this request: a raw question",
			wantSame: true,
		},
		"error: any other error is wrapped, naming the provider, escaped": {
			err:  cause,
			want: `The Stand-in dialect cannot send this request: cannot write \x1b this`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &testsupport.Recorder{Respond: func(testsupport.RecordedRequest) testsupport.Reply {
				t.Error("a request was sent")
				return testsupport.JSON(http.StatusOK, []byte(dialectBody))
			}}
			c := dialectClient(t, funcDialect(func([]byte, []PreparedQuestion) ([]byte, ResponseFunc, error) {
				return nil, nil, tt.err
			}), rec, WithRetry(DefaultRetry()))
			_, err := c.SystemOne(t.Context(), "hello", noulQuestion(t))
			de, ok := errors.AsType[*DialectError](err)
			if !ok {
				t.Fatalf("err = %v (%T), want a *DialectError", err, err)
			}
			if _, ok := errors.AsType[Error](err); !ok {
				t.Error("the error is not an SDK Error")
			}
			if got := de.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
			if tt.wantSame != (de == refusal) {
				t.Errorf("the dialect's own *DialectError returned = %t, want %t", de == refusal, tt.wantSame)
			}
			if !tt.wantSame && !errors.Is(err, cause) {
				t.Error("errors.Is(err, cause) = false: the wrapped error is lost")
			}
			if n := c.Stats().Attempts; n != 0 {
				t.Errorf("Stats().Attempts = %d, want 0", n)
			}
		})
	}
}

// TestDialectRetrySendsTheSameBody pins that every attempt of a call sends
// the dialect's body byte for byte: a 429 that asks for a retry, then a 200.
func TestDialectRetrySendsTheSameBody(t *testing.T) {
	out := []byte(`{"vendor":"request","n":1}`)
	tooMany := testsupport.JSON(http.StatusTooManyRequests, []byte(`{"error":"slow down"}`))
	tooMany.Header = http.Header{"Retry-After-Ms": {"1"}, "Content-Type": {"application/json"}}
	rec := &testsupport.Recorder{Replies: []testsupport.Reply{tooMany, testsupport.JSON(http.StatusOK, []byte(dialectBody))}}
	d := funcDialect(func([]byte, []PreparedQuestion) ([]byte, ResponseFunc, error) { return out, nil, nil })
	c := dialectClient(t, d, rec, WithRetry(DefaultRetry().MaxRetries(1).Backoff(time.Millisecond, time.Millisecond, 0)))
	if _, err := c.SystemOne(t.Context(), "hello", noulQuestion(t)); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	reqs := rec.Requests()
	if len(reqs) != 2 {
		t.Fatalf("%d requests, want 2", len(reqs))
	}
	for i, r := range reqs {
		if !bytes.Equal(r.Body, out) {
			t.Errorf("attempt %d sent %s, want %s", i, r.Body, out)
		}
	}
	if got := reqs[1].Header.Get("X-Typesafe-Retry-Count"); got != "1" {
		t.Errorf("the retry's X-TypeSafe-Retry-Count = %q, want 1", got)
	}
}

// TestDialectResponse pins the response seam: a 2xx body the ResponseFunc
// translates is what the decoder, RawBody and the validation errors see,
// while the LevelTrace record holds the body as it arrived; a failure of the
// translation is a *ResponseValidationError at the path the dialect named,
// holding the body as it arrived; any other status keeps its body and gains
// the ErrorType the dialect gives it when the body names none.
func TestDialectResponse(t *testing.T) {
	const vendor = `{"wrapped":` + dialectBody + `}`
	tests := map[string]struct {
		status    int
		body      string
		translate ResponseFunc
		check     func(t *testing.T, resp *SystemOneResponse, err error)
	}{
		"success: the translated body is decoded and kept": {
			status: http.StatusOK, body: vendor,
			translate: func(int, http.Header, []byte) (Translated, error) {
				return Translated{Body: []byte(dialectBody)}, nil
			},
			check: func(t *testing.T, resp *SystemOneResponse, err error) {
				if err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
				if got := string(resp.Meta().RawBody()); got != dialectBody {
					t.Errorf("RawBody() = %s, want the translated body", got)
				}
				if a, ok := resp.Answers().Noul("q"); !ok || a.Noul() != 0.25 {
					t.Errorf("answer q = %v, %t; want 0.25", a.Noul(), ok)
				}
			},
		},
		"success: a nil Body keeps the body as it arrived": {
			status: http.StatusOK, body: dialectBody,
			translate: func(int, http.Header, []byte) (Translated, error) { return Translated{}, nil },
			check: func(t *testing.T, resp *SystemOneResponse, err error) {
				if err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
				if got := string(resp.Meta().RawBody()); got != dialectBody {
					t.Errorf("RawBody() = %s, want the body as it arrived", got)
				}
			},
		},
		"error: a translated body the decoder refuses holds the translated body": {
			status: http.StatusOK, body: vendor,
			translate: func(int, http.Header, []byte) (Translated, error) {
				return Translated{Body: []byte(`{"usage":{},"answers":{}}`)}, nil
			},
			check: func(t *testing.T, _ *SystemOneResponse, err error) {
				ve := validationError(t, err)
				if ve.FieldPath != "model" || string(ve.Body) != `{"usage":{},"answers":{}}` {
					t.Errorf("FieldPath %q, Body %s; want model and the translated body", ve.FieldPath, ve.Body)
				}
			},
		},
		"error: a ResponsePathError names the FieldPath, escaped": {
			status: http.StatusOK, body: vendor,
			translate: func(int, http.Header, []byte) (Translated, error) {
				return Translated{}, fmt.Errorf("translate: %w", &ResponsePathError{Path: "answers[1].na\x1bme", Err: errors.New("missing")})
			},
			check: func(t *testing.T, _ *SystemOneResponse, err error) {
				ve := validationError(t, err)
				if ve.FieldPath != `answers[1].na\x1bme` || string(ve.Body) != vendor || ve.StatusCode != http.StatusOK {
					t.Errorf("FieldPath %q, Body %s, StatusCode %d; want the escaped path, the body as it arrived, 200", ve.FieldPath, ve.Body, ve.StatusCode)
				}
				if _, ok := errors.AsType[*ResponsePathError](err); !ok {
					t.Error("the *ResponsePathError is not in the chain")
				}
				const text = `POST https://vendor.example.test/v1/decide: 200 Invalid response data at 'answers[1].na\x1bme'. (request_id=req_1)`
				if got := ve.Error(); got != text {
					t.Errorf("Error() = %q, want %q", got, text)
				}
			},
		},
		"error: any other error is at the body as a whole": {
			status: http.StatusOK, body: vendor,
			translate: func(int, http.Header, []byte) (Translated, error) {
				return Translated{}, errors.New("not the vendor's shape")
			},
			check: func(t *testing.T, _ *SystemOneResponse, err error) {
				if ve := validationError(t, err); ve.FieldPath != "." {
					t.Errorf("FieldPath = %q, want .", ve.FieldPath)
				}
			},
		},
		"error: a failure status keeps its body and gains the ErrorType": {
			status: http.StatusUnauthorized, body: `{"code":-1,"message":"Invalid API key."}`,
			translate: func(int, http.Header, []byte) (Translated, error) {
				return Translated{Body: []byte(`ignored`), ErrorType: "authentication_error"}, nil
			},
			check: func(t *testing.T, _ *SystemOneResponse, err error) {
				apiErr, ok := errors.AsType[*APIError](err)
				if !ok {
					t.Fatalf("err = %v (%T), want an *APIError", err, err)
				}
				if string(apiErr.Body) != `{"code":-1,"message":"Invalid API key."}` || apiErr.ErrorType != "authentication_error" ||
					apiErr.Message != "Invalid API key." || !apiErr.IsAuthentication() {
					t.Errorf("Body %s, ErrorType %q, Message %q, IsAuthentication %t", apiErr.Body, apiErr.ErrorType, apiErr.Message, apiErr.IsAuthentication())
				}
			},
		},
		"error: the body's own error_type wins": {
			status: http.StatusForbidden, body: `{"detail":{"error_type":"permission_error","message":"no"}}`,
			translate: func(int, http.Header, []byte) (Translated, error) {
				return Translated{ErrorType: "authentication_error"}, nil
			},
			check: func(t *testing.T, _ *SystemOneResponse, err error) {
				if apiErr, ok := errors.AsType[*APIError](err); !ok || apiErr.ErrorType != "permission_error" {
					t.Errorf("err = %v, want an *APIError with the body's ErrorType permission_error", err)
				}
			},
		},
		"error: a translation that fails on a failure status leaves the APIError as the body makes it": {
			status: http.StatusBadRequest, body: `{"message":"bad"}`,
			translate: func(int, http.Header, []byte) (Translated, error) {
				return Translated{ErrorType: "api_usage_error"}, errors.New("unreadable")
			},
			check: func(t *testing.T, _ *SystemOneResponse, err error) {
				apiErr, ok := errors.AsType[*APIError](err)
				if !ok || apiErr.ErrorType != "" || apiErr.Message != "bad" {
					t.Errorf("err = %v, want an *APIError with message bad and no ErrorType", err)
				}
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			var seen struct {
				status int
				header http.Header
				body   string
			}
			d := funcDialect(func([]byte, []PreparedQuestion) ([]byte, ResponseFunc, error) {
				return nil, func(status int, h http.Header, body []byte) (Translated, error) {
					calls.Add(1)
					seen.status, seen.header, seen.body = status, h, string(body)
					return tt.translate(status, h, body)
				}, nil
			})
			reply := testsupport.JSON(tt.status, []byte(tt.body))
			reply.Header = http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"req_1"}}
			rec := &testsupport.Recorder{Replies: []testsupport.Reply{reply}}
			logs := testsupport.NewLogRecorder(LevelTrace)
			c := dialectClient(t, d, rec, WithLogger(logs.Logger()))
			resp, err := c.SystemOne(t.Context(), "hello", noulQuestion(t))
			tt.check(t, resp, err)
			if n := calls.Load(); n != 1 {
				t.Errorf("the ResponseFunc ran %d times, want once", n)
			}
			if seen.status != tt.status || seen.body != tt.body || seen.header.Get("X-Request-Id") != "req_1" {
				t.Errorf("the ResponseFunc saw status %d, body %s, header %v; want the response as it arrived", seen.status, seen.body, seen.header)
			}
			var traced []string
			for _, r := range logs.At(LevelTrace) {
				if v, ok := r.Attr("body"); ok && r.Message == "response body" {
					traced = append(traced, v.String())
				}
			}
			if diff := gocmp.Diff([]string{tt.body}, traced); diff != "" {
				t.Errorf("LevelTrace response body records (-want +got):\n%s", diff)
			}
		})
	}
}

// TestDialectResponseTooLarge pins that a body over the size limit never
// reaches the ResponseFunc.
func TestDialectResponseTooLarge(t *testing.T) {
	var calls atomic.Int32
	d := funcDialect(func([]byte, []PreparedQuestion) ([]byte, ResponseFunc, error) {
		return nil, func(int, http.Header, []byte) (Translated, error) {
			calls.Add(1)
			return Translated{}, nil
		}, nil
	})
	rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, []byte(dialectBody))}}
	c := dialectClient(t, d, rec, WithMaxResponseBytes(8))
	_, err := c.SystemOne(t.Context(), "hello", noulQuestion(t))
	if _, ok := errors.AsType[*ResponseTooLargeError](err); !ok {
		t.Fatalf("err = %v (%T), want a *ResponseTooLargeError", err, err)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the ResponseFunc ran %d times, want never", n)
	}
}

// TestDialectRequestLog pins that the LevelTrace request record holds the
// body the call sent: the dialect's, never the SDK body it released.
func TestDialectRequestLog(t *testing.T) {
	out := []byte(`{"vendor":"request"}`)
	d := funcDialect(func([]byte, []PreparedQuestion) ([]byte, ResponseFunc, error) { return out, nil, nil })
	rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, []byte(dialectBody))}}
	logs := testsupport.NewLogRecorder(LevelTrace)
	c := dialectClient(t, d, rec, WithLogger(logs.Logger()))
	if _, err := c.SystemOne(t.Context(), "hello", noulQuestion(t)); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	var got []string
	for _, r := range logs.Records() {
		switch r.Message {
		case "request":
			if v, ok := r.Attr("body_bytes"); ok {
				got = append(got, "bytes="+v.String())
			}
		case "request body":
			if v, ok := r.Attr("body"); ok {
				got = append(got, "body="+v.String())
			}
		}
	}
	want := []string{"bytes=" + strconv.Itoa(len(out)), "body=" + string(out)}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("request records (-want +got):\n%s", diff)
	}
}

// TestDialectConcurrentCalls runs calls in parallel through one dialect that
// writes each call's own body from the SDK's and reads each response back,
// with every record logged down to LevelTrace, so that, under -race, a body
// read or logged after the SDK released it, or a translation that saw
// another call's response, shows as a data race or as a call that received
// another call's answer.
func TestDialectConcurrentCalls(t *testing.T) {
	const calls = 32
	d := funcDialect(func(body []byte, _ []PreparedQuestion) ([]byte, ResponseFunc, error) {
		// The vendor's request is the SDK's body behind a prefix, in a
		// slice of its own.
		out := append([]byte("vendor:"), body...)
		return out, func(_ int, _ http.Header, b []byte) (Translated, error) {
			return Translated{Body: bytes.TrimPrefix(b, []byte("vendor:"))}, nil
		}, nil
	})
	rec := &testsupport.Recorder{Respond: func(r testsupport.RecordedRequest) testsupport.Reply {
		// Answer with the noul the state names, so that each call can
		// check it received its own answer.
		i := bytes.Index(r.Body, []byte(`"state":"call-`))
		n, _ := strconv.Atoi(string(r.Body[i+len(`"state":"call-`) : i+len(`"state":"call-`)+2]))
		return testsupport.JSON(http.StatusOK, fmt.Appendf(nil, `vendor:{"model":"m","usage":{},"answers":{"q":{"type":"noul","noul":0.%02d}}}`, n))
	}}
	logs := testsupport.NewLogRecorder(LevelTrace)
	c := dialectClient(t, d, rec, WithLogger(logs.Logger()))
	qs := noulQuestion(t)
	var wg sync.WaitGroup
	for i := range calls {
		wg.Go(func() {
			resp, err := c.SystemOne(t.Context(), fmt.Sprintf("call-%02d", i), qs)
			if err != nil {
				t.Errorf("call %d: %v", i, err)
				return
			}
			want, _ := strconv.ParseFloat(fmt.Sprintf("0.%02d", i), 64)
			if a, _ := resp.Answers().Noul("q"); a.Noul() != want {
				t.Errorf("call %d received noul %v, want %v", i, a.Noul(), want)
			}
		})
	}
	wg.Wait()
	for _, r := range rec.Requests() {
		if !bytes.HasPrefix(r.Body, []byte(`vendor:{"state":"call-`)) {
			t.Errorf("request %d sent %s, want the vendor's body over its own state", r.Index, r.Body)
		}
	}
	logged := 0
	for _, r := range logs.At(LevelTrace) {
		if v, ok := r.Attr("body"); ok && r.Message == "request body" && v.String() != "" {
			logged++
		}
	}
	if logged != calls {
		t.Errorf("%d request body records, want one per call, %d", logged, calls)
	}
}

// TestProviderDialectWithoutOne pins that a client without a dialect sends
// the SDK's body and keeps the response as it arrived: the seam costs it
// nothing but one nil check.
func TestProviderDialectWithoutOne(t *testing.T) {
	rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, []byte(dialectBody))}}
	c := dialectClient(t, nil, rec)
	if c.cfg().Dialect != nil {
		t.Fatalf("Config().Dialect = %v, want nil for a provider without one", c.cfg().Dialect)
	}
	resp, err := c.SystemOne(t.Context(), "hello", noulQuestion(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if got := string(resp.Meta().RawBody()); got != dialectBody {
		t.Errorf("RawBody() = %s, want the body as it arrived", got)
	}
	if !bytes.HasPrefix(rec.Requests()[0].Body, []byte(`{"state":"hello"`)) {
		t.Errorf("sent %s, want the SDK's body", rec.Requests()[0].Body)
	}
}

// TestResponsePathErrorText pins ResponsePathError's text and chain.
func TestResponsePathErrorText(t *testing.T) {
	reason := errors.New("missing")
	tests := map[string]struct {
		err  *ResponsePathError
		want string
	}{
		"success: a path and a reason": {err: &ResponsePathError{Path: "data.result", Err: reason}, want: "data.result: missing"},
		"success: the body as a whole": {err: &ResponsePathError{Err: reason}, want: ".: missing"},
		"success: no reason":           {err: &ResponsePathError{Path: "code"}, want: "code"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
			if tt.err.Err != nil && !errors.Is(tt.err, reason) {
				t.Error("errors.Is(err, reason) = false")
			}
		})
	}
}

// BenchmarkOverlaps measures the alias check on the common case, a dialect's
// own body against a short call body whose pooled scratch grew to 1 MiB in
// an earlier, larger call.
func BenchmarkOverlaps(b *testing.B) {
	scratch := make([]byte, 1<<20)
	body := scratch[:600:600]
	out := make([]byte, 700)
	for b.Loop() {
		if overlaps(out, body) {
			b.Fatal("a fresh slice overlaps")
		}
	}
}
