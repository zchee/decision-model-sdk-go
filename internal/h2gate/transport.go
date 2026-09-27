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

package h2gate

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

// state is the gate state: cold → dialing → warm.
type state int

const (
	stateCold state = iota
	stateDialing
	stateWarm
)

// outcome is how a leader's generation ended, as its waiters see it.
type outcome int

const (
	// pending: the leader has not reached GotConn.
	pending outcome = iota
	// released: the leader reached GotConn; the waiters send.
	released
	// failed: the dial failed while the leader's context was alive; the
	// waiters return a fresh copy of cause.
	failed
	// leaderGone: the leader left without a verdict; the waiters race to
	// lead the next generation.
	leaderGone
)

// generation is one leader's dial as its waiters see it. Its fields are
// guarded by Transport.mu; outcome and cause do not change once done is
// closed, and waiters stays under mu (a waiter that leaves decrements it
// whenever it leaves).
type generation struct {
	done    chan struct{}
	outcome outcome
	cause   *DialError // when failed
	waiters int        // parked on done now
}

// Stats counts the transport's events since it was built.
type Stats struct {
	// Dials counts connections handed to a request for the first time
	// (httptrace.GotConnInfo.Reused false), stock re-dials and replays
	// included.
	Dials uint64
	// Leaders counts cold dials led, handovers included.
	Leaders uint64
	// Releases counts leaders that reached GotConn (at most one per
	// transport: the gate is warm after it).
	Releases uint64
	// Failures counts leaders whose dial failed and failed their waiters.
	Failures uint64
	// Handovers counts leaders that left with waiters parked.
	Handovers uint64
	// ColdResets counts returns to the cold state.
	ColdResets uint64
	// FallThroughs counts waiters whose wait bound expired.
	FallThroughs uint64
	// FirstHolds counts first requests on a new HTTP/2 connection that kept
	// the token until their response headers, SettleHolds included.
	FirstHolds uint64
	// SettleHolds counts FirstHolds by the first token holder on a
	// connection that a stock replay opened without the token.
	SettleHolds uint64
	// HoldExpiries counts FirstHolds that the hold bound ended.
	HoldExpiries uint64
	// TokenExpiries counts requests that waited the hold bound for the
	// header-write token and went out without it.
	TokenExpiries uint64
	// DialExpiries counts dials of NewTransport's own dialer that the dial
	// bound failed and abandoned (see the package doc). A dial that ends at
	// the connect timeout with the dialer's own error, as one to a host that
	// does not answer does, is not counted.
	DialExpiries uint64
	// WaitExpiries counts requests whose wait for a connection the wait
	// bound ended.
	WaitExpiries uint64
}

// settings is what a Transport needs besides the stock transport.
type settings struct {
	mode  Mode
	scope alpnScope
	// waitBound bounds a waiter at the gate; holdBound bounds a FirstHold.
	waitBound, holdBound time.Duration
	log                  *slog.Logger
	// errorText renders the error of a DEBUG event (Config.ErrorText).
	errorText func(*http.Request, error) string
}

// Transport is the SDK's http.RoundTripper: the cold-start gate and the
// header-write token in front of a stock *http.Transport. It is safe for
// concurrent use.
type Transport struct {
	settings
	base *http.Transport
	h2c  bool // every connection is HTTP/2 with prior knowledge

	// token is the header-write token: a request sends into it before
	// RoundTrip and receives from it to give it back.
	token chan struct{}
	// firstHold engages FirstHold; tests clear it for the plain-token
	// negative control of the fan-out record test.
	firstHold bool

	warm     atomic.Bool // the gate is warm for good
	mu       sync.Mutex
	state    state
	gen      *generation
	handover bool // the generation's leader left; the next caller leads it

	parked atomic.Int64 // waiters parked now, for tests

	// stalled is set when a request's wait for a connection ended at the
	// wait bound, and cleared at the next GotConn; while it is set, every
	// request waits for its connection under the bound (see the package
	// doc).
	stalled atomic.Bool

	// unsettled holds the HTTP/2 connections a stock replay opened after
	// giving the token back, oldest first, at most maxUnsettled;
	// nUnsettled is its length, read without the lock.
	settleMu   sync.Mutex
	unsettled  []net.Conn
	nUnsettled atomic.Int64

	dials, leaders, releases, failures, handovers, coldResets atomic.Uint64
	fallThroughs, firstHolds, holdExpiries, settleHolds       atomic.Uint64
	tokenExpiries, dialExpiries, waitExpiries                 atomic.Uint64

	tokenWaits atomic.Uint64 // waitToken entries, for tests: a free token enters none
}

