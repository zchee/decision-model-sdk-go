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

// Package h2gate is the SDK's transport: the stock net/http.Transport set up
// for one HTTP/2 connection per API host, with a cold-start gate and a
// header-write token in front of it.
//
// # The stock transport
//
// [NewTransport] builds the default transport and [Wrap] applies the same
// rules to a clone of a caller's *http.Transport; their docs list the
// settings. Under [HTTP2Only] an API hop that does not negotiate h2 is
// refused with [ErrNotNegotiated], before any byte of a request wherever its
// handshake can be checked; [HTTPAuto] refuses nothing.
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
//     the same class around the same cause; the gate turns cold;
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
// MAX_CONCURRENT_STREAMS arrive at once (golang/go#70809): a caller queued
// for the header lock already holds a stream reservation, and the
// reservations count against the slot the first caller waits for. One token
// per transport (a channel of one) serialises the window from the pool's
// reservation to the header write: a request takes it before RoundTrip and
// gives it back at httptrace WroteHeaders. The first request on each new
// HTTP/2 connection keeps it until its response headers (FirstHold), by
// which time the client has read the server's SETTINGS, or until the hold
// bound (connect timeout + TLS handshake timeout, 20 s at the defaults). On
// an HTTP/1.1 connection ([HTTPAuto]) the token goes back at GotConn, so new
// HTTP/1.1 connections are dialled one at a time.
//
// The stock transport's own replays (GOAWAY, REFUSED_STREAM) write their
// headers without the token, and until the client has read a new
// connection's SETTINGS it assumes 100 streams, so the callers queued behind
// a GOAWAY could exceed the server's limit there and be refused into the
// stock retry backoff. A replay that opens a new connection marks it
// unsettled, and the next token holder there holds as a FirstHold does: one
// response time on each re-dial after a GOAWAY. The replay's own response
// clears a mark no holder has taken. The mark is set in the replay's GotConn
// hook, after the stock transport has recorded the connection as used (its
// Reused flag is a compare-and-swap before the hook,
// internal/http2/transport.go:423-424), so a holder whose GotConn falls
// between the two passes unheld; the window is a few instructions wide and
// cannot be closed from outside net/http.
//
// # Caller hooks
//
// A caller's httptrace hook, Proxy func or GetBody that panics inside
// RoundTrip reaches the caller unchanged; the transport gives the token back
// on the way out and resolves a leader's generation as a leader that left.
// A panic in GetConn on a warm HTTP/2 connection leaves the stock pool's
// mutex locked (internal/http2/client_conn_pool.go:52-61), and one in
// GotConn leaks the stream ReserveNewRequest reserved, which only
// cc.RoundTrip releases (internal/http2/transport.go:423-425); so
// internal/engine's shield recovers every caller hook in place and panics
// again once RoundTrip has returned.
//
// Hooks run while their request holds the token: from GetConn to
// WroteHeaders, and under FirstHold until the response headers. A request
// that waits for a token a blocked hook holds goes out without it at the
// hold bound (Stats.TokenExpiries), whatever its deadline, the root package's
// WithNoTimeout included, unless its own context ends first; the FirstHold
// bound, which starts only at WroteHeaders, does not end such a hold. The
// root package's WithClientTrace gives callers the contract hook by hook. Two
// cases need more.
//
// GetConn on a warm transport is unbounded: net/http holds its pool mutex
// while it calls the hook, so a request that leaves send at the hold bound
// blocks on the mutex, which neither a bound nor its context can end, until
// the hook returns. Only calling the hook on a goroutine of its own, apart
// from net/http's call, could bound it; the transport does not, and hooks
// must avoid the case.
//
// A new connection's DNS, connect and TLS hooks hold the connection permit,
// the only one MaxConnsPerHost 1 allows, and net/http's per-host wait for it
// ends only with a request's own context; its TLS handshake timeout closes
// the connection but then waits for the hook. Two bounds end those waits:
//
//   - The dial bound: NewTransport runs its dialer, inside which the DNS and
//     connect hooks run, in a goroutine of its own and waits for it at most
//     ConnectTimeout plus a grace of 100 ms, within which a dialer that
//     honours its context returns its own error. A dial still running then
//     fails with a timeout (Stats.DialExpiries), which frees the permit; the
//     goroutine is abandoned and closes a connection the dialer returns
//     later. Wrap keeps a caller's dialer as it is.
//   - The wait bound: a request whose trace has one of these hooks, one
//     that goes out without the token, and every request while the
//     transport is stalled waits for its connection at most waitBound
//     (Stats.WaitExpiries). An expiry marks the transport stalled until the
//     next GotConn, since a dial may still hold the permit; a TLS hook, or a
//     dial Wrap keeps, holds it until the hook returns.
//
// A hook a bound leaves running goes on with its dial after its request has
// returned, so the shield logs a panic it raises instead of raising it on a
// call. The bounds fire only after a stall, so a healthy burst's ordering and
// latency do not change; a free token is taken without a timer, and a request
// with neither a dial-phase hook nor a stall to wait behind arms no wait
// bound. Requests that go out without the token may exceed the server's
// stream limit, and the stock transport retries a refused stream. Hooks must
// return promptly: the shield covers panics, and the bounds a hook that
// blocks, except GetConn on a warm transport.
//
// # Logging
//
// The transport logs its events to [Config.Logger], whose doc lists them.
// An event that prints an error renders it through [Config.ErrorText], and
// only for a logger that keeps DEBUG events.
//
// # Goroutines
//
// The package starts one goroutine for each dial of NewTransport's dialer,
// which ends when the dial returns; one the dial bound abandoned ends when
// the caller's hook returns. Its hooks run on the transport's goroutines. A
// FirstHold bound that expires runs one time.AfterFunc callback, which gives
// the token back, and a wait bound that expires runs one, which ends its
// request's context.
//
// # Errors
//
// The package imports neither the SDK's root package nor internal/codec (the
// gotip canary, which cannot build internal/codec, builds and tests this
// package), so it returns its own values: [ErrNotNegotiated] and
// [*DialError]. The root package maps them to its exported error types.
package h2gate
