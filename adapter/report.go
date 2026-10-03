// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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

package adapter

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	decision "github.com/zchee/decision-model-sdk-go"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// Report is the Adapter's accounting and diagnostics for one call:
// upstream's usage and debug data (system-one-adapter-python v0.2.1,
// src/system_one_adapter/_response.py and the debug dictionary of
// _client.py:279-284,317-320). A call that fails after its evaluation
// started has a Report too, upstream's error.debug, with the accounting
// made until the failure.
type Report struct {
	// Usage is the call's token accounting and retry counts.
	Usage Usage
	// Debug is the call's probability diagnostics and attempt traces.
	Debug Debug
}

// Usage is upstream's Usage (_response.py:15-22): the last attempt's
// counts and the call's totals.
type Usage struct {
	// InputTokens is input_tokens: the input tokens of the last attempt
	// that returned a result; unknown when its provider did not report
	// them, or when no attempt returned a result.
	InputTokens llm.Count
	// OutputTokens is output_tokens, as InputTokens.
	OutputTokens llm.Count
	// InputTokensTotal is input_tokens_total: the input tokens of every
	// attempt that returned a result, unknown once one of them did not
	// report its count (_client.py:192-200).
	InputTokensTotal llm.Count
	// OutputTokensTotal is output_tokens_total, as InputTokensTotal.
	OutputTokensTotal llm.Count
	// Retries is n_retries: the provider requests repeated after a
	// transient failure.
	Retries int
	// MalformedRetries is n_retries_malformed_structure: the corrective
	// requests made after an answer that did not match the schema.
	MalformedRetries int
	// Latency is latency, the time from the start of the evaluation to its
	// answer or its failure, written as seconds; MarshalJSON writes any
	// Duration. While its size is below 2^22 seconds (48 days 13 hours 31
	// minutes 44 seconds), either side of 0, UnmarshalJSON reads the
	// seconds back to the same Duration. Beyond that a reader gets a
	// Duration that differs by less than |Latency|*2^-51 (under 15 ns for
	// a year), except that a Latency of 2^63-512 ns or more,
	// the last 512 ns of a Duration's range, is written as 2^63 ns, which
	// UnmarshalJSON refuses as no Duration.
	Latency time.Duration
}

// Debug is upstream's debug dictionary as a typed struct.
type Debug struct {
	// MaxError is max_error: the largest distance of a distribution's sum
	// from 1 over the questions that have one, 0 when none has.
	MaxError float64
	// InvalidProbs is invalid_probs: the number of distributions whose
	// sum is farther from 1 than the tolerance, 1e-6.
	InvalidProbs int
	// ProbabilityErrors is probability_errors, in question order: the
	// distance of each such distribution's sum from 1.
	ProbabilityErrors []QuestionValue
	// OriginalProbabilities is original_probabilities, in question order:
	// each distribution as the model gave it, for those the Adapter
	// rescaled. The member is written only when the slice is not empty.
	OriginalProbabilities []QuestionDistribution
	// Attempts is llm_attempts: every provider request, in order,
	// including those that failed.
	Attempts []Attempt
	// RetryReasons is retry_reasons, written as [category, msg] pairs, in
	// the order the retries were decided.
	RetryReasons []RetryReason
	// SDKRetryCount is sdk_retry_count, a member upstream does not have:
	// the root SDK's X-TypeSafe-Retry-Count of the request this Report
	// answers. 0 means the member is absent (a first attempt), so the JSON
	// keeps upstream's shape.
	SDKRetryCount int
}

// QuestionValue is one question's number.
type QuestionValue struct {
	Question string
	Value    float64
}

// QuestionDistribution is one question's distribution, in label order.
type QuestionDistribution struct {
	Question      string
	Probabilities []LabelValue
}

// LabelValue is one label's probability.
type LabelValue struct {
	Label string
	Value float64
}

