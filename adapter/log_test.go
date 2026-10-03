// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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

package adapter

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// logStep is what a logProvider's request returns: a result with text, or
// err, or neither when both are empty. With cancel set the request first
// cancels the call's context; with wait set it first waits for the
// context to end and returns its error; with panic set it panics with it.
type logStep struct {
	text   string
	err    error
	cancel bool
	wait   bool
	panic  any
}

// logProvider is a provider of TestLogRecords. Each request records a
// request body and a response body holding canary in its trace and then
// returns the next of steps.
type logProvider struct {
	canary string
	steps  []logStep
	n      int
	// cancel cancels the call's context.
	cancel context.CancelFunc
	// onDo, when not nil, is called at the start of each request.
	onDo func()
}

func (p *logProvider) Model() string { return "log-model" }

func (p *logProvider) Do(ctx context.Context, req *llm.Request) (*llm.Result, error) {
	req.Trace.RecordRequest("log-api", []byte(`{"request":"`+p.canary+`-request-body"}`))
	req.Trace.RecordResponse([]byte(`{"response":"`+p.canary+`-response-body"}`), nil)
	if p.onDo != nil {
		p.onDo()
	}
	s := p.steps[p.n]
	p.n++
	switch {
	case s.panic != nil:
		panic(s.panic)
	case s.cancel:
		p.cancel()
	case s.wait:
		<-ctx.Done()
		return nil, ctx.Err()
	}
	switch {
	case s.err != nil:
		return nil, s.err
	case s.text == "":
		return nil, nil
	}
	return textResult(s.text), nil
}

// wantAttempt is an attempt record TestLogRecords expects: its outcome,
// its status for a status outcome, and the word of the retry that follows,
// if any.
type wantAttempt struct {
	outcome string
	status  int
	retry   string
}

