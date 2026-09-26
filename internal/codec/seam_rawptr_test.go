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
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// rootStoreFile is the one non-test file of the root package that may
// import unsafe (owner ruling R116): the typed decode's store, which writes
// a decoded answer into T's field at the offset reflect gave it. Every other
// file of the root package, and of each package the root package imports,
// writes through no raw pointer by any route (K40).
const rootStoreFile = "decodeas_store.go"

// rawPointerSelectors are the selectors through which a file reaches a raw
// pointer without importing unsafe itself, or with it: reflect.Value's
// UnsafePointer and UnsafeAddr, reflect.NewAt, and unsafe's SliceData and
// StringData (strings and slices have no others; unsafe's own Pointer, Add,
// Slice and String need the import, which TestSeamImports confines).
var rawPointerSelectors = []string{"UnsafePointer", "UnsafeAddr", "NewAt", "SliceData", "StringData"}

// rawPointerUses returns every use in f of a raw-pointer route, as
// "line: what": a selector of rawPointerSelectors, any selector on the
// unsafe package (by whatever name the file imports it), and a conversion of
// a Pointer() result (reflect.Value.Pointer's uintptr) to a pointer type or
// to unsafe.Pointer. A Pointer() result compared or printed is not a route
// and passes (internal/h2gate compares two to recognise a function).
func rawPointerUses(fset *token.FileSet, f *ast.File) []string {
	unsafeName := ""
	for _, spec := range f.Imports {
		if p, err := strconv.Unquote(spec.Path.Value); err == nil && p == "unsafe" {
			unsafeName = "unsafe"
			if spec.Name != nil {
				unsafeName = spec.Name.Name
			}
		}
	}
	var uses []string
	add := func(n ast.Node, what string) {
		uses = append(uses, fmt.Sprintf("%d: %s", fset.Position(n.Pos()).Line, what))
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if slices.Contains(rawPointerSelectors, n.Sel.Name) {
				add(n, "selector "+n.Sel.Name)
			} else if x, ok := n.X.(*ast.Ident); ok && unsafeName != "" && x.Name == unsafeName {
				add(n, "unsafe."+n.Sel.Name)
			}
		case *ast.CallExpr:
			if len(n.Args) != 1 || !isPointerCall(n.Args[0]) {
				return true
			}
			fun := ast.Unparen(n.Fun)
			if _, ok := fun.(*ast.StarExpr); ok {
				add(n, "a Pointer() result converted to a pointer type")
			} else if sel, ok := fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Pointer" {
				add(n, "a Pointer() result converted to unsafe.Pointer")
			}
		}
		return true
	})
	return uses
}

// isPointerCall reports whether e is a call of a method named Pointer
// without arguments, as reflect.Value.Pointer is called.
func isPointerCall(e ast.Expr) bool {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Pointer"
}

// TestSeamRawPointerDetector checks rawPointerUses on sources that use each
// route, and on sources that only look alike.
func TestSeamRawPointerDetector(t *testing.T) {
	tests := map[string]struct {
		src  string
		want int
	}{
		"error: reflect.Value.UnsafePointer":            {src: `import "reflect"; func f(v reflect.Value) any { return v.UnsafePointer() }`, want: 1},
		"error: reflect.Value.UnsafeAddr":               {src: `import "reflect"; func f(v reflect.Value) uintptr { return v.UnsafeAddr() }`, want: 1},
		"error: reflect.NewAt":                          {src: `import "reflect"; func f(t reflect.Type, p any) { _ = reflect.NewAt }`, want: 1},
		"error: unsafe.SliceData and unsafe.StringData": {src: `import "unsafe"; func f(b []byte, s string) { _, _ = unsafe.SliceData(b), unsafe.StringData(s) }`, want: 2},
		"error: unsafe.Add under another name":          {src: `import u "unsafe"; func f(p u.Pointer) u.Pointer { return u.Add(p, 8) }`, want: 3},
		"error: a Pointer() result to a pointer type":   {src: `import "reflect"; func f(v reflect.Value) *int { return (*int)(v.Pointer()) }`, want: 1},
		"error: a Pointer() result to unsafe.Pointer":   {src: `import ("reflect"; "unsafe"); func f(v reflect.Value) unsafe.Pointer { return unsafe.Pointer(v.Pointer()) }`, want: 3},
		"success: two Pointer() results compared":       {src: `import "reflect"; func f(a, b any) bool { return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer() }`},
		"success: a field named Pointer":                {src: `type s struct{ Pointer int }; func f(x s) int { return x.Pointer }`},
		"success: a conversion of something else":       {src: `func f(x int) int64 { return int64(x) }`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "x.go", "package x; "+tt.src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			if got := rawPointerUses(fset, f); len(got) != tt.want {
				t.Errorf("rawPointerUses = %q, want %d uses", got, tt.want)
			}
		})
	}
}

