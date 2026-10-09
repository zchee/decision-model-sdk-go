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

package cassette

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
)

func TestRecorderProviderBodyIDs(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		body     string
		want     string
		response bool
	}{
		"success: Responses root":                    {body: `{"id":"resp_CedarMadeUp"}`, want: `{"id":"x"}`, response: true},
		"success: Chat root":                         {body: `{"id":"chatcmpl-CedarMadeUp"}`, want: `{"id":"x"}`, response: true},
		"success: Anthropic root":                    {body: `{"id":"msg_CedarMadeUp"}`, want: `{"id":"x"}`, response: true},
		"success: Gemini root":                       {body: `{"id":"v1_CedarMadeUp"}`, want: `{"id":"x"}`, response: true},
		"success: opaque response root":              {body: `{"id":"Cedar_42-madeup"}`, want: `{"id":"x"}`, response: true},
		"success: message output":                    {body: `{"output":[{"id":"msg_CedarMadeUp"}]}`, want: `{"output":[{"id":"x"}]}`, response: true},
		"success: function output":                   {body: `{"output":[{"id":"fc_CedarMadeUp"}]}`, want: `{"output":[{"id":"x"}]}`, response: true},
		"success: reasoning output":                  {body: `{"output":[{"id":"rs_CedarMadeUp"}]}`, want: `{"output":[{"id":"x"}]}`, response: true},
		"success: opaque output":                     {body: `{"output":[{"id":"CedarMadeUp"}]}`, want: `{"output":[{"id":"x"}]}`, response: true},
		"success: Gemini function step":              {body: `{"steps":[{"type":"function_call","id":"CedarMadeUp","name":"evaluation"}]}`, want: `{"steps":[{"type":"function_call","id":"x","name":"evaluation"}]}`, response: true},
		"success: Gemini untyped step":               {body: `{"steps":[{"id":"CedarMadeUp"}]}`, want: `{"steps":[{"id":"x"}]}`, response: true},
		"success: prior response request":            {body: `{"previous_response_id":"resp_CedarMadeUp"}`, want: `{"previous_response_id":"x"}`},
		"success: prior interaction request":         {body: `{"previous_interaction_id":"CedarMadeUp"}`, want: `{"previous_interaction_id":"x"}`},
		"success: message input request":             {body: `{"input":[{"id":"msg_CedarMadeUp"}]}`, want: `{"input":[{"id":"x"}]}`},
		"success: identifier boundary":               {body: `{"id":"_-09AZaz"}`, want: `{"id":"x"}`, response: true},
		"success: request root kept":                 {body: `{"id":"resp_CedarMadeUp"}`, want: `{"id":"resp_CedarMadeUp"}`},
		"success: request questions kept":            {body: `{"questions":[{"id":"rating"},{"id":"instruction_probe"},{"id":"criteria_probe"}]}`, want: `{"questions":[{"id":"rating"},{"id":"instruction_probe"},{"id":"criteria_probe"}]}`},
		"success: input question ids kept":           {body: `{"input":[{"id":"rating"},{"id":"instruction_probe"},{"id":"criteria_probe"}]}`, want: `{"input":[{"id":"rating"},{"id":"instruction_probe"},{"id":"criteria_probe"}]}`},
		"success: evaluation names kept":             {body: `{"text":{"format":{"name":"evaluation_probabilities"}},"tools":[{"name":"evaluation_probe"}]}`, want: `{"text":{"format":{"name":"evaluation_probabilities"}},"tools":[{"name":"evaluation_probe"}]}`},
		"success: response names and signature kept": {body: `{"name":"evaluation","steps":[{"id":"CedarMadeUp","name":"evaluation_probe","signature":"synthetic-signed-state"}]}`, want: `{"name":"evaluation","steps":[{"id":"x","name":"evaluation_probe","signature":"synthetic-signed-state"}]}`, response: true},
		"success: metadata id kept":                  {body: `{"metadata":{"id":"msg_CedarMadeUp"}}`, want: `{"metadata":{"id":"msg_CedarMadeUp"}}`, response: true},
		"success: nested output metadata kept":       {body: `{"output":[{"id":"msg_CedarMadeUp","metadata":{"id":"msg_BirchMadeUp"}}]}`, want: `{"output":[{"id":"x","metadata":{"id":"msg_BirchMadeUp"}}]}`, response: true},
		"success: numeric and null ids kept":         {body: `{"id":17,"output":[{"id":null},{"id":false},{"id":12}]}`, want: `{"id":17,"output":[{"id":null},{"id":false},{"id":12}]}`, response: true},
		"success: non-ASCII and empty ids kept":      {body: `{"id":"","steps":[{"id":"識別子"},{"id":"a.b"},{"id":"a/b"},{"id":"a b"}]}`, want: `{"id":"","steps":[{"id":"識別子"},{"id":"a.b"},{"id":"a/b"},{"id":"a b"}]}`, response: true},
		"success: incomplete request prefixes kept":  {body: `{"previous_response_id":"resp_","input":[{"id":"msg_"}]}`, want: `{"previous_response_id":"resp_","input":[{"id":"msg_"}]}`},
		"success: other request prefixes kept":       {body: `{"previous_response_id":"msg_CedarMadeUp","input":[{"id":"fc_CedarMadeUp"}]}`, want: `{"previous_response_id":"msg_CedarMadeUp","input":[{"id":"fc_CedarMadeUp"}]}`},
		"success: wrong direction kept":              {body: `{"output":[{"id":"msg_CedarMadeUp"}],"steps":[{"id":"CedarMadeUp"}]}`, want: `{"output":[{"id":"msg_CedarMadeUp"}],"steps":[{"id":"CedarMadeUp"}]}`},
		"success: response input reference kept":     {body: `{"input":[{"id":"msg_CedarMadeUp"}],"previous_response_id":"resp_CedarMadeUp"}`, want: `{"input":[{"id":"msg_CedarMadeUp"}],"previous_response_id":"resp_CedarMadeUp"}`, response: true},
		"success: unrelated nested output kept":      {body: `{"metadata":{"output":[{"id":"msg_CedarMadeUp"}]}}`, want: `{"metadata":{"output":[{"id":"msg_CedarMadeUp"}]}}`, response: true},
		"success: dropped proposal paths kept":       {body: `{"request_id":"req_CedarMadeUp","name":"interactions/CedarMadeUp"}`, want: `{"request_id":"req_CedarMadeUp","name":"interactions/CedarMadeUp"}`, response: true},
		"success: array items and ordering kept":     {body: `{"before":1,"output":[null,42,[],"ordinary",{"before":true,"id":"CedarMadeUp","after":false}],"after":2}`, want: `{"before":1,"output":[null,42,[],"ordinary",{"before":true,"id":"x","after":false}],"after":2}`, response: true},
		"success: body array kept":                   {body: `[{"id":"CedarMadeUp"},null,3]`, want: `[{"id":"CedarMadeUp"},null,3]`, response: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requestBody, responseBody := tt.body, `{}`
			if tt.response {
				requestBody, responseBody = `{}`, tt.body
			}
			dir := t.TempDir()
			calls := 0
			recorder, err := NewRecorder(tripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				body, readErr := io.ReadAll(req.Body)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if diff := gocmp.Diff(true, string(body) == requestBody); diff != "" {
					t.Error("outbound request body changed")
				}
				return jsonReply(responseBody), nil
			}), dir, "body.json")
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.invalid/record", strings.NewReader(requestBody))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := recorder.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(resp.Body)
			closeErr := resp.Body.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("reading returned response: read=%v close=%v", readErr, closeErr)
			}
			if diff := gocmp.Diff(true, string(body) == responseBody); diff != "" {
				t.Error("returned response body changed")
			}
			if calls != 1 {
				t.Fatalf("wrapped transport called %d times, want 1", calls)
			}
			cassette, err := Load(filepath.Join(dir, "body.json"))
			if err != nil {
				t.Fatal(err)
			}
			if len(cassette.Interactions) != 1 {
				t.Fatalf("recorded %d interactions, want 1", len(cassette.Interactions))
			}
			got := cassette.Interactions[0].Request.Body
			if tt.response {
				got = cassette.Interactions[0].Response.Body
			}
			if diff := gocmp.Diff(true, string(got) == tt.want); diff != "" {
				t.Errorf("recorded body differs: got %d bytes, want %d bytes; values withheld", len(got), len(tt.want))
			}
		})
	}
}

