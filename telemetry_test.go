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
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
// the dial's error without its proxyconnect wrap; the SDK's transport adds
// it (K16). The answer carries a body, which reaches neither the error nor a
// log record (review SLICE3 NIT 1).
type refusingProxy struct {
	ln net.Listener
	wg sync.WaitGroup
}

// newRefusingProxy starts a refusingProxy that answers with reason and body;
// it stops when the test ends.
func newRefusingProxy(t *testing.T, reason, body string) *refusingProxy {
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
				_, _ = io.WriteString(conn, "HTTP/1.1 502 "+reason+"\r\nContent-Length: "+strconv.Itoa(len(body))+"\r\nConnection: close\r\n\r\n"+body)
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

// echoStatusLine is the answer of an echoing proxy that refuses the CONNECT
// with a well-formed status line, "407 denied <password>
// (Proxy-Authorization: <value>)".
func echoStatusLine(password, auth string) string {
	return "HTTP/1.1 407 denied " + password + " (Proxy-Authorization: " + auth + ")\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
}

// echoingProxy is an HTTP/1.1 proxy on 127.0.0.1 that answers every request
// it reads, a CONNECT or a plain-HTTP request forwarded to it, with
// answer(password, auth), which repeats the credential the request carried:
// auth is the Proxy-Authorization value net/http sent for the proxy URL's
// userinfo and password the password it decodes to (review W6.2 MIN-4). A
// held proxy answers once open has run; requests counts the requests read.
type echoingProxy struct {
	ln       net.Listener
	release  chan struct{}
	open     func()
	requests atomic.Int64
	wg       sync.WaitGroup
}

// newEchoingProxy starts an echoingProxy that answers at once. It stops when
// the test ends.
func newEchoingProxy(t *testing.T, answer func(password, auth string) string) *echoingProxy {
	t.Helper()
	return newHeldEchoingProxy(t, answer, false)
}

// newHeldEchoingProxy starts an echoingProxy, held until its open runs when
// held is true. It stops when the test ends.
func newHeldEchoingProxy(t *testing.T, answer func(password, auth string) string, held bool) *echoingProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := &echoingProxy{ln: ln, release: make(chan struct{})}
	p.open = sync.OnceFunc(func() { close(p.release) })
	if !held {
		p.open()
	}
	p.wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			p.wg.Go(func() {
				defer conn.Close()
				req, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					return
				}
				p.requests.Add(1)
				auth := req.Header.Get("Proxy-Authorization")
				var password string
				if b64, ok := strings.CutPrefix(auth, "Basic "); ok {
					if raw, err := base64.StdEncoding.DecodeString(b64); err == nil {
						_, password, _ = strings.Cut(string(raw), ":")
					}
				}
				<-p.release
				_, _ = io.WriteString(conn, answer(password, auth))
			})
		}
	})
	t.Cleanup(func() {
		p.open()
		_ = ln.Close()
		p.wg.Wait()
	})
	return p
}

// URL returns the proxy's URL, without userinfo.
func (p *echoingProxy) URL() *url.URL { return &url.URL{Scheme: "http", Host: p.ln.Addr().String()} }

