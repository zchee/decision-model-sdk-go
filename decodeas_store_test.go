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
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// The typed store's tests (decodeas_store.go, ruling R116). Mutation
// checks, each planted in a copy of the tree, each failing the named test:
//   - the offset off by one byte (f.offset+1): TestStoreKeepsNeighbours and
//     TestStoreFieldKinds;
//   - another field's offset (the next answer field's): both, too;
//   - a plan offset that is not reflect's (the next field's, or 0):
//     requireStoreLayout stops TestStoreFieldKinds, TestStoreKeepsNeighbours
//     and the tests that decode before them by name, before the store writes
//     (critic-p5 m-3);
//   - a wider write (a ScoreAnswer stored where the field is a NoulAnswer):
//     TestStoreKeepsNeighbours;
//   - the answer copied as bytes, without the typed assignment (and so
//     without write barriers): TestStoreWritesTyped.

// embeddedJSON is an answer for storeKinds's embedded answer field.
const embeddedJSON = `"embedded":{"type":"noul","noul":0.25}`

// storeKinds interleaves the three answer types, and an embedded answer
// field, with fields of every other kind of type, so that a store that
// writes a byte outside its field changes a neighbour.
type storeKinds struct {
	S          string
	B          bool
	Spam       NoulAnswer `typesafe:"kind=noul;name=spam"`
	I8         int8
	I16        int16
	Tone       ChoiceAnswer `typesafe:"kind=choice;name=tone;options=friendly|hostile"`
	I32        int32
	I64        int64
	I          int
	U8         uint8
	Quality    ScoreAnswer `typesafe:"kind=score;name=quality;levels=bad|ok|great"`
	U16        uint16
	U32        uint32
	U64        uint64
	U          uint
	P          uintptr
	F32        float32
	F64        float64
	C64        complex64
	C128       complex128
	Bs         []byte
	M          map[string]int
	Ptr        *int
	Arr        [3]byte
	Any        any
	Ch         chan int
	E          struct{}
	NoulAnswer `typesafe:"kind=noul;name=embedded"`
	Last       byte
}

// storeKindsCmp compares storeKinds values, their answers included.
var storeKindsCmp = gocmp.AllowUnexported(NoulAnswer{}, ChoiceAnswer{}, ScoreAnswer{})

// storeKindsResponse returns a response with an answer for each of
// storeKinds's answer fields.
func storeKindsResponse(t *testing.T) *SystemOneResponse {
	t.Helper()
	resp := new(SystemOneResponse)
	if err := resp.UnmarshalJSON(resultWith(spamJSON, toneJSON, qualityJSON, embeddedJSON)); err != nil {
		t.Fatal(err)
	}
	return resp
}

// wantAnswers returns v with its answer fields set to the answers of resp,
// as the Answers() view holds them.
func wantAnswers(t *testing.T, resp *SystemOneResponse, v storeKinds) storeKinds {
	t.Helper()
	answer := func(name string) wire.Answer {
		a, ok := resp.res.Answers.Get(name)
		if !ok {
			t.Fatalf("the response has no answer %q", name)
		}
		return a
	}
	v.Spam = NoulAnswer{w: answer("spam").Noul, present: true}
	v.Tone = ChoiceAnswer{w: answer("tone").Choice, present: true}
	v.Quality = ScoreAnswer{w: answer("quality").Score, present: true}
	v.NoulAnswer = NoulAnswer{w: answer("embedded").Noul, present: true}
	return v
}

