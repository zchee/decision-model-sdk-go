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

// Package naive is the benchmark comparator of the SDK's performance
// criteria (AC-P2, AC-P6, AC-P7; owner decision G3 (a)): a System One call
// written the obvious way, with a general-purpose JSON library and no
// SDK machinery. It is a yardstick for benchmarks and allocation reports,
// not an SDK: it has no retries, no validation of the answers and no error
// types beyond [StatusError].
//
// A [Client] marshals a plain [Body] with its [Codec], builds a request with
// [net/http.NewRequestWithContext] over the encoded bytes, sets the headers
// it was given one by one, calls its RoundTripper directly (no
// [net/http.Client]), reads the response with [io.ReadAll] and unmarshals it
// into a map[string]any. Nothing is pooled, interned or reused between
// calls, and nothing is checked beyond what the codec checks: that is the
// point of the comparison.
//
// Two codecs are provided. [Sonic], sonic's Marshal and Unmarshal with its
// default configuration, is the comparator of record (G3 (a)): the SDK uses
// sonic too, so a gap between the two measures the SDK's design, not the
// library. [StdJSON], encoding/json, is a second comparator whose numbers
// are reported only.
//
// The comparison is apples to apples when the caller hands the Client what
// the SDK sends: the SDK's own header template, the endpoint URL, the model
// and the question set's JSON as the SDK prepared it. Then, for a state
// both encoders spell alike (text or a struct without HTML-special
// characters, whose members are in a fixed order), the body bytes are the
// SDK's; the root package's tests assert that byte for byte.
//
// With internal/codec it is the only package of the module that imports
// sonic (plan section 4, NF6; the seam test in internal/codec holds the
// rule). It imports encoding/json as the second comparator, which the seam
// test allows internal/testsupport and its subpackages, test tooling only.
// It imports neither the root package nor internal/codec.
package naive

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
)

// Codec is the JSON library a [Client] encodes and decodes with.
type Codec struct {
	// Name identifies the codec in benchmark and test names.
	Name string
	// Marshal returns the JSON encoding of v.
	Marshal func(v any) ([]byte, error)
	// Unmarshal decodes data into the value v points to.
	Unmarshal func(data []byte, v any) error
}

var (
	// Sonic is sonic's Marshal and Unmarshal with sonic's default
	// configuration (no HTML escaping, map members in iteration order):
	// the comparator of record, G3 (a).
	Sonic = Codec{Name: "sonic", Marshal: sonic.Marshal, Unmarshal: sonic.Unmarshal}
	// StdJSON is encoding/json's Marshal and Unmarshal, reported only (G3
	// (a)). It escapes <, > and & in strings, which sonic and the SDK do
	// not, and writes map members in key order.
	StdJSON = Codec{Name: "encoding/json", Marshal: json.Marshal, Unmarshal: json.Unmarshal}
)

// Body is the request body of POST /v1/systemone as a straightforward
// client declares it: its members in the order the SDK writes them, the
// question set as JSON the caller already holds.
type Body struct {
	State     any             `json:"state"`
	Model     string          `json:"model"`
	Questions json.RawMessage `json:"questions"`
}

// Encode returns the codec's encoding of the request body {state, model,
// questions}, questions being the question set as JSON.
func (c Codec) Encode(state any, model string, questions []byte) ([]byte, error) {
	return c.Marshal(Body{State: state, Model: model, Questions: questions})
}

// Decode returns the codec's decoding of a response body into a generic
// map, as a client without response types reads it.
func (c Codec) Decode(raw []byte) (map[string]any, error) {
	var out map[string]any
	if err := c.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// StatusError is the error of a response whose status is outside 2xx.
type StatusError struct {
	// StatusCode is the response's status code.
	StatusCode int
	// Body is the response body as read.
	Body []byte
}

// Error implements error.
func (e *StatusError) Error() string {
	return "naive: status " + strconv.Itoa(e.StatusCode)
}

// Client makes naive System One calls. Set its fields before the first
// call and do not change them afterwards; a Client is then safe for
// concurrent use.
type Client struct {
	// Transport carries every request; the SDK's benchmarks give both
	// clients the same one.
	Transport http.RoundTripper
	// URL is the System One endpoint, parsed again on every call.
	URL string
	// Header holds the headers every request carries, set one by one on
	// each request: hand it the SDK's own template.
	Header http.Header
	// Model is the body's "model" member.
	Model string
	// Questions is the question set as JSON, the body's "questions" member.
	Questions []byte
	// Timeout bounds each call with context.WithTimeout; zero sets none.
	Timeout time.Duration
	// Codec encodes the body and decodes the response.
	Codec Codec
}

// SystemOne makes one call: it encodes {state, Model, Questions}, posts it
// to URL with Header through Transport, reads the whole response and decodes
// it into a map. A status outside 2xx is a [*StatusError]; every other
// failure is the codec's, the request's or the transport's error as it is.
func (c *Client) SystemOne(ctx context.Context, state any) (map[string]any, error) {
	b, err := c.Codec.Encode(state, c.Model, c.Questions)
	if err != nil {
		return nil, err
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	for name, values := range c.Header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	resp, err := c.Transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &StatusError{StatusCode: resp.StatusCode, Body: raw}
	}
	return c.Codec.Decode(raw)
}