// TestLogRecords checks the Adapter's records against the attribute sets
// of WithLogger's godoc: for each way an attempt ends, the exact
// attributes of each attempt record at DEBUG and of the call record at
// INFO, the attempt numbers, the request id the response carries, and the
// retry words. It plants a canary in each place a provider's or a
// caller's text can come from (the state, a question's instructions, a
// criterion, the provider's recorded request and response bodies, a
// status error's body, a non-answer's message, a transient error's text,
// which becomes a retry reason's message, a custom provider's own error,
// and the model's malformed output) and checks that no resolved attribute
// value, key or message holds it. A request refused before its evaluation
// logs nothing, and an Adapter without a logger runs without one.
func TestLogRecords(t *testing.T) {
	const questions = `{"answer":{"type":"noul","instructions":"CANARY-instructions"},"stars":{"type":"score","instructions":"Rating.","criteria":["CANARY-criterion","Good."]}}`
	const answerText = `{"answers":{"answer":0.75,"stars":{"0":0.25,"1":0.75}}}`
	fast := WithRetry(DefaultRetry().MaxRetries(1).Backoff(time.Millisecond, time.Millisecond, 0))
	noRetry := WithRetry(NoRetry())
	tests := map[string]struct {
		steps    func(canary string) []logStep
		opts     []Option
		retryHdr string
		// timeout, when not zero, is the call's deadline from its start.
		timeout time.Duration
		// status is the response's status, 0 when RoundTrip returns an
		// error.
		status    int
		attempts  []wantAttempt
		answers   int
		retries   int
		malformed int
		sdkRetry  int
	}{
		"ok": {
			steps:    func(string) []logStep { return []logStep{{text: answerText}} },
			status:   200,
			attempts: []wantAttempt{{outcome: "ok"}},
			answers:  2,
		},
		"status": {
			steps: func(c string) []logStep {
				return []logStep{{err: &llm.StatusError{StatusCode: 400, Body: []byte(c + "-status-body")}}}
			},
			opts:     []Option{noRetry},
			status:   400,
			attempts: []wantAttempt{{outcome: "status", status: 400}},
		},
		"status, retried, then ok": {
			steps: func(c string) []logStep {
				return []logStep{{err: &llm.StatusError{StatusCode: 503, Body: []byte(c + "-retried-body")}}, {text: answerText}}
			},
			opts:     []Option{fast},
			status:   200,
			attempts: []wantAttempt{{outcome: "status", status: 503, retry: "provider_error"}, {outcome: "ok"}},
			answers:  2,
			retries:  1,
		},
		"timeout": {
			steps:    func(c string) []logStep { return []logStep{{err: &llm.TimeoutError{Err: errors.New(c + "-timeout")}}} },
			opts:     []Option{noRetry},
			attempts: []wantAttempt{{outcome: "timeout"}},
		},
		"connection": {
			steps: func(c string) []logStep {
				return []logStep{{err: &llm.ConnectionError{Err: errors.New(c + "-connection")}}}
			},
			opts:     []Option{noRetry},
			attempts: []wantAttempt{{outcome: "connection"}},
		},
		"connection, retried, then ok": {
			steps: func(c string) []logStep {
				return []logStep{{err: &llm.ConnectionError{Err: errors.New(c + "-retried-connection")}}, {text: answerText}}
			},
			opts:     []Option{fast},
			status:   200,
			attempts: []wantAttempt{{outcome: "connection", retry: "provider_error"}, {outcome: "ok"}},
			answers:  2,
			retries:  1,
		},
		"non-answer": {
			steps:    func(c string) []logStep { return []logStep{{err: &llm.NonAnswerError{Message: c + "-non-answer"}}} },
			opts:     []Option{noRetry},
			status:   424,
			attempts: []wantAttempt{{outcome: "non_answer"}},
		},
		"malformed, corrected once, still malformed": {
			steps: func(c string) []logStep {
				return []logStep{{text: `{"answers":{"answer":"` + c + `-output"}}`}, {text: c + "-output"}}
			},
			opts:      []Option{WithMalformedRetries(1)},
			status:    200,
			attempts:  []wantAttempt{{outcome: "malformed", retry: "malformed_structure"}, {outcome: "malformed"}},
			malformed: 1,
		},
		"malformed, then corrected": {
			steps:     func(c string) []logStep { return []logStep{{text: c + "-output"}, {text: answerText}} },
			opts:      []Option{WithMalformedRetries(1)},
			status:    200,
			attempts:  []wantAttempt{{outcome: "malformed", retry: "malformed_structure"}, {outcome: "ok"}},
			answers:   2,
			malformed: 1,
		},
		"custom provider error": {
			steps:    func(c string) []logStep { return []logStep{{err: errors.New(c + "-custom-error")}} },
			opts:     []Option{noRetry},
			status:   424,
			attempts: []wantAttempt{{outcome: "error"}},
		},
		"no result and no error": {
			steps:    func(string) []logStep { return []logStep{{}} },
			opts:     []Option{noRetry},
			status:   424,
			attempts: []wantAttempt{{outcome: "error"}},
		},
		"cancelled during the attempt": {
			steps: func(c string) []logStep {
				return []logStep{{cancel: true, err: &llm.ConnectionError{Err: fmt.Errorf("%s-cancelled: %w", c, context.Canceled)}}}
			},
			attempts: []wantAttempt{{outcome: "error"}},
		},
		"deadline passed, the provider's own error": {
			steps:    func(string) []logStep { return []logStep{{wait: true}} },
			timeout:  50 * time.Millisecond,
			attempts: []wantAttempt{{outcome: "timeout"}},
		},
		"a retry of the SDK": {
			steps:    func(string) []logStep { return []logStep{{text: answerText}} },
			retryHdr: "2",
			status:   200,
			attempts: []wantAttempt{{outcome: "ok"}},
			answers:  2,
			sdkRetry: 2,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			canary := canaryKey(t)
			h := &recordHandler{level: slog.LevelDebug}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.timeout != 0 {
				ctx, cancel = context.WithTimeout(ctx, tt.timeout)
				defer cancel()
			}
			p := &logProvider{canary: canary, steps: tt.steps(canary), cancel: cancel}
			ad := newAdapter(t, append([]Option{WithLogger(slog.New(h)), WithFactory("logfake", factoryOf(p))}, tt.opts...)...)
			body := `{"state":"` + canary + `-state","model":"logfake:m1","questions":` + strings.ReplaceAll(questions, "CANARY", canary) + `}`
			req := requestWith(ctx, http.MethodPost, systemOnePath, nil, retryCountHeader(tt.retryHdr))
			req.Body = rawRequest(ctx, http.MethodPost, systemOnePath, body).Body
			resp, err := ad.RoundTrip(req)
			id := ""
			switch {
			case tt.status == 0 && err == nil:
				_ = resp.Body.Close()
				t.Fatalf("RoundTrip answered %d, want an error", resp.StatusCode)
			case tt.status == 0:
			case err != nil:
				t.Fatalf("RoundTrip: %v", err)
			default:
				_ = resp.Body.Close()
				if resp.StatusCode != tt.status {
					t.Fatalf("status %d, want %d", resp.StatusCode, tt.status)
				}
				id = resp.Header.Get(requestIDHeader)
			}
			records := h.all()
			if len(records) != len(tt.attempts)+1 {
				t.Fatalf("%d records, want %d attempt records and the call's: %v", len(records), len(tt.attempts), records)
			}
			if id == "" {
				id = records[0].Attrs["request_id"]
			}
			for i, want := range tt.attempts {
				attrs := map[string]string{"request_id": id, "provider": "logfake", "model": "m1", "api": "log-api", "attempt": strconv.Itoa(i + 1), "outcome": want.outcome, "duration": "<duration>"}
				if want.outcome == "status" {
					attrs["status"] = strconv.Itoa(want.status)
				}
				if want.retry != "" {
					attrs["retry"] = want.retry
				}
				checkRecord(t, records[i], slog.LevelDebug, "attempt", attrs, "duration")
			}
			call := map[string]string{
				"request_id": id, "model": "m1", "answers": strconv.Itoa(tt.answers), "n_retries": strconv.Itoa(tt.retries),
				"n_retries_malformed_structure": strconv.Itoa(tt.malformed), "latency": "<duration>",
			}
			if tt.sdkRetry != 0 {
				call["sdk_retry_count"] = strconv.Itoa(tt.sdkRetry)
			}
			checkRecord(t, records[len(records)-1], slog.LevelInfo, "call", call, "latency")
			if n := strings.Count(fmt.Sprint(records), canary); n != 0 {
				t.Errorf("the records hold the canary %d times: %v", n, records)
			}
		})
	}
	t.Run("through the SDK, with its key", func(t *testing.T) {
		canary := canaryKey(t)
		h := &recordHandler{level: slog.LevelDebug}
		p := &logProvider{canary: canary, steps: []logStep{{err: &llm.StatusError{StatusCode: 503, Body: []byte(canary + "-status-body")}}, {text: noulAnswer}}}
		ad := newAdapter(t, WithLogger(slog.New(h)), WithFactory("logfake", factoryOf(p)), fast)
		c := sdkClient(t, ad, true, decision.WithAPIKey(canary), decision.WithModel("logfake:m1"))
		if _, err := c.SystemOne(t.Context(), canary+"-state", noulQuestions(t)); err != nil {
			t.Fatalf("SystemOne: %v", err)
		}
		records := h.all()
		if len(records) != 3 {
			t.Fatalf("%d records, want 3: %v", len(records), records)
		}
		if n := strings.Count(fmt.Sprint(records), canary); n != 0 {
			t.Errorf("the records hold the key's canary %d times: %v", n, records)
		}
	})
	t.Run("provider panics in a retry", func(t *testing.T) {
		const value = "the provider panicked in a retry"
		h := &recordHandler{level: slog.LevelDebug}
		p := &logProvider{canary: "c", steps: []logStep{{err: &llm.StatusError{StatusCode: 503}}, {panic: value}, {text: noulAnswer}}}
		var beforeRetry []loggedRecord
		p.onDo = func() {
			if p.n == 1 {
				beforeRetry = h.all()
			}
		}
		ad := newAdapter(t, WithLogger(slog.New(h)), WithFactory("logfake", factoryOf(p)), fast)
		c := sdkClient(t, ad, true, decision.WithModel("logfake:m1"))
		var got any
		func() {
			defer func() { got = recover() }()
			_, _ = c.SystemOne(t.Context(), "state", noulQuestions(t))
		}()
		if got != value {
			t.Fatalf("the caller recovered %v, want the provider's panic %q", got, value)
		}
		if len(beforeRetry) != 1 {
			t.Errorf("%d records when the retry started, want the first attempt's: %v", len(beforeRetry), beforeRetry)
		}
		records := h.all()
		if len(records) != 1 {
			t.Fatalf("%d records, want the first attempt's alone and no call record: %v", len(records), records)
		}
		checkRecord(t, records[0], slog.LevelDebug, "attempt", map[string]string{
			"request_id": records[0].Attrs["request_id"], "provider": "logfake", "model": "m1", "api": "log-api",
			"attempt": "1", "outcome": "status", "status": "503", "duration": "<duration>", "retry": "provider_error",
		}, "duration")
		assertUnlocked(t, ad)
		if _, err := c.SystemOne(t.Context(), "state", noulQuestions(t)); err != nil {
			t.Fatalf("a call after the panic: %v", err)
		}
		if n := len(h.all()); n != 3 {
			t.Errorf("%d records after a call that answered, want 3", n)
		}
	})
	t.Run("the retry predicate panics", func(t *testing.T) {
		const value = "the predicate panicked"
		h := &recordHandler{level: slog.LevelDebug}
		p := &logProvider{canary: "c", steps: []logStep{{err: &llm.StatusError{StatusCode: http.StatusTeapot}}}}
		policy := DefaultRetry().MaxRetries(1).Backoff(time.Millisecond, time.Millisecond, 0).Predicate(func(error) bool { panic(value) })
		ad := newAdapter(t, WithLogger(slog.New(h)), WithFactory("logfake", factoryOf(p)), WithRetry(policy))
		var got any
		func() {
			defer func() { got = recover() }()
			_, _, _ = send(t, ad, rawRequest(t.Context(), http.MethodPost, systemOnePath, `{"state":"s","model":"logfake:m1","questions":{"answer":{"type":"noul"}}}`))
		}()
		if got != value {
			t.Fatalf("the caller recovered %v, want the predicate's panic %q", got, value)
		}
		records := h.all()
		if len(records) != 1 {
			t.Fatalf("%d records, want the finished attempt's alone and no call record: %v", len(records), records)
		}
		checkRecord(t, records[0], slog.LevelDebug, "attempt", map[string]string{
			"request_id": records[0].Attrs["request_id"], "provider": "logfake", "model": "m1", "api": "log-api",
			"attempt": "1", "outcome": "status", "status": "418", "duration": "<duration>",
		}, "duration")
		assertUnlocked(t, ad)
	})
	t.Run("through NewClient, with the placeholder key", func(t *testing.T) {
		h := &recordHandler{level: slog.LevelDebug}
		ad := newAdapter(t, WithLogger(slog.New(h)), WithFactory("logfake", factoryOf(&logProvider{canary: "c", steps: []logStep{{text: noulAnswer}}})), WithDefaultModel("logfake:m1"))
		c := sdkClient(t, ad, false)
		if err := evaluateOn(t, c); err != nil {
			t.Fatalf("SystemOne: %v", err)
		}
		records := h.all()
		if len(records) != 2 {
			t.Fatalf("%d records, want 2: %v", len(records), records)
		}
		if strings.Contains(fmt.Sprint(records), PlaceholderAPIKey) {
			t.Errorf("the records hold the placeholder key: %v", records)
		}
	})
}

