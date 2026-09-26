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
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/h2gate"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// The retry tests port tests/test_retry.py (RT1-RT25). Every test that
// makes calls runs inside a testing/synctest bubble: the retry waits run on
// fake time, so a 60 s Retry-After costs nothing, and each wait is measured
// exactly as the fake time between two attempts. Every call in a bubble runs
// under bubbleCtx's fake-time deadline, so a loop that never stops fails
// there instead of spinning.

// absent stands for a request without X-TypeSafe-Retry-Count in the header
// sequences, as None does in the upstream tests.
const absent = "(absent)"

// maxDuration is the largest time.Duration.
const maxDuration = time.Duration(math.MaxInt64)

// bubbleCtx returns the bubble's test context under a deadline an hour of
// fake time away, which no test reaches unless its call never stops.
func bubbleCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
	t.Cleanup(cancel)
	return ctx
}

// rtHeader returns an http.Header with the pairs kv (name, value, ...) added
// as a server sends them.
func rtHeader(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

// rtReply returns a JSON reply with status, body and the header pairs kv.
func rtReply(status int, body string, kv ...string) testsupport.Reply {
	r := testsupport.JSON(status, []byte(body))
	for i := 0; i+1 < len(kv); i += 2 {
		r.Header.Add(kv[i], kv[i+1])
	}
	return r
}

// seenAttempt is what attemptLog saw of one attempt, besides what its
// Recorder records.
type seenAttempt struct {
	// at is the (fake) time the attempt reached the transport.
	at time.Time
	// timeout is the attempt's deadline less at; zero without a deadline.
	timeout time.Duration
	// sum and n are the digest and length of a fresh GetBody read (PM4);
	// zero for a request without a body.
	sum testsupport.BodySum
	n   int64
}

// attemptLog is a RoundTripper that records when each attempt arrives, the
// deadline it carries and the digest of a fresh read of its body through
// GetBody, then lets rec answer it.
type attemptLog struct {
	rec  *testsupport.Recorder
	mu   sync.Mutex
	seen []seenAttempt
}

func (l *attemptLog) RoundTrip(req *http.Request) (*http.Response, error) {
	a := seenAttempt{at: time.Now()}
	if dl, ok := req.Context().Deadline(); ok {
		a.timeout = dl.Sub(a.at)
	}
	if req.GetBody != nil {
		sum, n, err := testsupport.SumGetBody(req.GetBody)
		if err != nil {
			return nil, err
		}
		a.sum, a.n = sum, n
	}
	l.mu.Lock()
	l.seen = append(l.seen, a)
	l.mu.Unlock()
	return l.rec.RoundTrip(req)
}

// attempts returns what l saw, in order.
func (l *attemptLog) attempts() []seenAttempt {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]seenAttempt(nil), l.seen...)
}

// waits returns the fake time between the end of each attempt and the start
// of the next among seen, each attempt taking took.
func waits(seen []seenAttempt, took time.Duration) []time.Duration {
	out := []time.Duration{}
	for i := 1; i < len(seen); i++ {
		out = append(out, seen[i].at.Sub(seen[i-1].at)-took)
	}
	return out
}

// retryCounts returns the X-TypeSafe-Retry-Count of each request, absent
// where it has none and every value joined by "," where it has several.
func retryCounts(reqs []testsupport.RecordedRequest) []string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = absent
		if v := r.Header.Values(headerRetryCount); len(v) > 0 {
			out[i] = strings.Join(v, ",")
		}
	}
	return out
}

// wantCounts returns the header sequence of n attempts: absent, "1", ...,
// n-1.
func wantCounts(n int) []string {
	out := []string{absent}
	for i := 1; i < n; i++ {
		out = append(out, strconv.Itoa(i))
	}
	return out
}

// assertAttempts checks that c counts want attempts in Stats.
func assertAttempts(t *testing.T, c *Client, want int) {
	t.Helper()
	if n := c.Stats().Attempts; n != uint64(want) { //nolint:gosec // G115: want is a test's small, non-negative count.
		t.Errorf("Stats().Attempts = %d, want %d", n, want)
	}
}

// fixedRandom returns a jitter source that always returns v, as the upstream
// tests patch random.random.
func fixedRandom(v float64) func() float64 { return func() float64 { return v } }

// listCall and systemOneCall are the two SDK calls the upstream tests make
// (tests/helpers.py: models and system_one), with the call options opts.
func listCall(ctx context.Context, c *Client, opts ...CallOption) error {
	_, err := c.Models().List(ctx, opts...)
	return err
}

func systemOneCall(t *testing.T) func(ctx context.Context, c *Client, opts ...CallOption) error {
	qs := noulQuestion(t)
	return func(ctx context.Context, c *Client, opts ...CallOption) error {
		_, err := c.SystemOne(ctx, "x", qs, opts...)
		return err
	}
}

// resources returns the two calls by name, as the upstream tests
// parametrize "resource".
func resources(t *testing.T) map[string]func(ctx context.Context, c *Client, opts ...CallOption) error {
	return map[string]func(ctx context.Context, c *Client, opts ...CallOption) error{
		"models":     listCall,
		"system_one": systemOneCall(t),
	}
}

// assertRefused checks that policy is refused with exactly want, as a
// *ConfigError, by NewClient with WithRetry and by both calls with Retry,
// before anything reaches the transport.
func assertRefused(t *testing.T, policy RetryPolicy, want string) {
	t.Helper()
	check := func(where string, err error) {
		t.Helper()
		if _, ok := errors.AsType[*ConfigError](err); !ok || err.Error() != want {
			t.Errorf("%s: error = %T %v, want the *ConfigError %q", where, err, err, want)
		}
	}
	clearEnv(t)
	rec := replying(http.StatusOK, []byte(`{"models":[]}`))
	_, err := NewClient(WithAPIKey(testKey), WithRoundTripper(rec), WithRetry(policy))
	check("NewClient(WithRetry)", err)
	c := newTestClient(t, rec)
	check("Models().List(Retry)", listCall(t.Context(), c, Retry(policy)))
	check("SystemOne(Retry)", systemOneCall(t)(t.Context(), c, Retry(policy)))
	if n := rec.Count(); n != 0 {
		t.Errorf("the transport saw %d requests, want 0: the policy is checked before anything is sent", n)
	}
}

// assertAccepted checks that policy is accepted by NewClient and by a call.
func assertAccepted(t *testing.T, policy RetryPolicy) {
	t.Helper()
	clearEnv(t)
	rec := replying(http.StatusOK, []byte(`{"models":[]}`))
	c, err := NewClient(WithAPIKey(testKey), WithRoundTripper(rec), WithRetry(policy))
	if err != nil {
		t.Fatalf("NewClient(WithRetry): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := listCall(t.Context(), c, Retry(policy)); err != nil {
		t.Errorf("List(Retry): %v", err)
	}
}

// TestRetryPolicyInvalidBudget ports test_retry_policy_invalid_timeout
// (RT1): a budget that is not positive is refused with the Python SDK's
// message. Python's inf and nan are not a time.Duration (a partial
// deviation); NoBudget is its timeout=None.
func TestRetryPolicyInvalidBudget(t *testing.T) {
	const msg = "timeout must be a positive, finite number of seconds."
	tests := map[string]struct {
		policy RetryPolicy
		want   string
	}{
		"error: zero":                           {policy: DefaultRetry().Budget(0), want: msg},
		"error: negative":                       {policy: DefaultRetry().Budget(-time.Second), want: msg},
		"error: the most negative Duration":     {policy: DefaultRetry().Budget(math.MinInt64), want: msg},
		"error: a Budget after NoBudget counts": {policy: DefaultRetry().NoBudget().Budget(0), want: msg},
		"success: one nanosecond":               {policy: DefaultRetry().Budget(1)},
		"success: no budget":                    {policy: DefaultRetry().NoBudget()},
		"success: NoBudget after a bad Budget":  {policy: DefaultRetry().Budget(-1).NoBudget()},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.want != "" {
				assertRefused(t, tt.policy, tt.want)
				return
			}
			assertAccepted(t, tt.policy)
		})
	}
}

// TestRetryPolicyInvalidBackoff ports test_invalid_backoff (RT3): a
// negative initial or maximum backoff is refused with a message that names
// it. Python's nan and inf are not a time.Duration (a partial deviation).
func TestRetryPolicyInvalidBackoff(t *testing.T) {
	const (
		initialMsg = "backoff_initial must be a non-negative, finite number of seconds."
		maxMsg     = "backoff_max must be a non-negative, finite number of seconds."
	)
	tests := map[string]struct {
		policy RetryPolicy
		want   string
	}{
		"error: initial -1s":                    {policy: DefaultRetry().Backoff(-time.Second, 5*time.Second, 0.25), want: initialMsg},
		"error: initial, the most negative":     {policy: DefaultRetry().Backoff(math.MinInt64, 5*time.Second, 0.25), want: initialMsg},
		"error: max -1s":                        {policy: DefaultRetry().Backoff(500*time.Millisecond, -time.Second, 0.25), want: maxMsg},
		"error: max, the most negative":         {policy: DefaultRetry().Backoff(500*time.Millisecond, math.MinInt64, 0.25), want: maxMsg},
		"error: both, initial is checked first": {policy: DefaultRetry().Backoff(-1, -1, 0.25), want: initialMsg},
		"error: max_retries is checked first":   {policy: DefaultRetry().MaxRetries(-1).Backoff(-1, -1, 0.25), want: "max_retries must be a non-negative integer."},
		"success: zero initial and max":         {policy: DefaultRetry().Backoff(0, 0, 0.25)},
		"success: the largest Durations":        {policy: DefaultRetry().Backoff(maxDuration, maxDuration, 0.25)},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.want != "" {
				assertRefused(t, tt.policy, tt.want)
				return
			}
			assertAccepted(t, tt.policy)
		})
	}
}

