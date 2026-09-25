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

package typesafe

import (
	"runtime"
	"strings"
)

// Version is the version of this SDK. Every request names it in the
// User-Agent and X-TypeSafe-SDK headers as typesafe-sdk-go/<Version>.
const Version = "0.1.0-dev"

// sdkIdentifier is what the SDK calls itself in User-Agent and
// X-TypeSafe-SDK. typesafe-sdk-python sends typesafe-sdk/<version> in both
// (py:_core/transport.py:123-124); this port names itself instead, so the API
// never mistakes it for the SDK it is a port of.
const sdkIdentifier = "typesafe-sdk-go/" + Version

// runtimeIdentifier is the value of X-TypeSafe-Runtime: the Go release, the
// operating system and the architecture the program was built for, as
// typesafe-sdk-python sends python/<version> (<platform>; <machine>)
// (py:_core/transport.py:36).
var runtimeIdentifier = runtimeHeaderValue(runtime.Version(), runtime.GOOS, runtime.GOARCH)

// runtimeHeaderValue formats X-TypeSafe-Runtime as go/<release> (<goos>;
// <goarch>), where release is version, a [runtime.Version] string, without
// its leading "go": "go1.27.1" becomes "1.27.1", and a toolchain built with
// experiments keeps their suffix ("1.27.1-X:simd"). A development toolchain's
// "devel ..." string has no such prefix and is kept whole.
func runtimeHeaderValue(version, goos, goarch string) string {
	release, _ := strings.CutPrefix(version, "go")
	return "go/" + release + " (" + goos + "; " + goarch + ")"
}
