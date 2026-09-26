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
	"strconv"
	"strings"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport/naive"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// sinkNaive keeps the naive decodes' maps reachable, as a caller's would be.
var sinkNaive map[string]any

// TestAllocDecodeFixtures checks AC-P2 over every fixture of testdata that
// decodes as a System One body: the allocations of one decode, as a call
// makes it (the pooled decoder warm, a fresh result, the question set and
// model the response answers, no logger), equal the pinned count of
// decodeAllocs, which is within the frozen budget; a fixture that decodes
// without a pin, or is pinned and does not decode, fails. The plain
// 3-answer fixture, result.json, also allocates at most 8 times (the plan's
// ceiling; target 4) and at most half as often as the naive comparator's
// decode of the same body measured in the same run (G3 (a):
// internal/testsupport/naive's sonic codec, sonic.Unmarshal into a
// map[string]any); every other fixture's ratio, the 20-answer and escaped
// ones included, is reported.
//
// Counts are runtime.ReadMemStats deltas, the minimum that three of five
// runs share, with the collector off and GOMAXPROCS 1 (section 6.1.6); the
// naive decode's is the minimum of five runs, the comparator's cost at its
// best. Each DECODE line is a ledger row; its misses column is the same
// decode without a question set or model, where every string is a copy
// into one arena (recorded, not pinned). A fixture that does not decode is
// logged with its error, and so is one the naive decode refuses. The
// functional half is TestAllocDecodeFixturesFunctional.
func TestAllocDecodeFixtures(t *testing.T) {
	testsupport.QuietRuntime(t)
	decoded := 0
	for _, name := range testsupport.FixtureNames(t, "*.json") {
		t.Run(name, func(t *testing.T) {
			pin, pinned := decodeAllocs[name]
			meta, first, err := decodeFixture(t, name)
			switch {
			case err != nil && pinned:
				t.Fatalf("pinned at %d allocations but does not decode: %v", pin.want, err)
			case err != nil:
				t.Logf("DECODE %-34s does not decode, no budget: %v", strings.TrimSuffix(name, ".json"), err)
				return
			case !pinned:
				t.Fatalf("decodes as a System One body but decodeAllocs has no pin for it: AC-P2 covers every fixture that decodes")
			case pin.want > pin.frozen:
				t.Fatalf("pinned count %d exceeds the frozen budget %d", pin.want, pin.frozen)
			}
			decoded++
			qs, model := questionsFor(t, &first), strings.Clone(first.Model)
			measure := func(label string, qs *Prepared, model string) testsupport.Allocs {
				ctx := t.Context()
				var warm wire.SystemOneResult
				if err := decodeSystemOne(ctx, nil, meta, "", headerRedactor{}, qs, model, &warm); err != nil {
					t.Fatal(err)
				}
				return testsupport.MeasureMin(t, name+" "+label, func() *wire.SystemOneResult { return new(wire.SystemOneResult) }, func(res *wire.SystemOneResult) {
					if err := decodeSystemOne(ctx, nil, meta, "", headerRedactor{}, qs, model, res); err != nil {
						t.Fatal(err)
					}
				})
			}
			got := measure("interned", qs, model)
			misses := measure("misses", nil, "")

			naiveCount, ratio := "refused", ""
			var naiveErr error
			if sinkNaive, naiveErr = naive.Sonic.Decode(meta.Body); naiveErr == nil { // warms sonic's decoder for map[string]any
				least, _ := testsupport.Spread(t, name+" naive", measureRuns(nil, func() { sinkNaive, naiveErr = naive.Sonic.Decode(meta.Body) }))
				if naiveErr != nil {
					t.Fatalf("naive decode: %v", naiveErr)
				}
				naiveCount, ratio = least.String(), strconvRatio(got.Mallocs, least.Mallocs)
				if name == "result.json" && 2*got.Mallocs > least.Mallocs {
					t.Errorf("result.json decode allocations = %d, want at most half the naive decode's %d (AC-P2)", got.Mallocs, least.Mallocs)
				}
			} else {
				t.Logf("the naive decode refuses %s on this host: %v", name, naiveErr)
			}
			t.Logf("DECODE %-34s bytes=%-7d allocs=%-4d allocBytes=%-8d budget=%-4d misses=%-12s naive=%-14s ratio=%s", strings.TrimSuffix(name, ".json"), len(meta.Body), got.Mallocs, got.Bytes, pin.frozen, misses, naiveCount, ratio)
			if got.Mallocs != pin.want {
				t.Errorf("decode allocations = %d, want exactly %d (frozen budget %d): a change in the decoder or in sonic moved the count", got.Mallocs, pin.want, pin.frozen)
			}
			if name == "result.json" {
				if got.Mallocs > 8 {
					t.Errorf("result.json decode allocations = %d, want at most 8 (NF2)", got.Mallocs)
				}
				if naiveErr != nil {
					t.Errorf("the naive decode refuses result.json, so AC-P2's ratio cannot be formed: %v", naiveErr)
				}
			}
		})
	}
	if decoded != len(decodeAllocs) {
		t.Errorf("%d fixtures decoded, want the %d of decodeAllocs", decoded, len(decodeAllocs))
	}
}

