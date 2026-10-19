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

//go:build live

package livetest

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	decision "github.com/zchee/decision-model-sdk-go"
)

// TestLiveProviders calls each provider DECISION_MODEL_LIVE_PROVIDERS names
// with the SDK's own wire, through decision.WithProvider, and records what
// each returned under testdata/live/providers/<name> (-args -record). It
// reads its environment through providerEnvFrom and fails before it calls
// any API unless DECISION_MODEL_LIVE_TESTS is 1 and every named provider has
// its key variable and a model. It does not need the generic DECISION_MODEL_
// variables, which the other live tests need, so run it alone:
//
//	go test -tags live -count=1 -v -run '^TestLiveProviders$' ./livetest/ -args -record
//
// Per provider, in order, each subtest names the status it expects:
//
//   - models: the listing (200), or, for a provider without one, the
//     *ConfigError that List returns before sending.
//   - questions: a System One provider is asked the three questions of
//     TestLiveQuestions; a provider with plainNoul (Decisions API, OpenAI,
//     both OpenRouter endpoints) is asked liveTicket's, whose yes/no
//     question has no outcomes, which Decisions API requires both of,
//     OpenAI's wire has no place for and OpenRouter refuses one of without
//     the other. Each answers 200 and the answers are checked as there,
//     except that a provider with noRequestID (OpenRouter, whose responses
//     name the call in x-generation-id alone) is not expected to carry a
//     request id.
//   - typed: Ask[liveTicket], answered 200 and checked.
//   - wrong key: a key the provider did not issue, on the listing where one
//     exists and on the System One call otherwise: 401.
//
// What is recorded is each body as the vendor sent it: the response's
// RawBody on a System One provider, and the LevelTrace record of the
// response on a dialect provider, whose RawBody is the translated body. A
// body is recorded only where recordedProviderBodies lists it: OpenAI's
// answers are not.
func TestLiveProviders(t *testing.T) {
	env, err := providerEnvFrom(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	secrets := slices.Concat(env.secrets, []string{wrongLiveKey})
	for _, sp := range env.providers {
		t.Run(sp.name, func(t *testing.T) {
			liveProviderRun(t, sp, secrets)
		})
	}
}

// liveProviderRun runs TestLiveProviders' four subtests for sp. The client
// reads sp's key from sp's own variable; secrets is every key the recorder
// scrubs.
func liveProviderRun(t *testing.T, sp selectedProvider, secrets []string) {
	dir := filepath.Join(liveDir, "providers", sp.name)
	opts := []decision.ClientOption{decision.WithProvider(sp.p)}
	if sp.modelSet {
		opts = append(opts, decision.WithModel(sp.model))
	}
	t.Logf("provider %s: host %s, model %s", sp.name, strings.TrimPrefix(sp.p.BaseURL, "https://"), sp.model)
	lc := newLiveClient(t, opts...)

	t.Run("models", func(t *testing.T) {
		lc.begin()
		resp, err := lc.c.Models().List(t.Context())
		lc.done(t, sp.name+" models")
		if sp.p.ModelsPath == "" {
			if _, ok := errors.AsType[*decision.ConfigError](err); !ok {
				t.Fatalf("Models().List() error = %s, want the *ConfigError of a provider without a listing", describeErr(err, secrets))
			}
			if n := lc.c.Stats().Attempts; n != 0 {
				t.Errorf("Stats().Attempts = %d, want 0: List must send nothing", n)
			}
			return
		}
		if err != nil {
			t.Fatalf("Models().List() error = %s", describeErr(err, secrets))
		}
		if len(resp.Models()) == 0 {
			t.Error("Models().List() returned no models")
		}
		for i, m := range resp.Models() {
			if len(credentialFindings([]byte(m.Name()), secrets...)) > 0 || len(credentialFindings([]byte(m.ReleaseDate()), secrets...)) > 0 {
				t.Logf("model %d: (text withheld: it holds a credential shape)", i)
				continue
			}
			t.Logf("model %d: name=%q release_date=%q", i, m.Name(), m.ReleaseDate())
		}
		recordBody(t, dir, "models.json", resp.Meta().RawBody(), secrets...)
	})

	t.Run("questions", func(t *testing.T) {
		qs := liveQuestionSet(t)
		if sp.plainNoul {
			qs = plainNoulSet(t)
		}
		lc.begin()
		resp, err := lc.c.SystemOne(t.Context(), ticketState, qs)
		lc.done(t, sp.name+" system one")
		if err != nil {
			t.Fatalf("SystemOne() error = %s", describeErr(err, secrets))
		}
		recordVendorBody(t, lc, sp, dir, "questions.json", resp.Meta().RawBody(), secrets)
		checkLiveAnswers(t, resp, !sp.noRequestID)
	})

	t.Run("typed", func(t *testing.T) {
		lc.begin()
		ticket, err := decision.Ask[liveTicket](t.Context(), lc.c, ticketState)
		lc.done(t, sp.name+" ask")
		if err != nil {
			t.Fatalf("Ask() error = %s", describeErr(err, secrets))
		}
		checkLiveTicket(t, ticket)
		recordVendorBody(t, lc, sp, dir, "typed-response.json", nil, secrets)
	})

	t.Run("wrong key", func(t *testing.T) {
		wrong := newLiveClient(t, slices.Concat(opts, []decision.ClientOption{decision.WithAPIKey(wrongLiveKey), decision.WithRetry(decision.NoRetry())})...)
		wrong.begin()
		var err error
		endpoint := "system one"
		switch {
		case sp.p.ModelsPath != "":
			endpoint = "models"
			_, err = wrong.c.Models().List(t.Context())
		case sp.plainNoul:
			_, err = wrong.c.SystemOne(t.Context(), "hello", plainNoulSet(t))
		default:
			_, err = wrong.c.SystemOne(t.Context(), "hello", liveQuestionSet(t))
		}
		wrong.done(t, sp.name+" wrong key "+endpoint)
		apiErr := wantAPIError(t, err, secrets)
		if apiErr.StatusCode != http.StatusUnauthorized {
			t.Errorf("StatusCode = %d, want 401; nothing recorded", apiErr.StatusCode)
			return
		}
		if !apiErr.IsAuthentication() {
			t.Errorf("IsAuthentication() = false on a 401")
		}
		t.Logf("%s wrong key on %s: status %d", sp.name, endpoint, apiErr.StatusCode)
		recordBody(t, dir, "wrong-key.json", apiErr.Body, secrets...)
	})
}

// wantAPIError returns err as an *APIError, failing the test when it is not
// one or when its text holds a secret.
func wantAPIError(t *testing.T, err error, secrets []string) *decision.APIError {
	t.Helper()
	apiErr, ok := errors.AsType[*decision.APIError](err)
	if !ok {
		t.Fatalf("error = %s, want an *APIError", describeErr(err, secrets))
	}
	checkNoSecret(t, apiErr.Error(), secrets)
	t.Logf("%s", describeErr(apiErr, secrets))
	return apiErr
}

// plainNoulSet returns liveTicket's three questions, whose yes/no question
// has no outcomes: the set a provider with plainNoul is asked.
func plainNoulSet(t *testing.T) *decision.Prepared {
	t.Helper()
	qs, err := decision.PreparedFor[liveTicket]()
	if err != nil {
		t.Fatal(err)
	}
	return qs
}

// recordVendorBody records name for sp, when recordedProviderBodies lists
// it, as the vendor sent it: raw, the response's RawBody, on a System One
// provider, and the last LevelTrace response record on a dialect provider or
// when raw is nil.
func recordVendorBody(t *testing.T, lc *liveClient, sp selectedProvider, dir, name string, raw []byte, secrets []string) {
	t.Helper()
	if !recordsProviderBody(sp.name, name) {
		t.Logf("%s: %s is not recorded", sp.name, name)
		return
	}
	if raw == nil || sp.kind == kindDialect {
		rec, ok := lc.lastRecord("response body")
		if !ok {
			t.Fatal("no LevelTrace response body record")
		}
		body, _ := rec.Attr("body")
		raw = []byte(body.String())
	}
	recordBody(t, dir, name, raw, secrets...)
}

// describeErr renders err for the test's log without a server's text that
// could hold a key: the status, the error type, the kind and the request id
// of an *APIError, the status, the field path and the request id of a
// *ResponseValidationError, and any text (the server's message, or another
// error's own text) only when credentialFindings finds nothing in it.
func describeErr(err error, secrets []string) string {
	clean := func(text string) bool { return len(credentialFindings([]byte(text), secrets...)) == 0 }
	if apiErr, ok := errors.AsType[*decision.APIError](err); ok {
		id, _ := apiErr.RequestID()
		out := fmt.Sprintf("*APIError status=%d error_type=%q kind=%v request_id=%q", apiErr.StatusCode, apiErr.ErrorType, apiErr.Kind, id)
		if clean(apiErr.Message) {
			return out + fmt.Sprintf(" message=%q", apiErr.Message)
		}
		return out + " message=(withheld: it holds a credential shape)"
	}
	if ve, ok := errors.AsType[*decision.ResponseValidationError](err); ok {
		id, _ := ve.RequestID()
		return fmt.Sprintf("*ResponseValidationError status=%d field_path=%q request_id=%q", ve.StatusCode, ve.FieldPath, id)
	}
	if err == nil {
		return "nil"
	}
	if clean(err.Error()) {
		return fmt.Sprintf("%T: %s", err, err)
	}
	return fmt.Sprintf("%T (text withheld: it holds a credential shape)", err)
}

// checkNoSecret fails the test, naming no secret, when text holds one.
func checkNoSecret(t *testing.T, text string, secrets []string) {
	t.Helper()
	for i, s := range secrets {
		if s != "" && strings.Contains(text, s) {
			t.Errorf("the error text holds secret %d of %d", i+1, len(secrets))
		}
	}
}
