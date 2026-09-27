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
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/engine"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// netTimeout is a net.Error whose Timeout is true, as a dial or read
// deadline reports.
type netTimeout struct{}

func (netTimeout) Error() string   { return "i/o timeout" }
func (netTimeout) Timeout() bool   { return true }
func (netTimeout) Temporary() bool { return true }

// clearEnv unsets, for the rest of the test, the three variables a client
// reads, whatever the shell running the test holds (a developer's
// TYPESAFE_API_KEY among them); t.Setenv restores them when the test ends.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{APIKeyEnv, BaseURLEnv, DefaultModelEnv} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}

// noulQuestion is the question set of the upstream response tests,
// {"q": {"type": "noul", "instructions": "?"}}.
func noulQuestion(t *testing.T) *Prepared {
	t.Helper()
	qs, err := NewQuestions().Noul("q", Noul{Instructions: Text("?")}).Prepare()
	if err != nil {
		t.Fatal(err)
	}
	return qs
}

// validationError asserts that err is a *ResponseValidationError.
func validationError(t *testing.T, err error) *ResponseValidationError {
	t.Helper()
	rve, ok := errors.AsType[*ResponseValidationError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want a *ResponseValidationError", err, err)
	}
	return rve
}

// The timing rules of the transport tests: every measured span
// is at least 250 ms, a lower bound allows one tick of the coarsest CI clock,
// and every wait has a deadline.
const (
	// span is the length of every deadline and every delay a test measures.
	span = 300 * time.Millisecond
	// coarseTick is one tick of the coarsest CI clock (windows-2025).
	coarseTick = 20 * time.Millisecond
	// callBound is the longest any call of these tests may take.
	callBound = 15 * time.Second
)

// callWithin runs call on its own goroutine and returns how long it took
// and its error, failing the test when it does not return within callBound
// plus 5 s: the call's context carries callBound, so a transport that
// honours it returns first.
func callWithin(t *testing.T, call func(ctx context.Context) error) (time.Duration, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), callBound)
	defer cancel()
	type result struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		err := call(ctx)
		done <- result{err: err, elapsed: time.Since(start)}
	}()
	select {
	case r := <-done:
		return r.elapsed, r.err
	case <-time.After(callBound + 5*time.Second):
		t.Fatalf("the call did not return within %v", callBound+5*time.Second)
		return 0, nil
	}
}

// standInType is the type of the stand-in internal/engine's credential scrub
// returns for a transport error whose chain printed a credential
// (engine.Credentials.Cause). The type is unexported there, so the root
// package's tests take it from a stand-in the scrub itself built.
var standInType = func() reflect.Type {
	const key = "ts_live_0123456789abcdef"
	plain := errors.New("rejected " + key)
	s := engine.RequestCredentials(http.Header{"Authorization": {"Bearer " + key}}).Cause(plain)
	if s == plain { //nolint:errorlint // identity: the scrub kept the error
		panic("engine.Credentials.Cause kept an error whose text holds the key; there is no stand-in to recognise")
	}
	return reflect.TypeOf(s)
}()

// isStandIn reports whether err is the scrub's stand-in (standInType).
func isStandIn(err error) bool { return err != nil && reflect.TypeOf(err) == standInType }

// chainHolds reports whether err, or an error its chain wraps (errors.Unwrap,
// both forms, as errors.As walks it), satisfies match.
func chainHolds(err error, match func(error) bool) bool {
	if err == nil {
		return false
	}
	if match(err) {
		return true
	}
	switch u := err.(type) { //nolint:errorlint // visits each link of the chain as it is.
	case interface{ Unwrap() error }:
		return chainHolds(u.Unwrap(), match)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			if chainHolds(e, match) {
				return true
			}
		}
	}
	return false
}

// quoted returns v as Go's %q quotes it, without the quotes: a form in
// which an error text may show a credential.
func quoted(v string) string {
	q := strconv.Quote(v)
	return q[1 : len(q)-1]
}

// jsonQuoted returns v as encoding/json writes it inside a JSON string
// (through testsupport.StdlibMarshal: the root package's tests import no JSON
// library), without the quotes: another such form.
func jsonQuoted(t *testing.T, v string) string {
	t.Helper()
	b, err := testsupport.StdlibMarshal(v)
	if err != nil {
		t.Fatalf("StdlibMarshal: %v", err)
	}
	return string(b[1 : len(b)-1])
}

// quirkyKey is an API key with a quote, a double quote and a backslash, the
// upstream credential "ts_live_quo'te\"slash\\tail"
// (tests/test_logging.py:88), whose quoted forms differ from its raw one.
const quirkyKey = `ts_live_quo'te"slash\tail`

// testKey is the root tests' default API key (the upstream tests' key):
// 8 bytes long, so the checks that look for the key inside other text
// apply to it.
const testKey = "test-key"

// getResult is what a GET through a client's transport produced.
type getResult struct {
	status, protoMajor int
	err                error
}

// getVia sends GET rawURL through tr with the attempt timeout timeout, reads
// the whole response body and closes it.
func getVia(ctx context.Context, tr *engine.Transport, rawURL string, timeout time.Duration) getResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return getResult{err: err}
	}
	resp, err := roundTrip(tr, req, timeout)
	if err != nil {
		return getResult{err: err}
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return getResult{status: resp.StatusCode, protoMajor: resp.ProtoMajor, err: err}
}

// errString is an error with a fixed text.
type errString string

func (e errString) Error() string { return string(e) }

// headers returns an http.Header with the pairs set as a server sends them.
func headers(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}
