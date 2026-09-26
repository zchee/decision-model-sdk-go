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

// B6 (docs/perf/benchmarks.md): calls over a real HTTP/2 connection over
// TLS on loopback, through the default transport (internal/h2gate), wall
// clock only.
//
//   - call: one q3 call at a time on one warm connection (the client warmed
//     up with WarmUp before the timer starts): the plan's call/loopback.
//     The new-conns metric counts connections the server accepted during
//     the timed calls; anything but 0 means a call paid a handshake.
//   - cold-fanout-64: a fresh client, then 64 q3 calls started at once,
//     until the last answers, then the client is closed; the cold burst of
//     AC-P4 and K22 (the waiters wait for the leader's response headers,
//     so a burst pays two round trips of the server). conns/op is the
//     connections each burst opened: AC-P4 asserts 1 (internal/h2gate's
//     TestFanOut); here it is recorded. Each burst runs under a 30 s
//     deadline besides each attempt's own.
//
// The server is internal/testsupport's LoopbackServer: TLS on 127.0.0.1
// with its own HTTP/2 frame writer, answering result.json (and models.json
// for WarmUp) after reading the request body, as a real server would.
//
// How this can mislead: loopback has no latency, so this is an upper bound
// on what one connection gives, not a prediction for a network. The numbers
// are dominated by the kernel's loopback path, TLS records and goroutine
// scheduling on both ends, none of which is the SDK's code, and B/op and
// allocs/op include the server's allocations in the same process. The
// server records every request it sees (a header copy each), so memory
// grows with the iteration count. These rows are recorded, not gated.

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// fanOut is how many calls cold-fanout-64 starts at once.
const fanOut = 64

// newLoopbackServer starts B6's server: result.json for System One,
// models.json for the model list, each read after the request body.
func newLoopbackServer(tb testing.TB) *testsupport.LoopbackServer {
	tb.Helper()
	result := testsupport.Fixture(tb, "result.json")
	models := testsupport.Fixture(tb, "models.json")
	return testsupport.NewLoopbackServer(tb, testsupport.ServerConfig{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			body := result
			if strings.HasSuffix(r.URL.Path, "/v1/models") {
				body = models
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
		}),
	})
}

// loopbackOptions are the options of a client of srv with every other
// setting given, so that the environment cannot change a request.
func loopbackOptions(tb testing.TB, srv *testsupport.LoopbackServer) []ClientOption {
	tb.Helper()
	return []ClientOption{WithAPIKey(testKey), WithBaseURL(srv.URL()), WithModel(DefaultModel), WithRootCAs(testsupport.RootCAs(tb)), WithProxy(nil)}
}

// BenchmarkLoopback is B6; see the comment at the top of this file.
func BenchmarkLoopback(b *testing.B) {
	srv := newLoopbackServer(b)
	opts := loopbackOptions(b, srv)
	qs := q3Questions(b)
	state := newCallState()

	b.Run("call", func(b *testing.B) {
		c, err := NewClient(opts...)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { _ = c.Close() })
		ctx := b.Context()
		if err := c.WarmUp(ctx); err != nil {
			b.Fatal(err)
		}
		resp, err := c.SystemOne(ctx, state, qs)
		if err != nil {
			b.Fatal(err)
		}
		if n := resp.Answers().Len(); n != 3 {
			b.Fatalf("%d answers, want 3", n)
		}
		accepts := srv.Accepts()
		b.ReportAllocs()
		for b.Loop() {
			if sinkCall, err = c.SystemOne(ctx, state, qs); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(srv.Accepts()-accepts), "new-conns")
	})

	b.Run("cold-fanout-64", func(b *testing.B) {
		var bursts, conns int
		b.ReportAllocs()
		for b.Loop() {
			before := srv.Accepts()
			if err := coldBurst(b.Context(), opts, state, qs); err != nil {
				b.Fatal(err)
			}
			conns += srv.Accepts() - before
			bursts++
		}
		b.ReportMetric(float64(conns)/float64(bursts), "conns/op")
	})
}

// coldBurst builds a client from opts, starts fanOut calls at once, waits
// for all of them under a 30 s deadline, closes the client and returns the
// first error a call returned.
func coldBurst(ctx context.Context, opts []ClientOption, state any, qs *Prepared) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c, err := NewClient(opts...)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	for range fanOut {
		wg.Go(func() {
			if _, err := c.SystemOne(ctx, state, qs); err != nil {
				once.Do(func() { first = err })
			}
		})
	}
	wg.Wait()
	return first
}
