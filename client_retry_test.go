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
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// fastRetry is the policy of the tests over a real network: the production
// rules with a 1 ms backoff, so the waits cost no measurable time.
func fastRetry() RetryPolicy { return DefaultRetry().Backoff(time.Millisecond, time.Millisecond, 0) }

// serverCounts returns the X-TypeSafe-Retry-Count of each request the
// loopback server saw, absent where it had none.
func serverCounts(srv *testsupport.LoopbackServer) []string {
	var out []string
	for _, r := range srv.Requests() {
		v := absent
		if vs := r.Header.Values(headerRetryCount); len(vs) > 0 {
			v = strings.Join(vs, ",")
		}
		out = append(out, v)
	}
	return out
}

// TestConnectionErrorsRetried ports test_connection_retry_recovers (RT9):
// an attempt that fails without a response, as a *ConnectionError or a
// *TimeoutError, is retried under the production policy, and the third
// attempt recovers. Through the Recorder, inside a bubble, the four upstream
// kinds (ConnectError, ReadTimeout, ReadError, LocalProtocolError) and the
// two backoff waits, 375 ms to 500 ms and then 750 ms to 1 s. Through the
// SDK's own transport and the loopback server, a refused connection, a
// connection reset mid-body and the attempt's deadline, each twice before
// the server answers; and a GOAWAY after the request was written, which
// net/http cannot replay: the *ConnectionError it makes, which names the
// GOAWAY, is retried by the policy with X-TypeSafe-Retry-Count: 1 (S-T4's
// "failure after write" clause, deferred from W2.2), and each connection the
// server closed that way closed in the order the K33 rows of
// TestTransportErrorsBecomeConnectionOrTimeout pin.
func TestConnectionErrorsRetried(t *testing.T) {
	t.Run("recorder", func(t *testing.T) {
		tests := map[string]struct {
			fail  testsupport.Reply
			class func(error) bool
		}{
			"success: ConnectError, a refused dial": {
				fail:  testsupport.Reply{Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
				class: func(err error) bool { _, ok := errors.AsType[*ConnectionError](err); return ok },
			},
			"success: ReadTimeout, a network timeout": {
				fail:  testsupport.Reply{Err: netTimeout{}},
				class: func(err error) bool { _, ok := errors.AsType[*TimeoutError](err); return ok },
			},
			"success: ReadError, a body cut short": {
				fail:  testsupport.Reply{Status: 200, Body: []byte(`{"models":[`), ContentLength: 64},
				class: func(err error) bool { _, ok := errors.AsType[*ConnectionError](err); return ok },
			},
			"success: LocalProtocolError, a protocol failure": {
				fail:  testsupport.Reply{Err: errors.New("http2: invalid header field value")},
				class: func(err error) bool { _, ok := errors.AsType[*ConnectionError](err); return ok },
			},
		}
		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					log := &attemptLog{rec: &testsupport.Recorder{Replies: []testsupport.Reply{tt.fail, tt.fail, rtReply(200, `{"models":[]}`)}}}
					c := newTestClient(t, log, WithRetry(DefaultRetry()))
					// The kind is classified as upstream's is: a no-retry call
					// returns it.
					one := newTestClient(t, &testsupport.Recorder{Replies: []testsupport.Reply{tt.fail}})
					if err := listCall(bubbleCtx(t), one); !tt.class(err) {
						t.Fatalf("one attempt: error = %T %v, not of the kind", err, err)
					}
					resp, err := c.Models().List(bubbleCtx(t))
					if err != nil || len(resp.Models()) != 0 {
						t.Fatalf("List = %v, %v; want no models", resp, err)
					}
					if n := c.Stats().Attempts; n != 3 {
						t.Errorf("Stats().Attempts = %d, want 3", n)
					}
					w := waits(log.attempts(), 0)
					if len(w) != 2 || w[0] < 375*time.Millisecond || w[0] > 500*time.Millisecond || w[1] < 750*time.Millisecond || w[1] > time.Second ||
						w[0]%time.Millisecond != 0 || w[1]%time.Millisecond != 0 {
						t.Errorf("waits = %v, want 375ms..500ms and 750ms..1s, in whole milliseconds", w)
					}
					if diff := gocmp.Diff(wantCounts(3), retryCounts(log.rec.Requests())); diff != "" {
						t.Errorf("X-TypeSafe-Retry-Count (-want +got):\n%s", diff)
					}
				})
			})
		}
	})

	t.Run("loopback", func(t *testing.T) {
		const models = `{"models":[]}`
		// hold keeps a handler until the client drops its request or the
		// test ends.
		hold := func(r *http.Request, release <-chan struct{}) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}
		// serveModels answers the models list.
		serveModels := func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, models)
		}
		type target struct {
			srv  *testsupport.LoopbackServer
			opts []ClientOption
		}
		// loopback starts the server with handler, which gets the server, the
		// request's position (from 0) and a channel closed when the test
		// ends; the client trusts it and reaches it without a proxy.
		loopback := func(t *testing.T, cfg testsupport.ServerConfig, handler func(srv *testsupport.LoopbackServer, seq int, release <-chan struct{}) http.HandlerFunc) target {
			release := make(chan struct{})
			var srv *testsupport.LoopbackServer
			var seq atomic.Int64
			if handler != nil {
				cfg.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					handler(srv, int(seq.Add(1)-1), release)(w, r)
				})
			}
			srv = testsupport.NewLoopbackServer(t, cfg)
			t.Cleanup(func() { close(release) })
			return target{srv: srv, opts: []ClientOption{WithRootCAs(testsupport.RootCAs(t)), WithProxy(nil)}}
		}
		goAwayAfterWrite := func(srv *testsupport.LoopbackServer, r *http.Request, release <-chan struct{}) {
			_, _ = io.Copy(io.Discard, r.Body)
			conn := srv.LiveH2Conns()[0]
			// The request's stream is at or below LastStreamID, so it counts
			// as processed and net/http cannot replay it.
			_ = conn.GoAway(conn.ActiveStreams()[0], testsupport.CodeInternalError)
			srv.CloseConns()
			hold(r, release)
		}
		// closedAfterGoAway checks the close records of the first n
		// connections, each ended by goAwayAfterWrite, by the server's
		// sequence numbers (K29), in the terms of internal/h2gate's
		// closedGracefully (rulings K33, K34, R100): the GOAWAY frame, then
		// the server's close_notify and FIN, then the reader's read of the
		// end of the client's side, then the socket's close (0 < GoAwaySeq <
		// CloseWriteSeq < PeerClosedSeq < ClosedSeq). The server necessarily
		// closes first on this path, so client-first is not allowed: the
		// GOAWAY's LastStreamID is the held stream, which net/http leaves
		// open, and a connection with an open stream is never closed as
		// idle, so the client waits for the response until CloseConns ends
		// the call in flight, as in the K33 row of
		// TestTransportErrorsBecomeConnectionOrTimeout.
		closedAfterGoAway := func(t *testing.T, srv *testsupport.LoopbackServer, n int) {
			t.Helper()
			var conns []testsupport.ConnInfo
			waitFor(t, 5*time.Second, "the server to close the connections", func() bool {
				conns = srv.Conns()
				if len(conns) < n {
					return false
				}
				for _, ci := range conns[:n] {
					if ci.ClosedSeq == 0 {
						return false
					}
				}
				return true
			})
			for _, ci := range conns[:n] {
				ordered := 0 < ci.GoAwaySeq && ci.GoAwaySeq < ci.CloseWriteSeq && ci.CloseWriteSeq < ci.PeerClosedSeq && ci.PeerClosedSeq < ci.ClosedSeq
				if !ordered {
					t.Errorf("connection %d close records %+v, want GOAWAY < close_notify and FIN < the client's close < the socket's close", ci.Index, ci)
				}
				t.Logf("connection %d close records %+v", ci.Index, ci)
			}
		}
		tests := map[string]struct {
			target   func(t *testing.T) target
			policy   RetryPolicy
			call     []CallOption
			attempts int
			// goAways is the number of connections the server closes after
			// a GOAWAY.
			goAways int
			check   func(t *testing.T, err error)
		}{
			"success: a refused connection twice, then served (ConnectError)": {
				target: func(t *testing.T) target {
					tg := loopback(t, testsupport.ServerConfig{}, func(*testsupport.LoopbackServer, int, <-chan struct{}) http.HandlerFunc {
						return func(w http.ResponseWriter, _ *http.Request) { serveModels(w) }
					})
					refused := closedAddr(t)
					var dials atomic.Int32
					dialer := &net.Dialer{Timeout: 5 * time.Second}
					tr := &http.Transport{
						TLSClientConfig: testsupport.ClientTLSConfig(t),
						DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
							if dials.Add(1) <= 2 {
								addr = refused
							}
							return dialer.DialContext(ctx, network, addr)
						},
					}
					t.Cleanup(tr.CloseIdleConnections)
					tg.opts = []ClientOption{WithHTTPTransport(tr)}
					return tg
				},
				policy: fastRetry(), attempts: 3,
			},
			"success: a connection reset mid-body twice, then served (ReadError)": {
				target: func(t *testing.T) target {
					return loopback(t, testsupport.ServerConfig{}, func(srv *testsupport.LoopbackServer, seq int, release <-chan struct{}) http.HandlerFunc {
						return func(w http.ResponseWriter, r *http.Request) {
							if seq >= 2 {
								serveModels(w)
								return
							}
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusOK)
							_, _ = io.WriteString(w, `{"models":[`)
							w.(http.Flusher).Flush()
							srv.LiveH2Conns()[0].Reset()
							hold(r, release)
						}
					})
				},
				policy: fastRetry(), attempts: 3,
			},
			"success: the attempt's deadline twice, then served (ReadTimeout)": {
				target: func(t *testing.T) target {
					return loopback(t, testsupport.ServerConfig{OnStream: func(s *testsupport.Stream) testsupport.Action {
						if s.Seq < 2 {
							return testsupport.ActionHold
						}
						return testsupport.ActionServe
					}}, func(*testsupport.LoopbackServer, int, <-chan struct{}) http.HandlerFunc {
						return func(w http.ResponseWriter, _ *http.Request) { serveModels(w) }
					})
				},
				policy: fastRetry(), call: []CallOption{Timeout(span)}, attempts: 3,
			},
			"success: GOAWAY after the request was written, then served (S-T4)": {
				target: func(t *testing.T) target {
					return loopback(t, testsupport.ServerConfig{}, func(srv *testsupport.LoopbackServer, seq int, release <-chan struct{}) http.HandlerFunc {
						return func(w http.ResponseWriter, r *http.Request) {
							if seq >= 1 {
								serveModels(w)
								return
							}
							goAwayAfterWrite(srv, r, release)
						}
					})
				},
				policy: fastRetry(), attempts: 2, goAways: 1,
			},
			"error: GOAWAY after the request was written every time is the last *ConnectionError": {
				target: func(t *testing.T) target {
					return loopback(t, testsupport.ServerConfig{}, func(srv *testsupport.LoopbackServer, _ int, release <-chan struct{}) http.HandlerFunc {
						return func(_ http.ResponseWriter, r *http.Request) { goAwayAfterWrite(srv, r, release) }
					})
				},
				policy: fastRetry().MaxRetries(1), attempts: 2, goAways: 2,
				check: func(t *testing.T, err error) {
					ce, ok := errors.AsType[*ConnectionError](err)
					if !ok || ce.Proxy() || !strings.Contains(ce.Error(), "GOAWAY") {
						t.Errorf("error = %T %v, want an API-hop *ConnectionError naming the GOAWAY", err, err)
					}
				},
			},
		}
		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				tg := tt.target(t)
				clearEnv(t)
				c, err := NewClient(append([]ClientOption{WithAPIKey(testKey), WithBaseURL(tg.srv.URL()), WithRetry(tt.policy)}, tg.opts...)...)
				if err != nil {
					t.Fatalf("NewClient: %v", err)
				}
				t.Cleanup(func() { _ = c.Close() })
				_, err = callWithinBound(t, func(ctx context.Context) error { return listCall(ctx, c, tt.call...) })
				if tt.check != nil {
					tt.check(t, err)
				} else if err != nil {
					t.Fatalf("List: %T %v, want the server's answer", err, err)
				}
				assertAttempts(t, c, tt.attempts)
				// A refused dial never reaches the server: it sees the last
				// attempt alone.
				want := wantCounts(tt.attempts)
				if got := serverCounts(tg.srv); len(got) < len(want) {
					want = want[len(want)-len(got):]
				}
				if diff := gocmp.Diff(want, serverCounts(tg.srv)); diff != "" {
					t.Errorf("X-TypeSafe-Retry-Count the server saw (-want +got):\n%s", diff)
				}
				if tt.goAways > 0 {
					closedAfterGoAway(t, tg.srv, tt.goAways)
				}
			})
		}
	})
}

