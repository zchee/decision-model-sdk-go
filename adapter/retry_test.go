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
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/adapter/internal/fake"
	"github.com/zchee/typesafe-sdk-go/adapter/llm"
)

// Every test of the retry loop runs inside a testing/synctest bubble, whose
// clock advances only when every goroutine of the bubble is blocked: a wait
// of the loop takes no wall-clock time, and the times each test reads with
// time.Now are exact.

// unavailable returns the provider error upstream's tests script: a 503
// whose body is {"m": "unavailable"} (system-one-adapter-python v0.2.1,
// tests/utils/test_error_handling.py:111).
func unavailable() *llm.StatusError {
	return &llm.StatusError{StatusCode: http.StatusServiceUnavailable, Body: []byte(`{"m":"unavailable"}`)}
}

// statusError returns a provider status error with an empty body and the
// headers given as name, value pairs.
func statusError(code int, header ...string) *llm.StatusError {
	h := make(http.Header)
	for i := 0; i+1 < len(header); i += 2 {
		h.Set(header[i], header[i+1])
	}
	return &llm.StatusError{StatusCode: code, Header: h}
}

// errCustom is a provider's own error type, which only a predicate retries.
type errCustom struct{ msg string }

func (e *errCustom) Error() string { return e.msg }

// loop is what one run of the retry loop did.
type loop struct {
	result  *llm.Result
	retries int
	err     error
	reasons []RetryReason
	// starts are the times each attempt started, since the loop started.
	starts []time.Duration
	// elapsed is the time the loop took.
	elapsed time.Duration
}

// attempts returns the number of attempts the loop made.
func (l *loop) attempts() int { return len(l.starts) }

// runLoop runs runWithRetries inside the caller's bubble with p, the jitter
// source random, and a fake provider scripted with steps; before is called
// at the start of each attempt with its number (from 1) and may block or
// cancel. It returns what the loop did and the provider.
func runLoop(ctx context.Context, p RetryPolicy, random func() float64, before func(n int), steps ...fake.Outcome) (loop, *fake.Provider) {
	prov := fake.New(steps...)
	var l loop
	start := time.Now()
	attempt := func() (*llm.Result, error) {
		l.starts = append(l.starts, time.Since(start))
		if before != nil {
			before(len(l.starts))
		}
		return prov.Do(ctx, &llm.Request{})
	}
	l.result, l.retries, l.err = runWithRetries(ctx, p, random, attempt, func(r RetryReason) { l.reasons = append(l.reasons, r) })
	l.elapsed = time.Since(start)
	return l, prov
}

// ms returns n milliseconds.
func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// TestRetrySucceedsAfterTransientError ports
// test_retries_succeed_after_transient_error (system-one-adapter-python
// v0.2.1, tests/utils/test_error_handling.py:104-121): a 503 then a success
// under max_retries=1, backoff_initial=0.001, backoff_jitter=0 gives the
// success, two calls and one retry. The bubble's clock also shows the one
// wait of 1 ms.
func TestRetrySucceedsAfterTransientError(t *testing.T) {
	tests := map[string]struct {
		policy RetryPolicy
	}{
		"test_retries_succeed_after_transient_error": {
			policy: DefaultRetry().MaxRetries(1).Backoff(time.Millisecond, defaultBackoffMax, 0),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				l, prov := runLoop(t.Context(), tt.policy, nil, nil, fake.Error(unavailable()), fake.Text("success"))
				if l.err != nil {
					t.Fatalf("err = %v, want nil", l.err)
				}
				if got, want := l.result.Text, "success"; got != want {
					t.Errorf("result = %q, want %q", got, want)
				}
				if got, want := prov.Calls(), 2; got != want {
					t.Errorf("calls = %d, want %d", got, want)
				}
				if got, want := l.retries, 1; got != want {
					t.Errorf("n_retries = %d, want %d", got, want)
				}
				if diff := cmp.Diff([]time.Duration{0, ms(1)}, l.starts); diff != "" {
					t.Errorf("attempt start times mismatch (-want +got):\n%s", diff)
				}
			})
		})
	}
}

// TestNonRetryableErrorIsNotRetried ports
// test_non_retryable_error_is_not_retried (system-one-adapter-python v0.2.1,
// tests/utils/test_error_handling.py:124-138): a 400 under max_retries=2 is
// returned after one call, with no wait and no retry reason.
func TestNonRetryableErrorIsNotRetried(t *testing.T) {
	tests := map[string]struct {
		policy RetryPolicy
	}{
		"test_non_retryable_error_is_not_retried": {
			policy: DefaultRetry().MaxRetries(2).Backoff(time.Millisecond, defaultBackoffMax, 0),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				bad := &llm.StatusError{StatusCode: http.StatusBadRequest, Body: []byte(`{"m":"bad"}`)}
				l, prov := runLoop(t.Context(), tt.policy, nil, nil, fake.Error(bad))
				var se *llm.StatusError
				if !errors.As(l.err, &se) || se != bad {
					t.Fatalf("err = %v, want the 400 *llm.StatusError itself", l.err)
				}
				if got, want := prov.Calls(), 1; got != want {
					t.Errorf("calls = %d, want %d", got, want)
				}
				if l.elapsed != 0 || len(l.reasons) != 0 || l.retries != 0 {
					t.Errorf("elapsed %v, reasons %v, retries %d; want no wait, no reason, no retry", l.elapsed, l.reasons, l.retries)
				}
			})
		})
	}
}