// proxySecrets returns what of the credential of a proxy URL with user and
// password an error or a record must not show: the password as it is, as
// the URL escapes it and each of its words between single spaces, and the
// Basic token net/http sends for it.
func proxySecrets(user, password string) []string {
	out := []string{password, base64.StdEncoding.EncodeToString([]byte(user + ":" + password)), strings.TrimPrefix(url.UserPassword("", password).String(), ":")}
	for word := range strings.SplitSeq(password, " ") {
		if word != "" && !slices.Contains(out, word) {
			out = append(out, word)
		}
	}
	return out
}

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
	// proxyBody marks the refusing proxy's response body, which must reach
	// neither the error nor a record.
	const proxyBody = "PROXYBODYMARKER"
	// The echoing proxy's credential, in its URL's userinfo.
	const proxyUser, proxyPassword = "proxy-user", "hunter2-proxy-password"
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
		// proxy is the SDK error's Proxy().
		proxy bool
		// secrets are the scenario's own credentials, which no record and no
		// rendering of the error may hold.
		secrets []string
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
				proxy := newRefusingProxy(t, failure, proxyBody+" "+failure)
				return newCredentialClient(t, logger, WithBaseURL("https://example.com"), WithProxy(http.ProxyURL(proxy.URL())))
			},
			record: "DEBUG h2: gate error reason=proxy waiters=0 error=proxyconnect tcp: 502 " + scrubbed,
			error:  "Connection error: proxyconnect tcp: 502 " + scrubbed,
			proxy:  true,
		},
		// A proxy that repeats the credential its CONNECT carried, which the
		// request's own credentials do not name (review W6.2 MIN-4): the
		// SDK's transport replaces it where the refusal becomes an error.
		"a proxy's 407 that repeats its own credential": {
			client: func(t *testing.T, logger *slog.Logger) *Client {
				proxy := newEchoingProxy(t, echoStatusLine).URL()
				proxy.User = url.UserPassword(proxyUser, proxyPassword)
				return newCredentialClient(t, logger, WithBaseURL("https://example.com"), WithProxy(http.ProxyURL(proxy)))
			},
			record:  "DEBUG h2: gate error reason=proxy waiters=0 error=proxyconnect tcp: 407 denied *** (Proxy-Authorization: Basic ***)",
			error:   "Connection error: proxyconnect tcp: 407 denied *** (Proxy-Authorization: Basic ***)",
			proxy:   true,
			secrets: []string{proxyPassword, base64.StdEncoding.EncodeToString([]byte(proxyUser + ":" + proxyPassword))},
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
			if ce, ok := errors.AsType[*ConnectionError](err); !ok || ce.Proxy() != tt.proxy || ce.Error() != tt.error {
				t.Fatalf("List error = %T %v, want the *ConnectionError %q with Proxy() %t", err, err, tt.error, tt.proxy)
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
			if !strings.Contains(records, "INFO request failed") {
				t.Errorf("no INFO record of the failure:\n%s", records)
			}
			for _, s := range append([]string{quirkyKey, quotedForm(strconv.Quote(quirkyKey)), jsonForm(quirkyKey), secret, proxyBody}, tt.secrets...) {
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
// dials nothing; and, for a call that succeeds, WithLogEndpointHost(false)
// keeps the host out of every record down to LevelTrace, the transport's
// included, where the default names the full URL. A call that fails can
// still name the host: the text of a dial or DNS error, which the network
// stack writes, goes into the error and its records as it is (review W3.3
// MINOR 2; the option names only the endpoint attribute).
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

// TestProxyEchoHoldsNoCredential checks that no answer of a proxy repeating
// its own credential gets it past the SDK (review W6.2 MIN-4 and MINOR 1 of
// its review): a well-formed refusal, whose status line refusedConnect
// scrubs, and every answer net/http cannot parse, from which it builds an
// error quoting the answer before the SDK's transport sees a response. The
// proxy URL holds a password of 22 bytes, and each answer repeats it, or
// the Basic token of the Proxy-Authorization value the CONNECT carried, or
// the password as the proxy URL escapes it (its "@" is %40). No
// rendering of the SDK error, of anything it unwraps to, nor any record at
// DEBUG or LevelTrace holds either, and "***" stands for them in Error(), in
// the INFO "request failed" record and in the DEBUG "h2: gate error" record.
// A refusal is a proxy failure (Proxy() true, K16); an answer net/http
// cannot parse comes back from it as a failure of the dial, not of the
// proxy, and Proxy() is false (the reviewer's K16 residual). The last shape
// is the same proxy in front of a plain-HTTP API URL, which net/http sends
// the request itself, Proxy-Authorization included, without a CONNECT: the
// malformed answer then fails the round trip after the dial, which the
// attempt's classification, not the transport's, maps, and no "h2: gate
// error" record is written.
//
// The proxy's credential is looked for whatever its length (ruling
// D-W6-secfix-m2), so a 3-byte password is scrubbed too; and each word of a
// password between single spaces, since net/http quotes one word of a status
// line it cannot parse: the first where the status code is, or the second
// where the status code is when the first is read as the version, alone or
// glued to it (ruling D-W6-secfix-revise-2-scope (a)).
func TestProxyEchoHoldsNoCredential(t *testing.T) {
	const user, defaultPassword = "proxy-user", "hunter2@proxy-password"
	escaped := strings.TrimPrefix(url.UserPassword("", defaultPassword).String(), ":") // hunter2%40proxy-password
	const twoWords = "alpha7 bravo9"
	shapes := map[string]struct {
		answer   func(password, auth string) string
		password string // the proxy URL's password; empty for defaultPassword
		proxy    bool
		plain    bool // a plain-HTTP API URL: no CONNECT, no gate error
	}{
		"a refusal whose status line repeats the password and the token": {answer: echoStatusLine, proxy: true},
		"a malformed HTTP response (net/http quotes the status line)": {answer: func(pw, _ string) string {
			return "HTTP/1.1_" + pw + "\r\n\r\n"
		}},
		"a malformed HTTP response that repeats the token": {answer: func(_, auth string) string {
			return "HTTP/1.1_" + auth + "\r\n\r\n"
		}},
		"a malformed HTTP response that repeats the password as the URL escapes it": {answer: func(string, string) string {
			return "HTTP/1.1_" + escaped + "\r\n\r\n"
		}},
		"a malformed HTTP status code": {answer: func(pw, _ string) string {
			return "HTTP/1.1 " + pw + "\r\n\r\n"
		}},
		"a malformed HTTP version": {answer: func(pw, _ string) string {
			return pw + " 407 denied\r\n\r\n"
		}},
		"a malformed MIME header (a line without a colon)": {answer: func(pw, _ string) string {
			return "HTTP/1.1 407 denied\r\n" + pw + "\r\n\r\n"
		}},
		"a malformed HTTP response to a plain-HTTP request": {answer: func(pw, _ string) string {
			return "HTTP/1.1_" + pw + "\r\n\r\n"
		}, plain: true},
		"a malformed HTTP response that repeats a 3-byte password": {answer: func(pw, _ string) string {
			return "HTTP/1.1_" + pw + "\r\n\r\n"
		}, password: "p1x"},
		"the first word of a two-word password where the status code is": {answer: func(pw, _ string) string {
			return "HTTP/1.1 " + pw + "\r\n\r\n" // malformed HTTP status code "alpha7"
		}, password: twoWords},
		"the second word of a two-word password where the status code is, the first as the version": {answer: func(pw, _ string) string {
			return pw + " 407 denied\r\n\r\n" // malformed HTTP status code "bravo9"
		}, password: twoWords},
		"the second word of a two-word password where the status code is, the first glued to the version": {answer: func(pw, _ string) string {
			return "HTTP/1.1_" + pw + "\r\n\r\n" // malformed HTTP status code "bravo9"
		}, password: twoWords},
	}
	levels := map[string]slog.Level{"DEBUG": slog.LevelDebug, "LevelTrace": LevelTrace}
	for shape, sh := range shapes {
		for levelName, level := range levels {
			t.Run("error: "+shape+" at "+levelName, func(t *testing.T) {
				password := cmp.Or(sh.password, defaultPassword)
				secrets := proxySecrets(user, password)
				proxyURL := newEchoingProxy(t, sh.answer).URL()
				proxyURL.User = url.UserPassword(user, password)
				logs := testsupport.NewLogRecorder(level)
				base := "https://example.com"
				if sh.plain {
					base = "http://example.com"
				}
				c := newCredentialClient(t, logs.Logger(), WithBaseURL(base), WithProxy(http.ProxyURL(proxyURL)))
				_, err := callWithin(t, func(ctx context.Context) error {
					_, err := c.Models().List(ctx, Retry(NoRetry()))
					return err
				})
				ce, ok := errors.AsType[*ConnectionError](err)
				if !ok {
					t.Fatalf("List error = %T %v, want a *ConnectionError", err, err)
				}
				if ce.Proxy() != sh.proxy {
					t.Errorf("Proxy() = %t, want %t", ce.Proxy(), sh.proxy)
				}
				if !strings.Contains(ce.Error(), redacted) {
					t.Errorf("Error() = %q, want the credential replaced by %q", ce.Error(), redacted)
				}
				for _, secret := range secrets {
					assertNotPrinted(t, err, secret)
				}
				records := recordsText(logs)
				wantRecords := []string{"INFO request failed", "DEBUG h2: gate error"}
				if sh.plain {
					wantRecords = wantRecords[:1]
				}
				for _, want := range wantRecords {
					line := ""
					for l := range strings.Lines(records) {
						if strings.Contains(l, want) {
							line = l
							break
						}
					}
					if !strings.Contains(line, redacted) {
						t.Errorf("the %q record %q lacks %q:\n%s", want, line, redacted, records)
					}
				}
				for _, secret := range secrets {
					if strings.Contains(records, secret) {
						t.Errorf("the records hold %q:\n%s", secret, records)
					}
				}
			})
		}
	}
}

// malformedEcho is an echoing proxy's answer that net/http cannot parse and
// quotes: `malformed HTTP response "HTTP/1.1_<password>"`.
func malformedEcho(password, _ string) string { return "HTTP/1.1_" + password + "\r\n\r\n" }

// assertRecordsScrubbed checks that the records hold none of secrets and
// that each of the want INFO "request failed" records shows "***".
func assertRecordsScrubbed(t *testing.T, logs *testsupport.LogRecorder, secrets []string, want int) {
	t.Helper()
	records := recordsText(logs)
	failed := 0
	for l := range strings.Lines(records) {
		if !strings.Contains(l, "INFO request failed") {
			continue
		}
		failed++
		if !strings.Contains(l, redacted) {
			t.Errorf("the record %q lacks %q", l, redacted)
		}
	}
	if failed != want {
		t.Errorf("%d INFO request failed records, want %d:\n%s", failed, want, records)
	}
	for _, secret := range secrets {
		if strings.Contains(records, secret) {
			t.Errorf("the records hold %q:\n%s", secret, records)
		}
	}
}

// TestProxyEchoConcurrentColdClient pins ruling D-W6-secfix-m2 where the
// security review's probe found the per-request reading wrong: twelve
// calls at once on a cold client, through a proxy whose answer net/http
// cannot parse and quotes with the password in it. Over CONNECT the gate
// lets one call lead and parks the other eleven; the proxy answers the
// leader's CONNECT only once they are parked, so the proxy func is asked
// once, by net/http for the leader, and the eleven share the leader's
// failed dial (R19) without asking it. Every one of the twelve errors, and
// every record at the level, holds no password, token or escaped password
// and shows "***": the scrub reads the transport's set of the proxies it
// chose, not the request's own. A plain-HTTP API URL sends each call to the
// proxy itself, which asks the func once per call. A rotating func turns
// between two proxies, each repeating its own password. The func is asked
// exactly as often as the proxy is sent a request: none on the error path.
func TestProxyEchoConcurrentColdClient(t *testing.T) {
	const (
		n    = 12
		user = "proxy-user"
	)
	passwords := [2]string{"alpha@proxy-password", "bravo@proxy-password"}
	var secrets []string
	for _, pw := range passwords {
		secrets = append(secrets, proxySecrets(user, pw)...)
	}
	tests := map[string]struct {
		rotate bool
		plain  bool // a plain-HTTP API URL: no CONNECT, no gate
		level  slog.Level
	}{
		"error: CONNECT, one proxy, at INFO":                {level: slog.LevelInfo},
		"error: CONNECT, one proxy, at DEBUG":               {level: slog.LevelDebug},
		"error: CONNECT, one proxy, at LevelTrace":          {level: LevelTrace},
		"error: CONNECT, a rotating func, at DEBUG":         {rotate: true, level: slog.LevelDebug},
		"error: plain HTTP, one proxy, at INFO":             {plain: true, level: slog.LevelInfo},
		"error: plain HTTP, one proxy, at DEBUG":            {plain: true, level: slog.LevelDebug},
		"error: plain HTTP, a rotating func, at DEBUG":      {plain: true, rotate: true, level: slog.LevelDebug},
		"error: plain HTTP, a rotating func, at LevelTrace": {plain: true, rotate: true, level: LevelTrace},
		"error: CONNECT, a rotating func, at LevelTrace":    {rotate: true, level: LevelTrace},
		"error: CONNECT, a rotating func, at INFO":          {rotate: true, level: slog.LevelInfo},
		"error: plain HTTP, a rotating func, at INFO":       {plain: true, rotate: true, level: slog.LevelInfo},
		"error: plain HTTP, one proxy, at LevelTrace":       {plain: true, level: LevelTrace},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var proxies [2]*echoingProxy
			var urls [2]*url.URL
			for i, pw := range passwords {
				proxies[i] = newHeldEchoingProxy(t, malformedEcho, !tt.plain)
				urls[i] = proxies[i].URL()
				urls[i].User = url.UserPassword(user, pw)
			}
			var calls atomic.Int64
			choose := func(*http.Request) (*url.URL, error) {
				k := calls.Add(1) - 1
				if !tt.rotate {
					k = 0
				}
				return urls[k%2], nil
			}
			logs := testsupport.NewLogRecorder(tt.level)
			base := "https://example.com"
			if tt.plain {
				base = "http://example.com"
			}
			c := newCredentialClient(t, logs.Logger(), WithBaseURL(base), WithProxy(choose))
			ctx, cancel := context.WithTimeout(t.Context(), callBound)
			defer cancel()
			errs := make([]error, n)
			var wg sync.WaitGroup
			for i := range n {
				wg.Go(func() {
					_, errs[i] = c.Models().List(ctx, Retry(NoRetry()))
				})
			}
			if !tt.plain {
				gate := c.cfg.transport.gate
				deadline := time.Now().Add(callBound)
				for proxies[0].requests.Load() != 1 || gate.Parked() != n-1 {
					if time.Now().After(deadline) {
						t.Fatalf("no leader's CONNECT and %d parked calls within %v: CONNECTs %d, parked %d", n-1, callBound, proxies[0].requests.Load(), gate.Parked())
					}
					time.Sleep(time.Millisecond)
				}
				proxies[0].open()
			}
			wg.Wait()
			for i, err := range errs {
				ce, ok := errors.AsType[*ConnectionError](err)
				if !ok {
					t.Fatalf("call %d: error = %T %v, want a *ConnectionError", i, err, err)
				}
				if !strings.Contains(ce.Error(), redacted) {
					t.Errorf("call %d: Error() = %q, want the credential replaced by %q", i, ce.Error(), redacted)
				}
				for _, secret := range secrets {
					assertNotPrinted(t, err, secret)
				}
			}
			assertRecordsScrubbed(t, logs, secrets, n)
			sent := proxies[0].requests.Load() + proxies[1].requests.Load()
			t.Logf("CONSULTS %s: the func asked %d times for %d calls; the proxies sent %d requests", name, calls.Load(), n, sent)
			if calls.Load() != sent {
				t.Errorf("the func was asked %d times for the %d requests the proxies were sent, want as many: none on the error path", calls.Load(), sent)
			}
			if !tt.plain && calls.Load() != 1 {
				t.Errorf("the func was asked %d times for a cold burst over CONNECT, want 1: the leader's", calls.Load())
			}
		})
	}
}

// TestProxyCredentialSetEvictsTheOldest pins the bound of the transport's
// proxy credential set (ruling D-W6-secfix-m2): a func that rotates through
// 17 proxies, one call each, each proxy repeating its own password, leaves
// the 16 most recent in the set, the first forgotten, and every call's
// error and records scrubbed of its own proxy's password; a proxy chosen
// again comes back as the most recent, forgetting the then oldest.
func TestProxyCredentialSetEvictsTheOldest(t *testing.T) {
	const user = "proxy-user"
	const n = maxProxyUserinfos + 1
	urls := make([]*url.URL, n)
	for i := range n {
		urls[i] = newEchoingProxy(t, malformedEcho).URL()
		urls[i].User = url.UserPassword(user, "proxy-password-"+strconv.Itoa(i)+"-of-17")
	}
	var calls atomic.Int64
	choose := func(*http.Request) (*url.URL, error) {
		k := int(calls.Add(1)-1) % n
		if calls.Load() == n+1 {
			k = 0 // the first again, after all 17
		}
		return urls[k], nil
	}
	logs := testsupport.NewLogRecorder(slog.LevelDebug)
	c := newCredentialClient(t, logs.Logger(), WithProxy(choose))
	set := c.cfg.transport.proxies
	passwordsIn := func() []string {
		set.mu.Lock()
		defer set.mu.Unlock()
		out := make([]string, 0, len(set.users))
		for _, u := range set.users {
			pw, _ := u.Password()
			out = append(out, pw)
		}
		return out
	}
	var secrets []string
	for i := range n + 1 {
		u := urls[i%n]
		pw, _ := u.User.Password()
		secrets = append(secrets, proxySecrets(user, pw)...)
		_, err := callWithin(t, func(ctx context.Context) error {
			_, err := c.Models().List(ctx, Retry(NoRetry()))
			return err
		})
		if _, ok := errors.AsType[*ConnectionError](err); !ok {
			t.Fatalf("call %d: error = %T %v, want a *ConnectionError", i, err, err)
		}
		assertNotPrinted(t, err, pw)
		if i == n-1 {
			got := passwordsIn()
			if len(got) != maxProxyUserinfos || slices.Contains(got, "proxy-password-0-of-17") || got[len(got)-1] != pw {
				t.Errorf("after 17 proxies the set holds %d: %q; want the 16 most recent, the first forgotten, the 17th last", len(got), got)
			}
		}
	}
	got := passwordsIn()
	if len(got) != maxProxyUserinfos || got[len(got)-1] != "proxy-password-0-of-17" || slices.Contains(got, "proxy-password-1-of-17") {
		t.Errorf("after the first proxy again the set holds %d: %q; want it the most recent and the second forgotten", len(got), got)
	}
	assertRecordsScrubbed(t, logs, secrets, n+1)
}

// TestProxyFuncAskedOncePerAttempt pins the P6 critic's m-2 across the
// attempts of one call: the SDK's transport asks a WithProxy func once per
// attempt, where net/http asks it, and never again on the error path. The
// func counts its calls and rotates between two proxies, each with its own
// password in its URL, and each proxy's malformed answer repeats the
// password its CONNECT (or, for a plain-HTTP API URL, the request it
// forwards) carried. Three attempts (two retries) go through the first
// proxy, the second, then the first again; no rendering of the SDK error, of
// anything it unwraps to, nor any record at the level holds either
// password, its token or its escaped form.
func TestProxyFuncAskedOncePerAttempt(t *testing.T) {
	const (
		user     = "proxy-user"
		attempts = 3
	)
	passwords := [2]string{"alpha@proxy-password", "bravo@proxy-password"}
	var secrets []string
	for _, pw := range passwords {
		secrets = append(secrets, proxySecrets(user, pw)...)
	}
	tests := map[string]struct {
		plain bool // a plain-HTTP API URL: no CONNECT, no gate error
		level slog.Level
	}{
		"error: CONNECT through rotating proxies at INFO":                    {level: slog.LevelInfo},
		"error: CONNECT through rotating proxies at DEBUG":                   {level: slog.LevelDebug},
		"error: CONNECT through rotating proxies at LevelTrace":              {level: LevelTrace},
		"error: a plain-HTTP request through rotating proxies at INFO":       {plain: true, level: slog.LevelInfo},
		"error: a plain-HTTP request through rotating proxies at DEBUG":      {plain: true, level: slog.LevelDebug},
		"error: a plain-HTTP request through rotating proxies at LevelTrace": {plain: true, level: LevelTrace},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var urls [2]*url.URL
			for i, pw := range passwords {
				urls[i] = newEchoingProxy(t, malformedEcho).URL()
				urls[i].User = url.UserPassword(user, pw)
			}
			var calls atomic.Int64
			var mu sync.Mutex
			var chosen []string // the password of each URL the func returned, in order
			rotate := func(*http.Request) (*url.URL, error) {
				u := urls[(calls.Add(1)-1)%2]
				pw, _ := u.User.Password()
				mu.Lock()
				chosen = append(chosen, pw)
				mu.Unlock()
				return u, nil
			}
			logs := testsupport.NewLogRecorder(tt.level)
			base := "https://example.com"
			if tt.plain {
				base = "http://example.com"
			}
			c := newCredentialClient(t, logs.Logger(), WithBaseURL(base), WithProxy(rotate))
			_, err := callWithin(t, func(ctx context.Context) error {
				_, err := c.Models().List(ctx, Retry(fastRetry().MaxRetries(attempts-1)))
				return err
			})
			if got := calls.Load(); got != attempts {
				t.Errorf("the proxy func was called %d times for %d attempts (%q), want %d: once per attempt, never on the error path", got, attempts, chosen, attempts)
			}
			if want := []string{passwords[0], passwords[1], passwords[0]}; !slices.Equal(chosen, want) {
				t.Errorf("the func chose %q, want %q", chosen, want)
			}
			ce, ok := errors.AsType[*ConnectionError](err)
			if !ok {
				t.Fatalf("List error = %T %v, want a *ConnectionError", err, err)
			}
			if !strings.Contains(ce.Error(), redacted) {
				t.Errorf("Error() = %q, want the credential replaced by %q", ce.Error(), redacted)
			}
			for _, secret := range secrets {
				assertNotPrinted(t, err, secret)
			}
			assertRecordsScrubbed(t, logs, secrets, attempts)
		})
	}
}

