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
	"net"
	"net/http"
	"net/http/httptrace"
	"testing"
	"time"
)

// fakeConns returns n distinct connections for fake GotConn events.
func fakeConns(t *testing.T, n int) []net.Conn {
	t.Helper()
	conns := make([]net.Conn, n)
	for i := range conns {
		a, b := net.Pipe()
		t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
		conns[i] = a
	}
	return conns
}

// settleTransport is a Transport over a stock transport that is never used:
// the tests drive the httptrace hooks by hand. With h2 set every connection
// counts as HTTP/2 (prior knowledge), otherwise a pipe, which has no TLS
// state, counts as HTTP/1.1.
func settleTransport(t *testing.T, h2 bool, hold time.Duration) *Transport {
	t.Helper()
	mode := HTTPAuto
	if h2 {
		mode = HTTP2Only
	}
	return newTransport(&http.Transport{Protocols: protocols(mode, "http")}, settings{mode: mode, waitBound: time.Second, holdBound: hold})
}

// take is a request that took the token, as send does before RoundTrip.
func take(t *testing.T, tr *Transport) *call {
	t.Helper()
	select {
	case tr.token <- struct{}{}:
	case <-time.After(time.Second):
		t.Fatal("the token is still held")
	}
	return &call{t: tr, ctx: t.Context()}
}

// replay is a request that wrote its HEADERS on the warm connection old,
// gave the token back, and was replayed by the stock transport onto conn.
func replay(t *testing.T, tr *Transport, old, conn net.Conn, reused bool) {
	t.Helper()
	c := take(t, tr)
	c.gotConn(httptrace.GotConnInfo{Conn: old, Reused: true})
	c.wroteHeaders()
	c.gotConn(httptrace.GotConnInfo{Conn: conn, Reused: reused})
	c.wroteHeaders()
	c.giveBack(false)
}

// holder sends a request on conn (Reused, as a warm connection is) and
// reports whether it kept the token past its HEADERS. It gives the token
// back before it returns, as RoundTrip's end does.
func holder(t *testing.T, tr *Transport, conn net.Conn) bool {
	t.Helper()
	c := take(t, tr)
	c.gotConn(httptrace.GotConnInfo{Conn: conn, Reused: true})
	c.wroteHeaders()
	held := len(tr.token) == 1
	c.giveBack(false)
	if tm := c.hold.Load(); tm != nil {
		tm.Stop()
	}
	return held
}

