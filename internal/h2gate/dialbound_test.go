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

package h2gate

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// fakeTLSH2 is an HTTP/2 server over TLS on the in-memory network
// (httptest.NewTestServer with EnableHTTP2), for a transport that does its
// own TLS, as NewTransport's does, inside a testing/synctest bubble.
type fakeTLSH2 struct {
	// pool trusts the server's certificate, which covers example.com.
	pool *x509.CertPool
	// raw dials the server's TLS listener and returns the connection before
	// TLS, as a TCP dial would: the transport's own handshake runs on it.
	raw func(ctx context.Context, network, addr string) (net.Conn, error)
	// news and closes count the connections the server accepted and closed.
	news, closes atomic.Int64
}

// newFakeTLSH2 starts a fakeTLSH2 whose handler answers 200.
func newFakeTLSH2(t *testing.T) *fakeTLSH2 {
	t.Helper()
	f := &fakeTLSH2{}
	srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	srv.EnableHTTP2 = true
	// The handshakes the tests abandon end in EOF on the server, which
	// would print them.
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		switch st {
		case http.StateNew:
			f.news.Add(1)
		case http.StateClosed:
			f.closes.Add(1)
		default:
		}
	}
	fake, ok := srv.Client().Transport.(*http.Transport) // starts the in-memory network
	if !ok {
		t.Fatalf("fake client transport is %T", srv.Client().Transport)
	}
	f.pool = x509.NewCertPool()
	f.pool.AddCert(srv.Certificate())
	f.raw = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := fake.DialTLSContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tc, ok := c.(*tls.Conn)
		if !ok {
			return nil, errors.New("the in-memory TLS dial returned no *tls.Conn")
		}
		return tc.NetConn(), nil
	}
	return f
}

// hookDialer runs, around dial, the trace hooks a net.Dialer runs, in its
// order: DNSStart and DNSDone for the host, then ConnectStart and
// ConnectDone around the connection. A net.Dialer calls the same composed
// hooks, through the context value httptrace.WithClientTrace sets for the
// net package, on the goroutine that dials, so a hook that blocks here
// blocks where it would block there; the in-memory network has no DNS or
// TCP connect of its own to run them.
func hookDialer(dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		trace := httptrace.ContextClientTrace(ctx)
		if trace == nil {
			return dial(ctx, network, addr)
		}
		host, _, _ := net.SplitHostPort(addr)
		if trace.DNSStart != nil {
			trace.DNSStart(httptrace.DNSStartInfo{Host: host})
		}
		if trace.DNSDone != nil {
			trace.DNSDone(httptrace.DNSDoneInfo{})
		}
		if trace.ConnectStart != nil {
			trace.ConnectStart(network, addr)
		}
		conn, err := dial(ctx, network, addr)
		if trace.ConnectDone != nil {
			trace.ConnectDone(network, addr, err)
		}
		return conn, err
	}
}

// blockingTrace returns a trace whose hook named hook blocks until release
// is closed; entered counts the calls of the hook.
func blockingTrace(t *testing.T, hook string, release <-chan struct{}, entered *atomic.Int32) *httptrace.ClientTrace {
	t.Helper()
	block := func() {
		entered.Add(1)
		<-release
	}
	switch hook {
	case "DNSStart":
		return &httptrace.ClientTrace{DNSStart: func(httptrace.DNSStartInfo) { block() }}
	case "DNSDone":
		return &httptrace.ClientTrace{DNSDone: func(httptrace.DNSDoneInfo) { block() }}
	case "ConnectStart":
		return &httptrace.ClientTrace{ConnectStart: func(string, string) { block() }}
	case "ConnectDone":
		return &httptrace.ClientTrace{ConnectDone: func(string, string, error) { block() }}
	case "TLSHandshakeStart":
		return &httptrace.ClientTrace{TLSHandshakeStart: block}
	case "TLSHandshakeDone":
		return &httptrace.ClientTrace{TLSHandshakeDone: func(tls.ConnectionState, error) { block() }}
	default:
		t.Fatalf("no hook %q", hook)
		return nil
	}
}

// receive returns ch's result if one is ready, or reports that the call
// named what is still waiting.
func receive(t *testing.T, what string, ch <-chan result) (result, bool) {
	t.Helper()
	select {
	case r := <-ch:
		return r, true
	default:
		t.Errorf("%s is still waiting at the bound", what)
		return result{}, false
	}
}

