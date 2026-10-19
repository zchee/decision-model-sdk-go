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
	"encoding/json/jsontext"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"
	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
	"github.com/zchee/decision-model-sdk-go/provider"
)

// TestOpenAIRequestMalformedBody pins what the dialect's Request returns for
// a System One body it cannot read, each made from the SDK's own body for
// liveTicket changed in one place: a value cut off anywhere it reads one is
// a failure of the body as a whole (a *decision.ResponsePathError with an
// empty Path), and a body without a state is refused by name. A client never
// hands the dialect such a body; a caller that runs Dialect.Request itself
// can.
func TestOpenAIRequestMalformedBody(t *testing.T) {
	qs := ticketPrepared(t)
	sdkBody := string(systemOneBody(t, "x", qs))
	tests := map[string]struct {
		old, new string
		wantErr  string // the text the error holds; "" for a failure of the body as a whole
	}{
		"error: the state's value cut off": {
			old: `"state":"x"`, new: `"state":`,
		},
		"error: an added member's value cut off": {
			old: `"state":"x"`, new: `"state":"x","store":`,
		},
		"error: a question's instructions cut off": {
			old: `"instructions":"Is this ticket about billing?"`, new: `"instructions":`,
		},
		"error: a choice option's description cut off": {
			old: `"calm":null`, new: `"calm":`,
		},
		"error: no state": {
			old: `"state":"x",`, wantErr: "the System One body has no state",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(sdkBody, tt.old) {
				t.Fatalf("the SDK's body has no %q: %s", tt.old, sdkBody)
			}
			body := strings.Replace(sdkBody, tt.old, tt.new, 1)
			out, f, err := provider.OpenAI().Dialect.Request([]byte(body), qs.Questions())
			if err == nil {
				t.Fatalf("Request(%s) = %s, a ResponseFunc %t, nil; want an error", body, out, f != nil)
			}
			if out != nil || f != nil {
				t.Errorf("Request(%s) returned a body %s or a ResponseFunc (%t) beside its error", body, out, f != nil)
			}
			if tt.wantErr == "" {
				if got := pathOf(t, err); got != "" {
					t.Errorf("Path = %q, want the body as a whole (%v)", got, err)
				}
				return
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to hold %q", err, tt.wantErr)
			}
		})
	}
}

// TestOpenAIExtraBodyReplacements pins the request of a call whose
// ExtraBody replaces a member the SDK writes: a model that is not a string
// is sent as it is, and questions that are not the prepared set's are
// refused with a *decision.DialectError before anything is sent, since the
// dialect writes each question from the prepared set.
func TestOpenAIExtraBodyReplacements(t *testing.T) {
	tests := map[string]struct {
		opts       []decision.CallOption
		wantPrefix string // the request body's start, for a call that is sent
		wantReason string // the DialectError's Reason holds it, for a call that is refused
	}{
		"success: a model given as a number": {
			opts:       []decision.CallOption{decision.ExtraBody("model", 5)},
			wantPrefix: `{"model":5,"input":"hello","questions":[{"type":"predicate","name":"billing"`,
		},
		"error: no questions": {
			opts:       []decision.CallOption{decision.ExtraBody("questions", map[string]any{})},
			wantReason: `an ExtraBody member named "questions" replaced them`,
		},
		"error: a question the prepared set does not have": {
			opts:       []decision.CallOption{decision.ExtraBody("questions", map[string]any{"refund": map[string]any{"type": "noul"}})},
			wantReason: `an ExtraBody member named "questions" replaced them`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.wantReason != "" {
				c := presetClient(t, provider.OpenAI(), failOnSend(t))
				_, err := c.SystemOne(t.Context(), "hello", ticketPrepared(t), tt.opts...)
				de, ok := errors.AsType[*decision.DialectError](err)
				if !ok {
					t.Fatalf("err = %v (%T), want a *decision.DialectError", err, err)
				}
				if de.Provider != "OpenAI" || !strings.Contains(de.Reason, tt.wantReason) {
					t.Errorf("Provider %q, Reason %q; want OpenAI and a reason holding %q", de.Provider, de.Reason, tt.wantReason)
				}
				return
			}
			rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusBadRequest, []byte(`{"error":{"message":"stop"}}`))}}
			c := presetClient(t, provider.OpenAI(), rec)
			_, _ = c.SystemOne(t.Context(), "hello", ticketPrepared(t), tt.opts...)
			reqs := rec.Requests()
			if len(reqs) != 1 {
				t.Fatalf("%d requests, want 1", len(reqs))
			}
			if !bytes.HasPrefix(reqs[0].Body, []byte(tt.wantPrefix)) {
				t.Errorf("request body = %s, want it to start with %s", reqs[0].Body, tt.wantPrefix)
			}
		})
	}
}