// requireStoreLayout checks that the typed store may write into a T through
// T's plan, and stops the test before any write when it may not: the plan is
// T's, and each answer field's plan offset is reflect's offset of that
// field (the embedded one included: an answer field is never promoted, so no
// offset is a sum), a multiple of its type's alignment, and overlaps no
// other field. Every mismatch is reported, then the test stops with
// t.Fatalf. Without the stop, a wrong offset writes response bytes over a
// neighbour or past the struct, and the run ends in a fault, a checkptr
// failure under -race or a hang in a later comparison instead of failing by
// test name (critic-p5 m-3, condition C3). It returns T's plan; a plan that
// refuses T is returned unchecked, since the store never runs on it.
func requireStoreLayout[T any](t *testing.T) *typedPlan {
	t.Helper()
	typ := reflect.TypeFor[T]()
	p := typedPlanFor[T]()
	if p.err != nil {
		return p
	}
	if p.typ != typ {
		t.Fatalf("plan type = %v, want %v; the store must not write into a %v through it", p.typ, typ, typ)
	}
	bad := 0
	for _, f := range p.fields {
		sf := typ.Field(f.index)
		if f.offset != sf.Offset {
			bad++
			t.Errorf("%v field %s: plan offset %d, want reflect's %d", typ, sf.Name, f.offset, sf.Offset)
		}
		if f.offset%uintptr(sf.Type.Align()) != 0 {
			bad++
			t.Errorf("%v field %s: offset %d is not a multiple of %s's alignment %d", typ, sf.Name, f.offset, sf.Type, sf.Type.Align())
		}
		for j := range typ.NumField() {
			o := typ.Field(j)
			if j != f.index && o.Type.Size() > 0 && o.Offset < f.offset+sf.Type.Size() && f.offset < o.Offset+o.Type.Size() {
				bad++
				t.Errorf("%v answer field %s [%d, %d) overlaps field %s [%d, %d)", typ, sf.Name, f.offset, f.offset+sf.Type.Size(), o.Name, o.Offset, o.Offset+o.Type.Size())
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%v: %d layout mismatches in its plan; the store must not write through it", typ, bad)
	}
	return p
}

// TestStoreFieldKinds checks the store's layout assumptions on a struct of
// every field kind with requireStoreLayout, before any write, and prints the
// alignment and size table of each field. DecodeAs then fills exactly the
// answer fields and leaves every other field zero.
func TestStoreFieldKinds(t *testing.T) {
	typ := reflect.TypeFor[storeKinds]()
	p := requireStoreLayout[storeKinds](t)
	if p.err != nil {
		t.Fatal(p.err)
	}
	answerAt := map[int]bool{}
	for _, f := range p.fields {
		answerAt[f.index] = true
	}
	if len(p.fields) != 4 {
		t.Fatalf("plan has %d answer fields, want 4", len(p.fields))
	}
	for i := range typ.NumField() {
		sf := typ.Field(i)
		t.Logf("LAYOUT %-10s %-14s offset %3d size %3d align %d answer %t", sf.Name, sf.Type, sf.Offset, sf.Type.Size(), sf.Type.Align(), answerAt[i])
	}

	resp := storeKindsResponse(t)
	got, err := DecodeAs[storeKinds](resp)
	if err != nil {
		t.Fatal(err)
	}
	if diff := gocmp.Diff(wantAnswers(t, resp, storeKinds{}), got, storeKindsCmp); diff != "" {
		t.Errorf("DecodeAs[storeKinds] (-want +got):\n%s", diff)
	}
}

// TestStoreKeepsNeighbours checks that the store writes each answer field
// and nothing else: every other field of a storeKinds holding a value, one
// with no zero byte where it can be helped, keeps it through the decode.
func TestStoreKeepsNeighbours(t *testing.T) {
	n := 7
	v := storeKinds{
		S: "neighbour", B: true, I8: -1, I16: -2, I32: -3, I64: -4, I: -5, U8: 0xff, U16: 0xffff, U32: 0xffffffff,
		U64: 1<<64 - 1, U: 1<<64 - 1, P: 1<<64 - 1, F32: -1.5, F64: -2.5, C64: complex(-1, -1), C128: complex(-2, -2),
		Bs: []byte("bytes"), M: map[string]int{"k": 1}, Ptr: &n, Arr: [3]byte{0xff, 0xff, 0xff}, Any: "any",
		Ch: make(chan int), Last: 0xff,
	}
	resp := storeKindsResponse(t)
	want := wantAnswers(t, resp, v)
	p := requireStoreLayout[storeKinds](t)
	if err := p.decode(resp, "", headerRedactor{}, baseOf(&v)); err != nil {
		t.Fatal(err)
	}
	if diff := gocmp.Diff(want, v, storeKindsCmp); diff != "" {
		t.Errorf("after the store (-want +got):\n%s", diff)
	}
}

// TestDecodeTypedPlanMismatch checks invariant 1 of decodeas_store.go: a
// plan of another type is never applied to a T, whose memory its offsets
// do not describe; decodeTyped panics instead.
func TestDecodeTypedPlanMismatch(t *testing.T) {
	resp := storeKindsResponse(t)
	defer func() {
		r := recover()
		msg, _ := r.(string)
		if !strings.Contains(msg, "decodeTyped[typesafe.reviewAnswers] given the plan of typesafe.storeKinds") {
			t.Errorf("recover() = %v, want the plan mismatch panic", r)
		}
	}()
	_, _ = decodeTyped[reviewAnswers](typedPlanFor[storeKinds](), resp, "", headerRedactor{})
	t.Error("decodeTyped with the plan of another type returned")
}

// TestStoreWritesTyped checks invariant 4 of decodeas_store.go on its
// source: the file imports unsafe alone, uses only unsafe.Pointer and
// unsafe.Add, has no compiler directive, no array type and no call of copy
// or append, and writes only by one typed assignment through *F, where F is
// a type parameter constrained to the three answer types, of an unsafe.Add
// result. A copy of the answer's bytes would carry pointers past the write
// barriers.
func TestStoreWritesTyped(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "decodeas_store.go", nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var imports []string
	for _, spec := range f.Imports {
		p, _ := strconv.Unquote(spec.Path.Value)
		imports = append(imports, p)
	}
	if !slices.Equal(imports, []string{"unsafe"}) {
		t.Errorf("imports = %q, want only unsafe", imports)
	}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "//go:") {
				t.Errorf("%s: compiler directive %q", fset.Position(c.Pos()), c.Text)
			}
		}
	}
	adds, stores := 0, 0
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && x.Name == "unsafe" {
				switch n.Sel.Name {
				case "Pointer":
				case "Add":
					adds++
				default:
					t.Errorf("%s: unsafe.%s", fset.Position(n.Pos()), n.Sel.Name)
				}
			}
		case *ast.ArrayType:
			t.Errorf("%s: an array or slice type", fset.Position(n.Pos()))
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && (id.Name == "copy" || id.Name == "append") {
				t.Errorf("%s: a call of %s", fset.Position(n.Pos()), id.Name)
			}
		case *ast.FuncDecl:
			stores += typedStores(t, fset, n)
		}
		return true
	})
	if stores != 1 || adds != 1 {
		t.Errorf("%d typed stores and %d unsafe.Add calls, want one of each, the one inside the store", stores, adds)
	}
}

