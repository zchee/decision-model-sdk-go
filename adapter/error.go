// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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

package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// Error is an Adapter failure that RoundTrip returns as a Go error: a
// provider timeout or connection failure after the retries, a call whose
// context deadline passed, or a closed Adapter. These are the exceptions
// upstream's system_one raises without a status
// (src/system_one_adapter/_client.py:488-495): TypeSafeAPITimeoutError,
// TypeSafeAPIConnectionError and the RuntimeError of a closed client.
//
// The root SDK wraps it in a *decision.TimeoutError (when Timeout is
// true) or a *decision.ConnectionError, and errors.As reaches it because no
// link of its chain prints a credential: the SDK replaces the cause of a
// transport error whose text holds the call's API key. So Error prints only
// a fixed text for every fmt verb (it implements fmt.Formatter), holds its
// Report by pointer, which fmt prints as an address, and wraps only a
// sentinel error. It implements net.Error.
type Error struct {
	// Kind classifies the failure.
	Kind ErrorKind
	// Provider is the name of the provider the call resolved, such as
	// "openai" or a WithProvider name; empty for a closed Adapter.
	Provider string
	// Model is the model name the call resolved; empty for a closed Adapter.
	Model string
	// Report is the call's usage and debug data, upstream's error.debug: the
	// attempts made until the failure. It is nil for a closed Adapter, as
	// no evaluation ran.
	Report *Report
	// cause is the sentinel a KindConnection Error wraps: one of
	// context.DeadlineExceeded, context.Canceled, os.ErrDeadlineExceeded,
	// io.ErrUnexpectedEOF, io.EOF and net.ErrClosed, or a syscall.Errno, the
	// values the SDK keeps through its replacement of a cause; nil for none.
	cause error
}

// Error returns "adapter: <kind> from <provider> model <model>", or
// "adapter: <kind>" when Provider and Model are both empty, and nothing
// else.
func (e *Error) Error() string {
	if e.Provider == "" && e.Model == "" {
		return "adapter: " + e.Kind.String()
	}
	return "adapter: " + e.Kind.String() + " from " + e.Provider + " model " + e.Model
}

// Unwrap returns the sentinel cause: context.DeadlineExceeded for
// KindTimeout, ErrClosed for KindClosed, and for KindConnection the
// sentinel found in the provider's error, or nil when it held none.
func (e *Error) Unwrap() error {
	switch e.Kind {
	case KindTimeout:
		return context.DeadlineExceeded
	case KindClosed:
		return ErrClosed
	}
	return e.cause
}

// Timeout reports whether e is a timeout, which the SDK maps to
// *decision.TimeoutError: true for KindTimeout only.
func (e *Error) Timeout() bool { return e.Kind == KindTimeout }

// Temporary returns false; it exists so that *Error is a net.Error.
func (e *Error) Temporary() bool { return false }

// Format prints Error() for every verb and flag, so that %v, %+v, %#v, %s,
// %q and any other verb print the same fixed text.
func (e *Error) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, e.Error()) }

// adapterReport returns the Report, for ReportFromError.
func (e *Error) adapterReport() *Report { return e.Report }

// ErrorKind classifies an Error.
type ErrorKind uint8

const (
	// KindTimeout is a provider request that timed out after the retries, or
	// a call whose context deadline passed; either carries the Report.
	KindTimeout ErrorKind = iota + 1
	// KindConnection is a provider that could not be reached after the
	// retries; it carries the Report.
	KindConnection
	// KindClosed is a call on a closed Adapter; it carries no Report.
	KindClosed
)

// String returns the word Error prints for k: "timeout", "connection
// failure" or "closed", and "ErrorKind(<n>)" for any other value.
func (k ErrorKind) String() string {
	switch k {
	case KindTimeout:
		return "timeout"
	case KindConnection:
		return "connection failure"
	case KindClosed:
		return "closed"
	}
	return "ErrorKind(" + strconv.Itoa(int(k)) + ")"
}

// ErrClosed is the cause of a KindClosed Error.
var ErrClosed = errors.New("adapter: the adapter is closed")
