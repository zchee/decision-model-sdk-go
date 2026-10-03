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

package decision

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/internal/codec"
	"github.com/zchee/decision-model-sdk-go/internal/engine"
	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
	"github.com/zchee/decision-model-sdk-go/internal/wire"
)

// Citations of the form spikes@<commit>:<path> in this file name files in the
// private spike archive; the opening section of docs/perf/ledger.md says what
// such a citation tells a reader who cannot open the archive.

// The answers of the upstream RESULT body (tests/test_clients.py:42-56), one
// answers member each, for bodies that change one of them.
const (
	spamJSON    = `"spam":{"type":"noul","noul":0.98}`
	toneJSON    = `"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}}`
	qualityJSON = `"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"ok","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}}`
)

// resultWith returns the upstream RESULT body with members as the content
// of its answers object: resultWith(spamJSON, toneJSON, qualityJSON) is
// RESULT itself, testdata/result.json.
func resultWith(members ...string) []byte {
	return []byte(`{"model":"jev-latest","usage":{"input_tokens":12,"output_tokens":3},"answers":{` + strings.Join(members, ",") + `}}`)
}

func spamPrepared(t *testing.T) *Prepared {
	t.Helper()
	return mustPrepared(t, NewQuestions().Noul("spam", Noul{}))
}

// knownResponse is the Go form of the upstream KnownResponse
// (tests/test_pydantic_response_models.py:19-25): the one answer spam. The
// name is tagged, since a field's question name is otherwise the field's
// name as written, "Spam".
type knownResponse struct {
	Spam NoulAnswer `typesafe:"kind=noul;name=spam"`
}

// typedSystemOneResponse is the Go form of the upstream
// TypedSystemOneResponse (:28-43): spam, a tone whose pick is friendly or
// hostile (the upstream Tone's Literal), and an optional missing.
type typedSystemOneResponse struct {
	Spam    NoulAnswer   `typesafe:"kind=noul;name=spam"`
	Tone    ChoiceAnswer `typesafe:"kind=choice;name=tone;options=friendly|hostile"`
	Missing NoulAnswer   `typesafe:"kind=noul;name=missing;optional"`
}

// reviewAnswers types every answer of RESULT, with the question set
// q3Questions builds by hand (tests/test_clients.py:60-80).
type reviewAnswers struct {
	Spam    NoulAnswer   `typesafe:"kind=noul;name=spam;instructions=Spam?"`
	Tone    ChoiceAnswer `typesafe:"kind=choice;name=tone;instructions=Tone?;options=friendly|hostile"`
	Quality ScoreAnswer  `typesafe:"kind=score;name=quality;instructions=Quality?;levels=bad|ok|great"`
}

// typedCmp compares answer values with their unexported wire value and
// presence bit, and interleavedAnswers with its unexported field.
var typedCmp = gocmp.AllowUnexported(NoulAnswer{}, ChoiceAnswer{}, ScoreAnswer{}, interleavedAnswers{})

// TestResultWith pins the helper against the fixture it rebuilds.
func TestResultWith(t *testing.T) {
	if diff := gocmp.Diff(testsupport.FixtureString(t, "result.json"), string(resultWith(spamJSON, toneJSON, qualityJSON))); diff != "" {
		t.Errorf("resultWith(spam, tone, quality) is not result.json (-want +got):\n%s", diff)
	}
}

