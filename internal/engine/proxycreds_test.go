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
	"encoding/base64"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// proxyURL returns a proxy URL on 127.0.0.1 with userinfo user, nil for no
// userinfo.
func proxyURL(user *url.Userinfo) *url.URL {
	return &url.URL{Scheme: "http", User: user, Host: "127.0.0.1:3128"}
}

// passwords returns the password of each userinfo in p, oldest first.
func (p *ProxyCreds) passwords() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []string{}
	for _, u := range p.users {
		pw, _ := u.Password()
		out = append(out, pw)
	}
	return out
}

// TestProxyCredsRecord pins the transport's proxy credential set: distinct
// userinfos, most recent last, the oldest forgotten past 16, a proxy chosen
// again made the most recent; and the needles of each: the Basic token, the
// password as it is and as the URL escapes it, and each word between single
// spaces, whatever their length, with the forms a text may quote them in.
func TestProxyCredsRecord(t *testing.T) {
	pw := func(i int) *url.Userinfo { return url.UserPassword("proxy-user", "pw-"+strconv.Itoa(i)) }
	many := func(from, to int) []*url.URL {
		var out []*url.URL
		for i := from; i <= to; i++ {
			out = append(out, proxyURL(pw(i)))
		}
		return out
	}
	names := func(from, to int) []string {
		var out []string
		for i := from; i <= to; i++ {
			out = append(out, "pw-"+strconv.Itoa(i))
		}
		return out
	}
	tests := map[string]struct {
		record []*url.URL
		want   []string // the passwords held, oldest first
	}{
		"success: a URL without userinfo records nothing": {record: []*url.URL{proxyURL(nil)}, want: []string{}},
		"success: the same proxy twice is one entry":      {record: []*url.URL{proxyURL(pw(1)), proxyURL(pw(1))}, want: []string{"pw-1"}},
		"success: a proxy chosen again becomes the most recent": {
			record: []*url.URL{proxyURL(pw(1)), proxyURL(pw(2)), proxyURL(pw(1))}, want: []string{"pw-2", "pw-1"},
		},
		"success: sixteen proxies are all kept":       {record: many(1, 16), want: names(1, 16)},
		"success: the seventeenth forgets the oldest": {record: many(1, 17), want: names(2, 17)},
		"success: a kept proxy chosen again is not forgotten by the next": {
			record: append(append(many(1, 16), proxyURL(pw(1))), proxyURL(pw(17))), want: append(names(3, 16), "pw-1", "pw-17"),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var p ProxyCreds
			for _, u := range tt.record {
				p.Record(u)
			}
			if diff := gocmp.Diff(tt.want, p.passwords()); diff != "" {
				t.Errorf("passwords held (-want +got):\n%s", diff)
			}
			creds := p.Credentials()
			for _, held := range tt.want {
				if !slices.Contains(creds, held) {
					t.Errorf("the needles %q lack the held password %q", creds, held)
				}
			}
			if len(tt.want) == 0 && creds != nil {
				t.Errorf("an empty set's needles = %q, want nil", creds)
			}
		})
	}

	t.Run("success: the needles of a userinfo, whatever their length", func(t *testing.T) {
		var p ProxyCreds
		p.Record(proxyURL(url.UserPassword("u", "p w@<x")))
		token := base64.StdEncoding.EncodeToString([]byte("u:p w@<x"))
		want := []string{token, "p w@<x", "p%20w%40%3Cx", "p", "w@<x", `w@\u003cx`, `p w@\u003cx`}
		creds := p.Credentials()
		for _, n := range want {
			if !slices.Contains(creds, n) {
				t.Errorf("the needles %q lack %q", creds, n)
			}
		}
		for i := 1; i < len(creds); i++ {
			if len(creds[i]) > len(creds[i-1]) {
				t.Errorf("the needles %q are not longest first", creds)
				break
			}
		}
	})

	t.Run("success: a header value is scanned for the whole credentials of 8 bytes or more", func(t *testing.T) {
		var p ProxyCreds
		p.Record(proxyURL(url.UserPassword("u", "ab cdefghij")))
		p.Record(proxyURL(url.UserPassword("v", "short")))
		tests := map[string]struct {
			value string
			want  bool
		}{
			"success: a 2-byte word in an ordinary value is not a credential here": {value: "req_ab12"},
			"success: an 8-byte word is not a whole credential":                    {value: "seen cdefghij"},
			"success: a whole password under 8 bytes is not looked for":            {value: "seen short"},
			"success: the whole password":                                          {value: "ab cdefghij", want: true},
			"success: the whole password as the URL escapes it":                    {value: "ab%20cdefghij", want: true},
			"success: the Basic token":                                             {value: "Basic " + base64.StdEncoding.EncodeToString([]byte("u:ab cdefghij")), want: true},
		}
		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				if got := p.inHeader([]string{"x", tt.value}); got != tt.want {
					t.Errorf("inHeader(%q) = %t, want %t", tt.value, got, tt.want)
				}
			})
		}
		if !slices.Contains(p.Credentials(), "ab") {
			t.Errorf("the needles %q lack the 2-byte word, which an error's text is scrubbed of", p.Credentials())
		}
	})

	t.Run("success: a set whose whole credentials are all under 8 bytes has no header needles", func(t *testing.T) {
		var p ProxyCreds
		p.Record(proxyURL(url.UserPassword("u", "x"))) // the token of "u:x" is "dTp4", 4 bytes
		if p.inHeader([]string{"dTp4 x", "Basic dTp4"}) {
			t.Error("inHeader = true, want false: no whole credential is 8 bytes long")
		}
		if !slices.Contains(p.Credentials(), "x") {
			t.Errorf("the needles %q lack the password, which an error's text is scrubbed of", p.Credentials())
		}
	})

	t.Run("success: a user without a password: the token alone", func(t *testing.T) {
		var p ProxyCreds
		p.Record(proxyURL(url.User("only-user")))
		want := Credentials{base64.StdEncoding.EncodeToString([]byte("only-user:"))}
		if diff := gocmp.Diff(want, p.Credentials()); diff != "" {
			t.Errorf("needles (-want +got):\n%s", diff)
		}
	})

	t.Run("success: a nil set holds none", func(t *testing.T) {
		var p *ProxyCreds
		if c := p.Credentials(); c != nil || p.inHeader([]string{"anything"}) {
			t.Errorf("a nil set: credentials %q, inHeader true", c)
		}
	})

	t.Run("success: records at once from many goroutines keep the bound", func(t *testing.T) {
		var p ProxyCreds
		var wg sync.WaitGroup
		for g := range 32 {
			wg.Go(func() {
				for i := range 8 {
					p.Record(proxyURL(pw(g*8 + i)))
					_ = p.inHeader([]string{"pw-1"})
				}
			})
		}
		wg.Wait()
		held := p.passwords()
		if len(held) != MaxProxyUserinfos {
			t.Fatalf("held %d, want %d", len(held), MaxProxyUserinfos)
		}
		creds := p.Credentials()
		for _, h := range held {
			if !slices.Contains(creds, h) {
				t.Errorf("the needles lack the held %q", h)
			}
		}
	})
}
