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

//go:build !race

package typesafe

import (
	"context"
	"io"
	"maps"
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// Sinks keep measured results reachable, as a caller's would be.
var (
	sinkResponse *SystemOneResponse
	sinkRequest  *http.Request
)

// series measures section testsupport.AllocRuns times, running setup (not
// measured) before each run, and returns the minimum that at least
// testsupport.AllocAgree runs share in both counters.
func series(t *testing.T, label string, setup, section func()) testsupport.Allocs {
	t.Helper()
	return testsupport.StableMin(t, label, measureRuns(setup, section))
}

// measureRuns measures section testsupport.AllocRuns times, running setup
// (not measured) before each run, and returns every run.
func measureRuns(setup, section func()) []testsupport.Allocs {
	runs := make([]testsupport.Allocs, testsupport.AllocRuns)
	for i := range runs {
		if setup != nil {
			setup()
		}
		runs[i] = testsupport.Measure(section)
	}
	return runs
}

// TestAllocWholeCall checks AC-P6 (NF3): one whole SystemOne call of the q3
// shape (three questions, a 1 KiB boxed-string state, the discarding
// Recorder answering result.json, DefaultRetry in force and its first
// attempt succeeding, no call options, the default logger) makes at most N
// allocations of its own above the floor, where the floor is the
// Recorder's round trip of a request built beforehand plus E_sonic, sonic's
// own allocation for the state (frozen-budgets.md: N = 12, frozen at W5.3
// after W3.4's 14; the floor is 8/640, as in W0.5). Counts are runtime.ReadMemStats deltas
// with a warm pool, the collector off and GOMAXPROCS 1, the minimum that
// three of five runs share (section 6.1.6). The CALL and ITEM lines are the
// ledger's rows; the ITEM line splits the call into the allocations
// frozen-budgets.md lists as N's composition (ruling R28's format).
func TestAllocWholeCall(t *testing.T) {
	testsupport.QuietRuntime(t)
	ctx := t.Context()
	qs := q3Questions(t)
	body := testsupport.Fixture(t, "result.json")
	rec := &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, body)}}
	// The production policy, not newTestClient's single attempt: AC-P6 is
	// measured with DefaultRetry in force (ruling R88b), whose first attempt
	// that succeeds must allocate nothing more.
	c := newTestClient(t, rec, WithRetry(DefaultRetry()))
	state := newAllocState()
	for range 2 { // warm the pools, the encoder and the decoder
		if _, err := c.SystemOne(ctx, state, qs); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	check := func(label string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
	}

	// The floor: E_sonic, and the Recorder's round trip of a prebuilt
	// request over a rewindable copy of the body a call sends.
	buf := make([]byte, 0, 4<<10)
	esonic := series(t, "E_sonic", nil, func() {
		buf = buf[:0]
		err = codec.EncodeState(&buf, state)
	})
	check("E_sonic")
	floorReq, rd := floorRequest(t, c, state, qs)
	floorRT := series(t, "floor round trip", func() { _, _ = rd.Seek(0, io.SeekStart) }, func() { err = testsupport.FloorCall(rec, floorReq) })
	check("floor")
	total := series(t, "call/sdk", nil, func() { sinkResponse, err = c.SystemOne(ctx, state, qs) })
	check("call/sdk")

	floor := testsupport.Allocs{Mallocs: floorRT.Mallocs + esonic.Mallocs, Bytes: floorRT.Bytes + esonic.Bytes}
	if total.Mallocs < floor.Mallocs || total.Bytes < floor.Bytes {
		t.Fatalf("the call %s costs less than its floor %s", total, floor)
	}
	own := total.Mallocs - floor.Mallocs
	t.Logf("CALL q3 E_sonic=%s floorRT=%s floor=%s total=%s own=%d/%d (N = 12, frozen at W5.3)", esonic, floorRT, floor, total, own, total.Bytes-floor.Bytes)

	items := measureCallItems(t, c, state, qs, "")
	t.Logf("ITEM q3 %s", items)

	// Exact pins (R70 (3)'s precedent), so a change of the floor fails
	// loudly. The frozen ceiling is N = 12 (W5.3, frozen-budgets.md; 14 at
	// W3.4), and the pin is the ceiling itself (R104): an allocation added
	// to the call fails here, and one removed moves the pin and the frozen
	// row together.
	if floor != (testsupport.Allocs{Mallocs: 8, Bytes: 640}) {
		t.Errorf("the floor of one call = %s, want 8/640 (the Recorder's round trip 7/624 and E_sonic 1/16)", floor)
	}
	if own != 12 {
		t.Errorf("SDK-own allocations of one call = %d, want exactly 12 (AC-P6: N = 12, frozen at W5.3)", own)
	}

	// q20, recorded (frozen-budgets.md AC-P6, "Recorded, not in N"; W3.4
	// measured it with a probe): the same call asking the twenty questions
	// result-20.json answers, over the Recorder answering it.
	_, first20, err := decodeFixture(t, "result-20.json")
	check("result-20.json")
	qs20 := questionsFor(t, &first20)
	rec20 := &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, testsupport.Fixture(t, "result-20.json"))}}
	c20 := newTestClient(t, rec20, WithRetry(DefaultRetry()))
	for range 2 {
		_, err = c20.SystemOne(ctx, state, qs20)
		check("q20 warm call")
	}
	floorReq20, rd20 := floorRequest(t, c20, state, qs20)
	floorRT20 := series(t, "q20 floor round trip", func() { _, _ = rd20.Seek(0, io.SeekStart) }, func() { err = testsupport.FloorCall(rec20, floorReq20) })
	check("q20 floor")
	total20 := series(t, "q20 call/sdk", nil, func() { sinkResponse, err = c20.SystemOne(ctx, state, qs20) })
	check("q20 call/sdk")
	floor20 := testsupport.Allocs{Mallocs: floorRT20.Mallocs + esonic.Mallocs, Bytes: floorRT20.Bytes + esonic.Bytes}
	if total20.Mallocs < floor20.Mallocs || total20.Bytes < floor20.Bytes {
		t.Fatalf("the q20 call %s costs less than its floor %s", total20, floor20)
	}
	t.Logf("CALL q20 E_sonic=%s floorRT=%s floor=%s total=%s own=%d/%d (recorded, not in N)", esonic, floorRT20, floor20, total20, total20.Mallocs-floor20.Mallocs, total20.Bytes-floor20.Bytes)
	t.Logf("ITEM q20 %s", measureCallItems(t, c20, state, qs20, "q20 "))
}

