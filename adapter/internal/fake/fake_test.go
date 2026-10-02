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

package fake_test

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/adapter/internal/fake"
	"github.com/zchee/typesafe-sdk-go/adapter/llm"
)

// answer is what one request to a Provider produced, for comparison.
type answer struct {
	Result *llm.Result
	Err    error
	Panic  any
}

// do sends one request and returns its answer, recovering a scripted panic.
func do(t *testing.T, p *fake.Provider, req *llm.Request) (a answer) {
	t.Helper()
	defer func() {
		if v := recover(); v != nil {
			a = answer{Panic: v}
		}
	}()
	r, err := p.Do(t.Context(), req)
	return answer{Result: r, Err: err}
}

var errUnavailable = errors.New("503 unavailable")

func known(n uint64) llm.Count { return llm.Count{N: n, Known: true} }

// TestProviderScriptedOrder checks that a Provider answers with its
// outcomes in order and repeats the last one once the script is exhausted,
// for each kind of outcome upstream scripts (a payload, a ProviderResult, an
// exception) and for a panic.
func TestProviderScriptedOrder(t *testing.T) {
	result := llm.Result{Text: `{"answers":{}}`, InputTokens: known(3)}
	tests := map[string]struct {
		steps    []fake.Outcome
		requests int
		want     []answer
	}{
		"success: text answers with the default usage": {
			steps:    []fake.Outcome{fake.Text(`{"answers":{"a":0.5}}`)},
			requests: 1,
			want:     []answer{{Result: &llm.Result{Text: `{"answers":{"a":0.5}}`, InputTokens: known(11), OutputTokens: known(7)}}},
		},
		"success: result answers as scripted, unknown counts kept": {
			steps:    []fake.Outcome{fake.Result(result)},
			requests: 1,
			want:     []answer{{Result: &result}},
		},
		"success: error then text, the last repeated": {
			steps:    []fake.Outcome{fake.Error(errUnavailable), fake.Text("ok")},
			requests: 4,
			want: []answer{
				{Err: errUnavailable},
				{Result: &llm.Result{Text: "ok", InputTokens: known(11), OutputTokens: known(7)}},
				{Result: &llm.Result{Text: "ok", InputTokens: known(11), OutputTokens: known(7)}},
				{Result: &llm.Result{Text: "ok", InputTokens: known(11), OutputTokens: known(7)}},
			},
		},
		"success: an error as the last step repeats": {
			steps:    []fake.Outcome{fake.Error(errUnavailable)},
			requests: 3,
			want:     []answer{{Err: errUnavailable}, {Err: errUnavailable}, {Err: errUnavailable}},
		},
		"success: panic then text": {
			steps:    []fake.Outcome{fake.Panic("provider bug"), fake.Text("ok")},
			requests: 2,
			want: []answer{
				{Panic: "provider bug"},
				{Result: &llm.Result{Text: "ok", InputTokens: known(11), OutputTokens: known(7)}},
			},
		},
		"error: no outcome scripted": {
			requests: 2,
			want:     []answer{{Err: fake.ErrNoOutcome}, {Err: fake.ErrNoOutcome}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := fake.New(tt.steps...)
			var got []answer
			for range tt.requests {
				got = append(got, do(t, p, &llm.Request{}))
			}
			if diff := cmp.Diff(tt.want, got, cmp.Comparer(errors.Is)); diff != "" {
				t.Errorf("answers mismatch (-want +got):\n%s", diff)
			}
			if got, want := p.Calls(), tt.requests; got != want {
				t.Errorf("Calls() = %d, want %d", got, want)
			}
		})
	}
}

// TestProviderReturnsFreshResults checks that each answer is a new Result,
// so a caller that changes one does not change the next.
func TestProviderReturnsFreshResults(t *testing.T) {
	p := fake.New(fake.Result(llm.Result{Text: "a"}))
	first := do(t, p, &llm.Request{})
	first.Result.Text = "changed"
	if got := do(t, p, &llm.Request{}); got.Result.Text != "a" {
		t.Errorf("second answer's Text = %q, want %q", got.Result.Text, "a")
	}
}

