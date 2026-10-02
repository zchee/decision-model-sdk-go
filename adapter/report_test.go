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
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"

	typesafe "github.com/zchee/typesafe-sdk-go"

	"github.com/zchee/typesafe-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/typesafe-sdk-go/adapter/llm"
)

// reportCasesPath is the table adapter/testdata/python/gen_report_cases.py
// writes; the expected responses are under expectedDir (matrix_test.go).
const reportCasesPath = "testdata/python/report_cases.jsonl"

// project returns the value v with every member for which drop reports true
// left out, numbers spelled as v spells them. path is v's place: member
// names, and "[]" for an element.
func project(v jsonx.Node, path []string, drop func(path []string) bool) jsonx.Value {
	switch v.Kind() {
	case jsonx.KindArray:
		elems := make([]jsonx.Value, v.Len())
		for i := range elems {
			elems[i] = project(v.Index(i), append(path, "[]"), drop)
		}
		return jsonx.Array(elems...)
	case jsonx.KindObject:
		var members []jsonx.Member
		for i := range v.Len() {
			p := append(slices.Clone(path), v.Name(i))
			if drop(p) {
				continue
			}
			members = append(members, jsonx.Member{Name: v.Name(i), Value: project(v.Index(i), p, drop)})
		}
		return jsonx.Object(members...)
	}
	return rawValue(v)
}

