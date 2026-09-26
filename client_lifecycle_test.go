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
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// apiHandler serves the two API endpoints under any path prefix: the
// upstream RESULT for POST .../v1/systemone and models.json for GET
// .../v1/models.
func apiHandler(t *testing.T) http.Handler {
	t.Helper()
	result := testsupport.Fixture(t, "result.json")
	models := testsupport.Fixture(t, "models.json")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, systemOnePath):
			_, _ = w.Write(result)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, modelsPath):
			_, _ = w.Write(models)
		default:
			http.NotFound(w, r)
		}
	})
}

// closingRT is a caller's RoundTripper that implements io.Closer and counts
// its Close calls.
type closingRT struct {
	rt     http.RoundTripper
	closes atomic.Int32
}

func (c *closingRT) RoundTrip(req *http.Request) (*http.Response, error) { return c.rt.RoundTrip(req) }

func (c *closingRT) Close() error {
	c.closes.Add(1)
	return nil
}

// TestSystemOneOverHTTP2 runs the client over the SDK's own transport and a
// real TLS connection to the loopback server (WithRootCAs trusts its
// certificate): WarmUp opens the one HTTP/2 connection (Stats: one dial, one
// attempt) and three calls reuse it (one dial, four attempts), under a base
// URL's path prefix, with the SDK's headers on the wire and no
// X-TypeSafe-Retry-Count (C15's wire half).
func TestSystemOneOverHTTP2(t *testing.T) {
	srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: apiHandler(t)})
	clearEnv(t)
	c, err := NewClient(WithAPIKey(testKey), WithBaseURL(srv.URL()+"/prefix///"), WithRootCAs(testsupport.RootCAs(t)))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.WarmUp(t.Context()); err != nil {
		t.Fatalf("WarmUp: %v", err)
	}
	// WarmUp opened the connection the calls then share.
	if diff := gocmp.Diff(Stats{Dials: 1, Attempts: 1}, c.Stats()); diff != "" {
		t.Errorf("Stats after WarmUp (-want +got):\n%s", diff)
	}
	for range 3 {
		resp, err := c.SystemOne(t.Context(), map[string]any{"document": "Hello 🌍"}, q3Questions(t))
		if err != nil {
			t.Fatalf("SystemOne: %v", err)
		}
		if n, ok := resp.Answers().Noul("spam"); !ok || n.Noul() != 0.98 {
			t.Errorf(`Answers().Noul("spam") = %v, %t`, n.Noul(), ok)
		}
	}
	if diff := gocmp.Diff(Stats{Dials: 1, Attempts: 4}, c.Stats()); diff != "" {
		t.Errorf("Stats (-want +got):\n%s", diff)
	}
	if srv.Accepts() != 1 {
		t.Errorf("the server accepted %d connections, want 1", srv.Accepts())
	}
	type seen struct{ Proto, Method, Path, Authorization, ContentType, RetryCount string }
	var got []seen
	for _, r := range srv.Requests() {
		got = append(got, seen{r.Proto, r.Method, r.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Header.Get(headerRetryCount)})
	}
	post := seen{"HTTP/2.0", http.MethodPost, "/prefix/v1/systemone", "Bearer " + testKey, "application/json", ""}
	want := []seen{{"HTTP/2.0", http.MethodGet, "/prefix/v1/models", "Bearer " + testKey, "", ""}, post, post, post}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("requests on the wire (-want +got):\n%s", diff)
	}
}

// closeWatch returns a ConnState hook for a test server and a channel that
// receives once for every connection the server sees closed.
func closeWatch() (func(net.Conn, http.ConnState), <-chan struct{}) {
	closed := make(chan struct{}, 16)
	return func(_ net.Conn, st http.ConnState) {
		if st == http.StateClosed {
			closed <- struct{}{}
		}
	}, closed
}

// waitClosed waits up to 5 s for the server behind closed to see a
// connection closed.
func waitClosed(t *testing.T, closed <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: no connection closed within 5 s", what)
	}
}

