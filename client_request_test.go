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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// noulBody is the question set {"q": {"type": "noul", "instructions": "?"}}
// as a request body carries it.
const noulBody = `"questions":{"q":{"type":"noul","instructions":"?"}}`

// capture wraps rt and records the context deadline and the GetBody of each
// request before rt sees it.
type capture struct {
	rt        http.RoundTripper
	deadlines []time.Time
	hasDL     []bool
	requests  []*http.Request
}

func (c *capture) RoundTrip(req *http.Request) (*http.Response, error) {
	dl, ok := req.Context().Deadline()
	c.deadlines = append(c.deadlines, dl)
	c.hasDL = append(c.hasDL, ok)
	c.requests = append(c.requests, req)
	return c.rt.RoundTrip(req)
}

// newEnvClient builds a client over rt (WithRoundTripper) from opts alone,
// reading the environment the test set, and closes it when the test ends.
func newEnvClient(t *testing.T, rt http.RoundTripper, opts ...ClientOption) *Client {
	t.Helper()
	c, err := NewClient(append([]ClientOption{WithRoundTripper(rt)}, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestClientExtraBodyShallowOverride re-asserts test_extra_body_shallow_override
// (C2) through the client: an ExtraBody member named "model" replaces the
// call's model where it stands, and the others are appended in order, nil as
// null.
func TestClientExtraBodyShallowOverride(t *testing.T) {
	rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"))
	c := newTestClient(t, rec)
	_, err := c.SystemOne(t.Context(), "hi", noulQuestion(t), Model("call-model"),
		ExtraBody("model", "override-model"), ExtraBody("beam_width", 4), ExtraBody("nullable", nil))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	want := `{"state":"hi","model":"override-model",` + noulBody + `,"beam_width":4,"nullable":null}`
	if diff := gocmp.Diff(want, string(onlyRequest(t, rec).Body)); diff != "" {
		t.Errorf("body (-want +got):\n%s", diff)
	}
}

// TestClientUnencodableBodyFailsBeforeNetwork re-asserts
// test_unserializable_request_body_raises (C3) and the scalar-state refusal
// (T2) through the client: a body that cannot be encoded fails with
// *InvalidRequestError, "could not be encoded as JSON", and the transport
// sees nothing.
func TestClientUnencodableBodyFailsBeforeNetwork(t *testing.T) {
	tests := map[string]struct {
		state any
		opts  []CallOption
		want  string
	}{
		"error: the upstream test: an extra member with no JSON form": {
			state: "x", opts: []CallOption{ExtraBody("bad", make(chan int))},
			want: `The request body could not be encoded as JSON: extra body member "bad"`,
		},
		"error: a nil state (T2)":     {state: nil, want: "state: nil encodes as null"},
		"error: a boolean state (T2)": {state: true, want: "state: bool encodes as a boolean"},
		"error: a number state (T2)":  {state: 1.5, want: "state: float64 encodes as a number"},
		"error: a NaN in the state":   {state: map[string]any{"v": nanValue()}, want: "could not be encoded as JSON"},
		"error: a plain []byte state": {state: []byte(`{}`), want: "send string(b) for text or RawJSON(b) for JSON"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"))
			c := newTestClient(t, rec)
			_, err := c.SystemOne(t.Context(), tt.state, noulQuestion(t), tt.opts...)
			if _, ok := errors.AsType[*InvalidRequestError](err); !ok {
				t.Fatalf("SystemOne error = %v (%T), want an *InvalidRequestError", err, err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err, tt.want)
			}
			if rec.Count() != 0 {
				t.Errorf("the transport saw %d requests, want 0", rec.Count())
			}
		})
	}
}

// nanValue returns NaN without a constant expression the compiler folds.
func nanValue() float64 {
	zero := 0.0
	return zero / zero
}

// TestClientStateForms re-asserts T1 and T6 through the client: a named
// string type is sent as text, and a map, a slice and a struct as JSON.
func TestClientStateForms(t *testing.T) {
	type ticketID string
	type ticket struct {
		Subject string `json:"subject"`
		Tags    []string
	}
	tests := map[string]struct {
		state any
		want  string
	}{
		"success: a named string type is text (T1)": {state: ticketID("T-1"), want: `"T-1"`},
		"success: a map":    {state: map[string]any{"k": 1}, want: `{"k":1}`},
		"success: a slice":  {state: []string{"a", "b"}, want: `["a","b"]`},
		"success: a struct": {state: ticket{Subject: "s", Tags: []string{"x"}}, want: `{"subject":"s","Tags":["x"]}`},
		"success: RawJSON":  {state: RawJSON(`{"a": 1}`), want: `{"a": 1}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"))
			c := newTestClient(t, rec)
			if _, err := c.SystemOne(t.Context(), tt.state, noulQuestion(t)); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			want := `{"state":` + tt.want + `,"model":"jev-latest",` + noulBody + `}`
			if diff := gocmp.Diff(want, string(onlyRequest(t, rec).Body)); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRawQuestionPassthrough ports test_raw_question_passthrough (C4): the
// members of a raw question that the SDK does not model are sent as given,
// nested nulls included, after "type" in key order (ruling R38).
func TestRawQuestionPassthrough(t *testing.T) {
	rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"))
	c := newTestClient(t, rec)
	qs := mustPrepared(t, NewQuestions().
		Raw("q", RawQuestion{Type: "noul", Fields: map[string]any{"instructions": "Spam?", "weight": 3, "nested": map[string]any{"k": nil}}}).
		Raw("choice", RawQuestion{Type: "choice", Fields: map[string]any{"criteria": map[string]any{"a": nil}, "weight": 2}}).
		Raw("score", RawQuestion{Type: "score", Fields: map[string]any{"criteria": []any{"good"}, "weight": 1}}))
	if _, err := c.SystemOne(t.Context(), "hi", qs); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	want := `{"state":"hi","model":"jev-latest","questions":{` +
		`"q":{"type":"noul","instructions":"Spam?","nested":{"k":null},"weight":3},` +
		`"choice":{"type":"choice","criteria":{"a":null},"weight":2},` +
		`"score":{"type":"score","criteria":["good"],"weight":1}}}`
	if diff := gocmp.Diff(want, string(onlyRequest(t, rec).Body)); diff != "" {
		t.Errorf("body (-want +got):\n%s", diff)
	}
}

// TestRawQuestionSchemaLeftToAPI ports
// test_question_schema_validation_is_left_to_api (C5): a raw question whose
// members the API would refuse is sent as it is, and the API's 422 comes
// back as an *APIError of the unprocessable-entity kind with its message.
func TestRawQuestionSchemaLeftToAPI(t *testing.T) {
	tests := map[string]struct {
		question RawQuestion
		want     string
	}{
		"success: a number as instructions": {
			question: RawQuestion{Type: "noul", Fields: map[string]any{"instructions": 1}},
			want:     `"q":{"type":"noul","instructions":1}`,
		},
		"success: choice criteria as a list": {
			question: RawQuestion{Type: "choice", Fields: map[string]any{"criteria": []any{"invalid", "shape"}}},
			want:     `"q":{"type":"choice","criteria":["invalid","shape"]}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(http.StatusUnprocessableEntity, []byte(`{"detail":"Invalid question"}`))
			c := newTestClient(t, rec)
			_, err := c.SystemOne(t.Context(), "x", mustPrepared(t, NewQuestions().Raw("q", tt.question)))
			ae, ok := errors.AsType[*APIError](err)
			if !ok {
				t.Fatalf("SystemOne error = %v (%T), want an *APIError", err, err)
			}
			if ae.Kind != APIErrorUnprocessableEntity || ae.Message != "Invalid question" {
				t.Errorf("APIError kind %v message %q, want unprocessable entity, \"Invalid question\"", ae.Kind, ae.Message)
			}
			body := string(onlyRequest(t, rec).Body)
			if !strings.Contains(body, `"questions":{`+tt.want+`}`) {
				t.Errorf("body %s does not carry the question %s", body, tt.want)
			}
		})
	}
}

// TestStructuredContentRoundTrip ports test_rich_descriptions (C6): JSON
// content in a raw question's instructions and criteria, in a choice
// option's description and in a score level is sent as JSON, and a legend
// level that echoes the JSON comes back with its exact bytes.
func TestStructuredContentRoundTrip(t *testing.T) {
	const criteria = `{"summary":"duplicated","examples":["charged twice"]}`
	reply := `{"model":"custom","usage":{"input_tokens":1,"output_tokens":1},"answers":{"risk":{"type":"score","score":0,"confidence":1,"legend":{"0":` + criteria + `},"probabilities":{"0":1}}}}`
	rec := replying(http.StatusOK, []byte(reply))
	c := newTestClient(t, rec)
	qs := mustPrepared(t, NewQuestions().
		Raw("duplicate", RawQuestion{Type: "noul", Fields: map[string]any{
			"instructions": JSON([]byte(`{"question":"Duplicate?"}`)),
			"criteria":     JSON([]byte(`{"true":` + criteria + `}`)),
		}}).
		Choice("team", Choice{Instructions: Text("Team?"), Options: Options{{Label: "billing", Description: JSON([]byte(criteria))}, {Label: "other"}}}).
		Score("risk", Score{Instructions: Text("Risk?"), Levels: []Content{JSON([]byte(criteria))}}))
	resp, err := c.SystemOne(t.Context(), "a ticket", qs, Model("custom"))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	want := `{"state":"a ticket","model":"custom","questions":{` +
		`"duplicate":{"type":"noul","criteria":{"true":` + criteria + `},"instructions":{"question":"Duplicate?"}},` +
		`"team":{"type":"choice","instructions":"Team?","criteria":{"billing":` + criteria + `,"other":null}},` +
		`"risk":{"type":"score","instructions":"Risk?","criteria":[` + criteria + `]}}}`
	if diff := gocmp.Diff(want, string(onlyRequest(t, rec).Body)); diff != "" {
		t.Errorf("body (-want +got):\n%s", diff)
	}
	risk, ok := resp.Answers().Score("risk")
	if !ok {
		t.Fatal(`Answers().Score("risk") is missing`)
	}
	d, ok := risk.Description(0)
	if !ok || !d.IsJSON() {
		t.Fatalf("legend level 0 = %v, %t, want JSON content", d, ok)
	}
	if diff := gocmp.Diff(criteria, string(d.JSON())); diff != "" {
		t.Errorf("legend level 0 (-want +got):\n%s", diff)
	}
}

// TestQuestionValidationBeforeNetwork ports test_validation_before_network
// (C10): no question at all, and a score question without levels, fail with
// a *ConfigError before anything is sent. The Go port refuses the second at
// Prepare, where the question set is built, so it never reaches the client.
func TestQuestionValidationBeforeNetwork(t *testing.T) {
	rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"))
	c := newTestClient(t, rec)
	empty, err := NewQuestions().Prepare()
	if err == nil {
		t.Run("error: an empty question set", func(t *testing.T) {
			_, err := c.SystemOne(t.Context(), "x", empty)
			if _, ok := errors.AsType[*ConfigError](err); !ok || !strings.Contains(err.Error(), "At least one question") {
				t.Errorf("SystemOne error = %v, want a *ConfigError with \"At least one question\"", err)
			}
		})
	} else if !strings.Contains(err.Error(), "At least one question") {
		t.Errorf("Prepare of no question = %v, want \"At least one question\"", err)
	}
	t.Run("error: no question set", func(t *testing.T) {
		_, err := c.SystemOne(t.Context(), "x", nil)
		if _, ok := errors.AsType[*ConfigError](err); !ok || !strings.Contains(err.Error(), "At least one question") {
			t.Errorf("SystemOne error = %v, want a *ConfigError with \"At least one question\"", err)
		}
	})
	t.Run("error: a score question without levels", func(t *testing.T) {
		_, err := NewQuestions().Score("rating", Score{Instructions: Text("?")}).Prepare()
		if _, ok := errors.AsType[*ConfigError](err); !ok || !strings.Contains(err.Error(), `"rating" has no criteria`) {
			t.Errorf("Prepare error = %v, want a *ConfigError with %q", err, `"rating" has no criteria`)
		}
	})
	if rec.Count() != 0 {
		t.Errorf("the transport saw %d requests, want 0", rec.Count())
	}
}

// TestModelOverridePerCall ports test_model_override (F2): the call's Model
// wins over the client's WithModel, which applies when the call names none.
func TestModelOverridePerCall(t *testing.T) {
	tests := map[string]struct {
		opts []CallOption
		want string
	}{
		"success: the client's model":         {want: "client-model"},
		"success: the call's model":           {opts: []CallOption{Model("request-model")}, want: "request-model"},
		"success: the last Model option wins": {opts: []CallOption{Model("first"), Model("request-model")}, want: "request-model"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"))
			c := newTestClient(t, rec, WithModel("client-model"))
			if _, err := c.SystemOne(t.Context(), "hello", noulQuestion(t), tt.opts...); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			want := `{"state":"hello","model":"` + tt.want + `",` + noulBody + `}`
			if diff := gocmp.Diff(want, string(onlyRequest(t, rec).Body)); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
		})
	}
}

