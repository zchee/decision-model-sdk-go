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
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// secretSpellings are the nine header spellings test_secret_headers_redacted
// runs (pytest:test_logging.py:17-27): the six names of SECRET_HEADERS in
// several cases, and names that only contain "token" or "secret".
var secretSpellings = []string{
	"Authorization",
	"Proxy-Authorization",
	"X-API-Key",
	"API-Key",
	"Cookie",
	"Set-Cookie",
	"X-Access-Token",
	"X-Client-Secret",
	"x-MiXeD-ToKeN",
}

// TestIsSecretHeader pins the by-name rule (py:_core/logging.py:32-34): one
// of six names, or any name containing "token" or "secret", without regard
// to case. Names that merely resemble a secret one are not secret.
func TestIsSecretHeader(t *testing.T) {
	tests := map[string]struct {
		name string
		want bool
	}{
		"success: token inside a word":   {name: "X-Tokenizer", want: true},
		"success: secret inside a word":  {name: "x-secretive", want: true},
		"success: upper case":            {name: "SET-COOKIE", want: true},
		"success: canonical form":        {name: "X-Api-Key", want: true},
		"success: not secret: Accept":    {name: "Accept", want: false},
		"success: not secret: SDK":       {name: "X-Typesafe-Sdk", want: false},
		"success: not secret: X-Api":     {name: "X-Api", want: false},
		"success: not secret: X-Key":     {name: "X-Key", want: false},
		"success: not secret: cookie2":   {name: "Cookie2", want: false},
		"success: not secret: X-Visible": {name: "X-Visible", want: false},
	}
	for _, spelling := range secretSpellings {
		tests["success: upstream spelling "+spelling] = struct {
			name string
			want bool
		}{name: spelling, want: true}
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := isSecretHeader(tt.name); got != tt.want {
				t.Errorf("isSecretHeader(%q) = %t, want %t", tt.name, got, tt.want)
			}
		})
	}
}

// TestRedactedHeadersSecretSpellings ports the header part of
// test_secret_headers_redacted (L1) as far as it applies to the client's
// configuration: for each of the nine spellings, a header of that name set
// with WithHeader and a response header of that name are printed as "***",
// the API key never shows, and a header that is not a credential shows as
// it is. The record goes through the three handlers a caller is likely to
// use; each resolves the LogValuer.
func TestRedactedHeadersSecretSpellings(t *testing.T) {
	for _, spelling := range secretSpellings {
		t.Run(spelling, func(t *testing.T) {
			c := mustResolve(t, noEnv,
				WithAPIKey("auth-credential"),
				WithHeader(spelling, "request-credential"),
				WithHeader("x-visible", "request-visible"),
			)
			response := http.Header{}
			response.Set(spelling, "response-credential")
			response.Set("X-Visible", "response-visible")

			for handlerName, render := range renderers() {
				out := render(func(logger *slog.Logger) {
					logger.Debug("request", slog.Any("headers", redactedHeaders{header: c.systemOneHeader, apiKey: c.apiKey}))
					logger.Debug("response", slog.Any("headers", redactedHeaders{header: response, apiKey: c.apiKey}))
				})
				for _, visible := range []string{"request-visible", "response-visible", "***"} {
					if !strings.Contains(out, visible) {
						t.Errorf("%s output does not contain %q:\n%s", handlerName, visible, out)
					}
				}
				for _, secret := range []string{"auth-credential", "request-credential", "response-credential"} {
					if strings.Contains(out, secret) {
						t.Errorf("%s output contains %q:\n%s", handlerName, secret, out)
					}
				}
			}
		})
	}
}

// renderers returns, by name, functions that run a logging function against
// a handler at debug level and return what it wrote.
func renderers() map[string]func(log func(*slog.Logger)) string {
	opts := &slog.HandlerOptions{Level: slog.LevelDebug}
	return map[string]func(log func(*slog.Logger)) string{
		"TextHandler": func(log func(*slog.Logger)) string {
			var buf bytes.Buffer
			log(slog.New(slog.NewTextHandler(&buf, opts)))
			return buf.String()
		},
		"JSONHandler": func(log func(*slog.Logger)) string {
			var buf bytes.Buffer
			log(slog.New(slog.NewJSONHandler(&buf, opts)))
			return buf.String()
		},
		"LogRecorder": func(log func(*slog.Logger)) string {
			rec := testsupport.NewLogRecorder(nil)
			log(rec.Logger())
			var sb strings.Builder
			for _, r := range rec.Records() {
				sb.WriteString(r.String())
				sb.WriteByte('\n')
			}
			return sb.String()
		},
	}
}

// flaggedKey is the API key TestRedactedHeadersFlaggedValue looks for.
const flaggedKey = "auth-credential"

// TestRedactedHeadersFlaggedValue pins the second rule, which goes past
// upstream's by-name redaction (Appendix B, "Redaction by header name"):
// a value holding the API key is a credential under any name. The exact
// rendering is pinned too: one attribute per header in name order, values
// joined by ", ".
func TestRedactedHeadersFlaggedValue(t *testing.T) {
	tests := map[string]struct {
		header http.Header
		apiKey string
		want   string
	}{
		"success: key under a plain name": {
			header: http.Header{"X-Forwarded-Key": {flaggedKey}, "X-Visible": {"visible"}},
			apiKey: flaggedKey,
			want:   "DEBUG h headers.X-Forwarded-Key=*** headers.X-Visible=visible",
		},
		"success: key inside a value": {
			header: http.Header{"X-Echo": {"Bearer " + flaggedKey}, "Accept": {"application/json"}},
			apiKey: flaggedKey,
			want:   "DEBUG h headers.Accept=application/json headers.X-Echo=***",
		},
		"success: key in the second of several values": {
			header: http.Header{"X-Multi": {"first", flaggedKey}},
			apiKey: flaggedKey,
			want:   "DEBUG h headers.X-Multi=***",
		},
		"success: several values joined": {
			header: http.Header{"X-Multi": {"first", "second"}, "Cookie": {"a=1", "b=2"}},
			apiKey: flaggedKey,
			want:   "DEBUG h headers.Cookie=*** headers.X-Multi=first, second",
		},
		"success: no key redacts by name only": {
			header: http.Header{"X-Visible": {"visible"}, "Authorization": {"Bearer x"}},
			apiKey: "",
			want:   "DEBUG h headers.Authorization=*** headers.X-Visible=visible",
		},
		"success: empty map": {
			header: http.Header{},
			apiKey: flaggedKey,
			want:   "DEBUG h",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := testsupport.NewLogRecorder(nil)
			rec.Logger().Debug("h", slog.Any("headers", redactedHeaders{header: tt.header, apiKey: tt.apiKey}))
			var got []string
			for _, r := range rec.Records() {
				got = append(got, r.String())
			}
			if diff := gocmp.Diff([]string{tt.want}, got); diff != "" {
				t.Errorf("record mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
