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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// dialPhaseTrace returns a trace whose hook named hook blocks until release
// is closed, the first time it runs only.
func dialPhaseTrace(t *testing.T, hook string, release <-chan struct{}) *httptrace.ClientTrace {
	t.Helper()
	var armed atomic.Bool
	armed.Store(true)
	block := func() {
		if armed.CompareAndSwap(true, false) {
			<-release
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

// TestClientTraceDialPhaseBound pins the K28d bound (owner decision G11 (5))
// through the public options, on the SDK's default transport and the real
// net.Dialer over loopback TLS: a WithClientTrace hook that blocks the first
// connection's DNS lookup (a localhost base URL), TCP connect or TLS
// handshake ends the call under WithNoTimeout with a *TimeoutError at the
// bound, while the hook still blocks. The bound is the connect timeout for
// DNSStart and ConnectStart, which run inside the dial, and twice the
// connect timeout for TLSHandshakeStart, the wait bound (on this transport
// the TLS handshake timeout is the connect timeout). The call's INFO
// "request failed" record and the transport's DEBUG record of the bound
// name it. Once the hook returns, the next call succeeds.
//
// The bounds are the configured connect timeout, not a measurement
// (STANDING 9): a call the bound did not end waits until the watchdog frees
// its hook, hold after the start, beyond the upper limit. The clock is real:
// the lower limit allows one coarse tick (K29), and the upper one slack for a
// slow -race runner, far below hold. It runs in CI's -race test step (go
// test -race with coverage) on ubuntu-26.04, xcode-27 and windows-2025.
func TestClientTraceDialPhaseBound(t *testing.T) {
	const (
		connect = 250 * time.Millisecond
		slack   = 2 * time.Second
		hold    = 10 * time.Second
		coarse  = 20 * time.Millisecond
	)
	tests := map[string]struct {
		hook, host string
		bound      time.Duration
		record     string // the transport's DEBUG record of the bound
	}{
		"error: a blocking DNSStart ends the call at the connect timeout": {
			hook: "DNSStart", host: "localhost", bound: connect, record: "h2: dial bound expired",
		},
		"error: a blocking ConnectStart ends the call at the connect timeout": {
			hook: "ConnectStart", host: "127.0.0.1", bound: connect, record: "h2: dial bound expired",
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
			logs := testsupport.NewLogRecorder(slog.LevelDebug)
			clearEnv(t)
			c, err := NewClient(WithAPIKey(testKey), WithBaseURL("https://"+net.JoinHostPort(tt.host, port)), WithRootCAs(testsupport.RootCAs(t)),
				WithProxy(nil), WithNoTimeout(), WithConnectTimeout(connect), WithRetry(NoRetry()), WithLogger(logs.Logger()),
				WithClientTrace(dialPhaseTrace(t, tt.hook, release)))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			t.Cleanup(func() { _ = c.Close() })
			watchdog := time.AfterFunc(hold, free)
			defer watchdog.Stop()

			start := time.Now()
			_, err = c.Models().List(t.Context())
			elapsed := time.Since(start)
			blocking := true
			select {
			case <-release:
				blocking = false
			default:
			}
			var te *TimeoutError
			if !errors.As(err, &te) || te.Timeout != 0 || te.Proxy() || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("List = %T %v, want a *TimeoutError without an attempt timeout", err, err)
			}
			if elapsed < tt.bound-coarse || elapsed > tt.bound+slack || !blocking {
				t.Errorf("List failed after %v, the hook still blocking: %t; want %v (+%v slack) while it blocks", elapsed, blocking, tt.bound, slack)
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

			free()
			if _, err := c.Models().List(t.Context()); err != nil {
				t.Errorf("List after the hook returned: %v", err)
			}
		})
	}
}
