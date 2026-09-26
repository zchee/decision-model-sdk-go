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
	"strings"
	"testing"
)

// TestSizeHintCoversRawValues checks that the size hint of a raw question
// counts a field that is a long string, RawJSON or Content (W5.3's P3), so
// that Prepare's buffer holds the question without growing: the hint is at
// least the prepared length. A field of any other kind still counts 32
// bytes, which a long one outgrows.
func TestSizeHintCoversRawValues(t *testing.T) {
	long := strings.Repeat("x", 4096)
	tests := map[string]struct {
		value any
	}{
		"success: a string":      {value: long},
		"success: RawJSON":       {value: RawJSON(`["` + long + `"]`)},
		"success: Content text":  {value: Text(long)},
		"success: Content JSON":  {value: JSON(RawJSON(`{"k":"` + long + `"}`))},
		"success: pretty values": {value: RawJSON("[\n  \"" + long + "\"\n]")},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			qs := NewQuestions().Raw("q", RawQuestion{Type: "future", Fields: map[string]any{"v": tt.value, "w": tt.value}})
			p, err := qs.Prepare()
			if err != nil {
				t.Fatal(err)
			}
			if hint, got := qs.sizeHint(), len(p.w.Questions); hint < got {
				t.Errorf("sizeHint = %d, below the prepared length %d: the buffer grows", hint, got)
			}
		})
	}
}
