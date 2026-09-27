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

package engine

import (
	"fmt"
	"unicode/utf8"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// BodyMember is one member a call adds to the top level of its request
// body, as the Python SDK's extra_body does: the member's name and its value.
type BodyMember struct {
	Key   string
	Value any
}

// The members every request body starts with, in this order.
const (
	memberState     = "state"
	memberModel     = "model"
	memberQuestions = "questions"
)

// content is the root package's Content as the body encoder reads it,
// through its methods, since this package cannot name the root package's
// types: IsZero reports content that was never set, and Text and JSON give
// its two forms (wire.Content's two fields), of which JSON is non-nil for a
// JSON object or array. R is the root package's RawJSON, which JSON returns.
type content[R ~[]byte] interface {
	IsZero() bool
	Text() string
	JSON() R
}

// FailureKind is what kept [EncodeBody] from building a body.
type FailureKind uint8

const (
	// FailNone means the body was built.
	FailNone FailureKind = iota
	// FailNoQuestions means the question set is nil or holds no question.
	FailNoQuestions
	// FailModel means the model is not valid UTF-8.
	FailModel
	// FailEncode means a member could not be encoded.
	FailEncode
)

// Failure is why [EncodeBody] built no body. It is returned by value, so a
// call that builds its body allocates nothing for it; the root package turns
// it into its public error: a *ConfigError for [FailNoQuestions] and
// [FailModel], an *InvalidRequestError naming the member for [FailEncode].
type Failure struct {
	Kind FailureKind
	// Key is the member that could not be encoded: "state", or the key of
	// the extra body member when Extra is set.
	Key   string
	Extra bool
	// Err is the encoder's error.
	Err error
}

// EncodeBody encodes the request body of one System One call into a pooled
// scratch buffer, for the root package's encodeBody. It returns the body
// holding the call's reference, which the caller drops with Release when the
// call returns.
//
// The body is the JSON object {"state":…,"model":…,"questions":…} followed
// by the members of extra, laid out as the Python SDK's {**body,
// **extra_body}: an extra member named "state", "model" or "questions"
// replaces that member's value where it stands, and the value it replaces is
// not encoded at all; any other name is appended in the order extra first
// names it. When extra names a member more than once, the last value is
// written.
//
// R and C are the root package's RawJSON and Content, which this package
// cannot name. The state, and an extra "state" too, must be text, a JSON
// object or an array: sonic writes it (codec.EncodeState); an R state, or
// the R a non-nil *R points to, is written as it is after
// codec.AppendRawState's check; a C is written as a question writes it.
// model is written as a JSON string and q's bytes as they are. Any other
// value may be any JSON value: sonic writes it (codec.EncodeValue), an R
// value as it is after codec.AppendRawValue's check, a C as a question
// writes it and an unset C as null. The root package's encodeBody documents
// the float spelling, map order and json.Marshaler rules (rulings R46, R55,
// R59, R61, K26, K27), which are this function's.
//
// It fails, with a [Failure] the root package turns into its error, when q
// is nil or holds no question, when model is not valid UTF-8, or when a
// member cannot be encoded. The configuration is checked first.
func EncodeBody[R ~[]byte, C content[R]](state any, model string, q *wire.Prepared, extra []BodyMember) (codec.Body, Failure) {
	if q == nil || len(q.Entries()) == 0 {
		return codec.Body{}, Failure{Kind: FailNoQuestions}
	}
	stateAt, modelAt, questionsAt := -1, -1, -1 // the last extra member that replaces each
	for i := range extra {
		switch extra[i].Key {
		case memberState:
			stateAt = i
		case memberModel:
			modelAt = i
		case memberQuestions:
			questionsAt = i
		}
	}
	if modelAt < 0 && !utf8.ValidString(model) {
		return codec.Body{}, Failure{Kind: FailModel}
	}

	body := codec.NewBody()
	buf := body.Buffer()
	*buf = append(*buf, `{"state":`...)
	var f Failure
	if stateAt < 0 {
		if err := AppendState[R, C](buf, state); err != nil {
			f = Failure{Kind: FailEncode, Key: memberState, Err: err}
		}
	} else {
		f = appendMember(buf, memberState, extra[stateAt].Value, AppendState[R, C])
	}
	if f.Kind == FailNone {
		*buf = append(*buf, `,"model":`...)
		if modelAt < 0 {
			*buf, _ = wire.AppendString(*buf, model) // valid UTF-8: cannot fail
		} else {
			f = appendMember(buf, memberModel, extra[modelAt].Value, appendValue[R, C])
		}
	}
	if f.Kind == FailNone {
		*buf = append(*buf, `,"questions":`...)
		if questionsAt < 0 {
			*buf = append(*buf, q.Questions...)
		} else {
			f = appendMember(buf, memberQuestions, extra[questionsAt].Value, appendValue[R, C])
		}
	}
	if f.Kind == FailNone {
		f = appendExtra[R, C](buf, extra)
	}
	if f.Kind != FailNone {
		body.Release()
		return codec.Body{}, f
	}
	*buf = append(*buf, '}')
	return body, Failure{}
}

// appendExtra appends the extra members other than the three that replace a
// built-in member, each as ,"key":value: in the order extra first names a
// key, with the value extra gives it last.
func appendExtra[R ~[]byte, C content[R]](buf *[]byte, extra []BodyMember) Failure {
	var last map[string]int // key -> index of its last member, while unwritten; nil for short lists
	if len(extra) > RepeatScanLimit {
		last = make(map[string]int, len(extra))
		for i := range extra {
			last[extra[i].Key] = i
		}
	}
next:
	for i := range extra {
		key := extra[i].Key
		if key == memberState || key == memberModel || key == memberQuestions {
			continue
		}
		j := i // the member whose value is written
		if last != nil {
			var unwritten bool
			if j, unwritten = last[key]; !unwritten {
				continue
			}
			delete(last, key)
		} else {
			for k := range i {
				if extra[k].Key == key {
					continue next // written at its first position
				}
			}
			for k := i + 1; k < len(extra); k++ {
				if extra[k].Key == key {
					j = k
				}
			}
		}
		*buf = append(*buf, ',')
		var err error
		if *buf, err = wire.AppendString(*buf, key); err != nil {
			return Failure{Kind: FailEncode, Key: key, Extra: true, Err: err}
		}
		*buf = append(*buf, ':')
		if err := appendValue[R, C](buf, extra[j].Value); err != nil {
			return Failure{Kind: FailEncode, Key: key, Extra: true, Err: err}
		}
	}
	return Failure{}
}

// appendMember appends the value of the extra member key, which replaces a
// built-in member, with write, reporting a failure as that member's: the one
// failure site of the three built-in members.
func appendMember(buf *[]byte, key string, value any, write func(*[]byte, any) error) Failure {
	if err := write(buf, value); err != nil {
		return Failure{Kind: FailEncode, Key: key, Extra: true, Err: err}
	}
	return Failure{}
}

// AppendState appends a request state: text, a JSON object or an array. R
// and C are the root package's RawJSON and Content ([EncodeBody]).
func AppendState[R ~[]byte, C content[R]](buf *[]byte, state any) error {
	switch v := state.(type) {
	case R:
		return codec.AppendRawState(buf, []byte(v))
	case *R:
		if v == nil {
			return fmt.Errorf("nil *RawJSON holds no JSON value, %w", codec.ErrStateShape)
		}
		return codec.AppendRawState(buf, []byte(*v))
	case C:
		if v.IsZero() {
			return fmt.Errorf("unset Content encodes as null, %w", codec.ErrStateShape)
		}
		var err error
		*buf, err = wire.AppendContent(*buf, wire.Content{Text: v.Text(), JSON: []byte(v.JSON())})
		return err
	default:
		return codec.EncodeState(buf, state)
	}
}

// appendValue appends any JSON value, for a body member other than the
// state. R and C are the root package's RawJSON and Content ([EncodeBody]).
func appendValue[R ~[]byte, C content[R]](buf *[]byte, value any) error {
	switch v := value.(type) {
	case R:
		return codec.AppendRawValue(buf, []byte(v))
	case *R:
		if v == nil {
			return fmt.Errorf("nil *RawJSON holds no JSON value, %w", codec.ErrRawValue)
		}
		return codec.AppendRawValue(buf, []byte(*v))
	case C:
		if v.IsZero() {
			*buf = append(*buf, "null"...)
			return nil
		}
		var err error
		*buf, err = wire.AppendContent(*buf, wire.Content{Text: v.Text(), JSON: []byte(v.JSON())})
		return err
	default:
		return codec.EncodeValue(buf, value)
	}
}
