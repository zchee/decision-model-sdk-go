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
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// closingProvider is a provider of a test whose Close calls closeFn and
// counts its calls; it answers every request with do, or with noulAnswer
// when do is nil.
type closingProvider struct {
	model   string
	mu      sync.Mutex
	closes  int
	closeFn func(n int) error
	do      func(ctx context.Context, req *llm.Request) (*llm.Result, error)
	// setting is what the provider read from the environment when it was
	// built.
	setting string
}

func (p *closingProvider) Model() string { return p.model }

func (p *closingProvider) Do(ctx context.Context, req *llm.Request) (*llm.Result, error) {
	if p.do != nil {
		return p.do(ctx, req)
	}
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
		if status, err := sendWithin(t, ad, rawRequest(t.Context(), "POST", systemOnePath, `{"state":"s","model":"openai:`+m+`","questions":{"answer":{"type":"noul"}}}`)); err != nil || status != http.StatusOK {
			t.Fatalf("building %s: %d %v", m, status, err)
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
// stack is fn. It reads every goroutine's stack each millisecond and fails
// t with the stacks at once when a goroutine whose first function of this
// package is a method of *Adapter waits on another lock or channel (the
// call is stuck where it should not wait), at once when returned delivers
// (the call returned instead of blocking), and once lockBound has passed.
func awaitBlocked(t testing.TB, reason, fn string, returned <-chan error) {
	t.Helper()
	const pkg = "github.com/zchee/decision-model-sdk-go/adapter."
	deadline := time.Now().Add(lockBound)
	for {
		stacks := goroutineStacks()
		for g := range strings.SplitSeq(stacks, "\n\n") {
			header, frames, _ := strings.Cut(g, "\n")
			first := ""
			for line := range strings.SplitSeq(frames, "\n") {
				if strings.HasPrefix(line, pkg) {
					first = line
					break
				}
			}
			if !strings.HasPrefix(first, pkg+"(*Adapter).") {
				continue
			}
			_, state, _ := strings.Cut(header, "[")
			state, _, _ = strings.Cut(state, "]")
			state, _, _ = strings.Cut(state, ",")
			switch {
			case state == reason && strings.HasPrefix(first, pkg+fn+"("):
				return
			case state != reason && isLockOrChannelWait(state):
				t.Fatalf("%s is blocked (%s) where %s should wait (%s); goroutines:\n%s", strings.TrimPrefix(first, pkg), state, fn, reason, stacks)
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

// isLockOrChannelWait reports whether a goroutine's wait reason is a wait
// on a lock, a WaitGroup, a channel or a select.
func isLockOrChannelWait(state string) bool {
	for _, prefix := range []string{"sync.", "semacquire", "chan ", "select"} {
		if strings.HasPrefix(state, prefix) {
			return true
		}
	}
	return false
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
// not ended within three times lockBound: a call or Close stuck on a lock
// outside await blocks the test's own goroutine, which then cannot fail
// itself. Its bound is longer than lockBound so that a wait stuck inside
// await fails its test through await first, and the later tests still run.
// It is for tests outside a synctest bubble, whose clock it uses.
func watchLocks(t testing.TB) {
	name := t.Name()
	bound := 3 * lockBound
	timer := time.AfterFunc(bound, func() {
		panic(fmt.Sprintf("%s did not end within %v; goroutines:\n%s", name, bound, goroutineStacks()))
	})
	t.Cleanup(func() { timer.Stop() })
}

// roundTripError sends req to ad and returns RoundTrip's error, closing the
// body of a response. It calls no method of a testing.TB, so a goroutine
// other than the test's may run it.
func roundTripError(ad *Adapter, req *http.Request) error {
	_, err := roundTripStatus(ad, req)
	return err
}

// roundTripStatus sends req to ad and returns the response's status, 0 when
// RoundTrip returns an error, and that error, closing the body of a
// response. It calls no method of a testing.TB.
func roundTripStatus(ad *Adapter, req *http.Request) (int, error) {
	resp, err := ad.RoundTrip(req)
	if resp == nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, err
}

// sendWithin sends req to ad from another goroutine and returns the status
// and the error, failing t with every goroutine's stack once lockBound has
// passed.
func sendWithin(t testing.TB, ad *Adapter, req *http.Request) (int, error) {
	t.Helper()
	type result struct {
		status int
		err    error
	}
	done := make(chan result, 1)
	go func() {
		status, err := roundTripStatus(ad, req)
		done <- result{status, err}
	}()
	r := await(t, "a call to the Adapter", (<-chan result)(done))
	return r.status, r.err
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

// row PL1 of docs/port-test-matrix.md and the rows after it port the tests
// of tests/test_provider_lifecycle.py, whose two vendors, openai and
// anthropic, are here two test factories registered under those names.

// vendors are the provider names the lifecycle tests run with.
var vendors = []string{"openai", "anthropic"}

// lifecycle is the Adapter of one lifecycle test and the providers its two
// factories built, in build order.
type lifecycle struct {
	mu    sync.Mutex
	built []*closingProvider
	// closed lists the models of the providers closed, in close order.
	closed []string
	// fail, when not nil, is the next build's error, once.
	fail error
	// configure, when not nil, sets up each provider built.
	configure func(p *closingProvider)
}

// factory returns the factory of vendor: a closingProvider per model,
// named "<vendor>/<model>", whose Close is logged.
func (l *lifecycle) factory(vendor string) llm.Factory {
	return func(model string) (llm.Provider, error) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if err := l.fail; err != nil {
			l.fail = nil
			return nil, err
		}
		p := &closingProvider{model: model}
		name := vendor + "/" + model
		p.closeFn = func(int) error {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.closed = append(l.closed, name)
			return nil
		}
		if l.configure != nil {
			l.configure(p)
		}
		l.built = append(l.built, p)
		return p, nil
	}
}

// adapter returns an Adapter with the two factories, whose default model is
// vendor's test-model, and opts after those, and watches t's locks.
func (l *lifecycle) adapter(t testing.TB, vendor string, opts ...Option) *Adapter {
	t.Helper()
	watchLocks(t)
	return newAdapter(t, append([]Option{WithFactory("openai", l.factory("openai")), WithFactory("anthropic", l.factory("anthropic")), WithDefaultModel(vendor + ":test-model")}, opts...)...)
}

// providers returns the providers built, in build order.
func (l *lifecycle) providers() []*closingProvider {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.built)
}

// closeLog returns the providers closed, in close order.
func (l *lifecycle) closeLog() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.closed)
}

// other returns the vendor that is not vendor.
func other(vendor string) string {
	if vendor == "openai" {
		return "anthropic"
	}
	return "openai"
}

// evaluateOn makes one call on c with opts and returns its error.
func evaluateOn(t testing.TB, c *decision.Client, opts ...decision.CallOption) error {
	t.Helper()
	_, err := c.SystemOne(t.Context(), "Great book", noulQuestions(t), opts...)
	return err
}

// TestReusesOwnedProviderAndClosesIt ports
// test_reuses_owned_provider_and_closes_sdk_on_context_exit: three calls
// build one provider, which stays open until the client from NewClient is
// closed and is then closed once; calls on the closed client and on the
// closed Adapter are refused.
func TestReusesOwnedProviderAndClosesIt(t *testing.T) {
	for _, vendor := range vendors {
		t.Run(vendor, func(t *testing.T) {
			var l lifecycle
			ad := l.adapter(t, vendor)
			c, err := NewClient(ad)
			if err != nil {
				t.Fatal(err)
			}
			if len(l.providers()) != 0 {
				t.Fatal("a provider was built before the first call")
			}
			for range 3 {
				if err := evaluateOn(t, c); err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
			}
			built := l.providers()
			if len(built) != 1 || built[0].closeCount() != 0 {
				t.Fatalf("providers built %d, closes %d; want 1 open", len(built), built[0].closeCount())
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if err := ad.Close(); err != nil {
				t.Fatal(err)
			}
			if built[0].closeCount() != 1 {
				t.Errorf("provider closes %d, want 1", built[0].closeCount())
			}
			if err := evaluateOn(t, c); !errors.Is(err, decision.ErrClientClosed) {
				t.Errorf("a call on the closed client: %v", err)
			}
			if _, _, err := send(t, ad, rawRequest(t.Context(), "POST", systemOnePath, noulBody)); !isClosedError(err) {
				t.Errorf("a call on the closed Adapter: %v", err)
			}
		})
	}
}

// TestProviderCacheKey ports
// test_cache_uses_resolved_provider_and_model_and_is_per_client: the cache
// is keyed by the resolved provider and model and belongs to one Adapter;
// providers stay open until their Adapter is closed.
func TestProviderCacheKey(t *testing.T) {
	for _, vendor := range vendors {
		t.Run(vendor, func(t *testing.T) {
			var l lifecycle
			c := sdkClient(t, l.adapter(t, vendor), false)
			for _, opts := range [][]decision.CallOption{
				nil,
				{decision.Model(vendor + ":test-model")},
				{decision.Model("another-model")},
				{decision.Model(other(vendor) + ":test-model")},
			} {
				if err := evaluateOn(t, c, opts...); err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
			}
			if got := len(l.providers()); got != 3 {
				t.Fatalf("providers built %d, want 3", got)
			}
			second, err := NewClient(l.adapter(t, vendor))
			if err != nil {
				t.Fatal(err)
			}
			if err := evaluateOn(t, second); err != nil {
				t.Fatal(err)
			}
			if err := second.Close(); err != nil {
				t.Fatal(err)
			}
			built := l.providers()
			if len(built) != 4 {
				t.Fatalf("providers built %d, want 4", len(built))
			}
			for i, p := range built[:3] {
				if p.closeCount() != 0 {
					t.Errorf("provider %d of the first Adapter closed with the second", i)
				}
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			want := []string{vendor + "/test-model", vendor + "/test-model", vendor + "/another-model", other(vendor) + "/test-model"}
			if diff := gocmp.Diff(want, l.closeLog()); diff != "" {
				t.Errorf("close order (-want +got):\n%s", diff)
			}
		})
	}
}

// TestInjectedProviderIsBorrowed ports test_injected_provider_is_borrowed:
// a WithProvider provider, as the default model or named by a call, is used
// and never closed, while an owned provider of the same Adapter is closed;
// after Close a call naming the borrowed provider is refused, and its
// owner can still close it.
func TestInjectedProviderIsBorrowed(t *testing.T) {
	for _, vendor := range vendors {
		for _, constructorDefault := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, constructor default %v", vendor, constructorDefault), func(t *testing.T) {
				var l lifecycle
				injected := &closingProvider{model: "test-model"}
				opts := []Option{WithProvider("injected", injected)}
				var call []decision.CallOption
				if constructorDefault {
					opts = append(opts, WithDefaultModel("injected"))
				} else {
					call = []decision.CallOption{decision.Model("injected")}
				}
				ad := newAdapter(t, append([]Option{WithFactory("openai", l.factory("openai")), WithFactory("anthropic", l.factory("anthropic"))}, opts...)...)
				c := sdkClient(t, ad, false)
				for _, opts := range [][]decision.CallOption{call, {decision.Model(vendor + ":owned-model")}, call} {
					if err := evaluateOn(t, c, opts...); err != nil {
						t.Fatalf("SystemOne: %v", err)
					}
				}
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
				if injected.closeCount() != 0 {
					t.Errorf("the borrowed provider was closed %d times", injected.closeCount())
				}
				if built := l.providers(); len(built) != 1 || built[0].closeCount() != 1 {
					t.Errorf("owned providers %d; want one, closed once", len(built))
				}
				if _, _, err := send(t, ad, rawRequest(t.Context(), "POST", systemOnePath, `{"state":"s","model":"injected","questions":{"answer":{"type":"noul"}}}`)); !isClosedError(err) {
					t.Errorf("a call naming the borrowed provider after Close: %v", err)
				}
				if err := injected.Close(); err != nil || injected.closeCount() != 1 {
					t.Errorf("the owner's Close: %v, closes %d", err, injected.closeCount())
				}
			})
		}
	}
}

// noCloseProvider is a provider without a Close method.
type noCloseProvider struct{ calls int }

func (p *noCloseProvider) Model() string { return "no-close" }

func (p *noCloseProvider) Do(context.Context, *llm.Request) (*llm.Result, error) {
	p.calls++
	return textResult(noulAnswer), nil
}

// TestProviderWithoutCloseIsSupported ports
// test_custom_provider_without_close_remains_supported: a provider without
// Close works, borrowed or owned, and Close neither fails nor builds one.
func TestProviderWithoutCloseIsSupported(t *testing.T) {
	tests := map[string]struct {
		opts func(p llm.Provider) []Option
	}{
		"borrowed": {opts: func(p llm.Provider) []Option {
			return []Option{WithProvider("custom", p), WithDefaultModel("custom")}
		}},
		"owned": {opts: func(p llm.Provider) []Option {
			return []Option{WithFactory("custom", factoryOf(p)), WithDefaultModel("custom:m")}
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := &noCloseProvider{}
			c := sdkClient(t, newAdapter(t, tt.opts(p)...), false)
			if err := evaluateOn(t, c); err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
			if p.calls != 1 {
				t.Errorf("provider calls %d, want 1", p.calls)
			}
		})
	}
}

// TestCloseAfterFailureClosesOwnedProviders ports
// test_exceptional_exit_closes_owned_sdks: whatever ends the caller's work
// (its own failure after a call, a provider error, refused questions after
// the provider was built), Close closes the one owned provider. The
// cancelled case is DV2's.
func TestCloseAfterFailureClosesOwnedProviders(t *testing.T) {
	for _, vendor := range vendors {
		for _, failure := range []string{"body", "request", "validation"} {
			t.Run(vendor+", "+failure, func(t *testing.T) {
				var l lifecycle
				ad := l.adapter(t, vendor)
				c := sdkClient(t, ad, false)
				switch failure {
				case "validation":
					status, _, err := send(t, ad, rawRequest(t.Context(), "POST", systemOnePath, `{"state":"document","model":"`+vendor+`:test-model","questions":{}}`))
					if err != nil || status != 422 {
						t.Fatalf("empty questions: %d, %v", status, err)
					}
				case "body", "request":
					if err := evaluateOn(t, c); err != nil {
						t.Fatal(err)
					}
					if failure == "request" {
						l.providers()[0].do = func(context.Context, *llm.Request) (*llm.Result, error) { return nil, &customError{text: "failed"} }
						if status, _, _, _ := apiErrorParts(evaluateOn(t, c)); status != 424 {
							t.Fatalf("a failing request answered %d", status)
						}
					}
				}
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
				built := l.providers()
				if len(built) != 1 || built[0].closeCount() != 1 {
					t.Errorf("providers %d, closes %v; want one closed once", len(built), l.closeLog())
				}
			})
		}
	}
}

// TestCloseContinuesAfterFailure ports test_cleanup_continues_after_failure:
// Close tries every owned provider in build order, returns the first
// error, keeps the providers whose Close failed and refuses calls; a later
// Close closes those again, and a Close after that closes nothing.
func TestCloseContinuesAfterFailure(t *testing.T) {
	for _, vendor := range vendors {
		t.Run(vendor, func(t *testing.T) {
			var l lifecycle
			ad := l.adapter(t, vendor)
			c := sdkClient(t, ad, false)
			for _, m := range []string{"", "another-model", "third-model"} {
				var opts []decision.CallOption
				if m != "" {
					opts = append(opts, decision.Model(m))
				}
				if err := evaluateOn(t, c, opts...); err != nil {
					t.Fatal(err)
				}
			}
			built := l.providers()
			firstErr, secondErr := errors.New("close failed"), errors.New("another close failed")
			for i, err := range []error{firstErr, secondErr} {
				failing := err
				built[i].closeFn = func(n int) error {
					if n == 1 {
						return failing
					}
					return nil
				}
			}
			if err := ad.Close(); err != firstErr { //nolint:errorlint // Close returns the first cleanup error itself.
				t.Fatalf("Close = %v, want the first provider's error itself", err)
			}
			if got := []int{built[0].closeCount(), built[1].closeCount(), built[2].closeCount()}; !slices.Equal(got, []int{1, 1, 1}) {
				t.Errorf("close counts %v, want [1 1 1]", got)
			}
			if diff := gocmp.Diff([]string{vendor + "/third-model"}, l.closeLog()); diff != "" {
				t.Errorf("providers closed (-want +got):\n%s", diff)
			}
			if err := evaluateOn(t, c); !errors.Is(err, ErrClosed) {
				t.Errorf("a call after Close: %v", err)
			}
			if err := ad.Close(); err != nil {
				t.Fatalf("the second Close = %v", err)
			}
			if err := ad.Close(); err != nil {
				t.Fatalf("the third Close = %v", err)
			}
			if got := []int{built[0].closeCount(), built[1].closeCount(), built[2].closeCount()}; !slices.Equal(got, []int{2, 2, 1}) {
				t.Errorf("close counts %v, want [2 2 1]", got)
			}
		})
	}
}

// TestCloseBeforeFirstUse ports
// test_close_before_first_use_does_not_construct_providers: Close twice on
// an Adapter that built nothing, then a call is refused and nothing is
// built.
func TestCloseBeforeFirstUse(t *testing.T) {
	for _, vendor := range vendors {
		t.Run(vendor, func(t *testing.T) {
			var l lifecycle
			ad := l.adapter(t, vendor)
			for range 2 {
				if err := ad.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := send(t, ad, rawRequest(t.Context(), "POST", systemOnePath, `{"state":"s","model":"`+vendor+`:test-model","questions":{"answer":{"type":"noul"}}}`)); !isClosedError(err) {
				t.Errorf("a call after Close: %v", err)
			}
			if len(l.providers()) != 0 {
				t.Errorf("providers built %d, want 0", len(l.providers()))
			}
		})
	}
}

// TestFailedConstructionIsNotCached ports
// test_failed_construction_is_not_cached: a factory that fails builds
// nothing that is kept, and the next calls build one provider and reuse it.
func TestFailedConstructionIsNotCached(t *testing.T) {
	for _, vendor := range vendors {
		t.Run(vendor, func(t *testing.T) {
			var l lifecycle
			l.fail = errors.New("constructor failed")
			c := sdkClient(t, l.adapter(t, vendor), false)
			if status, errorType, message, _ := apiErrorParts(evaluateOn(t, c)); status != 400 || errorType != "provider_config" || message != "constructor failed" {
				t.Fatalf("first call: %d %q %q", status, errorType, message)
			}
			if len(l.providers()) != 0 {
				t.Fatal("a provider is kept after a failed construction")
			}
			for range 2 {
				if err := evaluateOn(t, c); err != nil {
					t.Fatal(err)
				}
			}
			if len(l.providers()) != 1 {
				t.Errorf("providers built %d, want 1", len(l.providers()))
			}
		})
	}
}

// TestFactoryWithoutProviderIsNotCached checks that a factory that returns
// neither a provider nor an error fails each call with 400 provider_config
// and caches nothing, so a later call that gets a provider uses it.
func TestFactoryWithoutProviderIsNotCached(t *testing.T) {
	builds := 0
	p := &closingProvider{model: "m"}
	ad := newAdapter(t, WithFactory("openai", func(string) (llm.Provider, error) {
		builds++
		if builds <= 2 {
			return nil, nil
		}
		return p, nil
	}), WithDefaultModel("openai:m"))
	c := sdkClient(t, ad, false)
	for range 2 {
		if status, errorType, _, _ := apiErrorParts(evaluateOn(t, c)); status != 400 || errorType != "provider_config" {
			t.Fatalf("a factory without a provider: %d %q", status, errorType)
		}
	}
	if err := evaluateOn(t, c); err != nil {
		t.Fatal(err)
	}
	if builds != 3 {
		t.Errorf("builds %d, want 3", builds)
	}
}

// TestEnvironmentIsReadAtConstruction ports
// test_environment_is_captured_on_first_use: a provider reads its settings
// from the environment when it is built, so a change after the first use
// reaches only a provider of a new Adapter.
func TestEnvironmentIsReadAtConstruction(t *testing.T) {
	for _, vendor := range vendors {
		t.Run(vendor, func(t *testing.T) {
			name := "ADAPTER_TEST_" + strings.ToUpper(vendor) + "_SETTING"
			var l lifecycle
			l.configure = func(p *closingProvider) { p.setting = os.Getenv(name) }
			t.Setenv(name, "first")
			c := sdkClient(t, l.adapter(t, vendor), false)
			if err := evaluateOn(t, c); err != nil {
				t.Fatal(err)
			}
			t.Setenv(name, "second")
			if err := evaluateOn(t, c); err != nil {
				t.Fatal(err)
			}
			fresh := sdkClient(t, l.adapter(t, vendor), false)
			if err := evaluateOn(t, fresh); err != nil {
				t.Fatal(err)
			}
			var settings []string
			for _, p := range l.providers() {
				settings = append(settings, p.setting)
			}
			if diff := gocmp.Diff([]string{"first", "second"}, settings); diff != "" {
				t.Errorf("settings read per provider (-want +got):\n%s", diff)
			}
		})
	}
}

// TestConcurrentCloseWaitsForSameCleanup ports
// test_concurrent_close_waits_for_same_cleanup: a Close called while
// another Close's cleanup runs waits for it and returns its result, nil or
// the cleanup's error; the provider is closed once. It runs outside a
// synctest bubble, where a second Close stuck on a lock would never be
// reported: the second Close is known to wait once its stack shows it
// blocked on the cleanup's channel, and every wait is bounded by lockBound.
// The cases after the first failing one are not run, as they would wait as
// long.
func TestConcurrentCloseWaitsForSameCleanup(t *testing.T) {
cases:
	for _, vendor := range vendors {
		for _, fails := range []bool{false, true} {
			ok := t.Run(fmt.Sprintf("%s, failure %v", vendor, fails), func(t *testing.T) {
				var l lifecycle
				ad := l.adapter(t, vendor)
				if status, err := sendWithin(t, ad, rawRequest(t.Context(), "POST", systemOnePath, `{"state":"s","model":"`+vendor+`:test-model","questions":{"answer":{"type":"noul"}}}`)); err != nil || status != http.StatusOK {
					t.Fatalf("the first call: %d %v", status, err)
				}
				started, release := make(chan struct{}), make(chan struct{})
				closeErr := errors.New("close failed")
				l.providers()[0].closeFn = func(int) error {
					close(started)
					<-release
					if fails {
						return closeErr
					}
					return nil
				}
				first, second := make(chan error, 1), make(chan error, 1)
				go func() { first <- ad.Close() }()
				await(t, "the first Close's cleanup", (<-chan struct{})(started))
				go func() { second <- ad.Close() }()
				awaitBlocked(t, "chan receive", "(*Adapter).Close", second)
				close(release)
				want := error(nil)
				if fails {
					want = closeErr
				}
				for i, ch := range []chan error{first, second} {
					if err := await(t, fmt.Sprintf("Close %d", i+1), (<-chan error)(ch)); err != want { //nolint:errorlint // both callers get the cleanup's error itself.
						t.Errorf("Close %d = %v, want %v", i+1, err, want)
					}
				}
				if got := l.providers()[0].closeCount(); got != 1 {
					t.Errorf("provider closes %d, want 1", got)
				}
			})
			if !ok {
				break cases
			}
		}
	}
}

// TestConcurrentFirstUseReusesProvider ports
// test_concurrent_first_use_reuses_pool_and_isolates_traces: eight calls
// that start together build one provider, and each call's Report holds its
// own attempt, its own state and the request its provider recorded.
func TestConcurrentFirstUseReusesProvider(t *testing.T) {
	for _, vendor := range vendors {
		t.Run(vendor, func(t *testing.T) {
			const n = 8
			arrived := make(chan struct{}, n)
			all := make(chan struct{})
			var l lifecycle
			l.configure = func(p *closingProvider) {
				p.do = func(_ context.Context, req *llm.Request) (*llm.Result, error) {
					arrived <- struct{}{}
					<-all
					var b strings.Builder
					for _, m := range req.Messages {
						b.WriteString(string(m.Role) + ":" + m.Content + "\n")
					}
					req.Trace.RecordRequest("offline-test", []byte(strconv.Quote(b.String())))
					return textResult(noulAnswer), nil
				}
			}
			c := sdkClient(t, l.adapter(t, vendor), false)
			go func() {
				for range n {
					<-arrived
				}
				close(all)
			}()
			reports := make(chan *Report, n)
			errs := make(chan error, n)
			questions := noulQuestions(t)
			for i := range n {
				go func() {
					resp, err := c.SystemOne(t.Context(), fmt.Sprintf("document-%d", i), questions)
					if err != nil {
						errs <- err
						return
					}
					r, err := ReportOf(resp)
					if err != nil {
						errs <- err
						return
					}
					reports <- r
				}()
			}
			seen := map[string]bool{}
			timer := time.NewTimer(lockBound)
			defer timer.Stop()
			for range n {
				select {
				case <-timer.C:
					t.Fatalf("the calls did not return within %v; goroutines:\n%s", lockBound, goroutineStacks())
				case err := <-errs:
					t.Fatal(err)
				case r := <-reports:
					if len(r.Debug.Attempts) != 1 || r.Usage.InputTokensTotal.N != 11 || r.Usage.Retries != 0 {
						t.Fatalf("attempts %d, input_tokens_total %v, n_retries %d", len(r.Debug.Attempts), r.Usage.InputTokensTotal, r.Usage.Retries)
					}
					a := r.Debug.Attempts[0]
					user := a.Messages[1].Content
					seen[user] = true
					var b strings.Builder
					for _, m := range a.Messages {
						b.WriteString(string(m.Role) + ":" + m.Content + "\n")
					}
					if string(a.Request) != mustQuote(b.String()) {
						t.Errorf("the attempt's request is another call's: %.80s", a.Request)
					}
				}
			}
			if len(seen) != n {
				t.Errorf("%d distinct states among the attempts, want %d", len(seen), n)
			}
			if got := len(l.providers()); got != 1 {
				t.Errorf("providers built %d, want 1", got)
			}
		})
	}
}

// mustQuote returns s as a JSON string, as strconv.Quote writes it for the
// ASCII texts of these tests.
func mustQuote(s string) string { return strconv.Quote(s) }

// TestCloseWaitsForRunningCalls checks that Close refuses new calls at once
// and waits for the calls already running before it closes the providers
// (DV10). A call blocked in its provider keeps Close waiting, which its
// stack shows blocked in the WaitGroup's Wait; a call made meanwhile is
// refused; and the provider is closed only after the running call
// returned. With 64 goroutines that call while Close runs, each call is
// refused or completes, and no provider request runs after its provider
// was closed. Both run outside a synctest bubble with every wait bounded by
// lockBound, as a call or Close stuck on a lock is never reported inside
// one.
func TestCloseWaitsForRunningCalls(t *testing.T) {
	t.Run("ordering", func(t *testing.T) {
		var (
			mu     sync.Mutex
			events []string
		)
		event := func(e string) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, e)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		var l lifecycle
		l.configure = func(p *closingProvider) {
			p.do = func(context.Context, *llm.Request) (*llm.Result, error) {
				close(entered)
				<-release
				event("call returned")
				return textResult(noulAnswer), nil
			}
			p.closeFn = func(int) error {
				event("provider closed")
				return nil
			}
		}
		ad := l.adapter(t, "openai")
		c := sdkClient(t, ad, false)
		questions := noulQuestions(t)
		called := make(chan error, 1)
		go func() {
			_, err := c.SystemOne(t.Context(), "Great book", questions)
			called <- err
		}()
		await(t, "the call's provider request", (<-chan struct{})(entered))
		closed := make(chan error, 1)
		go func() { closed <- ad.Close() }()
		awaitBlocked(t, "sync.WaitGroup.Wait", "(*Adapter).Close", closed)
		refused := make(chan error, 1)
		go func() { refused <- roundTripError(ad, rawRequest(t.Context(), "POST", systemOnePath, noulBody)) }()
		if err := await(t, "a call while Close waits", (<-chan error)(refused)); !isClosedError(err) {
			t.Errorf("a call while Close waits: %v, want the closed Error", err)
		}
		close(release)
		if err := await(t, "the running call", (<-chan error)(called)); err != nil {
			t.Errorf("the running call: %v", err)
		}
		if err := await(t, "Close", (<-chan error)(closed)); err != nil {
			t.Errorf("Close: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if diff := gocmp.Diff([]string{"call returned", "provider closed"}, events); diff != "" {
			t.Errorf("events (-want +got):\n%s", diff)
		}
	})
	t.Run("stress", func(t *testing.T) {
		const n = 64
		var (
			l         lifecycle
			closedNow sync.Mutex
			isClosed  bool
			late      int
		)
		l.configure = func(p *closingProvider) {
			p.do = func(context.Context, *llm.Request) (*llm.Result, error) {
				closedNow.Lock()
				if isClosed {
					late++
				}
				closedNow.Unlock()
				return textResult(noulAnswer), nil
			}
			p.closeFn = func(int) error {
				closedNow.Lock()
				defer closedNow.Unlock()
				isClosed = true
				return nil
			}
		}
		ad := l.adapter(t, "openai")
		start := make(chan struct{})
		outcomes := make(chan string, n)
		for range n {
			go func() {
				<-start
				status, err := roundTripStatus(ad, rawRequest(t.Context(), "POST", systemOnePath, `{"state":"s","model":"openai:test-model","questions":{"answer":{"type":"noul"}}}`))
				switch {
				case err == nil && status == 200:
					outcomes <- "completed"
				case isClosedError(err):
					outcomes <- "refused"
				default:
					outcomes <- fmt.Sprintf("%d %v", status, err)
				}
			}()
		}
		closeErr := make(chan error, 1)
		go func() {
			<-start
			closeErr <- ad.Close()
		}()
		close(start)
		if err := await(t, "Close", (<-chan error)(closeErr)); err != nil {
			t.Fatalf("Close: %v", err)
		}
		counts := map[string]int{}
		for i := range n {
			counts[await(t, fmt.Sprintf("call %d", i), (<-chan string)(outcomes))]++
		}
		if counts["completed"]+counts["refused"] != n {
			t.Errorf("outcomes %v: every call must complete or be refused", counts)
		}
		closedNow.Lock()
		defer closedNow.Unlock()
		if late != 0 {
			t.Errorf("%d provider requests ran after the provider was closed", late)
		}
		for i, p := range l.providers() {
			if p.closeCount() != 1 {
				t.Errorf("provider %d closed %d times, want 1", i, p.closeCount())
			}
		}
	})
}

// TestCloseAfterProviderPanic checks that a panic in a provider's request
// or in a factory propagates to the caller, as any Go panic does, and
// leaves the Adapter usable: no lock of the Adapter is held after it, a
// later call works, a factory that panicked cached nothing and builds
// again, and Close returns. Each call runs in a goroutine of its own whose
// panic is handed back, outside a synctest bubble, with every wait bounded
// by lockBound.
func TestCloseAfterProviderPanic(t *testing.T) {
	tests := map[string]struct {
		factoryPanics bool
		wantBuilds    int
	}{
		"the provider's request panics": {wantBuilds: 1},
		"the factory panics":            {factoryPanics: true, wantBuilds: 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var l lifecycle
			watchLocks(t)
			factoryCalls, requests := 0, 0
			build := l.factory("openai")
			ad := newAdapter(t, WithDefaultModel("openai:m"), WithFactory("openai", func(model string) (llm.Provider, error) {
				factoryCalls++
				if tt.factoryPanics && factoryCalls == 1 {
					panic("factory panicked")
				}
				p, err := build(model)
				p.(*closingProvider).do = func(context.Context, *llm.Request) (*llm.Result, error) {
					requests++
					if !tt.factoryPanics && requests == 1 {
						panic("request panicked")
					}
					return textResult(noulAnswer), nil
				}
				return p, err
			}))
			c := sdkClient(t, ad, false)
			type outcome struct {
				err      error
				panicked any
			}
			questions := noulQuestions(t)
			call := func(what string) outcome {
				done := make(chan outcome, 1)
				go func() {
					var o outcome
					defer func() {
						o.panicked = recover()
						done <- o
					}()
					_, o.err = c.SystemOne(t.Context(), "Great book", questions)
				}()
				return await(t, what, (<-chan outcome)(done))
			}
			if o := call("the call that panics"); o.panicked == nil {
				t.Errorf("the panic did not reach the caller; the call returned %v", o.err)
			}
			assertUnlocked(t, ad)
			if o := call("a call after the panic"); o.err != nil || o.panicked != nil {
				t.Fatalf("a call after the panic: %v, panic %v", o.err, o.panicked)
			}
			if got := len(l.providers()); got != tt.wantBuilds {
				t.Errorf("providers built %d, want %d", got, tt.wantBuilds)
			}
			closed := make(chan error, 1)
			go func() { closed <- ad.Close() }()
			if err := await(t, "Close", (<-chan error)(closed)); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if got := l.providers()[0].closeCount(); got != 1 {
				t.Errorf("provider closes %d, want 1", got)
			}
		})
	}
}
