// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// jsonRequest returns one recorded request with a JSON body.
func jsonRequest(method, uri, body string) Request {
	return Request{Method: method, URI: uri, Body: []byte(body), BodyIsJSON: true}
}

// jsonResponse returns one recorded 200 response with a JSON body.
func jsonResponse(body string) Response {
	return Response{
		StatusCode:    200,
		StatusMessage: "OK",
		Headers:       map[string][]string{"content-type": {"application/json"}},
		Body:          []byte(body),
		BodyIsJSON:    true,
	}
}

// served is what a matched round trip gave back, its body read and
// closed by send.
type served struct {
	status        string
	statusCode    int
	header        http.Header
	contentLength int64
	requestSet    bool
	body          []byte
}

// send round-trips one request with the given body through tr and, on a
// match, returns the response read whole. It asserts the contract that a
// served response carries a non-nil body.
func send(t *testing.T, tr *Transport, method, target, body string) (*served, []byte, error) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, target, reader)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if resp.Body == nil {
		t.Fatal("a served response has a nil Body")
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	s := &served{
		status:        resp.Status,
		statusCode:    resp.StatusCode,
		header:        resp.Header,
		contentLength: resp.ContentLength,
		requestSet:    resp.Request != nil,
		body:          payload,
	}
	return s, payload, nil
}

func TestReplayMatchKeys(t *testing.T) {
	t.Parallel()
	const recordedBody = `{"a":1,"b":[1,2],"s":"x"}`
	tests := map[string]struct {
		method  string
		target  string
		body    string
		match   bool
		wantErr string
	}{
		"success: the recorded request": {
			method: "POST", target: "https://api.example.test/v1/answers", body: recordedBody,
			match: true,
		},
		"success: body differing only in member order": {
			method: "POST", target: "https://api.example.test/v1/answers", body: `{"s":"x","b":[1,2],"a":1}`,
			match: true,
		},
		"success: an integer one equals a recorded float one": {
			method: "POST", target: "https://api.example.test/v1/answers", body: `{"a":1.0,"b":[1,2],"s":"x"}`,
			match: true,
		},
		"error: method differs": {
			method: "GET", target: "https://api.example.test/v1/answers", body: recordedBody,
			wantErr: `method ("GET" is not the recorded "POST")`,
		},
		"error: scheme differs": {
			method: "POST", target: "http://api.example.test/v1/answers", body: recordedBody,
			wantErr: `scheme ("http" is not the recorded "https")`,
		},
		"error: host differs": {
			method: "POST", target: "https://other.example.test/v1/answers", body: recordedBody,
			wantErr: `host ("other.example.test" is not the recorded "api.example.test")`,
		},
		"error: port differs": {
			method: "POST", target: "https://api.example.test:8443/v1/answers", body: recordedBody,
			wantErr: `port ("8443" is not the recorded "443")`,
		},
		"error: path differs": {
			method: "POST", target: "https://api.example.test/v1/other", body: recordedBody,
			wantErr: `path ("/v1/other" is not the recorded "/v1/answers")`,
		},
		"error: an extra query parameter": {
			method: "POST", target: "https://api.example.test/v1/answers?a=1", body: recordedBody,
			wantErr: `query (the request parameter "a" is not recorded)`,
		},
		"error: a body value differs": {
			method: "POST", target: "https://api.example.test/v1/answers", body: `{"a":2,"b":[1,2],"s":"x"}`,
			wantErr: "body at $.a (the numbers differ)",
		},
		"error: a float differing in the last bit": {
			method: "POST", target: "https://api.example.test/v1/answers", body: `{"a":1.0000000000000002,"b":[1,2],"s":"x"}`,
			wantErr: "body at $.a (the numbers differ)",
		},
		"error: a member null where a number is recorded": {
			method: "POST", target: "https://api.example.test/v1/answers", body: `{"a":null,"b":[1,2],"s":"x"}`,
			wantErr: "body at $.a (kind null is not the recorded number)",
		},
		"error: a member array where a number is recorded": {
			method: "POST", target: "https://api.example.test/v1/answers", body: `{"a":[],"b":[1,2],"s":"x"}`,
			wantErr: "body at $.a (kind array is not the recorded number)",
		},
		"error: a member object where an array is recorded": {
			method: "POST", target: "https://api.example.test/v1/answers", body: `{"a":1,"b":{},"s":"x"}`,
			wantErr: "body at $.b (kind object is not the recorded array)",
		},
		"error: an array element differs": {
			method: "POST", target: "https://api.example.test/v1/answers", body: `{"a":1,"b":[1,3],"s":"x"}`,
			wantErr: "body at $.b[1] (the numbers differ)",
		},
		"error: an array length differs": {
			method: "POST", target: "https://api.example.test/v1/answers", body: `{"a":1,"b":[1],"s":"x"}`,
			wantErr: "body at $.b (an array of 1 elements is not the recorded 2)",
		},
		"error: a member kind differs": {
			method: "POST", target: "https://api.example.test/v1/answers", body: `{"a":1,"b":[1,2],"s":5}`,
			wantErr: "body at $.s (kind number is not the recorded string)",
		},
		"error: a member only in the sent body": {
			method: "POST", target: "https://api.example.test/v1/answers", body: `{"a":1,"b":[1,2],"s":"x","extra":0}`,
			wantErr: "body at $.extra (a member the recorded body does not have)",
		},
		"error: a recorded member missing from the sent body": {
			method: "POST", target: "https://api.example.test/v1/answers", body: `{"a":1,"b":[1,2]}`,
			wantErr: "body at $.s (a recorded member the body does not have)",
		},
		"error: the sent body is not JSON": {
			method: "POST", target: "https://api.example.test/v1/answers", body: "not json",
			wantErr: "body (the request body is not a JSON text, the recorded body is one)",
		},
		"error: no body sent against a recorded JSON body": {
			method: "POST", target: "https://api.example.test/v1/answers", body: "",
			wantErr: "body (the request body is not a JSON text, the recorded body is one)",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := &Cassette{Interactions: []Interaction{{
				Request:  jsonRequest("POST", "https://api.example.test/v1/answers", recordedBody),
				Response: jsonResponse(`{"ok":true}`),
			}}}
			tr := c.Transport()
			_, _, err := send(t, tr, tt.method, tt.target, tt.body)
			if tt.match {
				if err != nil {
					t.Fatalf("RoundTrip failed: %v", err)
				}
				if got := tr.Unconsumed(); got != 0 {
					t.Errorf("Unconsumed = %d after the match, want 0", got)
				}
				return
			}
			if err == nil {
				t.Fatalf("RoundTrip matched, want a miss containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("RoundTrip error = %q, want it to contain %q", err, tt.wantErr)
			}
			if got := tr.Unconsumed(); got != 1 {
				t.Errorf("Unconsumed = %d after the miss, want 1", got)
			}
		})
	}
}

