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

// The seam tests hold the package boundaries of the port plan (section 4,
// NF6, D1, PM1, PM5) over the whole module. CI runs them on their own with
// go test -run Seam ./internal/codec/, and with every other test.

import (
	"bytes"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	modulePath = "github.com/zchee/typesafe-sdk-go"
	codecPath  = modulePath + "/internal/codec"
	sonicPath  = "github.com/bytedance/sonic"

	// d1Cutoff is the first Go release on which the SDK refuses to compile,
	// because sonic's JIT path does not support it yet (D1). The Go 1.28 bump
	// procedure in docs/support.md moves it, together with the constraint
	// line of every file of this package.
	d1Cutoff = "go1.28"
)

var (
	// refusalLine is the build constraint of unsupported.go.
	refusalLine = "//go:build " + d1Cutoff + " || !(amd64 || arm64)"
	// supportedLine is the build constraint of every other file of the
	// package, tests included.
	supportedLine = "//go:build !" + d1Cutoff + " && (amd64 || arm64)"
	// d1Identifier is the undefined identifier that unsupported.go uses; its
	// name is the compile error a consumer sees off the support matrix.
	d1Identifier = "typesafe_sdk_go_requires_go1_17_to_go1_" + strconv.Itoa(goMinor(d1Cutoff)-1) + "_on_amd64_or_arm64"
)

// goMinor returns N for a go1.N release tag, or -1.
func goMinor(tag string) int {
	v, ok := strings.CutPrefix(tag, "go1.")
	if !ok {
		return -1
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return -1
	}
	return n
}

// under reports whether the slash-separated path p is dir or lies below it.
func under(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// isStandard reports whether the import path names a standard-library
// package: its first element has no dot. cgo's "C" is not one.
func isStandard(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return importPath != "C" && !strings.Contains(first, ".")
}

// isJSONLibrary reports whether the import path names a JSON library:
// encoding/json and its v2 successors, go-json-experiment, sonic, and any
// third-party package whose path mentions json.
func isJSONLibrary(importPath string) bool {
	return under(importPath, sonicPath) || strings.Contains(strings.ToLower(importPath), "json")
}

// module describes the main module as its go.mod declares it.
type module struct {
	root      string // absolute directory of go.mod
	goVersion string // the go directive, e.g. "1.27"
}

// findModule walks up from the package directory to the go.mod of the main
// module and checks that it declares modulePath.
func findModule(t *testing.T) module {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			m := module{root: dir}
			var declared string
			for line := range strings.Lines(string(data)) {
				fields := strings.Fields(line)
				if len(fields) == 2 && fields[0] == "module" {
					declared = fields[1]
				}
				if len(fields) == 2 && fields[0] == "go" {
					m.goVersion = fields[1]
				}
			}
			if declared != modulePath {
				t.Fatalf("go.mod in %s declares module %q, want %q", dir, declared, modulePath)
			}
			return m
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the package directory")
		}
		dir = parent
	}
}

// goFile is one Go source file of the module with its imports.
type goFile struct {
	rel     string // slash-separated path from the module root
	dir     string // slash-separated directory from the module root, "." for the root
	test    bool
	imports []string
}

// moduleFiles parses the imports of every Go file of the module, whatever
// its build constraints, skipping what the go command skips: directories
// named testdata or vendor or starting with "." or "_", nested modules, and
// files starting with "." or "_".
func moduleFiles(t *testing.T, root string) []goFile {
	t.Helper()
	var files []goFile
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p == root {
				return nil
			}
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		gf := goFile{rel: rel, dir: path.Dir(rel), test: strings.HasSuffix(name, "_test.go")}
		for _, spec := range f.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			gf.imports = append(gf.imports, importPath)
		}
		files = append(files, gf)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	return files
}

