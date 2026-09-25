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
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// dialFlags returns a *DialError's classification for comparison.
func dialFlags(err error) map[string]bool {
	de, ok := errors.AsType[*DialError](err)
	if !ok {
		return nil
	}
	return map[string]bool{"proxy": de.Proxy, "timeout": de.Timeout, "not_negotiated": errors.Is(de, ErrNotNegotiated)}
}

// TestNoALPN points HTTP2Only at a server whose TLS handshake negotiates no
// protocol: the VerifyConnection check refuses the API hop before any byte
// of the request, and every caller gets its own not-negotiated *DialError.
// Without the check the stock transport would answer over HTTP/1.1 although
// Protocols lacks HTTP/1 (transport.go:2108-2124), the reason the check
// exists.
func TestNoALPN(t *testing.T) {
	t.Run("error: the API hop negotiated nothing: refused before any request", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: testsupport.ALPNNone})
		tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
		r := get(t.Context(), tr, srv.URL()+"/")
		want := map[string]bool{"proxy": false, "timeout": false, "not_negotiated": true}
		if diff := gocmp.Diff(want, dialFlags(r.Err)); diff != "" {
			t.Errorf("classification of %v (-want +got):\n%s", r.Err, diff)
		}
		if n := len(srv.Requests()); n != 0 {
			t.Errorf("the server saw %d requests, want none", n)
		}
		if srv.Accepts() != 1 {
			t.Errorf("accepts %d, want the one connection whose handshake the client aborted", srv.Accepts())
		}
		if st := tr.Stats(); tr.gateState() != stateCold || st.Failures != 1 {
			t.Errorf("gate %v, stats %+v; want cold after 1 failure", tr.gateState(), st)
		}
	})

	t.Run("error: every waiter of a refused leader gets a fresh not-negotiated error", func(t *testing.T) {
		const n = 8
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: testsupport.ALPNNone})
		tr, gd, gate := gatedTransport(t, srv, 10*time.Second)
		done := make(chan []result, 1)
		go func() {
			done <- fanOut(n, func(i int) result { return get(t.Context(), tr, srv.URL()+"/"+strconv.Itoa(i)) })
		}()
		waitUntil(t, "the leader's dial and 7 parked waiters", func() bool { return gd.Waiting() == 1 && tr.parked.Load() == n-1 })
		close(gate)
		calls := <-done
		distinct := map[*DialError]bool{}
		for i, r := range calls {
			de, ok := errors.AsType[*DialError](r.Err)
			if !ok || !errors.Is(r.Err, ErrNotNegotiated) || errors.Is(r.Err, context.Canceled) {
				t.Fatalf("call %d: %v, want a not-negotiated *DialError", i, r.Err)
			}
			distinct[de] = true
			if de.Err != calls[0].Err.(*DialError).Err { //nolint:errorlint // identity of the shared cause is the assertion
				t.Errorf("call %d: cause %v differs from call 0's", i, de.Err)
			}
		}
		if len(distinct) != n || srv.Accepts() != 1 || tr.Stats().Failures != 1 {
			t.Errorf("distinct values %d, accepts %d, stats %+v; want %d, 1, 1 failure", len(distinct), srv.Accepts(), tr.Stats(), n)
		}
	})

	t.Run("success: without the check the stock transport answers over HTTP/1.1", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: testsupport.ALPNNone})
		bare := &http.Transport{Protocols: protocols(HTTP2Only, "https"), TLSClientConfig: testsupport.ClientTLSConfig(t)}
		t.Cleanup(bare.CloseIdleConnections)
		r := get(t.Context(), bare, srv.URL()+"/")
		if r.Err != nil || r.Status != http.StatusOK || r.ProtoMajor != 1 {
			t.Errorf("bare stock transport: %d HTTP/%d %v, want 200 over HTTP/1.1", r.Status, r.ProtoMajor, r.Err)
		}
	})
}

