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
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// Error is implemented by every error the SDK returns, and only by them:
// [*APIError], [*ConnectionError], [*TimeoutError],
// [*ResponseValidationError], [*ResponseTooLargeError], [*ConfigError] and
// [*InvalidRequestError]. Each is a distinct type that [errors.As] matches,
// and errors.As with a *typesafe.Error target matches any of them, as the
// Python SDK's TypeSafeError base class does. The one exception is a
// cancellation: a call whose context is cancelled returns ctx.Err()
// ([context.Canceled]) itself, as the Python SDK lets
// asyncio.CancelledError through.
//
// An SDK error is a value: where the Python SDK copies, pickles and rebuilds
// its exceptions (to send one to another process, say), a Go caller copies
// the struct an error points to (c := *e) and uses &c, which renders, reads
// and unwraps as the original does and shares its Header and Body. The
// struct value c is not itself an error, and fmt prints its fields, so print
// &c, never c. A caller may also hand the pointer to another goroutine,
// since no read of an error changes it. [errors.As] with a pointer to one of
// the seven types, or with a *Error, finds the error through any wrapping and
// returns the very pointer. Nothing is serialised.
//
// No error's text holds the API key, a request's state or a response body
// unescaped: text the SDK did not write is escaped and cut (the server's
// message at 200 characters, a name the server chose at 128, a field path
// at 320).
type Error interface {
	error
	// typesafeError keeps the interface to the SDK's own types.
	typesafeError()
}

// ConfigError reports something the caller configured that the SDK cannot
// use: a [ClientOption] that fails when the client is built (a missing or
// malformed API key, the base URL, the model, a timeout, a header name or
// value, the User-Agent product), or a question set that [Questions.Prepare]
// rejects. It is returned before any request is sent, and retrying cannot fix
// it.
//
// Error returns the message, which names what is wrong without repeating a
// credential: neither the API key nor the value of a header the caller set is
// ever part of it. Unwrap returns the
// errors it wraps, if any. A rejected question set wraps the failure behind
// the message, such as the syntax error in JSON content; its type is
// internal to the SDK, so only its text, which the message already carries,
// is of use to a caller. A condition a caller may want to branch on is
// wrapped as a sentinel error that the function returning it documents.
type ConfigError struct {
	msg  string
	errs []error
}

// newConfigError returns a *ConfigError with message msg that wraps errs.
func newConfigError(msg string, errs ...error) *ConfigError {
	return &ConfigError{msg: msg, errs: errs}
}

// Error returns the message.
func (e *ConfigError) Error() string { return e.msg }

// Unwrap returns the wrapped errors, or nil when there are none.
func (e *ConfigError) Unwrap() []error { return e.errs }

func (*ConfigError) typesafeError() {}

// InvalidRequestError reports a request the SDK refuses to send because the
// API cannot accept it or its body cannot be written as JSON: a state that
// is not text, a JSON object or an array (nil, a number, a boolean); a
// plain []byte, whose intent is ambiguous (send string(b) for text or
// RawJSON(b) for JSON); text that is not valid UTF-8; or a value JSON has no
// form for, such as NaN, a channel or a cyclic structure. It is returned
// before any request is sent, and retrying the same request cannot fix it.
// A request the server itself refuses, with status 400 or 422 for example,
// is an [*APIError] instead.
//
// Error returns the message; the part of it that quotes the encoder is
// escaped and cut at 200 characters, so it never carries the state. Unwrap
// returns the cause. When the encoder failed, the chain continues to the
// encoder's own error, and through it to the error a caller's MarshalJSON
// method returned, which [errors.Is] and [errors.As] reach; the encoder's
// own error types are not part of the SDK's API and may change with it.
type InvalidRequestError struct {
	msg string
	err error
}

// newInvalidRequestError returns an *InvalidRequestError with message msg
// that wraps err.
func newInvalidRequestError(msg string, err error) *InvalidRequestError {
	return &InvalidRequestError{msg: msg, err: err}
}

// Error returns the message.
func (e *InvalidRequestError) Error() string { return e.msg }

// Unwrap returns the cause, or nil when there is none.
func (e *InvalidRequestError) Unwrap() error { return e.err }

func (*InvalidRequestError) typesafeError() {}

// APIErrorKind classifies an unsuccessful response by its status, as the
// Python SDK's subclasses of TypeSafeAPIError do. The mapping is by status
// alone, so a documented status with an undocumented body still lands in its
// class.
type APIErrorKind uint8

