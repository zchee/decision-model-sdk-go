//go:build !(linux || darwin)

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

package testsupport

import (
	"runtime"
	"testing"
)

func newGuard(tb testing.TB, _ int) *Guard {
	tb.Helper()
	tb.Skipf("guard pages need mmap and mprotect, which the syscall package offers on Linux and Darwin only, not on %s", runtime.GOOS)
	return nil
}
