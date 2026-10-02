// Copyright 2026 The typesafe-sdk-go Authors.
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
	"math"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"time"

	typesafe "github.com/zchee/typesafe-sdk-go"

	"github.com/zchee/typesafe-sdk-go/adapter/llm"
)

// The settings of [DefaultRetry]: typesafe-sdk-python 0.7.1's RetryPolicy()
// (_core/retry.py:52-86).
const (
	defaultMaxRetries     = 2
	defaultBackoffInitial = 500 * time.Millisecond
	defaultBackoffMax     = 5 * time.Second
	defaultBackoffJitter  = 0.25
	defaultRetryBudget    = 30 * time.Second
)

// categoryProviderError is the RetryReason category of a transient provider
// failure.
const categoryProviderError = "provider_error"

// RetryPolicy decides whether the Adapter repeats a provider request that
// failed transiently. The zero value is NoRetry(), upstream's default
// (src/system_one_adapter/_client.py:374).
//
// A policy has the settings of typesafe-sdk-python 0.7.1's RetryPolicy, whose
// retry loop upstream runs every provider request in
// (_utils/error_handling.py:85-95), and the builder names of the TypeSafe Go
// SDK's own policy; it is the Adapter's own type. Each builder returns a
// copy with one setting changed, so a policy can be shared by goroutines
// and derived from freely. A setting the builders have not changed has
// DefaultRetry's value, except the number of retries, which is 0 in the
// zero value: RetryPolicy{}.MaxRetries(2) is DefaultRetry().
//
// After a provider request fails, the Adapter makes another when all of
// these hold, in the order tenacity, which upstream's loop runs on, checks
// them:
//
//   - The error is one the policy retries: an *llm.TimeoutError or an
//     *llm.ConnectionError of a kind it retries, an *llm.StatusError whose
//     status is in its set, or any other error its predicate accepts. An
//     *llm.NonAnswerError is never retried, and its predicate is not asked.
//   - Fewer than MaxRetries retries have been made.
//   - The budget allows the wait: the time since the first request started
//     plus the wait is below the budget.
//
// The wait is the provider's when the policy respects it and the failed
// response carries a retry-after-ms (milliseconds) or Retry-After (seconds,
// or an HTTP date) that parses, as typesafe.APIError.RetryAfter reads it;
// however long it is, only the budget refuses it. Otherwise it is the
// backoff. A cancelled or expired context ends the retries with ctx.Err().
//
// A setting out of range is kept in the value and reported when the policy
// is used, with typesafe-sdk-python's message, before any request is made.
type RetryPolicy struct {
	// _ keeps RetryPolicy incomparable: the predicate and the statuses would
	// otherwise make == compare two policies by their identity.
	_          [0]func()
	maxRetries int
	initial    time.Duration
	maximum    time.Duration
	jitter     float64
	// statuses is sorted and holds each status once; it is shared by copies
	// of the policy and never written after Statuses builds it.
	statuses  []int
	predicate func(error) bool
	budget    time.Duration
	// set marks the settings whose fields replace DefaultRetry's values.
	set retrySetting
	// ignoreRetryAfter, noConnection and noTimeout hold their settings
	// negated, so that false, the zero value, is the default; unbounded is
	// NoBudget.
	ignoreRetryAfter bool
	noConnection     bool
	noTimeout        bool
	unbounded        bool
}

// retrySetting is a set of RetryPolicy settings, one bit each.
type retrySetting uint8

// The settings whose zero value differs from DefaultRetry's.
const (
	setBackoff retrySetting = 1 << iota
	setStatuses
	setBudget
)

// NoRetry never repeats a request: upstream's default, typesafe-sdk-python's
// RetryPolicy(max_retries=0). It is the zero RetryPolicy.
func NoRetry() RetryPolicy { return RetryPolicy{} }

// DefaultRetry is typesafe-sdk-python's RetryPolicy(): 2 retries, backoff 0.5 s
// to 5 s with jitter 0.25, statuses 408, 429 and 500-599, Retry-After
// respected, connection and timeout errors retried, a 30 s budget.
func DefaultRetry() RetryPolicy { return NoRetry().MaxRetries(defaultMaxRetries) }

// MaxRetries returns p with at most n retries after the first attempt
// (max_retries); 0 makes every provider request a single attempt. A
// negative n is refused when the policy is used.
func (p RetryPolicy) MaxRetries(n int) RetryPolicy {
	p.maxRetries = n
	return p
}

// Backoff returns p with delays from initial doubling to maximum, less up to
// jitter of each (backoff_initial, backoff_max, backoff_jitter): the wait
// before retry n (from 1) that no Retry-After decides is initial doubled n-1
// times, at most maximum, less a random fraction of at most jitter of it,
// rounded to the millisecond and never above the doubled value
// (typesafe-sdk-python's _backoff, _core/retry.py:27-33). A zero initial or
// maximum retries at once. initial and maximum must not be negative and
// jitter must be between 0 and 1, or the policy is refused when it is used.
func (p RetryPolicy) Backoff(initial, maximum time.Duration, jitter float64) RetryPolicy {
	p.initial, p.maximum, p.jitter = initial, maximum, jitter
	p.set |= setBackoff
	return p
}

