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
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/schema"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// The paths the Adapter answers, matched as the suffix of the request's
// path, so that any base URL of the SDK's client works.
const (
	systemOnePath = "/v1/systemone"
	modelsPath    = "/v1/models"
)

// releaseDate is the release_date of every model card: the release date of
// system-one-adapter-python v0.2.1 (docs/changelog.md:8 of its repository).
// The SDK requires a string there and does not parse it.
const releaseDate = "2026-09-22"

// The bodies and texts of the Adapter's own refusals. Upstream has no wire,
// so these texts are the port's, except where a refusal carries upstream's
// ValueError text (model.go, and upstream's question messages).
var (
	// notFoundBody is the body of a request to a path the Adapter does not
	// answer.
	notFoundBody = []byte(`{"detail": "Not Found"}`)
	// encodingFailedBody is the body that replaces a response the Adapter
	// could not write as JSON that reads back.
	encodingFailedBody = []byte(`{"detail":{"message":"adapter: response encoding failed","error_type":"adapter_internal"}}`)
)

const (
	unreadableBodyText   = "The request body could not be read."
	notAnObjectText      = "The request body is not a JSON object."
	modelNotStringText   = "The request body's model is not a string."
	questionsNotObjText  = "The request body's questions is not a JSON object."
	stateNoneText        = "State must not be None." // upstream's text (_client.py:431-432)
	stateKindText        = "State must be a string, an object or an array."
	stateTooDeepText     = "State is nested too deeply to be written into the prompt."
	internalFailureText  = "adapter: the evaluation could not start"
	encodingFailedStatus = 424
)

// reply is what serve answers a request with: a status and a body, or an
// error that RoundTrip returns instead of a response.
type reply struct {
	status int
	body   []byte
	err    error
}

// request is the body of a System One request as the Adapter reads it.
type request struct {
	// model is the model string; "" when the member is absent.
	model string
	// state is the state member; hasState is false when it is absent.
	state    jsonx.Node
	hasState bool
	// questions is the questions member, an object.
	questions jsonx.Node
}

// serve answers one request of the root SDK, given the parts of the
// http.Request RoundTrip read: the method and the path, which route it;
// the context; the X-TypeSafe-Retry-Count value, which it records and
// refuses nothing for; and the body, read to its end, with the error of
// that read.
func (ad *Adapter) serve(ctx context.Context, method, path, retryCount string, body []byte, readErr error) reply {
	switch {
	case method == http.MethodGet && strings.HasSuffix(path, modelsPath):
		return ad.modelsReply()
	case method != http.MethodPost || !strings.HasSuffix(path, systemOnePath):
		return reply{status: http.StatusNotFound, body: notFoundBody}
	}
	sdkRetries := parseRetryCount(retryCount)
	if readErr != nil {
		return refusalReply(&refusal{status: 400, errorType: "invalid_body", message: unreadableBodyText})
	}
	req, err := parseRequest(body)
	if err != nil {
		return refusalReply(err)
	}
	t, err := ad.resolve(req.model)
	if err != nil {
		return refusalReply(err)
	}
	p, err := ad.provider(t)
	if err != nil {
		return refusalReply(&refusal{status: 400, errorType: "provider_config", message: err.Error()})
	}
	switch {
	case !req.hasState || req.state.Kind() == jsonx.KindNull:
		return refusalReply(&refusal{status: 422, errorType: "invalid_state", message: stateNoneText})
	case req.state.Kind() != jsonx.KindString && req.state.Kind() != jsonx.KindObject && req.state.Kind() != jsonx.KindArray:
		return refusalReply(&refusal{status: 422, errorType: "invalid_state", message: stateKindText})
	}
	questions, err := schema.ParseQuestions(req.questions)
	if err != nil {
		return questionsReply(err)
	}
	cfg := ad.eval
	cfg.retry = retryPolicyFor(ctx, ad.eval.retry)
	if err := cfg.retry.check(); err != nil {
		return refusalReply(&refusal{status: 400, errorType: "invalid_retry", message: err.Error()})
	}
	result, report, err := evaluate(ctx, cfg, p, req.state, questions)
	if report == nil {
		if errors.Is(err, jsonx.ErrDepth) {
			return refusalReply(&refusal{status: 422, errorType: "invalid_state", message: stateTooDeepText})
		}
		// evaluate refuses nothing else that ParseQuestions accepted.
		return refusalReply(&refusal{status: encodingFailedStatus, errorType: "adapter_internal", message: internalFailureText})
	}
	report.Debug.SDKRetryCount = sdkRetries
	if err != nil {
		return failureReply(ctx, t, p, report, err)
	}
	return bodyReply(http.StatusOK, successBody(result, report))
}