// TestLogRecordsRefused checks that a request refused before its
// evaluation, and a call on a closed Adapter, write no record, and that an
// Adapter without a logger answers with none.
func TestLogRecordsRefused(t *testing.T) {
	deep := strings.Repeat("[", 256) + strings.Repeat("]", 256)
	tests := map[string]struct {
		method, path, body string
		ctx                func(context.Context) context.Context
		closed             bool
		status             int
	}{
		"a body that is not JSON":    {method: http.MethodPost, path: systemOnePath, body: `{`, status: 400},
		"an unknown path":            {method: http.MethodPost, path: "/v1/other", body: noulBody, status: 404},
		"the models list":            {method: http.MethodGet, path: modelsPath, status: 200},
		"a null state":               {method: http.MethodPost, path: systemOnePath, body: `{"state":null,"model":"logfake:m1","questions":{"answer":{"type":"noul"}}}`, status: 422},
		"no questions":               {method: http.MethodPost, path: systemOnePath, body: `{"state":"s","model":"logfake:m1","questions":{}}`, status: 422},
		"a state nested 256 levels":  {method: http.MethodPost, path: systemOnePath, body: `{"state":` + deep + `,"model":"logfake:m1","questions":{"answer":{"type":"noul"}}}`, status: 422},
		"a model without a provider": {method: http.MethodPost, path: systemOnePath, body: `{"state":"s","model":"other:m1","questions":{"answer":{"type":"noul"}}}`, status: 400},
		"an invalid retry policy": {
			method: http.MethodPost, path: systemOnePath, body: `{"state":"s","model":"logfake:m1","questions":{"answer":{"type":"noul"}}}`, status: 400,
			ctx: func(ctx context.Context) context.Context { return ContextWithRetry(ctx, DefaultRetry().MaxRetries(-1)) },
		},
		"a closed Adapter": {method: http.MethodPost, path: systemOnePath, body: noulBody, closed: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := &recordHandler{level: slog.LevelDebug}
			p := &logProvider{canary: "c", steps: []logStep{{text: noulAnswer}}}
			ad := newAdapter(t, WithLogger(slog.New(h)), WithFactory("logfake", factoryOf(p)))
			if tt.closed {
				if err := ad.Close(); err != nil {
					t.Fatal(err)
				}
			}
			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(ctx)
			}
			status, _, err := send(t, ad, rawRequest(ctx, tt.method, tt.path, tt.body))
			switch {
			case tt.closed && !isClosedError(err):
				t.Fatalf("RoundTrip: %v, want the closed Error", err)
			case !tt.closed && (err != nil || status != tt.status):
				t.Fatalf("RoundTrip: %d %v, want %d", status, err, tt.status)
			}
			if records := h.all(); len(records) != 0 {
				t.Errorf("records %v, want none", records)
			}
			if p.n != 0 {
				t.Errorf("the provider was called %d times", p.n)
			}
		})
	}
	t.Run("no logger", func(t *testing.T) {
		// Each way of observing an attempt runs without a logger: a
		// transient retry, a corrective retry, and an answer.
		for _, steps := range [][]logStep{
			{{err: &llm.StatusError{StatusCode: 503}}, {text: noulAnswer}},
			{{text: "not JSON"}, {text: noulAnswer}},
		} {
			p := &logProvider{canary: "c", steps: steps}
			ad := newAdapter(t, WithFactory("logfake", factoryOf(p)), WithRetry(DefaultRetry().MaxRetries(1).Backoff(time.Millisecond, time.Millisecond, 0)), WithMalformedRetries(1))
			status, _, err := send(t, ad, rawRequest(t.Context(), http.MethodPost, systemOnePath, `{"state":"s","model":"logfake:m1","questions":{"answer":{"type":"noul"}}}`))
			if err != nil || status != 200 || p.n != 2 {
				t.Fatalf("RoundTrip: %d %v after %d requests, want 200 after 2", status, err, p.n)
			}
		}
	})
}

