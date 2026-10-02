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

package adapter

import (
	"errors"

	decision "github.com/zchee/decision-model-sdk-go"
)

// PlaceholderAPIKey is the API key NewClient gives the SDK. It is not a
// credential: the Adapter never reads the Authorization header that carries
// it, nor forwards it. Its value is a string that no Adapter text contains,
// because the SDK replaces the cause of a transport error whose text holds
// the call's key.
const PlaceholderAPIKey = "ts-adapter-placeholder-7f3c9a1e" //nolint:gosec // G101: a fixed placeholder, not a credential; the Adapter never forwards it.

// maxResponseBytes is the response size limit NewClient sets, the SDK's
// largest: the debug data repeats the state twice per attempt, and the body
// is built in the same process, so the limit protects nothing here.
const maxResponseBytes = 1 << 30

// placeholderBaseURL is the base URL NewClient gives the SDK, which has no
// default. Nothing is dialled: ad is the client's transport and answers
// every request by its path, whatever the host, and a name under .invalid
// never resolves (RFC 2606).
const placeholderBaseURL = "http://adapter.invalid"

// NewClient returns a root SDK client whose requests ad answers. It applies,
// before opts: WithAPIKey(PlaceholderAPIKey), WithBaseURL with a placeholder
// URL that is never dialled, WithRetry(decision.NoRetry()), WithNoTimeout(),
// WithMaxResponseBytes(1<<30) and always WithModel: ad's default model, or
// a name ad reads as no model when ad has none, so that neither
// DECISION_MODEL_BASE_URL nor DECISION_MODEL_DEFAULT_MODEL in the
// environment chooses the URL or the model. opts may override each of
// these. After opts it applies WithRoundTripper(ad):
// ad is the client's transport, and a WithRoundTripper among opts has no
// effect. The SDK's transport options, which cannot be combined with a
// round tripper (WithHTTPTransport, WithHTTPVersion, WithRootCAs,
// WithTLSConfig, WithProxy, WithConnectTimeout and WithCompression), make
// NewClient fail with the SDK's *decision.ConfigError.
//
// Closing the client closes ad. An Adapter belongs to one client: NewClient
// fails when ad was already given to one. NewClient reserves ad with a
// compare-and-swap before it calls decision.NewClient and releases it when
// that call fails, so two concurrent NewClient calls cannot both take ad and
// a failed NewClient leaves ad free for another NewClient call. Giving ad to
// decision.WithRoundTripper directly bypasses this check; the caller then
// owns that client's Close, which closes ad.
//
// The SDK does not retry under these settings, and an Adapter retries each
// provider request by its own policy. On a client of the caller's own that
// retries (the SDK's default policy), the SDK runs the whole evaluation
// again after a provider status of 408, 429 or 500 to 599, a timeout or a
// connection failure, and each run is recorded as Debug.SDKRetryCount.
func NewClient(ad *Adapter, opts ...decision.ClientOption) (*decision.Client, error) {
	if ad == nil {
		return nil, errors.New("adapter: NewClient: the Adapter is nil")
	}
	if !ad.given.CompareAndSwap(false, true) {
		return nil, errors.New("adapter: NewClient: the Adapter already belongs to a client")
	}
	model := ad.defaultModel
	if model == "" {
		model = noModel
	}
	all := append([]decision.ClientOption{
		decision.WithAPIKey(PlaceholderAPIKey),
		decision.WithBaseURL(placeholderBaseURL),
		decision.WithRetry(decision.NoRetry()),
		decision.WithNoTimeout(),
		decision.WithMaxResponseBytes(maxResponseBytes),
		decision.WithModel(model),
	}, opts...)
	c, err := decision.NewClient(append(all, decision.WithRoundTripper(ad))...)
	if err != nil {
		ad.given.Store(false)
		return nil, err
	}
	return c, nil
}
