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
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// TestSilentListener checks that a TLS handshake to the silent listener
// never completes: a client's own bound ends it.
func TestSilentListener(t *testing.T) {
	t.Run("error: a context-bounded handshake times out", func(t *testing.T) {
		l := NewSilentListener(t)
		raw, err := net.DialTimeout("tcp", l.Addr(), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		cfg := ClientTLSConfig(t)
		cfg.ServerName = "127.0.0.1"
		err = tls.Client(raw, cfg).HandshakeContext(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("handshake error = %v, want context.DeadlineExceeded", err)
		}
		if l.Accepts() != 1 {
			t.Errorf("Accepts() = %d, want 1", l.Accepts())
		}
	})

	t.Run("error: net/http's TLSHandshakeTimeout fails the dial as a timeout", func(t *testing.T) {
		l := NewSilentListener(t)
		tr := newTransport(t, false, true)
		tr.TLSHandshakeTimeout = 200 * time.Millisecond
		start := time.Now()
		_, _, _, err := get(t, tr, l.URL())
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("GET error = %v, want a net.Error with Timeout()", err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("the dial took %v", elapsed)
		}
		if l.Accepts() != 1 {
			t.Errorf("Accepts() = %d, want 1", l.Accepts())
		}
	})
}

// TestFakeH2CServer checks prior-knowledge h2c on the in-memory network,
// inside a synctest bubble.
func TestFakeH2CServer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := NewFakeH2CServer(t, http.HandlerFunc(answerH2ExampleCom))
		client := srv.Client()
		for range 5 {
			status, proto, body, err := get(t, client.Transport, "http://example.com/")
			if err != nil || status != http.StatusOK || proto != 2 || body != "h2 example.com" {
				t.Fatalf("GET: %d HTTP/%d %q %v", status, proto, body, err)
			}
		}
		if srv.Accepts() != 1 {
			t.Errorf("Accepts() = %d, want 1", srv.Accepts())
		}

		// An HTTP/1.1 request on the same network fails: the server speaks
		// h2c only.
		h1, ok := client.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("client transport is %T", client.Transport)
		}
		h1 = h1.Clone()
		h1.Protocols = protocols(true, false)
		defer h1.CloseIdleConnections()
		if _, _, _, err := get(t, h1, "http://example.com/"); err == nil {
			t.Errorf("an HTTP/1.1 request succeeded against the h2c-only server")
		}
	})
}

// answerH2ExampleCom answers 200 "h2 example.com" to an HTTP/2 request for
// example.com and 400 to anything else, so a test can check both from the
// body.
func answerH2ExampleCom(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor != 2 || r.Host != "example.com" {
		http.Error(w, "not an HTTP/2 request for example.com", http.StatusBadRequest)
		return
	}
	_, _ = io.WriteString(w, "h2 example.com")
}

// TestRoutes covers address resolution and the no-network rule.
func TestRoutes(t *testing.T) {
	routes := Routes{"example.com:443": "127.0.0.1:9"}
	tests := map[string]struct {
		addr    string
		want    string
		wantErr bool
	}{
		"success: routed name":              {addr: "example.com:443", want: "127.0.0.1:9"},
		"success: IPv4 loopback literal":    {addr: "127.0.0.1:443", want: "127.0.0.1:443"},
		"success: IPv6 loopback literal":    {addr: "[::1]:443", want: "[::1]:443"},
		"error: unrouted name":              {addr: "api.typesafe.ai:443", wantErr: true},
		"error: localhost is not a literal": {addr: "localhost:443", wantErr: true},
		"error: non-loopback literal":       {addr: "10.0.0.1:443", wantErr: true},
		"error: routed name, other port":    {addr: "example.com:80", wantErr: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := routes.Resolve(tt.addr)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("Resolve(%q) = %q, %v; want %q, error %v", tt.addr, got, err, tt.want, tt.wantErr)
			}
		})
	}

	t.Run("success: DialContext reaches a routed server", func(t *testing.T) {
		srv := NewLoopbackServer(t, ServerConfig{})
		tr := newTransport(t, false, true)
		tr.DialContext = Routes{"example.com:443": srv.Addr()}.DialContext
		status, proto, _, err := get(t, tr, "https://example.com/")
		if err != nil || status != http.StatusOK || proto != 2 {
			t.Fatalf("GET: %d HTTP/%d %v", status, proto, err)
		}
		if got := srv.Requests()[0].Authority; got != "example.com" {
			t.Errorf(":authority = %q, want example.com", got)
		}
	})

	t.Run("error: DialContext refuses an unrouted name", func(t *testing.T) {
		_, err := Routes{}.DialContext(t.Context(), "tcp", "api.typesafe.ai:443")
		if err == nil || !strings.Contains(err.Error(), "no route") {
			t.Fatalf("DialContext error = %v, want a no-route error", err)
		}
	})
}
