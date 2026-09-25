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
// limitations under the License.

package codec

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// referenceValid is ValidString written the obvious way, rune by rune, for the
// differential checks below.
func referenceValid(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 })
}

func TestValidString(t *testing.T) {
	tests := map[string]struct {
		s    string
		want bool
	}{
		"success: empty": {s: "", want: true},
		"success: ASCII": {s: "Is this about billing?", want: true},
		"success: space is the first allowed byte":            {s: " ", want: true},
		"success: DEL is not a JSON control character":        {s: "\x7f", want: true},
		"success: two-byte sequence":                          {s: "café", want: true},
		"success: three-byte sequence":                        {s: "日本語", want: true},
		"success: four-byte sequence":                         {s: "\U0001F600", want: true},
		"success: C1 control U+0080 is allowed by JSON":       {s: "\u0080", want: true},
		"success: U+2028 line separator":                      {s: " ", want: true},
		"success: U+FFFD stays valid":                         {s: "�", want: true},
		"success: largest code point U+10FFFF":                {s: "\U0010FFFF", want: true},
		"success: escape text is plain ASCII before decoding": {s: `a\nb\u0000c`, want: true},
		"error: NUL": {s: "\x00", want: false},
		"error: U+001F is the last control character": {s: "\x1f", want: false},
		"error: raw tab":                                  {s: "a\tb", want: false},
		"error: raw newline":                              {s: "a\nb", want: false},
		"error: raw carriage return":                      {s: "\r", want: false},
		"error: control character after a multibyte rune": {s: "é\x01", want: false},
		"error: control character at the end of ASCII":    {s: "billing\x00", want: false},
		"error: lone continuation byte":                   {s: "\x80", want: false},
		"error: invalid lead byte":                        {s: "\xff", want: false},
		"error: truncated three-byte sequence":            {s: "\xe6\x97", want: false},
		"error: truncated sequence before ASCII":          {s: "\xe6\x97a", want: false},
		"error: overlong encoding of '/'":                 {s: "\xc0\xaf", want: false},
		"error: overlong encoding of NUL":                 {s: "\xc0\x80", want: false},
		"error: UTF-8 encoded surrogate U+D800":           {s: "\xed\xa0\x80", want: false},
		"error: code point past U+10FFFF":                 {s: "\xf4\x90\x80\x80", want: false},
		"error: invalid byte after valid text":            {s: "valid\xfe", want: false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := ValidString(tt.s); got != tt.want {
				t.Errorf("ValidString(%q) = %t, want %t", tt.s, got, tt.want)
			}
			if ref := referenceValid(tt.s); ref != tt.want {
				t.Fatalf("test case disagrees with the reference: referenceValid(%q) = %t", tt.s, ref)
			}
		})
	}
}

// TestValidStringExhaustiveShort compares ValidString with the reference on
// every string of one and two bytes, which covers every lead byte, every
// control byte and every short invalid sequence.
func TestValidStringExhaustiveShort(t *testing.T) {
	var buf [2]byte
	for a := range 256 {
		buf[0] = byte(a)
		if s := string(buf[:1]); ValidString(s) != referenceValid(s) {
			t.Fatalf("ValidString(%q) = %t, reference says %t", s, ValidString(s), referenceValid(s))
		}
		for b := range 256 {
			buf[1] = byte(b)
			if s := string(buf[:2]); ValidString(s) != referenceValid(s) {
				t.Fatalf("ValidString(%q) = %t, reference says %t", s, ValidString(s), referenceValid(s))
			}
		}
	}
}

func TestValidStringAllocatesNothing(t *testing.T) {
	s := strings.Repeat("Is this about billing, invoices or refunds? 日本語 ", 64)
	if allocs := testing.AllocsPerRun(100, func() { _ = ValidString(s) }); allocs != 0 {
		t.Fatalf("ValidString allocated %v times per call, want 0", allocs)
	}
}

func FuzzValidString(f *testing.F) {
	for _, seed := range []string{"", "a", "\x00", "\x1f", " ", "é", "\xff", "\xed\xa0\x80", "\xc0\x80", "a\tb", "\U0010FFFF"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := ValidString(s), referenceValid(s); got != want {
			t.Fatalf("ValidString(%q) = %t, reference says %t", s, got, want)
		}
	})
}
