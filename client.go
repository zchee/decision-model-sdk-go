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
	"errors"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// LevelTrace is the log level of the records that carry request and response
// bodies, below [slog.LevelDebug]: a body holds the caller's state, which
// may be personal data, so a handler shows it only when set to this level
// or lower. typesafe-sdk-python logs bodies at DEBUG.
const LevelTrace = slog.LevelDebug - 4

// Client calls the TypeSafe API. It is safe for concurrent use by multiple
// goroutines; build it with [NewClient] and release its connections with
// [Client.Close]. The zero Client is not usable.
//
// The first attempt of every call hands the transport the client's own
// header map, shared by every call and never copied, so a RoundTripper
// given with [WithRoundTripper] must not modify a request, as the
// [net/http.RoundTripper] contract already requires.
type Client struct {
	// cfg holds the key and the header templates, two pointers away from
	// the Client (ruling R66). fmt prints a pointer field as an address,
	// except under a verb a pointer does not take, such as %s or %q, where
	// it prints the value the field points to: that value is configRef,
	// whose one field is again a pointer, printed as an address.
	cfg *configRef

	// systemOneEndpoint and modelsEndpoint name the endpoints in errors: the
	// method and the URL (endpointOf).
	systemOneEndpoint string
	modelsEndpoint    string

	closed   atomic.Bool
	attempts atomic.Uint64

	// random is the jitter source of the retry backoff; nil means
	// math/rand/v2's Float64. Tests set it before the first call.
	random func() float64
}

// configRef holds a client's *config; see Client.cfg. The configuration's
// fields are promoted through it.
type configRef struct {
	*config
}

// WithPretouch prepares the JSON encoder for each type before the first
// call that encodes a state or an extra body value of it, so that the call
// does not pay the one-time compilation the encoder makes per type (which
// grows with the type's size). It changes nothing on the wire. The types are
// prepared when the client is built, after every other option is checked;
// a nil type, or one the encoder refuses (a map whose keys cannot be JSON
// object keys, for example), fails NewClient with a [*ConfigError]. Types
// from several WithPretouch options are all prepared.
func WithPretouch(types ...reflect.Type) ClientOption {
	return func(o *options) { o.pretouch = append(o.pretouch, types...) }
}

// NewClient builds a client from opts. Unless an option supplies the
// transport ([WithHTTPTransport], [WithRoundTripper]), the client builds its
// own: HTTP/2 on one connection per client for an https base URL (HTTP/1.1
// or HTTP/2 for http; [WithHTTPVersion]), through the proxy the environment
// names ([WithProxy]). Every option is checked before the client is built,
// and the first that cannot be used is reported as a [*ConfigError];
// nothing is sent.
func NewClient(opts ...ClientOption) (*Client, error) {
	o := collectOptions(opts)
	cfg, err := o.resolve(os.Getenv)
	if err != nil {
		return nil, err
	}
	for i, t := range o.pretouch {
		if err := codec.Pretouch(t); err != nil {
			name := "nil"
			if t != nil {
				name = t.String()
			}
			return nil, newConfigError("The type "+strconv.Itoa(i+1)+" passed to WithPretouch, "+safeName(name)+
				", cannot be prepared for the JSON encoder: "+safeMessage(err.Error())+".", err)
		}
	}
	return &Client{
		cfg:               &configRef{cfg},
		systemOneEndpoint: endpointOf(http.MethodPost, cfg.systemOneURL),
		modelsEndpoint:    endpointOf(http.MethodGet, cfg.modelsURL),
	}, nil
}

// Close releases the client's network resources: it closes the idle
// connections of the transport the client built or cloned
// ([WithHTTPTransport]), and calls Close once on a [WithRoundTripper]
// transport that implements [io.Closer], returning its error. Calls in
// flight finish on their own connections. Close is idempotent: a second
// Close does nothing and returns nil. Every call made after Close fails
// with a [*ConfigError] wrapping [ErrClientClosed].
func (c *Client) Close() error {
	if err := c.built(); err != nil {
		return err
	}
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	return c.cfg.transport.close()
}

// built returns a *ConfigError for a Client that NewClient did not build.
func (c *Client) built() error {
	if c == nil || c.cfg == nil || c.cfg.config == nil {
		return newConfigError("The client was not built by NewClient.")
	}
	return nil
}

