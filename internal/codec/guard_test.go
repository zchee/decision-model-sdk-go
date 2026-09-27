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
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	sonicdecoder "github.com/bytedance/sonic/decoder"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// sonicOverReads are the 31 inputs on which sonic v1.15.4's advance_dword
// reads past the input's end, on darwin/arm64 and on linux/amd64 alike: every
// one shorter than 4 bytes, with a t, n or f where sonic reads a literal.
var sonicOverReads = []string{
	"[f", "[fa", "[n", "[t", "f f", "f", "f+", "f0", "f2:", "f:", "fC3", "fE", "fF5", "fFA", "fa", "fal",
	"fc2", "fcn", "fee", "fr", "n", "nB", "nE", "nl", "nt", "nu", "t", "t1", `t\`, "tf", "tr",
}

// cutOverReads are bodies of 4 bytes or more whose cut (cutPoint) was
// shorter than 4 bytes, so that the one-scan traversal handed sonic a short
// input inside the body and sonic read past the body's end: " f]}" cuts to
// " f]", whose false is read 2 bytes past.
var cutOverReads = []string{" f]}", "\tf]}", "f]} ", "fa]}", " fa]}"}

// guardEntries are the SDK's ways of handing sonic bytes that a server or a
// caller chose, each as the SDK makes it: the four exported decoders; the
// three sonic steps inside them on the bytes their callers give them (the
// error body's compact JSON, which ReadErrorBody builds, is placed at the
// guard in its own right); and the request side's UTF-8 check, validUTF8,
// which EncodeState and EncodeValue run over the bytes of a caller's state:
// sonic's native validator (utf8.Validate) on amd64 and utf8.Valid on
// arm64 (validate_amd64.go, validate_arm64.go).
var guardEntries = []struct {
	name string
	run  func(b []byte)
}{
	{"DecodeSystemOne", func(b []byte) { var d wire.SystemOneResult; _, _ = DecodeSystemOne(b, nil, "m", &d) }},
	{"DecodeSystemOneInto", func(b []byte) {
		var d wire.SystemOneResult
		_, _ = DecodeSystemOneInto(b, nil, "m", &d, make([]wire.AnswerEntry, 0, 4))
	}},
	{"DecodeModels", func(b []byte) { var d wire.ModelList; _ = DecodeModels(b, &d) }},
	{"ReadErrorBody", func(b []byte) { _ = ReadErrorBody(b) }},
	{"trailing, as traverseWhole calls it", func(b []byte) { _ = trailing(padShort(b)) }},
	{"validUTF8, the request's UTF-8 check (sonic's on amd64)", func(b []byte) { _ = validUTF8(b) }},
	{"the lazy pass's parser, as lazy calls it", func(b []byte) {
		root, err := sonic.GetFromString(NoCopyString(padShort(b)))
		if err == nil {
			walkNode(&root, 0)
		}
	}},
}

// walkNode reads every node under n as the lazy pass reads a legend: the
// members, their raw bytes and their scalars.
func walkNode(n *ast.Node, depth int) {
	if depth > 64 {
		return
	}
	_, _ = n.Raw()
	switch n.TypeSafe() {
	case ast.V_OBJECT:
		it, err := n.Properties()
		if err != nil {
			return
		}
		var p ast.Pair
		for it.Next(&p) {
			walkNode(&p.Value, depth+1)
		}
	case ast.V_ARRAY:
		it, err := n.Values()
		if err != nil {
			return
		}
		var v ast.Node
		for it.Next(&v) {
			walkNode(&v, depth+1)
		}
	case ast.V_STRING:
		_, _ = n.String()
	case ast.V_NUMBER:
		_, _ = n.Number()
	}
}

// guardCorpus returns the inputs of TestGuardPage: the 31 of sonicOverReads
// and the cut shapes; every string of 1 to 4 bytes over an alphabet of JSON's
// structure, whitespace, the literals' first letters and a digit (30 940
// inputs, the shapes that reach advance_dword with less than 4 bytes); every
// prefix of four fixtures, the one that runs the lazy pass included; and the
// shapes of TestK41ScannerBoundary around the 32-byte boundary.
func guardCorpus(t *testing.T) [][]byte {
	var corpus [][]byte
	add := func(s string) { corpus = append(corpus, []byte(s)) }
	for _, s := range sonicOverReads {
		add(s)
	}
	for _, s := range cutOverReads {
		add(s)
	}
	const alphabet = "{}[]\":, tfnr0"
	var build func(prefix []byte, n int)
	build = func(prefix []byte, n int) {
		if n == 0 {
			corpus = append(corpus, append([]byte(nil), prefix...))
			return
		}
		for i := range len(alphabet) {
			build(append(prefix, alphabet[i]), n-1)
		}
	}
	for n := 1; n <= 4; n++ {
		build(nil, n)
	}
	for _, name := range []string{"result.json", "structured-legend.json", "models.json", "duplicates.json"} {
		b := testsupport.Fixture(t, name)
		for n := 1; n <= len(b); n++ {
			corpus = append(corpus, b[:n])
		}
	}
	for n := 28; n <= 40; n++ {
		zeros := strings.Repeat("0", n)
		add(`{"":"` + zeros)
		add(`{"":"` + zeros + `}`)
		add(`{"a":"` + zeros + `}}`)
	}
	return corpus
}

