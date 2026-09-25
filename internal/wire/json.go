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
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"unicode/utf8"
)

// Errors of the question serialiser.
var (
	// ErrInvalidUTF8 reports a string that is not valid UTF-8. JSON text is
	// Unicode, and the Python SDK cannot produce such a string at all.
	ErrInvalidUTF8 = errors.New("string is not valid UTF-8")
	// ErrContentShape reports JSON content that is not an object or an array:
	// the API takes text, a JSON object or a JSON array where it takes
	// content, and text is written from a string, never from JSON.
	ErrContentShape = errors.New("JSON content must be an object or an array")
	// ErrUnsupportedValue reports a raw question field value of a type the
	// serialiser does not write, or a float that JSON cannot represent.
	ErrUnsupportedValue = errors.New("unsupported value")
)

// SyntaxError reports JSON text that is not exactly one valid JSON value.
type SyntaxError struct {
	// Offset is the byte offset in the input at which the problem was found.
	Offset int
	msg    string
}

// Error returns the problem and its offset.
func (e *SyntaxError) Error() string {
	return "invalid JSON at byte " + strconv.Itoa(e.Offset) + ": " + e.msg
}

// hexDigits spells the \u00XX escapes in lower case, as Python's to_json and
// sonic do.
const hexDigits = "0123456789abcdef"

// AppendString appends s to dst as a JSON string: quoted, with '"' and '\'
// escaped, U+0008, U+0009, U+000A, U+000C and U+000D written as \b, \t, \n,
// \f and \r, and the other characters below U+0020 as \u00XX. Everything else
// is copied as is: no HTML escaping, no escaping of U+007F, U+2028, U+2029 or
// any other non-ASCII character. The bytes are those typesafe-sdk-python's
// to_json writes for the same string.
//
// It fails with [ErrInvalidUTF8], returning dst unchanged, when s is not
// valid UTF-8.
func AppendString(dst []byte, s string) ([]byte, error) {
	n0 := len(dst)
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				return dst[:n0], ErrInvalidUTF8
			}
			i += size
			continue
		}
		if c >= 0x20 && c != '"' && c != '\\' {
			i++
			continue
		}
		dst = append(dst, s[start:i]...)
		switch c {
		case '"', '\\':
			dst = append(dst, '\\', c)
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\t':
			dst = append(dst, '\\', 't')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\r':
			dst = append(dst, '\\', 'r')
		default:
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
		}
		i++
		start = i
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"'), nil
}

// AppendJSON appends raw, which must hold exactly one JSON value (RFC 8259)
// with optional whitespace around it, to dst without its insignificant
// whitespace. Everything else is copied byte for byte: strings keep their
// escape sequences, numbers their spelling.
//
// The value is checked, not decoded: structure, literals, number grammar,
// string escapes, and that every string is valid UTF-8 without a raw control
// character. A failure is a [*SyntaxError], and dst is returned unchanged.
// Nesting depth is not limited here; the scan is iterative and its memory
// grows with the depth only.
func AppendJSON(dst, raw []byte) ([]byte, error) {
	n0 := len(dst)
	var small [32]byte
	stack := small[:0] // the open containers, '{' or '['
	i := skipSpace(raw, 0)
	var err error
value:
	for {
		// A value starts at i.
		if i == len(raw) {
			return dst[:n0], &SyntaxError{Offset: i, msg: "unexpected end of input, want a value"}
		}
		switch c := raw[i]; {
		case c == '{' || c == '[':
			end := byte('}')
			if c == '[' {
				end = ']'
			}
			dst = append(dst, c)
			i = skipSpace(raw, i+1)
			if i < len(raw) && raw[i] == end {
				dst = append(dst, end)
				i++
				break
			}
			stack = append(stack, c)
			if c == '{' {
				if dst, i, err = appendMemberName(dst, raw, i); err != nil {
					return dst[:n0], err
				}
			}
			continue
		case c == '"':
			dst, i, err = appendStringToken(dst, raw, i)
		case c == 't':
			dst, i, err = appendLiteral(dst, raw, i, "true")
		case c == 'f':
			dst, i, err = appendLiteral(dst, raw, i, "false")
		case c == 'n':
			dst, i, err = appendLiteral(dst, raw, i, "null")
		case c == '-' || '0' <= c && c <= '9':
			dst, i, err = appendNumber(dst, raw, i)
		default:
			err = &SyntaxError{Offset: i, msg: "unexpected " + quoteByte(c) + ", want a value"}
		}
		if err != nil {
			return dst[:n0], err
		}
		// A value ended at i: close containers until one continues.
		for {
			i = skipSpace(raw, i)
			if len(stack) == 0 {
				if i != len(raw) {
					return dst[:n0], &SyntaxError{Offset: i, msg: "unexpected " + quoteByte(raw[i]) + " after the value"}
				}
				return dst, nil
			}
			if i == len(raw) {
				return dst[:n0], &SyntaxError{Offset: i, msg: "unexpected end of input, want ',' or a closing bracket"}
			}
			top, c := stack[len(stack)-1], raw[i]
			switch {
			case c == ',':
				dst = append(dst, ',')
				i = skipSpace(raw, i+1)
				if top == '{' {
					if dst, i, err = appendMemberName(dst, raw, i); err != nil {
						return dst[:n0], err
					}
				}
				continue value
			case top == '{' && c == '}', top == '[' && c == ']':
				dst = append(dst, c)
				stack = stack[:len(stack)-1]
				i++
			default:
				return dst[:n0], &SyntaxError{Offset: i, msg: "unexpected " + quoteByte(c) + ", want ',' or a closing bracket"}
			}
		}
	}
}

