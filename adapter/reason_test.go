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
	"strconv"
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
		"success: repr escapes a character Unicode 16.0 lacks":           {status: 503, body: "{\"detail\":[{\"loc\":[[\"\\u088f\\u1acf\\ufbc3\"]],\"msg\":\"m\"}]}", want: "503 ['\\u088f\\u1acf\\ufbc3']: m"},
		"success: repr escapes one above U+FFFF Unicode 16.0 lacks":      {status: 503, body: "{\"detail\":[{\"loc\":[[\"\xf0\x90\xa5\x80\xf0\xb2\x8e\xb0\xf0\xb3\x91\xb9\"]],\"msg\":\"m\"}]}", want: "503 ['\\U00010940\\U000323b0\\U00033479']: m"},
		"success: repr keeps the neighbours of such characters":          {status: 503, body: "{\"detail\":[{\"loc\":[[\"\xe0\xa2\x8e\xf0\xb3\x91\xba\xf0\xb2\x8e\xaf\"]],\"msg\":\"m\"}]}", want: "503 ['\xe0\xa2\x8e\\U0003347a\xf0\xb2\x8e\xaf']: m"},
		"success: str of a loc item keeps such a character":              {status: 503, body: "{\"detail\":[{\"loc\":[\"\xe0\xa2\x8f\"],\"msg\":\"m\"}]}", want: "503 \xe0\xa2\x8f: m"},
		"success: repr escapes such a character in a dict key":           {status: 503, body: "{\"detail\":[{\"loc\":[{\"\xe2\x83\x81\":\"\xe2\xae\x96\"}],\"msg\":\"m\"}]}", want: "503 {'\\u20c1': '\\u2b96'}: m"},
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
		"success: 10001 nested arrays beside a message":                              {status: 503, body: "{\"message\":\"x\",\"d\":" + strings.Repeat("[", 10001) + strings.Repeat("]", 10001) + "}", want: "503 {\"message\":\"x\",\"d\":" + strings.Repeat("[", 10001) + strings.Repeat("]", 10001) + "}", upstream: "503 x"},
		"success: lone surrogate in a member name":                                   {status: 503, body: "{\"\\ud800\":1,\"message\":\"x\"}", want: "503 {\"\\ud800\":1,\"message\":\"x\"}", upstream: "503 x"},
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

// TestPyPrintableUnicodeVersion pins the Unicode version of the unicode
// package that notPyPrintable was measured against.
func TestPyPrintableUnicodeVersion(t *testing.T) {
	const measured = "17.0.0"
	if unicode.Version != measured {
		t.Fatalf("unicode.Version = %s, want %s: notPyPrintable holds the code points that unicode.IsPrint of Unicode %s accepts and CPython 3.14.3's str.isprintable refuses. Measure both directions again over every code point with this Go release, replace notPyPrintable and the counts in its godoc and in TestIsPyPrintable (and handle a code point that only str.isprintable accepts, if there is one now), then change this version", unicode.Version, measured, measured)
	}
}

