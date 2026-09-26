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
// limitations under the License.

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

// FuzzDecodeResponse checks both response decoders on any body, within the
// per-input bound, as the Rust SDK's decode_response target reads every
// body with both. Its seed corpus is the fixtures and rows below, the Rust
// SDK's fuzz/corpus/decode_response byte for byte
// (testdata/fuzz/FuzzDecodeResponse), and the inputs campaigns found.
// Its seed corpus runs as a test in CI's -race test step (go test -race
// with coverage) and its non-race allocation-tests step (go test -count=1
// ./internal/codec/ ./internal/wire/ ./internal/testsupport/), on
// ubuntu-26.04, xcode-27 and windows-2025, and the fuzz job fuzzes it for
// 60 s on ubuntu-26.04.
//
// The models decoder never panics; it refuses with a *DecodeError or
// accepts only valid JSON of valid UTF-8, decodes the same twice, and its
// cards never alias the body.
//
// The System One decoder never
// panics; it refuses with a *DecodeError, or accepts only a body that is one
// valid JSON value of valid UTF-8 (encoding/json's json.Valid and
// utf8.Valid, which are weaker than the decoder's rules); an accepted body
// gives answers with unique labels and levels, structured levels that are
// valid JSON, and the same result on a second decode and when decoded
// against the question set it answers; the result never aliases the body;
// and, decoded as a System One and as a models response, the body gives the
// outcome of the whole-body traversal and decoder.Skip's trailing-data scan
// that the one-scan traversal replaced (K36, W5.3).
func FuzzDecodeResponse(f *testing.F) {
	for _, name := range testsupport.FixtureNames(f, "*.json") {
		if strings.HasPrefix(name, "structured-legend-flood-10k") {
			continue // 600 KB: too slow a seed for the mutator
		}
		f.Add(testsupport.Fixture(f, name))
	}
	for _, seed := range []string{
		`{"models":[{"name":"a","description":"b","release_date":"c"},{"name":"a\u0000","description":"","release_date":"","x":[{}]}],"models":[]}`,
		`{"model":"m","usage":{}}`,
		`{"model":"m","usage":{"input_tokens":null},"answers":{"a":{"type":"noul","noul":1}}}`,
		`{"model":"m","usage":{},"answers":{"s":{"type":"score","score":0,"confidence":1,"legend":{"0":{"v":1},"+1":"t","01":[2]},"probabilities":{"0":1,"1":0}}}}`,
		`{"model":"m","usage":{},"answers":{"c":{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":1,"a":"x","b":0}}}}`,
		`{"model":"m","usage":{},"answers":{"u":{"type":"aurora"},"c":7}}`,
		unescapeU(`{"model":"m","usage":{},"@U@0061nswers":{"s":{"type":"score","score":0,"confidence":1,"legend":{"@U@0030":{"k":"@U@d800"}},"probabilities":{}}}}`),
		// Root keys sonic fails to unquote (K36's one-scan traversal, W5.3).
		`{"\u"}`, `{"0000000000000000000000000000000\0}`,
		// A string value cut open where the root's brace was (the same;
		// sonic takes a string cut open by the end of its input as whole
		// when its content is a multiple of 32 bytes long).
		`{"":"` + strings.Repeat("0", 64) + `}`, `{"a":"` + strings.Repeat("0", 31) + `}}`,
		// Escapes next to the cut, and in root keys.
		`{"model":"m\"","usage":{},"\ud83d\ude00":{"a":"\\"}}`, `{"usage":{},"model":"m\\"}`, `{"usage":{},"model":"m\"}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		defer testsupport.BoundFuzzInput(t)()
		// The engine's input must stay unmodified (testing.F.Fuzz); the
		// alias checks overwrite a copy.
		body = bytes.Clone(body)
		checkModelsBody(t, body)
		orig := bytes.Clone(body)
		for _, models := range []bool{false, true} {
			if one, whole, _ := decodeBothScans(t, orig, models); !gocmp.Equal(whole, one) {
				t.Fatalf("decode of %q (models %t) on one scan differs from the whole-body decode (-whole +one):\n%s", orig, models, gocmp.Diff(whole, one))
			}
		}
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
		// The reference comes from a copy of the body that is never
		// written, so an aliasing result cannot move it along with res.
		var ref wire.SystemOneResult
		if _, err := DecodeSystemOne(orig, nil, "", &ref); err != nil {
			t.Fatalf("decode of a copy of %q: %v", orig, err)
		}
		for i := range body {
			body[i] = 0
		}
		if diff := gocmp.Diff(ref.Answers.Entries(), res.Answers.Entries()); diff != "" || res.Model != ref.Model {
			t.Fatalf("the result of %q changed with the body: model %q, answers (-reference +result):\n%s", orig, res.Model, diff)
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

// checkModelsBody checks DecodeModels on body, which it leaves unmodified.
func checkModelsBody(t *testing.T, body []byte) {
	t.Helper()
	work := bytes.Clone(body)
	var list wire.ModelList
	if err := DecodeModels(work, &list); err != nil {
		if _, ok := errors.AsType[*DecodeError](err); !ok {
			t.Fatalf("DecodeModels(%q) error = %T %v, want a *DecodeError", body, err, err)
		}
		return
	}
	if !json.Valid(body) || !utf8.Valid(body) {
		t.Fatalf("DecodeModels(%q) accepted a body that is not valid JSON of valid UTF-8", body)
	}
	var again wire.ModelList
	if err := DecodeModels(work, &again); err != nil {
		t.Fatalf("second models decode of %q: %v", body, err)
	}
	if diff := gocmp.Diff(list, again); diff != "" {
		t.Fatalf("second models decode of %q differs (-first +second):\n%s", body, diff)
	}
	// The reference comes from body, which is never written, so a list
	// that aliases work cannot move along with it.
	var ref wire.ModelList
	if err := DecodeModels(body, &ref); err != nil {
		t.Fatalf("models decode of a copy of %q: %v", body, err)
	}
	clear(work)
	if diff := gocmp.Diff(ref, list); diff != "" {
		t.Fatalf("the models of %q changed with the body (-reference +result):\n%s", body, diff)
	}
}
