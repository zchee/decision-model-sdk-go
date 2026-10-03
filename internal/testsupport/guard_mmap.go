//go:build linux || darwin

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

package testsupport

import (
	"os"
	"syscall"
	"testing"
)

func newGuard(tb testing.TB, size int) *Guard {
	tb.Helper()
	page := os.Getpagesize()
	data := max((size+page-1)/page, 1) * page
	mem, err := syscall.Mmap(-1, 0, data+2*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		tb.Fatalf("mmap of %d bytes: %v", data+2*page, err)
	}
	tb.Cleanup(func() {
		if err := syscall.Munmap(mem); err != nil {
			tb.Errorf("munmap: %v", err)
		}
	})
	if err := syscall.Mprotect(mem[:page], syscall.PROT_NONE); err != nil {
		tb.Fatalf("mprotect of the leading guard page: %v", err)
	}
	if err := syscall.Mprotect(mem[page+data:], syscall.PROT_NONE); err != nil {
		tb.Fatalf("mprotect of the trailing guard page: %v", err)
	}
	return &Guard{mem: mem, page: page, data: data}
}
