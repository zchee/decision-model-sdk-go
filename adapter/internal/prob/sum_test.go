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

package prob

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// sumVectorsPath is the file adapter/testdata/python/gen_sum_vectors.py
// wrote, as committed.
const sumVectorsPath = "../../testdata/python/sum_vectors.tsv"

// The header the vector file must have: the reference versions and the
// columns this test reads. A file made on another version fails the test.
const (
	wantFormat       = "sum-vectors/1"
	wantPython       = "3.14.3"
	wantPydantic     = "2.13.4"
	wantPydanticCore = "2.46.4"
	wantColumns      = "id class inputs sum error changed rescaled score_raw score_norm score_conf_raw score_conf_norm choice_conf_raw choice_conf_norm argmax_raw argmax_norm"
	maxLength        = 20
)

// vector is one line of the vector file: the inputs and CPython's results.
// "raw" results are of the inputs, "norm" results of Normalize(inputs,
// true).Probabilities.
type vector struct {
	id                            int
	class                         string
	inputs, rescaled              []float64
	sum, sumError                 float64
	changed                       bool
	scoreRaw, scoreNorm           float64
	scoreConfRaw, scoreConfNorm   float64
	choiceConfRaw, choiceConfNorm float64
	argmaxRaw, argmaxNorm         int
}

// loadVectors reads the vector file, checks its header against the reference
// versions, and checks that every generated class has a vector of every
// length from 1 to maxLength.
func loadVectors(t *testing.T) []vector {
	t.Helper()
	f, err := os.Open(sumVectorsPath)
	if err != nil {
		t.Fatalf("open the vector file: %v", err)
	}
	defer f.Close()

	parseFloat := func(s string) float64 {
		bits, err := strconv.ParseUint(s, 16, 64)
		if err != nil || len(s) != 16 {
			t.Fatalf("float %q: want 16 hexadecimal digits", s)
		}
		return math.Float64frombits(bits)
	}
	parseFloats := func(s string) []float64 {
		var out []float64
		for f := range strings.SplitSeq(s, ",") {
			out = append(out, parseFloat(f))
		}
		return out
	}
	parseInt := func(s string) int {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("integer %q: %v", s, err)
		}
		return n
	}

	header := map[string]string{}
	var vectors []vector
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if rest, ok := strings.CutPrefix(line, "# "); ok {
			key, value, _ := strings.Cut(rest, ": ")
			header[key] = value
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 15 {
			t.Fatalf("line %.80q: %d fields, want 15", line, len(fields))
		}
		vectors = append(vectors, vector{
			id:             parseInt(fields[0]),
			class:          fields[1],
			inputs:         parseFloats(fields[2]),
			sum:            parseFloat(fields[3]),
			sumError:       parseFloat(fields[4]),
			changed:        fields[5] == "1",
			rescaled:       parseFloats(fields[6]),
			scoreRaw:       parseFloat(fields[7]),
			scoreNorm:      parseFloat(fields[8]),
			scoreConfRaw:   parseFloat(fields[9]),
			scoreConfNorm:  parseFloat(fields[10]),
			choiceConfRaw:  parseFloat(fields[11]),
			choiceConfNorm: parseFloat(fields[12]),
			argmaxRaw:      parseInt(fields[13]),
			argmaxNorm:     parseInt(fields[14]),
		})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read the vector file: %v", err)
	}

	if got := header["format"]; got != wantFormat {
		t.Fatalf("header format = %q, want %q", got, wantFormat)
	}
	if version, _, _ := strings.Cut(header["python"], " "); version != wantPython {
		t.Fatalf("header python = %q, want CPython %s", header["python"], wantPython)
	}
	if got := header["pydantic"]; got != wantPydantic {
		t.Fatalf("header pydantic = %q, want %q", got, wantPydantic)
	}
	if got := header["pydantic_core"]; got != wantPydanticCore {
		t.Fatalf("header pydantic_core = %q, want %q", got, wantPydanticCore)
	}
	if got := header["columns"]; got != wantColumns {
		t.Fatalf("header columns = %q, want %q", got, wantColumns)
	}
	if got := header["count"]; got != strconv.Itoa(len(vectors)) {
		t.Fatalf("header count = %q, the file has %d vectors", got, len(vectors))
	}

	lengths := map[string]*[maxLength + 1]int{}
	for _, v := range vectors {
		if len(v.inputs) < 1 || len(v.inputs) > maxLength {
			t.Fatalf("vector %d: length %d outside 1..%d", v.id, len(v.inputs), maxLength)
		}
		if lengths[v.class] == nil {
			lengths[v.class] = new([maxLength + 1]int)
		}
		lengths[v.class][len(v.inputs)]++
	}
	for class, counts := range lengths {
		if class == "fixed" {
			continue
		}
		for n := 1; n <= maxLength; n++ {
			if counts[n] == 0 {
				t.Fatalf("class %q has no vector of length %d", class, n)
			}
		}
	}
	return vectors
}