// TestConfigResolutionOnTheWire ports test_resolution's wire half (F3): the
// key, the base URL and the model come from the options, else from the
// environment (trimmed, trailing slashes dropped), else from the defaults,
// and each attempt has the default 10 s deadline. TestConfigResolutionOrder
// checks the resolution itself.
func TestConfigResolutionOnTheWire(t *testing.T) {
	tests := map[string]struct {
		env                       map[string]string
		opts                      []ClientOption
		wantKey, wantURL, wantMod string
	}{
		"success: default": {
			opts:    []ClientOption{WithAPIKey(testKey)},
			wantKey: testKey, wantURL: "https://api.typesafe.ai/v1/systemone", wantMod: "jev-latest",
		},
		"success: env": {
			env:     map[string]string{APIKeyEnv: "  env-key  ", BaseURLEnv: "  https://env.test///  ", DefaultModelEnv: "  env-model  "}, //nolint:gosec // G101: a test's stand-in key.
			wantKey: "env-key", wantURL: "https://env.test/v1/systemone", wantMod: "env-model",
		},
		"success: options win over env": {
			env:     map[string]string{APIKeyEnv: "  env-key  ", BaseURLEnv: "  https://env.test///  ", DefaultModelEnv: "  env-model  "}, //nolint:gosec // G101: a test's stand-in key.
			opts:    []ClientOption{WithAPIKey("code-key"), WithBaseURL("https://code.test///"), WithModel("code-model")},
			wantKey: "code-key", wantURL: "https://code.test/v1/systemone", wantMod: "code-model",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"))
			cp := &capture{rt: rec}
			c := newEnvClient(t, cp, tt.opts...)
			before := time.Now()
			if _, err := c.SystemOne(t.Context(), "hello", noulQuestion(t)); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			req := onlyRequest(t, rec)
			got := []string{req.Header.Get("Authorization"), req.URL, string(req.Body)}
			want := []string{"Bearer " + tt.wantKey, tt.wantURL, `{"state":"hello","model":"` + tt.wantMod + `",` + noulBody + `}`}
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("Authorization, URL, body (-want +got):\n%s", diff)
			}
			assertDeadline(t, cp, 0, before, DefaultTimeout)
		})
	}
}

