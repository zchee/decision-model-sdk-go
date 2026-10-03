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
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// anthropicPreset is the preset factory "anthropic": the Provider of package
// anthropic for model, built by anthropic.New with no option, which reads
// ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN and ANTHROPIC_BASE_URL when it is
// built. When New fails it returns no provider and New's error, never a nil
// *anthropic.Provider, which the Adapter would keep as a provider.
func anthropicPreset(model string) (llm.Provider, error) {
	p, err := anthropic.New(model)
	if err != nil {
		return nil, err
	}
	return p, nil
}
