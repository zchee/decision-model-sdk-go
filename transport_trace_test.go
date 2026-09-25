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
	"slices"
	"strconv"
	"strings"
	"sync"
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
	sh := newShield(t.Context(), &in, slog.New(slog.DiscardHandler))
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
	empty := newShield(t.Context(), &httptrace.ClientTrace{}, slog.New(slog.DiscardHandler))
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
// which a server allowing 2 streams per connection makes visible. roundTrip
// closes the response body before it panics again; a server that holds each
// body open until the client resets the stream, with a call context that is
// never cancelled, shows that close is what frees the stream.
func TestClientTracePanicIsRaisedOnTheCaller(t *testing.T) {
	const (
		streams = 2
		wedged  = 10 * time.Second
	)
	tests := map[string]struct {
		hook     string
		site     hookSite
		holdBody bool // the server holds each panicking call's body open
	}{
		"success: a GotConn panic while the server holds the body open":          {hook: "GotConn", site: onOption, holdBody: true},
		"success: a warm GetConn panic from WithClientTrace":                     {hook: "GetConn", site: onOption},
		"success: a GotConn panic from WithClientTrace":                          {hook: "GotConn", site: onOption},
		"success: a warm GetConn panic from the call's context":                  {hook: "GetConn", site: onContext},
		"success: a GotConn panic from the call's context":                       {hook: "GotConn", site: onContext},
		"success: a warm GetConn panic from the call's context, with the option": {hook: "GetConn", site: onContextWithOption},
		"success: a GotConn panic from the call's context, with the option":      {hook: "GotConn", site: onContextWithOption},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{
				MaxConcurrentStreams: streams,
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					if tt.holdBody && strings.HasPrefix(r.URL.Path, "/panic/") {
						w.(http.Flusher).Flush()
						<-r.Context().Done() // until the client resets the stream
					}
				}),
			})
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
			getPanicking := get
			if tt.holdBody {
				// No deadline and no cancel: only closing the body resets the
				// stream the server holds.
				getPanicking = func(path string) getResult {
					return getVia(context.WithoutCancel(callCtx), c.transport, srv.URL()+path, 0)
				}
			}
			if r := get("/warm"); r.err != nil || r.protoMajor != 2 {
				t.Fatalf("warm-up GET = HTTP/%d %v", r.protoMajor, r.err)
			}
			observedWarm := observed.Load()
			armed.Store(true)
			for i := range streams + 1 {
				n.Store(int64(i))
				within(t, wedged, "the panicking GET #"+strconv.Itoa(i), func() []string {
					p := recovered(func() { getPanicking("/panic/" + strconv.Itoa(i)) })
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

// TestClientTraceMergeOrderAndCopy pins how roundTrip merges the two caller
// traces: the option's hooks run before those of the trace on the call's
// context, and the merge goes into a copy of the option's trace, so one
// call's context hooks never reach a later call of the client.
func TestClientTraceMergeOrderAndCopy(t *testing.T) {
	var calls []string
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		httptrace.ContextClientTrace(req.Context()).GetConn("api.typesafe.ai:443")
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
	})
	c := mustResolve(t, noEnv, WithAPIKey(testKey), WithRoundTripper(rt),
		WithClientTrace(&httptrace.ClientTrace{GetConn: func(string) { calls = append(calls, "option") }}))
	traced := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{GetConn: func(string) { calls = append(calls, "context") }})
	for i, ctx := range []context.Context{traced, t.Context(), traced} {
		if r := getVia(ctx, c.transport, "https://api.typesafe.ai/v1/models", 0); r.err != nil || r.status != http.StatusOK {
			t.Fatalf("call %d = %d %v", i, r.status, r.err)
		}
	}
	want := []string{"option", "context", "option", "option", "context"}
	if diff := gocmp.Diff(want, calls); diff != "" {
		t.Errorf("GetConn hooks run (-want +got):\n%s", diff)
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

// ctxLogKey is the context key whose value ctxRecorder keeps.
type ctxLogKey struct{}

// ctxRecord is a record as ctxRecorder keeps it.
type ctxRecord struct {
	level    slog.Level
	msg      string
	attrs    map[string]string
	ctxValue any // the record's context's value for ctxLogKey
}

// ctxRecorder is a slog.Handler that keeps every record with the value its
// context carries for ctxLogKey.
type ctxRecorder struct {
	mu      sync.Mutex
	records []ctxRecord
}

func (*ctxRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *ctxRecorder) Handle(ctx context.Context, rec slog.Record) error {
	attrs := map[string]string{}
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, ctxRecord{level: rec.Level, msg: rec.Message, attrs: attrs, ctxValue: ctx.Value(ctxLogKey{})})
	return nil
}

func (r *ctxRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }

func (r *ctxRecorder) WithGroup(string) slog.Handler { return r }

// all returns the records kept so far.
func (r *ctxRecorder) all() []ctxRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.records)
}