// TestRetriesExhaustedRecordReasons ports
// test_retries_are_exhausted_and_reasons_recorded (system-one-adapter-python
// v0.2.1, tests/utils/test_error_handling.py:141-157): a 503 on every call
// under max_retries=2 is returned after three calls, and two retry reasons
// of category provider_error are recorded. It also checks each reason's
// message, the failed attempt's error text (upstream records str(error)),
// and the two waits of 1 ms and 2 ms.
func TestRetriesExhaustedRecordReasons(t *testing.T) {
	tests := map[string]struct {
		policy RetryPolicy
	}{
		"test_retries_are_exhausted_and_reasons_recorded": {
			policy: DefaultRetry().MaxRetries(2).Backoff(time.Millisecond, defaultBackoffMax, 0),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				l, prov := runLoop(t.Context(), tt.policy, nil, nil, fake.Error(unavailable()))
				var se *llm.StatusError
				if !errors.As(l.err, &se) || se.StatusCode != http.StatusServiceUnavailable {
					t.Fatalf("err = %v, want the 503 *llm.StatusError", l.err)
				}
				if got, want := prov.Calls(), 3; got != want {
					t.Errorf("calls = %d, want %d", got, want)
				}
				var categories []string
				for _, r := range l.reasons {
					categories = append(categories, r.Category)
				}
				if diff := cmp.Diff([]string{"provider_error", "provider_error"}, categories); diff != "" {
					t.Errorf("reason categories mismatch (-want +got):\n%s", diff)
				}
				want := []RetryReason{
					{Category: "provider_error", Message: `503 {"m":"unavailable"}`},
					{Category: "provider_error", Message: `503 {"m":"unavailable"}`},
				}
				if diff := cmp.Diff(want, l.reasons); diff != "" {
					t.Errorf("reasons mismatch (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff([]time.Duration{0, ms(1), ms(3)}, l.starts); diff != "" {
					t.Errorf("attempt start times mismatch (-want +got):\n%s", diff)
				}
			})
		})
	}
}

// TestRetryPolicyBudget checks the budget as tenacity's stop_before_delay
// applies it: no retry whose wait would bring the time since the first
// attempt started to the budget or beyond, the time attempts take counted,
// the boundary itself refused, the 30 s default, and NoBudget.
func TestRetryPolicyBudget(t *testing.T) {
	fixed := func(d time.Duration) RetryPolicy { return DefaultRetry().MaxRetries(10).Backoff(d, d, 0) }
	tests := map[string]struct {
		policy      RetryPolicy
		attemptTime time.Duration
		wantStarts  []time.Duration
	}{
		"success: stops before a wait that would pass the budget": {
			policy:     fixed(ms(400)).Budget(time.Second),
			wantStarts: []time.Duration{0, ms(400), ms(800)},
		},
		"success: a wait that would reach the budget exactly is refused": {
			policy:     fixed(ms(500)).Budget(time.Second),
			wantStarts: []time.Duration{0, ms(500)},
		},
		"success: the time attempts take counts": {
			policy:      fixed(ms(400)).Budget(time.Second),
			attemptTime: ms(300),
			wantStarts:  []time.Duration{0, ms(700)},
		},
		"success: the default budget is 30 s": {
			policy:     fixed(20 * time.Second),
			wantStarts: []time.Duration{0, 20 * time.Second},
		},
		"success: NoBudget leaves only MaxRetries": {
			policy:     fixed(20 * time.Second).MaxRetries(3).NoBudget(),
			wantStarts: []time.Duration{0, 20 * time.Second, 40 * time.Second, 60 * time.Second},
		},
		"success: Budget after NoBudget restores a budget": {
			policy:     fixed(20 * time.Second).NoBudget().Budget(30 * time.Second),
			wantStarts: []time.Duration{0, 20 * time.Second},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				before := func(int) { time.Sleep(tt.attemptTime) }
				l, _ := runLoop(t.Context(), tt.policy, nil, before, fake.Error(unavailable()))
				if diff := cmp.Diff(tt.wantStarts, l.starts); diff != "" {
					t.Errorf("attempt start times mismatch (-want +got):\n%s", diff)
				}
				if !errors.As(l.err, new(*llm.StatusError)) {
					t.Errorf("err = %v, want the last attempt's *llm.StatusError", l.err)
				}
				if got, want := len(l.reasons), len(tt.wantStarts)-1; got != want {
					t.Errorf("%d reasons, want %d: a retry the budget refuses records none", got, want)
				}
			})
		})
	}
}

