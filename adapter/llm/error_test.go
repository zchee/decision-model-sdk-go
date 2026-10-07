// Copyright 2026 The decision-model-sdk-go Authors.
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

package llm_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// errProviderTimeout is a provider's own timeout error that does not wrap
// context.DeadlineExceeded, as an HTTP client of a custom provider may
// return.
var errProviderTimeout = errors.New("provider: read timeout after 600s")

// TestTimeoutErrorIsNetError checks that every *llm.TimeoutError, bare and
// wrapped, is a net.Error whose Timeout is true and Temporary false, holds
// context.DeadlineExceeded in its chain whatever its Err is, keeps its Err
// reachable, and prints upstream's fixed text.
func TestTimeoutErrorIsNetError(t *testing.T) {
	tests := map[string]struct {
		cause error
		wrap  bool
	}{
		"success: nil Err":                       {cause: nil},
		"success: context.DeadlineExceeded":      {cause: context.DeadlineExceeded},
		"success: os.ErrDeadlineExceeded":        {cause: os.ErrDeadlineExceeded},
		"success: provider error without one":    {cause: errProviderTimeout},
		"success: syscall.ETIMEDOUT":             {cause: syscall.ETIMEDOUT},
		"success: wrapped by the provider":       {cause: errProviderTimeout, wrap: true},
		"success: nil Err wrapped by a provider": {cause: nil, wrap: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			te := &llm.TimeoutError{Err: tt.cause}
			var err error = te
			if tt.wrap {
				err = fmt.Errorf("openai: %w", te)
			}

			var ne net.Error
			if !errors.As(err, &ne) {
				t.Fatalf("errors.As(%v, *net.Error) = false, want true", err)
			}
			if !ne.Timeout() {
				t.Error("Timeout() = false, want true")
			}
			if ne.Temporary() { //nolint:staticcheck // SA1019: the test checks the method net.Error still declares.
				t.Error("Temporary() = true, want false")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("errors.Is(%v, context.DeadlineExceeded) = false, want true", err)
			}
			if tt.cause != nil && !errors.Is(err, tt.cause) {
				t.Errorf("errors.Is(%v, %v) = false, want true: Err must stay reachable", err, tt.cause)
			}
			wantUnwrap := tt.cause
			if wantUnwrap == nil {
				wantUnwrap = context.DeadlineExceeded
			}
			if got := te.Unwrap(); got != wantUnwrap { //nolint:errorlint // Unwrap returns Err itself, or the sentinel itself.
				t.Errorf("Unwrap() = %v, want %v itself", got, wantUnwrap)
			}
			var got *llm.TimeoutError
			if !errors.As(err, &got) || got != te {
				t.Errorf("errors.As(%v, **llm.TimeoutError) = %p, want %p", err, got, te)
			}
			if diff := cmp.Diff("Request timed out (timeout=Timeout(timeout=None)).", te.Error()); diff != "" {
				t.Errorf("Error() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestErrorsIsAs checks what errors.Is and errors.As find in the chain of
// each error type, wrapped once by a provider: the type itself, the cause of
// the two that wrap one, and no context error where none belongs.
func TestErrorsIsAs(t *testing.T) {
	status := &llm.StatusError{StatusCode: http.StatusServiceUnavailable, Body: []byte(`{"m":"unavailable"}`)}
	conn := &llm.ConnectionError{Err: io.ErrUnexpectedEOF}
	connNil := &llm.ConnectionError{}
	nonAnswer := &llm.NonAnswerError{Message: "The model refused to answer."}
	timeout := &llm.TimeoutError{Err: errProviderTimeout}

	tests := map[string]struct {
		err              error
		wantCause        error // reachable with errors.Is; nil for none
		wantDeadline     bool
		wantNetError     bool
		wantStatus       *llm.StatusError
		wantConnection   *llm.ConnectionError
		wantNonAnswer    *llm.NonAnswerError
		wantTimeoutError *llm.TimeoutError
	}{
		"success: StatusError": {
			err:        status,
			wantStatus: status,
		},
		"success: ConnectionError with a cause": {
			err:            conn,
			wantCause:      io.ErrUnexpectedEOF,
			wantConnection: conn,
		},
		"success: ConnectionError without a cause": {
			err:            connNil,
			wantConnection: connNil,
		},
		"success: NonAnswerError": {
			err:           nonAnswer,
			wantNonAnswer: nonAnswer,
		},
		"success: TimeoutError": {
			err:              timeout,
			wantCause:        errProviderTimeout,
			wantDeadline:     true,
			wantNetError:     true,
			wantTimeoutError: timeout,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := fmt.Errorf("provider: %w", tt.err)

			if tt.wantCause != nil && !errors.Is(err, tt.wantCause) {
				t.Errorf("errors.Is(%v, %v) = false, want true", err, tt.wantCause)
			}
			if got := errors.Is(err, context.DeadlineExceeded); got != tt.wantDeadline {
				t.Errorf("errors.Is(%v, context.DeadlineExceeded) = %t, want %t", err, got, tt.wantDeadline)
			}
			if got := errors.Is(err, context.Canceled); got {
				t.Errorf("errors.Is(%v, context.Canceled) = true, want false", err)
			}
			var ne net.Error
			if got := errors.As(err, &ne); got != tt.wantNetError {
				t.Errorf("errors.As(%v, *net.Error) = %t, want %t", err, got, tt.wantNetError)
			}

			var (
				gotStatus     *llm.StatusError
				gotConnection *llm.ConnectionError
				gotNonAnswer  *llm.NonAnswerError
				gotTimeout    *llm.TimeoutError
			)
			errors.As(err, &gotStatus)
			errors.As(err, &gotConnection)
			errors.As(err, &gotNonAnswer)
			errors.As(err, &gotTimeout)
			if gotStatus != tt.wantStatus {
				t.Errorf("errors.As(**llm.StatusError) = %p, want %p", gotStatus, tt.wantStatus)
			}
			if gotConnection != tt.wantConnection {
				t.Errorf("errors.As(**llm.ConnectionError) = %p, want %p", gotConnection, tt.wantConnection)
			}
			if gotNonAnswer != tt.wantNonAnswer {
				t.Errorf("errors.As(**llm.NonAnswerError) = %p, want %p", gotNonAnswer, tt.wantNonAnswer)
			}
			if gotTimeout != tt.wantTimeoutError {
				t.Errorf("errors.As(**llm.TimeoutError) = %p, want %p", gotTimeout, tt.wantTimeoutError)
			}
		})
	}
}

// TestErrorText checks each type's Error text against the text upstream
// records for the error it ports (str(error) of the translated SDK error):
// a status error's "<status> <body>" with typesafe-sdk-python's 200-character
// cut and its no-body text, and the fixed texts of a timeout and a
// connection failure, which never print their cause.
func TestErrorText(t *testing.T) {
	long := strings.Repeat("x", 200)
	wide := strings.Repeat("é", 200)

	tests := map[string]struct {
		err  error
		want string
	}{
		"success: status with a JSON body": {
			err:  &llm.StatusError{StatusCode: 503, Body: []byte(`{"m":"unavailable"}`)},
			want: `503 {"m":"unavailable"}`,
		},
		"success: status with a nil body": {
			err:  &llm.StatusError{StatusCode: 502},
			want: "502 status code (no body)",
		},
		"success: status with an empty body": {
			err:  &llm.StatusError{StatusCode: 429, Body: []byte{}},
			want: "429 status code (no body)",
		},
		"success: status with a 200-character body is not cut": {
			err:  &llm.StatusError{StatusCode: 500, Body: []byte(long)},
			want: "500 " + long,
		},
		"success: status with a 201-character body is cut": {
			err:  &llm.StatusError{StatusCode: 500, Body: []byte(long + "y")},
			want: "500 " + long + "…",
		},
		"success: status body cut by characters, not bytes": {
			err:  &llm.StatusError{StatusCode: 400, Body: []byte(wide + "é")},
			want: "400 " + wide + "…",
		},
		"success: status header is not printed": {
			err:  &llm.StatusError{StatusCode: 401, Header: http.Header{"Retry-After": {"30"}}, Body: []byte("denied")},
			want: "401 denied",
		},
		"success: timeout never prints its cause": {
			err:  &llm.TimeoutError{Err: errors.New("dial https://user:canary@example.test/?key=canary")},
			want: "Request timed out (timeout=Timeout(timeout=None)).",
		},
		"success: connection failure never prints its cause": {
			err:  &llm.ConnectionError{Err: errors.New("dial https://user:canary@example.test/?key=canary")},
			want: "Connection error.",
		},
		"success: connection failure without a cause": {
			err:  &llm.ConnectionError{},
			want: "Connection error.",
		},
		"success: non-answer prints its message": {
			err:  &llm.NonAnswerError{Message: "The response was cut off by max_output_tokens."},
			want: "The response was cut off by max_output_tokens.",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, tt.err.Error()); diff != "" {
				t.Errorf("Error() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestStatusErrorGoString checks that %#v of a StatusError, as a value and
// as a pointer, prints its status code and nothing of its header or body,
// which may carry a cookie, a credential or any text, and that a nil
// pointer prints as <nil>.
func TestStatusErrorGoString(t *testing.T) {
	const canary = "canary-7f3a"
	full := llm.StatusError{
		StatusCode: http.StatusUnauthorized,
		Header: http.Header{
			"Set-Cookie":    {"session=" + canary},
			"Authorization": {"Bearer " + canary},
			"Retry-After":   {"30"},
		},
		Body: []byte(`{"error":{"message":"` + canary + `"}}`),
	}
	tests := map[string]struct {
		v    any
		want string
	}{
		"success: pointer, header and body not printed": {v: &full, want: "llm.StatusError{StatusCode:401}"},
		"success: value, header and body not printed":   {v: full, want: "llm.StatusError{StatusCode:401}"},
		"success: no header and no body":                {v: &llm.StatusError{StatusCode: http.StatusServiceUnavailable}, want: "llm.StatusError{StatusCode:503}"},
		"success: nil pointer":                          {v: (*llm.StatusError)(nil), want: "<nil>"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := fmt.Sprintf("%#v", tt.v)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("%%#v mismatch (-want +got):\n%s", diff)
			}
			if strings.Contains(got, canary) {
				t.Errorf("%%#v = %q prints a header value or a body byte", got)
			}
		})
	}
}

// TestStatusErrorFormat checks every verb on values, pointers and interfaces.
// The v, s and q verbs print the bounded Error text; other verbs print the
// status only, with GoString for %#v. No verb prints headers or the body tail.
func TestStatusErrorFormat(t *testing.T) {
	const (
		headerCanary = "silverorchard"
		cookieCanary = "peachthistle"
		bodyCanary   = "saffroncloud"
		tailCanary   = "rosemarydrift"
		short        = "llm.StatusError(401)"
		goSyntax     = "llm.StatusError{StatusCode:401}"
	)
	boundedBody := strings.Repeat("é", 200-len(bodyCanary)) + bodyCanary
	full := llm.StatusError{
		StatusCode: http.StatusUnauthorized,
		Header:     http.Header{"Set-Cookie": {cookieCanary}, "Authorization": {headerCanary}},
		Body:       []byte(boundedBody + tailCanary),
	}
	errText := "401 " + boundedBody + "…"
	if diff := cmp.Diff(errText, (&full).Error()); diff != "" {
		t.Fatalf("Error() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(short, full.String()); diff != "" {
		t.Errorf("String() mismatch (-want +got):\n%s", diff)
	}
	checkPlanted := func(t *testing.T, out string) {
		t.Helper()
		for name, canary := range map[string]string{"header": headerCanary, "cookie": cookieCanary, "body tail": tailCanary} {
			if strings.Contains(out, canary) {
				t.Errorf("output prints %s", name)
			}
		}
	}
	var boxedError error = &full
	var boxedAny any = full
	tests := map[string]struct {
		v          any
		nilPointer bool
		value      bool
	}{
		"success: value":           {v: full, value: true},
		"success: pointer":         {v: &full},
		"success: error interface": {v: boxedError},
		"success: any interface":   {v: boxedAny, value: true},
		"success: nil pointer":     {v: (*llm.StatusError)(nil), nilPointer: true},
	}
	// Each format is static so vet checks it, including unsupported verbs.
	formats := map[string]struct{ print func(any) string }{
		"v": {print: func(v any) string {
			return fmt.Sprintf("%v\n%20v\n%-20v\n%020v\n%+20v\n%#20v\n%.3v", v, v, v, v, v, v, v)
		}},
		"s": {print: func(v any) string {
			return fmt.Sprintf("%s\n%20s\n%-20s\n%020s\n%+20s\n%#20s\n%.3s", v, v, v, v, v, v, v)
		}},
		"q": {print: func(v any) string {
			return fmt.Sprintf("%q\n%20q\n%-20q\n%020q\n%+20q\n%#20q\n%.3q", v, v, v, v, v, v, v)
		}},
		"x": {print: func(v any) string {
			return fmt.Sprintf("%x\n%20x\n%-20x\n%020x\n%+20x\n%#20x\n%.3x", v, v, v, v, v, v, v)
		}},
		"X": {print: func(v any) string {
			return fmt.Sprintf("%X\n%20X\n%-20X\n%020X\n%+20X\n%#20X\n%.3X", v, v, v, v, v, v, v)
		}},
		"d": {print: func(v any) string {
			return fmt.Sprintf("%d\n%20d\n%-20d\n%020d\n%+20d\n%#20d\n%.3d", v, v, v, v, v, v, v)
		}},
		"t": {print: func(v any) string {
			return fmt.Sprintf("%t\n%20t\n%-20t\n%020t\n%+20t\n%#20t\n%.3t", v, v, v, v, v, v, v)
		}},
		"o": {print: func(v any) string {
			return fmt.Sprintf("%o\n%20o\n%-20o\n%020o\n%+20o\n%#20o\n%.3o", v, v, v, v, v, v, v)
		}},
		"O": {print: func(v any) string {
			return fmt.Sprintf("%O\n%20O\n%-20O\n%020O\n%+20O\n%#20O\n%.3O", v, v, v, v, v, v, v)
		}},
		"b": {print: func(v any) string {
			return fmt.Sprintf("%b\n%20b\n%-20b\n%020b\n%+20b\n%#20b\n%.3b", v, v, v, v, v, v, v)
		}},
		"c": {print: func(v any) string {
			return fmt.Sprintf("%c\n%20c\n%-20c\n%020c\n%+20c\n%#20c\n%.3c", v, v, v, v, v, v, v)
		}},
		"U": {print: func(v any) string {
			return fmt.Sprintf("%U\n%20U\n%-20U\n%020U\n%+20U\n%#20U\n%.3U", v, v, v, v, v, v, v)
		}},
		"e": {print: func(v any) string {
			return fmt.Sprintf("%e\n%20e\n%-20e\n%020e\n%+20e\n%#20e\n%.3e", v, v, v, v, v, v, v)
		}},
		"E": {print: func(v any) string {
			return fmt.Sprintf("%E\n%20E\n%-20E\n%020E\n%+20E\n%#20E\n%.3E", v, v, v, v, v, v, v)
		}},
		"f": {print: func(v any) string {
			return fmt.Sprintf("%f\n%20f\n%-20f\n%020f\n%+20f\n%#20f\n%.3f", v, v, v, v, v, v, v)
		}},
		"F": {print: func(v any) string {
			return fmt.Sprintf("%F\n%20F\n%-20F\n%020F\n%+20F\n%#20F\n%.3F", v, v, v, v, v, v, v)
		}},
		"g": {print: func(v any) string {
			return fmt.Sprintf("%g\n%20g\n%-20g\n%020g\n%+20g\n%#20g\n%.3g", v, v, v, v, v, v, v)
		}},
		"G": {print: func(v any) string {
			return fmt.Sprintf("%G\n%20G\n%-20G\n%020G\n%+20G\n%#20G\n%.3G", v, v, v, v, v, v, v)
		}},
		"z": {print: func(v any) string {
			return fmt.Sprintf("%z\n%20z\n%-20z\n%020z\n%+20z\n%#20z\n%.3z", v, v, v, v, v, v, v)
		}},
		"p": {print: func(v any) string {
			return fmt.Sprintf("%p\n%20p\n%-20p\n%020p\n%+20p\n%#20p\n%.3p", v, v, v, v, v, v, v)
		}},
		"T": {print: func(v any) string {
			return fmt.Sprintf("%T\n%20T\n%-20T\n%020T\n%+20T\n%#20T\n%.3T", v, v, v, v, v, v, v)
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			for modifier, out := range map[string]string{"plus": fmt.Sprintf("%+v", tt.v), "sharp": fmt.Sprintf("%#v", tt.v)} {
				want := errText
				if modifier == "sharp" {
					want = goSyntax
				}
				if tt.nilPointer {
					want = "<nil>"
				}
				checkPlanted(t, out)
				if diff := cmp.Diff(want, out); diff != "" {
					t.Errorf("%s mismatch (-want +got):\n%s", modifier, diff)
				}
			}
			for verb, format := range formats {
				t.Run(verb, func(t *testing.T) {
					out := format.print(tt.v)
					if verb == "p" && tt.value {
						// A value's %p is fmt's bad-verb path, which bypasses
						// Format. This detects it; no safety is claimed for it.
						for item := range strings.SplitSeq(out, "\n") {
							if !strings.Contains(item, "%!p(llm.StatusError={") || !strings.HasSuffix(item, "})") {
								t.Error("missing fmt value-pointer diagnostic")
							}
						}
						return
					}
					checkPlanted(t, out)
					if verb == "p" || verb == "T" {
						return
					}
					text := short
					if verb == "v" || verb == "s" || verb == "q" {
						text = errText
					}
					want := []string{text, text, text, text, text, text, text}
					if verb == "v" {
						want[5] = goSyntax
					}
					if tt.nilPointer {
						for i := range want {
							want[i] = "<nil>"
						}
					}
					if diff := cmp.Diff(want, strings.Split(out, "\n")); diff != "" {
						t.Errorf("plain/width/left/zero/plus/sharp/precision mismatch (-want +got):\n%s", diff)
					}
				})
			}
		})
	}
	containers := map[string]struct{ got, want string }{
		"success: value inside a slice":          {got: fmt.Sprintf("%v", []any{full}), want: "[" + errText + "]"},
		"success: plus value inside a slice":     {got: fmt.Sprintf("%+v", []any{full}), want: "[" + errText + "]"},
		"success: string value inside a slice":   {got: fmt.Sprintf("%s", []any{full}), want: "[" + errText + "]"},
		"success: GoString value inside a slice": {got: fmt.Sprintf("%#v", []any{full}), want: "[]interface {}{" + goSyntax + "}"},
		"success: wrapped error":                 {got: fmt.Errorf("provider: %w", boxedError).Error(), want: "provider: " + errText},
	}
	for name, tt := range containers {
		t.Run(name, func(t *testing.T) {
			checkPlanted(t, tt.got)
			if diff := cmp.Diff(tt.want, tt.got); diff != "" {
				t.Errorf("text mismatch (-want +got):\n%s", diff)
			}
		})
	}
	handlers := map[string]struct{ handler func(io.Writer) slog.Handler }{
		"success: slog text error attributes": {handler: func(w io.Writer) slog.Handler { return slog.NewTextHandler(w, nil) }},
		"success: slog JSON error attributes": {handler: func(w io.Writer) slog.Handler { return slog.NewJSONHandler(w, nil) }},
	}
	for name, tt := range handlers {
		t.Run(name, func(t *testing.T) {
			var buf strings.Builder
			slog.New(tt.handler(&buf)).LogAttrs(t.Context(), slog.LevelInfo, "error", slog.Any("pointer", &full), slog.Any("error", boxedError), slog.Any("any", any(&full)))
			out := buf.String()
			checkPlanted(t, out)
			if strings.Count(out, errText) != 3 {
				t.Error("each error attribute must print the bounded Error text")
			}
		})
	}
}
