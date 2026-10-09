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

package cassette

import (
	"strings"
	"testing"
)

func TestRecordedBodyReencodingErrors(t *testing.T) {
	t.Parallel()
	deep := strings.Repeat("[", 300) + strings.Repeat("]", 300)
	tests := map[string]struct {
		body     string
		response bool
	}{
		"error: ordinary nested member":           {body: `{"ordinary":` + deep + `}`},
		"error: response item cannot encode":      {body: `{"output":[` + deep + `]}`, response: true},
		"error: response item id is not a string": {body: `{"output":[{"id":` + deep + `}]}`, response: true},
		"error: request prior id is not a string": {body: `{"previous_response_id":` + deep + `}`},
		"error: request input cannot encode":      {body: `{"input":[` + deep + `]}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := recordedBody([]byte(tt.body), tt.response)
			if err == nil || !strings.Contains(err.Error(), "re-encoding a recorded body") {
				t.Fatal("deep body did not return the recorded-body re-encoding error")
			}
		})
	}
}
