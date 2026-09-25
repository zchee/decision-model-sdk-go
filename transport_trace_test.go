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
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// recovered runs fn and returns the value it panicked with, or nil.
func recovered(fn func()) (v any) {
	defer func() { v = recover() }()
	fn()
	return nil
}

// within runs fn on its own goroutine and fails the test when fn has not
// returned after d: a transport wedged by an escaped panic (a pool mutex
// never unlocked, a stream never released) blocks without regard to any
// context, and the test must fail rather than hang.
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %v: the transport is wedged", what, d)
	}
}

// hookPanic is the value the tests' hooks panic with.
type hookPanic struct {
	hook string
	n    int
}

// TestClientTraceShieldCoversEveryHook pins that the shield wraps every hook
// of httptrace.ClientTrace, that a wrapped hook calls the caller's, that no
// panic of one escapes it, and that done raises the first panic again. A hook
// added to ClientTrace in a later Go release fails the test until the shield
// wraps it.
func TestClientTraceShieldCoversEveryHook(t *testing.T) {
	var in httptrace.ClientTrace
	v := reflect.ValueOf(&in).Elem()
	var names, calls []string
	for i := range v.NumField() {
		f, name := v.Field(i), v.Type().Field(i).Name
		if f.Kind() != reflect.Func {
			t.Fatalf("ClientTrace.%s is a %v; the shield wraps hooks only", name, f.Kind())
		}
		names = append(names, name)
		f.Set(reflect.MakeFunc(f.Type(), func([]reflect.Value) []reflect.Value {
			calls = append(calls, name)
			panic(hookPanic{hook: name})
		}))
	}
	sh := newShield(&in, slog.New(slog.DiscardHandler))
	out := reflect.ValueOf(&sh.trace).Elem()
	for i, name := range names {
		f := out.Field(i)
		if f.IsNil() {
			t.Errorf("hook %s is not wrapped", name)
			continue
		}
		args := make([]reflect.Value, f.Type().NumIn())
		for j := range args {
			args[j] = reflect.Zero(f.Type().In(j))
		}
		if p := recovered(func() { f.Call(args) }); p != nil {
			t.Errorf("hook %s let its panic through: %v", name, p)
		}
	}
	if diff := gocmp.Diff(names, calls); diff != "" {
		t.Errorf("hooks reached (-want +got):\n%s", diff)
	}
	if p := recovered(func() { sh.done(nil) }); p != (hookPanic{hook: names[0]}) {
		t.Errorf("done panicked with %v, want the first hook's value %v", p, hookPanic{hook: names[0]})
	}
	empty := newShield(&httptrace.ClientTrace{}, slog.New(slog.DiscardHandler))
	if !reflect.ValueOf(empty.trace).IsZero() {
		t.Error("a trace without hooks gave a shield with hooks, want none")
	}
	if p := recovered(func() { empty.done(nil) }); p != nil {
		t.Errorf("done without a panic panicked with %v", p)
	}
}

