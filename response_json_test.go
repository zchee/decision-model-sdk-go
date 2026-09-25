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
	"bytes"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// The byte strings attributed to Python below are the output of
// _spikes/w2.4/python_dump.py (typesafe-sdk-python 0.7.1 at 0ffd094, its own
// .venv, pydantic-core 2.46.5), committed in
// _spikes/w2.4/results/python-dump.txt, probed 2026-09-26 04:54:53 JST (time
// from date).

// marshaler and unmarshaler are encoding/json's Marshaler and Unmarshaler,
// which the root package's tests cannot import (TestSeamImports).
type (
	marshaler   interface{ MarshalJSON() ([]byte, error) }
	unmarshaler interface{ UnmarshalJSON(data []byte) error }
)

// Every type that marshals does so by value and by pointer; the responses
// unmarshal by pointer.
var (
	_ marshaler   = SystemOneResponse{}
	_ marshaler   = (*SystemOneResponse)(nil)
	_ marshaler   = ModelsResponse{}
	_ marshaler   = (*ModelsResponse)(nil)
	_ marshaler   = NoulAnswer{}
	_ marshaler   = ChoiceAnswer{}
	_ marshaler   = ScoreAnswer{}
	_ marshaler   = Answer{}
	_ marshaler   = Answers{}
	_ marshaler   = Usage{}
	_ marshaler   = ModelCard{}
	_ unmarshaler = (*SystemOneResponse)(nil)
	_ unmarshaler = (*ModelsResponse)(nil)
)

// usageView is what a test compares of a usage.
type usageView struct {
	In, Out       uint64
	HasIn, HasOut bool
}

// namedAnswer is one answer's view with its question's name.
type namedAnswer struct {
	Name   string
	Answer answerView
}

// payloadView is what a test compares of a System One response's payload:
// every value its getters return, the answers in the order of All.
type payloadView struct {
	Model   string
	Usage   usageView
	Answers []namedAnswer
}

// payloadOf returns the view of r's payload.
func payloadOf(r *SystemOneResponse) payloadView {
	v := payloadView{Model: r.Model()}
	v.Usage.In, v.Usage.HasIn = r.Usage().InputTokens()
	v.Usage.Out, v.Usage.HasOut = r.Usage().OutputTokens()
	for name, a := range r.Answers().All() {
		v.Answers = append(v.Answers, namedAnswer{name, viewOf(a)})
	}
	return v
}

// metaView is what a test compares of a ResponseMeta.
type metaView struct {
	Status          int
	NilHeader       bool
	NilBody         bool
	RequestID       string
	HasRequestID    bool
	RawBodyAsString string
}

// metaOf returns the view of m.
func metaOf(m ResponseMeta) metaView {
	id, ok := m.RequestID()
	return metaView{m.StatusCode(), m.Header() == nil, m.RawBody() == nil, id, ok, string(m.RawBody())}
}

// emptyMeta is the view of the Meta of a response that did not come from a
// request.
var emptyMeta = metaView{NilHeader: true, NilBody: true}

// marshal returns the MarshalJSON of m, failing the test on an error.
func marshal(t *testing.T, m marshaler) string {
	t.Helper()
	b, err := m.MarshalJSON()
	if err != nil {
		t.Fatalf("%T.MarshalJSON: %v", m, err)
	}
	return string(b)
}