// TestProxyAnswerHeadersRedacted pins ruling D-W6-secfix-revise-2-scope-c
// (NOTE R4): a plain-HTTP API URL sends the request to the proxy itself,
// whose well-formed 407 is the call's response, and a proxy may repeat in
// its headers the Proxy-Authorization it was sent, or the password. Each of
// the three places that print a response header redacts a value holding a
// credential of a proxy the client's transport chose: the error types'
// Header (headerRedactor.header), the request id of the INFO "response"
// record and of the error (headerRedactor.requestID), and the DEBUG
// "response headers" record (newRedactedHeaders). The body is shown as the
// proxy wrote it, by design (ruling R103-rev); this one is empty.
func TestProxyAnswerHeadersRedacted(t *testing.T) {
	const user, password = "proxy-user", "hunter2@proxy-password"
	secrets := proxySecrets(user, password)
	proxyURL := newEchoingProxy(t, func(pw, auth string) string {
		return "HTTP/1.1 407 Proxy Authentication Required\r\n" +
			"X-Proxy-Echo: " + auth + "\r\n" +
			"X-Proxy-Password: seen " + pw + "\r\n" +
			"X-Typesafe-Request-Id: req-" + strings.TrimPrefix(auth, "Basic ") + "\r\n" +
			"Content-Length: 0\r\nConnection: close\r\n\r\n"
	}).URL()
	proxyURL.User = url.UserPassword(user, password)
	logs := testsupport.NewLogRecorder(slog.LevelDebug)
	c := newCredentialClient(t, logs.Logger(), WithBaseURL("http://example.com"), WithProxy(http.ProxyURL(proxyURL)))
	_, err := callWithin(t, func(ctx context.Context) error {
		_, err := c.Models().List(ctx, Retry(NoRetry()))
		return err
	})
	ae, ok := errors.AsType[*APIError](err)
	if !ok || ae.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("List error = %T %v, want the proxy's 407 as an *APIError", err, err)
	}
	for _, secret := range secrets {
		assertNotPrinted(t, err, secret)
	}
	records := recordsText(logs)
	recordWith := func(msg string) string {
		for l := range strings.Lines(records) {
			if strings.Contains(l, msg) {
				return l
			}
		}
		return ""
	}

	t.Run("error: the error's Header (headerRedactor.header)", func(t *testing.T) {
		for _, name := range []string{"X-Proxy-Echo", "X-Proxy-Password", "X-Typesafe-Request-Id"} {
			if got := ae.Header.Values(name); !slices.Equal(got, []string{redacted}) {
				t.Errorf("Header[%s] = %q, want [%q]", name, got, redacted)
			}
		}
	})
	t.Run("error: the request id (headerRedactor.requestID)", func(t *testing.T) {
		if line := recordWith("INFO response"); !strings.Contains(line, "request_id="+redacted) {
			t.Errorf("the INFO response record %q lacks request_id=%s", line, redacted)
		}
		if got, ok := ae.RequestID(); !ok || got != redacted {
			t.Errorf("RequestID() = %q, %t; want %q, true", got, ok, redacted)
		}
	})
	t.Run("error: the DEBUG response headers record (newRedactedHeaders)", func(t *testing.T) {
		line := recordWith("DEBUG response headers")
		for _, name := range []string{"X-Proxy-Echo", "X-Proxy-Password", "X-Typesafe-Request-Id"} {
			if !strings.Contains(line, name+"="+redacted) {
				t.Errorf("the record %q lacks %s=%s", line, name, redacted)
			}
		}
	})
	for _, secret := range secrets {
		if strings.Contains(records, secret) {
			t.Errorf("the records hold %q:\n%s", secret, records)
		}
	}
}

