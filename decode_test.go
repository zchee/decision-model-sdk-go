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
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

const systemOneEndpoint = "POST https://api.typesafe.ai/v1/systemone"

// noulQuestion is the question set of the upstream response tests,
// {"q": {"type": "noul", "instructions": "?"}}.
func noulQuestion(t *testing.T) *Prepared {
	t.Helper()
	qs, err := NewQuestions().Noul("q", Noul{Instructions: Text("?")}).Prepare()
	if err != nil {
		t.Fatal(err)
	}
	return qs
}

// validationError asserts that err is a *ResponseValidationError.
func validationError(t *testing.T, err error) *ResponseValidationError {
	t.Helper()
	rve, ok := errors.AsType[*ResponseValidationError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want a *ResponseValidationError", err, err)
	}
	return rve
}

// TestMalformedResponseFieldPaths ports
// test_malformed_response_raises_validation_error (R1,
// tests/test_responses.py:29-58) to the decode of a 200 response with its
// request id: the eight bodies fail at the Python SDK's field paths, and the
// error keeps the status, request id and body and renders as the Python
// SDK's str(). The call through the client is re-asserted by W2.3.
func TestMalformedResponseFieldPaths(t *testing.T) {
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
			meta := &wire.ResponseMeta{Status: http.StatusOK, Header: headers("X-Typesafe-Request-Id", "req-123"), Body: []byte(body)}
			var dst wire.SystemOneResult
			err := decodeSystemOne(t.Context(), nil, meta, systemOneEndpoint, noulQuestion(t), "jev-latest", &dst)
			rve := validationError(t, err)
			if rve.FieldPath != tt.path || rve.StatusCode != http.StatusOK || string(rve.Body) != body {
				t.Errorf("field path %q status %d body %q, want %q 200 %q", rve.FieldPath, rve.StatusCode, rve.Body, tt.path, body)
			}
			if id, ok := rve.RequestID(); !ok || id != "req-123" {
				t.Errorf("RequestID() = %q, %t", id, ok)
			}
			want := systemOneEndpoint + ": 200 Invalid response data at '" + tt.path + "'. (request_id=req-123)"
			if diff := gocmp.Diff(want, rve.Error()); diff != "" {
				t.Errorf("Error() (-want +got):\n%s", diff)
			}
			if rve.Unwrap() == nil {
				t.Error("Unwrap() = nil, want the decoder's failure")
			}
		})
	}
}

// TestModelsMissingMemberPath ports test_nested_missing_field_path (R2,
// tests/test_responses.py:61-68): a model card without one of its three
// members fails at models[1].<member>, rendered as the Python SDK's str() of
// an error without an endpoint.
func TestModelsMissingMemberPath(t *testing.T) {
	card := map[string]string{"name": "test", "description": "Test model", "release_date": "2026-09-14"}
	for _, missing := range []string{"name", "description", "release_date"} {
		t.Run("error: "+missing, func(t *testing.T) {
			var second []string
			for _, k := range []string{"name", "description", "release_date"} {
				if k != missing {
					second = append(second, strconv.Quote(k)+":"+strconv.Quote(card[k]))
				}
			}
			body := `{"models":[{"name":"test","description":"Test model","release_date":"2026-09-14"},{` + strings.Join(second, ",") + `}]}`
			var dst wire.ModelList
			rve := validationError(t, decodeModels(&wire.ResponseMeta{Status: http.StatusOK, Body: []byte(body)}, "", &dst))
			if want := "models[1]." + missing; rve.FieldPath != want {
				t.Errorf("FieldPath = %q, want %q", rve.FieldPath, want)
			}
			if want := "200 Invalid response data at 'models[1]." + missing + "'."; rve.Error() != want {
				t.Errorf("Error() = %q, want %q", rve.Error(), want)
			}
		})
	}
}

// TestFieldPathIsEscapedInError checks NF7 on a failing path that holds a
// name the server chose: it is escaped in FieldPath and in Error().
func TestFieldPathIsEscapedInError(t *testing.T) {
	body := `{"model":"m","usage":{},"answers":{"a\nb\\":{"type":"noul"}}}`
	var dst wire.SystemOneResult
	rve := validationError(t, decodeSystemOne(t.Context(), nil, &wire.ResponseMeta{Status: 200, Body: []byte(body)}, "", nil, "", &dst))
	if want := `answers.a\nb\\.noul`; rve.FieldPath != want {
		t.Errorf("FieldPath = %q, want %q", rve.FieldPath, want)
	}
	if want := `200 Invalid response data at 'answers.a\nb\\.noul'.`; rve.Error() != want {
		t.Errorf("Error() = %q, want %q", rve.Error(), want)
	}
}

