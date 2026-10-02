// Copyright 2026 The typesafe-sdk-go Authors.
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

package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/typesafe-sdk-go/adapter/internal/schema"
)

// The files of generated cases this file reads, as committed.
const (
	schemaCasesPath       = "testdata/python/schema_cases.jsonl"
	validatorVerdictsPath = "testdata/python/validator_verdicts.jsonl"
)

// member returns the member of v at path, failing the test when one is
// missing.
func member(t testing.TB, v jsonx.Node, path ...string) jsonx.Node {
	t.Helper()
	for _, name := range path {
		var ok bool
		if v, ok = v.Member(name); !ok {
			t.Fatalf("no member %v", path)
		}
	}
	return v
}

// readLines returns the lines of a JSON Lines file, each read.
func readLines(t testing.TB, path string) []jsonx.Node {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []jsonx.Node
	for line := range strings.Lines(string(data)) {
		n, err := jsonx.Read([]byte(strings.TrimSuffix(line, "\n")))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, n)
	}
	return out
}

// checkGeneratedHeader fails the test unless a generated file's header
// names generator and the reference versions.
func checkGeneratedHeader(t testing.TB, path string, header jsonx.Node, generator string) {
	t.Helper()
	for _, check := range []struct {
		path []string
		want string
	}{
		{[]string{"format"}, "1"},
		{[]string{"generator"}, generator},
		{[]string{"pydantic"}, "2.13.4"},
		{[]string{"pydantic_core"}, "2.46.4"},
		{[]string{"system_one_adapter", "version"}, UpstreamVersion},
		{[]string{"system_one_adapter", "commit"}, UpstreamCommit},
		{[]string{"typesafe_sdk"}, "0.7.0"},
	} {
		if got := member(t, header, check.path...).Text(); got != check.want {
			t.Fatalf("%s: header %v = %q, want %q", path, check.path, got, check.want)
		}
	}
	if version, _, _ := strings.Cut(member(t, header, "python").Text(), " "); version != "3.14.3" {
		t.Fatalf("%s: header python %q, want CPython 3.14.3", path, member(t, header, "python").Text())
	}
}

// TestPromptsMatchUpstreamCases builds the two messages of every request
// upstream accepts in adapter/testdata/python/schema_cases.jsonl, which
// gen_schema_cases.py wrote with upstream's own code, and compares them byte
// for byte with upstream's (_client.py:436-445): the user message with the
// row's user text, and the system message, in both answer modes and both
// output modes, with the prompt of the answer mode followed, in prompted
// mode, by the schema instruction around the row's schema text. The rows
// state_less_than_only and state_greater_than_only pin the replacement of
// '<' and of '>' in the state text, each on its own.
func TestPromptsMatchUpstreamCases(t *testing.T) {
	lines := readLines(t, schemaCasesPath)
	checkGeneratedHeader(t, schemaCasesPath, lines[0], "gen_schema_cases.py")
	accepted := 0
	names := map[string]bool{}
	for _, row := range lines[1:] {
		if _, refused := row.Member("refused"); refused {
			continue
		}
		accepted++
		name := member(t, row, "case").Text()
		names[name] = true
		t.Run(name, func(t *testing.T) {
			request, err := jsonx.Read([]byte(member(t, row, "request").Text()))
			if err != nil {
				t.Fatal(err)
			}
			user, err := userPrompt(member(t, request, "state"))
			if err != nil {
				t.Fatalf("userPrompt: %v", err)
			}
			if want := member(t, row, "user").Text(); user != want {
				t.Fatalf("user message:\n got: %q\nwant: %q", user, want)
			}
			questions, err := schema.ParseQuestions(member(t, request, "questions"))
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []struct {
				answer AnswerMode
				name   string
				prompt string
			}{
				{Probabilities, "probabilities", probabilitySystemPrompt},
				{Discrete, "discrete", discreteSystemPrompt},
			} {
				spec, err := schema.Build(questions, mode.answer.schemaMode())
				if err != nil {
					t.Fatal(err)
				}
				schemaText := member(t, row, "schema", mode.name).Text()
				if got := systemPrompt(mode.answer, Structured, spec.Schema()); got != mode.prompt {
					t.Errorf("%s structured: system message %q", mode.name, got)
				}
				want := mode.prompt + "\n\nReturn one JSON object that matches this schema exactly:\n\n" + schemaText +
					"\n\nDo not include text or Markdown fencing before or after the JSON object."
				if got := systemPrompt(mode.answer, Prompted, spec.Schema()); got != want {
					t.Errorf("%s prompted: system message\n got: %q\nwant: %q", mode.name, got, want)
				}
			}
		})
	}
	for _, name := range []string{"state_less_than_only", "state_greater_than_only"} {
		if !names[name] {
			t.Errorf("%s has no accepted case %s", schemaCasesPath, name)
		}
	}
	if accepted != 34 {
		t.Errorf("%d accepted requests, want 34", accepted)
	}
}

