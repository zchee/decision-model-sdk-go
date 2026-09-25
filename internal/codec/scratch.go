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
	// scratchInitialCap is the capacity of the scratch buffer NewBody
	// allocates for a scratch that has none.
	scratchInitialCap = 4 << 10

	// ScratchCeiling is the largest capacity a scratch buffer may have and
	// still go back to the pool when its body is released. A body that grew
	// past it is left to the garbage collector, so the pool never retains
	// more than this per buffer (NF1).
	ScratchCeiling = 8 << 20
)

// The state word of a scratch packs the generation of the body it currently
// holds with that body's reference count, so that one compare-and-swap checks
// both: a handle of an earlier generation can neither open nor release the
// body of a later call that reuses the scratch.
const (
	stateRefs     = 1<<31 - 1 // bits 0-30: the number of references
	stateReleased = 1 << 31   // bit 31: the SDK call has dropped its reference
	stateGenShift = 32        // bits 32-63: the generation
)

var (
	// ErrBodyClosed is returned by a Read on a [BodyReader] that has been
	// closed.
	ErrBodyClosed = errors.New("read on closed request body")

	// ErrBodyReleased is returned by [Body.Open] once every reference to the
	// body has been dropped, and by Open through a [Body] whose scratch
	// already serves a later call.
	ErrBodyReleased = errors.New("request body already released")
)

// scratch is the pooled state behind a [Body]: the buffer and the state word
// (generation, released flag, reference count).
type scratch struct {
	buf   []byte
	state atomic.Uint64
}

// scratchPool holds released scratches. Pooling the scratch rather than the
// bare buffer keeps taking one free of allocations; a scratch whose buffer
// outgrew [ScratchCeiling] comes back without its buffer, so the next
// NewBody pays one allocation, the buffer's.
var scratchPool = sync.Pool{
	New: func() any { return new(scratch) },
}

// testHookRecycle, when a test sets it, runs in the goroutine that drops a
// body's last reference, before the buffer goes back to the pool (pooled) or
// is left to the garbage collector (!pooled).
var testHookRecycle func(buf []byte, pooled bool)

// Body is a request body encoded into a pooled scratch buffer. Once built it
// is immutable and shared by every reader the transport opens on it: the
// request's Body and each GetBody result of a replay or a retry.
//
// A body is reference-counted. [NewBody] returns it holding one reference,
// the SDK call's own, which [Body.Release] drops when the call returns; each
// [Body.Open] adds one for the returned reader, which the reader's Close
// drops. The buffer goes back to the pool only when the count reaches zero,
// so a retry never re-sends bytes that another call has overwritten (PM4). A
// reader the transport never closes (the HTTP/2 replay path may drop one)
// keeps the count above zero: its buffer is then garbage-collected with it
// and never reused.
//
// A Body value is a handle: the pooled scratch and the generation NewBody
// gave it. Once the count has reached zero the scratch may serve a later
// call under a new generation, and every handle of the earlier one is stale:
// Open through it fails with [ErrBodyReleased] and Release does nothing, so a
// handle kept past its call can neither read nor free the next call's body.
// The generation has 32 bits: a handle would have to outlive 2^32 reuses of
// its scratch to match again. The zero Body is not usable.
type Body struct {
	s   *scratch
	gen uint32
}

// NewBody takes a body with an empty scratch buffer from the pool. It holds
// the caller's reference, which the caller drops with [Body.Release].
func NewBody() Body {
	s := scratchPool.Get().(*scratch)
	if s.buf == nil {
		s.buf = make([]byte, 0, scratchInitialCap)
	}
	s.buf = s.buf[:0]
	// No handle can change the word of a pooled scratch: its count is zero,
	// so Open and a reader's Close fail, and its released flag is set, so
	// Release does nothing.
	gen := uint32(s.state.Load()>>stateGenShift) + 1
	s.state.Store(uint64(gen)<<stateGenShift | 1)
	return Body{s: s, gen: gen}
}