// TestDecodeAsWithSeparateQuestions ports test_standalone_pydantic_response_model
// (tests/test_pydantic_response_models.py:46-56): questions built by
// hand, {"spam": Noul()}, and the answers decoded into a struct with
// DecodeAs, as response_model=KnownResponse decodes them; a member the
// struct's answer type does not model, explanation, is ignored.
func TestDecodeAsWithSeparateQuestions(t *testing.T) {
	tests := map[string]struct {
		spam string
	}{
		"success: the upstream spam answer":            {spam: spamJSON},
		"success: an extra explanation member in spam": {spam: `"spam":{"type":"noul","noul":0.98,"explanation":"spammy"}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(http.StatusOK, resultWith(tt.spam, toneJSON, qualityJSON))
			c := newTestClient(t, rec)
			resp, err := c.SystemOne(t.Context(), "x", spamPrepared(t))
			if err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			if got := onlyRequest(t, rec).Body; !bytes.Contains(got, []byte(`"questions":{"spam":{"type":"noul"}}`)) {
				t.Errorf("request body %s does not ask the hand-built question set", got)
			}
			got, err := DecodeAs[knownResponse](resp)
			if err != nil {
				t.Fatalf("DecodeAs[knownResponse]: %v", err)
			}
			if got.Spam.Noul() != 0.98 || !got.Spam.Present() {
				t.Errorf("Spam = (noul %v, present %v), want (0.98, true)", got.Spam.Noul(), got.Spam.Present())
			}
			if m := resp.Model(); m != "jev-latest" {
				t.Errorf("Model() = %q, want jev-latest", m)
			}
		})
	}
}

// TestSystemOneDefaultResponse ports test_explicit_default_response_model
// (:59-69). Both upstream parametrizations, response_model=None and
// response_model=SystemOneResponse, are the default response, which is what
// SystemOne returns: the typed struct is a separate step (DecodeAs), not a
// response type.
func TestSystemOneDefaultResponse(t *testing.T) {
	body := testsupport.Fixture(t, "result.json")
	c := newTestClient(t, replying(http.StatusOK, body, "x-typesafe-request-id", "req-default"))
	resp, err := c.SystemOne(t.Context(), "x", spamPrepared(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	nouls := maps.Collect(resp.Answers().Nouls())
	if spam := nouls["spam"]; spam.Noul() != 0.98 || !spam.Present() {
		t.Errorf(`Nouls()["spam"] = (noul %v, present %v), want (0.98, true)`, spam.Noul(), spam.Present())
	}
	if id, ok := resp.Meta().RequestID(); id != "req-default" || !ok {
		t.Errorf("Meta().RequestID() = (%q, %v), want (req-default, true)", id, ok)
	}
	if diff := gocmp.Diff(string(body), string(resp.Meta().RawBody())); diff != "" {
		t.Errorf("Meta().RawBody() (-want +got):\n%s", diff)
	}
}

// TestDecodeAsOptionalFieldAndUnknownAnswer ports
// test_pydantic_system_one_response_subclass (:72-91): a struct with an
// optional field whose answer is absent (Present false, the upstream
// missing is None), a tone restricted to two options, an answer of the
// unknown type future that the decoder drops with a WARN line, the full
// Answers() beside the typed struct, and the request id and raw body,
// which the two-step form (SystemOne, then DecodeAs) keeps in reach. Ask
// gives the same struct in one step.
func TestDecodeAsOptionalFieldAndUnknownAnswer(t *testing.T) {
	body := resultWith(spamJSON, toneJSON, qualityJSON, `"future":{"type":"future","value":1}`)
	logs := testsupport.NewLogRecorder(slog.LevelWarn)
	c := newTestClient(t, replying(http.StatusOK, body, "x-typesafe-request-id", "req-pydantic"), WithLogger(logs.Logger()))

	resp, err := c.SystemOne(t.Context(), "x", spamPrepared(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	got, err := DecodeAs[typedSystemOneResponse](resp)
	if err != nil {
		t.Fatalf("DecodeAs[typedSystemOneResponse]: %v", err)
	}
	friendly, ok := got.Tone.Probability("friendly")
	type view struct {
		Spam, Friendly                         float64
		Choice                                 string
		HasFriendly                            bool
		SpamPresent, TonePresent, MissingThere bool
		Missing                                float64
	}
	want := view{Spam: 0.98, Friendly: 0.9, Choice: "friendly", HasFriendly: true, SpamPresent: true, TonePresent: true}
	gotView := view{
		Spam: got.Spam.Noul(), Friendly: friendly, Choice: got.Tone.Choice(), HasFriendly: ok,
		SpamPresent: got.Spam.Present(), TonePresent: got.Tone.Present(), MissingThere: got.Missing.Present(), Missing: got.Missing.Noul(),
	}
	if diff := gocmp.Diff(want, gotView); diff != "" {
		t.Errorf("typed answers (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff(NoulAnswer{}, got.Missing, typedCmp); diff != "" {
		t.Errorf("Missing is not the zero NoulAnswer (-want +got):\n%s", diff)
	}
	// The upstream model_dump holds "missing": None; the struct marshals the
	// absent answer as null and the others as their kinds.
	dump, err := sonic.ConfigFastest.Marshal(got)
	if err != nil {
		t.Fatalf("sonic.ConfigFastest.Marshal(typed struct): %v", err)
	}
	wantDump := `{"Spam":{"type":"noul","noul":0.98},"Tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},"Missing":null}`
	if diff := gocmp.Diff(wantDump, string(dump)); diff != "" {
		t.Errorf("sonic.ConfigFastest.Marshal(typed struct) (-want +got):\n%s", diff)
	}

	answers := resp.Answers()
	if a, _ := answers.Noul("spam"); a.Noul() != 0.98 {
		t.Errorf(`Answers().Noul("spam") = %v, want 0.98`, a.Noul())
	}
	if a, _ := answers.Choice("tone"); a.Choice() != "friendly" {
		t.Errorf(`Answers().Choice("tone") = %q, want friendly`, a.Choice())
	}
	if a, _ := answers.Score("quality"); a.Score() != 1.7 {
		t.Errorf(`Answers().Score("quality") = %v, want 1.7`, a.Score())
	}
	if _, ok := answers.Get("future"); ok || answers.Len() != 3 {
		t.Errorf(`Answers() has future = %v, Len() = %d; want false, 3`, ok, answers.Len())
	}
	if id, ok := resp.Meta().RequestID(); id != "req-pydantic" || !ok {
		t.Errorf("Meta().RequestID() = (%q, %v), want (req-pydantic, true)", id, ok)
	}
	if diff := gocmp.Diff(string(body), string(resp.Meta().RawBody())); diff != "" {
		t.Errorf("Meta().RawBody() (-want +got):\n%s", diff)
	}
	// The upstream model_dump holds neither _raw nor _request_id: the
	// payload leaves out the HTTP response, and the dropped answer.
	payload, err := resp.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if bytes.Contains(payload, []byte("req-pydantic")) || bytes.Contains(payload, []byte("future")) {
		t.Errorf("MarshalJSON() = %s, want neither the request id nor the future answer", payload)
	}
	warns := logs.At(slog.LevelWarn)
	if len(warns) != 1 || warns[0].Message != engine.MsgSkippedAnswer {
		t.Fatalf("WARN records = %v, want one %q", warns, engine.MsgSkippedAnswer)
	}
	for key, want := range map[string]string{"answer": "future", "type": "future"} {
		if v, _ := warns[0].Attr(key); v.String() != want {
			t.Errorf("WARN %s = %q, want %q", key, v.String(), want)
		}
	}

	asked, err := Ask[typedSystemOneResponse](t.Context(), c, "x")
	if err != nil {
		t.Fatalf("Ask[typedSystemOneResponse]: %v", err)
	}
	if diff := gocmp.Diff(got, asked, typedCmp); diff != "" {
		t.Errorf("Ask and SystemOne+DecodeAs differ (-two-step +Ask):\n%s", diff)
	}
}

// TestDecodeAsOptional checks the decode halves of the three optional cases
// (TestPreparedForOptional has the question-set halves): an optional answer
// that is absent leaves the zero answer, whose Present is false; one that
// is there is read, with Present true, as a required one is; and optional
// on a field that is not an answer field makes DecodeAs and Ask fail with
// the *ConfigError of PreparedFor, Ask before any request.
func TestDecodeAsOptional(t *testing.T) {
	type notAnAnswer struct {
		Spam  NoulAnswer `typesafe:"kind=noul;name=spam"`
		Count int        `typesafe:"kind=noul;optional"`
	}
	tests := map[string]struct {
		body        []byte
		decode      func(*SystemOneResponse) (NoulAnswer, error)
		ask         func(*testing.T, *Client) (NoulAnswer, error)
		want        NoulAnswer
		wantConfig  bool
		wantRequest int
	}{
		"success: an optional answer absent is the zero answer, not present": {
			body: resultWith(spamJSON, toneJSON, qualityJSON),
			decode: func(r *SystemOneResponse) (NoulAnswer, error) {
				v, err := DecodeAs[typedSystemOneResponse](r)
				return v.Missing, err
			},
			ask: func(t *testing.T, c *Client) (NoulAnswer, error) {
				v, err := Ask[typedSystemOneResponse](t.Context(), c, "x")
				return v.Missing, err
			},
			wantRequest: 1,
		},
		"success: an optional answer there is read and present": {
			body: resultWith(spamJSON, toneJSON, `"missing":{"type":"noul","noul":0.25}`),
			decode: func(r *SystemOneResponse) (NoulAnswer, error) {
				v, err := DecodeAs[typedSystemOneResponse](r)
				return v.Missing, err
			},
			ask: func(t *testing.T, c *Client) (NoulAnswer, error) {
				v, err := Ask[typedSystemOneResponse](t.Context(), c, "x")
				return v.Missing, err
			},
			want:        NoulAnswer{w: wire.NoulAnswer{Noul: 0.25}, present: true},
			wantRequest: 1,
		},
		"error: optional on a field that is not an answer field": {
			body: resultWith(spamJSON, toneJSON, qualityJSON),
			decode: func(r *SystemOneResponse) (NoulAnswer, error) {
				v, err := DecodeAs[notAnAnswer](r)
				return v.Spam, err
			},
			ask: func(t *testing.T, c *Client) (NoulAnswer, error) {
				v, err := Ask[notAnAnswer](t.Context(), c, "x")
				return v.Spam, err
			},
			wantConfig: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var resp SystemOneResponse
			if err := resp.UnmarshalJSON(tt.body); err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}
			rec := replying(http.StatusOK, tt.body)
			c := newTestClient(t, rec)
			for form, run := range map[string]func() (NoulAnswer, error){
				"DecodeAs": func() (NoulAnswer, error) { return tt.decode(&resp) },
				"Ask":      func() (NoulAnswer, error) { return tt.ask(t, c) },
			} {
				got, err := run()
				if tt.wantConfig {
					var ce *ConfigError
					if !errors.As(err, &ce) || !strings.Contains(err.Error(), "optional applies only to") {
						t.Errorf("%s: err = %v, want the *ConfigError refusing optional on a non-answer field", form, err)
					}
				} else if err != nil {
					t.Errorf("%s: %v", form, err)
				}
				if diff := gocmp.Diff(tt.want, got, typedCmp); diff != "" {
					t.Errorf("%s: the optional field (-want +got):\n%s", form, diff)
				}
			}
			if rec.Count() != tt.wantRequest {
				t.Errorf("the transport saw %d requests, want %d", rec.Count(), tt.wantRequest)
			}
		})
	}
}

// TestDecodeAsReturnsZeroOnFailure pins DecodeAs's godoc: a decode that
// fails after reading some fields returns the zero T, not the fields read
// before the failure, from DecodeAs and from Ask, and leaves the response
// as it was.
func TestDecodeAsReturnsZeroOnFailure(t *testing.T) {
	// spam is read first; tone then picks an option reviewAnswers does not
	// list.
	body := resultWith(spamJSON, `"tone":{"type":"choice","choice":"x","confidence":1,"probabilities":{}}`, qualityJSON)
	tests := map[string]struct {
		decode func(*testing.T, *SystemOneResponse) (reviewAnswers, error)
	}{
		"error: DecodeAs returns the zero T": {
			decode: func(_ *testing.T, r *SystemOneResponse) (reviewAnswers, error) { return DecodeAs[reviewAnswers](r) },
		},
		"error: Ask returns the zero T": {
			decode: func(t *testing.T, _ *SystemOneResponse) (reviewAnswers, error) {
				return Ask[reviewAnswers](t.Context(), newTestClient(t, replying(http.StatusOK, body)), "x")
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var resp SystemOneResponse
			if err := resp.UnmarshalJSON(body); err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}
			before, err := resp.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}
			got, err := tt.decode(t, &resp)
			if !errors.Is(err, errTypedOption) {
				t.Fatalf("err = %v, want the undeclared-option failure", err)
			}
			if diff := gocmp.Diff(reviewAnswers{}, got, typedCmp); diff != "" {
				t.Errorf("the T returned with the error is not the zero T (-want +got):\n%s", diff)
			}
			after, err := resp.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}
			if diff := gocmp.Diff(string(before), string(after)); diff != "" {
				t.Errorf("the response changed (-before +after):\n%s", diff)
			}
		})
	}
}

