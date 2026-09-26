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
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// TestMemStatsFlood is AC-P5's structured-flood case (review W6.2 MAJ-1;
// docs/perf/frozen-budgets.md, AC-P5 (viii)): the SDK's own transport, over
// TLS and HTTP/2 to the in-process loopback server, answered with a 200
// whose undeclared body is 15 MiB of tiny answers of an unknown type
// (testsupport.UnknownAnswerFlood, 688 682 answers), inside the 16 MiB cap.
// The decode keeps one answer entry for each, so the call's live heap grows
// to many times the body; the clause bounds that peak and, the review's
// finding, what stays live afterwards.
//
//   - Peak: the heap (runtime.MemStats.HeapAlloc, read by another goroutine
//     on a 200 µs ticker, which on the test's one P runs only when the
//     runtime preempts the decoding goroutine, about every 10 ms: on the
//     order of 100 samples a second, 55 to 122 on the hosts and CI runners
//     measured (ledger W6-secfix-20), which the FLOOD peak line counts; so
//     the figure is a lower estimate of the true peak)
//     stays within 2.2 × (base + L), where base is the heap before the call
//     and L = 2 × cap + n × (2.25 × 288 + 64) bytes the call's largest live
//     set: readBody's buffers at their last doubling (AC-P5 (v)'s term); the
//     answer array's old and new copies while it grows, the old holding at
//     most the generator's n answers and append growing a large slice by a
//     quarter, so 2.25 n entries of 288 bytes; and the 64 bytes codec
//     charges an answer's index entry (mapEntryBytes). 2.2 is the
//     collector's limit for the default GOGC of 100: a cycle lets the heap
//     grow to twice the live heap it will mark, and 1.1 times that when it
//     overruns (runtime/mgcpacer.go, hardGoal and maxOvershoot); the base is
//     in it because the collector paces on the whole heap.
//   - Retained: after the flood call, six ordinary calls (result.json), each
//     followed by a collection, leave at most codec.DecoderCeiling + 1 MiB
//     live above the heap before the flood call, the 1 MiB being the
//     allowance for the loopback connection's two sides and the runtime,
//     whose own growth over the calls measured under 80 KiB (ledger
//     ## W6-secfix). Before the ceiling the pooled decoder kept about
//     233 MiB. The base is taken after two collections have emptied every
//     pool and one ordinary call has refilled them, so a scratch that an
//     earlier test left pooled cannot hide in it.
//
// Everything runs on one P (GOMAXPROCS 1), so the ordinary calls take the
// flood call's pool slot, the case in which the pool kept the flood's
// scratch. The control proves the pool is in play over the wire: a call
// whose answers' scratch is under the ceiling (8000 answers) leaves its
// scratch live, more than half the ceiling, and still within the bound. The
// test is //go:build !race, as every allocation budget is: under -race
// sync.Pool.Put drops one value in four, so the control would flake. It
// logs what it costs the machine that runs it (the COST line): the memory
// the runtime held from the OS at its peak (Sys − HeapReleased, sampled
// with the heap), the process's peak resident set on Linux (VmHWM, the
// whole test binary's so far) and the test's wall time.
func TestMemStatsFlood(t *testing.T) {
	began := time.Now()
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	const (
		entryBytes    = 288 // unsafe.Sizeof of codec's answer entry
		indexBytes    = 64  // codec's mapEntryBytes
		connAllowance = 1 << 20
	)
	flood, answers := testsupport.UnknownAnswerFlood(15 << 20)
	under, _ := testsupport.UnknownAnswerFlood(8000 * 21)
	small := testsupport.Fixture(t, "result.json")
	models := testsupport.Fixture(t, "models.json")
	var reply atomic.Pointer[[]byte]
	reply.Store(&small)
	srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, modelsPath) {
			_, _ = w.Write(models)
			return
		}
		// In 64 KiB writes and without a Content-Length: the undeclared
		// path, which every live 2xx takes (gzip).
		for rest := *reply.Load(); len(rest) > 0; {
			n := min(len(rest), 64<<10)
			if _, err := w.Write(rest[:n]); err != nil {
				return
			}
			rest = rest[n:]
		}
	})})
	clearEnv(t)
	c, err := NewClient(WithAPIKey(testKey), WithBaseURL(srv.URL()), WithRootCAs(testsupport.RootCAs(t)))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx := t.Context()
	if err := c.WarmUp(ctx); err != nil {
		t.Fatalf("WarmUp: %v", err)
	}
	qs, state := q3Questions(t), newAllocState()
	call := func(body *[]byte) error {
		reply.Store(body)
		_, err := c.SystemOne(ctx, state, qs)
		return err
	}
	heap := func() int64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return int64(m.HeapAlloc) //nolint:gosec // G115: a heap size, far below 2^63.
	}
	// Two collections empty every sync.Pool (a pool keeps a value through
	// one), so that no scratch an earlier test or run left pooled is in the
	// base; one ordinary call then fills the pools and warms the connection.
	runtime.GC()
	runtime.GC()
	if err := call(&small); err != nil {
		t.Fatalf("ordinary call: %v", err)
	}
	base := heap()

	var peak atomic.Int64
	var held atomic.Uint64 // Sys − HeapReleased at its peak
	var samples atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		var m runtime.MemStats
		tick := time.NewTicker(200 * time.Microsecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			runtime.ReadMemStats(&m)
			samples.Add(1)
			if h := int64(m.HeapAlloc); h > peak.Load() { //nolint:gosec // G115: a heap size, far below 2^63.
				peak.Store(h)
			}
			if h := m.Sys - m.HeapReleased; h > held.Load() {
				held.Store(h)
			}
		}
	})
	start := time.Now()
	err = call(&flood)
	took := time.Since(start)
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatalf("the flood call: %v (%T), want success: every answer is of an unknown type, skipped", err, err)
	}
	live := int64(2*DefaultMaxResponseBytes) + int64(answers)*(9*entryBytes/4+indexBytes)
	peakBound := int64(2.2 * float64(base+live))
	grew := peak.Load() - base
	t.Logf("FLOOD peak: body %d bytes, %d answers, %v; heap base %d, peak %+d bytes (%.1f× the body), bound %+d (2.2 × (base + %d)); %d samples (%.0f a second)",
		len(flood), answers, took.Round(time.Millisecond), base, grew, float64(grew)/float64(len(flood)), peakBound-base, live, samples.Load(), float64(samples.Load())/took.Seconds())
	if peak.Load() > peakBound {
		t.Errorf("the flood call's heap reached %d bytes, past 2.2 × (base %d + the largest live set %d) = %d", peak.Load(), base, live, peakBound)
	}

	bound := int64(codec.DecoderCeiling + connAllowance)
	for i := range 6 {
		if err := call(&small); err != nil {
			t.Fatalf("ordinary call #%d: %v", i+1, err)
		}
		retained := heap() - base
		t.Logf("FLOOD retained after ordinary call + GC #%d: %+d bytes (bound %d)", i+1, retained, bound)
		if retained > bound {
			t.Errorf("ordinary call + GC #%d: %d bytes stay live after the flood call, want at most codec.DecoderCeiling + 1 MiB = %d: the pool kept the flood's scratch", i+1, retained, bound)
		}
	}

	if err := call(&under); err != nil {
		t.Fatalf("the control call: %v", err)
	}
	retained := heap() - base
	t.Logf("FLOOD control: %d-byte body under the ceiling, %+d bytes live after it", len(under), retained)
	if retained <= codec.DecoderCeiling/2 || retained > bound {
		t.Errorf("after a call whose scratch is under the ceiling %d bytes stay live, want (%d, %d]: below it the pool is not in play, so the bound above proves nothing", retained, codec.DecoderCeiling/2, bound)
	}
	t.Logf("FLOOD COST: the runtime held at most %d bytes from the OS during the flood call (Sys − HeapReleased); process peak RSS %s; wall time %v",
		held.Load(), processPeakRSS(), time.Since(began).Round(time.Millisecond))
	runtime.KeepAlive(flood)
	runtime.KeepAlive(under)
}

// processPeakRSS returns the process's peak resident set size as Linux
// reports it (VmHWM in /proc/self/status), or "n/a" where there is no such
// file.
func processPeakRSS() string {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return "n/a"
	}
	for line := range strings.Lines(string(status)) {
		if v, ok := strings.CutPrefix(line, "VmHWM:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return "n/a"
}
