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
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/engine"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// secretSpellings are the nine header spellings test_secret_headers_redacted
// runs (pytest:test_logging.py:17-27): the six names of SECRET_HEADERS in
// several cases, and names that only contain "token" or "secret".
var secretSpellings = []string{
	"Authorization",
	"Proxy-Authorization",
	"X-API-Key",
	"API-Key",
	"Cookie",
	"Set-Cookie",
	"X-Access-Token",
	"X-Client-Secret",
	"x-MiXeD-ToKeN",
}

// TestRedactedHeadersSecretSpellings ports the header part of
// test_secret_headers_redacted (L1) as far as it applies to the client's
// configuration: for each of the nine spellings, a header of that name set
// with WithHeader and a response header of that name are printed as "***",
// the API key never shows, and a header that is not a credential shows as
// it is. The record goes through the three handlers a caller is likely to
// use; each resolves the LogValuer.
func TestRedactedHeadersSecretSpellings(t *testing.T) {
	for _, spelling := range secretSpellings {
		t.Run(spelling, func(t *testing.T) {
			c := mustResolve(t, noEnv,
				WithAPIKey("auth-credential"),
				WithHeader(spelling, "request-credential"),
				WithHeader("x-visible", "request-visible"),
			)
			response := http.Header{}
			response.Set(spelling, "response-credential")
			response.Set("X-Visible", "response-visible")

			for handlerName, render := range renderers() {
				out := render(func(logger *slog.Logger) {
					logger.Debug("request", slog.Any("headers", engine.NewRedactedHeaders(c.SystemOneHeader, engine.NewHeaderRedactor(c.APIKey))))
					logger.Debug("response", slog.Any("headers", engine.NewRedactedHeaders(response, engine.NewHeaderRedactor(c.APIKey))))
				})
				for _, visible := range []string{"request-visible", "response-visible", "***"} {
					if !strings.Contains(out, visible) {
						t.Errorf("%s output does not contain %q:\n%s", handlerName, visible, out)
					}
				}
				for _, secret := range []string{"auth-credential", "request-credential", "response-credential"} {
					if strings.Contains(out, secret) {
						t.Errorf("%s output contains %q:\n%s", handlerName, secret, out)
					}
				}
			}
		})
	}
}

// renderers returns, by name, functions that run a logging function against
// a handler at debug level and return what it wrote.
func renderers() map[string]func(log func(*slog.Logger)) string {
	opts := &slog.HandlerOptions{Level: slog.LevelDebug}
	return map[string]func(log func(*slog.Logger)) string{
		"TextHandler": func(log func(*slog.Logger)) string {
			var buf bytes.Buffer
			log(slog.New(slog.NewTextHandler(&buf, opts)))
			return buf.String()
		},
		"JSONHandler": func(log func(*slog.Logger)) string {
			var buf bytes.Buffer
			log(slog.New(slog.NewJSONHandler(&buf, opts)))
			return buf.String()
		},
		"LogRecorder": func(log func(*slog.Logger)) string {
			rec := testsupport.NewLogRecorder(nil)
			log(rec.Logger())
			var sb strings.Builder
			for _, r := range rec.Records() {
				sb.WriteString(r.String())
				sb.WriteByte('\n')
			}
			return sb.String()
		},
	}
}

// TestAPIKeyNeedleThreshold pins ruling R68: the key is looked for inside a
// WithHeader name and inside a header value only when it is at least
// engine.MinKeyNeedleBytes (8) long, so a test's short dummy key neither
// refuses an ordinary name nor hides an ordinary value, while Authorization
// is redacted by its name whatever the key; the client's redactor is the
// one of its key.
func TestAPIKeyNeedleThreshold(t *testing.T) {
	tests := map[string]struct {
		key         string
		name        string // a WithHeader name that contains the key
		wantRefused bool
		wantEcho    string // how X-Echo, whose value holds the key, prints
	}{
		"success: 1-byte key is not looked for": {
			key: "k", name: "X-K", wantEcho: "id=k",
		},
		"success: 7-byte key is not looked for": {
			key: "test-id", name: "X-Test-Id", wantEcho: "id=test-id",
		},
		"success: 8-byte key is looked for": {
			key: "trace-id", name: "X-Trace-Id", wantRefused: true, wantEcho: "***",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if len(tt.key) >= engine.MinKeyNeedleBytes != tt.wantRefused {
				t.Fatalf("row %q: a %d-byte key contradicts engine.MinKeyNeedleBytes = %d", name, len(tt.key), engine.MinKeyNeedleBytes)
			}
			opts := []ClientOption{WithAPIKey(tt.key), WithHeader(tt.name, "v")}
			if tt.wantRefused {
				err := resolveError(t, noEnv, opts...)
				if want := "The name given to WithHeader call 1 contains the API key, so it is not shown; pass the key with WithAPIKey only."; err.Error() != want {
					t.Errorf("Error() = %q, want %q", err.Error(), want)
				}
			} else {
				c := mustResolve(t, noEnv, opts...)
				if got := c.ModelsHeader.Get(tt.name); got != "v" {
					t.Errorf("template %s = %q, want %q", tt.name, got, "v")
				}
			}

			if r := (&config{APIKey: tt.key}).Redactor(); r != engine.NewHeaderRedactor(tt.key) {
				t.Errorf("config.redactor() = %+v, want the redactor of the client's key", r)
			}
			header := http.Header{"Authorization": {"Bearer " + tt.key}, "X-Echo": {"id=" + tt.key}}
			want := "[Authorization=*** X-Echo=" + tt.wantEcho + "]"
			if got := fmt.Sprint(engine.NewRedactedHeaders(header, engine.NewHeaderRedactor(tt.key))); got != want {
				t.Errorf("rendered = %q, want %q", got, want)
			}
		})
	}
}

