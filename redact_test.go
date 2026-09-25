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
	"context"
	"fmt"
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
					logger.Debug("request", slog.Any("headers", newRedactedHeaders(c.systemOneHeader, c.apiKey)))
					logger.Debug("response", slog.Any("headers", newRedactedHeaders(response, c.apiKey)))
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
			rec.Logger().Debug("h", slog.Any("headers", newRedactedHeaders(tt.header, tt.apiKey)))
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

// printedKey is the API key TestRedactedHeadersNeverPrintKey must never see
// printed.
const printedKey = "ts_live_zzsecret"

// rawValueHandler is a slog handler that prints each attribute's value with
// %v and never resolves it, as a hand-written handler might.
type rawValueHandler struct{ buf *bytes.Buffer }

func (h rawValueHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h rawValueHandler) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(h.buf, "%s=%v;", a.Key, a.Value)
		return true
	})
	return nil
}

func (h rawValueHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h rawValueHandler) WithGroup(string) slog.Handler { return h }

// TestRedactedHeadersNeverPrintKey pins that no rendering of a
// redactedHeaders prints the API key (review W2.1 MINOR 1, ruling R66): every
// fmt verb and an unresolved slog Value, Attr or handler print the redacted
// form, while %p and a redactedHeaders held in an unexported field, the two
// renderings fmt makes without calling Format, print an address. The key sits
// under Authorization in one map and under a plain name in the other.
func TestRedactedHeadersNeverPrintKey(t *testing.T) {
	type holder struct{ h redactedHeaders }
	headers := map[string]struct {
		header http.Header
		want   string // the redacted form
	}{
		"key under Authorization": {
			header: http.Header{"Authorization": {"Bearer " + printedKey}, "X-Visible": {"visible"}},
			want:   "[Authorization=*** X-Visible=visible]",
		},
		"key under a plain name": {
			header: http.Header{"X-Forward": {printedKey}, "X-Visible": {"visible"}},
			want:   "[X-Forward=*** X-Visible=visible]",
		},
	}
	// Each rendering returns what it printed and what it must print: want is
	// the redacted form, and an empty result from wrap means "an address, not
	// the fields" (checked below).
	renderings := map[string]struct {
		render func(redactedHeaders) string
		wrap   func(want string) string
	}{
		"%v":          {render: func(r redactedHeaders) string { return fmt.Sprintf("%v", r) }, wrap: same},
		"%+v":         {render: func(r redactedHeaders) string { return fmt.Sprintf("%+v", r) }, wrap: same},
		"%#v":         {render: func(r redactedHeaders) string { return fmt.Sprintf("%#v", r) }, wrap: same},
		"%s":          {render: func(r redactedHeaders) string { return fmt.Sprintf("%s", r) }, wrap: same},
		"%d":          {render: func(r redactedHeaders) string { return fmt.Sprintf("%d", r) }, wrap: same},
		"%x":          {render: func(r redactedHeaders) string { return fmt.Sprintf("%x", r) }, wrap: same},
		"%q":          {render: func(r redactedHeaders) string { return fmt.Sprintf("%q", r) }, wrap: same},
		"%-60.3v":     {render: func(r redactedHeaders) string { return fmt.Sprintf("%-60.3v", r) }, wrap: same},
		"Sprint":      {render: func(r redactedHeaders) string { return fmt.Sprint(r) }, wrap: same},
		"in a slice":  {render: func(r redactedHeaders) string { return fmt.Sprintf("%v", []any{r}) }, wrap: func(w string) string { return "[" + w + "]" }},
		"AnyValue":    {render: func(r redactedHeaders) string { return slog.AnyValue(r).String() }, wrap: same},
		"Any":         {render: func(r redactedHeaders) string { return slog.Any("headers", r).String() }, wrap: func(w string) string { return "headers=" + w }},
		"Resolve":     {render: func(r redactedHeaders) string { return slog.AnyValue(r).Resolve().String() }, wrap: same},
		"raw handler": {render: rawHandlerOutput, wrap: func(w string) string { return "headers=" + w + ";" }},
		"%p":          {render: func(r redactedHeaders) string { return fmt.Sprintf("%p", r) }, wrap: address},
		"unexported field %+v": {
			render: func(r redactedHeaders) string { return fmt.Sprintf("%+v", holder{h: r}) },
			wrap:   address,
		},
		"unexported field %#v": {
			render: func(r redactedHeaders) string { return fmt.Sprintf("%#v", holder{h: r}) },
			wrap:   address,
		},
	}
	for headerName, hc := range headers {
		for renderName, rc := range renderings {
			t.Run(headerName+" "+renderName, func(t *testing.T) {
				got := rc.render(newRedactedHeaders(hc.header, printedKey))
				if strings.Contains(got, printedKey) {
					t.Fatalf("output %q contains the key", got)
				}
				want := rc.wrap(hc.want)
				if want == "" {
					if !strings.Contains(got, "0x") || strings.Contains(got, "map[") || strings.Contains(got, "visible") {
						t.Errorf("output %q is not an address", got)
					}
					return
				}
				if got != want {
					t.Errorf("output = %q, want %q", got, want)
				}
			})
		}
	}
	if got := fmt.Sprintf("%v", redactedHeaders{}); got != "[]" {
		t.Errorf("zero value prints %q, want %q", got, "[]")
	}
}

