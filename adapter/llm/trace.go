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

package llm

import (
	"bytes"
	"sync"
)

// Trace records one attempt's exchange for the Adapter's debug data: the API
// name and the request body a provider built, and the response body and
// finish reason it received. It ports upstream's record_request and
// record_response with the attempt their ContextVar holds
// (providers/base.py:23-25,156-167): the Adapter gives each attempt its own
// Trace in Request.Trace instead of a context variable.
//
// The record methods keep a copy of what they are given, as upstream keeps a
// deep copy, and a second call replaces what the first kept. A nil *Trace
// records nothing, as upstream records nothing outside an attempt. The read
// methods return copies, so a caller cannot change what the Trace holds. A
// Trace is safe for concurrent use and must not be copied after first use.
// It never holds a header.
type Trace struct {
	mu           sync.Mutex
	api          string
	request      []byte
	requested    bool
	response     []byte
	finishReason *string
	responded    bool
}

// RecordRequest records the API name and the request body before it is sent.
func (t *Trace) RecordRequest(api string, body []byte) {
	if t == nil {
		return
	}
	body = bytes.Clone(body)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.api, t.request, t.requested = api, body, true
}

// RecordResponse records the response body and its finish reason (nil for null) before it is checked.
func (t *Trace) RecordResponse(body []byte, finishReason *string) {
	if t == nil {
		return
	}
	body = bytes.Clone(body)
	finishReason = cloneString(finishReason)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.response, t.finishReason, t.responded = body, finishReason, true
}

// API returns the API name RecordRequest recorded, or "" when it was not
// called (upstream's debug_info.api, absent then).
func (t *Trace) API() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.api
}

// Request returns a copy of the request body RecordRequest recorded, and
// whether it was called (upstream's attempt member request, absent when it
// was not).
func (t *Trace) Request() ([]byte, bool) {
	if t == nil {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return bytes.Clone(t.request), t.requested
}

// Response returns a copy of the response body RecordResponse recorded, and
// whether it was called (upstream's llm_response).
func (t *Trace) Response() ([]byte, bool) {
	if t == nil {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return bytes.Clone(t.response), t.responded
}

// FinishReason returns a copy of the finish reason RecordResponse recorded,
// nil for null or when it was not called; Responded tells the two apart.
func (t *Trace) FinishReason() *string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneString(t.finishReason)
}

// Responded reports whether RecordResponse was called, so that
// debug_info.finish_reason is written (upstream writes it, null included,
// only from record_response).
func (t *Trace) Responded() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.responded
}

// cloneString returns a pointer to a copy of *s, or nil for nil.
func cloneString(s *string) *string {
	if s == nil {
		return nil
	}
	c := *s
	return &c
}
