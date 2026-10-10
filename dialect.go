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
	"bytes"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"

	"github.com/zchee/decision-model-sdk-go/internal/codec"
	"github.com/zchee/decision-model-sdk-go/internal/engine"
	"github.com/zchee/decision-model-sdk-go/internal/wire"
)

// Dialect rewrites a System One call between the SDK's wire and a vendor's,
// for a vendor whose endpoint does not take the System One request or does
// not answer with the System One response. A [Provider] carries it; the
// provider package's presets hold the dialects the SDK has been checked
// against.
//
// The client calls Request once per System One call, after it encoded the
// System One body and before it sends anything. body is that body, and
// questions the call's question set ([Prepared.Questions]). Request returns
// the vendor's request body, or nil to send body as it is (an empty out
// means the same as nil), and the [ResponseFunc] that translates the response of each attempt, or nil to
// read every response as System One. An error stops the call before
// anything is sent, as a [*DialectError]: the one Request returned, or one
// that wraps any other error. Every attempt of the call, retries included,
// sends the same out.
//
// body is valid only while Request runs: it is the call's pooled buffer,
// which another call reuses once Request has returned a body of its own.
// Neither out nor the ResponseFunc may keep body or a slice of it, and must
// not modify it; an out that shares body's memory is treated as nil, so the
// call sends body. body's capacity is its length, so an append to it
// allocates a new array. questions is shared by every call of the set and
// must not be modified.
//
// A Dialect value is shared by every concurrent call of the client: it
// keeps no state of its own between calls, and Request may run on several
// goroutines at once. What one call's ResponseFunc needs of its request, such
// as the question names or the option labels, it keeps itself.
//
// [Models.List] is not translated: a provider with a Dialect lists models
// only when its ModelsPath serves the System One listing.
type Dialect interface {
	Request(body []byte, questions []PreparedQuestion) (out []byte, respond ResponseFunc, err error)
}

// ResponseFunc translates the response of one attempt of a System One call
// from a vendor's wire to System One's. The client calls it once per attempt
// that read a whole response, of any status, after the response is logged
// (the record at [LevelTrace] holds the body as the vendor sent it) and
// before its status decides the outcome; a response over the size limit
// never reaches it. header is the response's header, to read and never to
// modify, and body the response body, which it must not modify and may
// return a slice of.
//
// On a 2xx response, a non-nil [Translated].Body replaces the body: the
// decoder reads it, and the response's [ResponseMeta.RawBody] and a
// [*ResponseValidationError]'s Body hold it. An error ends the attempt with
// a *ResponseValidationError holding the body as it arrived, whose FieldPath
// is the Path of a [*ResponsePathError] in the error's chain, or "." without
// one.
//
// On any other status the body is kept as it arrived, so that an
// [*APIError]'s Body keeps the vendor's members, and Translated.ErrorType
// fills the APIError's ErrorType when the body names none itself
// (detail.error_type). An error leaves the APIError as the body alone makes
// it.
type ResponseFunc func(status int, header http.Header, body []byte) (Translated, error)

// Translated is what a [ResponseFunc] returns for one attempt.
type Translated struct {
	// Body is the System One body of a 2xx response; nil keeps the body as
	// it arrived. It is ignored for any other status.
	Body []byte
	// ErrorType is the System One error_type that a response outside 2xx
	// stands for, such as "authentication_error", or "" for none. It is
	// ignored for a 2xx status.
	ErrorType string
}

// PreparedQuestion is the read-only view of one question of a [Prepared]
// set, as a [Dialect] reads it.
type PreparedQuestion struct {
	// Name is the question's name.
	Name string
	// Kind is the question's kind; for a raw question it is the kind its
	// type names, or none of the kinds for a type this version does not
	// model.
	Kind AnswerKind
	// Options lists a choice question's option labels in the order they
	// are sent; it is nil for the other kinds and for a raw question.
	Options []string
	// Levels lists a score question's levels, lowest first: a level's
	// index is its score. It is nil for the other kinds and for a raw
	// question.
	Levels []Content
	// Raw reports a question added with [Questions.Raw], which has no
	// Options or Levels whatever its Kind.
	Raw bool
}

// Questions returns the set's questions in the order they are sent, as the
// read-only view a [Dialect] reads. The view is built on the first call and
// shared by every later one, from every goroutine: the slice and the tables
// in it must not be modified. It returns nil for a nil p.
func (p *Prepared) Questions() []PreparedQuestion {
	if p == nil {
		return nil
	}
	view, _ := (*engine.Prepared)(p).View(questionView).([]PreparedQuestion)
	return view
}

// questionView builds the view [Prepared.Questions] returns from the set's
// tables, all levels in one allocation. The option tables are the set's own.
func questionView(w *wire.Prepared) any {
	entries := w.Entries()
	n := 0
	for i := range entries {
		n += len(entries[i].Levels)
	}
	levels := make([]Content, n)
	view := make([]PreparedQuestion, len(entries))
	for i := range entries {
		e := &entries[i]
		q := PreparedQuestion{Name: e.Name, Kind: AnswerKind(e.Kind), Options: e.Options, Raw: e.Raw}
		if len(e.Levels) > 0 {
			q.Levels, levels = levels[:len(e.Levels):len(e.Levels)], levels[len(e.Levels):]
			for j, c := range e.Levels {
				q.Levels[j] = Content{w: c, set: true}
			}
		}
		view[i] = q
	}
	return view
}

// DialectError reports a System One call that a provider's [Dialect]
// cannot write in the vendor's wire, such as a question of a form the
// vendor has no counterpart for. It is returned before anything is sent,
// and retrying the same call cannot fix it.
//
// Error renders "The <Provider> dialect cannot send this request:
// <Reason>", the provider's name escaped and cut at 128 characters and the
// reason at 200, as other text the SDK did not write. Unwrap returns the
// error a Dialect returned when it was not a *DialectError itself, or nil.
type DialectError struct {
	// Provider names the provider whose dialect refused the call.
	Provider string
	// Reason says what the vendor's wire cannot express.
	Reason string

	err error
}

