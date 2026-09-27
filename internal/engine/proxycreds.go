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

package engine

import (
	"cmp"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// MaxProxyUserinfos bounds the proxy userinfos a [ProxyCreds] remembers.
const MaxProxyUserinfos = 16

// ProxyCreds is the credentials of the proxies the proxy func of the SDK's
// own transport chose (ruling D-W6-secfix-m2): the userinfo of each proxy
// URL the func returned, recorded where net/http asks it
// (h2gate.Config.OnProxy), never by asking it again, so that a func with
// side effects or one that rotates proxies is asked only as net/http asks
// it. It holds the most recent [MaxProxyUserinfos] distinct userinfos and
// forgets the oldest; a proxy chosen again becomes the most recent.
//
// The set belongs to the transport, not to a request: a request that waited
// at the gate for another's dial shares that dial's error without asking the
// func (h2gate, R19), and its error still holds the credential of the proxy
// the dial chose. Every transport error of the client is scrubbed of every
// credential in the set, whatever its length, each word of a password
// among them ([Transport.Credentials]). The header of a
// response to a plain-HTTP request that went through a proxy, which the
// proxy may have written itself, a 407 among them, is scanned for the whole
// credentials alone, the Basic token and the password as it is and as the
// URL escapes it, those of 8 bytes or more, as for the API key
// ([Transport.ResponseRedactor], [HeaderRedactor.WithProxies],
// [ProxyCreds.inHeader]; rulings D-W6-secfix-header-scope and -2): a word of
// a password, or a short one, would match ordinary header values, a
// Retry-After among them.
//
// A nil *ProxyCreds, a caller's transport's (WithHTTPTransport,
// WithRoundTripper), holds none. The set is written only when the func
// returns a proxy with userinfo, and read on the error path, for every
// response to check that it holds any (a lock-free load that allocates
// nothing), and for a header scan of a response through a proxy.
type ProxyCreds struct {
	mu sync.Mutex
	// users are the distinct userinfos, oldest first.
	users []url.Userinfo
	// needles are the credentials of users, nil while users is empty. A
	// new value replaces them when users gains or loses a member, so a
	// reader never sees one change.
	needles atomic.Pointer[proxyNeedles]
}

// proxyNeedles are what a [ProxyCreds] looks for, each longest first.
type proxyNeedles struct {
	// text are every credential of the userinfos, words of the passwords
	// included, whatever their length ([Credentials.addProxy]): an error's
	// text is scrubbed of them.
	text Credentials
	// header are the whole credentials of 8 bytes or more, a response
	// header's needles ([ProxyCreds.inHeader]).
	header Credentials
}

// Record adds the userinfo of proxy, if it has one, as the most recent,
// forgetting the oldest when the set is full. It is h2gate's OnProxy.
func (p *ProxyCreds) Record(proxy *url.URL) {
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
	case len(p.users) == MaxProxyUserinfos:
		p.users = slices.Delete(p.users, 0, 1)
	}
	p.users = append(p.users, user)
	var n proxyNeedles
	for i := range p.users {
		n.text.addProxy(&p.users[i], true)
		n.header.addProxy(&p.users[i], false)
	}
	n.header = slices.DeleteFunc(n.header, func(v string) bool { return !KeyNeedle(v) })
	slices.SortFunc(n.text, longestFirst)
	slices.SortFunc(n.header, longestFirst)
	p.needles.Store(&n)
}

// Snapshot returns a set that holds the credentials p, which must not be
// nil, holds now and never changes: what a response keeps to redact its
// header after its call has returned, when p may since have forgotten the
// proxy the response went through ([HeaderRedactor.ResponseFunc]). It costs
// one allocation; the credentials themselves are shared, since p replaces
// rather than changes them.
func (p *ProxyCreds) Snapshot() *ProxyCreds {
	s := new(ProxyCreds)
	s.needles.Store(p.needles.Load())
	return s
}

// Credentials returns every credential of every userinfo in p, the text
// needles, longest first, or nil; the slice is shared and must not be
// modified.
func (p *ProxyCreds) Credentials() Credentials {
	if p == nil {
		return nil
	}
	if n := p.needles.Load(); n != nil {
		return n.text
	}
	return nil
}

// inHeader reports whether one of values, a header's, holds a whole
// credential in p, the Basic token or the password as it is or as the URL
// escapes it, at least [MinKeyNeedleBytes] long (ruling R68 on the header
// paths, D-W6-secfix-header-scope-2): a word of a password, or a shorter
// credential, would match ordinary header values.
func (p *ProxyCreds) inHeader(values []string) bool {
	if p == nil {
		return false
	}
	n := p.needles.Load()
	if n == nil || len(n.header) == 0 {
		return false
	}
	return slices.ContainsFunc(values, func(v string) bool {
		return slices.ContainsFunc(n.header, func(c string) bool { return strings.Contains(v, c) })
	})
}

// longestFirst orders credentials so that a whole value is replaced before
// a credential inside it; equal lengths in a fixed order, so the result does
// not depend on map order.
func longestFirst(a, b string) int { return cmp.Or(cmp.Compare(len(b), len(a)), strings.Compare(a, b)) }
