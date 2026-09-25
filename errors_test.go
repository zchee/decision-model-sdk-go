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
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// errSentinel stands for a sentinel error that a later wave may wrap in a
// *ConfigError next to the cause (the port plan's section 6.3 pattern, W2.0
// decides): Unwrap() []error must reach both.
var errSentinel = errors.New("sentinel")

// causeError is a cause with a type of its own, reached with errors.As.
type causeError struct{ detail string }

func (e *causeError) Error() string { return "cause: " + e.detail }

func TestConfigError(t *testing.T) {
	cause := &causeError{detail: "dial"}
	tests := map[string]struct {
		err        *ConfigError
		wantMsg    string
		wantUnwrap []error
		wantIs     []error // errors.Is holds for each
		wantNotIs  []error // and does not hold for each
		wantCause  bool    // errors.As reaches *causeError
	}{
		"success: a message alone": {
			err:       newConfigError("At least one question is required."),
			wantMsg:   "At least one question is required.",
			wantNotIs: []error{errSentinel, fs.ErrNotExist},
		},
		"success: a sentinel and a cause": {
			err:        newConfigError("HTTP/2 was not negotiated", errSentinel, cause),
			wantMsg:    "HTTP/2 was not negotiated",
			wantUnwrap: []error{errSentinel, cause},
			wantIs:     []error{errSentinel, cause},
			wantNotIs:  []error{fs.ErrNotExist},
			wantCause:  true,
		},
		"success: a wrapped cause": {
			err:        newConfigError("bad question", fmt.Errorf("wrapped: %w", fs.ErrNotExist)),
			wantMsg:    "bad question",
			wantUnwrap: []error{fmt.Errorf("wrapped: %w", fs.ErrNotExist)},
			wantIs:     []error{fs.ErrNotExist},
			wantNotIs:  []error{errSentinel},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", got, tt.wantMsg)
			}
			// Errors compare by identity, or by text for the freshly wrapped
			// cause of the third case.
			sameError := gocmp.Comparer(func(a, b error) bool { return errors.Is(a, b) || a.Error() == b.Error() })
			if diff := gocmp.Diff(tt.wantUnwrap, tt.err.Unwrap(), sameError); diff != "" {
				t.Errorf("Unwrap() mismatch (-want +got):\n%s", diff)
			}
			// Through the error interface and one more wrap, as a caller
			// receives it.
			err := fmt.Errorf("call: %w", tt.err)
			for _, target := range tt.wantIs {
				if !errors.Is(err, target) {
					t.Errorf("errors.Is(%v, %v) = false, want true", err, target)
				}
			}
			for _, target := range tt.wantNotIs {
				if errors.Is(err, target) {
					t.Errorf("errors.Is(%v, %v) = true, want false", err, target)
				}
			}
			var ce *ConfigError
			if !errors.As(err, &ce) || ce != tt.err {
				t.Errorf("errors.As(*ConfigError) = %v, want %v", ce, tt.err)
			}
			var got *causeError
			if found := errors.As(err, &got); found != tt.wantCause {
				t.Errorf("errors.As(*causeError) = %t, want %t", found, tt.wantCause)
			}
		})
	}
}

