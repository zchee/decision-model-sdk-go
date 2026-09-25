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

package typesafe

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CallOption configures one call, over the client's settings: [Model],
// [Timeout], [Header], [ExtraBody] and [Retry]. As with [ClientOption], an
// option only records its setting; the call checks them all before it
// encodes or sends anything and reports the first it cannot use as a
// [*ConfigError], in a fixed order whatever the order of the options: a
// System One call checks the model, then the timeout, then the headers; a
// list-models call first refuses Model and ExtraBody, then checks the
// timeout and the headers. Among options of one kind the last one wins,
// except that every [Header] and every [ExtraBody] counts. A nil CallOption
// is ignored.
type CallOption func(*callOptions)

// callOptions is what a list of [CallOption] values recorded. A nil pointer
// is a setting no option gave.
type callOptions struct {
	model   *string
	timeout *time.Duration
	headers []headerOption
	extra   []bodyMember
	retry   *RetryPolicy
}

// Model sets the model this System One call names, in place of the client's
// ([WithModel]). The model is sent as given; an empty or blank model is
// refused rather than sent, and so is one that is not valid UTF-8. An
// [ExtraBody] member named "model" replaces it in the body. List-models calls
// name no model and refuse this option.
func Model(name string) CallOption {
	return func(o *callOptions) { o.model = new(name) }
}

// Timeout sets the deadline of each attempt of this call, in place of the
// client's ([WithTimeout], [WithNoTimeout]). It must be positive, as the
// Python SDK refuses a zero, negative or non-finite per-call timeout.
func Timeout(d time.Duration) CallOption {
	return func(o *callOptions) { o.timeout = new(d) }
}

// Header sets a header this call sends, over the client's: it replaces a
// [WithHeader] of the same name, compared without regard to case, as a
// per-call header replaces a default one in typesafe-sdk-python
// (py:_core/transport.py:117), and a later Header of the same name replaces
// an earlier one.
//
// The rules of WithHeader apply: the SDK's own headers and the transport's
// win, and a caller's value of any of them is dropped and logged at
// [slog.LevelDebug] by name; X-TypeSafe-Retry-Count, which the SDK sets on
// retries only, is dropped too (py:_core/transport.py:118).
//
// The name must be a valid HTTP field name and the value a valid field
// value. A name that contains the API key is refused, when the key is at
// least 8 bytes long (ruling R68). Each of these failures fails the call
// before anything is sent, and no error repeats a value.
func Header(name, value string) CallOption {
	return func(o *callOptions) { o.headers = append(o.headers, headerOption{name: name, value: value}) }
}

// ExtraBody adds the member key with value v to the top level of this System
// One call's request body, as the Python SDK's extra_body does: a key named
// "state", "model" or "questions" replaces that member's value where it
// stands (the replaced value is not encoded at all), any other key is
// appended in the order the options first name it, and a key given twice
// sends its last value. List-models calls have no body and refuse this
// option.
//
// v may be any value JSON can hold, encoded as the call's state is: a
// [RawJSON] is sent as it is after a check of its first byte, a [Content]
// as a question sends it (unset Content as null), a nil as null, and
// anything else by the JSON encoder, with the float spelling and map order
// that [Client.SystemOne] describes for the state. A value JSON has no form
// for, or a plain []byte (whose intent is ambiguous: send string(b) or
// RawJSON(b)), makes the call fail with an [*InvalidRequestError] before
// anything is sent.
//
// An extra "state" follows the rules of the state, so a nil, a number or a
// boolean is refused where typesafe-sdk-python sends it; an extra
// "questions" of nil is sent as null.
func ExtraBody(key string, v any) CallOption {
	return func(o *callOptions) { o.extra = append(o.extra, bodyMember{key: key, value: v}) }
}

// Retry sets the retry policy of this call, in place of the client's.
// [NoRetry] makes the call a single attempt.
func Retry(policy RetryPolicy) CallOption {
	return func(o *callOptions) { o.retry = &policy }
}

