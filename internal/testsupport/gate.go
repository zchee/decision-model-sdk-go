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
	"net"
	"sync/atomic"
)

// GatedDialer holds every dial until a gate channel is closed, then dials.
// It gives a test a dial that completes when the test says so: a cold-start
// leader cancelled while its dial is in flight, or a waiter whose bound
// expires before the connection exists. Install [GatedDialer.DialContext] as
// the client transport's DialContext, wait for [GatedDialer.Waiting] to show
// the dial, act, then close the gate.
type GatedDialer struct {
	gate    <-chan struct{}
	dial    func(ctx context.Context, network, addr string) (net.Conn, error)
	waiting atomic.Int64
	dials   atomic.Int64
}

// NewGatedDialer returns a dialer whose dials wait for gate to be closed and
// then go through dial, such as a [Routes] DialContext; a nil dial uses a
// zero [net.Dialer].
func NewGatedDialer(gate <-chan struct{}, dial func(ctx context.Context, network, addr string) (net.Conn, error)) *GatedDialer {
	if dial == nil {
		var d net.Dialer
		dial = d.DialContext
	}
	return &GatedDialer{gate: gate, dial: dial}
}

// DialContext waits until the gate is closed and then dials addr; when ctx
// ends first, it returns ctx's error without dialing. It has the signature of
// [net/http.Transport.DialContext].
func (g *GatedDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	g.waiting.Add(1)
	select {
	case <-g.gate:
		g.waiting.Add(-1)
	case <-ctx.Done():
		g.waiting.Add(-1)
		return nil, ctx.Err()
	}
	g.dials.Add(1)
	return g.dial(ctx, network, addr)
}

// Waiting returns the number of dials held at the gate now.
func (g *GatedDialer) Waiting() int { return int(g.waiting.Load()) }

// Dials returns the number of dials that passed the gate so far.
func (g *GatedDialer) Dials() int { return int(g.dials.Load()) }
