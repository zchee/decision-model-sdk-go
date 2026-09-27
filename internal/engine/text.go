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

package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
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
	MaxNameChars    = 128
	MaxMessageChars = 200
)

// QuotedName returns name between double quotes, escaped with its
// backslashes doubled, and cut at [MaxNameChars].
func QuotedName(name string) string {
	b := make([]byte, 0, min(len(name), MaxNameChars)+8)
	b = append(b, '"')
	b = AppendSafeText(b, name, MaxNameChars, true)
	return string(append(b, '"'))
}

// AppendSafeText appends s, text the SDK did not write, to dst escaped and
// cut at limit characters, as the Rust port's src/text.rs renders such text
// (NF7). Printable characters, non-ASCII and U+FFFD included, are written as
// they are; a control character (C0, DEL, C1), a byte that is not UTF-8 and a
// format character that reorders or hides the text around it (hidesText)
// are written as Go escapes: \n, \r, \t, \x1b, \xff, \u2028. A backslash is
// doubled when double is set, for a name, so that a name spelling \x1b cannot
// read like one holding the byte; a sentence keeps its backslashes, which
// its own layer wrote. A cut never splits a character or an escape and is
// marked with U+2026.
func AppendSafeText(dst []byte, s string, limit int, double bool) []byte {
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

// MaxPathChars caps a whole field path in characters, counted after
// escaping: it holds the deepest path of the response schema,
// answers.<name>.probabilities.<key>, with both server-chosen names at
// [MaxNameChars] (the Rust port's MAX_PATH_CHARS).
const MaxPathChars = 320

// SafeName returns name, which the server chose, escaped with its
// backslashes doubled and cut at [MaxNameChars], without quotes.
func SafeName(name string) string {
	return string(AppendSafeText(make([]byte, 0, min(len(name), MaxNameChars)+4), name, MaxNameChars, true))
}

// SafeMessage returns s, a sentence the SDK did not write, escaped with its
// backslashes kept and cut at [MaxMessageChars].
func SafeMessage(s string) string {
	return string(AppendSafeText(make([]byte, 0, min(len(s), MaxMessageChars)+4), s, MaxMessageChars, false))
}

// Credentials are the values a transport error's text must not show: the
// credentials of the request that failed, each in the forms an error text
// may quote it in, longest first. Build them with [RequestCredentials].
//
// A transport's error text is written by code the SDK does not control (a
// caller's RoundTripper or dialer, a proxy, net/http), which may repeat a
// header value, and it reaches the SDK error's message, its log record and
// the chain errors.Unwrap walks (Appendix B: "Transport error text verbatim
// → escaped, cut at 200, credentials ***"; note N-W2.5).
type Credentials []string

// RequestCredentials returns the credentials of a request with header h, as
// typesafe-sdk-python collects them (py:_core/logging.py:43-51): the value of
// every header whose name marks a credential ([IsSecretHeader]), and the
// credential after the scheme of an Authorization or Proxy-Authorization
// value, which for the SDK's own Authorization is the API key. Each is
// looked for as it is, as Go's %q and %+q quote it, and as encoding/json
// escapes it: the Go analogues of the Python SDK's raw, repr and json.dumps
// forms, which differ from them above the Basic Multilingual Plane and on
// '<', '>' and '&'. A value shorter than [MinKeyNeedleBytes] is not looked
// for, as the API key is not (ruling R68): it would match ordinary text; the
// Python SDK looks for every value.
func RequestCredentials(h http.Header) Credentials {
	return CallCredentials(h, nil)
}

// CallCredentials returns the credentials a transport error of a call may
// repeat: those of the request with header h ([RequestCredentials]) and
// proxies, the credentials of the proxies the SDK's own transport chose
// ([ProxyCreds]), which a proxy's answer may repeat in an error net/http
// builds from it before the SDK's transport sees a response (review W6.2
// MIN-4, its review's MINOR 1, and ruling D-W6-secfix-m2).
func CallCredentials(h http.Header, proxies Credentials) Credentials {
	var c Credentials
	for name, values := range h {
		if !IsSecretHeader(name) {
			continue
		}
		scheme := strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Proxy-Authorization")
		for _, v := range values {
			c.add(v)
			if cred, ok := afterScheme(v); scheme && ok {
				c.add(cred)
			}
		}
	}
	for _, v := range proxies {
		if !slices.Contains(c, v) {
			c = append(c, v)
		}
	}
	slices.SortFunc(c, longestFirst)
	return c
}

// add adds v to c in the forms a text may hold it (see
// [RequestCredentials]), unless it is shorter than [MinKeyNeedleBytes].
func (c *Credentials) add(v string) {
	if KeyNeedle(v) {
		c.addAny(v)
	}
}

// addAny adds v to c in the forms a text may hold it, whatever its length;
// the empty string is not added.
func (c *Credentials) addAny(v string) {
	if v == "" {
		return
	}
	for _, form := range [...]string{v, quotedForm(strconv.Quote(v)), quotedForm(strconv.QuoteToASCII(v)), jsonForm(v)} {
		if !slices.Contains(*c, form) {
			*c = append(*c, form)
		}
	}
}

// addProxy adds to c the credential a request through a proxy whose URL has
// the userinfo user carries, and what of it the proxy's answer may repeat:
// the token of the Basic Proxy-Authorization value net/http sends for user,
// the password as it is and as the URL escapes it, and each word of the
// password between single spaces, since net/http splits a status line there
// and quotes one word of it (a malformed status code or version). Unlike a
// header's credential, each is looked for whatever its length (ruling
// D-W6-secfix-m2): a short one may then match ordinary text, which the error
// shows as "***" and whose chain it stands in for ([Credentials.Cause]).
// With words false only the whole credentials are added, the token and
// the password as it is and as the URL escapes it: a response header's
// needles ([ProxyCreds.inHeader]).
func (c *Credentials) addProxy(user *url.Userinfo, words bool) {
	password, _ := user.Password()
	c.addAny(base64.StdEncoding.EncodeToString([]byte(user.Username() + ":" + password)))
	c.addAny(password)
	c.addAny(strings.TrimPrefix(url.UserPassword("", password).String(), ":"))
	if !words {
		return
	}
	for word := range strings.SplitSeq(password, " ") {
		c.addAny(word)
	}
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
// as encoding/json escapes it by default on Go 1.27: '"' and '\\' with a
// backslash, the controls, '<', '>', '&', U+2028 and U+2029 as \u escapes
// (\n, \r and \t as those), and a byte that is not UTF-8 as U+FFFD itself,
// unescaped. TestJSONFormMatchesEncodingJSON checks it against
// encoding/json, whose spelling of that last case differs between
// encoders.
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
			b.WriteString("\ufffd") // U+FFFD itself, unescaped
		case r < 0x20 || r == '<' || r == '>' || r == '&' || r == '\u2028' || r == '\u2029':
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

// Redact returns s with every credential replaced by [Redacted], then the
// userinfo of every URL in it ([ScrubUserinfo]), which may hold a proxy's
// password, and reports whether it replaced anything.
func (c Credentials) Redact(s string) (string, bool) {
	found := false
	for _, v := range c {
		if strings.Contains(s, v) {
			s, found = strings.ReplaceAll(s, v, Redacted), true
		}
	}
	if u, ok := ScrubUserinfo(s); ok {
		s, found = u, true
	}
	return s, found
}

// LogErrorText renders err, an error of the SDK's transport, for the
// transport's DEBUG records "h2: gate error" and "h2: redial error"
// (h2gate.Config.ErrorText, ruling R84): every credential of creds, the
// call's ([Transport.Credentials]), and every URL userinfo
// replaced by "***" ([Credentials.Redact]), then escaped and cut at 200
// characters ([SafeMessage]), as the text of a *ConnectionError is. The
// transport calls it only for a record the logger keeps.
func LogErrorText(creds Credentials, err error) string {
	text, _ := creds.Redact(err.Error())
	return SafeMessage(text)
}

// maxChainErrors bounds the errors [Credentials.Cause] reads in a chain; a
// longer chain is treated as holding a credential.
const maxChainErrors = 64

// Cause returns the error an SDK error made from the transport's error err
// unwraps to: err itself, unless a printed form of err or of an error its
// chain wraps (errors.Unwrap, both forms) holds a credential
// ([Credentials.printed]), in which case it returns a [scrubbedError]
// standing in for err, so that no printed form of the SDK error or of
// anything it unwraps to shows the credential. The stand-in keeps err's
// text and the rendering of its chain ([Credentials.detail]), each with its
// credentials replaced.
//
// An error whose fields point to the request, such as a caller's error type
// that keeps the *http.Request, is kept: fmt prints a pointer inside a value
// as an address, so no printed form shows the credential, and errors.As
// reaches the error with the request the caller's own RoundTripper, dialer
// or body gave it (review W2.5 MINOR 2, ruling R82).
func (c Credentials) Cause(err error) error {
	if err == nil || !c.inChain(err) {
		return err
	}
	msg, _ := c.Redact(err.Error())
	s := &scrubbedError{msg: msg, detail: c.detail(err)}
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

// maxDetailChars caps a [scrubbedError]'s rendering of the chain it stands
// in for, in characters counted after escaping: one line holding the
// transport error's text and the text of each cause it wraps, which a
// sentence's 200 characters would cut before the first cause.
const maxDetailChars = 1024

// detail returns the rendering of err's chain that a [scrubbedError] prints
// for %+v and %#v (ruling R95): the %+v form of err, then that of each error
// its chain wraps (errors.Unwrap, both forms, depth first, at most
// [maxChainErrors]) whose text the rendering does not already hold, joined
// by ": ", with every credential replaced by "***" ([Credentials.Redact]),
// then escaped, a line break as \n, and cut at [maxDetailChars]; the
// credentials are replaced before the cut. So a cause the transport's text
// does not print, such as one only errors.Unwrap reaches, still shows, as
// the Python SDK's rebuilt chain does, but as text. It runs on the error
// path only.
func (c Credentials) detail(err error) string {
	var b strings.Builder
	stack := []error{err}
	for n := 0; len(stack) > 0 && n < maxChainErrors; n++ {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e == nil {
			continue
		}
		if text := fmt.Sprintf("%+v", e); !strings.Contains(b.String(), text) {
			if b.Len() > 0 {
				b.WriteString(": ")
			}
			b.WriteString(text)
		}
		switch u := e.(type) { //nolint:errorlint // visits each link of the chain as it is; errors.As would skip links.
		case interface{ Unwrap() error }:
			stack = append(stack, u.Unwrap())
		case interface{ Unwrap() []error }:
			for _, w := range slices.Backward(u.Unwrap()) {
				stack = append(stack, w)
			}
		}
	}
	text, _ := c.Redact(b.String())
	return string(AppendSafeText(make([]byte, 0, min(len(text), maxDetailChars)+4), text, maxDetailChars, false))
}

// inChain reports whether a printed form of err, or of an error its chain
// wraps, holds a credential ([Credentials.printed]); a chain of more than
// [maxChainErrors] errors counts as holding one.
func (c Credentials) inChain(err error) bool {
	stack := []error{err}
	for n := 0; len(stack) > 0 && n < maxChainErrors; n++ {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e == nil {
			continue
		}
		if c.printed(e) {
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

// printed reports whether a form in which e can be printed holds a
// credential: its text; %+v, which an fmt.Formatter may widen beyond the
// text; and %#v, which shows the fields of an error held by value, a
// request header among them. It runs on the error path only.
func (c Credentials) printed(e error) bool {
	for _, form := range [...]string{e.Error(), fmt.Sprintf("%+v", e), fmt.Sprintf("%#v", e)} {
		if _, found := c.Redact(form); found {
			return true
		}
	}
	return false
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
// transport's error or any value inside it. %+v and %#v print the
// rendering of the transport error's chain with its credentials replaced
// ([Credentials.detail]), so a cause's diagnostic survives as text; every
// other verb prints the text, as for any error.
type scrubbedError struct {
	msg       string
	detail    string
	sentinels []error
}

// Error returns the transport error's text with its credentials replaced.
func (e *scrubbedError) Error() string { return e.msg }

// Format writes the rendering of the transport error's chain for %+v and
// %#v, and the text, under the verb and its flags, for every other verb.
func (e *scrubbedError) Format(f fmt.State, verb rune) {
	if verb == 'v' && (f.Flag('+') || f.Flag('#')) {
		_, _ = io.WriteString(f, e.detail)
		return
	}
	_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), e.msg)
}

// Unwrap returns the sentinels the transport's error matched.
func (e *scrubbedError) Unwrap() []error { return e.sentinels }

// ScrubUserinfo replaces the userinfo of every URL in s ("scheme://user@" or
// "scheme://user:password@") with "***", and reports whether it replaced
// any. A URL's authority is taken to run to the next whitespace or quote, not
// to the next "/": a password written with a raw "/" is still scrubbed, at
// the cost of scrubbing a path that holds an "@".
func ScrubUserinfo(s string) (string, bool) {
	var b strings.Builder
	rest, scrubbed := s, false
	for {
		i := strings.Index(rest, "://")
		if i < 0 {
			break
		}
		b.WriteString(rest[:i+3])
		rest = rest[i+3:]
		end := strings.IndexAny(rest, " \t\n\"'<>")
		if end < 0 {
			end = len(rest)
		}
		if at := strings.LastIndexByte(rest[:end], '@'); at >= 0 {
			b.WriteString("***")
			rest, scrubbed = rest[at:], true
		}
	}
	if !scrubbed {
		return s, false
	}
	b.WriteString(rest)
	return b.String(), true
}
