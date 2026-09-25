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
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// SilentListener accepts TCP connections on 127.0.0.1 and never reads or
// writes a byte: a TLS client's handshake to it never completes, so a dial
// fails only when the client's own bound (TLSHandshakeTimeout, a context)
// expires. Close runs from tb.Cleanup.
type SilentListener struct {
	tb testing.TB
	ln net.Listener
	wg sync.WaitGroup

	accepts atomic.Int64

	mu     sync.Mutex
	closed bool
	conns  []net.Conn
}

// NewSilentListener starts a silent listener with a free port.
func NewSilentListener(tb testing.TB) *SilentListener {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("testsupport: listen: %v", err)
	}
	l := &SilentListener{tb: tb, ln: ln}
	l.wg.Go(l.acceptLoop)
	tb.Cleanup(l.Close)
	return l
}

// Addr returns the listener's address, 127.0.0.1:port.
func (l *SilentListener) Addr() string { return l.ln.Addr().String() }

// URL returns "https://127.0.0.1:port".
func (l *SilentListener) URL() string { return "https://" + l.Addr() }

// Accepts returns the number of TCP connections accepted so far.
func (l *SilentListener) Accepts() int { return int(l.accepts.Load()) }

// Close stops the listener and closes every accepted connection. It is
// idempotent.
func (l *SilentListener) Close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	conns := l.conns
	l.conns = nil
	l.mu.Unlock()
	_ = l.ln.Close()
	for _, c := range conns {
		_ = c.Close()
	}
	waitGroupTimeout(l.tb, &l.wg, "SilentListener", 10*time.Second)
}

// acceptLoop accepts and keeps connections until the listener closes.
func (l *SilentListener) acceptLoop() {
	for {
		c, err := l.ln.Accept()
		if err != nil {
			return
		}
		l.accepts.Add(1)
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			_ = c.Close()
			return
		}
		l.conns = append(l.conns, c)
		l.mu.Unlock()
	}
}
