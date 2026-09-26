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
	"errors"
	"reflect"
	"testing"
)

// TestRetryPolicyRules checks where a RetryPolicy keeps its statuses and
// predicate (ruling R97-corr (c), W5.3): behind one pointer, so that the
// policy is 56 bytes and the options of a call, which hold it by value, fit
// the 128-byte size class; copies share the pointer, and a setter replaces
// it without touching the policy it was called on or any copy of it. The
// policy stays incomparable, as it was before the pointer (review V63).
func TestRetryPolicyRules(t *testing.T) {
	if got := reflect.TypeFor[RetryPolicy]().Size(); got != 56 {
		t.Errorf("RetryPolicy is %d bytes, want 56", got)
	}
	if reflect.TypeFor[RetryPolicy]().Comparable() {
		t.Error("RetryPolicy is comparable, want it not: == would compare two policies' rules by identity (review V63)")
	}
	if got := reflect.TypeFor[callOptions]().Size(); got > 128 {
		t.Errorf("callOptions is %d bytes, want at most 128, one size class below 160", got)
	}
	errNope := errors.New("nope")
	accept := func(err error) bool { return errors.Is(err, errNope) }
	base := DefaultRetry().Statuses(409)
	withPredicate := base.Predicate(accept)
	moreStatuses := withPredicate.Statuses(503)
	if base.rules.predicate != nil || !base.retriesStatus(409) || base.retriesStatus(503) {
		t.Errorf("Predicate or Statuses changed the policy it was called on: %+v", *base.rules)
	}
	if !withPredicate.retriesStatus(409) || !withPredicate.retryable(errNope) {
		t.Error("Predicate lost the policy's statuses or its own predicate")
	}
	if moreStatuses.retriesStatus(409) || !moreStatuses.retriesStatus(503) || !moreStatuses.retryable(errNope) {
		t.Error("Statuses lost the predicate or kept the old statuses")
	}
	if withPredicate.retriesStatus(503) {
		t.Error("Statuses changed the copy it was called on")
	}
	if none := DefaultRetry(); none.rules != nil || !none.retriesStatus(503) || none.retryable(errNope) {
		t.Errorf("DefaultRetry() holds rules %+v, want none and the default statuses", none.rules)
	}
	if cleared := withPredicate.Predicate(nil); cleared.retryable(errNope) || !cleared.retriesStatus(409) {
		t.Error("Predicate(nil) did not remove the predicate, or dropped the statuses")
	}
}