// same returns the redacted form unchanged.
func same(want string) string { return want }

// address marks a rendering that must print an address rather than the
// fields.
func address(string) string { return "" }

// rawHandlerOutput logs r through a rawValueHandler and returns what it
// wrote.
func rawHandlerOutput(r redactedHeaders) string {
	var buf bytes.Buffer
	slog.New(rawValueHandler{buf: &buf}).Debug("request", slog.Any("headers", r))
	return buf.String()
}

// TestAPIKeyNeedleThreshold pins ruling R68: the key is looked for inside a
// WithHeader name and inside a header value only when it is at least
// minKeyNeedleBytes (8) long, so a test's short dummy key neither refuses an
// ordinary name nor hides an ordinary value, while Authorization is redacted
// by its name whatever the key.
func TestAPIKeyNeedleThreshold(t *testing.T) {
	tests := map[string]struct {
		key         string
		name        string // a WithHeader name that contains the key
		wantRefused bool
		wantEcho    string // how X-Echo, whose value holds the key, prints
	}{
		"success: 1-byte key is not looked for": {
			key: "k", name: "X-K", wantEcho: "id=k",
		},
		"success: 7-byte key is not looked for": {
			key: "test-id", name: "X-Test-Id", wantEcho: "id=test-id",
		},
		"success: 8-byte key is looked for": {
			key: "trace-id", name: "X-Trace-Id", wantRefused: true, wantEcho: "***",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if len(tt.key) >= minKeyNeedleBytes != tt.wantRefused {
				t.Fatalf("row %q: a %d-byte key contradicts minKeyNeedleBytes = %d", name, len(tt.key), minKeyNeedleBytes)
			}
			opts := []ClientOption{WithAPIKey(tt.key), WithHeader(tt.name, "v")}
			if tt.wantRefused {
				err := resolveError(t, noEnv, opts...)
				if want := "The name given to WithHeader call 1 contains the API key, so it is not shown; pass the key with WithAPIKey only."; err.Error() != want {
					t.Errorf("Error() = %q, want %q", err.Error(), want)
				}
			} else {
				c := mustResolve(t, noEnv, opts...)
				if got := c.modelsHeader.Get(tt.name); got != "v" {
					t.Errorf("template %s = %q, want %q", tt.name, got, "v")
				}
			}

			header := http.Header{"Authorization": {"Bearer " + tt.key}, "X-Echo": {"id=" + tt.key}}
			want := "[Authorization=*** X-Echo=" + tt.wantEcho + "]"
			if got := fmt.Sprint(newRedactedHeaders(header, tt.key)); got != want {
				t.Errorf("rendered = %q, want %q", got, want)
			}
		})
	}
}
