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
	"slices"
	"strings"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// isSecretHeaderLower is isSecretHeader as it was before W5.3: the name
// lower-cased whole, then compared. FuzzIsSecretHeader holds isSecretHeader
// to it.
func isSecretHeaderLower(name string) bool {
	lower := strings.ToLower(name)
	return slices.Contains(secretHeaderNames, lower) || strings.Contains(lower, "token") || strings.Contains(lower, "secret")
}

// FuzzIsSecretHeader checks that isSecretHeader, which folds an ASCII name's
// letters in place, gives strings.ToLower's verdict on any name, within
// the per-input bound: the six names and the two words in every case,
// next to other bytes, split, cut short, and spelled with runes that
// lower-case to ASCII letters (the Kelvin sign K, the dotted capital I)
// or fold to them without lower-casing to them (the long s). Its seed corpus
// runs as a test in CI's -race test step (go test -race with coverage) on
// ubuntu-26.04, xcode-27 and windows-2025, and the fuzz job fuzzes it for
// 60 s on ubuntu-26.04.
func FuzzIsSecretHeader(f *testing.F) {
	for _, seed := range []string{
		"", "a", "Authorization", "PROXY-AUTHORIZATION", "x-api-key", "Api-Key", "Cookie", "Set-Cookie",
		"X-Access-Token", "X-Client-Secret", "x-MiXeD-ToKeN", "tokenizer", "TOKEN", "toke", "secre", "t0ken",
		"Content-Type", "X-Typesafe-Sdk", "Cookie2", "set-cookie ", " set-cookie", "X-Api", "X-Key",
		"x-toKen", "secretK", "Key", "x-api-Key", "ſecret", "x-ſecret", "İtoken",
		"töken", "secİret", "Authorizаtion", "\xff", "tok\xffen",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		defer testsupport.BoundFuzzInput(t)()
		if got, want := isSecretHeader(name), isSecretHeaderLower(name); got != want {
			t.Errorf("isSecretHeader(%q) = %t, strings.ToLower's rule says %t", name, got, want)
		}
	})
}
