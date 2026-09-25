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
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// countingBody is a request body that counts its Close calls and can fail a
// read after some bytes, or its Close.
type countingBody struct {
	r        io.Reader
	closes   *int
	readErr  error // returned once r is exhausted, instead of io.EOF
	closeErr error
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err == io.EOF && b.readErr != nil {
		return n, b.readErr
	}
	return n, err
}

func (b *countingBody) Close() error {
	*b.closes++
	return b.closeErr
}

// TestSumGetBody checks the digest against crypto/sha256 over the same
// bytes, that every reader it opens is closed, and each failure.
func TestSumGetBody(t *testing.T) {
	errOpen := errors.New("open failed")
	errRead := errors.New("read failed")
	errClose := errors.New("close failed")
	large := bytes.Repeat([]byte("0123456789abcdef"), 1<<10) // 16 KiB: several 4 KiB reads

	tests := map[string]struct {
		data     []byte
		openErr  error
		readErr  error
		closeErr error
		wantErr  error
		wantN    int64
		closes   int
	}{
		"success: a JSON body":                   {data: []byte(`{"state":"hi"}`), wantN: 14, closes: 1},
		"success: an empty body":                 {data: []byte{}, wantN: 0, closes: 1},
		"success: a body longer than the buffer": {data: large, wantN: int64(len(large)), closes: 1},
		"error: GetBody fails":                   {openErr: errOpen, wantErr: errOpen, closes: 0},
		"error: a read fails":                    {data: []byte("abc"), readErr: errRead, wantErr: errRead, wantN: 3, closes: 1},
		"error: Close fails":                     {data: []byte("abc"), closeErr: errClose, wantErr: errClose, wantN: 3, closes: 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			closes := 0
			getBody := func() (io.ReadCloser, error) {
				if tt.openErr != nil {
					return nil, tt.openErr
				}
				return &countingBody{r: bytes.NewReader(tt.data), closes: &closes, readErr: tt.readErr, closeErr: tt.closeErr}, nil
			}
			sum, n, err := SumGetBody(getBody)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if n != tt.wantN {
				t.Errorf("n = %d, want %d", n, tt.wantN)
			}
			if closes != tt.closes {
				t.Errorf("Close called %d times, want %d", closes, tt.closes)
			}
			if tt.wantErr != nil {
				if sum != (BodySum{}) {
					t.Errorf("sum = %s on failure, want the zero digest", sum)
				}
				return
			}
			want := BodySum(sha256.Sum256(tt.data))
			if diff := gocmp.Diff(want.String(), sum.String()); diff != "" {
				t.Errorf("digest (-want +got):\n%s", diff)
			}
		})
	}
}

// TestSumGetBodyOfRequest uses the helper as a test of the SDK does: once
// per attempt on the request's own GetBody, which net/http sets for a
// strings.Reader body; the digests agree, and a request without GetBody is
// an error.
func TestSumGetBodyOfRequest(t *testing.T) {
	const body = `{"state":"hi","model":"jev-latest"}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example/v1", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	first, n, err := SumGetBody(req.GetBody)
	if err != nil {
		t.Fatal(err)
	}
	if n != req.ContentLength {
		t.Errorf("n = %d, want ContentLength %d", n, req.ContentLength)
	}
	for attempt := 2; attempt <= 3; attempt++ {
		again, _, err := SumGetBody(req.GetBody)
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Errorf("attempt %d digest %s, want %s", attempt, again, first)
		}
	}

	req.GetBody = nil
	if _, _, err := SumGetBody(req.GetBody); !errors.Is(err, errNoGetBody) {
		t.Errorf("SumGetBody(nil) err = %v, want %v", err, errNoGetBody)
	}
}
