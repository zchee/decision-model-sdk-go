# Deviations from typesafe-sdk-python 0.7.1

typesafe-sdk-go ports [typesafe-sdk-python](https://github.com/typesafe-ai/typesafe-sdk-python)
0.7.1 (commit `0ffd094`) and behaves as it does, except in the rows below.
This page is the one home of the deviation table: the port plan's
Appendix B, as the waves built it and as the rulings named in the rows
amended it.

The key of each row is the text a row of
[`port-test-matrix.md`](port-test-matrix.md) cites (`deviation "<key>"`),
and the last column lists the matrix rows that cite it, or `—` when no
upstream test reaches the behaviour. `.github/scripts/port-test-matrix.py
--deviations docs/deviations.md` checks both directions in CI: every
citation is a key here, and every key lists exactly the rows that cite it.

## Client, configuration and transport

| Key | Python SDK 0.7.1 | Go SDK | Why | Matrix rows |
| --- | --- | --- | --- | --- |
| Sync and async clients → one `*Client`, `context.Context` | `TypeSafeClient` and `AsyncTypeSafeClient` | one `*Client`, safe for concurrent use; every call takes a `context.Context` | Go runs concurrent calls on goroutines, not an event loop | RT14 |
| one transport option, two kinds | `transport=` and `http_client=`, mutually exclusive; a supplied client is closed with the SDK client | `WithHTTPTransport` (a clone the SDK configures) or `WithRoundTripper` (used as it is), mutually exclusive; `Close` closes a supplied `io.Closer` and idles a supplied `*http.Transport`; a call after `Close` fails with `ErrClientClosed` | one option per kind of transport | F1 |
| one deadline per attempt | httpx per-phase timeouts; `timeout=` a number or an `httpx.Timeout` | one deadline per attempt: `WithTimeout`, or a call's `Timeout`; `WithConnectTimeout`; `WithNoTimeout`; each retry gets the deadline anew | a Go context carries one deadline | C14, F10 |
| a custom transport owns its timeouts | `http_client.timeout` takes precedence over the SDK's | a `WithRoundTripper` transport owns its connection timeouts; the per-attempt deadline still applies | the SDK cannot see inside an opaque RoundTripper | F11 |
| HTTP/2 only on https | httpx lets ALPN choose HTTP/1.1 or HTTP/2 | on an https base URL, `HTTP2Only` by default: one connection, a cold-start gate, strict stream accounting, and an API handshake that does not negotiate h2 fails with `ErrHTTP2NotNegotiated` before any byte is sent; `WithHTTPVersion(HTTPAuto)` lets ALPN choose; an http base URL defaults to `HTTPAuto` | one connection from a cold start, and a loud failure instead of a silent HTTP/1.1 fallback (plan D2) | — |
| the ALPN check applies to the API hop | httpx honours the proxy environment variables | `http.ProxyFromEnvironment` by default, `WithProxy` to change it; the h2 check applies to the API's handshake, never to the proxy's; a proxy failure is a `*ConnectionError` whose `Proxy()` is true; a proxy that negotiates h2 on its own hop is not supported | the proxy support of Go's transport | — |
| configuration checked at build | the base URL is checked at the first request; an empty explicit default model is sent | `NewClient` refuses, with a `*ConfigError` that does not repeat the value, a base URL with userinfo, a query, a fragment, no host or a scheme other than http and https, a blank model, and a response limit over 1 GiB | fail fast (ruling R63) | — |
| SDK headers | `User-Agent` and `X-TypeSafe-SDK` name the Python SDK; the runtime header names Python | `typesafe-sdk-go/<version>` and `X-TypeSafe-Runtime: go/<version> (<GOOS>; <GOARCH>)`; `WithUserAgentProduct` adds a product, `WithRuntimeHeader(false)` leaves the runtime header out | this is not the official SDK | — |
| caller headers | a caller's framing headers are sent; the defaults mapping sends both spellings of a header | framing headers are dropped; `WithHeader` replaces a header whatever its case; a header name that holds the key is refused | the transport frames requests; one value per header (rulings R66, R68) | — |
| supported platforms | any platform Python runs on | the Go releases, on amd64 and arm64, that [support.md](support.md) lists; elsewhere the build fails on purpose with a compile error that names the requirement | sonic, the SDK's only JSON codec, supports those alone (plan D1) | — |

## Requests

| Key | Python SDK 0.7.1 | Go SDK | Why | Matrix rows |
| --- | --- | --- | --- | --- |
| `any` state | `state=` takes a `str` (its subclasses as strings) and abstract mappings and sequences; a top-level `None` is refused | `state` is `any`: a string, a map, a slice, a struct, `RawJSON` or `Content`; `nil`, a number, a boolean, a plain `[]byte` and a string that is not valid UTF-8 are refused with an `*InvalidRequestError` before anything is sent | Go's types (rulings R48, R49) | T1, T2 |
| state encoding | JSON as Python writes it: members in insertion order, floats as `repr` spells them | sonic's encoding: a map's members in Go's iteration order, an integral float without `.0`, `-0.0` as `0` on arm64 and `-0` on amd64, `\b` and `\f` in a string as `\u0008` and `\u000c`; a struct or `RawJSON` gives exact bytes | the codec (rulings R46, R47, R55) | — |
| NaN and infinities refused | NaN and ±Infinity are written as `NaN` and `Infinity` | an `*InvalidRequestError` in a state, a `*ConfigError` in a raw question | they are not JSON | — |
| duplicate question names refused | a repeated name keeps the last question | `Prepare` refuses a repeated name, and `PreparedFor[T]` a repeated wire name, with a `*ConfigError` | fail fast | — |
| Typed noul sends `null` outcomes / empty criteria | a typed noul sends `null` outcomes and empty criteria | unset members are left off the wire; a `RawQuestion` sends any shape | the same meaning to the API | Q4, Q8 |
| not representable | typed questions validate on construction and refuse unknown fields | a Go struct literal cannot hold an unknown field or a value of the wrong type; `Prepare` checks the rest | the type system | Q6, Q7, Q9 |
| raw question field order | a raw question's fields in insertion order | after `type`, in sorted key order | a Go map has no order (ruling R38) | — |
| `Prepare` fails with `*ConfigError` | the four normalisation rules raise `TypeSafeError` | every failure of `Prepare` is a `*ConfigError`; a fault in one call's state is an `*InvalidRequestError` | one error type per phase (ruling R36) | — |

## Responses and decoding

| Key | Python SDK 0.7.1 | Go SDK | Why | Matrix rows |
| --- | --- | --- | --- | --- |
| responses are values | responses are pickled and copied; a copy keeps `raw_http_response` | a response is a value; a copy shares the read-only `Meta()` | there are no process pools to cross | R6 |
| empty `Meta()` | a response built without an HTTP response raises on `raw_http_response` | a response read back with `UnmarshalJSON` has an empty `Meta()` | no lifecycle errors | R7 |
| unexported fields with getters | frozen pydantic models | unexported fields behind getters | immutable values | R14 |
| `iter.Seq2` filters | `.nouls`, `.choices` and `.scores`: cached dicts left out of the serialised form | `Answers().Nouls()`, `Choices()` and `Scores()` are `iter.Seq2` filters over the answers | nothing to cache | R15 |
| response size limit | no limit | 16 MiB by default (`WithMaxResponseBytes`, at most 1 GiB), read into a bounded buffer; a larger 2xx body is a `*ResponseTooLargeError`, a larger error body an `*APIError` with no body | bounded memory (plan NF5) | — |
| nesting depth | the parser refuses more than 200 nested arrays | sonic accepts 4096 nested containers and the SDK refuses deeper ones with a `*ResponseValidationError` | the codec's limit; Go is the lenient side (ruling R73) | — |
| unknown answers logged at most 8 times | one WARN record per answer of an unknown type | at most 8 WARN records per response, then one that counts the rest | bounded logging | — |
| usage counts | optional counts; a negative one is accepted | `(uint64, bool)`; a negative count, or one over 2⁶⁴−1, fails at `usage.input_tokens` or `usage.output_tokens` | a count cannot be negative (ruling R70) | — |
| score level keys | pydantic's lax `int` accepts `" 1"`, `"1_0"`, `"1.0"`, `"-1"` and `"4294967296"` | a decimal `uint32`, with an optional `+` and leading zeros; any other key fails at `answers.<name>.legend.<key>` | a level is a small non-negative index | — |
| non-finite numbers in a response | `1e400` in a float member reads as ±inf; `NaN` and `Infinity` are accepted | `1e400` in a float member, and `NaN` or `Infinity` anywhere in a 2xx body, are a `*ResponseValidationError` | finite floats only (ruling R73) | — |
| lone surrogates | a lone surrogate escape is refused | a lone surrogate escape becomes U+FFFD in `Answers()`; a structured level's raw bytes keep the escape | one check per string; a surrogate check would need a second pass | — |
| root path | a body that is not an object fails at `''` | it fails at `.` | the codec names the root `.` | — |
| `Retry-After` precision | seconds as a float, `retry-after-ms` as float milliseconds | a `time.Duration` truncated to the millisecond; a date only in a format `http.ParseTime` reads | header precision (ruling R73) | — |
| legend value path | a structured legend value of the wrong kind fails at `answers.<n>.legend.0.str` | it fails at `answers.<n>.legend.0` | the codec reports the value, not its union member (ruling R70) | — |

## Errors and redaction

| Key | Python SDK 0.7.1 | Go SDK | Why | Matrix rows |
| --- | --- | --- | --- | --- |
| errors are values | exception classes under `TypeSafeError`, rebuilt from their state; the base class `TypeSafeAPIError` | distinct error types, each a pointer, matched with `errors.As` or `errors.AsType`: `*APIError` (its `Kind` from the status; `APIErrorOther` in a caller's own literal), `*ConnectionError`, `*TimeoutError`, `*ResponseValidationError`, `*ResponseTooLargeError`, `*ConfigError`, `*InvalidRequestError` | Go has no exception classes | E1 |
| no process pools | errors are pickled across process pools | a value handed to another goroutine is the same value; nothing is serialised | Go shares memory between goroutines | E2 |
| cancellation | `asyncio.CancelledError` propagates | a cancelled context returns `context.Canceled` itself, not an SDK error, after one attempt; a deadline that passes returns a `*TimeoutError` | Go's convention (ruling R81) | — |
| plain-text body cut at 200 | a server's message verbatim; a plain-text body is never cut | server text is escaped (control and format characters) and cut: a message at 200 characters, a name or request id at 128, a field path at 320; a plain-text body used as the message too | log hygiene (plan NF7, ruling R58) | E6 |
| cause via `errors.Unwrap` unless it printed a credential | a transport exception's causes, notes and cycles are copied with credentials redacted | a transport error's text is escaped, cut at 200 characters and shows credentials as `***`; its cause is reachable with `errors.Unwrap` unless the cause's text printed a credential, when a stand-in with the redacted text takes its place | hygiene (rulings R81, R95) | L4, L5 |
| stored headers redacted | `error.headers` holds the headers as received; only the logs redact | `*APIError`, `*ResponseValidationError` and `*ResponseTooLargeError` hold a copy of the response header whose credential values, and values that hold the key, are `***`; `Retry-After` and the request id stay readable unless they hold the key | a stored error can be printed (ruling R87) | — |
| redaction by source | the logs redact headers by name | a header-derived value (a header, the request id) that is a credential or holds the key is `***` in errors and log records alike, the INFO record's request id included; text from a response body (a message, a field path, a skipped answer's name) is shown as the server sent it, as upstream shows it | where a value came from decides (rulings R107, R114) | — |

## Retries

| Key | Python SDK 0.7.1 | Go SDK | Why | Matrix rows |
| --- | --- | --- | --- | --- |
| `RetryPolicy.exceptions` → dropped; `Predicate` kept | `RetryPolicy(exceptions=..., predicate=...)` | no exception classes to list; a `Predicate` can test an error's type with `errors.As` | Go has no exception classes | RT23 |
| 2xx in retry statuses retries a non-validating body → never; `Predicate` can opt in | a 2xx status in the retry set retries a body that fails validation | a 2xx is never retried by its status; a `Predicate` may accept its `*ResponseValidationError` | every retry is billed | RT23 |
| retry policy as a value | `RetryPolicy(...)` with times in float seconds | the zero `RetryPolicy` is `DefaultRetry`, Python's `RetryPolicy()`; setters return changed copies; times are `time.Duration`, so NaN, ±Inf and sub-nanosecond values cannot be written; the budget counts from the first attempt's start; a deadline that ends a wait returns a `*TimeoutError` with no timeout, and the last server error is lost | Go values (rulings R88, R97) | — |

## Logging

| Key | Python SDK 0.7.1 | Go SDK | Why | Matrix rows |
| --- | --- | --- | --- | --- |
| `TYPESAFE_LOG_LEVEL` not read | `TYPESAFE_LOG_LEVEL` configures the SDK's logger | not read: the SDK writes to the `*slog.Logger` of `WithLogger`, or discards its records, and never sets a level | a library must not configure logging | L8 |
| bodies at LevelTrace | DEBUG logs whole bodies | DEBUG logs headers and body lengths; `LevelTrace` (DEBUG−4) the bodies | bodies may hold personal data | — |

## Typed answers

| Key | Python SDK 0.7.1 | Go SDK | Why | Matrix rows |
| --- | --- | --- | --- | --- |
| typed answers by struct tags | `response_model=` a pydantic model; `Optional` fields default to `None` | `Ask[T]`, `PreparedFor[T]` and `DecodeAs[T]` over a struct's tagged fields; the `optional` key and `Present()` instead of `None` | static typing (plan D3) | — |
| `Ask` returns the answers only | the typed response carries `request_id`, `usage` and the raw response | `Ask[T]` returns a `T`; the two-step form (`PreparedFor`, `SystemOne`, `DecodeAs`) keeps the response | a `T` is the caller's own type (ruling R99) | — |
| typed wire names | the model's field name | the Go field name as written (`Billing`), or the tag's `name=` | no case conversion rules (ruling R94) | — |
| stricter typed checks | an undeclared probability label, and a legend or probability level beyond the levels, pass unless the model's `Literal` forbids them; a plain `BaseModel` may omit `usage` | the typed decode refuses them, and requires `usage` | a typed set declares its options and levels (ruling R99) | — |
| typed tag rules | pyrefly checks a model when the code is type-checked | a tag is a string checked once per type by `PreparedFor[T]`, which refuses 13 kinds of fault with a `*ConfigError`; on the typed path it also refuses an embedded struct holding a tagged field, a choice without options and repeated score levels, the last two of which `NewQuestions` accepts | struct tags are strings (rulings R94, R96) | — |
| pyrefly fixtures | pyrefly expectation fixtures (`tests/typing/`) | the negative expectations map to `PreparedFor[T]`'s runtime rejections; the positive fixtures to `go vet ./examples/...` | Go's compiler checks what pyrefly checks | XT1 |
| typed decode by field offsets | pydantic builds the model | `DecodeAs[T]` writes each answer at its field's offset through `unsafe`, in one file of the package (`decodeas_store.go`), without allocating | the owner allowed it for an allocation-free typed decode (ruling R116) | — |

## Tests and tooling

| Key | Python SDK 0.7.1 | Go SDK | Why | Matrix rows |
| --- | --- | --- | --- | --- |
| no sybil | sybil runs the README's, the docs' and the docstrings' examples against the API | the Markdown's Go blocks are the `examples/` programs (byte-equal, `docs-snippets.py`), compiled in CI, run against a local stand-in on every `go test` and against the API with `-tags live`; the root package's `Example*` functions check their output offline | Go has no sybil | XD1, XD2 |
| dev→public sync tooling not ported | `tests/test_public_sync.py` tests the script that syncs the private dev repository to the public one | not ported | repository tooling, not SDK behaviour | XS1, XS2, XS3, XS4, XS5, XS6, XS7, XS8, XS9, XS10 |
| no release-notes script | `.github/scripts/release_notes.py` cuts release notes from the changelog | not ported | release tooling, not SDK behaviour | XR1, XR2 |

## Parity kept by ruling

These looked like deviations during the port, and the owner kept the Python
SDK's behaviour:

- Typed failure paths take the form of a pydantic `SystemOneResponse`
  subclass: `tone.choice`, not `answers.tone.choice` (rulings G7 (6),
  R99-rev).
- An API key that the server echoes in an error message or a field path is
  shown as the server sent it; the SDK's own headers, and the header-derived
  values of its errors and records, never carry it (rulings G7 (8),
  R103-rev, R107).
- Duplicate members keep the last value at every level: top-level members,
  answer names, answer members and `type`.
- A request that HTTP/2 replays inside one attempt (a stream the server
  refused or never processed) is not a retry and is not counted in
  `X-TypeSafe-Retry-Count`.

## Found by the live pass

The live pass of W6.4 (ledger rows W6.4-01 to W6.4-05) recorded two facts
about the API that are not deviations of the SDK: a request carrying no
credential is answered with 403, and one with a key the API did not issue
with 401, both with the error type `authentication_error`, which
`APIError.IsAuthentication` reports for both; and the API gzips its
successful responses when the client asks for gzip, as Go's transport does
by default, so a response reaches the SDK with no declared length.
