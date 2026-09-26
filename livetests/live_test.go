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

//go:build live

package livetests

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"iter"
	"maps"
	"math"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	typesafe "github.com/zchee/typesafe-sdk-go"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// record makes the live tests write the bodies the API returned to
// testdata/live (K6), each scrubbed of credentials first (writeFixture).
var record = flag.Bool("record", false, "write the live response bodies to testdata/live, credentials removed")

// liveTimeout is each attempt's deadline, as upstream's live client sets it
// (tests/conftest.py: timeout=120).
const liveTimeout = 120 * time.Second

// ticketState is the state of upstream's test_live_questions.
var ticketState = map[string]any{
	"subject": "Charged twice this month",
	"body":    "I see two charges of $49. I only have one account. Please fix this ASAP.",
}

// requireLive returns the live-test environment, or fails the test before
// it calls the API (AC-F11: the live tests fail without both variables).
func requireLive(tb testing.TB) liveEnv {
	tb.Helper()
	env, err := liveEnvFrom(os.Getenv)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Logf("live API host: %s", env.host)
	return env
}

// phases holds the httptrace times of one call, each measured from the
// call's start: what K22 records about the live API's first response.
type phases struct {
	mu                                      sync.Mutex
	start                                   time.Time
	dns, connect, tls, gotConn, wrote, ttfb time.Duration
	reused                                  bool
}

// begin starts a call's measurement.
func (p *phases) begin() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.start = time.Now()
	p.dns, p.connect, p.tls, p.gotConn, p.wrote, p.ttfb, p.reused = 0, 0, 0, 0, 0, 0, false
}

// mark sets *d to the time since the call's start, once.
func (p *phases) mark(d *time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if *d == 0 && !p.start.IsZero() {
		*d = time.Since(p.start)
	}
}

// trace returns the hooks that fill p.
func (p *phases) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSDone:          func(httptrace.DNSDoneInfo) { p.mark(&p.dns) },
		ConnectDone:      func(string, string, error) { p.mark(&p.connect) },
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) { p.mark(&p.tls) },
		GotConn: func(info httptrace.GotConnInfo) {
			p.mark(&p.gotConn)
			p.mu.Lock()
			p.reused = info.Reused
			p.mu.Unlock()
		},
		WroteRequest:         func(httptrace.WroteRequestInfo) { p.mark(&p.wrote) },
		GotFirstResponseByte: func() { p.mark(&p.ttfb) },
	}
}

// line renders the phases and the call's total.
func (p *phases) line(total time.Duration) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	ms := func(d time.Duration) string {
		if d == 0 {
			return "-"
		}
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	}
	return fmt.Sprintf("dns=%s connect=%s tls=%s gotconn=%s reused=%t wrote=%s first_byte=%s server=%s total=%s",
		ms(p.dns), ms(p.connect), ms(p.tls), ms(p.gotConn), p.reused, ms(p.wrote), ms(p.ttfb), ms(max(p.ttfb-p.wrote, 0)), ms(total))
}

// liveClient is a client on the SDK's own default transport whose records
// (LevelTrace and up) and connection phases the test reads.
type liveClient struct {
	c     *typesafe.Client
	logs  *testsupport.LogRecorder
	times *phases
	start time.Time
}

