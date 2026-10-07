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

// Package gemini calls the Gemini Interactions API.
//
// It ports the GeminiProvider of system-one-adapter-python v0.2.1
// (src/system_one_adapter/providers/gemini.py) without a vendor SDK: the
// request body is written and the response body read here, and the text of
// the answer is taken from the response's steps as google-genai 2.24.0
// computes its output_text.
//
// A Provider sends POST <base URL>/v1beta/interactions with the key in the
// x-goog-api-key header and no other header of its own. A response with a
// status outside 200 to 299 is an *llm.StatusError that keeps the status,
// the body, with a body that is empty or only white space as a nil Body,
// and of the response headers only Retry-After, Retry-After-Ms and
// X-Typesafe-Request-Id: Gemini's own request id and every other header are
// dropped, so a retry predicate cannot read them. A timeout is an
// *llm.TimeoutError, a request that failed without a response an
// *llm.ConnectionError, and a cancelled context's error is returned as it
// is. A completed interaction is an answer; an interaction that did not
// complete, or that omits its token counts, is an *llm.NonAnswerError with
// upstream's text; a body that is not an interaction of the shape read here
// is an error of none of package llm's types.
//
// A Provider logs and prints nothing, and records request and response
// bodies only in the request's llm.Trace, never a header.
package gemini

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/rest"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

const (
	// apiName is the API name a Provider records with each request, as
	// upstream records it (providers/gemini.py:137).
	apiName = "interactions"
	// endpointPath is appended to the base URL's path. The Interactions API
	// is a beta; its version is this one constant.
	endpointPath = "v1beta/interactions"
	// defaultBaseURL is google-genai's base URL when GOOGLE_GEMINI_BASE_URL
	// is not set.
	defaultBaseURL = "https://generativelanguage.googleapis.com/"
	// keyHeader carries the API key, in the canonical form of
	// http.CanonicalHeaderKey.
	keyHeader = "X-Goog-Api-Key"
)

// The environment variables New reads, google-genai's own names.
const (
	envGoogleKey = "GOOGLE_API_KEY"
	envGeminiKey = "GEMINI_API_KEY"
	envBaseURL   = "GOOGLE_GEMINI_BASE_URL"
)

// The texts of upstream's non-answers (providers/gemini.py:70,82,85), byte
// for byte.
const (
	incompleteBefore = "Gemini response did not complete: "
	incompleteAfter  = "."
	omittedUsageText = "Gemini response omitted usage."
	// unknownReason is the reason of an interaction whose status is null,
	// absent or empty and that records no errors.
	unknownReason = "unknown"
)

// errNoKey is New's error when neither WithAPIKey nor the environment gives
// a key.
var errNoKey = errors.New("gemini: no API key: set GOOGLE_API_KEY or GEMINI_API_KEY, or pass WithAPIKey")

// Provider calls one Gemini model; it is safe for concurrent use.
// Print %p only on a pointer, where fmt prints its address. On a value,
// fmt prints a bad-verb diagnostic. Credential-bearing state is held
// behind a private pointer so reflective formatting prints only its address.
type Provider struct {
	model  string
	state  *requestState
	client *rest.Client
}

// requestState keeps two pointer levels because fmt's bad-verb path reprints
// a nested pointer with %v at depth zero. The credential pointer one level
// down then prints only as an address.
type requestState struct {
	credentials *requestCredentials
}

type requestCredentials struct {
	key      string
	endpoint *url.URL
}

// options are the settings Option values write.
type options struct {
	key           string
	baseURL       string
	httpClient    *http.Client
	httpClientSet bool
	timeout       time.Duration
	timeoutSet    bool
}

// Option configures a Provider.
type Option func(*options)