// TestRetryPolicyBackoff checks the backoff against typesafe-sdk-python's
// _backoff (_core/retry.py:27-33) at its bounds: the doubling from initial,
// the maximum, the jitter's full range (random() of 0 keeps the doubled
// value; of 1 removes jitter of it), the rounding to whole milliseconds,
// and a zero initial or maximum.
func TestRetryPolicyBackoff(t *testing.T) {
	tests := map[string]struct {
		initial, maximum time.Duration
		jitter, random   float64
		want             []time.Duration // retries 1, 2, ...
	}{
		"success: DefaultRetry's values without jitter drawn": {
			initial: defaultBackoffInitial, maximum: defaultBackoffMax, jitter: defaultBackoffJitter, random: 0,
			want: []time.Duration{ms(500), time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second},
		},
		"success: DefaultRetry's values with the whole jitter": {
			initial: defaultBackoffInitial, maximum: defaultBackoffMax, jitter: defaultBackoffJitter, random: 1,
			want: []time.Duration{ms(375), ms(750), ms(1500), ms(3000), ms(3750), ms(3750)},
		},
		"success: half the jitter": {
			initial: ms(100), maximum: time.Second, jitter: 0.5, random: 0.5,
			want: []time.Duration{ms(75), ms(150), ms(300), ms(600), ms(750)},
		},
		"success: rounded to the millisecond, never above the doubled value": {
			initial: time.Millisecond, maximum: time.Second, jitter: 0.5, random: 0.3,
			want: []time.Duration{ms(1), ms(2), ms(3), ms(7)},
		},
		"success: initial above maximum gives maximum": {
			initial: 10 * time.Second, maximum: time.Second, jitter: 0, random: 0,
			want: []time.Duration{time.Second, time.Second},
		},
		"success: zero initial retries at once": {
			initial: 0, maximum: time.Second, jitter: 0.25, random: 0.5,
			want: []time.Duration{0, 0},
		},
		"success: zero maximum retries at once": {
			initial: time.Second, maximum: 0, jitter: 0.25, random: 0.5,
			want: []time.Duration{0, 0},
		},
		"success: a maximum of the largest Duration does not overflow": {
			initial: time.Duration(math.MaxInt64), maximum: time.Duration(math.MaxInt64), jitter: 0.25, random: 0,
			want: []time.Duration{time.Duration(math.MaxInt64), time.Duration(math.MaxInt64)},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var got []time.Duration
			for retry := 1; retry <= len(tt.want); retry++ {
				got = append(got, backoff(retry, tt.initial, tt.maximum, tt.jitter, func() float64 { return tt.random }))
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("backoff mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRetryPolicyBackoffJitterBounds draws the backoff with a seeded
// math/rand/v2 source for DefaultRetry's values and checks every delay: a
// whole number of milliseconds, at most the doubled value d, and at least
// d*(1-jitter) rounded to the millisecond.
func TestRetryPolicyBackoffJitterBounds(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: a seeded jitter source, not cryptography.
	for retry := 1; retry <= 6; retry++ {
		exp := min(defaultBackoffInitial<<(retry-1), defaultBackoffMax)
		low := time.Duration(math.Round(float64(exp)*(1-defaultBackoffJitter)/1e6)) * time.Millisecond
		for range 1000 {
			d := backoff(retry, defaultBackoffInitial, defaultBackoffMax, defaultBackoffJitter, r.Float64)
			if d%time.Millisecond != 0 || d > exp || d < low {
				t.Fatalf("retry %d: delay %v; want a whole millisecond in [%v, %v]", retry, d, low, exp)
			}
		}
	}
}

// TestRetryPolicyBackoffWaits checks that the loop waits the backoff: with a
// jitter source that always returns 0.5, the attempts of a policy with
// initial 100 ms, maximum 1 s and jitter 0.5 start at 0, 75, 225 and 525 ms.
// A seeded math/rand/v2 source gives the same waits as backoff computes
// from an equally seeded one.
func TestRetryPolicyBackoffWaits(t *testing.T) {
	p := DefaultRetry().MaxRetries(3).Backoff(ms(100), time.Second, 0.5)
	t.Run("success: fixed jitter source", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, _ := runLoop(t.Context(), p, func() float64 { return 0.5 }, nil, fake.Error(unavailable()))
			if diff := cmp.Diff([]time.Duration{0, ms(75), ms(225), ms(525)}, l.starts); diff != "" {
				t.Errorf("attempt start times mismatch (-want +got):\n%s", diff)
			}
		})
	})
	t.Run("success: seeded jitter source", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			// Two equally seeded sources: one drives the loop, the other the expectation.
			loopSource := rand.New(rand.NewPCG(7, 9)) //nolint:gosec // G404: a seeded jitter source, not cryptography.
			ref := rand.New(rand.NewPCG(7, 9))        //nolint:gosec // G404: a seeded jitter source, not cryptography.
			l, _ := runLoop(t.Context(), p, loopSource.Float64, nil, fake.Error(unavailable()))
			want := []time.Duration{0}
			for retry := 1; retry <= 3; retry++ {
				want = append(want, want[len(want)-1]+backoff(retry, ms(100), time.Second, 0.5, ref.Float64))
			}
			if diff := cmp.Diff(want, l.starts); diff != "" {
				t.Errorf("attempt start times mismatch (-want +got):\n%s", diff)
			}
		})
	})
}