// newLiveClient builds a client from the environment (key, base URL) as a
// caller's would be built, plus the test's logger, trace and deadline.
func newLiveClient(t *testing.T, opts ...typesafe.ClientOption) *liveClient {
	t.Helper()
	lc := &liveClient{logs: testsupport.NewLogRecorder(typesafe.LevelTrace), times: new(phases)}
	base := []typesafe.ClientOption{
		typesafe.WithTimeout(liveTimeout),
		typesafe.WithLogger(lc.logs.Logger()),
		typesafe.WithClientTrace(lc.times.trace()),
	}
	c, err := typesafe.NewClient(append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	lc.c = c
	return lc
}

// begin starts measuring one call and forgets the previous call's records.
func (lc *liveClient) begin() {
	lc.logs.Reset()
	lc.times.begin()
	lc.start = time.Now()
}

// done logs the call's K22 line: its phases and the response's framing as
// the SDK saw it (the "response headers" record: Content-Length and
// Content-Encoding after the transport's own gzip handling).
func (lc *liveClient) done(t *testing.T, label string) {
	t.Helper()
	total := time.Since(lc.start)
	framing := "no response"
	if rec, ok := lc.lastRecord("response headers"); ok {
		cl, hasCL := rec.Attr("headers.Content-Length")
		ce, hasCE := rec.Attr("headers.Content-Encoding")
		n, _ := rec.Attr("body_bytes")
		framing = fmt.Sprintf("content_length=%s content_encoding=%s body_bytes=%s", attrOr(cl, hasCL), attrOr(ce, hasCE), n)
	}
	t.Logf("K22 %s: %s %s", label, lc.times.line(total), framing)
}

// attrOr renders a record attribute, or "absent".
func attrOr(v fmt.Stringer, ok bool) string {
	if !ok {
		return "absent"
	}
	return v.String()
}

// lastRecord returns the last record with message msg.
func (lc *liveClient) lastRecord(msg string) (testsupport.LogRecord, bool) {
	for _, rec := range slices.Backward(lc.logs.Records()) {
		if rec.Message == msg {
			return rec, true
		}
	}
	return testsupport.LogRecord{}, false
}

// recordBody writes body to testdata/live/name under -record, scrubbed of
// every credential, and otherwise only reports whether the recorder would
// refuse it.
func recordBody(t *testing.T, name string, body []byte, secrets ...string) {
	t.Helper()
	if !*record {
		if _, err := scrub(body, secrets...); err != nil {
			t.Logf("%s: the recorder would refuse this body: %v", name, err)
		}
		return
	}
	if err := writeFixture(filepath.Join("..", "testdata", "live"), name, body, secrets...); err != nil {
		t.Fatal(err)
	}
	t.Logf("recorded testdata/live/%s (%d bytes)", name, len(body))
}

// probabilitySum returns the sum of a Seq2's values.
func probabilitySum[K comparable](seq iter.Seq2[K, float64]) (map[K]float64, float64) {
	m := make(map[K]float64)
	sum := 0.0
	for k, v := range seq {
		m[k] = v
		sum += v
	}
	return m, sum
}

// TestLiveModels ports test_live_models: the API lists at least one model,
// each decoded with its name, description and release date. It records the
// body (models.json) and K22's first response on a cold client, then three
// warm calls on the same connection.
func TestLiveModels(t *testing.T) {
	env := requireLive(t)
	lc := newLiveClient(t)
	lc.begin()
	resp, err := lc.c.Models().List(t.Context())
	lc.done(t, "models cold")
	if err != nil {
		t.Fatalf("Models().List() error = %v", err)
	}
	models := resp.Models()
	if len(models) == 0 {
		t.Fatal("Models().List() returned no models")
	}
	for i, m := range models {
		t.Logf("model %d: name=%q release_date=%q description=%d bytes", i, m.Name(), m.ReleaseDate(), len(m.Description()))
	}
	recordBody(t, "models.json", resp.Meta().RawBody(), env.apiKey)
	for i := range 3 {
		lc.begin()
		if _, err := lc.c.Models().List(t.Context()); err != nil {
			t.Fatalf("warm Models().List() %d error = %v", i+1, err)
		}
		lc.done(t, fmt.Sprintf("models warm %d", i+1))
	}
	if st := lc.c.Stats(); st.Dials != 1 || st.Attempts != 4 {
		t.Errorf("Stats() = %+v, want 1 dial and 4 attempts", st)
	}
}

// TestLiveQuestions ports test_live_questions: one call with the three
// question forms mixed (a raw noul with structured criteria, a typed choice,
// a typed score) returns answers in range, probabilities summing to 1 within
// 0.1, and the score's legend as sent. It records questions.json.
func TestLiveQuestions(t *testing.T) {
	env := requireLive(t)
	lc := newLiveClient(t)
	qs, err := typesafe.NewQuestions().
		Raw("billing", typesafe.RawQuestion{Type: "noul", Fields: map[string]any{
			"instructions": "Is this ticket about billing?",
			"criteria": map[string]any{
				"true": map[string]any{"meaning": "Payments or invoices", "examples": []any{"charged twice"}},
			},
		}}).
		Choice("tone", typesafe.Choice{
			Instructions: typesafe.Text("What is the customer's tone?"),
			Options:      typesafe.Options{{Label: "calm"}, {Label: "frustrated"}, {Label: "angry"}},
		}).
		Score("urgency", typesafe.Score{
			Instructions: typesafe.Text("How urgent is this ticket?"),
			Levels:       []typesafe.Content{typesafe.Text("can wait"), typesafe.Text("this week"), typesafe.Text("today")},
		}).
		Prepare()
	if err != nil {
		t.Fatal(err)
	}
	lc.begin()
	resp, err := lc.c.SystemOne(t.Context(), ticketState, qs)
	lc.done(t, "system one cold")
	if err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}
	recordBody(t, "questions.json", resp.Meta().RawBody(), env.apiKey)

	if resp.Model() == "" {
		t.Error("Model() is empty")
	}
	if in, ok := resp.Usage().InputTokens(); ok && in == 0 {
		t.Error("InputTokens() = 0, want absent or positive")
	}
	if id, ok := resp.Meta().RequestID(); !ok || id == "" {
		t.Error("the response carries no request id")
	}
	answers := resp.Answers()
	billing, ok := answers.Noul("billing")
	if !ok || billing.Noul() < 0 || billing.Noul() > 1 {
		t.Errorf("billing = %v, %t: want a noul answer in [0, 1]", billing.Noul(), ok)
	}
	tone, ok := answers.Choice("tone")
	if !ok || !slices.Contains([]string{"calm", "frustrated", "angry"}, tone.Choice()) {
		t.Errorf("tone = %q, %t: want calm, frustrated or angry", tone.Choice(), ok)
	}
	if _, sum := probabilitySum(tone.Probabilities()); math.Abs(sum-1) > 0.1 {
		t.Errorf("tone probabilities sum to %v, want 1 within 0.1", sum)
	}
	urgency, ok := answers.Score("urgency")
	if !ok || urgency.Score() < 0 || urgency.Score() > 2 {
		t.Errorf("urgency = %v, %t: want a score in [0, 2]", urgency.Score(), ok)
	}
	legend := map[uint32]string{}
	for level, c := range urgency.Legend() {
		legend[level] = c.Text()
	}
	if diff := gocmp.Diff(map[uint32]string{0: "can wait", 1: "this week", 2: "today"}, legend); diff != "" {
		t.Errorf("urgency legend (-want +got):\n%s", diff)
	}
	probs, sum := probabilitySum(urgency.Probabilities())
	if diff := gocmp.Diff([]uint32{0, 1, 2}, slices.Sorted(maps.Keys(probs))); diff != "" {
		t.Errorf("urgency probability levels (-want +got):\n%s", diff)
	}
	if math.Abs(sum-1) > 0.1 {
		t.Errorf("urgency probabilities sum to %v, want 1 within 0.1", sum)
	}
}

