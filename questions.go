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
	"errors"
	"strconv"
	"unicode/utf8"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// Noul is a yes/no question: how true is a statement about the state? See
// https://docs.typesafe.ai/primitives/noul.
//
// Every member is optional; an unset one is left off the wire. Yes and No
// describe what counts as each outcome and are sent as the "criteria"
// object's "true" and "false" members; "criteria" is left off when both are
// unset. The Python SDK can also send an empty "criteria" object or a null
// outcome, which mean the same as leaving them out; a [RawQuestion] sends
// those shapes when they are wanted.
type Noul struct {
	// Instructions is the question or statement to evaluate.
	Instructions Content
	// Yes describes what counts as a yes answer.
	Yes Content
	// No describes what counts as a no answer.
	No Content
}

// Choice is a question that picks one of a set of named options. See
// https://docs.typesafe.ai/primitives/choice.
type Choice struct {
	// Instructions is what the model should decide; it is optional.
	Instructions Content
	// Options are the options, sent as the "criteria" object in this order.
	// Labels must be unique. With no options an empty object is sent, and
	// the server judges it.
	Options Options
}

// Options lists the options of a [Choice] in the order they are sent.
type Options []Option

// Option is one option of a [Choice].
type Option struct {
	// Label is the option's name; the answer's choice and probabilities
	// name options by it.
	Label string
	// Description describes the option. An unset Description is sent as
	// null: the option is interpreted by its label alone.
	Description Content
}

// Score is a question that rates the state on an ordered scale. See
// https://docs.typesafe.ai/primitives/score.
type Score struct {
	// Instructions is what the model should rate; it is optional.
	Instructions Content
	// Levels describes the scale, lowest first: a level's index is its
	// score. At least one level is required and none may be unset.
	Levels []Content
}

// RawQuestion is a question of a type, or with members, that this version of
// the SDK does not model, sent as a JSON object: "type" first, then Fields in
// sorted key order.
//
// Type must not be empty, and Fields must not name "type". A field value is
// nil, a bool, a string, an integer or float kind (a float must be finite, and
// is spelled as the Python SDK spells it: 3.0, 0.00001, 1e-6, 1e+16), []any,
// []string, map[string]any, map[string]string, [Content] (unset Content is
// null) or [RawJSON], nested in maps and slices to any depth up to 1000
// levels. Map members are sent in sorted key order, as a Go map has no order
// of its own. A value of any other type, such as []int, a named string type or
// a struct, makes Prepare fail: encode it first and pass the bytes as
// [RawJSON], which is checked and sent without its insignificant whitespace
// but never decoded.
//
// [Questions.Prepare] applies the checks the Python SDK applies to a raw
// question: a "choice" or "score" has a "criteria" field, and a "score"'s
// criteria is not empty (not null, false, 0, "", [] or {}). Everything else
// is left to the server.
type RawQuestion struct {
	// Type is the question type, sent as the "type" member.
	Type string
	// Fields are the other members of the question object.
	Fields map[string]any
}

// Questions is a question set under construction: the questions of one call,
// keyed by the names their answers come back under, in the order they are
// sent.
//
// Nothing is checked until [Questions.Prepare], which validates and
// serialises the whole set once. The zero Questions is an empty set ready to
// use.
//
// Adding a question copies the question value: a later change to the Noul,
// Choice, Score or RawQuestion variable that was added, such as assigning
// its Instructions, does not reach the set. The copy shares what the value
// refers to: the Options and Levels slices, the Fields map and the maps and
// slices inside it, and the bytes behind [JSON] content and [RawJSON]. Those
// are read by Prepare and must not change between adding the question and
// the return of Prepare; the prepared set shares none of them.
type Questions struct {
	entries []questionEntry
}

// questionForm is which of the four question types an entry holds.
type questionForm uint8

const (
	formNoul questionForm = iota
	formChoice
	formScore
	formRaw
)

// questionEntry is one added question.
type questionEntry struct {
	name   string
	form   questionForm
	noul   Noul
	choice Choice
	score  Score
	raw    RawQuestion
}

// NewQuestions returns an empty question set.
func NewQuestions() *Questions { return &Questions{} }

// Noul adds a copy of the yes/no question q under name and returns the set;
// [Questions] says what the copy shares with q.
func (qs *Questions) Noul(name string, q Noul) *Questions {
	qs.entries = append(qs.entries, questionEntry{name: name, form: formNoul, noul: q})
	return qs
}

// Choice adds a copy of the choice question q under name and returns the set;
// [Questions] says what the copy shares with q.
func (qs *Questions) Choice(name string, q Choice) *Questions {
	qs.entries = append(qs.entries, questionEntry{name: name, form: formChoice, choice: q})
	return qs
}

// Score adds a copy of the score question q under name and returns the set;
// [Questions] says what the copy shares with q.
func (qs *Questions) Score(name string, q Score) *Questions {
	qs.entries = append(qs.entries, questionEntry{name: name, form: formScore, score: q})
	return qs
}

// Raw adds a copy of the raw question q under name and returns the set;
// [Questions] says what the copy shares with q.
func (qs *Questions) Raw(name string, q RawQuestion) *Questions {
	qs.entries = append(qs.entries, questionEntry{name: name, form: formRaw, raw: q})
	return qs
}

