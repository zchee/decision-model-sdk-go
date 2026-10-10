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
	"strings"
	"unicode/utf8"
)

// Provider names one vendor's System One endpoint: where it is served, the
// paths it answers on, the environment variable that holds its key, and the
// model a call names when neither the call nor the client names one. Pass it
// with [WithProvider]; the provider package holds a Provider for each vendor
// the SDK has been checked against.
//
// A Provider is the caller naming the vendor, so its settings replace the
// generic ones: with a Provider, [APIKeyEnv], [BaseURLEnv] and
// [DefaultModelEnv] are not read at all, and the key is read from APIKeyEnv
// alone, a variable meant for that vendor's host only. That the key reaches
// one host is the caller's guarantee for a Provider literal they build:
// BaseURL and APIKeyEnv must name the same vendor.
type Provider struct {
	// Name names the provider in error messages, such as "Codiv". It must
	// not be empty.
	Name string

	// BaseURL is the vendor's API base URL, such as https://api.codiv.ai,
	// under the rules of [WithBaseURL].
	BaseURL string

	// SystemOnePath is the path of the System One endpoint under BaseURL,
	// such as /v1/systemone. It must start with "/" and carry no query or
	// fragment.
	SystemOnePath string

	// ModelsPath is the path of the list-models endpoint under BaseURL, such
	// as /v1/models, or empty when the vendor serves no listing in the
	// System One shape: [Models.List] on the client then fails with a
	// [*ConfigError] before anything is sent. A non-empty path must start
	// with "/" and carry no query or fragment.
	ModelsPath string

	// APIKeyEnv names the environment variable that holds the vendor's API
	// key, read when [WithAPIKey] is not given. It must not be empty.
	APIKeyEnv string

	// DefaultModel is the model a System One call names when neither the
	// call ([Model]) nor the client ([WithModel]) names one, or empty for
	// none. A non-empty model must not be blank and must be valid UTF-8.
	DefaultModel string
}

// WithProvider sets the vendor the client talks to: its base URL, its paths,
// the variable its key is read from and its default model ([Provider]).
//
// With a provider, the settings are taken in this order, and the generic
// variables [APIKeyEnv], [BaseURLEnv] and [DefaultModelEnv] are never read:
//
//   - The API key is [WithAPIKey]'s, else the variable p.APIKeyEnv names.
//   - The base URL is p.BaseURL. [WithBaseURL] replaces it only together
//     with WithAPIKey, for a stand-in or a gateway in front of the vendor;
//     without WithAPIKey the client is not built, so a key kept for the
//     vendor's host is never sent to another.
//   - The model is [WithModel]'s, else p.DefaultModel, else none.
//
// p is checked when the client is built: an empty Name, APIKeyEnv or
// SystemOnePath, a path that does not start with "/" or that carries a
// query or a fragment, a BaseURL that
// [WithBaseURL] would refuse, or a blank or invalid DefaultModel fails
// [NewClient] with a [*ConfigError] that repeats no value. Without
// WithProvider the client reads the generic variables and uses the paths
// /v1/systemone and /v1/models.
func WithProvider(p Provider) ClientOption {
	return func(o *options) { o.provider = &p }
}

// providerRule returns the rule p breaks as the end of a sentence, or "" when
// p can be used. It never repeats a value of p.
func providerRule(p *Provider) string {
	switch {
	case p.Name == "":
		return "has an empty Name"
	case p.APIKeyEnv == "":
		return "has an empty APIKeyEnv"
	case p.BaseURL == "":
		return "has an empty BaseURL"
	case p.SystemOnePath == "":
		return "has an empty SystemOnePath"
	case !strings.HasPrefix(p.SystemOnePath, "/"):
		return "has a SystemOnePath that does not start with '/'"
	case p.ModelsPath != "" && !strings.HasPrefix(p.ModelsPath, "/"):
		return "has a ModelsPath that does not start with '/'"
	case strings.Contains(p.SystemOnePath, "#"):
		return "has a SystemOnePath that must not carry a fragment ('#...')"
	case strings.Contains(p.SystemOnePath, "?"):
		return "has a SystemOnePath that must not carry a query ('?...')"
	case strings.Contains(p.ModelsPath, "#"):
		return "has a ModelsPath that must not carry a fragment ('#...')"
	case strings.Contains(p.ModelsPath, "?"):
		return "has a ModelsPath that must not carry a query ('?...')"
	case p.DefaultModel != "" && strings.TrimFunc(p.DefaultModel, isPythonSpace) == "":
		return "has a blank DefaultModel"
	case !utf8.ValidString(p.DefaultModel):
		return "has a DefaultModel that is not valid UTF-8"
	}
	if rule := baseURLRule(strings.TrimRight(p.BaseURL, "/")); rule != "" {
		return "has a BaseURL that " + rule
	}
	return ""
}