// maxUnsettled bounds the connections remembered as unsettled. A mark is
// taken by the next token holder on its connection; one whose connection
// died first would otherwise stay, so the oldest mark goes when a new one
// does not fit. Under HTTP2Only one connection per host is live at a time.
const maxUnsettled = 8

var _ http.RoundTripper = (*Transport)(nil)

// newTransport wraps base, which the Transport owns from now on.
func newTransport(base *http.Transport, s settings) *Transport {
	t := &Transport{settings: s, base: base, token: make(chan struct{}, 1), firstHold: true}
	if p := base.Protocols; p != nil && p.UnencryptedHTTP2() && !p.HTTP1() && !p.HTTP2() {
		t.h2c = true
	}
	if t.log == nil {
		t.log = discardLogger
	}
	if t.errorText == nil {
		t.errorText = errorString
	}
	return t
}

// discardLogger is the logger of a Transport built without one: it keeps no
// event. One value serves every such Transport.
var discardLogger = slog.New(slog.DiscardHandler)

// errorString is the default Config.ErrorText: the error's own text.
func errorString(_ *http.Request, err error) string { return err.Error() }

// Stats returns a snapshot of the counters.
func (t *Transport) Stats() Stats {
	return Stats{
		Dials:         t.dials.Load(),
		Leaders:       t.leaders.Load(),
		Releases:      t.releases.Load(),
		Failures:      t.failures.Load(),
		Handovers:     t.handovers.Load(),
		ColdResets:    t.coldResets.Load(),
		FallThroughs:  t.fallThroughs.Load(),
		FirstHolds:    t.firstHolds.Load(),
		SettleHolds:   t.settleHolds.Load(),
		HoldExpiries:  t.holdExpiries.Load(),
		TokenExpiries: t.tokenExpiries.Load(),
		DialExpiries:  t.dialExpiries.Load(),
		WaitExpiries:  t.waitExpiries.Load(),
	}
}

// Parked returns the number of requests waiting at the gate now. It is for
// a test outside the package that lets a cold burst's leader's dial end only
// after the burst's other requests have parked.
func (t *Transport) Parked() int { return int(t.parked.Load()) }

// CloseIdleConnections closes the stock transport's idle connections and
// cancels its pending dials.
func (t *Transport) CloseIdleConnections() { t.base.CloseIdleConnections() }

// RoundTrip implements http.RoundTripper. A request that fails before the
// transport handed it a connection, with its context alive, returns a
// [*DialError]; under HTTP2Only a response that is not HTTP/2 is closed and
// refused with [ErrNotNegotiated].
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.warm.Load() {
		return t.send(req, nil)
	}
	var bound *time.Timer
	defer func() {
		if bound != nil {
			bound.Stop()
		}
	}()
	for {
		t.mu.Lock()
		switch {
		case t.state == stateWarm:
			t.mu.Unlock()
			return t.send(req, nil)
		case t.state == stateCold || t.handover:
			if t.state == stateCold {
				t.gen = &generation{done: make(chan struct{})}
			}
			t.state = stateDialing
			t.handover = false
			gen := t.gen
			t.mu.Unlock()
			t.leaders.Add(1)
			return t.lead(req, gen)
		}
		gen := t.gen
		gen.waiters++
		t.mu.Unlock()
		t.parked.Add(1)
		if bound == nil {
			bound = time.NewTimer(t.waitBound)
		}
		select {
		case <-gen.done:
			t.parked.Add(-1)
			switch gen.outcome {
			case released:
				return t.send(req, nil)
			case failed:
				closeBody(req)
				return nil, gen.cause.clone()
			default: // leaderGone: the first waiter to lock leads next
				continue
			}
		case <-req.Context().Done():
			t.leave(gen)
			closeBody(req)
			return nil, context.Cause(req.Context())
		case <-bound.C:
			t.leave(gen)
			t.fallThroughs.Add(1)
			return t.send(req, nil)
		}
	}
}