// parseRequest reads a System One body: one JSON object whose model, when
// present, is a string and whose questions is an object. A body that opens
// more than 10000 arrays and objects inside one another is not read, so a
// state opening 10000 is refused here.
func parseRequest(body []byte) (request, error) {
	root, err := jsonx.Read(body)
	if err != nil || root.Kind() != jsonx.KindObject {
		return request{}, &refusal{status: 400, errorType: "invalid_body", message: notAnObjectText}
	}
	var req request
	if m, ok := root.Member("model"); ok {
		if m.Kind() != jsonx.KindString {
			return request{}, &refusal{status: 400, errorType: "invalid_body", message: modelNotStringText}
		}
		req.model = m.Text()
	}
	q, ok := root.Member("questions")
	if !ok || q.Kind() != jsonx.KindObject {
		return request{}, &refusal{status: 400, errorType: "invalid_body", message: questionsNotObjText}
	}
	req.questions = q
	req.state, req.hasState = root.Member("state")
	return req, nil
}

// parseRetryCount returns the SDK's retry number from the value of
// X-TypeSafe-Retry-Count: one to nine ASCII digits without a sign or a
// leading zero, so at least 1; any other value, an absent header included,
// gives 0, which records nothing.
func parseRetryCount(v string) int {
	if v == "" || len(v) > 9 || v[0] == '0' {
		return 0
	}
	n := 0
	for i := range len(v) {
		c := v[i]
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// newRequestID returns the value of X-Typesafe-Request-Id for one response:
// "adp_" and 16 lower-case hex digits from crypto/rand, which never fails
// (it crashes the program instead of returning an error).
func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "adp_" + hex.EncodeToString(b[:])
}

// failureReply answers an evaluation that ended in err after it started,
// with report the attempts made. The call's context decides first, whatever
// the provider returned: a cancelled call returns ctx.Err() itself and no
// response, and a call whose deadline passed returns the timeout Error with
// the Report. Then output that still does not match the schema answers 200
// with "answers": null, and the provider's typed errors their own class; any
// other error is a provider error upstream does not retry
// (src/system_one_adapter/_utils/error_handling.py:72), answered 424.
func failureReply(ctx context.Context, t target, p llm.Provider, report *Report, err error) reply {
	cerr := ctx.Err()
	switch {
	case errors.Is(cerr, context.Canceled):
		return reply{err: cerr}
	case errors.Is(cerr, context.DeadlineExceeded):
		return reply{err: &Error{Kind: KindTimeout, Provider: t.name, Model: t.model, Report: report}}
	}
	if _, ok := errors.AsType[*malformedError](err); ok {
		return bodyReply(http.StatusOK, malformedBody(p.Model(), report))
	}
	if _, ok := errors.AsType[*llm.TimeoutError](err); ok {
		return reply{err: &Error{Kind: KindTimeout, Provider: t.name, Model: t.model, Report: report}}
	}
	if _, ok := errors.AsType[*llm.ConnectionError](err); ok {
		return reply{err: &Error{Kind: KindConnection, Provider: t.name, Model: t.model, Report: report, cause: sentinelCause(err)}}
	}
	if e, ok := errors.AsType[*llm.StatusError](err); ok {
		status := e.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusFailedDependency
		}
		return bodyReply(status, errorBody(statusMessage(e), "provider_status", nil, report))
	}
	if e, ok := errors.AsType[*llm.NonAnswerError](err); ok {
		return bodyReply(http.StatusFailedDependency, errorBody(e.Message, "non_answer", nil, report))
	}
	return bodyReply(http.StatusFailedDependency, errorBody(err.Error(), "provider_error", nil, report))
}

