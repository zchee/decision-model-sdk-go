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
	"net/http"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"
	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
	"github.com/zchee/decision-model-sdk-go/provider"
)

// TestDecisionsAPIUnwrap pins the envelope's translation on both recorded
// 200 bodies: the dialect sends the SDK's body as it is, and hands the
// decoder data.result as a slice of the body itself, with no copy.
func TestDecisionsAPIUnwrap(t *testing.T) {
	tests := map[string]struct {
		fixture string // the recorded body under testdata/live/providers/decisions-api
	}{
		"success: the questions answer":  {fixture: "questions.json"},
		"success: the typed call answer": {fixture: "typed-response.json"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := fixture(t, "decisions-api", tt.fixture)
			out, f, err := provider.DecisionsAPI().Dialect.Request([]byte(`{"state":"x"}`), nil)
			if err != nil || out != nil || f == nil {
				t.Fatalf("Request = %q, %v, %v; want nil, a ResponseFunc, nil", out, f != nil, err)
			}
			tr, err := f(http.StatusOK, http.Header{}, body)
			if err != nil {
				t.Fatalf("ResponseFunc: %v", err)
			}
			start := bytes.Index(body, []byte(`"result":`)) + len(`"result":`)
			end := bytes.Index(body, []byte(`,"creditsUsed"`))
			if want := body[start:end]; !bytes.Equal(tr.Body, want) {
				t.Errorf("Body = %s, want data.result %s", tr.Body, want)
			}
			if len(tr.Body) == 0 || &tr.Body[0] != &body[start] {
				t.Error("Body is not a slice of the response body")
			}
			if cap(tr.Body) != len(tr.Body) {
				t.Errorf("cap(Body) = %d, want %d: an append to it must not write into the response body", cap(tr.Body), len(tr.Body))
			}
		})
	}
}

