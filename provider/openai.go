// Copyright 2026 The decision-model-sdk-go Authors.
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

package provider

import (
	"encoding/json/jsontext"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"

	decision "github.com/zchee/decision-model-sdk-go"
)

// openAIName names the OpenAI provider, in its preset and in the
// [decision.DialectError]s of its dialect.
const openAIName = "OpenAI"

// OpenAI returns the provider of OpenAI's decisions API:
// https://api.openai.com, POST /v1/decisions, with the key read from
// OPENAI_API_KEY and gpt-6-luna as the model of a call that names none
// ([decision.WithModel] and [decision.Model] still win).
//
// Its wire is not System One's, and the preset's [decision.Dialect]
// rewrites each call. The request becomes {"model", "input", "questions"}:
// input is the state, its text when the state is text and its compact JSON
// otherwise; each question is {"type", "name", "instructions", ...} with
// type predicate for a yes/no question, choice with "choices" of
// {"value", "description"} for a choice, and score with "levels" of
// {"label"} for a score. The answers come back as an array of
// {"type", "name", ...} with probabilities as arrays of objects; the
// dialect reads them back as the System One response, the score's legend
// taken from the request's levels, so [decision.Ask], [decision.DecodeAs]
// and Answers work as on any other provider. A response's
// [decision.ResponseMeta.RawBody] is that System One body. The usage keeps
// input_tokens and output_tokens.
//
// The dialect refuses, before anything is sent and with a
// [*decision.DialectError], a call the vendor's wire cannot express: a
// question added with [decision.Questions.Raw]; instructions, a choice's
// option description or a score level given as [decision.JSON] content; a
// yes/no question with a Yes or a No outcome, which a predicate has no room
// for. A member [decision.ExtraBody] adds is copied to the top level of the
// request, where OpenAI refuses one it does not know with status 400
// (unknown_parameter).
//
// Its errors are {"error":{"message","type","param","code"}}, whose type is
// invalid_request_error even for a wrong key; an [*decision.APIError] keeps
// that body, takes its Message from error.message, and has the ErrorType
// authentication_error for 401 and api_usage_error for 400 and 404 (an
// unknown model). The message of a 401 for a wrong key repeats the key's
// first and last characters. It sends x-request-id on every response.
//
// Its GET /v1/models lists every model of the account, not only those its
// decisions API serves, in a shape that is not System One's, so ModelsPath
// is empty and [decision.Models.List] on its client fails with a
// [*decision.ConfigError] before anything is sent.
func OpenAI() decision.Provider {
	return decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
		Name:          openAIName,
		BaseURL:       "https://api.openai.com",
		SystemOnePath: "/v1/decisions",
		APIKeyEnv:     "OPENAI_API_KEY",
		DefaultModel:  "gpt-6-luna",
		Dialect:       openAIDialect{},
	}
}

// openAIDialect is OpenAI's [decision.Dialect].
type openAIDialect struct{}

// The names OpenAI's wire gives the three kinds of question.
const (
	openAIPredicate = "predicate"
	openAIChoice    = "choice"
	openAIScore     = "score"
)

// openAIQuestion is one question as OpenAI's request writes it.
type openAIQuestion struct {
	typ, name    string
	instructions *string
	choices      []openAIOption // a choice's options
	levels       []string       // a score's levels
}

// openAIOption is one option of a choice question.
type openAIOption struct {
	value       string
	description *string
}

// openAIMember is a top-level member of the System One body that the dialect
// copies as it is: one that [decision.ExtraBody] added.
type openAIMember struct {
	name  string
	value jsontext.Value
}

// Request writes OpenAI's request from the System One body and returns the
// ResponseFunc that reads the answers back for questions.
func (openAIDialect) Request(body []byte, questions []decision.PreparedQuestion) ([]byte, decision.ResponseFunc, error) {
	out, err := openAIRequest(body, questions)
	if err != nil {
		return nil, nil, err
	}
	return out, openAIResponse(questions), nil
}

// refuse returns the DialectError of a call OpenAI's wire cannot express.
func refuse(reason string) error { return decision.NewDialectError(openAIName, reason) }

