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

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// shape is what a test reads back from a Node, depth first.
type shape struct {
	Kind    Kind
	Text    string
	IsInt   bool
	Names   []string
	Members []shape
}

func shapeOf(v Node) shape {
	s := shape{Kind: v.Kind(), Text: v.Text(), IsInt: v.IsInt()}
	for i := range v.Len() {
		if v.Kind() == KindObject {
			s.Names = append(s.Names, v.Name(i))
		}
		s.Members = append(s.Members, shapeOf(v.Index(i)))
	}
	return s
}

// TestRead checks the tree Read returns: each kind, the text of strings and
// numbers, which numbers json.loads makes an int of, the order of members
// and elements, and a repeated member name keeping its first position and
// its last value.
func TestRead(t *testing.T) {
	tests := map[string]struct {
		doc  string
		want shape
	}{
		"success: null":   {doc: `null`, want: shape{Kind: KindNull}},
		"success: false":  {doc: `false`, want: shape{Kind: KindFalse}},
		"success: true":   {doc: ` true `, want: shape{Kind: KindTrue}},
		"success: int":    {doc: `-0`, want: shape{Kind: KindNumber, Text: "-0", IsInt: true}},
		"success: float":  {doc: `1.50`, want: shape{Kind: KindNumber, Text: "1.50"}},
		"success: exp":    {doc: `1E5`, want: shape{Kind: KindNumber, Text: "1E5"}},
		"success: string": {doc: `"a\"é"`, want: shape{Kind: KindString, Text: "a\"é"}},
		"success: array": {doc: `[1,"x",[]]`, want: shape{Kind: KindArray, Members: []shape{
			{Kind: KindNumber, Text: "1", IsInt: true}, {Kind: KindString, Text: "x"}, {Kind: KindArray},
		}}},
		"success: object keeps first position and last value": {doc: `{"b":1,"a":{},"b":"two"}`, want: shape{
			Kind: KindObject, Names: []string{"b", "a"},
			Members: []shape{{Kind: KindString, Text: "two"}, {Kind: KindObject}},
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			v, err := Read([]byte(tt.doc))
			if err != nil {
				t.Fatalf("Read(%q) error = %v", tt.doc, err)
			}
			if diff := cmp.Diff(tt.want, shapeOf(v)); diff != "" {
				t.Errorf("Read(%q) mismatch (-want +got):\n%s", tt.doc, diff)
			}
		})
	}
}

// TestNodeMember checks Member on an object, a name it lacks, and every
// other kind, which has no members, and the zero Node, which is null and
// whose Index and Name return the zero Node and the empty name.
func TestNodeMember(t *testing.T) {
	obj, err := Read([]byte(`{"error":{"message":"boom"},"n":null}`))
	if err != nil {
		t.Fatal(err)
	}
	inner, ok := obj.Member("error")
	if !ok || inner.Kind() != KindObject {
		t.Fatalf(`Member("error") = %v, %t; want an object, true`, inner.Kind(), ok)
	}
	if m, ok := inner.Member("message"); !ok || m.Text() != "boom" {
		t.Errorf(`Member("message") = %q, %t; want "boom", true`, m.Text(), ok)
	}
	if n, ok := obj.Member("n"); !ok || n.Kind() != KindNull {
		t.Errorf(`Member("n") = %v, %t; want null, true`, n.Kind(), ok)
	}
	if m, ok := obj.Member("missing"); ok || m.Kind() != KindNull {
		t.Errorf(`Member("missing") = %v, %t; want the zero Node, false`, m.Kind(), ok)
	}
	for _, doc := range []string{`[{"a":1}]`, `"a"`, `1`, `true`, `null`} {
		v, err := Read([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := v.Member("a"); ok {
			t.Errorf("Read(%q).Member(\"a\") found a member; only an object has members", doc)
		}
	}
	var zero Node
	if zero.Kind() != KindNull || zero.Len() != 0 || zero.Text() != "" || zero.IsInt() {
		t.Errorf("zero Node: kind %v, len %d, text %q, int %t; want null, 0, \"\", false", zero.Kind(), zero.Len(), zero.Text(), zero.IsInt())
	}
	for _, i := range []int{0, 1, -1} {
		if got := zero.Index(i); got != (Node{}) {
			t.Errorf("zero Node: Index(%d) = %v, want the zero Node", i, got.Kind())
		}
		if got := zero.Name(i); got != "" {
			t.Errorf("zero Node: Name(%d) = %q, want \"\"", i, got)
		}
	}
	if m, ok := zero.Member("a"); ok || m != (Node{}) {
		t.Errorf("zero Node: Member = %v, %t; want the zero Node, false", m.Kind(), ok)
	}
}

// TestReadRefuses checks that Read refuses what PydanticJSON refuses, with
// the same error.
func TestReadRefuses(t *testing.T) {
	tests := map[string]struct {
		doc  string
		want error
	}{
		"error: NaN token":             {doc: `{"a":NaN}`, want: ErrSyntax},
		"error: two values":            {doc: `1 2`, want: ErrSyntax},
		"error: empty":                 {doc: ``, want: ErrSyntax},
		"error: ill-formed UTF-8":      {doc: "\"a\xffb\"", want: ErrSyntax},
		"error: too many digits":       {doc: "1" + strings.Repeat("0", 4300), want: ErrIntDigits},
		"error: too deeply nested":     {doc: strings.Repeat("[", 10001) + strings.Repeat("]", 10001), want: ErrReadDepth},
		"error: text that is not JSON": {doc: `oops`, want: ErrSyntax},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Read([]byte(tt.doc))
			if !errors.Is(err, tt.want) {
				t.Errorf("Read error = %v, want %v", err, tt.want)
			}
			_, perr := PydanticJSON([]byte(tt.doc))
			if !errors.Is(perr, tt.want) {
				t.Errorf("PydanticJSON error = %v, want %v as Read's", perr, tt.want)
			}
		})
	}
}
