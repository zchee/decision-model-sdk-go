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
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// The AC-P4 constants (docs/perf/frozen-budgets.md, R29c).
const (
	fanN      = 64                    // the cold fan-out width
	fanReps   = 10                    // bursts per run, each on a fresh server and transport
	fanGuard  = 5 * time.Second       // the handler's hold guard
	leadDelay = 20 * time.Millisecond // the leader's response delay (R29c: at least 5 ms)
	f1Calls   = 200                   // the 200-vs-8 case
	f1Limit   = 8                     // its MAX_CONCURRENT_STREAMS
	f1Service = 10 * time.Millisecond // its handler service time
	f1Timeout = 2 * time.Second       // its per-call deadline
)

// checkOrdering checks one cold burst against AC-P4's ordering clause as
// frozen (G2, R29 (4), R29c) and returns the leader's index and the problems
// found: (a) the first request the handler saw is the leader's; (b) the
// client had the leader's first response byte before any other call wrote
// its HEADERS (client-side traces); (c) the guard released nothing; (d)
// every request arrived on connection 0; and every call returned 200.
func checkOrdering(b *barrier, srv *testsupport.LoopbackServer, cold []result) (int, []string) {
	var problems []string
	leader := -1
	for i, r := range cold {
		switch {
		case r.Err != nil:
			problems = append(problems, fmt.Sprintf("call %d: %v", i, r.Err))
		case r.Status != http.StatusOK:
			problems = append(problems, fmt.Sprintf("call %d: status %d", i, r.Status))
		}
		if r.GotConnSet && !r.Reused {
			if leader >= 0 {
				problems = append(problems, fmt.Sprintf("calls %d and %d both got a new connection", leader, i))
			}
			leader = i
		}
	}
	if leader < 0 {
		return -1, append(problems, "no call got a new connection")
	}
	if got, want := b.firstFree(), "/cold/"+strconv.Itoa(leader); got != want {
		problems = append(problems, fmt.Sprintf("(a) first request at the handler %q, want the leader's %q", got, want))
	}
	answered := cold[leader].FirstByte
	for i, r := range cold {
		if i != leader && !r.WroteHeaders.After(answered) {
			problems = append(problems, fmt.Sprintf("(b) call %d wrote HEADERS at %v, not after the leader's first response byte at %v", i, r.WroteHeaders, answered))
			break
		}
	}
	if n := b.guardedCount("cold"); n != 0 {
		problems = append(problems, fmt.Sprintf("(c) the guard released %d handlers", n))
	}
	for _, r := range srv.Requests() {
		if r.Conn != 0 {
			problems = append(problems, fmt.Sprintf("(d) %s arrived on connection %d", r.Path, r.Conn))
			break
		}
	}
	return leader, problems
}

