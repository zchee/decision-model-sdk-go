// Copyright 2026 The decision-model-sdk-go Authors.
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
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	matrixPath    = "docs/port-test-matrix.md"
	functionsPath = "testdata/upstream/functions.txt"
	itemsPath     = "testdata/upstream/items.txt"
	cassettesDir  = "testdata/cassettes"
	expectedDir   = "testdata/expected"

	// upstreamFunctions and upstreamItems are the counts of upstream's tests
	// at UpstreamCommit: test functions, and the items pytest collects from
	// them. They pin the two lists, so a truncated list fails.
	upstreamFunctions = 71
	upstreamItems     = 424
)

// matrixStatuses are the values a Status cell may hold.
var matrixStatuses = map[string]bool{"planned": true, "ported": true, "deviation": true}

var (
	functionRowID = regexp.MustCompile(`^[A-Z]{2}[0-9]+$`)
	cassetteRowID = regexp.MustCompile(`^C[0-9]+$`)
	upstreamCell  = regexp.MustCompile("^`(tests/[A-Za-z0-9_/]+\\.py::test_[A-Za-z0-9_]+)`$")
	cassetteCell  = regexp.MustCompile("^`([^`/]+\\.json)`$")
	goTestName    = regexp.MustCompile("`(adapter(?:/[a-z0-9_]+)*)\\.(Test[A-Za-z0-9_]*)`")
)

// TestPortTestMatrix checks docs/port-test-matrix.md against the lists of
// upstream's tests: one function row per upstream test function, each row's
// Items cell equal to the items pytest collected from that function, their
// sum equal to the collected total, every Status cell a known status, every
// test a ported row names listed by go test -list, and every cassette and
// expected-response row naming a file of testdata.
func TestPortTestMatrix(t *testing.T) {
	functions := readFunctions(t)
	items := readItems(t)
	functionRows, cassetteRows := readMatrix(t)

	if len(functions) != upstreamFunctions {
		t.Errorf("%s lists %d test functions, want %d", functionsPath, len(functions), upstreamFunctions)
	}
	total := 0
	for _, n := range items {
		total += n
	}
	if total != upstreamItems {
		t.Errorf("%s lists %d collected items, want %d", itemsPath, total, upstreamItems)
	}
	for fn := range items {
		if !functions[fn] {
			t.Errorf("%s collects items of %s, which %s does not list", itemsPath, fn, functionsPath)
		}
	}

	rowOf := make(map[string]string, len(functionRows))
	ids := make(map[string]bool, len(functionRows)+len(cassetteRows))
	sum := 0
	for _, r := range functionRows {
		if ids[r.id] {
			t.Errorf("%s: ID %s is used by more than one row", matrixPath, r.id)
		}
		ids[r.id] = true
		if prev, dup := rowOf[r.upstream]; dup {
			t.Errorf("%s: rows %s and %s both map %s", matrixPath, prev, r.id, r.upstream)
		}
		rowOf[r.upstream] = r.id
		if !functions[r.upstream] {
			t.Errorf("%s: row %s names %s, which %s does not list", matrixPath, r.id, r.upstream, functionsPath)
		}
		if r.items != items[r.upstream] {
			t.Errorf("%s: row %s says %d items for %s; %s collects %d", matrixPath, r.id, r.items, r.upstream, itemsPath, items[r.upstream])
		}
		sum += r.items
		if !matrixStatuses[r.status] {
			t.Errorf("%s: row %s has status %q; want planned, ported or deviation", matrixPath, r.id, r.status)
		}
	}
	for fn := range functions {
		if _, ok := rowOf[fn]; !ok {
			t.Errorf("%s: no row for upstream test %s", matrixPath, fn)
		}
	}
	if len(functionRows) != len(functions) {
		t.Errorf("%s: %d function rows for %d upstream test functions", matrixPath, len(functionRows), len(functions))
	}
	if sum != total {
		t.Errorf("%s: the Items cells add up to %d; %s collects %d", matrixPath, sum, itemsPath, total)
	}

	checkCassetteRows(t, cassetteRows, ids)

	var ported []portedRow
	for _, r := range functionRows {
		if r.status == "ported" {
			ported = append(ported, portedRow{id: r.id, goTest: r.goTest})
		}
	}
	for _, r := range cassetteRows {
		if r.status == "ported" {
			ported = append(ported, portedRow{id: r.id, goTest: r.goTest})
		}
	}
	checkPortedTestsExist(t, ported)
}

// portedRow is a row whose status is ported, with its Go test cell.
type portedRow struct {
	id, goTest string
}

