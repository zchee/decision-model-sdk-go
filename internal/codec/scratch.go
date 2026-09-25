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
	"io"
	"sync"
	"sync/atomic"
)

const (
	// scratchInitialCap is the capacity of a scratch buffer the pool creates.
	scratchInitialCap = 4 << 10

	// ScratchCeiling is the largest capacity a scratch buffer may have and
	// still go back to the pool when its body is released. A body that grew
	// past it is left to the garbage collector, so the pool never retains
	// more than this per buffer (NF1).
	ScratchCeiling = 8 << 20
)

var (
	// ErrBodyClosed is returned by a Read on a [BodyReader] that has been
	// closed.
	ErrBodyClosed = errors.New("read on closed request body")

	// ErrBodyReleased is returned by [Body.Open] once every reference to the
	// body has been dropped and its buffer may already serve another call.
	ErrBodyReleased = errors.New("request body already released")
)

// bodyPool holds released bodies with their scratch buffers. Pooling the Body
// rather than the bare buffer keeps taking a scratch free of allocations.
var bodyPool = sync.Pool{
	New: func() any { return &Body{buf: make([]byte, 0, scratchInitialCap)} },
}

// testHookRecycle, when a test sets it, runs in the goroutine that drops a
// body's last reference, before the buffer goes back to the pool (pooled) or
// is left to the garbage collector (!pooled).
var testHookRecycle func(buf []byte, pooled bool)

// Body is a request body encoded into a pooled scratch buffer. Once built it
// is immutable and shared by every reader the transport opens on it: the
// request's Body and each GetBody result of a replay or a retry.
//
// A Body is reference-counted. [NewBody] returns it holding one reference,
// the SDK call's own, which [Body.Release] drops when the call returns; each
// [Body.Open] adds one for the returned reader, which the reader's Close
// drops. The buffer goes back to the pool only when the count reaches zero,
// so a retry never re-sends bytes that another call has overwritten (PM4). A
// reader the transport never closes (the HTTP/2 replay path may drop one)
// keeps the count above zero: its buffer is then garbage-collected with it
// and never reused.
type Body struct {
	buf      []byte
	refs     atomic.Int64
	released atomic.Bool
}

// NewBody takes a body with an empty scratch buffer from the pool. It holds
// the caller's reference, which the caller drops with [Body.Release].
func NewBody() *Body {
	b := bodyPool.Get().(*Body)
	b.buf = b.buf[:0]
	b.released.Store(false)
	b.refs.Store(1)
	return b
}

// Buffer returns the scratch buffer for appending the encoded body, as sonic's
// encoder.EncodeInto takes it. It may be used only while the body is being
// built, before the first [Body.Open].
func (b *Body) Buffer() *[]byte { return &b.buf }

// Bytes returns the encoded body. The slice is shared: it must not be
// modified, and it is valid only while the caller holds a reference.
func (b *Body) Bytes() []byte { return b.buf }

// Len returns the length of the encoded body, for the request's
// ContentLength.
func (b *Body) Len() int { return len(b.buf) }

// Open returns a new reader over the body, holding one more reference until
// its Close. It fails with [ErrBodyReleased] once the count has reached
// zero. Open is safe for concurrent use with every other method except
// [Body.Buffer].
func (b *Body) Open() (*BodyReader, error) {
	for {
		n := b.refs.Load()
		if n <= 0 {
			return nil, ErrBodyReleased
		}
		if b.refs.CompareAndSwap(n, n+1) {
			return &BodyReader{body: b, data: b.buf}, nil
		}
	}
}

// Release drops the SDK call's reference. It is idempotent: only the first
// call counts.
func (b *Body) Release() {
	if b.released.CompareAndSwap(false, true) {
		b.unref()
	}
}

// unref drops one reference and recycles the buffer when it was the last.
func (b *Body) unref() {
	n := b.refs.Add(-1)
	if n > 0 {
		return
	}
	if n < 0 {
		panic("codec: request body reference count below zero")
	}
	pooled := cap(b.buf) <= ScratchCeiling
	if testHookRecycle != nil {
		testHookRecycle(b.buf, pooled)
	}
	if pooled {
		bodyPool.Put(b)
	}
}

// BodyReader reads one copy of a [Body]. It implements io.ReadCloser for
// http.Request.Body and GetBody.
//
// Read and Close are safe for concurrent use: the HTTP transports may close a
// request body from another goroutine while a write loop is still reading it.
// Close waits for an in-flight Read to return, so the buffer is never
// recycled under a reader.
type BodyReader struct {
	body   *Body
	mu     sync.Mutex // held by Read; Close takes it to wait for an in-flight Read
	data   []byte     // the body's bytes; nil once closed
	off    int
	closed atomic.Bool
}

var _ io.ReadCloser = (*BodyReader)(nil)

// Read reads the next bytes of the body. It returns [ErrBodyClosed] after
// Close, and io.EOF at the end of the body.
func (r *BodyReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed.Load() {
		return 0, ErrBodyClosed
	}
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}

// Close drops the reader's reference to the body after any in-flight Read
// has returned. It is idempotent: a compare-and-swap on the closed flag lets
// only the first call drop the reference. It always returns nil.
func (r *BodyReader) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}
	r.mu.Lock()
	r.data = nil
	r.mu.Unlock()
	r.body.unref()
	return nil
}
