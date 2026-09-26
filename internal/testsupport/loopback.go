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
	"context"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ALPN selects what a [LoopbackServer] offers during the TLS handshake.
type ALPN int

const (
	// ALPNH2 offers h2 and http/1.1: a client that offers h2 speaks HTTP/2
	// through the server's own frame writer, one that offers only http/1.1
	// speaks HTTP/1.1.
	ALPNH2 ALPN = iota
	// ALPNNone offers nothing: every handshake completes with no protocol
	// negotiated, and the connection speaks HTTP/1.1.
	ALPNNone
	// ALPNHTTP1Only offers http/1.1 alone: a client that offers only h2 is
	// refused with TLS alert 120 (no_application_protocol), and one that also
	// offers http/1.1 speaks HTTP/1.1.
	ALPNHTTP1Only
)

// nextProtos returns the server's ALPN list for the mode.
func (a ALPN) nextProtos() []string {
	switch a {
	case ALPNNone:
		return nil
	case ALPNHTTP1Only:
		return []string{"http/1.1"}
	default:
		return []string{"h2", "http/1.1"}
	}
}

// Action is what a [LoopbackServer] does with an HTTP/2 stream once its
// request headers have arrived. [ServerConfig.OnStream] chooses it.
type Action int

const (
	// ActionServe hands the stream to the server's handler.
	ActionServe Action = iota
	// ActionRefuse resets the stream with REFUSED_STREAM: the request was not
	// processed, so the client may retry it.
	ActionRefuse
	// ActionGoAway sends GOAWAY with a LastStreamID below this stream (its ID
	// minus 2), so this stream and any later one are unprocessed; streams
	// already being served finish, then the connection closes gracefully
	// ([H2Conn.GoAway]).
	ActionGoAway
	// ActionClose closes the connection without GOAWAY, gracefully, as
	// [LoopbackServer.CloseConns] does: TLS close_notify, then TCP FIN, so
	// the client reads io.EOF, and the socket closes once the client has
	// closed its side. [H2Conn.Close] is the immediate close.
	ActionClose
	// ActionHold never answers: the stream stays open until the client resets
	// it or the connection closes. It counts against MAX_CONCURRENT_STREAMS.
	ActionHold
	// ActionReset ends the connection with a TCP reset ([H2Conn.Reset]):
	// no GOAWAY and no close_notify, so the client's read fails with
	// ECONNRESET.
	ActionReset
)

// String returns the action's name.
func (a Action) String() string {
	switch a {
	case ActionServe:
		return "serve"
	case ActionRefuse:
		return "refuse"
	case ActionGoAway:
		return "goaway"
	case ActionClose:
		return "close"
	case ActionHold:
		return "hold"
	case ActionReset:
		return "reset"
	default:
		return "action(" + strconv.Itoa(int(a)) + ")"
	}
}

// ErrCode is an HTTP/2 error code (RFC 9113 section 7), as sent in GOAWAY and
// RST_STREAM frames. It is this package's own type so a caller need not import
// golang.org/x/net. [H2Conn.GoAway] takes any code: ErrCode(n) for one not
// named here.
type ErrCode uint32

// The HTTP/2 error codes the server's own actions send. Besides these, the
// server answers a frame that the framer rejects as a stream error with
// RST_STREAM carrying the framer's code (for example PROTOCOL_ERROR).
const (
	// CodeNoError is the code of ActionGoAway's GOAWAY and of the RST_STREAM
	// that stops an upload the handler answered without reading.
	CodeNoError ErrCode = 0x0
	// CodeInternalError is the code of the RST_STREAM after a handler panics.
	CodeInternalError ErrCode = 0x2
	// CodeRefusedStream is the code of the RST_STREAM of ActionRefuse and of
	// a stream over MaxConcurrentStreams.
	CodeRefusedStream ErrCode = 0x7
)

