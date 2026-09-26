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

// Package engine holds the stages of a call that the root package's public
// types are built on: the escape and cut of text the SDK did not write, the
// credential scrub of a transport error, header redaction, the falsiness
// check of a question's JSON value, the response body's read, and the API's
// paths and retry-count header. It imports neither the root package nor anything that imports it,
// and no file of it imports unsafe (STANDING 3, ruling D-W6.5-design 6.2).
// They live here, not in the root package, so that a test outside the root
// package can measure them (owner instruction G9, W6.5 design D1).
package engine
