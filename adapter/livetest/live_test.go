// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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

package livetest

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter"
	"github.com/zchee/decision-model-sdk-go/adapter/anthropic"
	"github.com/zchee/decision-model-sdk-go/adapter/gemini"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/cassette"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
	"github.com/zchee/decision-model-sdk-go/adapter/openai"
)

// The environment switches of this package. Every test reads only
// whether a variable is set, never its value; the providers and the SDK
// read the credentials themselves when they are built.
const (
	// liveTestsVar must be "1" for any test here to run.
	liveTestsVar = "ADAPTER_LIVE_TESTS"
	// recordVar, also "1", additionally records each exchange to
	// testdata/cassettes-go/.
	recordVar = "ADAPTER_LIVE_RECORD"
)

// skipReasonAny returns why a live test must skip: liveTestsVar is not
// "1", or none of the named credential variables is set. An empty
// string means the test may run. The reasons name variable names only,
// never a value; getenv is a parameter so the decision is testable
// without reading the environment.
func skipReasonAny(getenv func(string) string, vars ...string) string {
	if getenv(liveTestsVar) != "1" {
		return liveTestsVar + " is not 1"
	}
	for _, name := range vars {
		if getenv(name) != "" {
			return ""
		}
	}
	return "none of " + strings.Join(vars, ", ") + " is set"
}

// skipReasonAll is skipReasonAny with every named variable required.
func skipReasonAll(getenv func(string) string, vars ...string) string {
	if getenv(liveTestsVar) != "1" {
		return liveTestsVar + " is not 1"
	}
	for _, name := range vars {
		if getenv(name) == "" {
			return name + " is not set"
		}
	}
	return ""
}

// skipUnlessLive skips the test unless liveTestsVar is 1 and at least
// one of the named credential variables is set.
func skipUnlessLive(t *testing.T, vars ...string) {
	t.Helper()
	if reason := skipReasonAny(os.Getenv, vars...); reason != "" {
		t.Skip(reason)
	}
}

// skipUnlessLiveAll skips the test unless liveTestsVar is 1 and every
// named credential variable is set.
func skipUnlessLiveAll(t *testing.T, vars ...string) {
	t.Helper()
	if reason := skipReasonAll(os.Getenv, vars...); reason != "" {
		t.Skip(reason)
	}
}

// The fixtures below are upstream's, copied from
// tests/test_client_with_live_apis.py of system-one-adapter-python
// v0.2.1 byte for byte; this package cannot share the sibling copies in
// the replay tests of package adapter, which live in its test files.

// liveState is upstream's STATE (test_client_with_live_apis.py lines 38
// to 42).
const liveState = "The reviewer calls this entirely invented novel about dragons and wizards a " +
	"flawless masterpiece and the best book they have ever read. They say it has no " +
	"weaknesses, offer only unreserved praise, and urge everyone to read it."

// ratingCriteria is upstream's QUESTIONS["rating"].criteria
// (test_client_with_live_apis.py lines 45 to 54), in order.
var ratingCriteria = []string{
	"The reviewer condemns the book and urges readers to avoid it.",
	"The reviewer is mostly critical and does not recommend the book.",
	"The reviewer expresses mixed or neutral feelings about the book.",
	"The reviewer praises the book overall while noting meaningful flaws.",
	"The reviewer offers unreserved praise and an emphatic recommendation.",
}

// liveQuestions returns upstream's QUESTIONS
// (test_client_with_live_apis.py lines 43 to 62) prepared for the SDK.
func liveQuestions(t *testing.T) *decision.Prepared {
	t.Helper()
	levels := make([]decision.Content, len(ratingCriteria))
	for i, criterion := range ratingCriteria {
		levels[i] = decision.Text(criterion)
	}
	prepared, err := decision.NewQuestions().
		Noul("positive", decision.Noul{Instructions: decision.Text("The book review is positive.")}).
		Score("rating", decision.Score{
			Instructions: decision.Text("How favorable the reviewer's overall assessment is."),
			Levels:       levels,
		}).
		Choice("genre", decision.Choice{
			Instructions: decision.Text("Which genre this review is about."),
			Options: decision.Options{
				{Label: "fiction", Description: decision.Text("A novel or short story.")},
				{Label: "nonfiction", Description: decision.Text("A book based on facts, real events, or ideas.")},
			},
		}).
		Prepare()
	if err != nil {
		t.Fatalf("preparing the questions: %v", err)
	}
	return prepared
}