// checkRecord checks rec's level, message and attributes against want,
// where each key of durations must hold a duration of at least zero, which
// want writes as "<duration>".
func checkRecord(t *testing.T, rec loggedRecord, level slog.Level, message string, want map[string]string, durations ...string) {
	t.Helper()
	got := maps.Clone(rec.Attrs)
	for _, k := range durations {
		d, err := time.ParseDuration(got[k])
		if err != nil || d < 0 {
			t.Errorf("%s record: %s = %q, want a duration of at least zero", message, k, got[k])
		}
		got[k] = "<duration>"
	}
	if rec.Level != level || rec.Message != message {
		t.Errorf("record %v %q, want %v %q", rec.Level, rec.Message, level, message)
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("%s record's attributes (-want +got):\n%s", message, diff)
	}
}

// unwatchedCalls are the names, by package, whose use writes to a logger or
// a stream no WithLogger logger stands for: slog's package-level logger, the
// log package's, and the standard output and error. os.Stdout and os.Stderr
// are listed as values, so that any use of them is found: fmt.Fprint* to
// them, their Write methods and log.New with one as its output. fmt.Fprint*
// to another writer stays allowed.
var unwatchedCalls = map[string][]string{
	"log/slog": {"Debug", "DebugContext", "Default", "Error", "ErrorContext", "Info", "InfoContext", "Log", "LogAttrs", "SetDefault", "SetLogLoggerLevel", "Warn", "WarnContext"},
	"log":      {"Fatal", "Fatalf", "Fatalln", "Panic", "Panicf", "Panicln", "Print", "Printf", "Println", "Default", "Output"},
	"fmt":      {"Print", "Printf", "Println"},
	"os":       {"Stdout", "Stderr"},
}

