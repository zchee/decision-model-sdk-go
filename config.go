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
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ClientOption configures a client.
//
// An option only records its setting; the client checks every setting
// together when it is built and reports the first one it cannot use as a
// [*ConfigError], before anything is sent. So the order of options of
// different kinds never decides whether a set of them is accepted. Among
// options of one kind the last one wins and is the only one checked:
// WithTimeout(0) followed by WithTimeout(time.Second) is accepted, the
// reverse is not. [WithHeader] keeps the last value per header name, and
// checks every call. A nil ClientOption is ignored.
//
// A setting that no option gives is read from its environment variable
// ([APIKeyEnv], [BaseURLEnv], [DefaultModelEnv]), trimmed of leading and
// trailing whitespace; a variable that is unset or blank counts as unset, and
// the SDK's default ([DefaultBaseURL], [DefaultModel]) applies. An option
// always wins over the environment, even when its value is unusable: the
// environment is not consulted for a setting an option gave.
type ClientOption func(*options)

// options is what a list of [ClientOption] values recorded, before any of it is
// checked. A nil pointer is a setting no option gave.
type options struct {
	apiKey           *string
	baseURL          *string
	model            *string
	timeout          *time.Duration
	noTimeout        bool
	connectTimeout   *time.Duration
	maxResponseBytes *int64
	headers          []headerOption
	userAgentProduct *string
	noRuntimeHeader  bool
	logger           *slog.Logger
	hideEndpointHost bool
	// transport is what the transport options (transport.go) recorded.
	transport transportOptions
}

// headerOption is one [WithHeader] call.
type headerOption struct {
	name, value string
}

// WithAPIKey sets the API key, sent as Authorization: Bearer <key>. Without
// it the key is read from [APIKeyEnv].
//
// The key is trimmed of leading and trailing whitespace as Python's
// str.strip() trims it; what is left must be printable ASCII without
// whitespace, and must not be empty (py:_core/config.py:26-33). A key given
// here that fails either rule is refused: the environment is not consulted
// in its place. No error repeats the key.
func WithAPIKey(key string) ClientOption {
	return func(o *options) { o.apiKey = new(key) }
}

// WithBaseURL sets the API base URL, such as https://api.typesafe.ai. Without
// it the URL is read from [BaseURLEnv], and [DefaultBaseURL] applies when that
// is unset.
//
// Trailing slashes are removed, and a path the URL keeps is a prefix of the
// API paths: https://example.test/prefix/ sends to
// https://example.test/prefix/v1/systemone. The URL must be an absolute http
// or https URL with a host, and without credentials, a query or a fragment;
// the API key is passed with [WithAPIKey] only. No error repeats the URL.
func WithBaseURL(rawURL string) ClientOption {
	return func(o *options) { o.baseURL = new(rawURL) }
}

// WithModel sets the model a request names when the call names none. Without
// it the model is read from [DefaultModelEnv], and [DefaultModel] applies
// when that is unset.
//
// The model is sent as given, without trimming. An empty or blank model is
// refused rather than sent, where typesafe-sdk-python would send it.
func WithModel(model string) ClientOption {
	return func(o *options) { o.model = new(model) }
}

// WithTimeout sets the deadline of each attempt of a request, from sending
// the request to reading the whole response; [DefaultTimeout] unless set. It
// must be positive; [WithNoTimeout] removes the deadline, and the two cannot
// be used together.
func WithTimeout(d time.Duration) ClientOption {
	return func(o *options) { o.timeout = new(d) }
}

// WithNoTimeout removes the deadline of each attempt: an attempt then ends
// when its response is read, when the call's context is done, or when the
// connection fails. It cannot be used together with [WithTimeout].
func WithNoTimeout() ClientOption {
	return func(o *options) { o.noTimeout = true }
}

// WithConnectTimeout sets the deadline for opening a connection, TCP and TLS
// together, inside the deadline of the attempt that opens it;
// [DefaultConnectTimeout] unless set. It must be positive.
func WithConnectTimeout(d time.Duration) ClientOption {
	return func(o *options) { o.connectTimeout = new(d) }
}