// usable returns the error a call fails with before it starts: a client that
// NewClient did not build, or one that has been closed.
func (c *Client) usable() error {
	if err := c.built(); err != nil {
		return err
	}
	if c.closed.Load() {
		return newClientClosedError()
	}
	return nil
}

// Stats counts what a client has done since it was built.
type Stats struct {
	// Dials counts the connections the SDK's transport (its own, or the
	// clone of WithHTTPTransport) handed to a request for the first time,
	// re-dials and the transport's internal replays included; zero under
	// WithRoundTripper.
	Dials uint64
	// Attempts counts the attempts the client sent: one per request that
	// reached the transport, whatever came of it. The transport's own
	// replay of a request inside one attempt is not counted.
	Attempts uint64
}

// Stats returns the client's counters. A Client that NewClient did not build
// counts nothing.
func (c *Client) Stats() Stats {
	if c.built() != nil {
		return Stats{}
	}
	return Stats{Dials: c.cfg.transport.stats().Dials, Attempts: c.attempts.Load()}
}

// WarmUp lists the models once, so that the connection, and the HTTP/2
// session a burst of calls then shares, is ready before the first call that
// matters. It returns the error of that call.
func (c *Client) WarmUp(ctx context.Context) error {
	_, err := c.Models().List(ctx)
	return err
}

