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

// RawJSON is one JSON value that is already encoded. The SDK checks it and
// removes its insignificant whitespace, but never decodes it: strings keep
// their escape sequences and numbers their spelling.
//
// As a [RawQuestion] field value it is written as the value it holds; [JSON]
// takes one to build structured [Content].
type RawJSON []byte

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
// built with [Text], never from a JSON string.
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

// orNil returns the content to write, or nil when c is unset.
func (c *Content) orNil() *wire.Content {
	if !c.set {
		return nil
	}
	return &c.w
}
