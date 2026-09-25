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

import "testing"

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
			if got := string(p.w.Questions); got != tt.want {
				t.Errorf("questions after the caller's change = %s, want %s", got, tt.want)
			}
		})
	}
}
