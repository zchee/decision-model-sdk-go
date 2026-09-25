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
	"errors"
	"io"
	"math"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// probeQuestions is the question set of the Python probe,
// {"q":{"type":"noul","instructions":"?"}}.
const probeQuestions = `{"q":{"type":"noul","instructions":"?"}}`

// prepared prepares qs, failing the test when Prepare fails.
func prepared(t *testing.T, qs *Questions) *Prepared {
	t.Helper()
	p, err := qs.Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return p
}

// probeSet returns the probe's question set, prepared.
func probeSet(t *testing.T) *Prepared {
	t.Helper()
	return prepared(t, NewQuestions().Noul("q", Noul{Instructions: Text("?")}))
}

// bodyOf encodes a request body and returns a copy of its bytes, releasing
// the body. On failure it checks that no body was handed out.
func bodyOf(t *testing.T, state any, model string, qs *Prepared, extra ...bodyMember) (string, error) {
	t.Helper()
	body, err := encodeBody(state, model, qs, extra)
	if err != nil {
		if body != (codec.Body{}) {
			t.Errorf("encodeBody returned a body with its error %v", err)
		}
		return "", err
	}
	defer body.Release()
	return string(body.Bytes()), nil
}

// mustBody is bodyOf for a body that must encode.
func mustBody(t *testing.T, state any, model string, qs *Prepared, extra ...bodyMember) string {
	t.Helper()
	got, err := bodyOf(t, state, model, qs, extra...)
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	return got
}

// invalidRequest checks that err is an *InvalidRequestError whose message
// contains every string of want, and returns it.
func invalidRequest(t *testing.T, err error, want ...string) *InvalidRequestError {
	t.Helper()
	ire, ok := errors.AsType[*InvalidRequestError](err)
	if !ok {
		t.Fatalf("err = %T %v, want an *InvalidRequestError", err, err)
	}
	for _, w := range want {
		if !strings.Contains(ire.Error(), w) {
			t.Errorf("message %q does not contain %q", ire.Error(), w)
		}
	}
	return ire
}

// ticketMeta, ticketItem and ticketState are the probe's nested dictionary
// as Go structs: members in declaration order, as a Python dict keeps them in
// insertion order.
type ticketMeta struct {
	Source string  `json:"source"`
	Rank   int     `json:"rank"`
	Note   *string `json:"note"`
}

type ticketItem struct {
	ID   int        `json:"id"`
	Text string     `json:"text"`
	Tags []string   `json:"tags"`
	OK   bool       `json:"ok"`
	Meta ticketMeta `json:"meta"`
}

type ticketState struct {
	Name  string       `json:"name"`
	Items []ticketItem `json:"items"`
}

