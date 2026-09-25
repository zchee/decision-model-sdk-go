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

package testsupport

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
)

// Reply is the canned outcome of one round trip through a [Recorder]: a
// transport error when Err is set, otherwise a response.
type Reply struct {
	// Status is the response status code; zero means 200.
	Status int
	// Header is copied into the response.
	Header http.Header
	// Body is the response body. The Recorder never modifies it.
	Body []byte
	// ContentLength is the length the response declares. Zero means
	// len(Body); -1 declares no length (a chunked body); a value above
	// len(Body) makes the body end with io.ErrUnexpectedEOF after len(Body)
	// bytes, as a connection that closes early would. A declared zero with a
	// non-empty body cannot be expressed.
	ContentLength int64
	// Err, when set, is returned by RoundTrip instead of a response.
	Err error
}

// JSON returns a Reply with the given status, a Content-Type of
// application/json and body.
func JSON(status int, body []byte) Reply {
	return Reply{Status: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}
}

// RecordedRequest is what a [Recorder] saw of one request.
type RecordedRequest struct {
	// Index is the request's position among all RoundTrip calls, from 0.
	Index int
	// Method is the request method.
	Method string
	// URL is the request URL as a string; empty when the Recorder discards.
	URL string
	// Host is the request's Host (the authority the transport would send).
	Host string
	// Header is a copy of the request header.
	Header http.Header
	// Body is the whole request body, read before RoundTrip returned; nil
	// when the request had none or the Recorder discards bodies.
	Body []byte
	// ContentLength is the request's declared length.
	ContentLength int64
	// HasGetBody reports whether the request carried GetBody, which a
	// transport needs to replay it.
	HasGetBody bool
	// Canceled reports that the request's context was already done, so the
	// Recorder returned the context's error and served no reply.
	Canceled bool
}

// errNoReplies is returned by a Recorder that has nothing to serve.
var errNoReplies = errors.New("testsupport: Recorder has no Replies and no Respond function")

// Recorder is an in-memory [net/http.RoundTripper] that records every request
// and answers with canned replies. It is synchronous: RoundTrip reads the
// whole request body and closes it before it returns, so a body's lifetime is
// in step with the call (the HTTP/2 transport closes bodies in a goroutine).
//
// Set the exported fields before the first RoundTrip and do not change them
// afterwards. A Recorder is safe for concurrent use.
type Recorder struct {
	// Replies are served in order; once they run out, the last one repeats.
	Replies []Reply
	// Respond, when set, chooses the reply for each request instead of
	// Replies. It runs on the goroutine that called RoundTrip and gets its
	// own copy of the record, which it may change.
	Respond func(RecordedRequest) Reply
	// Discard drains and closes each request body without keeping it, and
	// keeps only the count of requests, so a steady-state allocation test
	// measures little beyond the response it is handed. Requests then
	// returns nothing and Respond sees a nil Body.
	Discard bool

	mu       sync.Mutex
	calls    int
	served   int
	requests []RecordedRequest
	closes   int
}

// RoundTrip implements [net/http.RoundTripper].
func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := RecordedRequest{
		Method:        req.Method,
		Host:          req.Host,
		ContentLength: req.ContentLength,
		HasGetBody:    req.GetBody != nil,
	}
	if !r.Discard {
		rec.URL = req.URL.String()
		rec.Header = req.Header.Clone()
	}
	var readErr error
	if req.Body != nil {
		if r.Discard {
			_, readErr = io.Copy(io.Discard, req.Body)
		} else {
			rec.Body, readErr = io.ReadAll(req.Body)
		}
		if err := req.Body.Close(); readErr == nil {
			readErr = err
		}
	}
	ctxErr := req.Context().Err()
	rec.Canceled = ctxErr != nil

	r.mu.Lock()
	rec.Index = r.calls
	r.calls++
	if !r.Discard {
		r.requests = append(r.requests, rec)
	}
	var reply Reply
	var ok bool
	if ctxErr == nil && readErr == nil && r.Respond == nil && len(r.Replies) > 0 {
		reply, ok = r.Replies[min(r.served, len(r.Replies)-1)], true
		r.served++
	}
	r.mu.Unlock()

	switch {
	case ctxErr != nil:
		return nil, ctxErr
	case readErr != nil:
		return nil, readErr
	case r.Respond != nil:
		reply = r.Respond(rec.clone())
	case !ok:
		return nil, errNoReplies
	}
	if reply.Err != nil {
		return nil, reply.Err
	}
	return reply.response(req), nil
}

// response builds the *http.Response for reply.
func (reply Reply) response(req *http.Request) *http.Response {
	status := reply.Status
	if status == 0 {
		status = http.StatusOK
	}
	declared := reply.ContentLength
	if declared == 0 {
		declared = int64(len(reply.Body))
	}
	var body io.ReadCloser = http.NoBody
	if len(reply.Body) > 0 || declared > 0 {
		body = &replyBody{data: reply.Body, short: declared > int64(len(reply.Body))}
	}
	return &http.Response{
		Status:        strconv.Itoa(status) + " " + http.StatusText(status),
		StatusCode:    status,
		Proto:         "HTTP/2.0",
		ProtoMajor:    2,
		Header:        reply.Header.Clone(),
		Body:          body,
		ContentLength: declared,
		Request:       req,
	}
}

// replyBody reads a reply's bytes and, when the reply declared more than it
// holds, ends with io.ErrUnexpectedEOF.
type replyBody struct {
	data  []byte
	off   int
	short bool
}

// Read implements io.Reader.
func (b *replyBody) Read(p []byte) (int, error) {
	if b.off >= len(b.data) {
		if b.short {
			return 0, io.ErrUnexpectedEOF
		}
		return 0, io.EOF
	}
	n := copy(p, b.data[b.off:])
	b.off += n
	return n, nil
}

// Close implements io.Closer.
func (*replyBody) Close() error { return nil }

// clone returns a copy of rec that shares no header map or body bytes with
// it. A nil Header or Body stays nil, so a discarding Recorder pays nothing.
func (rec RecordedRequest) clone() RecordedRequest {
	rec.Header = rec.Header.Clone()
	rec.Body = bytes.Clone(rec.Body)
	return rec
}

// Requests returns copies of the requests recorded so far, in call order;
// changing one changes neither the Recorder's record nor a later copy.
func (r *Recorder) Requests() []RecordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecordedRequest, len(r.requests))
	for i, rec := range r.requests {
		out[i] = rec.clone()
	}
	return out
}

// Count returns the number of RoundTrip calls so far, recorded or not.
func (r *Recorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// Close counts a call, so a lifecycle test can assert that the SDK closes a
// supplied io.Closer transport exactly once. It always returns nil. A test
// that needs a transport without Close wraps the Recorder in a struct that
// embeds only http.RoundTripper.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closes++
	return nil
}

// Closes returns the number of Close calls so far.
func (r *Recorder) Closes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closes
}
