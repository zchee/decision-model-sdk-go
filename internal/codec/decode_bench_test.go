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
// limitations under the License.

package codec

import (
	"runtime"
	"strings"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport/naive"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// decodeBenchFixtures are the valid bodies every decode benchmark runs: the
// AC-P2 fixtures, in the order of the frozen table.
var decodeBenchFixtures = []string{
	"result.json",
	"type-last.json",
	"duplicates.json",
	"result-20.json",
	"score-flood-mini.json",
	"escaped-names.json",
	"escaped-member-names.json",
	"structured-legend.json",
	"deviation-lone-surrogate.json",
	"unknown-answer-type.json",
	"parity-big-exp-unknown.json",
	"no-answers.json",
	"structured-legend-flood-1k.json",
	"structured-legend-flood-10k.json",
}

// BenchmarkDecode measures the production decode of each fixture as a call
// makes it: the pooled decoder is warm, the result is fresh each iteration,
// and the question set and model are the ones the response answers, so every
// string is interned. Its sub-benchmarks pair with BenchmarkDecodeNaiveSonic's
// by name (G3: W5.1 reports the ratio).
func BenchmarkDecode(b *testing.B) {
	for _, name := range decodeBenchFixtures {
		body := testsupport.Fixture(b, name)
		first, _, _, err := decodeBody(b, body, nil, "")
		if err != nil {
			b.Fatalf("%s: %v", name, err)
		}
		q, model := questionsFor(b, first), first.Model
		b.Run(strings.TrimSuffix(name, ".json"), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				var res wire.SystemOneResult
				if _, err := DecodeSystemOne(body, q, model, &res); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDecodeNaiveSonic is B2's naive comparator of owner decision G3
// (a): internal/testsupport/naive's decode with sonic, sonic.Unmarshal of
// the same bodies into a map[string]any, which validates the JSON (less
// strictly: it takes raw control characters and invalid UTF-8) and builds a
// generic tree without the SDK's checks or types; call/naive decodes the
// same way. A body the codec refuses has no row: decoding into a
// map[string]any, sonic refuses 1e400 anywhere on both architectures
// ("float infinity" on arm64, "float number is infinity" on amd64), so
// parity-big-exp-unknown, which the SDK and the Python SDK accept, has no
// naive row (ledger W2.0). AC-P2's "≤ 0.5 × naive" compares result's
// allocations with BenchmarkDecode's (W5.1 reports it, W5.2 asserts it).
func BenchmarkDecodeNaiveSonic(b *testing.B) {
	benchmarkDecodeNaive(b, naive.Sonic)
}

// BenchmarkDecodeNaiveJSON is BenchmarkDecodeNaiveSonic with encoding/json,
// the second comparator of G3 (a), reported only. encoding/json also
// refuses 1e400 into a float64, so parity-big-exp-unknown has no row here
// either.
func BenchmarkDecodeNaiveJSON(b *testing.B) {
	benchmarkDecodeNaive(b, naive.StdJSON)
}

// benchmarkDecodeNaive runs cd's decode into a map[string]any over each
// fixture that cd accepts, one sub-benchmark per fixture named as
// BenchmarkDecode's.
func benchmarkDecodeNaive(b *testing.B, cd naive.Codec) {
	for _, name := range decodeBenchFixtures {
		body := testsupport.Fixture(b, name)
		if _, err := cd.Decode(body); err != nil {
			b.Logf("%s: %s refuses it on %s, no row: %v", name, cd.Name, runtime.GOARCH, err)
			continue
		}
		b.Run(strings.TrimSuffix(name, ".json"), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				if _, err := cd.Decode(body); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestLazyPassAllocations checks AC-P8's allocation clauses on the lazy pass
// alone, as frozen at W0.6: at most 20 + ceil(members/15) + escaped keys + 1
// allocations, where members is what the pass iterates (root members,
// answers members, the marked answers' members and their legend levels) and
// the one is the arena, which the lazy pass no longer allocates (the copies
// moved to intern), and a 10^4 : 10^3 ratio of at most 12. Counts are
// runtime.ReadMemStats deltas, the minimum that three of five runs share,
// with the collector off; under -race the test is skipped, since a pooled
// scratch is dropped one time in four. The members each fixture's pass visits
// are pinned too (the floods' 1 011 and 10 011 are frozen-budgets.md's inputs
// of the bound, which root's TestLinearityFlood counts from the fixture with
// encoding/json), so a pass that visits more than the flagged answers fails
// here, not only through the bound it computes from its own count.
func TestLazyPassAllocations(t *testing.T) {
	if raceEnabled() {
		t.Skip("allocation counts need a build without -race")
	}
	testsupport.QuietRuntime(t)
	tests := map[string]struct {
		fixture string
		escaped uint64 // keys with an escape that the lazy pass iterates, counted in the fixture
		members uint64 // the members the lazy pass visits, pinned
	}{
		"success: structured-legend":        {fixture: "structured-legend.json", members: 10},
		"success: escaped-member-names":     {fixture: "escaped-member-names.json", escaped: 5, members: 12}, // "model", "usage", "answers", "legend" and the level key "0"
		"success: duplicates":               {fixture: "duplicates.json", escaped: 1, members: 18},           // the second "answers"
		"success: deviation-lone-surrogate": {fixture: "deviation-lone-surrogate.json", members: 11},
		"success: flood-1k":                 {fixture: "structured-legend-flood-1k.json", members: 1011},
		"success: flood-10k":                {fixture: "structured-legend-flood-10k.json", members: 10011},
	}
	lazyAllocs := map[string]uint64{}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := testsupport.Fixture(t, tt.fixture)
			d := newDecoder()
			var res wire.SystemOneResult
			if _, err := d.systemOne(body, nil, "", &res); err != nil {
				t.Fatal(err)
			}
			src := NoCopyString(body)
			d.stats = stats{}
			if err := d.lazy(src); err != nil {
				t.Fatal(err)
			}
			members := d.stats.members
			if members != tt.members {
				t.Errorf("the lazy pass visited %d members, want %d: it reads members it should not, or skips some, and the bound below moves with it", members, tt.members)
			}
			got := testsupport.MeasureMin(t, tt.fixture+" lazy", func() struct{} { return struct{}{} }, func(struct{}) {
				if err := d.lazy(src); err != nil {
					t.Fatal(err)
				}
			})
			bound := 20 + (members+14)/15 + tt.escaped + 1
			t.Logf("LAZY %-34s members=%-6d allocs=%-4d bytes=%-8d bound=%d", tt.fixture, members, got.Mallocs, got.Bytes, bound)
			if got.Mallocs > bound {
				t.Errorf("lazy pass allocations = %d, want at most %d (members %d, escaped keys %d)", got.Mallocs, bound, members, tt.escaped)
			}
			lazyAllocs[tt.fixture] = got.Mallocs
			d.release()
		})
	}
	small, large := lazyAllocs["structured-legend-flood-1k.json"], lazyAllocs["structured-legend-flood-10k.json"]
	if small == 0 {
		t.Fatal("no lazy-pass count for the 1k flood")
	}
	ratio := float64(large) / float64(small)
	t.Logf("LAZY ratio 10k/1k = %d/%d = %.2f (bound 12)", large, small, ratio)
	if ratio > 12 {
		t.Errorf("10^4 : 10^3 lazy-pass allocation ratio = %.2f, want at most 12", ratio)
	}
}