// TestSettleHold drives the K21c hooks (R69) with fake GotConn sequences: a
// connection a stock replay opened after giving the token back is marked
// unsettled, and the next token holder there keeps the token until its
// response headers, once; a connection without a mark is unaffected.
func TestSettleHold(t *testing.T) {
	t.Run("success: a replay then a holder: the holder keeps the token, the next one does not", func(t *testing.T) {
		tr := settleTransport(t, true, time.Minute)
		cs := fakeConns(t, 2)
		old, fresh := cs[0], cs[1]
		replay(t, tr, old, fresh, false)
		if n := tr.nUnsettled.Load(); n != 1 {
			t.Fatalf("unsettled connections %d after the replay, want 1", n)
		}
		if !holder(t, tr, fresh) {
			t.Error("the first holder on the replay's connection gave the token back at its HEADERS")
		}
		if holder(t, tr, fresh) {
			t.Error("the second holder on it kept the token: the mark was not cleared")
		}
		if st := tr.Stats(); st.SettleHolds != 1 || st.FirstHolds != 1 || tr.nUnsettled.Load() != 0 {
			t.Errorf("stats %+v, marks %d; want 1 settle hold, no mark left", st, tr.nUnsettled.Load())
		}
	})

	t.Run("success: two replays on one connection mark it once; a replay on another marks that one", func(t *testing.T) {
		tr := settleTransport(t, true, time.Minute)
		cs := fakeConns(t, 3)
		old, a, b := cs[0], cs[1], cs[2]
		replay(t, tr, old, a, false) // the first replay onto a gets Reused == false
		replay(t, tr, old, a, true)  // the second finds a in the pool
		replay(t, tr, old, b, false)
		if n := tr.nUnsettled.Load(); n != 2 {
			t.Fatalf("unsettled connections %d, want 2 (a and b)", n)
		}
		got := []bool{holder(t, tr, a), holder(t, tr, b), holder(t, tr, a), holder(t, tr, b)}
		want := []bool{true, true, false, false}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("holders kept the token %v, want %v", got, want)
				break
			}
		}
		if st := tr.Stats(); st.SettleHolds != 2 {
			t.Errorf("settle holds %d, want 2", st.SettleHolds)
		}
	})

	t.Run("success: a holder on an already-settled connection is unaffected", func(t *testing.T) {
		tr := settleTransport(t, true, time.Minute)
		cs := fakeConns(t, 3)
		old, marked, settled := cs[0], cs[1], cs[2]
		replay(t, tr, old, marked, false)
		if holder(t, tr, settled) {
			t.Error("a holder on a connection without a mark kept the token")
		}
		if n := tr.nUnsettled.Load(); n != 1 {
			t.Errorf("unsettled connections %d, want the other connection's mark kept", n)
		}
	})

	t.Run("success: a hold that its bound ends clears the mark", func(t *testing.T) {
		const bound = 20 * time.Millisecond
		tr := settleTransport(t, true, bound)
		cs := fakeConns(t, 2)
		old, fresh := cs[0], cs[1]
		replay(t, tr, old, fresh, false)
		c := take(t, tr)
		c.gotConn(httptrace.GotConnInfo{Conn: fresh, Reused: true})
		c.wroteHeaders()
		waitUntil(t, "the hold bound to give the token back", func() bool { return len(tr.token) == 0 })
		c.giveBack(false) // RoundTrip's end, after the bound: a no-op
		if st := tr.Stats(); st.HoldExpiries != 1 || st.SettleHolds != 1 {
			t.Errorf("stats %+v, want 1 settle hold ended by the bound", st)
		}
		if holder(t, tr, fresh) {
			t.Error("a holder after the expired hold kept the token: the mark was not cleared")
		}
	})

	t.Run("success: a replay onto an HTTP/1.1 connection marks nothing", func(t *testing.T) {
		tr := settleTransport(t, false, time.Minute)
		cs := fakeConns(t, 2)
		replay(t, tr, cs[0], cs[1], false)
		if n := tr.nUnsettled.Load(); n != 0 || holder(t, tr, cs[1]) {
			t.Errorf("marks %d, or a holder kept the token on an HTTP/1.1 connection", n)
		}
	})

	t.Run("success: at most maxUnsettled marks are kept, the oldest goes first", func(t *testing.T) {
		tr := settleTransport(t, true, time.Minute)
		cs := fakeConns(t, maxUnsettled+2)
		old := cs[0]
		for _, c := range cs[1:] {
			replay(t, tr, old, c, false)
		}
		if n := tr.nUnsettled.Load(); n != maxUnsettled {
			t.Fatalf("marks %d, want %d", n, maxUnsettled)
		}
		if holder(t, tr, cs[1]) || !holder(t, tr, cs[2]) || !holder(t, tr, cs[len(cs)-1]) {
			t.Error("want the oldest mark evicted and the others kept")
		}
	})

	t.Run("success: a second WroteHeaders replaces the hold bound; a replay onto a second new connection counts once", func(t *testing.T) {
		tr := settleTransport(t, true, time.Minute)
		cs := fakeConns(t, 2)
		c := take(t, tr)
		c.gotConn(httptrace.GotConnInfo{Conn: cs[0], Reused: false})
		c.wroteHeaders()
		first := c.hold.Load()
		c.gotConn(httptrace.GotConnInfo{Conn: cs[1], Reused: false}) // the stock transport replays it, token still held
		c.wroteHeaders()
		if second := c.hold.Load(); second == nil || second == first {
			t.Fatal("the second WroteHeaders armed no new bound")
		}
		if first.Stop() {
			t.Error("the first bound was still armed after the second WroteHeaders")
		}
		c.finish()
		if st := tr.Stats(); st.FirstHolds != 1 || len(tr.token) != 0 {
			t.Errorf("stats %+v, token %d; want 1 FirstHold for the one request and the token free", st, len(tr.token))
		}
	})

	t.Run("success: a WroteHeaders after the token went back arms no bound", func(t *testing.T) {
		tr := settleTransport(t, true, time.Minute)
		cs := fakeConns(t, 1)
		c := take(t, tr)
		c.gotConn(httptrace.GotConnInfo{Conn: cs[0], Reused: false})
		c.finish() // send returned before the transport's write goroutine wrote the HEADERS
		c.wroteHeaders()
		if tm := c.hold.Load(); tm != nil {
			t.Errorf("a bound was armed after the token went back")
		}
	})

	t.Run("success: a request that still holds the token on a new connection is a plain FirstHold", func(t *testing.T) {
		tr := settleTransport(t, true, time.Minute)
		cs := fakeConns(t, 1)
		c := take(t, tr)
		c.gotConn(httptrace.GotConnInfo{Conn: cs[0], Reused: false})
		c.wroteHeaders()
		if len(tr.token) != 1 || tr.nUnsettled.Load() != 0 {
			t.Errorf("token held %t, marks %d; want a FirstHold and no mark", len(tr.token) == 1, tr.nUnsettled.Load())
		}
		c.giveBack(false)
		if tm := c.hold.Load(); tm != nil {
			tm.Stop()
		}
		if st := tr.Stats(); st.FirstHolds != 1 || st.SettleHolds != 0 {
			t.Errorf("stats %+v", st)
		}
	})
}
