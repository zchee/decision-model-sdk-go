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
	"net/http"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// Usage is the token usage a response reported. The API may leave either
// count out, which is not the same as a count of zero; a count cannot be
// negative, and a response that reports one is refused.
type Usage struct {
	w wire.Usage
}

// InputTokens returns the number of billable input tokens, and whether the
// response reported it.
func (u Usage) InputTokens() (uint64, bool) { return u.w.InputTokens, u.w.HasInputTokens }

// OutputTokens returns the number of output tokens, and whether the response
// reported it.
func (u Usage) OutputTokens() (uint64, bool) { return u.w.OutputTokens, u.w.HasOutputTokens }

// MarshalJSON returns the usage as the Python SDK's model_dump_json writes
// it, such as {"input_tokens":12,"output_tokens":3}, a count the response
// left out as null.
func (u Usage) MarshalJSON() ([]byte, error) { return wire.AppendUsage(nil, &u.w), nil }

// ResponseMeta is the HTTP side of a response, the Python SDK's
// raw_http_response: the status, the header and the body exactly as they
// arrived. The header and the body are shared with the response, not
// copied, and must not be modified. A response that did not come from a
// request, such as one read back from JSON, has an empty ResponseMeta.
//
// It is HTTP metadata, not part of the response's payload: a response's
// MarshalJSON leaves it out, and it has no MarshalJSON of its own.
type ResponseMeta struct {
	m wire.ResponseMeta
}

// StatusCode returns the HTTP status code, or zero when there is no HTTP
// response.
func (m ResponseMeta) StatusCode() int { return m.m.Status }

// Header returns the response header, or nil when there is no HTTP
// response.
func (m ResponseMeta) Header() http.Header { return m.m.Header }

// RawBody returns the body exactly as it was received, or nil when there is
// no HTTP response. It holds what the decoded response leaves out: members
// and answer types this version does not model.
func (m ResponseMeta) RawBody() []byte { return m.m.Body }

// RequestID returns the server's identifier for the request, from the
// x-typesafe-request-id response header, and whether the header was
// present; a repeated header's values are joined with ", ". It is the
// server's text as it arrived. The Python SDK raises where this reports
// false.
func (m ResponseMeta) RequestID() (string, bool) { return m.m.RequestID() }

// SystemOneResponse is the response to a System One call: the model that
// answered, the token usage and the answers, with the HTTP response they
// came in. It is immutable. See System One
// (https://docs.typesafe.ai/concepts/system-one).
type SystemOneResponse struct {
	res  wire.SystemOneResult
	meta wire.ResponseMeta
}

// Model returns the model that answered.
func (r *SystemOneResponse) Model() string { return r.res.Model }

// Usage returns the token usage of the request.
func (r *SystemOneResponse) Usage() Usage { return Usage{r.res.Usage} }

// Answers returns the answers, keyed by question name. The view is valid as
// long as r is.
func (r *SystemOneResponse) Answers() Answers { return Answers{&r.res.Answers} }

// Meta returns the HTTP response the answers came in.
func (r *SystemOneResponse) Meta() ResponseMeta { return ResponseMeta{r.meta} }

// MarshalJSON returns the response's payload, the object
// {"model":…,"usage":{…},"answers":{…}}, as the Python SDK's
// model_dump_json writes it: without the HTTP response (Meta) and without
// any view of the answers. Floats are spelled as Python spells them (0.0,
// 0.98, 1e-7, 1e+20), a token count the response left out is null, and the
// answers come in the order of [Answers.All]. A structured legend level is
// written as the bytes the response carried ([ScoreAnswer.Description]),
// where the Python SDK writes the value it parsed; the two differ only when
// those bytes hold an escape, whitespace or a number spelled otherwise than
// Python spells it. Every other byte is the Python SDK's.
//
// The receiver is a value, so a SystemOneResponse marshals the same whether
// it is held by pointer or by value. [SystemOneResponse.UnmarshalJSON] reads
// the payload back.
func (r SystemOneResponse) MarshalJSON() ([]byte, error) {
	return wire.AppendSystemOneResult(nil, &r.res)
}

// UnmarshalJSON sets *r to the response whose payload is data, such as one
// [SystemOneResponse.MarshalJSON] returned. data is read as the body of a
// System One response is, with the same checks and the same rules for a
// repeated member; an answer of a type this version does not model is left
// out without a log line. The response did not come from a request, so its
// Meta is empty, and it keeps no reference to data.
//
// A payload the decoder refuses leaves *r unchanged and fails with a
// [*ResponseValidationError] whose FieldPath names the first failure, whose
// StatusCode is zero, and whose Header, Body and Endpoint are empty. As
// json.Unmarshaler implementations do by convention, the JSON literal null
// leaves *r unchanged.
func (r *SystemOneResponse) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	if _, err := codec.DecodeSystemOne(data, nil, "", &r.res); err != nil {
		return newResponseValidationError(&wire.ResponseMeta{}, "", err)
	}
	r.meta = wire.ResponseMeta{}
	return nil
}

// ModelCard describes one model the account can use.
type ModelCard struct {
	w wire.ModelCard
}

// Name returns the model name or alias that a request's model accepts.
func (m ModelCard) Name() string { return m.w.Name }

// Description returns the human-readable description of the model.
func (m ModelCard) Description() string { return m.w.Description }

// ReleaseDate returns the model's release date, formatted as YYYY-MM-DD.
func (m ModelCard) ReleaseDate() string { return m.w.ReleaseDate }

// MarshalJSON returns the card as the Python SDK's model_dump_json writes it,
// {"name":…,"description":…,"release_date":…}.
func (m ModelCard) MarshalJSON() ([]byte, error) { return wire.AppendModelCard(nil, &m.w) }

// ModelsResponse is the response to a list-models call: the models the
// account can use, with the HTTP response they came in. It is immutable.
type ModelsResponse struct {
	list wire.ModelList
	meta wire.ResponseMeta
}

// Models returns the models, in the order the response lists them. The
// slice is the caller's; the cards share the response's strings.
func (r *ModelsResponse) Models() []ModelCard {
	cards := make([]ModelCard, len(r.list.Models))
	for i, m := range r.list.Models {
		cards[i] = ModelCard{m}
	}
	return cards
}

// Meta returns the HTTP response the models came in.
func (r *ModelsResponse) Meta() ResponseMeta { return ResponseMeta{r.meta} }

// MarshalJSON returns the response's payload, the object
// {"models":[{"name":…,"description":…,"release_date":…},…]}, as the
// Python SDK's model_dump_json writes it, byte for byte: the cards in the
// order of Models, without the HTTP response (Meta).
//
// The receiver is a value, so a ModelsResponse marshals the same whether it
// is held by pointer or by value. [ModelsResponse.UnmarshalJSON] reads the
// payload back.
func (r ModelsResponse) MarshalJSON() ([]byte, error) {
	return wire.AppendModelList(nil, &r.list)
}

// UnmarshalJSON sets *r to the response whose payload is data, such as one
// [ModelsResponse.MarshalJSON] returned. data is read as the body of a
// list-models response is, with the same checks. The response did not come
// from a request, so its Meta is empty, and it keeps no reference to data.
//
// A payload the decoder refuses leaves *r unchanged and fails with a
// [*ResponseValidationError], as [SystemOneResponse.UnmarshalJSON] does. The
// JSON literal null leaves *r unchanged.
func (r *ModelsResponse) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	if err := codec.DecodeModels(data, &r.list); err != nil {
		return newResponseValidationError(&wire.ResponseMeta{}, "", err)
	}
	r.meta = wire.ResponseMeta{}
	return nil
}
