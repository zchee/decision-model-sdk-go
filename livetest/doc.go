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

// Package livetest holds the SDK's tests against a live System One API,
// the port of the Python SDK's tests/test_integration.py. The API is billed
// per call, so the tests are opt-in twice over: they compile only with the
// build tag live, and each of them fails at once, before it calls the API,
// unless the environment sets DECISION_MODEL_LIVE_TESTS=1 and names the
// vendor: DECISION_MODEL_API_KEY, DECISION_MODEL_BASE_URL and
// DECISION_MODEL_DEFAULT_MODEL. The tests name no vendor's API or model in
// code. For TypeSafe AI's API:
//
//	DECISION_MODEL_LIVE_TESTS=1 DECISION_MODEL_API_KEY=... \
//		DECISION_MODEL_BASE_URL=https://api.typesafe.ai \
//		DECISION_MODEL_DEFAULT_MODEL=jev-latest \
//		go test -tags live -count=1 -v ./livetest/
//
// The client is built from those variables as a caller's would be. The key
// is read from the environment by the SDK itself; no test prints it, and no
// command line needs it. Each test checks the environment
// inside the test, never in TestMain, so go test -list -tags live lists the
// tests without the variables. CI does not run them.
//
// TestLiveProviders calls the vendors that serve the API through
// decision.WithProvider instead, and reads its own variables:
// DECISION_MODEL_LIVE_TESTS=1, DECISION_MODEL_LIVE_PROVIDERS (a
// comma-separated list of typesafe, codiv, perplexity, decisions-api and
// openai), and for each listed provider its own key variable and
// DECISION_MODEL_LIVE_MODEL_<NAME> (the name in upper case, "-" written
// "_"), which a provider with a default model may leave unset. It needs none
// of the three generic variables, so it runs alone, with
// -run '^TestLiveProviders$'.
//
// With -args -record the tests also write the bodies the API returned to
// testdata/live, and TestLiveProviders to testdata/live/providers/<name>,
// after removing every credential from them; a body that still holds
// anything shaped like a credential is refused, not written.
// The untagged tests of this package check that guard and that scrubber on
// every go test run, and run the programs under examples/ against a local
// stand-in for the API (TestExamplesOffline); TestExamples, tagged, runs
// them against the API.
package livetest
