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

package wire

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
)

// ErrDuplicateQuestion is returned by [NewPrepared] for a question set that
// names a question twice.
var ErrDuplicateQuestion = errors.New("wire: question name repeated in a prepared set")

// MaxLevelHint is the largest value [Prepared.LevelHint] takes.
//
// The hint sizes the lists a score answer's decoder starts with. The server
// decides how many levels an answer carries, so an unbounded hint would let a
// request with a long scale make every response reserve that much memory up
// front; 8 saves the one growth a list of 5 to 8 levels pays after starting at
// 4, and a longer list grows from 8 as it would without a hint.
const MaxLevelHint = 8

// PreparedQuestion is one question of a prepared set: its name and kind, and
// the tables the decoder interns response strings against.
type PreparedQuestion struct {
	// Name is the question name.
	Name string
	// Kind is the question's kind.
	Kind Kind
	// Options lists a choice question's option labels in the order they were
	// added. It is nil for the other kinds.
	Options []string
	// Levels lists a score question's level descriptions; a description's
	// index is its level. It is nil for the other kinds.
	Levels []Content
}

// Prepared is a question set serialised once and reused by every call that
// asks it.
//
// It is read-only once NewPrepared or [Builder.Finish] has made it, and safe
// for concurrent use. The
// entries sit behind [Prepared.Entries] so that the lookup index built over
// them cannot go stale.
type Prepared struct {
	// Questions is the compact JSON object that is the value of a request's
	// "questions" member, spliced into every request body as is.
	Questions []byte
	// LevelHint is the largest number of levels of any score question in the
	// set, capped at [MaxLevelHint], or 0 when the set has no score question
	// with a levels table. It is a sizing hint for the decoder, never sent.
	LevelHint int

	entries []PreparedQuestion
	index   map[string]int // nil when entries has at most linearLimit questions
}

// NewPrepared returns the prepared set of the questions whose serialised form
// is questions and whose tables are entries. It fails with
// [ErrDuplicateQuestion] when two entries share a name, whatever the size of
// the set (the root package rejects a repeated name before it gets here). The
// slices are kept, not copied: the caller must not modify them afterwards.
func NewPrepared(questions []byte, entries []PreparedQuestion) (*Prepared, error) {
	p := new(Prepared)
	if err := p.init(questions, entries); err != nil {
		return nil, err
	}
	return p, nil
}

// init makes p the prepared set of questions and entries, see NewPrepared.
// p is left unchanged when it fails.
func (p *Prepared) init(questions []byte, entries []PreparedQuestion) error {
	hint := 0
	for i := range entries {
		hint = max(hint, min(len(entries[i].Levels), MaxLevelHint))
	}
	var index map[string]int
	if len(entries) <= linearLimit {
		for i := range entries {
			for j := range i {
				if entries[j].Name == entries[i].Name {
					return fmt.Errorf("%w: %q", ErrDuplicateQuestion, entries[i].Name)
				}
			}
		}
	} else {
		index = make(map[string]int, len(entries))
		for i := range entries {
			if _, ok := index[entries[i].Name]; ok {
				return fmt.Errorf("%w: %q", ErrDuplicateQuestion, entries[i].Name)
			}
			index[entries[i].Name] = i
		}
	}
	*p = Prepared{Questions: questions, LevelHint: hint, entries: entries, index: index}
	return nil
}

// Entries returns the questions in the order they were added. The slice is
// shared with p: callers must not modify it.
func (p *Prepared) Entries() []PreparedQuestion { return p.entries }

// Lookup returns the question called name, and whether the set has one. The
// returned pointer refers into the set's own entries: it must not be used to
// modify them.
func (p *Prepared) Lookup(name string) (*PreparedQuestion, bool) {
	if p.index != nil {
		i, ok := p.index[name]
		if !ok {
			return nil, false
		}
		return &p.entries[i], true
	}
	for i := range p.entries {
		if p.entries[i].Name == name {
			return &p.entries[i], true
		}
	}
	return nil, false
}

// errTypeField reports a raw question whose fields name "type", which the
// serialiser writes from the question's type.
var errTypeField = errors.New(`"type" is written from the question's type, not from its fields`)

