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
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	gocmp "github.com/google/go-cmp/cmp"
)

// formatTag writes s back as a tag, in a canonical form: the keys in the
// order of the grammar's list, every separator a value contains escaped. It
// is the inverse parseTag's round trip is checked against.
func formatTag(s *tagSpec) string {
	var b strings.Builder
	entry := func(key string) {
		if b.Len() > 0 {
			b.WriteByte(';')
		}
		b.WriteString(key)
	}
	text := func(key, value string) {
		if s.keys&tagKeys[key] == 0 {
			return
		}
		entry(key)
		b.WriteByte('=')
		b.WriteString(escapeTag(value, ""))
	}
	text("kind", s.kind)
	text("name", s.name)
	text("instructions", s.instructions)
	text("yes", s.yes)
	text("no", s.no)
	if s.keys&keyOptions != 0 {
		entry("options=")
		for i, o := range s.options {
			if i > 0 {
				b.WriteByte('|')
			}
			b.WriteString(escapeTag(o.label, "|="))
			if o.description != "" {
				b.WriteByte('=')
				b.WriteString(escapeTag(o.description, "|"))
			}
		}
	}
	if s.keys&keyLevels != 0 {
		entry("levels=")
		for i, l := range s.levels {
			if i > 0 {
				b.WriteByte('|')
			}
			b.WriteString(escapeTag(l, "|"))
		}
	}
	if s.optional {
		entry("optional")
	}
	return b.String()
}

