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

package schema

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/zchee/typesafe-sdk-go/adapter/internal/jsonx"
)

// Mode is the answer mode of a request (AnswerMode,
// _utils/probability_normalization.py at v0.2.1).
type Mode uint8

// The two answer modes.
const (
	// Probabilities asks for a probability per Noul question and a
	// probability per label of a Choice or Score question.
	Probabilities Mode = iota + 1
	// Discrete asks for one allowed value per question.
	Discrete
)

// String returns the mode as upstream spells it: probabilities or discrete.
func (m Mode) String() string {
	switch m {
	case Probabilities:
		return "probabilities"
	case Discrete:
		return "discrete"
	}
	return "Mode(" + strconv.Itoa(int(m)) + ")"
}

// answersDoc is the description of the answers model
// (create_llm_output_model, _schema.py:106).
const answersDoc = "Exactly one answer per property below. Use these property names verbatim and do not add, rename, or nest them under any other key."

// The names upstream gives the models it generates.
const (
	answersModel        = "TypeSafeAnswers"
	probabilityMapModel = "ProbabilityMap"
	defsPrefix          = "#/$defs/"
)

// Spec is the answer schema of one request: the schema a provider is given
// and the validator of what the model returns. Build makes it; it is not
// changed afterwards and is safe for concurrent use.
type Spec struct {
	questions []Question
	mode      Mode
	schema    []byte
}

// Build makes the Spec of a question set that ParseQuestions returned, in
// the given answer mode (create_llm_output_model and
// create_raw_output_schema, _schema.py:69-125).
//
// It refuses an empty set, a mode that is neither Probabilities nor
// Discrete, a Question that ParseQuestions did not make, and two questions
// of one name.
func Build(questions []Question, mode Mode) (*Spec, error) {
	if len(questions) == 0 {
		return nil, errors.New("schema: no question to build a schema for")
	}
	if mode != Probabilities && mode != Discrete {
		return nil, fmt.Errorf("schema: unknown answer mode %v", mode)
	}
	for i := range questions {
		if k := questions[i].kind; k != Noul && k != Choice && k != Score {
			return nil, fmt.Errorf("schema: question %d was not made by ParseQuestions", i)
		}
	}
	root := rootNode(questions, mode)
	text, err := jsonx.Marshal(root.value())
	if err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &Spec{questions: slices.Clone(questions), mode: mode, schema: text}, nil
}

// Schema returns the schema as compact JSON in pydantic's order, the bytes
// of pydantic_core.to_json(create_raw_output_schema(model)): the text
// upstream puts into the system prompt of a prompted request
// (_client.py:441) and, as a JSON value, the schema of a native request.
//
// The order is that of pydantic 2.13.4's GenerateJsonSchema.sort
// (json_schema.py:590-613): the keywords of every schema object sorted by
// name; the members of "$defs" sorted by name, as Python compares strings,
// so ProbabilityMap10 comes before ProbabilityMap2; the members of
// "properties" in question order and label order; and the keywords of the
// schema of a property that is itself named "properties" or "default" in
// the order pydantic generated them, which the sort leaves alone. The
// keywords upstream removes for the providers (title, minimum, maximum,
// exclusiveMinimum, exclusiveMaximum; _schema.py:133) are never written.
//
// Each call returns a copy of its own.
func (s *Spec) Schema() []byte { return bytes.Clone(s.schema) }

// Mode returns the answer mode the Spec was built for.
func (s *Spec) Mode() Mode { return s.mode }

// node is one JSON Schema object of the closed keyword set upstream sends:
// "$defs", "$ref", "additionalProperties", "description", "enum",
// "properties", "required", "type". A zero field is not written.
type node struct {
	defs        []property
	ref         string
	closed      bool // "additionalProperties": false
	description *string
	enum        []string
	properties  []property
	typ         string
	// generationOrder says that this node is the value of a property named
	// "properties" or "default": pydantic's sort leaves the keys of such a
	// value in the order its generator made them (json_schema.py:598,609),
	// which is the type's own keywords first ("enum" before "type") and
	// "description" last.
	generationOrder bool
}

// property is a named schema: a member of "properties" or of "$defs".
type property struct {
	name   string
	schema node
}

func newProperty(name string, schema node) property {
	schema.generationOrder = name == "properties" || name == "default"
	return property{name: name, schema: schema}
}

// rootNode builds the schema of the whole answer: the root object, the
// answers model with one property per question in request order, and one
// probability map model per Choice or Score question in Probabilities mode.
func rootNode(questions []Question, mode Mode) node {
	answers := node{closed: true, description: new(answersDoc), typ: "object"}
	defs := make([]property, 0, len(questions)+1)
	for index := range questions {
		q := &questions[index]
		var schema node
		if mode == Probabilities && q.kind != Noul {
			name := probabilityMapModel + strconv.Itoa(index)
			defs = append(defs, property{name: name, schema: probabilityMap(q, mode)})
			// The reference site stays bare: a provider may drop what
			// stands beside "$ref" (_schema.py:94-99).
			schema = node{ref: defsPrefix + name}
		} else {
			schema = answerType(q, mode)
			schema.description = new(fieldDescription(q, mode))
		}
		answers.properties = append(answers.properties, newProperty(q.id, schema))
	}
	defs = append(defs, property{name: answersModel, schema: answers})
	// Python compares strings by code point, which is the byte order of
	// UTF-8.
	slices.SortFunc(defs, func(a, b property) int { return strings.Compare(a.name, b.name) })
	return node{
		defs:       defs,
		closed:     true,
		properties: []property{newProperty("answers", node{ref: defsPrefix + answersModel})},
		typ:        "object",
	}
}