// assertDeadline checks that request i of cp had a deadline d after a time
// between before and the check.
func assertDeadline(t *testing.T, cp *capture, i int, before time.Time, d time.Duration) {
	t.Helper()
	after := time.Now()
	if !cp.hasDL[i] {
		t.Fatalf("request %d has no deadline, want one %v away", i, d)
	}
	if dl := cp.deadlines[i]; dl.Before(before.Add(d)) || dl.After(after.Add(d)) {
		t.Errorf("request %d deadline %v is not %v after the call (%v to %v)", i, dl, d, before, after)
	}
}

// TestAPIKeyTrimmedOnTheWire is test_api_key_whitespace's wire half (F5):
// a key padded with whitespace, from the environment or from WithAPIKey,
// reaches Authorization trimmed. TestAPIKeyTrimmed checks the resolution.
func TestAPIKeyTrimmedOnTheWire(t *testing.T) {
	for _, padding := range []string{"", "\n", "\r\n", " \t\r\n "} {
		for _, source := range []string{"env", "option"} {
			t.Run(fmt.Sprintf("success: %s padded %q", source, padding), func(t *testing.T) {
				clearEnv(t)
				key := padding + testKey + padding
				var opts []ClientOption
				if source == "env" {
					t.Setenv(APIKeyEnv, key)
				} else {
					t.Setenv(APIKeyEnv, "env-key")
					opts = append(opts, WithAPIKey(key))
				}
				rec := replying(http.StatusOK, []byte(`{"models":[]}`))
				c := newEnvClient(t, rec, opts...)
				resp, err := c.Models().List(t.Context())
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				if n := len(resp.Models()); n != 0 {
					t.Errorf("List returned %d models, want 0", n)
				}
				if diff := gocmp.Diff("Bearer "+testKey, onlyRequest(t, rec).Header.Get("Authorization")); diff != "" {
					t.Errorf("Authorization (-want +got):\n%s", diff)
				}
			})
		}
	}
}