// TestErrorHeadersRedacted pins ruling R87 (K31, verify-p2 item 4) through
// the client for the three error types that keep a response's header,
// *APIError (from a failure status, and from one whose body passed the size
// limit), *ResponseValidationError and *ResponseTooLargeError: the
// stored Header, and so every fmt verb of the error, of the error's value
// held in an unexported field (which fmt prints field by field, D-W2.1b)
// and of a pointer to it held there, shows "***" for Set-Cookie, X-Api-Key,
// Authorization and a header whose value echoes the API key, and never the
// server's credential; the request id and the retry-after headers stay as
// sent, so RequestID, RetryAfter and IsAuthentication answer as before.
func TestErrorHeadersRedacted(t *testing.T) {
	const key = "ts_live_0123456789abcdef"
	const cookie, serverKey, serverAuth = "session=server-credential", "server-api-credential", "Bearer server-auth-credential"
	secrets := []string{cookie, serverKey, serverAuth, key, "server-credential", "server-auth-credential"}
	sent := []string{
		"Set-Cookie", cookie, "X-Api-Key", serverKey, "Authorization", serverAuth, "X-Echo", "echo Bearer " + key,
		"X-Typesafe-Request-Id", "req_123", "Retry-After-Ms", "125", "X-Visible", "response-visible",
	}
	want := http.Header{
		"Content-Type": {"application/json"}, "Set-Cookie": {engine.Redacted}, "X-Api-Key": {engine.Redacted}, "Authorization": {engine.Redacted}, "X-Echo": {engine.Redacted},
		"X-Typesafe-Request-Id": {"req_123"}, "Retry-After-Ms": {"125"}, "X-Visible": {"response-visible"},
	}
	type holder struct {
		value any // the error's struct value, which fmt prints field by field
		ptr   error
	}
	validation := func(t *testing.T, err error) (http.Header, any) {
		e, ok := errors.AsType[*ResponseValidationError](err)
		if !ok {
			t.Fatalf("error = %T %v, want a *ResponseValidationError", err, err)
		}
		if id, ok := e.RequestID(); !ok || id != "req_123" {
			t.Errorf("RequestID() = %q, %t, want req_123", id, ok)
		}
		return e.Header, *e
	}
	tests := map[string]struct {
		status    int
		body      string
		opts      []ClientOption
		systemOne bool // call SystemOne rather than list the models
		check     func(t *testing.T, err error) (http.Header, any)
	}{
		"error: *APIError (401)": {
			status: http.StatusUnauthorized, body: `{"message":"bad key"}`,
			check: func(t *testing.T, err error) (http.Header, any) {
				e, ok := errors.AsType[*APIError](err)
				if !ok {
					t.Fatalf("error = %T %v, want an *APIError", err, err)
				}
				if d, ok := e.RetryAfter(); !ok || d != 125*time.Millisecond {
					t.Errorf("RetryAfter() = %v, %t, want 125ms, true", d, ok)
				}
				if !e.IsAuthentication() {
					t.Error("IsAuthentication() = false for a 401")
				}
				if id, ok := e.RequestID(); !ok || id != "req_123" {
					t.Errorf("RequestID() = %q, %t, want req_123", id, ok)
				}
				return e.Header, *e
			},
		},
		"error: *APIError (503 over the size limit, without its body)": {
			status: http.StatusServiceUnavailable, body: `{"message":"overloaded"}`, opts: []ClientOption{WithMaxResponseBytes(4)},
			check: func(t *testing.T, err error) (http.Header, any) {
				e, ok := errors.AsType[*APIError](err)
				if !ok || e.Body != nil {
					t.Fatalf("error = %T %v, want an *APIError without a body", err, err)
				}
				if d, ok := e.RetryAfter(); !ok || d != 125*time.Millisecond {
					t.Errorf("RetryAfter() = %v, %t, want 125ms, true", d, ok)
				}
				return e.Header, *e
			},
		},
		"error: *ResponseValidationError (200, a body that is not a model list)": {
			status: http.StatusOK, body: `{"models":5}`, check: validation,
		},
		"error: *ResponseValidationError (200, a System One body without its members)": {
			status: http.StatusOK, body: `{}`, systemOne: true, check: validation,
		},
		"error: *ResponseTooLargeError (200 over the size limit)": {
			status: http.StatusOK, body: `{"models":[]}`, opts: []ClientOption{WithMaxResponseBytes(4)},
			check: func(t *testing.T, err error) (http.Header, any) {
				e, ok := errors.AsType[*ResponseTooLargeError](err)
				if !ok {
					t.Fatalf("error = %T %v, want a *ResponseTooLargeError", err, err)
				}
				if id, ok := e.RequestID(); !ok || id != "req_123" {
					t.Errorf("RequestID() = %q, %t, want req_123", id, ok)
				}
				return e.Header, *e
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			rec := replying(tt.status, []byte(tt.body), sent...)
			c := newEnvClient(t, rec, append([]ClientOption{WithAPIKey(key)}, tt.opts...)...)
			var err error
			if tt.systemOne {
				_, err = c.SystemOne(t.Context(), "hi", noulQuestion(t), Retry(NoRetry()))
			} else {
				_, err = c.Models().List(t.Context(), Retry(NoRetry()))
			}
			header, value := tt.check(t, err)
			if diff := gocmp.Diff(want, header); diff != "" {
				t.Errorf("the stored Header (-want +got):\n%s", diff)
			}
			h := holder{value: value, ptr: err}
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
				for what, v := range map[string]any{"the error": err, "a holder": h} {
					out := fmt.Sprintf(verb, v)
					for _, s := range secrets {
						if strings.Contains(out, s) {
							t.Errorf("%s of %s holds %q: %s", verb, what, s, out)
						}
					}
				}
			}
			if out := fmt.Sprintf("%#v", err); !strings.Contains(out, `"Set-Cookie":[]string{"***"}`) || !strings.Contains(out, `"X-Typesafe-Request-Id":[]string{"req_123"}`) {
				t.Errorf("%%#v of the error does not show the redacted and the kept headers: %s", out)
			}
		})
	}
}

