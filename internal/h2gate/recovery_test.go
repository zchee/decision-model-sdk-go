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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// The recovery tests pin what the stock transport does after GotConn (S-T3,
// S-T4): the gate is warm and plays no part, re-dials are the stock pool's,
// and a failure after GotConn is never a *DialError. They are part of the
// gotip canary (K5): a change in the stock behaviour fails them.

// seenOn maps each path to where the server saw it: c<conn>/<action>, with
// /dropped when the stream ended before its response.
func seenOn(srv *testsupport.LoopbackServer) map[string][]string {
	m := map[string][]string{}
	for _, r := range srv.Requests() {
		tag := fmt.Sprintf("c%d/%s", r.Conn, r.Action)
		if r.Dropped {
			tag += "/dropped"
		}
		m[r.Path] = append(m[r.Path], tag)
	}
	return m
}

// warmUp sends one request so the gate is warm and one connection is open.
func warmUp(t *testing.T, tr *Transport, base string) {
	t.Helper()
	if r := get(t.Context(), tr, base+"/warm"); r.Err != nil || r.Status != http.StatusOK {
		t.Fatalf("warm-up: %d %v", r.Status, r.Err)
	}
}

// heldStreams waits until the server's only connection has n open streams.
func heldStreams(t *testing.T, srv *testsupport.LoopbackServer, n int) *testsupport.H2Conn {
	t.Helper()
	waitUntil(t, fmt.Sprintf("%d held streams", n), func() bool {
		cs := srv.LiveH2Conns()
		return len(cs) == 1 && len(cs[0].ActiveStreams()) == n
	})
	return srv.LiveH2Conns()[0]
}

// TestGoAway covers GOAWAY on a warm connection (S-T3): streams above
// LastStreamID are replayed by the stock transport inside their RoundTrip on
// a second connection, a refused stream is retried on the same one, and a
// POST without GetBody cannot be replayed.
func TestGoAway(t *testing.T) {
	t.Run("success: GOAWAY below two of four in-flight GETs: two finish, two replay on a second connection", func(t *testing.T) {
		release := make(chan struct{})
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: holdHandler(release)})
		tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
		warmUp(t, tr, srv.URL())
		paths := []string{"/hold/a", "/hold/b", "/hold/c", "/hold/d"}
		calls := make([]result, len(paths))
		var wg sync.WaitGroup
		for i, p := range paths {
			wg.Go(func() { calls[i] = get(t.Context(), tr, srv.URL()+p) })
		}
		conn := heldStreams(t, srv, len(paths))
		active := conn.ActiveStreams()
		if err := conn.GoAway(active[1], testsupport.CodeNoError); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, "the two replays", func() bool { return len(srv.Requests()) == 1+len(paths)+2 })
		close(release)
		wg.Wait()
		// A replay that marked the new connection cleared its mark when its
		// response arrived (R72b); none may outlive the calls.
		if n := tr.nUnsettled.Load(); n != 0 {
			t.Errorf("%d unsettled marks left after every replay was answered, want 0", n)
		}
		for i, r := range calls {
			if r.Err != nil || r.Status != http.StatusOK || r.Body != "ok "+paths[i] {
				t.Errorf("%s: %d %q %v", paths[i], r.Status, r.Body, r.Err)
			}
		}
		seen := seenOn(srv)
		replayed := 0
		for _, p := range paths {
			if gocmp.Equal(seen[p], []string{"c0/serve/dropped", "c1/serve"}) {
				replayed++
			}
		}
		st := tr.Stats()
		if replayed != 2 || srv.Accepts() != 2 || st.Dials != 2 || st.Leaders != 1 || tr.gateState() != stateWarm {
			t.Errorf("seen %v; accepts %d; stats %+v; want 2 replayed on connection 1, 2 accepts, 2 dials, 1 leader, warm", seen, srv.Accepts(), st)
		}
	})

	t.Run("success: a refused stream is retried on the same connection", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{OnStream: func(s *testsupport.Stream) testsupport.Action {
			if s.Seq == 1 {
				return testsupport.ActionRefuse
			}
			return testsupport.ActionServe
		}})
		tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
		warmUp(t, tr, srv.URL())
		r := get(t.Context(), tr, srv.URL()+"/refused-once")
		if r.Err != nil || r.Status != http.StatusOK || srv.Accepts() != 1 {
			t.Errorf("%d %v, accepts %d; want 200 on the one connection", r.Status, r.Err, srv.Accepts())
		}
		if diff := gocmp.Diff([]string{"c0/refuse", "c0/serve"}, seenOn(srv)["/refused-once"]); diff != "" {
			t.Errorf("seen (-want +got):\n%s", diff)
		}
	})

	t.Run("error: a POST without GetBody above LastStreamID is not replayed; one with GetBody is", func(t *testing.T) {
		release := make(chan struct{})
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: holdHandler(release)})
		tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
		warmUp(t, tr, srv.URL())
		kinds := []string{"getbody", "nogetbody"}
		out := make([]result, len(kinds))
		var wg sync.WaitGroup
		for i, kind := range kinds {
			wg.Go(func() {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL()+"/hold/"+kind, strings.NewReader(`{"state":"x"}`))
				if err != nil {
					out[i] = result{Err: err}
					return
				}
				if kind == "nogetbody" {
					req.GetBody = nil
				}
				resp, err := tr.RoundTrip(req)
				if err != nil {
					out[i] = result{Err: err}
					return
				}
				b, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				out[i] = result{Status: resp.StatusCode, Body: string(b)}
			})
		}
		conn := heldStreams(t, srv, 2)
		if err := conn.GoAway(1, testsupport.CodeNoError); err != nil { // both held streams are above 1
			t.Fatal(err)
		}
		waitUntil(t, "the replay of the POST with GetBody", func() bool { return len(seenOn(srv)["/hold/getbody"]) == 2 })
		close(release)
		wg.Wait()
		if out[0].Err != nil || out[0].Status != http.StatusOK {
			t.Errorf("POST with GetBody: %d %v, want 200 after the replay", out[0].Status, out[0].Err)
		}
		var de *DialError
		if out[1].Err == nil || errors.As(out[1].Err, &de) || errClass(out[1].Err) != "connection" || !strings.Contains(out[1].Err.Error(), "GetBody") {
			t.Errorf("POST without GetBody: %v, want the stock transport's cannot-retry error, class connection", out[1].Err)
		}
	})
}

