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
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// allTokens reads doc to its end and returns its tokens without the
// TokenEnd, or the first error with the tokens read before it.
func allTokens(doc string) ([]Token, error) {
	r := NewTokens([]byte(doc))
	var out []Token
	for {
		tok, err := r.Next()
		if err != nil {
			return out, err
		}
		if tok.Kind == TokenEnd {
			return out, nil
		}
		out = append(out, tok)
	}
}

// TestTokens checks the tokens of a text: each kind, a string told from a
// member name, a number as the text spells it, the depth of every token, and
// every member of an object, a repeated name included.
func TestTokens(t *testing.T) {
	tests := map[string]struct {
		doc  string
		want []Token
	}{
		"success: null":                        {doc: `null`, want: []Token{{Kind: TokenNull}}},
		"success: false":                       {doc: ` false `, want: []Token{{Kind: TokenFalse}}},
		"success: true":                        {doc: "\ttrue\r\n", want: []Token{{Kind: TokenTrue}}},
		"success: a number keeps its spelling": {doc: `-0.50E+1`, want: []Token{{Kind: TokenNumber, Text: "-0.50E+1"}}},
		"success: a string is decoded":         {doc: `"a\u00e9\n"`, want: []Token{{Kind: TokenString, Text: "aé\n"}}},
		"success: empty containers": {doc: `[{},[]]`, want: []Token{
			{Kind: TokenBeginArray},
			{Kind: TokenBeginObject, Depth: 1},
			{Kind: TokenEndObject, Depth: 1},
			{Kind: TokenBeginArray, Depth: 1},
			{Kind: TokenEndArray, Depth: 1},
			{Kind: TokenEndArray},
		}},
		"success: a repeated name gives both members": {doc: `{"a":1,"\u0061":[2],"b":"a"}`, want: []Token{
			{Kind: TokenBeginObject},
			{Kind: TokenName, Text: "a", Depth: 1},
			{Kind: TokenNumber, Text: "1", Depth: 1},
			{Kind: TokenName, Text: "a", Depth: 1},
			{Kind: TokenBeginArray, Depth: 1},
			{Kind: TokenNumber, Text: "2", Depth: 2},
			{Kind: TokenEndArray, Depth: 1},
			{Kind: TokenName, Text: "b", Depth: 1},
			{Kind: TokenString, Text: "a", Depth: 1},
			{Kind: TokenEndObject},
		}},
		"success: a string in an array inside an object is not a name": {doc: `{"k":["k",{"k":null}],"l":true}`, want: []Token{
			{Kind: TokenBeginObject},
			{Kind: TokenName, Text: "k", Depth: 1},
			{Kind: TokenBeginArray, Depth: 1},
			{Kind: TokenString, Text: "k", Depth: 2},
			{Kind: TokenBeginObject, Depth: 2},
			{Kind: TokenName, Text: "k", Depth: 3},
			{Kind: TokenNull, Depth: 3},
			{Kind: TokenEndObject, Depth: 2},
			{Kind: TokenEndArray, Depth: 1},
			{Kind: TokenName, Text: "l", Depth: 1},
			{Kind: TokenTrue, Depth: 1},
			{Kind: TokenEndObject},
		}},
		"success: a long integer is one token": {
			doc:  "-" + strings.Repeat("9", 5000),
			want: []Token{{Kind: TokenNumber, Text: "-" + strings.Repeat("9", 5000)}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := allTokens(tt.doc)
			if err != nil {
				t.Fatalf("tokens of %.60q: error = %v", tt.doc, err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("tokens of %.60q mismatch (-want +got):\n%s", tt.doc, diff)
			}
		})
	}
}

// TestTokensEnd checks that the end of the text is reported as TokenEnd on
// every call after the last token.
func TestTokensEnd(t *testing.T) {
	r := NewTokens([]byte(` [] `))
	for _, want := range []TokenKind{TokenBeginArray, TokenEndArray, TokenEnd, TokenEnd, TokenEnd} {
		tok, err := r.Next()
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if tok.Kind != want {
			t.Fatalf("Next() kind = %d, want %d", tok.Kind, want)
		}
	}
}

// TestTokensRefuses checks what the reader refuses, how many tokens it gave
// before, and that a text which ends too early is ErrSyntax and not io.EOF.
func TestTokensRefuses(t *testing.T) {
	tests := map[string]struct {
		doc    string
		want   error
		before int
	}{
		"error: empty text":                 {doc: ``, want: ErrSyntax},
		"error: white space alone":          {doc: " \n", want: ErrSyntax},
		"error: a text that ends too early": {doc: `{"a":[1`, want: ErrSyntax, before: 4},
		"error: data after the value":       {doc: `{} x`, want: ErrSyntax, before: 2},
		"error: a second value":             {doc: `1 2`, want: ErrSyntax, before: 1},
		"error: a NaN token":                {doc: `[NaN]`, want: ErrSyntax, before: 1},
		"error: an Infinity token":          {doc: `{"a":-Infinity}`, want: ErrSyntax, before: 2},
		"error: a lone surrogate escape":    {doc: `["\ud800"]`, want: ErrSyntax, before: 1},
		"error: ill-formed UTF-8":           {doc: "\"a\xffb\"", want: ErrSyntax},
		"error: a byte order mark":          {doc: "\xef\xbb\xbf1", want: ErrSyntax},
		"error: a trailing comma":           {doc: `[1,]`, want: ErrSyntax, before: 2},
		"error: more than 10000 containers": {doc: strings.Repeat("[", 10001), want: ErrReadDepth, before: 10000},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := allTokens(tt.doc)
			if !errors.Is(err, tt.want) {
				t.Fatalf("tokens of %.40q: error = %v, want %v", tt.doc, err, tt.want)
			}
			if errors.Is(err, io.EOF) {
				t.Errorf("tokens of %.40q: error %v is io.EOF, which a caller would take for the end", tt.doc, err)
			}
			if len(got) != tt.before {
				t.Errorf("tokens of %.40q: %d tokens before the error, want %d", tt.doc, len(got), tt.before)
			}
		})
	}

	t.Run("error: every call after data that follows the value", func(t *testing.T) {
		r := NewTokens([]byte(`1 2`))
		if _, err := r.Next(); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if tok, err := r.Next(); !errors.Is(err, ErrSyntax) {
				t.Fatalf("Next() = %+v, %v; want ErrSyntax", tok, err)
			}
		}
	})
	t.Run("success: exactly 10000 containers", func(t *testing.T) {
		got, err := allTokens(strings.Repeat("[", 10000) + strings.Repeat("]", 10000))
		if err != nil || len(got) != 20000 {
			t.Fatalf("%d tokens, error %v; want 20000 and none", len(got), err)
		}
	})
}

