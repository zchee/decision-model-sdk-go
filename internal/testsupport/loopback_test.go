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
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// protocols builds an http.Protocols value.
func protocols(h1, h2 bool) *http.Protocols {
	var p http.Protocols
	p.SetHTTP1(h1)
	p.SetHTTP2(h2)
	return &p
}

// newTransport returns a client transport that trusts the package
// certificate, with strict stream accounting, closed at the end of the test.
func newTransport(t *testing.T, h1, h2 bool) *http.Transport {
	t.Helper()
	tr := &http.Transport{
		Protocols:       protocols(h1, h2),
		TLSClientConfig: ClientTLSConfig(t),
		HTTP2:           &http.HTTP2Config{StrictMaxConcurrentRequests: true},
	}
	t.Cleanup(tr.CloseIdleConnections)
	return tr
}

// get sends a GET and returns the status, the protocol major version and the
// body.
func get(t *testing.T, rt http.RoundTripper, url string) (status, proto int, body string, err error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return 0, 0, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.ProtoMajor, string(b), err
}

// waitFor polls cond for up to 5 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// actions returns the recorded actions of a server's requests.
func actions(s *LoopbackServer) []Action {
	var out []Action
	for _, r := range s.Requests() {
		out = append(out, r.Action)
	}
	return out
}

// dropped returns the recorded Dropped flags of a server's requests.
func dropped(s *LoopbackServer) []bool {
	var out []bool
	for _, r := range s.Requests() {
		out = append(out, r.Dropped)
	}
	return out
}

// recv returns the next value from ch, failing the test after 5 s.
func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

// isConnReset reports whether err is a TCP reset from the peer: ECONNRESET,
// or WSAECONNRESET (10054), the code Windows reports for it. The message
// differs by system, so the check compares codes, not text.
func isConnReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.Errno(10054))
}

// rawClient speaks HTTP/2 frame by frame, so a test can see exactly what
// the server writes.
type rawClient struct {
	t        *testing.T
	conn     *tls.Conn
	fr       *http2.Framer
	henc     *hpack.Encoder
	hbuf     bytes.Buffer
	settings map[http2.SettingID]uint32 // the server's first SETTINGS frame
}

// dialRaw connects to addr with ALPN h2 and sends the preface and SETTINGS.
func dialRaw(t *testing.T, addr string) *rawClient {
	t.Helper()
	cfg := ClientTLSConfig(t)
	cfg.NextProtos = []string{"h2"}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if p := conn.ConnectionState().NegotiatedProtocol; p != "h2" {
		t.Fatalf("negotiated %q, want h2", p)
	}
	c := &rawClient{t: t, conn: conn, fr: http2.NewFramer(conn, conn)}
	c.fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	c.henc = hpack.NewEncoder(&c.hbuf)
	if _, err := io.WriteString(conn, http2.ClientPreface); err != nil {
		t.Fatal(err)
	}
	if err := c.fr.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	return c
}

// request opens stream id with a GET for path.
func (c *rawClient) request(id uint32, path string, endStream bool) {
	c.t.Helper()
	c.hbuf.Reset()
	for _, f := range []hpack.HeaderField{
		{Name: ":method", Value: http.MethodGet},
		{Name: ":scheme", Value: "https"},
		{Name: ":authority", Value: "example.com"},
		{Name: ":path", Value: path},
		{Name: "x-typesafe-retry-count", Value: "0"},
	} {
		_ = c.henc.WriteField(f)
	}
	if err := c.fr.WriteHeaders(http2.HeadersFrameParam{StreamID: id, BlockFragment: c.hbuf.Bytes(), EndStream: endStream, EndHeaders: true}); err != nil {
		c.t.Fatal(err)
	}
}

// trailers ends stream id's request with a trailer block.
func (c *rawClient) trailers(id uint32) {
	c.t.Helper()
	c.hbuf.Reset()
	_ = c.henc.WriteField(hpack.HeaderField{Name: "x-checksum", Value: "none"})
	if err := c.fr.WriteHeaders(http2.HeadersFrameParam{StreamID: id, BlockFragment: c.hbuf.Bytes(), EndStream: true, EndHeaders: true}); err != nil {
		c.t.Fatal(err)
	}
}

// serverSettings returns the server's first SETTINGS frame, reading up to
// it (a server always sends SETTINGS first).
func (c *rawClient) serverSettings() map[http2.SettingID]uint32 {
	c.t.Helper()
	for c.settings == nil {
		_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		f, err := c.fr.ReadFrame()
		if err != nil {
			c.t.Fatalf("read SETTINGS: %v", err)
		}
		sf, ok := f.(*http2.SettingsFrame)
		if !ok || sf.IsAck() {
			c.t.Fatalf("first server frame is %v, want SETTINGS", f.Header())
		}
		c.settings = map[http2.SettingID]uint32{}
		_ = sf.ForeachSetting(func(s http2.Setting) error {
			c.settings[s.ID] = s.Val
			return nil
		})
		_ = c.fr.WriteSettingsAck()
	}
	return c.settings
}

