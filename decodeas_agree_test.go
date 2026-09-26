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
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// The struct types of TestDecodeAsAgreesWithAnswers, one per fixture the
// decoder accepts (result.json and type-last.json share reviewAnswers, the
// floods are in decodeas_flood_test.go). Each types every answer of its
// fixture, in the order the fixture's answers first appear, with the
// options and levels the answer names; a field for a name the fixture does
// not end up answering is optional.

// duplicatesAnswers types duplicates.json: the answers of its last answers
// member, and optional fields for the three answers of the superseded one,
// which are not there (last wins).
type duplicatesAnswers struct {
	Tone    ChoiceAnswer `typesafe:"kind=choice;name=tone;options=friendly|hostile"`
	Spam    NoulAnswer   `typesafe:"kind=noul;name=spam"`
	Quality ScoreAnswer  `typesafe:"kind=score;name=quality;levels=bad|fine|great"`
	Risk    ScoreAnswer  `typesafe:"kind=score;name=risk;levels=low"`
	Gone    NoulAnswer   `typesafe:"kind=noul;name=gone;optional"`
	Broken  NoulAnswer   `typesafe:"kind=noul;name=broken;optional"`
	Mystery NoulAnswer   `typesafe:"kind=noul;name=mystery;optional"`
}

// result20Answers types result-20.json: 7 noul, 7 choice and 6 score
// answers.
type result20Answers struct {
	Spam          NoulAnswer   `typesafe:"kind=noul;name=spam"`
	Tone          ChoiceAnswer `typesafe:"kind=choice;name=tone;options=calm|angry|confused"`
	Urgency       ScoreAnswer  `typesafe:"kind=score;name=urgency;levels=can wait|this week|today"`
	Billing       NoulAnswer   `typesafe:"kind=noul;name=billing"`
	Language      ChoiceAnswer `typesafe:"kind=choice;name=language;options=en|ja|de|fr"`
	Sentiment     ScoreAnswer  `typesafe:"kind=score;name=sentiment;levels=very negative|negative|neutral|positive|very positive"`
	RefundRequest NoulAnswer   `typesafe:"kind=noul;name=refund_request"`
	Team          ChoiceAnswer `typesafe:"kind=choice;name=team;options=billing|support|sales"`
	Priority      ScoreAnswer  `typesafe:"kind=score;name=priority;levels=low|normal|high|critical"`
	PIIPresent    NoulAnswer   `typesafe:"kind=noul;name=pii_present"`
	Product       ChoiceAnswer `typesafe:"kind=choice;name=product;options=sdk|api|dashboard|other"`
	Clarity       ScoreAnswer  `typesafe:"kind=score;name=clarity;levels=unclear|partly clear|clear"`
	NeedsHuman    NoulAnswer   `typesafe:"kind=noul;name=needs_human"`
	Channel       ChoiceAnswer `typesafe:"kind=choice;name=channel;options=email|chat|phone"`
	Complexity    ScoreAnswer  `typesafe:"kind=score;name=complexity;levels=simple|moderate|complex"`
	Duplicate     NoulAnswer   `typesafe:"kind=noul;name=duplicate"`
	Intent        ChoiceAnswer `typesafe:"kind=choice;name=intent;options=question|complaint|request|feedback"`
	Effort        ScoreAnswer  `typesafe:"kind=score;name=effort;levels=minutes|hours|days"`
	Escalate      NoulAnswer   `typesafe:"kind=noul;name=escalate"`
	Resolution    ChoiceAnswer `typesafe:"kind=choice;name=resolution;options=resolved|pending|wontfix"`
}

