//go:build !go1.28 && (amd64 || arm64)

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

package codec

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/bytedance/sonic/ast"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// scratchCaps is the capacity of every scratch slice of a decoder, and
// whether each index map exists.
type scratchCaps struct {
	set, probs, legend, folds, cards, nodes, raws, rawBase, strs, jsons int
	setIdx, lvlIdx, strIdx, optIdx                                      bool
	optPeak                                                             int
}

func capsOf(d *decoder) scratchCaps {
	v := &d.v
	return scratchCaps{
		set: cap(v.set), probs: cap(v.probs), legend: cap(v.legend), folds: cap(v.folds), cards: cap(v.cards),
		nodes: cap(d.nodes), raws: cap(d.raws), rawBase: cap(d.rawBase), strs: cap(d.strs), jsons: cap(d.jsons),
		setIdx: v.setIdx != nil, lvlIdx: v.lvlIdx != nil, strIdx: v.strIdx != nil, optIdx: d.optIdx != nil,
		optPeak: d.optPeak,
	}
}

// TestDecoderScratchCeiling checks the pool rule of review W6.2 MAJ-1 one
// scratch at a time: a decoder whose scratch holds DecoderCeiling bytes goes
// back to the pool with it, and one byte's worth of elements more drops every
// scratch slice and index map, keeping the traversal's options. Each case
// grows one slice, or one index map with the slice that bounds it, to the
// ceiling by its own arithmetic, from unsafe.Sizeof and the 64 bytes an index
// entry is charged (mapEntryBytes), not from scratchBytes: a term that
// scratchBytes leaves out, or an off-by-one at the ceiling, fails its case.
func TestDecoderScratchCeiling(t *testing.T) {
	const ceiling = DecoderCeiling
	if ceiling != 4<<20 {
		t.Fatalf("DecoderCeiling = %d, want 4 MiB (docs/perf/frozen-budgets.md, AC-P5)", ceiling)
	}
	// fill gives d a scratch of n elements in one place.
	type fill struct {
		elem uintptr // bytes one element costs
		set  func(d *decoder, n int)
	}
	tests := map[string]fill{
		"set":     {unsafe.Sizeof(entry{}), func(d *decoder, n int) { d.v.set = make([]entry, 0, n) }},
		"probs":   {unsafe.Sizeof(probPair{}), func(d *decoder, n int) { d.v.probs = make([]probPair, 0, n) }},
		"legend":  {unsafe.Sizeof(legendPair{}), func(d *decoder, n int) { d.v.legend = make([]legendPair, 0, n) }},
		"folds":   {unsafe.Sizeof(fold{}), func(d *decoder, n int) { d.v.folds = make([]fold, 0, n) }},
		"cards":   {unsafe.Sizeof(wire.ModelCard{}), func(d *decoder, n int) { d.v.cards = make([]wire.ModelCard, 0, n) }},
		"nodes":   {unsafe.Sizeof(ast.Node{}), func(d *decoder, n int) { d.nodes = make([]ast.Node, 0, n) }},
		"raws":    {unsafe.Sizeof(""), func(d *decoder, n int) { d.raws = make([]string, 0, n) }},
		"rawBase": {unsafe.Sizeof(0), func(d *decoder, n int) { d.rawBase = make([]int, 0, n) }},
		"strs":    {unsafe.Sizeof((*string)(nil)), func(d *decoder, n int) { d.strs = make([]*string, 0, n) }},
		"jsons":   {unsafe.Sizeof(jsonMiss{}), func(d *decoder, n int) { d.jsons = make([]jsonMiss, 0, n) }},
		// An index map is charged for the entries the slice it indexes has
		// room for, on top of that slice.
		"setIdx over set": {unsafe.Sizeof(entry{}) + 64, func(d *decoder, n int) {
			d.v.set, d.v.setIdx = make([]entry, 0, n), map[string]int{}
		}},
		"lvlIdx over legend": {unsafe.Sizeof(legendPair{}) + 64, func(d *decoder, n int) {
			d.v.legend, d.v.lvlIdx = make([]legendPair, 0, n), map[uint32]int{}
		}},
		"lvlIdx over probs": {unsafe.Sizeof(probPair{}) + 64, func(d *decoder, n int) {
			d.v.probs, d.v.lvlIdx = make([]probPair, 0, n), map[uint32]int{}
		}},
		"strIdx over probs": {unsafe.Sizeof(probPair{}) + 64, func(d *decoder, n int) {
			d.v.probs, d.v.strIdx = make([]probPair, 0, n), map[string]int{}
		}},
		"strIdx over legend": {unsafe.Sizeof(legendPair{}) + 64, func(d *decoder, n int) {
			d.v.legend, d.v.strIdx = make([]legendPair, 0, n), map[string]int{}
		}},
		"optIdx by its peak": {64, func(d *decoder, n int) { d.optIdx, d.optPeak = map[string]int{}, n }},
		// The option index as a decode fills it, so that its peak is
		// counted where the index is built.
		"optIdx through optionIndex": {64, func(d *decoder, n int) {
			opts := make([]string, n)
			for i := range opts {
				opts[i] = strconv.Itoa(i)
			}
			d.optionIndex(opts)
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			at := ceiling / int(tt.elem) // the most elements within the ceiling
			for _, n := range []int{at, at + 1} {
				d := newDecoder()
				tt.set(d, n)
				before := capsOf(d)
				d.release()
				after := capsOf(d)
				bytes := n * int(tt.elem)
				switch {
				case bytes <= ceiling && after != before:
					t.Errorf("%d elements (%d bytes, within the %d-byte ceiling): release changed the scratch from %+v to %+v, want it kept", n, bytes, ceiling, before, after)
				case bytes > ceiling && after != (scratchCaps{}):
					t.Errorf("%d elements (%d bytes, past the %d-byte ceiling): release left %+v, want every scratch slice and index map dropped", n, bytes, ceiling, after)
				}
				if !d.opts.OnlyNumber {
					t.Errorf("%d elements: release lost the traversal's options %+v", n, d.opts)
				}
			}
		})
	}
}

