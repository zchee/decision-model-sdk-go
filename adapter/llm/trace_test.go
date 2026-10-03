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

package llm_test

import (
	"strconv"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// recorded is everything a Trace returns, for comparison with go-cmp.
type recorded struct {
	API          string
	Request      []byte
	Requested    bool
	Response     []byte
	Responded    bool
	FinishReason *string
}

func readTrace(tr *llm.Trace) recorded {
	req, requested := tr.Request()
	resp, responded := tr.Response()
	if responded != tr.Responded() {
		panic("Response and Responded disagree")
	}
	return recorded{API: tr.API(), Request: req, Requested: requested, Response: resp, Responded: responded, FinishReason: tr.FinishReason()}
}

// TestTraceRecordsExchange checks what a Trace returns after each order of
// calls a provider makes: nothing, a request only (a provider that failed
// before a response), a request and a response, a response with a null
// finish reason, and a second call of either method, which replaces what the
// first kept as upstream's assignment does (providers/base.py:156-167).
func TestTraceRecordsExchange(t *testing.T) {
	type call struct {
		request      bool
		api          string
		body         string
		finishReason *string
	}
	tests := map[string]struct {
		calls []call
		want  recorded
	}{
		"success: nothing recorded": {
			want: recorded{},
		},
		"success: request only": {
			calls: []call{{request: true, api: "responses", body: `{"model":"m"}`}},
			want:  recorded{API: "responses", Request: []byte(`{"model":"m"}`), Requested: true},
		},
		"success: request then response": {
			calls: []call{
				{request: true, api: "chat_completions", body: `{"model":"m"}`},
				{body: `{"choices":[]}`, finishReason: new("stop")},
			},
			want: recorded{
				API: "chat_completions", Request: []byte(`{"model":"m"}`), Requested: true,
				Response: []byte(`{"choices":[]}`), Responded: true, FinishReason: new("stop"),
			},
		},
		"success: response with a null finish reason": {
			calls: []call{
				{request: true, api: "interactions", body: `{}`},
				{body: `{"outputs":[]}`},
			},
			want: recorded{
				API: "interactions", Request: []byte(`{}`), Requested: true,
				Response: []byte(`{"outputs":[]}`), Responded: true,
			},
		},
		"success: response without a request": {
			calls: []call{{body: `{"content":[]}`, finishReason: new("end_turn")}},
			want:  recorded{Response: []byte(`{"content":[]}`), Responded: true, FinishReason: new("end_turn")},
		},
		"success: a second request replaces the first": {
			calls: []call{
				{request: true, api: "responses", body: `{"a":1}`},
				{request: true, api: "chat_completions", body: `{"b":2}`},
			},
			want: recorded{API: "chat_completions", Request: []byte(`{"b":2}`), Requested: true},
		},
		"success: a second response replaces the first, null included": {
			calls: []call{
				{body: `{"a":1}`, finishReason: new("length")},
				{body: `{"b":2}`},
			},
			want: recorded{Response: []byte(`{"b":2}`), Responded: true},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var tr llm.Trace
			for _, c := range tt.calls {
				if c.request {
					tr.RecordRequest(c.api, []byte(c.body))
				} else {
					tr.RecordResponse([]byte(c.body), c.finishReason)
				}
			}
			if diff := cmp.Diff(tt.want, readTrace(&tr)); diff != "" {
				t.Errorf("recorded mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestTraceKeepsCopies checks that a Trace keeps what it was given and
// returns copies: changing the slices and the string passed to the record
// methods, or what the read methods returned, does not change what the
// Trace returns next.
func TestTraceKeepsCopies(t *testing.T) {
	var tr llm.Trace
	req := []byte(`{"model":"m"}`)
	resp := []byte(`{"status":"completed"}`)
	reason := "completed"
	tr.RecordRequest("responses", req)
	tr.RecordResponse(resp, &reason)

	want := recorded{
		API: "responses", Request: []byte(`{"model":"m"}`), Requested: true,
		Response: []byte(`{"status":"completed"}`), Responded: true, FinishReason: new("completed"),
	}

	req[0], resp[0], reason = 'X', 'X', "changed"
	if diff := cmp.Diff(want, readTrace(&tr)); diff != "" {
		t.Fatalf("after changing the inputs (-want +got):\n%s", diff)
	}

	got := readTrace(&tr)
	got.Request[0], got.Response[0], *got.FinishReason = 'Y', 'Y', "changed"
	if diff := cmp.Diff(want, readTrace(&tr)); diff != "" {
		t.Fatalf("after changing what the read methods returned (-want +got):\n%s", diff)
	}
}

// TestNilTraceRecordsNothing checks that a nil *Trace, the value of
// Request.Trace when the Adapter set none, accepts both record methods and
// reads as empty, as upstream's record_request and record_response do
// nothing outside an attempt (providers/base.py:158,165).
func TestNilTraceRecordsNothing(t *testing.T) {
	var tr *llm.Trace
	tr.RecordRequest("responses", []byte(`{}`))
	tr.RecordResponse([]byte(`{}`), new("stop"))
	if diff := cmp.Diff(recorded{}, readTrace(tr)); diff != "" {
		t.Errorf("nil Trace mismatch (-want +got):\n%s", diff)
	}
}

// TestTraceConcurrentUse records and reads one Trace from many goroutines;
// run under -race it checks that a Trace is safe for concurrent use, and
// at the end it holds one of the recorded pairs whole.
func TestTraceConcurrentUse(t *testing.T) {
	const n = 64
	var tr llm.Trace
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			s := strconv.Itoa(i)
			tr.RecordRequest("api"+s, []byte(s))
			tr.RecordResponse([]byte(s), &s)
			_ = readTrace(&tr)
		})
	}
	wg.Wait()

	got := readTrace(&tr)
	if !got.Requested || !got.Responded || got.FinishReason == nil {
		t.Fatalf("after %d writers: %+v, want a request and a response recorded", n, got)
	}
	if want := "api" + string(got.Request); got.API != want {
		t.Errorf("API %q with request %q: a request must be recorded whole, API and body together", got.API, got.Request)
	}
	if string(got.Response) != *got.FinishReason {
		t.Errorf("response %q with finish reason %q: a response must be recorded whole", got.Response, *got.FinishReason)
	}
}
