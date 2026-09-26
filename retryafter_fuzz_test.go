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
	"bytes"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// retryAfterNow is the instant FuzzRetryAfter measures an HTTP date against,
// 2026-01-01T00:00:00Z, the Rust SDK's retry_after target's, so a run is
// repeatable. It is a whole second, so an HTTP date without a fraction is a
// whole number of seconds away from it.
var retryAfterNow = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// retryAfterInput splits a FuzzRetryAfter input into the headers it sends,
// as the Rust SDK's retry_after target does, so that target's corpus is this
// one's: the first byte 'M' puts the rest in Retry-After-Ms alone; 'B' puts
// the rest up to its first line feed in Retry-After-Ms and what follows it,
// if anything, in Retry-After; 'R' puts the rest, and any other first byte
// the whole input, in Retry-After alone.
func retryAfterInput(data []byte) (ms, secs []byte, hasMs, hasSecs bool) {
	if len(data) == 0 {
		return nil, data, false, true
	}
	rest := data[1:]
	switch data[0] {
	case 'M':
		return rest, nil, true, false
	case 'B':
		if before, after, found := bytes.Cut(rest, []byte{'\n'}); found {
			return before, after, true, true
		}
		return rest, nil, true, false
	case 'R':
		return nil, rest, false, true
	default:
		return nil, data, false, true
	}
}

// fieldValue reports whether v can be the value of a header field a client
// receives: no control byte but a tab, and no DEL (RFC 9110 section 5.5).
// net/http refuses a response that carries any other.
func fieldValue(v []byte) bool {
	for _, c := range v {
		if (c < ' ' && c != '\t') || c == 0x7f {
			return false
		}
	}
	return true
}

// waitHeaders builds the headers of one FuzzRetryAfter case; a value is
// sent when its has flag is set, empty or not.
func waitHeaders(ms, secs string, hasMs, hasSecs bool) http.Header {
	h := make(http.Header, 2)
	if hasMs {
		h.Set(retryAfterMsHeader, ms)
	}
	if hasSecs {
		h.Set(retryAfterHeader, secs)
	}
	return h
}

// waitResult is one answer of retryAfter.
type waitResult struct {
	d  time.Duration
	ok bool
}

func waitOf(h http.Header, now time.Time) waitResult {
	d, ok := retryAfter(h, now)
	return waitResult{d, ok}
}

