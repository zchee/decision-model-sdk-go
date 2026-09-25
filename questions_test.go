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
	"math"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// mustPrepare prepares qs and returns the "questions" bytes, failing the test
// when Prepare fails.
func mustPrepare(t *testing.T, qs *Questions) string {
	t.Helper()
	p, err := qs.Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return string(p.w.Questions)
}

// prepareError prepares qs, which must fail with a *ConfigError, and returns
// it.
func prepareError(t *testing.T, qs *Questions) *ConfigError {
	t.Helper()
	p, err := qs.Prepare()
	if err == nil {
		t.Fatalf("Prepare = %s, want an error", p.w.Questions)
	}
	if p != nil {
		t.Errorf("Prepare returned a set with its error: %s", p.w.Questions)
	}
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("Prepare error = %T %v, want a *ConfigError", err, err)
	}
	return ce
}

// js is JSON content from a string literal.
func js(s string) Content { return JSON([]byte(s)) }

// TestPreparedBytesMatchPython pins the "questions" bytes of question sets
// that typesafe-sdk-python 0.7.1 can express as well: want is what its
// to_json wrote for the same questions (probe of the upstream venv,
// pydantic-core 2.46.5, 2026-09-25 20:36:06 JST; the raw floats
// 2026-09-25 21:16:58 JST), member order and separators included.
func TestPreparedBytesMatchPython(t *testing.T) {
	var ctl strings.Builder
	for c := range 0x20 {
		ctl.WriteByte(byte(c))
	}
	// DEL, the HTML characters, U+00E9, U+2028, U+2029 and U+1F600 are
	// written as they are.
	tail := "\x7f<>&" + string(rune(0xe9)) + string(rune(0x2028)) + string(rune(0x2029)) + string(rune(0x1f600))
	escaped := `\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\b\t\n\u000b\f\r\u000e\u000f` +
		`\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001a\u001b\u001c\u001d\u001e\u001f` + tail

	tests := map[string]struct {
		questions *Questions
		want      string
	}{
		"success: the API sketch of the port plan": {
			questions: NewQuestions().
				Noul("billing", Noul{Instructions: Text("Is this about billing?"), Yes: Text("payments or invoices")}).
				Choice("tone", Choice{Instructions: Text("What is the tone?"), Options: Options{{"calm", Text("neutral or polite")}, {Label: "angry"}}}).
				Score("urgency", Score{Levels: []Content{Text("can wait"), Text("this week"), Text("today")}}).
				Raw("spam", RawQuestion{Type: "noul", Fields: map[string]any{"instructions": "Spam?"}}),
			want: `{"billing":{"type":"noul","instructions":"Is this about billing?","criteria":{"true":"payments or invoices"}},` +
				`"tone":{"type":"choice","instructions":"What is the tone?","criteria":{"calm":"neutral or polite","angry":null}},` +
				`"urgency":{"type":"score","criteria":["can wait","this week","today"]},"spam":{"type":"noul","instructions":"Spam?"}}`,
		},
		"success: string escapes in a name and in text": {
			questions: NewQuestions().Noul(`q"\/`+ctl.String()+tail, Noul{Instructions: Text(`a"b\c/d` + ctl.String() + tail)}),
			want:      `{"q\"\\/` + escaped + `":{"type":"noul","instructions":"a\"b\\c/d` + escaped + `"}}`,
		},
		"success: structured content everywhere, whitespace removed": {
			questions: NewQuestions().
				Noul("yes", Noul{Instructions: js(`[ "Read the message", { "context": null } ]`), Yes: js(`["Example", null]`)}).
				Choice("label", Choice{Instructions: js("{\n  \"text\": \"Classify\",\n  \"extra\": null\n}"), Options: Options{{"a", js(`["Example",null]`)}, {Label: "b"}}}).
				Score("rating", Score{Instructions: js(`[]`), Levels: []Content{js(`["Example",null]`), js(`{"extra":null}`), Text("plain")}}),
			want: `{"yes":{"type":"noul","instructions":["Read the message",{"context":null}],"criteria":{"true":["Example",null]}},` +
				`"label":{"type":"choice","instructions":{"text":"Classify","extra":null},"criteria":{"a":["Example",null],"b":null}},` +
				`"rating":{"type":"score","instructions":[],"criteria":[["Example",null],{"extra":null},"plain"]}}`,
		},
		"success: typed defaults": {
			questions: NewQuestions().
				Noul("a", Noul{}).
				Choice("b", Choice{Options: Options{{Label: "a"}}}).
				Score("c", Score{Levels: []Content{Text("good")}}),
			want: `{"a":{"type":"noul"},"b":{"type":"choice","criteria":{"a":null}},"c":{"type":"score","criteria":["good"]}}`,
		},
		"success: noul outcomes are written true then false": {
			questions: NewQuestions().
				Noul("both", Noul{Instructions: Text("Spam?"), Yes: Text("Yes"), No: Text("No")}).
				Noul("no-only", Noul{Instructions: Text("Spam?"), No: Text("No")}).
				Noul("rev", Noul{No: Text("No"), Instructions: Text("Spam?"), Yes: Text("Yes")}),
			want: `{"both":{"type":"noul","instructions":"Spam?","criteria":{"true":"Yes","false":"No"}},` +
				`"no-only":{"type":"noul","instructions":"Spam?","criteria":{"false":"No"}},` +
				`"rev":{"type":"noul","instructions":"Spam?","criteria":{"true":"Yes","false":"No"}}}`,
		},
		// The Python dictionaries were written in sorted key order, the
		// order a Go map is sent in.
		"success: raw questions": {
			questions: NewQuestions().
				Raw("raw", RawQuestion{Type: "noul", Fields: map[string]any{"criteria": map[string]any{"future": "kept"}, "instructions": "Spam?", "weight": 3}}).
				Raw("future", RawQuestion{Type: "future", Fields: map[string]any{"nested": map[string]any{"k": nil}}}).
				Raw("nulls", RawQuestion{Type: "noul", Fields: map[string]any{"criteria": nil, "instructions": nil}}),
			want: `{"raw":{"type":"noul","criteria":{"future":"kept"},"instructions":"Spam?","weight":3},` +
				`"future":{"type":"future","nested":{"k":null}},"nulls":{"type":"noul","criteria":null,"instructions":null}}`,
		},
		// R42: every float the lead's and the reviewer's sweeps named, laid
		// out as pydantic-core writes them (zmij: fixed notation for
		// exponents -5 to 15 with ".0" when integral, else e+NN / e-N).
		"success: raw floats": {
			questions: NewQuestions().Raw("floats", RawQuestion{Type: "future", Fields: map[string]any{"f": []any{
				1e-05, 9.99e-06, 0.0001, 0.1, 1.5, 3.0, -7.0, 123456789.0, 9999999999999998.0, 1e+16,
				1e+20, 1e+21, 1e+22, 5e-324, 1.7976931348623157e+308, math.Copysign(0, -1), 0.0,
				float64(1<<53 + 1), 1.2345678901234567e+19, 1.0, 1e-06, 1e-07, 1.5e-07, 1.5e+21, 1e+300,
				2.2250738585072014e-308, 1000000000000000.0, 5e-07, 999999999999999.9, 9.9999e-06,
				1.234e-05, 1.0000000000000002, 123.456, -1e-05, 2.5e-05, 2.225073858507201e-308, 1e+100,
				1e-100, 1.5e+300, 0.3, 1.5e-323,
			}}}),
			want: `{"floats":{"type":"future","f":[0.00001,9.99e-6,0.0001,0.1,1.5,3.0,-7.0,123456789.0,` +
				`9999999999999998.0,1e+16,1e+20,1e+21,1e+22,5e-324,1.7976931348623157e+308,-0.0,0.0,` +
				`9007199254740992.0,1.2345678901234567e+19,1.0,1e-6,1e-7,1.5e-7,1.5e+21,1e+300,` +
				`2.2250738585072014e-308,1000000000000000.0,5e-7,999999999999999.9,9.9999e-6,0.00001234,` +
				`1.0000000000000002,123.456,-0.00001,0.000025,2.225073858507201e-308,1e+100,1e-100,1.5e+300,0.3,` +
				`1.5e-323]}}`,
		},
		"success: raw scalars": {
			questions: NewQuestions().Raw("n", RawQuestion{Type: "future", Fields: map[string]any{
				"a": 3, "b": -7, "c": 1.5, "e": 1e21, "f": 1e-7, "i": true, "j": false, "u": uint64(math.MaxUint64),
			}}),
			want: `{"n":{"type":"future","a":3,"b":-7,"c":1.5,"e":1e+21,"f":1e-7,"i":true,"j":false,"u":18446744073709551615}}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := mustPrepare(t, tt.questions); got != tt.want {
				t.Errorf("questions =\n%s\nwant (Python's to_json)\n%s", got, tt.want)
			}
		})
	}
}

// TestTypedQuestionsWireForm ports test_normalization_preserves_objects:
// typed questions are sent in the order they were added, with their members
// in schema order, and preparing them leaves them unchanged.
func TestTypedQuestionsWireForm(t *testing.T) {
	tests := map[string]struct {
		noul      Noul
		choice    Choice
		score     Score
		want      string
		wantKinds []wire.Kind
	}{
		"success: one question of each kind": {
			noul:      Noul{Instructions: Text("Spam?")},
			choice:    Choice{Instructions: Text("Tone?"), Options: Options{{Label: "calm"}}},
			score:     Score{Instructions: Text("Quality?"), Levels: []Content{Text("bad"), Text("good")}},
			want:      `{"noul":{"type":"noul","instructions":"Spam?"},"choice":{"type":"choice","instructions":"Tone?","criteria":{"calm":null}},"score":{"type":"score","instructions":"Quality?","criteria":["bad","good"]}}`,
			wantKinds: []wire.Kind{wire.KindNoul, wire.KindChoice, wire.KindScore},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			before := []any{tt.noul, tt.choice, tt.score}
			qs := NewQuestions().Noul("noul", tt.noul).Choice("choice", tt.choice).Score("score", tt.score)
			p, err := qs.Prepare()
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if got := string(p.w.Questions); got != tt.want {
				t.Errorf("questions =\n%s\nwant\n%s", got, tt.want)
			}
			var kinds []wire.Kind
			for _, e := range p.w.Entries() {
				kinds = append(kinds, e.Kind)
			}
			if diff := gocmp.Diff(tt.wantKinds, kinds); diff != "" {
				t.Errorf("kinds mismatch (-want +got):\n%s", diff)
			}
			after := []any{qs.entries[0].noul, qs.entries[1].choice, qs.entries[2].score}
			if diff := gocmp.Diff(before, after, gocmp.AllowUnexported(Content{}, wire.Content{})); diff != "" {
				t.Errorf("Prepare changed the questions (-before +after):\n%s", diff)
			}
		})
	}
}

// TestRawQuestionsPassThrough ports test_normalization_preserves_raw_questions:
// a raw question is sent as its type and fields, whatever the type, next to a
// typed question, and preparing it leaves its fields unchanged. Fields are
// sent in sorted key order; Python sends a dictionary's insertion order, and
// the upstream test compares parsed JSON, so the order is not asserted there.
func TestRawQuestionsPassThrough(t *testing.T) {
	tests := map[string]struct {
		raw      RawQuestion
		wantRaw  string
		wantKind wire.Kind
	}{
		"success: a noul with extra members": {
			raw:      RawQuestion{Type: "noul", Fields: map[string]any{"instructions": "Spam?", "weight": 3, "criteria": map[string]any{"future": "kept"}}},
			wantRaw:  `{"type":"noul","criteria":{"future":"kept"},"instructions":"Spam?","weight":3}`,
			wantKind: wire.KindNoul,
		},
		"success: a type the SDK does not model": {
			raw:      RawQuestion{Type: "future", Fields: map[string]any{"nested": map[string]any{"k": nil}}},
			wantRaw:  `{"type":"future","nested":{"k":null}}`,
			wantKind: wire.KindUnknown,
		},
		"success: a score with an extra member": {
			raw:      RawQuestion{Type: "score", Fields: map[string]any{"criteria": []any{"good"}, "weight": 3}},
			wantRaw:  `{"type":"score","criteria":["good"],"weight":3}`,
			wantKind: wire.KindScore,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			before := copyFields(tt.raw.Fields)
			p, err := NewQuestions().Raw("raw", tt.raw).Noul("typed", Noul{Instructions: Text("Spam?")}).Prepare()
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if want := `{"raw":` + tt.wantRaw + `,"typed":{"type":"noul","instructions":"Spam?"}}`; string(p.w.Questions) != want {
				t.Errorf("questions =\n%s\nwant\n%s", p.w.Questions, want)
			}
			if got := p.w.Entries()[0].Kind; got != tt.wantKind {
				t.Errorf("raw kind = %v, want %v", got, tt.wantKind)
			}
			if diff := gocmp.Diff(before, tt.raw.Fields); diff != "" {
				t.Errorf("Prepare changed the fields (-before +after):\n%s", diff)
			}
		})
	}
}

// copyFields deep-copies the maps and slices of a raw question's fields.
func copyFields(fields map[string]any) map[string]any {
	var cp func(v any) any
	cp = func(v any) any {
		switch v := v.(type) {
		case map[string]any:
			m := make(map[string]any, len(v))
			for k, e := range v {
				m[k] = cp(e)
			}
			return m
		case []any:
			s := make([]any, len(v))
			for i, e := range v {
				s[i] = cp(e)
			}
			return s
		default:
			return v
		}
	}
	return cp(fields).(map[string]any)
}

// TestRawQuestionStructuralChecks ports
// test_raw_questions_require_structural_keys: a raw question needs a nonempty
// type, and a raw choice or score needs criteria, with the Python SDK's
// messages. Eight of the ten upstream cases have a direct Go form. The other
// two are not objects at all (the string "noul" and None), which Raw cannot
// be given; the zero RawQuestion stands for None, and a "type" field next to
// a valid Type, which Go rejects because Type is the one place a raw
// question's type is written, takes the string's place.
func TestRawQuestionStructuralChecks(t *testing.T) {
	const noType = `Question "invalid" must be a question object or a dictionary with a nonempty string "type".`
	const noCriteria = `Question "invalid" requires "criteria".`
	tests := map[string]struct {
		raw     RawQuestion
		wantMsg string
	}{
		"error: {}": {raw: RawQuestion{Fields: map[string]any{}}, wantMsg: noType},
		`error: {"instructions": "Missing type"}`: {raw: RawQuestion{Fields: map[string]any{"instructions": "Missing type"}}, wantMsg: noType},
		`error: {"type": "choice"}`:               {raw: RawQuestion{Type: "choice"}, wantMsg: noCriteria},
		`error: {"type": "score"}`:                {raw: RawQuestion{Type: "score", Fields: map[string]any{"instructions": "x"}}, wantMsg: noCriteria},
		`error: {"type": ""}`:                     {raw: RawQuestion{Type: ""}, wantMsg: noType},
		`error: {"type": None}`:                   {raw: RawQuestion{Fields: map[string]any{"type": nil}}, wantMsg: noType},
		`error: {"type": 1}`:                      {raw: RawQuestion{Fields: map[string]any{"type": 1}}, wantMsg: noType},
		`error: {"type": ["future"]}`:             {raw: RawQuestion{Fields: map[string]any{"type": []any{"future"}}}, wantMsg: noType},
		"error: None, the zero RawQuestion":       {raw: RawQuestion{}, wantMsg: noType},
		`error: a "type" field next to Type (Go)`: {raw: RawQuestion{Type: "noul", Fields: map[string]any{"type": "noul"}}, wantMsg: noType},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := prepareError(t, NewQuestions().Raw("invalid", tt.raw))
			if err.Error() != tt.wantMsg {
				t.Errorf("error = %q, want %q", err, tt.wantMsg)
			}
			if errs := err.Unwrap(); errs != nil {
				t.Errorf("Unwrap() = %v, want nil: the rule itself is the cause", errs)
			}
		})
	}
}

// TestUnsetMembersLeftOffWire ports
// test_direct_encoding_omits_only_default_fields: an unset member is left
// off, a member set to empty text or an empty array is sent. The Python SDK
// also sends an empty criteria object and a null outcome, which a typed Noul
// leaves off (Appendix B: "Typed noul sends null outcomes / empty criteria":
// left out, same meaning; a RawQuestion sends those shapes).
func TestUnsetMembersLeftOffWire(t *testing.T) {
	tests := map[string]struct {
		add    func(qs *Questions) *Questions
		want   string
		python string // Python's bytes, where the Go form deviates
	}{
		"success: Noul()": {
			add:  func(qs *Questions) *Questions { return qs.Noul("q", Noul{}) },
			want: `{"q":{"type":"noul"}}`,
		},
		`success: Choice(criteria={"a": None})`: {
			add:  func(qs *Questions) *Questions { return qs.Choice("q", Choice{Options: Options{{Label: "a"}}}) },
			want: `{"q":{"type":"choice","criteria":{"a":null}}}`,
		},
		`success: Score(criteria=["good"])`: {
			add:  func(qs *Questions) *Questions { return qs.Score("q", Score{Levels: []Content{Text("good")}}) },
			want: `{"q":{"type":"score","criteria":["good"]}}`,
		},
		`deviation: Noul(instructions="", criteria={})`: {
			add:    func(qs *Questions) *Questions { return qs.Noul("q", Noul{Instructions: Text("")}) },
			want:   `{"q":{"type":"noul","instructions":""}}`,
			python: `{"q":{"type":"noul","instructions":"","criteria":{}}}`,
		},
		`deviation: Noul(instructions=[], criteria={"true": None})`: {
			add:    func(qs *Questions) *Questions { return qs.Noul("q", Noul{Instructions: js(`[]`)}) },
			want:   `{"q":{"type":"noul","instructions":[]}}`,
			python: `{"q":{"type":"noul","instructions":[],"criteria":{"true":null}}}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := mustPrepare(t, tt.add(NewQuestions())); got != tt.want {
				t.Errorf("questions = %s, want %s (Python: %s)", got, tt.want, tt.python)
			}
		})
	}
}

