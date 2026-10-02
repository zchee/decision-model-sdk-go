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

package prob

import (
	"math"
	"slices"
)

// Tolerance is upstream's PROBABILITY_TOLERANCE
// (_utils/probability_normalization.py:9): a distribution whose sum is
// farther than this from 1 is invalid.
const Tolerance = 1e-6

// Normalization is upstream's ProbabilityNormalization
// (_utils/probability_normalization.py:12-18): the result of processing one
// distribution.
type Normalization struct {
	// Probabilities is the distribution to report, in answer order.
	Probabilities []float64
	// Error is the distance of the original distribution's sum from 1.
	Error float64
	// Original is the distribution as the model gave it, set only when
	// Probabilities is its rescaled form; nil otherwise.
	Original []float64
}

// Normalize ports the probabilities branch of
// normalize_probabilities_of_all_answers
// (_utils/probability_normalization.py:100-107). Error is
// |Sum(ps) - 1|; when enabled is true and Error is greater than Tolerance,
// or NaN, Probabilities is Rescale(ps) and Original is a copy of ps;
// otherwise Probabilities is a copy of ps and Original is nil.
//
// The result never shares memory with ps: a caller may change ps afterwards.
func Normalize(ps []float64, enabled bool) Normalization {
	sumError := math.Abs(Sum(ps) - 1.0)
	// A NaN error is not <= Tolerance, so it is rescaled, as in Python.
	if !enabled || sumError <= Tolerance {
		return Normalization{Probabilities: slices.Clone(ps), Error: sumError}
	}
	return Normalization{Probabilities: Rescale(ps), Error: sumError, Original: slices.Clone(ps)}
}

// Discrete ports the discrete branch of
// normalize_probabilities_of_all_answers
// (_utils/probability_normalization.py:95-98): 1 for the answer equal to
// selected and 0 for every other, in the order of answers; all 0 when no
// answer equals selected.
func Discrete(answers []string, selected string) Normalization {
	ps := make([]float64, len(answers))
	for i, answer := range answers {
		if answer == selected {
			ps[i] = 1
		}
	}
	return Normalization{Probabilities: ps}
}

// Rescale ports rescale_probabilities
// (_utils/probability_normalization.py:57-73), which is also _normalize of
// _utils/confidence_metrics.py:27-32: each probability divided by Sum(ps),
// or 1/len(ps) for each when the sum is zero. The result is a new slice.
//
// Rescale panics on an empty distribution, where upstream raises
// ZeroDivisionError; a question has at least one answer.
func Rescale(ps []float64) []float64 {
	if len(ps) == 0 {
		panic("prob: Rescale of an empty distribution")
	}
	out := make([]float64, len(ps))
	total := Sum(ps)
	if total == 0 {
		uniform := 1.0 / float64(len(ps))
		for i := range out {
			out[i] = uniform
		}
		return out
	}
	for i, p := range ps {
		out[i] = p / total
	}
	return out
}

// Score ports a score answer's value (_client.py:141-142): the sum of
// index * probability over Rescale(ps), each product rounded before it is
// added, as Python computes it. It panics on an empty distribution.
func Score(ps []float64) float64 {
	var c compensated
	for i, p := range Rescale(ps) {
		c.addProduct(float64(i), p)
	}
	return c.value()
}

// Argmax ports max(answers, key=probabilities.__getitem__) (_client.py:159)
// and max(range(n), key=...) (_utils/confidence_metrics.py:10): the first
// index whose value no later value exceeds. Python's max replaces its
// candidate only on a strict >, so a NaN in first place stays and a NaN
// elsewhere never wins. Argmax panics on an empty distribution, where
// Python raises ValueError.
func Argmax(ps []float64) int {
	if len(ps) == 0 {
		panic("prob: Argmax of an empty distribution")
	}
	best := 0
	for i, p := range ps {
		if p > ps[best] {
			best = i
		}
	}
	return best
}

// Debug is upstream's probability debug data (probability_debug_data,
// _utils/probability_normalization.py:21-54), with questions named by their
// index in the slice DebugData was given.
type Debug struct {
	// MaxError is max_error: the largest Error, Python's max over the errors
	// in question order (a NaN in first place stays), or 0 when no question
	// has a distribution.
	MaxError float64
	// Invalid lists, in question order, the questions whose Error is
	// greater than Tolerance: probability_errors, whose length is
	// invalid_probs.
	Invalid []int
	// Rescaled lists, in question order, the questions whose Original is
	// set: original_probabilities, which upstream writes only when the list
	// is not empty.
	Rescaled []int
}

// DebugData ports probability_debug_data. norms holds one entry per question
// in question order, nil for a question that has no distribution (a noul).
func DebugData(norms []*Normalization) Debug {
	var d Debug
	first := true
	for i, n := range norms {
		if n == nil {
			continue
		}
		if first || n.Error > d.MaxError {
			d.MaxError = n.Error
			first = false
		}
		if n.Error > Tolerance {
			d.Invalid = append(d.Invalid, i)
		}
		if n.Original != nil {
			d.Rescaled = append(d.Rescaled, i)
		}
	}
	return d
}