// WithMaxResponseBytes sets the largest response body a request reads, in
// bytes; [DefaultMaxResponseBytes] (16 MiB) unless set. It must be at least 1,
// since every response carries a body, and at most 1 GiB.
func WithMaxResponseBytes(n int64) ClientOption {
	return func(o *options) { o.maxResponseBytes = new(n) }
}

// WithHeader sets a header sent on every request. A later WithHeader of the
// same name, compared without regard to case, replaces an earlier one, as a
// per-call header replaces a default one in typesafe-sdk-python
// (py:_core/transport.py:117). Its default headers differ: a mapping that
// holds two spellings of one name, such as X-Team and x-team, sends both.
//
// The SDK's own headers always win, as they do in typesafe-sdk-python, which
// writes them over the caller's (py:_core/transport.py:116-127): a caller's
// Authorization, Accept, User-Agent, X-TypeSafe-SDK, X-TypeSafe-Runtime and
// Content-Type are dropped, and so is X-TypeSafe-Retry-Count, which the SDK
// sets on retries only (py:_core/transport.py:118). [WithUserAgentProduct]
// and [WithRuntimeHeader] are the only ways to change what User-Agent and
// X-TypeSafe-Runtime carry. The headers that frame a message or manage its
// connection belong to the transport and are dropped too: Content-Length,
// Transfer-Encoding, Connection, Proxy-Connection, Keep-Alive, Upgrade, TE,
// Trailer and Host. Each dropped header is logged at [slog.LevelDebug] by
// name, never with its value.
//
// The name must be a valid HTTP field name (RFC 9110, section 5.6.2) and the
// value a valid field value (RFC 9110, section 5.5), or the client is not
// built. A name that contains the API key, without regard to case, is
// refused too, as when the two arguments are swapped, provided the key is at
// least 8 bytes long: a shorter key, a test's dummy such as "test", occurs in
// ordinary names. No error repeats a value, which is where a caller puts a
// token, or a name that holds the key.
func WithHeader(name, value string) ClientOption {
	return func(o *options) { o.headers = append(o.headers, headerOption{name: name, value: value}) }
}

// WithUserAgentProduct names the application in the User-Agent header, in
// front of the SDK's own product, the more significant product first (RFC
// 9110, section 10.1.5): "my-app/1.2.0" sends
// User-Agent: my-app/1.2.0 typesafe-sdk-go/<Version>. Unset, User-Agent is
// typesafe-sdk-go/<Version> alone, the form typesafe-sdk-python sends its own
// name in (py:_core/transport.py:123). X-TypeSafe-SDK always names the SDK
// alone.
//
// The product must be name/version, both parts tokens (RFC 9110, section
// 5.6.2: letters, digits and !#$%&'*+-.^_`|~), with exactly one "/" and at
// most 64 bytes in all. A product that breaks those rules is refused by name
// of the rule; the error does not repeat it.
func WithUserAgentProduct(product string) ClientOption {
	return func(o *options) { o.userAgentProduct = new(product) }
}

// WithRuntimeHeader sets whether requests carry X-TypeSafe-Runtime, which
// names the Go release, operating system and architecture the program was
// built for, such as go/1.27.1 (linux; amd64). The default is true; false
// leaves the header out of every request. X-TypeSafe-SDK is sent either way.
func WithRuntimeHeader(send bool) ClientOption {
	return func(o *options) { o.noRuntimeHeader = !send }
}

// WithLogger sets the logger the client writes its records to. The default,
// and what a nil logger means, discards every record. The client never
// configures the logger's level: typesafe-sdk-python's TYPESAFE_LOG_LEVEL is
// not read.
func WithLogger(logger *slog.Logger) ClientOption {
	return func(o *options) { o.logger = logger }
}

// WithLogEndpointHost sets whether log records name the full endpoint URL.
// The default is true; with false they name only the API path, /v1/systemone
// or /v1/models, without the scheme, the host or the base URL's path prefix.
func WithLogEndpointHost(log bool) ClientOption {
	return func(o *options) { o.hideEndpointHost = !log }
}

