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
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
)

// TestConstantsMatchPython pins every name and default the SDK shares with
// typesafe-sdk-python 0.7.1 to the value its source spells
// (py:constants.py, py:_core/constants.py), so a typo in one of them fails
// here rather than on the wire.
func TestConstantsMatchPython(t *testing.T) {
	got := map[string]any{
		"API_KEY_ENV":                   APIKeyEnv,
		"BASE_URL_ENV":                  BaseURLEnv,
		"DEFAULT_MODEL_ENV":             DefaultModelEnv,
		"DEFAULT_BASE_URL":              DefaultBaseURL,
		"DEFAULT_MODEL":                 DefaultModel,
		"DEFAULT_TIMEOUT":               DefaultTimeout,
		"SYSTEM_ONE_PATH":               systemOnePath,
		"MODELS_PATH":                   modelsPath,
		"JSON_CONTENT_TYPE":             jsonContentType,
		"AUTHORIZATION_HEADER":          headerAuthorization,
		"ACCEPT_HEADER":                 headerAccept,
		"CONTENT_TYPE_HEADER":           headerContentType,
		"USER_AGENT_HEADER":             headerUserAgent,
		"SDK_HEADER":                    headerSDK,
		"RUNTIME_HEADER":                headerRuntime,
		"RETRY_COUNT_HEADER":            headerRetryCount,
		"REQUEST_ID_HEADER":             headerRequestID,
		"DefaultConnectTimeout":         DefaultConnectTimeout,
		"DefaultMaxResponseBytes (NF5)": int64(DefaultMaxResponseBytes),
	}
	want := map[string]any{
		"API_KEY_ENV":                   "TYPESAFE_API_KEY",
		"BASE_URL_ENV":                  "TYPESAFE_BASE_URL",
		"DEFAULT_MODEL_ENV":             "TYPESAFE_DEFAULT_MODEL",
		"DEFAULT_BASE_URL":              "https://api.typesafe.ai",
		"DEFAULT_MODEL":                 "jev-latest",
		"DEFAULT_TIMEOUT":               10 * time.Second,
		"SYSTEM_ONE_PATH":               "/v1/systemone",
		"MODELS_PATH":                   "/v1/models",
		"JSON_CONTENT_TYPE":             "application/json",
		"AUTHORIZATION_HEADER":          "Authorization",
		"ACCEPT_HEADER":                 "Accept",
		"CONTENT_TYPE_HEADER":           "Content-Type",
		"USER_AGENT_HEADER":             "User-Agent",
		"SDK_HEADER":                    "X-TypeSafe-SDK",
		"RUNTIME_HEADER":                "X-TypeSafe-Runtime",
		"RETRY_COUNT_HEADER":            "X-TypeSafe-Retry-Count",
		"REQUEST_ID_HEADER":             "x-typesafe-request-id",
		"DefaultConnectTimeout":         10 * time.Second,
		"DefaultMaxResponseBytes (NF5)": int64(16 * 1024 * 1024),
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("constants mismatch (-want +got):\n%s", diff)
	}
}