// checkPortedTestsExist checks that every test a ported row names is listed
// by go test -list, with the live tag, in the package the name gives; a
// ported row that names no test fails. With no ported row it runs no go
// command.
func checkPortedTestsExist(t *testing.T, rows []portedRow) {
	t.Helper()
	want := make(map[string]map[string][]string) // package → test → row IDs
	for _, r := range rows {
		names := goTestNames(r.goTest)
		if len(names) == 0 {
			t.Errorf("%s: row %s is ported but its Go test cell names no test as `<package>.Test<Name>`", matrixPath, r.id)
		}
		for _, n := range names {
			if want[n.pkg] == nil {
				want[n.pkg] = make(map[string][]string)
			}
			want[n.pkg][n.test] = append(want[n.pkg][n.test], r.id)
		}
	}
	for _, pkg := range slices.Sorted(maps.Keys(want)) {
		dir := "." + strings.TrimPrefix(pkg, "adapter")
		//nolint:gosec // G204: the go command with the test's own fixed arguments and a package path the matrix names.
		cmd := exec.CommandContext(t.Context(), goTool(t), "test", "-tags=live", "-list", ".*", dir)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Errorf("%s: go test -list for package %s, which ported rows name: %v\n%s", matrixPath, pkg, err, stderr.String())
			continue
		}
		listed := strings.Fields(string(out))
		for _, test := range slices.Sorted(maps.Keys(want[pkg])) {
			if !slices.Contains(listed, test) {
				t.Errorf("%s: ported row(s) %s name %s, which go test -list does not list in package %s", matrixPath, strings.Join(want[pkg][test], ", "), test, pkg)
			}
		}
	}
}

// TestGoTestNames checks how a Go test cell is read for the ported-row check:
// cells in the forms the matrix uses, and cells that name no test.
func TestGoTestNames(t *testing.T) {
	tests := map[string]struct {
		cell string
		want []qualifiedTest
	}{
		"success: root package with a file note": {
			cell: "`adapter.TestPromptedModeAddsSchemaInstructions` (`evaluate_test.go`)",
			want: []qualifiedTest{{pkg: "adapter", test: "TestPromptedModeAddsSchemaInstructions"}},
		},
		"success: nested package": {
			cell: "`adapter/internal/schema.TestOutputRejectsExtraMembers`",
			want: []qualifiedTest{{pkg: "adapter/internal/schema", test: "TestOutputRejectsExtraMembers"}},
		},
		"success: two packages in one cell": {
			cell: "`adapter/internal/rest.TestStatusErrorKeepsStatusAndBody` (6 statuses) and `adapter.TestFailureClasses` (status → SDK kind)",
			want: []qualifiedTest{
				{pkg: "adapter/internal/rest", test: "TestStatusErrorKeepsStatusAndBody"},
				{pkg: "adapter", test: "TestFailureClasses"},
			},
		},
		"success: a replay test and a live test": {
			cell: "`adapter.TestReplayReferenceShape` (`replay_test.go`); live: `adapter/livetest.TestLiveReferenceShape` (`-tags live`)",
			want: []qualifiedTest{
				{pkg: "adapter", test: "TestReplayReferenceShape"},
				{pkg: "adapter/livetest", test: "TestLiveReferenceShape"},
			},
		},
		"none: a deviation cell": {
			cell: "none: `Close` takes no context",
		},
		"none: an unqualified name": {
			cell: "`TestPortTestMatrix`",
		},
		"none: a file name only": {
			cell: "(`evaluate_test.go`)",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := goTestNames(tt.cell)
			if !slices.Equal(got, tt.want) {
				t.Errorf("goTestNames(%q) = %v, want %v", tt.cell, got, tt.want)
			}
		})
	}
}

// qualifiedTest is a Go test named in the matrix as <package>.Test<Name>.
type qualifiedTest struct {
	pkg, test string
}

// goTestNames returns the tests a Go test cell names: every backtick-quoted
// <package>.Test<Name>, the package as its path from the repository root
// (adapter, adapter/openai).
func goTestNames(cell string) []qualifiedTest {
	var names []qualifiedTest
	for _, m := range goTestName.FindAllStringSubmatch(cell, -1) {
		names = append(names, qualifiedTest{pkg: m[1], test: m[2]})
	}
	return names
}

// checkCassetteRows checks the cassette table: one row per file of
// testdata/cassettes, and an expected-response file exactly where the row
// says yes.
func checkCassetteRows(t *testing.T, rows []cassetteRow, ids map[string]bool) {
	t.Helper()
	cassettes := dirFiles(t, cassettesDir)
	expected := dirFiles(t, expectedDir)
	seen := make(map[string]bool, len(rows))
	withExpected := 0
	for _, r := range rows {
		if ids[r.id] {
			t.Errorf("%s: ID %s is used by more than one row", matrixPath, r.id)
		}
		ids[r.id] = true
		if seen[r.file] {
			t.Errorf("%s: cassette %s has more than one row", matrixPath, r.file)
		}
		seen[r.file] = true
		if !cassettes[r.file] {
			t.Errorf("%s: row %s names %s, which is not in %s", matrixPath, r.id, r.file, cassettesDir)
		}
		switch r.expected {
		case "yes":
			withExpected++
			if !expected[r.file] {
				t.Errorf("%s: row %s says %s has an expected response, which is not in %s", matrixPath, r.id, r.file, expectedDir)
			}
		case "no":
			if expected[r.file] {
				t.Errorf("%s: row %s says %s has no expected response, but %s holds one", matrixPath, r.id, r.file, expectedDir)
			}
		default:
			t.Errorf("%s: row %s has expected-file cell %q; want yes or no", matrixPath, r.id, r.expected)
		}
		if !matrixStatuses[r.status] {
			t.Errorf("%s: row %s has status %q; want planned, ported or deviation", matrixPath, r.id, r.status)
		}
	}
	for f := range cassettes {
		if !seen[f] {
			t.Errorf("%s: no row for cassette %s", matrixPath, f)
		}
	}
	if withExpected != len(expected) {
		t.Errorf("%s: %d rows name an expected response; %s holds %d files", matrixPath, withExpected, expectedDir, len(expected))
	}
}