// Attempt is one provider request, including one that failed: an element
// of upstream's llm_attempts (src/system_one_adapter/providers/base.py:126-167).
type Attempt struct {
	// Messages is messages: the conversation the request sent.
	Messages []llm.Message
	// Schema is model_request_parameters.schema: the answer schema as
	// compact JSON in pydantic's order. It is nil (written as null) or one
	// JSON value that Response's rule accepts; MarshalJSON refuses any
	// other.
	Schema []byte
	// Structured is model_request_parameters.structured.
	Structured bool
	// Response is llm_response: the provider's response body as it
	// recorded it with llm.Trace.RecordResponse; for a provider that
	// recorded none but returned a result, the JSON object
	// {"text": …, "input_tokens": …, "output_tokens": …} with null for an
	// unknown count, upstream's asdict(result) (_client.py:236-237); nil,
	// written as null, when the request failed before a response.
	//
	// A body is written as JSON when it is one JSON value in UTF-8 with no
	// raw control character, no token JSON does not have (such as NaN), no
	// member name twice in one object, no integer of more than 4300 digits
	// and at most 1000 levels of arrays and objects. Any other body, and a
	// body whose Info.ResponseEncoding is "text", is written as a JSON
	// string of its text, each byte sequence that is not UTF-8 replaced
	// by U+FFFD, and debug_info gains llm_response_encoding "text".
	Response []byte
	// Info is debug_info.
	Info AttemptInfo
	// Request is request: the body the provider built, as it recorded it
	// with llm.Trace.RecordRequest, written by Response's rule with
	// request_encoding; nil when none was recorded, and then the member
	// is absent.
	Request []byte
}

// AttemptInfo is an attempt's debug_info.
type AttemptInfo struct {
	// ModelName is model_name: the provider's model.
	ModelName string
	// Provider is provider: the Go type of the provider, such as
	// "github.com/zchee/decision-model-sdk-go/adapter/openai.Provider".
	Provider string
	// API is api: the provider's API, such as "responses",
	// "chat_completions", "messages" or "interactions"; empty means absent,
	// as when the provider recorded no request.
	API string
	// Responded reports that a response was recorded, so that
	// finish_reason is written.
	Responded bool
	// FinishReason is finish_reason; nil is null.
	FinishReason *string
	// Error is error: the text of the request's failure. Error and
	// ErrorType are written together, when either is not empty.
	Error string
	// ErrorType is error_type: upstream's class name of the failure, such
	// as "TypeSafeInternalServerError".
	ErrorType string
	// ResponseEncoding is llm_response_encoding, a member upstream does not
	// have: "text" when Response is written as a JSON string of its text;
	// empty means absent. MarshalJSON writes "text" also when it writes the
	// body as text because the body is not one it embeds.
	ResponseEncoding string
	// RequestEncoding is request_encoding, a member upstream does not
	// have: as ResponseEncoding, for Request.
	RequestEncoding string
}

// RetryReason is one retry's cause, upstream's RetryReasons
// (src/system_one_adapter/_utils/error_handling.py:30-40).
type RetryReason struct {
	// Category is the mechanism that asked for another attempt:
	// "provider_error" for a transient provider failure, or
	// "malformed_structure" for a corrective retry after malformed output.
	Category string
	// Message is the cause, upstream's msg, str() of the failed attempt's
	// error: for a provider timeout, connection failure or status error
	// the text of that typed error, also when the provider wrapped it.
	Message string
}

// encodingText is the one value of the encoding members.
const encodingText = "text"

// The limits of a body written as JSON (Attempt.Response).
const (
	// maxEmbedDepth is the deepest nesting of arrays and objects in a body
	// written as JSON.
	maxEmbedDepth = 1000
	// maxIntDigits is the most digits of an integer in a body written as
	// JSON, the sign not counted: Python's json.loads, and so jsonx.Read,
	// refuses more.
	maxIntDigits = 4300
)

// errReport is the error of a Report that cannot be read or written.
var errReport = errors.New("adapter: report")

