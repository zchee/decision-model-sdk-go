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
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// prepareSink keeps every prepared set reachable, so that the compiler cannot
// drop or stack-allocate what Prepare builds.
var prepareSink *Prepared

// prepareCases are the question sets whose Prepare cost the performance
// ledger records (docs/perf/ledger.md, section W1.3), keyed by the name of
// their sub-benchmark; the name starts with the ledger's case number. Each
// function builds a fresh set, so that a measured section never includes
// building it.
//
// The n8 sets measure the falsiness check of a raw "score" question's JSON
// criteria (falsyJSON): each is paired with a control that differs only in
// the type string, "Score" instead of "score", so that the check does not run
// while the writer does the same work.
var prepareCases = map[string]func() *Questions{
	// The API sketch of the port plan's section 5.
	"c1-sketch": func() *Questions {
		return sketchQuestions("")
	},
	"c2-noul-short": func() *Questions {
		return NewQuestions().Noul("spam", Noul{Instructions: Text("Spam?")})
	},
	"c3-choice-20x10": func() *Questions {
		qs := NewQuestions()
		for i := range 20 {
			opts := make(Options, 10)
			for j := range opts {
				opts[j] = Option{Label: "option-" + strconv.Itoa(j), Description: Text("what option " + strconv.Itoa(j) + " means")}
			}
			qs.Choice("choice-"+strconv.Itoa(i), Choice{Instructions: Text("Which option fits the message?"), Options: opts})
		}
		return qs
	},
	"c4a-score-20x8-text": func() *Questions {
		return scoreQuestions(func(level int) Content {
			return Text("level " + strconv.Itoa(level) + ": how urgent the message is")
		})
	},
	"c4b-score-20x8-json": func() *Questions {
		return scoreQuestions(func(level int) Content { return JSON([]byte(prettyLevel(level))) })
	},
	"c5-raw-100x3": func() *Questions {
		qs := NewQuestions()
		for i := range 100 {
			qs.Raw("raw-"+strconv.Itoa(i), RawQuestion{Type: "noul", Fields: map[string]any{
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
	"c6-escapes": func() *Questions {
		var odd strings.Builder
		for c := range 0x20 {
			odd.WriteByte(byte(c))
		}
		odd.WriteString("  \U0001F600")
		return sketchQuestions(odd.String())
	},
	"n8a-array-score": func() *Questions {
		return rawScoreQuestions("score", 20, func() any { return RawJSON(prettyLevels()) })
	},
	"n8a-array-control": func() *Questions {
		return rawScoreQuestions("Score", 20, func() any { return RawJSON(prettyLevels()) })
	},
	"n8b-map-score": func() *Questions {
		return rawScoreQuestions("score", 100, func() any { return JSON([]byte(prettyScale)) })
	},
	"n8b-map-control": func() *Questions {
		return rawScoreQuestions("Score", 100, func() any { return JSON([]byte(prettyScale)) })
	},
}

// sketchQuestions returns the question set of the port plan's API sketch,
// with suffix appended to every name and text.
func sketchQuestions(suffix string) *Questions {
	return NewQuestions().
		Noul("billing"+suffix, Noul{Instructions: Text("Is this about billing?" + suffix), Yes: Text("payments or invoices" + suffix)}).
		Choice("tone"+suffix, Choice{Instructions: Text("What is the tone?" + suffix), Options: Options{{Label: "calm" + suffix, Description: Text("neutral or polite" + suffix)}, {Label: "angry" + suffix}}}).
		Score("urgency"+suffix, Score{Levels: []Content{Text("can wait" + suffix), Text("this week" + suffix), Text("today" + suffix)}}).
		Raw("spam"+suffix, RawQuestion{Type: "noul", Fields: map[string]any{"instructions": "Spam?" + suffix}})
}

// scoreQuestions returns 20 score questions of 8 levels each, level i being
// level(i).
func scoreQuestions(level func(i int) Content) *Questions {
	qs := NewQuestions()
	for i := range 20 {
		levels := make([]Content, 8)
		for j := range levels {
			levels[j] = level(j)
		}
		qs.Score("score-"+strconv.Itoa(i), Score{Instructions: Text("How urgent is the message?"), Levels: levels})
	}
	return qs
}

// rawScoreQuestions returns n raw questions of type typ with three fields: an
// instructions string, a weight and the criteria criteria() returns.
func rawScoreQuestions(typ string, n int, criteria func() any) *Questions {
	qs := NewQuestions()
	for i := range n {
		qs.Raw("raw-"+strconv.Itoa(i), RawQuestion{Type: typ, Fields: map[string]any{
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

// BenchmarkPrepare measures Questions.Prepare on each set of prepareCases.
// The set is built once, outside the loop: Prepare only reads it.
func BenchmarkPrepare(b *testing.B) {
	for _, name := range slices.Sorted(maps.Keys(prepareCases)) {
		qs := prepareCases[name]()
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				p, err := qs.Prepare()
				if err != nil {
					b.Fatal(err)
				}
				prepareSink = p
			}
		})
	}
}

// BenchmarkFalsyJSON measures the falsiness check of a raw score question's
// JSON criteria alone: below 32 compact bytes, its copy stays on the stack.
func BenchmarkFalsyJSON(b *testing.B) {
	inputs := map[string][]byte{
		"small-14B": []byte(`[ "low", "high" ]`),
		"array":     prettyLevels(),
		"map":       []byte(prettyScale),
	}
	for _, name := range slices.Sorted(maps.Keys(inputs)) {
		raw := inputs[name]
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if falsyJSON(raw) {
					b.Fatalf("falsyJSON(%s) = true, want false", raw)
				}
			}
		})
	}
}
