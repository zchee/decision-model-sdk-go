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

// Package anthropic calls the Anthropic Messages API.
//
// It ports the AnthropicProvider of system-one-adapter-python v0.2.1
// (src/system_one_adapter/providers/anthropic.py) without the vendor's
// Python SDK: the provider writes the request body itself, posts it to
// POST {base URL}/v1/messages with the credential headers (x-api-key for an
// API key, Authorization: Bearer for an auth token, both when both are set)
// and anthropic-version: 2023-06-01, and reads the answer text and the
// token counts from the response body.
//
// A response with a status outside 200 to 299 is an *llm.StatusError that
// keeps the status, the body, and of the response headers only
// Retry-After, Retry-After-Ms and X-Typesafe-Request-Id: Anthropic's own
// request-id header is not kept. A timeout is an *llm.TimeoutError, a
// request that failed without a response an *llm.ConnectionError, and a
// cancelled context gives the context's error itself. A completed response
// that is not an answer (truncated at the output limit, or stopped for any
// reason but end_turn, stop_sequence or none) is an *llm.NonAnswerError
// with upstream's text. A 2xx body that is not a Messages response, such as
// one without usage, is an error of none of package llm's types.
//
// A Provider records the request body and the response body in the
// request's llm.Trace, never a header, and logs nothing.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
	"github.com/zchee/decision-model-sdk-go/adapter/internal/rest"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

const (
	// defaultBaseURL is where requests go when neither WithBaseURL nor
	// ANTHROPIC_BASE_URL gives a base URL: the vendor SDK's default.
	defaultBaseURL = "https://api.anthropic.com"
	// messagesPath is the Messages operation's path under the base URL.
	messagesPath = "/v1/messages"
	// apiName is the API name a Provider records in the Trace
	// (providers/anthropic.py:118).
	apiName = "messages"
	// apiVersion is the anthropic-version header's value, the one the
	// vendor's SDK sends.
	apiVersion = "2023-06-01"
	// defaultMaxTokens is upstream's _DEFAULT_MAX_TOKENS
	// (providers/anthropic.py:17).
	defaultMaxTokens = 4096
)

// The environment variables New reads.
const (
	envAPIKey    = "ANTHROPIC_API_KEY"    //nolint:gosec // G101: a variable's name, not a credential.
	envAuthToken = "ANTHROPIC_AUTH_TOKEN" //nolint:gosec // G101: a variable's name, not a credential.
	envBaseURL   = "ANTHROPIC_BASE_URL"
)

// The texts of New's refusals. None holds a value it was given or read.
const (
	// maxTokensText is upstream's ValueError text, byte for byte
	// (providers/anthropic.py:85-86).
	maxTokensText    = "max_tokens must be > 0"                                                                                       //nolint:gosec // G101: an error text that names the output limit, not a credential.
	noCredentialText = "anthropic: no credential: set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN, or pass WithAPIKey or WithAuthToken" //nolint:gosec // G101: an error text that names where a credential comes from.
	optionURLText    = "anthropic: WithBaseURL: the base URL is not an absolute http or https URL with a host"
	envURLText       = "anthropic: ANTHROPIC_BASE_URL: the base URL is not an absolute http or https URL with a host"
	nilClientText    = "anthropic: WithHTTPClient: the client is nil"
	timeoutText      = "anthropic: WithTimeout: the timeout is not positive"
)

// The non-answer texts of upstream's _result, byte for byte
// (providers/anthropic.py:57-63).
const (
	truncatedText       = "Anthropic response was truncated at the output token limit. Increase max_tokens on AnthropicProvider or AsyncAnthropicProvider, or request fewer questions."
	incompleteTextStart = "Anthropic response did not complete: "
)

// The errors of a 2xx body that is not a Messages response. Each names the
// member the provider could not read, and none quotes the body.
var (
	errBodyNotJSON      = errors.New("anthropic: the response body is not a Messages response: it is not a JSON object")
	errStopReasonKind   = errors.New("anthropic: the response body is not a Messages response: stop_reason is not a string, an integer or null")
	errContentKind      = errors.New("anthropic: the response body is not a Messages response: content is not an array")
	errBlockKind        = errors.New("anthropic: the response body is not a Messages response: a content block is not an object with a string type")
	errTextKind         = errors.New("anthropic: the response body is not a Messages response: a text block's text is not a string")
	errUsageKind        = errors.New("anthropic: the response body is not a Messages response: usage is not an object")
	errInputTokensKind  = errors.New("anthropic: the response body is not a Messages response: usage.input_tokens is not null or an integer from 0 to 2^64-1")
	errOutputTokensKind = errors.New("anthropic: the response body is not a Messages response: usage.output_tokens is not null or an integer from 0 to 2^64-1")
)

