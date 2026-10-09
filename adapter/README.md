# System One Adapter for Go

`github.com/zchee/decision-model-sdk-go/adapter` answers the System One API
with an LLM through an in-process `http.RoundTripper`. It ports
[system-one-adapter-python](https://github.com/typesafe-ai/system-one-adapter-python)
0.2.1 at `e1d4cc938204b22fc5a3c3aca7044072fe3f712d`. Its purpose is to compare
the decision-model service with an LLM on cost, speed and intelligence,
without changing the questions or the root SDK's answer interface.

**Status: unreleased.** The API and offline replay tests are implemented;
live acceptance and release remain separate gates. The root SDK's `v0.1.0`
tag is not an adapter release. This independent nested module uses
`adapter/vX.Y.Z` release tags, created only when its release is approved.

[The test matrix](docs/port-test-matrix.md) maps upstream tests and recordings
to Go tests. [Deviations](docs/deviations.md) document intentional differences
and retained limitations. [Uncovered blocks](docs/uncovered-lines.md) give
precise reasons for measured gaps; they are not claims of tested behavior.

## Install

Use Go 1.27 on amd64 or arm64 with the standard library JSON v2 experiment.
Offline gates use Go 1.27.1 on macOS arm64 and Linux amd64; they do not prove
Windows execution or every toolchain configuration. Before an adapter tag,
select a repository commit explicitly:

```sh
go get github.com/zchee/decision-model-sdk-go/adapter@<commit>
```

Import the root module as `decision` and the nested module as `adapter`.
`go.mod` requires `github.com/zchee/decision-model-sdk-go v0.1.0`; this is a
minimum-version selection requirement, not a dependency lock. Repository
gates also test against the root checkout. The adapter no longer requires
`github.com/go-json-experiment/json` and deliberately does not build with
`GOEXPERIMENT=nojsonv2`: its JSON seam requires `encoding/json/v2` and
`encoding/json/jsontext`.

## Quick start

Prepare questions with `decision.NewQuestions`, for example a `Noul` named
`positive` with `Instructions: decision.Text("The review is positive.")`,
then call `Prepare` and handle its error.

Create `adapter.New(adapter.Probabilities, adapter.Structured,
adapter.WithDefaultModel(adapter.ModelID("openai", "gpt-4o-mini")))`, then
`adapter.NewClient(ad)`. Handle both construction errors, close the client
when finished, and call `client.SystemOne(ctx, state, questions)` with your
caller-owned context. Recover adapter diagnostics with
`adapter.ReportOf(response)` or `adapter.ReportFromError(err)` rather than
assuming the root response's serializer preserves the Report.

With real provider configuration, that setup makes a billed OpenAI request.
No decision-model service credential is needed for the adapter client:
the SDK's request is answered in process, not sent to that service.

`Probabilities` requests a distribution; `Discrete` requests one label or
level. `Structured` supplies the provider's native response schema;
`Prompted` puts schema instructions in the prompt. Neither guarantees a
schema-valid answer. The committed replay tests provide offline examples
of wiring real provider implementations to local cassette transports;
the opt-in live tests are a separate signal, not substitutes for replay.

`WithProvider(name, provider)` borrows a caller-owned `llm.Provider` and
never closes it. `WithFactory(name, factory)` creates owned providers lazily;
they are cached by provider/model and closed by `Adapter.Close`. An adapter
belongs to one `adapter.NewClient` client. Closing that client waits for
running calls before closing owned providers. Cancel those calls first
when shutdown must be bounded.

`adapter.NewClient` sets the root transport after caller options, so a
caller `decision.WithRoundTripper` option has no effect. The root's
`WithHTTPTransport`, `WithHTTPVersion`, `WithRootCAs`, `WithTLSConfig`,
`WithProxy`, `WithConnectTimeout` and `WithCompression` cannot be combined
with that transport and return a root `ConfigError`.

## Provider configuration

Built-in factories are `openai`, `anthropic` and `gemini`. They read their
environment when constructing a provider, not on every request. Empty
credential/string options fall back as each provider documents; they are
not a way to disable fallback.

| Provider | Environment | Endpoint fallback | Provider-package options |
| --- | --- | --- | --- |
| OpenAI | `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_ORG_ID`, `OPENAI_PROJECT_ID` | `https://api.openai.com/v1` | `WithAPIKey`, `WithBaseURL`, `WithOrganization`, `WithProject`, `WithAPI`, `WithPromptedResponseFormat`, `WithHTTPClient`, `WithTimeout` |
| Anthropic | `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL` | `https://api.anthropic.com` | `WithAPIKey`, `WithAuthToken`, `WithBaseURL`, `WithMaxTokens`, `WithHTTPClient`, `WithTimeout` |
| Gemini | `GOOGLE_API_KEY`, then `GEMINI_API_KEY`; `GOOGLE_GEMINI_BASE_URL` | `https://generativelanguage.googleapis.com/` | `WithAPIKey`, `WithBaseURL`, `WithHTTPClient`, `WithTimeout` |

These are options of each provider package, not of `adapter.New`; register
an explicitly configured provider with `WithProvider`, or construct it in
a `WithFactory` closure. Missing credentials fail provider construction.
Anthropic accepts an API key, a bearer token, or both: either explicit
credential disables both credential environment fallbacks. Gemini prefers
`GOOGLE_API_KEY`. OpenAI preserves its environment distinctions: an empty
key is missing, and empty organization/project values set no such header;
an empty base URL uses the provider's default.

OpenAI `WithAPI` selects `Auto`, `Responses` or `ChatCompletions`. `Auto`
uses Responses for host `api.openai.com` and Chat Completions otherwise.
Prompted Chat normally sends `"response_format": null`; for servers that
reject it, choose `WithPromptedResponseFormat(openai.FormatOmit)` instead
of the default `FormatNull`. Anthropic's output limit defaults to 4096
tokens and must be positive. Explicit nil HTTP clients and nonpositive
explicit timeouts are rejected as each provider's godoc specifies.

These endpoint defaults do not apply to the renamed root SDK. A separate
`decision.NewClient` for the decision-model service requires an explicit
key/base URL or `DECISION_MODEL_API_KEY` and `DECISION_MODEL_BASE_URL`;
there is no default service endpoint. `adapter.NewClient` supplies its own
noncredential placeholder settings, suppressing environment URL/model
selection unless caller options override them. Do not put secrets in
model names, endpoint paths or identifiers that may be logged.

## Retries, deadlines and cost

The adapter defaults to `NoRetry()`. `WithRetry(DefaultRetry())` enables
eligible timeout/connection failures and status 408, 429 or 500–599, not
provider non-answers. DefaultRetry permits two retries, 500 ms exponential
backoff capped at 5 s, jitter up to 0.25 and a 30 s retry budget. A usable
provider `Retry-After` can determine the wait; the budget still limits it.
`ContextWithRetry(ctx, policy)` overrides the policy for one call.

`WithMalformedRetries(n)` separately allows corrective requests after
schema-invalid output; its default is zero. Transient retries can apply to
each corrective request too. Each attempt can be billed, and prompts repeat
the state and growing correction conversation. Account for both mechanisms.

On your own `decision.Client`, each SDK retry re-runs the whole evaluation;
the Report records it as `debug.sdk_retry_count`, and `adapter.NewClient`
sets `decision.NoRetry()` so that it never happens. This concerns eligible
provider statuses, timeouts and connection failures, not every error or a
424 non-answer. Caller options can override NewClient's default root policy.

Provider `WithTimeout` bounds one HTTP request, including its body read,
and defaults to 600 s. A caller context deadline bounds the whole evaluation,
including waits/corrections. The retry budget is neither that whole-call
deadline nor a bound on an in-flight request. NewClient defaults to no root
timeout; pass a deadline-bearing context when the whole call needs a limit.
Cancellation returns the context error without a Report. A deadline can
carry a timeout Report, but a caller-owned SDK client's retry wait can
lose the preceding Report. Under NewClient, provider timeouts and caller
deadlines both read `Request timed out.` with Timeout 0s; use `ctx.Err()`
or the Report rather than that text alone.

## Reports, response size and logging

`ReportOf(response)` reads usage/debug from `response.Meta().RawBody()`;
`ReportFromError(err)` returns a Report and presence boolean where supported.
It may be absent before evaluation, on cancellation or if the SDK did not
retain the body. `Report.MarshalJSON` preserves the Report; the root response's
`MarshalJSON` drops adapter-only members. Keep the Report separately.

The SDK's ordinary response limit is 16 MiB. NewClient raises it to 1 GiB
because the body is generated in process; callers can override it with
`decision.WithMaxResponseBytes`. This is not an evaluation memory cap.
Provider responses have a separate 64 MiB wire-read limit. Large debug data
repeats state/prompts on each attempt: large inputs, many questions and
retries can exhaust memory or prevent Report recovery under a lower limit.

`adapter.WithLogger` records categories, counts and permitted identifiers,
not state/prompts/bodies or credential headers. The root's separate
`decision.WithLogger` at `decision.LevelTrace` includes bodies containing
state and prompts. Reports and Trace also retain content and provider text;
treat them as sensitive caller data, not universally sanitized diagnostics.

## Diagnostic and redaction boundaries

The built-in provider layout is two levels:
`Provider.state *requestState` → `requestState.credentials *requestCredentials`.
OpenAI and Anthropic's inner structs retain endpoint/header state; Gemini's
retains key/endpoint. One pointer level was insufficient: Go fmt's bad-verb
fallback can restart at depth zero for an unsupported verb. The committed
layout and formatting controls were checked on pinned Go 1.27.1, including
boxed/nested/private-field cases. They are not guarantees of immutable-string
zeroization, arbitrary reflection, unsafe memory access or every future fmt
implementation. Model identifiers intentionally remain printable.

`llm.StatusError.MarshalJSON` has a value receiver and emits a JSON string
of its bounded Error text, using an RFC 8259 escaper with invalid UTF-8
replaced. `LogValue` likewise returns a slog string of that text. The body
is limited to 200 Unicode code points, with an ellipsis when truncated;
headers and the body tail are not emitted through those two methods.
The exported Header and Body fields nevertheless remain retained data.

Accepted StatusError limitations remain: a value inside a caller's private
wrapper, invalid value `%p`, and invalid `%w` uses can enter fmt reflection
and expose retained fields rather than invoke its formatting methods.
Store/pass a pointer, use `%p` only for a pointer, and use an explicit
`Error()` string where a private wrapper needs a safe diagnostic value.
Vet catches some concrete static misuses; it is not a confidentiality
guarantee for dynamic format strings or values boxed in `any`.

REST replaces exact known credential forms with `***` before retaining
successful response bodies or StatusError bodies. The forms are raw text,
JSON string content, and upper/lower percent-hex QueryEscape/PathEscape
forms. Empty forms are skipped; overlapping forms match longest first.
The same scrubber handles the diagnostic request copy only when Trace is
present; outbound request bytes stay unchanged. It does not recursively
decode, remove every unknown secret, sanitize all caller content, or promise
that every spelling of a credential is recognized. This qualifies the
raw-wire response description in the deviation table.

REST error targets omit URL userinfo/query/fragment but retain scheme,
host/path. Credentials in paths are outside that promise. Default clients,
and borrowed clients without a redirect policy, refuse redirects. An
explicit caller CheckRedirect remains the caller's trust decision.

## Recordings and live verification

Replay covers pinned upstream exchanges, not current services. Opt-in
`live`-tag tests remain useful after release to detect API drift; they need
`ADAPTER_LIVE_TESTS=1` and provider/service configuration. Recording is the
separate `ADAPTER_LIVE_RECORD=1` opt-in and can incur charges. It writes
bounded recordings outside protected upstream fixture directories.

Recorder query-name filtering removes these exact 17 aliases,
case-insensitively after **one** decode:
`key`, `api_key`, `api-key`, `apikey`, `token`, `access_token`, `access-token`,
`accesstoken`, `auth_token`, `auth-token`, `authtoken`, `authorization`,
`password`, `secret`, `client_secret`, `client-secret`, `clientsecret`.
Every occurrence is removed, including bare/empty/duplicate pairs;
undecodable names are dropped. Benign raw spelling and order are preserved.
Only content-type headers are retained. Unknown, signed or double-encoded
query conventions are not universally sanitized.

**Recorder bodies are captured before REST scrubbing.** New recordings
replace provider-assigned identifiers at known JSON request/response paths
with "x" without changing outbound requests, returned responses or replay.
Reflected credentials can therefore remain in cassette request/response bodies.
Every new recording requires separate inspection, keyscan and maintainer approval
before addition; replay success does not waive these controls. The next
live-recording gate owns this inspection, not ordinary offline tests.

Use only a maintainer-approved 0600 credentials file, loaded by the isolated
live runner or a maintainer. Keep ordinary gates and interactive development
sessions free of
live credentials. The service entries are `DECISION_MODEL_API_KEY` and
`DECISION_MODEL_BASE_URL`; old `TYPESAFE_API_KEY` names are not fallbacks.
No live verification is implied by the offline evidence here.

## Dependency applicability and retained work

The scoped audit at `0b9e5690a90edf92007f2e119c3e7e7334e2709b`
reported GO-2026-5781 for `rsc.io/pdf` through `golang.org/x/arch`, and
GO-2026-5932 for the OpenPGP family of `golang.org/x/crypto` through the
root's `golang.org/x/net`. No fixed version was reported. Affected packages
were absent from 14 usable import closures; two adapter nojsonv2 closure
queries failed and were not counted as usable. Graph membership is not
imported applicability, and import absence is not a whole-program or
future-import guarantee.

The latest available scan audit at documentation preparation was for
`66fb80780cedee8c33ed6efc1ac45d8f30ff6bfb`, filed
**2026-10-07 21:29:55 JST**: 28 fresh query-mode calls retained the advisory
disposition; parent import closures remained applicable because imports and
modules were unchanged. This is audit evidence recorded with the release
evidence, not a new audit by the documentation writer. This branch's own
final head must be remeasured by the independent dependency audit before
final acceptance.

The four-dimensional nesting refactor in the caller-client regression
remains deferred; the nil-transport, Chat retry, Responses non-answer,
caller-owned logger and nil/empty-body checks were added without claiming
that structural cleanup was completed.

The CI workflow's existing fuzz/list commands predate the hardening work
and do not carry `-race`, an explicit `-timeout` or bounded
`-fuzzminimizetime`. Local release gates and the long campaign at
`0b9e5690` are the recorded fuzz evidence: ten minutes per target with
`-race`, `-parallel=1` and `-fuzzminimizetime=1x`. Aligning CI commands is
deferred to later work; this documentation change does not edit that workflow.

## License

Apache License, Version 2.0 ([LICENSE](LICENSE)). Portions are ported from
system-one-adapter-python; the byte-preserved upstream cassette/expected
fixtures are under its MIT license ([LICENSE-UPSTREAM](LICENSE-UPSTREAM)).