// TestEachKindWritesItsTypeTag ports test_discriminators_are_automatic: each
// typed question writes its own "type" without the caller naming it, and its
// members stay assignable after construction. The Go counterpart of assigning
// question.instructions goes through the public API: the caller changes the
// question value it holds and adds it to a new set. The set it was added to
// before keeps the old value, since adding a question copies it.
func TestEachKindWritesItsTypeTag(t *testing.T) {
	tests := map[string]struct {
		// sets adds a question built with the instructions "Spam?" to a
		// first set, assigns "Updated?" to the same question variable's
		// Instructions and adds it to a second set.
		sets     func() (first, second *Questions)
		tag      string
		wantKind wire.Kind
	}{
		"success: noul": {
			sets: func() (first, second *Questions) {
				q := Noul{Instructions: Text("Spam?")}
				first = NewQuestions().Noul("q", q)
				q.Instructions = Text("Updated?")
				return first, NewQuestions().Noul("q", q)
			},
			tag:      "noul",
			wantKind: wire.KindNoul,
		},
		"success: choice": {
			sets: func() (first, second *Questions) {
				q := Choice{Instructions: Text("Spam?"), Options: Options{{Label: "calm"}}}
				first = NewQuestions().Choice("q", q)
				q.Instructions = Text("Updated?")
				return first, NewQuestions().Choice("q", q)
			},
			tag:      "choice",
			wantKind: wire.KindChoice,
		},
		"success: score": {
			sets: func() (first, second *Questions) {
				q := Score{Instructions: Text("Spam?"), Levels: []Content{Text("good")}}
				first = NewQuestions().Score("q", q)
				q.Instructions = Text("Updated?")
				return first, NewQuestions().Score("q", q)
			},
			tag:      "score",
			wantKind: wire.KindScore,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			first, second := tt.sets()
			p, err := first.Prepare()
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if got := p.w.Entries()[0].Kind; got != tt.wantKind {
				t.Errorf("kind = %v, want %v", got, tt.wantKind)
			}
			for _, c := range []struct {
				qs           *Questions
				instructions string
			}{{first, "Spam?"}, {second, "Updated?"}} {
				prefix := `{"q":{"type":"` + tt.tag + `","instructions":"` + c.instructions + `"`
				if got := mustPrepare(t, c.qs); !strings.HasPrefix(got, prefix) {
					t.Errorf("questions = %s, want the prefix %s", got, prefix)
				}
			}
		})
	}
}