func TestReplayKeepsProviderBodyIDs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "replay.json")
	const document = `{"version":1,"interactions":[{"request":{"method":"POST","uri":"https://example.invalid/replay","body":{"previous_response_id":"resp_CedarMadeUp"},"headers":{}},"response":{"status":{"code":200,"message":"OK"},"headers":{},"body":{"string":{"id":"msg_CedarMadeUp","output":[{"id":"fc_BirchMadeUp"}]}}}}]}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	tr := c.Transport()
	_, body, err := send(t, tr, http.MethodPost, "https://example.invalid/replay", `{"previous_response_id":"resp_CedarMadeUp"}`)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":"msg_CedarMadeUp","output":[{"id":"fc_BirchMadeUp"}]}`
	if diff := gocmp.Diff(true, string(body) == want); diff != "" {
		t.Error("replay changed the retained provider identifiers")
	}
	if tr.Unconsumed() != 0 {
		t.Error("replay did not consume the original request")
	}
	unchanged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := gocmp.Diff(true, string(unchanged) == document); diff != "" {
		t.Error("replay rewrote its input cassette")
	}
}

func TestRecordedBodyNonObjectForms(t *testing.T) {
	t.Parallel()
	tests := map[string]struct{ body, want string }{
		"success: absent body":   {},
		"success: scalar body":   {body: `"resp_CedarMadeUp"`, want: `"resp_CedarMadeUp"`},
		"success: non-JSON body": {body: "not JSON", want: "not JSON"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			value, err := recordedBody([]byte(tt.body), true)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := jsonx.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			node, err := jsonx.Read(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if node.Kind() != jsonx.KindString || node.Text() != tt.want {
				t.Error("non-object body representation changed")
			}
		})
	}
}
