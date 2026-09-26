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

package benchmark

// B5 (docs/perf/benchmarks.md): one whole SystemOne call through an
// in-memory transport, against its floor and against the naive comparator
// on the same transport.
//
//   - sdk: Client.SystemOne with the default deadline and retry policy:
//     encode, header template, request, the transport, reading the body,
//     decoding it. The plan's call/sdk.
//   - floor: testsupport.FloorCall, the transport called directly with a
//     request built beforehand and its response drained, plus sonic's
//     encode of the state through codec.EncodeState into a presized
//     buffer, as TestAllocWholeCall measures E_sonic (its time includes
//     EncodeState's shape and UTF-8 checks): the NF3 floor as that test
//     defines it, through the same FloorCall. No client can cost less.
//   - naive: internal/testsupport/naive with sonic, G3 (a)'s comparator of
//     record; the plan's call/naive (AC-P6, AC-P7).
//   - naive-json: the same client with encoding/json, reported only.
//
// The q3 rows are the NF3 shape: the three questions of the upstream
// round-trip test, a 1 KiB boxed-string state and result.json. The -q20
// rows ask the twenty questions result-20.json answers. Before timing,
// each scenario makes one real SDK call through a recording transport;
// the floor sends that request's bytes again, and the naive client takes
// its URL, header template and question bytes from it, with the SDK's
// default model and deadline. So both clients send the same request byte
// for byte (TestNaiveRequestMatchesSDK), through the exported API only.
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
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	typesafe "github.com/zchee/typesafe-sdk-go"
	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport/naive"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// Sinks keep the benchmarks' results alive.
var (
	sinkCall  *typesafe.SystemOneResponse
	sinkNaive map[string]any
)

