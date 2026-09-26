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

// The benchmarks that stay in the root package (owner directive G5 moved the
// others to internal/benchmark; docs/perf/benchmarks.md lists both). Each
// times, or builds its case from, what only the root package can reach:
//
//   - BenchmarkPrepare: its case table (prepare_cases_test.go) is shared
//     with TestAllocPrepare, which reads unexported fields of the result.
//   - BenchmarkFalsyJSON: the unexported falsiness check of a raw score
//     question's criteria.
//   - BenchmarkEncodeBody (B1): its sdk arm times the unexported encodeBody;
//     the naive arms stay beside it so that the three encoders are compared
//     on the same states in one run.
//   - BenchmarkAssembly (B3): it rebuilds Client.attempt's unexported steps.
//   - BenchmarkBackoff (B4's delay): the unexported backoff.
//   - BenchmarkLoopback/cold-fanout-64 (B6's cold burst): its gate counters
//     come from the client's unexported transport.
//
// None of them needs a production change to move; each would need an
// exported hook that exists only for a benchmark, which the directive rules
// out.

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/h2gate"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport/naive"
)

// naiveCodecs are the naive comparator's two codecs, by row name.
var naiveCodecs = []struct {
	name  string
	codec naive.Codec
}{
	{name: "naive", codec: naive.Sonic},
	{name: "naive-json", codec: naive.StdJSON},
}

// newCallState returns the NF3 state: 1 KiB of text once encoded, boxed in
// an any before the call.
func newCallState() any { return strings.Repeat("s", 1<<10-2) }

// newBenchClient builds a client over rt with every setting an option
// gives, so that the environment cannot change the request, and closes it
// when tb ends.
func newBenchClient(tb testing.TB, rt http.RoundTripper, opts ...ClientOption) *Client {
	tb.Helper()
	c, err := NewClient(append([]ClientOption{WithRoundTripper(rt), WithAPIKey(testKey), WithBaseURL(DefaultBaseURL), WithModel(DefaultModel)}, opts...)...)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = c.Close() })
	return c
}

// BenchmarkPrepare measures Questions.Prepare on each set of prepareCases.
// The set is built once, outside the loop: Prepare only reads it.
func BenchmarkPrepare(b *testing.B) {
	for _, name := range slices.Sorted(maps.Keys(prepareCases)) {
		qs := prepareCases[name]()
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				p, err := qs.Prepare()
				if err != nil {
					b.Fatal(err)
				}
				prepareSink = p
			}
		})
	}
}

// BenchmarkFalsyJSON measures the falsiness check of a raw score question's
// JSON criteria alone: below 32 compact bytes, its copy stays on the stack.
func BenchmarkFalsyJSON(b *testing.B) {
	inputs := map[string][]byte{
		"small-14B": []byte(`[ "low", "high" ]`),
		"array":     prettyLevels(),
		"map":       []byte(prettyScale),
	}
	for _, name := range slices.Sorted(maps.Keys(inputs)) {
		raw := inputs[name]
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if falsyJSON(raw) {
					b.Fatalf("falsyJSON(%s) = true, want false", raw)
				}
			}
		})
	}
}

// B1 (docs/perf/benchmarks.md): the request body encode, by state kind and
// size, next to the naive comparator's encode of the same body.
//
//   - sdk: encodeBody, the SDK's hot path: the state goes through the codec
//     into a pooled scratch, then the model and the prepared question bytes
//     are spliced in; the scratch goes back to the pool after each call.
//   - naive: sonic.Marshal of internal/testsupport/naive's plain Body with
//     the same state, model and question bytes (G3 (a)); a fresh buffer
//     every call.
//   - naive-json: the same with encoding/json, reported only.
//
// The kinds are what a caller sends: text (a string boxed in an any, the
// NF3 kind), rawjson (a RawJSON object, which the SDK appends as it is and
// both codecs validate through its MarshalJSON), struct (a *struct with one
// large text member) and map (a map[string]any with the same members). The
// text holds quotes, tabs, newlines and multi-byte characters, so the
// escaper does real work; a state of one repeated letter would flatter
// every encoder. The body is byte for byte the same for all three encoders
// for text, rawjson and struct (TestEncodeBodyMatchesNaive); a map's members
// come out in each encoder's own order.
//
// How this can mislead: the scratch is warm after the first call, so these
// are steady-state numbers; a scratch's growth and its drop past the 8 MiB
// ceiling are what TestAllocScratchSequence asserts, not what this shows.
// Time per byte grows with the share of escaped characters in the text.

// sinkEncoded keeps the naive encodes alive.
var sinkEncoded []byte

