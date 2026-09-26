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

package codec

// validUTF8 reports whether b is valid UTF-8: the check of ruling R48 over
// an encoded state and every other member of a request body, which
// EncodeState and EncodeValue make before a body leaves the process. On
// arm64 it is utf8.Valid (validUTF8Portable): W5.3's probe measured
// sonic's arm64 validator 15 to 22 times slower than utf8.Valid on ASCII
// (ledger W5.3-80), so the owner's per-architecture choice (ruling G8-b on
// R54) keeps Go's here and takes sonic's on amd64 (validate_amd64.go).
func validUTF8(b []byte) bool { return validUTF8Portable(b) }
