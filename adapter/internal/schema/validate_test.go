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
	"errors"
	"maps"
	"math"
	"math/big"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
)

// verdictsPath is the table of pydantic's verdicts, as committed
// (testdata/python/gen_validator_verdicts.py writes it).
const verdictsPath = "../../testdata/python/validator_verdicts.jsonl"

// nonFiniteClass is the one class of the table on which the port differs
// from pydantic: pydantic's parser reads the tokens NaN, Infinity and
// -Infinity as numbers and accepts the text when the token stands in a value
// it does not check; the port refuses such a text as not JSON.
const nonFiniteClass = "non-finite-token"

// verdictModel is one model of the table: its Spec and its rows.
type verdictModel struct {
	name string
	spec *Spec
	rows []verdictRow
}

// verdictRow is one row of the table: an input and pydantic's verdict on
// it.
type verdictRow struct {
	name    string
	classes []string
	input   string
	// accepted is the value pydantic parsed, or the zero Node for a row it
	// refuses; refused is then its error list, one [type, loc...] per
	// error.
	accepted jsonx.Node
	refused  jsonx.Node
}

// readVerdicts reads the table after checking its header: the format, the
// three reference versions, upstream's release and commit, and the two
// counts.
func readVerdicts(t testing.TB) []verdictModel {
	t.Helper()
	data, err := os.ReadFile(verdictsPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	header := readJSON(t, lines[0])
	text := func(path ...any) string { return at(t, header, path...).Text() }
	if version, _, _ := strings.Cut(text("python"), " "); version != wantPython {
		t.Fatalf("%s: header python = %q, want CPython %s", verdictsPath, text("python"), wantPython)
	}
	for _, check := range []struct {
		path []any
		want string
	}{
		{[]any{"format"}, "1"},
		{[]any{"generator"}, "gen_validator_verdicts.py"},
		{[]any{"pydantic"}, wantPydantic},
		{[]any{"pydantic_core"}, wantPydanticCore},
		{[]any{"system_one_adapter", "version"}, wantUpstream},
		{[]any{"system_one_adapter", "commit"}, wantCommit},
	} {
		if got := text(check.path...); got != check.want {
			t.Fatalf("%s: header %v = %q, want %q", verdictsPath, check.path, got, check.want)
		}
	}

	var models []verdictModel
	rows := 0
	for _, line := range lines[1:] {
		row := readJSON(t, line)
		if name, ok := row.Member("model"); ok {
			mode := Discrete
			if at(t, row, "answer_mode").Text() == "probabilities" {
				mode = Probabilities
			}
			qs, err := ParseQuestions(at(t, row, "questions"))
			if err != nil {
				t.Fatalf("%s: model %s: %v", verdictsPath, name.Text(), err)
			}
			spec, err := Build(qs, mode)
			if err != nil {
				t.Fatalf("%s: model %s: %v", verdictsPath, name.Text(), err)
			}
			models = append(models, verdictModel{name: name.Text(), spec: spec})
			continue
		}
		r := verdictRow{name: at(t, row, "case").Text(), input: at(t, row, "input").Text()}
		if class, ok := row.Member("class"); ok {
			r.classes = strings.Split(class.Text(), "+")
		}
		if accepted, ok := row.Member("accepted"); ok {
			r.accepted = at(t, accepted, "answers")
		} else {
			r.refused = at(t, row, "refused")
		}
		m := &models[len(models)-1]
		m.rows = append(m.rows, r)
		rows++
	}
	if text("models") != strconv.Itoa(len(models)) || text("rows") != strconv.Itoa(rows) {
		t.Fatalf("%s: the header counts %s models and %s rows; the file has %d and %d", verdictsPath, text("models"), text("rows"), len(models), rows)
	}
	return models
}

// wantAccepted reports the port's expected verdict on a row: pydantic's,
// except on a row of the non-finite class, which the port refuses. A row
// with two classes carries pydantic's verdict only; it has that verdict in
// the port when every class on it is one the port reproduces.
func (r *verdictRow) wantAccepted() bool {
	return r.accepted.Kind() == jsonx.KindObject && !slices.Contains(r.classes, nonFiniteClass)
}

// issuePlaces returns the issues of a refusal as pydantic lists its errors:
// the type, then the path.
func issuePlaces(err error) [][]string {
	var ve *ValidationError
	if !errors.As(err, &ve) {
		return nil
	}
	out := make([][]string, len(ve.Issues))
	for i, issue := range ve.Issues {
		out[i] = append([]string{issue.Type}, issue.Loc...)
	}
	return out
}

// TestValidatorMatchesPydantic runs the validator over every row of the
// table of pydantic's verdicts: 882 inputs on 12 of upstream's generated
// models.
//
// The verdict is pydantic's on every row but the 18 of the non-finite
// class, where pydantic accepts and the port refuses the text as not JSON.
// The other four classes are reproduced: a member named by a generated
// field name is ignored; more than 200 arrays and objects around a value
// refuse the text; -0 for a probability is +0.0; more than 4300 characters
// before a number's fraction or exponent refuse the text.
//
// For an accepted row the value is compared, floats by their bits. For a
// refused row the issues are compared with pydantic's errors, their types
// and their paths in pydantic's order. They differ on the rows where
// pydantic's parser read a non-finite token and pydantic then refused the
// value for its type or its bound: there the port has the one issue
// json_invalid.
func TestValidatorMatchesPydantic(t *testing.T) {
	models := readVerdicts(t)
	perClass := map[string]int{}
	rows, accepted, verdictDiffers, issuesDiffer := 0, 0, 0, 0
	for _, m := range models {
		for _, r := range m.rows {
			rows++
			if len(r.classes) > 0 {
				perClass[strings.Join(r.classes, "+")]++
			}
			answers, err := m.spec.Validate(r.input)
			if (err == nil) != r.wantAccepted() {
				t.Errorf("%s %s: Validate error = %v; want accepted: %t (classes %v)", m.name, r.name, err, r.wantAccepted(), r.classes)
				continue
			}
			pydanticAccepts := r.accepted.Kind() == jsonx.KindObject
			switch {
			case err == nil:
				accepted++
				if diff := diffAnswers(m.spec, r.accepted, answers); diff != "" {
					t.Errorf("%s %s: value: %s", m.name, r.name, diff)
				}
			case pydanticAccepts:
				verdictDiffers++
				if diff := cmp.Diff([][]string{{"json_invalid"}}, issuePlaces(err)); diff != "" {
					t.Errorf("%s %s: a text with a non-finite token is refused as not JSON (-want +got):\n%s", m.name, r.name, diff)
				}
			default:
				var want [][]string
				for i := range r.refused.Len() {
					e := r.refused.Index(i)
					place := make([]string, e.Len())
					for j := range place {
						place[j] = e.Index(j).Text()
					}
					want = append(want, place)
				}
				got := issuePlaces(err)
				if cmp.Equal(want, got) {
					continue
				}
				// pydantic read a NaN or an Infinity token and refused the
				// value; the port refuses the text.
				if _, readErr := jsonx.Read([]byte(r.input)); cmp.Equal(got, [][]string{{"json_invalid"}}) && errors.Is(readErr, jsonx.ErrSyntax) &&
					(strings.Contains(r.input, "NaN") || strings.Contains(r.input, "Infinity")) {
					issuesDiffer++
					continue
				}
				t.Errorf("%s %s: issues mismatch (-pydantic +got):\n%s", m.name, r.name, cmp.Diff(want, got))
			}
		}
	}
	wantPerClass := map[string]int{
		"internal-name":                  12,
		"internal-name+non-finite-token": 6,
		"internal-name+recursion-limit":  1,
		"negative-zero":                  8,
		"non-finite-token":               12,
		"recursion-limit":                4,
		"integer-length":                 3,
	}
	if diff := cmp.Diff(wantPerClass, perClass); diff != "" {
		t.Errorf("rows per class mismatch (-want +got):\n%s", diff)
	}
	if rows != 882 || accepted != 189-18 || verdictDiffers != 18 || issuesDiffer != 43 {
		t.Errorf("%d rows, %d accepted, %d with another verdict than pydantic's, %d refused with another issue; want 882, 171, 18, 43", rows, accepted, verdictDiffers, issuesDiffer)
	}
	t.Logf("%d rows on %d models: %d accepted with pydantic's value, %d refused; %d differ from pydantic in verdict and %d in the issue alone, all on a non-finite token; rows per class: %v",
		rows, len(models), accepted, rows-accepted, verdictDiffers, issuesDiffer, perClass)
}

// diffAnswers compares the answers Validate returned with the value
// pydantic parsed, as the table writes it: a bool and a string as they are,
// an int as {"int": decimal} and a float as {"float": repr}, a probability
// map as an object of floats in label order. It returns "" when they are
// equal, floats bit for bit.
func diffAnswers(spec *Spec, want jsonx.Node, got []Answer) string {
	qs := spec.Questions()
	if want.Len() != len(qs) || len(got) != len(qs) {
		return "pydantic has " + strconv.Itoa(want.Len()) + " answers, Validate " + strconv.Itoa(len(got))
	}
	float := func(v jsonx.Node, got float64) string {
		repr, _ := v.Member("float")
		f, err := strconv.ParseFloat(repr.Text(), 64)
		if err != nil || math.Float64bits(f) != math.Float64bits(got) {
			return "float " + repr.Text() + " in the table, " + strconv.FormatFloat(got, 'g', -1, 64) + " from Validate"
		}
		return ""
	}
	for i := range qs {
		q, w, g := &qs[i], want.Index(i), got[i]
		if want.Name(i) != q.ID() {
			return "answer " + strconv.Itoa(i) + " is named " + want.Name(i) + " in the table"
		}
		var diff string
		switch {
		case spec.Mode() == Probabilities && q.Kind() == Noul:
			diff = float(w, g.Probability)
		case spec.Mode() == Probabilities:
			if w.Len() != len(g.Probabilities) {
				return q.ID() + ": " + strconv.Itoa(len(g.Probabilities)) + " probabilities"
			}
			for j, label := range q.Labels() {
				if w.Name(j) != label {
					return q.ID() + ": label " + w.Name(j) + " in the table at the place of " + label
				}
				if diff = float(w.Index(j), g.Probabilities[j]); diff != "" {
					break
				}
			}
		case q.Kind() == Noul:
			if (w.Kind() == jsonx.KindTrue) != g.Bool {
				diff = "bool"
			}
		case q.Kind() == Score:
			if n, _ := w.Member("int"); n.Text() != strconv.Itoa(g.Level) {
				diff = "int " + n.Text() + " in the table, " + strconv.Itoa(g.Level) + " from Validate"
			}
		default:
			if w.Text() != g.Label {
				diff = "label " + w.Text() + " in the table, " + g.Label + " from Validate"
			}
		}
		if diff != "" {
			return q.ID() + ": " + diff
		}
	}
	return ""
}

// The names upstream's schema tests use for questions and labels
// (FIELD_NAMES, tests/test_schema.py:18-19 at v0.2.1): schema keywords,
// attribute names of a pydantic model, a private name, the empty name, a
// name with a space, and the field names of the generated models.
var fieldNames = []string{
	"title", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum",
	"model_dump", "model_config", "_private", "", "with spaces", "answer_0", "probability_0",
}

// jsonObject writes a JSON object whose members are the names, each with the
// value value(name), a JSON text.
func jsonObject(names []string, value func(name string) string) string {
	members := make([]string, len(names))
	for i, name := range names {
		members[i] = strconv.Quote(name) + ":" + value(name)
	}
	return "{" + strings.Join(members, ",") + "}"
}

// TestQuestionNamesPreserveArbitraryNames ports
// tests/test_schema.py::test_question_ids_preserve_arbitrary_names of
// upstream v0.2.1, in its two answer modes: twelve Noul questions named by
// fieldNames keep their names in the schema and in a validated answer.
func TestQuestionNamesPreserveArbitraryNames(t *testing.T) {
	qs := mustParse(t, jsonObject(fieldNames, func(name string) string {
		return `{"type":"noul","instructions":` + strconv.Quote("Evaluate "+name+".") + `}`
	}))
	tests := map[string]struct {
		mode   Mode
		answer string
		want   Answer
	}{
		"success: probabilities": {mode: Probabilities, answer: "0.8", want: Answer{Probability: 0.8}},
		"success: discrete":      {mode: Discrete, answer: "true", want: Answer{Bool: true}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			spec, err := Build(qs, tt.mode)
			if err != nil {
				t.Fatal(err)
			}
			schema := readJSON(t, string(spec.Schema()))
			if ref := at(t, schema, "properties", "answers"); ref.Len() != 1 || at(t, ref, "$ref").Text() != "#/$defs/TypeSafeAnswers" {
				t.Errorf(`properties.answers is not {"$ref": "#/$defs/TypeSafeAnswers"}`)
			}
			answers := at(t, schema, "$defs", "TypeSafeAnswers")
			if !strings.Contains(at(t, answers, "description").Text(), "Use these property names verbatim") {
				t.Errorf("the description of the answers lacks its instruction: %q", at(t, answers, "description").Text())
			}
			checkProperties(t, answers, fieldNames, func(name, description string) bool {
				return strings.Contains(description, "Evaluate "+name+".")
			})

			got, err := spec.Validate(`{"answers":` + jsonObject(fieldNames, func(string) string { return tt.answer }) + `}`)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			want := slices.Repeat([]Answer{tt.want}, len(fieldNames))
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("answers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// checkProperties checks an object schema as upstream's two tests of names
// do: its properties and its required list are exactly names, in that
// order; each property's description passes ok; and no property carries a
// keyword upstream removes.
func checkProperties(t *testing.T, schema jsonx.Node, names []string, ok func(name, description string) bool) {
	t.Helper()
	properties := at(t, schema, "properties")
	var got, required []string
	for i := range properties.Len() {
		name := properties.Name(i)
		got = append(got, name)
		property := properties.Index(i)
		if description := at(t, property, "description").Text(); !ok(name, description) {
			t.Errorf("property %q: description %q", name, description)
		}
		for _, keyword := range unsupportedKeywords {
			if _, has := property.Member(keyword); has {
				t.Errorf("property %q carries the keyword %q", name, keyword)
			}
		}
	}
	list := at(t, schema, "required")
	for i := range list.Len() {
		required = append(required, list.Index(i).Text())
	}
	if diff := cmp.Diff(names, got); diff != "" {
		t.Errorf("properties mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(names, required); diff != "" {
		t.Errorf("required mismatch (-want +got):\n%s", diff)
	}
}

// TestProbabilityLabelsPreserveArbitraryNames ports
// tests/test_schema.py::test_probability_labels_preserve_arbitrary_names of
// upstream v0.2.1: a Choice question whose twelve labels are fieldNames
// keeps them in its probability map, in the schema and in a validated
// answer, and a probability of 2 is refused for each.
func TestProbabilityLabelsPreserveArbitraryNames(t *testing.T) {
	criteria := jsonObject(fieldNames, func(name string) string { return strconv.Quote("The " + name + " option.") })
	spec, err := Build(mustParse(t, `{"level":{"type":"choice","criteria":`+criteria+`}}`), Probabilities)
	if err != nil {
		t.Fatal(err)
	}
	probabilities := at(t, readJSON(t, string(spec.Schema())), "$defs", "ProbabilityMap0")
	if _, has := probabilities.Member("title"); has {
		t.Errorf("the probability map carries a title")
	}
	checkProperties(t, probabilities, fieldNames, func(name, description string) bool {
		return description == "The "+name+" option."
	})

	share := 1.0 / float64(len(fieldNames))
	payload := func(value string) string {
		return `{"answers":{"level":` + jsonObject(fieldNames, func(string) string { return value }) + `}}`
	}
	got, err := spec.Validate(payload(jsonx.PydanticFloat(share)))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	want := []Answer{{Probabilities: slices.Repeat([]float64{share}, len(fieldNames))}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("answers mismatch (-want +got):\n%s", diff)
	}

	_, err = spec.Validate(payload("2"))
	var wantIssues [][]string
	for _, name := range fieldNames {
		wantIssues = append(wantIssues, []string{"less_than_equal", "answers", "level", name})
	}
	if diff := cmp.Diff(wantIssues, issuePlaces(err)); diff != "" {
		t.Errorf("a probability of 2 for every label: issues mismatch (-want +got):\n%s", diff)
	}
}

// TestOutputValidationTypesBoundsAndValues ports
// tests/test_schema.py::test_output_validation_preserves_types_bounds_and_allowed_values
// of upstream v0.2.1: its thirteen answers, each to one question named
// "answer", are refused. The keys carry upstream's parameter ids, and input
// is what upstream's to_json writes for the payload.
//
// The issue of each case is pydantic's, with one exception. For question6,
// a NaN for a probability, pydantic's parser reads the NaN token as a
// number and its float validator refuses the value on the upper bound
// (less_than_equal); the port refuses the text as not JSON. The verdict is
// the same.
func TestOutputValidationTypesBoundsAndValues(t *testing.T) {
	const (
		noul   = `{"type":"noul"}`
		score  = `{"type":"score","criteria":["Bad.","Good."]}`
		choice = `{"type":"choice","criteria":{"yes":null,"no":null}}`
	)
	tests := map[string]struct {
		question string
		mode     Mode
		answer   string
		want     []string
	}{
		"error: question0-discrete-true, a string for a bool":        {noul, Discrete, `"true"`, []string{"bool_type", "answers", "answer"}},
		"error: question1-discrete-1, an integer for a bool":         {noul, Discrete, `1`, []string{"bool_type", "answers", "answer"}},
		"error: question2-probabilities-0.5, a string for a number":  {noul, Probabilities, `"0.5"`, []string{"float_type", "answers", "answer"}},
		"error: question3-probabilities-True, a bool for a number":   {noul, Probabilities, `true`, []string{"float_type", "answers", "answer"}},
		"error: question4-probabilities--0.1, below 0":               {noul, Probabilities, `-0.1`, []string{"greater_than_equal", "answers", "answer"}},
		"error: question5-probabilities-1.1, above 1":                {noul, Probabilities, `1.1`, []string{"less_than_equal", "answers", "answer"}},
		"error: question6-probabilities-nan, a NaN token":            {noul, Probabilities, `NaN`, []string{"json_invalid"}},
		"error: question7-discrete-1.0, a float for an integer":      {score, Discrete, `1.0`, []string{"int_type", "answers", "answer"}},
		"error: question8-discrete-True, a bool for an integer":      {score, Discrete, `true`, []string{"int_type", "answers", "answer"}},
		"error: question9-discrete-2, a level past the last":         {score, Discrete, `2`, []string{"less_than", "answers", "answer"}},
		"error: question10-discrete-maybe, a label not in the set":   {choice, Discrete, `"maybe"`, []string{"literal_error", "answers", "answer"}},
		"error: question11-probabilities-answer11, a label missing":  {choice, Probabilities, `{"yes":0.5}`, []string{"missing", "answers", "answer", "no"}},
		"error: question12-probabilities-answer12, a label too many": {choice, Probabilities, `{"yes":0.5,"no":0.5,"maybe":0}`, []string{"extra_forbidden", "answers", "answer", "maybe"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			spec, err := Build(mustParse(t, `{"answer":`+tt.question+`}`), tt.mode)
			if err != nil {
				t.Fatal(err)
			}
			answers, err := spec.Validate(`{"answers":{"answer":` + tt.answer + `}}`)
			if answers != nil {
				t.Errorf("Validate returned answers with its refusal: %+v", answers)
			}
			if diff := cmp.Diff([][]string{tt.want}, issuePlaces(err)); diff != "" {
				t.Errorf("issues mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestOutputRejectsExtraMembers ports
// tests/test_schema.py::test_output_rejects_extra_fields_and_internal_field_names
// of upstream v0.2.1: its three payloads for one Noul question named
// "answer", in Probabilities mode, are refused. The keys carry upstream's
// parameter ids.
//
// The third is refused because the answer is missing, not because answer_0
// is unknown: answer_0 is the name upstream's generated model gives the
// field of the first question, and pydantic ignores a member of that name.
func TestOutputRejectsExtraMembers(t *testing.T) {
	spec, err := Build(mustParse(t, `{"answer":{"type":"noul"}}`), Probabilities)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		payload string
		want    []string
	}{
		"error: payload0, a member beside answers":       {`{"answers":{"answer":0.5},"extra":1}`, []string{"extra_forbidden", "extra"}},
		"error: payload1, a member beside the answer":    {`{"answers":{"answer":0.5,"extra":1}}`, []string{"extra_forbidden", "answers", "extra"}},
		"error: payload2, the field name for the answer": {`{"answers":{"answer_0":0.5}}`, []string{"missing", "answers", "answer"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := spec.Validate(tt.payload)
			if diff := cmp.Diff([][]string{tt.want}, issuePlaces(err)); diff != "" {
				t.Errorf("issues mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// nest returns inner with n arrays, or n objects of one member each, around
// it.
func nest(n int, objects bool, inner string) string {
	if objects {
		return strings.Repeat(`{"k":`, n) + inner + strings.Repeat("}", n)
	}
	return strings.Repeat("[", n) + inner + strings.Repeat("]", n)
}

// TestParserLimitOnDepth checks the first of the parser's two limits at its
// boundary: a value with 200 arrays and objects around it is read, and one
// with 201 refuses the whole text. The value ends in a number, a string, a
// literal or an empty container, inside arrays or objects, and stands in a
// member that is ignored or that a later one of the same name replaces, so
// that only the limit can refuse the text.
//
// An ignored member of answers has two objects around it, so 198 further
// containers put the innermost value at 200.
func TestParserLimitOnDepth(t *testing.T) {
	spec, err := Build(mustParse(t, `{"answer":{"type":"noul"}}`), Probabilities)
	if err != nil {
		t.Fatal(err)
	}
	places := map[string]string{
		"in an ignored member":     `{"answers":{"answer_0":%s,"answer":0.5}}`,
		"behind a later duplicate": `{"answers":{"answer":%s,"answer":0.5}}`,
	}
	innermost := map[string]string{
		"a number":        `1`,
		"a string":        `"s"`,
		"a literal":       `null`,
		"true":            `true`,
		"an empty array":  `[]`,
		"an empty object": `{}`,
	}
	for place, format := range places {
		for inner, value := range innermost {
			for _, objects := range []bool{false, true} {
				kind := "arrays"
				if objects {
					kind = "objects"
				}
				t.Run(place+", "+inner+" in "+kind, func(t *testing.T) {
					at200 := strings.Replace(format, "%s", nest(198, objects, value), 1)
					if _, err := spec.Validate(at200); err != nil {
						t.Errorf("200 around the value: Validate error = %v, want none", err)
					}
					at201 := strings.Replace(format, "%s", nest(199, objects, value), 1)
					_, err := spec.Validate(at201)
					if diff := cmp.Diff([][]string{{"json_invalid"}}, issuePlaces(err)); diff != "" {
						t.Errorf("201 around the value: issues mismatch (-want +got):\n%s", diff)
					}
					if err == nil || !strings.Contains(err.Error(), "recursion limit exceeded") {
						t.Errorf("201 around the value: error = %v, want it to name the recursion limit", err)
					}
				})
			}
		}
	}
}

// TestParserLimitOnIntegerLength checks the second limit at its boundary:
// a number with 4300 characters before its fraction or exponent, the sign
// counted, is read, and one with 4301 refuses the whole text, whether a
// fraction or an exponent follows and wherever the number stands: as the
// validated value, in an ignored member, behind a later duplicate, and in an
// unknown member. A fraction or an exponent has no limit of its own.
func TestParserLimitOnIntegerLength(t *testing.T) {
	spec, err := Build(mustParse(t, `{"answer":{"type":"noul"}}`), Probabilities)
	if err != nil {
		t.Fatal(err)
	}
	digits := func(n int) string { return "1" + strings.Repeat("0", n-1) }
	const tooLong = "json_invalid"
	places := map[string]struct {
		format string
		// within is the issue type of a text whose number is within the
		// limit: "" when the text is accepted.
		within string
	}{
		"as the validated value":   {`{"answers":{"answer":%s}}`, "bound"},
		"in an ignored member":     {`{"answers":{"answer_0":%s,"answer":0.5}}`, ""},
		"behind a later duplicate": {`{"answers":{"answer":%s,"answer":0.5}}`, ""},
		"in an unknown member":     {`{"answers":{"answer":0.5},"extra":[%s]}`, "extra_forbidden"},
	}
	numbers := map[string]struct {
		literal string
		long    bool
	}{
		"4300 digits":                        {digits(4300), false},
		"4301 digits":                        {digits(4301), true},
		"a sign and 4299 digits":             {"-" + digits(4299), false},
		"a sign and 4300 digits":             {"-" + digits(4300), true},
		"4300 digits and a fraction":         {digits(4300) + ".5", false},
		"4301 digits and a fraction":         {digits(4301) + ".0", true},
		"4300 digits and an exponent":        {digits(4300) + "e0", false},
		"4301 digits and an exponent":        {digits(4301) + "E-4301", true},
		"a sign, 4300 digits and a fraction": {"-" + digits(4300) + ".0", true},
		"a fraction of 5000 digits":          {"0." + strings.Repeat("0", 5000), false},
		"an exponent of 4301 digits":         {"0e" + digits(4301), false},
	}
	for place, p := range places {
		for name, n := range numbers {
			t.Run(place+", "+name, func(t *testing.T) {
				_, err := spec.Validate(strings.Replace(p.format, "%s", n.literal, 1))
				got := issuePlaces(err)
				switch {
				case n.long:
					if diff := cmp.Diff([][]string{{tooLong}}, got); diff != "" {
						t.Errorf("issues mismatch (-want +got):\n%s", diff)
					}
					if err == nil || !strings.Contains(err.Error(), "number out of range") {
						t.Errorf("error = %v, want it to name the number's range", err)
					}
				case p.within == "":
					if err != nil {
						t.Errorf("Validate error = %v, want none", err)
					}
				case p.within == "bound":
					// The number is the answer: it is read and then judged
					// as a probability. The two with the value 0 pass.
					zero := strings.HasPrefix(n.literal, "0")
					if (err == nil) != zero || (!zero && (len(got) != 1 || !strings.Contains(got[0][0], "than"))) {
						t.Errorf("Validate error = %v; want a bound issue, or none for a zero", err)
					}
				default:
					if len(got) != 1 || got[0][0] != p.within {
						t.Errorf("issues = %v, want one %s", got, p.within)
					}
				}
			})
		}
	}
}

// TestValidationErrorText checks the text of a refusal, which the prompt
// that asks a model to correct its answer carries: the number of faults,
// then each with its path and what is wrong.
func TestValidationErrorText(t *testing.T) {
	qs := mustParse(t, `{"ok":{"type":"noul"},"pick":{"type":"choice","criteria":{"a b":null,"c":null}},"level":{"type":"score","criteria":["x","y","z"]}}`)
	tests := map[string]struct {
		mode Mode
		text string
		want string
		// prefix says that want is the start of the text: the rest is the
		// JSON library's own description of the fault.
		prefix bool
	}{
		"error: not JSON": {
			mode: Discrete, text: `{"answers":`,
			want: "1 validation error\nInvalid JSON: not a JSON text: ", prefix: true,
		},
		"error: nothing": {
			mode: Discrete, text: ``,
			want: "1 validation error\nInvalid JSON: not a JSON text: ", prefix: true,
		},
		"error: not an object": {
			mode: Discrete, text: `[]`,
			want: "1 validation error\nInput should be an object",
		},
		"error: no answers": {
			mode: Discrete, text: `{}`,
			want: "1 validation error\nanswers: Field required",
		},
		"error: every discrete fault": {
			mode: Discrete, text: `{"answers":{"ok":0,"pick":"d","level":3,"more":1},"other":2}`,
			want: "5 validation errors\n" +
				"other: Extra inputs are not permitted\n" +
				"answers.more: Extra inputs are not permitted\n" +
				"answers.ok: Input should be a valid boolean\n" +
				`answers.pick: Input should be one of the labels: "a b", "c"` + "\n" +
				"answers.level: Input should be less than 3",
		},
		"error: every probability fault": {
			mode: Probabilities, text: `{"answers":{"ok":"x","pick":{"a b":-1,"c":7},"level":[0,1,0]}}`,
			want: "4 validation errors\n" +
				"answers.ok: Input should be a valid number\n" +
				"answers.pick.a b: Input should be greater than or equal to 0\n" +
				"answers.pick.c: Input should be less than or equal to 1\n" +
				"answers.level: Input should be an object",
		},
		"error: integers out of range": {
			mode: Discrete, text: `{"answers":{"ok":true,"pick":"c","level":-1}}`,
			want: "1 validation error\nanswers.level: Input should be greater than or equal to 0",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			spec, err := Build(qs, tt.mode)
			if err != nil {
				t.Fatal(err)
			}
			_, err = spec.Validate(tt.text)
			got := errorText(err)
			ok := got == tt.want
			if tt.prefix {
				ok = strings.HasPrefix(got, tt.want) && len(got) > len(tt.want)
			}
			if !ok {
				t.Errorf("Validate error:\n%v\nwant:\n%s", err, tt.want)
			}
		})
	}
}

// TestValidateValues checks what an accepted text gives for each kind of
// question in each mode, a member name written twice at each level, an
// integer past 64 bits for a level, and that a Spec may be used from many
// goroutines.
func TestValidateValues(t *testing.T) {
	qs := mustParse(t, `{"ok":{"type":"noul"},"pick":{"type":"choice","criteria":{"a":null,"b":null}},"level":{"type":"score","criteria":["x","y","z"]}}`)
	huge := strings.Repeat("9", 30)
	tests := map[string]struct {
		mode    Mode
		text    string
		want    []Answer
		wantErr [][]string
	}{
		"success: discrete": {
			mode: Discrete, text: ` {"answers" : {"level":2, "pick":"b", "ok":false} } `,
			want: []Answer{{Bool: false}, {Label: "b"}, {Level: 2}},
		},
		"success: probabilities": {
			mode: Probabilities, text: `{"answers":{"ok":1,"pick":{"b":0.25,"a":0.75},"level":{"0":0,"1":1e-1,"2":0.9}}}`,
			want: []Answer{{Probability: 1}, {Probabilities: []float64{0.75, 0.25}}, {Probabilities: []float64{0, 0.1, 0.9}}},
		},
		"success: the last of two members of one name, at each level": {
			mode: Probabilities,
			text: `{"answers":null,"answers":{"ok":"x","ok":0.5,"pick":[],"pick":{"a":9,"a":0.5,"b":0.5},"level":{"0":0,"1":0,"2":1},"zz":1},` +
				`"answers":{"ok":0.25,"pick":{"a":1,"b":0},"level":{"0":1,"1":0,"2":0}}}`,
			want: []Answer{{Probability: 0.25}, {Probabilities: []float64{1, 0}}, {Probabilities: []float64{1, 0, 0}}},
		},
		"error: the last of two members is the one judged": {
			mode: Discrete, text: `{"answers":{"ok":true,"pick":"a","level":1,"ok":null}}`,
			wantErr: [][]string{{"bool_type", "answers", "ok"}},
		},
		"error: integers past 64 bits are compared": {
			mode: Discrete, text: `{"answers":{"ok":true,"pick":"a","level":` + huge + `,"level":-` + huge + `}}`,
			wantErr: [][]string{{"greater_than_equal", "answers", "level"}},
		},
		"error: a positive integer past 64 bits": {
			mode: Discrete, text: `{"answers":{"ok":true,"pick":"a","level":` + huge + `}}`,
			wantErr: [][]string{{"less_than", "answers", "level"}},
		},
		"error: a generated field name with a sign, a leading zero or an index past the last is unknown": {
			mode: Discrete, text: `{"answers":{"ok":true,"pick":"a","level":1,"answer_-0":1,"answer_00":1,"answer_3":1,"answer_":1,"answer_2":1}}`,
			wantErr: [][]string{
				{"extra_forbidden", "answers", "answer_-0"},
				{"extra_forbidden", "answers", "answer_00"},
				{"extra_forbidden", "answers", "answer_3"},
				{"extra_forbidden", "answers", "answer_"},
			},
		},
		"error: the top level has no generated field names": {
			mode: Discrete, text: `{"answers":{"ok":true,"pick":"a","level":1},"answer_0":1,"0":2}`,
			wantErr: [][]string{{"extra_forbidden", "answer_0"}, {"extra_forbidden", "0"}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			spec, err := Build(qs, tt.mode)
			if err != nil {
				t.Fatal(err)
			}
			got, err := spec.Validate(tt.text)
			if diff := cmp.Diff(tt.wantErr, issuePlaces(err)); diff != "" {
				t.Errorf("issues mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("answers mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("success: one Spec from many goroutines", func(t *testing.T) {
		spec, err := Build(qs, Discrete)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error)
		for range 8 {
			go func() {
				var err error
				for range 200 {
					if _, err = spec.Validate(`{"answers":{"ok":true,"pick":"a","level":1}}`); err != nil {
						break
					}
				}
				done <- err
			}()
		}
		for range 8 {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	})
}

// The reference checker of FuzzValidate. It decides, by another mechanism
// than Validate, whether an answer text is accepted and what it holds: it
// walks the tree jsonx.Read makes of the text, in which each member name has
// its last value, and checks on the raw text what the tree cannot show. It
// shares with the validator the package jsonx and the questions of the
// Spec, and nothing else: each rule is written here a second time.
//
// The one rule not written twice is the refusal of a NaN, Infinity or
// -Infinity token, which is the JSON reader's own.

// generatedField matches the names upstream's generated models give their
// fields; the first group is the index.
var generatedField = map[string]*regexp.Regexp{
	"answer":      regexp.MustCompile(`^answer_(0|[1-9][0-9]*)$`),
	"probability": regexp.MustCompile(`^probability_(0|[1-9][0-9]*)$`),
}

// referenceAccepts reports whether the reference accepts text as an answer
// to qs in mode, and the answers it reads.
func referenceAccepts(qs []Question, mode Mode, text string) ([]Answer, bool) {
	doc, err := jsonx.Read([]byte(text))
	if err != nil || !withinParserLimits(text) {
		return nil, false
	}
	if doc.Kind() != jsonx.KindObject || doc.Len() != 1 || doc.Name(0) != "answers" {
		return nil, false
	}
	ids := make([]string, len(qs))
	for i := range qs {
		ids[i] = qs[i].ID()
	}
	answers := doc.Index(0)
	if !referenceMembers(answers, ids, "answer") {
		return nil, false
	}
	out := make([]Answer, len(qs))
	for i := range qs {
		q := &qs[i]
		v, _ := answers.Member(q.ID())
		ok := false
		switch {
		case mode == Probabilities && q.Kind() == Noul:
			out[i].Probability, ok = referenceProbability(v)
		case mode == Probabilities:
			if ok = referenceMembers(v, q.Labels(), "probability"); !ok {
				break
			}
			for _, label := range q.Labels() {
				p, _ := v.Member(label)
				f, fine := referenceProbability(p)
				ok = ok && fine
				out[i].Probabilities = append(out[i].Probabilities, f)
			}
		case q.Kind() == Noul:
			out[i].Bool, ok = v.Kind() == jsonx.KindTrue, v.Kind() == jsonx.KindTrue || v.Kind() == jsonx.KindFalse
		case q.Kind() == Score:
			if n, isInt := new(big.Int).SetString(v.Text(), 10); v.IsInt() && isInt && n.Sign() >= 0 && n.Cmp(big.NewInt(int64(len(q.Labels())))) < 0 {
				out[i].Level, ok = int(n.Int64()), true
			}
		default:
			out[i].Label, ok = v.Text(), v.Kind() == jsonx.KindString && slices.Contains(q.Labels(), v.Text())
		}
		if !ok {
			return nil, false
		}
	}
	return out, true
}

// referenceMembers reports whether v is an object that has every name of
// fields and, besides them, only names of generated fields with an index
// below their number.
func referenceMembers(v jsonx.Node, fields []string, generated string) bool {
	if v.Kind() != jsonx.KindObject {
		return false
	}
	found := 0
	for i := range v.Len() {
		name := v.Name(i)
		if slices.Contains(fields, name) {
			found++
			continue
		}
		m := generatedField[generated].FindStringSubmatch(name)
		if m == nil {
			return false
		}
		if index, ok := new(big.Int).SetString(m[1], 10); !ok || index.Cmp(big.NewInt(int64(len(fields)))) >= 0 {
			return false
		}
	}
	return found == len(fields)
}

// referenceProbability reads a number from 0 to 1 with math/big: an integer
// literal by its integer value, any other literal rounded to a float64.
func referenceProbability(v jsonx.Node) (float64, bool) {
	if v.Kind() != jsonx.KindNumber {
		return 0, false
	}
	var f float64
	if v.IsInt() {
		n, _ := new(big.Int).SetString(v.Text(), 10)
		f, _ = new(big.Float).SetInt(n).Float64()
	} else if parsed, _, err := big.ParseFloat(v.Text(), 10, 53, big.ToNearestEven); err == nil {
		f, _ = parsed.Float64()
	} else {
		// math/big refuses an exponent that no int holds. Such a number is
		// zero when its digits are all zeros or its exponent is negative,
		// and infinite otherwise, with the literal's sign.
		mantissa, exponent, _ := strings.Cut(strings.ToLower(v.Text()), "e")
		if strings.Trim(mantissa, "-0.") != "" && !strings.HasPrefix(exponent, "-") {
			f = math.Inf(1)
		}
		if strings.HasPrefix(mantissa, "-") {
			f = -f
		}
	}
	return f, f >= 0 && f <= 1
}

// withinParserLimits scans the raw bytes of a text that is JSON and reports
// whether every value has at most 200 arrays and objects around it and every
// number at most 4300 characters before its fraction or exponent, the sign
// counted. It sees the values the tree of jsonx.Read does not hold: the ones
// a later member of the same name replaced.
func withinParserLimits(text string) bool {
	depth := 0
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '"':
			if depth > 200 {
				return false
			}
			for i++; text[i] != '"'; i++ {
				if text[i] == '\\' {
					i++
				}
			}
		case c == '[' || c == '{':
			if depth > 200 {
				return false
			}
			depth++
		case c == ']' || c == '}':
			depth--
		case c == '-' || (c >= '0' && c <= '9'):
			start := i
			for i+1 < len(text) && text[i+1] >= '0' && text[i+1] <= '9' {
				i++
			}
			if depth > 200 || i+1-start > 4300 {
				return false
			}
			for i+1 < len(text) && strings.IndexByte("+-.eE0123456789", text[i+1]) >= 0 {
				i++
			}
		case c == 't' || c == 'f' || c == 'n':
			if depth > 200 {
				return false
			}
		}
	}
	return true
}

// sameAnswers reports whether two results of Validate are equal, floats bit
// for bit.
func sameAnswers(a, b []Answer) bool {
	return slices.EqualFunc(a, b, func(x, y Answer) bool {
		return x.Bool == y.Bool && x.Level == y.Level && x.Label == y.Label &&
			math.Float64bits(x.Probability) == math.Float64bits(y.Probability) &&
			slices.EqualFunc(x.Probabilities, y.Probabilities, func(p, q float64) bool { return math.Float64bits(p) == math.Float64bits(q) })
	})
}

// checkAgainstReference holds one text to FuzzValidate's property for one
// Spec and returns what is wrong, or "".
func checkAgainstReference(spec *Spec, text string) string {
	qs := spec.Questions()
	got, err := spec.Validate(text)
	want, accepts := referenceAccepts(qs, spec.Mode(), text)
	switch {
	case (err == nil) != accepts:
		return "Validate error = " + errorText(err) + "; the reference accepts: " + strconv.FormatBool(accepts)
	case err != nil:
		if ve := new(ValidationError); !errors.As(err, &ve) || len(ve.Issues) == 0 || got != nil {
			return "a refusal that is not a *ValidationError with an issue and no answers: " + errorText(err)
		}
		return ""
	case !sameAnswers(got, want):
		return "Validate and the reference read other values"
	}
	if again, err := spec.Validate(text); err != nil || !sameAnswers(got, again) {
		return "a second Validate gives another result: " + errorText(err)
	}
	if len(got) != len(qs) {
		return strconv.Itoa(len(got)) + " answers for " + strconv.Itoa(len(qs)) + " questions"
	}
	for i := range qs {
		if isMap := spec.Mode() == Probabilities && qs[i].Kind() != Noul; isMap && len(got[i].Probabilities) != len(qs[i].Labels()) || !isMap && got[i].Probabilities != nil {
			return "question " + strconv.Itoa(i) + ": " + strconv.Itoa(len(got[i].Probabilities)) + " probabilities for " + strconv.Itoa(len(qs[i].Labels())) + " labels"
		}
	}
	return ""
}

func errorText(err error) string {
	if err == nil {
		return "none"
	}
	return err.Error()
}

// TestReferenceAgreesWithTheTable runs FuzzValidate's property over every
// row of the table of pydantic's verdicts, on the row's own model, and
// checks that the reference checker alone gives the expected verdict: the
// reference is held to pydantic before the fuzzer holds the validator to
// the reference.
func TestReferenceAgreesWithTheTable(t *testing.T) {
	for _, m := range readVerdicts(t) {
		for _, r := range m.rows {
			if _, accepts := referenceAccepts(m.spec.Questions(), m.spec.Mode(), r.input); accepts != r.wantAccepted() {
				t.Errorf("%s %s: the reference accepts: %t, want %t", m.name, r.name, accepts, r.wantAccepted())
			}
			if wrong := checkAgainstReference(m.spec, r.input); wrong != "" {
				t.Errorf("%s %s: %s", m.name, r.name, wrong)
			}
		}
	}
}

// FuzzValidate holds the validator to the reference checker on any text,
// for each of the 12 models of the table of pydantic's verdicts: it does not
// panic; it accepts exactly the texts the reference accepts, with the same
// values; a refusal is a *ValidationError that names an issue; and an
// accepted text gives the same value on a second call, an answer for every
// question, and in every probability map one probability per label of the
// question.
//
// The seeds are the inputs of the table.
func FuzzValidate(f *testing.F) {
	models := readVerdicts(f)
	seeds := map[string]bool{}
	for _, m := range models {
		for _, r := range m.rows {
			seeds[r.input] = true
		}
	}
	for _, seed := range slices.Sorted(maps.Keys(seeds)) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		for _, m := range models {
			if wrong := checkAgainstReference(m.spec, text); wrong != "" {
				t.Fatalf("model %s, text %.300q: %s", m.name, text, wrong)
			}
		}
	})
}
