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

import "unsafe"

// NoCopyString returns b viewed as a string, without copying it. It is the
// module's one use of unsafe (NF6): the decoder hands a response body to
// sonic's parser as a string, and sonic returns keys and strings without
// escape sequences as substrings of it.
//
// The string aliases b. The aliasing contract is that b must not be modified
// for as long as the string, or any substring of it, is reachable: a write to
// b would change a string Go treats as immutable. A response body satisfies
// this, since the SDK never writes to it after the read; a pooled buffer that
// is reused does not, so a string that must outlive its buffer is copied (the
// decoder's interning either returns the request's own string or copies).
func NoCopyString(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}
