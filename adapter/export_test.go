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
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/fake"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
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
	var out []string
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
