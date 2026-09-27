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

package testsupport

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// TestStructuredLegendFloodShape decodes the generator's output with the
// standard library and checks the shape its documentation promises.
func TestStructuredLegendFloodShape(t *testing.T) {
	type answer struct {
		Type          string                     `json:"type"`
		Score         float64                    `json:"score"`
		Confidence    float64                    `json:"confidence"`
		Legend        map[string]json.RawMessage `json:"legend"`
		Probabilities map[string]float64         `json:"probabilities"`
	}
	type body struct {
		Model   string            `json:"model"`
		Answers map[string]answer `json:"answers"`
	}
	tests := map[string]struct {
		levels     int
		wantLevels int
	}{
		"success: one level":                 {levels: 1, wantLevels: 1},
		"success: two levels (both shapes)":  {levels: 2, wantLevels: 2},
		"success: 1000 levels":               {levels: 1000, wantLevels: 1000},
		"success: zero is raised to one":     {levels: 0, wantLevels: 1},
		"success: negative is raised to one": {levels: -5, wantLevels: 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			raw := StructuredLegendFlood(tt.levels)
			if bytes.ContainsAny(raw, "\\\n") {
				t.Fatalf("output contains an escape or a newline")
			}
			var b body
			if err := json.Unmarshal(raw, &b); err != nil {
				t.Fatalf("output is not valid JSON: %v", err)
			}
			if diff := gocmp.Diff([]string{"flood", "spam", "tone"}, sortedKeys(b.Answers)); diff != "" {
				t.Errorf("answer names (-want +got):\n%s", diff)
			}
			flood := b.Answers[FloodAnswer]
			if flood.Type != "score" || len(flood.Legend) != tt.wantLevels || len(flood.Probabilities) != tt.wantLevels {
				t.Fatalf("flood answer: type %q, %d legend levels, %d probabilities; want score, %d, %d",
					flood.Type, len(flood.Legend), len(flood.Probabilities), tt.wantLevels, tt.wantLevels)
			}
			if want := float64(tt.wantLevels-1) / 2; flood.Score != want {
				t.Errorf("score = %v, want %v", flood.Score, want)
			}
			for i := range tt.wantLevels {
				level := flood.Legend[strconv.Itoa(i)]
				want := byte('{')
				if i%2 == 1 {
					want = '['
				}
				if len(level) == 0 || level[0] != want {
					t.Fatalf("legend level %d = %s, want a value starting with %q", i, level, want)
				}
			}
		})
	}
}

// TestUnknownAnswerFloodShape decodes the generator's output with the
// standard library and checks the shape its documentation promises.
func TestUnknownAnswerFloodShape(t *testing.T) {
	type body struct {
		Model   string                       `json:"model"`
		Usage   map[string]int               `json:"usage"`
		Answers map[string]map[string]string `json:"answers"`
	}
	tests := map[string]struct {
		size int
	}{
		"success: size 0 still holds one answer": {size: 0},
		"success: 1 KiB":                         {size: 1 << 10},
		"success: 1 MiB":                         {size: 1 << 20},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			raw, answers := UnknownAnswerFlood(tt.size)
			if len(raw) < tt.size {
				t.Errorf("the body is %d bytes, want at least %d", len(raw), tt.size)
			}
			var got body
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("the body is not JSON: %v", err)
			}
			if got.Model != "m" || got.Usage["input_tokens"] != 1 || got.Usage["output_tokens"] != 1 {
				t.Errorf("model %q, usage %v; want \"m\" and 1 and 1 tokens", got.Model, got.Usage)
			}
			if len(got.Answers) != answers || answers == 0 {
				t.Fatalf("the body holds %d distinct answers, the generator says %d (want at least one)", len(got.Answers), answers)
			}
			for i := range answers {
				name := "a" + strconv.Itoa(i)
				if a := got.Answers[name]; len(a) != 1 || a["type"] != "x" {
					t.Fatalf("answer %s = %v, want only type \"x\"", name, a)
				}
			}
			if bytes.ContainsAny(raw, " \n\t\\") {
				t.Error("the body is not compact or holds an escape")
			}
		})
	}
}
