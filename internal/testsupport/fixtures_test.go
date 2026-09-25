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

package testsupport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	gocmp "github.com/google/go-cmp/cmp"
)

// fixtureClass says what a fixture is for.
type fixtureClass int

const (
	// classValid: a body the SDK must accept, as Python 0.7.1 does.
	classValid fixtureClass = iota
	// classDeviation: a body where the Go port deliberately differs from
	// Python (plan Appendix B); named deviation-*.json.
	classDeviation
	// classMalformed: a body the SDK must reject with
	// *ResponseValidationError, as Python does; named malformed-*.json.
	classMalformed
)

// fixtureSpec is one row of the fixture manifest: its class and a check that
// the file still has the property it exists for.
type fixtureSpec struct {
	class fixtureClass
	check func(raw []byte) error
}

// resultJSON is the upstream RESULT body every trailing-data fixture extends.
const resultJSON = `{"model":"jev-latest","usage":{"input_tokens":12,"output_tokens":3},"answers":{"spam":{"type":"noul","noul":0.98},"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"ok","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}}}}`

// duplicatesLastWins is duplicates.json with every repeated member resolved
// as Python 0.7.1 resolves it: the last one wins at every level, and a
// repeated object replaces the earlier one whole (usage loses output_tokens).
const duplicatesLastWins = `{"model":"jev-latest","usage":{"input_tokens":12},"answers":{"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},"spam":{"type":"noul","noul":0.98},"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"fine","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}},"risk":{"type":"score","score":0,"confidence":1,"legend":{"0":{"summary":"low"}},"probabilities":{"0":1}}}}`

