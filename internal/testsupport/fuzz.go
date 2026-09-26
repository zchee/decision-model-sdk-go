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

package testsupport

import (
	"fmt"
	"testing"
	"time"
)

// FuzzInputBound is the time one fuzz input may run (plan section 7, W6.1):
// an input that takes longer is a finding, as a hang past libFuzzer's
// -timeout=10 is for the Rust SDK's targets.
const FuzzInputBound = 10 * time.Second

// BoundFuzzInput arms the per-input bound for the fuzz input tb runs and
// returns the function that disarms it; a fuzz function starts with
// defer testsupport.BoundFuzzInput(t)().
//
// Go's fuzzing engine has no per-input timeout (cmd/go sets no kill timeout
// while fuzzing, and internal/fuzz only stops a worker the coordinator has
// already cancelled), and a context cannot interrupt a decoder that never
// returns, so the bound is a watchdog: when the input runs past
// [FuzzInputBound] the watchdog panics on its own goroutine, which ends the
// process. Under go test -fuzz the coordinator then reports the worker as
// "hung or terminated unexpectedly" and writes the input to testdata/fuzz;
// a plain go test run of the seed corpus fails with the panic, which names
// the input.
func BoundFuzzInput(tb testing.TB) (disarm func()) {
	return boundFuzzInput(tb, FuzzInputBound)
}

// boundFuzzInput is [BoundFuzzInput] with the bound d.
func boundFuzzInput(tb testing.TB, d time.Duration) (disarm func()) {
	name := tb.Name()
	timer := time.AfterFunc(d, func() {
		panic(fmt.Sprintf("testsupport: fuzz input %s ran past the %v per-input bound", name, d))
	})
	return func() { timer.Stop() }
}
