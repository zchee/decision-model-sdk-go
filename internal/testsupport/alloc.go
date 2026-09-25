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

package testsupport

import (
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
)

// AllocRuns is the number of times [MeasureMin] measures a section.
const AllocRuns = 5

// AllocAgree is how many of the [AllocRuns] runs must equal the minimum for
// [MeasureMin] and [StableMin] to accept it.
//
// The runtime's counters are process-wide: a goroutine the test did not start
// (the testing package's own bookkeeping, a timer, a finalizer) can only add
// to a section, never take from it, and a repeated identical call costs the
// SDK the same every time by design, so the minimum is the SDK's cost. The
// minimum alone would hide an allocation made in some calls and not in
// others, so it has to be the value of at least AllocAgree runs; a run
// polluted by foreign work is visible in the printed runs, and when it splits
// across enough runs the check fails (a flake), never passes falsely. The
// same rule as the Rust port's tests/support/mod.rs.
const AllocAgree = 3

// Allocs is the change in the runtime's allocation counters across one
// measured section.
type Allocs struct {
	// Mallocs is the number of heap objects allocated
	// (runtime.MemStats.Mallocs).
	Mallocs uint64
	// Bytes is the number of heap bytes allocated
	// (runtime.MemStats.TotalAlloc).
	Bytes uint64
}

// String renders the counts as "<mallocs>/<bytes>".
func (a Allocs) String() string {
	return fmt.Sprintf("%d/%d", a.Mallocs, a.Bytes)
}

// quiet counts the QuietRuntime calls whose cleanup has not run yet.
var quiet atomic.Int32

// QuietRuntime prepares the process for allocation counting until tb's test
// ends: it runs a collection, turns the collector off
// (debug.SetGCPercent(-1)), so a collection cannot empty a sync.Pool between
// calls, and sets GOMAXPROCS to 1, so a goroutine cannot migrate to a P whose
// pool-local slot is empty (sync.Pool.Put fills the current P's private
// slot). Both settings are restored by tb.Cleanup.
//
// The settings are process-wide, so a test that calls QuietRuntime must not
// run in parallel with other tests (no t.Parallel). Allocation budgets belong
// in files built with //go:build !race: under the race detector
// sync.Pool.Put drops one value in four.
func QuietRuntime(tb testing.TB) {
	tb.Helper()
	runtime.GC()
	prevGC := debug.SetGCPercent(-1)
	prevProcs := runtime.GOMAXPROCS(1)
	quiet.Add(1)
	tb.Cleanup(func() {
		quiet.Add(-1)
		runtime.GOMAXPROCS(prevProcs)
		debug.SetGCPercent(prevGC)
	})
}

// errNotQuiet is returned by checkQuiet when QuietRuntime is not in effect.
var errNotQuiet = errors.New("testsupport: MeasureMin needs QuietRuntime(tb) first (collector off, GOMAXPROCS 1)")

// checkQuiet reports whether QuietRuntime's settings are in effect.
func checkQuiet() error {
	if quiet.Load() <= 0 || runtime.GOMAXPROCS(0) != 1 {
		return errNotQuiet
	}
	return nil
}

// Measure runs body once and returns the change in the allocation counters
// across it, read with runtime.ReadMemStats before and after. It does not
// warm anything up: unlike testing.AllocsPerRun, whose warm-up call would
// consume pool state, the caller decides what runs before the section.
func Measure(body func()) Allocs {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	body()
	runtime.ReadMemStats(&after)
	return Allocs{
		Mallocs: after.Mallocs - before.Mallocs,
		Bytes:   after.TotalAlloc - before.TotalAlloc,
	}
}

// MeasureMin measures body [AllocRuns] times, each on a fresh input() made
// outside the measured section, logs every run, and returns the minimum that
// at least [AllocAgree] runs share (see [StableMin]). It fails the test when
// [QuietRuntime] is not in effect or when the runs do not agree.
//
// A caller that needs a warm pool runs the call once before MeasureMin.
func MeasureMin[T any](tb testing.TB, label string, input func() T, body func(T)) Allocs {
	tb.Helper()
	if err := checkQuiet(); err != nil {
		tb.Fatal(err)
	}
	runs := make([]Allocs, AllocRuns)
	for i := range runs {
		in := input()
		runs[i] = Measure(func() { body(in) })
	}
	return StableMin(tb, label, runs)
}

// StableMin logs runs and returns their minimum, taken per counter; it fails
// the test when fewer than [AllocAgree] runs equal that minimum in both
// counters, because the section does not cost the same on identical calls.
func StableMin(tb testing.TB, label string, runs []Allocs) Allocs {
	tb.Helper()
	tb.Logf("runs of %-38s mallocs/bytes:%s", label, formatRuns(runs))
	least, err := stableMin(runs)
	if err != nil {
		tb.Fatalf("%s: %v", label, err)
	}
	return least
}

// stableMin is StableMin without the test plumbing.
func stableMin(runs []Allocs) (Allocs, error) {
	if len(runs) == 0 {
		return Allocs{}, errors.New("no runs")
	}
	least := runs[0]
	for _, run := range runs[1:] {
		least.Mallocs = min(least.Mallocs, run.Mallocs)
		least.Bytes = min(least.Bytes, run.Bytes)
	}
	agree := 0
	for _, run := range runs {
		if run == least {
			agree++
		}
	}
	if agree < AllocAgree {
		return least, fmt.Errorf("the count is not stable across identical calls: %d of %d runs equal the minimum %s (mallocs/bytes), %d must; the runs:%s",
			agree, len(runs), least, AllocAgree, formatRuns(runs))
	}
	return least, nil
}

// formatRuns renders runs as " m/b m/b ...".
func formatRuns(runs []Allocs) string {
	var sb strings.Builder
	for _, run := range runs {
		sb.WriteByte(' ')
		sb.WriteString(run.String())
	}
	return sb.String()
}
