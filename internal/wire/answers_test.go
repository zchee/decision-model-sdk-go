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
	"strconv"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

func TestParseKind(t *testing.T) {
	tests := map[string]struct {
		typ  string
		want Kind
	}{
		"success: noul":                    {typ: "noul", want: KindNoul},
		"success: choice":                  {typ: "choice", want: KindChoice},
		"success: score":                   {typ: "score", want: KindScore},
		"unknown: a future type":           {typ: "ranking", want: KindUnknown},
		"unknown: empty":                   {typ: "", want: KindUnknown},
		"unknown: case differs":            {typ: "Noul", want: KindUnknown},
		"unknown: surrounding whitespace":  {typ: " noul", want: KindUnknown},
		"unknown: the zero kind's name":    {typ: "unknown", want: KindUnknown},
		"unknown: prefix of a known type":  {typ: "sco", want: KindUnknown},
		"unknown: known type with a tail":  {typ: "scores", want: KindUnknown},
		"unknown: NUL inside a known name": {typ: "no\x00ul", want: KindUnknown},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := ParseKind(tt.typ); got != tt.want {
				t.Errorf("ParseKind(%q) = %v, want %v", tt.typ, got, tt.want)
			}
		})
	}
}

func TestKindString(t *testing.T) {
	tests := map[string]struct {
		kind Kind
		want string
	}{
		"success: noul":          {kind: KindNoul, want: "noul"},
		"success: choice":        {kind: KindChoice, want: "choice"},
		"success: score":         {kind: KindScore, want: "score"},
		"unknown: zero value":    {kind: KindUnknown, want: "unknown"},
		"unknown: out of range":  {kind: Kind(200), want: "unknown"},
		"unknown: one past last": {kind: KindScore + 1, want: "unknown"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := tt.kind.String()
			if got != tt.want {
				t.Fatalf("Kind(%d).String() = %q, want %q", tt.kind, got, tt.want)
			}
			// Every modelled kind survives the round trip through its wire
			// name; the unknown ones parse back to the zero value.
			want := tt.kind
			if tt.want == "unknown" {
				want = KindUnknown
			}
			if back := ParseKind(got); back != want {
				t.Errorf("ParseKind(%q) = %v, want %v", got, back, want)
			}
		})
	}
}

// put is one call to Answers.Put in a test script.
type put struct {
	name   string
	answer Answer
}

func noul(p float64) Answer { return Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: p}} }

func unknown() Answer { return Answer{Kind: KindUnknown} }

// numbered returns n puts of noul answers named q0, q1, ..., each valued by
// its index, so a test can tell which put a stored value came from.
func numbered(n int) []put {
	puts := make([]put, n)
	for i := range n {
		puts[i] = put{name: "q" + strconv.Itoa(i), answer: noul(float64(i))}
	}
	return puts
}

func entriesOf(puts ...put) []AnswerEntry {
	entries := make([]AnswerEntry, len(puts))
	for i, p := range puts {
		entries[i] = AnswerEntry{Name: p.name, Answer: p.answer}
	}
	return entries
}