// TestCallerTransportKeepsItsSettings ports test_http_client_settings (C16)
// to WithHTTPTransport: the client works over a clone of the caller's
// *http.Transport, so the requests go through the caller's dialer and TLS
// configuration and carry the SDK's headers, while the caller's transport
// is neither used nor changed. The connection stays open until Close, which
// closes the clone's idle connection.
func TestCallerTransportKeepsItsSettings(t *testing.T) {
	type seen struct{ Proto, Method, Authorization, SDKDefault, Call string }
	var mu sync.Mutex
	var requests []seen
	api := apiHandler(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, seen{r.Proto, r.Method, r.Header.Get("Authorization"), r.Header.Get("X-Sdk-Default"), r.Header.Get("X-Call")})
		mu.Unlock()
		api.ServeHTTP(w, r)
	}))
	srv.EnableHTTP2 = true
	hook, closed := closeWatch()
	srv.Config.ConnState = hook
	srv.StartTLS()
	t.Cleanup(srv.Close)

	var dials atomic.Int32
	dialer := &net.Dialer{}
	tlsConfig := &tls.Config{RootCAs: srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs, MinVersion: tls.VersionTLS12}
	tr := &http.Transport{
		TLSClientConfig:   tlsConfig,
		ForceAttemptHTTP2: true,
		MaxIdleConns:      7,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dials.Add(1)
			return dialer.DialContext(ctx, network, addr)
		},
	}
	clearEnv(t)
	c, err := NewClient(WithAPIKey(testKey), WithHTTPTransport(tr), WithBaseURL(srv.URL), WithHeader("X-Sdk-Default", "sdk"), WithHeader("X-Call", "sdk"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	models, err := c.Models().List(t.Context(), Header("X-Call", "call"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(models.Models()) != 1 {
		t.Errorf("List returned %d models, want 1", len(models.Models()))
	}
	if _, err := c.SystemOne(t.Context(), "x", noulQuestion(t), Header("X-Call", "call")); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	mu.Lock()
	got := requests
	mu.Unlock()
	want := []seen{
		{"HTTP/2.0", http.MethodGet, "Bearer " + testKey, "sdk", "call"},
		{"HTTP/2.0", http.MethodPost, "Bearer " + testKey, "sdk", "call"},
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("requests (-want +got):\n%s", diff)
	}
	if tr.TLSClientConfig != tlsConfig || tr.MaxIdleConns != 7 || tr.Proxy != nil || !tr.ForceAttemptHTTP2 || tr.MaxConnsPerHost != 0 {
		t.Errorf("the caller's transport changed: TLS %p (want %p), MaxIdleConns %d, Proxy set %t, MaxConnsPerHost %d", tr.TLSClientConfig, tlsConfig, tr.MaxIdleConns, tr.Proxy != nil, tr.MaxConnsPerHost)
	}
	if n := dials.Load(); n != 1 {
		t.Errorf("the caller's dialer ran %d times, want 1", n)
	}
	if diff := gocmp.Diff(Stats{Dials: 1, Attempts: 2}, c.Stats()); diff != "" {
		t.Errorf("Stats (-want +got):\n%s", diff)
	}
	if n := len(closed); n != 0 {
		t.Fatalf("%d connections closed before Close, want 0", n)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitClosed(t, closed, "Close of a WithHTTPTransport client")
}

// TestCloseClosesSuppliedTransport ports test_supplied_network_resources_closed
// (C17, AC-F9): Close calls Close once on a WithRoundTripper transport that
// is an io.Closer, however often the client is closed; a call after Close
// fails with a *ConfigError that wraps ErrClientClosed and says the client
// is closed. A WithHTTPTransport clone's idle connections close at Close
// (TestCallerTransportKeepsItsSettings).
func TestCloseClosesSuppliedTransport(t *testing.T) {
	tests := map[string]struct {
		rt     func(*testsupport.Recorder) http.RoundTripper
		closes func(http.RoundTripper) int
	}{
		"success: an io.Closer is closed once": {
			rt:     func(rec *testsupport.Recorder) http.RoundTripper { return rec },
			closes: func(rt http.RoundTripper) int { return rt.(*testsupport.Recorder).Closes() },
		},
		"success: an io.Closer wrapper is closed once": {
			rt:     func(rec *testsupport.Recorder) http.RoundTripper { return &closingRT{rt: rec} },
			closes: func(rt http.RoundTripper) int { return int(rt.(*closingRT).closes.Load()) },
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(http.StatusOK, []byte(`{"models":[]}`))
			rt := tt.rt(rec)
			c := newTestClient(t, rt)
			if _, err := c.Models().List(t.Context()); err != nil {
				t.Fatalf("List: %v", err)
			}
			for range 2 {
				if err := c.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
			}
			if n := tt.closes(rt); n != 1 {
				t.Errorf("the transport was closed %d times, want 1", n)
			}
			_, err := c.Models().List(t.Context())
			if _, ok := errors.AsType[*ConfigError](err); !ok || !errors.Is(err, ErrClientClosed) || !strings.Contains(err.Error(), "closed") {
				t.Errorf("List after Close = %v, want a *ConfigError wrapping ErrClientClosed", err)
			}
			if rec.Count() != 1 {
				t.Errorf("the transport saw %d requests, want 1", rec.Count())
			}
		})
	}
}

// TestCloseClosesOwnedTransport ports test_owned_http_client_closed (C18):
// the transport a client builds has the default 10 s timeout, and Close
// closes its idle connection.
func TestCloseClosesOwnedTransport(t *testing.T) {
	srv := httptest.NewUnstartedServer(apiHandler(t))
	hook, closed := closeWatch()
	srv.Config.ConnState = hook
	srv.Start()
	t.Cleanup(srv.Close)
	clearEnv(t)
	c, err := NewClient(WithAPIKey(testKey), WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.cfg.transport.gate == nil || c.cfg.timeout != DefaultTimeout {
		t.Errorf("SDK transport %t, timeout %v, want the SDK's own transport and %v", c.cfg.transport.gate != nil, c.cfg.timeout, DefaultTimeout)
	}
	if _, err := c.Models().List(t.Context()); err != nil {
		t.Fatalf("List: %v", err)
	}
	if n := len(closed); n != 0 {
		t.Fatalf("%d connections closed before Close, want 0", n)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitClosed(t, closed, "Close of the owned transport")
	if _, err := c.Models().List(t.Context()); !errors.Is(err, ErrClientClosed) {
		t.Errorf("List after Close = %v, want ErrClientClosed", err)
	}
}

// TestCloseAfterFailedCall ports test_exceptional_context_closes_http_client
// (C19): a call whose transport fails returns an error that wraps the
// transport's own (a caller's error, and a cancellation), and Close then
// closes the supplied transport once.
func TestCloseAfterFailedCall(t *testing.T) {
	failure := errors.New("original failure")
	for name, cause := range map[string]error{"error: a transport failure": failure, "error: a cancellation": context.Canceled} {
		t.Run(name, func(t *testing.T) {
			rec := &testsupport.Recorder{Replies: []testsupport.Reply{{Err: cause}}}
			c := newTestClient(t, rec)
			_, err := c.Models().List(t.Context())
			if !errors.Is(err, cause) {
				t.Fatalf("List error = %v, want one wrapping %v", err, cause)
			}
			if err := c.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if rec.Closes() != 1 {
				t.Errorf("the transport was closed %d times, want 1", rec.Closes())
			}
		})
	}
}

// assertCancelled checks that err is the cancelled context's own error,
// context.Canceled, and not an SDK error: typesafe-sdk-python maps only
// httpx's RequestError (py:_core/transport.py:79), so a cancellation reaches
// its caller as asyncio.CancelledError itself (tests/test_clients.py:559-596).
func assertCancelled(t *testing.T, err error) {
	t.Helper()
	if err != context.Canceled { //nolint:errorlint // the context's own error, not one wrapping it
		t.Errorf("error = %T %v, want context.Canceled itself", err, err)
	}
	if _, ok := errors.AsType[Error](err); ok {
		t.Errorf("error = %T %v, an SDK error; a cancellation is not one", err, err)
	}
}

// waitFor polls cond until it holds, failing the test when it does not
// within d.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %v", what, d)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ctxBody is a response body whose Read reports started once, then blocks
// until ctx is done and fails with its error.
type ctxBody struct {
	ctx     context.Context
	started func()
}

func (b ctxBody) Read([]byte) (int, error) {
	b.started()
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (ctxBody) Close() error { return nil }

// TestCancelInFlightRequest ports test_task_cancellation_closes_context
// (C20): cancelling the context of a call whose request is in flight ends it
// promptly after one attempt with context.Canceled itself, not an SDK error
// (assertCancelled); over the SDK's own transport the loopback server sees
// the stream reset, and Close then closes a supplied transport once. The
// cancellation lands while the request waits for its response and while its
// body is read.
func TestCancelInFlightRequest(t *testing.T) {
	type setup struct {
		c     *Client
		after func(t *testing.T) // checks what the transport saw
	}
	tests := map[string]struct {
		setup func(t *testing.T, started func()) setup
	}{
		"success: the loopback server sees the stream reset": {
			setup: func(t *testing.T, started func()) setup {
				release := make(chan struct{})
				t.Cleanup(func() { close(release) })
				srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					started()
					select {
					case <-r.Context().Done():
					case <-release:
					}
				})})
				clearEnv(t)
				c, err := NewClient(WithAPIKey(testKey), WithBaseURL(srv.URL()), WithRootCAs(testsupport.RootCAs(t)), WithProxy(nil))
				if err != nil {
					t.Fatalf("NewClient: %v", err)
				}
				t.Cleanup(func() { _ = c.Close() })
				return setup{c: c, after: func(t *testing.T) {
					waitFor(t, 5*time.Second, "the server sees the stream dropped", func() bool {
						reqs := srv.Requests()
						return len(reqs) == 1 && reqs[0].Dropped
					})
					if n := srv.Accepts(); n != 1 {
						t.Errorf("the server accepted %d connections, want 1", n)
					}
				}}
			},
		},
		"success: Close then closes a supplied transport once": {
			setup: func(t *testing.T, started func()) setup {
				rt := &closingRT{rt: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					started()
					<-req.Context().Done()
					return nil, req.Context().Err()
				})}
				c := newTestClient(t, rt)
				return setup{c: c, after: func(t *testing.T) {
					if err := c.Close(); err != nil {
						t.Fatalf("Close: %v", err)
					}
					if n := rt.closes.Load(); n != 1 {
						t.Errorf("the transport was closed %d times, want 1", n)
					}
				}}
			},
		},
		"success: a cancellation while the body is read": {
			setup: func(t *testing.T, started func()) setup {
				rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{}, ContentLength: -1, Request: req,
						Body: ctxBody{ctx: req.Context(), started: started},
					}, nil
				})
				return setup{c: newTestClient(t, rt), after: func(*testing.T) {}}
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			startedc := make(chan struct{})
			var once sync.Once
			s := tt.setup(t, func() { once.Do(func() { close(startedc) }) })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			errc := make(chan error, 1)
			go func() {
				_, err := s.c.SystemOne(ctx, "x", noulQuestion(t))
				errc <- err
			}()
			select {
			case <-startedc:
			case <-time.After(5 * time.Second):
				t.Fatal("the request did not reach the transport within 5 s")
			}
			cancel()
			select {
			case err := <-errc:
				assertCancelled(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("the call did not return within 5 s of the cancellation")
			}
			if n := s.c.Stats().Attempts; n != 1 {
				t.Errorf("Stats().Attempts = %d, want 1", n)
			}
			s.after(t)
		})
	}
}

