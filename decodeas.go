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

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"

	"github.com/zchee/decision-model-sdk-go/internal/codec"
	"github.com/zchee/decision-model-sdk-go/internal/engine"
	"github.com/zchee/decision-model-sdk-go/internal/wire"
)

// Ask asks the questions the struct type T declares about state and returns
// the answers as a T: it is [Client.SystemOne] with the question set
// [PreparedFor] builds for T, followed by [DecodeAs] of the response.
//
//	t, err := decision.Ask[Ticket](ctx, c, state)
//
// A T that PreparedFor refuses fails with its [*ConfigError] before any
// request is made. Every error of the call itself, an [*APIError], a
// [*ConnectionError], a [*TimeoutError], a [*ResponseValidationError] of the
// response as a whole and the others [Client.SystemOne] lists, is returned
// as SystemOne returns it. A response whose answers do not fit T fails as
// DecodeAs says, with the call's endpoint in the error and its header
// redacted as the call's other errors redact theirs ([APIError.Header]).
//
// Ask returns only the answers. A caller that also needs the response,
// its request id, usage, model or raw body, makes the two calls Ask makes:
//
//	qs, err := decision.PreparedFor[Ticket]()
//	resp, err := c.SystemOne(ctx, state, qs)
//	t, err := decision.DecodeAs[Ticket](resp)
//	id, ok := resp.Meta().RequestID()
func Ask[T any](ctx context.Context, c *Client, state any, opts ...CallOption) (T, error) {
	p := typedPlanFor[T]()
	if p.err != nil {
		var zero T
		return zero, p.err
	}
	var red engine.HeaderRedactor
	resp, err := c.systemOne(ctx, state, p.prepared, opts, &red)
	if err != nil {
		var zero T
		return zero, err
	}
	return decodeTyped[T](p, resp, c.eng().SystemOneEndpoint(), red)
}

// DecodeAs returns the answers of resp as a T, the struct type whose fields
// declare the questions ([PreparedFor] has the grammar): each answer field
// holds the answer that resp carries under the field's question name.
// resp may answer a question set built by hand, such as one from
// [NewQuestions]; the names are what DecodeAs matches, so a field tagged
// name=spam reads the answer to the question "spam".
//
// DecodeAs reads the answers as [SystemOneResponse.Answers] holds them:
// when the body repeats a name, the last answer under it is the one read,
// and an answer of a type this version does not model is not there. The
// fields hold copies that share resp's slices and bytes, which must not be
// modified; resp itself is not changed, and must not be nil. Answers that
// no field of T names are ignored. Each field read has Present true; an
// optional field without an answer is left as the zero answer, whose
// Present is false and which marshals as null, as the Python SDK holds and
// dumps None there.
//
// A T that PreparedFor refuses fails with its [*ConfigError]. Otherwise the
// fields are read in their order in T, and the first that does not fit fails
// the whole decode: DecodeAs returns the zero T, never one holding the fields
// read before the failure, and a [*ResponseValidationError] whose FieldPath
// names the answer. T's fields are the answers lifted out of the response's
// "answers" member, so the paths are the ones the Python SDK reports for such
// a response model, a SystemOneResponse subclass with one field per answer:
// the answer's name, then the member and the key. An answer of a type this
// version does not model has been skipped, as that model skips it, so under a
// required field's name it fails as absent and under an optional field's name
// it leaves the field absent, and a response without an "answers" member fails
// at T's first required field:
//
//	the answer is absent and the field is not optional  <name>
//	the answer is of another kind than the field        <name>.type
//	a choice picks an option T does not list            <name>.choice
//	a choice gives a probability to such an option      <name>.probabilities.<option>
//	a score's legend has a level beyond T's levels      <name>.legend.<level>
//	a score gives a probability to such a level         <name>.probabilities.<level>
//
// A failure of the response as a whole, a missing model or usage or an
// answer without a type, comes from the decoder before T is filled, at the
// decoder's own path, such as usage or answers.spam.noul, as in Python,
// which validates the base model's fields first.
//
// A choice's options and a score's levels are the ones T's tag lists: a
// level counts from zero, so a score with three levels has the levels 0, 1
// and 2, and a level is written in decimal in the path. An option or a
// level the answer does not mention, and the score's value, are not
// checked. Python checks an option only when the response model's own type
// says so, with a Literal, and then reports the pick at "tone.choice", as
// DecodeAs does.
//
// The error carries resp's HTTP metadata, none of it for a response read
// back with [SystemOneResponse.UnmarshalJSON], whose Meta is empty. Its
// Header is redacted as [APIError.Header] describes, with the credentials of
// the call resp came from, however long resp is kept; a response read back
// with UnmarshalJSON came from no client and is redacted by header name
// alone. FieldPath is not redacted: the answer's name and the option or
// level it names are shown as they arrived, as the SDK's other errors show a
// path. Its Endpoint is empty; Ask fills it in.
func DecodeAs[T any](resp *SystemOneResponse) (T, error) {
	return decodeTyped[T](typedPlanFor[T](), resp, "", resp.redactor())
}