// leave unparks a waiter that stops waiting on gen for its own reason.
func (t *Transport) leave(gen *generation) {
	t.parked.Add(-1)
	t.mu.Lock()
	gen.waiters--
	t.mu.Unlock()
}

// release makes the gate warm and releases gen's waiters, once.
func (t *Transport) release(ctx context.Context, gen *generation) {
	t.mu.Lock()
	if gen.outcome != pending {
		t.mu.Unlock()
		return
	}
	gen.outcome = released
	waiters := gen.waiters
	t.state = stateWarm
	t.warm.Store(true)
	close(gen.done)
	t.mu.Unlock()
	t.releases.Add(1)
	t.log.DebugContext(ctx, "h2: gate release", "waiters", waiters)
}

// lead sends the leader's request and resolves gen when it ends before
// GotConn.
func (t *Transport) lead(req *http.Request, gen *generation) (*http.Response, error) {
	ctx := req.Context()
	// A panic unwinding through the stock RoundTrip (a caller's trace hook
	// or Proxy func, GetBody on a retry) leaves gen pending: it is resolved
	// as a leader that left, and the panic goes on to the caller, since
	// nothing here recovers it. Every other return has resolved gen.
	defer func() {
		t.mu.Lock()
		if gen.outcome != pending {
			t.mu.Unlock()
			return
		}
		waiters := t.abandonLocked(gen)
		t.mu.Unlock()
		t.log.DebugContext(ctx, "h2: gate error", "reason", "leader-gone", "waiters", waiters)
	}()
	resp, err := t.send(req, gen)
	if err == nil {
		// A response follows GotConn, so this is a no-op; it keeps a
		// generation from staying pending should the stock transport ever
		// answer without the hook.
		t.release(ctx, gen)
		return resp, nil
	}
	t.mu.Lock()
	if gen.outcome != pending {
		// Failed after GotConn: per request; the gate stays warm.
		t.mu.Unlock()
		return nil, err
	}
	de, isDial := err.(*DialError) //nolint:errorlint // send returns the *DialError itself
	if !isDial || ctx.Err() != nil {
		// The leader's context ended, or its request failed before the
		// transport looked for a connection: no verdict on the dial. The
		// stock dial is detached from the request (transport.go:1596), so a
		// dial in flight usually completes for the next leader.
		waiters := t.abandonLocked(gen)
		t.mu.Unlock()
		t.log.DebugContext(ctx, "h2: gate error", "reason", "leader-gone", "waiters", waiters)
		return nil, err
	}
	gen.outcome = failed
	gen.cause = de
	waiters := gen.waiters
	t.state = stateCold
	close(gen.done)
	t.mu.Unlock()
	t.failures.Add(1)
	t.coldResets.Add(1)
	if t.log.Enabled(ctx, slog.LevelDebug) {
		t.log.DebugContext(ctx, "h2: gate error", "reason", reason(de), "waiters", waiters, "error", t.errorText(req, de.Err))
	}
	return nil, de.clone()
}

