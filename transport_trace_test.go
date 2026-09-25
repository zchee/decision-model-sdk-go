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
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"reflect"
	"runtime"
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
// context, and the test must fail rather than hang. fn reports what it found
// wrong as its result and never touches t, so a wedged fn that ends after the
// test cannot log into it.
func within(t *testing.T, d time.Duration, what string, fn func() []string) {
	t.Helper()
	done := make(chan []string, 1)
	go func() { done <- fn() }()
	select {
	case problems := <-done:
		for _, p := range problems {
			t.Error(p)
		}
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

// hookSite is where a test puts the hook that panics.
type hookSite int

const (
	// onOption is the WithClientTrace option.
	onOption hookSite = iota
	// onContext is a trace on the call's context, without the option.
	onContext
	// onContextWithOption is a trace on the call's context, with a
	// WithClientTrace option of its own that does not panic.
	onContextWithOption
)

// panickingTrace returns a trace whose hook panics with hookPanic{hook, n}
// while armed is set; hook is "GetConn", "GotConn" or "ConnectStart".
func panickingTrace(hook string, armed *atomic.Bool, n *atomic.Int64) *httptrace.ClientTrace {
	fire := func() {
		if armed.Load() {
			panic(hookPanic{hook: hook, n: int(n.Load())})
		}
	}
	switch hook {
	case "GetConn":
		return &httptrace.ClientTrace{GetConn: func(string) { fire() }}
	case "GotConn":
		return &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { fire() }}
	default:
		return &httptrace.ClientTrace{ConnectStart: func(string, string) { fire() }}
	}
}

// TestClientTracePanicIsRaisedOnTheCaller pins K28, K28b and K28c through the
// transport resolve builds: net/http calls a warm request's GetConn with its
// HTTP/2 connection pool locked and the stream reserved
// (internal/http2/client_conn_pool.go:52-61), and GotConn once the stream is
// reserved, so a panic unwinding from either would leave the pool locked or
// the stream counted. The shield recovers it inside the hook, whether the
// hook came from WithClientTrace or from a trace on the call's context, and
// raises it from roundTrip once net/http has returned: the caller sees the
// panic, the token is free, the pool is not locked and no stream is leaked,
// which a server allowing 2 streams per connection makes visible.
func TestClientTracePanicIsRaisedOnTheCaller(t *testing.T) {
	const (
		streams = 2
		wedged  = 10 * time.Second
	)
	tests := map[string]struct {
		hook string
		site hookSite
	}{
		"success: a warm GetConn panic from WithClientTrace":                     {hook: "GetConn", site: onOption},
		"success: a GotConn panic from WithClientTrace":                          {hook: "GotConn", site: onOption},
		"success: a warm GetConn panic from the call's context":                  {hook: "GetConn", site: onContext},
		"success: a GotConn panic from the call's context":                       {hook: "GotConn", site: onContext},
		"success: a warm GetConn panic from the call's context, with the option": {hook: "GetConn", site: onContextWithOption},
		"success: a GotConn panic from the call's context, with the option":      {hook: "GotConn", site: onContextWithOption},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{MaxConcurrentStreams: streams})
			var (
				armed    atomic.Bool
				n        atomic.Int64
				observed atomic.Int64 // calls of the non-panicking option's hooks
			)
			trace := panickingTrace(tt.hook, &armed, &n)
			callCtx := t.Context()
			var opts []ClientOption
			switch tt.site {
			case onOption:
				opts = append(opts, WithClientTrace(trace))
			case onContextWithOption:
				opts = append(opts, WithClientTrace(&httptrace.ClientTrace{
					GetConn: func(string) { observed.Add(1) },
					GotConn: func(httptrace.GotConnInfo) { observed.Add(1) },
				}))
				callCtx = httptrace.WithClientTrace(callCtx, trace)
			case onContext:
				callCtx = httptrace.WithClientTrace(callCtx, trace)
			}
			c := loopbackConfig(t, srv, opts...)
			get := func(path string) getResult {
				ctx, cancel := context.WithTimeout(callCtx, 5*time.Second)
				defer cancel()
				return getVia(ctx, c.transport, srv.URL()+path, 0)
			}
			if r := get("/warm"); r.err != nil || r.protoMajor != 2 {
				t.Fatalf("warm-up GET = HTTP/%d %v", r.protoMajor, r.err)
			}
			observedWarm := observed.Load()
			armed.Store(true)
			for i := range streams + 1 {
				n.Store(int64(i))
				within(t, wedged, "the panicking GET #"+strconv.Itoa(i), func() []string {
					p := recovered(func() { get("/panic/" + strconv.Itoa(i)) })
					if hp, ok := p.(hookPanic); !ok || hp.n != i {
						return []string{fmt.Sprintf("GET #%d panicked with %v, want the hook's value #%d", i, p, i)}
					}
					return nil
				})
			}
			armed.Store(false)
			if tt.site == onContextWithOption && observed.Load() == observedWarm {
				t.Error("the option's hooks did not run for the panicking GETs, want them run before the context's")
			}
			// More requests than the connection has streams: a leaked
			// reservation stalls them under strict accounting.
			within(t, wedged, "the GETs after the panics", func() []string {
				var problems []string
				for i := range 2 * streams {
					if r := get("/after/" + strconv.Itoa(i)); r.err != nil || r.status != http.StatusOK {
						problems = append(problems, fmt.Sprintf("GET after the panics #%d = %d %v", i, r.status, r.err))
					}
				}
				return problems
			})
			st := c.transport.stats()
			if srv.Accepts() != 1 || srv.OverLimit() != 0 || st.Dials != 1 {
				t.Errorf("accepts %d, over the limit %d, stats %+v; want 1 connection throughout", srv.Accepts(), srv.OverLimit(), st)
			}
		})
	}
}

