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

// B5 (docs/perf/benchmarks.md): one whole SystemOne call through an
// in-memory transport, against its floor and against the naive comparator
// on the same transport.
//
//   - sdk: Client.SystemOne with the default deadline and retry policy:
//     encode, header template, request, the transport, reading the body,
//     decoding it. The plan's call/sdk.
//   - floor: the transport called directly with a request built beforehand,
//     its response drained, plus sonic's encode of the state through
//     codec.EncodeState into a presized buffer, as TestAllocWholeCall
//     measures E_sonic (its time includes EncodeState's shape and UTF-8
//     checks): the NF3 floor as that test defines it. No client can cost
//     less.
//   - naive: internal/testsupport/naive with sonic, G3 (a)'s comparator of
//     record; the plan's call/naive (AC-P6, AC-P7).
//   - naive-json: the same client with encoding/json, reported only.
//
// The q3 rows are the NF3 shape: the three questions of the upstream
// round-trip test, a 1 KiB boxed-string state and result.json. The -q20
// rows ask the twenty questions result-20.json answers. The naive client is
// handed the SDK client's own header template, URL, model, deadline and
// prepared question bytes, so both send the same request byte for byte
// (TestNaiveRequestMatchesSDK).
//
// How this can mislead: the Recorder answers at once and discards the
// request body, so everything the network costs is absent by design (B6
// has a loopback socket). B/op runs with the collector on and every P, so
// a collection that empties a sync.Pool shows as a fraction of an
// allocation; TestAllocWholeCall has the exact counts.

import (
	"bytes"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport/naive"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// Sinks keep the benchmarks' results alive.
var (
	sinkCall  *SystemOneResponse
	sinkNaive map[string]any
)

// callScenario is one whole-call shape of B5.
type callScenario struct {
	name      string // "q3" or "q20"
	suffix    string // appended to each row's name: "" for q3, the plan's call/sdk and call/naive
	fixture   string // the response body
	answers   int    // how many answers the fixture holds
	questions func(tb testing.TB) *Prepared
}

// callScenarios are B5's shapes, in report order.
var callScenarios = []callScenario{
	{name: "q3", suffix: "", fixture: "result.json", answers: 3, questions: q3Questions},
	{name: "q20", suffix: "-q20", fixture: "result-20.json", answers: 20, questions: q20Questions},
}

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

// newNaiveClient returns the naive comparator of c's calls asking qs: the
// same transport, URL, header template, model, deadline and question
// bytes, encoding and decoding with cd.
func newNaiveClient(c *Client, rt http.RoundTripper, qs *Prepared, cd naive.Codec) *naive.Client {
	return &naive.Client{
		Transport: rt,
		URL:       c.cfg.systemOneURL.String(),
		Header:    c.cfg.systemOneHeader,
		Model:     c.cfg.model,
		Questions: qs.w.Questions,
		Timeout:   c.cfg.timeout,
		Codec:     cd,
	}
}

// q20Questions is the question set result-20.json answers, as S-C1 built
// it (testdata/README.md): each question named after its answer, a choice's
// options the keys of its probabilities in wire order, a score's levels its
// legend's texts in level order.
func q20Questions(tb testing.TB) *Prepared {
	tb.Helper()
	var res wire.SystemOneResult
	if _, err := codec.DecodeSystemOne(testsupport.Fixture(tb, "result-20.json"), nil, "", &res); err != nil {
		tb.Fatal(err)
	}
	qs := NewQuestions()
	for _, e := range res.Answers.Entries() {
		name := strings.Clone(e.Name)
		instructions := Text("Question about " + name + "?")
		switch e.Answer.Kind {
		case wire.KindNoul:
			qs.Noul(name, Noul{Instructions: instructions})
		case wire.KindChoice:
			var opts Options
			for _, p := range e.Answer.Choice.Probabilities {
				opts = append(opts, Option{Label: strings.Clone(p.Label)})
			}
			qs.Choice(name, Choice{Instructions: instructions, Options: opts})
		case wire.KindScore:
			legend := slices.Clone(e.Answer.Score.Legend)
			slices.SortFunc(legend, func(a, b wire.LegendEntry) int { return int(a.Level) - int(b.Level) })
			levels := make([]Content, 0, len(legend))
			for _, l := range legend {
				levels = append(levels, Text(strings.Clone(l.Description.Text)))
			}
			qs.Score(name, Score{Instructions: instructions, Levels: levels})
		default:
			tb.Fatalf("result-20.json: answer %q of kind %v", e.Name, e.Answer.Kind)
		}
	}
	p := mustPrepared(tb, qs)
	if p.Len() != 20 {
		tb.Fatalf("result-20.json asks %d questions, want 20", p.Len())
	}
	return p
}

// roundTripFloor is the floor of a call: rt called with a request built
// beforehand, its response drained into io.Discard and closed. It is a copy
// of alloc_call_test.go's floorCall, the source of truth, which only the
// non-race build compiles; TestRoundTripFloorMatchesFloorCall keeps the two
// alike until W5.2 moves floorCall to a file every build compiles and this
// copy goes.
func roundTripFloor(rt http.RoundTripper, req *http.Request) error {
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, resp.Body)
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	return err
}