// TestBodyBytesMatchPython pins request bodies byte for byte against what
// typesafe-sdk-python 0.7.1 sends for the same call: want is
// prepare_system_one(...).content from the upstream venv (pydantic-core
// 2.46.5; probe _spikes/w1.2/body_probe.py, run of 2026-09-25 21:50:14 JST in
// _spikes/w1.2/results/body_probe-M.txt). A map state has one member per
// level, because a Go map with more is sent in Go's iteration order (sonic
// does not sort keys); the struct case shows the nested order.
func TestBodyBytesMatchPython(t *testing.T) {
	tail := `,"model":"jev-latest","questions":` + probeQuestions + `}`
	tests := map[string]struct {
		state any
		model string
		qs    func(t *testing.T) *Prepared
		extra []bodyMember
		want  string
	}{
		"success: text state": {
			state: "I was charged twice. Please help.",
			want:  `{"state":"I was charged twice. Please help."` + tail,
		},
		"success: map state": {
			state: map[string]any{"message": "I was charged twice."},
			want:  `{"state":{"message":"I was charged twice."}` + tail,
		},
		"success: nested state as structs": {
			state: &ticketState{Name: "ticket", Items: []ticketItem{{ID: 1, Text: "hi", Tags: []string{"a", "b"}, OK: true, Meta: ticketMeta{Source: "web", Rank: 2}}}},
			want:  `{"state":{"name":"ticket","items":[{"id":1,"text":"hi","tags":["a","b"],"ok":true,"meta":{"source":"web","rank":2,"note":null}}]}` + tail,
		},
		"success: array state": {
			state: []any{map[string]any{"message": "Classify"}, nil},
			want:  `{"state":[{"message":"Classify"},null]` + tail,
		},
		"success: RawJSON state": {
			state: RawJSON(`{"a":[1,2,{"b":null}],"c":"d"}`),
			want:  `{"state":{"a":[1,2,{"b":null}],"c":"d"}` + tail,
		},
		"success: typed question set": {
			state: "x",
			qs: func(t *testing.T) *Prepared {
				return prepared(t, NewQuestions().Noul("billing", Noul{Instructions: Text("Is this about billing?")}))
			},
			want: `{"state":"x","model":"jev-latest","questions":{"billing":{"type":"noul","instructions":"Is this about billing?"}}}`,
		},
		"success: extra body replaces the model and appends members": {
			state: "hi",
			model: "call-model",
			extra: []bodyMember{{"model", "override-model"}, {"beam_width", 4}, {"nullable", nil}},
			want:  `{"state":"hi","model":"override-model","questions":` + probeQuestions + `,"beam_width":4,"nullable":null}`,
		},
		"success: extra body replaces the state and the questions in place": {
			state: "ignored",
			extra: []bodyMember{
				{"questions", map[string]any{"x": map[string]any{"type": "noul"}}},
				{"state", map[string]any{"s": 1}},
				{"z", []any{1}},
			},
			want: `{"state":{"s":1},"model":"jev-latest","questions":{"x":{"type":"noul"}},"z":[1]}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			qs := probeSet(t)
			if tt.qs != nil {
				qs = tt.qs(t)
			}
			model := tt.model
			if model == "" {
				model = "jev-latest"
			}
			got := mustBody(t, tt.state, model, qs, tt.extra...)
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("body differs from Python's (-python +go):\n%s", diff)
			}
		})
	}
}

// TestBodyDeviationsFromPython pins the two ruled differences between the
// state bytes sonic writes and Python's, with Python's bytes from the same
// probe (2026-09-25 21:50:14 JST) next to them; sonic's own bytes for the
// values are in _spikes/w1.2/results/sonic-*-M.txt (arm64) and
// sonic-*-L.txt (amd64). A sonic upgrade that changes either spelling fails
// here.
//
//   - R47: sonic escapes U+0008 and U+000C as \u0008 and \u000c where Python
//     writes \b and \f; the other 30 control characters, DEL, the HTML
//     characters, U+2028, U+2029 and non-ASCII text are byte-equal, and both
//     strings decode to the same value.
//   - R46: sonic spells floats as encoding/json does, except for the sign of
//     a negative zero on arm64: integral floats without ".0", -0.0 as 0 on
//     arm64 and as -0 on amd64 (K27), fixed digits for 1e-6 <= |x| < 1e-5
//     and for 1e16 <= |x| < 1e21. Every number reads back as the same
//     float64, except that -0.0 loses its sign on arm64. R59 extends this to
//     extra-body values, which take the same sonic path (the extra cases are
//     from the probe run of 2026-09-25 22:53:56 JST).
//
// K27: on amd64 sonic's JIT encoder calls its native float writer, which
// writes the sign of a negative zero; on every other GOARCH sonic's VM
// encoder writes any zero as 0 before reaching it. The cases whose bytes
// hold a negative zero are therefore pinned per GOARCH (wantByArch), and a
// GOARCH without a pin skips them. That skip is not reached today: codec's
// build line (D1) refuses every GOARCH but amd64 and arm64.
func TestBodyDeviationsFromPython(t *testing.T) {
	tail := `,"model":"jev-latest","questions":` + probeQuestions + `}`
	var ctl strings.Builder
	for c := range 0x20 {
		ctl.WriteByte(byte(c))
	}
	text := ctl.String() + "\x7f<>&\"\\/é\u2028\u2029\U0001F600"
	const escapedTail = "\x7f<>&\\\"\\\\/é\u2028\u2029\U0001F600"
	floats := []any{
		1e-5, 9.99e-6, 1e-4, 0.1, 1.5, 3.0, -7.0, 9999999999999998.0, 1e16, 1e20, 1e21, 1e22,
		5e-324, 1.7976931348623157e308, math.Copysign(0, -1), 0.0, float64(1<<53 + 1), 12345678901234567168.0,
	}

	tests := map[string]struct {
		state      any
		extra      []bodyMember
		python     string            // what the Python SDK sends
		want       string            // what sonic sends on every GOARCH
		wantByArch map[string]string // what sonic sends, by GOARCH, where it differs (K27)
		check      func(t *testing.T, python, got string)
	}{
		"deviation: control character escapes": {
			state: text,
			python: `{"state":"\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\b\t\n\u000b\f\r\u000e\u000f` +
				`\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001a\u001b\u001c\u001d\u001e\u001f` + escapedTail + `"` + tail,
			want: `{"state":"\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\t\n\u000b\u000c\r\u000e\u000f` +
				`\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001a\u001b\u001c\u001d\u001e\u001f` + escapedTail + `"` + tail,
			check: func(t *testing.T, python, got string) {
				// A JSON string without \/ is a Go string literal, so
				// strconv.Unquote decodes both.
				decode := func(body string) string {
					quoted, okPrefix := strings.CutPrefix(body, `{"state":`)
					quoted, okSuffix := strings.CutSuffix(quoted, tail)
					if !okPrefix || !okSuffix {
						t.Fatalf("body %q does not have the probe's layout", body)
					}
					s, err := strconv.Unquote(quoted)
					if err != nil {
						t.Fatalf("decoding %s: %v", quoted, err)
					}
					return s
				}
				if diff := gocmp.Diff(decode(python), decode(got)); diff != "" {
					t.Errorf("decoded states differ (-python +go):\n%s", diff)
				}
				if diff := gocmp.Diff(text, decode(got)); diff != "" {
					t.Errorf("decoded state differs from the text sent (-want +got):\n%s", diff)
				}
			},
		},
		"deviation: state float spelling": {
			state: floats,
			python: `{"state":[0.00001,9.99e-6,0.0001,0.1,1.5,3.0,-7.0,9999999999999998.0,1e+16,1e+20,1e+21,1e+22,` +
				`5e-324,1.7976931348623157e+308,-0.0,0.0,9007199254740992.0,1.2345678901234567e+19]` + tail,
			wantByArch: map[string]string{
				"arm64": `{"state":[0.00001,0.00000999,0.0001,0.1,1.5,3,-7,9999999999999998,10000000000000000,100000000000000000000,` +
					`1e+21,1e+22,5e-324,1.7976931348623157e+308,0,0,9007199254740992,12345678901234567000]` + tail,
				"amd64": `{"state":[0.00001,0.00000999,0.0001,0.1,1.5,3,-7,9999999999999998,10000000000000000,100000000000000000000,` +
					`1e+21,1e+22,5e-324,1.7976931348623157e+308,-0,0,9007199254740992,12345678901234567000]` + tail,
			},
			check: sameNumbers,
		},
		"deviation: extra-value float spelling (R59)": {
			state:  "hi",
			extra:  []bodyMember{{"cfg", []any{0.5, 3.0, 1e16, math.Copysign(0, -1), 1e-6, 1e21}}},
			python: `{"state":"hi","model":"jev-latest","questions":` + probeQuestions + `,"cfg":[0.5,3.0,1e+16,-0.0,1e-6,1e+21]}`,
			wantByArch: map[string]string{
				"arm64": `{"state":"hi","model":"jev-latest","questions":` + probeQuestions + `,"cfg":[0.5,3,10000000000000000,0,0.000001,1e+21]}`,
				"amd64": `{"state":"hi","model":"jev-latest","questions":` + probeQuestions + `,"cfg":[0.5,3,10000000000000000,-0,0.000001,1e+21]}`,
			},
			check: sameNumbers,
		},
		"deviation: nested extra-value float spelling (R59)": {
			state:  "hi",
			extra:  []bodyMember{{"cfg", map[string]any{"t": map[string]any{"x": 3.0}}}},
			python: `{"state":"hi","model":"jev-latest","questions":` + probeQuestions + `,"cfg":{"t":{"x":3.0}}}`,
			want:   `{"state":"hi","model":"jev-latest","questions":` + probeQuestions + `,"cfg":{"t":{"x":3}}}`,
			check:  sameNumbers,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			want := tt.want
			if tt.wantByArch != nil {
				w, ok := tt.wantByArch[runtime.GOARCH]
				if !ok {
					t.Skipf("sonic's bytes for this case are pinned for arm64 and amd64 only, which spell -0.0 differently (K27); GOARCH %s has no pin", runtime.GOARCH)
				}
				want = w
			}
			got := mustBody(t, tt.state, "jev-latest", probeSet(t), tt.extra...)
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("sonic's bytes changed on %s (-pinned +go):\n%s", runtime.GOARCH, diff)
			}
			if tt.python == want {
				t.Errorf("Python's bytes equal sonic's: the deviation is gone, move the case to TestBodyBytesMatchPython")
			}
			tt.check(t, tt.python, got)
		})
	}
}

