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

// ErrDuplicateQuestion is returned by [NewPrepared] for a question set that
// names a question twice.
var ErrDuplicateQuestion = errors.New("wire: question name repeated in a prepared set")

// MaxLevelHint is the largest value [Prepared.LevelHint] takes.
//
// The hint sizes the lists a score answer's decoder starts with. The server
// decides how many levels an answer carries, so an unbounded hint would let a
// request with a long scale make every response reserve that much memory up
// front; 8 saves the one growth a list of 5 to 8 levels pays after starting at
// 4, and a longer list grows from 8 as it would without a hint.
const MaxLevelHint = 8

// PreparedQuestion is one question of a prepared set: its name and kind, and
// the tables the decoder interns response strings against.
type PreparedQuestion struct {
	// Name is the question name.
	Name string
	// Kind is the question's kind.
	Kind Kind
	// Options lists a choice question's option labels in the order they were
	// added. It is nil for the other kinds.
	Options []string
	// Levels lists a score question's level descriptions; a description's
	// index is its level. It is nil for the other kinds.
	Levels []Content
}

// Prepared is a question set serialised once and reused by every call that
// asks it.
//
// It is read-only after NewPrepared returns and safe for concurrent use. The
// entries sit behind [Prepared.Entries] so that the lookup index built over
// them cannot go stale.
type Prepared struct {
	// Questions is the compact JSON object that is the value of a request's
	// "questions" member, spliced into every request body as is.
	Questions []byte
	// LevelHint is the largest number of levels of any score question in the
	// set, capped at [MaxLevelHint], or 0 when the set has no score question
	// with a levels table. It is a sizing hint for the decoder, never sent.
	LevelHint int

	entries []PreparedQuestion
	index   map[string]int // nil when entries has at most linearLimit questions
}

// NewPrepared returns the prepared set of the questions whose serialised form
// is questions and whose tables are entries. It fails with
// [ErrDuplicateQuestion] when two entries share a name, whatever the size of
// the set (the root package rejects a repeated name before it gets here). The
// slices are kept, not copied: the caller must not modify them afterwards.
func NewPrepared(questions []byte, entries []PreparedQuestion) (*Prepared, error) {
	p := &Prepared{Questions: questions, entries: entries}
	for i := range entries {
		p.LevelHint = max(p.LevelHint, min(len(entries[i].Levels), MaxLevelHint))
	}
	if len(entries) <= linearLimit {
		for i := range entries {
			for j := range i {
				if entries[j].Name == entries[i].Name {
					return nil, fmt.Errorf("%w: %q", ErrDuplicateQuestion, entries[i].Name)
				}
			}
		}
		return p, nil
	}
	p.index = make(map[string]int, len(entries))
	for i := range entries {
		if _, ok := p.index[entries[i].Name]; ok {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateQuestion, entries[i].Name)
		}
		p.index[entries[i].Name] = i
	}
	return p, nil
}

// Entries returns the questions in the order they were added. The slice is
// shared with p: callers must not modify it.
func (p *Prepared) Entries() []PreparedQuestion { return p.entries }

// Lookup returns the question called name, and whether the set has one. The
// returned pointer refers into the set's own entries: it must not be used to
// modify them.
func (p *Prepared) Lookup(name string) (*PreparedQuestion, bool) {
	if p.index != nil {
		i, ok := p.index[name]
		if !ok {
			return nil, false
		}
		return &p.entries[i], true
	}
	for i := range p.entries {
		if p.entries[i].Name == name {
			return &p.entries[i], true
		}
	}
	return nil, false
}

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
	// errTypeField reports a raw question whose fields name "type", which the
	// serialiser writes from the question's type.
	errTypeField = errors.New(`"type" is written from the question's type, not from its fields`)
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