// TestAskPassesCallOptions checks that Ask hands its call options to
// SystemOne: a Model and a Header reach the request
// that goes out, and without them the request carries the client's model
// and no such header.
func TestAskPassesCallOptions(t *testing.T) {
	tests := map[string]struct {
		opts       []CallOption
		wantModel  string
		wantHeader string
	}{
		"success: Model and Header reach the request": {
			opts:       []CallOption{Model("probe-model"), Header("x-probe", "1")},
			wantModel:  `"model":"probe-model"`,
			wantHeader: "1",
		},
		"success: without options, the client's model and no header": {wantModel: `"model":"jev-latest"`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(http.StatusOK, resultWith(spamJSON, toneJSON, qualityJSON))
			got, err := Ask[reviewAnswers](t.Context(), newTestClient(t, rec), "x", tt.opts...)
			if err != nil {
				t.Fatalf("Ask: %v", err)
			}
			if !got.Spam.Present() || got.Spam.Noul() != 0.98 {
				t.Errorf("Spam = (noul %v, present %v), want (0.98, true)", got.Spam.Noul(), got.Spam.Present())
			}
			req := onlyRequest(t, rec)
			if !bytes.Contains(req.Body, []byte(tt.wantModel)) {
				t.Errorf("request body %s does not carry %s", req.Body, tt.wantModel)
			}
			if h := req.Header.Get("x-probe"); h != tt.wantHeader {
				t.Errorf("request header x-probe = %q, want %q", h, tt.wantHeader)
			}
		})
	}
}

// askFunc runs Ask with one struct type of the test's choosing.
type askFunc func(t *testing.T, c *Client) error

// askAs returns an askFunc that runs Ask[T] as one attempt, passing the
// call option on to SystemOne.
func askAs[T any]() askFunc {
	return func(t *testing.T, c *Client) error {
		_, err := Ask[T](t.Context(), c, "x", Retry(NoRetry()))
		return err
	}
}

// decodeAsFunc runs DecodeAs with one struct type of the test's choosing.
type decodeAsFunc func(*SystemOneResponse) error

// decodeAs returns a decodeAsFunc that runs DecodeAs[T].
func decodeAs[T any]() decodeAsFunc {
	return func(r *SystemOneResponse) error {
		_, err := DecodeAs[T](r)
		return err
	}
}