// New returns a Provider for model.
//
// It reads the environment once, for what the options did not set, and Do
// reads it no more: the key from GOOGLE_API_KEY, else GEMINI_API_KEY ("if
// both are set, GOOGLE_API_KEY takes precedence", as google-genai says), and
// the base URL from GOOGLE_GEMINI_BASE_URL, else
// https://generativelanguage.googleapis.com/. A key has the white space
// around it removed (strings.TrimSpace), as google-genai strips it. A
// variable that is set to the empty string, or a key variable that holds
// only white space, counts as not set, and so does an empty WithAPIKey or
// WithBaseURL: the next source applies.
//
// New fails when no key is found, when the base URL is not an absolute http
// or https URL with a host, when WithHTTPClient was given nil and when
// WithTimeout was given a duration that is not positive. Its errors name
// the option or the variable and never print a value.
//
// Model returns model as it is given; the name is printed in the text of
// the Adapter's errors, so it must not hold a key.
//
// New builds replacement of the API key's exact known forms once: raw,
// JSON string content and QueryEscape/PathEscape with both percent-hex cases.
// Received bodies and diagnostic request copies use "***"; outbound bytes
// stay unchanged. This is exact replacement, not decoding-aware redaction.
func New(model string, opts ...Option) (*Provider, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	switch {
	case o.httpClientSet && o.httpClient == nil:
		return nil, errors.New("gemini: WithHTTPClient: the client is nil")
	case o.timeoutSet && o.timeout <= 0:
		return nil, errors.New("gemini: WithTimeout: the timeout is not positive")
	}
	key := strings.TrimSpace(o.key)
	if key == "" {
		key = strings.TrimSpace(envValue(envGoogleKey))
	}
	if key == "" {
		key = strings.TrimSpace(envValue(envGeminiKey))
	}
	if key == "" {
		return nil, errNoKey
	}
	base, source := o.baseURL, "WithBaseURL"
	if base == "" {
		base, source = envValue(envBaseURL), envBaseURL
	}
	if base == "" {
		base = defaultBaseURL
	}
	u, err := rest.ParseURL(base)
	if err != nil {
		// err holds no part of the URL, which may carry a credential.
		return nil, errors.New("gemini: the base URL of " + source + " is not an absolute http or https URL with a host")
	}
	return &Provider{
		model:  model,
		state:  &requestState{credentials: &requestCredentials{key: key, endpoint: endpoint(u)}},
		client: rest.New(rest.Config{HTTPClient: o.httpClient, Timeout: o.timeout, BlankErrorBodyIsNone: true, BodyScrubber: rest.NewBodyScrubber(key)}),
	}, nil
}

// envValue returns the value of the environment variable name, "" when it
// is not set.
func envValue(name string) string {
	v, _ := rest.Env(name)
	return v
}

// endpoint returns the Interactions URL under base: base's path without
// its trailing slashes, as google-genai strips them, then one slash and
// endpointPath; the path's escapes kept, the query kept as it is and the
// fragment dropped.
func endpoint(base *url.URL) *url.URL {
	u := *base
	u.Path = strings.TrimRight(base.Path, "/") + "/" + endpointPath
	u.RawPath = strings.TrimRight(base.EscapedPath(), "/") + "/" + endpointPath
	u.Fragment, u.RawFragment = "", ""
	return &u
}

// WithAPIKey sets the key sent as x-goog-api-key (default GOOGLE_API_KEY,
// else GEMINI_API_KEY), with the white space around it removed. The key
// travels in that header alone, never in the URL. A key that is empty, or
// only white space, counts as not given: the environment applies.
func WithAPIKey(key string) Option {
	return func(o *options) { o.key = key }
}

// WithBaseURL sets the URL to which v1beta/interactions is appended (default
// GOOGLE_GEMINI_BASE_URL, else https://generativelanguage.googleapis.com/),
// with one slash between them; u's query is kept and its fragment dropped.
// An empty u counts as not given: the environment applies.
//
// A userinfo in u is sent as it is: net/http turns it into an
// Authorization: Basic header beside x-goog-api-key. The Adapter's error
// texts print the URL's scheme, host and path only, so a credential belongs
// in WithAPIKey, never in u's path.
func WithBaseURL(u string) Option {
	return func(o *options) { o.baseURL = u }
}

// WithHTTPClient sets a borrowed client: the Provider never writes to c and
// Close leaves it alone. Its settings are read when New is called.
//
// No redirect is followed: a response of status 300 to 399 is an
// *llm.StatusError. When c's own CheckRedirect is set, c follows the
// redirects it allows, and net/http then sends the x-goog-api-key header,
// with the key, and a 307 or 308 the request body, on to the host a
// redirect names; c's CheckRedirect decides where they may go. A nil c is
// an error of New.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) { o.httpClient, o.httpClientSet = c, true }
}

// WithTimeout bounds each request, from its start to the end of the
// response body (default 600 s). A d that is not positive is an error of
// New.
func WithTimeout(d time.Duration) Option {
	return func(o *options) { o.timeout, o.timeoutSet = d, true }
}

// Model returns the model name.
func (p *Provider) Model() string { return p.model }

// String returns the model alone, as "gemini.Provider(<model>)". It is what
// %v, %+v and %s print for a Provider value or a pointer to one, so that
// they print nothing of the key or of the base URL, whose userinfo and
// query may carry a credential, as fmt's field-by-field form would. A nil
// *Provider prints as <nil>.
func (p Provider) String() string {
	return "gemini.Provider(" + p.model + ")"
}