// escapeTag escapes in v the backslash, ";" and the bytes of special.
func escapeTag(v, special string) string {
	var b strings.Builder
	for i := range len(v) {
		if c := v[i]; c == '\\' || c == ';' || strings.IndexByte(special, c) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

// tagGrammarSeeds are the corpus seeds: the section 5 tags, the eight AC-F8
// escape cases, the tags of the rejection cases and grammar edges.
var tagGrammarSeeds = []string{
	// Section 5.
	"kind=noul;instructions=Is this about billing, invoices or refunds?;yes=payments or invoices",
	"kind=choice;instructions=What is the tone?;options=calm=neutral or polite|angry",
	"kind=score;instructions=How urgent?;levels=can wait|this week|today",
	"kind=noul;optional;instructions=Spam?",
	// The eight escape cases.
	`kind=choice;options=a\;b=desc|c`,
	`kind=choice;options=a\|b=desc|c`,
	`kind=choice;options=a\=b=desc|c`,
	`kind=choice;options=a\\b=desc|c`,
	`kind=noul;instructions=a\;b`,
	`kind=noul;instructions=a\|b`,
	`kind=noul;instructions=a\=b`,
	`kind=noul;instructions=a\\b`,
	// The rejection cases' tags.
	"kind=noul;name=Spam",
	"kind=choice;options=calm=polite|angry|calm",
	"kind=score;levels=low|high|low",
	"kind=score;instructions=How urgent?",
	"instructions=Spam?",
	"kind=yesno",
	"kind=choice;options=calm",
	"kind=noul;name=model",
	"optional",
	"kind=noul;weight=3",
	`kind=noul;instructions=Spam?\`,
	"kind=noul;options=x",
	// Grammar edges.
	"",
	";",
	"kind=noul;",
	"kind=noul;;name=x",
	"=",
	"kind",
	"optional=true",
	"options=a||b",
	"options==d",
	"options=a=",
	"levels=|",
	`instructions=a\nb`,
	`instructions=a\\;kind=noul`,
	"instructions=\xff",
	"name=請求;instructions=請求の件ですか？",
}

// FuzzTagGrammar checks the tag grammar on any input: parseTag never
// panics; a tag it accepts has no empty values, is valid UTF-8 and survives
// a round trip through formatTag unchanged; a tag it refuses is refused with
// a *tagError, the same one each time. The same tag on a field of each
// answer type and of a string type either asks a question that
// Questions.Prepare accepts or is refused with a *ConfigError that names the
// field.
func FuzzTagGrammar(f *testing.F) {
	for _, seed := range tagGrammarSeeds {
		f.Add(seed)
	}
	fieldTypes := []reflect.Type{noulAnswerType, choiceAnswerType, scoreAnswerType, reflect.TypeFor[string]()}
	f.Fuzz(func(t *testing.T, tag string) {
		spec, err := parseTag(tag)
		spec2, err2 := parseTag(tag)
		if err != nil {
			if _, ok := errors.AsType[*tagError](err); !ok {
				t.Fatalf("parseTag(%q) error is a %T, want *tagError", tag, err)
			}
			if err.Error() == "" {
				t.Fatalf("parseTag(%q) error has an empty message", tag)
			}
			if err2 == nil || err2.Error() != err.Error() {
				t.Fatalf("parseTag(%q) is not deterministic: %v, then %v", tag, err, err2)
			}
		} else {
			if err2 != nil || !gocmp.Equal(spec, spec2, planOptions) {
				t.Fatalf("parseTag(%q) is not deterministic: %+v, then %+v, %v", tag, spec, spec2, err2)
			}
			checkSpec(t, tag, &spec)
			canonical := formatTag(&spec)
			back, err := parseTag(canonical)
			if err != nil {
				t.Fatalf("parseTag(%q) = %+v, whose form %q does not parse: %v", tag, spec, canonical, err)
			}
			if diff := gocmp.Diff(spec, back, planOptions); diff != "" {
				t.Fatalf("round trip of %q through %q (-first +second):\n%s", tag, canonical, diff)
			}
		}

		for _, typ := range fieldTypes {
			field := reflect.StructField{Name: "F", Type: typ, Tag: reflect.StructTag("typesafe:" + strconv.Quote(tag))}
			q, asks, err := planField("T", &field)
			if err != nil {
				ce, ok := errors.AsType[*ConfigError](err)
				if !ok {
					t.Fatalf("planField(%s, %q) error is a %T, want *ConfigError", typ, tag, err)
				}
				if !strings.HasPrefix(ce.Error(), "PreparedFor[T]: field F: ") {
					t.Fatalf("planField(%s, %q) error %q does not name the field", typ, tag, ce.Error())
				}
				continue
			}
			if !asks {
				t.Fatalf("planField(%s, %q) ignored a tagged exported field", typ, tag)
			}
			if typ != noulAnswerType && typ != choiceAnswerType && typ != scoreAnswerType {
				t.Fatalf("planField(%s, %q) accepted a field that is not an answer", typ, tag)
			}
			qs := &Questions{entries: []questionEntry{q.entry}}
			p, err := qs.Prepare()
			if err != nil {
				t.Fatalf("planField(%s, %q) accepted a question Prepare refuses: %v", typ, tag, err)
			}
			if got := p.w.Entries()[0].Kind; got != answerKind(typ) || q.kind != got {
				t.Fatalf("planField(%s, %q) asks a %v question, recorded as %v", typ, tag, got, q.kind)
			}
		}
	})
}

// checkSpec checks what an accepted tag parses to: every given value is
// nonempty valid UTF-8, and the key set says which were given.
func checkSpec(t *testing.T, tag string, s *tagSpec) {
	t.Helper()
	texts := map[tagKey]string{keyKind: s.kind, keyName: s.name, keyInstructions: s.instructions, keyYes: s.yes, keyNo: s.no}
	for key, v := range texts {
		if (s.keys&key != 0) != (v != "") {
			t.Fatalf("parseTag(%q): key %d given %v, value %q", tag, key, s.keys&key != 0, v)
		}
	}
	if (s.keys&keyOptions != 0) != (len(s.options) > 0) || (s.keys&keyLevels != 0) != (len(s.levels) > 0) || (s.keys&keyOptional != 0) != s.optional {
		t.Fatalf("parseTag(%q): keys %b disagree with %+v", tag, s.keys, *s)
	}
	all := []string{s.kind, s.name, s.instructions, s.yes, s.no}
	for _, o := range s.options {
		if o.label == "" {
			t.Fatalf("parseTag(%q): empty option label", tag)
		}
		all = append(all, o.label, o.description)
	}
	for _, l := range s.levels {
		if l == "" {
			t.Fatalf("parseTag(%q): empty level", tag)
		}
		all = append(all, l)
	}
	for _, v := range all {
		if !utf8.ValidString(v) {
			t.Fatalf("parseTag(%q): value %q is not valid UTF-8", tag, v)
		}
	}
}