// frame is a summary of one frame the server sent.
type frame struct {
	Type     string
	StreamID uint32
	Status   string // HEADERS: :status
	Data     string // DATA: payload
	End      bool   // END_STREAM
	Code     ErrCode
	LastID   uint32 // GOAWAY
	// MaxStreams is a later SETTINGS frame's MAX_CONCURRENT_STREAMS (the
	// first SETTINGS frame is kept in rawClient.settings, not returned).
	MaxStreams uint32
}

// next returns the next frame other than SETTINGS, WINDOW_UPDATE and PING,
// or the read error.
func (c *rawClient) next() (frame, error) {
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		f, err := c.fr.ReadFrame()
		if err != nil {
			return frame{}, err
		}
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if f.IsAck() {
				continue
			}
			_ = c.fr.WriteSettingsAck()
			if c.settings != nil {
				v, _ := f.Value(http2.SettingMaxConcurrentStreams)
				return frame{Type: "SETTINGS", MaxStreams: v}, nil
			}
			c.settings = map[http2.SettingID]uint32{}
			_ = f.ForeachSetting(func(s http2.Setting) error {
				c.settings[s.ID] = s.Val
				return nil
			})
		case *http2.WindowUpdateFrame, *http2.PingFrame:
		case *http2.MetaHeadersFrame:
			return frame{Type: "HEADERS", StreamID: f.StreamID, Status: f.PseudoValue("status"), End: f.StreamEnded()}, nil
		case *http2.DataFrame:
			return frame{Type: "DATA", StreamID: f.StreamID, Data: string(f.Data()), End: f.StreamEnded()}, nil
		case *http2.RSTStreamFrame:
			return frame{Type: "RST_STREAM", StreamID: f.StreamID, Code: ErrCode(f.ErrCode)}, nil
		case *http2.GoAwayFrame:
			return frame{Type: "GOAWAY", LastID: f.LastStreamID, Code: ErrCode(f.ErrCode)}, nil
		default:
			return frame{Type: f.Header().Type.String(), StreamID: f.Header().StreamID}, nil
		}
	}
}

// expect reads the next frames and compares them with want.
func (c *rawClient) expect(want ...frame) {
	c.t.Helper()
	var got []frame
	for range want {
		f, err := c.next()
		if err != nil {
			c.t.Fatalf("after %v: read: %v", got, err)
		}
		got = append(got, f)
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		c.t.Fatalf("frames (-want +got):\n%s", diff)
	}
}

// expectEOF checks that the server ends the connection.
func (c *rawClient) expectEOF() {
	c.t.Helper()
	f, err := c.next()
	if err == nil {
		c.t.Fatalf("got frame %+v, want the connection to end", f)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		c.t.Fatalf("the connection stayed open: %v", err)
	}
}

// TestLoopbackH2 covers plain HTTP/2 service through net/http: one
// connection, streams in order, bodies both ways under flow control.
func TestLoopbackH2(t *testing.T) {
	big := bytes.Repeat([]byte("0123456789abcdef"), 1<<18) // 4 MiB
	srv := NewLoopbackServer(t, ServerConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Header().Set("Content-Length", strconv.Itoa(len(big)))
			_, _ = w.Write(big)
		case "/echo-hash":
			h := sha256.New()
			n, _ := io.Copy(h, r.Body)
			_, _ = io.WriteString(w, strconv.FormatInt(n, 10)+" "+hex.EncodeToString(h.Sum(nil)))
		default:
			_, _ = io.WriteString(w, "hello")
		}
	})})
	tr := newTransport(t, false, true)

	for i := range 3 {
		status, proto, body, err := get(t, tr, srv.URL()+"/p"+strconv.Itoa(i))
		if err != nil || status != http.StatusOK || proto != 2 || body != "hello" {
			t.Fatalf("GET %d: %d HTTP/%d %q %v", i, status, proto, body, err)
		}
	}
	status, _, body, err := get(t, tr, srv.URL()+"/big")
	if err != nil || status != http.StatusOK || body != string(big) {
		t.Fatalf("GET /big: %d, %d bytes, %v", status, len(body), err)
	}

	upload := bytes.Repeat([]byte{'u'}, 8<<20)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL()+"/echo-hash", bytes.NewReader(upload))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	sum := sha256.Sum256(upload)
	if want := strconv.Itoa(len(upload)) + " " + hex.EncodeToString(sum[:]); string(got) != want {
		t.Fatalf("upload echo = %q, want %q", got, want)
	}

	if srv.Accepts() != 1 {
		t.Errorf("Accepts() = %d, want 1", srv.Accepts())
	}
	if diff := gocmp.Diff([]ConnInfo{{Index: 0, Protocol: "h2"}}, srv.Conns()); diff != "" {
		t.Errorf("Conns() (-want +got):\n%s", diff)
	}
	var ids []uint32
	var paths []string
	for _, r := range srv.Requests() {
		ids = append(ids, r.StreamID)
		paths = append(paths, r.Path)
		if r.Proto != "HTTP/2.0" || r.Authority != srv.Addr() || r.Action != ActionServe || r.Dropped {
			t.Errorf("request %+v", r)
		}
	}
	if diff := gocmp.Diff([]uint32{1, 3, 5, 7, 9}, ids); diff != "" {
		t.Errorf("stream IDs (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff([]string{"/p0", "/p1", "/p2", "/big", "/echo-hash"}, paths); diff != "" {
		t.Errorf("paths (-want +got):\n%s", diff)
	}
}