// GoString returns what %#v prints for p, a Provider value or a pointer to
// one, with the model only: the key and the base URL, which may carry a
// credential in its userinfo or query, are not printed. A nil *Provider
// prints as <nil>.
func (p Provider) GoString() string {
	return "gemini.Provider{Model:" + p.model + "}"
}

// Format prints String for every verb except %#v, which prints GoString.
// Width, precision and flags are ignored; headers and endpoints are never
// printed. The fmt package handles %T and %p itself before calling Format.
func (p Provider) Format(f fmt.State, verb rune) {
	var text string
	if verb == 'v' && f.Flag('#') {
		text = p.GoString()
	} else {
		text = p.String()
	}
	_, _ = fmt.Fprint(f, text)
}

// Do performs one request.
//
// It records the request body in req.Trace with the API name interactions
// before it sends it, and a response body of status 200 to 299 with its
// status as the finish reason before it reads it, so the Adapter's attempt
// keeps what arrived when the response is not an answer.
func (p *Provider) Do(ctx context.Context, req *llm.Request) (*llm.Result, error) {
	body, err := requestBody(p.model, req)
	if err != nil {
		return nil, err
	}
	if req.Trace != nil {
		req.Trace.RecordRequest(apiName, p.client.ScrubBody(body))
	}
	header := make(http.Header, 1)
	header.Set(keyHeader, p.state.credentials.key)
	data, err := p.client.Post(ctx, p.state.credentials.endpoint, header, body)
	if err != nil {
		return nil, err
	}
	return result(data, req.Trace)
}

// Close closes the idle connections of an owned client.
func (p *Provider) Close() error { return p.client.Close() }

// requestBody returns the Interactions request upstream builds
// (providers/gemini.py:35-64), its members in upstream's order: model,
// input, store, then system_instruction when the system messages hold any
// text, then response_format in structured mode. input has one step per
// message that is not a system message: model_output for an assistant
// message and user_input for any other, each with one text content.
// system_instruction is the system messages' contents joined with a blank
// line.
func requestBody(model string, req *llm.Request) ([]byte, error) {
	var system []string
	input := make([]jsonx.Value, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = append(system, m.Content)
			continue
		}
		kind := "user_input"
		if m.Role == "assistant" {
			kind = "model_output"
		}
		input = append(input, jsonx.Object(
			jsonx.Member{Name: "type", Value: jsonx.String(kind)},
			jsonx.Member{Name: "content", Value: jsonx.Array(jsonx.Object(
				jsonx.Member{Name: "type", Value: jsonx.String("text")},
				jsonx.Member{Name: "text", Value: jsonx.String(m.Content)},
			))},
		))
	}
	members := []jsonx.Member{
		{Name: "model", Value: jsonx.String(model)},
		{Name: "input", Value: jsonx.Array(input...)},
		{Name: "store", Value: jsonx.Bool(false)},
	}
	if s := strings.Join(system, "\n\n"); s != "" {
		members = append(members, jsonx.Member{Name: "system_instruction", Value: jsonx.String(s)})
	}
	if req.Structured {
		members = append(members, jsonx.Member{Name: "response_format", Value: jsonx.Object(
			jsonx.Member{Name: "type", Value: jsonx.String("text")},
			jsonx.Member{Name: "mime_type", Value: jsonx.String("application/json")},
			jsonx.Member{Name: "schema", Value: jsonx.Raw(req.Schema)},
		)})
	}
	body, err := jsonx.Marshal(jsonx.Object(members...))
	if err != nil {
		// Only the schema can be refused: every other value is a string,
		// written with invalid UTF-8 replaced.
		return nil, errors.New("gemini: the request's schema is not one JSON value")
	}
	return body, nil
}

// shapeError returns the error of a response body that is not an
// interaction of the shape read here; what names the member, never its
// value.
func shapeError(what string) error {
	return errors.New("gemini: the response body is not an Interaction: " + what)
}

