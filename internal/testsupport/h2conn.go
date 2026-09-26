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
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// maxFrameSize is the largest DATA or header-block payload the server writes:
// the smallest SETTINGS_MAX_FRAME_SIZE a peer may advertise, so it is always
// allowed.
const maxFrameSize = 16384

// defaultWindow is the initial flow-control window of RFC 9113.
const defaultWindow = 65535

// drainBound is how long a connection whose graceful close has begun
// ([LoopbackServer.CloseConns], ActionClose, or the end after GOAWAY) waits
// for the client to close its side before the server closes the socket
// anyway.
const drainBound = 5 * time.Second

// Errors a handler sees from its ResponseWriter.
var (
	errStreamReset = errors.New("testsupport: stream reset")
	errConnClosed  = errors.New("testsupport: connection closed")
)

// ErrConnClosing is what [H2Conn.SetMaxConcurrentStreams] returns, alone or
// wrapped, when the connection's close had begun before its SETTINGS frame
// could leave: GOAWAY drained it, the client closed it, or [H2Conn.Close] or
// [H2Conn.Reset] ran. The limit was not delivered, and the connection serves
// no new stream. [LoopbackServer.LiveH2Conns] lists a connection until its
// reader has stopped, so a test that walks that list can meet one.
var ErrConnClosing = errors.New("testsupport: connection closing")

// H2Conn is one HTTP/2 connection of a [LoopbackServer], served by the
// package's frame writer. Its methods are the connection-level knobs; they
// are safe to call from any goroutine, including from
// [ServerConfig.OnStream].
type H2Conn struct {
	srv   *LoopbackServer
	index int
	nc    *tls.Conn
	br    *bufio.Reader
	fr    *http2.Framer
	state tls.ConnectionState

	wmu  sync.Mutex // serialises frame writes and the HPACK encoder; taken before mu
	henc *hpack.Encoder
	hbuf bytes.Buffer

	mu         sync.Mutex
	cond       *sync.Cond // flow-control waits
	streams    map[uint32]*h2stream
	lastID     uint32
	connWin    int64 // what the server may still send on the connection
	initWin    int64 // the peer's SETTINGS_INITIAL_WINDOW_SIZE
	active     int
	maxStreams uint32 // the stream limit enforced now; 0 means none
	goAway     bool
	goAwayLast uint32
	closed     bool

	// draining is set, under wmu, once the graceful close has begun
	// (closeWrite): nothing is written after close_notify, so every writer
	// drops its frame, and the reader only reads and discards until the
	// client closes its side.
	draining atomic.Bool
}

// h2stream is the server's state of one stream.
type h2stream struct {
	id     uint32
	seq    int // the request's SeenRequest.Seq
	win    int64
	body   *bodyPipe // nil for a held stream
	ctx    context.Context
	cancel context.CancelFunc
	// bodyless is set when the request's HEADERS carried END_STREAM: the
	// request has no body at all.
	bodyless bool
	// reset is set once the stream is over from the server's point of view
	// (client RST_STREAM, GOAWAY drop, connection close): writers stop.
	reset bool
	// remoteDone is set when the client's side ended (END_STREAM).
	remoteDone bool
	// ended is set when the frame that ends the server's side (END_STREAM or
	// RST_STREAM) was cleared for writing; a drop after that is not recorded
	// as SeenRequest.Dropped.
	ended bool
	// retired is set once the stream no longer counts against the stream
	// limit: both sides have ended, or a RST_STREAM is about to leave. It
	// is set before the frame that tells the client so (see retireLocked).
	retired bool
}

// frameEnd says what a stream frame does to the server's side of it.
type frameEnd int

const (
	// frameMid leaves the stream open.
	frameMid frameEnd = iota
	// frameEndStream carries END_STREAM: the server's side ends, and the
	// stream closes if the client's side has ended too.
	frameEndStream
	// frameReset is RST_STREAM: the stream closes.
	frameReset
)

// newH2Conn prepares a connection whose TLS handshake negotiated h2.
func newH2Conn(s *LoopbackServer, nc *tls.Conn, idx int) *H2Conn {
	c := &H2Conn{
		srv:     s,
		index:   idx,
		nc:      nc,
		state:   nc.ConnectionState(),
		streams: make(map[uint32]*h2stream),
		connWin: defaultWindow,
		initWin: defaultWindow,

		maxStreams: s.cfg.MaxConcurrentStreams,
	}
	c.cond = sync.NewCond(&c.mu)
	c.henc = hpack.NewEncoder(&c.hbuf)
	c.br = bufio.NewReader(nc)
	c.fr = http2.NewFramer(nc, c.br)
	c.fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	c.fr.MaxHeaderListSize = 1 << 20
	return c
}

