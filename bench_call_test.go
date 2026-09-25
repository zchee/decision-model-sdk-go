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
	"strings"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// sinkCall keeps BenchmarkCall's result alive.
var sinkCall *SystemOneResponse

// BenchmarkCall measures one whole SystemOne call of the NF3 shape (AC-P6):
// the q3 questions, a 1 KiB boxed-string state, one attempt over the
// discarding Recorder answering result.json, no call options and no logger.
// "sdk" is the plan's call/sdk; call/naive, the comparator, is W5.1's.
func BenchmarkCall(b *testing.B) {
	b.Run("sdk", func(b *testing.B) {
		rec := &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, testsupport.Fixture(b, "result.json"))}}
		// Every setting an option gives, so the environment cannot change
		// the body.
		c, err := NewClient(WithRoundTripper(rec), WithAPIKey(testKey), WithBaseURL(DefaultBaseURL), WithModel(DefaultModel))
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { _ = c.Close() })
		qs := q3Questions(b)
		var state any = strings.Repeat("s", 1<<10-2)
		ctx := b.Context()
		b.ReportAllocs()
		for b.Loop() {
			if sinkCall, err = c.SystemOne(ctx, state, qs); err != nil {
				b.Fatal(err)
			}
		}
	})
}
