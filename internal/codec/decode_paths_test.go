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
	"fmt"
	"math/rand/v2"
	"strconv"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// The values a generated body draws from. Every string is valid UTF-8; the
// generator escapes what JSON requires and, by choice, anything else.
var (
	pathNumbers = [...]string{"0", "-0", "-0.0", "1", "7", "0.5", "-2.25", "1e3", "1E-2", "12.5e+1", "0.000001", "123456789"}
	pathCounts  = [...]string{"0", "12", "-0", "18446744073709551615"}
	pathNames   = [...]string{"s", "t", "c", "n", "u", "é", "😀", `q"/\`}
	pathLabels  = [...]string{"a", "b", "a b", "é", "😀", ""}
	pathTexts   = [...]string{"", "bad", "ok", "great", "é", "😀", "line\nbreak", `{"not":"json"}`}
	pathModels  = [...]string{"jev-latest", "m", "", "jév", "😀"}
	pathUnknown = [...]string{"aurora", "Score", "", "noul "}
	pathStrings = [...]string{"", "a", "score", "é", "😀", `x"y`, "a/b", "\n", "\x01"}
	// pathDecoyKeys are keys that equal, or nearly equal, a key the decoder
	// reads, for members at a depth or spelling where it must ignore them.
	pathDecoyKeys = [...]string{"answers", "legend", "type", "0", "score", "Answers", "legend ", "answers\x00", "model", "meta"}
	// pathRootDecoys are root keys the decoder does not read.
	pathRootDecoys = [...]string{"Answers", "legend ", "answers\x00", "model ", "usage\x00", "meta"}
)

// pathGen writes one System One body from a fuzz input and records how the
// Python SDK reads it: at every level the last of a repeated member wins,
// and a repeated answer name, legend level or label keeps the position of
// its first appearance. Every choice takes the next input byte, or 0 once
// the input is used up, so any input is a program and every program ends;
// choice 0 is the one the differential is about (a score answer, a
// structured level), so a short input still exercises it.
type pathGen struct {
	in  []byte
	buf []byte
}

// pathWant is the reading the decode must give.
type pathWant struct {
	model      string
	usage      wire.Usage
	set        []pathEntry // the last "answers" member, a Python dict
	structured int         // structured levels of the known answers
}

// pathEntry is one answer name of pathWant: a known answer, or an unknown
// type (kind KindUnknown) that the decode skips and reports.
type pathEntry struct {
	name string
	typ  string
	ans  wire.Answer
}

// put stores an answer under its name: a repeated name keeps its first
// position and takes the last value.
func (w *pathWant) put(e pathEntry) {
	for i := range w.set {
		if w.set[i].name == e.name {
			w.set[i] = e
			return
		}
	}
	w.set = append(w.set, e)
}

// pick returns a choice in [0, k).
func (g *pathGen) pick(k int) int {
	if len(g.in) == 0 {
		return 0
	}
	c := g.in[0]
	g.in = g.in[1:]
	return int(c) % k
}

func (g *pathGen) raw(s string) { g.buf = append(g.buf, s...) }

// ws writes JSON whitespace between two tokens, or none.
func (g *pathGen) ws() {
	g.raw([...]string{"", "", "", "", " ", "\t", "\n  ", "\r\n\t "}[g.pick(8)])
}

// str writes s as a JSON string. One choice per string decides the
// escapes: only those JSON requires (a quote, a backslash, a control
// character), every character, or each character by a choice of its own;
// an escaped character is \uXXXX (a surrogate pair past the Basic
// Multilingual Plane), or \/ for '/'.
func (g *pathGen) str(s string) {
	mode := g.pick(4)
	g.raw(`"`)
	for _, r := range s {
		esc := mode == 2 || (mode == 3 && g.pick(2) == 1)
		switch {
		case (r == '"' || r == '\\') && !esc:
			g.buf = utf8.AppendRune(append(g.buf, '\\'), r)
		case r == '\n' && !esc:
			g.raw(`\n`)
		case r == '/' && esc && mode == 3:
			g.raw(`\/`)
		case r < 0x20 || r == '"' || r == '\\' || esc:
			g.uesc(r)
		default:
			g.buf = utf8.AppendRune(g.buf, r)
		}
	}
	g.raw(`"`)
}

// uesc writes r as \uXXXX, or as a surrogate pair, in either case of hex.
func (g *pathGen) uesc(r rune) {
	format := `\u%04x`
	if r%2 == 1 {
		format = `\u%04X`
	}
	if r >= 0x10000 {
		hi, lo := utf16.EncodeRune(r)
		g.buf = fmt.Appendf(g.buf, format+format, hi, lo)
		return
	}
	g.buf = fmt.Appendf(g.buf, format, r)
}

// object writes an object with one member per function; each writes its
// member's value after the key.
func (g *pathGen) object(keys []string, values []func()) {
	g.raw("{")
	g.ws()
	for i, key := range keys {
		if i > 0 {
			g.raw(",")
			g.ws()
		}
		g.str(key)
		g.ws()
		g.raw(":")
		g.ws()
		values[i]()
		g.ws()
	}
	g.raw("}")
}

// num writes a number and returns its value as the decoder reads it.
func (g *pathGen) num() float64 {
	text := pathNumbers[g.pick(len(pathNumbers))]
	g.raw(text)
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		panic(err) // unreachable: every pathNumbers entry parses
	}
	return v
}

