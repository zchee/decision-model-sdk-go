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
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// recordsText renders every record logs kept, one per line.
func recordsText(logs *testsupport.LogRecorder) string {
	var sb strings.Builder
	for _, r := range logs.Records() {
		sb.WriteString(r.String())
		sb.WriteByte('\n')
	}
	return sb.String()
}

// attemptRecords renders, in order, the records of a call's attempts: those
// that name the endpoint, as "LEVEL message", with the attempt or the retry
// number for the records that carry one.
func attemptRecords(logs *testsupport.LogRecorder) []string {
	var out []string
	for _, r := range logs.Records() {
		if _, ok := r.Attr("endpoint"); !ok {
			continue
		}
		var line strings.Builder
		line.WriteString(r.Level.String() + " " + r.Message)
		for _, key := range []string{"attempt", "retry"} {
			if v, ok := r.Attr(key); ok {
				line.WriteString(" " + key + "=" + v.String())
			}
		}
		out = append(out, line.String())
	}
	return out
}

// TestSecretHeadersRedacted ports test_secret_headers_redacted (L1, AC-F5,
// tests/test_logging.py:14-67) through the client: the nine header
// spellings times the three statuses, under DefaultRetry with a backoff of
// 1 ms and no jitter, as upstream's RetryPolicy(backoff_initial=0.001,
// backoff_max=0.001); the policy is passed explicitly over the test
// helper's NoRetry (ruling R88b). A 429 is retried twice: three attempts,
// each logged as upstream logs one (DEBUG request, INFO response, DEBUG
// response headers, LevelTrace body), the retries after an INFO "request
// retry" record with retry=1 and retry=2; a 200 and a 400 make one attempt.
// A credential in the request header the caller set, in the same response
// header, and the API key appear in no record of any attempt down to
// LevelTrace, nor in any rendering of the error or of an error it wraps
// (%v, %+v, %#v, %q and %s; the error stores the response header redacted,
// ruling R87), while the other headers' values do and "***" stands for the
// redacted ones. It replaces Phase 2's TestClientLogsNoCredential, which ran
// the same grid with one attempt per call.
func TestSecretHeadersRedacted(t *testing.T) {
	type test struct {
		header string
		status int
	}
	tests := map[string]test{}
	for _, header := range secretSpellings {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusTooManyRequests} {
			tests["success: "+header+"/"+strconv.Itoa(status)] = test{header: header, status: status}
		}
	}
	policy := DefaultRetry().Backoff(time.Millisecond, time.Millisecond, 0)
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"models":[]}`)
			if tt.status != http.StatusOK {
				body = []byte(`{"message":"failure"}`)
			}
			rec := replying(tt.status, body, tt.header, "response-credential", "X-Visible", "response-visible")
			logs := testsupport.NewLogRecorder(LevelTrace)
			clearEnv(t)
			c := newEnvClient(t, rec, WithAPIKey("auth-credential"), WithHeader(tt.header, "request-credential"), WithHeader("x-visible", "request-visible"),
				WithLogger(logs.Logger()), WithRetry(policy))
			_, err := c.Models().List(t.Context())
			if (err == nil) != (tt.status == http.StatusOK) {
				t.Fatalf("List error = %v for status %d", err, tt.status)
			}
			attempts := 1
			if tt.status == http.StatusTooManyRequests {
				attempts = 3
			}
			if rec.Count() != attempts || c.Stats().Attempts != uint64(attempts) {
				t.Errorf("attempts: the transport saw %d, Stats().Attempts = %d, want %d", rec.Count(), c.Stats().Attempts, attempts)
			}
			var want []string
			for i := range attempts {
				n := strconv.Itoa(i)
				if i > 0 {
					want = append(want, "INFO request retry retry="+n)
				}
				want = append(want, "DEBUG request attempt="+n, "INFO response attempt="+n, "DEBUG response headers", "DEBUG-4 response body")
			}
			if diff := gocmp.Diff(want, attemptRecords(logs)); diff != "" {
				t.Errorf("the records of the attempts (-want +got):\n%s", diff)
			}
			text := recordsText(logs)
			for _, visible := range []string{"request-visible", "response-visible", redacted} {
				if !strings.Contains(text, visible) {
					t.Errorf("the records lack %q:\n%s", visible, text)
				}
			}
			for _, r := range logs.Records() {
				for _, secret := range []string{"auth-credential", "request-credential", "response-credential"} {
					if line := r.String(); strings.Contains(line, secret) {
						t.Errorf("a record holds %q: %s", secret, line)
					}
				}
			}
			if err == nil {
				return
			}
			for _, secret := range []string{"auth-credential", "request-credential", "response-credential"} {
				assertNotPrinted(t, err, secret)
				if out := fmt.Sprintf("%s", err); strings.Contains(out, secret) {
					t.Errorf("%%s of the error holds %q: %s", secret, out)
				}
			}
		})
	}
}

// TestTransportErrorsNeverExposeCredentials ports
// test_transport_errors_do_not_expose_credentials (L2, AC-F5,
// tests/test_logging.py:70-132): a RoundTripper fails every attempt in a Go
// form of each of upstream's five httpx errors (LocalProtocolError: a bare
// error; ConnectError: a refused dial; ReadError: a reset read;
// RemoteProtocolError: a body cut short; ReadTimeout: a network timeout),
// under DefaultRetry with no backoff, which retries each, so it is called
// three times. Its error's text repeats the request's Authorization value
// Go-quoted (Python's bytes repr), and a cause names the API key and the
// X-Client-Secret value the call sent; the cause is printed in the text and
// wrapped (%w, Python's __cause__), only wrapped (Python's __context__,
// which str() leaves out too), or only printed by %+v; the credentials are
// "ts_live_private" and "ts_live_quo'te\"slash\\tail". The SDK error's
// Error(), %v, %+v, %#v, %q and %s, every errors.Unwrap link, and every
// record of every attempt down to LevelTrace hold no form of either
// credential (raw, %q, JSON) nor the provider's value; "Illegal header
// value" and "Rejected authorization: ***; provider: ***" survive in the
// %+v of the cause (ruling R95).
//
// Upstream's type checks have a Go analogue, not a copy (rulings R81 (3),
// R82 (a)): the Python SDK rebuilds each exception with its own type, a
// traceback-free, request-free copy of its chain; the Go SDK maps the
// failure to its own class (*ConnectionError, or *TimeoutError for the
// timeout) and replaces a cause whose chain printed a credential by a
// *scrubbedError, a new value, not the transport's error, with the
// redacted text and the redacted rendering of the chain, through which
// errors.As reaches neither the transport's error types nor the request,
// and errors.Is only the standard sentinels and errno the original matched,
// so the class's diagnostic (ECONNREFUSED, ECONNRESET, io.ErrUnexpectedEOF)
// survives. The request the transport saw still holds the credential, as
// upstream's original_errors[-1].request does.
func TestTransportErrorsNeverExposeCredentials(t *testing.T) {
	type class struct {
		wrap     func(failure error) error
		timeout  bool  // the SDK error is a *TimeoutError
		sentinel error // errors.Is still holds through the stand-in
	}
	classes := map[string]class{
		"LocalProtocolError": {wrap: func(f error) error { return f }},
		"ConnectError": {
			wrap: func(f error) error {
				return fmt.Errorf("%w: %w", &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}, f)
			},
			sentinel: syscall.ECONNREFUSED,
		},
		"ReadError": {
			wrap: func(f error) error {
				return fmt.Errorf("%w: %w", &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}, f)
			},
			sentinel: syscall.ECONNRESET,
		},
		"RemoteProtocolError": {wrap: func(f error) error { return fmt.Errorf("%w: %w", io.ErrUnexpectedEOF, f) }, sentinel: io.ErrUnexpectedEOF},
		"ReadTimeout":         {wrap: func(f error) error { return fmt.Errorf("%w: %w", echoTimeout{msg: "read timed out"}, f) }, timeout: true},
	}
	chains := map[string]func(top string, cause error) error{
		"cause":   func(top string, cause error) error { return fmt.Errorf("%s: %w", top, cause) },
		"context": func(top string, cause error) error { return unwrapOnly{msg: top, cause: cause} },
		"%+v":     func(top string, cause error) error { return plusOnly{msg: top, cause: cause} },
	}
	type test struct {
		class      class
		chain      func(top string, cause error) error
		credential string
	}
	tests := map[string]test{}
	for className, cl := range classes {
		for chainName, chain := range chains {
			for _, credential := range []string{"ts_live_private", quirkyKey} {
				tests["error: "+className+"/"+chainName+"/"+credential] = test{class: cl, chain: chain, credential: credential}
			}
		}
	}
	policy := DefaultRetry().Backoff(0, 0, 0)
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var (
				mu       sync.Mutex
				calls    int
				failures []error
				lastAuth string
			)
			rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				// HTTP transports can echo header values in both their messages and
				// their error chains (tests/test_logging.py:96).
				cause := errors.New("Rejected authorization: " + tt.credential + "; provider: " + req.Header.Get("X-Client-Secret"))
				failure := tt.class.wrap(tt.chain(fmt.Sprintf("Illegal header value %q", req.Header.Get("Authorization")), cause))
				mu.Lock()
				defer mu.Unlock()
				calls++
				failures = append(failures, failure)
				lastAuth = req.Header.Get("Authorization")
				return nil, failure
			})
			logs := testsupport.NewLogRecorder(LevelTrace)
			clearEnv(t)
			c := newEnvClient(t, rt, WithAPIKey(tt.credential), WithHeader("X-Client-Secret", "provider-credential"), WithLogger(logs.Logger()), WithRetry(policy))
			_, err := c.Models().List(t.Context())
			mu.Lock()
			defer mu.Unlock()
			if calls != 3 || c.Stats().Attempts != 3 {
				t.Errorf("calls = %d, Stats().Attempts = %d, want 3", calls, c.Stats().Attempts)
			}
			if lastAuth != "Bearer "+tt.credential {
				t.Errorf("the transport saw Authorization %q, want the credential it was sent", lastAuth)
			}
			if _, ok := errors.AsType[*TimeoutError](err); ok != tt.class.timeout {
				t.Errorf("error = %T %v, want a *TimeoutError: %t", err, err, tt.class.timeout)
			}
			if _, ok := errors.AsType[*ConnectionError](err); ok == tt.class.timeout {
				t.Errorf("error = %T %v, want a *ConnectionError: %t", err, err, !tt.class.timeout)
			}
			standIn := errors.Unwrap(err)
			if _, ok := standIn.(*scrubbedError); !ok || standIn == failures[len(failures)-1] { //nolint:errorlint // the direct cause is the stand-in, a new value
				t.Fatalf("the cause = %T %v, want a new *scrubbedError standing in for the transport's error", standIn, standIn)
			}
			detail := fmt.Sprintf("%+v", standIn)
			for _, survivor := range []string{"Illegal header value", "Rejected authorization: ***; provider: ***"} {
				if !strings.Contains(detail, survivor) {
					t.Errorf("%%+v of the cause lacks %q: %s", survivor, detail)
				}
			}
			if !tt.class.timeout && !strings.Contains(err.Error(), "Illegal header value") {
				t.Errorf("Error() = %q, want the transport's text, scrubbed", err.Error())
			}
			if tt.class.sentinel != nil && !errors.Is(err, tt.class.sentinel) {
				t.Errorf("errors.Is(err, %v) = false, want the class's diagnostic kept", tt.class.sentinel)
			}
			if _, ok := errors.AsType[*net.OpError](err); ok {
				t.Error("errors.As reaches the transport's *net.OpError through the stand-in")
			}
			if _, ok := errors.AsType[unwrapOnly](err); ok {
				t.Error("errors.As reaches the transport's error type through the stand-in")
			}
			secrets := []string{tt.credential, quotedForm(strconv.Quote(tt.credential)), jsonForm(tt.credential), "provider-credential"}
			records := recordsText(logs)
			for _, secret := range secrets {
				assertNotPrinted(t, err, secret)
				if out := fmt.Sprintf("%s", err); strings.Contains(out, secret) {
					t.Errorf("%%s of the error holds %q: %s", secret, out)
				}
				if strings.Contains(records, secret) {
					t.Errorf("the records hold %q:\n%s", secret, records)
				}
			}
			var want []string
			for i := range 3 {
				n := strconv.Itoa(i)
				if i > 0 {
					want = append(want, "INFO request retry retry="+n)
				}
				want = append(want, "DEBUG request attempt="+n, "INFO request failed attempt="+n)
			}
			if diff := gocmp.Diff(want, attemptRecords(logs)); diff != "" {
				t.Errorf("the records of the attempts (-want +got):\n%s", diff)
			}
		})
	}
}

// TestClientLogRecords pins the records of one attempt at each level: INFO
// "response" with the method, the endpoint (its path alone under
// WithLogEndpointHost(false)), the status, the time taken, the request id
// escaped and cut, and the attempt; DEBUG "request" and "response headers"
// with the redacted headers and the body lengths; LevelTrace the bodies. A
// transport failure is one INFO "request failed" record with the SDK error's
// text. Nothing is logged below the logger's level.
func TestClientLogRecords(t *testing.T) {
	result := testsupport.Fixture(t, "result.json")
	tests := map[string]struct {
		level    slog.Level
		opts     []ClientOption
		endpoint string
		want     []string // "LEVEL message" of every record, in order
	}{
		"success: INFO": {
			level: slog.LevelInfo, endpoint: "https://api.typesafe.ai/v1/systemone",
			want: []string{"INFO response"},
		},
		"success: DEBUG": {
			level: slog.LevelDebug, endpoint: "https://api.typesafe.ai/v1/systemone",
			want: []string{"DEBUG request", "INFO response", "DEBUG response headers"},
		},
		"success: LevelTrace, the endpoint's path alone": {
			level: LevelTrace, opts: []ClientOption{WithLogEndpointHost(false)}, endpoint: "/v1/systemone",
			want: []string{"DEBUG request", "DEBUG-4 request body", "INFO response", "DEBUG response headers", "DEBUG-4 response body"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			logs := testsupport.NewLogRecorder(tt.level)
			rec := replying(http.StatusOK, result, "X-Typesafe-Request-Id", "req\nlog")
			c := newTestClient(t, rec, append(tt.opts, WithLogger(logs.Logger()))...)
			if _, err := c.SystemOne(t.Context(), "hello", noulQuestion(t)); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			var got []string
			for _, r := range logs.Records() {
				got = append(got, r.Level.String()+" "+r.Message)
				if v, ok := r.Attr("endpoint"); !ok || v.String() != tt.endpoint {
					t.Errorf("%s endpoint = %v, want %q", r.Message, v, tt.endpoint)
				}
				switch r.Message {
				case "response":
					status, _ := r.Attr("status")
					id, _ := r.Attr("request_id")
					attempt, _ := r.Attr("attempt")
					_, hasDuration := r.Attr("duration")
					if status.Int64() != 200 || id.String() != `req\nlog` || attempt.Int64() != 0 || !hasDuration {
						t.Errorf("response record %s", r)
					}
				case "request":
					auth, _ := r.Attr("headers.Authorization")
					n, _ := r.Attr("body_bytes")
					if auth.String() != redacted || n.Int64() != int64(len(`{"state":"hello","model":"jev-latest",`+noulBody+`}`)) {
						t.Errorf("request record %s", r)
					}
				case "response body":
					if b, _ := r.Attr("body"); b.String() != string(result) {
						t.Errorf("response body record %s", r)
					}
				}
			}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("records (-want +got):\n%s", diff)
			}
		})
	}
	t.Run("success: a transport failure", func(t *testing.T) {
		logs := testsupport.NewLogRecorder(slog.LevelInfo)
		rec := &testsupport.Recorder{Replies: []testsupport.Reply{{Err: errString("dial refused")}}}
		c := newTestClient(t, rec, WithLogger(logs.Logger()))
		if _, err := c.Models().List(t.Context()); err == nil {
			t.Fatal("List succeeded, want a transport failure")
		}
		records := logs.Records()
		if len(records) != 1 || records[0].Message != "request failed" {
			t.Fatalf("records:\n%s", recordsText(logs))
		}
		if v, _ := records[0].Attr("error"); v.String() != "Connection error: dial refused" {
			t.Errorf("error attribute = %q", v)
		}
	})
}

// errString is an error with a fixed text.
type errString string

func (e errString) Error() string { return string(e) }
