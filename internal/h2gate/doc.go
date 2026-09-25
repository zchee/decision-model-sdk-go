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

// Package h2gate is the SDK's transport: the stock net/http.Transport set up
// for one HTTP/2 connection per API host, with a cold-start gate and a
// header-write token in front of it (port plan section 6.3, owner decision
// G2, rulings R19, R20, R29, R29b, R29c and K21b).
//
// # The stock transport
//
// [NewTransport] builds the default transport: HTTP/2 only on https
// (Protocols{HTTP2}; prior knowledge, Protocols{UnencryptedHTTP2}, on http),
// MaxConnsPerHost 1, strict stream accounting
// (HTTP2Config.StrictMaxConcurrentRequests) with pings, a dial and a TLS
// handshake each bounded by the connect timeout, and a VerifyConnection hook
// that refuses, before any byte of a request, an API-hop handshake that did
// not negotiate h2 ([ErrNotNegotiated]). [Wrap] applies the same rules to a
// clone of a caller's *http.Transport and checks a caller's TLS dialer
// thinly. [HTTPAuto] keeps HTTP/1.1 available and refuses nothing.
//
// # The cold-start gate
//
// The first request on a cold transport leads: it dials. Every request that
// arrives while it dials waits, bounded by its own context and by the wait
// bound (connect timeout + TLS handshake timeout, plus the stock one-minute
// CONNECT limit and a second handshake when a proxy may apply). The leader's
// httptrace GotConn releases the waiters onto the new connection and makes
// the gate warm for good; later re-dials are the stock pool's. A waiter whose
// bound expires falls through to the transport, so no gate state can wedge
// the client. Before GotConn:
//
//   - a dial, TLS, ALPN or proxy failure while the leader's context is alive
//     gives the leader a [*DialError] and every waiter a fresh *DialError of
//     the same class around the same cause (R19); the gate turns cold;
//   - a leader whose context ended, or whose request failed before the
//     transport looked for a connection (an invalid header, a Proxy func
//     error), leaves without a verdict on the connection: the first waiter to
//     run takes over and the gate stays dialing, or, with nobody waiting, the
//     gate turns cold and the next caller leads;
//   - a waiter whose own context ends returns its context's error.
//
// # The header-write token
//
// Go 1.27's strict mode stalls when more callers than the server's
// MAX_CONCURRENT_STREAMS arrive at once (golang/go#70809, finding F1): a
// caller queued for the header lock already holds a stream reservation, and
// the reservations count against the slot the first caller waits for. One
// token per transport (a channel of one) serialises the window from the
// pool's reservation to the header write: a request takes it before
// RoundTrip and gives it back at httptrace WroteHeaders. The first request on
// each new HTTP/2 connection keeps it until its response headers (FirstHold),
// by which time the client has read the server's SETTINGS; the hold is
// bounded by the hold bound (connect timeout + TLS handshake timeout, 20 s at
// the defaults), after which the token is given back anyway. A request on an
// HTTP/1.1 connection ([HTTPAuto]) gives it back at GotConn. The stock
// transport's own replays (GOAWAY, REFUSED_STREAM) write their headers
// without the token (risk K21b).
//
// # Goroutines
//
// The package starts none of its own. Its hooks run on the transport's
// goroutines, and a FirstHold bound that expires runs one time.AfterFunc
// callback, which gives the token back.
//
// # Errors
//
// The package imports neither the SDK's root package nor internal/codec (port
// plan PM1), so it returns its own values: [ErrNotNegotiated] and
// [*DialError]. The root package maps them to its exported error types.
package h2gate
