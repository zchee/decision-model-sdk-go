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
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
)

// redacted is what a credential is printed as.
const redacted = "***"

// secretHeaderNames are the header names, in lower case, whose values are
// credentials (py:_core/constants.py:21).
var secretHeaderNames = []string{"authorization", "proxy-authorization", "x-api-key", "api-key", "cookie", "set-cookie"}

// isSecretHeader reports whether the value of the header name is a
// credential by its name, compared without regard to case: one of
// secretHeaderNames, or a name that contains "token" or "secret"
// (py:_core/logging.py:32-34).
func isSecretHeader(name string) bool {
	lower := strings.ToLower(name)
	return slices.Contains(secretHeaderNames, lower) || strings.Contains(lower, "token") || strings.Contains(lower, "secret")
}

// isCredential reports whether the values of the header name must not be
// printed: the name marks them as credentials ([isSecretHeader]), or one of
// them holds the API key, under whatever name the caller sent it. The second
// test goes past typesafe-sdk-python, which redacts by name alone.
func isCredential(name string, values []string, apiKey string) bool {
	if isSecretHeader(name) {
		return true
	}
	return apiKey != "" && slices.ContainsFunc(values, func(v string) bool { return strings.Contains(v, apiKey) })
}

// redactedHeaders is a header map as a log record shows it: a group with one
// attribute per header, in name order, whose value is the header's values
// joined by ", ", or "***" when they are a credential ([isCredential]).
//
// It is a [slog.LogValuer], so the map is walked and its values joined only
// when a handler keeps the record. Boxing the value into an attribute still
// allocates, so a request path that must not allocate with debug records off
// checks [slog.Logger.Enabled] before it builds the attribute.
type redactedHeaders struct {
	header http.Header
	apiKey string
}

// LogValue returns the redacted headers as a group value.
func (r redactedHeaders) LogValue() slog.Value {
	attrs := make([]slog.Attr, 0, len(r.header))
	for _, name := range slices.Sorted(maps.Keys(r.header)) {
		values := r.header[name]
		value := redacted
		if !isCredential(name, values, r.apiKey) {
			value = strings.Join(values, ", ")
		}
		attrs = append(attrs, slog.String(name, value))
	}
	return slog.GroupValue(attrs...)
}
