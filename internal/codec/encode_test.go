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
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// prefix stands for the bytes a body holds before the state; every encoder
// call appends after it and must leave it alone, on success and on failure.
const prefix = `{"state":`

// celsius is a named float type: a number however it is named.
type celsius float64

// label is a named string type: a string however it is named.
type label string

// ticket is a struct state with nested values, members in declaration order.
type ticket struct {
	Name  string   `json:"name"`
	Tags  []string `json:"tags"`
	Meta  *meta    `json:"meta"`
	Score int      `json:"score,omitempty"`
}

type meta struct {
	Source string `json:"source"`
}

// scalarMarshaler is a json.Marshaler whose output is a number after
// whitespace.
type scalarMarshaler struct{}

func (scalarMarshaler) MarshalJSON() ([]byte, error) { return []byte(" \n 3"), nil }

// cyclic values: sonic stops them at its nesting limit.
type node struct {
	Next *node `json:"next"`
}

func cyclicMap() map[string]any {
	m := map[string]any{}
	m["self"] = m
	return m
}

func cyclicPointer() *node {
	n := &node{}
	n.Next = n
	return n
}

func cyclicSlice() []any {
	s := []any{nil}
	s[0] = s
	return s
}

// errKind is how an encoder call is expected to fail.
type errKind int

const (
	errNone   errKind = iota
	errShape          // ErrStateShape
	errBytes          // ErrBytesState
	errRaw            // ErrRawValue
	errUTF8           // wire.ErrInvalidUTF8
	errEncode         // *EncodeError wrapping sonic's error
)

// checkErr reports whether err is the failure want names, with a readable
// reason when it is not.
func checkErr(t *testing.T, err error, want errKind, wantText string) {
	t.Helper()
	if want == errNone {
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("err = nil, want a failure (%d)", want)
	}
	var sentinel error
	switch want {
	case errShape:
		sentinel = ErrStateShape
	case errBytes:
		sentinel = ErrBytesState
	case errRaw:
		sentinel = ErrRawValue
	case errUTF8:
		sentinel = wire.ErrInvalidUTF8
	case errEncode:
		ee, ok := errors.AsType[*EncodeError](err)
		if !ok {
			t.Fatalf("err = %T %v, want an *EncodeError", err, err)
		}
		if ee.Unwrap() == nil || ee.Error() != ee.Unwrap().Error() {
			t.Errorf("EncodeError %q does not carry sonic's error %v", ee.Error(), ee.Unwrap())
		}
	default:
		t.Fatalf("bad errKind %d", want)
	}
	if sentinel != nil && !errors.Is(err, sentinel) {
		t.Fatalf("err = %T %v, want errors.Is %v", err, err, sentinel)
	}
	if !strings.Contains(err.Error(), wantText) {
		t.Errorf("err = %q, want it to contain %q", err.Error(), wantText)
	}
}