// result reads data, the body of a response of status 200 to 299, as
// upstream's _result reads the SDK's Interaction (providers/gemini.py:74-90).
// It records data in trace first, with the status as the finish reason
// when the status is a string and nil otherwise. Then:
//
//   - a status other than completed is a non-answer whose reason is the
//     status, unknown when the status is null, absent or empty, and the text
//     of errorsText when errors is a list that is not empty;
//   - a usage, total_input_tokens or total_output_tokens that is null or
//     absent is the non-answer "Gemini response omitted usage.";
//   - the text is the trailing text of the steps (outputText), "" for none.
//
// A body that is not JSON, or whose status, errors, usage, counts, steps or
// steps' content are not of the JSON kinds those rules read, is an error of
// none of package llm's types, whose text quotes nothing of the body.
func result(data []byte, trace *llm.Trace) (*llm.Result, error) {
	doc, err := jsonx.Read(data)
	var status jsonx.Node
	hasStatus := false
	if err == nil {
		status, hasStatus = doc.Member("status")
	}
	var finish *string
	if hasStatus && status.Kind() == jsonx.KindString {
		s := status.Text()
		finish = &s
	}
	trace.RecordResponse(data, finish)
	switch {
	case err != nil:
		return nil, shapeError("the body is not JSON")
	case doc.Kind() != jsonx.KindObject:
		return nil, shapeError("the body is not a JSON object")
	case hasStatus && status.Kind() != jsonx.KindString && status.Kind() != jsonx.KindNull:
		return nil, shapeError("status is neither a string nor null")
	}
	if finish == nil || *finish != "completed" {
		reason := unknownReason
		if finish != nil && *finish != "" {
			reason = *finish
		}
		errs, err := errorsText(doc)
		if err != nil {
			return nil, err
		}
		if errs != "" {
			reason = errs
		}
		return nil, &llm.NonAnswerError{Message: incompleteBefore + reason + incompleteAfter}
	}
	usage, ok := doc.Member("usage")
	switch {
	case !ok || usage.Kind() == jsonx.KindNull:
		return nil, &llm.NonAnswerError{Message: omittedUsageText}
	case usage.Kind() != jsonx.KindObject:
		return nil, shapeError("usage is neither an object nor null")
	}
	in, err := count(usage, "total_input_tokens")
	if err != nil {
		return nil, err
	}
	out, err := count(usage, "total_output_tokens")
	if err != nil {
		return nil, err
	}
	text, err := outputText(doc)
	if err != nil {
		return nil, err
	}
	return &llm.Result{Text: text, InputTokens: in, OutputTokens: out}, nil
}

// count returns the token count usage holds under name, upstream's
// _token_count (providers/gemini.py:67-71): a null or absent count is the
// omitted-usage non-answer, and a count that is not an integer from 0 to
// 2^64-1 an error of the body's shape.
func count(usage jsonx.Node, name string) (llm.Count, error) {
	v, ok := usage.Member(name)
	if !ok || v.Kind() == jsonx.KindNull {
		return llm.Count{}, &llm.NonAnswerError{Message: omittedUsageText}
	}
	if v.Kind() == jsonx.KindNumber && v.IsInt() {
		if v.Text() == "-0" {
			return llm.Count{Known: true}, nil
		}
		if n, err := strconv.ParseUint(v.Text(), 10, 64); err == nil {
			return llm.Count{N: n, Known: true}, nil
		}
	}
	return llm.Count{}, shapeError("usage." + name + " is not an integer from 0 to 2^64-1")
}

// errorsText returns the reason a did-not-complete non-answer gives for
// doc's errors member, "" when the member is absent, null or an empty list.
// When every element is an object holding only code and message, each a
// string of printable ASCII or null or absent, it is Python's str() of the
// SDK's list of Error objects, as upstream writes it
// (providers/gemini.py:79-81; measured on google-genai 2.24.0):
// [Error(code=<repr>, message=<repr>), ...], None for a null or absent
// field. For any other list it is the list as compact JSON, written as the
// response body writes numbers. An errors member that is not a list is an
// error of the body's shape.
func errorsText(doc jsonx.Node) (string, error) {
	list, ok := doc.Member("errors")
	if !ok || list.Kind() == jsonx.KindNull {
		return "", nil
	}
	if list.Kind() != jsonx.KindArray {
		return "", shapeError("errors is neither a list nor null")
	}
	if list.Len() == 0 {
		return "", nil
	}
	if text, ok := errorsRepr(list); ok {
		return text, nil
	}
	v, err := list.Value(jsonx.Repr)
	if err != nil {
		return "", shapeError("errors is nested too deeply")
	}
	// A Value made from a Node that Read accepted holds only finite numbers
	// and valid strings, so Marshal cannot refuse it.
	b, _ := jsonx.Marshal(v)
	return string(b), nil
}