// TestUnknownAnswerTypeSkipped ports test_unknown_answer_type_ignored (R10,
// tests/test_responses.py:157-175) to the decode: an answer of a type this
// version does not model is dropped with one WARN line naming it and its
// type, and the body keeps it. The call through the client is re-asserted
// by W2.3.
func TestUnknownAnswerTypeSkipped(t *testing.T) {
	body := testsupport.Fixture(t, "unknown-answer-type.json")
	rec := testsupport.NewLogRecorder(slog.LevelDebug)
	meta := &wire.ResponseMeta{Status: http.StatusOK, Header: headers("X-Typesafe-Request-Id", "req-9"), Body: body}
	var dst wire.SystemOneResult
	if err := decodeSystemOne(t.Context(), rec.Logger(), meta, systemOneEndpoint, noulQuestion(t), "test", &dst); err != nil {
		t.Fatal(err)
	}
	want := []wire.AnswerEntry{{Name: "spam", Answer: wire.Answer{Kind: wire.KindNoul, Noul: wire.NoulAnswer{Noul: 0.9}}}}
	if diff := gocmp.Diff(want, dst.Answers.Entries()); diff != "" {
		t.Errorf("answers (-want +got):\n%s", diff)
	}
	got := rec.Records()
	if len(got) != 1 || got[0].String() != "WARN "+msgSkippedAnswer+" answer=mystery type=aurora" {
		t.Errorf("records = %v, want one WARN naming mystery and aurora", got)
	}
	if !strings.Contains(string(meta.Body), `"mystery":{"type":"aurora"`) {
		t.Errorf("the body lost the unknown answer: %s", meta.Body)
	}
}

// TestUnknownAnswerTypeWarnCap checks the bound on the WARN lines (Appendix
// B: at most eight per response, then one counting the rest) and that the
// names and types in them are escaped and cut at 128 characters.
func TestUnknownAnswerTypeWarnCap(t *testing.T) {
	long := strings.Repeat("t", 300)
	var answers []string
	for i := range 9 {
		answers = append(answers, `"u`+strconv.Itoa(i)+`":{"type":"t`+strconv.Itoa(i)+`"}`)
	}
	tests := map[string]struct {
		answers []string
		want    []string
	}{
		"success: nine unknown answers, eight lines and a summary": {
			answers: answers,
			want: []string{
				"WARN " + msgSkippedAnswer + " answer=u0 type=t0", "WARN " + msgSkippedAnswer + " answer=u1 type=t1",
				"WARN " + msgSkippedAnswer + " answer=u2 type=t2", "WARN " + msgSkippedAnswer + " answer=u3 type=t3",
				"WARN " + msgSkippedAnswer + " answer=u4 type=t4", "WARN " + msgSkippedAnswer + " answer=u5 type=t5",
				"WARN " + msgSkippedAnswer + " answer=u6 type=t6", "WARN " + msgSkippedAnswer + " answer=u7 type=t7",
				"WARN " + msgSkippedAnswers + " count=1",
			},
		},
		"success: eight unknown answers, no summary": {
			answers: answers[:8],
			want: []string{
				"WARN " + msgSkippedAnswer + " answer=u0 type=t0", "WARN " + msgSkippedAnswer + " answer=u1 type=t1",
				"WARN " + msgSkippedAnswer + " answer=u2 type=t2", "WARN " + msgSkippedAnswer + " answer=u3 type=t3",
				"WARN " + msgSkippedAnswer + " answer=u4 type=t4", "WARN " + msgSkippedAnswer + " answer=u5 type=t5",
				"WARN " + msgSkippedAnswer + " answer=u6 type=t6", "WARN " + msgSkippedAnswer + " answer=u7 type=t7",
			},
		},
		"success: a name and a type escaped and cut": {
			answers: []string{`"a\u001b[2Jb\\":{"type":"` + long + `"}`},
			want:    []string{"WARN " + msgSkippedAnswer + ` answer=a\x1b[2Jb\\ type=` + long[:128] + "…"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := `{"model":"m","usage":{},"answers":{` + strings.Join(tt.answers, ",") + `}}`
			rec := testsupport.NewLogRecorder(nil)
			var dst wire.SystemOneResult
			if err := decodeSystemOne(t.Context(), rec.Logger(), &wire.ResponseMeta{Status: 200, Body: []byte(body)}, "", nil, "m", &dst); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range rec.Records() {
				got = append(got, r.String())
			}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("records (-want +got):\n%s", diff)
			}
		})
	}
}

