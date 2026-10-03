// Copyright 2026 The decision-model-sdk-go Authors.
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

package prob

import "math"

// ScoreConfidence ports score_confidence
// (_utils/confidence_metrics.py:4-14): how concentrated a score's
// distribution is around its mode, from 0 (as spread as a uniform
// distribution, or more) to 1. A distribution of one answer gives 1. It
// panics on an empty distribution.
//
// The center of the distribution, (n-1)/2, is rounded on its own before it
// is subtracted from each index, as Python computes it, so the result does
// not depend on whether the compiler fuses the halving and the subtraction
// into one rounding, which it does on arm64 without the explicit conversion.
// The halving is exact for every length, so both ways give the same bits
// today; the conversion keeps that from resting on the compiler.
func ScoreConfidence(ps []float64) float64 {
	if len(ps) == 1 {
		return 1.0
	}
	normalized := Rescale(ps)
	mode := Argmax(normalized)
	var distance compensated
	for i, p := range normalized {
		distance.addProduct(p, math.Abs(float64(i-mode)))
	}
	n := len(normalized)
	// The conversion forbids fusing the halving with the subtraction below.
	center := float64(float64(n-1) / 2)
	var deviation compensated
	for i := range n {
		deviation.add(math.Abs(float64(i) - center))
	}
	y := 1.0 - distance.value()/(deviation.value()/float64(n))
	// Python's max(0.0, y) returns y only when y > 0.0: a NaN and a -0.0 both
	// give +0.0. The builtin max and math.Max return NaN for a NaN.
	if y > 0.0 {
		return y
	}
	return 0.0
}

// ChoiceConfidence ports choice_confidence
// (_utils/confidence_metrics.py:17-24): the largest probability scaled from
// the uniform distribution's (0) to certainty (1). A distribution of one
// answer gives 1. It panics on an empty distribution.
func ChoiceConfidence(ps []float64) float64 {
	if len(ps) == 1 {
		return 1.0
	}
	normalized := Rescale(ps)
	uniform := 1.0 / float64(len(normalized))
	// max(normalized_probs) is the value at Argmax: Python's max over a list
	// keeps its first candidate unless a later one is strictly greater.
	return (normalized[Argmax(normalized)] - uniform) / (1.0 - uniform)
}