// MarshalJSON writes {"usage": …, "debug": …} with upstream's member names
// and order. It fails for a float that is NaN or an infinity, an
// Attempt.Schema that is not one JSON value, an
// encoding member other than "" and "text", and two questions, or two
// labels of one question, whose names are one name once each byte that
// is not valid UTF-8 is written as U+FFFD (a JSON object cannot hold a
// name twice; the Adapter's own Reports never have such names, because
// the question names and labels come from the request's JSON).
func (r Report) MarshalJSON() ([]byte, error) {
	members, err := r.members()
	if err != nil {
		return nil, err
	}
	return jsonx.Marshal(jsonx.Object(members...))
}

// members returns the members usage and debug, in that order, for a body
// that holds them among others.
func (r *Report) members() ([]jsonx.Member, error) {
	usage := r.Usage.value()
	debug, err := r.Debug.value()
	if err != nil {
		return nil, err
	}
	return []jsonx.Member{{Name: "usage", Value: usage}, {Name: "debug", Value: debug}}, nil
}

// value returns u in the member order of upstream's Usage
// (_response.py:15-22), the SDK's two counts first.
func (u *Usage) value() jsonx.Value {
	return jsonx.Object(
		jsonx.Member{Name: "input_tokens", Value: countValue(u.InputTokens)},
		jsonx.Member{Name: "output_tokens", Value: countValue(u.OutputTokens)},
		jsonx.Member{Name: "input_tokens_total", Value: countValue(u.InputTokensTotal)},
		jsonx.Member{Name: "output_tokens_total", Value: countValue(u.OutputTokensTotal)},
		jsonx.Member{Name: "n_retries", Value: intValue(u.Retries)},
		jsonx.Member{Name: "n_retries_malformed_structure", Value: intValue(u.MalformedRetries)},
		jsonx.Member{Name: "latency", Value: jsonx.Float(float64(u.Latency)/1e9, jsonx.Repr)},
	)
}

// value returns d in upstream's member order
// (_utils/probability_normalization.py:47-54, _client.py:279-284), with
// sdk_retry_count last.
func (d *Debug) value() (jsonx.Value, error) {
	errs := make([]jsonx.Member, len(d.ProbabilityErrors))
	for i, e := range d.ProbabilityErrors {
		errs[i] = jsonx.Member{Name: e.Question, Value: jsonx.Float(e.Value, jsonx.Repr)}
	}
	members := []jsonx.Member{
		{Name: "max_error", Value: jsonx.Float(d.MaxError, jsonx.Repr)},
		{Name: "invalid_probs", Value: intValue(d.InvalidProbs)},
		{Name: "probability_errors", Value: jsonx.Object(errs...)},
	}
	if len(d.OriginalProbabilities) > 0 {
		dists := make([]jsonx.Member, len(d.OriginalProbabilities))
		for i, q := range d.OriginalProbabilities {
			labels := make([]jsonx.Member, len(q.Probabilities))
			for j, l := range q.Probabilities {
				labels[j] = jsonx.Member{Name: l.Label, Value: jsonx.Float(l.Value, jsonx.Repr)}
			}
			dists[i] = jsonx.Member{Name: q.Question, Value: jsonx.Object(labels...)}
		}
		members = append(members, jsonx.Member{Name: "original_probabilities", Value: jsonx.Object(dists...)})
	}
	attempts := make([]jsonx.Value, len(d.Attempts))
	for i := range d.Attempts {
		a, err := d.Attempts[i].value()
		if err != nil {
			return jsonx.Value{}, fmt.Errorf("llm_attempts[%d]: %w", i, err)
		}
		attempts[i] = a
	}
	reasons := make([]jsonx.Value, len(d.RetryReasons))
	for i, r := range d.RetryReasons {
		reasons[i] = jsonx.Array(jsonx.String(r.Category), jsonx.String(r.Message))
	}
	members = append(members,
		jsonx.Member{Name: "llm_attempts", Value: jsonx.Array(attempts...)},
		jsonx.Member{Name: "retry_reasons", Value: jsonx.Array(reasons...)},
	)
	if d.SDKRetryCount != 0 {
		members = append(members, jsonx.Member{Name: "sdk_retry_count", Value: intValue(d.SDKRetryCount)})
	}
	return jsonx.Object(members...), nil
}

