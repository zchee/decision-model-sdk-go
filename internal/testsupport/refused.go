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

package testsupport

import (
	"net"
	"testing"
	"time"
)

// RefusedAddr returns a loopback address that refuses every connection
// until the test ends, on every OS: the local end of a TCP connection the
// test holds open, where nothing listens.
//
// The connection holds the port while the test runs, so a listener that
// asks for any port (127.0.0.1:0), as the tests' listeners do, is not given
// it on darwin or Linux (Windows is unmeasured). A closed listener's address
// would not do: it is free for anyone at once, and under load another
// listener can take it before the dial that was meant to be refused.
func RefusedAddr(tb testing.TB) string {
	tb.Helper()
	addr, _ := refusedConn(tb)
	return addr
}

// refusedConn returns RefusedAddr's address and the connection whose local
// end it is, which stays open until the test ends.
func refusedConn(tb testing.TB) (addr string, held net.Conn) {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("testsupport: RefusedAddr: listen: %v", err)
	}
	defer ln.Close()
	held, err = net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		tb.Fatalf("testsupport: RefusedAddr: dial: %v", err)
	}
	peer, err := ln.Accept()
	if err != nil {
		_ = held.Close()
		tb.Fatalf("testsupport: RefusedAddr: accept: %v", err)
	}
	tb.Cleanup(func() {
		_ = held.Close()
		_ = peer.Close()
	})
	return held.LocalAddr().String(), held
}
