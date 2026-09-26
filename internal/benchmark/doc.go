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

// Package benchmark holds the SDK's benchmarks that need only its exported
// API and the module's internal packages (docs/perf/benchmarks.md): B5, the
// whole call against its floor and the naive comparator; B4's Retry-After
// parse; B6's warm call over loopback; the header template's clone; and an
// empty loop. They left the root package on owner directive G5.
//
// The benchmarks that time an unexported symbol of the root package (the
// body encode, request assembly, the backoff, the falsiness check, the
// gate's counters) or share a case table with a root allocation test stay
// in the root's bench_internal_test.go.
//
// The package has no code outside its tests.
package benchmark
