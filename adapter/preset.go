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

package adapter

import (
	"github.com/zchee/decision-model-sdk-go/adapter/anthropic"
	"github.com/zchee/decision-model-sdk-go/adapter/gemini"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
	"github.com/zchee/decision-model-sdk-go/adapter/openai"
)

// The preset factories of the provider names "anthropic", "gemini" and
// "openai": the New of this module's package of that name, which reads the
// provider's environment variables when it is built, as upstream's
// build_sync_provider builds each provider from its name alone
// (providers/__init__.py:77-82).
var (
	anthropicPreset = preset(anthropic.New)
	geminiPreset    = preset(gemini.New)
	openaiPreset    = preset(openai.New)
)

// preset returns the factory of a preset provider name: newProvider called
// with model and no option, so that it reads the provider's environment
// variables when it is built. When newProvider fails the factory returns a
// nil interface and the error, never a nil *Provider, which the Adapter
// would keep as a provider.
func preset[P llm.Provider, O any](newProvider func(model string, opts ...O) (P, error)) llm.Factory {
	return func(model string) (llm.Provider, error) {
		p, err := newProvider(model)
		if err != nil {
			return nil, err
		}
		return p, nil
	}
}
