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
	"strings"
	"testing"
	"unicode/utf8"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
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

// TestRenderFieldPath checks how a field path is printed (NF7): the Python
// SDK's dotted field_path, "." for the root, each name the server chose
// escaped with its backslashes doubled and cut at 128 characters, and the
// whole cut at 320.
func TestRenderFieldPath(t *testing.T) {
	const ell = "…"
	long := strings.Repeat("n", 300)
	tests := map[string]struct {
		path codec.FieldPath
		want string
	}{
		"success: the root":            {path: codec.FieldPath{}, want: "."},
		"success: a top-level member":  {path: codec.FieldPath{Top: "model"}, want: "model"},
		"success: a usage member":      {path: codec.FieldPath{Top: "usage", Member: "input_tokens"}, want: "usage.input_tokens"},
		"success: an answer's member":  {path: codec.FieldPath{Top: "answers", Name: "tone", HasName: true, Member: "confidence"}, want: "answers.tone.confidence"},
		"success: a legend key":        {path: codec.FieldPath{Top: "answers", Name: "s", HasName: true, Member: "legend", Key: "x", HasKey: true}, want: "answers.s.legend.x"},
		"success: a model card member": {path: codec.FieldPath{Top: "models", Index: 1, HasIndex: true, Member: "name"}, want: "models[1].name"},
		"success: a model card":        {path: codec.FieldPath{Top: "models", Index: 0, HasIndex: true}, want: "models[0]"},
		"success: an empty name":       {path: codec.FieldPath{Top: "answers", Name: "", HasName: true, Member: "type"}, want: "answers..type"},
		"success: names escaped, backslashes doubled": {
			path: codec.FieldPath{Top: "answers", Name: "a\nb\x1b\\", HasName: true, Member: "probabilities", Key: "k\t\u202e", HasKey: true},
			want: `answers.a\nb\x1b\\.probabilities.k\t\u202e`,
		},
		"success: a long name cut at 128": {
			path: codec.FieldPath{Top: "answers", Name: long, HasName: true, Member: "noul"},
			want: "answers." + long[:128] + ell + ".noul",
		},
		"success: the deepest path, both names cut, within 320": {
			path: codec.FieldPath{Top: "answers", Name: long, HasName: true, Member: "probabilities", Key: long, HasKey: true},
			// 8 + 129 + 15 + 129 = 281 characters: the path's own cap of 320
			// holds both names at theirs, as the Rust port sized it.
			want: "answers." + long[:128] + ell + ".probabilities." + long[:128] + ell,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := renderFieldPath(tt.path)
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("renderFieldPath (-want +got):\n%s", diff)
			}
			if n := utf8.RuneCountInString(got); n > maxPathChars+1 {
				t.Errorf("rendered path has %d characters, want at most %d", n, maxPathChars+1)
			}
		})
	}
}