// junk writes any JSON value: a literal, a number, a string, or an object
// or array of junk whose keys are decoys, at most three containers deep.
func (g *pathGen) junk(depth int) {
	switch k := g.pick(8); {
	case k == 0:
		g.raw("null")
	case k == 1:
		g.raw([...]string{"true", "false"}[g.pick(2)])
	case k == 2:
		g.num()
	case k == 3 || depth >= 3:
		g.str(pathStrings[g.pick(len(pathStrings))])
	default:
		g.container(depth + 1)
	}
}

// container writes an object or an array of junk.
func (g *pathGen) container(depth int) {
	n := g.pick(4)
	if g.pick(2) == 0 {
		keys := make([]string, n)
		values := make([]func(), n)
		for i := range n {
			keys[i] = pathDecoyKeys[g.pick(len(pathDecoyKeys))]
			values[i] = func() { g.junk(depth) }
		}
		g.object(keys, values)
		return
	}
	g.raw("[")
	g.ws()
	for i := range n {
		if i > 0 {
			g.raw(",")
			g.ws()
		}
		g.junk(depth)
		g.ws()
	}
	g.raw("]")
}

// structured writes a legend level's structured value and returns its
// bytes as the body holds them.
func (g *pathGen) structured() []byte {
	start := len(g.buf)
	g.container(1)
	return []byte(string(g.buf[start:]))
}

// lvl returns a level from 0 to 3.
func (g *pathGen) lvl() uint32 { return [...]uint32{0, 1, 2, 3}[g.pick(4)] }

// level returns a spelling of level l: "1", "01", "+1" or "+01".
func (g *pathGen) level(l uint32) string {
	return [...]string{"", "0", "+", "+0"}[g.pick(4)] + strconv.FormatUint(uint64(l), 10)
}

// genMember is one member of an object the generator writes: the key, the
// function that writes the value the reading keeps, and, for a member whose
// earlier copies may be a valid value of its kind too, the function that
// writes one without changing the reading.
type genMember struct {
	key   string
	value func()
	alt   func()
}

// members writes an object of ms, and shuffles them first, inserts an
// earlier copy of some member (junk, or another valid value of its kind),
// and inserts decoys, so that every member the reading keeps is the last of
// its key.
func (g *pathGen) members(ms []genMember, decoys []string) {
	for i := len(ms) - 1; i > 0; i-- {
		j := g.pick(i + 1)
		ms[i], ms[j] = ms[j], ms[i]
	}
	var seq []genMember
	insert := func(m genMember) {
		at := g.pick(len(seq) + 1)
		seq = append(seq[:at], append([]genMember{m}, seq[at:]...)...)
	}
	for _, m := range ms {
		switch g.pick(4) {
		case 1:
			insert(genMember{key: m.key, value: func() { g.junk(1) }})
		case 2, 3:
			if m.alt != nil {
				insert(genMember{key: m.key, value: m.alt})
			}
		}
		seq = append(seq, m)
	}
	for range g.pick(3) {
		insert(genMember{key: decoys[g.pick(len(decoys))], value: func() { g.junk(1) }})
	}
	keys := make([]string, len(seq))
	values := make([]func(), len(seq))
	for i, m := range seq {
		keys[i], values[i] = m.key, m.value
	}
	g.object(keys, values)
}

