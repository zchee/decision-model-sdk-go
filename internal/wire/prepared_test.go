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
	"bytes"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// text and raw build Content for the builder tests.
func text(s string) *Content { return &Content{Text: s} }

func raw(s string) *Content { return &Content{JSON: []byte(s)} }

func TestBuilder(t *testing.T) {
	type choiceOption struct {
		label       string
		description *Content
	}
	tests := map[string]struct {
		build       func(b *Builder) error
		want        string
		wantEntries []PreparedQuestion
		wantHint    int
	}{
		"success: nothing written is an empty object": {
			build:       func(*Builder) error { return nil },
			want:        `{}`,
			wantEntries: []PreparedQuestion{}, // Grow allocated them
		},
		"success: a noul without members": {
			build:       func(b *Builder) error { return b.Noul("q", nil, nil, nil) },
			want:        `{"q":{"type":"noul"}}`,
			wantEntries: []PreparedQuestion{{Name: "q", Kind: KindNoul}},
		},
		"success: a noul with every member": {
			build:       func(b *Builder) error { return b.Noul("q", text("Spam?"), text("Yes"), raw(`{"why": "no"}`)) },
			want:        `{"q":{"type":"noul","instructions":"Spam?","criteria":{"true":"Yes","false":{"why":"no"}}}}`,
			wantEntries: []PreparedQuestion{{Name: "q", Kind: KindNoul}},
		},
		"success: a noul with only the no outcome": {
			build:       func(b *Builder) error { return b.Noul("q", nil, nil, text("No")) },
			want:        `{"q":{"type":"noul","criteria":{"false":"No"}}}`,
			wantEntries: []PreparedQuestion{{Name: "q", Kind: KindNoul}},
		},
		"success: a choice with described and undescribed options": {
			build: func(b *Builder) error {
				opts := []choiceOption{{"calm", text("neutral")}, {"angry", nil}, {"mixed", raw(`["a", "b"]`)}}
				labels := []string{"calm", "angry", "mixed"}
				return b.Choice("tone", text("Tone?"), labels, func(i int) *Content { return opts[i].description })
			},
			want:        `{"tone":{"type":"choice","instructions":"Tone?","criteria":{"calm":"neutral","angry":null,"mixed":["a","b"]}}}`,
			wantEntries: []PreparedQuestion{{Name: "tone", Kind: KindChoice, Options: []string{"calm", "angry", "mixed"}}},
		},
		"success: a choice without options": {
			build: func(b *Builder) error {
				return b.Choice("tone", nil, nil, func(int) *Content { return nil })
			},
			want:        `{"tone":{"type":"choice","criteria":{}}}`,
			wantEntries: []PreparedQuestion{{Name: "tone", Kind: KindChoice}},
		},
		"success: a score with text and JSON levels": {
			build: func(b *Builder) error {
				return b.Score("urgency", raw(`[ ]`), []Content{{Text: "low"}, {JSON: []byte(` { "extra" : null } `)}, {Text: "high"}})
			},
			want: `{"urgency":{"type":"score","instructions":[],"criteria":["low",{"extra":null},"high"]}}`,
			wantEntries: []PreparedQuestion{{Name: "urgency", Kind: KindScore, Levels: []Content{
				{Text: "low"}, {JSON: []byte(`{"extra":null}`)}, {Text: "high"},
			}}},
			wantHint: 3,
		},
		"success: raw fields in sorted order after type": {
			build: func(b *Builder) error {
				return b.Raw("r", "future", map[string]any{"weight": 3, "criteria": map[string]any{"b": nil, "a": "x"}, "instructions": "Spam?"}, nil)
			},
			want:        `{"r":{"type":"future","criteria":{"a":"x","b":null},"instructions":"Spam?","weight":3}}`,
			wantEntries: []PreparedQuestion{{Name: "r", Kind: KindUnknown}},
		},
		"success: a raw question of a known type takes its kind": {
			build:       func(b *Builder) error { return b.Raw("r", "score", map[string]any{"criteria": []string{"good"}}, nil) },
			want:        `{"r":{"type":"score","criteria":["good"]}}`,
			wantEntries: []PreparedQuestion{{Name: "r", Kind: KindScore}},
		},
		"success: several questions and a capped level hint": {
			build: func(b *Builder) error {
				levels := make([]Content, 12)
				for i := range levels {
					levels[i] = Content{Text: strconv.Itoa(i)}
				}
				if err := b.Noul("a", nil, nil, nil); err != nil {
					return err
				}
				if err := b.Score("b", nil, levels[:3]); err != nil {
					return err
				}
				return b.Score("c", nil, levels)
			},
			want: `{"a":{"type":"noul"},"b":{"type":"score","criteria":["0","1","2"]},"c":{"type":"score","criteria":["0","1","2","3","4","5","6","7","8","9","10","11"]}}`,
			wantEntries: func() []PreparedQuestion {
				levels := make([]Content, 12)
				for i := range levels {
					levels[i] = Content{Text: strconv.Itoa(i)}
				}
				return []PreparedQuestion{{Name: "a", Kind: KindNoul}, {Name: "b", Kind: KindScore, Levels: levels[:3]}, {Name: "c", Kind: KindScore, Levels: levels}}
			}(),
			wantHint: MaxLevelHint,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var b Builder
			b.Grow(4, 64)
			if err := tt.build(&b); err != nil {
				t.Fatalf("build: %v", err)
			}
			var p Prepared
			if err := b.Finish(&p); err != nil {
				t.Fatalf("Finish: %v", err)
			}
			if string(p.Questions) != tt.want {
				t.Errorf("Questions =\n%s\nwant\n%s", p.Questions, tt.want)
			}
			if diff := gocmp.Diff(tt.wantEntries, p.Entries()); diff != "" {
				t.Errorf("Entries() mismatch (-want +got):\n%s", diff)
			}
			if p.LevelHint != tt.wantHint {
				t.Errorf("LevelHint = %d, want %d", p.LevelHint, tt.wantHint)
			}
			if cap(p.Questions) != len(p.Questions) {
				t.Errorf("cap(Questions) = %d, want len %d: spare capacity would be shared", cap(p.Questions), len(p.Questions))
			}
			// Every JSON level is a view of the finished object, capped so
			// that nothing can append into it.
			for _, e := range p.Entries() {
				for i, lv := range e.Levels {
					if !lv.IsJSON() {
						continue
					}
					at := bytes.Index(p.Questions, lv.JSON)
					if at < 0 || &p.Questions[at] != &lv.JSON[0] {
						t.Errorf("%s level %d JSON %q does not alias Questions", e.Name, i, lv.JSON)
					}
					if cap(lv.JSON) != len(lv.JSON) {
						t.Errorf("%s level %d JSON cap = %d, want %d", e.Name, i, cap(lv.JSON), len(lv.JSON))
					}
				}
			}
		})
	}
}

