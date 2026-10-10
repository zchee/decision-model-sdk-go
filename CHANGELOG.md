# Changelog

All notable changes to decision-model-sdk-go are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `decision.Provider` and `decision.WithProvider` build a client for one
  vendor from its base URL, its System One and list-models paths, the
  environment variable its key is read from and an optional default
  model. With a provider, `DECISION_MODEL_API_KEY`,
  `DECISION_MODEL_BASE_URL` and `DECISION_MODEL_DEFAULT_MODEL` are not
  read; `WithAPIKey` and `WithModel` still win; `WithBaseURL` beside a
  provider is accepted only with `WithAPIKey`.
- The `provider` package holds three presets: `provider.TypeSafe()`
  (`TYPESAFE_API_KEY`), `provider.Codiv()` (`CODIV_API_KEY`) and
  `provider.Perplexity()` (`PERPLEXITY_API_KEY`, `POST /v1/decisions`, no
  model listing).
- `Models.List`, and so `Client.WarmUp`, on the client of a provider
  without a listing fail with a `*ConfigError` naming the provider before
  anything is sent.

### Changed

- `RequestID` of the error types and of `ResponseMeta`, and the request
  id of the INFO `response` record, read `x-request-id` when
  `x-typesafe-request-id` is absent, so a vendor that sends only the
  former has an id. The headers are not modified.

## [0.1.0] - 2026-10-03

The first release under the module path
`github.com/zchee/decision-model-sdk-go`. "Decision model" is the
vendor-neutral name for the models served through the System One API,
which more than one vendor serves, so the repository and the module were
renamed from `github.com/zchee/typesafe-sdk-go`, and the version starts
again at 0.1.0. The versions published as `github.com/zchee/typesafe-sdk-go`
v0.1.0 and v0.1.1 stay on the Go module proxy under that old path; their
entries follow this one, as they were written.

### Changed

- The module path is `github.com/zchee/decision-model-sdk-go` and the
  package is named `decision` (it was `typesafe`): import it as
  `decision "github.com/zchee/decision-model-sdk-go"`. The adapter module
  is `github.com/zchee/decision-model-sdk-go/adapter`.
- Every request names `decision-model-sdk-go/0.1.0` in its `User-Agent`
  and `X-TypeSafe-SDK` headers. The header names, the API paths and the
  JSON members are unchanged.
- **Breaking:** the client reads its settings from
  `DECISION_MODEL_API_KEY`, `DECISION_MODEL_BASE_URL` and
  `DECISION_MODEL_DEFAULT_MODEL`. The earlier names, `TYPESAFE_API_KEY`,
  `TYPESAFE_BASE_URL` and `TYPESAFE_DEFAULT_MODEL`, are not read, not even
  as a fallback.
- **Breaking:** there is no default base URL. `DefaultBaseURL` is removed,
  and `NewClient` without `WithBaseURL` or `DECISION_MODEL_BASE_URL` fails
  with a `*ConfigError`, as it does without an API key.
- **Breaking:** there is no default model. `DefaultModel` is removed, and a
  System One call that names no model (`Model`, or an `ExtraBody` member
  named `model`), on a client without `WithModel` or
  `DECISION_MODEL_DEFAULT_MODEL`, fails with a `*ConfigError` before
  anything is sent.
- **Breaking:** the struct tag key of `Ask`, `PreparedFor` and `DecodeAs`
  is `decision`: rewrite each `typesafe:"..."` tag as `decision:"..."`. The
  old key is not read, not even as a fallback; an answer field that carries
  only it is refused as a field without a tag.
- The live tests are switched on by `DECISION_MODEL_LIVE_TESTS=1`, and
  read the vendor's base URL and model from the environment, as any client
  does; they name no vendor in code.

## [typesafe-sdk-go 0.1.1] - 2026-09-30

This release corrects documents and comments. No Go code changed apart
from the constant `Version`, and `go.mod` and `go.sum` are those of 0.1.0.

### Changed

- `Version` is `0.1.1`, so every request names `typesafe-sdk-go/0.1.1` in
  its `User-Agent` and `X-TypeSafe-SDK` headers.
- The Go row of [`docs/perf/codspeed.md`](docs/perf/codspeed.md) also says
  which Go release the benchmark job uses when the `actions/go-versions`
  manifest cannot be read or lists no 1.27 release.

### Fixed

- The README said that a `go` command from Go 1.21 to 1.26 switches to a
  Go 1.27 toolchain; Go 1.21.0 to 1.21.10 and 1.22.0 to 1.22.3 stop with
  `toolchain not available` in a checkout of this repository, and in a
  module of yours after the `go get` that adds the SDK. The README and
  [`docs/support.md`](docs/support.md) said that Go 1.17 to 1.20 print
  `note: module requires Go 1.27` when the build fails; they fail on the
  standard-library packages they lack.
- The comment of the `livetest/**` entry in `.codecov.yaml` said that the
  package's tests are opt-in tests against the live API that CI does not
  run. Those are its tests built with `-tags live`; its untagged tests,
  `TestExamplesOffline` among them, run in CI's test job.
- The comment above the `actions/setup-go` step of
  `.github/workflows/bench.yaml` said that setup-go sets
  `GOTOOLCHAIN=local` after it gets Go; it exports it before. The comment
  now also names the failures after which the download comes from
  go.dev/dl: reading the manifest, or downloading, extracting or caching
  the file it lists.

## [typesafe-sdk-go 0.1.0] - 2026-09-27

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

The full table is [`docs/deviations.md`](docs/deviations.md); the main
ones:

- A cancelled context returns `context.Canceled` itself; a deadline that
  passes returns a `*TimeoutError`.
- Error values keep a copy of the response header with credentials
  redacted; a request id or header value holding the API key is `***` in
  errors and log records alike.
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
  `internal/codec` in the SDK's code, in the benchmarks' naive client, and
  through its root package in the tests of the root package and
  `internal/testsupport`, and no `import "C"`.
- The request state's UTF-8 check runs sonic's SIMD validator on amd64 and
  `utf8.Valid` on arm64.
- Benchmarks B1–B6 against a naive sonic client; the benchmark job fails
  when a whole call is not faster than the naive client's in the same run.
- Fuzzing of every parser of server-chosen bytes in CI.
- Live tests against the API (opt-in, `-tags live`).
- Goldens of the exported API and the client options, with constant values.
- A check that decoding stays linear in the number of structured levels.
- The raw measurement files and prototype sources that `docs/` cites are
  kept, with their history, in a private archive repository maintained by
  the owner, cited as `spikes@<commit>:<path>`; this repository's history
  was rewritten on 2026-09-27 to remove them, so the module and a clone
  carry none of them.

[Unreleased]: https://github.com/zchee/decision-model-sdk-go/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/zchee/decision-model-sdk-go/releases/tag/v0.1.0
[typesafe-sdk-go 0.1.1]: https://github.com/zchee/decision-model-sdk-go/compare/6e5bed2bb6e0065cf4f3cf5f06b9ca809315be86...4d8e724309e782417fbd0b38b52b743946e5c779
[typesafe-sdk-go 0.1.0]: https://github.com/zchee/decision-model-sdk-go/tree/6e5bed2bb6e0065cf4f3cf5f06b9ca809315be86