// TestClientTracePanicIsRaisedOnTheCaller pins K28 and K28b through the
// transport resolve builds: net/http calls a warm request's GetConn with its
// HTTP/2 connection pool locked and the stream reserved
// (internal/http2/client_conn_pool.go:52-61), and GotConn once the stream is
// reserved, so a panic unwinding from either would leave the pool locked or
// the stream counted. The shield recovers it inside the hook and raises it
// from roundTrip once net/http has returned: the caller sees the panic, the
// token is free, the pool is not locked and no stream is leaked, which a
// server allowing 2 streams per connection makes visible.
func TestClientTracePanicIsRaisedOnTheCaller(t *testing.T) {
	const (
		streams = 2
		wedged  = 10 * time.Second
	)
	tests := map[string]func(armed *atomic.Bool, n *atomic.Int64) *httptrace.ClientTrace{
		"success: a warm GetConn panic": func(armed *atomic.Bool, n *atomic.Int64) *httptrace.ClientTrace {
			return &httptrace.ClientTrace{GetConn: func(string) {
				if armed.Load() {
					panic(hookPanic{hook: "GetConn", n: int(n.Load())})
				}
			}}
		},
		"success: a GotConn panic": func(armed *atomic.Bool, n *atomic.Int64) *httptrace.ClientTrace {
			return &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) {
				if armed.Load() {
					panic(hookPanic{hook: "GotConn", n: int(n.Load())})
				}
			}}
		},
	}
	for name, trace := range tests {
		t.Run(name, func(t *testing.T) {
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{MaxConcurrentStreams: streams})
			var (
				armed atomic.Bool
				n     atomic.Int64
			)
			tr := trace(&armed, &n)
			c := loopbackConfig(t, srv, WithClientTrace(tr))
			get := func(path string) getResult { return getWithin(t, c.transport, srv.URL()+path, 0, 5*time.Second) }
			if r := get("/warm"); r.err != nil || r.protoMajor != 2 {
				t.Fatalf("warm-up GET = HTTP/%d %v", r.protoMajor, r.err)
			}
			armed.Store(true)
			for i := range streams + 1 {
				n.Store(int64(i))
				var p any
				within(t, wedged, "the panicking GET #"+strconv.Itoa(i), func() { p = recovered(func() { get("/panic/" + strconv.Itoa(i)) }) })
				hp, ok := p.(hookPanic)
				if !ok || hp.n != i {
					t.Fatalf("GET #%d panicked with %v, want the hook's value #%d", i, p, i)
				}
			}
			armed.Store(false)
			// More requests than the connection has streams: a leaked
			// reservation stalls them under strict accounting.
			within(t, wedged, "the GETs after the panics", func() {
				for i := range 2 * streams {
					if r := get("/after/" + strconv.Itoa(i)); r.err != nil || r.status != http.StatusOK {
						t.Errorf("GET after the panics #%d = %d %v", i, r.status, r.err)
					}
				}
			})
			st := c.transport.stats()
			if srv.Accepts() != 1 || srv.OverLimit() != 0 || st.Dials != 1 {
				t.Errorf("accepts %d, over the limit %d, stats %+v; want 1 connection throughout", srv.Accepts(), srv.OverLimit(), st)
			}
		})
	}
}

// TestClientTraceLatePanicIsLogged pins a hook that panics after roundTrip
// returned, on a net/http goroutine: PutIdleConn of an HTTP/1.1 response,
// which net/http calls once the body has been read. The panic cannot be
// raised on the caller any more; the shield recovers it and logs a WARN
// naming the hook, and the connection goes back to the pool.
func TestClientTraceLatePanicIsLogged(t *testing.T) {
	srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{
		ALPN:    testsupport.ALPNNone,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "hello") }),
	})
	logs := testsupport.NewLogRecorder(slog.LevelDebug)
	var puts atomic.Int64
	trace := &httptrace.ClientTrace{PutIdleConn: func(error) {
		puts.Add(1)
		panic(hookPanic{hook: "PutIdleConn"})
	}}
	c := loopbackConfig(t, srv, WithHTTPVersion(HTTPAuto), WithLogger(logs.Logger()), WithClientTrace(trace))
	for i := range 2 {
		var r getResult
		if p := recovered(func() { r = getWithin(t, c.transport, srv.URL()+"/late", 0, 5*time.Second) }); p != nil {
			t.Fatalf("GET #%d panicked with %v, want the late panic kept from the caller", i, p)
		}
		if r.err != nil || r.status != http.StatusOK || r.protoMajor != 1 {
			t.Fatalf("GET #%d = %d HTTP/%d %v, want 200 over HTTP/1.1", i, r.status, r.protoMajor, r.err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for len(logs.At(slog.LevelWarn)) < i+1 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
	warns := logs.At(slog.LevelWarn)
	if len(warns) != 2 || puts.Load() != 2 {
		t.Fatalf("%d WARN records, %d PutIdleConn calls; want 2 of each", len(warns), puts.Load())
	}
	for _, w := range warns {
		attrs := map[string]string{}
		for _, a := range w.Attrs {
			attrs[a.Key] = a.Value.String()
		}
		if w.Message != "transport: trace hook panic after the request returned, recovered" || attrs["hook"] != "PutIdleConn" {
			t.Errorf("WARN %q %v, want the late-panic record naming PutIdleConn", w.Message, attrs)
		}
	}
	if srv.Accepts() != 1 {
		t.Errorf("accepts %d, want 1: the connection went back to the pool", srv.Accepts())
	}
}