// scoreFloodMiniAnswers types score-flood-mini.json: eight scores with an
// empty legend and eight with one level.
type scoreFloodMiniAnswers struct {
	Empty0 ScoreAnswer `typesafe:"kind=score;name=empty0;levels=x"`
	Empty1 ScoreAnswer `typesafe:"kind=score;name=empty1;levels=x"`
	Empty2 ScoreAnswer `typesafe:"kind=score;name=empty2;levels=x"`
	Empty3 ScoreAnswer `typesafe:"kind=score;name=empty3;levels=x"`
	Empty4 ScoreAnswer `typesafe:"kind=score;name=empty4;levels=x"`
	Empty5 ScoreAnswer `typesafe:"kind=score;name=empty5;levels=x"`
	Empty6 ScoreAnswer `typesafe:"kind=score;name=empty6;levels=x"`
	Empty7 ScoreAnswer `typesafe:"kind=score;name=empty7;levels=x"`
	One0   ScoreAnswer `typesafe:"kind=score;name=one0;levels=x"`
	One1   ScoreAnswer `typesafe:"kind=score;name=one1;levels=x"`
	One2   ScoreAnswer `typesafe:"kind=score;name=one2;levels=x"`
	One3   ScoreAnswer `typesafe:"kind=score;name=one3;levels=x"`
	One4   ScoreAnswer `typesafe:"kind=score;name=one4;levels=x"`
	One5   ScoreAnswer `typesafe:"kind=score;name=one5;levels=x"`
	One6   ScoreAnswer `typesafe:"kind=score;name=one6;levels=x"`
	One7   ScoreAnswer `typesafe:"kind=score;name=one7;levels=x"`
}

// escapedNamesAnswers types escaped-names.json, whose answer names hold
// what its JSON escapes decode to. A tag is a Go string literal, so a
// backslash of the tag grammar's \\ escape is written twice again here.
type escapedNamesAnswers struct {
	Special   NoulAnswer   `typesafe:"kind=noul;name=spécial"`
	Quoted    NoulAnswer   `typesafe:"kind=noul;name=quote\"d"`
	Backslash ChoiceAnswer `typesafe:"kind=choice;name=back\\\\slash;options=a|b"`
	Newline   NoulAnswer   `typesafe:"kind=noul;name=new\nline"`
	Globe     ScoreAnswer  `typesafe:"kind=score;name=globe 🌍;levels=low|mid|high"`
	Slash     NoulAnswer   `typesafe:"kind=noul;name=sl/ash"`
}

// escapedMemberNamesAnswers types escaped-member-names.json.
type escapedMemberNamesAnswers struct {
	Spam NoulAnswer   `typesafe:"kind=noul;name=spam"`
	Tone ChoiceAnswer `typesafe:"kind=choice;name=tone;options=friendly|hostile"`
	Risk ScoreAnswer  `typesafe:"kind=score;name=risk;levels=duplicated"`
}

// riskAnswers types structured-legend.json: one score with one structured
// level.
type riskAnswers struct {
	Risk ScoreAnswer `typesafe:"kind=score;name=risk;levels=duplicated"`
}

// loneSurrogateAnswers types deviation-lone-surrogate.json, which Go
// accepts (plan Appendix B).
type loneSurrogateAnswers struct {
	Quality ScoreAnswer `typesafe:"kind=score;name=quality;levels=bad|note"`
}

// unknownTypeAnswers types unknown-answer-type.json: spam, and an optional
// field named like the answer of the unknown type aurora, which the
// decoder skips, so the field is absent as Answers() lacks it.
type unknownTypeAnswers struct {
	Spam    NoulAnswer `typesafe:"kind=noul;name=spam"`
	Mystery NoulAnswer `typesafe:"kind=noul;name=mystery;optional"`
}

// spamAnswers types parity-big-exp-unknown.json, whose one answer is spam.
type spamAnswers struct {
	Spam NoulAnswer `typesafe:"kind=noul;name=spam"`
}

// noAnswers types no-answers.json, which has no answers member: a struct
// needs a field, and an optional one is absent.
type noAnswers struct {
	Spam NoulAnswer `typesafe:"kind=noul;name=spam;optional"`
}

// agreeFunc checks one fixture's body with its struct type.
type agreeFunc func(t *testing.T, body []byte) any

