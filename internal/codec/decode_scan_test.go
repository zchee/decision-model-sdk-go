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

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// systemOneWhole is systemOne as it was before W5.3 (K36): the traversal
// over the whole body, then the trailing-data scan with decoder.Skip.
func (d *decoder) systemOneWhole(body []byte, q *wire.Prepared, model string, dst *wire.SystemOneResult) (Skipped, error) {
	s := NoCopyString(body)
	d.stats = stats{}
	if err := d.traverseWhole(s, body, modeSystemOne); err != nil {
		return Skipped{}, err
	}
	return d.finish(s, q, model, dst, nil)
}

// modelsWhole is models as it was before W5.3, as systemOneWhole is.
func (d *decoder) modelsWhole(body []byte, dst *wire.ModelList) error {
	s := NoCopyString(body)
	d.stats = stats{}
	if err := d.traverseWhole(s, body, modeModels); err != nil {
		return err
	}
	return d.finishModels(dst)
}

// scanOutcome is what a decode returned, in a form two decodes can be
// compared by: the error's text, path and depth mark, and the result.
type scanOutcome struct {
	Err      string
	Path     string
	Depth    bool
	Model    string
	Usage    wire.Usage
	Answers  []wire.AnswerEntry
	Skipped  []SkippedAnswer
	Count    int
	Models   []wire.ModelCard
	RawScans int
}

// outcome builds the scanOutcome of a decode.
func outcome(tb testing.TB, err error, res *wire.SystemOneResult, skipped Skipped, models *wire.ModelList, st stats) scanOutcome {
	tb.Helper()
	o := scanOutcome{RawScans: st.scans}
	if err != nil {
		o.Err, o.Path, o.Depth = err.Error(), pathOf(tb, err), errors.Is(err, errDepth)
		return o
	}
	if res != nil {
		o.Model, o.Usage, o.Answers = res.Model, res.Usage, res.Answers.Entries()
		o.Skipped, o.Count = skipped.Named(), skipped.Count
	}
	if models != nil {
		o.Models = models.Models
	}
	return o
}

// decodeBothScans decodes body as a System One response or a models
// response, on the one-scan traversal and on the whole-body one, each on a
// decoder of its own, and returns both outcomes and whether the one-scan
// decode read the body once (the cut traversal decided alone).
func decodeBothScans(tb testing.TB, body []byte, models bool) (one, whole scanOutcome, once bool) {
	tb.Helper()
	d1, d2 := newDecoder(), newDecoder()
	defer d1.release()
	defer d2.release()
	if models {
		var m1, m2 wire.ModelList
		err1 := d1.models(body, &m1)
		err2 := d2.modelsWhole(body, &m2)
		return outcome(tb, err1, nil, Skipped{}, &m1, d1.stats), outcome(tb, err2, nil, Skipped{}, &m2, d2.stats), d1.stats.wholes == 0
	}
	var r1, r2 wire.SystemOneResult
	s1, err1 := d1.systemOne(body, nil, "", &r1, nil)
	s2, err2 := d2.systemOneWhole(body, nil, "", &r2)
	return outcome(tb, err1, &r1, s1, nil, d1.stats), outcome(tb, err2, &r2, s2, nil, d2.stats), d1.stats.wholes == 0
}

// nested returns n containers nested in each other, the innermost holding
// inner: objects each with one member "a", or arrays.
func nested(n int, objects bool, inner string) string {
	if objects {
		return strings.Repeat(`{"a":`, n) + inner + strings.Repeat("}", n)
	}
	return strings.Repeat("[", n) + inner + strings.Repeat("]", n)
}

