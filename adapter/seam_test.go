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

package adapter

import (
	"bytes"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	// modulePath is this module's path.
	modulePath = "github.com/zchee/decision-model-sdk-go/adapter"
	// sdkPath is the root SDK's module path and its root package.
	sdkPath = "github.com/zchee/decision-model-sdk-go"
	// externalJSONPath is a JSON library outside the standard library that no
	// file of the module imports: the standard library's v2 JSON packages are
	// the module's JSON implementation.
	externalJSONPath = "github.com/go-json-experiment/json"
	// cmpPath is the one module test files may add.
	cmpPath = "github.com/google/go-cmp/cmp"
)

// allowedImports lists, for each package of this module (by its path
// relative to the module, "." for the root package), what its non-test
// files may import directly besides the standard library: packages of this
// module (relative paths), the SDK's root package and, for internal/jsonx,
// the standard library's v2 JSON packages. A package missing from
// the map fails TestImportPolicy, so a new package is added here on purpose,
// with the edges the import graph allows it.
var allowedImports = map[string][]string{
	".":                 {"llm", "openai", "anthropic", "gemini", "internal/jsonx", "internal/schema", "internal/prob", sdkPath},
	"openai":            {"llm", "internal/rest", "internal/jsonx"},
	"anthropic":         {"llm", "internal/rest", "internal/jsonx"},
	"gemini":            {"llm", "internal/rest", "internal/jsonx"},
	"internal/rest":     {"llm", "internal/jsonx"},
	"internal/schema":   {"internal/jsonx"},
	"internal/cassette": {"internal/jsonx"},
	"internal/fake":     {"llm"},
	"llm":               {},
	"internal/prob":     {},
	"internal/jsonx":    {"encoding/json/v2", "encoding/json/jsontext"},
	"livetest":          {".", "llm", "openai", "anthropic", "gemini", "internal/cassette", sdkPath},
	"examples/compare":  {".", "llm", "openai", "anthropic", "gemini", "internal/cassette", sdkPath},
}

// forbiddenStdlib is the standard library's first JSON package, which no
// file of the module imports: the v2 packages, confined to internal/jsonx,
// are the module's one JSON implementation.
var forbiddenStdlib = []string{"encoding/json"}

// jsonV2Packages are the standard library's v2 JSON packages. Only
// internal/jsonx imports them, and only those its allowedImports entry
// names.
var jsonV2Packages = []string{"encoding/json/v2", "encoding/json/jsontext"}

// listedPackage is one package of this module as go list reports it.
type listedPackage struct {
	rel     string // path relative to the module, "." for the root package
	imports []string
	tests   []string // TestImports and XTestImports
}

// TestImportPolicy holds the module to its import graph, allowedImports,
// over the direct imports of every package, its non-test files and its test
// files, in the default build configuration and with the live tag. It
// refuses an import under the SDK's internal/ directory (the go command would
// allow it, since this module's path lies under the SDK's), the standard
// library's encoding/json anywhere, its v2 JSON packages outside
// internal/jsonx, github.com/go-json-experiment/json anywhere, and any
// module other than the SDK and, in test files, go-cmp. Imports of the
// standard library are otherwise free; only direct imports are read,
// because the SDK itself imports encoding/json.
//
// allowedImports binds non-test files exactly. Test files are read more
// widely: they may import any package of this module and the SDK's root
// package, because the providers' external test packages (openai_test and
// the others) go through the root package and the SDK, and
// internal/cassette and internal/fake exist to be imported by other
// packages' tests; the go command itself refuses an import cycle. Every
// other rule above holds for test files as for non-test files.
func TestImportPolicy(t *testing.T) {
	for _, tags := range []string{"", "live"} {
		pkgs := listModulePackages(t, tags)
		if !slices.ContainsFunc(pkgs, func(p listedPackage) bool { return p.rel == "." }) {
			t.Fatalf("go list -tags=%q ./... did not report the root package %s; the check would pass vacuously", tags, modulePath)
		}
		for _, p := range pkgs {
			allowed, known := allowedImports[p.rel]
			if !known {
				t.Errorf("tags %q: package %s is not in allowedImports; add it with the edges the import graph allows", tags, p.rel)
				continue
			}
			for _, imp := range p.imports {
				if msg := importViolation(p.rel, imp, allowed, false); msg != "" {
					t.Errorf("tags %q: non-test file of %s imports %s: %s", tags, p.rel, imp, msg)
				}
			}
			for _, imp := range p.tests {
				if msg := importViolation(p.rel, imp, allowed, true); msg != "" {
					t.Errorf("tags %q: test file of %s imports %s: %s", tags, p.rel, imp, msg)
				}
			}
		}
	}
}

