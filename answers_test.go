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
	"iter"
	"net/http"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// upstreamResponse returns the upstream RESULT as the client decodes it.
func upstreamResponse(t *testing.T) *SystemOneResponse {
	t.Helper()
	c := newTestClient(t, replying(http.StatusOK, testsupport.Fixture(t, "result.json")))
	resp, err := c.SystemOne(t.Context(), "x", q3Questions(t))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	return resp
}

// firstOf returns the first pair seq yields and how many pairs it yielded
// before the consumer stopped it, which a correct iterator makes 1.
func firstOf[K, V any](seq iter.Seq2[K, V]) (K, V, int) {
	var (
		k K
		v V
		n int
	)
	for kk, vv := range seq {
		k, v = kk, vv
		n++
		break
	}
	return k, v, n
}

// TestAnswerViews checks the views of the answers beyond the round trip:
// every iterator stops when its consumer does, a question the response
// does not answer, or answers with another kind, reports false, a label or
// level an answer does not list reports false, and the zero values are
// empty.
func TestAnswerViews(t *testing.T) {
	resp := upstreamResponse(t)
	answers := resp.Answers()
	tone, _ := answers.Choice("tone")
	quality, _ := answers.Score("quality")

	t.Run("success: iterators stop with their consumer", func(t *testing.T) {
		type first struct {
			Key any
			N   int
		}
		got := map[string]first{}
		k, _, n := firstOf(answers.All())
		got["All"] = first{k, n}
		k2, _, n := firstOf(answers.Nouls())
		got["Nouls"] = first{k2, n}
		k3, _, n := firstOf(answers.Choices())
		got["Choices"] = first{k3, n}
		k4, _, n := firstOf(answers.Scores())
		got["Scores"] = first{k4, n}
		l, _, n := firstOf(tone.Probabilities())
		got["Choice.Probabilities"] = first{l, n}
		lv, _, n := firstOf(quality.Probabilities())
		got["Score.Probabilities"] = first{lv, n}
		lg, _, n := firstOf(quality.Legend())
		got["Score.Legend"] = first{lg, n}
		want := map[string]first{
			"All": {"spam", 1}, "Nouls": {"spam", 1}, "Choices": {"tone", 1}, "Scores": {"quality", 1},
			"Choice.Probabilities": {"friendly", 1}, "Score.Probabilities": {uint32(0), 1}, "Score.Legend": {uint32(0), 1},
		}
		if diff := gocmp.Diff(want, got); diff != "" {
			t.Errorf("first pair and pairs yielded (-want +got):\n%s", diff)
		}
	})

	t.Run("success: lookups that find nothing", func(t *testing.T) {
		_, noQuestion := answers.Get("missing")
		_, wrongKindNoul := answers.Noul("tone")
		_, wrongKindChoice := answers.Choice("quality")
		_, wrongKindScore := answers.Score("spam")
		_, noNoul := answers.Noul("missing")
		_, noChoice := answers.Choice("missing")
		_, noScore := answers.Score("missing")
		_, noLabel := tone.Probability("neutral")
		_, noLevel := quality.Probability(7)
		_, noDescription := quality.Description(7)
		got := []bool{noQuestion, wrongKindNoul, wrongKindChoice, wrongKindScore, noNoul, noChoice, noScore, noLabel, noLevel, noDescription}
		if diff := gocmp.Diff(make([]bool, len(got)), got); diff != "" {
			t.Errorf("found flags (-want +got):\n%s", diff)
		}
		if p, ok := tone.Probability("hostile"); !ok || p != 0.1 {
			t.Errorf(`Probability("hostile") = %v, %t, want 0.1, true`, p, ok)
		}
		if d, ok := quality.Description(2); !ok || d.Text() != "great" {
			t.Errorf("Description(2) = %q, %t, want \"great\", true", d.Text(), ok)
		}
	})

	t.Run("success: zero values are empty", func(t *testing.T) {
		var zero Answers
		_, got := zero.Get("spam")
		_, _, all := firstOf(zero.All())
		_, _, nouls := firstOf(zero.Nouls())
		if zero.Len() != 0 || got || all != 0 || nouls != 0 {
			t.Errorf("zero Answers: Len %d, Get %t, All %d, Nouls %d", zero.Len(), got, all, nouls)
		}
		var a Answer
		_, isNoul := a.Noul()
		_, isChoice := a.Choice()
		_, isScore := a.Score()
		if a.Kind() != 0 || a.Kind().String() != "unknown" || isNoul || isChoice || isScore {
			t.Errorf("zero Answer: kind %v, noul %t, choice %t, score %t", a.Kind(), isNoul, isChoice, isScore)
		}
	})

	t.Run("success: kinds are named as on the wire", func(t *testing.T) {
		got := []string{KindNoul.String(), KindChoice.String(), KindScore.String(), AnswerKind(9).String()}
		if diff := gocmp.Diff([]string{"noul", "choice", "score", "unknown"}, got); diff != "" {
			t.Errorf("kind names (-want +got):\n%s", diff)
		}
	})
}

// TestAttemptHeader checks the header of each attempt (R27, R28): the first
// attempt sends the call's template itself, without X-TypeSafe-Retry-Count;
// a retry sends a fresh map that shares the template's values and adds the
// count of attempts before it, from the static table and past it; the
// template is never written.
func TestAttemptHeader(t *testing.T) {
	c := newTestClient(t, replying(http.StatusOK, nil))
	tmpl := c.cfg.systemOneHeader
	before := tmpl.Clone()
	rq := request{header: tmpl}
	first := rq.attemptHeader(0)
	if _, ok := first[canonicalRetryCount]; ok {
		t.Errorf("the first attempt carries %s", headerRetryCount)
	}
	first["X-Probe"] = []string{"shared"}
	if tmpl.Get("X-Probe") != "shared" {
		t.Error("the first attempt's header is not the template itself")
	}
	delete(tmpl, "X-Probe")

	var counts []string
	for _, attempt := range []int{1, 2, 16, 17, 100} {
		h := rq.attemptHeader(attempt)
		counts = append(counts, h.Get(headerRetryCount))
		if len(h) != len(tmpl)+1 || h.Get("Authorization") != tmpl.Get("Authorization") {
			t.Errorf("attempt %d header %v, want the template plus the retry count", attempt, h)
		}
		if &h["Authorization"][0] != &tmpl["Authorization"][0] {
			t.Errorf("attempt %d copies the template's values instead of sharing them", attempt)
		}
	}
	if diff := gocmp.Diff([]string{"1", "2", "16", "17", "100"}, counts); diff != "" {
		t.Errorf("retry counts (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff(before, tmpl); diff != "" {
		t.Errorf("the template changed (-want +got):\n%s", diff)
	}
}
