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
	"flag"
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// update rewrites the API goldens under testdata/api from the package as it
// is now; review the diff before committing it:
//
//	go test -run '^(TestPublicAPISurface|TestClientOptionsSurface)$' -update .
//
// CI never rewrites them: under a CI environment the flag fails the test.
var update = flag.Bool("update", false, "rewrite testdata/api/*.txt from the package's exported API")

// apiPackage type-checks the root package from its non-test sources, once per
// test binary, through the "source" importer: the goldens then depend only on
// declarations, so an edited comment or a renamed parameter changes nothing.
var apiPackage = sync.OnceValues(func() (*types.Package, error) {
	path := reflect.TypeFor[Client]().PkgPath()
	return importer.ForCompiler(token.NewFileSet(), "source", nil).Import(path)
})

// TestPublicAPISurface pins every exported identifier of the package with its
// type, value (constants), fields, and method set, one feature per line, in
// the manner of the Go distribution's api/*.txt files (upstream XA1 and XA2:
// test_public_members, test_package_exports), and whether each exported type
// is comparable. Only exported identifiers are listed, and the internal/
// packages cannot be imported from outside this module at all (the go
// command's internal-directory rule), so no private name can leak into the
// surface.
func TestPublicAPISurface(t *testing.T) {
	pkg := loadAPIPackage(t)
	checkGolden(t, "public-api.txt", "exported API of "+pkg.Path(), apiLines(pkg))
}

// TestClientOptionsSurface pins the constructor's options (upstream XA3,
// test_constructor_kwargs): NewClient takes only a variadic list of
// ClientOption, so every setting is optional and named, as upstream's are
// keyword-only with a None default, and the golden lists every function that
// returns a ClientOption with its parameter types.
func TestClientOptionsSurface(t *testing.T) {
	pkg := loadAPIPackage(t)
	scope := pkg.Scope()
	option := scope.Lookup("ClientOption")
	if option == nil {
		t.Fatalf("%s declares no ClientOption", pkg.Path())
	}
	newClient, _ := scope.Lookup("NewClient").(*types.Func)
	if newClient == nil {
		t.Fatalf("%s declares no func NewClient", pkg.Path())
	}
	sig := newClient.Signature()
	if params := sig.Params(); !sig.Variadic() || params.Len() != 1 ||
		!types.Identical(params.At(0).Type().(*types.Slice).Elem(), option.Type()) {
		t.Errorf("NewClient%s: want NewClient(...ClientOption), options only", signature(sig, qualifier(pkg)))
	}

	lines := []string{"func NewClient" + signature(sig, qualifier(pkg))}
	for _, name := range scope.Names() {
		fn, ok := scope.Lookup(name).(*types.Func)
		if !ok || !fn.Exported() {
			continue
		}
		results := fn.Signature().Results()
		returnsOption := results.Len() == 1 && types.Identical(results.At(0).Type(), option.Type())
		switch {
		case returnsOption && !strings.HasPrefix(name, "With"):
			t.Errorf("func %s returns ClientOption: client options are named With*", name)
		case !returnsOption && strings.HasPrefix(name, "With"):
			t.Errorf("func %s%s: a With* function must return exactly one ClientOption", name, signature(fn.Signature(), qualifier(pkg)))
		case returnsOption:
			lines = append(lines, "func "+name+signature(fn.Signature(), qualifier(pkg)))
		}
	}
	checkGolden(t, "client-options.txt", "NewClient and its ClientOption constructors in "+pkg.Path(), lines)
}

// loadAPIPackage returns the type-checked root package or fails the test.
func loadAPIPackage(t *testing.T) *types.Package {
	t.Helper()
	pkg, err := apiPackage()
	if err != nil {
		t.Fatalf("type-check the package from source: %v", err)
	}
	return pkg
}

// checkGolden compares lines, under a header naming what they list, with
// testdata/api/<name>, or rewrites the file under -update.
func checkGolden(t *testing.T, name, what string, lines []string) {
	t.Helper()
	path := filepath.Join("testdata", "api", name)
	got := fmt.Sprintf("# %s: %s.\n# Regenerate: go test -run '^%s$' -update .\n%s\n", name, what, t.Name(), strings.Join(lines, "\n"))
	if *update {
		if os.Getenv("CI") != "" {
			t.Fatalf("-update under CI (CI=%q): the goldens are rewritten locally and reviewed, never by CI", os.Getenv("CI"))
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d lines)", path, len(lines))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the golden (create it with -update): %v", err)
	}
	if diff := gocmp.Diff(strings.Split(string(want), "\n"), strings.Split(got, "\n")); diff != "" {
		t.Errorf("%s differs from the package (-golden +package); if the change is intended, run with -update and commit the file:\n%s", path, diff)
	}
}

// qualifier writes identifiers of pkg unqualified and every other package's
// by its full import path, so that two packages with one name stay apart.
func qualifier(pkg *types.Package) types.Qualifier {
	return func(p *types.Package) string {
		if p == pkg {
			return ""
		}
		return p.Path()
	}
}

// apiLines lists the exported features of pkg in scope order (sorted by
// name): one line per constant, variable and function, and per type its
// declaration, then its exported fields or interface methods, then the
// exported methods of its value and pointer method sets.
func apiLines(pkg *types.Package) []string {
	qual := qualifier(pkg)
	var lines []string
	for _, name := range pkg.Scope().Names() {
		switch obj := pkg.Scope().Lookup(name).(type) {
		case *types.Const:
			if obj.Exported() {
				lines = append(lines, fmt.Sprintf("const %s %s = %s", name, typeString(obj.Type(), qual), obj.Val().ExactString()))
			}
		case *types.Var:
			if obj.Exported() {
				lines = append(lines, fmt.Sprintf("var %s %s", name, typeString(obj.Type(), qual)))
			}
		case *types.Func:
			if obj.Exported() {
				lines = append(lines, "func "+name+signature(obj.Signature(), qual))
			}
		case *types.TypeName:
			if obj.Exported() {
				lines = append(lines, typeLines(obj, qual)...)
			}
		}
	}
	return lines
}