// TestLoopbackStreamLimit checks the MAX_CONCURRENT_STREAMS knob: a strict
// net/http client stays under it on one connection, and a raw client that
// exceeds it is refused.
func TestLoopbackStreamLimit(t *testing.T) {
	t.Run("success: a strict client fills the limit on one connection", func(t *testing.T) {
		// Exactly the limit, never more: with StrictMaxConcurrentRequests,
		// net/http (go1.26 and go1.27.1) deadlocks when more callers than
		// the server's limit arrive at once, because every queued caller's
		// reservation counts against the slot the first one waits for.
		const limit = 4
		var arrived sync.WaitGroup
		arrived.Add(limit)
		srv := NewLoopbackServer(t, ServerConfig{
			MaxConcurrentStreams: limit,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				arrived.Done()
				arrived.Wait() // every stream is open at once before any answers
				w.WriteHeader(http.StatusNoContent)
			}),
		})
		tr := newTransport(t, false, true)
		tr.MaxConnsPerHost = 1
		c := dialRaw(t, srv.Addr())
		if got := c.serverSettings()[http2.SettingMaxConcurrentStreams]; got != limit {
			t.Fatalf("SETTINGS_MAX_CONCURRENT_STREAMS = %d, want %d", got, limit)
		}
		var wg sync.WaitGroup
		for range limit {
			wg.Go(func() {
				if status, _, _, err := get(t, tr, srv.URL()); err != nil || status != http.StatusNoContent {
					t.Errorf("GET: %d %v", status, err)
				}
			})
		}
		wg.Wait()
		// The raw client is connection 0, net/http's connection 1.
		if srv.Accepts() != 2 || srv.OverLimit() != 0 || srv.MaxActiveStreams() != limit {
			t.Errorf("Accepts %d, OverLimit %d, MaxActiveStreams %d; want 2, 0, %d", srv.Accepts(), srv.OverLimit(), srv.MaxActiveStreams(), limit)
		}
	})

	t.Run("success: a stream over the limit is refused", func(t *testing.T) {
		srv := NewLoopbackServer(t, ServerConfig{
			MaxConcurrentStreams: 2,
			OnStream:             func(*Stream) Action { return ActionHold },
		})
		c := dialRaw(t, srv.Addr())
		for _, id := range []uint32{1, 3, 5} {
			c.request(id, "/", true)
		}
		c.expect(frame{Type: "RST_STREAM", StreamID: 5, Code: CodeRefusedStream})
		waitFor(t, "three recorded requests", func() bool { return len(srv.Requests()) == 3 })
		if diff := gocmp.Diff([]Action{ActionHold, ActionHold, ActionRefuse}, actions(srv)); diff != "" {
			t.Errorf("actions (-want +got):\n%s", diff)
		}
		if srv.OverLimit() != 1 || srv.MaxActiveStreams() != 2 {
			t.Errorf("OverLimit %d, MaxActiveStreams %d; want 1, 2", srv.OverLimit(), srv.MaxActiveStreams())
		}
	})

	t.Run("success: a stream stops counting before the client reads its last frame", func(t *testing.T) {
		// A client that reads END_STREAM (or RST_STREAM) and opens its next
		// stream at once must find the slot free, as with net/http's server:
		// the count drops under the write lock before the frame leaves. Had it
		// dropped after the write, the stream would still be counted here for
		// as long as the handler goroutine took to finish it.
		srv := NewLoopbackServer(t, ServerConfig{
			MaxConcurrentStreams: 1,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/upload" {
					w.WriteHeader(http.StatusNoContent) // answered before the body: RST_STREAM(NO_ERROR) follows
				}
			}),
		})
		c := dialRaw(t, srv.Addr())
		c.serverSettings()
		conn := func() *H2Conn {
			cs := srv.LiveH2Conns()
			if len(cs) != 1 {
				t.Fatalf("live connections %d, want 1", len(cs))
			}
			return cs[0]
		}
		const n = 200
		for i := range n {
			id := uint32(2*i + 1)
			c.request(id, "/", true)
			c.expect(frame{Type: "HEADERS", StreamID: id, Status: "200", End: true})
			if open := conn().ActiveStreams(); len(open) != 0 {
				t.Fatalf("stream %d: open streams %v after the client read END_STREAM, want none", id, open)
			}
		}
		id := uint32(2*n + 1)
		c.request(id, "/upload", false)
		c.expect(frame{Type: "HEADERS", StreamID: id, Status: "204", End: true}, frame{Type: "RST_STREAM", StreamID: id, Code: CodeNoError})
		if open := conn().ActiveStreams(); len(open) != 0 {
			t.Errorf("open streams %v after the client read RST_STREAM, want none", open)
		}
		if srv.OverLimit() != 0 || srv.MaxActiveStreams() != 1 {
			t.Errorf("OverLimit %d, MaxActiveStreams %d; want 0 and 1", srv.OverLimit(), srv.MaxActiveStreams())
		}
	})

	t.Run("success: a limit lowered mid-connection is announced and enforced", func(t *testing.T) {
		srv := NewLoopbackServer(t, ServerConfig{
			MaxConcurrentStreams: 8,
			OnStream:             func(*Stream) Action { return ActionHold },
		})
		c := dialRaw(t, srv.Addr())
		if got := c.serverSettings()[http2.SettingMaxConcurrentStreams]; got != 8 {
			t.Fatalf("first SETTINGS_MAX_CONCURRENT_STREAMS = %d, want 8", got)
		}
		c.request(1, "/a", true)
		c.request(3, "/b", true)
		waitFor(t, "two held streams", func() bool {
			cs := srv.LiveH2Conns()
			return len(cs) == 1 && len(cs[0].ActiveStreams()) == 2
		})
		conn := srv.LiveH2Conns()[0]
		if err := conn.SetMaxConcurrentStreams(0); err == nil {
			t.Error("SetMaxConcurrentStreams(0) = nil, want an error")
		}
		if err := conn.SetMaxConcurrentStreams(2); err != nil {
			t.Fatal(err)
		}
		c.expect(frame{Type: "SETTINGS", MaxStreams: 2})
		c.request(5, "/c", true) // a third stream against the new limit of 2
		c.expect(frame{Type: "RST_STREAM", StreamID: 5, Code: CodeRefusedStream})
		if err := conn.SetMaxConcurrentStreams(3); err != nil {
			t.Fatal(err)
		}
		c.expect(frame{Type: "SETTINGS", MaxStreams: 3})
		c.request(7, "/d", true) // raised again: the third stream is held
		waitFor(t, "three held streams", func() bool { return len(conn.ActiveStreams()) == 3 })
		if diff := gocmp.Diff([]Action{ActionHold, ActionHold, ActionRefuse, ActionHold}, actions(srv)); diff != "" {
			t.Errorf("actions (-want +got):\n%s", diff)
		}
		if srv.OverLimit() != 1 || srv.MaxActiveStreams() != 3 {
			t.Errorf("OverLimit %d, MaxActiveStreams %d; want 1, 3", srv.OverLimit(), srv.MaxActiveStreams())
		}
	})
}

