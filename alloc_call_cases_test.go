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

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// The whole-call cases of AC-P6 and AC-P5, built with and without -race:
// the budgets are TestAllocWholeCall and TestMemStatsCap (//go:build
// !race), and their functional halves are in alloc_functional_test.go.

// allocStateSize is the length of the whole-call state's JSON encoding: the
// plan's 1 KiB state (NF3), a string boxed in an any before the call.
const allocStateSize = 1 << 10

// newAllocState returns the 1 KiB boxed-string state.
func newAllocState() any { return strings.Repeat("s", allocStateSize-2) }

// floorRequest returns the floor's request of a call of c with state and
// qs (NF3; testsupport.FloorCall): the request the call's first attempt
// sends, built beforehand, over a rewindable copy of the body the call
// encodes, and the reader under it, which the caller rewinds before each
// round trip.
func floorRequest(t *testing.T, c *Client, state any, qs *Prepared) (*http.Request, *bytes.Reader) {
	t.Helper()
	enc, err := encodeBody(state, c.cfg.model, qs, nil)
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	pre := bytes.Clone(enc.Bytes())
	enc.Release()
	rd := bytes.NewReader(pre)
	return &http.Request{
		Method: http.MethodPost, URL: c.cfg.systemOneURL, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: c.cfg.systemOneHeader, Body: io.NopCloser(rd), ContentLength: int64(len(pre)), Host: c.cfg.systemOneURL.Host,
	}, rd
}

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

// memCases returns AC-P5's cases (frozen-budgets.md; rulings R26, R26b,
// R27) for the default 16 MiB cap: (i) a body that declares 16 MiB and
// sends 10 bytes, (ii) a declared 16 MiB + 1 refused before a read, (iii)
// an undeclared 16 MiB + 1 refused at the byte past the cap, (iv) and (v) a
// declared and an undeclared body of exactly 16 MiB, read and decoded, and
// result.json declared and undeclared, (vi) and (vii). The bounds of (i) to
// (v) are frozen: the first read buffer (256 KiB for a declared body) or
// twice the cap, plus 64 KiB. (vi) and (vii), a call that reads a small
// body, are bounded by W5.2 on the same rule: the first read buffer (the
// declared length + 1, or 4 KiB for an undeclared body, R27) plus the
// 64 KiB that (ii) allows a call that reads nothing.
func memCases(t *testing.T) map[string]memCase {
	t.Helper()
	const limit = DefaultMaxResponseBytes
	const bigBound = 2*limit + 64<<10
	over := bytes.Repeat([]byte{' '}, limit+1)
	exact := paddedResult(t, limit)
	small := testsupport.Fixture(t, "result.json")
	return map[string]memCase{
		"i-declared-16MiB-sent-10B":  {reply: testsupport.Reply{Body: []byte(`{"model":"`), ContentLength: limit}, outcome: "eof", bound: 256<<10 + 64<<10},
		"ii-declared-16MiB+1":        {reply: testsupport.Reply{Body: over}, outcome: "large", bound: 64 << 10},
		"iii-undeclared-16MiB+1":     {reply: testsupport.Reply{Body: over, ContentLength: -1}, outcome: "large", bound: bigBound},
		"iv-declared-16MiB":          {reply: testsupport.Reply{Body: exact}, outcome: "ok", bound: bigBound},
		"v-undeclared-16MiB":         {reply: testsupport.Reply{Body: exact, ContentLength: -1}, outcome: "ok", bound: bigBound},
		"vi-declared-result.json":    {reply: testsupport.Reply{Body: small}, outcome: "ok", bound: uint64(len(small)) + 1 + 64<<10},
		"vii-undeclared-result.json": {reply: testsupport.Reply{Body: small, ContentLength: -1}, outcome: "ok", bound: initialUndeclared + 64<<10},
	}
}