// importViolation reports why package rel may not import imp, or "" when it
// may. allowed is rel's entry of allowedImports; test says whether the
// import is a test file's, which may also import any package of this module
// (the go command refuses a cycle) and go-cmp.
func importViolation(rel, imp string, allowed []string, test bool) string {
	switch {
	case slices.Contains(forbiddenStdlib, imp):
		return "encoding/json is not used; internal/jsonx wraps the standard library's v2 JSON packages"
	case imp == sdkPath+"/internal" || strings.HasPrefix(imp, sdkPath+"/internal/"):
		return "the SDK's internal packages are not part of its API"
	case imp == externalJSONPath || strings.HasPrefix(imp, externalJSONPath+"/"):
		return "github.com/go-json-experiment/json is not used; the standard library's v2 JSON packages are the module's JSON library"
	case slices.Contains(jsonV2Packages, imp):
		if rel == "internal/jsonx" && slices.Contains(allowed, imp) {
			return ""
		}
		return "only internal/jsonx imports the standard library's v2 JSON packages, and only those its allowedImports entry names"
	case isStdlib(imp):
		return ""
	case imp == modulePath || strings.HasPrefix(imp, modulePath+"/"):
		dep := strings.TrimPrefix(strings.TrimPrefix(imp, modulePath), "/")
		if dep == "" {
			dep = "."
		}
		if test || slices.Contains(allowed, dep) {
			return ""
		}
		return "not an edge of the import graph; allowed: [" + strings.Join(allowed, ", ") + "]"
	case imp == sdkPath:
		if test || slices.Contains(allowed, sdkPath) {
			return ""
		}
		return "only the root package, livetest and examples/compare import the SDK"
	case test && (imp == cmpPath || strings.HasPrefix(imp, cmpPath+"/")):
		return ""
	default:
		return "a module the adapter does not require"
	}
}