// TestAskValidationFieldPaths ports test_pydantic_response_validation
// (:94-117) and extends it to every row of DecodeAs's path table. Each
// case asks with Ask over a Recorder that replies with the body and the
// request id req-invalid, and checks the *ResponseValidationError as the
// upstream test does (field path, request id, body) and as the SDK's other
// validation errors carry it (status, endpoint, text). A case the typed
// decode refuses (typed) is also decoded with DecodeAs from the response
// SystemOne returns, which gives the same path without the endpoint.
//
// Paths, next to what the Python SDK reports (probed with
// spikes@815453827b43:w4.2/python_typed_paths.py): T's fields are the answers lifted
// out of "answers", so a typed failure is named as the Python SDK names it
// for a SystemOneResponse subclass with one field per answer, as the
// upstream TypedSystemOneResponse is:
//
//   - The upstream first case, KnownResponse over a body without usage,
//     fails at usage in Go: Ask validates the whole System One response
//     before the struct, as a SystemOneResponse subclass does, and
//     SystemOneResponse requires usage. With usage the failure is the
//     decoder's answers.spam.noul, the upstream path.
//   - The upstream second case is tone.choice, the upstream path.
//   - The rest are that model's paths (spam, spam.type, missing.type): an
//     answer of an unknown type is skipped, as that model skips it, so
//     under a required field's name it fails as absent (spam), and a body
//     without answers fails at the first required field (spam). Python
//     makes the option and level checks only when the model's own types
//     say so.
func TestAskValidationFieldPaths(t *testing.T) {
	// reviewWithFour is reviewAnswers with a fourth level, for a body whose
	// score names level 3.
	type reviewWithFour struct {
		Quality ScoreAnswer `typesafe:"kind=score;name=quality;levels=bad|ok|great|wow"`
	}
	tests := map[string]struct {
		body   []byte
		ask    askFunc
		decode decodeAsFunc // nil: the base decoder refuses the body
		path   string
	}{
		"error: upstream case 1 verbatim: KnownResponse over a body without usage": {
			body: []byte(`{"model":"test","answers":{"spam":{"type":"noul"}}}`),
			ask:  askAs[knownResponse](),
			path: "usage",
		},
		"error: upstream case 1 with usage: spam without noul": {
			body: []byte(`{"model":"test","usage":{},"answers":{"spam":{"type":"noul"}}}`),
			ask:  askAs[knownResponse](),
			path: "answers.spam.noul",
		},
		"error: upstream case 2: tone picks an option T does not list": {
			body:   resultWith(spamJSON, `"tone":{"type":"choice","choice":"unknown","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}}`, qualityJSON),
			ask:    askAs[typedSystemOneResponse](),
			decode: decodeAs[typedSystemOneResponse](),
			path:   "tone.choice",
		},
		"error: a required answer absent": {
			body:   resultWith(toneJSON, qualityJSON),
			ask:    askAs[typedSystemOneResponse](),
			decode: decodeAs[typedSystemOneResponse](),
			path:   "spam",
		},
		"error: a required answer of a type this version does not model": {
			body:   resultWith(`"spam":{"type":"future","value":1}`, toneJSON, qualityJSON),
			ask:    askAs[typedSystemOneResponse](),
			decode: decodeAs[typedSystemOneResponse](),
			path:   "spam",
		},
		"error: no answers member: the first required field in T's order": {
			body:   []byte(`{"model":"jev-latest","usage":{"input_tokens":1,"output_tokens":1}}`),
			ask:    askAs[typedSystemOneResponse](),
			decode: decodeAs[typedSystemOneResponse](),
			path:   "spam",
		},
		"error: the first failing field in T's order, not in the body's": {
			body:   resultWith(`"tone":{"type":"noul","noul":0.5}`, qualityJSON),
			ask:    askAs[typedSystemOneResponse](),
			decode: decodeAs[typedSystemOneResponse](),
			path:   "spam",
		},
		"error: a required answer of another kind": {
			body:   resultWith(`"spam":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}}`, toneJSON, qualityJSON),
			ask:    askAs[typedSystemOneResponse](),
			decode: decodeAs[typedSystemOneResponse](),
			path:   "spam.type",
		},
		"error: an optional answer of another kind": {
			body:   resultWith(spamJSON, toneJSON, `"missing":{"type":"score","score":0,"confidence":1,"legend":{},"probabilities":{}}`),
			ask:    askAs[typedSystemOneResponse](),
			decode: decodeAs[typedSystemOneResponse](),
			path:   "missing.type",
		},
		"error: a probability of an option T does not list": {
			body:   resultWith(spamJSON, `"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.05,"other":0.05}}`, qualityJSON),
			ask:    askAs[typedSystemOneResponse](),
			decode: decodeAs[typedSystemOneResponse](),
			path:   "tone.probabilities.other",
		},
		"error: a legend level past T's three levels": {
			body:   resultWith(spamJSON, toneJSON, `"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"ok","2":"great","3":"wow"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}}`),
			ask:    askAs[reviewAnswers](),
			decode: decodeAs[reviewAnswers](),
			path:   "quality.legend.3",
		},
		"error: a probability of a level past T's three levels": {
			body:   resultWith(spamJSON, toneJSON, `"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"ok","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.7,"3":0.1}}`),
			ask:    askAs[reviewAnswers](),
			decode: decodeAs[reviewAnswers](),
			path:   "quality.probabilities.3",
		},
		"error: the pick is checked before the probabilities": {
			body:   resultWith(spamJSON, `"tone":{"type":"choice","choice":"bad","confidence":1,"probabilities":{"other":1}}`, qualityJSON),
			ask:    askAs[typedSystemOneResponse](),
			decode: decodeAs[typedSystemOneResponse](),
			path:   "tone.choice",
		},
		"error: the legend is checked before the probabilities": {
			body:   resultWith(`"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","5":"x"},"probabilities":{"4":1}}`),
			ask:    askAs[reviewWithFour](),
			decode: decodeAs[reviewWithFour](),
			path:   "quality.legend.5",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(http.StatusOK, tt.body, "x-typesafe-request-id", "req-invalid")
			c := newTestClient(t, rec)
			checkTypedError(t, "Ask", tt.ask(t, c), tt.body, tt.path, systemOneEndpoint)
			if rec.Count() != 1 {
				t.Errorf("the transport saw %d requests, want 1", rec.Count())
			}
			if tt.decode == nil {
				return
			}
			resp, err := c.SystemOne(t.Context(), "x", spamPrepared(t))
			if err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			checkTypedError(t, "DecodeAs", tt.decode(resp), tt.body, tt.path, "")
		})
	}
}

// TestTypedErrorWrapsDecodeError pins what a typed failure wraps: FieldPath
// names the answer in the lifted model's form, while
// the decode error Unwrap returns keeps the answer's place in the body,
// under "answers", and says the response data is invalid (the decoder's
// "invalid response data: …", not a JSON failure's "invalid JSON: …").
// Both entry points wrap the same error.
func TestTypedErrorWrapsDecodeError(t *testing.T) {
	tests := map[string]struct {
		body              []byte
		outer, inner, msg string
	}{
		"error: a pick T does not list": {
			body:  resultWith(spamJSON, `"tone":{"type":"choice","choice":"unknown","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}}`, qualityJSON),
			outer: "tone.choice",
			inner: "answers.tone.choice",
			msg:   "invalid response data: " + errTypedOption.Error(),
		},
		"error: a required answer absent": {
			body:  resultWith(toneJSON, qualityJSON),
			outer: "spam",
			inner: "answers.spam",
			msg:   "invalid response data: " + errTypedMissing.Error(),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var resp SystemOneResponse
			if err := resp.UnmarshalJSON(tt.body); err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}
			c := newTestClient(t, replying(http.StatusOK, tt.body))
			for form, run := range map[string]func() error{
				"DecodeAs": func() error { _, err := DecodeAs[typedSystemOneResponse](&resp); return err },
				"Ask":      func() error { _, err := Ask[typedSystemOneResponse](t.Context(), c, "x"); return err },
			} {
				err := run()
				var rve *ResponseValidationError
				if !errors.As(err, &rve) {
					t.Fatalf("%s: err = %T %v, want *ResponseValidationError", form, err, err)
				}
				inner := errors.Unwrap(rve)
				if reflect.TypeOf(inner) != reflect.TypeFor[*codec.DecodeError]() {
					t.Fatalf("%s: Unwrap = %T, want the *codec.DecodeError itself", form, inner)
				}
				de, _ := errors.AsType[*codec.DecodeError](inner)
				type view struct{ Outer, Inner, Msg string }
				want := view{Outer: tt.outer, Inner: tt.inner, Msg: tt.msg}
				got := view{Outer: rve.FieldPath, Inner: renderFieldPath(de.Path), Msg: de.Error()}
				if diff := gocmp.Diff(want, got); diff != "" {
					t.Errorf("%s: the typed error and the decode error it wraps (-want +got):\n%s", form, diff)
				}
			}
		})
	}
}

