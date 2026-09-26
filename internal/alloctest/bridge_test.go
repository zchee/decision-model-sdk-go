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

// Package alloctest holds the root package's allocation budgets (AC-P1,
// AC-P2, AC-P3, AC-P5, AC-P6, AC-P8 and the rows the ledger records), which
// W6.5 moved out of the root package (owner instruction G9, design D1): they
// measure the stages of a call that internal/engine holds, as the root
// package's calls run them, through the public API and the bridge below. It
// has test files only. The files built with //go:build !race run in CI's
// allocation-budget step, whose list names every test of them; the others
// run in the -race coverage step.
package alloctest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"testing"
	"time"

	typesafe "github.com/zchee/typesafe-sdk-go"
	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/engine"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// The bridge from the root package's public types to the state and the
// stages internal/engine holds for them (W6.5 design D1). The root package
// declares Client, Prepared and SystemOneResponse as defined types over
// engine.Client[RetryPolicy], engine.Prepared and engine.Response, so each
// conversion below is free and compile-checked, and each stage below is the
// engine function the root package's own wrapper calls, with that wrapper's
// signature, so the budgets measure what a call runs.

// engOf returns c's state (the root package's Client.eng).
func engOf(c *typesafe.Client) *engine.Client[typesafe.RetryPolicy] {
	return (*engine.Client[typesafe.RetryPolicy])(c)
}

// cfgOf returns c's configuration (the root package's Client.cfg).
func cfgOf(c *typesafe.Client) *engine.Config[typesafe.RetryPolicy] { return engOf(c).Config() }

// wireOf returns qs's bytes and tables (the root package's
// Prepared.wirePrepared), or nil for a nil set, as the root package's
// encodeBody and decodeSystemOneInto pass it.
func wireOf(qs *typesafe.Prepared) *wire.Prepared {
	if qs == nil {
		return nil
	}
	return (*engine.Prepared)(qs).Wire()
}

// responseOf returns a response holding res and no HTTP metadata.
func responseOf(res wire.SystemOneResult) *typesafe.SystemOneResponse {
	r := new(typesafe.SystemOneResponse)
	*(*engine.Response)(r).Result() = res
	return r
}

// encodeBody is the root package's encodeBody: engine.EncodeBody
// instantiated with the root package's RawJSON and Content. A body that
// encodes costs what the root package's does; a failure, which no budget
// measures, is reported as an error naming the failure's kind and member
// rather than the root package's *ConfigError or *InvalidRequestError.
func encodeBody(state any, model string, qs *typesafe.Prepared, extra []engine.BodyMember) (codec.Body, error) {
	body, f := engine.EncodeBody[typesafe.RawJSON, typesafe.Content](state, model, wireOf(qs), extra)
	if f.Kind != engine.FailNone {
		return codec.Body{}, errors.Join(errors.New("encode failure kind "+strconv.Itoa(int(f.Kind))+" at member "+strconv.Quote(f.Key)), f.Err)
	}
	return body, nil
}

// decodeSystemOne is the root package's decodeSystemOne:
// engine.DecodeSystemOneInto with no room for the answers. endpoint and r
// are the root package's wrapper's, which name and redact the
// *ResponseValidationError it builds from a refused body; here that body's
// error is the decoder's own, which no budget measures.
func decodeSystemOne(ctx context.Context, logger *slog.Logger, meta *wire.ResponseMeta, endpoint string, r engine.HeaderRedactor, qs *typesafe.Prepared, model string, dst *wire.SystemOneResult) error {
	return decodeSystemOneInto(ctx, logger, meta, endpoint, r, qs, model, dst, nil)
}

// decodeSystemOneInto is decodeSystemOne with spare as the room for the
// answers (the root package's decodeSystemOneInto).
func decodeSystemOneInto(ctx context.Context, logger *slog.Logger, meta *wire.ResponseMeta, _ string, _ engine.HeaderRedactor, qs *typesafe.Prepared, model string, dst *wire.SystemOneResult, spare []wire.AnswerEntry) error {
	return engine.DecodeSystemOneInto(ctx, logger, meta.Body, wireOf(qs), model, dst, spare)
}

// callSettings is what one call sends besides its body, as the root
// package's callSettings holds it: the header every attempt starts from and
// the deadline of each attempt (the call's retry policy is not measured
// here).
type callSettings struct {
	header  http.Header
	timeout time.Duration
}

// requireStoreLayout checks, before a budget's first typed decode into a T,
// that the typed store may write into a T: PreparedFor builds T's plan and
// runs the plan's layout check, which panics by name on an offset, an end or
// a kind that disagrees with reflect's (the root package's
// requireStoreLayout also cross-checks the plan's offsets, which it can
// read and this package cannot). Trusting the plan check here is sound only
// while that root copy stays: it compares each plan offset and end with
// reflect's independently of the check, so a check that passed a wrong plan
// still fails a root test (critic-p6 n-9).
func requireStoreLayout[T any](t *testing.T) {
	t.Helper()
	if _, err := typesafe.PreparedFor[T](); err != nil {
		t.Fatalf("PreparedFor[%T]: %v", *new(T), err)
	}
}