// Statuses returns p retrying a provider response with one of codes, and no
// other status, in place of 408, 429 and 500 to 599 (http_statuses); with
// no codes it retries no status.
func (p RetryPolicy) Statuses(codes ...int) RetryPolicy {
	s := slices.Clone(codes)
	slices.Sort(s)
	p.statuses = slices.Clip(slices.Compact(s))
	p.set |= setStatuses
	return p
}

// RespectRetryAfter returns p that waits as retry-after-ms or Retry-After
// says when on, and with false waits the backoff instead
// (respect_retry_after). The default is true.
func (p RetryPolicy) RespectRetryAfter(on bool) RetryPolicy {
	p.ignoreRetryAfter = !on
	return p
}

// ConnectionErrors returns p that retries a provider connection failure, an
// *llm.ConnectionError, when on (api_connection_error). The default is true.
func (p RetryPolicy) ConnectionErrors(on bool) RetryPolicy {
	p.noConnection = !on
	return p
}

// TimeoutErrors returns p that retries a provider timeout, an
// *llm.TimeoutError, when on (api_timeout_error). The default is true.
func (p RetryPolicy) TimeoutErrors(on bool) RetryPolicy {
	p.noTimeout = !on
	return p
}

// Predicate returns p that also retries an error for which accept returns
// true, on top of the other settings (predicate). It covers upstream's
// exceptions setting too: accept can test an error's type with errors.As.
// accept is called with the error of each failed request that the other
// settings do not retry, the last one's included, except an
// *llm.NonAnswerError, which is never retried; it may be called from
// several goroutines at once. nil removes a predicate.
func (p RetryPolicy) Predicate(accept func(err error) bool) RetryPolicy {
	p.predicate = accept
	return p
}

// Budget returns p that stops before a retry whose delay would reach d since
// the first attempt (timeout): the Adapter makes no retry whose wait would
// bring the time since the first request of the loop started to d or
// beyond, and returns the last request's error instead (tenacity's
// stop_before_delay). d must be positive, or the policy is refused when it
// is used. The default is 30 s.
func (p RetryPolicy) Budget(d time.Duration) RetryPolicy {
	p.budget, p.unbounded = d, false
	p.set |= setBudget
	return p
}

// NoBudget returns p without a total budget (timeout=None): only MaxRetries
// and the call's context bound the retries.
func (p RetryPolicy) NoBudget() RetryPolicy {
	p.budget, p.unbounded = 0, true
	p.set |= setBudget
	return p
}

// retryPolicyError is a RetryPolicy setting out of range.
type retryPolicyError struct{ msg string }

// Error returns typesafe-sdk-python's message for the setting.
func (e *retryPolicyError) Error() string { return e.msg }

// check returns an error for the first setting of p out of range, in the
// order typesafe-sdk-python checks them (_core/retry.py:88-98 and
// _core/config.py:36-39), with its message, or nil.
func (p *RetryPolicy) check() error {
	var msg string
	switch {
	case p.maxRetries < 0:
		msg = "max_retries must be a non-negative integer."
	case p.initial < 0:
		msg = "backoff_initial must be a non-negative, finite number of seconds."
	case p.maximum < 0:
		msg = "backoff_max must be a non-negative, finite number of seconds."
	case !(p.jitter >= 0 && p.jitter <= 1): // false for NaN too
		msg = "backoff_jitter must be between zero and one."
	case !p.unbounded && p.set&setBudget != 0 && p.budget <= 0:
		msg = "timeout must be a positive, finite number of seconds."
	default:
		return nil
	}
	return &retryPolicyError{msg: msg}
}

// retriesStatus reports whether p retries a provider response of status.
func (p *RetryPolicy) retriesStatus(status int) bool {
	if p.set&setStatuses == 0 {
		return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || (status >= 500 && status <= 599)
	}
	_, found := slices.BinarySearch(p.statuses, status)
	return found
}

// retryable reports whether p retries a provider request that failed with
// err (typesafe-sdk-python's _retryable, _core/retry.py:100-109): a timeout
// is tested before a connection failure, as TypeSafeAPITimeoutError is a
// TypeSafeAPIConnectionError there, and the predicate is asked only when
// the other settings do not retry err. A non-answer is never retried.
func (p *RetryPolicy) retryable(err error) bool {
	var (
		timeout    *llm.TimeoutError
		connection *llm.ConnectionError
		status     *llm.StatusError
		nonAnswer  *llm.NonAnswerError
	)
	switch {
	case errors.As(err, &nonAnswer):
		return false
	case errors.As(err, &timeout):
		if !p.noTimeout {
			return true
		}
	case errors.As(err, &connection):
		if !p.noConnection {
			return true
		}
	case errors.As(err, &status):
		if p.retriesStatus(status.StatusCode) {
			return true
		}
	}
	return p.predicate != nil && p.predicate(err)
}

