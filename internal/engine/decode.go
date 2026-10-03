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

package engine

import (
	"context"
	"log/slog"

	"github.com/zchee/decision-model-sdk-go/internal/codec"
	"github.com/zchee/decision-model-sdk-go/internal/wire"
)

// DecodeSystemOneInto decodes body, the body of a successful System One
// response, into *dst, with spare as the room for the answers
// ([codec.DecodeSystemOneInto]). q is the question set the request asked
// and model the model it named: the answers' strings are theirs where equal,
// so the result never aliases the body. A body the decoder refuses returns
// the decoder's error, which the root package wraps into a
// *ResponseValidationError naming the first failure the Python SDK would
// report.
//
// Every answer of a type this version does not model is dropped and logged
// at WARN through logger, as the Python SDK logs "Ignoring answer %r with
// unrecognized type %r": at most [codec.MaxSkipped] lines per response,
// each with the answer's name and type escaped and cut at 128 characters,
// then one line counting the rest. The lines are logged even when the decode
// then fails, for the answers the Python SDK would have logged before
// failing. A nil logger logs nothing.
func DecodeSystemOneInto(ctx context.Context, logger *slog.Logger, body []byte, q *wire.Prepared, model string, dst *wire.SystemOneResult, spare []wire.AnswerEntry) error {
	skipped, err := codec.DecodeSystemOneInto(body, q, model, dst, spare)
	if skipped.Count > 0 {
		logSkipped(ctx, logger, &skipped)
	}
	return err
}

// The WARN lines for answers of unknown types.
const (
	MsgSkippedAnswer  = "Ignoring answer with unrecognized type"
	MsgSkippedAnswers = "Ignoring more answers with unrecognized types"
)

// logSkipped logs the answers a decode dropped: one WARN line per named
// answer, then one counting those past [codec.MaxSkipped]. An answer's name
// is body text and is not redacted: a name that echoes the client's API key
// is logged with it, as the Python SDK logs it.
func logSkipped(ctx context.Context, logger *slog.Logger, skipped *codec.Skipped) {
	if logger == nil || !logger.Enabled(ctx, slog.LevelWarn) {
		return
	}
	for _, s := range skipped.Named() {
		logger.LogAttrs(ctx, slog.LevelWarn, MsgSkippedAnswer, slog.String("answer", SafeName(s.Name)), slog.String("type", SafeName(s.Type)))
	}
	if rest := skipped.Count - len(skipped.Named()); rest > 0 {
		logger.LogAttrs(ctx, slog.LevelWarn, MsgSkippedAnswers, slog.Int("count", rest))
	}
}