// body writes a whole body and returns its reading: a few root members, of
// which the last "model", "usage" and "answers" are valid and any earlier
// one may be of the wrong kind, or, for "answers", a whole other answer
// set.
func (g *pathGen) body() *pathWant {
	want := new(pathWant)
	var keys []string
	var values []func()
	add := func(key string, value func()) {
		keys = append(keys, key)
		values = append(values, value)
	}
	lastValid := map[string]bool{}
	for range 1 + g.pick(6) {
		switch g.pick(8) {
		case 0:
			add("model", func() { want.model = g.model() })
			lastValid["model"] = true
		case 1:
			add("model", func() { g.nonString() })
			lastValid["model"] = false
		case 2:
			add("usage", func() { want.usage = g.usage() })
			lastValid["usage"] = true
		case 3:
			add("usage", func() { g.nonObject() })
			lastValid["usage"] = false
		case 4, 5:
			add("answers", func() { g.answers(want) })
			lastValid["answers"] = true
		case 6:
			add("answers", func() { g.nonObject() })
			lastValid["answers"] = false
		default:
			add(pathRootDecoys[g.pick(len(pathRootDecoys))], func() { g.junk(0) })
		}
	}
	if !lastValid["model"] {
		add("model", func() { want.model = g.model() })
	}
	if !lastValid["usage"] {
		add("usage", func() { want.usage = g.usage() })
	}
	if !lastValid["answers"] {
		add("answers", func() { g.answers(want) })
	}
	g.ws()
	g.object(keys, values)
	g.ws()
	return want
}

// nonString writes a value that is not a string.
func (g *pathGen) nonString() {
	switch g.pick(4) {
	case 0:
		g.raw("null")
	case 1:
		g.num()
	default:
		g.container(1)
	}
}

// nonObject writes a value that is not an object.
func (g *pathGen) nonObject() {
	switch g.pick(4) {
	case 0:
		g.raw("null")
	case 1:
		g.num()
	case 2:
		g.str(pathStrings[g.pick(len(pathStrings))])
	default:
		g.raw("[")
		g.junk(1)
		g.raw("]")
	}
}

// model writes a model name and returns it.
func (g *pathGen) model() string {
	m := pathModels[g.pick(len(pathModels))]
	g.str(m)
	return m
}

// usage writes a usage object and returns its reading: each count the last
// of its key, a number or null (absent).
func (g *pathGen) usage() wire.Usage {
	var u wire.Usage
	var ms []genMember
	for _, key := range [...]string{"input_tokens", "output_tokens"} {
		if g.pick(3) == 0 {
			continue
		}
		ms = append(ms, genMember{key: key, value: func() {
			if g.pick(4) == 0 {
				g.raw("null")
				return
			}
			text := pathCounts[g.pick(len(pathCounts))]
			g.raw(text)
			n, err := strconv.ParseUint(text, 10, 64)
			if text == "-0" {
				n, err = 0, nil
			}
			if err != nil {
				panic(err) // unreachable: every pathCounts entry is a count
			}
			if key == "input_tokens" {
				u.InputTokens, u.HasInputTokens = n, true
			} else {
				u.OutputTokens, u.HasOutputTokens = n, true
			}
		}})
	}
	g.members(ms, []string{"billing_units", "reasoning_tokens", "input_tokens "})
	return u
}

// answers writes an answers object and makes it the reading's answer set:
// each name's last value valid, an earlier one of any kind.
func (g *pathGen) answers(want *pathWant) {
	want.set = want.set[:0]
	n := 1 + g.pick(6)
	names := make([]string, n)
	last := map[string]int{}
	for i := range names {
		names[i] = pathNames[g.pick(len(pathNames))]
		last[names[i]] = i
	}
	values := make([]func(), n)
	for i, name := range names {
		if i != last[name] && g.pick(2) == 1 {
			values[i] = func() {
				want.put(pathEntry{name: name}) // its position; the last value replaces it
				g.junk(1)
			}
			continue
		}
		values[i] = func() { want.put(g.answer(name)) }
	}
	g.object(names, values)
	want.structured = 0
	for _, e := range want.set {
		for _, l := range e.ans.Score.Legend {
			if l.Description.JSON != nil {
				want.structured++
			}
		}
	}
}