// Prepare validates the set and serialises it into the bytes every call that
// asks it sends.
//
// It fails with a [*ConfigError] for the first problem, in the order the
// questions were added:
//
//   - the set is empty: "At least one question is required.";
//   - a name is added twice (the Python SDK's dictionary would keep the last
//     question; a Go set rejects the repeat), or is not valid UTF-8;
//   - a [Score] has no levels, or a raw "score" has empty criteria:
//     `Score question "<name>" has no criteria; at least one score is
//     required.`;
//   - a [RawQuestion] has an empty Type, or Fields name "type":
//     `Question "<name>" must be a question object or a dictionary with a
//     nonempty string "type".`;
//   - a raw "choice" or "score" has no "criteria" field:
//     `Question "<name>" requires "criteria".`;
//   - a [Choice] repeats an option label, or a [Score] level is unset;
//   - content or a raw field value cannot be written: text or a string that
//     is not valid UTF-8, [JSON] content that is not a single valid JSON
//     object or array, a field value of an unsupported type, or a float that
//     is NaN or infinite.
//
// The first four messages are the Python SDK's. The returned set does not
// share memory with the questions: changing them afterwards does not change
// it.
func (qs *Questions) Prepare() (*Prepared, error) {
	if len(qs.entries) == 0 {
		return nil, newConfigError("At least one question is required.")
	}
	var b wire.Builder
	b.Grow(len(qs.entries), qs.sizeHint())
	var seen map[string]struct{} // the names so far, when there are too many to scan
	if len(qs.entries) > repeatScanLimit {
		seen = make(map[string]struct{}, len(qs.entries))
	}
	for i := range qs.entries {
		e := &qs.entries[i]
		if err := qs.checkName(i, seen); err != nil {
			return nil, err
		}
		if seen != nil {
			seen[e.name] = struct{}{}
		}
		var err error
		switch e.form {
		case formNoul:
			err = b.Noul(e.name, e.noul.Instructions.orNil(), e.noul.Yes.orNil(), e.noul.No.orNil())
		case formChoice:
			err = prepareChoice(&b, e.name, &e.choice)
		case formScore:
			err = prepareScore(&b, e.name, &e.score)
		case formRaw:
			err = prepareRaw(&b, e.name, &e.raw)
		}
		if err != nil {
			if ce, ok := errors.AsType[*ConfigError](err); ok {
				return nil, ce
			}
			return nil, newConfigError(`Question "`+e.name+`": `+err.Error(), err)
		}
	}
	p := new(Prepared)
	if err := b.Finish(&p.w); err != nil {
		// Unreachable: the names were checked above.
		return nil, newConfigError("Question set cannot be prepared: "+err.Error(), err)
	}
	return p, nil
}

// repeatScanLimit is the number of strings up to which a repeat is found by
// comparing each with the ones before it; past it a map is used. At 32
// strings the scan makes at most 496 comparisons, cheaper than the map's
// allocations, and question sets and option lists are rarely longer.
const repeatScanLimit = 32

// checkName checks the name of question i: valid UTF-8, and not the name of
// an earlier question. seen holds the earlier names when the set is larger
// than repeatScanLimit, and is nil otherwise.
func (qs *Questions) checkName(i int, seen map[string]struct{}) error {
	name := qs.entries[i].name
	if !utf8.ValidString(name) {
		return newConfigError("Question name " + strconv.Quote(name) + " is not valid UTF-8.")
	}
	repeated := false
	if seen != nil {
		_, repeated = seen[name]
	} else {
		for j := range i {
			if qs.entries[j].name == name {
				repeated = true
				break
			}
		}
	}
	if repeated {
		return newConfigError(`Question "` + name + `" is added more than once; question names must be unique.`)
	}
	return nil
}

// quote puts s in double quotes as the Python SDK's messages do, or, when s
// is not valid UTF-8, spells it with Go escapes so that the message stays
// printable.
func quote(s string) string {
	if !utf8.ValidString(s) {
		return strconv.Quote(s)
	}
	return `"` + s + `"`
}

// firstRepeat returns the index of the first string of s that equals an
// earlier one, or -1 when all are distinct.
func firstRepeat(s []string) int {
	if len(s) <= repeatScanLimit {
		for i := range s {
			for j := range i {
				if s[j] == s[i] {
					return i
				}
			}
		}
		return -1
	}
	seen := make(map[string]struct{}, len(s))
	for i, v := range s {
		if _, ok := seen[v]; ok {
			return i
		}
		seen[v] = struct{}{}
	}
	return -1
}

// prepareChoice checks and writes a choice question.
func prepareChoice(b *wire.Builder, name string, q *Choice) error {
	labels := make([]string, len(q.Options))
	for i := range q.Options {
		labels[i] = q.Options[i].Label
	}
	if i := firstRepeat(labels); i >= 0 {
		return newConfigError(`Choice question "` + name + `" has option ` + quote(labels[i]) + ` more than once; option labels must be unique.`)
	}
	return b.Choice(name, q.Instructions.orNil(), labels, func(i int) *wire.Content { return q.Options[i].Description.orNil() })
}

