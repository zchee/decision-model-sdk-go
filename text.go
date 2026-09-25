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
	"cmp"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
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

// credentials are the values a transport error's text must not show: the
// credentials of the request that failed, each in the forms an error text
// may quote it in, longest first. Build them with [requestCredentials].
//
// A transport's error text is written by code the SDK does not control (a
// caller's RoundTripper or dialer, a proxy, net/http), which may repeat a
// header value, and it reaches the SDK error's message, its log record and
// the chain errors.Unwrap walks (Appendix B: "Transport error text verbatim
// → escaped, cut at 200, credentials ***"; note N-W2.5).
type credentials []string

// requestCredentials returns the credentials of a request with header h, as
// typesafe-sdk-python collects them (py:_core/logging.py:43-51): the value of
// every header whose name marks a credential ([isSecretHeader]), and the
// credential after the scheme of an Authorization or Proxy-Authorization
// value, which for the SDK's own Authorization is the API key. Each is
// looked for as it is, as Go's %q and %+q quote it, and as encoding/json
// escapes it: the Go analogues of the Python SDK's raw, repr and json.dumps
// forms, which differ from them above the Basic Multilingual Plane and on
// '<', '>' and '&'. A value shorter than [minKeyNeedleBytes] is not looked
// for, as the API key is not (ruling R68): it would match ordinary text; the
// Python SDK looks for every value.
func requestCredentials(h http.Header) credentials {
	var c credentials
	add := func(v string) {
		if !keyNeedle(v) {
			return
		}
		for _, form := range [...]string{v, quotedForm(strconv.Quote(v)), quotedForm(strconv.QuoteToASCII(v)), jsonForm(v)} {
			if !slices.Contains(c, form) {
				c = append(c, form)
			}
		}
	}
	for name, values := range h {
		if !isSecretHeader(name) {
			continue
		}
		scheme := strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Proxy-Authorization")
		for _, v := range values {
			add(v)
			if cred, ok := afterScheme(v); scheme && ok {
				add(cred)
			}
		}
	}
	// A whole value is replaced before a credential inside it; equal lengths
	// in a fixed order, so the result does not depend on map order.
	slices.SortFunc(c, func(a, b string) int { return cmp.Or(cmp.Compare(len(b), len(a)), strings.Compare(a, b)) })
	return c
}

// afterScheme returns what follows the scheme of an authorization value,
// "<scheme> <credential>", as Python's value.split(maxsplit=1) takes it
// (py:_core/logging.py:45-48), and false when there is nothing after it.
func afterScheme(v string) (string, bool) {
	v = strings.TrimLeft(v, " \t")
	i := strings.IndexAny(v, " \t")
	if i < 0 {
		return "", false
	}
	cred := strings.TrimLeft(v[i:], " \t")
	return cred, cred != ""
}

// quotedForm returns q, a Go-quoted string, without its quotes.
func quotedForm(q string) string { return q[1 : len(q)-1] }

// jsonForm returns v as a JSON string holds it, without its quotes, escaped
// as encoding/json escapes it by default: '"' and '\\' with a backslash,
// the controls, '<', '>', '&', U+2028 and U+2029 as \u escapes (\n, \r and
// \t as those), and a byte that is not UTF-8 as �.
func jsonForm(v string) string {
	const hex = "0123456789abcdef"
	var b strings.Builder
	for i := 0; i < len(v); {
		r, size := utf8.DecodeRuneInString(v[i:])
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteByte(byte(r))
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == utf8.RuneError && size == 1:
			b.WriteString(`�`)
		case r < 0x20 || r == '<' || r == '>' || r == '&' || r == ' ' || r == ' ':
			b.WriteString(`\u`)
			for shift := 12; shift >= 0; shift -= 4 {
				b.WriteByte(hex[r>>shift&0xf])
			}
		default:
			b.WriteString(v[i : i+size])
		}
		i += size
	}
	return b.String()
}

// redact returns s with every credential replaced by [redacted], then the
// userinfo of every URL in it ([scrubUserinfo]), which may hold a proxy's
// password, and reports whether it replaced anything.
func (c credentials) redact(s string) (string, bool) {
	found := false
	for _, v := range c {
		if strings.Contains(s, v) {
			s, found = strings.ReplaceAll(s, v, redacted), true
		}
	}
	if u, ok := scrubUserinfo(s); ok {
		s, found = u, true
	}
	return s, found
}

// maxChainErrors bounds the errors [credentials.cause] reads in a chain; a
// longer chain is treated as holding a credential.
const maxChainErrors = 64

// cause returns the error an SDK error made from the transport's error err
// unwraps to: err itself, unless the text of err or of an error its chain
// wraps (errors.Unwrap, both forms) holds a credential, in which case it
// returns a [scrubbedError] standing in for err, so that no printed form of
// the SDK error or of anything it unwraps to shows the credential.
func (c credentials) cause(err error) error {
	if err == nil || !c.inChain(err) {
		return err
	}
	msg, _ := c.redact(err.Error())
	s := &scrubbedError{msg: msg}
	for _, sentinel := range causeSentinels {
		if errors.Is(err, sentinel) {
			s.sentinels = append(s.sentinels, sentinel)
		}
	}
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		s.sentinels = append(s.sentinels, errno)
	}
	return s
}

// inChain reports whether the text of err, or of an error its chain wraps,
// holds a credential; a chain of more than [maxChainErrors] errors counts
// as holding one.
func (c credentials) inChain(err error) bool {
	stack := []error{err}
	for n := 0; len(stack) > 0 && n < maxChainErrors; n++ {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e == nil {
			continue
		}
		if _, found := c.redact(e.Error()); found {
			return true
		}
		switch u := e.(type) { //nolint:errorlint // visits each link of the chain as it is; errors.As would skip links.
		case interface{ Unwrap() error }:
			stack = append(stack, u.Unwrap())
		case interface{ Unwrap() []error }:
			stack = append(stack, u.Unwrap()...)
		}
	}
	return len(stack) > 0
}

// causeSentinels are the errors a [scrubbedError] still matches with
// errors.Is when the transport's error did: the ends of a deadline, a
// cancellation, a connection or a stream a caller may branch on.
var causeSentinels = [...]error{context.DeadlineExceeded, context.Canceled, os.ErrDeadlineExceeded, io.ErrUnexpectedEOF, io.EOF, net.ErrClosed}

// scrubbedError stands in for a transport error whose chain printed a
// credential of the request (Appendix B: "cause via errors.Unwrap unless it
// printed a credential"). Its text is the transport error's with every
// credential replaced by "***"; it unwraps to the [causeSentinels] and the
// [syscall.Errno] the transport error matched, so errors.Is(err,
// context.DeadlineExceeded) or errors.Is(err, syscall.ECONNRESET) still
// answers as it would have, and to nothing else: errors.As cannot reach the
// transport's error or any value inside it.
type scrubbedError struct {
	msg       string
	sentinels []error
}

// Error returns the transport error's text with its credentials replaced.
func (e *scrubbedError) Error() string { return e.msg }

// Unwrap returns the sentinels the transport's error matched.
func (e *scrubbedError) Unwrap() []error { return e.sentinels }
