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
	"net/http"
	"strconv"
	"testing"
)

func TestContent(t *testing.T) {
	tests := map[string]struct {
		a, b       Content
		wantAJSON  bool
		wantEquals bool
	}{
		"success: same text": {
			a: Content{Text: "can wait"}, b: Content{Text: "can wait"},
			wantEquals: true,
		},
		"success: different text": {
			a: Content{Text: "can wait"}, b: Content{Text: "today"},
		},
		"success: empty text equals empty text": {
			a: Content{}, b: Content{Text: ""},
			wantEquals: true,
		},
		"success: same JSON bytes": {
			a: Content{JSON: []byte(`{"a":1}`)}, b: Content{JSON: []byte(`{"a":1}`)},
			wantAJSON: true, wantEquals: true,
		},
		"success: JSON compares bytes, not meaning": {
			a: Content{JSON: []byte(`{"a":1}`)}, b: Content{JSON: []byte(`{"a": 1}`)},
			wantAJSON: true,
		},
		"success: an empty object is JSON, not text": {
			a: Content{JSON: []byte(`{}`)}, b: Content{},
			wantAJSON: true,
		},
		"success: an empty non-nil JSON slice is still JSON": {
			a: Content{JSON: []byte{}}, b: Content{},
			wantAJSON: true,
		},
		"success: text never equals JSON spelling the same bytes": {
			a: Content{Text: `["x"]`}, b: Content{JSON: []byte(`["x"]`)},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.a.IsJSON(); got != tt.wantAJSON {
				t.Errorf("IsJSON() = %t, want %t", got, tt.wantAJSON)
			}
			if got := tt.a.Equal(tt.b); got != tt.wantEquals {
				t.Errorf("a.Equal(b) = %t, want %t", got, tt.wantEquals)
			}
			if got := tt.b.Equal(tt.a); got != tt.wantEquals {
				t.Errorf("b.Equal(a) = %t, want %t (Equal must be symmetric)", got, tt.wantEquals)
			}
		})
	}
}

func TestResponseMetaRequestID(t *testing.T) {
	withValues := func(values ...string) http.Header {
		h := http.Header{}
		for _, v := range values {
			// Add canonicalises the lower-case wire spelling, as the HTTP/2
			// client does for every received header.
			h.Add("x-typesafe-request-id", v)
		}
		return h
	}
	tests := map[string]struct {
		header http.Header
		want   string
		wantOK bool
	}{
		"success: present": {
			header: withValues("req_123"), want: "req_123", wantOK: true,
		},
		"success: the first of several values": {
			header: withValues("req_1", "req_2"), want: "req_1", wantOK: true,
		},
		"success: present but empty": {
			header: withValues(""), want: "", wantOK: true,
		},
		"success: the value is returned as it arrived": {
			header: withValues("req\x1b[31m"), want: "req\x1b[31m", wantOK: true,
		},
		"missing: absent": {
			header: http.Header{"Content-Type": {"application/json"}},
		},
		"missing: nil header": {
			header: nil,
		},
		"missing: a non-canonical key is not looked up": {
			// A header map built by hand without canonicalisation is not
			// what net/http produces; the lookup does not guess.
			header: http.Header{"x-typesafe-request-id": {"req_1"}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			meta := ResponseMeta{Status: http.StatusOK, Header: tt.header}
			got, ok := meta.RequestID()
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("RequestID() = %q, %t; want %q, %t", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestRequestIDHeaderIsCanonical(t *testing.T) {
	if got := http.CanonicalHeaderKey("x-typesafe-request-id"); got != RequestIDHeader {
		t.Fatalf("RequestIDHeader = %q, want the canonical key %q", RequestIDHeader, got)
	}
}

func TestPreparedLookup(t *testing.T) {
	small := []PreparedQuestion{
		{Name: "billing", Kind: KindNoul},
		{Name: "tone", Kind: KindChoice, Options: []string{"calm", "angry"}},
		{Name: "urgency", Kind: KindScore, Levels: []Content{{Text: "can wait"}, {JSON: []byte(`{"t":"today"}`)}}},
	}
	large := make([]PreparedQuestion, linearLimit+5)
	for i := range large {
		large[i] = PreparedQuestion{Name: "q" + strconv.Itoa(i), Kind: KindNoul}
	}

	tests := map[string]struct {
		entries   []PreparedQuestion
		name      string
		wantIndex int // position in entries, or -1 for not found
		wantMap   bool
	}{
		"success: first of a small set":  {entries: small, name: "billing", wantIndex: 0},
		"success: last of a small set":   {entries: small, name: "urgency", wantIndex: 2},
		"missing: absent from small set": {entries: small, name: "spam", wantIndex: -1},
		"success: first of a large set":  {entries: large, name: "q0", wantIndex: 0, wantMap: true},
		"success: last of a large set":   {entries: large, name: "q" + strconv.Itoa(len(large)-1), wantIndex: len(large) - 1, wantMap: true},
		"missing: absent from large set": {entries: large, name: "q999", wantIndex: -1, wantMap: true},
		"missing: empty set":             {entries: nil, name: "billing", wantIndex: -1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			questions := []byte(`{"billing":{"type":"noul"}}`)
			p := NewPrepared(questions, tt.entries)
			if string(p.Questions) != string(questions) {
				t.Errorf("Questions = %q, want %q", p.Questions, questions)
			}
			if (p.index != nil) != tt.wantMap {
				t.Errorf("index built = %t, want %t", p.index != nil, tt.wantMap)
			}
			got, ok := p.Lookup(tt.name)
			if tt.wantIndex < 0 {
				if ok || got != nil {
					t.Fatalf("Lookup(%q) = %+v, %t; want nil, false", tt.name, got, ok)
				}
				return
			}
			if !ok {
				t.Fatalf("Lookup(%q) not found, want entry %d", tt.name, tt.wantIndex)
			}
			// The result points into Entries rather than at a copy, so the
			// decoder's interning reads the prepared tables themselves.
			if got != &p.Entries[tt.wantIndex] {
				t.Errorf("Lookup(%q) = %p, want &Entries[%d] = %p", tt.name, got, tt.wantIndex, &p.Entries[tt.wantIndex])
			}
		})
	}
}
