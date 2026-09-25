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

import "slices"

// Kind is the kind of question an answer belongs to, named on the wire by the
// answer's "type" member.
type Kind uint8

const (
	// KindUnknown is the zero Kind: an answer whose type this version of the
	// SDK does not model, or one whose type is not known yet.
	KindUnknown Kind = iota
	// KindNoul is a yes/no answer, "type": "noul".
	KindNoul
	// KindChoice is a choice answer, "type": "choice".
	KindChoice
	// KindScore is a score answer, "type": "score".
	KindScore
)

// ParseKind returns the Kind whose wire name is typ, or KindUnknown when typ
// names a type this version does not model. The match is exact and
// case-sensitive, as the API's discriminator is.
func ParseKind(typ string) Kind {
	switch typ {
	case "noul":
		return KindNoul
	case "choice":
		return KindChoice
	case "score":
		return KindScore
	default:
		return KindUnknown
	}
}

// String returns the wire name of k ("noul", "choice" or "score"), or
// "unknown" for KindUnknown and for any value outside the enumeration.
func (k Kind) String() string {
	switch k {
	case KindNoul:
		return "noul"
	case KindChoice:
		return "choice"
	case KindScore:
		return "score"
	default:
		return "unknown"
	}
}

// NoulAnswer is the value of a yes/no answer.
type NoulAnswer struct {
	// Noul is the probability, from 0 to 1, that the answer is yes or the
	// statement is true.
	Noul float64
}

// LabelProbability is the probability the model gave one option of a choice
// question.
type LabelProbability struct {
	// Label is the option's name.
	Label string
	// Probability is the option's probability, from 0 to 1.
	Probability float64
}

// ChoiceAnswer is the value of a choice answer.
type ChoiceAnswer struct {
	// Choice is the label of the option with the highest probability.
	Choice string
	// Confidence is how sure the model is of the pick, from 0 to 1.
	Confidence float64
	// Probabilities lists every option with its probability. Labels are
	// unique; the order is the order the decoder stored them in.
	Probabilities []LabelProbability
}

// Probability returns the probability of the option called label, and whether
// the answer lists that option.
func (a *ChoiceAnswer) Probability(label string) (float64, bool) {
	for i := range a.Probabilities {
		if a.Probabilities[i].Label == label {
			return a.Probabilities[i].Probability, true
		}
	}
	return 0, false
}

// LegendEntry is the description a score question gave one level, as the
// response echoes it back.
type LegendEntry struct {
	// Level is the score level, counted from zero.
	Level uint32
	// Description is the level's description: text, or a JSON object or
	// array kept as the exact bytes it arrived as (the question's own bytes
	// when they are equal).
	Description Content
}

// LevelProbability is the probability the model gave one level of a score
// question.
type LevelProbability struct {
	// Level is the score level, counted from zero.
	Level uint32
	// Probability is the level's probability, from 0 to 1.
	Probability float64
}

// ScoreAnswer is the value of a score answer.
type ScoreAnswer struct {
	// Score is the expected score: the probability-weighted average of the
	// levels. It may fall between two levels.
	Score float64
	// Confidence is how sure the model is of the score, from 0 to 1.
	Confidence float64
	// Legend lists every level with its description. Levels are unique; the
	// order is the order the decoder stored them in.
	Legend []LegendEntry
	// Probabilities lists every level with its probability. Levels are
	// unique; the order is the order the decoder stored them in.
	Probabilities []LevelProbability
}

// Description returns the description of level, and whether the legend lists
// that level.
func (a *ScoreAnswer) Description(level uint32) (Content, bool) {
	for i := range a.Legend {
		if a.Legend[i].Level == level {
			return a.Legend[i].Description, true
		}
	}
	return Content{}, false
}

// Probability returns the probability of level, and whether the answer lists
// that level.
func (a *ScoreAnswer) Probability(level uint32) (float64, bool) {
	for i := range a.Probabilities {
		if a.Probabilities[i].Level == level {
			return a.Probabilities[i].Probability, true
		}
	}
	return 0, false
}

