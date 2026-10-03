//go:build !go1.28 && (amd64 || arm64)

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

package codec

import (
	"runtime"
	"strings"
	"testing"

	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
	"github.com/zchee/decision-model-sdk-go/internal/testsupport/naive"
	"github.com/zchee/decision-model-sdk-go/internal/wire"
)

// decodeBenchFixtures are the valid bodies every decode benchmark runs: the
// fixtures whose decode budgets docs/perf/frozen-budgets.md pins, in the
// order of its table.
var decodeBenchFixtures = []string{
	"result.json",
	"type-last.json",
	"duplicates.json",
	"result-20.json",
	"score-flood-mini.json",
	"escaped-names.json",
	"escaped-member-names.json",
	"structured-legend.json",
	"deviation-lone-surrogate.json",
	"unknown-answer-type.json",
	"parity-big-exp-unknown.json",
	"no-answers.json",
	"structured-legend-flood-1k.json",
	"structured-legend-flood-10k.json",
}

// BenchmarkDecode measures the production decode of each fixture as a call
// makes it: the pooled decoder is warm, the result is fresh each iteration,
// and the question set and model are the ones the response answers, so every
// string is interned. Its sub-benchmarks pair with BenchmarkDecodeNaiveSonic's
// by name, so that the two can be compared.
func BenchmarkDecode(b *testing.B) {
	for _, name := range decodeBenchFixtures {
		body := testsupport.Fixture(b, name)
		first, _, _, err := decodeBody(b, body, nil, "")
		if err != nil {
			b.Fatalf("%s: %v", name, err)
		}
		q, model := questionsFor(b, first), first.Model
		b.Run(strings.TrimSuffix(name, ".json"), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				var res wire.SystemOneResult
				if _, err := DecodeSystemOne(body, q, model, &res); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDecodeNaiveSonic is the naive comparator of B2
// (docs/perf/benchmarks.md): internal/testsupport/naive's decode with sonic,
// sonic.Unmarshal of the same bodies into a map[string]any, which validates
// the JSON (less strictly: it takes raw control characters and invalid UTF-8)
// and builds a generic tree without the SDK's checks or types; call/naive
// decodes the same way. A body the codec refuses has no row: decoding into a
// map[string]any, sonic refuses 1e400 anywhere on both architectures ("float
// infinity" on arm64, "float number is infinity" on amd64), so
// parity-big-exp-unknown, which the SDK and the Python SDK accept, has no
// naive row. The decode budget "≤ 0.5 × naive" of docs/perf/frozen-budgets.md
// compares result's allocations with BenchmarkDecode's.
func BenchmarkDecodeNaiveSonic(b *testing.B) {
	benchmarkDecodeNaive(b, naive.Sonic)
}

// BenchmarkDecodeNaiveJSON is BenchmarkDecodeNaiveSonic with encoding/json,
// the second comparator, reported only. encoding/json also refuses 1e400 into
// a float64, so parity-big-exp-unknown has no row here either.
func BenchmarkDecodeNaiveJSON(b *testing.B) {
	benchmarkDecodeNaive(b, naive.StdJSON)
}

// benchmarkDecodeNaive runs cd's decode into a map[string]any over each
// fixture that cd accepts, one sub-benchmark per fixture named as
// BenchmarkDecode's.
func benchmarkDecodeNaive(b *testing.B, cd naive.Codec) {
	for _, name := range decodeBenchFixtures {
		body := testsupport.Fixture(b, name)
		if _, err := cd.Decode(body); err != nil {
			b.Logf("%s: %s refuses it on %s, no row: %v", name, cd.Name, runtime.GOARCH, err)
			continue
		}
		b.Run(strings.TrimSuffix(name, ".json"), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				if _, err := cd.Decode(body); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