// TestProxyCredentialParityWithAPIKey pins ruling D-W6-secfix-407msg-corr:
// a credential of a proxy the client's transport chose is treated at every
// sink exactly as the client's own API key is, "***" where the key is "***"
// (a response header, the request id, the records that print them) and
// shown where the key is shown by design (the message the server composed,
// the body, and the LevelTrace body record; ruling R103-rev, review W6.2
// N-10). A plain-HTTP API URL sends the request, the API key and the
// proxy's Proxy-Authorization with it, to a proxy whose 407 repeats one
// secret in its body's message, in a header and in the request id: once
// the client's API key, once the proxy's password. Every sink renders the
// two answers alike, the secret in place; no sink gains a redaction.
func TestProxyCredentialParityWithAPIKey(t *testing.T) {
	// The two secrets are of one length, so that the two answers differ in
	// the secret alone.
	const key = "ts_live_QzXjWvKpYbNmHgFd"
	const user, proxyPassword = "proxy-user", "hunter2@proxy-password12"
	const placeholder = "<SECRET>"
	duration := regexp.MustCompile(`duration=\S+`)
	render := func(t *testing.T, secret string) map[string]string {
		t.Helper()
		proxyURL := newEchoingProxy(t, func(string, string) string {
			body := `{"message":"denied ` + secret + `"}`
			return "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Type: application/json\r\n" +
				"X-Echo: seen " + secret + "\r\nX-Typesafe-Request-Id: req-" + secret + "\r\n" +
				"Content-Length: " + strconv.Itoa(len(body)) + "\r\nConnection: close\r\n\r\n" + body
		}).URL()
		proxyURL.User = url.UserPassword(user, proxyPassword)
		logs := testsupport.NewLogRecorder(LevelTrace)
		clearEnv(t)
		c, err := NewClient(WithAPIKey(key), WithBaseURL("http://example.com"), WithProxy(http.ProxyURL(proxyURL)), WithLogger(logs.Logger()))
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		t.Cleanup(func() { _ = c.Close() })
		_, err = callWithin(t, func(ctx context.Context) error {
			_, err := c.Models().List(ctx, Retry(NoRetry()))
			return err
		})
		ae, ok := errors.AsType[*APIError](err)
		if !ok || ae.StatusCode != http.StatusProxyAuthRequired {
			t.Fatalf("List error = %T %v, want the proxy's 407 as an *APIError", err, err)
		}
		id, _ := ae.RequestID()
		out := map[string]string{
			"Error()":                      ae.Error(),
			"%+v":                          fmt.Sprintf("%+v", ae),
			"Message":                      ae.Message,
			"Body":                         string(ae.Body),
			"Header X-Echo":                strings.Join(ae.Header.Values("X-Echo"), ", "),
			"Header X-Typesafe-Request-Id": strings.Join(ae.Header.Values("X-Typesafe-Request-Id"), ", "),
			"RequestID()":                  id,
		}
		for _, r := range logs.Records() {
			level := r.Level.String()
			if r.Level == LevelTrace {
				level = "TRACE"
			}
			out["record "+level+" "+r.Message] = duration.ReplaceAllString(r.String(), "duration=<elapsed>")
		}
		for k, v := range out {
			out[k] = strings.ReplaceAll(v, secret, placeholder)
		}
		return out
	}
	withKey := render(t, key)
	withPassword := render(t, proxyPassword)
	if diff := gocmp.Diff(slices.Sorted(maps.Keys(withKey)), slices.Sorted(maps.Keys(withPassword))); diff != "" {
		t.Fatalf("the sinks differ (-key +password):\n%s", diff)
	}
	for _, sink := range slices.Sorted(maps.Keys(withKey)) {
		t.Run("success: "+sink, func(t *testing.T) {
			if withKey[sink] != withPassword[sink] {
				t.Errorf("the API key's echo renders\n\t%q\nthe proxy password's\n\t%q", withKey[sink], withPassword[sink])
			}
		})
	}
	// What each treatment is, for the record: shown in the message, "***" in
	// the headers.
	if !strings.Contains(withKey["Message"], placeholder) || withKey["Header X-Echo"] != redacted || withKey["RequestID()"] != redacted {
		t.Errorf("Message %q, X-Echo %q, RequestID %q; want the secret shown, %q and %q", withKey["Message"], withKey["Header X-Echo"], withKey["RequestID()"], redacted, redacted)
	}
}

