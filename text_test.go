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

package typesafe

import (
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// TestAppendSafeText pins the escape and cut of text the SDK did not write
// (NF7, ruling R58), the rules of the Rust port's src/text.rs in Go escapes.
func TestAppendSafeText(t *testing.T) {
	tests := map[string]struct {
		s      string
		limit  int
		double bool
		want   string
	}{
		"success: printable text, non-ASCII and U+FFFD as they are": {s: "h\u00e9llo \u4e16\u754c \ufffd <>&\"'", limit: 200, want: "h\u00e9llo \u4e16\u754c \ufffd <>&\"'"},
		"success: line feed, carriage return and tab":               {s: "a\nb\rc\td", limit: 200, want: `a\nb\rc\td`},
		"success: other controls, DEL and C1 as Go escapes":         {s: "\x00\a\x1b\x7f\u0085\u009f", limit: 200, want: `\x00\a\x1b\x7f\u0085\u009f`},
		"success: bytes that are not UTF-8":                         {s: "a\xffb\xed\xa0\x80", limit: 200, want: `a\xffb\xed\xa0\x80`},
		"success: format characters that hide or reorder text": {
			s:     "\u00ad\u061c\u180e\u200b\u200f\u2028\u2029\u202e\u2060\u2066\ufeff\ufff9\U000e0041",
			limit: 200,
			want:  `\u00ad\u061c\u180e\u200b\u200f\u2028\u2029\u202e\u2060\u2066\ufeff\ufff9\U000e0041`,
		},
		"success: a sentence keeps its backslashes":   {s: `say \"hi\" \x1b`, limit: 200, want: `say \"hi\" \x1b`},
		"success: a name doubles its backslashes":     {s: `k\x1b`, limit: 200, double: true, want: `k\\x1b`},
		"success: exactly at the limit, no ellipsis":  {s: "abcde", limit: 5, want: "abcde"},
		"success: past the limit, cut with U+2026":    {s: "abcdef", limit: 5, want: "abcde\u2026"},
		"success: an escape is never split":           {s: "abcd\n", limit: 5, want: "abcd\u2026"},
		"success: an invalid byte's escape is whole":  {s: "ab\xff", limit: 5, want: "ab\u2026"},
		"success: a multi-byte character counts once": {s: "\u4e16\u754c\u4eba\u6c11", limit: 3, want: "\u4e16\u754c\u4eba\u2026"},
		"success: empty text":                         {s: "", limit: 5, want: ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := string(appendSafeText([]byte("<"), tt.s, tt.limit, tt.double))
			if diff := gocmp.Diff("<"+tt.want, got); diff != "" {
				t.Errorf("appendSafeText (-want +got):\n%s", diff)
			}
		})
	}
}
