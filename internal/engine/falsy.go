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

package engine

import "github.com/zchee/typesafe-sdk-go/internal/wire"

// FalsyJSON reports whether the JSON value raw is one Python finds false:
// null, false, "", [], {} or a number equal to zero. Invalid JSON is not
// falsy; writing it reports the problem.
func FalsyJSON(raw []byte) bool {
	if !maybeFalsyJSON(raw) {
		return false
	}
	// A candidate, the question's rejection path: maybeFalsyJSON let
	// through only values that are falsy when valid, so the verdict is
	// whether the whole value is valid JSON.
	var buf [32]byte
	_, err := wire.AppendJSON(buf[:0], raw)
	return err == nil
}

// maybeFalsyJSON reports whether raw can be one of FalsyJSON's falsy values,
// from its first bytes alone: a value that is valid JSON starts, after
// whitespace, as its compact form does, and no whitespace can stand inside a
// string or a number. So a value that starts with t, with a string of at
// least one character, with an array or object whose next non-space byte does
// not close it, or with a number that has a digit from 1 to 9 before its
// exponent, is not falsy whether or not the rest is valid, and FalsyJSON
// returns false without reading or copying the rest. That is the success path
// of a raw score question.
func maybeFalsyJSON(raw []byte) bool {
	i := skipJSONSpace(raw, 0)
	if i == len(raw) {
		return false
	}
	switch c := raw[i]; c {
	case 'n', 'f':
		return true
	case '"':
		return i+1 < len(raw) && raw[i+1] == '"'
	case '[':
		j := skipJSONSpace(raw, i+1)
		return j < len(raw) && raw[j] == ']'
	case '{':
		j := skipJSONSpace(raw, i+1)
		return j < len(raw) && raw[j] == '}'
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		for _, c := range raw[i:] {
			switch {
			case c == 'e' || c == 'E':
				return true
			case '1' <= c && c <= '9':
				return false
			}
		}
		return true
	default:
		return false
	}
}

// skipJSONSpace returns the index of the first byte of raw at or after i
// that is not JSON whitespace, or len(raw).
func skipJSONSpace(raw []byte, i int) int {
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t' || raw[i] == '\n' || raw[i] == '\r') {
		i++
	}
	return i
}
