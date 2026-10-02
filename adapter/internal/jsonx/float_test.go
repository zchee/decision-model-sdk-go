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
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestFloatWriters(t *testing.T) {
	tests := map[string]struct {
		in       float64
		pydantic string
		repr     string
	}{
		"success: one":                          {in: 1, pydantic: "1.0", repr: "1.0"},
		"success: positive zero":                {in: 0, pydantic: "0.0", repr: "0.0"},
		"success: negative zero":                {in: math.Copysign(0, -1), pydantic: "-0.0", repr: "-0.0"},
		"success: 1e-5 splits the two writers":  {in: 1e-5, pydantic: "0.00001", repr: "1e-05"},
		"success: 1e-4 is positional in both":   {in: 1e-4, pydantic: "0.0001", repr: "0.0001"},
		"success: 1e-6 has an exponent in both": {in: 1e-6, pydantic: "1e-6", repr: "1e-06"},
		"success: 1.5e-7":                       {in: 1.5e-7, pydantic: "1.5e-7", repr: "1.5e-07"},
		"success: a negative small value":       {in: -2.5e-5, pydantic: "-0.000025", repr: "-2.5e-05"},
		"success: 1e15 is positional":           {in: 1e15, pydantic: "1000000000000000.0", repr: "1000000000000000.0"},
		"success: 1e16 has an exponent":         {in: 1e16, pydantic: "1e+16", repr: "1e+16"},
		"success: three exponent digits":        {in: 1.7976931348623157e308, pydantic: "1.7976931348623157e+308", repr: "1.7976931348623157e+308"},
		"success: smallest subnormal":           {in: 5e-324, pydantic: "5e-324", repr: "5e-324"},
		"success: 100000":                       {in: 1e5, pydantic: "100000.0", repr: "100000.0"},
		"success: a fraction":                   {in: 123456.789, pydantic: "123456.789", repr: "123456.789"},
		"success: shortest digits of 0.1+0.2":   {in: math.Float64frombits(0x3fd3333333333334), pydantic: "0.30000000000000004", repr: "0.30000000000000004"},
		"success: infinity":                     {in: math.Inf(1), pydantic: "Infinity", repr: "inf"},
		"success: negative infinity":            {in: math.Inf(-1), pydantic: "-Infinity", repr: "-inf"},
		"success: not a number":                 {in: math.NaN(), pydantic: "NaN", repr: "nan"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := PydanticFloat(tt.in); got != tt.pydantic {
				t.Errorf("PydanticFloat(%v) = %q, want %q", tt.in, got, tt.pydantic)
			}
			if got := ReprFloat(tt.in); got != tt.repr {
				t.Errorf("ReprFloat(%v) = %q, want %q", tt.in, got, tt.repr)
			}
		})
	}
}

// TestReprMatchesCPython compares both float writers with CPython 3.14.3 on
// every double of float_vectors.tsv: ReprFloat with repr(x) and
// PydanticFloat with pydantic_core.to_json(x), byte for byte. The file holds
// the bit pattern of each double, so no parsing of a decimal is involved.
func TestReprMatchesCPython(t *testing.T) {
	lines := readVectors(t, floatVectorsPath, "float vectors: bits (hex of the IEEE 754 double), repr(x), pydantic_core.to_json(x); tab-separated")
	var reprDiff, pydanticDiff, nonFinite int
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			t.Fatalf("line %q: %d fields, want 3", line, len(fields))
		}
		bits, err := strconv.ParseUint(fields[0], 16, 64)
		if err != nil || len(fields[0]) != 16 {
			t.Fatalf("line %q: %q is not 16 hexadecimal digits", line, fields[0])
		}
		f := math.Float64frombits(bits)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			nonFinite++
		}
		if got := ReprFloat(f); got != fields[1] {
			reprDiff++
			if reprDiff <= 20 {
				t.Errorf("bits %s: ReprFloat = %q, repr = %q", fields[0], got, fields[1])
			}
		}
		if got := PydanticFloat(f); got != fields[2] {
			pydanticDiff++
			if pydanticDiff <= 20 {
				t.Errorf("bits %s: PydanticFloat = %q, to_json = %q", fields[0], got, fields[2])
			}
		}
	}
	t.Logf("floats compared: %d (%d not finite); ReprFloat differs on %d, PydanticFloat on %d", len(lines), nonFinite, reprDiff, pydanticDiff)
	if reprDiff+pydanticDiff > 0 {
		t.Errorf("ReprFloat differs from repr on %d of %d doubles, PydanticFloat from to_json on %d", reprDiff, len(lines), pydanticDiff)
	}
}
