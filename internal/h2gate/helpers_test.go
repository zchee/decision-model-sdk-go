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

package h2gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// Every loopback test here builds its own server and transport, so a test
// that fails leaves nothing behind for the next. Timing assertions carry the
// margins the W0.4 spike measured (docs/perf/ledger.md, W0.4 and W0.4b).
//
// Assertions about the order of client-side events read sequence numbers,
// not timestamps: on Windows time.Now advances in ticks (about 15.6 ms at the
// default timer resolution), so two events in a known order can carry the
// same time (K29, R75). A lower bound on an elapsed time allows one such
// tick, coarseClock.

// coarseClock is the error a measured duration may carry on a host whose
// clock and timers advance in ticks of up to about 15.6 ms (Windows).
const coarseClock = 20 * time.Millisecond

// traceSeq orders the client-side events the tests stamp: each stamp takes
// the next number, so an event that happens after another, by a chain of
// synchronisation, has the larger one whatever the clock's resolution.
var traceSeq atomic.Uint64

// mustURL parses a URL or fails the test.
func mustURL(tb testing.TB, raw string) *url.URL {
	tb.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		tb.Fatal(err)
	}
	return u
}

// newTestTransport builds cfg's default transport for a test: the test
// certificate is trusted, the connect timeout is 10 s unless cfg sets one,
// and the idle connections close when the test ends.
func newTestTransport(tb testing.TB, cfg Config) *Transport {
	tb.Helper()
	if cfg.RootCAs == nil {
		cfg.RootCAs = testsupport.RootCAs(tb)
	}
	tr, err := NewTransport(cfg)
	if err != nil {
		tb.Fatalf("NewTransport: %v", err)
	}
	tb.Cleanup(tr.CloseIdleConnections)
	return tr
}

// result is one completed call, with its client-side timeline.
type result struct {
	Path                                          string
	Start, GotConn, WroteHeaders, FirstByte, Done time.Time
	// WroteHeadersSeq, FirstByteSeq and DoneSeq order the same events by
	// traceSeq; zero when the event did not happen.
	WroteHeadersSeq, FirstByteSeq, DoneSeq uint64
	// Reused is GotConnInfo.Reused of the call's first GotConn: in a cold
	// burst only the leader's is false.
	Reused     bool
	GotConnSet bool
	Err        error
	Status     int
	ProtoMajor int
	Body       string
}

// timeline records a call's httptrace events; the hooks run on the
// transport's goroutines.
type timeline struct {
	mu                               sync.Mutex
	gotConn, wroteHeaders, firstByte time.Time
	wroteSeq, firstSeq               uint64
	reused, gotConnSet               bool
}

// traced returns ctx with hooks that record into a new timeline. They run
// after the Transport's own hooks (httptrace composes new before old).
func traced(ctx context.Context) (context.Context, *timeline) {
	tl := &timeline{}
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			now := time.Now()
			tl.mu.Lock()
			if !tl.gotConnSet {
				tl.gotConn, tl.reused, tl.gotConnSet = now, info.Reused, true
			}
			tl.mu.Unlock()
		},
		WroteHeaders: func() {
			now, seq := time.Now(), traceSeq.Add(1)
			tl.mu.Lock()
			if tl.wroteSeq == 0 {
				tl.wroteHeaders, tl.wroteSeq = now, seq
			}
			tl.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			now, seq := time.Now(), traceSeq.Add(1)
			tl.mu.Lock()
			if tl.firstSeq == 0 {
				tl.firstByte, tl.firstSeq = now, seq
			}
			tl.mu.Unlock()
		},
	}), tl
}

// do sends one request through rt and reads the whole response.
func do(ctx context.Context, rt http.RoundTripper, method, rawURL string, body []byte) result {
	var r io.Reader = http.NoBody
	if body != nil {
		r = bytes.NewReader(body)
	}
	ctx, tl := traced(ctx)
	req, err := http.NewRequestWithContext(ctx, method, rawURL, r)
	res := result{Path: req.URL.Path, Start: time.Now()}
	if err != nil {
		res.Err = err
		return res
	}
	resp, err := rt.RoundTrip(req)
	if err == nil {
		b, rerr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		res.Status, res.ProtoMajor, res.Body, res.Err = resp.StatusCode, resp.ProtoMajor, string(b), rerr
	} else {
		res.Err = err
	}
	res.Done, res.DoneSeq = time.Now(), traceSeq.Add(1)
	tl.mu.Lock()
	res.GotConn, res.WroteHeaders, res.FirstByte = tl.gotConn, tl.wroteHeaders, tl.firstByte
	res.WroteHeadersSeq, res.FirstByteSeq = tl.wroteSeq, tl.firstSeq
	res.Reused, res.GotConnSet = tl.reused, tl.gotConnSet
	tl.mu.Unlock()
	return res
}

// get sends a GET through rt.
func get(ctx context.Context, rt http.RoundTripper, rawURL string) result {
	return do(ctx, rt, http.MethodGet, rawURL, nil)
}

// fanOut runs fn for 0..n-1 at once, after a common start signal, and
// returns the results in index order.
func fanOut(n int, fn func(i int) result) []result {
	out := make([]result, n)
	var ready, wg sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	for i := range n {
		wg.Go(func() {
			ready.Done()
			<-start
			out[i] = fn(i)
		})
	}
	ready.Wait()
	close(start)
	wg.Wait()
	return out
}

