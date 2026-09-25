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
	"bytes"
	"errors"
	"io"
	rand "math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// poison is the byte a recycled buffer is overwritten with in these tests.
// pattern never produces it, so a reader that sees it read recycled memory.
const poison = 0xFF

// pattern returns n bytes that never contain poison.
func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

// recycleLog records every recycle of a body during one test.
type recycleLog struct {
	count  atomic.Int32
	pooled atomic.Bool
	cap    atomic.Int64
}

// watchRecycle installs testHookRecycle for the duration of t. The hook
// overwrites the buffer with poison before it goes back to the pool, as
// another call reusing it would: under -race, a Read still copying from it
// without a happens-before edge is reported, and without -race it reads
// poison.
func watchRecycle(t *testing.T) *recycleLog {
	t.Helper()
	log := &recycleLog{}
	prev := testHookRecycle
	testHookRecycle = func(buf []byte, pooled bool) {
		for i := range buf {
			buf[i] = poison
		}
		log.count.Add(1)
		log.pooled.Store(pooled)
		log.cap.Store(int64(cap(buf)))
	}
	t.Cleanup(func() { testHookRecycle = prev })
	return log
}

// newBodyWith returns a body holding payload.
func newBodyWith(payload []byte) *Body {
	b := NewBody()
	*b.Buffer() = append(*b.Buffer(), payload...)
	return b
}

func TestBodyReadsTheEncodedBytes(t *testing.T) {
	tests := map[string]struct {
		size  int
		chunk int // Read buffer size; 0 means io.ReadAll
	}{
		"success: empty body":                         {size: 0},
		"success: one byte":                           {size: 1},
		"success: exactly the initial capacity":       {size: scratchInitialCap},
		"success: one byte past the initial capacity": {size: scratchInitialCap + 1},
		"success: 1 MiB":                              {size: 1 << 20},
		"success: small reads reassemble the body":    {size: 10_000, chunk: 7},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			log := watchRecycle(t)
			payload := pattern(tt.size)
			b := newBodyWith(payload)
			if b.Len() != tt.size || !bytes.Equal(b.Bytes(), payload) {
				t.Fatalf("Len() = %d, Bytes() equal = %t; want %d, true", b.Len(), bytes.Equal(b.Bytes(), payload), tt.size)
			}
			// Two readers are independent copies of the same bytes, as the
			// request body and a GetBody result are.
			for i := range 2 {
				r, err := b.Open()
				if err != nil {
					t.Fatalf("Open() #%d: %v", i, err)
				}
				got := readAll(t, r, tt.chunk)
				if !bytes.Equal(got, payload) {
					t.Fatalf("reader #%d read %d bytes, want the %d-byte payload", i, len(got), len(payload))
				}
				if n, err := r.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
					t.Errorf("Read after the end = %d, %v; want 0, io.EOF", n, err)
				}
				if err := r.Close(); err != nil {
					t.Errorf("Close() = %v, want nil", err)
				}
			}
			if got := log.count.Load(); got != 0 {
				t.Fatalf("recycled %d times while the call still holds its reference, want 0", got)
			}
			b.Release()
			if got := log.count.Load(); got != 1 {
				t.Errorf("recycled %d times after the last reference, want 1", got)
			}
		})
	}
}

// readAll reads r to the end with reads of chunk bytes (io.ReadAll when
// chunk is 0).
func readAll(t *testing.T, r io.Reader, chunk int) []byte {
	t.Helper()
	if chunk == 0 {
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("io.ReadAll: %v", err)
		}
		return got
	}
	var got []byte
	p := make([]byte, chunk)
	for {
		n, err := r.Read(p)
		got = append(got, p[:n]...)
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
}

