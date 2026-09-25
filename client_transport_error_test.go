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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/h2gate"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// The timing rules of the transport tests (K29, K30): every measured span
// is at least 250 ms, a lower bound allows one tick of the coarsest CI clock,
// and every wait has a deadline.
const (
	// span is the length of every deadline and every delay a test measures.
	span = 300 * time.Millisecond
	// coarseTick is one tick of the coarsest CI clock (windows-2025).
	coarseTick = 20 * time.Millisecond
	// callBound is the longest any call of these tests may take.
	callBound = 15 * time.Second
)

// callWithin runs call on its own goroutine and returns how long it took
// and its error, failing the test when it does not return within callBound
// plus 5 s: the call's context carries callBound, so a transport that
// honours it returns first.
func callWithin(t *testing.T, call func(ctx context.Context) error) (time.Duration, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), callBound)
	defer cancel()
	type result struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		err := call(ctx)
		done <- result{err: err, elapsed: time.Since(start)}
	}()
	select {
	case r := <-done:
		return r.elapsed, r.err
	case <-time.After(callBound + 5*time.Second):
		t.Fatalf("the call did not return within %v", callBound+5*time.Second)
		return 0, nil
	}
}

// partialBody is what a mid-body failure sends before it fails: the start of
// a models response.
const partialBody = `{"models":[`

// writePartial writes a 200 response's headers and partialBody, and flushes
// them, so the client has the response and waits for the rest of its body.
func writePartial(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, partialBody)
	w.(http.Flusher).Flush()
}

