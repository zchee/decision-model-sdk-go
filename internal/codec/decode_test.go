//go:build !go1.28 && (amd64 || arm64)

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

package codec

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// TestDecodeFixtures checks the verdict on every fixture under testdata: each
// malformed-*.json and deviation-big-exp-noul.json is refused with the field
// path testdata/README.md names, and every other file is accepted. The log
// is the fixture table of the W2.0 report.
func TestDecodeFixtures(t *testing.T) {
	for _, name := range testsupport.FixtureNames(t, "malformed-*.json") {
		if _, ok := wantReject[name]; !ok {
			t.Errorf("%s has no row in wantReject", name)
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n%-38s %-28s %s\n", "fixture", "verdict (README)", "observed")
	for _, name := range testsupport.FixtureNames(t, "*.json") {
		body := testsupport.Fixture(t, name)
		var err error
		if name == "models.json" {
			err = DecodeModels(body, new(wire.ModelList))
		} else {
			_, err = DecodeSystemOne(body, nil, "", new(wire.SystemOneResult))
		}
		want, reject := wantReject[name]
		verdict, observed := "accept", "accept"
		if reject {
			verdict = "reject at " + want
		}
		if err != nil {
			observed = "reject at " + pathOf(t, err)
		}
		if observed != verdict {
			t.Errorf("%s: %s (%v), want %s", name, observed, err, verdict)
		}
		fmt.Fprintf(&sb, "%-38s %-28s %s\n", name, verdict, observed)
	}
	t.Log(sb.String())
}

// TestDecodedValues checks what the decoder returns, not only that it
// accepts: wire order, escapes, exact legend bytes and unknown types
// dropped; and that the result is the same when the question set the
// response answers is given, which only changes where its strings live.
func TestDecodedValues(t *testing.T) {
	floodLevel := func(i int) string {
		if i%2 == 0 {
			return `{"summary":"level ` + strconv.Itoa(i) + `","examples":["example ` + strconv.Itoa(i) + `"]}`
		}
		return `["level ` + strconv.Itoa(i) + `",{"note":null}]`
	}
	tests := map[string]struct {
		fixture string
		check   func(t *testing.T, res *wire.SystemOneResult, skipped Skipped, st stats)
	}{
		"success: result.json in wire order": {
			fixture: "result.json",
			check: func(t *testing.T, res *wire.SystemOneResult, _ Skipped, _ stats) {
				want := []wire.AnswerEntry{
					{Name: "spam", Answer: wire.Answer{Kind: wire.KindNoul, Noul: wire.NoulAnswer{Noul: 0.98}}},
					{Name: "tone", Answer: wire.Answer{Kind: wire.KindChoice, Choice: wire.ChoiceAnswer{
						Choice: "friendly", Confidence: 0.9,
						Probabilities: []wire.LabelProbability{{Label: "friendly", Probability: 0.9}, {Label: "hostile", Probability: 0.1}},
					}}},
					{Name: "quality", Answer: wire.Answer{Kind: wire.KindScore, Score: wire.ScoreAnswer{
						Score: 1.7, Confidence: 0.8,
						Legend: []wire.LegendEntry{
							{Level: 0, Description: wire.Content{Text: "bad"}},
							{Level: 1, Description: wire.Content{Text: "ok"}},
							{Level: 2, Description: wire.Content{Text: "great"}},
						},
						Probabilities: []wire.LevelProbability{{Level: 0, Probability: 0.1}, {Level: 1, Probability: 0.1}, {Level: 2, Probability: 0.8}},
					}}},
				}
				if diff := gocmp.Diff(want, res.Answers.Entries()); diff != "" {
					t.Errorf("answers (-want +got):\n%s", diff)
				}
				wantUsage := wire.Usage{InputTokens: 12, OutputTokens: 3, HasInputTokens: true, HasOutputTokens: true}
				if diff := gocmp.Diff(wantUsage, res.Usage); diff != "" || res.Model != "jev-latest" {
					t.Errorf("model %q, usage (-want +got):\n%s", res.Model, diff)
				}
			},
		},
		"success: type-last.json equals result.json": {
			fixture: "type-last.json",
			check: func(t *testing.T, res *wire.SystemOneResult, _ Skipped, _ stats) {
				want, _, _, err := decodeBody(t, testsupport.Fixture(t, "result.json"), nil, "")
				if err != nil {
					t.Fatal(err)
				}
				if diff := gocmp.Diff(want.Answers.Entries(), res.Answers.Entries()); diff != "" {
					t.Errorf("answers (-result.json +type-last.json):\n%s", diff)
				}
				if res.Model != want.Model || res.Usage != want.Usage {
					t.Errorf("model/usage = %q %+v, want %q %+v", res.Model, res.Usage, want.Model, want.Usage)
				}
			},
		},
		"success: escaped names decode": {
			fixture: "escaped-names.json",
			check: func(t *testing.T, res *wire.SystemOneResult, _ Skipped, _ stats) {
				want := []string{"spécial", `quote"d`, `back\slash`, "new\nline", "globe 🌍", "sl/ash"}
				if diff := gocmp.Diff(want, names(res)); diff != "" {
					t.Errorf("names (-want +got):\n%s", diff)
				}
			},
		},
		"success: escaped member names keep the level's escapes": {
			fixture: "escaped-member-names.json",
			check: func(t *testing.T, res *wire.SystemOneResult, _ Skipped, st stats) {
				if res.Model != "jev-latest" || res.Usage.InputTokens != 12 {
					t.Errorf("model/usage = %q %+v", res.Model, res.Usage)
				}
				r, _ := res.Answers.Get("risk")
				want := `{"summ\u0061ry":"duplicated","examples":["charged \"twice\""]}`
				if got := string(r.Score.Legend[0].Description.JSON); got != want {
					t.Errorf("level 0 = %s, want %s", got, want)
				}
				if st.lazyPasses != 1 {
					t.Errorf("lazy passes = %d, want 1", st.lazyPasses)
				}
			},
		},
		"success: structured legend bytes": {
			fixture: "structured-legend.json",
			check: func(t *testing.T, res *wire.SystemOneResult, _ Skipped, _ stats) {
				r, _ := res.Answers.Get("risk")
				if got, want := string(r.Score.Legend[0].Description.JSON), `{"summary":"duplicated","examples":["charged twice"]}`; got != want {
					t.Errorf("level 0 = %s, want %s", got, want)
				}
			},
		},
		"success: lone surrogate is U+FFFD in text and kept in the bytes": {
			fixture: "deviation-lone-surrogate.json",
			check: func(t *testing.T, res *wire.SystemOneResult, _ Skipped, _ stats) {
				q, _ := res.Answers.Get("quality")
				if got := q.Score.Legend[0].Description.Text; got != "bad \uFFFD" {
					t.Errorf("text level = %q", got)
				}
				if got := string(q.Score.Legend[1].Description.JSON); got != `{"note":"\ud800"}` {
					t.Errorf("structured level = %s", got)
				}
			},
		},
		"success: unknown answer type dropped and reported": {
			fixture: "unknown-answer-type.json",
			check: func(t *testing.T, res *wire.SystemOneResult, skipped Skipped, _ stats) {
				if diff := gocmp.Diff([]string{"spam"}, names(res)); diff != "" {
					t.Errorf("names (-want +got):\n%s", diff)
				}
				if diff := gocmp.Diff([]SkippedAnswer{{Name: "mystery", Type: "aurora"}}, skipped.Named()); diff != "" || skipped.Count != 1 {
					t.Errorf("skipped count %d, named (-want +got):\n%s", skipped.Count, diff)
				}
			},
		},
		"success: no answers member": {
			fixture: "no-answers.json",
			check: func(t *testing.T, res *wire.SystemOneResult, _ Skipped, _ stats) {
				if res.Answers.Len() != 0 || res.Model != "jev-latest" {
					t.Errorf("got %d answers, model %q", res.Answers.Len(), res.Model)
				}
			},
		},
		"success: 20 answers in wire order": {
			fixture: "result-20.json",
			check: func(t *testing.T, res *wire.SystemOneResult, _ Skipped, _ stats) {
				if res.Answers.Len() != 20 {
					t.Errorf("got %d answers, want 20", res.Answers.Len())
				}
			},
		},
		"success: 1k structured flood, exact bytes per level": {
			fixture: "structured-legend-flood-1k.json",
			check: func(t *testing.T, res *wire.SystemOneResult, _ Skipped, _ stats) {
				f, _ := res.Answers.Get(testsupport.FloodAnswer)
				if len(f.Score.Legend) != 1000 || len(f.Score.Probabilities) != 1000 {
					t.Fatalf("legend %d, probabilities %d, want 1000 each", len(f.Score.Legend), len(f.Score.Probabilities))
				}
				for i, l := range f.Score.Legend {
					if l.Level != uint32(i) || string(l.Description.JSON) != floodLevel(i) {
						t.Fatalf("level %d = %d %s, want %s", i, l.Level, l.Description.JSON, floodLevel(i))
					}
				}
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			res, skipped, st, err := decodeBody(t, testsupport.Fixture(t, tt.fixture), nil, "")
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, res, skipped, st)
			q := questionsFor(t, res)
			interned, skipped2, st2, err := decodeBody(t, testsupport.Fixture(t, tt.fixture), q, res.Model)
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, interned, skipped2, st2)
			if diff := gocmp.Diff(res.Answers.Entries(), interned.Answers.Entries()); diff != "" {
				t.Errorf("interning changed the answers (-without +with questions):\n%s", diff)
			}
		})
	}
}

