// Copyright 2026 The decision-model-sdk-go Authors.
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

package decision

import (
	"time"
)

// The environment variables a client reads for a setting its options leave
// unset. A value is trimmed of leading and trailing whitespace, and a
// variable that is unset or blank counts as unset. They are named for
// decision models rather than for one vendor, because the API is served by
// more than one: typesafe-sdk-python's TYPESAFE_API_KEY, TYPESAFE_BASE_URL
// and TYPESAFE_DEFAULT_MODEL are not read, not even as a fallback, so a key
// set for one vendor is never sent to another.
//
// The one exception is a [Provider] given with [WithProvider]: its APIKeyEnv
// names a vendor's own variable, such as TYPESAFE_API_KEY for the TypeSafe AI
// preset of the provider package, and the client then reads that variable in
// place of APIKeyEnv and none of the three below. The variable is read only by
// the client of that provider, whose base URL is the vendor's, so a key still
// reaches one host.
const (
	// APIKeyEnv names the variable holding the API key.
	APIKeyEnv = "DECISION_MODEL_API_KEY"

	// BaseURLEnv names the variable holding the API base URL.
	BaseURLEnv = "DECISION_MODEL_BASE_URL"

	// DefaultModelEnv names the variable holding the model a request names
	// when the call names none.
	DefaultModelEnv = "DECISION_MODEL_DEFAULT_MODEL"
)

// The settings a client uses when neither an option nor the environment
// gives one. There is no default base URL and no default model: the API is
// served by more than one vendor, and either default would pick one of them
// for a caller who named none. The one exception is a [Provider]'s BaseURL
// and DefaultModel: a provider is the caller naming the vendor, so its base
// URL and its default model are the caller's choice too.
const (
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
)

// jsonContentType is the media type of every request body and of every
// response the SDK reads.
const jsonContentType = "application/json"
