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
	"errors"
	"io"
	"log/slog"
	"maps"
	"strconv"
	"sync"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// Adapter answers System One requests with an LLM: the Go form of
// upstream's SystemOneAdapterClient (src/system_one_adapter/_client.py:327-551).
// It is an http.RoundTripper, given to the root SDK with NewClient or
// decision.WithRoundTripper, and an io.Closer, and is safe for concurrent
// use.
//
// It owns the providers it builds with a factory, one per provider name and
// model, built on the first call that needs it and closed by Close; it
// borrows the providers given with WithProvider and never closes them.
type Adapter struct {
	// eval holds the evaluation settings; its retry is the Adapter's
	// default policy.
	eval evalConfig
	// defaultModel is the WithDefaultModel id; defaultName is the provider
	// name it gives as "<name>:<model>", "" when it gives none.
	defaultModel string
	defaultName  string
	providers    map[string]llm.Provider
	providerList []string
	factories    map[string]llm.Factory
	logger       *slog.Logger

	// mu orders each call's entry against Close: it guards closed, the
	// Add of running, and closing. It is not cacheMu, so Close never waits
	// for a factory to take it.
	mu sync.Mutex
	// closed is set by the first Close; a call that finds it set is
	// refused.
	closed bool
	// running counts the calls that passed the closed check and have not
	// returned.
	running sync.WaitGroup
	// closing is the cleanup a Close runs now, which a concurrent Close
	// waits for; nil when none runs.
	closing *cleanup

	// cacheMu guards owned and order, and is held while a factory builds a
	// provider.
	cacheMu sync.Mutex
	owned   map[providerKey]llm.Provider
	// order holds the keys of owned in the order the providers were built.
	order []providerKey
}

// providerKey names an owned provider: its factory name and its model.
type providerKey struct {
	name, model string
}

// cleanup is one run of Close's cleanup; done is closed when err is set.
type cleanup struct {
	done chan struct{}
	err  error
}

// New returns an Adapter that asks for answer mode a with output mode o, as
// upstream requires llm_answer_mode and structured_outputs. It refuses a
// mode that is not one of the two constants of its type, a negative
// WithMalformedRetries, a WithRetry policy whose setting is out of range,
// an invalid WithProvider or WithFactory argument, and a WithDefaultModel
// id that names no provider; the errors of the first two and of the
// default model carry upstream's texts.
func New(a AnswerMode, o OutputMode, opts ...Option) (*Adapter, error) {
	var cfg options
	for _, opt := range opts {
		opt(&cfg)
	}
	switch {
	case cfg.err != nil:
		return nil, cfg.err
	case a != Probabilities && a != Discrete:
		return nil, errors.New("llm_answer_mode must be 'probabilities' or 'discrete'")
	case o != Structured && o != Prompted:
		return nil, errors.New("adapter: output mode " + strconv.Itoa(int(o)) + " is neither Structured nor Prompted")
	case cfg.malformedRetries < 0:
		return nil, errors.New("n_retry_malformed_structure must be >= 0")
	}
	if err := cfg.retry.check(); err != nil {
		return nil, err
	}
	factories := maps.Clone(presets)
	maps.Copy(factories, cfg.factories)
	ad := &Adapter{
		eval:         evalConfig{answer: a, output: o, normalize: cfg.normalize, malformedRetries: cfg.malformedRetries, retry: cfg.retry},
		defaultModel: cfg.defaultModel,
		providers:    cfg.providers,
		providerList: cfg.names,
		factories:    factories,
		logger:       cfg.logger,
		owned:        make(map[providerKey]llm.Provider),
	}
	name, err := ad.checkDefault()
	if err != nil {
		return nil, err
	}
	ad.defaultName = name
	return ad, nil
}

// provider returns the provider of t: the borrowed one, or the owned one
// for its name and model, which the factory builds on first use under
// cacheMu, as upstream builds and caches under its lifecycle lock
// (_client.py:406-408). A factory's error, or a nil provider, leaves no
// entry, so the next call builds again; so does a factory's panic, which
// propagates with cacheMu released.
func (ad *Adapter) provider(t target) (llm.Provider, error) {
	if t.borrowed != nil {
		return t.borrowed, nil
	}
	key := providerKey{name: t.name, model: t.model}
	ad.cacheMu.Lock()
	defer ad.cacheMu.Unlock()
	if p, ok := ad.owned[key]; ok {
		return p, nil
	}
	p, err := ad.factories[t.name](t.model)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errors.New("adapter: the factory " + strconv.Quote(t.name) + " returned no provider for model " + strconv.Quote(t.model))
	}
	ad.owned[key] = p
	ad.order = append(ad.order, key)
	return p, nil
}

// Close refuses new calls, waits for the calls already running to return
// (each is bounded by its own context and the provider timeouts), and then
// closes the providers the Adapter built that are io.Closers, in the order
// they were built. It returns the first cleanup error; a provider whose
// Close failed stays owned and is closed again by the next Close, after
// every other provider was tried (_client.py:505-540). Concurrent calls of
// Close wait for the same cleanup and return its result; a Close after a
// cleanup ended runs a new one. Providers given with WithProvider are not
// closed.
//
// Because it waits, (*decision.Client).Close blocks until running calls
// end; upstream leaves "after all evaluations have finished" to its caller.
// A caller who needs a bounded Close cancels the running calls' contexts
// first; a cancelled call returns ctx.Err() without a Report, and a call
// whose deadline passes returns its timeout Error with the Report.
//
// A mutex of the Adapter's own, not the lock of the provider cache, orders
// each call's entry against Close: RoundTrip checks the closed flag and
// counts itself as running under it, and Close sets the flag under it
// before it waits, as a sync.WaitGroup requires an Add from zero to happen
// before its Wait.
func (ad *Adapter) Close() error {
	ad.mu.Lock()
	ad.closed = true
	if c := ad.closing; c != nil {
		ad.mu.Unlock()
		<-c.done
		return c.err
	}
	c := &cleanup{done: make(chan struct{})}
	ad.closing = c
	ad.mu.Unlock()
	defer func() {
		ad.mu.Lock()
		ad.closing = nil
		ad.mu.Unlock()
		close(c.done)
	}()
	ad.running.Wait()
	c.err = ad.closeOwned()
	return c.err
}

// closeOwned closes every owned provider that is an io.Closer and keeps
// those whose Close failed, returning the first error; a provider that is
// not an io.Closer is dropped, as upstream drops one without close.
func (ad *Adapter) closeOwned() error {
	ad.cacheMu.Lock()
	defer ad.cacheMu.Unlock()
	var first error
	var kept []providerKey
	for _, key := range ad.order {
		c, ok := ad.owned[key].(io.Closer)
		if !ok {
			delete(ad.owned, key)
			continue
		}
		if err := c.Close(); err != nil {
			if first == nil {
				first = err
			}
			kept = append(kept, key)
			continue
		}
		delete(ad.owned, key)
	}
	ad.order = kept
	return first
}