// TestIsPyPrintable checks isPyPrintable on every code point against
// str.isprintable of CPython 3.14.3 (unicodedata.unidata_version 16.0.0),
// and that notPyPrintable is exactly the difference from unicode.IsPrint.
func TestIsPyPrintable(t *testing.T) {
	const (
		wantRanges     = 736
		wantPrintable  = 154810
		wantGoOnly     = 4803
		wantGoOnlyRuns = 47
	)
	want := make([]bool, unicode.MaxRune+1)
	ranges, printable, prev := 0, 0, int64(-2)
	for rg := range strings.FieldsSeq(pyPrintable) {
		first, last, _ := strings.Cut(rg, "-")
		lo, errLo := strconv.ParseInt(first, 16, 32)
		hi, errHi := strconv.ParseInt(last, 16, 32)
		if errLo != nil || errHi != nil || lo <= prev+1 || hi < lo || hi > unicode.MaxRune {
			t.Fatalf("range %q after U+%04X: want ascending ranges in hexadecimal with a gap between them", rg, prev)
		}
		for r := lo; r <= hi; r++ {
			want[r] = true
		}
		ranges++
		printable += int(hi-lo) + 1
		prev = hi
	}
	if ranges != wantRanges || printable != wantPrintable {
		t.Fatalf("pyPrintable holds %d code points in %d ranges, want %d in %d", printable, ranges, wantPrintable, wantRanges)
	}

	wrong, goOnly, pyOnly, inTable := 0, 0, 0, 0
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if got := isPyPrintable(r); got != want[r] {
			if wrong++; wrong <= 20 {
				t.Errorf("isPyPrintable(%U) = %t, str.isprintable gives %t", r, got, want[r])
			}
		}
		switch goPrint := unicode.IsPrint(r); {
		case goPrint && !want[r]:
			goOnly++
		case !goPrint && want[r]:
			pyOnly++
		}
		if unicode.Is(notPyPrintable, r) {
			inTable++
		}
	}
	if wrong > 0 {
		t.Errorf("isPyPrintable differs from str.isprintable on %d code points", wrong)
	}
	if goOnly != wantGoOnly || pyOnly != 0 {
		t.Errorf("unicode.IsPrint alone accepts %d code points and str.isprintable alone %d, want %d and 0", goOnly, pyOnly, wantGoOnly)
	}
	if inTable != wantGoOnly {
		t.Errorf("notPyPrintable holds %d code points, want %d", inTable, wantGoOnly)
	}
	if got := len(notPyPrintable.R16) + len(notPyPrintable.R32); got != wantGoOnlyRuns {
		t.Errorf("notPyPrintable holds %d ranges, want %d", got, wantGoOnlyRuns)
	}
}

