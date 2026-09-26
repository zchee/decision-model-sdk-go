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

//go:build live

package livetests

import (
	"os"
	"testing"
)

// TestExamples runs every program under examples/ against the live API
// (XD1: the README's and docs' Go blocks are those programs, and they call
// the API, as upstream's test_markdown runs its Markdown examples live).
// The programs read TYPESAFE_API_KEY and TYPESAFE_BASE_URL from the
// environment they inherit; the key is on no command line, and output that
// held it would fail the test with the key shown as ***. The programs make
// about 13 billed System One calls in all.
func TestExamples(t *testing.T) {
	env := requireLive(t)
	checkExamples(t, os.Environ(), []string{env.apiKey})
}
