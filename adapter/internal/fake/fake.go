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

// Package fake provides a scripted llm.Provider for tests that drive the
// Adapter without a network call. It ports _ScriptedProvider of
// system-one-adapter-python v0.2.1 (tests/test_client_with_fake_model.py:41-98)
// and is imported only by test files.
package fake

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"sync"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// DefaultModel is the model name a Provider reports unless WithModel sets
// another, upstream's "fake-model".
const DefaultModel = "fake-model"

// ErrNoOutcome is the error of a request to a Provider built with no
// outcome.
var ErrNoOutcome = errors.New("fake: no outcome is scripted")

// outcomeKind says what an Outcome does.
type outcomeKind uint8

const (
	kindText outcomeKind = iota + 1
	kindResult
	kindError
	kindPanic
)

// Outcome is one scripted answer of a Provider to a request. Build one with
// Text, Result, Error or Panic; the zero Outcome answers with empty text.
type Outcome struct {
	kind   outcomeKind
	text   string
	result llm.Result
	err    error
	panic  any
}

// Text returns the outcome that answers with text and the Provider's usage,
// as upstream's script answers with a raw string or an encoded dictionary
// (the caller encodes a dictionary itself).
func Text(text string) Outcome { return Outcome{kind: kindText, text: text} }

// Result returns the outcome that answers with r as it is, upstream's
// scripted ProviderResult.
func Result(r llm.Result) Outcome { return Outcome{kind: kindResult, result: r} }

// Error returns the outcome that fails the request with err, upstream's
// scripted exception.
func Error(err error) Outcome { return Outcome{kind: kindError, err: err} }

// Panic returns the outcome that panics with v in the caller's goroutine
// once the request is recorded.
func Panic(v any) Outcome { return Outcome{kind: kindPanic, panic: v} }

// Provider is an llm.Provider that answers each request with the next
// outcome of its script and repeats the last one once the script is
// exhausted, so corrective retries always get an answer, as upstream's
// _ScriptedProvider does. It records every request it receives and every
// call of Close. It is safe for concurrent use; concurrent requests take the
// outcomes in the order the Provider receives them.
type Provider struct {
	mu       sync.Mutex
	model    string
	input    llm.Count
	output   llm.Count
	closeErr error
	steps    []Outcome
	requests []llm.Request
	closes   int
}

var (
	_ llm.Provider = (*Provider)(nil)
	_ io.Closer    = (*Provider)(nil)
)

// New returns a Provider that answers with steps in order. Its model is
// DefaultModel, and a Text outcome reports 11 input and 7 output tokens,
// upstream's default usage.
func New(steps ...Outcome) *Provider {
	return &Provider{
		model:  DefaultModel,
		input:  llm.Count{N: 11, Known: true},
		output: llm.Count{N: 7, Known: true},
		steps:  slices.Clone(steps),
	}
}

// WithModel sets the model name the Provider reports and returns p.
func (p *Provider) WithModel(name string) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.model = name
	return p
}

// WithUsage sets the token counts a Text outcome reports and returns p.
func (p *Provider) WithUsage(input, output llm.Count) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.input, p.output = input, output
	return p
}

// WithCloseError sets the error Close returns and returns p.
func (p *Provider) WithCloseError(err error) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeErr = err
	return p
}

// Model returns the Provider's model name.
func (p *Provider) Model() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.model
}

// Do records a copy of req's messages, schema and structured flag (not its
// Trace, which the Provider never writes), then answers with the next
// outcome: a new Result, the scripted error, or a panic. A Provider without
// outcomes returns ErrNoOutcome. Do ignores ctx, as upstream's fake has none.
func (p *Provider) Do(_ context.Context, req *llm.Request) (*llm.Result, error) {
	p.mu.Lock()
	p.requests = append(p.requests, cloneRequest(req))
	if len(p.steps) == 0 {
		p.mu.Unlock()
		return nil, ErrNoOutcome
	}
	step := p.steps[min(len(p.requests), len(p.steps))-1]
	input, output := p.input, p.output
	p.mu.Unlock()

	switch step.kind {
	case kindError:
		return nil, step.err
	case kindPanic:
		panic(step.panic)
	case kindResult:
		r := step.result
		return &r, nil
	default:
		return &llm.Result{Text: step.text, InputTokens: input, OutputTokens: output}, nil
	}
}

// Close counts the call and returns the error WithCloseError set, or nil.
func (p *Provider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closes++
	return p.closeErr
}

// Requests returns copies of the requests the Provider received, in the
// order it received them, with Trace nil.
func (p *Provider) Requests() []llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]llm.Request, len(p.requests))
	for i := range p.requests {
		out[i] = cloneRequest(&p.requests[i])
	}
	return out
}

// Calls returns the number of requests the Provider received.
func (p *Provider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

// Closes returns the number of calls of Close.
func (p *Provider) Closes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closes
}

// cloneRequest returns a copy of req's messages, schema and structured flag,
// without its Trace; a nil req gives the zero Request.
func cloneRequest(req *llm.Request) llm.Request {
	if req == nil {
		return llm.Request{}
	}
	return llm.Request{Messages: slices.Clone(req.Messages), Schema: bytes.Clone(req.Schema), Structured: req.Structured}
}
