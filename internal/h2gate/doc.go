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
// HTTP/1.1 connection ([HTTPAuto]) gives it back at GotConn, so under
// HTTPAuto new HTTP/1.1 connections are dialled one at a time. The stock
// transport's own replays (GOAWAY, REFUSED_STREAM) write their headers
// without the token (risk K21b); when a replay opens a new connection, the
// first request that takes the token on it holds as the first request on a
// new connection would (K21c, R69). Until the client has read a
// connection's SETTINGS it assumes 100 streams, so without the hold the
// callers queued behind a GOAWAY could exceed the server's limit on the
// re-dialed connection and be refused into the stock retry backoff. The
// cost is one response time on each re-dial after a GOAWAY, as K22 is on a
// cold burst. The replay's own response clears a mark no holder has taken.
// The mark is set in the replay's GotConn hook, after the stock transport
// has already recorded the connection as used (its Reused flag is a
// compare-and-swap before the hook, internal/http2/transport.go:423-424),
// so a holder whose GotConn falls between the two passes unheld; the
// window is a few instructions wide and cannot be closed from outside
// net/http.
//
// # Caller hooks that panic
//
// A caller's httptrace hook, Proxy func or GetBody that panics inside
// RoundTrip reaches the caller unchanged; the transport gives the token
// back on the way out and resolves a leader's generation as a leader that
// left. The stock transport is not panic-safe for a hook it calls after it
// reserved a stream (K28, K28b): on a warm HTTP/2 connection it calls
// GetConn with its pool mutex held (internal/http2/client_conn_pool.go:
// 52-61), which a panic leaves locked, and GotConn after ReserveNewRequest,
// whose reservation only cc.RoundTrip releases
// (internal/http2/transport.go:423-425), so a panic there leaks a stream
// slot. No wrapper repairs either; the root package wraps every caller hook,
// from its WithClientTrace option and from a trace on the call's context
// (K28c), recovers inside it and panics again on the calling goroutine once
// RoundTrip has returned.
//
// # Caller hooks that block
//
// A caller's hooks run while their request holds the header-write token:
// every hook from GetConn to WroteHeaders (a new connection's DNS, connect
// and TLS hooks included), and, under FirstHold, every hook until the
// response headers. A hook that blocks holds the token, and every other
// request on the transport waits for it in send (the FirstHold bound is
// armed at WroteHeaders and does not reach a hook that blocks before it).
// That wait is bounded by the hold bound, ConnectTimeout plus the TLS
// handshake timeout (20 s by default), whatever the caller's deadline, the
// root package's WithNoTimeout included: a request that has waited that
// long goes out without the token, counted in Stats.TokenExpiries (risk
// K28d, ruling R85; verifier finding F-2). What it meets next depends on
// where the hook blocks:
//
//   - In GotConn or later, until the response headers: nothing; it goes out
//     on the same connection under HTTP2Only, so the others wait at most
//     the hold bound.
//   - In GetConn on a cold transport: the gate, so the others wait at most
//     the gate's wait bound (waitBound: the dial and the handshake, and the
//     CONNECT exchange and the proxy's handshake when a proxy may apply)
//     plus the hold bound.
//   - In GetConn on a warm transport: net/http's connection pool mutex,
//     which it holds while it calls GetConn on a warm HTTP/2 connection
//     (K28, above). A request that leaves send at the hold bound then blocks
//     on that mutex, which neither a bound nor its own context can end, so
//     the others wait until the hook returns, past their deadlines. Only
//     calling the hook where net/http does not call it could bound this: on
//     a goroutine of its own for every call that has the hook, running on
//     past the bound beside the request's later hooks. The transport does
//     neither; the case is the one hooks must avoid.
//   - In a new connection's DNS, connect or TLS hooks (DNSStart, DNSDone,
//     ConnectStart, ConnectDone, TLSHandshakeStart, TLSHandshakeDone): the
//     connection permit, the only one MaxConnsPerHost 1 allows, which the
//     dial holds while the hook runs, and net/http's per-host wait for it,
//     which only a request's own context ends. net/http's TLS handshake
//     timeout closes the connection but then waits for the hook.
//
// Two bounds end the waits of the last case, the root package's
// WithNoTimeout included (K28d, owner decision G11 (5)):
//
//   - The dial bound: NewTransport's dialer, inside which the DNS and
//     connect hooks run, runs in a goroutine of its own, and the transport
//     waits for it at most ConnectTimeout plus a grace of 100 ms. A dialer
//     that honours its context answers within the grace with its own
//     error, such as the i/o timeout of a host that does not answer, which
//     the transport returns as it is. A dial that has not returned by then
//     fails with a timeout, counted in Stats.DialExpiries, which frees the
//     permit for the next dial. The goroutine is abandoned, not stopped:
//     the dial's context ended at the connect timeout, so the dialer
//     returns as soon as the hook does, and a connection it returns then is
//     closed. Wrap keeps a caller's dialer as it is.
//   - The wait bound: a request whose trace has one of these hooks, one that
//     goes out without the token, and every request while the transport is
//     stalled waits for its connection, from GetConn to GotConn, at most
//     waitBound. send gives it a context that the bound ends with a
//     timeout, counted in Stats.WaitExpiries; the expiry marks the
//     transport stalled until the next GotConn, since a dial may still hold
//     the permit, and the response's body ends the context when it is
//     closed. A hook that blocks a TLS handshake, or a dial that Wrap
//     keeps, holds the permit until it returns: until then no connection is
//     made, and every request fails at its own wait bound.
//
// A hook either bound leaves running goes on with its dial: once it
// returns, the rest of that dial's hooks run, beside the hooks of the dials
// that come after it, and its request has returned by then, so the root
// package's shield logs a panic the hook raises rather than raising it on a
// call.
//
// The bounds fire only after a stall, the hold bound once a FirstHold has
// kept the token its longest and the dial and wait bounds once a dial has
// run past what a healthy one takes, so the measured AC-P4 clauses do not
// move; a request with neither a dial-phase hook nor a stall to wait
// behind pays for neither. A free token is taken in send at once, without
// a timer; only a request that finds it held enters waitToken, which arms
// the timer and counts the entry in tokenWaits, a diagnostic counter of the
// Transport (an atomic touched on that slow path only, read by the
// fast-path test and never by send; ruling D-W6.1-minor2). Requests that go
// out without the token may exceed a server's stream limit, and the stock
// transport retries a stream the server refuses. Hooks must return
// promptly: the shield above covers panics and ordering, and the bounds a
// hook that blocks, except GetConn on a warm transport.
//
// # Logging
//
// The transport logs its events to [Config.Logger] (section 6.3
// observability). The DEBUG events "h2: gate error" and "h2: redial error"
// print the error that failed the dial, whose text a caller's dialer, a
// proxy or net/http wrote and which may repeat a credential of the request;
// they print it through [Config.ErrorText], into which the root package
// passes its credential scrub (ruling R84). The error is rendered only for
// an event the logger keeps.
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
// The package imports neither the SDK's root package nor internal/codec (port
// plan PM1), so it returns its own values: [ErrNotNegotiated] and
// [*DialError]. The root package maps them to its exported error types.
package h2gate
