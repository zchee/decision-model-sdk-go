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
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// update regenerates the committed flood fixtures:
//
//	go test ./internal/testsupport -run TestStructuredLegendFloodFixtures -update
var update = flag.Bool("update", false, "rewrite testdata/structured-legend-flood-*.json from StructuredLegendFlood")

// TestStructuredLegendFloodFixtures keeps the committed flood fixtures equal
// to the generator's output, so the files cannot drift from the documented
// shape.
func TestStructuredLegendFloodFixtures(t *testing.T) {
	tests := map[string]struct {
		file   string
		levels int
	}{
		"success: 10^3 structured levels": {file: "structured-legend-flood-1k.json", levels: 1000},
		"success: 10^4 structured levels": {file: "structured-legend-flood-10k.json", levels: 10000},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			want := StructuredLegendFlood(tt.levels)
			if *update {
				path := filepath.Join(FixtureDir(t), tt.file)
				if err := os.WriteFile(path, want, 0o600); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
				t.Logf("wrote %s (%d bytes)", path, len(want))
				return
			}
			got := FixtureString(t, tt.file)
			if got != string(want) {
				t.Fatalf("%s differs from StructuredLegendFlood(%d) (%d vs %d bytes); run with -update after an intended generator change",
					tt.file, tt.levels, len(got), len(want))
			}
		})
	}
}

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
