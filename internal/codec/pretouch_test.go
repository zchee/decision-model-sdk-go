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
	"testing"

	"github.com/bytedance/sonic/encoder"
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
