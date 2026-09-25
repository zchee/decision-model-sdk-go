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

package h2gate

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// recordEnv enables the recording tests: measurements for the ledger that
// assert nothing and take seconds, so they stay out of the default run.
const recordEnv = "H2GATE_RECORD"

// TestRecordFanOut records, for docs/perf/ledger.md (section W2.2), what
// AC-P4 leaves unasserted: K22, the latency cost of FirstHold on a cold
// burst at 1 s of service time, against the plain token (option (iv-a)) as
// the comparison; and the negative control of the ordering clause, the
// plain token at the frozen 20 ms leader delay, which must fail it.
// Set H2GATE_RECORD=1 to run it.
func TestRecordFanOut(t *testing.T) {
	if os.Getenv(recordEnv) != "1" {
		t.Skipf("set %s=1 to record K22 and the negative control", recordEnv)
	}
	for _, firstHold := range []bool{true, false} {
		variant := map[bool]string{true: "iv-b/firsthold", false: "iv-a/plain-token"}[firstHold]
		t.Run("K22: cold 64 at 1 s service/"+variant, func(t *testing.T) {
			const (
				reps    = 3
				service = time.Second
			)
			var walls, leaderDone, waiterDone []time.Duration
			for range reps {
				srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: serviceHandler(service)})
				tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
				tr.firstHold = firstHold
				start := time.Now()
				calls := fanOut(fanN, func(i int) result { return get(t.Context(), tr, srv.URL()+"/k22/"+strconv.Itoa(i)) })
				var last time.Time
				for _, r := range calls {
					if r.Err != nil {
						t.Fatalf("call: %v", r.Err)
					}
					if r.Done.After(last) {
						last = r.Done
					}
					if r.Reused {
						waiterDone = append(waiterDone, r.Done.Sub(start))
					} else {
						leaderDone = append(leaderDone, r.Done.Sub(start))
					}
				}
				walls = append(walls, last.Sub(start))
				if srv.Accepts() != 1 {
					t.Errorf("accepts %d, want 1", srv.Accepts())
				}
			}
			record(t, "case", "k22-cold64-service1s", "variant", variant, "reps", reps, "service_ms", ms(service),
				"wall_p50_ms", ms(pct(walls, 0.5)), "wall_max_ms", ms(pct(walls, 1)),
				"leader_done_p50_ms", ms(pct(leaderDone, 0.5)),
				"waiter_done_p50_ms", ms(pct(waiterDone, 0.5)), "waiter_done_p99_ms", ms(pct(waiterDone, 0.99)))
		})
	}

	t.Run("negative control: the plain token at a 20 ms leader delay", func(t *testing.T) {
		passed, failedB := 0, 0
		for range fanReps {
			b := newBarrier(fanN, fanGuard)
			b.free, b.freeDelay = "cold", leadDelay
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: b})
			tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
			tr.firstHold = false
			cold := fanOut(fanN, func(i int) result { return get(t.Context(), tr, srv.URL()+"/cold/"+strconv.Itoa(i)) })
			_, problems := checkOrdering(b, srv, cold)
			if len(problems) == 0 {
				passed++
			}
			for _, p := range problems {
				if len(p) > 3 && p[:3] == "(b)" {
					failedB++
					break
				}
			}
		}
		record(t, "case", "negative-control", "variant", "iv-a/plain-token", "lead_delay_ms", ms(leadDelay),
			"ordering_passed", fmt.Sprintf("%d/%d", passed, fanReps), "failed_b", fmt.Sprintf("%d/%d", failedB, fanReps))
	})
}