// Index returns the connection's position in accept order, from 0.
func (c *H2Conn) Index() int { return c.index }

// ActiveStreams returns the identifiers of the streams open on the server
// side (served or held), in ascending order: the streams that count against
// the stream limit.
func (c *H2Conn) ActiveStreams() []uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]uint32, 0, len(c.streams))
	for id, st := range c.streams {
		if !st.retired {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// GoAway sends GOAWAY with lastStreamID and code. From then on no stream
// above lastStreamID is served: the ones already open are dropped (their
// handlers' contexts are cancelled, their ResponseWriters' writes fail, and
// nothing more is written for them) and new ones are ignored, as RFC 9113
// section 6.8 has it. GOAWAY tells the client that a dropped stream was not
// processed, but its handler may already have run, in part or up to its last
// write: a test that counts side effects must not assume otherwise. A dropped
// stream's [SeenRequest] shows Dropped. Once every stream at or below
// lastStreamID has finished, the server ends the connection gracefully, as
// [LoopbackServer.CloseConns] does: close_notify and a TCP FIN, then it
// reads and discards what the client still sends until the client closes
// its side, and only then closes the socket (ruling K34). The GOAWAY frame
// always reaches the wire before close_notify. A second call can only lower
// lastStreamID. On a connection whose graceful close has begun nothing is
// written, and GoAway returns an error.
func (c *H2Conn) GoAway(lastStreamID uint32, code ErrCode) error {
	// Once c.mu is released, the goroutine that finishes the last stream at or
	// below lastStreamID may run maybeFinish at once. Holding the write lock
	// from before the state changes until the frame is written makes its
	// close_notify wait for GOAWAY; without it, the frame write failed with
	// "tls: protocol is shutdown" and the client read EOF with no GOAWAY.
	c.wmu.Lock()
	if c.draining.Load() {
		c.wmu.Unlock()
		return fmt.Errorf("%w: GOAWAY after close_notify", errConnClosed)
	}
	c.mu.Lock()
	if c.goAway {
		lastStreamID = min(lastStreamID, c.goAwayLast)
	}
	c.goAway = true
	c.goAwayLast = lastStreamID
	for id, st := range c.streams {
		if id > lastStreamID {
			c.dropLocked(st)
		}
	}
	c.mu.Unlock()
	// Numbered before the frame leaves, as CloseWriteSeq is: a client may
	// close the connection as soon as it reads the GOAWAY, and the reader
	// can record that close before this goroutine runs again.
	c.srv.noteConn(c.index, func(ci *ConnInfo) { ci.GoAwaySeq = c.srv.seq.Add(1) })
	err := c.fr.WriteGoAway(lastStreamID, http2.ErrCode(code), nil)
	c.wmu.Unlock()
	if err != nil {
		return c.writeFailed(err)
	}
	c.maybeFinish()
	return nil
}

// SetMaxConcurrentStreams sends a SETTINGS frame advertising n (at least 1)
// as SETTINGS_MAX_CONCURRENT_STREAMS and enforces n on this connection from
// then on: a new stream that would exceed it is reset with REFUSED_STREAM and
// counted by [LoopbackServer.OverLimit], even one the client opened before it
// read the frame (RFC 9113 section 5.1.2 allows the refusal; a real server
// may wait for the client's acknowledgement). Streams already open are left
// alone. It lowers or raises a limit in the middle of a connection. On a
// connection whose close has begun it changes nothing and returns an error
// matching [ErrConnClosing].
func (c *H2Conn) SetMaxConcurrentStreams(n uint32) error {
	if n == 0 {
		return errors.New("testsupport: SetMaxConcurrentStreams needs a limit of at least 1")
	}
	// The write lock is taken first, as everywhere (wmu before mu), and held
	// from the state change through the frame write, so the new limit and
	// the frame that announces it cannot be reordered against another write.
	// Under both locks the close cannot have begun unseen: maybeFinish sets
	// closed before it waits for wmu to send close_notify.
	c.wmu.Lock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.wmu.Unlock()
		return ErrConnClosing
	}
	c.maxStreams = n
	c.mu.Unlock()
	err := c.fr.WriteSettings(http2.Setting{ID: http2.SettingMaxConcurrentStreams, Val: n})
	c.wmu.Unlock()
	if err != nil {
		// Close does not take wmu (it must break a write stuck on a peer that
		// stopped reading), so the reader's Close after the client closed the
		// connection can land during the write; it sets closed before it
		// closes the socket.
		c.mu.Lock()
		closing := c.closed
		c.mu.Unlock()
		werr := c.writeFailed(err)
		if closing {
			return fmt.Errorf("%w: %w", ErrConnClosing, err)
		}
		return werr
	}
	return nil
}

// Close closes the connection at once, without GOAWAY. Closing the TLS
// connection sends a close_notify alert (unless a frame write is in flight)
// before the TCP FIN, so the client reads io.EOF, provided the server has
// read everything the client sent and nothing more arrives: otherwise the
// kernel answers with a TCP reset, and on Windows the reset destroys what
// the client has not read yet (ruling K33). [LoopbackServer.CloseConns] and
// ActionClose close gracefully instead; [H2Conn.Reset] ends the connection
// with a TCP reset.
func (c *H2Conn) Close() {
	c.shutdown()
	_ = c.nc.Close()
	c.noteClosed()
}

// Reset ends the connection abruptly: it sets SO_LINGER to 0 on the TCP
// socket and closes the socket under the TLS layer, without GOAWAY or
// close_notify, so the kernel sends a TCP RST and the client's next read
// fails with ECONNRESET (WSAECONNRESET on Windows) rather than io.EOF.
func (c *H2Conn) Reset() {
	c.shutdown()
	raw := c.nc.NetConn()
	if tc, ok := raw.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = raw.Close()
	c.noteClosed()
}

// closeGracefully ends the connection for [LoopbackServer.CloseConns] and
// ActionClose: it drops every stream and begins the graceful close
// (closeWrite). A connection whose close had begun is left alone.
func (c *H2Conn) closeGracefully() {
	if c.shutdown() {
		c.closeWrite()
	}
}

// closeWrite begins the graceful close of a connection that shutdown or
// maybeFinish has just marked closed. Under the write lock, so a frame in
// flight (a GOAWAY among them) leaves first, it sets draining, which stops
// every later write, and sends close_notify and a TCP FIN; the reader then
// discards what the client still sends until it reads the end of the
// client's side or drainBound passes, and only then does serve close the
// socket. A socket closed earlier, with the client's frames unread or still
// on their way, is ended by the kernel with a TCP reset (RFC 1122 section
// 4.2.2.13), which on Windows destroys what the client has not read yet
// (rulings K33, K34).
func (c *H2Conn) closeWrite() {
	c.wmu.Lock()
	// The record precedes close_notify, and so anything the client does once
	// it reads it; the reader records the client's close only from here on.
	c.srv.noteConn(c.index, func(ci *ConnInfo) { ci.CloseWriteSeq = c.srv.seq.Add(1) })
	c.draining.Store(true)
	_ = c.nc.CloseWrite() // close_notify
	if cw, ok := c.nc.NetConn().(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite() // FIN
	}
	// Under the write lock, so serve cannot clear it after its preface.
	_ = c.nc.SetReadDeadline(time.Now().Add(drainBound))
	c.wmu.Unlock()
}

// noteClosed records the socket's first close.
func (c *H2Conn) noteClosed() {
	c.srv.noteConn(c.index, func(ci *ConnInfo) {
		if ci.ClosedSeq == 0 {
			ci.ClosedSeq = c.srv.seq.Add(1)
		}
	})
}

// shutdown marks the connection closed and releases every stream; it
// reports whether this call closed it.
func (c *H2Conn) shutdown() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.closed = true
	for _, st := range c.streams {
		c.dropLocked(st)
	}
	c.cond.Broadcast()
	return true
}