// TestNoulCriteriaShapes ports test_optional_noul_criteria: six criteria
// shapes, each as a typed Noul and as a raw question. A typed Noul cannot
// send an empty criteria object (Appendix B: "Typed noul sends null
// outcomes / empty criteria": left out, same meaning), so the typed {} case
// sends none; the raw case sends it. Raw fields are sent in sorted key order,
// so "criteria" precedes "instructions" there, and the structured outcome's
// members are sorted too; the upstream test compares parsed JSON.
func TestNoulCriteriaShapes(t *testing.T) {
	structured := `{"summary":"Unsolicited","examples":["Buy now"]}`
	tests := map[string]struct {
		q    func(qs *Questions) *Questions
		want string
	}{
		"success: typed, no criteria": {
			q:    func(qs *Questions) *Questions { return qs.Noul("q", Noul{Instructions: Text("Spam?")}) },
			want: `{"type":"noul","instructions":"Spam?"}`,
		},
		"deviation: typed, empty criteria": {
			q:    func(qs *Questions) *Questions { return qs.Noul("q", Noul{Instructions: Text("Spam?")}) },
			want: `{"type":"noul","instructions":"Spam?"}`,
		},
		"success: typed, yes only": {
			q: func(qs *Questions) *Questions {
				return qs.Noul("q", Noul{Instructions: Text("Spam?"), Yes: Text("Yes")})
			},
			want: `{"type":"noul","instructions":"Spam?","criteria":{"true":"Yes"}}`,
		},
		"success: typed, no only": {
			q:    func(qs *Questions) *Questions { return qs.Noul("q", Noul{Instructions: Text("Spam?"), No: Text("No")}) },
			want: `{"type":"noul","instructions":"Spam?","criteria":{"false":"No"}}`,
		},
		"success: typed, both": {
			q: func(qs *Questions) *Questions {
				return qs.Noul("q", Noul{Instructions: Text("Spam?"), Yes: Text("Yes"), No: Text("No")})
			},
			want: `{"type":"noul","instructions":"Spam?","criteria":{"true":"Yes","false":"No"}}`,
		},
		"success: typed, structured yes": {
			q: func(qs *Questions) *Questions {
				return qs.Noul("q", Noul{Instructions: Text("Spam?"), Yes: js(structured)})
			},
			want: `{"type":"noul","instructions":"Spam?","criteria":{"true":` + structured + `}}`,
		},
		"success: raw, no criteria": {
			q:    func(qs *Questions) *Questions { return qs.Raw("q", rawNoul(nil)) },
			want: `{"type":"noul","instructions":"Spam?"}`,
		},
		"success: raw, empty criteria": {
			q:    func(qs *Questions) *Questions { return qs.Raw("q", rawNoul(map[string]any{})) },
			want: `{"type":"noul","criteria":{},"instructions":"Spam?"}`,
		},
		"success: raw, yes only": {
			q:    func(qs *Questions) *Questions { return qs.Raw("q", rawNoul(map[string]any{"true": "Yes"})) },
			want: `{"type":"noul","criteria":{"true":"Yes"},"instructions":"Spam?"}`,
		},
		"success: raw, no only": {
			q:    func(qs *Questions) *Questions { return qs.Raw("q", rawNoul(map[string]any{"false": "No"})) },
			want: `{"type":"noul","criteria":{"false":"No"},"instructions":"Spam?"}`,
		},
		"success: raw, both": {
			q: func(qs *Questions) *Questions {
				return qs.Raw("q", rawNoul(map[string]any{"true": "Yes", "false": "No"}))
			},
			want: `{"type":"noul","criteria":{"false":"No","true":"Yes"},"instructions":"Spam?"}`,
		},
		"success: raw, structured yes": {
			q: func(qs *Questions) *Questions {
				return qs.Raw("q", rawNoul(map[string]any{"true": map[string]any{"summary": "Unsolicited", "examples": []any{"Buy now"}}}))
			},
			want: `{"type":"noul","criteria":{"true":{"examples":["Buy now"],"summary":"Unsolicited"}},"instructions":"Spam?"}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got, want := mustPrepare(t, tt.q(NewQuestions())), `{"q":`+tt.want+`}`; got != want {
				t.Errorf("questions = %s, want %s", got, want)
			}
		})
	}
}

// rawNoul is a raw noul with the instructions "Spam?" and, when criteria is
// not nil, those criteria.
func rawNoul(criteria map[string]any) RawQuestion {
	fields := map[string]any{"instructions": "Spam?"}
	if criteria != nil {
		fields["criteria"] = criteria
	}
	return RawQuestion{Type: "noul", Fields: fields}
}

// TestScoreWithoutLevelsRejected ports test_empty_score_criteria_is_rejected:
// a score without levels fails, typed or raw, with the Python SDK's message.
// The raw rule is Python's truthiness, so every empty or zero criteria value
// fails the same way.
func TestScoreWithoutLevelsRejected(t *testing.T) {
	const want = `Score question "rating" has no criteria; at least one score is required.`
	rawScore := func(criteria any) RawQuestion {
		return RawQuestion{Type: "score", Fields: map[string]any{"instructions": "Quality?", "criteria": criteria}}
	}
	tests := map[string]struct {
		add func(qs *Questions) *Questions
	}{
		"error: typed, no levels": {
			add: func(qs *Questions) *Questions { return qs.Score("rating", Score{Instructions: Text("Quality?")}) },
		},
		"error: typed, an empty level list": {
			add: func(qs *Questions) *Questions {
				return qs.Score("rating", Score{Instructions: Text("Quality?"), Levels: []Content{}})
			},
		},
		"error: raw, an empty list":        {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore([]any{})) }},
		"error: raw, null criteria":        {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(nil)) }},
		"error: raw, an empty []string":    {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore([]string{})) }},
		"error: raw, an empty map":         {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(map[string]any{})) }},
		"error: raw, an empty string map":  {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(map[string]string{})) }},
		"error: raw, an empty string":      {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore("")) }},
		"error: raw, false":                {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(false)) }},
		"error: raw, integer zero":         {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(0)) }},
		"error: raw, float zero":           {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(0.0)) }},
		"error: raw, RawJSON of []":        {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(RawJSON(` [ ] `))) }},
		"error: raw, RawJSON of a zero":    {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(RawJSON(`-0.0e5`))) }},
		"error: raw, unset Content":        {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(Content{})) }},
		"error: raw, empty text Content":   {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(Text(""))) }},
		"error: raw, empty JSON Content":   {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(js(`{}`))) }},
		"error: raw, int8 zero":            {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(int8(0))) }},
		"error: raw, int16 zero":           {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(int16(0))) }},
		"error: raw, int32 zero":           {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(int32(0))) }},
		"error: raw, int64 zero":           {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(int64(0))) }},
		"error: raw, uint zero":            {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(uint(0))) }},
		"error: raw, uint8 zero":           {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(uint8(0))) }},
		"error: raw, uint16 zero":          {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(uint16(0))) }},
		"error: raw, uint32 zero":          {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(uint32(0))) }},
		"error: raw, uint64 zero":          {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(uint64(0))) }},
		"error: raw, float32 zero":         {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(float32(0))) }},
		"error: raw, RawJSON of 0":         {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(RawJSON(`0`))) }},
		"error: raw, RawJSON of -0.000":    {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(RawJSON(`-0.000`))) }},
		"error: raw, RawJSON null":         {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(RawJSON(`null`))) }},
		"error: raw, RawJSON empty string": {add: func(qs *Questions) *Questions { return qs.Raw("rating", rawScore(RawJSON(`""`))) }},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if err := prepareError(t, tt.add(NewQuestions())); err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
		})
	}
}

// TestRawScoreCriteriaThatAreNotEmpty checks the other side of the
// truthiness rule of TestScoreWithoutLevelsRejected: a raw score whose
// criteria Python finds true is prepared, whatever the server makes of it.
func TestRawScoreCriteriaThatAreNotEmpty(t *testing.T) {
	tests := map[string]struct {
		criteria any
		want     string
	}{
		"success: a one-level list":   {criteria: []any{"good"}, want: `["good"]`},
		"success: a nonzero number":   {criteria: 2, want: `2`},
		"success: true":               {criteria: true, want: `true`},
		"success: a nonzero RawJSON":  {criteria: RawJSON(`0.5`), want: `0.5`},
		"success: a RawJSON string":   {criteria: RawJSON(`"a"`), want: `"a"`},
		"success: a RawJSON list":     {criteria: RawJSON(`[ "a" ]`), want: `["a"]`},
		"success: RawJSON true":       {criteria: RawJSON(`true`), want: `true`},
		"success: a text Content":     {criteria: Text("a"), want: `"a"`},
		"success: a JSON Content":     {criteria: js(`["a"]`), want: `["a"]`},
		"success: a nonzero exponent": {criteria: RawJSON(`1e-3`), want: `1e-3`},
		"success: a nonzero fraction": {criteria: RawJSON(`0.01`), want: `0.01`},
		"success: nonzero kinds":      {criteria: []any{int16(1), uint(2), float32(0.5)}, want: `[1,2,0.5]`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := mustPrepare(t, NewQuestions().Raw("r", RawQuestion{Type: "score", Fields: map[string]any{"criteria": tt.criteria}}))
			if want := `{"r":{"type":"score","criteria":` + tt.want + `}}`; got != want {
				t.Errorf("questions = %s, want %s", got, want)
			}
		})
	}
}

// TestMixedQuestionMapsThroughOneBuilder ports
// test_covariant_question_mappings. Python checks that nine mapping types
// (typed, raw, read-only, mixed) all type-check as questions and are sent;
// Go has one builder that takes every kind, so each of the nine sets is built
// with it and prepared, and the question values it was given stay unchanged.
// Sending them is the client's part (W2.3).
func TestMixedQuestionMapsThroughOneBuilder(t *testing.T) {
	noul := Noul{Instructions: Text("Spam?")}
	choice := Choice{Instructions: Text("Tone?"), Options: Options{{Label: "calm"}}}
	score := Score{Instructions: Text("Quality?"), Levels: []Content{Text("good")}}
	rawNoul := RawQuestion{Type: "noul", Fields: map[string]any{"instructions": "Spam?"}}
	rawChoice := RawQuestion{Type: "choice", Fields: map[string]any{"instructions": "Tone?", "criteria": map[string]any{"calm": nil}}}
	rawScore := RawQuestion{Type: "score", Fields: map[string]any{"instructions": "Quality?", "criteria": []any{"good"}}}

	const (
		noulJSON      = `{"type":"noul","instructions":"Spam?"}`
		choiceJSON    = `{"type":"choice","instructions":"Tone?","criteria":{"calm":null}}`
		scoreJSON     = `{"type":"score","instructions":"Quality?","criteria":["good"]}`
		rawChoiceJSON = `{"type":"choice","criteria":{"calm":null},"instructions":"Tone?"}`
		rawScoreJSON  = `{"type":"score","criteria":["good"],"instructions":"Quality?"}`
	)
	tests := map[string]struct {
		qs   *Questions
		want string
	}{
		"success: nouls":       {qs: NewQuestions().Noul("q", noul), want: `{"q":` + noulJSON + `}`},
		"success: choices":     {qs: NewQuestions().Choice("q", choice), want: `{"q":` + choiceJSON + `}`},
		"success: scores":      {qs: NewQuestions().Score("q", score), want: `{"q":` + scoreJSON + `}`},
		"success: raw nouls":   {qs: NewQuestions().Raw("q", rawNoul), want: `{"q":` + noulJSON + `}`},
		"success: raw choices": {qs: NewQuestions().Raw("q", rawChoice), want: `{"q":` + rawChoiceJSON + `}`},
		"success: raw scores":  {qs: NewQuestions().Raw("q", rawScore), want: `{"q":` + rawScoreJSON + `}`},
		// A read-only view of the choices mapping in Python; in Go the same
		// Choice value, added to a second set.
		"success: the same choice in another set": {qs: NewQuestions().Choice("q", choice), want: `{"q":` + choiceJSON + `}`},
		"success: mixed": {
			qs:   NewQuestions().Noul("one", noul).Raw("two", rawChoice).Score("three", score),
			want: `{"one":` + noulJSON + `,"two":` + rawChoiceJSON + `,"three":` + scoreJSON + `}`,
		},
		"success: an inline raw choice": {
			qs:   NewQuestions().Raw("q", RawQuestion{Type: "choice", Fields: map[string]any{"instructions": "Tone?", "criteria": map[string]any{"calm": nil}}}),
			want: `{"q":` + rawChoiceJSON + `}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := mustPrepare(t, tt.qs); got != tt.want {
				t.Errorf("questions = %s, want %s", got, tt.want)
			}
		})
	}
	if diff := gocmp.Diff([]Content{Text("good")}, score.Levels, gocmp.AllowUnexported(Content{}, wire.Content{})); diff != "" {
		t.Errorf("score levels changed (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff([]any{"good"}, rawScore.Fields["criteria"]); diff != "" {
		t.Errorf("raw score criteria changed (-want +got):\n%s", diff)
	}
}

