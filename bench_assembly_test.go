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

// B3 (docs/perf/benchmarks.md): request assembly, everything the first
// attempt of a SystemOne call does before the transport is called: the body
// encode, the GetBody a replay would use, the attempt's header (the
// client's template itself, ruling R28), its copy of the endpoint URL, its
// deadline, the body reader and the *http.Request.
//
//   - request: the port plan's section 5 question set (sketchQuestions),
//     prepared once, and the 1 KiB boxed-string state of NF3.
//   - prepare-and-request: the same with Questions.Prepare inside the loop,
//     as a caller that prepares on every call pays it.
//
// These steps are unexported and run inline in SystemOne and
// Client.attempt, so assembleRequest rebuilds them from the same parts;
// TestAssemblyMatchesCall checks, before anything is timed, that its
// request is the one a real call hands its transport: method, URL, Host,
// every header, the body byte for byte, its length and a GetBody.
//
// How this can mislead: the retry policy, the logging checks and the
// transport are not in it; B5's whole call has all of them.

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// sinkAssembled keeps B3's requests alive.
var sinkAssembled *http.Request

// assembled is one attempt's request as assembleRequest builds it, with
// what must be released once it has been sent.
type assembled struct {
	req    *http.Request
	cancel context.CancelFunc
	body   codec.Body
}

// release closes the request's body reader, ends its deadline and returns
// the body's scratch to the pool, as a call does when it returns.
func (a assembled) release() {
	_ = a.req.Body.Close()
	a.cancel()
	a.body.Release()
}

// assembleRequest builds the first attempt's request of c.SystemOne(ctx,
// state, qs) with no call options, step by step as SystemOne and
// Client.attempt build it.
func assembleRequest(ctx context.Context, c *Client, state any, qs *Prepared) (assembled, error) {
	body, err := encodeBody(state, c.cfg.model, qs, nil)
	if err != nil {
		return assembled{}, err
	}
	rq := request{
		method:  http.MethodPost,
		url:     c.cfg.systemOneURL,
		header:  c.cfg.systemOneHeader,
		timeout: c.cfg.timeout,
		body:    body,
		getBody: body.GetBody,
	}
	h := rq.attemptHeader(0)
	u := new(url.URL)
	*u = *rq.url
	actx, cancel := context.WithTimeout(ctx, rq.timeout)
	rc, err := body.Open()
	if err != nil {
		cancel()
		body.Release()
		return assembled{}, err
	}
	r := http.Request{
		Method:        rq.method,
		URL:           u,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        h,
		Host:          u.Host,
		Body:          rc,
		GetBody:       rq.getBody,
		ContentLength: int64(body.Len()),
	}
	return assembled{req: r.WithContext(actx), cancel: cancel, body: body}, nil
}

// BenchmarkAssembly is B3; see the comment at the top of this file.
func BenchmarkAssembly(b *testing.B) {
	c := newBenchClient(b, &testsupport.Recorder{Discard: true})
	state := newCallState()
	b.Run("request", func(b *testing.B) {
		ctx := b.Context()
		qs := mustPrepared(b, sketchQuestions(""))
		b.ReportAllocs()
		for b.Loop() {
			a, err := assembleRequest(ctx, c, state, qs)
			if err != nil {
				b.Fatal(err)
			}
			sinkAssembled = a.req
			a.release()
		}
	})
	b.Run("prepare-and-request", func(b *testing.B) {
		ctx := b.Context()
		questions := sketchQuestions("")
		b.ReportAllocs()
		for b.Loop() {
			qs, err := questions.Prepare()
			if err != nil {
				b.Fatal(err)
			}
			a, err := assembleRequest(ctx, c, state, qs)
			if err != nil {
				b.Fatal(err)
			}
			sinkAssembled = a.req
			a.release()
		}
	})
}

// TestAssemblyMatchesCall checks that B3 times what a call does: the
// request assembleRequest builds, sent through the same recording
// transport as a real SystemOne call's first attempt, is recorded as the
// same request (method, URL, Host, every header, the body byte for byte,
// its declared length, a GetBody), and both carry the client's deadline.
func TestAssemblyMatchesCall(t *testing.T) {
	tests := map[string]struct {
		questions func(tb testing.TB) *Prepared
		state     any
	}{
		"success: the section 5 set and the NF3 state": {
			questions: func(tb testing.TB) *Prepared { return mustPrepared(tb, sketchQuestions("")) },
			state:     newCallState(),
		},
		"success: the q3 set and a struct state": {
			questions: q3Questions,
			state:     &benchTicket{Subject: "billing", Body: benchText(1 << 10), Tags: []string{"refund"}, Priority: 2},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := replying(http.StatusOK, testsupport.Fixture(t, "result.json"))
			var deadlines []time.Duration
			rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				dl, ok := req.Context().Deadline()
				if !ok {
					deadlines = append(deadlines, -1)
				} else {
					deadlines = append(deadlines, time.Until(dl))
				}
				return rec.RoundTrip(req)
			})
			c := newBenchClient(t, rt)
			qs := tt.questions(t)
			if _, err := c.SystemOne(t.Context(), tt.state, qs); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}

			a, err := assembleRequest(t.Context(), c, tt.state, qs)
			if err != nil {
				t.Fatalf("assembleRequest: %v", err)
			}
			resp, err := rt.RoundTrip(a.req)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			_ = resp.Body.Close()
			a.release()

			reqs := rec.Requests()
			if len(reqs) != 2 {
				t.Fatalf("the transport saw %d requests, want 2", len(reqs))
			}
			call, built := reqs[0], reqs[1]
			call.Index, built.Index = 0, 0
			if diff := gocmp.Diff(call, built); diff != "" {
				t.Errorf("request (-call +assembled):\n%s", diff)
			}
			if len(call.Body) == 0 || !call.HasGetBody {
				t.Errorf("the call's request has a %d-byte body and GetBody %t, want both", len(call.Body), call.HasGetBody)
			}
			for i, d := range deadlines {
				if d <= 0 || d > DefaultTimeout {
					t.Errorf("request %d: deadline %v away, want within the client's %v", i, d, DefaultTimeout)
				}
			}
		})
	}
}