// dropLocked ends a stream on the server side without writing anything.
// c.mu must be held.
func (c *H2Conn) dropLocked(st *h2stream) {
	if _, ok := c.streams[st.id]; !ok {
		return
	}
	delete(c.streams, st.id)
	if !st.retired {
		c.active--
	}
	st.reset = true
	st.cancel()
	if st.body != nil {
		st.body.closeWithError(errStreamReset)
	}
	if !st.ended {
		c.srv.markDropped(st.seq)
	}
	c.cond.Broadcast()
}

// write runs one of the reader's frame writes (its SETTINGS and their
// acknowledgement, PING acknowledgements, WINDOW_UPDATE refunds and
// RST_STREAM) under the write lock. Once the graceful close has begun the
// frame is dropped and write reports success: nothing may follow
// close_notify, and the reader goes on to drain. A failed write ends the
// connection (writeFailed).
func (c *H2Conn) write(fn func() error) error {
	c.wmu.Lock()
	if c.draining.Load() {
		c.wmu.Unlock()
		return nil
	}
	err := fn()
	c.wmu.Unlock()
	if err != nil {
		return c.writeFailed(err)
	}
	return nil
}

// writeFailed ends the connection after a frame write failed and returns
// the error for the writer. Every writer checks draining under the write
// lock first, so the write did not follow close_notify: the socket is
// broken (the client reset it, or Close or Reset closed it during the
// write). A connection whose graceful close has begun since is left to its
// draining reader, which closes the socket once the client has closed its
// side or drainBound has passed, as closeWrite requires.
func (c *H2Conn) writeFailed(err error) error {
	if !c.draining.Load() {
		c.Close()
	}
	return fmt.Errorf("%w: %w", errConnClosed, err)
}