// TestDecodeDoesNotAliasBody overwrites the body after each decode and
// checks that the result did not change: no string or level of it borrows
// the body, with or without the question set to intern against.
func TestDecodeDoesNotAliasBody(t *testing.T) {
	for _, name := range testsupport.FixtureNames(t, "*.json") {
		if _, reject := wantReject[name]; reject {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if name == "models.json" {
				body := testsupport.Fixture(t, name)
				var got, want wire.ModelList
				if err := DecodeModels(body, &got); err != nil {
					t.Fatal(err)
				}
				if err := DecodeModels(testsupport.Fixture(t, name), &want); err != nil {
					t.Fatal(err)
				}
				for i := range body {
					body[i] = 'x'
				}
				if diff := gocmp.Diff(want, got); diff != "" {
					t.Errorf("the cards changed with the body (-want +got):\n%s", diff)
				}
				return
			}
			want, _, _, err := decodeBody(t, testsupport.Fixture(t, name), nil, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, q := range []*wire.Prepared{nil, questionsFor(t, want)} {
				body := testsupport.Fixture(t, name)
				var got wire.SystemOneResult
				if _, err := DecodeSystemOne(body, q, "", &got); err != nil {
					t.Fatal(err)
				}
				for i := range body {
					body[i] = 'x'
				}
				if diff := gocmp.Diff(want.Answers.Entries(), got.Answers.Entries()); diff != "" || got.Model != want.Model {
					t.Errorf("questions %t: the result changed with the body: model %q, answers (-want +got):\n%s", q != nil, got.Model, diff)
				}
			}
		})
	}
}

