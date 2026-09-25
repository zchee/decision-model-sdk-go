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
	"strconv"
)

// FloodAnswer is the name of the one score answer whose legend
// [StructuredLegendFlood] fills.
const FloodAnswer = "flood"

// StructuredLegendFlood returns a System One response body whose answers are
// a noul answer "spam", a choice answer "tone" and a score answer named
// [FloodAnswer] with the given number of levels (at least 1), every one of
// them structured: an even level i maps to
// {"summary":"level i","examples":["example i"]} and an odd one to
// ["level i",{"note":null}], so the lazy pass reads both structured shapes.
// The score answer's probabilities give every level 1/levels, its score is
// the mean level (levels-1)/2 and its confidence 1/levels.
//
// The output is compact JSON without escapes or a trailing newline, and it is
// deterministic: testdata/structured-legend-flood-1k.json and -10k.json are
// this function's output for 1000 and 10000 levels (the golden test in
// flood_test.go regenerates them with -update).
func StructuredLegendFlood(levels int) []byte {
	levels = max(levels, 1)
	p := strconv.FormatFloat(1/float64(levels), 'g', -1, 64)
	b := make([]byte, 0, 256+levels*72)
	b = append(b, `{"model":"jev-latest","usage":{"input_tokens":`...)
	b = strconv.AppendInt(b, int64(levels), 10)
	b = append(b, `,"output_tokens":3},"answers":{`...)
	b = append(b, `"spam":{"type":"noul","noul":0.02},`...)
	b = append(b, `"tone":{"type":"choice","choice":"calm","confidence":0.9,"probabilities":{"calm":0.9,"angry":0.1}},`...)
	b = append(b, `"`+FloodAnswer+`":{"type":"score","score":`...)
	b = strconv.AppendFloat(b, float64(levels-1)/2, 'g', -1, 64)
	b = append(b, `,"confidence":`...)
	b = append(b, p...)
	b = append(b, `,"legend":{`...)
	for i := range levels {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '"')
		b = strconv.AppendInt(b, int64(i), 10)
		if i%2 == 0 {
			b = append(b, `":{"summary":"level `...)
			b = strconv.AppendInt(b, int64(i), 10)
			b = append(b, `","examples":["example `...)
			b = strconv.AppendInt(b, int64(i), 10)
			b = append(b, `"]}`...)
		} else {
			b = append(b, `":["level `...)
			b = strconv.AppendInt(b, int64(i), 10)
			b = append(b, `",{"note":null}]`...)
		}
	}
	b = append(b, `},"probabilities":{`...)
	for i := range levels {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '"')
		b = strconv.AppendInt(b, int64(i), 10)
		b = append(b, `":`...)
		b = append(b, p...)
	}
	b = append(b, `}}}}`...)
	return b
}
