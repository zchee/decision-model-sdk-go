// Copyright 2026 The typesafe-sdk-go Authors.
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

package typesafe

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// modelsEndpoint is how errors name the default list-models endpoint.
const modelsEndpoint = "GET https://api.typesafe.ai/v1/models"

// cardView is what a test compares of a model card.
type cardView struct{ Name, Description, ReleaseDate string }

// cardsOf returns the views of resp's models.
func cardsOf(resp *ModelsResponse) []cardView {
	var views []cardView
	for _, m := range resp.Models() {
		views = append(views, cardView{m.Name(), m.Description(), m.ReleaseDate()})
	}
	return views
}

// TestModelsListShape ports test_models_shape (C7): the list-models call is a
// GET of /v1/models without a body or a Content-Type, and the response's
// cards come back in order.
func TestModelsListShape(t *testing.T) {
	rec := replying(http.StatusOK, testsupport.Fixture(t, "models.json"))
	c := newTestClient(t, rec)
	resp, err := c.Models().List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	req := onlyRequest(t, rec)
	got := []any{req.Method, req.URL, req.Body, req.ContentLength, req.HasGetBody, req.Header.Get("Content-Type")}
	want := []any{http.MethodGet, "https://api.typesafe.ai/v1/models", []byte(nil), int64(0), false, ""}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("method, URL, body, length, GetBody, Content-Type (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff([]cardView{{"jev-latest", "Fast model", "2026-08-01"}}, cardsOf(resp)); diff != "" {
		t.Errorf("models (-want +got):\n%s", diff)
	}
}

// TestModelsIgnoreUnknownFields ports test_models_ignore_unknown_fields (C8):
// members of a card the SDK does not model are dropped from the card and
// stay in the raw body.
func TestModelsIgnoreUnknownFields(t *testing.T) {
	body := []byte(`{"models":[{"name":"jev-latest","description":"Fast model","release_date":"2026-08-01","context_window":128000,"pricing":null}]}`)
	c := newTestClient(t, replying(http.StatusOK, body))
	resp, err := c.Models().List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if diff := gocmp.Diff([]cardView{{"jev-latest", "Fast model", "2026-08-01"}}, cardsOf(resp)); diff != "" {
		t.Errorf("models (-want +got):\n%s", diff)
	}
	if !bytes.Equal(resp.Meta().RawBody(), body) || !bytes.Contains(resp.Meta().RawBody(), []byte(`"context_window":128000`)) {
		t.Errorf("RawBody() = %s, want the body as sent", resp.Meta().RawBody())
	}
}

