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

import "time"

// The environment variables a client reads for a setting its options leave
// unset, as typesafe-sdk-python names them (py:constants.py). A value is
// trimmed of leading and trailing whitespace, and a variable that is unset or
// blank counts as unset.
const (
	// APIKeyEnv names the variable holding the API key.
	APIKeyEnv = "TYPESAFE_API_KEY" //nolint:gosec // G101: the name of the variable that holds the key, not a key.

	// BaseURLEnv names the variable holding the API base URL.
	BaseURLEnv = "TYPESAFE_BASE_URL"

	// DefaultModelEnv names the variable holding the model a request names
	// when the call names none.
	DefaultModelEnv = "TYPESAFE_DEFAULT_MODEL"
)

// The settings a client uses when neither an option nor the environment
// gives one.
const (
	// DefaultBaseURL is the API base URL.
	DefaultBaseURL = "https://api.typesafe.ai"

	// DefaultModel is the model a request names when the call names none.
	DefaultModel = "jev-latest"

	// DefaultTimeout is the deadline of each attempt of a request.
	DefaultTimeout = 10 * time.Second

	// DefaultConnectTimeout is the deadline for opening a connection,
	// inside the deadline of the attempt that opens it.
	DefaultConnectTimeout = 10 * time.Second

	// DefaultMaxResponseBytes is the largest response body a request reads:
	// 16 MiB.
	DefaultMaxResponseBytes = 16 << 20
)

// maxMaxResponseBytes is the largest limit WithMaxResponseBytes accepts:
// 1 GiB. A System One response is a few kilobytes; a limit past this one is a
// unit mistake rather than a need, and it would let one response hold that
// much memory.
const maxMaxResponseBytes = 1 << 30

// The API endpoints, appended to the base URL (py:_core/constants.py:5-6).
const (
	systemOnePath = "/v1/systemone"
	modelsPath    = "/v1/models"
)

// The request and response headers the SDK reads or writes, spelled as
// typesafe-sdk-python spells them (py:_core/constants.py:11-18). An
// [net/http.Header] stores a name in its canonical form ("X-Typesafe-Sdk"),
// so these are for Get and Set, never for indexing the map directly; the
// wire form is the same either way, since HTTP/2 sends every name in lower
// case and HTTP/1.1 names are case-insensitive.
const (
	headerAuthorization = "Authorization"
	headerAccept        = "Accept"
	headerContentType   = "Content-Type"
	headerUserAgent     = "User-Agent"
	headerSDK           = "X-TypeSafe-SDK"
	headerRuntime       = "X-TypeSafe-Runtime"
	headerRetryCount    = "X-TypeSafe-Retry-Count"
	headerRequestID     = "x-typesafe-request-id"
)

// jsonContentType is the media type of every request body and of every
// response the SDK reads.
const jsonContentType = "application/json"
