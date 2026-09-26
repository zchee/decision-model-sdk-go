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
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// The K21b constants.
const (
	k21Limit    = 8               // the MAX_CONCURRENT_STREAMS the server advertises
	k21Queued   = 64              // callers queued behind the 8 in flight
	k21Deadline = 2 * time.Second // the per-call deadline
	// k21Slack is how far past its deadline a call may return, and k21Probe
	// how long a probe may take to write its HEADERS, on any host; a host
	// that is slow now gets more (k21Control).
	k21Slack = 100 * time.Millisecond
	k21Probe = 50 * time.Millisecond
	// k21Factor scales the control's times to the scenario, which runs 72
	// calls where the control runs 8.
	k21Factor = 4
)

// k21Control measures how fast this host is now, through a stock net/http
// HTTP/2 transport on a server of its own (so the scenario's counts are not
// touched): probe, how long a request on a warm connection takes to write
// its HEADERS, and overshoot, how far past its deadline the latest of 8
// calls that the server never answers returns (each with a 50 ms deadline).
// The scenario's bounds scale with them: a runner that is slow or loaded
// now is as slow in the control as in the scenario (critic-p2 n-2; the rule
// of ruling K30 for durations measured on a host). The control runs no
// h2gate code, so a delay the Transport adds, to every call or only to one
// that timed out, lengthens the scenario's calls and leaves the bounds
// alone (D-W6.1-n2-major: calibrated through the Transport under test, a
// 300 ms linger on a timed-out call raised the slack to about 1.2 s and
// passed).
func k21Control(t *testing.T) (probe, overshoot time.Duration) {
	t.Helper()
	release := make(chan struct{})
	srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: k21Handler(release)})
	defer close(release)
	var h2 http.Protocols
	h2.SetHTTP2(true)
	stock := &http.Transport{Protocols: &h2, TLSClientConfig: testsupport.ClientTLSConfig(t)}
	defer stock.CloseIdleConnections()
	if w := get(t.Context(), stock, srv.URL()+"/warm"); w.Err != nil || w.Status != http.StatusOK || w.ProtoMajor != 2 {
		t.Fatalf("control warm-up: %d HTTP/%d %v", w.Status, w.ProtoMajor, w.Err)
	}
	p := get(t.Context(), stock, srv.URL()+"/probe")
	if p.Err != nil || p.WroteHeaders.IsZero() {
		t.Fatalf("control probe: %v", p.Err)
	}
	const deadline = 50 * time.Millisecond
	calls := fanOut(k21Limit, func(i int) result {
		ctx, cancel := context.WithTimeout(t.Context(), deadline)
		defer cancel()
		return get(ctx, stock, srv.URL()+"/hold/control-"+strconv.Itoa(i))
	})
	for i, r := range calls {
		if errClass(r.Err) != "timeout" {
			t.Fatalf("control call %d: %s, want its deadline to end it", i, chain(r.Err))
		}
		overshoot = max(overshoot, r.Done.Sub(r.Start)-deadline)
	}
	return p.WroteHeaders.Sub(p.Start), overshoot
}