// value returns a in upstream's member order (providers/base.py:136-167),
// with the two encoding members last in debug_info.
func (a *Attempt) value() (jsonx.Value, error) {
	messages := make([]jsonx.Value, len(a.Messages))
	for i, m := range a.Messages {
		messages[i] = jsonx.Object(
			jsonx.Member{Name: "role", Value: jsonx.String(string(m.Role))},
			jsonx.Member{Name: "content", Value: jsonx.String(m.Content)},
		)
	}
	schema := jsonx.Value{}
	if a.Schema != nil {
		if !embeddable(a.Schema) {
			return jsonx.Value{}, fmt.Errorf("%w: the schema is not one JSON value", errReport)
		}
		schema = jsonx.Raw(a.Schema)
	}
	response, responseText, err := bodyValue(a.Response, a.Info.ResponseEncoding)
	if err != nil {
		return jsonx.Value{}, fmt.Errorf("llm_response: %w", err)
	}
	request, requestText, err := bodyValue(a.Request, a.Info.RequestEncoding)
	if err != nil {
		return jsonx.Value{}, fmt.Errorf("request: %w", err)
	}
	info := []jsonx.Member{
		{Name: "model_name", Value: jsonx.String(a.Info.ModelName)},
		{Name: "provider", Value: jsonx.String(a.Info.Provider)},
	}
	if a.Info.API != "" {
		info = append(info, jsonx.Member{Name: "api", Value: jsonx.String(a.Info.API)})
	}
	if a.Info.Responded {
		finish := jsonx.Value{}
		if a.Info.FinishReason != nil {
			finish = jsonx.String(*a.Info.FinishReason)
		}
		info = append(info, jsonx.Member{Name: "finish_reason", Value: finish})
	}
	if a.Info.Error != "" || a.Info.ErrorType != "" {
		info = append(info,
			jsonx.Member{Name: "error", Value: jsonx.String(a.Info.Error)},
			jsonx.Member{Name: "error_type", Value: jsonx.String(a.Info.ErrorType)},
		)
	}
	if responseText {
		info = append(info, jsonx.Member{Name: "llm_response_encoding", Value: jsonx.String(encodingText)})
	}
	if requestText && a.Request != nil {
		info = append(info, jsonx.Member{Name: "request_encoding", Value: jsonx.String(encodingText)})
	}
	members := []jsonx.Member{
		{Name: "messages", Value: jsonx.Array(messages...)},
		{Name: "model_request_parameters", Value: jsonx.Object(
			jsonx.Member{Name: "schema", Value: schema},
			jsonx.Member{Name: "structured", Value: jsonx.Bool(a.Structured)},
		)},
		{Name: "llm_response", Value: response},
		{Name: "debug_info", Value: jsonx.Object(info...)},
	}
	if a.Request != nil {
		members = append(members, jsonx.Member{Name: "request", Value: request})
	}
	return jsonx.Object(members...), nil
}

// bodyValue returns how body is written (null for nil, JSON when
// embeddable, else a string of its text) and whether it is the text form.
func bodyValue(body []byte, encoding string) (jsonx.Value, bool, error) {
	switch {
	case encoding != "" && encoding != encodingText:
		return jsonx.Value{}, false, fmt.Errorf("%w: encoding %q is not %q", errReport, encoding, encodingText)
	case body == nil:
		return jsonx.Value{}, false, nil
	case encoding == "" && embeddable(body):
		return jsonx.Raw(body), false, nil
	}
	return jsonx.String(string(body)), true, nil
}

