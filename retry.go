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
	"errors"
	"math"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"time"
)

// The settings of [DefaultRetry]: typesafe-sdk-python's RetryPolicy()
// (py:_core/retry.py:52-86).
const (
	defaultMaxRetries     = 2
	defaultBackoffInitial = 500 * time.Millisecond
	defaultBackoffMax     = 5 * time.Second
	defaultBackoffJitter  = 0.25
	defaultRetryBudget    = 30 * time.Second
)

// RetryPolicy decides whether a call makes another attempt after one fails,
// and how long it waits first, as typesafe-sdk-python's RetryPolicy does
// (py:_core/retry.py). A policy is a value: each setter returns a copy with
// one setting changed, so a policy can be shared by goroutines and derived
// from freely. [WithRetry] sets a client's policy and [Retry] one call's.
//
// The zero RetryPolicy is [DefaultRetry], the Python SDK's RetryPolicy():
//
//   - at most 2 retries after the first attempt ([RetryPolicy.MaxRetries]);
//   - a wait that starts at 500 ms, doubles with each retry up to 5 s, and
//     loses a random fraction of at most 25 % ([RetryPolicy.Backoff]);
//   - the statuses 408, 429 and 500 to 599 retried ([RetryPolicy.Statuses]);
//   - the server's Retry-After-Ms or Retry-After honoured
//     ([RetryPolicy.RespectRetryAfter]);
//   - a [*ConnectionError] and a [*TimeoutError] retried
//     ([RetryPolicy.ConnectionErrors], [RetryPolicy.TimeoutErrors]);
//   - a budget of 30 s per call ([RetryPolicy.Budget]).
//
// After an attempt fails, the call makes another when all of these hold, in
// the order tenacity, which the Python SDK runs its policy on, checks them
// (tenacity also computes the wait, drawing its jitter, before it counts
// the attempts; the Go loop draws the jitter only for a retry that may
// follow, which no caller can observe):
//
//   - The error is one the policy retries: a *TimeoutError or a
//     *ConnectionError of a kind it retries, an [*APIError] whose status is
//     in its set, or any error its predicate accepts
//     ([RetryPolicy.Predicate]). A successful (2xx) status is never retried
//     by status, even when the set holds it: such a response is billed, so a
//     [*ResponseValidationError] is retried only when the predicate accepts
//     it. A cancellation ([context.Canceled]) is never retried, whatever the
//     predicate says.
//   - Fewer than MaxRetries retries have been made.
//   - The budget allows the wait: the time since the call's first attempt
//     started, which is after the call's options were checked and its body
//     encoded, plus the wait is below the budget (tenacity's
//     stop_before_delay).
//
// The wait is the server's when the policy honours it and the failed
// response carries a Retry-After-Ms (milliseconds) or a Retry-After
// (seconds, or an HTTP date) that parses, whatever the response's status,
// as [APIError.RetryAfter] reads it; however long it is, only the budget
// refuses it. Otherwise it is the backoff.
//
// A retry sends the same body and headers as the first attempt, plus
// X-TypeSafe-Retry-Count with the number of attempts before it, and has a
// deadline of its own ([WithTimeout], [Timeout]). The wait ends early when
// the call's context is done: a cancellation returns ctx.Err(),
// [context.Canceled], itself, and a passed deadline a [*TimeoutError]
// without a timeout. A call that stops returns the error of its last
// attempt. The transport's own replay of a request inside one attempt (an
// HTTP/2 request the server refused or never processed) is neither a retry
// nor counted by [Stats], and runs inside that attempt's deadline.
//
// A setting out of range is kept in the value and reported when the policy
// is used: [NewClient] with [WithRetry], or a call with [Retry], fails with a
// [*ConfigError] that carries the Python SDK's message, before anything is
// sent. The messages name the Python SDK's parameters: max_retries is
// [RetryPolicy.MaxRetries]; backoff_initial, backoff_max and backoff_jitter
// are the arguments of [RetryPolicy.Backoff]; timeout is
// [RetryPolicy.Budget], and timeout=None is [RetryPolicy.NoBudget]. Its
// other parameters are http_statuses ([RetryPolicy.Statuses]),
// respect_retry_after ([RetryPolicy.RespectRetryAfter]),
// api_connection_error ([RetryPolicy.ConnectionErrors]), api_timeout_error
// ([RetryPolicy.TimeoutErrors]) and predicate ([RetryPolicy.Predicate]);
// exceptions has no counterpart. A duration cannot be NaN or infinite, where
// the Python SDK refuses such a number of seconds.
type RetryPolicy struct {
	// _ keeps RetryPolicy incomparable, as its statuses slice and predicate
	// made it before W5.3: the rules pointer would otherwise make == compile
	// and compare two policies by the identity of their rules (review V63).
	// A zero-size first field adds no byte.
	_          [0]func()
	maxRetries int
	initial    time.Duration
	maximum    time.Duration
	jitter     float64
	// rules holds the statuses and the predicate, the settings a policy
	// seldom carries, behind one pointer, so that the policy every call's
	// options hold by value is 56 bytes, not 80 (ruling R97-corr (c), W5.3);
	// nil holds neither. Copies of the policy share it; Statuses and
	// Predicate replace it with a new one, and nothing writes to it after.
	rules  *retryRules
	budget time.Duration
	// set marks the settings whose fields replace DefaultRetry's values;
	// the fields of the others are not read.
	set retrySetting
	// ignoreRetryAfter, noConnection and noTimeout hold their settings
	// negated, so that false, the zero value, is the default; unbounded is
	// NoBudget.
	ignoreRetryAfter bool
	noConnection     bool
	noTimeout        bool
	unbounded        bool
}

