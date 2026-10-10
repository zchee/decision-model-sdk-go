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
	"errors"
	"maps"
	"net/http"
	"testing"

	decision "github.com/zchee/decision-model-sdk-go"
	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
)

// liveTicket is the live harness's typed question set (livetest), which the
// recorded OpenAI exchange and the recorded Decisions API answers answer.
type liveTicket struct {
	Billing decision.NoulAnswer   `decision:"kind=noul;name=billing;instructions=Is this ticket about billing?"`
	Tone    decision.ChoiceAnswer `decision:"kind=choice;name=tone;instructions=What is the customer's tone?;options=calm|frustrated|angry"`
	Urgency decision.ScoreAnswer  `decision:"kind=score;name=urgency;instructions=How urgent is this ticket?;levels=can wait|this week|today"`
}

// recordedState is the state of the OpenAI exchange the fixtures under
// testdata/live/providers/openai/dialect-*.json hold: a RawJSON, so that it
// is sent with the recorded bytes.
var recordedState = decision.RawJSON(`{"subject":"Charged twice this month","body":"I see two charges of $49. Please fix this ASAP."}`)

// testKey is the key the tests' clients send; no vendor issued it.
const testKey = "sk-dialect-test-key-0123456789"

// fixture returns the recorded body name of provider dir under
// testdata/live/providers.
func fixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	return testsupport.Fixture(t, "live/providers/"+dir+"/"+name)
}

// ticketPrepared returns liveTicket's question set.
func ticketPrepared(t *testing.T) *decision.Prepared {
	t.Helper()
	qs, err := decision.PreparedFor[liveTicket]()
	if err != nil {
		t.Fatal(err)
	}
	return qs
}

// respond returns the ResponseFunc p's dialect returns for the call that
// asks questions with the SDK body body.
func respond(t *testing.T, p decision.Provider, body []byte, questions []decision.PreparedQuestion) decision.ResponseFunc {
	t.Helper()
	_, f, err := p.Dialect.Request(body, questions)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if f == nil {
		t.Fatal("Request returned no ResponseFunc")
	}
	return f
}

// pathOf returns the Path of the *decision.ResponsePathError in err's chain,
// failing the test when there is none.
func pathOf(t *testing.T, err error) string {
	t.Helper()
	pe, ok := errors.AsType[*decision.ResponsePathError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want a *decision.ResponsePathError", err, err)
	}
	return pe.Path
}

// presetClient returns a client of preset p that sends through rec with
// testKey and no retry, the preset variables cleared.
func presetClient(t *testing.T, p decision.Provider, rec http.RoundTripper, opts ...decision.ClientOption) *decision.Client {
	t.Helper()
	setEnv(t, nil)
	base := []decision.ClientOption{decision.WithProvider(p), decision.WithAPIKey(testKey), decision.WithRoundTripper(rec), decision.WithRetry(decision.NoRetry())}
	c, err := decision.NewClient(append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// failOnSend is a transport that fails the test for any request: the
// refusals of a dialect send nothing.
func failOnSend(t *testing.T) *testsupport.Recorder {
	return &testsupport.Recorder{Respond: func(r testsupport.RecordedRequest) testsupport.Reply {
		t.Errorf("a request was sent: %s", r.Body)
		return testsupport.JSON(http.StatusInternalServerError, nil)
	}}
}

// apiErrorOf returns err as an *decision.APIError, failing the test when it
// is not one.
func apiErrorOf(t *testing.T, err error) *decision.APIError {
	t.Helper()
	apiErr, ok := errors.AsType[*decision.APIError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want an *decision.APIError", err, err)
	}
	return apiErr
}

// ticketView is the comparable form of a liveTicket's answers.
type ticketView struct {
	Billing           float64
	Tone              string
	ToneConfidence    float64
	ToneProbabilities map[string]float64
	Urgency           float64
	UrgencyConfidence float64
	Legend            map[uint32]string
	UrgencyProbs      map[uint32]float64
}

// viewTicket returns tk's answers in a comparable form.
func viewTicket(tk liveTicket) ticketView {
	v := ticketView{
		Billing: tk.Billing.Noul(), Tone: tk.Tone.Choice(), ToneConfidence: tk.Tone.Confidence(),
		ToneProbabilities: map[string]float64{}, Urgency: tk.Urgency.Score(), UrgencyConfidence: tk.Urgency.Confidence(),
		Legend: map[uint32]string{}, UrgencyProbs: map[uint32]float64{},
	}
	maps.Insert(v.ToneProbabilities, tk.Tone.Probabilities())
	for level, c := range tk.Urgency.Legend() {
		v.Legend[level] = c.Text()
	}
	maps.Insert(v.UrgencyProbs, tk.Urgency.Probabilities())
	return v
}

// systemOneBody returns the System One body a client without a dialect
// sends for state and qs, which a dialect's Request takes.
func systemOneBody(t *testing.T, state any, qs *decision.Prepared) []byte {
	t.Helper()
	setEnv(t, nil)
	rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusBadRequest, []byte(`{}`))}}
	c, err := decision.NewClient(decision.WithBaseURL("https://system-one.example.test"), decision.WithAPIKey(testKey),
		decision.WithModel("gpt-6-luna"), decision.WithRoundTripper(rec), decision.WithRetry(decision.NoRetry()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()
	_, _ = c.SystemOne(t.Context(), state, qs)
	return rec.Requests()[0].Body
}
