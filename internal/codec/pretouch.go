//go:build !go1.28 && (amd64 || arm64)

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

package codec

import (
	"errors"
	"reflect"

	"github.com/bytedance/sonic/encoder"
)

// errPretouchNilType is returned by Pretouch for a nil type.
var errPretouchNilType = errors.New("pretouch: nil type")

// Pretouch compiles sonic's encoder for values of type t ahead of the first
// request, so that the one-time JIT compilation is not paid inside a call
// (the root package's WithPretouch option calls it).
func Pretouch(t reflect.Type) error {
	if t == nil {
		return errPretouchNilType
	}
	return encoder.Pretouch(t)
}