// TestDecisionsAPIDecode decodes the recorded typed-response.json through a
// client of the preset, as Ask and DecodeAs read it, to the recorded values.
func TestDecisionsAPIDecode(t *testing.T) {
	reply := testsupport.JSON(http.StatusOK, fixture(t, "decisions-api", "typed-response.json"))
	rec := &testsupport.Recorder{Replies: []testsupport.Reply{reply}}
	c := presetClient(t, provider.DecisionsAPI(), rec, decision.WithModel("jev-latest"))
	resp, err := c.SystemOne(t.Context(), recordedState, ticketPrepared(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if !strings.HasPrefix(string(resp.Meta().RawBody()), `{"model":"typesafe/jev-1.13-20260917","answers":`) {
		t.Errorf("RawBody() = %s, want data.result", resp.Meta().RawBody())
	}
	if resp.Model() != "typesafe/jev-1.13-20260917" {
		t.Errorf("Model() = %q", resp.Model())
	}
	if in, _ := resp.Usage().InputTokens(); in != 389 {
		t.Errorf("InputTokens() = %d, want 389", in)
	}
	tk, err := decision.DecodeAs[liveTicket](resp)
	if err != nil {
		t.Fatalf("DecodeAs: %v", err)
	}
	want := ticketView{
		Billing: 0.98, Tone: "frustrated", ToneConfidence: 0.88,
		ToneProbabilities: map[string]float64{"calm": 0, "frustrated": 0.92, "angry": 0.08},
		Urgency:           1.92, UrgencyConfidence: 0.88,
		Legend:       map[uint32]string{0: "can wait", 1: "this week", 2: "today"},
		UrgencyProbs: map[uint32]float64{0: 0, 1: 0.08, 2: 0.92},
	}
	if diff := gocmp.Diff(want, viewTicket(tk)); diff != "" {
		t.Errorf("answers (-want +got):\n%s", diff)
	}
	if sent := rec.Requests()[0].Body; !bytes.HasPrefix(sent, []byte(`{"state":{"subject"`)) {
		t.Errorf("sent %s, want the System One body, which the dialect leaves as it is", sent)
	}
}

// TestDecisionsAPIUnwrapIndented pins that data.result is cut from the body
// at its exact bytes when the envelope is indented, with whitespace before,
// inside and after the object: the slice holds the object alone, starts at
// its opening brace and ends at its closing one.
func TestDecisionsAPIUnwrapIndented(t *testing.T) {
	const result = "{ \"model\" : \"m\",\n    \"usage\": {},\t\"answers\" : { } }"
	body := []byte("{\n  \"code\" : 0,\n  \"data\" : {\n    \"result\" :\n\t  " + result + "  \n ,\n \"creditsUsed\": 1 }\n}\n")
	tr, err := respond(t, provider.DecisionsAPI(), nil, nil)(http.StatusOK, http.Header{}, body)
	if err != nil {
		t.Fatalf("ResponseFunc: %v", err)
	}
	start := bytes.Index(body, []byte(result))
	if string(tr.Body) != result {
		t.Errorf("Body = %q, want %q", tr.Body, result)
	}
	if len(tr.Body) == 0 || &tr.Body[0] != &body[start] {
		t.Error("Body is not the slice of the response body that holds data.result")
	}
}

// TestDecisionsAPIEnvelope pins how the envelope of a 2xx is read: a member
// it does not know is ignored, at any level; a code that is not 0, a missing
// code or data.result, a data.result that is not an object and a body that
// is not one JSON object are failures at their path.
func TestDecisionsAPIEnvelope(t *testing.T) {
	const result = `{"model":"m","usage":{},"answers":{}}`
	tests := map[string]struct {
		body     string
		wantPath string // "-" for success
	}{
		"success: the envelope":                         {body: `{"code":0,"message":"ok","data":{"requestId":"r","result":` + result + `}}`, wantPath: "-"},
		"success: members it does not know are ignored": {body: `{"new":[1],"code":0,"data":{"x":{"y":2},"result":` + result + `,"z":null},"tail":"t"}`, wantPath: "-"},
		"success: code written 0.0":                     {body: `{"code":0.0,"data":{"result":` + result + `}}`, wantPath: "-"},
		"success: a trailing newline":                   {body: `{"code":0,"data":{"result":` + result + "}}\n", wantPath: "-"},
		"error: code is 1":                              {body: `{"code":1,"data":{"result":` + result + `}}`, wantPath: "code"},
		"error: code is a string":                       {body: `{"code":"0","data":{"result":` + result + `}}`, wantPath: "code"},
		"error: no code":                                {body: `{"data":{"result":` + result + `}}`, wantPath: "code"},
		"error: no data":                                {body: `{"code":0}`, wantPath: "data.result"},
		"error: no data.result":                         {body: `{"code":0,"data":{"requestId":"r"}}`, wantPath: "data.result"},
		"error: data.result is an array":                {body: `{"code":0,"data":{"result":[]}}`, wantPath: "data.result"},
		"error: data is a string":                       {body: `{"code":0,"data":"x"}`, wantPath: "data"},
		"error: not JSON":                               {body: `<html>`, wantPath: ""},
		"error: an empty body":                          {body: ``, wantPath: ""},
		"error: data after the object":                  {body: `{"code":0,"data":{"result":` + result + `}} {}`, wantPath: ""},
		"error: an array":                               {body: `[` + result + `]`, wantPath: ""},
	}
	f := respond(t, provider.DecisionsAPI(), nil, nil)
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tr, err := f(http.StatusOK, http.Header{}, []byte(tt.body))
			if tt.wantPath == "-" {
				if err != nil {
					t.Fatalf("ResponseFunc: %v", err)
				}
				if string(tr.Body) != result {
					t.Errorf("Body = %s, want %s", tr.Body, result)
				}
				return
			}
			if got := pathOf(t, err); got != tt.wantPath {
				t.Errorf("Path = %q, want %q (%v)", got, tt.wantPath, err)
			}
		})
	}
}

// TestDecisionsAPIErrors pins each failure status: the body is kept as the
// vendor sent it, the message is its "message", and the ErrorType is
// authentication_error for 401 and api_usage_error for 400, so that
// IsAuthentication holds on the recorded 401; the dialect leaves a 2xx
// envelope's failure to the call (TestDecisionsAPIEnvelope).
func TestDecisionsAPIErrors(t *testing.T) {
	wrongKey := fixture(t, "decisions-api", "wrong-key.json")
	tests := map[string]struct {
		status        int
		body          []byte
		wantErrorType string
		wantMessage   string
		wantAuth      bool
	}{
		"error: the recorded wrong key (401)": {
			status: http.StatusUnauthorized, body: wrongKey,
			wantErrorType: "authentication_error", wantMessage: "Invalid API key.", wantAuth: true,
		},
		"error: an unknown model (400)": {
			status: http.StatusBadRequest, body: []byte(`{"code":-1,"message":"Unknown model."}`),
			wantErrorType: "api_usage_error", wantMessage: "Unknown model.",
		},
		"error: insufficient credits (402)": {
			status: http.StatusPaymentRequired, body: []byte(`{"code":-1,"message":"Insufficient credits."}`),
			wantMessage: "Insufficient credits.",
		},
		"error: a server error (500)": {
			status: http.StatusInternalServerError, body: []byte(`{"code":-1,"message":"boom"}`),
			wantMessage: "boom",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(tt.status, tt.body)}}
			c := presetClient(t, provider.DecisionsAPI(), rec, decision.WithModel("jev-latest"))
			_, err := c.SystemOne(t.Context(), recordedState, ticketPrepared(t))
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
