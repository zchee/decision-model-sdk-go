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
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

func TestContent(t *testing.T) {
	raw := RawJSON(`{"a": 1}`)
	tests := map[string]struct {
		c        Content
		wantZero bool
		wantJSON bool
		wantText string
		wantRaw  string
		wantNil  bool // JSON() is nil
	}{
		"success: the zero Content is unset": {c: Content{}, wantZero: true, wantNil: true},
		"success: text":                      {c: Text("payments or invoices"), wantText: "payments or invoices", wantNil: true},
		"success: empty text is set":         {c: Text(""), wantNil: true},
		"success: JSON keeps the bytes as given": {
			c: JSON(raw), wantJSON: true, wantRaw: `{"a": 1}`,
		},
		"success: JSON from a byte slice": {c: JSON([]byte(`[1,2]`)), wantJSON: true, wantRaw: `[1,2]`},
		// Prepare rejects it; until then it is JSON content with no bytes.
		"success: JSON(nil) is set JSON content": {c: JSON(nil), wantJSON: true, wantRaw: ``},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.c.IsZero(); got != tt.wantZero {
				t.Errorf("IsZero() = %t, want %t", got, tt.wantZero)
			}
			if got := tt.c.IsJSON(); got != tt.wantJSON {
				t.Errorf("IsJSON() = %t, want %t", got, tt.wantJSON)
			}
			if got := tt.c.Text(); got != tt.wantText {
				t.Errorf("Text() = %q, want %q", got, tt.wantText)
			}
			got := tt.c.JSON()
			if (got == nil) != tt.wantNil || string(got) != tt.wantRaw {
				t.Errorf("JSON() = %q (nil %t), want %q (nil %t)", got, got == nil, tt.wantRaw, tt.wantNil)
			}
		})
	}
}

// TestContentJSONSharesTheBytes checks that JSON keeps the caller's slice
// until Prepare, as its documentation says, and that the prepared set holds
// its own copy afterwards.
func TestContentJSONSharesTheBytes(t *testing.T) {
	tests := map[string]struct {
		raw  string
		want string
	}{
		"success: a compact object":  {raw: `{"a":1}`, want: `{"q":{"type":"score","criteria":[{"a":1}]}}`},
		"success: an indented array": {raw: "[\n  1\n]", want: `{"q":{"type":"score","criteria":[[1]]}}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			raw := []byte(tt.raw)
			c := JSON(raw)
			if got := c.JSON(); &got[0] != &raw[0] {
				t.Error("JSON() does not share the caller's bytes")
			}
			p, err := NewQuestions().Score("q", Score{Levels: []Content{c}}).Prepare()
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			for i := range raw {
				raw[i] = ' '
			}
			if got := string(p.wirePrepared().Questions); got != tt.want {
				t.Errorf("questions after the caller's change = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestContentMarshalJSON covers the JSON form of Content (rulings R52 and
// R60): text escaped as the questions are, JSON content checked by wire's
// scanner and compacted, unset Content as null, and the failures.
func TestContentMarshalJSON(t *testing.T) {
	tests := map[string]struct {
		c          Content
		want       string
		wantErr    error // errors.Is target
		wantSyntax bool  // the failure is wire's scanner's (*wire.SyntaxError)
	}{
		"success: text, escaped as in the questions": {c: Text("a\"b\\c\b\f<>\u00e9"), want: `"a\"b\\c\b\f<>` + "\u00e9" + `"`},
		"success: empty text":                        {c: Text(""), want: `""`},
		"success: a JSON object, compacted":          {c: JSON([]byte(` { "a" : [1, null] } `)), want: `{"a":[1,null]}`},
		"success: a JSON array":                      {c: JSON([]byte(`[1,null]`)), want: `[1,null]`},
		"success: unset Content is null":             {c: Content{}, want: `null`},
		"error: text that is not UTF-8":              {c: Text("\xff"), wantErr: wire.ErrInvalidUTF8},
		"error: JSON content that is a number":       {c: JSON([]byte(` 3`)), wantErr: wire.ErrContentShape},
		"error: empty JSON content":                  {c: JSON(nil), wantErr: wire.ErrContentShape},
		"error: complete but invalid JSON":           {c: JSON([]byte(`{"a":}`)), wantSyntax: true},
		"error: a trailing comma":                    {c: JSON([]byte(`[1,]`)), wantSyntax: true},
		"error: truncated JSON":                      {c: JSON([]byte(`{"a":`)), wantSyntax: true},
		"error: a raw control character in a string": {c: JSON([]byte("[\"a\nb\"]")), wantSyntax: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := tt.c.MarshalJSON()
			checkMarshal(t, got, err, tt.want, tt.wantErr, tt.wantSyntax)
		})
	}
}

// TestRawJSONMarshalJSON covers the JSON form of nested RawJSON (rulings R57
// and R60): any single JSON value, checked by wire's scanner and compacted,
// nil as null, and the failures.
func TestRawJSONMarshalJSON(t *testing.T) {
	tests := map[string]struct {
		r          RawJSON
		want       string
		wantSyntax bool // the failure is wire's scanner's (*wire.SyntaxError)
	}{
		"success: an object, compacted":       {r: RawJSON(` { "a" : [1, 2] } `), want: `{"a":[1,2]}`},
		"success: a number":                   {r: RawJSON(` 4 `), want: `4`},
		"success: a string keeps its escapes": {r: RawJSON(`"a\u00e9\n"`), want: `"a\u00e9\n"`},
		"success: null":                       {r: RawJSON(`null`), want: `null`},
		"success: a nil RawJSON is null":      {r: nil, want: `null`},
		"error: empty":                        {r: RawJSON{}, wantSyntax: true},
		"error: complete but invalid":         {r: RawJSON(`{"a" 1}`), wantSyntax: true},
		"error: a trailing comma":             {r: RawJSON(`{"a":1,}`), wantSyntax: true},
		"error: two values":                   {r: RawJSON(`[1 2]`), wantSyntax: true},
		"error: trailing data":                {r: RawJSON(`{} x`), wantSyntax: true},
		"error: truncated":                    {r: RawJSON(`[1,`), wantSyntax: true},
		"error: invalid UTF-8 in a string":    {r: RawJSON("\"\xff\""), wantSyntax: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := tt.r.MarshalJSON()
			checkMarshal(t, got, err, tt.want, nil, tt.wantSyntax)
		})
	}
}

// checkMarshal checks a MarshalJSON result: the bytes on success, or a
// failure that is wantErr (errors.Is) or a *wire.SyntaxError.
func checkMarshal(t *testing.T, got []byte, err error, want string, wantErr error, wantSyntax bool) {
	t.Helper()
	if wantErr == nil && !wantSyntax {
		if err != nil {
			t.Fatalf("MarshalJSON: %v", err)
		}
		if diff := gocmp.Diff(want, string(got)); diff != "" {
			t.Errorf("MarshalJSON (-want +got):\n%s", diff)
		}
		return
	}
	if err == nil {
		t.Fatalf("MarshalJSON = %q, want an error", got)
	}
	if wantErr != nil && !errors.Is(err, wantErr) {
		t.Errorf("MarshalJSON err = %v, want errors.Is %v", err, wantErr)
	}
	if _, ok := errors.AsType[*wire.SyntaxError](err); ok != wantSyntax {
		t.Errorf("errors.As *wire.SyntaxError = %t, want %t (err %v)", ok, wantSyntax, err)
	}
}
