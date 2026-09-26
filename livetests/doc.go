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

// Package livetests holds the SDK's tests against the live TypeSafe API,
// the port of the Python SDK's tests/test_integration.py. The API is billed
// per call, so the tests are opt-in twice over: they compile only with the
// build tag live, and each of them fails at once, before it calls the API,
// unless the environment sets TYPESAFE_LIVE_TESTS=1 and TYPESAFE_API_KEY:
//
//	TYPESAFE_LIVE_TESTS=1 TYPESAFE_API_KEY=... go test -tags live -count=1 -v ./livetests/
//
// TYPESAFE_BASE_URL, when set, selects another API host, as it does for any
// client. The key is read from the environment by the SDK itself; no test
// prints it, and no command line needs it. Each test checks the environment
// inside the test, never in TestMain, so go test -list -tags live lists the
// tests without the variables. CI does not run them.
//
// With -args -record the tests also write the bodies the API returned to
// testdata/live, after removing every credential from them; a body that
// still holds anything shaped like a credential is refused, not written.
// The untagged tests of this package check that guard and that scrubber on
// every go test run, and run the programs under examples/ against a local
// stand-in for the API (TestExamplesOffline); TestExamples, tagged, runs
// them against the API.
package livetests
