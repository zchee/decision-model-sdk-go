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
	"log/slog"
	"strings"
	"testing"
	"testing/slogtest"

	gocmp "github.com/google/go-cmp/cmp"
)

// levelTrace is the SDK's trace level (plan section 0: slog.LevelDebug-4).
const levelTrace = slog.LevelDebug - 4

// TestLogRecorderConformance runs the standard library's handler tests over
// the recorder, rebuilding nested groups from the flattened keys.
func TestLogRecorderConformance(t *testing.T) {
	var rec *LogRecorder
	slogtest.Run(t, func(*testing.T) slog.Handler {
		rec = NewLogRecorder(nil)
		return rec.Handler()
	}, func(t *testing.T) map[string]any {
		records := rec.Records()
		if len(records) != 1 {
			t.Fatalf("%d records, want 1", len(records))
		}
		r := records[0]
		m := map[string]any{slog.LevelKey: r.Level, slog.MessageKey: r.Message}
		if !r.Time.IsZero() {
			m[slog.TimeKey] = r.Time
		}
		for _, a := range r.Attrs {
			parts := strings.Split(a.Key, ".")
			node := m
			for _, group := range parts[:len(parts)-1] {
				next, ok := node[group].(map[string]any)
				if !ok {
					next = map[string]any{}
					node[group] = next
				}
				node = next
			}
			node[parts[len(parts)-1]] = a.Value.Any()
		}
		return m
	})
}

// TestLogRecorder covers level filtering, flattening and the accessors.
func TestLogRecorder(t *testing.T) {
	tests := map[string]struct {
		level     slog.Leveler
		log       func(*slog.Logger)
		wantLines []string
	}{
		"success: nil level keeps trace records": {
			level: nil,
			log: func(l *slog.Logger) {
				l.Log(t.Context(), levelTrace, "body", "bytes", 12)
				l.Info("attempt", "n", 1)
			},
			wantLines: []string{"DEBUG-4 body bytes=12", "INFO attempt n=1"},
		},
		"success: INFO level drops DEBUG": {
			level: slog.LevelInfo,
			log: func(l *slog.Logger) {
				l.Debug("h2: dial")
				l.Warn("skipped answer", "name", "mystery")
			},
			wantLines: []string{"WARN skipped answer name=mystery"},
		},
		"success: groups and With attributes are flattened": {
			level: nil,
			log: func(l *slog.Logger) {
				l.With("sdk", "go").WithGroup("req").With("attempt", 2).Info("sent", slog.Group("hdr", "retry", "1"), "status", 503)
			},
			wantLines: []string{"INFO sent sdk=go req.attempt=2 req.hdr.retry=1 req.status=503"},
		},
		"success: a LogValuer is resolved": {
			level: nil,
			log: func(l *slog.Logger) {
				l.Info("redacted", "authorization", redacted("secret"))
			},
			wantLines: []string{"INFO redacted authorization=***"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := NewLogRecorder(tt.level)
			tt.log(rec.Logger())
			var got []string
			for _, r := range rec.Records() {
				got = append(got, r.String())
			}
			if diff := gocmp.Diff(tt.wantLines, got); diff != "" {
				t.Errorf("records (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("success: At, Attr and Reset", func(t *testing.T) {
		rec := NewLogRecorder(nil)
		l := rec.Logger()
		l.Info("attempt", "retry", 0)
		l.Warn("skipped", "name", "a")
		l.Info("attempt", "retry", 1, "retry", 2)
		infos := rec.At(slog.LevelInfo)
		if len(infos) != 2 {
			t.Fatalf("At(INFO) = %d records, want 2", len(infos))
		}
		if v, ok := infos[1].Attr("retry"); !ok || v.Int64() != 2 {
			t.Errorf("Attr(retry) = %v, %v; want the last value 2", v, ok)
		}
		if _, ok := infos[0].Attr("missing"); ok {
			t.Errorf("Attr(missing) reported a value")
		}
		rec.Reset()
		if n := len(rec.Records()); n != 0 {
			t.Errorf("%d records after Reset", n)
		}
	})
}

// redacted is a LogValuer that hides its value.
type redacted string

// LogValue implements slog.LogValuer.
func (redacted) LogValue() slog.Value { return slog.StringValue("***") }
