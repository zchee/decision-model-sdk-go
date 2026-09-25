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

import "bytes"

// Content is text, or a JSON object or array: the shape of question
// instructions, option and level descriptions, and the score legends a
// response echoes back.
//
// Text is held decoded (no quotes, no escape sequences). A JSON object or
// array is held as bytes, so that it can be spliced into a request or
// compared with a response byte for byte: the compact bytes a question set
// produced, or the exact bytes a response carried, escapes and any
// whitespace included.
type Content struct {
	// Text is the text when JSON is nil, and empty otherwise.
	Text string
	// JSON is the encoding of a JSON object or array, or nil when the content
	// is text. An empty object is []byte("{}"), never nil.
	JSON []byte
}

// IsJSON reports whether c holds a JSON object or array rather than text.
func (c Content) IsJSON() bool { return c.JSON != nil }

// Equal reports whether c and o hold the same text, or the same JSON bytes.
// Text never equals JSON, even when the text spells the same bytes.
func (c Content) Equal(o Content) bool {
	if c.IsJSON() != o.IsJSON() {
		return false
	}
	if c.IsJSON() {
		return bytes.Equal(c.JSON, o.JSON)
	}
	return c.Text == o.Text
}
