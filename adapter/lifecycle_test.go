// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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

package adapter

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// closingProvider is a provider of a test whose Close calls closeFn and
// counts its calls; it answers every request with noulAnswer.
type closingProvider struct {
	model   string
	mu      sync.Mutex
	closes  int
	closeFn func(n int) error
}

func (p *closingProvider) Model() string { return p.model }

func (p *closingProvider) Do(context.Context, *llm.Request) (*llm.Result, error) {
	return textResult(noulAnswer), nil
}

// Close counts the call, then runs closeFn with the call's number from 1.
func (p *closingProvider) Close() error {
	p.mu.Lock()
	p.closes++
	n := p.closes
	p.mu.Unlock()
	if p.closeFn == nil {
		return nil
	}
	return p.closeFn(n)
}

func (p *closingProvider) closeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closes
}

// ownedProviders returns an Adapter whose factory "openai" builds the
// providers of models from closers by model name, and builds one owned
// provider per model, in the order of models.
func ownedProviders(t testing.TB, closers map[string]*closingProvider, models ...string) *Adapter {
	t.Helper()
	ad := newAdapter(t, WithFactory("openai", func(model string) (llm.Provider, error) { return closers[model], nil }))
	for _, m := range models {
		if _, _, err := send(t, ad, rawRequest(t.Context(), "POST", systemOnePath, `{"state":"s","model":"openai:`+m+`","questions":{"answer":{"type":"noul"}}}`)); err != nil {
			t.Fatalf("building %s: %v", m, err)
		}
	}
	return ad
}

// lockBound bounds every wait of the lifecycle tests that a lock left held
// would make endless. A goroutine blocked on a sync.Mutex is not durably
// blocked, so inside a synctest bubble the clock never advances past it and
// only go test's own timeout ends the test; these tests wait on the real
// clock instead.
const lockBound = 30 * time.Second

// goroutineStacks returns the stacks of every goroutine, which name the
// lock a stuck goroutine waits for.
func goroutineStacks() string {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return string(buf[:n])
		}
		buf = make([]byte, 2*len(buf))
	}
}

// await returns what ch delivers, or fails t with every goroutine's stack
// once lockBound has passed.
func await[T any](t testing.TB, what string, ch <-chan T) T {
	t.Helper()
	timer := time.NewTimer(lockBound)
	defer timer.Stop()
	select {
	case v := <-ch:
		return v
	case <-timer.C:
		t.Fatalf("%s did not return within %v; goroutines:\n%s", what, lockBound, goroutineStacks())
		var zero T
		return zero
	}
}

// awaitBlocked waits until a goroutine is blocked with the wait reason
// reason, as runtime.Stack prints it ("chan receive",
// "sync.WaitGroup.Wait"), and the first function of this package on its
// stack is fn. It reads every goroutine's stack each millisecond, fails t
// at once when returned delivers (the call returned instead of blocking),
// and fails t with the stacks once lockBound has passed.
func awaitBlocked(t testing.TB, reason, fn string, returned <-chan error) {
	t.Helper()
	const pkg = "github.com/zchee/decision-model-sdk-go/adapter."
	deadline := time.Now().Add(lockBound)
	for {
		stacks := goroutineStacks()
		for g := range strings.SplitSeq(stacks, "\n\n") {
			header, frames, _ := strings.Cut(g, "\n")
			if !strings.Contains(header, "["+reason) {
				continue
			}
			for line := range strings.SplitSeq(frames, "\n") {
				if strings.HasPrefix(line, pkg) {
					if strings.HasPrefix(line, pkg+fn+"(") {
						return
					}
					break
				}
			}
		}
		select {
		case err := <-returned:
			t.Fatalf("%s returned %v instead of blocking (%s)", fn, err, reason)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("no goroutine blocked (%s) in %s within %v; goroutines:\n%s", reason, fn, lockBound, stacks)
		}
		time.Sleep(time.Millisecond)
	}
}

