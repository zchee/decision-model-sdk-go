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
	"reflect"
	"sync"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// Ticket is the typed question set of the port plan's section 5.
type Ticket struct {
	Billing NoulAnswer   `typesafe:"kind=noul;instructions=Is this about billing, invoices or refunds?;yes=payments or invoices"`
	Tone    ChoiceAnswer `typesafe:"kind=choice;instructions=What is the tone?;options=calm=neutral or polite|angry"`
	Urgency ScoreAnswer  `typesafe:"kind=score;instructions=How urgent?;levels=can wait|this week|today"`
	Spam    NoulAnswer   `typesafe:"kind=noul;optional;instructions=Spam?"`
}

// ticketByHand is the question set Ticket declares, built by hand.
func ticketByHand() *Questions {
	return NewQuestions().
		Noul("Billing", Noul{Instructions: Text("Is this about billing, invoices or refunds?"), Yes: Text("payments or invoices")}).
		Choice("Tone", Choice{Instructions: Text("What is the tone?"), Options: Options{{Label: "calm", Description: Text("neutral or polite")}, {Label: "angry"}}}).
		Score("Urgency", Score{Instructions: Text("How urgent?"), Levels: []Content{Text("can wait"), Text("this week"), Text("today")}}).
		Noul("Spam", Noul{Instructions: Text("Spam?")})
}

// planOptions compares typed plans field by field.
var planOptions = gocmp.AllowUnexported(typedField{}, tagSpec{}, tagOption{})