// TestRetryPolicyInvalidJitter ports test_invalid_backoff_jitter (RT4):
// a jitter outside [0, 1], NaN and the infinities included, is refused.
func TestRetryPolicyInvalidJitter(t *testing.T) {
	const msg = "backoff_jitter must be between zero and one."
	tests := map[string]struct {
		jitter float64
		want   string
	}{
		"error: -0.1":      {jitter: -0.1, want: msg},
		"error: 1.1":       {jitter: 1.1, want: msg},
		"error: NaN":       {jitter: math.NaN(), want: msg},
		"error: +Inf":      {jitter: math.Inf(1), want: msg},
		"error: -Inf":      {jitter: math.Inf(-1), want: msg},
		"success: 0":       {jitter: 0},
		"success: 1":       {jitter: 1},
		"success: -0 is 0": {jitter: math.Copysign(0, -1)},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := DefaultRetry().Backoff(500*time.Millisecond, 5*time.Second, tt.jitter)
			if tt.want != "" {
				assertRefused(t, p, tt.want)
				return
			}
			assertAccepted(t, p)
		})
	}
}

// TestRetryPolicyInvalidMaxRetries ports test_invalid_max_retries (RT5): a
// negative count is refused. Python's 0.5, nan and inf are not an int (a
// partial deviation: the type refuses them).
func TestRetryPolicyInvalidMaxRetries(t *testing.T) {
	const msg = "max_retries must be a non-negative integer."
	tests := map[string]struct {
		n    int
		want string
	}{
		"error: -1":                {n: -1, want: msg},
		"error: the most negative": {n: math.MinInt, want: msg},
		"success: zero":            {n: 0},
		"success: the largest":     {n: math.MaxInt},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := DefaultRetry().MaxRetries(tt.n)
			if tt.want != "" {
				assertRefused(t, p, tt.want)
				return
			}
			assertAccepted(t, p)
		})
	}
}

// TestRetryPolicyCheckOrder pins where a bad policy stands among the checks
// that fail before anything is sent: NewClient checks it after the
// timeouts and before the response limit, a call after its timeout and
// before its headers.
func TestRetryPolicyCheckOrder(t *testing.T) {
	const bad = "max_retries must be a non-negative integer."
	policy := DefaultRetry().MaxRetries(-1)
	tests := map[string]struct {
		client []ClientOption
		call   []CallOption
		want   string
	}{
		"error: NewClient, a bad timeout first": {
			client: []ClientOption{WithRetry(policy), WithTimeout(-1)}, want: "The timeout passed to WithTimeout must be positive; use WithNoTimeout for no deadline.",
		},
		"error: NewClient, a bad connect timeout first": {
			client: []ClientOption{WithRetry(policy), WithConnectTimeout(-1)}, want: "The connect timeout passed to WithConnectTimeout must be positive.",
		},
		"error: NewClient, the policy before the response limit": {
			client: []ClientOption{WithMaxResponseBytes(0), WithRetry(policy)}, want: bad,
		},
		"error: a call, a bad timeout first": {
			call: []CallOption{Retry(policy), Timeout(-1)}, want: "The timeout passed to Timeout must be positive.",
		},
		"error: a call, the policy before the headers": {
			call: []CallOption{Header("bad name", "v"), Retry(policy)}, want: bad,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(http.StatusOK, []byte(`{"models":[]}`))
			var err error
			if tt.client != nil {
				clearEnv(t)
				_, err = NewClient(append([]ClientOption{WithAPIKey(testKey), WithRoundTripper(rec)}, tt.client...)...)
			} else {
				err = listCall(t.Context(), newTestClient(t, rec), tt.call...)
			}
			if _, ok := errors.AsType[*ConfigError](err); !ok || err.Error() != tt.want {
				t.Errorf("error = %T %v, want the *ConfigError %q", err, err, tt.want)
			}
		})
	}
}

// TestRetryPolicyDefaults pins the zero RetryPolicy to DefaultRetry, the
// Python SDK's RetryPolicy() (py:_core/retry.py:52-86), setting by setting,
// and NoRetry to DefaultRetry().MaxRetries(0); each setter returns a copy
// and leaves its receiver as it was.
func TestRetryPolicyDefaults(t *testing.T) {
	var zero RetryPolicy
	d := DefaultRetry()
	for name, p := range map[string]*RetryPolicy{"the zero RetryPolicy": &zero, "DefaultRetry()": &d} {
		t.Run(name, func(t *testing.T) {
			if n := p.retries(); n != 2 {
				t.Errorf("retries = %d, want 2", n)
			}
			for _, status := range []int{408, 429, 500, 503, 599} {
				if !p.retriesStatus(status) {
					t.Errorf("status %d is not retried", status)
				}
			}
			for _, status := range []int{200, 204, 302, 400, 401, 403, 404, 409, 422, 499, 600} {
				if p.retriesStatus(status) {
					t.Errorf("status %d is retried", status)
				}
			}
			r := retryState{policy: p, random: fixedRandom(0)}
			if got := r.delay(1, errors.New("no response")); got != 500*time.Millisecond {
				t.Errorf("first backoff = %v, want 500ms", got)
			}
			if p.ignoreRetryAfter || p.noConnection || p.noTimeout || p.rules != nil || p.unbounded || p.set != 0 {
				t.Errorf("policy %+v, want every setting at its default", *p)
			}
			if err := p.check(); err != nil {
				t.Errorf("check: %v", err)
			}
		})
	}
	no := NoRetry()
	if no.retries() != 0 || no.set != setMaxRetries {
		t.Errorf("NoRetry() = %+v, want DefaultRetry().MaxRetries(0)", no)
	}
	base := DefaultRetry()
	_ = base.MaxRetries(7).Backoff(time.Second, time.Minute, 0.5).Statuses(409).RespectRetryAfter(false).
		ConnectionErrors(false).TimeoutErrors(false).Budget(time.Minute)
	if base.set != 0 || base.ignoreRetryAfter || base.noConnection || base.noTimeout {
		t.Errorf("the setters changed their receiver: %+v", base)
	}
	codes := []int{503, 409, 409}
	s := DefaultRetry().Statuses(codes...)
	codes[0] = 200
	if diff := gocmp.Diff([]int{409, 503}, s.rules.statuses); diff != "" {
		t.Errorf("Statuses kept (-want +got):\n%s", diff)
	}
	if none := DefaultRetry().Statuses(); none.retriesStatus(503) || none.retriesStatus(429) {
		t.Error("Statuses() with no codes still retries a status")
	}
	// A 2xx in the set is never retried by status. The loop cannot reach
	// this through a response (a 2xx is not an *APIError) unless a caller's
	// transport returns an SDK error itself.
	if ok := DefaultRetry().Statuses(200, 204); ok.retriesStatus(200) || ok.retriesStatus(204) {
		t.Error("Statuses(200, 204) retries a 2xx by status")
	}
}

// TestZeroBackoffRetriesAtOnce ports test_zero_backoff_retries (RT2): a zero
// initial or maximum backoff retries at once, whether the retry recovers or
// fails again, and the retry carries X-TypeSafe-Retry-Count: 1.
func TestZeroBackoffRetriesAtOnce(t *testing.T) {
	tests := map[string]struct {
		initial, maximum time.Duration
		recover          bool
	}{
		"success: zero initial, the retry recovers":              {initial: 0, maximum: 5 * time.Second, recover: true},
		"error: zero initial, the retry fails again":             {initial: 0, maximum: 5 * time.Second},
		"success: zero maximum, the retry recovers":              {initial: 500 * time.Millisecond, maximum: 0, recover: true},
		"error: zero maximum, the retry fails again":             {initial: 500 * time.Millisecond, maximum: 0},
		"success: zero initial and maximum, the retry recovers":  {recover: true},
		"error: zero initial and maximum, the retry fails again": {},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				replies := []testsupport.Reply{rtReply(503, `{"message": "temporarily unavailable"}`)}
				if tt.recover {
					replies = append(replies, rtReply(200, `{"models":[]}`))
				}
				log := &attemptLog{rec: &testsupport.Recorder{Replies: replies}}
				c := newTestClient(t, log, WithRetry(DefaultRetry().MaxRetries(1).Backoff(tt.initial, tt.maximum, 0.25)))
				resp, err := c.Models().List(bubbleCtx(t))
				if tt.recover {
					if err != nil || len(resp.Models()) != 0 {
						t.Fatalf("List = %v, %v; want no models", resp, err)
					}
				} else if ae, ok := errors.AsType[*APIError](err); !ok || !strings.Contains(ae.Error(), "temporarily unavailable") {
					t.Fatalf("List error = %T %v, want the 503's *APIError", err, err)
				}
				if diff := gocmp.Diff(wantCounts(2), retryCounts(log.rec.Requests())); diff != "" {
					t.Errorf("X-TypeSafe-Retry-Count (-want +got):\n%s", diff)
				}
				if diff := gocmp.Diff([]time.Duration{0}, waits(log.attempts(), 0)); diff != "" {
					t.Errorf("waits (-want +got):\n%s", diff)
				}
			})
		})
	}
}