// serve reads the client preface and then frames until the connection ends.
func (c *H2Conn) serve() {
	defer c.Close()
	_ = c.nc.SetReadDeadline(time.Now().Add(10 * time.Second))
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(c.br, preface); err != nil || string(preface) != http2.ClientPreface {
		return
	}
	// A graceful close that began during the preface has set the drain's
	// deadline (closeWrite, under the write lock); it stays.
	c.wmu.Lock()
	if !c.draining.Load() {
		_ = c.nc.SetReadDeadline(time.Time{})
	}
	c.wmu.Unlock()

	var settings []http2.Setting
	if n := c.srv.cfg.MaxConcurrentStreams; n > 0 {
		settings = append(settings, http2.Setting{ID: http2.SettingMaxConcurrentStreams, Val: n})
	}
	if c.write(func() error { return c.fr.WriteSettings(settings...) }) != nil {
		return
	}
	for {
		f, err := c.fr.ReadFrame()
		if c.draining.Load() {
			if !c.drain(f, err) {
				return
			}
			continue
		}
		if err != nil {
			if se, ok := errors.AsType[http2.StreamError](err); ok {
				_ = c.write(func() error { return c.fr.WriteRSTStream(se.StreamID, se.Code) })
				continue
			}
			c.peerClosed(err)
			return
		}
		if !c.handleFrame(f) {
			return
		}
	}
}

// drain takes one read of a connection whose graceful close has begun: it
// records a frame as drained, answering nothing, and reports true to read
// on, or records the end of the client's side (peerClosed) and reports
// false. Any other error (the drain's deadline, a reset) ends the read too,
// unrecorded.
func (c *H2Conn) drain(f http2.Frame, err error) bool {
	switch {
	case err == nil:
		c.srv.noteConn(c.index, func(ci *ConnInfo) { ci.Drained = append(ci.Drained, f.Header().Type.String()) })
		return true
	case c.peerClosed(err):
		return false
	}
	_, streamErr := errors.AsType[http2.StreamError](err)
	return streamErr
}

// peerClosed records PeerClosedSeq when err is the end of the client's side
// (io.EOF after its close_notify or FIN; io.ErrUnexpectedEOF inside a
// frame) and reports whether it was. The client may close first, before
// the server's graceful close begins: net/http closes a connection that
// GOAWAY ended as soon as its last stream is done, which can precede
// maybeFinish on the goroutine that finished that stream or called GoAway.
func (c *H2Conn) peerClosed(err error) bool {
	if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	c.srv.noteConn(c.index, func(ci *ConnInfo) { ci.PeerClosedSeq = c.srv.seq.Add(1) })
	return true
}

// handleFrame processes one frame; it reports false when the connection
// should end.
func (c *H2Conn) handleFrame(f http2.Frame) bool {
	switch f := f.(type) {
	case *http2.SettingsFrame:
		if f.IsAck() {
			return true
		}
		c.applySettings(f)
		return c.write(c.fr.WriteSettingsAck) == nil
	case *http2.MetaHeadersFrame:
		c.onHeaders(f)
	case *http2.DataFrame:
		c.onData(f)
	case *http2.WindowUpdateFrame:
		c.onWindowUpdate(f)
	case *http2.PingFrame:
		if !f.IsAck() {
			return c.write(func() error { return c.fr.WritePing(true, f.Data) }) == nil
		}
	case *http2.RSTStreamFrame:
		c.mu.Lock()
		if st, ok := c.streams[f.StreamID]; ok {
			c.dropLocked(st)
		}
		c.mu.Unlock()
		c.maybeFinish()
	case *http2.GoAwayFrame:
		return false
	}
	return true
}