// assertHeaderShown checks that the response header the call's *APIError
// err keeps shows each of want as it arrived, and so do its request id and
// the INFO "response" and DEBUG "response headers" records.
func assertHeaderShown(t *testing.T, err error, logs *testsupport.LogRecorder, want map[string]string) {
	t.Helper()
	ae, ok := errors.AsType[*APIError](err)
	if !ok {
		t.Fatalf("error = %T %v, want an *APIError", err, err)
	}
	records := recordsText(logs)
	line := func(msg string) string {
		for l := range strings.Lines(records) {
			if strings.Contains(l, msg) {
				return l
			}
		}
		return ""
	}
	for name, value := range want {
		if got := ae.Header.Get(name); got != value {
			t.Errorf("Header[%s] = %q, want %q as it arrived", name, got, value)
		}
		if l := line("DEBUG response headers"); !strings.Contains(l, name+"="+value) {
			t.Errorf("the DEBUG response headers record %q lacks %s=%s", l, name, value)
		}
	}
	id := want["X-Typesafe-Request-Id"]
	if got, _ := ae.RequestID(); got != id {
		t.Errorf("RequestID() = %q, want %q", got, id)
	}
	if l := line("INFO response"); !strings.Contains(l, "request_id="+id) {
		t.Errorf("the INFO response record %q lacks request_id=%s", l, id)
	}
}

