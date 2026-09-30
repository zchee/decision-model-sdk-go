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

package adapter

import "testing"

// FuzzCanary is a deliberate, trivial fuzz target. It shows that the
// repository's fuzz-target scan reaches this module and that the scan's
// exclusion of adapter/ stops it; it is removed once that is shown.
func FuzzCanary(f *testing.F) {
	f.Add([]byte("canary"))
	f.Fuzz(func(_ *testing.T, _ []byte) {})
}
