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
	"bytes"
	"errors"
	"fmt"
	"maps"
	"math"
	"runtime/debug"
	"slices"
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
		// The edges of the two byte classes the scanner draws a line through:
		// U+001F is the last control character JSON forbids raw inside a
		// string and U+007F is allowed; only space, tab, line feed and
		// carriage return separate tokens.
		"error: a raw U+001F inside a string":    {raw: "\"\x1f\"", wantOffset: 1, wantMsg: `raw control character "\x1f" in a string`},
		"success: a raw U+007F inside a string":  {raw: "\"\x7f\"", want: "\"\x7f\""},
		"error: a form feed between tokens":      {raw: "[1\f]", wantOffset: 2, wantMsg: `unexpected "\f", want ',' or a closing bracket`},
		"error: a vertical tab between tokens":   {raw: "[1\v]", wantOffset: 2, wantMsg: `unexpected "\v", want ',' or a closing bracket`},
		"error: a no-break space between tokens": {raw: "[1\xc2\xa0]", wantOffset: 2, wantMsg: `unexpected "\xc2", want ',' or a closing bracket`},
		"success: a space between tokens":        {raw: "[1 ]", want: "[1]"},
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
		// Floats in the layout of pydantic-core's writer (zmij), one case per
		// branch of appendFloat; the root package's
		// TestPreparedBytesMatchPython pins these layouts against Python's
		// own output.
		"success: zero keeps its sign": {v: []any{0.0, math.Copysign(0, -1)}, want: `[0.0,-0.0]`},
		"success: integral values below 1e16 end in .0": {
			v:    []any{3.0, -7.0, 123456789.0, 1e15, 9999999999999998.0},
			want: `[3.0,-7.0,123456789.0,1000000000000000.0,9999999999999998.0]`,
		},
		"success: a point inside the digits": {v: []any{1.5, -123.456, 999999999999999.9}, want: `[1.5,-123.456,999999999999999.9]`},
		"success: fixed notation down to 1e-5": {
			v:    []any{0.1, 0.00001, 0.00001234, -0.00001},
			want: `[0.1,0.00001,0.00001234,-0.00001]`,
		},
		"success: exponent form below 1e-5": {v: []any{9.99e-6, 1e-6, -1e-7, 5e-324}, want: `[9.99e-6,1e-6,-1e-7,5e-324]`},
		"success: exponent form from 1e16": {
			v:    []any{1e16, 1e20, 1e21, 12345678901234567168.0, 1.7976931348623157e308},
			want: `[1e+16,1e+20,1e+21,1.2345678901234567e+19,1.7976931348623157e+308]`,
		},
		"success: three-digit exponents": {v: []any{1e-100, 1e100}, want: `[1e-100,1e+100]`},
		"success: float32 from its own shortest digits": {
			v:    []any{float32(0.1), float32(1e21), float32(1e-7), float32(3), float32(16777216)},
			want: `[0.1,1e+21,1e-7,3.0,16777216.0]`,
		},
		"success: []string":       {v: []string{"a", "b\n"}, want: `["a","b\n"]`},
		"success: an empty []any": {v: []any{}, want: `[]`},
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
			var p Prepared
			if err := b.Finish(&p); err != nil {
				t.Fatalf("Finish: %v", err)
			}
			if want := `{"r":{"type":"future","v":` + tt.want + `}}`; string(p.Questions) != want {
				t.Errorf("Questions = %s, want %s", p.Questions, want)
			}
		})
	}
}