// MemberError reports a question member that could not be written.
type MemberError struct {
	// Member is the member's path inside the question object, as the API
	// names it: "instructions", "criteria.true", "criteria.<label>",
	// "criteria[<level>]", or a raw field's name followed by the path of the
	// offending value inside it (".key" and "[index]" segments).
	Member string
	// Err is the reason.
	Err error
}

// Error returns the member path and the reason.
func (e *MemberError) Error() string { return e.Member + ": " + e.Err.Error() }

// Unwrap returns the reason.
func (e *MemberError) Unwrap() error { return e.Err }

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

// levelSpan locates the compact JSON of one structured score level in the
// builder's buffer.
type levelSpan struct {
	entry, level int
	start, end   int
}

// Builder serialises a question set, one question at a time, into the
// compact JSON object a request sends as "questions", and records the tables
// of a [Prepared] set on the way.
//
// Members are written in the order the API's schema and the Python SDK use:
// "type", then "instructions", then "criteria". The zero Builder is ready to
// use. After a method returns an error the Builder must not be used again.
type Builder struct {
	buf     []byte
	entries []PreparedQuestion
	spans   []levelSpan
}

// Grow reserves room for questions more questions and size more bytes of
// serialised form, so that a Builder given a good estimate never regrows.
func (b *Builder) Grow(questions, size int) {
	b.buf = slices.Grow(b.buf, size+2)
	b.entries = slices.Grow(b.entries, questions)
}

// begin writes the separator, the name and the opening of a question object
// whose type is typ, and records its entry.
func (b *Builder) begin(name, typ string, kind Kind) error {
	if len(b.buf) == 0 {
		b.buf = append(b.buf, '{')
	} else {
		b.buf = append(b.buf, ',')
	}
	var err error
	if b.buf, err = AppendString(b.buf, name); err != nil {
		return fmt.Errorf("question name: %w", err)
	}
	b.buf = append(b.buf, `:{"type":`...)
	if b.buf, err = AppendString(b.buf, typ); err != nil {
		return &MemberError{Member: "type", Err: err}
	}
	b.entries = append(b.entries, PreparedQuestion{Name: name, Kind: kind})
	return nil
}

// member writes the member key and content c, when c is not nil.
func (b *Builder) member(key string, c *Content) error {
	if c == nil {
		return nil
	}
	b.buf = append(b.buf, ',', '"')
	b.buf = append(b.buf, key...)
	b.buf = append(b.buf, '"', ':')
	return b.content(key, c)
}

// content writes c, reporting a failure as a [*MemberError] for path.
func (b *Builder) content(path string, c *Content) error {
	var err error
	if b.buf, err = AppendContent(b.buf, *c); err != nil {
		return &MemberError{Member: path, Err: err}
	}
	return nil
}

// Noul writes a yes/no question. A nil instructions leaves the member out; a
// nil yes or no leaves that outcome out of "criteria", and "criteria" is left
// out when both are nil.
func (b *Builder) Noul(name string, instructions, yes, no *Content) error {
	if err := b.begin(name, "noul", KindNoul); err != nil {
		return err
	}
	if err := b.member("instructions", instructions); err != nil {
		return err
	}
	if yes != nil || no != nil {
		b.buf = append(b.buf, `,"criteria":{`...)
		if yes != nil {
			b.buf = append(b.buf, `"true":`...)
			if err := b.content("criteria.true", yes); err != nil {
				return err
			}
		}
		if no != nil {
			if yes != nil {
				b.buf = append(b.buf, ',')
			}
			b.buf = append(b.buf, `"false":`...)
			if err := b.content("criteria.false", no); err != nil {
				return err
			}
		}
		b.buf = append(b.buf, '}')
	}
	b.buf = append(b.buf, '}')
	return nil
}

