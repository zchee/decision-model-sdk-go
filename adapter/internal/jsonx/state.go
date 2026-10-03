// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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
	"strconv"
	"strings"

	"github.com/go-json-experiment/json/jsontext"
)

const (
	// maxValueDepth is the deepest value pydantic-core 2.46.4 writes: the
	// root is at depth 1, and a member or an element is one deeper than its
	// container; a scalar counts like a container. It is the u8 counter of
	// pydantic-core's recursion guard (RECURSION_GUARD_LIMIT, 255).
	maxValueDepth = 255
	// maxReadDepth is the number of arrays and objects the reader opens
	// inside one another, the root among them; a scalar is not a level of it.
	// It is the JSON library's own limit (maxNestingDepth in
	// jsontext/state.go of github.com/go-json-experiment/json
	// v0.0.0-20260820222146-c27c302e5fc3, and of go1.27.1's
	// encoding/json/jsontext), checked here when a container is opened so
	// that it has its own error. A library version with another limit
	// changes this constant.
	maxReadDepth = 10000
	// maxIntDigits is CPython's default limit on the digits of an integer
	// read from text (sys.int_info.default_max_str_digits), the sign not
	// counted; json.loads raises ValueError beyond it.
	maxIntDigits = 4300
)

// The refusals of PydanticJSON and EncodeState. Each is its own value, so
// that a caller can tell them apart with errors.Is.
var (
	// ErrSyntax reports bytes that are not one JSON text in UTF-8. It covers
	// the tokens NaN, Infinity and -Infinity, which Python's json.loads reads
	// and JSON does not have, and a string escape of a surrogate without its
	// partner, anywhere in the text.
	ErrSyntax = errors.New("jsonx: not a JSON text")
	// ErrDepth reports a value nested deeper than 255 levels, a scalar
	// counted as a level, which pydantic-core refuses to write. It is checked
	// on the value that is written, so a too-deep value that a later member
	// of the same name replaces is not refused.
	ErrDepth = errors.New("jsonx: value nested deeper than 255 levels")
	// ErrReadDepth reports a text that opens more than 10000 arrays and
	// objects inside one another, the JSON library's limit, whether or not
	// the value would be written.
	ErrReadDepth = errors.New("jsonx: more than 10000 nested arrays and objects")
	// ErrIntDigits reports an integer literal of more than 4300 digits, the
	// sign not counted, which Python's json.loads refuses, also inside a value
	// that a later member of the same name replaces.
	ErrIntDigits = errors.New("jsonx: integer literal of more than 4300 digits")
)

// The escaped forms of '<' and '>' that upstream puts into the prompt
// (_client.py:90-94 at v0.2.1).
var (
	escapedLess    = []byte("\x5cu003c") // \x5c is a backslash
	escapedGreater = []byte("\x5cu003e")
)

// nodeKind is the kind of one node of a read document.
type nodeKind uint8

const (
	kindNull nodeKind = iota
	kindFalse
	kindTrue
	kindNumber
	kindString
	kindArray
	kindObject
)

// node is one JSON value of a read document, as Python's json.loads holds
// it: an object keeps the first position of each member name and the last
// value of that name.
type node struct {
	kind  nodeKind
	text  string // kindNumber: the literal as read; kindString: the decoded text
	elems []node // kindArray: the elements; kindObject: the member values
	names []string
}

// PydanticJSON returns pydantic_core.to_json(json.loads(doc)) for a JSON
// text in UTF-8, as pydantic-core 2.46.4 and CPython 3.14.3 write it:
// compact; member order kept, a repeated member name keeping its first
// position and its last value; an integer literal written as its value
// (-0 as 0); any other number written by PydanticFloat (1.50 as 1.5, 1E5 as
// 100000.0, 1e400 as Infinity); strings with a backslash before '"' and '\',
// the short escapes \b, \t, \n, \f and \r, a six-byte escape with lowercase
// hex digits for every other byte below 0x20, and everything else as it is.
//
// It refuses with ErrSyntax, ErrDepth, ErrReadDepth or ErrIntDigits. Python
// accepts three kinds of text that PydanticJSON refuses with ErrSyntax or
// ErrReadDepth: one holding a NaN, Infinity or -Infinity token, one holding
// an escaped lone surrogate inside a value that a later member of the same
// name replaces, and one opening more than 10000 arrays and objects inside
// such a value.
func PydanticJSON(doc []byte) ([]byte, error) {
	root, err := read(doc)
	if err != nil {
		return nil, err
	}
	return appendNode(make([]byte, 0, len(doc)), &root, 1)
}

// EncodeState returns the text upstream puts into the prompt for a state
// whose JSON text is doc: PydanticJSON(doc) with every '<' written as its
// six-byte JSON escape (a backslash, 'u', then 003c) and every '>' as its
// escape (a backslash, 'u', then 003e), wherever they stand
// (_serialize_state_as_user_prompt, _client.py:90-94 at v0.2.1). It refuses
// as PydanticJSON does.
func EncodeState(doc []byte) ([]byte, error) {
	out, err := PydanticJSON(doc)
	if err != nil {
		return nil, err
	}
	out = bytes.ReplaceAll(out, []byte{'<'}, escapedLess)
	return bytes.ReplaceAll(out, []byte{'>'}, escapedGreater), nil
}

