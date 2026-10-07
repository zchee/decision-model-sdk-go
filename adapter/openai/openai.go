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

// Package openai calls the OpenAI Responses API and the Chat Completions API
// of OpenAI and OpenAI-compatible servers.
//
// It ports OpenAIProvider of system-one-adapter-python v0.2.1
// (src/system_one_adapter/providers/openai.py) without a vendor SDK: the
// request body is written and the response body read by this package, and
// the request goes through the module's shared HTTP call, which follows no
// redirect, retries nothing, bounds each request by the Provider's timeout
// and the call's context, and returns the error types of package llm. A
// Provider writes no log.
//
// New reads the environment once, when the Provider is built:
// OPENAI_API_KEY, OPENAI_BASE_URL, OPENAI_ORG_ID and OPENAI_PROJECT_ID, each
// only when its option was not given. Do reads none.
//
// A response with a status outside 200 to 299 is an *llm.StatusError that
// keeps the status, the body as received (empty and not nil for a body that
// is empty or only white space) and, of the response headers, only
// Retry-After, Retry-After-Ms and X-Typesafe-Request-Id: OpenAI's own
// x-request-id is not kept, so a retry policy's predicate cannot read it.
package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/rest"
	"github.com/zchee/decision-model-sdk-go/adapter/llm"
)

// API is the OpenAI API a Provider calls.
type API uint8

const (
	// Auto calls Responses when the base URL's host is api.openai.com, and
	// Chat Completions for any other host (providers/openai.py:122).
	Auto API = iota
	// Responses calls POST {base}/responses.
	Responses
	// ChatCompletions calls POST {base}/chat/completions.
	ChatCompletions
)

// PromptedFormat is what a Chat Completions request carries as
// response_format in prompted mode.
type PromptedFormat uint8

const (
	// FormatNull sends "response_format": null, as upstream does; it is the
	// default.
	FormatNull PromptedFormat = iota
	// FormatOmit leaves the member out, the way OpenAI documents for plain
	// output, for a server that refuses null.
	FormatOmit
)

// The defaults and names of the environment New reads.
const (
	// defaultBaseURL is the OpenAI Python SDK's default base URL.
	defaultBaseURL = "https://api.openai.com/v1"
	// openAIHost is the host for which Auto calls Responses.
	openAIHost = "api.openai.com"

	envAPIKey  = "OPENAI_API_KEY" //nolint:gosec // G101: the name of the variable, not a credential.
	envBaseURL = "OPENAI_BASE_URL"
	envOrg     = "OPENAI_ORG_ID"
	envProject = "OPENAI_PROJECT_ID"
)

// The api names a Provider records in the request's trace, upstream's
// selector values (providers/openai.py:102).
const (
	apiResponses       = "responses"
	apiChatCompletions = "chat_completions"
)

// errAPI is upstream's ValueError text for an API selector that is neither
// of its two names (providers/openai.py:118-119), byte for byte.
var errAPI = errors.New("api must be 'responses' or 'chat_completions'")

// The errors of New, each a fixed text that names the option or the
// variable and never a value.
var (
	errNoKey     = errors.New("openai: no API key: set OPENAI_API_KEY or pass WithAPIKey")
	errFormat    = errors.New("openai: WithPromptedResponseFormat: the format is neither FormatNull nor FormatOmit")
	errNilClient = errors.New("openai: WithHTTPClient: the client is nil")
	errTimeout   = errors.New("openai: WithTimeout: the timeout is not positive")
)

// Provider calls one OpenAI model; it is safe for concurrent use.
// Print %p only on a pointer, where fmt prints its address. On a value,
// fmt's bad-verb diagnostic bypasses Format and may expose private fields.
type Provider struct {
	model    string
	api      API
	format   PromptedFormat
	endpoint *url.URL
	header   http.Header
	client   *rest.Client
}

// Option configures a Provider.
type Option func(*options)

// options are a Provider's settings as its Options set them. A pointer that
// is nil, or points to the empty string, is a setting the options did not
// give, which New takes from the environment or the default.
type options struct {
	key, baseURL, org, project *string
	api                        API
	format                     PromptedFormat
	httpClient                 *http.Client
	httpClientSet              bool
	timeout                    time.Duration
	timeoutSet                 bool
}

