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
	"errors"
	"strconv"
	"sync"

	"github.com/bytedance/sonic/ast"
	sonicdecoder "github.com/bytedance/sonic/decoder"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// The reasons a response body is refused. None of them quotes the body.
var (
	errNotJSON       = errors.New("not a JSON value")
	errTrailing      = errors.New("data after the top-level value")
	errRootNotObject = errors.New("the response is not a JSON object")
	errInvalidUTF8   = errors.New("invalid UTF-8 in a string")
	errRawControl    = errors.New("raw control character in a string")
	errDepth         = errors.New("nested too deep")
	errLazyMissed    = errors.New("a structured legend level was not found by the second pass")
	errMissing       = errors.New("missing")
	errNotString     = errors.New("not a string")
	errNotNumber     = errors.New("not a finite number")
	errNotObject     = errors.New("not an object")
	errNotArray      = errors.New("not an array")
	errNotCount      = errors.New("not an integer from 0 to 2^64-1")
	errNotLevel      = errors.New("not a level: want [+]digits up to 2^32-1")
	errLegendValue   = errors.New("not a string, an object or an array")
)

// FieldPath locates a failure in a response body the way the Python SDK's
// field_path names it: dotted member names, a bracketed index for a model
// card, and the root for a failure of the JSON layer. Name and Key are text
// the server chose; whoever prints them escapes and cuts them.
type FieldPath struct {
	// Top is the top-level member: "model", "usage", "answers" or "models".
	// It is empty for a failure of the JSON layer, whose path is the root.
	Top string
	// Index is the position of a model card in "models", when HasIndex is
	// set.
	Index int
	// HasIndex reports whether the path goes through a model card.
	HasIndex bool
	// Name is the answer's name, when HasName is set. A name may be empty.
	Name string
	// HasName reports whether the path goes through an answer.
	HasName bool
	// Member is the member inside the answer, the usage or the model card,
	// or empty.
	Member string
	// Key is the probability or legend key, when HasKey is set.
	Key string
	// HasKey reports whether the path ends at a probability or legend key.
	HasKey bool
}

// String returns the path as the Python SDK spells field_path, except that
// the root is "." where Python writes the empty string (Appendix B): for
// example "answers.tone.confidence", "models[1].name" or ".". Name and Key
// are written as they are.
func (p FieldPath) String() string {
	if p.Top == "" {
		return "."
	}
	b := make([]byte, 0, len(p.Top)+len(p.Name)+len(p.Member)+len(p.Key)+16)
	b = append(b, p.Top...)
	if p.HasIndex {
		b = append(b, '[')
		b = strconv.AppendInt(b, int64(p.Index), 10)
		b = append(b, ']')
	}
	if p.HasName {
		b = append(b, '.')
		b = append(b, p.Name...)
	}
	if p.Member != "" {
		b = append(b, '.')
		b = append(b, p.Member...)
	}
	if p.HasKey {
		b = append(b, '.')
		b = append(b, p.Key...)
	}
	return string(b)
}

// DecodeError reports a response body the decoder refused: Path is where,
// Err is why. Err is one of the decoder's reasons or, for a body that is not
// JSON, sonic's error; Error never prints the body or Path's server-chosen
// names.
type DecodeError struct {
	// Path is where the body failed.
	Path FieldPath
	// Err is the reason.
	Err error
}

// Error returns a sentence naming the reason: "invalid JSON: …" for a failure
// of the JSON layer, "invalid response data: …" for a member. sonic's
// traversal reports a syntax error by a fixed description without a
// position; its lazy pass reports one with a position and a quotation of the
// body, of which only the position is kept.
func (e *DecodeError) Error() string {
	reason := e.Err.Error()
	if syntax, ok := errors.AsType[ast.SyntaxError](e.Err); ok {
		reason = "syntax error at byte " + strconv.Itoa(syntax.Pos) + ": " + syntax.Message()
	}
	if e.Path.Top == "" {
		return "invalid JSON: " + reason
	}
	return "invalid response data: " + reason
}

// Unwrap returns the reason.
func (e *DecodeError) Unwrap() error { return e.Err }

// error returns p as a *DecodeError.
func (p pend) error() *DecodeError { return &DecodeError{Path: p.path, Err: p.err} }

// jsonErr returns the failure of the JSON layer, at the root.
func jsonErr(err error) *DecodeError { return &DecodeError{Err: err} }

// MaxSkipped is the number of skipped answers a decode reports by name: the
// SDK logs one WARN line for each, and one line counting the rest.
const MaxSkipped = 8

