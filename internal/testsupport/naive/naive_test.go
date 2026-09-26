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

package naive

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// questionsJSON is the q3 question set (tests/test_clients.py:60-80 of the
// Python SDK) as the SDK prepares it.
const questionsJSON = `{"spam":{"type":"noul","instructions":"Spam?"},` +
	`"tone":{"type":"choice","instructions":"Tone?","criteria":{"friendly":null,"hostile":null}},` +
	`"quality":{"type":"score","instructions":"Quality?","criteria":["bad","ok","great"]}}`

// testHeader is a header template of the SDK's shape; the values are
// placeholders.
func testHeader() http.Header {
	return http.Header{
		"Authorization":      {"Bearer test-key"},
		"Accept":             {"application/json"},
		"Content-Type":       {"application/json"},
		"User-Agent":         {"typesafe-sdk-go/test"},
		"X-Typesafe-Sdk":     {"typesafe-sdk-go/test"},
		"X-Typesafe-Runtime": {"go/test"},
	}
}

// resultMap is testdata/result.json as a generic decoder returns it,
// written out by hand so that neither codec checks itself.
func resultMap() map[string]any {
	return map[string]any{
		"model": "jev-latest",
		"usage": map[string]any{"input_tokens": 12.0, "output_tokens": 3.0},
		"answers": map[string]any{
			"spam": map[string]any{"type": "noul", "noul": 0.98},
			"tone": map[string]any{
				"type": "choice", "choice": "friendly", "confidence": 0.9,
				"probabilities": map[string]any{"friendly": 0.9, "hostile": 0.1},
			},
			"quality": map[string]any{
				"type": "score", "score": 1.7, "confidence": 0.8,
				"legend":        map[string]any{"0": "bad", "1": "ok", "2": "great"},
				"probabilities": map[string]any{"0": 0.1, "1": 0.1, "2": 0.8},
			},
		},
	}
}

// codecs are the two comparators, by name.
var codecs = map[string]Codec{"sonic": Sonic, "encoding/json": StdJSON}

