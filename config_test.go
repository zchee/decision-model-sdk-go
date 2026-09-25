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
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// noEnv is a getenv that finds no variable, so every setting an option
// leaves unset takes its default.
func noEnv(string) string { return "" }

// mapEnv returns a getenv that finds the variables in m.
func mapEnv(m map[string]string) func(string) string {
	return func(name string) string { return m[name] }
}

// clearEnv unsets, for the rest of the test, the three variables a client
// reads, whatever the shell running the test holds (a developer's
// TYPESAFE_API_KEY among them); t.Setenv restores them when the test ends.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{APIKeyEnv, BaseURLEnv, DefaultModelEnv} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}

// resolveConfig resolves opts against the environment getenv reads, as
// NewClient does against the process environment.
func resolveConfig(getenv func(string) string, opts ...ClientOption) (*config, error) {
	o := collectOptions(opts)
	return o.resolve(getenv)
}

// mustResolve resolves opts, failing the test when that fails.
func mustResolve(t *testing.T, getenv func(string) string, opts ...ClientOption) *config {
	t.Helper()
	c, err := resolveConfig(getenv, opts...)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return c
}

// resolveError resolves opts, which must fail with a *ConfigError, and
// returns it.
func resolveError(t *testing.T, getenv func(string) string, opts ...ClientOption) *ConfigError {
	t.Helper()
	c, err := resolveConfig(getenv, opts...)
	if err == nil {
		t.Fatalf("resolve = %+v, want an error", c)
	}
	if c != nil {
		t.Errorf("resolve returned a config with its error")
	}
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("resolve error = %T %v, want a *ConfigError", err, err)
	}
	return ce
}

// errorTexts returns every way err and each error it wraps can be printed:
// %v, %+v, %#v and %q.
func errorTexts(err error) []string {
	var texts []string
	var walk func(error)
	walk = func(err error) {
		if err == nil {
			return
		}
		texts = append(texts, fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err), fmt.Sprintf("%q", err))
		switch u := err.(type) { //nolint:errorlint // visits each link of the chain as it is; errors.As would skip links.
		case interface{ Unwrap() error }:
			walk(u.Unwrap())
		case interface{ Unwrap() []error }:
			for _, e := range u.Unwrap() {
				walk(e)
			}
		}
	}
	walk(err)
	return texts
}

// assertNotPrinted fails the test when any printed form of err, or of an
// error it wraps, contains secret.
func assertNotPrinted(t *testing.T, err error, secret string) {
	t.Helper()
	for _, text := range errorTexts(err) {
		if strings.Contains(text, secret) {
			t.Errorf("error text %q contains %q", text, secret)
		}
	}
}

// TestMissingAPIKey ports test_missing_key (F4): no key from any source, an
// empty variable and a blank one are the same error, which names the
// variable to set.
func TestMissingAPIKey(t *testing.T) {
	tests := map[string]struct {
		set   bool
		value string
	}{
		"error: variable unset":                        {},
		"error: variable empty":                        {set: true, value: ""},
		"error: variable blank":                        {set: true, value: " \t\n "},
		"error: variable blank by Python's separators": {set: true, value: "\x1c\x1d\x1e\x1f\u3000"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			if tt.set {
				t.Setenv(APIKeyEnv, tt.value)
			}
			err := resolveError(t, os.Getenv)
			const want = "No API key was provided. Pass WithAPIKey or set the TYPESAFE_API_KEY environment variable."
			if got := err.Error(); got != want {
				t.Errorf("Error() = %q, want %q", got, want)
			}
		})
	}
}

// TestAPIKeyTrimmed ports test_api_key_whitespace (F5): a key from either
// source is trimmed as Python's str.strip() trims it before it is sent. The
// last two paddings go past upstream's four: the ASCII separators Python
// trims and Go's unicode.IsSpace does not, and Unicode spaces.
func TestAPIKeyTrimmed(t *testing.T) {
	paddings := map[string]string{
		"none":             "",
		"LF":               "\n",
		"CRLF":             "\r\n",
		"mixed":            " \t\r\n ",
		"ASCII separators": "\x1c\x1d\x1e\x1f",
		"Unicode spaces":   "\u00a0\u2003\u3000",
	}
	tests := map[string]struct {
		fromEnv bool
	}{
		"success: environment": {fromEnv: true},
		"success: WithAPIKey":  {fromEnv: false},
	}
	for name, tt := range tests {
		for padName, pad := range paddings {
			t.Run(name+" padded with "+padName, func(t *testing.T) {
				clearEnv(t)
				key := pad + "test-key" + pad
				var opts []ClientOption
				if tt.fromEnv {
					t.Setenv(APIKeyEnv, key)
				} else {
					t.Setenv(APIKeyEnv, "env-key")
					opts = append(opts, WithAPIKey(key))
				}
				c := mustResolve(t, os.Getenv, opts...)
				if c.apiKey != "test-key" {
					t.Errorf("apiKey = %q, want %q", c.apiKey, "test-key")
				}
				for _, h := range []http.Header{c.systemOneHeader, c.modelsHeader} {
					if got := h.Get(headerAuthorization); got != "Bearer test-key" {
						t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
					}
				}
			})
		}
	}
}

