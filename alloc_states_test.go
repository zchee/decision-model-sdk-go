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

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// The request states of AC-P1 (plan section 6.1, spike S-E1): one value of
// each state kind a caller passes, at the sizes the budget names. The file
// is built with and without -race: the allocation budgets over these states
// are TestAllocEncode and TestAllocScratchSequence (//go:build !race), and
// their functional halves, which also run under the race detector, are in
// alloc_functional_test.go.

// allocSizes are AC-P1's single-size targets: states whose JSON encoding is
// about 1 KiB, 64 KiB, 1 MiB and 6 MiB, S-E1's sizes below the scratch
// ceiling (codec.ScratchCeiling, 8 MiB).
var allocSizes = []int{1 << 10, 64 << 10, 1 << 20, 6 << 20}

// sizeName renders a size as 1KiB, 64KiB, 1MiB and so on.
func sizeName(n int) string {
	if n >= 1<<20 {
		return strconv.Itoa(n>>20) + "MiB"
	}
	return strconv.Itoa(n>>10) + "KiB"
}

// allocItem and allocState make the struct state, shaped like spike S-E1's:
// about 100 bytes of JSON per item.
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

// stateKind is one request state kind of AC-P1, S-E1's kinds.
type stateKind struct {
	name string
	// bare reports that a caller passes the state unboxed, so that passing
	// it to the SDK's any parameter boxes it: NF1's B = 1.
	bare bool
	// sonic is E(kind) for a state without maps: 1 for what sonic encodes,
	// 0 for RawJSON, which the SDK appends as it is. A state holding m maps
	// costs sonic 1 + m (frozen-budgets.md AC-P1, G2 (b)).
	sonic uint64
	// build returns the case for a state whose encoding is about size bytes.
	build func(tb testing.TB, size int) stateCase
}

// stateCase is one state of a kind and size, as the tests pass it.
type stateCase struct {
	// boxed is the state in an any, as the SDK's parameter holds it: the
	// input of E_sonic, and the state of every SystemOne call in the tests.
	boxed any
	// pass encodes a request body around qs with the state as a caller
	// passes it, bare or boxed; a bare state is boxed inside the call, as a
	// caller's argument is.
	pass func(qs *Prepared) (codec.Body, error)
	// maps is the number of maps the state holds, the root included.
	maps uint64
	// json is the state's own JSON encoding, the body's "state" member.
	json []byte
}

// wantE returns the frozen E(kind) of a state holding maps maps.
func (k stateKind) wantE(maps uint64) uint64 {
	if maps > 0 {
		return 1 + maps
	}
	return k.sonic
}

// b returns NF1's B for the kind: 1 when a caller's argument is boxed.
func (k stateKind) b() uint64 {
	if k.bare {
		return 1
	}
	return 0
}