// encodeSizes are B1's state sizes, by row name.
var encodeSizes = map[string]int{"1KiB": 1 << 10, "64KiB": 64 << 10, "1MiB": 1 << 20}

// encodeText is the unit B1's text states repeat: an ASCII sentence with
// quotes, a tab and a newline, Japanese text and an emoji, and none of <, >
// and &, which encoding/json alone escapes.
const encodeText = "He said \"please refund order 1042\".\n\tThe invoice was charged twice: 請求が二重に計上されました 🌍 "

// benchText returns whole repeats of encodeText, at most n bytes long.
func benchText(n int) string {
	return strings.Repeat(encodeText, max(n/len(encodeText), 1))
}

// benchTicket is B1's struct state: one large text member among small ones.
type benchTicket struct {
	Subject  string   `json:"subject"`
	Body     string   `json:"body"`
	Tags     []string `json:"tags"`
	Priority int      `json:"priority"`
}

// encodeKinds build B1's states of about n bytes, by row name.
var encodeKinds = map[string]func(n int) any{
	"text": func(n int) any { return benchText(n) },
	"rawjson": func(n int) any {
		return RawJSON(`{"subject":"billing","body":` + strconv.Quote(benchText(n)) + `,"tags":["billing","refund"],"priority":2}`)
	},
	"struct": func(n int) any {
		return &benchTicket{Subject: "billing", Body: benchText(n), Tags: []string{"billing", "refund"}, Priority: 2}
	},
	"map": func(n int) any {
		return map[string]any{"subject": "billing", "body": benchText(n), "tags": []any{"billing", "refund"}, "priority": 2}
	},
}