// TestFanOut asserts AC-P4 (NF4) as frozen under option (iv-b): a cold burst
// of 64 calls opens 1 connection, the leader's request is answered first
// (after 20 ms) and every other request is then on the wire, on that
// connection, before any other response; a warm burst opens none; 200 calls
// against MAX_CONCURRENT_STREAMS 8 all succeed within their 2 s deadline on
// 1 connection. Each case runs 10 times on a fresh server and transport.
// Waiter latency is logged, not asserted. The negative control (the plain
// token, which fails the ordering) is TestRecordFanOut's, not asserted here.
func TestFanOut(t *testing.T) {
	t.Run("success: cold 64 on 1 connection with the leader answered first; warm 64 on none", func(t *testing.T) {
		var waiterWire, warmWire, answerGaps, leadGaps []time.Duration
		ok := 0
		for rep := range fanReps {
			b := newBarrier(fanN, fanGuard)
			b.free, b.freeDelay = "cold", leadDelay
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: b})
			tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
			cold := fanOut(fanN, func(i int) result { return get(t.Context(), tr, srv.URL()+"/cold/"+strconv.Itoa(i)) })
			accCold := srv.Accepts()
			warm := fanOut(fanN, func(i int) result { return get(t.Context(), tr, srv.URL()+"/warm/"+strconv.Itoa(i)) })
			accWarm := srv.Accepts()

			leader, problems := checkOrdering(b, srv, cold)
			if accCold != 1 {
				problems = append(problems, fmt.Sprintf("cold burst opened %d connections, want 1", accCold))
			}
			if accWarm != accCold {
				problems = append(problems, fmt.Sprintf("warm burst opened %d connections, want 0", accWarm-accCold))
			}
			if n := b.guardedCount("warm"); n != 0 {
				problems = append(problems, fmt.Sprintf("warm: the guard released %d handlers", n))
			}
			for i, r := range warm {
				if r.Err != nil || r.Status != http.StatusOK {
					problems = append(problems, fmt.Sprintf("warm call %d: %d %v", i, r.Status, r.Err))
					break
				}
				warmWire = append(warmWire, r.WroteHeaders.Sub(r.Start))
			}
			if st := tr.Stats(); st.Dials != 1 || st.Leaders != 1 || st.Releases != 1 || st.FirstHolds != 1 || st.FallThroughs != 0 {
				problems = append(problems, fmt.Sprintf("stats %+v, want 1 dial, 1 leader, 1 release, 1 FirstHold, no fall-through", st))
			}
			if len(problems) > 0 {
				t.Errorf("rep %d:\n%s", rep, joinLines(problems))
				continue
			}
			ok++
			var firstOther time.Time
			for i, r := range cold {
				if i == leader {
					continue
				}
				waiterWire = append(waiterWire, r.WroteHeaders.Sub(r.Start))
				if firstOther.IsZero() || r.WroteHeaders.Before(firstOther) {
					firstOther = r.WroteHeaders
				}
			}
			answerGaps = append(answerGaps, firstOther.Sub(cold[leader].FirstByte))
			leadGaps = append(leadGaps, firstOther.Sub(cold[leader].WroteHeaders))
		}
		record(t, "case", "cold64+warm64", "reps", fanReps, "ordering_ok", fmt.Sprintf("%d/%d", ok, fanReps),
			"lead_delay_ms", ms(leadDelay),
			"waiter_wire_p50_ms", ms(pct(waiterWire, 0.5)), "waiter_wire_p99_ms", ms(pct(waiterWire, 0.99)),
			"warm_wire_p50_ms", ms(pct(warmWire, 0.5)), "warm_wire_p99_ms", ms(pct(warmWire, 0.99)),
			"answer_to_first_waiter_write_min_ms", ms(minDuration(answerGaps)),
			"answer_to_first_waiter_write_p50_ms", ms(pct(answerGaps, 0.5)),
			"leader_write_to_first_waiter_write_p50_ms", ms(pct(leadGaps, 0.5)))
	})

	t.Run("success: 200 calls against MAX_CONCURRENT_STREAMS 8 all succeed within 2 s on 1 connection", func(t *testing.T) {
		var walls []time.Duration
		maxActive, refused := 0, 0
		for rep := range fanReps {
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{MaxConcurrentStreams: f1Limit, Handler: serviceHandler(f1Service)})
			tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
			start := time.Now()
			calls := fanOut(f1Calls, func(i int) result {
				ctx, cancel := context.WithTimeout(t.Context(), f1Timeout)
				defer cancel()
				return get(ctx, tr, srv.URL()+"/"+strconv.Itoa(i))
			})
			walls = append(walls, time.Since(start))
			cl := classes(calls)
			if cl["ok"] != f1Calls || srv.Accepts() != 1 {
				t.Errorf("rep %d: classes %v, accepts %d; want %d ok on 1 connection; first error: %v", rep, cl, srv.Accepts(), f1Calls, firstErr(calls))
			}
			maxActive = max(maxActive, srv.MaxActiveStreams())
			refused += srv.OverLimit()
		}
		// The refused-stream count is recorded, not asserted: the loaded (M)
		// runs of W0.4 saw 1 and 2 with no missed deadline.
		record(t, "case", "200vs8", "reps", fanReps, "deadline_ms", ms(f1Timeout), "service_ms", ms(f1Service),
			"wall_p50_ms", ms(pct(walls, 0.5)), "wall_max_ms", ms(pct(walls, 1)),
			"max_active_streams", maxActive, "refused_streams_total", refused)
	})
}

// joinLines joins problems one per line, indented.
func joinLines(lines []string) string {
	return "  " + strings.Join(lines, "\n  ")
}

// minDuration returns the smallest of ds, or 0.
func minDuration(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	return slices.Min(ds)
}

// gatedTransport builds a default transport for srv whose dials wait at a
// testsupport.GatedDialer until the returned gate channel is closed.
func gatedTransport(t *testing.T, srv *testsupport.LoopbackServer, connect time.Duration) (*Transport, *testsupport.GatedDialer, chan struct{}) {
	t.Helper()
	gate := make(chan struct{})
	gd := testsupport.NewGatedDialer(gate, nil)
	tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL()), ConnectTimeout: connect, DialContext: gd.DialContext})
	return tr, gd, gate
}

