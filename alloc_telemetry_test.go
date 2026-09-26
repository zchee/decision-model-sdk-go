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

//go:build !race

package typesafe

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// discardHandler is a slog.Handler that keeps every record at or above min
// and does nothing with it, so a measurement sees the SDK's side of a
// record (building it and its attributes) and no handler's formatting.
type discardHandler struct{ min slog.Level }

func (h discardHandler) Enabled(_ context.Context, level slog.Level) bool { return level >= h.min }
func (discardHandler) Handle(context.Context, slog.Record) error          { return nil }
func (h discardHandler) WithAttrs([]slog.Attr) slog.Handler               { return h }
func (h discardHandler) WithGroup(string) slog.Handler                    { return h }

// TestAllocLoggedCall records what logging costs a call (section 9, ledger
// row W3.3-01): the whole q3 call of TestAllocWholeCall, under
// DefaultRetry, with the default logger (slog.DiscardHandler, which keeps
// nothing, so no record is built), with WithLogger at INFO and at DEBUG
// into a handler that keeps the records and discards them (the SDK's cost
// of one INFO record per attempt, and of the DEBUG records with their
// redacted headers), and at INFO into slog's text handler writing to
// io.Discard (a handler's formatting added). The "LOG q3" line is the
// ledger's row (W3.3-01). The "LOG q3+id" line measures the same call whose
// reply carries an x-typesafe-request-id header, as a real response does,
// so the INFO record's request id is read and redacted (ruling R107; ledger
// row W3.3-07); its default is not pinned, since the extra header costs
// bytes before any record is built. The default of q3 must cost what AC-P6
// measures, since no record is built.
func TestAllocLoggedCall(t *testing.T) {
	testsupport.QuietRuntime(t)
	ctx := t.Context()
	qs := q3Questions(t)
	body := testsupport.Fixture(t, "result.json")
	replies := []struct {
		name string
		kv   []string // the reply's headers past Content-Type, name then value
	}{
		{name: "q3"},
		{name: "q3+id", kv: []string{"X-Typesafe-Request-Id", "req_7f3c9a2e5b1d"}},
	}
	loggers := []struct {
		name   string
		logger *slog.Logger // nil: the default
	}{
		{name: "default"},
		{name: "info-discard", logger: slog.New(discardHandler{min: slog.LevelInfo})},
		{name: "debug-discard", logger: slog.New(discardHandler{min: slog.LevelDebug})},
		{name: "info-text", logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))},
	}
	for _, rp := range replies {
		var line strings.Builder
		line.WriteString("LOG " + rp.name)
		var def testsupport.Allocs
		for _, l := range loggers {
			reply := testsupport.JSON(http.StatusOK, body)
			for i := 0; i+1 < len(rp.kv); i += 2 {
				reply.Header.Add(rp.kv[i], rp.kv[i+1])
			}
			rec := &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{reply}}
			opts := []ClientOption{WithRetry(DefaultRetry())}
			if l.logger != nil {
				opts = append(opts, WithLogger(l.logger))
			}
			c := newTestClient(t, rec, opts...)
			state := newAllocState()
			for range 2 { // warm the pools, the encoder and the decoder
				if _, err := c.SystemOne(ctx, state, qs); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			total := series(t, "call/"+rp.name+"/"+l.name, nil, func() { sinkResponse, err = c.SystemOne(ctx, state, qs) })
			if err != nil {
				t.Fatalf("%s %s: %v", rp.name, l.name, err)
			}
			if l.logger == nil {
				def = total
			}
			line.WriteString(" " + l.name + "=" + total.String())
			if l.logger != nil {
				line.WriteString(" (+" + testsupport.Allocs{Mallocs: total.Mallocs - def.Mallocs, Bytes: total.Bytes - def.Bytes}.String() + ")")
			}
		}
		t.Log(line.String())
		if rp.name == "q3" && def != (testsupport.Allocs{Mallocs: 20, Bytes: 2648}) {
			t.Errorf("the call with the default logger = %s, want TestAllocWholeCall's 20/2648: a record is built that no handler keeps", def)
		}
	}
}

// sinkID keeps TestAllocRequestID's results alive.
var sinkID string

// TestAllocRequestID pins what reading a request id for the INFO
// "response" record costs (ruling R107, review R103REVERT MINOR 3). One
// value, with or without the client's API key in it, and no header cost
// no allocation. Several values cost what wire.ResponseMeta.RequestID
// costs, the reading the record made before R107: that joins them into one
// string, and requestID builds one string too, of "***" ones when a value
// holds the key. The header's name is not lower-cased, as isCredential's
// would be.
func TestAllocRequestID(t *testing.T) {
	const key = "ts_live_QzXjWvKpYbNmHgFd"
	r := newHeaderRedactor(key)
	tests := map[string]struct {
		values []string // the x-typesafe-request-id values, nil for none
		asMeta bool     // the count is wire.ResponseMeta.RequestID's, not 0
	}{
		"success: no request id":                      {},
		"success: an id without the key":              {values: []string{"req_123"}},
		"success: an id that holds the key":           {values: []string{"req " + key}},
		"success: a repeated id without the key":      {values: []string{"req_1", "req_2"}, asMeta: true},
		"success: a repeated id, one holding the key": {values: []string{"req_1", key, "req_3"}, asMeta: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := http.Header{"Content-Type": {"application/json"}, "Set-Cookie": {"a=1"}}
			if tt.values != nil {
				h["X-Typesafe-Request-Id"] = tt.values
			}
			var want float64
			if tt.asMeta {
				meta := wire.ResponseMeta{Header: h}
				want = testing.AllocsPerRun(100, func() { sinkID, _ = meta.RequestID() })
			}
			got := testing.AllocsPerRun(100, func() { sinkID, _ = r.requestID(h) })
			t.Logf("requestID allocates %v times, want %v (id %q)", got, want, sinkID)
			if got != want {
				t.Errorf("requestID allocates %v times, want %v", got, want)
			}
		})
	}
}
