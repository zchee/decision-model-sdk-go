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
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

// syntheticKey stands for the API key in the tests below; it has the real
// keys' ts_ prefix so the scrubber's shape rule sees what it would see.
const syntheticKey = "ts_synthetic_live_key_0123456789"

// TestLiveEnvGuard checks the guard every live test calls first (AC-F11's
// first clause): without TYPESAFE_LIVE_TESTS=1 and a non-blank
// TYPESAFE_API_KEY it fails naming each missing variable, and no message
// repeats a value. It runs on every go test, without the live tag.
func TestLiveEnvGuard(t *testing.T) {
	tests := map[string]struct {
		env      map[string]string
		wantErr  []string // parts the error must contain; nil: no error
		wantHost string
	}{
		"error: nothing set": {
			env:     map[string]string{},
			wantErr: []string{liveTestsEnv + " is not 1", typesafe.APIKeyEnv + " is unset or blank"},
		},
		"error: the key without the switch": {
			env:     map[string]string{typesafe.APIKeyEnv: syntheticKey},
			wantErr: []string{liveTestsEnv + " is not 1"},
		},
		"error: the switch set to something other than 1": {
			env:     map[string]string{liveTestsEnv: "true", typesafe.APIKeyEnv: syntheticKey},
			wantErr: []string{liveTestsEnv + " is not 1"},
		},
		"error: the switch without the key": {
			env:     map[string]string{liveTestsEnv: "1"},
			wantErr: []string{typesafe.APIKeyEnv + " is unset or blank"},
		},
		"error: a blank key": {
			env:     map[string]string{liveTestsEnv: "1", typesafe.APIKeyEnv: " \t "},
			wantErr: []string{typesafe.APIKeyEnv + " is unset or blank"},
		},
		"error: a base URL with userinfo is refused without echo": {
			env:     map[string]string{liveTestsEnv: "1", typesafe.APIKeyEnv: syntheticKey, typesafe.BaseURLEnv: "https://user:" + syntheticKey + "@api.example.com"},
			wantErr: []string{typesafe.BaseURLEnv + " is not an http or https URL"},
		},
		"error: a base URL without a scheme": {
			env:     map[string]string{liveTestsEnv: "1", typesafe.APIKeyEnv: syntheticKey, typesafe.BaseURLEnv: "api.example.com"},
			wantErr: []string{typesafe.BaseURLEnv + " is not an http or https URL"},
		},
		"success: the switch and the key, the default host": {
			env:      map[string]string{liveTestsEnv: "1", typesafe.APIKeyEnv: syntheticKey},
			wantHost: "api.typesafe.ai",
		},
		"success: blanks around the values are ignored": {
			env:      map[string]string{liveTestsEnv: " 1 ", typesafe.APIKeyEnv: " " + syntheticKey + "\n"},
			wantHost: "api.typesafe.ai",
		},
		"success: TYPESAFE_BASE_URL selects the host": {
			env:      map[string]string{liveTestsEnv: "1", typesafe.APIKeyEnv: syntheticKey, typesafe.BaseURLEnv: "https://staging.example.com/prefix"},
			wantHost: "staging.example.com",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := liveEnvFrom(func(k string) string { return tt.env[k] })
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("liveEnvFrom() = %+v, nil; want an error", got)
				}
				for _, part := range tt.wantErr {
					if !strings.Contains(err.Error(), part) {
						t.Errorf("liveEnvFrom() error = %q, want it to contain %q", err, part)
					}
				}
				if strings.Contains(err.Error(), syntheticKey) {
					t.Errorf("liveEnvFrom() error repeats the key: %q", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("liveEnvFrom() error = %v", err)
			}
			if got.apiKey != syntheticKey || got.host != tt.wantHost {
				t.Errorf("liveEnvFrom() = {key equal: %t, host: %q}, want {true, %q}", got.apiKey == syntheticKey, got.host, tt.wantHost)
			}
		})
	}
}

// goTool returns the go command that runs this test.
func goTool(t *testing.T) string {
	t.Helper()
	if gr := os.Getenv("GOROOT"); gr != "" {
		if p := filepath.Join(gr, "bin", "go"); fileExists(p) || fileExists(p+".exe") {
			return p
		}
	}
	p, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go command is needed to build the live tests: %v", err)
	}
	return p
}

func fileExists(p string) bool {
	_, err := os.Stat(p) //nolint:gosec // G703: p is GOROOT's own go command path, from the test's environment.
	return err == nil
}

