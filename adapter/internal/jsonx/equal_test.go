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
	"errors"
	"testing"
)

func TestEqual(t *testing.T) {
	tests := map[string]struct {
		a, b    string
		equal   bool // Equal: member order ignored
		ordered bool // EqualOrdered: member order kept
		err     error
	}{
		"success: the same text":                            {a: `{"a":[1,"x",null]}`, b: `{"a":[1,"x",null]}`, equal: true, ordered: true},
		"success: white space does not count":               {a: `{ "a" : [ 1 ] }`, b: `{"a":[1]}`, equal: true, ordered: true},
		"success: member order":                             {a: `{"a":1,"b":2}`, b: `{"b":2,"a":1}`, equal: true, ordered: false},
		"success: member order in a nested object":          {a: `[{"x":{"a":1,"b":2}}]`, b: `[{"x":{"b":2,"a":1}}]`, equal: true, ordered: false},
		"success: element order counts":                     {a: `[1,2]`, b: `[2,1]`, equal: false, ordered: false},
		"success: a repeated name keeps its last value":     {a: `{"a":1,"b":2,"a":3}`, b: `{"a":3,"b":2}`, equal: true, ordered: true},
		"success: a repeated name keeps its first position": {a: `{"a":1,"b":2,"a":3}`, b: `{"b":2,"a":3}`, equal: true, ordered: false},
		"success: a missing member":                         {a: `{"a":1}`, b: `{"a":1,"b":2}`, equal: false, ordered: false},
		"success: another member name":                      {a: `{"a":1}`, b: `{"b":1}`, equal: false, ordered: false},
		"success: an integer equals a float":                {a: `[1]`, b: `[1.0]`, equal: true, ordered: true},
		"success: float spellings":                          {a: `[1e-05, 100000.0, 1.50]`, b: `[0.00001, 1E5, 1.5]`, equal: true, ordered: true},
		"success: negative zero equals zero":                {a: `[-0.0, -0]`, b: `[0.0, 0]`, equal: true, ordered: true},
		"success: a large integer and the nearest double":   {a: `9007199254740993`, b: `9007199254740992.0`, equal: false, ordered: false},
		"success: a large integer and its double":           {a: `9007199254740992`, b: `9007199254740992.0`, equal: true, ordered: true},
		"success: two integers past a double":               {a: `12345678901234567890123`, b: `12345678901234567890124`, equal: false, ordered: false},
		"success: two floats that round alike":              {a: `0.1000000000000000000001`, b: `0.1`, equal: true, ordered: true},
		"success: overflowed floats":                        {a: `1e400`, b: `1e999`, equal: true, ordered: true},
		"success: an integer and an overflowed float":       {a: `1`, b: `1e400`, equal: false, ordered: false},
		"success: strings by their text":                    {a: "\"\x5cu00e9\x5cn\"", b: "\"\xc3\xa9\x5cn\"", equal: true, ordered: true},
		"success: a string is not a number":                 {a: `"1"`, b: `1`, equal: false, ordered: false},
		"success: false is not null":                        {a: `false`, b: `null`, equal: false, ordered: false},
		"success: true is not false":                        {a: `true`, b: `false`, equal: false, ordered: false},
		"success: arrays of another length":                 {a: `[1]`, b: `[1,1]`, equal: false, ordered: false},
		"error: the first text is not JSON":                 {a: `[1,]`, b: `[1]`, err: ErrSyntax},
		"error: the second text is not JSON":                {a: `[1]`, b: `NaN`, err: ErrSyntax},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Equal([]byte(tt.a), []byte(tt.b))
			if !errors.Is(err, tt.err) || (tt.err == nil) != (err == nil) {
				t.Fatalf("Equal error = %v, want %v", err, tt.err)
			}
			if got != tt.equal {
				t.Errorf("Equal(%s, %s) = %v, want %v", tt.a, tt.b, got, tt.equal)
			}
			got, err = EqualOrdered([]byte(tt.a), []byte(tt.b))
			if !errors.Is(err, tt.err) || (tt.err == nil) != (err == nil) {
				t.Fatalf("EqualOrdered error = %v, want %v", err, tt.err)
			}
			if got != tt.ordered {
				t.Errorf("EqualOrdered(%s, %s) = %v, want %v", tt.a, tt.b, got, tt.ordered)
			}
		})
	}
}