// TestTokensAgreeWithRead checks the token reader against Read on every
// document of the state vectors: a text Read reads, or refuses only for the
// depth of the value it would write, has tokens to its end, and their values
// with every repeated name resolved are the tree Read returns; a text Read
// refuses as not JSON is refused by the token reader as not JSON.
func TestTokensAgreeWithRead(t *testing.T) {
	docs, err := readSeedDocuments()
	if err != nil {
		t.Fatal(err)
	}
	read, refused := 0, 0
	for _, doc := range docs {
		node, rerr := Read(doc)
		toks, terr := allTokens(string(doc))
		switch {
		case rerr == nil:
			if terr != nil {
				t.Fatalf("%.60q: Read reads it, the token reader refuses it: %v", doc, terr)
			}
			// The documents nest up to 10000 levels, which reflect.DeepEqual
			// and cmp.Diff walk too slowly; sameShape is a plain recursion.
			pos := 0
			if !sameShape(shapeOf(node), shapeOfTokens(toks, &pos)) {
				t.Fatalf("%.60q: the tokens' value and Read's differ", doc)
			}
			if pos != len(toks) {
				t.Fatalf("%.60q: %d of %d tokens make the value", doc, pos, len(toks))
			}
			read++
		case errors.Is(rerr, ErrSyntax):
			if !errors.Is(terr, ErrSyntax) {
				t.Fatalf("%.60q: Read refuses it as not JSON, the token reader gives %v", doc, terr)
			}
			refused++
		}
	}
	if read == 0 || refused == 0 {
		t.Fatalf("%d documents read and %d refused as not JSON; want both kinds", read, refused)
	}
	t.Logf("%d documents: %d read by both, %d refused by both as not JSON", len(docs), read, refused)
}

