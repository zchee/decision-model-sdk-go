# Uncovered lines

Every statement block that the tests never run, outside the paths
`.codecov.yaml` ignores, with the reason it is not run (AC-Q2 of the port
plan). `.github/scripts/uncovered-lines.py` checks this file against the
coverage profile of the ubuntu-26.04 test job in CI (the
`go test -race -coverprofile` step): it fails on a block without a row, on a
row whose block is covered now or whose code is gone, and on a count that
differs, so the table cannot fall behind the code. The measurements behind
it are in [`perf/ledger.md`](perf/ledger.md) `## W6.3`.

A row names blocks by their file, the top-level function that holds them
(or `var name` for a function literal in a package-level variable) and their
first line of code, not by line numbers, so an edit elsewhere in a file
leaves the table valid; blocks with the same three cells share a row.
`Blocks` is the number of zero-count blocks with that key, or a range
`low-high` for a block that timing covers in some runs and not in others.
When the checker reports an unlisted block it prints the row to paste:
replace its `<one-line reason>` with the reason.

Each reason starts with its class:

- **Defensive:** not reachable through the public API, by construction; the
  reason gives the argument. A block that some caller of the public API can
  reach, however unlikely the input, is a Gap.
- **Race:** a timing window that no test provokes on purpose. Its row is
  always a range (`0-1`), since a run's scheduling may cover it.
- **Gap:** reachable, and no test sends the input the reason names; a test
  that sends it would cover the block.
- **Other package:** covered by another package's tests: `go test -cover`
  credits a package with its own tests only.