// New returns a Provider for model, reading OPENAI_API_KEY, OPENAI_BASE_URL,
// OPENAI_ORG_ID and OPENAI_PROJECT_ID once, each only when its option was
// not given. An option given with the empty string counts as not given, so
// its variable applies.
//
// A variable that is set to the empty string counts as not set, and so
// does a key from OPENAI_API_KEY that is only white space: the key, from
// the option or the variable, is sent with the white space around it
// removed (strings.TrimSpace). Without a key New fails, and without a base URL the default,
// https://api.openai.com/v1, applies; an organization or project that ends
// up empty is not sent. Model returns model as it is given.
//
// New fails for an API outside Auto, Responses and ChatCompletions (with
// upstream's text "api must be 'responses' or 'chat_completions'"), a
// PromptedFormat outside its two constants, a nil WithHTTPClient, a
// WithTimeout that is not positive, no key, and a base URL that is not an
// absolute http or https URL with a host. Its errors name the option or the
// variable, never a value.
func New(model string, opts ...Option) (*Provider, error) {
	o := options{timeout: rest.DefaultTimeout}
	for _, opt := range opts {
		opt(&o)
	}
	switch {
	case o.api > ChatCompletions:
		return nil, errAPI
	case o.format > FormatOmit:
		return nil, errFormat
	case o.httpClientSet && o.httpClient == nil:
		return nil, errNilClient
	case o.timeoutSet && o.timeout <= 0:
		return nil, errTimeout
	}
	key := strings.TrimSpace(setting(o.key, envAPIKey))
	if key == "" {
		return nil, errNoKey
	}
	base, err := baseURL(o.baseURL)
	if err != nil {
		return nil, err
	}
	header := http.Header{"Authorization": {"Bearer " + key}}
	if org := setting(o.org, envOrg); org != "" {
		header.Set("OpenAI-Organization", org)
	}
	if project := setting(o.project, envProject); project != "" {
		header.Set("OpenAI-Project", project)
	}
	api := o.api
	if api == Auto {
		api = ChatCompletions
		if strings.EqualFold(base.Hostname(), openAIHost) {
			api = Responses
		}
	}
	path := "/chat/completions"
	if api == Responses {
		path = "/responses"
	}
	return &Provider{
		model:    model,
		api:      api,
		format:   o.format,
		endpoint: endpoint(base, path),
		header:   header,
		client:   rest.New(rest.Config{HTTPClient: o.httpClient, Timeout: o.timeout}),
	}, nil
}

// setting returns *opt when the option was given with a value that is not
// empty, else the value of the environment variable name, "" when it is
// not set.
func setting(opt *string, name string) string {
	if opt != nil && *opt != "" {
		return *opt
	}
	v, _ := rest.Env(name)
	return v
}

// baseURL returns the base URL New uses: the option's, else
// OPENAI_BASE_URL's, else the default, an empty value counting as none. Its
// error names where the refused URL came from and holds no part of it.
func baseURL(opt *string) (*url.URL, error) {
	raw, src := setting(opt, envBaseURL), envBaseURL
	if opt != nil && *opt != "" {
		src = "WithBaseURL"
	}
	if raw == "" {
		raw = defaultBaseURL
	}
	u, err := rest.ParseURL(raw)
	if err != nil {
		return nil, errors.New("openai: the base URL of " + src + " is not an absolute http or https URL with a host")
	}
	return u, nil
}

// endpoint returns the URL of the operation at path under base: base's path
// and path joined by one slash, whatever slashes base's path ends with,
// with base's userinfo and query kept and its fragment dropped.
func endpoint(base *url.URL, path string) *url.URL {
	u := *base
	u.Fragment, u.RawFragment = "", ""
	u.Path = strings.TrimRight(base.Path, "/") + path
	if base.RawPath != "" {
		u.RawPath = strings.TrimRight(base.RawPath, "/") + path
	}
	return &u
}

// WithAPIKey sets the key sent as Authorization: Bearer (default
// OPENAI_API_KEY), with the white space around it removed. A key that is
// empty or only white space counts as not given: OPENAI_API_KEY applies,
// and without it New fails.
func WithAPIKey(key string) Option {
	return func(o *options) {
		// The Option may be applied by several New calls at once, so it
		// writes a copy and never the key it captured.
		k := strings.TrimSpace(key)
		o.key = &k
	}
}

// WithBaseURL sets the base URL (default OPENAI_BASE_URL, else
// https://api.openai.com/v1). An empty u counts as not given:
// OPENAI_BASE_URL applies, else the default. A request goes to its
// path with /responses or /chat/completions appended after one slash. Its
// query is kept and sent with every request, and its fragment is dropped.
// Its userinfo stays in the request's URL, which the HTTP client's
// transport receives as it is; net/http adds no Authorization: Basic header
// from it, because the request carries Authorization: Bearer.
func WithBaseURL(u string) Option {
	return func(o *options) { o.baseURL = &u }
}