// usageAndDebug returns the members usage and debug of body, in that order,
// without usage.latency (dropped on both sides, as upstream's own test drops
// it: tests/test_client_with_live_apis.py:127-131) and without the members
// drop names.
func usageAndDebug(t *testing.T, body []byte, drop ...string) []byte {
	t.Helper()
	root, err := jsonx.Read(body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var members []jsonx.Member
	for _, name := range []string{"usage", "debug"} {
		v, ok := root.Member(name)
		if !ok {
			t.Fatalf("no member %s in %.200s", name, body)
		}
		members = append(members, jsonx.Member{Name: name, Value: project(v, []string{name}, func(p []string) bool {
			joined := strings.Join(p, ".")
			return joined == "usage.latency" || slices.Contains(drop, joined)
		})})
	}
	out, err := jsonx.Marshal(jsonx.Object(members...))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// roundTrip reads body into a Report and writes it again.
func roundTrip(t *testing.T, body []byte) []byte {
	t.Helper()
	var r Report
	if err := r.UnmarshalJSON(body); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	out, err := r.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	return out
}

// sameOrdered fails the test unless a and b hold the same value with the
// same member order at every level (DV4).
func sameOrdered(t *testing.T, got, want []byte) {
	t.Helper()
	equal, err := jsonx.EqualOrdered(got, want)
	if err != nil {
		t.Fatalf("EqualOrdered: %v", err)
	}
	if !equal {
		t.Fatalf("the Report's JSON differs from upstream's in a member, its order or its value\n got: %.3000s\nwant: %.3000s", got, want)
	}
}

// reportCase is one row of report_cases.jsonl.
type reportCase struct {
	name     string
	scenario jsonx.Node
	response jsonx.Node // the whole response, set for a call that answered
	errClass string     // the error's class, set for a call that failed
	errDebug jsonx.Node // the error's debug attribute
}

// readReportCases returns the rows of report_cases.jsonl after checking its
// header.
func readReportCases(t *testing.T) []reportCase {
	t.Helper()
	data, err := os.ReadFile(reportCasesPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	header, err := jsonx.Read([]byte(lines[0]))
	if err != nil {
		t.Fatal(err)
	}
	text := func(path ...string) string {
		v := header
		for _, p := range path {
			v, _ = v.Member(p)
		}
		return v.Text()
	}
	for _, check := range []struct {
		path []string
		want string
	}{
		{[]string{"format"}, "1"},
		{[]string{"generator"}, "gen_report_cases.py"},
		{[]string{"pydantic"}, "2.13.4"},
		{[]string{"pydantic_core"}, "2.46.4"},
		{[]string{"system_one_adapter", "version"}, UpstreamVersion},
		{[]string{"system_one_adapter", "commit"}, UpstreamCommit},
		{[]string{"typesafe_sdk"}, "0.7.0"},
		{[]string{"rows"}, strconv.Itoa(len(lines) - 1)},
	} {
		if got := text(check.path...); got != check.want {
			t.Fatalf("%s: header %v = %q, want %q", reportCasesPath, check.path, got, check.want)
		}
	}
	if version, _, _ := strings.Cut(text("python"), " "); version != "3.14.3" {
		t.Fatalf("%s: header python %q, want CPython 3.14.3", reportCasesPath, text("python"))
	}
	cases := make([]reportCase, 0, len(lines)-1)
	for _, line := range lines[1:] {
		row, err := jsonx.Read([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		name, _ := row.Member("case")
		c := reportCase{name: name.Text()}
		c.scenario, _ = row.Member("scenario")
		if resp, ok := row.Member("response"); ok {
			c.response = resp
		} else {
			e, _ := row.Member("error")
			class, _ := e.Member("class")
			c.errClass = class.Text()
			c.errDebug, _ = e.Member("debug")
		}
		cases = append(cases, c)
	}
	return cases
}

// failureBody returns the body of a failed call as the Adapter writes it
// around upstream's error.debug: zero usage and the probability members with
// their zero values, then upstream's llm_attempts and retry_reasons.
func failureBody(t *testing.T, debug jsonx.Node) []byte {
	t.Helper()
	members := []jsonx.Member{
		{Name: "max_error", Value: jsonx.Float(0, jsonx.Repr)},
		{Name: "invalid_probs", Value: jsonx.Number("0")},
		{Name: "probability_errors", Value: jsonx.Object()},
	}
	for i := range debug.Len() {
		members = append(members, jsonx.Member{Name: debug.Name(i), Value: rawValue(debug.Index(i))})
	}
	zero := jsonx.Number("0")
	body, err := jsonx.Marshal(jsonx.Object(
		jsonx.Member{Name: "usage", Value: jsonx.Object(
			jsonx.Member{Name: "input_tokens", Value: jsonx.Value{}},
			jsonx.Member{Name: "output_tokens", Value: jsonx.Value{}},
			jsonx.Member{Name: "input_tokens_total", Value: zero},
			jsonx.Member{Name: "output_tokens_total", Value: zero},
			jsonx.Member{Name: "n_retries", Value: zero},
			jsonx.Member{Name: "n_retries_malformed_structure", Value: zero},
		)},
		jsonx.Member{Name: "debug", Value: jsonx.Object(members...)},
	))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// TestReportJSONMatchesUpstreamShape reads upstream's own usage and debug
// data into a Report and writes it again, and requires the same members in
// the same order with the same values (DV4): the 12 expected responses of
// upstream's recorded runs (tests/expected_responses, written with json.dumps
// of model_dump(mode="json"), tests/test_client_with_live_apis.py:143), and
// the responses and error.debug of the fake-provider scenarios of FM6 to FM9
// and FM11 that adapter/testdata/python/gen_report_cases.py ran through
// upstream's client. Report reads the attempts' bodies (schema,
// llm_response, request) as JSON and writes them back with their member
// order, so they are compared with order too, which is stricter than N5.
// The latency is dropped on both sides. A failed call's Report has the
// probability members upstream's error.debug lacks; they are added to
// upstream's data with their zero values before the comparison.
func TestReportJSONMatchesUpstreamShape(t *testing.T) {
	files := expectedResponses(t)
	if len(files) != 12 {
		t.Fatalf("%d expected responses under %s, want 12", len(files), expectedDir)
	}
	for _, file := range files {
		t.Run("expected/"+strings.TrimSuffix(strings.TrimPrefix(filepath.Base(file), "test_live_responses_match_reference_shape["), "].json"), func(t *testing.T) {
			body, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			sameOrdered(t, usageAndDebug(t, roundTrip(t, body)), usageAndDebug(t, body))
		})
	}
	cases := readReportCases(t)
	if len(cases) != 17 {
		t.Fatalf("%d report cases, want 17 (FM6 4, FM7 7, FM8 1, FM9 1, FM11 4)", len(cases))
	}
	for _, c := range cases {
		t.Run("upstream/"+c.name, func(t *testing.T) {
			var body []byte
			if c.errClass == "" {
				body = []byte(mustMarshalNode(t, c.response))
			} else {
				body = failureBody(t, c.errDebug)
			}
			sameOrdered(t, usageAndDebug(t, roundTrip(t, body)), usageAndDebug(t, body))
		})
	}
}

// expectedResponses returns the paths of upstream's 12 expected responses
// of its recorded runs, without the TypeSafe one.
func expectedResponses(t testing.TB) []string {
	t.Helper()
	entries, err := os.ReadDir(expectedDir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "test_live_responses_match_reference_shape[") {
			files = append(files, filepath.Join(expectedDir, e.Name()))
		}
	}
	return files
}

// mustMarshalNode returns v as compact JSON with its numbers as spelled.
func mustMarshalNode(t *testing.T, v jsonx.Node) string {
	t.Helper()
	b, err := jsonx.Marshal(rawValue(v))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTypeSafeBodyHoldsNoReport(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(expectedDir, "test_live_typesafe_response_matches_reference_shape.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r Report
	if err := r.UnmarshalJSON(body); !errors.Is(err, errReport) {
		t.Fatalf("UnmarshalJSON of TypeSafe's own response: %v, want an error: it has usage but no debug", err)
	}
}

// fullReport returns a Report that uses every member: a corrective retry, a
// failed attempt, a rescaled distribution and an SDK retry.
func fullReport() Report {
	stop := "stop"
	return Report{
		Usage: Usage{
			InputTokens:       llm.Count{N: 12, Known: true},
			InputTokensTotal:  llm.Count{N: 24, Known: true},
			Retries:           1,
			MalformedRetries:  1,
			Latency:           1234567891 * time.Nanosecond,
			OutputTokens:      llm.Count{},
			OutputTokensTotal: llm.Count{},
		},
		Debug: Debug{
			MaxError:              0.25,
			InvalidProbs:          1,
			ProbabilityErrors:     []QuestionValue{{Question: "genre", Value: 0.25}},
			OriginalProbabilities: []QuestionDistribution{{Question: "genre", Probabilities: []LabelValue{{Label: "fiction", Value: 1}, {Label: "nonfiction", Value: 0.25}}}},
			Attempts: []Attempt{
				{
					Messages:   []llm.Message{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}},
					Schema:     []byte(`{"type":"object"}`),
					Structured: true,
					Response:   []byte(`{"id":1.50}`),
					Info:       AttemptInfo{ModelName: "m", Provider: "p", API: "chat_completions", Responded: true, FinishReason: &stop},
					Request:    []byte(`{"model":"m"}`),
				},
				{
					Messages: []llm.Message{{Role: "system", Content: "s"}},
					Schema:   []byte(`{"type":"object"}`),
					Info:     AttemptInfo{ModelName: "m", Provider: "p", Error: "503 unavailable", ErrorType: "TypeSafeInternalServerError"},
				},
				{
					Messages: []llm.Message{{Role: "system", Content: "s"}},
					Response: []byte("NaN"),
					Info:     AttemptInfo{ModelName: "m", Provider: "p", API: "messages", Responded: true, ResponseEncoding: encodingText, RequestEncoding: encodingText},
					Request:  []byte("{\"a\":\x01}"),
				},
			},
			RetryReasons:  []RetryReason{{Category: "malformed_structure", Message: "1 validation error"}, {Category: "provider_error", Message: "503 unavailable"}},
			SDKRetryCount: 2,
		},
	}
}

// TestReportMarshalJSON pins the whole member order, including the members
// upstream does not have and those written only sometimes.
func TestReportMarshalJSON(t *testing.T) {
	got, err := fullReport().MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"usage":{"input_tokens":12,"output_tokens":null,"input_tokens_total":24,"output_tokens_total":null,"n_retries":1,"n_retries_malformed_structure":1,"latency":1.234567891},` +
		`"debug":{"max_error":0.25,"invalid_probs":1,"probability_errors":{"genre":0.25},"original_probabilities":{"genre":{"fiction":1.0,"nonfiction":0.25}},"llm_attempts":[` +
		`{"messages":[{"role":"system","content":"s"},{"role":"user","content":"u"}],"model_request_parameters":{"schema":{"type":"object"},"structured":true},"llm_response":{"id":1.50},"debug_info":{"model_name":"m","provider":"p","api":"chat_completions","finish_reason":"stop"},"request":{"model":"m"}},` +
		`{"messages":[{"role":"system","content":"s"}],"model_request_parameters":{"schema":{"type":"object"},"structured":false},"llm_response":null,"debug_info":{"model_name":"m","provider":"p","error":"503 unavailable","error_type":"TypeSafeInternalServerError"}},` +
		`{"messages":[{"role":"system","content":"s"}],"model_request_parameters":{"schema":null,"structured":false},"llm_response":"NaN","debug_info":{"model_name":"m","provider":"p","api":"messages","finish_reason":null,"llm_response_encoding":"text","request_encoding":"text"},"request":"{\"a\":\u0001}"}],` +
		`"retry_reasons":[["malformed_structure","1 validation error"],["provider_error","503 unavailable"]],"sdk_retry_count":2}}`
	if string(got) != want {
		t.Fatalf("MarshalJSON:\n got: %s\nwant: %s", got, want)
	}
	var back Report
	if err := back.UnmarshalJSON(got); err != nil {
		t.Fatal(err)
	}
	again, err := back.MarshalJSON()
	if err != nil || !bytes.Equal(again, got) {
		t.Fatalf("a second MarshalJSON differs (%v):\n%s", err, again)
	}
}

// TestReportMarshalJSONSuccessShape pins upstream's shape of a first
// attempt that answered: no original_probabilities, no sdk_retry_count, no
// encoding member, empty retry_reasons.
func TestReportMarshalJSONSuccessShape(t *testing.T) {
	r := Report{
		Usage: Usage{InputTokens: llm.Count{N: 11, Known: true}, OutputTokens: llm.Count{N: 7, Known: true}, InputTokensTotal: llm.Count{N: 11, Known: true}, OutputTokensTotal: llm.Count{N: 7, Known: true}},
		Debug: Debug{Attempts: []Attempt{{
			Messages: []llm.Message{{Role: "system", Content: "s"}},
			Schema:   []byte(`{}`),
			Response: []byte(`{"text":"{}","input_tokens":11,"output_tokens":7}`),
			Info:     AttemptInfo{ModelName: "fake-model", Provider: "p"},
		}}},
	}
	got, err := r.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"usage":{"input_tokens":11,"output_tokens":7,"input_tokens_total":11,"output_tokens_total":7,"n_retries":0,"n_retries_malformed_structure":0,"latency":0.0},` +
		`"debug":{"max_error":0.0,"invalid_probs":0,"probability_errors":{},"llm_attempts":[{"messages":[{"role":"system","content":"s"}],"model_request_parameters":{"schema":{},"structured":false},` +
		`"llm_response":{"text":"{}","input_tokens":11,"output_tokens":7},"debug_info":{"model_name":"fake-model","provider":"p"}}],"retry_reasons":[]}}`
	if string(got) != want {
		t.Fatalf("MarshalJSON:\n got: %s\nwant: %s", got, want)
	}
}

// TestReportWritesTheErrorPair checks that error and error_type are
// written together when either is set, as upstream writes both from one
// except, also when str(error) is "".
func TestReportWritesTheErrorPair(t *testing.T) {
	tests := map[string]struct {
		text, class string
		want        string
	}{
		"success: an empty text":    {class: "TypeSafeError", want: `"provider":"p","error":"","error_type":"TypeSafeError"}`},
		"success: an empty class":   {text: "boom", want: `"provider":"p","error":"boom","error_type":""}`},
		"success: both":             {text: "boom", class: "TypeSafeError", want: `"provider":"p","error":"boom","error_type":"TypeSafeError"}`},
		"success: neither, no pair": {want: `"provider":"p"}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := Report{Debug: Debug{Attempts: []Attempt{{Info: AttemptInfo{ModelName: "m", Provider: "p", Error: tt.text, ErrorType: tt.class}}}}}
			out, err := r.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(out, []byte(tt.want)) {
				t.Fatalf("debug_info does not end in %s:\n%s", tt.want, out)
			}
			var back Report
			if err := back.UnmarshalJSON(out); err != nil {
				t.Fatal(err)
			}
			if got := back.Debug.Attempts[0].Info; got.Error != tt.text || got.ErrorType != tt.class {
				t.Fatalf("read back %q, %q", got.Error, got.ErrorType)
			}
		})
	}
}

func TestReportMarshalJSONRefuses(t *testing.T) {
	tests := map[string]struct {
		change  func(*Report)
		wantErr error
	}{
		"error: max_error NaN":                {change: func(r *Report) { r.Debug.MaxError = math.NaN() }, wantErr: jsonx.ErrNonFinite},
		"error: probability error +Inf":       {change: func(r *Report) { r.Debug.ProbabilityErrors[0].Value = math.Inf(1) }, wantErr: jsonx.ErrNonFinite},
		"error: original probability -Inf":    {change: func(r *Report) { r.Debug.OriginalProbabilities[0].Probabilities[1].Value = math.Inf(-1) }, wantErr: jsonx.ErrNonFinite},
		"error: a schema that is not JSON":    {change: func(r *Report) { r.Debug.Attempts[0].Schema = []byte("{") }, wantErr: errReport},
		"error: an unknown response encoding": {change: func(r *Report) { r.Debug.Attempts[0].Info.ResponseEncoding = "base64" }, wantErr: errReport},
		"error: an unknown request encoding":  {change: func(r *Report) { r.Debug.Attempts[0].Info.RequestEncoding = "TEXT" }, wantErr: errReport},
		"error: a question named twice": {change: func(r *Report) {
			r.Debug.ProbabilityErrors = append(r.Debug.ProbabilityErrors, r.Debug.ProbabilityErrors[0])
		}, wantErr: jsonx.ErrInvalid},
		"success: latency just below 2^22 s":    {change: func(r *Report) { r.Usage.Latency = maxLatency - 1 }},
		"success: latency below 0":              {change: func(r *Report) { r.Usage.Latency = -1 }},
		"success: latency of 2^22 seconds":      {change: func(r *Report) { r.Usage.Latency = maxLatency }},
		"success: the largest Duration":         {change: func(r *Report) { r.Usage.Latency = math.MaxInt64 }},
		"success: the smallest Duration":        {change: func(r *Report) { r.Usage.Latency = math.MinInt64 }},
		"success: a negative zero probability":  {change: func(r *Report) { r.Debug.MaxError = math.Copysign(0, -1) }},
		"success: a response encoding of text":  {change: func(r *Report) { r.Debug.Attempts[0].Info.ResponseEncoding = encodingText }},
		"success: a nil schema is written null": {change: func(r *Report) { r.Debug.Attempts[0].Schema = nil }},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := fullReport()
			tt.change(&r)
			out, err := r.MarshalJSON()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("MarshalJSON: %v, want %v", err, tt.wantErr)
			}
			if (err != nil) != (out == nil) {
				t.Fatalf("MarshalJSON returned bytes %q with error %v", out, err)
			}
		})
	}
}

// TestReportBodyForms pins plan 7.4's rule 2 for each hostile class of
// provider body (7.4, line 980), and its additions here: a body is
// embedded as JSON only when it is valid, within 1000 levels, without a
// repeated member name and without an integer longer than json.loads reads;
// any other body is a string of its text, invalid UTF-8 replaced, and
// debug_info says llm_response_encoding "text".
func TestReportBodyForms(t *testing.T) {
	deep := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	tests := map[string]struct {
		body     string
		wantJSON string // the llm_response member as written
		wantText bool   // written as text, with the encoding member
		wantBack string // the Response UnmarshalJSON gives back
	}{
		"json: an object, white space dropped": {body: " {\"a\" : [1.50, \"\\u00e9\"]} ", wantJSON: `{"a":[1.50,"é"]}`, wantBack: `{"a":[1.50,"é"]}`},
		"json: 1000 levels":                    {body: deep(1000), wantJSON: deep(1000), wantBack: deep(1000)},
		"json: U+2028 raw":                     {body: "\"a\u2028b\"", wantJSON: "\"a\u2028b\"", wantBack: "\"a\u2028b\""},
		"json: 4300 digits":                    {body: "[" + strings.Repeat("9", 4300) + "]", wantJSON: "[" + strings.Repeat("9", 4300) + "]", wantBack: "[" + strings.Repeat("9", 4300) + "]"},
		"text: a raw control character":        {body: "{\"a\":\"\x01\"}", wantJSON: `"{\"a\":\"\u0001\"}"`, wantText: true, wantBack: "{\"a\":\"\x01\"}"},
		"text: invalid UTF-8":                  {body: "{\"a\":\"\xff\"}", wantJSON: "\"{\\\"a\\\":\\\"\ufffd\\\"}\"", wantText: true, wantBack: "{\"a\":\"\ufffd\"}"},
		"text: a NaN token":                    {body: `{"a":NaN}`, wantJSON: `"{\"a\":NaN}"`, wantText: true, wantBack: `{"a":NaN}`},
		"text: 1001 levels":                    {body: deep(1001), wantJSON: `"` + deep(1001) + `"`, wantText: true, wantBack: deep(1001)},
		"text: 5000 levels":                    {body: deep(5000), wantJSON: `"` + deep(5000) + `"`, wantText: true, wantBack: deep(5000)},
		"text: 1001 levels of objects":         {body: strings.Repeat(`{"a":`, 1001) + "1" + strings.Repeat("}", 1001), wantJSON: `"` + strings.ReplaceAll(strings.Repeat(`{"a":`, 1001), `"`, `\"`) + "1" + strings.Repeat("}", 1001) + `"`, wantText: true, wantBack: strings.Repeat(`{"a":`, 1001) + "1" + strings.Repeat("}", 1001)},
		"json: null":                           {body: "null", wantJSON: "null", wantBack: "null"},
		"text: a lone surrogate escape":        {body: `"\ud800"`, wantJSON: `"\"\\ud800\""`, wantText: true, wantBack: `"\ud800"`},
		"text: a member named twice":           {body: `{"a":1,"a":2}`, wantJSON: `"{\"a\":1,\"a\":2}"`, wantText: true, wantBack: `{"a":1,"a":2}`},
		"text: 4301 digits":                    {body: strings.Repeat("9", 4301), wantJSON: `"` + strings.Repeat("9", 4301) + `"`, wantText: true, wantBack: strings.Repeat("9", 4301)},
		"text: two values":                     {body: `{}{}`, wantJSON: `"{}{}"`, wantText: true, wantBack: `{}{}`},
		"text: empty":                          {body: "", wantJSON: `""`, wantText: true, wantBack: ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := Report{Debug: Debug{Attempts: []Attempt{{Response: []byte(tt.body), Request: []byte(tt.body), Info: AttemptInfo{Responded: true}}}}}
			out, err := r.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := jsonx.Read(out); err != nil {
				t.Fatalf("the Report is not one JSON text jsonx.Read reads: %v", err)
			}
			root, _ := jsonx.Read(out)
			debug, _ := root.Member("debug")
			attempts, _ := debug.Member("llm_attempts")
			attempt := attempts.Index(0)
			resp, _ := attempt.Member("llm_response")
			if got := mustMarshalNode(t, resp); got != tt.wantJSON {
				t.Fatalf("llm_response %.200s, want %.200s", got, tt.wantJSON)
			}
			info, _ := attempt.Member("debug_info")
			for _, name := range []string{"llm_response_encoding", "request_encoding"} {
				enc, ok := info.Member(name)
				if ok != tt.wantText || (ok && enc.Text() != encodingText) {
					t.Fatalf("%s present %v (%q), want %v", name, ok, enc.Text(), tt.wantText)
				}
			}
			var back Report
			if err := back.UnmarshalJSON(out); err != nil {
				t.Fatal(err)
			}
			a := back.Debug.Attempts[0]
			wantResponse := tt.wantBack
			if tt.body == "null" {
				// A response body null reads back as no response: llm_response
				// is null either way. A request member is present only when a
				// request was recorded, so its null is the body "null".
				wantResponse = ""
			}
			if string(a.Response) != wantResponse || string(a.Request) != tt.wantBack {
				t.Fatalf("read back %.200q and %.200q, want %.200q and %.200q", a.Response, a.Request, wantResponse, tt.wantBack)
			}
			if want := map[bool]string{true: encodingText}[tt.wantText]; a.Info.ResponseEncoding != want || a.Info.RequestEncoding != want {
				t.Fatalf("read back encodings %q and %q, want %q", a.Info.ResponseEncoding, a.Info.RequestEncoding, want)
			}
		})
	}
}

func TestReportUnmarshalJSONRefuses(t *testing.T) {
	good, err := fullReport().MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	replace := func(from, to string) string {
		if !strings.Contains(string(good), from) {
			t.Fatalf("%q is not in the Report", from)
		}
		return strings.Replace(string(good), from, to, 1)
	}
	// The usage and debug members of the good body, each valid on its own,
	// so that a body with only one of them is refused for the other's
	// absence alone.
	usage, debug, ok := strings.Cut(strings.TrimPrefix(string(good), `{"usage":`), `,"debug":`)
	debug = strings.TrimSuffix(debug, "}")
	if !ok || `{"usage":`+usage+`,"debug":`+debug+`}` != string(good) {
		t.Fatalf("the good body does not split into usage and debug: %s", good)
	}
	var whole Report
	if err := whole.UnmarshalJSON([]byte(`{"debug":` + debug + `,"usage":` + usage + `}`)); err != nil {
		t.Fatalf("the two members in the other order: %v", err)
	}
	tests := map[string]string{
		"not JSON":                         `{"usage":`,
		"not an object":                    `[]`,
		"no usage":                         `{"debug":` + debug + `}`,
		"no debug":                         `{"usage":` + usage + `}`,
		"usage not an object":              replace(`"usage":{`, `"usage":[{`),
		"a count that is negative":         replace(`"input_tokens":12`, `"input_tokens":-12`),
		"a count that is a float":          replace(`"input_tokens":12`, `"input_tokens":12.0`),
		"a count that is a string":         replace(`"input_tokens":12`, `"input_tokens":"12"`),
		"n_retries absent":                 replace(`"n_retries":1,`, ``),
		"n_retries a float":                replace(`"n_retries":1,`, `"n_retries":1.5,`),
		"latency a string":                 replace(`"latency":1.234567891`, `"latency":"1"`),
		"latency beyond a Duration":        replace(`"latency":1.234567891`, `"latency":1e300`),
		"latency an overflowing literal":   replace(`"latency":1.234567891`, `"latency":1e400`),
		"latency of 2^63 ns":               replace(`"latency":1.234567891`, `"latency":9.223372036854776e9`),
		"latency below -2^63 ns":           replace(`"latency":1.234567891`, `"latency":-9.223372036854778e9`),
		"max_error absent":                 replace(`"max_error":0.25,`, ``),
		"a probability error a string":     replace(`"probability_errors":{"genre":0.25}`, `"probability_errors":{"genre":"x"}`),
		"a distribution not an object":     replace(`{"genre":{"fiction":1.0,"nonfiction":0.25}}`, `{"genre":[1.0]}`),
		"llm_attempts absent":              replace(`"llm_attempts":`, `"attempts":`),
		"an attempt not an object":         replace(`"llm_attempts":[`, `"llm_attempts":[1,`),
		"a message not an object":          replace(`"messages":[{"role":"system","content":"s"},`, `"messages":["s",`),
		"a message without content":        replace(`{"role":"user","content":"u"}`, `{"role":"user"}`),
		"structured not a bool":            replace(`"structured":true`, `"structured":1`),
		"provider absent":                  replace(`"provider":"p","api":"chat_completions"`, `"api":"chat_completions"`),
		"finish_reason a number":           replace(`"finish_reason":"stop"`, `"finish_reason":1`),
		"an unknown encoding":              replace(`"llm_response_encoding":"text"`, `"llm_response_encoding":"hex"`),
		"a text body that is not a string": replace(`"llm_response":"NaN"`, `"llm_response":1`),
		"a retry reason not a pair":        replace(`["provider_error","503 unavailable"]`, `["provider_error"]`),
		"a retry reason not text":          replace(`["provider_error","503 unavailable"]`, `["provider_error",503]`),
		"sdk_retry_count a string":         replace(`"sdk_retry_count":2`, `"sdk_retry_count":"2"`),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			r := fullReport()
			before := fmt.Sprint(r)
			if err := r.UnmarshalJSON([]byte(body)); !errors.Is(err, errReport) {
				t.Fatalf("UnmarshalJSON: %v, want an error wrapping errReport", err)
			}
			if fmt.Sprint(r) != before {
				t.Fatal("a failed UnmarshalJSON changed the Report")
			}
		})
	}
}

// TestReportUnmarshalJSONReadsABody reads the two Adapter bodies and
// ignores the members that are not the Report's, at every level.
func TestReportUnmarshalJSONReadsABody(t *testing.T) {
	report, err := fullReport().MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	inner := string(report[1 : len(report)-1])
	tests := map[string]string{
		"a success body": `{"model":"m",` + strings.Replace(inner, `"debug":{`, `"answers":{"q":{"type":"noul","noul":0.5}},"debug":{"extra":[1],`, 1) + `}`,
		"an error body":  `{"detail":{"message":"x","error_type":"provider_status"},` + strings.Replace(inner, `"latency":`, `"other":true,"latency":`, 1) + `}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			var r Report
			if err := r.UnmarshalJSON([]byte(body)); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(fullReport(), r); diff != "" {
				t.Fatalf("Report (-want +got):\n%s", diff)
			}
		})
	}
}

// TestLatencyRoundTrip pins what a reader gets for Usage.Latency: the
// seconds MarshalJSON writes read back to the same Duration while its size
// is below 2^22 seconds, checked here at both ends of that range, either
// side of 0, and around every power of two (a sample of 10^7 values below
// 3*10^15 ns, and one of 10^7 negative values, found no exception); beyond
// it, to a Duration within |d|*2^-51 (a sample of 10^7 values found at
// most |d|*2.39e-16); and from 2^63-512 ns on, to nothing.
func TestLatencyRoundTrip(t *testing.T) {
	back := func(d time.Duration) (time.Duration, error) {
		out, err := Report{Usage: Usage{Latency: d}}.MarshalJSON()
		if err != nil {
			return 0, err
		}
		var r Report
		if err := r.UnmarshalJSON(out); err != nil {
			return 0, err
		}
		return r.Usage.Latency, nil
	}
	var ds []time.Duration
	for d := range time.Duration(2000) {
		ds = append(ds, d, -d, maxLatency-1-d, -(maxLatency - 1 - d))
	}
	for p := time.Duration(1); p < maxLatency; p *= 2 {
		ds = append(ds, p-1, p, p+1, 1-p, -p, -p-1)
	}
	for _, d := range ds {
		got, err := back(d)
		if err != nil || got != d {
			t.Fatalf("Latency %d ns reads back as %d ns (%v)", int64(d), int64(got), err)
		}
	}
	// 4194304811041337 ns is the smallest of the values in [0, 5e15) that
	// a sample of 10^7 found not to read back: seconds of 22 integer bits
	// leave too few fraction bits for every nanosecond.
	const outside = 4194304811041337 * time.Nanosecond
	for _, d := range []time.Duration{outside, -outside} {
		if got, err := back(d); err != nil || got == d {
			t.Fatalf("%d ns reads back as %d ns (%v); the exact range could be wider than documented", int64(d), int64(got), err)
		}
	}
	var far []time.Duration
	for p := maxLatency; p > 0 && p < firstUnreadableLatency; p *= 2 {
		far = append(far, p-1, p+1, p+p/3, -p+1, -p-1, -p-p/3)
	}
	far = append(far, firstUnreadableLatency-1, math.MinInt64, math.MinInt64+1)
	for _, d := range far {
		got, err := back(d)
		if err != nil || math.Abs(float64(got-d)) > math.Abs(float64(d))*0x1p-51 {
			t.Fatalf("Latency %d ns reads back as %d ns (%v), beyond |d|*2^-51", int64(d), int64(got), err)
		}
	}
	if got, err := back(math.MinInt64); err != nil || got != math.MinInt64 {
		t.Fatalf("the smallest Duration reads back as %d ns (%v)", int64(got), err)
	}
	for _, d := range []time.Duration{firstUnreadableLatency, firstUnreadableLatency + 1, math.MaxInt64} {
		if got, err := back(d); !errors.Is(err, errReport) {
			t.Fatalf("Latency %d ns reads back as %d ns (%v), want UnmarshalJSON to refuse 2^63 ns", int64(d), int64(got), err)
		}
	}
}

// carrierError is the test's stand-in for the Adapter's own error type,
// which carries the Report of the call it ended.
type carrierError struct{ report *Report }

func (e *carrierError) Error() string          { return "adapter: provider_error from p model m" }
func (e *carrierError) adapterReport() *Report { return e.report }

// noCarrierError holds a Report but lacks adapterReport, as an error type
// that forgot the method would: ReportFromError must not find it.
type noCarrierError struct{ Report *Report }

func (e *noCarrierError) Error() string { return "adapter: no method" }

func TestReportFromError(t *testing.T) {
	report := fullReport()
	body, err := report.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	errorBody := []byte(`{"detail":{"message":"unavailable","error_type":"provider_status"},` + string(body[1:]))
	tests := map[string]struct {
		err  error
		want bool
	}{
		"success: the Adapter's error":                       {err: &carrierError{report: &report}, want: true},
		"success: the Adapter's error, wrapped":              {err: fmt.Errorf("call: %w", &carrierError{report: &report}), want: true},
		"success: an APIError's body":                        {err: &typesafe.APIError{StatusCode: 503, Body: errorBody}, want: true},
		"success: an APIError's body, wrapped":               {err: fmt.Errorf("x: %w", &typesafe.APIError{StatusCode: 424, Body: errorBody}), want: true},
		"success: a ResponseValidationError's body":          {err: &typesafe.ResponseValidationError{StatusCode: 200, Body: body}, want: true},
		"success: a ResponseValidationError's body, wrapped": {err: fmt.Errorf("x: %w", &typesafe.ResponseValidationError{StatusCode: 200, Body: body}), want: true},
		"success: both, the Adapter's error first":           {err: errors.Join(&carrierError{report: &report}, &typesafe.APIError{Body: []byte(`{}`)}), want: true},
		"error: nil":           {err: nil},
		"error: another error": {err: errors.New("x")},
		"error: the Adapter's error without a Report":          {err: &carrierError{}},
		"error: an error type without the method":              {err: &noCarrierError{Report: &report}},
		"error: an APIError before an evaluation started":      {err: &typesafe.APIError{StatusCode: 400, Body: []byte(`{"detail":{"message":"bad","error_type":"invalid_body"}}`)}},
		"error: an APIError whose body the SDK did not keep":   {err: &typesafe.APIError{StatusCode: 503}},
		"error: an APIError from TypeSafe":                     {err: &typesafe.APIError{StatusCode: 404, Body: []byte(`{"detail":"Not Found"}`)}},
		"error: a ResponseValidationError of another server":   {err: &typesafe.ResponseValidationError{StatusCode: 200, Body: []byte(`{"model":"m","usage":{"input_tokens":1,"output_tokens":1},"answers":null}`)}},
		"error: a body with usage and debug of the wrong kind": {err: &typesafe.APIError{StatusCode: 503, Body: []byte(`{"usage":{},"debug":[]}`)}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := ReportFromError(tt.err)
			if ok != tt.want || (got != nil) != tt.want {
				t.Fatalf("ReportFromError = %v, %v; want ok %v", got, ok, tt.want)
			}
			if ok {
				if diff := cmp.Diff(report, *got); diff != "" {
					t.Fatalf("Report (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// bodyTransport answers every request with one status and body, in
// process: the SDK client under test never reaches a network.
type bodyTransport struct {
	status int
	body   []byte
}

func (b *bodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	return &http.Response{
		StatusCode:    b.status,
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(b.body)),
		ContentLength: int64(len(b.body)),
		Request:       req,
	}, nil
}

// sdkCall makes one System One call through a TypeSafe client whose
// transport answers with status and body, with a made-up key and no retry.
func sdkCall(t *testing.T, status int, body []byte) (*typesafe.SystemOneResponse, error) {
	t.Helper()
	client, err := typesafe.NewClient(
		typesafe.WithAPIKey("madeupword"),
		typesafe.WithRoundTripper(&bodyTransport{status: status, body: body}),
		typesafe.WithRetry(typesafe.NoRetry()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	questions, err := typesafe.NewQuestions().Noul("positive", typesafe.Noul{Instructions: typesafe.Text("The review is positive.")}).Prepare()
	if err != nil {
		t.Fatal(err)
	}
	return client.SystemOne(t.Context(), "A review.", questions)
}

// TestReportOfThroughTheSDK reads the Report of an answer the SDK returned,
// and of the two errors that carry one in their body.
func TestReportOfThroughTheSDK(t *testing.T) {
	file := filepath.Join(expectedDir, "test_live_responses_match_reference_shape[probabilities-prompted-anthropic].json")
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var want Report
	if err := want.UnmarshalJSON(body); err != nil {
		t.Fatal(err)
	}

	t.Run("success: an answer", func(t *testing.T) {
		resp, err := sdkCall(t, http.StatusOK, body)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ReportOf(resp)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(want, *got); diff != "" {
			t.Fatalf("ReportOf (-want +got):\n%s", diff)
		}
		if len(got.Debug.Attempts) != 1 || got.Usage.InputTokens != (llm.Count{N: 720, Known: true}) {
			t.Fatalf("ReportOf read %d attempts and %v input tokens", len(got.Debug.Attempts), got.Usage.InputTokens)
		}
	})

	report := fullReport()
	members, err := report.members()
	if err != nil {
		t.Fatal(err)
	}
	marshalBody := func(head ...jsonx.Member) []byte {
		b, err := jsonx.Marshal(jsonx.Object(append(head, members...)...))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	t.Run("success: a provider status error", func(t *testing.T) {
		_, err := sdkCall(t, http.StatusFailedDependency, marshalBody(jsonx.Member{Name: "detail", Value: jsonx.Object(
			jsonx.Member{Name: "message", Value: jsonx.String("unavailable")},
			jsonx.Member{Name: "error_type", Value: jsonx.String("non_answer")},
		)}))
		if _, ok := errors.AsType[*typesafe.APIError](err); !ok {
			t.Fatalf("SystemOne: %v, want *typesafe.APIError", err)
		}
		got, ok := ReportFromError(err)
		if !ok {
			t.Fatal("ReportFromError found no Report")
		}
		if diff := cmp.Diff(report, *got); diff != "" {
			t.Fatalf("ReportFromError (-want +got):\n%s", diff)
		}
	})
	t.Run("success: malformed output after the corrective retries", func(t *testing.T) {
		_, err := sdkCall(t, http.StatusOK, marshalBody(
			jsonx.Member{Name: "model", Value: jsonx.String("fake-model")},
			jsonx.Member{Name: "answers", Value: jsonx.Value{}},
		))
		if _, ok := errors.AsType[*typesafe.ResponseValidationError](err); !ok {
			t.Fatalf("SystemOne: %v, want *typesafe.ResponseValidationError", err)
		}
		got, ok := ReportFromError(err)
		if !ok {
			t.Fatal("ReportFromError found no Report")
		}
		if diff := cmp.Diff(report, *got); diff != "" {
			t.Fatalf("ReportFromError (-want +got):\n%s", diff)
		}
	})
	t.Run("error: an answer of another server", func(t *testing.T) {
		resp, err := sdkCall(t, http.StatusOK, []byte(`{"model":"jev","usage":{"input_tokens":1,"output_tokens":1},"answers":{"positive":{"type":"noul","noul":0.5}}}`))
		if err != nil {
			t.Fatal(err)
		}
		if r, err := ReportOf(resp); !errors.Is(err, errReport) || r != nil {
			t.Fatalf("ReportOf = %v, %v; want an error", r, err)
		}
	})
	t.Run("error: no response", func(t *testing.T) {
		if r, err := ReportOf(nil); !errors.Is(err, errReport) || r != nil {
			t.Fatalf("ReportOf(nil) = %v, %v; want an error", r, err)
		}
	})
}

// fuzzInput hands out the fuzz input in pieces: each chunk is preceded by
// its length in one byte, and an exhausted input gives empty chunks.
type fuzzInput struct{ data []byte }

func (in *fuzzInput) chunk() []byte {
	if len(in.data) == 0 {
		return nil
	}
	n := min(int(in.data[0]), len(in.data)-1)
	c := in.data[1 : 1+n]
	in.data = in.data[1+n:]
	return c
}

func (in *fuzzInput) octet() byte {
	if c := in.chunk(); len(c) > 0 {
		return c[0]
	}
	return 0
}

func (in *fuzzInput) word() uint64 {
	var b [8]byte
	copy(b[:], in.chunk())
	return binary.LittleEndian.Uint64(b[:])
}

func (in *fuzzInput) number() float64 { return math.Float64frombits(in.word()) }

func (in *fuzzInput) text() string { return string(in.chunk()) }

// body returns nil, an empty body or a chunk, by the next byte.
func (in *fuzzInput) body() []byte {
	switch in.octet() % 4 {
	case 0:
		return nil
	case 1:
		return []byte{}
	}
	return slices.Clone(in.chunk())
}

// unique returns name, made unique among seen in the form MarshalJSON
// writes names: each byte that is not part of valid UTF-8 replaced by
// U+FFFD, as converting the string to runes replaces it. Two names that
// differ only in such bytes are one name in the JSON.
func unique(seen map[string]bool, name string) string {
	for seen[string([]rune(name))] {
		name += "'"
	}
	seen[string([]rune(name))] = true
	return name
}

// maxLatency bounds the Latencies that come back exact: 2^22 seconds.
// Below it, the seconds float64(d)/1e9 written by ReprFloat and read back
// as math.Round(s*1e9) give d: s is the double nearest d/1e9, whose
// shortest digits read back to s, and the two roundings of s and of s*1e9
// stay below half a nanosecond together while s*1e9 is below 2^52. The
// operations are symmetric in the sign, so the same holds above -2^22 s.
const maxLatency = (1 << 22) * time.Second

// firstUnreadableLatency is the smallest Latency that UnmarshalJSON cannot
// read back: from it on, float64(d) rounds to 2^63, beyond a Duration.
const firstUnreadableLatency = time.Duration(1<<63 - 512)

// fuzzReport builds a Report from the fuzz input. Every float is finite:
// a non-finite one is noted in nonFinite and replaced by 0, so that the
// target checks the refusal and goes on with a Report MarshalJSON accepts.
func fuzzReport(data []byte, latency int64) (r Report, nonFinite bool) {
	in := &fuzzInput{data: data}
	finite := func(f float64) float64 {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			nonFinite = true
			return 0
		}
		return f
	}
	count := func() llm.Count {
		n := in.word()
		if n%3 == 0 {
			return llm.Count{}
		}
		return llm.Count{N: n, Known: true}
	}
	r.Usage = Usage{
		InputTokens: count(), OutputTokens: count(), InputTokensTotal: count(), OutputTokensTotal: count(),
		Retries: int(in.octet()) - 128, MalformedRetries: int(in.octet()),
		Latency: time.Duration(latency),
	}
	r.Debug.MaxError = finite(in.number())
	r.Debug.InvalidProbs = int(in.octet())
	seen := map[string]bool{}
	for range in.octet() % 4 {
		r.Debug.ProbabilityErrors = append(r.Debug.ProbabilityErrors, QuestionValue{Question: unique(seen, in.text()), Value: finite(in.number())})
	}
	seen = map[string]bool{}
	for range in.octet() % 3 {
		q := QuestionDistribution{Question: unique(seen, in.text())}
		labels := map[string]bool{}
		for range in.octet() % 4 {
			q.Probabilities = append(q.Probabilities, LabelValue{Label: unique(labels, in.text()), Value: finite(in.number())})
		}
		r.Debug.OriginalProbabilities = append(r.Debug.OriginalProbabilities, q)
	}
	for range in.octet() % 4 {
		a := Attempt{Structured: in.octet()%2 == 1, Response: in.body(), Request: in.body()}
		for range in.octet() % 5 {
			a.Messages = append(a.Messages, llm.Message{Role: llm.Role(in.text()), Content: in.text()})
		}
		if s := in.chunk(); embeddable(s) {
			a.Schema = slices.Clone(s)
		}
		flags := in.octet()
		a.Info = AttemptInfo{ModelName: in.text(), Provider: in.text(), API: in.text(), Responded: flags&1 != 0, Error: in.text(), ErrorType: in.text()}
		if flags&2 != 0 {
			reason := in.text()
			a.Info.FinishReason = &reason
		}
		if flags&4 != 0 {
			a.Info.ResponseEncoding = encodingText
		}
		if flags&8 != 0 {
			a.Info.RequestEncoding = encodingText
		}
		r.Debug.Attempts = append(r.Debug.Attempts, a)
	}
	for range in.octet() % 4 {
		r.Debug.RetryReasons = append(r.Debug.RetryReasons, RetryReason{Category: in.text(), Message: in.text()})
	}
	r.Debug.SDKRetryCount = int(in.octet() % 3)
	return r, nonFinite
}

// canonical reports whether r is a Report UnmarshalJSON gives back equal:
// its strings valid UTF-8, each body nil or in the form MarshalJSON keeps
// (compact JSON as the JSON library writes it, or text with its encoding
// set), a finish reason only where one is written.
func canonical(r *Report) bool {
	valid := func(ss ...string) bool {
		for _, s := range ss {
			if !utf8.ValidString(s) {
				return false
			}
		}
		return true
	}
	kept := func(body []byte, encoding string) bool {
		switch {
		case body == nil:
			return encoding == ""
		case encoding == encodingText:
			return utf8.Valid(body)
		case !embeddable(body) || string(body) == "null":
			return false
		}
		out, err := jsonx.Marshal(jsonx.Raw(body))
		return err == nil && bytes.Equal(out, body)
	}
	for _, e := range r.Debug.ProbabilityErrors {
		if !valid(e.Question) {
			return false
		}
	}
	for _, q := range r.Debug.OriginalProbabilities {
		if !valid(q.Question) {
			return false
		}
		for _, l := range q.Probabilities {
			if !valid(l.Label) {
				return false
			}
		}
	}
	for _, a := range r.Debug.Attempts {
		for _, m := range a.Messages {
			if !valid(string(m.Role), m.Content) {
				return false
			}
		}
		if !valid(a.Info.ModelName, a.Info.Provider, a.Info.API, a.Info.Error, a.Info.ErrorType) ||
			(a.Info.FinishReason != nil && (!a.Info.Responded || !valid(*a.Info.FinishReason))) ||
			!kept(a.Response, a.Info.ResponseEncoding) || !kept(a.Schema, "") || !kept(a.Request, a.Info.RequestEncoding) {
			return false
		}
	}
	for _, reason := range r.Debug.RetryReasons {
		if !valid(reason.Category, reason.Message) {
			return false
		}
	}
	return true
}

// seedChunks returns fuzz input whose chunks are the given pieces.
func seedChunks(pieces ...[]byte) []byte {
	var out []byte
	for _, p := range pieces {
		n := min(len(p), math.MaxUint8)
		out = append(out, byte(n))
		out = append(out, p[:n]...)
	}
	return out
}

// FuzzReportJSON checks the Report's JSON on Reports built from the fuzz
// input (mode 1) and read from a body (mode 0): MarshalJSON succeeds and
// writes one JSON text jsonx.Read reads; UnmarshalJSON reads it (and
// refuses it for a Latency of 2^63-512 ns or more), with the Latency exact
// below 2^22 s either side of 0 and within |d|*2^-51 beyond, and a second
// MarshalJSON of what it read, with the Latency put back, writes the same
// bytes; a canonical Report comes back equal; a NaN or an infinity in a
// float member makes MarshalJSON fail.
func FuzzReportJSON(f *testing.F) {
	for _, file := range expectedResponses(f) {
		// The usage and debug members of each expected response.
		body, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		root, err := jsonx.Read(body)
		if err != nil {
			f.Fatal(err)
		}
		usage, _ := root.Member("usage")
		debug, _ := root.Member("debug")
		seed, err := jsonx.Marshal(jsonx.Object(jsonx.Member{Name: "usage", Value: rawValue(usage)}, jsonx.Member{Name: "debug", Value: rawValue(debug)}))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(byte(0), seed, int64(0))
	}
	b := func(s string) []byte { return []byte(s) }
	for i, hostile := range [][]byte{b("{\"a\":\"\x01\"}"), b("\"\xff\""), b(`[NaN]`), b(strings.Repeat("[", 120) + strings.Repeat("]", 120)), b("\"\u2028\""), b(`"\ud800"`)} {
		// fuzzReport's order: four counts, the two retry counts, max_error,
		// invalid_probs, two empty lists, then one attempt whose response,
		// request and message content are the hostile bytes.
		seed := seedChunks(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			[]byte{1}, []byte{1}, []byte{2}, hostile, []byte{2}, hostile, []byte{1}, b("user"), hostile,
			b(`{"type":"object"}`), []byte{1}, b("m"), b("p"))
		f.Add(byte(1), seed, int64(i)*1_000_000_007)
	}
	// A body of 1500 levels, which rule 2 writes as text. It is longer than
	// a chunk of the built Reports can be, so it enters as a read body.
	deepBody := b(strings.Repeat("[", 1500) + strings.Repeat("]", 1500))
	deep, err := Report{Debug: Debug{Attempts: []Attempt{{Response: deepBody, Request: deepBody, Info: AttemptInfo{ModelName: "m", Provider: "p"}}}}}.MarshalJSON()
	if err != nil || !bytes.Contains(deep, b(`"llm_response_encoding":"text","request_encoding":"text"`)) {
		f.Fatalf("the deep seed: %v\n%.300s", err, deep)
	}
	f.Add(byte(0), deep, int64(0))
	f.Add(byte(1), []byte{}, int64(-1))
	f.Add(byte(1), seedChunks(nil, nil, nil, nil, nil, nil, b("\x00\x00\x00\x00\x00\x00\xf8\x7f")), int64(0)) // a NaN max_error
	// Two question names that differ only in invalid UTF-8 bytes, one
	// name once written.
	f.Add(byte('_'), []byte("\x00\x00\x00\x00\x00\x00\x00\x010\x0520000\x01\x9fn000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000\xb7"), int64(3999999930))
	f.Fuzz(func(t *testing.T, mode byte, data []byte, latency int64) {
		var r Report
		if mode%2 == 0 {
			if r.UnmarshalJSON(data) != nil {
				return
			}
			// A body may hold what MarshalJSON refuses: a schema it does
			// not embed.
			for i := range r.Debug.Attempts {
				if a := &r.Debug.Attempts[i]; a.Schema != nil && !embeddable(a.Schema) {
					a.Schema = nil
				}
			}
		} else {
			var nonFinite bool
			r, nonFinite = fuzzReport(data, latency)
			if nonFinite {
				bad := r
				bad.Debug.MaxError = math.NaN()
				if out, err := bad.MarshalJSON(); err == nil || out != nil {
					t.Fatalf("MarshalJSON of a NaN max_error: %q, %v; want an error", out, err)
				}
			}
		}
		out, err := r.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON: %v", err)
		}
		if _, err := jsonx.Read(out); err != nil {
			t.Fatalf("jsonx.Read of the Report: %v\n%.500s", err, out)
		}
		var back Report
		err = back.UnmarshalJSON(out)
		if d := r.Usage.Latency; d >= firstUnreadableLatency {
			if err == nil {
				t.Fatalf("UnmarshalJSON read a latency written for %d ns as %d ns, want a refusal", int64(d), int64(back.Usage.Latency))
			}
			return
		}
		if err != nil {
			t.Fatalf("UnmarshalJSON: %v\n%.500s", err, out)
		}
		// The latency comes back exact within 2^22 s, and within |d|*2^-51
		// beyond; the rest of the body is a fixed point.
		switch d, got := r.Usage.Latency, back.Usage.Latency; {
		case d > -maxLatency && d < maxLatency:
			if got != d {
				t.Fatalf("latency %d ns reads back as %d ns", int64(d), int64(got))
			}
		case math.Abs(float64(got-d)) > math.Abs(float64(d))*0x1p-51:
			t.Fatalf("latency %d ns reads back as %d ns, beyond |d|*2^-51", int64(d), int64(got))
		}
		back.Usage.Latency = r.Usage.Latency
		again, err := back.MarshalJSON()
		if err != nil {
			t.Fatalf("second MarshalJSON: %v", err)
		}
		if !bytes.Equal(again, out) {
			t.Fatalf("the second MarshalJSON differs:\nfirst:  %.500s\nsecond: %.500s", out, again)
		}
		if canonical(&r) {
			if diff := cmp.Diff(r, back); diff != "" {
				t.Fatalf("a canonical Report does not come back equal (-built +read):\n%s", diff)
			}
		}
	})
}