// answerType ports the scalar branches of
// _create_llm_answer_type_for_question (_schema.py:172-183).
func answerType(q *Question, mode Mode) node {
	switch {
	case q.kind == Noul && mode == Discrete:
		return node{typ: "boolean"}
	case q.kind == Noul:
		return node{typ: "number"}
	case q.kind == Score:
		return node{typ: "integer"}
	default:
		return node{enum: q.labels, typ: "string"}
	}
}

// probabilityMap ports the model branch (_schema.py:186-199): one number
// property per label, described by the label's criterion. The model's own
// description is its docstring, which pydantic passes through
// inspect.cleandoc (json_schema.py:1664-1665); a field's description is
// used as it is.
func probabilityMap(q *Question, mode Mode) node {
	m := node{closed: true, description: new(cleandoc(questionDescription(q, mode))), typ: "object"}
	for i, label := range q.labels {
		m.properties = append(m.properties, newProperty(label, node{description: new(q.criteriaTexts[i]), typ: "number"}))
	}
	return m
}

// value returns the node as a JSON value whose members are in the order
// Schema documents.
func (n *node) value() jsonx.Value {
	var members []jsonx.Member
	add := func(name string, v jsonx.Value) {
		members = append(members, jsonx.Member{Name: name, Value: v})
	}
	ref := func() {
		if n.ref != "" {
			add("$ref", jsonx.String(n.ref))
		}
	}
	description := func() {
		if n.description != nil {
			add("description", jsonx.String(*n.description))
		}
	}
	enum := func() {
		if n.enum != nil {
			add("enum", stringArray(n.enum))
		}
	}
	typ := func() {
		if n.typ != "" {
			add("type", jsonx.String(n.typ))
		}
	}
	if n.generationOrder {
		ref()
		enum()
		typ()
		description()
		return jsonx.Object(members...)
	}
	if len(n.defs) > 0 {
		add("$defs", propertiesValue(n.defs))
	}
	ref()
	if n.closed {
		add("additionalProperties", jsonx.Bool(false))
	}
	description()
	enum()
	if n.typ == "object" {
		// Every property is required, in the order of the properties.
		names := make([]string, len(n.properties))
		for i := range n.properties {
			names[i] = n.properties[i].name
		}
		add("properties", propertiesValue(n.properties))
		add("required", stringArray(names))
	}
	typ()
	return jsonx.Object(members...)
}

func propertiesValue(properties []property) jsonx.Value {
	members := make([]jsonx.Member, len(properties))
	for i := range properties {
		members[i] = jsonx.Member{Name: properties[i].name, Value: properties[i].schema.value()}
	}
	return jsonx.Object(members...)
}

func stringArray(items []string) jsonx.Value {
	values := make([]jsonx.Value, len(items))
	for i, item := range items {
		values[i] = jsonx.String(item)
	}
	return jsonx.Array(values...)
}

// questionDescription ports _build_llm_output_question_description
// (_schema.py:232-247).
func questionDescription(q *Question, mode Mode) string {
	if mode != Probabilities {
		return q.instructionsText
	}
	switch q.kind {
	case Noul:
		return "Probability that the answer is yes or the assertion is true. " +
			"0 means no or false, 0.5 means uncertain, and 1 means yes or true.\n" +
			"Question: " + q.instructionsText
	case Score:
		return "Each property maps a rubric level to the probability that the document matches it.\nQuestion: " + q.instructionsText
	default:
		return "Each property maps an option to the probability that it is the best answer.\nQuestion: " + q.instructionsText
	}
}

// fieldDescription ports _build_llm_output_field_description
// (_schema.py:202-229).
func fieldDescription(q *Question, mode Mode) string {
	description := questionDescription(q, mode)
	if q.kind == Noul {
		if !q.hasCriteria {
			return description
		}
		return description + "\nTrue criteria: " + q.trueText + "\nFalse criteria: " + q.falseText
	}
	header := "\nRequired probability keys:\n"
	switch {
	case mode != Discrete:
	case q.kind == Score:
		header = "\nScore levels, answer with the integer:\n"
	default:
		header = "\nChoice labels, answer with one label:\n"
	}
	lines := make([]string, len(q.labels))
	for i, label := range q.labels {
		lines[i] = label + " = " + q.criteriaTexts[i]
	}
	return description + header + strings.Join(lines, "\n")
}

// cleandoc ports CPython 3.14.3's inspect.cleandoc (Lib/inspect.py:790-815):
// tabs expanded, the leading spaces of the first line and the common margin
// of the later lines removed, and empty lines dropped at the end and at the
// start.
func cleandoc(doc string) string {
	lines := strings.Split(expandTabs(doc), "\n")
	margin := -1
	for _, line := range lines[1:] {
		content := len(strings.TrimLeft(line, " "))
		if content == 0 {
			continue
		}
		if indent := len(line) - content; margin < 0 || indent < margin {
			margin = indent
		}
	}
	lines[0] = strings.TrimLeft(lines[0], " ")
	if margin >= 0 {
		for i := 1; i < len(lines); i++ {
			// The margin counts spaces, one byte each, and Python's slice
			// past the end of a shorter line, which holds spaces only,
			// gives "".
			lines[i] = lines[i][min(margin, len(lines[i])):]
		}
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

// expandTabs ports str.expandtabs() with its default tab size of 8: the
// column counts code points and starts again after '\n' and '\r'.
func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	column := 0
	for _, r := range s {
		switch r {
		case '\t':
			n := 8 - column%8
			b.WriteString(strings.Repeat(" ", n))
			column += n
		case '\n', '\r':
			b.WriteRune(r)
			column = 0
		default:
			b.WriteRune(r)
			column++
		}
	}
	return b.String()
}