// sentinelCause returns the sentinel an Error of a provider connection
// failure wraps: the first of context.DeadlineExceeded, context.Canceled,
// os.ErrDeadlineExceeded, io.ErrUnexpectedEOF, io.EOF and net.ErrClosed in
// err's chain, else a syscall.Errno in it, else nil. These are the causes
// the root SDK keeps when it replaces a cause that prints a credential;
// nothing else of the provider's error reaches the chain.
func sentinelCause(err error) error {
	for _, s := range []error{context.DeadlineExceeded, context.Canceled, os.ErrDeadlineExceeded, io.ErrUnexpectedEOF, io.EOF, net.ErrClosed} {
		if errors.Is(err, s) {
			return s
		}
	}
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return errno
	}
	return nil
}

// refusalReply answers a request refused before an evaluation started: the
// refusal's status and an error body without usage and debug.
func refusalReply(err error) reply {
	r, ok := errors.AsType[*refusal](err)
	if !ok {
		r = &refusal{status: encodingFailedStatus, errorType: "adapter_internal", message: internalFailureText}
	}
	return bodyReply(r.status, errorBody(r.message, r.errorType, nil, nil))
}

// questionsReply answers questions schema.ParseQuestions refused: 422
// invalid_questions with upstream's message, and the defects of single
// questions as the member errors of detail, each {"loc": ["body",
// "questions", <name>, <field>…], "msg": <the rule it breaks>}, absent when
// the refusal names no question.
func questionsReply(err error) reply {
	qerr, ok := errors.AsType[*schema.QuestionError](err)
	if !ok {
		// ParseQuestions refuses only a value that is not an object, which
		// parseRequest refused already.
		return refusalReply(err)
	}
	var issues []jsonx.Value
	for _, issue := range qerr.Issues {
		loc := []jsonx.Value{jsonx.String("body"), jsonx.String("questions")}
		for _, part := range issue.Loc {
			loc = append(loc, jsonx.String(part))
		}
		issues = append(issues, jsonx.Object(
			jsonx.Member{Name: "loc", Value: jsonx.Array(loc...)},
			jsonx.Member{Name: "msg", Value: jsonx.String(issue.Message)},
		))
	}
	var extra []jsonx.Member
	if len(issues) > 0 {
		extra = []jsonx.Member{{Name: "errors", Value: jsonx.Array(issues...)}}
	}
	return bodyReply(422, errorBody(qerr.Message, "invalid_questions", extra, nil))
}

// writer is a response body as JSON values, written by bodyReply.
type writer func() (jsonx.Value, error)

// errorBody returns the error body {"detail": {"message", "error_type",
// extra…}, "usage", "debug"}, with usage and debug when report is not nil.
func errorBody(message, errorType string, extra []jsonx.Member, report *Report) writer {
	return func() (jsonx.Value, error) {
		detail := append([]jsonx.Member{
			{Name: "message", Value: jsonx.String(message)},
			{Name: "error_type", Value: jsonx.String(errorType)},
		}, extra...)
		members := []jsonx.Member{{Name: "detail", Value: jsonx.Object(detail...)}}
		if report != nil {
			more, err := report.members()
			if err != nil {
				return jsonx.Value{}, err
			}
			members = append(members, more...)
		}
		return jsonx.Object(members...), nil
	}
}

// malformedBody returns the body of output that still does not match the
// schema after the corrective retries: {"model", "usage", "answers": null,
// "debug"}, which the SDK reads as a *decision.ResponseValidationError of
// answers, as upstream raises TypeSafeAPIResponseValidationError(200, …,
// "answers") (src/system_one_adapter/_client.py:225-226).
func malformedBody(model string, report *Report) writer {
	return func() (jsonx.Value, error) {
		ud, err := report.members()
		if err != nil {
			return jsonx.Value{}, err
		}
		return jsonx.Object(
			jsonx.Member{Name: "model", Value: jsonx.String(model)},
			ud[0],
			jsonx.Member{Name: "answers", Value: jsonx.Value{}},
			ud[1],
		), nil
	}
}

// successBody returns the body of an answer, upstream's SystemOneResponse
// as json.dumps writes its model_dump(mode="json"): {"model", "usage",
// "answers", "debug"}, the answers in question order, each with the members
// of its type in upstream's order, floats as Python's repr writes them.
func successBody(result *evalResult, report *Report) writer {
	return func() (jsonx.Value, error) {
		ud, err := report.members()
		if err != nil {
			return jsonx.Value{}, err
		}
		answers := make([]jsonx.Member, len(result.answers))
		for i := range result.answers {
			answers[i] = jsonx.Member{Name: result.answers[i].question.ID(), Value: answerValue(&result.answers[i])}
		}
		return jsonx.Object(
			jsonx.Member{Name: "model", Value: jsonx.String(result.model)},
			ud[0],
			jsonx.Member{Name: "answers", Value: jsonx.Object(answers...)},
			ud[1],
		), nil
	}
}