// TestRetryBudgetStopsBeforeDelay ports test_retry_policy_timeout_budget
// (RT6), the budget's oracle: each attempt takes duration of fake time and
// answers 429 with Retry-After delay; the call stops before a retry whose
// wait would bring the time since the call started to the budget or
// beyond, returns the last attempt's error, and each SDK call gets a fresh
// budget. Both endpoints.
func TestRetryBudgetStopsBeforeDelay(t *testing.T) {
	tests := map[string]struct {
		budget   time.Duration // zero: NoBudget
		defaults bool          // DefaultRetry() as it is: its own 30 s budget
		duration time.Duration
		delay    string // Retry-After, in seconds as Python's str(float) spells it
		wait     time.Duration
		attempts int
	}{
		"error: no budget, max_retries stops":          {budget: 0, duration: time.Second, delay: "0.5", wait: 500 * time.Millisecond, attempts: 3},
		"error: 30s budget stops at 25s + 5s":          {budget: 30 * time.Second, duration: 10 * time.Second, delay: "5.0", wait: 5 * time.Second, attempts: 2},
		"error: 2.5s budget stops at 2s + 0.5s":        {budget: 2500 * time.Millisecond, duration: 750 * time.Millisecond, delay: "0.5", wait: 500 * time.Millisecond, attempts: 2},
		"error: a zero wait still counts elapsed time": {budget: 2 * time.Second, duration: time.Second, delay: "0.0", wait: 0, attempts: 2},
		"error: a wait equal to the budget stops":      {budget: time.Second, duration: 0, delay: "1.0", wait: time.Second, attempts: 1},
		"error: a server wait over the budget stops":   {budget: time.Second, duration: 0, delay: "60.0", wait: time.Minute, attempts: 1},
		// The default budget is 30 s exactly: 10 s + 20 s reaches it, and
		// 10 s + 19.999 s does not, while 10 s + 19.999 s + 10 s + 19.999 s
		// does.
		"error: the default budget stops at 10s + 20s":   {defaults: true, duration: 10 * time.Second, delay: "20.0", wait: 20 * time.Second, attempts: 1},
		"error: the default budget allows 10s + 19.999s": {defaults: true, duration: 10 * time.Second, delay: "19.999", wait: 19999 * time.Millisecond, attempts: 2},
	}
	for name, tt := range tests {
		for resource, call := range resources(t) {
			t.Run(name+" "+resource, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					var mu sync.Mutex
					n := 0
					rec := &testsupport.Recorder{Respond: func(testsupport.RecordedRequest) testsupport.Reply {
						time.Sleep(tt.duration)
						mu.Lock()
						n++
						k := n
						mu.Unlock()
						return rtReply(429, `{"message": "attempt `+strconv.Itoa(k)+`"}`, "Retry-After", tt.delay)
					}}
					log := &attemptLog{rec: rec}
					policy := DefaultRetry().Budget(tt.budget)
					switch {
					case tt.defaults:
						policy = DefaultRetry()
					case tt.budget == 0:
						policy = DefaultRetry().NoBudget()
					}
					// No per-attempt deadline: an attempt's own duration is
					// the test's clock, not a timeout.
					c := newTestClient(t, log, WithRetry(policy), WithNoTimeout())
					for round := range 2 { // each SDK call gets a fresh budget
						mu.Lock()
						n = 0
						mu.Unlock()
						before := len(log.attempts())
						err := call(bubbleCtx(t), c)
						ae, ok := errors.AsType[*APIError](err)
						if !ok || ae.StatusCode != http.StatusTooManyRequests || !strings.HasSuffix(ae.Error(), "attempt "+strconv.Itoa(tt.attempts)) {
							t.Fatalf("round %d: error = %T %v, want the 429 of attempt %d", round, err, err, tt.attempts)
						}
						seen := log.attempts()[before:]
						if len(seen) != tt.attempts {
							t.Errorf("round %d: %d attempts, want %d", round, len(seen), tt.attempts)
						}
						want := make([]time.Duration, tt.attempts-1)
						for i := range want {
							want[i] = tt.wait
						}
						if diff := gocmp.Diff(want, waits(seen, tt.duration)); diff != "" {
							t.Errorf("round %d: waits (-want +got):\n%s", round, diff)
						}
					}
				})
			})
		}
	}
}

// TestPerCallBudgetOverride ports test_retry_policy_timeout_override (RT7):
// each attempt takes 20 s of fake time, so the client's default 30 s budget
// stops a call after two attempts; a call's own policy replaces it for that
// call only: a 1 s budget stops after one, NoBudget lets max_retries stop
// after three, and the next call has the client's again.
func TestPerCallBudgetOverride(t *testing.T) {
	for resource, call := range resources(t) {
		t.Run(resource, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := &testsupport.Recorder{Respond: func(testsupport.RecordedRequest) testsupport.Reply {
					time.Sleep(20 * time.Second)
					return rtReply(429, `{}`, "Retry-After-Ms", "0")
				}}
				c := newTestClient(t, rec, WithRetry(DefaultRetry()), WithNoTimeout())
				steps := []struct {
					name   string
					policy *RetryPolicy
					want   int
				}{
					{name: "the client's default", want: 2},
					{name: "Budget(1s)", policy: new(DefaultRetry().Budget(time.Second)), want: 1},
					{name: "NoBudget()", policy: new(DefaultRetry().NoBudget()), want: 3},
					{name: "the client's default again", want: 2},
				}
				for _, step := range steps {
					before := rec.Count()
					var opts []CallOption
					if step.policy != nil {
						opts = append(opts, Retry(*step.policy))
					}
					err := call(bubbleCtx(t), c, opts...)
					if ae, ok := errors.AsType[*APIError](err); !ok || ae.StatusCode != http.StatusTooManyRequests {
						t.Fatalf("%s: error = %T %v, want a 429 *APIError", step.name, err, err)
					}
					if n := rec.Count() - before; n != step.want {
						t.Errorf("%s: %d attempts, want %d", step.name, n, step.want)
					}
				}
			})
		})
	}
}

// TestDefaultRetryStatuses ports test_default_retry_statuses (RT8): under
// the production policy 408, 429 and 5xx are tried three times and other
// statuses once, the last error keeping its status, only retries carry
// X-TypeSafe-Retry-Count, and each retry waits the Retry-After-Ms of the
// response before it, whatever its status.
func TestDefaultRetryStatuses(t *testing.T) {
	tests := map[string]struct {
		status, attempts int
	}{
		"error: 408 retried": {status: 408, attempts: 3},
		"error: 429 retried": {status: 429, attempts: 3},
		"error: 500 retried": {status: 500, attempts: 3},
		"error: 503 retried": {status: 503, attempts: 3},
		"error: 599 retried": {status: 599, attempts: 3},
		"error: 400 once":    {status: 400, attempts: 1},
		"error: 401 once":    {status: 401, attempts: 1},
		"error: 403 once":    {status: 403, attempts: 1},
		"error: 404 once":    {status: 404, attempts: 1},
		"error: 409 once":    {status: 409, attempts: 1},
		"error: 422 once":    {status: 422, attempts: 1},
		"error: 302 once":    {status: 302, attempts: 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := &testsupport.Recorder{Replies: []testsupport.Reply{rtReply(tt.status, `{"message": "failed"}`, "Retry-After-Ms", "0")}}
				log := &attemptLog{rec: rec}
				c := newTestClient(t, log, WithRetry(DefaultRetry()))
				err := listCall(bubbleCtx(t), c)
				if ae, ok := errors.AsType[*APIError](err); !ok || ae.StatusCode != tt.status {
					t.Fatalf("error = %T %v, want a %d *APIError", err, err, tt.status)
				}
				if diff := gocmp.Diff(wantCounts(tt.attempts), retryCounts(rec.Requests())); diff != "" {
					t.Errorf("X-TypeSafe-Retry-Count (-want +got):\n%s", diff)
				}
				// Retry-After-Ms: 0 holds for every status, where the
				// backoff would wait 500 ms, then 1 s.
				if diff := gocmp.Diff(make([]time.Duration, tt.attempts-1), waits(log.attempts(), 0)); diff != "" {
					t.Errorf("waits (-want +got):\n%s", diff)
				}
				assertAttempts(t, c, tt.attempts)
			})
		})
	}
}

// TestRetryAfterHonoured ports test_server_delay_through_tenacity (RT10):
// the wait before the retry is the server's, from Retry-After in seconds or
// Retry-After-Ms, the latter first, and 60 s is honoured as asked.
func TestRetryAfterHonoured(t *testing.T) {
	tests := map[string]struct {
		header []string
		want   time.Duration
	}{
		"success: Retry-After 2":                     {header: []string{"Retry-After", "2"}, want: 2 * time.Second},
		"success: Retry-After-Ms 125":                {header: []string{"Retry-After-Ms", "125"}, want: 125 * time.Millisecond},
		"success: Retry-After-Ms 0 over Retry-After": {header: []string{"Retry-After-Ms", "0", "Retry-After", "50"}, want: 0},
		"success: Retry-After 60":                    {header: []string{"Retry-After", "60"}, want: time.Minute},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				log := &attemptLog{rec: &testsupport.Recorder{Replies: []testsupport.Reply{
					rtReply(429, `{}`, tt.header...), rtReply(200, `{"models":[]}`),
				}}}
				c := newTestClient(t, log, WithRetry(DefaultRetry().NoBudget()))
				if err := listCall(bubbleCtx(t), c); err != nil {
					t.Fatalf("List: %v", err)
				}
				if diff := gocmp.Diff([]time.Duration{tt.want}, waits(log.attempts(), 0)); diff != "" {
					t.Errorf("waits (-want +got):\n%s", diff)
				}
			})
		})
	}
}

