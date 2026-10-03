// Copyright 2026 The decision-model-sdk-go Authors.
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

package decision

import (
	"bytes"
	"compress/gzip"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
)

// gzipAPI serves api as the live API does (docs/perf/ledger.md): gzip-encoded
// when the request accepts gzip, and as it is otherwise, with its length
// declared either way (the loopback server declares none on its own). It
// records each request's Accept-Encoding and whether the answer went out
// gzip-encoded.
type gzipAPI struct {
	api http.Handler

	mu   sync.Mutex
	seen []gzipSeen
}

// gzipSeen is one request as gzipAPI saw it.
type gzipSeen struct {
	AcceptEncoding string
	Gzipped        bool
}

// ServeHTTP implements http.Handler.
func (g *gzipAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ae := r.Header.Get("Accept-Encoding")
	gz := strings.Contains(ae, "gzip")
	g.mu.Lock()
	g.seen = append(g.seen, gzipSeen{AcceptEncoding: ae, Gzipped: gz})
	g.mu.Unlock()
	rec := httptest.NewRecorder()
	g.api.ServeHTTP(rec, r)
	body := rec.Body.Bytes()
	maps.Copy(w.Header(), rec.Header())
	if gz {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(body)
		_ = zw.Close()
		body = buf.Bytes()
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(rec.Code)
	_, _ = w.Write(body)
}

// requests returns what gzipAPI saw, in order.
func (g *gzipAPI) requests() []gzipSeen {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.seen)
}

// TestCompressionOption pins WithCompression against FakeAPI behind a server
// that gzips an answer when the request accepts gzip, as the live API does,
// over TLS and HTTP/2. A client that asks for gzip, by default, with
// WithCompression(true) or through a caller's transport whose
// DisableCompression is false, sends Accept-Encoding: gzip, gets the answer
// gzip-encoded and has the transport undo it, so the SDK reads a body without
// a declared length: the DEBUG "response headers" record holds no
// Content-Length. A client that does not, with WithCompression(false) or
// through a caller's transport whose DisableCompression is true, sends no
// Accept-Encoding and reads the length the server declares: the record's
// Content-Length equals its body_bytes. The models list and a System One
// answer decode alike either way.
func TestCompressionOption(t *testing.T) {
	sdk := func(opts ...ClientOption) func(*testing.T) []ClientOption {
		return func(t *testing.T) []ClientOption {
			return append([]ClientOption{WithRootCAs(testsupport.RootCAs(t)), WithProxy(nil)}, opts...)
		}
	}
	caller := func(disable bool) func(*testing.T) []ClientOption {
		return func(t *testing.T) []ClientOption {
			tr := &http.Transport{TLSClientConfig: testsupport.ClientTLSConfig(t), DisableCompression: disable}
			return []ClientOption{WithHTTPTransport(tr)}
		}
	}
	tests := map[string]struct {
		opts func(*testing.T) []ClientOption
		gzip bool // requests accept gzip, and answers come gzip-encoded
	}{
		"success: the default requests gzip and decodes it":                                      {opts: sdk(), gzip: true},
		"success: WithCompression(true) requests gzip and decodes it":                            {opts: sdk(WithCompression(true)), gzip: true},
		"success: WithCompression(false) sends no Accept-Encoding and reads the declared length": {opts: sdk(WithCompression(false))},
		"success: a caller transport with DisableCompression true sends no Accept-Encoding":      {opts: caller(true)},
		"success: a caller transport with DisableCompression false requests gzip":                {opts: caller(false), gzip: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			api := &gzipAPI{api: &testsupport.FakeAPI{}}
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: api})
			logs := testsupport.NewLogRecorder(slog.LevelDebug)
			clearEnv(t)
			opts := append([]ClientOption{WithAPIKey(testKey), WithBaseURL(srv.URL()), WithModel(testModel), WithLogger(logs.Logger())}, tt.opts(t)...)
			c, err := NewClient(opts...)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			t.Cleanup(func() { _ = c.Close() })

			models, err := c.Models().List(t.Context())
			if err != nil || len(models.Models()) != 1 || models.Models()[0].Name() != "jev-latest" {
				t.Fatalf("List = %v, %v; want FakeAPI's one model", models, err)
			}
			resp, err := c.SystemOne(t.Context(), "x", noulQuestion(t))
			if err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			if p, ok := resp.Answers().Noul("q"); !ok || p.Noul() != 0.75 {
				t.Errorf("answer q = %v (found %t), want FakeAPI's noul 0.75", p, ok)
			}

			accept := ""
			if tt.gzip {
				accept = "gzip"
			}
			want := []gzipSeen{{AcceptEncoding: accept, Gzipped: tt.gzip}, {AcceptEncoding: accept, Gzipped: tt.gzip}}
			if diff := gocmp.Diff(want, api.requests()); diff != "" {
				t.Errorf("requests as the server saw them (-want +got):\n%s", diff)
			}
			var records int
			for _, r := range logs.At(slog.LevelDebug) {
				if r.Message != "response headers" {
					continue
				}
				records++
				length, declared := r.Attr("headers.Content-Length")
				n, _ := r.Attr("body_bytes")
				if declared == tt.gzip || declared && length.String() != strconv.FormatInt(n.Int64(), 10) {
					t.Errorf("response headers record: Content-Length %q (present %t), body_bytes %d; want it present, equal to the body, only without gzip", length, declared, n.Int64())
				}
			}
			if records != 2 {
				t.Errorf("%d response headers records, want 2", records)
			}
		})
	}
}
