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

package wire

import (
	"errors"
	"math"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// answersOf returns an Answers holding entries in order, through Put.
func answersOf(entries ...AnswerEntry) Answers {
	var s Answers
	for _, e := range entries {
		s.Put(e.Name, e.Answer)
	}
	return s
}

// upstreamResult is the upstream RESULT (typesafe-sdk-python
// tests/test_clients.py:42-56) as the decoder leaves it.
func upstreamResult() SystemOneResult {
	return SystemOneResult{
		Model: "jev-latest",
		Usage: Usage{InputTokens: 12, OutputTokens: 3, HasInputTokens: true, HasOutputTokens: true},
		Answers: answersOf(
			AnswerEntry{"spam", Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: 0.98}}},
			AnswerEntry{"tone", Answer{Kind: KindChoice, Choice: ChoiceAnswer{
				Choice: "friendly", Confidence: 0.9,
				Probabilities: []LabelProbability{{"friendly", 0.9}, {"hostile", 0.1}},
			}}},
			AnswerEntry{"quality", Answer{Kind: KindScore, Score: ScoreAnswer{
				Score: 1.7, Confidence: 0.8,
				Legend:        []LegendEntry{{0, Content{Text: "bad"}}, {1, Content{Text: "ok"}}, {2, Content{Text: "great"}}},
				Probabilities: []LevelProbability{{0, 0.1}, {1, 0.1}, {2, 0.8}},
			}}},
		),
	}
}

// resultJSON is testdata/result.json: the upstream RESULT as
// pydantic_core.to_json writes it, which is also what
// SystemOneResponse.model_dump_json writes for it
// (_spikes/w2.4/results/python-dump.txt, "result.json: equals the fixture:
// True").
const resultJSON = `{"model":"jev-latest","usage":{"input_tokens":12,"output_tokens":3},"answers":{"spam":{"type":"noul","noul":0.98},"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"ok","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}}}}`

