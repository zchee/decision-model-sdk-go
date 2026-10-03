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

package openai

import (
	"errors"
	"strings"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// errNotResponses is the error of a 2xx body that is not a Responses API
// response: not JSON, not an object, or a member the result needs absent
// or of the wrong kind. Its text quotes nothing of the body.
var errNotResponses = errors.New("openai: the response body is not a Responses API response")

// responsesBody returns the Responses request body
// (providers/openai.py:56-69). In structured mode it is model, input, text,
// store and instructions, in that order: input holds the messages that are
// not system messages, text the schema, store false, and instructions the
// system messages' contents joined by a blank line. In prompted mode it is
// model, input, text and store, input holding every message and text
// asking for a JSON object: the API checks that the word JSON appears in
// input, which instructions does not satisfy.
func responsesBody(model string, req *llm.Request) ([]byte, error) {
	if !req.Structured {
		return marshal(jsonx.Object(
			jsonx.Member{Name: "model", Value: jsonx.String(model)},
			jsonx.Member{Name: "input", Value: messages(req.Messages)},
			jsonx.Member{Name: "text", Value: format(jsonx.Member{Name: "type", Value: jsonx.String("json_object")})},
			jsonx.Member{Name: "store", Value: jsonx.Bool(false)},
		))
	}
	var system []string
	input := make([]llm.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = append(system, m.Content)
			continue
		}
		input = append(input, m)
	}
	return marshal(jsonx.Object(
		jsonx.Member{Name: "model", Value: jsonx.String(model)},
		jsonx.Member{Name: "input", Value: messages(input)},
		jsonx.Member{Name: "text", Value: format(
			jsonx.Member{Name: "type", Value: jsonx.String("json_schema")},
			jsonx.Member{Name: "name", Value: jsonx.String("evaluation")},
			jsonx.Member{Name: "schema", Value: jsonx.Raw(req.Schema)},
			jsonx.Member{Name: "strict", Value: jsonx.Bool(true)},
		)},
		jsonx.Member{Name: "store", Value: jsonx.Bool(false)},
		jsonx.Member{Name: "instructions", Value: jsonx.String(strings.Join(system, "\n\n"))},
	))
}

// format returns the text member's value {"format": {members}}.
func format(members ...jsonx.Member) jsonx.Value {
	return jsonx.Object(jsonx.Member{Name: "format", Value: jsonx.Object(members...)})
}

// responsesResult returns the result of the Responses response body data
// (providers/openai.py:72-90). It records data with its status before it
// checks anything: the text pyValue gives, nil when the status is null or
// absent or cannot be read.
//
// A status other than the string "completed" is a non-answer whose reason
// is error.message when error is not null, else incomplete_details.reason
// when incomplete_details is not null, else the status, each written as
// pyValue writes it (None for null or absent, an integer as its digits).
// Then a part of type refusal in an item of type message is a non-answer
// with its refusal text, written the same way. The text is
// the text of every output_text part of every message item, in order;
// items of other types, such as reasoning, are skipped. The counts are
// usage.input_tokens and usage.output_tokens, unknown when usage or the
// member is null or absent.
func responsesResult(data []byte, trace *llm.Trace) (*llm.Result, error) {
	doc, err := jsonx.Read(data)
	ok := err == nil && doc.Kind() == jsonx.KindObject
	var (
		status *string
		text   string
		kind   jsonx.Kind
	)
	if ok {
		text, kind, ok = pyValue(doc, "status")
		if ok && kind != jsonx.KindNull {
			status = &text
		}
	}
	trace.RecordResponse(data, status)
	if !ok {
		return nil, errNotResponses
	}
	if kind != jsonx.KindString || text != "completed" {
		reason, ok := incompleteReason(doc, text)
		if !ok {
			return nil, errNotResponses
		}
		return nil, &llm.NonAnswerError{Message: "OpenAI response did not complete: " + reason + "."}
	}
	output, has := doc.Member("output")
	if !has || output.Kind() != jsonx.KindArray {
		return nil, errNotResponses
	}
	// Every refusal is looked for before any text is read, as upstream
	// checks the parts before it reads output_text.
	for i := range output.Len() {
		content, ok := messageContent(output.Index(i))
		if !ok {
			return nil, errNotResponses
		}
		for j := range content.Len() {
			part := content.Index(j)
			if part.Kind() != jsonx.KindObject {
				return nil, errNotResponses
			}
			if partType(part) != "refusal" {
				continue
			}
			refusal, _, ok := pyValue(part, "refusal")
			if !ok {
				return nil, errNotResponses
			}
			// Upstream's text has no period after the refusal.
			return nil, &llm.NonAnswerError{Message: "OpenAI response was a refusal: " + refusal}
		}
	}
	var answer strings.Builder
	for i := range output.Len() {
		content, _ := messageContent(output.Index(i))
		for j := range content.Len() {
			part := content.Index(j)
			if partType(part) != "output_text" {
				continue
			}
			t, has := part.Member("text")
			if !has || t.Kind() != jsonx.KindString {
				return nil, errNotResponses
			}
			answer.WriteString(t.Text())
		}
	}
	in, out, ok := usage(doc, "input_tokens", "output_tokens")
	if !ok {
		return nil, errNotResponses
	}
	return &llm.Result{Text: answer.String(), InputTokens: in, OutputTokens: out}, nil
}

// incompleteReason returns the reason of a response whose status, written
// status, is not "completed", and false when error or incomplete_details is
// neither null nor an object, or its member is one pyValue refuses.
func incompleteReason(doc jsonx.Node, status string) (string, bool) {
	for _, m := range [...]struct{ object, member string }{{"error", "message"}, {"incomplete_details", "reason"}} {
		v, has := doc.Member(m.object)
		if !has || v.Kind() == jsonx.KindNull {
			continue
		}
		if v.Kind() != jsonx.KindObject {
			return "", false
		}
		reason, _, ok := pyValue(v, m.member)
		return reason, ok
	}
	return status, true
}

// messageContent returns the content parts of the output item item: an
// empty Node for an item whose type is not "message", and false for an
// item that is not an object or a message item whose content is not an
// array.
func messageContent(item jsonx.Node) (jsonx.Node, bool) {
	if item.Kind() != jsonx.KindObject {
		return jsonx.Node{}, false
	}
	if t, has := item.Member("type"); !has || t.Kind() != jsonx.KindString || t.Text() != "message" {
		return jsonx.Node{}, true
	}
	content, has := item.Member("content")
	if !has || content.Kind() != jsonx.KindArray {
		return jsonx.Node{}, false
	}
	return content, true
}

// partType returns the type of the content part part, "" when it is not a
// string.
func partType(part jsonx.Node) string {
	if t, has := part.Member("type"); has && t.Kind() == jsonx.KindString {
		return t.Text()
	}
	return ""
}
