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

// This file holds the helpers of the tests in seam_test.go and
// evaluate_test.go that need packages those files do not import: their
// existing lines, the import declarations included, stay as they are, and
// their tests are added after them.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/fake"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/schema"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// retryCountLiteral is the one header name RoundTrip may read, as Go source
// spells the string literal.
const retryCountLiteral = `"X-TypeSafe-Retry-Count"`

// packageSources returns the source of every non-test Go file of the
// package in the current directory, by file name.
func packageSources(t testing.TB) map[string]string {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package's files: %v", err)
	}
	out := make(map[string]string)
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		out[path] = string(b)
	}
	return out
}

// requestFieldViolations parses sources, Go files of package adapter by
// name, and returns the number of methods RoundTrip with a receiver of type
// *Adapter, and each use of their *http.Request parameter that is not one
// of these: req.Method and req.URL.Path read as values; req.Context()
// called; req.Body compared with nil, closed with req.Body.Close(), or read
// with io.ReadAll(req.Body); req.Header.Get called with the string literal
// "X-TypeSafe-Retry-Count"; and req as the value of the field Request of an
// http.Response composite literal. A use inside a function literal is a
// violation too, since a closure keeps req. It also reports the literal
// "X-TypeSafe-Retry-Count" when the sources hold it other than once.
func requestFieldViolations(sources map[string]string) (roundTrips int, violations []string) {
	fset := token.NewFileSet()
	literals := 0
	for name, src := range sources {
		f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			return 0, []string{"parse " + name + ": " + err.Error()}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == retryCountLiteral {
				literals++
			}
			return true
		})
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "RoundTrip" || !isAdapterReceiver(fn) {
				continue
			}
			roundTrips++
			params := fn.Type.Params.List
			if len(params) != 1 || len(params[0].Names) != 1 {
				violations = append(violations, fset.Position(fn.Pos()).String()+": RoundTrip does not take one named parameter")
				continue
			}
			req := params[0].Names[0].Name
			if req == "_" {
				continue
			}
			var stack []ast.Node
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				if id, ok := n.(*ast.Ident); ok && id.Name == req {
					if msg := requestUse(stack, id); msg != "" {
						violations = append(violations, fset.Position(id.Pos()).String()+": "+msg)
					}
				}
				stack = append(stack, n)
				return true
			})
		}
	}
	if literals != 1 {
		violations = append(violations, "the literal "+retryCountLiteral+" appears "+strconv.Itoa(literals)+" times, want once")
	}
	return roundTrips, violations
}

// isAdapterReceiver reports whether fn is a method of *Adapter.
func isAdapterReceiver(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && id.Name == "Adapter"
}

// requestUse returns why the use id of the request parameter, under the
// nodes of stack (innermost last), is not allowed, or "" when it is.
func requestUse(stack []ast.Node, id *ast.Ident) string {
	for _, n := range stack {
		if _, ok := n.(*ast.FuncLit); ok {
			return "used inside a function literal"
		}
	}
	parent := func(i int) ast.Node {
		if len(stack) < i {
			return nil
		}
		return stack[len(stack)-i]
	}
	switch p := parent(1).(type) {
	case *ast.SelectorExpr:
		if p.Sel == id {
			return "" // a field or method named like the parameter
		}
		switch p.Sel.Name {
		case "Method":
			return readOnly(parent(2), p, "req.Method")
		case "URL":
			if q, ok := parent(2).(*ast.SelectorExpr); ok && q.X == p && q.Sel.Name == "Path" {
				return readOnly(parent(3), q, "req.URL.Path")
			}
			return "req.URL used other than as req.URL.Path"
		case "Context":
			if c, ok := parent(2).(*ast.CallExpr); ok && c.Fun == p && len(c.Args) == 0 {
				return ""
			}
			return "req.Context used other than called"
		case "Body":
			return bodyUse(parent(2), parent(3), p)
		case "Header":
			if q, ok := parent(2).(*ast.SelectorExpr); ok && q.X == p && q.Sel.Name == "Get" {
				if c, ok := parent(3).(*ast.CallExpr); ok && c.Fun == q && len(c.Args) == 1 {
					if lit, ok := c.Args[0].(*ast.BasicLit); ok && lit.Value == retryCountLiteral {
						return ""
					}
				}
				return "req.Header.Get called with another argument than " + retryCountLiteral
			}
			return "req.Header used other than by req.Header.Get"
		}
		return "req." + p.Sel.Name + " is read"
	case *ast.KeyValueExpr:
		if key, ok := p.Key.(*ast.Ident); ok && key.Name == "Request" && p.Value == id {
			if lit, ok := parent(2).(*ast.CompositeLit); ok && isHTTPResponse(lit.Type) {
				return ""
			}
		}
		return "req is the value of a composite literal's field"
	case *ast.CallExpr:
		return "req is passed to a function"
	case *ast.AssignStmt:
		return "req is assigned"
	case *ast.ReturnStmt:
		return "req is returned"
	}
	return "req is used as a value"
}

// readOnly returns "" when sel, one of req's fields, is read by parent, and
// why not when parent writes it or takes its address.
func readOnly(parent ast.Node, sel ast.Expr, name string) string {
	switch p := parent.(type) {
	case *ast.AssignStmt:
		if slices.Contains(p.Lhs, sel) {
			return name + " is written"
		}
	case *ast.UnaryExpr:
		if p.Op == token.AND {
			return "the address of " + name + " is taken"
		}
	case *ast.IncDecStmt:
		return name + " is written"
	}
	return ""
}

