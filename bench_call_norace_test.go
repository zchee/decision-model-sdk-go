//go:build !race

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
	"bytes"
	"io"
	"net/http"
	"testing"
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
	if err == io.EOF {
		b.eof = true
	}
	return n, err
}

// Close implements io.Closer.
func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}

// TestRoundTripFloorMatchesFloorCall keeps B5's call/floor and
// TestAllocWholeCall's floor the same work: roundTripFloor, B5's copy, and
// floorCall, the source of truth (compiled only without -race, so the
// benchmark cannot call it), both read the response to its end and close
// it, and both return the transport's error.
func TestRoundTripFloorMatchesFloorCall(t *testing.T) {
	floors := map[string]func(http.RoundTripper, *http.Request) error{
		"floorCall":      floorCall,
		"roundTripFloor": roundTripFloor,
	}
	for name, floor := range floors {
		t.Run("success: "+name+" drains and closes the response", func(t *testing.T) {
			body := &trackedBody{r: bytes.NewReader(bytes.Repeat([]byte("x"), 64<<10))}
			rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body, Request: req}, nil
			})
			if err := floor(rt, &http.Request{Method: http.MethodPost}); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !body.eof || !body.closed {
				t.Errorf("%s: read to the end %t, closed %t; want both", name, body.eof, body.closed)
			}
		})
		t.Run("error: "+name+" returns the transport's error", func(t *testing.T) {
			rt := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF })
			if err := floor(rt, &http.Request{Method: http.MethodPost}); err != io.ErrUnexpectedEOF { //nolint:errorlint // the floor returns the transport's error as it is.
				t.Errorf("%s: error %v, want io.ErrUnexpectedEOF", name, err)
			}
		})
	}
}