// TestProxyHeaderScanScope pins ruling D-W6-secfix-header-scope: the
// response header paths (the error types' Header, the request id and the
// DEBUG "response headers" record) look for a proxy's credential only in
// the answer to a plain-HTTP request for which the proxy func returned a
// proxy, where the proxy may have written the answer itself, and only for a
// credential of 8 bytes or more, as for the API key. Over HTTPS a proxy only
// tunnels the API's bytes: an API header that holds the proxy's password,
// short or long, is shown as the API sent it, and so is a request id that
// holds a 2-byte password. A plain-HTTP request the func sent to no proxy
// reaches the API alone, whose header is not scanned either, though the set
// holds a proxy's password. A 2-byte password a plain-HTTP proxy repeats in
// its own answer's header is shown: the residual the minimum leaves, as for
// a short API key. Errors' text is scrubbed of every length
// (TestProxyEchoHoldsNoCredential); a password of 8 bytes or more in a
// plain-HTTP proxy's own answer is "***" (TestProxyAnswerHeadersRedacted).
func TestProxyHeaderScanScope(t *testing.T) {
	const user = "proxy-user"
	const long = "hunter2@proxy-password"
	apiAnswer := func(id, ordinary string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Typesafe-Request-Id", id)
			w.Header().Set("X-Ordinary", ordinary)
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"no such model"}`)
		})
	}
	listOnce := func(t *testing.T, c *Client) error {
		t.Helper()
		_, err := callWithin(t, func(ctx context.Context) error {
			_, err := c.Models().List(ctx, Retry(NoRetry()))
			return err
		})
		return err
	}
	overHTTPS := func(t *testing.T, password string, want map[string]string) {
		t.Helper()
		srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: apiAnswer(want["X-Typesafe-Request-Id"], want["X-Ordinary"])})
		p := testsupport.NewProxy(t, testsupport.ProxyPlain, testsupport.Routes{"example.com:443": srv.Addr()})
		pu := p.URL()
		pu.User = url.UserPassword(user, password)
		logs := testsupport.NewLogRecorder(slog.LevelDebug)
		c := newCredentialClient(t, logs.Logger(), WithBaseURL("https://example.com"), WithRootCAs(testsupport.RootCAs(t)), WithProxy(http.ProxyURL(pu)))
		assertHeaderShown(t, listOnce(t, c), logs, want)
	}

	t.Run("success: a 2-byte password over HTTPS: request ids and ordinary values as the API sent them", func(t *testing.T) {
		overHTTPS(t, "ab", map[string]string{"X-Typesafe-Request-Id": "req_ab12", "X-Ordinary": "cab ab"})
	})

	t.Run("success: a password of 8 bytes or more over HTTPS: the API's header is not scanned", func(t *testing.T) {
		overHTTPS(t, long, map[string]string{"X-Typesafe-Request-Id": "req-" + long, "X-Ordinary": "seen " + long})
	})

	t.Run("success: a 2-byte password a plain-HTTP proxy repeats in its own header is shown (the residual)", func(t *testing.T) {
		want := map[string]string{"X-Typesafe-Request-Id": "req_ab12", "X-Echo": "seen ab"}
		pu := newEchoingProxy(t, func(pw, _ string) string {
			return "HTTP/1.1 407 Proxy Authentication Required\r\nX-Echo: seen " + pw + "\r\nX-Typesafe-Request-Id: req_" + pw + "12\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
		}).URL()
		pu.User = url.UserPassword(user, "ab")
		logs := testsupport.NewLogRecorder(slog.LevelDebug)
		c := newCredentialClient(t, logs.Logger(), WithBaseURL("http://example.com"), WithProxy(http.ProxyURL(pu)))
		assertHeaderShown(t, listOnce(t, c), logs, want)
	})

	t.Run("success: a 13-byte word of the password in a plain-HTTP proxy's own header is shown; the whole password is not", func(t *testing.T) {
		const words = "open sesame-street now" // "sesame-street" is a word of it, 13 bytes
		pu := newEchoingProxy(t, func(pw, _ string) string {
			return "HTTP/1.1 407 Proxy Authentication Required\r\nX-Word: seen sesame-street\r\nX-Whole: seen " + pw +
				"\r\nX-Typesafe-Request-Id: req_plain\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
		}).URL()
		pu.User = url.UserPassword(user, words)
		logs := testsupport.NewLogRecorder(slog.LevelDebug)
		c := newCredentialClient(t, logs.Logger(), WithBaseURL("http://example.com"), WithProxy(http.ProxyURL(pu)))
		err := listOnce(t, c)
		assertHeaderShown(t, err, logs, map[string]string{"X-Typesafe-Request-Id": "req_plain", "X-Word": "seen sesame-street"})
		if ae, _ := errors.AsType[*APIError](err); ae == nil || ae.Header.Get("X-Whole") != redacted {
			t.Errorf("X-Whole = %q, want %q: the whole password is a header's needle", ae.Header.Get("X-Whole"), redacted)
		}
	})

	t.Run("success: a plain-HTTP 429 through a proxy, with no echo, keeps its Retry-After and request id", func(t *testing.T) {
		for _, password := range []string{"x", "12", "open 2 sesame"} {
			pu := newEchoingProxy(t, func(string, string) string {
				return "HTTP/1.1 429 Too Many Requests\r\nRetry-After: 2\r\nX-Typesafe-Request-Id: req-2026-x12\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
			}).URL()
			pu.User = url.UserPassword(user, password)
			logs := testsupport.NewLogRecorder(slog.LevelDebug)
			c := newCredentialClient(t, logs.Logger(), WithBaseURL("http://example.com"), WithProxy(http.ProxyURL(pu)))
			err := listOnce(t, c)
			assertHeaderShown(t, err, logs, map[string]string{"Retry-After": "2", "X-Typesafe-Request-Id": "req-2026-x12"})
			if ae, _ := errors.AsType[*APIError](err); ae != nil {
				if d, ok := ae.RetryAfter(); !ok || d != 2*time.Second {
					t.Errorf("password %q: RetryAfter() = %v, %t; want 2s, true", password, d, ok)
				}
			}
		}
	})

	t.Run("success: a plain-HTTP request sent to no proxy: the API's header is not scanned", func(t *testing.T) {
		want := map[string]string{"X-Typesafe-Request-Id": "req-" + long, "X-Ordinary": "seen " + long}
		api := httptest.NewServer(apiAnswer(want["X-Typesafe-Request-Id"], want["X-Ordinary"]))
		t.Cleanup(api.Close)
		pu := newEchoingProxy(t, malformedEcho).URL()
		pu.User = url.UserPassword(user, long)
		var calls atomic.Int64
		choose := func(*http.Request) (*url.URL, error) {
			if calls.Add(1) == 1 {
				return pu, nil // the first call, through the proxy: its password joins the set
			}
			return nil, nil // the second, to the API alone
		}
		logs := testsupport.NewLogRecorder(slog.LevelDebug)
		c := newCredentialClient(t, logs.Logger(), WithBaseURL(api.URL), WithProxy(choose))
		if _, ok := errors.AsType[*ConnectionError](listOnce(t, c)); !ok {
			t.Fatal("the first call, through the proxy, did not fail as the proxy's malformed answer makes it")
		}
		if c.cfg.transport.proxies.credentials() == nil {
			t.Fatal("the set holds no credential after the first call")
		}
		logs.Reset()
		assertHeaderShown(t, listOnce(t, c), logs, want)
	})
}

// TestProxyRetryAfterSurvivesOverHTTPS pins ruling
// D-W6-secfix-header-scope-2 where the W6.2 DELTA found a consequence past
// the text: with a proxy password of a 1-byte word, "open 2 sesame", over
// HTTPS through a CONNECT tunnel, the API's `Retry-After: 2` became "***",
// RetryAfter() found none, and the retry fired after the backoff instead of
// the server's 2 s. The API's header, which a tunnelling proxy cannot
// write, is not scanned: for passwords of 1 and 2 bytes and one with a
// 1-byte word, Retry-After and a request id that holds the password's bytes
// stay as the server sent them, in the error's Header, RetryAfter(),
// RequestID(), Error() and the records; and the retry waits the server's
// 2 s.
func TestProxyRetryAfterSurvivesOverHTTPS(t *testing.T) {
	tests := map[string]struct {
		password, id string
		retry        bool // retry once and measure the wait
	}{
		"success: a 1-byte password":                                    {password: "x", id: "req-2026-x12"},
		"success: a 2-byte password":                                    {password: "12", id: "req-2026-x12"},
		"success: a password with a 1-byte word, retried after the 2 s": {password: "open 2 sesame", id: "req-2026-hunter2-x", retry: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var arrived []time.Time
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				arrived = append(arrived, time.Now())
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "2")
				w.Header().Set("X-Typesafe-Request-Id", tt.id)
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"message":"slow down"}`)
			})})
			p := testsupport.NewProxy(t, testsupport.ProxyPlain, testsupport.Routes{"example.com:443": srv.Addr()})
			pu := p.URL()
			pu.User = url.UserPassword("proxy-user", tt.password)
			logs := testsupport.NewLogRecorder(slog.LevelDebug)
			c := newCredentialClient(t, logs.Logger(), WithBaseURL("https://example.com"), WithRootCAs(testsupport.RootCAs(t)), WithProxy(http.ProxyURL(pu)))
			policy := NoRetry()
			if tt.retry {
				policy = fastRetry().MaxRetries(1)
			}
			_, err := callWithin(t, func(ctx context.Context) error {
				_, err := c.Models().List(ctx, Retry(policy))
				return err
			})
			ae, ok := errors.AsType[*APIError](err)
			if !ok || ae.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("List error = %T %v, want the API's 429 as an *APIError", err, err)
			}
			if d, ok := ae.RetryAfter(); !ok || d != 2*time.Second {
				t.Errorf("RetryAfter() = %v, %t; want 2s, true", d, ok)
			}
			if !strings.Contains(ae.Error(), "request_id="+tt.id) {
				t.Errorf("Error() = %q, want request_id=%s", ae.Error(), tt.id)
			}
			assertHeaderShown(t, err, logs, map[string]string{"Retry-After": "2", "X-Typesafe-Request-Id": tt.id})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case !tt.retry && len(arrived) != 1:
				t.Errorf("the server got %d requests, want 1", len(arrived))
			case tt.retry && len(arrived) != 2:
				t.Errorf("the server got %d requests, want 2: the first and its retry", len(arrived))
			case tt.retry && arrived[1].Sub(arrived[0]) < 2*time.Second:
				t.Errorf("the retry came %v after the first request, want the server's 2s at least", arrived[1].Sub(arrived[0]))
			}
		})
	}
}

