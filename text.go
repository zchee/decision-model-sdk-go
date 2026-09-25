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

import (
	"strconv"
	"unicode/utf8"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
)

// This file renders text the SDK did not write: a server's message, a name
// the server chose, a request id, a field path, another layer's error. Such
// text keeps its printable characters, non-ASCII included, but a control
// character, a byte that is not UTF-8 or a format character that reorders or
// hides the text around it is written as a Go escape, so that it cannot break
// a log line, recolour a terminal or disguise itself; and it is cut at a
// number of characters counted after escaping (NF7, rulings R58 and R58b).
// The rules are the Rust port's src/text.rs in Go escapes.

// The caps on text the SDK did not write, in characters counted after
// escaping (NF7): a name, such as an extra body member's, and a sentence
// another layer wrote, such as the encoder's.
const (
	maxNameChars    = 128
	maxMessageChars = 200
)

// quotedName returns name between double quotes, escaped with its
// backslashes doubled, and cut at [maxNameChars].
func quotedName(name string) string {
	b := make([]byte, 0, min(len(name), maxNameChars)+8)
	b = append(b, '"')
	b = appendSafeText(b, name, maxNameChars, true)
	return string(append(b, '"'))
}

// appendSafeText appends s, text the SDK did not write, to dst escaped and
// cut at limit characters, as the Rust port's src/text.rs renders such text
// (NF7). Printable characters, non-ASCII and U+FFFD included, are written as
// they are; a control character (C0, DEL, C1), a byte that is not UTF-8 and a
// format character that reorders or hides the text around it (hidesText)
// are written as Go escapes: \n, \r, \t, \x1b, \xff, \u2028. A backslash is
// doubled when double is set, for a name, so that a name spelling \x1b cannot
// read like one holding the byte; a sentence keeps its backslashes, which
// its own layer wrote. A cut never splits a character or an escape and is
// marked with U+2026.
func appendSafeText(dst []byte, s string, limit int, double bool) []byte {
	const hex = "0123456789abcdef"
	var buf [16]byte
	n := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		var esc []byte // the escape written for this character, if any
		switch {
		case r == utf8.RuneError && size == 1:
			esc = append(buf[:0], '\\', 'x', hex[s[i]>>4], hex[s[i]&0xf])
		case r == '\\':
			if double {
				esc = append(buf[:0], '\\', '\\')
			}
		case r < 0x20 || 0x7f <= r && r < 0xa0 || hidesText(r):
			q := strconv.AppendQuoteRune(buf[:0], r) // '\n', '\x1b', '\u2028'
			esc = q[1 : len(q)-1]
		}
		w := 1
		if esc != nil {
			w = len(esc)
		}
		if n+w > limit {
			return append(dst, "\u2026"...)
		}
		if esc != nil {
			dst = append(dst, esc...)
		} else {
			dst = append(dst, s[i:i+size]...)
		}
		n += w
		i += size
	}
	return dst
}

// hidesText reports whether r is a Unicode format character that reorders,
// joins or hides the text around it: the soft hyphen, the Arabic letter
// mark, the Mongolian vowel separator, the zero-width characters, the line
// and paragraph separators, the bidirectional embeddings, overrides and
// isolates, the byte-order mark, the interlinear annotation marks and the
// invisible tag characters (the Rust port's hides_text).
func hidesText(r rune) bool {
	switch {
	case r == 0x00ad, r == 0x061c, r == 0x180e, r == 0xfeff:
		return true
	case 0x200b <= r && r <= 0x200f, 0x2028 <= r && r <= 0x202e, 0x2060 <= r && r <= 0x206f:
		return true
	case 0xfff9 <= r && r <= 0xfffb, 0xe0000 <= r && r <= 0xe007f:
		return true
	}
	return false
}

// maxPathChars caps a whole field path in characters, counted after
// escaping: it holds the deepest path of the response schema,
// answers.<name>.probabilities.<key>, with both server-chosen names at
// [maxNameChars] (the Rust port's MAX_PATH_CHARS).
const maxPathChars = 320

// safeName returns name, which the server chose, escaped with its
// backslashes doubled and cut at [maxNameChars], without quotes.
func safeName(name string) string {
	return string(appendSafeText(make([]byte, 0, min(len(name), maxNameChars)+4), name, maxNameChars, true))
}

// safeMessage returns s, a sentence the SDK did not write, escaped with its
// backslashes kept and cut at [maxMessageChars].
func safeMessage(s string) string {
	return string(appendSafeText(make([]byte, 0, min(len(s), maxMessageChars)+4), s, maxMessageChars, false))
}

// renderFieldPath returns p as a *ResponseValidationError prints it: the
// Python SDK's dotted field_path, "." for the root, with each name the server
// chose (an answer name, a probability or legend key) escaped with its
// backslashes doubled and cut at [maxNameChars], and the whole cut at
// [maxPathChars].
func renderFieldPath(p codec.FieldPath) string {
	if p.Top == "" {
		return "."
	}
	t := pathText{limit: maxPathChars}
	t.fixed(p.Top)
	if p.HasIndex {
		t.fixed("[" + strconv.Itoa(p.Index) + "]")
	}
	if p.HasName {
		t.fixed(".")
		t.name(p.Name)
	}
	if p.Member != "" {
		t.fixed(".")
		t.fixed(p.Member)
	}
	if p.HasKey {
		t.fixed(".")
		t.name(p.Key)
	}
	return string(t.b)
}

// pathText builds a rendering capped at limit characters from the SDK's own
// text and escaped names. Once a piece would cross the limit, U+2026 is
// written in its place and nothing after it.
type pathText struct {
	b     []byte
	n     int // characters written
	limit int
	full  bool
}

// fixed appends s, ASCII text of the SDK's own, as it is.
func (t *pathText) fixed(s string) {
	if t.full {
		return
	}
	if t.n+len(s) > t.limit {
		t.b = append(t.b, "\u2026"...)
		t.full = true
		return
	}
	t.b = append(t.b, s...)
	t.n += len(s)
}

// name appends s, a name the server chose, escaped and cut at
// [maxNameChars] and at what remains of the limit.
func (t *pathText) name(s string) {
	if t.full {
		return
	}
	room := min(maxNameChars, t.limit-t.n)
	start := len(t.b)
	t.b = appendSafeText(t.b, s, room, true)
	written := utf8.RuneCount(t.b[start:])
	if room < maxNameChars && written > room {
		// The name was cut by the whole path's limit, not its own.
		t.full = true
	}
	t.n += written
}