// apiError builds the *APIError the SDK builds for a response with status,
// body and header, at endpoint.
func apiError(status int, body string, header http.Header, endpoint string) *APIError {
	return newAPIError(&wire.ResponseMeta{Status: status, Header: header, Body: []byte(body)}, endpoint)
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// headers returns an http.Header with the pairs set as a server sends them.
func headers(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

// TestAPIErrorStatusRows checks the rendering half of test_error_mapping
// (tests/test_clients.py:287-317, AC-F3): the kind each of the eleven
// statuses maps to, the exact Error() text, the request id and the
// retry-after-ms wait. The call through the client is W2.3's (C11).
func TestAPIErrorStatusRows(t *testing.T) {
	const body = `{"detail":{"message":"Server explanation"}}`
	tests := map[string]struct {
		status int
		kind   APIErrorKind
	}{
		"success: 400": {status: 400, kind: APIErrorBadRequest},
		"success: 401": {status: 401, kind: APIErrorAuthentication},
		"success: 403": {status: 403, kind: APIErrorPermissionDenied},
		"success: 404": {status: 404, kind: APIErrorNotFound},
		"success: 422": {status: 422, kind: APIErrorUnprocessableEntity},
		"success: 429": {status: 429, kind: APIErrorRateLimit},
		"success: 500": {status: 500, kind: APIErrorInternalServer},
		"success: 503": {status: 503, kind: APIErrorInternalServer},
		"success: 408": {status: 408, kind: APIErrorOther},
		"success: 409": {status: 409, kind: APIErrorOther},
		"success: 302": {status: 302, kind: APIErrorOther},
	}
	endpoint := endpointOf(http.MethodGet, mustURL(t, "https://api.typesafe.ai/v1/models"))
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := headers("X-Typesafe-Request-Id", "req_123", "Retry-After-Ms", "125")
			e := apiError(tt.status, body, h, endpoint)
			if e.Kind != tt.kind || e.StatusCode != tt.status {
				t.Errorf("kind %v status %d, want %v %d", e.Kind, e.StatusCode, tt.kind, tt.status)
			}
			want := "GET https://api.typesafe.ai/v1/models: " + strconv.Itoa(tt.status) + " Server explanation (request_id=req_123)"
			if diff := gocmp.Diff(want, e.Error()); diff != "" {
				t.Errorf("Error() (-want +got):\n%s", diff)
			}
			if id, ok := e.RequestID(); !ok || id != "req_123" {
				t.Errorf("RequestID() = %q, %t", id, ok)
			}
			if d, ok := e.RetryAfter(); !ok || d != 125*time.Millisecond {
				t.Errorf("RetryAfter() = %v, %t, want 125ms", d, ok)
			}
			if string(e.Body) != body || e.Header.Get("Retry-After-Ms") != "125" {
				t.Errorf("body %q, retry-after-ms %q", e.Body, e.Header.Get("Retry-After-Ms"))
			}
		})
	}
}