// TestConnClose ends the connection under an in-flight request (S-T3): a
// clean close (close_notify, FIN) gives io.ErrUnexpectedEOF, a TCP reset
// gives ECONNRESET; neither is retried or a *DialError, and the next request
// dials a new connection without the gate.
func TestConnClose(t *testing.T) {
	tests := map[string]struct {
		end   func(*testsupport.LoopbackServer)
		check func(error) bool
		want  string
	}{
		"error: a clean close gives unexpected EOF": {
			end: func(s *testsupport.LoopbackServer) { s.CloseConns() },
			check: func(err error) bool {
				var ne net.Error
				return errors.Is(err, io.ErrUnexpectedEOF) && !errors.As(err, &ne)
			},
			want: "io.ErrUnexpectedEOF, not a net.Error",
		},
		"error: a TCP reset gives ECONNRESET": {
			end: func(s *testsupport.LoopbackServer) { s.LiveH2Conns()[0].Reset() },
			check: func(err error) bool {
				ne, ok := errors.AsType[net.Error](err)
				return isConnReset(err) && ok && !ne.Timeout()
			},
			want: "ECONNRESET, a net.Error without Timeout",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: holdHandler(release)})
			tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
			warmUp(t, tr, srv.URL())
			done := make(chan result, 1)
			go func() { done <- get(t.Context(), tr, srv.URL()+"/hold/x") }()
			heldStreams(t, srv, 1)
			tt.end(srv)
			inflight := <-done
			var de *DialError
			if !tt.check(inflight.Err) || errors.As(inflight.Err, &de) || errClass(inflight.Err) != "connection" {
				t.Errorf("in-flight error %s, want %s, class connection", chain(inflight.Err), tt.want)
			}
			next := get(t.Context(), tr, srv.URL()+"/next")
			st := tr.Stats()
			if next.Err != nil || next.Status != http.StatusOK || srv.Accepts() != 2 || st.Leaders != 1 || st.Dials != 2 {
				t.Errorf("next %d %v; accepts %d; stats %+v; want 200 on a second connection, 1 leader", next.Status, next.Err, srv.Accepts(), st)
			}
			if got := seenOn(srv)["/hold/x"]; len(got) != 1 {
				t.Errorf("the in-flight request was seen %v, want once (no replay)", got)
			}
		})
	}
}

