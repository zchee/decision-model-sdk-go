// Copyright 2026 The decision-model-sdk-go Authors.
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

// UnknownAnswerFlood returns a System One response body of at least size
// bytes and the number of answers it holds: after a model and a usage, only
// answers of the type "x", which no version of the SDK models, each named
// "a" and its index and holding nothing but its type:
//
//	{"model":"m","usage":{"input_tokens":1,"output_tokens":1},"answers":{"a0":{"type":"x"},"a1":{"type":"x"},…}}
//
// It is a hostile body: a decode keeps one answer entry for each of about
// 20 bytes of it, so its scratch outgrows the body many times over. The body
// is compact, deterministic and decodes without a failure, every answer
// skipped.
func UnknownAnswerFlood(size int) (body []byte, answers int) {
	b := make([]byte, 0, size+64)
	b = append(b, `{"model":"m","usage":{"input_tokens":1,"output_tokens":1},"answers":{`...)
	for len(b) < size || answers == 0 {
		if answers > 0 {
			b = append(b, ',')
		}
		b = append(b, `"a`...)
		b = strconv.AppendInt(b, int64(answers), 10)
		b = append(b, `":{"type":"x"}`...)
		answers++
	}
	b = append(b, `}}`...)
	return b, answers
}