// collectOptions applies opts in order, skipping nil ones, and returns what
// they recorded.
func collectOptions(opts []ClientOption) options {
	var o options
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// config is a client's settings once every source has been consulted and
// every value checked. Nothing changes it after [options.resolve] returns,
// so requests read it without locking.
type config struct {
	// apiKey is the key after trimming, which the redaction of logged
	// headers looks for in their values.
	apiKey string

	// systemOneURL and modelsURL are the two endpoints, shared read-only by
	// every request.
	systemOneURL *url.URL
	modelsURL    *url.URL

	// systemOneLog and modelsLog are how log records name the endpoints: the
	// URL, or the API path alone under WithLogEndpointHost(false).
	systemOneLog string
	modelsLog    string

	// model is the model a request names when the call names none.
	model string

	// timeout is the deadline of each attempt; zero means none
	// (WithNoTimeout), since a zero WithTimeout is refused.
	timeout time.Duration

	// connectTimeout is the deadline for opening a connection.
	connectTimeout time.Duration

	// maxResponseBytes is the largest response body a request reads.
	maxResponseBytes int64

	// logger receives the client's records; it is never nil.
	logger *slog.Logger

	// systemOneHeader and modelsHeader are the headers of every POST
	// /v1/systemone and GET /v1/models request, built once. A request clones
	// its template and never writes to it.
	systemOneHeader http.Header
	modelsHeader    http.Header

	// transport carries every request of the client.
	transport *transport
}

// resolve checks what o recorded, fills every setting o left unset from the
// environment that getenv reads (NewClient passes [os.Getenv]) and then from
// the defaults, and returns the resulting configuration. The first setting
// that cannot be used is reported as a *ConfigError, in the order key, base
// URL, model, timeouts, response limit, User-Agent product, headers,
// transport. The transport is built last, once every other setting is known
// to be usable.
func (o *options) resolve(getenv func(string) string) (*config, error) {
	key, err := resolveAPIKey(o.apiKey, getenv)
	if err != nil {
		return nil, err
	}
	systemOne, models, err := resolveEndpoints(o.baseURL, getenv)
	if err != nil {
		return nil, err
	}
	model, err := resolveModel(o.model, getenv)
	if err != nil {
		return nil, err
	}
	timeout, err := o.resolveTimeout()
	if err != nil {
		return nil, err
	}
	connectTimeout := DefaultConnectTimeout
	if o.connectTimeout != nil {
		if *o.connectTimeout <= 0 {
			return nil, newConfigError("The connect timeout passed to WithConnectTimeout must be positive.")
		}
		connectTimeout = *o.connectTimeout
	}
	maxResponseBytes := int64(DefaultMaxResponseBytes)
	if o.maxResponseBytes != nil {
		switch n := *o.maxResponseBytes; {
		case n < 1:
			return nil, newConfigError("The limit passed to WithMaxResponseBytes must be at least 1: every response carries a body.")
		case n > maxMaxResponseBytes:
			return nil, newConfigError("The limit passed to WithMaxResponseBytes must be at most 1 GiB (" + strconv.Itoa(maxMaxResponseBytes) + " bytes).")
		}
		maxResponseBytes = *o.maxResponseBytes
	}
	userAgent := sdkIdentifier
	if o.userAgentProduct != nil {
		if rule := productRule(*o.userAgentProduct); rule != "" {
			return nil, newConfigError("The product passed to WithUserAgentProduct must be a product token, name/version (RFC 9110, section 10.1.5): " + rule + ".")
		}
		userAgent = *o.userAgentProduct + " " + sdkIdentifier
	}
	logger := o.logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	modelsHeader, err := o.headerTemplate(logger, key, userAgent)
	if err != nil {
		return nil, err
	}
	systemOneHeader := modelsHeader.Clone()
	systemOneHeader.Set(headerContentType, jsonContentType)
	tr, err := o.transport.build(systemOne, connectTimeout, o.connectTimeout != nil, logger)
	if err != nil {
		return nil, err
	}

	c := &config{
		apiKey:           key,
		systemOneURL:     systemOne,
		modelsURL:        models,
		systemOneLog:     systemOne.String(),
		modelsLog:        models.String(),
		model:            model,
		timeout:          timeout,
		connectTimeout:   connectTimeout,
		maxResponseBytes: maxResponseBytes,
		logger:           logger,
		systemOneHeader:  systemOneHeader,
		modelsHeader:     modelsHeader,
		transport:        tr,
	}
	if o.hideEndpointHost {
		c.systemOneLog, c.modelsLog = systemOnePath, modelsPath
	}
	return c, nil
}

// resolveAPIKey returns the API key from explicit, or from [APIKeyEnv] when
// explicit is nil, trimmed and checked as py:_core/config.py:26-33 checks
// it. An explicit key that is blank is the missing-key error; the environment
// is not consulted for it. No message repeats the key; each names where the
// key came from.
func resolveAPIKey(explicit *string, getenv func(string) string) (string, error) {
	var key string
	if explicit != nil {
		key = strings.TrimFunc(*explicit, isPythonSpace)
		if key == "" {
			return "", newConfigError("The API key passed to WithAPIKey is empty; the " + APIKeyEnv + " environment variable is not read when WithAPIKey is given.")
		}
	} else {
		key = envValue(getenv, APIKeyEnv)
		if key == "" {
			return "", newConfigError("No API key was provided. Pass WithAPIKey or set the " + APIKeyEnv + " environment variable.")
		}
	}
	for i := range len(key) {
		if c := key[i]; c < '!' || c > '~' {
			source := "in the " + APIKeyEnv + " environment variable"
			if explicit != nil {
				source = "passed to WithAPIKey"
			}
			return "", newConfigError("The API key " + source + " must contain only printable ASCII characters without whitespace.")
		}
	}
	return key, nil
}

// resolveEndpoints returns the System One and models endpoint URLs under the
// base URL from explicit, or from [BaseURLEnv] when explicit is nil, or
// [DefaultBaseURL] when that is unset.
func resolveEndpoints(explicit *string, getenv func(string) string) (systemOne, models *url.URL, err error) {
	raw, source := DefaultBaseURL, "The default base URL"
	if explicit != nil {
		raw, source = *explicit, "The base URL passed to WithBaseURL"
	} else if v := envValue(getenv, BaseURLEnv); v != "" {
		raw, source = v, "The base URL in the "+BaseURLEnv+" environment variable"
	}
	// As py:_core/config.py:61 strips them, before anything else looks at
	// the URL.
	raw = strings.TrimRight(raw, "/")
	if rule := baseURLRule(raw); rule != "" {
		return nil, nil, newConfigError(source + " " + rule + ".")
	}
	// The API paths are appended to the text, as Python appends them
	// (py:_core/transport.py:134), so the caller's own escaping of the prefix
	// is kept. A valid base followed by a fixed path always parses.
	if systemOne, err = url.Parse(raw + systemOnePath); err != nil {
		return nil, nil, newConfigError(source + " is not a valid URL.")
	}
	if models, err = url.Parse(raw + modelsPath); err != nil {
		return nil, nil, newConfigError(source + " is not a valid URL.")
	}
	return systemOne, models, nil
}

// baseURLRule returns the rule raw breaks as the end of a sentence, or ""
// when it is an absolute http or https URL with a host and without
// credentials, a query or a fragment. It never repeats raw: its userinfo, if
// any, is a credential.
func baseURLRule(raw string) string {
	// The first '?' or '#' of a URL always starts its query or fragment, so a
	// search of the text is exact, and it catches the empty ones ("x?",
	// "x#") that url.URL cannot tell from absent.
	if strings.Contains(raw, "#") {
		return "must not carry a fragment ('#...')"
	}
	if strings.Contains(raw, "?") {
		return "must not carry a query ('?...')"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "is not a valid URL"
	}
	switch {
	case u.Scheme == "":
		return "must be absolute, with a scheme and a host, such as " + DefaultBaseURL
	case u.Scheme != "http" && u.Scheme != "https":
		return "must use http or https"
	case u.User != nil:
		return "must not carry credentials; pass the API key with WithAPIKey instead"
	case u.Hostname() == "":
		return "has an empty host"
	}
	return ""
}

// resolveModel returns the model from explicit, or from [DefaultModelEnv]
// when explicit is nil, or [DefaultModel] when that is unset. An explicit
// model is taken as given, as Python takes it, except that a blank one is
// refused rather than sent.
func resolveModel(explicit *string, getenv func(string) string) (string, error) {
	model, source := DefaultModel, "default model"
	if explicit != nil {
		if strings.TrimFunc(*explicit, isPythonSpace) == "" {
			return "", newConfigError("The model passed to WithModel is empty; leave WithModel out to use " + DefaultModelEnv + " or " + DefaultModel + ".")
		}
		model, source = *explicit, "model passed to WithModel"
	} else if v := envValue(getenv, DefaultModelEnv); v != "" {
		model, source = v, "model in the "+DefaultModelEnv+" environment variable"
	}
	if !utf8.ValidString(model) {
		return "", newConfigError("The " + source + " is not valid UTF-8.")
	}
	return model, nil
}

// resolveTimeout returns the deadline of each attempt, zero for none.
func (o *options) resolveTimeout() (time.Duration, error) {
	switch {
	case o.timeout != nil && o.noTimeout:
		return 0, newConfigError("WithTimeout and WithNoTimeout cannot be used together: the first sets a timeout, the second removes it.")
	case o.noTimeout:
		return 0, nil
	case o.timeout != nil:
		if *o.timeout <= 0 {
			return 0, newConfigError("The timeout passed to WithTimeout must be positive; use WithNoTimeout for no deadline.")
		}
		return *o.timeout, nil
	}
	return DefaultTimeout, nil
}

// headerTemplate returns the headers of a request without a body: the
// caller's headers without the ones the SDK or the transport owns, then the
// SDK's own (py:_core/transport.py:116-127). Each dropped header is logged
// by name at debug level. A name that holds the key, without regard to case,
// is refused before anything else is checked, when the key is at least
// [minKeyNeedleBytes] long: names are logged and sent as they are, and
// redaction looks for the key in values only.
func (o *options) headerTemplate(logger *slog.Logger, key, userAgent string) (http.Header, error) {
	h := make(http.Header, len(o.headers)+5)
	lowerKey := strings.ToLower(key)
	for i, ho := range o.headers {
		if keyNeedle(key) && strings.Contains(strings.ToLower(ho.name), lowerKey) {
			return nil, newConfigError("The name given to WithHeader call " + strconv.Itoa(i+1) + " contains the API key, so it is not shown; pass the key with WithAPIKey only.")
		}
		if !validFieldName(ho.name) {
			return nil, newConfigError("The name given to WithHeader call " + strconv.Itoa(i+1) + " is not a valid HTTP field name (RFC 9110, section 5.6.2); it is not shown, since it may hold a credential.")
		}
		name := http.CanonicalHeaderKey(ho.name)
		if !validFieldValue(ho.value) {
			return nil, newConfigError("The value given to WithHeader for " + name + " is not a valid HTTP field value (RFC 9110, section 5.5).")
		}
		if reason, owned := sdkOwnedHeaders[name]; owned {
			logger.LogAttrs(context.Background(), slog.LevelDebug, "config: header dropped",
				slog.String("header", name), slog.String("reason", reason))
			continue
		}
		h[name] = []string{ho.value}
	}
	h.Set(headerAuthorization, "Bearer "+key)
	h.Set(headerAccept, jsonContentType)
	h.Set(headerUserAgent, userAgent)
	h.Set(headerSDK, sdkIdentifier)
	if !o.noRuntimeHeader {
		h.Set(headerRuntime, runtimeIdentifier)
	}
	return h, nil
}

// Why a caller's header is dropped, as the debug record reports it.
const (
	reasonSDKHeader       = "set by the SDK"
	reasonRetryHeader     = "set by the SDK on retries only"
	reasonTransportHeader = "belongs to the transport"
)

// sdkOwnedHeaders maps the canonical name of every header a caller cannot set
// to the reason it is dropped. A per-call header goes through the same map.
var sdkOwnedHeaders = func() map[string]string {
	m := make(map[string]string, 16)
	for _, name := range []string{headerAuthorization, headerAccept, headerUserAgent, headerSDK, headerRuntime, headerContentType} {
		m[http.CanonicalHeaderKey(name)] = reasonSDKHeader
	}
	m[http.CanonicalHeaderKey(headerRetryCount)] = reasonRetryHeader
	// They frame a message or manage its connection (RFC 9110, section 7.6.1;
	// RFC 9113, section 8.2.2): a caller's value disagrees with the body the
	// SDK sends or with how the transport runs the connection, and net/http
	// ignores Host in a request's header map.
	for _, name := range []string{"Content-Length", "Transfer-Encoding", "Connection", "Proxy-Connection", "Keep-Alive", "Upgrade", "TE", "Trailer", "Host"} {
		m[http.CanonicalHeaderKey(name)] = reasonTransportHeader
	}
	return m
}()

// maxUserAgentProductBytes is the longest product WithUserAgentProduct
// accepts.
const maxUserAgentProductBytes = 64

// productRule returns the rule product breaks as the end of a sentence, or ""
// when it is name/version with both parts tokens and at most
// maxUserAgentProductBytes bytes. The general rules come first, so a pasted
// line is reported as holding whitespace rather than as lacking a version.
func productRule(product string) string {
	switch {
	case product == "":
		return "it is empty"
	case len(product) > maxUserAgentProductBytes:
		return "it is longer than " + strconv.Itoa(maxUserAgentProductBytes) + " bytes"
	}
	for i := range len(product) {
		switch c := product[i]; {
		case c >= utf8.RuneSelf:
			return "it contains a character that is not ASCII"
		case c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r':
			return "it contains whitespace"
		case c < ' ' || c == 0x7f:
			return "it contains a control character"
		}
	}
	name, version, ok := strings.Cut(product, "/")
	switch {
	case !ok:
		return "it has no '/' between the name and the version"
	case strings.Contains(version, "/"):
		return "it has more than one '/'"
	case name == "":
		return "the name before the '/' is empty"
	case version == "":
		return "the version after the '/' is empty"
	case !isToken(name) || !isToken(version):
		return "it contains a character a token cannot hold (RFC 9110, section 5.6.2)"
	}
	return ""
}

// validFieldName reports whether name is a token (RFC 9110, section 5.6.2),
// which is what a field name must be (section 5.1).
func validFieldName(name string) bool {
	return name != "" && isToken(name)
}

// isToken reports whether every byte of s is a tchar (RFC 9110, section
// 5.6.2): a letter, a digit or one of !#$%&'*+-.^_`|~. Written out here
// because the root package imports no copy of httpguts.
func isToken(s string) bool {
	for i := range len(s) {
		c := s[i]
		if ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') || strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0 {
			continue
		}
		return false
	}
	return true
}

// validFieldValue reports whether value is a valid field value (RFC 9110,
// section 5.5), as httpguts.ValidHeaderFieldValue decides it: no control
// character other than horizontal tab, and no DEL; bytes from 0x80 up
// (obs-text) are allowed.
func validFieldValue(value string) bool {
	for i := range len(value) {
		if c := value[i]; (c < ' ' && c != '\t') || c == 0x7f {
			return false
		}
	}
	return true
}

// envValue returns the variable name as getenv finds it, trimmed as Python's
// str.strip() trims (py:_core/config.py:23); "" means unset or blank.
func envValue(getenv func(string) string, name string) string {
	return strings.TrimFunc(getenv(name), isPythonSpace)
}

// isPythonSpace reports whether Python's str.strip() trims r: Unicode white
// space, which [unicode.IsSpace] matches, and the four ASCII separators
// U+001C to U+001F, which Python counts as white space and Go does not.
func isPythonSpace(r rune) bool {
	return unicode.IsSpace(r) || ('\x1c' <= r && r <= '\x1f')
}
