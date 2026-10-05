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

package adapter

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter/anthropic"
	"github.com/zchee/decision-model-sdk-go/adapter/gemini"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/cassette"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
	"github.com/zchee/decision-model-sdk-go/adapter/openai"
)

// The replay tests port tests/test_client_with_live_apis.py of
// system-one-adapter-python v0.2.1: the recorded provider exchanges under
// testdata/cassettes replayed through the real providers and the real SDK
// client, and the recorded reference responses under testdata/expected
// compared member for member. The subtest names are upstream's bracketed
// parametrize ids from test_client_with_live_apis.py.
//
// The fixtures below are that file's STATE, QUESTIONS,
// CONTEXT_PROBE_STATE and CONTEXT_PROBE_QUESTIONS (its lines 38 to 85),
// byte for byte.

// replayState is upstream's STATE.
const replayState = "The reviewer calls this entirely invented novel about dragons and wizards a " +
	"flawless masterpiece and the best book they have ever read. They say it has no " +
	"weaknesses, offer only unreserved praise, and urge everyone to read it."

// replayRatingCriteria is upstream's QUESTIONS["rating"].criteria, in
// order.
var replayRatingCriteria = []string{
	"The reviewer condemns the book and urges readers to avoid it.",
	"The reviewer is mostly critical and does not recommend the book.",
	"The reviewer expresses mixed or neutral feelings about the book.",
	"The reviewer praises the book overall while noting meaningful flaws.",
	"The reviewer offers unreserved praise and an emphatic recommendation.",
}

// replayGenreCriteria is upstream's QUESTIONS["genre"].criteria, in
// order.
var replayGenreCriteria = []struct{ label, criterion string }{
	{"fiction", "A novel or short story."},
	{"nonfiction", "A book based on facts, real events, or ideas."},
}

// replayQuestions returns upstream's QUESTIONS prepared for the SDK.
func replayQuestions(t *testing.T) *decision.Prepared {
	t.Helper()
	levels := make([]decision.Content, len(replayRatingCriteria))
	for i, criterion := range replayRatingCriteria {
		levels[i] = decision.Text(criterion)
	}
	options := make(decision.Options, len(replayGenreCriteria))
	for i, c := range replayGenreCriteria {
		options[i] = decision.Option{Label: c.label, Description: decision.Text(c.criterion)}
	}
	prepared, err := decision.NewQuestions().
		Noul("positive", decision.Noul{Instructions: decision.Text("The book review is positive.")}).
		Score("rating", decision.Score{
			Instructions: decision.Text("How favorable the reviewer's overall assessment is."),
			Levels:       levels,
		}).
		Choice("genre", decision.Choice{
			Instructions: decision.Text("Which genre this review is about."),
			Options:      options,
		}).
		Prepare()
	if err != nil {
		t.Fatalf("preparing the questions: %v", err)
	}
	return prepared
}

// contextProbeState is upstream's CONTEXT_PROBE_STATE, trailing newline
// included.
const contextProbeState = `Catalog facts:
- marker_fen has state DORMANT.
- marker_tor has state ACTIVE.

Shipping facts:
- The parcel's handling class is CLASS_CRYSTAL.
`

