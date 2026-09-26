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
	"runtime"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// encodeExceptions are the single-size cases of AC-P1 outside its
// steady-state budget, by GOARCH (ruling R62: a sonic count that differs by
// architecture is keyed by it). The budget holds "while the scratch stays
// within the 8 MiB ceiling" (frozen-budgets.md AC-P1): sonic grows the
// scratch of a *struct or map state in runtime.growslice steps, and on arm64
// the steps of the 6 MiB *struct state end at 8.86 MiB, past the ceiling,
// so the pool drops the scratch after every call and no call is warm (R22,
// G2 (b); K24 is W5.3's). Where the steps end depends on the exact length
// of the body at each step: the flat map that S-E1 encoded alone ended at
// 8.76 MiB (finding 5), and the same map inside a request body, after
// {"state":, ends at 7.51 MiB and is warm. Such a case is recorded, and its
// scratch must still outgrow the ceiling, so that a change that ends the
// overshoot fails here and the exception is removed; any other case whose
// scratch outgrows the ceiling fails AC-P1.
var encodeExceptions = map[string]map[string]bool{
	"arm64": {"pointer-to-struct/6MiB": true},
	"amd64": {},
}

// encodeWarmLimit bounds the calls that bring a case to its steady state.
// A nested map is encoded in the map's random order, and sonic's reserve
// ahead of each member depends on it, so the scratch can grow over a few
// calls (three on (M) at 6 MiB) before it holds every order.
const encodeWarmLimit = 32

// encodeWarmBytes is the frozen ceiling on what a warm-pool body encode
// allocates above sonic's own encode of the state (frozen-budgets.md AC-P1,
// "encode bytes": 112 B, tighter than the plan's 1.05 × body + 4 KiB).
const encodeWarmBytes = 112

// noInput is the input of a measured section that takes none.
func noInput() struct{} { return struct{}{} }

