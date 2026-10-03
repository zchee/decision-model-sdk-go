// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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

package schema

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
)

// The files the tests read, as committed.
const (
	cassetteDir     = "../../testdata/cassettes"
	schemaCasesPath = "../../testdata/python/schema_cases.jsonl"
)

// The Python reference the generated files were made with, and upstream's
// release. A file whose header names anything else fails the test that
// reads it.
const (
	wantPython       = "3.14.3"
	wantPydantic     = "2.13.4"
	wantPydanticCore = "2.46.4"
	wantUpstream     = "0.2.1"
	wantCommit       = "e1d4cc938204b22fc5a3c3aca7044072fe3f712d"
	// wantPythonSDK is the version of typesafe-sdk-python that upstream's
	// release locks.
	wantPythonSDK = "0.7.0"
)

// The text around the schema in the system message of a prompted request
// (_OUTPUT_SCHEMA_INSTRUCTION_TEMPLATE, _client.py:83-87 at v0.2.1).
const (
	schemaInstructionPrefix = "Return one JSON object that matches this schema exactly:\n\n"
	schemaInstructionSuffix = "\n\nDo not include text or Markdown fencing before or after the JSON object."
)

// The two tests of upstream that recorded provider requests
// (tests/test_client_with_live_apis.py).
const (
	referenceTest    = "test_live_responses_match_reference_shape"
	probeTest        = "test_live_models_follow_question_instructions_and_criteria"
	typesafeCassette = "test_live_typesafe_response_matches_reference_shape.json"
)

// probeQuestions are CONTEXT_PROBE_QUESTIONS of upstream's
// tests/test_client_with_live_apis.py:71-86, in the wire form the SDK gives
// a question (type, instructions, criteria). No recording holds them as a
// System One request, so they are written out here.
const probeQuestions = `{` +
	`"instruction_probe":{"type":"choice","instructions":"Return the only marker whose state is ACTIVE.","criteria":{"marker_fen":"The marker_fen catalog entry.","marker_tor":"The marker_tor catalog entry."}},` +
	`"criteria_probe":{"type":"choice","instructions":"Return the correct opaque handling route for the parcel.","criteria":{"route_7q":"Use when the handling class is CLASS_CRYSTAL.","route_2m":"Use when the handling class is CLASS_STEEL."}}}`

// recording names one recorded provider request: the upstream test, the
// answer mode, whether the provider's native structured output was used, and
// the provider.
type recording struct {
	test     string
	mode     Mode
	native   bool
	provider string
}

// file returns the recording's file name. The bracketed id is
// "<answer mode>-<prompted|native>-<provider>".
func (r recording) file() string {
	output := "prompted"
	if r.native {
		output = "native"
	}
	return r.test + "[" + r.mode.String() + "-" + output + "-" + r.provider + "].json"
}

// recordings returns the 24 recorded provider requests: both upstream tests,
// both answer modes, both output modes, three providers.
func recordings() []recording {
	var out []recording
	for _, test := range []string{referenceTest, probeTest} {
		for _, mode := range []Mode{Probabilities, Discrete} {
			for _, native := range []bool{true, false} {
				for _, provider := range []string{"openai", "anthropic", "gemini"} {
					out = append(out, recording{test: test, mode: mode, native: native, provider: provider})
				}
			}
		}
	}
	return out
}

// readFile reads a JSON file of testdata.
func readFile(t testing.TB, path string) jsonx.Node {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonx.Read(data)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return v
}

// at follows a path of member names and element indexes from v.
func at(t testing.TB, v jsonx.Node, path ...any) jsonx.Node {
	t.Helper()
	for _, step := range path {
		switch step := step.(type) {
		case string:
			next, ok := v.Member(step)
			if !ok {
				t.Fatalf("no member %q on the path %v", step, path)
			}
			v = next
		case int:
			if v.Kind() != jsonx.KindArray || step >= v.Len() {
				t.Fatalf("no element %d on the path %v", step, path)
			}
			v = v.Index(step)
		}
	}
	return v
}

// recordedRequest returns the body of the one request a recording holds.
func recordedRequest(t testing.TB, name string) jsonx.Node {
	t.Helper()
	return at(t, readFile(t, filepath.Join(cassetteDir, name)), "interactions", 0, "request", "body")
}

