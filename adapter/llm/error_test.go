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

package llm_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/adapter/llm"
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

// TestStatusErrorFormat checks what the fmt verbs print for a StatusError:
// a value prints its status code and nothing of its header or body, which
// may carry a cookie, a credential or any text; a pointer, which is an
// error, prints its Error text for %v, %+v and %s, with the body and no
// header value; a nil pointer prints as <nil>.
func TestStatusErrorFormat(t *testing.T) {
	const (
		headerCanary = "canary-header-7f3a"
		bodyCanary   = "canary-body-91c2"
	)
	full := llm.StatusError{
		StatusCode: http.StatusUnauthorized,
		Header: http.Header{
			"Set-Cookie":    {"session=" + headerCanary},
			"Authorization": {"Bearer " + headerCanary},
		},
		Body: []byte(bodyCanary),
	}
	const (
		short    = "llm.StatusError(401)"
		goSyntax = "llm.StatusError{StatusCode:401}"
		errText  = "401 " + bodyCanary
	)
	if diff := cmp.Diff(errText, (&full).Error()); diff != "" {
		t.Fatalf("Error() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(short, full.String()); diff != "" {
		t.Errorf("String() mismatch (-want +got):\n%s", diff)
	}

	tests := map[string]struct {
		v        any
		want     map[string]string // by verb
		wantBody bool              // the body is printed, by design
	}{
		"success: value prints the status only": {
			v:    full,
			want: map[string]string{"%v": short, "%+v": short, "%s": short, "%#v": goSyntax},
		},
		"success: pointer prints its Error text": {
			v:        &full,
			want:     map[string]string{"%v": errText, "%+v": errText, "%s": errText, "%#v": goSyntax},
			wantBody: true,
		},
		"success: nil pointer": {
			v:    (*llm.StatusError)(nil),
			want: map[string]string{"%v": "<nil>", "%+v": "<nil>", "%s": "<nil>", "%#v": "<nil>"},
		},
		"success: value inside a slice": {
			v:    []any{full},
			want: map[string]string{"%v": "[" + short + "]", "%+v": "[" + short + "]", "%s": "[" + short + "]", "%#v": "[]interface {}{" + goSyntax + "}"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			for verb, want := range tt.want {
				got := fmt.Sprintf(verb, tt.v)
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("%s mismatch (-want +got):\n%s", verb, diff)
				}
				if strings.Contains(got, headerCanary) {
					t.Errorf("%s = %q prints a header value", verb, got)
				}
				if !tt.wantBody && strings.Contains(got, bodyCanary) {
					t.Errorf("%s = %q prints a body byte", verb, got)
				}
			}
		})
	}
}