// TestOneScanMatchesWholeScan checks the one-scan traversal of K36 against
// the traversal it replaced, the whole body and then decoder.Skip's
// trailing-data scan: on every body below both give the same error (text,
// path, depth mark) or the same result, and the same raw-control scans. The
// bodies are every fixture, decoded as a System One and as a models
// response; every prefix of three fixtures, with and without a closing
// brace appended, which cuts a body in every state the traversal can stand
// in; the valid bodies followed by every kind of tail; and bodies at the
// depth where the visitor's cap and Skip's differ (skipDepth).
func TestOneScanMatchesWholeScan(t *testing.T) {
	type tc struct {
		body   []byte
		models bool
	}
	tests := map[string]tc{}
	add := func(name, body string) {
		tests["system one: "+name] = tc{body: []byte(body)}
		tests["models: "+name] = tc{body: []byte(body), models: true}
	}
	for _, name := range testsupport.FixtureNames(t, "*.json") {
		add("fixture "+name, testsupport.FixtureString(t, name))
	}
	for _, name := range []string{"result.json", "structured-legend.json", "models.json", "escaped-member-names.json", "escaped-names.json", "duplicates.json", "deviation-lone-surrogate.json"} {
		body := testsupport.FixtureString(t, name)
		for i := range len(body) + 1 {
			add(name+" cut at "+strconv.Itoa(i), body[:i])
			add(name+" cut at "+strconv.Itoa(i)+" and closed", body[:i]+"}")
		}
		for _, tail := range []string{" ", "\t\n\r ", "}", " }", "}}", "x", "{}", `{"a":1}`, "]", ",", "\f", " ", "\x00", "1"} {
			add(name+" followed by "+strconv.Quote(tail), body+tail)
		}
		add(name+" without its closing brace", strings.TrimSuffix(strings.TrimRight(body, " \t\n\r"), "}"))
	}
	for _, body := range []string{
		"", " ", "}", " } ", "{", "{}", " {} ", "{} {}", `{"a"}`, `{"a":}`, `{"a":1,}`, `{"a":1`, `{"a":"}`, `{"a":1}}`,
		`{"a":{}}`, `{"a":{}`, `{"a":[1}`, `{"a":{"b":1}`, `{"a":{"b":1}}}`, `{"a":[]}`, `[{}]`, `"}"`, `1}`, `{,}`,
		`{"models":[]}`, `{"models":[1]}`, `{"models":{}}`, `{"models":[],"models":[]}`, `{"models":[{"name":"n","description":"d","release_date":"r"}]}`,
		`{"model":"m","usage":{},"answers":{}}`, `{"model":"m","usage":{},"answers":{"n":{"type":"noul","noul":1}}}`, `{"model":"m","usage":{}, "x":tru}`,
		// A root key sonic fails to unquote stops it with the error of the
		// cut before the key reaches the visitor (FuzzDecodeResponse found
		// the first and the last).
		`{"\u"}`, `{"\u12"}`, `{"a\u":1}`, `{"\u":1,"model":"m","usage":{}}`, `{"model":"m","usage":{},"\u00"}`,
		`{"model":"m","usage":{},"\u0078":1}`, `{"model":"m","usage":{},"x":"\u"}`, `{"model":"m","usage":{},"x":{"\u":1}}`,
		`{"model":"m","usage":{},"x":"\\u"}`, `{"model":"m","usage":{},"\ud800":1}`, `{"model":"m","usage":{},"\ud800\u":1}`,
		`{"a":1,"b\"}`, `{"a":1,"b\q":2}`, `{"model":"m","usage":{},"\n":1}`, `{"0000000000000000000000000000000\0}`,
		// A scanner reaching the cut inside a token takes it as complete
		// (FuzzDecodeResponse found a long string cut open).
		`{"":"` + strings.Repeat("0", 64) + `}`, `{"model":"jev}`, `{"a":"x}`, `{"a":1.}`, `{"a":1e}`, `{"a":-}`, `{"a":tru}`,
		`{"a":true}`, `{"a":12}`, `{"a":null}`, `{"a":` + strings.Repeat("1", 64) + `e}`, `{"a":` + strings.Repeat("1", 64) + `}`,
		`{"a":"` + strings.Repeat("x", 100) + `","b":"` + strings.Repeat("y", 100) + `}`, `{"a":["x"],"b":"}`, `{"model":"m","usage":{},"x":"y"}`,
		// Escapes around the cut: an escaped quote or backslash last, keys
		// and values with \uXXXX and surrogate pairs, repeated members.
		`{"a":"b\\"}`, `{"a":"b\"}`, `{"a":"b\\\"}`, `{"a":"b\\\\"}`, `{"a\"b":1,"model":"m","usage":{}}`, `{"model":"m\\","usage":{}}`,
		`{"\ud83d\ude00":"\ud83d\ude00","model":"m","usage":{}}`, `{"model":"m","usage":{},"\ud83d\u":1}`, `{"model":"m","usage":{},"\ud83d":"\ude00"}`,
		`{"model":"a","model":"b","usage":{},"usage":{"input_tokens":1}}`, `{"answers":{},"answers":{"n":{"type":"noul","noul":1}},"model":"m","usage":{}}`,
		`{"model":"m","usage":{},"x":"\\"}`, `{"model":"m","usage":{},"x":["\""]}`, `{"model":"m","usage":{},"\\":{}}`, `{"model":"m","usage":{},"x\\":"\\u"}`,
		// A string cut open whose last byte before the cut ends a container
		// or a string (only the count of unescaped quotes tells): sonic's
		// scanner returns a string cut open by the end of its input as
		// complete when its content is a multiple of 32 bytes long.
		`{"a":"` + strings.Repeat("0", 31) + `}}`, `{"a":"` + strings.Repeat("0", 63) + `]}`, `{"a":"` + strings.Repeat("0", 95) + `}}`,
		`{"a":"` + strings.Repeat("x", 64) + `}}`, `{"a":"` + strings.Repeat("x", 64) + `]}`, `{"a":"` + strings.Repeat("x", 64) + `\"}`,
		`{"model":"m","usage":{},"x":"` + strings.Repeat("x", 100) + `{}}`, `{"model":"m","usage":{},"x":"` + strings.Repeat("x", 100) + `\\\"}`,
	} {
		add(strconv.Quote(body), body)
	}
	member := func(v string) string {
		return `{"model":"m","usage":{},"answers":{"n":{"type":"noul","noul":1}},"x":` + v + `}`
	}
	card := func(v string) string {
		return `{"models":[{"name":"n","description":"d","release_date":"r","x":` + v + `}]}`
	}
	for n := skipDepth - 3; n <= maxNesting; n++ {
		for _, inner := range []string{"", "1", "1,1"} {
			objects := inner == "1"
			add("root member of "+strconv.Itoa(n-1)+" containers holding "+strconv.Quote(inner), member(nested(n-1, objects, inner)))
			add("card member of "+strconv.Itoa(n-3)+" containers holding "+strconv.Quote(inner), card(nested(n-3, objects, inner)))
		}
	}
	onceCount := 0
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			one, whole, once := decodeBothScans(t, tt.body, tt.models)
			if diff := gocmp.Diff(whole, one); diff != "" {
				t.Errorf("one-scan decode differs from the whole-body decode (-whole +one):\n%s", diff)
			}
			if once {
				onceCount++
			}
		})
	}
	t.Logf("%d of %d decodes read the body once", onceCount, len(tests))
}