// TestSystemPromptsMatchRecordings compares the system prompts with the
// system messages of upstream's 12 recorded runs (tests/expected_responses):
// the prompt of each answer mode exactly, and in prompted mode the schema
// instruction around a schema that equals, as a JSON value, the schema the
// run sent.
func TestSystemPromptsMatchRecordings(t *testing.T) {
	files := expectedResponses(t)
	if len(files) != 12 {
		t.Fatalf("%d expected responses, want 12", len(files))
	}
	for _, file := range files {
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(file), "test_live_responses_match_reference_shape["), "].json")
		t.Run(name, func(t *testing.T) {
			body, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			root, err := jsonx.Read(body)
			if err != nil {
				t.Fatal(err)
			}
			attempt := member(t, root, "debug", "llm_attempts").Index(0)
			got := member(t, attempt, "messages").Index(0)
			if member(t, got, "role").Text() != "system" {
				t.Fatal("the first message is not the system message")
			}
			answer := Probabilities
			if strings.HasPrefix(name, "discrete-") {
				answer = Discrete
			}
			if !strings.Contains(name, "-prompted-") {
				if want := systemPrompt(answer, Structured, nil); member(t, got, "content").Text() != want {
					t.Fatalf("system message\n got: %q\nwant: %q", member(t, got, "content").Text(), want)
				}
				return
			}
			prefix := strings.TrimSuffix(systemPrompt(answer, Prompted, nil), schemaInstructionAfter)
			text := member(t, got, "content").Text()
			rest, ok := strings.CutPrefix(text, prefix)
			if !ok {
				t.Fatalf("the recorded system message does not start with the prompt and the schema instruction:\n%q", text)
			}
			schemaText, ok := strings.CutSuffix(rest, schemaInstructionAfter)
			if !ok {
				t.Fatalf("the recorded system message does not end with the schema instruction:\n%q", text)
			}
			sent := mustMarshalNode(t, member(t, attempt, "model_request_parameters", "schema"))
			if equal, err := jsonx.Equal([]byte(schemaText), []byte(sent)); err != nil || !equal {
				t.Fatalf("the schema in the system message is not the schema sent (%v)", err)
			}
		})
	}
}