// TestBlankEnvIsUnsetOnTheWire is test_empty_env_unset's wire half (F8): a
// blank TYPESAFE_BASE_URL and TYPESAFE_DEFAULT_MODEL count as unset, so the
// request goes to the default URL with the default model;
// TYPESAFE_LOG_LEVEL is not read at all. TestBlankEnvIsUnset checks the
// resolution.
func TestBlankEnvIsUnsetOnTheWire(t *testing.T) {
	clearEnv(t)
	for _, name := range []string{BaseURLEnv, DefaultModelEnv, "TYPESAFE_LOG_LEVEL"} {
		t.Setenv(name, " \t ")
	}
	rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"))
	c := newEnvClient(t, rec, WithAPIKey(testKey))
	if _, err := c.SystemOne(t.Context(), "x", noulQuestion(t)); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	req := onlyRequest(t, rec)
	got := []string{req.URL, string(req.Body)}
	want := []string{"https://api.typesafe.ai/v1/systemone", `{"state":"x","model":"jev-latest",` + noulBody + `}`}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("URL, body (-want +got):\n%s", diff)
	}
}

// TestProtectedHeadersAndPrefixBaseURL ports test_headers_timeout_and_logging
// (C15): a base URL with a path prefix and trailing slashes posts under the
// prefix; the SDK's own headers win over the caller's, both the client's
// (WithHeader) and the call's (Header); a call header replaces a client
// header of the same name whatever its case; X-TypeSafe-Retry-Count is
// never sent on a first attempt, even when a call sets it; the call's
// Timeout is the attempt's deadline. The records, down to LevelTrace, hold
// the request id and the state and none of the credentials: the key, the
// injected Authorization, X-API-Key, Cookie and the response's Set-Cookie.
func TestProtectedHeadersAndPrefixBaseURL(t *testing.T) {
	protected := [][2]string{
		{"authorization", "injected-secret"},
		{"accept", "text/plain"},
		{"user-agent", "wrong"},
		{"x-typesafe-sdk", "wrong"},
		{"x-typesafe-runtime", "wrong"},
	}
	opts := []ClientOption{WithBaseURL("https://example.test/prefix///"), WithTimeout(7 * time.Second)}
	for _, h := range protected {
		opts = append(opts, WithHeader(h[0], h[1]))
	}
	opts = append(opts, WithHeader("X-Team", "default"), WithHeader("X-Default", "kept"), WithHeader("X-API-Key", "key-secret"), WithHeader("cookie", "cookie-secret"))
	logs := testsupport.NewLogRecorder(LevelTrace)
	opts = append(opts, WithLogger(logs.Logger()))
	var callOpts []CallOption
	for _, h := range protected {
		callOpts = append(callOpts, Header(h[0], h[1]))
	}
	callOpts = append(callOpts, Header("x-team", "call"), Header("x-typesafe-retry-count", "99"), Header("content-type", "wrong"), Timeout(2*time.Second))

	rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"), "Set-Cookie", "response-secret", "X-Typesafe-Request-Id", "req_log")
	cp := &capture{rt: rec}
	c := newTestClient(t, cp, opts...)
	before := time.Now()
	if _, err := c.SystemOne(t.Context(), "hello", noulQuestion(t), callOpts...); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	req := onlyRequest(t, rec)
	if diff := gocmp.Diff("https://example.test/prefix/v1/systemone", req.URL); diff != "" {
		t.Errorf("URL (-want +got):\n%s", diff)
	}
	want := http.Header{
		"Authorization":      {"Bearer " + testKey},
		"Accept":             {"application/json"},
		"User-Agent":         {sdkIdentifier},
		"X-Typesafe-Sdk":     {sdkIdentifier},
		"X-Typesafe-Runtime": {runtimeIdentifier},
		"Content-Type":       {"application/json"},
		"X-Team":             {"call"},
		"X-Default":          {"kept"},
		"X-Api-Key":          {"key-secret"},
		"Cookie":             {"cookie-secret"},
	}
	if diff := gocmp.Diff(want, req.Header); diff != "" {
		t.Errorf("headers (-want +got):\n%s", diff)
	}
	assertDeadline(t, cp, 0, before, 2*time.Second)

	var text strings.Builder
	for _, r := range logs.Records() {
		text.WriteString(r.String())
		text.WriteByte('\n')
	}
	for _, secret := range []string{testKey, "injected-secret", "key-secret", "cookie-secret", "response-secret"} {
		if strings.Contains(text.String(), secret) {
			t.Errorf("the records hold %q:\n%s", secret, text.String())
		}
	}
	for _, visible := range []string{"req_log", "hello"} {
		if !strings.Contains(text.String(), visible) {
			t.Errorf("the records lack %q:\n%s", visible, text.String())
		}
	}
	dropped := map[string]bool{}
	for _, r := range logs.At(slog.LevelDebug) {
		if r.Message == "call: header dropped" {
			if v, ok := r.Attr("header"); ok {
				dropped[v.String()] = true
			}
		}
	}
	wantDropped := map[string]bool{"Authorization": true, "Accept": true, "User-Agent": true, "X-Typesafe-Sdk": true, "X-Typesafe-Runtime": true, "X-Typesafe-Retry-Count": true, "Content-Type": true}
	if diff := gocmp.Diff(wantDropped, dropped); diff != "" {
		t.Errorf("call headers logged as dropped (-want +got):\n%s", diff)
	}
}

