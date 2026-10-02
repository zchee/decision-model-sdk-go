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

package adapter

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zchee/typesafe-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/typesafe-sdk-go/adapter/llm"
)

// maxErrorBodyChars is how many characters of a re-written error body a
// status error's text keeps before it is cut: typesafe-sdk-python 0.7.0's
// MAX_ERROR_BODY_LENGTH (_core/constants.py:3).
const maxErrorBodyChars = 200

// requestIDHeader is the response header whose value a status error's text
// ends with: typesafe-sdk-python 0.7.0's REQUEST_ID_HEADER
// (_core/constants.py:18), in the canonical form net/http stores header
// names in. Python looks the name up in any case; a header map built by
// hand with another case of the name is not found here.
const requestIDHeader = "X-Typesafe-Request-Id"

// statusMessage returns the text upstream records for a provider status
// error, str() of the TypeSafeAPIError that map_provider_error builds from
// it (system-one-adapter-python v0.2.1,
// src/system_one_adapter/_utils/error_handling.py:67-69; typesafe-sdk-python
// 0.7.0, _core/errors.py:40-65 and 86-105):
//
//   - the status code, then a space and the message when there is one;
//   - " (request_id=<value>)" at the end when the response has an
//     x-typesafe-request-id header, its values joined by ", ".
//
// The message comes from e.Body as the Python SDKs of the three providers
// hand a response body to upstream:
//
//   - A nil Body is no body: the message is "status code (no body)".
//   - Otherwise the bytes are read as UTF-8, each ill-formed sequence
//     replaced by U+FFFD as Python's "replace" error handler does, and the
//     white space Python's str.strip removes is taken off both ends. An
//     empty text, of a non-nil empty Body too, has no message: the text is
//     the status code alone.
//   - A text that is JSON gives the message extractMessage finds in it; a
//     JSON null gives "status code (no body)"; any other value without a
//     message is written again as compact JSON, as pydantic-core's to_json
//     writes it, and only that form is cut after 200 characters, with "…"
//     appended.
//   - A text that is not JSON is the message itself, whole. A leading byte
//     order mark is not JSON.
//
// Three kinds of text that Python reads as JSON are taken as text that is
// not JSON, so their message differs from upstream's: one with a NaN,
// Infinity or -Infinity token, one with a string escape of a surrogate
// without its partner, and one that opens more than 10000 arrays and
// objects inside one another. A value nested deeper than 255 levels with
// no message, for which upstream has no text at all (pydantic-core refuses
// to write it), is taken as text too.
func statusMessage(e *llm.StatusError) string {
	b := strconv.AppendInt(nil, int64(e.StatusCode), 10)
	if msg := bodyMessage(e.Body); msg != "" {
		b = append(append(b, ' '), msg...)
	}
	if ids := e.Header.Values(requestIDHeader); len(ids) > 0 {
		b = append(append(append(b, " (request_id="...), strings.Join(ids, ", ")...), ')')
	}
	return string(b)
}

// noBodyMessage is the message of a response without a body
// (typesafe-sdk-python 0.7.0, _core/errors.py:90-91).
const noBodyMessage = "status code (no body)"

// bodyMessage returns the message of TypeSafeAPIError.__init__ for a
// response body (typesafe-sdk-python 0.7.0, _core/errors.py:86-95), "" for
// none.
func bodyMessage(body []byte) string {
	if body == nil {
		return noBodyMessage
	}
	text := strings.TrimFunc(decodeReplace(body), isPySpace)
	if text == "" {
		return ""
	}
	root, err := jsonx.Read([]byte(text))
	if err != nil {
		return text
	}
	if msg := extractMessage(root); msg != "" {
		return msg
	}
	switch root.Kind() {
	case jsonx.KindNull:
		return noBodyMessage
	case jsonx.KindString:
		// The empty string has no message.
		return ""
	}
	raw, err := jsonx.PydanticJSON([]byte(text))
	if err != nil {
		return text
	}
	return cutChars(string(raw))
}