// embeddable reports whether body may be written as JSON inside a Report:
// one JSON value in UTF-8 that the JSON library writes as it is (no raw
// control character, no token JSON does not have, no member name twice in
// one object), with no integer of more than maxIntDigits digits, which
// jsonx.Read refuses, and at most maxEmbedDepth levels of arrays and
// objects.
func embeddable(body []byte) bool {
	toks := jsonx.NewTokens(body)
	// names holds, per open container, the member names seen; nil for an
	// array.
	var names []map[string]struct{}
	for {
		tok, err := toks.Next()
		if err != nil {
			return false
		}
		switch tok.Kind {
		case jsonx.TokenEnd:
			return true
		case jsonx.TokenBeginArray:
			if tok.Depth >= maxEmbedDepth {
				return false
			}
			names = append(names, nil)
		case jsonx.TokenBeginObject:
			if tok.Depth >= maxEmbedDepth {
				return false
			}
			names = append(names, map[string]struct{}{})
		case jsonx.TokenEndArray, jsonx.TokenEndObject:
			names = names[:len(names)-1]
		case jsonx.TokenName:
			seen := names[len(names)-1]
			if _, dup := seen[tok.Text]; dup {
				return false
			}
			seen[tok.Text] = struct{}{}
		case jsonx.TokenNumber:
			if !strings.ContainsAny(tok.Text, ".eE") && len(strings.TrimPrefix(tok.Text, "-")) > maxIntDigits {
				return false
			}
		}
	}
}

// countValue returns c as an integer, or null when it is unknown.
func countValue(c llm.Count) jsonx.Value {
	if !c.Known {
		return jsonx.Value{}
	}
	return jsonx.Number(strconv.FormatUint(c.N, 10))
}

// intValue returns n as a JSON integer.
func intValue(n int) jsonx.Value { return jsonx.Number(strconv.Itoa(n)) }

// UnmarshalJSON reads what MarshalJSON writes, and a whole Adapter
// response body, success ({"model", "usage", "answers", "debug"}) or error
// ({"detail", "usage", "debug"}): it reads the members usage and debug and
// ignores every other member, at every level. The latency and the members
// upstream does not always write (original_probabilities, sdk_retry_count,
// api, finish_reason, error, error_type, the encodings, request) may be
// absent. It fails when b is not one JSON text, when usage or debug is
// absent, or when a member it reads has the wrong kind.
func (r *Report) UnmarshalJSON(b []byte) error {
	root, err := jsonx.Read(b)
	if err != nil {
		return fmt.Errorf("%w: %w", errReport, err)
	}
	rd := reader{}
	usage := rd.member(root, "usage", jsonx.KindObject)
	debug := rd.member(root, "debug", jsonx.KindObject)
	var out Report
	if rd.err == nil {
		out.Usage = rd.usage(usage)
		out.Debug = rd.debug(debug)
	}
	if rd.err != nil {
		return rd.err
	}
	*r = out
	return nil
}

// reader reads a Report from a read body, keeping the first error and the
// path where it occurred.
type reader struct {
	path []string
	err  error
}

// fail records the first error, at the current path and name.
func (rd *reader) fail(name, format string, args ...any) {
	if rd.err == nil {
		rd.err = fmt.Errorf("%w: %s: %s", errReport, strings.Join(append(rd.path, name), "."), fmt.Sprintf(format, args...))
	}
}

// optional returns the member name of obj and whether it is present; a
// present member of another kind than want is an error. KindNull for want
// accepts any kind.
func (rd *reader) optional(obj jsonx.Node, name string, want jsonx.Kind) (jsonx.Node, bool) {
	v, ok := obj.Member(name)
	if !ok {
		return jsonx.Node{}, false
	}
	if want != jsonx.KindNull && v.Kind() != want {
		rd.fail(name, "kind %v, want %v", v.Kind(), want)
		return jsonx.Node{}, false
	}
	return v, true
}

// member returns the member name of obj, which must be present.
func (rd *reader) member(obj jsonx.Node, name string, want jsonx.Kind) jsonx.Node {
	v, ok := rd.optional(obj, name, want)
	if !ok {
		rd.fail(name, "absent or of the wrong kind")
	}
	return v
}

// in runs read with name added to the path.
func (rd *reader) in(name string, read func()) {
	rd.path = append(rd.path, name)
	read()
	rd.path = rd.path[:len(rd.path)-1]
}