// TestModelsInvalidBodies ports test_invalid_models_response (C9): a 200
// list-models body that is null, has no models, has models of the wrong kind
// or a card without its members fails with *ResponseValidationError.
func TestModelsInvalidBodies(t *testing.T) {
	tests := map[string]struct {
		body string
		path string
	}{
		"error: null":                 {body: `null`, path: "."},
		"error: an empty object":      {body: `{}`, path: "models"},
		"error: models a string":      {body: `{"models":"bad"}`, path: "models"},
		"error: a card without parts": {body: `{"models":[{"name":"x"}]}`, path: "models[0].description"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, replying(http.StatusOK, []byte(tt.body)))
			_, err := c.Models().List(t.Context())
			rve := validationError(t, err)
			got := []any{rve.FieldPath, rve.StatusCode, rve.Endpoint, string(rve.Body)}
			if diff := gocmp.Diff([]any{tt.path, http.StatusOK, modelsEndpoint, tt.body}, got); diff != "" {
				t.Errorf("path, status, endpoint, body (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAPIErrorMapping ports test_error_mapping (C11, AC-F3): every status
// outside 2xx is an *APIError whose kind the status alone decides, which
// keeps the status, the body, the headers and the request id, and renders
// as the Python SDK's str(); a 429's Retry-After-Ms is its RetryAfter. A 302
// is not followed.
func TestAPIErrorMapping(t *testing.T) {
	const body = `{"detail":{"message":"Server explanation"}}`
	tests := map[string]struct {
		status int
		kind   APIErrorKind
	}{
		"error: 400 bad request":           {status: 400, kind: APIErrorBadRequest},
		"error: 401 authentication":        {status: 401, kind: APIErrorAuthentication},
		"error: 403 permission denied":     {status: 403, kind: APIErrorPermissionDenied},
		"error: 404 not found":             {status: 404, kind: APIErrorNotFound},
		"error: 422 unprocessable entity":  {status: 422, kind: APIErrorUnprocessableEntity},
		"error: 429 rate limit":            {status: 429, kind: APIErrorRateLimit},
		"error: 500 internal server error": {status: 500, kind: APIErrorInternalServer},
		"error: 503 internal server error": {status: 503, kind: APIErrorInternalServer},
		"error: 408 other":                 {status: 408, kind: APIErrorOther},
		"error: 409 other":                 {status: 409, kind: APIErrorOther},
		"error: 302 other, not followed":   {status: 302, kind: APIErrorOther},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(tt.status, []byte(body), "X-Typesafe-Request-Id", "req_123", "Retry-After-Ms", "125")
			c := newTestClient(t, rec)
			_, err := c.Models().List(t.Context())
			ae, ok := errors.AsType[*APIError](err)
			if !ok {
				t.Fatalf("List error = %v (%T), want an *APIError", err, err)
			}
			id, idOK := ae.RequestID()
			got := []any{ae.Kind, ae.StatusCode, string(ae.Body), id, idOK, ae.Header.Get("Retry-After-Ms"), ae.Error()}
			want := []any{
				tt.kind, tt.status, body, "req_123", true, "125",
				modelsEndpoint + ": " + strconv.Itoa(tt.status) + " Server explanation (request_id=req_123)",
			}
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("kind, status, body, request id, Retry-After-Ms, Error() (-want +got):\n%s", diff)
			}
			if tt.kind == APIErrorRateLimit {
				if d, ok := ae.RetryAfter(); !ok || d != 125*time.Millisecond {
					t.Errorf("RetryAfter() = %v, %t, want 125ms, true", d, ok)
				}
			}
			if rec.Count() != 1 {
				t.Errorf("the transport saw %d requests, want 1", rec.Count())
			}
		})
	}
}

// TestAPIErrorMessages ports test_error_messages (C12, AC-F3): the message
// of a 400 is found in the body as the Python SDK finds it, and the error
// renders as "<endpoint>: 400 <message>".
func TestAPIErrorMessages(t *testing.T) {
	tests := map[string]struct {
		body    string
		message string
	}{
		"error: error wins over message and detail": {body: `{"error":"error","message":"message","detail":"detail"}`, message: "error"},
		"error: a nested error message":             {body: `{"error":{"message":"nested error"},"message":"message"}`, message: "nested error"},
		"error: message wins over detail":           {body: `{"message":"message","detail":"detail"}`, message: "message"},
		"error: detail":                             {body: `{"detail":"detail"}`, message: "detail"},
		"error: a nested detail message":            {body: `{"detail":{"message":"nested detail"}}`, message: "nested detail"},
		"error: a validation detail list": {
			body:    `{"detail":[{"loc":["body","questions","q","score","criteria",0],"msg":"Invalid"},{"msg":"Missing"},{}]}`,
			message: "questions.q.score.criteria.0: Invalid; Missing",
		},
		"error: plain text":             {body: `plain text`, message: "plain text"},
		"error: an object without text": {body: `{"unexpected":true}`, message: `{"unexpected":true}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, replying(http.StatusBadRequest, []byte(tt.body)))
			_, err := c.Models().List(t.Context())
			if _, ok := errors.AsType[*APIError](err); !ok {
				t.Fatalf("List error = %v (%T), want an *APIError", err, err)
			}
			if diff := gocmp.Diff(modelsEndpoint+": 400 "+tt.message, err.Error()); diff != "" {
				t.Errorf("Error() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestMalformedResponseThroughClient re-asserts
// test_malformed_response_raises_validation_error (R1, AC-F6) through the
// client: the eight bodies fail at the Python SDK's field paths, with the
// status, the request id and the body, rendered as the Python SDK's str().
// TestMalformedResponseFieldPaths checks the decode itself.
func TestMalformedResponseThroughClient(t *testing.T) {
	tests := map[string]struct {
		answers string
		path    string
	}{
		"error: no model":                  {answers: `{}`, path: "model"},
		"error: noul missing":              {answers: `{"n":{"type":"noul"}}`, path: "answers.n.noul"},
		"error: noul a string":             {answers: `{"n":{"type":"noul","noul":"0.5"}}`, path: "answers.n.noul"},
		"error: choice without confidence": {answers: `{"c":{"type":"choice","choice":"a","probabilities":{}}}`, path: "answers.c.confidence"},
		"error: choice without choice":     {answers: `{"c":{"type":"choice","confidence":0.5,"probabilities":{}}}`, path: "answers.c.choice"},
		"error: legend an array":           {answers: `{"s":{"type":"score","score":1.0,"confidence":1.0,"legend":[],"probabilities":{}}}`, path: "answers.s.legend"},
		"error: legend key not a level":    {answers: `{"s":{"type":"score","score":1.0,"confidence":1.0,"legend":{"x":"bad"},"probabilities":{}}}`, path: "answers.s.legend.x"},
		"error: answer not a mapping":      {answers: `{"c":"not-a-mapping"}`, path: "answers.c.type"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := `{"usage":{"input_tokens":1,"output_tokens":1},"answers":` + tt.answers
			if tt.path != "model" {
				body += `,"model":"test"`
			}
			body += "}"
			c := newTestClient(t, replying(http.StatusOK, []byte(body), "X-Typesafe-Request-Id", "req-123"))
			_, err := c.SystemOne(t.Context(), "x", noulQuestion(t))
			rve := validationError(t, err)
			id, _ := rve.RequestID()
			got := []any{rve.FieldPath, rve.StatusCode, id, string(rve.Body), rve.Error()}
			want := []any{tt.path, http.StatusOK, "req-123", body, systemOneEndpoint + ": 200 Invalid response data at '" + tt.path + "'. (request_id=req-123)"}
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("path, status, request id, body, Error() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestResponseRequestID ports test_response_carries_request_id (R3): the
// response's x-typesafe-request-id is Meta().RequestID().
func TestResponseRequestID(t *testing.T) {
	c := newTestClient(t, replying(http.StatusOK, testsupport.Fixture(t, "result.json"), "X-Typesafe-Request-Id", "req-42"))
	resp, err := c.SystemOne(t.Context(), "text", noulQuestion(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if id, ok := resp.Meta().RequestID(); !ok || id != "req-42" {
		t.Errorf("Meta().RequestID() = %q, %t, want \"req-42\", true", id, ok)
	}
}

// TestResponseMeta ports test_response_carries_raw_http_response (R4): the
// response keeps the status, the headers and the exact body it arrived with.
func TestResponseMeta(t *testing.T) {
	body := testsupport.Fixture(t, "result.json")
	c := newTestClient(t, replying(http.StatusOK, body, "X-Typesafe-Request-Id", "req-42"))
	resp, err := c.SystemOne(t.Context(), "text", noulQuestion(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	m := resp.Meta()
	got := []any{m.StatusCode(), m.Header().Get("X-Typesafe-Request-Id"), string(m.RawBody())}
	if diff := gocmp.Diff([]any{http.StatusOK, "req-42", string(body)}, got); diff != "" {
		t.Errorf("status, request id header, body (-want +got):\n%s", diff)
	}
}

// TestResponseCopyKeepsMeta ports test_copied_response_preserves_metadata
// (R6) to the deviation "responses are values": a copy of a response holds
// the same answers, request id and body, and its views agree with each
// other. Go has no pickle; a copy is an assignment.
func TestResponseCopyKeepsMeta(t *testing.T) {
	body := testsupport.Fixture(t, "result.json")
	c := newTestClient(t, replying(http.StatusOK, body, "X-Typesafe-Request-Id", "req-copy"))
	resp, err := c.SystemOne(t.Context(), "text", noulQuestion(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	restored := *resp
	if &restored == resp {
		t.Fatal("the copy is the response itself")
	}
	if id, ok := restored.Meta().RequestID(); !ok || id != "req-copy" {
		t.Errorf("copy's RequestID() = %q, %t", id, ok)
	}
	if !bytes.Equal(restored.Meta().RawBody(), body) {
		t.Errorf("copy's RawBody() = %s", restored.Meta().RawBody())
	}
	fromScores := map[string]float64{}
	for name, s := range restored.Answers().Scores() {
		fromScores[name] = s.Score()
	}
	quality, _ := restored.Answers().Get("quality")
	qs, _ := quality.Score()
	if diff := gocmp.Diff(map[string]float64{"quality": qs.Score()}, fromScores); diff != "" {
		t.Errorf("copy's Scores() against Get (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff(upstreamAnswers["quality"], viewOf(quality)); diff != "" {
		t.Errorf("copy's quality (-want +got):\n%s", diff)
	}
	if got, want := marshal(t, restored), marshal(t, resp); got != want {
		t.Errorf("copy's payload = %s, want %s", got, want)
	}
}

// TestZeroResponseHasEmptyMeta pins the deviation "empty Meta()" (R7): a
// response that did not come from a request has an empty Meta, where the
// Python SDK raises on access.
func TestZeroResponseHasEmptyMeta(t *testing.T) {
	var resp SystemOneResponse
	id, ok := resp.Meta().RequestID()
	got := []any{resp.Meta().StatusCode(), resp.Meta().Header() == nil, resp.Meta().RawBody() == nil, id, ok, resp.Answers().Len(), resp.Model()}
	if diff := gocmp.Diff([]any{0, true, true, "", false, 0, ""}, got); diff != "" {
		t.Errorf("status, nil header, nil body, request id, answers, model (-want +got):\n%s", diff)
	}
	var models ModelsResponse
	if models.Meta().StatusCode() != 0 || len(models.Models()) != 0 {
		t.Errorf("zero ModelsResponse has status %d and %d models", models.Meta().StatusCode(), len(models.Models()))
	}
}

// TestRequestIDAbsent ports test_missing_request_id_raises_on_access (R8): a
// response without x-typesafe-request-id reports none, where the Python SDK
// raises.
func TestRequestIDAbsent(t *testing.T) {
	c := newTestClient(t, replying(http.StatusOK, testsupport.Fixture(t, "result.json")))
	resp, err := c.SystemOne(t.Context(), "text", noulQuestion(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if id, ok := resp.Meta().RequestID(); ok || id != "" {
		t.Errorf("Meta().RequestID() = %q, %t, want \"\", false", id, ok)
	}
}

// TestUnknownMembersIgnoredThroughClient re-asserts
// test_unknown_extra_fields_tolerated (R9) through the client: members the
// SDK does not model, in the usage and in an answer, are ignored, and stay
// in the raw body.
func TestUnknownMembersIgnoredThroughClient(t *testing.T) {
	body := []byte(`{"model":"test","usage":{"input_tokens":1,"output_tokens":1,"reasoning_tokens":9,"billing_units":1},"answers":{"spam":{"type":"noul","noul":0.9,"explanation":"spammy"}}}`)
	c := newTestClient(t, replying(http.StatusOK, body))
	resp, err := c.SystemOne(t.Context(), "x", noulQuestion(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	n, _ := resp.Answers().Noul("spam")
	in, _ := resp.Usage().InputTokens()
	out, _ := resp.Usage().OutputTokens()
	got := []any{n.Noul(), in, out, string(resp.Meta().RawBody())}
	if diff := gocmp.Diff([]any{0.9, uint64(1), uint64(1), string(body)}, got); diff != "" {
		t.Errorf("noul, usage, body (-want +got):\n%s", diff)
	}
}

// TestUnknownAnswerTypeThroughClient re-asserts test_unknown_answer_type_ignored
// (R10) through the client: an answer of a type this version does not model
// is dropped and logged once at WARN, and stays in the raw body.
func TestUnknownAnswerTypeThroughClient(t *testing.T) {
	body := []byte(`{"model":"test","usage":{"input_tokens":1,"output_tokens":1},"answers":{"spam":{"type":"noul","noul":0.9},"mystery":{"type":"aurora","value":3}}}`)
	logs := testsupport.NewLogRecorder(slog.LevelWarn)
	c := newTestClient(t, replying(http.StatusOK, body, "X-Typesafe-Request-Id", "req-9"), WithLogger(logs.Logger()))
	resp, err := c.SystemOne(t.Context(), "text", noulQuestion(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	var names []string
	for name := range resp.Answers().All() {
		names = append(names, name)
	}
	if diff := gocmp.Diff([]string{"spam"}, names); diff != "" {
		t.Errorf("answers (-want +got):\n%s", diff)
	}
	if !bytes.Contains(resp.Meta().RawBody(), []byte(`"mystery":{"type":"aurora"`)) {
		t.Errorf("RawBody() lacks the unknown answer: %s", resp.Meta().RawBody())
	}
	var warned []string
	for _, r := range logs.At(slog.LevelWarn) {
		a, _ := r.Attr("answer")
		ty, _ := r.Attr("type")
		warned = append(warned, r.Message+" "+a.String()+" "+ty.String())
	}
	if diff := gocmp.Diff([]string{msgSkippedAnswer + " mystery aurora"}, warned); diff != "" {
		t.Errorf("WARN records (-want +got):\n%s", diff)
	}
}

// TestPublicTypesIgnoreUnknownMembersThroughClient re-asserts
// test_public_response_types_ignore_unknown_fields (R13) through the client
// for its seven types: an unknown member in a noul, a choice and a score
// answer, in the usage, at the top of a System One response, in a model card
// and at the top of a list-models response is ignored.
func TestPublicTypesIgnoreUnknownMembersThroughClient(t *testing.T) {
	systemOne := []byte(`{"unexpected":true,"model":"test","usage":{"unexpected":true},"answers":{` +
		`"n":{"type":"noul","noul":0.5,"unexpected":true},` +
		`"c":{"type":"choice","choice":"a","confidence":1.0,"probabilities":{"a":1.0},"unexpected":true},` +
		`"s":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"bad"},"probabilities":{"0":1.0},"unexpected":true}}}`)
	c := newTestClient(t, replying(http.StatusOK, systemOne))
	resp, err := c.SystemOne(t.Context(), "x", noulQuestion(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	got := map[string]answerView{}
	for name, a := range resp.Answers().All() {
		got[name] = viewOf(a)
	}
	want := map[string]answerView{
		"n": {Kind: KindNoul, Noul: 0.5},
		"c": {Kind: KindChoice, Choice: "a", Confidence: 1, Probabilities: map[string]float64{"a": 1}},
		"s": {Kind: KindScore, Confidence: 1, Levels: map[uint32]float64{0: 1}, Legend: map[uint32]string{0: "bad"}},
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("answers (-want +got):\n%s", diff)
	}
	if _, ok := resp.Usage().InputTokens(); ok {
		t.Error("Usage().InputTokens() reports a count the response did not carry")
	}

	models := []byte(`{"unexpected":true,"models":[{"name":"test","description":"Test model","release_date":"2026-09-14","unexpected":true}]}`)
	c = newTestClient(t, replying(http.StatusOK, models))
	list, err := c.Models().List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if diff := gocmp.Diff([]cardView{{"test", "Test model", "2026-09-14"}}, cardsOf(list)); diff != "" {
		t.Errorf("models (-want +got):\n%s", diff)
	}
}

// TestResponseSizeLimit checks NF5 through the client (section 6.2.1): a
// 2xx body over WithMaxResponseBytes, declared or not, fails with a
// *ResponseTooLargeError naming the limit, before a declared one is read; a
// body of exactly the limit is read; a failure status over the limit is an
// *APIError without a body; a body that ends before its declared length is a
// *ConnectionError that wraps io.ErrUnexpectedEOF.
func TestResponseSizeLimit(t *testing.T) {
	const limit = 64
	exact := []byte(`{"models":[]}` + strings.Repeat(" ", limit-len(`{"models":[]}`)))
	over := append(bytes.Clone(exact), ' ')
	tests := map[string]struct {
		reply testsupport.Reply
		check func(t *testing.T, resp *ModelsResponse, err error)
	}{
		"success: exactly the limit, declared": {
			reply: testsupport.Reply{Body: exact},
			check: func(t *testing.T, resp *ModelsResponse, err error) {
				if err != nil || len(resp.Meta().RawBody()) != limit {
					t.Errorf("List = %v, want the %d-byte body", err, limit)
				}
			},
		},
		"success: exactly the limit, undeclared": {
			reply: testsupport.Reply{Body: exact, ContentLength: -1},
			check: func(t *testing.T, resp *ModelsResponse, err error) {
				if err != nil || len(resp.Meta().RawBody()) != limit {
					t.Errorf("List = %v, want the %d-byte body", err, limit)
				}
			},
		},
		"error: one byte over, declared": {
			reply: testsupport.Reply{Body: over},
			check: tooLarge(limit),
		},
		"error: one byte over, undeclared": {
			reply: testsupport.Reply{Body: over, ContentLength: -1},
			check: tooLarge(limit),
		},
		"error: a failure status over the limit": {
			reply: testsupport.Reply{Status: http.StatusBadGateway, Body: over},
			check: func(t *testing.T, _ *ModelsResponse, err error) {
				ae, ok := errors.AsType[*APIError](err)
				if !ok {
					t.Fatalf("List error = %v (%T), want an *APIError", err, err)
				}
				got := []any{ae.StatusCode, ae.Body == nil, ae.Message}
				if diff := gocmp.Diff([]any{http.StatusBadGateway, true, "status code (no body)"}, got); diff != "" {
					t.Errorf("status, nil body, message (-want +got):\n%s", diff)
				}
			},
		},
		"error: a body shorter than declared": {
			reply: testsupport.Reply{Body: []byte(`{"models":`), ContentLength: 40},
			check: func(t *testing.T, _ *ModelsResponse, err error) {
				if _, ok := errors.AsType[*ConnectionError](err); !ok || !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Errorf("List error = %v (%T), want a *ConnectionError wrapping io.ErrUnexpectedEOF", err, err)
				}
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, &testsupport.Recorder{Replies: []testsupport.Reply{tt.reply}}, WithMaxResponseBytes(limit))
			resp, err := c.Models().List(t.Context())
			tt.check(t, resp, err)
		})
	}
}

// tooLarge returns a check that err is the *ResponseTooLargeError of a 200
// list-models response over limit.
func tooLarge(limit int64) func(*testing.T, *ModelsResponse, error) {
	return func(t *testing.T, _ *ModelsResponse, err error) {
		t.Helper()
		tl, ok := errors.AsType[*ResponseTooLargeError](err)
		if !ok {
			t.Fatalf("List error = %v (%T), want a *ResponseTooLargeError", err, err)
		}
		got := []any{tl.StatusCode, tl.Limit, tl.Endpoint, tl.Error()}
		want := []any{http.StatusOK, limit, modelsEndpoint, modelsEndpoint + ": 200 The response body is larger than the limit of " + strconv.FormatInt(limit, 10) + " bytes."}
		if diff := gocmp.Diff(want, got); diff != "" {
			t.Errorf("status, limit, endpoint, Error() (-want +got):\n%s", diff)
		}
	}
}

// chunks reads its data a few bytes at a time, as a network body does.
type chunks struct {
	data []byte
	step int
	err  error // returned once the data is read, io.EOF when nil
}

func (c *chunks) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		if c.err != nil {
			return 0, c.err
		}
		return 0, io.EOF
	}
	n := copy(p[:min(len(p), c.step)], c.data)
	c.data = c.data[n:]
	return n, nil
}

// TestReadBody checks the body read of section 6.2.1 (rulings R26b, R27):
// the first buffer is min(Content-Length, 256 KiB) for a declared body, with
// a spare byte when that is all of it, and 4 KiB for an undeclared one;
// growth doubles within the limit and the declared length, and the buffer
// that reaches either has a spare byte; the byte after the limit refuses the
// body, and a declared length over the limit is refused before a read.
func TestReadBody(t *testing.T) {
	body := func(n int) []byte { return bytes.Repeat([]byte{'x'}, n) }
	tests := map[string]struct {
		data     []byte
		declared int64
		limit    int64
		err      error
		wantErr  error
		wantCap  int // the capacity of the buffer returned, when it matters
	}{
		"success: an empty declared body":              {data: nil, declared: 0, limit: 64, wantCap: 1},
		"success: a declared body in one buffer":       {data: body(1000), declared: 1000, limit: 1 << 20, wantCap: 1001},
		"success: an undeclared small body":            {data: body(1000), declared: -1, limit: 1 << 20, wantCap: initialUndeclared},
		"success: an undeclared body that grows twice": {data: body(10000), declared: -1, limit: 1 << 20, wantCap: 4 * initialUndeclared},
		"success: an undeclared body of one full buffer grows once": {
			data: body(initialUndeclared), declared: -1, limit: 1 << 20, wantCap: 2 * initialUndeclared,
		},
		"success: a declared body of exactly the first buffer": {
			data: body(initialDeclared), declared: initialDeclared, limit: 1 << 30, wantCap: initialDeclared + 1,
		},
		"success: a declared body past the first buffer": {
			data: body(initialDeclared + 10), declared: initialDeclared + 10, limit: 1 << 30, wantCap: initialDeclared + 10 + 1,
		},
		"success: an undeclared body of exactly the limit": {data: body(5000), declared: -1, limit: 5000, wantCap: 5000 + 1},
		"error: declared over the limit, refused unread":   {data: nil, declared: 65, limit: 64, wantErr: errTooLarge},
		"error: undeclared one byte over the limit":        {data: body(65), declared: -1, limit: 64, wantErr: errTooLarge},
		"error: a read error":                              {data: body(10), declared: 20, limit: 64, err: io.ErrUnexpectedEOF, wantErr: io.ErrUnexpectedEOF},
		"success: declared zero, a few bytes sent":         {data: body(3), declared: 0, limit: 64},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := readBody(&chunks{data: tt.data, step: 700, err: tt.err}, tt.declared, tt.limit)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("readBody error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if !bytes.Equal(got, tt.data) {
				t.Errorf("readBody = %d bytes, want the %d sent", len(got), len(tt.data))
			}
			if tt.wantCap != 0 && cap(got) != tt.wantCap {
				t.Errorf("cap = %d, want %d", cap(got), tt.wantCap)
			}
		})
	}
}

// TestResponsesKeepTheirOwnBodies checks that a response owns the body it
// was read into: a later call with another body leaves an earlier response's
// RawBody, model and answers as they were.
func TestResponsesKeepTheirOwnBodies(t *testing.T) {
	first := testsupport.Fixture(t, "result.json")
	second := []byte(`{"model":"other-model","usage":{"input_tokens":7,"output_tokens":7},"answers":{"spam":{"type":"noul","noul":0.5}}}`)
	c := newTestClient(t, &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, first), testsupport.JSON(http.StatusOK, second)}})
	resp1, err := c.SystemOne(t.Context(), "x", q3Questions(t))
	if err != nil {
		t.Fatalf("first SystemOne: %v", err)
	}
	resp2, err := c.SystemOne(t.Context(), "x", q3Questions(t))
	if err != nil {
		t.Fatalf("second SystemOne: %v", err)
	}
	spam1, _ := resp1.Answers().Noul("spam")
	spam2, _ := resp2.Answers().Noul("spam")
	got := []any{string(resp1.Meta().RawBody()), resp1.Model(), spam1.Noul(), string(resp2.Meta().RawBody()), resp2.Model(), spam2.Noul()}
	want := []any{string(first), "jev-latest", 0.98, string(second), "other-model", 0.5}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("bodies, models and spam of both responses (-want +got):\n%s", diff)
	}
}

// TestAPIErrorRequestContextThroughClient ports test_api_error_request_context
// (E3) through the client: an error response of a list-models call and of a
// System One call names its endpoint, under the base URL's prefix, with the
// status, the message and the request id, and no printed form holds the API
// key. TestAPIErrorRendersEndpointStatusMessageRequestID checks the
// rendering itself. (E4's base URL with credentials is refused when the
// client is built, R63, and E5 builds its error directly: neither has a
// through-the-client form.)
func TestAPIErrorRequestContextThroughClient(t *testing.T) {
	const key = "private-api-key"
	tests := map[string]struct {
		call     func(c *Client) error
		endpoint string
	}{
		"error: models": {
			call:     func(c *Client) error { _, err := c.Models().List(t.Context()); return err },
			endpoint: "GET https://api.example.test/prefix/v1/models",
		},
		"error: system_one": {
			call: func(c *Client) error {
				_, err := c.SystemOne(t.Context(), "hello", mustPrepared(t, NewQuestions().Noul("q", Noul{Instructions: Text("Greeting?")})))
				return err
			},
			endpoint: "POST https://api.example.test/prefix/v1/systemone",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(http.StatusTooManyRequests, []byte(`{"message":"Too many requests"}`), "X-Typesafe-Request-Id", "req-context")
			clearEnv(t)
			c := newEnvClient(t, rec, WithAPIKey(key), WithBaseURL("https://api.example.test/prefix"))
			err := tt.call(c)
			ae, ok := errors.AsType[*APIError](err)
			if !ok {
				t.Fatalf("error = %v (%T), want an *APIError", err, err)
			}
			want := tt.endpoint + ": 429 Too many requests (request_id=req-context)"
			if diff := gocmp.Diff([]any{APIErrorRateLimit, tt.endpoint, want}, []any{ae.Kind, ae.Endpoint, ae.Error()}); diff != "" {
				t.Errorf("kind, endpoint, Error() (-want +got):\n%s", diff)
			}
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
				if out := fmt.Sprintf(verb, err); strings.Contains(out, key) {
					t.Errorf("%s of the error holds the key: %s", verb, out)
				}
			}
		})
	}
}

// TestAPIErrorBodyEdgeCasesThroughClient ports test_error_body_edge_cases
// (E6) through the client, with each body declared and undeclared: eight
// rows render exactly as the Python SDK's, and long-plain-message is cut at
// 200 characters (Appendix B "plain-text body cut at 200");
// TestAPIErrorBodyEdgeCases checks the reader itself.
func TestAPIErrorBodyEdgeCasesThroughClient(t *testing.T) {
	x := strings.Repeat("x", 201)
	rows := map[string]struct {
		body string
		want string
	}{
		"empty":                           {body: "", want: "400 status code (no body)"},
		"null":                            {body: "null", want: "400 status code (no body)"},
		"empty array":                     {body: "[]", want: "400 []"},
		"number":                          {body: "42", want: "400 42"},
		"not JSON, invalid UTF-8":         {body: "not JSON: \xff", want: "400 not JSON: �"},
		"deviation, long-plain-message":   {body: x, want: "400 " + x[:200] + "…"},
		"long-unstructured-body":          {body: `{"unknown":"` + x + `"}`, want: `400 {"unknown":"` + x[:188] + "…"},
		"an empty error stops the search": {body: `{"error":"","message":"ignored"}`, want: `400 {"error":"","message":"ignored"}`},
		"detail entries without a msg":    {body: `{"detail":[null,42,{"msg":4}]}`, want: `400 {"detail":[null,42,{"msg":4}]}`},
	}
	type test struct {
		body     string
		declared bool
		want     string
	}
	tests := map[string]test{}
	for name, r := range rows {
		tests["success: "+name+", declared"] = test{body: r.body, declared: true, want: r.want}
		tests["success: "+name+", undeclared"] = test{body: r.body, want: r.want}
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			reply := testsupport.Reply{Status: http.StatusBadRequest, Body: []byte(tt.body)}
			if !tt.declared {
				reply.ContentLength = -1
			}
			c := newTestClient(t, &testsupport.Recorder{Replies: []testsupport.Reply{reply}})
			_, err := c.Models().List(t.Context())
			ae, ok := errors.AsType[*APIError](err)
			if !ok {
				t.Fatalf("List error = %v (%T), want an *APIError", err, err)
			}
			if diff := gocmp.Diff(modelsEndpoint+": "+tt.want, ae.Error()); diff != "" {
				t.Errorf("Error() (-want +got):\n%s", diff)
			}
			if id, ok := ae.RequestID(); ok {
				t.Errorf("RequestID() = %q, want none", id)
			}
		})
	}
}

// TestSystemOneAnswersInline checks the answer entries a call allocates with
// its response (W5.3, newSystemOneAlloc): each call's answers live in an
// array of their own, so a later call changes no earlier response's
// answers; and a response with more answers than the questions asked, or a
// question set past maxInlineAnswers, whose entries the decode allocates,
// decodes the same.
func TestSystemOneAnswersInline(t *testing.T) {
	body := testsupport.Fixture(t, "result.json")
	one := mustPrepared(t, NewQuestions().Noul("spam", Noul{Instructions: Text("Spam?")}))
	five := NewQuestions()
	for _, name := range []string{"spam", "a", "b", "c", "d"} {
		five = five.Noul(name, Noul{Instructions: Text("?")})
	}
	tests := map[string]struct {
		qs *Prepared
	}{
		"success: three questions, the entries with the response":                {qs: q3Questions(t)},
		"success: one question and three answers, the entries outgrow the array": {qs: one},
		"success: five questions, past maxInlineAnswers":                         {qs: mustPrepared(t, five)},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, body)}}
			c := newTestClient(t, rec)
			first, err := c.SystemOne(t.Context(), "x", tt.qs)
			if err != nil {
				t.Fatal(err)
			}
			before := slices.Clone(first.res.Answers.Entries())
			second, err := c.SystemOne(t.Context(), "x", tt.qs)
			if err != nil {
				t.Fatal(err)
			}
			if n := first.Answers().Len(); n != 3 {
				t.Fatalf("%d answers, want result.json's 3", n)
			}
			if diff := gocmp.Diff(before, first.res.Answers.Entries()); diff != "" {
				t.Errorf("the first response's answers changed with the second call (-before +after):\n%s", diff)
			}
			if &first.res.Answers.Entries()[0] == &second.res.Answers.Entries()[0] {
				t.Error("two calls' responses share one array of answer entries")
			}
		})
	}
}

