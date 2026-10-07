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

package cassette

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
)

type queryScrubRoundTrip func(*http.Request) (*http.Response, error)

func (f queryScrubRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRecorderDropsCredentialQueryNames(t *testing.T) {
	tests := map[string]struct{}{
		"key": {}, "api_key": {}, "api-key": {}, "apikey": {},
		"token": {}, "access_token": {}, "access-token": {}, "accesstoken": {},
		"auth_token": {}, "auth-token": {}, "authtoken": {}, "authorization": {},
		"password": {}, "secret": {}, "client_secret": {}, "client-secret": {}, "clientsecret": {},
	}
	for name := range tests {
		t.Run("success: "+name, func(t *testing.T) {
			variants := map[string]struct{ name string }{
				"lowercase":         {name: name},
				"uppercase":         {name: strings.ToUpper(name)},
				"mixed case":        {name: strings.ToUpper(name[:1]) + name[1:]},
				"encoded uppercase": {name: fmt.Sprintf("%%%02X%s", name[0], strings.ToUpper(name[1:]))},
				"encoded lowercase": {name: fmt.Sprintf("%%%02x%s", name[0], name[1:])},
			}
			for variant, tt := range variants {
				t.Run(variant, func(t *testing.T) {
					const marker = "CedarQueryCanary"
					const before = "model=Birch%2BLeaf&token_count=4"
					const after = "API_KEY_HINT=ordinary&model=Hazel+Branch&bare&benign=client_secret"
					const want = before + "&" + after
					raw := before + "&" + tt.name + "=" + marker + "&" + tt.name + "=&" + tt.name + "&" + name + "=" + marker + "&" + after
					called := false
					next := queryScrubRoundTrip(func(req *http.Request) (*http.Response, error) {
						called = true
						if req.URL.RawQuery != raw {
							t.Error("outbound query was changed")
						}
						return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: req}, nil
					})
					dir := t.TempDir()
					recorder, err := NewRecorder(next, dir, "scrub.json")
					if err != nil {
						t.Fatal("recorder construction failed")
					}
					req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.invalid/path?"+raw, nil)
					if err != nil {
						t.Fatal("request construction failed")
					}
					resp, err := recorder.RoundTrip(req)
					if err != nil {
						t.Fatal("recording failed")
					}
					_ = resp.Body.Close()
					if !called || req.URL.RawQuery != raw {
						t.Fatal("offline round trip or original query changed")
					}
					written, err := os.ReadFile(filepath.Join(dir, "scrub.json"))
					if err != nil {
						t.Fatal("recorded bytes unavailable")
					}
					if strings.Contains(string(written), marker) {
						t.Errorf("credential query marker retained; length=%d", len(written))
					}
					doc, err := jsonx.Read(written)
					if err != nil {
						t.Fatal("recorded document is invalid")
					}
					interactions, ok := doc.Member("interactions")
					if !ok || interactions.Len() != 1 {
						t.Fatal("recorded interaction absent")
					}
					request, ok := interactions.Index(0).Member("request")
					if !ok {
						t.Fatal("recorded request absent")
					}
					uri, ok := request.Member("uri")
					if !ok {
						t.Fatal("recorded URI absent")
					}
					u, err := url.Parse(uri.Text())
					if err != nil {
						t.Fatal("recorded URI is invalid")
					}
					if diff := cmp.Diff(true, u.RawQuery == want); diff != "" {
						t.Errorf("benign query order or spelling changed; length=%d", len(u.RawQuery))
					}
				})
			}
		})
	}
}

func TestScrubbedQueryDecodeBoundary(t *testing.T) {
	tests := map[string]struct{ raw, want string }{
		"success: empty query":                {},
		"success: undecodable name dropped":   {raw: "a=1&%zz=CedarQueryCanary&b=2", want: "a=1&b=2"},
		"success: values not decoded":         {raw: "ordinary=%zz&token_count=1", want: "ordinary=%zz&token_count=1"},
		"success: name decoded once":          {raw: "%256Bey=ordinary&model=x", want: "%256Bey=ordinary&model=x"},
		"success: empty segments":             {raw: "&&token=&a=1&&", want: "a=1"},
		"success: camel names":                {raw: "apiKey=CedarQueryCanary&accessToken=CedarQueryCanary&authToken=CedarQueryCanary&clientSecret=CedarQueryCanary&keep=1", want: "keep=1"},
		"success: signed URL names untouched": {raw: "signature=ordinary&expires=4", want: "signature=ordinary&expires=4"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := scrubbedQuery(tt.raw)
			if diff := cmp.Diff(true, got == tt.want); diff != "" {
				t.Errorf("query boundary mismatch; length=%d", len(got))
			}
		})
	}
}
