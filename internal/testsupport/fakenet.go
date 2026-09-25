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
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// FakeH2CServer is an [net/http/httptest] server on the in-memory network
// (httptest.NewTestServer) that speaks HTTP/2 over cleartext with prior
// knowledge. It works inside a [testing/synctest] bubble when created there.
//
// Its Client's transport has Protocols set to unencrypted HTTP/2 only and
// dials the in-memory listener whatever the request's host, so a request to
// "http://example.com/..." reaches the server as h2c. The server refuses
// HTTP/1.1 and TLS, so a request that fell back to either fails loudly. A
// test that needs its own transport starts from Client().Transport (the
// DialContext that reaches the in-memory network lives there).
type FakeH2CServer struct {
	*httptest.Server

	accepts atomic.Int64
}

// NewFakeH2CServer returns a started FakeH2CServer serving handler (nil
// answers 500, as httptest.NewTestServer does). httptest closes it through
// tb.Cleanup.
func NewFakeH2CServer(tb testing.TB, handler http.Handler) *FakeH2CServer {
	tb.Helper()
	s := &FakeH2CServer{Server: httptest.NewTestServer(tb, handler)}
	var server http.Protocols
	server.SetUnencryptedHTTP2(true)
	s.Config.Protocols = &server
	s.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			s.accepts.Add(1)
		}
	}
	tr, ok := s.Client().Transport.(*http.Transport)
	if !ok {
		tb.Fatalf("testsupport: httptest client transport is %T, want *http.Transport", s.Client().Transport)
	}
	var client http.Protocols
	client.SetUnencryptedHTTP2(true)
	tr.Protocols = &client
	return s
}

// Accepts returns the number of connections the server accepted so far.
func (s *FakeH2CServer) Accepts() int { return int(s.accepts.Load()) }
