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

	"github.com/zchee/typesafe-sdk-go/internal/engine"
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

// TestModelsMissingMemberPath ports test_nested_missing_field_path
// (tests/test_responses.py:61-68): a model card without one of its three
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
			rve := validationError(t, decodeModels(&wire.ResponseMeta{Status: http.StatusOK, Body: []byte(body)}, "", engine.HeaderRedactor{}, &dst))
			if want := "models[1]." + missing; rve.FieldPath != want {
				t.Errorf("FieldPath = %q, want %q", rve.FieldPath, want)
			}
			if want := "200 Invalid response data at 'models[1]." + missing + "'."; rve.Error() != want {
				t.Errorf("Error() = %q, want %q", rve.Error(), want)
			}
		})
	}
}

// TestFieldPathIsEscapedInError checks the escaping of a failing path that
// holds a name the server chose: it is escaped in FieldPath and in Error().
func TestFieldPathIsEscapedInError(t *testing.T) {
	body := `{"model":"m","usage":{},"answers":{"a\nb\\":{"type":"noul"}}}`
	var dst wire.SystemOneResult
	rve := validationError(t, decodeSystemOneInto(t.Context(), nil, &wire.ResponseMeta{Status: 200, Body: []byte(body)}, "", engine.HeaderRedactor{}, nil, "", &dst, nil))
	if want := `answers.a\nb\\.noul`; rve.FieldPath != want {
		t.Errorf("FieldPath = %q, want %q", rve.FieldPath, want)
	}
	if want := `200 Invalid response data at 'answers.a\nb\\.noul'.`; rve.Error() != want {
		t.Errorf("Error() = %q, want %q", rve.Error(), want)
	}
}

// TestUnknownAnswerTypeSkipped ports test_unknown_answer_type_ignored
// (tests/test_responses.py:157-175) to the decode: an answer of a type this
// version does not model is dropped with one WARN line naming it and its
// type, and the body keeps it. The call through the client is re-asserted
// by TestUnknownAnswerTypeThroughClient.
func TestUnknownAnswerTypeSkipped(t *testing.T) {
	body := testsupport.Fixture(t, "unknown-answer-type.json")
	rec := testsupport.NewLogRecorder(slog.LevelDebug)
	meta := &wire.ResponseMeta{Status: http.StatusOK, Header: headers("X-Typesafe-Request-Id", "req-9"), Body: body}
	var dst wire.SystemOneResult
	if err := decodeSystemOneInto(t.Context(), rec.Logger(), meta, systemOneEndpoint, engine.HeaderRedactor{}, noulQuestion(t), "test", &dst, nil); err != nil {
		t.Fatal(err)
	}
	want := []wire.AnswerEntry{{Name: "spam", Answer: wire.Answer{Kind: wire.KindNoul, Noul: wire.NoulAnswer{Noul: 0.9}}}}
	if diff := gocmp.Diff(want, dst.Answers.Entries()); diff != "" {
		t.Errorf("answers (-want +got):\n%s", diff)
	}
	got := rec.Records()
	if len(got) != 1 || got[0].String() != "WARN "+engine.MsgSkippedAnswer+" answer=mystery type=aurora" {
		t.Errorf("records = %v, want one WARN naming mystery and aurora", got)
	}
	if !strings.Contains(string(meta.Body), `"mystery":{"type":"aurora"`) {
		t.Errorf("the body lost the unknown answer: %s", meta.Body)
	}
}

// TestUnknownAnswerTypeWarnCap checks the bound on the WARN lines, at most
// eight per response, then one counting the rest (docs/deviations.md,
// "unknown answers logged at most 8 times"), and that the names and types in
// them are escaped and cut at 128 characters.
func TestUnknownAnswerTypeWarnCap(t *testing.T) {
	long := strings.Repeat("t", 300)
	var answers, warned []string
	for i := range 9 {
		n := strconv.Itoa(i)
		answers = append(answers, `"u`+n+`":{"type":"t`+n+`"}`)
		if i < 8 {
			warned = append(warned, "WARN "+engine.MsgSkippedAnswer+" answer=u"+n+" type=t"+n)
		}
	}
	tests := map[string]struct {
		answers []string
		want    []string
	}{
		"success: nine unknown answers, eight lines and a summary": {
			answers: answers,
			want:    append(warned, "WARN "+engine.MsgSkippedAnswers+" count=1"),
		},
		"success: eight unknown answers, no summary": {
			answers: answers[:8],
			want:    warned,
		},
		"success: a name and a type escaped and cut": {
			answers: []string{`"a\u001b[2Jb\\":{"type":"` + long + `"}`},
			want:    []string{"WARN " + engine.MsgSkippedAnswer + ` answer=a\x1b[2Jb\\ type=` + long[:128] + "…"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := `{"model":"m","usage":{},"answers":{` + strings.Join(tt.answers, ",") + `}}`
			rec := testsupport.NewLogRecorder(nil)
			var dst wire.SystemOneResult
			if err := decodeSystemOneInto(t.Context(), rec.Logger(), &wire.ResponseMeta{Status: 200, Body: []byte(body)}, "", engine.HeaderRedactor{}, nil, "m", &dst, nil); err != nil {
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
		if err := decodeSystemOneInto(t.Context(), logger, &wire.ResponseMeta{Status: 200, Body: body}, "", engine.HeaderRedactor{}, nil, "", &dst, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := rec.Records(); len(got) != 0 {
		t.Errorf("records = %v, want none above ERROR", got)
	}
}

// TestMalformedFixturesRefused checks through the root package that every
// testdata/malformed-*.json, and deviation-big-exp-noul.json, is refused with
// a *ResponseValidationError at the field path testdata/README.md names, the
// body and status kept, and that every other System One fixture is accepted.
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
			err := decodeSystemOneInto(t.Context(), nil, meta, systemOneEndpoint, engine.HeaderRedactor{}, nil, "", &dst, nil)
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
