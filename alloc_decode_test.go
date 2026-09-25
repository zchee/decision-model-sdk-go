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
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// decodeAllocs pins, per fixture, the allocations of one decode of a System
// One body into its answers (want), measured identically on (M) and (L)
// (ledger rows W2.0-01 and W2.0-04), next to AC-P2's frozen budget (frozen:
// docs/perf/frozen-budgets.md, S-D1 variant a1; duplicates at 17 by the
// owner's G2 c). The counts are pinned exactly, not as ceilings (ruling
// R70), so that a sonic upgrade or a decoder change that moves one fails
// here and is looked at, as the NF1 encode pins do. The six fixtures one
// below their budget (duplicates, escaped-member-names, structured-legend,
// deviation-lone-surrogate and the two floods: each holds a structured
// level) are those whose frozen count included the arena that copied
// structured levels, which interning makes unnecessary. The plain
// 3-answer fixture also keeps the plan's ceiling of 8 (NF2).
var decodeAllocs = map[string]struct{ want, frozen uint64 }{
	"result.json":                      {want: 4, frozen: 4},
	"type-last.json":                   {want: 4, frozen: 4},
	"duplicates.json":                  {want: 16, frozen: 17},
	"result-20.json":                   {want: 24, frozen: 24},
	"score-flood-mini.json":            {want: 21, frozen: 21},
	"escaped-names.json":               {want: 10, frozen: 10},
	"escaped-member-names.json":        {want: 29, frozen: 30},
	"structured-legend.json":           {want: 11, frozen: 12},
	"deviation-lone-surrogate.json":    {want: 13, frozen: 14},
	"unknown-answer-type.json":         {want: 1, frozen: 1},
	"parity-big-exp-unknown.json":      {want: 1, frozen: 1},
	"no-answers.json":                  {want: 0, frozen: 0},
	"structured-legend-flood-1k.json":  {want: 90, frozen: 91},
	"structured-legend-flood-10k.json": {want: 685, frozen: 686},
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
// the question set and model the response answers, no logger), equal the
// pinned count, which is within the frozen budget. Counts are
// runtime.ReadMemStats deltas, the minimum that three of five runs share,
// with the collector off and GOMAXPROCS 1 (section 6.1.6). Each DECODE line
// is a ledger row; the misses column is the same decode without a question
// set or model, where every string is a copy into one arena (recorded, not
// pinned).
func TestAllocDecodeFixtures(t *testing.T) {
	testsupport.QuietRuntime(t)
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range decodeAllocs {
			if !yield(k) {
				return
			}
		}
	}) {
		pin := decodeAllocs[name]
		if pin.want > pin.frozen {
			t.Fatalf("%s: pinned count %d exceeds the frozen budget %d", name, pin.want, pin.frozen)
		}
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
			t.Logf("DECODE %-34s bytes=%-7d allocs=%-4d allocBytes=%-8d budget=%-4d misses=%s", strings.TrimSuffix(name, ".json"), len(meta.Body), got.Mallocs, got.Bytes, pin.frozen, misses)
			if got.Mallocs != pin.want {
				t.Errorf("decode allocations = %d, want exactly %d (frozen budget %d): a change in the decoder or in sonic moved the count", got.Mallocs, pin.want, pin.frozen)
			}
			if name == "result.json" && got.Mallocs > 8 {
				t.Errorf("result.json decode allocations = %d, want at most 8 (NF2)", got.Mallocs)
			}
		})
	}
}

// Timing spans of TestLinearityFlood. A span repeats one flood's decode
// until the clock has advanced by at least linearitySpan, so it holds as
// many decodes as the host needs for its clock to resolve it: Windows
// advances time.Now in ticks (about 15.6 ms by default), under which one
// 10^3 decode, well under a millisecond, can measure 0 s and make the ratio
// +Inf (K30), while a 250 ms span covers at least 16 such ticks, so its
// reading is within about 6% of its length. Each flood gets linearitySpans
// spans. linearityMaxDecodes only ends a span on a clock that never
// advances, which the test then reports: a 10^3 decode would have to take
// under 4 µs to reach it before 250 ms.
const (
	linearitySpan       = 250 * time.Millisecond
	linearitySpans      = 5
	linearityMaxDecodes = 1 << 16
)