// strconvRatio renders a / b with three decimals.
func strconvRatio(a, b uint64) string {
	if b == 0 {
		return "-"
	}
	return strconv.FormatFloat(float64(a)/float64(b), 'f', 3, 64)
}

// TestLinearityFlood checks AC-P8's allocation clauses on the
// structured-legend floods: one decode of the 10^4 flood allocates at most
// 12 times as often as one of the 10^3 flood (the frozen ratio; a1 measured
// 7.54 at W0.3), where each count is the flood's pinned AC-P2 count
// (decodeAllocs: 90 and 685). The members the lazy pass visits (plan
// section 8) are counted from the fixture by membersVisited, independently
// of the decoder, and must be frozen-budgets.md's 1 011 and 10 011, the
// inputs of the frozen c₀ + c₁ × members bound (c₀ = 20, c₁ = 1/15, plus one
// allocation per escaped key and the arena). That bound is on the lazy pass
// alone, which the root package cannot run apart from the decode: it is
// asserted by internal/codec's TestLazyPassAllocations, and here the whole
// decode's growth per member visited is recorded against c₁.
//
// Counts are runtime.ReadMemStats deltas, the minimum that three of five
// runs share, with the collector off and GOMAXPROCS 1. The time ratio is
// TestLinearityFloodTime, which runs in every build.
func TestLinearityFlood(t *testing.T) {
	testsupport.QuietRuntime(t)
	wantMembers := [2]uint64{1011, 10011}
	var allocs, members [2]uint64
	for i, name := range linearityFloods {
		meta, first, err := decodeFixture(t, name)
		if err != nil {
			t.Fatal(err)
		}
		qs := questionsFor(t, &first)
		decode := func(res *wire.SystemOneResult) {
			if err := decodeSystemOne(t.Context(), nil, meta, "", headerRedactor{}, qs, first.Model, res); err != nil {
				t.Fatal(err)
			}
		}
		decode(new(wire.SystemOneResult))
		allocs[i] = testsupport.MeasureMin(t, name, func() *wire.SystemOneResult { return new(wire.SystemOneResult) }, decode).Mallocs
		members[i] = membersVisited(t, meta.Body)
		if members[i] != wantMembers[i] {
			t.Errorf("%s: %d members visited, want frozen-budgets.md's %d", name, members[i], wantMembers[i])
		}
		if want := decodeAllocs[name].want; allocs[i] != want {
			t.Errorf("%s: decode allocations = %d, want exactly the AC-P2 pin %d", name, allocs[i], want)
		}
	}
	ratio := float64(allocs[1]) / float64(allocs[0])
	slope := float64(allocs[1]-allocs[0]) / float64(members[1]-members[0])
	t.Logf("LINEARITY allocations 1k %d (members %d), 10k %d (members %d), ratio %.2f (bound 12), growth %.4f per member visited (the lazy pass's c₁ = 1/15 = %.4f, recorded)", allocs[0], members[0], allocs[1], members[1], ratio, slope, 1.0/15)
	if ratio > 12 {
		t.Errorf("10^4 : 10^3 allocation ratio = %.2f, want at most 12", ratio)
	}
}
