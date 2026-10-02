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

package jsonx

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// The vector files written by adapter/testdata/python/gen_float_vectors.py
// and gen_state_vectors.py, as committed.
const (
	floatVectorsPath = "../../testdata/python/float_vectors.tsv"
	stateVectorsPath = "../../testdata/python/state_vectors.tsv"
)

// The Python reference every vector was made with. A file whose header names
// another version fails the tests that read it.
const (
	wantPython       = "3.14.3"
	wantPydantic     = "2.13.4"
	wantPydanticCore = "2.46.4"
)

// readVectors returns the data lines of a vector file after checking its
// header: the first line must be wantTitle, the python line must name
// CPython wantPython (the rest of that line is the interpreter's build, which
// does not change a row), the pydantic and pydantic_core lines must name the
// reference versions, and the count line must equal the number of data
// lines.
func readVectors(t *testing.T, path, wantTitle string) []string {
	t.Helper()
	title, header, lines, err := splitVectorFile(path)
	if err != nil {
		t.Fatalf("read the vector file: %v", err)
	}
	if title != wantTitle {
		t.Fatalf("%s: first line %q, want %q", path, title, wantTitle)
	}
	if version, _, _ := strings.Cut(header["python"], " "); version != wantPython {
		t.Fatalf("%s: header python = %q, want CPython %s", path, header["python"], wantPython)
	}
	if got := header["pydantic"]; got != wantPydantic {
		t.Fatalf("%s: header pydantic = %q, want %q", path, got, wantPydantic)
	}
	if got := header["pydantic_core"]; got != wantPydanticCore {
		t.Fatalf("%s: header pydantic_core = %q, want %q", path, got, wantPydanticCore)
	}
	if got := header["count"]; got != strconv.Itoa(len(lines)) {
		t.Fatalf("%s: header count = %q, the file has %d data lines", path, got, len(lines))
	}
	return lines
}

// splitVectorFile reads a vector file into its title (the first line, a
// comment), the "# key: value" lines of its header and its data lines.
func splitVectorFile(path string) (title string, header map[string]string, lines []string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, nil, err
	}
	header = map[string]string{}
	for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		rest, isHeader := strings.CutPrefix(line, "# ")
		switch {
		case isHeader && i == 0:
			title = rest
		case isHeader:
			key, value, _ := strings.Cut(rest, ": ")
			header[key] = value
		default:
			lines = append(lines, line)
		}
	}
	return title, header, lines, nil
}