func TestAppendSystemOneResult(t *testing.T) {
	tests := map[string]struct {
		r    SystemOneResult
		want string
	}{
		"success: the upstream RESULT is written as the Python SDK writes it": {
			r:    upstreamResult(),
			want: resultJSON,
		},
		"success: the zero result has an empty model, null counts and no answers": {
			want: `{"model":"","usage":{"input_tokens":null,"output_tokens":null},"answers":{}}`,
		},
		"success: an absent count is null and a reported zero is 0": {
			r: SystemOneResult{Model: "m", Usage: Usage{HasOutputTokens: true}},
			// no-answers.json as model_dump_json writes it, with the counts swapped.
			want: `{"model":"m","usage":{"input_tokens":null,"output_tokens":0},"answers":{}}`,
		},
		"success: the largest count is written in full": {
			r:    SystemOneResult{Model: "m", Usage: Usage{InputTokens: math.MaxUint64, HasInputTokens: true, OutputTokens: 1, HasOutputTokens: true}},
			want: `{"model":"m","usage":{"input_tokens":18446744073709551615,"output_tokens":1},"answers":{}}`,
		},
		"success: an entry of an unknown kind is left out": {
			r: SystemOneResult{Model: "m", Answers: answersOf(
				AnswerEntry{"future", Answer{}},
				AnswerEntry{"spam", Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: 1}}},
				AnswerEntry{"later", Answer{}},
			)},
			want: `{"model":"m","usage":{"input_tokens":null,"output_tokens":null},"answers":{"spam":{"type":"noul","noul":1.0}}}`,
		},
		"success: names and model are escaped as the Python SDK escapes them": {
			r: SystemOneResult{Model: "a\"b", Answers: answersOf(
				AnswerEntry{"new\nline", Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: 0.75}}},
				AnswerEntry{"sl/ash é 🌍", Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: 1}}},
			)},
			// escaped-names.json's names as model_dump_json writes them: \n
			// escaped, / and non-ASCII characters as they are.
			want: `{"model":"a\"b","usage":{"input_tokens":null,"output_tokens":null},"answers":{"new\nline":{"type":"noul","noul":0.75},"sl/ash é 🌍":{"type":"noul","noul":1.0}}}`,
		},
		"success: floats take zmij's layout": {
			r: SystemOneResult{Model: "m", Answers: answersOf(
				AnswerEntry{"a", Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: 1e-7}}},
				AnswerEntry{"b", Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: 1e20}}},
				AnswerEntry{"c", Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: 0.00001}}},
				AnswerEntry{"d", Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: 123456789}}},
			)},
			want: `{"model":"m","usage":{"input_tokens":null,"output_tokens":null},"answers":{"a":{"type":"noul","noul":1e-7},"b":{"type":"noul","noul":1e+20},"c":{"type":"noul","noul":0.00001},"d":{"type":"noul","noul":123456789.0}}}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := AppendSystemOneResult([]byte("prefix:"), &tt.r)
			if err != nil {
				t.Fatalf("AppendSystemOneResult: %v", err)
			}
			if diff := gocmp.Diff("prefix:"+tt.want, string(got)); diff != "" {
				t.Errorf("payload (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAppendAnswerKinds(t *testing.T) {
	structured := ScoreAnswer{
		Score: 0, Confidence: 1,
		Legend: []LegendEntry{
			{0, Content{JSON: []byte(`{"summ\u0061ry":"x", "n": 1E2}`)}},
			{1, Content{Text: "text"}},
			{4294967295, Content{JSON: []byte(`[]`)}},
		},
		Probabilities: []LevelProbability{{4294967295, 0.5}, {0, 0.25}, {1, 0.25}},
	}
	tests := map[string]struct {
		a    Answer
		want string
	}{
		"success: noul (R12's NoulAnswer(noul=0.98))": {
			a:    Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: 0.98}},
			want: `{"type":"noul","noul":0.98}`,
		},
		"success: choice (R12's ChoiceAnswer), probabilities in stored order": {
			a: Answer{Kind: KindChoice, Choice: ChoiceAnswer{
				Choice: "billing", Confidence: 0.9,
				Probabilities: []LabelProbability{{"billing", 0.9}, {"support", 0.1}},
			}},
			want: `{"type":"choice","choice":"billing","confidence":0.9,"probabilities":{"billing":0.9,"support":0.1}}`,
		},
		"success: score (R14's ScoreAnswer), integral floats keep .0": {
			a: Answer{Kind: KindScore, Score: ScoreAnswer{
				Score: 0, Confidence: 1,
				Legend:        []LegendEntry{{0, Content{Text: "bad"}}},
				Probabilities: []LevelProbability{{0, 1}},
			}},
			want: `{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"bad"},"probabilities":{"0":1.0}}`,
		},
		"success: structured levels are spliced as received (R73), levels as decimal names": {
			a:    Answer{Kind: KindScore, Score: structured},
			want: `{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":{"summ\u0061ry":"x", "n": 1E2},"1":"text","4294967295":[]},"probabilities":{"4294967295":0.5,"0":0.25,"1":0.25}}`,
		},
		"success: zero noul": {
			a:    Answer{Kind: KindNoul},
			want: `{"type":"noul","noul":0.0}`,
		},
		"success: zero choice": {
			a:    Answer{Kind: KindChoice},
			want: `{"type":"choice","choice":"","confidence":0.0,"probabilities":{}}`,
		},
		"success: zero score": {
			a:    Answer{Kind: KindScore},
			want: `{"type":"score","score":0.0,"confidence":0.0,"legend":{},"probabilities":{}}`,
		},
		"success: an unknown kind holds no value": {
			a:    Answer{Kind: Kind(9)},
			want: `null`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := AppendAnswer(nil, &tt.a)
			if err != nil {
				t.Fatalf("AppendAnswer: %v", err)
			}
			if diff := gocmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("AppendAnswer (-want +got):\n%s", diff)
			}
			// The kind's own appender writes the same bytes.
			var own []byte
			switch tt.a.Kind {
			case KindNoul:
				own, err = AppendNoulAnswer(nil, &tt.a.Noul)
			case KindChoice:
				own, err = AppendChoiceAnswer(nil, &tt.a.Choice)
			case KindScore:
				own, err = AppendScoreAnswer(nil, &tt.a.Score)
			default:
				return
			}
			if err != nil || string(own) != string(got) {
				t.Errorf("the kind's appender = %s, %v, want %s", own, err, got)
			}
		})
	}
}

