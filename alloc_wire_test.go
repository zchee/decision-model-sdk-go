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

package typesafe

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// countingRT passes every request to rt and counts the bytes read from the
// bodies of its responses.
type countingRT struct {
	rt   http.RoundTripper
	read atomic.Int64
}

func (c *countingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.rt.RoundTrip(req)
	if resp != nil {
		resp.Body = &countingBody{ReadCloser: resp.Body, read: &c.read}
	}
	return resp, err
}

// countingBody is a response body that adds what each Read returns to read.
type countingBody struct {
	io.ReadCloser
	read *atomic.Int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read.Add(int64(n))
	return n, err
}

// TestResponseCapOverTheWire checks the response size cap over a real
// connection (plan section 9, "size cap over the wire"; rulings R92 m-2 and
// R106 as V50 worded it): the SDK's own transport, over TLS and HTTP/2 to
// the in-process loopback server, answered with a 200 whose body is the
// default cap + 1 bytes, declared by its Content-Length or undeclared (no
// Content-Length: DATA frames until the end of the stream), ends the call
// with a *ResponseTooLargeError naming the 200 and the cap, after one
// attempt under DefaultRetry, which does not retry it (one POST on the
// server, Stats().Attempts one more than after WarmUp), having read none
// of a declared body and exactly cap + 1 bytes of an undeclared one, the
// byte past the cap (the transport's response body is wrapped in a
// counter, below the SDK's reads).
//
// It records the call's runtime.MemStats deltas (the WIRE lines, ledger
// rows), which are not AC-P5's measure and are not bounded here: the
// loopback server runs in the test's process, so they also count its
// 16 MiB write and both sides' TLS and HTTP/2 buffers. AC-P5's bounds are
// TestMemStatsCap's, over the Recorder. The test asserts no allocation
// count, so it runs in every build, the race detector's included.
func TestResponseCapOverTheWire(t *testing.T) {
	const limit = DefaultMaxResponseBytes
	over := bytes.Repeat([]byte{' '}, limit+1)
	models := testsupport.Fixture(t, "models.json")
	tests := map[string]struct {
		declared bool
		wantRead int64
	}{
		"error: declared cap + 1, refused before a read":              {declared: true, wantRead: 0},
		"error: undeclared cap + 1, refused at the byte past the cap": {declared: false, wantRead: limit + 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var posts atomic.Int32
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, modelsPath) {
					_, _ = w.Write(models)
					return
				}
				posts.Add(1)
				if tt.declared {
					w.Header().Set("Content-Length", strconv.Itoa(len(over)))
				}
				// In 64 KiB writes, so that the handler ends at the first
				// write the client's reset of the stream fails.
				for rest := over; len(rest) > 0; {
					n := min(len(rest), 64<<10)
					if _, err := w.Write(rest[:n]); err != nil {
						return
					}
					rest = rest[n:]
				}
			})})
			clearEnv(t)
			c, err := NewClient(WithAPIKey(testKey), WithBaseURL(srv.URL()), WithRootCAs(testsupport.RootCAs(t)))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			t.Cleanup(func() { _ = c.Close() })
			counter := &countingRT{rt: c.cfg.transport.rt}
			c.cfg.transport.rt = counter
			if err := c.WarmUp(t.Context()); err != nil { // the connection, so the call's deltas are the call's
				t.Fatalf("WarmUp: %v", err)
			}
			before := c.Stats()
			counter.read.Store(0)

			qs, state := q3Questions(t), newAllocState()
			runtime.GC()
			var m0, m1 runtime.MemStats
			runtime.ReadMemStats(&m0)
			_, err = c.SystemOne(t.Context(), state, qs)
			runtime.ReadMemStats(&m1)

			read := counter.read.Load()
			label := "undeclared"
			if tt.declared {
				label = "declared"
			}
			t.Logf("WIRE %-10s read=%-8d mallocs=%-6d totalAlloc=%-9d (%.3f MiB; the server's write and both sides' buffers included, recorded)",
				label, read, m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc, float64(m1.TotalAlloc-m0.TotalAlloc)/(1<<20))
			tl, ok := errors.AsType[*ResponseTooLargeError](err)
			if !ok {
				t.Fatalf("SystemOne error = %v (%T), want a *ResponseTooLargeError", err, err)
			}
			if tl.StatusCode != http.StatusOK || tl.Limit != limit {
				t.Errorf("*ResponseTooLargeError status %d, limit %d; want 200 and the cap %d", tl.StatusCode, tl.Limit, limit)
			}
			if read != tt.wantRead {
				t.Errorf("the SDK read %d bytes of the body, want %d (the cap is %d)", read, tt.wantRead, limit)
			}
			if read > limit+1 {
				t.Errorf("the SDK read %d bytes of the body, past the cap + 1 = %d", read, limit+1)
			}
			if got := c.Stats().Attempts - before.Attempts; got != 1 {
				t.Errorf("the call made %d attempts, want 1: DefaultRetry does not retry a 2xx over the cap", got)
			}
			if got := posts.Load(); got != 1 {
				t.Errorf("the server saw %d POST requests, want 1", got)
			}
		})
	}
}