// TestAllocEncode checks AC-P1's single-size clauses (NF1) for every state
// kind at 1 KiB, 64 KiB, 1 MiB and 6 MiB: one request body encoded around a
// prepared question set and released on a warm pool allocates at most
// E_sonic + B times, where E_sonic is the state's own encode (the SDK's
// appendState into a buffer that needs no growth, measured in the same run)
// and B = 1 when the caller's argument is boxed at the call; E_sonic is
// pinned to the frozen E(kind) (1, 0 for RawJSON, 1 + m for a state with m
// maps) and the body to exactly E(kind) + B, so a sonic upgrade or an
// encoder change that moves either fails here. Its bytes above E_sonic's
// are exactly the boxing's, 16 B for a bare string, 24 B for a bare RawJSON
// and 0 otherwise: no case varies from run to run, on any host or CI image,
// so the pin is exact, inside the frozen 112 B (also checked, as is the
// plan's 1.05 × body + 4 KiB),
// and the scratch the call leaves is within the 8 MiB ceiling, so the pool
// keeps it. Opening readers is the transport's cost and is not part of it
// (AC-P6 counts it).
//
// Each case starts from empty pools (two collections), as a process that
// sends only states of that size does, since a scratch another case grew
// would hide sonic's overshoot; unmeasured calls then bring it to its steady
// state, where the scratch a call leaves is the one the call before left.
// Counts are runtime.ReadMemStats deltas, the minimum that three of five
// runs share, with the collector off and GOMAXPROCS 1 (section 6.1.6). The
// ENCODE lines are the ledger's rows. The 1 KiB rows are the pins of the
// former TestAllocBodyKinds (W1.2): a bare string 2, a boxed string 1, a
// bare RawJSON 1, a boxed RawJSON 0, a *struct 1 and a flat map 2. The
// cases of encodeExceptions are recorded, the minimum and the maximum of
// five runs.
//
// The file is built without -race: under the race detector sync.Pool.Put
// drops one value in four, so a warm pool is not warm. The functional half,
// the bytes of each body, is TestAllocEncodeFunctional.
func TestAllocEncode(t *testing.T) {
	exceptions, ok := encodeExceptions[runtime.GOARCH]
	if !ok {
		t.Skipf("no AC-P1 table for GOARCH %s: sonic's growth differs by architecture (K27)", runtime.GOARCH)
	}
	testsupport.QuietRuntime(t)
	qs := encodeQuestions(t)
	for _, k := range stateKinds {
		for _, size := range allocSizes {
			name := k.name + "/" + sizeName(size)
			t.Run(name, func(t *testing.T) {
				sc := stateFor(t, k, size)
				var n, capAfter int
				encode := func(struct{}) {
					body, err := sc.pass(qs)
					if err != nil {
						t.Fatalf("encodeBody: %v", err)
					}
					n, capAfter = body.Len(), cap(*body.Buffer())
					body.Release()
				}
				overshoot := exceptions[name]
				runtime.GC()
				runtime.GC()
				prev, calls := -1, 0
				for calls = 1; ; calls++ {
					encode(struct{}{})
					dropped := capAfter > codec.ScratchCeiling
					if capAfter == prev && (!dropped || overshoot) || calls == encodeWarmLimit {
						break
					}
					if dropped {
						runtime.GC() // the pool dropped the scratch: free it, with the collector off
					}
					prev = capAfter
				}
				warmCap := capAfter

				var err error
				buf := make([]byte, 0, 2*len(sc.json)+64<<10)
				esonic := testsupport.MeasureMin(t, name+" E_sonic", noInput, func(struct{}) {
					buf = buf[:0]
					err = appendState(&buf, sc.boxed)
				})
				if err != nil {
					t.Fatalf("appendState: %v", err)
				}
				wantE, b := k.wantE(sc.maps), k.b()

				if overshoot {
					least, most := testsupport.Spread(t, name, measureRuns(nil, func() { encode(struct{}{}) }))
					t.Logf("ENCODE %-24s body=%-8d E_sonic=%-12s B=%d sdk=%-16s max=%-16s scratch=%-9d warm=%-2d recorded (outside the steady state on %s: R22, G2 (b))",
						name, n, esonic, b, least, most, warmCap, calls, runtime.GOARCH)
					if warmCap <= codec.ScratchCeiling {
						t.Errorf("the scratch after the call is %d B, within the ceiling %d B: sonic no longer overshoots it on %s, so the case is warm and encodeExceptions must drop it", warmCap, codec.ScratchCeiling, runtime.GOARCH)
					}
					return
				}

				sdk := testsupport.MeasureMin(t, name, noInput, encode)
				above := sdk.Bytes - min(sdk.Bytes, esonic.Bytes)
				t.Logf("ENCODE %-24s body=%-8d E_sonic=%-12s B=%d sdk=%-16s above=%-4d scratch=%-9d warm=%-2d asserted", name, n, esonic, b, sdk, above, capAfter, calls)
				if esonic.Mallocs != wantE {
					t.Errorf("E_sonic = %d allocations, want the frozen E(%s) = %d (maps %d): sonic's cost for the kind moved", esonic.Mallocs, k.name, wantE, sc.maps)
				}
				if sdk.Mallocs > esonic.Mallocs+b {
					t.Errorf("body encode = %d allocations, want at most E_sonic + B = %d + %d (AC-P1)", sdk.Mallocs, esonic.Mallocs, b)
				}
				if sdk.Mallocs != wantE+b {
					t.Errorf("body encode = %d allocations, want exactly E(kind) + B = %d + %d (NF1, frozen)", sdk.Mallocs, wantE, b)
				}
				if above != k.boxBytes {
					t.Errorf("body encode bytes = %d above E_sonic's %d, want exactly %d, what boxing the argument allocates (B)", above, esonic.Bytes, k.boxBytes)
				}
				if above > encodeWarmBytes {
					t.Errorf("body encode bytes = %d above E_sonic's %d, want at most %d (AC-P1 warm-pool bytes, frozen)", above, esonic.Bytes, encodeWarmBytes)
				}
				if limit := uint64(n)*105/100 + 4<<10; above > limit { //nolint:gosec // G115: n is a body's length, never negative.
					t.Errorf("body encode bytes = %d above E_sonic's, want at most 1.05 × %d + 4 KiB = %d (AC-P1)", above, n, limit)
				}
				if capAfter > codec.ScratchCeiling {
					t.Errorf("the scratch after the call is %d B (%d B after %d warm calls), over the ceiling %d B: the pool drops it, so no call is warm (AC-P1: scratch ≤ 8 MiB)", capAfter, warmCap, calls, codec.ScratchCeiling)
				}
			})
		}
	}
}