// TestParseRetryAfterTable ports test_parse_retry_after (RT11), the nine
// upstream rows: what retryAfter reads from the headers (Python's
// parse_retry_after, in milliseconds), and the wait the loop then takes
// through the client: the server's, or the backoff (500 ms with the jitter
// source at 0) when nothing parses.
func TestParseRetryAfterTable(t *testing.T) {
	tests := map[string]struct {
		header []string
		want   time.Duration
		ok     bool
	}{
		"success: no header":                    {},
		"success: Retry-After bad":              {header: []string{"Retry-After", "bad"}},
		"success: Retry-After -1":               {header: []string{"Retry-After", "-1"}},
		"success: ms NaN, then Retry-After 1.5": {header: []string{"Retry-After-Ms", "NaN", "Retry-After", "1.5"}, want: 1500 * time.Millisecond, ok: true},
		"success: ms -1, then Retry-After 2":    {header: []string{"Retry-After-Ms", "-1", "Retry-After", "2"}, want: 2 * time.Second, ok: true},
		"success: Retry-After empty":            {header: []string{"Retry-After", ""}, want: 0, ok: true},
		"success: ms inf":                       {header: []string{"Retry-After-Ms", "inf"}},
		"success: ms bad, then Retry-After 2":   {header: []string{"Retry-After-Ms", "bad", "Retry-After", "2"}, want: 2 * time.Second, ok: true},
		"success: Retry-After 1e308":            {header: []string{"Retry-After", "1e308"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				got, ok := retryAfter(rtHeader(tt.header...), time.Now())
				if got != tt.want || ok != tt.ok {
					t.Errorf("retryAfter = %v, %t, want %v, %t", got, ok, tt.want, tt.ok)
				}
				wait := tt.want
				if !tt.ok {
					wait = 500 * time.Millisecond
				}
				log := &attemptLog{rec: &testsupport.Recorder{Replies: []testsupport.Reply{
					rtReply(429, `{}`, tt.header...), rtReply(200, `{"models":[]}`),
				}}}
				c := newTestClient(t, log, WithRetry(DefaultRetry()))
				c.random = fixedRandom(0)
				if err := listCall(bubbleCtx(t), c); err != nil {
					t.Fatalf("List: %v", err)
				}
				if diff := gocmp.Diff([]time.Duration{wait}, waits(log.attempts(), 0)); diff != "" {
					t.Errorf("waits (-want +got):\n%s", diff)
				}
			})
		})
	}
}

// TestBackoffScheduleAndDates ports test_backoff_dates_cap_and_jitter
// (RT12): an HTTP date ten seconds ahead waits 10 s and one behind 0; the
// backoff with the jitter source at 0 doubles from 500 ms and stops at 5 s,
// and at 1 takes a quarter off (375 ms); a server's wait is honoured however
// long (61 s, 60.001 s, a date), and an unparseable one falls back to the
// backoff. The schedule is also measured through the client.
func TestBackoffScheduleAndDates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		future := now.Add(10 * time.Second).UTC().Format(http.TimeFormat)
		past := now.Add(-10 * time.Second).UTC().Format(http.TimeFormat)
		if d, ok := retryAfter(rtHeader("Retry-After", future), now); !ok || d != 10*time.Second {
			t.Errorf("a date 10 s ahead = %v, %t; want 10s", d, ok)
		}
		if d, ok := retryAfter(rtHeader("Retry-After", past), now); !ok || d != 0 {
			t.Errorf("a date 10 s behind = %v, %t; want 0", d, ok)
		}

		p := DefaultRetry()
		zero := retryState{policy: &p, random: fixedRandom(0)}
		noResponse := errors.New("no response")
		for retry, want := range map[int]time.Duration{1: 500 * time.Millisecond, 2: time.Second, 3: 2 * time.Second, 4: 4 * time.Second, 5: 5 * time.Second, 20: 5 * time.Second} {
			if got := zero.delay(retry, noResponse); got != want {
				t.Errorf("backoff before retry %d = %v, want %v", retry, got, want)
			}
		}
		one := retryState{policy: &p, random: fixedRandom(1)}
		if got := one.delay(1, noResponse); got != 375*time.Millisecond {
			t.Errorf("backoff at jitter 1 = %v, want 375ms", got)
		}
		for name, tt := range map[string]struct {
			header []string
			want   time.Duration
		}{
			"Retry-After 61":       {header: []string{"Retry-After", "61"}, want: 61 * time.Second},
			"Retry-After-Ms 60001": {header: []string{"Retry-After-Ms", "60001"}, want: 60001 * time.Millisecond},
			"a date 10 s ahead":    {header: []string{"Retry-After", future}, want: 10 * time.Second},
			"Retry-After bad":      {header: []string{"Retry-After", "bad"}, want: 375 * time.Millisecond},
		} {
			err := &APIError{StatusCode: 429, Header: rtHeader(tt.header...)}
			if got := one.delay(1, err); got != tt.want {
				t.Errorf("%s: wait = %v, want %v", name, got, tt.want)
			}
		}

		// Through the client: 503 every time, six retries, no budget.
		log := &attemptLog{rec: &testsupport.Recorder{Replies: []testsupport.Reply{rtReply(503, `{}`)}}}
		c := newTestClient(t, log, WithRetry(DefaultRetry().MaxRetries(6).NoBudget()))
		c.random = fixedRandom(0)
		if ae, ok := errors.AsType[*APIError](listCall(bubbleCtx(t), c)); !ok || ae.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("List: want the last 503")
		}
		want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
		if diff := gocmp.Diff(want, waits(log.attempts(), 0)); diff != "" {
			t.Errorf("waits through the client (-want +got):\n%s", diff)
		}

		// A date through the client: the wait ends at the date, which has
		// whole seconds (the waits above left the clock at .5 s).
		at := time.Now().Truncate(time.Second).Add(10 * time.Second)
		dated := &attemptLog{rec: &testsupport.Recorder{Replies: []testsupport.Reply{
			rtReply(429, `{}`, "Retry-After", at.UTC().Format(http.TimeFormat)), rtReply(200, `{"models":[]}`),
		}}}
		c = newTestClient(t, dated, WithRetry(DefaultRetry()))
		wantAt := time.Until(at)
		if err := listCall(bubbleCtx(t), c); err != nil {
			t.Fatalf("List: %v", err)
		}
		if diff := gocmp.Diff([]time.Duration{wantAt}, waits(dated.attempts(), 0)); diff != "" || wantAt != 9500*time.Millisecond {
			t.Errorf("wait for a date %v ahead (-want +got):\n%s", wantAt, diff)
		}
	})
}

// TestPerCallRetryPolicyOverride ports test_system_one_retry_override
// (RT13): a call's Retry replaces the client's policy for that call alone,
// its status set included (409 retried by the call's, 429 by the
// client's), and a call without it uses the client's.
func TestPerCallRetryPolicyOverride(t *testing.T) {
	tests := map[string]struct {
		clientAttempts, callAttempts int
	}{
		"error: client 1, call 3": {clientAttempts: 1, callAttempts: 3},
		"error: client 3, call 1": {clientAttempts: 3, callAttempts: 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := &testsupport.Recorder{Respond: func(r testsupport.RecordedRequest) testsupport.Reply {
					status := 429
					if r.Header.Get("X-Call") == "override" {
						status = 409
					}
					return rtReply(status, `{"message": "failed"}`, "Retry-After-Ms", "0")
				}}
				c := newTestClient(t, rec, WithRetry(DefaultRetry().MaxRetries(tt.clientAttempts-1)))
				callPolicy := DefaultRetry().MaxRetries(tt.callAttempts - 1).Statuses(409)
				qs := noulQuestion(t)
				for i, step := range []struct {
					name     string
					retry    []CallOption
					attempts int
				}{
					{name: "override", retry: []CallOption{Retry(callPolicy)}, attempts: tt.callAttempts},
					{name: "inherited", attempts: tt.clientAttempts},
					{name: "override", retry: []CallOption{Retry(callPolicy)}, attempts: tt.callAttempts},
				} {
					before := len(rec.Requests())
					_, err := c.SystemOne(bubbleCtx(t), "hello", qs, append([]CallOption{Header("X-Call", step.name)}, step.retry...)...)
					if _, ok := errors.AsType[*APIError](err); !ok {
						t.Fatalf("call %d (%s): error = %T %v, want an *APIError", i, step.name, err, err)
					}
					if diff := gocmp.Diff(wantCounts(step.attempts), retryCounts(rec.Requests()[before:])); diff != "" {
						t.Errorf("call %d (%s): X-TypeSafe-Retry-Count (-want +got):\n%s", i, step.name, diff)
					}
				}
			})
		})
	}
}

// TestConcurrentCallsCountTheirOwnRetries is RT14's Go half
// (test_async_concurrent_retry_state; a partial deviation, goroutines for
// asyncio tasks): four calls on one client from four goroutines each fail
// once with 429 and recover, and each call's retry carries its own count.
func TestConcurrentCallsCountTheirOwnRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		seen := map[string][]string{}
		rec := &testsupport.Recorder{Respond: func(r testsupport.RecordedRequest) testsupport.Reply {
			key := r.Header.Get("X-Call")
			mu.Lock()
			seen[key] = append(seen[key], retryCounts([]testsupport.RecordedRequest{r})[0])
			first := len(seen[key]) == 1
			mu.Unlock()
			if first {
				return rtReply(429, `{}`, "Retry-After-Ms", "0")
			}
			return rtReply(200, `{"models":[]}`)
		}}
		c := newTestClient(t, rec, WithRetry(DefaultRetry()))
		ctx := bubbleCtx(t)
		var wg sync.WaitGroup
		errs := make([]error, 4)
		for i := range 4 {
			wg.Go(func() { errs[i] = listCall(ctx, c, Header("X-Call", strconv.Itoa(i))) })
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("call %d: %v", i, err)
			}
		}
		want := map[string][]string{"0": wantCounts(2), "1": wantCounts(2), "2": wantCounts(2), "3": wantCounts(2)}
		if diff := gocmp.Diff(want, seen); diff != "" {
			t.Errorf("X-TypeSafe-Retry-Count per call (-want +got):\n%s", diff)
		}
	})
}