// retryRules are a [RetryPolicy]'s statuses and predicate.
type retryRules struct {
	// statuses is sorted and holds each status once.
	statuses  []int
	predicate func(error) bool
}

// withRules returns a copy of p's rules, or new empty ones, for Statuses or
// Predicate to change and store in place of p's.
func (p *RetryPolicy) withRules() *retryRules {
	r := new(retryRules)
	if p.rules != nil {
		*r = *p.rules
	}
	return r
}

// retrySetting is a set of [RetryPolicy] settings, one bit each.
type retrySetting uint8

// The settings whose zero value differs from DefaultRetry's.
const (
	setMaxRetries retrySetting = 1 << iota
	setBackoff
	setStatuses
	setBudget
)

// DefaultRetry returns the policy a client uses unless [WithRetry] sets
// another, as the Python SDK uses RetryPolicy() when its retry argument is
// None: 2 retries, a backoff of 500 ms doubling up to 5 s with a jitter of
// 0.25, the statuses 408, 429 and 500 to 599, Retry-After honoured,
// connection and timeout errors retried, and a budget of 30 s. It is the
// zero RetryPolicy.
func DefaultRetry() RetryPolicy { return RetryPolicy{} }

// NoRetry returns the policy that never tries a call again: every call makes
// exactly one attempt, as the Python SDK's RetryPolicy(max_retries=0) does.
// It is DefaultRetry().MaxRetries(0).
func NoRetry() RetryPolicy { return DefaultRetry().MaxRetries(0) }

// MaxRetries returns p with at most n retries after the first attempt of a
// call; 0 makes every call a single attempt. A negative n is refused when
// the policy is used.
func (p RetryPolicy) MaxRetries(n int) RetryPolicy {
	p.maxRetries = n
	p.set |= setMaxRetries
	return p
}

// Backoff returns p with the wait before retry n (from 1) that no
// Retry-After decides: initial doubled n-1 times, at most maximum, less a
// random fraction of at most jitter of it, rounded to the millisecond and
// never above the doubled value (the Python SDK's _backoff,
// py:_core/retry.py:27-33). A zero initial or maximum retries at once.
// initial and maximum must not be negative and jitter must be between 0 and
// 1, or the policy is refused when it is used.
func (p RetryPolicy) Backoff(initial, maximum time.Duration, jitter float64) RetryPolicy {
	p.initial, p.maximum, p.jitter = initial, maximum, jitter
	p.set |= setBackoff
	return p
}