// TestGuardPage is the regression test of sonic's advance_dword reading past
// an input shorter than 4 bytes: every way the SDK hands sonic bytes
// (guardEntries) runs over every input of guardCorpus placed to end at a page
// no access may touch, and to start right after one, and must not fault:
// sonic reads no byte of memory the SDK does not own. padShort gives a body
// shorter than 4 bytes zeroed room and cutPoint refuses a cut shorter than 4
// bytes; without them sonicOverReads and the cut shapes fault here.
//
// Two controls keep the test from passing on a guard that is not there,
// neither of them run by the SDK: a one-byte read past each edge of the
// guarded memory must fault, and sonic's own decoder.Skip, on sonicOverReads
// placed at the end, must fault on every one of them: the over-read the SDK
// guards against, as sonic v1.15.4 has it. Should a sonic release fix
// advance_dword, that control fails first, and the SDK's guard can be
// reviewed.
//
// The guard pages need mmap and mprotect, which the syscall package offers
// on Linux and Darwin only; elsewhere (windows-2025 among the CI images)
// the test skips, saying so.
func TestGuardPage(t *testing.T) {
	corpus := guardCorpus(t)
	longest := 0
	for _, in := range corpus {
		longest = max(longest, len(in))
	}
	g := testsupport.NewGuard(t, longest)

	if _, ok := testsupport.CatchFault(g.ReadPastEnd); !ok {
		t.Fatal("control: a read one byte past the guarded memory did not fault")
	}
	if _, ok := testsupport.CatchFault(g.ReadPastStart); !ok {
		t.Fatal("control: a read one byte before the guarded memory did not fault")
	}
	for _, in := range sonicOverReads {
		b := g.AtEnd([]byte(in))
		if _, ok := testsupport.CatchFault(func() { sonicdecoder.Skip(b) }); !ok {
			t.Errorf("control: sonic's decoder.Skip read %q at the end of the guarded memory without a fault; review W6.2 MIN-1 saw it read past every one of these inputs (sonic v1.15.4): has sonic changed?", in)
		}
	}

	calls, faults := 0, 0
	places := []struct {
		name  string
		place func([]byte) []byte
	}{{"ending at the guard", g.AtEnd}, {"starting after the guard", g.AtStart}}
	for _, e := range guardEntries {
		for _, in := range corpus {
			for _, p := range places {
				b := p.place(in)
				calls++
				if msg, ok := testsupport.CatchFault(func() { e.run(b) }); ok {
					faults++
					t.Errorf("%s read past the input %q (%d bytes) %s: %s", e.name, in, len(in), p.name, msg)
				}
			}
		}
	}
	// The error body's compact JSON, as ReadErrorBody hands it to sonic.
	for _, in := range corpus {
		compact, err := wire.AppendJSON(nil, in)
		if err != nil {
			continue // ReadErrorBody gives sonic only what AppendJSON takes
		}
		for _, p := range places {
			b := p.place(compact)
			calls++
			if msg, ok := testsupport.CatchFault(func() { _, _ = decodeErrorJSON(b) }); ok {
				faults++
				t.Errorf("decodeErrorJSON read past the compact JSON %q (%d bytes) %s: %s", compact, len(compact), p.name, msg)
			}
		}
	}
	t.Logf("GUARD %s/%s: page %d bytes, %d inputs, %d calls, %d faults", runtime.GOOS, runtime.GOARCH, os.Getpagesize(), len(corpus), calls, faults)
}