// MemberError reports a question member that could not be written.
type MemberError struct {
	// Member is the member's path inside the question object, as the API
	// names it: "instructions", "criteria.true", "criteria.<label>",
	// "criteria[<level>]", or a raw field's name followed by the path of the
	// offending value inside it (".key" and "[index]" segments).
	Member string
	// Err is the reason.
	Err error
}

// Error returns the member path and the reason.
func (e *MemberError) Error() string { return e.Member + ": " + e.Err.Error() }

// Unwrap returns the reason.
func (e *MemberError) Unwrap() error { return e.Err }

// levelSpan locates the compact JSON of one structured score level in the
// builder's buffer.
type levelSpan struct {
	entry, level int
	start, end   int
}

// Builder serialises a question set, one question at a time, into the
// compact JSON object a request sends as "questions", and records the tables
// of a [Prepared] set on the way.
//
// Members are written in the order the API's schema and the Python SDK use:
// "type", then "instructions", then "criteria". The zero Builder is ready to
// use. After a method returns an error the Builder must not be used again.
type Builder struct {
	buf     []byte
	entries []PreparedQuestion
	spans   []levelSpan
}

// Grow reserves room for questions more questions and size more bytes of
// serialised form, so that a Builder given a good estimate never regrows.
func (b *Builder) Grow(questions, size int) {
	b.buf = slices.Grow(b.buf, size+2)
	b.entries = slices.Grow(b.entries, questions)
}

// begin writes the separator, the name and the opening of a question object
// whose type is typ, and records its entry.
func (b *Builder) begin(name, typ string, kind Kind) error {
	if len(b.buf) == 0 {
		b.buf = append(b.buf, '{')
	} else {
		b.buf = append(b.buf, ',')
	}
	var err error
	if b.buf, err = AppendString(b.buf, name); err != nil {
		return fmt.Errorf("question name: %w", err)
	}
	b.buf = append(b.buf, `:{"type":`...)
	if b.buf, err = AppendString(b.buf, typ); err != nil {
		return &MemberError{Member: "type", Err: err}
	}
	b.entries = append(b.entries, PreparedQuestion{Name: name, Kind: kind})
	return nil
}

// member writes the member key and content c, when c is not nil.
func (b *Builder) member(key string, c *Content) error {
	if c == nil {
		return nil
	}
	b.buf = append(b.buf, ',', '"')
	b.buf = append(b.buf, key...)
	b.buf = append(b.buf, '"', ':')
	return b.content(key, c)
}

// content writes c, reporting a failure as a [*MemberError] for path.
func (b *Builder) content(path string, c *Content) error {
	var err error
	if b.buf, err = AppendContent(b.buf, *c); err != nil {
		return &MemberError{Member: path, Err: err}
	}
	return nil
}

// Noul writes a yes/no question. A nil instructions leaves the member out; a
// nil yes or no leaves that outcome out of "criteria", and "criteria" is left
// out when both are nil.
func (b *Builder) Noul(name string, instructions, yes, no *Content) error {
	if err := b.begin(name, "noul", KindNoul); err != nil {
		return err
	}
	if err := b.member("instructions", instructions); err != nil {
		return err
	}
	if yes != nil || no != nil {
		b.buf = append(b.buf, `,"criteria":{`...)
		if yes != nil {
			b.buf = append(b.buf, `"true":`...)
			if err := b.content("criteria.true", yes); err != nil {
				return err
			}
		}
		if no != nil {
			if yes != nil {
				b.buf = append(b.buf, ',')
			}
			b.buf = append(b.buf, `"false":`...)
			if err := b.content("criteria.false", no); err != nil {
				return err
			}
		}
		b.buf = append(b.buf, '}')
	}
	b.buf = append(b.buf, '}')
	return nil
}