// checkTypedError checks that err is the *ResponseValidationError of a 200
// response carrying body and the request id req-invalid, failing at path,
// with endpoint in its text.
func checkTypedError(t *testing.T, form string, err error, body []byte, path, endpoint string) {
	t.Helper()
	var rve *ResponseValidationError
	if !errors.As(err, &rve) {
		t.Fatalf("%s: err = %T %v, want *ResponseValidationError", form, err, err)
	}
	id, _ := rve.RequestID()
	type view struct {
		FieldPath, RequestID, Endpoint, Body, Error string
		StatusCode                                  int
	}
	prefix := ""
	if endpoint != "" {
		prefix = endpoint + ": "
	}
	want := view{
		FieldPath: path, RequestID: "req-invalid", Endpoint: endpoint, Body: string(body), StatusCode: http.StatusOK,
		Error: prefix + "200 Invalid response data at '" + path + "'. (request_id=req-invalid)",
	}
	got := view{FieldPath: rve.FieldPath, RequestID: id, Endpoint: rve.Endpoint, Body: string(rve.Body), StatusCode: rve.StatusCode, Error: rve.Error()}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("%s: *ResponseValidationError (-want +got):\n%s", form, diff)
	}
}

// TestTypedFieldPathEscapesNames checks that a typed failure's path writes
// an answer's name as the decoder writes the names in its own paths
// (renderFieldPath): escaped, without the "answers." the decoder puts
// first. escaped-names.json loses one answer, so the
// field that reads it fails as absent.
func TestTypedFieldPathEscapesNames(t *testing.T) {
	tests := map[string]struct {
		drop string // the answer the body loses, as the fixture spells it
		name string // its name, decoded
	}{
		"error: a name with a newline":   {drop: `"new\nline":{"type":"noul","noul":0.75},`, name: "new\nline"},
		"error: a name with a backslash": {drop: `"back\\slash":{"type":"choice","choice":"a","confidence":0.6,"probabilities":{"a":0.6,"b":0.4}},`, name: `back\slash`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := strings.Replace(testsupport.FixtureString(t, "escaped-names.json"), tt.drop, "", 1)
			var resp SystemOneResponse
			if err := resp.UnmarshalJSON([]byte(body)); err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}
			if _, ok := resp.Answers().Get(tt.name); ok {
				t.Fatalf("the body still answers %q", tt.name)
			}
			_, err := DecodeAs[escapedNamesAnswers](&resp)
			var rve *ResponseValidationError
			if !errors.As(err, &rve) {
				t.Fatalf("DecodeAs: err = %T %v, want *ResponseValidationError", err, err)
			}
			decoders := renderFieldPath(codec.FieldPath{Top: "answers", Name: tt.name, HasName: true})
			want, ok := strings.CutPrefix(decoders, "answers.")
			if !ok || want == tt.name {
				t.Fatalf("the decoder renders %q as %q: want an escaped name after answers.", tt.name, decoders)
			}
			if rve.FieldPath != want {
				t.Errorf("FieldPath = %q, want %q, the decoder's rendering of the name", rve.FieldPath, want)
			}
		})
	}
}

// TestDecodeAsStoredResponse checks the error of a typed decode of a
// response read back from JSON: it has the path and no HTTP metadata, as
// the stored response's own validation errors have none.
func TestDecodeAsStoredResponse(t *testing.T) {
	var resp SystemOneResponse
	if err := resp.UnmarshalJSON(resultWith(toneJSON, qualityJSON)); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	_, err := DecodeAs[reviewAnswers](&resp)
	var rve *ResponseValidationError
	if !errors.As(err, &rve) {
		t.Fatalf("DecodeAs: err = %T %v, want *ResponseValidationError", err, err)
	}
	type view struct {
		FieldPath, Error string
		StatusCode       int
		NilHeader        bool
		NilBody          bool
		Missing          bool
	}
	want := view{FieldPath: "spam", Error: "Invalid response data at 'spam'.", NilHeader: true, NilBody: true, Missing: true}
	got := view{
		FieldPath: rve.FieldPath, Error: rve.Error(), StatusCode: rve.StatusCode,
		NilHeader: rve.Header == nil, NilBody: rve.Body == nil, Missing: errors.Is(err, errTypedMissing),
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("stored response's typed error (-want +got):\n%s", diff)
	}
}

