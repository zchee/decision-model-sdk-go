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
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// TestAutoNoSerialisation covers HTTPAuto: no ALPN refusal, no connection
// cap, and no serialisation onto one connection when the server speaks
// HTTP/1.1 (the token is given back at GotConn on an HTTP/1.1 connection and
// FirstHold never engages there, K21b); the gate stays, and the redundant
// dials of its release window against an h2 server are counted, not
// asserted (K20, accepted).
func TestAutoNoSerialisation(t *testing.T) {
	for name, mode := range map[string]testsupport.ALPN{"no ALPN": testsupport.ALPNNone, "http/1.1 only": testsupport.ALPNHTTP1Only} {
		t.Run("success: a server with "+name+" answers over HTTP/1.1", func(t *testing.T) {
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: mode})
			tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL()), Mode: HTTPAuto})
			r := get(t.Context(), tr, srv.URL()+"/")
			if r.Err != nil || r.Status != http.StatusOK || r.ProtoMajor != 1 {
				t.Errorf("%d HTTP/%d %v, want 200 over HTTP/1.1", r.Status, r.ProtoMajor, r.Err)
			}
		})
	}

	t.Run("success: 8 concurrent requests to an HTTP/1.1 server use 8 connections", func(t *testing.T) {
		const n = 8
		b := newBarrier(n, fanGuard) // every response waits until all 8 requests are in handlers at once
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: testsupport.ALPNHTTP1Only, Handler: b})
		tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL()), Mode: HTTPAuto})
		start := time.Now()
		calls := fanOut(n, func(i int) result { return get(t.Context(), tr, srv.URL()+"/c/"+strconv.Itoa(i)) })
		wall := time.Since(start)
		for i, r := range calls {
			if r.Err != nil || r.Status != http.StatusOK || r.ProtoMajor != 1 {
				t.Errorf("call %d: %d HTTP/%d %v", i, r.Status, r.ProtoMajor, r.Err)
			}
		}
		st := tr.Stats()
		if b.guardedCount("c") != 0 || srv.Accepts() != n || st.FirstHolds != 0 || st.Leaders != 1 {
			t.Errorf("guard released %d, accepts %d, stats %+v; want all 8 in handlers at once on 8 connections, no FirstHold, 1 leader",
				b.guardedCount("c"), srv.Accepts(), st)
		}
		// The token is taken before RoundTrip, when the protocol is not yet
		// known, so new HTTP/1.1 connections are dialed one at a time.
		record(t, "case", "auto-h1-8", "accepts", srv.Accepts(), "wall_ms", ms(wall), "stats", fmt.Sprintf("%+v", st))
	})

	t.Run("success: against an h2 server the gate stays and redundant dials are counted (K20)", func(t *testing.T) {
		const reps = 10
		var accepts, carrying []int
		for range reps {
			// The reworded AC-P4 handler (the first request answered at
			// once): holding every response until all 64 arrive would
			// deadlock against FirstHold until the guard, by design (G2).
			b := newBarrier(fanN, fanGuard)
			b.free = "cold"
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: b})
			tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL()), Mode: HTTPAuto})
			calls := fanOut(fanN, func(i int) result { return get(t.Context(), tr, srv.URL()+"/cold/"+strconv.Itoa(i)) })
			for i, r := range calls {
				if r.Err != nil || r.Status != http.StatusOK || r.ProtoMajor != 2 {
					t.Errorf("call %d: %d HTTP/%d %v", i, r.Status, r.ProtoMajor, r.Err)
				}
			}
			if st := tr.Stats(); st.Leaders != 1 || st.Releases != 1 || b.guardedCount("cold") != 0 {
				t.Errorf("stats %+v, guard released %d; want the gate (1 leader, 1 release) and no guard", st, b.guardedCount("cold"))
			}
			seen := map[int]bool{}
			for _, r := range srv.Requests() {
				seen[r.Conn] = true
			}
			accepts = append(accepts, srv.Accepts())
			carrying = append(carrying, len(seen))
		}
		record(t, "case", "auto-h2-cold64", "reps", reps, "accepts", fmt.Sprint(accepts), "conns_carrying_requests", fmt.Sprint(carrying))
	})
}