// TestClientSystemOne checks one successful call with each codec: the
// request the transport saw (method, URL, every header, the body bytes, the
// declared length, a GetBody for a replay, the deadline) and the decoded
// response.
func TestClientSystemOne(t *testing.T) {
	tests := map[string]struct {
		state    any
		wantBody string
	}{
		"success: a text state": {
			state:    "He said \"refund\"\n\tplease: 請求が二重です 🌍",
			wantBody: `{"state":"He said \"refund\"\n\tplease: 請求が二重です 🌍","model":"jev-latest","questions":` + questionsJSON + `}`,
		},
		"success: a struct state keeps its field order": {
			state: struct {
				Subject string   `json:"subject"`
				Tags    []string `json:"tags"`
			}{Subject: "billing", Tags: []string{"a", "b"}},
			wantBody: `{"state":{"subject":"billing","tags":["a","b"]},"model":"jev-latest","questions":` + questionsJSON + `}`,
		},
		"success: a json.RawMessage state goes out as it is": {
			state:    json.RawMessage(`{"document":"Hello"}`),
			wantBody: `{"state":{"document":"Hello"},"model":"jev-latest","questions":` + questionsJSON + `}`,
		},
	}
	for codecName, codec := range codecs {
		for name, tt := range tests {
			t.Run(codecName+"/"+name, func(t *testing.T) {
				rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, testsupport.Fixture(t, "result.json"))}}
				var deadline time.Time
				var hasDeadline bool
				rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
					deadline, hasDeadline = req.Context().Deadline()
					return rec.RoundTrip(req)
				})
				c := &Client{
					Transport: rt,
					URL:       "https://api.typesafe.ai/v1/systemone",
					Header:    testHeader(),
					Model:     "jev-latest",
					Questions: []byte(questionsJSON),
					Timeout:   10 * time.Second,
					Codec:     codec,
				}
				before := time.Now()
				got, err := c.SystemOne(t.Context(), tt.state)
				if err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
				if diff := gocmp.Diff(resultMap(), got); diff != "" {
					t.Errorf("decoded response (-want +got):\n%s", diff)
				}
				reqs := rec.Requests()
				if len(reqs) != 1 {
					t.Fatalf("the transport saw %d requests, want 1", len(reqs))
				}
				req := reqs[0]
				want := testsupport.RecordedRequest{
					Method:        http.MethodPost,
					URL:           "https://api.typesafe.ai/v1/systemone",
					Host:          "api.typesafe.ai",
					Header:        testHeader(),
					Body:          []byte(tt.wantBody),
					ContentLength: int64(len(tt.wantBody)),
					HasGetBody:    true,
				}
				if diff := gocmp.Diff(want, req); diff != "" {
					t.Errorf("request (-want +got):\n%s\nbody: %s", diff, req.Body)
				}
				if !hasDeadline || deadline.Before(before.Add(10*time.Second)) || deadline.After(time.Now().Add(10*time.Second)) {
					t.Errorf("request deadline = %v (set %t), want 10 s after the call began", deadline, hasDeadline)
				}
			})
		}
	}
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestClientSystemOneErrors checks each failure a naive call reports: a
// status outside 2xx, a transport error, a body the codec refuses, and a
// state it cannot encode, which fails before the transport sees anything.
func TestClientSystemOneErrors(t *testing.T) {
	errDial := errors.New("dial refused")
	tests := map[string]struct {
		reply        testsupport.Reply
		state        any
		wantStatus   int   // a *StatusError with this code, when non-zero
		wantErr      error // errors.Is target, when set
		wantRequests int
	}{
		"error: a 429 is a *StatusError with its body": {
			reply: testsupport.JSON(http.StatusTooManyRequests, []byte(`{"error":"slow down"}`)), state: "x",
			wantStatus: http.StatusTooManyRequests, wantRequests: 1,
		},
		"error: a transport error comes back as it is": {
			reply: testsupport.Reply{Err: errDial}, state: "x",
			wantErr: errDial, wantRequests: 1,
		},
		"error: a body that is not JSON": {
			reply: testsupport.JSON(http.StatusOK, []byte(`{"model":`)), state: "x",
			wantRequests: 1,
		},
		"error: a state that cannot be encoded never reaches the transport": {
			reply: testsupport.JSON(http.StatusOK, []byte(`{}`)), state: make(chan int),
		},
	}
	for codecName, codec := range codecs {
		for name, tt := range tests {
			t.Run(codecName+"/"+name, func(t *testing.T) {
				rec := &testsupport.Recorder{Replies: []testsupport.Reply{tt.reply}}
				c := &Client{Transport: rec, URL: "https://api.typesafe.ai/v1/systemone", Header: testHeader(), Model: "jev-latest", Questions: []byte(questionsJSON), Codec: codec}
				got, err := c.SystemOne(t.Context(), tt.state)
				if err == nil {
					t.Fatalf("SystemOne = %v, want an error", got)
				}
				if tt.wantStatus != 0 {
					se, ok := errors.AsType[*StatusError](err)
					if !ok {
						t.Fatalf("error = %T %v, want a *StatusError", err, err)
					}
					if se.StatusCode != tt.wantStatus || string(se.Body) != string(tt.reply.Body) {
						t.Errorf("StatusError = %d %q, want %d %q", se.StatusCode, se.Body, tt.wantStatus, tt.reply.Body)
					}
					if want := "naive: status " + strconv.Itoa(tt.wantStatus); se.Error() != want {
						t.Errorf("Error() = %q, want %q", se.Error(), want)
					}
				}
				if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
					t.Errorf("error = %v, want %v in its chain", err, tt.wantErr)
				}
				if n := rec.Count(); n != tt.wantRequests {
					t.Errorf("the transport saw %d requests, want %d", n, tt.wantRequests)
				}
			})
		}
	}
}

// jsonEscape returns the six-byte JSON escape of r, as encoding/json writes
// an HTML-special character.
func jsonEscape(r rune) string { return fmt.Sprintf("%cu%04x", 0x5c, r) }

// TestCodecEncodeSpellings pins where the two codecs spell a body alike and
// where they do not, which decides the states for which the naive body is
// the SDK's (the package comment): both write members in Body's order and
// leave multi-byte text as it is; only encoding/json escapes <, > and &.
func TestCodecEncodeSpellings(t *testing.T) {
	tests := map[string]struct {
		state     any
		wantSonic string
		wantStd   string
	}{
		"success: quotes, control characters and multi-byte text": {
			state:     "\"a\"\n\t\x01請🌍",
			wantSonic: `{"state":"\"a\"\n\t\u0001請🌍","model":"m","questions":{}}`,
			wantStd:   `{"state":"\"a\"\n\t\u0001請🌍","model":"m","questions":{}}`,
		},
		"success: HTML-special characters differ": {
			state:     "<a&b>",
			wantSonic: `{"state":"<a&b>","model":"m","questions":{}}`,
			wantStd:   `{"state":"` + jsonEscape('<') + "a" + jsonEscape('&') + "b" + jsonEscape('>') + `","model":"m","questions":{}}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			for _, c := range []struct {
				codec Codec
				want  string
			}{{Sonic, tt.wantSonic}, {StdJSON, tt.wantStd}} {
				got, err := c.codec.Encode(tt.state, "m", []byte(`{}`))
				if err != nil {
					t.Fatalf("%s: Encode: %v", c.codec.Name, err)
				}
				if diff := gocmp.Diff(c.want, string(got)); diff != "" {
					t.Errorf("%s: body (-want +got):\n%s", c.codec.Name, diff)
				}
			}
		})
	}
}
