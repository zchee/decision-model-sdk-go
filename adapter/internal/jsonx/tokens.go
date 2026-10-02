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
	"io"

	"github.com/go-json-experiment/json/jsontext"
)

// TokenKind is the kind of a Token.
type TokenKind uint8

// The kinds of a Token. TokenEnd, the zero value, is what Next returns once
// the text has ended after its one value.
const (
	TokenEnd TokenKind = iota
	TokenNull
	TokenFalse
	TokenTrue
	TokenNumber
	TokenString
	TokenName
	TokenBeginArray
	TokenEndArray
	TokenBeginObject
	TokenEndObject
)

// Token is one token of a JSON text, as Tokens reads it.
type Token struct {
	// Kind is the token's kind.
	Kind TokenKind
	// Text is the decoded text of a TokenString or a TokenName and the
	// literal of a TokenNumber as the text spells it; it is "" for any other
	// kind.
	Text string
	// Depth is the number of arrays and objects that are open around the
	// token. For the token that opens or closes a container, the container
	// itself is not counted: the top-level value has Depth 0.
	Depth int
}

// Tokens reads a JSON text token by token, for code that must see what
// Read drops: every member of an object, a repeated name included, with each
// number as the text spells it and each token's depth.
//
// It refuses what is not one JSON text in UTF-8 as Read does (ErrSyntax,
// the tokens NaN, Infinity and -Infinity included) and a text that opens more
// than 10000 arrays and objects inside one another (ErrReadDepth). It has no
// limit on the digits of a number and none on the depth of a value below
// that: those are rules of whoever reads the tokens.
type Tokens struct {
	dec *jsontext.Decoder
	// open holds, for each open container, whether it is an object.
	open []bool
	// name says that the next token of the innermost object is a member
	// name.
	name bool
	// done says that the top-level value has been read to its end.
	done bool
	// err is the first error, which every later call returns again.
	err error
}

// NewTokens returns a reader of the tokens of doc.
func NewTokens(doc []byte) *Tokens {
	return &Tokens{dec: jsontext.NewDecoder(bytes.NewReader(doc), jsontext.AllowDuplicateNames(true))}
}

// Next returns the next token of the text. After the last token of the
// top-level value it returns a Token of kind TokenEnd, again on every later
// call, or ErrSyntax when anything but white space follows that value. After
// an error, every later call returns the same error.
func (t *Tokens) Next() (Token, error) {
	if t.err != nil {
		return Token{}, t.err
	}
	tok, err := t.next()
	t.err = err
	return tok, err
}

// next reads one token.
func (t *Tokens) next() (Token, error) {
	if t.done {
		if _, err := t.dec.ReadToken(); !errors.Is(err, io.EOF) {
			if err == nil {
				err = errors.New("data after the top-level value")
			}
			return Token{}, fmt.Errorf("%w: %w", ErrSyntax, err)
		}
		return Token{}, nil
	}
	kind := t.dec.PeekKind()
	if (kind == '[' || kind == '{') && len(t.open) >= maxReadDepth {
		return Token{}, ErrReadDepth
	}
	tok, err := t.dec.ReadToken()
	if err != nil {
		// The text ended before its value: io.EOF here is not the end that
		// Next reports with TokenEnd, so the error does not wrap it.
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return Token{}, fmt.Errorf("%w: %w", ErrSyntax, err)
	}
	out := Token{Depth: len(t.open)}
	switch kind {
	case 'n':
		out.Kind = TokenNull
	case 'f':
		out.Kind = TokenFalse
	case 't':
		out.Kind = TokenTrue
	case '0':
		out.Kind, out.Text = TokenNumber, tok.String()
	case '"':
		out.Kind, out.Text = TokenString, tok.String()
		if t.name {
			out.Kind = TokenName
			t.name = false
			return out, nil
		}
	case '[', '{':
		out.Kind = TokenBeginArray
		if kind == '{' {
			out.Kind = TokenBeginObject
		}
		t.open = append(t.open, kind == '{')
		t.name = kind == '{'
		return out, nil
	case ']', '}':
		out.Kind = TokenEndArray
		if kind == '}' {
			out.Kind = TokenEndObject
		}
		t.open = t.open[:len(t.open)-1]
		out.Depth = len(t.open)
	}
	// A value has ended: the top-level one, an element, or a member's value,
	// after which the innermost object expects a name again.
	t.done = len(t.open) == 0
	t.name = !t.done && t.open[len(t.open)-1]
	return out, nil
}