// TestCallOptionsRefused checks that a call option the call cannot use fails
// the call with a *ConfigError before anything is encoded or sent (F9's
// per-call half among them: a zero or negative Timeout), and that no error
// repeats a header value or a name that holds the key.
func TestCallOptionsRefused(t *testing.T) {
	const longKey = "ts_live_0123456789abcdef"
	tests := map[string]struct {
		models bool
		opts   []CallOption
		want   string
	}{
		"error: a zero timeout (F9)":         {opts: []CallOption{Timeout(0)}, want: "must be positive"},
		"error: a negative timeout (F9)":     {opts: []CallOption{Timeout(-time.Second)}, want: "must be positive"},
		"error: a zero timeout on List (F9)": {models: true, opts: []CallOption{Timeout(0)}, want: "must be positive"},
		"error: an empty model":              {opts: []CallOption{Model("")}, want: "The model passed to Model is empty"},
		"error: a blank model":               {opts: []CallOption{Model(" \t")}, want: "The model passed to Model is empty"},
		"error: a model that is not UTF-8":   {opts: []CallOption{Model("\xff")}, want: "is not valid UTF-8"},
		"error: a header name with a space":  {opts: []CallOption{Header("X Team", "v")}, want: "is not a valid HTTP field name"},
		"error: a header value with a CR LF": {opts: []CallOption{Header("X-Team", "a\r\nsecret-value")}, want: "is not a valid HTTP field value"},
		"error: a header name holding a key": {opts: []CallOption{Header("X-"+longKey, "v")}, want: "contains the API key"},
		"error: Model on a list-models call": {models: true, opts: []CallOption{Model("m")}, want: "Model applies to SystemOne calls only"},
		"error: ExtraBody on a list call":    {models: true, opts: []CallOption{ExtraBody("k", 1)}, want: "ExtraBody applies to SystemOne calls only"},
		"success: a nil call option is fine": {opts: []CallOption{nil}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := testsupport.Fixture(t, "result.json")
			if tt.models {
				body = []byte(`{"models":[]}`)
			}
			rec := replying(http.StatusOK, body)
			clearEnv(t)
			c := newEnvClient(t, rec, WithAPIKey(longKey))
			var err error
			if tt.models {
				_, err = c.Models().List(t.Context(), tt.opts...)
			} else {
				_, err = c.SystemOne(t.Context(), "x", noulQuestion(t), tt.opts...)
			}
			if tt.want == "" {
				if err != nil {
					t.Fatalf("call: %v", err)
				}
				return
			}
			if _, ok := errors.AsType[*ConfigError](err); !ok || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("call error = %v (%T), want a *ConfigError with %q", err, err, tt.want)
			}
			assertNotPrinted(t, err, longKey)
			assertNotPrinted(t, err, "secret-value")
			if rec.Count() != 0 {
				t.Errorf("the transport saw %d requests, want 0", rec.Count())
			}
		})
	}
}

