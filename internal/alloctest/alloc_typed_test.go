//go:build !race

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

package alloctest

import (
	"errors"
	"net/http"
	"testing"

	typesafe "github.com/zchee/typesafe-sdk-go"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// sinkReview keeps a typed decode's result alive past the measured section.
var sinkReview reviewAnswers

// TestAllocTypedDecode checks the typed decode budget
// (docs/perf/frozen-budgets.md): DecodeAs of result.json's response
// allocates fewer times than the decode that fills the response's Answers(),
// measured in the same run. The Answers() decode is the one a call makes
// (the pooled decoder warm, a fresh result, the question set and model of
// the call, interned), here with the question set Ask sends for
// reviewAnswers. DecodeAs's count is pinned exactly, as the per-fixture
// decode counts are, so a change that moves it fails here: no allocation.
// The T being decoded stays on DecodeAs's stack, since the answers are
// stored at the fields' offsets (decodeas_store.go; through
// reflect.Value.Interface it would move to the heap, one allocation of
// 144 B), and the answers share the response's slices.
//
// It also measures what Ask adds to a call: Ask over the Recorder allocates
// exactly what SystemOne with the same question set and DecodeAs allocate
// together. The TYPED line is a ledger row.
func TestAllocTypedDecode(t *testing.T) {
	testsupport.QuietRuntime(t)
	ctx := t.Context()
	qs, err := typesafe.PreparedFor[reviewAnswers]()
	if err != nil {
		t.Fatal(err)
	}
	requireStoreLayout[reviewAnswers](t)
	body := testsupport.Fixture(t, "result.json")
	rec := &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, body)}}
	c := newTestClient(t, rec)
	var resp *typesafe.SystemOneResponse
	for range 2 { // warm the pools, the encoder, the decoder and the plan cache
		if resp, err = c.SystemOne(ctx, "x", qs); err != nil {
			t.Fatal(err)
		}
		if sinkReview, err = typesafe.DecodeAs[reviewAnswers](resp); err != nil {
			t.Fatal(err)
		}
		if sinkReview, err = typesafe.Ask[reviewAnswers](ctx, c, "x"); err != nil {
			t.Fatal(err)
		}
	}
	check := func(label string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
	}
	none := func() struct{} { return struct{}{} }

	answersDecode := testsupport.MeasureMin(t, "Answers() decode", func() *wire.SystemOneResult { return new(wire.SystemOneResult) }, func(res *wire.SystemOneResult) {
		_, err = codec.DecodeSystemOne(body, wireOf(qs), cfgOf(c).Model, res)
	})
	check("Answers() decode")
	typedDecode := testsupport.MeasureMin(t, "DecodeAs[reviewAnswers]", none, func(struct{}) {
		sinkReview, err = typesafe.DecodeAs[reviewAnswers](resp)
	})
	check("DecodeAs")
	call := testsupport.MeasureMin(t, "SystemOne", none, func(struct{}) {
		sinkResponse, err = c.SystemOne(ctx, "x", qs)
	})
	check("SystemOne")
	ask := testsupport.MeasureMin(t, "Ask[reviewAnswers]", none, func(struct{}) {
		sinkReview, err = typesafe.Ask[reviewAnswers](ctx, c, "x")
	})
	check("Ask")
	t.Logf("TYPED result bytes=%d answersDecode=%s decodeAs=%s systemOne=%s ask=%s (AC-P3: decodeAs < answersDecode; AC-P6: ask = systemOne + decodeAs)", len(body), answersDecode, typedDecode, call, ask)

	if typedDecode.Mallocs >= answersDecode.Mallocs {
		t.Errorf("DecodeAs allocations = %d, want fewer than the Answers() decode's %d (AC-P3)", typedDecode.Mallocs, answersDecode.Mallocs)
	}
	if want := (testsupport.Allocs{}); typedDecode != want {
		t.Errorf("DecodeAs allocations = %s, want exactly %s: the reviewAnswers value moved to the heap, or a change in DecodeAs allocates", typedDecode, want)
	}
	if ask.Mallocs != call.Mallocs+typedDecode.Mallocs {
		t.Errorf("Ask allocations = %d, want exactly SystemOne's %d plus DecodeAs's %d: Ask adds nothing to a call (AC-P6)", ask.Mallocs, call.Mallocs, typedDecode.Mallocs)
	}
}

// failToneAnswers is reviewAnswers's tone with options result.json's
// "friendly" is not one of, so DecodeAs of result.json fails on it.
type failToneAnswers struct {
	Tone typesafe.ChoiceAnswer `typesafe:"kind=choice;name=tone;instructions=Tone?;options=calm|hostile"`
}

// TestAllocTypedFailure pins what a typed failure costs: DecodeAs of
// result.json into failToneAnswers fails at "tone.choice", and its
// *ResponseValidationError renders that path once; rendering the decoder's
// form and then replacing it would cost more. The response is
// UnmarshalJSON's, whose Meta has no header, so the count is the error's
// alone.
func TestAllocTypedFailure(t *testing.T) {
	requireStoreLayout[failToneAnswers](t)
	var resp typesafe.SystemOneResponse
	if err := resp.UnmarshalJSON(testsupport.Fixture(t, "result.json")); err != nil {
		t.Fatal(err)
	}
	_, err := typesafe.DecodeAs[failToneAnswers](&resp)
	if ve, ok := errors.AsType[*typesafe.ResponseValidationError](err); !ok || ve.FieldPath != "tone.choice" {
		t.Fatalf("DecodeAs error = %v, want a *ResponseValidationError at tone.choice", err)
	}
	n := testing.AllocsPerRun(100, func() { _, err = typesafe.DecodeAs[failToneAnswers](&resp) })
	t.Logf("TYPED failure at tone.choice: %v allocations", n)
	if n != 5 {
		t.Errorf("a typed failure allocates %v times, want 5 (its path rendered once)", n)
	}
}