// ServerConfig configures a [LoopbackServer].
type ServerConfig struct {
	// ALPN selects the protocols offered in the TLS handshake.
	ALPN ALPN
	// MaxConcurrentStreams, when non-zero, is advertised in the server's
	// SETTINGS frame; a stream that would exceed it is reset with
	// REFUSED_STREAM and counted by [LoopbackServer.OverLimit]. It is each
	// HTTP/2 connection's limit until [H2Conn.SetMaxConcurrentStreams]
	// changes it.
	MaxConcurrentStreams uint32
	// Handler serves every request, HTTP/2 and HTTP/1.1. Nil answers 200 with
	// an empty body. Over HTTP/2 the response carries no Content-Length unless
	// the handler sets one, so a test can declare a length that differs from
	// the body or none at all.
	Handler http.Handler
	// OnStream, when set, chooses what happens to each HTTP/2 stream when its
	// request headers arrive; nil serves every stream. It runs on the
	// connection's reader goroutine, so it must not block; it may call the
	// stream's connection methods (GoAway, Close).
	OnStream func(*Stream) Action
}

// Stream is an HTTP/2 request stream as [ServerConfig.OnStream] sees it.
type Stream struct {
	// Conn is the connection the stream arrived on.
	Conn *H2Conn
	// ID is the stream identifier.
	ID uint32
	// Seq is the stream's position among every request the server has seen,
	// from 0.
	Seq int
	// Method, Path and Authority are the :method, :path and :authority
	// pseudo-headers.
	Method, Path, Authority string
	// Header holds the regular header fields.
	Header http.Header
}

// SeenRequest is a request as a [LoopbackServer] recorded it: at the moment
// its headers arrived, whether or not it was then served.
type SeenRequest struct {
	// Seq is the request's position among all requests seen, from 0.
	Seq int
	// Conn is the index of the connection it arrived on (see ConnInfo).
	Conn int
	// StreamID is the HTTP/2 stream identifier; 0 for HTTP/1.1.
	StreamID uint32
	// Proto is "HTTP/2.0" or "HTTP/1.1".
	Proto string
	// Method, Path and Authority are the request's method, request target
	// and authority (Host).
	Method, Path, Authority string
	// Header holds the regular header fields.
	Header http.Header
	// Action is what the server did with it; always ActionServe for
	// HTTP/1.1. A stream refused for exceeding MaxConcurrentStreams shows
	// ActionRefuse.
	Action Action
	// Dropped reports that the stream ended before the server finished its
	// response: GOAWAY dropped it, the client reset it, or its connection
	// closed. Action keeps what the server chose first, so a stream whose
	// handler had started shows ActionServe and Dropped. The flag is decided
	// when the frame that ends the server's side is cleared for writing: a
	// stream whose final frame was cleared but whose write then failed shows
	// neither Dropped nor a completed response. Always false for HTTP/1.1.
	Dropped bool
}

// ConnInfo describes one accepted connection.
type ConnInfo struct {
	// Index is the connection's position in accept order, from 0.
	Index int
	// Protocol is the ALPN protocol negotiated ("h2", "http/1.1" or "").
	Protocol string
	// HandshakeErr is the server side's TLS handshake error, if any (for
	// example the refusal behind alert 120 in ALPNHTTP1Only mode).
	HandshakeErr string
	// GoAwaySeq, CloseWriteSeq, PeerClosedSeq and ClosedSeq record how an
	// HTTP/2 connection ended, as numbers of one sequence the server shares
	// across its connections, so they order events without a clock (K29); 0
	// means the event did not happen. GoAwaySeq is the last GOAWAY frame,
	// taken before it is written; CloseWriteSeq is the server beginning its
	// graceful close ([LoopbackServer.CloseConns], ActionClose, or the end
	// of a connection after GOAWAY once no stream at or below its
	// LastStreamID is left), taken before its close_notify and FIN leave;
	// PeerClosedSeq is the reader reading the end of the client's side;
	// ClosedSeq is the socket's close. 0 < PeerClosedSeq < ClosedSeq shows
	// that the socket closed with nothing the client sent left unread, and
	// CloseWriteSeq < PeerClosedSeq that the server's close came first
	// (rulings K33, K34). The client may close first: net/http closes a
	// connection that GOAWAY ended as soon as its last stream is done, and
	// then PeerClosedSeq precedes CloseWriteSeq, or CloseWriteSeq is 0.
	GoAwaySeq, CloseWriteSeq, PeerClosedSeq, ClosedSeq int64
	// Drained lists the types of the frames the reader read and discarded,
	// answering none, once the graceful close had begun: what the client
	// was still sending when the server's close_notify reached it.
	Drained []string
}

