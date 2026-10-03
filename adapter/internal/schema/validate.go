// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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

package schema

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
)

// The two limits of pydantic's JSON parser (jiter 0.14.0) on the text of an
// answer, which refuse the whole text wherever the value stands, also in a
// member that is ignored or that a later one of the same name replaces.
const (
	// maxEnclosing is the most arrays and objects a value may have around
	// it, the top-level one included (DEFAULT_RECURSION_LIMIT). The value
	// is a scalar or a container; an empty container is a value like any
	// other.
	maxEnclosing = 200
	// maxIntegerPart is the most characters of a number before its
	// fraction or exponent, the sign counted.
	maxIntegerPart = 4300
)

// The prefixes of the field names of upstream's generated models
// (_schema.py:100 and :188): the question ids and the labels are their
// aliases.
const (
	answerField      = "answer_"
	probabilityField = "probability_"
)

// Answer is the validated answer to one question. Which member holds it
// depends on the question's kind and on the answer mode.
type Answer struct {
	// Bool answers a Noul question in Discrete mode.
	Bool bool
	// Probability answers a Noul question in Probabilities mode: a number
	// from 0 to 1.
	Probability float64
	// Level answers a Score question in Discrete mode: the index of a
	// criterion.
	Level int
	// Label answers a Choice question in Discrete mode: one of the labels.
	Label string
	// Probabilities answers a Choice or Score question in Probabilities
	// mode: one number from 0 to 1 per label, in the order of the
	// question's Labels. Nothing relates them to each other; they need not
	// add up to 1.
	Probabilities []float64
}

// ValidationError is the refusal of an answer text: it is not JSON, or it
// does not match the schema. Its text goes into the prompt that asks the
// model to correct its answer.
type ValidationError struct {
	// Issues are the faults, in the order pydantic reports them: in each
	// object the unknown members first, in the order of the text, then the
	// members of the schema in its order. A text that is not JSON has one
	// issue.
	Issues []Issue
}

// Issue is one fault of an answer text.
type Issue struct {
	// Type is pydantic's name for the kind of fault: json_invalid,
	// model_type, missing, extra_forbidden, bool_type, int_type,
	// float_type, literal_error, greater_than_equal, less_than or
	// less_than_equal.
	Type string
	// Loc is the path of the faulty value from the top of the text:
	// "answers", then a question's name, then a label. It is empty for the
	// text as a whole.
	Loc []string
	// Message says what is wrong.
	Message string
}

// Error returns the number of faults and each fault as "path: message".
func (e *ValidationError) Error() string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(len(e.Issues)))
	b.WriteString(" validation error")
	if len(e.Issues) != 1 {
		b.WriteByte('s')
	}
	for _, issue := range e.Issues {
		b.WriteString("\n")
		if len(issue.Loc) > 0 {
			b.WriteString(strings.Join(issue.Loc, "."))
			b.WriteString(": ")
		}
		b.WriteString(issue.Message)
	}
	return b.String()
}

// level is one object of the answer and the members the schema gives it.
type level struct {
	// fields are the member names of the schema, in its order: "answers",
	// the question ids, or the labels of one question.
	fields []string
	index  map[string]int
	// children holds, per field, the level of the object that is its value,
	// or nil for a field whose value is not read as an object.
	children []*level
	// generated is the prefix of the field names upstream's generated model
	// gives its fields, or "" for the top level, which has none.
	generated string
}

func newLevel(fields []string, generated string) *level {
	lv := &level{fields: fields, index: make(map[string]int, len(fields)), children: make([]*level, len(fields)), generated: generated}
	for i, name := range fields {
		lv.index[name] = i
	}
	return lv
}

