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
	"net/http"
	"testing"
	"time"
)

// TestDrainBoundClosesSilentClient pins the path ruling R100 left unpinned:
// a client that reads the server's close_notify and never closes its own
// side still gets the server's close once the drain bound passes. The
// records say so in order: the graceful close began (CloseWriteSeq), the
// reader never read the end of the client's side (PeerClosedSeq 0), and the
// socket closed after that (ClosedSeq), no earlier than the bound. The
// bound is shortened for the test (h2Hooks.drainBound); the default is
// drainBound, 5 s.
//
// It runs in CI's -race test step (go test -race with coverage) and its
// non-race allocation-tests step (go test -count=1 ./internal/codec/
// ./internal/wire/ ./internal/testsupport/), on ubuntu-26.04, xcode-27 and
// windows-2025.
func TestDrainBoundClosesSilentClient(t *testing.T) {
	const bound = 200 * time.Millisecond
	// A timer that fires on a coarse clock (about 15.6 ms ticks on Windows)
	// may fire up to one tick early by time.Since.
	const tick = 20 * time.Millisecond
	srv := NewLoopbackServer(t, ServerConfig{Handler: http.NotFoundHandler()})
	srv.hooks.Store(&h2Hooks{drainBound: bound})
	c := dialRaw(t, srv.Addr())
	c.serverSettings()
	waitFor(t, "the live connection", func() bool { return len(srv.LiveH2Conns()) == 1 })

	start := time.Now()
	srv.CloseConns()
	c.expectEOF() // close_notify; the client neither writes nor closes from here on
	waitFor(t, "the server's close of the socket", func() bool { return srv.Conns()[0].ClosedSeq != 0 })
	elapsed := time.Since(start)

	ci := srv.Conns()[0]
	if ci.CloseWriteSeq == 0 || ci.PeerClosedSeq != 0 || ci.CloseWriteSeq >= ci.ClosedSeq {
		t.Errorf("close records %+v, want CloseWriteSeq, no PeerClosedSeq, then ClosedSeq", ci)
	}
	if elapsed < bound-tick {
		t.Errorf("the server closed the socket %v after its close began, before the %v drain bound", elapsed, bound)
	}
}