// assertHookRecords checks that got holds one WARN record per want entry, in
// order, each naming its hook, carrying the stack of the goroutine the hook
// panicked on (so it shows the hook's frame, fn), logged with the request's
// context, and never holding secret.
func assertHookRecords(t *testing.T, got []ctxRecord, want [][2]string, fn, secret string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%d records, want %d: %+v", len(got), len(want), got)
	}
	for i, r := range got {
		if r.level != slog.LevelWarn || r.msg != want[i][0] || r.attrs["hook"] != want[i][1] {
			t.Errorf("record %d = %v %q hook %q, want WARN %q hook %q", i, r.level, r.msg, r.attrs["hook"], want[i][0], want[i][1])
		}
		if stack := r.attrs["stack"]; !strings.Contains(stack, "panic(") || !strings.Contains(stack, fn) {
			t.Errorf("record %d stack does not show the hook %s panicking:\n%s", i, fn, stack)
		}
		if r.ctxValue != "the request's" {
			t.Errorf("record %d logged with context value %v, want the request's context", i, r.ctxValue)
		}
		for k, v := range r.attrs {
			if strings.Contains(r.msg, secret) || strings.Contains(v, secret) {
				t.Errorf("record %d attribute %s holds the panic value", i, k)
			}
		}
	}
}

// TestClientTraceShieldLogs pins the shield's records: a panic after an
// earlier one, the panic raised again on the caller and a panic after the
// request returned are each logged at WARN with the hook's name and stack,
// with the request's context, and never with the panic's value.
func TestClientTraceShieldLogs(t *testing.T) {
	const secret = "hunter2-in-a-panic-value"
	rec := &ctxRecorder{}
	ctx := context.WithValue(t.Context(), ctxLogKey{}, "the request's")
	sh := newShield(ctx, &httptrace.ClientTrace{
		GetConn:     func(string) { panic(secret + " first") },
		GotConn:     func(httptrace.GotConnInfo) { panic(secret + " second") },
		PutIdleConn: func(error) { panic(secret + " late") },
	}, slog.New(rec))
	sh.trace.GetConn("h:443")
	sh.trace.GotConn(httptrace.GotConnInfo{})
	if p := recovered(func() { sh.done(nil) }); p != secret+" first" {
		t.Errorf("done panicked with %v, want the first hook's value", p)
	}
	sh.trace.PutIdleConn(nil)
	sh.markReturned()
	assertHookRecords(t, rec.all(), [][2]string{
		{"transport: trace hook panic after an earlier one, recovered", "GotConn"},
		{"transport: trace hook panic, raised again on the caller", "GetConn"},
		{"transport: trace hook panic after the request returned, recovered", "PutIdleConn"},
	}, "TestClientTraceShieldLogs.func", secret)
}

// TestClientTraceRoundTripPanic pins roundTrip's deferred markReturned: when
// RoundTrip itself panics, its panic reaches the caller, the hook panic it
// replaces is logged rather than lost, and a hook that panics afterwards is
// logged as late rather than kept for a done that never comes.
func TestClientTraceRoundTripPanic(t *testing.T) {
	rec := &ctxRecorder{}
	var captured *httptrace.ClientTrace
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		captured = httptrace.ContextClientTrace(req.Context())
		captured.GetConn("h:443")
		panic("the round tripper failed")
	})
	c := mustResolve(t, noEnv, WithAPIKey(testKey), WithRoundTripper(rt), WithLogger(slog.New(rec)), WithClientTrace(&httptrace.ClientTrace{
		GetConn: func(string) { panic(hookPanic{hook: "GetConn"}) },
		GotConn: func(httptrace.GotConnInfo) { panic(hookPanic{hook: "GotConn"}) },
	}))
	ctx := context.WithValue(t.Context(), ctxLogKey{}, "the request's")
	if p := recovered(func() { getVia(ctx, c.transport, "https://api.typesafe.ai/v1/models", 0) }); p != "the round tripper failed" {
		t.Fatalf("roundTrip panicked with %v, want the round tripper's panic", p)
	}
	captured.GotConn(httptrace.GotConnInfo{})
	assertHookRecords(t, rec.all(), [][2]string{
		{"transport: trace hook panic replaced by a RoundTrip panic, recovered", "GetConn"},
		{"transport: trace hook panic after the request returned, recovered", "GotConn"},
	}, "TestClientTraceRoundTripPanic.func", "never set")
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
		if w.Message != "transport: trace hook panic after the request returned, recovered" || attrs["hook"] != "PutIdleConn" ||
			!strings.Contains(attrs["stack"], "TestClientTraceLatePanicIsLogged.func") {
			t.Errorf("WARN %q hook %q, want the late-panic record naming PutIdleConn with its stack:\n%s", w.Message, attrs["hook"], attrs["stack"])
		}
	}
	if srv.Accepts() != 1 {
		t.Errorf("accepts %d, want 1: the connection went back to the pool", srv.Accepts())
	}
}