// boundCause returns the *boundError in err's chain, with the *DialError
// that carries it, or nil values when either is missing.
func boundCause(err error) (*DialError, *boundError) {
	de, ok := errors.AsType[*DialError](err)
	if !ok {
		return nil, nil
	}
	be, _ := errors.AsType[*boundError](de.Err)
	return de, be
}

// TestDialPhaseBound pins the K28d bound (owner decision G11 (5)) on fake
// time. The first call carries a trace whose DNS, connect or TLS handshake
// hook blocks the dial of the transport's first connection, and no
// deadline, as under the root package's WithNoTimeout; the second, without
// a hook and started 100 ms later, waits for the first at the gate.
//
// NewTransport's dialer runs the DNS and connect hooks inside the dial,
// which it abandons at the connect timeout plus its grace (dialGrace): both
// calls fail with a timeout then, while the hook still blocks, the dial's
// connection permit is free, and a third call dials anew and succeeds. The
// TLS hooks run in
// net/http's handshake, after the dial, and a caller's dialer under Wrap is
// not the transport's to end: there the first call's wait for a connection
// ends at the wait bound (the connect timeout plus the TLS handshake
// timeout), the transport is marked stalled, and the third call, which
// waits behind the dial the hook holds, ends at its own wait bound. Once
// the hook returns, a fourth call succeeds and clears the mark.
//
// The instants are the configured bounds, not measurements (STANDING 9): a
// transport that dials without its own goroutine, never bounds the wait,
// or doubles either bound leaves a call waiting at the instant the test
// reads it. It runs in CI's -race test step (go test -race with coverage)
// on ubuntu-26.04, xcode-27 and windows-2025.
func TestDialPhaseBound(t *testing.T) {
	const (
		connect = time.Second
		// wait is the wait bound: the connect timeout plus the TLS handshake
		// timeout, which both transports set to the connect timeout; no proxy
		// may apply.
		wait    = connect + connect
		stagger = 100 * time.Millisecond
	)
	type outcome struct {
		first          time.Duration // when the first two calls fail, from the start
		what           string        // the bound's error names what did not end
		thirdSucceeds  bool          // the third call gets a connection; otherwise it fails at its wait bound
		dialExp, waitX uint64        // Stats.DialExpiries, Stats.WaitExpiries
		events         []string      // the DEBUG events
	}
	dialBound := outcome{
		first:         connect + dialGrace,
		what:          "dial tcp example.com:443",
		thirdSucceeds: true,
		dialExp:       1,
		events: []string{
			"h2: dial bound expired bound=1.1s", "h2: gate error reason=timeout", // the first call
			"h2: dial h2=true", "h2: gate release", // the third
		},
	}
	waitBound := outcome{
		first: wait,
		what:  "the wait for a connection",
		waitX: 2,
		events: []string{
			"h2: connection wait expired bound=2s", "h2: gate error reason=timeout", // the first call
			"h2: connection wait expired bound=2s", "h2: gate error reason=timeout", // the third
			"h2: dial h2=true", "h2: gate release", // the fourth
		},
	}
	tests := map[string]struct {
		hook string
		wrap bool // Wrap over a caller transport with the same dialer, instead of NewTransport
		want outcome
	}{
		"error: a blocking DNSStart fails the dial at the connect timeout and grace":     {hook: "DNSStart", want: dialBound},
		"error: a blocking DNSDone fails the dial at the connect timeout and grace":      {hook: "DNSDone", want: dialBound},
		"error: a blocking ConnectStart fails the dial at the connect timeout and grace": {hook: "ConnectStart", want: dialBound},
		"error: a blocking ConnectDone fails the dial at the connect timeout and grace":  {hook: "ConnectDone", want: dialBound},
		"error: a blocking TLSHandshakeStart ends the wait at the wait bound":            {hook: "TLSHandshakeStart", want: waitBound},
		"error: a blocking TLSHandshakeDone ends the wait at the wait bound":             {hook: "TLSHandshakeDone", want: waitBound},
		"error: a blocking DNSStart in a caller's dialer ends the wait at the bound":     {hook: "DNSStart", wrap: true, want: waitBound},
		"error: a blocking DNSDone in a caller's dialer ends the wait at the bound":      {hook: "DNSDone", wrap: true, want: waitBound},
		"error: a blocking ConnectStart in a caller's dialer ends the wait at the bound": {
			hook: "ConnectStart", wrap: true, want: waitBound,
		},
		"error: a blocking ConnectDone in a caller's dialer ends the wait at the bound": {
			hook: "ConnectDone", wrap: true, want: waitBound,
		},
		"error: a blocking TLSHandshakeStart under Wrap ends the wait at the wait bound": {
			hook: "TLSHandshakeStart", wrap: true, want: waitBound,
		},
		"error: a blocking TLSHandshakeDone under Wrap ends the wait at the wait bound": {
			hook: "TLSHandshakeDone", wrap: true, want: waitBound,
		},
	}
	attrs := map[string]string{
		"h2: dial": "h2", "h2: gate error": "reason",
		"h2: dial bound expired": "bound", "h2: connection wait expired": "bound",
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := newFakeTLSH2(t)
				logs := testsupport.NewLogRecorder(slog.LevelDebug)
				api := mustURL(t, "https://example.com")
				var (
					tr  *Transport
					err error
				)
				if tt.wrap {
					base := &http.Transport{DialContext: hookDialer(srv.raw), TLSClientConfig: &tls.Config{RootCAs: srv.pool, MinVersion: tls.VersionTLS12}}
					tr, err = Wrap(base, Config{APIURL: api, ConnectTimeout: connect, Logger: logs.Logger()})
				} else {
					tr, err = NewTransport(Config{APIURL: api, ConnectTimeout: connect, RootCAs: srv.pool, DialContext: hookDialer(srv.raw), Logger: logs.Logger()})
				}
				if err != nil {
					t.Fatalf("build: %v", err)
				}
				t.Cleanup(tr.CloseIdleConnections)
				if tr.waitBound != wait {
					t.Fatalf("wait bound %v, want %v", tr.waitBound, wait)
				}

				release := make(chan struct{})
				var entered atomic.Int32
				start := time.Now()
				first := make(chan result, 1)
				go func() {
					first <- get(httptrace.WithClientTrace(t.Context(), blockingTrace(t, tt.hook, release, &entered)), tr, "https://example.com/first")
				}()
				synctest.Wait() // the first call's dial is blocked in its hook
				time.Sleep(stagger)
				second := make(chan result, 1)
				go func() {
					ctx, cancel := context.WithTimeout(t.Context(), fakeDeadline)
					defer cancel()
					second <- get(ctx, tr, "https://example.com/second")
				}()
				synctest.Wait() // the second call waits at the gate
				if entered.Load() != 1 || tr.parked.Load() != 1 {
					t.Fatalf("hook entered %d times, %d waiters parked; want the first call in its hook and the second at the gate", entered.Load(), tr.parked.Load())
				}

				time.Sleep(tt.want.first - stagger)
				synctest.Wait() // the bound has ended both calls, while the hook still blocks
				f, fok := receive(t, "the first call", first)
				s, sok := receive(t, "the second call", second)
				if fok {
					de, be := boundCause(f.Err)
					if de == nil || be == nil || !de.Timeout || de.Proxy || !errors.Is(f.Err, context.DeadlineExceeded) || f.Done.Sub(start) != tt.want.first {
						t.Errorf("first call: %v (%s) after %v; want a timeout DialError around the bound's error after %v", f.Err, chain(f.Err), f.Done.Sub(start), tt.want.first)
					} else if be.what != tt.want.what {
						t.Errorf("first call's bound names %q, want %q", be.what, tt.want.what)
					}
					// The chain's net.Error answers Timeout and Temporary true, as a
					// dial's own timeout does.
					ne, isNet := errors.AsType[net.Error](f.Err)
					//lint:ignore SA1019 the deprecated method is part of the net.Error the chain carries
					if !isNet || !ne.Timeout() || !ne.Temporary() { //nolint:staticcheck // SA1019: see above
						t.Errorf("first call: the chain's net.Error is %v (found %t); want one whose Timeout and Temporary are true", ne, isNet)
					}
					if sok {
						sde, _ := boundCause(s.Err)
						if sde == nil || sde == de || sde.Err != de.Err || s.Done.Sub(start) != tt.want.first { //nolint:errorlint // identity: a waiter shares the leader's cause (R19)
							t.Errorf("second call: %v after %v; want a fresh DialError around the first call's cause at the same instant", s.Err, s.Done.Sub(start))
						}
					}
				}
				if entered.Load() != 1 {
					t.Errorf("hook entered %d times, want 1", entered.Load())
				}

				third := make(chan result, 1)
				go func() {
					ctx, cancel := context.WithTimeout(t.Context(), fakeDeadline)
					defer cancel()
					third <- get(ctx, tr, "https://example.com/third")
				}()
				if tt.want.thirdSucceeds {
					synctest.Wait()
					if r, ok := receive(t, "the third call", third); ok && (r.Err != nil || r.Status != http.StatusOK) {
						t.Errorf("third call: %d %v; want 200 on a dial of its own while the hook still blocks", r.Status, r.Err)
					}
				} else {
					time.Sleep(wait)
					synctest.Wait()
					if r, ok := receive(t, "the third call", third); ok {
						if _, be := boundCause(r.Err); be == nil || be.what != "the wait for a connection" || r.Done.Sub(r.Start) != wait {
							t.Errorf("third call: %v (%s) after %v; want the wait bound's timeout after %v", r.Err, chain(r.Err), r.Done.Sub(r.Start), wait)
						}
					}
				}

				close(release)
				synctest.Wait() // the hook has returned, and the dial it held with it
				ctx, cancel := context.WithTimeout(t.Context(), fakeDeadline)
				defer cancel()
				if r := get(ctx, tr, "https://example.com/fourth"); r.Err != nil || r.Status != http.StatusOK || r.ProtoMajor != 2 {
					t.Errorf("fourth call: %d HTTP/%d %v; want 200 over HTTP/2 once the hook returned", r.Status, r.ProtoMajor, r.Err)
				}
				time.Sleep(wait) // a bound left armed by a call that ended would fire by now
				synctest.Wait()
				if tr.stalled.Load() {
					t.Error("the transport is marked stalled after a connection was handed over")
				}
				st := tr.Stats()
				if st.DialExpiries != tt.want.dialExp || st.WaitExpiries != tt.want.waitX {
					t.Errorf("stats %+v; want %d dial expiries and %d wait expiries", st, tt.want.dialExp, tt.want.waitX)
				}
				if diff := gocmp.Diff(tt.want.events, debugEvents(logs, attrs)); diff != "" {
					t.Errorf("DEBUG events (-want +got):\n%s", diff)
				}
				if tt.want.dialExp > 0 {
					// The server saw the third call's connection open, and one more
					// open and close: under ConnectDone the connection the abandoned
					// dial got once the hook returned, which its goroutine closed;
					// under the DNS and ConnectStart hooks the dial ran after the
					// bound, on an ended context, and aborted its own.
					// TestBoundedDialGrace pins the goroutine's close alone.
					if n, c := srv.news.Load(), srv.closes.Load(); n != 2 || c != 1 {
						t.Errorf("the server accepted %d connections and saw %d close; want 2 and 1: the abandoned dial's connection is closed", n, c)
					}
				}
			})
		})
	}
}