// liveTicket is upstream's PydanticQuestionsResponse as a typed set: the
// questions of test_live_pydantic_response, the noul without criteria.
type liveTicket struct {
	Billing typesafe.NoulAnswer   `typesafe:"kind=noul;name=billing;instructions=Is this ticket about billing?"`
	Tone    typesafe.ChoiceAnswer `typesafe:"kind=choice;name=tone;instructions=What is the customer's tone?;options=calm|frustrated|angry"`
	Urgency typesafe.ScoreAnswer  `typesafe:"kind=score;name=urgency;instructions=How urgent is this ticket?;levels=can wait|this week|today"`
}

// choiceView and scoreView are the comparable forms of the two
// incomparable answer types.
type choiceView struct {
	Present       bool
	Choice        string
	Confidence    float64
	Probabilities map[string]float64
}

type scoreView struct {
	Present       bool
	Score         float64
	Confidence    float64
	Legend        map[uint32]string
	Probabilities map[uint32]float64
}

func viewChoice(a typesafe.ChoiceAnswer) choiceView {
	p, _ := probabilitySum(a.Probabilities())
	return choiceView{a.Present(), a.Choice(), a.Confidence(), p}
}

func viewScore(a typesafe.ScoreAnswer) scoreView {
	p, _ := probabilitySum(a.Probabilities())
	legend := map[uint32]string{}
	for level, c := range a.Legend() {
		if c.IsJSON() {
			legend[level] = string(c.JSON())
		} else {
			legend[level] = c.Text()
		}
	}
	return scoreView{a.Present(), a.Score(), a.Confidence(), legend, p}
}