// decodeTyped decodes resp into a T by p, the plan of T, and names endpoint
// in a validation error, whose header r redacts. On failure it returns the
// zero T, not a T with the fields read before the failure. The answers are
// written into t through the offsets of p (decodeas_store.go), so t stays
// on this frame: a plan of another type would write outside it, so that is
// a panic, not an error (the callers always pass T's own plan).
func decodeTyped[T any](p *typedPlan, resp *SystemOneResponse, endpoint string, r engine.HeaderRedactor) (T, error) {
	var t T
	if p.err == nil && p.typ != reflect.TypeFor[T]() {
		panic("decision: decodeTyped[" + reflect.TypeFor[T]().String() + "] given the plan of " + p.typ.String())
	}
	if err := p.decode(resp, endpoint, r, baseOf(&t)); err != nil {
		var zero T
		return zero, err
	}
	return t, nil
}

// Why an answer does not fit its field, as the decode error of a
// [*ResponseValidationError] from [DecodeAs] wraps it.
var (
	errTypedMissing = errors.New("the answer is absent, and the field is not optional")
	errTypedKind    = errors.New("the answer is of another kind than its field")
	errTypedOption  = errors.New("the answer names an option its field's tag does not list")
	errTypedLevel   = errors.New("the answer names a level beyond the levels its field's tag lists")
)

// decode reads the answers of resp into the struct b points to, a value of
// the type p was built for, field by field in p's order, and names endpoint
// in a validation error, whose header r redacts. It returns p's own error
// for a refused type.
func (p *typedPlan) decode(resp *SystemOneResponse, endpoint string, r engine.HeaderRedactor, b fieldBase) error {
	if p.err != nil {
		return p.err
	}
	answers := &resp.result().Answers
	for i := range p.fields {
		f := &p.fields[i]
		a, ok := answers.Get(f.name)
		switch {
		case !ok && f.optional:
			continue
		case !ok:
			return typedError(resp, endpoint, r, f.name, codec.FieldPath{}, errTypedMissing)
		case a.Kind != f.kind:
			return typedError(resp, endpoint, r, f.name, codec.FieldPath{Member: "type"}, errTypedKind)
		}
		// The answer goes into the field at its offset, as the field's own
		// type (decodeas_store.go): no answer is boxed and the T being
		// decoded stays on the caller's stack.
		switch f.kind {
		case wire.KindNoul:
			storeAnswer(b, p, f, NoulAnswer{w: a.Noul, present: true})
		case wire.KindChoice:
			if at, bad := undeclaredOption(&a.Choice, f.options); bad {
				return typedError(resp, endpoint, r, f.name, at, errTypedOption)
			}
			storeAnswer(b, p, f, ChoiceAnswer{w: a.Choice, present: true})
		default: // wire.KindScore: buildPlan gives every field one of the three kinds
			if at, bad := undeclaredLevel(&a.Score, uint64(len(f.levels))); bad {
				return typedError(resp, endpoint, r, f.name, at, errTypedLevel)
			}
			storeAnswer(b, p, f, ScoreAnswer{w: a.Score, present: true})
		}
	}
	return nil
}