// ignored reports whether a member name that is not a field is one pydantic
// neither validates nor refuses: the name its generated model gives one of
// its fields, the prefix followed by the index of a field in decimal
// (pydantic-core 2.46.4, src/validators/model_fields.rs:567-593). Such a
// name never supplies the field's value.
func (lv *level) ignored(name string) bool {
	digits, ok := strings.CutPrefix(name, lv.generated)
	if !ok || lv.generated == "" {
		return false
	}
	i, err := strconv.Atoi(digits)
	return err == nil && i >= 0 && i < len(lv.fields) && strconv.Itoa(i) == digits
}

// levels builds the levels of a Spec: the top one, the answers, and in
// Probabilities mode one per Choice or Score question.
func levels(questions []Question, mode Mode) *level {
	ids := make([]string, len(questions))
	for i := range questions {
		ids[i] = questions[i].id
	}
	answers := newLevel(ids, answerField)
	if mode == Probabilities {
		for i := range questions {
			if questions[i].kind != Noul {
				answers.children[i] = newLevel(questions[i].labels, probabilityField)
			}
		}
	}
	top := newLevel([]string{"answers"}, "")
	top.children[0] = answers
	return top
}

// slot is the value of one member as the text gives it last: the kind of
// its first token, the text of a string or the literal of a number, and the
// members of an object the schema reads.
type slot struct {
	present bool
	kind    jsonx.TokenKind
	text    string
	object  *object
}

// object is one object of the text, read against a level.
type object struct {
	// slots holds the last value of each field.
	slots []slot
	// unknown holds the names that are neither a field nor ignored, once
	// per occurrence, in the order of the text.
	unknown []string
}

// reader reads the tokens of an answer text and applies the parser's two
// limits to each.
type reader struct {
	tokens *jsonx.Tokens
}

// next returns the next token, or the reason the text is not JSON for
// pydantic's parser.
func (r *reader) next() (jsonx.Token, error) {
	tok, err := r.tokens.Next()
	if err != nil {
		return tok, errors.New(strings.TrimPrefix(err.Error(), "jsonx: "))
	}
	switch tok.Kind {
	case jsonx.TokenEnd, jsonx.TokenName, jsonx.TokenEndArray, jsonx.TokenEndObject:
		return tok, nil
	case jsonx.TokenNumber:
		// The limit is on the spelling, checked before any conversion: a
		// number that is never converted, because it is skipped, counts
		// too.
		if integerPart(tok.Text) > maxIntegerPart {
			return tok, fmt.Errorf("number out of range: more than %d characters before the fraction or the exponent", maxIntegerPart)
		}
	}
	if tok.Depth > maxEnclosing {
		return tok, fmt.Errorf("recursion limit exceeded: a value inside more than %d arrays and objects", maxEnclosing)
	}
	return tok, nil
}

// integerPart returns the number of characters of a number literal before
// its fraction or exponent, the sign counted.
func integerPart(literal string) int {
	if i := strings.IndexAny(literal, ".eE"); i >= 0 {
		return i
	}
	return len(literal)
}

// value reads the value whose first token is tok into a slot. An object is
// read against lv when the schema has one for it; any other array or object
// is read to its end.
func (r *reader) value(tok jsonx.Token, lv *level) (slot, error) {
	s := slot{present: true, kind: tok.Kind, text: tok.Text}
	var err error
	switch {
	case tok.Kind == jsonx.TokenBeginObject && lv != nil:
		s.object, err = r.object(lv)
	case tok.Kind == jsonx.TokenBeginObject, tok.Kind == jsonx.TokenBeginArray:
		// The value ends with the token that closes it, which is the next
		// closing token at its own depth.
		for {
			var end jsonx.Token
			if end, err = r.next(); err != nil {
				break
			}
			if (end.Kind == jsonx.TokenEndArray || end.Kind == jsonx.TokenEndObject) && end.Depth == tok.Depth {
				break
			}
		}
	}
	return s, err
}

