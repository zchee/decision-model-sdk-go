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
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// closeCounter wraps a request body and counts Close calls.
type closeCounter struct {
	io.Reader
	closes atomic.Int32
}

// Close implements io.Closer.
func (c *closeCounter) Close() error {
	c.closes.Add(1)
	return nil
}

// TestRecorderRoundTrip covers replies, recording and body handling through
// an http.Client, as the SDK's client tests will use it.
func TestRecorderRoundTrip(t *testing.T) {
	errDial := errors.New("dial refused")
	tests := map[string]struct {
		rec        *Recorder
		calls      int
		wantStatus []int
		wantErr    []error
		wantBodies []string
	}{
		"success: replies in order then the last repeats": {
			rec:        &Recorder{Replies: []Reply{{Status: http.StatusServiceUnavailable}, JSON(http.StatusOK, []byte(`{"ok":true}`))}},
			calls:      3,
			wantStatus: []int{503, 200, 200},
			wantErr:    []error{nil, nil, nil},
			wantBodies: []string{"", `{"ok":true}`, `{"ok":true}`},
		},
		"success: a transport error reply": {
			rec:        &Recorder{Replies: []Reply{{Err: errDial}, JSON(http.StatusOK, []byte("{}"))}},
			calls:      2,
			wantStatus: []int{0, 200},
			wantErr:    []error{errDial, nil},
			wantBodies: []string{"", "{}"},
		},
		"success: Respond chooses per request": {
			rec: &Recorder{Respond: func(r RecordedRequest) Reply {
				return Reply{Status: http.StatusOK, Body: append([]byte("echo:"), r.Body...)}
			}},
			calls:      2,
			wantStatus: []int{200, 200},
			wantErr:    []error{nil, nil},
			wantBodies: []string{"echo:body-0", "echo:body-1"},
		},
		"error: no replies at all": {
			rec:        &Recorder{},
			calls:      1,
			wantStatus: []int{0},
			wantErr:    []error{errNoReplies},
			wantBodies: []string{""},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: tt.rec}
			for i := range tt.calls {
				body := "body-" + string(rune('0'+i))
				req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.typesafe.ai/v1/systemone?x=1", strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("X-TypeSafe-Retry-Count", "1")
				resp, err := client.Do(req)
				if !errors.Is(err, tt.wantErr[i]) {
					t.Fatalf("call %d: error = %v, want %v", i, err, tt.wantErr[i])
				}
				if err != nil {
					continue
				}
				got, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					t.Fatalf("call %d: read body: %v", i, err)
				}
				if resp.StatusCode != tt.wantStatus[i] || string(got) != tt.wantBodies[i] {
					t.Errorf("call %d: %d %q, want %d %q", i, resp.StatusCode, got, tt.wantStatus[i], tt.wantBodies[i])
				}
			}
			reqs := tt.rec.Requests()
			if len(reqs) != tt.calls || tt.rec.Count() != tt.calls {
				t.Fatalf("recorded %d requests, Count %d, want %d", len(reqs), tt.rec.Count(), tt.calls)
			}
			for i, r := range reqs {
				want := RecordedRequest{
					Index:         i,
					Method:        http.MethodPost,
					URL:           "https://api.typesafe.ai/v1/systemone?x=1",
					Host:          "api.typesafe.ai",
					Body:          []byte("body-" + string(rune('0'+i))),
					ContentLength: 6,
					HasGetBody:    true,
				}
				if diff := gocmp.Diff(want, r, cmpopts.IgnoreFields(RecordedRequest{}, "Header")); diff != "" {
					t.Errorf("request %d (-want +got):\n%s", i, diff)
				}
				if got := r.Header.Get("X-TypeSafe-Retry-Count"); got != "1" {
					t.Errorf("request %d: header X-TypeSafe-Retry-Count = %q", i, got)
				}
			}
		})
	}
}