// prepareScore checks and writes a score question.
func prepareScore(b *wire.Builder, name string, q *Score) error {
	if len(q.Levels) == 0 {
		return noCriteria(name)
	}
	levels := make([]wire.Content, len(q.Levels))
	for i := range q.Levels {
		if !q.Levels[i].set {
			return newConfigError(`Score question "` + name + `" level ` + strconv.Itoa(i) + ` is unset; every level needs text or JSON content.`)
		}
		levels[i] = q.Levels[i].w
	}
	return b.Score(name, q.Instructions.orNil(), levels)
}

// noCriteria is the Python SDK's failure for a score question without
// levels.
func noCriteria(name string) *ConfigError {
	return newConfigError(`Score question "` + name + `" has no criteria; at least one score is required.`)
}

// prepareRaw checks a raw question as the Python SDK's normalize_questions
// checks a question dictionary, then writes it.
func prepareRaw(b *wire.Builder, name string, q *RawQuestion) error {
	if _, ok := q.Fields["type"]; ok || q.Type == "" {
		return newConfigError(`Question "` + name + `" must be a question object or a dictionary with a nonempty string "type".`)
	}
	criteria, hasCriteria := q.Fields["criteria"]
	if (q.Type == "choice" || q.Type == "score") && !hasCriteria {
		return newConfigError(`Question "` + name + `" requires "criteria".`)
	}
	if q.Type == "score" && falsy(criteria) {
		return noCriteria(name)
	}
	return b.Raw(name, q.Type, q.Fields, appendLeaf)
}

// falsy reports whether Python would find the field value v false: None,
// False, zero, or an empty string, list or dictionary. A value that cannot be
// written is not falsy; writing it reports the problem.
func falsy(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case bool:
		return !v
	case string:
		return v == ""
	case int:
		return v == 0
	case int8:
		return v == 0
	case int16:
		return v == 0
	case int32:
		return v == 0
	case int64:
		return v == 0
	case uint:
		return v == 0
	case uint8:
		return v == 0
	case uint16:
		return v == 0
	case uint32:
		return v == 0
	case uint64:
		return v == 0
	case float32:
		return v == 0
	case float64:
		return v == 0
	case []any:
		return len(v) == 0
	case []string:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	case map[string]string:
		return len(v) == 0
	case Content:
		if !v.set {
			return true
		}
		if !v.IsJSON() {
			return v.w.Text == ""
		}
		return falsyJSON(v.w.JSON)
	case RawJSON:
		return falsyJSON(v)
	default:
		return false
	}
}

// falsyJSON reports whether the JSON value raw is one Python finds false:
// null, false, "", [], {} or a number equal to zero.
func falsyJSON(raw []byte) bool {
	var buf [32]byte
	compact, err := wire.AppendJSON(buf[:0], raw)
	if err != nil {
		return false
	}
	switch s := string(compact); s {
	case "null", "false", `""`, "[]", "{}":
		return true
	default:
		if s[0] != '-' && (s[0] < '0' || s[0] > '9') {
			return false
		}
		// A number is zero when every digit of its mantissa is.
		for i := range len(s) {
			switch c := s[i]; {
			case c == 'e' || c == 'E':
				return true
			case '1' <= c && c <= '9':
				return false
			}
		}
		return true
	}
}

// appendLeaf writes the root package's own raw field value types.
func appendLeaf(dst []byte, v any) ([]byte, bool, error) {
	switch v := v.(type) {
	case RawJSON:
		out, err := wire.AppendJSON(dst, v)
		return out, true, err
	case Content:
		if !v.set {
			return append(dst, "null"...), true, nil
		}
		out, err := wire.AppendContent(dst, v.w)
		return out, true, err
	default:
		return dst, false, nil
	}
}

// sizeHint estimates the serialised size of the set, so that the builder's
// buffer rarely grows: the text and JSON lengths plus the member names and
// punctuation around them. Escaping can make the result longer and removing
// whitespace from JSON shorter.
func (qs *Questions) sizeHint() int {
	n := 0
	for i := range qs.entries {
		e := &qs.entries[i]
		n += len(e.name) + len(`,"":{"type":"choice","instructions":,"criteria":{}}`)
		switch e.form {
		case formNoul:
			n += contentSize(&e.noul.Instructions) + contentSize(&e.noul.Yes) + contentSize(&e.noul.No)
		case formChoice:
			n += contentSize(&e.choice.Instructions)
			for j := range e.choice.Options {
				n += len(e.choice.Options[j].Label) + contentSize(&e.choice.Options[j].Description) + len(`,"":`)
			}
		case formScore:
			n += contentSize(&e.score.Instructions)
			for j := range e.score.Levels {
				n += contentSize(&e.score.Levels[j]) + 1
			}
		case formRaw:
			n += len(e.raw.Type) + 32*len(e.raw.Fields)
		}
	}
	return n
}

// contentSize is the size of c as written, before escaping, plus its quotes.
func contentSize(c *Content) int {
	return len(c.w.Text) + len(c.w.JSON) + 2
}