// fixtureManifest lists every file under testdata. testdata/README.md
// documents the same rows for people.
var fixtureManifest = map[string]fixtureSpec{
	// Ported byte-exact from the upstream Python tests.
	"result.json": {classValid, func(raw []byte) error {
		if string(raw) != resultJSON {
			return errors.New("not the upstream RESULT bytes")
		}
		return wantAnswers(raw, "quality", "spam", "tone")
	}},
	"models.json": {classValid, func(raw []byte) error {
		if string(raw) != `{"models":[{"name":"jev-latest","description":"Fast model","release_date":"2026-08-01"}]}` {
			return errors.New("not the upstream {\"models\": [CARD]} bytes")
		}
		return nil
	}},
	"structured-legend.json": {classValid, func(raw []byte) error {
		b, err := decodeBody(raw)
		if err != nil {
			return err
		}
		legend, _ := answerField(b, "risk", "legend").(map[string]any)
		if _, ok := legend["0"].(map[string]any); !ok {
			return errors.New("answers.risk.legend.0 is not an object")
		}
		return schemaOK(raw)
	}},
	"unknown-answer-type.json": {classValid, func(raw []byte) error {
		b, err := decodeBody(raw)
		if err != nil {
			return err
		}
		if answerField(b, "mystery", "type") != "aurora" {
			return errors.New("answers.mystery.type is not the unknown kind aurora")
		}
		return schemaOK(raw)
	}},
	// Copied from the Rust port's fuzz corpus.
	"score-flood-mini.json": {classValid, func(raw []byte) error {
		b, err := decodeBody(raw)
		if err != nil {
			return err
		}
		// Eight score answers with an empty legend, eight with one level.
		levels := map[int]int{}
		answers, _ := b["answers"].(map[string]any)
		for name := range answers {
			legend, _ := answerField(b, name, "legend").(map[string]any)
			if answerField(b, name, "type") != "score" {
				return fmt.Errorf("answer %q is not a score answer", name)
			}
			levels[len(legend)]++
		}
		if diff := gocmp.Diff(map[int]int{0: 8, 1: 8}, levels); diff != "" {
			return fmt.Errorf("answers by legend size (-want +got):\n%s", diff)
		}
		return schemaOK(raw)
	}},
	// Authored.
	"result-20.json": {classValid, func(raw []byte) error {
		b, err := decodeBody(raw)
		if err != nil {
			return err
		}
		kinds := map[string]int{}
		answers, _ := b["answers"].(map[string]any)
		for name := range answers {
			kind, _ := answerField(b, name, "type").(string)
			kinds[kind]++
		}
		if diff := gocmp.Diff(map[string]int{"noul": 7, "choice": 7, "score": 6}, kinds); diff != "" {
			return fmt.Errorf("answer kinds (-want +got):\n%s", diff)
		}
		return schemaOK(raw)
	}},
	"type-last.json": {classValid, func(raw []byte) error {
		if err := typeLast(raw); err != nil {
			return err
		}
		return sameJSON(raw, []byte(resultJSON))
	}},
	"escaped-names.json": {classValid, func(raw []byte) error {
		for _, esc := range []string{u(`@U00e9`), `\"`, `\\`, `\n`, u(`@Ud83c@Udf0d`), `\/`} {
			if !bytes.Contains(raw, []byte(esc)) {
				return fmt.Errorf("the escape %s is missing", esc)
			}
		}
		if err := wantAnswers(raw, "back\\slash", "globe \U0001F30D", "new\nline", "quote\"d", "sl/ash", "sp\U000000e9cial"); err != nil {
			return err
		}
		return schemaOK(raw)
	}},
	"escaped-member-names.json": {classValid, func(raw []byte) error {
		for _, esc := range []string{u(`"@U0061nswers"`), u(`"@U0074ype"`), u(`"l@U0065gend"`), u(`"summ@U0061ry"`), u(`"@U0030"`)} {
			if !bytes.Contains(raw, []byte(esc)) {
				return fmt.Errorf("the escaped member name %s is missing", esc)
			}
		}
		want := `{"model":"jev-latest","usage":{"input_tokens":12,"output_tokens":3},"answers":{"spam":{"type":"noul","noul":0.98},"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},"risk":{"type":"score","score":0,"confidence":1,"legend":{"0":{"summary":"duplicated","examples":["charged \"twice\""]}},"probabilities":{"0":1}}}}`
		return sameJSON(raw, []byte(want))
	}},
	"structured-legend-flood-1k.json":  {classValid, floodCheck(1000)},
	"structured-legend-flood-10k.json": {classValid, floodCheck(10000)},
	"no-answers.json": {classValid, func(raw []byte) error {
		b, err := decodeBody(raw)
		if err != nil {
			return err
		}
		if _, ok := b["answers"]; ok {
			return errors.New("answers is present")
		}
		return schemaOK(raw)
	}},
	"duplicates.json": {classValid, func(raw []byte) error {
		objs, err := objectMembers(raw)
		if err != nil {
			return err
		}
		var dups []string
		var answers [][]string // the member names of each top-level answers object
		for _, o := range objs {
			seen := make(map[string]bool, len(o.names))
			for _, name := range o.names {
				if seen[name] {
					dups = append(dups, joinPath(o.path, name))
				}
				seen[name] = true
			}
			if o.path == "answers" {
				answers = append(answers, o.names)
			}
		}
		wantDups := []string{
			"model", "answers", "usage",
			"answers.tone",
			"answers.spam.type", "answers.spam.noul",
			"answers.tone.confidence", "answers.tone.probabilities",
			"answers.tone.probabilities.friendly",
			"answers.quality.legend", "answers.quality.legend.1",
			"answers.risk.legend",
		}
		if diff := gocmp.Diff(wantDups, dups); diff != "" {
			return fmt.Errorf("repeated members (-want +got):\n%s", diff)
		}
		// The superseded answers object holds an answer that disappears, an
		// invalid one and an unknown kind; the last one repeats tone, which
		// keeps its first position.
		wantAnswers := [][]string{{"gone", "broken", "mystery"}, {"tone", "spam", "tone", "quality", "risk"}}
		if diff := gocmp.Diff(wantAnswers, answers); diff != "" {
			return fmt.Errorf("answer names per answers object (-want +got):\n%s", diff)
		}
		// The walker unescapes member names, so the spelling of the two
		// answers members is pinned on the bytes: the last one carries the
		// escape, so a decoder comparing raw names keeps the wrong one.
		plain, escaped := []byte(`"answers":{`), []byte(u(`"@U0061nswers":{`))
		if bytes.Count(raw, plain) != 1 || bytes.Count(raw, escaped) != 1 || bytes.Index(raw, plain) > bytes.Index(raw, escaped) {
			return fmt.Errorf("want one plain %s followed by one escaped %s", plain, escaped)
		}
		pinned := map[string]string{
			// Each superseded member stays invalid or unknown, so a decoder
			// that validates or warns before the last one wins shows it.
			`"broken":{"type":"noul"}`:                    "the superseded broken is not a noul without its noul",
			`"mystery":{"type":"aurora"}`:                 "the superseded mystery is not of the unknown kind aurora",
			`"tone":{"type":"choice","choice":"hostile"}`: "the first tone is not a choice without confidence and probabilities",
			// A first-wins decoder would read spam as a choice without its
			// members.
			`"spam":{"type":"choice","noul":0.2,"type":"noul","noul":0.98}`: "answers.spam does not change its type from choice to noul",
			// Both directions of a legend change: structured to text in
			// quality, text to structured in risk.
			`"legend":{"0":{"summary":"stale"}},"legend":{"0":"bad",`: "answers.quality does not replace a structured legend with text levels",
			`"legend":{"0":"low"},"legend":{"0":{"summary":"low"}}`:   "answers.risk does not replace a text legend with a structured one",
		}
		for text, fault := range pinned {
			if n := bytes.Count(raw, []byte(text)); n != 1 {
				return fmt.Errorf("%s: %s appears %d times, want once", fault, text, n)
			}
		}
		if err := sameJSON(raw, []byte(duplicatesLastWins)); err != nil {
			return err
		}
		return schemaOK(raw)
	}},
	"parity-big-exp-unknown.json": {classValid, func(raw []byte) error {
		b, err := decodeBody(raw)
		if err != nil {
			return err
		}
		meta, _ := b["meta"].(map[string]any)
		if meta["cost"] != json.Number("1e400") {
			return errors.New("meta.cost is not 1e400")
		}
		return schemaOK(raw)
	}},
	"deviation-big-exp-noul.json": {classDeviation, func(raw []byte) error {
		b, err := decodeBody(raw)
		if err != nil {
			return err
		}
		if answerField(b, "spam", "noul") != json.Number("1e400") {
			return errors.New("answers.spam.noul is not 1e400")
		}
		return schemaOK(raw)
	}},
	"deviation-lone-surrogate.json": {classDeviation, func(raw []byte) error {
		if bytes.Count(raw, []byte(`\ud800"`)) != 2 {
			return errors.New(`want two lone \ud800 escapes, one in a text level and one in a structured level`)
		}
		return schemaOK(raw)
	}},

	// One way to be invalid each.
	"malformed-empty.json": {classMalformed, func(raw []byte) error {
		if len(raw) != 0 {
			return errors.New("not empty")
		}
		return nil
	}},
	"malformed-whitespace.json": {classMalformed, func(raw []byte) error {
		if len(bytes.Trim(raw, " \t\n\r")) != 0 {
			return errors.New("holds a byte other than the four JSON whitespace characters")
		}
		for _, ws := range []byte(" \t\n\r") {
			if bytes.IndexByte(raw, ws) < 0 {
				return fmt.Errorf("the JSON whitespace character %q is missing", ws)
			}
		}
		return nil
	}},
	"malformed-truncated.json": {classMalformed, func(raw []byte) error {
		if len(raw) == 0 || len(raw) >= len(resultJSON) || !strings.HasPrefix(resultJSON, string(raw)) {
			return errors.New("not a strict prefix of result.json")
		}
		return invalidJSON(raw)
	}},
	"malformed-trailing-garbage.json":  {classMalformed, trailing(" x")},
	"malformed-trailing-value.json":    {classMalformed, trailing(`{"model":"again"}`)},
	"malformed-trailing-nbsp.json":     {classMalformed, trailing("\U000000a0")},
	"malformed-trailing-formfeed.json": {classMalformed, trailing("\f")},
	"malformed-root-array.json": {classMalformed, func(raw []byte) error {
		if string(raw) != "["+resultJSON+"]" {
			return errors.New("not result.json inside an array")
		}
		return nil
	}},
	// Each fault twice, in a string value and in a member name: a decoder
	// that checks only one of the two accepts one file of each pair.
	"malformed-invalid-utf8.json":       {classMalformed, badByte(0xff, "\"caf\xff\",", "e")},
	"malformed-invalid-utf8-key.json":   {classMalformed, badByte(0xff, "\"caf\xff\":", "e")},
	"malformed-control-char.json":       {classMalformed, badByte(0x01, "\"a\x01b\"}", " ")},
	"malformed-control-char-key.json":   {classMalformed, badByte(0x01, "\"a\x01b\":", " ")},
	"malformed-invalid-escape.json":     {classMalformed, replaceOnce(`\q`, `q`)},
	"malformed-bad-literal.json":        {classMalformed, replaceOnce(`:tru}`, `:true}`)},
	"malformed-double-comma.json":       {classMalformed, replaceOnce(`[1,,2]`, `[1,2]`)},
	"malformed-leading-zero.json":       {classMalformed, replaceOnce(`:01}`, `:1}`)},
	"malformed-trailing-comma.json":     {classMalformed, replaceOnce(`[1,]`, `[1]`)},
	"malformed-big-exp.json":            {classMalformed, schemaFault(`"input_tokens":1e400`, `"input_tokens":1`)},
	"malformed-usage-type.json":         {classMalformed, schemaFault(`"input_tokens":"12"`, `"input_tokens":12`)},
	"malformed-missing-model.json":      {classMalformed, schemaFault(`{"usage"`, `{"model":"jev-latest","usage"`)},
	"malformed-missing-usage.json":      {classMalformed, schemaFault(`"jev-latest",`, `"jev-latest","usage":{},`)},
	"malformed-answers-not-object.json": {classMalformed, schemaFault(`"answers":[]`, `"answers":{}`)},
}

