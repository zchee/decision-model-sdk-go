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
	"fmt"
	"net"
	"net/netip"
)

// Routes maps a dial address, host:port as net/http passes it to a dialer
// (for example "example.com:443"), to the loopback address that serves it.
//
// A test that uses example.com as the API host (covered by the package
// certificate) routes it to a [LoopbackServer] with Routes, both in the
// client transport's DialContext and in a [Proxy].
type Routes map[string]string

// Resolve returns the address to dial for addr: the routed address when addr
// has a route, addr itself when it is a loopback IP literal, and an error
// otherwise, so a test can never reach the network by accident.
func (r Routes) Resolve(addr string) (string, error) {
	if to, ok := r[addr]; ok {
		return to, nil
	}
	ap, err := netip.ParseAddrPort(addr)
	if err == nil && ap.Addr().IsLoopback() {
		return addr, nil
	}
	return "", fmt.Errorf("testsupport: no route for %q (only routed or loopback addresses are dialed)", addr)
}

// DialContext dials the address [Routes.Resolve] returns for addr. It has
// the signature of [net/http.Transport.DialContext].
func (r Routes) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	to, err := r.Resolve(addr)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	return d.DialContext(ctx, network, to)
}
