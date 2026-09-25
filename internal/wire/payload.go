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
	"slices"
	"strconv"
)

// The payload writer turns decoded response values back into JSON, laid out
// as typesafe-sdk-python 0.7.1's model_dump_json writes its response models
// (pydantic-core 2.46.5's to_json): members in the models' field order,
// strings through [AppendString], floats through appendFloat (zmij's layout:
// 0.0, 1.0, 0.98, 1e-7, 1e+20), an absent token count as null, score levels
// as decimal member names ("0", "1"). No whitespace is written.
//
// A structured legend level is spliced as the bytes the response carried
// (ruling R73): the Python SDK writes the value it parsed, so a level whose
// received bytes hold an escape or a number spelling other than its own
// (\u0061 for a, 1E2 for 100.0) or whitespace differs from Python's output
// in those bytes only. Every other byte is Python's.
//
// Each exported appender grows dst once, to a capacity that fits the
// payload unless its strings need escapes, and on failure returns dst
// unchanged. The values the decoder produces never fail: their strings are
// valid UTF-8, their floats finite and their structured levels non-empty.

// maxFloatLen is the longest text appendFloat writes for a float64, as in
// -2.2250738585072014e-308.
const maxFloatLen = 24

// maxCountLen is the longest decimal uint64, 18446744073709551615.
const maxCountLen = 20

// maxLevelLen is the longest level as a member name: a quoted uint32.
const maxLevelLen = len(`"4294967295"`)

// The fixed text of each payload, from the first byte to the first value.
const (
	noulPrefix          = `{"type":"noul","noul":`
	choicePrefix        = `{"type":"choice","choice":`
	scorePrefix         = `{"type":"score","score":`
	confidenceMember    = `,"confidence":`
	probabilitiesMember = `,"probabilities":{`
	legendMember        = `,"legend":{`
	modelMember         = `{"model":`
	usageMember         = `,"usage":`
	answersMember       = `,"answers":`
	inputCountMember    = `{"input_tokens":`
	outputCountMember   = `,"output_tokens":`
	modelsMember        = `{"models":[`
	nameMember          = `{"name":`
	descriptionMember   = `,"description":`
	releaseDateMember   = `,"release_date":`
)

// AppendSystemOneResult appends r to dst as the payload of a System One
// response, {"model":…,"usage":{…},"answers":{…}}, the bytes the Python
// SDK's SystemOneResponse.model_dump_json writes: no HTTP metadata, and
// none of the nouls, choices and scores views.
func AppendSystemOneResult(dst []byte, r *SystemOneResult) ([]byte, error) {
	dst = slices.Grow(dst, systemOneResultSize(r))
	n0 := len(dst)
	dst = append(dst, modelMember...)
	dst, err := AppendString(dst, r.Model)
	if err != nil {
		return dst[:n0], err
	}
	dst = append(dst, usageMember...)
	dst = appendUsage(dst, &r.Usage)
	dst = append(dst, answersMember...)
	if dst, err = appendAnswers(dst, &r.Answers); err != nil {
		return dst[:n0], err
	}
	return append(dst, '}'), nil
}

// AppendModelList appends l to dst as the payload of a list-models
// response, {"models":[{"name":…,"description":…,"release_date":…},…]}, as
// the Python SDK's ListModelsResponse.model_dump_json writes it.
func AppendModelList(dst []byte, l *ModelList) ([]byte, error) {
	dst = slices.Grow(dst, modelListSize(l))
	n0 := len(dst)
	dst = append(dst, modelsMember...)
	for i := range l.Models {
		if i > 0 {
			dst = append(dst, ',')
		}
		var err error
		if dst, err = appendModelCard(dst, &l.Models[i]); err != nil {
			return dst[:n0], err
		}
	}
	return append(dst, ']', '}'), nil
}

// AppendModelCard appends c to dst as
// {"name":…,"description":…,"release_date":…}, as the Python SDK's
// ModelMetadata.model_dump_json writes it.
func AppendModelCard(dst []byte, c *ModelCard) ([]byte, error) {
	dst = slices.Grow(dst, modelCardSize(c))
	return appendModelCard(dst, c)
}

// AppendUsage appends u to dst as {"input_tokens":…,"output_tokens":…}, a
// count the response left out as null, as the Python SDK's
// Usage.model_dump_json writes it.
func AppendUsage(dst []byte, u *Usage) []byte {
	dst = slices.Grow(dst, usageSize)
	return appendUsage(dst, u)
}

// AppendAnswers appends s to dst as a JSON object from question names to
// answers, in the order of [Answers.Entries], as the answers member of a
// response payload. An entry of KindUnknown, which holds no value, is left
// out. A nil s is written {}.
func AppendAnswers(dst []byte, s *Answers) ([]byte, error) {
	dst = slices.Grow(dst, answersSize(s))
	return appendAnswers(dst, s)
}

