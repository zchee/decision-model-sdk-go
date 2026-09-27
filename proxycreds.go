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
	"cmp"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// maxProxyUserinfos bounds the proxy userinfos a [proxyCreds] remembers.
const maxProxyUserinfos = 16

// proxyCreds is the credentials of the proxies the proxy func of the SDK's
// own transport chose (ruling D-W6-secfix-m2): the userinfo of each proxy
// URL the func returned, recorded where net/http asks it
// (h2gate.Config.OnProxy), never by asking it again, so that a func with
// side effects or one that rotates proxies is asked only as net/http asks
// it. It holds the most recent [maxProxyUserinfos] distinct userinfos and
// forgets the oldest; a proxy chosen again becomes the most recent.
//
// The set belongs to the transport, not to a request: a request that waited
// at the gate for another's dial shares that dial's error without asking the
// func (h2gate, R19), and its error still holds the credential of the proxy
// the dial chose. Every transport error of the client is scrubbed of every
// credential in the set, whatever its length ([transport.credentials]).
// The header of a response to a plain-HTTP request that went through a
// proxy, which the proxy may have written itself, a 407 among them, is
// scanned for those of 8 bytes or more, as for the API key
// ([transport.responseRedactor], [proxyCreds.inHeader]; ruling
// D-W6-secfix-header-scope).
//
// A nil *proxyCreds, a caller's transport's (WithHTTPTransport,
// WithRoundTripper), holds none. The set is written only when the func
// returns a proxy with userinfo, and read on the error path, for every
// response to check that it holds any (a lock-free load that allocates
// nothing), and for a header scan of a response through a proxy.
type proxyCreds struct {
	mu sync.Mutex
	// users are the distinct userinfos, oldest first.
	users []url.Userinfo
	// needles are the credentials of users, longest first
	// ([credentials.addProxy]); nil while users is empty. A new slice
	// replaces them when users gains or loses a member, so a reader never
	// sees one change.
	needles atomic.Pointer[credentials]
}

// record adds the userinfo of proxy, if it has one, as the most recent,
// forgetting the oldest when the set is full. It is h2gate's OnProxy.
func (p *proxyCreds) record(proxy *url.URL) {
	if proxy.User == nil {
		return
	}
	user := *proxy.User
	p.mu.Lock()
	defer p.mu.Unlock()
	i := slices.Index(p.users, user)
	switch {
	case i == len(p.users)-1 && i >= 0:
		return // the most recent already, the common case: one proxy
	case i >= 0:
		p.users = append(slices.Delete(p.users, i, i+1), user)
		return // the same members: the needles stand
	case len(p.users) == maxProxyUserinfos:
		p.users = slices.Delete(p.users, 0, 1)
	}
	p.users = append(p.users, user)
	var c credentials
	for i := range p.users {
		c.addProxy(&p.users[i])
	}
	slices.SortFunc(c, longestFirst)
	p.needles.Store(&c)
}

// credentials returns the needles of every userinfo in p, longest first, or
// nil; the slice is shared and must not be modified.
func (p *proxyCreds) credentials() credentials {
	if p == nil {
		return nil
	}
	if c := p.needles.Load(); c != nil {
		return *c
	}
	return nil
}

// inHeader reports whether one of values, a header's, holds a credential
// in p at least [minKeyNeedleBytes] long (ruling R68 on the header paths): a
// shorter one would match ordinary header values.
func (p *proxyCreds) inHeader(values []string) bool {
	needles := p.credentials()
	if len(needles) == 0 {
		return false
	}
	return slices.ContainsFunc(values, func(v string) bool {
		return slices.ContainsFunc(needles, func(n string) bool { return keyNeedle(n) && strings.Contains(v, n) })
	})
}

// longestFirst orders credentials so that a whole value is replaced before
// a credential inside it; equal lengths in a fixed order, so the result does
// not depend on map order.
func longestFirst(a, b string) int { return cmp.Or(cmp.Compare(len(b), len(a)), strings.Compare(a, b)) }
