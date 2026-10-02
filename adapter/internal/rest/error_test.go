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

package rest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/adapter/llm"
)

// No test of this file opens a socket: each gives the Client an
// http.RoundTripper of its own, and runs inside a testing/synctest bubble,
// so a timeout passes on the bubble's clock and its length is asserted
// exactly.

// testTarget is the URL the tests of this file post to and testTargetText
// what an error text shows of it. The host is under the reserved .test
// domain, and no transport of these tests dials it.
const (
	testTarget     = "https://provider.example.test/v1/call"
	testTargetText = "rest: POST https://provider.example.test/v1/call: "
)

// roundTripFunc is an http.RoundTripper of one function.
type roundTripFunc func(req *http.Request) (*http.Response, error)

// RoundTrip calls f.
func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// respond returns a response to req with the given status, header and body.
func respond(req *http.Request, status int, header http.Header, body io.Reader) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	rc, ok := body.(io.ReadCloser)
	if !ok {
		rc = io.NopCloser(body)
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          rc,
		ContentLength: -1,
		Request:       req,
	}
}

// hostile wraps err in a text that prints the request's whole URL and every
// header value, as a transport's error may, so that a test of what Post
// returns also shows that none of it survives. The HTTP client adds its own
// *url.Error around it, which prints the URL with its query.
func hostile(req *http.Request, err error) error {
	return fmt.Errorf("transport of %s with headers %v: %w", req.URL.String(), req.Header, err)
}

// block is a transport that answers when the request's context ends, with
// the context's error.
func block(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, hostile(req, req.Context().Err())
}

// blockingBody is a response body whose Read returns when ctx ends, with
// the context's error.
type blockingBody struct{ ctx context.Context }