// TestRetryPolicyRetryAfter checks the wait a provider asks for, read by
// typesafe.APIError.RetryAfter: Retry-After in seconds and as an HTTP date,
// retry-after-ms, a value that does not parse (the backoff then), a wait
// longer than the backoff's maximum (not capped), a wait the budget refuses,
// Retry-After on an error that is not a status error, and the setting off.
// The backoff is 1 ms, so any other wait came from the header.
func TestRetryPolicyRetryAfter(t *testing.T) {
	base := DefaultRetry().MaxRetries(1).Backoff(time.Millisecond, 5*time.Second, 0)
	tests := map[string]struct {
		policy RetryPolicy
		// err builds the attempt's error inside the bubble, so that an HTTP
		// date can be written from the bubble's clock.
		err        func() error
		wantStarts []time.Duration
	}{
		"success: Retry-After in seconds": {
			policy:     base,
			err:        func() error { return statusError(429, "Retry-After", "2") },
			wantStarts: []time.Duration{0, 2 * time.Second},
		},
		"success: Retry-After as an HTTP date": {
			policy: base,
			err: func() error {
				return statusError(503, "Retry-After", time.Now().Add(3*time.Second).UTC().Format(http.TimeFormat))
			},
			wantStarts: []time.Duration{0, 3 * time.Second},
		},
		"success: retry-after-ms": {
			policy:     base,
			err:        func() error { return statusError(503, "retry-after-ms", "1500") },
			wantStarts: []time.Duration{0, ms(1500)},
		},
		"success: retry-after-ms before Retry-After": {
			policy:     base,
			err:        func() error { return statusError(503, "retry-after-ms", "250", "Retry-After", "9") },
			wantStarts: []time.Duration{0, ms(250)},
		},
		"success: a value that does not parse gives the backoff": {
			policy:     base,
			err:        func() error { return statusError(503, "Retry-After", "soon") },
			wantStarts: []time.Duration{0, ms(1)},
		},
		"success: not capped by the backoff's maximum": {
			policy:     base,
			err:        func() error { return statusError(503, "Retry-After", "10") },
			wantStarts: []time.Duration{0, 10 * time.Second},
		},
		"success: a wait the budget refuses ends the loop": {
			policy:     base,
			err:        func() error { return statusError(429, "Retry-After", "60") },
			wantStarts: []time.Duration{0},
		},
		"success: a wrapped status error keeps its header": {
			policy:     base,
			err:        func() error { return fmt.Errorf("openai: %w", statusError(429, "Retry-After", "2")) },
			wantStarts: []time.Duration{0, 2 * time.Second},
		},
		"success: ignored when off": {
			policy:     base.RespectRetryAfter(false),
			err:        func() error { return statusError(429, "Retry-After", "2") },
			wantStarts: []time.Duration{0, ms(1)},
		},
		"success: on again after off": {
			policy:     base.RespectRetryAfter(false).RespectRetryAfter(true),
			err:        func() error { return statusError(429, "Retry-After", "2") },
			wantStarts: []time.Duration{0, 2 * time.Second},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				l, _ := runLoop(t.Context(), tt.policy, nil, nil, fake.Error(tt.err()))
				if diff := cmp.Diff(tt.wantStarts, l.starts); diff != "" {
					t.Errorf("attempt start times mismatch (-want +got):\n%s", diff)
				}
			})
		})
	}
}

// TestRetryPolicyPredicate checks the predicate (upstream's predicate and,
// through errors.As, its exceptions): it retries an error the other
// settings do not, it is asked for every such failed attempt, the last
// included, and not for one they retry; a non-answer is never retried and
// never shown to it; nil removes it.
func TestRetryPolicyPredicate(t *testing.T) {
	base := DefaultRetry().MaxRetries(2).Backoff(time.Millisecond, time.Second, 0)
	isCustom := func(err error) bool { return errors.As(err, new(*errCustom)) }
	tests := map[string]struct {
		accept       func(error) bool
		removed      bool
		err          error
		wantAttempts int
		wantAsked    int
	}{
		"success: accepts a provider's own error type": {
			accept: isCustom, err: &errCustom{msg: "flaky"}, wantAttempts: 3, wantAsked: 3,
		},
		"success: accepts it wrapped": {
			accept: isCustom, err: fmt.Errorf("provider: %w", &errCustom{msg: "flaky"}), wantAttempts: 3, wantAsked: 3,
		},
		"success: rejects another error": {
			accept: isCustom, err: errors.New("other"), wantAttempts: 1, wantAsked: 1,
		},
		"success: retries a status outside the set": {
			accept: func(err error) bool { return errors.As(err, new(*llm.StatusError)) }, err: statusError(409), wantAttempts: 3, wantAsked: 3,
		},
		"success: not asked for an error the settings retry": {
			accept: func(error) bool { return false }, err: unavailable(), wantAttempts: 3, wantAsked: 0,
		},
		"success: a non-answer is never retried nor shown to it": {
			accept: func(error) bool { return true }, err: &llm.NonAnswerError{Message: "refused"}, wantAttempts: 1, wantAsked: 0,
		},
		"success: nil removes the predicate": {
			accept: func(error) bool { return true }, removed: true, err: errors.New("other"), wantAttempts: 1, wantAsked: 0,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				asked := 0
				p := base.Predicate(func(err error) bool { asked++; return tt.accept(err) })
				if tt.removed {
					p = p.Predicate(nil)
				}
				l, _ := runLoop(t.Context(), p, nil, nil, fake.Error(tt.err))
				if got := l.attempts(); got != tt.wantAttempts {
					t.Errorf("%d attempts, want %d", got, tt.wantAttempts)
				}
				if asked != tt.wantAsked {
					t.Errorf("predicate asked %d times, want %d", asked, tt.wantAsked)
				}
				if !errors.Is(l.err, tt.err) {
					t.Errorf("err = %v, want %v", l.err, tt.err)
				}
			})
		})
	}
}

