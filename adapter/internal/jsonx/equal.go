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

import "math/big"

// Equal reports whether the JSON texts a and b hold the same value, with the
// order of object members ignored at every level, as Python compares the
// values json.loads makes of them: a repeated member name keeps its last
// value; numbers are equal when their values are, an integer and a float
// included (1 equals 1.0, and 9007199254740993 does not equal
// 9007199254740992.0); a float compares with == (-0.0 equals 0.0). It
// returns the refusal of PydanticJSON when either text is not one it reads.
//
// Tests use it where a value is compared regardless of member order, such as
// a schema or a provider request body.
func Equal(a, b []byte) (bool, error) {
	return equalTexts(a, b, false)
}

// EqualOrdered is Equal with the order of object members kept: two objects
// are equal only when their members, after repeated names are resolved, have
// the same names in the same order and equal values.
func EqualOrdered(a, b []byte) (bool, error) {
	return equalTexts(a, b, true)
}

func equalTexts(a, b []byte, ordered bool) (bool, error) {
	x, err := read(a)
	if err != nil {
		return false, err
	}
	y, err := read(b)
	if err != nil {
		return false, err
	}
	return equalNodes(&x, &y, ordered), nil
}

func equalNodes(x, y *node, ordered bool) bool {
	if x.kind != y.kind {
		return false
	}
	switch x.kind {
	case kindNumber:
		return equalNumbers(x.text, y.text)
	case kindString:
		return x.text == y.text
	case kindArray:
		if len(x.elems) != len(y.elems) {
			return false
		}
		for i := range x.elems {
			if !equalNodes(&x.elems[i], &y.elems[i], ordered) {
				return false
			}
		}
		return true
	case kindObject:
		if len(x.names) != len(y.names) {
			return false
		}
		if ordered {
			for i := range x.names {
				if x.names[i] != y.names[i] || !equalNodes(&x.elems[i], &y.elems[i], ordered) {
					return false
				}
			}
			return true
		}
		index := make(map[string]int, len(y.names))
		for i, name := range y.names {
			index[name] = i
		}
		for i, name := range x.names {
			j, ok := index[name]
			if !ok || !equalNodes(&x.elems[i], &y.elems[j], ordered) {
				return false
			}
		}
		return true
	default: // null, false, true
		return true
	}
}

// equalNumbers compares two JSON number literals by value, as Python
// compares the int or float json.loads makes of each: two ints exactly, two
// floats with ==, and an int with a float exactly.
func equalNumbers(a, b string) bool {
	aInt, bInt := isIntLiteral(a), isIntLiteral(b)
	switch {
	case aInt && bInt:
		x, _ := new(big.Int).SetString(a, 10)
		y, _ := new(big.Int).SetString(b, 10)
		return x.Cmp(y) == 0
	case !aInt && !bInt:
		return numberFloat(a) == numberFloat(b)
	case aInt:
		return equalIntFloat(a, numberFloat(b))
	default:
		return equalIntFloat(b, numberFloat(a))
	}
}

// equalIntFloat reports whether the integer literal i and the float f have
// the same value. A float from a JSON literal is never NaN.
func equalIntFloat(i string, f float64) bool {
	x, _ := new(big.Int).SetString(i, 10)
	return new(big.Float).SetInt(x).Cmp(big.NewFloat(f)) == 0
}
