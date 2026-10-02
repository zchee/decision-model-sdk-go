// Copyright 2026 The typesafe-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
)

// reprVectorsPath is the table adapter/testdata/python/gen_repr_json_vectors.py
// writes, as committed.
const reprVectorsPath = "../../testdata/python/repr_json_vectors.jsonl"

// The upstream release and Python SDK version the table was made with.
const (
	wantUpstreamVersion = "0.2.1"
	wantUpstreamCommit  = "e1d4cc938204b22fc5a3c3aca7044072fe3f712d"
	wantPythonSDK       = "0.7.0"
)

// reprVector is one row of the table.
type reprVector struct {
	name      string
	class     string
	criterion string
	legend    string // the text json.dumps wrote; "" when refused
	refused   string // "<stage>:<class>"; "" when a legend was written
}

// member returns the text of the member of row at path, failing the test when
// it is missing.
func member(t *testing.T, row Node, path ...string) string {
	t.Helper()
	v := row
	for _, name := range path {
		var ok bool
		if v, ok = v.Member(name); !ok {
			t.Fatalf("%s: no member %v", reprVectorsPath, path)
		}
	}
	return v.Text()
}

// readReprVectors returns the rows of the table after checking its header:
// the format and generator, the reference versions, upstream's release and
// commit, the Python SDK's version and the row count.
func readReprVectors(t *testing.T) []reprVector {
	t.Helper()
	data, err := os.ReadFile(reprVectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	header, err := Read([]byte(lines[0]))
	if err != nil {
		t.Fatalf("%s: header: %v", reprVectorsPath, err)
	}
	if got := member(t, header, "generator"); member(t, header, "format") != "1" || got != "gen_repr_json_vectors.py" {
		t.Fatalf("%s: format %s by %q; want format 1 by gen_repr_json_vectors.py", reprVectorsPath, member(t, header, "format"), got)
	}
	if version, _, _ := strings.Cut(member(t, header, "python"), " "); version != wantPython {
		t.Fatalf("%s: header python %q, want CPython %s", reprVectorsPath, member(t, header, "python"), wantPython)
	}
	for _, check := range []struct {
		path []string
		want string
	}{
		{[]string{"pydantic"}, wantPydantic},
		{[]string{"pydantic_core"}, wantPydanticCore},
		{[]string{"system_one_adapter", "version"}, wantUpstreamVersion},
		{[]string{"system_one_adapter", "commit"}, wantUpstreamCommit},
		{[]string{"typesafe_sdk"}, wantPythonSDK},
		{[]string{"rows"}, strconv.Itoa(len(lines) - 1)},
	} {
		if got := member(t, header, check.path...); got != check.want {
			t.Fatalf("%s: header %v = %q, want %q", reprVectorsPath, check.path, got, check.want)
		}
	}
	rows := make([]reprVector, 0, len(lines)-1)
	for _, line := range lines[1:] {
		row, err := Read([]byte(line))
		if err != nil {
			t.Fatalf("%s: %v in %q", reprVectorsPath, err, line)
		}
		v := reprVector{name: member(t, row, "case"), criterion: member(t, row, "criterion")}
		if c, ok := row.Member("class"); ok {
			v.class = c.Text()
		}
		if l, ok := row.Member("legend"); ok {
			v.legend = l.Text()
		} else {
			v.refused = member(t, row, "refused")
		}
		rows = append(rows, v)
	}
	return rows
}

// TestNodeValueMatchesUpstreamLegend writes the first criterion of every
// request of the generated table with Value(Repr) and Marshal, and compares
// the bytes with upstream's legend entry as json.dumps writes it in the
// response body (_client.py:147). The rows Python refuses are refused here
// too where this package refuses: a request json.loads refuses (an integer of
// 4301 digits) by Read, and a criterion past 255 levels by Value. The rows
// that upstream's question validation refuses because a criterion is not
// text, an object or an array belong to the question parser, which is not
// in this package; they are counted. The one class, convert-depth, is a
// criterion of 255 levels that upstream's question validation accepts;
// after the model call, upstream's legend conversion
// (system-one-adapter-python v0.2.1, _client.py:147) raises
// PydanticSerializationError, an exception that is neither a result nor a
// typed error. The port answers at that depth: Value writes the criterion.
func TestNodeValueMatchesUpstreamLegend(t *testing.T) {
	rows := readReprVectors(t)
	var equal, refusedBoth, notHere, classed int
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			request := `{"q":{"type":"score","criteria":[` + row.criterion + `,"x"]}}`
			doc, err := Read([]byte(request))
			if strings.HasPrefix(row.refused, "loads:") {
				if !errors.Is(err, ErrIntDigits) {
					t.Fatalf("Read: %v; want ErrIntDigits, as json.loads refuses (%s)", err, row.refused)
				}
				refusedBoth++
				return
			}
			if err != nil {
				t.Fatalf("Read of the request: %v", err)
			}
			q, _ := doc.Member("q")
			criteria, _ := q.Member("criteria")
			v, err := criteria.Index(0).Value(Repr)
			switch {
			case strings.HasPrefix(row.refused, "validation:") && strings.HasPrefix(row.name, "depth/"):
				if !errors.Is(err, ErrDepth) {
					t.Fatalf("Value: %v; want ErrDepth, as upstream's validation refuses (%s)", err, row.refused)
				}
				refusedBoth++
				return
			case strings.HasPrefix(row.refused, "validation:"):
				if err != nil {
					t.Fatalf("Value: %v", err)
				}
				notHere++
				return
			case err != nil:
				t.Fatalf("Value: %v", err)
			}
			got, err := Marshal(v)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if row.class == "convert-depth" {
				if row.refused != "convert:PydanticSerializationError" {
					t.Fatalf("a convert-depth row refused as %q", row.refused)
				}
				if want := strings.Repeat("[", 254) + "1" + strings.Repeat("]", 254); string(got) != want {
					t.Fatalf("Value wrote %d bytes, not the criterion itself", len(got))
				}
				classed++
				return
			}
			if row.refused != "" {
				t.Fatalf("upstream refused with %s and has no class; Value wrote %s", row.refused, got)
			}
			if string(got) != row.legend {
				t.Fatalf("Value(Repr):\n got: %s\nwant: %s", got, row.legend)
			}
			equal++
		})
	}
	if t.Failed() {
		return
	}
	t.Logf("%d rows: %d equal bytes, %d refused by both, %d refused by the question validation (not this package), %d convert-depth", len(rows), equal, refusedBoth, notHere, classed)
	if equal+refusedBoth+notHere+classed != len(rows) || classed != 1 {
		t.Fatalf("the counts do not add up to the table's rows or the class is not seen once")
	}
}

