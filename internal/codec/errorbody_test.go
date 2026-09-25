//go:build !go1.28 && (amd64 || arm64)

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

package codec

import (
	"strings"
	"testing"
	"unicode/utf8"

	gocmp "github.com/google/go-cmp/cmp"
)

// TestReadErrorBody checks the lenient error-body reader against the Python
// SDK 0.7.1 (_spikes/w2.0/results/python-errors.txt, which prints str() of
// the APIError the SDK builds for each body): the message it finds, "no
// body" for an empty or null body, and the replacement of ill-formed UTF-8.
// The reader neither escapes nor cuts; the root package renders the message
// (the Python SDK cuts only a JSON body without a message, at 200
// characters, and the Go port cuts every message, NF7).
func TestReadErrorBody(t *testing.T) {
	long := strings.Repeat("x", 201)
	tests := map[string]struct {
		body string
		want ErrorBody
	}{
		"success: empty":                         {body: "", want: ErrorBody{NoBody: true}},
		"success: null":                          {body: "null", want: ErrorBody{NoBody: true}},
		"success: empty array":                   {body: "[]", want: ErrorBody{Message: "[]"}},
		"success: number":                        {body: "42", want: ErrorBody{Message: "42"}},
		"success: boolean":                       {body: "true", want: ErrorBody{Message: "true"}},
		"success: not JSON, invalid UTF-8":       {body: "not JSON: \xff", want: ErrorBody{Message: "not JSON: \ufffd"}},
		"success: long plain text, not cut":      {body: long, want: ErrorBody{Message: long}},
		"success: long JSON body, not cut":       {body: `{"unknown":"` + long + `"}`, want: ErrorBody{Message: `{"unknown":"` + long + `"}`}},
		"success: an empty error stops":          {body: `{"error":"","message":"ignored"}`, want: ErrorBody{Message: `{"error":"","message":"ignored"}`}},
		"success: detail entries without a msg":  {body: `{"detail":[null,42,{"msg":4}]}`, want: ErrorBody{Message: `{"detail":[null,42,{"msg":4}]}`}},
		"success: error first":                   {body: `{"error":"error","message":"message","detail":"detail"}`, want: ErrorBody{Message: "error"}},
		"success: error.message":                 {body: `{"error":{"message":"nested error"},"message":"message"}`, want: ErrorBody{Message: "nested error"}},
		"success: message before detail":         {body: `{"message":"message","detail":"detail"}`, want: ErrorBody{Message: "message"}},
		"success: detail":                        {body: `{"detail":"detail"}`, want: ErrorBody{Message: "detail"}},
		"success: detail.message":                {body: `{"detail":{"message":"nested detail"}}`, want: ErrorBody{Message: "nested detail"}},
		"success: detail list with loc":          {body: `{"detail":[{"loc":["body","questions","q","score","criteria",0],"msg":"Invalid"},{"msg":"Missing"},{}]}`, want: ErrorBody{Message: "questions.q.score.criteria.0: Invalid; Missing"}},
		"success: plain text":                    {body: "plain text", want: ErrorBody{Message: "plain text"}},
		"success: an object without a message":   {body: `{"unexpected":true}`, want: ErrorBody{Message: `{"unexpected":true}`}},
		"success: an empty JSON string":          {body: `""`, want: ErrorBody{Message: ""}},
		"success: a JSON string":                 {body: `"plain"`, want: ErrorBody{Message: "plain"}},
		"success: whitespace only is text":       {body: "   ", want: ErrorBody{Message: "   "}},
		"success: the last duplicate message":    {body: `{"message":"a","message":"b"}`, want: ErrorBody{Message: "b"}},
		"success: loc items as Python spells":    {body: `{"detail":[{"loc":["body",null,true,false,-0,7],"msg":"m"}]}`, want: ErrorBody{Message: "None.True.False.0.7: m"}},
		"success: detail.error_type":             {body: `{"detail":{"message":"denied","error_type":"authentication_error"}}`, want: ErrorBody{Message: "denied", ErrorType: "authentication_error"}},
		"success: a top-level error_type is not": {body: `{"message":"denied","error_type":"authentication_error"}`, want: ErrorBody{Message: "denied"}},
		"success: pretty JSON is compacted":      {body: "{\n  \"a\" : 1 }", want: ErrorBody{Message: `{"a":1}`}},
		"success: a raw control character":       {body: "{\"a\":\"\x01\"}", want: ErrorBody{Message: "{\"a\":\"\x01\"}"}},
		"success: a null error is not a message": {body: `{"error":null,"message":"m"}`, want: ErrorBody{Message: "m"}},
		"success: an empty detail list":          {body: `{"detail":[]}`, want: ErrorBody{Message: `{"detail":[]}`}},
		"success: a detail list of empty msgs":   {body: `{"detail":[{"msg":""}]}`, want: ErrorBody{Message: `{"detail":[{"msg":""}]}`}},
		"success: numbers keep their spelling":   {body: `{"n":1e400,"m":-0.0}`, want: ErrorBody{Message: `{"n":1e400,"m":-0.0}`}},
		"success: trailing data is text":         {body: `{"message":"m"} x`, want: ErrorBody{Message: `{"message":"m"} x`}},

		// Ill-formed UTF-8: one U+FFFD per maximal subpart, as Python.
		"success: truncated three-byte sequence": {body: "\xe2\x82A", want: ErrorBody{Message: "\ufffdA"}},
		"success: encoded surrogate":             {body: "\xed\xa0\x80", want: ErrorBody{Message: "\ufffd\ufffd\ufffd"}},
		"success: truncated four-byte sequence":  {body: "\xf0\x9f", want: ErrorBody{Message: "\ufffd"}},
		"success: overlong two-byte sequence":    {body: "\xc0\xaf", want: ErrorBody{Message: "\ufffd\ufffd"}},
		"success: bytes that never start one":    {body: "\xff\xfe", want: ErrorBody{Message: "\ufffd\ufffd"}},
		"success: past U+10FFFF":                 {body: "\xf4\x90\x80\x80", want: ErrorBody{Message: "\ufffd\ufffd\ufffd\ufffd"}},
		"success: overlong three-byte sequence":  {body: "\xe0\x80\x80", want: ErrorBody{Message: "\ufffd\ufffd\ufffd"}},
		"success: truncated at the end":          {body: "ok\xf0\x9f\x98", want: ErrorBody{Message: "ok\ufffd"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := []byte(tt.body)
			got := ReadErrorBody(body)
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ReadErrorBody(%q) (-want +got):\n%s", tt.body, diff)
			}
			for i := range body {
				body[i] = '#'
			}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the result changed with the body (-want +got):\n%s", diff)
			}
		})
	}
}