// Read blocks until the context ends.
func (b blockingBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

// Close does nothing.
func (blockingBody) Close() error { return nil }

// failingBody returns a body that gives prefix and then fails with err.
func failingBody(prefix string, err error) io.Reader {
	return io.MultiReader(strings.NewReader(prefix), errReader{err: err})
}

// errReader is a reader whose Read fails with err.
type errReader struct{ err error }

// Read returns the error.
func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// fail returns a transport that fails every request with err, wrapped by
// hostile.
func fail(err error) roundTripFunc {
	return func(req *http.Request) (*http.Response, error) { return nil, hostile(req, err) }
}

// timeoutCanceled is a net.Error that says it timed out and wraps
// context.Canceled.
type timeoutCanceled struct{}

func (timeoutCanceled) Error() string   { return "made-up timeout around a cancellation" }
func (timeoutCanceled) Timeout() bool   { return true }
func (timeoutCanceled) Temporary() bool { return false }
func (timeoutCanceled) Unwrap() error   { return context.Canceled }

// matches is an error without a Timeout method and without a cause that
// errors.Is matches with target, as an error type with an Is method of its
// own does.
type matches struct{ target error }

func (matches) Error() string          { return "made-up error that only errors.Is recognises" }
func (m matches) Is(target error) bool { return target == m.target }

// transportCase is one way a request ends without a response body read to
// its end, and what Post returns for it.
type transportCase struct {
	roundTrip roundTripFunc
	// timeout is Config.Timeout; clientTimeout the Timeout of the borrowed
	// http.Client; callerDeadline the timeout of the caller's context. Zero
	// leaves each unset.
	timeout        time.Duration
	clientTimeout  time.Duration
	callerDeadline time.Duration

	// wantTimeout says the error is an *llm.TimeoutError, else an
	// *llm.ConnectionError; wantCause is the cause of its Err, wantPhrase
	// the end of its text, and wantElapsed the time Post takes.
	wantTimeout bool
	wantCause   error
	wantPhrase  string
	wantElapsed time.Duration
}

// transportCases are the transport failures Post classifies.
func transportCases() map[string]transportCase {
	return map[string]transportCase{
		"timeout: the Client's timeout passes before a response": {
			roundTrip: block, timeout: 5 * time.Second,
			wantTimeout: true, wantCause: context.DeadlineExceeded, wantPhrase: phraseTimeout, wantElapsed: 5 * time.Second,
		},
		"timeout: a Client without a timeout waits DefaultTimeout": {
			roundTrip:   block,
			wantTimeout: true, wantCause: context.DeadlineExceeded, wantPhrase: phraseTimeout, wantElapsed: 600 * time.Second,
		},
		"timeout: a negative timeout is DefaultTimeout": {
			roundTrip: block, timeout: -time.Second,
			wantTimeout: true, wantCause: context.DeadlineExceeded, wantPhrase: phraseTimeout, wantElapsed: 600 * time.Second,
		},
		"timeout: the caller's deadline passes before the Client's timeout": {
			roundTrip: block, timeout: 5 * time.Second, callerDeadline: 2 * time.Second,
			wantTimeout: true, wantCause: context.DeadlineExceeded, wantPhrase: phraseTimeout, wantElapsed: 2 * time.Second,
		},
		"timeout: the borrowed client's own Timeout passes": {
			roundTrip: block, timeout: 5 * time.Second, clientTimeout: 3 * time.Second,
			wantTimeout: true, wantCause: context.DeadlineExceeded, wantPhrase: phraseTimeout, wantElapsed: 3 * time.Second,
		},
		"timeout: the Client's timeout passes, and the transport reports a refused connection": {
			roundTrip: func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, hostile(req, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)})
			},
			timeout:     5 * time.Second,
			wantTimeout: true, wantCause: context.DeadlineExceeded, wantPhrase: phraseTimeout, wantElapsed: 5 * time.Second,
		},
		"timeout: the caller's deadline passes while the body is read, and the read reports a reset": {
			roundTrip: func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return respond(req, http.StatusOK, nil, failingBody("{", hostile(req, syscall.ECONNRESET))), nil
			},
			timeout: 5 * time.Second, callerDeadline: 2 * time.Second,
			wantTimeout: true, wantCause: context.DeadlineExceeded, wantPhrase: phraseBodyTimeout, wantElapsed: 2 * time.Second,
		},
		"timeout: a net.Error whose Timeout is true": {
			roundTrip:   fail(&net.DNSError{Err: "i/o timeout", Name: "provider.example.test", IsTimeout: true}),
			wantTimeout: true, wantCause: context.DeadlineExceeded, wantPhrase: phraseTimeout,
		},
		"timeout: os.ErrDeadlineExceeded": {
			roundTrip:   fail(&net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}),
			wantTimeout: true, wantCause: os.ErrDeadlineExceeded, wantPhrase: phraseTimeout,
		},
		"timeout: an error that only errors.Is matches with context.DeadlineExceeded": {
			roundTrip:   fail(matches{target: context.DeadlineExceeded}),
			wantTimeout: true, wantCause: context.DeadlineExceeded, wantPhrase: phraseTimeout,
		},
		"timeout: an error that only errors.Is matches with os.ErrDeadlineExceeded": {
			roundTrip:   fail(matches{target: os.ErrDeadlineExceeded}),
			wantTimeout: true, wantCause: os.ErrDeadlineExceeded, wantPhrase: phraseTimeout,
		},
		"timeout: ETIMEDOUT": {
			roundTrip:   fail(&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ETIMEDOUT)}),
			wantTimeout: true, wantCause: syscall.ETIMEDOUT, wantPhrase: phraseTimeout,
		},
		"timeout: one that wraps a cancellation keeps a deadline": {
			roundTrip:   fail(timeoutCanceled{}),
			wantTimeout: true, wantCause: context.DeadlineExceeded, wantPhrase: phraseTimeout,
		},
		"timeout: one joined with another error": {
			roundTrip:   fail(errors.Join(io.EOF, nil, &net.DNSError{Err: "i/o timeout", Name: "provider.example.test", IsTimeout: true})),
			wantTimeout: true, wantCause: io.EOF, wantPhrase: phraseTimeout,
		},
		"timeout: the Client's timeout passes while the body is read": {
			roundTrip: func(req *http.Request) (*http.Response, error) {
				return respond(req, http.StatusOK, nil, blockingBody{ctx: req.Context()}), nil
			},
			timeout:     5 * time.Second,
			wantTimeout: true, wantCause: context.DeadlineExceeded, wantPhrase: phraseBodyTimeout, wantElapsed: 5 * time.Second,
		},
		"timeout: it passes while the body of a 503 is read, and the status is not reported": {
			roundTrip: func(req *http.Request) (*http.Response, error) {
				return respond(req, http.StatusServiceUnavailable, nil, blockingBody{ctx: req.Context()}), nil
			},
			timeout:     5 * time.Second,
			wantTimeout: true, wantCause: context.DeadlineExceeded, wantPhrase: phraseBodyTimeout, wantElapsed: 5 * time.Second,
		},
		"timeout: the borrowed client's own Timeout passes while the body is read": {
			roundTrip: func(req *http.Request) (*http.Response, error) {
				return respond(req, http.StatusOK, nil, blockingBody{ctx: req.Context()}), nil
			},
			timeout: 5 * time.Second, clientTimeout: 3 * time.Second,
			wantTimeout: true, wantCause: context.DeadlineExceeded, wantPhrase: phraseBodyTimeout, wantElapsed: 3 * time.Second,
		},
		"connection: refused": {
			roundTrip: fail(&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}),
			wantCause: syscall.ECONNREFUSED, wantPhrase: phraseConnection,
		},
		"connection: reset": {
			roundTrip: fail(&net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}),
			wantCause: syscall.ECONNRESET, wantPhrase: phraseConnection,
		},
		"connection: EOF before a response": {
			roundTrip: fail(io.EOF),
			wantCause: io.EOF, wantPhrase: phraseConnection,
		},
		"connection: unexpected EOF": {
			roundTrip: fail(io.ErrUnexpectedEOF),
			wantCause: io.ErrUnexpectedEOF, wantPhrase: phraseConnection,
		},
		"connection: closed network connection": {
			roundTrip: fail(&net.OpError{Op: "write", Net: "tcp", Err: net.ErrClosed}),
			wantCause: net.ErrClosed, wantPhrase: phraseConnection,
		},
		"connection: name not found, which has no cause to keep": {
			roundTrip:  fail(&net.DNSError{Err: "no such host", Name: "provider.example.test", IsNotFound: true}),
			wantPhrase: phraseConnection,
		},
		"connection: TLS record error, which has no cause to keep": {
			roundTrip:  fail(tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}),
			wantPhrase: phraseConnection,
		},
		"connection: certificate of an unknown authority, which has no cause to keep": {
			roundTrip:  fail(&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}),
			wantPhrase: phraseConnection,
		},
		"connection: the transport's own cancellation while the caller's context is live": {
			roundTrip: fail(context.Canceled),
			wantCause: context.Canceled, wantPhrase: phraseConnection,
		},
		"connection: reset while the body is read": {
			roundTrip: func(req *http.Request) (*http.Response, error) {
				err := hostile(req, &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)})
				return respond(req, http.StatusOK, nil, failingBody(`{"output":`, err)), nil
			},
			wantCause: syscall.ECONNRESET, wantPhrase: phraseBody,
		},
		"connection: the body of a 503 ends early, and the status is not reported": {
			roundTrip: func(req *http.Request) (*http.Response, error) {
				return respond(req, http.StatusServiceUnavailable, nil, failingBody(`{"error":`, hostile(req, io.ErrUnexpectedEOF))), nil
			},
			wantCause: io.ErrUnexpectedEOF, wantPhrase: phraseBody,
		},
	}
}

