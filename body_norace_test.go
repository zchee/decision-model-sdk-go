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

//go:build !race

package typesafe

import (
	"strings"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// allocItem and allocState make a struct state of about 1 KiB, shaped like
// spike S-E1's.
type allocItem struct {
	ID    int64    `json:"id"`
	Text  string   `json:"text"`
	Tags  []string `json:"tags"`
	Score float64  `json:"score"`
	Rank  int      `json:"rank"`
}

type allocState struct {
	Name  string      `json:"name"`
	Items []allocItem `json:"items"`
}

// TestAllocBodyKinds pins NF1 at the body level: one request body of each
// state kind at 1 KiB, assembled around the prepared questions and released
// on a warm pool, allocates E(kind) + B (docs/perf/frozen-budgets.md, AC-P1)
// and at most 112 bytes. The state is passed to encodeBody's any parameter
// inside the measured section, so a value that is not pointer-shaped pays B
// = 1 for its boxing there, as it does at SystemOne's: a bare string 2
// (sonic 1 + B), a boxed string 1, a bare RawJSON 1 (0 + B), a boxed RawJSON
// 0, a *struct 1 and a flat map 2 (1 + one map). Opening readers is the
// transport's cost and is not measured here.
//
// The file is built without -race: under the race detector sync.Pool.Put
// drops one value in four, so a warm pool is not warm.
func TestAllocBodyKinds(t *testing.T) {
	testsupport.QuietRuntime(t)
	qs := prepared(t, NewQuestions().
		Noul("billing", Noul{Instructions: Text("Is this about billing?"), Yes: Text("payments or invoices")}).
		Choice("tone", Choice{Instructions: Text("What is the tone?"), Options: Options{{"calm", Text("neutral or polite")}, {Label: "angry"}}}).
		Score("urgency", Score{Levels: []Content{Text("can wait"), Text("this week"), Text("today")}}))

	s := strings.Repeat("s", 1022)
	var boxed any = s
	st := &allocState{Name: "state", Items: make([]allocItem, 10)}
	for i := range st.Items {
		st.Items[i] = allocItem{ID: int64(i), Text: strings.Repeat("abcdefghij", 4), Tags: []string{"alpha", "beta"}, Score: 0.5, Rank: i}
	}
	flat := make(map[string]any, 16)
	for i := range 16 {
		flat["k"+string(rune('a'+i))+"000000"] = strings.Repeat("0123456789abcdef", 3)
	}
	raw := RawJSON(`{"items":[` + strings.Repeat(`{"id":1,"text":"abcdefghijabcdefghijabcdefghijabcdefghij","tags":["alpha","beta"]},`, 12) + `{}]}`)
	var boxedRaw any = raw

	tests := map[string]struct {
		call func(t *testing.T) int // encodes and releases one body, returning its length
		want uint64
	}{
		"success: bare string":       {call: func(t *testing.T) int { return encodeRelease(t, s, qs) }, want: 2},
		"success: boxed string":      {call: func(t *testing.T) int { return encodeRelease(t, boxed, qs) }, want: 1},
		"success: bare RawJSON":      {call: func(t *testing.T) int { return encodeRelease(t, raw, qs) }, want: 1},
		"success: boxed RawJSON":     {call: func(t *testing.T) int { return encodeRelease(t, boxedRaw, qs) }, want: 0},
		"success: pointer to struct": {call: func(t *testing.T) int { return encodeRelease(t, st, qs) }, want: 1},
		"success: flat map":          {call: func(t *testing.T) int { return encodeRelease(t, flat, qs) }, want: 2},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var n int
			call := func(struct{}) { n = tt.call(t) }
			call(struct{}{}) // warms the pool and sonic's compiled encoder for the type
			got := testsupport.MeasureMin(t, name, func() struct{} { return struct{}{} }, call)
			t.Logf("%s: body %d bytes, %s mallocs/bytes", name, n, got)
			if got.Mallocs != tt.want {
				t.Errorf("mallocs = %d, want %d (NF1: E(kind) + B)", got.Mallocs, tt.want)
			}
			if got.Bytes > 112 {
				t.Errorf("bytes = %d, want at most 112 per warm-pool call (AC-P1)", got.Bytes)
			}
		})
	}
}

// encodeRelease encodes one request body around qs and releases it,
// returning its length. The state reaches encodeBody's any parameter here,
// inside the caller's measured section.
func encodeRelease[S any](t *testing.T, state S, qs *Prepared) int {
	body, err := encodeBody(state, "jev-latest", qs, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := body.Len()
	body.Release()
	return n
}