func TestBodyReferenceCounting(t *testing.T) {
	tests := map[string]struct {
		run func(t *testing.T, b *Body, log *recycleLog)
	}{
		"success: a double Close drops one reference": {
			run: func(t *testing.T, b *Body, log *recycleLog) {
				r := mustOpen(t, b)
				for range 2 {
					if err := r.Close(); err != nil {
						t.Fatalf("Close() = %v, want nil", err)
					}
				}
				// Had the second Close counted, the call's own reference
				// would already be gone and the buffer recycled.
				wantRecycled(t, log, 0)
				b.Release()
				wantRecycled(t, log, 1)
			},
		},
		"success: a double Release drops one reference": {
			run: func(t *testing.T, b *Body, log *recycleLog) {
				r := mustOpen(t, b)
				b.Release()
				b.Release()
				wantRecycled(t, log, 0) // the reader still holds the body
				if err := r.Close(); err != nil {
					t.Fatalf("Close() = %v", err)
				}
				wantRecycled(t, log, 1)
			},
		},
		"success: the buffer outlives the call while a reader is open": {
			run: func(t *testing.T, b *Body, log *recycleLog) {
				r := mustOpen(t, b)
				b.Release()
				wantRecycled(t, log, 0)
				got := readAll(t, r, 0)
				if !bytes.Equal(got, pattern(64)) {
					t.Fatalf("a reader open after Release read %v, want the payload", got)
				}
				_ = r.Close()
				wantRecycled(t, log, 1)
			},
		},
		"success: Open after Release succeeds while a reader holds the body": {
			run: func(t *testing.T, b *Body, log *recycleLog) {
				r1 := mustOpen(t, b)
				b.Release()
				r2 := mustOpen(t, b)
				_ = r1.Close()
				wantRecycled(t, log, 0)
				if got := readAll(t, r2, 0); !bytes.Equal(got, pattern(64)) {
					t.Fatalf("second reader read %v, want the payload", got)
				}
				_ = r2.Close()
				wantRecycled(t, log, 1)
			},
		},
		"error: Read after Close returns ErrBodyClosed": {
			run: func(t *testing.T, b *Body, log *recycleLog) {
				r := mustOpen(t, b)
				if _, err := r.Read(make([]byte, 8)); err != nil {
					t.Fatalf("first Read: %v", err)
				}
				_ = r.Close()
				n, err := r.Read(make([]byte, 8))
				if n != 0 || !errors.Is(err, ErrBodyClosed) {
					t.Errorf("Read after Close = %d, %v; want 0, ErrBodyClosed", n, err)
				}
				b.Release()
				wantRecycled(t, log, 1)
			},
		},
		"error: Open after the last reference returns ErrBodyReleased": {
			run: func(t *testing.T, b *Body, log *recycleLog) {
				b.Release()
				wantRecycled(t, log, 1)
				r, err := b.Open()
				if r != nil || !errors.Is(err, ErrBodyReleased) {
					t.Errorf("Open after release = %v, %v; want nil, ErrBodyReleased", r, err)
				}
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			log := watchRecycle(t)
			tt.run(t, newBodyWith(pattern(64)), log)
		})
	}
}

func mustOpen(t *testing.T, b *Body) *BodyReader {
	t.Helper()
	r, err := b.Open()
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	return r
}

func wantRecycled(t *testing.T, log *recycleLog, want int32) {
	t.Helper()
	if got := log.count.Load(); got != want {
		t.Fatalf("recycled %d times, want %d", got, want)
	}
}

func TestBodyScratchCeiling(t *testing.T) {
	tests := map[string]struct {
		capacity   int
		wantPooled bool
	}{
		"success: the initial capacity is pooled":         {capacity: scratchInitialCap, wantPooled: true},
		"success: 6 MiB is pooled":                        {capacity: 6 << 20, wantPooled: true},
		"success: exactly the ceiling is pooled":          {capacity: ScratchCeiling, wantPooled: true},
		"success: one byte past the ceiling is dropped":   {capacity: ScratchCeiling + 1, wantPooled: false},
		"success: 9 MiB is dropped for the GC to reclaim": {capacity: 9 << 20, wantPooled: false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			log := watchRecycle(t)
			b := NewBody()
			buf := make([]byte, 16, tt.capacity)
			copy(buf, "{\"state\":\"x\"}")
			*b.Buffer() = buf
			b.Release()
			wantRecycled(t, log, 1)
			if got := log.pooled.Load(); got != tt.wantPooled {
				t.Errorf("pooled = %t for capacity %d, want %t (ceiling %d)", got, tt.capacity, tt.wantPooled, ScratchCeiling)
			}
			if got := log.cap.Load(); got != int64(tt.capacity) {
				t.Errorf("recycled capacity = %d, want %d", got, tt.capacity)
			}
		})
	}
}

func TestNewBodyStartsEmpty(t *testing.T) {
	// Whichever body the pool hands out, a new one starts empty and live,
	// even when the last user left bytes and a closed state behind.
	for i := range 64 {
		b := newBodyWith(pattern(100 + i))
		r := mustOpen(t, b)
		_ = r.Close()
		b.Release()

		fresh := NewBody()
		if fresh.Len() != 0 {
			t.Fatalf("iteration %d: NewBody().Len() = %d, want 0", i, fresh.Len())
		}
		r2, err := fresh.Open()
		if err != nil {
			t.Fatalf("iteration %d: Open on a new body: %v", i, err)
		}
		_ = r2.Close()
		fresh.Release()
	}
}

