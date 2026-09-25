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
	"math"
	"strconv"
	"strings"
	"testing"
)

// controls is U+0000 to U+001F, in order.
var controls = func() string {
	var b strings.Builder
	for c := range 0x20 {
		b.WriteByte(byte(c))
	}
	return b.String()
}()

func TestAppendString(t *testing.T) {
	tests := map[string]struct {
		s       string
		want    string
		wantErr error
	}{
		"success: empty":      {s: "", want: `""`},
		"success: plain text": {s: "Is this about billing?", want: `"Is this about billing?"`},
		"success: quote and backslash": {
			s:    `a"b\c`,
			want: `"a\"b\\c"`,
		},
		"success: the solidus is not escaped": {s: "a/b", want: `"a/b"`},
		// The escape table of Python's to_json (probe of typesafe-sdk-python
		// 0.7.1): five short forms, the other controls as lower-case \u00XX.
		"success: every control character": {
			s: controls,
			want: `"\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\b\t\n\u000b\f\r\u000e\u000f` +
				`\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001a\u001b\u001c\u001d\u001e\u001f"`,
		},
		"success: DEL, HTML characters, U+2028, U+2029 and non-ASCII are copied": {
			s:    "\x7f<>&\u00e9\u2028\u2029\U0001f600",
			want: "\"\x7f<>&\u00e9\u2028\u2029\U0001f600\"",
		},
		"success: escapes between runs of plain text": {s: "a\nbc\td", want: `"a\nbc\td"`},
		"error: a lone continuation byte":             {s: "ok\x80", wantErr: ErrInvalidUTF8},
		"error: a truncated sequence":                 {s: "\xe2\x82", wantErr: ErrInvalidUTF8},
		"error: an encoded surrogate":                 {s: "\xed\xa0\x80", wantErr: ErrInvalidUTF8},
		"error: 0xff":                                 {s: "\xff", wantErr: ErrInvalidUTF8},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			prefix := []byte("prefix:")
			got, err := AppendString(prefix, tt.s)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("AppendString(%q) error = %v, want %v", tt.s, err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if string(got) != "prefix:" {
					t.Errorf("AppendString(%q) on error = %q, want dst unchanged", tt.s, got)
				}
				return
			}
			if want := "prefix:" + tt.want; string(got) != want {
				t.Errorf("AppendString(%q) = %q, want %q", tt.s, got, want)
			}
		})
	}
}

