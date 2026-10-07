# Deviations from system-one-adapter-python 0.2.1

The upstream pin is `e1d4cc938204b22fc5a3c3aca7044072fe3f712d`. These existing
numbered rows describe intentional differences, not unresolved provider drift.
The [port matrix](port-test-matrix.md) identifies the corresponding upstream
functions and replay tests. Implementation paths below are repository-relative;
anchors refer to the implemented tree, not the earlier module skeleton.
No new deviation numbers are assigned to formatting or test-support notes.

| # | Deviation | Reason | Enforcing tests | Matrix rows | Implementation |
| --- | --- | --- | --- | --- | --- |
| DV1 | One synchronous, context-bearing API supports concurrent goroutines; upstream sync/async pairs become one Go case. | Go has no separate async client/provider API. | `TestConcurrentFirstUseReusesProvider`, `TestReplayReferenceShape` and the matrix's sync/async counterparts. | FM1, FM4-FM9, FM11, FM12, GT1-GT3, OT1, OT3, OT4, PL1-PL9, PL11, PL13, PN1-PN4, PR13, PR14, RT1 | `adapter/adapter.go:41,106`; `adapter/evaluate.go:246` |
| DV2 | Cancellation returns the context error without a Report. Close takes no context and its waiter cannot be cancelled. Deadlines differ from cancellation and can carry a timeout Report. | The HTTP/SDK boundary preserves context cancellation; there is no cancellable Close API. Caller-owned SDK retry waits may lose an earlier Report, so recovery is not unconditional. | `TestCancelledCallReturnsContextError`, `TestClassificationOrder`, `TestConcurrentCloseWaitsForSameCleanup`. | PL5, PL10, PL12 | `adapter/seam.go:228`; `adapter/adapter.go:263` |
| DV3 | `RetryPolicy` builders and `ContextWithRetry` replace upstream's exceptions set and `retry=` argument; Predicate supplies custom classification. | Each provider request is retried locally, while the SDK call carries no upstream retry argument. | `TestRetryPolicyIsAValue`, `TestRetryPolicyStatuses`, `TestRetryPolicyBudget`, `TestContextWithRetry`. | FM4 | `adapter/retry.go:80,365,459` |
| DV4 | Typed `ReportOf`/`ReportFromError` access replaces dynamic response extras; Report JSON preserves upstream member names/order. | Root SDK response types are not extended. | `TestReportJSONMatchesUpstreamShape`. | — | `adapter/report.go:38,230,768,794` |
| DV5 | Root response serialization drops adapter-only usage/debug; Report serialization or the raw body preserves them. | The root response schema is unchanged. | `TestSDKQuestionsAndResponseSerialization`. | FM1 | `adapter/report.go:230,768`; root response serializer is exercised through the SDK test. |
| DV6 | `debug_info.provider` uses the Go provider type path rather than the Python class path. | Language-specific type identity differs. | `TestReplayReferenceShape` normalizes this member. | LV1 | `adapter/evaluate.go:592`; `adapter/replay_test.go:597` |
| DV7 | Provider wire responses replace vendor `model_dump` output: wire numeric spellings and Gemini members are retained rather than SDK-added/dropped fields. Exact known credential forms are scrubbed before retention, qualifying the raw-wire description. | Providers use net/http directly; diagnostic retention must not copy a reflected known credential. | `TestReplayReferenceShape`, each provider's `TestProviderErrorClassification`, REST body-scrubber tests. | LV1 | `adapter/internal/rest/rest.go:264,288-304`; provider request-copy sinks are `adapter/openai/openai.go:393,406`, `adapter/anthropic/anthropic.go:372`, `adapter/gemini/gemini.go:299`. |
| DV8 | Corrective prompts use the Go validator's error rather than pydantic ValidationError text; fixed surrounding text is preserved. | There is no pydantic runtime; recorded exchanges contain no correction turn. | `TestCorrectionPromptNamesTheFailure`. | FM6, FM11 | `adapter/evaluate.go:150,246` |
| DV9 | One model string resolves provider/model; injected providers are registered by name; all three providers are compiled in, with no optional-dependency error. | The root SDK carries one model field and the adapter owns its provider factories. | `TestResolveModel`. | FM12, PL3, PR15, PR16 | `adapter/model.go:102`; `adapter/adapter.go:106` |
| DV10 | No context manager: NewClient transfers ownership to the root client. Close waits for running calls before closing owned providers; callers needing bounded shutdown cancel calls first. A failed cleanup can be retried by calling Adapter.Close directly; a second root Client.Close does not repeat it. | Closing connections beneath active evaluations would allow new connections during cleanup. | `TestNewClientCloseClosesAdapter`, `TestCloseContinuesAfterFailure`, `TestCloseWaitsForRunningCalls`, `TestCloseAfterProviderPanic`. | PL1 | `adapter/client.go:61-99`; `adapter/adapter.go:263` |
| DV11 | Failures cross HTTP as root SDK error classes. A provider status keeps its code; non-answers, unclassified errors and encoding failures answer 424. | 424 is outside the SDK retry set, preventing an upstream-nonretryable billed evaluation from being rerun. | `TestFailureClasses`. | FM10, UE1 | `adapter/seam.go:228,278` |
| DV12 | Provider construction failure answers 400 `provider_config`, rather than directly raising a vendor exception. | Configuration errors cross the same HTTP seam. | `TestProviderConfigFailure`. | — | `adapter/seam.go:119-122` |
| DV13 | Schema key order is stable; replay compares schema/request bodies by JSON value where vendor SDKs reordered them. | No vendor SDK mutates schema order in place. | `internal/schema.TestNativeSchemaEqualsCassette`, `TestReplayReferenceShape`. | — | `adapter/internal/schema/schema.go:104`; `adapter/replay_test.go:504,675`; `adapter/internal/cassette/replay.go` matcher. |
| DV14 | No translate_error/translating context manager; providers return typed errors where the HTTP call ends. | There are no vendor exceptions to translate. | `openai.TestProviderErrorClassification`, `anthropic.TestProviderErrorClassification`, `gemini.TestProviderErrorClassification`. | UE3, UE4 | `adapter/internal/rest/rest.go:264,295-304`; `adapter/llm/error.go:43,155,189,207`. |
| DV15 | Questions are validated from request JSON, rather than mutating SDK question values after Prepare. | Prepared question values cannot be edited through the SDK API; the regression sends raw JSON instead. | `internal/schema.TestQuestionsValidatedFromWire`. | SC2 | `adapter/seam.go:129-132,168`; `adapter/internal/schema/question_test.go:113`. |
| DV16 | Providers have net/http clients and no vendor retry/pool option to disable. Borrowed clients retain their transport and explicit redirect policy. | No vendor SDK is involved; the adapter retry policy owns HTTP attempt counts. | `TestRetryPolicyControlsHTTPAttempts`, including explicit Chat cases. | RT1 | `adapter/internal/rest/rest.go:185-209`; `adapter/retry.go:365`. |