// abandonLocked resolves gen as a leader that left without a verdict on the
// dial: the first of its waiters to run leads a new generation, or, with
// none, the gate turns cold and the next caller leads. It returns the
// number of waiters. t.mu must be held and gen must be pending.
func (t *Transport) abandonLocked(gen *generation) int {
	gen.outcome = leaderGone
	waiters := gen.waiters
	if waiters == 0 {
		t.state = stateCold
		t.coldResets.Add(1)
	} else {
		t.handover = true
		t.handovers.Add(1)
		t.gen = &generation{done: make(chan struct{})}
	}
	close(gen.done)
	return waiters
}

// reason names a DialError's class for the log.
func reason(d *DialError) string {
	switch {
	case d.Proxy:
		return "proxy"
	case d.notNegotiated:
		return "not-negotiated"
	case d.Timeout:
		return "timeout"
	default:
		return "dial"
	}
}

// closeBody closes the body of a request that will not reach the stock
// transport, as the RoundTripper contract requires.
func closeBody(req *http.Request) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
}

// send takes the token and runs req through the stock transport. gen is the
// generation req leads, or nil.
func (t *Transport) send(req *http.Request, gen *generation) (*http.Response, error) {
	ctx := req.Context()
	held := true
	select {
	case t.token <- struct{}{}:
	default:
		var err error
		if held, err = t.waitToken(ctx); err != nil {
			closeBody(req)
			return nil, err
		}
	}
	c := &call{t: t, gen: gen}
	if !held {
		c.given.Store(true) // out without the token: nothing to give back
	}
	// The token goes back on every exit, a panic unwinding through the stock
	// RoundTrip included.
	defer c.finish()
	rctx := ctx
	if !held || t.stalled.Load() || dialHooks(httptrace.ContextClientTrace(ctx)) {
		// The wait bound (see the package doc). The body of the response
		// ends the derived context, so it does not outlive the call among
		// its parent's children; every other exit ends it in release.
		var cancel context.CancelCauseFunc
		rctx, cancel = context.WithCancelCause(ctx)
		c.bound = &waitState{cancel: cancel}
		defer c.release()
	}
	c.trace = httptrace.ClientTrace{GetConn: c.getConn, GotConn: c.gotConn, WroteHeaders: c.wroteHeaders}
	c.Context = httptrace.WithClientTrace(rctx, &c.trace)
	resp, err := t.base.RoundTrip(req.WithContext(c))
	c.finish()
	if err == nil {
		c.responded()
	}
	if err != nil {
		if c.seen.Load()&(callLookedUp|callConnected) == callLookedUp && ctx.Err() == nil {
			de := classify(err)
			if gen == nil && t.warm.Load() && t.log.Enabled(ctx, slog.LevelDebug) {
				// After warm, re-dials are the stock pool's, serial and ungated.
				t.log.DebugContext(ctx, "h2: redial error", "reason", reason(de), "error", t.errorText(req, err))
			}
			return nil, de
		}
		return nil, err
	}
	if t.mode == HTTP2Only && resp.ProtoMajor != 2 {
		// Reached only where no handshake check could apply (an IP-literal
		// API host or a ServerName override behind a proxy): the request was
		// sent.
		_ = resp.Body.Close()
		t.log.WarnContext(ctx, "h2: response not HTTP/2", "proto", resp.Proto)
		return nil, &NotNegotiatedError{Detail: "the response is " + resp.Proto}
	}
	if b := c.bound; b != nil {
		resp.Body = &boundBody{ReadCloser: resp.Body, cancel: b.cancel}
		b.kept = true
	}
	return resp, nil
}

// dialHooks reports whether trace has a hook that runs in a new
// connection's dial phase (DNS, connect or TLS handshake), where a hook
// that blocks holds the connection permit (MaxConnsPerHost 1) and, under
// the stock transport, the request's wait for it.
func dialHooks(trace *httptrace.ClientTrace) bool {
	return trace != nil && (trace.DNSStart != nil || trace.DNSDone != nil || trace.ConnectStart != nil ||
		trace.ConnectDone != nil || trace.TLSHandshakeStart != nil || trace.TLSHandshakeDone != nil)
}