// run performs the case's request against target with header and returns
// the time Post took and its error on the bubble's clock. It must be
// called inside a synctest bubble.
func (tc transportCase) run(t *testing.T, target *url.URL, header http.Header, blankIsNone bool) (time.Duration, error) {
	t.Helper()
	c := New(Config{
		HTTPClient:           &http.Client{Transport: tc.roundTrip, Timeout: tc.clientTimeout},
		Timeout:              tc.timeout,
		BlankErrorBodyIsNone: blankIsNone,
	})
	ctx := t.Context()
	if tc.callerDeadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, tc.callerDeadline)
		defer cancel()
	}
	start := time.Now()
	body, err := c.Post(ctx, target, header, []byte(`{"model":"made-up"}`))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("Post() = %q, nil; want an error", body)
	}
	if body != nil {
		t.Errorf("Post() returned the body %q beside the error %v", body, err)
	}
	return elapsed, err
}

// mustURL parses raw.
func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}

// TestTransportErrorsClassify ports test_timeout_and_connection_errors_map
// of system-one-adapter-python v0.2.1 (tests/utils/test_error_handling.py:
// 70-77), with its parameter ids: for each provider, a request that times
// out is an *llm.TimeoutError and a request that fails without a response
// an *llm.ConnectionError. Upstream gives each provider's translator the
// timeout and connection error classes of that provider's Python SDK; here
// the three providers share one call and differ only in their Config, so
// each case builds the Client as that provider does.
func TestTransportErrorsClassify(t *testing.T) {
	tests := map[string]struct {
		target      string
		blankIsNone bool
	}{
		"openai":    {target: "https://openai.example.test/v1/responses"},
		"anthropic": {target: "https://anthropic.example.test/v1/messages"},
		"gemini":    {target: "https://gemini.example.test/v1beta/interactions", blankIsNone: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				target := mustURL(t, tt.target)

				timedOut := transportCase{roundTrip: block, timeout: 30 * time.Second}
				elapsed, err := timedOut.run(t, target, nil, tt.blankIsNone)
				te, ok := errors.AsType[*llm.TimeoutError](err)
				if !ok {
					t.Fatalf("a request that timed out: Post() error = %#v, want an *llm.TimeoutError", err)
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("errors.Is(%v, context.DeadlineExceeded) = false, want true", err)
				}
				if !te.Timeout() {
					t.Error("Timeout() = false, want true")
				}
				if _, ok := errors.AsType[*llm.ConnectionError](err); ok {
					t.Errorf("a request that timed out is also an *llm.ConnectionError: %#v", err)
				}
				if elapsed != 30*time.Second {
					t.Errorf("Post() returned after %v, want the Client's timeout of 30s", elapsed)
				}
				if diff := cmp.Diff("Request timed out (timeout=Timeout(timeout=None)).", err.Error()); diff != "" {
					t.Errorf("timeout text mismatch (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff("rest: POST "+tt.target+": "+phraseTimeout, te.Err.Error()); diff != "" {
					t.Errorf("timeout Err text mismatch (-want +got):\n%s", diff)
				}

				refused := transportCase{roundTrip: fail(&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)})}
				elapsed, err = refused.run(t, target, nil, tt.blankIsNone)
				ce, ok := errors.AsType[*llm.ConnectionError](err)
				if !ok {
					t.Fatalf("a request without a response: Post() error = %#v, want an *llm.ConnectionError", err)
				}
				if _, ok := errors.AsType[*llm.TimeoutError](err); ok {
					t.Errorf("a refused connection is also an *llm.TimeoutError: %#v", err)
				}
				if errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("errors.Is(%v, context.DeadlineExceeded) = true, want false", err)
				}
				if !errors.Is(err, syscall.ECONNREFUSED) {
					t.Errorf("errors.Is(%v, syscall.ECONNREFUSED) = false, want true", err)
				}
				if elapsed != 0 {
					t.Errorf("Post() returned after %v, want at once", elapsed)
				}
				if diff := cmp.Diff("Connection error.", err.Error()); diff != "" {
					t.Errorf("connection text mismatch (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff("rest: POST "+tt.target+": "+phraseConnection, ce.Err.Error()); diff != "" {
					t.Errorf("connection Err text mismatch (-want +got):\n%s", diff)
				}
			})
		})
	}
}

