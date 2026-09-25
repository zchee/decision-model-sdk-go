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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

// BodySum is the SHA-256 digest of a request body.
type BodySum [sha256.Size]byte

// String renders the digest in hexadecimal.
func (s BodySum) String() string { return hex.EncodeToString(s[:]) }

// errNoGetBody is returned by SumGetBody for a request without GetBody.
var errNoGetBody = errors.New("testsupport: the request has no GetBody, so it cannot be replayed or retried")

// SumGetBody opens a new reader with getBody (a request's GetBody), reads it
// to the end, closes it, and returns the SHA-256 digest of the bytes and
// their number. A test calls it once per attempt to assert that every
// attempt of a call, and every replay the transport makes, sends the same
// bytes (port plan PM4), and compares the count with the request's
// ContentLength. A nil getBody is an error, as is a read or close failure.
//
// It allocates the hash state and one 4 KiB read buffer per call, whatever
// the size of the body.
func SumGetBody(getBody func() (io.ReadCloser, error)) (BodySum, int64, error) {
	if getBody == nil {
		return BodySum{}, 0, errNoGetBody
	}
	rc, err := getBody()
	if err != nil {
		return BodySum{}, 0, err
	}
	h := sha256.New()
	n, err := io.CopyBuffer(h, onlyReader{rc}, make([]byte, 4<<10))
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return BodySum{}, n, err
	}
	var sum BodySum
	h.Sum(sum[:0])
	return sum, n, nil
}

// onlyReader hides every method of a reader but Read, so that io.CopyBuffer
// uses the buffer it is given instead of a WriteTo or ReadFrom of its own.
type onlyReader struct{ r io.Reader }

// Read implements io.Reader.
func (o onlyReader) Read(p []byte) (int, error) { return o.r.Read(p) }