// pyPrintable holds, as inclusive ranges "first-last" in hexadecimal and in
// ascending order, every code point for which CPython 3.14.3's
// str.isprintable is true: 154810 code points in 736 ranges.
const pyPrintable = `
20-7e a1-ac ae-377 37a-37f 384-38a 38c-38c 38e-3a1 3a3-52f 531-556 559-58a 58d-58f 591-5c7
5d0-5ea 5ef-5f4 606-61b 61d-6dc 6de-70d 710-74a 74d-7b1 7c0-7fa 7fd-82d 830-83e 840-85b 85e-85e
860-86a 870-88e 897-8e1 8e3-983 985-98c 98f-990 993-9a8 9aa-9b0 9b2-9b2 9b6-9b9 9bc-9c4 9c7-9c8
9cb-9ce 9d7-9d7 9dc-9dd 9df-9e3 9e6-9fe a01-a03 a05-a0a a0f-a10 a13-a28 a2a-a30 a32-a33 a35-a36
a38-a39 a3c-a3c a3e-a42 a47-a48 a4b-a4d a51-a51 a59-a5c a5e-a5e a66-a76 a81-a83 a85-a8d a8f-a91
a93-aa8 aaa-ab0 ab2-ab3 ab5-ab9 abc-ac5 ac7-ac9 acb-acd ad0-ad0 ae0-ae3 ae6-af1 af9-aff b01-b03
b05-b0c b0f-b10 b13-b28 b2a-b30 b32-b33 b35-b39 b3c-b44 b47-b48 b4b-b4d b55-b57 b5c-b5d b5f-b63
b66-b77 b82-b83 b85-b8a b8e-b90 b92-b95 b99-b9a b9c-b9c b9e-b9f ba3-ba4 ba8-baa bae-bb9 bbe-bc2
bc6-bc8 bca-bcd bd0-bd0 bd7-bd7 be6-bfa c00-c0c c0e-c10 c12-c28 c2a-c39 c3c-c44 c46-c48 c4a-c4d
c55-c56 c58-c5a c5d-c5d c60-c63 c66-c6f c77-c8c c8e-c90 c92-ca8 caa-cb3 cb5-cb9 cbc-cc4 cc6-cc8
cca-ccd cd5-cd6 cdd-cde ce0-ce3 ce6-cef cf1-cf3 d00-d0c d0e-d10 d12-d44 d46-d48 d4a-d4f d54-d63
d66-d7f d81-d83 d85-d96 d9a-db1 db3-dbb dbd-dbd dc0-dc6 dca-dca dcf-dd4 dd6-dd6 dd8-ddf de6-def
df2-df4 e01-e3a e3f-e5b e81-e82 e84-e84 e86-e8a e8c-ea3 ea5-ea5 ea7-ebd ec0-ec4 ec6-ec6 ec8-ece
ed0-ed9 edc-edf f00-f47 f49-f6c f71-f97 f99-fbc fbe-fcc fce-fda 1000-10c5 10c7-10c7 10cd-10cd 10d0-1248
124a-124d 1250-1256 1258-1258 125a-125d 1260-1288 128a-128d 1290-12b0 12b2-12b5 12b8-12be 12c0-12c0 12c2-12c5 12c8-12d6
12d8-1310 1312-1315 1318-135a 135d-137c 1380-1399 13a0-13f5 13f8-13fd 1400-167f 1681-169c 16a0-16f8 1700-1715 171f-1736
1740-1753 1760-176c 176e-1770 1772-1773 1780-17dd 17e0-17e9 17f0-17f9 1800-180d 180f-1819 1820-1878 1880-18aa 18b0-18f5
1900-191e 1920-192b 1930-193b 1940-1940 1944-196d 1970-1974 1980-19ab 19b0-19c9 19d0-19da 19de-1a1b 1a1e-1a5e 1a60-1a7c
1a7f-1a89 1a90-1a99 1aa0-1aad 1ab0-1ace 1b00-1b4c 1b4e-1bf3 1bfc-1c37 1c3b-1c49 1c4d-1c8a 1c90-1cba 1cbd-1cc7 1cd0-1cfa
1d00-1f15 1f18-1f1d 1f20-1f45 1f48-1f4d 1f50-1f57 1f59-1f59 1f5b-1f5b 1f5d-1f5d 1f5f-1f7d 1f80-1fb4 1fb6-1fc4 1fc6-1fd3
1fd6-1fdb 1fdd-1fef 1ff2-1ff4 1ff6-1ffe 2010-2027 2030-205e 2070-2071 2074-208e 2090-209c 20a0-20c0 20d0-20f0 2100-218b
2190-2429 2440-244a 2460-2b73 2b76-2b95 2b97-2cf3 2cf9-2d25 2d27-2d27 2d2d-2d2d 2d30-2d67 2d6f-2d70 2d7f-2d96 2da0-2da6
2da8-2dae 2db0-2db6 2db8-2dbe 2dc0-2dc6 2dc8-2dce 2dd0-2dd6 2dd8-2dde 2de0-2e5d 2e80-2e99 2e9b-2ef3 2f00-2fd5 2ff0-2fff
3001-303f 3041-3096 3099-30ff 3105-312f 3131-318e 3190-31e5 31ef-321e 3220-a48c a490-a4c6 a4d0-a62b a640-a6f7 a700-a7cd
a7d0-a7d1 a7d3-a7d3 a7d5-a7dc a7f2-a82c a830-a839 a840-a877 a880-a8c5 a8ce-a8d9 a8e0-a953 a95f-a97c a980-a9cd a9cf-a9d9
a9de-a9fe aa00-aa36 aa40-aa4d aa50-aa59 aa5c-aac2 aadb-aaf6 ab01-ab06 ab09-ab0e ab11-ab16 ab20-ab26 ab28-ab2e ab30-ab6b
ab70-abed abf0-abf9 ac00-d7a3 d7b0-d7c6 d7cb-d7fb f900-fa6d fa70-fad9 fb00-fb06 fb13-fb17 fb1d-fb36 fb38-fb3c fb3e-fb3e
fb40-fb41 fb43-fb44 fb46-fbc2 fbd3-fd8f fd92-fdc7 fdcf-fdcf fdf0-fe19 fe20-fe52 fe54-fe66 fe68-fe6b fe70-fe74 fe76-fefc
ff01-ffbe ffc2-ffc7 ffca-ffcf ffd2-ffd7 ffda-ffdc ffe0-ffe6 ffe8-ffee fffc-fffd 10000-1000b 1000d-10026 10028-1003a 1003c-1003d
1003f-1004d 10050-1005d 10080-100fa 10100-10102 10107-10133 10137-1018e 10190-1019c 101a0-101a0 101d0-101fd 10280-1029c 102a0-102d0 102e0-102fb
10300-10323 1032d-1034a 10350-1037a 10380-1039d 1039f-103c3 103c8-103d5 10400-1049d 104a0-104a9 104b0-104d3 104d8-104fb 10500-10527 10530-10563
1056f-1057a 1057c-1058a 1058c-10592 10594-10595 10597-105a1 105a3-105b1 105b3-105b9 105bb-105bc 105c0-105f3 10600-10736 10740-10755 10760-10767
10780-10785 10787-107b0 107b2-107ba 10800-10805 10808-10808 1080a-10835 10837-10838 1083c-1083c 1083f-10855 10857-1089e 108a7-108af 108e0-108f2
108f4-108f5 108fb-1091b 1091f-10939 1093f-1093f 10980-109b7 109bc-109cf 109d2-10a03 10a05-10a06 10a0c-10a13 10a15-10a17 10a19-10a35 10a38-10a3a
10a3f-10a48 10a50-10a58 10a60-10a9f 10ac0-10ae6 10aeb-10af6 10b00-10b35 10b39-10b55 10b58-10b72 10b78-10b91 10b99-10b9c 10ba9-10baf 10c00-10c48
10c80-10cb2 10cc0-10cf2 10cfa-10d27 10d30-10d39 10d40-10d65 10d69-10d85 10d8e-10d8f 10e60-10e7e 10e80-10ea9 10eab-10ead 10eb0-10eb1 10ec2-10ec4
10efc-10f27 10f30-10f59 10f70-10f89 10fb0-10fcb 10fe0-10ff6 11000-1104d 11052-11075 1107f-110bc 110be-110c2 110d0-110e8 110f0-110f9 11100-11134
11136-11147 11150-11176 11180-111df 111e1-111f4 11200-11211 11213-11241 11280-11286 11288-11288 1128a-1128d 1128f-1129d 1129f-112a9 112b0-112ea
112f0-112f9 11300-11303 11305-1130c 1130f-11310 11313-11328 1132a-11330 11332-11333 11335-11339 1133b-11344 11347-11348 1134b-1134d 11350-11350
11357-11357 1135d-11363 11366-1136c 11370-11374 11380-11389 1138b-1138b 1138e-1138e 11390-113b5 113b7-113c0 113c2-113c2 113c5-113c5 113c7-113ca
113cc-113d5 113d7-113d8 113e1-113e2 11400-1145b 1145d-11461 11480-114c7 114d0-114d9 11580-115b5 115b8-115dd 11600-11644 11650-11659 11660-1166c
11680-116b9 116c0-116c9 116d0-116e3 11700-1171a 1171d-1172b 11730-11746 11800-1183b 118a0-118f2 118ff-11906 11909-11909 1190c-11913 11915-11916
11918-11935 11937-11938 1193b-11946 11950-11959 119a0-119a7 119aa-119d7 119da-119e4 11a00-11a47 11a50-11aa2 11ab0-11af8 11b00-11b09 11bc0-11be1
11bf0-11bf9 11c00-11c08 11c0a-11c36 11c38-11c45 11c50-11c6c 11c70-11c8f 11c92-11ca7 11ca9-11cb6 11d00-11d06 11d08-11d09 11d0b-11d36 11d3a-11d3a
11d3c-11d3d 11d3f-11d47 11d50-11d59 11d60-11d65 11d67-11d68 11d6a-11d8e 11d90-11d91 11d93-11d98 11da0-11da9 11ee0-11ef8 11f00-11f10 11f12-11f3a
11f3e-11f5a 11fb0-11fb0 11fc0-11ff1 11fff-12399 12400-1246e 12470-12474 12480-12543 12f90-12ff2 13000-1342f 13440-13455 13460-143fa 14400-14646
16100-16139 16800-16a38 16a40-16a5e 16a60-16a69 16a6e-16abe 16ac0-16ac9 16ad0-16aed 16af0-16af5 16b00-16b45 16b50-16b59 16b5b-16b61 16b63-16b77
16b7d-16b8f 16d40-16d79 16e40-16e9a 16f00-16f4a 16f4f-16f87 16f8f-16f9f 16fe0-16fe4 16ff0-16ff1 17000-187f7 18800-18cd5 18cff-18d08 1aff0-1aff3
1aff5-1affb 1affd-1affe 1b000-1b122 1b132-1b132 1b150-1b152 1b155-1b155 1b164-1b167 1b170-1b2fb 1bc00-1bc6a 1bc70-1bc7c 1bc80-1bc88 1bc90-1bc99
1bc9c-1bc9f 1cc00-1ccf9 1cd00-1ceb3 1cf00-1cf2d 1cf30-1cf46 1cf50-1cfc3 1d000-1d0f5 1d100-1d126 1d129-1d172 1d17b-1d1ea 1d200-1d245 1d2c0-1d2d3
1d2e0-1d2f3 1d300-1d356 1d360-1d378 1d400-1d454 1d456-1d49c 1d49e-1d49f 1d4a2-1d4a2 1d4a5-1d4a6 1d4a9-1d4ac 1d4ae-1d4b9 1d4bb-1d4bb 1d4bd-1d4c3
1d4c5-1d505 1d507-1d50a 1d50d-1d514 1d516-1d51c 1d51e-1d539 1d53b-1d53e 1d540-1d544 1d546-1d546 1d54a-1d550 1d552-1d6a5 1d6a8-1d7cb 1d7ce-1da8b
1da9b-1da9f 1daa1-1daaf 1df00-1df1e 1df25-1df2a 1e000-1e006 1e008-1e018 1e01b-1e021 1e023-1e024 1e026-1e02a 1e030-1e06d 1e08f-1e08f 1e100-1e12c
1e130-1e13d 1e140-1e149 1e14e-1e14f 1e290-1e2ae 1e2c0-1e2f9 1e2ff-1e2ff 1e4d0-1e4f9 1e5d0-1e5fa 1e5ff-1e5ff 1e7e0-1e7e6 1e7e8-1e7eb 1e7ed-1e7ee
1e7f0-1e7fe 1e800-1e8c4 1e8c7-1e8d6 1e900-1e94b 1e950-1e959 1e95e-1e95f 1ec71-1ecb4 1ed01-1ed3d 1ee00-1ee03 1ee05-1ee1f 1ee21-1ee22 1ee24-1ee24
1ee27-1ee27 1ee29-1ee32 1ee34-1ee37 1ee39-1ee39 1ee3b-1ee3b 1ee42-1ee42 1ee47-1ee47 1ee49-1ee49 1ee4b-1ee4b 1ee4d-1ee4f 1ee51-1ee52 1ee54-1ee54
1ee57-1ee57 1ee59-1ee59 1ee5b-1ee5b 1ee5d-1ee5d 1ee5f-1ee5f 1ee61-1ee62 1ee64-1ee64 1ee67-1ee6a 1ee6c-1ee72 1ee74-1ee77 1ee79-1ee7c 1ee7e-1ee7e
1ee80-1ee89 1ee8b-1ee9b 1eea1-1eea3 1eea5-1eea9 1eeab-1eebb 1eef0-1eef1 1f000-1f02b 1f030-1f093 1f0a0-1f0ae 1f0b1-1f0bf 1f0c1-1f0cf 1f0d1-1f0f5
1f100-1f1ad 1f1e6-1f202 1f210-1f23b 1f240-1f248 1f250-1f251 1f260-1f265 1f300-1f6d7 1f6dc-1f6ec 1f6f0-1f6fc 1f700-1f776 1f77b-1f7d9 1f7e0-1f7eb
1f7f0-1f7f0 1f800-1f80b 1f810-1f847 1f850-1f859 1f860-1f887 1f890-1f8ad 1f8b0-1f8bb 1f8c0-1f8c1 1f900-1fa53 1fa60-1fa6d 1fa70-1fa7c 1fa80-1fa89
1fa8f-1fac6 1face-1fadc 1fadf-1fae9 1faf0-1faf8 1fb00-1fb92 1fb94-1fbf9 20000-2a6df 2a700-2b739 2b740-2b81d 2b820-2cea1 2ceb0-2ebe0 2ebf0-2ee5d
2f800-2fa1d 30000-3134a 31350-323af e0100-e01ef
`
