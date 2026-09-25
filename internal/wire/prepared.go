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
)

// ErrDuplicateQuestion is returned by [NewPrepared] for a question set that
// names a question twice.
var ErrDuplicateQuestion = errors.New("wire: question name repeated in a prepared set")

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
// It is read-only after NewPrepared returns and safe for concurrent use. The
// entries sit behind [Prepared.Entries] so that the lookup index built over
// them cannot go stale.
type Prepared struct {
	// Questions is the compact JSON object that is the value of a request's
	// "questions" member, spliced into every request body as is.
	Questions []byte

	entries []PreparedQuestion
	index   map[string]int // nil when entries has at most linearLimit questions
}

// NewPrepared returns the prepared set of the questions whose serialised form
// is questions and whose tables are entries. It fails with
// [ErrDuplicateQuestion] when two entries share a name, whatever the size of
// the set (the root package rejects a repeated name before it gets here). The
// slices are kept, not copied: the caller must not modify them afterwards.
func NewPrepared(questions []byte, entries []PreparedQuestion) (*Prepared, error) {
	p := &Prepared{Questions: questions, entries: entries}
	if len(entries) <= linearLimit {
		for i := range entries {
			for j := range i {
				if entries[j].Name == entries[i].Name {
					return nil, fmt.Errorf("%w: %q", ErrDuplicateQuestion, entries[i].Name)
				}
			}
		}
		return p, nil
	}
	p.index = make(map[string]int, len(entries))
	for i := range entries {
		if _, ok := p.index[entries[i].Name]; ok {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateQuestion, entries[i].Name)
		}
		p.index[entries[i].Name] = i
	}
	return p, nil
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