// TestDialPhaseBoundWarm pins the K28d bound on a warm transport whose
// connection has gone, on fake time: the next call re-dials, holding the
// header-write token, and its trace's TLS handshake hook (NewTransport) or
// connect hook (a caller's dialer under Wrap) blocks the dial. A proxy func
// that answers "no proxy" makes the wait bound (connect + handshake + the
// one-minute CONNECT limit + handshake, 63 s) longer than the hold bound
// (connect + handshake, 2 s), so a call made 100 ms later leaves the token
// wait at the hold bound without the token, before the first call's wait
// ends and the transport is marked stalled; it then waits behind the held
// dial under a wait bound of its own and fails 63 s after it went out. The
// first call fails at its wait bound, and a call made after both fails at
// its own, marked stalled. Once the hook returns, the transport dials again
// and a call succeeds.
//
// The instants are the configured bounds (STANDING 9): a transport that
// sent the call out without the token unbounded waits for the hook, and so
// does one that bounds neither a re-dialing call nor the stalled one. It
// runs in CI's -race test step (go test -race with coverage) on
// ubuntu-26.04, xcode-27 and windows-2025.
func TestDialPhaseBoundWarm(t *testing.T) {
	const (
		connect = time.Second
		hold    = connect + connect                               // the hold bound
		wait    = connect + connect + proxyConnectLimit + connect // the wait bound when a proxy may apply
		stagger = 100 * time.Millisecond
	)
	noProxy := func(*http.Request) (*url.URL, error) { return nil, nil }
	tests := map[string]struct {
		hook string
		wrap bool
	}{
		"error: a re-dial whose TLSHandshakeStart blocks fails every call at its wait bound": {hook: "TLSHandshakeStart"},
		"error: a re-dial whose ConnectStart blocks in a caller's dialer fails every call at its wait bound": {
			hook: "ConnectStart", wrap: true,
		},
	}
	attrs := map[string]string{
		"h2: dial": "h2", "h2: redial error": "reason",
		"h2: token wait expired": "bound", "h2: connection wait expired": "bound",
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := newFakeTLSH2(t)
				logs := testsupport.NewLogRecorder(slog.LevelDebug)
				api := mustURL(t, "https://example.com")
				var (
					tr  *Transport
					err error
				)
				if tt.wrap {
					base := &http.Transport{DialContext: hookDialer(srv.raw), Proxy: noProxy, TLSClientConfig: &tls.Config{RootCAs: srv.pool, MinVersion: tls.VersionTLS12}}
					tr, err = Wrap(base, Config{APIURL: api, ConnectTimeout: connect, Logger: logs.Logger()})
				} else {
					tr, err = NewTransport(Config{APIURL: api, ConnectTimeout: connect, RootCAs: srv.pool, Proxy: noProxy, DialContext: hookDialer(srv.raw), Logger: logs.Logger()})
				}
				if err != nil {
					t.Fatalf("build: %v", err)
				}
				t.Cleanup(tr.CloseIdleConnections)
				if tr.waitBound != wait || tr.holdBound != hold {
					t.Fatalf("bounds wait %v, hold %v; want %v and %v", tr.waitBound, tr.holdBound, wait, hold)
				}
				if r := fakeGetTLS(t, tr, "/warm-up"); r.Err != nil || r.Status != http.StatusOK {
					t.Fatalf("warm-up call: %d %v", r.Status, r.Err)
				}
				tr.CloseIdleConnections() // the connection goes: the next call re-dials

				release := make(chan struct{})
				var entered atomic.Int32
				start := time.Now()
				first := make(chan result, 1)
				go func() {
					first <- get(httptrace.WithClientTrace(t.Context(), blockingTrace(t, tt.hook, release, &entered)), tr, "https://example.com/first")
				}()
				synctest.Wait() // the first call holds the token, its re-dial blocked in the hook
				time.Sleep(stagger)
				second := make(chan result, 1)
				go func() { second <- fakeGetTLS(t, tr, "/second") }()
				synctest.Wait() // the second call waits for the token
				time.Sleep(hold)
				synctest.Wait() // it went out without the token and waits behind the held dial
				if n := tr.Stats().TokenExpiries; n != 1 || entered.Load() != 1 {
					t.Fatalf("token expiries %d, hook entered %d times; want the second call out without the token and the first in its hook", n, entered.Load())
				}

				time.Sleep(wait - stagger - hold)
				synctest.Wait() // the first call's wait bound has ended
				if f, ok := receive(t, "the first call", first); ok {
					if _, be := boundCause(f.Err); be == nil || f.Done.Sub(start) != wait {
						t.Errorf("first call: %v (%s) after %v; want the wait bound's timeout after %v", f.Err, chain(f.Err), f.Done.Sub(start), wait)
					}
				}
				select {
				case s := <-second:
					t.Fatalf("second call done after %v, before its own wait bound: %v", s.Done.Sub(start), s.Err)
				default:
				}
				time.Sleep(stagger + hold)
				synctest.Wait() // the second call's own wait bound has ended
				if s, ok := receive(t, "the second call", second); ok {
					if _, be := boundCause(s.Err); be == nil || s.Done.Sub(s.Start) != hold+wait {
						t.Errorf("second call: %v (%s) after %v; want the wait bound's timeout %v after it went out, %v after it started", s.Err, chain(s.Err), s.Done.Sub(s.Start), wait, hold+wait)
					}
				}

				third := make(chan result, 1)
				go func() { third <- fakeGetTLS(t, tr, "/third") }()
				time.Sleep(wait)
				synctest.Wait() // the third call, stalled, waited behind the held dial
				if r, ok := receive(t, "the third call", third); ok {
					if _, be := boundCause(r.Err); be == nil || r.Done.Sub(r.Start) != wait {
						t.Errorf("third call: %v (%s) after %v; want the wait bound's timeout after %v", r.Err, chain(r.Err), r.Done.Sub(r.Start), wait)
					}
				}

				close(release)
				synctest.Wait() // the hook has returned, and the dial it held with it
				if r := fakeGetTLS(t, tr, "/fourth"); r.Err != nil || r.Status != http.StatusOK {
					t.Errorf("fourth call: %d %v; want 200 once the hook returned", r.Status, r.Err)
				}
				time.Sleep(wait) // a bound left armed by a call that ended would fire by now
				synctest.Wait()
				if tr.stalled.Load() {
					t.Error("the transport is marked stalled after a connection was handed over")
				}
				if st := tr.Stats(); st.TokenExpiries != 1 || st.WaitExpiries != 3 || st.DialExpiries != 0 {
					t.Errorf("stats %+v; want 1 token expiry and 3 wait expiries", st)
				}
				want := []string{
					"h2: dial h2=true", "h2: gate release", // the warm-up
					"h2: token wait expired bound=2s",                                           // the second call
					"h2: connection wait expired bound=1m3s", "h2: redial error reason=timeout", // the first
					"h2: connection wait expired bound=1m3s", "h2: redial error reason=timeout", // the second
					"h2: connection wait expired bound=1m3s", "h2: redial error reason=timeout", // the third
					"h2: dial h2=true", // the fourth
				}
				if diff := gocmp.Diff(want, debugEvents(logs, attrs)); diff != "" {
					t.Errorf("DEBUG events (-want +got):\n%s", diff)
				}
			})
		})
	}
}

