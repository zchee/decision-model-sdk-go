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
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
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

// TestLogErrorText pins what the transport's DEBUG records print for a
// transport error (h2gate.Config.ErrorText, ruling R84): the error's text
// with every credential of the failing request's header and every URL
// userinfo replaced by "***", then escaped and cut at 200 characters, the
// scrub before the cut, so no piece of a credential survives at the edge.
func TestLogErrorText(t *testing.T) {
	const key = "ts_live_0123456789abcdef"
	req := &http.Request{Header: http.Header{"Authorization": {"Bearer " + key}, "X-Client-Secret": {"provider-credential"}, "X-Visible": {"request-visible"}}}
	pad := strings.Repeat("p", 190)
	tests := map[string]struct {
		err  error
		want string
	}{
		"success: the Authorization value, the key and a secret header's value": {
			err:  errors.New("dial: Bearer " + key + " refused; key " + key + "; secret provider-credential; visible request-visible"),
			want: "dial: *** refused; key ***; secret ***; visible request-visible",
		},
		"success: the %q form of the key": {
			err:  fmt.Errorf("dial: key %q refused", key),
			want: `dial: key "***" refused`,
		},
		"success: URL userinfo": {
			err:  errors.New("proxyconnect tcp: http://user:hunter2@proxy.test:3128: refused"),
			want: "proxyconnect tcp: http://***@proxy.test:3128: refused",
		},
		"success: control characters escaped, backslashes kept": {
			err:  errors.New(`dial refused \x1b ` + "\x1b[31m\n"),
			want: `dial refused \x1b \x1b[31m\n`,
		},
		"success: the key across the 200-character cut, replaced before the cut": {
			err:  errors.New(pad + key),
			want: pad + "***",
		},
		"success: text past 200 characters cut": {
			err:  errors.New(pad + strings.Repeat("q", 20)),
			want: pad + strings.Repeat("q", 10) + "…",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := gocmp.Diff(tt.want, logErrorText(req, tt.err)); diff != "" {
				t.Errorf("logErrorText (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRedactionCoversGoEscapeForms ports test_exception_redaction_escaped_values
// (L3, tests/test_logging.py:135-142): a credential holding a quote, a
// double quote and a backslash, sent after "Bearer " under Authorization and
// Proxy-Authorization and as the whole value under X-API-Key and
// X-MiXeD-ToKeN, is replaced by "***" in an error's text in each form Go
// prints a string in: raw, %q, %+q and JSON-escaped, the Go analogue of the
// Python SDK's raw, bytes repr and json.dumps forms ("raw=***;
// bytes=b'***'; json=\"***\""). The stand-in the SDK error unwraps to, and
// the transport's DEBUG record, carry that text; the original error keeps
// its own. Two more credentials make the forms differ: non-ASCII letters,
// which %+q escapes and %q and JSON keep, and with them '<' and '>', which
// JSON escapes and %q keeps, so that only the %q form matches %q.
func TestRedactionCoversGoEscapeForms(t *testing.T) {
	const want = `raw=***; quoted="***"; ascii="***"; json="***"`
	type test struct {
		header, credential string
	}
	tests := map[string]test{}
	for _, header := range []string{"Authorization", "Proxy-Authorization", "X-API-Key", "X-MiXeD-ToKeN"} {
		for name, credential := range map[string]string{"ASCII": `private'quoted"value\tail`, "non-ASCII": `privé'quoted"välue\tail`, "non-ASCII and HTML": `priv<é>'quoted"välue\tail`} {
			tests["success: "+header+"/"+name] = test{header: header, credential: credential}
		}
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			value := tt.credential
			if strings.Contains(strings.ToLower(tt.header), "authorization") {
				value = "Bearer " + tt.credential
			}
			h := http.Header{tt.header: {value}}
			j, err := testsupport.StdlibMarshal(tt.credential)
			if err != nil {
				t.Fatalf("StdlibMarshal: %v", err)
			}
			original := fmt.Errorf("raw=%s; quoted=%q; ascii=%+q; json=%s", tt.credential, tt.credential, tt.credential, j)
			creds := requestCredentials(h)
			if got, found := creds.redact(original.Error()); got != want || !found {
				t.Errorf("redact = %q, %t, want %q, true", got, found, want)
			}
			cause := creds.cause(original)
			if _, ok := cause.(*scrubbedError); !ok || cause.Error() != want { //nolint:errorlint // the stand-in itself, not a link of its chain
				t.Errorf("cause = %T %q, want a *scrubbedError %q", cause, cause, want)
			}
			if got := logErrorText(&http.Request{Header: h}, original); got != want {
				t.Errorf("logErrorText = %q, want %q", got, want)
			}
			if !strings.HasPrefix(original.Error(), "raw="+tt.credential+";") {
				t.Errorf("the original error's text changed: %q", original.Error())
			}
		})
	}
}

// TestRedactionKeepsCleanChains ports
// test_exception_redaction_preserves_network_diagnostics (L6,
// tests/test_logging.py:176-184): a transport error that shows no credential
// passes the scrub unchanged, so its diagnostics survive: the cause an SDK
// error unwraps to is the transport's error itself, with its type, text and
// chain, and errors.As reaches the *net.OpError and errors.Is the errno, as
// the Python SDK keeps a ConnectError of the same type and text with its
// OSError cause. The request carries a credential, which the error does not
// show. It holds at the scrub, through a RoundTripper, and through the SDK's
// own transport, where the error arrives inside h2gate's DialError.
func TestRedactionKeepsCleanChains(t *testing.T) {
	const key = "ts_live_0123456789abcdef"
	unreachable := func() *net.OpError {
		return &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 443}, Err: &os.SyscallError{Syscall: "connect", Err: syscall.ENETUNREACH}}
	}
	// check asserts that err unwraps, through its chain, to want itself.
	check := func(t *testing.T, err error, want *net.OpError) {
		t.Helper()
		oe, ok := errors.AsType[*net.OpError](err)
		if !ok || oe != want {
			t.Errorf("errors.As(*net.OpError) = %v, %t, want the transport's own %p", oe, ok, want)
		}
		if !errors.Is(err, syscall.ENETUNREACH) {
			t.Errorf("errors.Is(err, ENETUNREACH) = false for %v", err)
		}
		if se, ok := errors.AsType[*os.SyscallError](err); !ok || se.Syscall != "connect" {
			t.Errorf("errors.As(*os.SyscallError) = %v, %t", se, ok)
		}
		if _, ok := errors.AsType[*scrubbedError](err); ok {
			t.Errorf("the chain of %v holds a stand-in, want none", err)
		}
	}
	t.Run("success: the scrub keeps the error", func(t *testing.T) {
		creds := requestCredentials(http.Header{"Authorization": {"Bearer " + key}, "X-Client-Secret": {"provider-credential"}})
		for _, err := range []error{unreachable(), fmt.Errorf("proxy hop: %w", unreachable())} {
			if got := creds.cause(err); got != err { //nolint:errorlint // identity is the assertion
				t.Errorf("cause(%v) = %T %v, want the error itself", err, got, got)
			}
			if got, found := creds.redact(err.Error()); got != err.Error() || found {
				t.Errorf("redact(%q) = %q, %t, want it unchanged", err.Error(), got, found)
			}
		}
	})
	t.Run("success: through a RoundTripper", func(t *testing.T) {
		want := unreachable()
		clearEnv(t)
		c := newEnvClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, want }), WithAPIKey(key))
		_, err := c.Models().List(t.Context(), Retry(NoRetry()))
		ce, ok := errors.AsType[*ConnectionError](err)
		if !ok || ce.Error() != "Connection error: "+want.Error() || errors.Unwrap(err) != want { //nolint:errorlint // identity is the assertion
			t.Fatalf("error = %T %v unwrapping to %v, want a *ConnectionError with the error's text around it", err, err, errors.Unwrap(err))
		}
		check(t, err, want)
	})
	t.Run("success: through the SDK's transport and a caller dialer", func(t *testing.T) {
		want := unreachable()
		clearEnv(t)
		tr := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return nil, want }}
		c, err := NewClient(WithAPIKey(key), WithBaseURL("https://example.com"), WithHTTPTransport(tr))
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		t.Cleanup(func() { _ = c.Close() })
		_, err = c.Models().List(t.Context(), Retry(NoRetry()))
		if ce, ok := errors.AsType[*ConnectionError](err); !ok || ce.Error() != "Connection error: "+want.Error() {
			t.Fatalf("error = %T %v, want a *ConnectionError with the dialer's text", err, err)
		}
		check(t, err, want)
	})
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

