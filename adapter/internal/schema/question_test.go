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

package schema

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
)

// readJSON reads a JSON text the test wrote.
func readJSON(t testing.TB, doc string) jsonx.Node {
	t.Helper()
	v, err := jsonx.Read([]byte(doc))
	if err != nil {
		t.Fatalf("jsonx.Read(%.80q) error = %v", doc, err)
	}
	return v
}

// mustParse reads a questions object that ParseQuestions accepts.
func mustParse(t testing.TB, questions string) []Question {
	t.Helper()
	qs, err := ParseQuestions(readJSON(t, questions))
	if err != nil {
		t.Fatalf("ParseQuestions(%.80q) error = %v", questions, err)
	}
	return qs
}

// questionError returns the refusal of a questions object as a
// *QuestionError, and fails the test when ParseQuestions gives anything
// else.
func questionError(t *testing.T, questions string) *QuestionError {
	t.Helper()
	qs, err := ParseQuestions(readJSON(t, questions))
	var qe *QuestionError
	if !errors.As(err, &qe) {
		t.Fatalf("ParseQuestions(%.80q) = %d questions, %v; want a *QuestionError", questions, len(qs), err)
	}
	if qs != nil {
		t.Errorf("ParseQuestions(%.80q) returned %d questions with its refusal", questions, len(qs))
	}
	return qe
}