// TestLeaderVanish covers the failure-table rows where a leader leaves
// before GotConn without a verdict on the dial: alone (the gate turns cold
// and the next caller leads), with waiters parked (one takes over and the
// detached stock dial serves everyone on one connection), and with a request
// that fails before the transport looks for a connection.
func TestLeaderVanish(t *testing.T) {
	t.Run("success: a lone leader cancelled mid-dial leaves the gate cold and the next caller leads", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		tr, gd, gate := gatedTransport(t, srv, 10*time.Second)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan result, 1)
		go func() { done <- get(ctx, tr, srv.URL()+"/vanish") }()
		waitUntil(t, "the leader's dial at the gate", func() bool { return gd.Waiting() == 1 && tr.gateState() == stateDialing })
		cancel()
		first := <-done
		if !errors.Is(first.Err, context.Canceled) {
			t.Errorf("leader error %v, want context.Canceled", first.Err)
		}
		if got := tr.gateState(); got != stateCold {
			t.Errorf("gate after the lone leader left: %v, want cold", got)
		}
		next := make(chan result, 1)
		go func() { next <- get(t.Context(), tr, srv.URL()+"/next") }()
		waitUntil(t, "the next caller to lead", func() bool { return tr.Stats().Leaders == 2 })
		close(gate) // the detached first dial completes only now
		r := <-next
		st := tr.Stats()
		if r.Err != nil || r.Status != http.StatusOK || st.Leaders != 2 || st.ColdResets != 1 || st.Handovers != 0 || srv.Accepts() != 1 {
			t.Errorf("next %d %v; stats %+v; accepts %d; want 200, 2 leaders, 1 cold reset, 1 accept", r.Status, r.Err, st, srv.Accepts())
		}
		if got := tr.gateState(); got != stateWarm {
			t.Errorf("gate %v, want warm", got)
		}
	})

	t.Run("success: a leader cancelled with 63 waiters hands over and 1 connection serves them", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		tr, gd, gate := gatedTransport(t, srv, 10*time.Second)
		ctx, cancel := context.WithCancel(t.Context())
		leaderDone := make(chan result, 1)
		go func() { leaderDone <- get(ctx, tr, srv.URL()+"/leader") }()
		waitUntil(t, "the leader's dial at the gate", func() bool { return gd.Waiting() == 1 })
		waitersDone := make(chan []result, 1)
		go func() {
			waitersDone <- fanOut(fanN-1, func(i int) result { return get(t.Context(), tr, srv.URL()+"/w/"+strconv.Itoa(i)) })
		}()
		waitUntil(t, "63 parked waiters", func() bool { return tr.parked.Load() == fanN-1 })
		cancel()
		leader := <-leaderDone
		// The dial is still held, so the new leader is a waiter by
		// construction, and it cannot have reached GotConn yet.
		waitUntil(t, "a waiter to take over", func() bool { st := tr.Stats(); return st.Leaders == 2 && st.Handovers == 1 })
		close(gate)
		waiters := <-waitersDone
		cl := classes(waiters)
		st := tr.Stats()
		if !errors.Is(leader.Err, context.Canceled) || cl["ok"] != fanN-1 || srv.Accepts() != 1 || gd.Dials() != 1 {
			t.Errorf("leader %v; waiters %v (first error %v); accepts %d, dials %d; want canceled, 63 ok, 1 accept, 1 dial",
				leader.Err, cl, firstErr(waiters), srv.Accepts(), gd.Dials())
		}
		if st.Leaders != 2 || st.Handovers != 1 || st.Releases != 1 || st.Failures != 0 {
			t.Errorf("stats %+v, want 2 leaders, 1 handover, 1 release, no failure", st)
		}
	})

	t.Run("success: a leader whose request fails before the transport looks for a connection hands over", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
		// Hold the token so the leader parks in send, after taking the
		// leader role and before RoundTrip, while the waiters gather.
		tr.token <- struct{}{}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL()+"/bad", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		req.Header["X-Bad"] = []string{"line\nbreak"} // refused by the stock transport before getConn
		leaderErr := make(chan error, 1)
		go func() {
			resp, err := tr.RoundTrip(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			leaderErr <- err
		}()
		waitUntil(t, "the leader to take its role", func() bool { return tr.Stats().Leaders == 1 })
		waitersDone := make(chan []result, 1)
		go func() {
			waitersDone <- fanOut(fanN-1, func(i int) result { return get(t.Context(), tr, srv.URL()+"/w/"+strconv.Itoa(i)) })
		}()
		waitUntil(t, "63 parked waiters", func() bool { return tr.parked.Load() == fanN-1 })
		<-tr.token
		err = <-leaderErr
		waiters := <-waitersDone
		var de *DialError
		if err == nil || errors.As(err, &de) {
			t.Errorf("leader error %v (%T), want the transport's header refusal, not a *DialError", err, err)
		}
		st := tr.Stats()
		if cl := classes(waiters); cl["ok"] != fanN-1 || srv.Accepts() != 1 || st.Handovers != 1 || st.Failures != 0 {
			t.Errorf("waiters %v (first error %v); accepts %d; stats %+v; want 63 ok on 1 connection, 1 handover, no failure",
				cl, firstErr(waiters), srv.Accepts(), st)
		}
	})
}

