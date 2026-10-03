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

package jsonx

// Kind is the kind of a Node.
type Kind uint8

// The kinds of a Node.
const (
	KindNull Kind = iota
	KindFalse
	KindTrue
	KindNumber
	KindString
	KindArray
	KindObject
)

// Node is one value of a JSON text that Read has read, as Python's
// json.loads holds it: an object keeps the first position of each member
// name and the last value of that name. The zero Node is null.
type Node struct{ n *node }

// Read reads doc, one JSON text in UTF-8, as PydanticJSON reads it, and
// refuses what PydanticJSON refuses with the same errors.
func Read(doc []byte) (Node, error) {
	root, err := read(doc)
	if err != nil {
		return Node{}, err
	}
	return Node{n: &root}, nil
}

// PydanticJSON returns v as PydanticJSON writes the value of a JSON text:
// the text pydantic_core.to_json writes for the Python value json.loads made
// of v. The depth it counts starts at v, so a value that is too deep inside
// its document may be written when it is taken by itself. It refuses with
// ErrDepth.
func (v Node) PydanticJSON() ([]byte, error) {
	if v.n == nil {
		return []byte("null"), nil
	}
	return appendNode(nil, v.n, 1)
}

// Kind returns the kind of v.
func (v Node) Kind() Kind {
	if v.n == nil {
		return KindNull
	}
	switch v.n.kind {
	case kindFalse:
		return KindFalse
	case kindTrue:
		return KindTrue
	case kindNumber:
		return KindNumber
	case kindString:
		return KindString
	case kindArray:
		return KindArray
	case kindObject:
		return KindObject
	}
	return KindNull
}

// Text returns the decoded text of a string, the literal of a number as the
// document spells it, and "" for any other kind.
func (v Node) Text() string {
	if v.n == nil {
		return ""
	}
	return v.n.text
}

// IsInt reports whether v is a number literal with neither a fraction nor
// an exponent, of which json.loads makes an int; any other number becomes a
// float.
func (v Node) IsInt() bool { return v.Kind() == KindNumber && isIntLiteral(v.n.text) }

// Len returns the number of elements of an array or of members of an
// object, and 0 for any other kind.
func (v Node) Len() int {
	if v.n == nil {
		return 0
	}
	return len(v.n.elems)
}

// Index returns the i-th element of an array or the value of the i-th
// member of an object. For the zero Node it returns the zero Node, whatever
// i is; otherwise it panics when i is out of range.
func (v Node) Index(i int) Node {
	if v.n == nil {
		return Node{}
	}
	return Node{n: &v.n.elems[i]}
}

// Name returns the name of the i-th member of an object. For the zero Node
// it returns "", whatever i is; otherwise it panics when v is not an object
// or i is out of range.
func (v Node) Name(i int) string {
	if v.n == nil {
		return ""
	}
	return v.n.names[i]
}

// Member returns the value of the member of an object named name, and
// whether there is one; for any other kind it reports false.
func (v Node) Member(name string) (Node, bool) {
	if v.Kind() != KindObject {
		return Node{}, false
	}
	for i, n := range v.n.names {
		if n == name {
			return v.Index(i), true
		}
	}
	return Node{}, false
}