// TestTransportErrorKinds checks, for every way a request ends without a
// response body read to its end, the error type Post returns, the fixed
// text and the one cause its Err keeps, and how long Post took.
func TestTransportErrorKinds(t *testing.T) {
	for name, tt := range transportCases() {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				elapsed, err := tt.run(t, mustURL(t, testTarget), nil, false)

				var inner error
				if tt.wantTimeout {
					te, ok := errors.AsType[*llm.TimeoutError](err)
					if !ok {
						t.Fatalf("Post() error = %#v, want an *llm.TimeoutError", err)
					}
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Errorf("errors.Is(%v, context.DeadlineExceeded) = false, want true", err)
					}
					inner = te.Err
				} else {
					ce, ok := errors.AsType[*llm.ConnectionError](err)
					if !ok {
						t.Fatalf("Post() error = %#v, want an *llm.ConnectionError", err)
					}
					if _, ok := errors.AsType[*llm.TimeoutError](err); ok {
						t.Errorf("Post() error %#v is also an *llm.TimeoutError", err)
					}
					inner = ce.Err
				}
				if _, ok := errors.AsType[*llm.StatusError](err); ok {
					t.Errorf("Post() error %#v holds an *llm.StatusError; a body that was not read to its end reports no status", err)
				}

				te, ok := inner.(*transportError) //nolint:errorlint // Err is the package's own error itself, not a wrapper of it.
				if !ok {
					t.Fatalf("Err = %#v, want a *transportError", inner)
				}
				if diff := cmp.Diff(testTargetText+tt.wantPhrase, te.Error()); diff != "" {
					t.Errorf("Err text mismatch (-want +got):\n%s", diff)
				}
				if te.cause != tt.wantCause { //nolint:errorlint // the cause is the error value itself.
					t.Errorf("Err cause = %#v, want %#v itself", te.cause, tt.wantCause)
				}
				if tt.wantCause != nil && !errors.Is(err, tt.wantCause) {
					t.Errorf("errors.Is(%v, %v) = false, want true", err, tt.wantCause)
				}
				if elapsed != tt.wantElapsed {
					t.Errorf("Post() returned after %v, want %v", elapsed, tt.wantElapsed)
				}
			})
		})
	}
}