// boundBody is the body of a response whose request waited for its
// connection under the wait bound: closing it ends the context the
// transport derived for the bound, which the response's stream needed
// until then.
type boundBody struct {
	io.ReadCloser
	cancel context.CancelCauseFunc
}

// Close closes the body and ends the request's context.
func (b *boundBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel(nil)
	return err
}

// waitToken waits for the header-write token when another request holds
// it, at most the hold bound, and reports whether the caller now holds it,
// or the context's cause when the context ended first. A holder keeps the
// token until its HEADERS are written, or under FirstHold until its response
// headers or the hold bound, unless a caller's trace hook blocks before
// either: so a waiter that has waited the hold bound goes out without the
// token, counted in TokenExpiries, as a waiter at the gate falls through
// when its bound ends. Only a waiter pays for the timer; a free token is
// taken in send without one.
func (t *Transport) waitToken(ctx context.Context) (bool, error) {
	t.tokenWaits.Add(1)
	tm := time.NewTimer(t.holdBound)
	defer tm.Stop()
	select {
	case t.token <- struct{}{}:
		return true, nil
	case <-ctx.Done():
		return false, context.Cause(ctx)
	case <-tm.C:
		t.tokenExpiries.Add(1)
		t.log.DebugContext(ctx, "h2: token wait expired", "bound", t.holdBound)
		return false, nil
	}
}

// call is one request's passage through the stock transport, as its
// httptrace hooks see it. The hooks run on the transport's goroutines.
//
// A call is also the context the stock transport sees for the request, and
// the context of the request its response carries: the request's own with
// the hooks' trace, which answers callKey with the call, so that the Proxy
// func NewTransport installs (observeProxy) can mark the request it chose a
// proxy for, and [Proxied] can read the mark from the response. It costs no
// context of its own: the call is allocated anyway.
type call struct {
	context.Context // the request's context with trace installed; see Value

	t     *Transport
	gen   *generation // the generation this request leads, or nil
	trace httptrace.ClientTrace

	given atomic.Bool   // the token was given back
	seen  atomic.Uint32 // callLookedUp, callProxied, callConnected and callFirst
	hold  atomic.Pointer[time.Timer]
	// marked is the connection this request, a stock replay, marked
	// unsettled; its own response clears the mark (responded).
	marked atomic.Pointer[net.Conn]
	// bound is the wait bound's state when send gave the request a context
	// the bound can end, nil otherwise. It is set before RoundTrip and never
	// changes. Held behind a pointer, it keeps the call small for a request
	// without the bound (TestCallSize).
	bound *waitState
}

// waitState is the wait bound of one request: cancel ends the context send
// derived for it, wait is armed at each GetConn and stopped at GotConn,
// waiting is set in between, and kept records that the response's body took
// cancel over (kept is the calling goroutine's alone).
type waitState struct {
	cancel  context.CancelCauseFunc
	wait    atomic.Pointer[time.Timer]
	waiting atomic.Bool
	kept    bool
}

// The bits of call.seen, set once and never cleared. Or returns the word as
// it was, so c.seen.Or(bit)&bit == 0 reports that this call set the bit.
const (
	// callLookedUp: the transport looked for a connection (GetConn).
	callLookedUp uint32 = 1 << iota
	// callProxied: the Proxy func returned a proxy for the request.
	callProxied
	// callConnected: the transport handed over a connection (GotConn).
	callConnected
	// callFirst: FirstHold engaged.
	callFirst
)

// callKey is the context key under which a call answers for itself.
type callKey struct{}

// Value returns c for callKey and the request context's value for every
// other key.
func (c *call) Value(key any) any {
	if key == (callKey{}) {
		return c
	}
	return c.Context.Value(key)
}

