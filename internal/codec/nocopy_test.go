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
	"unsafe"
)

func TestNoCopyString(t *testing.T) {
	tests := map[string]struct {
		b    []byte
		want string
	}{
		"success: nil slice":        {b: nil, want: ""},
		"success: empty slice":      {b: []byte{}, want: ""},
		"success: ASCII":            {b: []byte(`{"model":"jev-latest"}`), want: `{"model":"jev-latest"}`},
		"success: multibyte text":   {b: []byte("日本語"), want: "日本語"},
		"success: invalid UTF-8":    {b: []byte{0xff, 0xfe}, want: "\xff\xfe"},
		"success: embedded NUL":     {b: []byte("a\x00b"), want: "a\x00b"},
		"success: sub-slice window": {b: []byte("xxbillingxx")[2:9], want: "billing"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := NoCopyString(tt.b)
			if got != tt.want {
				t.Fatalf("NoCopyString(%q) = %q, want %q", tt.b, got, tt.want)
			}
			if len(tt.b) > 0 && unsafe.StringData(got) != unsafe.SliceData(tt.b) {
				t.Errorf("NoCopyString copied: string data %p, slice data %p", unsafe.StringData(got), unsafe.SliceData(tt.b))
			}
		})
	}
}

// TestNoCopyStringAliasing documents the aliasing contract: the string is a
// view of the bytes, so a write to the bytes shows through every string and
// substring taken from them. This is why NoCopyString is only applied to a
// response body, which the SDK never writes after reading it, and why a
// string that must outlive a reused buffer is copied first.
func TestNoCopyStringAliasing(t *testing.T) {
	body := []byte(`{"answers":{"billing":{"type":"noul","noul":0.9}}}`)
	s := NoCopyString(body)
	start := strings.Index(s, "billing")
	end := start + len("billing")
	key := s[start:end]         // a substring, as sonic hands a key without escapes
	owned := strings.Clone(key) // what interning does on a miss

	copy(body[start:end], "BILLING")

	if s[start:end] != "BILLING" || key != "BILLING" {
		t.Errorf("the view did not follow the write: s[%d:%d] = %q, key = %q", start, end, s[start:end], key)
	}
	if owned != "billing" {
		t.Errorf("a cloned string changed with the buffer: %q", owned)
	}
}

func TestNoCopyStringAllocatesNothing(t *testing.T) {
	b := []byte(strings.Repeat("x", 1<<10))
	if allocs := testing.AllocsPerRun(100, func() { _ = NoCopyString(b) }); allocs != 0 {
		t.Fatalf("NoCopyString allocated %v times per call, want 0", allocs)
	}
}
