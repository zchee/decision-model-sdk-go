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
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

var coverprofileIn = flag.String("coverprofile-in", "", "coverage profile whose uncovered blocks must exactly match the documented reasons")

var coverageBounds = regexp.MustCompile(`^([1-9][0-9]*)\.([1-9][0-9]*),([1-9][0-9]*)\.([1-9][0-9]*)$`)

type coverageBlock struct {
	file, bounds string
}

var conditionalCoverageBlocks = map[coverageBlock]bool{
	{file: "adapter/seam.go", bounds: "617.3,618.1"}: true,
	{file: "adapter/seam.go", bounds: "652.3,653.1"}: true,
}

type coverageReason struct {
	block     coverageBlock
	low, high uint64
}

// TestUncoveredLinesListed validates every documented source range and reason.
// With -coverprofile-in it also rejects unlisted and stale uncovered blocks.
func TestUncoveredLinesListed(t *testing.T) {
	rows, err := readCoverageReasons(readFile(t, "docs/uncovered-lines.md"))
	if err != nil {
		t.Fatal(err)
	}
	sources := make(map[string][]string)
	checkSource := func(block coverageBlock) {
		t.Helper()
		lines, known := sources[block.file]
		if !known {
			b, err := os.ReadFile(filepath.FromSlash(strings.TrimPrefix(block.file, "adapter/")))
			if err != nil {
				t.Fatalf("coverage source %s: %v", block.file, err)
			}
			lines = strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
			sources[block.file] = lines
		}
		bounds := coverageBounds.FindStringSubmatch(block.bounds)
		values := make([]int, 4)
		for i := range values {
			values[i], err = strconv.Atoi(bounds[i+1])
			if err != nil {
				t.Fatalf("coverage coordinates %s: %v", block.bounds, err)
			}
		}
		sl, sc, el, ec := values[0], values[1], values[2], values[3]
		if sl > el || el > len(lines) || sc > len(lines[sl-1])+1 || ec > len(lines[el-1])+1 || (sl == el && sc > ec) {
			t.Errorf("stale source range %s:%s", block.file, block.bounds)
		}
	}
	for _, row := range rows {
		checkSource(row.block)
	}
	if *coverprofileIn == "" {
		t.Log("source ranges and reasons checked; use -coverprofile-in to compare a measured profile")
		return
	}
	b, err := os.ReadFile(*coverprofileIn)
	if err != nil {
		t.Fatalf("read coverage profile: %v", err)
	}
	counts, err := readCoverageProfile(string(b))
	if err != nil {
		t.Fatal(err)
	}
	for block := range counts {
		checkSource(block)
	}
	if err := compareCoverageReasons(counts, rows); err != nil {
		t.Fatal(err)
	}
	t.Logf("checked %d measured blocks against %d precise reasons", len(counts), len(rows))
}

// coverageSourcePath accepts canonical repository-relative adapter source paths.
func coverageSourcePath(file string) bool {
	return strings.HasPrefix(file, "adapter/") && path.Clean(file) == file && filepath.IsLocal(filepath.FromSlash(file)) && strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go")
}

// excludedCoverageSource matches the module's excluded Codecov support paths.
func excludedCoverageSource(file string) bool {
	for _, prefix := range []string{"adapter/examples/", "adapter/livetest/", "adapter/internal/cassette/", "adapter/internal/fake/"} {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	return false
}

// readCoverageProfile merges duplicate blocks by their largest hit count.
func readCoverageProfile(text string) (map[coverageBlock]uint64, error) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	if !scanner.Scan() {
		return nil, errors.New("coverage profile has no valid mode header")
	}
	switch scanner.Text() {
	case "mode: set", "mode: count", "mode: atomic":
	default:
		return nil, errors.New("coverage profile has no valid mode header")
	}
	counts := make(map[coverageBlock]uint64)
	statements := make(map[coverageBlock]uint64)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// The filename may contain spaces; the final two fields are numeric.
		at := strings.LastIndex(line, " ")
		if at < 0 {
			return nil, fmt.Errorf("invalid coverage block %q", line)
		}
		beforeCount, countText := line[:at], line[at+1:]
		count, err := strconv.ParseUint(countText, 10, 64)
		at = strings.LastIndex(beforeCount, " ")
		if err != nil || at < 0 {
			return nil, fmt.Errorf("invalid coverage block %q", line)
		}
		n, err := strconv.ParseUint(beforeCount[at+1:], 10, 64)
		location := beforeCount[:at]
		colon := strings.LastIndex(location, ":")
		if err != nil || colon < 0 || !coverageBounds.MatchString(location[colon+1:]) {
			return nil, fmt.Errorf("invalid coverage location %q", line)
		}
		file, inModule := strings.CutPrefix(location[:colon], modulePath+"/")
		file = "adapter/" + file
		if !inModule || !coverageSourcePath(file) {
			return nil, fmt.Errorf("coverage file is outside the adapter module: %q", location[:colon])
		}
		if excludedCoverageSource(file) {
			continue
		}
		block := coverageBlock{file: file, bounds: location[colon+1:]}
		if previous, known := statements[block]; known && previous != n {
			return nil, fmt.Errorf("coverage block %v has inconsistent statement counts", block)
		}
		statements[block] = n
		counts[block] = max(counts[block], count)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read coverage profile: %w", err)
	}
	if len(counts) == 0 {
		return nil, errors.New("coverage profile has no block outside excluded paths")
	}
	return counts, nil
}