// TestInterningReturnsRequestStrings checks plan 6.2.5: a name, a choice and
// its labels, a text level and a structured level equal to the question
// set's own are the set's strings and bytes, and the model is the one the
// request named.
func TestInterningReturnsRequestStrings(t *testing.T) {
	for _, fixture := range []string{"result.json", "structured-legend.json", "result-20.json", "escaped-names.json"} {
		t.Run(fixture, func(t *testing.T) {
			first, _, _, err := decodeBody(t, testsupport.Fixture(t, fixture), nil, "")
			if err != nil {
				t.Fatal(err)
			}
			q := questionsFor(t, first)
			model := strings.Clone(first.Model)
			res, _, st, err := decodeBody(t, testsupport.Fixture(t, fixture), q, model)
			if err != nil {
				t.Fatal(err)
			}
			if st.arena != 0 {
				t.Errorf("arena = %d bytes, want 0: every string should be the request's", st.arena)
			}
			same := func(what, got, want string) {
				t.Helper()
				if got != want || (len(want) > 0 && unsafe.StringData(got) != unsafe.StringData(want)) {
					t.Errorf("%s %q is not the request's string", what, got)
				}
			}
			same("model", res.Model, model)
			for _, e := range res.Answers.Entries() {
				pq, ok := q.Lookup(e.Name)
				if !ok {
					t.Fatalf("answer %q has no question", e.Name)
				}
				same("name", e.Name, pq.Name)
				switch e.Answer.Kind {
				case wire.KindChoice:
					for i, p := range e.Answer.Choice.Probabilities {
						same("label", p.Label, pq.Options[i])
					}
					same("choice", e.Answer.Choice.Choice, pq.Options[slicesIndex(pq.Options, e.Answer.Choice.Choice)])
				case wire.KindScore:
					for _, l := range e.Answer.Score.Legend {
						want := pq.Levels[l.Level]
						if want.JSON != nil {
							if unsafe.SliceData(l.Description.JSON) != unsafe.SliceData(want.JSON) {
								t.Errorf("level %d bytes are not the request's", l.Level)
							}
							continue
						}
						same("level text", l.Description.Text, want.Text)
					}
				}
			}
		})
	}
}