// isStdlib reports whether path names a standard library package: its first
// element holds no dot.
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// TestImportViolation checks importViolation's JSON rules on imports that
// no package of the module has, so that TestImportPolicy cannot pass by
// accepting them: internal/jsonx may import the standard library's v2 JSON
// packages its allowedImports entry names, no other package may import
// them, and no package imports encoding/json or
// github.com/go-json-experiment/json, not even one whose allowedImports
// entry names it.
func TestImportViolation(t *testing.T) {
	const (
		v1Refused       = "encoding/json is not used; internal/jsonx wraps the standard library's v2 JSON packages"
		externalRefused = "github.com/go-json-experiment/json is not used; the standard library's v2 JSON packages are the module's JSON library"
		v2Refused       = "only internal/jsonx imports the standard library's v2 JSON packages, and only those its allowedImports entry names"
	)
	tests := map[string]struct {
		rel, imp string
		// extra is added to rel's allowedImports entry.
		extra []string
		omit  []string
		test  bool
		want  string
	}{
		"internal/jsonx omits encoding/json/v2 from its allowed entry":  {rel: "internal/jsonx", imp: "encoding/json/v2", omit: []string{"encoding/json/v2"}, want: v2Refused},
		"a test file of internal/jsonx omits encoding/json/jsontext":    {rel: "internal/jsonx", imp: "encoding/json/jsontext", omit: []string{"encoding/json/jsontext"}, test: true, want: v2Refused},
		"internal/jsonx imports encoding/json/jsontext":                 {rel: "internal/jsonx", imp: "encoding/json/jsontext"},
		"internal/jsonx imports encoding/json/v2":                       {rel: "internal/jsonx", imp: "encoding/json/v2"},
		"a test file of internal/jsonx imports encoding/json/jsontext":  {rel: "internal/jsonx", imp: "encoding/json/jsontext", test: true},
		"internal/jsonx imports encoding/json":                          {rel: "internal/jsonx", imp: "encoding/json", want: v1Refused},
		"the root package imports encoding/json":                        {rel: ".", imp: "encoding/json", want: v1Refused},
		"a test file of openai imports encoding/json":                   {rel: "openai", imp: "encoding/json", test: true, want: v1Refused},
		"the root package imports encoding/json/v2":                     {rel: ".", imp: "encoding/json/v2", want: v2Refused},
		"openai imports encoding/json/jsontext":                         {rel: "openai", imp: "encoding/json/jsontext", want: v2Refused},
		"a test file of internal/schema imports encoding/json/v2":       {rel: "internal/schema", imp: "encoding/json/v2", test: true, want: v2Refused},
		"internal/schema lists encoding/json/jsontext and imports it":   {rel: "internal/schema", imp: "encoding/json/jsontext", extra: []string{"encoding/json/jsontext"}, want: v2Refused},
		"internal/jsonx imports the external json package":              {rel: "internal/jsonx", imp: externalJSONPath, want: externalRefused},
		"internal/jsonx imports the external jsontext package":          {rel: "internal/jsonx", imp: externalJSONPath + "/jsontext", want: externalRefused},
		"internal/jsonx lists the external jsontext and imports it":     {rel: "internal/jsonx", imp: externalJSONPath + "/jsontext", extra: []string{externalJSONPath + "/jsontext"}, want: externalRefused},
		"a test file of internal/jsonx imports the external json":       {rel: "internal/jsonx", imp: externalJSONPath, test: true, want: externalRefused},
		"the root package imports the external json package":            {rel: ".", imp: externalJSONPath, want: externalRefused},
		"a test file of the root package imports the external jsontext": {rel: ".", imp: externalJSONPath + "/jsontext", test: true, want: externalRefused},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			allowed, known := allowedImports[tt.rel]
			if !known {
				t.Fatalf("package %s is not in allowedImports", tt.rel)
			}
			allowed = append(slices.Clone(allowed), tt.extra...)
			allowed = slices.DeleteFunc(allowed, func(imp string) bool { return slices.Contains(tt.omit, imp) })
			if got := importViolation(tt.rel, tt.imp, allowed, tt.test); got != tt.want {
				t.Errorf("importViolation(%q, %q, %q, %v) = %q, want %q", tt.rel, tt.imp, allowed, tt.test, got, tt.want)
			}
		})
	}
}

// listModulePackages runs go list over every package of the module with the
// build tags given, from the module's root directory, which is this
// package's.
func listModulePackages(t *testing.T, tags string) []listedPackage {
	t.Helper()
	const format = `{{.ImportPath}}{{"\t"}}{{join .Imports " "}}{{"\t"}}{{join .TestImports " "}} {{join .XTestImports " "}}`
	//nolint:gosec // G204: the go command with the test's own fixed arguments.
	cmd := exec.CommandContext(t.Context(), goTool(t), "list", "-tags="+tags, "-f", format, "./...")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -tags=%q ./...: %v\n%s", tags, err, stderr.String())
	}
	var pkgs []listedPackage
	for line := range strings.Lines(string(out)) {
		fields := strings.Split(strings.TrimRight(line, "\r\n"), "\t")
		if len(fields) != 3 {
			t.Fatalf("go list printed %q; want three tab-separated fields", line)
		}
		rel, ok := strings.CutPrefix(fields[0], modulePath)
		if !ok || (rel != "" && !strings.HasPrefix(rel, "/")) {
			t.Fatalf("go list ./... reported %s, a package outside %s", fields[0], modulePath)
		}
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			rel = "."
		}
		pkgs = append(pkgs, listedPackage{rel: rel, imports: strings.Fields(fields[1]), tests: strings.Fields(fields[2])})
	}
	return pkgs
}

// goTool returns the go command that runs this test. go test puts the bin
// directory of its own toolchain first on PATH for the test binary and does
// not set GOROOT, so the go command found on PATH is the one running the
// test; GOROOT is not read, so a GOROOT set by the caller cannot select
// another toolchain.
func goTool(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go command is needed to list the module's packages: %v", err)
	}
	return p
}

