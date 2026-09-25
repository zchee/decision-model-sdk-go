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

import "testing"

// BenchmarkNoop measures an empty loop body. It gives the CodSpeed workflow
// one benchmark before the SDK has any, so a runner refusal can be told apart
// from a run that found nothing to measure.
func BenchmarkNoop(b *testing.B) {
	var n int
	for b.Loop() {
		n++
	}
	if n == 0 {
		b.Fatal("b.Loop ran zero iterations")
	}
}