// TestTypedErrorRedactsHeader checks what a typed validation error shows
// of a credential. The response echoes the client's API key in two headers,
// in the request id and in a probability label that T does not list, so the
// decode fails at that label. Ask, which has the client's key, shows "***"
// for it in the header and the request id, and so does DecodeAs over the
// same live response, which keeps its client's redactor. A response read
// back with UnmarshalJSON came from no client: its Meta is empty and it
// keeps no redactor. Both show the label in the field path and in Error as
// it arrived, as the SDK's other errors show a path, and both show
// Set-Cookie, a credential by its name, as "***". Neither changes the
// response's own header.
func TestTypedErrorRedactsHeader(t *testing.T) {
	// tone gives a probability to a label that is the key, which
	// typedSystemOneResponse's options do not list.
	body := resultWith(spamJSON, `"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"`+testKey+`":0.1}}`)
	echo := "echoed " + testKey + " back"
	location := "https://example.test/" + testKey
	requestID := "req-" + testKey
	type view struct {
		FieldPath, Cookie, Echo, Location, RequestID, Endpoint, Error string
	}
	path := "tone.probabilities." + testKey
	liveResponse := func(t *testing.T, c *Client) *SystemOneResponse {
		t.Helper()
		resp, err := c.SystemOne(t.Context(), "x", spamPrepared(t))
		if err != nil {
			t.Fatalf("SystemOne: %v", err)
		}
		return resp
	}
	tests := map[string]struct {
		typed func(*testing.T, *Client) error
		want  view
	}{
		"error: DecodeAs over a live response redacts as Ask does": {
			typed: func(t *testing.T, c *Client) error {
				resp := liveResponse(t, c)
				_, err := DecodeAs[typedSystemOneResponse](resp)
				if got := resp.Meta().Header().Get("Set-Cookie"); got != "session=abc" {
					t.Errorf("the response's own Set-Cookie = %q, want it unchanged", got)
				}
				if got := resp.Meta().Header().Get("X-Echo"); got != echo {
					t.Errorf("the response's own X-Echo = %q, want it unchanged, %q", got, echo)
				}
				return err
			},
			want: view{
				FieldPath: path, Cookie: "***", Echo: "***", Location: "***", RequestID: "***",
				Error: "200 Invalid response data at '" + path + "'. (request_id=***)",
			},
		},
		"error: Ask redacts the header by name and by the client's key": {
			typed: func(t *testing.T, c *Client) error {
				_, err := Ask[typedSystemOneResponse](t.Context(), c, "x")
				return err
			},
			want: view{
				FieldPath: path, Cookie: "***", Echo: "***", Location: "***", RequestID: "***", Endpoint: systemOneEndpoint,
				Error: systemOneEndpoint + ": 200 Invalid response data at '" + path + "'. (request_id=***)",
			},
		},
		"error: a live response read back in place keeps no redactor and no Meta": {
			typed: func(t *testing.T, c *Client) error {
				resp := liveResponse(t, c)
				payload, err := resp.MarshalJSON()
				if err != nil {
					t.Fatalf("MarshalJSON: %v", err)
				}
				if err := resp.UnmarshalJSON(payload); err != nil {
					t.Fatalf("UnmarshalJSON: %v", err)
				}
				if got := resp.redactor(); got != (engine.HeaderRedactor{}) {
					t.Errorf("the redactor after UnmarshalJSON = %+v, want the zero redactor: the response came from no client", got)
				}
				_, err = DecodeAs[typedSystemOneResponse](resp)
				return err
			},
			want: view{FieldPath: path, Error: "Invalid response data at '" + path + "'."},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, replying(http.StatusOK, body, "Set-Cookie", "session=abc", "X-Echo", echo, "Location", location, "x-typesafe-request-id", requestID))
			err := tt.typed(t, c)
			var rve *ResponseValidationError
			if !errors.As(err, &rve) {
				t.Fatalf("err = %T %v, want *ResponseValidationError", err, err)
			}
			id, _ := rve.RequestID()
			got := view{
				FieldPath: rve.FieldPath, Cookie: rve.Header.Get("Set-Cookie"), Echo: rve.Header.Get("X-Echo"), Location: rve.Header.Get("Location"), RequestID: id, Endpoint: rve.Endpoint,
				Error: rve.Error(),
			}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("typed error (-want +got):\n%s", diff)
			}
		})
	}
}

// TestDecodeAsProxyParity pins that DecodeAs over a live
// response redacts its header as Ask redacts the same answer, at every sink
// the error has (Header, RequestID, Error), when the answer went through a
// proxy. A plain-HTTP request through a proxy gets the proxy's own answer,
// which repeats the proxy's credential in two headers and the request id:
// "***" for all three from both. Over HTTPS the proxy only tunnels, and the
// API's header, which here holds the proxy's password, is shown as the
// server sent it by both. A response kept while the client goes through 16
// other proxies, as many as the client's set remembers
// (engine.MaxProxyUserinfos), still redacts its own proxy's credential, which
// the set has forgotten by then: the response keeps a snapshot of the
// credentials as they were when its call returned. The typed decode fails
// at a label T does not list, so each call yields a validation error.
func TestDecodeAsProxyParity(t *testing.T) {
	path := "tone.probabilities.sneaky"
	body := resultWith(spamJSON, `"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"sneaky":0.1}}`)
	// proxyAnswer is a plain-HTTP proxy's own 200: the typed body, and the
	// credential the request carried in two headers and the request id.
	proxyAnswer := func(pw, auth string) string {
		return "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n" +
			"X-Proxy-Echo: " + auth + "\r\nX-Proxy-Password: seen " + pw + "\r\nX-Typesafe-Request-Id: req-" + pw + "\r\n" +
			"Content-Length: " + strconv.Itoa(len(body)) + "\r\nConnection: close\r\n\r\n" + string(body)
	}
	type view struct {
		FieldPath, Echo, Password, RequestID, Error string
	}
	proxyView := func(t *testing.T, err error) view {
		t.Helper()
		rve, ok := errors.AsType[*ResponseValidationError](err)
		if !ok {
			t.Fatalf("err = %T %v, want *ResponseValidationError", err, err)
		}
		id, _ := rve.RequestID()
		return view{
			FieldPath: rve.FieldPath, Echo: rve.Header.Get("X-Proxy-Echo"), Password: rve.Header.Get("X-Proxy-Password"), RequestID: id,
			Error: strings.TrimPrefix(rve.Error(), rve.Endpoint+": "),
		}
	}
	ask := func(t *testing.T, c *Client) view {
		t.Helper()
		_, err := Ask[typedSystemOneResponse](t.Context(), c, "x", Retry(NoRetry()))
		return proxyView(t, err)
	}
	live := func(t *testing.T, c *Client) *SystemOneResponse {
		t.Helper()
		resp, err := c.SystemOne(t.Context(), "x", spamPrepared(t), Retry(NoRetry()))
		if err != nil {
			t.Fatalf("SystemOne: %v", err)
		}
		return resp
	}
	decodeView := func(t *testing.T, resp *SystemOneResponse) view {
		t.Helper()
		_, err := DecodeAs[typedSystemOneResponse](resp)
		return proxyView(t, err)
	}
	hidden := view{FieldPath: path, Echo: engine.Redacted, Password: engine.Redacted, RequestID: engine.Redacted, Error: "200 Invalid response data at '" + path + "'. (request_id=***)"}

	t.Run("error: plain HTTP through a proxy: DecodeAs redacts the proxy's answer as Ask does", func(t *testing.T) {
		pu := newEchoingProxy(t, proxyAnswer).URL()
		pu.User = url.UserPassword(proxyUser, "hunter2@proxy-password")
		c := newCredentialClient(t, slog.New(slog.DiscardHandler), WithBaseURL("http://example.com"), WithProxy(http.ProxyURL(pu)))
		byAsk, byDecodeAs := ask(t, c), decodeView(t, live(t, c))
		if diff := gocmp.Diff(hidden, byAsk); diff != "" {
			t.Errorf("Ask (-want +got):\n%s", diff)
		}
		if diff := gocmp.Diff(byAsk, byDecodeAs); diff != "" {
			t.Errorf("DecodeAs against Ask (-Ask +DecodeAs):\n%s", diff)
		}
	})

	t.Run("success: HTTPS through a proxy: DecodeAs shows the API's header as Ask does", func(t *testing.T) {
		const password = "hunter2@proxy-password"
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Proxy-Password", "seen "+password)
			w.Header().Set("X-Typesafe-Request-Id", "req-"+password)
			_, _ = w.Write(body)
		})})
		p := testsupport.NewProxy(t, testsupport.ProxyPlain, testsupport.Routes{"example.com:443": srv.Addr()})
		pu := p.URL()
		pu.User = url.UserPassword(proxyUser, password)
		c := newCredentialClient(t, slog.New(slog.DiscardHandler), WithBaseURL("https://example.com"), WithRootCAs(testsupport.RootCAs(t)), WithProxy(http.ProxyURL(pu)))
		shown := view{FieldPath: path, Password: "seen " + password, RequestID: "req-" + password, Error: "200 Invalid response data at '" + path + "'. (request_id=req-" + password + ")"}
		byAsk, byDecodeAs := ask(t, c), decodeView(t, live(t, c))
		if diff := gocmp.Diff(shown, byAsk); diff != "" {
			t.Errorf("Ask (-want +got):\n%s", diff)
		}
		if diff := gocmp.Diff(byAsk, byDecodeAs); diff != "" {
			t.Errorf("DecodeAs against Ask (-Ask +DecodeAs):\n%s", diff)
		}
	})

	t.Run("error: a response kept while the client goes through 16 other proxies still redacts its own", func(t *testing.T) {
		first := newEchoingProxy(t, proxyAnswer).URL()
		first.User = url.UserPassword(proxyUser, "proxy-password-kept")
		others := make([]*url.URL, engine.MaxProxyUserinfos)
		for i := range others {
			others[i] = newEchoingProxy(t, malformedEcho).URL()
			others[i].User = url.UserPassword(proxyUser, "proxy-password-"+strconv.Itoa(i)+"-of-16")
		}
		var calls atomic.Int64
		choose := func(*http.Request) (*url.URL, error) {
			if n := int(calls.Add(1)); n > 2 {
				return others[n-3], nil
			}
			return first, nil // Ask's call, then the kept response's
		}
		c := newCredentialClient(t, slog.New(slog.DiscardHandler), WithBaseURL("http://example.com"), WithProxy(choose))
		byAsk := ask(t, c)
		kept := live(t, c)
		for i := range others {
			_, err := c.Models().List(t.Context(), Retry(NoRetry()))
			if _, ok := errors.AsType[*ConnectionError](err); !ok {
				t.Fatalf("call %d through another proxy: err = %T %v, want the *ConnectionError of its malformed answer", i, err, err)
			}
		}
		if slices.Contains(c.cfg().Transport.Proxies.Credentials(), "proxy-password-kept") {
			t.Fatal("the client's set still holds the kept response's proxy: the case proves nothing")
		}
		if diff := gocmp.Diff(hidden, byAsk); diff != "" {
			t.Errorf("Ask (-want +got):\n%s", diff)
		}
		if diff := gocmp.Diff(byAsk, decodeView(t, kept)); diff != "" {
			t.Errorf("DecodeAs of the kept response against Ask (-Ask +DecodeAs):\n%s", diff)
		}
	})
}