// naiveAnswers returns how many answers a naive call decoded.
func naiveAnswers(out map[string]any) int {
	answers, _ := out["answers"].(map[string]any)
	return len(answers)
}

// BenchmarkCall is B5; see the comment at the top of this file.
func BenchmarkCall(b *testing.B) {
	for _, sc := range callScenarios {
		qs := sc.questions(b)
		rec := &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, testsupport.Fixture(b, sc.fixture))}}
		c := newBenchClient(b, rec)
		state := newCallState()

		b.Run("sdk"+sc.suffix, func(b *testing.B) {
			ctx := b.Context()
			resp, err := c.SystemOne(ctx, state, qs)
			if err != nil {
				b.Fatal(err)
			}
			if n := resp.Answers().Len(); n != sc.answers {
				b.Fatalf("%d answers, want %d", n, sc.answers)
			}
			b.ReportAllocs()
			for b.Loop() {
				if sinkCall, err = c.SystemOne(ctx, state, qs); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("floor"+sc.suffix, func(b *testing.B) {
			enc, err := encodeBody(state, c.cfg.model, qs, nil)
			if err != nil {
				b.Fatal(err)
			}
			sent := bytes.Clone(enc.Bytes())
			enc.Release()
			rd := bytes.NewReader(sent)
			req := &http.Request{
				Method: http.MethodPost, URL: c.cfg.systemOneURL, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
				Header: c.cfg.systemOneHeader, Body: io.NopCloser(rd), ContentLength: int64(len(sent)), Host: c.cfg.systemOneURL.Host,
			}
			buf := make([]byte, 0, 4<<10)
			b.ReportAllocs()
			for b.Loop() {
				buf = buf[:0]
				if err := codec.EncodeState(&buf, state); err != nil {
					b.Fatal(err)
				}
				rd.Reset(sent)
				if err := roundTripFloor(rec, req); err != nil {
					b.Fatal(err)
				}
			}
		})

		for _, nc := range naiveCodecs {
			client := newNaiveClient(c, rec, qs, nc.codec)
			b.Run(nc.name+sc.suffix, func(b *testing.B) {
				ctx := b.Context()
				out, err := client.SystemOne(ctx, state)
				if err != nil {
					b.Fatal(err)
				}
				if n := naiveAnswers(out); n != sc.answers {
					b.Fatalf("%d answers, want %d", n, sc.answers)
				}
				b.ReportAllocs()
				for b.Loop() {
					if sinkNaive, err = client.SystemOne(ctx, state); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// TestNaiveRequestMatchesSDK checks that B5 compares like with like: for
// each scenario, the naive client with either codec sends the request the
// SDK's call sends (method, URL, Host, every header, the body byte for
// byte, its declared length and a GetBody) under the client's per-attempt
// deadline, and decodes the same number of answers. It ranges over B5's
// own scenarios and codecs rather than a map of cases, so that it checks
// exactly what the benchmark runs.
func TestNaiveRequestMatchesSDK(t *testing.T) {
	for _, sc := range callScenarios {
		for _, nc := range naiveCodecs {
			t.Run("success: "+nc.name+" "+sc.name, func(t *testing.T) {
				rec := replying(http.StatusOK, testsupport.Fixture(t, sc.fixture))
				var remaining []time.Duration // each request's time left before its deadline; -1 for none
				rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
					if dl, ok := req.Context().Deadline(); ok {
						remaining = append(remaining, time.Until(dl))
					} else {
						remaining = append(remaining, -1)
					}
					return rec.RoundTrip(req)
				})
				qs := sc.questions(t)
				c := newBenchClient(t, rt)
				state := newCallState()
				resp, err := c.SystemOne(t.Context(), state, qs)
				if err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
				out, err := newNaiveClient(c, rt, qs, nc.codec).SystemOne(t.Context(), state)
				if err != nil {
					t.Fatalf("naive SystemOne: %v", err)
				}
				// Both deadlines are the client's DefaultTimeout from the
				// moment each request was built; a second of slack covers a
				// slow runner.
				for i, d := range remaining {
					switch {
					case d < 0:
						t.Errorf("request %d (0 SDK, 1 naive) has no deadline, want one %v away", i, DefaultTimeout)
					case d <= DefaultTimeout-time.Second || d > DefaultTimeout:
						t.Errorf("request %d (0 SDK, 1 naive): deadline %v away, want within a second of %v", i, d, DefaultTimeout)
					}
				}
				if got, want := naiveAnswers(out), resp.Answers().Len(); got != want || got != sc.answers {
					t.Errorf("answers: naive %d, SDK %d, want %d", got, want, sc.answers)
				}
				reqs := rec.Requests()
				if len(reqs) != 2 {
					t.Fatalf("the transport saw %d requests, want 2", len(reqs))
				}
				sdk, nv := reqs[0], reqs[1]
				sdk.Index, nv.Index = 0, 0
				if diff := gocmp.Diff(sdk, nv); diff != "" {
					t.Errorf("request (-sdk +naive):\n%s", diff)
				}
				if len(sdk.Header) != 6 || len(sdk.Body) == 0 {
					t.Errorf("the SDK's request has %d headers and a %d-byte body, want 6 and a body", len(sdk.Header), len(sdk.Body))
				}
			})
		}
	}
}
