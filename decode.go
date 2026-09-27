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

package typesafe

import (
	"context"
	"log/slog"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/engine"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// decodeSystemOne decodes the body of a successful System One response,
// meta.Body, into *dst. qs is the question set the request asked and model
// the model it named: the answers' strings are theirs where equal
// (codec.DecodeSystemOne), so the result never aliases the body. A body the
// decoder refuses is a [*ResponseValidationError] naming the first failure
// the Python SDK would report.
//
// Every answer of a type this version does not model is dropped and logged
// at WARN through logger, as the Python SDK logs "Ignoring answer %r with
// unrecognized type %r": at most [codec.MaxSkipped] lines per response,
// each with the answer's name and type escaped and cut at 128 characters,
// then one line counting the rest. The lines are logged even when the decode
// then fails, for the answers the Python SDK would have logged before
// failing. A nil logger logs nothing. r redacts the error's header
// ([headerRedactor]).
func decodeSystemOne(ctx context.Context, logger *slog.Logger, meta *wire.ResponseMeta, endpoint string, r headerRedactor, qs *Prepared, model string, dst *wire.SystemOneResult) error {
	return decodeSystemOneInto(ctx, logger, meta, endpoint, r, qs, model, dst, nil)
}

// decodeSystemOneInto is decodeSystemOne with spare as the room for the
// answers ([engine.DecodeSystemOneInto], which logs the WARN lines).
func decodeSystemOneInto(ctx context.Context, logger *slog.Logger, meta *wire.ResponseMeta, endpoint string, r headerRedactor, qs *Prepared, model string, dst *wire.SystemOneResult, spare []wire.AnswerEntry) error {
	var q *wire.Prepared
	if qs != nil {
		q = qs.wirePrepared()
	}
	if err := engine.DecodeSystemOneInto(ctx, logger, meta.Body, q, model, dst, spare); err != nil {
		return newResponseValidationError(meta, endpoint, r, err)
	}
	return nil
}

// decodeModels decodes the body of a successful list-models response,
// meta.Body, into *dst. A body the decoder refuses is a
// [*ResponseValidationError], whose header r redacts.
func decodeModels(meta *wire.ResponseMeta, endpoint string, r headerRedactor, dst *wire.ModelList) error {
	if err := codec.DecodeModels(meta.Body, dst); err != nil {
		return newResponseValidationError(meta, endpoint, r, err)
	}
	return nil
}