// TestExtractJSON ports upstream's _extract_json (_client.py:97-107): plan
// 6.4's cases, and the 24 rows of validator_verdicts.jsonl whose verdict
// upstream's own run of _extract_json changes (after_extract_json), with
// the validator built from the row's model.
func TestExtractJSON(t *testing.T) {
	tests := map[string]struct {
		in   string
		want string
	}{
		"fence with json":               {in: "```json\n{\"a\":1}\n```", want: `{"a":1}`},
		"fence with JSON":               {in: "```JSON\n{\"a\":1}\n```", want: `{"a":1}`},
		"fence with Json, no newline":   {in: "```Json{\"a\":1}```", want: `{"a":1}`},
		"fence without a language":      {in: "```\n{\"a\":1}\n```", want: `{"a":1}`},
		"no closing fence":              {in: "```json\n{\"a\":1}", want: `{"a":1}`},
		"surrounding white space":       {in: " \t\n```json\n{\"a\":1}\n```\n ", want: `{"a":1}`},
		"no fence":                      {in: " {\"a\":1} ", want: `{"a":1}`},
		"no fence, inner white space":   {in: "{ \"a\" : 1 }", want: `{ "a" : 1 }`},
		"Python's white space":          {in: " 　\x1f{\"a\":1} \u0085", want: `{"a":1}`},
		"a fence alone":                 {in: "```", want: ""},
		"a fence and json alone":        {in: "```json", want: ""},
		"two fences":                    {in: "``````", want: ""},
		"prose after the fence":         {in: "```json\n{}\n```\nDone.", want: "{}\n```\nDone."},
		"jso is not json":               {in: "```jso\n{}```", want: "jso\n{}"},
		"a fence later is not stripped": {in: "x```json{}```", want: "x```json{}```"},
		"empty":                         {in: "", want: ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := extractJSON(tt.in); got != tt.want {
				t.Fatalf("extractJSON(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	lines := readLines(t, validatorVerdictsPath)
	var spec *schema.Spec
	rows := 0
	for _, line := range lines[1:] {
		if model, ok := line.Member("model"); ok {
			questions, err := schema.ParseQuestions(member(t, line, "questions"))
			if err != nil {
				t.Fatalf("%s: model %s: %v", validatorVerdictsPath, model.Text(), err)
			}
			mode := Probabilities
			if member(t, line, "answer_mode").Text() == "discrete" {
				mode = Discrete
			}
			if spec, err = schema.Build(questions, mode.schemaMode()); err != nil {
				t.Fatal(err)
			}
			continue
		}
		after, ok := line.Member("after_extract_json")
		if !ok {
			continue
		}
		rows++
		input := member(t, line, "input").Text()
		t.Run("verdict/"+member(t, line, "case").Text()+"/"+strconv.Itoa(rows), func(t *testing.T) {
			answers, err := spec.Validate(extractJSON(input))
			if _, refused := after.Member("refused"); refused {
				verr, ok := errors.AsType[*schema.ValidationError](err)
				if !ok || len(verr.Issues) != 1 || verr.Issues[0].Type != "json_invalid" {
					t.Fatalf("Validate(extractJSON(%q)): %v; want one json_invalid issue", input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate(extractJSON(%q)): %v; upstream accepts it", input, err)
			}
			if got, want := acceptedForm(spec, answers), mustMarshalNode(t, member(t, after, "accepted")); got != want {
				t.Fatalf("accepted values\n got: %s\nwant: %s", got, want)
			}
		})
	}
	if rows != 24 {
		t.Fatalf("%d rows with after_extract_json, want 24", rows)
	}
}

// acceptedForm writes answers as validator_verdicts.jsonl writes an
// accepted value: {"answers": {question: value}} with a bool or a string as
// itself, an int as {"int": text} and a float as {"float": repr}, labels in
// order.
func acceptedForm(spec *schema.Spec, answers []schema.Answer) string {
	number := func(kind, text string) jsonx.Value {
		return jsonx.Object(jsonx.Member{Name: kind, Value: jsonx.String(text)})
	}
	float := func(f float64) jsonx.Value { return number("float", jsonx.ReprFloat(f)) }
	questions := spec.Questions()
	members := make([]jsonx.Member, len(questions))
	for i, q := range questions {
		a := answers[i]
		var v jsonx.Value
		switch {
		case q.Kind() == schema.Noul && spec.Mode() == schema.Discrete:
			v = jsonx.Bool(a.Bool)
		case q.Kind() == schema.Noul:
			v = float(a.Probability)
		case spec.Mode() == schema.Discrete && q.Kind() == schema.Score:
			v = number("int", strconv.Itoa(a.Level))
		case spec.Mode() == schema.Discrete:
			v = jsonx.String(a.Label)
		default:
			labels := make([]jsonx.Member, len(q.Labels()))
			for j, label := range q.Labels() {
				labels[j] = jsonx.Member{Name: label, Value: float(a.Probabilities[j])}
			}
			v = jsonx.Object(labels...)
		}
		members[i] = jsonx.Member{Name: q.ID(), Value: v}
	}
	out, _ := jsonx.Marshal(jsonx.Object(jsonx.Member{Name: "answers", Value: jsonx.Object(members...)}))
	return string(out)
}

// TestCorrectionPromptNamesTheFailure checks the user message that asks
// the model to correct its answer (DV8): upstream's fixed text around the
// Go validator's message (_client.py:110-115), which names the failed
// path; for an output that is not one JSON value, one fixed text instead of
// the JSON library's wording, so that no library message reaches a model.
func TestCorrectionPromptNamesTheFailure(t *testing.T) {
	positive, err := jsonx.Read([]byte(`{"answer":{"type":"noul","instructions":"The review is positive."},"genre":{"type":"choice","criteria":{"fiction":"A story.","nonfiction":"Facts."}}}`))
	if err != nil {
		t.Fatal(err)
	}
	questions, err := schema.ParseQuestions(positive)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := schema.Build(questions, schema.Probabilities)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		output  string
		want    []string // fragments of the validator's message, in order
		notJSON bool
	}{
		"missing answer":          {output: `{"answers":{"genre":{"fiction":0.5,"nonfiction":0.5}}}`, want: []string{"1 validation error\n", "answers.answer: "}},
		"missing probability key": {output: `{"answers":{"answer":0.75,"genre":{"fiction":0.5}}}`, want: []string{"1 validation error\n", "answers.genre.nonfiction: "}},
		"two faults":              {output: `{"answers":{"answer":"x","genre":{"fiction":2,"nonfiction":0.5}}}`, want: []string{"2 validation errors\n", "answers.answer: ", "\nanswers.genre.fiction: "}},
		"truncated JSON":          {output: `{"answers":`, want: []string{"1 validation error\n" + invalidJSONText}, notJSON: true},
		"invalid JSON":            {output: `{"answers": {"answer": nope}}`, want: []string{"1 validation error\n" + invalidJSONText}, notJSON: true},
		"empty output":            {output: ``, want: []string{"1 validation error\n" + invalidJSONText}, notJSON: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := spec.Validate(tt.output)
			verr, ok := errors.AsType[*schema.ValidationError](err)
			if !ok {
				t.Fatalf("Validate(%q): %v, want a *schema.ValidationError", tt.output, err)
			}
			prompt := correctionPrompt(validationText(verr))
			const upstreamBefore = "The previous response did not match the required schema: "
			const upstreamAfter = "\nReturn a single JSON object that matches the schema exactly, with no other text."
			message, ok := strings.CutPrefix(prompt, upstreamBefore)
			if !ok {
				t.Fatalf("the prompt does not start with upstream's text: %q", prompt)
			}
			if message, ok = strings.CutSuffix(message, upstreamAfter); !ok {
				t.Fatalf("the prompt does not end with upstream's text: %q", prompt)
			}
			rest := message
			for _, fragment := range tt.want {
				i := strings.Index(rest, fragment)
				if i < 0 {
					t.Fatalf("the validator's message %q lacks %q after what precedes it", message, fragment)
				}
				rest = rest[i+len(fragment):]
			}
			if tt.notJSON {
				if message != "1 validation error\n"+invalidJSONText {
					t.Fatalf("the message for an output that is not JSON is %q, not the fixed text", message)
				}
				if library := verr.Issues[0].Message; strings.Contains(prompt, library) {
					t.Fatalf("the prompt carries the JSON library's wording %q", library)
				}
			} else if message != verr.Error() {
				t.Fatalf("the message is not the validator's: %q vs %q", message, verr.Error())
			}
		})
	}
}

// TestValidationTextKeepsIssues checks that the fixed text replaces only
// the message of a json_invalid issue and keeps every other issue as the
// validator wrote it, without changing the error it was given.
func TestValidationTextKeepsIssues(t *testing.T) {
	err := &schema.ValidationError{Issues: []schema.Issue{
		{Type: "missing", Loc: []string{"answers", "a"}, Message: "Field required"},
		{Type: "json_invalid", Message: "Invalid JSON: the library's words"},
	}}
	want := "2 validation errors\nanswers.a: Field required\n" + invalidJSONText
	if got := validationText(err); got != want {
		t.Fatalf("validationText = %q, want %q", got, want)
	}
	if diff := cmp.Diff("Invalid JSON: the library's words", err.Issues[1].Message); diff != "" {
		t.Fatalf("validationText changed its argument (-want +got):\n%s", diff)
	}
}

func TestUserPromptRefusesADeepState(t *testing.T) {
	deep, err := jsonx.Read([]byte(strings.Repeat("[", 256) + strings.Repeat("]", 256)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := userPrompt(deep); !errors.Is(err, jsonx.ErrDepth) {
		t.Fatalf("userPrompt of 256 levels: %v, want jsonx.ErrDepth", err)
	}
}