// Statuses returns p retrying the response statuses codes and no others, in
// place of 408, 429 and 500 to 599; with no codes it retries no status. A
// successful (2xx) status among them is accepted and never retried.
func (p RetryPolicy) Statuses(codes ...int) RetryPolicy {
	s := slices.Clone(codes)
	slices.Sort(s)
	r := p.withRules()
	r.statuses = slices.Clip(slices.Compact(s))
	p.rules = r
	p.set |= setStatuses
	return p
}

// RespectRetryAfter returns p honouring, or with false ignoring, the wait a
// failed response asks for in Retry-After-Ms or Retry-After; the backoff
// applies when it is ignored. The default is true.
func (p RetryPolicy) RespectRetryAfter(respect bool) RetryPolicy {
	p.ignoreRetryAfter = !respect
	return p
}

// ConnectionErrors returns p retrying, or with false not retrying, an
// attempt that failed with a [*ConnectionError]. The default is true.
func (p RetryPolicy) ConnectionErrors(retry bool) RetryPolicy {
	p.noConnection = !retry
	return p
}

// TimeoutErrors returns p retrying, or with false not retrying, an attempt
// that failed with a [*TimeoutError]. The default is true.
func (p RetryPolicy) TimeoutErrors(retry bool) RetryPolicy {
	p.noTimeout = !retry
	return p
}

// Predicate returns p also retrying every error for which accept returns
// true, on top of the rules of the other settings, as the Python SDK's
// predicate does; nil removes a predicate. accept is called with the error
// of each failed attempt that the other rules do not retry, the last
// attempt's included, and may be called from several goroutines at once. It
// is not called for a cancellation ([context.Canceled]), which is never
// retried. The Python SDK's exceptions setting has no counterpart: accept
// can test an error's type with [errors.As].
func (p RetryPolicy) Predicate(accept func(err error) bool) RetryPolicy {
	r := p.withRules()
	r.predicate = accept
	p.rules = r
	return p
}

// Budget returns p with a budget of d for each call: the call makes no
// retry whose wait would bring the time since its first attempt started to
// d or beyond, and returns the error of its last attempt instead
// (tenacity's stop_before_delay). The clock starts once the call's options
// are checked and its body is encoded. d must be positive, or the policy is refused when it
// is used. The default is 30 s; [RetryPolicy.NoBudget] removes the budget.
func (p RetryPolicy) Budget(d time.Duration) RetryPolicy {
	p.budget, p.unbounded = d, false
	p.set |= setBudget
	return p
}

// NoBudget returns p without a budget: only MaxRetries and the caller's
// context bound a call, as the Python SDK's timeout=None does.
func (p RetryPolicy) NoBudget() RetryPolicy {
	p.budget, p.unbounded = 0, true
	p.set |= setBudget
	return p
}

// check returns a *ConfigError for the first setting of p out of range, in
// the order the Python SDK checks them (py:_core/retry.py:88-98), with its
// message, or nil.
func (p *RetryPolicy) check() error {
	switch {
	case p.maxRetries < 0:
		return newConfigError("max_retries must be a non-negative integer.")
	case p.initial < 0:
		return newConfigError("backoff_initial must be a non-negative, finite number of seconds.")
	case p.maximum < 0:
		return newConfigError("backoff_max must be a non-negative, finite number of seconds.")
	case !(p.jitter >= 0 && p.jitter <= 1): // false for NaN too
		return newConfigError("backoff_jitter must be between zero and one.")
	case !p.unbounded && p.set&setBudget != 0 && p.budget <= 0:
		// py:_core/config.py:36-39, resolve_timeout.
		return newConfigError("timeout must be a positive, finite number of seconds.")
	}
	return nil
}

// retries returns the most retries p allows.
func (p *RetryPolicy) retries() int {
	if p.set&setMaxRetries == 0 {
		return defaultMaxRetries
	}
	return p.maxRetries
}

