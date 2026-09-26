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
	"sync"
	"testing"
)

// TestGoAwayRaceWithFinish is the deterministic regression test of the
// D-TSflake race (ruling K25): the goroutine that finishes a connection's
// last stream runs maybeFinish, and so closeWrite (close_notify), in the
// window between GoAway publishing its state and writing the frame. The
// hooks stop GoAway in that window and start that goroutine there; GoAway
// must still return nil, the client must read the GOAWAY frame before the
// connection ends, and the close records must be in that order. Before the
// fix GoAway did not hold the write lock in the window, so the hook let
// close_notify leave first, every time: the frame write failed with "tls:
// protocol is shutdown" and the client read EOF without a GOAWAY.
//
// It runs in CI's -race test step (go test -race with coverage) and its
// non-race allocation-tests step (go test -count=1 ./internal/codec/
// ./internal/wire/ ./internal/testsupport/), on ubuntu-26.04, xcode-27 and
// windows-2025.
func TestGoAwayRaceWithFinish(t *testing.T) {
	srv := NewLoopbackServer(t, ServerConfig{Handler: http.NotFoundHandler()})
	c := dialRaw(t, srv.Addr())
	c.serverSettings()
	waitFor(t, "the live connection", func() bool { return len(srv.LiveH2Conns()) == 1 })
	conn := srv.LiveH2Conns()[0]

	entered := make(chan struct{})  // the finisher is about to take the write lock
	finished := make(chan struct{}) // the finisher's maybeFinish returned
	var once sync.Once
	srv.hooks.Store(&h2Hooks{
		goAwayPublished: func(hc *H2Conn) {
			go func() {
				hc.maybeFinish() // what finishStream runs after the last stream
				close(finished)
			}()
			<-entered
			// With the write lock free in this window, close_notify leaves
			// before the frame: wait for it, as the race did.
			if hc.wmu.TryLock() {
				hc.wmu.Unlock()
				<-finished
			}
		},
		closeWriteEnter: func(*H2Conn) { once.Do(func() { close(entered) }) },
	})

	if err := conn.GoAway(0, CodeNoError); err != nil {
		t.Fatalf("GoAway: %v", err)
	}
	c.expect(frame{Type: "GOAWAY", Code: CodeNoError})
	c.expectEOF()
	<-finished
	if ci := srv.Conns()[0]; ci.GoAwaySeq == 0 || ci.GoAwaySeq >= ci.CloseWriteSeq {
		t.Errorf("close records %+v, want GoAwaySeq before CloseWriteSeq", ci)
	}
}