// object reads the members of an object whose opening token has been read.
// A later member replaces an earlier one of the same name; the earlier
// value has been parsed, and is not validated.
func (r *reader) object(lv *level) (*object, error) {
	obj := &object{slots: make([]slot, len(lv.fields))}
	for {
		name, err := r.next()
		if err != nil {
			return nil, err
		}
		if name.Kind == jsonx.TokenEndObject {
			return obj, nil
		}
		first, err := r.next()
		if err != nil {
			return nil, err
		}
		i, known := lv.index[name.Text]
		var child *level
		if known {
			child = lv.children[i]
		}
		s, err := r.value(first, child)
		if err != nil {
			return nil, err
		}
		switch {
		case known:
			obj.slots[i] = s
		case !lv.ignored(name.Text):
			obj.unknown = append(obj.unknown, name.Text)
		}
	}
}

// Validate checks the text of a model's answer against the schema, as
// upstream's generated pydantic model does with model_validate_json
// (_schema.py:25-28 and :172-199 at v0.2.1; pydantic 2.13.4, pydantic-core
// 2.46.4), and returns one Answer per question, in question order.
//
// The text must be one JSON object whose only member is "answers", an
// object with one member per question, named by the question; in
// Probabilities mode the answer to a Choice or Score question is an object
// with one member per label. Each value is checked strictly:
//
//   - a Noul question in Discrete mode: true or false;
//   - a probability: any JSON number from 0 to 1, an integer literal
//     included; -0 is 0.0, while -0.0 keeps its sign;
//   - a Score question in Discrete mode: an integer literal (1.0 and 1e0
//     are refused) from 0 to one less than the number of criteria;
//   - a Choice question in Discrete mode: a string equal to a label.
//
// Every member is required, in any order. A member name written twice keeps
// its last value, at every level. An unknown member is refused, with one
// exception that upstream's models have: a member named answer_<i> beside
// the answers, or probability_<j> beside the labels, with the index of an
// existing question or label, is ignored whatever its value, unless that
// name is itself a question or a label.
//
// The whole text is parsed before anything is checked, also the values that
// are ignored or replaced, and it is refused as a whole when it is not JSON,
// when a value has more than 200 arrays and objects around it, or when a
// number has more than 4300 characters before its fraction or exponent, the
// sign counted. A text that holds the token NaN, Infinity or -Infinity is
// refused as not JSON; pydantic reads these three as numbers and accepts
// such a text when the token stands in a value it does not check.
//
// The error is a *ValidationError that names every fault.
func (s *Spec) Validate(text string) ([]Answer, error) {
	root, err := s.read(text)
	if err != nil {
		return nil, &ValidationError{Issues: []Issue{{Type: "json_invalid", Message: "Invalid JSON: " + err.Error()}}}
	}
	c := checker{spec: s, answers: make([]Answer, len(s.questions))}
	c.document(root)
	if len(c.issues) > 0 {
		return nil, &ValidationError{Issues: c.issues}
	}
	return c.answers, nil
}

// read parses the whole text and returns its top-level value.
func (s *Spec) read(text string) (slot, error) {
	r := reader{tokens: jsonx.NewTokens([]byte(text))}
	first, err := r.next()
	if err != nil {
		return slot{}, err
	}
	root, err := r.value(first, s.top)
	if err != nil {
		return slot{}, err
	}
	// The reader gives the end of the text, or an error for anything that
	// follows the value.
	if _, err := r.next(); err != nil {
		return slot{}, err
	}
	return root, nil
}

// checker validates what the reader kept.
type checker struct {
	spec    *Spec
	answers []Answer
	issues  []Issue
}

func (c *checker) refuse(kind, message string, loc ...string) {
	c.issues = append(c.issues, Issue{Type: kind, Loc: loc, Message: message})
}

// members reports the object of a slot, after refusing a value that is not
// one and every unknown member of one that is.
func (c *checker) members(s slot, loc ...string) *object {
	if s.object == nil {
		c.refuse("model_type", "Input should be an object", loc...)
		return nil
	}
	for _, name := range s.object.unknown {
		c.refuse("extra_forbidden", "Extra inputs are not permitted", append(slices.Clip(loc), name)...)
	}
	return s.object
}