// bodyUse returns "" when req.Body (sel) is compared with nil, closed, or
// read by io.ReadAll, and why not otherwise.
func bodyUse(p2, p3 ast.Node, sel *ast.SelectorExpr) string {
	switch p := p2.(type) {
	case *ast.BinaryExpr:
		other := p.Y
		if p.Y == sel {
			other = p.X
		}
		if id, ok := other.(*ast.Ident); ok && id.Name == "nil" && (p.Op == token.EQL || p.Op == token.NEQ) {
			return ""
		}
	case *ast.SelectorExpr:
		if p.Sel.Name == "Close" {
			if c, ok := p3.(*ast.CallExpr); ok && c.Fun == p && len(c.Args) == 0 {
				return ""
			}
		}
	case *ast.CallExpr:
		if f, ok := p.Fun.(*ast.SelectorExpr); ok && f.Sel.Name == "ReadAll" && len(p.Args) == 1 && p.Args[0] == sel {
			if pkg, ok := f.X.(*ast.Ident); ok && pkg.Name == "io" {
				return ""
			}
		}
	}
	return "req.Body used other than compared with nil, closed or read by io.ReadAll"
}

// isHTTPResponse reports whether typ is http.Response.
func isHTTPResponse(typ ast.Expr) bool {
	sel, ok := typ.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "http" && sel.Sel.Name == "Response"
}

// closeCounter is a request body that counts the calls of Close and
// whether it was read to its end.
type closeCounter struct {
	r      io.Reader
	closes atomic.Int32
	ended  atomic.Bool
}

func (c *closeCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if err == io.EOF {
		c.ended.Store(true)
	}
	return n, err
}

func (c *closeCounter) Close() error {
	c.closes.Add(1)
	return nil
}

// requestWith returns a request of method to path whose body is body (nil
// for none) and whose header is header.
func requestWith(ctx context.Context, method, path string, body io.ReadCloser, header http.Header) *http.Request {
	req := rawRequest(ctx, method, path, "")
	req.Body = body
	req.Header = header
	return req
}

// retryCountHeader returns a header such as the SDK sends: the headers of
// every request, and X-TypeSafe-Retry-Count with value when it is not
// empty.
func retryCountHeader(value string) http.Header {
	h := http.Header{
		"Accept":             {"application/json"},
		"Authorization":      {"Bearer " + ownClientKey},
		"Content-Type":       {"application/json"},
		"User-Agent":         {"decision-model-sdk-go/test"},
		"X-Typesafe-Runtime": {"go"},
	}
	if value != "" {
		h.Set("X-TypeSafe-Retry-Count", value)
	}
	return h
}

// fm1Questions returns upstream's three questions of
// tests/test_client_with_fake_model.py (QUESTIONS, :25-33), as the SDK's
// question types (sdk-models) or as the dictionaries upstream also accepts
// (dictionaries), sent through (*decision.Questions).Raw.
func fm1Questions(t testing.TB, dictionaries bool) *decision.Prepared {
	t.Helper()
	qs := decision.NewQuestions()
	if dictionaries {
		qs = qs.Raw("positive", decision.RawQuestion{Type: "noul", Fields: map[string]any{"criteria": decision.RawJSON(`{"true":"Positive.","false":"Negative."}`)}}).
			Raw("stars", decision.RawQuestion{Type: "score", Fields: map[string]any{"criteria": decision.RawJSON(`["Bad.","Good."]`)}}).
			Raw("genre", decision.RawQuestion{Type: "choice", Fields: map[string]any{"criteria": decision.RawJSON(`{"fiction":"A story.","nonfiction":"Facts."}`)}})
	} else {
		qs = qs.Noul("positive", decision.Noul{Instructions: decision.Text("The review is positive.")}).
			Score("stars", decision.Score{Instructions: decision.Text("Rating."), Levels: []decision.Content{decision.Text("Bad."), decision.Text("Good.")}}).
			Choice("genre", decision.Choice{Instructions: decision.Text("Genre."), Options: decision.Options{{Label: "fiction", Description: decision.Text("A story.")}, {Label: "nonfiction", Description: decision.Text("Facts.")}}})
	}
	q, err := qs.Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return q
}

// fm10Questions returns the question set of one case of
// tests/test_client_with_fake_model.py::test_invalid_questions_are_rejected
// as the SDK's question types, and Prepare's error, which the SDK gives for
// the cases it refuses itself.
func fm10Questions(id string) (*decision.Prepared, error) {
	qs := decision.NewQuestions()
	switch id {
	case "empty-score-criteria":
		qs = qs.Score("stars", decision.Score{Instructions: decision.Text("Rating.")})
	case "single-score-criterion":
		qs = qs.Score("stars", decision.Score{Instructions: decision.Text("Rating."), Levels: []decision.Content{decision.Text("Good.")}})
	case "empty-choice-criteria":
		qs = qs.Choice("genre", decision.Choice{Instructions: decision.Text("Genre.")})
	case "single-choice-criterion":
		qs = qs.Choice("genre", decision.Choice{Instructions: decision.Text("Genre."), Options: decision.Options{{Label: "fiction", Description: decision.Text("A story.")}}})
	}
	return qs.Prepare()
}