// callItems is one call split into the allocations of its request side and
// its response side.
type callItems struct {
	header, call, timeout, request, open, getBody, read, decode testsupport.Allocs
}

func (it callItems) String() string {
	return "header=" + it.header.String() + " systemOneAlloc=" + it.call.String() + " WithTimeout=" + it.timeout.String() +
		" Request=" + it.request.String() + " body.Open=" + it.open.String() + " GetBody=" + it.getBody.String() +
		" readBody=" + it.read.String() + " decode=" + it.decode.String()
}

// measureCallItems measures the allocations a call makes, one at a time, in
// the order SystemOne makes them; prefix starts the label of every series.
func measureCallItems(t *testing.T, c *Client, state any, qs *Prepared, prefix string) callItems {
	t.Helper()
	ctx := t.Context()
	var hdr, ca, to, rq, open, gb, rd, dec [testsupport.AllocRuns]testsupport.Allocs
	for i := range testsupport.AllocRuns {
		body, err := encodeBody(state, c.cfg.model, qs, nil)
		if err != nil {
			t.Fatal(err)
		}
		s := callSettings{header: c.cfg.systemOneHeader, timeout: c.cfg.timeout}
		rqs := request{method: http.MethodPost, url: c.cfg.systemOneURL, header: s.header, timeout: s.timeout, body: body}
		var (
			h       http.Header
			call    *systemOneAlloc
			actx    context.Context
			cancel  context.CancelFunc
			reader  *codec.BodyReader
			getBody func() (io.ReadCloser, error)
			req     *http.Request
			resp    *SystemOneResponse
			raw     []byte
			spare   []wire.AnswerEntry
		)
		ca[i] = testsupport.Measure(func() { call, spare = newSystemOneAlloc(qs.Len()) })
		gb[i] = testsupport.Measure(func() { getBody = body.GetBody })
		rqs.getBody = getBody
		hdr[i] = testsupport.Measure(func() { h = rqs.attemptHeader(0) })
		call.url = *rqs.url                                                                        // the first attempt's copy, in the call's allocation
		to[i] = testsupport.Measure(func() { actx, cancel = context.WithTimeout(ctx, s.timeout) }) //nolint:gosec // G118: cancel runs at the end of the iteration.
		open[i] = testsupport.Measure(func() { reader, err = body.Open() })
		if err != nil {
			t.Fatal(err)
		}
		rq[i] = testsupport.Measure(func() {
			r := http.Request{Method: http.MethodPost, URL: &call.url, Header: h, Body: reader, GetBody: getBody, ContentLength: int64(body.Len()), Host: call.url.Host}
			req = r.WithContext(actx)
		})
		sinkRequest = req
		hresp, err := c.cfg.transport.roundTrip(req, s.timeout)
		if err != nil {
			t.Fatal(err)
		}
		rd[i] = testsupport.Measure(func() { raw, err = readBody(hresp.Body, hresp.ContentLength, c.cfg.maxResponseBytes) })
		if err != nil {
			t.Fatal(err)
		}
		_ = hresp.Body.Close()
		resp = &call.resp
		resp.meta = wire.ResponseMeta{Status: hresp.StatusCode, Header: hresp.Header, Body: raw}
		dec[i] = testsupport.Measure(func() {
			err = decodeSystemOneInto(ctx, c.cfg.logger, &resp.meta, c.systemOneEndpoint, c.cfg.redactor(), qs, c.cfg.model, &resp.res, spare)
		})
		if err != nil {
			t.Fatal(err)
		}
		sinkResponse = resp
		cancel()
		body.Release()
	}
	return callItems{
		header:  testsupport.StableMin(t, prefix+"item header map", hdr[:]),
		call:    testsupport.StableMin(t, prefix+"item systemOneAlloc (the response, the first URL copy and up to four answer entries)", ca[:]),
		timeout: testsupport.StableMin(t, prefix+"item context.WithTimeout", to[:]),
		request: testsupport.StableMin(t, prefix+"item Request (WithContext)", rq[:]),
		open:    testsupport.StableMin(t, prefix+"item body.Open", open[:]),
		getBody: testsupport.StableMin(t, prefix+"item GetBody method value", gb[:]),
		read:    testsupport.StableMin(t, prefix+"item readBody", rd[:]),
		decode:  testsupport.StableMin(t, prefix+"item decode", dec[:]),
	}
}