// TestPreparedForTicketParity pins that the typed path and the builder
// agree: PreparedFor[Ticket] sends the bytes of the same set built by hand,
// records the same tables, and its plan maps each field to its question.
func TestPreparedForTicketParity(t *testing.T) {
	got, err := PreparedFor[Ticket]()
	if err != nil {
		t.Fatalf("PreparedFor[Ticket]: %v", err)
	}
	want, err := ticketByHand().Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	const wantBytes = `{"Billing":{"type":"noul","instructions":"Is this about billing, invoices or refunds?","criteria":{"true":"payments or invoices"}},` +
		`"Tone":{"type":"choice","instructions":"What is the tone?","criteria":{"calm":"neutral or polite","angry":null}},` +
		`"Urgency":{"type":"score","instructions":"How urgent?","criteria":["can wait","this week","today"]},` +
		`"Spam":{"type":"noul","instructions":"Spam?"}}`
	if diff := gocmp.Diff(string(want.w.Questions), string(got.w.Questions)); diff != "" {
		t.Errorf("PreparedFor[Ticket] bytes differ from the hand-built set (-hand +typed):\n%s", diff)
	}
	if diff := gocmp.Diff(wantBytes, string(got.w.Questions)); diff != "" {
		t.Errorf("PreparedFor[Ticket] bytes (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff(want.w.Entries(), got.w.Entries()); diff != "" {
		t.Errorf("PreparedFor[Ticket] tables differ from the hand-built set (-hand +typed):\n%s", diff)
	}
	if diff := gocmp.Diff(want.w.LevelHint, got.w.LevelHint); diff != "" {
		t.Errorf("PreparedFor[Ticket] level hint (-hand +typed):\n%s", diff)
	}

	plan := typedPlanFor[Ticket]()
	if plan.prepared != got || plan.err != nil {
		t.Fatalf("typedPlanFor[Ticket] = {%p, %v}, want {%p, nil}", plan.prepared, plan.err, got)
	}
	wantFields := []typedField{
		{index: 0, name: "Billing", kind: wire.KindNoul},
		{index: 1, name: "Tone", kind: wire.KindChoice, options: []string{"calm", "angry"}},
		{index: 2, name: "Urgency", kind: wire.KindScore, levels: []wire.Content{{Text: "can wait"}, {Text: "this week"}, {Text: "today"}}},
		{index: 3, name: "Spam", kind: wire.KindNoul, optional: true},
	}
	if diff := gocmp.Diff(wantFields, plan.fields, planOptions); diff != "" {
		t.Errorf("typedPlanFor[Ticket].fields (-want +got):\n%s", diff)
	}
	// The plan's tables are the prepared set's own, not copies: the decoder
	// interns against them.
	entries := got.w.Entries()
	if &plan.fields[1].options[0] != &entries[1].Options[0] {
		t.Error("the plan's options table is a copy of the prepared set's")
	}
	if &plan.fields[2].levels[0] != &entries[2].Levels[0] {
		t.Error("the plan's levels table is a copy of the prepared set's")
	}
	for i, typeField := range []string{"Billing", "Tone", "Urgency", "Spam"} {
		if f := reflect.TypeFor[Ticket]().Field(plan.fields[i].index); f.Name != typeField {
			t.Errorf("fields[%d].index names field %s, want %s", i, f.Name, typeField)
		}
	}
}

// escapes asks one question per case of the eight AC-F8 escape cases: each
// of the four escapes once inside an option label and once inside free text.
type escapes struct {
	LabelSemicolon ChoiceAnswer `typesafe:"kind=choice;options=a\\;b=desc|c"`
	LabelBar       ChoiceAnswer `typesafe:"kind=choice;options=a\\|b=desc|c"`
	LabelEquals    ChoiceAnswer `typesafe:"kind=choice;options=a\\=b=desc|c"`
	LabelBackslash ChoiceAnswer `typesafe:"kind=choice;options=a\\\\b=desc|c"`
	TextSemicolon  NoulAnswer   `typesafe:"kind=noul;instructions=a\\;b"`
	TextBar        NoulAnswer   `typesafe:"kind=noul;instructions=a\\|b"`
	TextEquals     NoulAnswer   `typesafe:"kind=noul;instructions=a\\=b"`
	TextBackslash  NoulAnswer   `typesafe:"kind=noul;instructions=a\\\\b"`
}

// TestPreparedForEscapes pins the eight AC-F8 escape cases end to end: the
// escaped character reaches the wire as itself, as the same text set by hand
// does.
func TestPreparedForEscapes(t *testing.T) {
	got, err := PreparedFor[escapes]()
	if err != nil {
		t.Fatalf("PreparedFor[escapes]: %v", err)
	}
	choice := func(label string) Choice {
		return Choice{Options: Options{{Label: label, Description: Text("desc")}, {Label: "c"}}}
	}
	want, err := NewQuestions().
		Choice("LabelSemicolon", choice("a;b")).
		Choice("LabelBar", choice("a|b")).
		Choice("LabelEquals", choice("a=b")).
		Choice("LabelBackslash", choice(`a\b`)).
		Noul("TextSemicolon", Noul{Instructions: Text("a;b")}).
		Noul("TextBar", Noul{Instructions: Text("a|b")}).
		Noul("TextEquals", Noul{Instructions: Text("a=b")}).
		Noul("TextBackslash", Noul{Instructions: Text(`a\b`)}).
		Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if diff := gocmp.Diff(string(want.w.Questions), string(got.w.Questions)); diff != "" {
		t.Errorf("PreparedFor[escapes] bytes differ from the hand-built set (-hand +typed):\n%s", diff)
	}
	if diff := gocmp.Diff(want.w.Entries(), got.w.Entries()); diff != "" {
		t.Errorf("PreparedFor[escapes] tables differ from the hand-built set (-hand +typed):\n%s", diff)
	}
}

// TestParseTag checks the grammar on its own: what each tag parses to, the
// eight AC-F8 escape cases, and every syntax error.
func TestParseTag(t *testing.T) {
	tests := map[string]struct {
		tag     string
		want    tagSpec
		wantErr string
	}{
		"success: empty tag": {
			tag:  "",
			want: tagSpec{},
		},
		"success: every key": {
			tag: "kind=noul;name=billing;instructions=Billing?;yes=invoices;no=anything else;optional",
			want: tagSpec{
				kind: "noul", name: "billing", instructions: "Billing?", yes: "invoices", no: "anything else", optional: true,
				keys: keyKind | keyName | keyInstructions | keyYes | keyNo | keyOptional,
			},
		},
		"success: options with and without descriptions": {
			tag:  "kind=choice;options=calm=neutral or polite|angry",
			want: tagSpec{kind: "choice", options: []tagOption{{label: "calm", description: "neutral or polite"}, {label: "angry"}}, keys: keyKind | keyOptions},
		},
		"success: levels": {
			tag:  "kind=score;levels=can wait|this week|today",
			want: tagSpec{kind: "score", levels: []string{"can wait", "this week", "today"}, keys: keyKind | keyLevels},
		},
		"success: bare | and = stand for themselves in free text": {
			tag:  "instructions=a|b=c",
			want: tagSpec{instructions: "a|b=c", keys: keyInstructions},
		},
		"success: an = after an option's label is part of its description": {
			tag:  "options=x=a=b",
			want: tagSpec{options: []tagOption{{label: "x", description: "a=b"}}, keys: keyOptions},
		},
		"success: = stands for itself in a level": {
			tag:  "levels=a=b|c",
			want: tagSpec{levels: []string{"a=b", "c"}, keys: keyLevels},
		},
		"success: non-ASCII text": {
			tag:  "instructions=請求の件ですか？;name=請求",
			want: tagSpec{instructions: "請求の件ですか？", name: "請求", keys: keyInstructions | keyName},
		},
		"success: the grammar leaves the kind unchecked": {
			tag:  "kind=yesno;options=a;levels=b",
			want: tagSpec{kind: "yesno", options: []tagOption{{label: "a"}}, levels: []string{"b"}, keys: keyKind | keyOptions | keyLevels},
		},

		// The eight AC-F8 escape cases: each escape inside an option or
		// level label, and inside a free-text value.
		`success: escape \; in an option label`: {
			tag:  `options=a\;b=desc`,
			want: tagSpec{options: []tagOption{{label: "a;b", description: "desc"}}, keys: keyOptions},
		},
		`success: escape \| in an option label`: {
			tag:  `options=a\|b|c`,
			want: tagSpec{options: []tagOption{{label: "a|b"}, {label: "c"}}, keys: keyOptions},
		},
		`success: escape \= in an option label`: {
			tag:  `options=a\=b=desc`,
			want: tagSpec{options: []tagOption{{label: "a=b", description: "desc"}}, keys: keyOptions},
		},
		`success: escape \\ in an option label`: {
			tag:  `options=a\\=desc`,
			want: tagSpec{options: []tagOption{{label: `a\`, description: "desc"}}, keys: keyOptions},
		},
		`success: escape \; in free text`: {
			tag:  `instructions=a\;b`,
			want: tagSpec{instructions: "a;b", keys: keyInstructions},
		},
		`success: escape \| in free text`: {
			tag:  `instructions=a\|b`,
			want: tagSpec{instructions: "a|b", keys: keyInstructions},
		},
		`success: escape \= in free text`: {
			tag:  `instructions=a\=b`,
			want: tagSpec{instructions: "a=b", keys: keyInstructions},
		},
		`success: escape \\ in free text`: {
			tag:  `instructions=a\\b`,
			want: tagSpec{instructions: `a\b`, keys: keyInstructions},
		},
		`success: escapes in levels and descriptions`: {
			tag:  `levels=low\|er|hi\;gh;options=x=d\|e\;s\\c`,
			want: tagSpec{levels: []string{"low|er", "hi;gh"}, options: []tagOption{{label: "x", description: `d|e;s\c`}}, keys: keyLevels | keyOptions},
		},
		`success: an escaped backslash before a separator ends the value`: {
			tag:  `instructions=a\\;kind=noul`,
			want: tagSpec{instructions: `a\`, kind: "noul", keys: keyInstructions | keyKind},
		},

		"error: unknown key": {
			tag:     "kind=noul;weight=3",
			wantErr: `unknown key "weight"; the keys are kind, name, instructions, yes, no, options, levels and optional`,
		},
		"error: spaces are not trimmed from a key": {
			tag:     "kind=noul; name=x",
			wantErr: `unknown key " name"; the keys are kind, name, instructions, yes, no, options, levels and optional`,
		},
		"error: a key is never escaped": {
			tag:     `ki\nd=noul`,
			wantErr: `unknown key "ki\nd"; the keys are kind, name, instructions, yes, no, options, levels and optional`,
		},
		"error: key given twice": {
			tag:     "instructions=a;instructions=b",
			wantErr: "the key instructions is given twice",
		},
		"error: optional given twice": {
			tag:     "optional;optional",
			wantErr: "the key optional is given twice",
		},
		"error: unterminated escape": {
			tag:     `kind=noul;instructions=Spam?\`,
			wantErr: `unterminated escape: a "\" at the end of the tag; write "\\" for a backslash`,
		},
		"error: unknown escape": {
			tag:     `instructions=a\nb`,
			wantErr: `unknown escape: "\" before 'n'; the escapes are \;, \|, \= and \\`,
		},
		"error: unknown escape in an option label": {
			tag:     `options=a\tb`,
			wantErr: `unknown escape: "\" before 't'; the escapes are \;, \|, \= and \\`,
		},
		"error: empty value": {
			tag:     "kind=",
			wantErr: "the key kind needs a nonempty value, as in kind=...",
		},
		"error: key without a value": {
			tag:     "kind",
			wantErr: "the key kind needs a nonempty value, as in kind=...",
		},
		"error: optional with a value": {
			tag:     "optional=true",
			wantErr: "optional takes no value: write it bare, as optional",
		},
		"error: empty entry at the start": {
			tag:     ";kind=noul",
			wantErr: `empty entry: two ";" in a row, or a ";" at the start of the tag`,
		},
		"error: empty entry in the middle": {
			tag:     "kind=noul;;name=x",
			wantErr: `empty entry: two ";" in a row, or a ";" at the start of the tag`,
		},
		"error: empty entry at the end": {
			tag:     "kind=noul;",
			wantErr: `empty entry: the tag ends with ";"`,
		},
		"error: empty key": {
			tag:     "=noul",
			wantErr: `unknown key ""; the keys are kind, name, instructions, yes, no, options, levels and optional`,
		},
		"error: empty option": {
			tag:     "options=a||b",
			wantErr: `empty option in options: two "|" in a row, or a "|" at either end`,
		},
		"error: trailing option separator": {
			tag:     "options=a|",
			wantErr: `empty option in options: two "|" in a row, or a "|" at either end`,
		},
		"error: empty option label": {
			tag:     "options==desc",
			wantErr: "an option of options has an empty label",
		},
		"error: empty option description": {
			tag:     "options=a=",
			wantErr: `option "a" has an empty description; leave out the "=" for an option without one`,
		},
		"error: empty level": {
			tag:     "levels=low||high",
			wantErr: `empty level in levels: two "|" in a row, or a "|" at either end`,
		},
		"error: leading level separator": {
			tag:     "levels=|low",
			wantErr: `empty level in levels: two "|" in a row, or a "|" at either end`,
		},
		"error: not valid UTF-8": {
			tag:     "instructions=\xff",
			wantErr: "the tag is not valid UTF-8",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseTag(tt.tag)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("parseTag(%q) = %+v, want error %q", tt.tag, got, tt.wantErr)
				}
				if _, ok := errors.AsType[*tagError](err); !ok {
					t.Errorf("parseTag(%q) error is a %T, want *tagError", tt.tag, err)
				}
				if diff := gocmp.Diff(tt.wantErr, err.Error()); diff != "" {
					t.Errorf("parseTag(%q) error (-want +got):\n%s", tt.tag, diff)
				}
				if diff := gocmp.Diff(tagSpec{}, got, planOptions); diff != "" {
					t.Errorf("parseTag(%q) returned a partial tag with its error (-want +got):\n%s", tt.tag, diff)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTag(%q): %v", tt.tag, err)
			}
			if diff := gocmp.Diff(tt.want, got, planOptions); diff != "" {
				t.Errorf("parseTag(%q) (-want +got):\n%s", tt.tag, diff)
			}
		})
	}
}

// selfPointer is a pointer type that points to itself.
type selfPointer *selfPointer

// TaggedBase is a struct with a tagged answer field, to be embedded.
type TaggedBase struct {
	Spam NoulAnswer `typesafe:"kind=noul"`
}

// OuterBase embeds TaggedBase, one level further down.
type OuterBase struct {
	Note string
	TaggedBase
}

// taggedBase is TaggedBase under an unexported name.
type taggedBase struct {
	Spam NoulAnswer `typesafe:"kind=noul"`
}

// CyclicTagged embeds a pointer to itself and has a tagged field.
type CyclicTagged struct {
	*CyclicTagged
	Spam NoulAnswer `typesafe:"kind=noul"`
}

// The struct types of TestPreparedForRejections: Go generics need a named
// type per case.
type (
	// (1) duplicate wire name.
	rejDupName struct {
		Spam  NoulAnswer `typesafe:"kind=noul"`
		Other NoulAnswer `typesafe:"kind=noul;name=Spam"`
	}
	// (2) duplicate option label.
	rejDupOption struct {
		Tone ChoiceAnswer `typesafe:"kind=choice;options=calm=polite|angry|calm"`
	}
	// (3) duplicate level text.
	rejDupLevel struct {
		Urgency ScoreAnswer `typesafe:"kind=score;levels=low|high|low"`
	}
	// (4) choice without options, in case 4's family (R96).
	rejNoOptions struct {
		Tone ChoiceAnswer `typesafe:"kind=choice;instructions=Tone?"`
	}
	// (4) score without levels.
	rejNoLevels struct {
		Urgency ScoreAnswer `typesafe:"kind=score;instructions=How urgent?"`
	}
	// (5) missing kind.
	rejNoKind struct {
		Spam NoulAnswer `typesafe:"instructions=Spam?"`
	}
	// (5) missing kind: an answer field without a tag.
	rejUntagged struct {
		Spam NoulAnswer
	}
	// (6) unknown kind.
	rejUnknownKind struct {
		Spam NoulAnswer `typesafe:"kind=yesno"`
	}
	// (7) kind/field-type mismatch.
	rejKindMismatch struct {
		Tone NoulAnswer `typesafe:"kind=choice;options=calm"`
	}
	// (7) kind/field-type mismatch: a tag on a field that is not an answer.
	rejTaggedString struct {
		Note string `typesafe:"kind=noul"`
	}
	// (7) kind/field-type mismatch: a pointer type that points to itself,
	// which the pointer walk must not follow forever.
	rejSelfPointer struct {
		Loose selfPointer
		Spam  selfPointer `typesafe:"kind=noul"`
	}
	// (8) pointer field.
	rejPointer struct {
		Spam *NoulAnswer `typesafe:"kind=noul;instructions=Spam?"`
	}
	// (8) pointer field: untagged, an answer field all the same.
	rejUntaggedPointer struct {
		Spam **NoulAnswer
	}
	// (9) a tagged field inside an embedded struct, which is not promoted.
	rejEmbeddedTagged struct {
		TaggedBase
		Tone ChoiceAnswer `typesafe:"kind=choice;options=calm"`
	}
	// (9) a tagged field inside an embedded pointer to a struct.
	rejEmbeddedPointer struct {
		Tone ChoiceAnswer `typesafe:"kind=choice;options=calm"`
		*TaggedBase
	}
	// (9) a tagged field two embeddings deep.
	rejEmbeddedDeep struct {
		OuterBase
	}
	// (9) a tagged field inside an unexported embedded struct: its exported
	// fields would be promoted all the same.
	rejEmbeddedUnexported struct {
		taggedBase
	}
	// (9) a tagged field inside a struct that embeds a pointer to itself.
	rejEmbeddedCyclic struct {
		CyclicTagged
	}
	// (9) unexported field with a tag.
	rejUnexported struct {
		Tone ChoiceAnswer `typesafe:"kind=choice;options=calm"`
		spam NoulAnswer   `typesafe:"kind=noul"`
	}
	// (11) reserved name.
	rejReserved struct {
		Model NoulAnswer `typesafe:"kind=noul;name=model"`
	}
	// (11) reserved name, after an ignored unexported field.
	rejReservedAfterIgnored struct {
		answers NoulAnswer
		Usage   NoulAnswer `typesafe:"kind=noul;name=usage"`
	}
	// (12) optional on a field that is not an answer.
	rejOptionalString struct {
		Spam NoulAnswer `typesafe:"kind=noul"`
		Note string     `typesafe:"optional"`
	}
	// (13) tag syntax error: unknown key.
	rejUnknownKey struct {
		Spam NoulAnswer `typesafe:"kind=noul;weight=3"`
	}
	// (13) tag syntax error: unterminated escape.
	rejUnterminated struct {
		Spam NoulAnswer `typesafe:"kind=noul;instructions=Spam?\\"`
	}
	// (13) tag syntax error: a key the kind does not take.
	rejKeyOutsideKind struct {
		Spam NoulAnswer `typesafe:"kind=noul;options=x"`
	}
	// (14) no answer fields: the empty set Prepare refuses.
	rejEmpty struct {
		Note string
	}
)

// Test TestPreparedForRejections covers upstream's pyrefly negative
// expectations (XT1, tests/typing/negative/*.py at 0ffd094). Each maps to a
// rejection of PreparedFor, to a compile error in Go (the mistake cannot be
// written in a Go program that builds), or to a runtime check outside
// PreparedFor:
//
//	file            expectation                                           Go analogue
//	async_client.py system_one(None, ...)                                 runtime *InvalidRequestError (nil state, W1.2), not PreparedFor
//	async_client.py AsyncTypeSafeClient(retry=Retrying())                 compile error: WithRetry takes a RetryPolicy
//	async_client.py AsyncTypeSafeClient(http_client=httpx2.Client())      not representable: one client, no sync/async split (WithHTTPTransport takes *http.Transport)
//	async_client.py system_one(..., retry=Retrying())                     compile error: Retry takes a RetryPolicy
//	async_client.py system_one(..., response_model=int)                   case 10: PreparedFor[int] / Ask[int] → *ConfigError
//	async_client.py system_one(..., response_model=object())              compile error: a type argument is a type, never a value
//	sync_client.py  the six expectations above, sync client               same six analogues
//	questions.py    ChoiceModel with "type": "noul"                       compile error: Choice has no Type field; tag analogue case 7 (kind=noul on a ChoiceAnswer)
//	questions.py    NoulModel with "type": "choice"                       compile error: Noul has no Type field; tag analogue case 7
//	questions.py    ScoreModel with "type": "choice"                      compile error: Score has no Type field; tag analogue case 7
//	questions.py    NoulModel without "type"                              compile error: the type is the Go type; tag analogue case 5 (no kind)
//	questions.py    ChoiceModel without "criteria"                        case 4's family: a kind=choice tag without options is refused (R96, as Rust refuses it); the builder's Choice{} still sends "criteria":{}, as Python's runtime accepts an empty dict; RawQuestion{Type: "choice"} without criteria → Prepare *ConfigError
//	questions.py    ScoreModel without "criteria"                         case 4 (score without levels); Score{} → Prepare *ConfigError
//	questions.py    NoulModel with an "extra" key                         compile error: Noul has no such field; tag analogue case 13 (unknown key)
//	questions.py    ChoiceModel with list criteria                        compile error: Options is []Option
//	questions.py    ScoreModel with dict criteria                         compile error: Levels is []Content
//	questions.py    Question {"unrelated": "value"}                       compile error for Noul/Choice/Score; RawQuestion{} (empty Type) → Prepare *ConfigError
//	questions.py    Question {"type": 123}                                compile error: RawQuestion.Type is a string
//	transport.py    send(...) of a models request as SystemOneResponse    compile error: ListModels and SystemOne return distinct types
//	transport.py    send_async(...) likewise                              compile error, as above (no async variant)
//	transport.py    client._request(models) as SystemOneResponse          compile error, as above
//	transport.py    async client._request(models) likewise                compile error, as above
//
// The positive fixtures (valid.py, transport.py, pydantic_response_models.py)
// are W6.4's: go vet ./examples/... .
func TestPreparedForRejections(t *testing.T) {
	_ = rejUnexported{}.spam              // unexported fields exist only to be refused
	_ = rejReservedAfterIgnored{}.answers // or ignored

	tests := map[string]struct {
		prepare func() (*Prepared, error)
		wantMsg string
	}{
		"error: (1) duplicate wire name": {
			prepare: PreparedFor[rejDupName],
			wantMsg: `PreparedFor[typesafe.rejDupName]: field Other: Question "Spam" is added more than once; question names must be unique. Field Spam asks it first.`,
		},
		"error: (2) duplicate option label": {
			prepare: PreparedFor[rejDupOption],
			wantMsg: `PreparedFor[typesafe.rejDupOption]: field Tone: Choice question "Tone" has option "calm" more than once; option labels must be unique.`,
		},
		"error: (3) duplicate level text": {
			prepare: PreparedFor[rejDupLevel],
			wantMsg: `PreparedFor[typesafe.rejDupLevel]: field Urgency: Score question "Urgency" has level "low" more than once; levels must be unique.`,
		},
		"error: (4) score without levels": {
			prepare: PreparedFor[rejNoLevels],
			wantMsg: `PreparedFor[typesafe.rejNoLevels]: field Urgency: Score question "Urgency" has no criteria; at least one score is required. List the levels, lowest first, as levels=low|high.`,
		},
		"error: (4) choice without options": {
			prepare: PreparedFor[rejNoOptions],
			wantMsg: `PreparedFor[typesafe.rejNoOptions]: field Tone: Choice question "Tone" has no options; list them, each optionally described, as options=calm=polite|angry.`,
		},
		"error: (5) missing kind": {
			prepare: PreparedFor[rejNoKind],
			wantMsg: `PreparedFor[typesafe.rejNoKind]: field Spam: the typesafe tag has no kind; add kind=noul.`,
		},
		"error: (5) missing kind: answer field without a tag": {
			prepare: PreparedFor[rejUntagged],
			wantMsg: `PreparedFor[typesafe.rejUntagged]: field Spam: the NoulAnswer field has no typesafe tag, so no kind; every answer field asks a question: add a tag such as typesafe:"kind=noul".`,
		},
		"error: (6) unknown kind": {
			prepare: PreparedFor[rejUnknownKind],
			wantMsg: `PreparedFor[typesafe.rejUnknownKind]: field Spam: unknown kind "yesno"; the kinds are noul, choice and score.`,
		},
		"error: (7) kind/field-type mismatch": {
			prepare: PreparedFor[rejKindMismatch],
			wantMsg: `PreparedFor[typesafe.rejKindMismatch]: field Tone: kind=choice needs a ChoiceAnswer field, and the field is a NoulAnswer; make the kind and the field's type agree.`,
		},
		"error: (7) kind/field-type mismatch: tag on a string field": {
			prepare: PreparedFor[rejTaggedString],
			wantMsg: `PreparedFor[typesafe.rejTaggedString]: field Note: a typesafe tag needs a NoulAnswer, ChoiceAnswer or ScoreAnswer field, and the field is a string.`,
		},
		"error: (7) kind/field-type mismatch: self-referential pointer": {
			prepare: PreparedFor[rejSelfPointer],
			wantMsg: `PreparedFor[typesafe.rejSelfPointer]: field Spam: a typesafe tag needs a NoulAnswer, ChoiceAnswer or ScoreAnswer field, and the field is a typesafe.selfPointer.`,
		},
		"error: (8) pointer field": {
			prepare: PreparedFor[rejPointer],
			wantMsg: `PreparedFor[typesafe.rejPointer]: field Spam: the field is a pointer, *typesafe.NoulAnswer; answer fields are values: make it a NoulAnswer, with optional in its tag if the answer may be absent.`,
		},
		"error: (8) pointer field: untagged": {
			prepare: PreparedFor[rejUntaggedPointer],
			wantMsg: `PreparedFor[typesafe.rejUntaggedPointer]: field Spam: the field is a pointer, **typesafe.NoulAnswer; answer fields are values: make it a NoulAnswer, with optional in its tag if the answer may be absent.`,
		},
		"error: (9) tagged field inside an embedded struct": {
			prepare: PreparedFor[rejEmbeddedTagged],
			wantMsg: `PreparedFor[typesafe.rejEmbeddedTagged]: field TaggedBase: fields of an embedded struct are not promoted, and TaggedBase.Spam has a typesafe tag; declare Spam in typesafe.rejEmbeddedTagged itself, since PreparedFor reads only the struct's own fields.`,
		},
		"error: (9) tagged field inside an embedded pointer to a struct": {
			prepare: PreparedFor[rejEmbeddedPointer],
			wantMsg: `PreparedFor[typesafe.rejEmbeddedPointer]: field TaggedBase: fields of an embedded struct are not promoted, and TaggedBase.Spam has a typesafe tag; declare Spam in typesafe.rejEmbeddedPointer itself, since PreparedFor reads only the struct's own fields.`,
		},
		"error: (9) tagged field two embeddings deep": {
			prepare: PreparedFor[rejEmbeddedDeep],
			wantMsg: `PreparedFor[typesafe.rejEmbeddedDeep]: field OuterBase: fields of an embedded struct are not promoted, and OuterBase.TaggedBase.Spam has a typesafe tag; declare Spam in typesafe.rejEmbeddedDeep itself, since PreparedFor reads only the struct's own fields.`,
		},
		"error: (9) tagged field inside an unexported embedded struct": {
			prepare: PreparedFor[rejEmbeddedUnexported],
			wantMsg: `PreparedFor[typesafe.rejEmbeddedUnexported]: field taggedBase: fields of an embedded struct are not promoted, and taggedBase.Spam has a typesafe tag; declare Spam in typesafe.rejEmbeddedUnexported itself, since PreparedFor reads only the struct's own fields.`,
		},
		"error: (9) tagged field inside a self-embedding struct": {
			prepare: PreparedFor[rejEmbeddedCyclic],
			wantMsg: `PreparedFor[typesafe.rejEmbeddedCyclic]: field CyclicTagged: fields of an embedded struct are not promoted, and CyclicTagged.Spam has a typesafe tag; declare Spam in typesafe.rejEmbeddedCyclic itself, since PreparedFor reads only the struct's own fields.`,
		},
		"error: (9) unexported field with a tag": {
			prepare: PreparedFor[rejUnexported],
			wantMsg: `PreparedFor[typesafe.rejUnexported]: field spam: the field is unexported and has a typesafe tag; only exported fields are answered: export the field or remove the tag.`,
		},
		"error: (10) non-struct T": {
			prepare: PreparedFor[int],
			wantMsg: `PreparedFor[int]: int is not a struct type; PreparedFor needs a struct with one answer field per question.`,
		},
		"error: (10) non-struct T: pointer to a struct": {
			prepare: PreparedFor[*Ticket],
			wantMsg: `PreparedFor[*typesafe.Ticket]: *typesafe.Ticket is a pointer; pass the struct type itself: PreparedFor[typesafe.Ticket].`,
		},
		"error: (10) non-struct T: interface": {
			prepare: PreparedFor[any],
			wantMsg: `PreparedFor[interface {}]: interface {} is not a struct type; PreparedFor needs a struct with one answer field per question.`,
		},
		"error: (11) reserved name": {
			prepare: PreparedFor[rejReserved],
			wantMsg: `PreparedFor[typesafe.rejReserved]: field Model: the question name "model" is reserved (type, model, usage and answers are); give the field another name with name=.`,
		},
		"error: (11) reserved name after an ignored field": {
			prepare: PreparedFor[rejReservedAfterIgnored],
			wantMsg: `PreparedFor[typesafe.rejReservedAfterIgnored]: field Usage: the question name "usage" is reserved (type, model, usage and answers are); give the field another name with name=.`,
		},
		"error: (12) optional on a non-answer field": {
			prepare: PreparedFor[rejOptionalString],
			wantMsg: `PreparedFor[typesafe.rejOptionalString]: field Note: optional applies only to NoulAnswer, ChoiceAnswer and ScoreAnswer fields, and the field is a string.`,
		},
		"error: (13) tag syntax: unknown key": {
			prepare: PreparedFor[rejUnknownKey],
			wantMsg: `PreparedFor[typesafe.rejUnknownKey]: field Spam: typesafe tag: unknown key "weight"; the keys are kind, name, instructions, yes, no, options, levels and optional.`,
		},
		"error: (13) tag syntax: unterminated escape": {
			prepare: PreparedFor[rejUnterminated],
			wantMsg: `PreparedFor[typesafe.rejUnterminated]: field Spam: typesafe tag: unterminated escape: a "\" at the end of the tag; write "\\" for a backslash.`,
		},
		"error: (13) tag syntax: key the kind does not take": {
			prepare: PreparedFor[rejKeyOutsideKind],
			wantMsg: `PreparedFor[typesafe.rejKeyOutsideKind]: field Spam: typesafe tag: options does not apply to kind=noul; a noul takes kind, name, instructions, yes, no and optional.`,
		},
		"error: (14) no answer fields": {
			prepare: PreparedFor[rejEmpty],
			wantMsg: `PreparedFor[typesafe.rejEmpty]: At least one question is required. typesafe.rejEmpty has no answer fields: give it one NoulAnswer, ChoiceAnswer or ScoreAnswer field per question, each with a typesafe tag.`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p, err := tt.prepare()
			if p != nil {
				t.Errorf("prepared set = %p, want nil with the error", p)
			}
			ce, ok := errors.AsType[*ConfigError](err)
			if !ok {
				t.Fatalf("error = %#v, want a *ConfigError", err)
			}
			if diff := gocmp.Diff(tt.wantMsg, ce.Error()); diff != "" {
				t.Errorf("message (-want +got):\n%s", diff)
			}
			// The failure is cached: a second call returns the same value.
			p2, err2 := tt.prepare()
			ce2, _ := errors.AsType[*ConfigError](err2)
			if p2 != nil || ce2 != ce {
				t.Errorf("second call = {%p, %p}, want {nil, %p}: the same *ConfigError", p2, ce2, ce)
			}
		})
	}
}

// TestPreparedForMalformedTag covers a tag that is not a valid Go string
// literal, such as `typesafe:"instructions=a\;b"` written with one
// backslash: reflect.StructTag.Lookup reports it as absent, and PreparedFor
// refuses the field (13) instead of ignoring the tag. go vet refuses such a
// literal in source, so the types are made with reflect.StructOf.
func TestPreparedForMalformedTag(t *testing.T) {
	tests := map[string]struct {
		field   reflect.StructField
		wantMsg string
	}{
		"error: (13) single backslash before ;": {
			field:   reflect.StructField{Name: "Spam", Type: reflect.TypeFor[NoulAnswer](), Tag: `typesafe:"kind=noul;instructions=a\;b"`},
			wantMsg: `field Spam: the typesafe tag is not a valid Go string literal; write each backslash of an escape twice in the struct tag, as in typesafe:"instructions=a\\;b".`,
		},
		"error: (13) unterminated Go literal": {
			field:   reflect.StructField{Name: "Spam", Type: reflect.TypeFor[NoulAnswer](), Tag: `json:"spam" typesafe:"kind=noul`},
			wantMsg: `field Spam: the typesafe tag is not a valid Go string literal; write each backslash of an escape twice in the struct tag, as in typesafe:"instructions=a\\;b".`,
		},
		"error: (13) malformed tag on a string field": {
			field:   reflect.StructField{Name: "Note", Type: reflect.TypeFor[string](), Tag: `typesafe:"optional\;"`},
			wantMsg: `field Note: the typesafe tag is not a valid Go string literal; write each backslash of an escape twice in the struct tag, as in typesafe:"instructions=a\\;b".`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			typ := reflect.StructOf([]reflect.StructField{tt.field})
			plan := buildPlan(typ)
			ce, ok := errors.AsType[*ConfigError](plan.err)
			if !ok {
				t.Fatalf("buildPlan error = %#v, want a *ConfigError", plan.err)
			}
			if diff := gocmp.Diff("PreparedFor[struct {...}]: "+tt.wantMsg, ce.Error()); diff != "" {
				t.Errorf("message (-want +got):\n%s", diff)
			}
		})
	}
}

