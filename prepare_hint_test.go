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
	"slices"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// TestSizeHintCoversRawValues checks that the size hint of a raw question
// counts a field that is a long string, RawJSON or Content (W5.3's P3), so
// that Prepare's buffer holds the question without growing: the hint is at
// least the prepared length. A field of any other kind still counts 32
// bytes, which a long one outgrows.
func TestSizeHintCoversRawValues(t *testing.T) {
	long := strings.Repeat("x", 4096)
	tests := map[string]struct {
		value any
	}{
		"success: a string":      {value: long},
		"success: RawJSON":       {value: RawJSON(`["` + long + `"]`)},
		"success: Content text":  {value: Text(long)},
		"success: Content JSON":  {value: JSON(RawJSON(`{"k":"` + long + `"}`))},
		"success: pretty values": {value: RawJSON("[\n  \"" + long + "\"\n]")},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			qs := NewQuestions().Raw("q", RawQuestion{Type: "future", Fields: map[string]any{"v": tt.value, "w": tt.value}})
			p, err := qs.Prepare()
			if err != nil {
				t.Fatal(err)
			}
			if hint := qs.sizeHint().bytes; hint < len(p.wirePrepared().Questions) {
				t.Errorf("sizeHint = %d, below the prepared length %d: the buffer grows", hint, len(p.wirePrepared().Questions))
			}
		})
	}
}

// TestPrepareTablesOwnArrays checks the tables Prepare cuts from one array
// per kind (W5.3's P5): each choice's Options and each score's Levels hold
// exactly that question's values, capped at their own length so that an
// append to one copies instead of writing into the next, and changing the
// questions afterwards changes no table (R45).
func TestPrepareTablesOwnArrays(t *testing.T) {
	qs := NewQuestions()
	for i := range 3 {
		n := strconv.Itoa(i)
		qs = qs.Choice("c"+n, Choice{Options: []Option{{Label: "a" + n}, {Label: "b" + n}, {Label: "c" + n}}})
		qs = qs.Score("s"+n, Score{Levels: []Content{Text("low" + n), JSON(RawJSON(`{"l":` + n + `}`))}})
	}
	p, err := qs.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	entries := p.wirePrepared().Entries()
	for i := range 3 {
		n := strconv.Itoa(i)
		c, s := entries[2*i], entries[2*i+1]
		if diff := gocmp.Diff([]string{"a" + n, "b" + n, "c" + n}, c.Options); diff != "" {
			t.Errorf("%s Options (-want +got):\n%s", c.Name, diff)
		}
		if cap(c.Options) != len(c.Options) || cap(s.Levels) != len(s.Levels) {
			t.Errorf("%s cap %d len %d, %s cap %d len %d: a table must be capped at its length", c.Name, cap(c.Options), len(c.Options), s.Name, cap(s.Levels), len(s.Levels))
		}
		if got := s.Levels[0].Text; got != "low"+n || len(s.Levels) != 2 || string(s.Levels[1].JSON) != `{"l":`+n+`}` {
			t.Errorf("%s Levels = %+v, want low%s and {\"l\":%s}", s.Name, s.Levels, n, n)
		}
	}
	before := slices.Clone(entries[2].Options)
	_ = append(entries[0].Options, "appended")
	_ = append(entries[1].Levels, entries[1].Levels[0])
	qs.entries[2].choice.Options[0].Label = "changed"
	if diff := gocmp.Diff(before, entries[2].Options); diff != "" {
		t.Errorf("c1's Options changed by an append to c0's or a change to the questions (-before +after):\n%s", diff)
	}
	if got := entries[3].Levels[0].Text; got != "low1" {
		t.Errorf("s1's first level = %q after an append to s0's, want low1", got)
	}
}

// TestCutTables checks cut, which hands out tables from an arena and
// allocates when the arena has not room.
func TestCutTables(t *testing.T) {
	arena := make([]string, 0, 5)
	a := cut(&arena, 2)
	b := cut(&arena, 3)
	c := cut(&arena, 1)
	if len(a) != 2 || cap(a) != 2 || len(b) != 3 || cap(b) != 3 || len(c) != 1 {
		t.Fatalf("lengths/caps a %d/%d b %d/%d c %d: want 2/2 3/3 1", len(a), cap(a), len(b), cap(b), len(c))
	}
	if &a[:1][0] != &arena[:1][0] || &b[0] != &arena[2] {
		t.Error("a and b are not cut from the arena in order")
	}
	if len(arena) != 5 {
		t.Errorf("the arena's length = %d, want 5 after cutting 2 and 3", len(arena))
	}
}