// TestSeamImports checks the direct imports of every file of the module,
// test files and files of every build configuration included.
func TestSeamImports(t *testing.T) {
	mod := findModule(t)
	files := moduleFiles(t, mod.root)

	// A walk that found nothing would pass every rule below.
	sawCodecSonic := slices.ContainsFunc(files, func(f goFile) bool {
		return f.dir == "internal/codec" && slices.ContainsFunc(f.imports, func(p string) bool { return under(p, sonicPath) })
	})
	if !sawCodecSonic {
		t.Fatal("the module walk did not find internal/codec importing sonic; every rule would pass vacuously")
	}

	confined := func(f goFile) bool {
		return !under(f.dir, "internal/codec") && !under(f.dir, "internal/testsupport/naive")
	}
	rules := map[string]struct {
		applies func(f goFile) bool
		forbids func(f goFile, importPath string) bool
	}{
		"sonic only under internal/codec and internal/testsupport/naive (D1, NF6)": {
			applies: confined,
			forbids: func(_ goFile, p string) bool { return under(p, sonicPath) },
		},
		"unsafe only under internal/codec and internal/testsupport/naive (NF6)": {
			applies: confined,
			forbids: func(_ goFile, p string) bool { return p == "unsafe" },
		},
		"golang.org/x/net only under internal/testsupport (D2)": {
			applies: func(f goFile) bool { return !under(f.dir, "internal/testsupport") },
			forbids: func(_ goFile, p string) bool { return under(p, "golang.org/x/net") },
		},
		"the root package imports no JSON library": {
			applies: func(f goFile) bool { return f.dir == "." },
			forbids: func(_ goFile, p string) bool { return isJSONLibrary(p) },
		},
		"internal/h2gate and internal/testsupport import neither root nor internal/codec (PM1)": {
			applies: func(f goFile) bool {
				return under(f.dir, "internal/h2gate") || under(f.dir, "internal/testsupport")
			},
			forbids: func(_ goFile, p string) bool { return p == modulePath || under(p, codecPath) },
		},
		"internal/wire imports the standard library only; its tests may add go-cmp": {
			applies: func(f goFile) bool { return under(f.dir, "internal/wire") },
			forbids: func(f goFile, p string) bool {
				if isStandard(p) {
					return false
				}
				return !f.test || !under(p, "github.com/google/go-cmp")
			},
		},
	}
	for name, rule := range rules {
		t.Run(name, func(t *testing.T) {
			for _, f := range files {
				if !rule.applies(f) {
					continue
				}
				for _, p := range f.imports {
					if rule.forbids(f, p) {
						t.Errorf("%s imports %q", f.rel, p)
					}
				}
			}
		})
	}
}

// goList runs the go command in the module root and returns its standard
// output.
func goList(t *testing.T, root string, args ...string) string {
	t.Helper()
	//nolint:gosec // G204: runs the go command with arguments the seam tests fix.
	cmd := exec.CommandContext(t.Context(), "go", append([]string{"list"}, args...)...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return string(out)
}

// TestSeamTransitiveImports checks that the packages the gotip canary builds
// depend, through any chain and including their tests, on neither the root
// package nor internal/codec, which do not compile on gotip by design (PM1).
// A package that does not exist yet is skipped.
//
// The root package is on the canary list only while it does not import
// internal/codec. Its row fails the day it does (W1.2): remove the root
// package from the "Build, vet and test with gotip" step of
// .github/workflows/gotip.yaml and this row together.
func TestSeamTransitiveImports(t *testing.T) {
	mod := findModule(t)
	tests := map[string]struct {
		dir     string // slash-separated, from the module root
		pattern string // the go list pattern
		gotip   string // what to do when the row fails
	}{
		"root package (until it imports internal/codec, W1.2)": {
			dir:     ".",
			pattern: ".",
			gotip:   "drop the root package from gotip.yaml's canary step and this row",
		},
		"internal/h2gate":      {dir: "internal/h2gate", pattern: "./internal/h2gate/..."},
		"internal/testsupport": {dir: "internal/testsupport", pattern: "./internal/testsupport/..."},
		"internal/wire":        {dir: "internal/wire", pattern: "./internal/wire/..."},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(mod.root, filepath.FromSlash(tt.dir))); err != nil {
				t.Skipf("%s does not exist yet: %v", tt.dir, err)
			}
			out := goList(t, mod.root, "-deps", "-test", "-f", "{{.ImportPath}}", tt.pattern)
			n := 0
			for line := range strings.Lines(out) {
				// Test variants print as "path [path.test]".
				dep, _, _ := strings.Cut(strings.TrimSpace(line), " ")
				if dep == "" {
					continue
				}
				n++
				// The root package lists itself among its own dependencies.
				if (dep == modulePath && tt.dir != ".") || under(dep, codecPath) {
					if tt.gotip != "" {
						t.Errorf("%s depends on %s: %s", tt.pattern, dep, tt.gotip)
					} else {
						t.Errorf("%s depends on %s", tt.pattern, dep)
					}
				}
			}
			if n == 0 {
				t.Fatalf("go list printed no dependencies for %s", tt.pattern)
			}
		})
	}
}

