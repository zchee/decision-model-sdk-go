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

package codec

// The raw control-character rule of ruling R23. sonic delivers a string
// without escape sequences as the raw text between its quotes and a string
// with one decoded, and it accepts a raw byte below 0x20 inside a string on
// every path (Preorder, Skip, ValidString, UnmarshalString). A delivered
// string holding such a byte therefore came either from a legal escape such
// as \n, or from the raw byte, which JSON forbids and the Python SDK's parser
// rejects. Only the body can tell the two apart, so the first delivered
// string that holds one makes the decoder ask the body once: [hasControlByte]
// finds whether the body holds any byte below 0x20 at all (a compact body
// holds none, so a legal escape costs one word-at-a-time pass), and only then
// [controlInString] tracks string boundaries to find one inside a string.

const (
	swarOnes = 0x0101010101010101
	swarHigh = 0x8080808080808080
)

// hasControlByte reports whether s holds any byte below 0x20, eight bytes at
// a time.
func hasControlByte(s string) bool {
	i := 0
	for ; i+8 <= len(s); i += 8 {
		b := s[i : i+8]
		// The compiler merges these byte loads into one 64-bit load.
		x := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
			uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
		// A byte below 0x20 borrows from its own high bit when 0x20 is
		// subtracted, and a byte with the high bit already set is masked out
		// by &^x, so the result is non-zero exactly when some byte is below
		// 0x20.
		if (x-0x20*swarOnes)&^x&swarHigh != 0 {
			return true
		}
	}
	for ; i < len(s); i++ {
		if s[i] < 0x20 {
			return true
		}
	}
	return false
}

// controlInString reports whether a raw byte below 0x20 sits inside a string
// of body. It relies on sonic having parsed body already: escapes are well
// formed, so a backslash inside a string consumes the next byte, and a quote
// outside an escape toggles the state.
func controlInString(body string) bool {
	in := false
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case c == '"':
			in = !in
		case !in:
		case c == '\\':
			i++
		case c < 0x20:
			return true
		}
	}
	return false
}

// rawControlInString is the body half of the rule: the word-at-a-time test
// first, and the string-tracking scan only when the body holds a byte below
// 0x20 somewhere.
func rawControlInString(body string) bool {
	return hasControlByte(body) && controlInString(body)
}
