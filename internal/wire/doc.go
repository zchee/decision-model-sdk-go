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

// Package wire holds the plain values that a decoded response and a prepared
// question set are made of: answers, token usage, model cards, response
// metadata, error data and the prepared question tables.
//
// The package imports the standard library only. internal/codec fills these
// values and the root package wraps them in its exported types
// (type NoulAnswer struct{ w wire.NoulAnswer }), so both can share the shapes
// without an import cycle, and the weekly gotip canary can test this package
// on a toolchain that internal/codec refuses to build on.
//
// Every value here is read-only once it has been handed to the root package:
// slices and maps are shared, not copied.
package wire
