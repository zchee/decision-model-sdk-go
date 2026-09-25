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
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"
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

// allocStateSize is the length of the whole-call state's JSON encoding: the
// plan's 1 KiB state (NF3), a string boxed in an any before the call.
const allocStateSize = 1 << 10

// newAllocState returns the 1 KiB boxed-string state.
func newAllocState() any { return strings.Repeat("s", allocStateSize-2) }

// series measures section testsupport.AllocRuns times, running setup (not
// measured) before each run, and returns the minimum that at least
// testsupport.AllocAgree runs share in both counters.
func series(t *testing.T, label string, setup, section func()) testsupport.Allocs {
	t.Helper()
	runs := make([]testsupport.Allocs, testsupport.AllocRuns)
	for i := range runs {
		if setup != nil {
			setup()
		}
		runs[i] = testsupport.Measure(section)
	}
	return testsupport.StableMin(t, label, runs)
}

// floorCall is the floor of a call: the transport called with a request
// built beforehand, its response drained into io.Discard and closed. No
// client can cost less.
func floorCall(rt http.RoundTripper, req *http.Request) error {
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

// TestAllocWholeCall checks AC-P6 (NF3): one whole SystemOne call of the q3
// shape (three questions, a 1 KiB boxed-string state, the discarding
// Recorder answering result.json, one attempt, no call options, no logger)
// makes at most N allocations of its own above the floor, where the floor
// is the Recorder's round trip of a request built beforehand plus E_sonic,
// sonic's own allocation for the state (frozen-budgets.md: N = 15
// provisional, target 12; the floor was 8 in W0.5). Counts are
// runtime.ReadMemStats deltas with a warm pool, the collector off and
// GOMAXPROCS 1, the minimum that three of five runs share (section 6.1.6).
// The CALL and ITEM lines are the ledger's rows; the ITEM line splits the
// request side into the allocations W0.5 accounted for (ruling R28).
func TestAllocWholeCall(t *testing.T) {
	testsupport.QuietRuntime(t)
	ctx := t.Context()
	qs := q3Questions(t)
	body := testsupport.Fixture(t, "result.json")
	rec := &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, body)}}
	c := newTestClient(t, rec)
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
	enc, err := encodeBody(state, c.cfg.model, qs, nil)
	check("encodeBody")
	pre := bytes.Clone(enc.Bytes())
	enc.Release()
	rd := bytes.NewReader(pre)
	floorReq := &http.Request{
		Method: http.MethodPost, URL: c.cfg.systemOneURL, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: c.cfg.systemOneHeader, Body: io.NopCloser(rd), ContentLength: int64(len(pre)), Host: c.cfg.systemOneURL.Host,
	}
	floorRT := series(t, "floor round trip", func() { rd.Reset(pre) }, func() { err = floorCall(rec, floorReq) })
	check("floor")
	total := series(t, "call/sdk", nil, func() { sinkResponse, err = c.SystemOne(ctx, state, qs) })
	check("call/sdk")

	floor := testsupport.Allocs{Mallocs: floorRT.Mallocs + esonic.Mallocs, Bytes: floorRT.Bytes + esonic.Bytes}
	if total.Mallocs < floor.Mallocs || total.Bytes < floor.Bytes {
		t.Fatalf("the call %s costs less than its floor %s", total, floor)
	}
	own := total.Mallocs - floor.Mallocs
	t.Logf("CALL q3 E_sonic=%s floorRT=%s floor=%s total=%s own=%d/%d (N = 15 provisional, target 12)", esonic, floorRT, floor, total, own, total.Bytes-floor.Bytes)

	items := measureCallItems(t, c, state, qs)
	t.Logf("ITEM q3 %s", items)

	// Exact pins (R70 (3)'s precedent), so a regression inside the budget
	// or a change of the floor fails loudly. The budget is N = 15,
	// provisional until W3.4 (frozen-budgets.md); the target is 12.
	if floor != (testsupport.Allocs{Mallocs: 8, Bytes: 640}) {
		t.Errorf("the floor of one call = %s, want 8/640 (the Recorder's round trip 7/624 and E_sonic 1/16)", floor)
	}
	if own != 14 {
		t.Errorf("SDK-own allocations of one call = %d, want exactly 14 (AC-P6, N = 15)", own)
	}
}