// TestResponseJSONRoundTrip ports
// test_response_serialization_excludes_http_metadata (R5,
// tests/test_responses.py:90-109) for both resources: a response from the
// client, after its answer views have been read, marshals to exactly the
// body the server sent (the upstream body is Python's model_dump_json of
// itself, byte for byte), without the request id or any view; the payload
// reads back into an equal response whose Meta is empty, and the original
// keeps its Meta.
func TestResponseJSONRoundTrip(t *testing.T) {
	const requestID = "req-export"

	t.Run("systemone", func(t *testing.T) {
		body := testsupport.Fixture(t, "result.json") // RESULT
		c := newTestClient(t, replying(http.StatusOK, body, "X-Typesafe-Request-Id", requestID))
		resp, err := c.SystemOne(t.Context(), "text", noulQuestion(t))
		if err != nil {
			t.Fatalf("SystemOne: %v", err)
		}
		// Reading the views must not add them to the payload (the Python
		// SDK caches them as properties that model_dump never sees).
		choices, scores := 0, 0
		for range resp.Answers().Choices() {
			choices++
		}
		for range resp.Answers().Scores() {
			scores++
		}
		if choices == 0 || scores == 0 {
			t.Fatalf("Choices() yielded %d, Scores() %d; want both non-empty", choices, scores)
		}
		if id, ok := resp.Meta().RequestID(); !ok || id != requestID {
			t.Fatalf("RequestID() = %q, %t", id, ok)
		}

		encoded := marshal(t, resp)
		if diff := gocmp.Diff(string(body), encoded); diff != "" {
			t.Errorf("MarshalJSON against the body (-want +got):\n%s", diff)
		}
		if strings.Contains(encoded, requestID) {
			t.Errorf("the payload carries the request id: %s", encoded)
		}
		if byValue := marshal(t, *resp); byValue != encoded {
			t.Errorf("MarshalJSON by value = %s, by pointer %s", byValue, encoded)
		}

		var restored SystemOneResponse
		if err := restored.UnmarshalJSON([]byte(encoded)); err != nil {
			t.Fatalf("UnmarshalJSON: %v", err)
		}
		if diff := gocmp.Diff(payloadOf(resp), payloadOf(&restored)); diff != "" {
			t.Errorf("restored response (-want +got):\n%s", diff)
		}
		if diff := gocmp.Diff(emptyMeta, metaOf(restored.Meta())); diff != "" {
			t.Errorf("restored Meta (-want +got):\n%s", diff)
		}
		want := metaView{Status: http.StatusOK, RequestID: requestID, HasRequestID: true, RawBodyAsString: string(body)}
		if diff := gocmp.Diff(want, metaOf(resp.Meta())); diff != "" {
			t.Errorf("original Meta after the round trip (-want +got):\n%s", diff)
		}
	})

	t.Run("models", func(t *testing.T) {
		// The upstream body, {"models": [{"name": "test", ...}]}, as
		// model_dump_json writes it (the shape of python-dump.txt's
		// models.json line).
		body := []byte(`{"models":[{"name":"test","description":"Test model","release_date":"2026-09-14"}]}`)
		c := newTestClient(t, replying(http.StatusOK, body, "X-Typesafe-Request-Id", requestID))
		resp, err := c.Models().List(t.Context())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		encoded := marshal(t, resp)
		if diff := gocmp.Diff(string(body), encoded); diff != "" {
			t.Errorf("MarshalJSON against the body (-want +got):\n%s", diff)
		}
		if byValue := marshal(t, *resp); byValue != encoded {
			t.Errorf("MarshalJSON by value = %s, by pointer %s", byValue, encoded)
		}
		var restored ModelsResponse
		if err := restored.UnmarshalJSON([]byte(encoded)); err != nil {
			t.Fatalf("UnmarshalJSON: %v", err)
		}
		if diff := gocmp.Diff(cardsOf(resp), cardsOf(&restored)); diff != "" {
			t.Errorf("restored models (-want +got):\n%s", diff)
		}
		if diff := gocmp.Diff(emptyMeta, metaOf(restored.Meta())); diff != "" {
			t.Errorf("restored Meta (-want +got):\n%s", diff)
		}
		if id, ok := resp.Meta().RequestID(); !ok || id != requestID || !bytes.Equal(resp.Meta().RawBody(), body) {
			t.Errorf("original Meta after the round trip: request id %q, %t, body %s", id, ok, resp.Meta().RawBody())
		}
	})
}