// unwatchedBuiltins are the builtin functions that write to the standard
// error.
var unwatchedBuiltins = []string{"print", "println"}

// unwatchedLogging parses the Go file src, named name, and returns each use
// of unwatchedCalls in it as "<file>:<line>:<column>: <path>.<name>",
// resolving the name each import has in the file, and each call of
// unwatchedBuiltins as "<file>:<line>:<column>: builtin <name>". The file is
// parsed without resolving names, so a function of the package named print
// or println would be reported as the builtin too; the module declares
// none.
func unwatchedLogging(name, src string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	local := map[string]string{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if _, ok := unwatchedCalls[path]; !ok {
			continue
		}
		n := filepath.Base(path)
		if imp.Name != nil {
			n = imp.Name.Name
		}
		local[n] = path
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if fn, ok := call.Fun.(*ast.Ident); ok && slices.Contains(unwatchedBuiltins, fn.Name) {
				found = append(found, fmt.Sprintf("%s: builtin %s", fset.Position(fn.Pos()), fn.Name))
			}
			return true
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if path, ok := local[x.Name]; ok && slices.Contains(unwatchedCalls[path], sel.Sel.Name) {
			found = append(found, fmt.Sprintf("%s: %s.%s", fset.Position(sel.Pos()), path, sel.Sel.Name))
		}
		return true
	})
	return found, nil
}

