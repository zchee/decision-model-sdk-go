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
	"iter"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// AnswerKind is the kind of question an answer belongs to, which the answer's
// "type" member names on the wire.
type AnswerKind uint8

// The kinds of [Answer]. The zero AnswerKind is none of them.
const (
	// KindNoul is a yes/no answer, a [NoulAnswer] ("type": "noul").
	KindNoul AnswerKind = AnswerKind(wire.KindNoul)
	// KindChoice is a choice answer, a [ChoiceAnswer] ("type": "choice").
	KindChoice AnswerKind = AnswerKind(wire.KindChoice)
	// KindScore is a score answer, a [ScoreAnswer] ("type": "score").
	KindScore AnswerKind = AnswerKind(wire.KindScore)
)

// String returns the kind's name on the wire ("noul", "choice" or "score"),
// or "unknown" for a value that is none of them.
func (k AnswerKind) String() string { return wire.Kind(k).String() }

// NoulAnswer is the answer to a yes/no question. See the noul primitive
// (https://docs.typesafe.ai/primitives/noul).
type NoulAnswer struct {
	w wire.NoulAnswer
}

// Noul returns the probability, from 0 to 1, that the answer is yes or the
// statement is true.
func (a NoulAnswer) Noul() float64 { return a.w.Noul }

// MarshalJSON returns the answer as the Python SDK's model_dump_json writes
// it, such as {"type":"noul","noul":0.98}.
func (a NoulAnswer) MarshalJSON() ([]byte, error) { return wire.AppendNoulAnswer(nil, &a.w) }

// ChoiceAnswer is the answer to a choice question: the option picked and the
// probability of every option. See the choice primitive
// (https://docs.typesafe.ai/primitives/choice).
type ChoiceAnswer struct {
	w wire.ChoiceAnswer
}

// Choice returns the label of the option with the highest probability.
func (a ChoiceAnswer) Choice() string { return a.w.Choice }

// Confidence returns how sure the model is of the pick, from 0 to 1.
func (a ChoiceAnswer) Confidence() float64 { return a.w.Confidence }

// Probability returns the probability of the option called label, and
// whether the answer lists that option.
func (a ChoiceAnswer) Probability(label string) (float64, bool) {
	return a.w.Probability(label)
}

// Probabilities yields every option's label with its probability, in the
// order the response lists them. Labels are unique.
func (a ChoiceAnswer) Probabilities() iter.Seq2[string, float64] {
	return func(yield func(string, float64) bool) {
		for _, p := range a.w.Probabilities {
			if !yield(p.Label, p.Probability) {
				return
			}
		}
	}
}

// MarshalJSON returns the answer as the Python SDK's model_dump_json writes
// it, such as
// {"type":"choice","choice":"billing","confidence":0.9,"probabilities":{"billing":0.9,"support":0.1}},
// the probabilities in the order of Probabilities.
func (a ChoiceAnswer) MarshalJSON() ([]byte, error) { return wire.AppendChoiceAnswer(nil, &a.w) }

// ScoreAnswer is the answer to a score question: the expected score, the
// question's rubric as the response echoes it, and the probability of every
// level. Levels count from zero. See the score primitive
// (https://docs.typesafe.ai/primitives/score).
type ScoreAnswer struct {
	w wire.ScoreAnswer
}

// Score returns the expected score: the probability-weighted average of the
// levels, which may fall between two of them.
func (a ScoreAnswer) Score() float64 { return a.w.Score }

// Confidence returns how sure the model is of the score, from 0 to 1.
func (a ScoreAnswer) Confidence() float64 { return a.w.Confidence }

// Description returns the description of level from the legend, and whether
// the legend lists that level. A description sent as JSON keeps the exact
// bytes it arrived as; they are shared and must not be modified.
func (a ScoreAnswer) Description(level uint32) (Content, bool) {
	d, ok := a.w.Description(level)
	if !ok {
		return Content{}, false
	}
	return Content{w: d, set: true}, true
}

// Legend yields every level of the legend with its description, in the order
// the response lists them. Levels are unique.
func (a ScoreAnswer) Legend() iter.Seq2[uint32, Content] {
	return func(yield func(uint32, Content) bool) {
		for _, e := range a.w.Legend {
			if !yield(e.Level, Content{w: e.Description, set: true}) {
				return
			}
		}
	}
}

// Probability returns the probability of level, and whether the answer lists
// that level.
func (a ScoreAnswer) Probability(level uint32) (float64, bool) {
	return a.w.Probability(level)
}

// Probabilities yields every level with its probability, in the order the
// response lists them. Levels are unique.
func (a ScoreAnswer) Probabilities() iter.Seq2[uint32, float64] {
	return func(yield func(uint32, float64) bool) {
		for _, p := range a.w.Probabilities {
			if !yield(p.Level, p.Probability) {
				return
			}
		}
	}
}

// MarshalJSON returns the answer as the Python SDK's model_dump_json writes
// it, such as
// {"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"bad"},"probabilities":{"0":1.0}},
// the legend and the probabilities in the order of Legend and
// Probabilities, each level a decimal member name. A structured level is
// written as the bytes the response carried ([ScoreAnswer.Description]),
// where the Python SDK writes the value it parsed.
func (a ScoreAnswer) MarshalJSON() ([]byte, error) { return wire.AppendScoreAnswer(nil, &a.w) }

