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

package engine

import (
	"net/http"
	"strconv"
)

// The API endpoints, appended to the base URL (py:_core/constants.py:5-6).
const (
	SystemOnePath = "/v1/systemone"
	ModelsPath    = "/v1/models"
)

// HeaderRetryCount is the header only retries carry, with the number of
// attempts before them (py:_core/transport.py:66-68).
const HeaderRetryCount = "X-TypeSafe-Retry-Count"

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

// CanonicalRetryCount is the canonical form of X-TypeSafe-Retry-Count, the
// key a header map holds it under.
var CanonicalRetryCount = http.CanonicalHeaderKey(HeaderRetryCount)

// RetryCountValue returns the X-TypeSafe-Retry-Count value of attempt, which
// is at least 1: the number of attempts before it.
func RetryCountValue(attempt int) []string {
	if attempt <= len(retryCountValues) {
		return retryCountValues[attempt-1]
	}
	return []string{strconv.Itoa(attempt)}
}