// stateKinds are the kinds AC-P1 budgets, in report order. The sequence of
// section 6.1.6 uses the boxed ones.
var stateKinds = []stateKind{
	{name: "string", bare: true, sonic: 1, build: func(_ testing.TB, size int) stateCase {
		s := stateString(size)
		return stateCase{boxed: s, pass: func(qs *Prepared) (codec.Body, error) { return encodeBody(s, DefaultModel, qs, nil) }, json: []byte(strconv.Quote(s))}
	}},
	{name: "boxed-string", sonic: 1, build: func(_ testing.TB, size int) stateCase {
		var s any = stateString(size)
		return stateCase{boxed: s, pass: func(qs *Prepared) (codec.Body, error) { return encodeBody(s, DefaultModel, qs, nil) }, json: []byte(strconv.Quote(s.(string)))}
	}},
	{name: "RawJSON", bare: true, build: func(tb testing.TB, size int) stateCase {
		raw := RawJSON(nestedMapJSON(tb, size))
		return stateCase{boxed: raw, pass: func(qs *Prepared) (codec.Body, error) { return encodeBody(raw, DefaultModel, qs, nil) }, json: raw}
	}},
	{name: "boxed-RawJSON", build: func(tb testing.TB, size int) stateCase {
		raw := nestedMapJSON(tb, size)
		var boxed any = RawJSON(raw)
		return stateCase{boxed: boxed, pass: func(qs *Prepared) (codec.Body, error) { return encodeBody(boxed, DefaultModel, qs, nil) }, json: raw}
	}},
	{name: "json.RawMessage", sonic: 1, build: func(tb testing.TB, size int) stateCase {
		raw := nestedMapJSON(tb, size)
		boxed := testsupport.StdlibRawMessage(raw)
		return stateCase{boxed: boxed, pass: func(qs *Prepared) (codec.Body, error) { return encodeBody(boxed, DefaultModel, qs, nil) }, json: raw}
	}},
	{name: "pointer-to-struct", sonic: 1, build: func(tb testing.TB, size int) stateCase {
		st := calibrate(tb, size, makeStruct)
		var boxed any = st
		return stateCase{boxed: boxed, pass: func(qs *Prepared) (codec.Body, error) { return encodeBody(st, DefaultModel, qs, nil) }, json: sonicJSON(tb, boxed)}
	}},
	{name: "flat-map", sonic: 1, build: func(tb testing.TB, size int) stateCase {
		m := calibrate(tb, size, func(n int) map[string]any { return makeMap(n, true) })
		var boxed any = m
		return stateCase{boxed: boxed, pass: func(qs *Prepared) (codec.Body, error) { return encodeBody(m, DefaultModel, qs, nil) }, maps: countMaps(m), json: sonicJSON(tb, boxed)}
	}},
	{name: "nested-map", sonic: 1, build: func(tb testing.TB, size int) stateCase {
		m := nestedMap(tb, size)
		var boxed any = m
		return stateCase{boxed: boxed, pass: func(qs *Prepared) (codec.Body, error) { return encodeBody(m, DefaultModel, qs, nil) }, maps: countMaps(m), json: sonicJSON(tb, boxed)}
	}},
}

// stateKindNamed returns the kind called name.
func stateKindNamed(tb testing.TB, name string) stateKind {
	tb.Helper()
	for _, k := range stateKinds {
		if k.name == name {
			return k
		}
	}
	tb.Fatalf("no state kind %q", name)
	return stateKind{}
}

// stateKey names one built state.
type stateKey struct {
	kind string
	size int
}

// stateCache holds every state built, by kind and size, for the whole test
// binary: the 6 MiB and 9 MiB states take long to build and are shared by
// the tests of both halves.
var (
	stateMu    sync.Mutex
	stateCache = map[stateKey]stateCase{}
)

// stateFor returns the state of kind k whose encoding is about size bytes,
// built once per process.
func stateFor(tb testing.TB, k stateKind, size int) stateCase {
	tb.Helper()
	stateMu.Lock()
	defer stateMu.Unlock()
	key := stateKey{k.name, size}
	if sc, ok := stateCache[key]; ok {
		return sc
	}
	sc := k.build(tb, size)
	stateCache[key] = sc
	return sc
}

// stringCache and nestedCache share the underlying values between the kinds
// built from them (string and boxed-string; the nested map and the three
// raw kinds), so a size is built once. Both are guarded by stateMu.
var (
	stringCache = map[int]string{}
	nestedCache = map[int]map[string]any{}
	rawCache    = map[int][]byte{}
)

// stateString returns a string whose JSON encoding is size bytes.
func stateString(size int) string {
	if s, ok := stringCache[size]; ok {
		return s
	}
	s := strings.Repeat("s", size-2)
	stringCache[size] = s
	return s
}

// nestedMap returns the nested-map state whose encoding is about size bytes.
func nestedMap(tb testing.TB, size int) map[string]any {
	if m, ok := nestedCache[size]; ok {
		return m
	}
	m := calibrate(tb, size, func(n int) map[string]any { return makeMap(n, false) })
	nestedCache[size] = m
	return m
}

// nestedMapJSON returns sonic's encoding of the nested-map state of size,
// the raw kinds' bytes (S-E1's choice): a JSON value of that size with
// every kind of member.
func nestedMapJSON(tb testing.TB, size int) []byte {
	if raw, ok := rawCache[size]; ok {
		return raw
	}
	raw := sonicJSON(tb, nestedMap(tb, size))
	rawCache[size] = raw
	return raw
}