// TestBuilderRawKeyStack checks the key stack Raw sorts map keys on
// (sortedKeys, W5.3): members come out in sorted key order at every level
// when a nested map pushes more keys than the stack holds while an outer
// map is still ranging over its own, the stack is empty after each
// question and after each map written as a value, and a second question of
// the same shape sorts its keys without allocating.
func TestBuilderRawKeyStack(t *testing.T) {
	wide := make(map[string]any, 40)
	for i := range 40 {
		wide["k"+strconv.Itoa(39-i)] = i
	}
	tests := map[string]struct {
		fields map[string]any
	}{
		"success: a wide map nested in the middle of an outer one": {
			fields: map[string]any{"b": map[string]any{"z": 1, "wide": wide, "a": 2}, "a": 0, "c": map[string]string{"y": "2", "x": "1"}},
		},
		"success: maps inside arrays inside maps": {
			fields: map[string]any{"list": []any{map[string]any{"q": 1, "p": map[string]string{"n": "o", "m": "l"}}, wide}, "first": wide},
		},
		"success: nested keys that sort before the outer ones": {
			fields: map[string]any{"z": map[string]any{"a": map[string]any{"b": 1, "a": 2}}, "y": 3},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var b Builder
			b.Grow(2, 1<<14)
			for _, q := range []string{"q1", "q2"} {
				if err := b.Raw(q, "future", tt.fields, nil); err != nil {
					t.Fatalf("Raw(%s): %v", q, err)
				}
				if len(b.keys) != 0 {
					t.Fatalf("after Raw(%s) the key stack holds %q, want it empty", q, b.keys)
				}
			}
			// The same maps as values in an array, below any map's
			// truncation: each map pops its own keys.
			if _, verr := b.appendValue(nil, []any{tt.fields, map[string]string{"b": "1", "a": "2"}}, nil, 0); verr != nil || len(b.keys) != 0 {
				t.Fatalf("after appendValue (error %v) the key stack holds %q, want it empty", verr, b.keys)
			}
			if !raceEnabled() {
				b.Grow(101, 1<<16)
				if n := testing.AllocsPerRun(100, func() { _ = b.Raw("q", "future", tt.fields, nil) }); n != 0 {
					t.Errorf("Raw of a shape the stack has held allocates %v times, want 0", n)
				}
			}
			var p Prepared
			b2 := Builder{}
			if err := b2.Raw("q1", "future", tt.fields, nil); err != nil {
				t.Fatal(err)
			}
			if err := b2.Finish(&p); err != nil {
				t.Fatal(err)
			}
			want := `{"q1":{"type":"future"` + refMembers(tt.fields) + `}}`
			if string(p.Questions) != want {
				t.Errorf("Questions =\n%s\nwant\n%s", p.Questions, want)
			}
		})
	}
}

// refMembers writes m's members as TestBuilderRawKeyStack expects them,
// each preceded by a comma, in sorted key order with its own sort, for maps,
// []any, map[string]string, strings without escapes and ints.
func refMembers(m map[string]any) string {
	var sb strings.Builder
	for _, k := range slices.Sorted(maps.Keys(m)) {
		sb.WriteString(`,"` + k + `":` + refValue(m[k]))
	}
	return sb.String()
}