// TestJSONFormMatchesEncodingJSON checks jsonForm against the encoder a
// caller's error text would carry, encoding/json (through
// testsupport.StdlibMarshal: the root package's tests import no JSON
// library), rather than against expected strings (review W2.5 NIT 8): for
// each value, jsonForm writes what json.Marshal writes between the quotes.
// The values cover obs-text bytes that are not UTF-8, a character above the
// Basic Multilingual Plane, the HTML characters, quotes and backslashes,
// controls, DEL, U+2028 and U+2029, and a header's own characters.
func TestJSONFormMatchesEncodingJSON(t *testing.T) {
	tests := map[string]string{ //nolint:gosec // G101: fake values that only exercise the escaper
		"success: obs-text bytes that are not UTF-8": "k\x80\xfe\xff-credential",
		"success: a truncated multi-byte sequence":   "k\xe2\x82-credential",
		"success: above the BMP":                     "k\U0001f600\U00010348-credential",
		"success: HTML characters":                   "a<b>&c-credential",
		"success: quotes and backslashes":            `q"uo\te'%41-credential`,
		"success: controls and DEL":                  "t\tab\x01\x1f\x7f\n\r-credential",
		"success: line and paragraph separators":     "l\u2028p\u2029-credential",
		"success: Latin-1 and BMP characters":        "t\u00f6k\u20acn\u00a0-credential",
		"success: a plain token":                     "Bearer ts_live_0123456789abcdef",
	}
	for name, v := range tests {
		t.Run(name, func(t *testing.T) {
			b, err := testsupport.StdlibMarshal(v)
			if err != nil {
				t.Fatalf("StdlibMarshal: %v", err)
			}
			if diff := gocmp.Diff(string(b[1:len(b)-1]), jsonForm(v)); diff != "" {
				t.Errorf("jsonForm(%q) (-encoding/json +got):\n%s", v, diff)
			}
		})
	}
}
