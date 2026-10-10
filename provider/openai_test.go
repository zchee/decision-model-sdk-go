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

package provider_test

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"
	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
	"github.com/zchee/decision-model-sdk-go/provider"
)

// openAITranslated is the System One response the dialect writes from the
// recorded dialect-response.json: the answers keyed by name, the score's
// legend from the request's levels, the probabilities keyed as System One
// keys them, the numbers as OpenAI spelled them, and the usage cut to its two
// counts.
const openAITranslated = `{"model":"gpt-6-luna","answers":{` +
	`"billing":{"type":"noul","noul":1.0},` +
	`"tone":{"type":"choice","choice":"frustrated","confidence":0.96,"probabilities":{"calm":0.01,"frustrated":0.97,"angry":0.02}},` +
	`"urgency":{"type":"score","score":1.98,"confidence":0.97,"legend":{"0":"can wait","1":"this week","2":"today"},"probabilities":{"0":0.0,"1":0.02,"2":0.98}}` +
	`},"usage":{"input_tokens":395,"output_tokens":0}}`

// TestOpenAIRequest pins the request the dialect writes: for the recorded
// state and liveTicket's questions, the recorded request byte for byte; text
// state as the text itself; option descriptions given as text; and a member
// ExtraBody adds, copied to the top level.
func TestOpenAIRequest(t *testing.T) {
	tests := map[string]struct {
		state any
		qs    func(t *testing.T) *decision.Prepared
		opts  []decision.CallOption
		want  string
	}{
		"success: the recorded request": {
			state: recordedState,
			qs:    ticketPrepared,
			want:  string(fixture(t, "openai", "dialect-request.json")),
		},
		"success: text state, a described option, no instructions": {
			state: `Charged "twice"`,
			qs: func(t *testing.T) *decision.Prepared {
				qs, err := decision.NewQuestions().
					Noul("billing", decision.Noul{}).
					Choice("tone", decision.Choice{Options: decision.Options{{Label: "calm", Description: decision.Text("No complaint.")}, {Label: "angry"}}}).
					Prepare()
				if err != nil {
					t.Fatal(err)
				}
				return qs
			},
			want: `{"model":"gpt-6-luna","input":"Charged \"twice\"","questions":[{"type":"predicate","name":"billing"},` +
				`{"type":"choice","name":"tone","choices":[{"value":"calm","description":"No complaint."},{"value":"angry"}]}]}`,
		},
		"success: the state's JSON compacted, an ExtraBody member copied, the call's model": {
			state: decision.RawJSON(`{ "a" : [1, 2] }`),
			qs:    ticketPrepared,
			opts:  []decision.CallOption{decision.ExtraBody("store", false), decision.Model("gpt-6-sol")},
			want: `{"model":"gpt-6-sol","input":"{\"a\":[1,2]}","questions":[{"type":"predicate","name":"billing","instructions":"Is this ticket about billing?"},` +
				`{"type":"choice","name":"tone","instructions":"What is the customer's tone?","choices":[{"value":"calm"},{"value":"frustrated"},{"value":"angry"}]},` +
				`{"type":"score","name":"urgency","instructions":"How urgent is this ticket?","levels":[{"label":"can wait"},{"label":"this week"},{"label":"today"}]}],"store":false}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusBadRequest, []byte(`{"error":{"message":"stop"}}`))}}
			c := presetClient(t, provider.OpenAI(), rec)
			_, _ = c.SystemOne(t.Context(), tt.state, tt.qs(t), tt.opts...)
			reqs := rec.Requests()
			if len(reqs) != 1 {
				t.Fatalf("%d requests, want 1", len(reqs))
			}
			if diff := gocmp.Diff(tt.want, string(reqs[0].Body)); diff != "" {
				t.Errorf("request body (-want +got):\n%s", diff)
			}
			if reqs[0].URL != "https://api.openai.com/v1/decisions" {
				t.Errorf("URL = %s", reqs[0].URL)
			}
		})
	}
}

// TestOpenAIRefusals pins the calls the dialect refuses before sending
// anything, each with a *decision.DialectError naming OpenAI.
func TestOpenAIRefusals(t *testing.T) {
	prepare := func(t *testing.T, qs *decision.Questions) *decision.Prepared {
		t.Helper()
		p, err := qs.Prepare()
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := map[string]struct {
		qs     func(t *testing.T) *decision.Prepared
		opts   []decision.CallOption
		reason string
	}{
		"error: a raw question": {
			qs: func(t *testing.T) *decision.Prepared {
				return prepare(t, decision.NewQuestions().Raw("billing", decision.RawQuestion{Type: "noul", Fields: map[string]any{"instructions": "?"}}))
			},
			reason: "Questions.Raw",
		},
		"error: instructions as JSON content": {
			qs: func(t *testing.T) *decision.Prepared {
				return prepare(t, decision.NewQuestions().Noul("billing", decision.Noul{Instructions: decision.JSON([]byte(`{"ask":"billing?"}`))}))
			},
			reason: "instructions as JSON content",
		},
		"error: a yes/no question with a Yes outcome": {
			qs: func(t *testing.T) *decision.Prepared {
				return prepare(t, decision.NewQuestions().Noul("billing", decision.Noul{Yes: decision.Text("about money")}))
			},
			reason: "describes its outcomes",
		},
		"error: a yes/no question with a No outcome": {
			qs: func(t *testing.T) *decision.Prepared {
				return prepare(t, decision.NewQuestions().Noul("billing", decision.Noul{No: decision.Text("not money")}))
			},
			reason: "describes its outcomes",
		},
		"error: an option described with JSON content": {
			qs: func(t *testing.T) *decision.Prepared {
				return prepare(t, decision.NewQuestions().Choice("tone", decision.Choice{Options: decision.Options{{Label: "calm", Description: decision.JSON([]byte(`{"x":1}`))}}}))
			},
			reason: "described with JSON content",
		},
		"error: a score level of JSON content": {
			qs: func(t *testing.T) *decision.Prepared {
				return prepare(t, decision.NewQuestions().Score("urgency", decision.Score{Levels: []decision.Content{decision.Text("low"), decision.JSON([]byte(`["high"]`))}}))
			},
			reason: "level 1 of score question",
		},
		"error: an ExtraBody member named input": {
			qs:     ticketPrepared,
			opts:   []decision.CallOption{decision.ExtraBody("input", "x")},
			reason: `member named "input"`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := presetClient(t, provider.OpenAI(), failOnSend(t), decision.WithRetry(decision.DefaultRetry()))
			_, err := c.SystemOne(t.Context(), "hello", tt.qs(t), tt.opts...)
			de, ok := errors.AsType[*decision.DialectError](err)
			if !ok {
				t.Fatalf("err = %v (%T), want a *decision.DialectError", err, err)
			}
			if de.Provider != "OpenAI" || !strings.Contains(de.Reason, tt.reason) {
				t.Errorf("Provider %q, Reason %q; want OpenAI and a reason holding %q", de.Provider, de.Reason, tt.reason)
			}
			if n := c.Stats().Attempts; n != 0 {
				t.Errorf("Stats().Attempts = %d, want 0", n)
			}
		})
	}
}

// TestOpenAIResponse pins the translation of the recorded answer: the System
// One body it writes, which decodes through UnmarshalJSON, Answers and
// DecodeAs to the recorded values.
func TestOpenAIResponse(t *testing.T) {
	qs := ticketPrepared(t)
	f := respond(t, provider.OpenAI(), systemOneBody(t, recordedState, qs), qs.Questions())
	tr, err := f(http.StatusOK, http.Header{}, fixture(t, "openai", "dialect-response.json"))
	if err != nil {
		t.Fatalf("ResponseFunc: %v", err)
	}
	if diff := gocmp.Diff(openAITranslated, string(tr.Body)); diff != "" {
		t.Errorf("translated body (-want +got):\n%s", diff)
	}
	var resp decision.SystemOneResponse
	if err := resp.UnmarshalJSON(tr.Body); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if n, ok := resp.Answers().Noul("billing"); !ok || n.Noul() != 1 {
		t.Errorf("billing = %v, %t; want 1", n.Noul(), ok)
	}
	tk, err := decision.DecodeAs[liveTicket](&resp)
	if err != nil {
		t.Fatalf("DecodeAs: %v", err)
	}
	want := ticketView{
		Billing: 1, Tone: "frustrated", ToneConfidence: 0.96,
		ToneProbabilities: map[string]float64{"calm": 0.01, "frustrated": 0.97, "angry": 0.02},
		Urgency:           1.98, UrgencyConfidence: 0.97,
		Legend:       map[uint32]string{0: "can wait", 1: "this week", 2: "today"},
		UrgencyProbs: map[uint32]float64{0: 0, 1: 0.02, 2: 0.98},
	}
	if diff := gocmp.Diff(want, viewTicket(tk)); diff != "" {
		t.Errorf("answers (-want +got):\n%s", diff)
	}
}

// TestOpenAIResponseRules pins what the translation refuses and what it
// lets through, each on the recorded answer changed in one place: a member
// it does not know is ignored at every level, while a missing member, one of
// the wrong kind, an answer to a question the request did not ask, a repeated
// answer, a kind that is not the question's and a probability that names no
// option or level are failures at their path in OpenAI's body.
func TestOpenAIResponseRules(t *testing.T) {
	recorded := string(fixture(t, "openai", "dialect-response.json"))
	tests := map[string]struct {
		old, new string
		wantPath string // "-" for success
	}{
		"success: a top-level member it does not know": {
			old: `{"model"`, new: `{"service_tier":"default","model"`, wantPath: "-",
		},
		"success: an answer member it does not know": {
			old: `"name":"billing",`, new: `"name":"billing","reasoning":{"steps":[]},`, wantPath: "-",
		},
		"success: a probability member it does not know": {
			old: `{"value":"calm","probability":0.01}`, new: `{"value":"calm","logprob":-4.6,"probability":0.01}`, wantPath: "-",
		},
		"error: no model":              {old: `"model":"gpt-6-luna",`, wantPath: "model"},
		"error: model is a number":     {old: `"model":"gpt-6-luna"`, new: `"model":6`, wantPath: "model"},
		"error: no usage":              {old: `,"usage":{"input_tokens":395,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens":0,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":395}`, wantPath: "usage"},
		"error: usage is an array":     {old: `"usage":{"input_tokens":395,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens":0,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":395}`, new: `"usage":[]`, wantPath: "usage"},
		"error: answers is an object":  {old: `"answers":[`, new: `"answers":{"x":[`, wantPath: "answers"},
		"error: an answer is a string": {old: `"answers":[`, new: `"answers":["billing",`, wantPath: "answers[0]"},
		"error: a name the request did not ask": {
			old: `"name":"billing"`, new: `"name":"refund"`, wantPath: "answers[0].name",
		},
		"error: a repeated name": {
			old: `"name":"tone"`, new: `"name":"billing"`, wantPath: "answers[1].name",
		},
		"error: a kind that is not the question's": {
			old: `"type":"predicate"`, new: `"type":"choice"`, wantPath: "answers[0].type",
		},
		"error: a kind OpenAI's wire does not have": {
			old: `"type":"predicate"`, new: `"type":"ranking"`, wantPath: "answers[0].type",
		},
		"error: no type": {old: `{"type":"predicate",`, new: `{`, wantPath: "answers[0].type"},
		"error: no name": {old: `"name":"billing",`, wantPath: "answers[0].name"},
		"error: a predicate without its probability": {
			old: `,"probability":1.0}`, new: `}`, wantPath: "answers[0].probability",
		},
		"error: a choice without its confidence": {
			old: `,"confidence":0.96`, wantPath: "answers[1].confidence",
		},
		"error: a choice without its choice": {
			old: `"choice":"frustrated",`, wantPath: "answers[1].choice",
		},
		"error: a choice probability whose value is not a label": {
			old: `{"value":"calm"`, new: `{"value":"serene"`, wantPath: "answers[1].probabilities[0].value",
		},
		"error: a choice probability without its probability": {
			old: `{"value":"calm","probability":0.01}`, new: `{"value":"calm"}`, wantPath: "answers[1].probabilities[0].probability",
		},
		"error: a repeated choice value": {
			old: `{"value":"angry"`, new: `{"value":"calm"`, wantPath: "answers[1].probabilities[2].value",
		},
		"error: choice probabilities as an object": {
			old: `"probabilities":[{"value":"calm","probability":0.01},{"value":"frustrated","probability":0.97},{"value":"angry","probability":0.02}]`,
			new: `"probabilities":{"calm":0.01}`, wantPath: "answers[1].probabilities",
		},
		"error: a score probability whose value is not a level": {
			old: `{"value":2,`, new: `{"value":3,`, wantPath: "answers[2].probabilities[2].value",
		},
		"error: a score probability whose value is a label": {
			old: `{"value":2,`, new: `{"value":"today",`, wantPath: "answers[2].probabilities[2].value",
		},
		"error: a score without its score": {
			old: `"score":1.98,`, wantPath: "answers[2].score",
		},
		"error: not JSON":                {old: `{"model"`, new: `<{"model"`, wantPath: ""},
		"error: an empty body":           {old: recorded, new: ``, wantPath: ""},
		"error: data after the response": {old: `"total_tokens":395}}`, new: `"total_tokens":395}}{}`, wantPath: ""},
	}
	qs := ticketPrepared(t)
	f := respond(t, provider.OpenAI(), systemOneBody(t, recordedState, qs), qs.Questions())
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(recorded, tt.old) {
				t.Fatalf("the recorded answer has no %q", tt.old)
			}
			body := strings.Replace(recorded, tt.old, tt.new, 1)
			tr, err := f(http.StatusOK, http.Header{}, []byte(body))
			if tt.wantPath == "-" {
				if err != nil {
					t.Fatalf("ResponseFunc: %v", err)
				}
				if diff := gocmp.Diff(openAITranslated, string(tr.Body)); diff != "" {
					t.Errorf("translated body (-want +got):\n%s", diff)
				}
				return
			}
			if got := pathOf(t, err); got != tt.wantPath {
				t.Errorf("Path = %q, want %q (%v)", got, tt.wantPath, err)
			}
		})
	}
}

// TestOpenAIResponseThroughTheClient pins that a translation failure reaches
// the caller as a *decision.ResponseValidationError whose FieldPath is the
// dialect's path in OpenAI's body and whose Body is that body as it arrived.
func TestOpenAIResponseThroughTheClient(t *testing.T) {
	body := bytes.Replace(fixture(t, "openai", "dialect-response.json"), []byte(`"name":"tone"`), []byte(`"name":"mood"`), 1)
	rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, body)}}
	c := presetClient(t, provider.OpenAI(), rec)
	_, err := decision.Ask[liveTicket](t.Context(), c, recordedState)
	ve, ok := errors.AsType[*decision.ResponseValidationError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want a *decision.ResponseValidationError", err, err)
	}
	if ve.FieldPath != "answers[1].name" || !bytes.Equal(ve.Body, body) || ve.StatusCode != http.StatusOK {
		t.Errorf("FieldPath %q, StatusCode %d, Body %s; want answers[1].name, 200, the body as it arrived", ve.FieldPath, ve.StatusCode, ve.Body)
	}
}

// TestOpenAIErrors pins each failure status: the body is kept as the vendor
// sent it, the message is its error.message, and the ErrorType is
// authentication_error for 401 and api_usage_error for 400 and 404, whatever
// error.type says, so that IsAuthentication holds on the recorded 401.
func TestOpenAIErrors(t *testing.T) {
	tests := map[string]struct {
		status        int
		body          []byte
		wantErrorType string
		wantMessage   string
		wantAuth      bool
	}{
		"error: the recorded wrong key (401)": {
			status: http.StatusUnauthorized, body: fixture(t, "openai", "wrong-key.json"),
			wantErrorType: "authentication_error", wantMessage: "A valid API key is required.", wantAuth: true,
		},
		"error: the recorded refusal of the System One wire (400)": {
			status: http.StatusBadRequest, body: fixture(t, "openai", "refused.json"),
			wantErrorType: "api_usage_error", wantMessage: "Missing required parameter: 'input'.",
		},
		"error: an unknown model (404)": {
			status: http.StatusNotFound, body: []byte(`{"error":{"message":"The model does not exist.","type":"invalid_request_error","param":null,"code":"model_not_found"}}`),
			wantErrorType: "api_usage_error", wantMessage: "The model does not exist.",
		},
		"error: a rate limit (429)": {
			status: http.StatusTooManyRequests, body: []byte(`{"error":{"message":"Slow down.","type":"rate_limit_error"}}`),
			wantMessage: "Slow down.",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(tt.status, tt.body)}}
			c := presetClient(t, provider.OpenAI(), rec)
			_, err := decision.Ask[liveTicket](t.Context(), c, recordedState)
			apiErr := apiErrorOf(t, err)
			if !bytes.Equal(apiErr.Body, tt.body) {
				t.Errorf("Body = %s, want the body as the vendor sent it", apiErr.Body)
			}
			if apiErr.ErrorType != tt.wantErrorType || apiErr.Message != tt.wantMessage || apiErr.IsAuthentication() != tt.wantAuth {
				t.Errorf("ErrorType %q, Message %q, IsAuthentication %t; want %q, %q, %t",
					apiErr.ErrorType, apiErr.Message, apiErr.IsAuthentication(), tt.wantErrorType, tt.wantMessage, tt.wantAuth)
			}
		})
	}
}