// TestLoopbackGoAway checks GOAWAY with a LastStreamID below streams in
// flight, frame by frame, and the replay it causes in net/http.
func TestLoopbackGoAway(t *testing.T) {
	t.Run("success: streams above LastStreamID are dropped, the rest finish", func(t *testing.T) {
		release := make(chan struct{})
		srv := NewLoopbackServer(t, ServerConfig{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, "one")
			}),
			OnStream: func(s *Stream) Action {
				if s.ID == 1 {
					return ActionServe
				}
				return ActionHold
			},
		})
		c := dialRaw(t, srv.Addr())
		for _, id := range []uint32{1, 3, 5} {
			c.request(id, "/", true)
		}
		waitFor(t, "three open streams", func() bool {
			conns := srv.LiveH2Conns()
			return len(conns) == 1 && len(conns[0].ActiveStreams()) == 3
		})
		conn := srv.LiveH2Conns()[0]
		if err := conn.GoAway(1, CodeNoError); err != nil {
			t.Fatal(err)
		}
		c.expect(frame{Type: "GOAWAY", LastID: 1, Code: CodeNoError})
		if diff := gocmp.Diff([]uint32{1}, conn.ActiveStreams()); diff != "" {
			t.Errorf("active streams after GOAWAY (-want +got):\n%s", diff)
		}
		c.request(7, "/", true) // above LastStreamID: ignored
		close(release)
		c.expect(
			frame{Type: "HEADERS", StreamID: 1, Status: "200"},
			frame{Type: "DATA", StreamID: 1, Data: "one"},
			frame{Type: "DATA", StreamID: 1, End: true},
		)
		c.expectEOF()
		if n := len(srv.Requests()); n != 3 {
			t.Errorf("%d requests recorded, want 3 (stream 7 arrived after GOAWAY)", n)
		}
		if diff := gocmp.Diff([]bool{false, true, true}, dropped(srv)); diff != "" {
			t.Errorf("Dropped flags of streams 1, 3, 5 (-want +got):\n%s", diff)
		}
	})

	t.Run("success: a stream dropped after its handler started shows Dropped", func(t *testing.T) {
		started := make(chan struct{})
		writeErr := make(chan error, 1)
		srv := NewLoopbackServer(t, ServerConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			_, err := io.WriteString(w, "too late")
			writeErr <- err
		})})
		c := dialRaw(t, srv.Addr())
		c.request(1, "/", true)
		recv(t, started, "the handler to start")
		// GoAway takes any code; ENHANCE_YOUR_CALM is not one of the named ones.
		const enhanceYourCalm = ErrCode(0xb)
		if err := srv.LiveH2Conns()[0].GoAway(0, enhanceYourCalm); err != nil {
			t.Fatal(err)
		}
		c.expect(frame{Type: "GOAWAY", LastID: 0, Code: enhanceYourCalm})
		if err := recv(t, writeErr, "the handler's write"); !errors.Is(err, errStreamReset) {
			t.Errorf("the dropped handler's Write returned %v, want %v", err, errStreamReset)
		}
		c.expectEOF()
		reqs := srv.Requests()
		if len(reqs) != 1 || reqs[0].Action != ActionServe || !reqs[0].Dropped {
			t.Errorf("requests %+v, want one with ActionServe and Dropped", reqs)
		}
	})

	t.Run("success: a stream GOAWAY overtakes in OnStream never runs and shows Dropped", func(t *testing.T) {
		const enhanceYourCalm = ErrCode(0xb)
		var ran atomic.Int64
		srv := NewLoopbackServer(t, ServerConfig{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				ran.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}),
			OnStream: func(s *Stream) Action {
				// GOAWAY below this stream after onHeaders recorded it, then
				// ask for it to be served: open() finds it overtaken.
				if err := s.Conn.GoAway(max(s.ID, 2)-2, enhanceYourCalm); err != nil {
					t.Errorf("GoAway: %v", err)
				}
				return ActionServe
			},
		})
		c := dialRaw(t, srv.Addr())
		c.request(1, "/", true)
		c.expect(frame{Type: "GOAWAY", LastID: 0, Code: enhanceYourCalm})
		c.expectEOF()
		// Close waits for the connection's reader goroutine, which runs
		// OnStream and open(): every record is final after it.
		srv.Close()
		if n := ran.Load(); n != 0 {
			t.Errorf("the handler ran %d times, want 0", n)
		}
		reqs := srv.Requests()
		if len(reqs) != 1 || reqs[0].StreamID != 1 || reqs[0].Action != ActionServe || !reqs[0].Dropped {
			t.Errorf("requests %+v, want stream 1 with ActionServe and Dropped", reqs)
		}
	})

	t.Run("success: ActionGoAway puts the stream itself above LastStreamID", func(t *testing.T) {
		srv := NewLoopbackServer(t, ServerConfig{
			OnStream: func(s *Stream) Action {
				if s.ID == 3 {
					return ActionGoAway
				}
				return ActionServe
			},
		})
		c := dialRaw(t, srv.Addr())
		c.request(1, "/", true)
		c.expect(frame{Type: "HEADERS", StreamID: 1, Status: "200", End: true})
		c.request(3, "/", true)
		c.expect(frame{Type: "GOAWAY", LastID: 1, Code: CodeNoError})
		c.expectEOF()
	})

	t.Run("success: net/http replays a request GOAWAY left unprocessed", func(t *testing.T) {
		srv := NewLoopbackServer(t, ServerConfig{
			OnStream: func(s *Stream) Action {
				if s.Conn.Index() == 0 {
					return ActionGoAway // LastStreamID 0: the first request was never processed
				}
				return ActionServe
			},
		})
		status, proto, _, err := get(t, newTransport(t, false, true), srv.URL())
		if err != nil || status != http.StatusOK || proto != 2 {
			t.Fatalf("GET: %d HTTP/%d %v", status, proto, err)
		}
		if srv.Accepts() != 2 {
			t.Errorf("Accepts() = %d, want 2", srv.Accepts())
		}
		var seen []int
		for _, r := range srv.Requests() {
			seen = append(seen, r.Conn)
		}
		if diff := gocmp.Diff([]int{0, 1}, seen); diff != "" {
			t.Errorf("the request was seen on connections (-want +got):\n%s", diff)
		}
		if diff := gocmp.Diff([]Action{ActionGoAway, ActionServe}, actions(srv)); diff != "" {
			t.Errorf("actions (-want +got):\n%s", diff)
		}
	})
}