// TestFixtureManifest checks that testdata holds exactly the manifest's
// files and that each still has the property it exists for.
func TestFixtureManifest(t *testing.T) {
	onDisk := FixtureNames(t, "*.json")
	if diff := gocmp.Diff(slices.Sorted(maps.Keys(fixtureManifest)), onDisk); diff != "" {
		t.Fatalf("testdata/*.json and the manifest differ (-manifest +disk):\n%s", diff)
	}
	for name, spec := range fixtureManifest {
		t.Run(name, func(t *testing.T) {
			prefix := map[fixtureClass]string{classDeviation: "deviation-", classMalformed: "malformed-"}[spec.class]
			if prefix != "" && !strings.HasPrefix(name, prefix) {
				t.Errorf("a class-%d fixture must be named %s*", spec.class, prefix)
			}
			if err := spec.check(Fixture(t, name)); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		})
	}
}

// TestFixtureNames covers the glob helper on the malformed set, which later
// waves loop over.
func TestFixtureNames(t *testing.T) {
	var want []string
	for name, spec := range fixtureManifest {
		if spec.class == classMalformed {
			want = append(want, name)
		}
	}
	slices.Sort(want)
	if diff := gocmp.Diff(want, FixtureNames(t, "malformed-*.json")); diff != "" {
		t.Errorf("FixtureNames(malformed-*.json) (-want +got):\n%s", diff)
	}
	if _, err := globFixtures("no-such-*.json"); err == nil {
		t.Errorf("globFixtures(no-such-*.json) = nil error, want a failure for an empty match")
	}
	if _, err := globFixtures("[bad"); err == nil {
		t.Errorf("globFixtures([bad) = nil error, want a pattern error")
	}
}