// Answer is one answer of any kind. Kind says which of Noul, Choice and Score
// holds the value; the other two are zero. An Answer of KindUnknown holds no
// value: it marks a slot whose type is not modelled, which
// [Answers.DropUnknown] removes.
type Answer struct {
	// Kind selects the field that holds the value.
	Kind Kind
	// Noul is the value when Kind is KindNoul.
	Noul NoulAnswer
	// Choice is the value when Kind is KindChoice.
	Choice ChoiceAnswer
	// Score is the value when Kind is KindScore.
	Score ScoreAnswer
}

// AnswerEntry is one answer with the name of the question it answers.
type AnswerEntry struct {
	// Name is the question name.
	Name string
	// Answer is the answer.
	Answer Answer
}

// linearLimit is the number of entries up to which a lookup scans the entries
// instead of hashing the name. A call carries a handful of questions, where a
// scan is faster than a map and allocates nothing; past the limit a map keeps
// a response with many answers linear in time.
const linearLimit = 8

// Answers is the set of answers of one response, keyed by question name.
//
// It behaves as a Python dict built from the response's "answers" object: a
// name keeps the position at which it first appeared and takes the value of
// its last appearance, so a document that repeats a name (JSON allows it, the
// API does not do it) yields one entry. The zero value is an empty set ready
// to use.
type Answers struct {
	entries []AnswerEntry
	index   map[string]int // nil until entries outgrow linearLimit
}

// Len returns the number of entries.
func (s *Answers) Len() int { return len(s.entries) }

// Entries returns the entries in the order their names first appeared. The
// slice is shared with s: callers must not modify it, and it is valid until
// the next Put, DropUnknown or Reset.
func (s *Answers) Entries() []AnswerEntry { return s.entries }

// Grow makes room for n more entries, so that the next n calls to Put with
// new names do not reallocate. A negative n is treated as zero.
func (s *Answers) Grow(n int) {
	if n > 0 {
		s.entries = slices.Grow(s.entries, n)
	}
}

// Get returns the answer to the question called name, and whether there is
// one.
func (s *Answers) Get(name string) (Answer, bool) {
	if i := s.find(name); i >= 0 {
		return s.entries[i].Answer, true
	}
	return Answer{}, false
}

// Put stores a under name. A new name is appended; a name already present
// keeps its position and takes a (last wins).
func (s *Answers) Put(name string, a Answer) {
	if i := s.find(name); i >= 0 {
		s.entries[i].Answer = a
		return
	}
	s.entries = append(s.entries, AnswerEntry{Name: name, Answer: a})
	switch {
	case s.index != nil:
		s.index[name] = len(s.entries) - 1
	case len(s.entries) > linearLimit:
		s.index = make(map[string]int, 2*len(s.entries))
		for i := range s.entries {
			s.index[s.entries[i].Name] = i
		}
	}
}

// DropUnknown removes every entry of KindUnknown and keeps the order of the
// rest, as the Python SDK deletes an answer of an unrecognised type after the
// whole object has been read. It runs in time linear in the number of
// entries.
func (s *Answers) DropUnknown() {
	kept := s.entries[:0]
	for i := range s.entries {
		if s.entries[i].Answer.Kind != KindUnknown {
			kept = append(kept, s.entries[i])
		}
	}
	clear(s.entries[len(kept):]) // release the dropped values to the GC
	s.entries = kept
	if s.index != nil {
		clear(s.index)
		for i := range s.entries {
			s.index[s.entries[i].Name] = i
		}
	}
}

// Reset removes every entry and keeps the storage, for a document that
// repeats its top-level "answers" member: the last one replaces every answer
// the earlier ones held.
func (s *Answers) Reset() {
	clear(s.entries)
	s.entries = s.entries[:0]
	clear(s.index)
}

// find returns the position of name, or -1.
func (s *Answers) find(name string) int {
	if s.index != nil {
		if i, ok := s.index[name]; ok {
			return i
		}
		return -1
	}
	for i := range s.entries {
		if s.entries[i].Name == name {
			return i
		}
	}
	return -1
}