// TestResponseNeverPrintsKey pins that a live response keeps the redactor
// its call used, which knows the API key and, for the answer to a plain-HTTP
// request through a proxy, the proxy's credentials, yet no fmt verb applied
// to the response, by pointer or by value, prints any of them, and neither
// does a dump that follows the response's pointers by reflection, as go-spew
// and testify's failure diffs do. The redactor sits behind a func, whose
// captures neither fmt nor reflection sees; a pointer to it would print the
// key under %s and %q, and a reflective walk would reach it. Two controls
// per response keep the probe honest: the response's redactor
// must redact each secret, so it does hold them, and the same walk must find
// each behind a plain pointer to a redactor that holds it or in its proxy
// set's credentials.
func TestResponseNeverPrintsKey(t *testing.T) {
	const password = "hunter2@proxy-password"
	body := resultWith(spamJSON)
	tests := map[string]struct {
		key      string
		response func(*testing.T) (*SystemOneResponse, *engine.ProxyCreds)
		secrets  []string // what the redactor must redact and no print may show
	}{
		"success: a response straight from the API": {
			key: testKey,
			response: func(t *testing.T) (*SystemOneResponse, *engine.ProxyCreds) {
				c := newTestClient(t, replying(http.StatusOK, body))
				resp, err := c.SystemOne(t.Context(), "x", spamPrepared(t))
				if err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
				return resp, nil
			},
			secrets: []string{testKey},
		},
		"success: a plain-HTTP proxy's answer, with a snapshot of the proxy's credentials": {
			key: quirkyKey,
			response: func(t *testing.T) (*SystemOneResponse, *engine.ProxyCreds) {
				pu := newEchoingProxy(t, func(string, string) string {
					return "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " + strconv.Itoa(len(body)) +
						"\r\nConnection: close\r\n\r\n" + string(body)
				}).URL()
				pu.User = url.UserPassword(proxyUser, password)
				c := newCredentialClient(t, slog.New(slog.DiscardHandler), WithBaseURL("http://example.com"), WithProxy(http.ProxyURL(pu)))
				resp, err := c.SystemOne(t.Context(), "x", spamPrepared(t), Retry(NoRetry()))
				if err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
				return resp, c.cfg().Transport.Proxies
			},
			secrets: []string{quirkyKey, password, proxySecrets(password)[1]},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resp, proxies := tt.response(t)
			red := engine.NewHeaderRedactor(tt.key).WithProxies(proxies)
			control := struct {
				r *engine.HeaderRedactor
				c engine.Credentials // the set's own, where its Basic token is
			}{&red, proxies.Credentials()}
			for _, secret := range tt.secrets {
				if got := resp.redactor().Header(http.Header{"X-Echo": {"seen " + secret}}).Get("X-Echo"); got != engine.Redacted {
					t.Fatalf("the response's redactor leaves %q in a header as %q: it does not hold it, so the probe would pass vacuously", secret, got)
				}
				if !reachesString(reflect.ValueOf(&control), secret, map[uintptr]bool{}) {
					t.Fatalf("the reflective walk misses %q behind a pointer to a redactor and its credentials; it proves nothing", secret)
				}
			}
			needles := append([]string{"Bearer "}, tt.secrets...)
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d", "%x", "%q"} {
				for form, v := range map[string]any{"*SystemOneResponse": resp, "SystemOneResponse": reflect.ValueOf(resp).Elem()} {
					out := fmt.Sprintf(verb, v)
					for _, needle := range needles {
						if strings.Contains(out, needle) {
							t.Errorf("%s of %s prints %q: %s", verb, form, needle, out)
						}
					}
				}
			}
			for _, needle := range needles {
				if reachesString(reflect.ValueOf(resp), needle, map[uintptr]bool{}) {
					t.Errorf("a reflective walk of the response reaches %q", needle)
				}
			}
		})
	}
}