// TestLiveTypedResponse ports test_live_pydantic_response through Ask[T]:
// one call returns the typed answers in range and a request id (read from
// the SDK's INFO "response" record, since Ask returns T alone). The body,
// taken from the LevelTrace "response body" record and recorded as
// typed-response.json, is then decoded offline twice: DecodeAs[T] over the
// stored response gives the same T, and the untyped Answers() of it the same
// answers (upstream: result.billing == result.nouls["billing"]).
func TestLiveTypedResponse(t *testing.T) {
	env := requireLive(t)
	lc := newLiveClient(t)
	state := map[string]any{"subject": "Charged twice this month", "body": "I see two charges of $49. Please fix this ASAP."}
	lc.begin()
	ticket, err := typesafe.Ask[liveTicket](t.Context(), lc.c, state)
	lc.done(t, "ask cold")
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if n := ticket.Billing.Noul(); !ticket.Billing.Present() || n < 0 || n > 1 {
		t.Errorf("Billing = %v, want a noul answer in [0, 1]", n)
	}
	if c := ticket.Tone.Choice(); !slices.Contains([]string{"calm", "frustrated", "angry"}, c) {
		t.Errorf("Tone = %q, want calm, frustrated or angry", c)
	}
	if s := ticket.Urgency.Score(); s < 0 || s > 2 {
		t.Errorf("Urgency = %v, want a score in [0, 2]", s)
	}
	info, ok := lc.lastRecord("response")
	if id, has := info.Attr("request_id"); !ok || !has || id.String() == "" {
		t.Error("the INFO response record carries no request id")
	}
	bodyRec, ok := lc.lastRecord("response body")
	if !ok {
		t.Fatal("no LevelTrace response body record")
	}
	bodyAttr, _ := bodyRec.Attr("body")
	body := []byte(bodyAttr.String())
	recordBody(t, "typed-response.json", body, env.apiKey)

	var stored typesafe.SystemOneResponse
	if err := stored.UnmarshalJSON(body); err != nil {
		t.Fatalf("UnmarshalJSON(the recorded body) error = %v", err)
	}
	again, err := typesafe.DecodeAs[liveTicket](&stored)
	if err != nil {
		t.Fatalf("DecodeAs(the recorded body) error = %v", err)
	}
	if again.Billing != ticket.Billing {
		t.Errorf("DecodeAs Billing = %+v, Ask Billing = %+v", again.Billing, ticket.Billing)
	}
	if diff := gocmp.Diff(viewChoice(ticket.Tone), viewChoice(again.Tone)); diff != "" {
		t.Errorf("Tone, Ask vs DecodeAs (-ask +decodeas):\n%s", diff)
	}
	if diff := gocmp.Diff(viewScore(ticket.Urgency), viewScore(again.Urgency)); diff != "" {
		t.Errorf("Urgency, Ask vs DecodeAs (-ask +decodeas):\n%s", diff)
	}
	answers := stored.Answers()
	billing, _ := answers.Noul("billing")
	tone, _ := answers.Choice("tone")
	urgency, _ := answers.Score("urgency")
	if billing != ticket.Billing {
		t.Errorf("Answers() billing = %+v, typed Billing = %+v", billing, ticket.Billing)
	}
	if diff := gocmp.Diff(viewChoice(ticket.Tone), viewChoice(tone)); diff != "" {
		t.Errorf("tone, typed vs Answers() (-typed +answers):\n%s", diff)
	}
	if diff := gocmp.Diff(viewScore(ticket.Urgency), viewScore(urgency)); diff != "" {
		t.Errorf("urgency, typed vs Answers() (-typed +answers):\n%s", diff)
	}
}