// rawQuestionsWithDefects returns a set of two questions whose shapes are
// refused, zeta before alpha, sent through (*decision.Questions).Raw.
func rawQuestionsWithDefects(t testing.TB) *decision.Prepared {
	t.Helper()
	q, err := decision.NewQuestions().
		Raw("zeta", decision.RawQuestion{Type: "noul", Fields: map[string]any{"color": "red"}}).
		Raw("alpha", decision.RawQuestion{Type: "choice", Fields: map[string]any{"criteria": []string{"a", "b"}}}).
		Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return q
}

// fakeAnswering returns a fake provider that answers every request with
// text.
func fakeAnswering(text string) *fake.Provider { return fake.New(fake.Text(text)) }

// factoryOf returns a factory that returns p for every model.
func factoryOf(p llm.Provider) llm.Factory {
	return func(string) (llm.Provider, error) { return p, nil }
}

// newAdapter returns an Adapter in probabilities, structured mode with
// opts.
func newAdapter(t testing.TB, opts ...Option) *Adapter {
	t.Helper()
	ad, err := New(Probabilities, Structured, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ad
}

// apiErrorParts returns the status, error type and message of the
// *decision.APIError in err's chain, and whether ReportFromError finds a
// Report in err; zeros when err holds no *decision.APIError.
func apiErrorParts(err error) (status int, errorType, message string, report bool) {
	apiErr, ok := errors.AsType[*decision.APIError](err)
	if !ok {
		return 0, "", "", false
	}
	_, report = ReportFromError(err)
	return apiErr.StatusCode, apiErr.ErrorType, apiErr.Message, report
}

// apiErrorBody returns the body of the *decision.APIError in err's chain.
func apiErrorBody(err error) []byte {
	if apiErr, ok := errors.AsType[*decision.APIError](err); ok {
		return apiErr.Body
	}
	return nil
}

// detailErrors returns the entries of detail.errors of an error body, each
// as "<loc as JSON>: <msg>"; nil when the member is absent.
func detailErrors(t testing.TB, body []byte) []string {
	t.Helper()
	root, err := jsonx.Read(body)
	if err != nil {
		t.Fatalf("the body is not JSON: %v", err)
	}
	detail, _ := root.Member("detail")
	list, ok := detail.Member("errors")
	if !ok {
		return nil
	}
	if detail.Len() != 3 || detail.Name(0) != "message" || detail.Name(1) != "error_type" || detail.Name(2) != "errors" {
		t.Errorf("detail members are not message, error_type, errors: %s", body)
	}
	out := []string{}
	for i := range list.Len() {
		entry := list.Index(i)
		loc, _ := entry.Member("loc")
		msg, _ := entry.Member("msg")
		locJSON, err := jsonx.Marshal(rawValue(loc))
		if err != nil {
			t.Fatalf("loc: %v", err)
		}
		out = append(out, string(locJSON)+": "+msg.Text())
	}
	return out
}

// detailNames returns the member names of an error body's detail object.
func detailNames(t testing.TB, body []byte) []string {
	t.Helper()
	root, err := jsonx.Read(body)
	if err != nil {
		t.Fatalf("the body is not JSON: %v", err)
	}
	detail, _ := root.Member("detail")
	names := make([]string, detail.Len())
	for i := range names {
		names[i] = detail.Name(i)
	}
	return names
}

// isClosedError reports whether err is the Error of a closed Adapter.
func isClosedError(err error) bool {
	ae, ok := errors.AsType[*Error](err)
	return ok && ae.Kind == KindClosed && ae.Report == nil && errors.Is(err, ErrClosed)
}

// responseRequestID returns the X-Typesafe-Request-Id of the response
// RoundTrip gives req.
func responseRequestID(t testing.TB, ad *Adapter, req *http.Request) string {
	t.Helper()
	resp, err := ad.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return resp.Header.Get("X-Typesafe-Request-Id")
}

// contractChecked runs the call of one failure class through a client of
// the caller's own whose transport checks every RoundTrip of the Adapter,
// and returns the number of RoundTrips checked: a response with a body,
// the request as its Request and a request id, or an error, never both and
// never neither.
func contractChecked(t *testing.T, tt classCase) int {
	t.Helper()
	var n atomic.Int32
	synctest.Test(t, func(t *testing.T) {
		build := tt.build
		if build == nil {
			build = func(t testing.TB) (*Adapter, func() int) {
				p := fake.New(tt.outcome)
				return fakeAdapter(t, p), p.Calls
			}
		}
		ad, _ := build(t)
		if tt.closed {
			if err := ad.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		}
		checked := rtFunc(func(req *http.Request) (*http.Response, error) {
			n.Add(1)
			resp, err := ad.RoundTrip(req)
			switch {
			case err != nil && resp != nil:
				t.Errorf("RoundTrip returned a response and the error %v", err)
			case err == nil && resp == nil:
				t.Error("RoundTrip returned no response and no error")
			case resp != nil && (resp.Body == nil || resp.Request != req || !strings.HasPrefix(resp.Header.Get("X-Typesafe-Request-Id"), "adp_")):
				t.Errorf("response body %v, request kept %v, request id %q", resp.Body != nil, resp.Request == req, resp.Header.Get("X-Typesafe-Request-Id"))
			}
			return resp, err
		})
		c, err := decision.NewClient(decision.WithAPIKey(ownClientKey), decision.WithRoundTripper(checked), decision.WithBaseURL(placeholderBaseURL), decision.WithModel(noModel), decision.WithRetry(decision.DefaultRetry()), decision.WithNoTimeout())
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		defer c.Close()
		questions := noulQuestions(t)
		if tt.questions != nil {
			questions = tt.questions(t)
		}
		state := tt.state
		if state == nil {
			state = "state"
		}
		if _, err := c.SystemOne(t.Context(), state, questions, tt.call...); err == nil {
			t.Error("SystemOne succeeded")
		}
	})
	return int(n.Load())
}

// reencodeResponse returns resp as the SDK serialises it and the response
// that serialisation reads back to.
func reencodeResponse(t testing.TB, resp *decision.SystemOneResponse) ([]byte, *decision.SystemOneResponse) {
	t.Helper()
	b, err := resp.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	restored := new(decision.SystemOneResponse)
	if err := restored.UnmarshalJSON(b); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	return b, restored
}

// canaryKey returns an API key for a test that no text of the test
// contains: a fixed prefix and 16 random hex digits.
func canaryKey(t testing.TB) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return "ts-canary-" + hex.EncodeToString(b[:])
}