// TestRawQuestionKeepsExplicitNull ports
// test_raw_optional_fields_preserve_explicit_null: a raw field set to nil is
// sent as null, not left off.
func TestRawQuestionKeepsExplicitNull(t *testing.T) {
	tests := map[string]struct {
		qs   *Questions
		want string
	}{
		"success: null instructions and criteria of every kind": {
			qs: NewQuestions().
				Raw("yes", RawQuestion{Type: "noul", Fields: map[string]any{"instructions": nil, "criteria": nil}}).
				Raw("label", RawQuestion{Type: "choice", Fields: map[string]any{"instructions": nil, "criteria": map[string]any{"a": nil}}}).
				Raw("rating", RawQuestion{Type: "score", Fields: map[string]any{"instructions": nil, "criteria": []any{"good"}}}),
			want: `{"yes":{"type":"noul","criteria":null,"instructions":null},` +
				`"label":{"type":"choice","criteria":{"a":null},"instructions":null},` +
				`"rating":{"type":"score","criteria":["good"],"instructions":null}}`,
		},
		"success: an unset Content field is null": {
			qs:   NewQuestions().Raw("yes", RawQuestion{Type: "noul", Fields: map[string]any{"instructions": Content{}, "criteria": map[string]any{"true": Text("Yes")}}}),
			want: `{"yes":{"type":"noul","criteria":{"true":"Yes"},"instructions":null}}`,
		},
		"success: a null raw choice criteria passes the SDK's check": {
			qs:   NewQuestions().Raw("label", RawQuestion{Type: "choice", Fields: map[string]any{"criteria": nil}}),
			want: `{"label":{"type":"choice","criteria":null}}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := mustPrepare(t, tt.qs); got != tt.want {
				t.Errorf("questions =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// TestArrayContentEverywhere is the question half of test_array_inputs:
// arrays with nested nulls as instructions, as an outcome or option
// description and as a score level, typed and raw. The state half, an array
// state, needs the request encoder (W1.2), which extends this test. A typed
// Noul leaves the null "false" outcome off (Appendix B: "Typed noul sends
// null outcomes / empty criteria"); the raw form sends it.
func TestArrayContentEverywhere(t *testing.T) {
	const (
		instructions = `["Read the message",{"context":null}]`
		description  = `["Example",null]`
	)
	instructionsValue := []any{"Read the message", map[string]any{"context": nil}}
	descriptionValue := []any{"Example", nil}
	tests := map[string]struct {
		qs   *Questions
		want string
	}{
		"success: typed": {
			qs: NewQuestions().
				Noul("yes", Noul{Instructions: js(instructions), Yes: js(description)}).
				Choice("label", Choice{Instructions: js(instructions), Options: Options{{"a", js(description)}, {Label: "b"}}}).
				Score("rating", Score{Instructions: js(instructions), Levels: []Content{js(description)}}),
			want: `{"yes":{"type":"noul","instructions":` + instructions + `,"criteria":{"true":` + description + `}},` +
				`"label":{"type":"choice","instructions":` + instructions + `,"criteria":{"a":` + description + `,"b":null}},` +
				`"rating":{"type":"score","instructions":` + instructions + `,"criteria":[` + description + `]}}`,
		},
		"success: raw": {
			qs: NewQuestions().
				Raw("yes", RawQuestion{Type: "noul", Fields: map[string]any{"instructions": instructionsValue, "criteria": map[string]any{"true": descriptionValue, "false": nil}}}).
				Raw("label", RawQuestion{Type: "choice", Fields: map[string]any{"instructions": instructionsValue, "criteria": map[string]any{"a": descriptionValue, "b": nil}}}).
				Raw("rating", RawQuestion{Type: "score", Fields: map[string]any{"instructions": instructionsValue, "criteria": []any{descriptionValue}}}),
			want: `{"yes":{"type":"noul","criteria":{"false":null,"true":` + description + `},"instructions":` + instructions + `},` +
				`"label":{"type":"choice","criteria":{"a":` + description + `,"b":null},"instructions":` + instructions + `},` +
				`"rating":{"type":"score","criteria":[` + description + `],"instructions":` + instructions + `}}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := mustPrepare(t, tt.qs); got != tt.want {
				t.Errorf("questions =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// TestNullInsideContentSurvives is the question half of
// test_explicitly_nullable_json_values: a null nested inside structured
// content is sent. The state half needs the request encoder (W1.2), which
// extends this test.
func TestNullInsideContentSurvives(t *testing.T) {
	const instructions = `{"text":"Classify","extra":null}`
	tests := map[string]struct {
		qs   *Questions
		want string
	}{
		"success: typed questions of every kind": {
			qs: NewQuestions().
				Noul("yes", Noul{Instructions: js(instructions), Yes: js(`{"extra":null}`)}).
				Choice("label", Choice{Instructions: js(instructions), Options: Options{{Label: "a"}, {"b", js(`{"extra":null}`)}}}).
				Score("rating", Score{Instructions: js(instructions), Levels: []Content{js(`{"extra":null}`)}}),
			want: `{"yes":{"type":"noul","instructions":` + instructions + `,"criteria":{"true":{"extra":null}}},` +
				`"label":{"type":"choice","instructions":` + instructions + `,"criteria":{"a":null,"b":{"extra":null}}},` +
				`"rating":{"type":"score","instructions":` + instructions + `,"criteria":[{"extra":null}]}}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := mustPrepare(t, tt.qs); got != tt.want {
				t.Errorf("questions =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// TestPrepareRejects covers the failures Prepare adds to the Python SDK's
// four rules: a repeated name (Appendix B: "Duplicate question names keep
// the last": a Go set rejects the repeat), a repeated option label, an unset
// level, and content or values that cannot be written. The first problem in
// the order the questions were added is the one reported.
func TestPrepareRejects(t *testing.T) {
	many := func(n, repeatAt int) *Questions {
		qs := NewQuestions()
		for i := range n {
			name := "q" + strconv.Itoa(i)
			if i == repeatAt {
				name = "q0"
			}
			qs.Noul(name, Noul{})
		}
		return qs
	}
	manyOptions := make(Options, repeatScanLimit+3)
	for i := range manyOptions {
		manyOptions[i] = Option{Label: "o" + strconv.Itoa(i)}
	}
	manyOptions[len(manyOptions)-1].Label = "o1"
	cycle := map[string]any{}
	cycle["self"] = cycle

	tests := map[string]struct {
		qs        *Questions
		wantMsg   string
		wantCause error // matched with errors.Is, when set
		syntax    bool  // the cause is a *wire.SyntaxError
	}{
		"error: an empty set":                        {qs: NewQuestions(), wantMsg: "At least one question is required."},
		"error: the zero Questions":                  {qs: &Questions{}, wantMsg: "At least one question is required."},
		"error: a repeated name":                     {qs: NewQuestions().Noul("tone", Noul{}).Choice("tone", Choice{}), wantMsg: `Question "tone" is added more than once; question names must be unique.`},
		"error: a repeated name at the scan limit":   {qs: many(repeatScanLimit, repeatScanLimit-1), wantMsg: `Question "q0" is added more than once; question names must be unique.`},
		"error: a repeated name past the scan limit": {qs: many(repeatScanLimit+5, repeatScanLimit+3), wantMsg: `Question "q0" is added more than once; question names must be unique.`},
		"error: the empty name twice":                {qs: NewQuestions().Noul("", Noul{}).Noul("", Noul{}), wantMsg: `Question "" is added more than once; question names must be unique.`},
		"error: a name that is not UTF-8":            {qs: NewQuestions().Noul("a\xff", Noul{}), wantMsg: `Question name "a\xff" is not valid UTF-8.`},
		"error: a repeated option label": {
			qs:      NewQuestions().Choice("tone", Choice{Options: Options{{Label: "calm"}, {Label: "angry"}, {"calm", Text("again")}}}),
			wantMsg: `Choice question "tone" has option "calm" more than once; option labels must be unique.`,
		},
		"error: a repeated option label past the scan limit": {
			qs:      NewQuestions().Choice("tone", Choice{Options: manyOptions}),
			wantMsg: `Choice question "tone" has option "o1" more than once; option labels must be unique.`,
		},
		"error: an unset level": {
			qs:      NewQuestions().Score("rating", Score{Levels: []Content{Text("low"), {}, Text("high")}}),
			wantMsg: `Score question "rating" level 1 is unset; every level needs text or JSON content.`,
		},
		"error: JSON instructions holding a string": {
			qs:        NewQuestions().Noul("q", Noul{Instructions: js(`"text"`)}),
			wantMsg:   `Question "q": instructions: JSON content must be an object or an array`,
			wantCause: wire.ErrContentShape,
		},
		"error: JSON(nil) is not an object": {
			qs:        NewQuestions().Noul("q", Noul{Yes: JSON(nil)}),
			wantMsg:   `Question "q": criteria.true: JSON content must be an object or an array`,
			wantCause: wire.ErrContentShape,
		},
		"error: invalid JSON as a level": {
			qs:      NewQuestions().Score("q", Score{Levels: []Content{Text("a"), js(`{"a":}`)}}),
			wantMsg: `Question "q": criteria[1]: invalid JSON at byte 5: unexpected "}", want a value`,
			syntax:  true,
		},
		"error: an option description that is not UTF-8": {
			qs:        NewQuestions().Choice("q", Choice{Options: Options{{"calm", Text("\xff")}}}),
			wantMsg:   `Question "q": criteria.calm: string is not valid UTF-8`,
			wantCause: wire.ErrInvalidUTF8,
		},
		"error: choice instructions that are not UTF-8": {
			qs:        NewQuestions().Choice("q", Choice{Instructions: Text("\xff")}),
			wantMsg:   `Question "q": instructions: string is not valid UTF-8`,
			wantCause: wire.ErrInvalidUTF8,
		},
		"error: a raw type that is not UTF-8": {
			qs:        NewQuestions().Raw("q", RawQuestion{Type: "\xff"}),
			wantMsg:   "Question \"q\": type: string is not valid UTF-8",
			wantCause: wire.ErrInvalidUTF8,
		},
		"error: a raw field of an unsupported type": {
			qs:        NewQuestions().Raw("q", RawQuestion{Type: "noul", Fields: map[string]any{"weight": []int{1}}}),
			wantMsg:   `Question "q": weight: unsupported value of type []int`,
			wantCause: wire.ErrUnsupportedValue,
		},
		"error: a NaN raw field": {
			qs:        NewQuestions().Raw("q", RawQuestion{Type: "noul", Fields: map[string]any{"weight": math.NaN()}}),
			wantMsg:   `Question "q": weight: unsupported value: NaN is not a JSON number`,
			wantCause: wire.ErrUnsupportedValue,
		},
		"error: a nested unsupported value": {
			qs:        NewQuestions().Raw("q", RawQuestion{Type: "noul", Fields: map[string]any{"criteria": map[string]any{"a": []any{1, struct{}{}}}}}),
			wantMsg:   `Question "q": criteria.a[1]: unsupported value of type struct {}`,
			wantCause: wire.ErrUnsupportedValue,
		},
		"error: an invalid RawJSON field": {
			qs:      NewQuestions().Raw("q", RawQuestion{Type: "noul", Fields: map[string]any{"criteria": RawJSON(`{"a" 1}`)}}),
			wantMsg: `Question "q": criteria: invalid JSON at byte 5: want ':' after a member name`,
			syntax:  true,
		},
		"error: an invalid RawJSON as raw score criteria is reported by the writer": {
			qs:      NewQuestions().Raw("q", RawQuestion{Type: "score", Fields: map[string]any{"criteria": RawJSON(`[`)}}),
			wantMsg: `Question "q": criteria: invalid JSON at byte 1: unexpected end of input, want a value`,
			syntax:  true,
		},
		"error: raw score criteria of an unsupported type are reported by the writer": {
			qs:        NewQuestions().Raw("q", RawQuestion{Type: "score", Fields: map[string]any{"criteria": []int{}}}),
			wantMsg:   `Question "q": criteria: unsupported value of type []int`,
			wantCause: wire.ErrUnsupportedValue,
		},
		"error: a Content field holding a JSON number": {
			qs:        NewQuestions().Raw("q", RawQuestion{Type: "noul", Fields: map[string]any{"instructions": js(`1`)}}),
			wantMsg:   `Question "q": instructions: JSON content must be an object or an array`,
			wantCause: wire.ErrContentShape,
		},
		"error: a cyclic raw field": {
			qs:        NewQuestions().Raw("q", RawQuestion{Type: "noul", Fields: map[string]any{"c": cycle}}),
			wantMsg:   `Question "q": c` + strings.Repeat(".self", 1001) + `: unsupported value: nested more than 1000 levels deep (a cycle?)`,
			wantCause: wire.ErrUnsupportedValue,
		},
		"error: the first failing question is reported": {
			qs: NewQuestions().
				Noul("ok", Noul{}).
				Noul("bad", Noul{Instructions: js(`1`)}).
				Noul("ok", Noul{}),
			wantMsg:   `Question "bad": instructions: JSON content must be an object or an array`,
			wantCause: wire.ErrContentShape,
		},
		"error: a repeat before a later failure is reported first": {
			qs: NewQuestions().
				Noul("a", Noul{}).
				Noul("a", Noul{}).
				Noul("bad", Noul{Instructions: js(`1`)}),
			wantMsg: `Question "a" is added more than once; question names must be unique.`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := prepareError(t, tt.qs)
			if err.Error() != tt.wantMsg {
				t.Errorf("error = %q, want %q", err, tt.wantMsg)
			}
			if tt.wantCause != nil && !errors.Is(err, tt.wantCause) {
				t.Errorf("errors.Is(%v, %v) = false", err, tt.wantCause)
			}
			if tt.syntax {
				if _, ok := errors.AsType[*wire.SyntaxError](err); !ok {
					t.Errorf("errors.As(%v, *wire.SyntaxError) = false", err)
				}
			}
			if tt.wantCause == nil && !tt.syntax && err.Unwrap() != nil {
				t.Errorf("Unwrap() = %v, want nil", err.Unwrap())
			}
		})
	}
}
