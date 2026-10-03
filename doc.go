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

// Package decision is a Go client for decision models served through the
// System One API, which TypeSafe AI (Jev) offered first and other vendors
// serve too; it is a port of TypeSafe AI's typesafe-sdk-python 0.7.1.
//
// A [Client] asks questions about a state and returns the answers: a
// question set built with [NewQuestions] and sent by [Client.SystemOne],
// or one declared by a struct's tags and asked by [Ask]. The repository's
// examples directory holds complete programs, docs/deviations.md lists
// where the port behaves differently from the Python SDK, and
// docs/support.md the supported Go releases and platforms.
package decision