func TestBodyReaderCloseWaitsForInFlightRead(t *testing.T) {
	log := watchRecycle(t)
	b := newBodyWith(pattern(64))
	r := mustOpen(t, b)
	b.Release() // the reader now holds the last reference

	// Stand in for a Read that is copying: hold the lock Read holds.
	r.mu.Lock()
	closed := make(chan struct{})
	go func() {
		_ = r.Close()
		close(closed)
	}()
	for !r.closed.Load() {
		runtime.Gosched()
	}
	// Close has flipped the flag and must now be waiting for the "Read".
	// A Close that did not wait would drop the reference within this window;
	// a correct one never does, so this check cannot fail spuriously.
	time.Sleep(20 * time.Millisecond)
	select {
	case <-closed:
		t.Fatal("Close returned while a Read held the reader")
	default:
	}
	wantRecycled(t, log, 0)

	r.mu.Unlock()
	<-closed
	wantRecycled(t, log, 1)
}

// TestBodyConcurrentReadersAndClosers opens, reads and closes readers from
// many goroutines while the call's reference is released at a random point,
// as the HTTP/2 transport does when it closes a body from its own goroutine.
// Run it with -race: the recycle hook writes poison into the buffer in the
// goroutine that drops the last reference, so a Read that could still copy
// from the buffer after that point is a reported data race, and without
// -race it would read poison.
func TestBodyConcurrentReadersAndClosers(t *testing.T) {
	const (
		iterations = 40
		readers    = 24
		size       = 64 << 10
	)
	payload := pattern(size)
	for iter := range uint64(iterations) {
		log := watchRecycle(t)
		b := newBodyWith(payload)

		var wg sync.WaitGroup
		for g := range uint64(readers) {
			wg.Go(func() {
				rng := seeded(iter, g)
				r, err := b.Open()
				if err != nil {
					// Only possible once every reference is gone.
					if !errors.Is(err, ErrBodyReleased) {
						t.Errorf("Open: %v", err)
					}
					return
				}
				// A second goroutine closes the reader at a random moment,
				// sometimes twice, while this one reads. It draws from its
				// own source: a rand.Rand is not safe for concurrent use.
				closerRNG := seeded(iter, g|1<<32)
				wg.Go(func() {
					for range closerRNG.IntN(64) {
						runtime.Gosched()
					}
					_ = r.Close()
					if closerRNG.IntN(2) == 0 {
						_ = r.Close()
					}
				})
				readConcurrently(t, r, payload, rng)
				_ = r.Close()
			})
		}
		for range seeded(iter, 0xca11).IntN(32) {
			runtime.Gosched()
		}
		b.Release()
		wg.Wait()

		if got := log.count.Load(); got != 1 {
			t.Fatalf("iteration %d: recycled %d times, want exactly 1", iter, got)
		}
		if got := b.refs.Load(); got != 0 {
			t.Fatalf("iteration %d: %d references left, want 0", iter, got)
		}
	}
}

// seeded returns a random source with a fixed seed, so that a failing
// schedule of reads and closes can be replayed.
func seeded(a, b uint64) *rand.Rand {
	return rand.New(rand.NewPCG(a, b)) //nolint:gosec // G404: scheduling jitter for a test, not a secret.
}

// readConcurrently reads r with random-sized reads until EOF or
// ErrBodyClosed, checking every byte against want.
func readConcurrently(t *testing.T, r *BodyReader, want []byte, rng *rand.Rand) {
	off := 0
	p := make([]byte, 4096)
	for {
		n, err := r.Read(p[:1+rng.IntN(len(p))])
		if !bytes.Equal(p[:n], want[off:off+n]) {
			t.Errorf("read bytes [%d, %d) that differ from the body (recycled memory?)", off, off+n)
			return
		}
		off += n
		switch {
		case err == nil:
		case errors.Is(err, io.EOF):
			if off != len(want) {
				t.Errorf("EOF after %d bytes, want %d", off, len(want))
			}
			return
		case errors.Is(err, ErrBodyClosed):
			return
		default:
			t.Errorf("Read: %v", err)
			return
		}
	}
}
