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
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// errPanic is what the test hooks panic with.
const errPanic = "h2gate test: the caller's hook panics"

// panicking sends one request through tr whose context carries hooks that
// may panic, and returns what the panic carried (nil when none unwound out
// of RoundTrip). The caller of an SDK that recovers, as net/http's server
// does for a handler, keeps using the transport afterwards.
func panicking(ctx context.Context, tr *Transport, rawURL string, trace *httptrace.ClientTrace) (recovered any) {
	defer func() { recovered = recover() }()
	if trace != nil {
		ctx = httptrace.WithClientTrace(ctx, trace)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := tr.RoundTrip(req)
	if err == nil {
		_ = resp.Body.Close()
	}
	return nil
}

// afterPanic checks that a panic left nothing behind: the token is free,
// the gate is not dialing and nobody is parked, and a call with a 2 s
// deadline succeeds.
func afterPanic(t *testing.T, tr *Transport, rawURL string) {
	t.Helper()
	if n := len(tr.token); n != 0 {
		t.Errorf("token held after the panic: %d", n)
	}
	if s := tr.gateState(); s == stateDialing {
		t.Errorf("gate %v after the panic, want cold or warm", s)
	}
	if n := tr.parked.Load(); n != 0 {
		t.Errorf("%d waiters parked after the panic", n)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if r := get(ctx, tr, rawURL); r.Err != nil || r.Status != http.StatusOK {
		t.Errorf("call after the panic: %d %v, want 200 within 2 s", r.Status, r.Err)
	}
}

// TestPanicUnwind covers a panic raised inside the stock RoundTrip by the
// caller's code (a trace hook composed through the request context, a Proxy
// func) and recovered by the caller: the panic reaches the caller unchanged,
// the token goes back, and a leader's generation is resolved as a leader
// that left (handover with waiters, cold without), so the next call does
// not park or block (review W2.2A MAJOR 1, R72).
func TestPanicUnwind(t *testing.T) {
	// The stock transport is not panic-safe for a hook it calls after
	// ReserveNewRequest (K28, K28b). On a warm HTTP/2 connection the pool
	// calls GetConn with its mutex held (internal/http2/client_conn_pool.go:
	// 52-61): a panic there leaves the pool locked and every later request
	// blocks on it. GotConn runs with no lock held
	// (internal/http2/transport.go:3151-3166) but after the stream was
	// reserved, and only cc.RoundTrip releases the reservation (:423-425):
	// each panic there leaks one stream slot, so with a server limit of 2,
	// two recovered panics leave the client at the limit although the token
	// is free. No wrapper around *http.Transport repairs either, and the
	// root package shields every caller hook instead, the WithClientTrace
	// option's and those on the call's context (K28c). This
	// test pins what h2gate owns, the token and the gate, so its warm case
	// panics in GotConn once, on a server without a limit.
	t.Run("success: a panicking hook on a warm transport leaves the token free", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
		warmUp(t, tr, srv.URL())
		got := panicking(t.Context(), tr, srv.URL()+"/panic", &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { panic(errPanic) }})
		if got != errPanic {
			t.Fatalf("recovered %v, want the hook's panic", got)
		}
		afterPanic(t, tr, srv.URL()+"/after")
	})

	t.Run("success: a leader's panicking Proxy func leaves the gate cold", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		var calls atomic.Int64
		proxy := func(*http.Request) (*url.URL, error) {
			if calls.Add(1) == 1 {
				panic(errPanic)
			}
			return nil, nil
		}
		tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL()), Proxy: proxy})
		if got := panicking(t.Context(), tr, srv.URL()+"/panic", nil); got != errPanic {
			t.Fatalf("recovered %v, want the Proxy func's panic", got)
		}
		if s := tr.gateState(); s != stateCold {
			t.Errorf("gate %v after the lone leader's panic, want cold", s)
		}
		afterPanic(t, tr, srv.URL()+"/after")
		if st := tr.Stats(); st.Leaders != 2 || st.ColdResets != 1 {
			t.Errorf("stats %+v, want the next caller to lead after a cold reset", st)
		}
	})

	t.Run("success: a leader's panicking hook with 63 waiters hands over, 1 connection", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
		tr.token <- struct{}{} // the leader parks in send until the waiters gather
		recovered := make(chan any, 1)
		go func() {
			recovered <- panicking(t.Context(), tr, srv.URL()+"/panic", &httptrace.ClientTrace{GetConn: func(string) { panic(errPanic) }})
		}()
		waitUntil(t, "the leader to take its role", func() bool { return tr.Stats().Leaders == 1 })
		waitersDone := make(chan []result, 1)
		go func() {
			waitersDone <- fanOut(fanN-1, func(i int) result {
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()
				return get(ctx, tr, srv.URL()+"/w/"+strconv.Itoa(i))
			})
		}()
		waitUntil(t, "63 parked waiters", func() bool { return tr.parked.Load() == fanN-1 })
		<-tr.token
		if got := <-recovered; got != errPanic {
			t.Fatalf("recovered %v, want the hook's panic", got)
		}
		waiters := <-waitersDone
		st := tr.Stats()
		if cl := classes(waiters); cl["ok"] != fanN-1 || srv.Accepts() != 1 || st.Handovers != 1 {
			t.Errorf("waiters %v (first error %v), accepts %d, stats %+v; want 63 ok on 1 connection after 1 handover",
				cl, firstErr(waiters), srv.Accepts(), st)
		}
		afterPanic(t, tr, srv.URL()+"/after")
	})
}