// functionRow is a row of the matrix's function table.
type functionRow struct {
	id, upstream, goTest, status string
	items                        int
}

// cassetteRow is a row of the matrix's cassette table.
type cassetteRow struct {
	id, file, goTest, expected, status string
}

// readMatrix returns the rows of the function table (seven cells: the ID,
// the upstream test, its items, the Go test, the Go cases, the intended
// outcome, the status) and of the cassette table (five cells: the ID, the
// cassette, the Go test, whether an expected response exists, the status). A row whose first cell is a row ID
// and whose shape fits neither table fails the test.
func readMatrix(t *testing.T) ([]functionRow, []cassetteRow) {
	t.Helper()
	var functions []functionRow
	var cassettes []cassetteRow
	for i, line := range strings.Split(readFile(t, matrixPath), "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|"), "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		switch id := cells[0]; {
		case cassetteRowID.MatchString(id) && len(cells) == 5:
			m := cassetteCell.FindStringSubmatch(cells[1])
			if m == nil {
				t.Errorf("%s:%d: cassette cell %q is not a backtick-quoted file name", matrixPath, i+1, cells[1])
				continue
			}
			cassettes = append(cassettes, cassetteRow{id: id, file: m[1], goTest: cells[2], expected: cells[3], status: cells[4]})
		case functionRowID.MatchString(id) && len(cells) == 7:
			m := upstreamCell.FindStringSubmatch(cells[1])
			if m == nil {
				t.Errorf("%s:%d: upstream cell %q is not a backtick-quoted tests/<file>.py::<function>", matrixPath, i+1, cells[1])
				continue
			}
			n, err := strconv.Atoi(cells[2])
			if err != nil {
				t.Errorf("%s:%d: items cell %q: %v", matrixPath, i+1, cells[2], err)
				continue
			}
			functions = append(functions, functionRow{id: id, upstream: m[1], goTest: cells[3], items: n, status: cells[6]})
		case functionRowID.MatchString(id) || cassetteRowID.MatchString(id):
			t.Errorf("%s:%d: row %s has %d cells; a function row has 7, a cassette row 5", matrixPath, i+1, id, len(cells))
		}
	}
	if len(functions) == 0 || len(cassettes) == 0 {
		t.Fatalf("%s: %d function rows and %d cassette rows read; the check would pass vacuously", matrixPath, len(functions), len(cassettes))
	}
	return functions, cassettes
}

// readFunctions returns the upstream test functions of functions.txt, whose
// lines other than # comments are tests/<file>.py::<function>.
func readFunctions(t *testing.T) map[string]bool {
	t.Helper()
	functions := make(map[string]bool)
	for i, line := range strings.Split(readFile(t, functionsPath), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !upstreamCell.MatchString("`" + line + "`") {
			t.Errorf("%s:%d: %q is not tests/<file>.py::<function>", functionsPath, i+1, line)
			continue
		}
		if functions[line] {
			t.Errorf("%s:%d: %s is listed twice", functionsPath, i+1, line)
		}
		functions[line] = true
	}
	return functions
}

// readItems returns the number of collected items per upstream test
// function, from items.txt: after its # comment, one pytest node id per line,
// tests/<file>.py::<function>[<parameters>].
func readItems(t *testing.T) map[string]int {
	t.Helper()
	items := make(map[string]int)
	for i, line := range strings.Split(readFile(t, itemsPath), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fn, _, _ := strings.Cut(line, "[")
		if !upstreamCell.MatchString("`" + fn + "`") {
			t.Errorf("%s:%d: %q is not a pytest node id tests/<file>.py::<function>[<parameters>]", itemsPath, i+1, line)
			continue
		}
		items[fn]++
	}
	return items
}

// dirFiles returns the names of the regular files in dir.
func dirFiles(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() {
			files[e.Name()] = true
		}
	}
	return files
}

// readFile returns the file at path, a slash-separated path relative to the
// module root, with CRLF line ends read as LF.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}