// TestCancelledContextReturnsContextError checks that a call whose context
// was cancelled returns ctx.Err() itself, whenever the cancellation came
// and whatever the transport reported, and no error type of package llm.
func TestCancelledContextReturnsContextError(t *testing.T) {
	tests := map[string]struct {
		roundTrip roundTripFunc
		// cancelAfter is when the context is cancelled; zero cancels it
		// before Post is called.
		cancelAfter time.Duration
		cause       error
	}{
		"success: cancelled before the request": {
			roundTrip: block,
		},
		"success: cancelled while the request waits": {
			roundTrip: block, cancelAfter: time.Second,
		},
		"success: cancelled while the body is read": {
			roundTrip: func(req *http.Request) (*http.Response, error) {
				return respond(req, http.StatusOK, nil, blockingBody{ctx: req.Context()}), nil
			},
			cancelAfter: time.Second,
		},
		"success: cancelled while the body of a 429 is read": {
			roundTrip: func(req *http.Request) (*http.Response, error) {
				return respond(req, http.StatusTooManyRequests, nil, blockingBody{ctx: req.Context()}), nil
			},
			cancelAfter: time.Second,
		},
		"success: cancelled with a cause": {
			roundTrip: block, cancelAfter: time.Second, cause: errors.New("made-up cause of the cancellation"),
		},
		"success: cancelled, and the transport reports a timeout": {
			roundTrip: func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, hostile(req, &net.DNSError{Err: "i/o timeout", IsTimeout: true})
			},
			cancelAfter: time.Second,
		},
		"success: cancelled, and the transport reports a refused connection": {
			roundTrip: func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, hostile(req, syscall.ECONNREFUSED)
			},
			cancelAfter: time.Second,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancelCause(t.Context())
				defer cancel(nil)
				if tt.cancelAfter == 0 {
					cancel(tt.cause)
				} else {
					time.AfterFunc(tt.cancelAfter, func() { cancel(tt.cause) })
				}

				c := New(Config{HTTPClient: &http.Client{Transport: tt.roundTrip}, Timeout: time.Minute})
				start := time.Now()
				body, err := c.Post(ctx, mustURL(t, testTarget), nil, []byte(`{}`))
				if elapsed := time.Since(start); elapsed != tt.cancelAfter {
					t.Errorf("Post() returned after %v, want %v", elapsed, tt.cancelAfter)
				}
				if body != nil {
					t.Errorf("Post() body = %q, want none", body)
				}
				if err != ctx.Err() || err != context.Canceled { //nolint:errorlint // the error is ctx.Err() itself, not a wrapper of it.
					t.Fatalf("Post() error = %#v, want ctx.Err() itself, context.Canceled", err)
				}
			})
		})
	}
}

// allowedLinks are the error values that may stand in the chain of an error
// of this package besides the types of package llm, a *transportError and a
// syscall.Errno: the causes a transportError keeps and the package's own
// fixed errors.
var allowedLinks = []error{
	context.DeadlineExceeded, context.Canceled, os.ErrDeadlineExceeded, io.ErrUnexpectedEOF, io.EOF, net.ErrClosed,
	ErrBodyTooLarge, ErrURL,
}