// waitUntil polls cond every millisecond for up to 5 s.
func waitUntil(tb testing.TB, what string, cond func() bool) {
	tb.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			tb.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// pct returns the nearest-rank percentile p (0 < p <= 1) of ds.
func pct(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[max(int(math.Ceil(p*float64(len(s))))-1, 0)]
}

// ms formats d in milliseconds with three decimals.
func ms(d time.Duration) string { return fmt.Sprintf("%.3f", float64(d)/float64(time.Millisecond)) }

// record logs one machine-readable line, RESULT key=value ..., which the
// ledger's raw files keep (go test -v).
func record(tb testing.TB, kv ...any) {
	tb.Helper()
	var b strings.Builder
	b.WriteString("RESULT")
	for i := 0; i+1 < len(kv); i += 2 {
		v := fmt.Sprint(kv[i+1])
		if strings.ContainsAny(v, " \t\"=") || v == "" {
			v = fmt.Sprintf("%q", v)
		}
		fmt.Fprintf(&b, " %v=%s", kv[i], v)
	}
	tb.Log(b.String())
}

// errClass is the class the root package's classification gives a transport
// error after W2.5 (section 6.3 and plan W2.5): a *DialError keeps its own
// flags; otherwise a timeout (a context deadline or a net.Error whose
// Timeout() is true) is "timeout" and anything else is "connection".
// ErrNotNegotiated is "config". Nil is "ok".
func errClass(err error) string {
	var de *DialError
	var ne net.Error
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, &de) && de.Proxy:
		return "proxy"
	case errors.Is(err, ErrNotNegotiated):
		return "config"
	case errors.As(err, &de) && de.Timeout:
		return "timeout"
	case errors.As(err, &de):
		return "connection"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "connection"
	}
}

// classes counts results per errClass.
func classes(rs []result) map[string]int {
	m := map[string]int{}
	for _, r := range rs {
		m[errClass(r.Err)]++
	}
	return m
}

// firstErr returns the first error among rs, for a failure message.
func firstErr(rs []result) error {
	for _, r := range rs {
		if r.Err != nil {
			return r.Err
		}
	}
	return nil
}

// chain describes every error in err's tree, %T and message, depth first.
func chain(err error) string {
	var parts []string
	walk(err, func(e error) { parts = append(parts, fmt.Sprintf("%T(%s)", e, e.Error())) })
	return strings.Join(parts, " -> ")
}

// isConnReset reports a TCP reset from the peer: ECONNRESET, or
// WSAECONNRESET (10054) on Windows.
func isConnReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.Errno(10054))
}

// barrier is the AC-P4 handler: it holds every response of a key (the first
// path segment) until n requests of that key have arrived, or until guard
// expires (the ordering then fails). When free names a key, the first
// request of that key is answered after freeDelay instead (R29c: the leader
// is answered first; the handler cannot single the leader out otherwise).
type barrier struct {
	n         int
	guard     time.Duration
	free      string
	freeDelay time.Duration

	mu        sync.Mutex
	arrived   map[string]int
	all       map[string]chan struct{}
	guarded   map[string]int
	freeFirst string
}

// newBarrier returns a barrier for n requests per key with the given guard.
func newBarrier(n int, guard time.Duration) *barrier {
	return &barrier{n: n, guard: guard, arrived: map[string]int{}, all: map[string]chan struct{}{}, guarded: map[string]int{}}
}

// barrierKey returns the first path segment: /cold/3 → cold.
func barrierKey(path string) string {
	k, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	return k
}

// ServeHTTP implements http.Handler.
func (b *barrier) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := barrierKey(r.URL.Path)
	b.mu.Lock()
	b.arrived[key]++
	if key == b.free && b.arrived[key] == 1 {
		b.freeFirst = r.URL.Path
		b.mu.Unlock()
		if b.freeDelay > 0 {
			tm := time.NewTimer(b.freeDelay)
			defer tm.Stop()
			select {
			case <-tm.C:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	c, ok := b.all[key]
	if !ok {
		c = make(chan struct{})
		b.all[key] = c
	}
	if b.arrived[key] == b.n {
		close(c)
	}
	b.mu.Unlock()
	tm := time.NewTimer(b.guard)
	defer tm.Stop()
	select {
	case <-c:
		w.WriteHeader(http.StatusOK)
	case <-tm.C:
		b.mu.Lock()
		b.guarded[key]++
		b.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
	case <-r.Context().Done():
	}
}

// guardedCount returns how many handlers of key the guard released.
func (b *barrier) guardedCount(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.guarded[key]
}

// firstFree returns the path of the free key's first request.
func (b *barrier) firstFree() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.freeFirst
}

// serviceHandler answers 200 after d, or ends with the request.
func serviceHandler(d time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tm := time.NewTimer(d)
		defer tm.Stop()
		select {
		case <-tm.C:
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	})
}

// holdHandler serves paths under /hold/ only once release is closed (or the
// request ends) and every other path at once; the body names the path.
func holdHandler(release <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.HasPrefix(r.URL.Path, "/hold/") {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, "ok "+r.URL.Path) //nolint:gosec // G705: a test server echoes the path to its own test client
	})
}

// countingConn counts the bytes written through it after the TLS layer, so
// a test can assert that a refused connection carried no request.
type countingConn struct {
	net.Conn
	mu      sync.Mutex
	written int
}

// Write implements net.Conn.
func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.mu.Lock()
	c.written += n
	c.mu.Unlock()
	return n, err
}

// Written returns the bytes written so far.
func (c *countingConn) Written() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.written
}