// same reports whether two floats are the same result: the same bits, or
// both NaN (a NaN's payload is not compared).
func same(a, b float64) bool {
	return math.Float64bits(a) == math.Float64bits(b) || (math.IsNaN(a) && math.IsNaN(b))
}

func hex(x float64) string { return fmt.Sprintf("%016x", math.Float64bits(x)) }

func hexAll(xs []float64) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = hex(x)
	}
	return strings.Join(parts, ",")
}

// mismatches counts the results that differ from CPython's and reports the
// first few with their inputs.
type mismatches struct {
	t     *testing.T
	count int
}

func (m *mismatches) report(v *vector, name string, equal bool, got, want any) {
	if equal {
		return
	}
	m.count++
	if m.count <= 10 {
		m.t.Errorf("vector %d (%s): %s = %v, CPython %v; inputs %s", v.id, v.class, name, got, want, hexAll(v.inputs))
	}
}

func (m *mismatches) float(v *vector, name string, got, want float64) {
	m.report(v, name, same(got, want), hex(got)+" ("+strconv.FormatFloat(got, 'g', -1, 64)+")", hex(want)+" ("+strconv.FormatFloat(want, 'g', -1, 64)+")")
}

// TestSumMatchesCPython compares Sum and the formulas built on it with
// CPython 3.14.3 on every vector of sum_vectors.tsv, bit for bit (two NaNs
// count as the same result): the sum, and eleven formula results per vector
// (Normalize's error and changed flag, Rescale, and Score, ScoreConfidence,
// ChoiceConfidence and Argmax of the inputs and of Normalize's
// probabilities).
func TestSumMatchesCPython(t *testing.T) {
	vectors := loadVectors(t)
	sums := &mismatches{t: t}
	formulas := &mismatches{t: t}
	classes := map[string]int{}
	changed := 0
	for i := range vectors {
		v := &vectors[i]
		classes[v.class]++
		sums.float(v, "Sum", Sum(v.inputs), v.sum)

		n := Normalize(v.inputs, true)
		norm := n.Probabilities
		formulas.float(v, "Normalize error", n.Error, v.sumError)
		formulas.report(v, "Normalize changed", (n.Original != nil) == v.changed, n.Original != nil, v.changed)
		rescaled := Rescale(v.inputs)
		formulas.report(v, "Rescale", slices.EqualFunc(rescaled, v.rescaled, same), hexAll(rescaled), hexAll(v.rescaled))
		formulas.float(v, "Score(raw)", Score(v.inputs), v.scoreRaw)
		formulas.float(v, "Score(norm)", Score(norm), v.scoreNorm)
		formulas.float(v, "ScoreConfidence(raw)", ScoreConfidence(v.inputs), v.scoreConfRaw)
		formulas.float(v, "ScoreConfidence(norm)", ScoreConfidence(norm), v.scoreConfNorm)
		formulas.float(v, "ChoiceConfidence(raw)", ChoiceConfidence(v.inputs), v.choiceConfRaw)
		formulas.float(v, "ChoiceConfidence(norm)", ChoiceConfidence(norm), v.choiceConfNorm)
		formulas.report(v, "Argmax(raw)", Argmax(v.inputs) == v.argmaxRaw, Argmax(v.inputs), v.argmaxRaw)
		formulas.report(v, "Argmax(norm)", Argmax(norm) == v.argmaxNorm, Argmax(norm), v.argmaxNorm)
		if n.Original != nil {
			changed++
		}
	}
	t.Logf("vectors: %d (classes %v); sums differing: %d; formula results: %d, differing: %d; vectors rescaled by Normalize: %d",
		len(vectors), classes, sums.count, 11*len(vectors), formulas.count, changed)
	if sums.count > 0 {
		t.Errorf("Sum differs from CPython on %d of %d vectors", sums.count, len(vectors))
	}
	if formulas.count > 0 {
		t.Errorf("the formulas differ from CPython in %d of %d results", formulas.count, 11*len(vectors))
	}
}