// withoutCredential sends each request without its Authorization header,
// on a clone, so the request the SDK built is left as it is: the API sees a
// request that carries no credential at all, which the SDK itself never
// sends.
type withoutCredential struct{ base *http.Transport }

// RoundTrip implements http.RoundTripper.
func (w withoutCredential) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.Header.Del("Authorization")
	return w.base.RoundTrip(r2)
}

// CloseIdleConnections closes the base transport's idle connections, as the
// client's Close asks.
func (w withoutCredential) CloseIdleConnections() { w.base.CloseIdleConnections() }

// TestLiveUnauthenticated checks AC-F11's last clause and the case next to
// it, on both endpoints and without a retry: a request that carries no
// credential is answered with 403 and the error type authentication_error,
// and one with a key the API did not issue with 401 and the same error type.
// The SDK reports both as an *APIError whose IsAuthentication is true: the
// first through its ErrorType (Kind permission denied), the second through
// its status as well (Kind authentication). The models endpoint's error
// bodies are recorded as unauthenticated.json (403) and wrong-key.json
// (401), each key scrubbed as the real one would be.
func TestLiveUnauthenticated(t *testing.T) {
	env := requireLive(t)
	var p http.Protocols
	p.SetHTTP2(true)
	stock := &http.Transport{Proxy: http.ProxyFromEnvironment, Protocols: &p}
	qs, err := typesafe.NewQuestions().Noul("spam", typesafe.Noul{Instructions: typesafe.Text("Is this spam?")}).Prepare()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		opts   []typesafe.ClientOption
		status int
		kind   typesafe.APIErrorKind
		record string
	}{
		"no credential": {
			opts:   []typesafe.ClientOption{typesafe.WithRoundTripper(withoutCredential{stock})},
			status: http.StatusForbidden,
			kind:   typesafe.APIErrorPermissionDenied,
			record: "unauthenticated.json",
		},
		"wrong key": {
			status: http.StatusUnauthorized,
			kind:   typesafe.APIErrorAuthentication,
			record: "wrong-key.json",
		},
	}
	for _, name := range slices.Sorted(maps.Keys(cases)) {
		tc := cases[name]
		lc := newLiveClient(t, append([]typesafe.ClientOption{typesafe.WithAPIKey(wrongLiveKey), typesafe.WithRetry(typesafe.NoRetry())}, tc.opts...)...)
		calls := map[string]func() error{
			"models": func() error {
				_, err := lc.c.Models().List(t.Context())
				return err
			},
			"system one": func() error {
				_, err := lc.c.SystemOne(t.Context(), "hello", qs)
				return err
			},
		}
		for _, endpoint := range slices.Sorted(maps.Keys(calls)) {
			t.Run(name+"/"+endpoint, func(t *testing.T) {
				lc.begin()
				err := calls[endpoint]()
				lc.done(t, name+" "+endpoint)
				apiErr, ok := errors.AsType[*typesafe.APIError](err)
				if !ok {
					t.Fatalf("error = %v, want an *APIError", err)
				}
				if apiErr.StatusCode != tc.status || apiErr.Kind != tc.kind || apiErr.ErrorType != "authentication_error" || !apiErr.IsAuthentication() {
					t.Errorf("StatusCode = %d, Kind = %v, ErrorType = %q, IsAuthentication() = %t; want %d, %v, authentication_error, true",
						apiErr.StatusCode, apiErr.Kind, apiErr.ErrorType, apiErr.IsAuthentication(), tc.status, tc.kind)
				}
				if strings.Contains(apiErr.Error(), env.apiKey) || strings.Contains(apiErr.Error(), wrongLiveKey) {
					t.Error("the error text holds a key")
				}
				t.Logf("%s %s: %s", name, endpoint, apiErr)
				if endpoint == "models" {
					recordBody(t, tc.record, apiErr.Body, env.apiKey, wrongLiveKey)
				}
			})
		}
		if st := lc.c.Stats(); st.Attempts != 2 {
			t.Errorf("%s: Stats().Attempts = %d, want 2 (no retry)", name, st.Attempts)
		}
	}
}

