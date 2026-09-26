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
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
)

// FakeAPIModels is the body FakeAPI answers GET /v1/models with.
const FakeAPIModels = `{"models":[{"name":"jev-latest","description":"a stand-in model","release_date":"2026-09-10T00:00:00+00:00"}]}`

// FakeAPI is an in-process stand-in for the TypeSafe API, for tests that run
// programs written against the real one (the examples under examples/).
// It answers:
//
//   - a request without an Authorization header: 401 with the API's
//     authentication error body;
//   - GET /v1/models: [FakeAPIModels];
//   - POST /v1/systemone: every question of the request answered by its
//     type, with fixed values that the SDK's typed checks accept: a noul
//     0.75; a choice the first of its option labels in byte order, with
//     probability 1 and 0 for the others; a score its highest level, with
//     probability 1, and its levels as the legend, each level's JSON as the
//     request sent it. A question of another type gets no answer. The
//     response names the request's model and counts the request's bytes as
//     its input tokens;
//   - a System One body that is not an object with state, model and
//     questions: 422 with a detail list, as the API's validation does;
//   - anything else: 404.
//
// Every response carries X-Typesafe-Request-Id "req_fake_<n>", n counting
// from 1. It is safe for concurrent use.
type FakeAPI struct {
	requests atomic.Int64
}

// Requests returns the number of requests served so far.
func (f *FakeAPI) Requests() int { return int(f.requests.Load()) }

// fakeQuestion is a question of a System One request as FakeAPI reads it.
type fakeQuestion struct {
	Type     string          `json:"type"`
	Criteria json.RawMessage `json:"criteria"`
}

// ServeHTTP implements http.Handler.
func (f *FakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := f.requests.Add(1)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Typesafe-Request-Id", "req_fake_"+strconv.FormatInt(n, 10))
	if r.Header.Get("Authorization") == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":{"error_type":"authentication_error","message":"Cannot authenticate with the server. Please check your API key and try again."}}`))
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		_, _ = w.Write([]byte(FakeAPIModels))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/systemone":
		body, status := fakeSystemOne(r)
		w.WriteHeader(status)
		_, _ = w.Write(body)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Not Found"}`))
	}
}

// fakeSystemOne answers one System One request body.
func fakeSystemOne(r *http.Request) ([]byte, int) {
	var req struct {
		State     json.RawMessage         `json:"state"`
		Model     *string                 `json:"model"`
		Questions map[string]fakeQuestion `json:"questions"`
	}
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return fakeValidation("body", err.Error()), http.StatusUnprocessableEntity
	}
	if err := json.Unmarshal(raw, &req); err != nil || req.State == nil || req.Model == nil || req.Questions == nil {
		return fakeValidation("body", "state, model and questions are required"), http.StatusUnprocessableEntity
	}
	answers := make(map[string]any, len(req.Questions))
	for name, q := range req.Questions {
		answer, err := fakeAnswer(q)
		if err != nil {
			return fakeValidation("questions."+name, err.Error()), http.StatusUnprocessableEntity
		}
		if answer != nil {
			answers[name] = answer
		}
	}
	out, err := json.Marshal(map[string]any{
		"model":   *req.Model,
		"answers": answers,
		"usage":   map[string]int{"input_tokens": len(raw), "output_tokens": len(answers)},
	})
	if err != nil {
		return fakeValidation("body", err.Error()), http.StatusInternalServerError
	}
	return out, http.StatusOK
}

// fakeAnswer returns the answer to one question, or nil for a type the fake
// does not know.
func fakeAnswer(q fakeQuestion) (any, error) {
	switch q.Type {
	case "noul":
		return map[string]any{"type": "noul", "noul": 0.75}, nil
	case "choice":
		var criteria map[string]json.RawMessage
		if err := json.Unmarshal(q.Criteria, &criteria); err != nil || len(criteria) == 0 {
			return nil, errors.New("a choice needs criteria with at least one option")
		}
		labels := make([]string, 0, len(criteria))
		for label := range criteria {
			labels = append(labels, label)
		}
		slices.Sort(labels)
		probabilities := make(map[string]float64, len(labels))
		for _, label := range labels {
			probabilities[label] = 0
		}
		probabilities[labels[0]] = 1
		return map[string]any{"type": "choice", "choice": labels[0], "confidence": 1.0, "probabilities": probabilities}, nil
	case "score":
		var levels []json.RawMessage
		if err := json.Unmarshal(q.Criteria, &levels); err != nil || len(levels) == 0 {
			return nil, errors.New("a score needs criteria with at least one level")
		}
		top := len(levels) - 1
		legend := make(map[string]json.RawMessage, len(levels))
		probabilities := make(map[string]float64, len(levels))
		for i, level := range levels {
			key := strconv.Itoa(i)
			legend[key] = level
			probabilities[key] = 0
		}
		probabilities[strconv.Itoa(top)] = 1
		return map[string]any{"type": "score", "score": float64(top), "confidence": 1.0, "legend": legend, "probabilities": probabilities}, nil
	default:
		return nil, nil
	}
}

// fakeValidation returns a 422 body in the API's shape: a detail list of
// {loc, msg, type}.
func fakeValidation(loc, msg string) []byte {
	out, _ := json.Marshal(map[string]any{"detail": []map[string]any{{
		"loc":  strings.Split(loc, "."),
		"msg":  msg,
		"type": "value_error",
	}}})
	return out
}
