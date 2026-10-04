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
	"github.com/zchee/decision-model-sdk-go/adapter/gemini"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// geminiPreset is the factory of the provider name "gemini": gemini.New
// with model and no option, as upstream's build_sync_provider builds a
// GeminiProvider (providers/__init__.py:50-55). gemini.New reads
// GOOGLE_API_KEY, else GEMINI_API_KEY, and GOOGLE_GEMINI_BASE_URL when it
// is called. It returns a nil interface with New's error, never a nil
// *gemini.Provider, which the Adapter would cache as a provider.
func geminiPreset(model string) (llm.Provider, error) {
	p, err := gemini.New(model)
	if err != nil {
		return nil, err
	}
	return p, nil
}
