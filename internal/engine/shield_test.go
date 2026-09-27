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

package engine

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptrace"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
)

// recovered runs fn and returns the value it panicked with, or nil.
func recovered(fn func()) (v any) {
	defer func() { v = recover() }()
	fn()
	return nil
}

// hookPanic is the value the tests' hooks panic with.
type hookPanic struct {
	hook string
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

// TestUntracedContext pins the context RoundTrip hands net/http when a
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