// everyError returns one error of each kind this package returns, by name,
// made with a request to target that carries header: every case of
// transportCases, a cancelled call, a status error with a body and with a
// blank one under both settings, a body past the limit under both kinds of
// status, a request that cannot be built, and a base URL that does not
// parse. Every transport's error prints the whole URL and the header
// values. It must be called inside a synctest bubble.
func everyError(t *testing.T, target *url.URL, header http.Header) map[string]error {
	t.Helper()
	errs := make(map[string]error)
	for name, tc := range transportCases() {
		_, errs[name] = tc.run(t, target, header, false)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, errs["cancelled"] = New(Config{HTTPClient: &http.Client{Transport: roundTripFunc(block)}}).Post(ctx, target, header, nil)

	// The response headers repeat the request's, so a header the status
	// error kept by mistake would show them.
	status := func(code int, body string) transportCase {
		return transportCase{roundTrip: func(req *http.Request) (*http.Response, error) {
			h := req.Header.Clone()
			h.Set("Retry-After", "7")
			h.Set("Set-Cookie", req.URL.String())
			return respond(req, code, h, strings.NewReader(body)), nil
		}}
	}
	_, errs["status: 503 with a body"] = status(http.StatusServiceUnavailable, `{"error":{"message":"boom"}}`).run(t, target, header, false)
	_, errs["status: 503 with a blank body kept as empty"] = status(http.StatusServiceUnavailable, " \n").run(t, target, header, false)
	_, errs["status: 503 with a blank body kept as none"] = status(http.StatusServiceUnavailable, " \n").run(t, target, header, true)
	_, errs["status: 302"] = status(http.StatusFound, "").run(t, target, header, false)

	for name, code := range map[string]int{"body past the limit: 200": http.StatusOK, "body past the limit: 500": http.StatusInternalServerError} {
		c := New(Config{HTTPClient: &http.Client{Transport: status(code, "0123456789").roundTrip}})
		c.maxBody = 4
		_, errs[name] = c.Post(t.Context(), target, header, nil)
	}

	unbuildable := *target
	unbuildable.Host = "provider.example.test:not a port"
	_, errs["request that cannot be built"] = New(Config{HTTPClient: &http.Client{Transport: roundTripFunc(block)}}).Post(t.Context(), &unbuildable, header, nil)

	_, errs["base URL that does not parse"] = ParseURL(strings.Replace(target.String(), target.Host, target.Host+":port", 1))

	for name, err := range errs {
		if err == nil {
			t.Fatalf("%s: no error; every entry must fail", name)
		}
	}
	return errs
}

// chain returns err and every error errors.Unwrap reaches from it, in
// order.
func chain(err error) []error {
	var links []error
	for e := err; e != nil; e = errors.Unwrap(e) {
		links = append(links, e)
	}
	return links
}

// TestErrorsKeepOnlySentinels walks, with errors.Unwrap, the chain of every
// error this package returns and checks that each link is an error type of
// package llm, the package's own fixed-text error, a syscall.Errno, or one
// of the well-known error values of allowedLinks; in particular no link is
// the *url.Error or *net.OpError of the HTTP client, which print the URL
// with its query. It also checks that the error types of package llm stand
// only at the head of a chain and that every chain is short.
func TestErrorsKeepOnlySentinels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		errs := everyError(t, mustURL(t, testTarget), nil)

		seen := make(map[string]int)
		for _, name := range slices.Sorted(maps.Keys(errs)) {
			links := chain(errs[name])
			if len(links) > 3 {
				t.Errorf("%s: a chain of %d links, want at most 3: %#v", name, len(links), links)
			}
			for i, link := range links {
				var kind string
				switch v := link.(type) { //nolint:errorlint // each link is inspected as it is; errors.As would skip links.
				case *llm.TimeoutError, *llm.ConnectionError, *llm.StatusError:
					kind = fmt.Sprintf("%T", v)
					if i != 0 {
						t.Errorf("%s: link %d is %T; an error type of package llm stands only at the head", name, i, v)
					}
				case *transportError:
					kind = "*rest.transportError"
				case syscall.Errno:
					kind = "syscall.Errno"
				case *url.Error, *net.OpError, *net.DNSError, *os.SyscallError:
					t.Errorf("%s: link %d is %T, an error of the HTTP client or the network that the chain must not keep", name, i, v)
					continue
				default:
					if !slices.Contains(allowedLinks, link) {
						t.Errorf("%s: link %d is %#v, which is none of the allowed error values", name, i, link)
						continue
					}
					kind = link.Error()
				}
				seen[kind]++
			}
			if _, ok := errors.AsType[*url.Error](errs[name]); ok {
				t.Errorf("%s: errors.As reaches a *url.Error", name)
			}
			if _, ok := errors.AsType[*net.OpError](errs[name]); ok {
				t.Errorf("%s: errors.As reaches a *net.OpError", name)
			}
		}

		// Every kind of link the package can produce was walked, so the
		// check above did not pass for want of cases.
		for _, kind := range []string{
			"*llm.TimeoutError", "*llm.ConnectionError", "*llm.StatusError", "*rest.transportError", "syscall.Errno",
			context.DeadlineExceeded.Error(), context.Canceled.Error(), os.ErrDeadlineExceeded.Error(),
			io.ErrUnexpectedEOF.Error(), io.EOF.Error(), net.ErrClosed.Error(), ErrBodyTooLarge.Error(), ErrURL.Error(),
		} {
			if seen[kind] == 0 {
				t.Errorf("no chain holds a link of kind %q; the cases do not cover it", kind)
			}
		}
	})
}