// TestDecoderKeepsFixtureScratch checks that the ceiling spares every
// decode the budgets pin (AC-P2, AC-P8): after decoding any fixture of
// testdata, as a System One body and as a models body, release keeps the
// decoder's scratch, so the next decode of that shape starts warm and the
// pinned allocation counts cannot move. Each line logs the fixture's scratch
// as scratchBytes bounds it. The largest must be structured-legend-flood-10k's
// (2 172 168 bytes at W6-secfix, the figure DecoderCeiling's derivation
// quotes) and leave the ceiling at least 1.5 times its size, so that a
// change in the decoder's scratch or in the runtime's growth that eats the
// headroom fails here before the ceiling starts dropping a pinned decode.
func TestDecoderKeepsFixtureScratch(t *testing.T) {
	largest, largestName := 0, ""
	for _, name := range testsupport.FixtureNames(t, "*.json") {
		body := testsupport.Fixture(t, name)
		d := newDecoder()
		var res wire.SystemOneResult
		_, _ = d.systemOne(body, nil, "", &res, nil)
		_ = d.models(body, &wire.ModelList{})
		n, before := d.scratchBytes(), capsOf(d)
		d.release()
		if after := capsOf(d); after != before {
			t.Errorf("%s: scratch %d bytes, release changed it from %+v to %+v; a fixture's decoder must stay warm", name, n, before, after)
		}
		if n > largest {
			largest, largestName = n, name
		}
		if n >= 100<<10 {
			t.Logf("SCRATCH %-34s %8d bytes", strings.TrimSuffix(name, ".json"), n)
		}
	}
	t.Logf("SCRATCH largest %s: %d bytes, %.2f of DecoderCeiling (%d)", largestName, largest, float64(largest)/DecoderCeiling, DecoderCeiling)
	if largestName != "structured-legend-flood-10k.json" {
		t.Errorf("the largest fixture scratch is %s's, want structured-legend-flood-10k.json's, the fixture DecoderCeiling's derivation names", largestName)
	}
	if 3*largest > 2*DecoderCeiling {
		t.Errorf("the largest fixture scratch, %d bytes, leaves DecoderCeiling (%d) less than 1.5 times its size: revisit the ceiling and its derivation (frozen-budgets.md, AC-P5) together", largest, DecoderCeiling)
	}
}