func TestNodeValue(t *testing.T) {
	deep := strings.Repeat("[", 254) + "0" + strings.Repeat("]", 254)
	tests := map[string]struct {
		doc     string
		style   FloatStyle
		want    string
		wantErr error
	}{
		"success: integer literals keep their digits, -0 is 0": {
			doc: `[0,-0,12345678901234567890123,-7]`, style: Repr,
			want: `[0,0,12345678901234567890123,-7]`,
		},
		"success: floats in the response spelling": {
			doc: `[1e-5,1.50,1E5,-0.0,1e16,0.0001]`, style: Repr,
			want: `[1e-05,1.5,100000.0,-0.0,1e+16,0.0001]`,
		},
		"success: floats in the prompt spelling": {
			doc: `[1e-5,1.50,1E5,-0.0,1e16,0.0001]`, style: Pydantic,
			want: `[0.00001,1.5,100000.0,-0.0,1e+16,0.0001]`,
		},
		"success: a literal beyond a double is null in the response spelling": {
			doc: `{"a":1e400,"b":-1E+400,"c":1e-400}`, style: Repr,
			want: `{"a":null,"b":null,"c":0.0}`,
		},
		"success: a literal beyond a double is null in the prompt spelling": {
			doc: `[1e400]`, style: Pydantic,
			want: `[null]`,
		},
		"success: a repeated name keeps its first position and its last value": {
			doc: `{"b":1,"a":{"x":1,"x":2},"b":[true,false,null]}`, style: Repr,
			want: `{"b":[true,false,null],"a":{"x":2}}`,
		},
		"success: strings as String writes them": {
			doc: `"é\u0001\/ "`, style: Repr,
			want: "\"é\\u0001/ \"",
		},
		"success: 255 levels": {
			doc: deep, style: Repr,
			want: deep,
		},
		"error: 256 levels": {
			doc: `[` + deep + `]`, style: Repr,
			wantErr: ErrDepth,
		},
		"error: 256 levels below a member": {
			doc: `{"a":` + deep + `}`, style: Repr,
			wantErr: ErrDepth,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			n, err := Read([]byte(tt.doc))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			v, err := n.Value(tt.style)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Value: %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			got, err := Marshal(v)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNodeValueOfZeroNode(t *testing.T) {
	v, err := Node{}.Value(Repr)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Marshal(v)
	if err != nil || string(got) != "null" {
		t.Fatalf("Marshal(Node{}.Value(Repr)) = %s, %v; want null", got, err)
	}
}
