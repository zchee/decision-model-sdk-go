// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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

//go:build live

package livetest

import "testing"

// The guard tests read no environment: getenv is a map lookup, and
// every value is a made-up word.

// TestSkipReasonAny pins the guard the provider tests run first: both
// the ADAPTER_LIVE_TESTS switch and at least one credential variable
// must be set, and the reasons name variable names only.
func TestSkipReasonAny(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		vars []string
		want string
	}{
		"skip: the switch is unset": {
			env:  map[string]string{"GOOGLE_API_KEY": "madeupword"},
			vars: []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"},
			want: "ADAPTER_LIVE_TESTS is not 1",
		},
		"skip: the switch is not 1": {
			env:  map[string]string{"ADAPTER_LIVE_TESTS": "0", "OPENAI_API_KEY": "madeupword"},
			vars: []string{"OPENAI_API_KEY"},
			want: "ADAPTER_LIVE_TESTS is not 1",
		},
		"skip: the switch alone is not enough": {
			env:  map[string]string{"ADAPTER_LIVE_TESTS": "1"},
			vars: []string{"OPENAI_API_KEY"},
			want: "none of OPENAI_API_KEY is set",
		},
		"skip: neither of two variables is set": {
			env:  map[string]string{"ADAPTER_LIVE_TESTS": "1"},
			vars: []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"},
			want: "none of GOOGLE_API_KEY, GEMINI_API_KEY is set",
		},
		"run: the first variable is set": {
			env:  map[string]string{"ADAPTER_LIVE_TESTS": "1", "GOOGLE_API_KEY": "madeupword"},
			vars: []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"},
			want: "",
		},
		"run: the second variable is set": {
			env:  map[string]string{"ADAPTER_LIVE_TESTS": "1", "GEMINI_API_KEY": "madeupword"},
			vars: []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"},
			want: "",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := skipReasonAny(func(name string) string { return tt.env[name] }, tt.vars...); got != tt.want {
				t.Errorf("skipReasonAny(%v, %v) = %q, want %q", tt.env, tt.vars, got, tt.want)
			}
		})
	}
}

// TestSkipReasonAll pins the System One reference test's guard: the
// switch and every named variable must be set.
func TestSkipReasonAll(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		vars []string
		want string
	}{
		"skip: the switch is unset": {
			env:  map[string]string{"DECISION_MODEL_API_KEY": "madeupword"},
			vars: []string{"DECISION_MODEL_API_KEY", "DECISION_MODEL_BASE_URL"},
			want: "ADAPTER_LIVE_TESTS is not 1",
		},
		"skip: the key alone is not enough": {
			env:  map[string]string{"ADAPTER_LIVE_TESTS": "1", "DECISION_MODEL_API_KEY": "madeupword"},
			vars: []string{"DECISION_MODEL_API_KEY", "DECISION_MODEL_BASE_URL"},
			want: "DECISION_MODEL_BASE_URL is not set",
		},
		"skip: the base URL alone is not enough": {
			env:  map[string]string{"ADAPTER_LIVE_TESTS": "1", "DECISION_MODEL_BASE_URL": "madeupword"},
			vars: []string{"DECISION_MODEL_API_KEY", "DECISION_MODEL_BASE_URL"},
			want: "DECISION_MODEL_API_KEY is not set",
		},
		"run: every variable is set": {
			env: map[string]string{
				"ADAPTER_LIVE_TESTS":      "1",
				"DECISION_MODEL_API_KEY":  "madeupword",
				"DECISION_MODEL_BASE_URL": "madeupword2",
			},
			vars: []string{"DECISION_MODEL_API_KEY", "DECISION_MODEL_BASE_URL"},
			want: "",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := skipReasonAll(func(name string) string { return tt.env[name] }, tt.vars...); got != tt.want {
				t.Errorf("skipReasonAll(%v, %v) = %q, want %q", tt.env, tt.vars, got, tt.want)
			}
		})
	}
}