// TestRetryRecoversWithOverrides ports
// test_system_one_retry_recovers_with_overrides (RT15): a call with its own
// model, timeout and headers fails with a timeout, then a 429 asking for
// 125 ms, then succeeds; every attempt sends the same body bytes (a fresh
// GetBody read hashes the same, PM4), the same headers besides the retry
// count, and the call's per-attempt timeout; the first wait is the backoff
// (375 ms to 500 ms), the second the server's. The next call has the
// client's settings and no retry count. Python's httpx.Timeout(3.0,
// connect=1.0, read=5.0) is one deadline of 3 s per attempt here.
func TestRetryRecoversWithOverrides(t *testing.T) {
	typed := noulQuestion
	raw := func(t *testing.T) *Prepared {
		return mustPrepared(t, NewQuestions().Raw("q", RawQuestion{Type: "noul", Fields: map[string]any{"instructions": "?"}}))
	}
	tests := map[string]struct {
		questions func(t *testing.T) *Prepared
		timeout   time.Duration
	}{
		"success: typed questions, 2s": {questions: typed, timeout: 2 * time.Second},
		"success: typed questions, 3s": {questions: typed, timeout: 3 * time.Second},
		"success: raw questions, 2s":   {questions: raw, timeout: 2 * time.Second},
		"success: raw questions, 3s":   {questions: raw, timeout: 3 * time.Second},
	}
	const wantBody = `{"state":{"document":"hello"},"model":"call-model","questions":{"q":{"type":"noul","instructions":"?"}}}`
	wantSum := testsupport.BodySum(sha256.Sum256([]byte(wantBody)))
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				result := testsupport.Fixture(t, "result.json")
				log := &attemptLog{rec: &testsupport.Recorder{Replies: []testsupport.Reply{
					{Err: netTimeout{}},
					rtReply(429, `{"message": "slow down"}`, "Retry-After-Ms", "125"),
					testsupport.JSON(200, result),
				}}}
				c := newTestClient(t, log, WithModel("client-model"), WithTimeout(7*time.Second), WithHeader("X-Default", "kept"), WithRetry(DefaultRetry()))
				qs := tt.questions(t)
				ctx := bubbleCtx(t)
				if _, err := c.SystemOne(ctx, map[string]any{"document": "hello"}, qs, Model("call-model"), Timeout(tt.timeout),
					Header("X-Call", "override"), Header("Authorization", "must-not-win")); err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
				reqs := log.rec.Requests()
				seen := log.attempts()
				if len(reqs) != 3 || len(seen) != 3 {
					t.Fatalf("%d attempts, want 3", len(reqs))
				}
				for i, r := range reqs {
					if string(r.Body) != wantBody {
						t.Errorf("attempt %d body = %s, want %s", i, r.Body, wantBody)
					}
					if seen[i].sum != wantSum || seen[i].n != int64(len(wantBody)) || r.ContentLength != int64(len(wantBody)) || !r.HasGetBody {
						t.Errorf("attempt %d: GetBody read %v (%d bytes), ContentLength %d, GetBody %t; want %v, %d bytes",
							i, seen[i].sum, seen[i].n, r.ContentLength, r.HasGetBody, wantSum, len(wantBody))
					}
					if seen[i].timeout != tt.timeout {
						t.Errorf("attempt %d deadline in %v, want %v", i, seen[i].timeout, tt.timeout)
					}
					got := []string{r.Header.Get("Authorization"), r.Header.Get("X-Default"), r.Header.Get("X-Call")}
					if diff := gocmp.Diff([]string{"Bearer " + testKey, "kept", "override"}, got); diff != "" {
						t.Errorf("attempt %d headers (-want +got):\n%s", i, diff)
					}
					h := r.Header.Clone()
					h.Del(headerRetryCount)
					if diff := gocmp.Diff(reqs[0].Header, h); diff != "" {
						t.Errorf("attempt %d headers besides the retry count differ from the first (-first +this):\n%s", i, diff)
					}
				}
				if diff := gocmp.Diff(wantCounts(3), retryCounts(reqs)); diff != "" {
					t.Errorf("X-TypeSafe-Retry-Count (-want +got):\n%s", diff)
				}
				w := waits(seen, 0)
				if w[0] < 375*time.Millisecond || w[0] > 500*time.Millisecond || w[0]%time.Millisecond != 0 || w[1] != 125*time.Millisecond {
					t.Errorf("waits = %v, want 375ms..500ms in whole milliseconds, then 125ms", w)
				}

				// The next call has the client's settings.
				if _, err := c.SystemOne(ctx, "next", qs); err != nil {
					t.Fatalf("second SystemOne: %v", err)
				}
				last := log.rec.Requests()[3]
				if !strings.Contains(string(last.Body), `"model":"client-model"`) || last.Header.Get("X-Call") != "" || len(last.Header.Values(headerRetryCount)) != 0 {
					t.Errorf("next call: body %s, X-Call %q, retry count %q; want the client's model and neither header", last.Body, last.Header.Get("X-Call"), last.Header.Values(headerRetryCount))
				}
				if got := log.attempts()[3].timeout; got != 7*time.Second {
					t.Errorf("next call's deadline in %v, want 7s", got)
				}
			})
		})
	}
}

// TestConcurrentCallsKeepTheirOverrides ports
// test_concurrent_system_one_overrides (RT16): three concurrent calls with
// their own retry counts, models, states and timeouts keep them on every
// attempt.
func TestConcurrentCallsKeepTheirOverrides(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		type attempt struct {
			body    string
			timeout time.Duration
			count   string
		}
		seen := map[string][]attempt{}
		rec := &testsupport.Recorder{Replies: []testsupport.Reply{rtReply(429, `{"message": "retry"}`, "Retry-After-Ms", "0")}}
		rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			dl, _ := req.Context().Deadline()
			a := attempt{timeout: time.Until(dl), count: absent}
			if v := req.Header.Values(headerRetryCount); len(v) > 0 {
				a.count = strings.Join(v, ",")
			}
			rc, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			body, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				return nil, err
			}
			a.body = string(body)
			mu.Lock()
			seen[req.Header.Get("X-Call")] = append(seen[req.Header.Get("X-Call")], a)
			mu.Unlock()
			return rec.RoundTrip(req)
		})
		c := newTestClient(t, rt, WithRetry(DefaultRetry().MaxRetries(1)), WithTimeout(9*time.Second))
		ctx := bubbleCtx(t)
		qs := noulQuestion(t)
		calls := map[string]struct {
			retries int // 0: the client's policy
			timeout time.Duration
			want    int
		}{
			"one":     {retries: 1, timeout: time.Second, want: 1},
			"three":   {retries: 3, timeout: 3 * time.Second, want: 3},
			"default": {timeout: 2 * time.Second, want: 2},
		}
		var wg sync.WaitGroup
		for name, call := range calls {
			wg.Go(func() {
				opts := []CallOption{Model(name), Header("X-Call", name), Timeout(call.timeout)}
				if call.retries > 0 {
					opts = append(opts, Retry(DefaultRetry().MaxRetries(call.retries-1)))
				}
				_, err := c.SystemOne(ctx, name, qs, opts...)
				if ae, ok := errors.AsType[*APIError](err); !ok || ae.StatusCode != http.StatusTooManyRequests {
					t.Errorf("%s: error = %T %v, want the 429", name, err, err)
				}
			})
		}
		wg.Wait()
		for name, call := range calls {
			got := seen[name]
			want := make([]attempt, call.want)
			for i := range want {
				want[i] = attempt{
					body:    `{"state":"` + name + `","model":"` + name + `",` + noulBody + `}`,
					timeout: time.Duration(call.want) * time.Second,
					count:   wantCounts(call.want)[i],
				}
			}
			if diff := gocmp.Diff(want, got, gocmp.AllowUnexported(attempt{})); diff != "" {
				t.Errorf("%s: attempts (-want +got):\n%s", name, diff)
			}
		}
	})
}

// timeoutText is a network timeout (net.Error with Timeout true) with the
// text an attempt's transport wrote.
type timeoutText string

func (e timeoutText) Error() string { return string(e) }
func (timeoutText) Timeout() bool   { return true }
func (timeoutText) Temporary() bool { return true }

