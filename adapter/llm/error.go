// Copyright 2026 The typesafe-sdk-go Authors.
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

package llm

import (
	"context"
	"net/http"
	"strconv"
)

// maxErrorBodyChars is how many characters of a response body
// StatusError.Error prints before it cuts the body off: typesafe-sdk-python
// 0.7.0's MAX_ERROR_BODY_LENGTH (_core/constants.py:3).
const maxErrorBodyChars = 200

// StatusError is a provider's non-2xx response. It ports the TypeSafeAPIError
// that upstream's map_provider_error builds from a provider status error
// (error_handling.py:67-69).
type StatusError struct {
	// StatusCode is the response's HTTP status code.
	StatusCode int
	// Header is the response header, for Retry-After; never recorded in a
	// trace.
	Header http.Header
	// Body is the response body as received.
	Body []byte
}

// Error returns "<status> <body>", the text typesafe-sdk-python 0.7.0's
// TypeSafeAPIError.__str__ prints for a body whose message it does not
// extract (_core/errors.py:86-100): "<status> status code (no body)" for an
// empty body, otherwise the body cut at 200 characters, with "…" appended
// when it was cut. This package reads no JSON, so the message member of a
// JSON error body is not extracted here.
func (e *StatusError) Error() string {
	b := strconv.AppendInt(make([]byte, 0, 16+min(len(e.Body), 4*maxErrorBodyChars)), int64(e.StatusCode), 10)
	b = append(b, ' ')
	if len(e.Body) == 0 {
		return string(append(b, "status code (no body)"...))
	}
	body, n := string(e.Body), 0
	for i := range body {
		if n == maxErrorBodyChars {
			return string(append(append(b, body[:i]...), "…"...))
		}
		n++
	}
	return string(append(b, body...))
}

// GoString returns the Go syntax %#v prints for e, with the status code
// only: a response header may carry a cookie or a credential and a body
// any text, so neither is printed.
func (e *StatusError) GoString() string {
	if e == nil {
		return "(*llm.StatusError)(nil)"
	}
	return "&llm.StatusError{StatusCode:" + strconv.Itoa(e.StatusCode) + "}"
}

// TimeoutError is a provider request that timed out. It ports the
// TypeSafeAPITimeoutError that upstream's map_provider_error builds for a
// provider timeout (error_handling.py:65-66). It is a net.Error whose
// Timeout is true, and errors.Is(err, context.DeadlineExceeded) holds for it
// whatever Err is.
type TimeoutError struct{ Err error }

// timeoutText is str() of the TypeSafeAPITimeoutError(httpx2.Timeout(None))
// that upstream builds for every provider timeout: typesafe-sdk-python's
// "Request timed out (timeout={timeout})." (_core/errors.py:168) with
// httpx2's repr of a Timeout whose four settings are None.
const timeoutText = "Request timed out (timeout=Timeout(timeout=None))."

// Error returns upstream's fixed text for a provider timeout, "Request timed
// out (timeout=Timeout(timeout=None)).", whatever Err is.
func (e *TimeoutError) Error() string { return timeoutText }

// Unwrap returns Err, or context.DeadlineExceeded when Err is nil.
func (e *TimeoutError) Unwrap() error {
	if e.Err == nil {
		return context.DeadlineExceeded
	}
	return e.Err
}

// Is reports whether target is context.DeadlineExceeded, so that the chain
// of a TimeoutError always holds it, also when Err is a provider's own
// timeout error that does not.
func (e *TimeoutError) Is(target error) bool { return target == context.DeadlineExceeded }

// Timeout returns true.
func (e *TimeoutError) Timeout() bool { return true }

// Temporary returns false; it exists so that *TimeoutError is a net.Error.
func (e *TimeoutError) Temporary() bool { return false }

// ConnectionError is a provider request that failed without a response. It
// ports the TypeSafeAPIConnectionError that upstream's map_provider_error
// builds for a provider connection error (error_handling.py:70-71).
type ConnectionError struct{ Err error }

// connectionText is the message of the APIConnectionError that the OpenAI,
// Anthropic and Gemini Python SDKs raise for a request without a response,
// which upstream's map_provider_error copies with str(error).
const connectionText = "Connection error."

// Error returns upstream's text for a provider connection failure,
// "Connection error.", whatever Err is.
func (e *ConnectionError) Error() string { return connectionText }

// Unwrap returns Err.
func (e *ConnectionError) Unwrap() error { return e.Err }

// NonAnswerError is a completed provider response that is not an answer
// (finish reason, refusal, truncation, incomplete status, missing usage).
// Upstream raises a plain TypeSafeError for it, which its retry policy never
// retries.
type NonAnswerError struct{ Message string }

// Error returns Message.
func (e *NonAnswerError) Error() string { return e.Message }