// NewDialectError returns the [*DialectError] a [Dialect] outside this
// package returns from Request for a call the vendor's wire cannot express:
// provider names the provider, and reason what the call holds that the wire
// cannot.
func NewDialectError(provider, reason string) *DialectError {
	return &DialectError{Provider: provider, Reason: reason}
}

// Error returns "The <Provider> dialect cannot send this request: <Reason>".
func (e *DialectError) Error() string {
	return "The " + engine.SafeName(e.Provider) + " dialect cannot send this request: " + engine.SafeMessage(e.Reason)
}

// Unwrap returns the error the dialect returned, or nil.
func (e *DialectError) Unwrap() error { return e.err }

func (*DialectError) decisionError() {}

// ResponsePathError is the error a [ResponseFunc] returns to name the member
// of a vendor's response it could not translate: the
// [*ResponseValidationError] of the attempt takes its FieldPath from Path and
// wraps the ResponsePathError.
type ResponsePathError struct {
	// Path names the member in the vendor's body as a FieldPath does: member
	// names joined with "." and array indexes in brackets, such as
	// "answers[1].probabilities[0].value", or "" for the body as a whole.
	// The FieldPath escapes it and cuts it at 320 characters, as it is text
	// the SDK did not write.
	Path string
	// Err is the reason, such as a member that is missing or of the wrong
	// kind.
	Err error
}

// Error returns "<Path>: <Err>", with "." for an empty Path.
func (e *ResponsePathError) Error() string {
	path := e.Path
	if path == "" {
		path = "."
	}
	if e.Err == nil {
		return path
	}
	return path + ": " + e.Err.Error()
}

// Unwrap returns the reason.
func (e *ResponsePathError) Unwrap() error { return e.Err }

// applyDialect runs the dialect of cfg's provider, when it has one, over
// the call's encoded body: a vendor body of its own replaces body in rq, and
// body is released at once, since the call no longer sends it; the
// dialect's ResponseFunc becomes rq's Respond. The closures it sets are the
// only allocations of the dialect path; a client without a dialect returns
// at the first line.
func applyDialect(cfg *config, rq *engine.Request, body codec.Body, qs *Prepared) error {
	d, _ := cfg.Dialect.(Dialect)
	if d == nil {
		return nil
	}
	// The dialect's view of the body is capped at its length: an append to
	// it reallocates, and the pooled buffer's spare capacity, which a later
	// call writes, is out of its reach.
	b := body.Bytes()
	b = slices.Clip(b)
	out, respond, err := d.Request(b, qs.Questions())
	if err != nil {
		if de, ok := errors.AsType[*DialectError](err); ok {
			return de
		}
		return &DialectError{Provider: cfg.Provider, Reason: err.Error(), err: err}
	}
	if len(out) == 0 || overlaps(out, b) {
		// An empty body means "send the SDK's", as nil does; one that
		// shares the pooled buffer would be overwritten by the next call
		// that takes it.
		out = nil
	}
	if out != nil {
		body.Release()
		rq.Raw = out
		rq.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(out)), nil }
	}
	if respond != nil {
		rq.Respond = func(status int, h http.Header, b []byte) (engine.Translated, error) {
			t, err := respond(status, h, b)
			return engine.Translated(t), err
		}
	}
	return nil
}

// translateResponse runs rq's Respond over the response of one attempt,
// read whole and logged, and returns what the attempt returns: for a 2xx,
// the translated body in meta, or a *ResponseValidationError holding the
// body as it arrived; for any other status, the *APIError of the body as it
// arrived, its ErrorType filled from the translation when the body names
// none.
func translateResponse(rq *engine.Request, meta wire.ResponseMeta, red engine.HeaderRedactor, success bool) (wire.ResponseMeta, engine.HeaderRedactor, error) {
	t, err := rq.Respond(meta.Status, meta.Header, meta.Body)
	if !success {
		apiErr := newAPIError(&meta, rq.Endpoint, red)
		if err == nil && apiErr.ErrorType == "" {
			apiErr.ErrorType = t.ErrorType
		}
		return meta, red, apiErr
	}
	if err != nil {
		path := "."
		if pe, ok := errors.AsType[*ResponsePathError](err); ok && pe.Path != "" {
			path = string(engine.AppendSafeText(nil, pe.Path, engine.MaxPathChars, true))
		}
		return meta, red, newResponseValidationErrorAt(&meta, rq.Endpoint, red, err, path)
	}
	if t.Body != nil {
		meta.Body = t.Body
	}
	return meta, red, nil
}

// overlaps reports whether out starts inside body, which starts at index 0
// of its array, the pooled buffer of a call's encoded body, and whose
// capacity is capped at its length, as the dialect's view of it is. Under
// that invariant a slice that shares body's memory starts on one of body's
// elements: one that starts past them would have to reach the buffer's
// spare capacity, which a slice of a capped body cannot, and an append to
// it reallocates. The check compares out's first element's address with
// body's range as integers, at a constant cost whatever the buffer's size;
// it neither reads memory nor converts an address back to a pointer. Each
// address is taken from a pointer to the slice's first element rather than
// from the slice itself, which reflect.ValueOf would box in an allocation.
func overlaps(out, body []byte) bool {
	if cap(out) == 0 || len(body) == 0 {
		return false
	}
	base := reflect.ValueOf(&body[0]).Pointer()
	p := reflect.ValueOf(&out[:1][0]).Pointer()
	return p >= base && p-base < uintptr(len(body))
}
