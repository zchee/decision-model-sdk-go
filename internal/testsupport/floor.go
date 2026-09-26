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

package testsupport

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// FloorCall is the floor of one whole SDK call (the port plan's NF3): rt
// called with a request built beforehand, its response drained into
// [io.Discard] and closed. No client can cost less. The root package's
// TestAllocWholeCall counts its allocations and the call/floor benchmark
// times it, so the two measure one floor. It returns the transport's error,
// or the read's, or the close's, in that order.
func FloorCall(rt http.RoundTripper, req *http.Request) error {
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, resp.Body)
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	return err
}

// NewFixtureServer starts a [LoopbackServer] that answers the way the
// benchmarks' calls expect: models.json for a path ending in /v1/models and
// result.json for every other request, each with a Content-Type of
// application/json and a Content-Length, after reading the whole request
// body as a real server would. Close runs from tb.Cleanup.
func NewFixtureServer(tb testing.TB) *LoopbackServer {
	tb.Helper()
	result := FixtureString(tb, "result.json")
	models := FixtureString(tb, "models.json")
	return NewLoopbackServer(tb, ServerConfig{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			body := result
			if strings.HasSuffix(r.URL.Path, "/v1/models") {
				body = models
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = io.WriteString(w, body)
		}),
	})
}
