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

package wire

import (
	"net/http"
	"strings"
)

// RequestIDHeader is the canonical form of the response header that carries
// the server's identifier for a request (x-typesafe-request-id on the wire).
const RequestIDHeader = "X-Typesafe-Request-Id"

// FallbackRequestIDHeader is the canonical form of the response header read
// for the request's identifier when [RequestIDHeader] is absent
// (x-request-id on the wire): the vendors that serve the API without
// TypeSafe AI's header name send their identifier under it.
const FallbackRequestIDHeader = "X-Request-Id"

// RequestIDValues returns the values of the header that carries the
// server's identifier for the request in h: [RequestIDHeader]'s, or
// [FallbackRequestIDHeader]'s when the first is absent, or nil. The slice is
// h's own and must not be modified.
func RequestIDValues(h http.Header) []string {
	if values := h[RequestIDHeader]; len(values) > 0 {
		return values
	}
	return h[FallbackRequestIDHeader]
}

// Usage is the token usage a response reported. The API may leave either
// count out; an absent count has its Has flag false and its value zero, which
// is never the same as a reported zero.
type Usage struct {
	// InputTokens is the number of billable input tokens.
	InputTokens uint64
	// OutputTokens is the number of output tokens.
	OutputTokens uint64
	// HasInputTokens reports whether the response carried input_tokens.
	HasInputTokens bool
	// HasOutputTokens reports whether the response carried output_tokens.
	HasOutputTokens bool
}

// SystemOneResult is the decoded body of a System One response: the model
// that answered, the token usage and the answers.
type SystemOneResult struct {
	// Model is the model that answered.
	Model string
	// Usage is the token usage the response reported.
	Usage Usage
	// Answers holds the answers, keyed by question name.
	Answers Answers
}

// ModelList is the decoded body of a list-models response.
type ModelList struct {
	// Models lists the models the account can use, in the order the
	// response lists them.
	Models []ModelCard
}

// ModelCard describes one model the account can use, as GET /v1/models lists
// it.
type ModelCard struct {
	// Name is the model name or alias a request's model member accepts.
	Name string
	// Description is the human-readable description of the model.
	Description string
	// ReleaseDate is the release date, formatted as YYYY-MM-DD.
	ReleaseDate string
}

// ResponseMeta is the HTTP side of a response: what it arrived with, kept
// for the caller and for error reports.
type ResponseMeta struct {
	// Status is the HTTP status code.
	Status int
	// Header is the response header. It is shared, not copied.
	Header http.Header
	// Body is the body exactly as it was received. It is shared, not
	// copied, and must not be modified.
	Body []byte
}

// RequestID returns the server's identifier for the request, from the
// x-typesafe-request-id header, or from x-request-id when that is absent
// ([RequestIDValues]), and whether either header was present. A header
// repeated in the response yields its values joined with ", " in the order
// they arrived, as the Python SDK's request_id reads them (httpx's
// Headers.get, py:_core/errors.py:115); a single value is returned as is,
// without allocating. The header is read, never modified. The value is the
// server's text as it arrived: a caller that puts it into a log line or an
// error message escapes and cuts it first.
func (m *ResponseMeta) RequestID() (string, bool) {
	values := RequestIDValues(m.Header)
	// Join returns "" for no value and the value itself for one, without
	// allocating; the second result tells the two empty cases apart.
	return strings.Join(values, ", "), len(values) > 0
}