// isPySpace reports whether r is white space that Python's str.strip
// removes (CPython 3.14's str.isspace): the 29 characters U+0009 to
// U+000D, U+001C to U+0020, U+0085, U+00A0, U+1680, U+2000 to U+200A,
// U+2028, U+2029, U+202F, U+205F and U+3000. unicode.IsSpace lacks U+001C
// to U+001F.
func isPySpace(r rune) bool {
	switch {
	case r >= 0x09 && r <= 0x0d, r >= 0x1c && r <= 0x20, r == 0x85, r == 0xa0, r == 0x1680,
		r >= 0x2000 && r <= 0x200a, r == 0x2028, r == 0x2029, r == 0x202f, r == 0x205f, r == 0x3000:
		return true
	}
	return false
}

// cutChars returns s cut after maxErrorBodyChars characters with "…"
// appended, or s when it is not longer.
func cutChars(s string) string {
	n := 0
	for i := range s {
		if n == maxErrorBodyChars {
			return s[:i] + "…"
		}
		n++
	}
	return s
}

// extractMessage is extract_message (typesafe-sdk-python 0.7.0,
// _core/errors.py:40-65): a string body itself; for an object, the first of
// its error member when it is a string, error.message, message, detail when
// it is a string, detail.message, and the entries of a detail array as
// "<loc>: <msg>" joined by "; ", each a string; "" when none applies.
func extractMessage(body jsonx.Node) string {
	switch body.Kind() {
	case jsonx.KindString:
		return body.Text()
	case jsonx.KindObject:
	default:
		return ""
	}
	errMember, _ := body.Member("error")
	message, _ := body.Member("message")
	detail, _ := body.Member("detail")
	if errMember.Kind() == jsonx.KindString {
		return errMember.Text()
	}
	if m, ok := errMember.Member("message"); ok && m.Kind() == jsonx.KindString {
		return m.Text()
	}
	if message.Kind() == jsonx.KindString {
		return message.Text()
	}
	if detail.Kind() == jsonx.KindString {
		return detail.Text()
	}
	if m, ok := detail.Member("message"); ok && m.Kind() == jsonx.KindString {
		return m.Text()
	}
	if detail.Kind() != jsonx.KindArray {
		return ""
	}
	var parts []string
	for i := range detail.Len() {
		entry := detail.Index(i)
		msg, ok := entry.Member("msg")
		if !ok || msg.Kind() != jsonx.KindString {
			continue
		}
		var path []string
		if loc, _ := entry.Member("loc"); loc.Kind() == jsonx.KindArray {
			for j := range loc.Len() {
				item := loc.Index(j)
				if item.Kind() == jsonx.KindString && item.Text() == "body" {
					continue
				}
				path = append(path, pyStr(item))
			}
		}
		// Python tests the joined path, so a loc of one empty string has no
		// path while two give ".".
		if joined := strings.Join(path, "."); joined != "" {
			parts = append(parts, joined+": "+msg.Text())
			continue
		}
		parts = append(parts, msg.Text())
	}
	return strings.Join(parts, "; ")
}

// pyStr returns str() of the Python value json.loads makes of v.
func pyStr(v jsonx.Node) string {
	if v.Kind() == jsonx.KindString {
		return v.Text()
	}
	return string(appendPyRepr(nil, v))
}

// appendPyRepr appends repr() of the Python value json.loads makes of v: an
// int as its value, a float as repr writes it (inf for a literal past the
// largest double), True, False, None, a string quoted as repr quotes it,
// and lists and dicts with ", " and ": " between their parts.
func appendPyRepr(dst []byte, v jsonx.Node) []byte {
	switch v.Kind() {
	case jsonx.KindFalse:
		return append(dst, "False"...)
	case jsonx.KindTrue:
		return append(dst, "True"...)
	case jsonx.KindNumber:
		if v.IsInt() {
			if v.Text() == "-0" {
				return append(dst, '0')
			}
			return append(dst, v.Text()...)
		}
		f, _ := strconv.ParseFloat(v.Text(), 64) // a range error carries the infinity Python makes
		return append(dst, jsonx.ReprFloat(f)...)
	case jsonx.KindString:
		return appendPyStrRepr(dst, v.Text())
	case jsonx.KindArray:
		dst = append(dst, '[')
		for i := range v.Len() {
			if i > 0 {
				dst = append(dst, ", "...)
			}
			dst = appendPyRepr(dst, v.Index(i))
		}
		return append(dst, ']')
	case jsonx.KindObject:
		dst = append(dst, '{')
		for i := range v.Len() {
			if i > 0 {
				dst = append(dst, ", "...)
			}
			dst = append(appendPyStrRepr(dst, v.Name(i)), ": "...)
			dst = appendPyRepr(dst, v.Index(i))
		}
		return append(dst, '}')
	}
	return append(dst, "None"...)
}