// The kinds of [*APIError].
const (
	// APIErrorOther is any status without a class of its own: 408, 409 or a
	// redirect the transport did not follow, for example.
	APIErrorOther APIErrorKind = iota
	// APIErrorBadRequest is status 400: the request was invalid.
	APIErrorBadRequest
	// APIErrorAuthentication is status 401: authentication failed.
	APIErrorAuthentication
	// APIErrorPermissionDenied is status 403: access was denied. The API
	// also answers a request without a key with 403; see
	// [APIError.IsAuthentication].
	APIErrorPermissionDenied
	// APIErrorNotFound is status 404: the resource was not found.
	APIErrorNotFound
	// APIErrorUnprocessableEntity is status 422: the request failed the
	// server's validation.
	APIErrorUnprocessableEntity
	// APIErrorRateLimit is status 429: the rate limit was exceeded.
	APIErrorRateLimit
	// APIErrorInternalServer is any status from 500 up: the server failed.
	APIErrorInternalServer
)

// apiErrorKind returns the kind of an unsuccessful response with status.
func apiErrorKind(status int) APIErrorKind {
	switch {
	case status == http.StatusBadRequest:
		return APIErrorBadRequest
	case status == http.StatusUnauthorized:
		return APIErrorAuthentication
	case status == http.StatusForbidden:
		return APIErrorPermissionDenied
	case status == http.StatusNotFound:
		return APIErrorNotFound
	case status == http.StatusUnprocessableEntity:
		return APIErrorUnprocessableEntity
	case status == http.StatusTooManyRequests:
		return APIErrorRateLimit
	case status >= http.StatusInternalServerError:
		return APIErrorInternalServer
	default:
		return APIErrorOther
	}
}

// String returns the kind's name in words, such as "rate limit", or
// "other" for a value outside the enumeration.
func (k APIErrorKind) String() string {
	switch k {
	case APIErrorBadRequest:
		return "bad request"
	case APIErrorAuthentication:
		return "authentication"
	case APIErrorPermissionDenied:
		return "permission denied"
	case APIErrorNotFound:
		return "not found"
	case APIErrorUnprocessableEntity:
		return "unprocessable entity"
	case APIErrorRateLimit:
		return "rate limit"
	case APIErrorInternalServer:
		return "internal server error"
	default:
		return "other"
	}
}

// APIError reports an unsuccessful HTTP response: a status outside 2xx,
// with the body and headers it arrived with.
//
// Error renders "<Endpoint>: <StatusCode> <Message> (request_id=<id>)", each
// part left out when it is empty or absent, as the Python SDK's str() of
// TypeSafeAPIError does. The SDK builds Message from the body the way the
// Python SDK finds a message in it (the "error", "message" and "detail"
// members, or the body itself), escaped and cut at 200 characters; a message
// the caller sets is printed as it is.
type APIError struct {
	// Kind classifies StatusCode.
	Kind APIErrorKind
	// StatusCode is the HTTP status code.
	StatusCode int
	// Header is the response header as a new map in which each value of a
	// header that carries a credential is "***": a header named
	// Authorization, Proxy-Authorization, X-Api-Key, Api-Key, Cookie or
	// Set-Cookie, or whose name contains "token" or "secret" (compared
	// without regard to case), and a header with a value that holds the
	// client's API key when the key is at least 8 bytes long. Every other
	// header, Retry-After, Retry-After-Ms and X-Typesafe-Request-Id
	// included, is as the server sent it, so [APIError.RetryAfter] and
	// [APIError.RequestID] read it. The values of those other headers are
	// the response's own and must not be modified. typesafe-sdk-python
	// keeps the headers as they arrived and redacts them only in its logs.
	Header http.Header
	// Body is the response body as it arrived, or nil when it was empty or
	// over the size limit. It is shared, not copied, and must not be
	// modified.
	Body []byte
	// Endpoint is the request's method and URL without credentials, query
	// or fragment, such as "GET https://api.typesafe.ai/v1/models", or
	// empty when it is not known.
	Endpoint string
	// Message is the message Error prints after the status; empty prints
	// the status alone. A message the server composes is not redacted
	// (ruling R103-rev): when it echoes the client's API key, Message,
	// Error and %+v show the key, as the Python SDK's error does. Header and
	// the request id show "***" for a value that holds the key, in the
	// error and in the SDK's log records alike (R87).
	Message string
	// ErrorType is the server's machine-readable name for the failure, from
	// the body's detail.error_type, or empty. It is the server's text as it
	// arrived.
	ErrorType string
}