// TestSeamD1IdentifierSites checks that every site naming the D1 identifier
// names the same token (ruling R10): the Go 1.28 bump renames it in several
// files at once, and a site left behind would either print a stale range to
// consumers or grep CI output for an identifier the compiler no longer prints.
func TestSeamD1IdentifierSites(t *testing.T) {
	mod := findModule(t)
	token := regexp.MustCompile(`typesafe_sdk_go_requires\w*`)
	tests := map[string]struct {
		path string   // slash-separated, from the module root
		also []string // further text the site must contain
	}{
		"internal/codec/unsupported.go": {path: "internal/codec/unsupported.go"},
		"ci.yaml AC-Q4 refusal check": {
			path: ".github/workflows/ci.yaml",
			also: []string{"GOFLAGS=-tags=" + d1Cutoff},
		},
		"gotip.yaml D1 probe": {path: ".github/workflows/gotip.yaml"},
		"docs/support.md":     {path: "docs/support.md"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(mod.root, filepath.FromSlash(tt.path)))
			if err != nil {
				t.Fatal(err)
			}
			found := token.FindAllString(string(data), -1)
			if len(found) == 0 {
				t.Fatalf("%s names no D1 identifier, want %s", tt.path, d1Identifier)
			}
			for _, got := range found {
				if got != d1Identifier {
					t.Errorf("%s names %s, want %s (d1Cutoff %s)", tt.path, got, d1Identifier, d1Cutoff)
				}
			}
			for _, want := range tt.also {
				if !strings.Contains(string(data), want) {
					t.Errorf("%s does not contain %q (d1Cutoff %s)", tt.path, want, d1Cutoff)
				}
			}
		})
	}
}

