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
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/engine"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// The typed store's tests (decodeas_store.go). Mutation
// checks, each planted in a copy of the tree, each failing the named test:
//   - buildPlan recording an offset, an end or a kind that is not reflect's
//     (the next field's offset, 0, past the last field, or the offset and
//     end of another field of the same size): checkPlanLayout panics by
//     name when the plan is built, in every test that builds it, before any
//     decode; TestPlanLayoutRefusesCorruptPlans plants each;
//   - a plan that is wrong after it is built, or a store of another answer
//     type than the field's: the store's per-write bound (invariant 6) panics
//     with its storeRefusal, which names the type, the field and the offset,
//     before a byte is written; requireStoreLayout stops TestStoreFieldKinds,
//     TestStoreKeepsNeighbours and the tests that decode before them by name,
//     before the store; TestStoreRefusesOutsideField plants each such plan and
//     checks the refusal;
//   - the bound removed, or reduced to one of its three comparisons:
//     TestStoreRefusesOutsideField;
//   - the write at another offset than the one the bound checked, such as
//     unsafe.Add(b.p, off+1): checkStoreShape, which TestMain runs before
//     every test;
//   - the answer copied as bytes, without the typed assignment (and so
//     without write barriers): checkStoreShape, as above.

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
		a, ok := resp.result().Answers.Get(name)
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
// other field, and its recorded end is that offset plus its type's size.
// Every mismatch is reported, then the test stops with t.Fatalf. Without the
// stop, a wrong offset writes response bytes over a neighbour or past the
// struct, and the run ends in a fault, a checkptr failure under -race or a
// hang in a later comparison instead of failing by test name. It returns T's
// plan; a plan that refuses T is returned unchecked, since the store never
// runs on it.
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
		if want := sf.Offset + sf.Type.Size(); f.end != want {
			bad++
			t.Errorf("%v field %s: plan end %d, want reflect's offset plus size %d", typ, sf.Name, f.end, want)
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
// every field kind with requireStoreLayout, before any write. DecodeAs then
// fills exactly the answer fields and leaves every other field zero.
func TestStoreFieldKinds(t *testing.T) {
	p := requireStoreLayout[storeKinds](t)
	if p.err != nil {
		t.Fatal(p.err)
	}
	if len(p.fields) != 4 {
		t.Fatalf("plan has %d answer fields, want 4", len(p.fields))
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
	if err := p.decode(resp, "", engine.HeaderRedactor{}, baseOf(&v)); err != nil {
		t.Fatal(err)
	}
	if diff := gocmp.Diff(want, v, storeKindsCmp); diff != "" {
		t.Errorf("after the store (-want +got):\n%s", diff)
	}
}

// TestStoreRefusesOutsideField checks invariant 6 of decodeas_store.go, the
// per-write bound: a plan whose first answer field, Spam, is recorded at a
// place its answer does not fit makes the store panic with a storeRefusal
// that names the type, the field and the offset, before it writes a byte,
// whether the offset is wrong (the shapes the subtests name S1 to S3, and
// past the struct), the recorded end is, or the answer is of
// another type than the field. Each row plants its plan in a copy of
// storeKinds's plan and decodes into a storeKinds whose every field holds a
// value; the value must come out of the panic unchanged.
func TestStoreRefusesOutsideField(t *testing.T) {
	good := requireStoreLayout[storeKinds](t)
	typ := reflect.TypeFor[storeKinds]()
	size := typ.Size()
	spam, tone := good.fields[0], good.fields[1]
	if typ.Field(spam.index).Name != "Spam" || typ.Field(tone.index).Name != "Tone" {
		t.Fatalf("storeKinds's first answer fields are %s and %s, want Spam and Tone", typ.Field(spam.index).Name, typ.Field(tone.index).Name)
	}
	noulSize := reflect.TypeFor[NoulAnswer]().Size()
	tests := map[string]struct {
		plant      func(f *typedField)
		wantOffset uintptr
		wantBytes  uintptr
	}{
		"error: the offset one byte on (S1)": {
			plant:      func(f *typedField) { f.offset++ },
			wantOffset: spam.offset + 1, wantBytes: noulSize,
		},
		"error: the offset of the next field (S2)": {
			plant:      func(f *typedField) { f.offset += 8 },
			wantOffset: spam.offset + 8, wantBytes: noulSize,
		},
		"error: the offset of another answer field": {
			plant:      func(f *typedField) { f.offset = tone.offset },
			wantOffset: tone.offset, wantBytes: noulSize,
		},
		"error: every offset 0 (S3)": {
			plant:      func(f *typedField) { f.offset = 0 },
			wantOffset: 0, wantBytes: noulSize,
		},
		"error: an offset past the last field": {
			plant:      func(f *typedField) { f.offset = size },
			wantOffset: size, wantBytes: noulSize,
		},
		"error: an offset whose end wraps around": {
			plant:      func(f *typedField) { f.offset = ^uintptr(0) - 7 },
			wantOffset: ^uintptr(0) - 7, wantBytes: noulSize,
		},
		"error: offset and end moved together so that the end wraps around": {
			plant:      func(f *typedField) { f.offset, f.end = ^uintptr(0)-7, ^uintptr(0)-7+noulSize },
			wantOffset: ^uintptr(0) - 7, wantBytes: noulSize,
		},
		"error: offset and end moved together past the struct": {
			plant:      func(f *typedField) { f.offset, f.end = size, size+noulSize },
			wantOffset: size, wantBytes: noulSize,
		},
		"error: offset and end moved together to overlap the struct's end": {
			plant:      func(f *typedField) { f.offset, f.end = size-8, size-8+noulSize },
			wantOffset: size - 8, wantBytes: noulSize,
		},
		"error: a ChoiceAnswer stored into the NoulAnswer field": {
			plant: func(f *typedField) {
				f.name, f.kind, f.options = tone.name, tone.kind, tone.options
			},
			wantOffset: spam.offset, wantBytes: reflect.TypeFor[ChoiceAnswer]().Size(),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			bad := *good
			bad.fields = slices.Clone(good.fields)
			tt.plant(&bad.fields[0])
			n := 7
			v := storeKinds{S: "neighbour", B: true, I8: -1, Ptr: &n, Any: "any", Last: 0xff}
			before := v
			resp := storeKindsResponse(t)
			func() {
				defer func() {
					r := recover()
					refusal, ok := r.(storeRefusal)
					if !ok {
						t.Fatalf("recover() = %#v, want a storeRefusal", r)
					}
					msg := refusal.Error()
					t.Logf("panic: %s", msg)
					for _, want := range []string{
						"refused to write field Spam ",
						" of typesafe.storeKinds ",
						" at offset " + strconv.FormatUint(uint64(tt.wantOffset), 10) + ":",
						" a " + strconv.FormatUint(uint64(tt.wantBytes), 10) + "-byte write ",
						" of the struct's " + strconv.FormatUint(uint64(size), 10) + ",",
					} {
						if !strings.Contains(msg, want) {
							t.Errorf("panic message %q does not contain %q", msg, want)
						}
					}
				}()
				_ = bad.decode(resp, "", engine.HeaderRedactor{}, baseOf(&v))
				t.Error("decode with a plan the bound refuses returned")
			}()
			if diff := gocmp.Diff(before, v, storeKindsCmp); diff != "" {
				t.Errorf("the refused store changed the struct (-before +after):\n%s", diff)
			}
		})
	}
}

// TestPlanLayoutRefusesCorruptPlans checks checkPlanLayout, the plan-build
// half of invariant 2 of decodeas_store.go: each row corrupts one answer field
// of a copy of storeKinds's plan, among them the in-bounds case the store's
// per-write bound cannot see, Spam's offset and end moved together onto the
// embedded NoulAnswer field of the same size, and the check must panic with a
// message naming the type, the field and both layouts. The good plan passes.
func TestPlanLayoutRefusesCorruptPlans(t *testing.T) {
	good := requireStoreLayout[storeKinds](t)
	typ := reflect.TypeFor[storeKinds]()
	byName := map[string]int{}
	for i, f := range good.fields {
		byName[typ.Field(f.index).Name] = i
	}
	spam, embedded, tone := good.fields[byName["Spam"]], good.fields[byName["NoulAnswer"]], good.fields[byName["Tone"]]
	checkPlanLayout(typ, good.fields)
	tests := map[string]struct {
		plant func(fs []typedField)
		want  []string
	}{
		"error: Spam's offset and end moved together onto the embedded NoulAnswer (V72)": {
			plant: func(fs []typedField) {
				fs[byName["Spam"]].offset, fs[byName["Spam"]].end = embedded.offset, embedded.end
			},
			want: []string{"field Spam ", "as a NoulAnswer at bytes [" + strconv.FormatUint(uint64(embedded.offset), 10) + ", ", "reflect gives a typesafe.NoulAnswer at bytes [" + strconv.FormatUint(uint64(spam.offset), 10) + ", "},
		},
		"error: the two NoulAnswer fields' records swapped": {
			plant: func(fs []typedField) {
				a, b := byName["Spam"], byName["NoulAnswer"]
				fs[a].offset, fs[b].offset, fs[a].end, fs[b].end = fs[b].offset, fs[a].offset, fs[b].end, fs[a].end
			},
			want: []string{"field Spam ", "as a NoulAnswer at bytes [" + strconv.FormatUint(uint64(embedded.offset), 10) + ", "},
		},
		"error: an offset of the next field": {
			plant: func(fs []typedField) { fs[byName["Tone"]].offset += 8 },
			want:  []string{"field Tone ", "as a ChoiceAnswer at bytes [" + strconv.FormatUint(uint64(tone.offset+8), 10) + ", "},
		},
		"error: an offset 0": {
			plant: func(fs []typedField) { fs[byName["Tone"]].offset = 0 },
			want:  []string{"field Tone ", "as a ChoiceAnswer at bytes [0, "},
		},
		"error: an end one byte short": {
			plant: func(fs []typedField) { fs[byName["Tone"]].end-- },
			want:  []string{"field Tone ", "as a ChoiceAnswer at bytes [" + strconv.FormatUint(uint64(tone.offset), 10) + ", " + strconv.FormatUint(uint64(tone.end-1), 10) + ")"},
		},
		"error: the ChoiceAnswer field recorded as a NoulAnswer of its size": {
			plant: func(fs []typedField) { fs[byName["Tone"]].kind = wire.KindNoul },
			want:  []string{"field Tone ", "as a NoulAnswer at bytes [", "reflect gives a typesafe.ChoiceAnswer at bytes ["},
		},
		"error: the index of a field that is not an answer": {
			plant: func(fs []typedField) { fs[byName["Spam"]].index = 0 },
			want:  []string{"field S ", "reflect gives a string at bytes [0, 16)"},
		},
		"error: an index past the struct's fields": {
			plant: func(fs []typedField) { fs[byName["Spam"]].index = typ.NumField() },
			want:  []string{`records question "spam" at field index ` + strconv.Itoa(typ.NumField()) + ", outside the struct's"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fs := slices.Clone(good.fields)
			tt.plant(fs)
			defer func() {
				msg, _ := recover().(string)
				t.Logf("panic: %s", msg)
				if !strings.HasPrefix(msg, "typesafe: the typed plan of typesafe.storeKinds ") {
					t.Fatalf("recover() = %q, want checkPlanLayout's panic naming typesafe.storeKinds", msg)
				}
				for _, want := range tt.want {
					if !strings.Contains(msg, want) {
						t.Errorf("panic message %q does not contain %q", msg, want)
					}
				}
			}()
			checkPlanLayout(typ, fs)
			t.Error("checkPlanLayout accepted a corrupt plan")
		})
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
	_, _ = decodeTyped[reviewAnswers](typedPlanFor[storeKinds](), resp, "", engine.HeaderRedactor{})
	t.Error("decodeTyped with the plan of another type returned")
}

// TestMain runs checkStoreShape before any test of the package. A store that
// writes past the offset its bound checked, such as
// *(*F)(unsafe.Add(b.p, off+8)) = v, corrupts memory in the first test that
// decodes a typed answer, and that test hangs until the binary's timeout;
// checked first, the package fails at once, with a failure line that still
// carries the name TestStoreWritesTyped, the test that ran this check before.
func TestMain(m *testing.M) {
	var failures shapeFailures
	checkStoreShape(&failures)
	if len(failures) > 0 {
		fmt.Fprintln(os.Stderr, "--- FAIL: TestStoreWritesTyped (run by TestMain before every test)")
		for _, f := range failures {
			fmt.Fprintln(os.Stderr, "    "+f)
		}
		fmt.Fprintln(os.Stderr, "FAIL")
		os.Exit(1)
	}
	m.Run()
}

// shapeReporter receives the failures of checkStoreShape: TestMain's
// shapeFailures.
type shapeReporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// shapeFailures keeps the failures checkStoreShape reports to TestMain.
type shapeFailures []string

// Helper does nothing: TestMain prints the failures as they are.
func (*shapeFailures) Helper() {}

// Errorf keeps one failure.
func (f *shapeFailures) Errorf(format string, args ...any) {
	*f = append(*f, fmt.Sprintf(format, args...))
}

// checkStoreShape checks invariants 4 and 6 of decodeas_store.go on its
// source, and reports to t every way the file departs from them: the file
// imports unsafe alone, uses only unsafe.Pointer, unsafe.Sizeof and
// unsafe.Add, has no compiler directive, no array type and no call of copy
// or append, and writes only by one typed assignment through *F, where F is
// a type parameter constrained to the three answer types, of an unsafe.Add
// result. A copy of the answer's bytes would carry pointers past the write
// barriers. The offset that unsafe.Add takes is a plain name that the
// condition of an earlier if statement, whose body is a panic, uses, and
// which is defined once, from the offset the plan recorded for the field,
// and never assigned again: the store writes at the offset its bound
// checked.
func checkStoreShape(t shapeReporter) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "decodeas_store.go", nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Errorf("parse decodeas_store.go: %v", err)
		return
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
				case "Pointer", "Sizeof":
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
func typedStores(t shapeReporter, fset *token.FileSet, fn *ast.FuncDecl) int {
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
			if off := conv.Args[0].(*ast.CallExpr).Args; len(off) != 2 || !checkedBefore(fn, as, off[1]) || !fromOffset(fn, off[1].(*ast.Ident).Name) {
				t.Errorf("%s: a store whose offset is not the plain name, defined once from the field's offset, that the bound before it checks", fset.Position(lhs.Pos()))
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

// checkedBefore reports whether off, the offset a store passes to
// unsafe.Add, is a plain name that the condition of an if statement of fn's
// body before the store uses, where the if statement's body is a single
// call of panic (invariant 6: the bound).
func checkedBefore(fn *ast.FuncDecl, store *ast.AssignStmt, off ast.Expr) bool {
	name, ok := off.(*ast.Ident)
	if !ok || fn.Body == nil {
		return false
	}
	for _, st := range fn.Body.List {
		if st == store {
			return false
		}
		guard, ok := st.(*ast.IfStmt)
		if !ok || len(guard.Body.List) != 1 {
			continue
		}
		call, ok := guard.Body.List[0].(*ast.ExprStmt)
		if !ok {
			continue
		}
		if c, ok := call.X.(*ast.CallExpr); !ok || !isIdent(c.Fun, "panic") {
			continue
		}
		uses := false
		ast.Inspect(guard.Cond, func(n ast.Node) bool {
			uses = uses || isIdent(n, name.Name)
			return !uses
		})
		if uses {
			return true
		}
	}
	return false
}

// fromOffset reports whether name is defined once in fn's body, from the
// offset the plan recorded for the field (a selector ending in .offset),
// and never assigned, incremented or taken the address of again, so that
// the offset the bound checks is the one the store writes at.
func fromOffset(fn *ast.FuncDecl, name string) bool {
	defs, sets := 0, 0
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				if !isIdent(lhs, name) {
					continue
				}
				sets++
				if n.Tok == token.DEFINE && len(n.Rhs) == len(n.Lhs) {
					if sel, ok := n.Rhs[i].(*ast.SelectorExpr); ok && sel.Sel.Name == "offset" {
						defs++
					}
				}
			}
		case *ast.IncDecStmt:
			if isIdent(n.X, name) {
				sets++
			}
		case *ast.UnaryExpr:
			if n.Op == token.AND && isIdent(n.X, name) {
				sets++
			}
		}
		return true
	})
	return defs == 1 && sets == 1
}

// isIdent reports whether n is the identifier name.
func isIdent(n ast.Node, name string) bool {
	id, ok := n.(*ast.Ident)
	return ok && id.Name == name
}