// TestSumRules shows each rule of CPython's sum() that a plain s += x
// misses, one vector per rule.
func TestSumRules(t *testing.T) {
	negZero := math.Copysign(0, -1)
	inf, nan := math.Inf(1), math.NaN()
	tests := map[string]struct {
		in   []float64
		want float64
	}{
		"success: rule 1, a leading -0.0 enters as 0.0 + x and becomes +0.0": {in: []float64{negZero}, want: 0.0},
		"success: rule 1, two -0.0 sum to +0.0":                              {in: []float64{negZero, negZero}, want: 0.0},
		"success: rule 2, the compensation recovers a cancelled term":        {in: []float64{1e16, 1.0, -1e16}, want: 1.0},
		"success: rule 2, decimal probabilities sum to one":                  {in: []float64{0.7, 0.2, 0.1}, want: 1.0},
		"success: rule 2, an overflowed sum stays infinite":                  {in: []float64{1e308, 1e308}, want: inf},
		"success: rule 2, a later term does not undo an overflow":            {in: []float64{1e308, 1e308, -1e308}, want: inf},
		"success: rule 2, an infinity among finite values stays infinite":    {in: []float64{inf, 1.0, inf}, want: inf},
		"success: opposite infinities give NaN":                              {in: []float64{inf, -inf}, want: nan},
		"success: a NaN term gives NaN":                                      {in: []float64{1.0, nan, 2.0}, want: nan},
		"success: an empty list gives zero":                                  {in: nil, want: 0.0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := Sum(tt.in); !same(got, tt.want) {
				t.Errorf("Sum(%v) = %s (%v), want %s (%v)", tt.in, hex(got), got, hex(tt.want), tt.want)
			}
		})
	}
}

// TestMaxIsStrict shows rule 4: Python's max keeps its first candidate
// unless a later one is strictly greater.
func TestMaxIsStrict(t *testing.T) {
	nan := math.NaN()
	argmax := map[string]struct {
		in   []float64
		want int
	}{
		"success: the first of equal values":   {in: []float64{0.5, 0.5}, want: 0},
		"success: a NaN in first place stays":  {in: []float64{nan, 1.0}, want: 0},
		"success: a NaN elsewhere never wins":  {in: []float64{1.0, nan, 2.0}, want: 2},
		"success: a NaN does not beat a value": {in: []float64{1.0, nan}, want: 0},
	}
	for name, tt := range argmax {
		t.Run("Argmax "+name, func(t *testing.T) {
			if got := Argmax(tt.in); got != tt.want {
				t.Errorf("Argmax(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
	// max(0.0, y) with y a NaN: ScoreConfidence of a distribution holding a
	// NaN is +0.0, where math.Max would give NaN.
	if got := ScoreConfidence([]float64{nan, 1.0}); !same(got, 0.0) {
		t.Errorf("ScoreConfidence([NaN 1]) = %s (%v), want +0.0", hex(got), got)
	}
	// max(errors) with a NaN first: Python keeps the NaN.
	d := DebugData([]*Normalization{{Error: nan}, {Error: 0.5}})
	if !math.IsNaN(d.MaxError) {
		t.Errorf("DebugData MaxError = %v, want NaN (the first error stays)", d.MaxError)
	}
}

// fusedScore is Score as a compiler would compute it if it fused each
// product with the addition that follows it: one rounding for i*p + hi
// instead of two. It exists to show that the vectors detect that fusion.
func fusedScore(ps []float64) float64 {
	var c compensated
	for i, p := range Rescale(ps) {
		a := float64(i)
		if !c.started {
			c.hi = math.FMA(a, p, 0.0)
			c.started = true
			continue
		}
		x := float64(a * p)
		t := math.FMA(a, p, c.hi)
		if math.Abs(c.hi) >= math.Abs(x) {
			c.lo += (c.hi - t) + x
		} else {
			c.lo += (x - t) + c.hi
		}
		c.hi = t
	}
	return c.value()
}

// TestProductIsRoundedBeforeTheSum shows rule 3 on every host: the score
// with each product fused into its addition by math.FMA differs from
// CPython's on some vectors of the file, so the file can tell a fused sum
// from CPython's. Whether a build fuses is the compiler's choice: on arm64
// it fuses the accumulator without its conversion, and TestSumMatchesCPython
// then fails.
func TestProductIsRoundedBeforeTheSum(t *testing.T) {
	vectors := loadVectors(t)
	differ := 0
	for i := range vectors {
		v := &vectors[i]
		if !same(fusedScore(v.inputs), v.scoreRaw) {
			differ++
		}
	}
	t.Logf("a fused score differs from CPython's on %d of %d vectors", differ, len(vectors))
	if differ == 0 {
		t.Error("no vector tells a fused score from CPython's; the file cannot detect rule 3")
	}
}

// TestEmptyDistribution checks that the functions upstream would raise on
// for an empty distribution panic, and that Sum gives 0.
func TestEmptyDistribution(t *testing.T) {
	tests := map[string]func(){
		"error: Rescale":          func() { Rescale(nil) },
		"error: Score":            func() { Score(nil) },
		"error: Argmax":           func() { Argmax(nil) },
		"error: ScoreConfidence":  func() { ScoreConfidence(nil) },
		"error: ChoiceConfidence": func() { ChoiceConfidence(nil) },
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic for an empty distribution")
				}
			}()
			call()
		})
	}
}