// TestRetryPolicyStatuses checks which provider statuses are retried: by
// default typesafe-sdk-python's set, 408, 429 and 500 to 599 (529 included,
// so Anthropic's overload is retried; 409 not), and with Statuses exactly
// the codes given.
func TestRetryPolicyStatuses(t *testing.T) {
	base := DefaultRetry().MaxRetries(1).Backoff(time.Millisecond, time.Second, 0)
	tests := map[string]struct {
		policy  RetryPolicy
		retried []int
		not     []int
	}{
		"success: the default set": {
			policy:  base,
			retried: []int{408, 429, 500, 502, 503, 504, 529, 599},
			not:     []int{400, 401, 403, 404, 409, 418, 422, 499, 600},
		},
		"success: codes given replace the set": {
			policy:  base.Statuses(409, 503, 409),
			retried: []int{409, 503},
			not:     []int{408, 429, 500, 529},
		},
		"success: no codes retry no status": {
			policy: base.Statuses(),
			not:    []int{408, 429, 500, 503},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				check := func(code, want int) {
					l, _ := runLoop(t.Context(), tt.policy, nil, nil, fake.Error(statusError(code)))
					if got := l.attempts(); got != want {
						t.Errorf("status %d: %d attempts, want %d", code, got, want)
					}
				}
				for _, code := range tt.retried {
					check(code, 2)
				}
				for _, code := range tt.not {
					check(code, 1)
				}
			})
		})
	}
}

// TestRetryPolicyStatusesCopies checks that Statuses keeps its own copy of
// the codes and that deriving a policy leaves the original's set alone.
func TestRetryPolicyStatusesCopies(t *testing.T) {
	codes := []int{503}
	p := DefaultRetry().Statuses(codes...)
	codes[0] = 400
	q := p.Statuses(409)
	if !p.retriesStatus(503) || p.retriesStatus(400) || p.retriesStatus(409) {
		t.Errorf("p retries 503 %t, 400 %t, 409 %t; want true, false, false", p.retriesStatus(503), p.retriesStatus(400), p.retriesStatus(409))
	}
	if !q.retriesStatus(409) || q.retriesStatus(503) {
		t.Errorf("q retries 409 %t, 503 %t; want true, false", q.retriesStatus(409), q.retriesStatus(503))
	}
}

// TestRetryPolicyConnectionAndTimeoutErrors checks that a provider
// connection failure and a provider timeout, bare or wrapped, are retried by
// default and not when their setting is off, each setting acting on its own
// class only.
func TestRetryPolicyConnectionAndTimeoutErrors(t *testing.T) {
	base := DefaultRetry().MaxRetries(1).Backoff(time.Millisecond, time.Second, 0)
	conn := &llm.ConnectionError{Err: errors.New("connection refused")}
	timeout := &llm.TimeoutError{Err: context.DeadlineExceeded}
	tests := map[string]struct {
		policy       RetryPolicy
		err          error
		wantAttempts int
	}{
		"success: connection failure retried by default":       {policy: base, err: conn, wantAttempts: 2},
		"success: wrapped connection failure retried":          {policy: base, err: fmt.Errorf("gemini: %w", conn), wantAttempts: 2},
		"success: connection failure not retried when off":     {policy: base.ConnectionErrors(false), err: conn, wantAttempts: 1},
		"success: connection setting leaves timeouts retried":  {policy: base.ConnectionErrors(false), err: timeout, wantAttempts: 2},
		"success: connection failure retried when on again":    {policy: base.ConnectionErrors(false).ConnectionErrors(true), err: conn, wantAttempts: 2},
		"success: timeout retried by default":                  {policy: base, err: timeout, wantAttempts: 2},
		"success: wrapped timeout retried":                     {policy: base, err: fmt.Errorf("anthropic: %w", timeout), wantAttempts: 2},
		"success: timeout not retried when off":                {policy: base.TimeoutErrors(false), err: timeout, wantAttempts: 1},
		"success: timeout setting leaves connections retried":  {policy: base.TimeoutErrors(false), err: conn, wantAttempts: 2},
		"success: a bare context.DeadlineExceeded is not one":  {policy: base, err: context.DeadlineExceeded, wantAttempts: 1},
		"success: a non-answer is not retried by any settings": {policy: base, err: &llm.NonAnswerError{Message: "cut off"}, wantAttempts: 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				l, _ := runLoop(t.Context(), tt.policy, nil, nil, fake.Error(tt.err))
				if got := l.attempts(); got != tt.wantAttempts {
					t.Errorf("%d attempts, want %d", got, tt.wantAttempts)
				}
				if !errors.Is(l.err, tt.err) {
					t.Errorf("err = %v, want %v", l.err, tt.err)
				}
			})
		})
	}
}

