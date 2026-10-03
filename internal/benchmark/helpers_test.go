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

package benchmark

import (
	"net/http"
	"strings"
	"testing"

	decision "github.com/zchee/decision-model-sdk-go"
)

// testKey is the benchmarks' API key; the Recorder and the loopback server
// never check it.
const testKey = "test-key"

// testBaseURL and testModel are the base URL and the model the benchmarks'
// clients name: TypeSafe AI's API and model, as the root tests name them.
// The SDK has no default for either.
const (
	testBaseURL = "https://api.typesafe.ai"
	testModel   = "jev-latest"
)

// mustPrepared prepares qs, failing tb when Prepare fails.
func mustPrepared(tb testing.TB, qs *decision.Questions) *decision.Prepared {
	tb.Helper()
	p, err := qs.Prepare()
	if err != nil {
		tb.Fatalf("Prepare: %v", err)
	}
	return p
}

// q3Questions is the whole call's question set: the three questions of the
// upstream round-trip test (tests/test_clients.py:60-80), which result.json
// answers. The root package's tests build the same set.
func q3Questions(tb testing.TB) *decision.Prepared {
	tb.Helper()
	return mustPrepared(tb, decision.NewQuestions().
		Noul("spam", decision.Noul{Instructions: decision.Text("Spam?")}).
		Choice("tone", decision.Choice{Instructions: decision.Text("Tone?"), Options: decision.Options{{Label: "friendly"}, {Label: "hostile"}}}).
		Score("quality", decision.Score{Instructions: decision.Text("Quality?"), Levels: []decision.Content{decision.Text("bad"), decision.Text("ok"), decision.Text("great")}}))
}

// newCallState returns the whole call's state: 1 KiB of text once encoded,
// boxed in an any before the call.
func newCallState() any { return strings.Repeat("s", 1<<10-2) }

// newBenchClient builds a client over rt with every setting an option
// gives, so that the environment cannot change the request, and closes it
// when tb ends.
func newBenchClient(tb testing.TB, rt http.RoundTripper, opts ...decision.ClientOption) *decision.Client {
	tb.Helper()
	c, err := decision.NewClient(append([]decision.ClientOption{decision.WithRoundTripper(rt), decision.WithAPIKey(testKey), decision.WithBaseURL(testBaseURL), decision.WithModel(testModel)}, opts...)...)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = c.Close() })
	return c
}