func TestReplayServesEachInteractionOnce(t *testing.T) {
	t.Parallel()
	const uri = "https://api.example.test/v1/answers"
	c := &Cassette{Interactions: []Interaction{
		{Request: jsonRequest("POST", uri, `{"n":1}`), Response: jsonResponse(`{"answer":1}`)},
		{Request: jsonRequest("POST", uri, `{"n":2}`), Response: jsonResponse(`{"answer":2}`)},
	}}
	tr := c.Transport()
	_, second, err := send(t, tr, "POST", uri, `{"n":2}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(second), `{"answer":2}`; got != want {
		t.Errorf("first served body = %q, want %q (the matching interaction, not the first)", got, want)
	}
	if _, _, err := send(t, tr, "POST", uri, `{"n":2}`); err == nil {
		t.Fatal("an identical second request matched a consumed interaction, want a miss")
	} else if want := "body at $.n (the numbers differ)"; !strings.Contains(err.Error(), want) {
		t.Errorf("second request's error = %q, want the difference against the one unconsumed candidate %q", err, want)
	}
	if got, want := tr.Unconsumed(), 1; got != want {
		t.Errorf("Unconsumed = %d, want %d", got, want)
	}
	if _, _, err := send(t, tr, "POST", uri, `{"n":1}`); err != nil {
		t.Fatal(err)
	}
	if got, want := tr.Unconsumed(), 0; got != want {
		t.Errorf("Unconsumed = %d after both served, want %d", got, want)
	}
	if _, _, err := send(t, tr, "POST", uri, `{"n":1}`); err == nil {
		t.Fatal("a request after every interaction was served matched, want a miss")
	} else if want := "every recorded interaction was already served"; !strings.Contains(err.Error(), want) {
		t.Errorf("exhausted transport's error = %q, want it to contain %q", err, want)
	}
	wantSent := []SentRequest{
		{URL: uri, Body: []byte(`{"n":2}`)},
		{URL: uri, Body: []byte(`{"n":1}`)},
	}
	if diff := gocmp.Diff(wantSent, tr.Requests()); diff != "" {
		t.Errorf("Requests (-want +got):\n%s", diff)
	}
}

func TestReplayMissErrorRedaction(t *testing.T) {
	t.Parallel()
	c := &Cassette{Interactions: []Interaction{{
		Request: jsonRequest("POST", "https://api.example.test/v1/answers?key=shared-query-value",
			`{"questions":{"q1":{"text":"recorded-value"}},"other":"never-quoted"}`),
		Response: jsonResponse(`{"ok":true}`),
	}}}
	tr := c.Transport()
	_, _, err := send(t, tr, "POST", "https://user:hunter2@api.example.test/v1/answers?key=shared-query-value",
		`{"questions":{"q1":{"text":"sent-value"}},"other":"never-quoted"}`)
	if err == nil {
		t.Fatal("RoundTrip matched, want a miss")
	}
	text := err.Error()
	if want := "body at $.questions.q1.text (the strings differ)"; !strings.Contains(text, want) {
		t.Errorf("error = %q, want the first differing member %q", text, want)
	}
	for _, leaked := range []string{"hunter2", "user:", "shared-query-value", "never-quoted", "sent-value", "recorded-value"} {
		if strings.Contains(text, leaked) {
			t.Errorf("error %q quotes %q: a miss must hold no userinfo, no query value and no body value at all", text, leaked)
		}
	}
	if want := "key="; !strings.Contains(text, want) {
		t.Errorf("error = %q, want the redacted URL to keep the query parameter name %q", text, want)
	}
}

func TestReplayQueryValueNeverInError(t *testing.T) {
	t.Parallel()
	c := &Cassette{Interactions: []Interaction{{
		Request:  jsonRequest("POST", "https://api.example.test/v1/answers?key=recorded-query-value", `{"a":1}`),
		Response: jsonResponse(`{"ok":true}`),
	}}}
	tr := c.Transport()
	_, _, err := send(t, tr, "POST", "https://api.example.test/v1/answers?key=sent-query-value", `{"a":1}`)
	if err == nil {
		t.Fatal("RoundTrip matched, want a query miss")
	}
	text := err.Error()
	if want := `query (the parameter "key" differs)`; !strings.Contains(text, want) {
		t.Errorf("error = %q, want it to contain %q", text, want)
	}
	for _, leaked := range []string{"sent-query-value", "recorded-query-value"} {
		if strings.Contains(text, leaked) {
			t.Errorf("error %q quotes the query value %q", text, leaked)
		}
	}
}

func TestReplayMissingQueryParameter(t *testing.T) {
	t.Parallel()
	c := &Cassette{Interactions: []Interaction{{
		Request:  jsonRequest("POST", "https://api.example.test/v1/answers?key=v", `{"a":1}`),
		Response: jsonResponse(`{"ok":true}`),
	}}}
	tr := c.Transport()
	_, _, err := send(t, tr, "POST", "https://api.example.test/v1/answers", `{"a":1}`)
	if err == nil {
		t.Fatal("RoundTrip matched, want a query miss")
	}
	if want := `query (the recorded parameter "key" is not in the request)`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

func TestReplayNonJSONBodyMatchesByBytes(t *testing.T) {
	t.Parallel()
	rec := Request{Method: "POST", URI: "https://api.example.test/v1/raw", Body: []byte("raw bytes"), BodyIsJSON: false}
	c := &Cassette{Interactions: []Interaction{{Request: rec, Response: jsonResponse(`{"ok":true}`)}}}
	_, _, err := send(t, c.Transport(), "POST", "https://api.example.test/v1/raw", "raw bytes")
	if err != nil {
		t.Fatalf("byte-equal body did not match: %v", err)
	}
	_, _, err = send(t, c.Transport(), "POST", "https://api.example.test/v1/raw", "other bytes")
	if err == nil {
		t.Fatal("differing non-JSON body matched, want a miss")
	}
	if want := "body (the bytes differ from the recorded non-JSON body)"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

func TestReplayRebuildsTheRecordedResponse(t *testing.T) {
	t.Parallel()
	c := &Cassette{Interactions: []Interaction{{
		Request: jsonRequest("POST", "https://api.example.test/v1/answers", `{"a":1}`),
		Response: Response{
			StatusCode:    418,
			StatusMessage: "I'm a teapot",
			Headers:       map[string][]string{"content-type": {"application/json"}},
			Body:          []byte(`{"f":1.0,"t":"ß"}`),
			BodyIsJSON:    true,
		},
	}}}
	tr := c.Transport()
	resp, body, err := send(t, tr, "POST", "https://api.example.test/v1/answers", `{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resp.statusCode, 418; got != want {
		t.Errorf("StatusCode = %d, want %d", got, want)
	}
	if got, want := resp.status, "418 I'm a teapot"; got != want {
		t.Errorf("Status = %q, want %q", got, want)
	}
	if got, want := resp.header.Get("Content-Type"), "application/json"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := string(body), `{"f":1.0,"t":"ß"}`; got != want {
		t.Errorf("served body = %q, want the re-encoded recorded body %q (float 1.0 kept, non-ASCII raw)", got, want)
	}
	if got, want := resp.contentLength, int64(len(body)); got != want {
		t.Errorf("ContentLength = %d, want %d", got, want)
	}
	if !resp.requestSet {
		t.Error("Request = nil, want the sent request")
	}
	wantSent := []SentRequest{{URL: "https://api.example.test/v1/answers", Body: []byte(`{"a":1}`)}}
	if diff := gocmp.Diff(wantSent, tr.Requests()); diff != "" {
		t.Errorf("Requests (-want +got):\n%s", diff)
	}
}

func TestReplayTopLevelOrderIgnoredByEqualOnly(t *testing.T) {
	t.Parallel()
	// Two documents differing only in top-level member order are the same
	// JSON value, so the matcher serves the interaction; a matcher that
	// compared bytes would miss. The inverse protection, that a comparison
	// which must respect order does so, belongs to the expected-file
	// comparison and is asserted where that comparison lives.
	c := &Cassette{Interactions: []Interaction{{
		Request:  jsonRequest("POST", "https://api.example.test/v1/answers", `{"a":1,"b":2}`),
		Response: jsonResponse(`{"ok":true}`),
	}}}
	_, _, err := send(t, c.Transport(), "POST", "https://api.example.test/v1/answers", `{"b":2,"a":1}`)
	if err != nil {
		t.Fatalf("member order alone broke the match: %v", err)
	}
}

func TestReplayNearestCandidateChosenByKey(t *testing.T) {
	t.Parallel()
	// Of two unconsumed candidates, the miss reports the one whose first
	// difference comes latest in the key order: the body difference of the
	// same-endpoint candidate, not the host difference of the other.
	c := &Cassette{Interactions: []Interaction{
		{Request: jsonRequest("POST", "https://other.example.test/v1/answers", `{"n":1}`), Response: jsonResponse(`{}`)},
		{Request: jsonRequest("POST", "https://api.example.test/v1/answers", `{"n":1}`), Response: jsonResponse(`{}`)},
	}}
	_, _, err := send(t, c.Transport(), "POST", "https://api.example.test/v1/answers", `{"n":9}`)
	if err == nil {
		t.Fatal("RoundTrip matched, want a miss")
	}
	if want := "body at $.n (the numbers differ)"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want the body difference of the nearest candidate %q", err, want)
	}
}

func TestReplayBodyReadFailure(t *testing.T) {
	t.Parallel()
	c := &Cassette{Interactions: []Interaction{{
		Request:  jsonRequest("POST", "https://api.example.test/v1/answers", `{"a":1}`),
		Response: jsonResponse(`{"ok":true}`),
	}}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example.test/v1/answers", failingReader{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Transport().RoundTrip(req)
	if err == nil {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("RoundTrip succeeded over a failing request body, want an error")
	}
	if want := "reading the request body"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// failingReader fails every read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestReplayDeepPathQuoting(t *testing.T) {
	t.Parallel()
	// A member name that is not a plain identifier is quoted in the path.
	c := &Cassette{Interactions: []Interaction{{
		Request:  jsonRequest("POST", "https://api.example.test/v1/answers", `{"outer":{"a b":true}}`),
		Response: jsonResponse(`{"ok":true}`),
	}}}
	_, _, err := send(t, c.Transport(), "POST", "https://api.example.test/v1/answers", `{"outer":{"a b":false}}`)
	if err == nil {
		t.Fatal("RoundTrip matched, want a miss")
	}
	if want := `body at $.outer["a b"] (kind false is not the recorded true)`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// TestReplayExactNumberMatching checks numeric misses and their exact paths
// without exposing the differing values in the diagnostic.
func TestReplayExactNumberMatching(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		sent, recorded string
	}{
		"error: integer rounds to recorded float": {
			sent: "9007199254740993", recorded: "9007199254740992.0",
		},
		"error: sent float rounds away recorded integer": {
			sent: "9007199254740992.0", recorded: "9007199254740993",
		},
		"error: negative integer rounds to recorded float": {
			sent: "-9007199254740993", recorded: "-9007199254740992.0",
		},
		"error: exponent float and large integer": {
			sent: "9007199254740993", recorded: "9.007199254740992e15",
		},
		"error: distinct large integers": {
			sent: "1152921504606846977", recorded: "1152921504606846976",
		},
		"error: mixed numbers beyond machine integer range": {
			sent: "1267650600228229401496703205377", recorded: "1267650600228229401496703205376.0",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			const uri = "https://api.example.test/v1/answers"
			// Equal mixed numbers are visited before the actual difference.
			recorded := `{"outer":[{"same":9007199254740992.0},{"a b":` + tt.recorded + `}],"note":"unquoted-body-marker"}`
			sent := `{"outer":[{"same":9007199254740992},{"a b":` + tt.sent + `}],"note":"unquoted-body-marker"}`
			c := &Cassette{Interactions: []Interaction{{
				Request:  jsonRequest(http.MethodPost, uri, recorded),
				Response: jsonResponse(`{"ok":true}`),
			}}}
			tr := c.Transport()
			_, _, err := send(t, tr, http.MethodPost, uri, sent)
			if err == nil {
				t.Fatal("different numeric values matched, want a miss")
			}
			const want = `cassette: no interaction matches POST https://api.example.test/v1/answers: the nearest candidate differs in body at $.outer[1]["a b"] (the numbers differ)`
			if diff := gocmp.Diff(want, err.Error()); diff != "" {
				t.Errorf("numeric mismatch diagnostic (-want +got):\n%s", diff)
			}
			for _, value := range []string{tt.sent, tt.recorded, "unquoted-body-marker"} {
				if strings.Contains(err.Error(), value) {
					t.Error("numeric mismatch diagnostic quotes a body value")
				}
			}
			if got := tr.Unconsumed(); got != 1 {
				t.Errorf("Unconsumed after a numeric miss = %d, want 1", got)
			}
			if got := tr.Requests(); len(got) != 0 {
				t.Errorf("matched requests after a numeric miss = %d, want 0", len(got))
			}
		})
	}
}

func TestReplayWalksEqualMembersToTheDifference(t *testing.T) {
	t.Parallel()
	// Equal null, boolean, minus-zero and string members are passed
	// over; the difference is the member after them.
	c := &Cassette{Interactions: []Interaction{{
		Request:  jsonRequest("POST", "https://api.example.test/v1/answers", `{"n":null,"t":true,"z":0,"q":"same","x":2}`),
		Response: jsonResponse(`{"ok":true}`),
	}}}
	_, _, err := send(t, c.Transport(), "POST", "https://api.example.test/v1/answers", `{"n":null,"t":true,"z":-0,"q":"same","x":1}`)
	if err == nil {
		t.Fatal("RoundTrip matched, want a miss")
	}
	if want := "body at $.x (the numbers differ)"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

func TestReplayDefaultPorts(t *testing.T) {
	t.Parallel()
	// An explicit port equal to the scheme's default matches a recorded
	// URI that leaves it out, and a scheme without a default port
	// compares the empty port equal.
	httpCassette := &Cassette{Interactions: []Interaction{{
		Request:  jsonRequest("POST", "http://api.example.test/v1/answers", `{"a":1}`),
		Response: jsonResponse(`{"ok":true}`),
	}}}
	_, _, err := send(t, httpCassette.Transport(), "POST", "http://api.example.test:80/v1/answers", `{"a":1}`)
	if err != nil {
		t.Errorf("an explicit :80 missed the recorded default http port: %v", err)
	}
	rec := Request{Method: "POST", URI: "ftp://files.example.test/drop", Body: []byte("payload"), BodyIsJSON: false}
	ftpCassette := &Cassette{Interactions: []Interaction{{Request: rec, Response: jsonResponse(`{"ok":true}`)}}}
	_, _, err = send(t, ftpCassette.Transport(), "POST", "ftp://files.example.test/drop", "payload")
	if err != nil {
		t.Errorf("a scheme without a default port missed: %v", err)
	}
}

func TestReplayUnparsableRecordedURI(t *testing.T) {
	t.Parallel()
	// A recorded URI that url.Parse refuses is reported without any part
	// of the URI: url.Parse's own error quotes it whole, userinfo and
	// query values included, so that error never reaches the message.
	// The userinfo is a made-up canary, concatenated so no literal spells
	// a credential-in-URL shape.
	userinfo := "user:" + "hunter2"
	c := &Cassette{Interactions: []Interaction{{
		Request: Request{
			Method:     "POST",
			URI:        "https://" + userinfo + "@example.test/%zz?key=recorded-query-value",
			Body:       []byte(`{}`),
			BodyIsJSON: true,
		},
		Response: jsonResponse(`{"ok":true}`),
	}}}
	_, _, err := send(t, c.Transport(), "POST", "https://example.test/x", `{}`)
	if err == nil {
		t.Fatal("RoundTrip matched, want an error about the recorded URI")
	}
	text := err.Error()
	if want := "a recorded URI that does not parse"; !strings.Contains(text, want) {
		t.Errorf("error = %q, want it to contain %q", text, want)
	}
	for _, leaked := range []string{"hunter2", "user:", "recorded-query-value", "%zz"} {
		if strings.Contains(text, leaked) {
			t.Errorf("error %q quotes %q of the unparsable recorded URI", text, leaked)
		}
	}
}

func TestReplayEscapedPathNotUnescaped(t *testing.T) {
	t.Parallel()
	// Paths compare in escaped form: a recorded /a%2Fb is not a sent
	// /a/b although both decode to the same text, and the same escaped
	// spelling matches.
	c := &Cassette{Interactions: []Interaction{{
		Request:  jsonRequest("POST", "https://api.example.test/a%2Fb", `{"n":1}`),
		Response: jsonResponse(`{"ok":true}`),
	}}}
	tr := c.Transport()
	_, _, err := send(t, tr, "POST", "https://api.example.test/a/b", `{"n":1}`)
	if err == nil {
		t.Fatal("an unescaped /a/b matched the recorded /a%2Fb, want a path miss")
	}
	if want := `path ("/a/b" is not the recorded "/a%2Fb")`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
	_, _, err = send(t, tr, "POST", "https://api.example.test/a%2Fb", `{"n":1}`)
	if err != nil {
		t.Errorf("the recorded escaped path missed itself: %v", err)
	}
}

func TestReplayQueryPairsAsPython(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		sent, recorded string
		match          bool
	}{
		"success: sent blank value omitted":        {sent: "a=", match: true},
		"success: recorded blank value omitted":    {recorded: "a=", match: true},
		"success: absent equal sign omitted":       {sent: "a", match: true},
		"success: blank values mixed with pairs":   {sent: "a=&x=1", recorded: "x=1", match: true},
		"success: semicolon self match":            {sent: "a=one;two", recorded: "a=one;two", match: true},
		"success: invalid escape self match":       {sent: "key=%zz", recorded: "key=%zz", match: true},
		"success: encoded semicolon":               {sent: "a=one;two", recorded: "a=one%3Btwo", match: true},
		"success: escaped invalid percent":         {sent: "key=%zz", recorded: "key=%25zz", match: true},
		"success: plus decodes to space":           {sent: "q=a+b", recorded: "q=a%20b", match: true},
		"success: valid and invalid escapes mix":   {sent: "q=%zz%41", recorded: "q=%25zzA", match: true},
		"success: incomplete escape kept":          {sent: "q=%2", recorded: "q=%252", match: true},
		"success: trailing percent kept":           {sent: "q=%", recorded: "q=%25", match: true},
		"success: names decode too":                {sent: "%61=value", recorded: "a=value", match: true},
		"success: empty name kept":                 {sent: "=value", recorded: "=value", match: true},
		"success: unicode percent decoding":        {sent: "q=%e6%97%a5", recorded: "q=日", match: true},
		"success: invalid UTF-8 byte replaced":     {sent: "q=%FF", recorded: "q=%EF%BF%BD", match: true},
		"success: truncated UTF-8 prefix replaced": {sent: "q=%E2%82", recorded: "q=%EF%BF%BD", match: true},
		"success: invalid UTF-8 suffix retained":   {sent: "q=%E2%82x", recorded: "q=%EF%BF%BDx", match: true},
		"success: distinct invalid bytes replaced": {
			sent: "q=%FF%FF", recorded: "q=%EF%BF%BD%EF%BF%BD", match: true,
		},
		"error: sent semicolon is not an absent pair":     {sent: "a=one;two"},
		"error: recorded semicolon is not an absent pair": {recorded: "a=one;two"},
		"error: sent invalid escape is not absent":        {sent: "key=%zz"},
		"error: recorded invalid escape is not absent":    {recorded: "key=%zz"},
		"error: semicolon suffix matters":                 {sent: "a=one;two", recorded: "a=one"},
		"error: invalid escape text matters":              {sent: "key=%zz", recorded: "key=%zy"},
		"error: empty name is not an absent pair":         {sent: "=value"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			const base = "https://api.example.test/v1/answers"
			c := &Cassette{Interactions: []Interaction{{
				Request:  jsonRequest("POST", base+"?"+tt.recorded, `{"a":1}`),
				Response: jsonResponse(`{"ok":true}`),
			}}}
			tr := c.Transport()
			_, _, err := send(t, tr, "POST", base+"?"+tt.sent, `{"a":1}`)
			if tt.match {
				if err != nil {
					t.Fatalf("equivalent query pairs missed: %v", err)
				}
				if got := tr.Unconsumed(); got != 0 {
					t.Errorf("Unconsumed = %d after match, want 0", got)
				}
				return
			}
			if err == nil {
				t.Fatal("different query pairs matched, want a miss")
			}
			if !strings.Contains(err.Error(), "query (") {
				t.Errorf("error = %q, want a query mismatch", err)
			}
			if got := tr.Unconsumed(); got != 1 {
				t.Errorf("Unconsumed = %d after miss, want 1", got)
			}
		})
	}
}

