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
	"iter"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// Prepared is a question set validated and serialised once by
// [Questions.Prepare], and reused as is by every call that asks it.
//
// It is read-only and safe for concurrent use. It holds, besides the bytes a
// request sends as "questions", the tables the response decoder matches
// answers against: the names, each choice question's option labels and each
// score question's levels.
type Prepared struct {
	w wire.Prepared
}

// Len returns the number of questions in the set. It is never zero for a set
// returned by [Questions.Prepare].
func (p *Prepared) Len() int { return len(p.w.Entries()) }

// Names returns the question names in the order they are sent.
func (p *Prepared) Names() iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, e := range p.w.Entries() {
			if !yield(e.Name) {
				return
			}
		}
	}
}
