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

// ConfigError reports something the caller configured that the SDK cannot
// use: a question set that [Questions.Prepare] rejects, for example. It is
// returned before any request is sent, and retrying cannot fix it.
//
// Error returns the message. Unwrap returns the errors it wraps, if any: the
// cause of the failure, such as the JSON syntax error behind rejected
// content, which [errors.Is] and [errors.As] reach.
type ConfigError struct {
	msg  string
	errs []error
}

// newConfigError returns a *ConfigError with message msg that wraps errs.
func newConfigError(msg string, errs ...error) *ConfigError {
	return &ConfigError{msg: msg, errs: errs}
}

// Error returns the message.
func (e *ConfigError) Error() string { return e.msg }

// Unwrap returns the wrapped errors, or nil when there are none.
func (e *ConfigError) Unwrap() []error { return e.errs }

// InvalidRequestError reports a request the API cannot accept, which the SDK
// refuses to send: a state that is not text, a JSON object or an array, or a
// value that has no JSON form, such as NaN, a channel or a cyclic structure.
// It is returned before any request is sent, and retrying the same request
// cannot fix it.
//
// Error returns the message. Unwrap returns the cause, which [errors.Is] and
// [errors.As] reach.
type InvalidRequestError struct {
	msg string
	err error
}

// newInvalidRequestError returns an *InvalidRequestError with message msg
// that wraps err.
func newInvalidRequestError(msg string, err error) *InvalidRequestError {
	return &InvalidRequestError{msg: msg, err: err}
}

// Error returns the message.
func (e *InvalidRequestError) Error() string { return e.msg }

// Unwrap returns the cause, or nil when there is none.
func (e *InvalidRequestError) Unwrap() error { return e.err }
