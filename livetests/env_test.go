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
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

// liveTestsEnv is the switch that allows the tests of this package to call
// the billed API.
const liveTestsEnv = "TYPESAFE_LIVE_TESTS"

// liveEnv is what a live test needs from the environment. The key stays in
// memory: the scrubber needs it to find an echo of it in a body, and it is
// never printed.
type liveEnv struct {
	apiKey string
	host   string // the API host the SDK will call, for the test's log
}

// liveEnvFrom reads the live-test variables through getenv. It fails, naming
// every variable that is missing, unless TYPESAFE_LIVE_TESTS is 1 and
// TYPESAFE_API_KEY is not blank; no message repeats a variable's value. The
// base URL comes from TYPESAFE_BASE_URL or the SDK's default, as for any
// client; a value the SDK would refuse (no scheme, userinfo, a query) fails
// here too, without echoing it.
func liveEnvFrom(getenv func(string) string) (liveEnv, error) {
	var errs []error
	if strings.TrimSpace(getenv(liveTestsEnv)) != "1" {
		errs = append(errs, errors.New(liveTestsEnv+" is not 1: set it to 1 to allow the tests to call the billed API"))
	}
	key := strings.TrimSpace(getenv(typesafe.APIKeyEnv))
	if key == "" {
		errs = append(errs, errors.New(typesafe.APIKeyEnv+" is unset or blank: set it to the API key the tests call with"))
	}
	base := strings.TrimSpace(getenv(typesafe.BaseURLEnv))
	if base == "" {
		base = typesafe.DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		errs = append(errs, errors.New(typesafe.BaseURLEnv+" is not an http or https URL with a host and no userinfo, query or fragment"))
	}
	if len(errs) > 0 {
		return liveEnv{}, fmt.Errorf("live tests: %w", errors.Join(errs...))
	}
	return liveEnv{apiKey: key, host: u.Host}, nil
}

// redactedCredential replaces a credential that scrub removes.
const redactedCredential = "***"

// credentialShapes are what no recorded body may hold once scrub has run:
// a token that starts with the API key prefix ts_, a credential header
// written as a JSON member (a server that echoed request headers), and a
// bearer credential. Each finding names its shape, never the matched text.
var credentialShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a token with the API key prefix ts_", regexp.MustCompile(`(?i)\bts_[0-9a-z]`)},
	{"an Authorization, Proxy-Authorization or X-Api-Key member", regexp.MustCompile(`(?i)"(proxy-)?authorization"\s*:|"x-api-key"\s*:`)},
	{"a bearer credential", regexp.MustCompile(`(?i)\bbearer\s+[0-9a-z._~+/=-]{8,}`)},
}

// credentialFindings returns one line per credential in b: each secret that
// occurs in it, and each credential shape it matches.
func credentialFindings(b []byte, secrets ...string) []string {
	var out []string
	for i, s := range secrets {
		if s != "" && bytes.Contains(b, []byte(s)) {
			out = append(out, fmt.Sprintf("secret %d of %d occurs", i+1, len(secrets)))
		}
	}
	for _, shape := range credentialShapes {
		if shape.re.Match(b) {
			out = append(out, shape.name)
		}
	}
	return out
}

// scrub returns body with every occurrence of each secret replaced by ***,
// or an error, naming what it found and never the text, when the result
// still holds a secret or a credential shape (credentialShapes).
func scrub(body []byte, secrets ...string) ([]byte, error) {
	out := body
	for _, s := range secrets {
		if s != "" {
			out = bytes.ReplaceAll(out, []byte(s), []byte(redactedCredential))
		}
	}
	if found := credentialFindings(out, secrets...); len(found) > 0 {
		return nil, fmt.Errorf("the body still holds %s after scrubbing; nothing written", strings.Join(found, ", "))
	}
	return out, nil
}

// writeFixture scrubs body and writes the result to dir/name. When scrub
// refuses the body, the error is returned and no file is created, so a
// credential never reaches the disk.
func writeFixture(dir, name string, body []byte, secrets ...string) error {
	clean, err := scrub(body, secrets...)
	if err != nil {
		return fmt.Errorf("record %s: %w", name, err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("record %s: %w", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), clean, 0o600); err != nil {
		return fmt.Errorf("record %s: %w", name, err)
	}
	return nil
}
