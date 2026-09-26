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

// This file is the typed decode's store, the root package's one use of
// unsafe (owner ruling R116, superseding R112; the seam test
// TestSeamRootRawPointers names this file and no other). DecodeAs writes
// each answer into the field of the caller's T at the offset reflect gave
// that field when the plan was built, so that the T stays on DecodeAs's
// stack instead of moving to the heap through reflect.Value.Interface.
//
// The invariants that make each write sound, and where each is kept:
//
//  1. base points to a whole, live value of the struct type the plan was
//     built for: decodeTyped takes it from its own local T with baseOf, and
//     panics unless the plan's type is that T's type (typedPlan.typ).
//  2. off is reflect.StructField.Offset of one of that struct's own fields,
//     taken once per type by buildPlan (typedField.offset). An answer field
//     is never promoted from an embedded struct: planField refuses a
//     typesafe tag below the struct's own fields, so no offset is a sum.
//  3. F is that field's type: buildPlan admits a field only when its type is
//     the answer type of its kind (answerKind), and decode picks F from the
//     same kind. So the write covers exactly the field, at its own
//     alignment, and never a neighbour.
//  4. The write is a typed assignment through *F, never a copy of bytes:
//     the compiler emits the write barriers F's pointer fields need, so the
//     collector sees every pointer stored (TestStoreWritesTyped checks the
//     shape of this file).
//  5. The pointer is not kept: a fieldBase lives on the caller's stack for
//     one decode, and nothing here stores it.

import "unsafe"

// fieldBase is the address of the T a typed decode fills. It is opaque
// outside this file, which keeps every use of unsafe here.
type fieldBase struct {
	p unsafe.Pointer
}

// baseOf returns the address of *t as a fieldBase.
func baseOf[T any](t *T) fieldBase {
	return fieldBase{p: unsafe.Pointer(t)}
}

// storeAnswer writes v into the field of type F at offset off of the struct
// b points to (invariants 1 to 5 above).
func storeAnswer[F NoulAnswer | ChoiceAnswer | ScoreAnswer](b fieldBase, off uintptr, v F) {
	*(*F)(unsafe.Add(b.p, off)) = v
}