// Error returns "<Endpoint>: <StatusCode> <Message> (request_id=<id>)".
func (e *APIError) Error() string {
	return renderResponseError(e.Endpoint, e.StatusCode, e.Message, e.Header)
}

// RequestID returns the server's identifier for the request, from the
// x-typesafe-request-id response header, and whether the header was
// present; a repeated header's values are joined with ", ". It is the
// server's text as it arrived.
func (e *APIError) RequestID() (string, bool) { return requestID(e.Header) }

// RetryAfter returns how long the server asked the caller to wait before
// trying again, and whether it asked, as the Python SDK's parse_retry_after
// reads it: the retry-after-ms header in milliseconds, else Retry-After in
// seconds or as an HTTP date (a date already past is zero); a value that is
// not a number, negative, NaN or infinite gives no answer from that header.
// The wait is truncated to whole milliseconds. It is read for any status,
// not only 429. A date is read in the three formats RFC 9110 names
// (IMF-fixdate, RFC 850, asctime; [net/http.ParseTime]); the Python SDK's
// parser also takes a numeric zone such as +0000 and a missing weekday,
// which give no answer here (ruling R73).
func (e *APIError) RetryAfter() (time.Duration, bool) { return retryAfter(e.Header, time.Now()) }

// IsAuthentication reports whether the status or the server's error type
// names an authentication failure: status 401, or any status whose
// ErrorType is "authentication_error" (the API answers a request without a
// key with 403 and that error type).
func (e *APIError) IsAuthentication() bool {
	return e.Kind == APIErrorAuthentication || e.ErrorType == "authentication_error"
}

func (*APIError) typesafeError() {}

// newAPIError returns the *APIError for an unsuccessful response: its kind,
// its message read from the body by the lenient reader
// (codec.ReadErrorBody), escaped and cut at 200 characters, or "status code
// (no body)" for an empty or null body, and the response header with its
// credentials redacted by r ([headerRedactor], ruling R87). The message is
// not redacted (ruling R103-rev): a key the server echoes in it is shown, as
// the Python SDK shows it. Header and the request id show "***" for a value
// that holds the key, as the log records do (R87).
func newAPIError(meta *wire.ResponseMeta, endpoint string, r headerRedactor) *APIError {
	eb := codec.ReadErrorBody(meta.Body)
	msg := "status code (no body)"
	if !eb.NoBody {
		msg = safeMessage(eb.Message)
	}
	return &APIError{
		Kind:       apiErrorKind(meta.Status),
		StatusCode: meta.Status,
		Header:     r.header(meta.Header),
		Body:       meta.Body,
		Endpoint:   endpoint,
		Message:    msg,
		ErrorType:  eb.ErrorType,
	}
}

// ResponseValidationError reports a successful response whose body is not
// the response the SDK expected: not one JSON object, not valid UTF-8, a
// raw control character in a string, data after the object, nesting past
// the codec's 4096 levels, or a member that is missing or of the wrong
// kind. FieldPath names the first failure the Python SDK would report.
//
// Error renders "<Endpoint>: <StatusCode> Invalid response data at
// '<FieldPath>'. (request_id=<id>)", as the Python SDK's str() of
// TypeSafeAPIResponseValidationError does. Unwrap returns the decoder's
// failure, whose type is internal to the SDK. A failure of a typed decode
// ([DecodeAs], [Ask]) names FieldPath as the Python SDK's response model
// with one field per answer names it, such as "tone.choice", while the
// failure it wraps keeps the answer's place in the body,
// "answers.tone.choice" (ruling R99-rev).
type ResponseValidationError struct {
	// StatusCode is the HTTP status code.
	StatusCode int
	// Header is the response header as a new map with each credential's
	// value "***", as [APIError.Header] describes; the values of the other
	// headers are the response's own and must not be modified.
	Header http.Header
	// Body is the response body as it arrived. It is shared, not copied,
	// and must not be modified.
	Body []byte
	// Endpoint is the request's method and URL without credentials, query
	// or fragment, or empty when it is not known.
	Endpoint string
	// FieldPath is the path of the first failure as Error prints it: the
	// Python SDK's field_path, such as "answers.tone.confidence" or
	// "models[1].name", with "." for the body as a whole (Python writes the
	// empty string), and each name the server chose escaped and cut at 128
	// characters.
	FieldPath string

	err error
}