// fakeGetTLS sends a GET for https://example.com+path with the fake
// deadline.
func fakeGetTLS(t *testing.T, tr http.RoundTripper, path string) result {
	ctx, cancel := context.WithTimeout(t.Context(), fakeDeadline)
	defer cancel()
	return get(ctx, tr, "https://example.com"+path)
}

// TestWaitBoundRearms pins the wait bound's timer through a stock retry, on
// fake time, by driving one call's hooks as net/http drives them: GetConn
// arms the bound, GotConn stops it before it ends, and the GetConn of a
// retry that looks for a connection again re-arms it for the whole bound,
// at whose end the transport is marked stalled and then the call's context
// ends with the bound's error: the mark comes first, so the token the call
// gives back on its way out reaches the next holder after it (review W6.6
// NIT 2). The instants are the bound (STANDING 9): a timer that GotConn
// leaves running, or that the retry arms for longer, fails it. It runs in
// CI's -race test step (go test -race with coverage) on ubuntu-26.04,
// xcode-27 and windows-2025.
func TestWaitBoundRearms(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := testsupport.NewFakeH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		tr := fakeTransport(t, srv.Client().Transport)
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		var markedFirst atomic.Bool // the transport was marked when the bound ended the context
		c := &call{t: tr, bound: &waitState{cancel: func(cause error) {
			if cause != nil {
				markedFirst.Store(tr.stalled.Load())
			}
			cancel(cause)
		}}}
		c.Context = t.Context()

		c.getConn("example.com:80")
		time.Sleep(tr.waitBound / 2)
		c.gotConn(httptrace.GotConnInfo{Reused: true})
		time.Sleep(tr.waitBound)
		synctest.Wait()
		// A timer whose callback had started when GotConn stopped it runs
		// expire after GotConn won: it must leave the call alone.
		c.expire()
		if err := ctx.Err(); err != nil || tr.stalled.Load() || tr.Stats().WaitExpiries != 0 {
			t.Fatalf("after GotConn and a whole bound: context %v, stalled %t, stats %+v; want the bound stopped", context.Cause(ctx), tr.stalled.Load(), tr.Stats())
		}

		c.getConn("example.com:80") // a stock retry looks for a connection again
		time.Sleep(tr.waitBound - time.Millisecond)
		synctest.Wait()
		if err := ctx.Err(); err != nil {
			t.Fatalf("the re-armed bound ended the context %v early: %v", tr.waitBound-time.Millisecond, context.Cause(ctx))
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		be, ok := errors.AsType[*boundError](context.Cause(ctx))
		if !ok || be.bound != tr.waitBound || !tr.stalled.Load() || tr.Stats().WaitExpiries != 1 {
			t.Errorf("at the re-armed bound: cause %v, stalled %t, stats %+v; want the %v bound's error, the mark and 1 wait expiry", context.Cause(ctx), tr.stalled.Load(), tr.Stats(), tr.waitBound)
		}
		if !markedFirst.Load() {
			t.Error("the bound ended the call's context before it marked the transport stalled; want the mark first")
		}
	})
}

