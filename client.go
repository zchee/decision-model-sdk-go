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
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/engine"
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
type Client engine.Client[RetryPolicy]

// eng returns c's state, internal/engine's Client, over which Client is
// defined (W6.5 design D1): a free conversion. The key sits two pointers
// away from it (ruling R66, engine.ConfigRef).
func (c *Client) eng() *engine.Client[RetryPolicy] { return (*engine.Client[RetryPolicy])(c) }

// cfg returns c's configuration.
func (c *Client) cfg() *config { return c.eng().Config() }

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
	return (*Client)(engine.NewClient(cfg, endpointOf(http.MethodPost, cfg.SystemOneURL), endpointOf(http.MethodGet, cfg.ModelsURL))), nil
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
	if !c.eng().Closed().CompareAndSwap(false, true) {
		return nil
	}
	return c.cfg().Transport.Close()
}

// built returns a *ConfigError for a Client that NewClient did not build.
func (c *Client) built() error {
	if !c.eng().Built() {
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
	if c.eng().Closed().Load() {
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
	return Stats{Dials: c.cfg().Transport.Stats().Dials, Attempts: c.eng().Attempts().Load()}
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
	return c.systemOne(ctx, state, qs, opts, nil)
}

// systemOne is [Client.SystemOne], which also stores in *red, when red is
// not nil, the redactor of the header of the response it returns
// ([engine.Transport.ResponseRedactor]), for [Ask]'s decode.
func (c *Client) systemOne(ctx context.Context, state any, qs *Prepared, opts []CallOption, red *headerRedactor) (*SystemOneResponse, error) {
	if err := c.usable(); err != nil {
		return nil, err
	}
	var o callOptions
	if len(opts) > 0 {
		o = collectCallOptions(opts)
	}
	cfg := c.cfg()
	model, err := o.systemOneModel(cfg)
	if err != nil {
		return nil, err
	}
	s, err := o.settings(ctx, cfg, cfg.SystemOneHeader)
	if err != nil {
		return nil, err
	}
	body, err := encodeBody(state, model, qs, o.extra)
	if err != nil {
		return nil, err
	}
	defer body.Release()
	call, spare := engine.NewSystemOneAlloc(qs.Len())
	rq := engine.Request{
		Method:   http.MethodPost,
		URL:      cfg.SystemOneURL,
		FirstURL: &call.URL,
		LogURL:   cfg.SystemOneLog,
		Endpoint: c.eng().SystemOneEndpoint(),
		Header:   s.header,
		Timeout:  s.timeout,
		Body:     body,
		GetBody:  body.GetBody,
	}
	resp := (*SystemOneResponse)(&call.Resp)
	r, err := c.send(ctx, &rq, s.retry, resp.respMeta(), func(r headerRedactor) error {
		return decodeSystemOneInto(ctx, cfg.Logger, resp.respMeta(), c.eng().SystemOneEndpoint(), r, qs, model, resp.result(), spare)
	})
	if err != nil {
		return nil, err
	}
	if red != nil {
		*red = r
	}
	return resp, nil
}

// modelsAlloc is engine.SystemOneAlloc for a list-models call.
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
	cfg := c.cfg()
	s, err := o.settings(ctx, cfg, cfg.ModelsHeader)
	if err != nil {
		return nil, err
	}
	call := new(modelsAlloc)
	rq := engine.Request{
		Method:   http.MethodGet,
		URL:      cfg.ModelsURL,
		FirstURL: &call.url,
		LogURL:   cfg.ModelsLog,
		Endpoint: c.eng().ModelsEndpoint(),
		Header:   s.header,
		Timeout:  s.timeout,
	}
	resp := &call.resp
	_, err = c.send(ctx, &rq, s.retry, &resp.meta, func(red headerRedactor) error {
		return decodeModels(&resp.meta, c.eng().ModelsEndpoint(), red, &resp.list)
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
// its response's status, header and body in *meta, which decode reads with
// the response's header redactor ([engine.Transport.ResponseRedactor]),
// which send also returns. One loop serves every endpoint (ruling R79 NIT
// 9). Neither decode nor the policy, a copy on send's stack, escapes, so a
// first attempt that succeeds allocates nothing here.
func (c *Client) send(ctx context.Context, rq *engine.Request, policy RetryPolicy, meta *wire.ResponseMeta, decode func(headerRedactor) error) (headerRedactor, error) {
	r := retryState{policy: &policy, start: time.Now(), random: c.eng().Random()}
	for attempt := 0; ; attempt++ {
		var (
			red headerRedactor
			err error
		)
		*meta, red, err = c.attempt(ctx, rq, attempt)
		if err == nil {
			err = decode(red)
		}
		if err == nil {
			return red, nil
		}
		if err = r.wait(ctx, attempt, err); err != nil {
			return red, err
		}
	}
}

// attempt sends attempt number attempt of rq and reads its response. It
// returns the response's status, header and body, and an error for every
// outcome but a 2xx response read whole: an *APIError for another status, a
// *ResponseTooLargeError for a 2xx body over the limit, and a transport
// failure as a *ConnectionError or a *TimeoutError; and the redactor of
// the response's header ([engine.Transport.ResponseRedactor]), which the
// errors, the records and decode redact it with.
func (c *Client) attempt(ctx context.Context, rq *engine.Request, attempt int) (wire.ResponseMeta, headerRedactor, error) {
	h := rq.AttemptHeader(attempt)
	// Each request carries its own copy of the endpoint URL, so a
	// RoundTripper that rewrites req.URL, which the RoundTripper contract
	// forbids, cannot change the next request's: the first attempt's in the
	// call's own allocation, a retry's in a fresh one.
	u := rq.FirstURL
	if attempt > 0 || u == nil {
		u = new(url.URL)
	}
	*u = *rq.URL
	actx := ctx
	if timeout := rq.Timeout; timeout > 0 {
		var cancel context.CancelFunc
		actx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	r := http.Request{
		Method:     rq.Method,
		URL:        u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     h,
		Host:       u.Host,
	}
	if rq.GetBody != nil {
		rc, err := rq.Body.Open()
		if err != nil {
			// The call holds its reference until it returns: unreachable.
			return wire.ResponseMeta{}, headerRedactor{}, newConnectionError(err.Error(), err, false)
		}
		r.Body, r.GetBody, r.ContentLength = rc, rq.GetBody, int64(rq.Body.Len())
	}
	req := r.WithContext(actx)
	c.logRequest(ctx, rq, h, attempt)

	start := time.Now()
	c.eng().Attempts().Add(1)
	resp, err := roundTrip(c.cfg().Transport, req, rq.Timeout)
	if err != nil {
		err = c.attemptError(ctx, actx, rq.Timeout, req, err)
		c.logFailure(ctx, rq, attempt, start, err)
		return wire.ResponseMeta{}, headerRedactor{}, err
	}
	red := c.cfg().Transport.ResponseRedactor(c.cfg().Redactor(), resp)
	meta := wire.ResponseMeta{Status: resp.StatusCode, Header: resp.Header}
	success := resp.StatusCode >= 200 && resp.StatusCode <= 299
	raw, err := engine.ReadBody(resp.Body, resp.ContentLength, c.cfg().MaxResponseBytes)
	// A body read to its end has nothing left to report on Close; one cut
	// short is abandoned, which Close tells the transport.
	_ = resp.Body.Close()
	switch {
	case errors.Is(err, engine.ErrTooLarge):
		c.logResponse(ctx, rq, attempt, start, &meta, red)
		if success {
			return meta, red, newResponseTooLargeError(&meta, rq.Endpoint, red, c.cfg().MaxResponseBytes)
		}
		return meta, red, newAPIError(&meta, rq.Endpoint, red)
	case err != nil:
		err = c.attemptError(ctx, actx, rq.Timeout, req, err)
		c.logFailure(ctx, rq, attempt, start, err)
		return wire.ResponseMeta{}, headerRedactor{}, err
	}
	meta.Body = raw
	c.logResponse(ctx, rq, attempt, start, &meta, red)
	if !success {
		return meta, red, newAPIError(&meta, rq.Endpoint, red)
	}
	return meta, red, nil
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
// of the call req made ([engine.Transport.Credentials]) and every URL userinfo
// replaced by "***" ([engine.Credentials.Redact]); both types wrap the
// transport's error, or a stand-in for it when its chain printed a
// credential ([engine.Credentials.Cause]).
func (c *Client) attemptError(ctx, actx context.Context, timeout time.Duration, req *http.Request, err error) error {
	if cerr := ctx.Err(); errors.Is(cerr, context.Canceled) {
		return cerr
	}
	if _, ok := err.(Error); ok { //nolint:errorlint // only an error the transport returned as the SDK's own is kept.
		return err
	}
	creds := c.cfg().Transport.Credentials(req)
	var ne net.Error
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return newTimeoutError(0, creds.Cause(err))
	case errors.Is(actx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return newTimeoutError(timeout, creds.Cause(err))
	}
	text, _ := creds.Redact(err.Error())
	return newConnectionError(text, creds.Cause(err), false)
}

// logRequest logs attempt's request at debug level, its body at LevelTrace.
// Headers are redacted ([engine.NewRedactedHeaders]); nothing is built unless a
// handler takes the record.
func (c *Client) logRequest(ctx context.Context, rq *engine.Request, h http.Header, attempt int) {
	logger := c.cfg().Logger
	if attempt > 0 && logger.Enabled(ctx, slog.LevelInfo) {
		logger.LogAttrs(ctx, slog.LevelInfo, "request retry", slog.String("method", rq.Method),
			slog.String("endpoint", rq.LogURL), slog.Int("retry", attempt))
	}
	if !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	n := 0
	if rq.GetBody != nil {
		n = rq.Body.Len()
	}
	logger.LogAttrs(ctx, slog.LevelDebug, "request", slog.String("method", rq.Method), slog.String("endpoint", rq.LogURL),
		slog.Int("attempt", attempt), slog.Any("headers", engine.NewRedactedHeaders(h, c.cfg().Redactor())), slog.Int("body_bytes", n))
	if n > 0 && logger.Enabled(ctx, LevelTrace) {
		logger.LogAttrs(ctx, LevelTrace, "request body", slog.String("method", rq.Method), slog.String("endpoint", rq.LogURL),
			slog.String("body", string(rq.Body.Bytes())))
	}
}

// logResponse logs attempt's response: one INFO record with the status, the
// time since start and the request id, as the Python SDK's
// "<method> <url> <- <status> in <ms>ms (request <id>)", then the redacted
// headers and the body's length at debug level and the body at LevelTrace.
// The request id and the headers are redacted by r, the response's
// redactor, as the error types' Header is: "***" when a value holds the
// client's API key or, for a response to a plain-HTTP request through a
// proxy, the proxy's credential (ruling R87; typesafe-sdk-python logs the id
// as it arrived); the body is as it arrived.
func (c *Client) logResponse(ctx context.Context, rq *engine.Request, attempt int, start time.Time, meta *wire.ResponseMeta, r headerRedactor) {
	logger := c.cfg().Logger
	if !logger.Enabled(ctx, slog.LevelInfo) {
		return
	}
	id := "-"
	if v, ok := r.RequestID(meta.Header); ok {
		id = safeName(v)
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "response", slog.String("method", rq.Method), slog.String("endpoint", rq.LogURL),
		slog.Int("status", meta.Status), slog.Duration("duration", time.Since(start)), slog.String("request_id", id),
		slog.Int("attempt", attempt))
	if !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	logger.LogAttrs(ctx, slog.LevelDebug, "response headers", slog.String("method", rq.Method), slog.String("endpoint", rq.LogURL),
		slog.Any("headers", engine.NewRedactedHeaders(meta.Header, r)), slog.Int("body_bytes", len(meta.Body)))
	if len(meta.Body) > 0 && logger.Enabled(ctx, LevelTrace) {
		logger.LogAttrs(ctx, LevelTrace, "response body", slog.String("method", rq.Method), slog.String("endpoint", rq.LogURL),
			slog.String("body", string(meta.Body)))
	}
}

// logFailure logs an attempt that produced no response, at INFO, with the
// SDK error's text, which carries no credential.
func (c *Client) logFailure(ctx context.Context, rq *engine.Request, attempt int, start time.Time, err error) {
	logger := c.cfg().Logger
	if !logger.Enabled(ctx, slog.LevelInfo) {
		return
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "request failed", slog.String("method", rq.Method), slog.String("endpoint", rq.LogURL),
		slog.String("error", err.Error()), slog.Duration("duration", time.Since(start)), slog.Int("attempt", attempt))
}