// TestReplay pins the stock transport's own replay (S-T4): GOAWAY before the
// request was processed gives one RoundTrip that returns 200 while the
// server accepted two connections and saw the request twice (invisible to
// any wrapper, so it is one SDK attempt); a failure after the body was
// written is a connection error, never replayed.
func TestReplay(t *testing.T) {
	for name, post := range map[string]bool{"a GET": false, "a POST with GetBody": true} {
		t.Run("success: GOAWAY before "+name+" was processed: one RoundTrip, 200, two connections", func(t *testing.T) {
			var mu sync.Mutex
			var bodies []string
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{
				OnStream: func(s *testsupport.Stream) testsupport.Action {
					if s.Conn.Index() == 0 {
						return testsupport.ActionGoAway
					}
					return testsupport.ActionServe
				},
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					mu.Lock()
					bodies = append(bodies, string(b))
					mu.Unlock()
					w.WriteHeader(http.StatusOK)
				}),
			})
			tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
			var r result
			if post {
				r = do(t.Context(), tr, http.MethodPost, srv.URL()+"/r", []byte(`{"state":"s"}`))
			} else {
				r = get(t.Context(), tr, srv.URL()+"/r")
			}
			if r.Err != nil || r.Status != http.StatusOK {
				t.Fatalf("%d %v, want 200", r.Status, r.Err)
			}
			if diff := gocmp.Diff([]string{"c0/goaway", "c1/serve"}, seenOn(srv)["/r"]); diff != "" {
				t.Errorf("seen (-want +got):\n%s", diff)
			}
			want := []string{""}
			if post {
				want = []string{`{"state":"s"}`}
			}
			mu.Lock()
			defer mu.Unlock()
			if diff := gocmp.Diff(want, bodies); diff != "" {
				t.Errorf("bodies served (-want +got):\n%s", diff)
			}
			if st := tr.Stats(); srv.Accepts() != 2 || st.Dials != 2 || st.Leaders != 1 {
				t.Errorf("accepts %d, stats %+v; want 2 connections, 1 leader", srv.Accepts(), st)
			}
		})
	}

	t.Run("error: GOAWAY before a POST without GetBody was processed is not replayed", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{OnStream: func(*testsupport.Stream) testsupport.Action { return testsupport.ActionGoAway }})
		tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL()+"/r", strings.NewReader(`{"state":"s"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.GetBody = nil
		resp, err := tr.RoundTrip(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil || errClass(err) != "connection" || srv.Accepts() != 1 {
			t.Errorf("%v, accepts %d; want a connection-class error on 1 connection", err, srv.Accepts())
		}
	})

	for name, kind := range map[string]string{"a clean close": "tcp-close", "a TCP reset": "tcp-reset", "RST_STREAM INTERNAL_ERROR": "rst-internal"} {
		t.Run("error: "+name+" after the body was read is a connection error, not replayed", func(t *testing.T) {
			var srv *testsupport.LoopbackServer
			srv = testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				if r.URL.Path != "/fail" {
					return
				}
				switch kind {
				case "tcp-close":
					srv.CloseConns()
				case "tcp-reset":
					srv.LiveH2Conns()[0].Reset()
				default:
					panic(http.ErrAbortHandler) // RST_STREAM INTERNAL_ERROR
				}
				<-r.Context().Done()
			})})
			tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL())})
			warmUp(t, tr, srv.URL())
			r := do(t.Context(), tr, http.MethodPost, srv.URL()+"/fail", []byte(`{"state":"s"}`))
			var de *DialError
			if r.Err == nil || errors.As(r.Err, &de) || errClass(r.Err) != "connection" {
				t.Errorf("error %s, want a connection-class error that is not a *DialError", chain(r.Err))
			}
			switch kind {
			case "tcp-close":
				if !errors.Is(r.Err, io.ErrUnexpectedEOF) {
					t.Errorf("error %v, want io.ErrUnexpectedEOF", r.Err)
				}
			case "tcp-reset":
				if !isConnReset(r.Err) {
					t.Errorf("error %v, want ECONNRESET", r.Err)
				}
			default:
				if !strings.Contains(r.Err.Error(), "INTERNAL_ERROR") {
					t.Errorf("error %v, want the peer's INTERNAL_ERROR stream reset", r.Err)
				}
			}
			if got := seenOn(srv)["/fail"]; len(got) != 1 {
				t.Errorf("/fail seen %v, want once (no replay)", got)
			}
		})
	}
}