// contextProbeQuestions returns upstream's CONTEXT_PROBE_QUESTIONS
// prepared for the SDK.
func contextProbeQuestions(t *testing.T) *decision.Prepared {
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

// replayCase is one cell of upstream's provider x output mode x answer
// mode parametrization.
type replayCase struct {
	id       string
	provider string
	model    string
	base     string
	answer   AnswerMode
	output   OutputMode
}

// replayMatrix returns the 12 cells with upstream's parametrize ids.
func replayMatrix() []replayCase {
	providers := []struct{ name, model, base string }{
		{"openai", "gpt-4o-mini", "https://api.openai.com/v1"},
		{"anthropic", "claude-haiku-4-5", "https://api.anthropic.com"},
		{"gemini", "gemini-3.5-flash-lite", "https://generativelanguage.googleapis.com/"},
	}
	answers := []struct {
		word string
		mode AnswerMode
	}{{"probabilities", Probabilities}, {"discrete", Discrete}}
	outputs := []struct {
		word string
		mode OutputMode
	}{{"native", Structured}, {"prompted", Prompted}}
	var cases []replayCase
	for _, a := range answers {
		for _, o := range outputs {
			for _, p := range providers {
				cases = append(cases, replayCase{
					id:       a.word + "-" + o.word + "-" + p.name,
					provider: p.name,
					model:    p.model,
					base:     p.base,
					answer:   a.mode,
					output:   o.mode,
				})
			}
		}
	}
	return cases
}

// replayFactory returns a factory building the named real provider over
// the replay transport, with a made-up key and the recorded default base.
func replayFactory(provider string, tr http.RoundTripper) llm.Factory {
	client := &http.Client{Transport: tr}
	switch provider {
	case "openai":
		return func(model string) (llm.Provider, error) {
			return openai.New(model, openai.WithAPIKey("not-a-key"), openai.WithBaseURL("https://api.openai.com/v1"), openai.WithHTTPClient(client))
		}
	case "anthropic":
		return func(model string) (llm.Provider, error) {
			return anthropic.New(model, anthropic.WithAPIKey("not-a-key"), anthropic.WithBaseURL("https://api.anthropic.com"), anthropic.WithHTTPClient(client))
		}
	default:
		return func(model string) (llm.Provider, error) {
			return gemini.New(model, gemini.WithAPIKey("not-a-key"), gemini.WithBaseURL("https://generativelanguage.googleapis.com/"), gemini.WithHTTPClient(client))
		}
	}
}

// loadCassette loads one committed cassette by its file name.
func loadCassette(t *testing.T, name string) *cassette.Cassette {
	t.Helper()
	c, err := cassette.Load(filepath.Join("testdata", "cassettes", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// replayThroughSDK replays one cassette through a real client built on
// the real provider and returns the response and the transport.
func replayThroughSDK(t *testing.T, tc replayCase, name string, state any, prepared *decision.Prepared) (*decision.SystemOneResponse, *cassette.Transport) {
	t.Helper()
	tr := loadCassette(t, name).Transport()
	ad, err := New(tc.answer, tc.output, WithFactory(tc.provider, replayFactory(tc.provider, tr)))
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ad)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	resp, err := client.SystemOne(t.Context(), state, prepared, decision.Model(ModelID(tc.provider, tc.model)))
	if err != nil {
		t.Fatalf("SystemOne over the recorded exchange failed: %v", err)
	}
	return resp, tr
}

// TestReplayReferenceShape ports test_live_responses_match_reference_shape
// (tests/test_client_with_live_apis.py:146-207): each of the 12 recorded
// exchanges replays through the SDK, and the Adapter's body equals the
// reference response under the normalisation rules of the comparison
// helpers below.
func TestReplayReferenceShape(t *testing.T) {
	t.Parallel()
	for _, tc := range replayMatrix() {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			name := "test_live_responses_match_reference_shape[" + tc.id + "]"
			c := loadCassette(t, name)
			resp, tr := replayThroughSDK(t, tc, name, replayState, replayQuestions(t))
			body := resp.Meta().RawBody()
			expected, err := os.ReadFile(filepath.Join("testdata", "expected", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			cassetteBody := c.Interactions[0].Response.Body
			diffs, err := compareReplayBody(body, expected, cassetteBody)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range diffs {
				t.Error(d)
			}
			// The reason the reference's llm_response may stand in for the
			// wire body is itself tested: the reference value equals the
			// recorded body under the known vendor-SDK differences.
			t.Run("expected llm_response derives from the cassette body", func(t *testing.T) {
				want := readJSON(t, expected, "the expected file")
				attempts := memberOf(t, memberOf(t, want, "debug", "$"), "llm_attempts", "$.debug")
				for i := range attempts.Len() {
					attempt := attempts.Index(i)
					ref := memberOf(t, attempt, "llm_response", "$.debug.llm_attempts")
					var refDiffs []string
					referenceProviderDiffs(ref, readJSON(t, cassetteBody, "the cassette body"), tc.provider, "$", &refDiffs)
					for _, d := range refDiffs {
						t.Error(d)
					}
				}
			})

			// Upstream's typed-view assertions (its lines 119-125 and
			// 181-186): the expected probabilities, the score legend and
			// level keys, and the noul and choice views.
			answers := resp.Answers()
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
			legend := make(map[uint32]string)
			for level, content := range score.Legend() {
				legend[level] = content.Text()
			}
			wantLegend := make(map[uint32]string, len(replayRatingCriteria))
			var level uint32
			for _, criterion := range replayRatingCriteria {
				wantLegend[level] = criterion
				level++
			}
			if diff := cmp.Diff(wantLegend, legend); diff != "" {
				t.Errorf("score legend (-criteria +got):\n%s", diff)
			}
			var probabilityLevels []uint32
			for level := range score.Probabilities() {
				probabilityLevels = append(probabilityLevels, level)
			}
			slices.Sort(probabilityLevels)
			if diff := cmp.Diff([]uint32{0, 1, 2, 3, 4}, probabilityLevels); diff != "" {
				t.Errorf("score probability levels (-want +got):\n%s", diff)
			}
			general, ok := answers.Get("positive")
			if !ok {
				t.Fatal("positive is not among the answers")
			}
			generalNoul, ok := general.Noul()
			if !ok || generalNoul.Noul() != noul.Noul() {
				t.Errorf("the noul view (%v) is not the answer (%v, present %t)", noul.Noul(), generalNoul.Noul(), ok)
			}
			generalAnswer, ok := answers.Get("genre")
			if !ok {
				t.Fatal("genre is not among the answers")
			}
			generalChoice, ok := generalAnswer.Choice()
			if !ok || generalChoice.Choice() != choice.Choice() || generalChoice.Confidence() != choice.Confidence() {
				t.Errorf("the choice view (%q, %v) is not the answer (%q, %v, present %t)",
					choice.Choice(), choice.Confidence(), generalChoice.Choice(), generalChoice.Confidence(), ok)
			}

			sent := tr.Requests()
			if len(sent) != 1 {
				t.Fatalf("the provider sent %d requests, want 1", len(sent))
			}
			if tc.output == Structured && tc.answer == Probabilities {
				checkChoiceSchemaInRequest(t, sent[0].Body)
			}
			if tc.output == Prompted {
				comparePromptedSystemText(t, tc.provider, sent[0].Body, c.Interactions[0].Request.Body)
			}
			if n := tr.Unconsumed(); n != 0 {
				t.Errorf("Unconsumed = %d at the test's end, want 0", n)
			}
		})
	}
}

// TestReplayFollowsInstructionsAndCriteria ports
// test_live_models_follow_question_instructions_and_criteria
// (tests/test_client_with_live_apis.py:210-236): each recorded probe
// exchange replays through the SDK, and each answer is the choice whose
// model-visible context makes it unambiguous, with a high probability.
func TestReplayFollowsInstructionsAndCriteria(t *testing.T) {
	t.Parallel()
	for _, tc := range replayMatrix() {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			name := "test_live_models_follow_question_instructions_and_criteria[" + tc.id + "]"
			c := loadCassette(t, name)
			resp, tr := replayThroughSDK(t, tc, name, contextProbeState, contextProbeQuestions(t))
			answers := resp.Answers()
			tests := map[string]struct{ choice string }{
				"instruction_probe": {choice: "marker_tor"},
				"criteria_probe":    {choice: "route_7q"},
			}
			for question, tt := range tests {
				answer, ok := answers.Choice(question)
				if !ok {
					t.Errorf("%s is not a choice answer", question)
					continue
				}
				if answer.Choice() != tt.choice {
					t.Errorf("%s choice = %q, want %q", question, answer.Choice(), tt.choice)
				}
				if p, ok := answer.Probability(tt.choice); !ok || p <= 0.9 {
					t.Errorf("%s P(%s) = %v (present %t), want above 0.9", question, tt.choice, p, ok)
				}
			}
			sent := tr.Requests()
			if len(sent) != 1 {
				t.Fatalf("the provider sent %d requests, want 1", len(sent))
			}
			if tc.output == Prompted {
				comparePromptedSystemText(t, tc.provider, sent[0].Body, c.Interactions[0].Request.Body)
			}
			if n := tr.Unconsumed(); n != 0 {
				t.Errorf("Unconsumed = %d at the test's end, want 0", n)
			}
		})
	}
}

// TestReplayTypeSafeReference ports
// test_live_typesafe_response_matches_reference_shape
// (tests/test_client_with_live_apis.py:239-246): the root SDK client over
// the recorded TypeSafe exchange, serialized and compared exactly with
// the reference response, which documents the shape the Adapter
// imitates. The recording spells each question's members in another
// order than the Go SDK writes (its rating holds type, criteria,
// instructions), which is why the replay matches request bodies as JSON
// values.
func TestReplayTypeSafeReference(t *testing.T) {
	t.Parallel()
	c := loadCassette(t, "test_live_typesafe_response_matches_reference_shape")
	tr := c.Transport()
	client, err := decision.NewClient(
		decision.WithAPIKey("not-a-key"),
		decision.WithBaseURL("https://api.typesafe.ai"),
		decision.WithRetry(decision.NoRetry()),
		decision.WithRoundTripper(tr),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	resp, err := client.SystemOne(t.Context(), replayState, replayQuestions(t), decision.Model("speed_latest"))
	if err != nil {
		t.Fatalf("SystemOne over the recorded exchange failed: %v", err)
	}
	body, err := resp.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(filepath.Join("testdata", "expected", "test_live_typesafe_response_matches_reference_shape.json"))
	if err != nil {
		t.Fatal(err)
	}
	var diffs []string
	exactDiffs(readJSON(t, body, "the serialized response"), readJSON(t, expected, "the expected file"), "$", &diffs)
	for _, d := range diffs {
		t.Error(d)
	}
	if n := tr.Unconsumed(); n != 0 {
		t.Errorf("Unconsumed = %d at the test's end, want 0", n)
	}
}

// readJSON reads one JSON text or fails the test.
func readJSON(t *testing.T, doc []byte, what string) jsonx.Node {
	t.Helper()
	v, err := jsonx.Read(doc)
	if err != nil {
		t.Fatalf("reading %s: %v", what, err)
	}
	return v
}

// memberOf returns the named member or fails the test.
func memberOf(t *testing.T, v jsonx.Node, name, path string) jsonx.Node {
	t.Helper()
	m, ok := v.Member(name)
	if !ok {
		t.Fatalf("%s has no member %q", path, name)
	}
	return m
}

// The comparison of the Adapter's body with the reference response is a
// set of pure functions that return the differences they find, so a test
// can also assert that a deliberately wrong document is refused.

// memberNames returns an object's member names in order.
func memberNames(v jsonx.Node) []string {
	names := make([]string, 0, v.Len())
	for i := range v.Len() {
		names = append(names, v.Name(i))
	}
	return names
}

// memberOrderDiff reports whether got and want are objects with the same
// member names in the same order; the returned difference is "" when they
// are.
func memberOrderDiff(got, want jsonx.Node, path string) string {
	if got.Kind() != jsonx.KindObject || want.Kind() != jsonx.KindObject {
		return path + ": not an object on both sides"
	}
	if diff := cmp.Diff(memberNames(want), memberNames(got)); diff != "" {
		return path + ": member names or order differ (-reference +got):\n" + diff
	}
	return ""
}

// compareReplayBody compares the Adapter's wire body with the reference
// response and returns every difference: latency bounds-checked and left
// out of the comparison (the reference holds none), llm_response checked
// against the recorded provider body, debug_info.provider mapped from the
// Python class path to the Go type path, the schema and request members
// compared as JSON values, and everything else exact with its member
// order.
func compareReplayBody(gotBody, wantBody, cassetteBody []byte) ([]string, error) {
	got, err := jsonx.Read(gotBody)
	if err != nil {
		return nil, fmt.Errorf("reading the Adapter body: %w", err)
	}
	want, err := jsonx.Read(wantBody)
	if err != nil {
		return nil, fmt.Errorf("reading the expected file: %w", err)
	}
	var diffs []string
	if d := memberOrderDiff(got, want, "$"); d != "" {
		return []string{d}, nil
	}
	for i := range want.Len() {
		name := want.Name(i)
		g, w := got.Index(i), want.Index(i)
		switch name {
		case "usage":
			usageDiffs(g, w, &diffs)
		case "debug":
			debugDiffs(g, w, cassetteBody, &diffs)
		default:
			exactDiffs(g, w, "$."+name, &diffs)
		}
	}
	return diffs, nil
}

// latencyBoundsOK reports upstream's plausibility bounds for a recorded
// wall-clock latency, a number above 0 and below 120 seconds.
func latencyBoundsOK(text string) bool {
	v, err := strconv.ParseFloat(text, 64)
	return err == nil && v > 0 && v < 120
}

// usageDiffs compares usage: the body's latency is bounds-checked as
// upstream's test checks it, then left out (the reference holds none);
// the rest is exact in order.
func usageDiffs(got, want jsonx.Node, out *[]string) {
	if got.Kind() != jsonx.KindObject || want.Kind() != jsonx.KindObject {
		*out = append(*out, "$.usage: not an object on both sides")
		return
	}
	gotNames := make([]string, 0, got.Len())
	sawLatency := false
	for i := range got.Len() {
		name := got.Name(i)
		if name == "latency" {
			sawLatency = true
			if text := got.Index(i).Text(); got.Index(i).Kind() != jsonx.KindNumber || !latencyBoundsOK(text) {
				*out = append(*out, "$.usage.latency = "+text+", want a number above 0 and below 120")
			}
			continue
		}
		gotNames = append(gotNames, name)
	}
	if !sawLatency {
		*out = append(*out, "$.usage has no latency member to bounds-check")
	}
	if diff := cmp.Diff(memberNames(want), gotNames); diff != "" {
		*out = append(*out, "$.usage: member names or order differ, latency aside (-reference +got):\n"+diff)
		return
	}
	for i := range want.Len() {
		name := want.Name(i)
		g, _ := got.Member(name)
		exactDiffs(g, want.Index(i), "$.usage."+name, out)
	}
}

// debugDiffs compares debug: exact but for the attempts.
func debugDiffs(got, want jsonx.Node, cassetteBody []byte, out *[]string) {
	if d := memberOrderDiff(got, want, "$.debug"); d != "" {
		*out = append(*out, d)
		return
	}
	for i := range want.Len() {
		name := want.Name(i)
		g, w := got.Index(i), want.Index(i)
		if name != "llm_attempts" {
			exactDiffs(g, w, "$.debug."+name, out)
			continue
		}
		if g.Kind() != jsonx.KindArray || w.Kind() != jsonx.KindArray || g.Len() != w.Len() {
			*out = append(*out, fmt.Sprintf("$.debug.llm_attempts: an array of %d attempts is not the reference's %d", g.Len(), w.Len()))
			continue
		}
		for k := range w.Len() {
			attemptDiffs(g.Index(k), w.Index(k), cassetteBody, "$.debug.llm_attempts["+strconv.Itoa(k)+"]", out)
		}
	}
}

// providerClassToGoType maps the reference's Python provider class paths
// to the Go type paths the port records. The async names stand beside the
// sync ones because upstream's structured branch ran its async client.
var providerClassToGoType = map[string]string{
	"system_one_adapter.providers.openai.OpenAIProvider":            "github.com/zchee/decision-model-sdk-go/adapter/openai.Provider",
	"system_one_adapter.providers.openai.AsyncOpenAIProvider":       "github.com/zchee/decision-model-sdk-go/adapter/openai.Provider",
	"system_one_adapter.providers.anthropic.AnthropicProvider":      "github.com/zchee/decision-model-sdk-go/adapter/anthropic.Provider",
	"system_one_adapter.providers.anthropic.AsyncAnthropicProvider": "github.com/zchee/decision-model-sdk-go/adapter/anthropic.Provider",
	"system_one_adapter.providers.gemini.GeminiProvider":            "github.com/zchee/decision-model-sdk-go/adapter/gemini.Provider",
	"system_one_adapter.providers.gemini.AsyncGeminiProvider":       "github.com/zchee/decision-model-sdk-go/adapter/gemini.Provider",
}

// attemptDiffs compares one llm_attempts element: messages and
// debug_info exact (provider mapped through providerClassToGoType), the
// schema and request as JSON values, llm_response against the recorded
// provider body.
func attemptDiffs(got, want jsonx.Node, cassetteBody []byte, path string, out *[]string) {
	if d := memberOrderDiff(got, want, path); d != "" {
		*out = append(*out, d)
		return
	}
	for i := range want.Len() {
		name := want.Name(i)
		g, w := got.Index(i), want.Index(i)
		at := path + "." + name
		switch name {
		case "model_request_parameters":
			if d := memberOrderDiff(g, w, at); d != "" {
				*out = append(*out, d)
				continue
			}
			for j := range w.Len() {
				if inner := w.Name(j); inner == "schema" {
					valueDiffs(g.Index(j), w.Index(j), at+".schema", out)
				} else {
					exactDiffs(g.Index(j), w.Index(j), at+"."+inner, out)
				}
			}
		case "llm_response":
			b, err := g.PydanticJSON()
			if err != nil {
				*out = append(*out, at+": serializing: "+err.Error())
				continue
			}
			if equal, err := jsonx.Equal(b, cassetteBody); err != nil || !equal {
				*out = append(*out, fmt.Sprintf("%s does not equal the cassette's recorded body as a JSON value (err %v)", at, err))
			}
		case "debug_info":
			if d := memberOrderDiff(g, w, at); d != "" {
				*out = append(*out, d)
				continue
			}
			for j := range w.Len() {
				inner := w.Name(j)
				if inner != "provider" {
					exactDiffs(g.Index(j), w.Index(j), at+"."+inner, out)
					continue
				}
				wantClass := w.Index(j).Text()
				goType, known := providerClassToGoType[wantClass]
				if !known {
					*out = append(*out, fmt.Sprintf("%s.provider: the reference names the unknown class %q", at, wantClass))
					continue
				}
				if g.Index(j).Kind() != jsonx.KindString || g.Index(j).Text() != goType {
					*out = append(*out, fmt.Sprintf("%s.provider = %q, want the Go type %q for the reference class %q", at, g.Index(j).Text(), goType, wantClass))
				}
			}
		case "request":
			valueDiffs(g, w, at, out)
		default:
			exactDiffs(g, w, at, out)
		}
	}
}

// valueDiffs compares two subtrees as JSON values, member order ignored
// at every level.
func valueDiffs(got, want jsonx.Node, path string, out *[]string) {
	gotB, errG := got.PydanticJSON()
	wantB, errW := want.PydanticJSON()
	if errG != nil || errW != nil {
		*out = append(*out, fmt.Sprintf("%s: serializing: %v, %v", path, errG, errW))
		return
	}
	equal, err := jsonx.Equal(gotB, wantB)
	if err != nil {
		*out = append(*out, path+": comparing as JSON values: "+err.Error())
		return
	}
	if !equal {
		*out = append(*out, fmt.Sprintf("%s: the values differ\n got: %s\nreference: %s", path, clip(gotB), clip(wantB)))
	}
}

// clip bounds a value quoted in a failure message.
func clip(b []byte) string {
	const limit = 600
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + " [clipped]"
}

// exactDiffs compares two subtrees exactly: member names and order, kinds
// (an integer literal is not a float literal), strings, and numbers by
// value.
func exactDiffs(got, want jsonx.Node, path string, out *[]string) {
	if want.Kind() != got.Kind() {
		*out = append(*out, path+": kind differs from the reference")
		return
	}
	switch want.Kind() {
	case jsonx.KindNull, jsonx.KindTrue, jsonx.KindFalse:
	case jsonx.KindNumber:
		if got.IsInt() != want.IsInt() {
			*out = append(*out, fmt.Sprintf("%s: %s and the reference's %s differ in integer-ness", path, got.Text(), want.Text()))
			return
		}
		if got.IsInt() {
			if normalizeMinusZero(got.Text()) != normalizeMinusZero(want.Text()) {
				*out = append(*out, fmt.Sprintf("%s: %s is not the reference's %s", path, got.Text(), want.Text()))
			}
			return
		}
		g, errG := strconv.ParseFloat(got.Text(), 64)
		w, errW := strconv.ParseFloat(want.Text(), 64)
		if errG != nil || errW != nil || g != w {
			*out = append(*out, fmt.Sprintf("%s: %s is not the reference's %s", path, got.Text(), want.Text()))
		}
	case jsonx.KindString:
		if got.Text() != want.Text() {
			*out = append(*out, fmt.Sprintf("%s: %q is not the reference's %q", path, got.Text(), want.Text()))
		}
	case jsonx.KindArray:
		if got.Len() != want.Len() {
			*out = append(*out, fmt.Sprintf("%s: an array of %d elements is not the reference's %d", path, got.Len(), want.Len()))
			return
		}
		for i := range want.Len() {
			exactDiffs(got.Index(i), want.Index(i), path+"["+strconv.Itoa(i)+"]", out)
		}
	case jsonx.KindObject:
		if d := memberOrderDiff(got, want, path); d != "" {
			*out = append(*out, d)
			return
		}
		for i := range want.Len() {
			exactDiffs(got.Index(i), want.Index(i), path+"."+want.Name(i), out)
		}
	}
}

// normalizeMinusZero folds the one JSON integer spelling difference.
func normalizeMinusZero(text string) string {
	if text == "-0" {
		return "0"
	}
	return text
}

// geminiSDKAddedMembers are the members google-genai adds to its model
// dump that the wire body does not carry, dropped from the reference side.
var geminiSDKAddedMembers = []string{"id", "output_text"}

// geminiSDKDroppedUsageMembers are the usage members google-genai does not
// model, present in the recorded body and absent from the reference,
// ignored on the cassette side.
var geminiSDKDroppedUsageMembers = []string{
	"model_invocation_token_counts",
	"non_grounding_model_invocation_token_counts",
	"raw_prompt_token",
}

// referenceProviderDiffs compares the reference's llm_response with the recorded
// provider body under the known vendor-SDK differences: null members
// dropped on both sides as upstream's test drops them, numbers compared
// as values (an integer equals the float it is), and, for gemini, the
// SDK-added members dropped from the reference and the SDK-dropped usage
// members ignored in the recording. Member order is ignored, as Python's
// dict equality ignores it.
func referenceProviderDiffs(expected, recorded jsonx.Node, provider, path string, out *[]string) {
	if expected.Kind() == jsonx.KindObject && recorded.Kind() == jsonx.KindObject {
		expectedNames := withoutNullMembers(expected)
		recordedNames := withoutNullMembers(recorded)
		if provider == "gemini" && path == "$" {
			expectedNames = slices.DeleteFunc(expectedNames, func(name string) bool {
				return slices.Contains(geminiSDKAddedMembers, name)
			})
		}
		if provider == "gemini" && path == "$.usage" {
			recordedNames = slices.DeleteFunc(recordedNames, func(name string) bool {
				return slices.Contains(geminiSDKDroppedUsageMembers, name)
			})
		}
		e, r := slices.Sorted(slices.Values(expectedNames)), slices.Sorted(slices.Values(recordedNames))
		if diff := cmp.Diff(r, e); diff != "" {
			*out = append(*out, path+": the reference's member set differs from the recording's (-recorded +reference):\n"+diff)
			return
		}
		for _, name := range expectedNames {
			ev, _ := expected.Member(name)
			rv, _ := recorded.Member(name)
			referenceProviderDiffs(ev, rv, provider, path+"."+name, out)
		}
		return
	}
	if expected.Kind() == jsonx.KindArray && recorded.Kind() == jsonx.KindArray {
		if expected.Len() != recorded.Len() {
			*out = append(*out, fmt.Sprintf("%s: an array of %d elements in the reference, %d in the recording", path, expected.Len(), recorded.Len()))
			return
		}
		for i := range expected.Len() {
			referenceProviderDiffs(expected.Index(i), recorded.Index(i), provider, path+"["+strconv.Itoa(i)+"]", out)
		}
		return
	}
	if expected.Kind() == jsonx.KindNumber && recorded.Kind() == jsonx.KindNumber {
		if !pythonNumbersEqual(expected, recorded) {
			*out = append(*out, fmt.Sprintf("%s: the reference's %s is not the recording's %s", path, expected.Text(), recorded.Text()))
		}
		return
	}
	if expected.Kind() != recorded.Kind() {
		*out = append(*out, path+": the reference's kind differs from the recording's")
		return
	}
	if expected.Kind() == jsonx.KindString && expected.Text() != recorded.Text() {
		*out = append(*out, fmt.Sprintf("%s: the reference's %q is not the recording's %q", path, expected.Text(), recorded.Text()))
	}
}

// withoutNullMembers returns an object's member names whose values are
// not null, in order.
func withoutNullMembers(v jsonx.Node) []string {
	names := make([]string, 0, v.Len())
	for i := range v.Len() {
		if v.Index(i).Kind() != jsonx.KindNull {
			names = append(names, v.Name(i))
		}
	}
	return names
}

// pythonNumbersEqual compares two number literals as Python's == compares
// the values json.loads makes of them.
func pythonNumbersEqual(a, b jsonx.Node) bool {
	equal, err := jsonx.Equal([]byte(a.Text()), []byte(b.Text()))
	return err == nil && equal
}

// checkChoiceSchemaInRequest reads the native answer schema back out of
// the first provider request's envelope, as upstream's test reads it
// (tests/test_client_with_live_apis.py:188-207), and asserts the choice
// question's instructions and criteria inside the nested schema's
// descriptions.
func checkChoiceSchemaInRequest(t *testing.T, sentBody []byte) {
	t.Helper()
	body := readJSON(t, sentBody, "the first provider request")
	var schema jsonx.Node
	switch {
	case hasMember(body, "output_config"):
		schema = memberOf(t, memberOf(t, memberOf(t, body, "output_config", "$"), "format", "$.output_config"), "schema", "$.output_config.format")
	case hasMember(body, "response_format"):
		schema = memberOf(t, memberOf(t, body, "response_format", "$"), "schema", "$.response_format")
	default:
		schema = memberOf(t, memberOf(t, memberOf(t, body, "text", "$"), "format", "$.text"), "schema", "$.text.format")
	}
	defs := memberOf(t, schema, "$defs", "the schema")
	answersSchema := memberOf(t, defs, "TypeSafeAnswers", "the schema's $defs")
	genre := memberOf(t, memberOf(t, answersSchema, "properties", "TypeSafeAnswers"), "genre", "TypeSafeAnswers.properties")
	ref := memberOf(t, genre, "$ref", "TypeSafeAnswers.properties.genre").Text()
	choiceSchema := memberOf(t, defs, ref[strings.LastIndex(ref, "/")+1:], "the schema's $defs")
	description := memberOf(t, choiceSchema, "description", "the choice schema").Text()
	if want := "Which genre this review is about."; !strings.Contains(description, want) {
		t.Errorf("the choice schema's description %q does not contain the instructions %q", description, want)
	}
	properties := memberOf(t, choiceSchema, "properties", "the choice schema")
	for _, c := range replayGenreCriteria {
		property := memberOf(t, properties, c.label, "the choice schema's properties")
		propertyDescription := memberOf(t, property, "description", "the choice property").Text()
		if !strings.Contains(propertyDescription, c.criterion) {
			t.Errorf("the %q property's description %q does not contain the criterion %q", c.label, propertyDescription, c.criterion)
		}
	}
}

// hasMember reports whether an object has the named member.
func hasMember(v jsonx.Node, name string) bool {
	_, ok := v.Member(name)
	return ok
}

// comparePromptedSystemText compares the system text of the sent prompted
// request with the recorded one byte for byte, the decoded strings
// compared with ==; the member holding it differs per provider.
func comparePromptedSystemText(t *testing.T, provider string, sentBody, recordedBody []byte) {
	t.Helper()
	sent := promptedSystemText(t, provider, readJSON(t, sentBody, "the sent request"))
	recorded := promptedSystemText(t, provider, readJSON(t, recordedBody, "the recorded request"))
	if sent != recorded {
		t.Errorf("the sent system text is not byte-equal to the recorded one\n sent: %s\nrecorded: %s", clip([]byte(sent)), clip([]byte(recorded)))
	}
}

// promptedSystemText extracts a prompted request's system text: the
// system-role input item for openai, the system member for anthropic, the
// system_instruction member for gemini.
func promptedSystemText(t *testing.T, provider string, body jsonx.Node) string {
	t.Helper()
	switch provider {
	case "openai":
		input := memberOf(t, body, "input", "the request")
		for i := range input.Len() {
			item := input.Index(i)
			if role, ok := item.Member("role"); ok && role.Text() == "system" {
				return memberOf(t, item, "content", "the system input item").Text()
			}
		}
		t.Fatal("the request's input has no system item")
		return ""
	case "anthropic":
		return memberOf(t, body, "system", "the request").Text()
	default:
		return memberOf(t, body, "system_instruction", "the request").Text()
	}
}

// TestReplayComparisonEntryPoints checks complete responses through the
// same comparison used for recorded provider calls, including dispatch
// to the actual provider-body comparison and the fixed provider table.
func TestReplayComparisonEntryPoints(t *testing.T) {
	t.Parallel()
	const recorded = `{"answer":"wire"}`
	const actual = `{"model":"reference","usage":{"input_tokens":1,"latency":1.5},"answers":{},"debug":{"llm_attempts":[{"messages":[],"model_request_parameters":{"schema":{},"structured":true},"llm_response":{"answer":"wire"},"debug_info":{"provider":"github.com/zchee/decision-model-sdk-go/adapter/openai.Provider"},"request":{}}]}}`
	const expected = `{"model":"reference","usage":{"input_tokens":1},"answers":{},"debug":{"llm_attempts":[{"messages":[],"model_request_parameters":{"schema":{},"structured":true},"llm_response":{"answer":"wire"},"debug_info":{"provider":"system_one_adapter.providers.openai.OpenAIProvider"},"request":{}}]}}`
	tests := map[string]struct {
		actual, expected, recorded string
		wantDifference             string
	}{
		"success: complete matching response": {
			actual: actual, expected: expected, recorded: recorded,
		},
		"error: actual provider body differs": {
			actual:   strings.Replace(actual, `"answer":"wire"`, `"answer":"changed"`, 1),
			expected: expected, recorded: recorded,
			wantDifference: ".llm_response",
		},
		"error: recorded body differs from actual": {
			actual: actual, expected: expected, recorded: `{"answer":"changed"}`,
			wantDifference: ".llm_response",
		},
		"error: unknown class contains a known vendor name": {
			actual: actual, recorded: recorded,
			expected:       strings.Replace(expected, "openai.OpenAIProvider", "openai.UnregisteredProvider", 1),
			wantDifference: "unknown class",
		},
		"error: actual Go provider type is wrong": {
			actual:   strings.Replace(actual, "openai.Provider", "gemini.Provider", 1),
			expected: expected, recorded: recorded,
			wantDifference: ".provider",
		},
		"error: full attempt messages differ": {
			actual:   strings.Replace(actual, `"messages":[]`, `"messages":[{"role":"user","content":"changed"}]`, 1),
			expected: expected, recorded: recorded,
			wantDifference: ".messages",
		},
		"error: numeric-looking string latency": {
			actual:   strings.Replace(actual, `"latency":1.5`, `"latency":"1.5"`, 1),
			expected: expected, recorded: recorded,
			wantDifference: "$.usage.latency",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diffs, err := compareReplayBody([]byte(tt.actual), []byte(tt.expected), []byte(tt.recorded))
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantDifference == "" {
				if len(diffs) != 0 {
					t.Fatalf("matching complete response refused: %v", diffs)
				}
				return
			}
			if !strings.Contains(strings.Join(diffs, "\n"), tt.wantDifference) {
				t.Errorf("differences = %v, want a refusal containing %q", diffs, tt.wantDifference)
			}
		})
	}
}

// TestReplayComparisonHelpers pins each normalisation rule of the
// comparison against documents built to break it, so a weakened rule
// cannot pass unnoticed: order where it must be respected, order where it
// must be ignored, exact floats, the latency bounds, and the known
// vendor-SDK differences of the reference derivation.
func TestReplayComparisonHelpers(t *testing.T) {
	t.Parallel()
	read := func(t *testing.T, doc string) jsonx.Node {
		t.Helper()
		v, err := jsonx.Read([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	t.Run("exact comparison", func(t *testing.T) {
		t.Parallel()
		tests := map[string]struct {
			got, want string
			refused   bool
		}{
			"success: equal documents": {
				got: `{"a":1,"b":[true,null,"s"],"f":0.5}`, want: `{"a":1,"b":[true,null,"s"],"f":0.5}`,
			},
			"error: top-level member order differs": {
				got: `{"b":2,"a":1}`, want: `{"a":1,"b":2}`,
				refused: true,
			},
			"error: member order differs although every value is equal": {
				got: `{"b":1,"a":1}`, want: `{"a":1,"b":1}`,
				refused: true,
			},
			"error: nested member order differs": {
				got: `{"o":{"y":2,"x":1}}`, want: `{"o":{"x":1,"y":2}}`,
				refused: true,
			},
			"error: a float differing in the last bit": {
				got: `{"f":1.0000000000000002}`, want: `{"f":1.0}`,
				refused: true,
			},
			"error: an integer literal where the reference holds a float": {
				got: `{"v":4}`, want: `{"v":4.0}`,
				refused: true,
			},
			"error: a string differs": {
				got: `{"s":"x"}`, want: `{"s":"y"}`,
				refused: true,
			},
			"error: an array length differs": {
				got: `{"a":[1]}`, want: `{"a":[1,2]}`,
				refused: true,
			},
		}
		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				var diffs []string
				exactDiffs(read(t, tt.got), read(t, tt.want), "$", &diffs)
				if got := len(diffs) > 0; got != tt.refused {
					t.Errorf("exactDiffs found %d differences (%v), want refused=%t", len(diffs), diffs, tt.refused)
				}
			})
		}
	})

	t.Run("value comparison ignores member order at every level", func(t *testing.T) {
		t.Parallel()
		var diffs []string
		valueDiffs(read(t, `{"o":{"y":{"b":2,"a":1}},"x":[1,2]}`), read(t, `{"x":[1,2],"o":{"y":{"a":1,"b":2}}}`), "$", &diffs)
		if len(diffs) != 0 {
			t.Errorf("valueDiffs refused a reordering: %v", diffs)
		}
		diffs = nil
		valueDiffs(read(t, `{"a":1}`), read(t, `{"a":2}`), "$", &diffs)
		if len(diffs) == 0 {
			t.Error("valueDiffs accepted differing values")
		}
	})

	t.Run("latency bounds", func(t *testing.T) {
		t.Parallel()
		tests := map[string]struct {
			text string
			ok   bool
		}{
			"success: a short latency":    {text: "0.001", ok: true},
			"success: a long latency":     {text: "119.9", ok: true},
			"error: zero":                 {text: "0", ok: false},
			"error: negative":             {text: "-1", ok: false},
			"error: two minutes":          {text: "120", ok: false},
			"error: far beyond the bound": {text: "500", ok: false},
			"error: not a number":         {text: "soon", ok: false},
		}
		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				if got := latencyBoundsOK(tt.text); got != tt.ok {
					t.Errorf("latencyBoundsOK(%q) = %t, want %t", tt.text, got, tt.ok)
				}
			})
		}
	})

	t.Run("usage requires a latency member and its bounds", func(t *testing.T) {
		t.Parallel()
		want := read(t, `{"input_tokens":1,"output_tokens":2}`)
		var diffs []string
		usageDiffs(read(t, `{"input_tokens":1,"output_tokens":2,"latency":1.5}`), want, &diffs)
		if len(diffs) != 0 {
			t.Errorf("usageDiffs refused a well-formed usage: %v", diffs)
		}
		diffs = nil
		usageDiffs(read(t, `{"input_tokens":1,"output_tokens":2}`), want, &diffs)
		if len(diffs) == 0 {
			t.Error("usageDiffs accepted a usage without latency")
		}
		diffs = nil
		usageDiffs(read(t, `{"input_tokens":1,"output_tokens":2,"latency":500.0}`), want, &diffs)
		if len(diffs) == 0 {
			t.Error("usageDiffs accepted an out-of-bounds latency")
		}
		diffs = nil
		usageDiffs(read(t, `{"input_tokens":1,"output_tokens":2,"latency":"1.5"}`), want, &diffs)
		if len(diffs) == 0 {
			t.Error("usageDiffs accepted a string latency")
		}
	})

	t.Run("the reference derivation", func(t *testing.T) {
		t.Parallel()
		tests := map[string]struct {
			expected, recorded, provider string
			refused                      bool
		}{
			"success: null members dropped on both sides": {
				expected: `{"a":1,"gone":null}`, recorded: `{"a":1,"other":null}`, provider: "anthropic",
			},
			"success: an integer equals the float it is": {
				expected: `{"created_at":1789727875}`, recorded: `{"created_at":1789727875.0}`, provider: "openai",
			},
			"success: a large integer equals its exact float": {
				expected: `{"v":9007199254740992}`, recorded: `{"v":9007199254740992.0}`, provider: "openai",
			},
			"error: a large integer differs from the rounded float": {
				expected: `{"v":9007199254740993}`, recorded: `{"v":9007199254740992.0}`, provider: "openai",
				refused: true,
			},
			"error: a float differs from the unrounded large integer": {
				expected: `{"v":9007199254740992.0}`, recorded: `{"v":9007199254740993}`, provider: "openai",
				refused: true,
			},
			"error: adjacent float values differ": {
				expected: `{"v":1.0000000000000002}`, recorded: `{"v":1.0}`, provider: "openai",
				refused: true,
			},
			"success: gemini's added and dropped members": {
				expected: `{"id":"","output_text":"x","usage":{"input_tokens":3}}`,
				recorded: `{"usage":{"input_tokens":3,"raw_prompt_token":7,"model_invocation_token_counts":[],"non_grounding_model_invocation_token_counts":[]}}`,
				provider: "gemini",
			},
			"error: a changed value": {
				expected: `{"a":1}`, recorded: `{"a":2}`, provider: "anthropic",
				refused: true,
			},
			"error: a member only in the reference": {
				expected: `{"a":1,"b":2}`, recorded: `{"a":1}`, provider: "anthropic",
				refused: true,
			},
			"error: gemini's added members are not ignored for another provider": {
				expected: `{"id":"","a":1}`, recorded: `{"a":1}`, provider: "openai",
				refused: true,
			},
		}
		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				var diffs []string
				referenceProviderDiffs(read(t, tt.expected), read(t, tt.recorded), tt.provider, "$", &diffs)
				if got := len(diffs) > 0; got != tt.refused {
					t.Errorf("referenceProviderDiffs found %d differences (%v), want refused=%t", len(diffs), diffs, tt.refused)
				}
			})
		}
	})
}
