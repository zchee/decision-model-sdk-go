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
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// bodyMember is one member a call adds to the top level of its request
// body, as the Python SDK's extra_body does: the member's name and its value.
type bodyMember struct {
	key   string
	value any
}

// The members every request body starts with, in this order.
const (
	memberState     = "state"
	memberModel     = "model"
	memberQuestions = "questions"
)

// encodeBody encodes the request body of one System One call into a pooled
// scratch buffer. It returns the body holding the call's reference, which
// the caller drops with Release when the call returns; [requestReaders]
// gives the transport its own references.
//
// The body is the JSON object {"state":…,"model":…,"questions":…} followed
// by the members of extra, laid out as the Python SDK's {**body,
// **extra_body}: an extra member named "state", "model" or "questions"
// replaces that member's value where it stands, and the value it replaces is
// not encoded at all; any other name is appended in the order extra first
// names it. When extra names a member more than once, the last value is
// written.
//
// The state, and an extra "state" too, must be text, a JSON object or an
// array: sonic writes it (codec.EncodeState); a [RawJSON] state, or the
// RawJSON a non-nil *RawJSON points to, is written as it is after
// codec.AppendRawState's check; [Content] is written as a question writes
// it. model is written as a JSON string and qs's bytes as
// they are. Any other value may be any JSON value: sonic writes it
// (codec.EncodeValue), a RawJSON value as it is after codec.AppendRawValue's
// check, Content as a question writes it and unset Content as null.
//
// A float inside a state that sonic writes keeps sonic's spelling (ruling
// R46), a consequence of section 6.1.2 encoding the state with sonic and of
// the owner's decision D1, not a choice of this function: 3.0 is written 3,
// -0.0 as 0, and 1e16 <= |x| < 1e21 and 1e-6 <= |x| < 1e-5 in fixed digits,
// where the Python SDK writes 3.0, -0.0 and e-notation. A state that needs an
// exact spelling is sent as RawJSON, or carries the number as a string. A
// map's members go out in Go's iteration order, which changes from one call
// to the next where Python keeps a dict's insertion order (ruling R55); the
// body of one call, and so every attempt of it, is encoded once. A struct or
// RawJSON gives stable bytes.
//
// It fails with a [*ConfigError] when qs is nil or holds no question (a
// Prepared that [Questions.Prepare] did not return), or when model is not
// valid UTF-8, and with an [*InvalidRequestError] when a member cannot be
// encoded. The configuration is checked first.
func encodeBody(state any, model string, qs *Prepared, extra []bodyMember) (codec.Body, error) {
	if qs == nil || qs.Len() == 0 {
		return codec.Body{}, newConfigError("At least one question is required.")
	}
	stateAt, modelAt, questionsAt := -1, -1, -1 // the last extra member that replaces each
	for i := range extra {
		switch extra[i].key {
		case memberState:
			stateAt = i
		case memberModel:
			modelAt = i
		case memberQuestions:
			questionsAt = i
		}
	}
	if modelAt < 0 && !utf8.ValidString(model) {
		return codec.Body{}, newConfigError("Model " + strconv.Quote(model) + " is not valid UTF-8.")
	}

	body := codec.NewBody()
	buf := body.Buffer()
	*buf = append(*buf, `{"state":`...)
	var err error
	if stateAt < 0 {
		if err = appendState(buf, state); err != nil {
			err = encodeError(memberState, err)
		}
	} else {
		err = appendMember(buf, memberState, extra[stateAt].value, appendState)
	}
	if err == nil {
		*buf = append(*buf, `,"model":`...)
		if modelAt < 0 {
			*buf, err = wire.AppendString(*buf, model) // valid UTF-8: cannot fail
		} else {
			err = appendMember(buf, memberModel, extra[modelAt].value, appendValue)
		}
	}
	if err == nil {
		*buf = append(*buf, `,"questions":`...)
		if questionsAt < 0 {
			*buf = append(*buf, qs.w.Questions...)
		} else {
			err = appendMember(buf, memberQuestions, extra[questionsAt].value, appendValue)
		}
	}
	if err == nil {
		err = appendExtra(buf, extra)
	}
	if err != nil {
		body.Release()
		return codec.Body{}, err
	}
	*buf = append(*buf, '}')
	return body, nil
}