// TestEncodeState covers the state encoder: what it writes for each state
// kind, which states it refuses and why, and that a failure leaves the bytes
// before the state as they were (R34, R48, R49; Appendix B "`any` state" and
// "NaN/Infinity written").
func TestEncodeState(t *testing.T) {
	tests := map[string]struct {
		state    any
		want     string // the encoded state, on success
		err      errKind
		wantText string // a substring of the error text
	}{
		"success: string":              {state: "hi", want: `"hi"`},
		"success: named string":        {state: label("hi"), want: `"hi"`},
		"success: empty string":        {state: "", want: `""`},
		"success: map with one member": {state: map[string]any{"message": "hi"}, want: `{"message":"hi"}`},
		"success: map[string]string":   {state: map[string]string{"k": "v"}, want: `{"k":"v"}`},
		"success: empty map":           {state: map[string]any{}, want: `{}`},
		"success: []any with a nested null": {
			state: []any{map[string]any{"message": "Classify"}, nil},
			want:  `[{"message":"Classify"},null]`,
		},
		"success: []string":    {state: []string{"a", "b"}, want: `["a","b"]`},
		"success: empty []any": {state: []any{}, want: `[]`},
		"success: struct in declaration order": {
			state: ticket{Name: "t", Tags: []string{"a"}, Meta: &meta{Source: "web"}},
			want:  `{"name":"t","tags":["a"],"meta":{"source":"web"}}`,
		},
		"success: pointer to struct, nil members as null": {
			state: &ticket{Name: "t"},
			want:  `{"name":"t","tags":null,"meta":null}`,
		},
		"success: json.RawMessage is validated, not compacted": {
			state: json.RawMessage(" {\"a\" : [1, 2]} "),
			want:  " {\"a\" : [1, 2]} ",
		},
		"success: every non-ASCII character as is": {
			state: "é\u2028\u2029\U0001F600<>&",
			want:  "\"é\u2028\u2029\U0001F600<>&\"",
		},

		"error: nil":                      {state: nil, err: errShape, wantText: "nil encodes as null, not a string"},
		"error: true":                     {state: true, err: errShape, wantText: "bool encodes as a boolean"},
		"error: false":                    {state: false, err: errShape, wantText: "bool encodes as a boolean"},
		"error: int":                      {state: 3, err: errShape, wantText: "int encodes as a number"},
		"error: negative int8":            {state: int8(-3), err: errShape, wantText: "int8 encodes as a number"},
		"error: uint64":                   {state: uint64(math.MaxUint64), err: errShape, wantText: "a number"},
		"error: float64":                  {state: 1.5, err: errShape, wantText: "float64 encodes as a number"},
		"error: float32":                  {state: float32(1.5), err: errShape, wantText: "a number"},
		"error: named float":              {state: celsius(21.5), err: errShape, wantText: "codec.celsius encodes as a number"},
		"error: json.Number":              {state: json.Number("12"), err: errShape, wantText: "json.Number encodes as a number"},
		"error: nil map":                  {state: map[string]any(nil), err: errShape, wantText: "encodes as null"},
		"error: nil slice":                {state: []any(nil), err: errShape, wantText: "encodes as null"},
		"error: nil pointer":              {state: (*ticket)(nil), err: errShape, wantText: "*codec.ticket encodes as null"},
		"error: json.RawMessage number":   {state: json.RawMessage("3"), err: errShape, wantText: "a number"},
		"error: json.RawMessage null":     {state: json.RawMessage(" null"), err: errShape, wantText: "null"},
		"error: marshaler number":         {state: scalarMarshaler{}, err: errShape, wantText: "a number"},
		"error: []byte":                   {state: []byte(`{"a":1}`), err: errBytes, wantText: "[]byte"},
		"error: NaN":                      {state: []any{math.NaN()}, err: errEncode, wantText: "NaN"},
		"error: +Inf in a map":            {state: map[string]any{"a": math.Inf(1)}, err: errEncode, wantText: "Infinite"},
		"error: -Inf float32 in a struct": {state: struct{ F float32 }{float32(math.Inf(-1))}, err: errEncode, wantText: "Infinite"},
		"error: channel":                  {state: map[string]any{"c": make(chan int)}, err: errEncode, wantText: "chan int"},
		"error: function":                 {state: []any{func() {}}, err: errEncode, wantText: "func()"},
		"error: complex number":           {state: []any{complex(1, 2)}, err: errEncode, wantText: "complex128"},
		"error: cyclic map":               {state: cyclicMap(), err: errEncode, wantText: "too deep"},
		"error: cyclic pointer":           {state: cyclicPointer(), err: errEncode, wantText: "too deep"},
		"error: cyclic slice":             {state: cyclicSlice(), err: errEncode, wantText: "too deep"},
		"error: invalid marshaler output": {state: json.RawMessage(`{"a":`), err: errEncode, wantText: "Marshaler"},
		"error: invalid UTF-8 string":     {state: "a\xffb", err: errUTF8},
		"error: surrogate bytes (WTF-8)":  {state: "a\xed\xa0\x80b", err: errUTF8},
		"error: invalid UTF-8 map key":    {state: map[string]any{"k\xff": 1}, err: errUTF8},
		"error: invalid UTF-8 in a field": {state: ticket{Name: "\xc3"}, err: errUTF8},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			buf := []byte(prefix)
			err := EncodeState(&buf, tt.state)
			checkErr(t, err, tt.err, tt.wantText)
			want := prefix + tt.want
			if diff := gocmp.Diff(want, string(buf)); diff != "" {
				t.Errorf("buffer after EncodeState (-want +got):\n%s", diff)
			}
		})
	}
}