// Values of the struct and map states: 40 and 48 bytes, no escapes.
var (
	textChunk = strings.Repeat("abcdefghij", 4)
	valueText = strings.Repeat("0123456789abcdef", 3)
)

// makeStruct returns an *allocState with n items.
func makeStruct(n int) *allocState {
	st := &allocState{Name: "state", Items: make([]allocItem, max(n, 1))}
	for i := range st.Items {
		st.Items[i] = allocItem{ID: int64(i), Text: textChunk, Tags: []string{"alpha", "beta"}, Score: 0.5, Rank: i % 10}
	}
	return st
}

// makeMap returns a map[string]any with n entries: when flat, every value a
// string; otherwise strings, numbers, nested maps and slices in turn, as
// S-E1's map kinds.
func makeMap(n int, flat bool) map[string]any {
	n = max(n, 1)
	m := make(map[string]any, n)
	for i := range n {
		k := "k" + strconv.Itoa(1000000+i)
		switch {
		case flat || i%4 == 0:
			m[k] = valueText
		case i%4 == 1:
			m[k] = float64(i) + 0.25
		case i%4 == 2:
			m[k] = map[string]any{"a": "nested", "b": int64(i)}
		default:
			m[k] = []any{"x", 1.5, true}
		}
	}
	return m
}

// countMaps returns the number of maps v holds, itself included.
func countMaps(v any) uint64 {
	switch v := v.(type) {
	case map[string]any:
		n := uint64(1)
		for _, e := range v {
			n += countMaps(e)
		}
		return n
	case []any:
		var n uint64
		for _, e := range v {
			n += countMaps(e)
		}
		return n
	}
	return 0
}

// calibrate returns build(n) for the element count n whose encoding is
// about size bytes: it encodes a sample of 1000 elements with sonic and
// scales.
func calibrate[T any](tb testing.TB, size int, build func(n int) T) T {
	tb.Helper()
	const sample = 1000
	per := float64(len(sonicJSON(tb, build(sample)))) / sample
	return build(max(int(float64(size)/per+0.5), 1))
}

// sonicJSON returns the SDK encoder's encoding of v, sonic's.
func sonicJSON(tb testing.TB, v any) []byte {
	tb.Helper()
	var buf []byte
	if err := codec.EncodeState(&buf, v); err != nil {
		tb.Fatalf("encoding a %T state: %v", v, err)
	}
	return buf
}

// encodeQuestions is the question set of the AC-P1 body tests: three
// questions, one of each kind.
func encodeQuestions(t testing.TB) *Prepared {
	t.Helper()
	return mustPrepared(t, NewQuestions().
		Noul("billing", Noul{Instructions: Text("Is this about billing?"), Yes: Text("payments or invoices")}).
		Choice("tone", Choice{Instructions: Text("What is the tone?"), Options: Options{{"calm", Text("neutral or polite")}, {Label: "angry"}}}).
		Score("urgency", Score{Levels: []Content{Text("can wait"), Text("this week"), Text("today")}}))
}

// The AC-P1 mixed-size sequence of plan section 6.1.6: 32 calls on one
// client, a 1 KiB state ×10, a 6 MiB state, 1 KiB ×10, a 9 MiB state and
// 1 KiB ×10.
const (
	sequenceCalls = 32
	sequenceSix   = 11 // the call whose state is 6 MiB
	sequenceNine  = 22 // the call whose state is 9 MiB, past the ceiling
)

// sequenceSizes are the sizes of the sequence's three states.
var sequenceSizes = [3]int{1 << 10, 6 << 20, 9 << 20}

// sequenceState returns the index in sequenceSizes of the state that call
// (1 to 32) sends.
func sequenceState(call int) int {
	switch call {
	case sequenceSix:
		return 1
	case sequenceNine:
		return 2
	}
	return 0
}

