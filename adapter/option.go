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
	"log/slog"
	"strconv"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// options are an Adapter's settings as its Options set them, before New
// checks them: upstream's constructor arguments
// (src/system_one_adapter/_client.py:334-383).
type options struct {
	normalize        bool
	malformedRetries int
	retry            RetryPolicy
	defaultModel     string
	// providers holds the WithProvider providers by name, and names their
	// names in the order they were first registered.
	providers map[string]llm.Provider
	names     []string
	// factories holds the WithFactory factories by name.
	factories map[string]llm.Factory
	logger    *slog.Logger
	// err is the first invalid argument an Option was given.
	err error
}

// Option configures an Adapter.
type Option func(*options)

// fail records err when no Option failed before.
func (o *options) fail(err error) {
	if o.err == nil {
		o.err = err
	}
}

// WithNormalizeProbabilities rescales a distribution whose sum is farther
// from 1 than 1e-6 when on (upstream's normalize_probabilities; default
// false).
func WithNormalizeProbabilities(on bool) Option {
	return func(o *options) { o.normalize = on }
}

// WithMalformedRetries sets how many corrective requests the Adapter makes
// after a model answer that does not match the schema
// (n_retry_malformed_structure; default 0). A negative n is an error of New.
func WithMalformedRetries(n int) Option {
	return func(o *options) { o.malformedRetries = n }
}

// WithRetry sets the policy for transient provider failures (default
// NoRetry()). A policy with a setting out of range is an error of New, with
// the message the policy's check gives.
func WithRetry(p RetryPolicy) Option {
	return func(o *options) { o.retry = p }
}

// WithDefaultModel sets the Adapter's default model id, "<provider>:<model>"
// or a WithProvider name. A call whose model is empty uses it, and a call
// with a bare model name uses the provider it names (a WithProvider name
// names no provider). An id that is neither a WithProvider name nor
// "<name>:<model>" with a factory for name and a model that is not empty
// is an error of New. The provider's name and the model name are printed
// in the text of the Adapter's errors and logged in its records, so an id
// must never hold a key.
func WithDefaultModel(id string) Option {
	return func(o *options) { o.defaultModel = id }
}

// WithProvider registers a caller-owned provider under name; a call whose
// model is name uses p. The Adapter borrows p and never closes it. A second
// WithProvider with the same name replaces p. An empty name or a nil p is
// an error of New. name and p's model name are printed in the text of the
// Adapter's errors and logged in its records, so neither may hold a key.
func WithProvider(name string, p llm.Provider) Option {
	return func(o *options) {
		switch {
		case name == "":
			o.fail(errors.New("adapter: WithProvider: the name is empty"))
			return
		case p == nil:
			o.fail(errors.New("adapter: WithProvider " + strconv.Quote(name) + ": the provider is nil"))
			return
		}
		if o.providers == nil {
			o.providers = make(map[string]llm.Provider)
		}
		if _, ok := o.providers[name]; !ok {
			o.names = append(o.names, name)
		}
		o.providers[name] = p
	}
}

// WithFactory sets how the Adapter builds the owned providers named name: a
// call whose model is "<name>:<model>" uses the provider f builds for model,
// built on first use and closed by Close. A second WithFactory with the same
// name replaces f. An empty name or a nil f is an error of New.
func WithFactory(name string, f llm.Factory) Option {
	return func(o *options) {
		switch {
		case name == "":
			o.fail(errors.New("adapter: WithFactory: the name is empty"))
			return
		case f == nil:
			o.fail(errors.New("adapter: WithFactory " + strconv.Quote(name) + ": the factory is nil"))
			return
		}
		if o.factories == nil {
			o.factories = make(map[string]llm.Factory)
		}
		o.factories[name] = f
	}
}

// WithLogger sets the logger of the Adapter's records (default: none). A
// nil l logs nothing.
//
// Each provider attempt gets a record at slog.LevelDebug with request_id
// (the response's X-Typesafe-Request-Id), provider (the name it was given
// or registered under), model, api, attempt (from 1), outcome (ok, status,
// timeout, connection, non_answer, malformed, or error for an error of
// none of those kinds), status for a status outcome, duration, and retry
// (provider_error or malformed_structure) when a retry follows. Each call
// whose evaluation started gets one record at slog.LevelInfo with
// request_id, model, answers (0 when none were written), n_retries,
// n_retries_malformed_structure, latency, and sdk_retry_count when the
// request was a retry of the SDK. No record holds a message, the state, a
// schema, a body, a header, a key or an error's text, and a request
// refused before its evaluation logs nothing. The provider and model in
// every record are the names the call resolved from the model string the
// caller wrote, so a key placed in a model name would be logged, as the
// text of an *Error would print it.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.logger = l }
}