// openAIRequest reads the System One body, {"state", "model", "questions"}
// and the members ExtraBody added, in one pass, and writes OpenAI's request
// in a new slice: model, input and questions, in that order, then the added
// members as they are. questions is the set whose bytes the body carries,
// in the same order.
func openAIRequest(body []byte, questions []decision.PreparedQuestion) ([]byte, error) {
	var (
		model    jsontext.Value
		input    string
		hasState bool
		qs       []openAIQuestion
		extra    []openAIMember
	)
	dec := newDecoder(body)
	err := members(dec, "", func(name string) error {
		switch name {
		case "model":
			var err error
			model, err = readValue(dec)
			return err
		case "state":
			v, err := readValue(dec)
			if err != nil {
				return err
			}
			hasState = true
			if v.Kind() == '"' {
				input = unquote(v)
				return nil
			}
			if err := v.Compact(); err != nil {
				return err
			}
			input = string(v)
		case "questions":
			var err error
			qs, err = readQuestions(dec, questions)
			return err
		default:
			if name == "input" {
				return refuse(`a member named "input" was added to the request body, a member the dialect writes itself`)
			}
			v, err := readValue(dec)
			if err != nil {
				return err
			}
			extra = append(extra, openAIMember{name: name, value: v})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !hasState {
		return nil, errors.New("the System One body has no state")
	}
	e := newEncoder()
	e.token(jsontext.BeginObject)
	if model != nil {
		e.str("model")
		if model.Kind() == '"' {
			e.str(unquote(model))
		} else {
			// An ExtraBody member named "model" that is not a string.
			e.value(model)
		}
	}
	e.str("input")
	e.str(input)
	e.str("questions")
	e.token(jsontext.BeginArray)
	for i := range qs {
		qs[i].write(e)
	}
	e.token(jsontext.EndArray)
	for _, m := range extra {
		e.str(m.name)
		e.value(m.value)
	}
	e.token(jsontext.EndObject)
	return e.bytes()
}

// write writes q as OpenAI's request names its members: type, name,
// instructions, then choices or levels.
func (q *openAIQuestion) write(e *encoder) {
	e.token(jsontext.BeginObject)
	e.str("type")
	e.str(q.typ)
	e.str("name")
	e.str(q.name)
	if q.instructions != nil {
		e.str("instructions")
		e.str(*q.instructions)
	}
	switch q.typ {
	case openAIChoice:
		e.str("choices")
		e.token(jsontext.BeginArray)
		for _, o := range q.choices {
			e.token(jsontext.BeginObject)
			e.str("value")
			e.str(o.value)
			if o.description != nil {
				e.str("description")
				e.str(*o.description)
			}
			e.token(jsontext.EndObject)
		}
		e.token(jsontext.EndArray)
	case openAIScore:
		e.str("levels")
		e.token(jsontext.BeginArray)
		for _, l := range q.levels {
			e.token(jsontext.BeginObject)
			e.str("label")
			e.str(l)
			e.token(jsontext.EndObject)
		}
		e.token(jsontext.EndArray)
	}
	e.token(jsontext.EndObject)
}

// readQuestions reads the body's questions object, whose members are the
// questions of the prepared set in order, and returns them as OpenAI's
// request writes them, or the DialectError of the first question the wire
// cannot express.
func readQuestions(dec *jsontext.Decoder, questions []decision.PreparedQuestion) ([]openAIQuestion, error) {
	qs := make([]openAIQuestion, 0, len(questions))
	err := members(dec, "questions", func(name string) error {
		i := len(qs)
		if i >= len(questions) || questions[i].Name != name {
			return errQuestionSet
		}
		q, err := readQuestion(dec, &questions[i])
		if err != nil {
			return err
		}
		qs = append(qs, q)
		return nil
	})
	if err == nil && len(qs) != len(questions) {
		err = errQuestionSet
	}
	return qs, err
}

// errQuestionSet reports a body whose questions are not the prepared set's,
// which only an ExtraBody member named "questions" can cause.
var errQuestionSet = errors.New(`the request body's questions are not the prepared set's: an ExtraBody member named "questions" replaced them`)

// readQuestion reads one question object of the body, the question pq of
// the prepared set, and returns it as OpenAI's request writes it.
func readQuestion(dec *jsontext.Decoder, pq *decision.PreparedQuestion) (openAIQuestion, error) {
	q := openAIQuestion{name: pq.Name}
	switch {
	case pq.Raw:
		return q, refuse(`question ` + strconv.Quote(pq.Name) + ` was added with Questions.Raw, whose fields OpenAI's wire has no place for; build it as a Noul, a Choice or a Score`)
	case pq.Kind == decision.KindNoul:
		q.typ = openAIPredicate
	case pq.Kind == decision.KindChoice:
		q.typ = openAIChoice
	case pq.Kind == decision.KindScore:
		q.typ = openAIScore
		for i, l := range pq.Levels {
			if l.IsJSON() {
				return q, refuse(`level ` + strconv.Itoa(i) + ` of score question ` + strconv.Quote(pq.Name) + ` is JSON content; OpenAI's levels take text`)
			}
			q.levels = append(q.levels, l.Text())
		}
	}
	err := members(dec, "", func(name string) error {
		switch name {
		case "instructions":
			v, err := readValue(dec)
			if err != nil {
				return err
			}
			if v.Kind() != '"' {
				return refuse(`question ` + strconv.Quote(pq.Name) + ` gives its instructions as JSON content; OpenAI's instructions take text`)
			}
			s := unquote(v)
			q.instructions = &s
		case "criteria":
			switch q.typ {
			case openAIPredicate:
				return refuse(`yes/no question ` + strconv.Quote(pq.Name) + ` describes its outcomes (Yes or No); OpenAI's predicate takes no outcomes`)
			case openAIChoice:
				return members(dec, "", func(label string) error {
					v, err := readValue(dec)
					if err != nil {
						return err
					}
					o := openAIOption{value: label}
					switch v.Kind() {
					case 'n':
					case '"':
						d := unquote(v)
						o.description = &d
					default:
						return refuse(`option ` + strconv.Quote(label) + ` of choice question ` + strconv.Quote(pq.Name) + ` is described with JSON content; OpenAI's descriptions take text`)
					}
					q.choices = append(q.choices, o)
					return nil
				})
			}
			// A score's levels come from the prepared set.
			return bodyError(dec.SkipValue())
		default:
			return bodyError(dec.SkipValue())
		}
		return nil
	})
	return q, err
}

// The reasons of the failures of an answer that does not fit the request.
var (
	errNotAsked  = errors.New("names no question the request asked")
	errRepeated  = errors.New("repeats an earlier answer's")
	errWrongKind = errors.New("is not the kind of the question it names")
	errNotLabel  = errors.New("is not an option label of the question")
	errNotLevel  = errors.New("is not a level of the question")
)

// openAIAnswer holds the members of one answer that the translation reads,
// each nil when absent.
type openAIAnswer struct {
	typ, name, probability, choice, confidence, score, probabilities jsontext.Value
}

// openAIResponse returns the ResponseFunc of a call that asked questions: a
// 2xx body is written as the System One response, and any other status is
// given its System One error_type, with the body kept.
func openAIResponse(questions []decision.PreparedQuestion) decision.ResponseFunc {
	return func(status int, _ http.Header, body []byte) (decision.Translated, error) {
		if !success(status) {
			return decision.Translated{ErrorType: errorType(status, http.StatusBadRequest, http.StatusNotFound)}, nil
		}
		out, err := openAIResult(body, questions)
		if err != nil {
			return decision.Translated{}, err
		}
		return decision.Translated{Body: out}, nil
	}
}

// openAIResult reads OpenAI's response, {"model", "answers": [...],
// "usage"}, and writes the System One response in a new slice. Unknown
// members are ignored at every level; a missing member, one of the wrong
// kind, or an answer that does not fit the request is a
// [decision.ResponsePathError] at the member's place in OpenAI's body. Once
// the body has been read, the top-level members are checked in the order
// model, usage, answers, then the answers in order.
func openAIResult(body []byte, questions []decision.PreparedQuestion) ([]byte, error) {
	var (
		model, usageIn, usageOut jsontext.Value
		answers                  []openAIAnswer
		hasAnswers, hasUsage     bool
	)
	dec := newDecoder(body)
	err := members(dec, "", func(name string) error {
		switch name {
		case "model":
			v, err := readValue(dec)
			model = v
			return err
		case "usage":
			hasUsage = true
			usageIn, usageOut = nil, nil
			return members(dec, "usage", func(name string) error {
				var err error
				switch name {
				case "input_tokens":
					usageIn, err = readValue(dec)
				case "output_tokens":
					usageOut, err = readValue(dec)
				default:
					err = bodyError(dec.SkipValue())
				}
				return err
			})
		case "answers":
			hasAnswers = true
			answers = answers[:0]
			return elements(dec, "answers", func(i int) error {
				var a openAIAnswer
				err := members(dec, index("answers", i), func(name string) error {
					var err error
					switch name {
					case "type":
						a.typ, err = readValue(dec)
					case "name":
						a.name, err = readValue(dec)
					case "probability":
						a.probability, err = readValue(dec)
					case "choice":
						a.choice, err = readValue(dec)
					case "confidence":
						a.confidence, err = readValue(dec)
					case "score":
						a.score, err = readValue(dec)
					case "probabilities":
						a.probabilities, err = readValue(dec)
					default:
						err = bodyError(dec.SkipValue())
					}
					return err
				})
				answers = append(answers, a)
				return err
			})
		default:
			return bodyError(dec.SkipValue())
		}
	})
	if err == nil {
		err = atEnd(dec)
	}
	if err != nil {
		return nil, err
	}
	modelName, err := stringAt(model, "model")
	if err != nil {
		return nil, err
	}
	switch {
	case !hasUsage:
		return nil, pathError("usage", errMissing)
	case !hasAnswers:
		return nil, pathError("answers", errMissing)
	}

	e := newEncoder()
	e.token(jsontext.BeginObject)
	e.str("model")
	e.str(modelName)
	e.str("answers")
	e.token(jsontext.BeginObject)
	seen := make([]bool, len(questions))
	for i := range answers {
		if err := answers[i].write(e, index("answers", i), questions, seen); err != nil {
			return nil, err
		}
	}
	e.token(jsontext.EndObject)
	e.str("usage")
	e.token(jsontext.BeginObject)
	if usageIn != nil {
		e.str("input_tokens")
		e.value(usageIn)
	}
	if usageOut != nil {
		e.str("output_tokens")
		e.value(usageOut)
	}
	e.token(jsontext.EndObject)
	e.token(jsontext.EndObject)
	return e.bytes()
}

// write writes a as a member of the System One answers object, the answer
// at path in OpenAI's body: its name, then the answer of its kind. seen
// marks the questions already answered.
func (a *openAIAnswer) write(e *encoder, path string, questions []decision.PreparedQuestion, seen []bool) error {
	typ, err := stringAt(a.typ, path+".type")
	if err != nil {
		return err
	}
	name, err := stringAt(a.name, path+".name")
	if err != nil {
		return err
	}
	qi := -1
	for i := range questions {
		if questions[i].Name == name {
			qi = i
			break
		}
	}
	switch {
	case qi < 0:
		return pathError(path+".name", errNotAsked)
	case seen[qi]:
		return pathError(path+".name", errRepeated)
	}
	seen[qi] = true
	q := &questions[qi]
	var kind decision.AnswerKind
	switch typ {
	case openAIPredicate:
		kind = decision.KindNoul
	case openAIChoice:
		kind = decision.KindChoice
	case openAIScore:
		kind = decision.KindScore
	}
	if kind == 0 || kind != q.Kind {
		return pathError(path+".type", errWrongKind)
	}
	e.str(name)
	e.token(jsontext.BeginObject)
	e.str("type")
	e.str(kind.String())
	switch kind {
	case decision.KindNoul:
		p, err := numberAt(a.probability, path+".probability")
		if err != nil {
			return err
		}
		e.str("noul")
		e.value(p)
	case decision.KindChoice:
		if err := a.writeChoice(e, path, q); err != nil {
			return err
		}
	case decision.KindScore:
		if err := a.writeScore(e, path, q); err != nil {
			return err
		}
	}
	e.token(jsontext.EndObject)
	return nil
}

// writeChoice writes the members of a choice answer: the option chosen, the
// confidence and the probabilities keyed by option label.
func (a *openAIAnswer) writeChoice(e *encoder, path string, q *decision.PreparedQuestion) error {
	choice, err := stringAt(a.choice, path+".choice")
	if err != nil {
		return err
	}
	confidence, err := numberAt(a.confidence, path+".confidence")
	if err != nil {
		return err
	}
	probs, err := readProbabilities(a.probabilities, path+".probabilities", func(v jsontext.Value, at string) (string, error) {
		label, err := stringAt(v, at)
		if err != nil {
			return "", err
		}
		if slices.Contains(q.Options, label) {
			return label, nil
		}
		return "", pathError(at, errNotLabel)
	})
	if err != nil {
		return err
	}
	e.str("choice")
	e.str(choice)
	e.str("confidence")
	e.value(confidence)
	writeProbabilities(e, probs)
	return nil
}

// writeScore writes the members of a score answer: the score, the
// confidence, the legend of the request's levels by index, and the
// probabilities keyed by level.
func (a *openAIAnswer) writeScore(e *encoder, path string, q *decision.PreparedQuestion) error {
	score, err := numberAt(a.score, path+".score")
	if err != nil {
		return err
	}
	confidence, err := numberAt(a.confidence, path+".confidence")
	if err != nil {
		return err
	}
	probs, err := readProbabilities(a.probabilities, path+".probabilities", func(v jsontext.Value, at string) (string, error) {
		n, err := numberAt(v, at)
		if err != nil {
			return "", err
		}
		f, err := strconv.ParseFloat(string(n), 64)
		if err != nil || f != math.Trunc(f) || f < 0 || f >= float64(len(q.Levels)) {
			return "", pathError(at, errNotLevel)
		}
		return strconv.Itoa(int(f)), nil
	})
	if err != nil {
		return err
	}
	e.str("score")
	e.value(score)
	e.str("confidence")
	e.value(confidence)
	e.str("legend")
	e.token(jsontext.BeginObject)
	for i, l := range q.Levels {
		e.str(strconv.Itoa(i))
		e.str(l.Text())
	}
	e.token(jsontext.EndObject)
	writeProbabilities(e, probs)
	return nil
}

// probability is one entry of an answer's probabilities: the System One key
// it is written under and the probability as the vendor spelled it.
type probability struct {
	key   string
	value jsontext.Value
}

// readProbabilities reads v, the array of {"value", "probability"} objects
// at path, with key turning each value, at its place, into the System One
// key it is written under. Members other than value and probability, such
// as a score's label, are ignored; a key that repeats an earlier one is a
// failure at its value.
func readProbabilities(v jsontext.Value, path string, key func(v jsontext.Value, at string) (string, error)) ([]probability, error) {
	if v == nil {
		return nil, pathError(path, errMissing)
	}
	var probs []probability
	dec := newDecoder(v)
	err := elements(dec, path, func(i int) error {
		at := index(path, i)
		var value, p jsontext.Value
		err := members(dec, at, func(name string) error {
			var err error
			switch name {
			case "value":
				value, err = readValue(dec)
			case "probability":
				p, err = readValue(dec)
			default:
				err = bodyError(dec.SkipValue())
			}
			return err
		})
		if err != nil {
			return err
		}
		k, err := key(value, at+".value")
		if err != nil {
			return err
		}
		for _, earlier := range probs {
			if earlier.key == k {
				return pathError(at+".value", errRepeated)
			}
		}
		if p, err = numberAt(p, at+".probability"); err != nil {
			return err
		}
		probs = append(probs, probability{key: k, value: p})
		return nil
	})
	return probs, err
}

// writeProbabilities writes the probabilities member, an object keyed as
// System One keys it.
func writeProbabilities(e *encoder, probs []probability) {
	e.str("probabilities")
	e.token(jsontext.BeginObject)
	for _, p := range probs {
		e.str(p.key)
		e.value(p.value)
	}
	e.token(jsontext.EndObject)
}