// TestWaiterFallThrough covers the two bounds that keep the gate from
// wedging the client (K19, R29 (3)): the FirstHold bound, after which the
// token is given back while the first response is still outstanding, and
// the gate's wait bound, after which a waiter calls the transport itself.
// Both leave every call on one connection.
func TestWaiterFallThrough(t *testing.T) {
	t.Run("success: a hung first response holds the waiters for the hold bound only", func(t *testing.T) {
		const connect = 250 * time.Millisecond // hold bound = connect + handshake = 500 ms
		var mu sync.Mutex
		held := ""
		releaseLeader := make(chan struct{})
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			first := held == ""
			if first {
				held = r.URL.Path
			}
			mu.Unlock()
			if first {
				// The first response hangs (the connection stays healthy: the
				// server answers PINGs) until every other call has returned
				// on the client.
				select {
				case <-releaseLeader:
				case <-r.Context().Done():
					return
				}
			}
			w.WriteHeader(http.StatusOK)
		})})
		tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL()), ConnectTimeout: connect})
		if tr.holdBound != 2*connect {
			t.Fatalf("hold bound %v, want %v", tr.holdBound, 2*connect)
		}
		var others sync.WaitGroup
		others.Add(fanN - 1)
		go func() {
			others.Wait()
			close(releaseLeader)
		}()
		calls := fanOut(fanN, func(i int) result {
			// No per-call deadline: the SDK's WithNoTimeout.
			r := get(t.Context(), tr, srv.URL()+"/hold/"+strconv.Itoa(i))
			if !r.GotConnSet || r.Reused {
				others.Done()
			}
			return r
		})
		leader := -1
		for i, r := range calls {
			if r.GotConnSet && !r.Reused {
				leader = i
			}
		}
		if leader < 0 {
			t.Fatalf("no leader; first error %v", firstErr(calls))
		}
		mu.Lock()
		heldPath := held
		mu.Unlock()
		l := calls[leader]
		var minGap time.Duration = -1
		lastWaiterDone := time.Time{}
		for i, r := range calls {
			if i == leader {
				continue
			}
			if gap := r.WroteHeaders.Sub(l.WroteHeaders); minGap < 0 || gap < minGap {
				minGap = gap
			}
			if r.Done.After(lastWaiterDone) {
				lastWaiterDone = r.Done
			}
		}
		st := tr.Stats()
		record(t, "case", "firsthold-bound", "hold_bound_ms", ms(tr.holdBound), "leader_write_to_first_waiter_write_ms", ms(minGap),
			"last_waiter_done_ms", ms(lastWaiterDone.Sub(l.Start)), "leader_done_ms", ms(l.Done.Sub(l.Start)), "accepts", srv.Accepts())
		if cl := classes(calls); cl["ok"] != fanN || srv.Accepts() != 1 || heldPath != "/hold/"+strconv.Itoa(leader) {
			t.Errorf("classes %v (first error %v), accepts %d, held %q, leader %d; want 64 ok on 1 connection, the leader's request held",
				cl, firstErr(calls), srv.Accepts(), heldPath, leader)
		}
		// 5 ms of slack: the Transport arms the bound in its WroteHeaders
		// hook, which runs before the test's.
		if minGap < tr.holdBound-5*time.Millisecond || !lastWaiterDone.Before(l.Done) {
			t.Errorf("first waiter HEADERS %v after the leader's (want at least the %v bound); last waiter done at %v, leader done at %v",
				minGap, tr.holdBound, lastWaiterDone.Sub(l.Start), l.Done.Sub(l.Start))
		}
		if st.FirstHolds != 1 || st.HoldExpiries != 1 {
			t.Errorf("stats %+v, want 1 FirstHold ended by the bound", st)
		}
	})

	t.Run("success: at the default bounds on fake time, a first response hung 60 s holds the waiters 20 s", testHoldBoundOnFakeTime)

	t.Run("success: a leader's dial outlasts the wait bound; the waiters fall through and share its connection", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		tr, gd, gate := gatedTransport(t, srv, 10*time.Second)
		// The default bounds equal the dial's own (connect + handshake), so
		// a held dial would fail first; a short wait bound isolates the
		// fall-through path.
		tr.waitBound = 200 * time.Millisecond
		callsDone := make(chan []result, 1)
		go func() {
			callsDone <- fanOut(fanN, func(i int) result { return get(t.Context(), tr, srv.URL()+"/"+strconv.Itoa(i)) })
		}()
		waitUntil(t, "63 fall-throughs", func() bool { return tr.Stats().FallThroughs == fanN-1 })
		close(gate) // the leader's dial completes only after every waiter fell through
		calls := <-callsDone
		st := tr.Stats()
		if cl := classes(calls); cl["ok"] != fanN || srv.Accepts() != 1 || gd.Dials() != 1 || st.Leaders != 1 {
			t.Errorf("classes %v (first error %v), accepts %d, dials %d, stats %+v; want 64 ok on 1 connection, 1 leader",
				cl, firstErr(calls), srv.Accepts(), gd.Dials(), st)
		}
	})
}