// TestTransportErrorsBecomeConnectionOrTimeout ports test_transport_errors
// (C13) to failures of a real connection, through the client and the SDK's
// own transport, against the loopback server and its knobs: every failure
// that produced no HTTP response is a *TimeoutError when a deadline passed
// or the network reported a timeout (httpx's ConnectTimeout and ReadTimeout
// rows), and a *ConnectionError otherwise (LocalProtocolError, ConnectError,
// ReadError, RemoteProtocolError; py:_core/transport.py:79-86). A proxy
// that refuses the connection is a proxy *ConnectionError; a proxy that
// answers the CONNECT with 502 reaches the SDK without net/http's
// proxyconnect wrap (K16), so it is an API-hop one. Each makes one attempt,
// and each error is an SDK error.
func TestTransportErrorsBecomeConnectionOrTimeout(t *testing.T) {
	type target struct {
		base string
		opts []ClientOption
		// after checks what the server saw, when set.
		after func(t *testing.T)
	}
	// loopback serves handler over the loopback server, trusted and reached
	// without a proxy; the handler gets the server and a channel closed when
	// the test ends.
	loopback := func(t *testing.T, cfg testsupport.ServerConfig, handler func(srv *testsupport.LoopbackServer, release <-chan struct{}) http.HandlerFunc) (*testsupport.LoopbackServer, target) {
		release := make(chan struct{})
		var srv *testsupport.LoopbackServer
		if handler != nil {
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler(srv, release)(w, r) })
			cfg.Handler = h
		}
		srv = testsupport.NewLoopbackServer(t, cfg)
		// Registered after the server's own Close, so it runs first and no
		// handler holds Close up.
		t.Cleanup(func() { close(release) })
		return srv, target{base: srv.URL(), opts: []ClientOption{WithRootCAs(testsupport.RootCAs(t)), WithProxy(nil)}}
	}
	// servedOnce checks that the server saw the one request once: no replay.
	servedOnce := func(srv *testsupport.LoopbackServer) func(t *testing.T) {
		return func(t *testing.T) {
			if reqs := srv.Requests(); len(reqs) != 1 {
				t.Errorf("the server saw %d requests, want 1 (no replay): %+v", len(reqs), reqs)
			}
		}
	}
	hold := func(r *http.Request, release <-chan struct{}) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}
	tests := map[string]struct {
		target     func(t *testing.T) target
		client     []ClientOption
		call       []CallOption
		minElapsed time.Duration // the call takes at least this, less one tick
		check      func(t *testing.T, err error)
	}{
		"error: a TLS-silent API host is a *TimeoutError (ConnectTimeout)": {
			target: func(t *testing.T) target {
				silent := testsupport.NewSilentListener(t)
				return target{base: silent.URL(), opts: []ClientOption{WithProxy(nil)}}
			},
			client: []ClientOption{WithConnectTimeout(span)}, minElapsed: span,
			check: func(t *testing.T, err error) {
				te, ok := errors.AsType[*TimeoutError](err)
				if !ok || te.Proxy() || te.Timeout != DefaultTimeout || te.Error() != "Request timed out (timeout=10s)." {
					t.Fatalf("error = %T %v, want an API-hop *TimeoutError naming the attempt's timeout", err, err)
				}
				if _, ok := errors.AsType[*h2gate.DialError](err); !ok {
					t.Errorf("error %v does not unwrap to the transport's DialError", err)
				}
			},
		},
		"error: a refused connection is a *ConnectionError (ConnectError)": {
			target: func(t *testing.T) target {
				return target{base: "https://" + closedAddr(t), opts: []ClientOption{WithProxy(nil)}}
			},
			check: func(t *testing.T, err error) {
				ce, ok := errors.AsType[*ConnectionError](err)
				if !ok || ce.Proxy() || !strings.HasPrefix(ce.Error(), "Connection error: dial tcp ") {
					t.Errorf("error = %T %v, want an API-hop *ConnectionError from the dial", err, err)
				}
			},
		},
		"error: a connection reset mid-body is a *ConnectionError (ReadError)": {
			target: func(t *testing.T) target {
				srv, tg := loopback(t, testsupport.ServerConfig{}, func(srv *testsupport.LoopbackServer, release <-chan struct{}) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						writePartial(w)
						srv.LiveH2Conns()[0].Reset()
						hold(r, release)
					}
				})
				tg.after = servedOnce(srv)
				return tg
			},
			check: func(t *testing.T, err error) {
				ce, ok := errors.AsType[*ConnectionError](err)
				_, dial := errors.AsType[*h2gate.DialError](err)
				if !ok || ce.Proxy() || dial {
					t.Errorf("error = %T %v, want a *ConnectionError after the connection was had", err, err)
				}
			},
		},
		"error: a clean close mid-body is a *ConnectionError wrapping io.ErrUnexpectedEOF (RemoteProtocolError)": {
			target: func(t *testing.T) target {
				srv, tg := loopback(t, testsupport.ServerConfig{}, func(srv *testsupport.LoopbackServer, release <-chan struct{}) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						writePartial(w)
						srv.CloseConns()
						hold(r, release)
					}
				})
				tg.after = servedOnce(srv)
				return tg
			},
			check: func(t *testing.T, err error) {
				ce, ok := errors.AsType[*ConnectionError](err)
				if !ok || ce.Proxy() || !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Errorf("error = %T %v, want a *ConnectionError wrapping io.ErrUnexpectedEOF", err, err)
				}
			},
		},
		"error: GOAWAY after the request was written is a *ConnectionError": {
			target: func(t *testing.T) target {
				srv, tg := loopback(t, testsupport.ServerConfig{}, func(srv *testsupport.LoopbackServer, release <-chan struct{}) http.HandlerFunc {
					return func(_ http.ResponseWriter, r *http.Request) {
						_, _ = io.Copy(io.Discard, r.Body)
						conn := srv.LiveH2Conns()[0]
						// The request's stream is at or below LastStreamID, so it
						// counts as processed and net/http cannot replay it.
						_ = conn.GoAway(conn.ActiveStreams()[0], testsupport.CodeInternalError)
						srv.CloseConns()
						hold(r, release)
					}
				})
				tg.after = servedOnce(srv)
				return tg
			},
			check: func(t *testing.T, err error) {
				ce, ok := errors.AsType[*ConnectionError](err)
				if !ok || ce.Proxy() || !strings.Contains(ce.Error(), "GOAWAY") {
					t.Errorf("error = %T %v, want a *ConnectionError naming the GOAWAY", err, err)
				}
			},
		},
		"error: a refused proxy is a proxy *ConnectionError": {
			target: func(t *testing.T) target {
				proxy := &url.URL{Scheme: "http", Host: closedAddr(t)}
				return target{base: "https://example.com", opts: []ClientOption{WithProxy(http.ProxyURL(proxy))}}
			},
			check: func(t *testing.T, err error) {
				ce, ok := errors.AsType[*ConnectionError](err)
				if !ok || !ce.Proxy() || !strings.HasPrefix(ce.Error(), "Connection error: proxyconnect tcp: ") {
					t.Errorf("error = %T %v, want a proxy *ConnectionError", err, err)
				}
			},
		},
		"error: a proxy that answers the CONNECT with 502 is a *ConnectionError (K16)": {
			target: func(t *testing.T) target {
				proxy := testsupport.NewProxy(t, testsupport.ProxyPlain, nil) // no route: 502
				return target{
					base: "https://example.com", opts: []ClientOption{WithProxy(http.ProxyURL(proxy.URL()))},
					after: func(t *testing.T) {
						if c := proxy.Connects(); len(c) != 1 || c[0].Status != http.StatusBadGateway {
							t.Errorf("proxy CONNECTs %+v, want one answered 502", c)
						}
					},
				}
			},
			check: func(t *testing.T, err error) {
				ce, ok := errors.AsType[*ConnectionError](err)
				if !ok || ce.Proxy() || ce.Error() != "Connection error: Bad Gateway" {
					t.Errorf("error = %T %v, want the *ConnectionError of an unwrapped CONNECT refusal", err, err)
				}
			},
		},
		"error: the attempt's deadline before the response is a *TimeoutError (ReadTimeout)": {
			target: func(t *testing.T) target {
				srv, tg := loopback(t, testsupport.ServerConfig{OnStream: func(*testsupport.Stream) testsupport.Action { return testsupport.ActionHold }}, nil)
				tg.after = func(t *testing.T) {
					waitFor(t, 5*time.Second, "the server sees the held stream reset", func() bool {
						reqs := srv.Requests()
						return len(reqs) == 1 && reqs[0].Dropped
					})
				}
				return tg
			},
			call: []CallOption{Timeout(span)}, minElapsed: span,
			check: func(t *testing.T, err error) {
				te, ok := errors.AsType[*TimeoutError](err)
				if !ok || te.Proxy() || te.Timeout != span || !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("error = %T %v, want a *TimeoutError of %v wrapping context.DeadlineExceeded", err, err, span)
				}
			},
		},
		"error: the attempt's deadline while the body is read is a *TimeoutError (ReadTimeout)": {
			target: func(t *testing.T) target {
				srv, tg := loopback(t, testsupport.ServerConfig{}, func(_ *testsupport.LoopbackServer, release <-chan struct{}) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						writePartial(w)
						hold(r, release)
					}
				})
				tg.after = servedOnce(srv)
				return tg
			},
			call: []CallOption{Timeout(span)}, minElapsed: span,
			check: func(t *testing.T, err error) {
				te, ok := errors.AsType[*TimeoutError](err)
				if !ok || te.Timeout != span || !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("error = %T %v, want a *TimeoutError of %v wrapping context.DeadlineExceeded", err, err, span)
				}
			},
		},
		"success: WithNoTimeout outlasts a slow server": {
			target: func(t *testing.T) target {
				_, tg := loopback(t, testsupport.ServerConfig{}, func(_ *testsupport.LoopbackServer, release <-chan struct{}) http.HandlerFunc {
					return func(w http.ResponseWriter, _ *http.Request) {
						select {
						case <-time.After(span):
						case <-release:
							return
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"models":[]}`)
					}
				})
				return tg
			},
			client: []ClientOption{WithNoTimeout()}, minElapsed: span,
			check: func(t *testing.T, err error) {
				if err != nil {
					t.Errorf("List error = %T %v, want success", err, err)
				}
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tg := tt.target(t)
			clearEnv(t)
			c, err := NewClient(append(append([]ClientOption{WithAPIKey(testKey), WithBaseURL(tg.base)}, tg.opts...), tt.client...)...)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			t.Cleanup(func() { _ = c.Close() })
			elapsed, err := callWithin(t, func(ctx context.Context) error {
				_, err := c.Models().List(ctx, tt.call...)
				return err
			})
			tt.check(t, err)
			if _, ok := errors.AsType[Error](err); err != nil && !ok {
				t.Errorf("error = %T %v, not an SDK error", err, err)
			}
			if tt.minElapsed > 0 && elapsed < tt.minElapsed-coarseTick {
				t.Errorf("the call took %v, less than %v", elapsed, tt.minElapsed)
			}
			if n := c.Stats().Attempts; n != 1 {
				t.Errorf("Stats().Attempts = %d, want 1", n)
			}
			if tg.after != nil {
				tg.after(t)
			}
		})
	}
}

// echoTimeout is a network timeout whose text a transport wrote.
type echoTimeout struct{ msg string }

func (e echoTimeout) Error() string { return e.msg }
func (echoTimeout) Timeout() bool   { return true }
func (echoTimeout) Temporary() bool { return true }

// failingBody is a response body that sends partialBody, then fails with
// err.
func failingBody(err error) io.ReadCloser {
	return io.NopCloser(io.MultiReader(strings.NewReader(partialBody), readerFunc(func([]byte) (int, error) { return 0, err })))
}

// readerFunc is an io.Reader made of a func.
type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// TestTransportErrorsHoldNoCredential checks AC-F5 for transport errors, as
// test_transport_errors_do_not_expose_credentials does
// (tests/test_logging.py:71-130): a transport that repeats the request's
// credentials in its error's text, raw and quoted (%q), and in the text of
// an error it wraps, never shows one in the SDK error, in any printed form
// of anything it unwraps to, or in any log record down to LevelTrace; the
// text around them stays ("Illegal header value", a header whose name marks
// no credential), and "***" stands for them.
//
// The grid is AC-F5's nine header spellings (tests/test_logging.py:14-27),
// each set with WithHeader to "request-credential", times the upstream
// keys ("auth-credential" and one with quotes and a backslash), times five
// places a transport error can arise: the round trip, a round trip that
// timed out (a *TimeoutError), and the body read of a response with each of
// AC-F5's three statuses, 200, 400 and 429 (a response whose body cannot be
// read is a *ConnectionError whatever its status). A caller's Authorization
// is dropped for the SDK's own (W2.1), which carries the key.
func TestTransportErrorsHoldNoCredential(t *testing.T) {
	type test struct {
		header, key, failure string
	}
	failures := []string{"round trip", "round trip timeout", "body 200", "body 400", "body 429"}
	tests := map[string]test{}
	for _, header := range []string{"Authorization", "Proxy-Authorization", "X-API-Key", "API-Key", "Cookie", "Set-Cookie", "X-Access-Token", "X-Client-Secret", "x-MiXeD-ToKeN"} {
		for keyName, key := range map[string]string{"plain key": "auth-credential", "quirky key": quirkyKey} {
			for _, failure := range failures {
				tests["error: "+header+"/"+keyName+"/"+failure] = test{header: header, key: key, failure: failure}
			}
		}
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				// The error repeats the Authorization value quoted, the visible
				// header, every header raw (the map's %v, cut by the SDK's 200
				// characters), and wraps an error that repeats the key and the
				// spelling's value but whose text the outer one does not print.
				inner := opaqueError{inner: errors.New("Rejected authorization: " + tt.key + "; provider: " + req.Header.Get(tt.header))}
				failure := fmt.Errorf("Illegal header value %q; visible %s; headers %v: %w", req.Header.Get("Authorization"), req.Header.Get("X-Visible"), req.Header, inner)
				switch tt.failure {
				case "round trip":
					return nil, failure
				case "round trip timeout":
					return nil, fmt.Errorf("%w: %w", echoTimeout{msg: "timed out sending " + req.Header.Get(tt.header)}, failure)
				}
				status, _ := strconv.Atoi(strings.TrimPrefix(tt.failure, "body "))
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: -1, Request: req, Body: failingBody(failure)}, nil
			})
			logs := testsupport.NewLogRecorder(LevelTrace)
			clearEnv(t)
			c := newEnvClient(t, rt, WithAPIKey(tt.key), WithHeader(tt.header, "request-credential"), WithHeader("X-Visible", "request-visible"), WithLogger(logs.Logger()))
			_, err := c.Models().List(t.Context())
			if tt.failure == "round trip timeout" {
				if _, ok := errors.AsType[*TimeoutError](err); !ok {
					t.Fatalf("List error = %T %v, want a *TimeoutError", err, err)
				}
			} else {
				ce, ok := errors.AsType[*ConnectionError](err)
				if !ok {
					t.Fatalf("List error = %T %v, want a *ConnectionError", err, err)
				}
				for _, visible := range []string{"Illegal header value", "request-visible", redacted} {
					if !strings.Contains(ce.Error(), visible) {
						t.Errorf("Error() = %q lacks %q", ce.Error(), visible)
					}
				}
			}
			if _, standIn := errors.AsType[*scrubbedError](err); !standIn {
				t.Errorf("error %v does not unwrap to a stand-in for the transport's error", err)
			}
			if _, ok := errors.AsType[opaqueError](err); ok {
				t.Error("errors.As reaches the transport's wrapped error, whose text holds the key")
			}
			secrets := []string{tt.key, quotedForm(strconv.Quote(tt.key)), "request-credential"}
			if tt.header == "Authorization" {
				secrets = secrets[:2] // dropped for the SDK's own, so never sent
			}
			records := recordsText(logs)
			if !strings.Contains(records, "request failed") {
				t.Errorf("no record of the failure:\n%s", records)
			}
			for _, secret := range secrets {
				assertNotPrinted(t, err, secret)
				if strings.Contains(records, secret) {
					t.Errorf("the records hold %q:\n%s", secret, records)
				}
			}
		})
	}
}

// TestTransportErrorsHoldNoCredentialOverTheNetwork checks the scrub where a
// transport error's text is written below the SDK's own transport: a
// caller's dialer (WithHTTPTransport) whose error, or timeout, repeats the
// API key it was handed, and a proxy URL with a password, which net/http's
// proxyconnect error does not print. The key is replaced by "***" in the
// SDK error, and the error unwraps to a stand-in, through which errors.As
// cannot reach h2gate's DialError or the dialer's error.
func TestTransportErrorsHoldNoCredentialOverTheNetwork(t *testing.T) {
	const key = "ts_live_0123456789abcdef"
	dialing := func(err func(addr string) error) ClientOption {
		return WithHTTPTransport(&http.Transport{DialContext: func(_ context.Context, _, addr string) (net.Conn, error) { return nil, err(addr) }})
	}
	tests := map[string]struct {
		opts  []ClientOption
		check func(t *testing.T, err error)
	}{
		"error: a dialer's error that prints the key": {
			opts: []ClientOption{WithBaseURL("https://example.com"), dialing(func(addr string) error { return errors.New("dial " + addr + " refused for key " + key) })},
			check: func(t *testing.T, err error) {
				ce, ok := errors.AsType[*ConnectionError](err)
				if !ok || ce.Proxy() || ce.Error() != "Connection error: dial example.com:443 refused for key ***" {
					t.Errorf("error = %T %v, want a *ConnectionError with the key replaced", err, err)
				}
			},
		},
		"error: a dialer's timeout that prints the key": {
			opts: []ClientOption{WithBaseURL("https://example.com"), dialing(func(addr string) error { return echoTimeout{msg: "dial " + addr + " timed out for Bearer " + key} })},
			check: func(t *testing.T, err error) {
				te, ok := errors.AsType[*TimeoutError](err)
				if !ok || te.Proxy() || te.Error() != "Request timed out (timeout=10s)." {
					t.Errorf("error = %T %v, want an API-hop *TimeoutError", err, err)
				}
				if u := errors.Unwrap(err); u == nil || !strings.HasSuffix(u.Error(), ": dial example.com:443 timed out for ***") {
					t.Errorf("the timeout's cause = %v, want the dialer's text with the Authorization value replaced", u)
				}
			},
		},
		"error: a refused proxy whose URL holds a password": {
			opts: []ClientOption{WithBaseURL("https://example.com"), WithProxy(http.ProxyURL(&url.URL{Scheme: "http", User: url.UserPassword("user", "hunter2"), Host: closedAddr(t)}))},
			check: func(t *testing.T, err error) {
				ce, ok := errors.AsType[*ConnectionError](err)
				if !ok || !ce.Proxy() {
					t.Errorf("error = %T %v, want a proxy *ConnectionError", err, err)
				}
				assertNotPrinted(t, err, "hunter2")
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			logs := testsupport.NewLogRecorder(slog.LevelInfo)
			clearEnv(t)
			c, err := NewClient(append([]ClientOption{WithAPIKey(key), WithLogger(logs.Logger())}, tt.opts...)...)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			t.Cleanup(func() { _ = c.Close() })
			_, err = callWithin(t, func(ctx context.Context) error {
				_, err := c.Models().List(ctx)
				return err
			})
			tt.check(t, err)
			assertNotPrinted(t, err, key)
			if strings.Contains(recordsText(logs), key) {
				t.Errorf("the records hold the key:\n%s", recordsText(logs))
			}
			if _, ok := errors.AsType[*h2gate.DialError](err); ok && strings.Contains(fmt.Sprint(errors.Unwrap(err)), key) {
				t.Error("errors.As reaches a DialError whose text holds the key")
			}
		})
	}
}

// TestCallerTransportOwnsItsTimeouts pins the deviation "a custom transport
// owns its timeouts" (F11, test_http_client_timeout_precedence): where
// typesafe-sdk-python replaces an httpx client's timeout with its own, a Go
// caller's transport keeps every timeout it sets, here ResponseHeaderTimeout,
// under the SDK's per-attempt deadline, which still applies. The caller's
// timer ends the attempt long before the SDK's 10 s deadline, and the call
// is a *TimeoutError naming the attempt's timeout, since net/http's error is
// a network timeout (a net.Error whose Timeout is true, over HTTP/2 and
// HTTP/1.1 alike).
func TestCallerTransportOwnsItsTimeouts(t *testing.T) {
	tests := map[string]struct {
		option func(t *testing.T) ClientOption
	}{
		"error: WithHTTPTransport": {option: func(t *testing.T) ClientOption {
			return WithHTTPTransport(&http.Transport{ResponseHeaderTimeout: span, TLSClientConfig: testsupport.ClientTLSConfig(t)})
		}},
		"error: WithRoundTripper": {option: func(t *testing.T) ClientOption {
			tr := &http.Transport{ResponseHeaderTimeout: span, TLSClientConfig: testsupport.ClientTLSConfig(t)}
			t.Cleanup(tr.CloseIdleConnections)
			return WithRoundTripper(tr)
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-release:
				}
			})})
			t.Cleanup(func() { close(release) })
			clearEnv(t)
			c, err := NewClient(WithAPIKey(testKey), WithBaseURL(srv.URL()), tt.option(t))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			t.Cleanup(func() { _ = c.Close() })
			elapsed, err := callWithin(t, func(ctx context.Context) error {
				_, err := c.Models().List(ctx)
				return err
			})
			te, ok := errors.AsType[*TimeoutError](err)
			ne, isNet := errors.AsType[net.Error](err)
			if !ok || te.Timeout != DefaultTimeout || !isNet || !ne.Timeout() {
				t.Errorf("List error = %T %v, want a *TimeoutError of the attempt's %v wrapping the transport's network timeout", err, err, DefaultTimeout)
			}
			if elapsed < span-coarseTick || elapsed >= DefaultTimeout/2 {
				t.Errorf("the call took %v, want the caller's %v timer to end it, not the SDK's %v", elapsed, span, DefaultTimeout)
			}
		})
	}
}

// valueErr is a caller's error held by value, whose text is clean and whose
// field holds the request's header: %#v shows the field.
type valueErr struct{ h http.Header }

func (valueErr) Error() string { return "boom" }

// formatterErr is a caller's error whose text and %#v are clean and whose
// %+v prints the request's header, as an fmt.Formatter may.
type formatterErr struct{ h http.Header }

func (formatterErr) Error() string { return "boom" }

func (e formatterErr) Format(f fmt.State, verb rune) {
	if verb == 'v' && f.Flag('+') {
		fmt.Fprintf(f, "boom: %v", e.h)
		return
	}
	_, _ = io.WriteString(f, "boom")
}

// reqErr is a caller's error that points to the request.
type reqErr struct{ req *http.Request }

func (*reqErr) Error() string { return "boom" }

// TestTransportErrorFieldsHoldNoCredential pins the review's MINOR 2
// (ruling R82 (b)) through the client: a transport error whose text is
// clean but whose %#v (an error held by value) or %+v (an fmt.Formatter)
// shows the request's header is replaced by a stand-in, so no printed form
// of the SDK error or of anything it unwraps to holds the key; an error
// that points to the request is kept, prints its pointer as an address, and
// errors.As reaches the caller's own request through it, which the
// ConnectionError godoc states.
func TestTransportErrorFieldsHoldNoCredential(t *testing.T) {
	const key = `ts_live_q"uo\te%41abcdef`
	tests := map[string]struct {
		fail   func(req *http.Request) error
		text   string // the transport error's text; "boom" when empty
		keptAs bool   // the error is kept, and errors.As reaches the request
	}{
		"error: a value-type error holding the header": {fail: func(req *http.Request) error { return valueErr{h: req.Header.Clone()} }},
		"error: a Formatter that prints the header under %+v": {
			fail: func(req *http.Request) error { return formatterErr{h: req.Header.Clone()} },
		},
		"error: an error wrapping one that holds the header by value": {
			fail: func(req *http.Request) error { return fmt.Errorf("round trip: %w", valueErr{h: req.Header.Clone()}) },
			text: "round trip: boom",
		},
		"error: an error pointing to the request is the caller's own": {
			fail: func(req *http.Request) error { return &reqErr{req: req} }, keptAs: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rt := roundTripFunc(func(req *http.Request) (*http.Response, error) { return nil, tt.fail(req) })
			clearEnv(t)
			c := newEnvClient(t, rt, WithAPIKey(key))
			_, err := c.Models().List(t.Context())
			text := cmp.Or(tt.text, "boom")
			ce, ok := errors.AsType[*ConnectionError](err)
			if !ok || ce.Error() != "Connection error: "+text {
				t.Fatalf("List error = %T %v, want the *ConnectionError of the transport's text", err, err)
			}
			// Every printed form of err and of each error it unwraps to: no
			// form of the key, raw or quoted, may appear, so the check is on
			// its prefix.
			assertNotPrinted(t, err, "ts_live_q")
			_, standIn := ce.Unwrap().(*scrubbedError) //nolint:errorlint // the direct cause is the stand-in
			re, reached := errors.AsType[*reqErr](err)
			if tt.keptAs {
				if standIn || !reached || !strings.Contains(re.req.Header.Get("Authorization"), key) {
					t.Errorf("the cause = %T, errors.As reached the request %t; want the caller's error kept and its request reachable", ce.Unwrap(), reached)
				}
				return
			}
			if !standIn {
				t.Errorf("the cause = %T, want a *scrubbedError stand-in", ce.Unwrap())
			}
		})
	}
}

// TestTransportErrorTextScrubbedBeforeCut pins the order of the scrub and
// the render (review W2.5 MINOR 1): a credential is replaced in the
// transport error's whole text before safeMessage escapes it and cuts it at
// 200 characters, so a key that straddles the cut leaves "***" and not its
// first bytes. The key sits after pads of 176 to 195 characters, across the
// boundary, in the text of a caller RoundTripper's error (attemptError) and
// of an h2gate DialError (transportError); no 8-byte piece of the key may
// survive in Error().
func TestTransportErrorTextScrubbedBeforeCut(t *testing.T) {
	const key = "ts_live_0123456789abcdef"
	header := http.Header{"Authorization": {"Bearer " + key}}
	paths := map[string]func(t *testing.T, text string) error{
		"attemptError": func(t *testing.T, text string) error {
			clearEnv(t)
			c := newEnvClient(t, &testsupport.Recorder{Replies: []testsupport.Reply{{Err: errString(text)}}}, WithAPIKey(key))
			_, err := c.Models().List(t.Context())
			return err
		},
		"transportError": func(_ *testing.T, text string) error {
			return transportError(&h2gate.DialError{Err: errors.New(text)}, time.Second, header)
		},
	}
	type test struct {
		path func(t *testing.T, text string) error
		pad  int
	}
	tests := map[string]test{}
	for name, path := range paths {
		for pad := 176; pad <= 195; pad++ {
			tests["error: "+name+"/pad "+strconv.Itoa(pad)] = test{path: path, pad: pad}
		}
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := tt.path(t, strings.Repeat("a", tt.pad)+key+" tail")
			ce, ok := errors.AsType[*ConnectionError](err)
			if !ok {
				t.Fatalf("error = %T %v, want a *ConnectionError", err, err)
			}
			got := ce.Error()
			if !strings.Contains(got, strings.Repeat("a", tt.pad)+redacted) {
				t.Errorf("Error() = %q, want the pad then %q", got, redacted)
			}
			for i := 0; i+minKeyNeedleBytes <= len(key); i++ {
				if piece := key[i : i+minKeyNeedleBytes]; strings.Contains(got, piece) {
					t.Errorf("Error() = %q holds %q, %d bytes of the key", got, piece, minKeyNeedleBytes)
				}
			}
		})
	}
}
