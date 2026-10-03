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
	"math"
	"testing"
)

// approx reports whether got equals want as pytest.approx(want) compares
// with its default tolerance: within 1e-6 of want relative to want, or
// within 1e-12 absolutely.
func approx(got, want float64) bool {
	return got == want || math.Abs(got-want) <= max(1e-6*math.Abs(want), 1e-12)
}

// TestConfidence ports test_confidence_metrics
// (tests/utils/test_confidence_metrics.py at v0.2.1), one case per pytest
// item, named by the item's id. The tolerance is pytest.approx's default.
func TestConfidence(t *testing.T) {
	tests := map[string]struct {
		metric func([]float64) float64
		probs  []float64
		want   float64
	}{
		"score_confidence-probabilities0-0.0":   {metric: ScoreConfidence, probs: []float64{0.2, 0.2, 0.2, 0.2, 0.2}, want: 0.0},
		"score_confidence-probabilities1-0.0":   {metric: ScoreConfidence, probs: []float64{0.04, 0.04, 0.04, 0.04, 0.04}, want: 0.0},
		"score_confidence-probabilities2-0.55":  {metric: ScoreConfidence, probs: []float64{0.01, 0.02, 0.07, 0.3, 0.6}, want: 0.55},
		"choice_confidence-probabilities3-0.0":  {metric: ChoiceConfidence, probs: []float64{0.5, 0.5}, want: 0.0},
		"choice_confidence-probabilities4-0.0":  {metric: ChoiceConfidence, probs: []float64{0.2, 0.2}, want: 0.0},
		"choice_confidence-probabilities5-0.64": {metric: ChoiceConfidence, probs: []float64{0.82, 0.18}, want: 0.64},
		"score_confidence-probabilities6-1.0":   {metric: ScoreConfidence, probs: []float64{1.0}, want: 1.0},
		"choice_confidence-probabilities7-1.0":  {metric: ChoiceConfidence, probs: []float64{1.0}, want: 1.0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.metric(tt.probs); !approx(got, tt.want) {
				t.Errorf("confidence of %v = %v, want approximately %v", tt.probs, got, tt.want)
			}
		})
	}
}
