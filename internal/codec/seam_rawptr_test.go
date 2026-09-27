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
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// rootStoreFile is the one non-test file of the root package that may import
// unsafe: the typed decode's store, which writes a decoded answer into T's
// field at the offset reflect gave it. Every other file of the root package,
// and of each package the root package imports, writes through no raw pointer
// by any route.
const rootStoreFile = "decodeas_store.go"

// rootCodeDirs are the directories of the root package's own code: the
// package itself and internal/engine, which holds the stages of a call. Every
// rule the seam tests hold the root package to holds internal/engine item for
// item, except that no file of internal/engine may import unsafe:
// rootStoreFile stays in the root package.
var rootCodeDirs = []string{".", "internal/engine"}

// sourceExts are the extensions of the files, other than .go files, that the
// go command compiles into a package (go/build's Package: CgoFiles and
// SFiles, and the C, C++, Objective-C, Fortran, SWIG and syso files). A
// function declared without a body in a Go file and defined in one of them
// writes through whatever pointer it is handed, with no unsafe import and no
// selector of rawPointerUses.
var sourceExts = []string{".s", ".S", ".sx", ".c", ".cc", ".cpp", ".cxx", ".m", ".h", ".hh", ".hpp", ".hxx", ".f", ".F", ".for", ".f90", ".swig", ".swigcxx", ".syso"}

// isSource reports whether the go command compiles a file named name, a Go
// file or one of sourceExts.
func isSource(name string) bool {
	return strings.HasSuffix(name, ".go") || slices.Contains(sourceExts, path.Ext(name))
}

// rawPointerSelectors are the selectors through which a file reaches a raw
// pointer without importing unsafe itself, or with it: reflect.Value's
// UnsafePointer and UnsafeAddr, reflect.NewAt, and unsafe's SliceData and
// StringData (strings and slices have no others; unsafe's own Pointer, Add,
// Slice and String need the import, which TestSeamImports confines).
var rawPointerSelectors = []string{"UnsafePointer", "UnsafeAddr", "NewAt", "SliceData", "StringData"}

// rawPointerUses returns every use in f of a raw-pointer route, as
// "line: what": a selector of rawPointerSelectors, any selector on the
// unsafe package (by whatever name the file imports it), a conversion of a
// Pointer() result (reflect.Value.Pointer's uintptr) to a pointer type or to
// unsafe.Pointer, and a function declared without a body, whose body is
// assembly or another package's (go:linkname) and so out of every Go-level
// check. A Pointer() result compared or printed is not a route and passes
// (internal/h2gate compares two to recognise a function).
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
		case *ast.FuncDecl:
			if n.Body == nil {
				add(n, "func "+n.Name.Name+" declared without a body")
			}
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

