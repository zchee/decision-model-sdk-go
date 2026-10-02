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

// Package jsonx is the adapter's one user of the JSON library
// github.com/go-json-experiment/json. It holds:
//
//   - the two float spellings upstream writes: PydanticFloat, pydantic-core's
//     to_json spelling, used in every text that goes into a prompt, and
//     ReprFloat, Python's repr spelling, used in the response body;
//   - the re-encoding of a state into its prompt text, PydanticJSON and
//     EncodeState, which equal pydantic_core.to_json(json.loads(text)) and
//     upstream's replacement of '<' and '>';
//   - an ordered writer, Value and Marshal, that writes members in the order
//     they are given;
//   - semantic equality of two JSON texts for tests, Equal and EqualOrdered.
//
// No other package of the module imports the JSON library, so a change of
// its API is an edit of this package alone.
package jsonx
