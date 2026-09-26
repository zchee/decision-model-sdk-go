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

// B4's parse of a server's wait (docs/perf/benchmarks.md):
// BenchmarkRetryAfter/{seconds,ms,http-date} times (*APIError).RetryAfter,
// the way a caller reaches it, on the error of a real call through the
// Recorder answered 429 with Retry-After: 2, Retry-After-Ms: 1500 or
// Retry-After as an HTTP date. The error is made once, outside the loop.
// The retry loop reads the same header through the same parser before each
// wait. B4's backoff delay times an unexported function and stays in the
// root package (bench_internal_test.go).
//
// How this can mislead: RetryAfter reads the clock on every call, so each
// row includes a time.Now, which only the date row uses. The rows are
// recorded, not targets: the parse runs once per retry, beside a wait of
// hundreds of milliseconds.

import (
	"errors"
	"net/http"
	"testing"
	"time"

	typesafe "github.com/zchee/typesafe-sdk-go"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// Sinks keep BenchmarkRetryAfter's results alive.
var (
	sinkDelay time.Duration
	sinkOK    bool
)

// rateLimited returns the *APIError of one call that the server answers
// 429 with the header name: value, as a caller receives it.
func rateLimited(tb testing.TB, name, value string) *typesafe.APIError {
	tb.Helper()
	reply := testsupport.JSON(http.StatusTooManyRequests, []byte(`{"error":"slow down"}`))
	reply.Header.Set(name, value)
	c := newBenchClient(tb, &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{reply}})
	_, err := c.SystemOne(tb.Context(), newCallState(), q3Questions(tb), typesafe.Retry(typesafe.NoRetry()))
	apiErr, ok := errors.AsType[*typesafe.APIError](err)
	if !ok {
		tb.Fatalf("a 429 gave %T %v, want an *APIError", err, err)
	}
	return apiErr
}

// BenchmarkRetryAfter is B4's parse of a server's wait; see the comment at
// the top of this file.
func BenchmarkRetryAfter(b *testing.B) {
	tests := []struct {
		name, header, value string
		want                time.Duration // 0: any positive wait (the date's depends on the clock)
	}{
		{name: "seconds", header: "Retry-After", value: "2", want: 2 * time.Second},
		{name: "ms", header: "Retry-After-Ms", value: "1500", want: 1500 * time.Millisecond},
		{name: "http-date", header: "Retry-After", value: "Fri, 01 Jan 2100 00:00:00 GMT"},
	}
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			apiErr := rateLimited(b, tt.header, tt.value)
			d, ok := apiErr.RetryAfter()
			if !ok || d <= 0 || (tt.want != 0 && d != tt.want) {
				b.Fatalf("%s %q: RetryAfter = %v, %t; want %v", tt.header, tt.value, d, ok, tt.want)
			}
			b.ReportAllocs()
			for b.Loop() {
				sinkDelay, sinkOK = apiErr.RetryAfter()
			}
		})
	}
}
