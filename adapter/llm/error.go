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

package llm

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"unicode/utf8"
)

// maxErrorBodyChars is how many characters of a response body
// StatusError.Error prints before it cuts the body off: typesafe-sdk-python
// 0.7.0's MAX_ERROR_BODY_LENGTH (_core/constants.py:3).
const maxErrorBodyChars = 200

// StatusError is a provider's non-2xx response. It ports the TypeSafeAPIError
// that upstream's map_provider_error builds from a provider status error
// (error_handling.py:67-69).
// Print %p only on a pointer, where fmt prints its address. On a value,
// fmt's bad-verb diagnostic bypasses Format and may expose Header and Body.
// The same limitation applies to invalid %w uses: Sprintf with %w, or Errorf
// with a value rather than a pointer. Go vet reports these misuses when it
// knows the format and type; dynamic formats may escape that check.
// A value in a caller struct's unexported field also bypasses Format under
// %v and exposes Header and Body. Store the pointer or Error text there.
// JSON and structured logging use the bounded Error text, never Header.
type StatusError struct {
	// StatusCode is the response's HTTP status code.
	StatusCode int
	// Header is the response header, for Retry-After; never recorded in a
	// trace. Its keys are in the canonical form of
	// http.CanonicalHeaderKey, as http.Header's methods and a net/http
	// response keep them: the Adapter reads Retry-After and the request id
	// by their canonical names and does not find a key in another case.
	// The providers of this module keep only Retry-After, Retry-After-Ms
	// and X-Typesafe-Request-Id here and drop every other response header,
	// the vendors' own request ids included, so a retry policy's predicate
	// cannot read another header from them.
	Header http.Header
	// Body is the response body as received. For the Adapter's retry
	// reason, a nil Body is a response without a body and a Body that is
	// not nil and empty is an empty body; Error prints the two alike.
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

// String returns the status code alone, as "llm.StatusError(<status>)".
// Format uses it for verbs other than v, s and q, which print Error's bounded
// body text, and %#v, which prints GoString. No verb prints header values.
func (e StatusError) String() string {
	return "llm.StatusError(" + strconv.Itoa(e.StatusCode) + ")"
}

// GoString returns what %#v prints for e, a StatusError value or a pointer
// to one, with the status code only: a response header may carry a cookie
// or a credential and a body any text, so neither is printed. A nil
// *StatusError prints as <nil>.
func (e StatusError) GoString() string {
	return "llm.StatusError{StatusCode:" + strconv.Itoa(e.StatusCode) + "}"
}

// Format prints Error's bounded body text for v, s and q, GoString for %#v,
// and String for every other verb. It never prints header values. Width,
// precision and flags are ignored. The fmt package handles %T and %p itself
// before calling Format. A nil *StatusError prints as <nil>.
func (e StatusError) Format(f fmt.State, verb rune) {
	var text string
	switch verb {
	case 'v':
		if f.Flag('#') {
			text = e.GoString()
		} else {
			text = (&e).Error()
		}
	case 's', 'q':
		text = (&e).Error()
	default:
		text = e.String()
	}
	_, _ = fmt.Fprint(f, text)
}

// MarshalJSON returns the bounded Error text as a JSON string, without
// headers or the body tail. Invalid UTF-8 bytes are replaced by U+FFFD.
// It always returns a nil error.
func (e StatusError) MarshalJSON() ([]byte, error) {
	text := (&e).Error()
	out := make([]byte, 0, len(text)+2)
	out = append(out, '"')
	for _, r := range text {
		switch {
		case r == '"' || r == '\\':
			out = append(out, '\\', byte(r))
		case r < 0x20:
			out = append(out, '\\', 'u', '0', '0')
			if r < 0x10 {
				out = append(out, '0')
			}
			out = strconv.AppendInt(out, int64(r), 16)
		default:
			out = utf8.AppendRune(out, r)
		}
	}
	return append(out, '"'), nil
}

// LogValue returns the bounded Error text as a string value, without
// headers or the body tail.
func (e StatusError) LogValue() slog.Value {
	return slog.StringValue((&e).Error())
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
