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
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// utf8Classes are the rune classes the UTF-8 parity checks place at every
// offset around sonic's 32-byte blocks: valid runes of each width and the
// invalid forms, among them a lone surrogate and a cut sequence (sonic's
// native scanner has a 32-byte boundary bug, so the offsets matter).
var utf8Classes = map[string][]byte{
	"two-byte rune":       []byte("é"),
	"three-byte rune":     []byte("請"),
	"four-byte rune":      []byte("😀"),
	"lone surrogate":      {0xed, 0xa0, 0x80},
	"overlong slash":      {0xc0, 0xaf},
	"overlong three":      {0xe0, 0x80, 0xaf},
	"past U+10FFFF":       {0xf4, 0x90, 0x80, 0x80},
	"cut three-byte rune": {0xe8, 0xab},
	"cut four-byte rune":  {0xf0, 0x9f, 0x98},
	"stray continuation":  {0x80},
	"invalid lead":        {0xff},
}

// TestValidUTF8Parity holds validUTF8, the request body's UTF-8 check, to
// validUTF8Portable, Go's utf8.Valid: on amd64 validUTF8 is sonic's SIMD
// validator (validate_amd64.go) and this is the differential that lets the
// per-architecture choice keep the check's verdicts; on arm64 validUTF8 is
// validUTF8Portable itself (validate_arm64.go) and the test pins that. The
// inputs: nil and empty, every string of up to three bytes, the four-byte
// lead bytes with continuation bytes at the edges of their ranges, and each
// rune class of utf8Classes at every offset from 0 to 69 in ASCII, followed
// by nothing, ASCII, CJK or more ASCII.
func TestValidUTF8Parity(t *testing.T) {
	t.Logf("GOARCH %s: validUTF8 is %s", runtime.GOARCH, map[bool]string{true: "sonic's utf8.Validate", false: "utf8.Valid"}[runtime.GOARCH == "amd64"])
	checks := 0
	check := func(b []byte) {
		t.Helper()
		checks++
		if got, want := validUTF8(b), validUTF8Portable(b); got != want {
			t.Fatalf("validUTF8(% x) = %t, utf8.Valid says %t", b, got, want)
		}
	}
	check(nil)
	check([]byte{})
	var b [3]byte
	for x := range 1 << 8 {
		b[0] = byte(x)
		check(b[:1])
	}
	for x := range 1 << 16 {
		b[0], b[1] = byte(x>>8), byte(x)
		check(b[:2])
	}
	for x := range 1 << 24 {
		b[0], b[1], b[2] = byte(x>>16), byte(x>>8), byte(x)
		check(b[:3])
	}
	edge := []byte{0x00, 0x7f, 0x80, 0x8f, 0x90, 0x9f, 0xa0, 0xbf, 0xc0, 0xff}
	for lead := 0xf0; lead <= 0xf7; lead++ {
		for c1 := 0x7e; c1 <= 0xc1; c1++ {
			for _, c2 := range edge {
				for _, c3 := range edge {
					check([]byte{byte(lead), byte(c1), c2, c3})
				}
			}
		}
	}
	pad := strings.Repeat("a", 96)
	for name, rn := range utf8Classes {
		for off := range 70 {
			for _, tail := range []string{"", "b", strings.Repeat("請", 12), strings.Repeat("c", 40)} {
				buf := bytes.Join([][]byte{[]byte(pad[:off]), rn, []byte(tail)}, nil)
				if got, want := validUTF8(buf), validUTF8Portable(buf); got != want {
					t.Fatalf("%s at offset %d, tail %q: validUTF8 = %t, utf8.Valid says %t", name, off, tail, got, want)
				}
				checks++
			}
		}
	}
	t.Logf("%d inputs, each with the same verdict from both validators", checks)
}

// FuzzValidUTF8 holds validUTF8 to validUTF8Portable on any input: on amd64,
// sonic's SIMD validator against Go's utf8.Valid (TestValidUTF8Parity has
// the exhaustive short inputs).
func FuzzValidUTF8(f *testing.F) {
	for _, seed := range []string{
		"", "a", "é", "請求", "😀", "\xed\xa0\x80", "\xc0\xaf", "\xf4\x90\x80\x80", "\xe8\xab", "\x80", "\xff",
		strings.Repeat("a", 31) + "請", strings.Repeat("a", 32) + "\xe8\xab", strings.Repeat("請", 11) + "\xed\xa0\x80",
		strings.Repeat("a", 63) + "😀" + strings.Repeat("b", 33),
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		defer testsupport.BoundFuzzInput(t)()
		if got, want := validUTF8(b), validUTF8Portable(b); got != want {
			t.Errorf("validUTF8(%q) = %t, utf8.Valid says %t", b, got, want)
		}
	})
}