// TestSkippedAnswersLogWithoutALogger checks that a nil logger and one
// whose level is above WARN log nothing and the decode still succeeds.
func TestSkippedAnswersLogWithoutALogger(t *testing.T) {
	body := testsupport.Fixture(t, "unknown-answer-type.json")
	rec := testsupport.NewLogRecorder(slog.LevelError)
	for _, logger := range []*slog.Logger{nil, rec.Logger(), slog.New(slog.DiscardHandler)} {
		var dst wire.SystemOneResult
		if err := decodeSystemOne(t.Context(), logger, &wire.ResponseMeta{Status: 200, Body: body}, "", nil, "", &dst); err != nil {
			t.Fatal(err)
		}
	}
	if got := rec.Records(); len(got) != 0 {
		t.Errorf("records = %v, want none above ERROR", got)
	}
}

// TestMalformedFixturesRefused checks AC-F7 through the root package: every
// testdata/malformed-*.json, and deviation-big-exp-noul.json, is refused with
// a *ResponseValidationError at the field path testdata/README.md names, the
// body and status kept, and every other System One fixture is accepted.
func TestMalformedFixturesRefused(t *testing.T) {
	want := map[string]string{
		"malformed-empty.json":              ".",
		"malformed-whitespace.json":         ".",
		"malformed-truncated.json":          ".",
		"malformed-trailing-garbage.json":   ".",
		"malformed-trailing-value.json":     ".",
		"malformed-trailing-nbsp.json":      ".",
		"malformed-trailing-formfeed.json":  ".",
		"malformed-root-array.json":         ".",
		"malformed-invalid-utf8.json":       ".",
		"malformed-control-char.json":       ".",
		"malformed-control-char-key.json":   ".",
		"malformed-invalid-utf8-key.json":   ".",
		"malformed-invalid-escape.json":     ".",
		"malformed-bad-literal.json":        ".",
		"malformed-double-comma.json":       ".",
		"malformed-leading-zero.json":       ".",
		"malformed-trailing-comma.json":     ".",
		"malformed-big-exp.json":            "usage.input_tokens",
		"malformed-usage-type.json":         "usage.input_tokens",
		"malformed-missing-model.json":      "model",
		"malformed-missing-usage.json":      "usage",
		"malformed-answers-not-object.json": "answers",
		"deviation-big-exp-noul.json":       "answers.spam.noul",
		"malformed-too-deep.json":           ".",
		"deviation-nan-unknown.json":        ".",
		"deviation-nan-noul.json":           ".",
	}
	for _, name := range testsupport.FixtureNames(t, "malformed-*.json") {
		if _, ok := want[name]; !ok {
			t.Errorf("%s has no expected field path", name)
		}
	}
	for _, name := range testsupport.FixtureNames(t, "*.json") {
		if name == "models.json" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			body := testsupport.Fixture(t, name)
			meta := &wire.ResponseMeta{Status: http.StatusOK, Body: body}
			var dst wire.SystemOneResult
			err := decodeSystemOne(t.Context(), nil, meta, systemOneEndpoint, nil, "", &dst)
			path, reject := want[name]
			if !reject {
				if err != nil {
					t.Fatalf("refused (%v), want accepted", err)
				}
				return
			}
			rve := validationError(t, err)
			sameBody := len(rve.Body) == len(body) && (len(body) == 0 || &rve.Body[0] == &body[0])
			if rve.FieldPath != path || rve.StatusCode != http.StatusOK || !sameBody {
				t.Errorf("field path %q status %d, want %q 200 and the body kept", rve.FieldPath, rve.StatusCode, path)
			}
			if want := systemOneEndpoint + ": 200 Invalid response data at '" + path + "'."; rve.Error() != want {
				t.Errorf("Error() = %q, want %q", rve.Error(), want)
			}
		})
	}
}