// k21Handler holds /hold/ paths until release is closed, serves /svc/ paths
// in f1Service and everything else at once.
func k21Handler(release <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/hold/"):
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		case strings.HasPrefix(r.URL.Path, "/svc/"):
			serviceHandler(f1Service).ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// k21Burst sends n calls to prefix/i, each with the K21b deadline.
func k21Burst(t *testing.T, tr *Transport, base, prefix string, n int) []result {
	t.Helper()
	return fanOut(n, func(i int) result {
		ctx, cancel := context.WithTimeout(t.Context(), k21Deadline)
		defer cancel()
		return get(ctx, tr, base+prefix+strconv.Itoa(i))
	})
}

// TestTokenResidualK21 drives the stock transport's own retries, which write
// their HEADERS without the token (K21b: GOAWAY replays, REFUSED_STREAM
// retries, a limit lowered mid-connection), with 8 streams in flight and 64
// callers queued against a limit of 8. Whatever the retries do, the design
// must stay bounded: no call returns later than its deadline + 100 ms, every
// error is a timeout or a connection error, the token is free once every
// RoundTrip has returned (a probe's HEADERS leave within 50 ms, and before
// the probe's own deadline, ordered by traceSeq: a token still held, leaked
// or kept by a hold timer, whose bound is 20 s, would keep it past that
// deadline), the client opens at most 2 connections, and a fresh burst of
// 200 calls against the limit of 8 afterwards succeeds 200/200. The 100 ms
// and 50 ms grow on a host the control (k21Control, which runs no h2gate
// code) finds slow now.
func TestTokenResidualK21(t *testing.T) {
	type scenario struct {
		prefix string // the burst's paths
		// refuseAbove, when non-zero, resets streams above that many open
		// ones with REFUSED_STREAM while the server advertises the limit.
		refuseAbove int
		// act runs once 8 streams are in flight on the first connection.
		act func(t *testing.T, srv *testsupport.LoopbackServer, conn *testsupport.H2Conn)
		// goAway is set when act ends the first connection with GOAWAY.
		goAway bool
	}
	tests := map[string]scenario{
		"success: GOAWAY(LastStreamID=3) with 8 in flight and 64 queued": {
			prefix: "/hold/",
			goAway: true,
			act: func(t *testing.T, srv *testsupport.LoopbackServer, conn *testsupport.H2Conn) {
				if err := conn.GoAway(3, testsupport.CodeNoError); err != nil { // stream 1 was the warm-up
					t.Error(err)
				}
				// Let the replays reach the second connection, but never wait
				// past the deadlines.
				until := time.Now().Add(time.Second)
				for srv.Accepts() < 2 && time.Now().Before(until) {
					time.Sleep(time.Millisecond)
				}
			},
		},
		"success: REFUSED_STREAM above 4 open streams while advertising 8": {
			prefix:      "/svc/",
			refuseAbove: 4,
		},
		"success: SETTINGS lowers the limit from 8 to 2 with 8 in flight": {
			prefix: "/hold/",
			act: func(t *testing.T, _ *testsupport.LoopbackServer, conn *testsupport.H2Conn) {
				if err := conn.SetMaxConcurrentStreams(2); err != nil {
					t.Error(err)
				}
			},
		},
	}
	for name, sc := range tests {
		t.Run(name, func(t *testing.T) {
			controlProbe, overshoot := k21Control(t)
			slack := max(k21Slack, k21Factor*overshoot)
			probeBound := max(k21Probe, k21Factor*controlProbe)
			release := make(chan struct{})
			var adversarial atomic.Bool
			adversarial.Store(sc.refuseAbove > 0)
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{
				MaxConcurrentStreams: k21Limit,
				Handler:              k21Handler(release),
				OnStream: func(s *testsupport.Stream) testsupport.Action {
					if adversarial.Load() && len(s.Conn.ActiveStreams()) >= sc.refuseAbove {
						return testsupport.ActionRefuse
					}
					return testsupport.ActionServe
				},
			})
			tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
			warmUp(t, tr, srv.URL())

			done := make(chan []result, 1)
			go func() { done <- k21Burst(t, tr, srv.URL(), sc.prefix, k21Limit+k21Queued) }()
			if sc.act != nil {
				conn := heldStreams(t, srv, k21Limit)
				sc.act(t, srv, conn)
			}
			close(release)
			calls := <-done
			adversarial.Store(false)

			late, bad := 0, 0
			for i, r := range calls {
				if took := r.Done.Sub(r.Start); took > k21Deadline+slack {
					late++
					t.Errorf("call %d returned after %v, past its %v deadline + %v", i, took, k21Deadline, slack)
				}
				switch c := errClass(r.Err); c {
				case "ok":
					if r.Status != http.StatusOK {
						bad++
						t.Errorf("call %d: status %d", i, r.Status)
					}
				case "timeout", "connection":
				default:
					bad++
					t.Errorf("call %d: error %s of class %s, want a timeout or a connection error", i, chain(r.Err), c)
				}
			}
			if n := len(tr.token); n != 0 {
				t.Errorf("token held after every RoundTrip returned: %d", n)
			}
			probeCtx, cancel := context.WithTimeout(t.Context(), k21Deadline)
			var expiredSeq atomic.Uint64 // traceSeq when the probe's deadline passed
			stop := context.AfterFunc(probeCtx, func() { expiredSeq.Store(traceSeq.Add(1)) })
			probe := get(probeCtx, tr, srv.URL()+"/probe")
			stop()
			cancel()
			var took time.Duration // zero when the probe wrote no HEADERS
			if !probe.WroteHeaders.IsZero() {
				took = probe.WroteHeaders.Sub(probe.Start)
			}
			if exp := expiredSeq.Load(); probe.Err != nil || probe.WroteHeadersSeq == 0 || (exp != 0 && exp < probe.WroteHeadersSeq) || took > probeBound {
				t.Errorf("probe: %v, HEADERS after %v at traceSeq %d (0: none), deadline at %d; want them within %v and before the deadline", probe.Err, took, probe.WroteHeadersSeq, exp, probeBound)
			}
			scenarioAccepts := srv.Accepts()
			refused := 0
			for _, r := range srv.Requests() {
				if r.Action == testsupport.ActionRefuse {
					refused++
				}
			}

			// A fresh 200-vs-8 burst on the same transport, with the server
			// behaving again (a lowered limit is raised back to 8). After
			// GOAWAY the first connection closes once its last stream is
			// done, which can be after every call returned: a connection
			// whose close has begun serves no new stream, so it is skipped
			// and counted.
			restoreSkipped := 0
			for _, c := range srv.LiveH2Conns() {
				switch err := c.SetMaxConcurrentStreams(k21Limit); {
				case errors.Is(err, testsupport.ErrConnClosing):
					restoreSkipped++
				case err != nil:
					t.Error(err)
				}
			}
			fresh := k21Burst(t, tr, srv.URL(), "/svc/fresh-", f1Calls)
			freshCl := classes(fresh)
			if freshCl["ok"] != f1Calls {
				t.Errorf("fresh burst: %v, want %d ok; first error %v", freshCl, f1Calls, firstErr(fresh))
			}
			if srv.Accepts() > 2 {
				t.Errorf("accepts %d, want at most 2", srv.Accepts())
			}
			if sc.goAway {
				closedGracefully(t, srv, 0, true, true)
			}
			record(t, "case", "k21/"+strings.Fields(name)[1], "classes", fmt.Sprint(classes(calls)), "late", late, "bad", bad,
				"accepts_scenario", scenarioAccepts, "accepts_total", srv.Accepts(), "refused_streams", refused,
				"probe_headers_ms", ms(took), "probe_bound_ms", ms(probeBound), "slack_ms", ms(slack), "restore_skipped", restoreSkipped, "fresh", fmt.Sprint(freshCl),
				"stats", fmt.Sprintf("%+v", tr.Stats()))
		})
	}
}