func refValue(v any) string {
	switch v := v.(type) {
	case int:
		return strconv.Itoa(v)
	case string:
		return `"` + v + `"`
	case []any:
		parts := make([]string, len(v))
		for i, e := range v {
			parts[i] = refValue(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]string:
		var sb strings.Builder
		for i, k := range slices.Sorted(maps.Keys(v)) {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(`"` + k + `":"` + v[k] + `"`)
		}
		return "{" + sb.String() + "}"
	case map[string]any:
		return "{" + strings.TrimPrefix(refMembers(v), ",") + "}"
	}
	panic(fmt.Sprintf("refValue: %T", v))
}

// FuzzAppendJSON checks the invariants AppendJSON keeps on any input (ruling
// R44). The differential check against encoding/json belongs to
// internal/codec's tests, since this package's tests import no JSON library.
//
//   - It never panics.
//   - A failure is a *SyntaxError whose offset lies within the input, and
//     the returned slice is dst unchanged.
//   - A success scans again to itself: removing whitespace is a fixed point,
//     and the output is never longer than the input.
//   - A success allocates nothing while at most 32 containers are open at
//     once, the scanner's stack array; deeper input grows the stack on the
//     heap. Not checked under -race, like the other allocation tests.
func FuzzAppendJSON(f *testing.F) {
	for _, seed := range []string{
		``, ` `, `null`, `true`, `false`, `0`, `-0`, `1.5e+3`, `-12.25E-3`, `01`, `1.`, `1e`, `-`, `+1`, `.5`,
		`""`, `"a\"b\\c\/d\b\f\n\r\t\u00e9\uD83D\uDE00"`, `"\ud800"`, `"\q"`, `"\u12g4"`, "\"\x1f\"", "\"\x7f\"",
		"\"a\xffb\"", "\"\xed\xa0\x80\"", `"abc`, `"abc\`,
		`[]`, `{}`, `[ ]`, `{ }`, `[1,2,[3,{"a":[]}]]`, `{"a":1,"b":[true,null],"c":{"d":"e"}}`,
		"{\n  \"text\" : \"Classify\",\r\n\t\"extra\" : null\n}\n", "[1\f]", "[1\v]", "[1\xc2\xa0]", "\xef\xbb\xbf{}",
		`[1,]`, `{"a":1,}`, `[1}`, `{1:2}`, `{"a" 1}`, `{"a":1} x`, `{}{}`, `[tru]`, `[nul]`,
		strings.Repeat("[", 32) + strings.Repeat("]", 32), strings.Repeat("[", 40) + strings.Repeat("]", 40),
		strings.Repeat(`{"a":`, 20) + `1` + strings.Repeat("}", 20),
	} {
		f.Add([]byte(seed))
	}
	checkAllocs := !raceEnabled()
	f.Fuzz(func(t *testing.T, raw []byte) {
		prefix := []byte("prefix:")
		dst := append(make([]byte, 0, len(prefix)+len(raw)), prefix...)
		out, err := AppendJSON(dst, raw)
		if err != nil {
			se, ok := errors.AsType[*SyntaxError](err)
			if !ok {
				t.Fatalf("AppendJSON(%q) error = %T %v, want a *SyntaxError", raw, err, err)
			}
			if se.Offset < 0 || se.Offset > len(raw) {
				t.Fatalf("AppendJSON(%q) error offset %d outside [0, %d]", raw, se.Offset, len(raw))
			}
			if string(out) != "prefix:" {
				t.Fatalf("AppendJSON(%q) on error = %q, want dst unchanged", raw, out)
			}
			return
		}
		if !bytes.HasPrefix(out, prefix) {
			t.Fatalf("AppendJSON(%q) = %q, lost the prefix", raw, out)
		}
		compact := out[len(prefix):]
		if len(compact) > len(raw) {
			t.Fatalf("AppendJSON(%q) = %q, longer than the input", raw, compact)
		}
		again, err := AppendJSON(nil, compact)
		if err != nil {
			t.Fatalf("AppendJSON(%q) = %q, which does not scan again: %v", raw, compact, err)
		}
		if !bytes.Equal(again, compact) {
			t.Fatalf("AppendJSON(%q) = %q, but scanning that gives %q", raw, compact, again)
		}
		if checkAllocs && maxOpen(compact) <= 32 {
			if n := testing.AllocsPerRun(1, func() { _, _ = AppendJSON(dst[:len(prefix)], raw) }); n != 0 {
				t.Fatalf("AppendJSON(%q) allocated %.0f times, want 0", raw, n)
			}
		}
	})
}

// maxOpen returns the largest number of containers open at once in the
// compact JSON b that the scanner pushes on its stack: an empty container,
// closed at once, is never pushed.
func maxOpen(b []byte) int {
	depth, most := 0, 0
	inString := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case inString && c == '\\':
			i++
		case c == '"':
			inString = !inString
		case inString:
		case c == '{' || c == '[':
			if i+1 < len(b) && (b[i+1] == '}' || b[i+1] == ']') {
				i++
				continue
			}
			depth++
			most = max(most, depth)
		case c == '}' || c == ']':
			depth--
		}
	}
	return most
}

// raceEnabled reports whether the test binary was built with -race, under
// which allocation counts are not asserted (internal/codec's tests do the
// same).
func raceEnabled() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, s := range info.Settings {
		if s.Key == "-race" {
			return s.Value == "true"
		}
	}
	return false
}