// runGo runs the go command in this package's directory with env, and
// returns its combined output and exit error.
func runGo(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), goTool(t), args...) //nolint:gosec // G204: the go command with the test's own fixed arguments.
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// testNames returns the Test functions a go test -list output names.
func testNames(out string) []string {
	var names []string
	for line := range strings.SplitSeq(out, "\n") {
		if name := strings.TrimSpace(line); strings.HasPrefix(name, "Test") {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// TestLiveTestsFailWithoutEnv builds this package with the live tag in a
// child go test whose environment has no TYPESAFE_ variable, and checks
// AC-F11's first clause end to end: go test -list names every live test
// without the variables, and running them fails every one of them with the
// guard's message, none passing. The live tests are the ones -tags live
// adds to the list, so a new one is covered without an edit here; the four
// of the upstream port must be among them.
func TestLiveTestsFailWithoutEnv(t *testing.T) {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "TYPESAFE_") {
			env = append(env, kv)
		}
	}
	untagged, err := runGo(t, env, "test", "-list", ".*", ".")
	if err != nil {
		t.Fatalf("go test -list without the tag: %v\n%s", err, untagged)
	}
	tagged, err := runGo(t, env, "test", "-tags", "live", "-list", ".*", ".")
	if err != nil {
		t.Fatalf("go test -tags live -list without the variables: %v\n%s", err, tagged)
	}
	var live []string
	for _, name := range testNames(tagged) {
		if !slices.Contains(testNames(untagged), name) {
			live = append(live, name)
		}
	}
	for _, want := range []string{"TestLiveModels", "TestLiveQuestions", "TestLiveTypedResponse", "TestLiveUnauthenticated"} {
		if !slices.Contains(live, want) {
			t.Errorf("go test -tags live -list does not name %s; live tests found: %v", want, live)
		}
	}
	if t.Failed() {
		return
	}
	out, err := runGo(t, env, "test", "-tags", "live", "-count=1", "-v", "-run", "^("+strings.Join(live, "|")+")$", ".")
	if exitErr, ok := errors.AsType[*exec.ExitError](err); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("go test -tags live without the variables: err = %v, want exit status 1\n%s", err, out)
	}
	for _, name := range live {
		if !strings.Contains(out, "--- FAIL: "+name+" ") {
			t.Errorf("%s did not fail without the variables", name)
		}
	}
	if n := strings.Count(out, liveTestsEnv+" is not 1"); n != len(live) {
		t.Errorf("the guard's message appears %d times, want once per live test (%d)\n%s", n, len(live), out)
	}
	if strings.Contains(out, "--- PASS") {
		t.Errorf("a live test passed without the variables:\n%s", out)
	}
}