// skipSpace returns the offset of the first byte at or after i that is not
// JSON whitespace (space, tab, line feed, carriage return).
func skipSpace(raw []byte, i int) int {
	for i < len(raw) {
		switch raw[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

// quoteByte spells c for an error message.
func quoteByte(c byte) string { return strconv.Quote(string([]byte{c})) }

// appendMemberName appends the member name at raw[i:] and the colon after it,
// and returns the offset of the member's value.
func appendMemberName(dst, raw []byte, i int) ([]byte, int, error) {
	if i == len(raw) || raw[i] != '"' {
		if i == len(raw) {
			return dst, i, &SyntaxError{Offset: i, msg: "unexpected end of input, want a member name"}
		}
		return dst, i, &SyntaxError{Offset: i, msg: "unexpected " + quoteByte(raw[i]) + ", want a member name"}
	}
	dst, i, err := appendStringToken(dst, raw, i)
	if err != nil {
		return dst, i, err
	}
	i = skipSpace(raw, i)
	if i == len(raw) || raw[i] != ':' {
		return dst, i, &SyntaxError{Offset: i, msg: "want ':' after a member name"}
	}
	return append(dst, ':'), skipSpace(raw, i+1), nil
}

// appendStringToken appends the JSON string that starts at raw[i] ('"') and
// returns the offset just past its closing quote.
func appendStringToken(dst, raw []byte, i int) ([]byte, int, error) {
	for j := i + 1; j < len(raw); {
		switch c := raw[j]; {
		case c == '"':
			return append(dst, raw[i:j+1]...), j + 1, nil
		case c == '\\':
			if j+1 == len(raw) {
				return dst, j, &SyntaxError{Offset: j, msg: "unterminated escape sequence"}
			}
			switch raw[j+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				j += 2
			case 'u':
				if j+6 > len(raw) || !isHex(raw[j+2]) || !isHex(raw[j+3]) || !isHex(raw[j+4]) || !isHex(raw[j+5]) {
					return dst, j, &SyntaxError{Offset: j, msg: `invalid \u escape sequence`}
				}
				j += 6
			default:
				return dst, j, &SyntaxError{Offset: j, msg: "invalid escape sequence " + quoteByte(raw[j+1])}
			}
		case c < 0x20:
			return dst, j, &SyntaxError{Offset: j, msg: "raw control character " + quoteByte(c) + " in a string"}
		case c < utf8.RuneSelf:
			j++
		default:
			r, size := utf8.DecodeRune(raw[j:])
			if r == utf8.RuneError && size == 1 {
				return dst, j, &SyntaxError{Offset: j, msg: "invalid UTF-8 in a string"}
			}
			j += size
		}
	}
	return dst, len(raw), &SyntaxError{Offset: len(raw), msg: "unterminated string"}
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// appendLiteral appends the literal lit ("true", "false" or "null") that must
// start at raw[i].
func appendLiteral(dst, raw []byte, i int, lit string) ([]byte, int, error) {
	if len(raw)-i < len(lit) || string(raw[i:i+len(lit)]) != lit {
		return dst, i, &SyntaxError{Offset: i, msg: "invalid literal, want " + lit}
	}
	return append(dst, lit...), i + len(lit), nil
}

// appendNumber appends the number that starts at raw[i]:
// -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?.
func appendNumber(dst, raw []byte, i int) ([]byte, int, error) {
	j := i
	if raw[j] == '-' {
		j++
	}
	switch {
	case j < len(raw) && raw[j] == '0':
		j++
	case j < len(raw) && '1' <= raw[j] && raw[j] <= '9':
		j = skipDigits(raw, j+1)
	default:
		return dst, j, &SyntaxError{Offset: j, msg: "invalid number, want a digit"}
	}
	if j < len(raw) && raw[j] == '.' {
		k := skipDigits(raw, j+1)
		if k == j+1 {
			return dst, k, &SyntaxError{Offset: k, msg: "invalid number, want a digit after '.'"}
		}
		j = k
	}
	if j < len(raw) && (raw[j] == 'e' || raw[j] == 'E') {
		j++
		if j < len(raw) && (raw[j] == '+' || raw[j] == '-') {
			j++
		}
		k := skipDigits(raw, j)
		if k == j {
			return dst, k, &SyntaxError{Offset: k, msg: "invalid number, want a digit in the exponent"}
		}
		j = k
	}
	return append(dst, raw[i:j]...), j, nil
}

func skipDigits(raw []byte, i int) int {
	for i < len(raw) && '0' <= raw[i] && raw[i] <= '9' {
		i++
	}
	return i
}

// AppendContent appends c to dst: text as a JSON string ([AppendString]), a
// JSON object or array compacted ([AppendJSON]). JSON content that holds
// anything else fails with [ErrContentShape].
func AppendContent(dst []byte, c Content) ([]byte, error) {
	if !c.IsJSON() {
		return AppendString(dst, c.Text)
	}
	if i := skipSpace(c.JSON, 0); i == len(c.JSON) || c.JSON[i] != '{' && c.JSON[i] != '[' {
		return dst, ErrContentShape
	}
	return AppendJSON(dst, c.JSON)
}

// Leaf appends the JSON of a raw question field value of a type the
// serialiser does not know, and reports whether it knew it. The root package
// passes one for its own types.
type Leaf func(dst []byte, v any) ([]byte, bool, error)

// maxValueDepth bounds the nesting of a raw question field value. Go maps and
// slices held in interfaces can form a cycle, which would otherwise recurse
// until the stack overflows; encoding/json stops at the same depth.
const maxValueDepth = 1000

// valueError is a failure inside a raw field value, with the path to the
// value that failed; Builder.Raw turns it into a *MemberError.
type valueError struct {
	path string
	err  error
}

// appendValue appends the JSON of v, one of: nil, bool, string, the integer
// and float kinds, []any, []string, map[string]any, map[string]string, or a
// value leaf knows. Map members are written in sorted key order.
func appendValue(dst []byte, v any, leaf Leaf, depth int) ([]byte, *valueError) {
	if depth > maxValueDepth {
		return dst, &valueError{err: fmt.Errorf("%w: nested more than %d levels deep (a cycle?)", ErrUnsupportedValue, maxValueDepth)}
	}
	var err error
	switch v := v.(type) {
	case nil:
		return append(dst, "null"...), nil
	case bool:
		return strconv.AppendBool(dst, v), nil
	case string:
		dst, err = AppendString(dst, v)
	case int:
		return strconv.AppendInt(dst, int64(v), 10), nil
	case int8:
		return strconv.AppendInt(dst, int64(v), 10), nil
	case int16:
		return strconv.AppendInt(dst, int64(v), 10), nil
	case int32:
		return strconv.AppendInt(dst, int64(v), 10), nil
	case int64:
		return strconv.AppendInt(dst, v, 10), nil
	case uint:
		return strconv.AppendUint(dst, uint64(v), 10), nil
	case uint8:
		return strconv.AppendUint(dst, uint64(v), 10), nil
	case uint16:
		return strconv.AppendUint(dst, uint64(v), 10), nil
	case uint32:
		return strconv.AppendUint(dst, uint64(v), 10), nil
	case uint64:
		return strconv.AppendUint(dst, v, 10), nil
	case float32:
		dst, err = appendFloat(dst, float64(v), 32)
	case float64:
		dst, err = appendFloat(dst, v, 64)
	case []any:
		dst = append(dst, '[')
		for i, e := range v {
			if i > 0 {
				dst = append(dst, ',')
			}
			var verr *valueError
			if dst, verr = appendValue(dst, e, leaf, depth+1); verr != nil {
				verr.path = "[" + strconv.Itoa(i) + "]" + verr.path
				return dst, verr
			}
		}
		return append(dst, ']'), nil
	case []string:
		dst = append(dst, '[')
		for i, e := range v {
			if i > 0 {
				dst = append(dst, ',')
			}
			if dst, err = AppendString(dst, e); err != nil {
				return dst, &valueError{path: "[" + strconv.Itoa(i) + "]", err: err}
			}
		}
		return append(dst, ']'), nil
	case map[string]any:
		dst = append(dst, '{')
		for i, k := range slices.Sorted(maps.Keys(v)) {
			if i > 0 {
				dst = append(dst, ',')
			}
			if dst, err = AppendString(dst, k); err != nil {
				return dst, &valueError{path: "." + k, err: err}
			}
			dst = append(dst, ':')
			var verr *valueError
			if dst, verr = appendValue(dst, v[k], leaf, depth+1); verr != nil {
				verr.path = "." + k + verr.path
				return dst, verr
			}
		}
		return append(dst, '}'), nil
	case map[string]string:
		dst = append(dst, '{')
		for i, k := range slices.Sorted(maps.Keys(v)) {
			if i > 0 {
				dst = append(dst, ',')
			}
			if dst, err = AppendString(dst, k); err != nil {
				return dst, &valueError{path: "." + k, err: err}
			}
			dst = append(dst, ':')
			if dst, err = AppendString(dst, v[k]); err != nil {
				return dst, &valueError{path: "." + k, err: err}
			}
		}
		return append(dst, '}'), nil
	default:
		var ok bool
		if leaf != nil {
			dst, ok, err = leaf(dst, v)
		}
		if !ok {
			err = fmt.Errorf("%w of type %T", ErrUnsupportedValue, v)
		}
	}
	if err != nil {
		return dst, &valueError{err: err}
	}
	return dst, nil
}

// appendFloat appends f as encoding/json and sonic spell a float: the
// shortest representation that reads back as f, in exponent form below 1e-6
// and from 1e21 on. Python's to_json spells some of those values differently
// (3.0 for 3, 1e-6 for 0.000001); the numbers are equal. NaN and the
// infinities are not JSON and fail with [ErrUnsupportedValue].
func appendFloat(dst []byte, f float64, bits int) ([]byte, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return dst, fmt.Errorf("%w: %v is not a JSON number", ErrUnsupportedValue, f)
	}
	format := byte('f')
	if abs := math.Abs(f); abs != 0 {
		if bits == 64 && (abs < 1e-6 || abs >= 1e21) || bits == 32 && (float32(abs) < 1e-6 || float32(abs) >= 1e21) {
			format = 'e'
		}
	}
	dst = strconv.AppendFloat(dst, f, format, -1, bits)
	if format == 'e' {
		// Shorten a two-digit negative exponent, e-07 to e-7, as
		// encoding/json does.
		if n := len(dst); n >= 4 && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
			dst[n-2] = dst[n-1]
			dst = dst[:n-1]
		}
	}
	return dst, nil
}
