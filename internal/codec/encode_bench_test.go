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

// BenchmarkEncodeState measures a text state's encode into a pooled scratch
// (encode: sonic plus the checks of EncodeState) next to the UTF-8 pass of
// ruling R48 alone (check: utf8.Valid over the same encoded bytes), for
// ASCII text and for CJK text, whose three-byte runes take utf8.Valid's slow
// path, at 1 KiB, 64 KiB and 6 MiB.
func BenchmarkEncodeState(b *testing.B) {
	texts := map[string]string{
		"ascii": "The quick brown fox jumps over the lazy dog. ",
		"cjk":   "請求が二重に計上されました。確認をお願いします。",
	}
	sizes := []struct {
		name string
		n    int
	}{{"1KiB", 1 << 10}, {"64KiB", 64 << 10}, {"6MiB", 6 << 20}}
	for _, text := range []string{"ascii", "cjk"} {
		for _, size := range sizes {
			// Whole repeats, so a multi-byte rune is never cut: at most size.n bytes.
			var state any = strings.Repeat(texts[text], size.n/len(texts[text]))
			encoded := make([]byte, 0, size.n+2)
			if err := EncodeState(&encoded, state); err != nil {
				b.Fatal(err)
			}
			b.Run(text+"/"+size.name+"/encode", func(b *testing.B) {
				b.SetBytes(int64(len(encoded)))
				body := NewBody()
				defer body.Release()
				buf := body.Buffer()
				for b.Loop() {
					*buf = (*buf)[:0]
					if err := EncodeState(buf, state); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run(text+"/"+size.name+"/check", func(b *testing.B) {
				b.SetBytes(int64(len(encoded)))
				for b.Loop() {
					if !utf8.Valid(encoded) {
						b.Fatal("invalid UTF-8")
					}
				}
			})
		}
	}
}