// TestResponseJSONFixtures reads every fixture that is not malformed,
// marshals it, reads the payload back and marshals that again. For each
// fixture Go accepts, the payload reads back to the same values, the second
// payload is the first byte for byte (a fixed point), and the payload is
// Python's model_dump_json output byte for byte, except where Appendix B
// says otherwise: a structured legend level keeps its received bytes (R73),
// and the bodies Python refuses or Go refuses.
func TestResponseJSONFixtures(t *testing.T) {
	// python is Python's payload, or "" when it is the fixture's own bytes
	// (python-dump.txt: "equals the fixture: True"; for the floods it prints
	// the length and SHA-256 instead of the 57 KB and 616 KB).
	tests := map[string]struct {
		python string
		// r73 is {Go's bytes, Python's} of a structured level whose
		// received bytes hold an escape: Go's payload is python with the
		// one occurrence of Python's replaced by Go's.
		r73 [2]string
		// pythonRefuses is Go's payload for a body Python refuses.
		pythonRefuses string
		// goRefuses is the field path of a body Go refuses (Appendix B).
		goRefuses string
	}{
		"deviation-big-exp-noul.json": {goRefuses: "answers.spam.noul"}, // Python: "noul":null (inf)
		"deviation-nan-noul.json":     {goRefuses: "."},                 // Python: "noul":null (nan)
		"deviation-nan-unknown.json":  {goRefuses: "."},                 // Python: accepted, NaN in an unknown member
		"deviation-lone-surrogate.json": {
			// Python refuses at ''. The text level holds U+FFFD, written as
			// it is; the structured level keeps its \ud800 escape.
			pythonRefuses: `{"model":"jev-latest","usage":{"input_tokens":1,"output_tokens":1},"answers":{"quality":{"type":"score","score":0.5,"confidence":0.5,"legend":{"0":"bad ` + "\uFFFD" + `","1":{"note":"\ud800"}},"probabilities":{"0":0.5,"1":0.5}}}}`,
		},
		"duplicates.json": {
			python: `{"model":"jev-latest","usage":{"input_tokens":12,"output_tokens":null},"answers":{"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},"spam":{"type":"noul","noul":0.98},"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"fine","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}},"risk":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":{"summary":"low"}},"probabilities":{"0":1.0}}}}`,
		},
		"escaped-member-names.json": {
			python: `{"model":"jev-latest","usage":{"input_tokens":12,"output_tokens":3},"answers":{"spam":{"type":"noul","noul":0.98},"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},"risk":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":{"summary":"duplicated","examples":["charged \"twice\""]}},"probabilities":{"0":1.0}}}}`,
			r73:    [2]string{`{"summ\u0061ry":`, `{"summary":`},
		},
		"escaped-names.json": {
			python: `{"model":"jev-latest","usage":{"input_tokens":40,"output_tokens":6},"answers":{"spécial":{"type":"noul","noul":0.5},"quote\"d":{"type":"noul","noul":0.25},"back\\slash":{"type":"choice","choice":"a","confidence":0.6,"probabilities":{"a":0.6,"b":0.4}},"new\nline":{"type":"noul","noul":0.75},"globe 🌍":{"type":"score","score":1.5,"confidence":0.5,"legend":{"0":"low","1":"mid","2":"high"},"probabilities":{"0":0.0,"1":0.5,"2":0.5}},"sl/ash":{"type":"noul","noul":1.0}}}`,
		},
		"models.json": {},
		"no-answers.json": {
			python: `{"model":"jev-latest","usage":{"input_tokens":1,"output_tokens":0},"answers":{}}`,
		},
		"parity-big-exp-unknown.json": {
			python: `{"model":"jev-latest","usage":{"input_tokens":1,"output_tokens":1},"answers":{"spam":{"type":"noul","noul":0.5}}}`,
		},
		"result-20.json": {},
		"result.json":    {},
		"score-flood-mini.json": {
			python: `{"model":"jev-latest","usage":{"input_tokens":1,"output_tokens":1},"answers":{"empty0":{"type":"score","score":0.0,"confidence":0.0,"legend":{},"probabilities":{}},"empty1":{"type":"score","score":0.0,"confidence":0.0,"legend":{},"probabilities":{}},"empty2":{"type":"score","score":0.0,"confidence":0.0,"legend":{},"probabilities":{}},"empty3":{"type":"score","score":0.0,"confidence":0.0,"legend":{},"probabilities":{}},"empty4":{"type":"score","score":0.0,"confidence":0.0,"legend":{},"probabilities":{}},"empty5":{"type":"score","score":0.0,"confidence":0.0,"legend":{},"probabilities":{}},"empty6":{"type":"score","score":0.0,"confidence":0.0,"legend":{},"probabilities":{}},"empty7":{"type":"score","score":0.0,"confidence":0.0,"legend":{},"probabilities":{}},"one0":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"only"},"probabilities":{"0":1.0}},"one1":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"only"},"probabilities":{"0":1.0}},"one2":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"only"},"probabilities":{"0":1.0}},"one3":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"only"},"probabilities":{"0":1.0}},"one4":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"only"},"probabilities":{"0":1.0}},"one5":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"only"},"probabilities":{"0":1.0}},"one6":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"only"},"probabilities":{"0":1.0}},"one7":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"only"},"probabilities":{"0":1.0}}}}`,
		},
		"structured-legend-flood-10k.json": {}, // len=616421 sha256=1e3b65a3…
		"structured-legend-flood-1k.json":  {}, // len=57418 sha256=f6b478fa…
		"structured-legend.json": {
			python: `{"model":"custom","usage":{"input_tokens":1,"output_tokens":1},"answers":{"risk":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":{"summary":"duplicated","examples":["charged twice"]}},"probabilities":{"0":1.0}}}}`,
		},
		"type-last.json": {
			// result.json's payload: the members in schema order.
			python: `{"model":"jev-latest","usage":{"input_tokens":12,"output_tokens":3},"answers":{"spam":{"type":"noul","noul":0.98},"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"ok","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}}}}`,
		},
		"unknown-answer-type.json": {
			python: `{"model":"test","usage":{"input_tokens":1,"output_tokens":1},"answers":{"spam":{"type":"noul","noul":0.9}}}`,
		},
	}

	// Every fixture that is not malformed has a row, so a new one cannot
	// escape the round trip.
	var names []string
	for _, name := range testsupport.FixtureNames(t, "*.json") {
		if !strings.HasPrefix(name, "malformed-") {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if diff := gocmp.Diff(slices.Sorted(maps.Keys(tests)), names); diff != "" {
		t.Fatalf("fixtures without a row, or rows without a fixture (-rows +fixtures):\n%s", diff)
	}

	accepted := 0
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := testsupport.Fixture(t, name)
			want := tt.python
			switch {
			case tt.pythonRefuses != "":
				want = tt.pythonRefuses
			case want == "":
				want = string(body)
			case tt.r73 != [2]string{}:
				if n := strings.Count(want, tt.r73[1]); n != 1 {
					t.Fatalf("Python's payload holds %q %d times, want once", tt.r73[1], n)
				}
				want = strings.Replace(want, tt.r73[1], tt.r73[0], 1)
			}

			if name == "models.json" {
				var first, second ModelsResponse
				if err := first.UnmarshalJSON(body); err != nil {
					t.Fatalf("UnmarshalJSON(fixture): %v", err)
				}
				payload := marshal(t, first)
				if diff := gocmp.Diff(want, payload); diff != "" {
					t.Errorf("payload (-want +got):\n%s", diff)
				}
				if err := second.UnmarshalJSON([]byte(payload)); err != nil {
					t.Fatalf("UnmarshalJSON(payload): %v", err)
				}
				if diff := gocmp.Diff(cardsOf(&first), cardsOf(&second)); diff != "" {
					t.Errorf("read back (-want +got):\n%s", diff)
				}
				if again := marshal(t, second); again != payload {
					t.Errorf("second payload differs:\n%s\n%s", again, payload)
				}
				accepted++
				return
			}

			var first SystemOneResponse
			err := first.UnmarshalJSON(body)
			if tt.goRefuses != "" {
				if rve := validationError(t, err); rve.FieldPath != tt.goRefuses {
					t.Errorf("FieldPath = %q, want %q", rve.FieldPath, tt.goRefuses)
				}
				return
			}
			if err != nil {
				t.Fatalf("UnmarshalJSON(fixture): %v", err)
			}
			payload := marshal(t, first)
			if diff := gocmp.Diff(want, payload); diff != "" {
				t.Errorf("payload (-want +got):\n%s", diff)
			}
			var second SystemOneResponse
			if err := second.UnmarshalJSON([]byte(payload)); err != nil {
				t.Fatalf("UnmarshalJSON(payload): %v", err)
			}
			if diff := gocmp.Diff(payloadOf(&first), payloadOf(&second)); diff != "" {
				t.Errorf("read back (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(emptyMeta, metaOf(second.Meta())); diff != "" {
				t.Errorf("read back Meta (-want +got):\n%s", diff)
			}
			if again := marshal(t, second); again != payload {
				t.Errorf("second payload differs from the first:\n%s\n%s", again, payload)
			}
			accepted++
		})
	}
	// 18 fixtures: 15 read back, 3 refused by Go (Appendix B).
	if accepted != 15 {
		t.Errorf("%d fixtures round-tripped, want 15", accepted)
	}
}