// answerValue returns one answer: {"type", "noul"} for a Noul question;
// {"type", "score", "confidence", "legend", "probabilities"} for a Score,
// legend and probabilities keyed by level; {"type", "choice", "confidence",
// "probabilities"} for a Choice, probabilities keyed by label.
func answerValue(a *answer) jsonx.Value {
	kind := a.question.Kind()
	members := []jsonx.Member{{Name: "type", Value: jsonx.String(kind.String())}}
	switch kind {
	case schema.Noul:
		return jsonx.Object(append(members, jsonx.Member{Name: "noul", Value: jsonx.Float(a.noul, jsonx.Repr)})...)
	case schema.Score:
		members = append(members, jsonx.Member{Name: "score", Value: jsonx.Float(a.score, jsonx.Repr)})
	default:
		members = append(members, jsonx.Member{Name: "choice", Value: jsonx.String(a.choice)})
	}
	members = append(members, jsonx.Member{Name: "confidence", Value: jsonx.Float(a.confidence, jsonx.Repr)})
	labels := a.question.Labels()
	if kind == schema.Score {
		legend := make([]jsonx.Member, len(labels))
		for i, label := range labels {
			legend[i] = jsonx.Member{Name: label, Value: a.legend[i]}
		}
		members = append(members, jsonx.Member{Name: "legend", Value: jsonx.Object(legend...)})
	}
	probabilities := make([]jsonx.Member, len(labels))
	for i, label := range labels {
		probabilities[i] = jsonx.Member{Name: label, Value: jsonx.Float(a.probabilities[i], jsonx.Repr)}
	}
	return jsonx.Object(append(members, jsonx.Member{Name: "probabilities", Value: jsonx.Object(probabilities...)})...)
}

// modelsReply answers GET /v1/models: {"models": [{"name", "description",
// "release_date"}, …]}, one card for the default model id and one per
// WithProvider name that is not that id, in the order they were registered.
// An Adapter with neither answers {"models": []}, which the SDK accepts.
func (ad *Adapter) modelsReply() reply {
	return bodyReply(http.StatusOK, func() (jsonx.Value, error) {
		var cards []jsonx.Value
		card := func(name, provider string) {
			cards = append(cards, jsonx.Object(
				jsonx.Member{Name: "name", Value: jsonx.String(name)},
				jsonx.Member{Name: "description", Value: jsonx.String("Answered by the LLM provider " + provider + " through system-one-adapter-go " + Version + ".")},
				jsonx.Member{Name: "release_date", Value: jsonx.String(releaseDate)},
			))
		}
		if ad.defaultModel != "" {
			provider := ad.defaultName
			if provider == "" {
				provider = ad.defaultModel
			}
			card(ad.defaultModel, provider)
		}
		for _, name := range ad.providerList {
			if name != ad.defaultModel {
				card(name, name)
			}
		}
		return jsonx.Object(jsonx.Member{Name: "models", Value: jsonx.Array(cards...)}), nil
	})
}

// bodyReply writes the body w gives and answers it with status. A body
// that cannot be written, or that does not read back as JSON, is answered
// with the 424 adapter_internal body instead, so that the SDK never
// receives a body it cannot decode.
func bodyReply(status int, w writer) reply {
	v, err := w()
	if err != nil {
		return reply{status: encodingFailedStatus, body: encodingFailedBody}
	}
	b, err := jsonx.Marshal(v)
	if err != nil {
		return reply{status: encodingFailedStatus, body: encodingFailedBody}
	}
	if _, err := jsonx.Read(b); err != nil {
		return reply{status: encodingFailedStatus, body: encodingFailedBody}
	}
	return reply{status: status, body: b}
}

// responseHeader returns the header of a response with a JSON body of n
// bytes and the request id id.
func responseHeader(n int, id string) http.Header {
	h := make(http.Header, 3)
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(n))
	h.Set(requestIDHeader, id)
	return h
}