// Answer is one answer of any kind: Kind says which of Noul, Choice and
// Score holds it.
type Answer struct {
	w wire.Answer
}

// Kind returns the kind of the answer.
func (a Answer) Kind() AnswerKind { return AnswerKind(a.w.Kind) }

// Noul returns the answer as a yes/no answer, and whether it is one.
func (a Answer) Noul() (NoulAnswer, bool) {
	return NoulAnswer{a.w.Noul}, a.w.Kind == wire.KindNoul
}

// Choice returns the answer as a choice answer, and whether it is one.
func (a Answer) Choice() (ChoiceAnswer, bool) {
	return ChoiceAnswer{a.w.Choice}, a.w.Kind == wire.KindChoice
}

// Score returns the answer as a score answer, and whether it is one.
func (a Answer) Score() (ScoreAnswer, bool) {
	return ScoreAnswer{a.w.Score}, a.w.Kind == wire.KindScore
}

// MarshalJSON returns the answer as the MarshalJSON of its kind writes it.
// The zero Answer, which is none of the kinds, is written null.
func (a Answer) MarshalJSON() ([]byte, error) { return wire.AppendAnswer(nil, &a.w) }

// Answers is the answers of one response, keyed by the question names. It
// reads as the Python SDK's answers dict: a name the response repeats keeps
// the position where it first appeared and the value of its last
// appearance, and an answer of a type this version does not model is not
// there (it stays in the raw body, [ResponseMeta.RawBody]). The Python SDK's
// nouls, choices and scores views are the Nouls, Choices and Scores
// iterators.
//
// An Answers is a view of its response: it is valid as long as the response
// is, and every value it yields is a copy that shares the response's slices
// and bytes, which must not be modified. The zero Answers is empty.
type Answers struct {
	s *wire.Answers
}

// Len returns the number of answers.
func (a Answers) Len() int {
	if a.s == nil {
		return 0
	}
	return a.s.Len()
}

// Get returns the answer to the question called name, and whether there is
// one.
func (a Answers) Get(name string) (Answer, bool) {
	if a.s == nil {
		return Answer{}, false
	}
	w, ok := a.s.Get(name)
	return Answer{w}, ok
}

// Noul returns the answer to the question called name, and whether there is
// one and it is a yes/no answer.
func (a Answers) Noul(name string) (NoulAnswer, bool) {
	if ans, ok := a.Get(name); ok {
		return ans.Noul()
	}
	return NoulAnswer{}, false
}

// Choice returns the answer to the question called name, and whether there
// is one and it is a choice answer.
func (a Answers) Choice(name string) (ChoiceAnswer, bool) {
	if ans, ok := a.Get(name); ok {
		return ans.Choice()
	}
	return ChoiceAnswer{}, false
}

// Score returns the answer to the question called name, and whether there is
// one and it is a score answer.
func (a Answers) Score(name string) (ScoreAnswer, bool) {
	if ans, ok := a.Get(name); ok {
		return ans.Score()
	}
	return ScoreAnswer{}, false
}

// All yields every answer with its question's name, in the order the names
// first appeared in the response.
func (a Answers) All() iter.Seq2[string, Answer] {
	return func(yield func(string, Answer) bool) {
		for _, e := range a.entries() {
			if !yield(e.Name, Answer{e.Answer}) {
				return
			}
		}
	}
}

// Nouls yields the yes/no answers with their questions' names, in the order
// of All.
func (a Answers) Nouls() iter.Seq2[string, NoulAnswer] {
	return func(yield func(string, NoulAnswer) bool) {
		for _, e := range a.entries() {
			if e.Answer.Kind == wire.KindNoul && !yield(e.Name, NoulAnswer{e.Answer.Noul}) {
				return
			}
		}
	}
}

// Choices yields the choice answers with their questions' names, in the
// order of All.
func (a Answers) Choices() iter.Seq2[string, ChoiceAnswer] {
	return func(yield func(string, ChoiceAnswer) bool) {
		for _, e := range a.entries() {
			if e.Answer.Kind == wire.KindChoice && !yield(e.Name, ChoiceAnswer{e.Answer.Choice}) {
				return
			}
		}
	}
}

// Scores yields the score answers with their questions' names, in the order
// of All.
func (a Answers) Scores() iter.Seq2[string, ScoreAnswer] {
	return func(yield func(string, ScoreAnswer) bool) {
		for _, e := range a.entries() {
			if e.Answer.Kind == wire.KindScore && !yield(e.Name, ScoreAnswer{e.Answer.Score}) {
				return
			}
		}
	}
}

// MarshalJSON returns the answers as the object the "answers" member of a
// response payload holds, {"<name>":<answer>,…}, in the order of All, each
// answer as its MarshalJSON writes it. The zero Answers is written {}.
func (a Answers) MarshalJSON() ([]byte, error) { return wire.AppendAnswers(nil, a.s) }

// entries returns the answers in order, nil for the zero Answers.
func (a Answers) entries() []wire.AnswerEntry {
	if a.s == nil {
		return nil
	}
	return a.s.Entries()
}
