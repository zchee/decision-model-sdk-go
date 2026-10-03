// Copyright 2026 The decision-model-sdk-go Authors.
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
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/go-json-experiment/json/jsontext"
	gocmp "github.com/google/go-cmp/cmp"
)

// nested returns open repeated n times, then inner, then closeTok repeated n
// times.
func nested(open, inner, closeTok string, n int) string {
	return strings.Repeat(open, n) + inner + strings.Repeat(closeTok, n)
}

// replaced returns an object whose member "a" holds v and is then replaced
// by 1: the reader reads v, and nothing of it is written.
func replaced(v string) string {
	return `{"a":` + v + `,"a":1}`
}

func TestEncodeState(t *testing.T) {
	digits := func(n int) string { return strings.Repeat("7", n) }
	tests := map[string]struct {
		doc   string
		plain string // PydanticJSON's output, when it differs from want
		want  string // EncodeState's output
		err   error
	}{
		"success: the numbers of the state rule": {
			doc:  `[1.50, 1E5, 3, 3.0, -0.0, 1e400, 12345678901234567890123, 1e-7]`,
			want: `[1.5,100000.0,3,3.0,-0.0,Infinity,12345678901234567890123,1e-7]`,
		},
		"success: an integer minus zero is zero, a float keeps its sign": {doc: `[-0, -0.0, -0e0]`, want: `[0,-0.0,-0.0]`},
		"success: overflow and underflow of a float literal": {
			doc:  `[-1e400, 1.7976931348623159e308, 1e-400, -1e-400]`,
			want: `[-Infinity,Infinity,0.0,-0.0]`,
		},
		"success: a float literal is rounded to the nearest double": {doc: `9007199254740993.0`, want: `9007199254740992.0`},
		"success: a repeated name keeps the first position and the last value": {
			doc:  `{"a":1,"b":2,"a":3}`,
			want: `{"a":3,"b":2}`,
		},
		"success: names are compared after unescaping": {doc: `{"a":1,"a":2}`, want: `{"a":2}`},
		"success: a repeated name inside an array of objects": {
			doc:  `[{"x":[1],"y":0,"x":{"z":true}}]`,
			want: `[{"x":{"z":true},"y":0}]`,
		},
		"success: white space is removed": {doc: " {\n\t\"a\" : [ 1 , null , false , true ] } \r\n", want: `{"a":[1,null,false,true]}`},
		"success: '<' and '>' are escaped after encoding, in names and values": {
			doc:   "{\"<k>\":\"\x5cu003cv\x5cu003E\",\"x\":\"a<b>c\"}",
			plain: `{"<k>":"<v>","x":"a<b>c"}`,
			want:  "{\"\x5cu003ck\x5cu003e\":\"\x5cu003cv\x5cu003e\",\"x\":\"a\x5cu003cb\x5cu003ec\"}",
		},
		"success: a lone '<'":                             {doc: `"<"`, plain: `"<"`, want: "\"\x5cu003c\""},
		"success: a lone '>'":                             {doc: `">"`, plain: `">"`, want: "\"\x5cu003e\""},
		"success: a surrogate pair becomes one character": {doc: "\"\x5cud83d\x5cude00\"", want: "\"\xf0\x9f\x98\x80\""},
		"success: non-ASCII text is written as it is":     {doc: "\"\x5cu00e9 \xc3\xa9 \x5cu2028\"", want: "\"\xc3\xa9 \xc3\xa9 \xe2\x80\xa8\""},
		"success: control characters, DEL and slash": {
			doc:  `"\u0000\u0008\u0009\u000a\u000b\u000c\u000d\u001F\u007f\/"`,
			want: "\"\\u0000\\b\\t\\n\\u000b\\f\\r\\u001f\x7f/\"",
		},
		"success: quote and backslash": {doc: `"\"\\"`, want: `"\"\\"`},
		"error: NaN is not a JSON token": {
			doc: `{"a":NaN}`, err: ErrSyntax,
		},
		"error: Infinity is not a JSON token":          {doc: `[Infinity]`, err: ErrSyntax},
		"error: -Infinity is not a JSON token":         {doc: `[-Infinity]`, err: ErrSyntax},
		"error: a lone surrogate":                      {doc: `"\ud800"`, err: ErrSyntax},
		"error: a lone surrogate in a replaced value":  {doc: replaced(`"\udc00"`), err: ErrSyntax},
		"error: a lone surrogate in a member name":     {doc: `{"\ud800":1}`, err: ErrSyntax},
		"error: bytes that are not UTF-8":              {doc: "\"\xff\"", err: ErrSyntax},
		"error: a byte order mark":                     {doc: "\xef\xbb\xbf1", err: ErrSyntax},
		"error: data after the value":                  {doc: `1 2`, err: ErrSyntax},
		"error: garbage after the value":               {doc: `{}x`, err: ErrSyntax},
		"error: an empty text":                         {doc: ``, err: ErrSyntax},
		"error: a trailing comma":                      {doc: `[1,]`, err: ErrSyntax},
		"error: a raw control character in a string":   {doc: "\"\x01\"", err: ErrSyntax},
		"success: 255 levels, a scalar at the bottom":  {doc: nested("[", "1", "]", 254), want: nested("[", "1", "]", 254)},
		"error: 256 levels, a scalar at the bottom":    {doc: nested("[", "1", "]", 255), err: ErrDepth},
		"success: 255 arrays":                          {doc: nested("[", "", "]", 255), want: nested("[", "", "]", 255)},
		"error: 256 arrays":                            {doc: nested("[", "", "]", 256), err: ErrDepth},
		"success: 255 levels of objects":               {doc: nested(`{"a":`, `"s"`, "}", 254), want: nested(`{"a":`, `"s"`, "}", 254)},
		"error: 256 levels of objects":                 {doc: nested(`{"a":`, `"s"`, "}", 255), err: ErrDepth},
		"success: a too-deep value that is replaced":   {doc: replaced(nested("[", "", "]", 300)), want: `{"a":1}`},
		"error: a too-deep value that replaces":        {doc: `{"a":1,"a":` + nested("[", "", "]", 300) + `}`, err: ErrDepth},
		"success: an integer of 4300 digits":           {doc: digits(4300), want: digits(4300)},
		"success: a negative integer of 4300 digits":   {doc: "-" + digits(4300), want: "-" + digits(4300)},
		"error: an integer of 4301 digits":             {doc: digits(4301), err: ErrIntDigits},
		"error: a negative integer of 4301 digits":     {doc: "-" + digits(4301), err: ErrIntDigits},
		"error: an integer of 4301 digits, replaced":   {doc: replaced(digits(4301)), err: ErrIntDigits},
		"success: 4301 digits with a fraction":         {doc: digits(4301) + ".0", want: "Infinity"},
		"success: 4301 digits with an exponent":        {doc: digits(4301) + "e0", want: "Infinity"},
		"success: 10000 arrays around a number":        {doc: replaced(nested("[", "1", "]", 9999)), want: `{"a":1}`},
		"error: 10001 arrays around a number":          {doc: replaced(nested("[", "1", "]", 10000)), err: ErrReadDepth},
		"success: 10000 arrays around a string":        {doc: replaced(nested("[", `"s"`, "]", 9999)), want: `{"a":1}`},
		"error: 10001 arrays around a string":          {doc: replaced(nested("[", `"s"`, "]", 10000)), err: ErrReadDepth},
		"success: 10000 arrays around a literal":       {doc: replaced(nested("[", "true", "]", 9999)), want: `{"a":1}`},
		"error: 10001 arrays around a literal":         {doc: replaced(nested("[", "null", "]", 10000)), err: ErrReadDepth},
		"success: 10000 arrays, the last one empty":    {doc: replaced(nested("[", "[]", "]", 9998)), want: `{"a":1}`},
		"error: 10001 arrays, the last one empty":      {doc: replaced(nested("[", "[]", "]", 9999)), err: ErrReadDepth},
		"success: 10000 objects around a number":       {doc: replaced(nested(`{"k":`, "1", "}", 9999)), want: `{"a":1}`},
		"error: 10001 objects around a number":         {doc: replaced(nested(`{"k":`, "1", "}", 10000)), err: ErrReadDepth},
		"success: 10000 objects around a string":       {doc: replaced(nested(`{"k":`, `"s"`, "}", 9999)), want: `{"a":1}`},
		"error: 10001 objects around a literal":        {doc: replaced(nested(`{"k":`, "false", "}", 10000)), err: ErrReadDepth},
		"success: 10000 objects, the last one empty":   {doc: replaced(nested(`{"k":`, "{}", "}", 9998)), want: `{"a":1}`},
		"error: 10001 objects, the last one empty":     {doc: replaced(nested(`{"k":`, "{}", "}", 9999)), err: ErrReadDepth},
		"error: 10000 arrays that are not closed":      {doc: strings.Repeat("[", 10000), err: ErrSyntax},
		"error: 10001 arrays before the end of text":   {doc: strings.Repeat("[", 10001), err: ErrReadDepth},
		"error: a surviving value of 10000 containers": {doc: nested("[", "", "]", 10000), err: ErrDepth},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := EncodeState([]byte(tt.doc))
			if !errors.Is(err, tt.err) || (tt.err == nil) != (err == nil) {
				t.Fatalf("EncodeState error = %v, want %v", err, tt.err)
			}
			if diff := gocmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("EncodeState mismatch (-want +got):\n%s", diff)
			}
			plain, err := PydanticJSON([]byte(tt.doc))
			if !errors.Is(err, tt.err) || (tt.err == nil) != (err == nil) {
				t.Fatalf("PydanticJSON error = %v, want %v", err, tt.err)
			}
			wantPlain := tt.plain
			if wantPlain == "" {
				wantPlain = tt.want
			}
			if diff := gocmp.Diff(wantPlain, string(plain)); diff != "" {
				t.Errorf("PydanticJSON mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// pythonRefusal names the error the port gives for each refusal the
// committed state vectors record for Python.
var pythonRefusal = map[string]error{
	"decode:UnicodeDecodeError":                             ErrSyntax,
	"loads:JSONDecodeError":                                 ErrSyntax,
	"loads:ValueError":                                      ErrIntDigits,
	"to_json:PydanticSerializationError:UnicodeEncodeError": ErrSyntax,
	"to_json:PydanticSerializationError:ValueError":         ErrDepth,
}

// classRefusal names, for each class of document that Python accepts and the
// port refuses, the error the port refuses it with.
var classRefusal = map[string]error{
	"nan-token":               ErrSyntax,
	"replaced-lone-surrogate": ErrSyntax,
	"read-depth":              ErrReadDepth,
}

// The class column's other values.
const (
	classAgree      = "-"
	classTwoDefects = "two-defects"
)

// TestStateMatchesPydantic compares PydanticJSON with
// pydantic_core.to_json(json.loads(document.decode("utf-8"))), and
// EncodeState with that text after upstream's replacement of '<' and '>', on
// every document of state_vectors.tsv. Python's verdict and the class column
// decide what the port must do with a document:
//
//   - verdict ok, class "-": the same bytes, from both functions;
//   - verdict ok, class nan-token, replaced-lone-surrogate or read-depth, or
//     several of them joined with '+': the port refuses, with the error of
//     one of the classes named (the deviations from upstream the port
//     records: Python accepts these texts);
//   - a refusal, class "-": the port refuses, with the error that
//     corresponds to Python's;
//   - a refusal, class two-defects: the port refuses, with any error (the
//     text has two defects, and the two sides may report different ones).
//
// The number of documents of each verdict and class, and of each outcome, is
// pinned, so a file with a row moved from one class to another fails too.
func TestStateMatchesPydantic(t *testing.T) {
	lines := readVectors(t, stateVectorsPath, `state vectors: document (base64), verdict, class, pydantic_core.to_json(json.loads(document.decode("utf-8"))) (base64), the same with < and > escaped (base64, or = when equal); tab-separated`)
	rows := map[string]int{}
	outcomes := map[string]int{}
	failures := 0
	fail := func(format string, args ...any) {
		failures++
		if failures <= 20 {
			t.Errorf(format, args...)
		}
	}
	decode := func(field string) []byte {
		raw, err := base64.StdEncoding.DecodeString(field)
		if err != nil {
			t.Fatalf("bad base64 %q: %v", field, err)
		}
		return raw
	}
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			t.Fatalf("line %.80q: %d fields, want 5", line, len(fields))
		}
		docField, verdict, class := fields[0], fields[1], fields[2]
		doc := decode(docField)
		rows[verdict+" / "+class]++
		plain, plainErr := PydanticJSON(doc)
		state, stateErr := EncodeState(doc)
		if refusalKind(plainErr) != refusalKind(stateErr) {
			fail("document %.120s: PydanticJSON error %v, EncodeState error %v", docField, plainErr, stateErr)
			continue
		}
		if verdict != "ok" {
			want, known := pythonRefusal[verdict]
			switch {
			case !known:
				t.Fatalf("document %.120s: unknown verdict %q", docField, verdict)
			case class != classAgree && class != classTwoDefects:
				t.Fatalf("document %.120s: class %q on a refusal", docField, class)
			case plainErr == nil:
				fail("document %.120s: Python refuses (%s), the port gives %.100q", docField, verdict, plain)
			case errors.Is(plainErr, want):
				outcomes["refused by both, the same kind"]++
			case class == classTwoDefects:
				outcomes["refused by both, another kind (two-defects)"]++
			default:
				fail("document %.120s: Python refuses (%s), the port refuses with %v instead of %v", docField, verdict, plainErr, want)
			}
			continue
		}
		if class != classAgree {
			var allowed []error
			for name := range strings.SplitSeq(class, "+") {
				err, known := classRefusal[name]
				if !known {
					t.Fatalf("document %.120s: unknown class %q", docField, class)
				}
				allowed = append(allowed, err)
			}
			switch {
			case plainErr == nil:
				fail("document %.120s: class %s, the port accepts: %.100q", docField, class, plain)
			case !slices.ContainsFunc(allowed, func(err error) bool { return errors.Is(plainErr, err) }):
				fail("document %.120s: class %s, the port refuses with %v, which the class does not name", docField, class, plainErr)
			default:
				outcomes["Python accepts, the port refuses (class "+class+")"]++
			}
			continue
		}
		wantPlain := decode(fields[3])
		wantState := wantPlain
		if fields[4] != "=" {
			wantState = decode(fields[4])
		}
		switch {
		case plainErr != nil:
			fail("document %.120s: Python accepts, the port refuses with %v", docField, plainErr)
		case !bytes.Equal(plain, wantPlain):
			fail("document %.120s: PydanticJSON = %.200q, Python %.200q", docField, plain, wantPlain)
		case !bytes.Equal(state, wantState):
			fail("document %.120s: EncodeState = %.200q, Python %.200q", docField, state, wantState)
		default:
			outcomes["equal bytes"]++
		}
	}
	t.Logf("documents: %d; rows %v; outcomes %v; failures %d", len(lines), rows, outcomes, failures)

	wantRows := map[string]int{
		"ok / -":                                                      1302,
		"ok / nan-token":                                              9,
		"ok / nan-token+replaced-lone-surrogate":                      1,
		"ok / replaced-lone-surrogate":                                6,
		"ok / read-depth":                                             2,
		"decode:UnicodeDecodeError / -":                               15,
		"decode:UnicodeDecodeError / two-defects":                     2,
		"loads:JSONDecodeError / -":                                   88,
		"loads:JSONDecodeError / two-defects":                         1,
		"loads:ValueError / -":                                        6,
		"loads:ValueError / two-defects":                              5,
		"to_json:PydanticSerializationError:UnicodeEncodeError / -":   22,
		"to_json:PydanticSerializationError:ValueError / -":           35,
		"to_json:PydanticSerializationError:ValueError / two-defects": 6,
	}
	if diff := gocmp.Diff(wantRows, rows); diff != "" {
		t.Errorf("documents per verdict and class (-want +got):\n%s", diff)
	}
	wantOutcomes := map[string]int{
		"equal bytes":                                                                1302,
		"refused by both, the same kind":                                             167,
		"refused by both, another kind (two-defects)":                                13,
		"Python accepts, the port refuses (class nan-token)":                         9,
		"Python accepts, the port refuses (class nan-token+replaced-lone-surrogate)": 1,
		"Python accepts, the port refuses (class replaced-lone-surrogate)":           6,
		"Python accepts, the port refuses (class read-depth)":                        2,
	}
	if diff := gocmp.Diff(wantOutcomes, outcomes); diff != "" {
		t.Errorf("outcomes (-want +got):\n%s", diff)
	}
	if failures > 0 {
		t.Errorf("%d of %d documents are not explained by Python's verdict and the class", failures, len(lines))
	}
}

// unknownRefusal is what refusalKind returns for an error that is none of
// the package's refusals.
const unknownRefusal = "an error that is none of the package's refusals"

// refusalKind returns the name of the package's refusal err is, "" for no
// error, and unknownRefusal for any other error.
func refusalKind(err error) string {
	if err == nil {
		return ""
	}
	kinds := map[string]error{"ErrSyntax": ErrSyntax, "ErrDepth": ErrDepth, "ErrReadDepth": ErrReadDepth, "ErrIntDigits": ErrIntDigits}
	for name, kind := range kinds {
		if errors.Is(err, kind) {
			return name
		}
	}
	return unknownRefusal
}

// hasNonFinite reports whether a read document, after repeated names are
// resolved, holds a number literal that PydanticJSON writes as Infinity or
// -Infinity: a float literal past the largest double. The reader refuses
// the NaN token, so no other value that is not finite can be in the tree.
func hasNonFinite(n *node) bool {
	switch n.kind {
	case kindNumber:
		return !isIntLiteral(n.text) && math.IsInf(numberFloat(n.text), 0)
	case kindArray, kindObject:
		return slices.ContainsFunc(n.elems, func(e node) bool { return hasNonFinite(&e) })
	}
	return false
}

// FuzzStateEncode checks the state re-encoding on any input:
//
//   - it never panics;
//   - EncodeState and PydanticJSON refuse together or accept together, and a
//     refusal is one of ErrSyntax, ErrDepth, ErrReadDepth and ErrIntDigits;
//   - when they accept and the output holds no value that is not finite, the
//     output of each is valid JSON, PydanticJSON of PydanticJSON's output is
//     the same bytes, and EncodeState's output equals PydanticJSON's as a
//     value (its escapes of '<' and '>' read back as '<' and '>');
//   - when the output holds Infinity or -Infinity, which PydanticJSON writes
//     for a float literal past the largest double as pydantic-core does,
//     reading the output again refuses with ErrSyntax, and with nothing else.
//
// Whether the output holds such a value is read from the document's tree,
// not from the output's bytes. The seed corpus is every document of the
// committed state vectors, which covers each verdict and class.
func FuzzStateEncode(f *testing.F) {
	data, err := readSeedDocuments()
	if err != nil {
		f.Fatal(err)
	}
	for _, doc := range data {
		f.Add(doc)
	}
	f.Fuzz(func(t *testing.T, doc []byte) {
		plain, plainErr := PydanticJSON(doc)
		state, stateErr := EncodeState(doc)
		kind := refusalKind(plainErr)
		if kind != refusalKind(stateErr) {
			t.Fatalf("PydanticJSON error %v, EncodeState error %v", plainErr, stateErr)
		}
		if plainErr != nil {
			if kind == unknownRefusal {
				t.Fatalf("refusal %v is none of the package's errors", plainErr)
			}
			return
		}
		root, err := read(doc)
		if err != nil {
			t.Fatalf("PydanticJSON accepts a document read refuses: %v", err)
		}
		again, againErr := PydanticJSON(plain)
		if hasNonFinite(&root) {
			if !errors.Is(againErr, ErrSyntax) {
				t.Fatalf("output %.200q holds a value that is not finite; reading it again gives %.200q, %v, want ErrSyntax", plain, again, againErr)
			}
			return
		}
		if !jsontext.Value(plain).IsValid() {
			t.Fatalf("PydanticJSON output %.200q is not valid JSON", plain)
		}
		if !jsontext.Value(state).IsValid() {
			t.Fatalf("EncodeState output %.200q is not valid JSON", state)
		}
		if againErr != nil || !bytes.Equal(again, plain) {
			t.Fatalf("PydanticJSON is not idempotent: %.200q gives %.200q, %v", plain, again, againErr)
		}
		equal, err := EqualOrdered(state, plain)
		if err != nil || !equal {
			t.Fatalf("EncodeState output %.200q does not equal PydanticJSON output %.200q as a value: %v", state, plain, err)
		}
	})
}

// readSeedDocuments returns the documents of the committed state vectors,
// read without the header checks of TestStateMatchesPydantic.
func readSeedDocuments() ([][]byte, error) {
	_, _, lines, err := splitVectorFile(stateVectorsPath)
	if err != nil {
		return nil, err
	}
	var docs [][]byte
	for _, line := range lines {
		field, _, _ := strings.Cut(line, "\t")
		doc, err := base64.StdEncoding.DecodeString(field)
		if err != nil {
			return nil, fmt.Errorf("%s: bad base64 %.40q: %w", stateVectorsPath, field, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}
