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

package typesafe

// B1 (docs/perf/benchmarks.md): the request body encode, by state kind and
// size, next to the naive comparator's encode of the same body.
//
//   - sdk: encodeBody, the SDK's hot path: the state goes through the codec
//     into a pooled scratch, then the model and the prepared question bytes
//     are spliced in; the scratch goes back to the pool after each call.
//   - naive: sonic.Marshal of internal/testsupport/naive's plain Body with
//     the same state, model and question bytes (G3 (a)); a fresh buffer
//     every call.
//   - naive-json: the same with encoding/json, reported only.
//
// The kinds are what a caller sends: text (a string boxed in an any, the
// NF3 kind), rawjson (a RawJSON object, which the SDK appends as it is and
// both codecs validate through its MarshalJSON), struct (a *struct with one
// large text member) and map (a map[string]any with the same members). The
// text holds quotes, tabs, newlines and multi-byte characters, so the
// escaper does real work; a state of one repeated letter would flatter
// every encoder. The body is byte for byte the same for all three encoders
// for text, rawjson and struct (TestEncodeBodyMatchesNaive); a map's members
// come out in each encoder's own order.
//
// How this can mislead: the scratch is warm after the first call, so these
// are steady-state numbers; a scratch's growth and its drop past the 8 MiB
// ceiling are what TestAllocScratchSequence asserts, not what this shows.
// Time per byte grows with the share of escaped characters in the text.

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// sinkEncoded keeps the naive encodes alive.
var sinkEncoded []byte

// encodeSizes are B1's state sizes, by row name.
var encodeSizes = map[string]int{"1KiB": 1 << 10, "64KiB": 64 << 10, "1MiB": 1 << 20}

// encodeText is the unit B1's text states repeat: an ASCII sentence with
// quotes, a tab and a newline, Japanese text and an emoji, and none of <, >
// and &, which encoding/json alone escapes.
const encodeText = "He said \"please refund order 1042\".\n\tThe invoice was charged twice: 請求が二重に計上されました 🌍 "

// benchText returns whole repeats of encodeText, at most n bytes long.
func benchText(n int) string {
	return strings.Repeat(encodeText, max(n/len(encodeText), 1))
}

// benchTicket is B1's struct state: one large text member among small ones.
type benchTicket struct {
	Subject  string   `json:"subject"`
	Body     string   `json:"body"`
	Tags     []string `json:"tags"`
	Priority int      `json:"priority"`
}

// encodeKinds build B1's states of about n bytes, by row name.
var encodeKinds = map[string]func(n int) any{
	"text": func(n int) any { return benchText(n) },
	"rawjson": func(n int) any {
		return RawJSON(`{"subject":"billing","body":` + strconv.Quote(benchText(n)) + `,"tags":["billing","refund"],"priority":2}`)
	},
	"struct": func(n int) any {
		return &benchTicket{Subject: "billing", Body: benchText(n), Tags: []string{"billing", "refund"}, Priority: 2}
	},
	"map": func(n int) any {
		return map[string]any{"subject": "billing", "body": benchText(n), "tags": []any{"billing", "refund"}, "priority": 2}
	},
}

// BenchmarkEncodeBody is B1; see the comment at the top of this file.
func BenchmarkEncodeBody(b *testing.B) {
	qs := q3Questions(b)
	for _, kind := range slices.Sorted(maps.Keys(encodeKinds)) {
		for _, size := range []string{"1KiB", "64KiB", "1MiB"} {
			state := encodeKinds[kind](encodeSizes[size])
			first, err := encodeBody(state, DefaultModel, qs, nil)
			if err != nil {
				b.Fatalf("%s/%s: %v", kind, size, err)
			}
			n := int64(first.Len())
			first.Release()

			b.Run(kind+"/"+size+"/sdk", func(b *testing.B) {
				b.SetBytes(n)
				b.ReportAllocs()
				for b.Loop() {
					body, err := encodeBody(state, DefaultModel, qs, nil)
					if err != nil {
						b.Fatal(err)
					}
					body.Release()
				}
			})
			for _, nc := range naiveCodecs {
				b.Run(kind+"/"+size+"/"+nc.name, func(b *testing.B) {
					b.SetBytes(n)
					b.ReportAllocs()
					for b.Loop() {
						if sinkEncoded, err = nc.codec.Encode(state, DefaultModel, qs.w.Questions); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

// TestEncodeBodyMatchesNaive checks that B1 times the same output three
// ways: for every kind but map, at every size, the SDK's body and both
// naive encodes are the same bytes; a map's bodies have the same length,
// its members in each encoder's order.
func TestEncodeBodyMatchesNaive(t *testing.T) {
	qs := q3Questions(t)
	for kind, build := range encodeKinds {
		for size, n := range encodeSizes {
			t.Run("success: "+kind+"/"+size, func(t *testing.T) {
				state := build(n)
				body, err := encodeBody(state, DefaultModel, qs, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer body.Release()
				sdk := string(body.Bytes())
				if len(sdk) < n {
					t.Errorf("the SDK's body is %d bytes, want at least the state's %d", len(sdk), n)
				}
				for _, nc := range naiveCodecs {
					got, err := nc.codec.Encode(state, DefaultModel, qs.w.Questions)
					if err != nil {
						t.Fatalf("%s: %v", nc.name, err)
					}
					if kind == "map" {
						if len(got) != len(sdk) {
							t.Errorf("%s: map body is %d bytes, the SDK's %d", nc.name, len(got), len(sdk))
						}
						continue
					}
					if string(got) != sdk {
						t.Errorf("%s: body differs from the SDK's (%d vs %d bytes)\nnaive: %.200s\nsdk:   %.200s", nc.name, len(got), len(sdk), got, sdk)
					}
				}
			})
		}
	}
}