// TestAnswersOutliveTheirResponse checks the lifetime of answers whose
// entries live in the call's allocation (W5.3's N2): an Answers taken from a
// response keeps that allocation reachable after the caller drops the
// response, through collections and reuse of freed memory, as it kept the
// response and the decode's own array before. It holds for a set within
// maxInlineAnswers and for one past it.
func TestAnswersOutliveTheirResponse(t *testing.T) {
	body := testsupport.Fixture(t, "result.json")
	five := NewQuestions()
	for _, name := range []string{"spam", "a", "b", "c", "d"} {
		five = five.Noul(name, Noul{Instructions: Text("?")})
	}
	tests := map[string]struct {
		qs *Prepared
	}{
		"success: three questions, the entries in the call's allocation": {qs: q3Questions(t)},
		"success: five questions, the entries the decode's":              {qs: mustPrepared(t, five)},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, body)}})
			answers := func() Answers {
				resp, err := c.SystemOne(t.Context(), "x", tt.qs)
				if err != nil {
					t.Fatal(err)
				}
				return resp.Answers()
			}()
			want := slices.Clone(answers.entries())
			for range 3 {
				runtime.GC()
				junk := make([][]byte, 256)
				for i := range junk {
					junk[i] = bytes.Repeat([]byte{0xff}, 704)
				}
				runtime.KeepAlive(junk)
			}
			if diff := gocmp.Diff(want, answers.entries()); diff != "" || len(want) != 3 {
				t.Errorf("%d answers; they changed after the response was dropped (-before +after):\n%s", len(want), diff)
			}
		})
	}
}