// errorsRepr returns Python's str() of the list of google-genai's Error
// objects the SDK makes of list, and true, when every element is an object
// whose members are only code and message, each a string of printable
// ASCII (0x20 to 0x7e) or null; otherwise false.
func errorsRepr(list jsonx.Node) (string, bool) {
	dst := []byte{'['}
	for i := range list.Len() {
		e := list.Index(i)
		if e.Kind() != jsonx.KindObject {
			return "", false
		}
		for j := range e.Len() {
			if name := e.Name(j); name != "code" && name != "message" {
				return "", false
			}
		}
		if i > 0 {
			dst = append(dst, ", "...)
		}
		dst = append(dst, "Error("...)
		for j, field := range [...]string{"code", "message"} {
			if j > 0 {
				dst = append(dst, ", "...)
			}
			dst = append(append(dst, field...), '=')
			v, _ := e.Member(field)
			switch {
			case v.Kind() == jsonx.KindNull:
				dst = append(dst, "None"...)
			case v.Kind() == jsonx.KindString && isPrintableASCII(v.Text()):
				dst = appendPyStrRepr(dst, v.Text())
			default:
				return "", false
			}
		}
		dst = append(dst, ')')
	}
	return string(append(dst, ']')), true
}

// isPrintableASCII reports whether s holds only the bytes 0x20 to 0x7e.
func isPrintableASCII(s string) bool {
	for i := range len(s) {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// appendPyStrRepr appends Python's repr() of s, a string of printable
// ASCII: in single quotes, or double quotes when s holds a single quote and
// no double quote, with a backslash before a backslash and before the quote
// in use.
func appendPyStrRepr(dst []byte, s string) []byte {
	quote := byte('\'')
	if strings.IndexByte(s, '\'') >= 0 && strings.IndexByte(s, '"') < 0 {
		quote = '"'
	}
	dst = append(dst, quote)
	for i := range len(s) {
		if s[i] == quote || s[i] == '\\' {
			dst = append(dst, '\\')
		}
		dst = append(dst, s[i])
	}
	return append(dst, quote)
}

// outputText returns the text google-genai 2.24.0 computes as output_text
// (google/genai/_gaos/types/interactions/interaction.py, the validator
// _populate_output_helpers): walking the steps from the last, it stops at a
// user_input step; it skips any other step that is not model_output, and a
// model_output step whose content is not a list, until text has been found,
// and stops at either after; within a model_output step it walks the content
// from the last item, skipping items that are not text until text has been
// found and stopping at the first such item after, and it takes the text of
// each text item, "" when it is not a string. The texts taken are joined in
// their order. So the text is the last run of consecutive text items, which
// may span model_output steps that hold nothing else.
//
// A steps member that is not a list, a step that is not an object, or a
// step's content that is neither a list nor null, is an error of the body's
// shape; a step or item of another type is read as the SDK reads it, by its
// type alone.
func outputText(doc jsonx.Node) (string, error) {
	steps, ok := doc.Member("steps")
	if !ok || steps.Kind() == jsonx.KindNull {
		return "", nil
	}
	if steps.Kind() != jsonx.KindArray {
		return "", shapeError("steps is neither a list nor null")
	}
	for i := range steps.Len() {
		step := steps.Index(i)
		if step.Kind() != jsonx.KindObject {
			return "", shapeError("a step is not an object")
		}
		if c, ok := step.Member("content"); ok && c.Kind() != jsonx.KindArray && c.Kind() != jsonx.KindNull {
			return "", shapeError("the content of a step is neither a list nor null")
		}
	}
	var parts []string
	collecting := false
	for i := steps.Len() - 1; i >= 0; i-- {
		step := steps.Index(i)
		kind := typeOf(step)
		if kind == "user_input" {
			break
		}
		content, _ := step.Member("content")
		if kind != "model_output" || content.Kind() != jsonx.KindArray {
			if collecting {
				break
			}
			continue
		}
		stop := false
		for j := content.Len() - 1; j >= 0; j-- {
			item := content.Index(j)
			if typeOf(item) != "text" {
				if collecting {
					stop = true
					break
				}
				continue
			}
			collecting = true
			text, _ := item.Member("text")
			parts = append(parts, stringOf(text))
		}
		if stop {
			break
		}
	}
	var b strings.Builder
	for _, part := range slices.Backward(parts) {
		b.WriteString(part)
	}
	return b.String(), nil
}

// typeOf returns the type member of v when it is a string, and "" for any
// other v.
func typeOf(v jsonx.Node) string {
	t, _ := v.Member("type")
	return stringOf(t)
}

// stringOf returns the text of a string and "" for any other kind.
func stringOf(v jsonx.Node) string {
	if v.Kind() != jsonx.KindString {
		return ""
	}
	return v.Text()
}