// TestSeamRootRawPointers checks the root package's raw-pointer rule (K40,
// as the owner amended it in R116), over the NON-TEST files of every build
// configuration: of the root package's files, exactly one imports unsafe,
// rootStoreFile; and no file of the root package but that
// one, nor any file of a package of this module that the root package
// imports directly or through others (internal/codec and
// internal/testsupport/naive excepted, which hold the module's other unsafe
// uses), uses a raw-pointer route of rawPointerUses. Test files are out of
// scope: errors_test.go compares two maps' identities with UnsafePointer,
// which writes nothing. Packages outside the module are not read.
//
// Mutation check: reflect.ValueOf(t).UnsafePointer() added to decodeas.go,
// an import of unsafe in any root file but rootStoreFile, or the store
// moved to another file, fails it.
func TestSeamRootRawPointers(t *testing.T) {
	mod := findModule(t)
	files := moduleFiles(t, mod.root)

	var importers []string
	for _, f := range files {
		if f.dir == "." && !f.test && slices.Contains(f.imports, "unsafe") {
			importers = append(importers, f.rel)
		}
	}
	if want := []string{rootStoreFile}; !slices.Equal(importers, want) {
		t.Errorf("non-test files of the root package importing unsafe = %q, want exactly %q (R116)", importers, want)
	}

	// The module's packages the root package's non-test files import,
	// directly or through each other.
	dirs := map[string]bool{".": true}
	for queue := []string{"."}; len(queue) > 0; queue = queue[1:] {
		for _, f := range files {
			if f.dir != queue[0] || f.test {
				continue
			}
			for _, p := range f.imports {
				rest, ok := strings.CutPrefix(p, modulePath+"/")
				if !ok || under(rest, "internal/codec") || under(rest, "internal/testsupport/naive") || dirs[rest] {
					continue
				}
				dirs[rest] = true
				queue = append(queue, rest)
			}
		}
	}
	for _, want := range []string{".", "internal/wire", "internal/h2gate"} {
		if !dirs[want] {
			t.Fatalf("the root package's imports %v miss %q; the check would read too little", slices.Sorted(maps.Keys(dirs)), want)
		}
	}

	fset := token.NewFileSet()
	checked := 0
	for _, f := range files {
		if f.test || !dirs[f.dir] || f.rel == rootStoreFile {
			continue
		}
		af, err := parser.ParseFile(fset, filepath.Join(mod.root, filepath.FromSlash(f.rel)), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, use := range rawPointerUses(fset, af) {
			t.Errorf("%s:%s (K40, R116: only %s writes through a raw pointer)", f.rel, use, path.Join(".", rootStoreFile))
		}
	}
	if checked == 0 || !slices.ContainsFunc(files, func(f goFile) bool { return f.rel == "decodeas.go" }) {
		t.Fatalf("checked %d files and found no decodeas.go; the check would pass vacuously", checked)
	}
	t.Logf("read %d non-test files of %d packages", checked, len(dirs))
}
