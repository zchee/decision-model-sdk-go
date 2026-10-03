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

package jsonx

import (
	"errors"
	"math"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

func TestMarshal(t *testing.T) {
	tests := map[string]struct {
		in   Value
		want string
		err  error
	}{
		"success: the zero Value is null": {in: Value{}, want: `null`},
		"success: members keep the order given": {
			in: Object(
				Member{Name: "z", Value: Number("1")},
				Member{Name: "a", Value: Bool(true)},
				Member{Name: "m", Value: Value{}},
			),
			want: `{"z":1,"a":true,"m":null}`,
		},
		"success: nested arrays and objects": {
			in: Array(
				Object(Member{Name: "k", Value: Array()}),
				Object(),
				Array(String("x"), Bool(false)),
			),
			want: `[{"k":[]},{},["x",false]]`,
		},
		"success: a number keeps its text": {
			in:   Array(Number("12345678901234567890123"), Number("1.50"), Number("1E5"), Number("-0")),
			want: `[12345678901234567890123,1.50,1E5,-0]`,
		},
		"success: floats in both styles": {
			in: Array(
				Float(1e-5, Pydantic), Float(1e-5, Repr),
				Float(1e16, Pydantic), Float(1e16, Repr),
				Float(3, Pydantic), Float(math.Copysign(0, -1), Repr),
			),
			want: `[0.00001,1e-05,1e+16,1e+16,3.0,-0.0]`,
		},
		"success: control characters are escaped": {
			in:   String("a\x00\x01\x1f\t\n\"\\b"),
			want: `"a\u0000\u0001\u001f\t\n\"\\b"`,
		},
		"success: '<', '>', '&' and U+2028 are written as they are": {
			in:   String("<a>&\u2028"),
			want: "\"<a>&\u2028\"",
		},
		"success: invalid UTF-8 is replaced": {
			in:   Object(Member{Name: "k\xff", Value: String("a\xffb\xed\xa0\x80")}),
			want: "{\"k\ufffd\":\"a\ufffdb\ufffd\ufffd\ufffd\"}",
		},
		"success: raw JSON loses its white space, numbers keep their spelling": {
			in:   Object(Member{Name: "schema", Value: Raw([]byte(" { \"b\" : [ 1.50 , \"\\u00e9\" ] ,\n \"a\" : null } "))}),
			want: "{\"schema\":{\"b\":[1.50,\"\xc3\xa9\"],\"a\":null}}",
		},
		"error: NaN":                                {in: Float(math.NaN(), Repr), err: ErrNonFinite},
		"error: an infinity":                        {in: Array(Float(math.Inf(1), Pydantic)), err: ErrNonFinite},
		"error: a negative infinity":                {in: Object(Member{Name: "x", Value: Float(math.Inf(-1), Repr)}), err: ErrNonFinite},
		"error: a number that is not a JSON number": {in: Number("01"), err: ErrInvalid},
		"error: a number with white space":          {in: Number(" 1"), err: ErrInvalid},
		"error: a number that is two numbers":       {in: Number("1 2"), err: ErrInvalid},
		"error: NaN as a number":                    {in: Number("NaN"), err: ErrInvalid},
		"error: an empty number":                    {in: Number(""), err: ErrInvalid},
		"error: two members of one name": {
			in:  Object(Member{Name: "a", Value: Number("1")}, Member{Name: "a", Value: Number("2")}),
			err: ErrInvalid,
		},
		"error: raw bytes that are not JSON":         {in: Raw([]byte(`{"a":}`)), err: ErrInvalid},
		"error: raw bytes with a repeated name":      {in: Raw([]byte(`{"a":1,"a":2}`)), err: ErrInvalid},
		"error: raw bytes that are not UTF-8":        {in: Raw([]byte("\"\xff\"")), err: ErrInvalid},
		"error: raw bytes with a NaN token":          {in: Raw([]byte(`[NaN]`)), err: ErrInvalid},
		"error: raw bytes with a raw control byte":   {in: Raw([]byte("\"\x01\"")), err: ErrInvalid},
		"error: raw bytes holding two values":        {in: Raw([]byte(`1 2`)), err: ErrInvalid},
		"error: empty raw bytes":                     {in: Raw(nil), err: ErrInvalid},
		"error: nesting past the library's limit":    {in: deepArray(10001), err: ErrInvalid},
		"success: nesting at the library's limit":    {in: deepArray(10000), want: strings.Repeat("[", 10000) + strings.Repeat("]", 10000)},
		"error: a non-finite float deep in an array": {in: Array(Array(Array(Float(math.NaN(), Pydantic)))), err: ErrNonFinite},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Marshal(tt.in)
			if !errors.Is(err, tt.err) || (tt.err == nil) != (err == nil) {
				t.Fatalf("Marshal error = %v, want %v", err, tt.err)
			}
			if diff := gocmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("Marshal mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// deepArray returns n arrays nested inside one another.
func deepArray(n int) Value {
	v := Array()
	for range n - 1 {
		v = Array(v)
	}
	return v
}
