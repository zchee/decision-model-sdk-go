// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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

package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// reportCanary is a text that the Report of the errors below holds in every
// place a provider's text can reach, and that no printed form of an Error
// may contain.
const reportCanary = "report-canary-6b1f"

// canaryReport returns a Report whose texts hold reportCanary.
func canaryReport() *Report {
	return &Report{Debug: Debug{
		Attempts: []Attempt{{
			Messages: []llm.Message{{Role: "user", Content: reportCanary}},
			Response: []byte(`{"error":"` + reportCanary + `"}`),
			Info:     AttemptInfo{ModelName: "m", Provider: "p", Error: reportCanary, ErrorType: "TypeSafeError"},
		}},
		RetryReasons: []RetryReason{{Category: categoryProviderError, Message: reportCanary}},
	}}
}

// TestErrorPrintsFixedText checks that every fmt verb prints an Error's
// fixed text and nothing else, for each kind, with and without the
// provider and model, alone and wrapped: the SDK keeps an error in the chain
// it returns only when no link prints the call's key, so the Report's texts
// must never be printed.
func TestErrorPrintsFixedText(t *testing.T) {
	tests := map[string]struct {
		err  *Error
		want string
	}{
		"timeout with provider and model": {
			err:  &Error{Kind: KindTimeout, Provider: "openai", Model: "gpt-x", Report: canaryReport()},
			want: "adapter: timeout from openai model gpt-x",
		},
		"connection failure with provider and model": {
			err:  &Error{Kind: KindConnection, Provider: "anthropic", Model: "claude-x", Report: canaryReport(), cause: syscall.ECONNREFUSED},
			want: "adapter: connection failure from anthropic model claude-x",
		},
		"closed with provider and model": {
			err:  &Error{Kind: KindClosed, Provider: "mine", Model: "m"},
			want: "adapter: closed from mine model m",
		},
		"timeout without provider and model": {
			err:  &Error{Kind: KindTimeout, Report: canaryReport()},
			want: "adapter: timeout",
		},
		"connection failure without provider and model": {
			err:  &Error{Kind: KindConnection, Report: canaryReport(), cause: io.ErrUnexpectedEOF},
			want: "adapter: connection failure",
		},
		"closed without provider and model": {
			err:  &Error{Kind: KindClosed},
			want: "adapter: closed",
		},
		"provider only": {
			err:  &Error{Kind: KindTimeout, Provider: "openai"},
			want: "adapter: timeout from openai model ",
		},
		"unknown kind": {
			err:  &Error{Kind: 9, Provider: "openai", Model: "gpt-x"},
			want: "adapter: ErrorKind(9) from openai model gpt-x",
		},
	}
	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%10v", "%-30s", "%.3s"}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Fatalf("Error() = %q, want %q", got, tt.want)
			}
			wrapped := fmt.Errorf("call: %w", tt.err)
			for _, verb := range verbs {
				if got := fmt.Sprintf(verb, tt.err); got != tt.want {
					t.Errorf("Sprintf(%q, err) = %q, want %q", verb, got, tt.want)
				}
				if got, want := fmt.Sprintf(verb, wrapped), "call: "+tt.want; verb == "%v" && got != want {
					t.Errorf("Sprintf(%q, wrapped) = %q, want %q", verb, got, want)
				}
				for _, v := range []any{tt.err, wrapped} {
					if got := fmt.Sprintf(verb, v); strings.Contains(got, reportCanary) {
						t.Errorf("Sprintf(%q, %T) = %q holds the Report's text", verb, v, got)
					}
				}
			}
		})
	}
}