// TestDecoderPoolRetention is review W6.2 MAJ-1's pin: one decode of a
// 15 MiB body of unknown answers inside the response cap, then ordinary
// decodes with a collection after each, all on one P (GOMAXPROCS 1, so every
// decode takes the same pool slot, the case in which the pool kept the
// hostile scratch: about 233 MiB before the ceiling). The live heap after
// each collection, against the heap before the hostile decode, must stay
// within DecoderCeiling + 64 KiB, the fixed allowance for the runtime's and
// the test's own small allocations; and the pooled decoder's scratch within
// the ceiling. The control proves the pool is live in this run: a flood whose
// scratch is under the ceiling (8000 answers) leaves its decoder in the pool
// with that scratch, which the same bound still holds.
//
// Pool hits are the premise, and under -race sync.Pool.Put drops one value in
// four, so the test skips there; the non-race CI steps run it.
func TestDecoderPoolRetention(t *testing.T) {
	if raceEnabled() {
		t.Skip("sync.Pool.Put drops values under -race; the pool's retention is measured without it")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	const bound = DecoderCeiling + 64<<10
	flood, answers := testsupport.UnknownAnswerFlood(15 << 20)
	under, underAnswers := testsupport.UnknownAnswerFlood(8000 * 21)
	small := testsupport.Fixture(t, "result.json")
	decode := func(body []byte) {
		t.Helper()
		var res wire.SystemOneResult
		if _, err := DecodeSystemOne(body, nil, "m", &res); err != nil {
			t.Fatalf("decode of a %d-byte body: %v", len(body), err)
		}
	}
	heap := func() int64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return int64(m.HeapAlloc) //nolint:gosec // G115: a heap size, far below 2^63.
	}
	pooled := func() int {
		d := decoders.Get().(*decoder)
		defer decoders.Put(d)
		return d.scratchBytes()
	}

	// Two collections empty the pool (it keeps a value through one), so that
	// no scratch an earlier test or run left pooled is in the base; the
	// ordinary decode then leaves a warm decoder in the P's pool slot.
	runtime.GC()
	runtime.GC()
	decode(small)
	base := heap()
	decode(flood)
	t.Logf("POOL hostile: %d-byte body, %d answers; pooled scratch after it %d bytes", len(flood), answers, pooled())
	for i := range 6 {
		decode(small)
		retained := heap() - base
		t.Logf("POOL after the hostile decode, ordinary decode + GC #%d: live heap %+d bytes (bound %d)", i+1, retained, bound)
		if retained > bound {
			t.Errorf("ordinary decode + GC #%d: %d bytes stay live after a hostile decode, want at most DecoderCeiling + 64 KiB = %d: the pool kept its scratch", i+1, retained, bound)
		}
	}
	if n := pooled(); n > DecoderCeiling {
		t.Errorf("the pooled decoder holds %d bytes of scratch, past DecoderCeiling %d", n, DecoderCeiling)
	}

	// The control: a scratch under the ceiling stays pooled and counted.
	decode(under)
	kept := pooled()
	retained := heap() - base
	t.Logf("POOL control: %d-byte body, %d answers; pooled scratch %d bytes, live heap %+d bytes", len(under), underAnswers, kept, retained)
	if kept <= DecoderCeiling/2 || kept > DecoderCeiling {
		t.Errorf("after a decode whose scratch is under the ceiling the pooled decoder holds %d bytes, want (%d, %d]: the pool is not in play, so the pin above proves nothing", kept, DecoderCeiling/2, DecoderCeiling)
	}
	if retained > bound {
		t.Errorf("the control keeps %d bytes live, past the bound %d", retained, bound)
	}
	runtime.KeepAlive(flood)
	runtime.KeepAlive(under)
}