// TestExtraBodyShallowOverride ports test_extra_body_shallow_override (C2):
// extra members are merged over the body as Python's dict.update does. A
// member named state, model or questions replaces that member's value where
// it stands, other members follow in the order they were first given, and a
// repeated member keeps its first position and its last value. The body's
// bytes here are re-asserted through the client in W2.3.
func TestExtraBodyShallowOverride(t *testing.T) {
	const questions = `"questions":` + probeQuestions
	many := make([]bodyMember, 0, repeatScanLimit+10)
	var manyWant strings.Builder
	for i := range repeatScanLimit + 8 {
		key := "k" + strconv.Itoa(i)
		many = append(many, bodyMember{key, i})
		value := strconv.Itoa(i)
		if i == 5 {
			value = `"last"`
		}
		manyWant.WriteString(`,"` + key + `":` + value)
	}
	many = append(many, bodyMember{"k5", "last"}, bodyMember{"model", "m2"})

	tests := map[string]struct {
		state any
		model string
		extra []bodyMember
		want  string
	}{
		"success: the upstream test: model replaced, beam_width and nullable appended": {
			state: "hi",
			model: "call-model",
			extra: []bodyMember{{"model", "override-model"}, {"beam_width", 4}, {"nullable", nil}},
			want:  `{"state":"hi","model":"override-model",` + questions + `,"beam_width":4,"nullable":null}`,
		},
		"success: a replaced state is not encoded at all": {
			state: make(chan int), // would fail if it were encoded
			extra: []bodyMember{{"state", "replacement"}},
			want:  `{"state":"replacement","model":"jev-latest",` + questions + `}`,
		},
		"success: a replaced model is not checked": {
			state: "hi",
			model: "\xff",
			extra: []bodyMember{{"model", "m"}},
			want:  `{"state":"hi","model":"m",` + questions + `}`,
		},
		"success: replaced questions": {
			state: "hi",
			extra: []bodyMember{{"questions", RawJSON(`{"x":{"type":"noul"}}`)}},
			want:  `{"state":"hi","model":"jev-latest","questions":{"x":{"type":"noul"}}}`,
		},
		"success: a repeated member keeps its first position and its last value": {
			state: "hi",
			extra: []bodyMember{{"a", 1}, {"b", 2}, {"a", 3}},
			want:  `{"state":"hi","model":"jev-latest",` + questions + `,"a":3,"b":2}`,
		},
		"success: a repeated built-in member takes its last value": {
			state: "hi",
			extra: []bodyMember{{"model", "x"}, {"z", true}, {"model", "y"}},
			want:  `{"state":"hi","model":"y",` + questions + `,"z":true}`,
		},
		"success: a repeated state and questions take their last values": {
			state: "hi",
			extra: []bodyMember{
				{"state", "first"},
				{"questions", RawJSON(`{"a":{}}`)},
				{"state", []any{"second"}},
				{"questions", RawJSON(`{"b":{}}`)},
			},
			want: `{"state":["second"],"model":"jev-latest","questions":{"b":{}}}`,
		},
		"success: a long member list follows the same rules": {
			state: "hi",
			extra: many,
			want:  `{"state":"hi","model":"m2",` + questions + manyWant.String() + `}`,
		},
		"success: RawJSON and Content values": {
			state: "hi",
			extra: []bodyMember{{"raw", RawJSON(" 4 ")}, {"text", Text("t")}, {"json", JSON([]byte(`{ "a" : 1 }`))}, {"unset", Content{}}},
			want:  `{"state":"hi","model":"jev-latest",` + questions + `,"raw": 4 ,"text":"t","json":{"a":1},"unset":null}`,
		},
		"success: a replacing model may be any JSON value": {
			state: "hi",
			extra: []bodyMember{{"model", 4}},
			want:  `{"state":"hi","model":4,` + questions + `}`,
		},
		"deviation: a []byte nested in a member is sent as base64 (R56)": {
			state: "hi",
			extra: []bodyMember{{"blob", map[string]any{"b": []byte("hi")}}},
			want:  `{"state":"hi","model":"jev-latest",` + questions + `,"blob":{"b":"aGk="}}`,
		},
		"success: an empty member name": {
			state: "hi",
			extra: []bodyMember{{"", 1}},
			want:  `{"state":"hi","model":"jev-latest",` + questions + `,"":1}`,
		},
		"success: a member name that needs escaping": {
			state: "hi",
			extra: []bodyMember{{"a\"b\n", 1}},
			want:  `{"state":"hi","model":"jev-latest",` + questions + `,"a\"b\n":1}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			model := tt.model
			if model == "" {
				model = "jev-latest"
			}
			got := mustBody(t, tt.state, model, probeSet(t), tt.extra...)
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("error: a replacing state follows the state rules", func(t *testing.T) {
		_, err := bodyOf(t, "hi", "jev-latest", probeSet(t), bodyMember{"state", 3})
		ire := invalidRequest(t, err, `extra body member "state"`, "int encodes as a number")
		if !errors.Is(ire, codec.ErrStateShape) {
			t.Errorf("err = %v, want errors.Is codec.ErrStateShape", ire)
		}
	})
}

// blob is a named byte slice without a marshaler: sonic would send it as
// base64 (R59b).
type blob []byte

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// send does what a call does with a body, in its order: encode, then build
// the request over the body's readers and hand it to rt. An encoding failure
// returns before rt sees anything. W2.3's client re-asserts the same through
// SystemOne.
func send(t *testing.T, rt http.RoundTripper, state any, extra ...bodyMember) error {
	t.Helper()
	body, err := encodeBody(state, "jev-latest", probeSet(t), extra)
	if err != nil {
		return err
	}
	defer body.Release()
	rc, getBody, n, err := requestReaders(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example/v1/system-one", rc)
	if err != nil {
		t.Fatal(err)
	}
	req.GetBody, req.ContentLength = getBody, n
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// TestUnencodableBodyFailsBeforeNetwork ports
// test_unserializable_request_body_raises (C3): a body that cannot be encoded
// fails with *InvalidRequestError, whose message starts with Python's "The
// request body could not be encoded as JSON", and nothing reaches the
// network. Python's object() has no JSON form; a channel is Go's nearest
// counterpart. The rest are the Go values with no JSON form (Appendix B
// "NaN/Infinity written"; R48; R49).
func TestUnencodableBodyFailsBeforeNetwork(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	tests := map[string]struct {
		state   any
		extra   []bodyMember
		want    []string
		isCause error // errors.Is target, when the cause is a sentinel
		encode  bool  // the cause is sonic's (*codec.EncodeError)
	}{
		"error: the upstream test: an extra member with no JSON form": {
			state: "x", extra: []bodyMember{{"bad", make(chan int)}},
			want: []string{`extra body member "bad"`, "chan int"}, encode: true,
		},
		"error: a function": {
			state: "x", extra: []bodyMember{{"f", func() {}}},
			want: []string{`extra body member "f"`, "func()"}, encode: true,
		},
		"error: NaN in an extra member": {
			state: "x", extra: []bodyMember{{"n", math.NaN()}},
			want: []string{"NaN or ±Infinite"}, encode: true,
		},
		"error: an infinity in the state": {
			state: map[string]any{"t": math.Inf(1)},
			want:  []string{"state: json: unsupported value: NaN or ±Infinite"}, encode: true,
		},
		"error: a cyclic state": {
			state: cycle,
			want:  []string{"state:", "too deep"}, encode: true,
		},
		"error: invalid UTF-8 in the state (R48)": {
			state: map[string]any{"text": "caf\xe9"},
			want:  []string{"state: string is not valid UTF-8"}, isCause: wire.ErrInvalidUTF8,
		},
		"error: invalid UTF-8 in an extra member's value": {
			state: "x", extra: []bodyMember{{"v", "\xff"}},
			want: []string{`extra body member "v": string is not valid UTF-8`}, isCause: wire.ErrInvalidUTF8,
		},
		"error: invalid UTF-8 in an extra member's name": {
			state: "x", extra: []bodyMember{{"k\xff", 1}},
			want: []string{`extra body member "k\xff": string is not valid UTF-8`}, isCause: wire.ErrInvalidUTF8,
		},
		"error: an empty RawJSON extra member": {
			state: "x", extra: []bodyMember{{"r", RawJSON("  ")}},
			want: []string{`extra body member "r": raw JSON is empty, not a JSON value`}, isCause: codec.ErrRawValue,
		},
		"error: invalid JSON content in an extra member": {
			state: "x", extra: []bodyMember{{"c", JSON([]byte(`{"a":}`))}},
			want: []string{`extra body member "c": invalid JSON at byte 5`},
		},
		"error: a []byte state (R49)": {
			state:   []byte(`{"a":1}`),
			want:    []string{"state: a plain []byte is ambiguous; send string(b) for text or RawJSON(b) for JSON"},
			isCause: codec.ErrPlainBytes,
		},
		"error: a named byte-slice state (R59b)": {
			state:   blob(`{"a":1}`),
			want:    []string{"state: a plain []byte is ambiguous: typesafe.blob is a byte slice; send string(b) for text or RawJSON(b) for JSON"},
			isCause: codec.ErrPlainBytes,
		},
		"error: a named byte-slice extra member (R59b)": {
			state:   "x",
			extra:   []bodyMember{{"b", blob("hi")}},
			want:    []string{`extra body member "b": a plain []byte is ambiguous: typesafe.blob is a byte slice; send string(b)`},
			isCause: codec.ErrPlainBytes,
		},
		"error: a pointer to a []byte state (NIT C)": {
			state:   new([]byte(`{"a":1}`)),
			want:    []string{"state: a plain []byte is ambiguous: *[]uint8 points to a byte slice; send string(b) for text or RawJSON(b) for JSON"},
			isCause: codec.ErrPlainBytes,
		},
		"error: a pointer to a named byte slice extra member (NIT C)": {
			state:   "x",
			extra:   []bodyMember{{"p", new(blob("hi"))}},
			want:    []string{`extra body member "p": a plain []byte is ambiguous: *typesafe.blob points to a byte slice; send string(b)`},
			isCause: codec.ErrPlainBytes,
		},
		"error: a nil pointer to a byte slice extra member (NIT C)": {
			state:   "x",
			extra:   []bodyMember{{"p", (*blob)(nil)}},
			want:    []string{`extra body member "p": nil *typesafe.blob holds no value, not a JSON value`},
			isCause: codec.ErrRawValue,
		},
		"error: a []byte extra member (R56)": {
			state:   "x",
			extra:   []bodyMember{{"blob", []byte("hi")}},
			want:    []string{`extra body member "blob": a plain []byte is ambiguous; send string(b) for text or RawJSON(b) for JSON`},
			isCause: codec.ErrPlainBytes,
		},
		"error: a []byte replacing the model (R56)": {
			state:   "x",
			extra:   []bodyMember{{"model", []byte("m")}},
			want:    []string{`extra body member "model": a plain []byte is ambiguous`},
			isCause: codec.ErrPlainBytes,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			network := roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("an unencodable request body reached the network")
				return nil, errors.New("unreachable")
			})
			err := send(t, network, tt.state, tt.extra...)
			ire := invalidRequest(t, err, append([]string{"The request body could not be encoded as JSON: "}, tt.want...)...)
			if !strings.HasPrefix(ire.Error(), "The request body could not be encoded as JSON: ") {
				t.Errorf("message %q does not start with Python's sentence", ire.Error())
			}
			if tt.isCause != nil && !errors.Is(err, tt.isCause) {
				t.Errorf("err = %v, want errors.Is %v", err, tt.isCause)
			}
			if _, ok := errors.AsType[*codec.EncodeError](err); ok != tt.encode {
				t.Errorf("errors.As *codec.EncodeError = %t, want %t (err %v)", ok, tt.encode, err)
			}
		})
	}

	t.Run("success: an encodable body does reach the network", func(t *testing.T) {
		rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, []byte(`{}`))}}
		if err := send(t, rec, "x", bodyMember{"good", 1}); err != nil {
			t.Fatal(err)
		}
		if got := rec.Count(); got != 1 {
			t.Fatalf("round trips = %d, want 1", got)
		}
		want := `{"state":"x","model":"jev-latest","questions":` + probeQuestions + `,"good":1}`
		if diff := gocmp.Diff(want, string(rec.Requests()[0].Body)); diff != "" {
			t.Errorf("body sent (-want +got):\n%s", diff)
		}
	})
}

// TestScalarStatesRefused is the Go half of
// test_json_value_and_state_exclude_top_level_none (T2; Appendix B "`any`
// state"): Python's JSONContent type admits text, a mapping or a sequence and
// no top-level None; Go's state is any, so a state that encodes as null, a
// boolean or a number fails with *InvalidRequestError before any network,
// whatever its Go type, and text, objects and arrays pass.
func TestScalarStatesRefused(t *testing.T) {
	type celsius float64
	tests := map[string]struct {
		state any
		extra []bodyMember
		want  string // the state as sent, when it passes; else a substring of the message
		ok    bool
	}{
		"success: text":                     {state: "text", want: `"text"`, ok: true},
		"success: a map with a nested nil":  {state: map[string]any{"key": nil}, want: `{"key":null}`, ok: true},
		"success: a slice with a nil":       {state: []any{"item", nil}, want: `["item",null]`, ok: true},
		"success: RawJSON text":             {state: RawJSON(`"text"`), want: `"text"`, ok: true},
		"success: RawJSON object":           {state: RawJSON(` {}`), want: ` {}`, ok: true},
		"success: text Content":             {state: Text("text"), want: `"text"`, ok: true},
		"success: JSON Content, compacted":  {state: JSON([]byte(`[ 1, 2 ]`)), want: `[1,2]`, ok: true},
		"error: nil":                        {state: nil, want: "state: nil encodes as null, not a string, an array or an object"},
		"error: true":                       {state: true, want: "state: bool encodes as a boolean"},
		"error: int":                        {state: 0, want: "state: int encodes as a number"},
		"error: negative int64":             {state: int64(-1), want: "int64 encodes as a number"},
		"error: uint8":                      {state: uint8(7), want: "uint8 encodes as a number"},
		"error: float64":                    {state: 1.5, want: "float64 encodes as a number"},
		"error: a named float":              {state: celsius(20), want: "typesafe.celsius encodes as a number"},
		"error: a nil map":                  {state: map[string]any(nil), want: "map[string]interface {} encodes as null"},
		"error: a nil slice":                {state: []string(nil), want: "[]string encodes as null"},
		"error: a nil pointer":              {state: (*ticketState)(nil), want: "*typesafe.ticketState encodes as null"},
		"error: RawJSON number":             {state: RawJSON(`3`), want: "state: raw JSON starts with a number"},
		"error: RawJSON null":               {state: RawJSON("\tnull"), want: "raw JSON starts with null"},
		"error: empty RawJSON":              {state: RawJSON(nil), want: "state: raw JSON is empty"},
		"error: unset Content":              {state: Content{}, want: "state: unset Content encodes as null"},
		"error: a replacing state of false": {state: "text", extra: []bodyMember{{"state", false}}, want: `extra body member "state": bool encodes as a boolean`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := bodyOf(t, tt.state, "jev-latest", probeSet(t), tt.extra...)
			if tt.ok {
				if err != nil {
					t.Fatalf("encodeBody: %v", err)
				}
				want := `{"state":` + tt.want + `,"model":"jev-latest","questions":` + probeQuestions + `}`
				if diff := gocmp.Diff(want, got); diff != "" {
					t.Errorf("body (-want +got):\n%s", diff)
				}
				return
			}
			ire := invalidRequest(t, err, tt.want)
			if !errors.Is(ire, codec.ErrStateShape) {
				t.Errorf("err = %v, want errors.Is codec.ErrStateShape", ire)
			}
		})
	}
}

// TestNamedStringStateEncodesAsString is the evidence for T1's deviation
// (test_str_subclasses_fallback_to_strings; Appendix B "`any` state"): Go has
// no str subclasses, and a value of a named string type, the nearest Go
// counterpart, is sent as a plain JSON string wherever it appears.
func TestNamedStringStateEncodesAsString(t *testing.T) {
	type subject string
	type nested subject
	tests := map[string]struct {
		state any
		want  string
	}{
		"success: a named string type":            {state: subject("ARPANET"), want: `"ARPANET"`},
		"success: a type named after a named one": {state: nested("ARPANET"), want: `"ARPANET"`},
		"success: inside a map":                   {state: map[string]subject{"net": "ARPANET"}, want: `{"net":"ARPANET"}`},
		"success: inside a slice":                 {state: []any{subject("ARPANET")}, want: `["ARPANET"]`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := mustBody(t, tt.state, "jev-latest", probeSet(t))
			want := `{"state":` + tt.want + `,"model":"jev-latest","questions":` + probeQuestions + `}`
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
		})
	}
}

// TestMapSliceStructStates ports test_abstract_input_containers_encode (T6):
// Python accepts any Mapping or Sequence (a MappingProxyType holding a
// tuple); Go's counterparts are every map, slice, array and struct kind,
// which encode like map[string]any and []any. The full-body case is the
// upstream test's expected request, with its questions.
func TestMapSliceStructStates(t *testing.T) {
	type items struct {
		Items []any `json:"items"`
	}
	type embedded struct {
		items
	}
	a := "a"
	tests := map[string]struct {
		state any
		want  string
	}{
		"success: map[string]any with a slice":               {state: map[string]any{"items": []any{"a", nil}}, want: `{"items":["a",null]}`},
		"success: map[string][]any":                          {state: map[string][]any{"items": {"a", nil}}, want: `{"items":["a",null]}`},
		"success: a map holding an array":                    {state: map[string][2]any{"items": {"a", nil}}, want: `{"items":["a",null]}`},
		"success: a map holding pointers":                    {state: map[string][]*string{"items": {&a, nil}}, want: `{"items":["a",null]}`},
		"success: map[string]int":                            {state: map[string]int{"n": 1}, want: `{"n":1}`},
		"success: a struct":                                  {state: items{Items: []any{"a", nil}}, want: `{"items":["a",null]}`},
		"success: a pointer to a struct":                     {state: &items{Items: []any{"a", nil}}, want: `{"items":["a",null]}`},
		"success: an embedded struct's fields":               {state: embedded{items{Items: []any{"a", nil}}}, want: `{"items":["a",null]}`},
		"success: []any":                                     {state: []any{"a", nil}, want: `["a",null]`},
		"success: []string":                                  {state: []string{"a", "b"}, want: `["a","b"]`},
		"success: an array":                                  {state: [2]any{"a", nil}, want: `["a",null]`},
		"success: []int":                                     {state: []int{1, 2}, want: `[1,2]`},
		"success: a slice of maps":                           {state: []map[string]any{{"k": nil}}, want: `[{"k":null}]`},
		"success: map[string]string with one member":         {state: map[string]string{"k": "v"}, want: `{"k":"v"}`},
		"deviation: a nested []byte is sent as base64 (R49)": {state: map[string]any{"b": []byte("hi")}, want: `{"b":"aGk="}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := mustBody(t, tt.state, "jev-latest", probeSet(t))
			want := `{"state":` + tt.want + `,"model":"jev-latest","questions":` + probeQuestions + `}`
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("success: the upstream request", func(t *testing.T) {
		qs := prepared(t, NewQuestions().
			Choice("label", Choice{Instructions: JSON([]byte(`["read",{"ctx":null}]`)), Options: Options{{Label: "a"}, {"b", Text("x")}}}).
			Score("rating", Score{Levels: []Content{Text("low"), Text("high")}}).
			Raw("raw", RawQuestion{Type: "score", Fields: map[string]any{"criteria": []any{"bad", "good"}}}))
		got := mustBody(t, map[string]any{"items": []any{"a", nil}}, "jev-latest", qs)
		const want = `{"state":{"items":["a",null]},"model":"jev-latest","questions":{` +
			`"label":{"type":"choice","instructions":["read",{"ctx":null}],"criteria":{"a":null,"b":"x"}},` +
			`"rating":{"type":"score","criteria":["low","high"]},"raw":{"type":"score","criteria":["bad","good"]}}}`
		if diff := gocmp.Diff(want, got); diff != "" {
			t.Errorf("body (-want +got):\n%s", diff)
		}
	})
}

// TestEncodeBodyChecksConfigurationFirst covers the *ConfigError cases of the
// body entry point: a question set that Prepare did not return (review W1.1
// NIT 6, R45: Python's message for an empty set), and a model name that is
// not valid UTF-8. They are reported before any member is encoded.
func TestEncodeBodyChecksConfigurationFirst(t *testing.T) {
	noSet := func(*testing.T) *Prepared { return nil }
	tests := map[string]struct {
		qs    func(t *testing.T) *Prepared
		model string
		want  string
	}{
		"error: a nil question set": {qs: noSet, model: "jev-latest", want: "At least one question is required."},
		"error: a zero Prepared": {
			qs:    func(*testing.T) *Prepared { return &Prepared{} },
			model: "jev-latest",
			want:  "At least one question is required.",
		},
		"error: the question set is checked before the model": {qs: noSet, model: "jev\xff", want: "At least one question is required."},
		"error: a model that is not UTF-8":                    {qs: probeSet, model: "jev\xff", want: `Model "jev\xff" is not valid UTF-8.`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			// The state cannot be encoded: a configuration error must win.
			_, err := bodyOf(t, make(chan int), tt.model, tt.qs(t))
			ce, ok := errors.AsType[*ConfigError](err)
			if !ok {
				t.Fatalf("err = %T %v, want a *ConfigError", err, err)
			}
			if diff := gocmp.Diff(tt.want, ce.Error()); diff != "" {
				t.Errorf("message (-want +got):\n%s", diff)
			}
		})
	}
}

// TestEncodeBodyModel checks that the model is written as a JSON string with
// the question serialiser's escaping.
func TestEncodeBodyModel(t *testing.T) {
	got := mustBody(t, "x", "a\"b\\c\u0001é", probeSet(t))
	want := `{"state":"x","model":"a\"b\\c\u0001é","questions":` + probeQuestions + `}`
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("body (-want +got):\n%s", diff)
	}
}