// document validates the top-level value.
func (c *checker) document(root slot) {
	top := c.members(root)
	if top == nil {
		return
	}
	if !top.slots[0].present {
		c.refuse("missing", "Field required", "answers")
		return
	}
	answers := c.members(top.slots[0], "answers")
	if answers == nil {
		return
	}
	for i := range c.spec.questions {
		q := &c.spec.questions[i]
		s := answers.slots[i]
		if !s.present {
			c.refuse("missing", "Field required", "answers", q.id)
			continue
		}
		c.answer(i, q, s)
	}
}

// answer validates the value of question i.
func (c *checker) answer(i int, q *Question, s slot) {
	a := &c.answers[i]
	loc := []string{"answers", q.id}
	switch {
	case c.spec.mode == Probabilities && q.kind == Noul:
		a.Probability = c.probability(s, loc...)
	case c.spec.mode == Probabilities:
		m := c.members(s, loc...)
		if m == nil {
			return
		}
		a.Probabilities = make([]float64, len(q.labels))
		for j, label := range q.labels {
			if !m.slots[j].present {
				c.refuse("missing", "Field required", "answers", q.id, label)
				continue
			}
			a.Probabilities[j] = c.probability(m.slots[j], "answers", q.id, label)
		}
	case q.kind == Noul:
		if s.kind != jsonx.TokenTrue && s.kind != jsonx.TokenFalse {
			c.refuse("bool_type", "Input should be a valid boolean", loc...)
			return
		}
		a.Bool = s.kind == jsonx.TokenTrue
	case q.kind == Score:
		a.Level = c.scoreLevel(s, len(q.labels), loc...)
	default:
		for _, label := range q.labels {
			if s.kind == jsonx.TokenString && s.text == label {
				a.Label = label
				return
			}
		}
		c.refuse("literal_error", "Input should be one of the labels: "+quoted(q.labels), loc...)
	}
}

// probability validates a number from 0 to 1. An integer literal is taken
// as an integer first, so -0 is +0.0 (jiter number_decoder.rs:203-210,
// pydantic-core src/input/input_json.rs:185); any other spelling is the
// nearest float, an infinity past the largest one, and keeps its sign at
// zero.
func (c *checker) probability(s slot, loc ...string) float64 {
	if s.kind != jsonx.TokenNumber {
		c.refuse("float_type", "Input should be a valid number", loc...)
		return 0
	}
	var f float64
	if s.text != "-0" {
		// A range error carries the infinity the bounds refuse.
		f, _ = strconv.ParseFloat(s.text, 64)
	}
	switch {
	case f < 0:
		c.refuse("greater_than_equal", "Input should be greater than or equal to 0", loc...)
	case f > 1:
		c.refuse("less_than_equal", "Input should be less than or equal to 1", loc...)
	}
	return f
}

// scoreLevel validates an integer literal from 0 to n-1. An integer past 64 bits
// is compared, not refused for its size: it is out of the range on the side
// of its sign.
func (c *checker) scoreLevel(s slot, n int, loc ...string) int {
	if s.kind != jsonx.TokenNumber || integerPart(s.text) != len(s.text) {
		c.refuse("int_type", "Input should be a valid integer", loc...)
		return 0
	}
	v, err := strconv.ParseInt(s.text, 10, 64)
	switch {
	case v < 0, err != nil && s.text[0] == '-':
		c.refuse("greater_than_equal", "Input should be greater than or equal to 0", loc...)
	case err != nil, v >= int64(n):
		c.refuse("less_than", "Input should be less than "+strconv.Itoa(n), loc...)
	}
	return int(v)
}

// quoted returns the labels as a list of quoted strings.
func quoted(labels []string) string {
	q := make([]string, len(labels))
	for i, label := range labels {
		q[i] = strconv.Quote(label)
	}
	return strings.Join(q, ", ")
}