// Error returns "<Endpoint>: <StatusCode> Invalid response data at
// '<FieldPath>'. (request_id=<id>)".
func (e *ResponseValidationError) Error() string {
	return renderResponseError(e.Endpoint, e.StatusCode, "Invalid response data at '"+e.FieldPath+"'.", e.Header)
}

// Unwrap returns the decoder's failure, or nil.
func (e *ResponseValidationError) Unwrap() error { return e.err }

// RequestID returns the server's identifier for the request, as
// [APIError.RequestID] does.
func (e *ResponseValidationError) RequestID() (string, bool) { return requestID(e.Header) }

func (*ResponseValidationError) typesafeError() {}

// newResponseValidationError returns the *ResponseValidationError for a
// successful response whose body the decoder refused with err, with the
// response header's credentials redacted by r ([headerRedactor]). The
// path's names, an answer's name and a probability or legend key, are not
// redacted (ruling R103-rev): a key the server echoes in them is shown, as
// the Python SDK shows it. Header and the request id show "***" for a value
// that holds the key, as the log records do (R87).
func newResponseValidationError(meta *wire.ResponseMeta, endpoint string, r headerRedactor, err error) *ResponseValidationError {
	var path codec.FieldPath
	if de, ok := errors.AsType[*codec.DecodeError](err); ok {
		path = de.Path
	}
	return newResponseValidationErrorAt(meta, endpoint, r, err, renderFieldPath(path))
}

// newResponseValidationErrorAt is [newResponseValidationError] with the field
// path already rendered, as fieldPath, for a caller that renders it its own
// way ([typedError]) and so renders it once.
func newResponseValidationErrorAt(meta *wire.ResponseMeta, endpoint string, r headerRedactor, err error, fieldPath string) *ResponseValidationError {
	return &ResponseValidationError{
		StatusCode: meta.Status,
		Header:     r.header(meta.Header),
		Body:       meta.Body,
		Endpoint:   endpoint,
		FieldPath:  fieldPath,
		err:        err,
	}
}

// ResponseTooLargeError reports a successful response whose body is larger
// than the client's size limit (16 MiB unless configured otherwise): the
// body was not read past the limit and nothing was decoded. Retrying the
// same request cannot help. A failure status with a body over the limit is
// an [*APIError] with an empty body instead, which keeps its status and
// headers.
type ResponseTooLargeError struct {
	// StatusCode is the HTTP status code.
	StatusCode int
	// Header is the response header as a new map with each credential's
	// value "***", as [APIError.Header] describes; the values of the other
	// headers are the response's own and must not be modified.
	Header http.Header
	// Endpoint is the request's method and URL without credentials, query
	// or fragment, or empty when it is not known.
	Endpoint string
	// Limit is the size limit the body exceeded, in bytes.
	Limit int64
}

// Error returns "<Endpoint>: <StatusCode> The response body is larger than
// the limit of <Limit> bytes. (request_id=<id>)".
func (e *ResponseTooLargeError) Error() string {
	msg := "The response body is larger than the limit of " + strconv.FormatInt(e.Limit, 10) + " bytes."
	return renderResponseError(e.Endpoint, e.StatusCode, msg, e.Header)
}

// RequestID returns the server's identifier for the request, as
// [APIError.RequestID] does.
func (e *ResponseTooLargeError) RequestID() (string, bool) { return requestID(e.Header) }

func (*ResponseTooLargeError) typesafeError() {}

// newResponseTooLargeError returns the *ResponseTooLargeError for a
// successful response whose body passed limit, with the response header's
// credentials redacted by r ([headerRedactor]).
func newResponseTooLargeError(meta *wire.ResponseMeta, endpoint string, r headerRedactor, limit int64) *ResponseTooLargeError {
	return &ResponseTooLargeError{StatusCode: meta.Status, Header: r.header(meta.Header), Endpoint: endpoint, Limit: limit}
}

// ConnectionError reports a request that produced no HTTP response: the
// connection could not be made, failed or was lost, or a proxy refused it.
// Retrying may help.
//
// Error returns "Connection error: <cause>", the cause's text escaped, cut
// at 200 characters and with any credential of the request replaced. Unwrap
// returns the transport's error, unless it, or an error it wraps, showed a
// credential of the request in its text, its %+v or its %#v: then it
// returns a stand-in whose text has the credentials replaced and which
// unwraps only to the well-known errors the transport's error matched, such
// as [context.DeadlineExceeded], [io.ErrUnexpectedEOF] or a
// [syscall.Errno]. An error that points to the request, which only a
// caller's RoundTripper, dialer or response body can return, is kept, and
// errors.As reaches the caller's own request through it.
type ConnectionError struct {
	msg   string
	err   error
	proxy bool
}

