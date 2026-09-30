// Copyright 2026 The typesafe-sdk-go Authors.
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

// Package adapter answers the TypeSafe System One API with an LLM: a port of
// system-one-adapter-python v0.2.1, used through typesafe.WithRoundTripper.
//
// Upstream's purpose is this module's: comparing TypeSafe against an LLM on
// cost, speed and intelligence. The module is not released yet;
// docs/port-test-matrix.md tracks which upstream tests are ported, and
// docs/deviations.md lists where the port behaves differently.
package adapter
