//go:build !go1.28 && (amd64 || arm64)

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

// Package codec is the SDK's JSON layer: the only package of the module that
// imports github.com/bytedance/sonic, and the only one that uses unsafe (one
// zero-copy bytes-to-string bridge, [NoCopyString]).
//
// It builds only where sonic's JIT path does, Go 1.17 to 1.27 on amd64 and
// arm64; everywhere else the package is unsupported.go alone and fails to
// compile on purpose (docs/support.md). The seam test in seam_test.go keeps
// sonic, unsafe and every JSON library out of the other packages and checks
// the two build constraints file by file.
//
// The package holds the request body's scratch pool ([Body]), the request
// encoder that writes a state or a body member into it ([EncodeState],
// [EncodeValue], [AppendRawState], [AppendRawValue]), the response decoder
// ([DecodeSystemOne], [DecodeModels]: one ast.Preorder traversal that
// validates every token, a trailing-data check, and a lazy second pass for
// structured legends), the lenient reader of unsuccessful responses'
// bodies ([ReadErrorBody]), the per-string check the decoder runs
// ([ValidString]) and the encoder's [Pretouch] hook. Decoded values are the
// std-only types of internal/wire, which the root package wraps.
package codec
