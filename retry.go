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
	"net/http"
	"strconv"
)

// RetryPolicy decides whether a call tries a failed attempt again, as the
// Python SDK's RetryPolicy does. [DefaultRetry] is the policy a client uses
// unless [WithRetry] sets another, and [NoRetry] the policy of one attempt
// per call; the [Retry] call option passes a policy to one call.
//
// In this version of the SDK every call makes one attempt, whatever its
// policy: a policy is accepted and kept with the client or the call, and the
// settings that make a call retry are not there yet.
type RetryPolicy struct {
	_ struct{}
}

// DefaultRetry returns the policy a client uses unless [WithRetry] sets
// another, as the Python SDK uses RetryPolicy() when its retry argument is
// None. It is the zero RetryPolicy.
func DefaultRetry() RetryPolicy { return RetryPolicy{} }

// NoRetry returns the policy that never tries a call again: every call makes
// exactly one attempt, as the Python SDK's RetryPolicy(max_retries=0) does.
func NoRetry() RetryPolicy { return RetryPolicy{} }

// retryState is one call's progress through its policy: the loop of a call
// asks it after every attempt whether to make another.
type retryState struct {
	policy RetryPolicy
}

// again reports whether the call makes another attempt after attempt (0 for
// the first), which ended with err: a response error, or nil for a response
// that decoded. No policy retries in this version, so it reports false and
// never waits.
func (*retryState) again(context.Context, int, error) bool { return false }

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
