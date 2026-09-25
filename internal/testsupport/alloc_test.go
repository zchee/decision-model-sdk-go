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
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// TestStableMin covers the min-of-5, 3-agree rule.
func TestStableMin(t *testing.T) {
	a := func(m, b uint64) Allocs { return Allocs{Mallocs: m, Bytes: b} }
	tests := map[string]struct {
		runs    []Allocs
		want    Allocs
		wantErr string
	}{
		"success: all runs equal": {
			runs: []Allocs{a(4, 256), a(4, 256), a(4, 256), a(4, 256), a(4, 256)},
			want: a(4, 256),
		},
		"success: one polluted run is ignored": {
			runs: []Allocs{a(4, 256), a(8, 1156), a(4, 256), a(4, 256), a(4, 256)},
			want: a(4, 256),
		},
		"success: exactly three agree": {
			runs: []Allocs{a(5, 300), a(4, 256), a(6, 400), a(4, 256), a(4, 256)},
			want: a(4, 256),
		},
		"error: only two runs at the minimum": {
			runs:    []Allocs{a(4, 256), a(5, 300), a(4, 256), a(5, 300), a(5, 300)},
			want:    a(4, 256),
			wantErr: "2 of 5 runs equal the minimum 4/256",
		},
		"error: the minimum counters come from different runs": {
			runs:    []Allocs{a(3, 300), a(4, 200), a(4, 300), a(4, 300), a(4, 300)},
			want:    a(3, 200),
			wantErr: "0 of 5 runs equal the minimum 3/200",
		},
		"error: no runs": {
			wantErr: "no runs",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := stableMin(tt.runs)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("stableMin() error = %v, want it to contain %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("stableMin() error = %v", err)
			}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("stableMin() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestSpread covers the per-counter minimum and maximum of a bounded
// section's runs, which need not agree (ruling K32).
func TestSpread(t *testing.T) {
	a := func(m, b uint64) Allocs { return Allocs{Mallocs: m, Bytes: b} }
	tests := map[string]struct {
		runs      []Allocs
		wantLeast Allocs
		wantMost  Allocs
		wantErr   string
	}{
		"success: all runs equal": {
			runs:      []Allocs{a(38, 263952), a(38, 263952), a(38, 263952), a(38, 263952), a(38, 263952)},
			wantLeast: a(38, 263952),
			wantMost:  a(38, 263952),
		},
		"success: the runs of CI 36201375147 that failed StableMin": {
			runs:      []Allocs{a(42, 264240), a(39, 264000), a(38, 263952), a(38, 263952), a(39, 264016)},
			wantLeast: a(38, 263952),
			wantMost:  a(42, 264240),
		},
		"success: the counters' extremes come from different runs": {
			runs:      []Allocs{a(3, 300), a(4, 200), a(5, 250)},
			wantLeast: a(3, 200),
			wantMost:  a(5, 300),
		},
		"success: one run": {
			runs:      []Allocs{a(7, 112)},
			wantLeast: a(7, 112),
			wantMost:  a(7, 112),
		},
		"error: no runs": {
			wantErr: "no runs",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			least, most, err := spread(tt.runs)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("spread() error = %v, want it to contain %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("spread() error = %v", err)
			}
			if diff := gocmp.Diff(tt.wantLeast, least); diff != "" {
				t.Errorf("spread() least (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(tt.wantMost, most); diff != "" {
				t.Errorf("spread() most (-want +got):\n%s", diff)
			}
		})
	}
}

// TestQuietRuntimeRestores checks that QuietRuntime's settings hold inside
// the test and are restored by its cleanup. It must not run in parallel.
func TestQuietRuntimeRestores(t *testing.T) {
	procs := runtime.GOMAXPROCS(0)
	gcPercent := debug.SetGCPercent(100)
	debug.SetGCPercent(gcPercent)
	if err := checkQuiet(); !errors.Is(err, errNotQuiet) {
		t.Fatalf("checkQuiet() before QuietRuntime = %v, want errNotQuiet", err)
	}

	t.Run("quiet", func(t *testing.T) {
		QuietRuntime(t)
		if got := runtime.GOMAXPROCS(0); got != 1 {
			t.Errorf("GOMAXPROCS = %d inside QuietRuntime, want 1", got)
		}
		if got := debug.SetGCPercent(-1); got != -1 {
			t.Errorf("GC percent = %d inside QuietRuntime, want -1", got)
		}
		if err := checkQuiet(); err != nil {
			t.Errorf("checkQuiet() inside QuietRuntime = %v", err)
		}
		got := MeasureMin(t, "no-op", func() int { return 0 }, func(int) {})
		if got.Mallocs != 0 {
			t.Errorf("MeasureMin(no-op) = %v, want 0 mallocs", got)
		}
	})

	if got := runtime.GOMAXPROCS(0); got != procs {
		t.Errorf("GOMAXPROCS after the cleanup = %d, want %d", got, procs)
	}
	if got := debug.SetGCPercent(gcPercent); got != gcPercent {
		t.Errorf("GC percent after the cleanup = %d, want %d", got, gcPercent)
	}
	if err := checkQuiet(); !errors.Is(err, errNotQuiet) {
		t.Errorf("checkQuiet() after the cleanup = %v, want errNotQuiet", err)
	}
}

// TestMeasureCountsAllocations checks the direction and rough size of
// Measure's counters in every build (the race detector adds allocations of
// its own, so exact counts live in alloc_norace_test.go).
func TestMeasureCountsAllocations(t *testing.T) {
	var sink [][]byte
	got := Measure(func() {
		for range 10 {
			sink = append(sink, make([]byte, 1024))
		}
	})
	if got.Mallocs < 10 || got.Bytes < 10*1024 {
		t.Errorf("Measure(10 x 1 KiB) = %v, want at least 10 mallocs and 10240 bytes", got)
	}
	if len(sink) != 10 {
		t.Fatalf("sink has %d slices", len(sink))
	}
}