// applySettings applies the client's SETTINGS that affect what the server
// sends.
func (c *H2Conn) applySettings(f *http2.SettingsFrame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = f.ForeachSetting(func(s http2.Setting) error {
		if s.ID == http2.SettingInitialWindowSize {
			delta := int64(s.Val) - c.initWin
			c.initWin = int64(s.Val)
			for _, st := range c.streams {
				st.win += delta
			}
		}
		return nil
	})
	c.cond.Broadcast()
}

// onHeaders starts a stream: it records the request, applies the stream
// limit and OnStream, and serves, refuses or drops it.
func (c *H2Conn) onHeaders(f *http2.MetaHeadersFrame) {
	id := f.StreamID
	c.mu.Lock()
	if id <= c.lastID || id%2 == 0 {
		// On an open stream this is the request's trailer block: its fields
		// are ignored, but its END_STREAM ends the body. Anything else (a
		// reused or even identifier) is ignored.
		var body *bodyPipe
		if st := c.streams[id]; st != nil && f.StreamEnded() && !st.remoteDone {
			st.remoteDone = true
			body = st.body
			if st.ended {
				c.retireLocked(st)
			}
		}
		c.mu.Unlock()
		if body != nil {
			body.closeWithError(io.EOF)
		}
		return
	}
	c.lastID = id
	ignore := c.closed || (c.goAway && id > c.goAwayLast)
	c.mu.Unlock()
	if ignore {
		return
	}

	st := &Stream{Conn: c, ID: id, Header: make(http.Header)}
	for _, hf := range f.Fields {
		switch hf.Name {
		case ":method":
			st.Method = hf.Value
		case ":path":
			st.Path = hf.Value
		case ":authority":
			st.Authority = hf.Value
		default:
			if !strings.HasPrefix(hf.Name, ":") {
				st.Header.Add(hf.Name, hf.Value)
			}
		}
	}
	st.Seq = c.srv.record(SeenRequest{
		Conn: c.index, StreamID: id, Proto: "HTTP/2.0", Method: st.Method, Path: st.Path,
		Authority: st.Authority, Header: st.Header.Clone(), Action: ActionServe,
	})

	c.mu.Lock()
	full := c.maxStreams > 0 && c.active >= int(c.maxStreams)
	c.mu.Unlock()
	if full {
		c.srv.overLimit.Add(1)
		c.srv.setAction(st.Seq, ActionRefuse)
		_ = c.write(func() error { return c.fr.WriteRSTStream(id, http2.ErrCode(CodeRefusedStream)) })
		return
	}

	action := ActionServe
	if c.srv.cfg.OnStream != nil {
		action = c.srv.cfg.OnStream(st)
	}
	c.srv.setAction(st.Seq, action)
	switch action {
	case ActionRefuse:
		_ = c.write(func() error { return c.fr.WriteRSTStream(id, http2.ErrCode(CodeRefusedStream)) })
	case ActionGoAway:
		_ = c.GoAway(max(id, 2)-2, CodeNoError)
	case ActionClose:
		c.closeGracefully()
	case ActionReset:
		c.Reset()
	case ActionHold:
		c.open(st, f.StreamEnded(), false)
	default:
		c.open(st, f.StreamEnded(), true)
	}
}

// open registers a stream and, when serve is set, starts its handler. A
// stream that GOAWAY or a closed connection overtook after onHeaders recorded
// it (from OnStream, or from another goroutine) never opens: nothing runs or
// is written for it, and its SeenRequest shows Dropped.
func (c *H2Conn) open(info *Stream, ended, serve bool) {
	ctx, cancel := context.WithCancel(context.Background())
	st := &h2stream{id: info.ID, seq: info.Seq, ctx: ctx, cancel: cancel, bodyless: ended, remoteDone: ended}
	if serve {
		st.body = newBodyPipe()
		if ended {
			st.body.closeWithError(io.EOF)
		}
	}
	c.mu.Lock()
	if c.closed || (c.goAway && info.ID > c.goAwayLast) {
		c.srv.markDropped(info.Seq)
		c.mu.Unlock()
		cancel()
		return
	}
	st.win = c.initWin
	c.streams[info.ID] = st
	c.active++
	active := c.active
	c.mu.Unlock()
	c.srv.noteActive(active)
	if serve {
		c.srv.wg.Go(func() { c.runHandler(st, info) })
	}
}

