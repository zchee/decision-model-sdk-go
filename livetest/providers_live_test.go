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
//   - questions: the three questions of TestLiveQuestions. A System One
//     provider answers 200 and the answers are checked as there; Decisions
//     API answers 200 with an envelope the decoder refuses, so the test
//     expects a *ResponseValidationError and records its body; OpenAI
//     refuses the request with 400, recorded as refused.json.
//   - typed: Ask[liveTicket], with the same three outcomes.
//   - wrong key: a key the provider did not issue, on the listing where one
//     exists and on the System One call otherwise: 401, except that OpenAI
//     may check the request before the key and answer 400; the test records
//     and logs whichever arrives.
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
		if sp.kind == kindEnvelope {
			// Decisions API validates a noul's criteria more strictly than
			// TypeSafe AI: it refuses criteria that do not carry both the
			// true and the false key (400), and liveQuestionSet's raw noul
			// carries only true. It is asked liveTicket's three questions,
			// whose noul has no criteria, as the typed call asks them.
			var err error
			if qs, err = decision.PreparedFor[liveTicket](); err != nil {
				t.Fatal(err)
			}
		}
		lc.begin()
		resp, err := lc.c.SystemOne(t.Context(), ticketState, qs)
		lc.done(t, sp.name+" system one")
		switch sp.kind {
		case kindSystemOne:
			if err != nil {
				t.Fatalf("SystemOne() error = %s", describeErr(err, secrets))
			}
			recordBody(t, dir, "questions.json", resp.Meta().RawBody(), secrets...)
			checkLiveAnswers(t, resp)
		case kindEnvelope:
			ve := wantEnvelope(t, err, secrets)
			if ve.FieldPath != "model" {
				t.Errorf("FieldPath = %q, want %q: the envelope has no top-level model", ve.FieldPath, "model")
			}
			recordBody(t, dir, "questions.json", ve.Body, secrets...)
		case kindRefused:
			apiErr := wantAPIError(t, err, secrets)
			if apiErr.StatusCode != http.StatusBadRequest {
				t.Errorf("StatusCode = %d, want 400: the provider refuses the System One wire; nothing recorded", apiErr.StatusCode)
				return
			}
			recordBody(t, dir, "refused.json", apiErr.Body, secrets...)
		}
	})

	t.Run("typed", func(t *testing.T) {
		lc.begin()
		ticket, err := decision.Ask[liveTicket](t.Context(), lc.c, ticketState)
		lc.done(t, sp.name+" ask")
		switch sp.kind {
		case kindSystemOne:
			if err != nil {
				t.Fatalf("Ask() error = %s", describeErr(err, secrets))
			}
			checkLiveTicket(t, ticket)
			bodyRec, ok := lc.lastRecord("response body")
			if !ok {
				t.Fatal("no LevelTrace response body record")
			}
			body, _ := bodyRec.Attr("body")
			recordBody(t, dir, "typed-response.json", []byte(body.String()), secrets...)
		case kindEnvelope:
			ve := wantEnvelope(t, err, secrets)
			recordBody(t, dir, "typed-response.json", ve.Body, secrets...)
		case kindRefused:
			if apiErr := wantAPIError(t, err, secrets); apiErr.StatusCode != http.StatusBadRequest {
				t.Errorf("StatusCode = %d, want 400: the provider refuses the System One wire", apiErr.StatusCode)
			}
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		wrong := newLiveClient(t, slices.Concat(opts, []decision.ClientOption{decision.WithAPIKey(wrongLiveKey), decision.WithRetry(decision.NoRetry())})...)
		wrong.begin()
		var err error
		endpoint := "system one"
		if sp.p.ModelsPath != "" {
			endpoint = "models"
			_, err = wrong.c.Models().List(t.Context())
		} else {
			_, err = wrong.c.SystemOne(t.Context(), "hello", liveQuestionSet(t))
		}
		wrong.done(t, sp.name+" wrong key "+endpoint)
		apiErr := wantAPIError(t, err, secrets)
		switch {
		case apiErr.StatusCode == http.StatusUnauthorized:
			if !apiErr.IsAuthentication() {
				t.Errorf("IsAuthentication() = false on a 401")
			}
		case sp.kind == kindRefused && apiErr.StatusCode == http.StatusBadRequest:
			t.Logf("%s answered the wrong key with 400: it checks the request before the key", sp.name)
		default:
			t.Errorf("StatusCode = %d, want 401; nothing recorded", apiErr.StatusCode)
			return
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

// wantEnvelope returns err as the *ResponseValidationError of a 200 whose
// body wraps the answers in an envelope, and fails the test, so that nothing
// is recorded, otherwise.
func wantEnvelope(t *testing.T, err error, secrets []string) *decision.ResponseValidationError {
	t.Helper()
	ve, ok := errors.AsType[*decision.ResponseValidationError](err)
	if !ok {
		t.Fatalf("error = %s, want a *ResponseValidationError for the envelope", describeErr(err, secrets))
	}
	checkNoSecret(t, ve.Error(), secrets)
	if ve.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200; nothing recorded", ve.StatusCode)
	}
	t.Logf("%s", describeErr(ve, secrets))
	return ve
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
