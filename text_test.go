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
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"syscall"
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

// quirkyKey is an API key with a quote, a double quote and a backslash, the
// upstream credential "ts_live_quo'te\"slash\\tail"
// (tests/test_logging.py:88), whose quoted forms differ from its raw one.
const quirkyKey = `ts_live_quo'te"slash\tail`

// TestRequestCredentials pins what the scrub of a transport error's text
// looks for (py:_core/logging.py:43-51): the values of the headers whose
// names mark credentials, and the credential after the scheme of an
// Authorization or Proxy-Authorization value, each raw, Go-quoted (%q and
// %+q) and JSON-escaped, at least 8 bytes long (R68), longest first.
func TestRequestCredentials(t *testing.T) {
	h := http.Header{
		"Authorization":       {"Bearer " + quirkyKey},
		"Proxy-Authorization": {"Basic  dXNlcjpodW50ZXIy"},
		"X-Client-Secret":     {"provider-credential"},
		"X-Access-Token":      {"t\u00f6k\u20acn-credential"},
		"X-Api-Key":           {"a<b>&c-credential"},
		"Cookie":              {"k=v1234"}, // 7 bytes: not looked for
		"X-Visible":           {"request-visible"},
	}
	got := requestCredentials(h)
	want := []string{
		"Bearer " + quirkyKey, `Bearer ts_live_quo'te\"slash\\tail`,
		quirkyKey, `ts_live_quo'te\"slash\\tail`,
		"Basic  dXNlcjpodW50ZXIy", "dXNlcjpodW50ZXIy",
		"provider-credential",
		"t\u00f6k\u20acn-credential", `t\u00f6k\u20acn-credential`,
		"a<b>&c-credential", `a\u003cb\u003e\u0026c-credential`,
	}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("the credentials lack %q:\n%q", w, got)
		}
	}
	for _, absent := range []string{"k=v1234", "request-visible", "Bearer", "Basic"} {
		if slices.Contains(got, absent) {
			t.Errorf("the credentials hold %q, which is not one:\n%q", absent, got)
		}
	}
	if !slices.IsSortedFunc(got, func(a, b string) int { return len(b) - len(a) }) {
		t.Errorf("the credentials are not longest first:\n%q", got)
	}
	if n := len(requestCredentials(nil)); n != 0 {
		t.Errorf("a request without headers has %d credentials, want 0", n)
	}
}

// TestCredentialsRedact pins the scrub of a transport error's text (note
// N-W2.5): every form of a credential of the request becomes "***", a whole
// Authorization value before the key inside it, then URL userinfo; a value
// shorter than 8 bytes and a header whose name marks no credential are left
// as they are.
func TestCredentialsRedact(t *testing.T) {
	const key = "ts_live_0123456789abcdef"
	auth := func(v string) http.Header { return http.Header{"Authorization": {v}} }
	tests := map[string]struct {
		h         http.Header
		s         string
		want      string
		wantFound bool
	}{
		"success: the API key": {
			h: auth("Bearer " + key), s: "dial refused for " + key, want: "dial refused for ***", wantFound: true,
		},
		"success: a whole Authorization value, before the key inside it": {
			h: auth("Bearer " + key), s: "proxy said: Bearer " + key + " and " + key, want: "proxy said: *** and ***", wantFound: true,
		},
		"success: the %q form of a key with quotes and a backslash": {
			h: auth("Bearer " + quirkyKey), s: fmt.Sprintf("Illegal header value %q; key %q", "Bearer "+quirkyKey, quirkyKey),
			want: `Illegal header value "***"; key "***"`, wantFound: true,
		},
		"success: the %q form of a value neither %+q nor JSON spells alike": {
			// Non-ASCII, which %+q escapes and %q keeps, and '<', which JSON
			// escapes and %q keeps.
			h: http.Header{"X-Access-Token": {"t\u00f6<\"k\u20acn-credential"}}, s: fmt.Sprintf("token %q", "t\u00f6<\"k\u20acn-credential"),
			want: `token "***"`, wantFound: true,
		},
		"success: the %+q form of a non-ASCII value": {
			h: http.Header{"X-Access-Token": {"t\u00f6k\u20acn-credential"}}, s: fmt.Sprintf("token %+q", "t\u00f6k\u20acn-credential"),
			want: `token "***"`, wantFound: true,
		},
		"success: the JSON form, HTML escapes included": {
			h: http.Header{"X-Api-Key": {"a<b>&c-credential"}}, s: `{"x-api-key":"a\u003cb\u003e\u0026c-credential"}`,
			want: `{"x-api-key":"***"}`, wantFound: true,
		},
		"success: a Proxy-Authorization credential after its scheme": {
			h: http.Header{"Proxy-Authorization": {"Basic dXNlcjpodW50ZXIy"}}, s: "rejected dXNlcjpodW50ZXIy", want: "rejected ***", wantFound: true,
		},
		"success: an Authorization value without a scheme, whole": {
			h: auth("opaque-token-value"), s: "rejected opaque-token-value", want: "rejected ***", wantFound: true,
		},
		"success: a value of 8 bytes is looked for (R68)": {
			h: http.Header{"Cookie": {"k=v12345"}}, s: "cookie k=v12345 refused", want: "cookie *** refused", wantFound: true,
		},
		"success: a value of 7 bytes is not (R68)": {
			h: http.Header{"Cookie": {"k=v1234"}}, s: "cookie k=v1234 refused", want: "cookie k=v1234 refused",
		},
		"success: a short key alone is not, its whole Authorization value is": {
			h: auth("Bearer test"), s: "testsupport: Bearer test refused", want: "testsupport: *** refused", wantFound: true,
		},
		"success: a header that marks no credential keeps its value": {
			h: http.Header{"X-Visible": {"request-visible"}}, s: "request-visible failed", want: "request-visible failed",
		},
		"success: URL userinfo without any header": {
			s: "proxyconnect tcp: http://user:hunter2@proxy.test:3128: refused", want: "proxyconnect tcp: http://***@proxy.test:3128: refused", wantFound: true,
		},
		"success: nothing to replace": {
			h: auth("Bearer " + key), s: "dial tcp 127.0.0.1:9: connect: connection refused", want: "dial tcp 127.0.0.1:9: connect: connection refused",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, found := requestCredentials(tt.h).redact(tt.s)
			if diff := gocmp.Diff(tt.want, got); diff != "" || found != tt.wantFound {
				t.Errorf("redact found %t, want %t (-want +got):\n%s", found, tt.wantFound, diff)
			}
		})
	}
}