// TestClientTraceColdDialPanic pins K28c on the dial: a ConnectStart hook on
// the call's context runs on net/http's dialing goroutine, through the net
// package's own hooks, where an escaped panic would end the process. The
// shield recovers it there and raises it on the caller, and the client dials
// again on the next call.
func TestClientTraceColdDialPanic(t *testing.T) {
	srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
	var (
		armed atomic.Bool
		n     atomic.Int64
	)
	armed.Store(true)
	callCtx := httptrace.WithClientTrace(t.Context(), panickingTrace("ConnectStart", &armed, &n))
	c := loopbackConfig(t, srv)
	get := func(path string) getResult {
		ctx, cancel := context.WithTimeout(callCtx, 5*time.Second)
		defer cancel()
		return getVia(ctx, c.transport, srv.URL()+path, 0)
	}
	within(t, 10*time.Second, "the cold GET", func() []string {
		if p := recovered(func() { get("/cold") }); p != (hookPanic{hook: "ConnectStart"}) {
			return []string{fmt.Sprintf("cold GET panicked with %v, want the ConnectStart hook's value", p)}
		}
		return nil
	})
	armed.Store(false)
	if r := get("/after"); r.err != nil || r.status != http.StatusOK {
		t.Errorf("GET after the panic = %d %v, want 200", r.status, r.err)
	}
}

// TestUntracedContext pins the context roundTrip hands net/http when a
// caller trace is shielded: it hides httptrace's values, so net/http cannot
// compose the unshielded caller trace again, and keeps everything else of
// the wrapped context.
func TestUntracedContext(t *testing.T) {
	type key struct{}
	cause := errors.New("the caller gave up")
	deadline := time.Now().Add(time.Hour)
	withDeadline, stopDeadline := context.WithDeadline(context.WithValue(t.Context(), key{}, "kept"), deadline)
	defer stopDeadline()
	parent, cancel := context.WithCancelCause(withDeadline)
	defer cancel(nil)
	traced := httptrace.WithClientTrace(parent, &httptrace.ClientTrace{GetConn: func(string) {}, ConnectStart: func(string, string) {}})
	ctx := untracedContext{traced}
	if tr := httptrace.ContextClientTrace(ctx); tr != nil {
		t.Errorf("ContextClientTrace = %p, want none", tr)
	}
	if got := ctx.Value(key{}); got != "kept" {
		t.Errorf("Value(key) = %v, want the wrapped context's", got)
	}
	if got, ok := ctx.Deadline(); !ok || !got.Equal(deadline) {
		t.Errorf("Deadline() = %v, %t; want the wrapped context's", got, ok)
	}
	before := runtime.NumGoroutine()
	children := make([]context.CancelFunc, 0, 100)
	for range 100 {
		_, stop := context.WithCancel(ctx)
		children = append(children, stop)
	}
	if grew := runtime.NumGoroutine() - before; grew >= 50 {
		t.Errorf("100 child contexts started %d goroutines, want none: the wrapped cancellation must be found through Value", grew)
	}
	cancel(cause)
	<-ctx.Done()
	if got := context.Cause(ctx); got != cause { //nolint:errorlint // identity is the assertion
		t.Errorf("Cause = %v, want the wrapped context's", got)
	}
	for _, stop := range children {
		stop()
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