func TestAppendJSON(t *testing.T) {
	deep := strings.Repeat("[", 100) + strings.Repeat("]", 100)
	tests := map[string]struct {
		raw        string
		want       string
		wantOffset int    // for a *SyntaxError
		wantMsg    string // for a *SyntaxError
	}{
		"success: literals":              {raw: `[true,false,null]`, want: `[true,false,null]`},
		"success: a bare literal":        {raw: ` null `, want: `null`},
		"success: numbers keep spelling": {raw: `[0,-0,1.50,-12e+3,4E-2,1e400,123456789012345678901234567890]`, want: `[0,-0,1.50,-12e+3,4E-2,1e400,123456789012345678901234567890]`},
		"success: strings keep escapes":  {raw: `"a\"b\\c\/d\b\f\n\r\t\u00e9\uD83D\uDE00"`, want: `"a\"b\\c\/d\b\f\n\r\t\u00e9\uD83D\uDE00"`},
		"success: UTF-8 in a string":     {raw: "\"\u00e9\u2028\U0001f600\"", want: "\"\u00e9\u2028\U0001f600\""},
		"success: whitespace removed": {
			raw:  "{\n  \"text\" : \"Classify\",\r\n\t\"extra\" : null ,\n  \"items\" : [ 1 , [ ] , { } ]\n}\n",
			want: `{"text":"Classify","extra":null,"items":[1,[],{}]}`,
		},
		"success: whitespace inside strings kept":                 {raw: `{ "a b" : " c d " }`, want: `{"a b":" c d "}`},
		"success: empty containers with spaces":                   {raw: `[ { } , [ ] ]`, want: `[{},[]]`},
		"success: nesting deeper than the stack's first array":    {raw: deep, want: deep},
		"success: a lone surrogate escape is syntax, not checked": {raw: `"\ud800"`, want: `"\ud800"`},

		"error: empty input":                   {raw: ``, wantOffset: 0, wantMsg: "unexpected end of input, want a value"},
		"error: whitespace only":               {raw: " \n ", wantOffset: 3, wantMsg: "unexpected end of input, want a value"},
		"error: trailing data":                 {raw: `{"a":1} x`, wantOffset: 8, wantMsg: `unexpected "x" after the value`},
		"error: two values":                    {raw: `{}{}`, wantOffset: 2, wantMsg: `unexpected "{" after the value`},
		"error: unterminated array":            {raw: `[1`, wantOffset: 2, wantMsg: "unexpected end of input, want ',' or a closing bracket"},
		"error: unterminated object":           {raw: `{"a":1`, wantOffset: 6, wantMsg: "unexpected end of input, want ',' or a closing bracket"},
		"error: a trailing comma in an array":  {raw: `[1,]`, wantOffset: 3, wantMsg: `unexpected "]", want a value`},
		"error: a trailing comma in an object": {raw: `{"a":1,}`, wantOffset: 7, wantMsg: `unexpected "}", want a member name`},
		"error: mismatched brackets":           {raw: `[1}`, wantOffset: 2, wantMsg: `unexpected "}", want ',' or a closing bracket`},
		"error: a missing comma":               {raw: `[1 2]`, wantOffset: 3, wantMsg: `unexpected "2", want ',' or a closing bracket`},
		"error: a number as a member name":     {raw: `{1:2}`, wantOffset: 1, wantMsg: `unexpected "1", want a member name`},
		"error: object ends after a comma":     {raw: `{"a":1,`, wantOffset: 7, wantMsg: "unexpected end of input, want a member name"},
		"error: a missing colon":               {raw: `{"a" 1}`, wantOffset: 5, wantMsg: "want ':' after a member name"},
		"error: an invalid member name":        {raw: `{"a\q":1}`, wantOffset: 3, wantMsg: `invalid escape sequence "q"`},
		"error: an unexpected byte":            {raw: `[@]`, wantOffset: 1, wantMsg: `unexpected "@", want a value`},
		"error: an unterminated string":        {raw: `"abc`, wantOffset: 4, wantMsg: "unterminated string"},
		"error: an escape at the end":          {raw: `"abc\`, wantOffset: 4, wantMsg: "unterminated escape sequence"},
		"error: an invalid escape":             {raw: `"\q"`, wantOffset: 1, wantMsg: `invalid escape sequence "q"`},
		"error: a short unicode escape":        {raw: `"\u12"`, wantOffset: 1, wantMsg: `invalid \u escape sequence`},
		"error: a non-hex unicode escape":      {raw: `"\u12g4"`, wantOffset: 1, wantMsg: `invalid \u escape sequence`},
		"error: a raw control character":       {raw: "\"a\nb\"", wantOffset: 2, wantMsg: `raw control character "\n" in a string`},
		"error: invalid UTF-8 in a string":     {raw: "\"a\xffb\"", wantOffset: 2, wantMsg: "invalid UTF-8 in a string"},
		"error: a truncated literal":           {raw: `tru`, wantOffset: 0, wantMsg: "invalid literal, want true"},
		"error: a misspelled literal":          {raw: `[nul]`, wantOffset: 1, wantMsg: "invalid literal, want null"},
		"error: a false prefix":                {raw: `fals`, wantOffset: 0, wantMsg: "invalid literal, want false"},
		"error: a leading zero":                {raw: `01`, wantOffset: 1, wantMsg: `unexpected "1" after the value`},
		"error: a lone minus":                  {raw: `-`, wantOffset: 1, wantMsg: "invalid number, want a digit"},
		"error: a minus before a letter":       {raw: `-a`, wantOffset: 1, wantMsg: "invalid number, want a digit"},
		"error: a leading plus":                {raw: `+1`, wantOffset: 0, wantMsg: `unexpected "+", want a value`},
		"error: a leading dot":                 {raw: `.5`, wantOffset: 0, wantMsg: `unexpected ".", want a value`},
		"error: no digit after the dot":        {raw: `1.`, wantOffset: 2, wantMsg: "invalid number, want a digit after '.'"},
		"error: no digit in the exponent":      {raw: `1e+`, wantOffset: 3, wantMsg: "invalid number, want a digit in the exponent"},
		"error: a byte-order mark":             {raw: "\xef\xbb\xbf{}", wantOffset: 0, wantMsg: `unexpected "\xef", want a value`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			prefix := []byte("prefix:")
			got, err := AppendJSON(prefix, []byte(tt.raw))
			if tt.wantMsg == "" {
				if err != nil {
					t.Fatalf("AppendJSON(%q) error = %v, want nil", tt.raw, err)
				}
				if want := "prefix:" + tt.want; string(got) != want {
					t.Errorf("AppendJSON(%q) = %q, want %q", tt.raw, got, want)
				}
				return
			}
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("AppendJSON(%q) error = %v, want a *SyntaxError", tt.raw, err)
			}
			if se.Offset != tt.wantOffset || se.msg != tt.wantMsg {
				t.Errorf("AppendJSON(%q) error = offset %d %q, want offset %d %q", tt.raw, se.Offset, se.msg, tt.wantOffset, tt.wantMsg)
			}
			if want := "invalid JSON at byte " + strconv.Itoa(tt.wantOffset) + ": " + tt.wantMsg; err.Error() != want {
				t.Errorf("Error() = %q, want %q", err, want)
			}
			if string(got) != "prefix:" {
				t.Errorf("AppendJSON(%q) on error = %q, want dst unchanged", tt.raw, got)
			}
		})
	}
}

func TestAppendContent(t *testing.T) {
	tests := map[string]struct {
		c       Content
		want    string
		wantErr error
		syntax  bool // the error is a *SyntaxError
	}{
		"success: text":                       {c: Content{Text: "a\"b"}, want: `"a\"b"`},
		"success: empty text":                 {c: Content{}, want: `""`},
		"success: an object, compacted":       {c: Content{JSON: []byte(` { "a" : [ 1 ] } `)}, want: `{"a":[1]}`},
		"success: an array":                   {c: Content{JSON: []byte(`["Example",null]`)}, want: `["Example",null]`},
		"error: text that is not UTF-8":       {c: Content{Text: "\xff"}, wantErr: ErrInvalidUTF8},
		"error: a JSON string is not content": {c: Content{JSON: []byte(`"text"`)}, wantErr: ErrContentShape},
		"error: a JSON number":                {c: Content{JSON: []byte(` 1`)}, wantErr: ErrContentShape},
		"error: null":                         {c: Content{JSON: []byte(`null`)}, wantErr: ErrContentShape},
		"error: empty JSON":                   {c: Content{JSON: []byte{}}, wantErr: ErrContentShape},
		"error: whitespace-only JSON":         {c: Content{JSON: []byte("  ")}, wantErr: ErrContentShape},
		"error: an unterminated object":       {c: Content{JSON: []byte(`{"a":`)}, syntax: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := AppendContent(nil, tt.c)
			if tt.syntax {
				if _, ok := errors.AsType[*SyntaxError](err); !ok {
					t.Fatalf("AppendContent error = %v, want a *SyntaxError", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("AppendContent error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && string(got) != tt.want {
				t.Errorf("AppendContent = %q, want %q", got, tt.want)
			}
		})
	}
}

// leafType is a value only the test leaf knows.
type leafType struct{ s string }

// testLeaf writes a leafType as its string, and fails on "fail".
func testLeaf(dst []byte, v any) ([]byte, bool, error) {
	l, ok := v.(leafType)
	if !ok {
		return dst, false, nil
	}
	if l.s == "fail" {
		return dst, true, errors.New("leaf refused")
	}
	return append(dst, l.s...), true, nil
}

func TestBuilderRawValues(t *testing.T) {
	tests := map[string]struct {
		v    any
		want string
	}{
		"success: nil":             {v: nil, want: `null`},
		"success: booleans":        {v: []any{true, false}, want: `[true,false]`},
		"success: a string":        {v: "a\"b", want: `"a\"b"`},
		"success: signed integers": {v: []any{int(-1), int8(-8), int16(-16), int32(-32), int64(math.MinInt64)}, want: `[-1,-8,-16,-32,-9223372036854775808]`},
		"success: unsigned integers": {
			v:    []any{uint(1), uint8(8), uint16(16), uint32(32), uint64(math.MaxUint64)},
			want: `[1,8,16,32,18446744073709551615]`,
		},
		// encoding/json's spelling, which sonic shares; Python writes 3.0
		// and 1e-6 for the third and sixth, the same numbers.
		"success: float64 spellings": {
			v:    []any{1.5, -7.0, 3.0, 1e21, 1e-7, 0.000001, 123456789.0, math.Copysign(0, -1), 1e20},
			want: `[1.5,-7,3,1e+21,1e-7,0.000001,123456789,-0,100000000000000000000]`,
		},
		"success: float32 spellings":               {v: []any{float32(0.1), float32(1e21), float32(1e-7)}, want: `[0.1,1e+21,1e-7]`},
		"success: a three-digit negative exponent": {v: 1e-100, want: `1e-100`},
		"success: []string":                        {v: []string{"a", "b\n"}, want: `["a","b\n"]`},
		"success: an empty []any":                  {v: []any{}, want: `[]`},
		"success: map[string]any in sorted key order": {
			v:    map[string]any{"b": 1, "a": map[string]any{"d": nil, "c": []any{"x"}}},
			want: `{"a":{"c":["x"],"d":null},"b":1}`,
		},
		"success: map[string]string in sorted key order": {v: map[string]string{"z": "1", "y": "2"}, want: `{"y":"2","z":"1"}`},
		"success: an empty map":                          {v: map[string]any{}, want: `{}`},
		"success: a leaf value":                          {v: []any{leafType{s: `{"k":1}`}}, want: `[{"k":1}]`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var b Builder
			if err := b.Raw("r", "future", map[string]any{"v": tt.v}, testLeaf); err != nil {
				t.Fatalf("Raw: %v", err)
			}
			p, err := b.Prepared()
			if err != nil {
				t.Fatalf("Prepared: %v", err)
			}
			if want := `{"r":{"type":"future","v":` + tt.want + `}}`; string(p.Questions) != want {
				t.Errorf("Questions = %s, want %s", p.Questions, want)
			}
		})
	}
}