// TestWaitBoundContext pins the context the wait bound gives a call, on fake
// time: a call whose trace carries a dial-phase hook gets a context the
// bound can end, which its response's body keeps alive while it is read and
// ends when it is closed, so the context does not outlive the call; a call
// without such a hook goes through untouched, its response body the stock
// transport's own. It runs in CI's -race test step (go test -race with
// coverage) on ubuntu-26.04, xcode-27 and windows-2025.
func TestWaitBoundContext(t *testing.T) {
	tests := map[string]struct {
		trace   *httptrace.ClientTrace
		bounded bool
	}{
		"success: a dial-phase hook bounds the call and its body ends the context": {
			trace: &httptrace.ClientTrace{ConnectStart: func(string, string) {}}, bounded: true,
		},
		"success: a trace without one leaves the call as it is": {
			trace: &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) {}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				body := "a body of more than one read " + string(make([]byte, 64<<10))
				srv := testsupport.NewFakeH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
				tr := fakeTransport(t, srv.Client().Transport)
				for i := range 2 { // a cold call, then a warm one
					ctx, cancel := context.WithTimeout(httptrace.WithClientTrace(t.Context(), tt.trace), fakeDeadline)
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com/", nil)
					if err != nil {
						t.Fatal(err)
					}
					resp, err := tr.RoundTrip(req)
					if err != nil {
						t.Fatalf("call %d: %v", i, err)
					}
					_, wrapped := resp.Body.(*boundBody)
					sent := resp.Request.Context()
					got, err := io.ReadAll(resp.Body)
					if err != nil || string(got) != body || sent.Err() != nil {
						t.Errorf("call %d: read %d bytes, %v, context %v; want the whole body with the context alive", i, len(got), err, sent.Err())
					}
					_ = resp.Body.Close()
					if wrapped != tt.bounded || (sent.Err() != nil) != tt.bounded {
						t.Errorf("call %d: body wrapped %t, context after Close %v; want the bound's body and its context ended: %t", i, wrapped, sent.Err(), tt.bounded)
					}
					cancel()
				}
			})
		})
	}
}