// TestPerCallTimeoutOverride ports test_system_one_timeout_override (C14)
// to the deviation "one deadline per attempt": a call's Timeout is the
// deadline of its attempt and does not stay with the client; without one,
// the client's WithTimeout applies, and WithNoTimeout leaves the attempt
// with the caller's context alone.
func TestPerCallTimeoutOverride(t *testing.T) {
	tests := map[string]struct {
		client []ClientOption
		call   []CallOption
		want   time.Duration // 0: no deadline
	}{
		"success: the client's timeout":           {client: []ClientOption{WithTimeout(7 * time.Second)}, want: 7 * time.Second},
		"success: the call's timeout":             {client: []ClientOption{WithTimeout(7 * time.Second)}, call: []CallOption{Timeout(2 * time.Second)}, want: 2 * time.Second},
		"success: no timeout":                     {client: []ClientOption{WithNoTimeout()}},
		"success: a call timeout over no timeout": {client: []ClientOption{WithNoTimeout()}, call: []CallOption{Timeout(3 * time.Second)}, want: 3 * time.Second},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cp := &capture{rt: replying(http.StatusOK, testsupport.Fixture(t, "result.json"))}
			c := newTestClient(t, cp, tt.client...)
			before := time.Now()
			if _, err := c.SystemOne(t.Context(), "hello", noulQuestion(t), tt.call...); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			if tt.want == 0 {
				if cp.hasDL[0] {
					t.Errorf("the attempt has deadline %v, want none", cp.deadlines[0])
				}
			} else {
				assertDeadline(t, cp, 0, before, tt.want)
			}
			// The call's timeout does not stay with the client.
			before = time.Now()
			if _, err := c.SystemOne(t.Context(), "hello", noulQuestion(t)); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			if d, ok := clientTimeout(tt.client); ok {
				assertDeadline(t, cp, 1, before, d)
			} else if cp.hasDL[1] {
				t.Errorf("the second attempt has deadline %v, want none", cp.deadlines[1])
			}
		})
	}
}

