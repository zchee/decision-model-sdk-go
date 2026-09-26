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
	"net"
	"os"
	"runtime"
	"testing"
	"time"
)

// TestRefusedAddr checks RefusedAddr through refusedConn: its address is
// the local end of the connection it holds, and that connection is still
// open, so a dial to the address is refused on every OS; when runtime.GOOS
// is "linux" an explicit listen on it fails too. As the control, the
// address of a closed listener, which the tests used before (ruling K39),
// can be taken by the next listener, after which a dial meant to be refused
// succeeds. The closed-listener mutant (refusedConn returning the closed
// listener's address) fails the first check on every OS.
//
// It runs in CI's -race test step (go test -race with coverage) and its
// non-race allocation-tests step (go test -count=1 ./internal/codec/
// ./internal/wire/ ./internal/testsupport/), on ubuntu-26.04, xcode-27 and
// windows-2025; ubuntu-26.04 is the image that asserts the Linux-only half.
func TestRefusedAddr(t *testing.T) {
	t.Run("error: a closed listener's address can be taken again", func(t *testing.T) {
		// The race this shows can hit it too: another listener may take the
		// freed port before the re-listen. Retry once, on a new port.
		var addr string
		var taken net.Listener
		var err error
		for range 2 {
			ln, lerr := net.Listen("tcp", "127.0.0.1:0")
			if lerr != nil {
				t.Fatal(lerr)
			}
			addr = ln.Addr().String()
			if cerr := ln.Close(); cerr != nil {
				t.Fatal(cerr)
			}
			if taken, err = net.Listen("tcp", addr); err == nil {
				break
			}
		}
		if err != nil {
			t.Fatalf("listen on the closed listener's address %s: %v; want it free for anyone", addr, err)
		}
		defer taken.Close()
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatalf("dial %s: %v; want the listener that took the address to answer", addr, err)
		}
		_ = conn.Close()
	})

	t.Run("success: the address refuses every dial", func(t *testing.T) {
		addr, held := refusedConn(t)
		if local := held.LocalAddr().String(); addr != local {
			t.Fatalf("address %s is not the held connection's local end %s", addr, local)
		}
		// Open, the connection waits for data until its read deadline; a
		// closed one fails at once, and one whose peer closed reads EOF.
		if err := held.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
			t.Fatalf("set a read deadline on the held connection: %v", err)
		}
		if _, err := held.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("read on the held connection: %v; want it open, waiting until its read deadline", err)
		}
		// Only Linux refuses a listener the held port asked for by number;
		// darwin and Windows bind it (RefusedAddr's godoc).
		if ln, err := net.Listen("tcp", addr); err == nil {
			_ = ln.Close()
			if runtime.GOOS == "linux" {
				t.Fatalf("listen on %s succeeded; want the held connection to keep the port", addr)
			}
		}
		for range 2 {
			conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
			if err == nil {
				_ = conn.Close()
				t.Fatalf("dial %s succeeded; want it refused", addr)
			}
			if oe, ok := errors.AsType[*net.OpError](err); !ok || oe.Op != "dial" || oe.Timeout() {
				t.Fatalf("dial %s: %v; want a refused dial", addr, err)
			}
		}
	})
}
