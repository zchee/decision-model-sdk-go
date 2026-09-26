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
	"errors"
	"runtime"
	"testing"
)

// TestGuard checks the guard-page memory that the codec's TestGuardPage
// runs sonic over: a read one byte past the end or before the start faults
// and CatchFault reports it, a read inside does not, AtEnd and AtStart put
// the input's bytes at the two edges, and a panic that is not a fault
// passes through CatchFault. Off Linux and Darwin NewGuard skips.
func TestGuard(t *testing.T) {
	g := NewGuard(t, 100)
	tests := map[string]struct {
		run       func()
		wantFault bool
	}{
		"error: one byte past the end faults":     {run: g.ReadPastEnd, wantFault: true},
		"error: one byte before the start faults": {run: g.ReadPastStart, wantFault: true},
		"success: the last readable byte reads":   {run: func() { runtime.KeepAlive(g.AtEnd([]byte("xyz"))[2]) }},
		"success: the first readable byte reads":  {run: func() { runtime.KeepAlive(g.AtStart([]byte("xyz"))[0]) }},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			msg, faulted := CatchFault(tt.run)
			if faulted != tt.wantFault {
				t.Fatalf("faulted = %t (%q), want %t", faulted, msg, tt.wantFault)
			}
			if faulted && msg == "" {
				t.Error("a fault with no message")
			}
		})
	}
	t.Run("success: AtEnd and AtStart place the input at the edges", func(t *testing.T) {
		end := g.AtEnd([]byte("tail"))
		if string(end) != "tail" || cap(end) != len(end) {
			t.Errorf("AtEnd = %q with capacity %d, want \"tail\" ending the memory", end, cap(end))
		}
		if &end[len(end)-1] != &g.mem[g.page+g.data-1] {
			t.Error("AtEnd's last byte is not the last readable byte")
		}
		start := g.AtStart([]byte("head"))
		if string(start) != "head" || &start[0] != &g.mem[g.page] {
			t.Errorf("AtStart = %q, want \"head\" at the first readable byte", start)
		}
	})
	t.Run("error: a panic that is not a fault passes through", func(t *testing.T) {
		boom := errors.New("boom")
		defer func() {
			if err, ok := recover().(error); !ok || !errors.Is(err, boom) {
				t.Errorf("recovered %v, want the panic value passed on", err)
			}
		}()
		CatchFault(func() { panic(boom) })
		t.Error("CatchFault returned after a panic that is not a fault")
	})
}