// TestSeamCodecUnsafeIsNoCopyString bounds internal/codec's use of unsafe to
// NoCopyString. The raw-pointer rule of TestSeamRootRawPointers exempts
// internal/codec, and TestSeamOneUnsafeFile counts the package's unsafe
// importers, one, nocopy.go, but not what the package's files do: a raw-write
// helper added to nocopy.go, or to another codec file through reflect with no
// unsafe import at all (reflect.NewAt over a Value's UnsafePointer), and
// called from the root package would write through a raw pointer outside
// decodeas_store.go and pass both. So nocopy.go must declare exactly one
// thing, the function NoCopyString, and reach a raw pointer only through
// unsafe.String and unsafe.SliceData; and every other non-test file of the
// package, whatever its build constraints or GOARCH suffix, must import
// unsafe under no name and use no raw-pointer route of rawPointerUses.
//
// The exemption is internal/codec itself, not what lies below it: the
// directory holds Go files only, since an assembly or C file would give a
// body-less Go function a body that writes through any pointer, and no
// sub-directory but testdata, since a package below internal/codec is not
// internal/codec; testdata holds no file the go command compiles.
//
// Mutation checks: internal/codec/rawsub importing unsafe, the same
// sub-package writing through reflect.NewAt with no unsafe import, and a
// body-less RawStore with its body in rawasm_arm64.s and rawasm_amd64.s, each
// referenced from the root package, fail it.
func TestSeamCodecUnsafeIsNoCopyString(t *testing.T) {
	mod := findModule(t)
	codecDir := filepath.Join(mod.root, "internal", "codec")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(codecDir, "nocopy.go"), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var decls []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil {
				decls = append(decls, "method "+d.Name.Name)
			} else {
				decls = append(decls, "func "+d.Name.Name)
			}
		case *ast.GenDecl:
			if d.Tok != token.IMPORT {
				decls = append(decls, fmt.Sprintf("%s at line %d", d.Tok, fset.Position(d.Pos()).Line))
			}
		}
	}
	if want := []string{"func NoCopyString"}; !slices.Equal(decls, want) {
		t.Errorf("internal/codec/nocopy.go declares %q, want only %q", decls, want)
	}
	var routes []string
	for _, use := range rawPointerUses(fset, f) {
		_, what, _ := strings.Cut(use, ": ")
		routes = append(routes, what)
	}
	slices.Sort(routes)
	routes = slices.Compact(routes)
	if want := []string{"selector SliceData", "unsafe.String"}; !slices.Equal(routes, want) {
		t.Errorf("internal/codec/nocopy.go reaches raw pointers through %q, want only %q (unsafe.String over unsafe.SliceData)", routes, want)
	}

	// Every other non-test file, read from disk so that a file this build
	// leaves out (validate_amd64.go on arm64, and the reverse) is read too.
	entries, err := os.ReadDir(codecDir)
	if err != nil {
		t.Fatal(err)
	}
	var checked []string
	for _, e := range entries {
		name := e.Name()
		switch {
		case e.IsDir() && name == "testdata":
			for _, src := range sourcesUnder(t, filepath.Join(codecDir, name)) {
				t.Errorf("internal/codec/testdata/%s: a file the go command compiles; testdata holds data only", src)
			}
			continue
		case e.IsDir():
			t.Errorf("internal/codec/%s/: a directory below internal/codec, which only testdata may be: a package there is not internal/codec and gets none of its exemptions", name)
			continue
		case !strings.HasSuffix(name, ".go"):
			t.Errorf("internal/codec/%s: not a Go file; internal/codec holds Go files only (an assembly or C body writes through raw pointers that no Go-level check sees)", name)
			continue
		case strings.HasSuffix(name, "_test.go") || name == "nocopy.go":
			continue
		}
		af, err := parser.ParseFile(fset, filepath.Join(codecDir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		checked = append(checked, name)
		for _, spec := range af.Imports {
			if p, err := strconv.Unquote(spec.Path.Value); err == nil && p == "unsafe" {
				t.Errorf("internal/codec/%s:%d: imports unsafe; only nocopy.go may (NoCopyString)", name, fset.Position(spec.Pos()).Line)
			}
		}
		for _, use := range rawPointerUses(fset, af) {
			t.Errorf("internal/codec/%s:%s (only nocopy.go's NoCopyString reaches a raw pointer in internal/codec)", name, use)
		}
	}
	for _, want := range []string{"decode.go", "encode.go", "validate_amd64.go", "validate_arm64.go"} {
		if !slices.Contains(checked, want) {
			t.Fatalf("read %d non-test files of internal/codec %q and not %s; the check would read too little", len(checked), checked, want)
		}
	}
	t.Logf("nocopy.go and %d other non-test files of internal/codec read", len(checked))
}

// sourcesUnder returns the files below dir, as slash-separated paths from
// dir, that the go command would compile in a package: Go files and those
// of sourceExts.
func sourcesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && isSource(d.Name()) {
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return out
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
		"error: a function declared without a body":     {src: `func f(p any, off uintptr, v float64)`, want: 1},
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

// TestSeamRootRawPointers checks the root package's raw-pointer rule over the
// NON-TEST files of every build configuration, for each directory of
// rootCodeDirs, the root package and internal/engine, item for item.
//
// The rule: of the root package's files exactly one imports unsafe,
// rootStoreFile, and of internal/engine's none; and no other file uses a
// raw-pointer route of rawPointerUses, whether it is a file of either
// directory, of a package of this module that either imports directly or
// through others, or of any other package that go list ./... lists.
//
// The exemptions: internal/codec and internal/testsupport/naive, exactly,
// which hold the module's other unsafe uses; a package below either is not
// exempt. Test files are out of scope: errors_test.go compares two maps'
// identities with UnsafePointer, which writes nothing. Packages outside the
// module are not read.
//
// The guards: each package the rule covers holds no file other than Go files
// that the go command compiles (sourceExts: an assembly body, say); and the
// walk reads a file of every package it reaches, so a package the walk cannot
// read (under testdata, or a directory starting with "_") fails instead of
// passing unread.
//
// Mutation check: reflect.ValueOf(t).UnsafePointer() added to decodeas.go, an
// import of unsafe in any root file but rootStoreFile, or the store moved to
// another file, fails it; so do a sub-package internal/codec/rawsub imported
// by the root package that imports unsafe or writes through reflect.NewAt, a
// root file declared without a body with its body in a .s file, and an
// internal/engine file that imports unsafe or calls UnsafePointer; and a
// package outside internal/codec that only internal/codec imports, writing
// through reflect.NewAt over a field's UnsafePointer.
func TestSeamRootRawPointers(t *testing.T) {
	mod := findModule(t)
	files := moduleFiles(t, mod.root)

	importers := map[string][]string{}
	for _, f := range files {
		if !f.test && slices.Contains(rootCodeDirs, f.dir) && slices.Contains(f.imports, "unsafe") {
			importers[f.dir] = append(importers[f.dir], f.rel)
		}
	}
	for _, dir := range rootCodeDirs {
		var want []string
		if dir == "." {
			want = []string{rootStoreFile}
		}
		if !slices.Equal(importers[dir], want) {
			t.Errorf("non-test files of %s importing unsafe = %q, want exactly %q (R116, STANDING 3)", dir, importers[dir], want)
		}
	}

	// The module's packages the root code's non-test files import, directly
	// or through each other.
	dirs := map[string]bool{}
	queue := slices.Clone(rootCodeDirs)
	for _, dir := range rootCodeDirs {
		dirs[dir] = true
	}
	for ; len(queue) > 0; queue = queue[1:] {
		for _, f := range files {
			if f.dir != queue[0] || f.test {
				continue
			}
			for _, p := range f.imports {
				rest, ok := strings.CutPrefix(p, modulePath+"/")
				if !ok || rest == "internal/codec" || rest == naiveDir || dirs[rest] {
					continue
				}
				dirs[rest] = true
				queue = append(queue, rest)
			}
		}
	}
	for _, want := range []string{".", "internal/engine", "internal/wire", "internal/h2gate"} {
		if !dirs[want] {
			t.Fatalf("the root package's imports %v miss %q; the check would read too little", slices.Sorted(maps.Keys(dirs)), want)
		}
	}
	// And every other package of the module the go command lists, whoever
	// imports it: a package that only internal/codec imports is not reached
	// from the root code, since the walk stops at internal/codec, and would
	// otherwise write through a raw pointer unread. A listed package whose
	// files are all tests is out of scope, as every test file is.
	walked := maps.Clone(dirs)
	for line := range strings.Lines(goList(t, mod.root, "-e", "-f", "{{.ImportPath}}", "./...")) {
		p := strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(p, modulePath+"/")
		switch {
		case p == modulePath:
			rest = "."
		case !ok:
			t.Fatalf("go list ./... printed %q, which is not a package of %s", p, modulePath)
		}
		if rest != "internal/codec" && rest != naiveDir {
			dirs[rest] = true
		}
	}
	for _, want := range []string{"internal/testsupport", "examples/quickstart"} {
		if !dirs[want] {
			t.Fatalf("go list ./... misses %q; the check would read too little", want)
		}
	}

	fset := token.NewFileSet()
	checked := map[string]int{}
	for _, f := range files {
		if f.test || !dirs[f.dir] || f.rel == rootStoreFile {
			continue
		}
		af, err := parser.ParseFile(fset, filepath.Join(mod.root, filepath.FromSlash(f.rel)), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		checked[f.dir]++
		for _, use := range rawPointerUses(fset, af) {
			t.Errorf("%s:%s (K40, R116: only %s writes through a raw pointer)", f.rel, use, path.Join(".", rootStoreFile))
		}
	}
	for _, dir := range slices.Sorted(maps.Keys(dirs)) {
		if checked[dir] == 0 && walked[dir] {
			t.Errorf("%s: the root code imports it, and the walk read none of its non-test files (a package under testdata or a directory starting with \"_\" is not walked)", dir)
		}
		entries, err := os.ReadDir(filepath.Join(mod.root, filepath.FromSlash(dir)))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if !e.IsDir() && isSource(e.Name()) && !strings.HasSuffix(e.Name(), ".go") {
				t.Errorf("%s: a %s file the go command compiles into the package, whose functions write through raw pointers that no Go-level check sees (review V77 NIT 2)", path.Join(dir, e.Name()), path.Ext(e.Name()))
			}
		}
	}
	if checked["."] == 0 || !slices.ContainsFunc(files, func(f goFile) bool { return f.rel == "decodeas.go" }) {
		t.Fatalf("checked %d root files and found no decodeas.go; the check would pass vacuously", checked["."])
	}
	read := 0
	for _, n := range checked {
		read += n
	}
	t.Logf("read %d non-test files of %d packages (%d reached from %q)", read, len(dirs), len(walked), rootCodeDirs)
}