// TestInvalidExplicitKeyDoesNotFallBack ports
// test_invalid_explicit_key_does_not_fall_back_to_env (F6): a key given
// with WithAPIKey is the key, even when it is unusable; the environment's
// usable key is not taken in its place.
func TestInvalidExplicitKeyDoesNotFallBack(t *testing.T) {
	tests := map[string]struct {
		key  string
		want string
	}{
		"error: empty": {
			key:  "",
			want: "The API key passed to WithAPIKey is empty; the TYPESAFE_API_KEY environment variable is not read when WithAPIKey is given.",
		},
		"error: blank": {
			key:  " \t\r\n ",
			want: "The API key passed to WithAPIKey is empty; the TYPESAFE_API_KEY environment variable is not read when WithAPIKey is given.",
		},
		"error: leading NUL": {
			key:  "\x00private",
			want: "The API key passed to WithAPIKey must contain only printable ASCII characters without whitespace.",
		},
		"error: trailing NUL": {
			key:  "private\x00",
			want: "The API key passed to WithAPIKey must contain only printable ASCII characters without whitespace.",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(APIKeyEnv, "env-key")
			err := resolveError(t, os.Getenv, WithAPIKey(tt.key))
			if got := err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
			assertNotPrinted(t, err, "private")
		})
	}
}

// TestInvalidAPIKeyNeverEchoed ports test_invalid_api_key (F7): a key with
// a character outside printable ASCII without whitespace is refused from
// either source, by an error that names the source and never prints the
// key, in any form, through any error it wraps.
func TestInvalidAPIKeyNeverEchoed(t *testing.T) {
	const credential = "ts_live_private"
	characters := map[string]string{
		"LF":                "\n",
		"CR":                "\r",
		"tab":               "\t",
		"unit separator":    "\x1f",
		"DEL":               "\x7f",
		"space":             " ",
		"e acute":           "\u00e9",
		"zero-width space":  "\u200b",
		"invalid UTF-8":     "\xff",
		"NUL":               "\x00",
		"non-breaking":      "\u00a0",
		"line separator":    "\u2028",
		"replacement char":  "\ufffd",
		"C1 control (NEL)":  "\u0085",
		"ideographic space": "\u3000",
	}
	tests := map[string]struct {
		fromEnv bool
		want    string
	}{
		"error: environment": {
			fromEnv: true,
			want:    "The API key in the TYPESAFE_API_KEY environment variable must contain only printable ASCII characters without whitespace.",
		},
		"error: WithAPIKey": {
			fromEnv: false,
			want:    "The API key passed to WithAPIKey must contain only printable ASCII characters without whitespace.",
		},
	}
	for name, tt := range tests {
		for charName, char := range characters {
			if tt.fromEnv && strings.ContainsRune(char, 0) {
				// No environment variable can hold NUL: setenv(3) refuses it.
				continue
			}
			t.Run(name+" with "+charName, func(t *testing.T) {
				clearEnv(t)
				key := credential + char + "suffix"
				var opts []ClientOption
				if tt.fromEnv {
					t.Setenv(APIKeyEnv, key)
				} else {
					t.Setenv(APIKeyEnv, "env-key")
					opts = append(opts, WithAPIKey(key))
				}
				err := resolveError(t, os.Getenv, opts...)
				if got := err.Error(); got != tt.want {
					t.Errorf("Error() = %q, want %q", got, tt.want)
				}
				assertNotPrinted(t, err, credential)
				assertNotPrinted(t, err, "suffix")
			})
		}
	}
}

// TestBlankEnvIsUnset ports test_empty_env_unset (F8): a variable that is
// blank once trimmed counts as unset, so the defaults apply.
// TYPESAFE_LOG_LEVEL is set as upstream sets it, and is not read at all
// (Appendix B, "TYPESAFE_LOG_LEVEL not read").
func TestBlankEnvIsUnset(t *testing.T) {
	tests := map[string]struct {
		blank string
	}{
		"success: spaces and tabs":             {blank: " \t "},
		"success: Python-only separators":      {blank: "\x1c\x1d\x1e\x1f"},
		"success: Unicode spaces and newlines": {blank: "\u00a0\u2028\r\n\u3000"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for _, v := range []string{BaseURLEnv, DefaultModelEnv, "TYPESAFE_LOG_LEVEL"} {
				t.Setenv(v, tt.blank)
			}
			c := mustResolve(t, os.Getenv, WithAPIKey("test-key"))
			got := [3]string{c.systemOneURL.String(), c.modelsURL.String(), c.model}
			want := [3]string{"https://api.typesafe.ai/v1/systemone", "https://api.typesafe.ai/v1/models", "jev-latest"}
			if got != want {
				t.Errorf("(system one URL, models URL, model) = %q, want %q", got, want)
			}
		})
	}
}

// TestInvalidTimeout ports test_invalid_timeout (F9) for the client half:
// a timeout that is not positive is refused when the client is built.
// Upstream's float("inf") and float("nan") rows have no time.Duration to
// stand for them; WithNoTimeout is how a caller asks for no deadline, and
// asking for both a timeout and none is refused too. The per-call half
// (a call's Timeout option) belongs to the request path.
func TestInvalidTimeout(t *testing.T) {
	const (
		notPositive = "The timeout passed to WithTimeout must be positive; use WithNoTimeout for no deadline."
		both        = "WithTimeout and WithNoTimeout cannot be used together: the first sets a timeout, the second removes it."
		connect     = "The connect timeout passed to WithConnectTimeout must be positive."
	)
	tests := map[string]struct {
		opts []ClientOption
		want string
	}{
		"error: zero":                           {opts: []ClientOption{WithTimeout(0)}, want: notPositive},
		"error: minus one nanosecond":           {opts: []ClientOption{WithTimeout(-1)}, want: notPositive},
		"error: minus one second":               {opts: []ClientOption{WithTimeout(-time.Second)}, want: notPositive},
		"error: valid, then zero":               {opts: []ClientOption{WithTimeout(time.Second), WithTimeout(0)}, want: notPositive},
		"error: WithTimeout then WithNoTimeout": {opts: []ClientOption{WithTimeout(time.Second), WithNoTimeout()}, want: both},
		"error: WithNoTimeout then WithTimeout": {opts: []ClientOption{WithNoTimeout(), WithTimeout(time.Second)}, want: both},
		"error: zero WithTimeout and WithNoTimeout": {
			opts: []ClientOption{WithTimeout(0), WithNoTimeout()},
			want: both,
		},
		"error: zero connect timeout":     {opts: []ClientOption{WithConnectTimeout(0)}, want: connect},
		"error: negative connect timeout": {opts: []ClientOption{WithConnectTimeout(-time.Second)}, want: connect},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := resolveError(t, noEnv, append([]ClientOption{WithAPIKey("test-key")}, tt.opts...)...)
			if got := err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
			if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "Timeout") {
				t.Errorf("Error() = %q does not name the timeout", err.Error())
			}
		})
	}
}