// TestExhaustedTransportRetryReturnsLastError ports
// test_exhausted_transport_retry (RT17): when every attempt fails without a
// response, the call returns the third attempt's error, classified as that
// attempt was: a *TimeoutError naming the call's timeout, or a
// *ConnectionError with the transport's text, each wrapping the transport's
// error of the last attempt.
func TestExhaustedTransportRetryReturnsLastError(t *testing.T) {
	tests := map[string]struct {
		fail  func(n int) error
		check func(t *testing.T, err error)
	}{
		"error: a read timeout every time": {
			fail: func(n int) error { return timeoutText("attempt " + strconv.Itoa(n)) },
			check: func(t *testing.T, err error) {
				if te, ok := errors.AsType[*TimeoutError](err); !ok || te.Timeout != 2*time.Second {
					t.Errorf("error = %T %v, want a *TimeoutError of the call's 2s", err, err)
				}
			},
		},
		"error: a connection error every time": {
			fail: func(n int) error { return errors.New("attempt " + strconv.Itoa(n)) },
			check: func(t *testing.T, err error) {
				if ce, ok := err.(*ConnectionError); !ok || ce.Error() != "Connection error: attempt 3" { //nolint:errorlint // the exact type, as upstream's type(...) is
					t.Errorf("error = %T %v, want the *ConnectionError of attempt 3", err, err)
				}
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var mu sync.Mutex
				n := 0
				rec := &testsupport.Recorder{Respond: func(testsupport.RecordedRequest) testsupport.Reply {
					mu.Lock()
					defer mu.Unlock()
					n++
					return testsupport.Reply{Err: tt.fail(n)}
				}}
				c := newTestClient(t, rec)
				err := systemOneCall(t)(bubbleCtx(t), c, Retry(DefaultRetry().Backoff(time.Millisecond, time.Millisecond, 0.25)), Timeout(2*time.Second))
				if rec.Count() != 3 {
					t.Errorf("%d attempts, want 3", rec.Count())
				}
				if cause := errors.Unwrap(err); cause == nil || cause.Error() != "attempt 3" {
					t.Errorf("the error's cause = %v, want the third attempt's", cause)
				}
				tt.check(t, err)
			})
		})
	}
}

// TestExhaustedRetryKeepsLastAPIError ports
// test_exhausted_retry_preserves_final_http_error (RT18): after 429, 500
// and 503 the call returns the 503, with its body, request id and text.
func TestExhaustedRetryKeepsLastAPIError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var replies []testsupport.Reply
		for i, status := range []int{429, 500, 503} {
			n := strconv.Itoa(i + 1)
			replies = append(replies, rtReply(status, `{"message": "attempt `+n+`"}`, "X-TypeSafe-Request-Id", "request-"+n, "Retry-After-Ms", "0"))
		}
		rec := &testsupport.Recorder{Replies: replies}
		c := newTestClient(t, rec, WithRetry(DefaultRetry()))
		err := systemOneCall(t)(bubbleCtx(t), c)
		ae, ok := errors.AsType[*APIError](err)
		if !ok {
			t.Fatalf("error = %T %v, want an *APIError", err, err)
		}
		id, _ := ae.RequestID()
		got := []string{strconv.Itoa(ae.StatusCode), string(ae.Body), id, ae.Error()}
		want := []string{"503", `{"message": "attempt 3"}`, "request-3", "POST https://api.typesafe.ai/v1/systemone: 503 attempt 3 (request_id=request-3)"}
		if diff := gocmp.Diff(want, got); diff != "" {
			t.Errorf("the last error (-want +got):\n%s", diff)
		}
		if rec.Count() != 3 {
			t.Errorf("%d attempts, want 3", rec.Count())
		}
	})
}

// TestCancelPendingRetry ports test_cancel_pending_retry (RT19, ruling R81
// (1) and (8)): a call waiting to retry ends as soon as its context does,
// after one attempt and before the wait's timer: a cancellation returns
// context.Canceled itself (its cause kept for context.Cause), a deadline a
// *TimeoutError without a timeout wrapping context.DeadlineExceeded, and a
// cancellation before a zero wait makes no second attempt either.
func TestCancelPendingRetry(t *testing.T) {
	cause := errors.New("the caller gave up")
	tests := map[string]struct {
		policy RetryPolicy
		reply  testsupport.Reply
		// then, when set, answers every attempt after the first.
		then *testsupport.Reply
		// attempts is how many attempts the call makes; zero means 1.
		attempts int
		// took is the fake time the transport takes to answer.
		took time.Duration
		// ctx returns the call's context and the function that ends it,
		// nil when the context ends on its own.
		ctx func(t *testing.T) (context.Context, func())
		// cancelInReply ends the context while the transport answers.
		cancelInReply bool
		// waited is the fake time the call runs.
		waited time.Duration
		check  func(t *testing.T, ctx context.Context, err error)
	}{
		"error: a cancellation during the backoff": {
			reply: rtReply(429, `{}`),
			ctx: func(t *testing.T) (context.Context, func()) {
				ctx, cancel := context.WithCancel(bubbleCtx(t))
				return ctx, cancel
			},
			check: func(t *testing.T, _ context.Context, err error) { assertCancelled(t, err) },
		},
		"error: a cancellation with a cause during a 60s Retry-After": {
			// No budget: the default 30 s one would refuse a 60 s wait.
			policy: DefaultRetry().NoBudget(),
			reply:  rtReply(429, `{}`, "Retry-After", "60"),
			ctx: func(t *testing.T) (context.Context, func()) {
				ctx, cancel := context.WithCancelCause(bubbleCtx(t))
				return ctx, func() { cancel(cause) }
			},
			check: func(t *testing.T, ctx context.Context, err error) {
				assertCancelled(t, err)
				if got := context.Cause(ctx); got != cause { //nolint:errorlint // the canceller's own value
					t.Errorf("context.Cause = %v, want %v", got, cause)
				}
			},
		},
		"error: the caller's deadline during a 60s Retry-After": {
			policy: DefaultRetry().NoBudget(),
			reply:  rtReply(429, `{}`, "Retry-After", "60"),
			ctx: func(t *testing.T) (context.Context, func()) {
				ctx, cancel := context.WithTimeout(bubbleCtx(t), 100*time.Millisecond)
				t.Cleanup(cancel)
				return ctx, nil
			},
			waited: 100 * time.Millisecond,
			check: func(t *testing.T, _ context.Context, err error) {
				te, ok := errors.AsType[*TimeoutError](err)
				if !ok || te.Timeout != 0 || !errors.Is(err, context.DeadlineExceeded) || te.Error() != "Request timed out." {
					t.Errorf("error = %T %v, want a *TimeoutError without a timeout wrapping context.DeadlineExceeded", err, err)
				}
			},
		},
		"error: a deadline at the wait's end makes no second attempt": {
			// The review's probe (MINOR 3): the wait and the caller's
			// deadline end at the same instant.
			reply: rtReply(429, `{}`, "Retry-After", "1"),
			ctx: func(t *testing.T) (context.Context, func()) {
				ctx, cancel := context.WithTimeout(bubbleCtx(t), time.Second)
				t.Cleanup(cancel)
				return ctx, nil
			},
			waited: time.Second,
			check: func(t *testing.T, _ context.Context, err error) {
				te, ok := errors.AsType[*TimeoutError](err)
				if !ok || te.Timeout != 0 || !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("error = %T %v, want a *TimeoutError without a timeout wrapping context.DeadlineExceeded", err, err)
				}
			},
		},
		"error: a deadline at a zero wait's start makes no second attempt": {
			// The attempt takes the second the caller has, then asks for no
			// wait.
			reply: rtReply(429, `{}`, "Retry-After-Ms", "0"),
			took:  time.Second,
			ctx: func(t *testing.T) (context.Context, func()) {
				ctx, cancel := context.WithTimeout(bubbleCtx(t), time.Second)
				t.Cleanup(cancel)
				return ctx, nil
			},
			waited: time.Second,
			check: func(t *testing.T, _ context.Context, err error) {
				if _, ok := errors.AsType[*TimeoutError](err); !ok || !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("error = %T %v, want a *TimeoutError wrapping context.DeadlineExceeded", err, err)
				}
			},
		},
		"error: a cancellation during a wait longer than the caller's deadline": {
			// The deadline (10 s) comes before the wait's end (60 s), so the
			// call waits for the context, which the cancellation ends
			// first: context.Canceled itself, not a timeout (review R1).
			policy: DefaultRetry().NoBudget(),
			reply:  rtReply(429, `{}`, "Retry-After", "60"),
			ctx: func(t *testing.T) (context.Context, func()) {
				parent, cancel := context.WithCancel(bubbleCtx(t))
				ctx, stop := context.WithTimeout(parent, 10*time.Second)
				t.Cleanup(stop)
				return ctx, cancel
			},
			check: func(t *testing.T, _ context.Context, err error) { assertCancelled(t, err) },
		},
		"success: a deadline just after the wait's end lets the retry run": {
			// One nanosecond of the caller's time is left when the wait
			// ends, so the retry starts and succeeds (review R-N1).
			reply: rtReply(429, `{}`, "Retry-After", "1"),
			then:  new(rtReply(200, `{"models":[]}`)),
			ctx: func(t *testing.T) (context.Context, func()) {
				ctx, cancel := context.WithTimeout(bubbleCtx(t), time.Second+time.Nanosecond)
				t.Cleanup(cancel)
				return ctx, nil
			},
			attempts: 2,
			waited:   time.Second,
			check: func(t *testing.T, _ context.Context, err error) {
				if err != nil {
					t.Errorf("error = %T %v, want the retry's success", err, err)
				}
			},
		},
		"error: a cancellation before a zero wait": {
			reply: rtReply(429, `{}`, "Retry-After-Ms", "0"),
			ctx: func(t *testing.T) (context.Context, func()) {
				ctx, cancel := context.WithCancel(bubbleCtx(t))
				return ctx, cancel
			},
			cancelInReply: true,
			check:         func(t *testing.T, _ context.Context, err error) { assertCancelled(t, err) },
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, end := tt.ctx(t)
				rec := &testsupport.Recorder{Respond: func(r testsupport.RecordedRequest) testsupport.Reply {
					if r.Index > 0 && tt.then != nil {
						return *tt.then
					}
					time.Sleep(tt.took)
					if tt.cancelInReply {
						end()
					}
					return tt.reply
				}}
				c := newTestClient(t, rec, WithRetry(tt.policy))
				start := time.Now()
				errc := make(chan error, 1)
				go func() { errc <- listCall(ctx, c) }()
				synctest.Wait() // the call has made its attempt and waits to retry
				if n := rec.Count(); n != 1 {
					t.Fatalf("%d attempts before the wait, want 1", n)
				}
				if end != nil && !tt.cancelInReply {
					end()
				}
				err := <-errc
				if got := time.Since(start); got != tt.waited {
					t.Errorf("the call ran %v of fake time, want %v", got, tt.waited)
				}
				tt.check(t, ctx, err)
				want := max(tt.attempts, 1)
				if rec.Count() != want {
					t.Errorf("the transport saw %d requests, want %d", rec.Count(), want)
				}
				assertAttempts(t, c, want)
			})
		})
	}
}

