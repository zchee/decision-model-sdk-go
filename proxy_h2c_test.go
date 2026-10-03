// Copyright 2026 The decision-model-sdk-go Authors.
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

package decision

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/internal/testsupport"
)

// TestProxyH2CEchoRedactedOnEveryCall pins the response header scan on a
// held h2c connection to a proxy. With HTTP2Only on a plain-http base URL
// the transport speaks h2c prior knowledge to the proxy, and every call
// after the first rides that one connection. The scan applies to a
// response only when the proxy func returned a proxy for its request, and
// net/http asks the func for every request, the ones on the held
// connection included (three of three on Go 1.27.1). A Go release that
// stopped asking it there would leave the scan off from the second call
// on, and the proxy's password would show in the request id and the
// headers it wrote: the test then fails on the func's
// count, by name, before it reads a sink.
//
// The proxy's body does not repeat the password: APIError.Message shows
// body text as it arrived, a separate rule.
func TestProxyH2CEchoRedactedOnEveryCall(t *testing.T) {
	const password = "h2c-proxy-secret-020" // 20 bytes, one word
	var (
		mu     sync.Mutex
		protos []int
	)
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		protos = append(protos, r.ProtoMajor)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Echo", "denied "+password)
		w.Header().Set("X-Typesafe-Request-Id", "req-"+password)
		w.WriteHeader(http.StatusProxyAuthRequired)
		_, _ = io.WriteString(w, `{"message":"proxy authentication required"}`)
	}))
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	proxy.Config.Protocols = &protocols
	proxy.Start()
	t.Cleanup(proxy.Close)
	pu, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse the proxy's URL: %v", err)
	}
	pu.User = url.UserPassword(proxyUser, password)

	var asked atomic.Int64
	choose := func(*http.Request) (*url.URL, error) {
		asked.Add(1)
		return pu, nil
	}
	logs := testsupport.NewLogRecorder(slog.LevelDebug)
	c := newCredentialClient(t, logs.Logger(), WithBaseURL("http://api.example.test"), WithHTTPVersion(HTTP2Only), WithRetry(NoRetry()), WithProxy(choose))

	sinks := map[string]struct {
		got  func(ae *APIError, records []testsupport.LogRecord) string
		want string
	}{
		"success: the header the proxy wrote is ***": {
			got:  func(ae *APIError, _ []testsupport.LogRecord) string { return ae.Header.Get("X-Echo") },
			want: "***",
		},
		"success: the request id is ***": {
			got: func(ae *APIError, _ []testsupport.LogRecord) string {
				id, _ := ae.RequestID()
				return id
			},
			want: "***",
		},
		"success: the error's text holds no password": {
			got: func(ae *APIError, _ []testsupport.LogRecord) string {
				if s := ae.Error(); strings.Contains(s, password) {
					return s
				}
				return ""
			},
		},
		"success: no record holds the password": {
			got: func(_ *APIError, records []testsupport.LogRecord) string {
				var leaks []string
				for _, r := range records {
					if s := r.String(); strings.Contains(s, password) {
						leaks = append(leaks, s)
					}
				}
				return strings.Join(leaks, "\n")
			},
		},
	}

	for call := int64(1); call <= 3; call++ {
		logs.Reset()
		_, err := callWithin(t, func(ctx context.Context) error {
			_, err := c.Models().List(ctx)
			return err
		})
		ae, ok := errors.AsType[*APIError](err)
		if !ok {
			t.Fatalf("call %d: err = %T %v, want the proxy's 407 as an *APIError", call, err, err)
		}
		if ae.StatusCode != http.StatusProxyAuthRequired {
			t.Fatalf("call %d: StatusCode = %d, want 407", call, ae.StatusCode)
		}
		// The guard: net/http asked the func for this call's request.
		if got := asked.Load(); got != call {
			t.Fatalf("call %d: the proxy func was asked %d times, want %d: a request on the held h2c connection went out without it, so its response is not scanned", call, got, call)
		}
		records := logs.Records()
		for name, s := range sinks {
			t.Run(fmt.Sprintf("call %d/%s", call, name), func(t *testing.T) {
				if diff := gocmp.Diff(s.want, s.got(ae, records)); diff != "" {
					t.Errorf("mismatch (-want +got):\n%s", diff)
				}
			})
		}
	}

	// The premise: the three calls went to the proxy over h2c, on one
	// connection.
	mu.Lock()
	got := slices.Clone(protos)
	mu.Unlock()
	if diff := gocmp.Diff([]int{2, 2, 2}, got); diff != "" {
		t.Errorf("the requests' HTTP major versions at the proxy (-want +got):\n%s", diff)
	}
	if dials := c.Stats().Dials; dials != 1 {
		t.Errorf("Stats().Dials = %d, want 1: the calls did not share one held connection", dials)
	}
}