// keepAliveProxy is an HTTP/1.1 proxy on 127.0.0.1 that answers every
// request a connection carries with answer(password, auth), keeping the
// connection open, as newEchoingProxy's proxy answers one; accepts and
// requests count the connections and the requests.
type keepAliveProxy struct {
	ln                net.Listener
	accepts, requests atomic.Int64
	wg                sync.WaitGroup
}

// newKeepAliveProxy starts a keepAliveProxy; it stops when the test ends.
func newKeepAliveProxy(t *testing.T, answer func(password, auth string) string) *keepAliveProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := &keepAliveProxy{ln: ln}
	p.wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			p.accepts.Add(1)
			p.wg.Go(func() {
				defer conn.Close()
				br := bufio.NewReader(conn)
				for {
					req, err := http.ReadRequest(br)
					if err != nil {
						return
					}
					_, _ = io.Copy(io.Discard, req.Body)
					p.requests.Add(1)
					auth := req.Header.Get("Proxy-Authorization")
					var password string
					if b64, ok := strings.CutPrefix(auth, "Basic "); ok {
						if raw, err := base64.StdEncoding.DecodeString(b64); err == nil {
							_, password, _ = strings.Cut(string(raw), ":")
						}
					}
					if _, err := io.WriteString(conn, answer(password, auth)); err != nil {
						return
					}
				}
			})
		}
	})
	t.Cleanup(func() {
		_ = ln.Close()
		p.wg.Wait()
	})
	return p
}