// SystemOne asks the questions qs about state and returns the answers:
// POST /v1/systemone with {"state": state, "model": ..., "questions": ...}.
//
// state is text, a JSON object or an array: a string, a map, a slice, a
// struct, a [RawJSON] sent as it is, or a [Content]. A nil, a number or a
// boolean is refused, as is a plain []byte, whose intent is ambiguous. The
// body is encoded once per call, before anything is sent, and every attempt
// sends the same bytes; a state that cannot be encoded fails with an
// [*InvalidRequestError], and a missing question set with a [*ConfigError].
// A float inside the state keeps the encoder's spelling (3.0 is sent as 3),
// and a map's members go out in Go's iteration order; a struct or RawJSON
// gives stable bytes.
//
// opts override the client's settings for this call; see [CallOption].
//
// Each attempt has its own deadline ([WithTimeout], [Timeout]) under ctx. A
// response with a status outside 2xx is an [*APIError]; a successful
// response whose body is not the answer the SDK expects is a
// [*ResponseValidationError], and one over the size limit a
// [*ResponseTooLargeError]; an attempt that produced no response is a
// [*ConnectionError], or a [*TimeoutError] when its deadline or ctx's
// passed. A failed attempt is tried again as the call's retry policy
// decides ([WithRetry], [Retry], [RetryPolicy]), and the call returns the
// error of its last attempt. A call whose ctx is cancelled, while a request
// is in flight or while it waits to retry, stops and returns ctx.Err(),
// [context.Canceled], itself, which is not an SDK [Error], as
// typesafe-sdk-python lets a cancellation through unwrapped;
// [context.Cause] gives a cause the canceller set.
func (c *Client) SystemOne(ctx context.Context, state any, qs *Prepared, opts ...CallOption) (*SystemOneResponse, error) {
	if err := c.usable(); err != nil {
		return nil, err
	}
	var o callOptions
	if len(opts) > 0 {
		o = collectCallOptions(opts)
	}
	model, err := o.systemOneModel(c.cfg.config)
	if err != nil {
		return nil, err
	}
	s, err := o.settings(ctx, c.cfg.config, c.cfg.systemOneHeader)
	if err != nil {
		return nil, err
	}
	body, err := encodeBody(state, model, qs, o.extra)
	if err != nil {
		return nil, err
	}
	defer body.Release()
	call, spare := newSystemOneAlloc(qs.Len())
	rq := request{
		method:   http.MethodPost,
		url:      c.cfg.systemOneURL,
		firstURL: &call.url,
		logURL:   c.cfg.systemOneLog,
		endpoint: c.systemOneEndpoint,
		header:   s.header,
		timeout:  s.timeout,
		body:     body,
		getBody:  body.GetBody,
	}
	resp := &call.resp
	err = c.send(ctx, &rq, s.retry, &resp.meta, func() error {
		return decodeSystemOneInto(ctx, c.cfg.logger, &resp.meta, c.systemOneEndpoint, c.cfg.redactor(), qs, model, &resp.res, spare)
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// systemOneAlloc is what a SystemOne call keeps on the heap, in one piece:
// the response it returns and its first attempt's copy of the endpoint URL
// (request.firstURL), which that attempt's request points to. The copy
// lives as long as the response, and nothing reads it but the transport.
type systemOneAlloc struct {
	resp SystemOneResponse
	url  url.URL
}

// systemOneAllocWith is a systemOneAlloc with the array of the response's
// answer entries, E, in the same allocation.
type systemOneAllocWith[E any] struct {
	systemOneAlloc
	entries E
}

// maxInlineAnswers is the largest question set whose answer entries are
// allocated with the call's response.
const maxInlineAnswers = 4

// newSystemOneAlloc allocates a call's systemOneAlloc and, for a question
// set of n questions, 1 to maxInlineAnswers, an array of n answer entries
// in the same allocation, which it returns as an empty slice for the
// decode (codec.DecodeSystemOneInto): a response that answers the
// questions asked, the usual case, then costs no allocation for its
// entries. Each size is its own type, so the array is exactly n entries.
// A larger set gets its entries from the decode, in an allocation of their
// own, as does a response with more answers than questions.
//
// Lifetime: the entries share one block with the response and the first
// attempt's URL copy, so an Answers taken from the response keeps the whole
// block reachable (704 B for three questions), as it kept the response and
// the decode's own array before, the same bytes; an answer value copied out
// of it holds no pointer into the block. No entry points into the body: the
// decode copies or interns every string it stores, whichever array holds
// the entries (TestDecodeDoesNotAliasBody, TestAnswersOutliveTheirResponse).
func newSystemOneAlloc(n int) (*systemOneAlloc, []wire.AnswerEntry) {
	switch n {
	case 1:
		a := new(systemOneAllocWith[[1]wire.AnswerEntry])
		return &a.systemOneAlloc, a.entries[:0]
	case 2:
		a := new(systemOneAllocWith[[2]wire.AnswerEntry])
		return &a.systemOneAlloc, a.entries[:0]
	case 3:
		a := new(systemOneAllocWith[[3]wire.AnswerEntry])
		return &a.systemOneAlloc, a.entries[:0]
	case maxInlineAnswers:
		a := new(systemOneAllocWith[[maxInlineAnswers]wire.AnswerEntry])
		return &a.systemOneAlloc, a.entries[:0]
	default:
		return new(systemOneAlloc), nil
	}
}

// modelsAlloc is systemOneAlloc for a list-models call.
type modelsAlloc struct {
	resp ModelsResponse
	url  url.URL
}

// Models is the list-models endpoint of a client, from [Client.Models].
type Models struct {
	c *Client
}

// Models returns the client's list-models endpoint.
func (c *Client) Models() Models { return Models{c} }

// List lists the models the account can use: GET /v1/models. Of the call
// options it takes [Timeout], [Header] and [Retry]; [Model] and [ExtraBody]
// are refused with a [*ConfigError]. Its errors are those of
// [Client.SystemOne].
func (m Models) List(ctx context.Context, opts ...CallOption) (*ModelsResponse, error) {
	c := m.c
	if err := c.usable(); err != nil {
		return nil, err
	}
	var o callOptions
	if len(opts) > 0 {
		o = collectCallOptions(opts)
	}
	if err := o.forModels(); err != nil {
		return nil, err
	}
	s, err := o.settings(ctx, c.cfg.config, c.cfg.modelsHeader)
	if err != nil {
		return nil, err
	}
	call := new(modelsAlloc)
	rq := request{
		method:   http.MethodGet,
		url:      c.cfg.modelsURL,
		firstURL: &call.url,
		logURL:   c.cfg.modelsLog,
		endpoint: c.modelsEndpoint,
		header:   s.header,
		timeout:  s.timeout,
	}
	resp := &call.resp
	err = c.send(ctx, &rq, s.retry, &resp.meta, func() error {
		return decodeModels(&resp.meta, c.modelsEndpoint, c.cfg.redactor(), &resp.list)
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// send makes the attempts of one call as policy asks (section 6.4) and
// returns nil for a response that decoded, or the error that ended the
// call: the last attempt's own, decode's for a response that arrived, or
// the context's when it ended a wait (retryState.wait). Each attempt stores
// its response's status, header and body in *meta, which decode reads. One
// loop serves every endpoint (ruling R79 NIT 9). Neither decode nor the
// policy, a copy on send's stack, escapes, so a first attempt that succeeds
// allocates nothing here.
func (c *Client) send(ctx context.Context, rq *request, policy RetryPolicy, meta *wire.ResponseMeta, decode func() error) error {
	r := retryState{policy: &policy, start: time.Now(), random: c.random}
	for attempt := 0; ; attempt++ {
		var err error
		*meta, err = c.attempt(ctx, rq, attempt)
		if err == nil {
			err = decode()
		}
		if err == nil {
			return nil
		}
		if err = r.wait(ctx, attempt, err); err != nil {
			return err
		}
	}
}

// request is what every attempt of one call sends.
type request struct {
	method string
	url    *url.URL
	// firstURL is where the first attempt copies url: storage allocated
	// with the call's response, so that the copy costs no allocation of
	// its own. A retry copies url to a fresh URL.
	firstURL *url.URL
	logURL   string // how log records name the endpoint
	endpoint string // how errors name the endpoint
	// header is the call's header template, which every attempt's header
	// starts from, and timeout the deadline of each attempt (zero: none).
	header  http.Header
	timeout time.Duration
	// body and getBody are the encoded body and its GetBody, set for a
	// request with a body; getBody is made once per call.
	body    codec.Body
	getBody func() (io.ReadCloser, error)
}

// attemptHeader returns the header map of attempt (ruling R28). The first
// attempt sends the call's template itself: the client's, built once, or
// the call's own when it sets headers. That map is shared by every call and
// never written, and net/http's RoundTripper contract forbids a transport to
// modify a request, so no copy is made. A retry sends a fresh map over the
// template, whose value slices it shares (each has len == cap, so an append
// reallocates), with X-TypeSafe-Retry-Count, which only retries carry
// (py:_core/transport.py:66-68).
func (rq *request) attemptHeader(attempt int) http.Header {
	if attempt == 0 {
		return rq.header
	}
	h := make(http.Header, len(rq.header)+1)
	maps.Copy(h, rq.header)
	h[canonicalRetryCount] = retryCountValue(attempt)
	return h
}

// attempt sends attempt number attempt of rq and reads its response. It
// returns the response's status, header and body, and an error for every
// outcome but a 2xx response read whole: an *APIError for another status, a
// *ResponseTooLargeError for a 2xx body over the limit, and a transport
// failure as a *ConnectionError or a *TimeoutError.
func (c *Client) attempt(ctx context.Context, rq *request, attempt int) (wire.ResponseMeta, error) {
	h := rq.attemptHeader(attempt)
	// Each request carries its own copy of the endpoint URL, so a
	// RoundTripper that rewrites req.URL, which the RoundTripper contract
	// forbids, cannot change the next request's: the first attempt's in the
	// call's own allocation, a retry's in a fresh one.
	u := rq.firstURL
	if attempt > 0 || u == nil {
		u = new(url.URL)
	}
	*u = *rq.url
	actx := ctx
	if timeout := rq.timeout; timeout > 0 {
		var cancel context.CancelFunc
		actx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	r := http.Request{
		Method:     rq.method,
		URL:        u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     h,
		Host:       u.Host,
	}
	if rq.getBody != nil {
		rc, err := rq.body.Open()
		if err != nil {
			// The call holds its reference until it returns: unreachable.
			return wire.ResponseMeta{}, newConnectionError(err.Error(), err, false)
		}
		r.Body, r.GetBody, r.ContentLength = rc, rq.getBody, int64(rq.body.Len())
	}
	req := r.WithContext(actx)
	c.logRequest(ctx, rq, h, attempt)

	start := time.Now()
	c.attempts.Add(1)
	resp, err := c.cfg.transport.roundTrip(req, rq.timeout)
	if err != nil {
		err = c.attemptError(ctx, actx, rq.timeout, h, err)
		c.logFailure(ctx, rq, attempt, start, err)
		return wire.ResponseMeta{}, err
	}
	meta := wire.ResponseMeta{Status: resp.StatusCode, Header: resp.Header}
	success := resp.StatusCode >= 200 && resp.StatusCode <= 299
	raw, err := readBody(resp.Body, resp.ContentLength, c.cfg.maxResponseBytes)
	// A body read to its end has nothing left to report on Close; one cut
	// short is abandoned, which Close tells the transport.
	_ = resp.Body.Close()
	switch {
	case errors.Is(err, errTooLarge):
		c.logResponse(ctx, rq, attempt, start, &meta)
		if success {
			return meta, newResponseTooLargeError(&meta, rq.endpoint, c.cfg.redactor(), c.cfg.maxResponseBytes)
		}
		return meta, newAPIError(&meta, rq.endpoint, c.cfg.redactor())
	case err != nil:
		err = c.attemptError(ctx, actx, rq.timeout, h, err)
		c.logFailure(ctx, rq, attempt, start, err)
		return wire.ResponseMeta{}, err
	}
	meta.Body = raw
	c.logResponse(ctx, rq, attempt, start, &meta)
	if !success {
		return meta, newAPIError(&meta, rq.endpoint, c.cfg.redactor())
	}
	return meta, nil
}

// attemptError turns the error that ended an attempt without a response,
// from the transport or from reading the body, into what the call returns
// (section 6.3). ctx is the call's context, actx the attempt's under its
// timeout, and h the header the attempt sent. In this order:
//
//   - A call whose context was cancelled returns ctx.Err(), context.Canceled,
//     itself: typesafe-sdk-python maps only httpx's RequestError
//     (py:_core/transport.py:79-86), so asyncio.CancelledError reaches its
//     caller as it is (tests/test_clients.py:559-596, C20 and C21).
//   - An error the transport already mapped ([transportError]) is returned
//     as it is.
//   - The call's own deadline is a *TimeoutError without a timeout.
//   - The attempt's deadline, or an error that is a timeout itself
//     (context.DeadlineExceeded in its chain, or a net.Error whose Timeout
//     is true), is a *TimeoutError with the attempt's timeout, as the
//     Python SDK maps every httpx TimeoutException
//     (py:_core/transport.py:83-84).
//   - Anything else, a refused or reset connection, a stream reset, a
//     GOAWAY after the request was written, a body cut short, is a
//     *ConnectionError.
//
// A *ConnectionError's text is the transport error's, with every credential
// of h and every URL userinfo replaced by "***" ([credentials.redact]);
// both types wrap the transport's error, or a stand-in for it when its
// chain printed a credential ([credentials.cause]).
func (c *Client) attemptError(ctx, actx context.Context, timeout time.Duration, h http.Header, err error) error {
	if cerr := ctx.Err(); errors.Is(cerr, context.Canceled) {
		return cerr
	}
	if _, ok := err.(Error); ok { //nolint:errorlint // only an error the transport returned as the SDK's own is kept.
		return err
	}
	creds := requestCredentials(h)
	var ne net.Error
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return newTimeoutError(0, creds.cause(err))
	case errors.Is(actx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return newTimeoutError(timeout, creds.cause(err))
	}
	text, _ := creds.redact(err.Error())
	return newConnectionError(text, creds.cause(err), false)
}

// errTooLarge ends a body read that passed the size limit.
var errTooLarge = errors.New("typesafe: response body over the size limit")

// The first buffer of a response body read (NF5, ruling R27).
const (
	// initialDeclared bounds the first buffer of a body whose length is
	// declared: the buffer holds min(Content-Length, initialDeclared).
	initialDeclared = 256 << 10
	// initialUndeclared is the first buffer of a body that declares no
	// length (a chunked body); it doubles as the body grows.
	initialUndeclared = 4 << 10
	// minGrow is the smallest room a growth step makes, so a body that
	// declared zero bytes and sent some does not grow a byte at a time.
	minGrow = 512
)

// readBody reads a response body of at most limit bytes (section 6.2.1,
// rulings R26b and R27). declared is its Content-Length, or -1 when it
// declares none.
//
//   - A declared length above limit is refused before anything is read.
//   - The first buffer holds min(declared, initialDeclared) bytes for a
//     declared body and initialUndeclared for an undeclared one.
//   - A full buffer grows by doubling, up to the end the body may reach: its
//     declared length while it is within it, else the limit. Growth that
//     would pass half that end goes to the end at once.
//   - The buffer that can reach the end has one byte of room beyond it, so
//     the read that finds the end of the body, or the byte after the limit,
//     needs no other buffer; the buffers before it are exact powers of two,
//     so none costs the allocator a page for one byte.
//   - The read stops at limit + 1 bytes: that byte makes the body too large.
//
// It returns errTooLarge for a body over the limit, and a read's own error
// other than io.EOF as it is.
func readBody(r io.Reader, declared, limit int64) ([]byte, error) {
	if declared > limit {
		return nil, errTooLarge
	}
	var size int64
	switch {
	case declared > initialDeclared:
		size = initialDeclared
	case declared >= 0:
		size = declared + 1
	default:
		size = min(initialUndeclared, limit+1)
	}
	buf := make([]byte, 0, size)
	for {
		if len(buf) == cap(buf) {
			if int64(len(buf)) > limit {
				return nil, errTooLarge
			}
			buf = grow(buf, declared, limit)
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err != nil {
			if int64(len(buf)) > limit {
				return nil, errTooLarge
			}
			if errors.Is(err, io.EOF) {
				return buf, nil
			}
			return nil, err
		}
	}
}

// grow returns buf, which is full and within limit, with room for more: see
// readBody.
func grow(buf []byte, declared, limit int64) []byte {
	held := int64(len(buf))
	end := limit
	if declared > held {
		end = declared // within limit: readBody refused a larger one
	}
	next := max(2*held, minGrow)
	if 2*next > end {
		next = end + 1
	}
	nb := make([]byte, held, next)
	copy(nb, buf)
	return nb
}

// logRequest logs attempt's request at debug level, its body at LevelTrace.
// Headers are redacted ([newRedactedHeaders]); nothing is built unless a
// handler takes the record.
func (c *Client) logRequest(ctx context.Context, rq *request, h http.Header, attempt int) {
	logger := c.cfg.logger
	if attempt > 0 && logger.Enabled(ctx, slog.LevelInfo) {
		logger.LogAttrs(ctx, slog.LevelInfo, "request retry", slog.String("method", rq.method),
			slog.String("endpoint", rq.logURL), slog.Int("retry", attempt))
	}
	if !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	n := 0
	if rq.getBody != nil {
		n = rq.body.Len()
	}
	logger.LogAttrs(ctx, slog.LevelDebug, "request", slog.String("method", rq.method), slog.String("endpoint", rq.logURL),
		slog.Int("attempt", attempt), slog.Any("headers", newRedactedHeaders(h, c.cfg.apiKey)), slog.Int("body_bytes", n))
	if n > 0 && logger.Enabled(ctx, LevelTrace) {
		logger.LogAttrs(ctx, LevelTrace, "request body", slog.String("method", rq.method), slog.String("endpoint", rq.logURL),
			slog.String("body", string(rq.body.Bytes())))
	}
}

// logResponse logs attempt's response: one INFO record with the status, the
// time since start and the request id, as the Python SDK's
// "<method> <url> <- <status> in <ms>ms (request <id>)", then the redacted
// headers and the body's length at debug level and the body at LevelTrace.
// The request id is redacted as the error types' Header is, "***" when it
// holds the client's API key (ruling R87; typesafe-sdk-python logs it as it
// arrived); the body is as it arrived.
func (c *Client) logResponse(ctx context.Context, rq *request, attempt int, start time.Time, meta *wire.ResponseMeta) {
	logger := c.cfg.logger
	if !logger.Enabled(ctx, slog.LevelInfo) {
		return
	}
	id := "-"
	if v, ok := c.cfg.redactor().requestID(meta.Header); ok {
		id = safeName(v)
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "response", slog.String("method", rq.method), slog.String("endpoint", rq.logURL),
		slog.Int("status", meta.Status), slog.Duration("duration", time.Since(start)), slog.String("request_id", id),
		slog.Int("attempt", attempt))
	if !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	logger.LogAttrs(ctx, slog.LevelDebug, "response headers", slog.String("method", rq.method), slog.String("endpoint", rq.logURL),
		slog.Any("headers", newRedactedHeaders(meta.Header, c.cfg.apiKey)), slog.Int("body_bytes", len(meta.Body)))
	if len(meta.Body) > 0 && logger.Enabled(ctx, LevelTrace) {
		logger.LogAttrs(ctx, LevelTrace, "response body", slog.String("method", rq.method), slog.String("endpoint", rq.logURL),
			slog.String("body", string(meta.Body)))
	}
}

// logFailure logs an attempt that produced no response, at INFO, with the
// SDK error's text, which carries no credential.
func (c *Client) logFailure(ctx context.Context, rq *request, attempt int, start time.Time, err error) {
	logger := c.cfg.logger
	if !logger.Enabled(ctx, slog.LevelInfo) {
		return
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "request failed", slog.String("method", rq.method), slog.String("endpoint", rq.logURL),
		slog.String("error", err.Error()), slog.Duration("duration", time.Since(start)), slog.Int("attempt", attempt))
}
