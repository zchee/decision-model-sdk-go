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
	"errors"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic/encoder"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

type pretouchState struct {
	Subject string            `json:"subject"`
	Body    string            `json:"body,omitzero"`
	Tags    []string          `json:"tags"`
	Extra   map[string]string `json:"extra"`
}

func TestPretouch(t *testing.T) {
	tests := map[string]struct {
		typ     reflect.Type
		value   any
		want    string
		wantErr error
	}{
		"success: struct state": {
			typ:   reflect.TypeFor[pretouchState](),
			value: pretouchState{Subject: "Duplicate charge", Tags: []string{"billing"}},
			want:  `{"subject":"Duplicate charge","tags":["billing"],"extra":null}`,
		},
		"success: pointer to struct": {
			typ:   reflect.TypeFor[*pretouchState](),
			value: &pretouchState{Subject: "s"},
			want:  `{"subject":"s","tags":null,"extra":null}`,
		},
		"success: map state": {
			typ:   reflect.TypeFor[map[string]any](),
			value: map[string]any{"message": "Please help."},
			want:  `{"message":"Please help."}`,
		},
		"error: nil type": {
			typ:     nil,
			wantErr: errPretouchNilType,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := Pretouch(tt.typ)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Pretouch(%v) = %v, want %v", tt.typ, err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			// A pretouched type encodes as it would have without the
			// pretouch: the hook only moves the compilation earlier.
			var buf []byte
			if err := encoder.EncodeInto(&buf, tt.value, 0); err != nil {
				t.Fatalf("EncodeInto after Pretouch: %v", err)
			}
			if string(buf) != tt.want {
				t.Errorf("EncodeInto after Pretouch = %s, want %s", buf, tt.want)
			}
		})
	}
}

// freshTypes numbers the types freshStateType builds.
var freshTypes atomic.Int64

// freshStateType returns a struct type that sonic has never seen in this
// process: the encoder caches its compiled program per type, so a type used
// by another test, or by an earlier run under -count, would already be warm.
// Each call names the fields after a new number, and reflect.StructOf returns
// a distinct type for distinct field names.
func freshStateType() reflect.Type {
	n := strconv.FormatInt(freshTypes.Add(1), 10)
	return reflect.StructOf([]reflect.StructField{
		{Name: "Subject" + n, Type: reflect.TypeFor[string](), Tag: `json:"subject"`},
		{Name: "Tags" + n, Type: reflect.TypeFor[[]string](), Tag: `json:"tags"`},
		{Name: "Extra" + n, Type: reflect.TypeFor[map[string]string](), Tag: `json:"extra"`},
	})
}

// TestPretouchFirstCallCostsAWarmCall measures what Pretouch is for: after it,
// the first EncodeInto of a type allocates exactly what a warm call does, so
// the compilation happened inside Pretouch. The control case, a first call
// without Pretouch, must allocate more than a warm call; it shows that the
// measurement can see a compilation at all.
func TestPretouchFirstCallCostsAWarmCall(t *testing.T) {
	if raceEnabled() {
		t.Skip("allocation counts need a normal build: under -race sync.Pool.Put drops one value in four")
	}
	testsupport.QuietRuntime(t)
	buf := make([]byte, 0, 4<<10)
	// encode takes the calling test, so a failure inside a subtest fails that
	// subtest from its own goroutine. It does not call tb.Helper: that records
	// the caller in a map, an allocation inside the measured section.
	encode := func(tb testing.TB, v any) {
		buf = buf[:0]
		if err := encoder.EncodeInto(&buf, v, 0); err != nil {
			tb.Fatalf("EncodeInto: %v", err)
		}
	}
	// Fill sonic's own pools (its encoder stack) before anything is measured.
	encode(t, "warm")

	tests := map[string]struct {
		pretouch bool
	}{
		"success: after Pretouch the first call allocates like a warm call": {pretouch: true},
		"success: without Pretouch the first call allocates more (control)": {pretouch: false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			first := make([]testsupport.Allocs, testsupport.AllocRuns)
			warm := make([]testsupport.Allocs, testsupport.AllocRuns)
			for i := range first {
				typ := freshStateType()
				v := reflect.New(typ).Elem().Interface() // boxed outside the measured sections
				if tt.pretouch {
					if err := Pretouch(typ); err != nil {
						t.Fatalf("Pretouch(%v): %v", typ, err)
					}
				}
				first[i] = testsupport.Measure(func() { encode(t, v) })
				warm[i] = testsupport.Measure(func() { encode(t, v) })
			}
			if want := `{"subject":"","tags":null,"extra":null}`; string(buf) != want {
				t.Fatalf("EncodeInto wrote %s, want %s", buf, want)
			}
			gotWarm := testsupport.StableMin(t, "warm call", warm)
			if tt.pretouch {
				gotFirst := testsupport.StableMin(t, "first call after Pretouch", first)
				if gotFirst != gotWarm {
					t.Errorf("first call after Pretouch allocated %s (mallocs/bytes), a warm call %s: Pretouch left work for the first call", gotFirst, gotWarm)
				}
				return
			}
			// Every first call compiles a new type, so a run can differ from
			// its neighbours by the compiler's own bookkeeping: compare the
			// cheapest first call with the warm call instead of requiring
			// agreement.
			cheapest := first[0]
			for _, run := range first[1:] {
				cheapest.Mallocs = min(cheapest.Mallocs, run.Mallocs)
			}
			t.Logf("first calls without Pretouch, mallocs/bytes: %v", first)
			if cheapest.Mallocs <= gotWarm.Mallocs {
				t.Errorf("first call without Pretouch allocated %d objects, a warm call %d: the measurement cannot see a compilation", cheapest.Mallocs, gotWarm.Mallocs)
			}
		})
	}
}