// TestFixtureLoader covers the cache, the copy semantics and name checks.
func TestFixtureLoader(t *testing.T) {
	first := Fixture(t, "result.json")
	first[0] = 'X'
	if got := Fixture(t, "result.json"); got[0] != '{' {
		t.Fatalf("a caller's change to Fixture's slice reached the cache")
	}
	if a, b := FixtureString(t, "result.json"), FixtureString(t, "result.json"); a != b || a != resultJSON {
		t.Fatalf("FixtureString returned different content")
	}
	// Every refused name fails the name check, never the file system, so a
	// case cannot pass because a file happens to be absent.
	tests := map[string]struct {
		name    string
		wantErr error
	}{
		"success: top-level file":                {name: "models.json"},
		"error: parent directory":                {name: "../go.mod", wantErr: errFixtureName},
		"error: absolute path":                   {name: "/etc/hosts", wantErr: errFixtureName},
		"error: backslash separator is refused":  {name: `sub\..\..\go.mod`, wantErr: errFixtureName},
		"error: backslash in a file name":        {name: `result\.json`, wantErr: errFixtureName},
		"error: the directory itself":            {name: ".", wantErr: errFixtureName},
		"error: empty name":                      {name: "", wantErr: errFixtureName},
		"error: trailing slash in dir":           {name: "result.json/", wantErr: errFixtureName},
		"error: a valid name for a missing file": {name: "no-such-fixture.json", wantErr: fs.ErrNotExist},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := readFixture(tt.name)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("readFixture(%q) error = %v, want %v", tt.name, err, tt.wantErr)
			}
		})
	}
}