// TestRetryPolicyNoRetry checks that NoRetry and the zero RetryPolicy make
// exactly one attempt, that DefaultRetry makes three on a retried error, and
// that the zero value with MaxRetries(2) behaves as DefaultRetry, with the
// default backoff.
func TestRetryPolicyNoRetry(t *testing.T) {
	half := func() float64 { return 0.5 }
	defaultStarts := []time.Duration{0, ms(438), ms(438 + 875)} // 500 and 1000 ms less 12.5 %, rounded
	tests := map[string]struct {
		policy     RetryPolicy
		wantStarts []time.Duration
	}{
		"success: NoRetry":                       {policy: NoRetry(), wantStarts: []time.Duration{0}},
		"success: the zero value":                {policy: RetryPolicy{}, wantStarts: []time.Duration{0}},
		"success: DefaultRetry().MaxRetries(0)":  {policy: DefaultRetry().MaxRetries(0), wantStarts: []time.Duration{0}},
		"success: DefaultRetry":                  {policy: DefaultRetry(), wantStarts: defaultStarts},
		"success: zero value with MaxRetries(2)": {policy: RetryPolicy{}.MaxRetries(2), wantStarts: defaultStarts},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				l, prov := runLoop(t.Context(), tt.policy, half, nil, fake.Error(unavailable()))
				if diff := cmp.Diff(tt.wantStarts, l.starts); diff != "" {
					t.Errorf("attempt start times mismatch (-want +got):\n%s", diff)
				}
				if got, want := prov.Calls(), len(tt.wantStarts); got != want {
					t.Errorf("calls = %d, want %d", got, want)
				}
			})
		})
	}
}

// TestContextWithRetry checks that a policy put in a call's context by
// ContextWithRetry replaces the Adapter's, that a context without one
// leaves the Adapter's, that the innermost one wins, and that the loop then
// runs under the context's policy.
func TestContextWithRetry(t *testing.T) {
	callPolicy := DefaultRetry().MaxRetries(1).Backoff(time.Millisecond, time.Second, 0)
	tests := map[string]struct {
		adapter      RetryPolicy
		ctx          func(context.Context) context.Context
		wantAttempts int
	}{
		"success: no policy in the context keeps the Adapter's": {
			adapter:      NoRetry(),
			ctx:          func(ctx context.Context) context.Context { return ctx },
			wantAttempts: 1,
		},
		"success: the context's policy replaces the Adapter's": {
			adapter:      NoRetry(),
			ctx:          func(ctx context.Context) context.Context { return ContextWithRetry(ctx, callPolicy) },
			wantAttempts: 2,
		},
		"success: the innermost policy wins": {
			adapter: NoRetry(),
			ctx: func(ctx context.Context) context.Context {
				return ContextWithRetry(ContextWithRetry(ctx, callPolicy), callPolicy.MaxRetries(3))
			},
			wantAttempts: 4,
		},
		"success: a NoRetry in the context replaces a retrying Adapter's": {
			adapter:      callPolicy,
			ctx:          func(ctx context.Context) context.Context { return ContextWithRetry(ctx, NoRetry()) },
			wantAttempts: 1,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := tt.ctx(t.Context())
				l, _ := runLoop(ctx, retryPolicyFor(ctx, tt.adapter), nil, nil, fake.Error(unavailable()))
				if got := l.attempts(); got != tt.wantAttempts {
					t.Errorf("%d attempts, want %d", got, tt.wantAttempts)
				}
			})
		})
	}
}

