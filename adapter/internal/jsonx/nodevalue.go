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

import "math"

// Value returns v as a Value for Marshal, so that a value kept from a read
// document is written inside a body Marshal writes. With style Repr it is
// the response body's spelling of the Python value json.loads made of v, as
// json.dumps writes what pydantic's model_dump(mode="json") returns for it:
// the spelling of a score's legend (system-one-adapter-python v0.2.1,
// src/system_one_adapter/_client.py:147), which holds the question's
// criteria.
//
// Members keep their order, a repeated member name its first position and
// its last value, as Read resolved them. An integer literal is written as
// its value (-0 as 0); any other number in style; strings as String
// writes them. A number literal beyond the range of a double (1e400), of
// which Python makes an infinity, is written null in both styles, while
// Node.PydanticJSON writes it Infinity: upstream's legend passes through
// model_dump(mode="json"), which turns an infinity into None, before
// json.dumps writes the response, so the legend holds null where
// pydantic-core's own to_json would write Infinity.
//
// The depth it counts starts at v, as Node.PydanticJSON counts it, and it
// refuses a value nested deeper than 255 levels with ErrDepth.
func (v Node) Value(style FloatStyle) (Value, error) {
	if v.n == nil {
		return Value{}, nil
	}
	return nodeValue(v.n, style, 1)
}

// nodeValue converts n, the depth-th level of the value being converted.
func nodeValue(n *node, style FloatStyle, depth int) (Value, error) {
	if depth > maxValueDepth {
		return Value{}, ErrDepth
	}
	switch n.kind {
	case kindNull:
		return Value{}, nil
	case kindFalse:
		return Bool(false), nil
	case kindTrue:
		return Bool(true), nil
	case kindNumber:
		if isIntLiteral(n.text) {
			if n.text == "-0" {
				return Number("0"), nil // int("-0") is 0
			}
			return Number(n.text), nil
		}
		f := numberFloat(n.text)
		if math.IsInf(f, 0) {
			return Value{}, nil
		}
		return Float(f, style), nil
	case kindString:
		return String(n.text), nil
	case kindArray:
		elems := make([]Value, len(n.elems))
		for i := range n.elems {
			e, err := nodeValue(&n.elems[i], style, depth+1)
			if err != nil {
				return Value{}, err
			}
			elems[i] = e
		}
		return Array(elems...), nil
	default: // kindObject
		members := make([]Member, len(n.elems))
		for i := range n.elems {
			e, err := nodeValue(&n.elems[i], style, depth+1)
			if err != nil {
				return Value{}, err
			}
			members[i] = Member{Name: n.names[i], Value: e}
		}
		return Object(members...), nil
	}
}
