//go:build !go1.28 && (amd64 || arm64)

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

package codec

import sonicutf8 "github.com/bytedance/sonic/utf8"

// validUTF8 reports whether b is valid UTF-8, the check that EncodeState and
// EncodeValue make before a body leaves the process. On amd64 it is sonic's
// SIMD validator over the whole input, faster than utf8.Valid there
// (validate.go says by how much). TestValidUTF8Parity and FuzzValidUTF8 hold
// it to validUTF8Portable, utf8.Valid itself, on every string of up to three
// bytes and around sonic's 32-byte blocks.
func validUTF8(b []byte) bool { return sonicutf8.Validate(b) }
