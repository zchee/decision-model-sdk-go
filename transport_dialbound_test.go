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
	"log/slog"
	"net"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// dialPhaseTrace returns a trace whose hook named hook blocks until release
// is closed, the first time it runs only, and then panics with a hookPanic
// when late is set; runs counts the hook's runs, blocked ones included.
func dialPhaseTrace(t *testing.T, hook string, release <-chan struct{}, late bool, runs *atomic.Int32) *httptrace.ClientTrace {
	t.Helper()
	var armed atomic.Bool
	armed.Store(true)
	block := func() {
		runs.Add(1)
		if armed.CompareAndSwap(true, false) {
			<-release
			if late {
				panic(hookPanic{hook: hook})
			}
		}
	}
	switch hook {
	case "DNSStart":
		return &httptrace.ClientTrace{DNSStart: func(httptrace.DNSStartInfo) { block() }}
	case "ConnectStart":
		return &httptrace.ClientTrace{ConnectStart: func(string, string) { block() }}
	case "TLSHandshakeStart":
		return &httptrace.ClientTrace{TLSHandshakeStart: block}
	default:
		t.Fatalf("no hook %q", hook)
		return nil
	}
}

// TestClientTraceDialPhaseBound pins the dial-phase bound
// through the public options, on the SDK's default transport and the real
// net.Dialer over loopback TLS: a WithClientTrace hook that blocks the first
// connection's DNS lookup (a localhost base URL), TCP connect or TLS
// handshake ends the call under WithNoTimeout with a *TimeoutError at the
// bound, while the hook still blocks. The bound is the connect timeout plus
// the dial bound's grace of 100 ms for DNSStart and ConnectStart, which run
// inside the dial, and twice the connect timeout for TLSHandshakeStart, the
// wait bound (on this transport the TLS handshake timeout is the connect
// timeout). The call's INFO "request failed" record and the transport's
// DEBUG record of the bound name it.
//
// A second call made while the hook still blocks shows what the bound
// leaves: after a DNS or connect hook the dial was abandoned and the permit
// freed, so the call dials anew and succeeds, running the client's hook
// again beside the run that still blocks; after a TLS hook the dial still
// holds the permit, so the call fails at its own wait bound. Once the hook
// returns, the next call succeeds.
//
// The bounds are the configured connect timeout and the dial bound's grace
// past it, not a measurement: a call the bound did not end waits until the
// watchdog frees its hook, hold after the start, beyond the upper limit. The
// clock is real: the lower limit allows one coarse tick, and the upper one
// slack for a slow -race runner, far below hold.
func TestClientTraceDialPhaseBound(t *testing.T) {
	const (
		connect = 250 * time.Millisecond
		grace   = 100 * time.Millisecond // internal/h2gate's dialGrace, past the connect timeout
		slack   = 2 * time.Second
		hold    = 10 * time.Second
	)
	tests := map[string]struct {
		hook, host string
		bound      time.Duration
		record     string // the transport's DEBUG record of the bound
		// redials: a call made while the hook blocks dials anew and succeeds;
		// otherwise it waits behind the held dial and fails at its bound.
		redials bool
	}{
		"error: a blocking DNSStart ends the call at the connect timeout and grace": {
			hook: "DNSStart", host: "localhost", bound: connect + grace, record: "h2: dial bound expired", redials: true,
		},
		"error: a blocking ConnectStart ends the call at the connect timeout and grace": {
			hook: "ConnectStart", host: "127.0.0.1", bound: connect + grace, record: "h2: dial bound expired", redials: true,
		},
		"error: a blocking TLSHandshakeStart ends the call at the wait bound": {
			hook: "TLSHandshakeStart", host: "127.0.0.1", bound: 2 * connect, record: "h2: connection wait expired",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: apiHandler(t)})
			_, port, err := net.SplitHostPort(srv.Addr())
			if err != nil {
				t.Fatal(err)
			}
			release := make(chan struct{})
			var once sync.Once
			free := func() { once.Do(func() { close(release) }) }
			t.Cleanup(free)
			var runs atomic.Int32
			logs := testsupport.NewLogRecorder(slog.LevelDebug)
			clearEnv(t)
			c, err := NewClient(WithAPIKey(testKey), WithBaseURL("https://"+net.JoinHostPort(tt.host, port)), WithRootCAs(testsupport.RootCAs(t)),
				WithProxy(nil), WithNoTimeout(), WithConnectTimeout(connect), WithRetry(NoRetry()), WithLogger(logs.Logger()),
				WithClientTrace(dialPhaseTrace(t, tt.hook, release, false, &runs)))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			t.Cleanup(func() { _ = c.Close() })
			watchdog := time.AfterFunc(hold, free)
			defer watchdog.Stop()
			blocking := func() bool {
				select {
				case <-release:
					return false
				default:
					return true
				}
			}

			start := time.Now()
			_, err = c.Models().List(t.Context())
			elapsed := time.Since(start)
			var te *TimeoutError
			if !errors.As(err, &te) || te.Timeout != 0 || te.Proxy() || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("List = %T %v, want a *TimeoutError without an attempt timeout", err, err)
			}
			if elapsed < tt.bound-coarseTick || elapsed > tt.bound+slack || !blocking() {
				t.Errorf("List failed after %v, the hook still blocking: %t; want %v (+%v slack) while it blocks", elapsed, blocking(), tt.bound, slack)
			}
			var failed, bounded bool
			for _, r := range logs.Records() {
				switch {
				case r.Level == slog.LevelInfo && r.Message == "request failed":
					v, _ := r.Attr("error")
					failed = v.String() == te.Error()
				case r.Level == slog.LevelDebug && r.Message == tt.record:
					v, _ := r.Attr("bound")
					bounded = v.Kind() == slog.KindDuration && v.Duration() == tt.bound
				}
			}
			if !failed || !bounded {
				t.Errorf("INFO %q with the timeout: %t; DEBUG %q with bound=%v: %t; records:\n%v", "request failed", failed, tt.record, tt.bound, bounded, logs.Records())
			}

			start = time.Now()
			_, err = c.Models().List(t.Context())
			elapsed = time.Since(start)
			switch {
			case tt.redials && (err != nil || runs.Load() != 2 || !blocking()):
				t.Errorf("a List while the hook blocks: %v, the hook run %d times, still blocking: %t; want a success on a dial of its own that ran the hook again beside the blocked run", err, runs.Load(), blocking())
			case !tt.redials && (!errors.As(err, &te) || elapsed < 2*connect-coarseTick || elapsed > 2*connect+slack || runs.Load() != 1 || !blocking()):
				t.Errorf("a List while the hook blocks: %v after %v, the hook run %d times; want a *TimeoutError at the %v wait bound behind the held dial", err, elapsed, runs.Load(), 2*connect)
			}

			free()
			if _, err := c.Models().List(t.Context()); err != nil {
				t.Errorf("List after the hook returned: %v", err)
			}
		})
	}
}