// sameShape reports whether two shapes are equal.
func sameShape(a, b shape) bool {
	if a.Kind != b.Kind || a.Text != b.Text || a.IsInt != b.IsInt || !slices.Equal(a.Names, b.Names) || len(a.Members) != len(b.Members) {
		return false
	}
	for i := range a.Members {
		if !sameShape(a.Members[i], b.Members[i]) {
			return false
		}
	}
	return true
}

// shapeOfTokens builds from the tokens at *pos the shape Read gives the same
// value: a repeated member name keeps its first position and its last value.
func shapeOfTokens(toks []Token, pos *int) shape {
	tok := toks[*pos]
	*pos++
	switch tok.Kind {
	case TokenFalse:
		return shape{Kind: KindFalse}
	case TokenTrue:
		return shape{Kind: KindTrue}
	case TokenNumber:
		return shape{Kind: KindNumber, Text: tok.Text, IsInt: isIntLiteral(tok.Text)}
	case TokenString:
		return shape{Kind: KindString, Text: tok.Text}
	case TokenBeginArray:
		s := shape{Kind: KindArray}
		for toks[*pos].Kind != TokenEndArray {
			s.Members = append(s.Members, shapeOfTokens(toks, pos))
		}
		*pos++
		return s
	case TokenBeginObject:
		s := shape{Kind: KindObject}
		index := map[string]int{}
		for toks[*pos].Kind != TokenEndObject {
			name := toks[*pos].Text
			*pos++
			value := shapeOfTokens(toks, pos)
			if i, ok := index[name]; ok {
				s.Members[i] = value
				continue
			}
			index[name] = len(s.Names)
			s.Names = append(s.Names, name)
			s.Members = append(s.Members, value)
		}
		*pos++
		return s
	}
	return shape{Kind: KindNull}
}

// TestNodePydanticJSON checks that a Node is written as PydanticJSON writes
// the same value as a text of its own, that the depth is counted from the
// Node, and that the zero Node is null.
func TestNodePydanticJSON(t *testing.T) {
	doc := `{"s":"a<b\u00e9","n":[1.50,1E5,-0,-0.0,1e400,12345678901234567890123,1e-7],"o":{"b":1,"a":null,"b":true}}`
	root, err := Read([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		member string
		want   string
	}{
		"success: a string keeps < and non-ASCII text":  {member: "s", want: `"a<bé"`},
		"success: numbers in pydantic-core's spelling":  {member: "n", want: `[1.5,100000.0,0,-0.0,Infinity,12345678901234567890123,1e-7]`},
		"success: an object with a repeated name":       {member: "o", want: `{"b":true,"a":null}`},
		"success: a member the object lacks is written": {member: "missing", want: `null`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			v, _ := root.Member(tt.member)
			got, err := v.PydanticJSON()
			if err != nil {
				t.Fatalf("PydanticJSON() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("PydanticJSON() = %s, want %s", got, tt.want)
			}
		})
	}

	whole, err := root.PydanticJSON()
	if err != nil {
		t.Fatal(err)
	}
	want, err := PydanticJSON([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if string(whole) != string(want) {
		t.Errorf("the root's PydanticJSON() = %s, want PydanticJSON of the text, %s", whole, want)
	}

	// 255 levels below the root is one level too deep for the whole text
	// and exactly the limit for the root's one element.
	deep, err := Read([]byte(strings.Repeat("[", 255) + "1" + strings.Repeat("]", 255)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deep.PydanticJSON(); !errors.Is(err, ErrDepth) {
		t.Errorf("256 levels: error = %v, want ErrDepth", err)
	}
	if got, err := deep.Index(0).PydanticJSON(); err != nil || len(got) != 254+1+254 {
		t.Errorf("255 levels: %d bytes, error %v; want %d and none", len(got), err, 254+1+254)
	}
}
