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

package decision_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"
	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
	"github.com/zchee/decision-model-sdk-go/provider"
)

// TestCodivPresetReadsOnlyItsVariable builds a client of the Codiv preset,
// pointed at a stand-in by replacing its BaseURL alone, with a getenv that
// fails the test on any name other than CODIV_API_KEY, although every
// generic variable is set too. The System One call and the listing reach
// the stand-in under Codiv's paths with CODIV_API_KEY's value as the bearer
// token, and CODIV_API_KEY is the one variable read.
func TestCodivPresetReadsOnlyItsVariable(t *testing.T) {
	const key = "sk-codiv-test-key-0123456789"
	env := map[string]string{
		"CODIV_API_KEY":          key,
		decision.APIKeyEnv:       "generic-key-not-for-codiv",
		decision.BaseURLEnv:      "https://generic.example.test",
		decision.DefaultModelEnv: "generic-model",
	}
	var (
		mu   sync.Mutex
		read []string
	)
	getenv := func(name string) string {
		mu.Lock()
		read = append(read, name)
		mu.Unlock()
		if name != "CODIV_API_KEY" {
			t.Errorf("the client read the environment variable %s; it may read only CODIV_API_KEY", name)
		}
		return env[name]
	}

	result := testsupport.Fixture(t, "result.json")
	models := testsupport.Fixture(t, "models.json")
	type seen struct{ Method, Path, Authorization string }
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, seen{Method: r.Method, Path: r.URL.Path, Authorization: r.Header.Get("Authorization")})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/systemone":
			_, _ = w.Write(result)
		case "/v1/models":
			_, _ = w.Write(models)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	p := provider.Codiv()
	p.BaseURL = srv.URL
	c, err := decision.NewClientWithEnv(getenv, decision.WithProvider(p), decision.WithRetry(decision.NoRetry()))
	if err != nil {
		t.Fatalf("NewClientWithEnv: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	qs, err := decision.NewQuestions().Noul("q", decision.Noul{Instructions: decision.Text("?")}).Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SystemOne(t.Context(), "hello", qs, decision.Model("openjev-latest")); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if _, err := c.Models().List(t.Context()); err != nil {
		t.Fatalf("List: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []seen{
		{Method: http.MethodPost, Path: "/v1/systemone", Authorization: "Bearer " + key},
		{Method: http.MethodGet, Path: "/v1/models", Authorization: "Bearer " + key},
	}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("what the stand-in received (-want +got):\n%s", diff)
	}
	if !slices.Equal(read, []string{"CODIV_API_KEY"}) {
		t.Errorf("variables read = %q, want only CODIV_API_KEY", read)
	}
}