// TestEncodeValue covers the encoder of body members other than the state:
// any JSON value, scalars included, with the same UTF-8 rule.
func TestEncodeValue(t *testing.T) {
	tests := map[string]struct {
		value    any
		want     string
		err      errKind
		wantText string
	}{
		"success: nil":                {value: nil, want: `null`},
		"success: int":                {value: 4, want: `4`},
		"success: bool":               {value: false, want: `false`},
		"success: string":             {value: "override-model", want: `"override-model"`},
		"success: map":                {value: map[string]any{"x": map[string]any{"type": "noul"}}, want: `{"x":{"type":"noul"}}`},
		"success: []byte as base64":   {value: []byte("hi"), want: `"aGk="`},
		"success: json.RawMessage":    {value: json.RawMessage(`null`), want: `null`},
		"error: NaN":                  {value: math.NaN(), err: errEncode, wantText: "NaN"},
		"error: channel":              {value: make(chan int), err: errEncode, wantText: "chan int"},
		"error: invalid UTF-8 string": {value: "\xff", err: errUTF8},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			buf := []byte(prefix)
			err := EncodeValue(&buf, tt.value)
			checkErr(t, err, tt.err, tt.wantText)
			if diff := gocmp.Diff(prefix+tt.want, string(buf)); diff != "" {
				t.Errorf("buffer after EncodeValue (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAppendRawState covers R34's O(1) shape check on a raw state: the first
// byte other than JSON whitespace opens an object, an array or a string, and
// the bytes are appended as they are, whitespace included; nothing else is
// checked.
func TestAppendRawState(t *testing.T) {
	tests := map[string]struct {
		raw      string
		want     string
		err      errKind
		wantText string
	}{
		"success: object":                     {raw: `{"a":[1,2,{"b":null}]}`, want: `{"a":[1,2,{"b":null}]}`},
		"success: array":                      {raw: `[1]`, want: `[1]`},
		"success: string":                     {raw: `"text"`, want: `"text"`},
		"success: whitespace kept":            {raw: " \t\r\n{ \"a\" : 1 }\n", want: " \t\r\n{ \"a\" : 1 }\n"},
		"success: validity is not checked":    {raw: `{"a":`, want: `{"a":`},
		"error: empty":                        {raw: ``, err: errShape, wantText: "raw JSON is empty"},
		"error: whitespace only":              {raw: " \n\t\r", err: errShape, wantText: "raw JSON is empty"},
		"error: number":                       {raw: `3`, err: errShape, wantText: "raw JSON starts with a number"},
		"error: null":                         {raw: ` null`, err: errShape, wantText: "starts with null"},
		"error: boolean":                      {raw: `true`, err: errShape, wantText: "a boolean"},
		"error: form feed is not whitespace":  {raw: "\f{}", err: errShape, wantText: `'\f'`},
		"error: non-ASCII first byte":         {raw: " {}", err: errShape, wantText: "byte 0xc2"},
		"error: a closing bracket is no open": {raw: `}`, err: errShape, wantText: `'}'`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			buf := []byte(prefix)
			err := AppendRawState(&buf, []byte(tt.raw))
			checkErr(t, err, tt.err, tt.wantText)
			if diff := gocmp.Diff(prefix+tt.want, string(buf)); diff != "" {
				t.Errorf("buffer after AppendRawState (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAppendRawValue covers the O(1) check on a raw body member: its first
// byte other than JSON whitespace can start any JSON value.
func TestAppendRawValue(t *testing.T) {
	tests := map[string]struct {
		raw      string
		want     string
		err      errKind
		wantText string
	}{
		"success: number":             {raw: `4`, want: `4`},
		"success: negative number":    {raw: `-0.5`, want: `-0.5`},
		"success: null":               {raw: `null`, want: `null`},
		"success: true":               {raw: ` true `, want: ` true `},
		"success: false":              {raw: `false`, want: `false`},
		"success: object":             {raw: `{}`, want: `{}`},
		"success: array":              {raw: `[]`, want: `[]`},
		"success: string":             {raw: `""`, want: `""`},
		"error: empty":                {raw: ``, err: errRaw, wantText: "raw JSON is empty"},
		"error: whitespace only":      {raw: "  ", err: errRaw, wantText: "raw JSON is empty"},
		"error: a word":               {raw: `hello`, err: errRaw, wantText: `'h'`},
		"error: plus is not a number": {raw: `+1`, err: errRaw, wantText: `'+'`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			buf := []byte(prefix)
			err := AppendRawValue(&buf, []byte(tt.raw))
			checkErr(t, err, tt.err, tt.wantText)
			if diff := gocmp.Diff(prefix+tt.want, string(buf)); diff != "" {
				t.Errorf("buffer after AppendRawValue (-want +got):\n%s", diff)
			}
		})
	}
}

// TestEncodeStateIntoBody encodes into a pooled scratch, as the request body
// assembly does, and reads the body back through GetBody.
func TestEncodeStateIntoBody(t *testing.T) {
	body := NewBody()
	buf := body.Buffer()
	*buf = append(*buf, prefix...)
	if err := EncodeState(buf, map[string]any{"k": []any{"v", 1, true, nil}}); err != nil {
		t.Fatal(err)
	}
	*buf = append(*buf, '}')
	const want = `{"state":{"k":["v",1,true,null]}}`

	for i := range 2 {
		rc, err := body.GetBody()
		if err != nil {
			t.Fatalf("GetBody #%d: %v", i, err)
		}
		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("reading reader #%d: %v", i, err)
		}
		if err := rc.Close(); err != nil {
			t.Fatalf("closing reader #%d: %v", i, err)
		}
		if diff := gocmp.Diff(want, string(got)); diff != "" {
			t.Errorf("reader #%d (-want +got):\n%s", i, diff)
		}
	}
	if body.Len() != len(want) {
		t.Errorf("Len = %d, want %d", body.Len(), len(want))
	}
}

// TestBodyGetBodyAfterRelease checks that GetBody, once the body is gone,
// returns a nil io.ReadCloser, not a nil *BodyReader inside a non-nil
// interface, which a transport would try to read.
func TestBodyGetBodyAfterRelease(t *testing.T) {
	body := NewBody()
	*body.Buffer() = append(*body.Buffer(), `{}`...)
	body.Release()
	rc, err := body.GetBody()
	if !errors.Is(err, ErrBodyReleased) {
		t.Fatalf("GetBody after Release: err = %v, want %v", err, ErrBodyReleased)
	}
	if rc != nil {
		t.Fatalf("GetBody after Release returned a non-nil reader %#v", rc)
	}
}

// allocItem and allocState make a struct state of about 1 KiB, shaped like
// spike S-E1's.
type allocItem struct {
	ID    int64    `json:"id"`
	Text  string   `json:"text"`
	Tags  []string `json:"tags"`
	Score float64  `json:"score"`
	Rank  int      `json:"rank"`
}

type allocState struct {
	Name  string      `json:"name"`
	Items []allocItem `json:"items"`
}

// allocStates returns a state of each NF1 kind whose encoding is about 1 KiB:
// the struct, a flat map of strings and its JSON as raw bytes.
func allocStates() (st *allocState, flat map[string]any, raw []byte) {
	st = &allocState{Name: "state", Items: make([]allocItem, 10)}
	for i := range st.Items {
		st.Items[i] = allocItem{ID: int64(i), Text: strings.Repeat("abcdefghij", 4), Tags: []string{"alpha", "beta"}, Score: 0.5, Rank: i}
	}
	flat = make(map[string]any, 16)
	for i := range 16 {
		flat["k"+string(rune('a'+i))+"000000"] = strings.Repeat("0123456789abcdef", 3)
	}
	raw = []byte(`{"items":[` + strings.Repeat(`{"id":1,"text":"abcdefghijabcdefghijabcdefghijabcdefghij","tags":["alpha","beta"]},`, 12) + `{}]}`)
	return st, flat, raw
}

// TestEncodeStateAllocations pins NF1 for one pooled state encode of each
// kind at 1 KiB on a warm pool (docs/perf/frozen-budgets.md, AC-P1): a bare
// string 2 (sonic 1, plus boxing the string into the any parameter), a
// boxed string 1, a boxed json.RawMessage 1, raw bytes 0, a *struct 1 and a
// flat map 2 (1 + one map); every call at most 112 bytes. The body-level
// counts of the same kinds are TestAllocBodyKinds in the root package.
func TestEncodeStateAllocations(t *testing.T) {
	if raceEnabled() {
		t.Skip("allocation counts need a normal build: under -race sync.Pool.Put drops one value in four")
	}
	testsupport.QuietRuntime(t)
	st, flat, raw := allocStates()
	s := strings.Repeat("s", 1022)
	var boxed any = s
	var rawMessage any = json.RawMessage(raw)

	tests := map[string]struct {
		encode func(buf *[]byte) error
		want   uint64
	}{
		"success: bare string":             {encode: func(buf *[]byte) error { return EncodeState(buf, s) }, want: 2},
		"success: boxed string":            {encode: func(buf *[]byte) error { return EncodeState(buf, boxed) }, want: 1},
		"success: boxed json.RawMessage":   {encode: func(buf *[]byte) error { return EncodeState(buf, rawMessage) }, want: 1},
		"success: raw bytes":               {encode: func(buf *[]byte) error { return AppendRawState(buf, raw) }, want: 0},
		"success: pointer to struct":       {encode: func(buf *[]byte) error { return EncodeState(buf, st) }, want: 1},
		"success: flat map[string]any":     {encode: func(buf *[]byte) error { return EncodeState(buf, flat) }, want: 2},
		"success: empty section (control)": {encode: func(*[]byte) error { return nil }, want: 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var n int
			call := func(struct{}) {
				body := NewBody()
				buf := body.Buffer()
				*buf = append(*buf, prefix...)
				if err := tt.encode(buf); err != nil {
					t.Fatal(err)
				}
				n = body.Len()
				body.Release()
			}
			call(struct{}{}) // warms the pool and sonic's compiled encoder for the type
			got := testsupport.MeasureMin(t, name, func() struct{} { return struct{}{} }, call)
			t.Logf("%s: body %d bytes, %s mallocs/bytes", name, n, got)
			if got.Mallocs != tt.want {
				t.Errorf("mallocs = %d, want %d (NF1)", got.Mallocs, tt.want)
			}
			if got.Bytes > 112 {
				t.Errorf("bytes = %d, want at most 112 per warm-pool call (AC-P1)", got.Bytes)
			}
		})
	}
}
