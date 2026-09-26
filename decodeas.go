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
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// Ask asks the questions the struct type T declares about state and returns
// the answers as a T: it is [Client.SystemOne] with the question set
// [PreparedFor] builds for T, followed by [DecodeAs] of the response.
//
//	t, err := typesafe.Ask[Ticket](ctx, c, state)
//
// A T that PreparedFor refuses fails with its [*ConfigError] before any
// request is made. Every error of the call itself, an [*APIError], a
// [*ConnectionError], a [*TimeoutError], a [*ResponseValidationError] of the
// response as a whole and the others [Client.SystemOne] lists, is returned
// as SystemOne returns it. A response whose answers do not fit T fails as
// DecodeAs says, with the call's endpoint in the error and its header
// redacted as the call's other errors redact theirs: by the header's name,
// and a header whose value holds the client's API key.
//
// Ask returns only the answers. A caller that also needs the response,
// its request id, usage, model or raw body, makes the two calls Ask makes:
//
//	qs, err := typesafe.PreparedFor[Ticket]()
//	resp, err := c.SystemOne(ctx, state, qs)
//	t, err := typesafe.DecodeAs[Ticket](resp)
//	id, ok := resp.Meta().RequestID()
func Ask[T any](ctx context.Context, c *Client, state any, opts ...CallOption) (T, error) {
	p := typedPlanFor[T]()
	if p.err != nil {
		var zero T
		return zero, p.err
	}
	resp, err := c.SystemOne(ctx, state, p.prepared, opts...)
	if err != nil {
		var zero T
		return zero, err
	}
	return decodeTyped[T](p, resp, c.systemOneEndpoint, c.cfg.redactor())
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
// fields are read in their order in T, and the first that does not fit
// fails the whole decode: DecodeAs returns the zero T, never one holding
// the fields read before the failure, and a [*ResponseValidationError]
// whose FieldPath names the answer. The paths are the ones the Python SDK
// reports for a response model that holds the answers under "answers" (a
// pydantic response_model such as {"model": str, "answers": {"spam":
// NoulAnswer}}), except that an answer of a type this version does not
// model is skipped before T is filled, as a SystemOneResponse subclass
// skips it: under a required field's name it fails as absent
// (answers.spam, where that model says answers.spam.type), under an
// optional field's name it leaves the field absent, and a response without
// an "answers" member fails at T's first required field (answers.spam,
// where that model says answers):
//
//	the answer is absent and the field is not optional  answers.<name>
//	the answer is of another kind than the field        answers.<name>.type
//	a choice picks an option T does not list            answers.<name>.choice
//	a choice gives a probability to such an option      answers.<name>.probabilities.<option>
//	a score's legend has a level beyond T's levels      answers.<name>.legend.<level>
//	a score gives a probability to such a level         answers.<name>.probabilities.<level>
//
// A choice's options and a score's levels are the ones T's tag lists: a
// level counts from zero, so a score with three levels has the levels 0, 1
// and 2, and a level is written in decimal in the path. An option or a
// level the answer does not mention, and the score's value, are not
// checked. Python checks an option only when the response model's own type
// says so, with a Literal; it reports the choice above at "tone.choice" for
// a SystemOneResponse subclass, which lifts each answer out of "answers",
// and at "answers.tone.choice" for a model that keeps them there, as T
// does.
//
// The error carries resp's HTTP metadata: its status, header, request id
// and body when resp came from a request, and none of them when it was read
// back with [SystemOneResponse.UnmarshalJSON], whose Meta is empty. Its
// Header is a new map in which each value of a header that is a credential
// by its name, such as Authorization, Cookie or Set-Cookie
// ([APIError.Header] lists them), is "***"; the other headers' values are
// the response's own and must not be modified. DecodeAs has no client, so
// unlike [Ask] it cannot look for the client's API key: a key the server
// echoes in another header or in the request id stays visible in Header
// and Error, where Ask shows "***". Neither redacts FieldPath: the answer's
// name and the option or level it names are shown as they arrived, as the
// SDK's other errors show a path (ruling R103-rev). Its Endpoint is empty;
// Ask fills it in.
func DecodeAs[T any](resp *SystemOneResponse) (T, error) {
	return decodeTyped[T](typedPlanFor[T](), resp, "", headerRedactor{})
}

// decodeTyped decodes resp into a T by p, the plan of T, and names endpoint
// in a validation error, whose header r redacts. On failure it returns the
// zero T, not a T with the fields read before the failure.
func decodeTyped[T any](p *typedPlan, resp *SystemOneResponse, endpoint string, r headerRedactor) (T, error) {
	var t T
	if err := p.decode(resp, endpoint, r, reflect.ValueOf(&t).Elem()); err != nil {
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

// decode reads the answers of resp into v, a struct of the type p was built
// for, field by field in p's order, and names endpoint in a validation
// error, whose header r redacts. It returns p's own error for a refused
// type.
func (p *typedPlan) decode(resp *SystemOneResponse, endpoint string, r headerRedactor, v reflect.Value) error {
	if p.err != nil {
		return p.err
	}
	answers := &resp.res.Answers
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
		// The field's address, as an interface holding a pointer: no answer
		// is boxed, and the answer is stored through the pointer. It moves
		// the T being decoded to the heap, DecodeAs's one allocation;
		// reflect.Value.Set would move each answer there as well.
		dst := v.Field(f.index).Addr().Interface()
		switch f.kind {
		case wire.KindNoul:
			*dst.(*NoulAnswer) = NoulAnswer{w: a.Noul, present: true}
		case wire.KindChoice:
			if at, bad := undeclaredOption(&a.Choice, f.options); bad {
				return typedError(resp, endpoint, r, f.name, at, errTypedOption)
			}
			*dst.(*ChoiceAnswer) = ChoiceAnswer{w: a.Choice, present: true}
		default: // wire.KindScore: buildPlan gives every field one of the three kinds
			if at, bad := undeclaredLevel(&a.Score, uint64(len(f.levels))); bad {
				return typedError(resp, endpoint, r, f.name, at, errTypedLevel)
			}
			*dst.(*ScoreAnswer) = ScoreAnswer{w: a.Score, present: true}
		}
	}
	return nil
}

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
// The error carries resp's HTTP metadata and endpoint; r redacts its header,
// and the names in its path ([newResponseValidationError]).
func typedError(resp *SystemOneResponse, endpoint string, r headerRedactor, name string, at codec.FieldPath, reason error) *ResponseValidationError {
	at.Top, at.Name, at.HasName = "answers", name, true
	return newResponseValidationError(&resp.meta, endpoint, r, &codec.DecodeError{Path: at, Err: reason})
}