// TestRetryContextEndsLoop checks that a context that is done ends the loop
// with ctx.Err() itself and makes no further attempt: cancelled before the
// first attempt, during an attempt that then fails, and during a wait; and
// an expired deadline before the first attempt, during a wait, and exactly
// at the end of a wait, where the context's own timer and the wait's fire
// at one instant. A reason recorded before a wait that the context ended
// stays recorded, as upstream records it before the sleep, and the retries
// returned count only the attempts that started. Each case runs in
// tieRuns bubbles: at a tie the order in which the wait's timer and the
// context's own timer fire is not fixed, so one run can pass by chance.
func TestRetryContextEndsLoop(t *testing.T) {
	const tieRuns = 64
	p := DefaultRetry().MaxRetries(10).Backoff(time.Second, time.Second, 0).NoBudget()
	tests := map[string]struct {
		// setup returns the loop's context and a hook run at the start of
		// each attempt, both inside the bubble.
		setup       func(t *testing.T, ctx context.Context) (context.Context, func(n int))
		wantErr     error
		wantStarts  []time.Duration
		wantRetries int
		wantReasons int
		wantElapsed time.Duration
	}{
		"error: cancelled before the first attempt": {
			setup: func(_ *testing.T, ctx context.Context) (context.Context, func(int)) {
				ctx, cancel := context.WithCancel(ctx)
				cancel()
				return ctx, nil
			},
			wantErr: context.Canceled,
		},
		"error: cancelled during an attempt that fails": {
			setup: func(t *testing.T, ctx context.Context) (context.Context, func(int)) {
				ctx, cancel := context.WithCancel(ctx)
				t.Cleanup(cancel)
				return ctx, func(n int) {
					if n == 2 {
						cancel()
					}
				}
			},
			wantErr:     context.Canceled,
			wantStarts:  []time.Duration{0, time.Second},
			wantRetries: 1,
			wantReasons: 1,
			wantElapsed: time.Second,
		},
		"error: cancelled during a wait": {
			setup: func(t *testing.T, ctx context.Context) (context.Context, func(int)) {
				ctx, cancel := context.WithCancel(ctx)
				t.Cleanup(cancel)
				time.AfterFunc(ms(1500), cancel)
				return ctx, nil
			},
			wantErr:     context.Canceled,
			wantStarts:  []time.Duration{0, time.Second},
			wantRetries: 1,
			wantReasons: 2,
			wantElapsed: ms(1500),
		},
		"error: deadline passed before the first attempt": {
			setup: func(t *testing.T, ctx context.Context) (context.Context, func(int)) {
				ctx, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
				t.Cleanup(cancel)
				return ctx, nil
			},
			wantErr: context.DeadlineExceeded,
		},
		"error: deadline passes during a wait": {
			setup: func(t *testing.T, ctx context.Context) (context.Context, func(int)) {
				ctx, cancel := context.WithTimeout(ctx, ms(2500))
				t.Cleanup(cancel)
				return ctx, nil
			},
			wantErr:     context.DeadlineExceeded,
			wantStarts:  []time.Duration{0, time.Second, 2 * time.Second},
			wantRetries: 2,
			wantReasons: 3,
			wantElapsed: ms(2500),
		},
		"error: deadline at the end of the first wait": {
			setup: func(t *testing.T, ctx context.Context) (context.Context, func(int)) {
				ctx, cancel := context.WithTimeout(ctx, time.Second)
				t.Cleanup(cancel)
				return ctx, nil
			},
			wantErr:     context.DeadlineExceeded,
			wantStarts:  []time.Duration{0},
			wantRetries: 0,
			wantReasons: 1,
			wantElapsed: time.Second,
		},
		"error: deadline at the end of the second wait": {
			setup: func(t *testing.T, ctx context.Context) (context.Context, func(int)) {
				ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
				t.Cleanup(cancel)
				return ctx, nil
			},
			wantErr:     context.DeadlineExceeded,
			wantStarts:  []time.Duration{0, time.Second},
			wantRetries: 1,
			wantReasons: 2,
			wantElapsed: 2 * time.Second,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			for run := 0; run < tieRuns && !t.Failed(); run++ {
				synctest.Test(t, func(t *testing.T) {
					ctx, before := tt.setup(t, t.Context())
					l, prov := runLoop(ctx, p, nil, before, fake.Error(unavailable()))
					if l.err != tt.wantErr { //nolint:errorlint // the loop returns ctx.Err() itself, unwrapped.
						t.Errorf("err = %v, want %v itself", l.err, tt.wantErr)
					}
					if diff := cmp.Diff(tt.wantStarts, l.starts); diff != "" {
						t.Errorf("attempt start times mismatch (-want +got):\n%s", diff)
					}
					if got := prov.Calls(); got != len(tt.wantStarts) {
						t.Errorf("calls = %d, want %d", got, len(tt.wantStarts))
					}
					if l.retries != tt.wantRetries {
						t.Errorf("retries = %d, want %d", l.retries, tt.wantRetries)
					}
					if got := len(l.reasons); got != tt.wantReasons {
						t.Errorf("%d reasons, want %d", got, tt.wantReasons)
					}
					if l.elapsed != tt.wantElapsed {
						t.Errorf("elapsed %v, want %v", l.elapsed, tt.wantElapsed)
					}
				})
			}
		})
	}
}

// TestRetryPolicyInvalid checks that a setting out of range is refused when
// the policy is used, before any attempt, with typesafe-sdk-python's
// message (_core/retry.py:88-98, _core/config.py:36-39), in its order.
func TestRetryPolicyInvalid(t *testing.T) {
	tests := map[string]struct {
		policy  RetryPolicy
		wantErr string // "" for a valid policy
	}{
		"error: negative max_retries": {
			policy: DefaultRetry().MaxRetries(-1), wantErr: "max_retries must be a non-negative integer.",
		},
		"error: negative backoff_initial": {
			policy: DefaultRetry().Backoff(-time.Millisecond, time.Second, 0), wantErr: "backoff_initial must be a non-negative, finite number of seconds.",
		},
		"error: negative backoff_max": {
			policy: DefaultRetry().Backoff(0, -time.Second, 0), wantErr: "backoff_max must be a non-negative, finite number of seconds.",
		},
		"error: backoff_jitter above one": {
			policy: DefaultRetry().Backoff(0, 0, 1.5), wantErr: "backoff_jitter must be between zero and one.",
		},
		"error: backoff_jitter below zero": {
			policy: DefaultRetry().Backoff(0, 0, -0.1), wantErr: "backoff_jitter must be between zero and one.",
		},
		"error: backoff_jitter NaN": {
			policy: DefaultRetry().Backoff(0, 0, math.NaN()), wantErr: "backoff_jitter must be between zero and one.",
		},
		"error: zero budget": {
			policy: DefaultRetry().Budget(0), wantErr: "timeout must be a positive, finite number of seconds.",
		},
		"error: negative budget": {
			policy: DefaultRetry().Budget(-time.Second), wantErr: "timeout must be a positive, finite number of seconds.",
		},
		"error: the first setting out of range is reported": {
			policy: DefaultRetry().MaxRetries(-1).Backoff(0, 0, 2), wantErr: "max_retries must be a non-negative integer.",
		},
		"success: NoBudget after a zero budget": {
			policy: DefaultRetry().Budget(0).NoBudget(),
		},
		"success: jitter at the bounds": {
			policy: DefaultRetry().Backoff(0, time.Second, 1),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				l, prov := runLoop(t.Context(), tt.policy, nil, nil, fake.Text("ok"))
				if tt.wantErr == "" {
					if l.err != nil || prov.Calls() != 1 {
						t.Errorf("err = %v after %d calls, want nil after 1", l.err, prov.Calls())
					}
					return
				}
				if l.err == nil || l.err.Error() != tt.wantErr {
					t.Errorf("err = %v, want %q", l.err, tt.wantErr)
				}
				if got := prov.Calls(); got != 0 {
					t.Errorf("calls = %d, want 0: a refused policy makes no attempt", got)
				}
			})
		})
	}
}

