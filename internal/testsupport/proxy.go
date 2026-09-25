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
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ProxyMode selects how a [Proxy] is reached and what its TLS layer offers.
type ProxyMode int

const (
	// ProxyPlain is a plain TCP proxy ("http://" proxy URL).
	ProxyPlain ProxyMode = iota
	// ProxyTLSLenient is a TLS proxy without ALPN ("https://" proxy URL): it
	// accepts any client offer and negotiates nothing, so an h2-only client
	// hop completes.
	ProxyTLSLenient
	// ProxyTLSStrict is a TLS proxy that offers http/1.1 alone: a client that
	// offers only h2 on the proxy hop is refused with alert 120
	// (no_application_protocol).
	ProxyTLSStrict
	// ProxyTLSOfferH2 is a TLS proxy that offers h2 and http/1.1, so an
	// h2-offering client negotiates h2 on the proxy hop, although the proxy
	// still expects an HTTP/1.1 CONNECT (the case the plan records under
	// K16).
	ProxyTLSOfferH2
)

// nextProtos returns the proxy's ALPN list (nil for a plain or lenient
// proxy).
func (m ProxyMode) nextProtos() []string {
	switch m {
	case ProxyTLSStrict:
		return []string{"http/1.1"}
	case ProxyTLSOfferH2:
		return []string{"h2", "http/1.1"}
	default:
		return nil
	}
}

// ProxyConnect is one CONNECT request a [Proxy] handled.
type ProxyConnect struct {
	// Conn is the index of the proxy connection it arrived on.
	Conn int
	// Target is the requested authority, such as "example.com:443".
	Target string
	// Status is the status the proxy answered: 200 when the tunnel opened,
	// 502 when the target had no route or could not be dialed, 405 for a
	// method other than CONNECT.
	Status int
}

// Proxy is an HTTP/1.1 CONNECT proxy on 127.0.0.1. It dials the requested
// authority through its [Routes] (never the network) and tunnels bytes both
// ways. Clients select it with http.ProxyURL(p.URL()); a TLS proxy trusts
// the package certificate like the [LoopbackServer]. Close runs from
// tb.Cleanup.
type Proxy struct {
	tb     testing.TB
	mode   ProxyMode
	routes Routes
	ln     net.Listener
	tls    *tls.Config
	wg     sync.WaitGroup

	accepts atomic.Int64

	mu       sync.Mutex
	closed   bool
	conns    []ConnInfo
	open     map[net.Conn]struct{}
	connects []ProxyConnect
}

// NewProxy starts a proxy on 127.0.0.1 with a free port.
func NewProxy(tb testing.TB, mode ProxyMode, routes Routes) *Proxy {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("testsupport: listen: %v", err)
	}
	p := &Proxy{tb: tb, mode: mode, routes: routes, ln: ln, open: make(map[net.Conn]struct{})}
	if mode != ProxyPlain {
		p.tls = serverTLSConfig(mustCert(tb), mode.nextProtos())
	}
	p.wg.Go(p.acceptLoop)
	tb.Cleanup(p.Close)
	return p
}

// Addr returns the proxy's address, 127.0.0.1:port.
func (p *Proxy) Addr() string { return p.ln.Addr().String() }

// URL returns the proxy URL: http://127.0.0.1:port for ProxyPlain,
// https://127.0.0.1:port otherwise.
func (p *Proxy) URL() *url.URL {
	scheme := "https"
	if p.mode == ProxyPlain {
		scheme = "http"
	}
	return &url.URL{Scheme: scheme, Host: p.Addr()}
}

// Accepts returns the number of TCP connections accepted so far.
func (p *Proxy) Accepts() int { return int(p.accepts.Load()) }

// Conns returns what is known of every accepted connection: for a TLS proxy
// the protocol its handshake negotiated or the handshake error.
func (p *Proxy) Conns() []ConnInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.conns)
}

// Connects returns every request handled so far, in arrival order.
func (p *Proxy) Connects() []ProxyConnect {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.connects)
}

// Close stops the listener, closes every connection and tunnel, and waits
// for the proxy's goroutines. It is idempotent.
func (p *Proxy) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	conns := make([]net.Conn, 0, len(p.open))
	for c := range p.open {
		conns = append(conns, c)
	}
	p.mu.Unlock()
	_ = p.ln.Close()
	for _, c := range conns {
		_ = c.Close()
	}
	waitGroupTimeout(p.tb, &p.wg, "Proxy", 10*time.Second)
}

// acceptLoop accepts connections until the listener closes.
func (p *Proxy) acceptLoop() {
	for {
		nc, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.accepts.Add(1)
		p.mu.Lock()
		idx := len(p.conns)
		p.conns = append(p.conns, ConnInfo{Index: idx})
		p.mu.Unlock()
		if !p.track(nc) {
			return
		}
		p.wg.Go(func() { p.serve(nc, idx) })
	}
}

// track registers an open connection; it reports false (and closes c) when
// the proxy is closing.
func (p *Proxy) track(c net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		_ = c.Close()
		return false
	}
	p.open[c] = struct{}{}
	return true
}

// untrack forgets and closes a connection.
func (p *Proxy) untrack(c net.Conn) {
	p.mu.Lock()
	delete(p.open, c)
	p.mu.Unlock()
	_ = c.Close()
}

// serve handles one client connection: the TLS handshake when the proxy has
// one, then a single CONNECT request and the tunnel.
func (p *Proxy) serve(nc net.Conn, idx int) {
	defer p.untrack(nc)
	conn := nc
	if p.tls != nil {
		tc := tls.Server(nc, p.tls)
		_ = tc.SetDeadline(time.Now().Add(10 * time.Second))
		err := tc.Handshake()
		_ = tc.SetDeadline(time.Time{})
		p.mu.Lock()
		p.conns[idx].Protocol = tc.ConnectionState().NegotiatedProtocol
		if err != nil {
			p.conns[idx].HandshakeErr = err.Error()
		}
		p.mu.Unlock()
		if err != nil {
			return
		}
		conn = tc
	}
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	req, err := http.ReadRequest(br)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return
	}
	target := req.Host
	if req.Method != http.MethodConnect {
		p.answer(conn, idx, target, http.StatusMethodNotAllowed)
		return
	}
	to, err := p.routes.Resolve(target)
	if err != nil {
		p.answer(conn, idx, target, http.StatusBadGateway)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	var d net.Dialer
	upstream, err := d.DialContext(ctx, "tcp", to)
	cancel()
	if err != nil {
		p.answer(conn, idx, target, http.StatusBadGateway)
		return
	}
	if !p.track(upstream) {
		return
	}
	defer p.untrack(upstream)
	p.answer(conn, idx, target, http.StatusOK)

	done := make(chan struct{}, 2)
	p.wg.Go(func() {
		_, _ = io.Copy(upstream, br) // br first returns what it already buffered
		done <- struct{}{}
	})
	p.wg.Go(func() {
		_, _ = io.Copy(conn, upstream)
		done <- struct{}{}
	})
	<-done
	_ = conn.Close()
	_ = upstream.Close()
	<-done
}

// answer records a CONNECT outcome and writes its status line.
func (p *Proxy) answer(conn net.Conn, idx int, target string, status int) {
	p.mu.Lock()
	p.connects = append(p.connects, ProxyConnect{Conn: idx, Target: target, Status: status})
	p.mu.Unlock()
	var line string
	switch status {
	case http.StatusOK:
		line = "HTTP/1.1 200 Connection established\r\n\r\n"
	case http.StatusBadGateway:
		line = "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
	default:
		line = "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
	}
	_, _ = io.WriteString(conn, line)
}
