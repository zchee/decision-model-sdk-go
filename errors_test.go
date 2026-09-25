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
	"fmt"
	"io/fs"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// errSentinel stands for a sentinel error of this package that a
// *ConfigError identifies its failure with (the port plan's section 6.3 pattern).
var errSentinel = errors.New("sentinel")

// causeError is a cause with a type of its own, reached with errors.As.
type causeError struct{ detail string }

func (e *causeError) Error() string { return "cause: " + e.detail }

func TestConfigError(t *testing.T) {
	cause := &causeError{detail: "dial"}
	tests := map[string]struct {
		err        *ConfigError
		wantMsg    string
		wantUnwrap []error
		wantIs     []error // errors.Is holds for each
		wantNotIs  []error // and does not hold for each
		wantCause  bool    // errors.As reaches *causeError
	}{
		"success: a message alone": {
			err:       newConfigError("At least one question is required."),
			wantMsg:   "At least one question is required.",
			wantNotIs: []error{errSentinel, fs.ErrNotExist},
		},
		"success: a sentinel and a cause": {
			err:        newConfigError("HTTP/2 was not negotiated", errSentinel, cause),
			wantMsg:    "HTTP/2 was not negotiated",
			wantUnwrap: []error{errSentinel, cause},
			wantIs:     []error{errSentinel, cause},
			wantNotIs:  []error{fs.ErrNotExist},
			wantCause:  true,
		},
		"success: a wrapped cause": {
			err:        newConfigError("bad question", fmt.Errorf("wrapped: %w", fs.ErrNotExist)),
			wantMsg:    "bad question",
			wantUnwrap: []error{fmt.Errorf("wrapped: %w", fs.ErrNotExist)},
			wantIs:     []error{fs.ErrNotExist},
			wantNotIs:  []error{errSentinel},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", got, tt.wantMsg)
			}
			// Errors compare by identity, or by text for the freshly wrapped
			// cause of the third case.
			sameError := gocmp.Comparer(func(a, b error) bool { return errors.Is(a, b) || a.Error() == b.Error() })
			if diff := gocmp.Diff(tt.wantUnwrap, tt.err.Unwrap(), sameError); diff != "" {
				t.Errorf("Unwrap() mismatch (-want +got):\n%s", diff)
			}
			// Through the error interface and one more wrap, as a caller
			// receives it.
			err := fmt.Errorf("call: %w", tt.err)
			for _, target := range tt.wantIs {
				if !errors.Is(err, target) {
					t.Errorf("errors.Is(%v, %v) = false, want true", err, target)
				}
			}
			for _, target := range tt.wantNotIs {
				if errors.Is(err, target) {
					t.Errorf("errors.Is(%v, %v) = true, want false", err, target)
				}
			}
			var ce *ConfigError
			if !errors.As(err, &ce) || ce != tt.err {
				t.Errorf("errors.As(*ConfigError) = %v, want %v", ce, tt.err)
			}
			var got *causeError
			if found := errors.As(err, &got); found != tt.wantCause {
				t.Errorf("errors.As(*causeError) = %t, want %t", found, tt.wantCause)
			}
		})
	}
}
