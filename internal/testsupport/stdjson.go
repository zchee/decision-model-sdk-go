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

package testsupport

import "encoding/json"

// StdlibMarshal returns encoding/json's Marshal of v, and StdlibUnmarshal
// is its Unmarshal. The root package imports no JSON library, its tests
// included (the seam test); through these its tests check how a caller's
// encoding/json treats the SDK's types, which it finds json.Marshaler and
// json.Unmarshaler on by method set.
func StdlibMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// StdlibUnmarshal returns encoding/json's Unmarshal of data into v; see
// [StdlibMarshal].
func StdlibUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