// TestRequestReaders checks what a request carries to send a body: a reader
// for Body, GetBody for a replay or a retry, and ContentLength. Every reader
// reads the same bytes (PM4, by digest), each holds the body until it is
// closed, and GetBody fails once the body is gone.
func TestRequestReaders(t *testing.T) {
	body, err := encodeBody(map[string]any{"k": "v"}, "jev-latest", probeSet(t), []bodyMember{{"n", 1}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"state":{"k":"v"},"model":"jev-latest","questions":` + probeQuestions + `,"n":1}`
	rc, getBody, n, err := requestReaders(body)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(want)) {
		t.Errorf("ContentLength = %d, want %d", n, len(want))
	}
	first, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if diff := gocmp.Diff(want, string(first)); diff != "" {
		t.Errorf("Body (-want +got):\n%s", diff)
	}

	sum, sumN, err := testsupport.SumGetBody(getBody)
	if err != nil {
		t.Fatal(err)
	}
	if sumN != n {
		t.Errorf("GetBody read %d bytes, want ContentLength %d", sumN, n)
	}
	for attempt := 2; attempt <= 4; attempt++ {
		again, _, err := testsupport.SumGetBody(getBody)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if again != sum {
			t.Errorf("attempt %d sends %s, attempt 1 sent %s", attempt, again, sum)
		}
	}

	// The call's reference is dropped while the first reader is open: the
	// reader keeps the bytes, and GetBody still works until it is closed.
	body.Release()
	if _, _, err := testsupport.SumGetBody(getBody); err != nil {
		t.Errorf("GetBody while a reader is open: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	late, err := getBody()
	if !errors.Is(err, codec.ErrBodyReleased) {
		t.Errorf("GetBody after the last reader: err = %v, want %v", err, codec.ErrBodyReleased)
	}
	if late != nil {
		t.Errorf("GetBody after the last reader returned a non-nil reader %#v", late)
	}
	if _, _, _, err := requestReaders(body); !errors.Is(err, codec.ErrBodyReleased) {
		t.Errorf("requestReaders after release: err = %v, want %v", err, codec.ErrBodyReleased)
	}
}

// TestInvalidRequestError checks the error's message and its cause chain,
// down to sonic's error for a value with no JSON form.
func TestInvalidRequestError(t *testing.T) {
	cause := errors.New("cause")
	e := newInvalidRequestError("message", cause)
	if diff := gocmp.Diff("message", e.Error()); diff != "" {
		t.Errorf("Error (-want +got):\n%s", diff)
	}
	if !errors.Is(e, cause) || !errors.Is(error(e), cause) {
		t.Errorf("errors.Is(e, cause) = false")
	}
	if got := newInvalidRequestError("m", nil).Unwrap(); got != nil {
		t.Errorf("Unwrap without a cause = %v, want nil", got)
	}

	_, err := bodyOf(t, []any{math.Inf(-1)}, "jev-latest", probeSet(t))
	ee, ok := errors.AsType[*codec.EncodeError](err)
	if !ok {
		t.Fatalf("err = %T %v, want a chain through *codec.EncodeError", err, err)
	}
	if ee.Unwrap() == nil {
		t.Errorf("the *codec.EncodeError does not wrap sonic's error")
	}
}

// TestNestedContentEncodesAsContent checks rulings R52 and R60: Content
// nested in a state or in a body member's value, where sonic writes it
// through Content.MarshalJSON, is sent as content, never as the empty object
// its unexported fields would give: text escaped as the questions are (\b as
// Python writes it), JSON content checked by wire's scanner and compacted,
// unset Content as null. Invalid nested content fails before any network, in
// every build.
func TestNestedContentEncodesAsContent(t *testing.T) {
	type holder struct {
		C Content  `json:"c"`
		P *Content `json:"p"`
	}
	text := Text("a\bb")
	body := func(state, extra string) string {
		return `{"state":` + state + `,"model":"jev-latest","questions":` + probeQuestions + extra + `}`
	}
	tests := map[string]struct {
		state   any
		extra   []bodyMember
		want    []string // the accepted bodies, when it encodes (a Go map has no member order)
		wantErr string   // a substring of the *InvalidRequestError message, when it does not
		wantIs  error    // errors.Is target of a refusal, if any
		syntax  bool     // the refusal is wire's scanner's (*wire.SyntaxError)
	}{
		"success: text in a map state": {
			state: map[string]any{"c": text},
			want:  []string{body(`{"c":"a\bb"}`, "")},
		},
		"success: JSON content in a map state, compacted": {
			state: map[string]any{"c": JSON([]byte(` { "a" : [1, null] } `))},
			want:  []string{body(`{"c":{"a":[1,null]}}`, "")},
		},
		"success: unset Content in a map state is null": {
			state: map[string]any{"c": Content{}},
			want:  []string{body(`{"c":null}`, "")},
		},
		"success: Content and *Content struct fields": {
			state: holder{C: Text("x"), P: &text},
			want:  []string{body(`{"c":"x","p":"a\bb"}`, "")},
		},
		"success: a nil *Content field is null": {
			state: holder{C: JSON([]byte(`[]`))},
			want:  []string{body(`{"c":[],"p":null}`, "")},
		},
		"success: Content nested in an extra member's value": {
			state: "hi",
			extra: []bodyMember{{"ctx", map[string]any{"c": text, "u": Content{}}}},
			want: []string{
				body(`"hi"`, `,"ctx":{"c":"a\bb","u":null}`),
				body(`"hi"`, `,"ctx":{"u":null,"c":"a\bb"}`),
			},
		},
		// Under -race, sonic's own check of a MarshalJSON output accepts
		// a few complete but invalid outputs ({"a":}, [1 2], a trailing
		// comma: K26, _spikes/w1.2/validrace); wire's scanner refuses them
		// first, in every build (R60).
		"error: complete but invalid JSON content nested in the state": {
			state:   map[string]any{"c": JSON([]byte(`{"a":}`))},
			wantErr: `state: invalid JSON at byte 5: unexpected "}", want a value`,
			syntax:  true,
		},
		"error: JSON content with a trailing comma, nested in an extra member": {
			state:   "hi",
			extra:   []bodyMember{{"ctx", []any{JSON([]byte(`{"a":1,}`))}}},
			wantErr: `extra body member "ctx": invalid JSON at byte 7: unexpected "}", want a member name`,
			syntax:  true,
		},
		"error: truncated JSON content nested in the state": {
			state:   map[string]any{"c": JSON([]byte(`{"a":`))},
			wantErr: "state: invalid JSON at byte 5: unexpected end of input, want a value",
			syntax:  true,
		},
		"error: JSON content that is not an object or an array": {
			state:   []any{JSON([]byte(`3`))},
			wantErr: "state: JSON content must be an object or an array",
			wantIs:  wire.ErrContentShape,
		},
		"error: nested text that is not UTF-8, in an extra member": {
			state:   "hi",
			extra:   []bodyMember{{"ctx", []any{Text("\xff")}}},
			wantErr: `extra body member "ctx": string is not valid UTF-8`,
			wantIs:  wire.ErrInvalidUTF8,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := bodyOf(t, tt.state, "jev-latest", probeSet(t), tt.extra...)
			if tt.wantErr != "" {
				_ = invalidRequest(t, err, tt.wantErr)
				if _, ok := errors.AsType[*codec.EncodeError](err); !ok {
					t.Errorf("err = %v, want a chain through *codec.EncodeError", err)
				}
				if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
					t.Errorf("err = %v, want errors.Is %v", err, tt.wantIs)
				}
				if _, ok := errors.AsType[*wire.SyntaxError](err); ok != tt.syntax {
					t.Errorf("errors.As *wire.SyntaxError = %t, want %t (err %v)", ok, tt.syntax, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("encodeBody: %v", err)
			}
			if diff := gocmp.Diff(closest(tt.want, got), got); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
		})
	}
}

// closest returns the entry of want equal to got, or the first one when
// none is, so that gocmp can diff a body against the member orders a Go map
// may take.
func closest(want []string, got string) string {
	for _, w := range want {
		if w == got {
			return w
		}
	}
	return want[0]
}

// TestNestedRawJSONEncodesAsJSON checks rulings R57 and R60: RawJSON nested in
// a state or in a body member's value, where sonic writes it through
// RawJSON.MarshalJSON, is sent as the JSON it holds (checked by wire's
// scanner and compacted), never as the base64 string a plain byte slice
// would give; a nil one is null. A top-level *RawJSON takes the verbatim path
// of the RawJSON it points to, whitespace kept, and a nil one is refused.
func TestNestedRawJSONEncodesAsJSON(t *testing.T) {
	type holder struct {
		R RawJSON  `json:"r"`
		P *RawJSON `json:"p"`
	}
	raw := RawJSON(` {"a" : [1, null]} `)
	four := RawJSON(`4`)
	body := func(state, extra string) string {
		return `{"state":` + state + `,"model":"jev-latest","questions":` + probeQuestions + extra + `}`
	}
	tests := map[string]struct {
		state   any
		extra   []bodyMember
		want    string // the body, when it encodes
		wantErr string // a substring of the *InvalidRequestError message, when it does not
		wantIs  error  // errors.Is target of a refusal, if any
		sonic   bool   // the refusal comes through sonic (*codec.EncodeError)
		syntax  bool   // the refusal is wire's scanner's (*wire.SyntaxError)
	}{
		"success: RawJSON in a map state, compacted": {
			state: map[string]any{"r": raw},
			want:  body(`{"r":{"a":[1,null]}}`, ""),
		},
		"success: RawJSON as a struct field and through a pointer field": {
			state: holder{R: RawJSON(`[1,2]`), P: &four},
			want:  body(`{"r":[1,2],"p":4}`, ""),
		},
		"success: a nil RawJSON and a nil *RawJSON field are null": {
			state: holder{},
			want:  body(`{"r":null,"p":null}`, ""),
		},
		"success: RawJSON in a slice state": {
			state: []any{RawJSON(`"text"`), RawJSON(`{}`)},
			want:  body(`["text",{}]`, ""),
		},
		"success: a *RawJSON state takes the verbatim path": {
			state: &raw,
			want:  body(` {"a" : [1, null]} `, ""),
		},
		"success: RawJSON inside an extra member's value": {
			state: "hi",
			extra: []bodyMember{{"cfg", []any{RawJSON(`{"k":true}`), four}}},
			want:  body(`"hi"`, `,"cfg":[{"k":true},4]`),
		},
		"success: a *RawJSON extra member takes the verbatim path": {
			state: "hi",
			extra: []bodyMember{{"p", &four}},
			want:  body(`"hi"`, `,"p":4`),
		},
		"error: truncated RawJSON nested in the state": {
			state:   map[string]any{"r": RawJSON(`{"a":`)},
			wantErr: "state: invalid JSON at byte 5: unexpected end of input, want a value",
			sonic:   true,
			syntax:  true,
		},
		// The complete but invalid outputs sonic's own check lets through
		// under -race (K26): wire's scanner refuses them first (R60).
		"error: complete but invalid RawJSON nested in the state": {
			state:   holder{R: RawJSON(`[1 2]`)},
			wantErr: `state: invalid JSON at byte 3: unexpected "2", want ',' or a closing bracket`,
			sonic:   true,
			syntax:  true,
		},
		"error: RawJSON with a trailing comma, nested through a pointer": {
			state:   holder{P: new(RawJSON(`{"a":1,}`))},
			wantErr: `state: invalid JSON at byte 7: unexpected "}", want a member name`,
			sonic:   true,
			syntax:  true,
		},
		"error: RawJSON with trailing data nested in an extra member": {
			state:   "hi",
			extra:   []bodyMember{{"cfg", []any{RawJSON(`1 2`)}}},
			wantErr: `extra body member "cfg": invalid JSON at byte 2: unexpected "2" after the value`,
			sonic:   true,
			syntax:  true,
		},
		"error: nested RawJSON that is not UTF-8": {
			state:   map[string]any{"r": RawJSON("\"\xff\"")},
			wantErr: "state: invalid JSON at byte 1: invalid UTF-8 in a string",
			sonic:   true,
			syntax:  true,
		},
		"error: a nil *RawJSON state": {
			state:   (*RawJSON)(nil),
			wantErr: "state: nil *RawJSON holds no JSON value, not a string, an array or an object",
			wantIs:  codec.ErrStateShape,
		},
		"error: a nil *RawJSON extra member": {
			state:   "hi",
			extra:   []bodyMember{{"p", (*RawJSON)(nil)}},
			wantErr: `extra body member "p": nil *RawJSON holds no JSON value, not a JSON value`,
			wantIs:  codec.ErrRawValue,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := bodyOf(t, tt.state, "jev-latest", probeSet(t), tt.extra...)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("encodeBody: %v", err)
				}
				if diff := gocmp.Diff(tt.want, got); diff != "" {
					t.Errorf("body (-want +got):\n%s", diff)
				}
				return
			}
			_ = invalidRequest(t, err, tt.wantErr)
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("err = %v, want errors.Is %v", err, tt.wantIs)
			}
			if _, ok := errors.AsType[*codec.EncodeError](err); ok != tt.sonic {
				t.Errorf("errors.As *codec.EncodeError = %t, want %t (err %v)", ok, tt.sonic, err)
			}
			if _, ok := errors.AsType[*wire.SyntaxError](err); ok != tt.syntax {
				t.Errorf("errors.As *wire.SyntaxError = %t, want %t (err %v)", ok, tt.syntax, err)
			}
		})
	}
}

// TestAppendSafeText pins the escape and cut of text the SDK did not write
// (NF7, ruling R58), the rules of the Rust port's src/text.rs in Go escapes.
func TestAppendSafeText(t *testing.T) {
	tests := map[string]struct {
		s      string
		limit  int
		double bool
		want   string
	}{
		"success: printable text, non-ASCII and U+FFFD as they are": {s: "h\u00e9llo \u4e16\u754c \ufffd <>&\"'", limit: 200, want: "h\u00e9llo \u4e16\u754c \ufffd <>&\"'"},
		"success: line feed, carriage return and tab":               {s: "a\nb\rc\td", limit: 200, want: `a\nb\rc\td`},
		"success: other controls, DEL and C1 as Go escapes":         {s: "\x00\a\x1b\x7f\u0085\u009f", limit: 200, want: `\x00\a\x1b\x7f\u0085\u009f`},
		"success: bytes that are not UTF-8":                         {s: "a\xffb\xed\xa0\x80", limit: 200, want: `a\xffb\xed\xa0\x80`},
		"success: format characters that hide or reorder text": {
			s:     "\u00ad\u061c\u180e\u200b\u200f\u2028\u2029\u202e\u2060\u2066\ufeff\ufff9\U000e0041",
			limit: 200,
			want:  `\u00ad\u061c\u180e\u200b\u200f\u2028\u2029\u202e\u2060\u2066\ufeff\ufff9\U000e0041`,
		},
		"success: a sentence keeps its backslashes":   {s: `say \"hi\" \x1b`, limit: 200, want: `say \"hi\" \x1b`},
		"success: a name doubles its backslashes":     {s: `k\x1b`, limit: 200, double: true, want: `k\\x1b`},
		"success: exactly at the limit, no ellipsis":  {s: "abcde", limit: 5, want: "abcde"},
		"success: past the limit, cut with U+2026":    {s: "abcdef", limit: 5, want: "abcde\u2026"},
		"success: an escape is never split":           {s: "abcd\n", limit: 5, want: "abcd\u2026"},
		"success: an invalid byte's escape is whole":  {s: "ab\xff", limit: 5, want: "ab\u2026"},
		"success: a multi-byte character counts once": {s: "\u4e16\u754c\u4eba\u6c11", limit: 3, want: "\u4e16\u754c\u4eba\u2026"},
		"success: empty text":                         {s: "", limit: 5, want: ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := string(appendSafeText([]byte("<"), tt.s, tt.limit, tt.double))
			if diff := gocmp.Diff("<"+tt.want, got); diff != "" {
				t.Errorf("appendSafeText (-want +got):\n%s", diff)
			}
		})
	}
}

// rawMessage is json.RawMessage, which root tests cannot import (the seam
// keeps encoding/json out of the root package): a byte slice whose
// MarshalJSON returns it, so sonic validates it as it does a RawMessage.
type rawMessage []byte

func (m rawMessage) MarshalJSON() ([]byte, error) { return m, nil }

// failingMarshaler is a json.Marshaler that fails with its message.
type failingMarshaler struct{ msg string }

func (f failingMarshaler) MarshalJSON() ([]byte, error) { return nil, errors.New(f.msg) }

// TestEncodeErrorMessageIsBounded checks ruling R58: however large or
// unprintable the caller's data, an *InvalidRequestError's message stays
// short and printable, and carries no state for a json.Marshaler's invalid
// output, while the whole cause stays behind Unwrap.
func TestEncodeErrorMessageIsBounded(t *testing.T) {
	const secret = "SECRET"
	payload := strings.Repeat(secret, (1<<20)/len(secret)) // 1 MiB
	tests := map[string]struct {
		state    any
		extra    []bodyMember
		want     string // a substring of the message
		noSecret bool   // the message must not quote the state at all
		cut      bool   // the message ends with the cut's U+2026
		inCause  bool   // the payload is in the cause, which Unwrap keeps whole
	}{
		// Nested JSON Content and RawJSON fail in wire's scanner (R60),
		// whose message names a position and at most one byte of the input.
		"error: 1 MiB of truncated JSON content nested in the state": {
			state:    map[string]any{"c": JSON([]byte(`{"k":"` + payload + `"`))},
			want:     "state: invalid JSON at byte 1048579: unexpected end of input, want ',' or a closing bracket",
			noSecret: true,
		},
		"error: 1 MiB of JSON content with a trailing comma nested in the state": {
			state:    map[string]any{"c": JSON([]byte(`{"k":"` + payload + `",}`))},
			want:     `state: invalid JSON at byte 1048580: unexpected "}", want a member name`,
			noSecret: true,
		},
		"error: 1 MiB of truncated RawJSON nested in the state": {
			state:    []any{RawJSON(`["` + payload + `"`)},
			want:     "state: invalid JSON at byte 1048575: unexpected end of input, want ',' or a closing bracket",
			noSecret: true,
		},
		"error: 1 MiB of RawJSON with a trailing comma nested in the state": {
			state:    []any{RawJSON(`["` + payload + `",]`)},
			want:     `state: invalid JSON at byte 1048576: unexpected "]", want a value`,
			noSecret: true,
		},
		"error: a 1 MiB invalid RawMessage-like state": {
			state:    rawMessage(`{"k":"` + payload + `"`),
			want:     "state: a MarshalJSON method returned invalid JSON (syntax error at position ",
			noSecret: true,
			inCause:  true,
		},
		"error: a 1 MiB cause with control characters, escaped and cut": {
			state:   map[string]any{"m": failingMarshaler{msg: "bad\x1b[31m\n" + payload}},
			want:    `state: bad\x1b[31m\n` + secret,
			cut:     true,
			inCause: true,
		},
		"error: a 1 MiB member name with control characters, escaped and cut": {
			state: "x",
			extra: []bodyMember{{"k\n\x1b\u2028" + payload, make(chan int)}},
			want:  `extra body member "k\n\x1b\u2028` + secret,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := bodyOf(t, tt.state, "jev-latest", probeSet(t), tt.extra...)
			ire := invalidRequest(t, err, tt.want)
			msg := ire.Error()
			t.Logf("message (%d bytes): %s", len(msg), msg)
			if len(msg) >= 400 {
				t.Errorf("message is %d bytes, want fewer than 400", len(msg))
			}
			if !utf8.ValidString(msg) || strings.ContainsFunc(msg, func(r rune) bool { return r < 0x20 || r == 0x7f || hidesText(r) }) {
				t.Errorf("message holds an unprintable character: %q", msg)
			}
			if tt.noSecret && strings.Contains(msg, secret) {
				t.Errorf("message quotes the state")
			}
			if tt.cut && !strings.HasSuffix(msg, "\u2026") {
				t.Errorf("message does not end with the cut's U+2026")
			}
			ee, ok := errors.AsType[*codec.EncodeError](err)
			if !ok {
				t.Fatalf("err = %T, want a chain through *codec.EncodeError", err)
			}
			if cause := ee.Unwrap().Error(); tt.inCause && len(cause) < len(payload) {
				t.Errorf("the cause behind Unwrap is %d bytes, want the whole cause (at least %d)", len(cause), len(payload))
			}
		})
	}
}