// retriesStatus reports whether p retries a response of status. A 2xx is
// never retried by status (the plan's Appendix B).
func (p *RetryPolicy) retriesStatus(status int) bool {
	switch {
	case status >= 200 && status <= 299:
		return false
	case p.set&setStatuses == 0:
		return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || (status >= 500 && status <= 599)
	}
	var statuses []int
	if p.rules != nil {
		statuses = p.rules.statuses
	}
	_, found := slices.BinarySearch(statuses, status)
	return found
}

// retryable reports whether p retries an attempt that failed with err
// (py:_core/retry.py:100-109, with the Go rules on 2xx and cancellation).
// As in the Python SDK, the predicate is called only for an error the other
// rules do not retry, and for every such failed attempt, the last included.
func (p *RetryPolicy) retryable(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var builtin bool
	switch e := err.(type) { //nolint:errorlint // an attempt returns the SDK's errors unwrapped.
	case *TimeoutError:
		builtin = !p.noTimeout
	case *ConnectionError:
		builtin = !p.noConnection
	case *APIError:
		builtin = p.retriesStatus(e.StatusCode)
	}
	return builtin || (p.rules != nil && p.rules.predicate != nil && p.rules.predicate(err))
}

// retryState is one call's progress through its policy: the loop of a call
// asks it after every failed attempt whether to make another.
type retryState struct {
	policy *RetryPolicy
	// start is when the call's first attempt started (send's start, after
	// the options and the body), which the budget counts from.
	start time.Time
	// random is the backoff's jitter source; nil means math/rand/v2's
	// Float64.
	random func() float64
}