// TestOpenAIResponseMalformed pins the failures of OpenAI answers that
// TestOpenAIResponseRules does not reach, each on the recorded answer
// changed in one place: a body cut off where a member name, a value or
// answers' array should follow fails as a whole; no answers, a choice
// probability whose value is not a string, a score without its confidence,
// a choice without its probabilities and a probability that is not an
// object fail at their path in OpenAI's body.
func TestOpenAIResponseMalformed(t *testing.T) {
	recorded := string(fixture(t, "openai", "dialect-response.json"))
	tests := map[string]struct {
		old, new string
		wantPath string
	}{
		"error: cut off where a member name follows": {
			old: recorded, new: `{"model":"gpt-6-luna",`, wantPath: "",
		},
		"error: cut off inside a value": {
			old: recorded, new: `{"model":"gpt`, wantPath: "",
		},
		"error: cut off where answers' value should be": {
			old: recorded, new: `{"model":"gpt-6-luna","answers":`, wantPath: "",
		},
		"error: no answers": {
			old: recorded, new: `{"model":"gpt-6-luna","usage":{"input_tokens":395,"output_tokens":0}}`, wantPath: "answers",
		},
		"error: a choice probability whose value is a number": {
			old: `{"value":"calm"`, new: `{"value":1`, wantPath: "answers[1].probabilities[0].value",
		},
		"error: a score without its confidence": {
			old: `,"confidence":0.97`, wantPath: "answers[2].confidence",
		},
		"error: a choice without its probabilities": {
			old:      `"probabilities":[{"value":"calm","probability":0.01},{"value":"frustrated","probability":0.97},{"value":"angry","probability":0.02}],`,
			wantPath: "answers[1].probabilities",
		},
		"error: a probability that is not an object": {
			old: `"probabilities":[{"value":"calm","probability":0.01},`, new: `"probabilities":[1,`, wantPath: "answers[1].probabilities[0]",
		},
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
			if err == nil {
				t.Fatalf("ResponseFunc(%s) = %s, nil; want an error", body, tr.Body)
			}
			if got := pathOf(t, err); got != tt.wantPath {
				t.Errorf("Path = %q, want %q (%v)", got, tt.wantPath, err)
			}
		})
	}
}

// TestOpenAIResponseRepeatedNameInUsage pins that a usage count the
// dialect copies as it is, here an object that repeats a member name, which
// the dialect's reader takes and its writer refuses, reaches the caller as a
// *decision.ResponseValidationError of the body as a whole that wraps
// jsontext.ErrDuplicateName, with the body as it arrived.
func TestOpenAIResponseRepeatedNameInUsage(t *testing.T) {
	body := bytes.Replace(fixture(t, "openai", "dialect-response.json"), []byte(`"input_tokens":395`), []byte(`"input_tokens":{"n":1,"n":2}`), 1)
	rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, body)}}
	c := presetClient(t, provider.OpenAI(), rec)
	_, err := decision.Ask[liveTicket](t.Context(), c, recordedState)
	ve, ok := errors.AsType[*decision.ResponseValidationError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want a *decision.ResponseValidationError", err, err)
	}
	if ve.FieldPath != "." || !bytes.Equal(ve.Body, body) || !errors.Is(err, jsontext.ErrDuplicateName) {
		t.Errorf("FieldPath %q, Body %s, err %v; want ., the body as it arrived, and an error wrapping jsontext.ErrDuplicateName", ve.FieldPath, ve.Body, err)
	}
}

// replayTransport calls each request's GetBody before passing the request
// to next, as net/http does to send a body again after a redirect or on a
// new connection, and keeps what each GetBody reader read.
type replayTransport struct {
	t       *testing.T
	next    http.RoundTripper
	replays [][]byte
}

// RoundTrip implements [net/http.RoundTripper].
func (rt *replayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.GetBody == nil {
		rt.t.Error("the request has no GetBody")
		return rt.next.RoundTrip(req)
	}
	rc, err := req.GetBody()
	if err != nil {
		rt.t.Errorf("GetBody: %v", err)
		return rt.next.RoundTrip(req)
	}
	b, err := io.ReadAll(rc)
	if err != nil {
		rt.t.Errorf("reading GetBody's reader: %v", err)
	}
	_ = rc.Close()
	rt.replays = append(rt.replays, b)
	return rt.next.RoundTrip(req)
}

// TestOpenAIRequestGetBody pins that the request of a dialect that writes a
// body of its own carries a GetBody that replays that body, not the SDK's
// System One body the call released: the replay and the body sent are the
// recorded OpenAI request byte for byte, and so equal to each other.
func TestOpenAIRequestGetBody(t *testing.T) {
	rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, fixture(t, "openai", "dialect-response.json"))}}
	rt := &replayTransport{t: t, next: rec}
	c := presetClient(t, provider.OpenAI(), rt)
	if _, err := decision.Ask[liveTicket](t.Context(), c, recordedState); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	want := string(fixture(t, "openai", "dialect-request.json"))
	reqs := rec.Requests()
	if len(reqs) != 1 || len(rt.replays) != 1 {
		t.Fatalf("%d requests and %d replays, want 1 of each", len(reqs), len(rt.replays))
	}
	if diff := gocmp.Diff(want, string(rt.replays[0])); diff != "" {
		t.Errorf("GetBody's replay (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff(want, string(reqs[0].Body)); diff != "" {
		t.Errorf("the body sent (-want +got):\n%s", diff)
	}
	if !bytes.Equal(rt.replays[0], reqs[0].Body) {
		t.Errorf("GetBody's replay %s differs from the body sent %s", rt.replays[0], reqs[0].Body)
	}
}

// TestDecisionsAPIEnvelopeCutOff pins that an envelope cut off where the
// value of code or of data.result should be fails as a whole: a
// *decision.ResponsePathError with an empty Path.
func TestDecisionsAPIEnvelopeCutOff(t *testing.T) {
	tests := map[string]struct {
		body string
	}{
		"error: code with no value":             {body: `{"code":}`},
		"error: cut off at data.result's value": {body: `{"code":0,"data":{"result":`},
	}
	_, f, err := provider.DecisionsAPI().Dialect.Request([]byte(`{"state":"x"}`), nil)
	if err != nil || f == nil {
		t.Fatalf("Request = a ResponseFunc %t, %v; want one and nil", f != nil, err)
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tr, err := f(http.StatusOK, http.Header{}, []byte(tt.body))
			if err == nil {
				t.Fatalf("ResponseFunc(%s) = %s, nil; want an error", tt.body, tr.Body)
			}
			if got := pathOf(t, err); got != "" {
				t.Errorf("Path = %q, want the body as a whole (%v)", got, err)
			}
		})
	}
}
