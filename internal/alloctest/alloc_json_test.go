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

package alloctest

import (
	"maps"
	"slices"
	"testing"

	typesafe "github.com/zchee/typesafe-sdk-go"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// sinkPayload keeps a measured payload reachable, as a caller's would be.
var sinkPayload []byte

// jsonAllocs pins the allocations of reading a response payload back and of
// writing it (informational: no frozen budget covers them). A payload
// is one allocation whatever its size: the writer sizes its buffer before
// writing, with every float at its longest, so only strings that need
// escapes can outgrow it. Reading a payload back is the decode without a
// question set or a model, so every string is a copy into one arena:
// TestAllocDecodeFixtures's misses column for the same bytes.
var jsonAllocs = map[string]struct{ marshal, unmarshal uint64 }{
	"result.json":                     {marshal: 1, unmarshal: 5},
	"result-20.json":                  {marshal: 1, unmarshal: 25},
	"structured-legend-flood-1k.json": {marshal: 1, unmarshal: 91},
	"models.json":                     {marshal: 1, unmarshal: 2},
}

// TestAllocResponseJSON measures MarshalJSON and UnmarshalJSON of a response
// read from each fixture of jsonAllocs, the decoder's pool warm. Each JSON
// line is a ledger row.
func TestAllocResponseJSON(t *testing.T) {
	testsupport.QuietRuntime(t)
	for _, name := range slices.Sorted(maps.Keys(jsonAllocs)) {
		pin := jsonAllocs[name]
		t.Run(name, func(t *testing.T) {
			data := testsupport.Fixture(t, name)
			measure := measureJSON[typesafe.SystemOneResponse]
			if name == "models.json" {
				measure = measureJSON[typesafe.ModelsResponse]
			}
			marshal, unmarshal, payload := measure(t, name, data)
			t.Logf("JSON %-32s payload=%-7d marshal allocs=%d bytes=%-7d unmarshal allocs=%d bytes=%d",
				name, len(payload), marshal.Mallocs, marshal.Bytes, unmarshal.Mallocs, unmarshal.Bytes)
			if marshal.Mallocs != pin.marshal || unmarshal.Mallocs != pin.unmarshal {
				t.Errorf("allocations: marshal %d, unmarshal %d; want %d, %d", marshal.Mallocs, unmarshal.Mallocs, pin.marshal, pin.unmarshal)
			}
		})
	}
}

// measureJSON reads a response of type T from data with UnmarshalJSON,
// writes it back with MarshalJSON, and measures each: the write of that
// response, and the read of the payload it wrote into a new T.
func measureJSON[T any, P interface {
	*T
	MarshalJSON() ([]byte, error)
	UnmarshalJSON(data []byte) error
}](t *testing.T, name string, data []byte) (marshal, unmarshal testsupport.Allocs, payload []byte) {
	t.Helper()
	var resp T
	if err := P(&resp).UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	payload, err := P(&resp).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	marshal = testsupport.MeasureMin(t, name+" marshal", func() P { return &resp }, func(r P) {
		sinkPayload, _ = r.MarshalJSON()
	})
	unmarshal = testsupport.MeasureMin(t, name+" unmarshal", func() P { return new(T) }, func(r P) {
		if err := r.UnmarshalJSON(payload); err != nil {
			t.Error(err)
		}
	})
	return marshal, unmarshal, payload
}
