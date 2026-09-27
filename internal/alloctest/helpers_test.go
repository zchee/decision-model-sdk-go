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

package alloctest

// Copies of the root package's test helpers that the allocation budgets use,
// each from the root file its comment names: a test of the root package
// cannot be imported, and the root package's own tests keep theirs. Keep
// each copy equal to its original; a copy that drifts measures or checks
// something else.

import (
	"maps"
	"net/http"
	"os"
	"testing"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

// testKey is the API key of the clients these tests build, the root tests'
// default key; no call of theirs reaches an API.
//
// Copied from the root package's helpers_test.go.
const testKey = "test-key"

// clearEnv unsets, for the rest of the test, the three variables a client
// reads, whatever the shell running the test holds (a developer's
// TYPESAFE_API_KEY among them); t.Setenv restores them when the test ends.
//
// Copied from the root package's helpers_test.go.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{typesafe.APIKeyEnv, typesafe.BaseURLEnv, typesafe.DefaultModelEnv} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}

// newTestClient builds a client over rt, the test's transport
// (WithRoundTripper), with testKey (the upstream tests' key, 8 bytes long,
// so the checks that look for the key inside other text apply to it) and
// opts, after clearing the variables a client reads, so a developer's
// TYPESAFE_API_KEY never reaches a test. The client is closed when the test
// ends.
//
// Its calls make one attempt each (NoRetry), as the upstream tests' clients
// do unless a test asks for retries (tests/conftest.py:34-35); a test that
// wants the production policy passes WithRetry(DefaultRetry()) in opts,
// which comes later and wins.
//
// Copied from the root package's client_test.go.
func newTestClient(t *testing.T, rt http.RoundTripper, opts ...typesafe.ClientOption) *typesafe.Client {
	t.Helper()
	clearEnv(t)
	c, err := typesafe.NewClient(append([]typesafe.ClientOption{typesafe.WithAPIKey(testKey), typesafe.WithRoundTripper(rt), typesafe.WithRetry(typesafe.NoRetry())}, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// mustPrepared prepares qs, failing the test when Prepare fails.
//
// Copied from the root package's client_test.go.
func mustPrepared(t testing.TB, qs *typesafe.Questions) *typesafe.Prepared {
	t.Helper()
	p, err := qs.Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return p
}

// q3Questions is the whole call's question set: the three questions of the
// upstream round-trip test (tests/test_clients.py:60-80), which result.json
// answers.
//
// Copied from the root package's client_test.go.
func q3Questions(t testing.TB) *typesafe.Prepared {
	t.Helper()
	return mustPrepared(t, typesafe.NewQuestions().
		Noul("spam", typesafe.Noul{Instructions: typesafe.Text("Spam?")}).
		Choice("tone", typesafe.Choice{Instructions: typesafe.Text("Tone?"), Options: typesafe.Options{{Label: "friendly"}, {Label: "hostile"}}}).
		Score("quality", typesafe.Score{Instructions: typesafe.Text("Quality?"), Levels: []typesafe.Content{typesafe.Text("bad"), typesafe.Text("ok"), typesafe.Text("great")}}))
}

// answerView is what a test compares of an answer: its kind and every value
// its getters return.
//
// Copied from the root package's client_test.go.
type answerView struct {
	Kind          typesafe.AnswerKind
	Noul          float64
	Choice        string
	Confidence    float64
	Score         float64
	Probabilities map[string]float64
	Levels        map[uint32]float64
	Legend        map[uint32]string
}

// viewOf returns the view of a.
//
// Copied from the root package's client_test.go.
func viewOf(a typesafe.Answer) answerView {
	v := answerView{Kind: a.Kind()}
	if n, ok := a.Noul(); ok {
		v.Noul = n.Noul()
	}
	if c, ok := a.Choice(); ok {
		v.Choice, v.Confidence = c.Choice(), c.Confidence()
		v.Probabilities = maps.Collect(c.Probabilities())
	}
	if s, ok := a.Score(); ok {
		v.Score, v.Confidence = s.Score(), s.Confidence()
		v.Levels = maps.Collect(s.Probabilities())
		v.Legend = map[uint32]string{}
		for level, d := range s.Legend() {
			if d.IsJSON() {
				v.Legend[level] = string(d.JSON())
			} else {
				v.Legend[level] = d.Text()
			}
		}
	}
	return v
}

// usageView is what a test compares of a usage.
//
// Copied from the root package's response_json_test.go.
type usageView struct {
	In, Out       uint64
	HasIn, HasOut bool
}

// namedAnswer is one answer's view with its question's name.
//
// Copied from the root package's response_json_test.go.
type namedAnswer struct {
	Name   string
	Answer answerView
}

// payloadView is what a test compares of a System One response's payload:
// every value its getters return, the answers in the order of All.
//
// Copied from the root package's response_json_test.go.
type payloadView struct {
	Model   string
	Usage   usageView
	Answers []namedAnswer
}

// payloadOf returns the view of r's payload.
//
// Copied from the root package's response_json_test.go.
func payloadOf(r *typesafe.SystemOneResponse) payloadView {
	v := payloadView{Model: r.Model()}
	v.Usage.In, v.Usage.HasIn = r.Usage().InputTokens()
	v.Usage.Out, v.Usage.HasOut = r.Usage().OutputTokens()
	for name, a := range r.Answers().All() {
		v.Answers = append(v.Answers, namedAnswer{name, viewOf(a)})
	}
	return v
}

// reviewAnswers types every answer of RESULT, with the question set
// q3Questions builds by hand (tests/test_clients.py:60-80).
//
// Copied from the root package's decodeas_test.go.
type reviewAnswers struct {
	Spam    typesafe.NoulAnswer   `typesafe:"kind=noul;name=spam;instructions=Spam?"`
	Tone    typesafe.ChoiceAnswer `typesafe:"kind=choice;name=tone;instructions=Tone?;options=friendly|hostile"`
	Quality typesafe.ScoreAnswer  `typesafe:"kind=score;name=quality;instructions=Quality?;levels=bad|ok|great"`
}