## Retained diagnostic contracts

Built-in providers retain two pointer levels:
`Provider.state *requestState` → `requestState.credentials *requestCredentials`.
OpenAI/Anthropic inner state holds endpoint/header; Gemini holds key/endpoint.
A single pointer level did not contain fmt's unsupported-verb badVerb
restart at depth zero. The final layout is verified for pinned Go 1.27.1
formatting, not immutable-string zeroization, arbitrary reflect/unsafe memory
access or all future implementations. Models remain intentionally printable.
The source definitions are `openai/openai.go`, `anthropic/anthropic.go` and
`gemini/gemini.go`; the provider formatting regressions enforce the contract.

StatusError value-receiver `MarshalJSON` emits an RFC 8259 JSON string of its
bounded Error text; `LogValue` emits the same text as a slog string
(`adapter/llm/error.go:123-147`). Neither method emits Header or the body tail.
`TestStatusErrorMarshalJSON` and `TestStatusErrorStructuredLogging` pin those
representations. Exported Header/Body fields still retain data. Private
StatusError value wrappers and invalid value `%p`/`%w` remain accepted
limitations, not fixed behavior. Use a pointer and valid pointer verbs, or
retain an explicit Error string for private diagnostic wrappers. Vet is not
a confidentiality guarantee for dynamic formats or boxed `any` values.

`rest.NewBodyScrubber` (`adapter/internal/rest/rest.go:120-151`) replaces exact
known raw/JSON-content and upper/lower percent-hex QueryEscape/PathEscape
forms with `***` before retention. The same diagnostic request-copy scrub is
inside Trace guards; original outbound bytes are unchanged. Empty forms are
skipped and longer overlapping forms match first. This is not recursive or
universal sanitization of caller/provider content. Reports/Trace and root
LevelTrace bodies remain sensitive caller data. REST error URLs omit
userinfo/query/fragment but retain path; path credentials are outside the
promise. Explicit caller redirect policy is a caller-owned trust decision.

## Test-support and platform contracts

Recorder query names are decoded once and compared case-insensitively to
exactly 17 aliases listed in the README. Bare, empty and duplicate sensitive
pairs are removed; undecodable names are dropped; benign raw spelling/order
is retained. Headers keep content-type only. Request/response bodies are
captured **before** REST scrubbing and can contain reflected credentials.
Independent recording inspection/keyscan and maintainer approval are required
before any new cassette is added. Protected upstream fixtures stay byte-equal.
These are recorder/test-support notes, not new numbered wire deviations.

Cassette Transport borrows immutable nested interactions; Requests clones
its outer slice while exposing the documented nested aliases. Borrowed
bodies/headers must not be mutated concurrently. The nested-borrowing test
and alias-destroying mutations enforce this ownership contract. Numeric
replay mismatch paths use the exact matcher comparison, not float rounding.

Unix permission-denial tests require measured capability; Windows cannot
assert Unix permission bits, and root may not experience permission denial.
Case-alias tests skip only when the filesystem demonstrably treats differently
cased names as distinct. A skip is not Windows or live-service execution.
Protected-child regressions also assert no child/intermediate creation and
unchanged sibling bytes after refusal.

The [coverage manifest](uncovered-lines.md) is checked with an explicit
measured profile. Conditional `0-1` rows are permitted only at
`adapter/seam.go:617.3,618.1` and `adapter/seam.go:652.3,653.1`, the two dynamic
logging guards. Other ranged rows, including wider guard ranges, are refused;
fixed-count covered or missing blocks are stale. The ordinary no-profile run
checks real doc/source structure and does not claim a measured comparison.

The caller-client test's four-dimensional nesting refactor is still
**deferred**. Behavioral nil-transport/Chat retry/Responses non-answer/logger
and nil-versus-empty-body improvements do not complete that structural cleanup.
CI fuzz/list command alignment is also deferred as the README/CHANGELOG
state; local gates and the pinned long campaign are the recorded evidence.
