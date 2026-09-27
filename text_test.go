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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/engine"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// TestRenderFieldPath checks how a field path is printed: the Python SDK's
// dotted field_path, "." for the root, each name the server chose escaped with
// its backslashes doubled and cut at 128 characters, and the whole cut at 320.
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
			if n := utf8.RuneCountInString(got); n > engine.MaxPathChars+1 {
				t.Errorf("rendered path has %d characters, want at most %d", n, engine.MaxPathChars+1)
			}
		})
	}
}

// TestRedactionKeepsCleanChains ports
// test_exception_redaction_preserves_network_diagnostics
// (tests/test_logging.py:176-184) through the client: a transport error that
// shows no credential passes the scrub unchanged, so its diagnostics
// survive: the cause an SDK error unwraps to is the transport's error
// itself, with its type, text and chain, and errors.As reaches the
// *net.OpError and errors.Is the errno, as the Python SDK keeps a
// ConnectError of the same type and text with its OSError cause. The
// request carries a credential, which the error does not show. It holds
// through a RoundTripper and through the SDK's own transport, where the error
// arrives inside h2gate's DialError; internal/engine's
// TestRedactionKeepsCleanChains holds it at the scrub.
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
		if chainHolds(err, isStandIn) {
			t.Errorf("the chain of %v holds a stand-in, want none", err)
		}
	}
	t.Run("success: through a RoundTripper", func(t *testing.T) {
		want := unreachable()
		clearEnv(t)
		c := newEnvClient(t, testsupport.RoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, want }), WithAPIKey(key))
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
		c := mustClient(t, WithAPIKey(key), WithBaseURL("https://example.com"), WithHTTPTransport(tr))
		_, err := c.Models().List(t.Context(), Retry(NoRetry()))
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

// unwrapOnly is an error whose text leaves out the cause it wraps, as a
// Python exception's str() leaves out its __cause__ and __context__.
type unwrapOnly struct {
	msg   string
	cause error
}

func (e unwrapOnly) Error() string { return e.msg }
func (e unwrapOnly) Unwrap() error { return e.cause }

// plusOnly is an error that prints the cause it holds only under %+v and
// does not unwrap to it, as a formatter that shows a stack or a cause does.
type plusOnly struct {
	msg   string
	cause error
}

func (e plusOnly) Error() string { return e.msg }

func (e plusOnly) Format(f fmt.State, verb rune) {
	if verb == 'v' && f.Flag('+') {
		_, _ = fmt.Fprintf(f, "%s: %s", e.msg, e.cause)
		return
	}
	_, _ = io.WriteString(f, e.msg)
}

// TestScrubbedErrorFormat pins, through the client, for a key across the cut,
// that the credentials are replaced before the stand-in's rendering of the
// transport error's chain is cut, so no piece of the key that is long enough
// to be one of its needles is left at the edge of the cause's %+v or %#v.
// internal/engine's TestScrubbedErrorFormat pins the rendering at the scrub.
func TestScrubbedErrorFormat(t *testing.T) {
	const longKey = "ts_live_0123456789abcdefghij"
	prefixes := func(t *testing.T, what, out string) {
		t.Helper()
		for k := engine.MinKeyNeedleBytes; k <= len(longKey); k++ {
			if strings.Contains(out, longKey[:k]) {
				t.Errorf("%s holds %q, %d bytes of the key: %.60q…", what, longKey[:k], k, out[max(len(out)-80, 0):])
				return
			}
		}
	}
	// Through the client the rendering starts with "Illegal header value: "
	// and the key after one space, at character pad+23: these pads cut the key
	// after 8 to 27 of its bytes.
	for _, pad := range []int{975, 985, 993} {
		t.Run("success: the key after "+strconv.Itoa(pad)+" characters, through the client", func(t *testing.T) {
			rt := testsupport.RoundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, unwrapOnly{msg: "Illegal header value", cause: errors.New(strings.Repeat("p", pad) + " " + longKey)}
			})
			clearEnv(t)
			c := newEnvClient(t, rt, WithAPIKey(longKey))
			_, err := c.Models().List(t.Context(), Retry(NoRetry()))
			if err == nil {
				t.Fatal("List succeeded, want a transport failure")
			}
			prefixes(t, "%+v of the cause", fmt.Sprintf("%+v", errors.Unwrap(err)))
			prefixes(t, "%#v of the cause", fmt.Sprintf("%#v", errors.Unwrap(err)))
		})
	}
}
