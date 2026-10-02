// Copyright 2026 The typesafe-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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

// Package prob ports upstream's probability arithmetic (system-one-adapter
// v0.2.1: _utils/probability_normalization.py, _utils/confidence_metrics.py
// and the score of _client.py:141-142) so that every result has the bits
// CPython 3.14.3 gives.
//
// The bits differ from a plain Go port in three places, each needed for
// equality and each tested against the committed vectors:
//
//   - every sum is CPython's sum() of floats, a compensated sum, not s += x
//     (Sum and the unexported accumulator);
//   - every product that feeds a sum is rounded to float64 before it is added
//     (the accumulator's addProduct);
//   - Python's max keeps its first candidate unless a later one is strictly
//     greater, so a NaN never wins and max(0.0, y) is +0.0 for a NaN or -0.0;
//     Go's builtin max, math.Max and slices.Max propagate a NaN, so this
//     package compares with > in a loop.
//
// Distributions are []float64 in answer order: the order of a question's
// criteria, which the caller keeps.
package prob

import "math"

// compensated is CPython 3.14.3's sum() of floats (Python/bltinmodule.c,
// lines 2722-2990 at the tag v3.14.3): a running sum and the low-order bits
// its additions lost (CompensatedSum, lines 2730-2733).
type compensated struct {
	hi, lo  float64
	started bool
}

// add adds one term, as one step of sum() over floats does.
func (c *compensated) add(x float64) {
	if !c.started {
		// sum() starts from the int 0 (bltinmodule.c:2791-2792), so the first
		// float leaves the int loop through PyNumber_Add(0, x), a plain
		// 0.0 + x (2859-2869), which turns a leading -0.0 into +0.0. The float
		// loop then starts from that value with no compensation (2877-2878).
		c.hi = 0.0 + x
		c.started = true
		return
	}
	// cs_add, bltinmodule.c:2741-2752: Neumaier's form of Kahan's sum. A NaN
	// makes the comparison false, so the second form runs, as in C.
	t := c.hi + x
	if math.Abs(c.hi) >= math.Abs(x) {
		c.lo += (c.hi - t) + x
	} else {
		c.lo += (x - t) + c.hi
	}
	c.hi = t
}

// addProduct adds the product a*b, rounded to float64 before it is added.
//
// Python computes the product and the sum in separate calls, so the product
// is rounded once and the sum once. The Go specification lets a compiler
// fuse x*y + z into one instruction that rounds once; the explicit
// conversion float64(a * b) forbids that. The tests detect a missing
// conversion on arm64 (the macOS CI image), where the compiler fuses this
// code and TestSumMatchesCPython then fails. A compiler may also fuse on
// amd64 built with GOAMD64=v3 or higher, but the committed tests do not
// detect a missing conversion there, and a default amd64 build
// (GOAMD64=v1, the Linux and Windows CI images) never fuses. So the
// conversion stays although removing it fails no test on amd64.
// TestProductIsRoundedBeforeTheSum shows on every host that the vectors
// tell a fused sum from CPython's.
func (c *compensated) addProduct(a, b float64) {
	c.add(float64(a * b))
}

// value is cs_to_double, bltinmodule.c:2754-2764: the compensation is added
// only when it is nonzero and finite, so that an infinite or overflowed sum
// does not become NaN and a zero compensation does not change a -0.0.
func (c *compensated) value() float64 {
	if c.lo != 0 && !math.IsInf(c.lo, 0) && !math.IsNaN(c.lo) {
		return c.hi + c.lo
	}
	return c.hi
}

// Sum returns what CPython 3.14.3's builtin sum() returns for a list of
// floats, bit for bit. For an empty list Python returns the int 0; Sum
// returns 0.
func Sum(xs []float64) float64 {
	var c compensated
	for _, x := range xs {
		c.add(x)
	}
	return c.value()
}