// delay returns the wait before retry (from 1) after a request that failed
// with err: the provider's retry-after-ms or Retry-After when p respects it
// and it parses, read by typesafe.APIError.RetryAfter (typesafe-sdk-python's
// _retry_after and _wait, _core/retry.py:18-24,111-116), else the backoff.
func (p *RetryPolicy) delay(retry int, err error, random func() float64) time.Duration {
	if !p.ignoreRetryAfter {
		if status, ok := errors.AsType[*llm.StatusError](err); ok {
			if d, ok := (&typesafe.APIError{Header: status.Header}).RetryAfter(); ok {
				return d
			}
		}
	}
	initial, maximum, jitter := defaultBackoffInitial, defaultBackoffMax, defaultBackoffJitter
	if p.set&setBackoff != 0 {
		initial, maximum, jitter = p.initial, p.maximum, p.jitter
	}
	return backoff(retry, initial, maximum, jitter, random)
}

// budgetOf returns p's budget and whether it has one.
func (p *RetryPolicy) budgetOf() (time.Duration, bool) {
	switch {
	case p.unbounded:
		return 0, false
	case p.set&setBudget != 0:
		return p.budget, true
	}
	return defaultRetryBudget, true
}

// backoff is typesafe-sdk-python's _backoff (_core/retry.py:27-33) over
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
func roundMillis(seconds float64) float64 {
	var buf [64]byte
	r, _ := strconv.ParseFloat(string(strconv.AppendFloat(buf[:0], seconds, 'f', 3, 64)), 64) // 'f' output always parses
	return r
}

// runWithRetries performs attempt under policy p and returns its result, the
// number of retries made, and the error that ended the loop. It ports
// upstream's run_with_retries (_utils/error_handling.py:85-95) with the
// tenacity loop typesafe-sdk-python's build_tenacity configures:
//
//   - a policy with a setting out of range returns its error before any
//     attempt;
//   - no attempt starts on a context that is done: the loop returns
//     ctx.Err(), also when the context ended during an attempt that failed;
//   - after a failed attempt, an error p does not retry is returned at
//     once; otherwise the wait is computed, and the loop returns the
//     error when MaxRetries retries were made or the budget refuses the
//     wait;
//   - before each wait, record (when not nil) receives one RetryReason
//     {"provider_error", err.Error()}, as upstream's before_sleep records
//     str(error) (error_handling.py:75-82); a context that ends during the
//     wait ends the loop with ctx.Err(), and that reason stays recorded.
//
// random is the backoff's jitter source, a math/rand/v2 Float64; nil means
// rand.Float64.
func runWithRetries(ctx context.Context, p RetryPolicy, random func() float64, attempt func() (*llm.Result, error), record func(RetryReason)) (*llm.Result, int, error) {
	if err := p.check(); err != nil {
		return nil, 0, err
	}
	if random == nil {
		random = rand.Float64
	}
	start := time.Now()
	for retries := 0; ; retries++ {
		if err := ctx.Err(); err != nil {
			return nil, retries, err
		}
		result, err := attempt()
		if err == nil {
			return result, retries, nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return nil, retries, cerr
		}
		if !p.retryable(err) {
			return nil, retries, err
		}
		d := p.delay(retries+1, err, random)
		if retries >= p.maxRetries {
			return nil, retries, err
		}
		// elapsed + d >= budget, written so that a huge d cannot overflow.
		if budget, ok := p.budgetOf(); ok && d >= budget-time.Since(start) {
			return nil, retries, err
		}
		if record != nil {
			record(RetryReason{Category: categoryProviderError, Message: err.Error()})
		}
		if d > 0 {
			t := time.NewTimer(d)
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
				return nil, retries, ctx.Err()
			}
		}
	}
}

// retryContextKey is the context key of ContextWithRetry's policy.
type retryContextKey struct{}

// ContextWithRetry returns a context whose System One calls use p instead of
// the Adapter's policy, upstream's per-call retry argument
// (_client.py:473,499).
func ContextWithRetry(ctx context.Context, p RetryPolicy) context.Context {
	return context.WithValue(ctx, retryContextKey{}, p)
}

// retryPolicyFor returns the policy a call with ctx uses: the one
// ContextWithRetry put in ctx, else def, the Adapter's.
func retryPolicyFor(ctx context.Context, def RetryPolicy) RetryPolicy {
	if p, ok := ctx.Value(retryContextKey{}).(RetryPolicy); ok {
		return p
	}
	return def
}