// storeRefusal is the panic value of a typed store that refuses to write an
// answer of n bytes at offset off into the field f of a struct of size
// bytes, the T that p decodes (decodeas_store.go, invariant 6): the write
// would not cover exactly the bytes the plan recorded for f inside T. Its
// message, built only when the panic is printed or read, names T, the field
// and the offset, and says what the store compared.
type storeRefusal struct {
	p            *typedPlan
	f            *typedField
	off, n, size uintptr
}

// Error returns the refusal's message.
func (e storeRefusal) Error() string {
	f := e.f
	field := "#" + strconv.Itoa(f.index)
	if f.index >= 0 && f.index < e.p.typ.NumField() {
		field = e.p.typ.Field(f.index).Name
	}
	return "decision: the typed store refused to write field " + field + " (a " + answerTypeName(f.kind) + ", question " + strconv.Quote(f.name) + ") of " +
		e.p.typ.String() + " at offset " + utoa(e.off) + ": a " + utoa(e.n) + "-byte write there would cover bytes [" + utoa(e.off) + ", " + utoa(e.off+e.n) +
		") of the struct's " + utoa(e.size) + ", but the plan recorded the field's end at " + utoa(f.end) + "; the plan is corrupt"
}

// utoa formats v in decimal, for the messages of a store refusal and of a
// corrupt plan.
func utoa(v uintptr) string { return strconv.FormatUint(uint64(v), 10) }

// undeclaredOption returns the path, below the answer, of the first option
// that a names and options does not list: the pick itself first, then the
// option of each probability in the order the answer lists them. bad is
// false when options lists every option a names.
func undeclaredOption(a *wire.ChoiceAnswer, options []string) (at codec.FieldPath, bad bool) {
	if !slices.Contains(options, a.Choice) {
		return codec.FieldPath{Member: "choice"}, true
	}
	for _, p := range a.Probabilities {
		if !slices.Contains(options, p.Label) {
			return codec.FieldPath{Member: "probabilities", Key: p.Label, HasKey: true}, true
		}
	}
	return codec.FieldPath{}, false
}

// undeclaredLevel returns the path, below the answer, of the first level
// that a names and that is not below levels, the number of levels its
// field lists: the legend first, then the probabilities, each in the order
// the answer lists them, as the Python SDK validates a score's members in
// its schema's order. The level is written in decimal, whatever spelling
// the body gave it ("01" is 1). bad is false when every level a names is
// below levels.
func undeclaredLevel(a *wire.ScoreAnswer, levels uint64) (at codec.FieldPath, bad bool) {
	for _, e := range a.Legend {
		if uint64(e.Level) >= levels {
			return codec.FieldPath{Member: "legend", Key: strconv.FormatUint(uint64(e.Level), 10), HasKey: true}, true
		}
	}
	for _, p := range a.Probabilities {
		if uint64(p.Level) >= levels {
			return codec.FieldPath{Member: "probabilities", Key: strconv.FormatUint(uint64(p.Level), 10), HasKey: true}, true
		}
	}
	return codec.FieldPath{}, false
}

// typedError returns the *ResponseValidationError for the answer called
// name that does not fit its field: at is the path below the answer (its
// Member and Key; the rest is filled in here), reason why it does not fit.
// The error carries resp's HTTP metadata and endpoint; r redacts its header
// ([newResponseValidationError]). The decode error it wraps keeps the
// answer's place in the body, under "answers"; its FieldPath is that
// path's lifted form ([typedFieldPath]), rendered once.
func typedError(resp *SystemOneResponse, endpoint string, r engine.HeaderRedactor, name string, at codec.FieldPath, reason error) *ResponseValidationError {
	at.Top, at.Name, at.HasName = "answers", name, true
	return newResponseValidationErrorAt(resp.respMeta(), endpoint, r, &codec.DecodeError{Path: at, Err: reason}, typedFieldPath(at))
}

// typedFieldPath renders p, the path in the body of an answer that does not
// fit its field, as the Python SDK names it in a response model that lifts
// each answer out of "answers": the answer's name, then the member and the
// key, such as "tone.choice" or "quality.legend.3". The name and the key are
// escaped and cut as [renderFieldPath] writes the decoder's.
func typedFieldPath(p codec.FieldPath) string {
	t := pathText{limit: engine.MaxPathChars}
	t.name(p.Name)
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
