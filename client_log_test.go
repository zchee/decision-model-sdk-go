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
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// recordsText renders every record logs kept, one per line.
func recordsText(logs *testsupport.LogRecorder) string {
	var sb strings.Builder
	for _, r := range logs.Records() {
		sb.WriteString(r.String())
		sb.WriteByte('\n')
	}
	return sb.String()
}

// TestClientLogsNoCredential checks AC-F5 through the client, with the nine
// header spellings and three statuses of test_secret_headers_redacted
// (tests/test_logging.py:14-67): a credential in a request header the
// caller set, in the same response header, and the API key never appear in
// any record down to LevelTrace or in the error's text (every verb that
// prints it through Error), while the other headers' values do, and "***"
// stands for the redacted ones. %#v of an *APIError dumps its exported
// fields, the response Header among them, as it does for any Go struct. Each call makes one
// attempt; the upstream 429 row's two retries are W3.3's (L1).
func TestClientLogsNoCredential(t *testing.T) {
	// The upstream parametrize grid, 9 spellings x 3 statuses, as a map.
	type test struct {
		header string
		status int
	}
	tests := map[string]test{}
	for _, header := range []string{"Authorization", "Proxy-Authorization", "X-API-Key", "API-Key", "Cookie", "Set-Cookie", "X-Access-Token", "X-Client-Secret", "x-MiXeD-ToKeN"} {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusTooManyRequests} {
			tests["success: "+header+"/"+strconv.Itoa(status)] = test{header: header, status: status}
		}
	}
	for name, tt := range tests {
		header, status := tt.header, tt.status
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"models":[]}`)
			if status != http.StatusOK {
				body = []byte(`{"message":"failure"}`)
			}
			rec := replying(status, body, header, "response-credential", "X-Visible", "response-visible")
			logs := testsupport.NewLogRecorder(LevelTrace)
			clearEnv(t)
			c := newEnvClient(t, rec, WithAPIKey("auth-credential"), WithHeader(header, "request-credential"), WithHeader("x-visible", "request-visible"), WithLogger(logs.Logger()))
			_, err := c.Models().List(t.Context())
			if (err == nil) != (status == http.StatusOK) {
				t.Fatalf("List error = %v for status %d", err, status)
			}
			if rec.Count() != 1 {
				t.Errorf("the transport saw %d requests, want 1", rec.Count())
			}
			text := recordsText(logs)
			for _, visible := range []string{"request-visible", "response-visible", redacted} {
				if !strings.Contains(text, visible) {
					t.Errorf("the records lack %q:\n%s", visible, text)
				}
			}
			for _, secret := range []string{"auth-credential", "request-credential", "response-credential"} {
				if strings.Contains(text, secret) {
					t.Errorf("the records hold %q:\n%s", secret, text)
				}
				if err == nil {
					continue
				}
				for _, verb := range []string{"%v", "%+v", "%s", "%q"} {
					if out := fmt.Sprintf(verb, err); strings.Contains(out, secret) {
						t.Errorf("%s of the error holds %q: %s", verb, secret, out)
					}
				}
			}
		})
	}
}

// TestClientLogRecords pins the records of one attempt at each level: INFO
// "response" with the method, the endpoint (its path alone under
// WithLogEndpointHost(false)), the status, the time taken, the request id
// escaped and cut, and the attempt; DEBUG "request" and "response headers"
// with the redacted headers and the body lengths; LevelTrace the bodies. A
// transport failure is one INFO "request failed" record with the SDK error's
// text. Nothing is logged below the logger's level.
func TestClientLogRecords(t *testing.T) {
	result := testsupport.Fixture(t, "result.json")
	tests := map[string]struct {
		level    slog.Level
		opts     []ClientOption
		endpoint string
		want     []string // "LEVEL message" of every record, in order
	}{
		"success: INFO": {
			level: slog.LevelInfo, endpoint: "https://api.typesafe.ai/v1/systemone",
			want: []string{"INFO response"},
		},
		"success: DEBUG": {
			level: slog.LevelDebug, endpoint: "https://api.typesafe.ai/v1/systemone",
			want: []string{"DEBUG request", "INFO response", "DEBUG response headers"},
		},
		"success: LevelTrace, the endpoint's path alone": {
			level: LevelTrace, opts: []ClientOption{WithLogEndpointHost(false)}, endpoint: "/v1/systemone",
			want: []string{"DEBUG request", "DEBUG-4 request body", "INFO response", "DEBUG response headers", "DEBUG-4 response body"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			logs := testsupport.NewLogRecorder(tt.level)
			rec := replying(http.StatusOK, result, "X-Typesafe-Request-Id", "req\nlog")
			c := newTestClient(t, rec, append(tt.opts, WithLogger(logs.Logger()))...)
			if _, err := c.SystemOne(t.Context(), "hello", noulQuestion(t)); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			var got []string
			for _, r := range logs.Records() {
				got = append(got, r.Level.String()+" "+r.Message)
				if v, ok := r.Attr("endpoint"); !ok || v.String() != tt.endpoint {
					t.Errorf("%s endpoint = %v, want %q", r.Message, v, tt.endpoint)
				}
				switch r.Message {
				case "response":
					status, _ := r.Attr("status")
					id, _ := r.Attr("request_id")
					attempt, _ := r.Attr("attempt")
					_, hasDuration := r.Attr("duration")
					if status.Int64() != 200 || id.String() != `req\nlog` || attempt.Int64() != 0 || !hasDuration {
						t.Errorf("response record %s", r)
					}
				case "request":
					auth, _ := r.Attr("headers.Authorization")
					n, _ := r.Attr("body_bytes")
					if auth.String() != redacted || n.Int64() != int64(len(`{"state":"hello","model":"jev-latest",`+noulBody+`}`)) {
						t.Errorf("request record %s", r)
					}
				case "response body":
					if b, _ := r.Attr("body"); b.String() != string(result) {
						t.Errorf("response body record %s", r)
					}
				}
			}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("records (-want +got):\n%s", diff)
			}
		})
	}
	t.Run("success: a transport failure", func(t *testing.T) {
		logs := testsupport.NewLogRecorder(slog.LevelInfo)
		rec := &testsupport.Recorder{Replies: []testsupport.Reply{{Err: errString("dial refused")}}}
		c := newTestClient(t, rec, WithLogger(logs.Logger()))
		if _, err := c.Models().List(t.Context()); err == nil {
			t.Fatal("List succeeded, want a transport failure")
		}
		records := logs.Records()
		if len(records) != 1 || records[0].Message != "request failed" {
			t.Fatalf("records:\n%s", recordsText(logs))
		}
		if v, _ := records[0].Attr("error"); v.String() != "Connection error: dial refused" {
			t.Errorf("error attribute = %q", v)
		}
	})
}

// errString is an error with a fixed text.
type errString string

func (e errString) Error() string { return string(e) }
