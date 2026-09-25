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
	"slices"
	"strconv"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// TestPreparedTables checks what Prepare records besides the bytes: the
// names in order, each choice's labels, each score's levels (text as given,
// structured levels as their compact bytes inside the prepared object) and
// the decoder's level hint.
func TestPreparedTables(t *testing.T) {
	twelve := make([]Content, 12)
	for i := range twelve {
		twelve[i] = Text(strconv.Itoa(i))
	}
	tests := map[string]struct {
		qs          *Questions
		wantNames   []string
		wantEntries []wire.PreparedQuestion
		wantHint    int
	}{
		"success: one question of each form": {
			qs: NewQuestions().
				Noul("billing", Noul{Instructions: Text("Billing?")}).
				Choice("tone", Choice{Options: Options{{"calm", Text("neutral")}, {Label: "angry"}}}).
				Score("urgency", Score{Levels: []Content{Text("can wait"), JSON([]byte(`{ "when" : "today" }`))}}).
				Raw("spam", RawQuestion{Type: "choice", Fields: map[string]any{"criteria": map[string]any{"yes": nil}}}),
			wantNames: []string{"billing", "tone", "urgency", "spam"},
			wantEntries: []wire.PreparedQuestion{
				{Name: "billing", Kind: wire.KindNoul},
				{Name: "tone", Kind: wire.KindChoice, Options: []string{"calm", "angry"}},
				{Name: "urgency", Kind: wire.KindScore, Levels: []wire.Content{{Text: "can wait"}, {JSON: []byte(`{"when":"today"}`)}}},
				// A raw question has no tables, whatever its type.
				{Name: "spam", Kind: wire.KindChoice},
			},
			wantHint: 2,
		},
		"success: the hint is capped": {
			qs:        NewQuestions().Score("a", Score{Levels: twelve[:3]}).Score("b", Score{Levels: twelve}),
			wantNames: []string{"a", "b"},
			wantEntries: func() []wire.PreparedQuestion {
				levels := make([]wire.Content, len(twelve))
				for i := range twelve {
					levels[i] = wire.Content{Text: strconv.Itoa(i)}
				}
				return []wire.PreparedQuestion{{Name: "a", Kind: wire.KindScore, Levels: levels[:3]}, {Name: "b", Kind: wire.KindScore, Levels: levels}}
			}(),
			wantHint: wire.MaxLevelHint,
		},
		"success: many distinct options": {
			qs: NewQuestions().Choice("tone", Choice{Options: func() Options {
				opts := make(Options, repeatScanLimit+2)
				for i := range opts {
					opts[i] = Option{Label: "o" + strconv.Itoa(i)}
				}
				return opts
			}()}),
			wantNames: []string{"tone"},
			wantEntries: []wire.PreparedQuestion{{Name: "tone", Kind: wire.KindChoice, Options: func() []string {
				labels := make([]string, repeatScanLimit+2)
				for i := range labels {
					labels[i] = "o" + strconv.Itoa(i)
				}
				return labels
			}()}},
		},
		"success: past the scan limit names are indexed": {
			qs: func() *Questions {
				qs := NewQuestions()
				for i := range repeatScanLimit + 2 {
					qs.Noul("q"+strconv.Itoa(i), Noul{})
				}
				return qs
			}(),
			wantNames: func() []string {
				var names []string
				for i := range repeatScanLimit + 2 {
					names = append(names, "q"+strconv.Itoa(i))
				}
				return names
			}(),
			wantEntries: func() []wire.PreparedQuestion {
				var entries []wire.PreparedQuestion
				for i := range repeatScanLimit + 2 {
					entries = append(entries, wire.PreparedQuestion{Name: "q" + strconv.Itoa(i), Kind: wire.KindNoul})
				}
				return entries
			}(),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p, err := tt.qs.Prepare()
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if got := p.Len(); got != len(tt.wantNames) {
				t.Errorf("Len() = %d, want %d", got, len(tt.wantNames))
			}
			if diff := gocmp.Diff(tt.wantNames, slices.Collect(p.Names())); diff != "" {
				t.Errorf("Names() mismatch (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(tt.wantEntries, p.w.Entries()); diff != "" {
				t.Errorf("tables mismatch (-want +got):\n%s", diff)
			}
			if p.w.LevelHint != tt.wantHint {
				t.Errorf("LevelHint = %d, want %d", p.w.LevelHint, tt.wantHint)
			}
			for _, e := range p.w.Entries() {
				if _, ok := p.w.Lookup(e.Name); !ok {
					t.Errorf("Lookup(%q) found nothing", e.Name)
				}
				for i, lv := range e.Levels {
					if !lv.IsJSON() {
						continue
					}
					at := bytes.Index(p.w.Questions, lv.JSON)
					if at < 0 || &p.w.Questions[at] != &lv.JSON[0] {
						t.Errorf("%s level %d JSON %q is not a view of the prepared object", e.Name, i, lv.JSON)
					}
				}
			}
		})
	}
}