// TestCancelledContextMakesOneAttempt ports test_cancellation_propagates
// (C21) under the production policy, which retries a failed attempt: a
// cancellation makes one attempt. Upstream's transport raises
// CancelledError while the task runs; here the transport reports
// context.Canceled while the call's context lives, which the attempt
// classifies as a *ConnectionError, a class the policy retries, so only the
// rule that a cancellation is never retried keeps the call to one attempt
// (ruling R82 NIT 6). A call whose own context is cancelled returns
// context.Canceled itself, not an SDK error (assertCancelled).
func TestCancelledContextMakesOneAttempt(t *testing.T) {
	tests := map[string]struct {
		reply  testsupport.Reply
		cancel bool
		check  func(t *testing.T, err error)
	}{
		"error: the transport reports a cancellation (upstream's shape)": {
			reply: testsupport.Reply{Err: context.Canceled},
			check: func(t *testing.T, err error) {
				if _, ok := errors.AsType[*ConnectionError](err); !ok || !errors.Is(err, context.Canceled) {
					t.Errorf("error = %T %v, want a *ConnectionError wrapping context.Canceled", err, err)
				}
			},
		},
		"error: the call's context is cancelled": {
			reply:  testsupport.JSON(http.StatusOK, []byte(`{"models":[]}`)),
			cancel: true,
			check:  assertCancelled,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &testsupport.Recorder{Replies: []testsupport.Reply{tt.reply}}
			// The production policy, not newTestClient's single attempt: the
			// one attempt must come from the cancellation (rulings R82 NIT 6
			// and R88b).
			c := newTestClient(t, rec, WithRetry(DefaultRetry()))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			_, err := c.Models().List(ctx)
			tt.check(t, err)
			if rec.Count() != 1 || c.Stats().Attempts != 1 {
				t.Errorf("the transport saw %d requests and Stats counts %d attempts, want 1 and 1", rec.Count(), c.Stats().Attempts)
			}
		})
	}
}

