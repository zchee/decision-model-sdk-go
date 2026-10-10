// Copyright 2026 The decision-model-sdk-go Authors.
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

package provider

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"

	decision "github.com/zchee/decision-model-sdk-go"
)

// The reasons of the [decision.ResponsePathError]s the dialects return.
var (
	errMissing   = errors.New("missing")
	errNotString = errors.New("not a string")
	errNotNumber = errors.New("not a number")
	errNotObject = errors.New("not an object")
	errNotArray  = errors.New("not an array")
)

// pathError returns the error a dialect's ResponseFunc returns for the
// member at path of the vendor's body: the SDK's ResponseValidationError
// names path as its FieldPath.
func pathError(path string, err error) error {
	return &decision.ResponsePathError{Path: path, Err: err}
}

// bodyError returns err, an error of the JSON decoder, as the failure of the
// body as a whole; nil and a path error are returned as they are.
func bodyError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*decision.ResponsePathError](err); ok {
		return err
	}
	return pathError("", err)
}

// newDecoder returns a decoder over body that takes a repeated member's last
// value, as the SDK's own decoder does, and checks that strings are valid
// UTF-8, as it does too.
func newDecoder(body []byte) *jsontext.Decoder {
	return jsontext.NewDecoder(bytes.NewReader(body), jsontext.AllowDuplicateNames(true))
}

// members reads the object the decoder stands before, calling member with
// each member's name while the decoder stands before its value, which
// member must read or skip. A value that is not an object is a failure at
// path. Unknown members are member's to skip: the dialects ignore them, as
// the SDK's decoder does.
func members(dec *jsontext.Decoder, path string, member func(name string) error) error {
	switch dec.PeekKind() {
	case '{':
	case 0:
		_, err := dec.ReadToken()
		return bodyError(err)
	default:
		return pathError(path, errNotObject)
	}
	if _, err := dec.ReadToken(); err != nil {
		return bodyError(err)
	}
	for dec.PeekKind() != '}' {
		name, err := dec.ReadToken()
		if err != nil {
			return bodyError(err)
		}
		if err := member(name.String()); err != nil {
			return err
		}
	}
	_, err := dec.ReadToken()
	return bodyError(err)
}

// elements reads the array the decoder stands before, calling element with
// each element's index while the decoder stands before the element, which
// element must read or skip. A value that is not an array is a failure at
// path.
func elements(dec *jsontext.Decoder, path string, element func(i int) error) error {
	switch dec.PeekKind() {
	case '[':
	case 0:
		_, err := dec.ReadToken()
		return bodyError(err)
	default:
		return pathError(path, errNotArray)
	}
	if _, err := dec.ReadToken(); err != nil {
		return bodyError(err)
	}
	for i := 0; dec.PeekKind() != ']'; i++ {
		if err := element(i); err != nil {
			return err
		}
	}
	_, err := dec.ReadToken()
	return bodyError(err)
}

// atEnd checks that nothing but whitespace follows the top-level value, as
// the SDK's decoder does.
func atEnd(dec *jsontext.Decoder) error {
	_, err := dec.ReadToken()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		err = errors.New("data after the top-level value")
	}
	return pathError("", err)
}

// readValue reads the value the decoder stands before, as a copy that the
// decoder's next read does not overwrite.
func readValue(dec *jsontext.Decoder) (jsontext.Value, error) {
	v, err := dec.ReadValue()
	if err != nil {
		return nil, bodyError(err)
	}
	return v.Clone(), nil
}

// stringAt returns the string v holds, or the failure at path of a v that is
// absent (nil) or not a string.
func stringAt(v jsontext.Value, path string) (string, error) {
	switch {
	case v == nil:
		return "", pathError(path, errMissing)
	case v.Kind() != '"':
		return "", pathError(path, errNotString)
	}
	return unquote(v), nil
}

// numberAt returns v, a number as the vendor spelled it, or the failure at
// path of a v that is absent (nil) or not a number.
func numberAt(v jsontext.Value, path string) (jsontext.Value, error) {
	switch {
	case v == nil:
		return nil, pathError(path, errMissing)
	case v.Kind() != '0':
		return nil, pathError(path, errNotNumber)
	}
	return v, nil
}

// unquote returns the text of v, a JSON string the decoder has checked.
func unquote(v jsontext.Value) string {
	s, _ := jsontext.AppendUnquote(nil, v)
	return string(s)
}

// index returns the position of i inside path, such as "answers[2]".
func index(path string, i int) string { return path + "[" + strconv.Itoa(i) + "]" }

// encoder writes one JSON value into a new buffer, keeping the first error
// of its writes, so that a dialect writes a body as a sequence of tokens and
// checks once.
type encoder struct {
	buf bytes.Buffer
	enc *jsontext.Encoder
	err error
}

// newEncoder returns an encoder over a new buffer.
func newEncoder() *encoder {
	e := new(encoder)
	e.enc = jsontext.NewEncoder(&e.buf)
	return e
}

// token writes t, unless an earlier write failed.
func (e *encoder) token(t jsontext.Token) {
	if e.err == nil {
		e.err = e.enc.WriteToken(t)
	}
}

// value writes v, unless an earlier write failed.
func (e *encoder) value(v jsontext.Value) {
	if e.err == nil {
		e.err = e.enc.WriteValue(v)
	}
}

// str writes the string s.
func (e *encoder) str(s string) { e.token(jsontext.String(s)) }

// bytes returns what was written, without the newline the encoder ends a
// top-level value with, or the first error.
func (e *encoder) bytes() ([]byte, error) {
	if e.err != nil {
		return nil, e.err
	}
	return bytes.TrimSuffix(e.buf.Bytes(), []byte("\n")), nil
}

// errorType returns the System One error_type a vendor's failure with status
// stands for: authentication_error for 401, api_usage_error for a status
// usage names, and "" for any other. The vendors' bodies name no System One
// type of their own, and their status already tells the two apart.
func errorType(status int, usage ...int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case slices.Contains(usage, status):
		return "api_usage_error"
	}
	return ""
}

// success reports whether status is a 2xx.
func success(status int) bool { return status >= 200 && status <= 299 }