// typedStores counts the assignments in fn of the form *(*F)(unsafe.Add(…))
// = v, F a type parameter of fn constrained to the three answer types, and
// fails on any other assignment through a pointer dereference.
func typedStores(t *testing.T, fset *token.FileSet, fn *ast.FuncDecl) int {
	t.Helper()
	answerParams := map[string]bool{}
	if fn.Type.TypeParams != nil {
		for _, tp := range fn.Type.TypeParams.List {
			var union []string
			ast.Inspect(tp.Type, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					union = append(union, id.Name)
				}
				return true
			})
			slices.Sort(union)
			if slices.Equal(union, []string{"ChoiceAnswer", "NoulAnswer", "ScoreAnswer"}) {
				for _, name := range tp.Names {
					answerParams[name.Name] = true
				}
			}
		}
	}
	stores := 0
	ast.Inspect(fn, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range as.Lhs {
			star, ok := lhs.(*ast.StarExpr)
			if !ok {
				continue
			}
			conv, ok := star.X.(*ast.CallExpr)
			if !ok || len(conv.Args) != 1 {
				t.Errorf("%s: a store through a pointer that is not a conversion", fset.Position(lhs.Pos()))
				continue
			}
			if !isAnswerStore(conv, answerParams) {
				t.Errorf("%s: a store that is not *(*F)(unsafe.Add(…)) with F one of the answer types", fset.Position(lhs.Pos()))
				continue
			}
			stores++
		}
		return true
	})
	return stores
}

// isAnswerStore reports whether conv is (*F)(unsafe.Add(…)) with F one of
// answerParams.
func isAnswerStore(conv *ast.CallExpr, answerParams map[string]bool) bool {
	ptr, ok := ast.Unparen(conv.Fun).(*ast.StarExpr)
	if !ok {
		return false
	}
	param, ok := ast.Unparen(ptr.X).(*ast.Ident)
	if !ok || !answerParams[param.Name] {
		return false
	}
	add, ok := conv.Args[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := add.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Add"
}
