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
	"math"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
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

// text and raw build Content for the builder tests.
func text(s string) *Content { return &Content{Text: s} }

func raw(s string) *Content { return &Content{JSON: []byte(s)} }

func TestBuilder(t *testing.T) {
	type choiceOption struct {
		label       string
		description *Content
	}
	tests := map[string]struct {
		build       func(b *Builder) error
		want        string
		wantEntries []PreparedQuestion
		wantHint    int
	}{
		"success: nothing written is an empty object": {
			build:       func(*Builder) error { return nil },
			want:        `{}`,
			wantEntries: []PreparedQuestion{}, // Grow allocated them
		},
		"success: a noul without members": {
			build:       func(b *Builder) error { return b.Noul("q", nil, nil, nil) },
			want:        `{"q":{"type":"noul"}}`,
			wantEntries: []PreparedQuestion{{Name: "q", Kind: KindNoul}},
		},
		"success: a noul with every member": {
			build:       func(b *Builder) error { return b.Noul("q", text("Spam?"), text("Yes"), raw(`{"why": "no"}`)) },
			want:        `{"q":{"type":"noul","instructions":"Spam?","criteria":{"true":"Yes","false":{"why":"no"}}}}`,
			wantEntries: []PreparedQuestion{{Name: "q", Kind: KindNoul}},
		},
		"success: a noul with only the no outcome": {
			build:       func(b *Builder) error { return b.Noul("q", nil, nil, text("No")) },
			want:        `{"q":{"type":"noul","criteria":{"false":"No"}}}`,
			wantEntries: []PreparedQuestion{{Name: "q", Kind: KindNoul}},
		},
		"success: a choice with described and undescribed options": {
			build: func(b *Builder) error {
				opts := []choiceOption{{"calm", text("neutral")}, {"angry", nil}, {"mixed", raw(`["a", "b"]`)}}
				labels := []string{"calm", "angry", "mixed"}
				return b.Choice("tone", text("Tone?"), labels, func(i int) *Content { return opts[i].description })
			},
			want:        `{"tone":{"type":"choice","instructions":"Tone?","criteria":{"calm":"neutral","angry":null,"mixed":["a","b"]}}}`,
			wantEntries: []PreparedQuestion{{Name: "tone", Kind: KindChoice, Options: []string{"calm", "angry", "mixed"}}},
		},
		"success: a choice without options": {
			build: func(b *Builder) error {
				return b.Choice("tone", nil, nil, func(int) *Content { return nil })
			},
			want:        `{"tone":{"type":"choice","criteria":{}}}`,
			wantEntries: []PreparedQuestion{{Name: "tone", Kind: KindChoice}},
		},
		"success: a score with text and JSON levels": {
			build: func(b *Builder) error {
				return b.Score("urgency", raw(`[ ]`), []Content{{Text: "low"}, {JSON: []byte(` { "extra" : null } `)}, {Text: "high"}})
			},
			want: `{"urgency":{"type":"score","instructions":[],"criteria":["low",{"extra":null},"high"]}}`,
			wantEntries: []PreparedQuestion{{Name: "urgency", Kind: KindScore, Levels: []Content{
				{Text: "low"}, {JSON: []byte(`{"extra":null}`)}, {Text: "high"},
			}}},
			wantHint: 3,
		},
		"success: raw fields in sorted order after type": {
			build: func(b *Builder) error {
				return b.Raw("r", "future", map[string]any{"weight": 3, "criteria": map[string]any{"b": nil, "a": "x"}, "instructions": "Spam?"}, nil)
			},
			want:        `{"r":{"type":"future","criteria":{"a":"x","b":null},"instructions":"Spam?","weight":3}}`,
			wantEntries: []PreparedQuestion{{Name: "r", Kind: KindUnknown}},
		},
		"success: a raw question of a known type takes its kind": {
			build:       func(b *Builder) error { return b.Raw("r", "score", map[string]any{"criteria": []string{"good"}}, nil) },
			want:        `{"r":{"type":"score","criteria":["good"]}}`,
			wantEntries: []PreparedQuestion{{Name: "r", Kind: KindScore}},
		},
		"success: several questions and a capped level hint": {
			build: func(b *Builder) error {
				levels := make([]Content, 12)
				for i := range levels {
					levels[i] = Content{Text: strconv.Itoa(i)}
				}
				if err := b.Noul("a", nil, nil, nil); err != nil {
					return err
				}
				if err := b.Score("b", nil, levels[:3]); err != nil {
					return err
				}
				return b.Score("c", nil, levels)
			},
			want: `{"a":{"type":"noul"},"b":{"type":"score","criteria":["0","1","2"]},"c":{"type":"score","criteria":["0","1","2","3","4","5","6","7","8","9","10","11"]}}`,
			wantEntries: func() []PreparedQuestion {
				levels := make([]Content, 12)
				for i := range levels {
					levels[i] = Content{Text: strconv.Itoa(i)}
				}
				return []PreparedQuestion{{Name: "a", Kind: KindNoul}, {Name: "b", Kind: KindScore, Levels: levels[:3]}, {Name: "c", Kind: KindScore, Levels: levels}}
			}(),
			wantHint: MaxLevelHint,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var b Builder
			b.Grow(4, 64)
			if err := tt.build(&b); err != nil {
				t.Fatalf("build: %v", err)
			}
			p, err := b.Prepared()
			if err != nil {
				t.Fatalf("Prepared: %v", err)
			}
			if string(p.Questions) != tt.want {
				t.Errorf("Questions =\n%s\nwant\n%s", p.Questions, tt.want)
			}
			if diff := gocmp.Diff(tt.wantEntries, p.Entries()); diff != "" {
				t.Errorf("Entries() mismatch (-want +got):\n%s", diff)
			}
			if p.LevelHint != tt.wantHint {
				t.Errorf("LevelHint = %d, want %d", p.LevelHint, tt.wantHint)
			}
			// Every JSON level is a view of the finished object, capped so
			// that nothing can append into it.
			for _, e := range p.Entries() {
				for i, lv := range e.Levels {
					if !lv.IsJSON() {
						continue
					}
					at := bytes.Index(p.Questions, lv.JSON)
					if at < 0 || &p.Questions[at] != &lv.JSON[0] {
						t.Errorf("%s level %d JSON %q does not alias Questions", e.Name, i, lv.JSON)
					}
					if cap(lv.JSON) != len(lv.JSON) {
						t.Errorf("%s level %d JSON cap = %d, want %d", e.Name, i, cap(lv.JSON), len(lv.JSON))
					}
				}
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

func TestBuilderErrors(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	never := func(int) *Content { return nil }
	tests := map[string]struct {
		build      func(b *Builder) error
		wantMember string // "" for an error that is not a *MemberError
		wantErr    error  // matched with errors.Is, when set
		wantMsg    string // the whole Error() text
	}{
		"error: a question name that is not UTF-8": {
			build:   func(b *Builder) error { return b.Noul("\xff", nil, nil, nil) },
			wantErr: ErrInvalidUTF8,
			wantMsg: "question name: string is not valid UTF-8",
		},
		"error: a raw type that is not UTF-8": {
			build:      func(b *Builder) error { return b.Raw("r", "\xff", nil, nil) },
			wantMember: "type", wantErr: ErrInvalidUTF8,
			wantMsg: "type: string is not valid UTF-8",
		},
		"error: noul instructions": {
			build:      func(b *Builder) error { return b.Noul("q", raw(`"x"`), nil, nil) },
			wantMember: "instructions", wantErr: ErrContentShape,
			wantMsg: "instructions: JSON content must be an object or an array",
		},
		"error: the yes outcome": {
			build:      func(b *Builder) error { return b.Noul("q", nil, text("\xff"), nil) },
			wantMember: "criteria.true", wantErr: ErrInvalidUTF8,
			wantMsg: "criteria.true: string is not valid UTF-8",
		},
		"error: the no outcome after the yes outcome": {
			build:      func(b *Builder) error { return b.Noul("q", nil, text("y"), raw(`[`)) },
			wantMember: "criteria.false",
			wantMsg:    "criteria.false: invalid JSON at byte 1: unexpected end of input, want a value",
		},
		"error: choice instructions": {
			build:      func(b *Builder) error { return b.Choice("q", raw(`1`), nil, never) },
			wantMember: "instructions", wantErr: ErrContentShape,
			wantMsg: "instructions: JSON content must be an object or an array",
		},
		"error: an option label that is not UTF-8": {
			build:      func(b *Builder) error { return b.Choice("q", nil, []string{"ok", "\xff"}, never) },
			wantMember: "criteria.\xff", wantErr: ErrInvalidUTF8,
			wantMsg: "criteria.\xff: string is not valid UTF-8",
		},
		"error: an option description": {
			build: func(b *Builder) error {
				return b.Choice("q", nil, []string{"calm"}, func(int) *Content { return raw(`{"a" 1}`) })
			},
			wantMember: "criteria.calm",
			wantMsg:    "criteria.calm: invalid JSON at byte 5: want ':' after a member name",
		},
		"error: score instructions": {
			build:      func(b *Builder) error { return b.Score("q", text("\xff"), []Content{{Text: "a"}}) },
			wantMember: "instructions", wantErr: ErrInvalidUTF8,
			wantMsg: "instructions: string is not valid UTF-8",
		},
		"error: a score level": {
			build:      func(b *Builder) error { return b.Score("q", nil, []Content{{Text: "a"}, {JSON: []byte(`true`)}}) },
			wantMember: "criteria[1]", wantErr: ErrContentShape,
			wantMsg: "criteria[1]: JSON content must be an object or an array",
		},
		"error: a raw field named type": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"type": "noul"}, nil) },
			wantMember: "type", wantErr: errTypeField,
			wantMsg: `type: "type" is written from the question's type, not from its fields`,
		},
		"error: a raw field name that is not UTF-8": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"\xff": 1}, nil) },
			wantMember: "\xff", wantErr: ErrInvalidUTF8,
			wantMsg: "\xff: string is not valid UTF-8",
		},
		"error: a string value that is not UTF-8": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"s": "\xff"}, nil) },
			wantMember: "s", wantErr: ErrInvalidUTF8,
			wantMsg: "s: string is not valid UTF-8",
		},
		"error: an unsupported type without a leaf": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"w": leafType{}}, nil) },
			wantMember: "w", wantErr: ErrUnsupportedValue,
			wantMsg: "w: unsupported value of type wire.leafType",
		},
		"error: a value the leaf does not know": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"w": make(chan int)}, testLeaf) },
			wantMember: "w", wantErr: ErrUnsupportedValue,
			wantMsg: "w: unsupported value of type chan int",
		},
		"error: the leaf's own failure": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"w": leafType{s: "fail"}}, testLeaf) },
			wantMember: "w",
			wantMsg:    "w: leaf refused",
		},
		"error: NaN": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"w": math.NaN()}, nil) },
			wantMember: "w", wantErr: ErrUnsupportedValue,
			wantMsg: "w: unsupported value: NaN is not a JSON number",
		},
		"error: infinity as float32": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"w": float32(math.Inf(-1))}, nil) },
			wantMember: "w", wantErr: ErrUnsupportedValue,
			wantMsg: "w: unsupported value: -Inf is not a JSON number",
		},
		"error: the path of a nested value": {
			build: func(b *Builder) error {
				return b.Raw("r", "noul", map[string]any{"criteria": map[string]any{"a": []any{1, struct{}{}}}}, nil)
			},
			wantMember: "criteria.a[1]", wantErr: ErrUnsupportedValue,
			wantMsg: "criteria.a[1]: unsupported value of type struct {}",
		},
		"error: a nested map key that is not UTF-8": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"m": map[string]any{"\xff": 1}}, nil) },
			wantMember: "m.\xff", wantErr: ErrInvalidUTF8,
			wantMsg: "m.\xff: string is not valid UTF-8",
		},
		"error: a []string element": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"l": []string{"a", "\xff"}}, nil) },
			wantMember: "l[1]", wantErr: ErrInvalidUTF8,
			wantMsg: "l[1]: string is not valid UTF-8",
		},
		"error: a map[string]string key": {
			build: func(b *Builder) error {
				return b.Raw("r", "noul", map[string]any{"m": map[string]string{"\xff": "v"}}, nil)
			},
			wantMember: "m.\xff", wantErr: ErrInvalidUTF8,
			wantMsg: "m.\xff: string is not valid UTF-8",
		},
		"error: a map[string]string value": {
			build: func(b *Builder) error {
				return b.Raw("r", "noul", map[string]any{"m": map[string]string{"k": "\xff"}}, nil)
			},
			wantMember: "m.k", wantErr: ErrInvalidUTF8,
			wantMsg: "m.k: string is not valid UTF-8",
		},
		"error: a cycle": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"c": cycle}, nil) },
			wantMember: "c" + strings.Repeat(".self", maxValueDepth+1), wantErr: ErrUnsupportedValue,
			wantMsg: "c" + strings.Repeat(".self", maxValueDepth+1) + ": unsupported value: nested more than 1000 levels deep (a cycle?)",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var b Builder
			err := tt.build(&b)
			if err == nil {
				t.Fatal("build succeeded, want an error")
			}
			if err.Error() != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", err, tt.wantMsg)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("errors.Is(%v, %v) = false", err, tt.wantErr)
			}
			var me *MemberError
			if isMember := errors.As(err, &me); isMember != (tt.wantMember != "") {
				t.Fatalf("errors.As(*MemberError) = %t, want %t", isMember, tt.wantMember != "")
			}
			if me != nil && me.Member != tt.wantMember {
				t.Errorf("Member = %q, want %q", me.Member, tt.wantMember)
			}
		})
	}
}

func TestNewPreparedLevelHint(t *testing.T) {
	levels := func(n int) []Content { return make([]Content, n) }
	tests := map[string]struct {
		entries []PreparedQuestion
		want    int
	}{
		"success: no score":              {entries: []PreparedQuestion{{Name: "a", Kind: KindNoul}}, want: 0},
		"success: a score with 3 levels": {entries: []PreparedQuestion{{Name: "a", Kind: KindScore, Levels: levels(3)}}, want: 3},
		"success: the largest score wins": {
			entries: []PreparedQuestion{{Name: "a", Levels: levels(5)}, {Name: "b", Levels: levels(2)}},
			want:    5,
		},
		"success: capped at MaxLevelHint": {entries: []PreparedQuestion{{Name: "a", Levels: levels(MaxLevelHint + 1)}}, want: MaxLevelHint},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p, err := NewPrepared([]byte(`{}`), tt.entries)
			if err != nil {
				t.Fatalf("NewPrepared: %v", err)
			}
			if p.LevelHint != tt.want {
				t.Errorf("LevelHint = %d, want %d", p.LevelHint, tt.want)
			}
		})
	}
}