// AppendAnswer appends a to dst as the payload of its kind: see
// [AppendNoulAnswer], [AppendChoiceAnswer] and [AppendScoreAnswer]. An
// Answer of KindUnknown holds no value and is written null.
func AppendAnswer(dst []byte, a *Answer) ([]byte, error) {
	dst = slices.Grow(dst, answerSize(a))
	return appendAnswer(dst, a)
}

// AppendNoulAnswer appends a to dst as {"type":"noul","noul":…}.
func AppendNoulAnswer(dst []byte, a *NoulAnswer) ([]byte, error) {
	dst = slices.Grow(dst, noulSize)
	return appendNoul(dst, a)
}

// AppendChoiceAnswer appends a to dst as
// {"type":"choice","choice":…,"confidence":…,"probabilities":{…}}, the
// probabilities in the order the answer lists them.
func AppendChoiceAnswer(dst []byte, a *ChoiceAnswer) ([]byte, error) {
	dst = slices.Grow(dst, choiceSize(a))
	return appendChoice(dst, a)
}

// AppendScoreAnswer appends a to dst as
// {"type":"score","score":…,"confidence":…,"legend":{…},"probabilities":{…}},
// the legend and the probabilities in the order the answer lists them, each
// level a decimal member name. A text level is a JSON string; a structured
// level is spliced as its bytes, which must hold one JSON object or array
// (the decoder's: the bytes the response carried). Empty structured bytes
// fail with [ErrContentShape].
func AppendScoreAnswer(dst []byte, a *ScoreAnswer) ([]byte, error) {
	dst = slices.Grow(dst, scoreSize(a))
	return appendScore(dst, a)
}

// appendModelCard is AppendModelCard without the growth.
func appendModelCard(dst []byte, c *ModelCard) ([]byte, error) {
	n0 := len(dst)
	dst = append(dst, nameMember...)
	dst, err := AppendString(dst, c.Name)
	if err != nil {
		return dst[:n0], err
	}
	dst = append(dst, descriptionMember...)
	if dst, err = AppendString(dst, c.Description); err != nil {
		return dst[:n0], err
	}
	dst = append(dst, releaseDateMember...)
	if dst, err = AppendString(dst, c.ReleaseDate); err != nil {
		return dst[:n0], err
	}
	return append(dst, '}'), nil
}

// appendUsage is AppendUsage without the growth.
func appendUsage(dst []byte, u *Usage) []byte {
	dst = append(dst, inputCountMember...)
	dst = appendCount(dst, u.InputTokens, u.HasInputTokens)
	dst = append(dst, outputCountMember...)
	dst = appendCount(dst, u.OutputTokens, u.HasOutputTokens)
	return append(dst, '}')
}

// appendCount appends n in decimal, or null when the count is absent.
func appendCount(dst []byte, n uint64, present bool) []byte {
	if !present {
		return append(dst, "null"...)
	}
	return strconv.AppendUint(dst, n, 10)
}

// appendAnswers is AppendAnswers without the growth.
func appendAnswers(dst []byte, s *Answers) ([]byte, error) {
	n0 := len(dst)
	dst = append(dst, '{')
	first := true
	for _, e := range s.entriesOrNil() {
		if e.Answer.Kind == KindUnknown {
			continue
		}
		if !first {
			dst = append(dst, ',')
		}
		first = false
		var err error
		if dst, err = AppendString(dst, e.Name); err != nil {
			return dst[:n0], err
		}
		dst = append(dst, ':')
		if dst, err = appendAnswer(dst, &e.Answer); err != nil {
			return dst[:n0], err
		}
	}
	return append(dst, '}'), nil
}

// entriesOrNil returns the entries of s, or nil when s is nil.
func (s *Answers) entriesOrNil() []AnswerEntry {
	if s == nil {
		return nil
	}
	return s.entries
}

// appendAnswer is AppendAnswer without the growth.
func appendAnswer(dst []byte, a *Answer) ([]byte, error) {
	switch a.Kind {
	case KindNoul:
		return appendNoul(dst, &a.Noul)
	case KindChoice:
		return appendChoice(dst, &a.Choice)
	case KindScore:
		return appendScore(dst, &a.Score)
	default:
		return append(dst, "null"...), nil
	}
}

// appendNoul is AppendNoulAnswer without the growth.
func appendNoul(dst []byte, a *NoulAnswer) ([]byte, error) {
	n0 := len(dst)
	dst = append(dst, noulPrefix...)
	dst, err := appendFloat(dst, a.Noul, 64)
	if err != nil {
		return dst[:n0], err
	}
	return append(dst, '}'), nil
}

// appendChoice is AppendChoiceAnswer without the growth.
func appendChoice(dst []byte, a *ChoiceAnswer) ([]byte, error) {
	n0 := len(dst)
	dst = append(dst, choicePrefix...)
	dst, err := AppendString(dst, a.Choice)
	if err != nil {
		return dst[:n0], err
	}
	dst = append(dst, confidenceMember...)
	if dst, err = appendFloat(dst, a.Confidence, 64); err != nil {
		return dst[:n0], err
	}
	dst = append(dst, probabilitiesMember...)
	for i := range a.Probabilities {
		p := &a.Probabilities[i]
		if i > 0 {
			dst = append(dst, ',')
		}
		if dst, err = AppendString(dst, p.Label); err != nil {
			return dst[:n0], err
		}
		dst = append(dst, ':')
		if dst, err = appendFloat(dst, p.Probability, 64); err != nil {
			return dst[:n0], err
		}
	}
	return append(dst, '}', '}'), nil
}