func TestReplayHostCaseInsensitive(t *testing.T) {
	t.Parallel()
	c := &Cassette{Interactions: []Interaction{{
		Request:  jsonRequest("POST", "https://API.Example.TEST/v1/answers", `{"a":1}`),
		Response: jsonResponse(`{"ok":true}`),
	}}}
	_, _, err := send(t, c.Transport(), "POST", "https://api.example.test/v1/answers", `{"a":1}`)
	if err != nil {
		t.Errorf("a host differing only in case missed: %v", err)
	}
}

func TestReplayRepeatedQueryValueOrderIgnored(t *testing.T) {
	t.Parallel()
	c := &Cassette{Interactions: []Interaction{{
		Request:  jsonRequest("POST", "https://api.example.test/v1/answers?a=1&a=2", `{"n":1}`),
		Response: jsonResponse(`{"ok":true}`),
	}}}
	_, _, err := send(t, c.Transport(), "POST", "https://api.example.test/v1/answers?a=2&a=1", `{"n":1}`)
	if err != nil {
		t.Errorf("repeated query values in another order missed: %v", err)
	}
}

// closeRecorder records whether a request body was closed.
type closeRecorder struct {
	io.Reader
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true
	return nil
}

func TestReplayClosesTheRequestBody(t *testing.T) {
	t.Parallel()
	c := &Cassette{Interactions: []Interaction{{
		Request:  jsonRequest("POST", "https://api.example.test/v1/answers", `{"a":1}`),
		Response: jsonResponse(`{"ok":true}`),
	}}}
	tr := c.Transport()
	recorder := &closeRecorder{Reader: strings.NewReader(`{"a":1}`)}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example.test/v1/answers", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Body = recorder
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Error(err)
	}
	if !recorder.closed {
		t.Error("the request body was not closed after the round trip")
	}
}

func TestReplayUnreadableRecordedBody(t *testing.T) {
	t.Parallel()
	c := &Cassette{Interactions: []Interaction{{
		Request:  Request{Method: "POST", URI: "https://example.test/x", Body: []byte("{bad"), BodyIsJSON: true},
		Response: jsonResponse(`{"ok":true}`),
	}}}
	_, _, err := send(t, c.Transport(), "POST", "https://example.test/x", `{}`)
	if err == nil {
		t.Fatal("RoundTrip matched, want an error about the recorded body")
	}
	if want := "the recorded body does not read back"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// The transport is an http.RoundTripper, so a test hands it to a provider
// through an http.Client without a listener.
var _ http.RoundTripper = (*Transport)(nil)
