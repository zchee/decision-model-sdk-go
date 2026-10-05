// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestScrubMatchesUpstream pins the scrub lists to the values
// tests/conftest.py of system-one-adapter-python v0.2.1 configures
// (FILTERED_HEADERS, filter_query_parameters, ALLOWED_RESPONSE_HEADERS)
// and checks every committed cassette against them: no cassette holds a
// filtered request header, no recorded URI carries a filtered query
// parameter, and every response header is within the allowlist.
func TestScrubMatchesUpstream(t *testing.T) {
	t.Parallel()
	wantFilteredHeaders := []string{
		"authorization",
		"x-api-key",
		"x-goog-api-key",
		"api-key",
		"openai-organization",
		"openai-project",
		"cookie",
		"set-cookie",
	}
	if diff := cmp.Diff(wantFilteredHeaders, FilteredRequestHeaders); diff != "" {
		t.Errorf("FilteredRequestHeaders (-upstream +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"api_key", "key"}, FilteredQueryParameters); diff != "" {
		t.Errorf("FilteredQueryParameters (-upstream +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"content-type"}, AllowedResponseHeaders); diff != "" {
		t.Errorf("AllowedResponseHeaders (-upstream +got):\n%s", diff)
	}

	dir := filepath.Join("..", "..", "testdata", "cassettes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	if got, want := len(paths), 25; got != want {
		t.Fatalf("found %d committed cassettes, want %d", got, want)
	}
	for _, path := range paths {
		c, err := Load(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		for i, ix := range c.Interactions {
			for name := range ix.Request.Headers {
				if slices.Contains(FilteredRequestHeaders, strings.ToLower(name)) {
					t.Errorf("%s interaction %d: the request header %q is one upstream's recording strips", path, i, name)
				}
			}
			u, err := url.Parse(ix.Request.URI)
			if err != nil {
				t.Errorf("%s interaction %d: the recorded URI does not parse: %v", path, i, err)
				continue
			}
			for name := range u.Query() {
				if slices.Contains(FilteredQueryParameters, strings.ToLower(name)) {
					t.Errorf("%s interaction %d: the recorded URI carries the query parameter %q, which upstream's recording removes", path, i, name)
				}
			}
			for name := range ix.Response.Headers {
				if !slices.Contains(AllowedResponseHeaders, strings.ToLower(name)) {
					t.Errorf("%s interaction %d: the response header %q is outside upstream's allowlist", path, i, name)
				}
			}
		}
	}
}