// answer writes a valid answer called name and returns its reading.
func (g *pathGen) answer(name string) pathEntry {
	e := pathEntry{name: name}
	kind := [...]wire.Kind{wire.KindScore, wire.KindChoice, wire.KindScore, wire.KindNoul, wire.KindUnknown, wire.KindScore}[g.pick(6)]
	typ := kind.String()
	if kind == wire.KindUnknown {
		typ = pathUnknown[g.pick(len(pathUnknown))]
	}
	e.typ, e.ans.Kind = typ, kind
	num := func() { g.num() }
	otherType := func() { g.str([...]string{"score", "choice", "noul", "aurora"}[g.pick(4)]) }
	ms := []genMember{{key: "type", value: func() { g.str(typ) }, alt: otherType}}
	a := &e.ans
	switch kind {
	case wire.KindUnknown:
		// Members of a known kind, a structured legend among them: the
		// decode ignores them, and so must the lazy pass.
		ms = append(ms,
			genMember{key: "legend", value: func() { g.legend() }},
			genMember{key: "score", value: num},
			genMember{key: "probabilities", value: func() { g.junk(1) }},
		)
	case wire.KindNoul:
		ms = append(ms, genMember{key: "noul", value: func() { a.Noul.Noul = g.num() }, alt: num})
	case wire.KindChoice:
		ms = append(ms,
			genMember{key: "choice", value: func() {
				a.Choice.Choice = pathLabels[g.pick(len(pathLabels))]
				g.str(a.Choice.Choice)
			}, alt: func() { g.str(pathLabels[g.pick(len(pathLabels))]) }},
			genMember{key: "confidence", value: func() { a.Choice.Confidence = g.num() }, alt: num},
			genMember{key: "probabilities", value: func() { a.Choice.Probabilities = g.labels() }, alt: func() { g.labels() }},
		)
	case wire.KindScore:
		ms = append(ms,
			genMember{key: "score", value: func() { a.Score.Score = g.num() }, alt: num},
			genMember{key: "confidence", value: func() { a.Score.Confidence = g.num() }, alt: num},
			genMember{key: "legend", value: func() { a.Score.Legend = g.legend() }, alt: func() { g.legend() }},
			genMember{key: "probabilities", value: func() { a.Score.Probabilities = g.levels() }, alt: func() { g.levels() }},
		)
	}
	g.members(ms, []string{"explanation", "legend ", "Legend", "meta", "answers"})
	if kind == wire.KindUnknown {
		e.ans = wire.Answer{}
	}
	return e
}

// legend writes a legend and returns its reading: one entry per level, at
// its first spelling's position, with the value of its last spelling, text
// or the exact bytes of a structured value.
func (g *pathGen) legend() []wire.LegendEntry {
	out := make([]wire.LegendEntry, 0)
	n := 1 + g.pick(6)
	keys := make([]string, n)
	values := make([]func(), n)
	for i := range n {
		lvl := g.lvl()
		keys[i] = g.level(lvl)
		values[i] = func() {
			var desc wire.Content
			if g.pick(3) == 2 {
				desc.Text = pathTexts[g.pick(len(pathTexts))]
				g.str(desc.Text)
			} else {
				desc.JSON = g.structured()
			}
			for j := range out {
				if out[j].Level == lvl {
					out[j].Description = desc
					return
				}
			}
			out = append(out, wire.LegendEntry{Level: lvl, Description: desc})
		}
	}
	g.object(keys, values)
	return out
}

// levels writes a score's probabilities and returns their reading.
func (g *pathGen) levels() []wire.LevelProbability {
	out := make([]wire.LevelProbability, 0)
	n := g.pick(6)
	keys := make([]string, n)
	values := make([]func(), n)
	for i := range n {
		lvl := g.lvl()
		keys[i] = g.level(lvl)
		values[i] = func() {
			p := g.num()
			for j := range out {
				if out[j].Level == lvl {
					out[j].Probability = p
					return
				}
			}
			out = append(out, wire.LevelProbability{Level: lvl, Probability: p})
		}
	}
	g.object(keys, values)
	return out
}