// readCoverageReasons rejects malformed, duplicate, excluded and blank entries.
func readCoverageReasons(text string) ([]coverageReason, error) {
	var rows []coverageReason
	seen := make(map[coverageBlock]bool)
	headers := 0
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) != 4 {
			return nil, fmt.Errorf("coverage reason table row has %d cells, want 4", len(cells))
		}
		if cells[0] == "File" && cells[1] == "Block" && cells[2] == "Uncovered" && cells[3] == "Reason" {
			headers++
			continue
		}
		if cells[0] == "---" && cells[1] == "---" && cells[2] == "---" && cells[3] == "---" {
			continue
		}
		block := coverageBlock{file: strings.Trim(cells[0], "`"), bounds: strings.Trim(cells[1], "`")}
		if !coverageSourcePath(block.file) || excludedCoverageSource(block.file) || !coverageBounds.MatchString(block.bounds) {
			return nil, fmt.Errorf("invalid or excluded coverage reason key %v", block)
		}
		lowText, highText, ranged := strings.Cut(cells[2], "-")
		if !ranged {
			highText = lowText
		}
		low, lowErr := strconv.ParseUint(lowText, 10, 64)
		high, highErr := strconv.ParseUint(highText, 10, 64)
		if lowErr != nil || highErr != nil || high < 1 || low > high || (ranged && low == high) {
			return nil, fmt.Errorf("invalid uncovered count %q", cells[2])
		}
		if ranged && (cells[2] != "0-1" || !conditionalCoverageBlocks[block]) {
			return nil, fmt.Errorf("conditional uncovered range is not allowed for %v", block)
		}
		if seen[block] || strings.Trim(cells[3], " -") == "" {
			return nil, fmt.Errorf("duplicate block or blank reason for %v", block)
		}
		seen[block] = true
		rows = append(rows, coverageReason{block: block, low: low, high: high})
	}
	if headers != 1 || len(rows) == 0 {
		return nil, errors.New("coverage reasons need one header and a nonempty table")
	}
	return rows, nil
}

// compareCoverageReasons requires an exact manifest of uncovered blocks.
func compareCoverageReasons(counts map[coverageBlock]uint64, rows []coverageReason) error {
	listed := make(map[coverageBlock]bool, len(rows))
	for _, row := range rows {
		count, exists := counts[row.block]
		if listed[row.block] || !exists {
			return fmt.Errorf("duplicate or stale coverage reason for %v", row.block)
		}
		listed[row.block] = true
		var zero uint64
		if count == 0 {
			zero = 1
		}
		if zero < row.low || zero > row.high {
			return fmt.Errorf("stale coverage reason for %v: uncovered=%d, want %d-%d", row.block, zero, row.low, row.high)
		}
	}
	for block, count := range counts {
		if count == 0 && !listed[block] {
			return fmt.Errorf("unlisted uncovered block %s:%s", block.file, block.bounds)
		}
	}
	return nil
}