// requestFieldMutants are RoundTrip bodies that TestRequestFieldPolicy must
// refuse: each reads, writes, passes or keeps the request otherwise than
// RoundTrip's contract allows.
var requestFieldMutants = map[string]string{ //nolint:gosec // G101: Go source texts of the test, which name headers and hold no credential.
	"passed to a function":             `helper(req)`,
	"assigned to a variable":           `r := req; _ = r`,
	"the header map indexed":           `_ = req.Header["X-TypeSafe-Retry-Count"]`,
	"the header's values read":         `_ = req.Header.Values("X-TypeSafe-Retry-Count")`,
	"another header read":              `_ = req.Header.Get("Authorization")`,
	"the header name held in a const":  `const name = "X-TypeSafe-Retry-Count"; _ = req.Header.Get(name)`,
	"a header written":                 `req.Header.Set("X-Other", "1")`,
	"the URL's query read":             `_ = req.URL.Query()`,
	"GetBody read":                     `_ = req.GetBody`,
	"ContentLength read":               `_ = req.ContentLength`,
	"kept in a field":                  `ad.last = req`,
	"captured by a closure":            `go func() { _ = req.Method }()`,
	"the body closed by a closure":     `defer func() { _ = req.Body.Close() }()`,
	"the body copied":                  `_, _ = io.Copy(io.Discard, req.Body)`,
	"the method written":               `req.Method = "GET"`,
	"the address of the path taken":    `_ = &req.URL.Path`,
	"returned from a function literal": `_ = func() any { return req }`,
}

// requestFieldSource returns a file of package adapter whose RoundTrip runs
// body.
func requestFieldSource(body string) map[string]string {
	return map[string]string{"roundtrip.go": `package adapter

import (
	"io"
	"net/http"
)

func (ad *Adapter) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer req.Body.Close()
		_, _ = io.ReadAll(req.Body)
	}
	_ = req.Header.Get("X-TypeSafe-Retry-Count")
	_, _, _ = req.Method, req.URL.Path, req.Context()
	` + body + `
	return &http.Response{Request: req}, nil
}
`}
}

// TestRequestFieldPolicy checks statically that RoundTrip uses its request
// only as its contract allows: it reads the method, the path and the
// context, reads the body to its end and closes it, reads one header,
// X-TypeSafe-Retry-Count, with Header.Get, and returns the request as its
// response's Request; it never passes, stores, copies or captures the
// request, and reads no other header, so the SDK's Authorization value
// never reaches it. A read or a copy of a Go map cannot be observed at run
// time, so the check reads the source. It refuses each of
// requestFieldMutants.
func TestRequestFieldPolicy(t *testing.T) {
	n, violations := requestFieldViolations(packageSources(t))
	if n != 1 {
		t.Fatalf("the package has %d methods RoundTrip of *Adapter, want 1", n)
	}
	for _, v := range violations {
		t.Errorf("RoundTrip: %s", v)
	}
	if n, v := requestFieldViolations(requestFieldSource("")); n != 1 || len(v) != 0 {
		t.Fatalf("the allowed form is refused: %d methods, %v", n, v)
	}
	for name, body := range requestFieldMutants {
		t.Run(name, func(t *testing.T) {
			if _, v := requestFieldViolations(requestFieldSource(body)); len(v) == 0 {
				t.Errorf("RoundTrip running %q passes the check", body)
			}
		})
	}
}