// count reads an integer or null.
func (rd *reader) count(obj jsonx.Node, name string) llm.Count {
	v := rd.member(obj, name, jsonx.KindNull)
	if v.Kind() == jsonx.KindNull {
		return llm.Count{}
	}
	n, err := strconv.ParseUint(v.Text(), 10, 64)
	if err != nil || !v.IsInt() {
		rd.fail(name, "not a count: %v %q", v.Kind(), v.Text())
	}
	return llm.Count{N: n, Known: true}
}

// integer reads an integer that fits an int.
func (rd *reader) integer(v jsonx.Node, name string) int {
	n, err := strconv.Atoi(v.Text())
	if err != nil || v.Kind() != jsonx.KindNumber {
		rd.fail(name, "not an integer: %v %q", v.Kind(), v.Text())
	}
	return n
}

// float reads a number.
func (rd *reader) float(v jsonx.Node, name string) float64 {
	f, err := strconv.ParseFloat(v.Text(), 64)
	if err != nil || v.Kind() != jsonx.KindNumber {
		rd.fail(name, "not a finite number: %v %q", v.Kind(), v.Text())
	}
	return f
}

// text reads a string.
func (rd *reader) text(v jsonx.Node, name string) string {
	if v.Kind() != jsonx.KindString {
		rd.fail(name, "kind %v, want a string", v.Kind())
	}
	return v.Text()
}

// usage reads the usage object.
func (rd *reader) usage(v jsonx.Node) (u Usage) {
	rd.in("usage", func() {
		u.InputTokens = rd.count(v, "input_tokens")
		u.OutputTokens = rd.count(v, "output_tokens")
		u.InputTokensTotal = rd.count(v, "input_tokens_total")
		u.OutputTokensTotal = rd.count(v, "output_tokens_total")
		u.Retries = rd.integer(rd.member(v, "n_retries", jsonx.KindNumber), "n_retries")
		u.MalformedRetries = rd.integer(rd.member(v, "n_retries_malformed_structure", jsonx.KindNumber), "n_retries_malformed_structure")
		if l, ok := rd.optional(v, "latency", jsonx.KindNumber); ok {
			s := rd.float(l, "latency")
			if ns := math.Round(s * 1e9); ns >= -(1<<63) && ns < 1<<63 {
				u.Latency = time.Duration(ns)
			} else {
				rd.fail("latency", "%v seconds is out of a Duration's range", s)
			}
		}
	})
	return u
}

// debug reads the debug object.
func (rd *reader) debug(v jsonx.Node) (d Debug) {
	rd.in("debug", func() {
		d.MaxError = rd.float(rd.member(v, "max_error", jsonx.KindNumber), "max_error")
		d.InvalidProbs = rd.integer(rd.member(v, "invalid_probs", jsonx.KindNumber), "invalid_probs")
		errs := rd.member(v, "probability_errors", jsonx.KindObject)
		for i := range errs.Len() {
			d.ProbabilityErrors = append(d.ProbabilityErrors, QuestionValue{Question: errs.Name(i), Value: rd.float(errs.Index(i), errs.Name(i))})
		}
		if dists, ok := rd.optional(v, "original_probabilities", jsonx.KindObject); ok {
			rd.in("original_probabilities", func() {
				for i := range dists.Len() {
					q := QuestionDistribution{Question: dists.Name(i)}
					labels := dists.Index(i)
					if labels.Kind() != jsonx.KindObject {
						rd.fail(q.Question, "kind %v, want an object", labels.Kind())
						return
					}
					for j := range labels.Len() {
						q.Probabilities = append(q.Probabilities, LabelValue{Label: labels.Name(j), Value: rd.float(labels.Index(j), labels.Name(j))})
					}
					d.OriginalProbabilities = append(d.OriginalProbabilities, q)
				}
			})
		}
		attempts := rd.member(v, "llm_attempts", jsonx.KindArray)
		for i := range attempts.Len() {
			rd.in("llm_attempts["+strconv.Itoa(i)+"]", func() {
				d.Attempts = append(d.Attempts, rd.attempt(attempts.Index(i)))
			})
		}
		reasons := rd.member(v, "retry_reasons", jsonx.KindArray)
		for i := range reasons.Len() {
			pair := reasons.Index(i)
			name := "retry_reasons[" + strconv.Itoa(i) + "]"
			if pair.Kind() != jsonx.KindArray || pair.Len() != 2 {
				rd.fail(name, "not a [category, msg] pair")
				continue
			}
			d.RetryReasons = append(d.RetryReasons, RetryReason{Category: rd.text(pair.Index(0), name), Message: rd.text(pair.Index(1), name)})
		}
		if n, ok := rd.optional(v, "sdk_retry_count", jsonx.KindNumber); ok {
			d.SDKRetryCount = rd.integer(n, "sdk_retry_count")
		}
	})
	return d
}