// sequenceKind is one state kind of the AC-P1 sequence and what the frozen
// row asserts of it.
type sequenceKind struct {
	name string
	// frozen is the encode-level count of calls 2 to 32 in the notation of
	// frozen-budgets.md (the state's encode and the body around it, as
	// S-E1 measured it), or "" for a kind that is recorded, not asserted.
	frozen string
	// g6 and g9 are the growth budgets of S-E1: the allocations of one
	// encode of the 6 MiB and the 9 MiB state into a fresh 4 KiB scratch,
	// E included.
	g6, g9 uint64
}

// sequenceKinds are the kinds the sequence runs: the boxed string and
// RawJSON states, whose growth is exact on both architectures, asserted, and
// the *struct and flat-map states, recorded per host (owner decision G3 (b),
// rulings R22 and R22b; K24 is W5.3's).
var sequenceKinds = []sequenceKind{
	{name: "boxed-string", frozen: "1×9 | 2 | 1×10 | 2 | 2 | 1×9", g6: 2, g9: 2},
	{name: "boxed-RawJSON", frozen: "0×9 | 1 | 0×10 | 1 | 1 | 0×9", g6: 1, g9: 1},
	{name: "pointer-to-struct"},
	{name: "flat-map"},
}

// sequenceStates returns the kind's three states, boxed, and the lengths of
// the request bodies they make with qs.
func sequenceStates(t *testing.T, k stateKind, qs *Prepared) (states [3]any, lens [3]int) {
	t.Helper()
	for i, size := range sequenceSizes {
		states[i] = stateFor(t, k, size).boxed
		body, err := encodeBody(states[i], DefaultModel, qs, nil)
		if err != nil {
			t.Fatalf("encodeBody: %v", err)
		}
		lens[i] = body.Len()
		body.Release()
	}
	return states, lens
}

// sequenceProbe makes the sequence's 32 calls on c and returns, after each,
// the capacity of the scratch the pool hands out next (codec.NewBody), which
// it hands back at once: the scratch the call left, or a fresh 4 KiB one
// when the pool dropped it or never got it back. The probe's own fresh
// scratch then serves the next call, so a probed sequence is not a measured
// one.
func sequenceProbe(t *testing.T, c *Client, qs *Prepared, states [3]any) [sequenceCalls]int {
	t.Helper()
	var caps [sequenceCalls]int
	for i := range caps {
		if _, err := c.SystemOne(t.Context(), states[sequenceState(i+1)], qs); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		next := codec.NewBody()
		caps[i] = cap(*next.Buffer())
		next.Release()
	}
	return caps
}

// sequenceDrops returns the calls (1 to 32) after which the pool no longer
// held a scratch large enough for the call's body: the scratch was dropped.
func sequenceDrops(caps [sequenceCalls]int, lens [3]int) []int {
	var drops []int
	for i, c := range caps {
		if c < lens[sequenceState(i+1)] {
			drops = append(drops, i+1)
		}
	}
	return drops
}

// sequenceNotation renders counts in the notation of frozen-budgets.md's
// AC-P1 row: runs of equal values as "v×n", joined by " | ".
func sequenceNotation(counts []uint64) string {
	var sb strings.Builder
	for i := 0; i < len(counts); {
		j := i
		for j < len(counts) && counts[j] == counts[i] {
			j++
		}
		if sb.Len() > 0 {
			sb.WriteString(" | ")
		}
		sb.WriteString(strconv.FormatUint(counts[i], 10))
		if j-i > 1 {
			sb.WriteString("×" + strconv.Itoa(j-i))
		}
		i = j
	}
	return sb.String()
}

// parseSequenceNotation expands the notation of [sequenceNotation]: "v×n"
// is n values v, "v" one; parts are separated by "|".
func parseSequenceNotation(tb testing.TB, s string) []uint64 {
	tb.Helper()
	var counts []uint64
	for part := range strings.SplitSeq(s, "|") {
		value, times, found := strings.Cut(strings.TrimSpace(part), "×")
		n := 1
		v, err := strconv.ParseUint(value, 10, 64)
		if err == nil && found {
			n, err = strconv.Atoi(times)
		}
		if err != nil {
			tb.Fatalf("sequence notation %q: part %q: %v", s, part, err)
		}
		for range n {
			counts = append(counts, v)
		}
	}
	return counts
}