// TestProviderRecordsRequests checks that a Provider records each request's
// messages, schema and structured flag, as upstream records calls and
// structured_flags, also for a request it answers with an error or a panic;
// that it keeps copies, so a caller that changes its request afterwards
// does not change the record; and that it never records or writes a Trace.
func TestProviderRecordsRequests(t *testing.T) {
	p := fake.New(fake.Error(errUnavailable), fake.Panic("bug"), fake.Text("ok"))
	trace := new(llm.Trace)
	reqs := []*llm.Request{
		{Messages: []llm.Message{{Role: "system", Content: "s"}, {Role: "user", Content: "u1"}}, Schema: []byte(`{"type":"object"}`), Structured: true, Trace: trace},
		{Messages: []llm.Message{{Role: "user", Content: "u2"}}, Schema: []byte(`{}`)},
		{Messages: []llm.Message{{Role: "user", Content: "u3"}, {Role: "assistant", Content: "a"}}, Structured: true},
	}
	for _, r := range reqs {
		do(t, p, r)
	}
	want := []llm.Request{
		{Messages: []llm.Message{{Role: "system", Content: "s"}, {Role: "user", Content: "u1"}}, Schema: []byte(`{"type":"object"}`), Structured: true},
		{Messages: []llm.Message{{Role: "user", Content: "u2"}}, Schema: []byte(`{}`)},
		{Messages: []llm.Message{{Role: "user", Content: "u3"}, {Role: "assistant", Content: "a"}}, Structured: true},
	}

	reqs[0].Messages[0].Content = "changed"
	reqs[0].Schema[0] = 'X'
	if diff := cmp.Diff(want, p.Requests()); diff != "" {
		t.Fatalf("Requests() mismatch after the caller changed its request (-want +got):\n%s", diff)
	}

	got := p.Requests()
	got[0].Messages[0].Content = "changed"
	if diff := cmp.Diff(want, p.Requests()); diff != "" {
		t.Fatalf("Requests() mismatch after changing what it returned (-want +got):\n%s", diff)
	}

	if trace.Responded() || trace.API() != "" {
		t.Error("the Provider wrote the request's Trace; it must leave it alone")
	}
}

// TestProviderModelUsageAndClose checks the Provider's model name, the usage
// a Text outcome reports, and that Close is counted and returns the error
// set for it, each call.
func TestProviderModelUsageAndClose(t *testing.T) {
	errClose := errors.New("close failed")
	tests := map[string]struct {
		p         *fake.Provider
		closes    int
		wantModel string
		wantUsage [2]llm.Count
		wantClose error
	}{
		"success: defaults": {
			p:         fake.New(fake.Text("t")),
			closes:    1,
			wantModel: "fake-model",
			wantUsage: [2]llm.Count{known(11), known(7)},
		},
		"success: model, unknown usage, close error": {
			p:         fake.New(fake.Text("t")).WithModel("gpt-test").WithUsage(llm.Count{}, known(0)).WithCloseError(errClose),
			closes:    2,
			wantModel: "gpt-test",
			wantUsage: [2]llm.Count{{}, known(0)},
			wantClose: errClose,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.p.Model(); got != tt.wantModel {
				t.Errorf("Model() = %q, want %q", got, tt.wantModel)
			}
			a := do(t, tt.p, &llm.Request{})
			if diff := cmp.Diff(tt.wantUsage, [2]llm.Count{a.Result.InputTokens, a.Result.OutputTokens}); diff != "" {
				t.Errorf("usage mismatch (-want +got):\n%s", diff)
			}
			if got := tt.p.Closes(); got != 0 {
				t.Errorf("Closes() before Close = %d, want 0", got)
			}
			for i := range tt.closes {
				if err := tt.p.Close(); !errors.Is(err, tt.wantClose) || (tt.wantClose == nil && err != nil) {
					t.Errorf("Close() call %d = %v, want %v", i+1, err, tt.wantClose)
				}
			}
			if got := tt.p.Closes(); got != tt.closes {
				t.Errorf("Closes() = %d, want %d", got, tt.closes)
			}
		})
	}
}

// TestProviderConcurrentUse sends requests from many goroutines while
// others read the records and close the Provider; run under -race it checks
// that a Provider is safe for concurrent use, and it checks that every
// request was recorded once and that the outcomes were taken in the order
// the requests were received: with the script [error, text], exactly one
// request got the error.
func TestProviderConcurrentUse(t *testing.T) {
	const n = 64
	p := fake.New(fake.Error(errUnavailable), fake.Text("ok"))
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed int
	)
	for i := range n {
		wg.Go(func() {
			_, err := p.Do(t.Context(), &llm.Request{Messages: []llm.Message{{Role: "user", Content: strconv.Itoa(i)}}})
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
			}
			_ = p.Requests()
			_ = p.Close()
		})
	}
	wg.Wait()

	if failed != 1 {
		t.Errorf("%d requests got the scripted error, want exactly 1", failed)
	}
	var contents []string
	for _, r := range p.Requests() {
		contents = append(contents, r.Messages[0].Content)
	}
	slices.Sort(contents)
	want := make([]string, n)
	for i := range n {
		want[i] = strconv.Itoa(i)
	}
	slices.Sort(want)
	if diff := cmp.Diff(want, contents); diff != "" {
		t.Errorf("recorded requests mismatch (-want +got):\n%s", diff)
	}
	if got := p.Closes(); got != n {
		t.Errorf("Closes() = %d, want %d", got, n)
	}
}