// SkippedAnswer is an answer of a type this version of the SDK does not
// model, which the decoder dropped.
type SkippedAnswer struct {
	// Name is the answer's name.
	Name string
	// Type is its type.
	Type string
}

// Skipped lists the answers a decode dropped because their type is one this
// version does not model, in the order their names first appear, as the
// Python SDK logs them: every such answer of the last "answers" member
// before the first answer that is not an object or has no string type, even
// when the decode then fails. The names and types alias the body.
type Skipped struct {
	// First holds the first min(Count, MaxSkipped) skipped answers.
	First [MaxSkipped]SkippedAnswer
	// Count is the number of skipped answers.
	Count int
}

// Named returns the skipped answers reported by name.
func (s *Skipped) Named() []SkippedAnswer { return s.First[:min(s.Count, MaxSkipped)] }

func (s *Skipped) add(name, typ string) {
	if s.Count < MaxSkipped {
		s.First[s.Count] = SkippedAnswer{Name: name, Type: typ}
	}
	s.Count++
}

// stats describes the work of the last decode, for the tests.
type stats struct {
	scans      int    // whole-body raw-control scans (0 or 1)
	lazyPasses int    // lazy passes (0 or 1)
	members    uint64 // members the lazy pass iterated (AC-P8's "members visited")
	arena      int    // bytes copied into the arena
}

// decoder holds a decode's scratch. It is pooled: a warm decoder decodes a
// body of a shape it has seen without growing any scratch.
type decoder struct {
	v       visitor
	opts    ast.VisitorOptions
	nodes   []ast.Node
	raws    []string
	rawBase []int
	strs    []*string
	jsons   []jsonMiss
	optIdx  map[string]int
	stats   stats
}

// jsonMiss is a structured legend level that no level of the question set
// equals: where its bytes go, and the raw bytes the lazy pass found.
type jsonMiss struct {
	dst *[]byte
	raw string
}

func newDecoder() *decoder {
	return &decoder{opts: ast.VisitorOptions{OnlyNumber: true}}
}

var decoders = sync.Pool{New: func() any { return newDecoder() }}

// release drops every reference the decoder holds into the last body, the
// last result and the last question set, so the pool keeps none of them
// alive.
func (d *decoder) release() {
	d.v.release()
	clear(d.optIdx)
	clear(d.nodes)
	clear(d.raws)
	d.raws = d.raws[:0]
	clear(d.strs)
	d.strs = d.strs[:0]
	clear(d.jsons)
	d.jsons = d.jsons[:0]
}

// DecodeSystemOne decodes the body of a successful System One response into
// *dst, which it overwrites. q is the question set the request asked, and
// model the model it named; either may be nil or empty.
//
// The body is traversed once by sonic's ast.Preorder, which validates every
// token, including those of members the SDK does not read; every string and
// key must be valid UTF-8 without a raw control character (R23); only JSON
// whitespace may follow the top-level object. Unknown members are ignored.
// A repeated member takes its last value at every level, and a repeated
// answer name keeps the position of its first appearance (a Python dict). An
// answer whose type this version does not model is dropped and reported in
// the returned Skipped. When an answer's legend holds a JSON object or
// array, one lazy second pass reads its exact bytes.
//
// The result never aliases body: every string of the answers, and the model,
// is the request's own when it equals one (a question name, an option label,
// a level's text or compact JSON, the model), and a copy otherwise, all
// copies of one decode sharing one allocation.
//
// It fails with a *DecodeError naming the first failure the Python SDK would
// report: a body that is not one JSON object at the root; then the first
// answer that is not an object or has no string type; then model, usage and
// answers in that order; then, in the order the answers' names first
// appear, the first member of the answer in schema order that is missing,
// of the wrong kind, or holds a bad key or value.
func DecodeSystemOne(body []byte, q *wire.Prepared, model string, dst *wire.SystemOneResult) (Skipped, error) {
	d := decoders.Get().(*decoder)
	skipped, err := d.systemOne(body, q, model, dst)
	d.release()
	decoders.Put(d)
	return skipped, err
}

// DecodeModels decodes the body of a successful list-models response into
// *dst, which it overwrites, with the checks of [DecodeSystemOne]. The
// result never aliases body: the cards' strings are copies sharing one
// allocation. It fails with a *DecodeError at "models", at "models[i]" or at
// "models[i].<member>", the first card first and its members in schema
// order.
func DecodeModels(body []byte, dst *wire.ModelList) error {
	d := decoders.Get().(*decoder)
	err := d.models(body, dst)
	d.release()
	decoders.Put(d)
	return err
}