// TestZeroClientRefused checks that a Client NewClient did not build fails
// every call and Close with a *ConfigError, and counts nothing.
func TestZeroClientRefused(t *testing.T) {
	var c Client
	var nilClient *Client
	for name, fn := range map[string]func() error{
		"SystemOne": func() error { _, err := c.SystemOne(t.Context(), "x", noulQuestion(t)); return err },
		"List":      func() error { _, err := c.Models().List(t.Context()); return err },
		"WarmUp":    func() error { return c.WarmUp(t.Context()) },
		"Close":     c.Close,
		"nil Close": nilClient.Close,
	} {
		if _, ok := errors.AsType[*ConfigError](fn()); !ok {
			t.Errorf("%s on a zero Client: want a *ConfigError", name)
		}
	}
	if diff := gocmp.Diff(Stats{}, nilClient.Stats()); diff != "" {
		t.Errorf("Stats of a nil Client (-want +got):\n%s", diff)
	}
}

// TestConcurrentCalls runs calls from many goroutines on one client, under
// -race in CI: they share the header template and the endpoint URL, which
// no call writes, and each gets its own answers.
func TestConcurrentCalls(t *testing.T) {
	rec := &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, testsupport.Fixture(t, "result.json"))}}
	c := newTestClient(t, rec)
	qs := q3Questions(t)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 8 {
				resp, err := c.SystemOne(t.Context(), map[string]any{"k": "v"}, qs)
				if err != nil {
					t.Error(err)
					return
				}
				if p, ok := resp.Answers().Choice("tone"); !ok || p.Choice() != "friendly" {
					t.Errorf("tone = %q, %t", p.Choice(), ok)
				}
			}
		})
	}
	wg.Wait()
	if n := c.Stats().Attempts; n != 16*8 {
		t.Errorf("Stats().Attempts = %d, want %d", n, 16*8)
	}
}

