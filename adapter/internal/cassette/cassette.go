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

// Package cassette loads the recorded HTTP exchanges committed under
// testdata/cassettes and replays them through an in-process
// http.RoundTripper, so the replay tests drive the real providers and the
// real SDK client without a network call. The files are vcrpy JSON
// cassettes written by upstream's recording configuration
// (tests/conftest.py of system-one-adapter-python v0.2.1): one document
// {"version": 1, "interactions": [...]}, each interaction a request
// {method, uri, body, headers} and a response {status{code, message},
// headers, body{string}}, with JSON bodies stored as decoded objects by
// upstream's ReadableJsonSerializer. The package is test support, imported
// only by test files and the livetest package.
package cassette

import (
	"fmt"
	"os"
	"slices"
	"strconv"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
)

// Request is the recorded HTTP request of one interaction.
type Request struct {
	// Method is the recorded request method.
	Method string
	// URI is the recorded request URI, scheme through query.
	URI string
	// Body is the recorded request body. A body upstream's serializer
	// stored as a decoded JSON object or array is re-encoded compactly by
	// the loader; any other body keeps its recorded text's bytes.
	Body []byte
	// BodyIsJSON reports whether the recorded body was stored as a decoded
	// JSON object or array, so a matcher compares Body as a JSON value.
	BodyIsJSON bool
	// Headers holds the recorded request headers under their recorded
	// names. The committed cassettes carry none of the credential headers
	// upstream's recording strips; TestScrubMatchesUpstream pins that.
	Headers map[string][]string
}

// Response is the recorded HTTP response of one interaction.
type Response struct {
	// StatusCode is the recorded status code.
	StatusCode int
	// StatusMessage is the recorded status message, such as "OK".
	StatusMessage string
	// Headers holds the recorded response headers under their recorded
	// names; upstream's recording keeps content-type only.
	Headers map[string][]string
	// Body is the recorded response body, re-encoded as Request.Body is.
	// For a stored JSON object or array the loader writes it compactly
	// with jsonx ([jsonx.Repr] floats, non-ASCII raw), the closest Go
	// counterpart of the bytes vcrpy's replay re-encodes with Python's
	// json.dumps(..., ensure_ascii=False, separators=(",", ":")); every
	// test that reads these bytes compares them as a JSON value.
	Body []byte
	// BodyIsJSON reports whether the recorded body was stored as a decoded
	// JSON object or array.
	BodyIsJSON bool
}

// Interaction is one recorded request and its response.
type Interaction struct {
	Request  Request
	Response Response
}

// Cassette is one loaded cassette file.
type Cassette struct {
	// Interactions holds the recorded exchanges in their recorded order.
	Interactions []Interaction
}

// Load reads the vcrpy JSON cassette at path. A member the format does not
// have, a missing member, or a member of an unexpected kind is an error
// naming the file and the member's path, never ignored.
func Load(path string) (*Cassette, error) {
	doc, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cassette: %w", err)
	}
	root, err := jsonx.Read(doc)
	if err != nil {
		return nil, fmt.Errorf("cassette %s: %w", path, err)
	}
	if err := wantMembers(root, "", "version", "interactions"); err != nil {
		return nil, fmt.Errorf("cassette %s: %w", path, err)
	}
	version, _ := root.Member("version")
	if version.Kind() != jsonx.KindNumber || version.Text() != "1" {
		return nil, fmt.Errorf("cassette %s: version is not the number 1", path)
	}
	interactions, _ := root.Member("interactions")
	if interactions.Kind() != jsonx.KindArray {
		return nil, fmt.Errorf("cassette %s: interactions is not an array", path)
	}
	c := &Cassette{Interactions: make([]Interaction, 0, interactions.Len())}
	for i := range interactions.Len() {
		at := fmt.Sprintf("interactions[%d]", i)
		ix, err := parseInteraction(interactions.Index(i), at)
		if err != nil {
			return nil, fmt.Errorf("cassette %s: %w", path, err)
		}
		c.Interactions = append(c.Interactions, ix)
	}
	return c, nil
}

// parseInteraction reads one member of the interactions array at path at.
func parseInteraction(v jsonx.Node, at string) (Interaction, error) {
	if err := wantMembers(v, at, "request", "response"); err != nil {
		return Interaction{}, err
	}
	reqNode, _ := v.Member("request")
	req, err := parseRequest(reqNode, at+".request")
	if err != nil {
		return Interaction{}, err
	}
	respNode, _ := v.Member("response")
	resp, err := parseResponse(respNode, at+".response")
	if err != nil {
		return Interaction{}, err
	}
	return Interaction{Request: req, Response: resp}, nil
}

