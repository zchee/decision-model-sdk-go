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

package livetests

import (
	"bytes"
	"maps"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	typesafe "github.com/zchee/typesafe-sdk-go"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// exampleOutputs holds, for each program under examples/, the pattern its
// standard output must match whatever the answers' values are. Every
// program under examples/ must have a row, and every row a program.
var exampleOutputs = map[string]*regexp.Regexp{
	"concurrency": regexp.MustCompile(`^("[^"]*"\s+(positive|neutral|negative)\n){3}\d+ attempts on \d+ connection\(s\)\n$`),
	"logging":     regexp.MustCompile(`^greeting: \d\.\d\d\n$`),
	"options":     regexp.MustCompile(`^([^:\n]+: (billing|other) \(3 answers from \S+\)\n){4}(model \S+ \(\S+\)\n)+$`),
	"quickstart":  regexp.MustCompile(`^(billing|technical|other)\n$`),
	"retries":     regexp.MustCompile(`^without retries: refund \d\.\d\d\nwith a predicate: refund \d\.\d\d\n$`),
	"transport":   regexp.MustCompile(`^\d+ model\(s\)\nquestion: \d\.\d\d after 1 request\(s\)\n$`),
	"typed":       regexp.MustCompile(`^billing: \d\.\d\d\ntone: (calm|frustrated|angry) \(confidence \d\.\d\d\)\nurgency: \d\.\d\d\n(spam: \d\.\d\d\n)?request \S+: tone (calm|frustrated|angry)\n$`),
}

// environWithout returns the process environment without the variables
// whose name starts with prefix, then extra.
func environWithout(prefix string, extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), prefix) {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

// redacted returns s with each secret replaced by ***.
func redacted(s string, secrets []string) string {
	for _, k := range secrets {
		if k != "" {
			s = strings.ReplaceAll(s, k, redactedCredential)
		}
	}
	return s
}

// runExample runs go run ./examples/<name> from the module's root with env,
// and returns its standard output and error with every secret redacted. A
// program that fails, or prints a secret, fails the test; what the test
// prints is redacted too.
func runExample(t *testing.T, name string, env, secrets []string) (stdout, stderr string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), goTool(t), "run", "./examples/"+name) //nolint:gosec // G204: the go command with the test's own fixed arguments.
	cmd.Dir = ".."
	cmd.Env = env
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	stdout, stderr = redacted(out.String(), secrets), redacted(errOut.String(), secrets)
	if stdout != out.String() || stderr != errOut.String() {
		t.Errorf("examples/%s printed a key (shown as ***)", name)
	}
	if err != nil {
		t.Fatalf("go run ./examples/%s: %v\nstdout:\n%s\nstderr:\n%s", name, err, stdout, stderr)
	}
	return stdout, stderr
}

// checkExamples runs every program under examples/ with env and checks its
// output: the exampleOutputs pattern on standard output, and, for the
// logging example, records that show the Authorization header as *** and
// one INFO record per call.
func checkExamples(t *testing.T, env, secrets []string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if diff := gocmp.Diff(slices.Sorted(maps.Keys(exampleOutputs)), dirs); diff != "" {
		t.Fatalf("examples/ and exampleOutputs differ (-exampleOutputs +examples/):\n%s", diff)
	}
	for _, name := range dirs {
		t.Run(name, func(t *testing.T) {
			stdout, stderr := runExample(t, name, env, secrets)
			if !exampleOutputs[name].MatchString(stdout) {
				t.Errorf("examples/%s printed\n%s\nwhich does not match %s\nstderr:\n%s", name, stdout, exampleOutputs[name], stderr)
			}
			if name == "logging" {
				for _, want := range []string{"level=INFO msg=response ", "level=DEBUG msg=request ", "headers.Authorization=***"} {
					if !strings.Contains(stderr, want) {
						t.Errorf("examples/logging's records hold no %q:\n%s", want, stderr)
					}
				}
			}
		})
	}
}

// fakeExampleKey is the key the examples send to the fake API.
const fakeExampleKey = "fake-example-key-0000000000"

// TestExamplesOffline runs every program under examples/ (XD1) against
// testsupport.FakeAPI, an in-process stand-in for the API, with the
// environment the programs read: TYPESAFE_API_KEY and TYPESAFE_BASE_URL,
// and no other TYPESAFE_ variable. Each must exit 0 and print what its
// exampleOutputs pattern describes; none may print the key. The live
// variant, TestExamples, runs them against the API itself (-tags live).
func TestExamplesOffline(t *testing.T) {
	api := &testsupport.FakeAPI{}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	env := environWithout("TYPESAFE_", typesafe.APIKeyEnv+"="+fakeExampleKey, typesafe.BaseURLEnv+"="+srv.URL)
	checkExamples(t, env, []string{fakeExampleKey})
	if api.Requests() == 0 {
		t.Error("the fake API served no request")
	}
}
