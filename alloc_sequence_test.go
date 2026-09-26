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
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// TestAllocScratchSequence checks AC-P1's mixed-size sequence (plan section
// 6.1.6, frozen-budgets.md): 32 SystemOne calls on one client, a 1 KiB state
// ×10, a 6 MiB state, 1 KiB ×10, a 9 MiB state and 1 KiB ×10, through the
// Recorder, a synchronous in-memory RoundTripper that reads and closes each
// request body before it returns, so every call's scratch is back in the
// pool, or dropped, when the call returns (the HTTP/2 transport closes
// bodies on a goroutine of its own). For the boxed-string and RawJSON
// states:
//
//   - calls 2-10, 12-21 and 24-32 cost exactly the single-size call, the
//     same call on a warm client measured on its own (the pool hit);
//   - call 11 grows the pooled scratch to the 6 MiB body and call 22 to the
//     9 MiB one, within S-E1's growth budgets g₆ and g₉, and the pool keeps
//     the first (calls 12-21 cost the single-size call) and drops the
//     second, past the 8 MiB ceiling, so that call 23 pays one allocation
//     more, its fresh scratch;
//   - the encode-level counts, the call's count less what the single-size
//     call spends outside its encode, are frozen-budgets.md's row exactly:
//     "1×9 | 2 | 1×10 | 2 | 2 | 1×9" and "0×9 | 1 | 0×10 | 1 | 1 | 0×9";
//   - a probed run, which reads the pool's scratch after every call,
//     finds exactly one drop, after call 22, and no retained scratch over
//     the ceiling.
//
// The *struct and flat-map states are measured and recorded, not asserted
// (G3 (b)): sonic's growslice steps can end past the ceiling at 6 MiB, and
// then call 11 drops its scratch too.
//
// Call 1 is recorded: it follows two collections and pays the body, its
// scratch and the refill of every pool (R22b). Counts are per-call
// runtime.ReadMemStats deltas, not testing.AllocsPerRun, whose warm-up call
// would consume pool state; the collector is off (the 6 MiB and 9 MiB
// bodies would otherwise start collections that empty the pool) and
// GOMAXPROCS is 1 (sync.Pool.Put fills the current P's private slot, which
// another P does not see). The sequence runs five times, each on a fresh
// client after two collections, and each call's count is the minimum that
// three of the five share (testsupport.StableMin); a recorded kind's is the
// minimum and the maximum (testsupport.Spread). The SEQ lines are the
// ledger's rows.
//
// The file is built without -race: under the race detector sync.Pool.Put
// drops one value in four. The functional half is
// TestAllocScratchSequenceFunctional.
func TestAllocScratchSequence(t *testing.T) {
	testsupport.QuietRuntime(t)
	qs := q3Questions(t)
	reply := testsupport.JSON(http.StatusOK, testsupport.Fixture(t, "result.json"))
	newClient := func() *Client {
		return newTestClient(t, &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{reply}})
	}
	for _, sk := range sequenceKinds {
		t.Run(sk.name, func(t *testing.T) {
			ctx := t.Context()
			states, lens := sequenceStates(t, stateKindNamed(t, sk.name), qs)
			var err error
			check := func(label string) {
				t.Helper()
				if err != nil {
					t.Fatalf("%s: %v", label, err)
				}
			}

			// The single-size numbers: the 1 KiB call on a warm client, and
			// its encode alone.
			runtime.GC()
			runtime.GC()
			c := newClient()
			for range 2 {
				_, err = c.SystemOne(ctx, states[0], qs)
				check("warm call")
			}
			single := series(t, sk.name+" single-size call", nil, func() { sinkResponse, err = c.SystemOne(ctx, states[0], qs) })
			check("single-size call")
			encode := series(t, sk.name+" single-size encode", nil, func() {
				var body codec.Body
				if body, err = encodeBody(states[0], c.cfg.model, qs, nil); err == nil {
					body.Release()
				}
			})
			check("single-size encode")
			if single.Mallocs < encode.Mallocs {
				t.Fatalf("the single-size call %s costs less than its encode %s", single, encode)
			}
			rest := single.Mallocs - encode.Mallocs // what a call spends outside its encode

			var runs [sequenceCalls][testsupport.AllocRuns]testsupport.Allocs
			for r := range testsupport.AllocRuns {
				runtime.GC()
				runtime.GC()
				c := newClient()
				for i := range sequenceCalls {
					st := states[sequenceState(i+1)]
					runs[i][r] = testsupport.Measure(func() { sinkResponse, err = c.SystemOne(ctx, st, qs) })
					check("call " + strconv.Itoa(i+1))
				}
			}
			runtime.GC()
			runtime.GC()
			caps := sequenceProbe(t, newClient(), qs, states)
			drops := sequenceDrops(caps, lens)

			least := make([]testsupport.Allocs, sequenceCalls)
			most := make([]testsupport.Allocs, sequenceCalls)
			for i := range sequenceCalls {
				label := sk.name + " call " + strconv.Itoa(i+1)
				if sk.frozen == "" || i == 0 {
					least[i], most[i] = testsupport.Spread(t, label, runs[i][:])
				} else {
					least[i] = testsupport.StableMin(t, label, runs[i][:])
					most[i] = least[i]
				}
			}
			calls := make([]uint64, 0, sequenceCalls)
			encodes := make([]uint64, 0, sequenceCalls-1) // calls 2 to 32
			for i, a := range least {
				calls = append(calls, a.Mallocs)
				if i > 0 {
					encodes = append(encodes, a.Mallocs-min(a.Mallocs, rest))
				}
			}
			verdict := "recorded (G3 (b))"
			if sk.frozen != "" {
				verdict = "asserted"
			}
			t.Logf("SEQ %-17s single-size call=%s encode=%s rest=%d; call 1 min %s max %s; calls 1..32 %s", sk.name, single, encode, rest, least[0], most[0], sequenceNotation(calls))
			t.Logf("SEQ %-17s encode-level calls 2..32: %s; bytes call 11 %d, call 22 %d, call 23 %d; drops after calls %v; scratch after each call %v; %s",
				sk.name, sequenceNotation(encodes), least[sequenceSix-1].Bytes, least[sequenceNine-1].Bytes, least[sequenceNine].Bytes, drops, caps, verdict)
			if sk.frozen == "" {
				mostCalls := make([]uint64, sequenceCalls)
				for i, a := range most {
					mostCalls[i] = a.Mallocs
				}
				t.Logf("SEQ %-17s maximum calls 1..32 %s", sk.name, sequenceNotation(mostCalls))
				return
			}

			if diff := gocmp.Diff(parseSequenceNotation(t, sk.frozen), encodes); diff != "" {
				t.Errorf("encode-level counts of calls 2..32 = %s, want the frozen %s (-want +got, by call from 2):\n%s", sequenceNotation(encodes), sk.frozen, diff)
			}
			for call := 2; call <= sequenceCalls; call++ {
				got := least[call-1]
				switch call {
				case sequenceSix:
					if e := got.Mallocs - rest; e > sk.g6 {
						t.Errorf("call %d (6 MiB) encode = %d allocations, want at most g₆ = %d", call, e, sk.g6)
					}
				case sequenceNine:
					if e := got.Mallocs - rest; e > sk.g9 {
						t.Errorf("call %d (9 MiB) encode = %d allocations, want at most g₉ = %d", call, e, sk.g9)
					}
				case sequenceNine + 1:
					if got.Mallocs != single.Mallocs+1 {
						t.Errorf("call %d = %s, want the single-size call's %d allocations + 1, the fresh scratch after the drop", call, got, single.Mallocs)
					}
				default:
					if got != single {
						t.Errorf("call %d = %s, want the single-size call's %s (a pool hit)", call, got, single)
					}
				}
			}
			if diff := gocmp.Diff([]int{sequenceNine}, drops); diff != "" {
				t.Errorf("the calls after which the pool dropped the scratch (-want +got):\n%s", diff)
			}
			if i := slices.IndexFunc(caps[:], func(c int) bool { return c > codec.ScratchCeiling }); i >= 0 {
				t.Errorf("after call %d the pool held a scratch of %d B, over the ceiling %d B", i+1, caps[i], codec.ScratchCeiling)
			}
		})
	}
}