// parseRequest reads a recorded request at path at.
func parseRequest(v jsonx.Node, at string) (Request, error) {
	if err := wantMembers(v, at, "method", "uri", "body", "headers"); err != nil {
		return Request{}, err
	}
	method, _ := v.Member("method")
	if method.Kind() != jsonx.KindString {
		return Request{}, fmt.Errorf("%s.method is not a string", at)
	}
	uri, _ := v.Member("uri")
	if uri.Kind() != jsonx.KindString {
		return Request{}, fmt.Errorf("%s.uri is not a string", at)
	}
	bodyNode, _ := v.Member("body")
	body, isJSON, err := encodeBody(bodyNode, at+".body")
	if err != nil {
		return Request{}, err
	}
	headersNode, _ := v.Member("headers")
	headers, err := parseHeaders(headersNode, at+".headers")
	if err != nil {
		return Request{}, err
	}
	return Request{Method: method.Text(), URI: uri.Text(), Body: body, BodyIsJSON: isJSON, Headers: headers}, nil
}

// parseResponse reads a recorded response at path at.
func parseResponse(v jsonx.Node, at string) (Response, error) {
	if err := wantMembers(v, at, "status", "headers", "body"); err != nil {
		return Response{}, err
	}
	statusNode, _ := v.Member("status")
	if err := wantMembers(statusNode, at+".status", "code", "message"); err != nil {
		return Response{}, err
	}
	codeNode, _ := statusNode.Member("code")
	if codeNode.Kind() != jsonx.KindNumber || !codeNode.IsInt() {
		return Response{}, fmt.Errorf("%s.status.code is not an integer", at)
	}
	code, err := strconv.Atoi(codeNode.Text())
	if err != nil {
		return Response{}, fmt.Errorf("%s.status.code: %w", at, err)
	}
	messageNode, _ := statusNode.Member("message")
	if messageNode.Kind() != jsonx.KindString {
		return Response{}, fmt.Errorf("%s.status.message is not a string", at)
	}
	headersNode, _ := v.Member("headers")
	headers, err := parseHeaders(headersNode, at+".headers")
	if err != nil {
		return Response{}, err
	}
	bodyNode, _ := v.Member("body")
	if err := wantMembers(bodyNode, at+".body", "string"); err != nil {
		return Response{}, err
	}
	stringNode, _ := bodyNode.Member("string")
	body, isJSON, err := encodeBody(stringNode, at+".body.string")
	if err != nil {
		return Response{}, err
	}
	return Response{
		StatusCode:    code,
		StatusMessage: messageNode.Text(),
		Headers:       headers,
		Body:          body,
		BodyIsJSON:    isJSON,
	}, nil
}

// encodeBody returns a recorded body's bytes: a stored JSON object or array
// re-encoded compactly with jsonx, a stored string as its bytes.
func encodeBody(v jsonx.Node, at string) (body []byte, isJSON bool, err error) {
	switch v.Kind() {
	case jsonx.KindObject, jsonx.KindArray:
		value, err := v.Value(jsonx.Repr)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", at, err)
		}
		b, err := jsonx.Marshal(value)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", at, err)
		}
		return b, true, nil
	case jsonx.KindString:
		return []byte(v.Text()), false, nil
	default:
		return nil, false, fmt.Errorf("%s is neither a JSON object or array nor a string", at)
	}
}

// parseHeaders reads a recorded header object: each member a name whose
// value is an array of strings.
func parseHeaders(v jsonx.Node, at string) (map[string][]string, error) {
	if v.Kind() != jsonx.KindObject {
		return nil, fmt.Errorf("%s is not an object", at)
	}
	headers := make(map[string][]string, v.Len())
	for i := range v.Len() {
		name := v.Name(i)
		values := v.Index(i)
		if values.Kind() != jsonx.KindArray {
			return nil, fmt.Errorf("%s.%s is not an array", at, name)
		}
		list := make([]string, 0, values.Len())
		for j := range values.Len() {
			value := values.Index(j)
			if value.Kind() != jsonx.KindString {
				return nil, fmt.Errorf("%s.%s[%d] is not a string", at, name, j)
			}
			list = append(list, value.Text())
		}
		headers[name] = list
	}
	return headers, nil
}

// wantMembers checks that v is an object holding exactly the named members.
func wantMembers(v jsonx.Node, at string, names ...string) error {
	where := at
	if where == "" {
		where = "the document"
	}
	if v.Kind() != jsonx.KindObject {
		return fmt.Errorf("%s is not an object", where)
	}
	for i := range v.Len() {
		name := v.Name(i)
		if !slices.Contains(names, name) {
			return fmt.Errorf("%s has the unknown member %q", where, name)
		}
	}
	for _, name := range names {
		if _, ok := v.Member(name); !ok {
			return fmt.Errorf("%s is missing the member %q", where, name)
		}
	}
	return nil
}