// typeLines lists one exported type: its declaration ending in its
// comparability, its exported fields (with the embedded ones marked) or
// interface methods, and the exported methods callable on a value and,
// marked *T, only on a pointer.
func typeLines(obj *types.TypeName, qual types.Qualifier) []string {
	mark := comparability(obj.Type())
	if alias, ok := obj.Type().(*types.Alias); ok {
		return []string{fmt.Sprintf("type %s%s = %s%s", obj.Name(), typeParams(alias.TypeParams(), qual), typeString(alias.Rhs(), qual), mark)}
	}
	named := obj.Type().(*types.Named)
	head := "type " + obj.Name() + typeParams(named.TypeParams(), qual)
	var lines []string
	switch u := named.Underlying().(type) {
	case *types.Struct:
		lines = append(lines, head+" struct"+mark)
		for field := range u.Fields() {
			switch {
			case field.Embedded():
				lines = append(lines, fmt.Sprintf("%s struct, embedded %s", head, typeString(field.Type(), qual)))
			case field.Exported():
				lines = append(lines, fmt.Sprintf("%s struct, %s %s", head, field.Name(), typeString(field.Type(), qual)))
			}
		}
	case *types.Interface:
		lines = append(lines, head+" interface"+mark)
		for embedded := range u.EmbeddedTypes() {
			lines = append(lines, fmt.Sprintf("%s interface, embedded %s", head, typeString(embedded, qual)))
		}
		unexported := false
		for method := range u.Methods() {
			if !method.Exported() {
				unexported = true
				continue
			}
			lines = append(lines, fmt.Sprintf("%s interface, %s%s", head, method.Name(), signature(method.Signature(), qual)))
		}
		if unexported {
			lines = append(lines, head+" interface, unexported methods")
		}
		return lines
	default:
		lines = append(lines, head+" "+typeString(u, qual)+mark)
	}
	values := types.NewMethodSet(named)
	pointers := types.NewMethodSet(types.NewPointer(named))
	for sel := range pointers.Methods() {
		method := sel.Obj()
		if !method.Exported() {
			continue
		}
		recv := "*" + obj.Name()
		if values.Lookup(method.Pkg(), method.Name()) != nil {
			recv = obj.Name()
		}
		lines = append(lines, fmt.Sprintf("method (%s) %s%s", recv, method.Name(), signature(sel.Type().(*types.Signature), qual)))
	}
	return lines
}

// comparability writes the column that ends a type's declaration line: whether
// its values work with == and as map keys (types.Comparable). A field change
// can flip it while every other line stays the same, as moving RetryPolicy's
// slice and func fields behind a pointer did in W5.3 (review V63).
func comparability(typ types.Type) string {
	if types.Comparable(typ) {
		return ", comparable"
	}
	return ", incomparable"
}

// signature writes sig's type parameters, parameter types and result types
// without parameter names, which are not part of the API.
func signature(sig *types.Signature, qual types.Qualifier) string {
	var b strings.Builder
	b.WriteString(typeParams(sig.TypeParams(), qual))
	b.WriteByte('(')
	params := sig.Params()
	for i := range params.Len() {
		if i > 0 {
			b.WriteString(", ")
		}
		typ := params.At(i).Type()
		if sig.Variadic() && i == params.Len()-1 {
			b.WriteString("...")
			typ = typ.(*types.Slice).Elem()
		}
		b.WriteString(typeString(typ, qual))
	}
	b.WriteByte(')')
	results := sig.Results()
	out := make([]string, results.Len())
	for i := range results.Len() {
		out[i] = typeString(results.At(i).Type(), qual)
	}
	switch len(out) {
	case 0:
	case 1:
		b.WriteString(" " + out[0])
	default:
		b.WriteString(" (" + strings.Join(out, ", ") + ")")
	}
	return b.String()
}

// typeString writes typ as types.TypeString does, except that a function
// type at any depth is written by signature, without parameter names.
func typeString(typ types.Type, qual types.Qualifier) string {
	switch t := typ.(type) {
	case *types.Signature:
		return "func" + signature(t, qual)
	case *types.Pointer:
		return "*" + typeString(t.Elem(), qual)
	case *types.Slice:
		return "[]" + typeString(t.Elem(), qual)
	case *types.Array:
		return fmt.Sprintf("[%d]%s", t.Len(), typeString(t.Elem(), qual))
	case *types.Map:
		return "map[" + typeString(t.Key(), qual) + "]" + typeString(t.Elem(), qual)
	case *types.Named:
		args := t.TypeArgs()
		if args.Len() == 0 {
			return types.TypeString(t, qual)
		}
		name := t.Obj().Name()
		if pkg := t.Obj().Pkg(); pkg != nil {
			if prefix := qual(pkg); prefix != "" {
				name = prefix + "." + name
			}
		}
		parts := make([]string, args.Len())
		for i := range args.Len() {
			parts[i] = typeString(args.At(i), qual)
		}
		return name + "[" + strings.Join(parts, ", ") + "]"
	default:
		return types.TypeString(typ, qual)
	}
}

// typeParams writes a type parameter list with its constraints, or nothing.
func typeParams(list *types.TypeParamList, qual types.Qualifier) string {
	if list.Len() == 0 {
		return ""
	}
	parts := make([]string, list.Len())
	for i := range list.Len() {
		tp := list.At(i)
		parts[i] = tp.Obj().Name() + " " + typeString(tp.Constraint(), qual)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