func TestAppendAnswers(t *testing.T) {
	tests := map[string]struct {
		s    *Answers
		want string
	}{
		"success: nil is an empty object": {
			want: `{}`,
		},
		"success: a repeated name keeps its first position and last value": {
			s: func() *Answers {
				s := answersOf(
					AnswerEntry{"b", Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: 0.1}}},
					AnswerEntry{"a", Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: 0.2}}},
					AnswerEntry{"b", Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: 0.3}}},
				)
				return &s
			}(),
			want: `{"b":{"type":"noul","noul":0.3},"a":{"type":"noul","noul":0.2}}`,
		},
		"success: only unknown entries is an empty object": {
			s: func() *Answers {
				s := answersOf(AnswerEntry{"x", Answer{}})
				return &s
			}(),
			want: `{}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := AppendAnswers(nil, tt.s)
			if err != nil {
				t.Fatalf("AppendAnswers: %v", err)
			}
			if diff := gocmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("AppendAnswers (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAppendModelList(t *testing.T) {
	tests := map[string]struct {
		l    ModelList
		want string
	}{
		"success: models.json as model_dump_json writes it": {
			l:    ModelList{Models: []ModelCard{{"jev-latest", "Fast model", "2026-08-01"}}},
			want: `{"models":[{"name":"jev-latest","description":"Fast model","release_date":"2026-08-01"}]}`,
		},
		"success: no models (ListModelsResponse(models=()))": {
			want: `{"models":[]}`,
		},
		"success: two cards in order, escaped": {
			l:    ModelList{Models: []ModelCard{{"a", "tab\there", ""}, {"b", "", "x"}}},
			want: `{"models":[{"name":"a","description":"tab\there","release_date":""},{"name":"b","description":"","release_date":"x"}]}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := AppendModelList(nil, &tt.l)
			if err != nil {
				t.Fatalf("AppendModelList: %v", err)
			}
			if diff := gocmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("AppendModelList (-want +got):\n%s", diff)
			}
			// Each card is what AppendModelCard writes for it.
			cards := []byte(`{"models":[`)
			for i := range tt.l.Models {
				if i > 0 {
					cards = append(cards, ',')
				}
				if cards, err = AppendModelCard(cards, &tt.l.Models[i]); err != nil {
					t.Fatalf("AppendModelCard: %v", err)
				}
			}
			if cards = append(cards, ']', '}'); string(cards) != string(got) {
				t.Errorf("the cards of AppendModelCard make %s, want %s", cards, got)
			}
		})
	}
}