// onData delivers request body bytes and gives the flow-control credit back
// at once, so an upload never stalls on the server.
func (c *H2Conn) onData(f *http2.DataFrame) {
	n := f.Length
	c.mu.Lock()
	st := c.streams[f.StreamID]
	open := st != nil && !st.reset && !st.remoteDone
	if st != nil && f.StreamEnded() {
		st.remoteDone = true
		if st.ended {
			// The client's END_STREAM closes a stream whose server side
			// has ended; the reader retires it before it reads the next
			// frame, which may open a stream in its place.
			c.retireLocked(st)
		}
	}
	c.mu.Unlock()
	if n > 0 {
		_ = c.write(func() error { return c.fr.WriteWindowUpdate(0, n) })
		if open && !f.StreamEnded() {
			_ = c.write(func() error { return c.fr.WriteWindowUpdate(f.StreamID, n) })
		}
	}
	if st != nil && st.body != nil {
		if data := f.Data(); len(data) > 0 {
			st.body.write(data)
		}
		if f.StreamEnded() {
			st.body.closeWithError(io.EOF)
		}
	}
}

// onWindowUpdate adds send credit.
func (c *H2Conn) onWindowUpdate(f *http2.WindowUpdateFrame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f.StreamID == 0 {
		c.connWin += int64(f.Increment)
	} else if st, ok := c.streams[f.StreamID]; ok {
		st.win += int64(f.Increment)
	}
	c.cond.Broadcast()
}

// maybeFinish ends the connection gracefully (closeWrite) once GOAWAY was
// sent and no stream at or below its LastStreamID is left. The streams above
// it were dropped by GoAway, so none is left at all. Before ruling K34 it
// sent close_notify alone and let the reader handle frames as before: a
// DATA frame of a stream GOAWAY had refused made the reader write a
// WINDOW_UPDATE refund after close_notify, the write failed and closed the
// socket with the client's frames unread, and on Windows the reset
// destroyed the GOAWAY before net/http could replay the request.
func (c *H2Conn) maybeFinish() {
	c.mu.Lock()
	if !c.goAway || c.closed {
		c.mu.Unlock()
		return
	}
	for id := range c.streams {
		if id <= c.goAwayLast {
			c.mu.Unlock()
			return
		}
	}
	c.closed = true
	c.cond.Broadcast()
	c.mu.Unlock()
	c.closeWrite()
}

// retireLocked takes st out of the count of open streams, once: the stream
// is closed, or about to be, as far as the client can tell. A stream whose
// last frame is written is closed on the server side when the frame leaves
// (RFC 9113 section 5.1), so the count drops before the write, under the
// write lock: a client that reads the frame and opens its next stream at
// once never finds the server one stream over its limit, as with net/http's
// server. c.mu must be held.
func (c *H2Conn) retireLocked(st *h2stream) {
	if st.retired || c.streams[st.id] != st {
		return
	}
	st.retired = true
	c.active--
	c.cond.Broadcast()
}

// retireIfClosing retires st when the frame about to be written closes it:
// a RST_STREAM, or END_STREAM after the client's END_STREAM. The caller holds
// c.wmu (taken before c.mu).
func (c *H2Conn) retireIfClosing(st *h2stream, end frameEnd) {
	c.mu.Lock()
	if end == frameReset || (end == frameEndStream && st.remoteDone) {
		c.retireLocked(st)
	}
	c.mu.Unlock()
}

// finishStream forgets a stream the handler is done with.
func (c *H2Conn) finishStream(st *h2stream) {
	c.mu.Lock()
	if cur, ok := c.streams[st.id]; ok && cur == st {
		delete(c.streams, st.id)
		if !st.retired {
			c.active--
		}
		c.cond.Broadcast()
	}
	c.mu.Unlock()
	st.cancel()
	c.maybeFinish()
}