// Proxied reports whether resp answers a request for which the Proxy func of
// a transport [NewTransport] built returned a proxy, when the stock
// transport last looked for a connection for it. A request sent on an HTTP/2
// connection the stock transport held asks the func nothing
// (GOROOT/src/net/http/transport.go:643-646), so its response reports false,
// as does one from a transport [Wrap] built, or with no request.
func Proxied(resp *http.Response) bool {
	if resp == nil || resp.Request == nil {
		return false
	}
	c, ok := resp.Request.Context().Value(callKey{}).(*call)
	return ok && c.seen.Load()&callProxied != 0
}

// finish ends the call's hold on the token: it gives the token back, if it
// has not gone back already, and stops the hold bound's timer and the wait
// bound's.
func (c *call) finish() {
	c.giveBack(false)
	if tm := c.hold.Load(); tm != nil {
		tm.Stop()
	}
	if b := c.bound; b != nil {
		if tm := b.wait.Load(); tm != nil {
			b.waiting.Store(false)
			tm.Stop()
		}
	}
}

// release ends the context send derived for the wait bound, unless the
// response's body took it over.
func (c *call) release() {
	if !c.bound.kept {
		c.bound.cancel(nil)
	}
}

// expire is the wait bound's callback: a request still waiting for its
// connection when the bound ends marks the transport stalled and ends its
// context, so the stock transport returns the bound's error; a request
// that got its connection meanwhile is left alone.
func (c *call) expire() {
	if !c.bound.waiting.CompareAndSwap(true, false) {
		return
	}
	t := c.t
	// Before the cancel: the token this request gives back on its way out
	// reaches the next holder after the mark.
	t.stalled.Store(true)
	t.waitExpiries.Add(1)
	t.log.DebugContext(c, "h2: connection wait expired", "bound", t.waitBound)
	c.bound.cancel(&boundError{what: "the wait for a connection", bound: t.waitBound})
}

// responded clears the unsettled mark this request set, if a holder has not
// taken it: the replay's response came over the connection, so the client
// has read the connection's SETTINGS and a later holder need not wait. A
// mark left to a later holder would make it pay a hold for nothing, and one
// on a dead connection would keep the connection referenced and the marks'
// fast path off.
func (c *call) responded() {
	if conn := c.marked.Load(); conn != nil {
		c.t.takeUnsettled(*conn)
	}
}

// giveBack returns the token, once; expired reports a FirstHold bound. The
// expiry is counted before the token goes back: a receive from the buffered
// token is synchronized before the send it makes room for completes, so the
// request that takes the token next sees the count in Stats.
func (c *call) giveBack(expired bool) {
	if !c.given.CompareAndSwap(false, true) {
		return
	}
	if expired {
		c.t.holdExpiries.Add(1)
	}
	<-c.t.token
}

// getConn is the httptrace GetConn hook. Under the wait bound it arms the
// bound, again for a stock retry that looks for a connection once more.
func (c *call) getConn(string) {
	c.seen.Or(callLookedUp)
	b := c.bound
	if b == nil {
		return
	}
	b.waiting.Store(true)
	if tm := b.wait.Load(); tm != nil {
		tm.Reset(c.t.waitBound)
		return
	}
	b.wait.Store(time.AfterFunc(c.t.waitBound, c.expire))
}