// specOf builds the Spec of the questions an upstream test asks, in mode.
// The reference questions are read from the one recording that holds them
// as a System One request, so they come from upstream's wire form.
func specOf(t testing.TB, test string, mode Mode) *Spec {
	t.Helper()
	var questions jsonx.Node
	if test == referenceTest {
		questions = at(t, recordedRequest(t, typesafeCassette), "questions")
	} else {
		questions = readJSON(t, probeQuestions)
	}
	qs, err := ParseQuestions(questions)
	if err != nil {
		t.Fatalf("ParseQuestions: %v", err)
	}
	spec, err := Build(qs, mode)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return spec
}

// TestRecordingsAreTheCommittedFiles checks that the 24 names the two tests
// below build are, with the one System One recording, exactly the files of
// the directory.
func TestRecordingsAreTheCommittedFiles(t *testing.T) {
	want := []string{typesafeCassette}
	for _, r := range recordings() {
		want = append(want, r.file())
	}
	entries, err := os.ReadDir(cassetteDir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	slices.Sort(want)
	slices.Sort(got)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("recordings mismatch (-named +in the directory):\n%s", diff)
	}
}

// TestNativeSchemaEqualsCassette compares the schema with the one each of
// the 12 native recordings sent to its provider, as a JSON value.
//
// The key order is also compared, to hold the deviation the port records:
// the schema is written in pydantic's order for every provider, while in
// the probabilities-native recordings of OpenAI and Anthropic upstream's
// vendor SDKs had reordered it before sending. Those four differ in order
// and in nothing else; the other eight are equal in order too.
func TestNativeSchemaEqualsCassette(t *testing.T) {
	schemaPath := map[string][]any{
		"openai":    {"text", "format", "schema"},
		"anthropic": {"output_config", "format", "schema"},
		"gemini":    {"response_format", "schema"},
	}
	compared := 0
	for _, r := range recordings() {
		if !r.native {
			continue
		}
		compared++
		t.Run(r.file(), func(t *testing.T) {
			recorded, err := at(t, recordedRequest(t, r.file()), schemaPath[r.provider]...).PydanticJSON()
			if err != nil {
				t.Fatal(err)
			}
			got := specOf(t, r.test, r.mode).Schema()
			equal, err := jsonx.Equal(got, recorded)
			if err != nil {
				t.Fatal(err)
			}
			if !equal {
				t.Errorf("the schema differs from the recorded one as a value\n got: %s\nwant: %s", got, recorded)
			}
			sameOrder, err := jsonx.EqualOrdered(got, recorded)
			if err != nil {
				t.Fatal(err)
			}
			reordered := r.mode == Probabilities && r.provider != "gemini"
			if sameOrder == reordered {
				t.Errorf("the recorded schema has the port's key order: %t; a recording reordered by a vendor SDK: %t", sameOrder, reordered)
			}
		})
	}
	if compared != 12 {
		t.Errorf("%d native recordings compared, want 12", compared)
	}
}

// TestPromptedSchemaBytesEqualCassette compares the schema text with the
// text each of the 12 prompted recordings carries in its system message,
// byte for byte.
func TestPromptedSchemaBytesEqualCassette(t *testing.T) {
	systemPath := map[string][]any{
		"openai":    {"input", 0, "content"},
		"anthropic": {"system"},
		"gemini":    {"system_instruction"},
	}
	compared := 0
	for _, r := range recordings() {
		if r.native {
			continue
		}
		compared++
		t.Run(r.file(), func(t *testing.T) {
			system := at(t, recordedRequest(t, r.file()), systemPath[r.provider]...)
			if system.Kind() != jsonx.KindString {
				t.Fatalf("the system message is not a string")
			}
			_, rest, found := strings.Cut(system.Text(), schemaInstructionPrefix)
			recorded, ok := strings.CutSuffix(rest, schemaInstructionSuffix)
			if !found || !ok {
				t.Fatalf("the system message does not carry the schema instruction: %.200q", system.Text())
			}
			if got := specOf(t, r.test, r.mode).Schema(); string(got) != recorded {
				t.Errorf("the schema text differs from the recorded one\n got: %s\nwant: %s", got, recorded)
			}
		})
	}
	if compared != 12 {
		t.Errorf("%d prompted recordings compared, want 12", compared)
	}
}