// clientTimeout returns the per-attempt timeout opts configure, and false
// for none.
func clientTimeout(opts []ClientOption) (time.Duration, bool) {
	o := collectOptions(opts)
	d, err := o.resolveTimeout()
	return d, err == nil && d > 0
}

// TestRequestBodyIdenticalAcrossReaders checks PM4 through the client with
// one attempt: every GetBody reader of a request reads the bytes its Body
// sent, the length is ContentLength, and GetBody is set so a replay can
// read them again. W3.2 extends the check across retries.
func TestRequestBodyIdenticalAcrossReaders(t *testing.T) {
	rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"))
	var sums []testsupport.BodySum
	var lengths []int64
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.GetBody == nil {
			t.Error("the request has no GetBody")
			return rec.RoundTrip(req)
		}
		for range 2 {
			sum, n, err := testsupport.SumGetBody(req.GetBody)
			if err != nil {
				t.Fatal(err)
			}
			sums = append(sums, sum)
			lengths = append(lengths, n)
		}
		lengths = append(lengths, req.ContentLength)
		return rec.RoundTrip(req)
	})
	c := newTestClient(t, rt)
	if _, err := c.SystemOne(t.Context(), map[string]any{"k": "v"}, noulQuestion(t)); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	sent := onlyRequest(t, rec).Body
	sentSum, sentLen, err := testsupport.SumGetBody(func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(string(sent))), nil })
	if err != nil {
		t.Fatal(err)
	}
	if diff := gocmp.Diff([]testsupport.BodySum{sentSum, sentSum}, sums); diff != "" {
		t.Errorf("GetBody sums against the sent body's (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff([]int64{sentLen, sentLen, sentLen}, lengths); diff != "" {
		t.Errorf("GetBody lengths and ContentLength against the sent body's (-want +got):\n%s", diff)
	}
}

// TestClientNeverPrintsKey pins ruling R66: no fmt verb applied to a Client
// or a *Client, over the SDK's own transport or a caller's, prints the API
// key or the Authorization header's "Bearer " value.
func TestClientNeverPrintsKey(t *testing.T) {
	const longKey = "ts_live_0123456789abcdef"
	clearEnv(t)
	own, err := NewClient(WithAPIKey(longKey))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = own.Close() })
	caller := newEnvClient(t, replying(http.StatusOK, nil), WithAPIKey(longKey))
	for name, c := range map[string]*Client{"own transport": own, "caller's transport": caller} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d", "%x", "%q"} {
			// fmt prints the Client a reflect.Value holds, as it prints a
			// Client passed by value, which vet's copylocks check refuses.
			for form, v := range map[string]any{"*Client": c, "Client": reflect.ValueOf(c).Elem()} {
				if out := fmt.Sprintf(verb, v); strings.Contains(out, longKey) || strings.Contains(out, "Bearer ") {
					t.Errorf("%s: %s of %s prints the key: %s", name, verb, form, out)
				}
			}
		}
	}
}

