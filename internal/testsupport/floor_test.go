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
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// trackedBody is a response body that records whether it was read to its
// end and closed.
type trackedBody struct {
	r           *bytes.Reader
	eof, closed bool
}

// Read implements io.Reader.
func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if errors.Is(err, io.EOF) {
		b.eof = true
	}
	return n, err
}

// Close implements io.Closer.
func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestFloorCall checks the floor both the allocation test and the call/floor
// benchmark use: it reads the response to its end and closes it (review
// W5.1 MINOR 4's mutant, a floor that stops draining, fails here), and it
// returns the transport's error as it is.
func TestFloorCall(t *testing.T) {
	errTransport := errors.New("transport down")
	tests := map[string]struct {
		body    *trackedBody
		rtErr   error
		wantErr error
	}{
		"success: drains and closes the response": {body: &trackedBody{r: bytes.NewReader(bytes.Repeat([]byte("x"), 64<<10))}},
		"error: returns the transport's error":    {rtErr: errTransport, wantErr: errTransport},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if tt.rtErr != nil {
					return nil, tt.rtErr
				}
				return &http.Response{StatusCode: http.StatusOK, Body: tt.body, Request: req}, nil
			})
			err := FloorCall(rt, &http.Request{Method: http.MethodPost})
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("FloorCall error = %v, want %v", err, tt.wantErr)
			}
			if tt.body != nil && (!tt.body.eof || !tt.body.closed) {
				t.Errorf("read to the end %t, closed %t; want both", tt.body.eof, tt.body.closed)
			}
		})
	}
}

// TestNewFixtureServer checks what the loopback benchmarks rely on: over
// HTTP/2, the model list answers models.json and anything else result.json,
// both as JSON with their length declared.
func TestNewFixtureServer(t *testing.T) {
	srv := NewFixtureServer(t)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: ClientTLSConfig(t), ForceAttemptHTTP2: true}}
	t.Cleanup(client.CloseIdleConnections)
	tests := map[string]struct {
		method, path, fixture string
	}{
		"success: the model list":       {method: http.MethodGet, path: "/v1/models", fixture: "models.json"},
		"success: a System One request": {method: http.MethodPost, path: "/v1/systemone", fixture: "result.json"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), tt.method, srv.URL()+tt.path, strings.NewReader(`{"state":"s"}`))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := FixtureString(t, tt.fixture)
			if diff := gocmp.Diff(want, string(body)); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
			if resp.ProtoMajor != 2 || resp.Header.Get("Content-Type") != "application/json" || resp.ContentLength != int64(len(want)) {
				t.Errorf("HTTP/%d, Content-Type %q, Content-Length %d; want HTTP/2, application/json, %d", resp.ProtoMajor, resp.Header.Get("Content-Type"), resp.ContentLength, len(want))
			}
		})
	}
}