func TestAnswersPut(t *testing.T) {
	many := numbered(linearLimit + 4)
	manyRepeated := append(numbered(linearLimit+4), put{name: "q2", answer: noul(0.5)})
	wantManyRepeated := entriesOf(numbered(linearLimit + 4)...)
	wantManyRepeated[2].Answer = noul(0.5)

	tests := map[string]struct {
		puts      []put
		want      []AnswerEntry
		wantIndex bool
	}{
		"success: empty set": {
			puts: nil,
			want: nil,
		},
		"success: distinct names keep insertion order": {
			puts: []put{{"billing", noul(0.9)}, {"spam", noul(0.1)}, {"urgent", noul(0.4)}},
			want: entriesOf(put{"billing", noul(0.9)}, put{"spam", noul(0.1)}, put{"urgent", noul(0.4)}),
		},
		"success: a repeated name keeps its first position and takes the last value": {
			puts: []put{{"a", noul(0.1)}, {"b", noul(0.2)}, {"a", noul(0.3)}},
			want: entriesOf(put{"a", noul(0.3)}, put{"b", noul(0.2)}),
		},
		"success: a name repeated three times keeps the third value": {
			puts: []put{{"a", noul(0.1)}, {"a", noul(0.2)}, {"a", noul(0.3)}},
			want: entriesOf(put{"a", noul(0.3)}),
		},
		"success: a repeat may change the kind": {
			puts: []put{
				{"a", noul(0.1)},
				{"a", Answer{Kind: KindChoice, Choice: ChoiceAnswer{Choice: "x", Confidence: 1}}},
			},
			want: entriesOf(put{"a", Answer{Kind: KindChoice, Choice: ChoiceAnswer{Choice: "x", Confidence: 1}}}),
		},
		"success: exactly linearLimit names stay on the scan path": {
			puts: numbered(linearLimit),
			want: entriesOf(numbered(linearLimit)...),
		},
		"success: past linearLimit the index takes over": {
			puts:      many,
			want:      entriesOf(many...),
			wantIndex: true,
		},
		"success: a repeat past linearLimit updates in place through the index": {
			puts:      manyRepeated,
			want:      wantManyRepeated,
			wantIndex: true,
		},
		"success: the empty name is a name like any other": {
			puts: []put{{"", noul(0.1)}, {"x", noul(0.2)}, {"", noul(0.3)}},
			want: entriesOf(put{"", noul(0.3)}, put{"x", noul(0.2)}),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var s Answers
			for _, p := range tt.puts {
				s.Put(p.name, p.answer)
			}
			if diff := gocmp.Diff(tt.want, s.Entries()); diff != "" {
				t.Fatalf("Entries() mismatch (-want +got):\n%s", diff)
			}
			if s.Len() != len(tt.want) {
				t.Errorf("Len() = %d, want %d", s.Len(), len(tt.want))
			}
			if (s.index != nil) != tt.wantIndex {
				t.Errorf("index built = %t, want %t", s.index != nil, tt.wantIndex)
			}
			for _, e := range tt.want {
				got, ok := s.Get(e.Name)
				if !ok {
					t.Errorf("Get(%q) not found, want %+v", e.Name, e.Answer)
				} else if diff := gocmp.Diff(e.Answer, got); diff != "" {
					t.Errorf("Get(%q) mismatch (-want +got):\n%s", e.Name, diff)
				}
			}
			if got, ok := s.Get("never-put"); ok {
				t.Errorf("Get(%q) = %+v, true; want not found", "never-put", got)
			}
		})
	}
}

func TestAnswersDropUnknown(t *testing.T) {
	manyWithUnknown := numbered(linearLimit + 4)
	manyWithUnknown[3].answer = unknown()
	manyWithUnknown[7].answer = unknown()
	var wantMany []put
	for i, p := range numbered(linearLimit + 4) {
		if i != 3 && i != 7 {
			wantMany = append(wantMany, p)
		}
	}

	tests := map[string]struct {
		puts      []put
		want      []AnswerEntry
		wantIndex bool
	}{
		"success: nothing to drop": {
			puts: []put{{"a", noul(0.1)}, {"b", noul(0.2)}},
			want: entriesOf(put{"a", noul(0.1)}, put{"b", noul(0.2)}),
		},
		"success: an unknown answer is dropped and the order of the rest kept": {
			puts: []put{{"a", noul(0.1)}, {"future", unknown()}, {"b", noul(0.2)}},
			want: entriesOf(put{"a", noul(0.1)}, put{"b", noul(0.2)}),
		},
		"success: unknown first, known last keeps the first position": {
			puts: []put{{"a", unknown()}, {"b", noul(0.2)}, {"a", noul(0.3)}},
			want: entriesOf(put{"a", noul(0.3)}, put{"b", noul(0.2)}),
		},
		"success: known first, unknown last drops the name": {
			puts: []put{{"a", noul(0.1)}, {"b", noul(0.2)}, {"a", unknown()}},
			want: entriesOf(put{"b", noul(0.2)}),
		},
		"success: every answer unknown leaves an empty set": {
			puts: []put{{"a", unknown()}, {"b", unknown()}},
			want: []AnswerEntry{},
		},
		"success: dropping past linearLimit rebuilds the index": {
			puts:      manyWithUnknown,
			want:      entriesOf(wantMany...),
			wantIndex: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var s Answers
			for _, p := range tt.puts {
				s.Put(p.name, p.answer)
			}
			s.DropUnknown()
			if diff := gocmp.Diff(tt.want, s.Entries()); diff != "" {
				t.Fatalf("Entries() after DropUnknown mismatch (-want +got):\n%s", diff)
			}
			if (s.index != nil) != tt.wantIndex {
				t.Errorf("index built = %t, want %t", s.index != nil, tt.wantIndex)
			}
			// Positions must still resolve after the shift: every kept name
			// finds its own value, and every dropped name is gone.
			kept := map[string]bool{}
			for _, e := range tt.want {
				kept[e.Name] = true
				got, ok := s.Get(e.Name)
				if !ok {
					t.Errorf("Get(%q) not found after DropUnknown, want %+v", e.Name, e.Answer)
				} else if diff := gocmp.Diff(e.Answer, got); diff != "" {
					t.Errorf("Get(%q) mismatch (-want +got):\n%s", e.Name, diff)
				}
			}
			for _, p := range tt.puts {
				if kept[p.name] {
					continue
				}
				if got, ok := s.Get(p.name); ok {
					t.Errorf("Get(%q) = %+v, true after DropUnknown; want not found", p.name, got)
				}
			}
			// A later Put appends after the kept entries.
			s.Put("late", noul(1))
			if last := s.Entries()[s.Len()-1]; last.Name != "late" {
				t.Errorf("last entry after Put = %q, want %q", last.Name, "late")
			}
		})
	}
}