// appendPyStrRepr appends repr() of the Python string s (CPython's
// unicode_repr): in single quotes, or double quotes when s holds a single
// quote and no double quote; a backslash before the quote and before a
// backslash; \t, \n and \r; \xhh for the other ASCII control characters and
// for a character up to U+00FF that is not printable; \uhhhh and
// \Uhhhhhhhh for the other characters that are not printable. Printable is
// isPyPrintable.
func appendPyStrRepr(dst []byte, s string) []byte {
	quote := byte('\'')
	if strings.IndexByte(s, '\'') >= 0 && strings.IndexByte(s, '"') < 0 {
		quote = '"'
	}
	dst = append(dst, quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			dst = utf8.AppendRune(append(dst, '\\'), r)
		case r == '\t':
			dst = append(dst, `\t`...)
		case r == '\n':
			dst = append(dst, `\n`...)
		case r == '\r':
			dst = append(dst, `\r`...)
		case r < ' ' || r == 0x7f:
			dst = appendHexEscape(dst, 'x', r, 2)
		case r < utf8.RuneSelf || isPyPrintable(r):
			dst = utf8.AppendRune(dst, r)
		case r <= 0xff:
			dst = appendHexEscape(dst, 'x', r, 2)
		case r <= 0xffff:
			dst = appendHexEscape(dst, 'u', r, 4)
		default:
			dst = appendHexEscape(dst, 'U', r, 8)
		}
	}
	return append(dst, quote)
}

// isPyPrintable reports whether r is printable for CPython 3.14.3's
// str.isprintable, the test its repr escapes by. CPython 3.14.3 holds the
// character data of Unicode 16.0.0 (unicodedata.unidata_version) and Go
// 1.27.1 that of Unicode 17.0.0 (unicode.Version), so unicode.IsPrint also
// accepts the characters Unicode 17.0 assigned: over every code point it
// accepts the 4803 of notPyPrintable that str.isprintable refuses, and
// refuses none that str.isprintable accepts.
func isPyPrintable(r rune) bool {
	return unicode.IsPrint(r) && !unicode.Is(notPyPrintable, r)
}

