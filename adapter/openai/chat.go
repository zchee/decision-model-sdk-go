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
	"fmt"
	"strconv"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// errNotChat is the error of a 2xx body that is not a Chat Completions
// response: not JSON, not an object, or a member the result needs absent
// or of the wrong kind. Its text quotes nothing of the body.
var errNotChat = errors.New("openai: the response body is not a Chat Completions response")

// chatBody returns the Chat Completions request body
// (providers/openai.py:156-160): model, messages and response_format, in
// that order. response_format wraps the schema in structured mode
// (providers/openai.py:35-41); in prompted mode it is null with FormatNull,
// as upstream sends it, and absent with FormatOmit.
func chatBody(model string, req *llm.Request, format PromptedFormat) ([]byte, error) {
	members := []jsonx.Member{
		{Name: "model", Value: jsonx.String(model)},
		{Name: "messages", Value: messages(req.Messages)},
	}
	switch {
	case req.Structured:
		members = append(members, jsonx.Member{Name: "response_format", Value: jsonx.Object(
			jsonx.Member{Name: "type", Value: jsonx.String("json_schema")},
			jsonx.Member{Name: "json_schema", Value: jsonx.Object(
				jsonx.Member{Name: "name", Value: jsonx.String("evaluation")},
				jsonx.Member{Name: "schema", Value: jsonx.Raw(req.Schema)},
				jsonx.Member{Name: "strict", Value: jsonx.Bool(true)},
			)},
		)})
	case format == FormatNull:
		// The zero Value is JSON null.
		members = append(members, jsonx.Member{Name: "response_format"})
	}
	return marshal(jsonx.Object(members...))
}

// messages returns msgs as the role and content objects the two APIs take
// (providers/base.py:121-123).
func messages(msgs []llm.Message) jsonx.Value {
	elems := make([]jsonx.Value, len(msgs))
	for i, m := range msgs {
		elems[i] = jsonx.Object(
			jsonx.Member{Name: "role", Value: jsonx.String(string(m.Role))},
			jsonx.Member{Name: "content", Value: jsonx.String(m.Content)},
		)
	}
	return jsonx.Array(elems...)
}

// marshal returns v as a request body. Only a schema that is not one JSON
// value makes it fail, since every other member is a string the writer
// makes valid.
func marshal(v jsonx.Value) ([]byte, error) {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("openai: the request body could not be written: %w", err)
	}
	return b, nil
}

// chatResult returns the result of the Chat Completions response body data
// (providers/openai.py:44-53). It records data with choices[0].finish_reason
// before it checks anything else: the text pyValue gives, nil when the
// member is null or absent or cannot be read. A finish reason other than
// "stop" or null is a non-answer with upstream's text, an integer written
// as its digits. The text is choices[0].message.content,
// "" when it is null or absent, and the counts are usage.prompt_tokens and
// usage.completion_tokens, unknown when usage or the member is null or
// absent.
func chatResult(data []byte, trace *llm.Trace) (*llm.Result, error) {
	doc, err := jsonx.Read(data)
	var choice jsonx.Node
	ok := err == nil && doc.Kind() == jsonx.KindObject
	if ok {
		choices, has := doc.Member("choices")
		ok = has && choices.Kind() == jsonx.KindArray && choices.Len() > 0
		if ok {
			choice = choices.Index(0)
			ok = choice.Kind() == jsonx.KindObject
		}
	}
	var (
		reason *string
		text   string
		kind   jsonx.Kind
	)
	if ok {
		text, kind, ok = pyValue(choice, "finish_reason")
		if ok && kind != jsonx.KindNull {
			reason = &text
		}
	}
	trace.RecordResponse(data, reason)
	if !ok {
		return nil, errNotChat
	}
	if kind != jsonx.KindNull && (kind != jsonx.KindString || text != "stop") {
		return nil, &llm.NonAnswerError{Message: "OpenAI chat completion did not complete: " + text + "."}
	}
	message, has := choice.Member("message")
	if !has || message.Kind() != jsonx.KindObject {
		return nil, errNotChat
	}
	content, ok := optionalString(message, "content")
	if !ok {
		return nil, errNotChat
	}
	in, out, ok := usage(doc, "prompt_tokens", "completion_tokens")
	if !ok {
		return nil, errNotChat
	}
	res := &llm.Result{InputTokens: in, OutputTokens: out}
	if content != nil {
		res.Text = *content
	}
	return res, nil
}

// optionalString returns the member name of the object obj: a pointer to
// its text for a string, nil for null or an absent member, and false for
// any other kind.
func optionalString(obj jsonx.Node, name string) (*string, bool) {
	v, has := obj.Member(name)
	switch {
	case !has || v.Kind() == jsonx.KindNull:
		return nil, true
	case v.Kind() == jsonx.KindString:
		s := v.Text()
		return &s, true
	}
	return nil, false
}

// pyValue returns the text upstream's f-string writes, with Python's str(),
// for the member name of the object obj, and the member's kind: a string
// is itself (KindString); null or an absent member is None (KindNull); an
// integer literal is its digits (KindNumber), -0 written 0 as json.loads
// reads it. It returns false for any other kind, a number with a fraction
// or an exponent included, whose Python text the port does not reproduce.
func pyValue(obj jsonx.Node, name string) (string, jsonx.Kind, bool) {
	v, has := obj.Member(name)
	switch {
	case !has || v.Kind() == jsonx.KindNull:
		return "None", jsonx.KindNull, true
	case v.Kind() == jsonx.KindString:
		return v.Text(), jsonx.KindString, true
	case v.Kind() == jsonx.KindNumber && v.IsInt():
		if v.Text() == "-0" {
			return "0", jsonx.KindNumber, true
		}
		return v.Text(), jsonx.KindNumber, true
	}
	return "", v.Kind(), false
}

// usage returns the counts named in and out of the usage member of the
// object doc: unknown when usage or the count is null or absent, as
// upstream's getattr(response.usage, name, None) gives None
// (providers/openai.py:51-52,88-89). It returns false for a usage that is
// neither an object nor null and for a count that is neither null nor an
// integer from 0 to 2^64-1.
func usage(doc jsonx.Node, in, out string) (inCount, outCount llm.Count, ok bool) {
	u, has := doc.Member("usage")
	if !has || u.Kind() == jsonx.KindNull {
		return llm.Count{}, llm.Count{}, true
	}
	if u.Kind() != jsonx.KindObject {
		return llm.Count{}, llm.Count{}, false
	}
	inCount, inOK := count(u, in)
	outCount, outOK := count(u, out)
	return inCount, outCount, inOK && outOK
}

// count returns the count named name of the object u, unknown when it is
// null or absent, and false when it is not an integer from 0 to 2^64-1.
func count(u jsonx.Node, name string) (llm.Count, bool) {
	v, has := u.Member(name)
	switch {
	case !has || v.Kind() == jsonx.KindNull:
		return llm.Count{}, true
	case v.Kind() != jsonx.KindNumber || !v.IsInt():
		return llm.Count{}, false
	case v.Text() == "-0":
		// JSON's one negative spelling of zero.
		return llm.Count{Known: true}, true
	}
	n, err := strconv.ParseUint(v.Text(), 10, 64)
	if err != nil {
		return llm.Count{}, false
	}
	return llm.Count{N: n, Known: true}, true
}