// callItems is one call split into the allocations of its request side and
// its response side.
type callItems struct {
	header, url, timeout, request, open, getBody, result, read, decode testsupport.Allocs
}

func (it callItems) String() string {
	return "header=" + it.header.String() + " url=" + it.url.String() + " WithTimeout=" + it.timeout.String() +
		" Request=" + it.request.String() + " body.Open=" + it.open.String() + " GetBody=" + it.getBody.String() +
		" *SystemOneResponse=" + it.result.String() + " readBody=" + it.read.String() + " decode=" + it.decode.String()
}

// measureCallItems measures the allocations a call makes, one at a time, in
// the order SystemOne makes them.
func measureCallItems(t *testing.T, c *Client, state any, qs *Prepared) callItems {
	t.Helper()
	ctx := t.Context()
	var hdr, u, to, rq, open, gb, res, rd, dec [testsupport.AllocRuns]testsupport.Allocs
	for i := range testsupport.AllocRuns {
		body, err := encodeBody(state, c.cfg.model, qs, nil)
		if err != nil {
			t.Fatal(err)
		}
		s := callSettings{header: c.cfg.systemOneHeader, timeout: c.cfg.timeout}
		rqs := request{method: http.MethodPost, url: c.cfg.systemOneURL, header: s.header, timeout: s.timeout, body: body}
		var (
			h       http.Header
			uc      *requestURL
			actx    context.Context
			cancel  context.CancelFunc
			reader  *codec.BodyReader
			getBody func() (io.ReadCloser, error)
			req     *http.Request
			resp    *SystemOneResponse
			raw     []byte
		)
		gb[i] = testsupport.Measure(func() { getBody = body.GetBody })
		rqs.getBody = getBody
		hdr[i] = testsupport.Measure(func() { h = rqs.attemptHeader(0) })
		u[i] = testsupport.Measure(func() { uc = &requestURL{u: *rqs.url} })
		to[i] = testsupport.Measure(func() { actx, cancel = context.WithTimeout(ctx, s.timeout) }) //nolint:gosec // G118: cancel runs at the end of the iteration.
		open[i] = testsupport.Measure(func() { reader, err = body.Open() })
		if err != nil {
			t.Fatal(err)
		}
		rq[i] = testsupport.Measure(func() {
			r := http.Request{Method: http.MethodPost, URL: &uc.u, Header: h, Body: reader, GetBody: getBody, ContentLength: int64(body.Len()), Host: uc.u.Host}
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
		res[i] = testsupport.Measure(func() { resp = new(SystemOneResponse) })
		resp.meta = wire.ResponseMeta{Status: hresp.StatusCode, Header: hresp.Header, Body: raw}
		dec[i] = testsupport.Measure(func() {
			err = decodeSystemOne(ctx, c.cfg.logger, &resp.meta, c.systemOneEndpoint, qs, c.cfg.model, &resp.res)
		})
		if err != nil {
			t.Fatal(err)
		}
		sinkResponse = resp
		cancel()
		body.Release()
	}
	return callItems{
		header:  testsupport.StableMin(t, "item header map", hdr[:]),
		url:     testsupport.StableMin(t, "item URL copy", u[:]),
		timeout: testsupport.StableMin(t, "item context.WithTimeout", to[:]),
		request: testsupport.StableMin(t, "item Request (WithContext)", rq[:]),
		open:    testsupport.StableMin(t, "item body.Open", open[:]),
		getBody: testsupport.StableMin(t, "item GetBody method value", gb[:]),
		result:  testsupport.StableMin(t, "item *SystemOneResponse", res[:]),
		read:    testsupport.StableMin(t, "item readBody", rd[:]),
		decode:  testsupport.StableMin(t, "item decode", dec[:]),
	}
}

// requestURL holds a URL copy on the heap, as a request's URL is.
type requestURL struct{ u url.URL }

// memCase is one AC-P5 reply and the outcome and bound it must meet.
type memCase struct {
	reply   testsupport.Reply
	outcome string // "ok", "eof" (io.ErrUnexpectedEOF) or "large" (*ResponseTooLargeError)
	bound   uint64 // the frozen TotalAlloc bound in bytes; 0 records only
}

// outcomeOf classifies a call's error for TestMemStatsCap.
func outcomeOf(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "eof"
	}
	if _, ok := errors.AsType[*ResponseTooLargeError](err); ok {
		return "large"
	}
	return err.Error()
}

// paddedResult returns a valid response body of exactly size bytes:
// result.json with an unknown member "pad" holding a string that fills the
// rest, which the decoder traverses and ignores.
func paddedResult(t *testing.T, size int) []byte {
	t.Helper()
	base := testsupport.FixtureString(t, "result.json")
	const open, closing = `{"pad":"`, `",`
	n := size - len(base) - len(open) - len(closing) + 1 // base's '{' is dropped
	if n < 0 {
		t.Fatalf("size %d is below the fixture's %d bytes", size, len(base))
	}
	b := make([]byte, 0, size)
	b = append(b, open...)
	b = append(b, bytes.Repeat([]byte{'x'}, n)...)
	b = append(b, closing...)
	b = append(b, base[1:]...)
	if len(b) != size {
		t.Fatalf("padded body is %d bytes, want %d", len(b), size)
	}
	return b
}

// TestMemStatsCap checks AC-P5 per attempt (frozen-budgets.md; rulings R26,
// R26b, R27): the TotalAlloc delta of one whole SystemOne call with
// Retry(NoRetry()) and the default 16 MiB cap, for (i) a body that declares
// 16 MiB and sends 10 bytes (≤ 256 KiB + 64 KiB), (ii) a declared 16 MiB + 1
// refused before a read (≤ 64 KiB), (iii) an undeclared 16 MiB + 1 refused
// at the byte past the cap and (iv), (v) a declared and an undeclared body
// of exactly 16 MiB, read and decoded (each ≤ 2 × cap + 64 KiB). result.json
// declared and undeclared, (vi) and (vii), are recorded (W5.2 bounds them).
// Before each measured call the heap is collected, outside the section,
// and one small call re-fills the pools the collection emptied, so the
// section pays only for its own call. The MEM lines are the ledger's rows.
func TestMemStatsCap(t *testing.T) {
	testsupport.QuietRuntime(t)
	ctx := t.Context()
	const limit = DefaultMaxResponseBytes
	const bigBound = 2*limit + 64<<10
	over := bytes.Repeat([]byte{' '}, limit+1)
	exact := paddedResult(t, limit)
	small := testsupport.Fixture(t, "result.json")
	qs := q3Questions(t)
	state := newAllocState()
	cases := map[string]memCase{
		"i-declared-16MiB-sent-10B":  {reply: testsupport.Reply{Body: []byte(`{"model":"`), ContentLength: limit}, outcome: "eof", bound: 256<<10 + 64<<10},
		"ii-declared-16MiB+1":        {reply: testsupport.Reply{Body: over}, outcome: "large", bound: 64 << 10},
		"iii-undeclared-16MiB+1":     {reply: testsupport.Reply{Body: over, ContentLength: -1}, outcome: "large", bound: bigBound},
		"iv-declared-16MiB":          {reply: testsupport.Reply{Body: exact}, outcome: "ok", bound: bigBound},
		"v-undeclared-16MiB":         {reply: testsupport.Reply{Body: exact, ContentLength: -1}, outcome: "ok", bound: bigBound},
		"vi-declared-result.json":    {reply: testsupport.Reply{Body: small}, outcome: "ok"},
		"vii-undeclared-result.json": {reply: testsupport.Reply{Body: small, ContentLength: -1}, outcome: "ok"},
	}
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
		call := series(t, name, func() { checkOutcome(); rewarm() }, func() { sinkResponse, err = c.SystemOne(ctx, state, qs, Retry(NoRetry())) })
		checkOutcome()
		sinkResponse = nil
		verdict := "recorded"
		if mc.bound > 0 {
			verdict = "bound " + strconv.FormatUint(mc.bound, 10) + " B"
			if call.Bytes > mc.bound {
				t.Errorf("%s: TotalAlloc delta %d B exceeds the frozen bound %d B (AC-P5)", name, call.Bytes, mc.bound)
			}
		}
		t.Logf("MEM %-27s outcome=%-5s call=%-18s callMiB=%.3f %s", name, outcomeOf(err), call, float64(call.Bytes)/(1<<20), verdict)
	}
}
