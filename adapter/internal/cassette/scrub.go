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

package cassette

// The scrub upstream's recording applies before a cassette is written,
// as tests/conftest.py of system-one-adapter-python v0.2.1 configures it:
// a credential header is stripped outright rather than masked, a
// credential query parameter is removed, and response headers are reduced
// to an allowlist. TestScrubMatchesUpstream pins these lists against that
// file's values and against every committed cassette, and a recorder
// writing new cassettes applies the same lists.
var (
	// FilteredRequestHeaders holds the request header names upstream's
	// recording removes (its FILTERED_HEADERS), lowercase as upstream
	// spells them.
	FilteredRequestHeaders = []string{
		"authorization",
		"x-api-key",
		"x-goog-api-key",
		"api-key",
		"openai-organization",
		"openai-project",
		"cookie",
		"set-cookie",
	}
	// FilteredQueryParameters holds the query parameter names upstream's
	// recording removes (its filter_query_parameters).
	FilteredQueryParameters = []string{"api_key", "key"}
	// AllowedResponseHeaders holds the only response header names a
	// recording keeps (its ALLOWED_RESPONSE_HEADERS), lowercase.
	AllowedResponseHeaders = []string{"content-type"}
)