// Provider calls one Anthropic model through the Messages API. It is safe
// for concurrent use.
// Print %p only on a pointer, where fmt prints its address. On a value,
// fmt prints a bad-verb diagnostic. Credential-bearing state is held
// behind a private pointer so reflective formatting prints only its address.
type Provider struct {
	model     string
	maxTokens int
	state     *requestState
	client    *rest.Client
}

// requestState keeps two pointer levels because fmt's bad-verb path reprints
// a nested pointer with %v at depth zero. The credential pointer one level
// down then prints only as an address.
type requestState struct {
	credentials *requestCredentials
}

type requestCredentials struct {
	endpoint *url.URL
	header   http.Header
}

// Option configures a Provider.
type Option func(*options)

// options holds what the Options gave New; an empty string is an option
// not given.
type options struct {
	maxTokens  int
	apiKey     string
	authToken  string
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
	err        error
}

// fail keeps the first error an Option reports.
func (o *options) fail(err error) {
	if o.err == nil {
		o.err = err
	}
}

// WithMaxTokens sets the request's max_tokens, the most output tokens a
// response may hold (default 4096, upstream's default). New refuses a value
// of 0 or less with upstream's text "max_tokens must be > 0".
func WithMaxTokens(n int) Option {
	return func(o *options) { o.maxTokens = n }
}

// WithAPIKey sets the API key, sent as the x-api-key header. A key given
// here makes New read neither ANTHROPIC_API_KEY nor ANTHROPIC_AUTH_TOKEN
// (see New). The key is trimmed of surrounding white space, and a key that
// is empty after that counts as not given.
func WithAPIKey(key string) Option {
	return func(o *options) { o.apiKey = strings.TrimSpace(key) }
}

// WithAuthToken sets an auth token, sent as Authorization: Bearer, beside
// x-api-key when the Provider also has an API key. A token given here makes
// New read neither ANTHROPIC_API_KEY nor ANTHROPIC_AUTH_TOKEN (see New). The
// token is trimmed of surrounding white space, and a token that is empty
// after that counts as not given.
func WithAuthToken(token string) Option {
	return func(o *options) { o.authToken = strings.TrimSpace(token) }
}

// WithBaseURL sets the base URL, to whose path /v1/messages is appended with
// one slash between them; its query is kept and its fragment dropped.
// Without it New reads ANTHROPIC_BASE_URL, and without that, or when the
// value is empty, the base URL is https://api.anthropic.com. An empty URL
// counts as not given. New refuses a URL that is not an absolute http or
// https URL with a host.
//
// The URL's userinfo is sent as it is, and net/http turns it into an
// Authorization: Basic header only when the request carries no
// Authorization header. With an API key alone the userinfo therefore
// reaches the server as Authorization: Basic beside x-api-key; with an
// auth token the request carries Authorization: Bearer, and the userinfo
// does not reach the server.
func WithBaseURL(u string) Option {
	return func(o *options) { o.baseURL = u }
}

// WithHTTPClient sets the client that performs the requests, which the
// Provider borrows: it never writes to it and Close leaves it alone (default:
// a client of the Provider's own over a clone of http.DefaultTransport).
// New refuses a nil client.
//
// No redirect is followed: a response of status 300 to 399 is an
// *llm.StatusError, because net/http would send the x-api-key header on to
// the host a redirect names. A borrowed client whose CheckRedirect is set
// keeps that policy, and then decides where the x-api-key header may go.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) {
		if c == nil {
			o.fail(errors.New(nilClientText))
			return
		}
		o.httpClient = c
	}
}

// WithTimeout bounds each request, from its start to the end of the
// response body (default 600 s, the vendor SDK's read timeout). New refuses
// a timeout of 0 or less.
func WithTimeout(d time.Duration) Option {
	return func(o *options) {
		if d <= 0 {
			o.fail(errors.New(timeoutText))
			return
		}
		o.timeout = d
	}
}