func slicesIndex(s []string, v string) int {
	for i := range s {
		if s[i] == v {
			return i
		}
	}
	return 0
}

// TestLastWins checks plan 6.2.4's one duplicate rule at every level: the
// last occurrence wins, a repeated answer name keeps its first position, and
// the lazy pass picks the same occurrence as the visitor.
func TestLastWins(t *testing.T) {
	const head = `{"model":"m","usage":{"input_tokens":1,"output_tokens":1},`
	noul := func(n string) string { return `{"type":"noul","noul":` + n + `}` }
	score := func(legend string) string {
		return `{"type":"score","score":0,"confidence":1,"legend":` + legend + `,"probabilities":{"0":1}}`
	}
	type want struct {
		names  []string
		model  string
		input  uint64
		noul   map[string]float64
		legend map[string][]wire.LegendEntry
		probs  map[string][]wire.LabelProbability
	}
	tests := map[string]struct {
		body string
		want want
	}{
		"success: repeated answers member resets every answer": {
			body: head + `"answers":{"x":` + noul("0.1") + `},"answers":{"y":` + noul("0.2") + `}}`,
			want: want{names: []string{"y"}, noul: map[string]float64{"y": 0.2}},
		},
		"success: repeated name keeps its first position and takes the last value": {
			body: head + `"answers":{"a":` + noul("0.1") + `,"b":` + noul("0.2") + `,"a":` + noul("0.3") + `}}`,
			want: want{names: []string{"a", "b"}, noul: map[string]float64{"a": 0.3, "b": 0.2}},
		},
		"success: repeated type member overwrites": {
			body: head + `"answers":{"a":{"type":"choice","noul":0.5,"type":"noul"}}}`,
			want: want{names: []string{"a"}, noul: map[string]float64{"a": 0.5}},
		},
		"success: repeated model and usage overwrite": {
			body: `{"model":"m1","usage":{"input_tokens":1,"output_tokens":1},"model":"m2","usage":{"input_tokens":7,"output_tokens":1},"answers":{}}`,
			want: want{names: nil, model: "m2", input: 7},
		},
		"success: a duplicate repairs an invalid answer": {
			body: head + `"answers":{"a":{"type":"noul"},"a":` + noul("1") + `}}`,
			want: want{names: []string{"a"}, noul: map[string]float64{"a": 1}},
		},
		"success: a duplicate repairs an answer that is not an object": {
			body: head + `"answers":{"a":7,"a":` + noul("1") + `}}`,
			want: want{names: []string{"a"}, noul: map[string]float64{"a": 1}},
		},
		"success: a duplicate repairs an invalid answers member": {
			body: head + `"answers":[],"answers":{"a":` + noul("1") + `}}`,
			want: want{names: []string{"a"}, noul: map[string]float64{"a": 1}},
		},
		"success: a duplicate unknown type drops the answer": {
			body: head + `"answers":{"a":` + noul("1") + `,"b":` + noul("1") + `,"a":{"type":"aurora"}}}`,
			want: want{names: []string{"b"}, noul: map[string]float64{"b": 1}},
		},
		"success: a repeated probability repairs a bad value": {
			body: head + `"answers":{"c":{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":"x","b":0.5,"a":0.25}}}}`,
			want: want{names: []string{"c"}, probs: map[string][]wire.LabelProbability{"c": {{Label: "a", Probability: 0.25}, {Label: "b", Probability: 0.5}}}},
		},
		"success: repeated text level keeps its first position": {
			body: head + `"answers":{"s":` + score(`{"0":"a","1":"b","0":"c"}`) + `}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{Text: "c"}},
				{Level: 1, Description: wire.Content{Text: "b"}},
			}}},
		},
		"success: repeated structured level takes the last bytes": {
			body: head + `"answers":{"s":` + score(`{"0":{"v":1},"1":"t","0":{"v":2}}`) + `}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{JSON: []byte(`{"v":2}`)}},
				{Level: 1, Description: wire.Content{Text: "t"}},
			}}},
		},
		"success: structured level superseded by text": {
			body: head + `"answers":{"s":` + score(`{"0":{"v":1},"0":"t"}`) + `}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{Text: "t"}},
			}}},
		},
		"success: text level superseded by structured": {
			body: head + `"answers":{"s":` + score(`{"0":"t","0":[1]}`) + `}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{JSON: []byte(`[1]`)}},
			}}},
		},
		"success: a bad level value superseded by a good one": {
			body: head + `"answers":{"s":` + score(`{"0":5,"0":"ok"}`) + `}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{Text: "ok"}},
			}}},
		},
		"success: level keys 1 and 01 are one level": {
			body: head + `"answers":{"s":` + score(`{"1":{"v":1},"01":{"v":2}}`) + `}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 1, Description: wire.Content{JSON: []byte(`{"v":2}`)}},
			}}},
		},
		"success: repeated legend member takes the last": {
			body: head + `"answers":{"s":{"type":"score","score":0,"confidence":1,"legend":{"0":{"v":1}},"probabilities":{"0":1},"legend":{"0":{"v":9}}}}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{JSON: []byte(`{"v":9}`)}},
			}}},
		},
		"success: lazy pass reads the last occurrence of a repeated answer": {
			body: head + `"answers":{"s":` + score(`{"0":{"v":1}}`) + `,"t":` + noul("0") + `,"s":` + score(`{"0":{"v":3}}`) + `}}`,
			want: want{names: []string{"s", "t"}, noul: map[string]float64{"t": 0}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{JSON: []byte(`{"v":3}`)}},
			}}},
		},
		"success: lazy pass reads the last answers member, escaped or not": {
			body: head + `"answers":{"s":` + score(`{"0":{"v":1}}`) + `},"\u0061nswers":{"s":` + score(`{"0":{"v":4}}`) + `}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{JSON: []byte(`{"v":4}`)}},
			}}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			res, _, _, err := decodeBody(t, []byte(tt.body), nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if diff := gocmp.Diff(tt.want.names, names(res)); diff != "" {
				t.Errorf("names (-want +got):\n%s", diff)
			}
			if tt.want.model != "" && (res.Model != tt.want.model || res.Usage.InputTokens != tt.want.input) {
				t.Errorf("model %q input %d, want %q %d", res.Model, res.Usage.InputTokens, tt.want.model, tt.want.input)
			}
			for n, f := range tt.want.noul {
				if a, _ := res.Answers.Get(n); a.Noul.Noul != f {
					t.Errorf("%s.noul = %v, want %v", n, a.Noul.Noul, f)
				}
			}
			for n, l := range tt.want.legend {
				a, _ := res.Answers.Get(n)
				if diff := gocmp.Diff(l, a.Score.Legend); diff != "" {
					t.Errorf("%s.legend (-want +got):\n%s", n, diff)
				}
			}
			for n, p := range tt.want.probs {
				a, _ := res.Answers.Get(n)
				if diff := gocmp.Diff(p, a.Choice.Probabilities); diff != "" {
					t.Errorf("%s.probabilities (-want +got):\n%s", n, diff)
				}
			}
		})
	}
}

