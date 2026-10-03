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

package decision

// This file is the typed decode's store, the root package's one use of unsafe
// (the seam test TestSeamRootRawPointers names this file and no other).
// DecodeAs writes each answer into the field of the caller's T at the offset
// reflect gave that field when the plan was built, so that the T stays on
// DecodeAs's stack instead of moving to the heap through
// reflect.Value.Interface.
//
// The invariants that make each write sound, and where each is kept:
//
//  1. base points to a whole, live value of the struct type the plan was
//     built for: decodeTyped takes it from its own local T with baseOf, and
//     panics unless the plan's type is that T's type (typedPlan.typ).
//  2. off is reflect.StructField.Offset of one of that struct's own fields,
//     taken once per type by buildPlan (typedField.offset). An answer field
//     is never promoted from an embedded struct: planField refuses a
//     decision tag below the struct's own fields, so no offset is a sum.
//     Before a plan is used, checkPlanLayout compares each field's offset,
//     end and kind with what reflect gives the field at its index, and
//     panics by name on any difference.
//  3. F is that field's type: buildPlan admits a field only when its type is
//     the answer type of its kind (answerKind), and decode picks F from the
//     same kind. So the write covers exactly the field, at its own
//     alignment, and never a neighbour.
//  4. The write is a typed assignment through *F, never a copy of bytes:
//     the compiler emits the write barriers F's pointer fields need, so the
//     collector sees every pointer stored (TestMain checks the shape of
//     this file before every test).
//  5. The pointer is not kept: a fieldBase lives on the caller's stack for
//     one decode, and nothing here stores it.
//  6. Invariants 2 and 3 are checked before every write, not only trusted (the
//     per-write bound): storeAnswer writes only when the offset is inside T,
//     the write of F's size from it ends exactly at the field's end that
//     buildPlan recorded apart from the offset (typedField.end, reflect's
//     Offset plus the size of the field's type), and that end is inside T,
//     whose size baseOf takes from T itself at compile time rather than from
//     the plan. Otherwise it panics with a message that names T, the field and
//     the offset. So a plan holding a wrong offset (another field's, 0, one
//     past the last field) or a store of the wrong answer type stops before a
//     byte is written, where the write would put response bytes into a pointer
//     slot of T or past T into the caller's frame. The check compares values
//     the decode already holds: no allocation, no reflection, until it fails.
//     It cannot see a plan whose offset and end both name another field of the
//     same size; checkPlanLayout refuses that plan when it is built
//     (invariant 2).

import "unsafe"

// fieldBase is the address and the size of the T a typed decode fills. It
// is opaque outside this file, which keeps every use of unsafe here.
type fieldBase struct {
	p unsafe.Pointer
	// size is T's size, the bound of every write (invariant 6).
	size uintptr
}

// baseOf returns the address of *t, and T's size, as a fieldBase.
func baseOf[T any](t *T) fieldBase {
	return fieldBase{p: unsafe.Pointer(t), size: unsafe.Sizeof(*t)}
}

// storeAnswer writes v into the answer field f of the struct b points to,
// a value of the type p was built for (invariants 1 to 6 above). It panics
// before the write unless the write stays inside the struct and covers
// exactly the bytes buildPlan recorded for f (invariant 6). The first
// comparison keeps off+n from wrapping around.
func storeAnswer[F NoulAnswer | ChoiceAnswer | ScoreAnswer](b fieldBase, p *typedPlan, f *typedField, v F) {
	off, n := f.offset, unsafe.Sizeof(v)
	if off > b.size || off+n != f.end || f.end > b.size {
		panic(storeRefusal{p: p, f: f, off: off, n: n, size: b.size})
	}
	*(*F)(unsafe.Add(b.p, off)) = v
}
