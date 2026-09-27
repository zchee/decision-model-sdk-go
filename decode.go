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

// decodeSystemOneInto decodes the body of a successful System One response,
// meta.Body, into *dst, with spare as the room for the answers
// ([engine.DecodeSystemOneInto], which logs at WARN through logger the
// answers of a type this version does not model). qs is the question set the
// request asked and model the model it named. A body the decoder refuses is a
// [*ResponseValidationError] naming the first failure the Python SDK would
// report, whose header r redacts.
func decodeSystemOneInto(ctx context.Context, logger *slog.Logger, meta *wire.ResponseMeta, endpoint string, r engine.HeaderRedactor, qs *Prepared, model string, dst *wire.SystemOneResult, spare []wire.AnswerEntry) error {
	if err := engine.DecodeSystemOneInto(ctx, logger, meta.Body, qs.wirePrepared(), model, dst, spare); err != nil {
		return newResponseValidationError(meta, endpoint, r, err)
	}
	return nil
}

// decodeModels decodes the body of a successful list-models response,
// meta.Body, into *dst. A body the decoder refuses is a
// [*ResponseValidationError], whose header r redacts.
func decodeModels(meta *wire.ResponseMeta, endpoint string, r engine.HeaderRedactor, dst *wire.ModelList) error {
	if err := codec.DecodeModels(meta.Body, dst); err != nil {
		return newResponseValidationError(meta, endpoint, r, err)
	}
	return nil
}