// TestLinearityFlood checks AC-P8's time and allocation ratios on the
// structured-legend floods: one 10^4 decode takes at most 15 times as long
// as one 10^3 decode, and allocates at most 12 times as often. A decode's
// time is a span's length divided by the decodes it holds, the minimum over
// the flood's spans, which filters scheduler noise. The number of decodes
// is not fixed in advance, since a fixed count would have to be sized for
// the slowest runner; each span runs until linearitySpan has elapsed on the
// host's own clock. The two floods' spans alternate, so a change in the
// host's load reaches both. The collector stays off (QuietRuntime) and is
// run once before each span, to free the last span's garbage, which keeps
// the pooled decoder (sync.Pool keeps it through one collection); a warm
// decode then precedes the span. The lazy pass's own bound is
// internal/codec's TestLazyPassAllocations.
func TestLinearityFlood(t *testing.T) {
	testsupport.QuietRuntime(t)
	type flood struct {
		name    string
		meta    *wire.ResponseMeta
		qs      *Prepared
		model   string
		decodes []int           // per span
		each    []time.Duration // per span: its length divided by its decodes
		time    time.Duration
		allocs  uint64
	}
	decode := func(f *flood, res *wire.SystemOneResult) {
		if err := decodeSystemOne(t.Context(), nil, f.meta, "", f.qs, f.model, res); err != nil {
			t.Fatal(err)
		}
	}
	var floods []*flood
	for _, name := range []string{"structured-legend-flood-1k.json", "structured-legend-flood-10k.json"} {
		f := &flood{name: name, meta: &wire.ResponseMeta{Status: 200, Body: []byte(testsupport.FixtureString(t, name))}}
		var first wire.SystemOneResult
		if err := decodeSystemOne(t.Context(), nil, f.meta, "", nil, "", &first); err != nil {
			t.Fatal(err)
		}
		f.qs, f.model = questionsFor(t, &first), first.Model
		decode(f, new(wire.SystemOneResult))
		f.allocs = testsupport.MeasureMin(t, name, func() *wire.SystemOneResult { return new(wire.SystemOneResult) }, func(res *wire.SystemOneResult) {
			decode(f, res)
		}).Mallocs
		floods = append(floods, f)
	}
	for span := range linearitySpans {
		for _, f := range floods {
			runtime.GC()
			decode(f, new(wire.SystemOneResult))
			n, start := 0, time.Now()
			var elapsed time.Duration
			for elapsed < linearitySpan && n < linearityMaxDecodes {
				var res wire.SystemOneResult
				decode(f, &res)
				n++
				elapsed = time.Since(start)
			}
			if elapsed <= 0 {
				t.Fatalf("%s span %d: %d decodes measured %v: the clock did not advance, so no time ratio can be formed", f.name, span, n, elapsed)
			}
			each := elapsed / time.Duration(n)
			t.Logf("span %d %-32s %5d decodes in %v, %v each", span, f.name, n, elapsed, each)
			f.decodes = append(f.decodes, n)
			f.each = append(f.each, each)
		}
	}
	for _, f := range floods {
		f.time = slices.Min(f.each)
	}
	small, large := floods[0], floods[1]
	if small.time <= 0 {
		t.Fatalf("1k decode time = %v over spans %v of %v decodes: want > 0", small.time, small.each, small.decodes)
	}
	timeRatio := float64(large.time) / float64(small.time)
	allocRatio := float64(large.allocs) / float64(small.allocs)
	t.Logf("LINEARITY 1k %v %d allocs, 10k %v %d allocs, time ratio %.2f (bound 15), allocation ratio %.2f (bound 12), decodes per span 1k %v 10k %v, %d spans of at least %v", small.time, small.allocs, large.time, large.allocs, timeRatio, allocRatio, small.decodes, large.decodes, linearitySpans, linearitySpan)
	if timeRatio > 15 {
		t.Errorf("10^4 : 10^3 time ratio = %.2f, want at most 15", timeRatio)
	}
	if allocRatio > 12 {
		t.Errorf("10^4 : 10^3 allocation ratio = %.2f, want at most 12", allocRatio)
	}
}