// TestCutPointMinimumLength pins cutPoint's refusal of a cut shorter than
// minSonicInput, the rule that keeps the one-scan traversal from handing
// sonic a short prefix of a longer body: a 4-byte body whose cut is 3 bytes
// goes to the whole-body path, a cut of exactly 4 bytes is taken, and a cut
// of 4 bytes of whitespace is refused. It runs on every system,
// TestGuardPage's guard pages or not.
func TestCutPointMinimumLength(t *testing.T) {
	tests := map[string]struct {
		body string
		want int
	}{
		`error: " f]}" cuts to 3 bytes`:            {body: " f]}", want: -1},
		`error: "fa]}" cuts to 3 bytes`:            {body: "fa]}", want: -1},
		`error: "{f]}" cuts to 3 bytes`:            {body: "{f]}", want: -1},
		`error: "{}" cuts to 1 byte`:               {body: "{}", want: -1},
		`error: "f]} " cuts to 2 bytes`:            {body: "f]} ", want: -1},
		`error: "    }" cuts to 4 spaces`:          {body: "    }", want: -1},
		`success: "{   }" cuts to exactly 4 bytes`: {body: "{   }", want: 4},
		`success: "{\"\":{}}" cuts before its end`: {body: `{"":{}}`, want: 6},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := cutPoint([]byte(tt.body)); got != tt.want {
				t.Errorf("cutPoint(%q) = %d, want %d", tt.body, got, tt.want)
			}
		})
	}
}

// TestPadShort pins padShort's contract: a body sonic may read as it is,
// empty or at least minSonicInput bytes long, comes back as it is, the same
// backing array; a body of 1 to 3 bytes comes back as a copy in an array of
// its own whose room past the body holds at least minSonicInput zero bytes,
// the room sonic's advance_dword reads. The short inputs have readable
// non-zero bytes after them (the rest of a literal, as []byte("true")[:1] has
// "rue"), which the copy must not carry. TestGuardPage cannot see this room:
// padShort's copy is on the Go heap, never next to its guard page.
func TestPadShort(t *testing.T) {
	fixture := testsupport.Fixture(t, "result.json")
	tests := map[string]struct {
		in   []byte
		same bool // padShort returns in itself
	}{
		"success: nil stays nil":                     {in: nil, same: true},
		"success: an empty body with room":           {in: make([]byte, 0, 8), same: true},
		"success: 4 bytes, the shortest read as is":  {in: []byte("true"), same: true},
		"success: a fixture":                         {in: fixture, same: true},
		`error: 1 byte, "t" with "rue" after it`:     {in: []byte("true")[:1]},
		`error: 2 bytes, "nu" with "ll" after it`:    {in: []byte("null")[:2]},
		`error: 3 bytes, "fal" with "se" after it`:   {in: []byte("false")[:3]},
		`error: 3 bytes at the end of their array`:   {in: []byte("[f]")},
		`error: 1 byte with a whole page after it`:   {in: append(make([]byte, 0, 4096), 'f')},
		`error: 2 bytes, "{}" with a brace after it`: {in: []byte("{}}")[:2]},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			before := slices.Clone(tt.in)
			out := padShort(tt.in)
			if !bytes.Equal(out, before) || (out == nil) != (tt.in == nil) {
				t.Fatalf("padShort(%q) = %q, want the same bytes", before, out)
			}
			sameArray := unsafe.SliceData(out) == unsafe.SliceData(tt.in)
			if tt.same {
				if !sameArray || cap(out) != cap(tt.in) {
					t.Errorf("padShort(%q) returned another array (cap %d, the input's %d), want the body itself", before, cap(out), cap(tt.in))
				}
				return
			}
			if sameArray {
				t.Fatalf("padShort(%q) returned the input's own array, want a copy with zeroed room", before)
			}
			if cap(out) < len(out)+minSonicInput {
				t.Fatalf("padShort(%q) has capacity %d, want at least %d: sonic reads %d bytes past a short body", before, cap(out), len(out)+minSonicInput, minSonicInput)
			}
			if room := out[len(out):cap(out)]; slices.ContainsFunc(room, func(b byte) bool { return b != 0 }) {
				t.Errorf("padShort(%q)'s room past the body is %q, want zeros", before, room)
			}
		})
	}
}