// scripted is an http.RoundTripper of a test for the SDK's side of the
// seam: it reads and closes the request body as the Adapter does, records
// each request's body, header map and retry count, and answers with the
// script's response or error for the attempt.
type scripted struct {
	mu       sync.Mutex
	bodies   [][]byte
	nilBody  []bool
	getBody  []bool
	headers  []http.Header
	counts   []string
	ctxs     []context.Context
	closes   int
	idles    int
	script   func(req *http.Request, attempt int) (*http.Response, error)
	requests int
}

func (s *scripted) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	s.mu.Lock()
	attempt := s.requests
	s.requests++
	s.bodies = append(s.bodies, body)
	s.nilBody = append(s.nilBody, req.Body == nil)
	s.getBody = append(s.getBody, req.GetBody != nil)
	s.headers = append(s.headers, req.Header)
	s.counts = append(s.counts, req.Header.Get("X-TypeSafe-Retry-Count"))
	s.ctxs = append(s.ctxs, req.Context())
	s.mu.Unlock()
	return s.script(req, attempt)
}

func (s *scripted) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closes++
	return nil
}

func (s *scripted) CloseIdleConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idles++
}

// respondWith returns the response the Adapter gives with status and body.
func respondWith(req *http.Request, status int, body []byte) *http.Response {
	return &http.Response{
		Status: strconv.Itoa(status) + " " + http.StatusText(status), StatusCode: status,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:        responseHeader(len(body), newRequestID()),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// seamClient returns a client of the SDK with key whose transport is rt,
// with policy and opts.
func seamClient(t testing.TB, rt http.RoundTripper, key string, policy decision.RetryPolicy, opts ...decision.ClientOption) *decision.Client {
	t.Helper()
	c, err := decision.NewClient(append([]decision.ClientOption{decision.WithAPIKey(key), decision.WithRoundTripper(rt), decision.WithBaseURL(ownClientBaseURL), decision.WithModel("fake"), decision.WithRetry(policy)}, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// sampleReport returns a Report of one attempt that failed with class.
func sampleReport(class string) *Report {
	return &Report{
		Usage: Usage{InputTokensTotal: llm.Count{N: 3, Known: true}, OutputTokensTotal: llm.Count{N: 4, Known: true}, Latency: 1500 * time.Millisecond},
		Debug: Debug{
			Attempts:     []Attempt{{Messages: []llm.Message{{Role: "user", Content: "state"}}, Schema: []byte(`{"type":"object"}`), Info: AttemptInfo{ModelName: "m", Provider: "p", Error: "failed", ErrorType: class}}},
			RetryReasons: []RetryReason{},
		},
	}
}

// bodyOf writes w, failing the test when it cannot.
func bodyOf(t testing.TB, w writer) []byte {
	t.Helper()
	v, err := w()
	if err != nil {
		t.Fatal(err)
	}
	b, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// noKeyFormatError is an error type of a test with no Format method whose
// field holds a key: %+v and %#v print the field.
type noKeyFormatError struct{ key string }

func (e *noKeyFormatError) Error() string   { return "adapter: timeout" }
func (e *noKeyFormatError) Timeout() bool   { return true }
func (e *noKeyFormatError) Temporary() bool { return false }

// seamContractChecks returns the checks of TestSeamContract, by the SDK
// behaviour each asserts.
func seamContractChecks() map[string]func(t *testing.T) {
	return map[string]func(t *testing.T){
		"a POST carries its body with GetBody, a GET carries none": func(t *testing.T) {
			rt := &scripted{script: func(req *http.Request, _ int) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return respondWith(req, http.StatusOK, []byte(`{"models":[]}`)), nil
				}
				return respondWith(req, http.StatusOK, []byte(`{"model":"m","usage":{"input_tokens":1,"output_tokens":1},"answers":{"answer":{"type":"noul","noul":0.5}},"debug":{}}`)), nil
			}}
			c := seamClient(t, rt, ownClientKey, decision.NoRetry())
			resp, err := c.SystemOne(t.Context(), "state", noulQuestions(t))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Models().List(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := c.WarmUp(t.Context()); err != nil {
				t.Fatal(err)
			}
			if rt.nilBody[0] || !rt.getBody[0] || !bytes.HasPrefix(rt.bodies[0], []byte(`{"state":"state","model":"fake","questions":{"answer":`)) {
				t.Errorf("POST body nil %v, GetBody %v, body %s", rt.nilBody[0], rt.getBody[0], rt.bodies[0])
			}
			for i := 1; i < 3; i++ {
				if !rt.nilBody[i] || rt.getBody[i] {
					t.Errorf("GET %d: body nil %v, GetBody %v", i, rt.nilBody[i], rt.getBody[i])
				}
			}
			if !bytes.HasPrefix(resp.Meta().RawBody(), []byte(`{"model":"m"`)) {
				t.Errorf("RawBody %s", resp.Meta().RawBody())
			}
		},
		"the raw body is the response's bytes, and a request id is read": func(t *testing.T) {
			ad := fakeAdapter(t, fakeAnswering(noulAnswer))
			var sent []byte
			rt := rtFunc(func(req *http.Request) (*http.Response, error) {
				resp, err := ad.RoundTrip(req)
				if err == nil {
					sent, _ = io.ReadAll(resp.Body)
					resp.Body = io.NopCloser(bytes.NewReader(sent))
				}
				return resp, err
			})
			resp, err := seamClient(t, rt, ownClientKey, decision.NoRetry()).SystemOne(t.Context(), "state", noulQuestions(t))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(resp.Meta().RawBody(), sent) {
				t.Error("RawBody differs from the bytes RoundTrip returned")
			}
			if id, ok := resp.Meta().RequestID(); !ok || !strings.HasPrefix(id, "adp_") {
				t.Errorf("RequestID = %q, %v", id, ok)
			}
		},
		"an error body is kept byte for byte, its detail read": func(t *testing.T) {
			body := bodyOf(t, errorBody("the provider failed", "provider_status", nil, sampleReport("TypeSafeInternalServerError")))
			rt := &scripted{script: func(req *http.Request, _ int) (*http.Response, error) {
				return respondWith(req, http.StatusBadGateway, body), nil
			}}
			_, err := seamClient(t, rt, ownClientKey, decision.NoRetry()).SystemOne(t.Context(), "state", noulQuestions(t))
			apiErr, ok := errors.AsType[*decision.APIError](err)
			if !ok || !bytes.Equal(apiErr.Body, body) || apiErr.Message != "the provider failed" || apiErr.ErrorType != "provider_status" {
				t.Fatalf("SystemOne error = %v", err)
			}
			if r, ok := ReportFromError(err); !ok || r.Debug.Attempts[0].Info.ErrorType != "TypeSafeInternalServerError" {
				t.Errorf("ReportFromError = %v, %v", r, ok)
			}
		},
		"answers null is a validation error of answers with the body kept": func(t *testing.T) {
			body := bodyOf(t, malformedBody("m", sampleReport("")))
			rt := &scripted{script: func(req *http.Request, _ int) (*http.Response, error) {
				return respondWith(req, http.StatusOK, body), nil
			}}
			c := seamClient(t, rt, ownClientKey, decision.DefaultRetry())
			_, err := c.SystemOne(t.Context(), "state", noulQuestions(t))
			invalid, ok := errors.AsType[*decision.ResponseValidationError](err)
			if !ok || invalid.StatusCode != http.StatusOK || invalid.FieldPath != "answers" || !bytes.Equal(invalid.Body, body) || c.Stats().Attempts != 1 {
				t.Fatalf("SystemOne error = %v, attempts %d", err, c.Stats().Attempts)
			}
			if _, ok := ReportFromError(err); !ok {
				t.Error("ReportFromError found no Report")
			}
		},
		"a status's kind and the attempts of DefaultRetry": func(t *testing.T) {
			kinds := map[int]decision.APIErrorKind{
				400: decision.APIErrorBadRequest, 401: decision.APIErrorAuthentication, 403: decision.APIErrorPermissionDenied,
				404: decision.APIErrorNotFound, 408: decision.APIErrorOther, 418: decision.APIErrorOther, 422: decision.APIErrorUnprocessableEntity,
				424: decision.APIErrorOther, 429: decision.APIErrorRateLimit, 500: decision.APIErrorInternalServer, 503: decision.APIErrorInternalServer,
			}
			for status, kind := range kinds {
				synctest.Test(t, func(t *testing.T) {
					rt := &scripted{script: func(req *http.Request, _ int) (*http.Response, error) {
						return respondWith(req, status, []byte(`{"detail":{"message":"m","error_type":"t"}}`)), nil
					}}
					c := seamClient(t, rt, ownClientKey, decision.DefaultRetry())
					_, err := c.SystemOne(t.Context(), "state", noulQuestions(t))
					apiErr, ok := errors.AsType[*decision.APIError](err)
					want := uint64(1)
					if status == 408 || status == 429 || status >= 500 {
						want = 3
					}
					if !ok || apiErr.Kind != kind || c.Stats().Attempts != want {
						t.Errorf("status %d: error %v, kind %v, attempts %d; want kind %v, attempts %d", status, err, apiErr.Kind, c.Stats().Attempts, kind, want)
					}
					if status == 401 && !apiErr.IsAuthentication() {
						t.Error("a 401 is not IsAuthentication")
					}
				})
			}
		},
		"the retry count is sent on retries only, with the earlier attempts": func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rt := &scripted{script: func(req *http.Request, _ int) (*http.Response, error) {
					return respondWith(req, http.StatusServiceUnavailable, []byte(`{"detail":{"message":"m","error_type":"t"}}`)), nil
				}}
				_, _ = seamClient(t, rt, ownClientKey, decision.DefaultRetry()).SystemOne(t.Context(), "state", noulQuestions(t))
				if !slices.Equal(rt.counts, []string{"", "1", "2"}) {
					t.Errorf("X-TypeSafe-Retry-Count per attempt = %q, want [\"\" 1 2]", rt.counts)
				}
				for i, h := range rt.headers {
					_, canonical := h["X-Typesafe-Retry-Count"]
					if canonical != (i > 0) {
						t.Errorf("attempt %d: the canonical key present %v", i, canonical)
					}
				}
			})
		},
		"the first attempts share the client's header map": func(t *testing.T) {
			rt := &scripted{script: func(req *http.Request, _ int) (*http.Response, error) {
				return respondWith(req, http.StatusNotFound, notFoundBody), nil
			}}
			c := seamClient(t, rt, ownClientKey, decision.NoRetry())
			for range 2 {
				_, _ = c.SystemOne(t.Context(), "state", noulQuestions(t))
			}
			if reflect.ValueOf(rt.headers[0]).UnsafePointer() != reflect.ValueOf(rt.headers[1]).UnsafePointer() {
				t.Error("two first attempts got two header maps; RoundTrip's no-write rule would protect nothing shared")
			}
		},
		"the deadline comes only through the context": func(t *testing.T) {
			for name, tt := range map[string]struct {
				opt  decision.ClientOption
				want bool
			}{"WithNoTimeout": {opt: decision.WithNoTimeout()}, "WithTimeout": {opt: decision.WithTimeout(5 * time.Second), want: true}} {
				rt := &scripted{script: func(req *http.Request, _ int) (*http.Response, error) {
					return respondWith(req, http.StatusNotFound, notFoundBody), nil
				}}
				_, _ = seamClient(t, rt, ownClientKey, decision.NoRetry(), tt.opt).SystemOne(t.Context(), "state", noulQuestions(t))
				if _, has := rt.ctxs[0].Deadline(); has != tt.want {
					t.Errorf("%s: the request's context has a deadline %v, want %v", name, has, tt.want)
				}
			}
		},
		"errors.As reaches the Error through both transport wrappers": func(t *testing.T) {
			for _, policy := range []string{"NoRetry", "DefaultRetry"} {
				for _, kind := range []ErrorKind{KindTimeout, KindConnection, KindClosed} {
					synctest.Test(t, func(t *testing.T) {
						key := canaryKey(t)
						rt := &scripted{script: func(*http.Request, int) (*http.Response, error) {
							e := &Error{Kind: kind}
							if kind != KindClosed {
								e.Provider, e.Model, e.Report = "openai", "gpt-x", sampleReport("TypeSafeAPITimeoutError")
							}
							if kind == KindConnection {
								e.cause = syscall.ECONNREFUSED
							}
							return nil, e
						}}
						p := decision.NoRetry()
						if policy == "DefaultRetry" {
							p = decision.DefaultRetry()
						}
						c := seamClient(t, rt, key, p)
						_, err := c.SystemOne(t.Context(), "state", noulQuestions(t))
						ae, ok := errors.AsType[*Error](err)
						wantType := "*decision.ConnectionError"
						if kind == KindTimeout {
							wantType = "*decision.TimeoutError"
						}
						if !ok || ae.Kind != kind || fmt.Sprintf("%T", err) != wantType {
							t.Fatalf("%s %v: error %T %v; errors.As %v", policy, kind, err, err, ok)
						}
						if _, ok := ReportFromError(err); ok != (kind != KindClosed) {
							t.Errorf("%s %v: ReportFromError %v", policy, kind, ok)
						}
						want := uint64(1)
						if policy == "DefaultRetry" {
							want = 3
						}
						if c.Stats().Attempts != want {
							t.Errorf("%s %v: attempts %d, want %d", policy, kind, c.Stats().Attempts, want)
						}
					})
				}
			}
		},
		"an error that prints the key is replaced, so errors.As fails": func(t *testing.T) {
			key := canaryKey(t)
			rt := &scripted{script: func(*http.Request, int) (*http.Response, error) {
				return nil, &noKeyFormatError{key: key}
			}}
			_, err := seamClient(t, rt, key, decision.NoRetry()).SystemOne(t.Context(), "state", noulQuestions(t))
			if _, ok := errors.AsType[*noKeyFormatError](err); ok {
				t.Errorf("errors.As reached an error whose %%#v prints the key: %v", err)
			}
			if _, ok := errors.AsType[*decision.TimeoutError](err); !ok {
				t.Errorf("SystemOne error = %T %v, want a *decision.TimeoutError", err, err)
			}
		},
		"a passed deadline wraps the Error, a cancellation drops it": func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				rt := &scripted{script: func(req *http.Request, _ int) (*http.Response, error) {
					<-req.Context().Done()
					return nil, &Error{Kind: KindTimeout, Provider: "openai", Model: "gpt-x", Report: sampleReport("TypeSafeAPITimeoutError")}
				}}
				_, err := seamClient(t, rt, ownClientKey, decision.DefaultRetry(), decision.WithNoTimeout()).SystemOne(ctx, "state", noulQuestions(t))
				if te, ok := errors.AsType[*decision.TimeoutError](err); !ok || te.Timeout != 0 {
					t.Fatalf("deadline: %v", err)
				}
				if _, ok := ReportFromError(err); !ok {
					t.Error("deadline: no Report")
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			rt := &scripted{script: func(*http.Request, int) (*http.Response, error) {
				cancel()
				return nil, &Error{Kind: KindTimeout, Provider: "openai", Model: "gpt-x", Report: sampleReport("TypeSafeAPITimeoutError")}
			}}
			_, err := seamClient(t, rt, ownClientKey, decision.DefaultRetry()).SystemOne(ctx, "state", noulQuestions(t))
			if err != context.Canceled { //nolint:errorlint // the SDK returns the context's error itself.
				t.Errorf("cancellation: %T %v, want context.Canceled itself", err, err)
			}
			if _, ok := ReportFromError(err); ok {
				t.Error("cancellation: a Report")
			}
		},
		"the response size limit": func(t *testing.T) {
			big := bytes.Repeat([]byte("x"), 17<<20)
			success := append(append([]byte(`{"model":"m","usage":{"input_tokens":1,"output_tokens":1},"answers":{"answer":{"type":"noul","noul":0.5}},"debug":"`), big...), `"}`...)
			failure := append(append([]byte(`{"detail":{"message":"m","error_type":"t"},"debug":"`), big...), `"}`...)
			for name, tt := range map[string]struct {
				status int
				body   []byte
				limit  int64
				check  func(error) bool
			}{
				"a success over the default limit": {status: 200, body: success, check: func(err error) bool {
					_, ok := errors.AsType[*decision.ResponseTooLargeError](err)
					return ok
				}},
				"an error body over the default limit": {status: 424, body: failure, check: func(err error) bool {
					apiErr, ok := errors.AsType[*decision.APIError](err)
					return ok && len(apiErr.Body) == 0
				}},
				"a success under a limit of 1 GiB": {status: 200, body: success, limit: 1 << 30, check: func(err error) bool { return err == nil }},
			} {
				rt := &scripted{script: func(req *http.Request, _ int) (*http.Response, error) {
					return respondWith(req, tt.status, tt.body), nil
				}}
				var opts []decision.ClientOption
				if tt.limit > 0 {
					opts = append(opts, decision.WithMaxResponseBytes(tt.limit))
				}
				_, err := seamClient(t, rt, ownClientKey, decision.NoRetry(), opts...).SystemOne(t.Context(), "state", noulQuestions(t))
				if !tt.check(err) {
					t.Errorf("%s: %T %v", name, err, err)
				}
			}
		},
		"Prepare refuses an empty set and a score without levels only": func(t *testing.T) {
			if _, err := decision.NewQuestions().Prepare(); err == nil {
				t.Error("an empty set is prepared")
			}
			for _, id := range []string{"empty-score-criteria"} {
				if _, err := fm10Questions(id); err == nil {
					t.Errorf("%s is prepared", id)
				}
			}
			for _, id := range []string{"single-score-criterion", "empty-choice-criteria", "single-choice-criterion"} {
				if _, err := fm10Questions(id); err != nil {
					t.Errorf("%s is refused: %v", id, err)
				}
			}
		},
		"a 404 is an APIError of kind not found": func(t *testing.T) {
			rt := &scripted{script: func(req *http.Request, _ int) (*http.Response, error) {
				return respondWith(req, http.StatusNotFound, notFoundBody), nil
			}}
			_, err := seamClient(t, rt, ownClientKey, decision.NoRetry()).SystemOne(t.Context(), "state", noulQuestions(t))
			if apiErr, ok := errors.AsType[*decision.APIError](err); !ok || apiErr.Kind != decision.APIErrorNotFound || apiErr.Message != "Not Found" {
				t.Errorf("404: %v", err)
			}
		},
		"Close closes the round tripper once and refuses later calls": func(t *testing.T) {
			rt := &scripted{script: func(req *http.Request, _ int) (*http.Response, error) {
				return respondWith(req, http.StatusNotFound, notFoundBody), nil
			}}
			c, err := decision.NewClient(decision.WithAPIKey(ownClientKey), decision.WithRoundTripper(rt), decision.WithBaseURL(ownClientBaseURL), decision.WithModel("fake"))
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Errorf("a second Close = %v", err)
			}
			if _, err := c.SystemOne(t.Context(), "state", noulQuestions(t)); !errors.Is(err, decision.ErrClientClosed) || rt.requests != 0 {
				t.Errorf("after Close: %v, requests %d", err, rt.requests)
			}
			if rt.closes != 1 || rt.idles != 1 {
				t.Errorf("Close calls %d, CloseIdleConnections calls %d; want 1 and 1", rt.closes, rt.idles)
			}
		},
		"two round trippers: the SDK keeps the last": func(t *testing.T) {
			first := &scripted{script: func(req *http.Request, _ int) (*http.Response, error) {
				return respondWith(req, http.StatusNotFound, notFoundBody), nil
			}}
			last := &scripted{script: first.script}
			c, err := decision.NewClient(decision.WithAPIKey(ownClientKey), decision.WithRoundTripper(first), decision.WithRoundTripper(last), decision.WithBaseURL(ownClientBaseURL), decision.WithModel("fake"))
			if err != nil {
				t.Fatalf("the SDK refuses two round trippers (%v); NewClient's order of options rests on it accepting them", err)
			}
			_, _ = c.SystemOne(t.Context(), "state", noulQuestions(t))
			_ = c.Close()
			if first.requests != 0 || last.requests != 1 || first.closes != 0 {
				t.Errorf("first: %d requests, %d closes; last: %d requests", first.requests, first.closes, last.requests)
			}
		},
	}
}

// installCheck makes ad check each body it answers 200 with checkBody,
// reporting a violation to t.
func installCheck(t testing.TB, ad *Adapter) {
	ad.mu.Lock()
	defer ad.mu.Unlock()
	ad.check = func(request, response []byte) {
		if err := checkBody(request, response); err != nil {
			t.Errorf("the Adapter's 200 body breaks its invariant: %v\nrequest: %s\nresponse: %s", err, request, response)
		}
	}
}

// answerMembers are the members of each kind of answer in a success body,
// in the order the expected responses under testdata/expected write them.
var answerMembers = map[schema.Kind][]string{
	schema.Noul:   {"type", "noul"},
	schema.Score:  {"type", "score", "confidence", "legend", "probabilities"},
	schema.Choice: {"type", "choice", "confidence", "probabilities"},
}

// checkBody checks a 200 body of the Adapter against the request it
// answers: the models list, an object with the one member models, which is
// an array; otherwise the members model, usage, answers and debug, where answers is null (output
// that never matched the schema) or holds one answer per question in
// question order, each with the members of its kind, a noul, every
// probability and every confidence finite and in [0, 1], a choice among
// the labels, a score in [0, n-1], and the keys of a score's legend and
// probabilities equal to its levels and of a choice's probabilities to its
// labels, in order.
func checkBody(request, response []byte) error {
	body, err := jsonx.Read(response)
	if err != nil {
		return fmt.Errorf("the body is not JSON: %w", err)
	}
	if body.Kind() == jsonx.KindObject && body.Len() == 1 && body.Name(0) == "models" {
		if models := body.Index(0); models.Kind() != jsonx.KindArray {
			return fmt.Errorf("models is not an array but %v", models.Kind())
		}
		return nil
	}
	if err := sameNames(body, []string{"model", "usage", "answers", "debug"}); err != nil {
		return fmt.Errorf("top level: %w", err)
	}
	req, err := jsonx.Read(request)
	if err != nil {
		return fmt.Errorf("the request is not JSON: %w", err)
	}
	questionsNode, _ := req.Member("questions")
	questions, err := schema.ParseQuestions(questionsNode)
	if err != nil {
		return fmt.Errorf("answered a request whose questions the parser refuses: %w", err)
	}
	answers, _ := body.Member("answers")
	if answers.Kind() == jsonx.KindNull {
		return nil
	}
	names := make([]string, len(questions))
	for i := range questions {
		names[i] = questions[i].ID()
	}
	if err := sameNames(answers, names); err != nil {
		return fmt.Errorf("answers: %w", err)
	}
	for i := range questions {
		q := &questions[i]
		a := answers.Index(i)
		if err := checkAnswer(q, a); err != nil {
			return fmt.Errorf("answer %q: %w", q.ID(), err)
		}
	}
	return nil
}

// checkAnswer checks one answer of a success body against its question.
func checkAnswer(q *schema.Question, a jsonx.Node) error {
	if err := sameNames(a, answerMembers[q.Kind()]); err != nil {
		return err
	}
	if typ, _ := a.Member("type"); typ.Kind() != jsonx.KindString || typ.Text() != q.Kind().String() {
		return fmt.Errorf("type %q, want %q", typ.Text(), q.Kind().String())
	}
	if q.Kind() == schema.Noul {
		v, _ := a.Member("noul")
		return unitNumber("noul", v)
	}
	confidence, _ := a.Member("confidence")
	if err := unitNumber("confidence", confidence); err != nil {
		return err
	}
	labels := q.Labels()
	probabilities, _ := a.Member("probabilities")
	if err := sameNames(probabilities, labels); err != nil {
		return fmt.Errorf("probabilities: %w", err)
	}
	for i := range labels {
		if err := unitNumber("probability of "+labels[i], probabilities.Index(i)); err != nil {
			return err
		}
	}
	if q.Kind() == schema.Choice {
		choice, _ := a.Member("choice")
		if choice.Kind() != jsonx.KindString || !slices.Contains(labels, choice.Text()) {
			return fmt.Errorf("choice %q is not one of %q", choice.Text(), labels)
		}
		return nil
	}
	legend, _ := a.Member("legend")
	if err := sameNames(legend, labels); err != nil {
		return fmt.Errorf("legend: %w", err)
	}
	score, _ := a.Member("score")
	v, err := number(score)
	if err != nil {
		return fmt.Errorf("score: %w", err)
	}
	if !(v >= 0 && v <= float64(len(labels)-1)) {
		return fmt.Errorf("score %v is outside [0, %d]", v, len(labels)-1)
	}
	return nil
}

// sameNames reports whether v is an object whose member names are names,
// in that order.
func sameNames(v jsonx.Node, names []string) error {
	if v.Kind() != jsonx.KindObject {
		return fmt.Errorf("not an object but %v", v.Kind())
	}
	got := make([]string, v.Len())
	for i := range got {
		got[i] = v.Name(i)
	}
	if !slices.Equal(got, names) {
		return fmt.Errorf("members %q, want %q", got, names)
	}
	return nil
}

// number returns v's value, which must be a JSON number.
func number(v jsonx.Node) (float64, error) {
	if v.Kind() != jsonx.KindNumber {
		return 0, fmt.Errorf("not a number but %v", v.Kind())
	}
	return strconv.ParseFloat(v.Text(), 64)
}

// unitNumber checks that v is a finite number in [0, 1].
func unitNumber(what string, v jsonx.Node) error {
	f, err := number(v)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if math.IsNaN(f) || f < 0 || f > 1 {
		return fmt.Errorf("%s %v is outside [0, 1]", what, f)
	}
	return nil
}
