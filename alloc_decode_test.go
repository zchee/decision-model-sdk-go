//go:build !race

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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// frozenDecodeAllocs is AC-P2's frozen allocation budget per fixture for the
// decode of a System One body into its answers (docs/perf/frozen-budgets.md,
// S-D1 variant a1; duplicates at 17 by the owner's G2 c). The plain 3-answer
// fixture keeps the plan's ceiling of 8 as well, which 4 meets.
var frozenDecodeAllocs = map[string]uint64{
	"result.json":                      4,
	"type-last.json":                   4,
	"duplicates.json":                  17,
	"result-20.json":                   24,
	"score-flood-mini.json":            21,
	"escaped-names.json":               10,
	"escaped-member-names.json":        30,
	"structured-legend.json":           12,
	"deviation-lone-surrogate.json":    14,
	"unknown-answer-type.json":         1,
	"parity-big-exp-unknown.json":      1,
	"no-answers.json":                  0,
	"structured-legend-flood-1k.json":  91,
	"structured-legend-flood-10k.json": 686,
}

// questionsFor returns the question set a response like res answers, built
// through the public API as a caller builds it: every answer's name and
// kind, a choice's options in the order of its probabilities, and a score's
// levels from its legend, a level the legend leaves out (or an empty legend)
// being the text "-". A response without answers answers one noul question,
// "q", since a set cannot be empty.
func questionsFor(t *testing.T, res *wire.SystemOneResult) *Prepared {
	t.Helper()
	qs := NewQuestions()
	for _, e := range res.Answers.Entries() {
		name := strings.Clone(e.Name)
		switch e.Answer.Kind {
		case wire.KindNoul:
			qs.Noul(name, Noul{})
		case wire.KindChoice:
			var opts Options
			for _, p := range e.Answer.Choice.Probabilities {
				opts = append(opts, Option{Label: strings.Clone(p.Label)})
			}
			qs.Choice(name, Choice{Options: opts})
		case wire.KindScore:
			levels := []Content{Text("-")}
			for _, l := range e.Answer.Score.Legend {
				for len(levels) <= int(l.Level) {
					levels = append(levels, Text("-"))
				}
				if l.Description.JSON != nil {
					levels[l.Level] = JSON(slices.Clone(l.Description.JSON))
				} else {
					levels[l.Level] = Text(strings.Clone(l.Description.Text))
				}
			}
			qs.Score(name, Score{Levels: levels})
		}
	}
	if res.Answers.Len() == 0 {
		qs.Noul("q", Noul{})
	}
	p, err := qs.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestAllocDecodeFixtures checks AC-P2: the allocations of one decode of
// each fixture, as a call makes it (the pooled decoder warm, a fresh result,
// the question set and model the response answers, no logger), are within
// the frozen budget. Counts are runtime.ReadMemStats deltas, the minimum
// that three of five runs share, with the collector off and GOMAXPROCS 1
// (section 6.1.6). Each DECODE line is a ledger row; the misses column is
// the same decode without a question set or model, where every string is a
// copy into one arena (recorded, not budgeted).
func TestAllocDecodeFixtures(t *testing.T) {
	testsupport.QuietRuntime(t)
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range frozenDecodeAllocs {
			if !yield(k) {
				return
			}
		}
	}) {
		budget := frozenDecodeAllocs[name]
		t.Run(name, func(t *testing.T) {
			meta := &wire.ResponseMeta{Status: 200, Body: []byte(testsupport.FixtureString(t, name))}
			var first wire.SystemOneResult
			if err := decodeSystemOne(t.Context(), nil, meta, "", nil, "", &first); err != nil {
				t.Fatal(err)
			}
			qs, model := questionsFor(t, &first), strings.Clone(first.Model)
			measure := func(label string, qs *Prepared, model string) testsupport.Allocs {
				ctx := t.Context()
				var warm wire.SystemOneResult
				if err := decodeSystemOne(ctx, nil, meta, "", qs, model, &warm); err != nil {
					t.Fatal(err)
				}
				return testsupport.MeasureMin(t, name+" "+label, func() *wire.SystemOneResult { return new(wire.SystemOneResult) }, func(res *wire.SystemOneResult) {
					if err := decodeSystemOne(ctx, nil, meta, "", qs, model, res); err != nil {
						t.Fatal(err)
					}
				})
			}
			got := measure("interned", qs, model)
			misses := measure("misses", nil, "")
			t.Logf("DECODE %-34s bytes=%-7d allocs=%-4d allocBytes=%-8d budget=%-4d misses=%s", strings.TrimSuffix(name, ".json"), len(meta.Body), got.Mallocs, got.Bytes, budget, misses)
			if got.Mallocs > budget {
				t.Errorf("decode allocations = %d, want at most the frozen %d", got.Mallocs, budget)
			}
			if name == "result.json" && got.Mallocs > 8 {
				t.Errorf("result.json decode allocations = %d, want at most 8 (NF2)", got.Mallocs)
			}
		})
	}
}

// TestLinearityFlood checks AC-P8's time and allocation ratios on the
// structured-legend floods: the 10^4 decode takes at most 15 times the 10^3
// decode (the minimum of 21 runs each, which filters scheduler noise), and
// allocates at most 12 times as often. The lazy pass's own bound is
// internal/codec's TestLazyPassAllocations.
func TestLinearityFlood(t *testing.T) {
	testsupport.QuietRuntime(t)
	type flood struct {
		meta   *wire.ResponseMeta
		qs     *Prepared
		model  string
		time   time.Duration
		allocs uint64
	}
	floods := map[string]*flood{}
	for _, name := range []string{"structured-legend-flood-1k.json", "structured-legend-flood-10k.json"} {
		f := &flood{meta: &wire.ResponseMeta{Status: 200, Body: []byte(testsupport.FixtureString(t, name))}}
		var first wire.SystemOneResult
		if err := decodeSystemOne(t.Context(), nil, f.meta, "", nil, "", &first); err != nil {
			t.Fatal(err)
		}
		f.qs, f.model = questionsFor(t, &first), first.Model
		runs := make([]time.Duration, 21)
		for i := range runs {
			var res wire.SystemOneResult
			start := time.Now()
			if err := decodeSystemOne(t.Context(), nil, f.meta, "", f.qs, f.model, &res); err != nil {
				t.Fatal(err)
			}
			runs[i] = time.Since(start)
		}
		f.time = slices.Min(runs)
		f.allocs = testsupport.MeasureMin(t, name, func() *wire.SystemOneResult { return new(wire.SystemOneResult) }, func(res *wire.SystemOneResult) {
			if err := decodeSystemOne(t.Context(), nil, f.meta, "", f.qs, f.model, res); err != nil {
				t.Fatal(err)
			}
		}).Mallocs
		floods[name] = f
	}
	small, large := floods["structured-legend-flood-1k.json"], floods["structured-legend-flood-10k.json"]
	timeRatio := float64(large.time) / float64(small.time)
	allocRatio := float64(large.allocs) / float64(small.allocs)
	t.Logf("LINEARITY 1k %v %d allocs, 10k %v %d allocs, time ratio %.2f (bound 15), allocation ratio %.2f (bound 12)", small.time, small.allocs, large.time, large.allocs, timeRatio, allocRatio)
	if timeRatio > 15 {
		t.Errorf("10^4 : 10^3 time ratio = %.2f, want at most 15", timeRatio)
	}
	if allocRatio > 12 {
		t.Errorf("10^4 : 10^3 allocation ratio = %.2f, want at most 12", allocRatio)
	}
}