// Choice writes a choice question whose options are labels, in order; the
// description of option i is description(i), written as null when it is nil.
// The labels become the question's Options table: the caller must not modify
// them afterwards. Labels are not checked for repeats.
func (b *Builder) Choice(name string, instructions *Content, labels []string, description func(i int) *Content) error {
	if err := b.begin(name, "choice", KindChoice); err != nil {
		return err
	}
	if err := b.member("instructions", instructions); err != nil {
		return err
	}
	b.buf = append(b.buf, `,"criteria":{`...)
	var err error
	for i, label := range labels {
		if i > 0 {
			b.buf = append(b.buf, ',')
		}
		if b.buf, err = AppendString(b.buf, label); err != nil {
			return &MemberError{Member: "criteria." + label, Err: err}
		}
		b.buf = append(b.buf, ':')
		d := description(i)
		if d == nil {
			b.buf = append(b.buf, "null"...)
			continue
		}
		// The member path is built only on failure: a concatenation per
		// option would allocate on every Prepare.
		if b.buf, err = AppendContent(b.buf, *d); err != nil {
			return &MemberError{Member: "criteria." + label, Err: err}
		}
	}
	b.buf = append(b.buf, '}', '}')
	b.entries[len(b.entries)-1].Options = labels
	return nil
}

// Score writes a score question whose levels are levels, lowest first. The
// levels become the question's Levels table: the caller must not modify them
// afterwards, and [Builder.Prepared] points every JSON level at its compact
// bytes inside the finished set.
func (b *Builder) Score(name string, instructions *Content, levels []Content) error {
	if err := b.begin(name, "score", KindScore); err != nil {
		return err
	}
	if err := b.member("instructions", instructions); err != nil {
		return err
	}
	b.buf = append(b.buf, `,"criteria":[`...)
	entry := len(b.entries) - 1
	for i := range levels {
		if i > 0 {
			b.buf = append(b.buf, ',')
		}
		start := len(b.buf)
		var err error
		if b.buf, err = AppendContent(b.buf, levels[i]); err != nil {
			return &MemberError{Member: "criteria[" + strconv.Itoa(i) + "]", Err: err}
		}
		if levels[i].IsJSON() {
			b.spans = append(b.spans, levelSpan{entry: entry, level: i, start: start, end: len(b.buf)})
		}
	}
	b.buf = append(b.buf, ']', '}')
	b.entries[entry].Levels = levels
	return nil
}

// Raw writes a question of type typ, a type the SDK may not model, whose
// other members are fields, in sorted key order after "type". A field value
// is nil, a bool, a string, an integer or float kind, []any, []string,
// map[string]any or map[string]string (maps written in sorted key order,
// nested to any depth up to 1000 levels), or a value leaf knows. fields must
// not name "type". The question's Kind is [ParseKind] of typ; a raw question
// has no Options or Levels table.
func (b *Builder) Raw(name, typ string, fields map[string]any, leaf Leaf) error {
	if err := b.begin(name, typ, ParseKind(typ)); err != nil {
		return err
	}
	var err error
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		if key == "type" {
			return &MemberError{Member: key, Err: errTypeField}
		}
		b.buf = append(b.buf, ',')
		if b.buf, err = AppendString(b.buf, key); err != nil {
			return &MemberError{Member: key, Err: err}
		}
		b.buf = append(b.buf, ':')
		var verr *valueError
		if b.buf, verr = appendValue(b.buf, fields[key], leaf, 0); verr != nil {
			return &MemberError{Member: key + verr.path, Err: verr.err}
		}
	}
	b.buf = append(b.buf, '}')
	return nil
}

// Prepared closes the JSON object and returns the prepared set, see
// [NewPrepared]. The Builder must not be used afterwards.
func (b *Builder) Prepared() (*Prepared, error) {
	if len(b.buf) == 0 {
		b.buf = append(b.buf, '{')
	}
	b.buf = append(b.buf, '}')
	for _, s := range b.spans {
		b.entries[s.entry].Levels[s.level].JSON = b.buf[s.start:s.end:s.end]
	}
	return NewPrepared(b.buf, b.entries)
}
