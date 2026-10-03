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

package alloctest

// A copy of the root package's prepare_cases_test.go, which the root
// package's BenchmarkPrepare shares: TestAllocPrepare pins the counts of
// these sets, BenchmarkPrepare times them.

import (
	"strconv"
	"strings"

	decision "github.com/zchee/decision-model-sdk-go"
)

// prepareSink keeps every prepared set reachable, so that the compiler cannot
// drop or stack-allocate what Prepare builds.
var prepareSink *decision.Prepared

// prepareCases are the question sets whose Prepare cost the performance
// ledger records (docs/perf/ledger.md), keyed by the name of their
// sub-benchmark; the name starts with the ledger's case number. Each
// function builds a fresh set, so that a measured section never includes
// building it.
//
// The n8 sets measure the falsiness check of a raw "score" question's JSON
// criteria (engine.FalsyJSON): each is paired with a control that differs
// only in the type string, "Score" instead of "score", so that the check
// does not run while the writer does the same work.
var prepareCases = map[string]func() *decision.Questions{
	// The sketch set: one question of each kind.
	"c1-sketch": func() *decision.Questions {
		return sketchQuestions("")
	},
	"c2-noul-short": func() *decision.Questions {
		return decision.NewQuestions().Noul("spam", decision.Noul{Instructions: decision.Text("Spam?")})
	},
	"c3-choice-20x10": func() *decision.Questions {
		qs := decision.NewQuestions()
		for i := range 20 {
			opts := make(decision.Options, 10)
			for j := range opts {
				opts[j] = decision.Option{Label: "option-" + strconv.Itoa(j), Description: decision.Text("what option " + strconv.Itoa(j) + " means")}
			}
			qs.Choice("choice-"+strconv.Itoa(i), decision.Choice{Instructions: decision.Text("Which option fits the message?"), Options: opts})
		}
		return qs
	},
	"c4a-score-20x8-text": func() *decision.Questions {
		return scoreQuestions(func(level int) decision.Content {
			return decision.Text("level " + strconv.Itoa(level) + ": how urgent the message is")
		})
	},
	"c4b-score-20x8-json": func() *decision.Questions {
		return scoreQuestions(func(level int) decision.Content { return decision.JSON([]byte(prettyLevel(level))) })
	},
	"c5-raw-100x3": func() *decision.Questions {
		qs := decision.NewQuestions()
		for i := range 100 {
			qs.Raw("raw-"+strconv.Itoa(i), decision.RawQuestion{Type: "noul", Fields: map[string]any{
				"instructions": "Is message " + strconv.Itoa(i) + " spam?",
				"weight":       0.5,
				"meta": map[string]any{
					"source": "crm",
					"tags":   []any{"billing", "priority"},
					"limits": map[string]any{"min": 1, "max": 10},
				},
			}})
		}
		return qs
	},
	// The sketch with every control character, U+2028, U+2029 and an emoji
	// appended to each name and text, so that every string takes the
	// escaper's slow path.
	"c6-escapes": func() *decision.Questions {
		var odd strings.Builder
		for c := range 0x20 {
			odd.WriteByte(byte(c))
		}
		odd.WriteString("  \U0001F600")
		return sketchQuestions(odd.String())
	},
	"n8a-array-score": func() *decision.Questions {
		return rawScoreQuestions("score", 20, func() any { return decision.RawJSON(prettyLevels()) })
	},
	"n8a-array-control": func() *decision.Questions {
		return rawScoreQuestions("Score", 20, func() any { return decision.RawJSON(prettyLevels()) })
	},
	"n8b-map-score": func() *decision.Questions {
		return rawScoreQuestions("score", 100, func() any { return decision.JSON([]byte(prettyScale)) })
	},
	"n8b-map-control": func() *decision.Questions {
		return rawScoreQuestions("Score", 100, func() any { return decision.JSON([]byte(prettyScale)) })
	},
}

// sketchQuestions returns the sketch set, one question of each kind (a
// noul, a choice, a score and a raw question), with suffix appended to
// every name and text.
func sketchQuestions(suffix string) *decision.Questions {
	return decision.NewQuestions().
		Noul("billing"+suffix, decision.Noul{Instructions: decision.Text("Is this about billing?" + suffix), Yes: decision.Text("payments or invoices" + suffix)}).
		Choice("tone"+suffix, decision.Choice{Instructions: decision.Text("What is the tone?" + suffix), Options: decision.Options{{Label: "calm" + suffix, Description: decision.Text("neutral or polite" + suffix)}, {Label: "angry" + suffix}}}).
		Score("urgency"+suffix, decision.Score{Levels: []decision.Content{decision.Text("can wait" + suffix), decision.Text("this week" + suffix), decision.Text("today" + suffix)}}).
		Raw("spam"+suffix, decision.RawQuestion{Type: "noul", Fields: map[string]any{"instructions": "Spam?" + suffix}})
}

// scoreQuestions returns 20 score questions of 8 levels each, level i being
// level(i).
func scoreQuestions(level func(i int) decision.Content) *decision.Questions {
	qs := decision.NewQuestions()
	for i := range 20 {
		levels := make([]decision.Content, 8)
		for j := range levels {
			levels[j] = level(j)
		}
		qs.Score("score-"+strconv.Itoa(i), decision.Score{Instructions: decision.Text("How urgent is the message?"), Levels: levels})
	}
	return qs
}

// rawScoreQuestions returns n raw questions of type typ with three fields: an
// instructions string, a weight and the criteria criteria() returns.
func rawScoreQuestions(typ string, n int, criteria func() any) *decision.Questions {
	qs := decision.NewQuestions()
	for i := range n {
		qs.Raw("raw-"+strconv.Itoa(i), decision.RawQuestion{Type: typ, Fields: map[string]any{
			"instructions": "How urgent is message " + strconv.Itoa(i) + "?",
			"weight":       0.5,
			"criteria":     criteria(),
		}})
	}
	return qs
}

// prettyLevel returns score level i as a pretty-printed JSON object, the way
// a caller's json.MarshalIndent would write it.
func prettyLevel(i int) string {
	return "{\n  \"score\": " + strconv.Itoa(i) + ",\n  \"label\": \"level " + strconv.Itoa(i) +
		"\",\n  \"examples\": [\n    \"a first example\",\n    \"a second example\"\n  ]\n}"
}

// prettyLevels returns the 8 levels of prettyLevel as one pretty-printed
// JSON array.
func prettyLevels() []byte {
	var sb strings.Builder
	sb.WriteString("[\n")
	for i := range 8 {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString(prettyLevel(i))
	}
	sb.WriteString("\n]")
	return []byte(sb.String())
}

// prettyScale is a pretty-printed JSON object with nested objects and
// arrays, used as the criteria of a raw score question.
const prettyScale = `{
  "scale": "urgency",
  "levels": {
    "0": {"label": "can wait", "examples": ["a newsletter", "a feature idea"], "sla_hours": 168},
    "1": {"label": "this month", "examples": ["a billing question", "a slow page"], "sla_hours": 72},
    "2": {"label": "this week", "examples": ["a failed payment", "a broken export"], "sla_hours": 24},
    "3": {"label": "today", "examples": ["an outage report", "a locked account"], "sla_hours": 4},
    "4": {"label": "now", "examples": ["data loss", "a security incident"], "sla_hours": 1}
  },
  "owner": {"team": "support", "escalation": ["on-call", "lead"], "reviewed": true, "version": 3}
}`
