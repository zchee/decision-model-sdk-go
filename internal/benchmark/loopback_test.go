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

package benchmark

// B6's warm call (docs/perf/benchmarks.md): one q3 call at a time over a
// real HTTP/2 connection over TLS on loopback, through the default
// transport (internal/h2gate), on one warm connection (the client warmed up
// with WarmUp before the timer starts), wall clock only: the plan's
// call/loopback. The new-conns metric counts connections the server
// accepted during the timed calls; anything but 0 means a call paid a
// handshake. B6's cold 64-way burst reports the gate's own counters, which
// the exported API does not carry, and stays in the root package
// (bench_internal_test.go).
//
// The server is testsupport.NewFixtureServer: TLS on 127.0.0.1 with its own
// HTTP/2 frame writer, answering result.json (and models.json for WarmUp)
// after reading the request body, as a real server would.
//
// How this can mislead: loopback has no latency, so this is an upper bound
// on what one connection gives, not a prediction for a network. The numbers
// are dominated by the kernel's loopback path, TLS records and goroutine
// scheduling on both ends, none of which is the SDK's code, and B/op and
// allocs/op include the server's allocations in the same process. The
// server records every request it sees (a header copy each), so memory
// grows with the iteration count. The row is recorded, never gated.

import (
	"testing"

	typesafe "github.com/zchee/typesafe-sdk-go"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// BenchmarkLoopback is B6's warm call; see the comment at the top of this
// file.
func BenchmarkLoopback(b *testing.B) {
	srv := testsupport.NewFixtureServer(b)
	qs := q3Questions(b)
	state := newCallState()

	b.Run("call", func(b *testing.B) {
		c, err := typesafe.NewClient(typesafe.WithAPIKey(testKey), typesafe.WithBaseURL(srv.URL()), typesafe.WithModel(typesafe.DefaultModel), typesafe.WithRootCAs(testsupport.RootCAs(b)), typesafe.WithProxy(nil))
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
}