// attempt reads one element of llm_attempts.
func (rd *reader) attempt(v jsonx.Node) (a Attempt) {
	if v.Kind() != jsonx.KindObject {
		rd.fail("", "kind %v, want an object", v.Kind())
		return a
	}
	messages := rd.member(v, "messages", jsonx.KindArray)
	for i := range messages.Len() {
		m := messages.Index(i)
		name := "messages[" + strconv.Itoa(i) + "]"
		if m.Kind() != jsonx.KindObject {
			rd.fail(name, "kind %v, want an object", m.Kind())
			return a
		}
		rd.in(name, func() {
			a.Messages = append(a.Messages, llm.Message{
				Role:    llm.Role(rd.text(rd.member(m, "role", jsonx.KindString), "role")),
				Content: rd.text(rd.member(m, "content", jsonx.KindString), "content"),
			})
		})
	}
	params := rd.member(v, "model_request_parameters", jsonx.KindObject)
	rd.in("model_request_parameters", func() {
		a.Schema = rawBytes(rd.member(params, "schema", jsonx.KindNull))
		switch s := rd.member(params, "structured", jsonx.KindNull); s.Kind() {
		case jsonx.KindTrue:
			a.Structured = true
		case jsonx.KindFalse:
		default:
			rd.fail("structured", "kind %v, want a bool", s.Kind())
		}
	})
	info := rd.member(v, "debug_info", jsonx.KindObject)
	rd.in("debug_info", func() {
		a.Info.ModelName = rd.text(rd.member(info, "model_name", jsonx.KindString), "model_name")
		a.Info.Provider = rd.text(rd.member(info, "provider", jsonx.KindString), "provider")
		if api, ok := rd.optional(info, "api", jsonx.KindString); ok {
			a.Info.API = api.Text()
		}
		if f, ok := rd.optional(info, "finish_reason", jsonx.KindNull); ok {
			a.Info.Responded = true
			if f.Kind() != jsonx.KindNull {
				s := rd.text(f, "finish_reason")
				a.Info.FinishReason = &s
			}
		}
		if e, ok := rd.optional(info, "error", jsonx.KindString); ok {
			a.Info.Error = e.Text()
		}
		if e, ok := rd.optional(info, "error_type", jsonx.KindString); ok {
			a.Info.ErrorType = e.Text()
		}
		a.Info.ResponseEncoding = rd.encoding(info, "llm_response_encoding")
		a.Info.RequestEncoding = rd.encoding(info, "request_encoding")
	})
	a.Response = rd.body(rd.member(v, "llm_response", jsonx.KindNull), "llm_response", a.Info.ResponseEncoding)
	if req, ok := rd.optional(v, "request", jsonx.KindNull); ok {
		a.Request = rd.body(req, "request", a.Info.RequestEncoding)
		if a.Request == nil {
			// A request member that is null is a recorded body "null".
			a.Request = []byte("null")
		}
	}
	return a
}

// encoding reads an encoding member, "" when absent.
func (rd *reader) encoding(info jsonx.Node, name string) string {
	e, ok := rd.optional(info, name, jsonx.KindString)
	if !ok {
		return ""
	}
	if e.Text() != encodingText {
		rd.fail(name, "%q, want %q", e.Text(), encodingText)
	}
	return e.Text()
}

