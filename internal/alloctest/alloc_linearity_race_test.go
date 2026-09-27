//go:build race

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

// TestLinearityFloodTimeUnderRace runs AC-P8's timing spans under the race
// detector, through linearityRatio, and logs the ratio in the LINEARITY line
// without asserting the bound, which TestLinearityFloodTime asserts in the
// build without the race detector (alloc_linearity_test.go; ruling
// D-W6.6-ci-linearity). Under -race, with ci.yaml's atomic coverage over
// every package, a 10^3 decode takes 30 to 67 ms instead of well under a
// millisecond, and a 10^4 decode outlasts linearitySpan, so each of its
// spans holds one decode and the minimum is taken over five single samples:
// on a shared 4-CPU runner that reading measures the instrumentation and the
// runner, not the decoder (15.43 in ci 36297648346, 10.3 on (L) in the same
// build, ledger W6.6-15). Its name differs from the asserting test's, so the
// two files' build constraints swapped would leave ci.yaml's non-race list
// naming a test the build does not have and a test built only without -race
// that the list does not name, which the step's guards refuse by name. It
// runs in CI's -race step on the three images.
func TestLinearityFloodTimeUnderRace(t *testing.T) {
	ratio := linearityRatio(t)
	t.Logf("the ratio %.2f is not asserted under the race detector; TestLinearityFloodTime asserts at most 15 in the build without it (alloc_linearity_test.go)", ratio)
}
