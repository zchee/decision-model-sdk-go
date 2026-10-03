// Copyright 2026 The decision-model-sdk-go Authors.
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
	"errors"
	"io"
)

// ErrTooLarge ends a body read that passed the size limit.
var ErrTooLarge = errors.New("decision: response body over the size limit")

// The first buffer of a response body read.
const (
	// initialDeclared bounds the first buffer of a body whose length is
	// declared: the buffer holds min(Content-Length, initialDeclared).
	initialDeclared = 256 << 10
	// InitialUndeclared is the first buffer of a body that declares no
	// length (a chunked body); it doubles as the body grows.
	InitialUndeclared = 4 << 10
	// minGrow is the smallest room a growth step makes, so a body that
	// declared zero bytes and sent some does not grow a byte at a time.
	minGrow = 512
)

// ReadBody reads a response body of at most limit bytes. declared is its
// Content-Length, or -1 when it declares none.
//
//   - A declared length above limit is refused before anything is read.
//   - The first buffer holds min(declared, initialDeclared) bytes for a
//     declared body and InitialUndeclared for an undeclared one.
//   - A full buffer grows by doubling, up to the end the body may reach: its
//     declared length while it is within it, else the limit. Growth that
//     would pass half that end goes to the end at once.
//   - The buffer that can reach the end has one byte of room beyond it, so
//     the read that finds the end of the body, or the byte after the limit,
//     needs no other buffer; the buffers before it are exact powers of two,
//     so none costs the allocator a page for one byte.
//   - The read stops at limit + 1 bytes: that byte makes the body too large.
//
// It returns ErrTooLarge for a body over the limit, and a read's own error
// other than io.EOF as it is.
func ReadBody(r io.Reader, declared, limit int64) ([]byte, error) {
	if declared > limit {
		return nil, ErrTooLarge
	}
	var size int64
	switch {
	case declared > initialDeclared:
		size = initialDeclared
	case declared >= 0:
		size = declared + 1
	default:
		size = min(InitialUndeclared, limit+1)
	}
	buf := make([]byte, 0, size)
	for {
		if len(buf) == cap(buf) {
			if int64(len(buf)) > limit {
				return nil, ErrTooLarge
			}
			buf = grow(buf, declared, limit)
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err != nil {
			if int64(len(buf)) > limit {
				return nil, ErrTooLarge
			}
			if errors.Is(err, io.EOF) {
				return buf, nil
			}
			return nil, err
		}
	}
}

// grow returns buf, which is full and within limit, with room for more: see
// ReadBody.
func grow(buf []byte, declared, limit int64) []byte {
	held := int64(len(buf))
	end := limit
	if declared > held {
		end = declared // within limit: ReadBody refused a larger one
	}
	next := max(2*held, minGrow)
	if 2*next > end {
		next = end + 1
	}
	nb := make([]byte, held, next)
	copy(nb, buf)
	return nb
}