// TestLoopbackRefuseCloseHold covers the per-stream knobs.
func TestLoopbackRefuseCloseHold(t *testing.T) {
	t.Run("success: ActionRefuse sends REFUSED_STREAM", func(t *testing.T) {
		srv := NewLoopbackServer(t, ServerConfig{OnStream: func(s *Stream) Action {
			if s.Seq == 0 {
				return ActionRefuse
			}
			return ActionServe
		}})
		c := dialRaw(t, srv.Addr())
		c.request(1, "/", true)
		c.expect(frame{Type: "RST_STREAM", StreamID: 1, Code: CodeRefusedStream})
		c.request(3, "/", true)
		c.expect(frame{Type: "HEADERS", StreamID: 3, Status: "200", End: true})
	})

	t.Run("success: net/http retries a refused stream", func(t *testing.T) {
		srv := NewLoopbackServer(t, ServerConfig{OnStream: func(s *Stream) Action {
			if s.Seq == 0 {
				return ActionRefuse
			}
			return ActionServe
		}})
		status, _, _, err := get(t, newTransport(t, false, true), srv.URL())
		if err != nil || status != http.StatusOK {
			t.Fatalf("GET: %d %v", status, err)
		}
		if diff := gocmp.Diff([]Action{ActionRefuse, ActionServe}, actions(srv)); diff != "" {
			t.Errorf("actions (-want +got):\n%s", diff)
		}
	})

	t.Run("success: ActionClose closes the connection without GOAWAY", func(t *testing.T) {
		srv := NewLoopbackServer(t, ServerConfig{OnStream: func(*Stream) Action { return ActionClose }})
		c := dialRaw(t, srv.Addr())
		c.request(1, "/", true)
		c.expectEOF()
		if diff := gocmp.Diff([]Action{ActionClose}, actions(srv)); diff != "" {
			t.Errorf("actions (-want +got):\n%s", diff)
		}
	})

	t.Run("success: CloseConns closes an idle connection", func(t *testing.T) {
		srv := NewLoopbackServer(t, ServerConfig{})
		c := dialRaw(t, srv.Addr())
		c.request(1, "/", true)
		c.expect(frame{Type: "HEADERS", StreamID: 1, Status: "200", End: true})
		srv.CloseConns()
		c.expectEOF()
	})

	t.Run("success: a held stream stays open until the client resets it", func(t *testing.T) {
		srv := NewLoopbackServer(t, ServerConfig{OnStream: func(*Stream) Action { return ActionHold }})
		c := dialRaw(t, srv.Addr())
		c.request(1, "/", true)
		waitFor(t, "a held stream", func() bool {
			conns := srv.LiveH2Conns()
			return len(conns) == 1 && len(conns[0].ActiveStreams()) == 1
		})
		if err := c.fr.WriteRSTStream(1, http2.ErrCodeCancel); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the reset stream to close", func() bool { return len(srv.LiveH2Conns()[0].ActiveStreams()) == 0 })
		if reqs := srv.Requests(); reqs[0].Action != ActionHold || !reqs[0].Dropped {
			t.Errorf("request %+v, want ActionHold and Dropped", reqs[0])
		}
	})

	t.Run("success: a handler panic resets the stream with INTERNAL_ERROR", func(t *testing.T) {
		srv := NewLoopbackServer(t, ServerConfig{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic(http.ErrAbortHandler)
		})})
		c := dialRaw(t, srv.Addr())
		c.request(1, "/", true)
		c.expect(frame{Type: "RST_STREAM", StreamID: 1, Code: CodeInternalError})
		if diff := gocmp.Diff([]bool{false}, dropped(srv)); diff != "" {
			t.Errorf("Dropped flags (-want +got):\n%s", diff)
		}
	})
}

