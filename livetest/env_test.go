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

package livetest

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	decision "github.com/zchee/decision-model-sdk-go"
)

// liveTestsEnv is the switch that allows the tests of this package to call
// the billed API.
const liveTestsEnv = "DECISION_MODEL_LIVE_TESTS"

// wrongLiveKey is the key TestLiveUnauthenticated sends: printable ASCII,
// so the SDK accepts it, and no key the API issues. The recorder scrubs it
// as it scrubs the real one.
const wrongLiveKey = "invalid-live-test-key-00000000"

// liveEnv is what a live test needs from the environment. The key stays in
// memory: the scrubber needs it to find an echo of it in a body, and it is
// never printed.
type liveEnv struct {
	apiKey string
	host   string // the API host the SDK will call, for the test's log
}

// liveEnvFrom reads the live-test variables through getenv. It fails, naming
// every variable that is missing, unless DECISION_MODEL_LIVE_TESTS is 1 and
// DECISION_MODEL_API_KEY, DECISION_MODEL_BASE_URL and
// DECISION_MODEL_DEFAULT_MODEL are not blank; no message repeats a variable's
// value. The environment alone names the vendor the tests call and its model:
// the SDK has no default for either, and the tests name none in code. A base
// URL the SDK would refuse (no scheme, userinfo, a query) fails here too,
// without echoing it.
func liveEnvFrom(getenv func(string) string) (liveEnv, error) {
	var errs []error
	if strings.TrimSpace(getenv(liveTestsEnv)) != "1" {
		errs = append(errs, errors.New(liveTestsEnv+" is not 1: set it to 1 to allow the tests to call the billed API"))
	}
	key := strings.TrimSpace(getenv(decision.APIKeyEnv))
	if key == "" {
		errs = append(errs, errors.New(decision.APIKeyEnv+" is unset or blank: set it to the API key the tests call with"))
	}
	var host string
	if base := strings.TrimSpace(getenv(decision.BaseURLEnv)); base == "" {
		errs = append(errs, errors.New(decision.BaseURLEnv+" is unset or blank: set it to the base URL of the API the tests call"))
	} else if u, err := url.Parse(base); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		errs = append(errs, errors.New(decision.BaseURLEnv+" is not an http or https URL with a host and no userinfo, query or fragment"))
	} else {
		host = u.Host
	}
	if strings.TrimSpace(getenv(decision.DefaultModelEnv)) == "" {
		errs = append(errs, errors.New(decision.DefaultModelEnv+" is unset or blank: set it to the model the tests ask"))
	}
	if len(errs) > 0 {
		return liveEnv{}, fmt.Errorf("live tests: %w", errors.Join(errs...))
	}
	return liveEnv{apiKey: key, host: host}, nil
}

// redactedCredential replaces a credential that scrub removes.
const redactedCredential = "***"

// credentialShapes are what no recorded body may hold once scrub has run: a
// token that starts with the API key prefix ts_, a credential header written
// as a JSON member (a server that echoed request headers), and a bearer
// credential. The header names are the ones internal/engine treats as
// credentials: the six its secretHeaderNames lists, and a name that holds
// "token" or "secret". That last rule matches only a header-style member name
// (letters, digits and hyphens), so the body's own usage.input_tokens is not
// one. Each finding names its shape, never the matched text.
var credentialShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a token with the API key prefix ts_", regexp.MustCompile(`(?i)\bts_[0-9a-z]`)},
	{"a credential header as a member", regexp.MustCompile(`(?i)"(?:(?:proxy-)?authorization|(?:x-)?api-key|(?:set-)?cookie|[a-z0-9-]*(?:token|secret)[a-z0-9-]*)"\s*:`)},
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
	out := replaceSecrets(body, secrets)
	if found := credentialFindings(out, secrets...); len(found) > 0 {
		return nil, fmt.Errorf("the body still holds %s after scrubbing; nothing written", strings.Join(found, ", "))
	}
	return out, nil
}

// replaceSecrets returns b with every occurrence of each non-empty secret
// replaced by ***.
func replaceSecrets(b []byte, secrets []string) []byte {
	for _, s := range secrets {
		if s != "" {
			b = bytes.ReplaceAll(b, []byte(s), []byte(redactedCredential))
		}
	}
	return b
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