// Buffer returns the scratch buffer for appending the encoded body, as sonic's
// encoder.EncodeInto takes it. It may be used only while the body is being
// built, before the first [Body.Open].
func (b Body) Buffer() *[]byte { return &b.s.buf }

// Bytes returns the encoded body. The slice is shared: it must not be
// modified, and it is valid only while the caller holds a reference.
func (b Body) Bytes() []byte { return b.s.buf }

// Len returns the length of the encoded body, for the request's
// ContentLength.
func (b Body) Len() int { return len(b.s.buf) }

// Open returns a new reader over the body, holding one more reference until
// its Close. It fails with [ErrBodyReleased] once the count has reached zero
// or the handle is stale. Open is safe for concurrent use with every other
// method except [Body.Buffer].
func (b Body) Open() (*BodyReader, error) {
	for {
		st := b.s.state.Load()
		if uint32(st>>stateGenShift) != b.gen || st&stateRefs == 0 {
			return nil, ErrBodyReleased
		}
		if st&stateRefs == stateRefs {
			panic("codec: request body reference count overflow")
		}
		if b.s.state.CompareAndSwap(st, st+1) {
			return &BodyReader{s: b.s, gen: b.gen, data: b.s.buf}, nil
		}
	}
}

// GetBody is [Body.Open] in the form of http.Request.GetBody: it returns a
// new reader over the body, or a nil reader and [ErrBodyReleased]. A request
// that sends the body sets GetBody to this method, so that a replay or a
// retry reads the same bytes.
func (b Body) GetBody() (io.ReadCloser, error) {
	r, err := b.Open()
	if err != nil {
		// A nil *BodyReader in the interface would not compare equal to nil.
		return nil, err
	}
	return r, nil
}

// Release drops the SDK call's reference. It is idempotent: only the first
// call through a live handle counts, and a stale handle changes nothing.
func (b Body) Release() {
	for {
		st := b.s.state.Load()
		if uint32(st>>stateGenShift) != b.gen || st&stateReleased != 0 {
			return
		}
		// While the flag is clear the call's own reference is counted, so
		// the count is at least one and the subtraction cannot borrow.
		next := (st | stateReleased) - 1
		if b.s.state.CompareAndSwap(st, next) {
			if next&stateRefs == 0 {
				b.s.recycle()
			}
			return
		}
	}
}

// unref drops one reader's reference of generation gen and recycles the
// scratch when it was the last. A reader holds its reference until this
// call, so the generation cannot have moved on; a mismatch or a zero count
// is a broken invariant.
func (s *scratch) unref(gen uint32) {
	for {
		st := s.state.Load()
		if uint32(st>>stateGenShift) != gen || st&stateRefs == 0 {
			panic("codec: request body reference dropped twice")
		}
		if s.state.CompareAndSwap(st, st-1) {
			if (st-1)&stateRefs == 0 {
				s.recycle()
			}
			return
		}
	}
}

// recycle returns the scratch to the pool once its last reference is gone.
// A buffer past [ScratchCeiling] is dropped for the garbage collector and the
// scratch goes back without it.
func (s *scratch) recycle() {
	pooled := cap(s.buf) <= ScratchCeiling
	if testHookRecycle != nil {
		testHookRecycle(s.buf, pooled)
	}
	if !pooled {
		s.buf = nil
	}
	scratchPool.Put(s)
}

// BodyReader reads one copy of a [Body]. It implements io.ReadCloser for
// http.Request.Body and GetBody.
//
// Read and Close are safe for concurrent use: the HTTP transports may close a
// request body from another goroutine while a write loop is still reading it.
// Close waits for an in-flight Read to return, so the buffer is never
// recycled under a reader.
type BodyReader struct {
	s      *scratch
	gen    uint32
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
	r.s.unref(r.gen)
	return nil
}