// wait decides whether the call makes another attempt after attempt (0 for
// the first), which failed with err, and waits for it. It returns nil once
// the retry may start, or the error the call returns: err itself when the
// policy stops, or the context's when the call's context ends the wait. No
// retry starts on a context that has ended: a caller's deadline that comes
// no later than the wait's end ends the call at the deadline, and the
// context is checked again when the wait ends. Only a wait that is not zero
// and ends before the caller's deadline makes a timer.
func (r *retryState) wait(ctx context.Context, attempt int, err error) error {
	p := r.policy
	if !p.retryable(err) || attempt >= p.retries() {
		return err
	}
	d := r.delay(attempt+1, err)
	if p.set&setBudget == 0 || !p.unbounded {
		budget := defaultRetryBudget
		if p.set&setBudget != 0 {
			budget = p.budget
		}
		// elapsed + d >= budget, written so that a huge d cannot overflow.
		if d >= budget-time.Since(r.start) {
			return err
		}
	}
	if cerr := ctx.Err(); cerr != nil {
		// An attempt that the caller's deadline ended already reports it.
		if te, ok := err.(*TimeoutError); ok && te.Timeout == 0 && errors.Is(cerr, context.DeadlineExceeded) { //nolint:errorlint // an attempt returns the SDK's errors unwrapped.
			return err
		}
		return waitError(ctx)
	}
	// The context's own timer may not have run yet at its deadline, so the
	// deadline is compared, not only ctx.Err().
	if dl, ok := ctx.Deadline(); ok && !dl.After(time.Now().Add(d)) {
		<-ctx.Done()
		return waitError(ctx)
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	select {
	case <-t.C:
		// A context that ended at the instant the wait did makes no attempt.
		if ctx.Err() != nil {
			return waitError(ctx)
		}
		return nil
	case <-ctx.Done():
		// Since Go 1.23 a stopped timer's channel holds no stale value, so
		// nothing needs draining.
		t.Stop()
		return waitError(ctx)
	}
}

// delay returns the wait before retry (from 1), after an attempt that
// failed with err: the failed response's Retry-After-Ms or Retry-After when
// the policy honours it and it parses (py:_core/retry.py:18-24 and
// :111-116), else the backoff.
func (r *retryState) delay(retry int, err error) time.Duration {
	p := r.policy
	if !p.ignoreRetryAfter {
		if h := responseHeader(err); h != nil {
			if d, ok := retryAfter(h, time.Now()); ok {
				return d
			}
		}
	}
	initial, maximum, jitter := defaultBackoffInitial, defaultBackoffMax, defaultBackoffJitter
	if p.set&setBackoff != 0 {
		initial, maximum, jitter = p.initial, p.maximum, p.jitter
	}
	random := r.random
	if random == nil {
		random = rand.Float64
	}
	return backoff(retry, initial, maximum, jitter, random)
}

// responseHeader returns the header of the response err reports, or nil
// for an error without a response. The Python SDK reads Retry-After from
// every TypeSafeAPIError, a validation error included.
func responseHeader(err error) http.Header {
	switch e := err.(type) { //nolint:errorlint // an attempt returns the SDK's errors unwrapped.
	case *APIError:
		return e.Header
	case *ResponseValidationError:
		return e.Header
	case *ResponseTooLargeError:
		return e.Header
	}
	return nil
}

// waitError returns the error of a call whose context ended a retry's
// wait: ctx.Err() itself for a cancellation (ruling R81 (1)), and a
// *TimeoutError without a timeout for a passed deadline, as an attempt that
// the caller's deadline ends is classified.
func waitError(ctx context.Context) error {
	err := ctx.Err()
	if errors.Is(err, context.DeadlineExceeded) {
		return newTimeoutError(0, err)
	}
	return err
}

// backoff is the Python SDK's _backoff (py:_core/retry.py:27-33) over
// durations: the wait before retry (from 1) is initial doubled retry-1
// times, or maximum once that reaches it, less random() * jitter of it,
// rounded to the millisecond as Python's round(delay, 3) rounds, and never
// above the doubled value. It computes in float64 seconds, as Python does;
// a result at or above maximum is maximum itself, so a maximum near the
// largest Duration cannot overflow.
func backoff(retry int, initial, maximum time.Duration, jitter float64, random func() float64) time.Duration {
	if initial == 0 || maximum == 0 {
		return 0
	}
	lo, hi := initial.Seconds(), maximum.Seconds()
	exponent := retry - 1
	exponential := hi
	if float64(exponent) < math.Log2(hi)-math.Log2(lo) {
		exponential = math.Ldexp(lo, exponent)
	}
	// The conversion rounds the product, so the compiler cannot fuse it
	// with the subtraction into one FMA that Python does not do.
	delay := exponential * (1 - float64(random()*jitter))
	s := min(exponential, roundMillis(delay))
	if s >= hi {
		return maximum
	}
	return time.Duration(math.Round(s * 1e9))
}

// roundMillis rounds seconds to three decimals as Python's round(x, 3)
// does: the exact binary value, half to even, then the nearest float64.
// The formatting buffer stays on the stack for any wait below 10^55 s.
func roundMillis(seconds float64) float64 {
	var buf [64]byte
	r, err := strconv.ParseFloat(string(strconv.AppendFloat(buf[:0], seconds, 'f', 3, 64)), 64)
	if err != nil { // unreachable: 'f' output always parses
		return seconds
	}
	return r
}

// retryCountValues holds the value of X-TypeSafe-Retry-Count for the first
// retries, so an attempt never formats its number (section 6.3: "retry-count
// from a static table"); index n is retry n+1. The value slices have
// len == cap, so a transport that appends to one reallocates instead of
// writing into the table.
var retryCountValues = func() [16][]string {
	var t [16][]string
	for i := range t {
		t[i] = []string{strconv.Itoa(i + 1)}
	}
	return t
}()

// canonicalRetryCount is the canonical form of X-TypeSafe-Retry-Count, the
// key a header map holds it under.
var canonicalRetryCount = http.CanonicalHeaderKey(headerRetryCount)

// retryCountValue returns the X-TypeSafe-Retry-Count value of attempt, which
// is at least 1: the number of attempts before it.
func retryCountValue(attempt int) []string {
	if attempt <= len(retryCountValues) {
		return retryCountValues[attempt-1]
	}
	return []string{strconv.Itoa(attempt)}
}
