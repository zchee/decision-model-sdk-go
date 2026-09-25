//go:build !go1.28 && (amd64 || arm64)

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

package codec

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/bytedance/sonic"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// ErrorBody is what the lenient error-body reader found in the body of an
// unsuccessful response.
type ErrorBody struct {
	// Message is the server's message, found as the Python SDK finds it
	// (errors.py extract_message): the body when it is a JSON string or not
	// JSON at all, else "error" when it is a string, "error.message",
	// "message", "detail" when it is a string, "detail.message", or the
	// "msg" of each "detail" entry prefixed with its "loc" path and joined
	// with "; ". When none of those holds a non-empty string, Message is the
	// body itself: its JSON compacted (whitespace between tokens dropped,
	// nothing else re-encoded), or a JSON string's value, which may be
	// empty. A body that is not JSON is its text with each ill-formed UTF-8
	// sequence replaced by U+FFFD. Message is neither escaped nor cut.
	Message string
	// NoBody reports a body that is empty or the JSON null, which the Python
	// SDK reports as "status code (no body)".
	NoBody bool
	// ErrorType is the server's machine-readable name for the failure, from
	// "detail.error_type" when it is a string, or empty.
	ErrorType string
}

// errorBodyAPI decodes an error body into generic values. Numbers stay text
// (json.Number): a float out of range would make sonic's verdict depend on
// the architecture (ledger W0.3), and the reader never computes with one.
var errorBodyAPI = sonic.Config{UseNumber: true}.Froze()

// ReadErrorBody reads the body of an unsuccessful response as the Python SDK
// does (errors.py extract_message over json.py deserialize). The body is
// JSON only if it is exactly one valid JSON value, with valid UTF-8 and no
// raw control character in any string, as the Python SDK's parser requires;
// anything else is text. A duplicate member takes its last value. It never
// fails and never aliases body.
func ReadErrorBody(body []byte) ErrorBody {
	if len(body) == 0 {
		return ErrorBody{NoBody: true}
	}
	compact, err := wire.AppendJSON(nil, body)
	if err != nil {
		return ErrorBody{Message: replaceInvalidUTF8(body)}
	}
	var v any
	if err := errorBodyAPI.UnmarshalFromString(NoCopyString(compact), &v); err != nil {
		// wire's scanner has no depth limit and sonic has one; a body past
		// it is reported as the text it is.
		return ErrorBody{Message: replaceInvalidUTF8(body)}
	}
	if v == nil {
		return ErrorBody{NoBody: true}
	}
	eb := ErrorBody{ErrorType: detailErrorType(v)}
	if msg := extractMessage(v); msg != "" {
		eb.Message = msg
		return eb
	}
	if s, ok := v.(string); ok {
		eb.Message = s
		return eb
	}
	eb.Message = string(compact)
	return eb
}

// extractMessage is the Python SDK's extract_message over a decoded JSON
// value; "" stands for its None.
func extractMessage(v any) string {
	switch b := v.(type) {
	case string:
		return b
	case map[string]any:
		errV, msgV, detail := b["error"], b["message"], b["detail"]
		if s, ok := errV.(string); ok {
			return s // an empty "error" still stops the search, as in Python
		}
		if m, ok := errV.(map[string]any); ok {
			if s, ok := m["message"].(string); ok {
				return s
			}
		}
		if s, ok := msgV.(string); ok {
			return s
		}
		if s, ok := detail.(string); ok {
			return s
		}
		if m, ok := detail.(map[string]any); ok {
			if s, ok := m["message"].(string); ok {
				return s
			}
		}
		if list, ok := detail.([]any); ok {
			return detailMessages(list)
		}
	}
	return ""
}

// detailMessages joins the "msg" of each entry of a FastAPI-style "detail"
// list with "; ", each prefixed with its "loc" path and ": " when the path
// is not empty; entries without a string "msg" are skipped.
func detailMessages(list []any) string {
	var sb strings.Builder
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		msg, ok := m["msg"].(string)
		if !ok {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("; ")
		}
		if loc, ok := m["loc"].([]any); ok {
			if path := locPath(loc); path != "" {
				sb.WriteString(path)
				sb.WriteString(": ")
			}
		}
		sb.WriteString(msg)
	}
	return sb.String()
}

// locPath joins the items of a "loc" list with "." as the Python SDK does
// (str(item) for every item that is not the string "body"): a string as it
// is, an integer in decimal, true, false and null as Python spells them. A
// number with a fraction or an exponent keeps its JSON spelling, and an
// object or an array is left out, where Python would print its repr.
func locPath(loc []any) string {
	var sb strings.Builder
	first := true
	for _, item := range loc {
		var s string
		switch x := item.(type) {
		case string:
			if x == "body" {
				continue
			}
			s = x
		case json.Number:
			s = string(x)
			if s == "-0" {
				s = "0"
			}
		case bool:
			s = "False"
			if x {
				s = "True"
			}
		case nil:
			s = "None"
		default:
			continue
		}
		if !first {
			sb.WriteByte('.')
		}
		first = false
		sb.WriteString(s)
	}
	return sb.String()
}

// detailErrorType returns "detail.error_type" when v holds it as a string.
func detailErrorType(v any) string {
	b, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	d, ok := b["detail"].(map[string]any)
	if !ok {
		return ""
	}
	s, _ := d["error_type"].(string)
	return s
}

// replaceInvalidUTF8 returns b as text, each maximal ill-formed subsequence
// replaced by one U+FFFD, as Python's bytes.decode("utf-8", "replace") and
// the WHATWG decoder do (Unicode 15, section 3.9, "U+FFFD Substitution of
// Maximal Subparts"). A maximal subpart is a lead byte followed by the
// continuation bytes that are valid at their positions before the sequence
// breaks off, or a single byte that cannot start a sequence.
func replaceInvalidUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b) + 8)
	for i := 0; i < len(b); {
		if b[i] < utf8.RuneSelf {
			sb.WriteByte(b[i])
			i++
			continue
		}
		if r, size := utf8.DecodeRune(b[i:]); r != utf8.RuneError || size > 1 {
			sb.Write(b[i : i+size])
			i += size
			continue
		}
		sb.WriteRune(utf8.RuneError)
		i += maximalSubpart(b[i:])
	}
	return sb.String()
}

// maximalSubpart returns the length of the maximal subpart at the start of
// b, which does not start a well-formed sequence (Unicode 15, table 3-7).
func maximalSubpart(b []byte) int {
	lo, hi := byte(0x80), byte(0xbf) // the second byte's range
	var need int
	switch c := b[0]; {
	case 0xc2 <= c && c <= 0xdf:
		need = 1
	case c == 0xe0:
		need, lo = 2, 0xa0
	case 0xe1 <= c && c <= 0xec, c == 0xee, c == 0xef:
		need = 2
	case c == 0xed:
		need, hi = 2, 0x9f
	case c == 0xf0:
		need, lo = 3, 0x90
	case 0xf1 <= c && c <= 0xf3:
		need = 3
	case c == 0xf4:
		need, hi = 3, 0x8f
	default:
		return 1
	}
	n := 1
	for n <= need && n < len(b) && lo <= b[n] && b[n] <= hi {
		n++
		lo, hi = 0x80, 0xbf
	}
	return n
}
