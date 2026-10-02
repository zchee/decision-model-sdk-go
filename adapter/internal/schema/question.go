// Copyright 2026 The typesafe-sdk-go Authors.
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

// Package schema ports system-one-adapter-python v0.2.1's
// src/system_one_adapter/_schema.py: it reads the questions of a System One
// request as they arrive on the wire, and refuses what upstream's question
// models refuse (ParseQuestions); and it builds from them the answer schema
// of the request in an answer mode (Build), whose text is byte for byte the
// one upstream gives a provider (Spec.Schema).
package schema

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/zchee/typesafe-sdk-go/adapter/internal/jsonx"
)

// Kind is a question's type.
type Kind uint8

// The three question types of the System One API.
const (
	Noul Kind = iota + 1
	Choice
	Score
)

// String returns the type as the wire spells it: noul, choice or score.
func (k Kind) String() string {
	switch k {
	case Noul:
		return "noul"
	case Choice:
		return "choice"
	case Score:
		return "score"
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// minCriteria is the least number of criteria of a Choice or Score question
// (_MIN_CRITERIA, _schema.py:30-32): fewer leave nothing to choose between.
const minCriteria = 2

// noInstructions is the prompt text of a missing or null instruction or
// criterion (_serialize_instruction_value_for_prompt, _schema.py:250-255).
const noInstructions = "No additional instructions."

// The messages of upstream's two ValueErrors (_schema.py:55 and :65), kept
// word for word: they reach the caller of the System One request.
const (
	msgNoQuestions    = "At least one question is required."
	msgFewCriteria    = "Score and choice questions require at least two criteria."
	msgInvalidContent = "must be a string, an object or an array"
)

// ErrNotObject reports a questions value that is not a JSON object. It is a
// fault of the request body, not of a question: upstream's caller hands over
// a mapping, and the HTTP form of that is an object.
var ErrNotObject = errors.New("schema: questions is not a JSON object")

// QuestionError is the refusal of a question set that is an object: the set
// is empty, a question does not have the shape of its type, or a Choice or
// Score question has fewer than two criteria.
type QuestionError struct {
	// Message says what was refused. For an empty set and for too few
	// criteria it is upstream's message word for word.
	Message string
	// Issues names each refused place. It is empty for an empty set.
	Issues []QuestionIssue
}

// QuestionIssue is one refused place of a question set.
type QuestionIssue struct {
	// Loc is the path of the refused value inside the questions object: the
	// question's name, then the member names below it, such as
	// ["answer", "criteria", "true"]. An element of a Score question's
	// criteria is named by its index in decimal.
	Loc []string
	// Message says which rule the value breaks.
	Message string
}

// Error returns the message, followed by each issue as "path: message" when
// the message does not already say it.
func (e *QuestionError) Error() string {
	var b strings.Builder
	b.WriteString("schema: ")
	b.WriteString(e.Message)
	for _, issue := range e.Issues {
		if issue.Message == e.Message {
			continue
		}
		b.WriteString("; ")
		b.WriteString(strings.Join(issue.Loc, "."))
		b.WriteString(": ")
		b.WriteString(issue.Message)
	}
	return b.String()
}

// Question is one question of a request, as upstream's question models hold
// it after validation. ParseQuestions makes it; its zero value is not a
// question.
type Question struct {
	id           string
	kind         Kind
	instructions jsonx.Node
	criteria     jsonx.Node
	// instructionsText is the prompt text of the instructions.
	instructionsText string
	// hasCriteria says that a Noul question carries a criteria object, also
	// an empty one: upstream writes both criteria lines for it.
	hasCriteria bool
	// trueText and falseText are a Noul question's criteria prompt texts.
	trueText, falseText string
	// labels are a Choice question's labels in request order, or a Score
	// question's levels "0", "1", ... in order.
	labels []string
	// criteriaTexts are the prompt texts of the criteria, one per label.
	criteriaTexts []string
}

// ID returns the question's name: the member name of the questions object,
// any text, the empty string included.
func (q *Question) ID() string { return q.id }

// Kind returns the question's type.
func (q *Question) Kind() Kind { return q.kind }

// Instructions returns the instructions as the request gave them: a string,
// an object or an array, or the zero Node, which is null, when the request
// gave null or no instructions.
func (q *Question) Instructions() jsonx.Node { return q.instructions }

// Criteria returns the criteria as the request gave them: for a Noul
// question an object with the members "true" and "false", each optional, or
// the zero Node when the request gave null or no criteria; for a Choice
// question an object from each label to its criterion or null; for a Score
// question an array of one criterion per level.
func (q *Question) Criteria() jsonx.Node { return q.criteria }

// Labels returns the allowed answers of a Choice question, in request order,
// or the levels "0", "1", ... of a Score question; nil for a Noul question.
// The caller does not change the slice.
func (q *Question) Labels() []string { return q.labels }

// ParseQuestions reads the questions member of a System One request, as
// jsonx.Read gives it, into upstream's validated question models
// (convert_question_collection_to_validated_api_question_models,
// _schema.py:35-66): at least one question; each an object whose type is
// noul, choice or score, with no member besides type, instructions and
// criteria; instructions and each criterion a string, an object or an array,
// or null where the type allows it; a Noul question's criteria an object
// with no member besides true and false, or null; a Choice question's
// criteria an object and a Score question's an array, each with at least two
// entries. The order of the questions and of a Choice question's labels is
// the order of the request; a repeated name keeps its first position and its
// last value, as jsonx.Read resolved it.
//
// It refuses with ErrNotObject when questions is not an object, and else
// with a *QuestionError. The shapes of all questions are checked before the
// number of criteria of any, as upstream does. A JSON-valued instruction or
// criterion nested deeper than pydantic-core writes is refused here, where
// upstream fails when it builds the schema.
func ParseQuestions(questions jsonx.Node) ([]Question, error) {
	if questions.Kind() != jsonx.KindObject {
		return nil, ErrNotObject
	}
	if questions.Len() == 0 {
		return nil, &QuestionError{Message: msgNoQuestions}
	}
	out := make([]Question, questions.Len())
	var issues []QuestionIssue
	for i := range out {
		p := parser{loc: []string{questions.Name(i)}}
		out[i] = p.question(questions.Name(i), questions.Index(i))
		issues = append(issues, p.issues...)
	}
	if len(issues) > 0 {
		return nil, &QuestionError{Message: "a question does not have the shape of its type", Issues: issues}
	}
	for i := range out {
		if q := &out[i]; q.kind != Noul && len(q.labels) < minCriteria {
			return nil, &QuestionError{
				Message: msgFewCriteria,
				Issues:  []QuestionIssue{{Loc: []string{q.id, "criteria"}, Message: msgFewCriteria}},
			}
		}
	}
	return out, nil
}

// parser reads one question and collects what it refuses.
type parser struct {
	// loc is the path of the value being read.
	loc    []string
	issues []QuestionIssue
}

// refuse records an issue at the current path followed by more.
func (p *parser) refuse(message string, more ...string) {
	loc := make([]string, 0, len(p.loc)+len(more))
	p.issues = append(p.issues, QuestionIssue{Loc: append(append(loc, p.loc...), more...), Message: message})
}

// question reads the question named id. What it returns is used only when
// the parser recorded no issue.
func (p *parser) question(id string, v jsonx.Node) Question {
	q := Question{id: id}
	if v.Kind() != jsonx.KindObject {
		p.refuse("a question must be an object")
		return q
	}
	typ, ok := v.Member("type")
	switch {
	case !ok:
		p.refuse("the member type is required: noul, choice or score")
		return q
	case typ.Kind() != jsonx.KindString:
		p.refuse("must be noul, choice or score", "type")
		return q
	}
	switch typ.Text() {
	case "noul":
		q.kind = Noul
	case "choice":
		q.kind = Choice
	case "score":
		q.kind = Score
	default:
		p.refuse("must be noul, choice or score", "type")
		return q
	}
	for i := range v.Len() {
		switch name := v.Name(i); name {
		case "type", "instructions", "criteria":
		default:
			p.refuse("a "+q.kind.String()+" question has no such member", name)
		}
	}
	if instructions, ok := v.Member("instructions"); ok && instructions.Kind() != jsonx.KindNull {
		q.instructions = instructions
	}
	q.instructionsText = p.promptText(q.instructions, true, "instructions")

	criteria, ok := v.Member("criteria")
	switch q.kind {
	case Noul:
		p.noulCriteria(&q, criteria)
	case Choice:
		p.choiceCriteria(&q, criteria, ok)
	default:
		p.scoreCriteria(&q, criteria, ok)
	}
	return q
}

// noulCriteria reads a Noul question's criteria: null or absent, or an
// object whose members are true and false, each optional and each null or a
// content value.
func (p *parser) noulCriteria(q *Question, criteria jsonx.Node) {
	switch criteria.Kind() {
	case jsonx.KindNull:
		return
	case jsonx.KindObject:
	default:
		p.refuse("must be an object with the members true and false, or null", "criteria")
		return
	}
	q.criteria, q.hasCriteria = criteria, true
	q.trueText, q.falseText = noInstructions, noInstructions
	for i := range criteria.Len() {
		switch name := criteria.Name(i); name {
		case "true":
			q.trueText = p.promptText(criteria.Index(i), true, "criteria", name)
		case "false":
			q.falseText = p.promptText(criteria.Index(i), true, "criteria", name)
		default:
			p.refuse("a noul question's criteria have no such member", "criteria", name)
		}
	}
}

// choiceCriteria reads a Choice question's criteria: an object from each
// label to null or a content value.
func (p *parser) choiceCriteria(q *Question, criteria jsonx.Node, present bool) {
	if !present {
		p.refuse("a choice question requires criteria", "criteria")
		return
	}
	if criteria.Kind() != jsonx.KindObject {
		p.refuse("must be an object from each label to its criterion", "criteria")
		return
	}
	q.criteria = criteria
	q.labels = make([]string, criteria.Len())
	q.criteriaTexts = make([]string, criteria.Len())
	for i := range q.labels {
		q.labels[i] = criteria.Name(i)
		q.criteriaTexts[i] = p.promptText(criteria.Index(i), true, "criteria", q.labels[i])
	}
}

// scoreCriteria reads a Score question's criteria: an array of content
// values, one per level from zero; null is not one.
func (p *parser) scoreCriteria(q *Question, criteria jsonx.Node, present bool) {
	if !present {
		p.refuse("a score question requires criteria", "criteria")
		return
	}
	if criteria.Kind() != jsonx.KindArray {
		p.refuse("must be an array of one criterion per level", "criteria")
		return
	}
	q.criteria = criteria
	q.labels = make([]string, criteria.Len())
	q.criteriaTexts = make([]string, criteria.Len())
	for i := range q.labels {
		q.labels[i] = strconv.Itoa(i)
		q.criteriaTexts[i] = p.promptText(criteria.Index(i), false, "criteria", q.labels[i])
	}
}

// promptText ports _serialize_instruction_value_for_prompt
// (_schema.py:250-255) for a content value at the path more below the
// question: a string as it is, an object or an array as pydantic-core's
// to_json writes it, and null, where nullable allows it, as the fixed text
// for no instructions. It records an issue for any other value.
func (p *parser) promptText(v jsonx.Node, nullable bool, more ...string) string {
	switch v.Kind() {
	case jsonx.KindString:
		return v.Text()
	case jsonx.KindObject, jsonx.KindArray:
		text, err := v.PydanticJSON()
		if err != nil {
			p.refuse(fmt.Sprintf("cannot be written into a prompt: %v", err), more...)
		}
		return string(text)
	case jsonx.KindNull:
		if nullable {
			return noInstructions
		}
	}
	p.refuse(msgInvalidContent, more...)
	return ""
}