// TestNoUnwatchedLogging checks that no non-test Go file of the module
// writes to slog's package-level logger, the log package or the standard
// output, so that every record the Adapter writes goes to the WithLogger
// logger, whose records TestLogRecords checks, and no body or text can
// reach a logger the tests do not watch. A planted file shows that each
// form is found, an import renamed included, and that writing to another
// writer is not. The walk skips what ./... skips: testdata and directories
// whose names begin with a dot or an underscore, and vendor, whose files
// are third-party code a checkout may hold beside the module's own.
func TestNoUnwatchedLogging(t *testing.T) {
	const planted = `package p

import (
	"fmt"
	stdlog "log"
	"log/slog"
	"os"
	"strings"
)

func f() {
	slog.Info("x")
	slog.Default().Info("x")
	stdlog.Printf("x")
	fmt.Println("x")
	_ = fmt.Sprint("x")
	print("x")
	println("x")
	fmt.Fprintln(os.Stderr, "x")
	_, _ = os.Stdout.Write(nil)
	_ = stdlog.New(os.Stderr, "", 0)
	var b strings.Builder
	fmt.Fprint(&b, "x")
}
`
	got, err := unwatchedLogging("planted.go", planted)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"planted.go:12:2: log/slog.Info",
		"planted.go:13:2: log/slog.Default",
		"planted.go:14:2: log.Printf",
		"planted.go:15:2: fmt.Println",
		"planted.go:17:2: builtin print",
		"planted.go:18:2: builtin println",
		"planted.go:19:15: os.Stderr",
		"planted.go:20:9: os.Stdout",
		"planted.go:21:17: os.Stderr",
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Fatalf("the planted file's calls (-want +got):\n%s", diff)
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	fsys := root.FS()
	var files int
	err = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && (d.Name() == "testdata" || d.Name() == "vendor" || strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) && path != ".":
			return filepath.SkipDir
		case d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go"):
			return nil
		}
		src, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		files++
		found, err := unwatchedLogging(path, string(src))
		if err != nil {
			return err
		}
		for _, f := range found {
			t.Errorf("a call that logs where WithLogger's logger does not: %s", f)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 10 {
		t.Fatalf("read %d non-test files, want the module's", files)
	}
}