// notPyPrintable holds the code points that Go 1.27.1's unicode.IsPrint
// accepts and CPython 3.14.3's str.isprintable refuses: 4803 code points in
// 47 ranges, each unassigned in Unicode 16.0.0. A Go release with the
// character data of another Unicode version needs the table measured again.
var notPyPrintable = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x088f, Hi: 0x088f, Stride: 1},
		{Lo: 0x0c5c, Hi: 0x0c5c, Stride: 1},
		{Lo: 0x0cdc, Hi: 0x0cdc, Stride: 1},
		{Lo: 0x1acf, Hi: 0x1add, Stride: 1},
		{Lo: 0x1ae0, Hi: 0x1aeb, Stride: 1},
		{Lo: 0x20c1, Hi: 0x20c1, Stride: 1},
		{Lo: 0x2b96, Hi: 0x2b96, Stride: 1},
		{Lo: 0xa7ce, Hi: 0xa7cf, Stride: 1},
		{Lo: 0xa7d2, Hi: 0xa7d2, Stride: 1},
		{Lo: 0xa7d4, Hi: 0xa7d4, Stride: 1},
		{Lo: 0xa7f1, Hi: 0xa7f1, Stride: 1},
		{Lo: 0xfbc3, Hi: 0xfbd2, Stride: 1},
		{Lo: 0xfd90, Hi: 0xfd91, Stride: 1},
		{Lo: 0xfdc8, Hi: 0xfdce, Stride: 1},
	},
	R32: []unicode.Range32{
		{Lo: 0x10940, Hi: 0x10959, Stride: 1},
		{Lo: 0x10ec5, Hi: 0x10ec7, Stride: 1},
		{Lo: 0x10ed0, Hi: 0x10ed8, Stride: 1},
		{Lo: 0x10efa, Hi: 0x10efb, Stride: 1},
		{Lo: 0x11b60, Hi: 0x11b67, Stride: 1},
		{Lo: 0x11db0, Hi: 0x11ddb, Stride: 1},
		{Lo: 0x11de0, Hi: 0x11de9, Stride: 1},
		{Lo: 0x16ea0, Hi: 0x16eb8, Stride: 1},
		{Lo: 0x16ebb, Hi: 0x16ed3, Stride: 1},
		{Lo: 0x16ff2, Hi: 0x16ff6, Stride: 1},
		{Lo: 0x187f8, Hi: 0x187ff, Stride: 1},
		{Lo: 0x18d09, Hi: 0x18d1e, Stride: 1},
		{Lo: 0x18d80, Hi: 0x18df2, Stride: 1},
		{Lo: 0x1ccfa, Hi: 0x1ccfc, Stride: 1},
		{Lo: 0x1ceba, Hi: 0x1ced0, Stride: 1},
		{Lo: 0x1cee0, Hi: 0x1cef0, Stride: 1},
		{Lo: 0x1e6c0, Hi: 0x1e6de, Stride: 1},
		{Lo: 0x1e6e0, Hi: 0x1e6f5, Stride: 1},
		{Lo: 0x1e6fe, Hi: 0x1e6ff, Stride: 1},
		{Lo: 0x1f6d8, Hi: 0x1f6d8, Stride: 1},
		{Lo: 0x1f777, Hi: 0x1f77a, Stride: 1},
		{Lo: 0x1f8d0, Hi: 0x1f8d8, Stride: 1},
		{Lo: 0x1fa54, Hi: 0x1fa57, Stride: 1},
		{Lo: 0x1fa8a, Hi: 0x1fa8a, Stride: 1},
		{Lo: 0x1fa8e, Hi: 0x1fa8e, Stride: 1},
		{Lo: 0x1fac8, Hi: 0x1fac8, Stride: 1},
		{Lo: 0x1facd, Hi: 0x1facd, Stride: 1},
		{Lo: 0x1faea, Hi: 0x1faea, Stride: 1},
		{Lo: 0x1faef, Hi: 0x1faef, Stride: 1},
		{Lo: 0x1fbfa, Hi: 0x1fbfa, Stride: 1},
		{Lo: 0x2b73a, Hi: 0x2b73f, Stride: 1},
		{Lo: 0x2cea2, Hi: 0x2cead, Stride: 1},
		{Lo: 0x323b0, Hi: 0x33479, Stride: 1},
	},
}

// appendHexEscape appends a backslash, letter and r in width lowercase hex
// digits.
func appendHexEscape(dst []byte, letter byte, r rune, width int) []byte {
	dst = append(dst, '\\', letter)
	hex := strconv.FormatInt(int64(r), 16)
	for range width - len(hex) {
		dst = append(dst, '0')
	}
	return append(dst, hex...)
}

// decodeReplace returns b as Python's b.decode("utf-8", "replace") does:
// each maximal ill-formed subpart (Unicode 16.0, section 3.9, table 3-8)
// becomes one U+FFFD, so a truncated sequence gives one replacement, not
// one per byte as Go's decoding does.
func decodeReplace(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b) + 8)
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r != utf8.RuneError || n > 1 {
			sb.Write(b[:n])
			b = b[n:]
			continue
		}
		sb.WriteRune(utf8.RuneError)
		b = b[maximalSubpart(b):]
	}
	return sb.String()
}

// maximalSubpart returns the length, at least 1, of the longest prefix of b
// that begins a well-formed UTF-8 sequence (Unicode 16.0, table 3-7), for a
// b that does not begin with one.
func maximalSubpart(b []byte) int {
	lo, hi, n := byte(0x80), byte(0xbf), 0
	switch c := b[0]; {
	case c >= 0xc2 && c <= 0xdf:
		n = 2
	case c == 0xe0:
		lo, n = 0xa0, 3
	case c == 0xed:
		hi, n = 0x9f, 3
	case c >= 0xe1 && c <= 0xef:
		n = 3
	case c == 0xf0:
		lo, n = 0x90, 4
	case c >= 0xf1 && c <= 0xf3:
		n = 4
	case c == 0xf4:
		hi, n = 0x8f, 4
	default:
		return 1
	}
	i := 1
	for ; i < n && i < len(b); i++ {
		if b[i] < lo || b[i] > hi {
			break
		}
		lo, hi = 0x80, 0xbf
	}
	return i
}