// callWithinBound runs call on its own goroutine under a context that ends
// after 15 s and returns how long it took and its error, failing the test
// when it has not returned 5 s after that (K29, K30).
func callWithinBound(t *testing.T, call func(ctx context.Context) error) (time.Duration, error) {
	t.Helper()
	const bound = 15 * time.Second
	ctx, cancel := context.WithTimeout(t.Context(), bound)
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
	case <-time.After(bound + 5*time.Second):
		t.Fatalf("the call did not return within %v", bound+5*time.Second)
		return 0, nil
	}
}

// uploadCall is one System One call of TestEarlyAnswerToLargeUpload: its
// name (the X-Call header), its state and the body it sends.
type uploadCall struct {
	name  string
	state string
	body  []byte
}

// uploadCalls returns n calls whose states are 6 MiB of one letter each, a
// different letter per call, so a body overwritten by another call's shows
// in its digest.
func uploadCalls(n int) []uploadCall {
	calls := make([]uploadCall, n)
	for i := range calls {
		state := strings.Repeat(string(rune('a'+i)), 6<<20)
		calls[i] = uploadCall{
			name:  "upload-" + strconv.Itoa(i),
			state: state,
			body:  []byte(`{"state":"` + state + `","model":"` + DefaultModel + `",` + noulBody + `}`),
		}
	}
	return calls
}