// probeState is upstream's CONTEXT_PROBE_STATE
// (test_client_with_live_apis.py lines 63 to 69), trailing newline
// included.
const probeState = `Catalog facts:
- marker_fen has state DORMANT.
- marker_tor has state ACTIVE.

Shipping facts:
- The parcel's handling class is CLASS_CRYSTAL.
`

// probeQuestions returns upstream's CONTEXT_PROBE_QUESTIONS
// (test_client_with_live_apis.py lines 70 to 85) prepared for the SDK.
func probeQuestions(t *testing.T) *decision.Prepared {
	t.Helper()
	prepared, err := decision.NewQuestions().
		Choice("instruction_probe", decision.Choice{
			Instructions: decision.Text("Return the only marker whose state is ACTIVE."),
			Options: decision.Options{
				{Label: "marker_fen", Description: decision.Text("The marker_fen catalog entry.")},
				{Label: "marker_tor", Description: decision.Text("The marker_tor catalog entry.")},
			},
		}).
		Choice("criteria_probe", decision.Choice{
			Instructions: decision.Text("Return the correct opaque handling route for the parcel."),
			Options: decision.Options{
				{Label: "route_7q", Description: decision.Text("Use when the handling class is CLASS_CRYSTAL.")},
				{Label: "route_2m", Description: decision.Text("Use when the handling class is CLASS_STEEL.")},
			},
		}).
		Prepare()
	if err != nil {
		t.Fatalf("preparing the probe questions: %v", err)
	}
	return prepared
}

// liveCase is one cell of upstream's provider x output mode x answer
// mode parametrization, with the credential variables that admit it.
type liveCase struct {
	id       string
	provider string
	model    string
	answer   adapter.AnswerMode
	output   adapter.OutputMode
	keyVars  []string
}