// TestBaseURLDefaultPortDropped checks ruling R70 Q2: an explicit default
// port in the base URL (:443 on https, :80 on http) is dropped when the
// client is built, as httpx drops it, so the request URL, the Host it sends
// and the endpoint an error names carry none; any other port is kept.
func TestBaseURLDefaultPortDropped(t *testing.T) {
	tests := map[string]struct {
		base, wantURL, wantHost string
	}{
		"success: https with :443":            {base: "https://example.test:443", wantURL: "https://example.test/v1/systemone", wantHost: "example.test"},
		"success: http with :80 and a prefix": {base: "http://example.test:80/p/", wantURL: "http://example.test/p/v1/systemone", wantHost: "example.test"},
		"success: an upper-case scheme":       {base: "HTTPS://example.test:443", wantURL: "https://example.test/v1/systemone", wantHost: "example.test"},
		"success: an IPv6 literal with :443":  {base: "https://[::1]:443", wantURL: "https://[::1]/v1/systemone", wantHost: "[::1]"},
		"success: https with :8443 kept":      {base: "https://example.test:8443", wantURL: "https://example.test:8443/v1/systemone", wantHost: "example.test:8443"},
		"success: http with :443 kept":        {base: "http://example.test:443", wantURL: "http://example.test:443/v1/systemone", wantHost: "example.test:443"},
		"success: https with :80 kept":        {base: "https://example.test:80", wantURL: "https://example.test:80/v1/systemone", wantHost: "example.test:80"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(http.StatusBadRequest, []byte(`{"message":"no"}`))
			c := newTestClient(t, rec, WithBaseURL(tt.base))
			_, err := c.SystemOne(t.Context(), "x", noulQuestion(t))
			ae, ok := errors.AsType[*APIError](err)
			if !ok {
				t.Fatalf("SystemOne error = %v (%T), want an *APIError", err, err)
			}
			req := onlyRequest(t, rec)
			got := []string{req.URL, req.Host, ae.Endpoint}
			want := []string{tt.wantURL, tt.wantHost, http.MethodPost + " " + tt.wantURL}
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("URL, Host, error endpoint (-want +got):\n%s", diff)
			}
		})
	}
}

// TestWithPretouch checks WithPretouch: the types are prepared when the
// client is built and a call with such a state sends the same bytes as
// without; a nil type or one the encoder refuses fails NewClient with a
// *ConfigError that names the type by position and name.
func TestWithPretouch(t *testing.T) {
	type ticket struct {
		Subject string   `json:"subject"`
		Tags    []string `json:"tags"`
	}
	tests := map[string]struct {
		opts    []ClientOption
		wantErr string
	}{
		"success: a struct and a pointer to it":      {opts: []ClientOption{WithPretouch(reflect.TypeFor[ticket](), reflect.TypeFor[*ticket]())}},
		"success: types from two options":            {opts: []ClientOption{WithPretouch(reflect.TypeFor[ticket]()), WithPretouch(reflect.TypeFor[map[string]any]())}},
		"success: no type at all":                    {opts: []ClientOption{WithPretouch()}},
		"error: a nil type":                          {opts: []ClientOption{WithPretouch(reflect.TypeFor[ticket](), nil)}, wantErr: "The type 2 passed to WithPretouch, nil, cannot be prepared for the JSON encoder: pretouch: nil type."},
		"error: a map the encoder cannot key":        {opts: []ClientOption{WithPretouch(reflect.TypeFor[map[complex64]int]())}, wantErr: "The type 1 passed to WithPretouch, map[complex64]int, cannot be prepared for the JSON encoder"},
		"error: the other options are checked first": {opts: []ClientOption{WithPretouch(nil), WithTimeout(-1)}, wantErr: "The timeout passed to WithTimeout must be positive"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"))
			c, err := NewClient(append([]ClientOption{WithAPIKey(testKey), WithRoundTripper(rec)}, tt.opts...)...)
			if tt.wantErr != "" {
				if _, ok := errors.AsType[*ConfigError](err); !ok || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NewClient error = %v, want a *ConfigError with %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			t.Cleanup(func() { _ = c.Close() })
			if _, err := c.SystemOne(t.Context(), ticket{Subject: "s", Tags: []string{"a"}}, noulQuestion(t)); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			want := `{"state":{"subject":"s","tags":["a"]},"model":"jev-latest",` + noulBody + `}`
			if diff := gocmp.Diff(want, string(onlyRequest(t, rec).Body)); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
		})
	}
}
