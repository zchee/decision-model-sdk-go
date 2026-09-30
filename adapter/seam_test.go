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

package adapter

import (
	"bytes"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	// modulePath is this module's path.
	modulePath = "github.com/zchee/typesafe-sdk-go/adapter"
	// sdkPath is the TypeSafe SDK's module path and its root package.
	sdkPath = "github.com/zchee/typesafe-sdk-go"
	// jsonLibPath is the JSON library the module uses (go-json-experiment).
	jsonLibPath = "github.com/go-json-experiment/json"
	// cmpPath is the one module test files may add.
	cmpPath = "github.com/google/go-cmp/cmp"
)

// allowedImports lists, for each package of this module (by its path
// relative to the module, "." for the root package), what its non-test
// files may import directly besides the standard library: packages of this
// module (relative paths) and the SDK's root package. A package missing from
// the map fails TestImportPolicy, so a new package is added here on purpose,
// with the edges the import graph allows it.
var allowedImports = map[string][]string{
	".":                 {"llm", "openai", "anthropic", "gemini", "internal/jsonx", "internal/schema", "internal/prob", sdkPath},
	"openai":            {"llm", "internal/rest", "internal/jsonx"},
	"anthropic":         {"llm", "internal/rest", "internal/jsonx"},
	"gemini":            {"llm", "internal/rest", "internal/jsonx"},
	"internal/rest":     {"llm", "internal/jsonx"},
	"internal/schema":   {"internal/jsonx"},
	"internal/cassette": {"internal/jsonx"},
	"internal/fake":     {"llm"},
	"llm":               {},
	"internal/prob":     {},
	"internal/jsonx":    {jsonLibPath, jsonLibPath + "/jsontext"},
	"livetest":          {".", "llm", "openai", "anthropic", "gemini", "internal/cassette", sdkPath},
	"examples/compare":  {".", "llm", "openai", "anthropic", "gemini", "internal/cassette", sdkPath},
}

// forbiddenStdlib are the standard library's JSON packages, which no file of
// the module imports: the JSON library, confined to internal/jsonx, is the
// module's one JSON implementation.
var forbiddenStdlib = []string{"encoding/json", "encoding/json/v2", "encoding/json/jsontext"}

// listedPackage is one package of this module as go list reports it.
type listedPackage struct {
	rel     string // path relative to the module, "." for the root package
	imports []string
	tests   []string // TestImports and XTestImports
}

// TestImportPolicy holds the module to its import graph, allowedImports,
// over the direct imports of every package, its non-test files and its test
// files, in the default build configuration and with the live tag. It
// refuses an import under the SDK's internal/ directory (the go command would
// allow it, since this module's path lies under the SDK's), the standard
// library's JSON packages anywhere, the JSON library outside internal/jsonx,
// and any module other than the SDK, the JSON library and, in test files,
// go-cmp. Imports of the standard library are otherwise free; only direct
// imports are read, because the SDK and the JSON library themselves import
// the standard library's JSON packages.
//
// allowedImports binds non-test files exactly. Test files are read more
// widely: they may import any package of this module and the SDK's root
// package, because the providers' external test packages (openai_test and
// the others) go through the root package and the SDK, and
// internal/cassette and internal/fake exist to be imported by other
// packages' tests; the go command itself refuses an import cycle. Every
// other rule above holds for test files as for non-test files.
func TestImportPolicy(t *testing.T) {
	for _, tags := range []string{"", "live"} {
		pkgs := listModulePackages(t, tags)
		if !slices.ContainsFunc(pkgs, func(p listedPackage) bool { return p.rel == "." }) {
			t.Fatalf("go list -tags=%q ./... did not report the root package %s; the check would pass vacuously", tags, modulePath)
		}
		for _, p := range pkgs {
			allowed, known := allowedImports[p.rel]
			if !known {
				t.Errorf("tags %q: package %s is not in allowedImports; add it with the edges the import graph allows", tags, p.rel)
				continue
			}
			for _, imp := range p.imports {
				if msg := importViolation(p.rel, imp, allowed, false); msg != "" {
					t.Errorf("tags %q: non-test file of %s imports %s: %s", tags, p.rel, imp, msg)
				}
			}
			for _, imp := range p.tests {
				if msg := importViolation(p.rel, imp, allowed, true); msg != "" {
					t.Errorf("tags %q: test file of %s imports %s: %s", tags, p.rel, imp, msg)
				}
			}
		}
	}
}