// TestAPIErrorMessageRows checks the rendering half of test_error_messages
// (tests/test_clients.py:320-339, AC-F3): the message the Python SDK finds
// in each of the eight bodies, exactly. The call through the client is
// W2.3's (C12).
func TestAPIErrorMessageRows(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"success: error first":           {body: `{"error":"error","message":"message","detail":"detail"}`, want: "error"},
		"success: error.message":         {body: `{"error":{"message":"nested error"},"message":"message"}`, want: "nested error"},
		"success: message before detail": {body: `{"message":"message","detail":"detail"}`, want: "message"},
		"success: detail":                {body: `{"detail":"detail"}`, want: "detail"},
		"success: detail.message":        {body: `{"detail":{"message":"nested detail"}}`, want: "nested detail"},
		"success: detail list":           {body: `{"detail":[{"loc":["body","questions","q","score","criteria",0],"msg":"Invalid"},{"msg":"Missing"},{}]}`, want: "questions.q.score.criteria.0: Invalid; Missing"},
		"success: plain text":            {body: "plain text", want: "plain text"},
		"success: a body without one":    {body: `{"unexpected":true}`, want: `{"unexpected":true}`},
	}
	endpoint := endpointOf(http.MethodGet, mustURL(t, "https://api.typesafe.ai/v1/models"))
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := apiError(400, tt.body, http.Header{}, endpoint).Error()
			if diff := gocmp.Diff("GET https://api.typesafe.ai/v1/models: 400 "+tt.want, got); diff != "" {
				t.Errorf("Error() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAPIErrorRendersEndpointStatusMessageRequestID ports
// test_api_error_request_context (E3, tests/test_errors.py:83-101): the
// endpoint of either resource under a base URL with a path prefix, the
// status, the message and the request id, in one line, and nothing of the
// request's credentials in any rendering of the error.
func TestAPIErrorRendersEndpointStatusMessageRequestID(t *testing.T) {
	tests := map[string]struct {
		method, path string
		want         string
	}{
		"success: models":     {method: http.MethodGet, path: "/v1/models", want: "GET https://api.example.test/prefix/v1/models"},
		"success: system_one": {method: http.MethodPost, path: "/v1/systemone", want: "POST https://api.example.test/prefix/v1/systemone"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			endpoint := endpointOf(tt.method, mustURL(t, "https://api.example.test/prefix"+tt.path))
			e := apiError(429, `{"message":"Too many requests"}`, headers("X-Typesafe-Request-Id", "req-context"), endpoint)
			want := tt.want + ": 429 Too many requests (request_id=req-context)"
			if e.Endpoint != tt.want {
				t.Errorf("Endpoint = %q, want %q", e.Endpoint, tt.want)
			}
			if diff := gocmp.Diff(want, e.Error()); diff != "" {
				t.Errorf("Error() (-want +got):\n%s", diff)
			}
			var err error = e
			for _, rendered := range []string{fmt.Sprint(err), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
				if strings.Contains(rendered, "private-api-key") {
					t.Errorf("a rendering holds the key: %s", rendered)
				}
			}
			if got := fmt.Errorf("call: %w", err).Error(); got != "call: "+want {
				t.Errorf("wrapped = %q", got)
			}
		})
	}
}

// TestEndpointOmitsCredentialsQueryFragment ports
// test_api_error_endpoint_omits_url_credentials (E4,
// tests/test_errors.py:104-110): the endpoint keeps the method, scheme, host
// and path, and drops the userinfo, query and fragment.
func TestEndpointOmitsCredentialsQueryFragment(t *testing.T) {
	tests := map[string]struct {
		method, raw string
		want        string
	}{
		//nolint:gosec // G101: upstream's own fake credential, which the endpoint must drop.
		"success: E4's URL":                   {method: http.MethodGet, raw: "https://user:password@example.test/v1/models?token=secret#fragment", want: "GET https://example.test/v1/models"},
		"success: a port and an escaped path": {method: http.MethodPost, raw: "https://u@api.example.test:8443/a%20b/v1/systemone?x=1", want: "POST https://api.example.test:8443/a%20b/v1/systemone"},
		"success: a user without a password":  {method: http.MethodGet, raw: "https://token@example.test/v1/models", want: "GET https://example.test/v1/models"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			endpoint := endpointOf(tt.method, mustURL(t, tt.raw))
			if endpoint != tt.want {
				t.Fatalf("endpointOf = %q, want %q", endpoint, tt.want)
			}
			e := apiError(400, `{"message":"Bad request"}`, http.Header{}, endpoint)
			if diff := gocmp.Diff(tt.want+": 400 Bad request", e.Error()); diff != "" {
				t.Errorf("Error() (-want +got):\n%s", diff)
			}
			for _, secret := range []string{"password", "secret", "fragment", "token="} {
				if strings.Contains(e.Error(), secret) {
					t.Errorf("Error() %q holds %q", e.Error(), secret)
				}
			}
		})
	}
}

