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

package testsupport

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// Guard is memory for a reader's input with no readable memory around it:
// whole pages mapped between two pages that no access may touch
// (PROT_NONE), so that a read one byte before the first readable byte or
// one byte past the last one faults instead of reading a neighbour's
// memory. sonic's native scanner can read up to 4 bytes past a short input
// (internal/codec's minSonicInput); the Go heap, which always has something
// after an allocation, cannot show such a read.
type Guard struct {
	mem  []byte // the leading guard page, the readable pages, the trailing guard page
	page int    // the page size
	data int    // the bytes of readable pages between the guards
}

// NewGuard maps room for inputs of up to size bytes between two guard pages
// and unmaps it when the test ends. It needs mmap and mprotect, which the
// syscall package offers on Linux and Darwin; on any other system it skips
// the test, saying so.
func NewGuard(tb testing.TB, size int) *Guard {
	tb.Helper()
	return newGuard(tb, size)
}

// AtEnd copies in so that its last byte is the last readable byte, and
// returns the copy: a read of one byte past it faults.
func (g *Guard) AtEnd(in []byte) []byte {
	end := g.page + g.data
	b := g.mem[end-len(in) : end : end]
	copy(b, in)
	return b
}

// AtStart copies in so that its first byte is the first readable byte, and
// returns the copy: a read of the byte before it faults.
func (g *Guard) AtStart(in []byte) []byte {
	b := g.mem[g.page : g.page+len(in) : g.page+len(in)]
	copy(b, in)
	return b
}

// ReadPastEnd reads the byte just past the last readable one, which must
// fault: the control that the trailing guard is in place. runtime.KeepAlive
// keeps the compiler from dropping the load.
func (g *Guard) ReadPastEnd() { runtime.KeepAlive(g.mem[g.page+g.data]) }

// ReadPastStart reads the byte just before the first readable one, which
// must fault: the control that the leading guard is in place.
func (g *Guard) ReadPastStart() { runtime.KeepAlive(g.mem[g.page-1]) }

// CatchFault runs f with the runtime's panic on a memory fault turned on for
// the goroutine (runtime/debug.SetPanicOnFault), and reports whether f
// faulted and the runtime's message. A panic that is not a fault is passed
// on.
func CatchFault(f func()) (msg string, faulted bool) {
	defer debug.SetPanicOnFault(debug.SetPanicOnFault(true))
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		s := fmt.Sprint(r)
		if !strings.Contains(s, "fault") && !strings.Contains(s, "invalid memory address") {
			panic(r)
		}
		msg, faulted = s, true
	}()
	f()
	return "", false
}