// reserve waits until want bytes (or part of them) may be sent on st and
// takes that much credit from both windows.
func (c *H2Conn) reserve(st *h2stream, want int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		switch {
		case st.reset:
			return 0, errStreamReset
		case c.closed:
			return 0, errConnClosed
		}
		if n := min(int64(want), c.connWin, st.win, maxFrameSize); n > 0 {
			c.connWin -= n
			st.win -= n
			return int(n), nil
		}
		c.cond.Wait()
	}
}

// runHandler serves one stream with the configured handler.
func (c *H2Conn) runHandler(st *h2stream, info *Stream) {
	w := &h2ResponseWriter{conn: c, st: st, header: make(http.Header)}
	defer func() {
		if v := recover(); v != nil {
			if err, ok := v.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
				buf := make([]byte, 64<<10)
				buf = buf[:runtime.Stack(buf, false)]
				c.srv.tb.Errorf("testsupport: panic in LoopbackServer handler: %v\n%s", v, buf)
			}
			st.body.abandon()
			_ = c.writeStream(st, frameReset, func() error { return c.fr.WriteRSTStream(st.id, http2.ErrCode(CodeInternalError)) })
			c.finishStream(st)
		}
	}()
	c.srv.handler().ServeHTTP(w, c.request(st, info))
	w.finish()
	c.mu.Lock()
	uploading := !st.remoteDone && !st.reset
	c.mu.Unlock()
	st.body.abandon()
	if uploading {
		// The handler answered before reading the whole body: tell the client
		// to stop sending, as net/http's server does.
		_ = c.writeStream(st, frameReset, func() error { return c.fr.WriteRSTStream(st.id, http2.ErrCode(CodeNoError)) })
	}
	c.finishStream(st)
}

// request builds the *http.Request a handler sees.
func (c *H2Conn) request(st *h2stream, info *Stream) *http.Request {
	u, err := url.ParseRequestURI(info.Path)
	if err != nil {
		u = &url.URL{Path: info.Path}
	}
	contentLength := int64(-1)
	if v := info.Header.Get("Content-Length"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			contentLength = n
		}
	}
	var body io.ReadCloser = st.body
	if st.bodyless {
		// No body at all: net/http's servers report ContentLength 0 and the
		// HTTP/1.1 one sets http.NoBody. A declared length stays as sent.
		body = http.NoBody
		if contentLength < 0 {
			contentLength = 0
		}
	}
	state := c.state
	r := &http.Request{
		Method:        info.Method,
		URL:           u,
		Proto:         "HTTP/2.0",
		ProtoMajor:    2,
		Header:        info.Header,
		Body:          body,
		ContentLength: contentLength,
		Host:          info.Authority,
		RemoteAddr:    c.nc.RemoteAddr().String(),
		RequestURI:    info.Path,
		TLS:           &state,
	}
	return r.WithContext(st.ctx)
}

// claim reports whether a frame for st may still be written. With end set,
// the frame ends the server's side of the stream, and claim records that in
// the same critical section, so a drop that comes later does not mark a
// stream whose last frame is already on its way as SeenRequest.Dropped.
func (c *H2Conn) claim(st *h2stream, end bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st.reset || c.closed {
		return false
	}
	st.ended = st.ended || end
	return true
}

// writeStream runs a frame write for st unless the stream is already over
// or the graceful close has begun since claim (nothing follows
// close_notify); end says what the frame does to the server's side of the
// stream. A frame that closes the stream retires it under the write lock,
// before the write.
func (c *H2Conn) writeStream(st *h2stream, end frameEnd, fn func() error) error {
	if !c.claim(st, end != frameMid) {
		return errStreamReset
	}
	c.wmu.Lock()
	if c.draining.Load() {
		c.wmu.Unlock()
		return errConnClosed
	}
	c.retireIfClosing(st, end)
	err := fn()
	c.wmu.Unlock()
	if err != nil {
		return c.writeFailed(err)
	}
	return nil
}

