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

package testsupport

import (
	"sync"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// allocSink keeps measured allocations reachable so the compiler cannot
// place them on the stack.
var allocSink []byte

// TestMeasureMinExactCounts pins MeasureMin's counts on sections whose cost
// is known, including a sync.Pool hit that QuietRuntime keeps warm.
func TestMeasureMinExactCounts(t *testing.T) {
	QuietRuntime(t)
	pool := sync.Pool{New: func() any { return new([4096]byte) }}
	pool.Put(pool.Get()) // warm the pool before the section, as a budget test does

	tests := map[string]struct {
		body func(int)
		want uint64
	}{
		"success: empty section": {
			body: func(int) {},
			want: 0,
		},
		"success: one escaping allocation": {
			body: func(n int) { allocSink = make([]byte, n) },
			want: 1,
		},
		"success: warm pool get and put": {
			body: func(int) { pool.Put(pool.Get()) },
			want: 0,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := MeasureMin(t, name, func() int { return 64 }, tt.body)
			if diff := gocmp.Diff(tt.want, got.Mallocs); diff != "" {
				t.Errorf("MeasureMin mallocs (-want +got):\n%s", diff)
			}
		})
	}
}