// TestMemStatsCap checks AC-P5 per attempt (frozen-budgets.md; rulings R26,
// R26b, R27): the TotalAlloc delta of one whole SystemOne call with
// Retry(NoRetry()) and the default 16 MiB cap, for each case of memCases:
// (i) a body that declares 16 MiB and sends 10 bytes (≤ 256 KiB + 64 KiB),
// (ii) a declared 16 MiB + 1 refused before a read (≤ 64 KiB), (iii) an
// undeclared 16 MiB + 1 refused at the byte past the cap and (iv), (v) a
// declared and an undeclared body of exactly 16 MiB, read and decoded (each
// ≤ 2 × cap + 64 KiB), and result.json declared and undeclared, (vi) and
// (vii), each within its first read buffer + 64 KiB (W5.2's bounds, on the
// frozen cases' rule). Before each measured call the heap is collected,
// outside the section, and one small call re-fills the pools the collection
// emptied, so the section pays only for its own call.
//
// AC-P5 is a set of bounds (R26), and every one of the
// testsupport.AllocRuns runs of a case is checked against its bound. The
// runs are not asked to agree, as the exact pins of AC-P1, AC-P2 and AC-P6
// are (testsupport.StableMin, three of five): identical calls can differ by
// a few allocations for reasons outside the SDK (ruling K32). The runtime
// builds a type assertion's or a type switch's cache on about one lookup
// in 1024 that misses it, at random, and (i)'s error path makes many such
// lookups (errors.Is and errors.As on the chain, fmt printing the cause), so
// one run in a hundred or so costs 1 or 2 allocations and 48 to 112 B more;
// and (i)'s first run pays for fmt's pooled printer, which the collections
// of the cases before it emptied and the small success call does not
// refill (4 allocations, 288 B). The MEM lines record each case's minimum,
// its own cost and the ledger's row, and the maximum of its runs
// (testsupport.Spread). The functional half, each case's outcome under the
// race detector too, is TestMemStatsCapFunctional; the cap over a real
// HTTP/2 connection is TestResponseCapOverTheWire.
func TestMemStatsCap(t *testing.T) {
	testsupport.QuietRuntime(t)
	ctx := t.Context()
	small := testsupport.Fixture(t, "result.json")
	qs := q3Questions(t)
	state := newAllocState()
	cases := memCases(t)
	warm := newTestClient(t, &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, small)}})
	rewarm := func() {
		runtime.GC()
		if _, err := warm.SystemOne(ctx, state, qs); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(cases)) {
		mc := cases[name]
		c := newTestClient(t, &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{mc.reply}})
		var err error
		ran := false
		// checkOutcome checks the previous run's outcome, from the next
		// run's setup and after the last run, outside every section.
		checkOutcome := func() {
			if ran {
				if got := outcomeOf(err); got != mc.outcome {
					t.Fatalf("%s: outcome %q, want %q", name, got, mc.outcome)
				}
			}
			ran = true
		}
		runs := measureRuns(func() { checkOutcome(); rewarm() }, func() { sinkResponse, err = c.SystemOne(ctx, state, qs, Retry(NoRetry())) })
		checkOutcome()
		sinkResponse = nil
		call, most := testsupport.Spread(t, name, runs)
		verdict := "recorded"
		if mc.bound > 0 {
			verdict = "bound " + strconv.FormatUint(mc.bound, 10) + " B"
			for i, run := range runs {
				if run.Bytes > mc.bound {
					t.Errorf("%s: run %d: TotalAlloc delta %d B exceeds the frozen bound %d B (AC-P5)", name, i+1, run.Bytes, mc.bound)
				}
			}
		}
		t.Logf("MEM %-27s outcome=%-5s call=%-18s callMiB=%.3f max=%-18s spread=+%d/+%d %s", name, outcomeOf(err), call, float64(call.Bytes)/(1<<20), most, most.Mallocs-call.Mallocs, most.Bytes-call.Bytes, verdict)
	}
}