// liveMatrix returns the 12 cells with upstream's parametrize ids.
func liveMatrix() []liveCase {
	providers := []struct {
		name, model string
		keyVars     []string
	}{
		{"openai", "gpt-4o-mini", []string{"OPENAI_API_KEY"}},
		{"anthropic", "claude-haiku-4-5", []string{"ANTHROPIC_API_KEY"}},
		{"gemini", "gemini-3.5-flash-lite", []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"}},
	}
	answers := []struct {
		word string
		mode adapter.AnswerMode
	}{{"probabilities", adapter.Probabilities}, {"discrete", adapter.Discrete}}
	outputs := []struct {
		word string
		mode adapter.OutputMode
	}{{"native", adapter.Structured}, {"prompted", adapter.Prompted}}
	var cases []liveCase
	for _, a := range answers {
		for _, o := range outputs {
			for _, p := range providers {
				cases = append(cases, liveCase{
					id:       a.word + "-" + o.word + "-" + p.name,
					provider: p.name,
					model:    p.model,
					answer:   a.mode,
					output:   o.mode,
					keyVars:  p.keyVars,
				})
			}
		}
	}
	return cases
}

// recordingTransport returns a transport recording to
// testdata/cassettes-go/<name>.json when recordVar is 1, and nil
// otherwise. The recorder wraps the default transport and scrubs every
// exchange before a byte is written.
func recordingTransport(t *testing.T, name string) http.RoundTripper {
	t.Helper()
	if os.Getenv(recordVar) != "1" {
		return nil
	}
	rec, err := cassette.NewRecorder(http.DefaultTransport, filepath.Join("..", "testdata", "cassettes-go"), name+".json")
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// liveFactory returns a factory building the named real provider from
// its own environment, as a caller's would be built; with a recording
// transport the provider's HTTP client goes through it.
func liveFactory(provider string, rt http.RoundTripper) llm.Factory {
	var client *http.Client
	if rt != nil {
		client = &http.Client{Transport: rt}
	}
	switch provider {
	case "openai":
		return func(model string) (llm.Provider, error) {
			if client != nil {
				return openai.New(model, openai.WithHTTPClient(client))
			}
			return openai.New(model)
		}
	case "anthropic":
		return func(model string) (llm.Provider, error) {
			if client != nil {
				return anthropic.New(model, anthropic.WithHTTPClient(client))
			}
			return anthropic.New(model)
		}
	default:
		return func(model string) (llm.Provider, error) {
			if client != nil {
				return gemini.New(model, gemini.WithHTTPClient(client))
			}
			return gemini.New(model)
		}
	}
}

// liveSystemOne performs one billed evaluation through the Adapter and
// the real provider.
func liveSystemOne(t *testing.T, tc liveCase, cassetteName, state string, prepared *decision.Prepared) *decision.SystemOneResponse {
	t.Helper()
	ad, err := adapter.New(tc.answer, tc.output, adapter.WithFactory(tc.provider, liveFactory(tc.provider, recordingTransport(t, cassetteName))))
	if err != nil {
		t.Fatal(err)
	}
	client, err := adapter.NewClient(ad)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	resp, err := client.SystemOne(t.Context(), state, prepared, decision.Model(adapter.ModelID(tc.provider, tc.model)))
	if err != nil {
		t.Fatalf("SystemOne against the live provider failed: %v", err)
	}
	return resp
}

// checkExpectedProbabilities asserts upstream's three expected answers
// (test_client_with_live_apis.py lines 119 to 125): the positive noul,
// the rating's probability of level 4 and the genre's probability of
// fiction each above 0.9.
func checkExpectedProbabilities(t *testing.T, answers decision.Answers) {
	t.Helper()
	noul, ok := answers.Noul("positive")
	if !ok || noul.Noul() <= 0.9 {
		t.Errorf("positive noul = %v (present %t), want above 0.9", noul.Noul(), ok)
	}
	score, ok := answers.Score("rating")
	if !ok {
		t.Fatal("rating is not a score answer")
	}
	if p, ok := score.Probability(4); !ok || p <= 0.9 {
		t.Errorf("rating P(4) = %v (present %t), want above 0.9", p, ok)
	}
	choice, ok := answers.Choice("genre")
	if !ok {
		t.Fatal("genre is not a choice answer")
	}
	if p, ok := choice.Probability("fiction"); !ok || p <= 0.9 {
		t.Errorf("genre P(fiction) = %v (present %t), want above 0.9", p, ok)
	}
}

// TestLiveReferenceShape is the live half of the replayed reference
// test: upstream's test_live_responses_match_reference_shape
// (test_client_with_live_apis.py lines 146 to 207) against the real
// provider APIs, every provider x output mode x answer mode cell. A
// fresh answer cannot equal a pinned recording, so the test asserts
// what upstream's live run asserts of a fresh response: the three
// expected probabilities, a plausible latency, and the typed views, and
// it compares nothing against testdata/expected.
func TestLiveReferenceShape(t *testing.T) {
	for _, tc := range liveMatrix() {
		t.Run(tc.id, func(t *testing.T) {
			skipUnlessLive(t, tc.keyVars...)
			name := "test_live_responses_match_reference_shape[" + tc.id + "]"
			resp := liveSystemOne(t, tc, name, liveState, liveQuestions(t))
			answers := resp.Answers()
			checkExpectedProbabilities(t, answers)

			// Latency is wall-clock and never reproducible; upstream
			// asserts it is plausible (its lines 127 to 131).
			report, err := adapter.ReportOf(resp)
			if err != nil {
				t.Fatalf("reading the Report: %v", err)
			}
			if l := report.Usage.Latency; l <= 0 || l >= 120*time.Second {
				t.Errorf("latency = %v, want above 0 and under 120s", l)
			}

			// The typed views (upstream's lines 181 to 186): the score
			// legend is the criteria by level, the probability levels are
			// 0 to 4, and the noul and choice views are the answers.
			score, ok := answers.Score("rating")
			if !ok {
				t.Fatal("rating is not a score answer")
			}
			var level uint32
			for l, content := range score.Legend() {
				if l != level || content.Text() != ratingCriteria[level] {
					t.Errorf("legend[%d] = %q at position %d, want %q", l, content.Text(), level, ratingCriteria[level])
				}
				level++
			}
			if int(level) != len(ratingCriteria) {
				t.Errorf("the legend holds %d levels, want %d", level, len(ratingCriteria))
			}
			var probabilityLevels []uint32
			for l := range score.Probabilities() {
				probabilityLevels = append(probabilityLevels, l)
			}
			slices.Sort(probabilityLevels)
			if !slices.Equal(probabilityLevels, []uint32{0, 1, 2, 3, 4}) {
				t.Errorf("score probability levels = %v, want 0 through 4", probabilityLevels)
			}
			noul, _ := answers.Noul("positive")
			general, ok := answers.Get("positive")
			if !ok {
				t.Fatal("positive is not among the answers")
			}
			generalNoul, ok := general.Noul()
			if !ok || generalNoul.Noul() != noul.Noul() {
				t.Errorf("the noul view (%v, present %t) is not the answer (%v)", generalNoul.Noul(), ok, noul.Noul())
			}
			choice, _ := answers.Choice("genre")
			generalAnswer, ok := answers.Get("genre")
			if !ok {
				t.Fatal("genre is not among the answers")
			}
			generalChoice, ok := generalAnswer.Choice()
			if !ok || generalChoice.Choice() != choice.Choice() {
				t.Errorf("the choice view (%q, present %t) is not the answer (%q)", generalChoice.Choice(), ok, choice.Choice())
			}
		})
	}
}

// TestLiveFollowsInstructionsAndCriteria is the live half of the
// replayed probe test: upstream's
// test_live_models_follow_question_instructions_and_criteria
// (test_client_with_live_apis.py lines 210 to 236). Each answer is
// unambiguous only when its model-visible context is available, so the
// test requires both the expected choice and a high probability for it.
func TestLiveFollowsInstructionsAndCriteria(t *testing.T) {
	for _, tc := range liveMatrix() {
		t.Run(tc.id, func(t *testing.T) {
			skipUnlessLive(t, tc.keyVars...)
			name := "test_live_models_follow_question_instructions_and_criteria[" + tc.id + "]"
			resp := liveSystemOne(t, tc, name, probeState, probeQuestions(t))
			answers := resp.Answers()
			expected := map[string]string{
				"instruction_probe": "marker_tor",
				"criteria_probe":    "route_7q",
			}
			for question, want := range expected {
				answer, ok := answers.Choice(question)
				if !ok {
					t.Errorf("%s is not a choice answer", question)
					continue
				}
				if answer.Choice() != want {
					t.Errorf("%s choice = %q, want %q", question, answer.Choice(), want)
				}
				if p, ok := answer.Probability(want); !ok || p <= 0.9 {
					t.Errorf("%s P(%s) = %v (present %t), want above 0.9", question, want, p, ok)
				}
			}
		})
	}
}

// TestLiveTypeSafeReference is the live half of the replayed reference
// baseline: upstream's test_live_typesafe_response_matches_reference_shape
// (test_client_with_live_apis.py lines 239 to 246), the plain SDK client
// against the real System One endpoint with model speed_latest. The
// client reads its own environment as any caller's does; the test only
// requires the two variables to be set before it runs.
func TestLiveTypeSafeReference(t *testing.T) {
	skipUnlessLiveAll(t, decision.APIKeyEnv, decision.BaseURLEnv)
	var opts []decision.ClientOption
	if rt := recordingTransport(t, "test_live_typesafe_response_matches_reference_shape"); rt != nil {
		opts = append(opts, decision.WithRoundTripper(rt))
	}
	client, err := decision.NewClient(opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	resp, err := client.SystemOne(t.Context(), liveState, liveQuestions(t), decision.Model("speed_latest"))
	if err != nil {
		t.Fatalf("SystemOne against the live endpoint failed: %v", err)
	}
	// Upstream's shared assertion checks the three expected answers; its
	// latency bound applies only when the response reports one, which
	// this API's usage does not.
	checkExpectedProbabilities(t, resp.Answers())
}
