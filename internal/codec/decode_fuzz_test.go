//go:build !go1.28 && (amd64 || arm64)

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

package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// FuzzDecodeResponse checks the System One decoder on any body: it never
// panics; it refuses with a *DecodeError, or accepts only a body that is one
// valid JSON value of valid UTF-8 (encoding/json's json.Valid and
// utf8.Valid, which are weaker than the decoder's rules); an accepted body
// gives answers with unique labels and levels, structured levels that are
// valid JSON, and the same result on a second decode and when decoded
// against the question set it answers; and the result never aliases the
// body.
func FuzzDecodeResponse(f *testing.F) {
	for _, name := range testsupport.FixtureNames(f, "*.json") {
		if strings.HasPrefix(name, "structured-legend-flood-10k") {
			continue // 600 KB: too slow a seed for the mutator
		}
		f.Add(testsupport.Fixture(f, name))
	}
	for _, seed := range []string{
		`{"model":"m","usage":{}}`,
		`{"model":"m","usage":{"input_tokens":null},"answers":{"a":{"type":"noul","noul":1}}}`,
		`{"model":"m","usage":{},"answers":{"s":{"type":"score","score":0,"confidence":1,"legend":{"0":{"v":1},"+1":"t","01":[2]},"probabilities":{"0":1,"1":0}}}}`,
		`{"model":"m","usage":{},"answers":{"c":{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":1,"a":"x","b":0}}}}`,
		`{"model":"m","usage":{},"answers":{"u":{"type":"aurora"},"c":7}}`,
		unescapeU(`{"model":"m","usage":{},"@U@0061nswers":{"s":{"type":"score","score":0,"confidence":1,"legend":{"@U@0030":{"k":"@U@d800"}},"probabilities":{}}}}`),
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		orig := bytes.Clone(body)
		var res wire.SystemOneResult
		skipped, err := DecodeSystemOne(body, nil, "", &res)
		if skipped.Count < len(skipped.Named()) || len(skipped.Named()) > MaxSkipped {
			t.Fatalf("skipped = %+v", skipped)
		}
		if err != nil {
			if _, ok := errors.AsType[*DecodeError](err); !ok {
				t.Fatalf("DecodeSystemOne(%q) error = %T %v, want a *DecodeError", body, err, err)
			}
			return
		}
		if !json.Valid(body) || !utf8.Valid(body) {
			t.Fatalf("DecodeSystemOne(%q) accepted a body that is not valid JSON of valid UTF-8", body)
		}
		checkAnswers(t, &res)
		var again wire.SystemOneResult
		if _, err := DecodeSystemOne(body, nil, "", &again); err != nil {
			t.Fatalf("second decode of %q: %v", body, err)
		}
		if diff := gocmp.Diff(res.Answers.Entries(), again.Answers.Entries()); diff != "" || res.Model != again.Model || res.Usage != again.Usage {
			t.Fatalf("second decode of %q differs (-first +second):\n%s", body, diff)
		}
		var interned wire.SystemOneResult
		if _, err := DecodeSystemOne(body, questionsFor(t, &res), res.Model, &interned); err != nil {
			t.Fatalf("decode of %q against its questions: %v", body, err)
		}
		if diff := gocmp.Diff(res.Answers.Entries(), interned.Answers.Entries()); diff != "" {
			t.Fatalf("decode of %q against its questions differs (-without +with):\n%s", body, diff)
		}
		for i := range body {
			body[i] = 0
		}
		if diff := gocmp.Diff(again.Answers.Entries(), res.Answers.Entries()); diff != "" {
			t.Fatalf("the result of %q changed with the body (-before +after):\n%s", orig, diff)
		}
	})
}

// checkAnswers checks the invariants of a decoded answer set.
func checkAnswers(t *testing.T, res *wire.SystemOneResult) {
	t.Helper()
	for _, e := range res.Answers.Entries() {
		switch e.Answer.Kind {
		case wire.KindNoul:
		case wire.KindChoice:
			seen := map[string]bool{}
			for _, p := range e.Answer.Choice.Probabilities {
				if seen[p.Label] {
					t.Fatalf("answer %q repeats label %q", e.Name, p.Label)
				}
				seen[p.Label] = true
			}
		case wire.KindScore:
			seen := map[uint32]bool{}
			for _, l := range e.Answer.Score.Legend {
				if seen[l.Level] {
					t.Fatalf("answer %q repeats legend level %d", e.Name, l.Level)
				}
				seen[l.Level] = true
				if l.Description.JSON != nil && !json.Valid(l.Description.JSON) {
					t.Fatalf("answer %q level %d is not valid JSON: %q", e.Name, l.Level, l.Description.JSON)
				}
			}
			clear(seen)
			for _, p := range e.Answer.Score.Probabilities {
				if seen[p.Level] {
					t.Fatalf("answer %q repeats probability level %d", e.Name, p.Level)
				}
				seen[p.Level] = true
			}
		default:
			t.Fatalf("answer %q has kind %v", e.Name, e.Answer.Kind)
		}
	}
}