// sumPerAttempt is a RoundTripper that hashes a fresh GetBody read of every
// attempt (PM4) before rt sends it, keyed by the call's X-Call header.
type sumPerAttempt struct {
	rt   http.RoundTripper
	mu   sync.Mutex
	sums map[string][]testsupport.BodySum
}

func (s *sumPerAttempt) RoundTrip(req *http.Request) (*http.Response, error) {
	sum, _, err := testsupport.SumGetBody(req.GetBody)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.sums[req.Header.Get("X-Call")] = append(s.sums[req.Header.Get("X-Call")], sum)
	s.mu.Unlock()
	return s.rt.RoundTrip(req)
}

// lingeringTransport answers every request at once with a 403 and leaves
// its body open: a goroutine reads it only once release is closed, as an
// HTTP/2 transport's writer may still read a request's body after the
// answer came and closes it from another goroutine (the RoundTripper
// contract allows both), and records the digest of what it read, keyed by
// the call's X-Call header.
type lingeringTransport struct {
	release chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	read    map[string][]testsupport.BodySum
}

func (l *lingeringTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	name, body := req.Header.Get("X-Call"), req.Body
	l.wg.Go(func() {
		<-l.release
		h := sha256.New()
		_, err := io.Copy(h, body)
		_ = body.Close()
		var sum testsupport.BodySum
		if err == nil {
			h.Sum(sum[:0])
		}
		l.mu.Lock()
		l.read[name] = append(l.read[name], sum)
		l.mu.Unlock()
	})
	const answer = `{"message": "forbidden"}`
	return &http.Response{
		StatusCode: http.StatusForbidden, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(answer)), ContentLength: int64(len(answer)), Request: req,
	}, nil
}