// TestRecorderContract covers the RoundTripper contract details the SDK's
// tests rely on.
func TestRecorderContract(t *testing.T) {
	t.Run("success: the body is read and closed before RoundTrip returns", func(t *testing.T) {
		rec := &Recorder{Replies: []Reply{{}}}
		body := &closeCounter{Reader: strings.NewReader("payload")}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.com/", body)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := rec.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if got := body.closes.Load(); got != 1 {
			t.Errorf("body closed %d times, want 1", got)
		}
		if got := string(rec.Requests()[0].Body); got != "payload" {
			t.Errorf("recorded body %q", got)
		}
	})

	t.Run("success: a cancelled context returns its error and serves no reply", func(t *testing.T) {
		rec := &Recorder{Replies: []Reply{{Status: http.StatusTeapot}, {Status: http.StatusOK}}}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		body := &closeCounter{Reader: strings.NewReader("x")}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.com/", body)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := rec.RoundTrip(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RoundTrip error = %v, want context.Canceled", err)
		}
		if body.closes.Load() != 1 {
			t.Errorf("the body of a cancelled request was not closed")
		}
		req2, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com/", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err = rec.RoundTrip(req2)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusTeapot {
			t.Errorf("the reply after a cancelled call = %d, want the first reply 418", resp.StatusCode)
		}
		if reqs := rec.Requests(); len(reqs) != 2 || !reqs[0].Canceled || reqs[1].Canceled {
			t.Errorf("Canceled flags = %+v", reqs)
		}
	})

	t.Run("success: declared and undeclared lengths", func(t *testing.T) {
		tests := map[string]struct {
			reply    Reply
			wantCL   int64
			wantBody string
			wantErr  error
		}{
			"success: length from the body":       {reply: Reply{Body: []byte("12345")}, wantCL: 5, wantBody: "12345"},
			"success: chunked, no length":         {reply: Reply{Body: []byte("12345"), ContentLength: -1}, wantCL: -1, wantBody: "12345"},
			"error: declared more than delivered": {reply: Reply{Body: []byte("12345"), ContentLength: 16 << 20}, wantCL: 16 << 20, wantBody: "12345", wantErr: io.ErrUnexpectedEOF},
			"success: empty body":                 {reply: Reply{}, wantCL: 0, wantBody: ""},
		}
		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				rec := &Recorder{Replies: []Reply{tt.reply}}
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com/", http.NoBody)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := rec.RoundTrip(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				got, err := io.ReadAll(resp.Body)
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("read error = %v, want %v", err, tt.wantErr)
				}
				if resp.ContentLength != tt.wantCL || string(got) != tt.wantBody {
					t.Errorf("ContentLength %d body %q, want %d %q", resp.ContentLength, got, tt.wantCL, tt.wantBody)
				}
				if resp.ProtoMajor != 2 || resp.StatusCode != http.StatusOK || resp.Request != req {
					t.Errorf("response %d HTTP/%d request %p", resp.StatusCode, resp.ProtoMajor, resp.Request)
				}
			})
		}
	})

	t.Run("success: the canned header is copied per response", func(t *testing.T) {
		rec := &Recorder{Replies: []Reply{JSON(http.StatusOK, []byte("{}"))}}
		for range 2 {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com/", http.NoBody)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := rec.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			resp.Header.Set("Content-Type", "mutated")
		}
		if got := rec.Replies[0].Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("the canned header changed to %q", got)
		}
	})

	t.Run("success: discard keeps only the count", func(t *testing.T) {
		var seen []RecordedRequest
		rec := &Recorder{Discard: true, Respond: func(r RecordedRequest) Reply {
			seen = append(seen, r)
			return Reply{}
		}}
		body := &closeCounter{Reader: bytes.NewReader(make([]byte, 1<<20))}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.com/", body)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := rec.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if rec.Count() != 1 || len(rec.Requests()) != 0 || body.closes.Load() != 1 {
			t.Errorf("Count %d, Requests %d, closes %d; want 1, 0, 1", rec.Count(), len(rec.Requests()), body.closes.Load())
		}
		if len(seen) != 1 || seen[0].Body != nil || seen[0].Method != http.MethodPost {
			t.Errorf("Respond saw %+v", seen)
		}
	})

	t.Run("success: Close is counted", func(t *testing.T) {
		rec := &Recorder{}
		var closer io.Closer = rec
		for range 2 {
			if err := closer.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if got := rec.Closes(); got != 2 {
			t.Errorf("Closes() = %d, want 2", got)
		}
	})
}

// TestRecorderConcurrent runs many round trips at once; -race checks the
// locking, the count checks that none is lost.
func TestRecorderConcurrent(t *testing.T) {
	rec := &Recorder{Replies: []Reply{JSON(http.StatusOK, []byte("{}"))}}
	client := &http.Client{Transport: rec}
	const n = 64
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.com/", strings.NewReader("x"))
			if err != nil {
				t.Error(err)
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		})
	}
	wg.Wait()
	reqs := rec.Requests()
	if len(reqs) != n || rec.Count() != n {
		t.Fatalf("recorded %d, Count %d, want %d", len(reqs), rec.Count(), n)
	}
	seen := make(map[int]bool, n)
	for _, r := range reqs {
		seen[r.Index] = true
	}
	if len(seen) != n {
		t.Errorf("indexes are not unique: %d distinct of %d", len(seen), n)
	}
}