// writeHeaders encodes and writes a response header block, split into
// CONTINUATION frames when it exceeds maxFrameSize, unless the stream is
// already over or the graceful close has begun since claim.
func (c *H2Conn) writeHeaders(st *h2stream, status int, h http.Header, endStream bool) error {
	if !c.claim(st, endStream) {
		return errStreamReset
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.draining.Load() {
		return errConnClosed
	}
	if endStream {
		c.retireIfClosing(st, frameEndStream)
	}
	c.hbuf.Reset()
	_ = c.henc.WriteField(hpack.HeaderField{Name: ":status", Value: strconv.Itoa(status)})
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		name := strings.ToLower(k)
		switch name {
		case "connection", "keep-alive", "proxy-connection", "transfer-encoding", "upgrade":
			continue
		}
		for _, v := range h[k] {
			_ = c.henc.WriteField(hpack.HeaderField{Name: name, Value: v})
		}
	}
	block := c.hbuf.Bytes()
	first := block[:min(len(block), maxFrameSize)]
	rest := block[len(first):]
	err := c.fr.WriteHeaders(http2.HeadersFrameParam{
		StreamID: st.id, BlockFragment: first, EndStream: endStream, EndHeaders: len(rest) == 0,
	})
	for err == nil && len(rest) > 0 {
		chunk := rest[:min(len(rest), maxFrameSize)]
		rest = rest[len(chunk):]
		err = c.fr.WriteContinuation(st.id, len(rest) == 0, chunk)
	}
	if err != nil {
		return c.writeFailed(err)
	}
	return nil
}

// h2ResponseWriter is the http.ResponseWriter of a LoopbackServer handler.
type h2ResponseWriter struct {
	conn        *H2Conn
	st          *h2stream
	header      http.Header
	status      int
	sentHeaders bool
	ended       bool
}

// Header implements http.ResponseWriter.
func (w *h2ResponseWriter) Header() http.Header { return w.header }

// WriteHeader implements http.ResponseWriter.
func (w *h2ResponseWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

// Write implements http.ResponseWriter, sending DATA frames under flow
// control.
func (w *h2ResponseWriter) Write(p []byte) (int, error) {
	if err := w.sendHeaders(false); err != nil {
		return 0, err
	}
	written := 0
	for len(p) > 0 {
		n, err := w.conn.reserve(w.st, len(p))
		if err != nil {
			return written, err
		}
		chunk := p[:n]
		if err := w.conn.writeStream(w.st, frameMid, func() error { return w.conn.fr.WriteData(w.st.id, false, chunk) }); err != nil {
			return written, err
		}
		p = p[n:]
		written += n
	}
	return written, nil
}

// Flush implements http.Flusher: it sends the headers if they are not out.
func (w *h2ResponseWriter) Flush() { _ = w.sendHeaders(false) }

// sendHeaders writes the header block once.
func (w *h2ResponseWriter) sendHeaders(endStream bool) error {
	if w.sentHeaders {
		return nil
	}
	w.sentHeaders = true
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.ended = endStream
	return w.conn.writeHeaders(w.st, w.status, w.header, endStream)
}

// finish ends the response: headers with END_STREAM when nothing was
// written, otherwise an empty DATA frame with END_STREAM.
func (w *h2ResponseWriter) finish() {
	if !w.sentHeaders {
		_ = w.sendHeaders(true)
		return
	}
	if !w.ended {
		w.ended = true
		_ = w.conn.writeStream(w.st, frameEndStream, func() error { return w.conn.fr.WriteData(w.st.id, true, nil) })
	}
}

// bodyPipe carries request body bytes from the reader goroutine to the
// handler. Its buffer is unbounded: the server returns flow-control credit
// at once, and a test's upload is bounded.
type bodyPipe struct {
	mu        sync.Mutex
	cond      *sync.Cond
	buf       []byte
	err       error // set once the body ends; io.EOF for a normal end
	abandoned bool
}

// newBodyPipe returns an empty pipe.
func newBodyPipe() *bodyPipe {
	p := &bodyPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// write appends data unless the handler is done with the body.
func (p *bodyPipe) write(data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.abandoned || p.err != nil {
		return
	}
	p.buf = append(p.buf, data...)
	p.cond.Broadcast()
}

// closeWithError ends the body; the first error wins.
func (p *bodyPipe) closeWithError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err == nil {
		p.err = err
	}
	p.cond.Broadcast()
}

// abandon drops buffered and future bytes.
func (p *bodyPipe) abandon() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.abandoned = true
	p.buf = nil
	p.cond.Broadcast()
}

// Read implements io.Reader.
func (p *bodyPipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.buf) == 0 && p.err == nil && !p.abandoned {
		p.cond.Wait()
	}
	if len(p.buf) > 0 {
		n := copy(b, p.buf)
		p.buf = p.buf[n:]
		return n, nil
	}
	if p.abandoned {
		return 0, net.ErrClosed
	}
	return 0, p.err
}

// Close implements io.Closer.
func (p *bodyPipe) Close() error {
	p.abandon()
	return nil
}