// FuzzErrorBody checks that the lenient error-body reader never fails or
// panics, always returns valid UTF-8, reports "no body" only for an empty
// or null body, and returns the same result for the same bytes.
func FuzzErrorBody(f *testing.F) {
	for _, seed := range []string{
		"", "null", " null ", "[]", "42", "true", `""`, `"plain"`, "plain text", "not JSON: \xff",
		`{"error":"e"}`, `{"error":{"message":"m"}}`, `{"message":"m"}`, `{"detail":"d"}`,
		`{"detail":{"message":"m","error_type":"authentication_error"}}`,
		`{"detail":[{"loc":["body","q",0,null,true],"msg":"m"},{"msg":"n"},{}]}`,
		`{"detail":[null,42,{"msg":4}]}`, `{"error":"","message":"ignored"}`,
		"{\n \"a\" : [ 1 , 2 ] }", "{\"a\":\"\x01\"}", "\xe2\x82A", "\xed\xa0\x80", "ok\xf0\x9f\x98",
		strings.Repeat("[", 5000) + strings.Repeat("]", 5000), `{"message":"a","message":"b"}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		got := ReadErrorBody(body)
		if !utf8.ValidString(got.Message) || !utf8.ValidString(got.ErrorType) {
			t.Fatalf("ReadErrorBody(%q) = %+v: not valid UTF-8", body, got)
		}
		if got.NoBody && got.Message != "" {
			t.Fatalf("ReadErrorBody(%q) = %+v: no body with a message", body, got)
		}
		if got.NoBody != (len(body) == 0 || strings.TrimSpace(string(body)) == "null") && got.NoBody {
			t.Fatalf("ReadErrorBody(%q) = %+v: no body for a body", body, got)
		}
		if again := ReadErrorBody(body); again != got {
			t.Fatalf("ReadErrorBody(%q) = %+v, then %+v", body, got, again)
		}
	})
}