// TestLookupTag checks the struct tag lookup against reflect's for
// well-formed tags, and its report of a malformed typesafe value.
func TestLookupTag(t *testing.T) {
	tests := map[string]struct {
		tag           reflect.StructTag
		wantValue     string
		wantOK        bool
		wantMalformed bool
	}{
		"success: no tag":               {tag: ``},
		"success: other keys only":      {tag: `json:"spam,omitzero" xml:"spam"`},
		"success: typesafe alone":       {tag: `typesafe:"kind=noul"`, wantValue: "kind=noul", wantOK: true},
		"success: after another key":    {tag: `json:"spam" typesafe:"kind=noul;optional"`, wantValue: "kind=noul;optional", wantOK: true},
		"success: Go escapes unquoted":  {tag: `typesafe:"instructions=a\\;b \"quoted\""`, wantValue: `instructions=a\;b "quoted"`, wantOK: true},
		"success: empty value":          {tag: `typesafe:""`, wantOK: true},
		"success: key as a prefix only": {tag: `typesafe2:"kind=noul"`},
		"error: invalid Go escape":      {tag: `typesafe:"a\;b"`, wantMalformed: true},
		"error: unterminated literal":   {tag: `typesafe:"kind=noul`, wantMalformed: true},
		"error: bad literal of another key is not ours": {
			tag: `json:"a\;b" typesafe:"kind=noul"`, wantValue: "kind=noul", wantOK: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			value, ok, malformed := lookupTag(tt.tag)
			if diff := gocmp.Diff([]any{tt.wantValue, tt.wantOK, tt.wantMalformed}, []any{value, ok, malformed}); diff != "" {
				t.Errorf("lookupTag(%q) = (value, ok, malformed) (-want +got):\n%s", tt.tag, diff)
			}
			if !tt.wantMalformed {
				rv, rok := tt.tag.Lookup("typesafe")
				if rv != value || rok != ok {
					t.Errorf("reflect's Lookup(%q) = (%q, %v), lookupTag = (%q, %v)", tt.tag, rv, rok, value, ok)
				}
			}
		})
	}
}