// collectCallOptions applies opts in order, skipping nil ones, and returns
// what they recorded. It returns by value and is never inlined, so that the
// options, whose address each option takes, are moved to the heap inside it
// and not in a call that has no options.
//
//go:noinline
func collectCallOptions(opts []CallOption) callOptions {
	var o callOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// callSettings is what one call sends besides its body, once its options
// are checked: the header every attempt starts from, the deadline of each
// attempt and the retry policy.
type callSettings struct {
	// header is the call's header template, shared read-only by its
	// attempts: the client's own when the call sets no header.
	header http.Header
	// timeout is the deadline of each attempt; zero means none.
	timeout time.Duration
	retry   RetryPolicy
}

// settings checks o against the client's configuration and returns what
// every attempt of the call uses: base is the endpoint's header template.
// A failure is a *ConfigError, before anything is encoded or sent; the
// order is timeout, then headers.
func (o *callOptions) settings(ctx context.Context, cfg *config, base http.Header) (callSettings, error) {
	s := callSettings{header: base, timeout: cfg.timeout}
	if o.timeout != nil {
		if *o.timeout <= 0 {
			return callSettings{}, newConfigError("The timeout passed to Timeout must be positive.")
		}
		s.timeout = *o.timeout
	}
	if o.retry != nil {
		s.retry = *o.retry
	}
	if len(o.headers) > 0 {
		h, err := callHeader(ctx, cfg, base, o.headers)
		if err != nil {
			return callSettings{}, err
		}
		s.header = h
	}
	return s, nil
}

// systemOneModel returns the model the call names: its own from [Model], or
// the client's.
func (o *callOptions) systemOneModel(cfg *config) (string, error) {
	if o.model == nil {
		return cfg.model, nil
	}
	if strings.TrimFunc(*o.model, isPythonSpace) == "" {
		return "", newConfigError("The model passed to Model is empty; leave Model out to use the client's model.")
	}
	return *o.model, nil
}

// forModels refuses the options a list-models call cannot use.
func (o *callOptions) forModels() error {
	switch {
	case o.model != nil:
		return newConfigError("Model applies to SystemOne calls only; a list-models call names no model.")
	case len(o.extra) > 0:
		return newConfigError("ExtraBody applies to SystemOne calls only; a list-models call sends no body.")
	}
	return nil
}

// callHeader returns base with the call's headers set over it, checked as
// [options.headerTemplate] checks the client's: a name holding the key is
// refused first, then the name and the value must be valid, and a header the
// SDK or the transport owns is dropped with a debug record. base is not
// modified.
func callHeader(ctx context.Context, cfg *config, base http.Header, headers []headerOption) (http.Header, error) {
	h := base.Clone()
	lowerKey := strings.ToLower(cfg.apiKey)
	for i, ho := range headers {
		if keyNeedle(cfg.apiKey) && strings.Contains(strings.ToLower(ho.name), lowerKey) {
			return nil, newConfigError("The name given to Header call option " + strconv.Itoa(i+1) + " contains the API key, so it is not shown; pass the key with WithAPIKey only.")
		}
		if !validFieldName(ho.name) {
			return nil, newConfigError("The name given to Header call option " + strconv.Itoa(i+1) + " is not a valid HTTP field name (RFC 9110, section 5.6.2); it is not shown, since it may hold a credential.")
		}
		name := http.CanonicalHeaderKey(ho.name)
		if !validFieldValue(ho.value) {
			return nil, newConfigError("The value given to Header for " + name + " is not a valid HTTP field value (RFC 9110, section 5.5).")
		}
		if reason, owned := sdkOwnedHeaders[name]; owned {
			cfg.logger.LogAttrs(ctx, slog.LevelDebug, "call: header dropped",
				slog.String("header", name), slog.String("reason", reason))
			continue
		}
		h[name] = []string{ho.value}
	}
	return h, nil
}