// TestSeamBuildConstraints checks the D1 constraint of every file of this
// package: unsupported.go carries the refusal, every other file the
// complementary line, each as the file's first line followed by a blank
// line, and no file carries a second constraint.
func TestSeamBuildConstraints(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var sawRefusal, sawOther bool
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		want := supportedLine
		if name == "unsupported.go" {
			want, sawRefusal = refusalLine, true
		} else {
			sawOther = true
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		var constraints []string
		for _, line := range lines {
			if constraint.IsGoBuild(line) || constraint.IsPlusBuild(line) {
				constraints = append(constraints, line)
			}
		}
		switch {
		case len(constraints) != 1:
			t.Errorf("%s: %d build constraint lines %q, want exactly %q", name, len(constraints), constraints, want)
		case lines[0] != want:
			t.Errorf("%s: first line %q, want %q", name, lines[0], want)
		case len(lines) < 2 || lines[1] != "":
			t.Errorf("%s: the constraint line is not followed by a blank line", name)
		}
	}
	if !sawRefusal || !sawOther {
		t.Fatalf("found unsupported.go: %t, other files: %t; want both", sawRefusal, sawOther)
	}

	t.Run("unsupported.go holds only the undefined identifier", func(t *testing.T) {
		f, err := parser.ParseFile(token.NewFileSet(), "unsupported.go", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.Imports) != 0 {
			t.Errorf("unsupported.go imports %d packages, want none: off the matrix it must fail on the identifier and nothing else", len(f.Imports))
		}
		if len(f.Decls) != 1 {
			t.Fatalf("unsupported.go has %d declarations, want 1", len(f.Decls))
		}
		decl, ok := f.Decls[0].(*ast.GenDecl)
		if !ok || decl.Tok != token.VAR || len(decl.Specs) != 1 {
			t.Fatalf("unsupported.go's declaration is not a single var spec")
		}
		spec := decl.Specs[0].(*ast.ValueSpec)
		if len(spec.Names) != 1 || spec.Names[0].Name != "_" || len(spec.Values) != 1 {
			t.Fatalf("unsupported.go declares %v, want var _ = %s", spec.Names, d1Identifier)
		}
		if id, ok := spec.Values[0].(*ast.Ident); !ok || id.Name != d1Identifier {
			t.Errorf("unsupported.go assigns %T %v, want the identifier %s", spec.Values[0], spec.Values[0], d1Identifier)
		}
	})

	t.Run("the two constraints are complements", func(t *testing.T) {
		refusal, err := constraint.Parse(refusalLine)
		if err != nil {
			t.Fatal(err)
		}
		supported, err := constraint.Parse(supportedLine)
		if err != nil {
			t.Fatal(err)
		}
		cutoff := goMinor(d1Cutoff)
		for minor := 17; minor <= cutoff+3; minor++ {
			for _, arch := range []string{"amd64", "arm64", "386", "arm", "riscv64", "loong64", "ppc64le", "s390x", "wasm"} {
				tags := func(tag string) bool {
					if n := goMinor(tag); n >= 0 {
						return n <= minor
					}
					return tag == arch
				}
				wantSupported := minor < cutoff && (arch == "amd64" || arch == "arm64")
				if got := supported.Eval(tags); got != wantSupported {
					t.Errorf("go1.%d %s: supported constraint = %t, want %t", minor, arch, got, wantSupported)
				}
				if got := refusal.Eval(tags); got == wantSupported {
					t.Errorf("go1.%d %s: refusal constraint = %t, want %t", minor, arch, got, !wantSupported)
				}
			}
		}
	})
}

// TestSeamSonicJITPath checks that sonic compiles its JIT path, not its
// encoding/json fallback, wherever this package compiles: on the host now,
// and for every GOARCH and Go release from the go directive up to the cutoff
// by comparing the two build constraints (PM1).
func TestSeamSonicJITPath(t *testing.T) {
	mod := findModule(t)
	out := strings.TrimSpace(goList(t, mod.root, "-f", "{{.Dir}}\t{{join .GoFiles \" \"}}", sonicPath))
	dir, list, ok := strings.Cut(out, "\t")
	if !ok {
		t.Fatalf("unexpected go list output %q", out)
	}
	files := strings.Fields(list)
	if !slices.Contains(files, "sonic.go") || slices.Contains(files, "compat.go") {
		t.Fatalf("sonic compiles %v on this host; want sonic.go (the JIT path) and not compat.go", files)
	}

	data, err := os.ReadFile(filepath.Join(dir, "sonic.go"))
	if err != nil {
		t.Fatal(err)
	}
	var jitLine string
	for line := range strings.Lines(string(data)) {
		if line = strings.TrimRight(line, "\r\n"); constraint.IsGoBuild(line) {
			jitLine = line
			break
		}
	}
	jit, err := constraint.Parse(jitLine)
	if err != nil {
		t.Fatalf("sonic.go build line %q: %v", jitLine, err)
	}
	supported, err := constraint.Parse(supportedLine)
	if err != nil {
		t.Fatal(err)
	}
	from := goMinor("go" + mod.goVersion)
	if from < 0 {
		// A go directive with a patch version, e.g. 1.27.1.
		major, _, _ := strings.Cut(strings.TrimPrefix(mod.goVersion, "1."), ".")
		from, _ = strconv.Atoi(major)
	}
	for minor := from; minor <= goMinor(d1Cutoff)+3; minor++ {
		for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "wasm"} {
			tags := func(tag string) bool {
				if n := goMinor(tag); n >= 0 {
					return n <= minor
				}
				return tag == arch
			}
			if supported.Eval(tags) && !jit.Eval(tags) {
				t.Errorf("go1.%d %s: internal/codec compiles but sonic %q would take its fallback", minor, arch, jitLine)
			}
		}
	}
}