// opaqueError prints a fixed text and wraps an error whose text it does not
// print.
type opaqueError struct{ inner error }

func (e opaqueError) Error() string { return "opaque failure" }
func (e opaqueError) Unwrap() error { return e.inner }

// TestCredentialsCause pins the cause an SDK error made from a transport
// error unwraps to (Appendix B: "cause via errors.Unwrap unless it printed a
// credential"): the transport's error itself, or, when the text of any
// error in its chain holds a credential, a *scrubbedError whose text has
// the credentials replaced, which errors.Is still matches against the
// well-known sentinels and the errno the transport's error matched, and
// through which errors.As reaches nothing else.
func TestCredentialsCause(t *testing.T) {
	const key = "ts_live_0123456789abcdef"
	creds := requestCredentials(http.Header{"Authorization": {"Bearer " + key}})
	reset := &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}
	deep := io.ErrUnexpectedEOF
	for i := range maxChainErrors {
		deep = fmt.Errorf("layer %d: %w", i, deep)
	}
	tests := map[string]struct {
		err       error
		kept      bool    // the cause is the transport's error itself
		text      string  // the stand-in's text
		sentinels []error // errors.Is holds for each through the stand-in
		absent    []error // and not for these
	}{
		"success: a text without a credential keeps the error": {err: reset, kept: true},
		"success: a credential in the text: a stand-in with the sentinel": {
			err: fmt.Errorf("read %s: %w", key, io.ErrUnexpectedEOF), text: "read ***: unexpected EOF",
			sentinels: []error{io.ErrUnexpectedEOF}, absent: []error{io.EOF, context.DeadlineExceeded},
		},
		"success: the errno of a reset survives, the *net.OpError does not": {
			err: fmt.Errorf("%s: %w", key, reset), text: "***: read tcp: read: connection reset by peer",
			sentinels: []error{syscall.ECONNRESET}, absent: []error{syscall.ECONNREFUSED},
		},
		"success: a deadline survives": {
			err: fmt.Errorf("Bearer %s: %w", key, context.DeadlineExceeded), text: "***: context deadline exceeded",
			sentinels: []error{context.DeadlineExceeded}, absent: []error{context.Canceled},
		},
		"success: a credential only in a wrapped error the text does not print": {
			err: opaqueError{inner: errors.New("rejected " + key)}, text: "opaque failure",
		},
		"success: a chain longer than the bound counts as holding one": {
			err: deep, text: deep.Error(), sentinels: []error{io.ErrUnexpectedEOF},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := creds.cause(tt.err)
			if tt.kept {
				if got != tt.err { //nolint:errorlint // identity is the assertion
					t.Errorf("cause = %v, want the transport's error itself", got)
				}
				return
			}
			s, ok := got.(*scrubbedError) //nolint:errorlint // the stand-in itself, not a link of its chain
			if !ok {
				t.Fatalf("cause = %T %v, want a *scrubbedError", got, got)
			}
			if diff := gocmp.Diff(tt.text, s.Error()); diff != "" {
				t.Errorf("Error() (-want +got):\n%s", diff)
			}
			for _, sentinel := range tt.sentinels {
				if !errors.Is(got, sentinel) {
					t.Errorf("errors.Is(stand-in, %v) = false, want true", sentinel)
				}
			}
			for _, sentinel := range append(tt.absent, tt.err) {
				if errors.Is(got, sentinel) {
					t.Errorf("errors.Is(stand-in, %v) = true, want false", sentinel)
				}
			}
			if _, ok := errors.AsType[*net.OpError](got); ok {
				t.Error("errors.As reaches the transport's *net.OpError through the stand-in")
			}
			assertNotPrinted(t, got, key)
		})
	}
	if got := creds.cause(nil); got != nil {
		t.Errorf("cause(nil) = %v, want nil", got)
	}
}