// TestServerEchoedKeyShownAsReceived pins owner decision G7 (8), rulings
// R103-rev and R107: text the server composed in the body is not redacted,
// and a value the SDK takes from a header is. A message, a field path's
// names and a skipped answer's name that echo the client's API key show
// it: in Message or FieldPath, in Error(), %v and %+v (escaped and cut as
// any message is), and in the WARN line naming the skipped answer. Body and
// the LevelTrace "response body" record hold the body as it arrived. The
// stored Header shows "***" for a header value that holds the key, and so
// does the request id, in Error() and in the INFO "response" record alike
// (R87). In these responses no other record above LevelTrace holds any 8
// consecutive bytes of the key.
func TestServerEchoedKeyShownAsReceived(t *testing.T) {
	const key = "ts_live_QzXjWvKpYbNmHgFd"
	const models = "GET https://api.typesafe.ai/v1/models: "
	const systemOne = "POST https://api.typesafe.ai/v1/systemone: 200 Invalid response data at '"
	const plainID = "req_123"
	pad := strings.Repeat("m", 190)
	noul := func(name string) string { return `{"model":"m","usage":{},"answers":{"` + name + `":{"type":"noul"}}}` }
	logged := []string{"request", "response", "response headers"} // the records above LevelTrace of every call
	tests := map[string]struct {
		status    int
		body      string
		id        string // the X-Typesafe-Request-Id sent; empty sends plainID
		systemOne bool   // call SystemOne rather than list the models
		message   string // the *APIError's Message
		path      string // the *ResponseValidationError's FieldPath
		shownID   string // the request id Error, RequestID and the INFO record show
		warned    []string
		want      string // Error(), %v and %+v
	}{
		"error: a 401 message that echoes the key": {
			status: http.StatusUnauthorized, body: `{"message":"invalid key ` + key + `"}`,
			message: "invalid key " + key, shownID: plainID,
			want: models + "401 invalid key " + key + " (request_id=" + plainID + ")",
		},
		"error: a message with the key across the 200-character cut keeps the key's first bytes": {
			status: http.StatusBadRequest, body: `{"message":"` + pad + " " + key + ` and more"}`,
			message: pad + " " + key[:9] + "…", shownID: plainID, // the message's 200 characters, then the cut
			want: models + "400 " + pad + " " + key[:9] + "… (request_id=" + plainID + ")",
		},
		"error: an answer named with the key": {
			status: http.StatusOK, body: noul(key), systemOne: true,
			path: "answers." + key + ".noul", shownID: plainID,
			want: systemOne + "answers." + key + ".noul'. (request_id=" + plainID + ")",
		},
		"error: a probability key that echoes the key": {
			status: http.StatusOK, systemOne: true,
			body: `{"model":"m","usage":{},"answers":{"q":{"type":"choice","choice":"a","confidence":0.5,"probabilities":{"` + key + `":"x"}}}}`,
			path: "answers.q.probabilities." + key, shownID: plainID,
			want: systemOne + "answers.q.probabilities." + key + "'. (request_id=" + plainID + ")",
		},
		"error: a request id that echoes the key is *** in Error and in the INFO record (R87)": {
			status: http.StatusUnauthorized, body: `{"message":"no"}`, id: "req " + key,
			message: "no", shownID: engine.Redacted,
			want: models + "401 no (request_id=" + engine.Redacted + ")",
		},
		"error: an unrecognized answer named with the key is logged at WARN as received": {
			status: http.StatusOK, systemOne: true,
			body: `{"model":"m","usage":{},"answers":{"` + key + `":{"type":"aurora"},"q":{"type":"noul"}}}`,
			path: "answers.q.noul", shownID: plainID, warned: []string{key},
			want: systemOne + "answers.q.noul'. (request_id=" + plainID + ")",
		},
	}
	// shown is what the error and the SDK's records show of one response.
	type shown struct {
		Error, V, PlusV    string
		Message, FieldPath string
		RequestID, InfoID  string // the error's RequestID, the INFO "response" record's request_id
		Body               string
		Header             http.Header
		Warned             []string // the answer of each WARN "Ignoring answer" record
		Logged             []string // the messages of the records above LevelTrace
		TraceBody          string
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			id := cmp.Or(tt.id, plainID)
			logs := testsupport.NewLogRecorder(LevelTrace)
			clearEnv(t)
			c := newEnvClient(t, replying(tt.status, []byte(tt.body), "X-Echo", "echo "+key, "X-Typesafe-Request-Id", id),
				WithAPIKey(key), WithLogger(logs.Logger()))
			var err error
			if tt.systemOne {
				_, err = c.SystemOne(t.Context(), "hi", noulQuestion(t), Retry(NoRetry()))
			} else {
				_, err = c.Models().List(t.Context(), Retry(NoRetry()))
			}
			if err == nil {
				t.Fatal("error = nil, want an error for the response")
			}
			got := shown{Error: err.Error(), V: fmt.Sprintf("%v", err), PlusV: fmt.Sprintf("%+v", err)}
			if tt.systemOne {
				e, ok := errors.AsType[*ResponseValidationError](err)
				if !ok {
					t.Fatalf("error = %T %v, want a *ResponseValidationError", err, err)
				}
				got.FieldPath, got.Body, got.Header = e.FieldPath, string(e.Body), e.Header
				got.RequestID, _ = e.RequestID()
			} else {
				e, ok := errors.AsType[*APIError](err)
				if !ok {
					t.Fatalf("error = %T %v, want an *APIError", err, err)
				}
				got.Message, got.Body, got.Header = e.Message, string(e.Body), e.Header
				got.RequestID, _ = e.RequestID()
			}
			for _, r := range logs.Records() {
				switch {
				case r.Level > LevelTrace:
					got.Logged = append(got.Logged, r.Message)
					if r.Message == engine.MsgSkippedAnswer {
						a, _ := r.Attr("answer")
						got.Warned = append(got.Warned, a.String())
						continue // body text, shown as received (R103-rev)
					}
					if r.Message == "response" {
						v, _ := r.Attr("request_id")
						got.InfoID = v.String()
					}
					for i := range len(key) - engine.MinKeyNeedleBytes + 1 {
						if w := key[i : i+engine.MinKeyNeedleBytes]; strings.Contains(r.String(), w) {
							t.Errorf("a record above LevelTrace holds %q of the key: %s", w, r)
						}
					}
				case r.Message == "response body":
					b, _ := r.Attr("body")
					got.TraceBody = b.String()
				}
			}
			wantLogged := logged
			if tt.warned != nil {
				wantLogged = append(slices.Clone(logged), engine.MsgSkippedAnswer)
			}
			want := shown{
				Error: tt.want, V: tt.want, PlusV: tt.want,
				Message: tt.message, FieldPath: tt.path,
				RequestID: tt.shownID, InfoID: tt.shownID,
				Body: tt.body,
				Header: http.Header{
					"Content-Type": {"application/json"}, "X-Echo": {engine.Redacted},
					"X-Typesafe-Request-Id": {tt.shownID},
				},
				Warned: tt.warned, Logged: wantLogged, TraceBody: tt.body,
			}
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("what the error and the records show (-want +got):\n%s\n%s", diff, recordsText(logs))
			}
		})
	}
}