// Choice writes a choice question whose options are labels, in order; the
// description of option i is description(i), written as null when it is nil.
// The labels become the question's Options table: the caller must not modify
// them afterwards. Labels are not checked for repeats.
func (b *Builder) Choice(name string, instructions *Content, labels []string, description func(i int) *Content) error {
	if err := b.begin(name, "choice", KindChoice); err != nil {
		return err
	}
	if err := b.member("instructions", instructions); err != nil {
		return err
	}
	b.buf = append(b.buf, `,"criteria":{`...)
	var err error
	for i, label := range labels {
		if i > 0 {
			b.buf = append(b.buf, ',')
		}
		if b.buf, err = AppendString(b.buf, label); err != nil {
			return &MemberError{Member: "criteria." + label, Err: err}
		}
		b.buf = append(b.buf, ':')
		d := description(i)
		if d == nil {
			b.buf = append(b.buf, "null"...)
			continue
		}
		// The member path is built only on failure: a concatenation per
		// option would allocate on every Prepare.
		if b.buf, err = AppendContent(b.buf, *d); err != nil {
			return &MemberError{Member: "criteria." + label, Err: err}
		}
	}
	b.buf = append(b.buf, '}', '}')
	b.entries[len(b.entries)-1].Options = labels
	return nil
}

// Score writes a score question whose levels are levels, lowest first. The
// levels become the question's Levels table: the caller must not modify them
// afterwards, and [Builder.Prepared] points every JSON level at its compact
// bytes inside the finished set.
func (b *Builder) Score(name string, instructions *Content, levels []Content) error {
	if err := b.begin(name, "score", KindScore); err != nil {
		return err
	}
	if err := b.member("instructions", instructions); err != nil {
		return err
	}
	b.buf = append(b.buf, `,"criteria":[`...)
	entry := len(b.entries) - 1
	for i := range levels {
		if i > 0 {
			b.buf = append(b.buf, ',')
		}
		start := len(b.buf)
		var err error
		if b.buf, err = AppendContent(b.buf, levels[i]); err != nil {
			return &MemberError{Member: "criteria[" + strconv.Itoa(i) + "]", Err: err}
		}
		if levels[i].IsJSON() {
			b.spans = append(b.spans, levelSpan{entry: entry, level: i, start: start, end: len(b.buf)})
		}
	}
	b.buf = append(b.buf, ']', '}')
	b.entries[entry].Levels = levels
	return nil
}

// Raw writes a question of type typ, a type the SDK may not model, whose
// other members are fields, in sorted key order after "type". A field value
// is nil, a bool, a string, an integer or float kind, []any, []string,
// map[string]any or map[string]string (maps written in sorted key order,
// nested to any depth up to 1000 levels), or a value leaf knows. fields must
// not name "type". The question's Kind is [ParseKind] of typ; a raw question
// has no Options or Levels table.
func (b *Builder) Raw(name, typ string, fields map[string]any, leaf Leaf) error {
	if err := b.begin(name, typ, ParseKind(typ)); err != nil {
		return err
	}
	var err error
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		if key == "type" {
			return &MemberError{Member: key, Err: errTypeField}
		}
		b.buf = append(b.buf, ',')
		if b.buf, err = AppendString(b.buf, key); err != nil {
			return &MemberError{Member: key, Err: err}
		}
		b.buf = append(b.buf, ':')
		var verr *valueError
		if b.buf, verr = appendValue(b.buf, fields[key], leaf, 0); verr != nil {
			return &MemberError{Member: key + verr.path, Err: verr.err}
		}
	}
	b.buf = append(b.buf, '}')
	return nil
}

// Finish closes the JSON object and makes *p the prepared set, as
// [NewPrepared] would, so that a caller holding a Prepared inside a larger
// value does not pay a second allocation. Questions has no spare capacity: an
// append to it copies instead of writing into memory the set shares. p is
// left unchanged when Finish fails, and the Builder must not be used
// afterwards.
func (b *Builder) Finish(p *Prepared) error {
	if len(b.buf) == 0 {
		b.buf = append(b.buf, '{')
	}
	b.buf = slices.Clip(append(b.buf, '}'))
	for _, s := range b.spans {
		b.entries[s.entry].Levels[s.level].JSON = b.buf[s.start:s.end:s.end]
	}
	return p.init(b.buf, b.entries)
}