// reachesString reports whether a string or byte slice containing s is
// reachable from v through pointers, interfaces, struct fields, arrays,
// slices and maps, as a reflective dump follows them. A func is opaque to
// reflection, so the walk, like the dumps, cannot look into what it holds.
func reachesString(v reflect.Value, s string, seen map[uintptr]bool) bool {
	switch v.Kind() {
	case reflect.String:
		return strings.Contains(v.String(), s)
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return false
		}
		seen[v.Pointer()] = true
		return reachesString(v.Elem(), s, seen)
	case reflect.Interface:
		return !v.IsNil() && reachesString(v.Elem(), s, seen)
	case reflect.Struct:
		for _, f := range v.Fields() {
			if reachesString(f, s, seen) {
				return true
			}
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return bytes.Contains(v.Bytes(), []byte(s))
		}
		fallthrough
	case reflect.Array:
		for i := range v.Len() {
			if reachesString(v.Index(i), s, seen) {
				return true
			}
		}
	case reflect.Map:
		for it := v.MapRange(); it.Next(); {
			if reachesString(it.Key(), s, seen) || reachesString(it.Value(), s, seen) {
				return true
			}
		}
	}
	return false
}

// TestAskPreservesAPIErrors ports test_custom_response_preserves_api_errors
// (:120-131) and extends it: with a struct type, a failure status is still
// the *APIError it is without one (400 with its body and request id, 429
// with its Retry-After), a transport failure is still a *ConnectionError,
// each after one attempt, and a struct type PreparedFor refuses fails with
// that *ConfigError before any request. Each error is the bare value, of
// the type itself, not a wrapper that errors.As would see through, so a
// caller's type switch holds.
func TestAskPreservesAPIErrors(t *testing.T) {
	type unTagged struct {
		Spam NoulAnswer
	}
	_, refusal := PreparedFor[unTagged]()
	if refusal == nil {
		t.Fatal("PreparedFor[unTagged] accepted a field without a tag")
	}
	tests := map[string]struct {
		reply       testsupport.Reply
		ask         askFunc
		check       func(*testing.T, error)
		wantType    reflect.Type
		wantRequest int
	}{
		"error: upstream: 400 is an *APIError with its body and request id": {
			reply: withHeader(testsupport.JSON(http.StatusBadRequest, []byte(`{"detail":"Invalid request"}`)), "x-typesafe-request-id", "req-error"),
			ask:   askAs[knownResponse](),
			check: func(t *testing.T, err error) {
				t.Helper()
				checkAPIError(t, err, APIErrorBadRequest, http.StatusBadRequest, `{"detail":"Invalid request"}`, "req-error", 0)
			},
			wantType:    reflect.TypeFor[*APIError](),
			wantRequest: 1,
		},
		"error: 429 is an *APIError with its Retry-After": {
			reply: withHeader(testsupport.JSON(http.StatusTooManyRequests, []byte(`{"detail":"Too many requests"}`)), "x-typesafe-request-id", "req-429", "Retry-After", "7"),
			ask:   askAs[typedSystemOneResponse](),
			check: func(t *testing.T, err error) {
				t.Helper()
				checkAPIError(t, err, APIErrorRateLimit, http.StatusTooManyRequests, `{"detail":"Too many requests"}`, "req-429", 7*time.Second)
			},
			wantType:    reflect.TypeFor[*APIError](),
			wantRequest: 1,
		},
		"error: a transport failure is a *ConnectionError": {
			reply: testsupport.Reply{Err: errors.New("dial tcp: connection refused")},
			ask:   askAs[knownResponse](),
			check: func(t *testing.T, err error) {
				t.Helper()
				var ce *ConnectionError
				if !errors.As(err, &ce) || !strings.Contains(err.Error(), "connection refused") {
					t.Errorf("err = %T %v, want a *ConnectionError naming the cause", err, err)
				}
			},
			wantType:    reflect.TypeFor[*ConnectionError](),
			wantRequest: 1,
		},
		"error: a struct type PreparedFor refuses fails before any request": {
			reply: testsupport.JSON(http.StatusOK, resultWith(spamJSON)),
			ask:   askAs[unTagged](),
			check: func(t *testing.T, err error) {
				t.Helper()
				var ce *ConfigError
				if !errors.As(err, &ce) || !errors.Is(err, refusal) {
					t.Errorf("err = %T %v, want PreparedFor's own *ConfigError %v", err, err, refusal)
				}
			},
			wantType: reflect.TypeFor[*ConfigError](),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &testsupport.Recorder{Replies: []testsupport.Reply{tt.reply}}
			c := newTestClient(t, rec)
			err := tt.ask(t, c)
			tt.check(t, err)
			if got := reflect.TypeOf(err); got != tt.wantType {
				t.Errorf("Ask's error is a %v, want the bare %v SystemOne returns", got, tt.wantType)
			}
			if rec.Count() != tt.wantRequest {
				t.Errorf("the transport saw %d requests, want %d", rec.Count(), tt.wantRequest)
			}
		})
	}
}

// withHeader returns reply with the headers kv (name, value, ...) added.
func withHeader(reply testsupport.Reply, kv ...string) testsupport.Reply {
	for i := 0; i+1 < len(kv); i += 2 {
		reply.Header.Add(kv[i], kv[i+1])
	}
	return reply
}

// checkAPIError checks that err is an *APIError of kind and status carrying
// body and the request id, and, when retryAfter is not zero, the
// Retry-After it asks for.
func checkAPIError(t *testing.T, err error, kind APIErrorKind, status int, body, id string, retryAfter time.Duration) {
	t.Helper()
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %T %v, want *APIError", err, err)
	}
	gotID, _ := ae.RequestID()
	gotAfter, _ := ae.RetryAfter()
	type view struct {
		Kind       APIErrorKind
		StatusCode int
		Body, ID   string
		RetryAfter time.Duration
	}
	want := view{Kind: kind, StatusCode: status, Body: body, ID: id, RetryAfter: retryAfter}
	if diff := gocmp.Diff(want, view{Kind: ae.Kind, StatusCode: ae.StatusCode, Body: string(ae.Body), ID: gotID, RetryAfter: gotAfter}); diff != "" {
		t.Errorf("*APIError (-want +got):\n%s", diff)
	}
}