// labels writes a choice's probabilities and returns their reading.
func (g *pathGen) labels() []wire.LabelProbability {
	out := make([]wire.LabelProbability, 0)
	n := g.pick(6)
	keys := make([]string, n)
	values := make([]func(), n)
	for i := range n {
		label := pathLabels[g.pick(len(pathLabels))]
		keys[i] = label
		values[i] = func() {
			p := g.num()
			for j := range out {
				if out[j].Label == label {
					out[j].Probability = p
					return
				}
			}
			out = append(out, wire.LabelProbability{Label: label, Probability: p})
		}
	}
	g.object(keys, values)
	return out
}

// FuzzDecodePaths is the differential between the decoder's two passes over
// the bodies they disagree on most easily: each input is a program for
// pathGen, which writes a body with repeated members at every level (root
// members, answer names, answer members, legends, levels in several
// spellings, labels, counts), earlier copies of any kind or a whole other
// answer set, escaped spellings of every key, decoys of the keys the passes
// read at depths they must ignore, and structured levels with whitespace
// and escapes inside. The visitor decides which member is the last of each
// key and marks a structured level; the lazy pass finds that level's bytes
// by walking the body again, last wins throughout. The decode must equal
// pathGen's reading: the same last-wins choice everywhere (answers, their
// order, every value), every structured level byte for byte as the body
// holds it, the same skipped answers (unknown types) in order, and one lazy
// pass exactly when a known answer has a structured level. A decode against
// the question set the result answers must give the same answers. Each
// input runs within the per-input bound.
//
// The 66 seed programs run as a test in CI's -race test step (go test
// -race with coverage) on ubuntu-26.04, xcode-27 and windows-2025, and the
// fuzz job fuzzes it for 60 s on ubuntu-26.04.
func FuzzDecodePaths(f *testing.F) {
	f.Add([]byte{})
	ramp := make([]byte, 256)
	for i := range ramp {
		ramp[i] = byte(i)
	}
	f.Add(ramp)
	src := rand.NewChaCha8([32]byte{'w', '6', '.', '1', 'p', 'a', 't', 'h', 's'})
	r := rand.New(src) //nolint:gosec // G404: seed programs for a fuzz target, not a secret.
	for range 64 {
		p := make([]byte, 256+r.IntN(1792))
		_, _ = src.Read(p) // ChaCha8.Read never fails
		f.Add(p)
	}
	f.Fuzz(func(t *testing.T, program []byte) {
		defer testsupport.BoundFuzzInput(t)()
		g := &pathGen{in: program}
		want := g.body()
		body := g.buf
		res, skipped, st, err := decodeBody(t, body, nil, "")
		if err != nil {
			t.Fatalf("decode of the generated body %s: %v", body, err)
		}
		var wantEntries []wire.AnswerEntry // nil when no answer is known, as Entries is
		wantSkipped := []SkippedAnswer{}
		for _, e := range want.set {
			if e.ans.Kind == wire.KindUnknown {
				wantSkipped = append(wantSkipped, SkippedAnswer{Name: e.name, Type: e.typ})
				continue
			}
			wantEntries = append(wantEntries, wire.AnswerEntry{Name: e.name, Answer: e.ans})
		}
		if diff := gocmp.Diff(wantEntries, res.Answers.Entries()); diff != "" {
			t.Fatalf("answers of %s (-want +got):\n%s", body, diff)
		}
		if res.Model != want.model || res.Usage != want.usage {
			t.Fatalf("model and usage of %s = %q %+v, want %q %+v", body, res.Model, res.Usage, want.model, want.usage)
		}
		if diff := gocmp.Diff(wantSkipped[:min(len(wantSkipped), MaxSkipped)], skipped.Named()); diff != "" || skipped.Count != len(wantSkipped) {
			t.Fatalf("skipped of %s: count %d, want %d (-want +got):\n%s", body, skipped.Count, len(wantSkipped), diff)
		}
		if lazy := min(want.structured, 1); st.lazyPasses != lazy {
			t.Fatalf("%s: %d lazy passes for %d structured levels, want %d", body, st.lazyPasses, want.structured, lazy)
		}
		interned, _, _, err := decodeBody(t, body, questionsFor(t, res), res.Model)
		if err != nil {
			t.Fatalf("decode of %s against its questions: %v", body, err)
		}
		if diff := gocmp.Diff(wantEntries, interned.Answers.Entries()); diff != "" {
			t.Fatalf("answers of %s against its questions (-want +got):\n%s", body, diff)
		}
	})
}
