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
	"errors"
	"net/http"
	"strconv"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
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
		"success: repeated values are joined with a comma and a space": {
			// httpx2.Headers([("x-typesafe-request-id", "req_1"),
			// ("x-typesafe-request-id", "req_2")]).get(...) == "req_1, req_2".
			header: withValues("req_1", "req_2"), want: "req_1, req_2", wantOK: true,
		},
		"success: three values keep their order": {
			header: withValues("c", "a", "b"), want: "c, a, b", wantOK: true,
		},
		"success: an empty value among several is kept": {
			// httpx2 joins ["a", ""] into "a, " and ["", "b"] into ", b".
			header: withValues("a", ""), want: "a, ", wantOK: true,
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

func TestRequestIDSingleValueDoesNotAllocate(t *testing.T) {
	meta := ResponseMeta{Header: http.Header{RequestIDHeader: {"req_123"}}}
	var got string
	allocs := testing.AllocsPerRun(100, func() { got, _ = meta.RequestID() })
	if allocs != 0 || got != "req_123" {
		t.Errorf("RequestID() = %q with %v allocations per call, want %q with 0", got, allocs, "req_123")
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
			p, err := NewPrepared(questions, tt.entries)
			if err != nil {
				t.Fatalf("NewPrepared: %v", err)
			}
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
			// The result points into the entries rather than at a copy, so
			// the decoder's interning reads the prepared tables themselves.
			if got != &p.Entries()[tt.wantIndex] {
				t.Errorf("Lookup(%q) = %p, want &Entries()[%d] = %p", tt.name, got, tt.wantIndex, &p.Entries()[tt.wantIndex])
			}
			if diff := gocmp.Diff(tt.entries, p.Entries()); diff != "" {
				t.Errorf("Entries() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNewPreparedRejectsRepeatedNames(t *testing.T) {
	numbered := func(n int) []PreparedQuestion {
		entries := make([]PreparedQuestion, n)
		for i := range entries {
			entries[i] = PreparedQuestion{Name: "q" + strconv.Itoa(i), Kind: KindNoul}
		}
		return entries
	}
	withRepeat := func(n, at int, name string) []PreparedQuestion {
		entries := numbered(n)
		entries[at].Name = name
		return entries
	}
	tests := map[string]struct {
		entries  []PreparedQuestion
		wantErr  bool
		wantName string // the repeated name the error reports
	}{
		"success: distinct names up to linearLimit":   {entries: numbered(linearLimit)},
		"success: distinct names past linearLimit":    {entries: numbered(linearLimit + 1)},
		"success: the empty name once":                {entries: []PreparedQuestion{{Name: ""}, {Name: "a"}}},
		"error: adjacent repeat in a small set":       {entries: withRepeat(3, 1, "q0"), wantErr: true, wantName: "q0"},
		"error: first and last of a set at the limit": {entries: withRepeat(linearLimit, linearLimit-1, "q0"), wantErr: true, wantName: "q0"},
		"error: repeat just past linearLimit":         {entries: withRepeat(linearLimit+1, linearLimit, "q3"), wantErr: true, wantName: "q3"},
		"error: repeat in a large set":                {entries: withRepeat(linearLimit+20, 25, "q24"), wantErr: true, wantName: "q24"},
		"error: the empty name twice":                 {entries: []PreparedQuestion{{Name: ""}, {Name: "a"}, {Name: ""}}, wantErr: true, wantName: ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p, err := NewPrepared([]byte(`{}`), tt.entries)
			if !tt.wantErr {
				if err != nil || p == nil {
					t.Fatalf("NewPrepared = %v, %v; want a set, nil", p, err)
				}
				return
			}
			if p != nil || !errors.Is(err, ErrDuplicateQuestion) {
				t.Fatalf("NewPrepared = %v, %v; want nil, ErrDuplicateQuestion", p, err)
			}
			if want := ErrDuplicateQuestion.Error() + ": " + strconv.Quote(tt.wantName); err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
		})
	}
}