// systemOne is DecodeSystemOne on d.
func (d *decoder) systemOne(body []byte, q *wire.Prepared, model string, dst *wire.SystemOneResult) (Skipped, error) {
	s := NoCopyString(body)
	d.stats = stats{}
	if err := d.traverse(s, body, modeSystemOne); err != nil {
		return Skipped{}, err
	}
	return d.finish(s, q, model, dst)
}

// models is DecodeModels on d.
func (d *decoder) models(body []byte, dst *wire.ModelList) error {
	s := NoCopyString(body)
	d.stats = stats{}
	if err := d.traverse(s, body, modeModels); err != nil {
		return err
	}
	v := &d.v
	switch {
	case !v.hasCards:
		return topPend("models", "", errMissing).error()
	case v.cardsErr.set:
		return v.cardsErr.error()
	}
	n := 0
	for i := range v.cards {
		c := &v.cards[i]
		n += len(c.Name) + len(c.Description) + len(c.ReleaseDate)
	}
	arena := make([]byte, 0, n)
	cards := make([]wire.ModelCard, len(v.cards))
	for i := range v.cards {
		c := &v.cards[i]
		cards[i] = wire.ModelCard{Name: arenaString(&arena, c.Name), Description: arenaString(&arena, c.Description), ReleaseDate: arenaString(&arena, c.ReleaseDate)}
	}
	d.stats.arena = len(arena)
	*dst = wire.ModelList{Models: cards}
	return nil
}

// traverse runs the visitor over s in mode m, then the trailing-data check.
func (d *decoder) traverse(s string, body []byte, m mode) error {
	d.v.reset(s, m)
	err := ast.Preorder(s, &d.v, &d.opts)
	d.stats.scans = d.v.scans
	if err != nil {
		return jsonErr(err)
	}
	return trailing(body)
}

// trailing is the trailing-data check: ast.Preorder stops after the root
// value, so sonic's decoder.Skip finds its end, and only JSON whitespace (space,
// tab, line feed, carriage return) may follow it. Skip returns a negative
// start for a body that holds no value at all.
func trailing(body []byte) error {
	start, end := sonicdecoder.Skip(body)
	if start < 0 || end > len(body) {
		return jsonErr(errNotJSON)
	}
	for _, c := range body[end:] {
		switch c {
		case ' ', '\t', '\n', '\r':
		default:
			return jsonErr(errTrailing)
		}
	}
	return nil
}

// finish reports the first failure in the Python SDK's order, runs the lazy
// pass when a legend level is structured, interns the strings and moves the
// answers into dst.
func (d *decoder) finish(src string, q *wire.Prepared, model string, dst *wire.SystemOneResult) (Skipped, error) {
	v := &d.v
	var skipped Skipped
	// The Python SDK's pre-pass over the answers: the first answer that is
	// not an object or has no string type fails before anything else is
	// validated, and every answer of an unknown type before it is logged.
	for i := range v.set {
		e := &v.set[i]
		if e.err.set && e.err.path.Member == "type" {
			return skipped, e.err.error()
		}
		if !e.err.set && e.ans.Kind == wire.KindUnknown {
			skipped.add(e.name, e.typ)
		}
	}
	switch {
	case !v.hasModel:
		return skipped, topPend("model", "", errMissing).error()
	case v.modelErr.set:
		return skipped, v.modelErr.error()
	case !v.hasUsage:
		return skipped, topPend("usage", "", errMissing).error()
	case v.usageErr.set:
		return skipped, v.usageErr.error()
	case v.inBad:
		return skipped, topPend("usage", "input_tokens", errNotCount).error()
	case v.outBad:
		return skipped, topPend("usage", "output_tokens", errNotCount).error()
	case v.answersErr.set:
		return skipped, v.answersErr.error()
	}
	known, structured := 0, 0
	for i := range v.set {
		e := &v.set[i]
		if e.err.set {
			return skipped, e.err.error()
		}
		if e.ans.Kind != wire.KindUnknown {
			known++
			structured += e.structured
		}
	}
	if structured > 0 {
		if err := d.lazy(src); err != nil {
			return skipped, err
		}
	}
	d.intern(q, model)
	*dst = wire.SystemOneResult{Model: v.model, Usage: v.usage}
	dst.Answers.Grow(known)
	for i := range v.set {
		if e := &v.set[i]; e.ans.Kind != wire.KindUnknown {
			dst.Answers.Put(e.name, e.ans)
		}
	}
	return skipped, nil
}