// agreeAs returns the agreeFunc of the struct type T: checkAgreement[T].
func agreeAs[T any]() agreeFunc {
	return func(t *testing.T, body []byte) any { return checkAgreement[T](t, body) }
}

// TestDecodeAsAgreesWithAnswers is AC-F12's differential test: for every
// fixture the decoder accepts as a System One body, the struct DecodeAs
// fills holds, field by field, the answer Answers() holds under the field's
// name (the same values, the same last-wins choice for duplicates.json, the
// same skip for unknown-answer-type.json), every answer of Answers() has a
// field, and Ask over a Recorder replying with the fixture gives the same
// struct. A fixture the decoder accepts without a struct type here fails
// the test, so a new fixture joins it. models.json, the one accepted
// fixture that is not a System One body, is a list-models body.
func TestDecodeAsAgreesWithAnswers(t *testing.T) {
	tests := map[string]struct {
		fixture string
		agree   agreeFunc
		// then checks the struct beyond agreement, or is nil.
		then func(t *testing.T, got any)
	}{
		"success: result.json":    {fixture: "result.json", agree: agreeAs[reviewAnswers]()},
		"success: type-last.json": {fixture: "type-last.json", agree: agreeAs[reviewAnswers]()},
		"success: duplicates.json": {
			fixture: "duplicates.json",
			agree:   agreeAs[duplicatesAnswers](),
			then: func(t *testing.T, got any) {
				t.Helper()
				d := got.(duplicatesAnswers)
				friendly, _ := d.Tone.Probability("friendly")
				fine, _ := d.Quality.Description(1)
				risk, _ := d.Risk.Description(0)
				type view struct {
					Tone                       string
					Friendly, Spam             float64
					Fine, Risk                 string
					Gone, Broken, MysteryThere bool
				}
				want := view{Tone: "friendly", Friendly: 0.9, Spam: 0.98, Fine: "fine", Risk: `{"summary":"low"}`}
				gotView := view{
					Tone: d.Tone.Choice(), Friendly: friendly, Spam: d.Spam.Noul(), Fine: fine.Text(), Risk: string(risk.JSON()),
					Gone: d.Gone.Present(), Broken: d.Broken.Present(), MysteryThere: d.Mystery.Present(),
				}
				if diff := gocmp.Diff(want, gotView); diff != "" {
					t.Errorf("duplicates.json: not the last answer at every level (-want +got):\n%s", diff)
				}
			},
		},
		"success: result-20.json":                   {fixture: "result-20.json", agree: agreeAs[result20Answers]()},
		"success: score-flood-mini.json":            {fixture: "score-flood-mini.json", agree: agreeAs[scoreFloodMiniAnswers]()},
		"success: escaped-names.json":               {fixture: "escaped-names.json", agree: agreeAs[escapedNamesAnswers]()},
		"success: escaped-member-names.json":        {fixture: "escaped-member-names.json", agree: agreeAs[escapedMemberNamesAnswers]()},
		"success: structured-legend.json":           {fixture: "structured-legend.json", agree: agreeAs[riskAnswers]()},
		"success: deviation-lone-surrogate.json":    {fixture: "deviation-lone-surrogate.json", agree: agreeAs[loneSurrogateAnswers]()},
		"success: parity-big-exp-unknown.json":      {fixture: "parity-big-exp-unknown.json", agree: agreeAs[spamAnswers]()},
		"success: no-answers.json":                  {fixture: "no-answers.json", agree: agreeAs[noAnswers]()},
		"success: structured-legend-flood-1k.json":  {fixture: "structured-legend-flood-1k.json", agree: agreeAs[flood1kAnswers]()},
		"success: structured-legend-flood-10k.json": {fixture: "structured-legend-flood-10k.json", agree: agreeAs[flood10kAnswers]()},
		"success: unknown-answer-type.json": {
			fixture: "unknown-answer-type.json",
			agree:   agreeAs[unknownTypeAnswers](),
			then: func(t *testing.T, got any) {
				t.Helper()
				if u := got.(unknownTypeAnswers); u.Mystery.Present() || u.Spam.Noul() != 0.9 {
					t.Errorf("unknown-answer-type.json: Mystery present %v, Spam %v; want false, 0.9", u.Mystery.Present(), u.Spam.Noul())
				}
			},
		},
	}
	var accepted []string
	for _, name := range testsupport.FixtureNames(t, "*.json") {
		var r SystemOneResponse
		if r.UnmarshalJSON(testsupport.Fixture(t, name)) == nil {
			accepted = append(accepted, name)
		}
	}
	var typed []string
	for _, tt := range tests {
		typed = append(typed, tt.fixture)
	}
	slices.Sort(typed)
	if diff := gocmp.Diff(typed, accepted); diff != "" {
		t.Fatalf("the fixtures the decoder accepts and the struct types here differ (-typed +accepted):\n%s", diff)
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := tt.agree(t, testsupport.Fixture(t, tt.fixture))
			if tt.then != nil {
				tt.then(t, got)
			}
		})
	}
}

