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

// RetryReason is one retry's cause, upstream's RetryReasons
// (src/system_one_adapter/_utils/error_handling.py:30-40).
type RetryReason struct {
	// Category is the mechanism that asked for another attempt:
	// "provider_error" for a transient provider failure, or
	// "malformed_structure" for a corrective retry after malformed output.
	Category string
	// Message is the cause: the failed attempt's error text (upstream's
	// msg, str() of the error).
	Message string
}