// assertUnlocked fails t when a lock of ad is held while no call and no
// Close runs: such a lock was left behind by the one before, and the next
// call or Close would block on it forever. It releases a lock it finds
// held, so that the test's cleanups, which close the Adapter, return.
func assertUnlocked(t testing.TB, ad *Adapter) {
	t.Helper()
	locks := []struct {
		name string
		mu   *sync.Mutex
	}{
		{"the Adapter's mutex", &ad.mu},
		{"the provider cache's mutex", &ad.cacheMu},
	}
	for _, l := range locks {
		held := !l.mu.TryLock()
		l.mu.Unlock()
		if held {
			t.Fatalf("%s is still held", l.name)
		}
	}
}

// watchLocks ends the test binary with every goroutine's stack when t has
// not ended within lockBound: a call or Close stuck on a lock blocks the
// test's own goroutine, which then cannot fail itself. It is for tests
// outside a synctest bubble, whose clock it uses.
func watchLocks(t testing.TB) {
	name := t.Name()
	timer := time.AfterFunc(lockBound, func() {
		panic(fmt.Sprintf("%s did not end within %v; goroutines:\n%s", name, lockBound, goroutineStacks()))
	})
	t.Cleanup(func() { timer.Stop() })
}

// roundTripError sends req to ad and returns RoundTrip's error, closing the
// body of a response.
func roundTripError(ad *Adapter, req *http.Request) error {
	resp, err := ad.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	return err
}

// TestClosePanicLeavesTheCleanupConsistent checks a provider whose Close
// panics during a cleanup that a second Close waits for: the panic
// propagates in the goroutine that ran the cleanup; the waiting Close
// returns an error, not nil, as upstream's waiters raise the cleanup's
// exception (src/system_one_adapter/_client.py:516-524); no lock of the
// Adapter is left held, so a call after it is refused; the provider closed
// before the panic is not closed again; and the next Close runs a new
// cleanup over the panicking provider and the one it did not reach. The
// second Close is known to wait once its stack shows it blocked on the
// cleanup's channel. The test runs outside a synctest bubble, with every
// wait bounded by lockBound, as a Close stuck on a lock is never reported
// inside one.
func TestClosePanicLeavesTheCleanupConsistent(t *testing.T) {
	watchLocks(t)
	entered, release := make(chan struct{}), make(chan struct{})
	closers := map[string]*closingProvider{
		"first": {model: "first"},
		"second": {model: "second", closeFn: func(n int) error {
			if n == 1 {
				close(entered)
				<-release
				panic("provider close panicked")
			}
			return nil
		}},
		"third": {model: "third"},
	}
	ad := ownedProviders(t, closers, "first", "second", "third")
	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		_ = ad.Close()
	}()
	await(t, "the first Close's cleanup", (<-chan struct{})(entered))
	waited := make(chan error, 1)
	go func() { waited <- ad.Close() }()
	awaitBlocked(t, "chan receive", "(*Adapter).Close", waited)
	close(release)
	if got := await(t, "the first Close", (<-chan any)(panicked)); got != "provider close panicked" {
		t.Fatalf("the first Close's panic = %v, want the provider's", got)
	}
	if err := await(t, "the waiting Close", (<-chan error)(waited)); err == nil || err.Error() != "adapter: a provider's Close panicked while the Adapter closed its providers" {
		t.Fatalf("the waiting Close returned %v, want the fixed error", err)
	}
	assertUnlocked(t, ad)
	refused := make(chan error, 1)
	go func() { refused <- roundTripError(ad, rawRequest(t.Context(), "POST", systemOnePath, noulBody)) }()
	if err := await(t, "a call after the panic", (<-chan error)(refused)); !isClosedError(err) {
		t.Errorf("a call after the panic: %v, want the closed Error", err)
	}
	for _, step := range []string{"the next Close", "a Close after everything was closed"} {
		closed := make(chan error, 1)
		go func() { closed <- ad.Close() }()
		if err := await(t, step, (<-chan error)(closed)); err != nil {
			t.Fatalf("%s returned %v", step, err)
		}
		for model, want := range map[string]int{"first": 1, "second": 2, "third": 1} {
			if got := closers[model].closeCount(); got != want {
				t.Errorf("after %s, provider %s closed %d times, want %d", step, model, got, want)
			}
		}
	}
}