// checkAgreement decodes body with DecodeAs[T] and with Ask[T], checks both
// against Answers(), and returns the decoded T.
func checkAgreement[T any](t *testing.T, body []byte) T {
	t.Helper()
	var resp SystemOneResponse
	if err := resp.UnmarshalJSON(body); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	got, err := DecodeAs[T](&resp)
	if err != nil {
		t.Fatalf("DecodeAs: %v", err)
	}
	p := typedPlanFor[T]()
	answers := resp.Answers()
	v := reflect.ValueOf(got)
	var typed []string
	for _, f := range p.fields {
		a, ok := answers.Get(f.name)
		if !ok && !f.optional {
			t.Errorf("Answers() has no %q, which the struct requires, and DecodeAs did not fail", f.name)
		}
		if ok {
			typed = append(typed, f.name)
		}
		var want any
		switch f.kind {
		case wire.KindNoul:
			want, _ = a.Noul()
		case wire.KindChoice:
			want, _ = a.Choice()
		default:
			want, _ = a.Score()
		}
		if diff := gocmp.Diff(want, v.Field(f.index).Interface(), typedCmp); diff != "" {
			t.Errorf("field for %q: DecodeAs and Answers() differ (-Answers +DecodeAs):\n%s", f.name, diff)
		}
	}
	var all []string
	for name := range answers.All() {
		all = append(all, name)
	}
	if diff := gocmp.Diff(all, typed); diff != "" {
		t.Errorf("answers with a field, in Answers() order (-Answers +typed):\n%s", diff)
	}

	rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(200, body)}}
	asked, err := Ask[T](t.Context(), newTestClient(t, rec), "x", Retry(NoRetry()))
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if diff := gocmp.Diff(got, asked, typedCmp); diff != "" {
		t.Errorf("Ask and DecodeAs differ (-DecodeAs +Ask):\n%s", diff)
	}
	return got
}

// TestFloodTypeTags checks the generated tags of decodeas_flood_test.go: a
// flood's score lists the levels 0 to n-1, as many as its fixture's legend
// has.
func TestFloodTypeTags(t *testing.T) {
	tests := map[string]struct {
		typ    reflect.Type
		levels int
	}{
		"success: flood1kAnswers lists 10^3 levels":  {typ: reflect.TypeFor[flood1kAnswers](), levels: 1000},
		"success: flood10kAnswers lists 10^4 levels": {typ: reflect.TypeFor[flood10kAnswers](), levels: 10000},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			levels := make([]string, tt.levels)
			for i := range levels {
				levels[i] = strconv.Itoa(i)
			}
			want := "kind=score;name=flood;levels=" + strings.Join(levels, "|")
			if got := tt.typ.Field(2).Tag.Get("typesafe"); got != want {
				t.Errorf("%v.Flood's tag is not the %d levels 0 to %d", tt.typ, tt.levels, tt.levels-1)
			}
		})
	}
}
