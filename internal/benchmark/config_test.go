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

import (
	"maps"
	"net/http"
	"slices"
	"testing"

	typesafe "github.com/zchee/typesafe-sdk-go"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// sinkHeader keeps BenchmarkHeaderTemplateClone's result alive.
var sinkHeader http.Header

// sentHeader returns the header a client built with opts sends on a call's
// first attempt, which is its POST template itself (ruling R28), as a
// Recorder records it: a copy with the same names and values.
func sentHeader(tb testing.TB, opts ...typesafe.ClientOption) http.Header {
	tb.Helper()
	rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, testsupport.Fixture(tb, "result.json"))}}
	c := newBenchClient(tb, rec, opts...)
	if _, err := c.SystemOne(tb.Context(), newCallState(), q3Questions(tb)); err != nil {
		tb.Fatal(err)
	}
	reqs := rec.Requests()
	if len(reqs) != 1 {
		tb.Fatalf("the transport saw %d requests, want 1", len(reqs))
	}
	return reqs[0].Header
}

// BenchmarkHeaderTemplateClone measures one http.Header.Clone of the POST
// template, which is what a retry's header costs before it adds its own
// X-TypeSafe-Retry-Count. The template is taken from a first attempt
// through the exported API: the cost of a clone depends only on the map's
// names and values, which the recorded copy shares with the client's.
func BenchmarkHeaderTemplateClone(b *testing.B) {
	tests := map[string][]typesafe.ClientOption{
		"sdk-only":       nil,
		"three-defaults": {typesafe.WithHeader("X-Team", "billing"), typesafe.WithHeader("X-Trace", "on"), typesafe.WithHeader("X-Region", "ap-northeast-1")},
		"no-runtime-hdr": {typesafe.WithRuntimeHeader(false)},
	}
	for _, name := range slices.Sorted(maps.Keys(tests)) {
		b.Run(name, func(b *testing.B) {
			template := sentHeader(b, tests[name]...)
			b.ReportAllocs()
			for b.Loop() {
				sinkHeader = template.Clone()
			}
		})
	}
}