// Types for TestPreparedForOptional: the same question with and without
// optional, and optional on each kind.
type (
	optionalSet struct {
		Spam NoulAnswer   `typesafe:"kind=noul;optional;instructions=Spam?"`
		Tone ChoiceAnswer `typesafe:"optional;kind=choice;options=calm|angry"`
		Mood ScoreAnswer  `typesafe:"kind=score;levels=low|high;optional"`
	}
	requiredSet struct {
		Spam NoulAnswer   `typesafe:"kind=noul;instructions=Spam?"`
		Tone ChoiceAnswer `typesafe:"kind=choice;options=calm|angry"`
		Mood ScoreAnswer  `typesafe:"kind=score;levels=low|high"`
	}
)

// TestPreparedForOptional covers the three AC-F8 optional cases at the plan's
// level: the flag is recorded when given and absent when not, and changes
// nothing on the wire. What it means for decoding (an absent answer leaves
// Present false) is W4.2's DecodeAs.
func TestPreparedForOptional(t *testing.T) {
	tests := map[string]struct {
		plan         *typedPlan
		wantOptional []bool
	}{
		"success: optional recorded in the plan (decode half: W4.2 Present)": {
			plan:         typedPlanFor[optionalSet](),
			wantOptional: []bool{true, true, true},
		},
		"success: no optional recorded when absent (decode half: W4.2 Present)": {
			plan:         typedPlanFor[requiredSet](),
			wantOptional: []bool{false, false, false},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.plan.err != nil {
				t.Fatalf("plan error: %v", tt.plan.err)
			}
			got := make([]bool, len(tt.plan.fields))
			for i := range tt.plan.fields {
				got[i] = tt.plan.fields[i].optional
			}
			if diff := gocmp.Diff(tt.wantOptional, got); diff != "" {
				t.Errorf("optional flags (-want +got):\n%s", diff)
			}
		})
	}
	t.Run("success: optional is not sent", func(t *testing.T) {
		opt, req := typedPlanFor[optionalSet](), typedPlanFor[requiredSet]()
		if diff := gocmp.Diff(string(req.prepared.w.Questions), string(opt.prepared.w.Questions)); diff != "" {
			t.Errorf("optional changed the wire bytes (-required +optional):\n%s", diff)
		}
	})
	t.Run("error: (12) optional on a non-answer field", func(t *testing.T) {
		// The third case is rejection 12 of TestPreparedForRejections.
		_, err := PreparedFor[rejOptionalString]()
		if _, ok := errors.AsType[*ConfigError](err); !ok {
			t.Fatalf("PreparedFor[rejOptionalString] error = %#v, want a *ConfigError", err)
		}
	})
}