// LoopbackServer is a TLS server on 127.0.0.1 for transport tests. HTTP/2
// connections are served by the package's own frame writer (x/net's
// http2.Framer and hpack), which gives the knobs of [ServerConfig] and
// [H2Conn]; HTTP/1.1 connections (ALPN http/1.1 or none) by net/http.
//
// Clients trust it with [RootCAs] or [ClientTLSConfig]. Close runs from
// tb.Cleanup.
type LoopbackServer struct {
	tb      testing.TB
	cfg     ServerConfig
	cert    certBundle
	tlsConf *tls.Config
	ln      net.Listener
	h1      *http.Server
	h1ln    *chanListener
	wg      sync.WaitGroup

	accepts   atomic.Int64
	overLimit atomic.Int64
	maxActive atomic.Int64
	seq       atomic.Int64 // the sequence of ConnInfo's close records
	hooks     atomic.Pointer[h2Hooks]

	mu       sync.Mutex
	closed   bool
	conns    []ConnInfo
	raw      map[net.Conn]int // every accepted connection still open, by index
	h2       map[*H2Conn]struct{}
	requests []SeenRequest
}

// NewLoopbackServer starts a server on 127.0.0.1 with a free port and
// registers its Close with tb.Cleanup.
func NewLoopbackServer(tb testing.TB, cfg ServerConfig) *LoopbackServer {
	tb.Helper()
	cert := mustCert(tb)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("testsupport: listen: %v", err)
	}
	s := &LoopbackServer{
		tb:      tb,
		cfg:     cfg,
		cert:    cert,
		tlsConf: serverTLSConfig(cert, cfg.ALPN.nextProtos()),
		ln:      ln,
		raw:     make(map[net.Conn]int),
		h2:      make(map[*H2Conn]struct{}),
		h1ln:    newChanListener(ln.Addr()),
	}
	var h1Protocols http.Protocols
	h1Protocols.SetHTTP1(true)
	s.h1 = &http.Server{
		Handler:           http.HandlerFunc(s.serveH1),
		Protocols:         &h1Protocols,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
		ConnState: func(c net.Conn, st http.ConnState) {
			if st == http.StateClosed || st == http.StateHijacked {
				s.untrack(c)
			}
		},
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			s.mu.Lock()
			idx, ok := s.raw[c]
			s.mu.Unlock()
			if !ok {
				idx = -1
			}
			return context.WithValue(ctx, connIndexKey{}, idx)
		},
	}
	s.wg.Go(func() { _ = s.h1.Serve(s.h1ln) })
	s.wg.Go(s.acceptLoop)
	tb.Cleanup(s.Close)
	return s
}

// connIndexKey carries a connection's index in an HTTP/1.1 request context.
type connIndexKey struct{}

// Addr returns the listener's address, 127.0.0.1:port.
func (s *LoopbackServer) Addr() string { return s.ln.Addr().String() }

// URL returns "https://127.0.0.1:port".
func (s *LoopbackServer) URL() string { return "https://" + s.Addr() }

// Accepts returns the number of TCP connections accepted so far.
func (s *LoopbackServer) Accepts() int { return int(s.accepts.Load()) }

// OverLimit returns the number of streams reset because they would have
// exceeded MaxConcurrentStreams.
func (s *LoopbackServer) OverLimit() int { return int(s.overLimit.Load()) }

// MaxActiveStreams returns the largest number of HTTP/2 streams that were
// open at once on any one connection (served or held).
func (s *LoopbackServer) MaxActiveStreams() int { return int(s.maxActive.Load()) }