// TestCoverageProfileValidation exercises merging and invalid profile boundaries.
func TestCoverageProfileValidation(t *testing.T) {
	block := modulePath + "/evaluate.go:1.1,2.1 1 "
	tests := map[string]struct {
		text       string
		wantBlocks int
		refused    bool
	}{
		"success: duplicate profiles retain covered hit": {text: "mode: atomic\n" + block + "0\n" + block + "1\n", wantBlocks: 1},
		"error: empty":                      {refused: true},
		"error: no blocks":                  {text: "mode: atomic\n", refused: true},
		"error: foreign module":             {text: "mode: atomic\nother/module/evaluate.go:1.1,2.1 1 0\n", refused: true},
		"error: traversal":                  {text: "mode: atomic\n" + modulePath + "/../evaluate.go:1.1,2.1 1 0\n", refused: true},
		"error: inconsistent duplicate":     {text: "mode: atomic\n" + block + "0\n" + modulePath + "/evaluate.go:1.1,2.1 2 0\n", refused: true},
		"error: only excluded support code": {text: "mode: atomic\n" + modulePath + "/internal/cassette/replay.go:1.1,2.1 1 0\n", refused: true},
		"error: malformed count":            {text: "mode: atomic\n" + block + "bad\n", refused: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			counts, err := readCoverageProfile(tt.text)
			if (err != nil) != tt.refused || (!tt.refused && len(counts) != tt.wantBlocks) {
				t.Fatalf("profile: blocks=%d err=%v; want blocks=%d refused=%t", len(counts), err, tt.wantBlocks, tt.refused)
			}
			if !tt.refused {
				for block, count := range counts {
					if diff := gocmp.Diff(uint64(1), count); diff != "" {
						t.Errorf("merged block %v count (-want +got):\n%s", block, diff)
					}
				}
			}
		})
	}
}

// TestCoverageReasonsValidation exercises missing, stale and invalid reasons.
func TestCoverageReasonsValidation(t *testing.T) {
	block := coverageBlock{file: "adapter/evaluate.go", bounds: "1.1,2.1"}
	guard := coverageBlock{file: "adapter/seam.go", bounds: "617.3,618.1"}
	const header = "| File | Block | Uncovered | Reason |\n| --- | --- | --- | --- |\n"
	const row = "| `adapter/evaluate.go` | `1.1,2.1` | 1 | Defensive encoding error. |\n"
	const guardRow = "| `adapter/seam.go` | `617.3,618.1` | 0-1 | Dynamic logging-level guard. |\n"
	tests := map[string]struct {
		doc     string
		counts  map[coverageBlock]uint64
		refused bool
	}{
		"success: precise reason":                       {doc: header + row, counts: map[coverageBlock]uint64{block: 0}},
		"success: conditional block covered":            {doc: header + guardRow, counts: map[coverageBlock]uint64{guard: 1}},
		"success: conditional guard uncovered":          {doc: header + guardRow, counts: map[coverageBlock]uint64{guard: 0}},
		"error: conditional range on a non-guard block": {doc: header + strings.Replace(row, "| 1 |", "| 0-1 |", 1), counts: map[coverageBlock]uint64{block: 1}, refused: true},
		"error: conditional range wider than 0-1":       {doc: header + strings.Replace(guardRow, "| 0-1 |", "| 0-2 |", 1), counts: map[coverageBlock]uint64{guard: 1}, refused: true},
		"error: missing reason":                         {doc: header + row, counts: map[coverageBlock]uint64{block: 0, {file: "adapter/model.go", bounds: "1.1,2.1"}: 0}, refused: true},
		"error: stale covered block":                    {doc: header + row, counts: map[coverageBlock]uint64{block: 1}, refused: true},
		"error: stale absent block":                     {doc: header + row, counts: map[coverageBlock]uint64{}, refused: true},
		"error: duplicate reason":                       {doc: header + row + row, refused: true},
		"error: blank reason":                           {doc: header + strings.Replace(row, "Defensive encoding error.", "", 1), refused: true},
		"error: blank table":                            {doc: header, refused: true},
		"error: inverted count range":                   {doc: header + strings.Replace(row, "| 1 |", "| 2-1 |", 1), refused: true},
		"error: excluded reason":                        {doc: header + strings.Replace(row, "adapter/evaluate.go", "adapter/internal/fake/fake.go", 1), refused: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rows, err := readCoverageReasons(tt.doc)
			if err == nil {
				err = compareCoverageReasons(tt.counts, rows)
			}
			if (err != nil) != tt.refused {
				t.Fatalf("reasons: err=%v; want refused=%t", err, tt.refused)
			}
		})
	}
}