// intern makes every string of the known answers, their structured levels
// and the model the request's own when it is equal to one, and a copy
// otherwise (plan 6.2.5), so that the result never aliases the body: a name
// against the question set's names, a choice and its labels against the
// question's options, a text level against the level's text and a
// structured level's bytes against the level's compact JSON, the model
// against the model the request named. The copies share one arena, so the
// misses of one decode cost one allocation.
func (d *decoder) intern(q *wire.Prepared, model string) {
	v := &d.v
	n := 0
	miss := func(s *string) {
		if *s != "" {
			d.strs = append(d.strs, s)
			n += len(*s)
		}
	}
	if v.model == model {
		v.model = model
	} else {
		miss(&v.model)
	}
	for i := range v.set {
		e := &v.set[i]
		if e.ans.Kind == wire.KindUnknown {
			continue
		}
		var pq *wire.PreparedQuestion
		if q != nil {
			pq, _ = q.Lookup(e.name)
		}
		if pq != nil {
			e.name = pq.Name
		} else {
			miss(&e.name)
		}
		switch e.ans.Kind {
		case wire.KindChoice:
			var opts []string
			if pq != nil && pq.Kind == wire.KindChoice {
				opts = pq.Options
			}
			d.optionIndex(opts)
			c := &e.ans.Choice
			if s, ok := d.option(opts, c.Choice, -1); ok {
				c.Choice = s
			} else {
				miss(&c.Choice)
			}
			for j := range c.Probabilities {
				if s, ok := d.option(opts, c.Probabilities[j].Label, j); ok {
					c.Probabilities[j].Label = s
				} else {
					miss(&c.Probabilities[j].Label)
				}
			}
		case wire.KindScore:
			var levels []wire.Content
			if pq != nil && pq.Kind == wire.KindScore {
				levels = pq.Levels
			}
			legend := e.ans.Score.Legend
			for j := range legend {
				desc := &legend[j].Description
				var want wire.Content
				if lvl := legend[j].Level; uint64(lvl) < uint64(len(levels)) {
					want = levels[lvl]
				}
				if desc.JSON == nil {
					if want.JSON == nil && want.Text == desc.Text {
						desc.Text = want.Text
					} else {
						miss(&desc.Text)
					}
					continue
				}
				raw := d.raws[d.rawBase[i]+j]
				if want.JSON != nil && string(want.JSON) == raw {
					desc.JSON = want.JSON
					continue
				}
				d.jsons = append(d.jsons, jsonMiss{dst: &desc.JSON, raw: raw})
				n += len(raw)
			}
		}
	}
	if n == 0 {
		return
	}
	arena := make([]byte, 0, n)
	for _, s := range d.strs {
		*s = arenaString(&arena, *s)
	}
	for _, m := range d.jsons {
		start := len(arena)
		arena = append(arena, m.raw...)
		*m.dst = arena[start:len(arena):len(arena)]
	}
	d.stats.arena = len(arena)
}

// optionIndex readies the scratch index of opts when the list is long enough
// to hash.
func (d *decoder) optionIndex(opts []string) {
	if len(opts) <= linearFold {
		return
	}
	if d.optIdx == nil {
		d.optIdx = make(map[string]int, len(opts))
	}
	clear(d.optIdx)
	for i, o := range opts {
		if _, ok := d.optIdx[o]; !ok {
			d.optIdx[o] = i
		}
	}
}

// option returns the option of opts equal to s, and whether there is one.
// hint is the position s most likely has: the API lists the probabilities
// of a choice in the order of its options.
func (d *decoder) option(opts []string, s string, hint int) (string, bool) {
	if hint >= 0 && hint < len(opts) && opts[hint] == s {
		return opts[hint], true
	}
	if len(opts) > linearFold {
		if i, ok := d.optIdx[s]; ok {
			return opts[i], true
		}
		return "", false
	}
	for _, o := range opts {
		if o == s {
			return o, true
		}
	}
	return "", false
}

// arenaString appends s to *arena and returns the appended bytes as a
// string. The arena is sized for every copy up front, so it never grows,
// and nothing writes to the bytes afterwards.
func arenaString(arena *[]byte, s string) string {
	if s == "" {
		return ""
	}
	start := len(*arena)
	*arena = append(*arena, s...)
	return NoCopyString((*arena)[start:len(*arena):len(*arena)])
}
