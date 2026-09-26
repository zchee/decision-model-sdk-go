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
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// boundChildEnv makes TestBoundFuzzInput's child run an input that never
// returns.
const boundChildEnv = "TYPESAFE_TESTSUPPORT_BOUND_CHILD"

// TestBoundFuzzInput checks the per-input bound every fuzz target arms: an
// input that runs past it ends the process with a non-zero status and a
// panic naming the input and the bound, and a bound disarmed before it
// fires never fires. The hanging input runs in a child process, since the
// panic ends the process it fires in; the disarm is checked through
// boundFuzzInput, the wrapper every target calls, with boundFire swapped for
// a recorder.
//
// It runs in CI's -race test step (go test -race with coverage) and its
// non-race allocation-tests step (go test -count=1 ./internal/codec/
// ./internal/wire/ ./internal/testsupport/), on ubuntu-26.04, xcode-27 and
// windows-2025.
func TestBoundFuzzInput(t *testing.T) {
	const bound = 50 * time.Millisecond
	if os.Getenv(boundChildEnv) == "1" {
		defer boundFuzzInput(t, bound)()
		<-t.Context().Done() // an input that never returns: the test never ends on its own
		return
	}

	t.Run("an input past the bound ends the process", func(t *testing.T) {
		// A watchdog that never fires leaves the child hanging: the context
		// kills it, and its output then lacks the panic.
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBoundFuzzInput$", "-test.count=1", "-test.v") //nolint:gosec // G204: re-runs this test binary itself.
		cmd.Env = append(os.Environ(), boundChildEnv+"=1")
		start := time.Now()
		out, err := cmd.CombinedOutput()
		elapsed := time.Since(start)
		if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() == 0 {
			t.Fatalf("child = %v after %v, want a non-zero exit; output:\n%s", err, elapsed, out)
		}
		want := "testsupport: fuzz input TestBoundFuzzInput ran past the 50ms per-input bound"
		if !strings.Contains(string(out), want) {
			t.Fatalf("child output lacks %q:\n%s", want, out)
		}
		if elapsed < bound {
			t.Errorf("child ended after %v, before the %v bound", elapsed, bound)
		}
	})

	t.Run("a bound disarmed before it fires never fires", func(t *testing.T) {
		const arm, window = 50 * time.Millisecond, 150 * time.Millisecond
		saved := boundFire
		t.Cleanup(func() { boundFire = saved })
		// A timer never fires before its duration, so a disarm that returns
		// less than arm after a start taken before arming stopped the
		// watchdog in time. One that returns later (this goroutine
		// descheduled for 50 ms) proves nothing; arm again, each attempt
		// with a recorder of its own.
		var fired chan string
		for attempt := 0; ; attempt++ {
			if attempt == 20 {
				t.Fatalf("no disarm within %v of arming in %d attempts", arm, attempt)
			}
			ch := make(chan string, 1)
			fired, boundFire = ch, func(msg string) { ch <- msg }
			start := time.Now()
			boundFuzzInput(t, arm)()
			if time.Since(start) < arm {
				break
			}
		}
		select {
		case msg := <-fired:
			t.Fatalf("a disarmed bound fired: %s", msg)
		case <-time.After(window):
		}
	})

	t.Run("an armed bound fires with its message", func(t *testing.T) {
		fired := make(chan string, 1)
		disarm := armBound("armed", time.Millisecond, func(msg string) { fired <- msg })
		defer disarm()
		select {
		case msg := <-fired:
			if want := "testsupport: fuzz input armed ran past the 1ms per-input bound"; msg != want {
				t.Errorf("fired %q, want %q", msg, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("an armed bound did not fire within 5 s")
		}
	})
}