// TestALPNHTTP1Only points HTTP2Only at a server that offers http/1.1 alone:
// the server refuses the client's h2-only offer with TLS alert 120, which
// the classification maps to not-negotiated (R20), before any request.
func TestALPNHTTP1Only(t *testing.T) {
	srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: testsupport.ALPNHTTP1Only})
	tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
	r := get(t.Context(), tr, srv.URL()+"/")
	want := map[string]bool{"proxy": false, "timeout": false, "not_negotiated": true}
	if diff := gocmp.Diff(want, dialFlags(r.Err)); diff != "" {
		t.Errorf("classification of %v (-want +got):\n%s", r.Err, diff)
	}
	oe, ok := errors.AsType[*net.OpError](r.Err)
	if !ok || oe.Op != "remote error" || oe.Err == nil || oe.Err.Error() != alertNoApplicationProtocol {
		t.Errorf("chain %s, want a *net.OpError{Op: \"remote error\"} carrying alert 120", chain(r.Err))
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("the server saw %d requests, want none", n)
	}
	if conns := srv.Conns(); len(conns) != 1 || conns[0].HandshakeErr == "" {
		t.Errorf("server connections %+v, want one refused handshake", conns)
	}
	if tr.gateState() != stateCold {
		t.Errorf("gate %v, want cold", tr.gateState())
	}
}

// TestClassify checks the classification of error chains the transport
// produces (S-T3, S-T4, S-T5b), built by hand so every branch is pinned.
func TestClassify(t *testing.T) {
	alert := &net.OpError{Op: "remote error", Err: errors.New(alertNoApplicationProtocol)}
	timeout := &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}
	tests := map[string]struct {
		err  error
		want map[string]bool
	}{
		"success: alert 120 is not-negotiated": {
			err:  fmt.Errorf("tls: %w", alert),
			want: map[string]bool{"proxy": false, "timeout": false, "not_negotiated": true},
		},
		"success: ErrNotNegotiated from VerifyConnection is not-negotiated": {
			err:  fmt.Errorf("%w: the API host's TLS handshake negotiated %q", ErrNotNegotiated, ""),
			want: map[string]bool{"proxy": false, "timeout": false, "not_negotiated": true},
		},
		"success: proxyconnect around alert 120 is a proxy failure only (R20)": {
			err:  &net.OpError{Op: "proxyconnect", Net: "tcp", Err: fmt.Errorf("tls: %w", alert)},
			want: map[string]bool{"proxy": true, "timeout": false, "not_negotiated": false},
		},
		"success: a proxy TLS handshake timeout keeps both flags": {
			err:  &net.OpError{Op: "proxyconnect", Net: "tcp", Err: timeout},
			want: map[string]bool{"proxy": true, "timeout": true, "not_negotiated": false},
		},
		"success: a dial timeout": {
			err:  timeout,
			want: map[string]bool{"proxy": false, "timeout": true, "not_negotiated": false},
		},
		"success: a context deadline is a timeout": {
			err:  fmt.Errorf("dial: %w", context.DeadlineExceeded),
			want: map[string]bool{"proxy": false, "timeout": true, "not_negotiated": false},
		},
		"success: a refused dial is a plain dial failure": {
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
			want: map[string]bool{"proxy": false, "timeout": false, "not_negotiated": false},
		},
		"success: a joined chain is walked in full": {
			err:  errors.Join(errors.New("first"), fmt.Errorf("second: %w", alert)),
			want: map[string]bool{"proxy": false, "timeout": false, "not_negotiated": true},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			d := classify(tt.err)
			if diff := gocmp.Diff(tt.want, dialFlags(d)); diff != "" {
				t.Errorf("classify(%s) (-want +got):\n%s", chain(tt.err), diff)
			}
			if !errors.Is(d, tt.err) {
				t.Errorf("errors.Is(DialError, cause) = false")
			}
			c := d.clone()
			if c == d || c.Err != d.Err || errors.Is(c, ErrNotNegotiated) != errors.Is(d, ErrNotNegotiated) { //nolint:errorlint // identity is the assertion
				t.Errorf("clone %p of %p: a fresh value of the same class around the same cause is wanted", c, d)
			}
			if d.Error() == "" {
				t.Error("empty message")
			}
		})
	}
}