// TestTimeoutSettings pins what the accepted timeout options resolve to.
// The "per-phase" case is the Go form of test_timeout_object (F10,
// Appendix B "one deadline per attempt"): httpx.Timeout(7.0, connect=1.0)
// becomes one deadline per attempt plus a connect deadline inside it.
func TestTimeoutSettings(t *testing.T) {
	tests := map[string]struct {
		opts        []ClientOption
		wantTimeout time.Duration
		wantConnect time.Duration
	}{
		"success: defaults": {
			wantTimeout: 10 * time.Second, wantConnect: 10 * time.Second,
		},
		"success: per-phase": {
			opts:        []ClientOption{WithTimeout(7 * time.Second), WithConnectTimeout(time.Second)},
			wantTimeout: 7 * time.Second, wantConnect: time.Second,
		},
		"success: no timeout is zero": {
			opts:        []ClientOption{WithNoTimeout()},
			wantTimeout: 0, wantConnect: 10 * time.Second,
		},
		"success: later WithTimeout wins": {
			opts:        []ClientOption{WithTimeout(3 * time.Second), WithTimeout(2 * time.Second)},
			wantTimeout: 2 * time.Second, wantConnect: 10 * time.Second,
		},
		"success: smallest positive": {
			opts:        []ClientOption{WithTimeout(1), WithConnectTimeout(1)},
			wantTimeout: 1, wantConnect: 1,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := mustResolve(t, noEnv, append([]ClientOption{WithAPIKey("test-key")}, tt.opts...)...)
			if c.timeout != tt.wantTimeout || c.connectTimeout != tt.wantConnect {
				t.Errorf("(timeout, connect timeout) = (%v, %v), want (%v, %v)", c.timeout, c.connectTimeout, tt.wantTimeout, tt.wantConnect)
			}
		})
	}
}