// TestErrorUnwrapsASentinel checks the cause each kind wraps: the deadline
// for a timeout, ErrClosed for a closed Adapter, and the sentinel kept for a
// connection failure, which may be none.
func TestErrorUnwrapsASentinel(t *testing.T) {
	tests := map[string]struct {
		err   *Error
		want  error
		isAll []error
	}{
		"timeout": {
			err:   &Error{Kind: KindTimeout},
			want:  context.DeadlineExceeded,
			isAll: []error{context.DeadlineExceeded},
		},
		"timeout ignores a cause": {
			err:   &Error{Kind: KindTimeout, cause: io.EOF},
			want:  context.DeadlineExceeded,
			isAll: []error{context.DeadlineExceeded},
		},
		"closed": {
			err:   &Error{Kind: KindClosed},
			want:  ErrClosed,
			isAll: []error{ErrClosed},
		},
		"connection failure with an errno": {
			err:   &Error{Kind: KindConnection, cause: syscall.ECONNRESET},
			want:  syscall.ECONNRESET,
			isAll: []error{syscall.ECONNRESET},
		},
		"connection failure without a cause": {
			err: &Error{Kind: KindConnection},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.err.Unwrap(); !errors.Is(got, tt.want) || (tt.want == nil) != (got == nil) {
				t.Errorf("Unwrap() = %v, want %v", got, tt.want)
			}
			for _, target := range tt.isAll {
				if !errors.Is(fmt.Errorf("wrapped: %w", tt.err), target) {
					t.Errorf("errors.Is(wrapped, %v) = false", target)
				}
			}
			if errors.Is(tt.err, ErrClosed) != (tt.err.Kind == KindClosed) {
				t.Errorf("errors.Is(err, ErrClosed) = %v for kind %v", errors.Is(tt.err, ErrClosed), tt.err.Kind)
			}
		})
	}
}

// TestErrorIsNetError checks that *Error is a net.Error whose Timeout is
// true only for KindTimeout, which the SDK reads through errors.As to build
// a *decision.TimeoutError, and whose Temporary is false.
func TestErrorIsNetError(t *testing.T) {
	tests := map[string]struct {
		kind        ErrorKind
		wantTimeout bool
	}{
		"timeout":            {kind: KindTimeout, wantTimeout: true},
		"connection failure": {kind: KindConnection},
		"closed":             {kind: KindClosed},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := fmt.Errorf("transport: %w", &Error{Kind: tt.kind, Provider: "p", Model: "m"})
			ne, ok := errors.AsType[net.Error](err)
			if !ok {
				t.Fatalf("errors.As(%v, net.Error) = false", err)
			}
			if got := ne.Timeout(); got != tt.wantTimeout {
				t.Errorf("Timeout() = %v, want %v", got, tt.wantTimeout)
			}
			if ne.Temporary() { //nolint:staticcheck // SA1019: Temporary is the method net.Error requires.
				t.Error("Temporary() = true, want false")
			}
		})
	}
}

// TestReportFromErrorFindsTheAdapterError checks that ReportFromError
// returns the Report an Error holds, alone or wrapped, and false for an
// Error without one, as a closed Adapter's is.
func TestReportFromErrorFindsTheAdapterError(t *testing.T) {
	report := canaryReport()
	tests := map[string]struct {
		err    error
		want   *Report
		wantOK bool
	}{
		"timeout": {
			err:    &Error{Kind: KindTimeout, Provider: "p", Model: "m", Report: report},
			want:   report,
			wantOK: true,
		},
		"connection failure, wrapped twice": {
			err:    fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", &Error{Kind: KindConnection, Provider: "p", Model: "m", Report: report})),
			want:   report,
			wantOK: true,
		},
		"closed, no Report": {
			err: &Error{Kind: KindClosed, cause: ErrClosed},
		},
		"timeout without a Report": {
			err: fmt.Errorf("wrapped: %w", &Error{Kind: KindTimeout}),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := ReportFromError(tt.err)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("ReportFromError() = %p, %v; want %p, %v", got, ok, tt.want, tt.wantOK)
			}
			if ok {
				if diff := gocmp.Diff(canaryReport(), got); diff != "" {
					t.Errorf("Report (-want +got):\n%s", diff)
				}
			}
		})
	}
}
