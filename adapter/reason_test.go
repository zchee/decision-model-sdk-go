// Copyright 2026 The typesafe-sdk-go Authors.
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
	"net/http"
	"strings"
	"testing"
	"unicode"

	"github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/adapter/llm"
)

// TestStatusMessage checks the text of a provider status error's retry
// reason against str() of typesafe-sdk-python's TypeSafeAPIError built by
// api_error(status, body, headers) for the same response
// (_core/errors.py:40-105), each want measured with typesafe-sdk 0.7.0,
// httpx2 2.13.0, pydantic-core 2.46.4 and Python 3.14.3.
//
// The body given to api_error was made from the response bytes as the
// Python SDKs of the three providers, at the versions upstream locks, make
// it: None for no body; else the bytes decoded with the "replace" error
// handler and stripped, then json.loads of that text, or the text itself
// when it is not JSON. The cases named "oracle", "oracle 2" and "strip" are
// three sets of bodies measured that way for this port, those named
// "vendor" were sent through the provider SDKs themselves against a local
// server, and the rest are this file's own; every want was run again for
// this table. For the rows "oracle: nested error.message" and "oracle:
// request id header" the first set also records the text of an earlier
// revision of the port, which was read from its code and not run; that
// text is not used here.
func TestStatusMessage(t *testing.T) {
	tests := map[string]struct {
		status int
		header http.Header
		noBody bool // a nil Body
		body   string
		want   string
	}{
		"success: oracle: error.message":                                 {status: 503, body: "{\"error\":{\"message\":\"boom\"}}", want: "503 boom"},
		"success: oracle: message":                                       {status: 429, body: "{\"message\":\"slow down\"}", want: "429 slow down"},
		"success: oracle: detail string":                                 {status: 500, body: "{\"detail\":\"d\"}", want: "500 d"},
		"success: oracle: detail list":                                   {status: 500, body: "{\"detail\":[{\"loc\":[\"body\",\"q\"],\"msg\":\"bad\"},{\"msg\":\"worse\"}]}", want: "500 q: bad; worse"},
		"success: oracle: error string":                                  {status: 408, body: "{\"error\":\"timeout\"}", want: "408 timeout"},
		"success: oracle: non-compact JSON, no message":                  {status: 503, body: "{ \"x\" : 1 }", want: "503 {\"x\":1}"},
		"success: oracle: nested error.message":                          {status: 400, body: "{\"error\":{\"type\":\"invalid\",\"message\":\"bad request\"}}", want: "400 bad request"},
		"success: oracle: no message member":                             {status: 503, body: "{\"m\":\"unavailable\"}", want: "503 {\"m\":\"unavailable\"}"},
		"success: oracle: not JSON":                                      {status: 502, body: "oops", want: "502 oops"},
		"success: oracle: long, no message":                              {status: 503, body: "{\"x\":\"" + strings.Repeat("y", 300) + "\"}", want: "503 {\"x\":\"" + strings.Repeat("y", 194) + "\xe2\x80\xa6"},
		"success: oracle: request id header":                             {status: 503, header: http.Header{"X-Typesafe-Request-Id": {"req_1"}}, body: "{\"message\":\"x\"}", want: "503 x (request_id=req_1)"},
		"success: oracle: no body":                                       {status: 503, noBody: true, want: "503 status code (no body)"},
		"success: oracle: empty body":                                    {status: 503, body: "", want: "503"},
		"success: oracle 2: json-string-empty":                           {status: 503, body: "\"\"", want: "503"},
		"success: oracle 2: json-string":                                 {status: 503, body: "\"hi\"", want: "503 hi"},
		"success: oracle 2: json-string-ws":                              {status: 503, body: "  \"hi\"  ", want: "503 hi"},
		"success: oracle 2: json-null":                                   {status: 503, body: "null", want: "503 status code (no body)"},
		"success: oracle 2: json-true":                                   {status: 503, body: "true", want: "503 true"},
		"success: oracle 2: json-int":                                    {status: 503, body: "42", want: "503 42"},
		"success: oracle 2: json-float":                                  {status: 503, body: "1.5", want: "503 1.5"},
		"success: oracle 2: json-array":                                  {status: 503, body: "[1,2]", want: "503 [1,2]"},
		"success: oracle 2: json-array-objs":                             {status: 503, body: "[{\"message\":\"a\"}]", want: "503 [{\"message\":\"a\"}]"},
		"success: oracle 2: json-empty-object":                           {status: 503, body: "{}", want: "503 {}"},
		"success: oracle 2: float-in-body":                               {status: 503, body: "{\"x\":1.0,\"y\":1e20,\"z\":1e-7}", want: "503 {\"x\":1.0,\"y\":1e+20,\"z\":1e-7}"},
		"success: oracle 2: huge-float-in-body":                          {status: 503, body: "{\"x\":1e400}", want: "503 {\"x\":Infinity}"},
		"success: oracle 2: neg-huge-float":                              {status: 503, body: "{\"x\":-1e400}", want: "503 {\"x\":-Infinity}"},
		"success: oracle 2: big-int":                                     {status: 503, body: "{\"x\":123456789012345678901234567890}", want: "503 {\"x\":123456789012345678901234567890}"},
		"success: oracle 2: dup-keys":                                    {status: 503, body: "{\"message\":\"first\",\"message\":\"second\"}", want: "503 second"},
		"success: oracle 2: dup-keys-nomsg":                              {status: 503, body: "{\"a\":1,\"a\":2}", want: "503 {\"a\":2}"},
		"success: oracle 2: escape-unicode":                              {status: 503, body: "{\"x\":\"\\u00e9\\u2028<>&\"}", want: "503 {\"x\":\"\xc3\xa9\xe2\x80\xa8<>&\"}"},
		"success: oracle 2: message-escape":                              {status: 503, body: "{\"message\":\"a\\nb\\u00e9\"}", want: "503 a\x0ab\xc3\xa9"},
		"success: oracle 2: non-ascii-long":                              {status: 503, body: "{\"x\":\"" + strings.Repeat("\xc3\xa9", 300) + "\"}", want: "503 {\"x\":\"" + strings.Repeat("\xc3\xa9", 194) + "\xe2\x80\xa6"},
		"success: oracle 2: non-ascii-long-msg":                          {status: 503, body: "{\"message\":\"" + strings.Repeat("\xe3\x81\x82", 250) + "\"}", want: "503 " + strings.Repeat("\xe3\x81\x82", 250)},
		"success: oracle 2: long-message-exact200":                       {status: 503, body: "{\"message\":\"" + strings.Repeat("a", 200) + "\"}", want: "503 " + strings.Repeat("a", 200)},
		"success: oracle 2: long-message-201":                            {status: 503, body: "{\"message\":\"" + strings.Repeat("a", 201) + "\"}", want: "503 " + strings.Repeat("a", 201)},
		"success: oracle 2: long-text-201":                               {status: 503, body: strings.Repeat("t", 201), want: "503 " + strings.Repeat("t", 201)},
		"success: oracle 2: message-empty":                               {status: 503, body: "{\"message\":\"\"}", want: "503 {\"message\":\"\"}"},
		"success: oracle 2: message-nonstring":                           {status: 503, body: "{\"message\":42}", want: "503 {\"message\":42}"},
		"success: oracle 2: message-null":                                {status: 503, body: "{\"message\":null}", want: "503 {\"message\":null}"},
		"success: oracle 2: error-message-empty":                         {status: 503, body: "{\"error\":{\"message\":\"\"}}", want: "503 {\"error\":{\"message\":\"\"}}"},
		"success: oracle 2: error-message-int":                           {status: 503, body: "{\"error\":{\"message\":7}}", want: "503 {\"error\":{\"message\":7}}"},
		"success: oracle 2: error-obj-no-message":                        {status: 503, body: "{\"error\":{\"type\":\"x\"}}", want: "503 {\"error\":{\"type\":\"x\"}}"},
		"success: oracle 2: error-list":                                  {status: 503, body: "{\"error\":[\"a\"]}", want: "503 {\"error\":[\"a\"]}"},
		"success: oracle 2: detail-empty-list":                           {status: 500, body: "{\"detail\":[]}", want: "500 {\"detail\":[]}"},
		"success: oracle 2: detail-list-loc-int":                         {status: 500, body: "{\"detail\":[{\"loc\":[\"body\",0,\"q\"],\"msg\":\"bad\"}]}", want: "500 0.q: bad"},
		"success: oracle 2: detail-list-loc-empty":                       {status: 500, body: "{\"detail\":[{\"loc\":[],\"msg\":\"bad\"}]}", want: "500 bad"},
		"success: oracle 2: detail-list-no-msg":                          {status: 500, body: "{\"detail\":[{\"loc\":[\"a\"]}]}", want: "500 {\"detail\":[{\"loc\":[\"a\"]}]}"},
		"success: oracle 2: detail-list-strings":                         {status: 500, body: "{\"detail\":[\"x\",\"y\"]}", want: "500 {\"detail\":[\"x\",\"y\"]}"},
		"success: oracle 2: detail-list-loc-str":                         {status: 500, body: "{\"detail\":[{\"loc\":\"body\",\"msg\":\"bad\"}]}", want: "500 bad"},
		"success: oracle 2: detail-list-loc-float":                       {status: 500, body: "{\"detail\":[{\"loc\":[1.5,true,null],\"msg\":\"bad\"}]}", want: "500 1.5.True.None: bad"},
		"success: oracle 2: detail-int":                                  {status: 500, body: "{\"detail\":5}", want: "500 {\"detail\":5}"},
		"success: oracle 2: detail-and-message":                          {status: 500, body: "{\"message\":\"m\",\"detail\":\"d\"}", want: "500 m"},
		"success: oracle 2: error-and-message":                           {status: 500, body: "{\"message\":\"m\",\"error\":{\"message\":\"e\"}}", want: "500 e"},
		"success: oracle 2: invalid-json":                                {status: 502, body: "{oops", want: "502 {oops"},
		"success: oracle 2: trailing-garbage":                            {status: 502, body: "{\"message\":\"x\"} y", want: "502 {\"message\":\"x\"} y"},
		"success: oracle 2: bom-json":                                    {status: 503, body: "\xef\xbb\xbf{\"message\":\"x\"}", want: "503 \xef\xbb\xbf{\"message\":\"x\"}"},
		"success: oracle 2: invalid-utf8":                                {status: 502, body: "\xff\xfe", want: "502 \xef\xbf\xbd\xef\xbf\xbd"},
		"success: oracle 2: nan-literal":                                 {status: 503, body: "{\"x\":NaN}", want: "503 {\"x\":NaN}"},
		"success: oracle 2: infinity-literal":                            {status: 503, body: "{\"x\":Infinity}", want: "503 {\"x\":Infinity}"},
		"success: vendor: body 0x202020":                                 {status: 503, body: "   ", want: "503"},
		"success: vendor: body 0x0a":                                     {status: 503, body: "\x0a", want: "503"},
		"success: vendor: body oops":                                     {status: 503, body: "oops", want: "503 oops"},
		"success: vendor: body 0x20206f6f70732020":                       {status: 503, body: "  oops  ", want: "503 oops"},
		"success: vendor: body {'message':'x'}":                          {status: 503, body: "{\"message\":\"x\"}", want: "503 x"},
		"success: vendor: body 0x20207b226d657373616765223a2278227d2020": {status: 503, body: "  {\"message\":\"x\"}  ", want: "503 x"},
		"success: vendor: body 0x7b226d657373616765223a22ff227d":         {status: 503, body: "{\"message\":\"\xff\"}", want: "503 \xef\xbf\xbd"},
		"success: vendor: body 0xff":                                     {status: 503, body: "\xff", want: "503 \xef\xbf\xbd"},
		"success: vendor: body [[[[[[...":                                {status: 503, body: strings.Repeat("[", 60000) + strings.Repeat("]", 60000), want: "503 " + strings.Repeat("[", 60000) + strings.Repeat("]", 60000)},
		"success: strip: 1c206f6f7073201f":                               {status: 503, body: "\x1c oops \x1f", want: "503 oops"},
		"success: strip: c2a06f6f7073e280a8":                             {status: 503, body: "\xc2\xa0oops\xe2\x80\xa8", want: "503 oops"},
		"success: strip: e380807b226d657373616765223a2278227dc285":       {status: 503, body: "\xe3\x80\x80{\"message\":\"x\"}\xc2\x85", want: "503 x"},
		"success: strip: efbbbf6f6f7073":                                 {status: 503, body: "\xef\xbb\xbfoops", want: "503 \xef\xbb\xbfoops"},
		"success: strip: 0b0c6f6f70730d0a":                               {status: 503, body: "\x0b\x0coops\x0d\x0a", want: "503 oops"},
		"success: detail object message":                                 {status: 400, body: "{\"detail\":{\"message\":\"dm\"}}", want: "400 dm"},
		"success: error before message":                                  {status: 400, body: "{\"message\":\"m\",\"error\":\"e\"}", want: "400 e"},
		"success: error object without a message":                        {status: 400, body: "{\"error\":{\"code\":1},\"message\":\"fallback\"}", want: "400 fallback"},
		"success: empty error string stops the search":                   {status: 400, body: "{\"error\":\"\",\"message\":\"m\"}", want: "400 {\"error\":\"\",\"message\":\"m\"}"},
		"success: detail list item kinds":                                {status: 422, body: "{\"detail\":[{\"loc\":[\"body\",0,1.5,true,false,null,\"a\"],\"msg\":\"m1\"},{\"loc\":\"notalist\",\"msg\":\"m2\"},{\"loc\":[],\"msg\":\"m3\"},{\"loc\":[\"body\"],\"msg\":\"m4\"},{\"msg\":1},{\"x\":1},7]}", want: "422 0.1.5.True.False.None.a: m1; m2; m3; m4"},
		"success: detail list containers and numbers in loc":             {status: 422, body: "{\"detail\":[{\"loc\":[[\"a\",1],{\"k\":\"v\"},\"it's\",1e400,-0.0,1e-5,100000000000000000000],\"msg\":\"m\"}]}", want: "422 ['a', 1].{'k': 'v'}.it's.inf.-0.0.1e-05.100000000000000000000: m"},
		"success: detail list with no usable entry":                      {status: 422, body: "{\"detail\":[{\"x\":1}],\"message\":7}", want: "422 {\"detail\":[{\"x\":1}],\"message\":7}"},
		"success: loc removes only body itself":                          {status: 422, body: "{\"detail\":[{\"loc\":[\"Body\",\"body \",\"body\"],\"msg\":\"m\"}]}", want: "422 Body.body : m"},
		"success: loc of empty strings":                                  {status: 422, body: "{\"detail\":[{\"loc\":[\"\"],\"msg\":\"a\"},{\"loc\":[\"body\",\"\"],\"msg\":\"b\"},{\"loc\":[\"\",\"\"],\"msg\":\"c\"}]}", want: "422 a; b; .: c"},
		"success: repr quoting":                                          {status: 422, body: "{\"detail\":[{\"loc\":[[\"it's\"],[\"say \\\"hi\\\"\"],[\"both ' \\\"\"],[\"back\\\\slash\"]],\"msg\":\"m\"}]}", want: "422 [\"it's\"].['say \"hi\"'].['both \\' \"'].['back\\\\slash']: m"},
		"success: repr escapes":                                          {status: 422, body: "{\"detail\":[{\"loc\":[[\"\\t\\n\\r\\u0001\\u007f\\u0080\\u00a0\\u00e9\\u200b\\u2028\\ud83d\\ude00\\ue000\\u0378\"]],\"msg\":\"m\"}]}", want: "422 ['\\t\\n\\r\\x01\\x7f\\x80\\xa0\xc3\xa9\\u200b\\u2028\xf0\x9f\x98\x80\\ue000\\u0378']: m"},
		"success: repr escapes above U+FFFF":                             {status: 422, body: "{\"detail\":[{\"loc\":[[\"\\udb40\\udc01\\udb80\\udc00\\udbff\\udfff\\ud83d\\ude00\"]],\"msg\":\"m\"}]}", want: "422 ['\\U000e0001\\U000f0000\\U0010ffff\xf0\x9f\x98\x80']: m"},
		"success: repr nested containers":                                {status: 422, body: "{\"detail\":[{\"loc\":[{\"a\":[1,2.5,true,null,{\"b\":\"c\"}],\"d\":-0}],\"msg\":\"m\"}]}", want: "422 {'a': [1, 2.5, True, None, {'b': 'c'}], 'd': 0}: m"},
		"success: repr empty containers":                                 {status: 422, body: "{\"detail\":[{\"loc\":[[],{}],\"msg\":\"m\"}]}", want: "422 [].{}: m"},
		"success: numbers written as pydantic-core writes them":          {status: 503, body: "{\"a\":1.50,\"b\":1E5,\"c\":1e-7,\"d\":-0.0,\"e\":12345678901234567890123}", want: "503 {\"a\":1.5,\"b\":100000.0,\"c\":1e-7,\"d\":-0.0,\"e\":12345678901234567890123}"},
		"success: long text body is not cut":                             {status: 502, body: strings.Repeat("y", 300), want: "502 " + strings.Repeat("y", 300)},
		"success: cut counts code points":                                {status: 503, body: "{\"x\":\"" + strings.Repeat("\xf0\x9f\x98\x80", 300) + "\"}", want: "503 {\"x\":\"" + strings.Repeat("\xf0\x9f\x98\x80", 194) + "\xe2\x80\xa6"},
		"success: 200 characters are not cut":                            {status: 503, body: "{\"x\":\"" + strings.Repeat("y", 192) + "\"}", want: "503 {\"x\":\"" + strings.Repeat("y", 192) + "\"}"},
		"success: 201 characters are cut":                                {status: 503, body: "{\"x\":\"" + strings.Repeat("y", 193) + "\"}", want: "503 {\"x\":\"" + strings.Repeat("y", 193) + "\"\xe2\x80\xa6"},
		"success: truncated sequence gives one replacement":              {status: 502, body: "a\xe2\x82b", want: "502 a\xef\xbf\xbdb"},
		"success: overlong and surrogate bytes":                          {status: 502, body: "\xc0\xaf\xed\xa0\x80\xf4\x90\x80\x80z", want: "502 \xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbdz"},
		"success: truncated four-byte sequence":                          {status: 502, body: "a\xf0\x90\x80z", want: "502 a\xef\xbf\xbdz"},
		"success: truncated sequence after F4":                           {status: 502, body: "a\xf4\x8f\xbfz\xe0\xa0", want: "502 a\xef\xbf\xbdz\xef\xbf\xbd"},
		"success: overlong sequences after E0 and F0":                    {status: 502, body: "\xe0\x80\x80|\xf0\x8f\xbf\xbf|", want: "502 \xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd|\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd|"},
		"success: replacement inside a JSON message":                     {status: 502, body: "{\"message\":\"a\xe3\x81b\"}", want: "502 a\xef\xbf\xbdb"},
		"success: white space inside the text is kept":                   {status: 502, body: " a \x09 b ", want: "502 a \x09 b"},
		"success: JSON white space around a JSON string":                 {status: 503, body: "\x09\x0d\x0a \"s\" \x0a", want: "503 s"},
		"success: text of only a byte order mark":                        {status: 503, body: "\xef\xbb\xbf", want: "503 \xef\xbb\xbf"},
		"success: value nested 255 levels is written":                    {status: 503, body: strings.Repeat("[", 254) + "1" + strings.Repeat("]", 254), want: "503 " + strings.Repeat("[", 200) + "\xe2\x80\xa6"},
		"success: message beside a value nested 300 levels":              {status: 503, body: "{\"message\":\"deep\",\"v\":" + strings.Repeat("[", 300) + strings.Repeat("]", 300) + "}", want: "503 deep"},
		"success: integer literal of 4301 digits":                        {status: 503, body: "{\"n\":1" + strings.Repeat("0", 4300) + "}", want: "503 {\"n\":1" + strings.Repeat("0", 4300) + "}"},
		"success: no body with a request id":                             {status: 503, header: http.Header{"X-Typesafe-Request-Id": {"req_2"}}, noBody: true, want: "503 status code (no body) (request_id=req_2)"},
		"success: empty request id":                                      {status: 503, header: http.Header{"X-Typesafe-Request-Id": {""}}, body: "{\"message\":\"x\"}", want: "503 x (request_id=)"},
		"success: two request ids":                                       {status: 503, header: http.Header{"X-Typesafe-Request-Id": {"a", "b"}}, body: "{\"message\":\"x\"}", want: "503 x (request_id=a, b)"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := &llm.StatusError{StatusCode: tt.status, Header: tt.header}
			if !tt.noBody {
				e.Body = []byte(tt.body)
			}
			if diff := cmp.Diff(tt.want, statusMessage(e)); diff != "" {
				t.Errorf("statusMessage mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestStatusMessagePortLimits checks the bodies whose text DIFFERS FROM
// UPSTREAM'S: JSON that Python's json.loads reads and the port's reader
// refuses (a NaN, Infinity or -Infinity token; a string escape of a
// surrogate without its partner; more than 10000 nested arrays and
// objects), and a value with no message nested deeper than pydantic-core
// writes. The port prints the status and the stripped text. upstream is
// the text measured as in TestStatusMessage; raises marks a body for which
// upstream's api_error raises instead of giving a text.
func TestStatusMessagePortLimits(t *testing.T) {
	tests := map[string]struct {
		status   int
		body     string
		want     string
		upstream string
		raises   bool
	}{
		"success: oracle 2: deep-nesting":                                            {status: 503, body: strings.Repeat("[", 20000) + strings.Repeat("]", 20000), want: "503 " + strings.Repeat("[", 20000) + strings.Repeat("]", 20000), raises: true},
		"success: vendor: body {'message':'x','v':NaN}":                              {status: 503, body: "{\"message\":\"x\",\"v\":NaN}", want: "503 {\"message\":\"x\",\"v\":NaN}", upstream: "503 x"},
		"success: strip: 7b226d657373616765223a225c7564383030227d":                   {status: 503, body: "{\"message\":\"\\ud800\"}", want: "503 {\"message\":\"\\ud800\"}", upstream: "503 \xed\xa0\x80"},
		"success: strip: 7b226d657373616765223a22615c756438303062227d":               {status: 503, body: "{\"message\":\"a\\ud800b\"}", want: "503 {\"message\":\"a\\ud800b\"}", upstream: "503 a\xed\xa0\x80b"},
		"success: strip: 7b2278223a225c7564383030227d":                               {status: 503, body: "{\"x\":\"\\ud800\"}", want: "503 {\"x\":\"\\ud800\"}", raises: true},
		"success: strip: 7b226d657373616765223a2278222c2276223a496e66696e6974797d":   {status: 503, body: "{\"message\":\"x\",\"v\":Infinity}", want: "503 {\"message\":\"x\",\"v\":Infinity}", upstream: "503 x"},
		"success: strip: 7b226d657373616765223a2278222c2276223a2d496e66696e6974797d": {status: 503, body: "{\"message\":\"x\",\"v\":-Infinity}", want: "503 {\"message\":\"x\",\"v\":-Infinity}", upstream: "503 x"},
		"success: value nested 256 levels":                                           {status: 503, body: strings.Repeat("[", 255) + "1" + strings.Repeat("]", 255), want: "503 " + strings.Repeat("[", 255) + "1" + strings.Repeat("]", 255), raises: true},
		"success: 10001 nested arrays":                                               {status: 503, body: strings.Repeat("[", 10001) + strings.Repeat("]", 10001), want: "503 " + strings.Repeat("[", 10001) + strings.Repeat("]", 10001), raises: true},
		"success: lone surrogate in a replaced value":                                {status: 503, body: "{\"message\":\"\\ud800\",\"message\":\"kept\"}", want: "503 {\"message\":\"\\ud800\",\"message\":\"kept\"}", upstream: "503 kept"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := &llm.StatusError{StatusCode: tt.status, Body: []byte(tt.body)}
			if diff := cmp.Diff(tt.want, statusMessage(e)); diff != "" {
				t.Errorf("statusMessage mismatch (-want +got):\n%s", diff)
			}
			if !tt.raises && tt.want == tt.upstream {
				t.Errorf("want equals upstream's text %q: the case belongs in TestStatusMessage", tt.upstream)
			}
		})
	}
}

// TestDecodeReplace checks decodeReplace against Python's
// bytes.decode("utf-8", "replace") (CPython 3.14.3): one U+FFFD for each
// maximal ill-formed subpart, so a truncated sequence gives one and a
// sequence that could never be well formed gives one per byte.
func TestDecodeReplace(t *testing.T) {
	tests := map[string]struct {
		in   string
		want string
	}{
		"success: ff":         {in: "\xff", want: "\xef\xbf\xbd"},
		"success: fffe":       {in: "\xff\xfe", want: "\xef\xbf\xbd\xef\xbf\xbd"},
		"success: e381":       {in: "\xe3\x81", want: "\xef\xbf\xbd"},
		"success: e38178":     {in: "\xe3\x81x", want: "\xef\xbf\xbdx"},
		"success: f09080":     {in: "\xf0\x90\x80", want: "\xef\xbf\xbd"},
		"success: f090":       {in: "\xf0\x90", want: "\xef\xbf\xbd"},
		"success: eda080":     {in: "\xed\xa0\x80", want: "\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd"},
		"success: c0af":       {in: "\xc0\xaf", want: "\xef\xbf\xbd\xef\xbf\xbd"},
		"success: f4908080":   {in: "\xf4\x90\x80\x80", want: "\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd"},
		"success: 8080":       {in: "\x80\x80", want: "\xef\xbf\xbd\xef\xbf\xbd"},
		"success: e3":         {in: "\xe3", want: "\xef\xbf\xbd"},
		"success: 61e38182e3": {in: "a\xe3\x81\x82\xe3", want: "a\xe3\x81\x82\xef\xbf\xbd"},
		"success: 7b226d657373616765223a22e381227d":                     {in: "{\"message\":\"\xe3\x81\"}", want: "{\"message\":\"\xef\xbf\xbd\"}"},
		"success: 7b226d657373616765223a225c7564383030227d":             {in: "{\"message\":\"\\ud800\"}", want: "{\"message\":\"\\ud800\"}"},
		"success: 7b226d657373616765223a225c75643830305c7564633030227d": {in: "{\"message\":\"\\ud800\\udc00\"}", want: "{\"message\":\"\\ud800\\udc00\"}"},
		"success: 61f48fbf7ae0a0":                                       {in: "a\xf4\x8f\xbfz\xe0\xa0", want: "a\xef\xbf\xbdz\xef\xbf\xbd"},
		"success: e080807cf08fbfbf7c":                                   {in: "\xe0\x80\x80|\xf0\x8f\xbf\xbf|", want: "\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd|\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd|"},
		"success: 706c61696e20c3a9":                                     {in: "plain \xc3\xa9", want: "plain \xc3\xa9"},
		"success: 61c3":                                                 {in: "a\xc3", want: "a\xef\xbf\xbd"},
		"success: 61f180807a":                                           {in: "a\xf1\x80\x80z", want: "a\xef\xbf\xbdz"},
		"success: f3807adf":                                             {in: "\xf3\x80z\xdf", want: "\xef\xbf\xbdz\xef\xbf\xbd"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, decodeReplace([]byte(tt.in))); diff != "" {
				t.Errorf("decodeReplace(%q) mismatch (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

// TestIsPySpace checks isPySpace on every code point against the set
// Python's str.strip removes (CPython 3.14.3, 29 characters), and that the
// set is not unicode.IsSpace's.
func TestIsPySpace(t *testing.T) {
	pySpace := map[rune]bool{}
	for _, r := range []rune{0x0009, 0x000a, 0x000b, 0x000c, 0x000d, 0x001c, 0x001d, 0x001e, 0x001f, 0x0020, 0x0085, 0x00a0, 0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000} {
		pySpace[r] = true
	}
	if got, want := len(pySpace), 29; got != want {
		t.Fatalf("%d characters in the set, want %d", got, want)
	}
	differs := 0
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if got := isPySpace(r); got != pySpace[r] {
			t.Errorf("isPySpace(%U) = %t, want %t", r, got, pySpace[r])
		}
		if pySpace[r] != unicode.IsSpace(r) {
			differs++
		}
	}
	if got, want := differs, 4; got != want { // U+001C to U+001F
		t.Errorf("%d characters differ from unicode.IsSpace, want %d", got, want)
	}
}
