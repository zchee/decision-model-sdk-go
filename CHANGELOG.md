# Changelog

All notable changes to typesafe-sdk-go are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-09-27

The first release: a Go client for the TypeSafe System One API, ported from
typesafe-sdk-python 0.7.1 (commit `0ffd094`). Each of the Python SDK's 129
tests maps to a Go test or to a documented difference
([`docs/port-test-matrix.md`](docs/port-test-matrix.md),
[`docs/deviations.md`](docs/deviations.md)).

### Added

- A compile-time refusal on the platforms the JSON codec (sonic) does not
  support: the SDK builds with Go 1.27 on amd64 and arm64 and fails
  elsewhere with an error that names the requirement
  ([`docs/support.md`](docs/support.md)).
- Questions: typed `Noul`, `Choice` and `Score` questions, `RawQuestion` for
  any shape, `Content` for text or JSON descriptions, and
  `NewQuestions().Prepare()`, which checks the Python SDK's rules once and
  keeps the question bytes for every call.
- Request encoding: a state of any JSON-encodable Go value, `RawJSON` and
  `Content` states, and `ExtraBody` members; NaN, infinities, invalid UTF-8
  and a plain `[]byte` are refused before anything is sent. The API refuses
  a top-level member it does not know with a 400 `api_usage_error`, so
  `ExtraBody` is for members the API accepts.
- Response decoding: noul, choice and score answers read with the Python
  SDK's validation and error paths, unknown answer types skipped with a
  bounded warning, and the response's metadata (`Meta()`, the request id,
  the raw body).
- Seven error types matched with `errors.As` (`*APIError`,
  `*ConnectionError`, `*TimeoutError`, `*ResponseValidationError`,
  `*ResponseTooLargeError`, `*ConfigError`, `*InvalidRequestError`), whose
  text never holds the request's state or an unescaped body, and holds the
  API key only where the server echoed it (below).
  `APIError.IsAuthentication` reports both of the API's refusals: 403 for a
  request without a credential and 401 for a key the API did not issue.
- Configuration from options or the environment (`TYPESAFE_API_KEY`,
  `TYPESAFE_BASE_URL`, `TYPESAFE_DEFAULT_MODEL`), checked when the client is
  built; the SDK and runtime headers; `WithHeader`.
- An HTTP/2 transport that opens one connection from a cold start, refuses
  an API server that does not negotiate h2, and honours the proxy
  environment; a proxy that refuses the CONNECT is a `*ConnectionError`
  whose `Proxy()` is true. `WithHTTPTransport`, `WithRoundTripper`,
  `WithProxy` and `WithHTTPVersion` change it.
