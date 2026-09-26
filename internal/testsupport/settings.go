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
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/net/http2"
)

// PeerSettings is what an HTTP/2 server announces in the first SETTINGS
// frame of a connection (RFC 9113 section 6.5), as [ReadPeerSettings]
// reads it.
type PeerSettings struct {
	// Protocol is the ALPN protocol the TLS handshake negotiated: "h2".
	Protocol string
	// Values holds every setting of the frame by its RFC 9113 name, such as
	// "MAX_CONCURRENT_STREAMS"; a setting the server leaves out keeps its
	// initial value and has no entry (for MAX_CONCURRENT_STREAMS, no limit).
	Values map[string]uint32
	// Handshake is the time from the start of the dial to the end of the
	// TLS handshake; FirstSettings the time from the start of the dial to
	// the arrival of the SETTINGS frame.
	Handshake, FirstSettings time.Duration
}

// MaxConcurrentStreams returns the server's SETTINGS_MAX_CONCURRENT_STREAMS
// and whether the frame carried it.
func (s PeerSettings) MaxConcurrentStreams() (uint32, bool) {
	v, ok := s.Values[http2.SettingMaxConcurrentStreams.String()]
	return v, ok
}

// ReadPeerSettings dials addr ("host:port") over TLS offering only h2,
// writes the client connection preface and an empty SETTINGS frame, and
// returns the settings of the server's first SETTINGS frame. It sends no
// request, so no credential is involved; it closes the connection with
// GOAWAY before it returns. cfg is cloned and its NextProtos replaced by
// "h2"; a nil cfg verifies the server against the system roots, with the
// server name taken from addr. A handshake that negotiates another
// protocol, or a first frame that is not SETTINGS, is an error.
func ReadPeerSettings(ctx context.Context, addr string, cfg *tls.Config) (PeerSettings, error) {
	var conf *tls.Config
	if cfg != nil {
		conf = cfg.Clone()
	} else {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return PeerSettings{}, fmt.Errorf("testsupport: peer settings: %w", err)
		}
		conf = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	conf.NextProtos = []string{http2.NextProtoTLS}

	start := time.Now()
	d := tls.Dialer{Config: conf}
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return PeerSettings{}, fmt.Errorf("testsupport: peer settings: dial %s: %w", addr, err)
	}
	tc := nc.(*tls.Conn)
	defer tc.Close()
	out := PeerSettings{
		Protocol:  tc.ConnectionState().NegotiatedProtocol,
		Values:    make(map[string]uint32),
		Handshake: time.Since(start),
	}
	if out.Protocol != http2.NextProtoTLS {
		return out, fmt.Errorf("testsupport: peer settings: %s negotiated %q, not h2", addr, out.Protocol)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = tc.SetDeadline(deadline)
	}
	if _, err := tc.Write([]byte(http2.ClientPreface)); err != nil {
		return out, fmt.Errorf("testsupport: peer settings: preface: %w", err)
	}
	fr := http2.NewFramer(tc, tc)
	if err := fr.WriteSettings(); err != nil {
		return out, fmt.Errorf("testsupport: peer settings: SETTINGS: %w", err)
	}
	f, err := fr.ReadFrame()
	if err != nil {
		return out, fmt.Errorf("testsupport: peer settings: read: %w", err)
	}
	sf, ok := f.(*http2.SettingsFrame)
	if !ok || sf.IsAck() {
		return out, fmt.Errorf("testsupport: peer settings: the first frame is %v, not SETTINGS", f.Header())
	}
	out.FirstSettings = time.Since(start)
	_ = sf.ForeachSetting(func(s http2.Setting) error {
		out.Values[s.ID.String()] = s.Val
		return nil
	})
	// Acknowledge, then close politely: the server sees a client that went
	// away without opening a stream.
	err = errors.Join(fr.WriteSettingsAck(), fr.WriteGoAway(0, http2.ErrCodeNo, nil))
	if err != nil {
		return out, fmt.Errorf("testsupport: peer settings: close: %w", err)
	}
	return out, nil
}
