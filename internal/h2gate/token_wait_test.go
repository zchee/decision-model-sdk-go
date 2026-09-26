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
	"net/http"
	"net/http/httptrace"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// TestTokenWaitBound pins ruling R85 (risk K28d) on fake time: the first
// call's trace hook blocks in GotConn, so the call keeps the header-write
// token. When the hook blocks past the hold bound (20 s by default), the
// other calls wait for the token that long and then go out without it, on
// the one connection, while the hook still blocks; Stats.TokenExpiries
// counts them. When it returns within the bound, they wait until it returns
// and no wait expires. A waiter whose own deadline comes before the bound
// ends on it, with its context's error, and no wait expires either. Every
// other call carries a deadline two minutes out, so without the bound the
// waiters end on that deadline instead. The order is read by traceSeq. It
// runs in CI's -race test step (go test -race with coverage) on
// ubuntu-26.04, xcode-27 and windows-2025.
func TestTokenWaitBound(t *testing.T) {
	const waiters = 2
	tests := map[string]struct {
		block    time.Duration // how long the hook blocks; 0 until the test releases it
		deadline time.Duration // the waiters' deadline; 0 for fakeDeadline
		expiries uint64
	}{
		"success: a hook that blocks past the bound stalls no other call":                 {expiries: waiters},
		"success: a hook that returns within the bound holds the others until it returns": {block: 10 * time.Second},
		"error: a waiter whose deadline comes first ends on it":                           {deadline: 5 * time.Second},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := testsupport.NewFakeH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
				tr := fakeTransport(t, srv.Client().Transport)
				release := make(chan struct{})
				var returned atomic.Uint64 // traceSeq when the hook returned
				hook := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) {
					if tt.block > 0 {
						time.Sleep(tt.block)
					} else {
						<-release
					}
					returned.Store(traceSeq.Add(1))
				}}
				start := time.Now()
				first := make(chan result, 1)
				go func() {
					ctx, cancel := context.WithTimeout(httptrace.WithClientTrace(t.Context(), hook), fakeDeadline)
					defer cancel()
					first <- get(ctx, tr, "http://example.com/first")
				}()
				synctest.Wait() // the first call holds the token, blocked in its hook

				others := fanOut(waiters, func(i int) result {
					if tt.deadline == 0 {
						return fakeGet(t, tr, "/other/"+strconv.Itoa(i))
					}
					ctx, cancel := context.WithTimeout(t.Context(), tt.deadline)
					defer cancel()
					return get(ctx, tr, "http://example.com/other/"+strconv.Itoa(i))
				})
				releasedSeq := traceSeq.Add(1)
				close(release)
				f := <-first

				if f.Err != nil || f.Status != http.StatusOK {
					t.Errorf("first call: %d %v", f.Status, f.Err)
				}
				for i, r := range others {
					if tt.deadline > 0 {
						// Ended on its deadline while it waited: no HEADERS.
						if !errors.Is(r.Err, context.DeadlineExceeded) || !r.WroteHeaders.IsZero() || r.Done.Sub(start) != tt.deadline || r.DoneSeq > releasedSeq {
							t.Errorf("call %d: %v, HEADERS %v, done %v after the start at traceSeq %d, the release at %d; want its %v deadline's error while it waited", i, r.Err, !r.WroteHeaders.IsZero(), r.Done.Sub(start), r.DoneSeq, releasedSeq, tt.deadline)
						}
						continue
					}
					if r.Err != nil || r.Status != http.StatusOK {
						t.Errorf("call %d: %d %v", i, r.Status, r.Err)
						continue
					}
					waited := r.WroteHeaders.Sub(start)
					if tt.block == 0 {
						// Out at the bound, while the hook still blocked.
						if waited != tr.holdBound || r.DoneSeq > releasedSeq {
							t.Errorf("call %d wrote HEADERS after %v, done at traceSeq %d, the hook released at %d; want the %v bound and done before the release", i, waited, r.DoneSeq, releasedSeq, tr.holdBound)
						}
					} else if r.WroteHeadersSeq < returned.Load() || waited < tt.block {
						t.Errorf("call %d wrote HEADERS after %v at traceSeq %d, the hook returned at %d; want them after the hook's %v", i, waited, r.WroteHeadersSeq, returned.Load(), tt.block)
					}
				}
				st := tr.Stats()
				if st.TokenExpiries != tt.expiries || srv.Accepts() != 1 {
					t.Errorf("stats %+v, accepts %d; want %d token expiries on 1 connection", st, srv.Accepts(), tt.expiries)
				}
			})
		})
	}
}

// TestTokenFreeTakesNoWait pins send's fast path (review SLICE3 MINOR 2): a
// call that finds the header-write token free takes it at once and never
// enters waitToken, so it arms no timer; only a call that finds the token
// held waits there. Eight uncontended calls on one transport enter
// waitToken 0 times; a call made while another call's trace hook holds the
// token enters it once, the control that the count counts. It runs in CI's
// -race test step (go test -race with coverage) on ubuntu-26.04, xcode-27
// and windows-2025.
func TestTokenFreeTakesNoWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := testsupport.NewFakeH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		tr := fakeTransport(t, srv.Client().Transport)
		for i := range 8 {
			if r := fakeGet(t, tr, "/free/"+strconv.Itoa(i)); r.Err != nil || r.Status != http.StatusOK {
				t.Fatalf("uncontended call %d: %d %v", i, r.Status, r.Err)
			}
		}
		if n := tr.tokenWaits.Load(); n != 0 {
			t.Errorf("8 uncontended calls entered waitToken %d times, want 0: a free token is taken without waiting", n)
		}

		release := make(chan struct{})
		hook := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { <-release }}
		held := make(chan result, 1)
		go func() {
			ctx, cancel := context.WithTimeout(httptrace.WithClientTrace(t.Context(), hook), fakeDeadline)
			defer cancel()
			held <- get(ctx, tr, "http://example.com/held")
		}()
		synctest.Wait() // the call holds the token, blocked in its hook
		waiter := make(chan result, 1)
		go func() { waiter <- fakeGet(t, tr, "/waiter") }()
		synctest.Wait() // the waiter is in waitToken
		if n := tr.tokenWaits.Load(); n != 1 {
			t.Errorf("a call made while the token is held entered waitToken %d times, want 1", n)
		}
		close(release)
		for _, r := range []result{<-held, <-waiter} {
			if r.Err != nil || r.Status != http.StatusOK {
				t.Errorf("%s: %d %v", r.Path, r.Status, r.Err)
			}
		}
	})
}
