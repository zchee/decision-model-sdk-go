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

package provider

import (
	"encoding/json/jsontext"
	"errors"
	"net/http"
	"strconv"

	decision "github.com/zchee/decision-model-sdk-go"
)

// DecisionsAPI returns the provider of Decisions API:
// https://decisions-api.dev, POST /v1/systemone, with the key read from
// DECISIONS_API_KEY. It is not decisionapi.net, a vendor of its own whose
// key goes by DECISIONAPI_API_KEY, which this preset never reads.
//
// It takes the System One request unchanged and answers inside an envelope:
// {"code":0,"message":"ok","data":{"requestId":...,"result":{...},...}},
// where data.result is the System One response. The preset's [decision.Dialect]
// hands the decoder data.result and drops the rest of the envelope, so a
// response's [decision.ResponseMeta.RawBody] is data.result, and a 2xx whose
// code is not 0 or that has no data.result is a
// [*decision.ResponseValidationError] at "code" or "data.result". Its
// errors are {"code":-1,"message":...} with the HTTP status; an
// [*decision.APIError] keeps that body, takes its Message from "message",
// and has the ErrorType authentication_error for 401 and api_usage_error
// for 400 (an unknown model, whose message lists the models it accepts).
//
// It sends x-request-id on a success only, so a response's RequestID holds
// the id and an APIError's reports none; the envelope's data.requestId, the
// same id, is dropped with the envelope.
//
// It refuses, with status 400, a yes/no question that describes its Yes
// outcome without its No outcome, with a message asking for both. It has no
// listing, so ModelsPath is empty and [decision.Models.List] on its client
// fails with a [*decision.ConfigError] before anything is sent; it names
// models as vendor/model, such as typesafe/jev-1.13, and also takes
// jev-latest. It ignores a top-level member it does not know, such as one
// [decision.ExtraBody] adds.
func DecisionsAPI() decision.Provider {
	return decision.Provider{
		Name:          "Decisions API",
		BaseURL:       "https://decisions-api.dev",
		SystemOnePath: "/v1/systemone",
		APIKeyEnv:     "DECISIONS_API_KEY",
		Dialect:       envelopeDialect{},
	}
}

// envelopeDialect is Decisions API's [decision.Dialect]: the request is
// System One's, and the response's System One body is the data.result
// member of an envelope.
type envelopeDialect struct{}

// Request sends body as it is and reads each response with unwrapEnvelope,
// which needs nothing of the call.
func (envelopeDialect) Request([]byte, []decision.PreparedQuestion) ([]byte, decision.ResponseFunc, error) {
	return nil, unwrapEnvelope, nil
}

// unwrapEnvelope translates one response of Decisions API: a 2xx body to
// its data.result, a slice of body with no copy; any other status to the
// System One error_type of the status, with the body kept.
func unwrapEnvelope(status int, _ http.Header, body []byte) (decision.Translated, error) {
	if !success(status) {
		return decision.Translated{ErrorType: errorType(status, http.StatusBadRequest)}, nil
	}
	result, err := envelopeResult(body)
	if err != nil {
		return decision.Translated{}, err
	}
	return decision.Translated{Body: result}, nil
}

// envelopeResult returns the data.result object of body, the slice of body
// that holds it, after checking that code is 0. The decoder hands back each
// value's bytes as they are in body, so the value ends where the decoder's
// offset stands after reading it.
func envelopeResult(body []byte) ([]byte, error) {
	dec := newDecoder(body)
	var (
		code   jsontext.Value
		result []byte
	)
	err := members(dec, "", func(name string) error {
		switch name {
		case "code":
			v, err := readValue(dec)
			if err != nil {
				return err
			}
			code = v
		case "data":
			result = nil
			return members(dec, "data", func(name string) error {
				if name != "result" {
					return bodyError(dec.SkipValue())
				}
				if dec.PeekKind() != '{' && dec.PeekKind() != 0 {
					return pathError("data.result", errNotObject)
				}
				v, err := dec.ReadValue()
				if err != nil {
					return bodyError(err)
				}
				end := dec.InputOffset()
				start := end - int64(len(v))
				result = body[start:end:end]
				return nil
			})
		default:
			return bodyError(dec.SkipValue())
		}
		return nil
	})
	if err == nil {
		err = atEnd(dec)
	}
	if err != nil {
		return nil, err
	}
	switch {
	case code == nil:
		return nil, pathError("code", errMissing)
	case !isZero(code):
		return nil, pathError("code", errNotZero)
	case result == nil:
		return nil, pathError("data.result", errMissing)
	}
	return result, nil
}

// errNotZero is the failure of a 2xx envelope whose code is not 0.
var errNotZero = errors.New("not 0: the envelope reports a failure")

// isZero reports whether v is the number 0, in any spelling.
func isZero(v jsontext.Value) bool {
	if v.Kind() != '0' {
		return false
	}
	f, err := strconv.ParseFloat(string(v), 64)
	return err == nil && f == 0
}
