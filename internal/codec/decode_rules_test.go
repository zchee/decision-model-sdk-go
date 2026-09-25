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
	"runtime"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// TestControlRule checks ruling R23: a raw control character anywhere inside
// a string or a key is refused at the root, one that came from an escape is
// accepted, and the body is scanned only when a delivered string holds a
// control character.
func TestControlRule(t *testing.T) {
	const head = `{"model":"m","usage":{"input_tokens":1,"output_tokens":1},"answers":{"s":{"type":"score","score":0,"confidence":1,"probabilities":{"0":1},"legend":{"0":`
	tests := map[string]struct {
		body      string
		accept    bool
		wantScans int
	}{
		"success: escaped newline in a text level":        {body: head + `"a\nb"}}}}`, accept: true, wantScans: 1},
		"success: escaped U+0001 in a text level":         {body: head + `"a@U@0001b"}}}}`, accept: true, wantScans: 1},
		"success: escaped U+0000 in a key":                {body: head + `"ab"}}},"x@U@0000":1}`, accept: true, wantScans: 1},
		"success: raw LF between tokens, escape in value": {body: head + "\n\"a\\nb\"}}}}\n", accept: true, wantScans: 1},
		"success: plain text, no scan":                    {body: head + `"ab"}}}}`, accept: true, wantScans: 0},
		"error: raw U+0001 in a text level":               {body: head + "\"a\x01b\"}}}}"},
		"error: raw TAB in a text level":                  {body: head + "\"a\tb\"}}}}"},
		"error: raw U+001F in a key":                      {body: head + "\"ab\"}}},\"k\x1f\":1}}"},
		"error: escape and raw U+0001 in one string":      {body: head + "\"\\n\x01\"}}}}"},
		"error: raw U+001F in an unknown member's key":    {body: `{"model":"m","usage":{},"meta":{"k` + "\x1f" + `":1},"answers":{}}`},
		"error: raw U+0000 inside a structured level":     {body: head + "{\"x\":\"\x00\"}}}}}"},
		"error: invalid UTF-8 in an escaped string":       {body: head + "\"\\n\xff\"}}}}"},
		"error: invalid UTF-8 in an unknown member":       {body: `{"model":"m","usage":{},"meta":"` + "\xc3" + `","answers":{}}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, st, err := decodeBody(t, []byte(unescapeU(tt.body)), nil, "")
			if tt.accept != (err == nil) {
				t.Fatalf("accept = %v (err %v), want %v", err == nil, err, tt.accept)
			}
			if err != nil {
				if got := pathOf(t, err); got != "." {
					t.Errorf("path = %q, want %q", got, ".")
				}
				return
			}
			if st.scans != tt.wantScans {
				t.Errorf("body scans = %d, want %d", st.scans, tt.wantScans)
			}
		})
	}
}

// unescapeU turns the test's @U@ into \u, so that the source never holds a
// \u escape that a tool might decode.
func unescapeU(s string) string { return strings.ReplaceAll(s, "@U@", `\u`) }

// TestDecodeFieldPaths checks where the decoder reports a failure against the
// Python SDK 0.7.1 (probed on the upstream checkout's venv; the script and
// its output are in _spikes/w2.0/results/python-paths.txt): the eight rows of
// test_malformed_response_raises_validation_error (R1), the order in which
// the Python SDK reports a body with several faults (its answer-type
// pre-pass, then model, usage and answers, then each answer's members in
// schema order), and the documented deviations.
func TestDecodeFieldPaths(t *testing.T) {
	// r1 builds R1's body: usage, answers, then model unless it is the row
	// that leaves model out.
	r1 := func(answers string, model bool) string {
		b := `{"usage":{"input_tokens":1,"output_tokens":1},"answers":` + answers
		if model {
			b += `,"model":"test"`
		}
		return b + "}"
	}
	body := func(answers string) string {
		return `{"model":"m","usage":{"input_tokens":1,"output_tokens":1},"answers":` + answers + "}"
	}
	tests := map[string]struct {
		body string
		want string // the field path, or "" for accepted
	}{
		// R1: tests/test_responses.py:29-40.
		"error: R1 no model":                  {body: r1(`{}`, false), want: "model"},
		"error: R1 noul missing":              {body: r1(`{"n":{"type":"noul"}}`, true), want: "answers.n.noul"},
		"error: R1 noul a string":             {body: r1(`{"n":{"type":"noul","noul":"0.5"}}`, true), want: "answers.n.noul"},
		"error: R1 choice without confidence": {body: r1(`{"c":{"type":"choice","choice":"a","probabilities":{}}}`, true), want: "answers.c.confidence"},
		"error: R1 choice without choice":     {body: r1(`{"c":{"type":"choice","confidence":0.5,"probabilities":{}}}`, true), want: "answers.c.choice"},
		"error: R1 legend an array":           {body: r1(`{"s":{"type":"score","score":1.0,"confidence":1.0,"legend":[],"probabilities":{}}}`, true), want: "answers.s.legend"},
		"error: R1 legend key not a level":    {body: r1(`{"s":{"type":"score","score":1.0,"confidence":1.0,"legend":{"x":"bad"},"probabilities":{}}}`, true), want: "answers.s.legend.x"},
		"error: R1 answer not a mapping":      {body: r1(`{"c":"not-a-mapping"}`, true), want: "answers.c.type"},

		// Several faults: the first one the Python SDK reports.
		"error: score wrong, confidence missing":                {body: body(`{"s":{"type":"score","score":"x","legend":{},"probabilities":{}}}`), want: "answers.s.score"},
		"error: confidence wrong, score missing":                {body: body(`{"s":{"type":"score","confidence":"x","legend":{},"probabilities":{}}}`), want: "answers.s.score"},
		"error: legend missing, probabilities wrong":            {body: body(`{"s":{"type":"score","score":1,"confidence":1,"probabilities":[]}}`), want: "answers.s.legend"},
		"error: probabilities wrong first on the wire":          {body: body(`{"s":{"probabilities":[],"type":"score","score":1,"confidence":1}}`), want: "answers.s.legend"},
		"error: score probability not a number":                 {body: body(`{"s":{"type":"score","score":1,"confidence":1,"legend":{},"probabilities":{"0":"x"}}}`), want: "answers.s.probabilities.0"},
		"error: score probability key not a level":              {body: body(`{"s":{"type":"score","score":1,"confidence":1,"legend":{},"probabilities":{"x":1}}}`), want: "answers.s.probabilities.x"},
		"error: choice probabilities wrong, choice missing":     {body: body(`{"c":{"type":"choice","confidence":1,"probabilities":[]}}`), want: "answers.c.choice"},
		"error: choice probability a string":                    {body: body(`{"c":{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":"x"}}}`), want: "answers.c.probabilities.a"},
		"error: choice probability a boolean":                   {body: body(`{"c":{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":true}}}`), want: "answers.c.probabilities.a"},
		"error: noul a boolean":                                 {body: body(`{"n":{"type":"noul","noul":true}}`), want: "answers.n.noul"},
		"error: two bad answers, the first in wire order":       {body: body(`{"a":{"type":"noul"},"b":{"type":"choice"}}`), want: "answers.a.noul"},
		"error: type not a string":                              {body: body(`{"a":{"type":5}}`), want: "answers.a.type"},
		"error: type null":                                      {body: body(`{"a":{"type":null}}`), want: "answers.a.type"},
		"error: answer type pre-pass before a missing model":    {body: `{"usage":{},"answers":{"c":"x"}}`, want: "answers.c.type"},
		"error: missing model before an answer's member":        {body: `{"usage":{},"answers":{"n":{"type":"noul"}}}`, want: "model"},
		"error: answer type pre-pass before an earlier answer":  {body: body(`{"n":{"type":"noul"},"c":"x"}`), want: "answers.c.type"},
		"error: answer type pre-pass on a later integer type":   {body: body(`{"n":{"type":"noul"},"c":{"type":1}}`), want: "answers.c.type"},
		"error: model before usage":                             {body: `{"model":1,"usage":1,"answers":{}}`, want: "model"},
		"error: usage before answers":                           {body: `{"model":"m","usage":1,"answers":[]}`, want: "usage"},
		"error: answers null":                                   {body: `{"model":"m","usage":{},"answers":null}`, want: "answers"},
		"error: usage null":                                     {body: `{"model":"m","usage":null}`, want: "usage"},
		"success: a token count null is absent":                 {body: `{"model":"m","usage":{"input_tokens":null}}`},
		"error: a token count 1.0":                              {body: `{"model":"m","usage":{"input_tokens":1.0}}`, want: "usage.input_tokens"},
		"error: a token count 1.5":                              {body: `{"model":"m","usage":{"input_tokens":1.5}}`, want: "usage.input_tokens"},
		"error: output_tokens a string":                         {body: `{"model":"m","usage":{"output_tokens":"3"}}`, want: "usage.output_tokens"},
		"error: model null":                                     {body: `{"model":null,"usage":{}}`, want: "model"},
		"error: an unknown type before a missing model":         {body: `{"usage":{},"answers":{"u":{"type":"aurora"}}}`, want: "model"},
		"error: answer type pre-pass past an unknown type":      {body: body(`{"u":{"type":"aurora"},"c":1}`), want: "answers.c.type"},
		"success: an empty legend and probabilities":            {body: body(`{"s":{"type":"score","score":1,"confidence":1,"legend":{},"probabilities":{}}}`)},
		"success: an integer noul":                              {body: body(`{"n":{"type":"noul","noul":1}}`)},
		"success: legend members of other kinds are not needed": {body: body(`{"n":{"type":"noul","noul":1,"choice":5,"legend":[]}}`)},

		// Deviations (Appendix B).
		"error: deviation, a negative token count":         {body: `{"model":"m","usage":{"input_tokens":-1}}`, want: "usage.input_tokens"},
		"error: deviation, a token count past 2^64-1":      {body: `{"model":"m","usage":{"input_tokens":18446744073709551616}}`, want: "usage.input_tokens"},
		"error: deviation, a level value that is a number": {body: body(`{"s":{"type":"score","score":1,"confidence":1,"legend":{"0":5},"probabilities":{}}}`), want: "answers.s.legend.0"},
		"error: deviation, level key -1":                   {body: body(`{"s":{"type":"score","score":1,"confidence":1,"legend":{"-1":"a"},"probabilities":{}}}`), want: "answers.s.legend.-1"},
		"error: deviation, 1e400 in a confidence":          {body: body(`{"c":{"type":"choice","choice":"a","confidence":1e400,"probabilities":{}}}`), want: "answers.c.confidence"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := decodeBody(t, []byte(tt.body), nil, "")
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("refused at %s (%v), want accepted", pathOf(t, err), err)
			case tt.want == "":
			case err == nil:
				t.Fatalf("accepted, want a refusal at %s", tt.want)
			default:
				if got := pathOf(t, err); got != tt.want {
					t.Errorf("path = %q (%v), want %q", got, err, tt.want)
				}
			}
		})
	}
}

// TestModelsFieldPaths checks the list-models decoder's paths against the
// Python SDK 0.7.1 (the same probe): R2's models[1].<missing> rows
// (tests/test_responses.py:61-68), the first card first and each card's
// members in schema order.
func TestModelsFieldPaths(t *testing.T) {
	const card = `{"name":"test","description":"Test model","release_date":"2026-09-14"}`
	tests := map[string]struct {
		body string
		want string
	}{
		"error: R2 models[1] without name":         {body: `{"models":[` + card + `,{"description":"Test model","release_date":"2026-09-14"}]}`, want: "models[1].name"},
		"error: R2 models[1] without description":  {body: `{"models":[` + card + `,{"name":"test","release_date":"2026-09-14"}]}`, want: "models[1].description"},
		"error: R2 models[1] without release_date": {body: `{"models":[` + card + `,{"name":"test","description":"Test model"}]}`, want: "models[1].release_date"},
		"error: models missing":                    {body: `{}`, want: "models"},
		"error: models an object":                  {body: `{"models":{}}`, want: "models"},
		"error: models null":                       {body: `{"models":null}`, want: "models"},
		"error: a card that is not an object":      {body: `{"models":[1]}`, want: "models[0]"},
		"error: two members missing, schema order": {body: `{"models":[` + card + `,{"release_date":"c"}]}`, want: "models[1].name"},
		"error: a name that is a number":           {body: `{"models":[{"name":1,"description":"b","release_date":"c"}]}`, want: "models[0].name"},
		"error: the first failing card first":      {body: `{"models":[{"name":"a","release_date":"c"},5]}`, want: "models[0].description"},
		"error: a card after a scalar card":        {body: `{"models":[5,{"name":"a"}]}`, want: "models[0]"},
		"error: the second card is null":           {body: `{"models":[` + card + `,null]}`, want: "models[1]"},
		"success: an unknown top-level member":     {body: `{"models":[],"x":1}`},
		"success: a repeated models member":        {body: `{"models":[1],"models":[` + card + `]}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := DecodeModels([]byte(tt.body), new(wire.ModelList))
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("refused at %s (%v), want accepted", pathOf(t, err), err)
			case tt.want == "":
			case err == nil:
				t.Fatalf("accepted, want a refusal at %s", tt.want)
			default:
				if got := pathOf(t, err); got != tt.want {
					t.Errorf("path = %q (%v), want %q", got, err, tt.want)
				}
			}
		})
	}
}

// TestLevelKeys checks plan 6.2.6's level-key grammar [+]?[0-9]+ as a
// uint32, on a legend key and on a probability key: 1, +1, 01 and +01 are
// level 1, and every other spelling fails at the key (a deviation from
// pydantic's lax int, which takes " 1", "1_0", "1.0" and "-1").
func TestLevelKeys(t *testing.T) {
	tests := map[string]struct {
		key   string
		level uint32
		ok    bool
	}{
		"success: 1":             {key: "1", level: 1, ok: true},
		"success: +1":            {key: "+1", level: 1, ok: true},
		"success: 01":            {key: "01", level: 1, ok: true},
		"success: +01":           {key: "+01", level: 1, ok: true},
		"success: 0":             {key: "0", level: 0, ok: true},
		"success: 4294967295":    {key: "4294967295", level: 4294967295, ok: true},
		"success: many zeros":    {key: "0000000000000000000007", level: 7, ok: true},
		"error: 4294967296":      {key: "4294967296"},
		"error: 99999999999999":  {key: "99999999999999"},
		"error: -1":              {key: "-1"},
		"error: leading space":   {key: " 1"},
		"error: trailing space":  {key: "1 "},
		"error: underscore":      {key: "1_0"},
		"error: point":           {key: "1.0"},
		"error: empty":           {key: ""},
		"error: sign alone":      {key: "+"},
		"error: two signs":       {key: "++1"},
		"error: exponent":        {key: "1e2"},
		"error: fullwidth digit": {key: "\uff11"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if lvl, ok := parseLevel(tt.key); ok != tt.ok || lvl != tt.level {
				t.Errorf("parseLevel(%q) = %d, %t, want %d, %t", tt.key, lvl, ok, tt.level, tt.ok)
			}
			for _, member := range []string{"legend", "probabilities"} {
				legend, probs := `{"`+tt.key+`":"text"}`, `{}`
				if member == "probabilities" {
					legend, probs = `{}`, `{"`+tt.key+`":1}`
				}
				b := `{"model":"m","usage":{},"answers":{"s":{"type":"score","score":1,"confidence":1,"legend":` + legend + `,"probabilities":` + probs + `}}}`
				res, _, _, err := decodeBody(t, []byte(b), nil, "")
				if !tt.ok {
					if err == nil {
						t.Fatalf("%s key %q accepted", member, tt.key)
					}
					if got, want := pathOf(t, err), "answers.s."+member+"."+tt.key; got != want {
						t.Errorf("%s key %q: path %q, want %q", member, tt.key, got, want)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s key %q: %v", member, tt.key, err)
				}
				a, _ := res.Answers.Get("s")
				got := a.Score.Probabilities
				if member == "legend" {
					if len(a.Score.Legend) != 1 || a.Score.Legend[0].Level != tt.level {
						t.Errorf("legend = %+v, want level %d", a.Score.Legend, tt.level)
					}
					continue
				}
				if diff := gocmp.Diff([]wire.LevelProbability{{Level: tt.level, Probability: 1}}, got); diff != "" {
					t.Errorf("probabilities (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// TestUnknownMembersIgnored ports test_unknown_extra_fields_tolerated (R9,
// tests/test_responses.py:140-154) to the decoder: members the schema does
// not name, in usage and in an answer, are read past and dropped.
func TestUnknownMembersIgnored(t *testing.T) {
	body := `{"model":"test","usage":{"input_tokens":1,"output_tokens":1,"reasoning_tokens":9,"billing_units":1},"answers":{"spam":{"type":"noul","noul":0.9,"explanation":"spammy"}}}`
	res, _, _, err := decodeBody(t, []byte(body), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []wire.AnswerEntry{{Name: "spam", Answer: wire.Answer{Kind: wire.KindNoul, Noul: wire.NoulAnswer{Noul: 0.9}}}}
	if diff := gocmp.Diff(want, res.Answers.Entries()); diff != "" {
		t.Errorf("answers (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff(wire.Usage{InputTokens: 1, OutputTokens: 1, HasInputTokens: true, HasOutputTokens: true}, res.Usage); diff != "" {
		t.Errorf("usage (-want +got):\n%s", diff)
	}
}

// TestStructuredLegendExactBytes ports test_response_preserves_nested_json
// (R11, tests/test_responses.py:178-215) to the decoder: a structured level
// arrives as the exact bytes of the body, escapes and all, and a level
// equal to the question's compact JSON is the question's bytes.
func TestStructuredLegendExactBytes(t *testing.T) {
	tests := map[string]struct {
		level string
	}{
		"success: R11's nested object and array": {level: `{"examples":["a",{"note":null}]}`},
		"success: escapes kept":                  {level: unescapeU(`{"summ@U@0061ry":"\"quoted\"\\","emoji":"@U@d83c@U@df0d","nl":"a\nb"}`)},
		"success: an array at the top":           {level: `["level 1",{"note":null},[],{}]`},
		"success: numbers as written":            {level: `{"n":1e2,"m":-0.0,"k":10000000000000000000000}`},
		"success: insignificant whitespace kept": {level: "{ \"a\" : [ 1 , 2 ] }"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := `{"model":"test","usage":{"input_tokens":1,"output_tokens":1},"answers":{"q":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":` + tt.level + `},"probabilities":{"0":1.0}}}}`
			for _, q := range []*wire.Prepared{nil, questionsFor(t, mustDecode(t, body))} {
				res, _, _, err := decodeBody(t, []byte(body), q, "")
				if err != nil {
					t.Fatal(err)
				}
				a, _ := res.Answers.Get("q")
				if got := string(a.Score.Legend[0].Description.JSON); got != tt.level {
					t.Errorf("questions %t: level 0 = %s, want %s", q != nil, got, tt.level)
				}
			}
		})
	}
}

func mustDecode(tb testing.TB, body string) *wire.SystemOneResult {
	tb.Helper()
	res, _, _, err := decodeBody(tb, []byte(body), nil, "")
	if err != nil {
		tb.Fatal(err)
	}
	return res
}

// TestPublicTypesIgnoreUnknownMembers ports
// test_public_response_types_ignore_unknown_fields (R13,
// tests/test_responses.py:234-249) to the decoder: an "unexpected" member in
// each of the seven response types leaves the decoded value as it is
// without it.
func TestPublicTypesIgnoreUnknownMembers(t *testing.T) {
	const x = `"unexpected":true`
	tests := map[string]struct {
		plain, extra string
		models       bool
	}{
		"success: NoulAnswer": {
			plain: `{"model":"m","usage":{},"answers":{"a":{"type":"noul","noul":0.5}}}`,
			extra: `{"model":"m","usage":{},"answers":{"a":{"type":"noul","noul":0.5,` + x + `}}}`,
		},
		"success: ChoiceAnswer": {
			plain: `{"model":"m","usage":{},"answers":{"a":{"type":"choice","choice":"a","confidence":1.0,"probabilities":{"a":1.0}}}}`,
			extra: `{"model":"m","usage":{},"answers":{"a":{` + x + `,"type":"choice","choice":"a","confidence":1.0,"probabilities":{"a":1.0}}}}`,
		},
		"success: ScoreAnswer": {
			plain: `{"model":"m","usage":{},"answers":{"a":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"bad"},"probabilities":{"0":1.0}}}}`,
			extra: `{"model":"m","usage":{},"answers":{"a":{"type":"score","score":0.0,"confidence":1.0,` + x + `,"legend":{"0":"bad"},"probabilities":{"0":1.0}}}}`,
		},
		"success: Usage": {
			plain: `{"model":"m","usage":{}}`,
			extra: `{"model":"m","usage":{` + x + `}}`,
		},
		"success: SystemOneResponse": {
			plain: `{"model":"test","usage":{}}`,
			extra: `{"model":"test","usage":{},` + x + `}`,
		},
		"success: ModelMetadata": {
			plain:  `{"models":[{"name":"test","description":"Test model","release_date":"2026-09-14"}]}`,
			extra:  `{"models":[{"name":"test","description":"Test model","release_date":"2026-09-14",` + x + `}]}`,
			models: true,
		},
		"success: ListModelsResponse": {
			plain:  `{"models":[]}`,
			extra:  `{"models":[],` + x + `}`,
			models: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.models {
				var plain, extra wire.ModelList
				if err := DecodeModels([]byte(tt.plain), &plain); err != nil {
					t.Fatal(err)
				}
				if err := DecodeModels([]byte(tt.extra), &extra); err != nil {
					t.Fatal(err)
				}
				if diff := gocmp.Diff(plain, extra); diff != "" {
					t.Errorf("(-without +with the member):\n%s", diff)
				}
				return
			}
			plain, extra := mustDecode(t, tt.plain), mustDecode(t, tt.extra)
			if diff := gocmp.Diff(plain.Answers.Entries(), extra.Answers.Entries()); diff != "" || plain.Model != extra.Model || plain.Usage != extra.Usage {
				t.Errorf("model %q/%q usage %+v/%+v, answers (-without +with the member):\n%s", plain.Model, extra.Model, plain.Usage, extra.Usage, diff)
			}
		})
	}
}

// TestSkippedAnswers checks what a decode reports as skipped, as the Python
// SDK logs it: the answers of unknown types in the order their names first
// appear, the first MaxSkipped by name and the rest counted, only those of
// the last "answers" member, and, when the answer-type pre-pass fails, only
// those before the failing answer, even though the decode fails.
func TestSkippedAnswers(t *testing.T) {
	unknown := func(n int) string {
		var b strings.Builder
		b.WriteString(`{"model":"m","usage":{},"answers":{`)
		for i := range n {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`"u` + string(rune('a'+i)) + `":{"type":"t` + string(rune('a'+i)) + `"}`)
		}
		b.WriteString(`}}`)
		return b.String()
	}
	tests := map[string]struct {
		body      string
		wantNamed []SkippedAnswer
		wantCount int
		wantPath  string
	}{
		"success: none": {body: `{"model":"m","usage":{},"answers":{"a":{"type":"noul","noul":1}}}`},
		"success: nine unknown answers, eight by name": {
			body: unknown(9),
			wantNamed: []SkippedAnswer{
				{"ua", "ta"}, {"ub", "tb"}, {"uc", "tc"}, {"ud", "td"}, {"ue", "te"}, {"uf", "tf"}, {"ug", "tg"}, {"uh", "th"},
			},
			wantCount: 9,
		},
		"success: a repeated unknown name is one answer at its first position": {
			body:      `{"model":"m","usage":{},"answers":{"x":{"type":"a"},"n":{"type":"noul","noul":1},"x":{"type":"b"}}}`,
			wantNamed: []SkippedAnswer{{"x", "b"}},
			wantCount: 1,
		},
		"error: the pre-pass stops the report at the failing answer": {
			body:      `{"model":"m","usage":{},"answers":{"u":{"type":"a"},"c":1,"v":{"type":"b"}}}`,
			wantNamed: []SkippedAnswer{{"u", "a"}},
			wantCount: 1,
			wantPath:  "answers.c.type",
		},
		"error: a later failure still reports every unknown answer": {
			body:      `{"usage":{},"answers":{"u":{"type":"a"},"v":{"type":"b"}}}`,
			wantNamed: []SkippedAnswer{{"u", "a"}, {"v", "b"}},
			wantCount: 2,
			wantPath:  "model",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var res wire.SystemOneResult
			skipped, err := DecodeSystemOne([]byte(tt.body), nil, "", &res)
			if tt.wantPath == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantPath != "" {
				if err == nil {
					t.Fatalf("accepted, want a refusal at %s", tt.wantPath)
				}
				if got := pathOf(t, err); got != tt.wantPath {
					t.Errorf("path = %q, want %q", got, tt.wantPath)
				}
			}
			if diff := gocmp.Diff(tt.wantNamed, skipped.Named(), gocmp.Comparer(func(a, b []SkippedAnswer) bool {
				return len(a) == len(b) && (len(a) == 0 || gocmp.Equal(a, b))
			})); diff != "" || skipped.Count != tt.wantCount {
				t.Errorf("count %d, want %d; named (-want +got):\n%s", skipped.Count, tt.wantCount, diff)
			}
		})
	}
}

// TestDecodeErrorText checks that a *DecodeError's text never carries the
// body: not sonic's quotation of it for a syntax error, and not a name the
// server chose.
func TestDecodeErrorText(t *testing.T) {
	const secret = "s3cr3t-token-in-body"
	tests := map[string]struct {
		body string
		want string
	}{
		"error: a syntax error names the position": {body: `{"model":"` + secret + `","usage":{},"meta":tru}`, want: "invalid JSON: json: error when parsing input"},
		"error: a member failure names the reason": {body: `{"model":"m","usage":{},"answers":{"` + secret + `":{"type":"noul"}}}`, want: "invalid response data: missing"},
		"error: trailing data":                     {body: `{"model":"m","usage":{}} ` + secret, want: "invalid JSON: data after the top-level value"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeSystemOne([]byte(tt.body), nil, "", new(wire.SystemOneResult))
			if err == nil {
				t.Fatal("accepted")
			}
			if msg := err.Error(); strings.Contains(msg, secret) || !strings.HasPrefix(msg, tt.want) {
				t.Errorf("Error() = %q, want a prefix %q and no body text", msg, tt.want)
			}
			var de *DecodeError
			if !errors.As(err, &de) || de.Unwrap() == nil {
				t.Errorf("error %v does not unwrap to its reason", err)
			}
		})
	}
}