// TestAPIErrorMessageOverride ports test_message_override (E5,
// tests/test_errors.py:113-137): a message set by the caller replaces the
// body's, an empty one leaves the status alone, and the status, body and
// headers are the ones given. The Python SDK reads retry_after_ms on its
// rate-limit class only; RetryAfter answers for every kind (as the Rust
// port's retry_after does).
func TestAPIErrorMessageOverride(t *testing.T) {
	kinds := []APIErrorKind{APIErrorOther, APIErrorBadRequest, APIErrorAuthentication, APIErrorPermissionDenied, APIErrorNotFound, APIErrorUnprocessableEntity, APIErrorRateLimit, APIErrorInternalServer}
	tests := map[string]struct {
		message string
		want    string
	}{
		"success: a custom explanation": {message: "A custom explanation", want: "429 A custom explanation"},
		"success: an empty message":     {message: "", want: "429"},
	}
	for name, tt := range tests {
		for _, kind := range kinds {
			t.Run(name+"/"+kind.String(), func(t *testing.T) {
				h := headers("Retry-After-Ms", "125")
				body := []byte(`{"message":"Server explanation"}`)
				e := &APIError{Kind: kind, StatusCode: 429, Header: h, Body: body, Message: tt.message}
				if got := e.Error(); got != tt.want {
					t.Errorf("Error() = %q, want %q", got, tt.want)
				}
				if e.StatusCode != http.StatusTooManyRequests || &e.Body[0] != &body[0] {
					t.Errorf("status %d, body not the one given", e.StatusCode)
				}
				h.Set("X-Probe", "1")
				if e.Header.Get("X-Probe") != "1" {
					t.Error("header is not the one given")
				}
				if id, ok := e.RequestID(); ok {
					t.Errorf("RequestID() = %q, want absent", id)
				}
				if d, ok := e.RetryAfter(); !ok || d != 125*time.Millisecond {
					t.Errorf("RetryAfter() = %v, %t, want 125ms", d, ok)
				}
			})
		}
	}
}

// TestAPIErrorBodyEdgeCases ports test_error_body_edge_cases (E6,
// tests/test_errors.py:140-158): eight of the nine rows render exactly as
// the Python SDK's; long-plain-message is cut at 200 characters, where the
// Python SDK never cuts a body that is not JSON (Appendix B, NF7).
func TestAPIErrorBodyEdgeCases(t *testing.T) {
	x := strings.Repeat("x", 201)
	tests := map[string]struct {
		body string
		want string
	}{
		"success: empty":                           {body: "", want: "400 status code (no body)"},
		"success: null":                            {body: "null", want: "400 status code (no body)"},
		"success: empty array":                     {body: "[]", want: "400 []"},
		"success: number":                          {body: "42", want: "400 42"},
		"success: not JSON, invalid UTF-8":         {body: "not JSON: \xff", want: "400 not JSON: \ufffd"},
		"success: deviation, long-plain-message":   {body: x, want: "400 " + x[:200] + "…"},
		"success: long-unstructured-body":          {body: `{"unknown":"` + x + `"}`, want: `400 {"unknown":"` + x[:188] + "…"},
		"success: an empty error stops the search": {body: `{"error":"","message":"ignored"}`, want: `400 {"error":"","message":"ignored"}`},
		"success: detail entries without a msg":    {body: `{"detail":[null,42,{"msg":4}]}`, want: `400 {"detail":[null,42,{"msg":4}]}`},
	}
	endpoint := endpointOf(http.MethodGet, mustURL(t, "https://api.typesafe.ai/v1/models"))
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := apiError(400, tt.body, http.Header{}, endpoint)
			if diff := gocmp.Diff("GET https://api.typesafe.ai/v1/models: "+tt.want, e.Error()); diff != "" {
				t.Errorf("Error() (-want +got):\n%s", diff)
			}
			if id, ok := e.RequestID(); ok {
				t.Errorf("RequestID() = %q, want absent", id)
			}
		})
	}
}