// TestBoundedDialGrace pins the dial bound's grace on fake time (review
// W6.6 MINOR 1, ruling D-W6.6-dialbound-grace). A dialer that honours its
// context answers when the connect timeout ends it, or up to the end of the
// grace after, with its own error, which the transport returns as it is,
// counting and logging nothing. A dial a hook holds past the grace is
// abandoned then, with the bound's timeout, counted and logged, and the
// connection it returns once the hook lets it go is closed, twenty held
// dials in a row: a hand-over that raced the abandon would leave it open
// about half the time (review W6.6 NIT 1). The instants are the configured
// bounds (STANDING 9). It runs in CI's -race test step (go test -race with
// coverage) on ubuntu-26.04, xcode-27 and windows-2025.
func TestBoundedDialGrace(t *testing.T) {
	const timeout = time.Second
	tests := map[string]struct {
		after  time.Duration // how long after its deadline the dialer answers; held until released when negative
		rounds int
	}{
		"success: a dialer that answers at its deadline keeps its own error":            {after: 0, rounds: 1},
		"success: a dialer that answers 1 ms after its deadline keeps its own error":    {after: time.Millisecond, rounds: 1},
		"success: a dialer that answers as the grace ends keeps its own error":          {after: dialGrace - time.Millisecond, rounds: 1},
		"error: a dial held past the grace is abandoned and its late connection closed": {after: -1, rounds: 20},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				logs := testsupport.NewLogRecorder(slog.LevelDebug)
				tr := &Transport{log: logs.Logger()}
				own := &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
				var wantEvents []string
				for round := range tt.rounds {
					release, late := make(chan struct{}), &lateConn{}
					dial := tr.boundedDial(func(ctx context.Context, _, _ string) (net.Conn, error) {
						if tt.after < 0 {
							<-release
							return late, nil
						}
						<-ctx.Done()
						time.Sleep(tt.after)
						return nil, own
					}, timeout)
					start := time.Now()
					_, err := dial(t.Context(), "tcp", "example.com:443")
					elapsed := time.Since(start)
					close(release)
					synctest.Wait() // a held dial has returned, and its goroutine is done with the connection
					if tt.after >= 0 {
						if !errors.Is(err, own) || elapsed != timeout+tt.after {
							t.Errorf("err %v (%s) after %v; want the dialer's own error after %v", err, chain(err), elapsed, timeout+tt.after)
						}
						continue
					}
					be, ok := errors.AsType[*boundError](err)
					if !ok || be.bound != timeout+dialGrace || elapsed != timeout+dialGrace || !late.closed.Load() {
						t.Fatalf("round %d: err %v after %v, late connection closed %t; want the bound's error after %v and the connection closed", round, err, elapsed, late.closed.Load(), timeout+dialGrace)
					}
					wantEvents = append(wantEvents, "h2: dial bound expired bound=1.1s")
				}
				if got, want := tr.dialExpiries.Load(), uint64(len(wantEvents)); got != want {
					t.Errorf("dial expiries %d, want %d", got, want)
				}
				if diff := gocmp.Diff(wantEvents, debugEvents(logs, map[string]string{"h2: dial bound expired": "bound"})); diff != "" {
					t.Errorf("DEBUG events (-want +got):\n%s", diff)
				}
			})
		})
	}
}

