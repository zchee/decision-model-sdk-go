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

package engine

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
	"github.com/zchee/decision-model-sdk-go/internal/wire"
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
			if got := IsSecretHeader(tt.name); got != tt.want {
				t.Errorf("IsSecretHeader(%q) = %t, want %t", tt.name, got, tt.want)
			}
		})
	}
}

// flaggedKey is the API key TestRedactedHeadersFlaggedValue looks for.
const flaggedKey = "auth-credential"

// TestRedactedHeadersFlaggedValue pins the second rule, which goes past
// upstream's by-name redaction (docs/deviations.md, "redaction by source"):
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
			rec.Logger().Debug("h", slog.Any("headers", NewRedactedHeaders(tt.header, NewHeaderRedactor(tt.apiKey))))
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
// RedactedHeaders prints the API key: every fmt verb and an unresolved slog
// Value, Attr or handler print the redacted form, while %p and a
// RedactedHeaders held in an unexported field, the two renderings fmt makes
// without calling Format, print an address. The key sits under Authorization
// in one map and under a plain name in the other.
func TestRedactedHeadersNeverPrintKey(t *testing.T) {
	type holder struct{ h RedactedHeaders }
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
		render func(RedactedHeaders) string
		wrap   func(want string) string
	}{
		"%v":          {render: func(r RedactedHeaders) string { return fmt.Sprintf("%v", r) }, wrap: same},
		"%+v":         {render: func(r RedactedHeaders) string { return fmt.Sprintf("%+v", r) }, wrap: same},
		"%#v":         {render: func(r RedactedHeaders) string { return fmt.Sprintf("%#v", r) }, wrap: same},
		"%s":          {render: func(r RedactedHeaders) string { return fmt.Sprintf("%s", r) }, wrap: same},
		"%d":          {render: func(r RedactedHeaders) string { return fmt.Sprintf("%d", r) }, wrap: same},
		"%x":          {render: func(r RedactedHeaders) string { return fmt.Sprintf("%x", r) }, wrap: same},
		"%q":          {render: func(r RedactedHeaders) string { return fmt.Sprintf("%q", r) }, wrap: same},
		"%-60.3v":     {render: func(r RedactedHeaders) string { return fmt.Sprintf("%-60.3v", r) }, wrap: same},
		"Sprint":      {render: func(r RedactedHeaders) string { return fmt.Sprint(r) }, wrap: same},
		"in a slice":  {render: func(r RedactedHeaders) string { return fmt.Sprintf("%v", []any{r}) }, wrap: func(w string) string { return "[" + w + "]" }},
		"AnyValue":    {render: func(r RedactedHeaders) string { return slog.AnyValue(r).String() }, wrap: same},
		"Any":         {render: func(r RedactedHeaders) string { return slog.Any("headers", r).String() }, wrap: func(w string) string { return "headers=" + w }},
		"Resolve":     {render: func(r RedactedHeaders) string { return slog.AnyValue(r).Resolve().String() }, wrap: same},
		"raw handler": {render: rawHandlerOutput, wrap: func(w string) string { return "headers=" + w + ";" }},
		"%p":          {render: func(r RedactedHeaders) string { return fmt.Sprintf("%p", r) }, wrap: address},
		"unexported field %+v": {
			render: func(r RedactedHeaders) string { return fmt.Sprintf("%+v", holder{h: r}) },
			wrap:   address,
		},
		"unexported field %#v": {
			render: func(r RedactedHeaders) string { return fmt.Sprintf("%#v", holder{h: r}) },
			wrap:   address,
		},
	}
	for headerName, hc := range headers {
		for renderName, rc := range renderings {
			t.Run(headerName+" "+renderName, func(t *testing.T) {
				got := rc.render(NewRedactedHeaders(hc.header, NewHeaderRedactor(printedKey)))
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
	if got := fmt.Sprintf("%v", RedactedHeaders{}); got != "[]" {
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
func rawHandlerOutput(r RedactedHeaders) string {
	var buf bytes.Buffer
	slog.New(rawValueHandler{buf: &buf}).Debug("request", slog.Any("headers", r))
	return buf.String()
}

// TestRedactHeader pins the copy of a response header that the error types
// store: each value of a header that is a credential by its name (each of the
// table's nine spellings) or, for a client whose key is at least 8 bytes
// long, by holding the key, becomes "***", one per value; every other header
// keeps its value slice, shared with the response's; the response's map is
// left as it was; nil stays nil. The zero HeaderRedactor, and a client's
// whose key is shorter, redact by name alone.
func TestRedactHeader(t *testing.T) {
	const key = "ts_live_0123456789abcdef"
	tests := map[string]struct {
		r    HeaderRedactor
		h    http.Header
		want http.Header
	}{
		"success: nil stays nil": {r: NewHeaderRedactor(key)},
		"success: an empty header": {
			r: NewHeaderRedactor(key), h: http.Header{}, want: http.Header{},
		},
		"success: every name-marked spelling, by the zero value too": {
			h: http.Header{
				"Authorization": {"Bearer a"}, "Proxy-Authorization": {"Basic b"}, "X-Api-Key": {"c"}, "Api-Key": {"d"},
				"Cookie": {"e"}, "Set-Cookie": {"f=1", "g=2"}, "X-Access-Token": {"h"}, "X-Client-Secret": {"i"}, "x-MiXeD-ToKeN": {"j"},
			},
			want: http.Header{
				"Authorization": {Redacted}, "Proxy-Authorization": {Redacted}, "X-Api-Key": {Redacted}, "Api-Key": {Redacted},
				"Cookie": {Redacted}, "Set-Cookie": {Redacted, Redacted}, "X-Access-Token": {Redacted}, "X-Client-Secret": {Redacted}, "x-MiXeD-ToKeN": {Redacted},
			},
		},
		"success: the headers the error's methods read stay as they are": {
			r:    NewHeaderRedactor(key),
			h:    http.Header{"Retry-After": {"2"}, "Retry-After-Ms": {"125"}, "X-Typesafe-Request-Id": {"req_123"}, "Content-Type": {"application/json"}},
			want: http.Header{"Retry-After": {"2"}, "Retry-After-Ms": {"125"}, "X-Typesafe-Request-Id": {"req_123"}, "Content-Type": {"application/json"}},
		},
		"success: a value that holds a key of 8 bytes or more, under any name": {
			r:    NewHeaderRedactor(key),
			h:    http.Header{"X-Echo": {"ok", "Bearer " + key}, "X-Other": {"visible"}},
			want: http.Header{"X-Echo": {Redacted, Redacted}, "X-Other": {"visible"}},
		},
		"success: the zero value does not look for a key": {
			h:    http.Header{"X-Echo": {"Bearer " + key}},
			want: http.Header{"X-Echo": {"Bearer " + key}},
		},
		"success: a key of 7 bytes is not looked for (R68)": {
			r:    NewHeaderRedactor("k123456"),
			h:    http.Header{"X-Echo": {"Bearer k123456"}, "Cookie": {"k123456"}},
			want: http.Header{"X-Echo": {"Bearer k123456"}, "Cookie": {Redacted}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			before := tt.h.Clone()
			got := tt.r.Header(tt.h)
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("header (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(before, tt.h); diff != "" {
				t.Errorf("header changed the response's header (-before +after):\n%s", diff)
			}
			for name, values := range got {
				if len(values) > 0 && values[0] != Redacted && &values[0] != &tt.h[name][0] {
					t.Errorf("%s was copied, want its value slice shared", name)
				}
			}
		})
	}
	t.Run("success: the 8-byte threshold lives in the redactor", func(t *testing.T) {
		if r := NewHeaderRedactor("k123456"); r != (HeaderRedactor{}) {
			t.Errorf("NewHeaderRedactor(7-byte key) = %+v, want the zero value", r)
		}
		if r := NewHeaderRedactor("k1234567"); r.key != "k1234567" {
			t.Errorf("NewHeaderRedactor(8-byte key) = %+v, want the key kept", r)
		}
	})
}

// TestHeaderRedactorRequestID pins the request id a log record shows in the
// INFO "response" record: for every header, redactor and key length,
// HeaderRedactor.RequestID returns what the error types' RequestID returns
// from the header the same redactor stored, without copying the header,
// whether the id comes from x-typesafe-request-id or, without a value
// there, from x-request-id.
func TestHeaderRedactorRequestID(t *testing.T) {
	const key = "ts_live_QzXjWvKpYbNmHgFd"
	tests := map[string]struct {
		r      HeaderRedactor
		values []string // the x-typesafe-request-id values, nil for none
		// fallback is the x-request-id values, nil for none: read only when
		// the x-typesafe-request-id header has no value.
		fallback []string
		want     string
		wantOK   bool
	}{
		"success: no request id": {r: NewHeaderRedactor(key)},
		"success: an x-request-id alone": {
			r: NewHeaderRedactor(key), fallback: []string{"req_x"}, want: "req_x", wantOK: true,
		},
		"success: a repeated x-request-id, joined": {
			r: NewHeaderRedactor(key), fallback: []string{"req_x1", "req_x2"}, want: "req_x1, req_x2", wantOK: true,
		},
		"success: an x-request-id that holds the key": {
			r: NewHeaderRedactor(key), fallback: []string{"req " + key}, want: Redacted, wantOK: true,
		},
		"success: an x-typesafe-request-id without values falls back to x-request-id": {
			r: NewHeaderRedactor(key), values: []string{}, fallback: []string{"req_x"}, want: "req_x", wantOK: true,
		},
		"success: x-typesafe-request-id wins over x-request-id": {
			r: NewHeaderRedactor(key), values: []string{"req_ts"}, fallback: []string{"req " + key}, want: "req_ts", wantOK: true,
		},
		"success: an id without the key": {
			r: NewHeaderRedactor(key), values: []string{"req_123"}, want: "req_123", wantOK: true,
		},
		"success: a repeated id without the key, joined": {
			r: NewHeaderRedactor(key), values: []string{"req_1", "req_2"}, want: "req_1, req_2", wantOK: true,
		},
		"success: an id that holds the key": {
			r: NewHeaderRedactor(key), values: []string{"req " + key}, want: Redacted, wantOK: true,
		},
		"success: one of two ids holds the key, so both are hidden": {
			r: NewHeaderRedactor(key), values: []string{"req_1", key}, want: Redacted + ", " + Redacted, wantOK: true,
		},
		"success: the zero redactor redacts by name alone": {
			values: []string{"req " + key}, want: "req " + key, wantOK: true,
		},
		"success: a key of 7 bytes is not looked for (R68)": {
			r: NewHeaderRedactor("k123456"), values: []string{"req k123456"}, want: "req k123456", wantOK: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := http.Header{"Content-Type": {"application/json"}}
			if tt.values != nil {
				h["X-Typesafe-Request-Id"] = slices.Clone(tt.values)
			}
			if tt.fallback != nil {
				h["X-Request-Id"] = slices.Clone(tt.fallback)
			}
			id, ok := tt.r.RequestID(h)
			if id != tt.want || ok != tt.wantOK {
				t.Errorf("requestID = %q, %t, want %q, %t", id, ok, tt.want, tt.wantOK)
			}
			stored := wire.ResponseMeta{Header: tt.r.Header(h)} // what the error types' RequestID reads
			if storedID, storedOK := stored.RequestID(); id != storedID || ok != storedOK {
				t.Errorf("requestID = %q, %t, but the stored header's is %q, %t", id, ok, storedID, storedOK)
			}
			if tt.values != nil && !slices.Equal(h["X-Typesafe-Request-Id"], tt.values) {
				t.Errorf("the header's values changed to %q", h["X-Typesafe-Request-Id"])
			}
			if tt.fallback != nil && !slices.Equal(h["X-Request-Id"], tt.fallback) {
				t.Errorf("the x-request-id values changed to %q", h["X-Request-Id"])
			}
		})
	}
}
