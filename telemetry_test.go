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
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// newModelsServer starts a loopback HTTP/2 server that answers every request
// with an empty model list.
func newModelsServer(t *testing.T) *testsupport.LoopbackServer {
	t.Helper()
	return testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[]}`)
	})})
}

// refusingProxy is an HTTP/1.1 proxy on 127.0.0.1 that answers every
// CONNECT with "502 <reason>", a status line whose text net/http returns as
// the dial's error without its proxyconnect wrap (K16).
type refusingProxy struct {
	ln net.Listener
	wg sync.WaitGroup
}

// newRefusingProxy starts a refusingProxy that answers with reason; it
// stops when the test ends.
func newRefusingProxy(t *testing.T, reason string) *refusingProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := &refusingProxy{ln: ln}
	p.wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			p.wg.Go(func() {
				defer conn.Close()
				if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
					return
				}
				_, _ = io.WriteString(conn, "HTTP/1.1 502 "+reason+"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			})
		}
	})
	t.Cleanup(func() {
		_ = ln.Close()
		p.wg.Wait()
	})
	return p
}

// URL returns the proxy's URL.
func (p *refusingProxy) URL() *url.URL { return &url.URL{Scheme: "http", Host: p.ln.Addr().String()} }

// TestTransportDebugRecordsHoldNoCredential pins ruling R84 (verifier
// finding F-1) through the client: the transport's DEBUG records that print
// an error, "h2: gate error" for a cold dial that failed and "h2: redial
// error" for a dial that failed after the gate was warm, print it through
// the credential scrub, escaped, as the SDK error does, at DEBUG and at
// LevelTrace. The errors come from a caller dialer (WithHTTPTransport) and
// from a proxy that answers the CONNECT with 502 and a reason of its own
// (K16), and each repeats the request's Authorization value, the API key
// quoted and an X-Client-Secret value the call sent.
func TestTransportDebugRecordsHoldNoCredential(t *testing.T) {
	const secret = "provider-credential"
	// failure is the text every failure writes, with an escape to show the
	// records render it as the SDK error does.
	failure := "Authorization: Bearer " + quirkyKey + " refused (" + strconv.Quote(quirkyKey) + "); X-Client-Secret: " + secret + "\x1b[31m"
	const scrubbed = `Authorization: *** refused ("***"); X-Client-Secret: ***\x1b[31m`
	errDial := errors.New(failure)
	type scenario struct {
		// client builds the client with its logger.
		client func(t *testing.T, logger *slog.Logger) *Client
		// warm makes one call that succeeds, then drops the connection, so the
		// failing call re-dials after the gate is warm.
		warm bool
		// record is the DEBUG record that prints the error, error is the SDK
		// error's text.
		record, error string
	}
	scenarios := map[string]scenario{
		"a caller dialer's cold dial": {
			client: func(t *testing.T, logger *slog.Logger) *Client {
				tr := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return nil, errDial }}
				return newCredentialClient(t, logger, WithBaseURL("https://example.com"), WithHTTPTransport(tr))
			},
			record: "DEBUG h2: gate error reason=dial waiters=0 error=" + scrubbed,
			error:  "Connection error: " + scrubbed,
		},
		"a caller dialer's re-dial after warm": {
			client: func(t *testing.T, logger *slog.Logger) *Client {
				srv := newModelsServer(t)
				var dials atomic.Int64
				tr := &http.Transport{TLSClientConfig: testsupport.ClientTLSConfig(t), DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					if dials.Add(1) == 1 {
						return (&net.Dialer{}).DialContext(ctx, network, addr)
					}
					return nil, errDial
				}}
				return newCredentialClient(t, logger, WithBaseURL(srv.URL()), WithHTTPTransport(tr))
			},
			warm:   true,
			record: "DEBUG h2: redial error reason=dial error=" + scrubbed,
			error:  "Connection error: " + scrubbed,
		},
		"a proxy's 502 answer to the CONNECT (K16)": {
			client: func(t *testing.T, logger *slog.Logger) *Client {
				proxy := newRefusingProxy(t, failure)
				return newCredentialClient(t, logger, WithBaseURL("https://example.com"), WithProxy(http.ProxyURL(proxy.URL())))
			},
			record: "DEBUG h2: gate error reason=dial waiters=0 error=" + scrubbed,
			error:  "Connection error: " + scrubbed,
		},
	}
	tests := map[string]struct {
		scenario
		level slog.Level
	}{}
	for name, sc := range scenarios {
		tests["error: "+name+" at DEBUG"] = struct {
			scenario
			level slog.Level
		}{sc, slog.LevelDebug}
		tests["error: "+name+" at LevelTrace"] = struct {
			scenario
			level slog.Level
		}{sc, LevelTrace}
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			logs := testsupport.NewLogRecorder(tt.level)
			c := tt.client(t, logs.Logger())
			if tt.warm {
				if _, err := c.Models().List(t.Context()); err != nil {
					t.Fatalf("warm-up List: %v", err)
				}
				c.cfg.transport.gate.CloseIdleConnections() // the next call dials again
			}
			_, err := callWithin(t, func(ctx context.Context) error {
				_, err := c.Models().List(ctx, Retry(NoRetry()))
				return err
			})
			if ce, ok := errors.AsType[*ConnectionError](err); !ok || ce.Proxy() || ce.Error() != tt.error {
				t.Fatalf("List error = %T %v, want the *ConnectionError %q", err, err, tt.error)
			}
			var got []string
			for _, r := range logs.Records() {
				if _, ok := r.Attr("error"); ok && strings.HasPrefix(r.Message, "h2: ") {
					got = append(got, r.String())
				}
			}
			if diff := gocmp.Diff([]string{tt.record}, got); diff != "" {
				t.Errorf("the transport's records that print an error (-want +got):\n%s", diff)
			}
			records := recordsText(logs)
			for _, s := range []string{quirkyKey, quotedForm(strconv.Quote(quirkyKey)), jsonForm(quirkyKey), secret} {
				if strings.Contains(records, s) {
					t.Errorf("the records hold %q:\n%s", s, records)
				}
				assertNotPrinted(t, err, s)
			}
		})
	}
}

// newCredentialClient builds a client with the API key quirkyKey, an
// X-Client-Secret header, logger and opts, reading no environment, and
// closes it when the test ends.
func newCredentialClient(t *testing.T, logger *slog.Logger, opts ...ClientOption) *Client {
	t.Helper()
	clearEnv(t)
	c, err := NewClient(append([]ClientOption{WithAPIKey(quirkyKey), WithHeader("X-Client-Secret", "provider-credential"), WithLogger(logger)}, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestLogLevelEnvNotRead pins the deviation "`TYPESAFE_LOG_LEVEL` not read"
// (L8, test_setup_logging_from_env, tests/test_logging.py:213-231): the SDK
// never configures a logger, so each value the upstream test sets, "debug",
// "info", "off", "bogus" and "", leaves a call's records as they are without
// the variable: at INFO the one "response" record, at DEBUG the DEBUG
// records too, and without WithLogger nothing, not even in slog's default
// logger. typesafe-sdk-python applies the variable to its logger at import.
func TestLogLevelEnvNotRead(t *testing.T) {
	const env = "TYPESAFE_LOG_LEVEL"
	// records returns "LEVEL message" of every record one Models().List
	// logs, with the logger at level, or with no WithLogger when level is
	// nil, in which case it listens on slog's default logger.
	records := func(t *testing.T, level slog.Leveler) []string {
		t.Helper()
		logs := testsupport.NewLogRecorder(level)
		opts := []ClientOption{WithAPIKey(testKey)}
		if level != nil {
			opts = append(opts, WithLogger(logs.Logger()))
		} else {
			prev := slog.Default()
			slog.SetDefault(logs.Logger())
			t.Cleanup(func() { slog.SetDefault(prev) })
		}
		c := newEnvClient(t, replying(http.StatusOK, []byte(`{"models":[]}`)), opts...)
		if _, err := c.Models().List(t.Context()); err != nil {
			t.Fatalf("List: %v", err)
		}
		var out []string
		for _, r := range logs.Records() {
			out = append(out, r.Level.String()+" "+r.Message)
		}
		return out
	}
	levels := map[string]slog.Leveler{"INFO": slog.LevelInfo, "DEBUG": slog.LevelDebug, "no WithLogger": nil}
	for levelName, level := range levels {
		clearEnv(t)
		want := records(t, level)
		for _, value := range []string{"debug", "info", "off", "bogus", ""} {
			t.Run("success: "+levelName+" with "+env+"="+strconv.Quote(value), func(t *testing.T) {
				clearEnv(t)
				t.Setenv(env, value)
				if diff := gocmp.Diff(want, records(t, level)); diff != "" {
					t.Errorf("records (-without the variable +with it):\n%s", diff)
				}
			})
		}
	}
	t.Run("success: the baselines", func(t *testing.T) {
		clearEnv(t)
		for levelName, want := range map[string][]string{
			"INFO":          {"INFO response"},
			"DEBUG":         {"DEBUG request", "INFO response", "DEBUG response headers"},
			"no WithLogger": nil,
		} {
			if diff := gocmp.Diff(want, records(t, levels[levelName])); diff != "" {
				t.Errorf("%s records (-want +got):\n%s", levelName, diff)
			}
		}
	})
}

// TestLogLevelsPerAttempt ports test_logger_level_controls_output (L7,
// tests/test_logging.py:187-210) and pins the section 9 observability rows
// for one attempt: the logger's level decides which records a call makes,
// exactly {DEBUG, INFO} at DEBUG, {INFO} at INFO and none at WARN; each
// attempt makes one INFO record, "response" naming the method and the
// endpoint (or "request failed" for an attempt without a response); the
// DEBUG records carry the headers, redacted; no record above DEBUG carries
// a header, and none above LevelTrace a body. Each call makes one attempt.
func TestLogLevelsPerAttempt(t *testing.T) {
	const endpoint = "https://api.typesafe.ai/v1/models"
	body := `{"models":[]}`
	tests := map[string]struct {
		level slog.Level
		reply testsupport.Reply
		want  []string // "LEVEL message" of every record, in order
	}{
		"success: DEBUG": {
			level: slog.LevelDebug, reply: testsupport.JSON(http.StatusOK, []byte(body)),
			want: []string{"DEBUG request", "INFO response", "DEBUG response headers"},
		},
		"success: INFO": {
			level: slog.LevelInfo, reply: testsupport.JSON(http.StatusOK, []byte(body)),
			want: []string{"INFO response"},
		},
		"success: WARN": {
			level: slog.LevelWarn, reply: testsupport.JSON(http.StatusOK, []byte(body)),
		},
		"success: LevelTrace": {
			level: LevelTrace, reply: testsupport.JSON(http.StatusOK, []byte(body)),
			want: []string{"DEBUG request", "INFO response", "DEBUG response headers", "DEBUG-4 response body"},
		},
		"error: a failure status at INFO": {
			level: slog.LevelInfo, reply: testsupport.JSON(http.StatusBadRequest, []byte(`{"message":"bad"}`)),
			want: []string{"INFO response"},
		},
		"error: an attempt without a response at INFO": {
			level: slog.LevelInfo, reply: testsupport.Reply{Err: errString("connection refused")},
			want: []string{"INFO request failed"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			logs := testsupport.NewLogRecorder(tt.level)
			reply := tt.reply
			if reply.Header != nil {
				reply.Header.Set("X-Visible", "response-visible")
			}
			rec := &testsupport.Recorder{Replies: []testsupport.Reply{reply}}
			c := newTestClient(t, rec, WithHeader("X-Visible", "request-visible"), WithLogger(logs.Logger()))
			_, _ = c.Models().List(t.Context(), Retry(NoRetry()))
			if rec.Count() != 1 {
				t.Fatalf("the transport saw %d requests, want 1", rec.Count())
			}
			var got []string
			for _, r := range logs.Records() {
				got = append(got, r.Level.String()+" "+r.Message)
				if r.Level == slog.LevelInfo {
					method, _ := r.Attr("method")
					ep, _ := r.Attr("endpoint")
					if method.String() != http.MethodGet || ep.String() != endpoint {
						t.Errorf("INFO record %s, want it to name GET %s", r, endpoint)
					}
				}
				for _, a := range r.Attrs {
					v := a.Value.String()
					if r.Level > slog.LevelDebug && (strings.HasPrefix(a.Key, "headers") || strings.Contains(v, "visible") || strings.Contains(v, "application/json") || strings.Contains(v, "typesafe-sdk-go/")) {
						t.Errorf("%s record %q carries a header: %s=%s", r.Level, r.Message, a.Key, v)
					}
					if r.Level > LevelTrace && (a.Key == "body" || strings.Contains(v, `"models"`) || strings.Contains(v, `"message"`)) {
						t.Errorf("%s record %q carries a body: %s=%s", r.Level, r.Message, a.Key, v)
					}
				}
				switch r.Message {
				case "request":
					auth, _ := r.Attr("headers.Authorization")
					vis, _ := r.Attr("headers.X-Visible")
					if auth.String() != redacted || vis.String() != "request-visible" {
						t.Errorf("request record %s, want the headers with Authorization redacted", r)
					}
				case "response headers":
					if vis, _ := r.Attr("headers.X-Visible"); vis.String() != "response-visible" {
						t.Errorf("response headers record %s, want the response's headers", r)
					}
				}
			}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("records (-want +got):\n%s", diff)
			}
		})
	}
}

// newLoopbackClient builds a client of the SDK's own transport against srv,
// trusting its certificate and using no proxy, with logger and opts.
func newLoopbackClient(t *testing.T, srv *testsupport.LoopbackServer, logger *slog.Logger, opts ...ClientOption) *Client {
	t.Helper()
	clearEnv(t)
	c, err := NewClient(append([]ClientOption{WithAPIKey(testKey), WithBaseURL(srv.URL()), WithRootCAs(testsupport.RootCAs(t)), WithProxy(nil), WithLogger(logger)}, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestLogTransportRecords pins the section 9 rows that need the SDK's own
// transport: a cold call logs "h2: dial" at DEBUG, with h2=true, and the
// gate's release, and Stats().Dials counts the one connection; a warm call
// dials nothing; and WithLogEndpointHost(false) keeps the host out of every
// record down to LevelTrace, the transport's included, where the default
// names the full URL.
func TestLogTransportRecords(t *testing.T) {
	t.Run("success: a cold call dials once and logs it", func(t *testing.T) {
		srv := newModelsServer(t)
		logs := testsupport.NewLogRecorder(slog.LevelDebug)
		c := newLoopbackClient(t, srv, logs.Logger())
		for i := range 2 {
			if _, err := c.Models().List(t.Context()); err != nil {
				t.Fatalf("List %d: %v", i, err)
			}
		}
		var got []string
		for _, r := range logs.Records() {
			if strings.HasPrefix(r.Message, "h2: ") {
				got = append(got, r.String())
			}
		}
		if diff := gocmp.Diff([]string{"DEBUG h2: dial h2=true", "DEBUG h2: gate release waiters=0"}, got); diff != "" {
			t.Errorf("the transport's records over two calls (-want +got):\n%s", diff)
		}
		if s := c.Stats(); s.Dials != 1 || s.Attempts != 2 {
			t.Errorf("Stats() = %+v, want 1 dial and 2 attempts", s)
		}
	})
	tests := map[string]struct {
		opts []ClientOption
		// endpoint is what the records name; host, whether the host appears.
		endpoint func(srv *testsupport.LoopbackServer) string
		host     bool
	}{
		"success: the default names the full URL": {
			endpoint: func(srv *testsupport.LoopbackServer) string { return srv.URL() + "/v1/models" }, host: true,
		},
		"success: WithLogEndpointHost(false) names the path alone": {
			opts:     []ClientOption{WithLogEndpointHost(false)},
			endpoint: func(*testsupport.LoopbackServer) string { return "/v1/models" },
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := newModelsServer(t)
			logs := testsupport.NewLogRecorder(LevelTrace)
			c := newLoopbackClient(t, srv, logs.Logger(), tt.opts...)
			if _, err := c.Models().List(t.Context()); err != nil {
				t.Fatalf("List: %v", err)
			}
			for _, r := range logs.Records() {
				if ep, ok := r.Attr("endpoint"); ok && ep.String() != tt.endpoint(srv) {
					t.Errorf("record %q endpoint = %q, want %q", r.Message, ep, tt.endpoint(srv))
				}
			}
			if text := recordsText(logs); strings.Contains(text, srv.Addr()) != tt.host {
				t.Errorf("the host %s appears in the records: %t, want %t:\n%s", srv.Addr(), !tt.host, tt.host, text)
			}
		})
	}
}

// TestLogWarnCapThroughClient re-asserts AC-F7's WARN cap through the client
// (Appendix B "Unknown-answer WARN per answer → ≤ 8 per response +
// summary"; the rule's own table is TestUnknownAnswerTypeWarnCap): a
// successful call whose response holds 12 answers of unknown types logs
// eight WARN records naming the first eight, then one counting the other
// four, and succeeds; a name and a type the server chose are escaped and
// cut at 128 characters; at ERROR nothing is logged.
func TestLogWarnCapThroughClient(t *testing.T) {
	long := strings.Repeat("t", 300)
	var twelve, wantTwelve []string
	for i := range 12 {
		n := strconv.Itoa(i)
		twelve = append(twelve, `"u`+n+`":{"type":"t`+n+`"}`)
		if i < 8 {
			wantTwelve = append(wantTwelve, "WARN "+msgSkippedAnswer+" answer=u"+n+" type=t"+n)
		}
	}
	tests := map[string]struct {
		level   slog.Level
		answers []string
		want    []string
	}{
		"success: 12 unknown answers, 8 records and a summary": {
			level: slog.LevelWarn, answers: twelve, want: append(wantTwelve, "WARN "+msgSkippedAnswers+" count=4"),
		},
		"success: a name and a type escaped and cut": {
			level: slog.LevelWarn, answers: []string{`"a\u001b[2Jb\\":{"type":"` + long + `"}`},
			want: []string{"WARN " + msgSkippedAnswer + ` answer=a\x1b[2Jb\\ type=` + long[:128] + "\u2026"},
		},
		"success: nothing at ERROR": {
			level: slog.LevelError, answers: twelve,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			logs := testsupport.NewLogRecorder(tt.level)
			body := `{"model":"jev-latest","usage":{},"answers":{` + strings.Join(tt.answers, ",") + `}}`
			c := newTestClient(t, replying(http.StatusOK, []byte(body)), WithLogger(logs.Logger()))
			resp, err := c.SystemOne(t.Context(), "hi", noulQuestion(t))
			if err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			if n := resp.Answers().Len(); n != 0 {
				t.Errorf("Answers().Len() = %d, want the unknown answers dropped", n)
			}
			var got []string
			for _, r := range logs.Records() {
				got = append(got, r.String())
			}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("records (-want +got):\n%s", diff)
			}
		})
	}
}