// newConnectionError returns a *ConnectionError whose text is "Connection
// error: " and text, escaped and cut. text is the transport error's text
// with every credential already replaced by "***" ([credentials.redact]);
// cause is the error to unwrap to, a stand-in for the transport's error
// when its chain printed a credential ([credentials.cause]). proxy marks a
// failure of the proxy hop. The transport classification (transportError,
// Client.attemptError) does the redaction; this only renders.
func newConnectionError(text string, cause error, proxy bool) *ConnectionError {
	return &ConnectionError{msg: "Connection error: " + safeMessage(text), err: cause, proxy: proxy}
}

// Error returns "Connection error: <cause>".
func (e *ConnectionError) Error() string { return e.msg }

// Unwrap returns the transport's error, or its stand-in.
func (e *ConnectionError) Unwrap() error { return e.err }

// Proxy reports whether the failure was on the hop to the proxy rather than
// to the API.
func (e *ConnectionError) Proxy() bool { return e.proxy }

func (*ConnectionError) typesafeError() {}

// TimeoutError reports a request attempt that did not complete within its
// deadline. Retrying may help. It is not a [*ConnectionError], which
// errors.As tells apart; the Python SDK's TypeSafeAPITimeoutError is a
// subclass of its connection error.
type TimeoutError struct {
	// Timeout is the per-attempt timeout the attempt was given, or zero
	// when the deadline came from the caller's context alone.
	Timeout time.Duration

	err   error
	proxy bool
}

// Error returns "Request timed out (timeout=<seconds>s).", or "Request timed
// out." without a timeout, with " on the proxy hop" after "out" when the hop
// to the proxy timed out. The Python SDK's str() prints the timeout as a
// float without a unit ("timeout=10.0"); the Go port prints it with an "s"
// and without a trailing ".0" ("timeout=10s"), as the Rust port does.
func (e *TimeoutError) Error() string {
	msg := "Request timed out"
	if e.proxy {
		msg += " on the proxy hop"
	}
	if e.Timeout <= 0 {
		return msg + "."
	}
	return msg + " (timeout=" + strconv.FormatFloat(e.Timeout.Seconds(), 'f', -1, 64) + "s)."
}

// Proxy reports whether the attempt timed out on the hop to the proxy (the
// connection to it, or its TLS handshake) rather than to the API, as
// [ConnectionError.Proxy] reports a proxy failure.
func (e *TimeoutError) Proxy() bool { return e.proxy }

// Unwrap returns the error that ended the attempt, such as
// context.DeadlineExceeded, or its stand-in when its chain printed a
// credential of the request, as [ConnectionError] describes.
func (e *TimeoutError) Unwrap() error { return e.err }

func (*TimeoutError) typesafeError() {}

// newTimeoutError returns the *TimeoutError for an attempt given timeout
// that ended with cause.
func newTimeoutError(timeout time.Duration, cause error) *TimeoutError {
	return &TimeoutError{Timeout: timeout, err: cause}
}

// newProxyTimeoutError returns the *TimeoutError for an attempt given
// timeout whose hop to the proxy timed out with cause (R67 Q3).
func newProxyTimeoutError(timeout time.Duration, cause error) *TimeoutError {
	return &TimeoutError{Timeout: timeout, err: cause, proxy: true}
}

// renderResponseError renders an error about a response as the Python SDK's
// TypeSafeAPIError.__str__ does: "<endpoint>: <status> <message>
// (request_id=<id>)", without the endpoint when it is empty, the status
// when it is zero (a payload read back by UnmarshalJSON), the message when
// it is empty, and the request id when the header is absent. The request id
// is escaped and cut at 128 characters.
func renderResponseError(endpoint string, status int, message string, h http.Header) string {
	b := make([]byte, 0, len(endpoint)+len(message)+48)
	if endpoint != "" {
		b = append(b, endpoint...)
		b = append(b, ": "...)
	}
	if status != 0 {
		b = strconv.AppendInt(b, int64(status), 10)
		if message != "" {
			b = append(b, ' ')
		}
	}
	b = append(b, message...)
	if id, ok := requestID(h); ok {
		b = append(b, " (request_id="...)
		b = appendSafeText(b, id, maxNameChars, false)
		b = append(b, ')')
	}
	return string(b)
}

