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

package alloctest

import "testing"

// TestLinearityFloodTime checks the time ratio of the structured-legend
// floods (docs/perf/frozen-budgets.md): one 10^4 decode takes at most 15
// times as long as one 10^3 decode, measured by linearityRatio. The bound is
// a statement about the shipped decoder, which reads about 9, so it is
// asserted in this build, without the race detector; ci.yaml's step
// 'go test without -race (allocation budgets)' runs it by name on
// ubuntu-26.04, xcode-27 and windows-2025, and its list guard refuses the
// name's removal, since this file is built only without -race. Under the
// race detector TestLinearityFloodTimeUnderRace
// (alloc_linearity_race_test.go) runs the same spans and logs the ratio
// without asserting it.
func TestLinearityFloodTime(t *testing.T) {
	if ratio := linearityRatio(t); ratio > 15 {
		t.Errorf("10^4 : 10^3 time ratio = %.2f, want at most 15", ratio)
	}
}
