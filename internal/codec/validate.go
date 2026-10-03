//go:build !go1.28 && (amd64 || arm64)

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

package codec

import "unicode/utf8"

// ValidString reports whether s is valid UTF-8 and free of the raw control
// characters U+0000 to U+001F, which JSON forbids inside a string and the
// Python SDK's parser rejects. sonic accepts both (invalid UTF-8 and raw
// control characters pass every sonic path), so the response decoder runs
// this check on the strings it is handed. It makes one pass over s and
// allocates nothing.
//
// s must be the raw text between a JSON string's quotes, before escape
// sequences are decoded. sonic's ast.Visitor delivers exactly that for a
// string without escape sequences; for a string with one it delivers the
// decoded value, in which a legal escape such as \n or \u0000 has become a
// control character that this check would reject. The caller keeps the two
// cases apart.
func ValidString(s string) bool {
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x20 {
			return false
		}
		if c < utf8.RuneSelf {
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return false
		}
		i += size
	}
	return true
}

// validUTF8Portable reports whether b is valid UTF-8 with Go's own validator,
// utf8.Valid. validUTF8, the check of an encoded state and of every other
// member of a request body before the body leaves the process, is chosen per
// architecture: on amd64 it is sonic's SIMD validator, 2 to 3 times
// utf8.Valid's speed on ASCII and about 9 times on CJK text; on arm64 it is
// this function, since sonic's validator reads ASCII 15 to 22 times slower
// there. It lives in a file of every architecture so that amd64's tests hold
// sonic's validator to it (TestValidUTF8Parity, FuzzValidUTF8).
func validUTF8Portable(b []byte) bool { return utf8.Valid(b) }