// requestID returns the x-typesafe-request-id header of h.
func requestID(h http.Header) (string, bool) {
	meta := wire.ResponseMeta{Header: h}
	return meta.RequestID()
}

// endpointOf renders a request's endpoint as the errors name it: the method,
// then the URL without userinfo, query or fragment (a credential may sit in
// either), as the Python SDK's _request_endpoint does.
func endpointOf(method string, u *url.URL) string {
	return method + " " + u.Scheme + "://" + u.Host + u.EscapedPath()
}

// The headers a server asks a caller to wait with.
const (
	retryAfterMsHeader = "Retry-After-Ms"
	retryAfterHeader   = "Retry-After"
)

// retryAfter is [APIError.RetryAfter] at the time now: the Python SDK's
// parse_retry_after, in which retry-after-ms counts milliseconds and
// Retry-After seconds, and only Retry-After may be an HTTP date. A value
// that does not parse, is NaN or infinite, or whose milliseconds overflow,
// hands over to the next header; a negative Retry-After ends the search,
// a negative retry-after-ms does not.
func retryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	for _, hdr := range [...]struct {
		name    string
		perUnit float64
	}{{retryAfterMsHeader, 1}, {retryAfterHeader, 1000}} {
		values := h.Values(hdr.name)
		if len(values) == 0 {
			continue
		}
		raw := strings.Join(values, ", ")
		spelled := strings.TrimSpace(raw)
		if spelled == "" {
			spelled = "0"
		}
		v, ok := parsePythonFloat(spelled)
		switch {
		case !ok:
			if hdr.name == retryAfterHeader {
				if when, err := http.ParseTime(strings.TrimSpace(raw)); err == nil {
					return max(when.Sub(now), 0).Truncate(time.Millisecond), true
				}
			}
		case math.IsNaN(v) || math.IsInf(v, 0):
		case v >= 0:
			if ms := v * hdr.perUnit; !math.IsInf(ms, 0) {
				return millisToDuration(ms), true
			}
		case hdr.name == retryAfterHeader:
			return 0, false
		}
	}
	return 0, false
}

// millisToDuration turns a finite, non-negative count of milliseconds into a
// Duration truncated to whole milliseconds, saturating at the largest
// Duration.
func millisToDuration(ms float64) time.Duration {
	whole := math.Trunc(ms)
	if whole >= float64(math.MaxInt64/int64(time.Millisecond)) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(whole) * time.Millisecond
}

// parsePythonFloat parses s as Python's float() parses a string that has no
// surrounding whitespace: an optional sign, then "inf", "infinity" or "nan"
// in any case, or a decimal with an optional fraction and exponent in which
// a single underscore may sit between two digits. A value past float64's
// range is an infinity or zero, as in Python. Python also takes digits of
// other scripts; this does not.
func parsePythonFloat(s string) (float64, bool) {
	body := s
	neg := false
	if body != "" && (body[0] == '+' || body[0] == '-') {
		neg = body[0] == '-'
		body = body[1:]
	}
	switch strings.ToLower(body) {
	case "inf", "infinity":
		if neg {
			return math.Inf(-1), true
		}
		return math.Inf(1), true
	case "nan":
		return math.NaN(), true
	}
	clean := make([]byte, 0, len(s))
	if neg {
		clean = append(clean, '-')
	}
	i := 0
	digits := func() bool { // one or more digits, single underscores between them
		start := len(clean)
		for i < len(body) {
			c := body[i]
			switch {
			case '0' <= c && c <= '9':
				clean = append(clean, c)
				i++
			case c == '_' && len(clean) > start && i+1 < len(body) && '0' <= body[i+1] && body[i+1] <= '9':
				i++
			default:
				return len(clean) > start
			}
		}
		return len(clean) > start
	}
	intPart := digits()
	frac := false
	if i < len(body) && body[i] == '.' {
		clean = append(clean, '.')
		i++
		frac = digits()
	}
	if !intPart && !frac {
		return 0, false
	}
	if i < len(body) && (body[i] == 'e' || body[i] == 'E') {
		clean = append(clean, 'e')
		i++
		if i < len(body) && (body[i] == '+' || body[i] == '-') {
			clean = append(clean, body[i])
			i++
		}
		if !digits() {
			return 0, false
		}
	}
	if i != len(body) {
		return 0, false
	}
	v, err := strconv.ParseFloat(string(clean), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	return v, true
}