func TestAnswersReset(t *testing.T) {
	tests := map[string]struct {
		before []put
		after  []put
	}{
		"success: reset below linearLimit": {
			before: []put{{"a", noul(0.1)}, {"b", noul(0.2)}},
			after:  []put{{"b", noul(0.9)}},
		},
		"success: reset past linearLimit keeps the index usable": {
			before: numbered(linearLimit + 4),
			after:  []put{{"q5", noul(0.9)}, {"new", noul(0.8)}, {"q5", noul(0.7)}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var s Answers
			for _, p := range tt.before {
				s.Put(p.name, p.answer)
			}
			capBefore := cap(s.entries)
			s.Reset()
			if s.Len() != 0 {
				t.Fatalf("Len() after Reset = %d, want 0", s.Len())
			}
			if cap(s.entries) != capBefore {
				t.Errorf("cap after Reset = %d, want the storage kept (%d)", cap(s.entries), capBefore)
			}
			for _, p := range tt.before {
				if _, ok := s.Get(p.name); ok {
					t.Errorf("Get(%q) found an answer after Reset", p.name)
				}
			}
			var want Answers
			for _, p := range tt.after {
				s.Put(p.name, p.answer)
				want.Put(p.name, p.answer)
			}
			if diff := gocmp.Diff(want.Entries(), s.Entries()); diff != "" {
				t.Errorf("Entries() after Reset and Put mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAnswersGrow(t *testing.T) {
	tests := map[string]struct {
		grow    int
		puts    int
		wantCap int // lower bound on cap after Grow
	}{
		"success: grow for the exact number of puts": {grow: 5, puts: 5, wantCap: 5},
		"success: grow zero is a no-op":              {grow: 0, puts: 0, wantCap: 0},
		"success: negative grow is a no-op":          {grow: -3, puts: 0, wantCap: 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var s Answers
			s.Grow(tt.grow)
			if cap(s.entries) < tt.wantCap {
				t.Fatalf("cap after Grow(%d) = %d, want >= %d", tt.grow, cap(s.entries), tt.wantCap)
			}
			capAfterGrow := cap(s.entries)
			for i := range tt.puts {
				s.Put("q"+strconv.Itoa(i), noul(0))
			}
			if cap(s.entries) != capAfterGrow {
				t.Errorf("Put reallocated after Grow(%d): cap %d -> %d", tt.grow, capAfterGrow, cap(s.entries))
			}
		})
	}
}

func TestChoiceAnswerProbability(t *testing.T) {
	answer := ChoiceAnswer{
		Choice:     "angry",
		Confidence: 0.9,
		Probabilities: []LabelProbability{
			{Label: "angry", Probability: 0.8},
			{Label: "calm", Probability: 0.1},
			{Label: "", Probability: 0.05},
		},
	}
	tests := map[string]struct {
		answer ChoiceAnswer
		label  string
		want   float64
		wantOK bool
	}{
		"success: first label":           {answer: answer, label: "angry", want: 0.8, wantOK: true},
		"success: later label":           {answer: answer, label: "calm", want: 0.1, wantOK: true},
		"success: the empty label":       {answer: answer, label: "", want: 0.05, wantOK: true},
		"missing: label not listed":      {answer: answer, label: "excited", wantOK: false},
		"missing: case differs":          {answer: answer, label: "Angry", wantOK: false},
		"missing: no probabilities":      {answer: ChoiceAnswer{Choice: "a"}, label: "a", wantOK: false},
		"missing: the chosen label only": {answer: ChoiceAnswer{Choice: "a", Probabilities: []LabelProbability{{"b", 1}}}, label: "a", wantOK: false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := tt.answer.Probability(tt.label)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("Probability(%q) = %v, %t; want %v, %t", tt.label, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestScoreAnswerLookups(t *testing.T) {
	answer := ScoreAnswer{
		Score:      1.7,
		Confidence: 0.9,
		// Out of level order on purpose: lookups must not assume a sort.
		Legend: []LegendEntry{
			{Level: 2, Description: Content{Text: "today"}},
			{Level: 0, Description: Content{Text: "can wait"}},
			{Level: 1, Description: Content{JSON: []byte(`{"when":"this week"}`)}},
		},
		Probabilities: []LevelProbability{
			{Level: 1, Probability: 0.1},
			{Level: 2, Probability: 0.8},
			{Level: 0, Probability: 0.1},
		},
	}
	tests := map[string]struct {
		level           uint32
		wantDescription Content
		wantDescOK      bool
		wantProbability float64
		wantProbOK      bool
	}{
		"success: level 0 text": {
			level: 0, wantDescription: Content{Text: "can wait"}, wantDescOK: true,
			wantProbability: 0.1, wantProbOK: true,
		},
		"success: level 1 structured": {
			level: 1, wantDescription: Content{JSON: []byte(`{"when":"this week"}`)}, wantDescOK: true,
			wantProbability: 0.1, wantProbOK: true,
		},
		"success: level 2 text": {
			level: 2, wantDescription: Content{Text: "today"}, wantDescOK: true,
			wantProbability: 0.8, wantProbOK: true,
		},
		"missing: level past the legend": {level: 3},
		"missing: largest level":         {level: ^uint32(0)},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			description, ok := answer.Description(tt.level)
			if ok != tt.wantDescOK || !description.Equal(tt.wantDescription) {
				t.Errorf("Description(%d) = %+v, %t; want %+v, %t", tt.level, description, ok, tt.wantDescription, tt.wantDescOK)
			}
			probability, ok := answer.Probability(tt.level)
			if ok != tt.wantProbOK || probability != tt.wantProbability {
				t.Errorf("Probability(%d) = %v, %t; want %v, %t", tt.level, probability, ok, tt.wantProbability, tt.wantProbOK)
			}
		})
	}
}

// TestAnswersGrowInto checks that an empty set takes a spare array with room
// for n entries, and grows its own otherwise: either way the set has room
// for n more entries, and the entries Put stores land in spare's array
// exactly when it was taken (W5.3, the call's inline answers); a set of no
// entries stays nil, as the decode without a spare leaves it.
func TestAnswersGrowInto(t *testing.T) {
	tests := map[string]struct {
		spareLen int
		spareCap int
		putFirst bool
		n        int
		wantUsed bool
	}{
		"success: room for every entry":                       {spareCap: 3, n: 3, wantUsed: true},
		"success: more room than needed":                      {spareCap: 4, n: 2, wantUsed: true},
		"success: a spare with a length, its capacity counts": {spareLen: 2, spareCap: 3, n: 3, wantUsed: true},
		"success: too little room, own array":                 {spareCap: 2, n: 3},
		"success: no spare":                                   {n: 3},
		"success: a set with entries, own array":              {spareCap: 8, putFirst: true, n: 2},
		"success: no entries, the set stays nil":              {spareCap: 4, n: 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			spare := make([]AnswerEntry, tt.spareLen, tt.spareCap)
			var s Answers
			if tt.putFirst {
				s.Put("first", Answer{Kind: KindNoul})
			}
			s.GrowInto(spare, tt.n)
			if room := cap(s.Entries()) - s.Len(); room < tt.n {
				t.Errorf("room for %d more entries after GrowInto(%d), want at least %d", room, tt.n, tt.n)
			}
			for i := range tt.n {
				s.Put("q"+strconv.Itoa(i), Answer{Kind: KindNoul, Noul: NoulAnswer{Noul: float64(i)}})
			}
			if tt.n == 0 && !tt.putFirst && s.Entries() != nil {
				t.Errorf("Entries = %#v after GrowInto(spare, 0), want nil as Grow(0) leaves it", s.Entries())
			}
			used := tt.spareCap > 0 && s.Len() > 0 && &s.Entries()[0] == &spare[:1][0]
			if used != tt.wantUsed {
				t.Errorf("the entries are in spare's array: %t, want %t", used, tt.wantUsed)
			}
			want := tt.n
			if tt.putFirst {
				want++
			}
			if s.Len() != want {
				t.Errorf("Len = %d, want %d", s.Len(), want)
			}
		})
	}
}