// TestCallerDeadlineEndsAnAttempt pins what a call returns when the
// caller's deadline ends an attempt that the policy would retry: the
// attempt's own *TimeoutError without a timeout, which wraps the
// transport's error, and no wait and no second attempt.
func TestCallerDeadlineEndsAnAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cause := errors.New("transport: the request's context ended")
		var n int
		rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			n++
			<-req.Context().Done()
			return nil, cause
		})
		c := newTestClient(t, rt, WithRetry(DefaultRetry()))
		ctx, cancel := context.WithTimeout(bubbleCtx(t), time.Second)
		defer cancel()
		start := time.Now()
		err := listCall(ctx, c)
		te, ok := errors.AsType[*TimeoutError](err)
		if !ok || te.Timeout != 0 || !errors.Is(err, cause) {
			t.Errorf("error = %T %v, want the attempt's own *TimeoutError without a timeout, wrapping the transport's error", err, err)
		}
		if got := time.Since(start); got != time.Second || n != 1 {
			t.Errorf("the call ran %v of fake time and made %d attempts, want 1s and 1", got, n)
		}
		assertAttempts(t, c, 1)
	})
}

// TestMaxRetriesCountsAttempts ports test_retry_policy_max_retries (RT20):
// a call makes max_retries + 1 attempts.
func TestMaxRetriesCountsAttempts(t *testing.T) {
	tests := map[string]struct {
		maxRetries, attempts int
	}{
		"error: 0 retries, 1 attempt":  {maxRetries: 0, attempts: 1},
		"error: 1 retry, 2 attempts":   {maxRetries: 1, attempts: 2},
		"error: 4 retries, 5 attempts": {maxRetries: 4, attempts: 5},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := &testsupport.Recorder{Replies: []testsupport.Reply{rtReply(429, `{"message": "slow"}`, "Retry-After-Ms", "0")}}
				c := newTestClient(t, rec, WithRetry(DefaultRetry().MaxRetries(tt.maxRetries)))
				if ae, ok := errors.AsType[*APIError](listCall(bubbleCtx(t), c)); !ok || ae.StatusCode != http.StatusTooManyRequests {
					t.Fatal("want the 429")
				}
				if n := rec.Count(); n != tt.attempts {
					t.Errorf("%d attempts, want %d", n, tt.attempts)
				}
			})
		})
	}
}

// TestCustomStatusesReplaceDefault ports test_retry_policy_custom_statuses
// (RT21): Statuses replaces the default set: 409 is retried, 500 no longer.
func TestCustomStatusesReplaceDefault(t *testing.T) {
	tests := map[string]struct {
		status, attempts int
	}{
		"error: 409 retried":     {status: 409, attempts: 3},
		"error: 500 not retried": {status: 500, attempts: 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := &testsupport.Recorder{Replies: []testsupport.Reply{rtReply(tt.status, `{"message": "x"}`, "Retry-After-Ms", "0")}}
				c := newTestClient(t, rec, WithRetry(DefaultRetry().Statuses(409)))
				if _, ok := errors.AsType[*APIError](listCall(bubbleCtx(t), c)); !ok {
					t.Fatal("want an *APIError")
				}
				if n := rec.Count(); n != tt.attempts {
					t.Errorf("%d attempts, want %d", n, tt.attempts)
				}
			})
		})
	}
}

// TestPerCallMaxRetries ports test_retry_policy_per_call_override (RT22): a
// call's NoRetry makes one attempt under a client that retries twice; and a
// call's policy replaces the client's as a whole, so a setting the call's
// policy leaves at its default is the default, not the client's.
func TestPerCallMaxRetries(t *testing.T) {
	tests := map[string]struct {
		client, call RetryPolicy
		status       int
		attempts     int
	}{
		"error: max_retries 0 on the call": {client: DefaultRetry().MaxRetries(2), call: DefaultRetry().MaxRetries(0), status: 429, attempts: 1},
		"error: NoRetry on the call":       {client: DefaultRetry(), call: NoRetry(), status: 503, attempts: 1},
		"error: the call's default statuses, not the client's 409 alone": {
			client: DefaultRetry().Statuses(409), call: DefaultRetry(), status: 429, attempts: 3,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := &testsupport.Recorder{Replies: []testsupport.Reply{rtReply(tt.status, `{}`, "Retry-After-Ms", "0")}}
				c := newTestClient(t, rec, WithRetry(tt.client))
				if _, ok := errors.AsType[*APIError](listCall(bubbleCtx(t), c, Retry(tt.call))); !ok {
					t.Fatal("want an *APIError")
				}
				if n := rec.Count(); n != tt.attempts {
					t.Errorf("%d attempts, want %d", n, tt.attempts)
				}
			})
		})
	}
}

// TestPredicateOptsIn is RT23's Go half (test_retry_policy_exceptions_and_
// predicate; a partial deviation: the Python SDK's exceptions setting is
// dropped, and a Predicate that tests the error's type does its work): a
// predicate retries an error the rules do not (a 404), is asked only then,
// can opt a 2xx whose body does not validate in, which a 2xx in the status
// set never does, and cannot make a call retry a cancellation.
func TestPredicateOptsIn(t *testing.T) {
	is404 := func(err error) bool {
		ae, ok := errors.AsType[*APIError](err)
		return ok && ae.StatusCode == http.StatusNotFound
	}
	anyAPIError := func(err error) bool { _, ok := errors.AsType[*APIError](err); return ok }
	anyValidation := func(err error) bool { _, ok := errors.AsType[*ResponseValidationError](err); return ok }
	anyTooLarge := func(err error) bool { _, ok := errors.AsType[*ResponseTooLargeError](err); return ok }
	always := func(error) bool { return true }
	tests := map[string]struct {
		policy   RetryPolicy
		client   []ClientOption
		reply    testsupport.Reply
		attempts int
		// asked is how often the predicate is called, -1 to leave it out.
		asked int
		check func(t *testing.T, err error)
	}{
		"error: a predicate retries a 404": {
			policy: DefaultRetry().MaxRetries(1), reply: rtReply(404, `{"message": "gone"}`, "Retry-After-Ms", "0"), attempts: 2, asked: 2,
		},
		"error: a type test in place of exceptions={TypeSafeAPIError}": {
			policy: DefaultRetry().MaxRetries(1).Predicate(anyAPIError), reply: rtReply(404, `{"message": "gone"}`, "Retry-After-Ms", "0"), attempts: 2, asked: -1,
		},
		"error: the predicate is not asked about an error the rules retry": {
			policy: DefaultRetry().MaxRetries(1), reply: rtReply(503, `{}`, "Retry-After-Ms", "0"), attempts: 2, asked: 0,
		},
		"error: a 2xx in the status set is never retried": {
			policy: DefaultRetry().MaxRetries(1).Statuses(200), reply: rtReply(200, `{"models": 1}`, "Retry-After-Ms", "0"), attempts: 1, asked: -1,
			check: func(t *testing.T, err error) {
				if _, ok := errors.AsType[*ResponseValidationError](err); !ok {
					t.Errorf("error = %T %v, want a *ResponseValidationError", err, err)
				}
			},
		},
		"error: a predicate opts a 2xx that does not validate in, and its Retry-After holds": {
			policy: DefaultRetry().MaxRetries(1).Predicate(anyValidation), reply: rtReply(200, `{"models": 1}`, "Retry-After-Ms", "0"), attempts: 2, asked: -1,
		},
		"error: a predicate opts a 2xx over the size limit in, and its Retry-After holds": {
			policy: DefaultRetry().MaxRetries(1).Predicate(anyTooLarge), client: []ClientOption{WithMaxResponseBytes(8)},
			reply: rtReply(200, `{"models":[]}`, "Retry-After-Ms", "0"), attempts: 2, asked: -1,
			check: func(t *testing.T, err error) {
				if _, ok := errors.AsType[*ResponseTooLargeError](err); !ok {
					t.Errorf("error = %T %v, want a *ResponseTooLargeError", err, err)
				}
			},
		},
		"error: a predicate cannot retry a cancellation": {
			policy: DefaultRetry().MaxRetries(1).Predicate(always), reply: testsupport.Reply{Err: context.Canceled}, attempts: 1, asked: -1,
			check: func(t *testing.T, err error) {
				if !errors.Is(err, context.Canceled) {
					t.Errorf("error = %T %v, want one wrapping context.Canceled", err, err)
				}
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var mu sync.Mutex
				asked := 0
				policy := tt.policy
				if tt.asked >= 0 {
					policy = policy.Predicate(func(err error) bool {
						mu.Lock()
						asked++
						mu.Unlock()
						return is404(err)
					})
				}
				rec := &testsupport.Recorder{Replies: []testsupport.Reply{tt.reply}}
				log := &attemptLog{rec: rec}
				c := newTestClient(t, log, append([]ClientOption{WithRetry(policy)}, tt.client...)...)
				err := listCall(bubbleCtx(t), c)
				if err == nil {
					t.Fatal("List succeeded, want an error")
				}
				if n := rec.Count(); n != tt.attempts {
					t.Errorf("%d attempts, want %d", n, tt.attempts)
				}
				// Every reply asks for no wait, where the backoff would
				// wait 500 ms: the server's wait holds for every class a
				// response carries, the opted-in ones included.
				if diff := gocmp.Diff(make([]time.Duration, tt.attempts-1), waits(log.attempts(), 0)); diff != "" {
					t.Errorf("waits (-want +got):\n%s", diff)
				}
				if tt.asked >= 0 && asked != tt.asked {
					t.Errorf("the predicate was asked %d times, want %d", asked, tt.asked)
				}
				if tt.check != nil {
					tt.check(t, err)
				}
			})
		})
	}
}

