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

import "github.com/zchee/typesafe-sdk-go/internal/wire"

// RawJSON is one JSON value that is already encoded. The SDK never decodes
// it: strings keep their escape sequences and numbers their spelling. That
// makes it the way to send a number spelled exactly: a float in a state that
// is not RawJSON is written as Go's JSON encoders spell it, 3 for 3.0 and
// 10000000000000000 for 1e16, where the Python SDK writes 3.0 and 1e+16.
//
// As a [RawQuestion] field value it is checked and written without its
// insignificant whitespace, as the value it holds; [JSON] takes one to build
// structured [Content]. As the state of a call, or as the value of a member
// the call adds to the request body, it is sent as it is, whitespace
// included, after a check of its first byte only: a state must start a
// string, an array or an object, a member any JSON value. That it is one
// valid JSON value is then the caller's contract. A *RawJSON there is sent
// as the RawJSON it points to; a nil one is refused. Nested inside a state
// or a member's value (a map value, a struct field, a slice element, through
// a pointer too), it is written by [RawJSON.MarshalJSON]: checked and written
// without its insignificant whitespace, as a question field is. A member name repeated inside an object
// (`{"a":1,"a":2}`) is passed through unchanged, which a Python dict cannot
// produce; the server decides which one counts.
type RawJSON []byte

// MarshalJSON returns r without its insignificant whitespace, or null when r
// is nil, as json.RawMessage does, so that RawJSON nested inside a state or a
// request body member is sent as the JSON it holds, never as a base64
// string. It fails when r is not exactly one valid JSON value, checked by the
// same scanner as a question's JSON, so that invalid JSON never reaches the
// network (ruling R60); the encoder checks the result a second time.
//
// Nested RawJSON costs one scan and one copy of its bytes per call. A
// RawJSON that is the state itself, or an extra body member's whole value, is
// sent as it is and costs neither.
func (r RawJSON) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("null"), nil
	}
	return wire.AppendJSON(make([]byte, 0, len(r)), r)
}

// Content is text, or a JSON object or array: what the API accepts as a
// question's instructions, as the description of an outcome or an option,
// and as a score level.
//
// Build it with [Text] or [JSON]. The zero Content is unset: an optional
// member holding it is left off the wire, as the Python SDK leaves off an
// optional member that is None.
type Content struct {
	w   wire.Content
	set bool
}

// Text returns content that is the text s. Text("") is set: it is sent as an
// empty string, unlike the zero Content.
func Text(s string) Content {
	return Content{w: wire.Content{Text: s}, set: true}
}

// JSON returns content that is the JSON object or array raw, such as
// JSON([]byte(`{"subject":"refund","amount":20}`)) or a [RawJSON] value.
//
// raw is kept, not copied, until [Questions.Prepare] checks it and writes it
// without its insignificant whitespace; the caller must not modify it in
// between. Anything but a single JSON object or array, with optional
// whitespace around it, makes Prepare fail with a [*ConfigError]; text is
// built with [Text], never from a JSON string. As with [RawJSON], a repeated
// member name inside an object is passed through for the server to decide.
func JSON(raw []byte) Content {
	if raw == nil {
		// A nil JSON would read as text; keep the "not an object" failure.
		raw = []byte{}
	}
	return Content{w: wire.Content{JSON: raw}, set: true}
}

// IsZero reports whether c is unset, the zero Content.
func (c Content) IsZero() bool { return !c.set }

// IsJSON reports whether c holds a JSON object or array rather than text.
func (c Content) IsJSON() bool { return c.w.IsJSON() }

// Text returns the text, or "" when c holds JSON or is unset.
func (c Content) Text() string { return c.w.Text }

// JSON returns the JSON bytes as they were given, or nil when c is text or
// unset. The slice is shared: callers must not modify it.
func (c Content) JSON() RawJSON { return c.w.JSON }

// MarshalJSON returns c as a JSON value, written as a question writes it, so
// that Content nested inside a state or a request body member, such as a
// map[string]any value or a struct field, is sent as content rather than as
// an empty object: text as a JSON string, escaped as the questions are; JSON
// content checked and without its insignificant whitespace; unset Content as
// null. It fails when the text is not valid UTF-8, or when the JSON content
// is not a single valid JSON object or array, so that invalid JSON never
// reaches the network (ruling R60); the encoder checks the result a second
// time.
//
// Nested content costs one copy per call, and JSON content one scan too: it
// is built in a new slice for the encoder, where a string field is written
// straight into the body. A Content that is the state itself, or an extra
// body member's whole value, is written into the body without the copy. For
// large values, prefer a string field or a top-level Content.
func (c Content) MarshalJSON() ([]byte, error) {
	if !c.set {
		return []byte("null"), nil
	}
	return wire.AppendContent(make([]byte, 0, len(c.w.Text)+len(c.w.JSON)+2), c.w)
}

// orNil returns the content to write, or nil when c is unset.
func (c *Content) orNil() *wire.Content {
	if !c.set {
		return nil
	}
	return &c.w
}
