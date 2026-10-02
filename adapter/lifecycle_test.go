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
	"sync"
	"testing"
	"testing/synctest"

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

// TestClosePanicLeavesTheCleanupConsistent checks a provider whose Close
// panics during a cleanup that a second Close waits for: the panic
// propagates in the goroutine that ran the cleanup; the waiting Close
// returns an error, not nil, as upstream's waiters raise the cleanup's
// exception (src/system_one_adapter/_client.py:516-524); the provider
// closed before the panic is not closed again; and the next Close runs a
// new cleanup over the panicking provider and the one it did not reach.
func TestClosePanicLeavesTheCleanupConsistent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		<-entered
		waited := make(chan error, 1)
		go func() { waited <- ad.Close() }()
		synctest.Wait() // the second Close is blocked, waiting for the first's cleanup
		close(release)
		if got := <-panicked; got != "provider close panicked" {
			t.Fatalf("the first Close's panic = %v, want the provider's", got)
		}
		if err := <-waited; err == nil || err.Error() != "adapter: a provider's Close panicked while the Adapter closed its providers" {
			t.Fatalf("the waiting Close returned %v, want the fixed error", err)
		}
		if err := ad.Close(); err != nil {
			t.Fatalf("the next Close returned %v", err)
		}
		for model, want := range map[string]int{"first": 1, "second": 2, "third": 1} {
			if got := closers[model].closeCount(); got != want {
				t.Errorf("provider %s closed %d times, want %d", model, got, want)
			}
		}
		if err := ad.Close(); err != nil {
			t.Fatalf("a Close after everything was closed returned %v", err)
		}
		if got := closers["second"].closeCount(); got != 2 {
			t.Errorf("provider second closed %d times after a cleanup with nothing owned", got)
		}
	})
}