// TestProxyKeepAliveSecondRequestHidden pins the W6.2 probe's keep-alive
// case: a plain-HTTP request through a proxy whose connection net/http
// keeps and reuses for the next request, which it sends without dialing.
// net/http still asks the proxy func for it, to build the connection's key
// (GOROOT/src/net/http/transport.go:1051-1058), so the wrapper marks it,
// and the proxy's own 407, which repeats the Proxy-Authorization and the
// password in its header and the password in its message, has the header
// hidden on the second request as on the first, and the message shown on
// both, as the API key would be (parity, D-W6-secfix-407msg-corr).
func TestProxyKeepAliveSecondRequestHidden(t *testing.T) {
	const user, password = "proxy-user", "hunter2@proxy-password"
	p := newKeepAliveProxy(t, func(pw, auth string) string {
		body := `{"message":"denied ` + pw + `"}`
		return "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Type: application/json\r\n" +
			"X-Echo: seen " + auth + "\r\nX-Password: seen " + pw + "\r\nX-Typesafe-Request-Id: req-" + strings.TrimPrefix(auth, "Basic ") + "\r\n" +
			"Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
	})
	pu := &url.URL{Scheme: "http", User: url.UserPassword(user, password), Host: p.ln.Addr().String()}
	logs := testsupport.NewLogRecorder(slog.LevelDebug)
	c := newCredentialClient(t, logs.Logger(), WithBaseURL("http://example.com"), WithProxy(http.ProxyURL(pu)))
	for i := range 2 {
		_, err := callWithin(t, func(ctx context.Context) error {
			_, err := c.Models().List(ctx, Retry(NoRetry()))
			return err
		})
		ae, ok := errors.AsType[*APIError](err)
		if !ok || ae.StatusCode != http.StatusProxyAuthRequired {
			t.Fatalf("request %d: error = %T %v, want the proxy's 407 as an *APIError", i+1, err, err)
		}
		for _, name := range []string{"X-Echo", "X-Password", "X-Typesafe-Request-Id"} {
			if got := ae.Header.Values(name); !slices.Equal(got, []string{redacted}) {
				t.Errorf("request %d: Header[%s] = %q, want [%q]", i+1, name, got, redacted)
			}
		}
		if id, _ := ae.RequestID(); id != redacted {
			t.Errorf("request %d: RequestID() = %q, want %q", i+1, id, redacted)
		}
		if !strings.Contains(ae.Message, password) {
			t.Errorf("request %d: Message = %q, want the password shown as the proxy wrote it (parity with the API key)", i+1, ae.Message)
		}
	}
	if p.accepts.Load() != 1 || p.requests.Load() != 2 {
		t.Fatalf("the proxy accepted %d connections for %d requests, want 1 for 2: the second on the kept connection", p.accepts.Load(), p.requests.Load())
	}
	headerRecords := 0
	for l := range strings.Lines(recordsText(logs)) {
		if !strings.Contains(l, "DEBUG response headers") {
			continue
		}
		headerRecords++
		if !strings.Contains(l, "X-Echo="+redacted) || !strings.Contains(l, "X-Password="+redacted) {
			t.Errorf("the record %q shows the proxy's echo", l)
		}
	}
	if headerRecords != 2 {
		t.Errorf("%d DEBUG response headers records, want 2", headerRecords)
	}
}
