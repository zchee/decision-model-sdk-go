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
	"net/http"
	"strconv"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// The functional halves of the allocation tests. Every allocation budget is
// built without -race (//go:build !race: under the race detector
// sync.Pool.Put drops one value in four, so a warm pool is not warm); what
// the same calls must do, whatever they allocate, is checked here, in both
// builds, over the budget tests' own cases. Each test is named after its
// budget test with the suffix Functional.

// TestAllocEncodeFunctional is the functional half of TestAllocEncode: for
// every state kind at every AC-P1 size, the request body is
// {"state":<state>,"model":"jev-latest","questions":<prepared>}, where the
// state member is the state's encoding by encoding/json, an encoder
// independent of the SDK's (byte for byte; a map's members come out in each
// encoder's own order, so a map state is compared as a JSON value), and the
// scratch the pool holds after the body is released is within the 8 MiB
// ceiling (AC-P1: scratch ≤ 8 MiB).
func TestAllocEncodeFunctional(t *testing.T) {
	qs := encodeQuestions(t)
	prefix := []byte(`{"state":`)
	suffix := []byte(`,"model":` + strconv.Quote(DefaultModel) + `,"questions":` + string(qs.w.Questions) + `}`)
	for _, k := range stateKinds {
		for _, size := range allocSizes {
			t.Run(k.name+"/"+sizeName(size), func(t *testing.T) {
				sc := stateFor(t, k, size)
				body, err := sc.pass(qs)
				if err != nil {
					t.Fatalf("encodeBody: %v", err)
				}
				got := bytes.Clone(body.Bytes())
				body.Release()

				state, ok := bytes.CutPrefix(got, prefix)
				if ok {
					state, ok = bytes.CutSuffix(state, suffix)
				}
				if !ok {
					t.Fatalf("the body does not have the shape %s<state>%s; its first and last bytes: %q ... %q", prefix, suffix, got[:min(len(got), 64)], got[max(len(got)-64, 0):])
				}
				want, err := testsupport.StdlibMarshal(sc.boxed)
				if err != nil {
					t.Fatalf("encoding/json: %v", err)
				}
				if sc.maps > 0 {
					// encoding/json writes a map's members sorted by key, so
					// its encoding of the decoded member is canonical.
					var value any
					if err := testsupport.StdlibUnmarshal(state, &value); err != nil {
						t.Fatalf("the state member is not JSON: %v", err)
					}
					canonical, err := testsupport.StdlibMarshal(value)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(canonical, want) {
						t.Errorf("the state member (%d B) is not the state's JSON value: sorted, it differs from encoding/json's encoding (%d B) at byte %d", len(state), len(want), firstDiff(canonical, want))
					}
				} else if !bytes.Equal(state, want) {
					t.Errorf("the state member (%d B) differs from encoding/json's encoding (%d B) at byte %d", len(state), len(want), firstDiff(state, want))
				}

				// The pool keeps the scratch the body left only within the
				// ceiling: the next body starts with at most that much.
				next := codec.NewBody()
				kept := cap(*next.Buffer())
				next.Release()
				if kept > codec.ScratchCeiling {
					t.Errorf("the pool handed out a scratch of %d B, over the ceiling %d B", kept, codec.ScratchCeiling)
				}
			})
		}
	}
}

// TestAllocScratchSequenceFunctional is the functional half of
// TestAllocScratchSequence: for each state kind of the sequence, the 32
// calls of section 6.1.6 on one client succeed, each request the Recorder
// saw carries GetBody and declares its length, and its body is the body of
// the same state encoded on its own, byte for byte (a map state, whose
// members come in a random order: the same length, and for the 1 KiB state
// the same JSON value), so a scratch reused across the sizes never leaks the
// bytes of a larger body into a smaller one; the scratch the pool hands out
// after any call is within the 8 MiB ceiling, and after call 22 (9 MiB) it
// is not the one the call grew, which the pool dropped. Under the race
// detector the pool also drops one value in four at random, so which other
// calls leave no scratch behind is the budget test's clause, not this one's.
func TestAllocScratchSequenceFunctional(t *testing.T) {
	qs := q3Questions(t)
	reply := testsupport.JSON(http.StatusOK, testsupport.Fixture(t, "result.json"))
	for _, sk := range sequenceKinds {
		t.Run(sk.name, func(t *testing.T) {
			k := stateKindNamed(t, sk.name)
			states, lens := sequenceStates(t, k, qs)
			var want [3][]byte
			for i, st := range states {
				body, err := encodeBody(st, DefaultModel, qs, nil)
				if err != nil {
					t.Fatal(err)
				}
				want[i] = bytes.Clone(body.Bytes())
				body.Release()
			}
			isMap := stateFor(t, k, sequenceSizes[0]).maps > 0
			rec := &testsupport.Recorder{Replies: []testsupport.Reply{reply}}
			c := newTestClient(t, rec)
			caps := sequenceProbe(t, c, qs, states)

			reqs := rec.Requests()
			if len(reqs) != sequenceCalls {
				t.Fatalf("the Recorder saw %d requests, want %d", len(reqs), sequenceCalls)
			}
			for i, r := range reqs {
				call, w := i+1, want[sequenceState(i+1)]
				if !r.HasGetBody || r.ContentLength != int64(len(r.Body)) {
					t.Errorf("call %d: GetBody %t, ContentLength %d for a %d B body", call, r.HasGetBody, r.ContentLength, len(r.Body))
				}
				switch {
				case len(r.Body) != len(w):
					t.Errorf("call %d: body of %d B, want %d B", call, len(r.Body), len(w))
				case isMap:
					if sequenceState(call) == 0 && !sameJSON(t, r.Body, w) {
						t.Errorf("call %d: the body is not the state's body as a JSON value", call)
					}
				case !bytes.Equal(r.Body, w):
					t.Errorf("call %d: the body differs from the state's body encoded on its own at byte %d", call, firstDiff(r.Body, w))
				}
			}
			for i, c := range caps {
				if c > codec.ScratchCeiling {
					t.Errorf("after call %d the pool handed out a scratch of %d B, over the ceiling %d B", i+1, c, codec.ScratchCeiling)
				}
			}
			if c := caps[sequenceNine-1]; c >= lens[2] {
				t.Errorf("after call %d the pool handed out a scratch of %d B, room for its %d B body: the scratch past the ceiling was kept", sequenceNine, c, lens[2])
			}
		})
	}
}

// sameJSON reports whether a and b hold the same JSON value, compared in
// encoding/json's canonical form (members sorted by key).
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	canonical := func(data []byte) []byte {
		var v any
		if err := testsupport.StdlibUnmarshal(data, &v); err != nil {
			t.Fatalf("not JSON: %v", err)
		}
		out, err := testsupport.StdlibMarshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	return bytes.Equal(canonical(a), canonical(b))
}

// firstDiff returns the index of the first byte where a and b differ, or
// the shorter length when one is a prefix of the other.
func firstDiff(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}
