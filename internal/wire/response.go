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

package wire

import "net/http"

// RequestIDHeader is the canonical form of the response header that carries
// the server's identifier for a request (x-typesafe-request-id on the wire).
const RequestIDHeader = "X-Typesafe-Request-Id"

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

// RequestID returns the server's identifier for the request, from the first
// value of the x-typesafe-request-id header, and whether the header was
// present. The value is the server's text as it arrived: a caller that puts
// it into a log line or an error message escapes and cuts it first.
func (m *ResponseMeta) RequestID() (string, bool) {
	values := m.Header[RequestIDHeader]
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

// ErrorData is what an unsuccessful response carried, as the lenient
// error-body reader leaves it for the root package's error types.
type ErrorData struct {
	// Meta is the response's status, header and body.
	Meta ResponseMeta
	// Endpoint is the request method and URL without credentials, query or
	// fragment, or empty when it is not known.
	Endpoint string
	// Message is the message the body carried, or the text standing in for
	// one when it carried none.
	Message string
	// ErrorType is the server's machine-readable name for the failure, from
	// detail.error_type, when HasErrorType is true.
	ErrorType string
	// HasErrorType reports whether the body carried an error type.
	HasErrorType bool
}