// appendExtra appends the extra members other than the three that replace a
// built-in member, each as ,"key":value: in the order extra first names a
// key, with the value extra gives it last.
func appendExtra(buf *[]byte, extra []bodyMember) error {
	var last map[string]int // key -> index of its last member, while unwritten; nil for short lists
	if len(extra) > repeatScanLimit {
		last = make(map[string]int, len(extra))
		for i := range extra {
			last[extra[i].key] = i
		}
	}
next:
	for i := range extra {
		key := extra[i].key
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
				if extra[k].key == key {
					continue next // written at its first position
				}
			}
			for k := i + 1; k < len(extra); k++ {
				if extra[k].key == key {
					j = k
				}
			}
		}
		*buf = append(*buf, ',')
		var err error
		if *buf, err = wire.AppendString(*buf, key); err != nil {
			return encodeError("extra body member "+quote(key), err)
		}
		*buf = append(*buf, ':')
		if err := appendValue(buf, extra[j].value); err != nil {
			return encodeError("extra body member "+quote(key), err)
		}
	}
	return nil
}

// appendMember appends the value of the extra member key, which replaces a
// built-in member, with write, reporting a failure as that member's.
func appendMember(buf *[]byte, key string, value any, write func(*[]byte, any) error) error {
	if err := write(buf, value); err != nil {
		return encodeError("extra body member "+quote(key), err)
	}
	return nil
}

// appendState appends a request state: text, a JSON object or an array.
func appendState(buf *[]byte, state any) error {
	switch v := state.(type) {
	case RawJSON:
		return codec.AppendRawState(buf, v)
	case *RawJSON:
		if v == nil {
			return fmt.Errorf("nil *RawJSON holds no JSON value, %w", codec.ErrStateShape)
		}
		return codec.AppendRawState(buf, *v)
	case Content:
		if !v.set {
			return fmt.Errorf("unset Content encodes as null, %w", codec.ErrStateShape)
		}
		var err error
		*buf, err = wire.AppendContent(*buf, v.w)
		return err
	default:
		return codec.EncodeState(buf, state)
	}
}

// appendValue appends any JSON value, for a body member other than the
// state.
func appendValue(buf *[]byte, value any) error {
	switch v := value.(type) {
	case RawJSON:
		return codec.AppendRawValue(buf, v)
	case *RawJSON:
		if v == nil {
			return fmt.Errorf("nil *RawJSON holds no JSON value, %w", codec.ErrRawValue)
		}
		return codec.AppendRawValue(buf, *v)
	case Content:
		if !v.set {
			*buf = append(*buf, "null"...)
			return nil
		}
		var err error
		*buf, err = wire.AppendContent(*buf, v.w)
		return err
	default:
		return codec.EncodeValue(buf, value)
	}
}

// encodeError is the [*InvalidRequestError] for the body member that could
// not be encoded, named as the message names it.
func encodeError(member string, err error) *InvalidRequestError {
	msg := "The request body could not be encoded as JSON: " + member + ": " + err.Error()
	if errors.Is(err, codec.ErrPlainBytes) {
		msg += "; send string(b) for text or RawJSON(b) for JSON"
	}
	return newInvalidRequestError(msg, err)
}

// requestReaders returns what an http.Request carries to send body: a reader
// for its Body, the GetBody function a replay or a retry reads the same bytes
// again through, and its ContentLength. Every reader holds a reference to the
// body until the transport closes it, so the scratch buffer is reused only
// after the last one is closed and the call has dropped its own reference
// with Release. It fails with codec.ErrBodyReleased when the body is gone.
func requestReaders(body codec.Body) (io.ReadCloser, func() (io.ReadCloser, error), int64, error) {
	r, err := body.GetBody()
	if err != nil {
		return nil, nil, 0, err
	}
	return r, body.GetBody, int64(body.Len()), nil
}