// TestPreparedNamesStopsEarly checks that Names honours a consumer that
// stops.
func TestPreparedNamesStopsEarly(t *testing.T) {
	tests := map[string]struct {
		stopAfter int
		want      []string
	}{
		"success: stop after the first": {stopAfter: 1, want: []string{"a"}},
		"success: stop after two":       {stopAfter: 2, want: []string{"a", "b"}},
	}
	p, err := NewQuestions().Noul("a", Noul{}).Noul("b", Noul{}).Noul("c", Noul{}).Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var got []string
			for n := range p.Names() {
				got = append(got, n)
				if len(got) == tt.stopAfter {
					break
				}
			}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("names mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestPreparedIsIndependentOfQuestions checks that a prepared set does not
// change when the questions and values it was built from change afterwards,
// and that preparing the same set twice gives equal sets.
func TestPreparedIsIndependentOfQuestions(t *testing.T) {
	tests := map[string]struct {
		mutate func(opts Options, levels []Content, fields map[string]any, qs *Questions)
	}{
		"success: option labels change": {
			mutate: func(opts Options, _ []Content, _ map[string]any, _ *Questions) { opts[0].Label = "changed" },
		},
		"success: levels change": {
			mutate: func(_ Options, levels []Content, _ map[string]any, _ *Questions) { levels[0] = Text("changed") },
		},
		"success: raw fields change": {
			mutate: func(_ Options, _ []Content, fields map[string]any, _ *Questions) { fields["weight"] = 99 },
		},
		"success: more questions are added": {
			mutate: func(_ Options, _ []Content, _ map[string]any, qs *Questions) { qs.Noul("later", Noul{}) },
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			opts := Options{{Label: "calm"}, {Label: "angry"}}
			levels := []Content{Text("low"), JSON([]byte(`{"x":1}`))}
			fields := map[string]any{"weight": 3}
			qs := NewQuestions().Choice("tone", Choice{Options: opts}).Score("urgency", Score{Levels: levels}).Raw("raw", RawQuestion{Type: "future", Fields: fields})
			p, err := qs.Prepare()
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			wantBytes := string(p.w.Questions)
			wantEntries := slices.Clone(p.w.Entries())

			tt.mutate(opts, levels, fields, qs)

			if got := string(p.w.Questions); got != wantBytes {
				t.Errorf("questions changed to %s, want %s", got, wantBytes)
			}
			if diff := gocmp.Diff(wantEntries, p.w.Entries()); diff != "" {
				t.Errorf("tables changed (-want +got):\n%s", diff)
			}
			again, err := qs.Prepare()
			if err != nil {
				t.Fatalf("second Prepare: %v", err)
			}
			if string(again.w.Questions) == wantBytes {
				t.Errorf("second Prepare after the change = %s, want the change to show", again.w.Questions)
			}
		})
	}
}