// lateConn is the connection a held dial returns after the bound abandoned
// it; its Close is recorded.
type lateConn struct {
	net.Conn
	closed atomic.Bool
}

// Close records the close.
func (c *lateConn) Close() error {
	c.closed.Store(true)
	return nil
}

// TestDialUnreachableHostKeepsItsError pins, on the real network, that a
// connect timeout to a host that does not answer is the dialer's own
// (review W6.6 MINOR 1, ruling D-W6.6-dialbound-grace). A request without a
// trace to 192.0.2.1, in TEST-NET-1 (RFC 5737), which no host answers,
// through NewTransport with a 20 ms connect timeout fails with net.Dialer's
// *net.OpError in its chain, not the bound's error, and neither counts a
// dial expiry nor logs "h2: dial bound expired". Before the grace the bound
// won the race with the dialer's own timer in 598 of 600 dials on (M) and
// 543 of 600 on (L) (ledger W6.6-04). A network that refuses the address at
// once answers with a *net.OpError too.
func TestDialUnreachableHostKeepsItsError(t *testing.T) {
	logs := testsupport.NewLogRecorder(slog.LevelDebug)
	tr, err := NewTransport(Config{
		APIURL:         mustURL(t, "https://192.0.2.1"),
		ConnectTimeout: 20 * time.Millisecond,
		Proxy:          func(*http.Request) (*url.URL, error) { return nil, nil },
		Logger:         logs.Logger(),
	})
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	t.Cleanup(tr.CloseIdleConnections)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://192.0.2.1/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a request to 192.0.2.1 succeeded; the test needs an address no host answers")
	}
	_, isOp := errors.AsType[*net.OpError](err)
	_, be := boundCause(err)
	events := debugEvents(logs, nil)
	if !isOp || be != nil || tr.Stats().DialExpiries != 0 || slices.Contains(events, "h2: dial bound expired") {
		t.Errorf("err %v (%s), %d dial expiries, DEBUG events %q; want the dialer's own *net.OpError, none counted and no dial bound event", err, chain(err), tr.Stats().DialExpiries, events)
	}
}

// TestCallSize pins the size of call, which send allocates for every
// request: at most 192 bytes on 64-bit, the malloc size class it is
// allocated from (size classes 14 to 16 are 192, 208 and 224 bytes,
// GOROOT/src/internal/runtime/gc/sizeclasses.go). A field that takes it
// past 192 bytes costs every request the next class, the untraced
// included: the first cut of the K28d wait bound held its state in the
// call, which made it 216 bytes, allocated from the 224-byte class, 32
// bytes more a request (W6.6; ledger W6.6-01). That state lives behind
// call.bound, allocated only for a request the bound applies to. On 32-bit
// the call is smaller still. reflect gives the size unsafe.Sizeof would:
// the module keeps unsafe out of internal/h2gate, tests included
// (TestSeamImports, NF6).
func TestCallSize(t *testing.T) {
	const sizeClass = 192
	got := reflect.TypeFor[call]().Size()
	t.Logf("SIZE call=%d bytes, size class %d", got, sizeClass)
	if got > sizeClass {
		t.Errorf("the size of call = %d bytes, want at most %d: the call leaves the %d-byte size class, and every request pays for the next one", got, sizeClass, sizeClass)
	}
}
