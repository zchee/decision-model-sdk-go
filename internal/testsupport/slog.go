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

package testsupport

import (
	"context"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
)

// LogRecord is one record a [LogRecorder] kept. Its attributes are resolved
// (a LogValuer is replaced by its value) and flattened: an attribute inside
// group "g" has the key "g.key", with the handler's groups (from WithGroup)
// first.
type LogRecord struct {
	// Time is the record's time (zero when the logger set none).
	Time time.Time
	// Level is the record's level.
	Level slog.Level
	// Message is the record's message.
	Message string
	// Attrs are the handler's and the record's attributes, flattened.
	Attrs []slog.Attr
}

// Attr returns the value of the attribute with the given flattened key; the
// last one wins when a key repeats.
func (r LogRecord) Attr(key string) (slog.Value, bool) {
	for _, a := range slices.Backward(r.Attrs) {
		if a.Key == key {
			return a.Value, true
		}
	}
	return slog.Value{}, false
}

// String renders the record as "LEVEL message key=value ...", without the
// time, so a test can compare or search lines.
func (r LogRecord) String() string {
	var sb strings.Builder
	sb.WriteString(r.Level.String())
	sb.WriteByte(' ')
	sb.WriteString(r.Message)
	for _, a := range r.Attrs {
		sb.WriteByte(' ')
		sb.WriteString(a.Key)
		sb.WriteByte('=')
		sb.WriteString(a.Value.String())
	}
	return sb.String()
}

// LogRecorder keeps every record logged through its handler (and through
// handlers derived from it with WithAttrs and WithGroup). It is safe for
// concurrent use.
type LogRecorder struct {
	level slog.Leveler

	mu      sync.Mutex
	records []LogRecord
}

// NewLogRecorder returns a recorder that keeps the records at or above
// level; a nil level keeps every record, including the SDK's trace level
// below slog.LevelDebug.
func NewLogRecorder(level slog.Leveler) *LogRecorder {
	if level == nil {
		level = slog.Level(math.MinInt)
	}
	return &LogRecorder{level: level}
}

// Handler returns a [log/slog.Handler] that records into r.
func (r *LogRecorder) Handler() slog.Handler {
	return &recordingHandler{rec: r}
}

// Logger returns a logger over [LogRecorder.Handler].
func (r *LogRecorder) Logger() *slog.Logger {
	return slog.New(r.Handler())
}

// Records returns a copy of the records kept so far, in logging order.
func (r *LogRecorder) Records() []LogRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.records)
}

// At returns the records at exactly level, in logging order.
func (r *LogRecorder) At(level slog.Level) []LogRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []LogRecord
	for _, rec := range r.records {
		if rec.Level == level {
			out = append(out, rec)
		}
	}
	return out
}

// Reset drops every record kept so far.
func (r *LogRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = nil
}

// recordingHandler is the slog.Handler behind a LogRecorder.
type recordingHandler struct {
	rec    *LogRecorder
	prefix string      // the open groups, each followed by a dot
	attrs  []slog.Attr // flattened attributes from WithAttrs
}

// Enabled implements slog.Handler.
func (h *recordingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.rec.level.Level()
}

// Handle implements slog.Handler.
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := slices.Clone(h.attrs)
	r.Attrs(func(a slog.Attr) bool {
		attrs = appendFlat(attrs, h.prefix, a)
		return true
	})
	h.rec.mu.Lock()
	defer h.rec.mu.Unlock()
	h.rec.records = append(h.rec.records, LogRecord{Time: r.Time, Level: r.Level, Message: r.Message, Attrs: attrs})
	return nil
}

// WithAttrs implements slog.Handler.
func (h *recordingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	next := *h
	next.attrs = slices.Clone(h.attrs)
	for _, a := range as {
		next.attrs = appendFlat(next.attrs, h.prefix, a)
	}
	return &next
}

// WithGroup implements slog.Handler.
func (h *recordingHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := *h
	next.prefix = h.prefix + name + "."
	return &next
}

// appendFlat appends a to dst with its value resolved and its key prefixed,
// following the slog.Handler rules: an empty attribute is dropped, a group
// with an empty key is inlined, and an empty group is dropped.
func appendFlat(dst []slog.Attr, prefix string, a slog.Attr) []slog.Attr {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return dst
	}
	if a.Value.Kind() != slog.KindGroup {
		return append(dst, slog.Attr{Key: prefix + a.Key, Value: a.Value})
	}
	inner := prefix
	if a.Key != "" {
		inner = prefix + a.Key + "."
	}
	for _, g := range a.Value.Group() {
		dst = appendFlat(dst, inner, g)
	}
	return dst
}