// gotConn is the httptrace GotConn hook: it counts a new connection, gives
// the token back at once on an HTTP/1.1 connection, engages FirstHold on a
// new HTTP/2 one or on one a replay left unsettled, and releases the gate's
// waiters when this request leads.
//
// A request that reaches GotConn after it gave the token back is a stock
// replay (GOAWAY, REFUSED_STREAM: the transport retries inside RoundTrip,
// internal/http2/transport.go:417-446). On a new connection it cannot hold,
// and until the client reads that connection's SETTINGS it assumes 100
// streams (:57, :624), so it marks the connection unsettled for the next
// token holder there (see the package doc).
func (c *call) gotConn(info httptrace.GotConnInfo) {
	t := c.t
	if b := c.bound; b != nil && b.waiting.CompareAndSwap(true, false) {
		if tm := b.wait.Load(); tm != nil {
			tm.Stop()
		}
	}
	if t.stalled.Load() {
		// A connection was handed over: no dial holds the permit now.
		t.stalled.Store(false)
	}
	h2 := t.isH2(info.Conn)
	if !info.Reused {
		t.dials.Add(1)
		t.log.DebugContext(c, "h2: dial", "h2", h2)
	}
	switch {
	case !h2:
		c.giveBack(false)
	case c.given.Load():
		if !info.Reused && t.markUnsettled(info.Conn) {
			conn := info.Conn
			c.marked.Store(&conn)
		}
	case !t.firstHold:
	case !info.Reused:
		// A request that still holds the token and is replayed onto a
		// second new connection holds on, but counts once.
		if c.seen.Or(callFirst)&callFirst == 0 {
			t.firstHolds.Add(1)
		}
	case t.takeUnsettled(info.Conn):
		if c.seen.Or(callFirst)&callFirst == 0 {
			t.firstHolds.Add(1)
		}
		t.settleHolds.Add(1)
	}
	c.seen.Or(callConnected)
	if c.gen != nil {
		t.release(c, c.gen)
	}
}

// wroteHeaders is the httptrace WroteHeaders hook: it gives the token back,
// or, under FirstHold, arms the hold bound.
func (c *call) wroteHeaders() {
	if c.seen.Load()&callFirst == 0 {
		c.giveBack(false)
		return
	}
	if c.given.Load() {
		return
	}
	// A stock retry writes the HEADERS again: its bound replaces the
	// earlier one, which is stopped rather than left to fire as a no-op.
	tm := time.AfterFunc(c.t.holdBound, func() { c.giveBack(true) })
	if old := c.hold.Swap(tm); old != nil {
		old.Stop()
	}
	// WroteHeaders can run on the transport's write goroutine after send
	// returned and gave the token back; the timer then has nothing to end.
	if c.given.Load() {
		tm.Stop()
	}
}

// markUnsettled remembers conn as a connection a replay opened without the
// token, and reports whether it did. A connection whose type is not
// comparable cannot be looked up and is not remembered (the stock types
// and their wrappers are pointers).
func (t *Transport) markUnsettled(conn net.Conn) bool {
	if conn == nil || !reflect.ValueOf(conn).Comparable() {
		return false
	}
	t.settleMu.Lock()
	defer t.settleMu.Unlock()
	if len(t.unsettled) == maxUnsettled {
		t.unsettled = append(t.unsettled[:0], t.unsettled[1:]...)
	}
	t.unsettled = append(t.unsettled, conn)
	t.nUnsettled.Store(int64(len(t.unsettled)))
	return true
}

// takeUnsettled reports whether conn was marked unsettled, and clears the
// mark. Only comparable values are stored, so the comparison cannot panic.
func (t *Transport) takeUnsettled(conn net.Conn) bool {
	if t.nUnsettled.Load() == 0 {
		return false
	}
	t.settleMu.Lock()
	defer t.settleMu.Unlock()
	for i, u := range t.unsettled {
		if u == conn {
			t.unsettled = append(t.unsettled[:i], t.unsettled[i+1:]...)
			t.nUnsettled.Store(int64(len(t.unsettled)))
			return true
		}
	}
	return false
}

// isH2 reports whether conn, from httptrace.GotConnInfo, carries HTTP/2: its
// TLS state negotiated h2 (a caller dialer's wrapper included), or the
// transport speaks HTTP/2 with prior knowledge only.
func (t *Transport) isH2(conn net.Conn) bool {
	if t.h2c {
		return true
	}
	if cs, ok := conn.(connectionStater); ok {
		return cs.ConnectionState().NegotiatedProtocol == "h2"
	}
	return false
}