// Conns returns what is known of every accepted connection, in accept order.
func (s *LoopbackServer) Conns() []ConnInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.conns)
	for i := range out {
		out[i].Drained = slices.Clone(out[i].Drained)
	}
	return out
}

// Requests returns every request seen so far, in arrival order.
func (s *LoopbackServer) Requests() []SeenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.requests)
	for i := range out {
		out[i].Header = out[i].Header.Clone()
	}
	return out
}

// LiveH2Conns returns the HTTP/2 connections still open, in accept order.
func (s *LoopbackServer) LiveH2Conns() []*H2Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*H2Conn, 0, len(s.h2))
	for c := range s.h2 {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b *H2Conn) int { return a.index - b.index })
	return out
}

// CloseConns ends every open connection without GOAWAY; the listener stays
// open. An HTTP/2 connection ends as a server that is done with it should
// (as ActionClose and the end after GOAWAY do too): its streams are
// dropped, TLS close_notify and a TCP FIN follow the frames already
// written, nothing is written after them, and the server keeps reading,
// discarding whatever the client still sends, until the client closes its
// side or [drainBound] passes; only then does it close the socket. It returns once close_notify
// and FIN are sent, without waiting for the client, and
// [ConnInfo.CloseWriteSeq] and the records after it tell how the close
// went. Any other connection (HTTP/1.1, or one still in its handshake) is
// closed at once: close_notify when past the handshake, then FIN.
//
// Closing a socket that holds unread data, or one that data reaches after
// its close, makes the kernel end the connection with a TCP reset instead
// of FIN (RFC 1122 section 4.2.2.13), and a client that has not yet read the
// server's last frames and close_notify when the reset arrives loses them
// on Windows, where its read fails with WSAECONNABORTED (ruling K33). A
// fresh client connection writes after its request (its SETTINGS
// acknowledgement, for one), so the close must not race those writes.
func (s *LoopbackServer) CloseConns() {
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.raw))
	for c := range s.raw {
		conns = append(conns, c)
	}
	h2 := make(map[net.Conn]*H2Conn, len(s.h2))
	for c := range s.h2 {
		h2[c.nc] = c
	}
	s.mu.Unlock()
	for _, c := range conns {
		if hc, ok := h2[c]; ok {
			hc.closeGracefully()
			continue
		}
		_ = c.Close()
	}
}

// closeRaw closes every open connection at once: close_notify on a
// connection past its handshake, then FIN, with no wait for the client.
func (s *LoopbackServer) closeRaw() {
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.raw))
	for c := range s.raw {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// noteConn updates the record of connection idx.
func (s *LoopbackServer) noteConn(idx int, update func(*ConnInfo)) {
	s.mu.Lock()
	update(&s.conns[idx])
	s.mu.Unlock()
}

// Close stops the listener, closes every connection and waits for the
// server's goroutines, including running handlers, which see their request
// context cancelled. It is idempotent.
func (s *LoopbackServer) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	_ = s.ln.Close()
	_ = s.h1ln.Close()
	_ = s.h1.Close()
	s.closeRaw()
	waitGroupTimeout(s.tb, &s.wg, "LoopbackServer", 10*time.Second)
}

// handler returns the configured handler or the default one.
func (s *LoopbackServer) handler() http.Handler {
	if s.cfg.Handler != nil {
		return s.cfg.Handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

// acceptLoop accepts connections until the listener closes.
func (s *LoopbackServer) acceptLoop() {
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.accepts.Add(1)
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = nc.Close()
			return
		}
		idx := len(s.conns)
		s.conns = append(s.conns, ConnInfo{Index: idx})
		s.mu.Unlock()
		s.wg.Go(func() { s.serveConn(nc, idx) })
	}
}

