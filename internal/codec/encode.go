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
// limitations under the License.

package codec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bytedance/sonic/encoder"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// encodeOptions are the sonic options of every encode, the ones spike S-E1
// froze NF1 with: none. Strings are neither HTML-escaped nor validated (the
// state encoder checks UTF-8 itself, see [EncodeState]), map members are
// written in Go's iteration order, and the output of a json.Marshaler is
// validated but not compacted.
const encodeOptions encoder.Options = 0

var (
	// ErrStateShape reports a state that is not a JSON string, array or
	// object. The API takes text, a JSON object or an array as the state;
	// null, booleans and numbers are refused before any request is sent.
	ErrStateShape = errors.New("not a string, an array or an object")

	// ErrPlainBytes reports a state or a body member value that is a plain
	// []byte: sonic would send it as a base64 string, which is rarely what
	// the caller meant (text, or JSON that is already encoded).
	ErrPlainBytes = errors.New("a plain []byte is ambiguous")

	// ErrRawValue reports raw JSON that does not start a JSON value.
	ErrRawValue = errors.New("not a JSON value")
)

// EncodeError reports a value that sonic cannot encode: NaN or an infinity,
// a type JSON has no form for (a channel, a function, a complex number), a
// value nested too deep (a cycle), or a json.Marshaler whose output is not
// valid JSON. Err is sonic's error.
type EncodeError struct {
	Err error
}

// sonicMarshalerSyntax starts sonic's error for a json.Marshaler whose output
// is not valid JSON (internal/encoder/vars/errors.go, Error_marshaler:
// "invalid Marshaler output json syntax at %d: %q"), which quotes the whole
// output: the caller's data.
const sonicMarshalerSyntax = "invalid Marshaler output json syntax at "

// Error returns sonic's message, except for a json.Marshaler whose output is
// not valid JSON, such as a nested RawJSON or JSON Content: sonic's message
// quotes that output, which is the caller's data, so only the position sonic
// reports is kept (it can lie past the end of a truncated output). Unwrap
// still gives sonic's error.
func (e *EncodeError) Error() string {
	msg := e.Err.Error()
	if rest, ok := strings.CutPrefix(msg, sonicMarshalerSyntax); ok {
		if pos, _, ok := strings.Cut(rest, ":"); ok && pos != "" && strings.Trim(pos, "0123456789") == "" {
			return "a MarshalJSON method returned invalid JSON (syntax error at position " + pos + ")"
		}
	}
	return msg
}

// Unwrap returns sonic's error.
func (e *EncodeError) Unwrap() error { return e.Err }

// EncodeState appends the JSON encoding of a request state to *buf with
// sonic's encoder.EncodeInto and checks the result: it must be a JSON
// string, array or object ([ErrStateShape] otherwise: nil, booleans,
// numbers, and nil maps, slices and pointers, which encode as null), and
// valid UTF-8 ([wire.ErrInvalidUTF8] otherwise: sonic copies the bytes of a
// Go string as they are, and a body with an invalid byte is not JSON text).
// A plain []byte fails with [ErrPlainBytes]; a []byte nested inside the
// state is sent as a base64 string, as encoding/json does. A value sonic
// cannot encode fails with an [*EncodeError].
//
// Floats keep sonic's spelling, which is encoding/json's (ruling R46): 3.0 is
// written 3, -0.0 as 0, and 1e16 <= |x| < 1e21 and 1e-6 <= |x| < 1e-5 in
// fixed digits. Raw JSON appended with [AppendRawState], or a number carried
// as a string, keeps an exact spelling. A map's members are written in Go's
// iteration order, which changes from one encode to the next (ruling R55):
// a struct or raw JSON gives stable bytes.
//
// On failure *buf keeps its length from before the call. The checks cost one
// pass over the encoded bytes and allocate nothing.
func EncodeState(buf *[]byte, state any) error {
	if _, ok := state.([]byte); ok {
		return ErrPlainBytes
	}
	start := len(*buf)
	if err := encoder.EncodeInto(buf, state, encodeOptions); err != nil {
		*buf = (*buf)[:start]
		return &EncodeError{Err: err}
	}
	enc := (*buf)[start:]
	var err error
	switch i := skipSpace(enc); {
	case i == len(enc):
		err = fmt.Errorf("%s encodes as nothing, %w", typeName(state), ErrStateShape)
	case enc[i] != '{' && enc[i] != '[' && enc[i] != '"':
		err = fmt.Errorf("%s encodes as %s, %w", typeName(state), describe(enc[i]), ErrStateShape)
	case !utf8.Valid(enc):
		err = wire.ErrInvalidUTF8
	}
	if err != nil {
		*buf = (*buf)[:start]
		return err
	}
	return nil
}