// decodeBody decodes a JSON object with numbers kept as json.Number.
func decodeBody(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var b map[string]any
	if err := dec.Decode(&b); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("data after the root value")
	}
	return b, nil
}

// answerField returns answers.<name>.<field> of a decoded body.
func answerField(b map[string]any, name, field string) any {
	answers, _ := b["answers"].(map[string]any)
	answer, _ := answers[name].(map[string]any)
	return answer[field]
}

// schemaOK checks the response shape Python 0.7.1 requires: a string model,
// a usage object whose counts are absent, null or non-negative integers, and
// answers (when present) as an object of objects with a string type.
func schemaOK(raw []byte) error {
	if !json.Valid(raw) {
		return errors.New("not valid JSON")
	}
	b, err := decodeBody(raw)
	if err != nil {
		return err
	}
	if _, ok := b["model"].(string); !ok {
		return errors.New("model is missing or not a string")
	}
	usage, ok := b["usage"].(map[string]any)
	if !ok {
		return errors.New("usage is missing or not an object")
	}
	for _, key := range []string{"input_tokens", "output_tokens"} {
		switch v := usage[key].(type) {
		case nil:
		case json.Number:
			if _, err := strconv.ParseUint(v.String(), 10, 64); err != nil {
				return fmt.Errorf("usage.%s = %s is not a count", key, v)
			}
		default:
			return fmt.Errorf("usage.%s is a %T", key, v)
		}
	}
	answers, present := b["answers"]
	if !present {
		return nil
	}
	set, ok := answers.(map[string]any)
	if !ok {
		return errors.New("answers is not an object")
	}
	for name, a := range set {
		if _, ok := a.(map[string]any)["type"].(string); !ok {
			return fmt.Errorf("answers.%s has no string type", name)
		}
	}
	return nil
}

// invalidJSON fails unless raw is not valid JSON.
func invalidJSON(raw []byte) error {
	if json.Valid(raw) {
		return errors.New("the body is valid JSON")
	}
	return nil
}

// repaired checks that raw is invalid and that fixed, raw with its one fault
// removed, is a valid response: so the fault is the only one.
func repaired(raw, fixed []byte) error {
	if schemaOK(raw) == nil {
		return errors.New("the body is a valid response")
	}
	if err := schemaOK(fixed); err != nil {
		return fmt.Errorf("still invalid after removing the fault: %w", err)
	}
	return nil
}

// replaceOnce checks a syntax fault: raw holds old exactly once, is invalid
// JSON, and becomes a valid response with old replaced by fixed.
func replaceOnce(old, fixed string) func([]byte) error {
	return func(raw []byte) error {
		if n := bytes.Count(raw, []byte(old)); n != 1 {
			return fmt.Errorf("%q appears %d times, want once", old, n)
		}
		if err := invalidJSON(raw); err != nil {
			return err
		}
		return repaired(raw, bytes.Replace(raw, []byte(old), []byte(fixed), 1))
	}
}

// badByte checks a fault of one byte, b, inside a string: raw holds b exactly
// once, inside the text at (whose last byte after the closing quote says
// whether b sits in a member name, ':', or in a value), the byte makes raw
// invalid UTF-8 or invalid JSON, and replacing it with fix gives a valid
// response. It does not use repaired: encoding/json accepts invalid UTF-8
// inside strings, so schemaOK calls the faulty body valid.
func badByte(b byte, at, fix string) func([]byte) error {
	return func(raw []byte) error {
		if n := bytes.Count(raw, []byte{b}); n != 1 {
			return fmt.Errorf("byte %#x appears %d times, want once", b, n)
		}
		if strings.IndexByte(at, b) < 0 || !bytes.Contains(raw, []byte(at)) {
			return fmt.Errorf("byte %#x is not inside %q", b, at)
		}
		if utf8.Valid(raw) && json.Valid(raw) {
			return fmt.Errorf("byte %#x leaves the body valid UTF-8 and valid JSON", b)
		}
		fixed := bytes.Replace(raw, []byte{b}, []byte(fix), 1)
		if !utf8.Valid(fixed) {
			return errors.New("invalid UTF-8 other than the one byte")
		}
		if err := schemaOK(fixed); err != nil {
			return fmt.Errorf("still invalid after removing the fault: %w", err)
		}
		return nil
	}
}

