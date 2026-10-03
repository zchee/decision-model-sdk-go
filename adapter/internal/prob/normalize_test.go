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

import (
	"maps"
	"slices"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// TestNormalizationAndDebugData ports
// test_probability_normalization_and_debug_data
// (tests/utils/test_probability_normalization.py at v0.2.1), one case per
// pytest item, named by the item's id: a noul without a distribution, a
// score and a choice question with the same raw probabilities, normalized
// or not, and the debug data built from the three. The probabilities,
// max_error and probability_errors are compared with pytest.approx's default
// tolerance; invalid_probs and original_probabilities exactly, as upstream
// compares them.
func TestNormalizationAndDebugData(t *testing.T) {
	tests := map[string]struct {
		enabled         bool
		raw             float64
		wantProbability float64
		wantOriginals   map[string][]float64
		wantMaxError    float64
		wantProbErrors  map[string]float64
	}{
		"False-0.2-0.2-None-0.6-expected_probability_errors0": {
			enabled:         false,
			raw:             0.2,
			wantProbability: 0.2,
			wantOriginals:   nil,
			wantMaxError:    0.6,
			wantProbErrors:  map[string]float64{"stars": 0.6, "genre": 0.6},
		},
		"True-0.2-0.5-expected_originals1-0.6-expected_probability_errors1": {
			enabled:         true,
			raw:             0.2,
			wantProbability: 0.5,
			wantOriginals:   map[string][]float64{"stars": {0.2, 0.2}, "genre": {0.2, 0.2}},
			wantMaxError:    0.6,
			wantProbErrors:  map[string]float64{"stars": 0.6, "genre": 0.6},
		},
		"False-0.50000025-0.50000025-None-5e-07-expected_probability_errors2": {
			enabled:         false,
			raw:             0.50000025,
			wantProbability: 0.50000025,
			wantOriginals:   nil,
			wantMaxError:    5e-7,
			wantProbErrors:  map[string]float64{},
		},
	}
	questions := []string{"positive", "stars", "genre"}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			stars := Normalize([]float64{tt.raw, tt.raw}, tt.enabled)
			genre := Normalize([]float64{tt.raw, tt.raw}, tt.enabled)
			norms := []*Normalization{nil, &stars, &genre}

			for _, i := range []int{1, 2} {
				for j, p := range norms[i].Probabilities {
					if !approx(p, tt.wantProbability) {
						t.Errorf("%s probability %d = %v, want approximately %v", questions[i], j, p, tt.wantProbability)
					}
				}
			}

			d := DebugData(norms)
			if !approx(d.MaxError, tt.wantMaxError) {
				t.Errorf("max_error = %v, want approximately %v", d.MaxError, tt.wantMaxError)
			}
			if got := len(d.Invalid); got != len(tt.wantProbErrors) {
				t.Errorf("invalid_probs = %d, want %d", got, len(tt.wantProbErrors))
			}
			gotErrors := map[string]float64{}
			for _, i := range d.Invalid {
				gotErrors[questions[i]] = norms[i].Error
			}
			if !slices.Equal(slices.Sorted(maps.Keys(gotErrors)), slices.Sorted(maps.Keys(tt.wantProbErrors))) {
				t.Fatalf("probability_errors names = %v, want %v", slices.Sorted(maps.Keys(gotErrors)), slices.Sorted(maps.Keys(tt.wantProbErrors)))
			}
			for q, want := range tt.wantProbErrors {
				if !approx(gotErrors[q], want) {
					t.Errorf("probability_errors[%s] = %v, want approximately %v", q, gotErrors[q], want)
				}
			}

			var gotOriginals map[string][]float64
			for _, i := range d.Rescaled {
				if gotOriginals == nil {
					gotOriginals = map[string][]float64{}
				}
				gotOriginals[questions[i]] = norms[i].Original
			}
			if diff := gocmp.Diff(tt.wantOriginals, gotOriginals); diff != "" {
				t.Errorf("original_probabilities mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestNormalizeDoesNotAlias checks that a Normalization never shares memory
// with the slice it was made from.
func TestNormalizeDoesNotAlias(t *testing.T) {
	tests := map[string]struct {
		in      []float64
		enabled bool
	}{
		"success: unchanged": {in: []float64{0.4, 0.6}, enabled: true},
		"success: disabled":  {in: []float64{0.2, 0.2}, enabled: false},
		"success: rescaled":  {in: []float64{0.2, 0.2}, enabled: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			in := slices.Clone(tt.in)
			n := Normalize(in, tt.enabled)
			want := Normalize(tt.in, tt.enabled)
			in[0] = 99
			if diff := gocmp.Diff(want, n); diff != "" {
				t.Errorf("changing the input changed the result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDiscrete(t *testing.T) {
	tests := map[string]struct {
		answers  []string
		selected string
		want     []float64
	}{
		"success: the selected answer":       {answers: []string{"0", "1", "2"}, selected: "1", want: []float64{0, 1, 0}},
		"success: a label":                   {answers: []string{"fiction", "nonfiction"}, selected: "fiction", want: []float64{1, 0}},
		"success: no answer equals selected": {answers: []string{"a", "b"}, selected: "c", want: []float64{0, 0}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			want := Normalization{Probabilities: tt.want}
			if diff := gocmp.Diff(want, Discrete(tt.answers, tt.selected)); diff != "" {
				t.Errorf("Discrete mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
