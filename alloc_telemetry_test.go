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
// io.Discard (a handler's formatting added). The LOG line is the ledger's
// row. The default must cost what AC-P6 measures, since no record is built.
func TestAllocLoggedCall(t *testing.T) {
	testsupport.QuietRuntime(t)
	ctx := t.Context()
	qs := q3Questions(t)
	body := testsupport.Fixture(t, "result.json")
	loggers := []struct {
		name   string
		logger *slog.Logger // nil: the default
	}{
		{name: "default"},
		{name: "info-discard", logger: slog.New(discardHandler{min: slog.LevelInfo})},
		{name: "debug-discard", logger: slog.New(discardHandler{min: slog.LevelDebug})},
		{name: "info-text", logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))},
	}
	var line strings.Builder
	line.WriteString("LOG q3")
	var def testsupport.Allocs
	for _, l := range loggers {
		rec := &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, body)}}
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
		total := series(t, "call/"+l.name, nil, func() { sinkResponse, err = c.SystemOne(ctx, state, qs) })
		if err != nil {
			t.Fatalf("%s: %v", l.name, err)
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
	if def != (testsupport.Allocs{Mallocs: 22, Bytes: 2648}) {
		t.Errorf("the call with the default logger = %s, want TestAllocWholeCall's 22/2648: a record is built that no handler keeps", def)
	}
}