| File | Function | Code | Blocks | Reason |
| --- | --- | --- | --- | --- |
| `client.go` | `(*Client).attempt` | `return wire.ResponseMeta{}, newConnectionError(err.Error(), err, false)` | 1 | Defensive: `Body.Open` fails only after the body's last reference is dropped, and the call holds one until it returns. |
| `client.go` | `readBody` | `return nil, errTooLarge` | 1 | Gap: a body reader that returns the byte past the limit together with an error (`iotest.DataErrReader`); the readers in tests return `io.EOF` on a later call, so the full-buffer check refuses the body first. |
| `config.go` | `resolveEndpoints` | `return nil, nil, newConfigError(source + " is not a valid URL.")` | 2 | Defensive: `baseURLRule` has parsed the base, and a valid base followed by a fixed path always parses. |
| `config.go` | `dropDefaultPort` | `return raw` | 2 | Defensive: `baseURLRule` admits only http and https URLs with a host, so the text always holds `://` and one of the two schemes. |
| `errors.go` | `(*ConfigError).typesafeError` | `{}` | 1 | Defensive: the unexported marker method that seals the `Error` interface; nothing calls it. |
| `errors.go` | `(*InvalidRequestError).typesafeError` | `{}` | 1 | Defensive: the unexported marker method that seals the `Error` interface; nothing calls it. |
| `errors.go` | `(*APIError).typesafeError` | `{}` | 1 | Defensive: the unexported marker method that seals the `Error` interface; nothing calls it. |
| `errors.go` | `(*ResponseValidationError).typesafeError` | `{}` | 1 | Defensive: the unexported marker method that seals the `Error` interface; nothing calls it. |
| `errors.go` | `(*ResponseTooLargeError).typesafeError` | `{}` | 1 | Defensive: the unexported marker method that seals the `Error` interface; nothing calls it. |
| `errors.go` | `(*ConnectionError).typesafeError` | `{}` | 1 | Defensive: the unexported marker method that seals the `Error` interface; nothing calls it. |
| `errors.go` | `(*TimeoutError).typesafeError` | `{}` | 1 | Defensive: the unexported marker method that seals the `Error` interface; nothing calls it. |
| `errors.go` | `parsePythonFloat` | `return 0, false` | 1 | Defensive: the scan above admits only text that `strconv.ParseFloat` parses, and `ErrRange` is accepted. |
| `questions.go` | `(*Questions).Prepare` | `return nil, newConfigError("Question set cannot be prepared: "+err.Error(), err)` | 1 | Defensive: the names were checked above, and `Finish` fails only on them. |
| `questions.go` | `falsyJSON` | `return false` | 2 | Defensive: `maybeFalsyJSON` lets through only a value that starts with `n`, `f`, `""`, an empty array or object, or a number with no digit 1-9 before its exponent; a valid one of those compacts to `null`, `false`, `""`, `[]`, `{}` or that number spelled as written (`wire.AppendJSON`), so neither return is reached (probed over every value of up to 5 bytes, 2026-09-26). |
| `retry.go` | `(*retryState).wait` | `return waitError(ctx)` | 0-1 | Race: a context that ends at the instant the backoff timer fires. |
| `retry.go` | `roundMillis` | `return seconds` | 1 | Defensive: `'f'` formatting always parses back. |
| `text.go` | `(*pathText).fixed` | `return` | 1 | Gap: a field path that is already cut when more of the SDK's own text follows. |
| `text.go` | `(*pathText).fixed` | `t.b = append(t.b, "\u2026"...)` | 1 | Gap: a field path whose own text, not a name, crosses the path limit. |
| `text.go` | `(*pathText).name` | `return` | 1 | Gap: a field path that is already cut when another name follows. |
| `text.go` | `(*pathText).name` | `t.full = true` | 1 | Gap: a name cut by the room left in the path rather than by its own limit. |
| `text.go` | `credentials.detail` | `continue` | 1 | Gap: a wrapped chain with a nil link (an `Unwrap` that returns nil); no error chain in tests has one. |
| `text.go` | `credentials.inChain` | `continue` | 1 | Gap: a wrapped chain with a nil link (an `Unwrap` that returns nil); no error chain in tests has one. |
| `transport.go` | `var traceKeys` | `{}` | 1 | Defensive: a `ConnectStart` hook that exists only so that `httptrace` sets its net-level key; the context never dials. |
| `typed.go` | `kindKeys` | `return "a choice takes kind, name, instructions, options and optional"` | 1 | Gap: a tag key outside its kind on a choice field; only the noul message is tested. |
| `typed.go` | `kindKeys` | `return "a score takes kind, name, instructions, levels and optional"` | 1 | Gap: a tag key outside its kind on a score field; only the noul message is tested. |
| `typed.go` | `lookupTag` | `break` | 2 | Gap: a struct tag that ends in spaces, and an unterminated value under a key other than `typesafe`. |
| `typed.go` | `(*tagSpec).keyOutside` | `return "yes"` | 1 | Gap: `yes=` on a choice or score field; only a misplaced `options=` is tested. |
| `typed.go` | `(*tagSpec).keyOutside` | `return "no"` | 1 | Gap: `no=` on a choice or score field; only a misplaced `options=` is tested. |
| `typed.go` | `(*tagSpec).keyOutside` | `return "levels"` | 1 | Gap: `levels=` on a noul or choice field; only a misplaced `options=` is tested. |
| `typed.go` | `parseOptions` | `return nil, err` | 1 | Gap: an unknown escape inside an option's description. |
| `typed.go` | `parseLevels` | `return nil, err` | 1 | Gap: an unknown escape inside a level. |
| `typed.go` | `unescape` | `return "", errUnterminated` | 1 | Defensive: `parseTag`'s entry scan refuses a trailing backslash, so every backslash here has a byte after it. |
| `internal/codec/commit.go` | `wrongKindErr` | `return errNotString` | 1 | Gap: a choice answer whose `choice` is a number or a container; `mType` never gets here, since `commitAnswer` reports it first. |
| `internal/codec/commit.go` | `firstFailure` | `break` | 1 | Gap: a legend or probability list whose unparseable key comes before its first bad value. |
| `internal/codec/commit.go` | `(*visitor).foldLegend` | `j = k` | 1 | Gap: a legend of more than 16 levels (the map path) that repeats a level. |
| `internal/codec/commit.go` | `(*visitor).foldLevels` | `j = k` | 1 | Gap: score probabilities over more than 16 levels (the map path) that repeat a level. |
| `internal/codec/commit.go` | `(*visitor).foldLabels` | `j = k` | 1 | Gap: choice probabilities with more than 16 labels (the map path) that repeat a label. |
| `internal/codec/commit.go` | `(*visitor).find` | `return i` | 1 | Gap: a response of more than 8 answers (the index path) that repeats an answer name. |
| `internal/codec/decode.go` | `(*DecodeError).Error` | `reason = "syntax error at byte " + strconv.Itoa(syntax.Pos) + ": " + syntax.Message()` | 1 | Defensive: only the lazy pass returns an `ast.SyntaxError`, and it re-reads a body the visitor has already accepted; W6.1 adds `FuzzDecodePaths`, a visitor-vs-lazy-pass fuzz differential, to check that sonic's two parsers agree. |
| `internal/codec/decode.go` | `(*decoder).traverse` | `return jsonErr(err)` | 1 | Defensive: `(*visitor).OnObjectEnd` calls `end`, which returns nil on every path; the error result is the one sonic's `ast.Visitor` interface declares. |
| `internal/codec/decode.go` | `(*decoder).finish` | `return skipped, err` | 1 | Defensive: the lazy pass re-reads a body the visitor has already accepted, so it does not fail; W6.1 adds `FuzzDecodePaths` to check that sonic's two parsers agree. |
| `internal/codec/decode.go` | `(*decoder).option` | `return "", false` | 1 | Gap: a choice answer to a question of more than 16 options (the index path) whose choice or label is not one of them. |
| `internal/codec/decode.go` | `arenaString` | `return ""` | 1 | Gap: an empty string that the decoder copies: a choice `""` that is not an option, or an empty model-card field. |
| `internal/codec/encode.go` | `EncodeState` | `err = fmt.Errorf("%s encodes as nothing, %w", typeName(state), ErrStateShape)` | 1 | Defensive: sonic v1.15.4 refuses an empty or blank `MarshalJSON` output inside `EncodeInto`, an empty `json.RawMessage` and a nil `MarshalJSON` result included (probed 2026-09-26), and every other value encodes to at least one byte. |
| `internal/codec/errorbody.go` | `locPath` | `continue` | 1 | Gap: an error body whose `detail[].loc` holds an object or an array. |
| `internal/codec/errorbody.go` | `replaceInvalidUTF8` | `sb.Write(b[i : i+size])` | 1 | Gap: an error body that mixes invalid UTF-8 with a valid multi-byte character. |
| `internal/codec/errorbody.go` | `maximalSubpart` | `need = 1` | 1 | Gap: invalid UTF-8 led by a byte in C2-DF. |
| `internal/codec/errorbody.go` | `maximalSubpart` | `need = 3` | 1 | Gap: invalid UTF-8 led by a byte in F1-F3. |
| `internal/codec/lazy.go` | `(*decoder).lazy` | `return jsonErr(err)` | 6 | Defensive: the lazy pass re-reads, with sonic's ast, a body the visitor has already accepted, so sonic does not fail on it; W6.1 adds `FuzzDecodePaths` to check that the two parsers agree. |
| `internal/codec/lazy.go` | `(*decoder).lazy` | `continue` | 1 | Defensive: the visitor refuses a legend key that is not a level before the lazy pass runs. |
| `internal/codec/lazy.go` | `(*decoder).lazy` | `return jsonErr(errLazyMissed) // unreachable: both passes read the same members` | 1 | Defensive: both passes read the same members. |
| `internal/codec/scratch.go` | `Body.Open` | `panic("codec: request body reference count overflow")` | 1 | Gap: a `WithRoundTripper` that calls `req.GetBody` 2^31-1 times without closing a reader (each call opens a reference); reachable in principle, impractical to test (minutes of CPU, bounded memory: nothing finalizes an abandoned reader, so the collector frees it while its reference stays counted). |
| `internal/codec/scratch.go` | `(*scratch).unref` | `panic("codec: request body reference dropped twice")` | 1 | Defensive: an invariant panic: `BodyReader.Close` drops its reference once (a compare-and-swap on its closed flag), and the call drops its own once. |
| `internal/codec/visitor.go` | `(*visitor).push` | `return errReadDepth // unreachable: every readable container is at most maxDepth deep` | 1 | Defensive: `begin` refuses a container once the depth reaches `maxNesting` (ruling R73), before `push` can fill the stack. |
| `internal/codec/visitor.go` | `(*visitor).begin` | `return err` | 1 | Defensive: `push` fails only at `maxDepth`, and the models array is at depth 1. |
| `internal/codec/visitor.go` | `(*visitor).wrongKind` | `v.probs = append(v.probs, probPair{key: v.curKey, bad: true})` | 1 | Gap: a probability value that is an object or an array. |
| `internal/codec/visitor.go` | `(*visitor).wrongKind` | `v.cardBad \|= cardDescription` | 1 | Gap: a model card whose `description` is an object or an array. |
| `internal/codec/visitor.go` | `(*visitor).wrongKind` | `v.cardBad \|= cardReleaseDate` | 1 | Gap: a model card whose `release_date` is an object or an array. |
| `internal/codec/visitor.go` | `(*visitor).OnNull` | `v.usage.OutputTokens, v.usage.HasOutputTokens, v.outBad = 0, false, false` | 1 | Other package: root-package tests send `"output_tokens": null`; internal/codec's own tests null only `input_tokens`. |
| `internal/codec/visitor.go` | `(*visitor).OnInt64` | `return v.scalar(false, "", n)` | 1 | Defensive: with `OnlyNumber` set, sonic v1.15.4's parser skips number conversion and reports every number, integers included, through `OnFloat64`. |
| `internal/codec/visitor.go` | `(*visitor).OnObjectKey` | `v.slot = slotIgnore` | 1 | Defensive: keys occur only inside objects, and every object container has its own case; the models array, the one other container, gets no key. |
| `internal/h2gate/config.go` | `alpnScope.String` | `return [...]string{"none", "every-handshake", "sni", "post-check-only"}[s]` | 1 | Defensive: called only when a failing test prints a scope. |
| `internal/h2gate/config.go` | `proxyMayApply` | `return false, fmt.Errorf("%w: %w", ErrProxyEnvironment, err)` | 1 | Gap: `http.ProxyFromEnvironment` reads the environment once per process, so only a subprocess started with an invalid `HTTPS_PROXY` reaches it. |
| `internal/h2gate/config.go` | `NewTransport` | `return nil, err` | 1 | Gap: the `proxyMayApply` failure above, which needs an invalid proxy environment in a subprocess. |
| `internal/h2gate/config.go` | `Wrap` | `return nil, err` | 2 | Gap: `Wrap` with an API URL that `resolveTarget` refuses (tested through `NewTransport` only), and the `proxyMayApply` failure. |
| `internal/h2gate/errors.go` | `walk` | `return` | 1 | Gap: an error whose `Unwrap() error` returns nil; the dial errors in tests always wrap a cause. |
| `internal/h2gate/transport.go` | `nopLogger.WarnContext` | `{}` | 1 | Gap: the one warning, `h2: response not HTTP/2`, is reached only by tests that attach a logger. |
| `internal/h2gate/transport.go` | `(*Transport).RoundTrip` | `t.mu.Unlock()` | 0-1 | Race: a waiter that loops back and finds the connection warm; covered in 4 of 15 runs at 67dcbb0 and in 3 of 6 on f73ab2b's production files ((M) on 16 cores, (L) on 44 and pinned to 4; -race and not). |
| `internal/h2gate/transport.go` | `(*Transport).RoundTrip` | `t.leave(gen)` | 0-1 | Race: a waiter whose context ends while the leader dials; no test cancels a waiter on purpose. |
| `internal/h2gate/transport.go` | `reason` | `return "proxy"` | 1 | Gap: no test logs a proxy dial failure with a logger attached. |
| `internal/h2gate/transport.go` | `reason` | `return "not-negotiated"` | 1 | Gap: no test logs a failed ALPN negotiation with a logger attached. |
| `internal/h2gate/transport.go` | `(*Transport).send` | `closeBody(req)` | 0-1 | Race: a request whose context ends while it waits for the header-write token (K28d, W6.1). |
| `internal/h2gate/transport.go` | `(*call).gotConn` | `{}` | 1 | Gap: a new HTTP/2 connection for a call that holds the token on a transport with `firstHold` cleared. |
| `internal/h2gate/transport.go` | `(*call).wroteHeaders` | `tm.Stop()` | 0-1 | Race: `WroteHeaders` arriving on the write goroutine after `send` has given the token back. |
| `internal/h2gate/transport.go` | `(*Transport).markUnsettled` | `return false` | 1 | Gap: a caller TLS dialer (`WithHTTPTransport`) that returns a non-comparable `net.Conn` value, on a replay that opens a new connection while the call holds the token; the stock connection types are pointers. |
| `internal/wire/prepared.go` | `(*Builder).GrowLevels` | `b.spans = slices.Grow(b.spans, n)` | 1 | Other package: its one caller is the root package's `(*Questions).Prepare`, which root-package tests run; internal/wire's own tests do not call it. |
| `internal/wire/prepared.go` | `(*Builder).Choice` | `return err` | 1 | Gap: a choice question name that is not valid UTF-8; the refusal in `begin` is tested through `Noul` only. |
| `internal/wire/prepared.go` | `(*Builder).Score` | `return err` | 1 | Gap: a score question name that is not valid UTF-8; the refusal in `begin` is tested through `Noul` only. |