// New returns a Provider for model. It reads the environment once and keeps
// what it read: Do reads no environment variable. ANTHROPIC_BASE_URL is
// read when WithBaseURL was not given. ANTHROPIC_API_KEY and
// ANTHROPIC_AUTH_TOKEN are read only when neither WithAPIKey nor
// WithAuthToken was given: an explicit credential makes New read neither
// variable, as the vendor's Python SDK reads none once it is given one. A
// key or token, given or read, is trimmed of surrounding white space, and
// one that is empty after that counts as none; ANTHROPIC_BASE_URL set to
// the empty string counts as not set. Each credential found is sent:
// x-api-key for the key, Authorization: Bearer for the token, both when
// both are found.
//
// New fails, in this order, when the output limit is not positive (with
// upstream's text), when an option was given an invalid value, when the
// base URL is not an absolute http or https URL with a host, and when
// neither an API key nor an auth token is found. No error text holds a
// value New was given or read.
//
// New builds replacement of the API key and auth token's exact known forms
// once: raw, JSON string content and QueryEscape/PathEscape with both
// percent-hex cases. Received bodies and diagnostic request copies use "***";
// outbound bytes stay unchanged. No decoding-aware redaction is claimed.
func New(model string, opts ...Option) (*Provider, error) {
	o := options{maxTokens: defaultMaxTokens}
	for _, opt := range opts {
		opt(&o)
	}
	switch {
	case o.maxTokens <= 0:
		return nil, errors.New(maxTokensText)
	case o.err != nil:
		return nil, o.err
	}
	endpoint, err := endpointURL(o.baseURL)
	if err != nil {
		return nil, err
	}
	key, token := o.apiKey, o.authToken
	if key == "" && token == "" {
		key, token = envCredential(envAPIKey), envCredential(envAuthToken)
	}
	if key == "" && token == "" {
		return nil, errors.New(noCredentialText)
	}
	header := make(http.Header, 3)
	header.Set("Anthropic-Version", apiVersion)
	if key != "" {
		header.Set("X-Api-Key", key)
	}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	return &Provider{
		model:     model,
		maxTokens: o.maxTokens,
		state:     &requestState{credentials: &requestCredentials{endpoint: endpoint, header: header}},
		client:    rest.New(rest.Config{HTTPClient: o.httpClient, Timeout: o.timeout, BodyScrubber: rest.NewBodyScrubber(key, token)}),
	}, nil
}

// envCredential returns the value of the credential variable name, trimmed
// of surrounding white space: "" when it is not set or holds only white
// space.
func envCredential(name string) string {
	v, _ := rest.Env(name)
	return strings.TrimSpace(v)
}

// endpointURL returns the URL of the Messages operation under the base URL
// opt, or ANTHROPIC_BASE_URL when opt is empty, or the default when both
// are: the base path, its trailing slashes removed, then /v1/messages; the
// query kept and the fragment dropped.
func endpointURL(opt string) (*url.URL, error) {
	raw, refusal := opt, optionURLText
	if raw == "" {
		raw, _ = rest.Env(envBaseURL)
		refusal = envURLText
	}
	if raw == "" {
		raw = defaultBaseURL
	}
	u, err := rest.ParseURL(raw)
	if err != nil {
		return nil, errors.New(refusal)
	}
	u.Path = strings.TrimRight(u.Path, "/") + messagesPath
	if u.RawPath != "" {
		u.RawPath = strings.TrimRight(u.RawPath, "/") + messagesPath
	}
	u.Fragment, u.RawFragment = "", ""
	return u, nil
}

// Model returns the model name New was given.
func (p *Provider) Model() string { return p.model }

// String returns the model alone, as "anthropic.Provider(<model>)". It is
// what %v, %+v and %s print for a Provider value or a pointer to one, so
// that they print nothing of the headers, which hold the API key and the
// auth token, or of the base URL, whose userinfo and query may carry a
// credential, as fmt's field-by-field form would. A nil *Provider prints as
// <nil>.
func (p Provider) String() string {
	return "anthropic.Provider(" + p.model + ")"
}

// GoString returns what %#v prints for p, a Provider value or a pointer to
// one, with the model only: the headers hold the API key and the auth token
// and the base URL may carry a credential in its userinfo or query, so
// neither is printed. A nil *Provider prints as <nil>.
func (p Provider) GoString() string {
	return "anthropic.Provider{Model:" + p.model + "}"
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

// Close closes the idle connections of a client the Provider owns and does
// nothing to a borrowed one. It returns nil.
func (p *Provider) Close() error { return p.client.Close() }

// Do performs one Messages request (upstream's request and _result,
// providers/anthropic.py:34-69,95-120). It records the request body in
// req.Trace before it is sent and the response body of a 2xx response,
// with its stop_reason, before the body is checked.
func (p *Provider) Do(ctx context.Context, req *llm.Request) (*llm.Result, error) {
	body, err := p.requestBody(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: the request body could not be written: %w", err)
	}
	if req.Trace != nil {
		req.Trace.RecordRequest(apiName, p.client.ScrubBody(body))
	}
	data, err := p.client.Post(ctx, p.state.credentials.endpoint, p.state.credentials.header, body)
	if err != nil {
		return nil, err
	}
	return result(data, req.Trace)
}

// requestBody returns upstream's _request_kwargs as JSON, in its member
// order (providers/anthropic.py:34-52): model, max_tokens, system (the
// system messages' contents joined with two newlines, sent even when
// empty), messages (every other message's role and content, in order),
// then output_config in structured mode.
func (p *Provider) requestBody(req *llm.Request) ([]byte, error) {
	var system []string
	conversation := make([]jsonx.Value, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = append(system, m.Content)
			continue
		}
		conversation = append(conversation, jsonx.Object(
			jsonx.Member{Name: "role", Value: jsonx.String(string(m.Role))},
			jsonx.Member{Name: "content", Value: jsonx.String(m.Content)},
		))
	}
	members := []jsonx.Member{
		{Name: "model", Value: jsonx.String(p.model)},
		{Name: "max_tokens", Value: jsonx.Number(strconv.Itoa(p.maxTokens))},
		{Name: "system", Value: jsonx.String(strings.Join(system, "\n\n"))},
		{Name: "messages", Value: jsonx.Array(conversation...)},
	}
	if req.Structured {
		members = append(members, jsonx.Member{Name: "output_config", Value: jsonx.Object(
			jsonx.Member{Name: "format", Value: jsonx.Object(
				jsonx.Member{Name: "type", Value: jsonx.String("json_schema")},
				jsonx.Member{Name: "schema", Value: jsonx.Raw(req.Schema)},
			)},
		)})
	}
	return jsonx.Marshal(jsonx.Object(members...))
}