// TestClientTraceDialPhaseBoundLatePanic pins what becomes of a hook the
// dial-phase bound left running that then panics: the bound has ended its
// call with a *TimeoutError and the call has returned, so the panic is not
// raised on the caller; the shield recovers it on the goroutine the hook
// runs on (the transport's dial for ConnectStart, net/http's handshake for
// TLSHandshakeStart) and logs the WARN record of a hook that panicked after
// its request returned, naming the hook and its stack, and the client's next
// call succeeds. A shield that did not recover it would crash the test
// binary.
func TestClientTraceDialPhaseBoundLatePanic(t *testing.T) {
	const connect = 250 * time.Millisecond
	tests := map[string]struct{ hook string }{
		"success: a ConnectStart the dial bound left running panics into a WARN record":      {hook: "ConnectStart"},
		"success: a TLSHandshakeStart the wait bound left running panics into a WARN record": {hook: "TLSHandshakeStart"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: apiHandler(t)})
			release := make(chan struct{})
			var once sync.Once
			free := func() { once.Do(func() { close(release) }) }
			t.Cleanup(free)
			var runs atomic.Int32
			logs := testsupport.NewLogRecorder(slog.LevelDebug)
			clearEnv(t)
			c, err := NewClient(WithAPIKey(testKey), WithBaseURL(srv.URL()), WithRootCAs(testsupport.RootCAs(t)), WithProxy(nil),
				WithNoTimeout(), WithConnectTimeout(connect), WithRetry(NoRetry()), WithLogger(logs.Logger()),
				WithClientTrace(dialPhaseTrace(t, tt.hook, release, true, &runs)))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			t.Cleanup(func() { _ = c.Close() })

			var te *TimeoutError
			if p := recovered(func() { _, err = c.Models().List(t.Context()) }); p != nil || !errors.As(err, &te) {
				t.Fatalf("List panicked with %v, returned %v; want the bound's *TimeoutError and no panic", p, err)
			}
			free()
			deadline := time.Now().Add(5 * time.Second)
			for len(logs.At(slog.LevelWarn)) == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			warns := logs.At(slog.LevelWarn)
			if len(warns) != 1 {
				t.Fatalf("%d WARN records after the hook panicked, want 1: %v", len(warns), warns)
			}
			hook, _ := warns[0].Attr("hook")
			stack, _ := warns[0].Attr("stack")
			if warns[0].Message != "transport: trace hook panic after the request returned, recovered" || hook.String() != tt.hook ||
				!strings.Contains(stack.String(), "dialPhaseTrace.func") {
				t.Errorf("WARN %q hook %q, want the late-panic record naming %s with its stack:\n%s", warns[0].Message, hook, tt.hook, stack)
			}
			if p := recovered(func() { _, err = c.Models().List(t.Context()) }); p != nil || err != nil {
				t.Errorf("List after the late panic: panic %v, error %v; want a success", p, err)
			}
		})
	}
}
