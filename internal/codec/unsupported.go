//go:build go1.28 || !(amd64 || arm64)

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

// This file is the SDK's deliberate compile error (decision D1 of the port
// plan). The SDK's only JSON codec is github.com/bytedance/sonic, whose JIT
// path compiles only for Go 1.17 to 1.27 on amd64 and arm64; anywhere else
// sonic would fall back to encoding/json and print a warning at init, so the
// SDK refuses to compile there instead of running on a codec it was never
// measured with.
//
// Off that matrix this is the only file of the package (every other file,
// tests included, carries the complementary constraint
// "!go1.28 && (amd64 || arm64)"), so go build and go vet both stop at the
// identifier below and print its name, which is the error message:
//
//	undefined: typesafe_sdk_go_requires_go1_17_to_go1_27_on_amd64_or_arm64
//
// On the Go 1.28 release both constraints move together (pre-mortem PM5): see
// the bump procedure in docs/support.md.

package codec

var _ = typesafe_sdk_go_requires_go1_17_to_go1_27_on_amd64_or_arm64