// result ports upstream's _result (providers/anthropic.py:55-69) for the
// response body data: it records data with its stop_reason in trace, then
// refuses a truncated response and one that did not complete, in that
// order, and only then reads the text and the usage. A stop_reason that is
// an integer is taken as its digits, as Python formats it. The text is the
// text of every content block of type text, joined in order; the usage
// object must be present, and a count that it lacks or holds as null is
// unknown, as upstream reports None.
func result(data []byte, trace *llm.Trace) (*llm.Result, error) {
	doc, err := jsonx.Read(data)
	if err != nil || doc.Kind() != jsonx.KindObject {
		trace.RecordResponse(data, nil)
		return nil, errBodyNotJSON
	}
	var stopReason *string
	switch sr, _ := doc.Member("stop_reason"); sr.Kind() {
	case jsonx.KindNull:
	case jsonx.KindString:
		s := sr.Text()
		stopReason = &s
	case jsonx.KindNumber:
		if !sr.IsInt() {
			trace.RecordResponse(data, nil)
			return nil, errStopReasonKind
		}
		s := intText(sr.Text())
		stopReason = &s
	default:
		trace.RecordResponse(data, nil)
		return nil, errStopReasonKind
	}
	trace.RecordResponse(data, stopReason)
	if stopReason != nil {
		switch *stopReason {
		case "max_tokens":
			return nil, &llm.NonAnswerError{Message: truncatedText}
		case "end_turn", "stop_sequence":
		default:
			return nil, &llm.NonAnswerError{Message: incompleteTextStart + *stopReason + "."}
		}
	}
	text, err := joinText(doc)
	if err != nil {
		return nil, err
	}
	usage, _ := doc.Member("usage")
	if usage.Kind() != jsonx.KindObject {
		return nil, errUsageKind
	}
	in, ok := count(usage, "input_tokens")
	if !ok {
		return nil, errInputTokensKind
	}
	out, ok := count(usage, "output_tokens")
	if !ok {
		return nil, errOutputTokensKind
	}
	return &llm.Result{Text: text, InputTokens: in, OutputTokens: out}, nil
}

// joinText returns the text of every content block of type text in doc's
// content, joined in order; blocks of any other type are skipped.
func joinText(doc jsonx.Node) (string, error) {
	content, _ := doc.Member("content")
	if content.Kind() != jsonx.KindArray {
		return "", errContentKind
	}
	var b strings.Builder
	for i := range content.Len() {
		block := content.Index(i)
		typ, _ := block.Member("type")
		if block.Kind() != jsonx.KindObject || typ.Kind() != jsonx.KindString {
			return "", errBlockKind
		}
		if typ.Text() != "text" {
			continue
		}
		text, _ := block.Member("text")
		if text.Kind() != jsonx.KindString {
			return "", errTextKind
		}
		b.WriteString(text.Text())
	}
	return b.String(), nil
}

// count returns the member name of usage as a count: unknown when it is
// absent or null, known when it is an integer from 0 to 2^64-1, and false
// for anything else.
func count(usage jsonx.Node, name string) (llm.Count, bool) {
	v, _ := usage.Member(name)
	switch {
	case v.Kind() == jsonx.KindNull:
		return llm.Count{}, true
	case v.Kind() != jsonx.KindNumber || !v.IsInt():
		return llm.Count{}, false
	}
	n, err := strconv.ParseUint(intText(v.Text()), 10, 64)
	if err != nil {
		return llm.Count{}, false
	}
	return llm.Count{N: n, Known: true}, true
}

// intText returns an integer literal as Python's str writes the int
// json.loads makes of it: the literal itself, but 0 for -0.
func intText(literal string) string {
	if literal == "-0" {
		return "0"
	}
	return literal
}
