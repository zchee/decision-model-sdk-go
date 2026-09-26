//go:build !go1.28 && (amd64 || arm64)

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

package codec

import (
	"bytes"
	"maps"
	"slices"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// liveBodies names the reader of each body the live pass recorded under
// testdata/live (livetests, -record): a models body, a System One body, or
// an error body, which the SDK reads with ReadErrorBody and never cuts. A
// recording added without a row here fails TestLiveBodiesOneScan.
var liveBodies = map[string]string{
	"live/models.json":          "models",
	"live/questions.json":       "system one",
	"live/typed-response.json":  "system one",
	"live/unauthenticated.json": "error",
	"live/wrong-key.json":       "error",
}

// lastMemberEnd returns the last byte before the root object's closing
// brace, blanks skipped: the end of the root's last member's value, or the
// opening brace of an empty object. It is read from the bytes alone, apart
// from cutPoint, so the test below checks cutPoint's verdict against it.
func lastMemberEnd(tb testing.TB, body []byte) byte {
	tb.Helper()
	b := bytes.TrimRight(body, " \t\r\n")
	if len(b) < 2 || b[len(b)-1] != '}' {
		tb.Fatalf("the body does not end with the root object's closing brace")
	}
	b = bytes.TrimRight(b[:len(b)-1], " \t\r\n")
	return b[len(b)-1]
}

// TestLiveBodiesOneScan decodes every body of the live pass (critic-p5 C4)
// with the one-scan traversal of K36 and with the whole-body one it
// replaced: both give the same outcome, and the one-scan decode reads the
// body once exactly when the root object's last member ends in '}', ']' or
// '"' (a number or a literal there costs a second traversal). Each body's
// ONESCAN line and the share are ledger rows; the verdict is asserted only
// against the bytes, so the share itself is recorded, not gated. The error
// bodies go through ReadErrorBody, which has no cut, and must name the
// authentication failure. Three synthetic controls, whose last member is a
// number or a literal, must take the second traversal.
func TestLiveBodiesOneScan(t *testing.T) {
	names := testsupport.FixtureNames(t, "live/*.json")
	if diff := gocmp.Diff(slices.Sorted(maps.Keys(liveBodies)), names); diff != "" {
		t.Fatalf("testdata/live and liveBodies differ (-liveBodies +disk):\n%s", diff)
	}
	decoded, onceCount := 0, 0
	for _, name := range names {
		body := testsupport.Fixture(t, name)
		switch kind := liveBodies[name]; kind {
		case "error":
			eb := ReadErrorBody(body)
			if eb.NoBody || eb.ErrorType != "authentication_error" || eb.Message == "" {
				t.Errorf("%s: ReadErrorBody = %+v, want an authentication_error with a message", name, eb)
			}
			t.Logf("ONESCAN %-26s bytes=%-4d reader=ReadErrorBody (no cut) error_type=%s", name, len(body), eb.ErrorType)
		default:
			one, whole, once := decodeBothScans(t, body, kind == "models")
			if one.Err != "" {
				t.Errorf("%s: the %s decode fails: %s at %q", name, kind, one.Err, one.Path)
			}
			if diff := gocmp.Diff(whole, one); diff != "" {
				t.Errorf("%s: one scan and whole body disagree (-whole +one):\n%s", name, diff)
			}
			end := lastMemberEnd(t, body)
			if want := end == '}' || end == ']' || end == '"'; once != want {
				t.Errorf("%s: read once = %t, want %t: the root's last member ends in %q", name, once, want, end)
			}
			decoded++
			if once {
				onceCount++
			}
			t.Logf("ONESCAN %-26s bytes=%-4d reader=%s last_member_end=%q one_scan=%t", name, len(body), kind, end, once)
		}
	}
	t.Logf("ONESCAN share: %d of %d decoded live bodies took one scan", onceCount, decoded)

	// Controls: bodies whose last member is a number or a literal take the
	// second traversal, so the check above can tell the two apart even when
	// every live body reads once.
	for _, body := range []string{
		`{"model":"m","answers":{},"usage":{"input_tokens":1},"n":1}`,
		`{"model":"m","answers":{},"usage":{},"flag":true}`,
		`{"models":[],"n":null}`,
	} {
		if _, _, once := decodeBothScans(t, []byte(body), strings.HasPrefix(body, `{"models"`)); once {
			t.Errorf("control %s was read once, want a second traversal", body)
		}
	}
}