- `WithCompression(false)` asks the API for uncompressed responses. By
  default (`WithCompression(true)`) the client asks for gzip, as Go's
  transport does (the Python SDK's httpx asks for gzip and deflate); the
  API's answers are a few hundred bytes, and an uncompressed answer, whose
  length the API declares, saves the gzip reader's work on every call for
  more bytes on the wire. The option configures the SDK's own transport, so
  it cannot be combined with `WithHTTPTransport` or `WithRoundTripper`.
- `WithClientTrace` for `net/http/httptrace` hooks, which cannot wedge the
  client: a hook's panic is raised on the call that made the request, and a
  hook that blocks holds the client's other calls only up to the
  transport's bounds, one that blocks a new connection's DNS, connect or
  TLS handshake included, under `WithNoTimeout` too. `GetConn` on a client
  that already has its connection is the one hook that must not block.
- The client: `NewClient`, `SystemOne`, `Models().List`, `WarmUp`, `Stats`,
  `Close`, per-call options (`Model`, `Timeout`, `Header`, `ExtraBody`,
  `Retry`) and `WithPretouch`.
- JSON round trips of responses and answers that match the Python SDK's
  `model_dump_json` byte for byte.
- A deadline per attempt, cancellation that makes one attempt, and transport
  errors classified as connection or timeout errors with credentials
  scrubbed.
- `RetryPolicy`: the Python SDK's default policy as the zero value,
  `DefaultRetry`, `NoRetry`, backoff with jitter, `Retry-After`, a time
  budget, `WithRetry` for the client and `Retry` per call.
- Logging through `log/slog` (`WithLogger`): a record per attempt,
  credentials redacted, bodies only at `LevelTrace`, at most 8 warnings
  naming unknown answers per response, then one that counts the rest.
- Typed answers from struct tags: `PreparedFor[T]`, `Ask[T]` and
  `DecodeAs[T]`, with `optional` fields and `Present()`. The typed decode
  allocates nothing. A response keeps the redactor its call used, so the
  errors of `DecodeAs` show what those of `Ask` show: the client's API key
  as `***` in the response's header and request id and, for a plain-HTTP
  request through a proxy, the proxy's credentials too, however long the
  response is kept ([`docs/support.md`](docs/support.md) says what a kept
  response holds).
- Seven example programs that the README and `docs/` show byte for byte,
  and `Example` functions.

### Security

- A pooled decoder drops scratch memory above 4 MiB when its call ends, so
  one response of many tiny answers inside the 16 MiB response cap cannot
  keep about 233 MiB live while the client stays busy. The decode's own
  peak still grows with the number of answers, which has no limit, as in
  the Python SDK: lower `WithMaxResponseBytes` for an API address you do
  not control.
- sonic (v1.15.4) can read up to 4 bytes past an input shorter than 4
  bytes. The SDK decodes such a body from a zeroed copy and never hands
  sonic a shorter slice of a larger one, so no decode reads past the bytes
  it was given; a test runs every decode entry point, and the request's
  UTF-8 check, against guard pages on arm64 and amd64.
- A proxy's credentials are `***` wherever a client on the SDK's own
  transport reports a failure: the password as it is, as the URL escapes
  it and each word of it, and the Basic token sent for it, at any length,
  in every transport error and the log records that carry one. A
  response's request id and header values are scrubbed of the whole
  password and token only, from 8 bytes, as they are of the API key, and
  only when the response answers a plain-http request that went through a
  proxy: over HTTPS a proxy only tunnels and cannot write them. The SDK
  records the proxies its proxy function returns, the 16 most recent, when
  net/http asks the function, and never calls it again for this. The
  message of an `APIError` is shown as the server sent it, as the Python
  SDK shows it, so a proxy password or an API key the server echoes there
  stays visible. A transport given with `WithHTTPTransport` or
  `WithRoundTripper` keeps its proxy, whose credentials the SDK does not
  know.

### Differences from the Python SDK

The full table is [`docs/deviations.md`](docs/deviations.md). The ones
decided while the port was built:

- A cancelled context returns `context.Canceled` itself; a deadline that
  passes returns a `*TimeoutError`.
- A trace hook that blocks a new connection's DNS, connect or TLS handshake
  phase ends that call, and every call waiting for a connection meanwhile,
  with a `*TimeoutError` at the connection bound, under `WithNoTimeout`
  too, while the hook runs on; a panic it raises afterwards is logged at
  WARN. httpx has no such hooks.
- Error values keep a copy of the response header with credentials
  redacted; a request id or header value holding the API key is `***` in
  errors and log records alike.
- An API key or a proxy password that the server echoes in an error
  message or a field path is shown as the server sent it, as the Python
  SDK shows it.
- The zero `RetryPolicy` is the Python SDK's default; times are
  `time.Duration`; a deadline that ends a retry wait returns a
  `*TimeoutError`, and the last server error is not returned.
- `Answers` and `RetryPolicy` values cannot be compared with `==`.
- Floats in a request state are spelled as sonic writes them; a `RawJSON`
  or string state gives exact bytes.
- A raw score question whose criteria are a JSON number counts as empty
  only when every digit before the exponent is zero, so `1e-400` is sent,
  where the Python SDK reads it as `0.0` and refuses the question.
- A typed answer's wire name is its Go field name unless its tag sets
  `name=`; the typed decode refuses labels and levels the question set does
  not declare, and requires `usage`; its error paths keep the Python SDK's
  form (`tone.choice`).
- The typed decode writes answers at their fields' offsets through `unsafe`,
  in one file of the package, so that it allocates nothing.

### Development

- CI on linux/amd64, darwin/arm64 and windows/amd64 with the race
  detector, lint and vulnerability checks; coverage on Codecov, which
  counts every test of the module toward each package; benchmarks on
  CodSpeed; Dependabot.
- The call's stages live in `internal/engine`. `Client`, `Prepared` and
  `SystemOneResponse` are defined types over its state, so `go doc` shows
  `type Client engine.Client[RetryPolicy]`, `type Prepared engine.Prepared`
  and `type SystemOneResponse engine.Response`; their methods and
  documentation are the root package's, and `%#v` of a `*Client` names its
  internal configuration type, never the key.
- Allocation budgets asserted in CI from `internal/alloctest` for request
  encoding, decoding, a whole call (12 allocations of the SDK's own on top
  of the HTTP/2 transport's) and the response-size cap.
- Seam tests over every package of the module: `unsafe` only in the typed
  store's file and the codec's no-copy string, sonic only behind
  `internal/codec` and in the benchmarks' naive client, and no
  `import "C"`.
- The request state's UTF-8 check runs sonic's SIMD validator on amd64 and
  `utf8.Valid` on arm64.
- Benchmarks B1–B6 against a naive sonic client; the benchmark job fails
  when a whole call is not faster than the naive client's in the same run.
- Fuzzing of every parser of server-chosen bytes in CI.
- Live tests against the API (opt-in, `-tags live`).
- Goldens of the exported API and the client options, with constant values.
- The time ratio of decoding an answer with 10 000 structured levels
  against one with 1 000 (at most 15) is asserted in the builds without
  the race detector, on every CI image; under the race detector a
  companion test logs the ratio without asserting it, because the
  instrumentation, not the decoder, sets the ratio there.
- The raw measurement files and prototype sources that `docs/` cites are
  kept, with their history, in a private archive repository maintained by
  the owner, cited as `spikes@<commit>:<path>`; this repository's history
  was rewritten on 2026-09-27 to remove them, so the module and a clone
  carry none of them.

[0.1.0]: https://github.com/zchee/typesafe-sdk-go/tree/v0.1.0