// TestLoopbackConnEnd checks how the client sees each way the server ends a
// connection: Close and ActionClose send close_notify and FIN (io.EOF),
// Reset and ActionReset a TCP reset.
func TestLoopbackConnEnd(t *testing.T) {
	tests := map[string]struct {
		action    Action        // what OnStream chooses for stream 1
		end       func(*H2Conn) // when set, ends the connection after stream 1 is served
		wantReset bool
	}{
		"success: ActionClose ends with close_notify": {action: ActionClose},
		"success: ActionReset ends with a TCP reset":  {action: ActionReset, wantReset: true},
		"success: Close ends an idle connection":      {action: ActionServe, end: (*H2Conn).Close},
		"success: Reset ends an idle connection":      {action: ActionServe, end: (*H2Conn).Reset, wantReset: true},
		"success: CloseConns ends an idle connection": {action: ActionServe, end: func(c *H2Conn) { c.srv.CloseConns() }},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := NewLoopbackServer(t, ServerConfig{OnStream: func(*Stream) Action { return tt.action }})
			c := dialRaw(t, srv.Addr())
			// Take and acknowledge the server's SETTINGS before the request, so
			// the client writes nothing after it: data the server has not read
			// when it closes would make the kernel send a reset instead of FIN.
			c.serverSettings()
			c.request(1, "/", true)
			if tt.end != nil {
				c.expect(frame{Type: "HEADERS", StreamID: 1, Status: "200", End: true})
				tt.end(srv.LiveH2Conns()[0])
			}
			_, err := c.next()
			if tt.wantReset {
				if !isConnReset(err) {
					t.Fatalf("read after the end: %v, want a connection reset", err)
				}
				return
			}
			if !errors.Is(err, io.EOF) || isConnReset(err) {
				t.Fatalf("read after the end: %v, want io.EOF", err)
			}
		})
	}
}