func TestBuilderErrors(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	never := func(int) *Content { return nil }
	tests := map[string]struct {
		build      func(b *Builder) error
		wantMember string // "" for an error that is not a *MemberError
		wantErr    error  // matched with errors.Is, when set
		wantMsg    string // the whole Error() text
	}{
		"error: a question name that is not UTF-8": {
			build:   func(b *Builder) error { return b.Noul("\xff", nil, nil, nil) },
			wantErr: ErrInvalidUTF8,
			wantMsg: "question name: string is not valid UTF-8",
		},
		"error: a raw type that is not UTF-8": {
			build:      func(b *Builder) error { return b.Raw("r", "\xff", nil, nil) },
			wantMember: "type", wantErr: ErrInvalidUTF8,
			wantMsg: "type: string is not valid UTF-8",
		},
		"error: noul instructions": {
			build:      func(b *Builder) error { return b.Noul("q", raw(`"x"`), nil, nil) },
			wantMember: "instructions", wantErr: ErrContentShape,
			wantMsg: "instructions: JSON content must be an object or an array",
		},
		"error: the yes outcome": {
			build:      func(b *Builder) error { return b.Noul("q", nil, text("\xff"), nil) },
			wantMember: "criteria.true", wantErr: ErrInvalidUTF8,
			wantMsg: "criteria.true: string is not valid UTF-8",
		},
		"error: the no outcome after the yes outcome": {
			build:      func(b *Builder) error { return b.Noul("q", nil, text("y"), raw(`[`)) },
			wantMember: "criteria.false",
			wantMsg:    "criteria.false: invalid JSON at byte 1: unexpected end of input, want a value",
		},
		"error: choice instructions": {
			build:      func(b *Builder) error { return b.Choice("q", raw(`1`), nil, never) },
			wantMember: "instructions", wantErr: ErrContentShape,
			wantMsg: "instructions: JSON content must be an object or an array",
		},
		"error: an option label that is not UTF-8": {
			build:      func(b *Builder) error { return b.Choice("q", nil, []string{"ok", "\xff"}, never) },
			wantMember: "criteria.\xff", wantErr: ErrInvalidUTF8,
			wantMsg: `"criteria.\xff": string is not valid UTF-8`,
		},
		"error: an option description": {
			build: func(b *Builder) error {
				return b.Choice("q", nil, []string{"calm"}, func(int) *Content { return raw(`{"a" 1}`) })
			},
			wantMember: "criteria.calm",
			wantMsg:    "criteria.calm: invalid JSON at byte 5: want ':' after a member name",
		},
		"error: score instructions": {
			build:      func(b *Builder) error { return b.Score("q", text("\xff"), []Content{{Text: "a"}}) },
			wantMember: "instructions", wantErr: ErrInvalidUTF8,
			wantMsg: "instructions: string is not valid UTF-8",
		},
		"error: a score level": {
			build:      func(b *Builder) error { return b.Score("q", nil, []Content{{Text: "a"}, {JSON: []byte(`true`)}}) },
			wantMember: "criteria[1]", wantErr: ErrContentShape,
			wantMsg: "criteria[1]: JSON content must be an object or an array",
		},
		"error: a raw field named type": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"type": "noul"}, nil) },
			wantMember: "type", wantErr: errTypeField,
			wantMsg: `type: "type" is written from the question's type, not from its fields`,
		},
		"error: a raw field name that is not UTF-8": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"\xff": 1}, nil) },
			wantMember: "\xff", wantErr: ErrInvalidUTF8,
			wantMsg: `"\xff": string is not valid UTF-8`,
		},
		"error: a string value that is not UTF-8": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"s": "\xff"}, nil) },
			wantMember: "s", wantErr: ErrInvalidUTF8,
			wantMsg: "s: string is not valid UTF-8",
		},
		"error: an unsupported type without a leaf": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"w": leafType{}}, nil) },
			wantMember: "w", wantErr: ErrUnsupportedValue,
			wantMsg: "w: unsupported value of type wire.leafType",
		},
		"error: a value the leaf does not know": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"w": make(chan int)}, testLeaf) },
			wantMember: "w", wantErr: ErrUnsupportedValue,
			wantMsg: "w: unsupported value of type chan int",
		},
		"error: the leaf's own failure": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"w": leafType{s: "fail"}}, testLeaf) },
			wantMember: "w",
			wantMsg:    "w: leaf refused",
		},
		"error: NaN": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"w": math.NaN()}, nil) },
			wantMember: "w", wantErr: ErrUnsupportedValue,
			wantMsg: "w: unsupported value: NaN is not a JSON number",
		},
		"error: infinity as float32": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"w": float32(math.Inf(-1))}, nil) },
			wantMember: "w", wantErr: ErrUnsupportedValue,
			wantMsg: "w: unsupported value: -Inf is not a JSON number",
		},
		"error: the path of a nested value": {
			build: func(b *Builder) error {
				return b.Raw("r", "noul", map[string]any{"criteria": map[string]any{"a": []any{1, struct{}{}}}}, nil)
			},
			wantMember: "criteria.a[1]", wantErr: ErrUnsupportedValue,
			wantMsg: "criteria.a[1]: unsupported value of type struct {}",
		},
		"error: a nested map key that is not UTF-8": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"m": map[string]any{"\xff": 1}}, nil) },
			wantMember: "m.\xff", wantErr: ErrInvalidUTF8,
			wantMsg: `"m.\xff": string is not valid UTF-8`,
		},
		"error: a []string element": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"l": []string{"a", "\xff"}}, nil) },
			wantMember: "l[1]", wantErr: ErrInvalidUTF8,
			wantMsg: "l[1]: string is not valid UTF-8",
		},
		"error: a map[string]string key": {
			build: func(b *Builder) error {
				return b.Raw("r", "noul", map[string]any{"m": map[string]string{"\xff": "v"}}, nil)
			},
			wantMember: "m.\xff", wantErr: ErrInvalidUTF8,
			wantMsg: `"m.\xff": string is not valid UTF-8`,
		},
		"error: a map[string]string value": {
			build: func(b *Builder) error {
				return b.Raw("r", "noul", map[string]any{"m": map[string]string{"k": "\xff"}}, nil)
			},
			wantMember: "m.k", wantErr: ErrInvalidUTF8,
			wantMsg: "m.k: string is not valid UTF-8",
		},
		"error: a cycle": {
			build:      func(b *Builder) error { return b.Raw("r", "noul", map[string]any{"c": cycle}, nil) },
			wantMember: "c" + strings.Repeat(".self", maxValueDepth+1), wantErr: ErrUnsupportedValue,
			wantMsg: "c" + strings.Repeat(".self", maxValueDepth+1) + ": unsupported value: nested more than 1000 levels deep (a cycle?)",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var b Builder
			err := tt.build(&b)
			if err == nil {
				t.Fatal("build succeeded, want an error")
			}
			if err.Error() != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", err, tt.wantMsg)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("errors.Is(%v, %v) = false", err, tt.wantErr)
			}
			var me *MemberError
			if isMember := errors.As(err, &me); isMember != (tt.wantMember != "") {
				t.Fatalf("errors.As(*MemberError) = %t, want %t", isMember, tt.wantMember != "")
			}
			if me != nil && me.Member != tt.wantMember {
				t.Errorf("Member = %q, want %q", me.Member, tt.wantMember)
			}
		})
	}
}

