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
	"errors"
	"strconv"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/engine"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// bodyMember is one member a call adds to the top level of its request
// body, as the Python SDK's extra_body does ([engine.BodyMember]).
type bodyMember = engine.BodyMember

// encodeBody encodes the request body of one System One call into a pooled
// scratch buffer, with [engine.EncodeBody] instantiated with the root
// package's RawJSON and Content. It returns the body holding the call's
// reference, which the caller drops with Release when the call returns;
// [requestReaders] gives the transport its own references.
//
// The body is the JSON object {"state":…,"model":…,"questions":…} followed
// by the members of extra, laid out as the Python SDK's {**body,
// **extra_body}: an extra member named "state", "model" or "questions"
// replaces that member's value where it stands, and the value it replaces is
// not encoded at all; any other name is appended in the order extra first
// names it. When extra names a member more than once, the last value is
// written.
//
// The state, and an extra "state" too, must be text, a JSON object or an
// array: sonic writes it (codec.EncodeState); a [RawJSON] state, or the
// RawJSON a non-nil *RawJSON points to, is written as it is after
// codec.AppendRawState's check; [Content] is written as a question writes
// it. model is written as a JSON string and qs's bytes as
// they are. Any other value may be any JSON value: sonic writes it
// (codec.EncodeValue), a RawJSON value as it is after codec.AppendRawValue's
// check, Content as a question writes it and unset Content as null.
//
// A float inside the state and the extra values that sonic writes keeps
// sonic's spelling (rulings R46 and R59), a consequence of section 6.1.2
// encoding them with sonic and of the owner's decision D1, not a choice of
// this function: 3.0 is written 3, -0.0 as 0 on arm64 and as -0 on amd64
// (K27: sonic's amd64 JIT writes the sign, its arm64 VM does not), and
// 1e16 <= |x| < 1e21 and 1e-6 <= |x| < 1e-5 in fixed digits, where the
// Python SDK writes 3.0, -0.0 and e-notation. The same state can therefore
// be sent with different bytes from the two architectures, for a negative
// zero only. A value that needs an exact spelling is sent as RawJSON,
// or carries the number as a string. In the state and the extra values, a
// map's members go out in Go's iteration order, which changes from one call
// to the next where Python keeps a dict's insertion order (rulings R55 and
// R59); the body of one call, and so every attempt of it, is encoded once. A
// struct or RawJSON gives stable bytes.
//
// The output of a caller's json.Marshaler, a nested json.RawMessage among
// them, is the caller's contract, as a top-level RawJSON is (ruling R61):
// sonic's check of it does not refuse every invalid output (K26), while the
// SDK's own nested Content and RawJSON go through wire's scanner (R60), and
// the body is not scanned again as a whole.
//
// It fails with a [*ConfigError] when qs is nil or holds no question (a
// Prepared that [Questions.Prepare] did not return), or when model is not
// valid UTF-8, and with an [*InvalidRequestError] when a member cannot be
// encoded. The configuration is checked first.
func encodeBody(state any, model string, qs *Prepared, extra []bodyMember) (codec.Body, error) {
	var q *wire.Prepared
	if qs != nil {
		q = qs.wirePrepared()
	}
	body, f := engine.EncodeBody[RawJSON, Content](state, model, q, extra)
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
// quote what the caller passed, is escaped and cut at [engine.MaxMessageChars]
// (NF7, ruling R58), so the message never carries the state into a log; the
// whole cause stays behind Unwrap.
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
