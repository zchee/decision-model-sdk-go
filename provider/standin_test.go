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

package provider_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	decision "github.com/zchee/decision-model-sdk-go"
	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
	"github.com/zchee/decision-model-sdk-go/provider"
)

// standIn replays a dialect provider's recorded bodies over HTTP, as the
// vendor sent them: x-request-id on a 200 only, as Decisions API does. Each
// request's body is kept; the first tooMany requests are answered 429 with
// Retry-After-Ms, and a request whose key is not testKey is answered 401
// with the recorded wrong-key body.
type standIn struct {
	path     string
	ok       []byte
	wrongKey []byte
	tooMany  atomic.Int32

	mu     sync.Mutex
	bodies [][]byte
}

// ServeHTTP implements http.Handler.
func (s *standIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method != http.MethodPost || r.URL.Path != s.path:
		w.WriteHeader(http.StatusNotFound)
	case r.Header.Get("Authorization") != "Bearer "+testKey:
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(s.wrongKey)
	case s.tooMany.Add(-1) >= 0:
		w.Header().Set("Retry-After-Ms", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Slow down."}}`))
	default:
		w.Header().Set("X-Request-Id", "req_stand_in")
		_, _ = w.Write(s.ok)
	}
}

// sent returns the bodies of the requests the stand-in received.
func (s *standIn) sent() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies
}