// TestRoundTripLeavesRequestUntouched checks that RoundTrip never writes
// to its request: 64 concurrent calls share one header map, as the SDK's
// calls do, and the map equals its copy taken before; the race detector
// sees any write.
func TestRoundTripLeavesRequestUntouched(t *testing.T) {
	ad := fakeAdapter(t, fakeAnswering(noulAnswer))
	header := retryCountHeader("2")
	before := header.Clone()
	const n = 64
	done := make(chan string, n)
	start := make(chan struct{})
	for i := range n {
		go func() {
			<-start
			body := &closeCounter{r: strings.NewReader(noulBody)}
			path := systemOnePath
			if i%8 == 0 {
				path = modelsPath
			}
			method := "POST"
			if path == modelsPath {
				method = "GET"
			}
			resp, err := ad.RoundTrip(requestWith(t.Context(), method, path, body, header))
			if err != nil {
				done <- err.Error()
				return
			}
			_ = resp.Body.Close()
			switch {
			case resp.Status != "200 OK":
				done <- resp.Status
			case body.closes.Load() != 1 || !body.ended.Load():
				done <- "the body was not read to its end and closed once"
			default:
				done <- ""
			}
		}()
	}
	close(start)
	for range n {
		if msg := <-done; msg != "" {
			t.Error(msg)
		}
	}
	if len(header) != len(before) {
		t.Fatalf("the header has %d names after the calls, %d before", len(header), len(before))
	}
	for name, values := range before {
		if !slices.Equal(header[name], values) {
			t.Errorf("header %s = %q after the calls, %q before", name, header[name], values)
		}
	}
}

// TestInvalidQuestionsAreRejected ports
// tests/test_client_with_fake_model.py::test_invalid_questions_are_rejected:
// an empty question set and a Choice or Score question with fewer than two
// criteria are refused before any provider request, with upstream's
// message. The SDK's Prepare refuses two of the cases itself; those reach
// the Adapter as raw bodies. A questions member that is null or an array is
// not a question set (400), and an empty object is the empty set (422).
func TestInvalidQuestionsAreRejected(t *testing.T) {
	tests := map[string]struct {
		// sdkRefuses is Prepare's error text for a case the SDK refuses
		// itself; empty when the SDK sends the case.
		sdkRefuses string
		// raw is the questions member sent as a raw body.
		raw         string
		wantStatus  int
		wantType    string
		wantMessage string
	}{
		"no-questions": {
			sdkRefuses:  "At least one question is required.",
			raw:         `{}`,
			wantStatus:  422,
			wantType:    "invalid_questions",
			wantMessage: "At least one question is required.",
		},
		"empty-score-criteria": {
			sdkRefuses:  `Score question "stars" has no criteria; at least one score is required.`,
			raw:         `{"stars":{"type":"score","instructions":"Rating.","criteria":[]}}`,
			wantStatus:  422,
			wantType:    "invalid_questions",
			wantMessage: "Score and choice questions require at least two criteria.",
		},
		"single-score-criterion": {
			raw:         `{"stars":{"type":"score","instructions":"Rating.","criteria":["Good."]}}`,
			wantStatus:  422,
			wantType:    "invalid_questions",
			wantMessage: "Score and choice questions require at least two criteria.",
		},
		"empty-choice-criteria": {
			raw:         `{"genre":{"type":"choice","instructions":"Genre.","criteria":{}}}`,
			wantStatus:  422,
			wantType:    "invalid_questions",
			wantMessage: "Score and choice questions require at least two criteria.",
		},
		"single-choice-criterion": {
			raw:         `{"genre":{"type":"choice","instructions":"Genre.","criteria":{"fiction":"A story."}}}`,
			wantStatus:  422,
			wantType:    "invalid_questions",
			wantMessage: "Score and choice questions require at least two criteria.",
		},
		"questions null": {
			raw:         `null`,
			wantStatus:  400,
			wantType:    "invalid_body",
			wantMessage: "The request body's questions is not a JSON object.",
		},
		"questions an array": {
			raw:         `[]`,
			wantStatus:  400,
			wantType:    "invalid_body",
			wantMessage: "The request body's questions is not a JSON object.",
		},
		"questions an empty object": {
			raw:         `{}`,
			wantStatus:  422,
			wantType:    "invalid_questions",
			wantMessage: "At least one question is required.",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := fakeAnswering(`{"answers":{}}`)
			ad := fakeAdapter(t, p)
			if tt.wantType == "invalid_questions" && !strings.Contains(tt.wantMessage, "required") && !strings.Contains(tt.wantMessage, "criteria") {
				t.Fatalf("message %q does not match upstream's pattern required|criteria", tt.wantMessage)
			}
			if _, isUpstream := map[string]bool{"no-questions": true, "empty-score-criteria": true, "single-score-criterion": true, "empty-choice-criteria": true, "single-choice-criterion": true}[name]; isUpstream {
				q, err := fm10Questions(name)
				switch {
				case tt.sdkRefuses != "" && (err == nil || !strings.Contains(err.Error(), tt.sdkRefuses)):
					t.Errorf("Prepare error = %v, want the SDK's own refusal %q", err, tt.sdkRefuses)
				case tt.sdkRefuses == "" && err != nil:
					t.Fatalf("Prepare error = %v; the SDK sends this case", err)
				case tt.sdkRefuses == "":
					_, err := sdkClient(t, ad, false).SystemOne(t.Context(), "state", q)
					status, errorType, message, report := apiErrorParts(err)
					if status != tt.wantStatus || errorType != tt.wantType || message != tt.wantMessage || report {
						t.Errorf("through the SDK: %d %q %q report %v, want %d %q %q no report", status, errorType, message, report, tt.wantStatus, tt.wantType, tt.wantMessage)
					}
				}
			}
			status, body, err := send(t, ad, rawRequest(t.Context(), "POST", systemOnePath, `{"state":"state","model":"fake","questions":`+tt.raw+`}`))
			if err != nil {
				t.Fatalf("RoundTrip error = %v", err)
			}
			message, errorType := detailOf(t, body)
			if status != tt.wantStatus || errorType != tt.wantType || message != tt.wantMessage {
				t.Errorf("raw body: %d %q %q, want %d %q %q", status, errorType, message, tt.wantStatus, tt.wantType, tt.wantMessage)
			}
			if p.Calls() != 0 {
				t.Errorf("provider calls = %d, want 0", p.Calls())
			}
		})
	}
}

