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

package testsupport

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ErrorTexts returns every way err and each error it wraps can be printed:
// %v, %+v, %#v, %s and %q.
func ErrorTexts(err error) []string {
	var texts []string
	var walk func(error)
	walk = func(err error) {
		if err == nil {
			return
		}
		texts = append(texts, fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err), fmt.Sprintf("%s", err), fmt.Sprintf("%q", err))
		switch u := err.(type) { //nolint:errorlint // visits each link of the chain as it is; errors.As would skip links.
		case interface{ Unwrap() error }:
			walk(u.Unwrap())
		case interface{ Unwrap() []error }:
			for _, e := range u.Unwrap() {
				walk(e)
			}
		}
	}
	walk(err)
	return texts
}

// AssertNotPrinted fails the test when any printed form of err, or of an
// error it wraps, contains secret.
func AssertNotPrinted(tb testing.TB, err error, secret string) {
	tb.Helper()
	for _, text := range ErrorTexts(err) {
		if strings.Contains(text, secret) {
			tb.Errorf("error text %q contains %q", text, secret)
		}
	}
}

// DuplicatesLastWins is testdata/duplicates.json with every repeated member
// resolved as Python 0.7.1 resolves it: the last one wins at every level, and
// a repeated object replaces the earlier one whole (usage loses
// output_tokens).
const DuplicatesLastWins = `{"model":"jev-latest","usage":{"input_tokens":12},"answers":{"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},"spam":{"type":"noul","noul":0.98},"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"fine","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}},"risk":{"type":"score","score":0,"confidence":1,"legend":{"0":{"summary":"low"}},"probabilities":{"0":1}}}}`

// IsConnReset reports whether err is a TCP reset from the peer: ECONNRESET,
// or WSAECONNRESET (10054), the code Windows reports for it. The message
// differs by system, so the check compares codes, not text.
func IsConnReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.Errno(10054))
}

// RoundTripFunc adapts a function to http.RoundTripper.
type RoundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f RoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// WaitUntil polls cond every millisecond for up to 5 s.
func WaitUntil(tb testing.TB, what string, cond func() bool) {
	tb.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			tb.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// AnswerH2ExampleCom answers 200 "h2 example.com" to an HTTP/2 request for
// example.com and 400 to anything else, so a test can check both from the
// body.
func AnswerH2ExampleCom(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor != 2 || r.Host != "example.com" {
		http.Error(w, "not an HTTP/2 request for example.com", http.StatusBadRequest)
		return
	}
	_, _ = io.WriteString(w, "h2 example.com")
}
