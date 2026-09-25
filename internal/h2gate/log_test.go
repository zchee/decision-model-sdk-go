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
	"log/slog"
	"strconv"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// debugEvents returns the messages of the DEBUG records, in order, with
// the attribute named key rendered after each, for comparison.
func debugEvents(logs *testsupport.LogRecorder, key map[string]string) []string {
	var out []string
	for _, r := range logs.At(slog.LevelDebug) {
		line := r.Message
		if k, ok := key[r.Message]; ok {
			v, _ := r.Attr(k)
			line += " " + k + "=" + v.String()
		}
		out = append(out, line)
	}
	return out
}

// TestLogEvents checks the transport's DEBUG events (section 6.3
// observability): one "h2: dial" per new connection, "h2: gate release"
// with the waiters it released, "h2: gate error" with the reason of a
// failed or vanished leader, and "h2: redial error" for a dial that fails
// after the gate is warm.
func TestLogEvents(t *testing.T) {
	attrs := map[string]string{"h2: dial": "h2", "h2: gate error": "reason", "h2: redial error": "reason"}

	t.Run("success: a cold burst logs one dial and one gate release", func(t *testing.T) {
		const n = 8
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		logs := testsupport.NewLogRecorder(slog.LevelDebug)
		tr, gd, gate := gatedTransport(t, srv, 10*time.Second)
		tr.log = logs.Logger()
		done := make(chan []result, 1)
		go func() {
			done <- fanOut(n, func(i int) result { return get(t.Context(), tr, srv.URL()+"/"+strconv.Itoa(i)) })
		}()
		waitUntil(t, "the leader's dial and 7 parked waiters", func() bool { return gd.Waiting() == 1 && tr.parked.Load() == n-1 })
		close(gate)
		if cl := classes(<-done); cl["ok"] != n {
			t.Fatalf("classes %v", cl)
		}
		if diff := gocmp.Diff([]string{"h2: dial h2=true", "h2: gate release"}, debugEvents(logs, attrs)); diff != "" {
			t.Errorf("DEBUG events (-want +got):\n%s", diff)
		}
		for _, r := range logs.At(slog.LevelDebug) {
			if v, ok := r.Attr("waiters"); r.Message == "h2: gate release" && (!ok || v.Int64() != n-1) {
				t.Errorf("gate release waiters = %v, want %d", v, n-1)
			}
		}
	})

	t.Run("error: a failed leader logs the gate error with its reason", func(t *testing.T) {
		l := testsupport.NewSilentListener(t)
		logs := testsupport.NewLogRecorder(slog.LevelDebug)
		tr := newTestTransport(t, Config{APIURL: mustURL(t, l.URL()), ConnectTimeout: 100 * time.Millisecond, Logger: logs.Logger()})
		if r := get(t.Context(), tr, l.URL()+"/"); r.Err == nil {
			t.Fatal("want a dial failure")
		}
		if diff := gocmp.Diff([]string{"h2: gate error reason=timeout"}, debugEvents(logs, attrs)); diff != "" {
			t.Errorf("DEBUG events (-want +got):\n%s", diff)
		}
	})

	t.Run("error: a vanished leader logs the gate error as leader-gone", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		logs := testsupport.NewLogRecorder(slog.LevelDebug)
		tr, gd, gate := gatedTransport(t, srv, 10*time.Second)
		t.Cleanup(func() { close(gate) })
		tr.log = logs.Logger()
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan result, 1)
		go func() { done <- get(ctx, tr, srv.URL()+"/") }()
		waitUntil(t, "the leader's dial at the gate", func() bool { return gd.Waiting() == 1 })
		cancel()
		if r := <-done; !errors.Is(r.Err, context.Canceled) {
			t.Fatalf("leader error %v", r.Err)
		}
		if diff := gocmp.Diff([]string{"h2: gate error reason=leader-gone"}, debugEvents(logs, attrs)); diff != "" {
			t.Errorf("DEBUG events (-want +got):\n%s", diff)
		}
	})

	t.Run("error: a dial that fails after warm logs a redial error", func(t *testing.T) {
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
		logs := testsupport.NewLogRecorder(slog.LevelDebug)
		tr := newTestTransport(t, Config{APIURL: mustURL(t, srv.URL()), Logger: logs.Logger()})
		warmUp(t, tr, srv.URL())
		srv.Close()               // the listener is gone: the next dial is refused
		tr.CloseIdleConnections() // and the pooled connection with it, so the next call dials
		r := get(t.Context(), tr, srv.URL()+"/after")
		if _, ok := errors.AsType[*DialError](r.Err); !ok {
			t.Fatalf("error %s, want a *DialError from the refused re-dial", chain(r.Err))
		}
		if diff := gocmp.Diff([]string{"h2: dial h2=true", "h2: gate release", "h2: redial error reason=dial"}, debugEvents(logs, attrs)); diff != "" {
			t.Errorf("DEBUG events (-want +got):\n%s", diff)
		}
	})
}