// canary is a made-up word that the redaction test puts wherever a
// credential can travel; it is not shaped like a key.
const canary = "canary-word-of-the-redaction-test"

// TestErrorsHoldNoURLSecretAndNoHeaderValue plants a canary in the
// userinfo, the query and the fragment of the URL and in three request
// headers, lets every transport fail with an error whose own text prints
// the URL and the headers, lets a server repeat the request's headers in
// its response, and checks that the canary appears in no printed form of
// any error this package returns, nor of any link of its chain: Error and
// the verbs %v, %+v, %s, %q and %#v.
func TestErrorsHoldNoURLSecretAndNoHeaderValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		target := mustURL(t, "https://user-"+canary+":pass-"+canary+"@provider.example.test/v1/call?key="+canary+"&alt=json#"+canary)
		header := http.Header{
			"Authorization":  {"Bearer " + canary},
			"X-Api-Key":      {canary},
			"X-Goog-Api-Key": {canary},
		}
		// The transports of the cases do print the canary: the test would
		// pass for nothing if they did not.
		if probe := hostile(&http.Request{URL: target, Header: header}, io.EOF).Error(); strings.Count(probe, canary) < 6 {
			t.Fatalf("the transports' own error text holds the canary %d times, want at least 6: %s", strings.Count(probe, canary), probe)
		}

		errs := everyError(t, target, header)
		for _, name := range slices.Sorted(maps.Keys(errs)) {
			for i, link := range chain(errs[name]) {
				forms := map[string]string{
					"Error()": link.Error(),
					"%v":      fmt.Sprintf("%v", link),
					"%+v":     fmt.Sprintf("%+v", link),
					"%s":      fmt.Sprintf("%s", link),
					"%q":      fmt.Sprintf("%q", link),
					"%#v":     fmt.Sprintf("%#v", link),
				}
				for form, text := range forms {
					if strings.Contains(text, canary) {
						t.Errorf("%s: link %d (%T) prints the canary with %s: %s", name, i, link, form, text)
					}
					if strings.Contains(text, "key=") || strings.Contains(text, "alt=json") || strings.Contains(text, "user-") {
						t.Errorf("%s: link %d (%T) prints a part of the URL's userinfo or query with %s: %s", name, i, link, form, text)
					}
				}
			}
			// A status error's header and body are fields, which the verbs
			// above do not all print.
			if se, ok := errors.AsType[*llm.StatusError](errs[name]); ok {
				if text := fmt.Sprintf("%v %s", se.Header, se.Body); strings.Contains(text, canary) {
					t.Errorf("%s: the status error's header or body holds the canary: %s", name, text)
				}
			}
		}

		// The text of a transport error shows the URL's scheme, host and
		// path, and nothing else of it.
		te, ok := errors.AsType[*llm.ConnectionError](errs["connection: refused"])
		if !ok {
			t.Fatalf("connection: refused is %#v, want an *llm.ConnectionError", errs["connection: refused"])
		}
		if diff := cmp.Diff(testTargetText+phraseConnection, te.Err.Error()); diff != "" {
			t.Errorf("Err text mismatch (-want +got):\n%s", diff)
		}
	})
}
