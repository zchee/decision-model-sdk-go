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

package typesafe

import (
	"maps"
	"slices"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// TestAllocPrepare counts the heap allocations of Questions.Prepare on every
// set of prepareCases, the set built outside the measured section, and pins
// the count of the first three cases of the performance ledger's W1.3
// section (c1-sketch 9 → 6 with W5.3's P1, which sorts map keys on the
// Builder's key stack) and of c5-raw-100x3, P1's case (1 710 → 14; 1 700 of
// the 1 710 sorted the keys). The other sets are measured and logged, with
// the bytes of every run and the prepared length, for the ledger.
func TestAllocPrepare(t *testing.T) {
	testsupport.QuietRuntime(t)

	tests := map[string]struct {
		// mallocs is the pinned allocation count, or -1 when the ledger
		// records the count without this test pinning it.
		mallocs int
	}{
		"c1-sketch":           {mallocs: 6},
		"c2-noul-short":       {mallocs: 3},
		"c3-choice-20x10":     {mallocs: 27},
		"c4a-score-20x8-text": {mallocs: -1},
		"c4b-score-20x8-json": {mallocs: -1},
		"c5-raw-100x3":        {mallocs: 14},
		"c6-escapes":          {mallocs: -1},
		"n8a-array-score":     {mallocs: -1},
		"n8a-array-control":   {mallocs: -1},
		"n8b-map-score":       {mallocs: -1},
		"n8b-map-control":     {mallocs: -1},
	}
	if diff := gocmp.Diff(slices.Sorted(maps.Keys(prepareCases)), slices.Sorted(maps.Keys(tests))); diff != "" {
		t.Fatalf("TestAllocPrepare cases differ from prepareCases (-prepareCases +tests):\n%s", diff)
	}
	// Sorted, so that the log reads in the ledger's case order.
	for _, name := range slices.Sorted(maps.Keys(tests)) {
		tt := tests[name]
		t.Run(name, func(t *testing.T) {
			got := testsupport.MeasureMin(t, name, prepareCases[name], func(qs *Questions) {
				p, err := qs.Prepare()
				if err != nil {
					t.Fatal(err)
				}
				prepareSink = p
			})
			t.Logf("%s: %d mallocs, %d bytes; prepared %d bytes", name, got.Mallocs, got.Bytes, len(prepareSink.w.Questions))
			if tt.mallocs < 0 {
				return
			}
			if diff := gocmp.Diff(uint64(tt.mallocs), got.Mallocs); diff != "" {
				t.Errorf("Prepare mallocs (-want +got):\n%s", diff)
			}
		})
	}
}