// TestBuilderFinishRejectsRepeatedNames checks that Finish fails as
// NewPrepared does on a repeated name, on both lookup paths, and leaves the
// destination as it was.
func TestBuilderFinishRejectsRepeatedNames(t *testing.T) {
	tests := map[string]struct {
		n        int // questions written, all named "q<i>" except the last
		wantName string
	}{
		"error: a repeat in a small set":   {n: 3, wantName: "q0"},
		"error: a repeat past linearLimit": {n: linearLimit + 3, wantName: "q0"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var b Builder
			for i := range tt.n {
				qname := "q" + strconv.Itoa(i)
				if i == tt.n-1 {
					qname = tt.wantName
				}
				if err := b.Noul(qname, nil, nil, nil); err != nil {
					t.Fatalf("Noul: %v", err)
				}
			}
			p := Prepared{LevelHint: -1}
			err := b.Finish(&p)
			if !errors.Is(err, ErrDuplicateQuestion) {
				t.Fatalf("Finish error = %v, want ErrDuplicateQuestion", err)
			}
			if p.LevelHint != -1 || p.Questions != nil || p.Entries() != nil {
				t.Errorf("Finish changed its destination on failure: %+v", p)
			}
		})
	}
}

func TestNewPreparedLevelHint(t *testing.T) {
	levels := func(n int) []Content { return make([]Content, n) }
	tests := map[string]struct {
		entries []PreparedQuestion
		want    int
	}{
		"success: no score":              {entries: []PreparedQuestion{{Name: "a", Kind: KindNoul}}, want: 0},
		"success: a score with 3 levels": {entries: []PreparedQuestion{{Name: "a", Kind: KindScore, Levels: levels(3)}}, want: 3},
		"success: the largest score wins": {
			entries: []PreparedQuestion{{Name: "a", Levels: levels(5)}, {Name: "b", Levels: levels(2)}},
			want:    5,
		},
		"success: capped at MaxLevelHint": {entries: []PreparedQuestion{{Name: "a", Levels: levels(MaxLevelHint + 1)}}, want: MaxLevelHint},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p, err := NewPrepared([]byte(`{}`), tt.entries)
			if err != nil {
				t.Fatalf("NewPrepared: %v", err)
			}
			if p.LevelHint != tt.want {
				t.Errorf("LevelHint = %d, want %d", p.LevelHint, tt.want)
			}
		})
	}
}