func TestAppendUsage(t *testing.T) {
	tests := map[string]struct {
		u    Usage
		want string
	}{
		"success: Usage(input_tokens=12, output_tokens=3)": {
			u:    Usage{InputTokens: 12, OutputTokens: 3, HasInputTokens: true, HasOutputTokens: true},
			want: `{"input_tokens":12,"output_tokens":3}`,
		},
		"success: Usage() writes null counts": {
			want: `{"input_tokens":null,"output_tokens":null}`,
		},
		"success: a reported zero is 0, an absent count null": {
			u:    Usage{InputTokens: 7, HasOutputTokens: true},
			want: `{"input_tokens":null,"output_tokens":0}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := gocmp.Diff("prefix"+tt.want, string(AppendUsage([]byte("prefix"), &tt.u))); diff != "" {
				t.Errorf("AppendUsage (-want +got):\n%s", diff)
			}
		})
	}
}

// TestPayloadErrors covers values the decoder never produces: every failure
// names its cause and leaves dst as it was, through the outer appenders and
// through each per-kind one, whose own dst is returned to the caller.
func TestPayloadErrors(t *testing.T) {
	const bad = "bad \xff"
	noul := func(f float64) Answer { return Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: f}} }
	withAnswer := func(name string, a Answer) func(dst []byte) ([]byte, error) {
		return func(dst []byte) ([]byte, error) {
			r := SystemOneResult{Answers: answersOf(AnswerEntry{name, a})}
			return AppendSystemOneResult(dst, &r)
		}
	}
	tests := map[string]struct {
		write   func(dst []byte) ([]byte, error)
		wantErr error
	}{
		"error: invalid UTF-8 in the model": {
			write: func(dst []byte) ([]byte, error) {
				r := SystemOneResult{Model: bad}
				return AppendSystemOneResult(dst, &r)
			},
			wantErr: ErrInvalidUTF8,
		},
		"error: invalid UTF-8 in a name": {
			write:   withAnswer(bad, noul(0.5)),
			wantErr: ErrInvalidUTF8,
		},
		"error: NaN noul": {
			write:   withAnswer("n", noul(math.NaN())),
			wantErr: ErrUnsupportedValue,
		},
		"error: infinite confidence": {
			write:   withAnswer("c", Answer{Kind: KindChoice, Choice: ChoiceAnswer{Confidence: math.Inf(1)}}),
			wantErr: ErrUnsupportedValue,
		},
		"error: invalid UTF-8 in the choice": {
			write:   withAnswer("c", Answer{Kind: KindChoice, Choice: ChoiceAnswer{Choice: bad}}),
			wantErr: ErrInvalidUTF8,
		},
		"error: invalid UTF-8 in a label": {
			write:   withAnswer("c", Answer{Kind: KindChoice, Choice: ChoiceAnswer{Probabilities: []LabelProbability{{bad, 1}}}}),
			wantErr: ErrInvalidUTF8,
		},
		"error: infinite choice probability": {
			write:   withAnswer("c", Answer{Kind: KindChoice, Choice: ChoiceAnswer{Probabilities: []LabelProbability{{"a", math.Inf(-1)}}}}),
			wantErr: ErrUnsupportedValue,
		},
		"error: NaN score": {
			write:   withAnswer("s", Answer{Kind: KindScore, Score: ScoreAnswer{Score: math.NaN()}}),
			wantErr: ErrUnsupportedValue,
		},
		"error: NaN score confidence": {
			write:   withAnswer("s", Answer{Kind: KindScore, Score: ScoreAnswer{Confidence: math.NaN()}}),
			wantErr: ErrUnsupportedValue,
		},
		"error: invalid UTF-8 in a text level": {
			write:   withAnswer("s", Answer{Kind: KindScore, Score: ScoreAnswer{Legend: []LegendEntry{{0, Content{Text: bad}}}}}),
			wantErr: ErrInvalidUTF8,
		},
		"error: empty structured level": {
			write:   withAnswer("s", Answer{Kind: KindScore, Score: ScoreAnswer{Legend: []LegendEntry{{0, Content{JSON: []byte{}}}}}}),
			wantErr: ErrContentShape,
		},
		"error: NaN level probability": {
			write:   withAnswer("s", Answer{Kind: KindScore, Score: ScoreAnswer{Probabilities: []LevelProbability{{0, math.NaN()}}}}),
			wantErr: ErrUnsupportedValue,
		},
		"error: invalid UTF-8 in a card name": {
			write: func(dst []byte) ([]byte, error) {
				return AppendModelList(dst, &ModelList{Models: []ModelCard{{Name: bad}}})
			},
			wantErr: ErrInvalidUTF8,
		},
		"error: invalid UTF-8 in a card description": {
			write: func(dst []byte) ([]byte, error) {
				return AppendModelList(dst, &ModelList{Models: []ModelCard{{Description: bad}}})
			},
			wantErr: ErrInvalidUTF8,
		},
		"error: invalid UTF-8 in a card release date": {
			write: func(dst []byte) ([]byte, error) {
				return AppendModelList(dst, &ModelList{Models: []ModelCard{{ReleaseDate: bad}}})
			},
			wantErr: ErrInvalidUTF8,
		},
		"error: invalid UTF-8 in a card of AppendModelCard": {
			write: func(dst []byte) ([]byte, error) {
				return AppendModelCard(dst, &ModelCard{Name: "a", Description: "b", ReleaseDate: bad})
			},
			wantErr: ErrInvalidUTF8,
		},
		"error: AppendNoulAnswer with a NaN noul": {
			write: func(dst []byte) ([]byte, error) {
				return AppendNoulAnswer(dst, &NoulAnswer{Noul: math.NaN()})
			},
			wantErr: ErrUnsupportedValue,
		},
		"error: AppendChoiceAnswer with invalid UTF-8 in a label after a valid one": {
			write: func(dst []byte) ([]byte, error) {
				return AppendChoiceAnswer(dst, &ChoiceAnswer{Choice: "a", Probabilities: []LabelProbability{{"a", 0.5}, {bad, 0.5}}})
			},
			wantErr: ErrInvalidUTF8,
		},
		"error: AppendScoreAnswer with an empty structured level after a text one": {
			write: func(dst []byte) ([]byte, error) {
				return AppendScoreAnswer(dst, &ScoreAnswer{Legend: []LegendEntry{{0, Content{Text: "x"}}, {1, Content{JSON: []byte{}}}}})
			},
			wantErr: ErrContentShape,
		},
		"error: AppendAnswer with an infinite score probability": {
			write: func(dst []byte) ([]byte, error) {
				return AppendAnswer(dst, &Answer{Kind: KindScore, Score: ScoreAnswer{Probabilities: []LevelProbability{{0, 0.5}, {1, math.Inf(1)}}}})
			},
			wantErr: ErrUnsupportedValue,
		},
		"error: invalid UTF-8 in a name of AppendAnswers": {
			write: func(dst []byte) ([]byte, error) {
				s := answersOf(AnswerEntry{bad, noul(0.5)})
				return AppendAnswers(dst, &s)
			},
			wantErr: ErrInvalidUTF8,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := tt.write([]byte("prefix"))
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
			if string(got) != "prefix" {
				t.Errorf("dst = %q, want it unchanged (%q)", got, "prefix")
			}
		})
	}
}

// TestPayloadGrowsOnce checks the sizing: a payload whose strings need no
// escape is written into the one buffer its appender grows, even with every
// float and count at its longest, and one whose strings need escapes is
// still written in full. The allocation count is not checked under -race,
// like the other allocation tests.
func TestPayloadGrowsOnce(t *testing.T) {
	long := -2.2250738585072014e-308
	longest := SystemOneResult{
		Model: "jev-latest",
		Usage: Usage{InputTokens: math.MaxUint64, OutputTokens: math.MaxUint64, HasInputTokens: true, HasOutputTokens: true},
		Answers: answersOf(
			AnswerEntry{"n", Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: long}}},
			AnswerEntry{"c", Answer{Kind: KindChoice, Choice: ChoiceAnswer{Choice: "a", Confidence: long, Probabilities: []LabelProbability{{"a", long}, {"b", long}}}}},
			AnswerEntry{"s", Answer{Kind: KindScore, Score: ScoreAnswer{
				Score: long, Confidence: long,
				Legend:        []LegendEntry{{4294967295, Content{Text: "t"}}, {4294967294, Content{JSON: []byte(`{"a":[1,2]}`)}}},
				Probabilities: []LevelProbability{{4294967295, long}, {4294967294, long}},
			}}},
		),
	}
	upstream := upstreamResult()
	cards := ModelList{Models: []ModelCard{{"a", "b", "c"}, {"d", "e", "f"}}}
	tests := map[string]struct {
		write     func() ([]byte, error)
		wantAlloc float64
	}{
		"success: the longest floats and counts": {
			write:     func() ([]byte, error) { return AppendSystemOneResult(nil, &longest) },
			wantAlloc: 1,
		},
		"success: the upstream RESULT": {
			write:     func() ([]byte, error) { return AppendSystemOneResult(nil, &upstream) },
			wantAlloc: 1,
		},
		"success: model cards": {
			write:     func() ([]byte, error) { return AppendModelList(nil, &cards) },
			wantAlloc: 1,
		},
		"success: answers": {
			write:     func() ([]byte, error) { return AppendAnswers(nil, &longest.Answers) },
			wantAlloc: 1,
		},
		"success: one answer": {
			write:     func() ([]byte, error) { return AppendAnswer(nil, &longest.Answers.entries[2].Answer) },
			wantAlloc: 1,
		},
		"success: one card": {
			write:     func() ([]byte, error) { return AppendModelCard(nil, &cards.Models[1]) },
			wantAlloc: 1,
		},
		"success: the longest usage": {
			write:     func() ([]byte, error) { return AppendUsage(nil, &longest.Usage), nil },
			wantAlloc: 1,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := tt.write(); err != nil {
				t.Fatalf("write: %v", err)
			}
			if raceEnabled() {
				return // -race adds allocations of its own
			}
			if n := testing.AllocsPerRun(100, func() { _, _ = tt.write() }); n != tt.wantAlloc {
				t.Errorf("allocations = %v, want %v", n, tt.wantAlloc)
			}
		})
	}

	t.Run("success: strings with escapes outgrow the estimate and are written in full", func(t *testing.T) {
		r := SystemOneResult{Model: string(make([]byte, 64))} // 64 NULs, 6 bytes each
		got, err := AppendSystemOneResult(nil, &r)
		if err != nil {
			t.Fatalf("AppendSystemOneResult: %v", err)
		}
		if want := len(`{"model":""`) + 64*len(`\u0000`) + len(`,"usage":{"input_tokens":null,"output_tokens":null},"answers":{}}`); len(got) != want {
			t.Errorf("len = %d, want %d: %s", len(got), want, got)
		}
	})
}