// TestLoopbackRequestBody checks what a handler sees of a request body over
// HTTP/2: the length net/http's servers report, and the end of a body that
// the client ends with trailers.
func TestLoopbackRequestBody(t *testing.T) {
	// requestView is what the handler saw; it goes to the test over a channel
	// rather than back in the response.
	type requestView struct {
		ContentLength int64
		NoBody        bool
		Body          string
	}
	seen := make(chan requestView, 1)
	srv := NewLoopbackServer(t, ServerConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		seen <- requestView{ContentLength: r.ContentLength, NoBody: r.Body == http.NoBody, Body: string(b)}
		w.WriteHeader(http.StatusNoContent)
	})})
	tests := map[string]struct {
		h1, h2 bool
		method string
		body   io.Reader // nil sends http.NoBody
		want   requestView
	}{
		"success: a GET over HTTP/2 has length 0 and no body": {
			h2: true, method: http.MethodGet, want: requestView{ContentLength: 0, NoBody: true},
		},
		"success: a GET over HTTP/1.1 has length 0 and no body": {
			h1: true, method: http.MethodGet, want: requestView{ContentLength: 0, NoBody: true},
		},
		"success: a POST of a known length over HTTP/2": {
			h2: true, method: http.MethodPost, body: strings.NewReader("abc"), want: requestView{ContentLength: 3, Body: "abc"},
		},
		"success: a POST of an unknown length over HTTP/2": {
			h2: true, method: http.MethodPost, body: io.MultiReader(strings.NewReader("abc")), want: requestView{ContentLength: -1, Body: "abc"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := tt.body
			if body == nil {
				body = http.NoBody
			}
			req, err := http.NewRequestWithContext(t.Context(), tt.method, srv.URL(), body)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := newTransport(t, tt.h1, tt.h2).RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status %d, want 204", resp.StatusCode)
			}
			if diff := gocmp.Diff(tt.want, recv(t, seen, "the handler's view")); diff != "" {
				t.Errorf("the handler saw (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("success: END_STREAM on the trailers ends the body", func(t *testing.T) {
		c := dialRaw(t, srv.Addr())
		c.request(1, "/", false)
		if err := c.fr.WriteData(1, false, []byte("abc")); err != nil {
			t.Fatal(err)
		}
		c.trailers(1)
		c.expect(frame{Type: "HEADERS", StreamID: 1, Status: "204", End: true})
		if diff := gocmp.Diff(requestView{ContentLength: -1, Body: "abc"}, recv(t, seen, "the handler's view")); diff != "" {
			t.Errorf("the handler saw (-want +got):\n%s", diff)
		}
	})
}

// TestLoopbackResponses covers response shapes the SDK's size-cap and
// failure tests need.
func TestLoopbackResponses(t *testing.T) {
	srv := NewLoopbackServer(t, ServerConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/declared-16MiB":
			w.Header().Set("Content-Length", strconv.Itoa(16<<20))
			_, _ = io.WriteString(w, "0123456789")
		case "/undeclared":
			_, _ = w.Write(make([]byte, 1<<20))
		case "/abort":
			_, _ = io.WriteString(w, "partial")
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		case "/forbidden-early":
			w.WriteHeader(http.StatusForbidden)
		}
	})})
	tr := newTransport(t, false, true)
	tests := map[string]struct {
		method, path string
		body         io.Reader
		wantStatus   int
		wantLength   int64
		wantReadErr  bool
	}{
		"success: undeclared length": {method: http.MethodGet, path: "/undeclared", wantStatus: 200, wantLength: -1},
		"error: declared length above the body": {
			method: http.MethodGet, path: "/declared-16MiB", wantStatus: 200, wantLength: 16 << 20, wantReadErr: true,
		},
		"error: stream reset after a partial body": {method: http.MethodGet, path: "/abort", wantStatus: 200, wantLength: -1, wantReadErr: true},
		"success: 403 before the upload is read": {
			method: http.MethodPost, path: "/forbidden-early", body: bytes.NewReader(make([]byte, 16<<20)), wantStatus: 403, wantLength: 0,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := tt.body
			if body == nil {
				body = http.NoBody
			}
			req, err := http.NewRequestWithContext(t.Context(), tt.method, srv.URL()+tt.path, body)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := tr.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			defer resp.Body.Close()
			_, err = io.Copy(io.Discard, resp.Body)
			if resp.StatusCode != tt.wantStatus || resp.ContentLength != tt.wantLength || (err != nil) != tt.wantReadErr {
				t.Errorf("status %d, length %d, read error %v; want %d, %d, error %v",
					resp.StatusCode, resp.ContentLength, err, tt.wantStatus, tt.wantLength, tt.wantReadErr)
			}
		})
	}
}

