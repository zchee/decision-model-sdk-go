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

// Package llm defines what the Adapter asks of an LLM provider.
//
// A Provider that wants a failure classified by the Adapter (and retried
// where retryable) returns *TimeoutError, *ConnectionError, *StatusError or
// *NonAnswerError; a non-answer is never retried. The Adapter treats any other
// error as a provider error that is not retried (424 provider_error). Whatever
// the Provider returns, a cancelled request context is the caller's
// cancellation and an expired one is a timeout.
//
// For example, a Provider whose own HTTP client timed out returns
// &TimeoutError{Err: err}; a bare context.DeadlineExceeded or *url.Error
// returned while the request's context is still live is any other error.
//
// The package ports the provider-neutral types of system-one-adapter-python
// v0.2.1, src/system_one_adapter/providers/base.py, and the error classes its
// map_provider_error builds (src/system_one_adapter/_utils/error_handling.py).
package llm

import "context"

// Role is a message author: "system", "user" or "assistant".
type Role string

// Message is one chat message in provider-neutral form (upstream's Message,
// providers/base.py:44-49).
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// Request is one model request.
type Request struct {
	// Messages are the conversation sent to the model, in order.
	Messages []Message
	// Schema is the answer schema, canonical compact JSON.
	Schema []byte
	// Structured asks the provider to use its native structured output.
	Structured bool
	// Trace is where the provider records its exchange; the Adapter sets it,
	// and it may be nil, in which case nothing is recorded.
	Trace *Trace
}

// Count is a token count a provider may leave unreported: Known is false
// for upstream's None.
type Count struct {
	N     uint64
	Known bool
}

// Result is a provider's raw answer text and token counts (upstream's
// ProviderResult, providers/base.py:52-58).
type Result struct {
	Text         string
	InputTokens  Count
	OutputTokens Count
}

// Provider performs one model request. Implementations are safe for
// concurrent use.
type Provider interface {
	// Model returns the model name the provider requests.
	Model() string
	// Do performs one model request and returns its raw answer text and
	// usage, or one of this package's error types for a failure the Adapter
	// classifies.
	Do(ctx context.Context, req *Request) (*Result, error)
}

// Factory builds a provider for model; the Adapter owns what it returns and
// closes it when it is an io.Closer.
type Factory func(model string) (Provider, error)