// read reads doc as one JSON text into a tree.
func read(doc []byte) (node, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(doc), jsontext.AllowDuplicateNames(true))
	root, err := readValue(dec, 1)
	if err != nil {
		return node{}, err
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("data after the top-level value")
		}
		return node{}, fmt.Errorf("%w: %w", ErrSyntax, err)
	}
	return root, nil
}

// readValue reads one value whose containers, if it is one, is the depth-th
// open inside one another.
func readValue(dec *jsontext.Decoder, depth int) (node, error) {
	kind := dec.PeekKind()
	if (kind == '[' || kind == '{') && depth > maxReadDepth {
		return node{}, ErrReadDepth
	}
	tok, err := dec.ReadToken()
	if err != nil {
		return node{}, fmt.Errorf("%w: %w", ErrSyntax, err)
	}
	switch kind {
	case 'n':
		return node{kind: kindNull}, nil
	case 'f':
		return node{kind: kindFalse}, nil
	case 't':
		return node{kind: kindTrue}, nil
	case '"':
		return node{kind: kindString, text: tok.String()}, nil
	case '0':
		lit := tok.String()
		if isIntLiteral(lit) && len(strings.TrimPrefix(lit, "-")) > maxIntDigits {
			return node{}, ErrIntDigits
		}
		return node{kind: kindNumber, text: lit}, nil
	case '[':
		return readArray(dec, depth)
	case '{':
		return readObject(dec, depth)
	}
	// ReadToken reports every token PeekKind does not know as an error.
	return node{}, fmt.Errorf("%w: unexpected token %v", ErrSyntax, tok)
}

// readArray reads the elements of an array whose '[' has been read.
func readArray(dec *jsontext.Decoder, depth int) (node, error) {
	n := node{kind: kindArray}
	for dec.PeekKind() != ']' {
		elem, err := readValue(dec, depth+1)
		if err != nil {
			return node{}, err
		}
		n.elems = append(n.elems, elem)
	}
	if _, err := dec.ReadToken(); err != nil {
		return node{}, fmt.Errorf("%w: %w", ErrSyntax, err)
	}
	return n, nil
}

// readObject reads the members of an object whose '{' has been read. A
// repeated name keeps its first position and takes the later value.
func readObject(dec *jsontext.Decoder, depth int) (node, error) {
	n := node{kind: kindObject}
	var index map[string]int
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return node{}, fmt.Errorf("%w: %w", ErrSyntax, err)
		}
		name := tok.String()
		value, err := readValue(dec, depth+1)
		if err != nil {
			return node{}, err
		}
		if i, ok := index[name]; ok {
			n.elems[i] = value
			continue
		}
		if index == nil {
			index = make(map[string]int)
		}
		index[name] = len(n.names)
		n.names = append(n.names, name)
		n.elems = append(n.elems, value)
	}
	if _, err := dec.ReadToken(); err != nil {
		return node{}, fmt.Errorf("%w: %w", ErrSyntax, err)
	}
	return n, nil
}

// isIntLiteral reports whether a JSON number literal has neither a fraction
// nor an exponent, so that json.loads makes an int of it.
func isIntLiteral(lit string) bool {
	for i := range len(lit) {
		switch lit[i] {
		case '.', 'e', 'E':
			return false
		}
	}
	return true
}

// numberFloat returns the float Python's float() makes of a JSON number
// literal with a fraction or an exponent: the nearest double, or an infinity
// past the largest one (strconv's range error carries the same value).
func numberFloat(lit string) float64 {
	f, _ := strconv.ParseFloat(lit, 64)
	return f
}

// appendNode writes n as pydantic-core writes the value json.loads made of
// it, depth first in document order, and stops at the first value nested
// deeper than maxValueDepth.
func appendNode(dst []byte, n *node, depth int) ([]byte, error) {
	if depth > maxValueDepth {
		return nil, ErrDepth
	}
	var err error
	switch n.kind {
	case kindNull:
		return append(dst, "null"...), nil
	case kindFalse:
		return append(dst, "false"...), nil
	case kindTrue:
		return append(dst, "true"...), nil
	case kindNumber:
		if !isIntLiteral(n.text) {
			return appendPydanticFloat(dst, numberFloat(n.text)), nil
		}
		if n.text == "-0" {
			return append(dst, '0'), nil // int("-0") is 0
		}
		return append(dst, n.text...), nil
	case kindString:
		return appendString(dst, n.text), nil
	case kindArray:
		dst = append(dst, '[')
		for i := range n.elems {
			if i > 0 {
				dst = append(dst, ',')
			}
			if dst, err = appendNode(dst, &n.elems[i], depth+1); err != nil {
				return nil, err
			}
		}
		return append(dst, ']'), nil
	default: // kindObject
		dst = append(dst, '{')
		for i := range n.elems {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendString(dst, n.names[i])
			dst = append(dst, ':')
			if dst, err = appendNode(dst, &n.elems[i], depth+1); err != nil {
				return nil, err
			}
		}
		return append(dst, '}'), nil
	}
}

const hexDigits = "0123456789abcdef"

// appendString appends s as serde_json 1.0.149's compact formatter writes a
// string, which pydantic-core 2.46.4 uses: a backslash before '"' and '\',
// the short escapes for backspace, tab, newline, form feed and carriage
// return, a six-byte escape with lowercase hexadecimal digits for every other
// byte below 0x20, and every other byte as it is (DEL, '/', '<', '>', '&' and
// all non-ASCII text). s is valid UTF-8: the reader refuses anything else.
func appendString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := range len(s) {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		dst = append(dst, s[start:i]...)
		start = i + 1
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
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}