// netTimeout is a net.Error whose Timeout is true, as a dial or read
// deadline reports.
type netTimeout struct{}

func (netTimeout) Error() string   { return "i/o timeout" }
func (netTimeout) Timeout() bool   { return true }
func (netTimeout) Temporary() bool { return true }

// TestAttemptErrorClassification pins the attempt's classification of an
// error that ended it without a response (C13, section 6.3): the attempt's
// own deadline is a *TimeoutError naming the attempt's timeout, whether it
// passes before the response or while its body is read and whatever error
// the transport reports for it; the caller's deadline is one without a
// timeout; a network timeout is one too; anything else, a body cut short
// included, is a *ConnectionError. Each wraps the transport's error, except
// that a text holding a credential of the request is shown with "***" and
// the error unwraps to a stand-in: the whole Authorization value is
// replaced, as typesafe-sdk-python collects it (py:_core/logging.py:43).
func TestAttemptErrorClassification(t *testing.T) {
	const longKey = "ts_live_0123456789abcdef"
	blockUntilDone := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	tests := map[string]struct {
		rt     http.RoundTripper
		client []ClientOption
		ctx    func(t *testing.T) context.Context
		call   []CallOption
		check  func(t *testing.T, err error)
	}{
		"error: the attempt's deadline": {
			rt: blockUntilDone, call: []CallOption{Timeout(30 * time.Millisecond)},
			check: func(t *testing.T, err error) {
				te, ok := errors.AsType[*TimeoutError](err)
				if !ok || te.Timeout != 30*time.Millisecond || !errors.Is(err, context.DeadlineExceeded) || te.Error() != "Request timed out (timeout=0.03s)." {
					t.Errorf("error = %v (%T), want a *TimeoutError of 30ms wrapping context.DeadlineExceeded", err, err)
				}
			},
		},
		"error: the caller's deadline": {
			rt: blockUntilDone, client: []ClientOption{WithNoTimeout()},
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
				t.Cleanup(cancel)
				return ctx
			},
			check: func(t *testing.T, err error) {
				te, ok := errors.AsType[*TimeoutError](err)
				if !ok || te.Timeout != 0 || !errors.Is(err, context.DeadlineExceeded) || te.Error() != "Request timed out." {
					t.Errorf("error = %v (%T), want a *TimeoutError without a timeout", err, err)
				}
			},
		},
		"error: the caller's deadline before the attempt's": {
			rt: blockUntilDone,
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
				t.Cleanup(cancel)
				return ctx
			},
			check: func(t *testing.T, err error) {
				te, ok := errors.AsType[*TimeoutError](err)
				if !ok || te.Timeout != 0 || !errors.Is(err, context.DeadlineExceeded) || te.Error() != "Request timed out." {
					t.Errorf("error = %v (%T), want a *TimeoutError without a timeout: the client's 10s did not pass", err, err)
				}
			},
		},
		"error: a network timeout": {
			rt: &testsupport.Recorder{Replies: []testsupport.Reply{{Err: netTimeout{}}}},
			check: func(t *testing.T, err error) {
				if _, ok := errors.AsType[*TimeoutError](err); !ok || !errors.Is(err, netTimeout{}) {
					t.Errorf("error = %v (%T), want a *TimeoutError wrapping the net.Error", err, err)
				}
			},
		},
		"error: a text that holds the key": {
			rt: &testsupport.Recorder{Replies: []testsupport.Reply{{Err: errString("proxy said: Bearer " + longKey + "; key " + longKey)}}},
			check: func(t *testing.T, err error) {
				ce, ok := errors.AsType[*ConnectionError](err)
				_, standIn := ce.Unwrap().(*scrubbedError) //nolint:errorlint // the direct cause is the stand-in
				if !ok || ce.Error() != "Connection error: proxy said: ***; key ***" || !standIn || ce.Proxy() {
					t.Errorf("error = %v (%T), want a *ConnectionError with the credentials replaced, wrapping a stand-in", err, err)
				}
				assertNotPrinted(t, err, longKey)
			},
		},
		"error: the attempt's deadline, whatever error the transport reports": {
			rt: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, errString("net/http: request canceled")
			}),
			call: []CallOption{Timeout(30 * time.Millisecond)},
			check: func(t *testing.T, err error) {
				te, ok := errors.AsType[*TimeoutError](err)
				if !ok || te.Timeout != 30*time.Millisecond || !errors.Is(err, errString("net/http: request canceled")) {
					t.Errorf("error = %v (%T), want a *TimeoutError of 30ms wrapping the transport's error", err, err)
				}
			},
		},
		"error: the attempt's deadline while the body is read": {
			rt: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{}, ContentLength: -1, Request: req,
					Body: ctxBody{ctx: req.Context(), started: func() {}},
				}, nil
			}),
			call: []CallOption{Timeout(30 * time.Millisecond)},
			check: func(t *testing.T, err error) {
				te, ok := errors.AsType[*TimeoutError](err)
				if !ok || te.Timeout != 30*time.Millisecond || !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("error = %v (%T), want a *TimeoutError of 30ms wrapping context.DeadlineExceeded", err, err)
				}
			},
		},
		"error: a body cut short": {
			rt: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{}, ContentLength: 64, Request: req,
					Body: io.NopCloser(io.MultiReader(strings.NewReader(`{"models":`), iotest.ErrReader(io.ErrUnexpectedEOF))),
				}, nil
			}),
			check: func(t *testing.T, err error) {
				ce, ok := errors.AsType[*ConnectionError](err)
				if !ok || ce.Proxy() || !errors.Is(err, io.ErrUnexpectedEOF) || ce.Error() != "Connection error: unexpected EOF" {
					t.Errorf("error = %v (%T), want a *ConnectionError wrapping io.ErrUnexpectedEOF", err, err)
				}
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			c := newEnvClient(t, tt.rt, append([]ClientOption{WithAPIKey(longKey)}, tt.client...)...)
			var ctx context.Context // nil: callWithin's own
			if tt.ctx != nil {
				ctx = tt.ctx(t)
			}
			// Bounded (K29, K30): a row whose deadline no longer ends the
			// call fails by name instead of hanging the test binary.
			_, err := callWithin(t, func(bounded context.Context) error {
				_, err := c.Models().List(cmp.Or(ctx, bounded), tt.call...)
				return err
			})
			tt.check(t, err)
		})
	}
}

