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
	"errors"
	"maps"
	"net/http"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// newTestClient builds a client over rt, the test's transport
// (WithRoundTripper), with testKey (the upstream tests' key, 8 bytes long,
// so the checks that look for the key inside other text apply to it, ruling
// R68) and opts, after clearing the variables a client reads, so a
// developer's TYPESAFE_API_KEY never reaches a test. The client is closed
// when the test ends.
//
// Its calls make one attempt each (NoRetry), as the upstream tests' clients
// do unless a test asks for retries (tests/conftest.py:34-35, ruling R88b);
// a test that wants the production policy passes WithRetry(DefaultRetry())
// in opts, which comes later and wins.
func newTestClient(t *testing.T, rt http.RoundTripper, opts ...ClientOption) *Client {
	t.Helper()
	clearEnv(t)
	c, err := NewClient(append([]ClientOption{WithAPIKey(testKey), WithRoundTripper(rt), WithRetry(NoRetry())}, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// replying returns a Recorder that answers every request with status, a
// Content-Type of application/json, the headers kv (name, value, ...) and
// body.
func replying(status int, body []byte, kv ...string) *testsupport.Recorder {
	reply := testsupport.JSON(status, body)
	for i := 0; i+1 < len(kv); i += 2 {
		reply.Header.Add(kv[i], kv[i+1])
	}
	return &testsupport.Recorder{Replies: []testsupport.Reply{reply}}
}

// onlyRequest returns the one request rec recorded.
func onlyRequest(t *testing.T, rec *testsupport.Recorder) testsupport.RecordedRequest {
	t.Helper()
	reqs := rec.Requests()
	if len(reqs) != 1 {
		t.Fatalf("the transport saw %d requests, want 1", len(reqs))
	}
	return reqs[0]
}

// mustPrepared prepares qs, failing the test when Prepare fails.
func mustPrepared(t testing.TB, qs *Questions) *Prepared {
	t.Helper()
	p, err := qs.Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return p
}

// q3Questions is the NF3 question set: the three questions of the upstream
// round-trip test (tests/test_clients.py:60-80), which result.json answers.
func q3Questions(t testing.TB) *Prepared {
	t.Helper()
	return mustPrepared(t, NewQuestions().
		Noul("spam", Noul{Instructions: Text("Spam?")}).
		Choice("tone", Choice{Instructions: Text("Tone?"), Options: Options{{Label: "friendly"}, {Label: "hostile"}}}).
		Score("quality", Score{Instructions: Text("Quality?"), Levels: []Content{Text("bad"), Text("ok"), Text("great")}}))
}

// answerView is what a test compares of an answer: its kind and every value
// its getters return.
type answerView struct {
	Kind          AnswerKind
	Noul          float64
	Choice        string
	Confidence    float64
	Score         float64
	Probabilities map[string]float64
	Levels        map[uint32]float64
	Legend        map[uint32]string
}

// viewOf returns the view of a.
func viewOf(a Answer) answerView {
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

// upstreamAnswers is RESULT's answers (tests/test_clients.py:43-56) as the
// getters return them.
var upstreamAnswers = map[string]answerView{
	"spam": {Kind: KindNoul, Noul: 0.98},
	"tone": {
		Kind: KindChoice, Choice: "friendly", Confidence: 0.9,
		Probabilities: map[string]float64{"friendly": 0.9, "hostile": 0.1},
	},
	"quality": {
		Kind: KindScore, Score: 1.7, Confidence: 0.8,
		Levels: map[uint32]float64{0: 0.1, 1: 0.1, 2: 0.8},
		Legend: map[uint32]string{0: "bad", 1: "ok", 2: "great"},
	},
}

// TestSystemOneRoundTrip ports test_round_trip (C1, AC-F2): the three
// questions of the upstream test, built as typed questions, as raw ones and
// mixed, are posted to /v1/systemone with the state and the default model
// as a JSON body, and the upstream RESULT comes back as the answers, the
// model and the usage. Upstream compares the parsed body; the bytes are
// compared here, since the SDK writes the members of a typed question in its
// field order and a raw question's after "type" in key order (ruling R38).
func TestSystemOneRoundTrip(t *testing.T) {
	criteria := []Content{Text("bad"), Text("ok"), Text("great")}
	rawSpam := RawQuestion{Type: "noul", Fields: map[string]any{"instructions": "Spam?"}}
	rawTone := RawQuestion{Type: "choice", Fields: map[string]any{"instructions": "Tone?", "criteria": map[string]any{"friendly": nil, "hostile": nil}}}
	rawQuality := RawQuestion{Type: "score", Fields: map[string]any{"instructions": "Quality?", "criteria": []any{"bad", "ok", "great"}}}
	typedTone := Choice{Instructions: Text("Tone?"), Options: Options{{Label: "friendly"}, {Label: "hostile"}}}
	typedQuality := Score{Instructions: Text("Quality?"), Levels: criteria}

	const (
		typedSpamJSON    = `"spam":{"type":"noul","instructions":"Spam?"}`
		typedToneJSON    = `"tone":{"type":"choice","instructions":"Tone?","criteria":{"friendly":null,"hostile":null}}`
		typedQualityJSON = `"quality":{"type":"score","instructions":"Quality?","criteria":["bad","ok","great"]}`
		rawToneJSON      = `"tone":{"type":"choice","criteria":{"friendly":null,"hostile":null},"instructions":"Tone?"}`
		rawQualityJSON   = `"quality":{"type":"score","criteria":["bad","ok","great"],"instructions":"Quality?"}`
		prefix           = `{"state":{"document":"Hello 🌍"},"model":"jev-latest","questions":{`
	)
	tests := map[string]struct {
		questions *Questions
		wantBody  string
	}{
		"success: typed questions (upstream dataclass)": {
			questions: NewQuestions().Noul("spam", Noul{Instructions: Text("Spam?")}).Choice("tone", typedTone).Score("quality", typedQuality),
			wantBody:  prefix + typedSpamJSON + "," + typedToneJSON + "," + typedQualityJSON + "}}",
		},
		"success: raw questions": {
			questions: NewQuestions().Raw("spam", rawSpam).Raw("tone", rawTone).Raw("quality", rawQuality),
			wantBody:  prefix + typedSpamJSON + "," + rawToneJSON + "," + rawQualityJSON + "}}",
		},
		"success: mixed, a raw noul among typed questions": {
			questions: NewQuestions().Raw("spam", rawSpam).Choice("tone", typedTone).Score("quality", typedQuality),
			wantBody:  prefix + typedSpamJSON + "," + typedToneJSON + "," + typedQualityJSON + "}}",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"))
			c := newTestClient(t, rec)
			resp, err := c.SystemOne(t.Context(), map[string]any{"document": "Hello 🌍"}, mustPrepared(t, tt.questions))
			if err != nil {
				t.Fatalf("SystemOne: %v", err)
			}

			req := onlyRequest(t, rec)
			if diff := gocmp.Diff(http.MethodPost, req.Method); diff != "" {
				t.Errorf("method (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff("https://api.typesafe.ai/v1/systemone", req.URL); diff != "" {
				t.Errorf("URL (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(tt.wantBody, string(req.Body)); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(jsonContentType, req.Header.Get("Content-Type")); diff != "" {
				t.Errorf("Content-Type (-want +got):\n%s", diff)
			}

			if diff := gocmp.Diff("jev-latest", resp.Model()); diff != "" {
				t.Errorf("Model (-want +got):\n%s", diff)
			}
			in, inOK := resp.Usage().InputTokens()
			out, outOK := resp.Usage().OutputTokens()
			if diff := gocmp.Diff([]any{uint64(12), true, uint64(3), true}, []any{in, inOK, out, outOK}); diff != "" {
				t.Errorf("Usage (-want +got):\n%s", diff)
			}
			got := map[string]answerView{}
			var order []string
			for name, a := range resp.Answers().All() {
				got[name] = viewOf(a)
				order = append(order, name)
			}
			if diff := gocmp.Diff(upstreamAnswers, got); diff != "" {
				t.Errorf("answers (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff([]string{"spam", "tone", "quality"}, order); diff != "" {
				t.Errorf("answer order (-want +got):\n%s", diff)
			}
			if resp.Answers().Len() != 3 {
				t.Errorf("Answers().Len() = %d, want 3", resp.Answers().Len())
			}
			// The Python SDK's nouls, choices and scores views.
			groups := map[string][]string{}
			for name := range resp.Answers().Nouls() {
				groups["nouls"] = append(groups["nouls"], name)
			}
			for name := range resp.Answers().Choices() {
				groups["choices"] = append(groups["choices"], name)
			}
			for name := range resp.Answers().Scores() {
				groups["scores"] = append(groups["scores"], name)
			}
			if diff := gocmp.Diff(map[string][]string{"nouls": {"spam"}, "choices": {"tone"}, "scores": {"quality"}}, groups); diff != "" {
				t.Errorf("groups (-want +got):\n%s", diff)
			}
			if n, ok := resp.Answers().Noul("spam"); !ok || n.Noul() != 0.98 {
				t.Errorf(`Answers().Noul("spam") = %v, %t, want 0.98, true`, n.Noul(), ok)
			}
			if _, ok := resp.Answers().Noul("tone"); ok {
				t.Errorf(`Answers().Noul("tone") reports a choice answer as a noul`)
			}
			if p, ok := resp.Answers().Choice("tone"); !ok || p.Choice() != "friendly" {
				t.Errorf(`Answers().Choice("tone") = %q, %t, want "friendly", true`, p.Choice(), ok)
			}
			if s, ok := resp.Answers().Score("quality"); !ok || s.Score() != 1.7 {
				t.Errorf(`Answers().Score("quality") = %v, %t, want 1.7, true`, s.Score(), ok)
			}
		})
	}
}

// TestClosedClientRefusesCalls checks that a call after Close fails with a
// *ConfigError wrapping ErrClientClosed, before anything is sent.
func TestClosedClientRefusesCalls(t *testing.T) {
	rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"))
	c := newTestClient(t, rec)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := c.SystemOne(t.Context(), "x", noulQuestion(t))
	if _, ok := errors.AsType[*ConfigError](err); !ok || !errors.Is(err, ErrClientClosed) {
		t.Fatalf("SystemOne after Close = %v, want a *ConfigError wrapping ErrClientClosed", err)
	}
	if rec.Count() != 0 {
		t.Errorf("the transport saw %d requests after Close, want 0", rec.Count())
	}
}