// FuzzRetryAfter checks the Retry-After-Ms and Retry-After parser on any
// header values (the input layout is retryAfterInput's; a value no response
// can carry is skipped). It never panics and answers within the per-input
// bound, and:
//
//   - the same headers at the same instant give the same answer twice;
//   - a wait is never negative and is whole milliseconds, or the largest
//     Duration a count of milliseconds saturates at; an answer without a
//     wait has a zero duration;
//   - Retry-After-Ms decides alone whenever it gives a wait, and when it
//     gives none the answer is Retry-After's alone (the Python SDK's
//     parse_retry_after order), checked against the input's other header or,
//     when it has one header, against a fixed partner;
//   - spaces and tabs around a value change nothing;
//   - the answer depends on the clock only through an HTTP date: at an
//     instant a second earlier it is the same, a second longer (a date
//     ahead), or at most a second when it was 0 (a date less than a second
//     behind), and ok never changes;
//   - the units: a value Retry-After-Ms reads as m whole milliseconds (m up
//     to 10^9, so that 1000(m+1) milliseconds stays below the largest
//     Duration) reads under Retry-After as 1000m to 1000(m+1) milliseconds.
//
// Its seed corpus is testdata/fuzz/FuzzRetryAfter, the Rust SDK's
// fuzz/corpus/retry_after byte for byte, and the rows below.
// Its seed corpus runs as a test in CI's -race test step (go test -race
// with coverage) on ubuntu-26.04, xcode-27 and windows-2025, and the
// fuzz job fuzzes it for 60 s on ubuntu-26.04.
func FuzzRetryAfter(f *testing.F) {
	for _, seed := range []string{
		"", "R", "Rbad", "R-1", "R1.5", "R1_000", "R1__0", "R_1", "R1e", "R.", "R.5", "R5.", "R+inf", "R-Infinity", "RnAn",
		"R1e308", "R1e309", "R-0", "R-0.0", "R\t 7 \t", "R\xc2\xa042", "R1, 2", "M1e-400", "M1_0.5_0", "M9.3e15", "M1e16",
		"BNaN\n1.5", "B-1\n2", "Bbad\n2", "B\n", "B5\n", "Binf\nThu, 01 Jan 2026 00:00:10 GMT",
		"RThu, 01 Jan 2026 00:00:10.5 GMT", "RWed, 31 Dec 2025 23:59:59.5 GMT", "RFri, 31 Dec 9999 23:59:59 GMT",
		"RMon, 01 Jan 0001 00:00:00 GMT", "RThursday, 01-Jan-26 00:00:10 GMT", "RThu Jan  1 00:00:10 2026",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		defer testsupport.BoundFuzzInput(t)()
		msb, secsb, hasMs, hasSecs := retryAfterInput(data)
		if !fieldValue(msb) || !fieldValue(secsb) {
			return // no response carries such a header
		}
		ms, secs := string(msb), string(secsb)
		h := waitHeaders(ms, secs, hasMs, hasSecs)
		got := waitOf(h, retryAfterNow)

		if again := waitOf(h, retryAfterNow); again != got {
			t.Fatalf("retryAfter(%q) = %+v, then %+v", h, got, again)
		}
		switch {
		case !got.ok && got.d != 0:
			t.Fatalf("retryAfter(%q) = %+v: a duration without a wait", h, got)
		case got.ok && got.d < 0:
			t.Fatalf("retryAfter(%q) = %+v: a negative wait", h, got)
		case got.ok && got.d%time.Millisecond != 0 && got.d != math.MaxInt64:
			t.Fatalf("retryAfter(%q) = %+v: not whole milliseconds", h, got)
		}

		var msAlone, secsAlone waitResult
		if hasMs {
			msAlone = waitOf(waitHeaders(ms, "", true, false), retryAfterNow)
		}
		if hasSecs {
			secsAlone = waitOf(waitHeaders("", secs, false, true), retryAfterNow)
		}
		// The order: the millisecond header first, the other only when it
		// gives no wait. A single header is paired with a fixed partner.
		const partnerMs, partnerSecs = "250", "7"
		switch {
		case hasMs && hasSecs:
			want := secsAlone
			if msAlone.ok {
				want = msAlone
			}
			if got != want {
				t.Fatalf("retryAfter(%q) = %+v; Retry-After-Ms alone gives %+v and Retry-After alone %+v", h, got, msAlone, secsAlone)
			}
		case hasMs:
			want := waitResult{7 * time.Second, true}
			if msAlone.ok {
				want = msAlone
			}
			paired := waitHeaders(ms, partnerSecs, true, true)
			if w := waitOf(paired, retryAfterNow); w != want {
				t.Fatalf("retryAfter(%q) = %+v, want %+v", paired, w, want)
			}
		case hasSecs:
			paired := waitHeaders(partnerMs, secs, true, true)
			if w := waitOf(paired, retryAfterNow); w != (waitResult{250 * time.Millisecond, true}) {
				t.Fatalf("retryAfter(%q) = %+v, want Retry-After-Ms's 250ms", paired, w)
			}
		}

		padded := waitHeaders(" \t"+ms+"\t ", "\t "+secs+" \t", hasMs, hasSecs)
		if w := waitOf(padded, retryAfterNow); w != got {
			t.Fatalf("retryAfter(%q) = %+v, but padded %q = %+v", h, got, padded, w)
		}

		earlier := waitOf(h, retryAfterNow.Add(-time.Second))
		const unsaturated = time.Duration(math.MaxInt64) - 2*time.Second
		switch {
		case earlier.ok != got.ok:
			t.Fatalf("retryAfter(%q) = %+v, but a second earlier %+v", h, got, earlier)
		case got.d > unsaturated || earlier.d > unsaturated:
			// A date past the largest Duration: both saturate.
		case earlier.d == got.d, earlier.d == got.d+time.Second:
		case got.d == 0 && earlier.d <= time.Second:
		default:
			t.Fatalf("retryAfter(%q) = %+v, but a second earlier %+v", h, got, earlier)
		}

		if hasMs && msAlone.ok && msAlone.d <= 1e9*time.Millisecond {
			m := msAlone.d / time.Millisecond
			asSecs := waitOf(waitHeaders("", ms, false, true), retryAfterNow)
			if !asSecs.ok || asSecs.d < 1000*m*time.Millisecond || asSecs.d > 1000*(m+1)*time.Millisecond {
				t.Fatalf("%q reads as %v under Retry-After-Ms but %+v under Retry-After", ms, msAlone.d, asSecs)
			}
		}
	})
}
