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

package testsupport

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fixtureCache holds every fixture read so far, keyed by its slash-separated
// name. The values are strings, so nothing a caller does can change them.
var fixtureCache sync.Map // map[string]string

// moduleRoot finds the directory holding go.mod once per process.
var moduleRoot = sync.OnceValues(findModuleRoot)

// findModuleRoot walks up from the working directory (the package directory
// under go test) to the first directory that holds a go.mod file.
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("testsupport: working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("testsupport: no go.mod above the working directory")
		}
		dir = parent
	}
}

// testdataDir returns the module's testdata directory.
func testdataDir() (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "testdata"), nil
}

// readFixture returns the content of testdata/<name>, reading the file on
// the first call for that name only.
func readFixture(name string) (string, error) {
	if !fs.ValidPath(name) || name == "." {
		return "", fmt.Errorf("testsupport: fixture name %q is not a slash-separated path inside testdata", name)
	}
	if v, ok := fixtureCache.Load(name); ok {
		return v.(string), nil
	}
	dir, err := testdataDir()
	if err != nil {
		return "", err
	}
	data, err := fs.ReadFile(os.DirFS(dir), name)
	if err != nil {
		return "", fmt.Errorf("testsupport: fixture %q: %w", name, err)
	}
	v, _ := fixtureCache.LoadOrStore(name, string(data))
	return v.(string), nil
}

// FixtureDir returns the absolute path of the module's testdata directory,
// found by walking up from the working directory to go.mod.
func FixtureDir(tb testing.TB) string {
	tb.Helper()
	dir, err := testdataDir()
	if err != nil {
		tb.Fatal(err)
	}
	return dir
}

// FixtureString returns the content of testdata/<name> (a slash-separated
// path such as "result.json"). The file is read once per process; later calls
// return the same cached string without copying.
func FixtureString(tb testing.TB, name string) string {
	tb.Helper()
	s, err := readFixture(name)
	if err != nil {
		tb.Fatal(err)
	}
	return s
}

// Fixture returns a fresh copy of the bytes of testdata/<name>. The cache
// behind it is never handed out, so a caller may modify the slice. An
// allocation test that must not pay for the copy inside its measured section
// calls Fixture before the section or uses [FixtureString].
func Fixture(tb testing.TB, name string) []byte {
	tb.Helper()
	return []byte(FixtureString(tb, name))
}

// FixtureNames returns the sorted names of the files directly in testdata
// that match pattern ([path.Match] syntax), such as "malformed-*.json". It
// fails the test when nothing matches, so a renamed fixture set cannot turn a
// loop over it into a test that checks nothing.
func FixtureNames(tb testing.TB, pattern string) []string {
	tb.Helper()
	names, err := globFixtures(pattern)
	if err != nil {
		tb.Fatal(err)
	}
	return names
}

// globFixtures lists the fixtures matching pattern, in lexical order.
func globFixtures(pattern string) ([]string, error) {
	dir, err := testdataDir()
	if err != nil {
		return nil, err
	}
	names, err := fs.Glob(os.DirFS(dir), pattern)
	if err != nil {
		return nil, fmt.Errorf("testsupport: fixture pattern %q: %w", pattern, err)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("testsupport: no fixture matches %q in %s", pattern, dir)
	}
	return names, nil
}
