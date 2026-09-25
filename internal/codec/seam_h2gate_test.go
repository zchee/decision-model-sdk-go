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

// The internal/h2gate rows of the seam tests (W2.2), in their own file so the
// wave does not edit seam_test.go, which W2.0 owns. TestSeamImports already
// forbids root and internal/codec imports in internal/h2gate, and
// TestSeamTransitiveImports keeps them out of its dependencies; these rows add
// that the package itself is standard-library only (it runs on the gotip
// canary, K5), and that its tests add only internal/testsupport and go-cmp.
//
// Mutation checks: each change below, planted in a copy of the tree, makes
// the named test fail.
//   - TestSeamH2GateImports: a non-test h2gate file importing
//     internal/testsupport, golang.org/x/net/http2 or go-cmp; a test file
//     importing golang.org/x/net/http2 or sonic.
//   - TestSeamH2GateTransitiveImports: a non-test h2gate file importing any
//     module package (internal/wire, say): the listing then shows a
//     non-standard dependency.

import (
	"slices"
	"strings"
	"testing"
)

const (
	h2gatePath      = modulePath + "/internal/h2gate"
	testsupportPath = modulePath + "/internal/testsupport"
	goCmpPath       = "github.com/google/go-cmp"
)

// TestSeamH2GateImports checks the direct imports of every internal/h2gate
// file, whatever its build constraints: the standard library only, and for
// test files also internal/testsupport (through which x/net's framer serves
// the loopback server) and go-cmp.
func TestSeamH2GateImports(t *testing.T) {
	mod := findModule(t)
	var prod, tests []goFile
	for _, f := range moduleFiles(t, mod.root) {
		if !under(f.dir, "internal/h2gate") {
			continue
		}
		if f.test {
			tests = append(tests, f)
		} else {
			prod = append(prod, f)
		}
	}
	// A walk that found nothing would pass every rule below.
	if len(prod) == 0 {
		t.Fatal("the module walk found no internal/h2gate file; the rules would pass vacuously")
	}
	if !slices.ContainsFunc(tests, func(f goFile) bool { return slices.Contains(f.imports, testsupportPath) }) {
		t.Fatal("no internal/h2gate test imports internal/testsupport; the test rule would pass vacuously")
	}
	t.Run("non-test files import the standard library only", func(t *testing.T) {
		for _, f := range prod {
			for _, p := range f.imports {
				if !isStandard(p) {
					t.Errorf("%s imports %q", f.rel, p)
				}
			}
		}
	})
	t.Run("test files add only internal/testsupport and go-cmp", func(t *testing.T) {
		for _, f := range tests {
			for _, p := range f.imports {
				if !isStandard(p) && p != testsupportPath && !under(p, goCmpPath) {
					t.Errorf("%s imports %q", f.rel, p)
				}
			}
		}
	})
}

// TestSeamH2GateTransitiveImports checks that internal/h2gate without its
// tests depends, through any chain, on the standard library alone, in the
// build configuration of the host.
func TestSeamH2GateTransitiveImports(t *testing.T) {
	mod := findModule(t)
	out := goList(t, mod.root, "-deps", "-f", "{{.ImportPath}}", "./internal/h2gate")
	n, sawSelf := 0, false
	for line := range strings.Lines(out) {
		dep := strings.TrimSpace(line)
		if dep == "" {
			continue
		}
		n++
		if dep == h2gatePath {
			sawSelf = true
			continue
		}
		if !isStandard(dep) {
			t.Errorf("internal/h2gate depends on %s", dep)
		}
	}
	if n == 0 || !sawSelf {
		t.Fatalf("go list printed %d dependencies without internal/h2gate itself:\n%s", n, out)
	}
}