// TestLoopbackALPNModes covers the ALPN knobs at the TLS layer and through
// net/http.
func TestLoopbackALPNModes(t *testing.T) {
	handshake := func(t *testing.T, addr string, offer ...string) (string, error) {
		cfg := ClientTLSConfig(t)
		cfg.NextProtos = offer
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, cfg)
		if err != nil {
			return "", err
		}
		defer conn.Close()
		return conn.ConnectionState().NegotiatedProtocol, nil
	}
	tests := map[string]struct {
		mode      ALPN
		offer     []string
		wantProto string
		wantErr   string
		h1, h2    bool // net/http client protocols
		wantMajor int
	}{
		"success: h2 mode negotiates h2":             {mode: ALPNH2, offer: []string{"h2"}, wantProto: "h2", h1: false, h2: true, wantMajor: 2},
		"success: h2 mode serves an HTTP/1.1 client": {mode: ALPNH2, offer: []string{"http/1.1"}, wantProto: "http/1.1", h1: true, h2: false, wantMajor: 1},
		"success: no-ALPN mode negotiates nothing":   {mode: ALPNNone, offer: []string{"h2"}, wantProto: "", h1: true, h2: false, wantMajor: 1},
		"error: http/1.1-only mode refuses h2 alone": {mode: ALPNHTTP1Only, offer: []string{"h2"}, wantErr: "no application protocol"},
		"success: http/1.1-only mode with fallback":  {mode: ALPNHTTP1Only, offer: []string{"h2", "http/1.1"}, wantProto: "http/1.1", h1: true, h2: true, wantMajor: 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := NewLoopbackServer(t, ServerConfig{ALPN: tt.mode})
			proto, err := handshake(t, srv.Addr(), tt.offer...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("handshake error = %v, want %q", err, tt.wantErr)
				}
				waitFor(t, "the server-side handshake error", func() bool {
					conns := srv.Conns()
					return len(conns) == 1 && conns[0].HandshakeErr != ""
				})
				return
			}
			if err != nil || proto != tt.wantProto {
				t.Fatalf("handshake = %q, %v; want %q", proto, err, tt.wantProto)
			}
			status, major, _, err := get(t, newTransport(t, tt.h1, tt.h2), srv.URL())
			if err != nil || status != http.StatusOK || major != tt.wantMajor {
				t.Fatalf("GET: %d HTTP/%d %v; want 200 HTTP/%d", status, major, err, tt.wantMajor)
			}
			reqs := srv.Requests()
			if len(reqs) != 1 || reqs[0].Conn != 1 || reqs[0].Proto != "HTTP/"+map[int]string{1: "1.1", 2: "2.0"}[tt.wantMajor] {
				t.Errorf("requests %+v", reqs)
			}
		})
	}
}

// TestActionString covers the names used in failure messages.
func TestActionString(t *testing.T) {
	tests := map[string]struct {
		a    Action
		want string
	}{
		"success: serve":   {a: ActionServe, want: "serve"},
		"success: refuse":  {a: ActionRefuse, want: "refuse"},
		"success: goaway":  {a: ActionGoAway, want: "goaway"},
		"success: close":   {a: ActionClose, want: "close"},
		"success: hold":    {a: ActionHold, want: "hold"},
		"success: reset":   {a: ActionReset, want: "reset"},
		"success: unknown": {a: Action(42), want: "action(42)"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.a.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
