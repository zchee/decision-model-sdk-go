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

package decision

import (
	"errors"
	"strconv"

	"github.com/zchee/decision-model-sdk-go/internal/codec"
	"github.com/zchee/decision-model-sdk-go/internal/engine"
)

// bodyMember is one member a call adds to the top level of its request
// body, as the Python SDK's extra_body does ([engine.BodyMember]).
type bodyMember = engine.BodyMember

// encodeBody encodes the request body of one System One call into a pooled
// scratch buffer, with [engine.EncodeBody] instantiated with the root
// package's RawJSON and Content. It returns the body holding the call's
// reference, which the caller drops with Release when the call returns;
// each attempt takes its own reference with Body.Open, and the transport
// one more with GetBody each time it sends the request again.
//
// [engine.EncodeBody] describes the body's layout, and which values sonic
// writes and which are checked.
//
// A float inside the state and the extra values that sonic writes keeps
// sonic's spelling: 3.0 is written 3, -0.0 as 0 on arm64 and as -0 on amd64
// (sonic's amd64 JIT writes the sign, its arm64 VM does not), and
// 1e16 <= |x| < 1e21 and 1e-6 <= |x| < 1e-5 in fixed digits, where the
// Python SDK writes 3.0, -0.0 and e-notation. The same state can therefore
// be sent with different bytes from the two architectures, for a negative
// zero only. A value that needs an exact spelling is sent as RawJSON,
// or carries the number as a string. In the state and the extra values, a
// map's members go out in Go's iteration order, which changes from one call
// to the next where Python keeps a dict's insertion order; the body of one
// call, and so every attempt of it, is encoded once. A struct or RawJSON
// gives stable bytes.
//
// The output of a caller's json.Marshaler, a nested json.RawMessage among
// them, is the caller's contract, as a top-level RawJSON is: sonic's check
// of it does not refuse every invalid output, while the SDK's own nested
// Content and RawJSON go through wire's scanner, and the body is not
// scanned again as a whole.
//
// It fails with a [*ConfigError] when qs is nil or holds no question (a
// Prepared that [Questions.Prepare] did not return), or when model is not
// valid UTF-8, and with an [*InvalidRequestError] when a member cannot be
// encoded. The configuration is checked first.
func encodeBody(state any, model string, qs *Prepared, extra []bodyMember) (codec.Body, error) {
	body, f := engine.EncodeBody[RawJSON, Content](state, model, qs.wirePrepared(), extra)
	switch f.Kind {
	case engine.FailNone:
		return body, nil
	case engine.FailNoQuestions:
		return codec.Body{}, newConfigError("At least one question is required.")
	case engine.FailModel:
		return codec.Body{}, newConfigError("Model " + strconv.Quote(model) + " is not valid UTF-8.")
	}
	member := f.Key
	if f.Extra {
		member = "extra body member " + engine.QuotedName(f.Key)
	}
	return codec.Body{}, encodeError(member, f.Err)
}

// encodeError is the [*InvalidRequestError] for the body member that could
// not be encoded, named as the message names it. The cause's text, which can
// quote what the caller passed, is escaped and cut at
// [engine.MaxMessageChars], so the message never carries the state into a
// log; the whole cause stays behind Unwrap.
func encodeError(member string, err error) *InvalidRequestError {
	msg := make([]byte, 0, 64+len(member)+engine.MaxMessageChars)
	msg = append(msg, "The request body could not be encoded as JSON: "...)
	msg = append(msg, member...)
	msg = append(msg, ": "...)
	msg = engine.AppendSafeText(msg, err.Error(), engine.MaxMessageChars, false)
	if errors.Is(err, codec.ErrPlainBytes) {
		msg = append(msg, "; send string(b) for text or RawJSON(b) for JSON"...)
	}
	return newInvalidRequestError(string(msg), err)
}