// TestCloseIdlesSuppliedHTTPTransport completes C17 (AC-F9, ruling R79): a
// *http.Transport given with WithRoundTripper counts as a supplied
// *http.Transport, so Close closes its idle connection, which the server
// sees closed; the connection stays open until then.
func TestCloseIdlesSuppliedHTTPTransport(t *testing.T) {
	srv := httptest.NewUnstartedServer(apiHandler(t))
	hook, closed := closeWatch()
	srv.Config.ConnState = hook
	srv.Start()
	t.Cleanup(srv.Close)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	clearEnv(t)
	c, err := NewClient(WithAPIKey(testKey), WithRoundTripper(tr), WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.Models().List(t.Context()); err != nil {
		t.Fatalf("List: %v", err)
	}
	if n := len(closed); n != 0 {
		t.Fatalf("%d connections closed before Close, want 0", n)
	}
	for range 2 {
		if err := c.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	waitClosed(t, closed, "Close of a WithRoundTripper *http.Transport")
	if _, err := c.Models().List(t.Context()); !errors.Is(err, ErrClientClosed) {
		t.Errorf("List after Close = %v, want ErrClientClosed", err)
	}
}

// TestAttemptDeadlineEndsTheCall pins the per-attempt deadline against a
// server that never answers: with WithTimeout, and with a per-call Timeout
// over the default, the call returns within the timeout plus 1 s with an
// error that wraps context.DeadlineExceeded, and not before the timeout
// less one 20 ms clock tick (K29). A client without the deadline fails
// here at once instead of at the test binary's timeout. The error's type
// is W2.5's classification.
func TestAttemptDeadlineEndsTheCall(t *testing.T) {
	const timeout = span // at least 250 ms, less one coarseTick below (K30)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	tests := map[string]struct {
		client []ClientOption
		call   []CallOption
	}{
		"error: the client's timeout": {client: []ClientOption{WithTimeout(timeout)}},
		"error: a per-call timeout":   {call: []CallOption{Timeout(timeout)}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			// One attempt: a retry of the timed-out attempt would outlast
			// the bound below.
			c, err := NewClient(append([]ClientOption{WithAPIKey(testKey), WithBaseURL(srv.URL), WithRetry(NoRetry())}, tt.client...)...)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			t.Cleanup(func() { _ = c.Close() })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			errc := make(chan error, 1)
			start := time.Now()
			go func() {
				_, err := c.Models().List(ctx, tt.call...)
				errc <- err
			}()
			select {
			case err := <-errc:
				elapsed := time.Since(start)
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("List error = %v (%T), want one wrapping context.DeadlineExceeded", err, err)
				}
				if elapsed < timeout-coarseTick {
					t.Errorf("List returned after %v, before its %v deadline", elapsed, timeout)
				}
			case <-time.After(timeout + time.Second):
				cancel()
				t.Fatalf("List did not return within %v of its %v deadline", time.Second, timeout)
			}
		})
	}
}