// body reads a body written by bodyValue: the text of a string in the text
// form, else the value as compact JSON, nil for null.
func (rd *reader) body(v jsonx.Node, name, encoding string) []byte {
	if encoding == encodingText {
		return []byte(rd.text(v, name))
	}
	return rawBytes(v)
}

// rawBytes returns v as compact JSON with its numbers spelled as they are,
// the bytes jsonx.Raw writes for the body v was read from; nil for null.
func rawBytes(v jsonx.Node) []byte {
	if v.Kind() == jsonx.KindNull {
		return nil
	}
	// A value jsonx.Read made is valid UTF-8 with valid numbers, no
	// repeated name and at most 10000 levels, so Marshal cannot refuse it.
	b, _ := jsonx.Marshal(rawValue(v))
	return b
}

// rawValue returns v as a Value that writes its numbers as v spells them.
func rawValue(v jsonx.Node) jsonx.Value {
	switch v.Kind() {
	case jsonx.KindFalse:
		return jsonx.Bool(false)
	case jsonx.KindTrue:
		return jsonx.Bool(true)
	case jsonx.KindNumber:
		return jsonx.Number(v.Text())
	case jsonx.KindString:
		return jsonx.String(v.Text())
	case jsonx.KindArray:
		elems := make([]jsonx.Value, v.Len())
		for i := range elems {
			elems[i] = rawValue(v.Index(i))
		}
		return jsonx.Array(elems...)
	case jsonx.KindObject:
		members := make([]jsonx.Member, v.Len())
		for i := range members {
			members[i] = jsonx.Member{Name: v.Name(i), Value: rawValue(v.Index(i))}
		}
		return jsonx.Object(members...)
	}
	return jsonx.Value{}
}

// reportCarrier is an error that carries the Report of the call it ended,
// such as the Adapter's own error type; a nil Report means the call ended
// before an evaluation started.
type reportCarrier interface {
	error
	adapterReport() *Report
}

// ReportOf returns the Report in resp's body (ResponseMeta.RawBody): the
// usage and debug of an answer the Adapter gave. It fails when the body
// holds no Report, as a body from another server does not.
func ReportOf(resp *decision.SystemOneResponse) (*Report, error) {
	if resp == nil {
		return nil, fmt.Errorf("%w: no response", errReport)
	}
	r := new(Report)
	if err := r.UnmarshalJSON(resp.Meta().RawBody()); err != nil {
		return nil, err
	}
	return r, nil
}

// ReportFromError returns the Report carried by an error a call returned:
// the Adapter's own error in its chain, or the body of a *decision.APIError
// or *decision.ResponseValidationError that the Adapter produced, upstream's
// error.debug with the call's usage. It returns false for an error that
// carries none: one that ended the call before an evaluation started (a
// request the Adapter refused, a closed Adapter, a cancelled context), and
// one whose body the SDK did not keep, such as an error body over the
// client's response size limit.
//
// On a client that retries, it returns false when an earlier attempt failed
// with an error the SDK's policy retries (a status in 408, 429 or 500-599, a
// timeout, or a connection failure) and the call's context deadline then
// ended the SDK's wait before the next attempt: the SDK then returns a new
// *decision.TimeoutError that holds no earlier attempt's error. A client
// from NewClient does not retry, so this does not occur there.
func ReportFromError(err error) (*Report, bool) {
	if carrier, ok := errors.AsType[reportCarrier](err); ok {
		if r := carrier.adapterReport(); r != nil {
			return r, true
		}
	}
	if apiErr, ok := errors.AsType[*decision.APIError](err); ok {
		if r, ok := reportFromBody(apiErr.Body); ok {
			return r, true
		}
	}
	if invalid, ok := errors.AsType[*decision.ResponseValidationError](err); ok {
		if r, ok := reportFromBody(invalid.Body); ok {
			return r, true
		}
	}
	return nil, false
}

// reportFromBody reads the Report of an Adapter body, false when the body
// holds none.
func reportFromBody(body []byte) (*Report, bool) {
	r := new(Report)
	if err := r.UnmarshalJSON(body); err != nil {
		return nil, false
	}
	return r, true
}
