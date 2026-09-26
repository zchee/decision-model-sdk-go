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

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// falsyJSONWhole is falsyJSON as it was before W5.3's P2: every value is
// checked and compacted whole, and the verdict read from the compact form.
// FuzzFalsyJSON holds falsyJSON to it.
func falsyJSONWhole(raw []byte) bool {
	compact, err := wire.AppendJSON(nil, raw)
	if err != nil {
		return false
	}
	switch s := string(compact); s {
	case "null", "false", `""`, "[]", "{}":
		return true
	default:
		if s[0] != '-' && (s[0] < '0' || s[0] > '9') {
			return false
		}
		for i := range len(s) {
			switch c := s[i]; {
			case c == 'e' || c == 'E':
				return true
			case '1' <= c && c <= '9':
				return false
			}
		}
		return true
	}
}

// FuzzFalsyJSON checks that falsyJSON, which reads a value's first bytes
// before it checks the whole value (maybeFalsyJSON), gives the verdict of
// the whole-value check on any input: the falsy literals and zeros with
// whitespace around and inside them, their truthy neighbours, and invalid
// JSON that starts like either.
func FuzzFalsyJSON(f *testing.F) {
	for _, seed := range []string{
		``, ` `, "\t\n\r ", `null`, ` null `, `nul`, `nulll`, `false`, "\nfalse\t", `fals`, `true`, `tru`,
		`""`, ` "" `, `"`, `"a"`, `"\""`, `""x`, `"" ""`,
		`[]`, `[ ]`, "[\n\t]", `[`, `[ ]]`, `[0]`, `[ ""]`, `{}`, "{\r\n}", `{`, `{ }x`, `{"a":1}`, `{ "" : 0 }`,
		`0`, `-0`, `0.0`, `-0.000`, `0e5`, `-0.0e-5`, `0E+1`, `00`, `-`, `-a`, `0.`, `.0`, `0x0`, `1`, `-1`, `0.01`,
		`1e-3`, `0e`, `0 0`, `0 1`, ` 0 `, `-0.0000000000000000000000000000000000000000e99`,
		`00000000000000000000000000000000000000000000`, "\xef\xbb\xbf0", "\f0", "\v[]", "\xc2\xa0null", `[] []`,
		strings.Repeat(" ", 40) + "{" + strings.Repeat(" ", 40) + "}",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if got, want := falsyJSON(raw), falsyJSONWhole(raw); got != want {
			t.Errorf("falsyJSON(%q) = %t, the whole-value check says %t", raw, got, want)
		}
	})
}
