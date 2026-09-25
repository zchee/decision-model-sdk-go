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
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// FuzzAppendJSON is the differential half of ruling R44: wire's scanner,
// which the SDK uses to check and compact JSON content without a JSON
// library, accepts exactly the inputs that encoding/json's json.Valid
// accepts and that are valid UTF-8 (encoding/json does not check UTF-8),
// and its output is encoding/json's json.Compact of the input. wire's own
// FuzzAppendJSON checks the scanner's invariants.
func FuzzAppendJSON(f *testing.F) {
	for _, seed := range []string{
		``, ` `, `null`, `true`, `false`, `0`, `-0`, `1.5e+3`, `-12.25E-3`, `01`, `1.`, `1e`, `-`, `+1`, `.5`,
		`""`, `"a\"b\\c\/d\b\f\n\r\t@U@00e9@U@D83D@U@DE00"`, `"@U@d800"`, `"\q"`, `"@U@12g4"`, "\"\x1f\"", "\"\x7f\"",
		"\"a\xffb\"", "\"\xed\xa0\x80\"", `"abc`, `"abc\`,
		`[]`, `{}`, `[ ]`, `{ }`, `[1,2,[3,{"a":[]}]]`, `{"a":1,"b":[true,null],"c":{"d":"e"}}`,
		"{\n  \"text\" : \"Classify\",\r\n\t\"extra\" : null\n}\n", "[1\f]", "[1\v]", "[1\xc2\xa0]", "\xef\xbb\xbf{}",
		`[1,]`, `{"a":1,}`, `[1}`, `{1:2}`, `{"a" 1}`, `{"a":1} x`, `{}{}`, `[tru]`, `[nul]`,
		"\"\u2028\u2029<>&\"", `{"a":1,"a":2}`, `1E400`, `-1e-400`, `[` + strings.Repeat(`"x",`, 50) + `0]`,
		strings.Repeat("[", 40) + strings.Repeat("]", 40),
	} {
		f.Add([]byte(unescapeU(seed)))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		out, err := wire.AppendJSON(nil, raw)
		want := json.Valid(raw) && utf8.Valid(raw)
		if (err == nil) != want {
			t.Fatalf("wire.AppendJSON(%q) error = %v; json.Valid && utf8.Valid = %t", raw, err, want)
		}
		if err != nil {
			return
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			t.Fatalf("json.Compact(%q) = %v after json.Valid", raw, err)
		}
		if !bytes.Equal(out, compact.Bytes()) {
			t.Fatalf("wire.AppendJSON(%q) = %q, json.Compact = %q", raw, out, compact.Bytes())
		}
	})
}