// TestRetryPolicyIsAValue checks that each builder returns a changed copy
// and leaves the policy it was called on as it was.
func TestRetryPolicyIsAValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := DefaultRetry().Backoff(time.Millisecond, time.Second, 0)
		q := p.MaxRetries(0).Statuses().ConnectionErrors(false).TimeoutErrors(false).
			Predicate(func(error) bool { return false }).RespectRetryAfter(false).Budget(time.Nanosecond).NoBudget().
			Backoff(time.Hour, time.Hour, 1)
		if l, _ := runLoop(t.Context(), q.MaxRetries(1), nil, nil, fake.Error(unavailable())); l.attempts() != 1 {
			t.Errorf("q made %d attempts, want 1: Statuses() retries no status", l.attempts())
		}
		l, _ := runLoop(t.Context(), p, nil, nil, fake.Error(unavailable()))
		if got := l.attempts(); got != 3 {
			t.Errorf("after deriving policies from p, p made %d attempts, want 3", got)
		}
		if !slices.Equal(l.starts, []time.Duration{0, ms(1), ms(3)}) {
			t.Errorf("attempt start times %v, want [0 1ms 3ms]", l.starts)
		}
	})
}

// TestRetryReasonMessage checks the message of the reason recorded for a
// retry: upstream records str() of the translated error its provider
// raised, never of a wrapper (system-one-adapter-python v0.2.1,
// src/system_one_adapter/providers/base.py:35-41 and
// _utils/error_handling.py:75-82), so a provider timeout, connection
// failure or status error gives the text of that typed error, bare or
// wrapped; an error only the predicate retries gives its own text.
func TestRetryReasonMessage(t *testing.T) {
	custom := &errCustom{msg: "vendor: overloaded"}
	p := DefaultRetry().MaxRetries(1).Backoff(time.Millisecond, time.Second, 0).
		Predicate(func(err error) bool { return errors.As(err, new(*errCustom)) })
	timeoutText := "Request timed out (timeout=Timeout(timeout=None))."
	tests := map[string]struct {
		err  error
		want string
	}{
		"success: timeout":                             {err: &llm.TimeoutError{Err: context.DeadlineExceeded}, want: timeoutText},
		"success: wrapped timeout":                     {err: fmt.Errorf("gemini: %w", &llm.TimeoutError{Err: context.DeadlineExceeded}), want: timeoutText},
		"success: timeout wrapped twice":               {err: fmt.Errorf("a: %w", fmt.Errorf("b: %w", &llm.TimeoutError{})), want: timeoutText},
		"success: connection failure":                  {err: &llm.ConnectionError{Err: errors.New("dial tcp: refused")}, want: "Connection error."},
		"success: wrapped connection":                  {err: fmt.Errorf("openai: %w", &llm.ConnectionError{}), want: "Connection error."},
		"success: timeout inside a connection failure": {err: &llm.ConnectionError{Err: &llm.TimeoutError{}}, want: timeoutText},
		"success: connection failure inside a timeout": {err: &llm.TimeoutError{Err: &llm.ConnectionError{}}, want: timeoutText},
		"success: status error":                        {err: unavailable(), want: `503 {"m":"unavailable"}`},
		"success: wrapped status error":                {err: fmt.Errorf("anthropic: %w", unavailable()), want: `503 {"m":"unavailable"}`},
		"success: status error's message":              {err: fmt.Errorf("openai: %w", &llm.StatusError{StatusCode: 503, Body: []byte(`{"error":{"message":"boom"}}`)}), want: "503 boom"},
		"success: predicate's error":                   {err: custom, want: "vendor: overloaded"},
		"success: wrapped predicate's error":           {err: fmt.Errorf("custom provider: %w", custom), want: "custom provider: vendor: overloaded"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				l, _ := runLoop(t.Context(), p, nil, nil, fake.Error(tt.err), fake.Text("ok"))
				if l.err != nil {
					t.Fatalf("err = %v, want nil after one retry", l.err)
				}
				want := []RetryReason{{Category: "provider_error", Message: tt.want}}
				if diff := cmp.Diff(want, l.reasons); diff != "" {
					t.Errorf("reasons mismatch (-want +got):\n%s", diff)
				}
			})
		})
	}
}
