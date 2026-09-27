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

package alloctest

import (
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	typesafe "github.com/zchee/typesafe-sdk-go"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// The decode cases of the per-fixture decode budget and of the floods'
// linearity, built with and without -race: the budgets are
// TestAllocDecodeFixtures and TestLinearityFlood (//go:build !race);
// TestLinearityFloodTime, the floods' time ratio, measures with
// linearityRatio and asserts the bound in the build without -race
// (alloc_linearity_test.go; alloc_linearity_race_test.go logs it under
// -race), and TestAllocDecodeFixturesFunctional is the decode budget's
// functional half.

// decodeAllocs pins, per fixture, the allocations of one decode of a System
// One body into its answers (want), measured identically on darwin/arm64 and
// linux/amd64, next to the frozen budget (docs/perf/frozen-budgets.md). The
// counts are pinned exactly, not as ceilings, so that a sonic upgrade or a
// decoder change that moves one fails here and is looked at, as the encode
// pins do. The six fixtures one below their budget (duplicates,
// escaped-member-names, structured-legend, deviation-lone-surrogate and the
// two floods: each holds a structured level) are those whose frozen count
// included the arena that copied structured levels, which interning makes
// unnecessary. The plain 3-answer fixture also keeps its ceiling of 8.
//
// The keys are exactly the fixtures of testdata that decode as a System One
// body: TestAllocDecodeFixtures decodes every testdata/*.json and fails on
// one that decodes without a pin, or is pinned and does not decode.
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

// decodeFixture decodes testdata/name as a call's response is decoded, with
// no question set or model, and returns the result.
func decodeFixture(t *testing.T, name string) (*wire.ResponseMeta, wire.SystemOneResult, error) {
	t.Helper()
	meta := &wire.ResponseMeta{Status: 200, Body: []byte(testsupport.FixtureString(t, name))}
	var res wire.SystemOneResult
	err := decodeSystemOne(t.Context(), meta, nil, "", &res)
	return meta, res, err
}

// questionsFor returns the question set a response like res answers, built
// through the public API as a caller builds it: every answer's name and
// kind, a choice's options in the order of its probabilities, and a score's
// levels from its legend, a level the legend leaves out (or an empty legend)
// being the text "-". A response without answers answers one noul question,
// "q", since a set cannot be empty.
func questionsFor(t *testing.T, res *wire.SystemOneResult) *typesafe.Prepared {
	t.Helper()
	qs := typesafe.NewQuestions()
	for _, e := range res.Answers.Entries() {
		name := strings.Clone(e.Name)
		switch e.Answer.Kind {
		case wire.KindNoul:
			qs.Noul(name, typesafe.Noul{})
		case wire.KindChoice:
			var opts typesafe.Options
			for _, p := range e.Answer.Choice.Probabilities {
				opts = append(opts, typesafe.Option{Label: strings.Clone(p.Label)})
			}
			qs.Choice(name, typesafe.Choice{Options: opts})
		case wire.KindScore:
			levels := []typesafe.Content{typesafe.Text("-")}
			for _, l := range e.Answer.Score.Legend {
				for len(levels) <= int(l.Level) {
					levels = append(levels, typesafe.Text("-"))
				}
				if l.Description.JSON != nil {
					levels[l.Level] = typesafe.JSON(slices.Clone(l.Description.JSON))
				} else {
					levels[l.Level] = typesafe.Text(strings.Clone(l.Description.Text))
				}
			}
			qs.Score(name, typesafe.Score{Levels: levels})
		}
	}
	if res.Answers.Len() == 0 {
		qs.Noul("q", typesafe.Noul{})
	}
	p, err := qs.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The linearity floods: one score answer whose legend has 10^3 and 10^4
// levels, half of them structured, among a noul and a choice answer.
var linearityFloods = [2]string{"structured-legend-flood-1k.json", "structured-legend-flood-10k.json"}

// membersVisited counts, in the JSON object body, the members the decoder's
// lazy pass iterates: the root's members, the members of "answers", and for
// each answer whose legend holds a structured level (an array or an object,
// not a string) the answer's own members and its legend levels. It reads
// body with encoding/json, independently of the SDK's decoder, and counts a
// name once, so it holds for a body without duplicate member names, as the
// floods are.
func membersVisited(t *testing.T, body []byte) uint64 {
	t.Helper()
	var root map[string]any
	if err := testsupport.StdlibUnmarshal(body, &root); err != nil {
		t.Fatalf("not a JSON object: %v", err)
	}
	answers, _ := root["answers"].(map[string]any)
	n := uint64(len(root) + len(answers))
	for _, a := range answers {
		answer, _ := a.(map[string]any)
		legend, _ := answer["legend"].(map[string]any)
		structured := false
		for _, level := range legend {
			if _, text := level.(string); !text {
				structured = true
			}
		}
		if structured {
			n += uint64(len(answer) + len(legend))
		}
	}
	return n
}

// Timing spans of linearityRatio. A span repeats one flood's decode until
// the clock has advanced by at least linearitySpan, so it holds as many
// decodes as the host needs for its clock to resolve it: Windows advances
// time.Now in ticks (about 15.6 ms by default), under which one 10^3 decode,
// well under a millisecond, can measure 0 s and make the ratio +Inf, while a
// 250 ms span covers at least 16 such ticks, so its reading is within about
// 6% of its length. Each flood gets linearitySpans spans.
// linearityMaxDecodes only ends a span on a clock that never advances, which
// the test then reports: a 10^3 decode would have to take under 4 µs to
// reach it before 250 ms.
const (
	linearitySpan       = 250 * time.Millisecond
	linearitySpans      = 5
	linearityMaxDecodes = 1 << 16
)

// linearityRatio measures the time ratio on the structured-legend floods,
// the time of one 10^4 decode over the time of one 10^3 decode, logs it in
// the LINEARITY line and returns it; TestLinearityFloodTime asserts it at
// most 15 in the build without -race (alloc_linearity_test.go). The
// allocation clauses of the same budget are TestLinearityFlood's. A decode's
// time is a span's length divided by the decodes it holds, the minimum over
// the flood's spans, which filters scheduler noise. The number of decodes is
// not fixed in advance, since a fixed count would have to be sized for the
// slowest runner; each span runs until linearitySpan has elapsed on the
// host's own clock. The two floods' spans alternate, so a change in the
// host's load reaches both. The collector stays off (QuietRuntime) and is
// run once before each span, to free the last span's garbage, which keeps
// the pooled decoder (sync.Pool keeps it through one collection); a warm
// decode then precedes the span.
func linearityRatio(t *testing.T) float64 {
	testsupport.QuietRuntime(t)
	type flood struct {
		name    string
		meta    *wire.ResponseMeta
		qs      *typesafe.Prepared
		model   string
		decodes []int           // per span
		each    []time.Duration // per span: its length divided by its decodes
		time    time.Duration
	}
	decode := func(f *flood, res *wire.SystemOneResult) {
		if err := decodeSystemOne(t.Context(), f.meta, f.qs, f.model, res); err != nil {
			t.Fatal(err)
		}
	}
	var floods []*flood
	for _, name := range linearityFloods {
		meta, first, err := decodeFixture(t, name)
		if err != nil {
			t.Fatal(err)
		}
		floods = append(floods, &flood{name: name, meta: meta, qs: questionsFor(t, &first), model: first.Model})
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
	ratio := float64(large.time) / float64(small.time)
	t.Logf("LINEARITY time 1k %v, 10k %v, ratio %.2f (bound 15), decodes per span 1k %v 10k %v, %d spans of at least %v", small.time, large.time, ratio, small.decodes, large.decodes, linearitySpans, linearitySpan)
	return ratio
}