// jsonNumber matches the numbers of a JSON body whose strings hold no digits.
var jsonNumber = regexp.MustCompile(`-?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?`)

// sameNumbers checks that the numbers of Python's body and Go's, in order,
// read back as the same float64, their spelling aside; -0.0 loses its sign
// on arm64 and keeps it on amd64 (R46, K27).
func sameNumbers(t *testing.T, python, got string) {
	t.Helper()
	py, gonums := jsonNumber.FindAllString(python, -1), jsonNumber.FindAllString(got, -1)
	if len(py) == 0 || len(py) != len(gonums) {
		t.Fatalf("Python's body has %d numbers %q, Go's %d %q", len(py), py, len(gonums), gonums)
	}
	for i := range py {
		p, err1 := strconv.ParseFloat(py[i], 64)
		g, err2 := strconv.ParseFloat(gonums[i], 64)
		if err1 != nil || err2 != nil {
			t.Fatalf("number %d: %v, %v", i, err1, err2)
		}
		if p != g { // -0.0 == 0 holds: only the sign differs
			t.Errorf("number %d: Python %s reads as %v, Go %s as %v", i, py[i], p, gonums[i], g)
		}
		if math.Signbit(p) != math.Signbit(g) && py[i] != "-0.0" {
			t.Errorf("number %d: sign differs: Python %s, Go %s", i, py[i], gonums[i])
		}
	}
}