// appendScore is AppendScoreAnswer without the growth.
func appendScore(dst []byte, a *ScoreAnswer) ([]byte, error) {
	n0 := len(dst)
	dst = append(dst, scorePrefix...)
	dst, err := appendFloat(dst, a.Score, 64)
	if err != nil {
		return dst[:n0], err
	}
	dst = append(dst, confidenceMember...)
	if dst, err = appendFloat(dst, a.Confidence, 64); err != nil {
		return dst[:n0], err
	}
	dst = append(dst, legendMember...)
	for i := range a.Legend {
		e := &a.Legend[i]
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendLevel(dst, e.Level)
		switch d := &e.Description; {
		case !d.IsJSON():
			if dst, err = AppendString(dst, d.Text); err != nil {
				return dst[:n0], err
			}
		case len(d.JSON) == 0:
			return dst[:n0], ErrContentShape
		default:
			dst = append(dst, d.JSON...)
		}
	}
	dst = append(dst, '}')
	dst = append(dst, probabilitiesMember...)
	for i := range a.Probabilities {
		p := &a.Probabilities[i]
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendLevel(dst, p.Level)
		if dst, err = appendFloat(dst, p.Probability, 64); err != nil {
			return dst[:n0], err
		}
	}
	return append(dst, '}', '}'), nil
}

// appendLevel appends level as a member name and its colon: "2":.
func appendLevel(dst []byte, level uint32) []byte {
	dst = append(dst, '"')
	dst = strconv.AppendUint(dst, uint64(level), 10)
	return append(dst, '"', ':')
}

// The sizes below are the length of a payload when none of its strings
// needs an escape, with every float and count taken at its longest; a
// payload whose strings need escapes can be longer, and its buffer then
// grows as it is written.

// noulSize is the longest payload of a noul answer.
const noulSize = len(noulPrefix) + maxFloatLen + len(`}`)

// usageSize is the longest payload of a usage.
const usageSize = len(inputCountMember) + maxCountLen + len(outputCountMember) + maxCountLen + len(`}`)

// stringSize is the length of s as a JSON string without escapes.
func stringSize(s string) int { return len(s) + len(`""`) }

// systemOneResultSize bounds the payload of r.
func systemOneResultSize(r *SystemOneResult) int {
	return len(modelMember) + stringSize(r.Model) + len(usageMember) + usageSize +
		len(answersMember) + answersSize(&r.Answers) + len(`}`)
}

// modelListSize bounds the payload of l.
func modelListSize(l *ModelList) int {
	n := len(modelsMember) + len(`]}`)
	for i := range l.Models {
		n += len(`,`) + modelCardSize(&l.Models[i])
	}
	return n
}

// modelCardSize bounds the payload of c.
func modelCardSize(c *ModelCard) int {
	return len(nameMember) + stringSize(c.Name) + len(descriptionMember) + stringSize(c.Description) +
		len(releaseDateMember) + stringSize(c.ReleaseDate) + len(`}`)
}

// answersSize bounds the payload of s.
func answersSize(s *Answers) int {
	n := len(`{}`)
	for _, e := range s.entriesOrNil() {
		n += len(`,`) + stringSize(e.Name) + len(`:`) + answerSize(&e.Answer)
	}
	return n
}

// answerSize bounds the payload of a.
func answerSize(a *Answer) int {
	switch a.Kind {
	case KindNoul:
		return noulSize
	case KindChoice:
		return choiceSize(&a.Choice)
	case KindScore:
		return scoreSize(&a.Score)
	default:
		return len("null")
	}
}

// choiceSize bounds the payload of a.
func choiceSize(a *ChoiceAnswer) int {
	n := len(choicePrefix) + stringSize(a.Choice) + len(confidenceMember) + maxFloatLen + len(probabilitiesMember) + len(`}}`)
	for i := range a.Probabilities {
		n += len(`,`) + stringSize(a.Probabilities[i].Label) + len(`:`) + maxFloatLen
	}
	return n
}

// scoreSize bounds the payload of a.
func scoreSize(a *ScoreAnswer) int {
	n := len(scorePrefix) + maxFloatLen + len(confidenceMember) + maxFloatLen + len(legendMember) + len(`}`) +
		len(probabilitiesMember) + len(`}}`)
	for i := range a.Legend {
		d := &a.Legend[i].Description
		n += len(`,`) + maxLevelLen + len(`:`)
		if d.IsJSON() {
			n += len(d.JSON)
		} else {
			n += stringSize(d.Text)
		}
	}
	n += len(a.Probabilities) * (len(`,`) + maxLevelLen + len(`:`) + maxFloatLen)
	return n
}
