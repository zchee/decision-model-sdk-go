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
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
)

// TestProxyModes sends a request for https://example.com/ through each proxy
// mode to a loopback HTTP/2 server, with example.com routed by the proxy.
func TestProxyModes(t *testing.T) {
	tests := map[string]struct {
		mode        ProxyMode
		h1, h2      bool // net/http client protocols
		wantErr     string
		wantProto   string // ALPN on the proxy hop
		wantConnect []ProxyConnect
	}{
		"success: plain proxy": {
			mode: ProxyPlain, h2: true,
			wantConnect: []ProxyConnect{{Conn: 0, Target: "example.com:443", Status: 200}},
		},
		"success: TLS proxy with lenient ALPN": {
			mode: ProxyTLSLenient, h2: true, wantProto: "",
			wantConnect: []ProxyConnect{{Conn: 0, Target: "example.com:443", Status: 200}},
		},
		"error: TLS proxy with strict ALPN refuses an h2-only client": {
			mode: ProxyTLSStrict, h2: true, wantErr: "no application protocol",
		},
		"success: TLS proxy with strict ALPN and an HTTP/1.1 fallback": {
			mode: ProxyTLSStrict, h1: true, h2: true, wantProto: "http/1.1",
			wantConnect: []ProxyConnect{{Conn: 0, Target: "example.com:443", Status: 200}},
		},
		"success: TLS proxy offering h2 negotiates h2 on its hop": {
			mode: ProxyTLSOfferH2, h2: true, wantProto: "h2",
			wantConnect: []ProxyConnect{{Conn: 0, Target: "example.com:443", Status: 200}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := NewLoopbackServer(t, ServerConfig{Handler: http.HandlerFunc(answerH2ExampleCom)})
			proxy := NewProxy(t, tt.mode, Routes{"example.com:443": srv.Addr()})
			tr := newTransport(t, tt.h1, tt.h2)
			tr.Proxy = http.ProxyURL(proxy.URL())
			status, _, body, err := get(t, tr, "https://example.com/")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), "proxyconnect") {
					t.Fatalf("GET error = %v, want a proxyconnect error containing %q", err, tt.wantErr)
				}
				// The proxy records its side of the refused handshake after its
				// Handshake returns, which can be after the client read the
				// alert and returned (K29, as in TestLoopbackALPNModes): wait
				// for the record instead of reading it at once.
				waitFor(t, "the proxy's record of the refused handshake", func() bool {
					conns := proxy.Conns()
					return len(conns) == 1 && conns[0].HandshakeErr != ""
				})
				if srv.Accepts() != 0 {
					t.Errorf("the API server accepted %d connections", srv.Accepts())
				}
				return
			}
			if err != nil || status != http.StatusOK || body != "h2 example.com" {
				t.Fatalf("GET: %d %q %v", status, body, err)
			}
			if diff := gocmp.Diff(tt.wantConnect, proxy.Connects()); diff != "" {
				t.Errorf("Connects() (-want +got):\n%s", diff)
			}
			if tt.mode != ProxyPlain {
				if conns := proxy.Conns(); len(conns) != 1 || conns[0].Protocol != tt.wantProto || conns[0].HandshakeErr != "" {
					t.Errorf("proxy conns %+v, want one handshake that negotiated %q", conns, tt.wantProto)
				}
			}
			if proxy.Accepts() != 1 || srv.Accepts() != 1 {
				t.Errorf("proxy accepts %d, server accepts %d; want 1, 1", proxy.Accepts(), srv.Accepts())
			}
		})
	}
}

// TestProxyRefusals covers what the proxy answers besides a tunnel.
func TestProxyRefusals(t *testing.T) {
	tests := map[string]struct {
		request    string
		wantStatus string
		wantTarget string
	}{
		"error: unrouted target": {
			request:    "CONNECT unrouted.test:443 HTTP/1.1\r\nHost: unrouted.test:443\r\n\r\n",
			wantStatus: "HTTP/1.1 502 Bad Gateway", wantTarget: "unrouted.test:443",
		},
		"error: routed target that refuses the dial": {
			request:    "CONNECT closed.test:443 HTTP/1.1\r\nHost: closed.test:443\r\n\r\n",
			wantStatus: "HTTP/1.1 502 Bad Gateway", wantTarget: "closed.test:443",
		},
		"error: not a CONNECT request": {
			request:    "GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n",
			wantStatus: "HTTP/1.1 405 Method Not Allowed", wantTarget: "example.com",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			closed, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			closedAddr := closed.Addr().String()
			_ = closed.Close()
			proxy := NewProxy(t, ProxyPlain, Routes{"closed.test:443": closedAddr})
			conn, err := net.DialTimeout("tcp", proxy.Addr(), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.WriteString(conn, tt.request); err != nil {
				t.Fatal(err)
			}
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(line); got != tt.wantStatus {
				t.Errorf("status line %q, want %q", got, tt.wantStatus)
			}
			waitFor(t, "the recorded request", func() bool { return len(proxy.Connects()) == 1 })
			if got := proxy.Connects()[0].Target; got != tt.wantTarget {
				t.Errorf("target %q, want %q", got, tt.wantTarget)
			}
		})
	}
}
