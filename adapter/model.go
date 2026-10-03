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
	"errors"
	"strconv"
	"strings"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// The texts of upstream's ValueErrors for a call without a model or a
// provider (src/system_one_adapter/_client.py:396-405 and
// src/system_one_adapter/providers/__init__.py:32-33), byte for byte.
const (
	modelRequiredText   = "An LLM model is required on the client or call."
	missingProviderText = "A provider is required: set provider='openai', 'anthropic', or 'gemini', or pass a provider instance as the model."
	// unknownProviderBefore and unknownProviderAfter surround repr() of the
	// provider name in _UNKNOWN_PROVIDER.
	unknownProviderBefore = "Unknown provider "
	unknownProviderAfter  = ". Use 'openai', 'anthropic', or 'gemini', or pass a provider instance as the model."
)

// noModel is a model string that names no model: resolve reads it as an
// empty model before it reads anything else, so a call that sends it uses
// the default model, or is refused with upstream's model_required text
// when there is none. Its provider name before the colon is empty, so
// the model-string rules never read it as a model: New refuses it as a
// default model, and no factory has an empty name.
const noModel = ":no-model"

// presets holds the factories an Adapter has without WithFactory, by
// provider name: "openai", "anthropic" and "gemini" build the Provider of
// this module's package of that name with New(model) and no option, which
// reads the provider's environment variables when it is built. WithFactory
// adds to them or replaces one for one Adapter.
var presets = map[string]llm.Factory{
	"anthropic": anthropicPreset,
	"openai":    openaiPreset,
}

// ModelID returns the model string that selects model on provider:
// provider + ":" + model. The provider is the name before the first colon,
// so model may hold colons itself.
func ModelID(provider, model string) string { return provider + ":" + model }

// target is what a call's model string resolves to: the provider's name and
// the model name, and the provider itself when it is borrowed.
type target struct {
	// name is the WithProvider name of a borrowed provider, or the factory
	// name of an owned one.
	name string
	// model is the model name: the borrowed provider's own, or the one the
	// owned provider is built for.
	model string
	// borrowed is the WithProvider provider, nil for an owned one.
	borrowed llm.Provider
}

// refusal is a request the Adapter refuses before an evaluation starts:
// the status, the error_type and the detail message of its error body.
type refusal struct {
	status    int
	errorType string
	message   string
}

func (r *refusal) Error() string { return r.message }

// resolve returns the provider and the model a call whose model string is
// m uses, by the rules below, in this order, as upstream's _resolve_provider
// takes the call's model, else the client's, and the call's provider, else
// the client's (_client.py:388-409):
//
//   - An empty m is the default model; without one the call is refused
//     with upstream's model_required text.
//   - A WithProvider name is that borrowed provider and its own model.
//   - "<name>:<model>" with a factory for name and a model that is not
//     empty, split at the first colon, is the owned provider for (name,
//     model).
//   - Any other m is a model name for the provider the default model names
//     as "<name>:<model>"; without such a default (none, or a WithProvider
//     name) the call is refused with upstream's provider_required text.
//
// So a prefix without a factory is part of a model name, and upstream's
// Unknown-provider text appears only as an error of New.
func (ad *Adapter) resolve(m string) (target, error) {
	if m == "" || m == noModel {
		if ad.defaultModel == "" {
			return target{}, &refusal{status: 400, errorType: "model_required", message: modelRequiredText}
		}
		m = ad.defaultModel
	}
	if p, ok := ad.providers[m]; ok {
		return target{name: m, model: p.Model(), borrowed: p}, nil
	}
	if name, model, ok := ad.factoryModel(m); ok {
		return target{name: name, model: model}, nil
	}
	if ad.defaultName == "" {
		return target{}, &refusal{status: 400, errorType: "provider_required", message: missingProviderText}
	}
	return target{name: ad.defaultName, model: m}, nil
}

// factoryModel splits m into a provider name that has a factory and a model
// name that is not empty, at the first colon.
func (ad *Adapter) factoryModel(m string) (name, model string, ok bool) {
	name, model, found := strings.Cut(m, ":")
	if !found || model == "" || ad.factories[name] == nil {
		return "", "", false
	}
	return name, model, true
}

// checkDefault returns the provider name the default model id names in
// the form "<name>:<model>", "" for a WithProvider name, or the error New
// returns for an id that neither form resolves: upstream's Unknown-provider
// text, byte for byte with the name as Python's repr writes it, for a name
// without a factory (providers/__init__.py:33,83), and its missing-provider
// text for an id without a provider name.
func (ad *Adapter) checkDefault() (string, error) {
	id := ad.defaultModel
	if _, ok := ad.providers[id]; ok || id == "" {
		return "", nil
	}
	name, model, found := strings.Cut(id, ":")
	switch {
	case !found || name == "":
		return "", errors.New(missingProviderText) //nolint:staticcheck // ST1005: upstream's ValueError text, byte for byte.
	case ad.factories[name] == nil:
		return "", errors.New(unknownProviderBefore + string(appendPyStrRepr(nil, name)) + unknownProviderAfter)
	case model == "":
		return "", errors.New("adapter: the default model " + strconv.Quote(id) + " names no model after its provider")
	}
	return name, nil
}