// EncodeValue appends the JSON encoding of v, any JSON value, to *buf with
// sonic's encoder.EncodeInto, for a member of the request body other than
// the state. Unlike [EncodeState] it takes null, booleans and numbers; the
// output must still be valid UTF-8. A plain []byte fails with
// [ErrPlainBytes], as for the state (ruling R56), and a []byte nested inside
// v is sent as a base64 string. A value sonic cannot encode fails with an
// [*EncodeError]. On failure *buf keeps its length from before the call.
func EncodeValue(buf *[]byte, v any) error {
	if _, ok := v.([]byte); ok {
		return ErrPlainBytes
	}
	start := len(*buf)
	if err := encoder.EncodeInto(buf, v, encodeOptions); err != nil {
		*buf = (*buf)[:start]
		return &EncodeError{Err: err}
	}
	if !utf8.Valid((*buf)[start:]) {
		*buf = (*buf)[:start]
		return wire.ErrInvalidUTF8
	}
	return nil
}

// AppendRawState appends raw, an encoded state, to *buf as it is, after an
// O(1) shape check: its first byte other than JSON whitespace must be '{',
// '[' or '"' ([ErrStateShape] otherwise, including for raw that is empty or
// only whitespace). raw is not otherwise checked: that it is one valid JSON
// value is the caller's contract.
func AppendRawState(buf *[]byte, raw []byte) error {
	switch i := skipSpace(raw); {
	case i == len(raw):
		return fmt.Errorf("raw JSON is empty, %w", ErrStateShape)
	case raw[i] != '{' && raw[i] != '[' && raw[i] != '"':
		return fmt.Errorf("raw JSON starts with %s, %w", describe(raw[i]), ErrStateShape)
	}
	*buf = append(*buf, raw...)
	return nil
}

// AppendRawValue appends raw, one encoded JSON value, to *buf as it is,
// after an O(1) check that its first byte other than JSON whitespace can
// start a JSON value ([ErrRawValue] otherwise, including for raw that is
// empty or only whitespace). raw is not otherwise checked.
func AppendRawValue(buf *[]byte, raw []byte) error {
	i := skipSpace(raw)
	if i == len(raw) {
		return fmt.Errorf("raw JSON is empty, %w", ErrRawValue)
	}
	switch c := raw[i]; {
	case c == '{', c == '[', c == '"', c == 't', c == 'f', c == 'n', c == '-', '0' <= c && c <= '9':
	default:
		return fmt.Errorf("raw JSON starts with %s, %w", describe(c), ErrRawValue)
	}
	*buf = append(*buf, raw...)
	return nil
}

// skipSpace returns the offset of the first byte of b that is not JSON
// whitespace (space, tab, line feed, carriage return), or len(b).
func skipSpace(b []byte) int {
	for i, c := range b {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return i
		}
	}
	return len(b)
}

// describe names what a JSON text starting with c is, for an error message.
func describe(c byte) string {
	switch {
	case c == 'n':
		return "null"
	case c == 't' || c == 'f':
		return "a boolean"
	case c == '-' || '0' <= c && c <= '9':
		return "a number"
	case c < utf8.RuneSelf:
		return strconv.QuoteRune(rune(c))
	default:
		return fmt.Sprintf("byte 0x%02x", c)
	}
}

// typeName names the Go type of a state for an error message.
func typeName(v any) string {
	if v == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", v)
}
