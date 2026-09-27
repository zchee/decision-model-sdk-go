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
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/engine"
)

// Text the SDK did not write is escaped and cut by internal/engine (NF7,
// rulings R58 and R58b); these names keep the root package's call sites as
// they were.
const (
	maxNameChars    = engine.MaxNameChars
	maxMessageChars = engine.MaxMessageChars
	maxPathChars    = engine.MaxPathChars
)

// appendSafeText is [engine.AppendSafeText].
func appendSafeText(dst []byte, s string, limit int, double bool) []byte {
	return engine.AppendSafeText(dst, s, limit, double)
}

// quotedName is [engine.QuotedName].
func quotedName(name string) string { return engine.QuotedName(name) }

// safeName is [engine.SafeName].
func safeName(name string) string { return engine.SafeName(name) }

// safeMessage is [engine.SafeMessage].
func safeMessage(s string) string { return engine.SafeMessage(s) }

// requestCredentials is [engine.RequestCredentials].
func requestCredentials(h http.Header) engine.Credentials { return engine.RequestCredentials(h) }

// renderFieldPath returns p as a *ResponseValidationError prints it: the
// Python SDK's dotted field_path, "." for the root, with each name the server
// chose (an answer name, a probability or legend key) escaped with its
// backslashes doubled and cut at [maxNameChars], and the whole cut at
// [maxPathChars].
func renderFieldPath(p codec.FieldPath) string {
	if p.Top == "" {
		return "."
	}
	t := pathText{limit: maxPathChars}
	t.fixed(p.Top)
	if p.HasIndex {
		t.fixed("[" + strconv.Itoa(p.Index) + "]")
	}
	if p.HasName {
		t.fixed(".")
		t.name(p.Name)
	}
	if p.Member != "" {
		t.fixed(".")
		t.fixed(p.Member)
	}
	if p.HasKey {
		t.fixed(".")
		t.name(p.Key)
	}
	return string(t.b)
}

// pathText builds a rendering capped at limit characters from the SDK's own
// text and escaped names. Once a piece would cross the limit, U+2026 is
// written in its place and nothing after it.
type pathText struct {
	b     []byte
	n     int // characters written
	limit int
	full  bool
}

// fixed appends s, ASCII text of the SDK's own, as it is.
func (t *pathText) fixed(s string) {
	if t.full {
		return
	}
	if t.n+len(s) > t.limit {
		t.b = append(t.b, "\u2026"...)
		t.full = true
		return
	}
	t.b = append(t.b, s...)
	t.n += len(s)
}

// name appends s, a name the server chose, escaped and cut at
// [maxNameChars] and at what remains of the limit.
func (t *pathText) name(s string) {
	if t.full {
		return
	}
	room := min(maxNameChars, t.limit-t.n)
	start := len(t.b)
	t.b = appendSafeText(t.b, s, room, true)
	written := utf8.RuneCount(t.b[start:])
	if room < maxNameChars && written > room {
		// The name was cut by the whole path's limit, not its own.
		t.full = true
	}
	t.n += written
}
