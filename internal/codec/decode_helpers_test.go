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
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// wantReject maps every fixture the decoder must refuse to the field path the
// Go column of testdata/README.md names ("." for the JSON layer). Every
// malformed-*.json file must have a row (TestDecodeFixtures checks).
var wantReject = map[string]string{
	"malformed-empty.json":              ".",
	"malformed-whitespace.json":         ".",
	"malformed-truncated.json":          ".",
	"malformed-trailing-garbage.json":   ".",
	"malformed-trailing-value.json":     ".",
	"malformed-trailing-nbsp.json":      ".",
	"malformed-trailing-formfeed.json":  ".",
	"malformed-root-array.json":         ".",
	"malformed-invalid-utf8.json":       ".",
	"malformed-control-char.json":       ".",
	"malformed-control-char-key.json":   ".",
	"malformed-invalid-utf8-key.json":   ".",
	"malformed-invalid-escape.json":     ".",
	"malformed-bad-literal.json":        ".",
	"malformed-double-comma.json":       ".",
	"malformed-leading-zero.json":       ".",
	"malformed-trailing-comma.json":     ".",
	"malformed-big-exp.json":            "usage.input_tokens",
	"malformed-usage-type.json":         "usage.input_tokens",
	"malformed-missing-model.json":      "model",
	"malformed-missing-usage.json":      "usage",
	"malformed-answers-not-object.json": "answers",
	"deviation-big-exp-noul.json":       "answers.spam.noul",
	"malformed-too-deep.json":           ".",
	"deviation-nan-unknown.json":        ".",
	"deviation-nan-noul.json":           ".",
}

// decodeBody decodes body as a System One response against q and model, on
// a decoder of its own so that the test can read its stats.
func decodeBody(tb testing.TB, body []byte, q *wire.Prepared, model string) (*wire.SystemOneResult, Skipped, stats, error) {
	tb.Helper()
	d := newDecoder()
	res := new(wire.SystemOneResult)
	skipped, err := d.systemOne(body, q, model, res)
	st := d.stats
	d.release()
	return res, skipped, st, err
}

// pathOf returns the field path of a *DecodeError, or fails the test.
func pathOf(tb testing.TB, err error) string {
	tb.Helper()
	var de *DecodeError
	if !errors.As(err, &de) {
		tb.Fatalf("error %v (%T) is not a *DecodeError", err, err)
	}
	return de.Path.String()
}

// questionsFor returns the question set that a response like res answers:
// each answer's name and kind, a choice's options in the order of its
// probabilities, and a score's levels from its legend (level i's
// description at index i, and the text "" for a level the legend leaves
// out). Every string is a copy, so a decode against the set cannot borrow
// res's strings.
func questionsFor(tb testing.TB, res *wire.SystemOneResult) *wire.Prepared {
	tb.Helper()
	var b wire.Builder
	for _, e := range res.Answers.Entries() {
		name := strings.Clone(e.Name)
		var err error
		switch e.Answer.Kind {
		case wire.KindNoul:
			err = b.Noul(name, nil, nil, nil)
		case wire.KindChoice:
			labels := make([]string, len(e.Answer.Choice.Probabilities))
			for i, p := range e.Answer.Choice.Probabilities {
				labels[i] = strings.Clone(p.Label)
			}
			err = b.Choice(name, nil, labels, func(int) *wire.Content { return nil })
		case wire.KindScore:
			var levels []wire.Content
			for _, l := range e.Answer.Score.Legend {
				for len(levels) <= int(l.Level) {
					levels = append(levels, wire.Content{})
				}
				levels[l.Level] = wire.Content{Text: strings.Clone(l.Description.Text), JSON: slices.Clone(l.Description.JSON)}
			}
			err = b.Score(name, nil, levels)
		}
		if err != nil {
			tb.Fatalf("question %q: %v", name, err)
		}
	}
	p := new(wire.Prepared)
	if err := b.Finish(p); err != nil {
		tb.Fatal(err)
	}
	return p
}

// names returns the answer names in order.
func names(res *wire.SystemOneResult) []string {
	var out []string
	for _, e := range res.Answers.Entries() {
		out = append(out, e.Name)
	}
	return out
}