// TestScrubRefusesCredentials checks the recorder's scrubber on synthetic
// bodies: every occurrence of a known secret becomes ***, and a body that
// still holds a credential shape (a ts_ token the test did not know, an
// echoed credential header, a bearer credential) is refused with no file
// written and no message repeating the text.
func TestScrubRefusesCredentials(t *testing.T) {
	const otherKey = "wrong-live-key-0000000000"
	tests := map[string]struct {
		body    string
		secrets []string
		want    string // the file written; "" when refused
		wantErr string
	}{
		"success: a body without credentials is written unchanged": {
			body:    `{"model":"jev","usage":{},"answers":{}}`,
			secrets: []string{syntheticKey},
			want:    `{"model":"jev","usage":{},"answers":{}}`,
		},
		"success: an echoed key is replaced wherever it occurs": {
			body:    `{"detail":{"message":"key ` + syntheticKey + ` is invalid","key":"` + syntheticKey + `"}}`,
			secrets: []string{syntheticKey},
			want:    `{"detail":{"message":"key *** is invalid","key":"***"}}`,
		},
		"success: the wrong key of the unauthenticated test is replaced too": {
			body:    `{"detail":"bad key ` + otherKey + `"}`,
			secrets: []string{syntheticKey, otherKey},
			want:    `{"detail":"bad key ***"}`,
		},
		"success: words holding ts_ inside them are not key shapes": {
			body:    `{"counts_total":1,"results_ts":"x"}`,
			secrets: []string{syntheticKey},
			want:    `{"counts_total":1,"results_ts":"x"}`,
		},
		"error: an unknown key-shaped token is refused": {
			body:    `{"echo":"ts_another_key_5555"}`,
			secrets: []string{syntheticKey},
			wantErr: "a token with the API key prefix ts_",
		},
		"error: an echoed Authorization member is refused": {
			body:    `{"headers":{"Authorization":"***"}}`,
			secrets: []string{syntheticKey},
			wantErr: "an Authorization, Proxy-Authorization or X-Api-Key member",
		},
		"error: an echoed x-api-key member is refused": {
			body:    `{"headers":{"x-api-key":"abc"}}`,
			secrets: []string{syntheticKey},
			wantErr: "an Authorization, Proxy-Authorization or X-Api-Key member",
		},
		"error: a bearer credential is refused": {
			body:    `{"note":"Bearer abcdefgh12345678"}`,
			secrets: []string{syntheticKey},
			wantErr: "a bearer credential",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			err := writeFixture(dir, "body.json", []byte(tt.body), tt.secrets...)
			data, readErr := os.ReadFile(filepath.Join(dir, "body.json"))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("writeFixture() error = %v, want one containing %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "ts_another") || strings.Contains(err.Error(), "abcdefgh") {
					t.Errorf("writeFixture() error repeats the credential: %q", err)
				}
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Errorf("a refused body reached the disk: read error = %v", readErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("writeFixture() error = %v", err)
			}
			if diff := gocmp.Diff(tt.want, string(data)); diff != "" {
				t.Errorf("written file (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRecorderOnSyntheticResponses runs the SDK against a local server that
// echoes the request's key in its response bodies and headers, and records
// what a live test records: a 2xx body from Meta().RawBody() and an error
// body from APIError.Body. The files on disk hold the bodies with the key
// replaced by ***, and nothing else changed.
func TestRecorderOnSyntheticResponses(t *testing.T) {
	// An http:// base URL defaults to HTTPAuto, so the SDK speaks HTTP/1.1
	// to this server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Echo", key)
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"detail":{"error_type":"authentication_error","message":"key `+key+` is not valid"}}`) //nolint:gosec // G705: a test server echoing the test's own synthetic key on purpose.
			return
		}
		_, _ = io.WriteString(w, `{"model":"jev","usage":{},"answers":{"spam":{"type":"noul","noul":0.25}},"echo":"`+key+`"}`) //nolint:gosec // G705: a test server echoing the test's own synthetic key on purpose.
	}))
	t.Cleanup(srv.Close)

	c, err := typesafe.NewClient(typesafe.WithAPIKey(syntheticKey), typesafe.WithBaseURL(srv.URL), typesafe.WithRetry(typesafe.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	qs, err := typesafe.NewQuestions().Noul("spam", typesafe.Noul{Instructions: typesafe.Text("Spam?")}).Prepare()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.SystemOne(t.Context(), "state", qs)
	if err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}
	_, err = c.Models().List(t.Context())
	apiErr, ok := errors.AsType[*typesafe.APIError](err)
	if !ok {
		t.Fatalf("Models().List() error = %v, want an *APIError", err)
	}

	dir := t.TempDir()
	if err := writeFixture(dir, "ok.json", resp.Meta().RawBody(), syntheticKey); err != nil {
		t.Fatal(err)
	}
	if err := writeFixture(dir, "forbidden.json", apiErr.Body, syntheticKey); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ok.json":        `{"model":"jev","usage":{},"answers":{"spam":{"type":"noul","noul":0.25}},"echo":"***"}`,
		"forbidden.json": `{"detail":{"error_type":"authentication_error","message":"key *** is not valid"}}`,
	}
	for name, body := range want {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if diff := gocmp.Diff(body, string(data)); diff != "" {
			t.Errorf("%s (-want +got):\n%s", name, diff)
		}
		if found := credentialFindings(data, syntheticKey); len(found) > 0 {
			t.Errorf("%s holds %v", name, found)
		}
	}
	// The raw bodies did hold the key: the scrub, not the server, removed it.
	if !bytes.Contains(resp.Meta().RawBody(), []byte(syntheticKey)) || !bytes.Contains(apiErr.Body, []byte(syntheticKey)) {
		t.Errorf("the synthetic server did not echo the key; the test proves nothing")
	}
}

// recordedBodies are the bodies the owner-run live pass recorded (-record):
// each scenario's response, and the two authentication failures.
var recordedBodies = []string{
	"models.json",
	"questions.json",
	"typed-response.json",
	"unauthenticated.json",
	"wrong-key.json",
}

// TestRecordedBodiesHoldNoCredentials checks what reached testdata/live:
// exactly the recorded bodies, each a JSON object as the API sent it (no
// trailing newline added), with no credential shape in it: no ts_ token, no
// Authorization or X-Api-Key member, no bearer credential. When the
// environment holds TYPESAFE_API_KEY, as on the machine that recorded them,
// the key's bytes must not occur either; the check prints nothing of it.
// internal/codec's TestLiveBodiesOneScan decodes every one of them.
func TestRecordedBodiesHoldNoCredentials(t *testing.T) {
	dir := filepath.Join("..", "testdata", "live")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			onDisk = append(onDisk, e.Name())
		}
	}
	if diff := gocmp.Diff(recordedBodies, onDisk); diff != "" {
		t.Fatalf("testdata/live/*.json (-want +got):\n%s", diff)
	}
	key := strings.TrimSpace(os.Getenv(typesafe.APIKeyEnv))
	for _, name := range onDisk {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if found := credentialFindings(data, key, wrongLiveKey); len(found) > 0 {
			t.Errorf("%s holds %s", name, strings.Join(found, ", "))
		}
		if len(data) < 2 || data[0] != '{' || data[len(data)-1] != '}' {
			t.Errorf("%s is not a JSON object as the API sent it: %d bytes from %q to %q", name, len(data), data[:min(len(data), 1)], data[max(len(data)-1, 0):])
		}
	}
}