// callScenario is one whole-call shape of B5.
type callScenario struct {
	name      string // "q3" or "q20"
	suffix    string // appended to each row's name: "" for q3, the plan's call/sdk and call/naive
	fixture   string // the response body
	answers   int    // how many answers the fixture holds
	questions func(tb testing.TB) *typesafe.Prepared
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

// q20Questions is the question set result-20.json answers, as S-C1 built
// it (testdata/README.md): each question named after its answer, a choice's
// options the keys of its probabilities in wire order, a score's levels its
// legend's texts in level order.
func q20Questions(tb testing.TB) *typesafe.Prepared {
	tb.Helper()
	var res wire.SystemOneResult
	if _, err := codec.DecodeSystemOne(testsupport.Fixture(tb, "result-20.json"), nil, "", &res); err != nil {
		tb.Fatal(err)
	}
	qs := typesafe.NewQuestions()
	for _, e := range res.Answers.Entries() {
		name := strings.Clone(e.Name)
		instructions := typesafe.Text("Question about " + name + "?")
		switch e.Answer.Kind {
		case wire.KindNoul:
			qs.Noul(name, typesafe.Noul{Instructions: instructions})
		case wire.KindChoice:
			var opts typesafe.Options
			for _, p := range e.Answer.Choice.Probabilities {
				opts = append(opts, typesafe.Option{Label: strings.Clone(p.Label)})
			}
			qs.Choice(name, typesafe.Choice{Instructions: instructions, Options: opts})
		case wire.KindScore:
			legend := slices.Clone(e.Answer.Score.Legend)
			slices.SortFunc(legend, func(a, b wire.LegendEntry) int { return int(a.Level) - int(b.Level) })
			levels := make([]typesafe.Content, 0, len(legend))
			for _, l := range legend {
				levels = append(levels, typesafe.Text(strings.Clone(l.Description.Text)))
			}
			qs.Score(name, typesafe.Score{Instructions: instructions, Levels: levels})
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

// sentRequest returns the request a client's call asking qs with the NF3
// state sends, as a Recorder answering fixture records it.
func sentRequest(tb testing.TB, qs *typesafe.Prepared, fixture string) testsupport.RecordedRequest {
	tb.Helper()
	rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, testsupport.Fixture(tb, fixture))}}
	c := newBenchClient(tb, rec)
	if _, err := c.SystemOne(tb.Context(), newCallState(), qs); err != nil {
		tb.Fatal(err)
	}
	reqs := rec.Requests()
	if len(reqs) != 1 {
		tb.Fatalf("the transport saw %d requests, want 1", len(reqs))
	}
	return reqs[0]
}

// questionsMember is what precedes the question set in a request body; the
// state and the model, which come before it, are the benchmarks' own and
// never contain it.
const questionsMember = `,"questions":`

// sentQuestions returns the question set's JSON from a body the SDK sent,
// {"state":...,"model":...,"questions":<set>}: the bytes the SDK prepared.
func sentQuestions(tb testing.TB, body []byte) []byte {
	tb.Helper()
	i := bytes.Index(body, []byte(questionsMember))
	if i < 0 || !bytes.HasSuffix(body, []byte("}")) {
		tb.Fatalf("no question set in the body %.120s", body)
	}
	return body[i+len(questionsMember) : len(body)-1]
}

// newNaiveClient returns the naive comparator of the call that sent
// sent: the same URL, header template and question bytes, with the SDK's
// default model and per-attempt deadline, through rt, encoding and
// decoding with cd.
func newNaiveClient(tb testing.TB, sent testsupport.RecordedRequest, rt http.RoundTripper, cd naive.Codec) *naive.Client {
	tb.Helper()
	return &naive.Client{
		Transport: rt,
		URL:       sent.URL,
		Header:    sent.Header,
		Model:     typesafe.DefaultModel,
		Questions: sentQuestions(tb, sent.Body),
		Timeout:   typesafe.DefaultTimeout,
		Codec:     cd,
	}
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
		sent := sentRequest(b, qs, sc.fixture)
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
			u, err := url.Parse(sent.URL)
			if err != nil {
				b.Fatal(err)
			}
			rd := bytes.NewReader(sent.Body)
			req := &http.Request{
				Method: http.MethodPost, URL: u, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
				Header: sent.Header, Body: io.NopCloser(rd), ContentLength: int64(len(sent.Body)), Host: sent.Host,
			}
			buf := make([]byte, 0, 4<<10)
			b.ReportAllocs()
			for b.Loop() {
				buf = buf[:0]
				if err := codec.EncodeState(&buf, state); err != nil {
					b.Fatal(err)
				}
				rd.Reset(sent.Body)
				if err := testsupport.FloorCall(rec, req); err != nil {
					b.Fatal(err)
				}
			}
		})

		for _, nc := range naiveCodecs {
			client := newNaiveClient(b, sent, rec, nc.codec)
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
// deadline, and decodes the same number of answers. The naive client takes
// its URL, header template and question bytes from the SDK's own first
// request, as BenchmarkCall's does, so the body check is what proves its
// encode of the state and the model, and the deadline check its timeout.
// It ranges over B5's own scenarios and codecs rather than a map of cases,
// so that it checks exactly what the benchmark runs.
func TestNaiveRequestMatchesSDK(t *testing.T) {
	for _, sc := range callScenarios {
		for _, nc := range naiveCodecs {
			t.Run("success: "+nc.name+" "+sc.name, func(t *testing.T) {
				rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, testsupport.Fixture(t, sc.fixture))}}
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
				out, err := newNaiveClient(t, rec.Requests()[0], rt, nc.codec).SystemOne(t.Context(), state)
				if err != nil {
					t.Fatalf("naive SystemOne: %v", err)
				}
				// Both deadlines are the client's DefaultTimeout from the
				// moment each request was built; a second of slack covers a
				// slow runner.
				for i, d := range remaining {
					switch {
					case d < 0:
						t.Errorf("request %d (0 SDK, 1 naive) has no deadline, want one %v away", i, typesafe.DefaultTimeout)
					case d <= typesafe.DefaultTimeout-time.Second || d > typesafe.DefaultTimeout:
						t.Errorf("request %d (0 SDK, 1 naive): deadline %v away, want within a second of %v", i, d, typesafe.DefaultTimeout)
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
