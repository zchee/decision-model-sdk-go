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
	"bytes"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

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

	// given is set once NewClient has given the Adapter to a client.
	given atomic.Bool

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
	// check is a test hook: nil outside the package's own tests, which set
	// it under mu to see each request answered 200, as the body RoundTrip
	// read, and the bytes of that answer's body as RoundTrip returns them.
	check func(request, response []byte)

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

// errCleanupPanicked is what a Close that waited for another Close's
// cleanup returns when a provider's Close panicked in that cleanup. The
// panic itself propagates in the goroutine whose Close ran the cleanup.
var errCleanupPanicked = errors.New("adapter: a provider's Close panicked while the Adapter closed its providers")

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

// RoundTrip answers one request of the root SDK: POST <base>/v1/systemone
// with an evaluation, GET <base>/v1/models with the model list, and any
// other method or path with 404 {"detail": "Not Found"}. It reads
// req.Method, req.URL.Path and req.Context(), reads req.Body to its end and
// closes it on every path when it is not nil (a GET carries none), and
// reads one header, X-TypeSafe-Retry-Count, which it records in the
// Report; it never writes, copies or keeps req or any of its fields, and
// reads no other header, so the SDK's API key never reaches it. Every
// response carries X-Typesafe-Request-Id, "adp_" and 16 random hex digits,
// and has req as its Request.
//
// A request that fails answers with a status and an error body
// {"detail": {"message", "error_type"}, "usage", "debug"}, usage and debug
// present once the evaluation has started: 400 for a body that is not a
// System One request (invalid_body), a call without a model
// (model_required) or a provider (provider_required), a provider that
// cannot be built (provider_config) and a ContextWithRetry policy out of
// range (invalid_retry); 422 for a state that is null, not a string, object
// or array, or nested too deeply (invalid_state), and for invalid questions
// (invalid_questions); the provider's status after the retries
// (provider_status), 424 when that status is outside 400 to 599; 424 for a
// non-answer (non_answer) and any other provider error (provider_error);
// and 200 with "answers": null for output that still does not match the
// schema after the corrective retries. A provider timeout or connection
// failure after the retries, and a call whose context deadline passed,
// return an *Error with the Report; a cancelled call returns its context's
// error and no Report; a closed Adapter returns an *Error of KindClosed.
func (ad *Adapter) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	var readErr error
	if req.Body != nil {
		defer req.Body.Close()
		body, readErr = io.ReadAll(req.Body)
	}
	ad.mu.Lock()
	if ad.closed {
		ad.mu.Unlock()
		return nil, &Error{Kind: KindClosed}
	}
	ad.running.Add(1)
	check := ad.check
	ad.mu.Unlock()
	defer ad.running.Done()
	id := newRequestID()
	r := ad.serve(req.Context(), id, req.Method, req.URL.Path, req.Header.Get("X-TypeSafe-Retry-Count"), body, readErr)
	if r.err != nil {
		return nil, r.err
	}
	if check != nil && r.status == http.StatusOK {
		check(body, r.body)
	}
	return &http.Response{
		Status:        strconv.Itoa(r.status) + " " + http.StatusText(r.status),
		StatusCode:    r.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        responseHeader(len(r.body), id),
		Body:          io.NopCloser(bytes.NewReader(r.body)),
		ContentLength: int64(len(r.body)),
		Request:       req,
	}, nil
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
//
// A provider whose Close panics is not recovered: the panic propagates in
// the goroutine whose Close ran the cleanup. The providers closed before it
// are no longer owned; it and the providers not yet reached stay owned for
// the next Close; and a concurrent Close that waited for that cleanup
// returns an error of its own, as upstream's waiters raise the cleanup's
// exception (_client.py:516-524).
func (ad *Adapter) Close() error {
	ad.mu.Lock()
	ad.closed = true
	if c := ad.closing; c != nil {
		ad.mu.Unlock()
		<-c.done
		return c.err
	}
	// The result stays errCleanupPanicked unless the cleanup returns.
	c := &cleanup{done: make(chan struct{}), err: errCleanupPanicked}
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
// not an io.Closer is dropped, as upstream drops one without close. When a
// provider's Close panics, the providers closed before it are dropped and
// it and those after it are kept.
func (ad *Adapter) closeOwned() error {
	ad.cacheMu.Lock()
	defer ad.cacheMu.Unlock()
	var first error
	var kept []providerKey
	i := 0
	defer func() { ad.order = append(kept, ad.order[i:]...) }()
	for ; i < len(ad.order); i++ {
		key := ad.order[i]
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
	return first
}