// TestDialectStandIn calls each dialect preset's stand-in through
// WithProvider, WithBaseURL and WithAPIKey, over a real HTTP connection.
func TestDialectStandIn(t *testing.T) {
	tests := map[string]struct {
		preset   decision.Provider
		dir      string
		model    string
		okFile   string // the recorded 200 body the stand-in answers with
		wantSent []byte // the body every request must carry, or nil for the SDK's own
		want     ticketView
	}{
		"success: Decisions API": {
			preset: provider.DecisionsAPI(), dir: "decisions-api", okFile: "typed-response.json", model: "jev-latest",
			want: ticketView{
				Billing: 0.98, Tone: "frustrated", ToneConfidence: 0.88,
				ToneProbabilities: map[string]float64{"calm": 0, "frustrated": 0.92, "angry": 0.08},
				Urgency:           1.92, UrgencyConfidence: 0.88,
				Legend:       map[uint32]string{0: "can wait", 1: "this week", 2: "today"},
				UrgencyProbs: map[uint32]float64{0: 0, 1: 0.08, 2: 0.92},
			},
		},
		"success: OpenAI": {
			preset: provider.OpenAI(), dir: "openai", okFile: "dialect-response.json",
			wantSent: fixture(t, "openai", "dialect-request.json"),
			want: ticketView{
				Billing: 1, Tone: "frustrated", ToneConfidence: 0.96,
				ToneProbabilities: map[string]float64{"calm": 0.01, "frustrated": 0.97, "angry": 0.02},
				Urgency:           1.98, UrgencyConfidence: 0.97,
				Legend:       map[uint32]string{0: "can wait", 1: "this week", 2: "today"},
				UrgencyProbs: map[uint32]float64{0: 0, 1: 0.02, 2: 0.98},
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ok := fixture(t, tt.dir, tt.okFile)
			s := &standIn{path: tt.preset.SystemOnePath, ok: ok, wrongKey: fixture(t, tt.dir, "wrong-key.json")}
			srv := httptest.NewServer(s)
			t.Cleanup(srv.Close)
			setEnv(t, nil)
			logs := testsupport.NewLogRecorder(decision.LevelTrace)
			opts := []decision.ClientOption{
				decision.WithProvider(tt.preset), decision.WithBaseURL(srv.URL), decision.WithLogger(logs.Logger()),
				decision.WithRetry(decision.DefaultRetry().MaxRetries(1)),
			}
			if tt.model != "" {
				opts = append(opts, decision.WithModel(tt.model))
			}
			c, err := decision.NewClient(append(opts, decision.WithAPIKey(testKey))...)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}

			t.Run("ask", func(t *testing.T) {
				tk, err := decision.Ask[liveTicket](t.Context(), c, recordedState)
				if err != nil {
					t.Fatalf("Ask: %v", err)
				}
				if diff := gocmp.Diff(tt.want, viewTicket(tk)); diff != "" {
					t.Errorf("answers (-want +got):\n%s", diff)
				}
			})

			t.Run("response", func(t *testing.T) {
				logs.Reset()
				resp, err := c.SystemOne(t.Context(), recordedState, ticketPrepared(t))
				if err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
				if id, ok := resp.Meta().RequestID(); !ok || id != "req_stand_in" {
					t.Errorf("RequestID() = %q, %t; want req_stand_in, true", id, ok)
				}
				if bytes.Equal(resp.Meta().RawBody(), ok) || !bytes.HasPrefix(resp.Meta().RawBody(), []byte(`{"model":`)) {
					t.Errorf("RawBody() = %s, want the translated System One body", resp.Meta().RawBody())
				}
				var traced [][]byte
				for _, r := range logs.At(decision.LevelTrace) {
					if v, has := r.Attr("body"); has && r.Message == "response body" {
						traced = append(traced, []byte(v.String()))
					}
				}
				if len(traced) != 1 || !bytes.Equal(traced[0], ok) {
					t.Errorf("LevelTrace response bodies = %q, want the vendor's body alone", traced)
				}
				tk, err := decision.DecodeAs[liveTicket](resp)
				if err != nil {
					t.Fatalf("DecodeAs: %v", err)
				}
				if diff := gocmp.Diff(tt.want, viewTicket(tk)); diff != "" {
					t.Errorf("answers (-want +got):\n%s", diff)
				}
			})

			t.Run("wrong key", func(t *testing.T) {
				wrong, err := decision.NewClient(append(opts, decision.WithAPIKey("sk-not-issued-0123456789"))...)
				if err != nil {
					t.Fatalf("NewClient: %v", err)
				}
				defer wrong.Close()
				_, err = wrong.SystemOne(t.Context(), recordedState, ticketPrepared(t))
				apiErr := apiErrorOf(t, err)
				if apiErr.StatusCode != http.StatusUnauthorized || apiErr.ErrorType != "authentication_error" || !apiErr.IsAuthentication() {
					t.Errorf("StatusCode %d, ErrorType %q, IsAuthentication %t; want 401, authentication_error, true", apiErr.StatusCode, apiErr.ErrorType, apiErr.IsAuthentication())
				}
				if id, ok := apiErr.RequestID(); ok {
					t.Errorf("RequestID() = %q, true; want none on a 401", id)
				}
			})

			t.Run("retry", func(t *testing.T) {
				before := len(s.sent())
				s.tooMany.Store(1)
				if _, err := c.SystemOne(t.Context(), recordedState, ticketPrepared(t)); err != nil {
					t.Fatalf("SystemOne: %v", err)
				}
				sent := s.sent()[before:]
				if len(sent) != 2 {
					t.Fatalf("%d requests, want 2: a 429, then its retry", len(sent))
				}
				if !bytes.Equal(sent[0], sent[1]) {
					t.Errorf("the retry sent %s, want the first attempt's %s byte for byte", sent[1], sent[0])
				}
			})

			t.Run("concurrent calls", func(t *testing.T) {
				var wg sync.WaitGroup
				for range 16 {
					wg.Go(func() {
						tk, err := decision.Ask[liveTicket](t.Context(), c, recordedState)
						if err != nil {
							t.Errorf("Ask: %v", err)
							return
						}
						if diff := gocmp.Diff(tt.want, viewTicket(tk)); diff != "" {
							t.Errorf("answers (-want +got):\n%s", diff)
						}
					})
				}
				wg.Wait()
			})

			for i, body := range s.sent() {
				if tt.wantSent != nil && !bytes.Equal(body, tt.wantSent) {
					t.Errorf("request %d sent %s, want %s", i, body, tt.wantSent)
				}
				if tt.wantSent == nil && !bytes.HasPrefix(body, []byte(`{"state":{"subject"`)) {
					t.Errorf("request %d sent %s, want the System One body", i, body)
				}
			}

			if err := c.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if _, err := c.SystemOne(t.Context(), recordedState, ticketPrepared(t)); !errors.Is(err, decision.ErrClientClosed) {
				t.Errorf("a call after Close = %v, want ErrClientClosed", err)
			}
		})
	}
}