// schemaFault checks a schema fault: raw is valid JSON holding old exactly
// once, and becomes a valid response with old replaced by fixed.
func schemaFault(old, fixed string) func([]byte) error {
	return func(raw []byte) error {
		if !json.Valid(raw) {
			return errors.New("not valid JSON; a schema fault needs valid syntax")
		}
		if n := bytes.Count(raw, []byte(old)); n != 1 {
			return fmt.Errorf("%q appears %d times, want once", old, n)
		}
		return repaired(raw, bytes.Replace(raw, []byte(old), []byte(fixed), 1))
	}
}

// trailing checks result.json followed by suffix.
func trailing(suffix string) func([]byte) error {
	return func(raw []byte) error {
		if string(raw) != resultJSON+suffix {
			return fmt.Errorf("not result.json followed by %q", suffix)
		}
		return invalidJSON(raw)
	}
}

// floodCheck checks a committed flood fixture against the generator. Under
// -update, TestStructuredLegendFloodFixtures rewrites the file, perhaps after
// this test read it, so the check takes the generator's output instead:
// `go test -update` without -run then passes once the files are rewritten.
func floodCheck(levels int) func([]byte) error {
	return func(raw []byte) error {
		want := StructuredLegendFlood(levels)
		if *update {
			raw = want
		}
		if !bytes.Equal(raw, want) {
			return fmt.Errorf("differs from StructuredLegendFlood(%d)", levels)
		}
		return schemaOK(raw)
	}
}

// wantAnswers checks the set of answer names.
func wantAnswers(raw []byte, names ...string) error {
	b, err := decodeBody(raw)
	if err != nil {
		return err
	}
	answers, _ := b["answers"].(map[string]any)
	if diff := gocmp.Diff(names, sortedKeys(answers)); diff != "" {
		return fmt.Errorf("answer names (-want +got):\n%s", diff)
	}
	return nil
}

// sameJSON checks that a and b decode to the same value.
func sameJSON(a, b []byte) error {
	va, err := decodeBody(a)
	if err != nil {
		return err
	}
	vb, err := decodeBody(b)
	if err != nil {
		return err
	}
	if diff := gocmp.Diff(vb, va); diff != "" {
		return fmt.Errorf("decoded values differ (-want +got):\n%s", diff)
	}
	return nil
}

// objectNames is one JSON object's member names in wire order, repeats
// included, with the object's path from the root: "" for the root,
// "answers.tone" below it, and "[]" for an array element.
type objectNames struct {
	path  string
	names []string
}

// objectMembers walks raw with encoding/json's tokenizer, which keeps the
// repeated member names that a decode into a map merges, and returns every
// object in the order it opens.
func objectMembers(raw []byte) ([]*objectNames, error) {
	type frame struct {
		obj     *objectNames // nil for an array
		path    string
		wantKey bool   // the object's next token is a member name
		name    string // the member whose value comes next
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var objs []*objectNames
	var stack []*frame
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return objs, nil
		}
		if err != nil {
			return nil, err
		}
		if tok == json.Delim('}') || tok == json.Delim(']') {
			stack = stack[:len(stack)-1]
			continue
		}
		path := ""
		if n := len(stack); n > 0 {
			top := stack[n-1]
			switch {
			case top.obj != nil && top.wantKey:
				top.name, _ = tok.(string)
				top.obj.names = append(top.obj.names, top.name)
				top.wantKey = false
				continue
			case top.obj != nil:
				path = joinPath(top.path, top.name)
				top.wantKey = true
			default:
				path = joinPath(top.path, "[]")
			}
		}
		switch tok {
		case json.Delim('{'):
			o := &objectNames{path: path}
			objs = append(objs, o)
			stack = append(stack, &frame{obj: o, path: path, wantKey: true})
		case json.Delim('['):
			stack = append(stack, &frame{path: path})
		}
	}
}

// joinPath appends name to a dotted path.
func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// typeLast checks that every answer object ends with its type member.
func typeLast(raw []byte) error {
	var root struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	for name, answer := range root.Answers {
		var a struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(answer, &a); err != nil {
			return err
		}
		if !bytes.HasSuffix(answer, []byte(`,"type":"`+a.Type+`"}`)) {
			return fmt.Errorf("answer %q does not end with its type member: %s", name, answer)
		}
	}
	return nil
}

// u turns each "@U" in s into a JSON backslash-u escape, so this file can
// spell escapes without the source itself holding one.
func u(s string) string {
	return strings.ReplaceAll(s, "@U", `\`+"u")
}

// sortedKeys returns m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