// TestConfigResolutionOrder is the configuration half of test_resolution
// (F3): each setting comes from its option, else from its trimmed
// variable, else from the default, independently of the others. Whether
// the resolved values reach the wire is the client's test.
func TestConfigResolutionOrder(t *testing.T) {
	env := map[string]string{ //nolint:gosec // G101: test values, not credentials.
		APIKeyEnv:       "  env-key  ",
		BaseURLEnv:      "  https://env.test///  ",
		DefaultModelEnv: "  env-model  ",
	}
	type resolved struct {
		Authorization, SystemOne, Models, Model string
		Timeout                                 time.Duration
	}
	tests := map[string]struct {
		env  map[string]string
		opts []ClientOption
		want resolved
	}{
		"success: default": {
			opts: []ClientOption{WithAPIKey("test-key")},
			want: resolved{"Bearer test-key", "https://api.typesafe.ai/v1/systemone", "https://api.typesafe.ai/v1/models", "jev-latest", 10 * time.Second},
		},
		"success: environment": {
			env:  env,
			want: resolved{"Bearer env-key", "https://env.test/v1/systemone", "https://env.test/v1/models", "env-model", 10 * time.Second},
		},
		"success: options": {
			env:  env,
			opts: []ClientOption{WithAPIKey("code-key"), WithBaseURL("https://code.test///"), WithModel("code-model")},
			want: resolved{"Bearer code-key", "https://code.test/v1/systemone", "https://code.test/v1/models", "code-model", 10 * time.Second},
		},
		"success: option key, variable URL, default model": {
			env:  map[string]string{BaseURLEnv: env[BaseURLEnv]},
			opts: []ClientOption{WithAPIKey("code-key")},
			want: resolved{"Bearer code-key", "https://env.test/v1/systemone", "https://env.test/v1/models", "jev-latest", 10 * time.Second},
		},
		"success: variable key, default URL, option model": {
			env:  map[string]string{APIKeyEnv: env[APIKeyEnv]},
			opts: []ClientOption{WithModel("code-model")},
			want: resolved{"Bearer env-key", "https://api.typesafe.ai/v1/systemone", "https://api.typesafe.ai/v1/models", "code-model", 10 * time.Second},
		},
		"success: an option is taken as given, without trimming": {
			env:  env,
			opts: []ClientOption{WithModel(" code-model ")},
			want: resolved{"Bearer env-key", "https://env.test/v1/systemone", "https://env.test/v1/models", " code-model ", 10 * time.Second},
		},
		"success: later option wins": {
			opts: []ClientOption{
				WithAPIKey("first-key"), WithAPIKey("code-key"),
				WithBaseURL("https://first.test"), WithBaseURL("https://code.test"),
				WithModel("first-model"), WithModel("code-model"),
			},
			want: resolved{"Bearer code-key", "https://code.test/v1/systemone", "https://code.test/v1/models", "code-model", 10 * time.Second},
		},
		"success: nil options are ignored": {
			opts: []ClientOption{nil, WithAPIKey("test-key"), nil},
			want: resolved{"Bearer test-key", "https://api.typesafe.ai/v1/systemone", "https://api.typesafe.ai/v1/models", "jev-latest", 10 * time.Second},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := mustResolve(t, mapEnv(tt.env), tt.opts...)
			got := resolved{c.modelsHeader.Get(headerAuthorization), c.systemOneURL.String(), c.modelsURL.String(), c.model, c.timeout}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("resolved settings mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestBaseURL pins how a base URL becomes the two endpoints (the base URL
// half of C15: trailing slashes removed, a path prefix kept) and which base
// URLs are refused when the client is built rather than at the first
// request (Appendix B, "Base URL checked at first request"). No error
// prints the URL: hunter2 stands for a credential that must never show.
func TestBaseURL(t *testing.T) {
	tests := map[string]struct {
		env           map[string]string
		baseURL       *string
		wantSystemOne string
		wantModels    string
		wantErr       string
	}{
		"success: prefix kept, trailing slashes removed": {
			baseURL:       new("https://example.test/prefix///"),
			wantSystemOne: "https://example.test/prefix/v1/systemone",
			wantModels:    "https://example.test/prefix/v1/models",
		},
		"success: root with a slash": {
			baseURL:       new("https://api.typesafe.ai/"),
			wantSystemOne: "https://api.typesafe.ai/v1/systemone",
			wantModels:    "https://api.typesafe.ai/v1/models",
		},
		"success: http with a port": {
			baseURL:       new("http://localhost:8080"),
			wantSystemOne: "http://localhost:8080/v1/systemone",
			wantModels:    "http://localhost:8080/v1/models",
		},
		"success: https with the default port written": {
			baseURL:       new("https://example.test:443"),
			wantSystemOne: "https://example.test:443/v1/systemone",
			wantModels:    "https://example.test:443/v1/models",
		},
		"success: IPv6 host with a port": {
			baseURL:       new("https://[::1]:8443/"),
			wantSystemOne: "https://[::1]:8443/v1/systemone",
			wantModels:    "https://[::1]:8443/v1/models",
		},
		"success: IPv6 host with a zone": {
			baseURL:       new("https://[fe80::1%25en0]/"),
			wantSystemOne: "https://[fe80::1%25en0]/v1/systemone",
			wantModels:    "https://[fe80::1%25en0]/v1/models",
		},
		"success: scheme in upper case": {
			baseURL:       new("HTTPS://Example.TEST/Prefix"),
			wantSystemOne: "https://Example.TEST/Prefix/v1/systemone",
			wantModels:    "https://Example.TEST/Prefix/v1/models",
		},
		"success: escaped prefix kept as written": {
			baseURL:       new("https://example.test/a%2Fb/"),
			wantSystemOne: "https://example.test/a%2Fb/v1/systemone",
			wantModels:    "https://example.test/a%2Fb/v1/models",
		},
		"success: variable trimmed": {
			env:           map[string]string{BaseURLEnv: "\t https://env.test/prefix// \n"},
			wantSystemOne: "https://env.test/prefix/v1/systemone",
			wantModels:    "https://env.test/prefix/v1/models",
		},
		"error: empty": {
			baseURL: new(""),
			wantErr: "The base URL passed to WithBaseURL must be absolute, with a scheme and a host, such as https://api.typesafe.ai.",
		},
		"error: slashes only": {
			baseURL: new("///"),
			wantErr: "The base URL passed to WithBaseURL must be absolute, with a scheme and a host, such as https://api.typesafe.ai.",
		},
		"error: no scheme": {
			baseURL: new("api.typesafe.ai"),
			wantErr: "The base URL passed to WithBaseURL must be absolute, with a scheme and a host, such as https://api.typesafe.ai.",
		},
		"error: ftp": {
			baseURL: new("ftp://example.test"),
			wantErr: "The base URL passed to WithBaseURL must use http or https.",
		},
		"error: opaque mailto": {
			baseURL: new("mailto:hunter2@example.test"),
			wantErr: "The base URL passed to WithBaseURL must use http or https.",
		},
		"error: scheme only": {
			baseURL: new("https://"),
			wantErr: "The base URL passed to WithBaseURL has an empty host.",
		},
		"error: empty host with a path": {
			baseURL: new("https:///v1"),
			wantErr: "The base URL passed to WithBaseURL has an empty host.",
		},
		"error: empty host with a port": {
			baseURL: new("https://:8443"),
			wantErr: "The base URL passed to WithBaseURL has an empty host.",
		},
		"error: opaque https": {
			baseURL: new("https:example.test"),
			wantErr: "The base URL passed to WithBaseURL has an empty host.",
		},
		"error: user and password": {
			baseURL: new("https://user:hunter2@example.test"),
			wantErr: "The base URL passed to WithBaseURL must not carry credentials; pass the API key with WithAPIKey instead.",
		},
		"error: user only": {
			baseURL: new("https://hunter2@example.test"),
			wantErr: "The base URL passed to WithBaseURL must not carry credentials; pass the API key with WithAPIKey instead.",
		},
		"error: query": {
			baseURL: new("https://example.test/?key=hunter2"),
			wantErr: "The base URL passed to WithBaseURL must not carry a query ('?...').",
		},
		"error: empty query": {
			baseURL: new("https://example.test?"),
			wantErr: "The base URL passed to WithBaseURL must not carry a query ('?...').",
		},
		"error: fragment": {
			baseURL: new("https://example.test/#hunter2"),
			wantErr: "The base URL passed to WithBaseURL must not carry a fragment ('#...').",
		},
		"error: invalid port": {
			baseURL: new("https://example.test:hunter2"),
			wantErr: "The base URL passed to WithBaseURL is not a valid URL.",
		},
		"error: space in the host": {
			baseURL: new("https://hunter2 example.test"),
			wantErr: "The base URL passed to WithBaseURL is not a valid URL.",
		},
		"error: an option is not trimmed": {
			baseURL: new(" https://example.test"),
			wantErr: "The base URL passed to WithBaseURL is not a valid URL.",
		},
		"error: variable names itself": {
			env:     map[string]string{BaseURLEnv: "ftp://hunter2.test"},
			wantErr: "The base URL in the TYPESAFE_BASE_URL environment variable must use http or https.",
		},
		"error: option wins over a valid variable": {
			env:     map[string]string{BaseURLEnv: "https://env.test"},
			baseURL: new("https://user:hunter2@example.test"),
			wantErr: "The base URL passed to WithBaseURL must not carry credentials; pass the API key with WithAPIKey instead.",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			opts := []ClientOption{WithAPIKey("test-key")}
			if tt.baseURL != nil {
				opts = append(opts, WithBaseURL(*tt.baseURL))
			}
			if tt.wantErr != "" {
				err := resolveError(t, mapEnv(tt.env), opts...)
				if got := err.Error(); got != tt.wantErr {
					t.Errorf("Error() = %q, want %q", got, tt.wantErr)
				}
				if errs := err.Unwrap(); errs != nil {
					t.Errorf("Unwrap() = %v, want nil: a url.Error prints the URL", errs)
				}
				assertNotPrinted(t, err, "hunter2")
				return
			}
			c := mustResolve(t, mapEnv(tt.env), opts...)
			if got := c.systemOneURL.String(); got != tt.wantSystemOne {
				t.Errorf("system one URL = %q, want %q", got, tt.wantSystemOne)
			}
			if got := c.modelsURL.String(); got != tt.wantModels {
				t.Errorf("models URL = %q, want %q", got, tt.wantModels)
			}
			if c.systemOneLog != tt.wantSystemOne || c.modelsLog != tt.wantModels {
				t.Errorf("log endpoints = (%q, %q), want the URLs", c.systemOneLog, c.modelsLog)
			}
		})
	}
}

// TestLogEndpointHost pins how log records name the endpoints: the URL by
// default, the API path alone under WithLogEndpointHost(false), and the
// last of several options.
func TestLogEndpointHost(t *testing.T) {
	tests := map[string]struct {
		opts                      []ClientOption
		wantSystemOne, wantModels string
	}{
		"success: default names the URL": {
			wantSystemOne: "https://example.test/prefix/v1/systemone", wantModels: "https://example.test/prefix/v1/models",
		},
		"success: false names the path": {
			opts:          []ClientOption{WithLogEndpointHost(false)},
			wantSystemOne: "/v1/systemone", wantModels: "/v1/models",
		},
		"success: later true wins": {
			opts:          []ClientOption{WithLogEndpointHost(false), WithLogEndpointHost(true)},
			wantSystemOne: "https://example.test/prefix/v1/systemone", wantModels: "https://example.test/prefix/v1/models",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := mustResolve(t, noEnv, append([]ClientOption{WithAPIKey("test-key"), WithBaseURL("https://example.test/prefix/")}, tt.opts...)...)
			if c.systemOneLog != tt.wantSystemOne || c.modelsLog != tt.wantModels {
				t.Errorf("log endpoints = (%q, %q), want (%q, %q)", c.systemOneLog, c.modelsLog, tt.wantSystemOne, tt.wantModels)
			}
			if got := c.systemOneURL.String(); got != "https://example.test/prefix/v1/systemone" {
				t.Errorf("system one URL = %q: the log setting must not change the request URL", got)
			}
		})
	}
}

// TestModel pins the default model's sources beyond the resolution table:
// an empty or blank WithModel is refused rather than sent (Appendix B,
// "Empty explicit default model sent"), and a model that is not UTF-8 is
// refused when the client is built.
func TestModel(t *testing.T) {
	const empty = "The model passed to WithModel is empty; leave WithModel out to use TYPESAFE_DEFAULT_MODEL or jev-latest."
	tests := map[string]struct {
		env     map[string]string
		opts    []ClientOption
		want    string
		wantErr string
	}{
		"success: variable trimmed":         {env: map[string]string{DefaultModelEnv: "\u3000env-model\n"}, want: "env-model"},
		"error: empty":                      {opts: []ClientOption{WithModel("")}, wantErr: empty},
		"error: blank":                      {opts: []ClientOption{WithModel(" \t\n")}, wantErr: empty},
		"error: blank by Python's rule":     {opts: []ClientOption{WithModel("\x1f\u3000")}, wantErr: empty},
		"error: empty wins over a variable": {env: map[string]string{DefaultModelEnv: "env-model"}, opts: []ClientOption{WithModel("")}, wantErr: empty},
		"error: option not UTF-8":           {opts: []ClientOption{WithModel("jev-\xff")}, wantErr: "The model passed to WithModel is not valid UTF-8."},
		"error: variable not UTF-8":         {env: map[string]string{DefaultModelEnv: "jev-\xff"}, wantErr: "The model in the TYPESAFE_DEFAULT_MODEL environment variable is not valid UTF-8."},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			opts := append([]ClientOption{WithAPIKey("test-key")}, tt.opts...)
			if tt.wantErr != "" {
				err := resolveError(t, mapEnv(tt.env), opts...)
				if got := err.Error(); got != tt.wantErr {
					t.Errorf("Error() = %q, want %q", got, tt.wantErr)
				}
				return
			}
			if got := mustResolve(t, mapEnv(tt.env), opts...).model; got != tt.want {
				t.Errorf("model = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestMaxResponseBytes pins the response limit's default and its bounds:
// at least 1 byte, at most 1 GiB.
func TestMaxResponseBytes(t *testing.T) {
	tests := map[string]struct {
		opts    []ClientOption
		want    int64
		wantErr string
	}{
		"success: default is 16 MiB": {want: 16 << 20},
		"success: one byte":          {opts: []ClientOption{WithMaxResponseBytes(1)}, want: 1},
		"success: 1 GiB":             {opts: []ClientOption{WithMaxResponseBytes(1 << 30)}, want: 1 << 30},
		"success: later wins":        {opts: []ClientOption{WithMaxResponseBytes(0), WithMaxResponseBytes(4096)}, want: 4096},
		"error: zero": {
			opts:    []ClientOption{WithMaxResponseBytes(0)},
			wantErr: "The limit passed to WithMaxResponseBytes must be at least 1: every response carries a body.",
		},
		"error: negative": {
			opts:    []ClientOption{WithMaxResponseBytes(-1)},
			wantErr: "The limit passed to WithMaxResponseBytes must be at least 1: every response carries a body.",
		},
		"error: past 1 GiB": {
			opts:    []ClientOption{WithMaxResponseBytes(1<<30 + 1)},
			wantErr: "The limit passed to WithMaxResponseBytes must be at most 1 GiB (1073741824 bytes).",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			opts := append([]ClientOption{WithAPIKey("test-key")}, tt.opts...)
			if tt.wantErr != "" {
				if got := resolveError(t, noEnv, opts...).Error(); got != tt.wantErr {
					t.Errorf("Error() = %q, want %q", got, tt.wantErr)
				}
				return
			}
			if got := mustResolve(t, noEnv, opts...).maxResponseBytes; got != tt.want {
				t.Errorf("maxResponseBytes = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestLogger pins the logger setting: records are discarded unless a
// logger is given, and a nil logger means the default.
func TestLogger(t *testing.T) {
	logger := slog.New(testsupport.NewLogRecorder(nil).Handler())
	tests := map[string]struct {
		opts        []ClientOption
		want        *slog.Logger
		wantDiscard bool
	}{
		"success: default discards":     {wantDiscard: true},
		"success: nil discards":         {opts: []ClientOption{WithLogger(nil)}, wantDiscard: true},
		"success: given logger is used": {opts: []ClientOption{WithLogger(logger)}, want: logger},
		"success: later nil wins":       {opts: []ClientOption{WithLogger(logger), WithLogger(nil)}, wantDiscard: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := mustResolve(t, noEnv, append([]ClientOption{WithAPIKey("test-key")}, tt.opts...)...)
			if c.logger == nil {
				t.Fatal("logger is nil")
			}
			if tt.wantDiscard {
				if h := c.logger.Handler(); h != slog.DiscardHandler {
					t.Errorf("handler = %T, want slog.DiscardHandler", h)
				}
				return
			}
			if c.logger != tt.want {
				t.Errorf("logger = %p, want %p", c.logger, tt.want)
			}
		})
	}
}

// TestHeaderTemplate pins the headers every request carries, built once
// when the client is built: the caller's headers under canonical names,
// without the ones the SDK sets (dropped as py:_core/transport.py:116-127
// overwrites them; test_clients.py:383-409 asserts the same protected set),
// without X-TypeSafe-Retry-Count (py:_core/transport.py:118) and without the
// framing headers (Appendix B, "Caller framing headers sent"); then the
// SDK's own. POST /v1/systemone adds Content-Type; GET /v1/models has none.
// The client half of C15 (the request on the wire) is the client's test.
func TestHeaderTemplate(t *testing.T) {
	sdk := "typesafe-sdk-go/" + Version
	base := func(extra map[string]string) http.Header {
		h := http.Header{
			"Authorization":      {"Bearer test-key"},
			"Accept":             {"application/json"},
			"User-Agent":         {sdk},
			"X-Typesafe-Sdk":     {sdk},
			"X-Typesafe-Runtime": {runtimeIdentifier},
		}
		for k, v := range extra {
			if v == "" {
				delete(h, k)
				continue
			}
			h[k] = []string{v}
		}
		return h
	}
	protected := []ClientOption{
		WithHeader("authorization", "injected-secret"),
		WithHeader("accept", "text/plain"),
		WithHeader("user-agent", "wrong-agent"),
		WithHeader("x-typesafe-sdk", "wrong-sdk"),
		WithHeader("x-typesafe-runtime", "wrong-runtime"),
		WithHeader("content-type", "wrong/type"),
		WithHeader("x-typesafe-retry-count", "99"),
	}
	framing := []ClientOption{
		WithHeader("content-length", "5"),
		WithHeader("transfer-encoding", "chunked"),
		WithHeader("connection", "close"),
		WithHeader("proxy-connection", "keep-alive"),
		WithHeader("keep-alive", "timeout=5"),
		WithHeader("upgrade", "h2c"),
		WithHeader("te", "trailers"),
		WithHeader("trailer", "X-Checksum"),
		WithHeader("host", "evil.test"),
	}
	tests := map[string]struct {
		opts []ClientOption
		want http.Header // the models template; the system one template adds Content-Type
	}{
		"success: SDK headers only": {
			want: base(nil),
		},
		"success: protected headers dropped, caller headers kept": {
			opts: append(slices.Clone(protected), WithHeader("X-Team", "default"), WithHeader("X-Default", "kept"), WithHeader("x-team", "call")),
			want: base(map[string]string{"X-Team": "call", "X-Default": "kept"}),
		},
		"success: framing headers dropped": {
			opts: append(slices.Clone(framing), WithHeader("X-Kept", "yes")),
			want: base(map[string]string{"X-Kept": "yes"}),
		},
		"success: names canonicalised, later name wins": {
			opts: []ClientOption{
				WithHeader("x-mIxEd-CASE", "first"), WithHeader("X-MIXED-case", "second"),
				WithHeader("x_under_score", "u"), WithHeader("X-Empty", ""),
				WithHeader("X-Tab", "a\tb"), WithHeader("X-Obs-Text", "caf\xc3\xa9"),
			},
			want: func() http.Header {
				h := base(map[string]string{"X-Mixed-Case": "second", "X_under_score": "u", "X-Tab": "a\tb", "X-Obs-Text": "caf\xc3\xa9"})
				h["X-Empty"] = []string{""}
				return h
			}(),
		},
		"success: User-Agent product in front": {
			opts: []ClientOption{WithUserAgentProduct("my-app/1.2.0"), WithHeader("User-Agent", "wrong-agent")},
			want: base(map[string]string{"User-Agent": "my-app/1.2.0 " + sdk}),
		},
		"success: later product wins": {
			opts: []ClientOption{WithUserAgentProduct("first/1"), WithUserAgentProduct("second/2")},
			want: base(map[string]string{"User-Agent": "second/2 " + sdk}),
		},
		"success: runtime header off, caller's dropped all the same": {
			opts: []ClientOption{WithRuntimeHeader(false), WithHeader("X-TypeSafe-Runtime", "wrong-runtime")},
			want: base(map[string]string{"X-Typesafe-Runtime": ""}),
		},
		"success: runtime header off, then on": {
			opts: []ClientOption{WithRuntimeHeader(false), WithRuntimeHeader(true)},
			want: base(nil),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := mustResolve(t, noEnv, append([]ClientOption{WithAPIKey("test-key")}, tt.opts...)...)
			if diff := gocmp.Diff(tt.want, c.modelsHeader); diff != "" {
				t.Errorf("models template mismatch (-want +got):\n%s", diff)
			}
			wantPost := tt.want.Clone()
			wantPost["Content-Type"] = []string{"application/json"}
			if diff := gocmp.Diff(wantPost, c.systemOneHeader); diff != "" {
				t.Errorf("system one template mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestHeaderDropsLogged pins the debug record for each dropped caller
// header: the canonical name and the reason, never the value, and nothing
// for a header that is kept.
func TestHeaderDropsLogged(t *testing.T) {
	rec := testsupport.NewLogRecorder(nil)
	mustResolve(t, noEnv,
		WithAPIKey("test-key"), WithLogger(rec.Logger()),
		WithHeader("authorization", "injected-secret"),
		WithHeader("x-typesafe-retry-count", "99"),
		WithHeader("Host", "evil.test"),
		WithHeader("content-type", "wrong/type"),
		WithHeader("X-Team", "kept-value"),
	)
	var got []string
	for _, r := range rec.Records() {
		got = append(got, r.String())
		for _, secret := range []string{"injected-secret", "99", "evil.test", "wrong/type", "kept-value", "test-key"} {
			if strings.Contains(r.String(), secret) {
				t.Errorf("record %q contains the value %q", r.String(), secret)
			}
		}
	}
	want := []string{
		"DEBUG config: header dropped header=Authorization reason=set by the SDK",
		"DEBUG config: header dropped header=X-Typesafe-Retry-Count reason=set by the SDK on retries only",
		"DEBUG config: header dropped header=Host reason=belongs to the transport",
		"DEBUG config: header dropped header=Content-Type reason=set by the SDK",
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("records mismatch (-want +got):\n%s", diff)
	}
}

// TestInvalidHeader pins the refusal of a header that HTTP cannot carry: an
// invalid name is reported by the position of its WithHeader call, since
// it may be anything, a pasted "Authorization: Bearer ..." line included;
// an invalid value by its header's name. Neither is ever printed.
func TestInvalidHeader(t *testing.T) {
	const secret = "ts_live_private"
	tests := map[string]struct {
		opts []ClientOption
		want string
	}{
		"error: empty name": {
			opts: []ClientOption{WithHeader("", "v")},
			want: "The name given to WithHeader call 1 is not a valid HTTP field name (RFC 9110, section 5.6.2); it is not shown, since it may hold a credential.",
		},
		"error: pasted header line as the name": {
			opts: []ClientOption{WithHeader("X-Ok", "v"), WithHeader("Authorization: Bearer "+secret, "")},
			want: "The name given to WithHeader call 2 is not a valid HTTP field name (RFC 9110, section 5.6.2); it is not shown, since it may hold a credential.",
		},
		"error: space in the name": {
			opts: []ClientOption{WithHeader("X "+secret, "v")},
			want: "The name given to WithHeader call 1 is not a valid HTTP field name (RFC 9110, section 5.6.2); it is not shown, since it may hold a credential.",
		},
		"error: non-ASCII name": {
			opts: []ClientOption{WithHeader("X-na\u00efve-"+secret, "v")},
			want: "The name given to WithHeader call 1 is not a valid HTTP field name (RFC 9110, section 5.6.2); it is not shown, since it may hold a credential.",
		},
		"error: CRLF in the value": {
			opts: []ClientOption{WithHeader("x-ok", "v\r\nX-Injected: "+secret)},
			want: "The value given to WithHeader for X-Ok is not a valid HTTP field value (RFC 9110, section 5.5).",
		},
		"error: NUL in the value": {
			opts: []ClientOption{WithHeader("X-Ok", secret+"\x00")},
			want: "The value given to WithHeader for X-Ok is not a valid HTTP field value (RFC 9110, section 5.5).",
		},
		"error: DEL in the value": {
			opts: []ClientOption{WithHeader("X-Ok", secret+"\x7f")},
			want: "The value given to WithHeader for X-Ok is not a valid HTTP field value (RFC 9110, section 5.5).",
		},
		"error: invalid value of a header the SDK would drop": {
			opts: []ClientOption{WithHeader("Authorization", "Bearer "+secret+"\n")},
			want: "The value given to WithHeader for Authorization is not a valid HTTP field value (RFC 9110, section 5.5).",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := resolveError(t, noEnv, append([]ClientOption{WithAPIKey("test-key")}, tt.opts...)...)
			if got := err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
			assertNotPrinted(t, err, secret)
		})
	}
}

// TestUserAgentProductRules pins which products WithUserAgentProduct
// accepts: name/version, both tokens, at most 64 bytes. The error names the
// rule and never prints the product.
func TestUserAgentProductRules(t *testing.T) {
	const prefix = "The product passed to WithUserAgentProduct must be a product token, name/version (RFC 9110, section 10.1.5): "
	tests := map[string]struct {
		product string
		want    string // the rule, "" when accepted
	}{
		"success: name and version":     {product: "my-app/1.2.0"},
		"success: one character each":   {product: "a/b"},
		"success: every tchar":          {product: "!#$%&'*+-.^_`|~09AZaz/!#$%&'*+-.^_`|~09AZaz"},
		"success: 64 bytes":             {product: strings.Repeat("a", 31) + "/" + strings.Repeat("b", 32)},
		"error: empty":                  {product: "", want: "it is empty"},
		"error: 65 bytes":               {product: strings.Repeat("a", 32) + "/" + strings.Repeat("b", 32), want: "it is longer than 64 bytes"},
		"error: not ASCII":              {product: "caf\u00e9/1", want: "it contains a character that is not ASCII"},
		"error: space":                  {product: "my app/1", want: "it contains whitespace"},
		"error: newline before a slash": {product: "my-app\n", want: "it contains whitespace"},
		"error: control":                {product: "my-app/1\x00", want: "it contains a control character"},
		"error: DEL":                    {product: "my-app/1\x7f", want: "it contains a control character"},
		"error: no version":             {product: "my-app", want: "it has no '/' between the name and the version"},
		"error: two slashes":            {product: "a/b/c", want: "it has more than one '/'"},
		"error: empty name":             {product: "/1", want: "the name before the '/' is empty"},
		"error: empty version":          {product: "a/", want: "the version after the '/' is empty"},
		"error: comment":                {product: "(my-app)/1", want: "it contains a character a token cannot hold (RFC 9110, section 5.6.2)"},
		"error: separator":              {product: "my-app/1;x", want: "it contains a character a token cannot hold (RFC 9110, section 5.6.2)"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			opts := []ClientOption{WithAPIKey("test-key"), WithUserAgentProduct(tt.product)}
			if tt.want == "" {
				c := mustResolve(t, noEnv, opts...)
				if got, want := c.modelsHeader.Get(headerUserAgent), tt.product+" typesafe-sdk-go/"+Version; got != want {
					t.Errorf("User-Agent = %q, want %q", got, want)
				}
				return
			}
			err := resolveError(t, noEnv, opts...)
			if got, want := err.Error(), prefix+tt.want+"."; got != want {
				t.Errorf("Error() = %q, want %q", got, want)
			}
			if tt.product != "" {
				assertNotPrinted(t, err, tt.product)
			}
		})
	}
}

// TestPythonSpace pins isPythonSpace to the characters Python's
// str.isspace() accepts, which str.strip() trims: all 29 of them, probed on
// the upstream virtual environment (Python 3.14.6, 2026-09-26 00:14:48 JST)
// by listing every code point c with chr(c).isspace(). The check runs over
// every rune, so a character either side of the set is covered too.
func TestPythonSpace(t *testing.T) {
	python := map[rune]bool{}
	for _, r := range []rune{
		0x9, 0xa, 0xb, 0xc, 0xd, 0x1c, 0x1d, 0x1e, 0x1f, 0x20, 0x85, 0xa0, 0x1680,
		0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a,
		0x2028, 0x2029, 0x202f, 0x205f, 0x3000,
	} {
		python[r] = true
	}
	var mismatches []string
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if got := isPythonSpace(r); got != python[r] {
			mismatches = append(mismatches, fmt.Sprintf("U+%04X: got %t, want %t", r, got, python[r]))
		}
	}
	if len(mismatches) > 0 {
		t.Errorf("isPythonSpace disagrees with Python's str.isspace() on %d code points:\n%s", len(mismatches), strings.Join(mismatches, "\n"))
	}
}

// sinkHeader keeps BenchmarkHeaderTemplateClone's result alive.
var sinkHeader http.Header

// BenchmarkHeaderTemplateClone measures the per-attempt cost of the header
// template: one http.Header.Clone of the POST template, which is what a
// request copies before it adds its own headers.
func BenchmarkHeaderTemplateClone(b *testing.B) {
	tests := map[string][]ClientOption{
		"sdk-only":       nil,
		"three-defaults": {WithHeader("X-Team", "billing"), WithHeader("X-Trace", "on"), WithHeader("X-Region", "ap-northeast-1")},
		"no-runtime-hdr": {WithRuntimeHeader(false)},
	}
	for name, extra := range tests {
		b.Run(name, func(b *testing.B) {
			c := mustResolveB(b, append([]ClientOption{WithAPIKey("test-key")}, extra...)...)
			b.ReportAllocs()
			for b.Loop() {
				sinkHeader = c.systemOneHeader.Clone()
			}
		})
	}
}

// mustResolveB resolves opts with no environment, failing the benchmark
// when that fails.
func mustResolveB(b *testing.B, opts ...ClientOption) *config {
	b.Helper()
	c, err := resolveConfig(noEnv, opts...)
	if err != nil {
		b.Fatalf("resolve: %v", err)
	}
	return c
}
