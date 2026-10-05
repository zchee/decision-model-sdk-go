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

//go:build live

// Package livetest holds the adapter's opt-in tests against the billed
// LLM provider APIs and the System One API: the live halves of the
// replay tests of package adapter, which drive the same fixtures through
// the recorded exchanges instead.
//
// The tests compile only with the build tag live, and each of them skips
// itself, before any call, unless ADAPTER_LIVE_TESTS=1 and its
// provider's credential variable is set: OPENAI_API_KEY for the OpenAI
// cases, ANTHROPIC_API_KEY for the Anthropic cases, GOOGLE_API_KEY or
// GEMINI_API_KEY for the Gemini cases, and both DECISION_MODEL_API_KEY
// and DECISION_MODEL_BASE_URL for the System One reference test. The
// tests read only whether a variable is set; every credential value is
// read by the providers and the SDK themselves, and nothing here prints
// one. A typical run:
//
//	ADAPTER_LIVE_TESTS=1 OPENAI_API_KEY=... ANTHROPIC_API_KEY=... \
//		GEMINI_API_KEY=... DECISION_MODEL_API_KEY=... \
//		DECISION_MODEL_BASE_URL=https://api.typesafe.ai \
//		go test -tags live -count=1 -v ./livetest/
//
// With ADAPTER_LIVE_RECORD=1 also set, each test records its provider
// exchanges through the recorder of internal/cassette and writes
// testdata/cassettes-go/<test id>.json, one file per upstream test id,
// in the committed cassette format. A recording never touches
// testdata/cassettes/ or testdata/expected/, the committed upstream
// files; the recorder refuses those directories.
//
// CI compiles and lists these tests without any credential; nothing
// live runs there.
package livetest