// framingProbe is a RoundTripper over a stock HTTP/2 transport that notes
// how each response is framed: what the SDK's body reader is handed
// (ContentLength, and whether the transport undid a gzip encoding).
type framingProbe struct {
	base  http.RoundTripper
	mu    sync.Mutex
	notes []string
}

// RoundTrip implements http.RoundTripper; it leaves the request as it is.
func (p *framingProbe) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := p.base.RoundTrip(r)
	if err == nil {
		p.mu.Lock()
		p.notes = append(p.notes, fmt.Sprintf("%s %s: proto=%s status=%d content_length=%d uncompressed=%t header_content_length=%q content_type=%q",
			r.Method, r.URL.Path, resp.Proto, resp.StatusCode, resp.ContentLength, resp.Uncompressed, resp.Header.Get("Content-Length"), resp.Header.Get("Content-Type")))
		p.mu.Unlock()
	}
	return resp, err
}

// TestLiveTransportFacts records K22's server facts, asserting nothing about
// their values: the SETTINGS the API sends on a new HTTP/2 connection
// (MAX_CONCURRENT_STREAMS among them; no request, no key), and how the
// models response is framed through a stock HTTP/2 transport with and
// without the transparent gzip that the SDK's transport requests.
func TestLiveTransportFacts(t *testing.T) {
	env := requireLive(t)
	addr := env.host
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "443")
	}
	settings, err := testsupport.ReadPeerSettings(t.Context(), addr, nil)
	if err != nil {
		t.Fatalf("ReadPeerSettings(%s) error = %v", addr, err)
	}
	limit, limited := settings.MaxConcurrentStreams()
	t.Logf("K22 settings: protocol=%s max_concurrent_streams=%d advertised=%t handshake=%s first_settings=%s all=%v",
		settings.Protocol, limit, limited, settings.Handshake, settings.FirstSettings, settings.Values)

	for _, gzip := range []bool{true, false} {
		t.Run(fmt.Sprintf("gzip requested %t", gzip), func(t *testing.T) {
			var p http.Protocols
			p.SetHTTP2(true)
			probe := &framingProbe{base: &http.Transport{
				Proxy:              http.ProxyFromEnvironment,
				Protocols:          &p,
				DisableCompression: !gzip,
			}}
			c, err := typesafe.NewClient(typesafe.WithTimeout(liveTimeout), typesafe.WithRoundTripper(probe))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			if _, err := c.Models().List(t.Context()); err != nil {
				t.Fatalf("Models().List() error = %v", err)
			}
			probe.mu.Lock()
			defer probe.mu.Unlock()
			for _, n := range probe.notes {
				t.Logf("K22 framing (gzip requested %t): %s", gzip, n)
			}
		})
	}
}
