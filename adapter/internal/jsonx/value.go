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

package jsonx

import (
	"bytes"
	"errors"
	"fmt"
	"math"

	"github.com/go-json-experiment/json/jsontext"
)

// The refusals of Marshal.
var (
	// ErrNonFinite reports a Float that is NaN or an infinity, which JSON
	// cannot hold.
	ErrNonFinite = errors.New("jsonx: float is not finite")
	// ErrInvalid reports a Value that cannot be written as JSON: a Number
	// whose text is not one JSON number, a Raw whose bytes are not one valid
	// JSON value (syntax, UTF-8, a repeated member name), an Object with two
	// members of one name, or nesting past the JSON library's limit of 10000.
	ErrInvalid = errors.New("jsonx: value cannot be written as JSON")
)

// FloatStyle selects the spelling of a Float.
type FloatStyle uint8

const (
	// Pydantic writes a Float as PydanticFloat does: the prompt spelling.
	Pydantic FloatStyle = iota
	// Repr writes a Float as ReprFloat does: the response body spelling.
	Repr
)

// valueKind is the kind of a Value.
type valueKind uint8

const (
	valueNull valueKind = iota
	valueBool
	valueString
	valueNumber
	valueFloat
	valueRaw
	valueArray
	valueObject
)

// Value is one JSON value to write with Marshal. The zero Value is null.
// Build the others with Bool, String, Number, Float, Raw, Array and Object.
type Value struct {
	kind    valueKind
	b       bool
	s       string // valueString: the text; valueNumber: the literal
	f       float64
	style   FloatStyle
	raw     []byte
	elems   []Value
	members []Member
}

// Member is one member of an Object: a name and its value.
type Member struct {
	Name  string
	Value Value
}

// Bool returns the JSON literal true or false.
func Bool(b bool) Value { return Value{kind: valueBool, b: b} }

// String returns a JSON string of s. Marshal writes it with every control
// character escaped and each byte sequence that is not UTF-8 replaced by
// U+FFFD, so text the caller does not control (a provider's message, an
// LLM's output) always gives valid JSON.
func String(s string) Value { return Value{kind: valueString, s: s} }

// Number returns a JSON number written as the literal text, unchanged. Marshal
// refuses a text that is not one JSON number with ErrInvalid.
func Number(text string) Value { return Value{kind: valueNumber, s: text} }

// Float returns a JSON number written as f in the given style. Marshal
// refuses a value that is not finite with ErrNonFinite.
func Float(f float64, style FloatStyle) Value {
	return Value{kind: valueFloat, f: f, style: style}
}

// Raw returns a JSON value that is already encoded, such as a schema or a
// provider's response body. Marshal checks that raw is one valid JSON value
// (UTF-8, no raw control character, no repeated member name, no token that
// JSON does not have) and writes the same value: its insignificant white
// space removed, its numbers spelled as they are, and a string that uses an
// escape it does not need written in the library's canonical form (an
// escaped non-ASCII letter as the letter). It refuses anything else with
// ErrInvalid. raw is not copied, so the caller does not change it before
// Marshal returns.
func Raw(raw []byte) Value { return Value{kind: valueRaw, raw: raw} }

// Array returns a JSON array of elems, in their order.
func Array(elems ...Value) Value { return Value{kind: valueArray, elems: elems} }

// Object returns a JSON object of members, written in the order given.
// Marshal refuses two members of one name with ErrInvalid.
func Object(members ...Member) Value { return Value{kind: valueObject, members: members} }

// Marshal returns v as compact JSON. Members are written in the order v
// holds them. It refuses with ErrNonFinite or ErrInvalid.
func Marshal(v Value) ([]byte, error) {
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf, jsontext.AllowInvalidUTF8(true))
	if err := writeValue(enc, &v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}

// writeValue writes v to enc.
func writeValue(enc *jsontext.Encoder, v *Value) error {
	var err error
	switch v.kind {
	case valueNull:
		err = enc.WriteToken(jsontext.Null)
	case valueBool:
		err = enc.WriteToken(jsontext.Bool(v.b))
	case valueString:
		err = enc.WriteToken(jsontext.String(v.s))
	case valueNumber:
		// A JSON number starts with '-' or a digit and ends with a digit; the
		// encoder checks the rest. White space around it is not allowed,
		// because the text is written unchanged.
		if s := v.s; s == "" || (s[0] != '-' && !isDigit(s[0])) || !isDigit(s[len(s)-1]) {
			return fmt.Errorf("%w: %q is not a JSON number", ErrInvalid, v.s)
		}
		err = enc.WriteValue(jsontext.Value(v.s))
	case valueFloat:
		if math.IsNaN(v.f) || math.IsInf(v.f, 0) {
			return fmt.Errorf("%w: %v", ErrNonFinite, v.f)
		}
		var lit []byte
		if v.style == Repr {
			lit = appendReprFloat(nil, v.f)
		} else {
			lit = appendPydanticFloat(nil, v.f)
		}
		err = enc.WriteValue(lit)
	case valueRaw:
		// The encoder replaces invalid UTF-8 in what it writes; a Raw value
		// must be valid as it is, so it is checked with the library's
		// default options first.
		if !jsontext.Value(v.raw).IsValid() {
			return fmt.Errorf("%w: raw bytes are not one valid JSON value", ErrInvalid)
		}
		err = enc.WriteValue(v.raw)
	case valueArray:
		if err = enc.WriteToken(jsontext.BeginArray); err != nil {
			break
		}
		for i := range v.elems {
			if err := writeValue(enc, &v.elems[i]); err != nil {
				return err
			}
		}
		err = enc.WriteToken(jsontext.EndArray)
	default: // valueObject
		if err = enc.WriteToken(jsontext.BeginObject); err != nil {
			break
		}
		for i := range v.members {
			m := &v.members[i]
			if err := enc.WriteToken(jsontext.String(m.Name)); err != nil {
				return fmt.Errorf("%w: member %q: %w", ErrInvalid, m.Name, err)
			}
			if err := writeValue(enc, &m.Value); err != nil {
				return err
			}
		}
		err = enc.WriteToken(jsontext.EndObject)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
