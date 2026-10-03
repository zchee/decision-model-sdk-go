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

package rest

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"slices"
	"syscall"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// ErrBodyTooLarge reports a response of status 200 to 299 whose body is
// longer than the 64 MiB a Client reads. It is none of the error types of
// package llm, so the Adapter does not retry it.
var ErrBodyTooLarge = errors.New("rest: response body is longer than 64 MiB")

// ErrURL reports a base URL that is not an absolute http or https URL with
// a host.
var ErrURL = errors.New("rest: base URL is not an absolute http or https URL with a host")

// The fixed phrases an error text of this package ends with.
const (
	phraseBuild       = "the request could not be built"
	phraseTimeout     = "the request timed out"
	phraseConnection  = "the request failed without a response"
	phraseBodyTimeout = "reading the response body timed out"
	phraseBody        = "reading the response body failed"
)

// transportError is the Err of an *llm.TimeoutError or *llm.ConnectionError
// that Post returns, and the error Post returns when no request could be
// built. Its text is fixed but for the URL, which is written without
// userinfo, query and fragment, and its cause is nil or one error value of
// causes or a syscall.Errno: never the HTTP client's error, whose text
// holds the whole URL.
type transportError struct {
	text  string
	cause error
}

// Error returns the fixed text.
func (e *transportError) Error() string { return e.text }

// Unwrap returns the cause, nil for none.
func (e *transportError) Unwrap() error { return e.cause }

// errorText returns the text of a transportError: "rest: POST <target>:
// <phrase>", target being a URL that redact wrote.
func errorText(target, phrase string) string {
	return "rest: POST " + target + ": " + phrase
}

// causes are the error values a transportError keeps as its cause when the
// HTTP client's error matches one with errors.Is, in the order they are
// tried: the ends of a deadline, of a stream and of a connection, and a
// cancellation that is not the caller's.
var causes = [...]error{context.DeadlineExceeded, os.ErrDeadlineExceeded, io.ErrUnexpectedEOF, io.EOF, net.ErrClosed, context.Canceled}

// causeOf returns the one error value a transportError keeps of err: the
// syscall.Errno in its chain when there is one, else the first of causes
// it matches, else nil.
func causeOf(err error) error {
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return errno
	}
	for _, cause := range causes {
		if errors.Is(err, cause) {
			return cause
		}
	}
	return nil
}

// classify returns the error Post returns for err, the error of the HTTP
// client or of a read of the response body. ctx is the caller's context and
// rctx the request's, which ends with ctx or when the Client's timeout
// passes. The order is that of system-one-adapter-python v0.2.1's
// map_provider_error, which tests a timeout before a connection error
// (src/system_one_adapter/_utils/error_handling.py:65-71), after the
// caller's cancellation:
//
//   - ctx cancelled: ctx.Err() itself;
//   - rctx ended, which is then a deadline of ctx or the Client's timeout:
//     an *llm.TimeoutError with timeoutPhrase whose cause is
//     context.DeadlineExceeded, whatever err says;
//   - err matches context.DeadlineExceeded or os.ErrDeadlineExceeded, or an
//     error in its chain has a Timeout method that returns true, as a
//     net.Error that timed out has: an *llm.TimeoutError with
//     timeoutPhrase, whose cause is context.DeadlineExceeded when err has
//     no cause of its own to keep;
//   - anything else: an *llm.ConnectionError with failurePhrase.
func classify(ctx, rctx context.Context, target, timeoutPhrase, failurePhrase string, err error) error {
	if cerr := ctx.Err(); errors.Is(cerr, context.Canceled) {
		return cerr
	}
	cause := causeOf(err)
	switch {
	case rctx.Err() != nil:
		cause = context.DeadlineExceeded
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded), reportsTimeout(err):
		if cause == nil || errors.Is(cause, context.Canceled) {
			// A timeout's chain holds a deadline, never a cancellation: the
			// caller's context is not cancelled here.
			cause = context.DeadlineExceeded
		}
	default:
		return &llm.ConnectionError{Err: &transportError{text: errorText(target, failurePhrase), cause: cause}}
	}
	return &llm.TimeoutError{Err: &transportError{text: errorText(target, timeoutPhrase), cause: cause}}
}

// reportsTimeout reports whether err or an error its chain wraps
// (errors.Unwrap, both forms) has a Timeout method that returns true, as a
// net.Error that timed out has. Every link is asked, not the first that has
// the method: the *url.Error the HTTP client returns asks only the error it
// holds directly, so it answers false for a transport that wrapped its
// timeout in another error.
func reportsTimeout(err error) bool {
	if t, ok := err.(interface{ Timeout() bool }); ok && t.Timeout() {
		return true
	}
	switch u := err.(type) { //nolint:errorlint // the chain is walked link by link.
	case interface{ Unwrap() error }:
		inner := u.Unwrap()
		return inner != nil && reportsTimeout(inner)
	case interface{ Unwrap() []error }:
		return slices.ContainsFunc(u.Unwrap(), func(inner error) bool { return inner != nil && reportsTimeout(inner) })
	}
	return false
}