// serveConn runs the TLS handshake and hands the connection to the HTTP/2
// frame server or to net/http's HTTP/1.1 server.
func (s *LoopbackServer) serveConn(nc net.Conn, idx int) {
	tc := tls.Server(nc, s.tlsConf)
	if !s.track(tc, idx) {
		return
	}
	_ = tc.SetDeadline(time.Now().Add(10 * time.Second))
	err := tc.Handshake()
	_ = tc.SetDeadline(time.Time{})
	proto := tc.ConnectionState().NegotiatedProtocol
	s.mu.Lock()
	s.conns[idx].Protocol = proto
	if err != nil {
		s.conns[idx].HandshakeErr = err.Error()
	}
	s.mu.Unlock()
	if err != nil {
		s.untrack(tc)
		_ = tc.Close()
		return
	}
	if proto == "h2" {
		c := newH2Conn(s, tc, idx)
		s.mu.Lock()
		s.h2[c] = struct{}{}
		s.mu.Unlock()
		c.serve()
		s.mu.Lock()
		delete(s.h2, c)
		s.mu.Unlock()
		s.untrack(tc)
		return
	}
	if !s.h1ln.hand(tc) {
		s.untrack(tc)
		_ = tc.Close()
	}
}

// track registers an open connection; it reports false (and closes c) when
// the server is closing.
func (s *LoopbackServer) track(c net.Conn, idx int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		_ = c.Close()
		return false
	}
	s.raw[c] = idx
	return true
}

// untrack forgets a connection.
func (s *LoopbackServer) untrack(c net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.raw, c)
}

// record appends a seen request and returns its sequence number.
func (s *LoopbackServer) record(r SeenRequest) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.Seq = len(s.requests)
	s.requests = append(s.requests, r)
	return r.Seq
}

// setAction updates the action recorded for request seq.
func (s *LoopbackServer) setAction(seq int, a Action) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[seq].Action = a
}

// markDropped records that request seq was dropped. It runs with an H2Conn's
// mu held; that order cannot deadlock because no code acquires an H2Conn's mu
// while it holds s.mu.
func (s *LoopbackServer) markDropped(seq int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[seq].Dropped = true
}

// noteActive raises the high-water mark of open streams.
func (s *LoopbackServer) noteActive(n int) {
	for {
		cur := s.maxActive.Load()
		if int64(n) <= cur || s.maxActive.CompareAndSwap(cur, int64(n)) {
			return
		}
	}
}

// serveH1 records an HTTP/1.1 request and hands it to the handler.
func (s *LoopbackServer) serveH1(w http.ResponseWriter, r *http.Request) {
	idx, _ := r.Context().Value(connIndexKey{}).(int)
	s.record(SeenRequest{
		Conn: idx, Proto: r.Proto, Method: r.Method, Path: r.RequestURI, Authority: r.Host,
		Header: r.Header.Clone(), Action: ActionServe,
	})
	s.handler().ServeHTTP(w, r)
}

// chanListener is a net.Listener fed by hand: the accept loop passes it the
// connections that negotiated HTTP/1.1, and net/http's server accepts them.
type chanListener struct {
	addr   net.Addr
	ch     chan net.Conn
	done   chan struct{}
	closer sync.Once
}

// newChanListener returns a chanListener reporting addr.
func newChanListener(addr net.Addr) *chanListener {
	return &chanListener{addr: addr, ch: make(chan net.Conn), done: make(chan struct{})}
}

// hand passes c to the HTTP/1.1 server; it reports false once the listener
// is closed.
func (l *chanListener) hand(c net.Conn) bool {
	select {
	case l.ch <- c:
		return true
	case <-l.done:
		return false
	}
}

// Accept implements net.Listener.
func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close implements net.Listener.
func (l *chanListener) Close() error {
	l.closer.Do(func() { close(l.done) })
	return nil
}

// Addr implements net.Listener.
func (l *chanListener) Addr() net.Addr { return l.addr }

// waitGroupTimeout waits for wg, failing tb when that takes longer than d (a
// handler that ignores its request context).
func waitGroupTimeout(tb testing.TB, wg *sync.WaitGroup, what string, d time.Duration) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		tb.Errorf("testsupport: %s: goroutines still running %v after Close (a handler ignoring its request context?)", what, d)
	}
}