// TestTLSSilentPeer points the transport at a listener that accepts TCP and
// never answers TLS: the leader's handshake fails after the connect timeout
// (TLSHandshakeTimeout), the leader and every waiter get a fresh
// *DialError{Timeout: true} around the same cause (R19), the gate is cold,
// and the listener saw one connection.
func TestTLSSilentPeer(t *testing.T) {
	const connect = 500 * time.Millisecond
	l := testsupport.NewSilentListener(t)
	tr := newTestTransport(t, Config{APIURL: mustURL(t, l.URL()), ConnectTimeout: connect})
	start := time.Now()
	calls := fanOut(fanN, func(i int) result { return get(t.Context(), tr, l.URL()+"/"+strconv.Itoa(i)) })
	elapsed := time.Since(start)
	var values []*DialError
	for i, r := range calls {
		de, ok := errors.AsType[*DialError](r.Err)
		if !ok {
			t.Fatalf("call %d: error %v (%T) is not a *DialError", i, r.Err, r.Err)
		}
		if !de.Timeout || de.Proxy || errors.Is(de, ErrNotNegotiated) {
			t.Errorf("call %d: flags proxy=%t timeout=%t not-negotiated=%t, want a timeout only", i, de.Proxy, de.Timeout, errors.Is(de, ErrNotNegotiated))
		}
		if r.GotConnSet {
			t.Errorf("call %d reached GotConn", i)
		}
		values = append(values, de)
	}
	// Every caller, the leader included, holds its own value around the one
	// cause the leader's dial produced.
	distinct := map[*DialError]bool{}
	sameCause := 0
	for _, de := range values {
		distinct[de] = true
		if de.Err == values[0].Err { //nolint:errorlint // identity of the shared cause is the assertion
			sameCause++
		}
	}
	st := tr.Stats()
	record(t, "case", "tls-silent", "connect_timeout_ms", ms(connect), "elapsed_ms", ms(elapsed), "accepts", l.Accepts(),
		"stats", fmt.Sprintf("%+v", st), "chain", chain(values[0]))
	if len(distinct) != fanN || sameCause != fanN || l.Accepts() != 1 || tr.gateState() != stateCold {
		t.Errorf("distinct values %d, sharing the cause %d, accepts %d, gate %v; want %d, %d, 1, cold",
			len(distinct), sameCause, l.Accepts(), tr.gateState(), fanN, fanN)
	}
	if st.Leaders != 1 || st.Failures != 1 || st.ColdResets != 1 || st.FallThroughs != 0 {
		t.Errorf("stats %+v, want 1 leader, 1 failure, 1 cold reset, no fall-through", st)
	}
	if elapsed < connect || elapsed > connect+time.Second {
		t.Errorf("elapsed %v, want about the %v connect timeout", elapsed, connect)
	}
}