// TestAPIErrorMessageIsBounded checks NF7 on a server's message: control and
// format characters are escaped and the message is cut at 200 characters
// after escaping, whichever member it came from; the request id is escaped
// and cut at 128.
func TestAPIErrorMessageIsBounded(t *testing.T) {
	long := strings.Repeat("y", 500)
	tests := map[string]struct {
		body string
		id   string
		want string
	}{
		"success: a long message member":   {body: `{"message":"` + long + `"}`, want: "400 " + long[:200] + "…"},
		"success: control characters":      {body: `{"message":"a\nb\u001b[31mred"}`, want: `400 a\nb\x1b[31mred`},
		"success: a raw control in text":   {body: "bad\x1b[2Jtext", want: `400 bad\x1b[2Jtext`},
		"success: a request id is escaped": {body: `{"message":"m"}`, id: "req\tx", want: `400 m (request_id=req\tx)`},
		"success: a long request id":       {body: `{"message":"m"}`, id: strings.Repeat("r", 200), want: "400 m (request_id=" + strings.Repeat("r", 128) + "…)"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := http.Header{}
			if tt.id != "" {
				h.Set("X-Typesafe-Request-Id", tt.id)
			}
			if diff := gocmp.Diff(tt.want, apiError(400, tt.body, h, "").Error()); diff != "" {
				t.Errorf("Error() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestIsAuthentication checks that 401, and any status whose error type is
// authentication_error (the API's answer to a request without a key is 403
// with it), is an authentication failure, and that a 403 without it is not.
func TestIsAuthentication(t *testing.T) {
	tests := map[string]struct {
		status int
		body   string
		want   bool
	}{
		"success: 401":                           {status: 401, body: `{}`, want: true},
		"success: 403 with authentication_error": {status: 403, body: `{"detail":{"message":"Must supply an API key!","error_type":"authentication_error"}}`, want: true},
		"success: 403 without it":                {status: 403, body: `{"detail":{"message":"denied","error_type":"permission_error"}}`},
		"success: a top-level error_type":        {status: 403, body: `{"error_type":"authentication_error"}`},
		"success: 500":                           {status: 500, body: `{}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := apiError(tt.status, tt.body, http.Header{}, "").IsAuthentication(); got != tt.want {
				t.Errorf("IsAuthentication() = %t, want %t", got, tt.want)
			}
		})
	}
}

// TestRetryAfter ports test_parse_retry_after (tests/test_retry.py:234-248)
// and the date half of test_backoff_dates_cap_and_jitter (:251-255) to
// the Retry-After reading of every *APIError, and pins what Go adds: the
// wait is a Duration truncated to milliseconds and saturates.
func TestRetryAfter(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	future := now.Add(10 * time.Second).UTC().Format(http.TimeFormat)
	past := now.Add(-10 * time.Second).UTC().Format(http.TimeFormat)
	// RFC 2822 spellings the Python SDK's parsedate_to_datetime takes and
	// http.ParseTime does not (ruling R73: no answer, W3 may widen).
	numericZone := now.Add(10 * time.Second).UTC().Format(time.RFC1123Z)
	eastZone := now.Add(10 * time.Second).In(time.FixedZone("JST", 9*3600)).Format(time.RFC1123Z)
	noWeekday := now.Add(10 * time.Second).UTC().Format("02 Jan 2006 15:04:05 GMT")
	tests := map[string]struct {
		header []string
		want   time.Duration
		ok     bool
	}{
		"success: no header":                            {},
		"success: Retry-After bad":                      {header: []string{"Retry-After", "bad"}},
		"success: Retry-After -1":                       {header: []string{"Retry-After", "-1"}},
		"success: ms NaN, then Retry-After 1.5":         {header: []string{"Retry-After-Ms", "NaN", "Retry-After", "1.5"}, want: 1500 * time.Millisecond, ok: true},
		"success: ms -1, then Retry-After 2":            {header: []string{"Retry-After-Ms", "-1", "Retry-After", "2"}, want: 2 * time.Second, ok: true},
		"success: Retry-After empty":                    {header: []string{"Retry-After", ""}, want: 0, ok: true},
		"success: ms inf":                               {header: []string{"Retry-After-Ms", "inf"}},
		"success: ms bad, then Retry-After 2":           {header: []string{"Retry-After-Ms", "bad", "Retry-After", "2"}, want: 2 * time.Second, ok: true},
		"success: Retry-After 1e308":                    {header: []string{"Retry-After", "1e308"}},
		"success: a date in the future":                 {header: []string{"Retry-After", future}, want: 10 * time.Second, ok: true},
		"success: a date in the past":                   {header: []string{"Retry-After", past}, want: 0, ok: true},
		"success: ms wins over Retry-After":             {header: []string{"Retry-After-Ms", "125", "Retry-After", "9"}, want: 125 * time.Millisecond, ok: true},
		"success: ms truncated to whole milliseconds":   {header: []string{"Retry-After-Ms", "1.9"}, want: time.Millisecond, ok: true},
		"success: underscores as Python takes them":     {header: []string{"Retry-After-Ms", " 1_000 "}, want: time.Second, ok: true},
		"success: a huge wait saturates":                {header: []string{"Retry-After", "1e300"}, want: time.Duration(math.MaxInt64), ok: true},
		"success: a repeated header is not a number":    {header: []string{"Retry-After", "1", "Retry-After", "2"}},
		"success: hexadecimal is not a Python float":    {header: []string{"Retry-After-Ms", "0x10"}},
		"success: a date is read from Retry-After only": {header: []string{"Retry-After-Ms", future}},
		"success: deviation, a numeric zone +0000":      {header: []string{"Retry-After", numericZone}},
		"success: deviation, a numeric zone +0900":      {header: []string{"Retry-After", eastZone}},
		"success: deviation, a date without a weekday":  {header: []string{"Retry-After", noWeekday}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := retryAfter(headers(tt.header...), now)
			if got != tt.want || ok != tt.ok {
				t.Errorf("retryAfter = %v, %t, want %v, %t", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestParsePythonFloat checks the float() grammar RetryAfter reads numbers
// with.
func TestParsePythonFloat(t *testing.T) {
	tests := map[string]struct {
		s    string
		want float64
		ok   bool
	}{
		"success: integer":           {s: "2", want: 2, ok: true},
		"success: fraction":          {s: "1.5", want: 1.5, ok: true},
		"success: leading point":     {s: ".5", want: 0.5, ok: true},
		"success: trailing point":    {s: "5.", want: 5, ok: true},
		"success: exponent":          {s: "1e3", want: 1000, ok: true},
		"success: signed exponent":   {s: "-1.5E-2", want: -0.015, ok: true},
		"success: plus sign":         {s: "+7", want: 7, ok: true},
		"success: underscores":       {s: "1_000.000_1e1_0", want: 1000.0001e10, ok: true},
		"success: infinity":          {s: "-Infinity", want: math.Inf(-1), ok: true},
		"success: inf":               {s: "INF", want: math.Inf(1), ok: true},
		"success: past the range":    {s: "1e400", want: math.Inf(1), ok: true},
		"error: empty":               {s: ""},
		"error: point alone":         {s: "."},
		"error: double underscore":   {s: "1__0"},
		"error: leading underscore":  {s: "_1"},
		"error: trailing underscore": {s: "1_"},
		"error: underscore at point": {s: "1_.5"},
		"error: hexadecimal":         {s: "0x10"},
		"error: bare exponent":       {s: "1e"},
		"error: words":               {s: "bad"},
		"error: two numbers":         {s: "1, 2"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := parsePythonFloat(tt.s)
			if ok != tt.ok || (ok && got != tt.want) {
				t.Errorf("parsePythonFloat(%q) = %v, %t, want %v, %t", tt.s, got, ok, tt.want, tt.ok)
			}
		})
	}
	if v, ok := parsePythonFloat("nan"); !ok || !math.IsNaN(v) {
		t.Errorf("parsePythonFloat(nan) = %v, %t", v, ok)
	}
}

// TestErrorRenderings pins the Error() text of the types that render
// without a response body: *ResponseTooLargeError, *ConnectionError and
// *TimeoutError, and what they unwrap to; and of a *ResponseValidationError
// without a status, as UnmarshalJSON returns for a payload it refuses.
func TestErrorRenderings(t *testing.T) {
	cause := errors.New("dial tcp 192.0.2.1:443: i/o timeout")
	tests := map[string]struct {
		err       error
		want      string
		wantCause error
	}{
		"success: response too large": {
			err:  &ResponseTooLargeError{StatusCode: 200, Header: headers("X-Typesafe-Request-Id", "r"), Endpoint: "GET https://api.typesafe.ai/v1/models", Limit: 16 << 20},
			want: "GET https://api.typesafe.ai/v1/models: 200 The response body is larger than the limit of 16777216 bytes. (request_id=r)",
		},
		"success: connection error, escaped and cut": {
			err:       newConnectionError("proxyconnect tcp: \x1b[31m"+strings.Repeat("z", 300), cause, true),
			want:      `Connection error: proxyconnect tcp: \x1b[31m` + strings.Repeat("z", 200-len(`proxyconnect tcp: \x1b[31m`)) + "…",
			wantCause: cause,
		},
		"success: timeout in seconds":              {err: newTimeoutError(1250*time.Millisecond, context.DeadlineExceeded), want: "Request timed out (timeout=1.25s).", wantCause: context.DeadlineExceeded},
		"success: whole seconds":                   {err: newTimeoutError(10*time.Second, nil), want: "Request timed out (timeout=10s)."},
		"success: the context's deadline":          {err: newTimeoutError(0, context.DeadlineExceeded), want: "Request timed out.", wantCause: context.DeadlineExceeded},
		"success: the proxy hop":                   {err: newProxyTimeoutError(10*time.Second, cause), want: "Request timed out on the proxy hop (timeout=10s).", wantCause: cause},
		"success: the proxy hop without a timeout": {err: newProxyTimeoutError(0, cause), want: "Request timed out on the proxy hop.", wantCause: cause},
		"success: a zero status is left out":       {err: &ResponseValidationError{FieldPath: "answers.n.noul"}, want: "Invalid response data at 'answers.n.noul'."},
		"success: a zero status after an endpoint": {err: &ResponseValidationError{Endpoint: "GET https://api.typesafe.ai/v1/models", FieldPath: "."}, want: "GET https://api.typesafe.ai/v1/models: Invalid response data at '.'."},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := gocmp.Diff(tt.want, tt.err.Error()); diff != "" {
				t.Errorf("Error() (-want +got):\n%s", diff)
			}
			if tt.wantCause != nil && !errors.Is(tt.err, tt.wantCause) {
				t.Errorf("errors.Is(%v, %v) = false", tt.err, tt.wantCause)
			}
		})
	}
	ce := newConnectionError("refused", nil, true)
	if !ce.Proxy() || ce.Unwrap() != nil {
		t.Errorf("Proxy() = %t, Unwrap() = %v", ce.Proxy(), ce.Unwrap())
	}
	if te, pte := newTimeoutError(time.Second, nil), newProxyTimeoutError(time.Second, nil); te.Proxy() || !pte.Proxy() {
		t.Errorf("Proxy() = %t for an attempt timeout, %t for a proxy-hop timeout; want false, true", te.Proxy(), pte.Proxy())
	}
}

// TestErrorsAsRoundTrip is the Go half of test_exception_reconstruction
// (E1): errors are values, so where Python rebuilds an exception from its
// args, copies and unpickles it, a Go error is matched with errors.As
// through any wrapping, copied by value with the same rendering, and each
// of the seven types is a typesafe.Error.
func TestErrorsAsRoundTrip(t *testing.T) {
	meta := &wire.ResponseMeta{Status: 200, Header: headers("X-Typesafe-Request-Id", "req"), Body: []byte(`{}`)}
	tests := map[string]struct {
		err  Error
		copy func(Error) Error
	}{
		"success: *APIError": {
			err:  apiError(429, `{"message":"slow down"}`, headers("Retry-After-Ms", "125"), "GET https://x/v1/models"),
			copy: func(e Error) Error { c := *e.(*APIError); return &c },
		},
		"success: *ResponseValidationError": {
			err:  newResponseValidationError(meta, "", &codec.DecodeError{Path: codec.FieldPath{Top: "answers", Name: "q", HasName: true, Member: "noul"}, Err: errors.New("missing")}),
			copy: func(e Error) Error { c := *e.(*ResponseValidationError); return &c },
		},
		"success: *ResponseTooLargeError": {
			err:  &ResponseTooLargeError{StatusCode: 200, Limit: 1},
			copy: func(e Error) Error { c := *e.(*ResponseTooLargeError); return &c },
		},
		"success: *ConnectionError": {
			err:  newConnectionError("Connection failure", nil, false),
			copy: func(e Error) Error { c := *e.(*ConnectionError); return &c },
		},
		"success: *TimeoutError": {
			err:  newTimeoutError(time.Second, context.DeadlineExceeded),
			copy: func(e Error) Error { c := *e.(*TimeoutError); return &c },
		},
		"success: *ConfigError": {
			err:  newConfigError("SDK failure"),
			copy: func(e Error) Error { c := *e.(*ConfigError); return &c },
		},
		"success: *InvalidRequestError": {
			err:  newInvalidRequestError("bad state", errSentinel),
			copy: func(e Error) Error { c := *e.(*InvalidRequestError); return &c },
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			wrapped := fmt.Errorf("call: %w", fmt.Errorf("attempt: %w", tt.err))
			var te Error
			if !errors.As(wrapped, &te) || te != tt.err {
				t.Fatalf("errors.As(typesafe.Error) = %v, want the error itself", te)
			}
			target := reflect.New(reflect.TypeOf(tt.err))
			if !errors.As(wrapped, target.Interface()) || target.Elem().Interface() != tt.err {
				t.Errorf("errors.As(%T) did not find the error", tt.err)
			}
			c := tt.copy(tt.err)
			if c == tt.err || c.Error() != tt.err.Error() || fmt.Sprint(c) != tt.err.Error() {
				t.Errorf("copy renders %q, want %q", c.Error(), tt.err.Error())
			}
		})
	}
	var rve *ResponseValidationError
	if !errors.As(tests["success: *ResponseValidationError"].err, &rve) || rve.Error() != "200 Invalid response data at 'answers.q.noul'. (request_id=req)" {
		t.Errorf("response validation error = %v", rve)
	}
}

// errMarshal is the error a caller's MarshalJSON returns in
// TestInvalidRequestErrorChain.
var errMarshal = errors.New("caller: cannot marshal")

type refusingMarshaler struct{}

func (refusingMarshaler) MarshalJSON() ([]byte, error) { return nil, errMarshal }

// TestInvalidRequestErrorChain pins ruling R53's decision: the chain of an
// *InvalidRequestError is not cut at the codec, so the error a caller's own
// MarshalJSON returned stays reachable with errors.Is, while the message
// carries the escaped, cut text.
func TestInvalidRequestErrorChain(t *testing.T) {
	qs, err := NewQuestions().Noul("q", Noul{Instructions: Text("?")}).Prepare()
	if err != nil {
		t.Fatal(err)
	}
	_, err = encodeBody(map[string]any{"v": refusingMarshaler{}}, "m", qs, nil)
	if _, ok := errors.AsType[*InvalidRequestError](err); !ok {
		t.Fatalf("err = %v (%T), want an *InvalidRequestError", err, err)
	}
	if !errors.Is(err, errMarshal) {
		t.Errorf("errors.Is(err, the caller's error) = false; the chain was cut: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot marshal") {
		t.Errorf("Error() = %q, want the cause's text", err.Error())
	}
}