// TestDecodeDepthBound checks the nesting cap of review W2.0 MAJOR 1 and
// ruling R73: a body may nest 4096 containers in all, the root included,
// as sonic's decoder.Skip takes, and a container past that is refused by the
// visitor as it opens (errDepth at the root), so sonic's traversal, which
// recurses once per level, never goes deeper. A body nested a million deep
// is refused the same way, with the goroutine's stack growing by a few
// hundred KiB rather than the 256 MiB it took before the cap (a body under
// the 16 MiB size cap nested 4x10^6 deep killed the process). Objects stop
// one level earlier, in Skip: it counts the innermost value of an object.
func TestDecodeDepthBound(t *testing.T) {
	arrays := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	objects := func(n int) string { return strings.Repeat(`{"a":`, n) + "1" + strings.Repeat("}", n) }
	systemOne := func(member string) string {
		return `{"model":"m","usage":{},"answers":{"n":{"type":"noul","noul":1}},"x":` + member + `}`
	}
	legend := func(level string) string {
		return `{"model":"m","usage":{},"answers":{"s":{"type":"score","score":0,"confidence":1,"legend":{"0":` + level + `},"probabilities":{}}}}`
	}
	models := func(member string) string {
		return `{"models":[{"name":"n","description":"d","release_date":"r","x":` + member + `}]}`
	}
	tests := map[string]struct {
		body      string
		models    bool
		wantErr   bool
		wantDepth bool // refused by the visitor's cap, not by Skip
		bounded   bool // assert bounded stack growth
	}{
		"success: 4095 arrays in a root member, 4096 in all":    {body: systemOne(arrays(4095))},
		"error: 4096 arrays in a root member":                   {body: systemOne(arrays(4096)), wantErr: true, wantDepth: true},
		"error: 4097 arrays in a root member":                   {body: systemOne(arrays(4097)), wantErr: true, wantDepth: true},
		"success: 4092 arrays in a legend level, 4096 in all":   {body: legend(arrays(4092))},
		"error: 4093 arrays in a legend level":                  {body: legend(arrays(4093)), wantErr: true, wantDepth: true},
		"error: 4095 objects in a root member, refused by Skip": {body: systemOne(objects(4095)), wantErr: true},
		"error: a million arrays in a root member":              {body: systemOne(arrays(1_000_000)), wantErr: true, wantDepth: true, bounded: true},
		"success: models, 4093 arrays in a card member":         {body: models(arrays(4093)), models: true},
		"error: models, 4094 arrays in a card member":           {body: models(arrays(4094)), models: true, wantErr: true, wantDepth: true},
		"error: models, a million arrays in a card member":      {body: models(arrays(1_000_000)), models: true, wantErr: true, wantDepth: true, bounded: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var err error
			decode := func() {
				if tt.models {
					err = DecodeModels([]byte(tt.body), new(wire.ModelList))
					return
				}
				_, err = DecodeSystemOne([]byte(tt.body), nil, "", new(wire.SystemOneResult))
			}
			growth, elapsed := stackGrowth(decode)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("refused (%v), want accepted", err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted, want refused")
			}
			if got := pathOf(t, err); got != "." {
				t.Errorf("path = %q, want %q", got, ".")
			}
			if got := errors.Is(err, errDepth); got != tt.wantDepth {
				t.Errorf("errors.Is(err, errDepth) = %t, want %t (err %v)", got, tt.wantDepth, err)
			}
			if tt.bounded {
				t.Logf("stack growth %d KiB, %v", growth>>10, elapsed)
				if growth > 16<<20 || elapsed > 2*time.Second {
					t.Errorf("stack grew by %d bytes in %v, want at most 16 MiB and 2 s", growth, elapsed)
				}
			}
		})
	}
	var res wire.SystemOneResult
	_, err := DecodeSystemOne(testsupport.Fixture(t, "malformed-too-deep.json"), nil, "", &res)
	if !errors.Is(err, errDepth) {
		t.Errorf("malformed-too-deep.json: err = %v, want errDepth", err)
	}
}

// stackGrowth runs f on a goroutine of its own and returns how much the
// stack memory in use grew by (runtime.MemStats.StackInuse, read on that
// goroutine after f returns and before it exits, while its grown stack is
// still held), which is how deep f recursed, and how long f took.
func stackGrowth(f func()) (uint64, time.Duration) {
	var before, after runtime.MemStats
	var elapsed time.Duration
	runtime.ReadMemStats(&before)
	done := make(chan struct{})
	go func() {
		defer close(done)
		start := time.Now()
		f()
		elapsed = time.Since(start)
		runtime.ReadMemStats(&after)
	}()
	<-done
	if after.StackInuse < before.StackInuse {
		return 0, elapsed
	}
	return after.StackInuse - before.StackInuse, elapsed
}