// ignoredFields mixes answer fields with fields PreparedFor ignores, and one
// it refuses.
type ignoredFields struct {
	note     string
	Count    int
	internal NoulAnswer
	plainBase
	First  NoulAnswer  `typesafe:"kind=noul;name=first"`
	Label  string      `json:"label"`
	Second ScoreAnswer // refused: an answer field without a tag
}

// plainBase is an embedded struct without typesafe tags: ignored, answer
// fields included, since its fields are not promoted.
type plainBase struct {
	Hidden NoulAnswer
	Note   string
}

// Cyclic embeds a pointer to itself; searching it for tags must end.
type Cyclic struct {
	*Cyclic
	Note string
}

// ignoredOnly is ignoredFields without its refused field.
type ignoredOnly struct {
	note     string
	Count    int
	internal NoulAnswer
	plainBase
	First NoulAnswer  `typesafe:"kind=noul;name=first"`
	Label string      `json:"label"`
	Last  ScoreAnswer `typesafe:"kind=score;levels=1|2"`
	*Cyclic
}

// TestPreparedForIgnoredFields checks which fields ask nothing: untagged
// fields of other types, untagged unexported fields (answer types
// included), and an embedded struct without typesafe tags, even one that
// embeds a pointer to itself; and that the plan's indexes skip them.
func TestPreparedForIgnoredFields(t *testing.T) {
	_ = ignoredFields{}.note
	_ = ignoredFields{}.internal
	_ = ignoredOnly{}.note
	_ = ignoredOnly{}.internal

	plan := typedPlanFor[ignoredOnly]()
	if plan.err != nil {
		t.Fatalf("typedPlanFor[ignoredOnly]: %v", plan.err)
	}
	want := []typedField{
		{index: 4, name: "first", kind: wire.KindNoul},
		{index: 6, name: "Last", kind: wire.KindScore, levels: []wire.Content{{Text: "1"}, {Text: "2"}}},
	}
	if diff := gocmp.Diff(want, plan.fields, planOptions); diff != "" {
		t.Errorf("fields (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff([]string{"first", "Last"}, collectNames(plan.prepared)); diff != "" {
		t.Errorf("names (-want +got):\n%s", diff)
	}

	_, err := PreparedFor[ignoredFields]()
	want5 := `PreparedFor[typesafe.ignoredFields]: field Second: the ScoreAnswer field has no typesafe tag, so no kind; every answer field asks a question: add a tag such as typesafe:"kind=score".`
	if err == nil || err.Error() != want5 {
		t.Errorf("PreparedFor[ignoredFields] error = %v, want %q", err, want5)
	}
}

// collectNames returns the question names of p in order.
func collectNames(p *Prepared) []string {
	var names []string
	for name := range p.Names() {
		names = append(names, name)
	}
	return names
}

// TestPreparedForManyFields checks the duplicate-name check past the scan
// limit, where it switches to a map: a struct type of repeatScanLimit+8
// answer fields, and the same with its last field renamed onto an early one.
func TestPreparedForManyFields(t *testing.T) {
	n := repeatScanLimit + 8
	fields := make([]reflect.StructField, n)
	for i := range fields {
		fields[i] = reflect.StructField{Name: "Q" + string(rune('A'+i/26)) + string(rune('a'+i%26)), Type: reflect.TypeFor[NoulAnswer](), Tag: `typesafe:"kind=noul"`}
	}
	plan := buildPlan(reflect.StructOf(fields))
	if plan.err != nil {
		t.Fatalf("buildPlan: %v", plan.err)
	}
	if got := plan.prepared.Len(); got != n {
		t.Errorf("Len() = %d, want %d", got, n)
	}
	for i := range plan.fields {
		if plan.fields[i].index != i || plan.fields[i].name != fields[i].Name {
			t.Errorf("fields[%d] = {index %d, name %q}, want {%d, %q}", i, plan.fields[i].index, plan.fields[i].name, i, fields[i].Name)
		}
	}

	fields[n-1].Tag = reflect.StructTag(`typesafe:"kind=noul;name=` + fields[3].Name + `"`)
	plan = buildPlan(reflect.StructOf(fields))
	want := `PreparedFor[struct {...}]: field ` + fields[n-1].Name + `: Question "` + fields[3].Name + `" is added more than once; question names must be unique. Field ` + fields[3].Name + ` asks it first.`
	if plan.err == nil || plan.err.Error() != want {
		t.Errorf("buildPlan error = %v, want %q", plan.err, want)
	}
}

// cacheOnly is used by TestPreparedForCache alone, so that its first call
// there is the first call of the process (of the first run, with -count).
type cacheOnly struct {
	Spam NoulAnswer `typesafe:"kind=noul;instructions=Spam?"`
}

// concurrentOnly is used by TestPreparedForConcurrentFirstUse alone.
type concurrentOnly struct {
	Spam NoulAnswer `typesafe:"kind=noul;instructions=Spam?"`
}

// TestPreparedForCache pins the caching contract: the first call builds
// the plan, and every later call returns the same *Prepared, or the same
// error, without allocating.
func TestPreparedForCache(t *testing.T) {
	// With -count above 1 an earlier run of this test made the first call.
	_, cachedBefore := typedPlans.Load(reflect.TypeFor[cacheOnly]())
	t.Logf("cacheOnly cached before this run's first call: %v", cachedBefore)
	first, err := PreparedFor[cacheOnly]()
	if err != nil {
		t.Fatalf("PreparedFor[cacheOnly]: %v", err)
	}
	if _, ok := typedPlans.Load(reflect.TypeFor[cacheOnly]()); !ok {
		t.Fatal("cacheOnly is not cached after its first call")
	}
	second, err := PreparedFor[cacheOnly]()
	if err != nil || second != first {
		t.Fatalf("second call = {%p, %v}, want {%p, nil}: the same set", second, err, first)
	}

	var sink *Prepared
	if allocs := testing.AllocsPerRun(100, func() { sink, _ = PreparedFor[cacheOnly]() }); allocs != 0 {
		t.Errorf("cached PreparedFor[cacheOnly] allocates %v times per call, want 0", allocs)
	}
	var errSink error
	_, _ = PreparedFor[rejDupName]()
	if allocs := testing.AllocsPerRun(100, func() { _, errSink = PreparedFor[rejDupName]() }); allocs != 0 {
		t.Errorf("cached failing PreparedFor[rejDupName] allocates %v times per call, want 0", allocs)
	}
	if sink != first || errSink == nil {
		t.Errorf("sinks = {%p, %v}, want {%p, non-nil}", sink, errSink, first)
	}
}

// TestPreparedForConcurrentFirstUse checks that goroutines racing to make
// the first call all get the one set that was kept.
func TestPreparedForConcurrentFirstUse(t *testing.T) {
	const goroutines = 16
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		got   [goroutines]*Prepared
		errs  [goroutines]error
	)
	for i := range goroutines {
		wg.Go(func() {
			<-start
			got[i], errs[i] = PreparedFor[concurrentOnly]()
		})
	}
	close(start)
	wg.Wait()
	for i := range goroutines {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if got[i] != got[0] {
			t.Errorf("goroutine %d got set %p, goroutine 0 got %p", i, got[i], got[0])
		}
	}
	if kept, _ := PreparedFor[concurrentOnly](); kept != got[0] {
		t.Errorf("later call got set %p, the racers got %p", kept, got[0])
	}
}