// TestInvalidQuestionsAreRejected ports
// tests/test_schema.py::test_invalid_dictionary_questions_are_rejected of
// upstream v0.2.1: its four question dictionaries, each alone under the name
// "answer", are refused. The keys carry upstream's parameter ids.
func TestInvalidQuestionsAreRejected(t *testing.T) {
	tests := map[string]struct {
		question string
		wantLoc  []string
	}{
		"error: question0, an unknown type": {
			question: `{"type":"unknown"}`,
			wantLoc:  []string{"answer", "type"},
		},
		"error: question1, a number for instructions": {
			question: `{"type":"noul","instructions":42}`,
			wantLoc:  []string{"answer", "instructions"},
		},
		"error: question2, an array for a choice's criteria": {
			question: `{"type":"choice","criteria":["yes","no"]}`,
			wantLoc:  []string{"answer", "criteria"},
		},
		"error: question3, an object for a score's criteria": {
			question: `{"type":"score","criteria":{"0":"Bad.","1":"Good."}}`,
			wantLoc:  []string{"answer", "criteria"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			qe := questionError(t, `{"answer":`+tt.question+`}`)
			if len(qe.Issues) != 1 {
				t.Fatalf("issues = %+v, want one", qe.Issues)
			}
			if diff := cmp.Diff(tt.wantLoc, qe.Issues[0].Loc); diff != "" {
				t.Errorf("the issue's place mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestQuestionsValidatedFromWire holds the rule of
// tests/test_schema.py::test_sdk_question_fields_are_revalidated of upstream
// v0.2.1, which has no counterpart of that shape here: upstream mutates an
// SDK question object after it was built and expects the adapter to validate
// it again, while a Go SDK question cannot be changed after it is prepared.
// The rule that is left is that the adapter trusts no SDK value: it
// validates the questions of the request body as they arrive. The first case
// is upstream's mutation, written as a body; the others are the remaining
// rules of the question models (typesafe-sdk-python 0.7.0,
// _core/question_types.py:65-113) and of _schema.py:35-66.
func TestQuestionsValidatedFromWire(t *testing.T) {
	const shape = "a question does not have the shape of its type"
	tests := map[string]struct {
		questions   string
		wantMessage string
		wantLocs    [][]string
	}{
		"error: a number for a noul criterion, upstream's mutated field": {
			questions:   `{"answer":{"type":"noul","criteria":{"true":42}}}`,
			wantMessage: shape,
			wantLocs:    [][]string{{"answer", "criteria", "true"}},
		},
		"error: no question": {
			questions:   `{}`,
			wantMessage: "At least one question is required.",
		},
		"error: a choice with one criterion": {
			questions:   `{"ok":{"type":"noul"},"c":{"type":"choice","criteria":{"only":null}}}`,
			wantMessage: "Score and choice questions require at least two criteria.",
			wantLocs:    [][]string{{"c", "criteria"}},
		},
		"error: a score with one criterion": {
			questions:   `{"s":{"type":"score","criteria":["only"]}}`,
			wantMessage: "Score and choice questions require at least two criteria.",
			wantLocs:    [][]string{{"s", "criteria"}},
		},
		"error: a score with no criterion": {
			questions:   `{"s":{"type":"score","criteria":[]}}`,
			wantMessage: "Score and choice questions require at least two criteria.",
			wantLocs:    [][]string{{"s", "criteria"}},
		},
		"error: only the first question with too few criteria is named": {
			questions:   `{"a":{"type":"choice","criteria":{}},"b":{"type":"score","criteria":["x"]}}`,
			wantMessage: "Score and choice questions require at least two criteria.",
			wantLocs:    [][]string{{"a", "criteria"}},
		},
		"error: a shape is refused before a count": {
			questions:   `{"few":{"type":"score","criteria":["x"]},"bad":{"type":"noul","instructions":true}}`,
			wantMessage: shape,
			wantLocs:    [][]string{{"bad", "instructions"}},
		},
		"error: every refused place of every question": {
			questions: `{"":{"type":"noul","extra":1,"instructions":1.5,"criteria":{"true":false,"maybe":"x","false":null}},` +
				`"c":{"type":"choice","instructions":null,"criteria":{"a":1,"b":"ok","c":true}},` +
				`"s":{"type":"score","criteria":["ok",null,2,{"k":1},[]],"title":"t"}}`,
			wantMessage: shape,
			wantLocs: [][]string{
				{"", "extra"},
				{"", "instructions"},
				{"", "criteria", "true"},
				{"", "criteria", "maybe"},
				{"c", "criteria", "a"},
				{"c", "criteria", "c"},
				{"s", "title"},
				{"s", "criteria", "1"},
				{"s", "criteria", "2"},
			},
		},
		"error: a question that is not an object": {
			questions:   `{"a":"noul","b":null,"c":[{"type":"noul"}]}`,
			wantMessage: shape,
			wantLocs:    [][]string{{"a"}, {"b"}, {"c"}},
		},
		"error: no type": {
			questions:   `{"a":{"instructions":"i"}}`,
			wantMessage: shape,
			wantLocs:    [][]string{{"a"}},
		},
		"error: a type that is not a string": {
			questions:   `{"a":{"type":null},"b":{"type":1},"c":{"type":["noul"]}}`,
			wantMessage: shape,
			wantLocs:    [][]string{{"a", "type"}, {"b", "type"}, {"c", "type"}},
		},
		"error: a type in other letters": {
			questions:   `{"a":{"type":"Noul"},"b":{"type":""},"c":{"type":"noul "}}`,
			wantMessage: shape,
			wantLocs:    [][]string{{"a", "type"}, {"b", "type"}, {"c", "type"}},
		},
		"error: the other members of a question with a bad type are not read": {
			questions:   `{"a":{"type":"x","instructions":1,"extra":2}}`,
			wantMessage: shape,
			wantLocs:    [][]string{{"a", "type"}},
		},
		"error: noul criteria of another kind": {
			questions:   `{"a":{"type":"noul","criteria":["t","f"]},"b":{"type":"noul","criteria":"t"},"c":{"type":"noul","criteria":0}}`,
			wantMessage: shape,
			wantLocs:    [][]string{{"a", "criteria"}, {"b", "criteria"}, {"c", "criteria"}},
		},
		"error: a choice or a score without criteria": {
			questions:   `{"c":{"type":"choice"},"s":{"type":"score","instructions":"i"}}`,
			wantMessage: shape,
			wantLocs:    [][]string{{"c", "criteria"}, {"s", "criteria"}},
		},
		"error: null criteria for a choice or a score": {
			questions:   `{"c":{"type":"choice","criteria":null},"s":{"type":"score","criteria":null}}`,
			wantMessage: shape,
			wantLocs:    [][]string{{"c", "criteria"}, {"s", "criteria"}},
		},
		"error: a string for a score's criteria": {
			questions:   `{"s":{"type":"score","criteria":"ab"}}`,
			wantMessage: shape,
			wantLocs:    [][]string{{"s", "criteria"}},
		},
		"error: a later member of the same name is the one validated": {
			questions:   `{"a":{"type":"noul"},"a":{"type":"noul","instructions":"i","instructions":7}}`,
			wantMessage: shape,
			wantLocs:    [][]string{{"a", "instructions"}},
		},
		"error: JSON instructions nested deeper than a prompt can hold": {
			questions:   `{"a":{"type":"noul","instructions":` + strings.Repeat("[", 256) + strings.Repeat("]", 256) + `}}`,
			wantMessage: shape,
			wantLocs:    [][]string{{"a", "instructions"}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			// The questions are read out of a whole request body, as the
			// adapter receives them.
			body := readJSON(t, `{"state":"s","model":"m","questions":`+tt.questions+`}`)
			questions, _ := body.Member("questions")
			qs, err := ParseQuestions(questions)
			var qe *QuestionError
			if !errors.As(err, &qe) {
				t.Fatalf("ParseQuestions = %d questions, %v; want a *QuestionError", len(qs), err)
			}
			if qe.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", qe.Message, tt.wantMessage)
			}
			var locs [][]string
			for _, is := range qe.Issues {
				locs = append(locs, is.Loc)
				if is.Message == "" {
					t.Errorf("the issue at %q has no message", is.Loc)
				}
			}
			if diff := cmp.Diff(tt.wantLocs, locs); diff != "" {
				t.Errorf("the refused places mismatch (-want +got):\n%s", diff)
			}
			if !strings.Contains(qe.Error(), tt.wantMessage) {
				t.Errorf("Error() = %q, which lacks the message %q", qe.Error(), tt.wantMessage)
			}
			for _, is := range qe.Issues {
				if !strings.Contains(qe.Error(), is.Message) {
					t.Errorf("Error() = %q, which lacks the issue %q", qe.Error(), is.Message)
				}
			}
		})
	}

	t.Run("error: questions that are not an object", func(t *testing.T) {
		for _, doc := range []string{`null`, `[]`, `[{"type":"noul"}]`, `"x"`, `0`, `true`} {
			if qs, err := ParseQuestions(readJSON(t, doc)); !errors.Is(err, ErrNotObject) || qs != nil {
				t.Errorf("ParseQuestions(%s) = %v, %v; want nil, ErrNotObject", doc, qs, err)
			}
		}
		if _, err := ParseQuestions(jsonx.Node{}); !errors.Is(err, ErrNotObject) {
			t.Errorf("ParseQuestions(the zero Node) error = %v, want ErrNotObject", err)
		}
	})
}

// TestQuestionErrorText checks the text of a refusal: the message alone when
// an issue only repeats it, and each other issue with its place.
func TestQuestionErrorText(t *testing.T) {
	tests := map[string]struct {
		questions string
		want      string
	}{
		"error: an empty set": {
			questions: `{}`,
			want:      "schema: At least one question is required.",
		},
		"error: too few criteria": {
			questions: `{"c":{"type":"choice","criteria":{"a":"x"}}}`,
			want:      "schema: Score and choice questions require at least two criteria.",
		},
		"error: two places": {
			questions: `{"q 1":{"type":"noul","instructions":3,"criteria":{"true":4}}}`,
			want: "schema: a question does not have the shape of its type; " +
				"q 1.instructions: must be a string, an object or an array; " +
				"q 1.criteria.true: must be a string, an object or an array",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := questionError(t, tt.questions).Error(); got != tt.want {
				t.Errorf("Error() = %q\nwant      %q", got, tt.want)
			}
		})
	}
}

// questionView is what a test reads back from a Question.
type questionView struct {
	ID           string
	Kind         string
	Labels       []string
	Instructions string
	Criteria     string
	Text         string
	Criteria0    []string
	Noul         []string
}

// view returns the parts of q a test compares. Instructions and Criteria are
// the raw values the question keeps, in the prompt spelling; Text is the
// prompt text of the instructions; Criteria0 are the prompt texts of the
// criteria, and Noul those of a Noul question that has a criteria object.
func view(t *testing.T, q *Question) questionView {
	t.Helper()
	raw := func(v jsonx.Node) string {
		text, err := v.PydanticJSON()
		if err != nil {
			t.Fatalf("PydanticJSON() error = %v", err)
		}
		return string(text)
	}
	v := questionView{
		ID: q.ID(), Kind: q.Kind().String(), Labels: q.Labels(),
		Instructions: raw(q.Instructions()), Criteria: raw(q.Criteria()),
		Text: q.instructionsText, Criteria0: q.criteriaTexts,
	}
	if q.hasCriteria {
		v.Noul = []string{q.trueText, q.falseText}
	}
	return v
}

// TestParseQuestions checks what an accepted question set gives: the order
// of the questions and of the labels, each question's kind, the raw
// instructions and criteria it keeps, and the prompt texts upstream's
// _serialize_instruction_value_for_prompt builds from them
// (_schema.py:250-255): a string as it is, an object or an array as
// pydantic-core's to_json writes it, and the fixed text for null or nothing.
func TestParseQuestions(t *testing.T) {
	const none = "No additional instructions."
	tests := map[string]struct {
		questions string
		want      []questionView
	}{
		"success: the three kinds in request order": {
			questions: `{"positive":{"type":"noul","instructions":"Is it positive?"},` +
				`"rating":{"type":"score","instructions":"How favorable?","criteria":["Bad.","Fair.","Good."]},` +
				`"genre":{"type":"choice","instructions":"Which genre?","criteria":{"fiction":"A novel.","nonfiction":null}}}`,
			want: []questionView{
				{ID: "positive", Kind: "noul", Instructions: `"Is it positive?"`, Criteria: `null`, Text: "Is it positive?"},
				{
					ID: "rating", Kind: "score", Labels: []string{"0", "1", "2"},
					Instructions: `"How favorable?"`, Criteria: `["Bad.","Fair.","Good."]`, Text: "How favorable?",
					Criteria0: []string{"Bad.", "Fair.", "Good."},
				},
				{
					ID: "genre", Kind: "choice", Labels: []string{"fiction", "nonfiction"},
					Instructions: `"Which genre?"`, Criteria: `{"fiction":"A novel.","nonfiction":null}`, Text: "Which genre?",
					Criteria0: []string{"A novel.", none},
				},
			},
		},
		"success: noul criteria in each form": {
			questions: `{"bare":{"type":"noul"},` +
				`"null":{"type":"noul","instructions":null,"criteria":null},` +
				`"empty":{"type":"noul","criteria":{}},` +
				`"true only":{"type":"noul","criteria":{"true":"T"}},` +
				`"false first":{"type":"noul","criteria":{"false":["F",1],"true":null}}}`,
			want: []questionView{
				{ID: "bare", Kind: "noul", Instructions: `null`, Criteria: `null`, Text: none},
				{ID: "null", Kind: "noul", Instructions: `null`, Criteria: `null`, Text: none},
				{ID: "empty", Kind: "noul", Instructions: `null`, Criteria: `{}`, Text: none, Noul: []string{none, none}},
				{ID: "true only", Kind: "noul", Instructions: `null`, Criteria: `{"true":"T"}`, Text: none, Noul: []string{"T", none}},
				{ID: "false first", Kind: "noul", Instructions: `null`, Criteria: `{"false":["F",1],"true":null}`, Text: none, Noul: []string{none, `["F",1]`}},
			},
		},
		"success: JSON content is written in pydantic-core's spelling": {
			questions: `{"q":{"type":"choice","instructions":{"z":1.50,"a":[1E5,1e-05,-0.0,3,true,null,"<b>"],"z":2.0},` +
				`"criteria":{"obj":{"b":1e16,"a":12345678901234567890123},"arr":[0.1,[1e-7]],"":"empty label"}}}`,
			want: []questionView{{
				ID: "q", Kind: "choice", Labels: []string{"obj", "arr", ""},
				Instructions: `{"z":2.0,"a":[100000.0,0.00001,-0.0,3,true,null,"<b>"]}`,
				Criteria:     `{"obj":{"b":1e+16,"a":12345678901234567890123},"arr":[0.1,[1e-7]],"":"empty label"}`,
				Text:         `{"z":2.0,"a":[100000.0,0.00001,-0.0,3,true,null,"<b>"]}`,
				Criteria0:    []string{`{"b":1e+16,"a":12345678901234567890123}`, `[0.1,[1e-7]]`, "empty label"},
			}},
		},
		"success: a repeated question or label keeps its first position and last value": {
			questions: `{"a":{"type":"noul"},"b":{"type":"noul","instructions":"b"},` +
				`"a":{"type":"choice","criteria":{"x":"1","y":"2","x":"3"}}}`,
			want: []questionView{
				{
					ID: "a", Kind: "choice", Labels: []string{"x", "y"},
					Instructions: `null`, Criteria: `{"x":"3","y":"2"}`, Text: none, Criteria0: []string{"3", "2"},
				},
				{ID: "b", Kind: "noul", Instructions: `"b"`, Criteria: `null`, Text: "b"},
			},
		},
		"success: members of a question in any order": {
			questions: `{"s":{"criteria":["0",{"one":1}],"instructions":["i"],"type":"score"}}`,
			want: []questionView{{
				ID: "s", Kind: "score", Labels: []string{"0", "1"},
				Instructions: `["i"]`, Criteria: `["0",{"one":1}]`, Text: `["i"]`, Criteria0: []string{"0", `{"one":1}`},
			}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			qs := mustParse(t, tt.questions)
			got := make([]questionView, len(qs))
			for i := range qs {
				got[i] = view(t, &qs[i])
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseQuestions mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestKindString checks the wire spelling of each kind and the text of a
// value that is none.
func TestKindString(t *testing.T) {
	tests := map[string]struct {
		kind Kind
		want string
	}{
		"success: noul":   {kind: Noul, want: "noul"},
		"success: choice": {kind: Choice, want: "choice"},
		"success: score":  {kind: Score, want: "score"},
		"error: no kind":  {kind: 0, want: "Kind(0)"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