// TestRetryClassSwitches pins the settings that turn a built-in class off,
// and the classes no setting turns on: ConnectionErrors(false) leaves a
// *ConnectionError to one attempt and a *TimeoutError retried, and
// TimeoutErrors(false) the reverse, as the Python SDK checks its timeout
// class first (py:_core/retry.py:100-109); Predicate(nil) removes a
// predicate; and the transport's refusal of a host that did not negotiate
// HTTP/2, a *ConfigError wrapping ErrHTTP2NotNegotiated (section 6.3), is
// never retried under the production policy.
func TestRetryClassSwitches(t *testing.T) {
	is404 := func(err error) bool {
		ae, ok := errors.AsType[*APIError](err)
		return ok && ae.StatusCode == http.StatusNotFound
	}
	connection := testsupport.Reply{Err: errors.New("connection refused")}
	timeout := testsupport.Reply{Err: netTimeout{}}
	tests := map[string]struct {
		policy   RetryPolicy
		reply    testsupport.Reply
		attempts int
		check    func(t *testing.T, err error)
	}{
		"error: ConnectionErrors(false) makes a connection error final": {
			policy: DefaultRetry().ConnectionErrors(false), reply: connection, attempts: 1,
			check: func(t *testing.T, err error) {
				if _, ok := errors.AsType[*ConnectionError](err); !ok {
					t.Errorf("error = %T %v, want a *ConnectionError", err, err)
				}
			},
		},
		"error: ConnectionErrors(false) leaves timeouts retried": {
			policy: DefaultRetry().ConnectionErrors(false), reply: timeout, attempts: 3,
		},
		"error: TimeoutErrors(false) makes a timeout final": {
			policy: DefaultRetry().TimeoutErrors(false), reply: timeout, attempts: 1,
			check: func(t *testing.T, err error) {
				if _, ok := errors.AsType[*TimeoutError](err); !ok {
					t.Errorf("error = %T %v, want a *TimeoutError", err, err)
				}
			},
		},
		"error: TimeoutErrors(false) leaves connection errors retried": {
			policy: DefaultRetry().TimeoutErrors(false), reply: connection, attempts: 3,
		},
		"error: Predicate(nil) removes a predicate": {
			policy: DefaultRetry().Predicate(is404).Predicate(nil), reply: rtReply(404, `{"message": "gone"}`), attempts: 1,
		},
		"error: a host that did not negotiate HTTP/2 is never retried": {
			policy: DefaultRetry(), reply: testsupport.Reply{Err: fmt.Errorf("%w: the API host's TLS handshake negotiated %q", h2gate.ErrNotNegotiated, "http/1.1")},
			attempts: 1,
			check: func(t *testing.T, err error) {
				if _, ok := errors.AsType[*ConfigError](err); !ok || !errors.Is(err, ErrHTTP2NotNegotiated) {
					t.Errorf("error = %T %v, want a *ConfigError wrapping ErrHTTP2NotNegotiated", err, err)
				}
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := &testsupport.Recorder{Replies: []testsupport.Reply{tt.reply}}
				c := newTestClient(t, rec, WithRetry(tt.policy))
				err := listCall(bubbleCtx(t), c)
				if err == nil {
					t.Fatal("List succeeded, want an error")
				}
				if n := rec.Count(); n != tt.attempts {
					t.Errorf("%d attempts, want %d", n, tt.attempts)
				}
				if tt.check != nil {
					tt.check(t, err)
				}
			})
		})
	}
}

// TestWaitOptions ports test_retry_policy_wait_options (RT24): with the
// jitter source at 0 and Retry-After 5, the default policy waits 5 s,
// RespectRetryAfter(false) the 500 ms backoff, and a 200 ms initial backoff
// 200 ms; the same waits are measured through the client.
func TestWaitOptions(t *testing.T) {
	tests := map[string]struct {
		policy RetryPolicy
		want   time.Duration
	}{
		"success: Retry-After honoured":           {policy: DefaultRetry(), want: 5 * time.Second},
		"success: Retry-After ignored":            {policy: DefaultRetry().RespectRetryAfter(false), want: 500 * time.Millisecond},
		"success: ignored, a 200ms first backoff": {policy: DefaultRetry().Backoff(200*time.Millisecond, 5*time.Second, 0.25).RespectRetryAfter(false), want: 200 * time.Millisecond},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := tt.policy
			r := retryState{policy: &p, random: fixedRandom(0)}
			if got := r.delay(1, &APIError{StatusCode: 429, Header: rtHeader("Retry-After", "5")}); got != tt.want {
				t.Errorf("delay = %v, want %v", got, tt.want)
			}
			synctest.Test(t, func(t *testing.T) {
				log := &attemptLog{rec: &testsupport.Recorder{Replies: []testsupport.Reply{
					rtReply(429, `{}`, "Retry-After", "5"), rtReply(200, `{"models":[]}`),
				}}}
				c := newTestClient(t, log, WithRetry(tt.policy))
				c.random = fixedRandom(0)
				if err := listCall(bubbleCtx(t), c); err != nil {
					t.Fatalf("List: %v", err)
				}
				if diff := gocmp.Diff([]time.Duration{tt.want}, waits(log.attempts(), 0)); diff != "" {
					t.Errorf("waits through the client (-want +got):\n%s", diff)
				}
			})
		})
	}
}

// TestBackoffExtremeValues ports test_backoff_extreme_values (RT25) with the
// jitter source at 0. Python's 1e-300, 1e300 and 1e308 seconds are not
// Durations; their rows use the shortest (1 ns) and the longest Duration in
// their place and keep their meaning: a wait below half a millisecond
// rounds to 0, a maximum reached by doubling is the wait, and a maximum
// below the initial backoff caps it without rounding (600 µs).
func TestBackoffExtremeValues(t *testing.T) {
	tests := map[string]struct {
		initial, maximum time.Duration
		retry            int
		want             time.Duration
	}{
		"success: 1ns, the longest, retry 1 rounds to 0":       {initial: 1, maximum: maxDuration, retry: 1, want: 0},
		"success: 1ns, the longest, retry 2000 is the maximum": {initial: 1, maximum: maxDuration, retry: 2000, want: maxDuration},
		"success: the longest, the longest, retry 1":           {initial: maxDuration, maximum: maxDuration, retry: 1, want: maxDuration},
		"success: 500ms capped at 600µs":                       {initial: 500 * time.Millisecond, maximum: 600 * time.Microsecond, retry: 1, want: 600 * time.Microsecond},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := backoff(tt.retry, tt.initial, tt.maximum, 0.25, fixedRandom(0)); got != tt.want {
				t.Errorf("backoff = %v, want %v", got, tt.want)
			}
			p := DefaultRetry().Backoff(tt.initial, tt.maximum, 0.25)
			r := retryState{policy: &p, random: fixedRandom(0)}
			if got := r.delay(tt.retry, errors.New("no response")); got != tt.want {
				t.Errorf("delay = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRoundMillis pins roundMillis to Python's round(x, 3), each row checked
// against CPython: the exact value of the float, half to even. 0.0005 is
// 0.00050000000000000001… and rounds up; 1.0005 is 1.00049999999999994…
// and rounds down; 0.0625 and 0.1875 are exact ties and round to the even
// 0.062 and 0.188.
func TestRoundMillis(t *testing.T) {
	tests := map[string]struct {
		in, want float64
	}{
		"success: 2.675 is below the half":  {in: 2.675, want: 2.675},
		"success: 1.0005 is below the half": {in: 1.0005, want: 1.0},
		"success: 0.0005 is above the half": {in: 0.0005, want: 0.001},
		"success: 0.0015 rounds to 0.002":   {in: 0.0015, want: 0.002},
		"success: 0.3749999 rounds up":      {in: 0.3749999, want: 0.375},
		"success: 1e-9 rounds to 0":         {in: 1e-9, want: 0},
		"success: 0.0006 rounds to 0.001":   {in: 0.0006, want: 0.001},
		"success: 5 stays":                  {in: 5, want: 5},
		"success: the exact tie 0.0625":     {in: 0.0625, want: 0.062},
		"success: 0.0375 is below the half": {in: 0.0375, want: 0.037},
		"success: the exact tie 0.1875":     {in: 0.1875, want: 0.188},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := roundMillis(tt.in); got != tt.want {
				t.Errorf("roundMillis(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
