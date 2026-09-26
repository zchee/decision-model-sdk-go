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

package testsupport

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// TestFakeAPI checks the stand-in API the examples run against: each route,
// each question type's answer, the missing-credential and validation
// failures, and the request ids.
func TestFakeAPI(t *testing.T) {
	tests := map[string]struct {
		method, path, auth, body string
		wantStatus               int
		want                     string // the body as JSON, BODYLEN standing for the request body's length; compared as values
	}{
		"success: models": {
			method: http.MethodGet, path: "/v1/models", auth: "Bearer k",
			wantStatus: http.StatusOK, want: FakeAPIModels,
		},
		"success: every question type answered, an unknown one skipped": {
			method: http.MethodPost, path: "/v1/systemone", auth: "Bearer k",
			body: `{"state":"s","model":"jev-latest","questions":{` +
				`"n":{"type":"noul","instructions":"?"},` +
				`"c":{"type":"choice","criteria":{"tech":null,"billing":"payments"}},` +
				`"s":{"type":"score","criteria":["low",{"summary":"high"}]},` +
				`"x":{"type":"future"}}}`,
			wantStatus: http.StatusOK,
			want: `{"model":"jev-latest","usage":{"input_tokens":BODYLEN,"output_tokens":3},"answers":{` +
				`"n":{"type":"noul","noul":0.75},` +
				`"c":{"type":"choice","choice":"billing","confidence":1,"probabilities":{"billing":1,"tech":0}},` +
				`"s":{"type":"score","score":1,"confidence":1,"legend":{"0":"low","1":{"summary":"high"}},"probabilities":{"0":0,"1":1}}}}`,
		},
		"error: no credential": {
			method: http.MethodGet, path: "/v1/models",
			wantStatus: http.StatusUnauthorized,
			want:       `{"detail":{"error_type":"authentication_error","message":"Cannot authenticate with the server. Please check your API key and try again."}}`,
		},
		"error: a body without questions": {
			method: http.MethodPost, path: "/v1/systemone", auth: "Bearer k",
			body:       `{"state":"s","model":"m"}`,
			wantStatus: http.StatusUnprocessableEntity,
			want:       `{"detail":[{"loc":["body"],"msg":"state, model and questions are required","type":"value_error"}]}`,
		},
		"error: a choice without options": {
			method: http.MethodPost, path: "/v1/systemone", auth: "Bearer k",
			body:       `{"state":"s","model":"m","questions":{"c":{"type":"choice","criteria":{}}}}`,
			wantStatus: http.StatusUnprocessableEntity,
			want:       `{"detail":[{"loc":["questions","c"],"msg":"a choice needs criteria with at least one option","type":"value_error"}]}`,
		},
		"error: an unknown path": {
			method: http.MethodGet, path: "/v2/models", auth: "Bearer k",
			wantStatus: http.StatusNotFound, want: `{"detail":"Not Found"}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			api := &FakeAPI{}
			srv := httptest.NewServer(api)
			t.Cleanup(srv.Close)
			req, err := http.NewRequestWithContext(t.Context(), tt.method, srv.URL+tt.path, strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if id := resp.Header.Get("X-Typesafe-Request-Id"); id != "req_fake_1" || api.Requests() != 1 {
				t.Errorf("request id = %q after %d requests, want req_fake_1 after 1", id, api.Requests())
			}
			var got, want any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("the body is not JSON: %v\n%s", err, body)
			}
			if err := json.Unmarshal([]byte(strings.ReplaceAll(tt.want, "BODYLEN", strconv.Itoa(len(tt.body)))), &want); err != nil {
				t.Fatal(err)
			}
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
		})
	}
}
