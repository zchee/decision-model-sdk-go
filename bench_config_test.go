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

import (
	"net/http"
	"testing"
)

// sinkHeader keeps BenchmarkHeaderTemplateClone's result alive.
var sinkHeader http.Header

// BenchmarkHeaderTemplateClone measures the per-attempt cost of the header
// template: one http.Header.Clone of the POST template, which is what a
// request copies before it adds its own headers.
func BenchmarkHeaderTemplateClone(b *testing.B) {
	tests := map[string][]ClientOption{
		"sdk-only":       nil,
		"three-defaults": {WithHeader("X-Team", "billing"), WithHeader("X-Trace", "on"), WithHeader("X-Region", "ap-northeast-1")},
		"no-runtime-hdr": {WithRuntimeHeader(false)},
	}
	for name, extra := range tests {
		b.Run(name, func(b *testing.B) {
			c := mustResolveB(b, append([]ClientOption{WithAPIKey("test-key")}, extra...)...)
			b.ReportAllocs()
			for b.Loop() {
				sinkHeader = c.systemOneHeader.Clone()
			}
		})
	}
}

// mustResolveB resolves opts with no environment, failing the benchmark
// when that fails.
func mustResolveB(b *testing.B, opts ...ClientOption) *config {
	b.Helper()
	c, err := resolveConfig(noEnv, opts...)
	if err != nil {
		b.Fatalf("resolve: %v", err)
	}
	return c
}
