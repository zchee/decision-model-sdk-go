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

//go:build !race

package alloctest

import (
	"runtime"
	"slices"
	"testing"
	"time"

	typesafe "github.com/zchee/typesafe-sdk-go"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// TestLinearityFloodTime checks the time ratio of the structured-legend
// floods (docs/perf/frozen-budgets.md): one 10^4 decode takes at most 15
// times as long as one 10^3 decode, measured by linearityRatio. The bound is
// a statement about the shipped decoder, which reads about 9, so it is
// asserted in this build, without the race detector; ci.yaml's step
// 'go test without -race (allocation budgets)' runs it by name on
// ubuntu-26.04, xcode-27 and windows-2025, and its list guard refuses the
// name's removal, since this file is built only without -race.
func TestLinearityFloodTime(t *testing.T) {
	if ratio := linearityRatio(t); ratio > 15 {
		t.Errorf("10^4 : 10^3 time ratio = %.2f, want at most 15", ratio)
	}
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
// most 15. The allocation clauses of the same budget are
// TestLinearityFlood's. A decode's time is a span's length divided by the
// decodes it holds, the minimum over the flood's spans, which filters
// scheduler noise. The number of decodes is not fixed in advance, since a
// fixed count would have to be sized for the slowest runner; each span runs
// until linearitySpan has elapsed on the host's own clock. The two floods'
// spans alternate, so a change in the host's load reaches both. The
// collector stays off (QuietRuntime) and is run once before each span, to
// free the last span's garbage, which keeps the pooled decoder (sync.Pool
// keeps it through one collection); a warm decode then precedes the span.
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