// schemaCase is one line of the generated case file.
type schemaCase struct {
	name    string
	request jsonx.Node
	// schema holds the schema text per answer mode of an accepted request.
	schema map[Mode]string
	// refused is the refusal of a request upstream refuses: the exception's
	// class, its message for a ValueError, and for a pydantic
	// ValidationError one [type, loc...] list per error, or none when the
	// file records only their number.
	refused      bool
	refusedError string
	refusedText  string
	refusedLocs  jsonx.Node
}

// readSchemaCases reads the generated case file after checking its header:
// the format, the three reference versions, upstream's release and commit,
// the Python SDK's version, and the two counts.
func readSchemaCases(t testing.TB) []schemaCase {
	t.Helper()
	data, err := os.ReadFile(schemaCasesPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	header := readJSON(t, lines[0])
	text := func(path ...any) string { return at(t, header, path...).Text() }
	if text("format") != "1" || text("generator") != "gen_schema_cases.py" {
		t.Fatalf("%s: format %s by %q; want format 1 by gen_schema_cases.py", schemaCasesPath, text("format"), text("generator"))
	}
	if version, _, _ := strings.Cut(text("python"), " "); version != wantPython {
		t.Fatalf("%s: header python = %q, want CPython %s", schemaCasesPath, text("python"), wantPython)
	}
	for _, check := range []struct {
		path []any
		want string
	}{
		{[]any{"pydantic"}, wantPydantic},
		{[]any{"pydantic_core"}, wantPydanticCore},
		{[]any{"system_one_adapter", "version"}, wantUpstream},
		{[]any{"system_one_adapter", "commit"}, wantCommit},
		{[]any{"typesafe_sdk"}, wantPythonSDK},
	} {
		if got := text(check.path...); got != check.want {
			t.Fatalf("%s: header %v = %q, want %q", schemaCasesPath, check.path, got, check.want)
		}
	}

	var cases []schemaCase
	accepted, refused := 0, 0
	for _, line := range lines[1:] {
		row := readJSON(t, line)
		c := schemaCase{name: at(t, row, "case").Text()}
		request, err := jsonx.Read([]byte(at(t, row, "request").Text()))
		if err != nil {
			t.Fatalf("%s: case %s: the request: %v", schemaCasesPath, c.name, err)
		}
		c.request = request
		if r, ok := row.Member("refused"); ok {
			refused++
			c.refused, c.refusedError = true, at(t, r, "error").Text()
			if m, ok := r.Member("message"); ok {
				c.refusedText = m.Text()
			}
			c.refusedLocs, _ = r.Member("errors")
		} else {
			accepted++
			c.schema = map[Mode]string{
				Probabilities: at(t, row, "schema", "probabilities").Text(),
				Discrete:      at(t, row, "schema", "discrete").Text(),
			}
		}
		cases = append(cases, c)
	}
	if text("accepted") != strconv.Itoa(accepted) || text("refused") != strconv.Itoa(refused) {
		t.Fatalf("%s: the header counts %s accepted and %s refused; the file has %d and %d", schemaCasesPath, text("accepted"), text("refused"), accepted, refused)
	}
	return cases
}

// TestSchemaMatchesUpstreamCases compares the schema with what upstream's
// own code builds for the requests of the generated case file
// (testdata/python/gen_schema_cases.py), where the recordings cannot tell a
// right writer from a wrong one: for every accepted request and each answer
// mode the schema text byte for byte, which is also the native schema as a
// value; for every request upstream refuses, a refusal of ParseQuestions
// with upstream's message, or at the places pydantic names.
//
// The file also holds, per accepted request, the user message upstream
// builds from the state. This package builds neither that nor the system
// message; the test of the code that does reads them from the same file.
//
// Four rules of the writer are invisible to the recordings, and the named
// cases pin them: the key order of a property named properties or default
// (reserved_names), inspect.cleandoc on a probability map's description
// (docstring_cleaning), the sorted order of $defs (many_questions), and the
// replacement of < and of > in the state text, each on its own
// (state_less_than_only, state_greater_than_only), which belongs to the
// user message.
func TestSchemaMatchesUpstreamCases(t *testing.T) {
	cases := readSchemaCases(t)
	names := map[string]bool{}
	accepted, refused := 0, 0
	for _, c := range cases {
		names[c.name] = true
		questions, _ := c.request.Member("questions")
		if c.refused {
			refused++
			t.Run(c.name, func(t *testing.T) { checkRefusedCase(t, c, questions) })
			continue
		}
		accepted++
		t.Run(c.name, func(t *testing.T) {
			qs, err := ParseQuestions(questions)
			if err != nil {
				t.Fatalf("ParseQuestions: %v", err)
			}
			for _, mode := range []Mode{Probabilities, Discrete} {
				spec, err := Build(qs, mode)
				if err != nil {
					t.Fatalf("Build(%v): %v", mode, err)
				}
				got, want := spec.Schema(), c.schema[mode]
				if string(got) != want {
					t.Errorf("%v: the schema text differs from upstream's at byte %d\n got: %s\nwant: %s", mode, firstDifference(string(got), want), got, want)
				}
				if equal, err := jsonx.EqualOrdered(got, []byte(want)); err != nil || !equal {
					t.Errorf("%v: the schema differs from upstream's as a value: %v", mode, err)
				}
			}
		})
	}
	for _, name := range []string{"reserved_names", "docstring_cleaning", "many_questions", "state_less_than_only", "state_greater_than_only"} {
		if !names[name] {
			t.Errorf("%s has no case %s, which pins a rule the recordings do not", schemaCasesPath, name)
		}
	}
	t.Logf("%d requests: %d accepted, each in two answer modes; %d refused", len(cases), accepted, refused)
}

// checkRefusedCase checks that ParseQuestions refuses a request upstream
// refuses: with upstream's message for a ValueError it raises itself; at
// the places a pydantic ValidationError names, when the file lists them.
func checkRefusedCase(t *testing.T, c schemaCase, questions jsonx.Node) {
	t.Helper()
	_, err := ParseQuestions(questions)
	var qe *QuestionError
	if !errors.As(err, &qe) {
		t.Fatalf("ParseQuestions error = %v; upstream refuses the request with %s", err, c.refusedError)
	}
	switch {
	case c.refusedError == "ValueError":
		if qe.Message != c.refusedText {
			t.Errorf("Message = %q, want upstream's %q", qe.Message, c.refusedText)
		}
	case c.refusedLocs.Kind() == jsonx.KindArray:
		var got [][]string
		for _, issue := range qe.Issues {
			got = append(got, issue.Loc)
		}
		if diff := cmp.Diff(pydanticPlaces(c.refusedLocs), got); diff != "" {
			t.Errorf("the refused places mismatch (-pydantic +got):\n%s", diff)
		}
	}
}

// pydanticPlaces turns the errors of a pydantic ValidationError over the
// question collection into the places ParseQuestions names. An error is
// [type, question, tag, member...]: the tag, which is the question's type,
// is dropped, and so is the last element when it names the alternative of
// the content union that failed (a string, an object or an array: three
// errors for one place, kept once). An error of the tag itself is
// [type, question]; ParseQuestions names the type member for a bad type and
// the question for a missing one or for a value that is no object.
func pydanticPlaces(errs jsonx.Node) [][]string {
	var out [][]string
	for i := range errs.Len() {
		e := errs.Index(i)
		kind := e.Index(0).Text()
		loc := []string{e.Index(1).Text()}
		for j := 3; j < e.Len(); j++ {
			loc = append(loc, e.Index(j).Text())
		}
		switch kind {
		case "union_tag_invalid":
			loc = append(loc, "type")
		case "string_type", "dict_type", "is_instance_of":
			if last := loc[len(loc)-1]; last == "str" || strings.HasPrefix(last, "dict[") || strings.HasPrefix(last, "json-or-python[") {
				loc = loc[:len(loc)-1]
			}
		}
		if len(out) == 0 || !slices.Equal(out[len(out)-1], loc) {
			out = append(out, loc)
		}
	}
	return out
}

// firstDifference returns the index of the first byte at which a and b
// differ.
func firstDifference(a, b string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// unsupportedKeywords are the schema keywords upstream removes for the
// providers (_UNSUPPORTED_SCHEMA_KEYWORDS, _schema.py:133).
var unsupportedKeywords = []string{"title", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"}

// TestSchemaDropsUnsupportedKeywords checks that no schema object carries
// one of the five keywords upstream removes, in either answer mode, while a
// question or a label that has such a keyword as its name keeps it: the
// members of "properties" and of "$defs" are names, not keywords.
//
// Upstream removes the bounds for every provider although only one of them
// refuses them; the port does the same, so that its requests equal the
// recorded ones.
func TestSchemaDropsUnsupportedKeywords(t *testing.T) {
	var questions strings.Builder
	questions.WriteString(`{"n":{"type":"noul","instructions":"i","criteria":{"true":"t"}},"s":{"type":"score","criteria":["a","b","c"]}`)
	for _, keyword := range unsupportedKeywords {
		questions.WriteString(`,"` + keyword + `":{"type":"choice","criteria":{"title":"1","minimum":"2","maximum":null,"exclusiveMinimum":"4","exclusiveMaximum":"5"}}`)
	}
	questions.WriteString(`}`)
	qs := mustParse(t, questions.String())

	tests := map[string]struct {
		mode Mode
		// wantNames is how often each keyword is a member of "properties":
		// as a question, and in Probabilities mode as a label of each of
		// the five choice questions.
		wantNames int
	}{
		"success: probabilities": {mode: Probabilities, wantNames: 1 + 5},
		"success: discrete":      {mode: Discrete, wantNames: 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			spec, err := Build(qs, tt.mode)
			if err != nil {
				t.Fatal(err)
			}
			names := map[string]int{}
			checkKeywords(t, readJSON(t, string(spec.Schema())), "$", names)
			for _, keyword := range unsupportedKeywords {
				if names[keyword] != tt.wantNames {
					t.Errorf("%q is a property name %d times, want %d", keyword, names[keyword], tt.wantNames)
				}
			}
		})
	}
}

// checkKeywords walks a schema object: each member is a keyword of the
// closed set the port writes, and the members of "properties" and "$defs"
// are names, counted in names, whose values are schema objects.
func checkKeywords(t *testing.T, schema jsonx.Node, path string, names map[string]int) {
	t.Helper()
	allowed := []string{"$defs", "$ref", "additionalProperties", "description", "enum", "properties", "required", "type"}
	for i := range schema.Len() {
		keyword := schema.Name(i)
		if !slices.Contains(allowed, keyword) {
			t.Errorf("%s: keyword %q; the port writes only %v", path, keyword, allowed)
		}
		if keyword != "properties" && keyword != "$defs" {
			continue
		}
		named := schema.Index(i)
		for j := range named.Len() {
			names[named.Name(j)]++
			checkKeywords(t, named.Index(j), path+"."+keyword+"."+named.Name(j), names)
		}
	}
}

// TestSchemaShape checks one small schema in each answer mode against its
// whole text, so that a reader sees what the writer produces.
func TestSchemaShape(t *testing.T) {
	qs := mustParse(t, `{"ok":{"type":"noul","instructions":"Is it ok?","criteria":{"false":"no"}},`+
		`"default":{"type":"choice","criteria":{"properties":"p","b":null}},`+
		`"level":{"type":"score","instructions":"\tHow good?\n\n","criteria":["bad","good"]}}`)
	tests := map[string]struct {
		mode Mode
		want string
	}{
		"success: probabilities": {
			mode: Probabilities,
			want: `{"$defs":{` +
				`"ProbabilityMap1":{"additionalProperties":false,"description":"Each property maps an option to the probability that it is the best answer.\nQuestion: No additional instructions.",` +
				`"properties":{"properties":{"type":"number","description":"p"},"b":{"description":"No additional instructions.","type":"number"}},"required":["properties","b"],"type":"object"},` +
				`"ProbabilityMap2":{"additionalProperties":false,"description":"Each property maps a rubric level to the probability that the document matches it.\nQuestion:       How good?",` +
				`"properties":{"0":{"description":"bad","type":"number"},"1":{"description":"good","type":"number"}},"required":["0","1"],"type":"object"},` +
				`"TypeSafeAnswers":{"additionalProperties":false,"description":"Exactly one answer per property below. Use these property names verbatim and do not add, rename, or nest them under any other key.",` +
				`"properties":{"ok":{"description":"Probability that the answer is yes or the assertion is true. 0 means no or false, 0.5 means uncertain, and 1 means yes or true.\nQuestion: Is it ok?\nTrue criteria: No additional instructions.\nFalse criteria: no","type":"number"},` +
				`"default":{"$ref":"#/$defs/ProbabilityMap1"},"level":{"$ref":"#/$defs/ProbabilityMap2"}},"required":["ok","default","level"],"type":"object"}},` +
				`"additionalProperties":false,"properties":{"answers":{"$ref":"#/$defs/TypeSafeAnswers"}},"required":["answers"],"type":"object"}`,
		},
		"success: discrete": {
			mode: Discrete,
			want: `{"$defs":{` +
				`"TypeSafeAnswers":{"additionalProperties":false,"description":"Exactly one answer per property below. Use these property names verbatim and do not add, rename, or nest them under any other key.",` +
				`"properties":{"ok":{"description":"Is it ok?\nTrue criteria: No additional instructions.\nFalse criteria: no","type":"boolean"},` +
				`"default":{"enum":["properties","b"],"type":"string","description":"No additional instructions.\nChoice labels, answer with one label:\nproperties = p\nb = No additional instructions."},` +
				`"level":{"description":"\tHow good?\n\n\nScore levels, answer with the integer:\n0 = bad\n1 = good","type":"integer"}},"required":["ok","default","level"],"type":"object"}},` +
				`"additionalProperties":false,"properties":{"answers":{"$ref":"#/$defs/TypeSafeAnswers"}},"required":["answers"],"type":"object"}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			spec, err := Build(qs, tt.mode)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Mode() != tt.mode {
				t.Errorf("Mode() = %v, want %v", spec.Mode(), tt.mode)
			}
			got := spec.Schema()
			if string(got) != tt.want {
				t.Errorf("Schema() differs at byte %d\n got: %s\nwant: %s", firstDifference(string(got), tt.want), got, tt.want)
			}
			// The text is the caller's own: changing it does not change
			// the Spec.
			got[0] = 'X'
			if again := spec.Schema(); string(again) != tt.want {
				t.Errorf("Schema() after the caller changed an earlier result: %.40s", again)
			}
		})
	}
}

// TestBuildRefuses checks what Build refuses.
func TestBuildRefuses(t *testing.T) {
	one := mustParse(t, `{"a":{"type":"noul"}}`)
	tests := map[string]struct {
		questions []Question
		mode      Mode
		want      string
	}{
		"error: no question":            {questions: nil, mode: Discrete, want: "no question to build a schema for"},
		"error: no mode":                {questions: one, mode: 0, want: "unknown answer mode Mode(0)"},
		"error: a mode past the two":    {questions: one, mode: 3, want: "unknown answer mode Mode(3)"},
		"error: a zero Question":        {questions: []Question{one[0], {}}, mode: Discrete, want: "question 1 was not made by ParseQuestions"},
		"error: one name for two":       {questions: []Question{one[0], one[0]}, mode: Probabilities, want: "cannot be written as JSON"},
		"error: one name, another kind": {questions: append(mustParse(t, `{"a":{"type":"score","criteria":["x","y"]}}`), one[0]), mode: Discrete, want: "cannot be written as JSON"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			spec, err := Build(tt.questions, tt.mode)
			if err == nil || !strings.Contains(err.Error(), tt.want) || spec != nil {
				t.Errorf("Build = %v, %v; want nil and an error holding %q", spec, err, tt.want)
			}
		})
	}
}

// TestBuildKeepsItsOwnQuestions checks that a Spec does not change when the
// caller reuses the slice it was built from.
func TestBuildKeepsItsOwnQuestions(t *testing.T) {
	qs := mustParse(t, `{"a":{"type":"noul","instructions":"first"}}`)
	spec, err := Build(qs, Discrete)
	if err != nil {
		t.Fatal(err)
	}
	qs[0] = mustParse(t, `{"b":{"type":"noul"}}`)[0]
	if got := spec.questions[0].ID(); got != "a" {
		t.Errorf("the Spec's first question is %q after the caller changed its slice, want a", got)
	}
}

// TestModeString checks the spelling of each mode and of a value that is
// none.
func TestModeString(t *testing.T) {
	tests := map[string]struct {
		mode Mode
		want string
	}{
		"success: probabilities": {mode: Probabilities, want: "probabilities"},
		"success: discrete":      {mode: Discrete, want: "discrete"},
		"error: no mode":         {mode: 0, want: "Mode(0)"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.mode.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCleandoc checks the port of inspect.cleandoc against values computed
// with CPython 3.14.3's inspect.cleandoc; the generated cases hold the same
// rule inside whole schemas.
func TestCleandoc(t *testing.T) {
	tests := map[string]struct {
		doc  string
		want string
	}{
		"success: one line":                          {doc: "text", want: "text"},
		"success: empty":                             {doc: "", want: ""},
		"success: spaces alone":                      {doc: "   ", want: ""},
		"success: leading spaces of the first line":  {doc: "  a\nb", want: "a\nb"},
		"success: the common margin":                 {doc: "a\n    b\n      c\n    d", want: "a\nb\n  c\nd"},
		"success: a blank line shorter than margin":  {doc: "a\n    b\n \n    c", want: "a\nb\n\nc"},
		"success: empty lines at both ends":          {doc: "\n\na\n\n\n", want: "a"},
		"success: a line of spaces at the end stays": {doc: "a\n   ", want: "a\n   "},
		"success: a tab is eight columns":            {doc: "\tx\n  y\n\n", want: "x\ny"},
		"success: a tab after text":                  {doc: "ab\tc\n\td", want: "ab      c\nd"},
		"success: a carriage return ends a column":   {doc: "a\r\n\tb\r\n", want: "a\r\nb\r"},
		// A carriage return by itself starts the columns again: c is at
		// column 0, so the tab is seven spaces. Computed, like the other
		// rows, with CPython 3.14.3's inspect.cleandoc.
		"success: a carriage return alone ends a column": {doc: "ab\rc\td", want: "ab\rc       d"},
		"success: wide characters are one column":        {doc: "日本\tx", want: "日本      x"},
		"success: other white space is content":          {doc: "a\n\u00a0b\n  c", want: "a\n\u00a0b\n  c"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := cleandoc(tt.doc); got != tt.want {
				t.Errorf("cleandoc(%q) = %q, want %q", tt.doc, got, tt.want)
			}
		})
	}
}

// FuzzSchemaWriter checks the schema writer on any question set
// ParseQuestions accepts, in each answer mode: Build does not fail and does
// not panic; the schema text is JSON, and it is the text pydantic-core's
// to_json writes for its own value, byte for byte, so the writer's string
// escapes are pydantic-core's; and a second Build writes the same bytes.
//
// The seeds are the questions of the generated case file and of the
// recordings.
func FuzzSchemaWriter(f *testing.F) {
	for _, c := range readSchemaCases(f) {
		questions, _ := c.request.Member("questions")
		text, err := questions.PydanticJSON()
		if err != nil {
			continue
		}
		f.Add(text)
	}
	f.Add([]byte(probeQuestions))
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := jsonx.Read(data)
		if err != nil {
			return
		}
		qs, err := ParseQuestions(doc)
		if err != nil {
			return
		}
		for _, mode := range []Mode{Probabilities, Discrete} {
			spec, err := Build(qs, mode)
			if err != nil {
				t.Fatalf("Build(%v) refuses questions ParseQuestions accepted: %v", mode, err)
			}
			text := spec.Schema()
			again, err := jsonx.PydanticJSON(text)
			if err != nil {
				t.Fatalf("%v: the schema text is not JSON: %v\n%.300q", mode, err, text)
			}
			if !bytes.Equal(again, text) {
				t.Fatalf("%v: the schema text is not what pydantic-core writes for its value, at byte %d\n got: %.300q\nwant: %.300q", mode, firstDifference(string(text), string(again)), text, again)
			}
			second, err := Build(qs, mode)
			if err != nil || !bytes.Equal(second.Schema(), text) {
				t.Fatalf("%v: a second Build writes other bytes: %v", mode, err)
			}
		}
	})
}