// duplicatesLastWins is testdata/duplicates.json resolved as Python 0.7.1
// resolves it (testdata/README.md; internal/testsupport/fixtures_test.go).
const duplicatesLastWins = `{"model":"jev-latest","usage":{"input_tokens":12},"answers":{"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},"spam":{"type":"noul","noul":0.98},"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"fine","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}},"risk":{"type":"score","score":0,"confidence":1,"legend":{"0":{"summary":"low"}},"probabilities":{"0":1}}}}`

// TestDuplicatesLastWins decodes testdata/duplicates.json and the same body
// without repeats: both give the same answers, model and usage (last wins
// at every level, first position kept), and the unknown answer of the
// superseded "answers" member is not reported.
func TestDuplicatesLastWins(t *testing.T) {
	got, skipped, _, err := decodeBody(t, testsupport.Fixture(t, "duplicates.json"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if skipped.Count != 0 {
		t.Errorf("skipped = %+v, want none (mystery was superseded)", skipped.Named())
	}
	want, _, _, err := decodeBody(t, []byte(duplicatesLastWins), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if diff := gocmp.Diff(want.Answers.Entries(), got.Answers.Entries()); diff != "" {
		t.Errorf("answers (-last-wins body +duplicates.json):\n%s", diff)
	}
	if got.Model != want.Model || got.Usage != want.Usage {
		t.Errorf("model/usage = %q %+v, want %q %+v", got.Model, got.Usage, want.Model, want.Usage)
	}
	if got.Usage.HasOutputTokens {
		t.Errorf("usage kept output_tokens from the superseded usage member")
	}
}

// TestReleaseDropsBodyReferences checks that a decoder going back to the
// pool keeps no string of the last body and no string of the last question
// set: its scratch maps and lists are empty, so neither is kept alive by the
// pool. The body holds a choice of 20 labels and a flood legend, so every
// map the decode hashes into is used.
func TestReleaseDropsBodyReferences(t *testing.T) {
	var probs []string
	for i := range 20 {
		probs = append(probs, `"label-`+strconv.Itoa(i)+`":0.05`)
	}
	body := `{"model":"m","usage":{},"answers":{"c":{"type":"choice","choice":"label-0","confidence":1,"probabilities":{` + strings.Join(probs, ",") + `}}}}`
	first, _, _, err := decodeBody(t, []byte(body), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range [][]byte{[]byte(body), testsupport.Fixture(t, "structured-legend-flood-1k.json"), testsupport.Fixture(t, "result-20.json")} {
		d := newDecoder()
		var res wire.SystemOneResult
		if _, err := d.systemOne(b, questionsFor(t, first), "m", &res); err != nil {
			t.Fatal(err)
		}
		d.release()
		v := &d.v
		if v.body != "" || len(v.set) != 0 || len(v.setIdx) != 0 || len(v.strIdx) != 0 || len(v.probs) != 0 || len(v.legend) != 0 || len(v.cards) != 0 || v.curKey != "" || v.model != "" {
			t.Errorf("visitor keeps the body: body %d bytes, set %d, setIdx %d, strIdx %d, probs %d, legend %d, cards %d, curKey %q, model %q",
				len(v.body), len(v.set), len(v.setIdx), len(v.strIdx), len(v.probs), len(v.legend), len(v.cards), v.curKey, v.model)
		}
		if len(d.optIdx) != 0 || len(d.raws) != 0 || len(d.strs) != 0 || len(d.jsons) != 0 {
			t.Errorf("decoder keeps references: optIdx %d, raws %d, strs %d, jsons %d", len(d.optIdx), len(d.raws), len(d.strs), len(d.jsons))
		}
		for i, n := range d.nodes {
			if n.Exists() {
				t.Errorf("node %d still set", i)
			}
		}
	}
}
