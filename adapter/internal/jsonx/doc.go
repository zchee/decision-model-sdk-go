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

// Package jsonx is the adapter's one user of the standard library's v2 JSON
// packages; it reads and writes JSON with encoding/json/jsontext. It holds:
//
//   - the two float spellings upstream writes: PydanticFloat, pydantic-core's
//     to_json spelling, used in every text that goes into a prompt, and
//     ReprFloat, Python's repr spelling, used in the response body;
//   - the re-encoding of a state into its prompt text, PydanticJSON and
//     EncodeState, which equal pydantic_core.to_json(json.loads(text)) and
//     upstream's replacement of '<' and '>';
//   - an ordered writer, Value and Marshal, that writes members in the order
//     they are given;
//   - a reader, Read and Node, that gives a JSON text's values as Python's
//     json.loads holds them, for code that looks members up;
//   - a token reader, Tokens, that gives every token of a JSON text with its
//     depth, for code that must see what Read drops: a member that a later
//     one of the same name replaces, and each number as the text spells it;
//   - semantic equality of two JSON texts for tests, Equal and EqualOrdered.
//
// No other package of the module imports those packages, so a change of
// their API is an edit of this package alone.
package jsonx