// importViolation reports why package rel may not import imp, or "" when it
// may. allowed is rel's entry of allowedImports; test says whether the
// import is a test file's, which may also import any package of this module
// (the go command refuses a cycle) and go-cmp.
func importViolation(rel, imp string, allowed []string, test bool) string {
	switch {
	case slices.Contains(forbiddenStdlib, imp):
		return "the standard library's JSON packages are not used; internal/jsonx wraps the JSON library"
	case imp == sdkPath+"/internal" || strings.HasPrefix(imp, sdkPath+"/internal/"):
		return "the SDK's internal packages are not part of its API"
	case imp == jsonLibPath || strings.HasPrefix(imp, jsonLibPath+"/"):
		if rel == "internal/jsonx" && slices.Contains(allowed, imp) {
			return ""
		}
		return "only internal/jsonx imports the JSON library, and only its json and jsontext packages"
	case isStdlib(imp):
		return ""
	case imp == modulePath || strings.HasPrefix(imp, modulePath+"/"):
		dep := strings.TrimPrefix(strings.TrimPrefix(imp, modulePath), "/")
		if dep == "" {
			dep = "."
		}
		if test || slices.Contains(allowed, dep) {
			return ""
		}
		return "not an edge of the import graph; allowed: [" + strings.Join(allowed, ", ") + "]"
	case imp == sdkPath:
		if test || slices.Contains(allowed, sdkPath) {
			return ""
		}
		return "only the root package, livetest and examples/compare import the SDK"
	case test && (imp == cmpPath || strings.HasPrefix(imp, cmpPath+"/")):
		return ""
	default:
		return "a module the adapter does not require"
	}
}

// isStdlib reports whether path names a standard library package: its first
// element holds no dot.
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// listModulePackages runs go list over every package of the module with the
// build tags given, from the module's root directory, which is this
// package's.
func listModulePackages(t *testing.T, tags string) []listedPackage {
	t.Helper()
	const format = `{{.ImportPath}}{{"\t"}}{{join .Imports " "}}{{"\t"}}{{join .TestImports " "}} {{join .XTestImports " "}}`
	//nolint:gosec // G204: the go command with the test's own fixed arguments.
	cmd := exec.CommandContext(t.Context(), goTool(t), "list", "-tags="+tags, "-f", format, "./...")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -tags=%q ./...: %v\n%s", tags, err, stderr.String())
	}
	var pkgs []listedPackage
	for line := range strings.Lines(string(out)) {
		fields := strings.Split(strings.TrimRight(line, "\r\n"), "\t")
		if len(fields) != 3 {
			t.Fatalf("go list printed %q; want three tab-separated fields", line)
		}
		rel, ok := strings.CutPrefix(fields[0], modulePath)
		if !ok || (rel != "" && !strings.HasPrefix(rel, "/")) {
			t.Fatalf("go list ./... reported %s, a package outside %s", fields[0], modulePath)
		}
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			rel = "."
		}
		pkgs = append(pkgs, listedPackage{rel: rel, imports: strings.Fields(fields[1]), tests: strings.Fields(fields[2])})
	}
	return pkgs
}

// goTool returns the go command that runs this test. go test puts the bin
// directory of its own toolchain first on PATH for the test binary and does
// not set GOROOT, so the go command found on PATH is the one running the
// test; GOROOT is not read, so a GOROOT set by the caller cannot select
// another toolchain.
func goTool(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go command is needed to list the module's packages: %v", err)
	}
	return p
}
