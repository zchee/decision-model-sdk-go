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
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
)

// TestReadPeerSettings reads the first SETTINGS frame of a loopback server
// with and without a MAX_CONCURRENT_STREAMS limit, and refuses a server that
// does not negotiate h2. The live test of K22 (livetests) reads the API's
// frame with the same function, so what it reports is checked here against
// a server whose frame is known.
func TestReadPeerSettings(t *testing.T) {
	tests := map[string]struct {
		cfg     ServerConfig
		want    map[string]uint32
		limit   uint32
		limited bool
		errPart string
	}{
		"success: a limit of 8 is advertised": {
			cfg:     ServerConfig{MaxConcurrentStreams: 8},
			want:    map[string]uint32{"MAX_CONCURRENT_STREAMS": 8},
			limit:   8,
			limited: true,
		},
		"success: no limit leaves the setting out": {
			cfg:  ServerConfig{},
			want: map[string]uint32{},
		},
		"error: a server offering only http/1.1 refuses the h2-only handshake": {
			cfg:     ServerConfig{ALPN: ALPNHTTP1Only},
			errPart: "dial",
		},
		"error: a server without ALPN negotiates no protocol": {
			cfg:     ServerConfig{ALPN: ALPNNone},
			errPart: `negotiated "", not h2`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := NewLoopbackServer(t, tt.cfg)
			got, err := ReadPeerSettings(t.Context(), srv.Addr(), ClientTLSConfig(t))
			if tt.errPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errPart) {
					t.Fatalf("ReadPeerSettings() error = %v, want one containing %q", err, tt.errPart)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadPeerSettings() error = %v", err)
			}
			if got.Protocol != "h2" {
				t.Errorf("Protocol = %q, want h2", got.Protocol)
			}
			if diff := gocmp.Diff(tt.want, got.Values); diff != "" {
				t.Errorf("Values (-want +got):\n%s", diff)
			}
			if limit, ok := got.MaxConcurrentStreams(); limit != tt.limit || ok != tt.limited {
				t.Errorf("MaxConcurrentStreams() = %d, %t, want %d, %t", limit, ok, tt.limit, tt.limited)
			}
			if got.Handshake <= 0 || got.FirstSettings < got.Handshake || got.FirstSettings > time.Minute {
				t.Errorf("Handshake = %v, FirstSettings = %v: want 0 < Handshake <= FirstSettings", got.Handshake, got.FirstSettings)
			}
			// The probe opened no stream.
			if n := len(srv.Requests()); n != 0 {
				t.Errorf("the server saw %d requests, want 0", n)
			}
		})
	}
}
