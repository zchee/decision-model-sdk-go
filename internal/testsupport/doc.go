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

// Package testsupport holds the test doubles and measurement helpers the
// SDK's tests share.
//
// It provides:
//
//   - [LoopbackServer]: a TLS server on 127.0.0.1 that speaks HTTP/2 through
//     its own frame writer (golang.org/x/net/http2's Framer and hpack), so a
//     test can send GOAWAY with a LastStreamID below streams in flight, refuse
//     a stream, close the connection (close_notify) or reset it (TCP RST),
//     advertise a MAX_CONCURRENT_STREAMS limit and count accepted
//     connections; its ALPN modes also give a server without ALPN and one
//     that offers http/1.1 only.
//   - [SilentListener]: a TCP listener that accepts and never answers, for a
//     peer that never finishes the TLS handshake.
//   - [GatedDialer]: a client DialContext that holds each dial until the test
//     closes a channel. The LoopbackServer has no knob that delays its
//     handshake; a test that needs a dial to complete late gates the
//     client's dial to [LoopbackServer.Addr] instead.
//   - [FakeH2CServer]: an [net/http/httptest] server on the in-memory network
//     that speaks HTTP/2 over cleartext with prior knowledge, usable inside a
//     [testing/synctest] bubble.
//   - [Proxy]: an HTTP/1.1 CONNECT proxy, plain or behind TLS with lenient,
//     strict or h2-offering ALPN.
//   - [Recorder]: an in-memory [net/http.RoundTripper] that records requests
//     and returns canned replies.
//   - [LogRecorder]: a [log/slog.Handler] that keeps every record.
//   - [QuietRuntime], [Measure] and [MeasureMin]: allocation counting with the
//     collector off, GOMAXPROCS at 1, and a stable minimum of five runs.
//   - [Fixture] and friends: the response bodies under the module's testdata
//     directory, read once and cached.
//
// The package imports neither the SDK's root package nor internal/codec, so
// the packages that test the transport can use it without sonic. It is the
// only package of the module that imports golang.org/x/net.
package testsupport
