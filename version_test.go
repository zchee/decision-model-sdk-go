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
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestSDKIdentifier pins the product the SDK names itself by in User-Agent
// and X-TypeSafe-SDK: its own name, not the Python SDK's typesafe-sdk, and a
// version that is a semantic version (https://semver.org), so that a
// release that forgets to set it cannot ship an empty or free-form one.
func TestSDKIdentifier(t *testing.T) {
	semver := regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?$`)
	if !semver.MatchString(Version) {
		t.Errorf("Version = %q, want a semantic version", Version)
	}
	if want := "typesafe-sdk-go/" + Version; sdkIdentifier != want {
		t.Errorf("sdkIdentifier = %q, want %q", sdkIdentifier, want)
	}
	if !validFieldValue(sdkIdentifier) || productRule(sdkIdentifier) != "" {
		t.Errorf("sdkIdentifier %q is not a product token: %s", sdkIdentifier, productRule(sdkIdentifier))
	}
}

// TestRuntimeHeaderValue pins the X-TypeSafe-Runtime format, go/<release>
// (<goos>; <goarch>), for the three shapes runtime.Version takes: a release;
// a release built with experiments, whose "-X:" or " X:" suffix is cut
// (rulings R63, R63b); and a development toolchain, whose "devel ..." string
// has no "go" prefix and is kept whole. The linker writes " X:" when the
// version already holds a "-" (cmd/link/internal/ld/main.go:193-197).
// go1.27.1 on (M), darwin/arm64, reports "go1.27.1-X:simd,runtimesecret"
// without the repository's GOEXPERIMENT and "go1.27.1" with it (probe
// 2026-09-26 00:28:27 JST).
func TestRuntimeHeaderValue(t *testing.T) {
	tests := map[string]struct {
		version, goos, goarch string
		want                  string
	}{
		"success: release": {
			version: "go1.27.1", goos: "linux", goarch: "amd64",
			want: "go/1.27.1 (linux; amd64)",
		},
		"success: release candidate": {
			version: "go1.28rc1", goos: "darwin", goarch: "arm64",
			want: "go/1.28rc1 (darwin; arm64)",
		},
		"success: experiments are cut": {
			version: "go1.27.1-X:simd,runtimesecret", goos: "darwin", goarch: "arm64",
			want: "go/1.27.1 (darwin; arm64)",
		},
		"success: one experiment is cut": {
			version: "go1.27.1-X:nosimd", goos: "linux", goarch: "amd64",
			want: "go/1.27.1 (linux; amd64)",
		},
		"success: experiments after a space are cut": {
			version: "go1.27.1-bigcorp X:simd", goos: "linux", goarch: "amd64",
			want: "go/1.27.1-bigcorp (linux; amd64)",
		},
		"success: the earlier of the two spellings wins": {
			version: "go1.27.1-X:a X:b", goos: "linux", goarch: "amd64",
			want: "go/1.27.1 (linux; amd64)",
		},
		"success: devel string with experiments is kept whole": {
			version: "devel go1.28-4c3b2a1 Tue Sep 22 10:00:00 2026 +0000 X:simd", goos: "linux", goarch: "arm64",
			want: "go/devel go1.28-4c3b2a1 Tue Sep 22 10:00:00 2026 +0000 X:simd (linux; arm64)",
		},
		"success: devel string is kept whole": {
			version: "devel go1.28-4c3b2a1 Tue Sep 22 10:00:00 2026 +0000", goos: "linux", goarch: "arm64",
			want: "go/devel go1.28-4c3b2a1 Tue Sep 22 10:00:00 2026 +0000 (linux; arm64)",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := runtimeHeaderValue(tt.version, tt.goos, tt.goarch)
			if got != tt.want {
				t.Errorf("runtimeHeaderValue(%q, %q, %q) = %q, want %q", tt.version, tt.goos, tt.goarch, got, tt.want)
			}
			if !validFieldValue(got) {
				t.Errorf("runtimeHeaderValue(%q, ...) = %q is not a valid header field value", tt.version, got)
			}
		})
	}

	// The value this process sends, derived from the running toolchain by
	// the same rule: a release loses "go" and everything from the first
	// "-X:" or " X:", a devel string is kept whole.
	release := runtime.Version()
	if r, ok := strings.CutPrefix(release, "go"); ok {
		release = r
		for _, sep := range []string{"-X:", " X:"} {
			if i := strings.Index(release, sep); i >= 0 {
				release = release[:i]
			}
		}
	}
	want := "go/" + release + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
	if runtimeIdentifier != want {
		t.Errorf("runtimeIdentifier = %q, want %q (runtime.Version() = %q)", runtimeIdentifier, want, runtime.Version())
	}
	if !validFieldValue(runtimeIdentifier) {
		t.Errorf("runtimeIdentifier %q is not a valid header field value", runtimeIdentifier)
	}
}