// BenchmarkEncodeBody is B1; see the comment at the top of this file.
func BenchmarkEncodeBody(b *testing.B) {
	qs := q3Questions(b)
	for _, kind := range slices.Sorted(maps.Keys(encodeKinds)) {
		for _, size := range []string{"1KiB", "64KiB", "1MiB"} {
			state := encodeKinds[kind](encodeSizes[size])
			first, err := encodeBody(state, DefaultModel, qs, nil)
			if err != nil {
				b.Fatalf("%s/%s: %v", kind, size, err)
			}
			n := int64(first.Len())
			first.Release()

			b.Run(kind+"/"+size+"/sdk", func(b *testing.B) {
				b.SetBytes(n)
				b.ReportAllocs()
				for b.Loop() {
					body, err := encodeBody(state, DefaultModel, qs, nil)
					if err != nil {
						b.Fatal(err)
					}
					body.Release()
				}
			})
			for _, nc := range naiveCodecs {
				b.Run(kind+"/"+size+"/"+nc.name, func(b *testing.B) {
					b.SetBytes(n)
					b.ReportAllocs()
					for b.Loop() {
						if sinkEncoded, err = nc.codec.Encode(state, DefaultModel, qs.w.Questions); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

// TestEncodeBodyMatchesNaive checks that B1 times the same output three
// ways: for every kind but map, at every size, the SDK's body and both
// naive encodes are the same bytes; a map's bodies have the same length,
// its members in each encoder's order.
func TestEncodeBodyMatchesNaive(t *testing.T) {
	qs := q3Questions(t)
	for kind, build := range encodeKinds {
		for size, n := range encodeSizes {
			t.Run("success: "+kind+"/"+size, func(t *testing.T) {
				state := build(n)
				body, err := encodeBody(state, DefaultModel, qs, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer body.Release()
				sdk := string(body.Bytes())
				if len(sdk) < n {
					t.Errorf("the SDK's body is %d bytes, want at least the state's %d", len(sdk), n)
				}
				for _, nc := range naiveCodecs {
					got, err := nc.codec.Encode(state, DefaultModel, qs.w.Questions)
					if err != nil {
						t.Fatalf("%s: %v", nc.name, err)
					}
					if kind == "map" {
						if len(got) != len(sdk) {
							t.Errorf("%s: map body is %d bytes, the SDK's %d", nc.name, len(got), len(sdk))
						}
						continue
					}
					if string(got) != sdk {
						t.Errorf("%s: body differs from the SDK's (%d vs %d bytes)\nnaive: %.200s\nsdk:   %.200s", nc.name, len(got), len(sdk), got, sdk)
					}
				}
			})
		}
	}
}

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
// every header, the body byte for byte, its length and a GetBody. It
// cannot see a change that alters only the cost (a copy of the header
// template, say), so re-sync assembleRequest with Client.attempt and
// SystemOne whenever they change.
//
// How this can mislead: the retry policy, the logging checks and the
// transport are not in it; B5's whole call has all of them.

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

// B4's backoff delay (docs/perf/benchmarks.md): BenchmarkBackoff/
// {retry-1,retry-6,retry-1000} times the unexported backoff with
// DefaultRetry's numbers (500 ms initial, 5 s maximum, jitter 0.25) at the
// first retry, one past the cap and far past it, where the cap is taken in
// log2 space before any doubling; schedule is the waits of DefaultRetry's
// two retries, what one call that fails every attempt computes. The random
// draw is a constant function, so no generator is timed. The backoff is
// pure float64 arithmetic and one format-and-parse round trip, the one that
// rounds as Python's round(x, 3) does; it runs once per retry, next to a
// wait of hundreds of milliseconds, so the rows are recorded, not targets.
// B4's Retry-After parse moved to internal/benchmark.

// sinkDelay keeps BenchmarkBackoff's result alive.
var sinkDelay time.Duration

// BenchmarkBackoff is B4's backoff delay; see the comment at the top of
// this file.
func BenchmarkBackoff(b *testing.B) {
	random := func() float64 { return 0.5 }
	for _, retry := range []int{1, 6, 1000} {
		b.Run("retry-"+strconv.Itoa(retry), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkDelay = backoff(retry, defaultBackoffInitial, defaultBackoffMax, defaultBackoffJitter, random)
			}
		})
	}
	b.Run("schedule", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var total time.Duration
			for retry := 1; retry <= defaultMaxRetries; retry++ {
				total += backoff(retry, defaultBackoffInitial, defaultBackoffMax, defaultBackoffJitter, random)
			}
			sinkDelay = total
		}
	})
}

// B6's cold burst (docs/perf/benchmarks.md): a fresh client over the
// default transport (internal/h2gate) and a loopback HTTP/2 server over TLS,
// then 64 q3 calls started at once, until the last answers, then the client
// is closed; the cold burst of AC-P4 and K22 (the waiters wait for the
// leader's response headers, so a burst pays two round trips of the
// server). Each burst runs under a 30 s deadline besides each attempt's
// own. Three metrics are recorded, never asserted. conns/op, the
// connections each burst opened, is a sanity count only: on loopback the
// stock HTTP/2 pool alone also puts a cold burst on one connection (review
// W5.1 MINOR 1, with the gate bypassed), so AC-P4's evidence stays
// internal/h2gate's TestFanOut. leaders/op and firstholds/op are the gate's
// own counters (h2gate.Stats: cold dials led, and first requests on a new
// connection that held the header-write token until their response
// headers), 1 each for a burst the gate ran; the exported Client.Stats
// carries neither, which is why this arm stays here while B6's warm call
// moved to internal/benchmark. The server is testsupport.NewFixtureServer;
// wall clock only, with the server's allocations in B/op and allocs/op.

// fanOut is how many calls cold-fanout-64 starts at once.
const fanOut = 64

// BenchmarkLoopback is B6's cold burst; see the comment above.
func BenchmarkLoopback(b *testing.B) {
	srv := testsupport.NewFixtureServer(b)
	opts := []ClientOption{WithAPIKey(testKey), WithBaseURL(srv.URL()), WithModel(DefaultModel), WithRootCAs(testsupport.RootCAs(b)), WithProxy(nil)}
	qs := q3Questions(b)
	state := newCallState()

	b.Run("cold-fanout-64", func(b *testing.B) {
		var bursts, conns int
		var leaders, firstHolds uint64
		b.ReportAllocs()
		for b.Loop() {
			before := srv.Accepts()
			st, err := coldBurst(b.Context(), opts, state, qs)
			if err != nil {
				b.Fatal(err)
			}
			conns += srv.Accepts() - before
			leaders += st.Leaders
			firstHolds += st.FirstHolds
			bursts++
		}
		b.ReportMetric(float64(conns)/float64(bursts), "conns/op")
		b.ReportMetric(float64(leaders)/float64(bursts), "leaders/op")
		b.ReportMetric(float64(firstHolds)/float64(bursts), "firstholds/op")
	})
}

// coldBurst builds a client from opts, starts fanOut calls at once, waits
// for all of them under a 30 s deadline, closes the client and returns its
// transport's gate counters and the first error a call returned.
func coldBurst(ctx context.Context, opts []ClientOption, state any, qs *Prepared) (h2gate.Stats, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c, err := NewClient(opts...)
	if err != nil {
		return h2gate.Stats{}, err
	}
	defer func() { _ = c.Close() }()
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	for range fanOut {
		wg.Go(func() {
			if _, err := c.SystemOne(ctx, state, qs); err != nil {
				once.Do(func() { first = err })
			}
		})
	}
	wg.Wait()
	return c.cfg.transport.stats(), first
}