// WithOrganization sets the OpenAI-Organization header (default
// OPENAI_ORG_ID; not sent when empty). An empty id counts as not given:
// OPENAI_ORG_ID applies.
func WithOrganization(id string) Option {
	return func(o *options) { o.org = &id }
}

// WithProject sets the OpenAI-Project header (default OPENAI_PROJECT_ID;
// not sent when empty). An empty id counts as not given: OPENAI_PROJECT_ID
// applies.
func WithProject(id string) Option {
	return func(o *options) { o.project = &id }
}

// WithAPI chooses the API (default Auto).
func WithAPI(a API) Option {
	return func(o *options) { o.api = a }
}

// WithPromptedResponseFormat chooses the response_format of a Chat
// Completions request in prompted mode (default FormatNull).
func WithPromptedResponseFormat(f PromptedFormat) Option {
	return func(o *options) { o.format = f }
}

// WithHTTPClient sets a borrowed client (default: an owned client over a
// clone of http.DefaultTransport). The Provider calls through a copy of it,
// made when New is called, and Close leaves it alone.
//
// No redirect is followed: a response of status 300 to 399 is an
// *llm.StatusError. A client whose CheckRedirect is set keeps that policy,
// and then decides where the request and its Authorization header may go:
// net/http drops Authorization only on a redirect to another host.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) { o.httpClient, o.httpClientSet = c, true }
}

// WithTimeout bounds each request, from its start to the end of the
// response body (default 600 s). A d that is not positive is an error of
// New.
func WithTimeout(d time.Duration) Option {
	return func(o *options) { o.timeout, o.timeoutSet = d, true }
}

// Model returns the model name New was given.
func (p *Provider) Model() string { return p.model }

// API returns the API the Provider calls, Auto resolved.
func (p *Provider) API() API { return p.api }

// String returns the model alone, as "openai.Provider(<model>)". It is what
// %v, %+v and %s print for a Provider value or a pointer to one, so that
// they print nothing of the headers, which hold the key, or of the base
// URL, whose userinfo and query may carry a credential, as fmt's
// field-by-field form would. A nil *Provider prints as <nil>.
func (p Provider) String() string {
	return "openai.Provider(" + p.model + ")"
}

// GoString returns what %#v prints for p, a Provider value or a pointer to
// one, with the model only: the headers hold the key and the base URL may
// carry a credential in its userinfo or query, so neither is printed. A nil
// *Provider prints as <nil>.
func (p Provider) GoString() string {
	return "openai.Provider{Model:" + p.model + "}"
}

// Format prints String for every verb except %#v, which prints GoString.
// Width, precision and flags are ignored; headers and endpoints are never
// printed. The fmt package handles %T and %p itself before calling Format.
func (p Provider) Format(f fmt.State, verb rune) {
	text := p.String()
	if verb == 'v' && f.Flag('#') {
		text = p.GoString()
	}
	_, _ = fmt.Fprint(f, text)
}

// Do performs one request: it writes the body, records it in req.Trace,
// sends it with Authorization: Bearer, records the 2xx response body and
// its finish reason in req.Trace before it checks them, and returns the
// answer text and the token counts, a count being unknown when the
// response does not report it.
//
// It returns an *llm.NonAnswerError, with upstream's text, for a Chat
// Completions finish reason other than "stop" or null, a Responses status
// other than "completed", and a refusal part in a Responses message. A 2xx
// body that is not a response of the API is an error of a fixed text that
// quotes nothing of the body. The other failures are those of the module's
// HTTP call: the context's own error when ctx is cancelled, an
// *llm.TimeoutError, an *llm.ConnectionError, and an *llm.StatusError that
// keeps of the response headers only Retry-After, Retry-After-Ms and
// X-Typesafe-Request-Id.
func (p *Provider) Do(ctx context.Context, req *llm.Request) (*llm.Result, error) {
	if p.api == Responses {
		body, err := responsesBody(p.model, req)
		if err != nil {
			return nil, err
		}
		req.Trace.RecordRequest(apiResponses, body)
		data, err := p.client.Post(ctx, p.endpoint, p.header, body)
		if err != nil {
			return nil, err
		}
		return responsesResult(data, req.Trace)
	}
	body, err := chatBody(p.model, req, p.format)
	if err != nil {
		return nil, err
	}
	req.Trace.RecordRequest(apiChatCompletions, body)
	data, err := p.client.Post(ctx, p.endpoint, p.header, body)
	if err != nil {
		return nil, err
	}
	return chatResult(data, req.Trace)
}

// Close closes the idle connections of an owned client and does nothing to
// a borrowed one. It returns nil.
func (p *Provider) Close() error { return p.client.Close() }
