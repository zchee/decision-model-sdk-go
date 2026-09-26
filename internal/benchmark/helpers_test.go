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

package benchmark

import (
	"net/http"
	"strings"
	"testing"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

// testKey is the benchmarks' API key; the Recorder and the loopback server
// never check it.
const testKey = "test-key"

// mustPrepared prepares qs, failing tb when Prepare fails.
func mustPrepared(tb testing.TB, qs *typesafe.Questions) *typesafe.Prepared {
	tb.Helper()
	p, err := qs.Prepare()
	if err != nil {
		tb.Fatalf("Prepare: %v", err)
	}
	return p
}

// q3Questions is the NF3 question set: the three questions of the upstream
// round-trip test (tests/test_clients.py:60-80), which result.json answers.
// The root package's tests build the same set.
func q3Questions(tb testing.TB) *typesafe.Prepared {
	tb.Helper()
	return mustPrepared(tb, typesafe.NewQuestions().
		Noul("spam", typesafe.Noul{Instructions: typesafe.Text("Spam?")}).
		Choice("tone", typesafe.Choice{Instructions: typesafe.Text("Tone?"), Options: typesafe.Options{{Label: "friendly"}, {Label: "hostile"}}}).
		Score("quality", typesafe.Score{Instructions: typesafe.Text("Quality?"), Levels: []typesafe.Content{typesafe.Text("bad"), typesafe.Text("ok"), typesafe.Text("great")}}))
}

// newCallState returns the NF3 state: 1 KiB of text once encoded, boxed in
// an any before the call.
func newCallState() any { return strings.Repeat("s", 1<<10-2) }

// newBenchClient builds a client over rt with every setting an option
// gives, so that the environment cannot change the request, and closes it
// when tb ends.
func newBenchClient(tb testing.TB, rt http.RoundTripper, opts ...typesafe.ClientOption) *typesafe.Client {
	tb.Helper()
	c, err := typesafe.NewClient(append([]typesafe.ClientOption{typesafe.WithRoundTripper(rt), typesafe.WithAPIKey(testKey), typesafe.WithBaseURL(typesafe.DefaultBaseURL), typesafe.WithModel(typesafe.DefaultModel)}, opts...)...)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = c.Close() })
	return c
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