// TestInvalidQuestionsListTheirDefects checks the list of a 422
// invalid_questions body: detail keeps upstream's message and the error
// type, and its member errors lists each defective question's place as
// ["body", "questions", <name>, <field>…] with the rule it breaks, in the
// request's order; a refusal that names no question has no such member.
func TestInvalidQuestionsListTheirDefects(t *testing.T) {
	p := fakeAnswering(`{"answers":{}}`)
	ad := fakeAdapter(t, p)
	_, err := sdkClient(t, ad, false).SystemOne(t.Context(), "state", rawQuestionsWithDefects(t))
	status, errorType, message, _ := apiErrorParts(err)
	if status != 422 || errorType != "invalid_questions" || message != "a question does not have the shape of its type" {
		t.Fatalf("SystemOne: %d %q %q", status, errorType, message)
	}
	got := detailErrors(t, apiErrorBody(err))
	want := []string{
		`["body","questions","zeta","color"]: a noul question has no such member`,
		`["body","questions","alpha","criteria"]: must be an object from each label to its criterion`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("detail.errors = %q, want %q", got, want)
	}
	_, body, err := send(t, ad, rawRequest(t.Context(), "POST", systemOnePath, `{"state":"s","model":"fake","questions":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := detailErrors(t, body); got != nil {
		t.Errorf("the empty set lists %q, want no member errors", got)
	}
	if got, want := detailNames(t, body), []string{"message", "error_type"}; !slices.Equal(got, want) {
		t.Errorf("the empty set's detail members %q, want %q", got, want)
	}
}

// TestUnknownPath checks that a request to any other method or path is
// answered 404 with {"detail": "Not Found"}, its body read to its end and
// closed once, or not touched when it is nil; a closed Adapter refuses it
// as any other request and still closes its body.
func TestUnknownPath(t *testing.T) {
	tests := map[string]struct {
		method, path string
	}{
		"GET of the evaluation path":   {method: "GET", path: systemOnePath},
		"PUT of the evaluation path":   {method: "PUT", path: systemOnePath},
		"POST of the models path":      {method: "POST", path: modelsPath},
		"a longer path":                {method: "POST", path: systemOnePath + "/more"},
		"another version":              {method: "POST", path: "/v2/systemone"},
		"the root":                     {method: "GET", path: "/"},
		"DELETE of the models path":    {method: "DELETE", path: modelsPath},
		"a path that only contains it": {method: "POST", path: "/v1/systemone.json"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			for _, withBody := range []bool{false, true} {
				ad := fakeAdapter(t, fakeAnswering(noulAnswer))
				var body *closeCounter
				req := requestWith(t.Context(), tt.method, tt.path, nil, retryCountHeader(""))
				if withBody {
					body = &closeCounter{r: strings.NewReader(noulBody)}
					req = requestWith(t.Context(), tt.method, tt.path, body, retryCountHeader(""))
				}
				status, got, err := send(t, ad, req)
				if err != nil || status != 404 || string(got) != `{"detail": "Not Found"}` {
					t.Errorf("RoundTrip = %d %s, %v; want 404 {\"detail\": \"Not Found\"}", status, got, err)
				}
				if withBody && (body.closes.Load() != 1 || !body.ended.Load()) {
					t.Errorf("the body was closed %d times, read to its end %v", body.closes.Load(), body.ended.Load())
				}
				if err := ad.Close(); err != nil {
					t.Fatal(err)
				}
				closedBody := &closeCounter{r: strings.NewReader(noulBody)}
				if _, _, err := send(t, ad, requestWith(t.Context(), tt.method, tt.path, closedBody, retryCountHeader(""))); !isClosedError(err) {
					t.Errorf("a closed Adapter answered %v, want the closed Error", err)
				}
				if closedBody.closes.Load() != 1 || !closedBody.ended.Load() {
					t.Errorf("a closed Adapter closed the body %d times, read it to its end %v", closedBody.closes.Load(), closedBody.ended.Load())
				}
			}
		})
	}
}

// TestModelsAndWarmUp checks GET /v1/models through the SDK: one card for
// the default model and one for each WithProvider name that is not the
// default, in the order they were registered, and no card for an Adapter
// with neither; WarmUp, which sends this request, succeeds. The request
// arrives without a body.
func TestModelsAndWarmUp(t *testing.T) {
	tests := map[string]struct {
		opts      []Option
		wantNames []string
	}{
		"a default of a factory and two providers": {
			opts:      []Option{WithFactory("openai", factoryOf(fakeAnswering(noulAnswer))), WithDefaultModel("openai:gpt-x"), WithProvider("zeta", fakeAnswering(noulAnswer)), WithProvider("alpha", fakeAnswering(noulAnswer))},
			wantNames: []string{"openai:gpt-x", "zeta", "alpha"},
		},
		"a default that is a provider": {
			opts:      []Option{WithProvider("zeta", fakeAnswering(noulAnswer)), WithProvider("alpha", fakeAnswering(noulAnswer)), WithDefaultModel("alpha")},
			wantNames: []string{"alpha", "zeta"},
		},
		"neither": {},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ad := newAdapter(t, tt.opts...)
			c := sdkClient(t, ad, false)
			resp, err := c.Models().List(t.Context())
			if err != nil {
				t.Fatalf("Models().List: %v", err)
			}
			var names []string
			for _, card := range resp.Models() {
				names = append(names, card.Name())
				if card.ReleaseDate() != "2026-09-22" || !strings.Contains(card.Description(), "system-one-adapter-go "+Version) {
					t.Errorf("card %q: description %q, release date %q", card.Name(), card.Description(), card.ReleaseDate())
				}
			}
			if !slices.Equal(names, tt.wantNames) {
				t.Errorf("model names = %q, want %q", names, tt.wantNames)
			}
			if err := c.WarmUp(t.Context()); err != nil {
				t.Errorf("WarmUp: %v", err)
			}
			body := &closeCounter{r: strings.NewReader("ignored")}
			status, _, err := send(t, ad, requestWith(t.Context(), "GET", modelsPath, body, retryCountHeader("")))
			if err != nil || status != 200 || body.closes.Load() != 1 {
				t.Errorf("GET with a body: %d, %v, closes %d", status, err, body.closes.Load())
			}
		})
	}
}

// TestRoundTripNeverReturnsNilResponse checks RoundTrip's contract on
// every failure class, through a transport that wraps the Adapter: a
// response with a body and the request as its Request, or an error, never
// both and never neither, and a request id on every response.
func TestRoundTripNeverReturnsNilResponse(t *testing.T) {
	for name, tt := range classCases() {
		t.Run(name, func(t *testing.T) {
			if n := contractChecked(t, tt); n == 0 {
				t.Error("no request reached the Adapter")
			}
		})
	}
	ad := fakeAdapter(t, fakeAnswering(noulAnswer))
	for _, body := range []string{"", "not JSON", `{"state":null,"model":"fake","questions":{}}`, noulBody} {
		if _, _, err := send(t, ad, rawRequest(t.Context(), "POST", systemOnePath, body)); err != nil {
			t.Errorf("body %q: %v", body, err)
		}
	}
}

// TestResponsesCarryARequestID checks that every response carries
// X-Typesafe-Request-Id: "adp_" and 16 lower-case hex digits, another on
// each response, which the SDK reads as the response's request id.
func TestResponsesCarryARequestID(t *testing.T) {
	ad := fakeAdapter(t, fakeAnswering(noulAnswer))
	seen := map[string]bool{}
	for i := range 50 {
		method, path, body := "POST", systemOnePath, noulBody
		switch i % 5 {
		case 1:
			method, path, body = "GET", modelsPath, ""
		case 2:
			path = "/v1/other"
		case 3:
			body = "not JSON"
		case 4:
			body = `{"state":"s","model":"fake","questions":{}}`
		}
		id := responseRequestID(t, ad, rawRequest(t.Context(), method, path, body))
		if len(id) != 20 || !strings.HasPrefix(id, "adp_") || strings.Trim(id[4:], "0123456789abcdef") != "" {
			t.Errorf("request id %q is not adp_ and 16 lower-case hex digits", id)
		}
		if seen[id] {
			t.Errorf("request id %q repeats", id)
		}
		seen[id] = true
	}
	resp, err := sdkClient(t, ad, false).SystemOne(t.Context(), "state", noulQuestions(t))
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := resp.Meta().RequestID(); !ok || !strings.HasPrefix(id, "adp_") {
		t.Errorf("the SDK reads request id %q, %v", id, ok)
	}
}

// TestInvalidQuestionsHoldNoContent checks that a 422 invalid_questions
// body is built from the parser's own texts and the members' names only:
// a marker in a question's instructions, in a criterion's value, in an
// unknown member's value and in an unknown type appears nowhere in it.
func TestInvalidQuestionsHoldNoContent(t *testing.T) {
	const marker = "content-marker-41d7"
	questions := `{"a":{"type":"noul","instructions":"` + marker + ` one","color":"` + marker + ` two"},` +
		`"b":{"type":"choice","criteria":{"x":"` + marker + ` three"},"size":["` + marker + ` four"]},` +
		`"c":{"type":"` + marker + ` five"}}`
	ad := fakeAdapter(t, fakeAnswering(noulAnswer))
	status, body, err := send(t, ad, rawRequest(t.Context(), "POST", systemOnePath, `{"state":"s","model":"fake","questions":`+questions+`}`))
	if err != nil || status != 422 {
		t.Fatalf("RoundTrip = %d %s, %v; want 422", status, body, err)
	}
	if strings.Contains(string(body), marker) {
		t.Errorf("the 422 body holds the request's content: %s", body)
	}
	want := []string{
		`["body","questions","a","color"]: a noul question has no such member`,
		`["body","questions","b","size"]: a choice question has no such member`,
		`["body","questions","c","type"]: must be noul, choice or score`,
	}
	if got := detailErrors(t, body); !slices.Equal(got, want) {
		t.Errorf("detail.errors = %q, want %q", got, want)
	}
}

// TestSeamContract asserts, through the root SDK this module is built
// against, every behaviour of the SDK the Adapter relies on: what a request
// carries, how the SDK keeps and reads a response or an error body, the
// class and the retries of every status, the retry count it sends, its
// header map, its deadlines, the size limit, the replacement of an error
// that prints the key, Prepare's own refusals, Close, and that it keeps the
// last of two round trippers. A later SDK that changes one of them fails
// here before the Adapter misbehaves.
func TestSeamContract(t *testing.T) {
	for name, check := range seamContractChecks() {
		t.Run(name, check)
	}
}