// TestEarlyAnswerToLargeUpload pins the pooled request body's lifetime
// against a transport that still reads it after the answer came (K14, PM4;
// ruling R92 m-2): each call sends 6 MiB, the answer is a 403 that comes
// before any of it is read, and the policy retries it (Statuses(403)), so
// every attempt leaves a reader open when the call returns and drops its
// own reference. The scratch must not be recycled, and so reused by the
// next call's encoding, while a reader is open.
//
// With the lingering transport the readers read only after the next call
// has encoded its body: every attempt's reader must still read its own
// call's bytes, which a scratch recycled at the call's return would have
// replaced with the next call's (the digest check fails without -race).
// Over the SDK's own HTTP/2 transport and the loopback server, four
// concurrent calls: the server answers early and reads each call's third
// attempt whole, whose digest must be the call's body; over a caller's
// HTTP/2 transport, every attempt's GetBody read must hash to the call's
// body too. net/http aborts an upload answered with a status above 299, so
// these two rows leave the transport's writer only a short window; under
// -race they check what that window holds.
func TestEarlyAnswerToLargeUpload(t *testing.T) {
	const attempts = 3
	policy := fastRetry().Statuses(http.StatusForbidden).MaxRetries(attempts - 1)

	t.Run("success: readers that outlive their call", func(t *testing.T) {
		l := &lingeringTransport{release: make(chan struct{}), read: map[string][]testsupport.BodySum{}}
		c := newTestClient(t, l, WithRetry(policy))
		qs := noulQuestion(t)
		calls := uploadCalls(8)
		for _, call := range calls {
			_, err := c.SystemOne(t.Context(), call.state, qs, Header("X-Call", call.name))
			if ae, ok := errors.AsType[*APIError](err); !ok || ae.StatusCode != http.StatusForbidden {
				t.Fatalf("%s: error = %T %v, want the last 403", call.name, err, err)
			}
		}
		close(l.release)
		l.wg.Wait()
		for _, call := range calls {
			want := testsupport.BodySum(sha256.Sum256(call.body))
			if diff := gocmp.Diff([]testsupport.BodySum{want, want, want}, l.read[call.name]); diff != "" {
				t.Errorf("%s: digests of what each attempt's reader read after the call returned (-want +got):\n%s", call.name, diff)
			}
		}
	})

	calls := uploadCalls(4)
	// earlyThenRead answers 403 without reading the body until a call's
	// last attempt, which it reads whole and hashes into got.
	earlyThenRead := func(mu *sync.Mutex, seen map[string]int, got map[string]testsupport.BodySum) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			name := r.Header.Get("X-Call")
			mu.Lock()
			seen[name]++
			last := seen[name] == attempts
			mu.Unlock()
			if last {
				h := sha256.New()
				if _, err := io.Copy(h, r.Body); err != nil {
					t.Errorf("%s: reading the last attempt's body: %v", name, err)
				}
				var sum testsupport.BodySum
				h.Sum(sum[:0])
				mu.Lock()
				got[name] = sum
				mu.Unlock()
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"message": "forbidden"}`)
		}
	}
	tests := map[string]struct {
		// client returns the client's transport options and, for a caller
		// transport, the per-attempt digests it records.
		client func(t *testing.T) ([]ClientOption, *sumPerAttempt)
	}{
		"success: the SDK's own transport": {
			client: func(t *testing.T) ([]ClientOption, *sumPerAttempt) {
				return []ClientOption{WithRootCAs(testsupport.RootCAs(t)), WithProxy(nil)}, nil
			},
		},
		"success: a caller's HTTP/2 transport, each attempt hashed": {
			client: func(t *testing.T) ([]ClientOption, *sumPerAttempt) {
				tr := &http.Transport{Protocols: new(http.Protocols), TLSClientConfig: testsupport.ClientTLSConfig(t)}
				tr.Protocols.SetHTTP2(true)
				t.Cleanup(tr.CloseIdleConnections)
				s := &sumPerAttempt{rt: tr, sums: map[string][]testsupport.BodySum{}}
				return []ClientOption{WithRoundTripper(s)}, s
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			seen := map[string]int{}
			got := map[string]testsupport.BodySum{}
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: earlyThenRead(&mu, seen, got)})
			opts, sums := tt.client(t)
			clearEnv(t)
			c, err := NewClient(append([]ClientOption{WithAPIKey(testKey), WithBaseURL(srv.URL()), WithRetry(policy)}, opts...)...)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			t.Cleanup(func() { _ = c.Close() })
			qs := noulQuestion(t)
			var wg sync.WaitGroup
			for _, call := range calls {
				wg.Go(func() {
					_, err := callWithinBound(t, func(ctx context.Context) error {
						_, err := c.SystemOne(ctx, call.state, qs, Header("X-Call", call.name))
						return err
					})
					if ae, ok := errors.AsType[*APIError](err); !ok || ae.StatusCode != http.StatusForbidden {
						t.Errorf("%s: error = %T %v, want the last 403", call.name, err, err)
					}
				})
			}
			wg.Wait()
			assertAttempts(t, c, attempts*len(calls))
			for _, call := range calls {
				want := testsupport.BodySum(sha256.Sum256(call.body))
				mu.Lock()
				served, sum := seen[call.name], got[call.name]
				mu.Unlock()
				if served != attempts || sum != want {
					t.Errorf("%s: the server saw %d attempts and read a last body hashing %v, want %d and %v", call.name, served, sum, attempts, want)
				}
				if sums == nil {
					continue
				}
				sums.mu.Lock()
				perAttempt := sums.sums[call.name]
				sums.mu.Unlock()
				wantSums := []testsupport.BodySum{want, want, want}
				if diff := gocmp.Diff(wantSums, perAttempt); diff != "" {
					t.Errorf("%s: GetBody digests per attempt (-want +got):\n%s", call.name, diff)
				}
			}
		})
	}
}
