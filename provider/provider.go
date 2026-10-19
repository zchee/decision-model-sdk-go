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

// Package provider holds a [decision.Provider] for each vendor whose System
// One endpoint the SDK has been checked against, to pass to
// [decision.WithProvider]:
//
//	client, err := decision.NewClient(decision.WithProvider(provider.Codiv()))
//
// Each preset names the vendor's base URL, its paths and the environment
// variable its key is read from. A vendor-named variable is read only by
// its own preset, so a key kept for one vendor is sent to that vendor's host
// alone; with a preset, the generic DECISION_MODEL_ variables are not read.
// Only [OpenAI] carries a default model; with any other preset, name one
// with [decision.WithModel] or with [decision.Model] on each call.
//
// [TypeSafe], [Codiv], [Perplexity], [OpenRouter] and [OpenRouterDecisions]
// speak System One. [DecisionsAPI] and [OpenAI] do not, and each carries a
// [decision.Dialect] that rewrites every call between the SDK's wire and the
// vendor's, so that a call reads the same on every preset; each one's
// documentation says what its wire cannot express.
//
// The vendors differ in what they do with a top-level request member they do
// not know, such as one [decision.ExtraBody] adds: each preset's
// documentation says which.
//
// This "provider" is a vendor of the System One API that the client calls;
// the adapter module's providers are the language models its local stand-in
// for the API drives, a different thing.
package provider

import (
	decision "github.com/zchee/decision-model-sdk-go"
)

// TypeSafe returns the provider of TypeSafe AI's API, which offered System
// One first: https://api.typesafe.ai, POST /v1/systemone and GET /v1/models,
// with the key read from TYPESAFE_API_KEY.
//
// Its listing named the models jev-latest and jev-preview. It refuses a
// request with a top-level member it does not know, such as one
// [decision.ExtraBody] adds, with status 400.
func TypeSafe() decision.Provider {
	return decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
		Name:          "TypeSafe AI",
		BaseURL:       "https://api.typesafe.ai",
		SystemOnePath: "/v1/systemone",
		ModelsPath:    "/v1/models",
		APIKeyEnv:     "TYPESAFE_API_KEY",
	}
}

// Codiv returns the provider of Codiv's API: https://api.codiv.ai, POST
// /v1/systemone and GET /v1/models, with the key read from CODIV_API_KEY.
//
// Its listing named the models openjev-latest, openjev-0.1,
// diffusiongemma-26b, laya-1.0, verdict-1.4, clm-v0.1 and jevk5-0.2; the
// listing may hold models its System One endpoint does not serve. It
// ignores a top-level member it does not know, such as one
// [decision.ExtraBody] adds, and answers as if the member were absent.
func Codiv() decision.Provider {
	return decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
		Name:          "Codiv",
		BaseURL:       "https://api.codiv.ai",
		SystemOnePath: "/v1/systemone",
		ModelsPath:    "/v1/models",
		APIKeyEnv:     "CODIV_API_KEY",
	}
}

// Perplexity returns the provider of Perplexity's decisions API:
// https://api.perplexity.ai, POST /v1/decisions, with the key read from
// PERPLEXITY_API_KEY. It takes the System One request and answers in the
// System One response shape.
//
// It has no listing in the System One shape (its GET /v1/models lists
// another product's models), so ModelsPath is empty and
// [decision.Models.List] on its client fails with a [*decision.ConfigError]
// before anything is sent. Its decision model is pplx-decider-v1.1-27b. It
// refuses a request with a top-level member it does not know, such as one
// [decision.ExtraBody] adds, with status 400.
func Perplexity() decision.Provider {
	return decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
		Name:          "Perplexity",
		BaseURL:       "https://api.perplexity.ai",
		SystemOnePath: "/v1/decisions",
		APIKeyEnv:     "PERPLEXITY_API_KEY",
	}
}

// OpenRouter returns the provider of OpenRouter's System One endpoint:
// https://openrouter.ai, POST /api/v1/systemone, with the key read from
// OPENROUTER_API_KEY, which [OpenRouterDecisions] reads too. It takes the
// System One request and answers in the System One response shape, with the
// members id, provider and usage.cost besides, which the SDK ignores.
//
// OpenRouter names a System One model with its vendor: typesafe/jev-1.13.
// The bare id jev-1.13 was accepted too and answered under the namespaced
// one, the response's model being typesafe/jev-1.13-20260917 for either;
// the vendor's reference page states that mapping for every System One id.
//
// It has no listing in the System One shape (its GET /api/v1/models lists
// OpenRouter's whole catalogue of chat models, in a shape of its own), so
// ModelsPath is empty and [decision.Models.List] on its client fails with a
// [*decision.ConfigError] before anything is sent.
//
// It sends no x-request-id or x-typesafe-request-id header. A success
// carries x-generation-id, equal to the body's id, and x-provider-name
// instead, which the SDK does not read, so Meta().RequestID() and
// [decision.APIError.RequestID] report none. Its error body is
// {"error":{"message","code"}}: [decision.APIError.Message] holds the
// message, and [decision.APIError.ErrorType] is empty: its error bodies,
// 400 and 401 among those recorded, carry no error_type.
//
// It ignores a top-level request member it does not know, such as one
// [decision.ExtraBody] adds, and answers as if the member were absent. It
// refuses, with status 400, a noul question whose criteria describe only the
// true outcome (the error names criteria.false as required); a noul with no
// criteria is accepted.
func OpenRouter() decision.Provider {
	return decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
		Name:          "OpenRouter",
		BaseURL:       "https://openrouter.ai",
		SystemOnePath: "/api/v1/systemone",
		APIKeyEnv:     "OPENROUTER_API_KEY",
	}
}

// OpenRouterDecisions returns the provider of OpenRouter's alpha decisions
// endpoint: https://openrouter.ai, POST /api/alpha/decisions, with the key
// read from OPENROUTER_API_KEY, the variable [OpenRouter] reads, so one key
// serves both. The path has no /v1, and the vendor marks the endpoint alpha:
// /api/v1/alpha/decisions and /api/v1/api/alpha/decisions both answer 404.
//
// It takes the same request and gives the same answers, request id, listing
// (none), error body, treatment of an unknown member and refusal of a
// true-only noul as [OpenRouter]; its documentation has the details, among
// them the namespaced model ids such as typesafe/jev-1.13.
func OpenRouterDecisions() decision.Provider {
	return decision.Provider{ //nolint:gosec // G101: APIKeyEnv holds the name of a variable, not a key.
		Name:          "OpenRouter Decisions",
		BaseURL:       "https://openrouter.ai",
		SystemOnePath: "/api/alpha/decisions",
		APIKeyEnv:     "OPENROUTER_API_KEY",
	}
}