// TestOneScanTakesValidBodies checks that the one-scan traversal decides
// alone, without the trailing-data scan, on the bodies a server sends: each
// fixture that decodes, escaped ones included, and a body with whitespace
// after the root; and that the bodies it does not decide on (cutPoint's
// refusals: a number or literal last, an odd count of unescaped quotes, a
// \u without four hex digits; a root closed before the end, a body not
// ending in a brace, one nested deeper than skipDepth) are handed to the
// whole-body traversal and decoder.Skip.
func TestOneScanTakesValidBodies(t *testing.T) {
	type tc struct {
		body     string
		models   bool
		wantOnce bool
		wantErr  bool
	}
	tests := map[string]tc{
		"success: models.json":                             {body: testsupport.FixtureString(t, "models.json"), models: true, wantOnce: true},
		"success: whitespace after the root":               {body: testsupport.FixtureString(t, "result.json") + " \t\r\n", wantOnce: true},
		"success: an empty root object":                    {body: `{"models":[]}`, models: true, wantOnce: true},
		"success: nested skipDepth deep, Skip's limit":     {body: `{"x":` + nested(skipDepth-1, true, "1") + `,"models":[]}`, models: true, wantOnce: true},
		"success: nested maxNesting deep, taken by Skip":   {body: `{"x":` + nested(maxNesting-1, false, "") + `,"models":[]}`, models: true},
		"error: nested maxNesting deep, refused by Skip":   {body: `{"x":` + nested(maxNesting-1, true, "1") + `,"models":[]}`, models: true, wantErr: true},
		"error: a second root object":                      {body: testsupport.FixtureString(t, "malformed-trailing-value.json"), wantErr: true},
		"error: data after the root that is not a brace":   {body: testsupport.FixtureString(t, "malformed-trailing-garbage.json"), wantErr: true},
		"error: a root that is not an object":              {body: testsupport.FixtureString(t, "malformed-root-array.json"), wantErr: true},
		"error: a truncated body that ends in a brace":     {body: `{"model":"m","usage":{}`, wantErr: true},
		"error: a member without a value before the end":   {body: `{"model":"m","usage":}`, wantErr: true},
		"error: a trailing comma before the closing brace": {body: `{"model":"m","usage":{},}`, wantErr: true},
		"error: a root key whose \\u escape is cut short":  {body: `{"\u":1,"model":"m","usage":{}}`, wantErr: true},
		"success: an escaped key and value":                {body: `{"model":"m\n","usage":{},"\u0078\"":"\u00e9"}`, wantOnce: true},
		"success: an escaped quote last":                   {body: `{"usage":{},"model":"m\""}`, wantOnce: true},
		"success: an escaped backslash before the quote":   {body: `{"usage":{},"model":"m\\"}`, wantOnce: true},
		"success: a surrogate pair in a key and a value":   {body: `{"model":"\ud83d\ude00","usage":{},"\ud83d\ude00":1,"x":{}}`, wantOnce: true},
		"error: a string ending in an escaped quote":       {body: `{"usage":{},"model":"m\"}`, wantErr: true},
		"error: an escaped quote left unterminated":        {body: `{"usage":{},"model":"m\\\"}`, wantErr: true},
		"error: a root key with a cut \\u escape":          {body: `{"model":"m","usage":{},"\u12":{}}`, wantErr: true},
		"success: an escaped backslash before a letter u":  {body: `{"model":"m","usage":{},"x":"\\u"}`},
		"success: a string last, cut":                      {body: `{"usage":{},"model":"m"}`, wantOnce: true},
		"success: a number last, not cut":                  {body: `{"model":"m","usage":{},"x":1}`},
		"success: a literal last, not cut":                 {body: `{"model":"m","usage":{},"x":true}`},
		"error: a string value cut open by the cut":        {body: `{"model":"m","usage":{},"x":"` + strings.Repeat("0", 64) + `}`, wantErr: true},
		"error: a string cut open, a brace last":           {body: `{"model":"m","usage":{},"x":"` + strings.Repeat("x", 31) + `}}`, wantErr: true},
		"error: a string cut open, a bracket last":         {body: `{"model":"m","usage":{},"x":"` + strings.Repeat("x", 63) + `]}`, wantErr: true},
	}
	for _, name := range decodeBenchFixtures {
		body := testsupport.FixtureString(t, name)
		tests["success: "+name] = tc{body: body, wantOnce: true}
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			one, whole, once := decodeBothScans(t, []byte(tt.body), tt.models)
			if diff := gocmp.Diff(whole, one); diff != "" {
				t.Errorf("one-scan decode differs from the whole-body decode (-whole +one):\n%s", diff)
			}
			if gotErr := one.Err != ""; gotErr != tt.wantErr {
				t.Fatalf("error = %q, want an error: %t", one.Err, tt.wantErr)
			}
			if once != tt.wantOnce {
				t.Errorf("read the body once = %t, want %t (errCut = %v)", once, tt.wantOnce, errCut)
			}
		})
	}
}