// TestAnswerJSONShapes ports test_answer_attributes_and_dictionary_types
// (R12, tests/test_responses.py:218-229): each answer type marshals to
// Python's model_dump_json of the same answer, byte for byte, with the
// answers of test_answer_fields_are_frozen's parameters (R14) and
// test_response_preserves_nested_json (R11) besides, and Usage and
// ModelCard as Python's Usage and ModelMetadata. The Go port has no public
// constructor for an answer, so the answers come from a payload. Answer
// writes its kind's bytes, Answers the answers member, and the zero values
// write the shapes of their Python defaults (ruling R80).
func TestAnswerJSONShapes(t *testing.T) {
	const payload = `{"model":"test","usage":{"input_tokens":1,"output_tokens":1},"answers":{` +
		`"noul":{"type":"noul","noul":0.98},` +
		`"choice":{"type":"choice","choice":"billing","confidence":0.9,"probabilities":{"billing":0.9,"support":0.1}},` +
		`"sure_choice":{"type":"choice","choice":"billing","confidence":1,"probabilities":{"billing":1}},` +
		`"score":{"type":"score","score":0,"confidence":1,"legend":{"0":"bad"},"probabilities":{"0":1}},` +
		`"nested":{"type":"score","score":0,"confidence":1,"legend":{"0":{"examples":["a",{"note":null}]}},"probabilities":{"0":1}}}}`
	var resp SystemOneResponse
	if err := resp.UnmarshalJSON([]byte(payload)); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	answers := resp.Answers()
	noul, _ := answers.Noul("noul")
	choice, _ := answers.Choice("choice")
	sureChoice, _ := answers.Choice("sure_choice")
	score, _ := answers.Score("score")
	nested, _ := answers.Score("nested")
	answer := func(name string) Answer {
		a, ok := answers.Get(name)
		if !ok {
			t.Fatalf("no answer %q", name)
		}
		return a
	}
	var empty SystemOneResponse
	if err := empty.UnmarshalJSON([]byte(`{"model":"test","usage":{}}`)); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	var result SystemOneResponse
	if err := result.UnmarshalJSON(testsupport.Fixture(t, "result.json")); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	var models ModelsResponse
	if err := models.UnmarshalJSON(testsupport.Fixture(t, "models.json")); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}

	const (
		pyNoul       = `{"type":"noul","noul":0.98}`                                                                                             // R12 noul
		pyChoice     = `{"type":"choice","choice":"billing","confidence":0.9,"probabilities":{"billing":0.9,"support":0.1}}`                     // R12 choice
		pySureChoice = `{"type":"choice","choice":"billing","confidence":1.0,"probabilities":{"billing":1.0}}`                                   // R14 choice
		pyScore      = `{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"bad"},"probabilities":{"0":1.0}}`                            // R14 score
		pyNested     = `{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":{"examples":["a",{"note":null}]}},"probabilities":{"0":1.0}}` // R11 structured score
	)
	tests := map[string]struct {
		value marshaler
		want  string
	}{
		"success: NoulAnswer(noul=0.98)":                          {noul, pyNoul},
		"success: ChoiceAnswer(choice=billing, confidence=0.9)":   {choice, pyChoice},
		"success: ChoiceAnswer(confidence=1.0) keeps .0":          {sureChoice, pySureChoice},
		"success: ScoreAnswer(score=0.0, legend={0: bad})":        {score, pyScore},
		"success: ScoreAnswer with a nested legend level":         {nested, pyNested},
		"success: Answer of a noul writes its kind's bytes":       {answer("noul"), pyNoul},
		"success: Answer of a choice writes its kind's bytes":     {answer("choice"), pyChoice},
		"success: Answer of a score writes its kind's bytes":      {answer("nested"), pyNested},
		"success: Answers is the answers member, in order of All": {answers, `{"noul":` + pyNoul + `,"choice":` + pyChoice + `,"sure_choice":` + pySureChoice + `,"score":` + pyScore + `,"nested":` + pyNested + `}`},
		"success: SystemOneResponse(model=test, usage=Usage())":   {empty, `{"model":"test","usage":{"input_tokens":null,"output_tokens":null},"answers":{}}`},
		"success: ListModelsResponse(models=())":                  {ModelsResponse{}, `{"models":[]}`},
		"success: zero NoulAnswer":                                {NoulAnswer{}, `{"type":"noul","noul":0.0}`},
		"success: zero ChoiceAnswer":                              {ChoiceAnswer{}, `{"type":"choice","choice":"","confidence":0.0,"probabilities":{}}`},
		"success: zero ScoreAnswer":                               {ScoreAnswer{}, `{"type":"score","score":0.0,"confidence":0.0,"legend":{},"probabilities":{}}`},
		"success: zero Answer is none of the kinds":               {Answer{}, `null`},
		"success: zero Answers":                                   {Answers{}, `{}`},
		"success: zero SystemOneResponse":                         {SystemOneResponse{}, `{"model":"","usage":{"input_tokens":null,"output_tokens":null},"answers":{}}`},
		"success: Usage(input_tokens=12, output_tokens=3)":        {result.Usage(), `{"input_tokens":12,"output_tokens":3}`},
		"success: Usage() writes null counts":                     {empty.Usage(), `{"input_tokens":null,"output_tokens":null}`},
		"success: ModelMetadata(name=jev-latest, …)":              {models.Models()[0], `{"name":"jev-latest","description":"Fast model","release_date":"2026-08-01"}`},
		"success: zero Usage":                                     {Usage{}, `{"input_tokens":null,"output_tokens":null}`},
		"success: zero ModelCard":                                 {ModelCard{}, `{"name":"","description":"","release_date":""}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := gocmp.Diff(tt.want, marshal(t, tt.value)); diff != "" {
				t.Errorf("MarshalJSON (-want +got):\n%s", diff)
			}
		})
	}
}

// TestResponseUnmarshalJSON pins what UnmarshalJSON does beyond the round
// trip: a payload the decoder refuses fails with a *ResponseValidationError
// that names the field and nothing of an HTTP response, and leaves the
// response as it was; null changes nothing; an answer of an unknown type is
// left out; the result keeps no reference to the payload; and a response
// from the client loses its Meta when it reads a payload.
func TestResponseUnmarshalJSON(t *testing.T) {
	result := testsupport.Fixture(t, "result.json")
	models := testsupport.Fixture(t, "models.json")

	errorTests := map[string]struct {
		data    string
		models  bool
		wantErr string
	}{
		"error: a noul without its value (R1)": {
			data:    `{"model":"test","usage":{"input_tokens":1,"output_tokens":1},"answers":{"n":{"type":"noul"}}}`,
			wantErr: "Invalid response data at 'answers.n.noul'.",
		},
		"error: no model (R1)": {
			data:    `{"usage":{"input_tokens":1,"output_tokens":1},"answers":{}}`,
			wantErr: "Invalid response data at 'model'.",
		},
		"error: not JSON": {
			data:    `{"model":`,
			wantErr: "Invalid response data at '.'.",
		},
		"error: data after the object": {
			data:    string(result) + " x",
			wantErr: "Invalid response data at '.'.",
		},
		"error: empty": {
			wantErr: "Invalid response data at '.'.",
		},
		"error: a model card without its description": {
			data:    `{"models":[{"name":"a","release_date":"b"}]}`,
			models:  true,
			wantErr: "Invalid response data at 'models[0].description'.",
		},
		"error: no models": {
			data:    `{}`,
			models:  true,
			wantErr: "Invalid response data at 'models'.",
		},
	}
	for name, tt := range errorTests {
		t.Run(name, func(t *testing.T) {
			var (
				before, after string
				err           error
			)
			if tt.models {
				var r ModelsResponse
				if err := r.UnmarshalJSON(models); err != nil {
					t.Fatal(err)
				}
				before = marshal(t, r)
				err = r.UnmarshalJSON([]byte(tt.data))
				after = marshal(t, r)
			} else {
				var r SystemOneResponse
				if err := r.UnmarshalJSON(result); err != nil {
					t.Fatal(err)
				}
				before = marshal(t, r)
				err = r.UnmarshalJSON([]byte(tt.data))
				after = marshal(t, r)
			}
			rve := validationError(t, err)
			got := []any{rve.Error(), rve.StatusCode, rve.Header == nil, rve.Body == nil, rve.Endpoint}
			if diff := gocmp.Diff([]any{tt.wantErr, 0, true, true, ""}, got); diff != "" {
				t.Errorf("error, status, nil header, nil body, endpoint (-want +got):\n%s", diff)
			}
			if after != before {
				t.Errorf("the refused payload changed the response:\n%s\n%s", before, after)
			}
		})
	}

	t.Run("success: null leaves the responses unchanged", func(t *testing.T) {
		var r SystemOneResponse
		var m ModelsResponse
		if err := r.UnmarshalJSON(result); err != nil {
			t.Fatal(err)
		}
		if err := m.UnmarshalJSON(models); err != nil {
			t.Fatal(err)
		}
		if err := r.UnmarshalJSON([]byte("null")); err != nil {
			t.Errorf("SystemOneResponse.UnmarshalJSON(null) = %v", err)
		}
		if err := m.UnmarshalJSON([]byte("null")); err != nil {
			t.Errorf("ModelsResponse.UnmarshalJSON(null) = %v", err)
		}
		if got := marshal(t, r); got != string(result) {
			t.Errorf("after null: %s", got)
		}
		if got := marshal(t, m); got != string(models) {
			t.Errorf("after null: %s", got)
		}
	})

	t.Run("success: an answer of an unknown type is left out", func(t *testing.T) {
		var r SystemOneResponse
		if err := r.UnmarshalJSON(testsupport.Fixture(t, "unknown-answer-type.json")); err != nil {
			t.Fatal(err)
		}
		var names []string
		for name := range r.Answers().All() {
			names = append(names, name)
		}
		if diff := gocmp.Diff([]string{"spam"}, names); diff != "" {
			t.Errorf("answers (-want +got):\n%s", diff)
		}
	})

	t.Run("success: the response keeps no reference to the payload", func(t *testing.T) {
		for _, name := range []string{"result.json", "structured-legend.json", "escaped-names.json", "models.json"} {
			data := bytes.Clone(testsupport.Fixture(t, name))
			var (
				r    marshaler
				read func([]byte) error
			)
			if name == "models.json" {
				m := new(ModelsResponse)
				r, read = m, m.UnmarshalJSON
			} else {
				s := new(SystemOneResponse)
				r, read = s, s.UnmarshalJSON
			}
			if err := read(data); err != nil {
				t.Fatalf("%s: UnmarshalJSON: %v", name, err)
			}
			want := marshal(t, r)
			for i := range data {
				data[i] = 'x'
			}
			if got := marshal(t, r); got != want {
				t.Errorf("%s: the payload changed with the caller's bytes:\n%s\n%s", name, want, got)
			}
		}
	})

	t.Run("success: a response from the client loses its Meta", func(t *testing.T) {
		c := newTestClient(t, replying(http.StatusOK, result, "X-Typesafe-Request-Id", "req-1"))
		resp, err := c.SystemOne(t.Context(), "text", noulQuestion(t))
		if err != nil {
			t.Fatalf("SystemOne: %v", err)
		}
		if err := resp.UnmarshalJSON(testsupport.Fixture(t, "no-answers.json")); err != nil {
			t.Fatal(err)
		}
		if diff := gocmp.Diff(emptyMeta, metaOf(resp.Meta())); diff != "" {
			t.Errorf("Meta (-want +got):\n%s", diff)
		}
		if resp.Answers().Len() != 0 {
			t.Errorf("Answers().Len() = %d, want 0", resp.Answers().Len())
		}
	})
}

// TestStdlibJSON checks the responses through a caller's encoding/json
// (ruling R80): json.Marshal of a response by value and by pointer, and as
// a struct field of either kind, gives the MarshalJSON bytes, as it does for
// every other type that marshals; json.Unmarshal reaches UnmarshalJSON, and
// null leaves a field as it was. ResponseMeta is HTTP metadata with no
// payload of its own, and marshals as an empty object.
func TestStdlibJSON(t *testing.T) {
	body := testsupport.Fixture(t, "result.json")
	c := newTestClient(t, replying(http.StatusOK, body, "X-Typesafe-Request-Id", "req-std"))
	resp, err := c.SystemOne(t.Context(), "text", noulQuestion(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	var models ModelsResponse
	if err := models.UnmarshalJSON(testsupport.Fixture(t, "models.json")); err != nil {
		t.Fatal(err)
	}
	noul, _ := resp.Answers().Noul("spam")
	choice, _ := resp.Answers().Choice("tone")
	score, _ := resp.Answers().Score("quality")
	answer, _ := resp.Answers().Get("tone")

	tests := map[string]struct {
		value any
		want  string
	}{
		"success: a response by value":   {*resp, string(body)},
		"success: a response by pointer": {resp, string(body)},
		"success: responses as struct fields": {
			struct {
				ByValue   SystemOneResponse
				ByPointer *SystemOneResponse
				Models    ModelsResponse
			}{*resp, resp, models},
			`{"ByValue":` + string(body) + `,"ByPointer":` + string(body) + `,"Models":` + string(testsupport.Fixture(t, "models.json")) + `}`,
		},
		"success: answers, usage and a card as struct fields": {
			struct {
				Noul    NoulAnswer
				Choice  ChoiceAnswer
				Score   ScoreAnswer
				Answer  Answer
				Answers Answers
				Usage   Usage
				Card    ModelCard
			}{noul, choice, score, answer, resp.Answers(), resp.Usage(), models.Models()[0]},
			`{"Noul":` + marshal(t, noul) + `,"Choice":` + marshal(t, choice) + `,"Score":` + marshal(t, score) +
				`,"Answer":` + marshal(t, choice) + `,"Answers":` + marshal(t, resp.Answers()) +
				`,"Usage":{"input_tokens":12,"output_tokens":3}` +
				`,"Card":{"name":"jev-latest","description":"Fast model","release_date":"2026-08-01"}}`,
		},
		"success: ResponseMeta has no payload": {resp.Meta(), `{}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := testsupport.StdlibMarshal(tt.value)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			if diff := gocmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("json.Marshal (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("success: json.Unmarshal reads a payload, and null keeps a field", func(t *testing.T) {
		var dst struct {
			Response SystemOneResponse
			Pointer  *SystemOneResponse
			Models   ModelsResponse
			Kept     SystemOneResponse
		}
		if err := dst.Kept.UnmarshalJSON(body); err != nil {
			t.Fatal(err)
		}
		data := `{"Response":` + string(body) + `,"Pointer":` + string(body) + `,"Models":` + string(testsupport.Fixture(t, "models.json")) + `,"Kept":null}`
		if err := testsupport.StdlibUnmarshal([]byte(data), &dst); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		want := payloadOf(resp)
		got := []payloadView{payloadOf(&dst.Response), payloadOf(dst.Pointer), payloadOf(&dst.Kept)}
		if diff := gocmp.Diff([]payloadView{want, want, want}, got); diff != "" {
			t.Errorf("read back (-want +got):\n%s", diff)
		}
		if diff := gocmp.Diff(cardsOf(&models), cardsOf(&dst.Models)); diff != "" {
			t.Errorf("models (-want +got):\n%s", diff)
		}
	})

	t.Run("error: json.Unmarshal returns UnmarshalJSON's error", func(t *testing.T) {
		var dst struct{ Response SystemOneResponse }
		err := testsupport.StdlibUnmarshal([]byte(`{"Response":{"model":"m","usage":{},"answers":{"n":{"type":"noul"}}}}`), &dst)
		if rve := validationError(t, err); rve.FieldPath != "answers.n.noul" {
			t.Errorf("FieldPath = %q", rve.FieldPath)
		}
	})
}
