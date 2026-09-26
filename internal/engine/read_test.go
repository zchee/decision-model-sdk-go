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

package engine

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// chunks reads its data a few bytes at a time, as a network body does.
type chunks struct {
	data []byte
	step int
	err  error // returned once the data is read, io.EOF when nil
}

func (c *chunks) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		if c.err != nil {
			return 0, c.err
		}
		return 0, io.EOF
	}
	n := copy(p[:min(len(p), c.step)], c.data)
	c.data = c.data[n:]
	return n, nil
}

// TestReadBody checks the body read of section 6.2.1 (rulings R26b, R27):
// the first buffer is min(Content-Length, 256 KiB) for a declared body, with
// a spare byte when that is all of it, and 4 KiB for an undeclared one;
// growth doubles within the limit and the declared length, and the buffer
// that reaches either has a spare byte; the byte after the limit refuses the
// body, and a declared length over the limit is refused before a read.
func TestReadBody(t *testing.T) {
	body := func(n int) []byte { return bytes.Repeat([]byte{'x'}, n) }
	tests := map[string]struct {
		data     []byte
		declared int64
		limit    int64
		err      error
		wantErr  error
		wantCap  int // the capacity of the buffer returned, when it matters
	}{
		"success: an empty declared body":              {data: nil, declared: 0, limit: 64, wantCap: 1},
		"success: a declared body in one buffer":       {data: body(1000), declared: 1000, limit: 1 << 20, wantCap: 1001},
		"success: an undeclared small body":            {data: body(1000), declared: -1, limit: 1 << 20, wantCap: InitialUndeclared},
		"success: an undeclared body that grows twice": {data: body(10000), declared: -1, limit: 1 << 20, wantCap: 4 * InitialUndeclared},
		"success: an undeclared body of one full buffer grows once": {
			data: body(InitialUndeclared), declared: -1, limit: 1 << 20, wantCap: 2 * InitialUndeclared,
		},
		"success: a declared body of exactly the first buffer": {
			data: body(InitialDeclared), declared: InitialDeclared, limit: 1 << 30, wantCap: InitialDeclared + 1,
		},
		"success: a declared body past the first buffer": {
			data: body(InitialDeclared + 10), declared: InitialDeclared + 10, limit: 1 << 30, wantCap: InitialDeclared + 10 + 1,
		},
		"success: an undeclared body of exactly the limit": {data: body(5000), declared: -1, limit: 5000, wantCap: 5000 + 1},
		"error: declared over the limit, refused unread":   {data: nil, declared: 65, limit: 64, wantErr: ErrTooLarge},
		"error: undeclared one byte over the limit":        {data: body(65), declared: -1, limit: 64, wantErr: ErrTooLarge},
		"error: a read error":                              {data: body(10), declared: 20, limit: 64, err: io.ErrUnexpectedEOF, wantErr: io.ErrUnexpectedEOF},
		"success: declared zero, a few bytes sent":         {data: body(3), declared: 0, limit: 64},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := ReadBody(&chunks{data: tt.data, step: 700, err: tt.err}, tt.declared, tt.limit)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ReadBody error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if !bytes.Equal(got, tt.data) {
				t.Errorf("ReadBody = %d bytes, want the %d sent", len(got), len(tt.data))
			}
			if tt.wantCap != 0 && cap(got) != tt.wantCap {
				t.Errorf("cap = %d, want %d", cap(got), tt.wantCap)
			}
		})
	}
}
