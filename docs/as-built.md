# As built

typesafe-sdk-go was built from a port plan that the owner approved on
2026-09-24 (plan v7): a frozen contract of decisions (D1–D5), requirements
(NF1–NF7), acceptance criteria (AC-F1–AC-F12, AC-P1–AC-P8, AC-Q1–AC-Q4),
risks (K1–K20), a Python → Go behaviour map (its Appendix B) and a map of
the 129 upstream tests (its Appendix D). Where a wave met a fact the plan
had wrong, a gap, or a choice it had left open, the lead recorded a ruling,
and the owner answered the public-contract ones; the rulings are
authoritative over the plan text, which was never edited after its
approval.

This page is the record of those changes: one table per phase, a row per
ruling that changed the contract, and at the end the plan's Appendix B row
by row against [`deviations.md`](deviations.md). It names tests, rulings
and ledger rows, not source files, so a later move of code does not break
it. For what the SDK does differently from the Python SDK, read
[`deviations.md`](deviations.md); for every performance number, the
[ledger](perf/ledger.md) and the [frozen budgets](perf/frozen-budgets.md).

## How to read the tables

| Column | Content |
| --- | --- |
| Ruling | The ruling ids, as the rulings ledger numbers them: `R` a lead ruling, `G` an owner decision, `K` a risk, `F` a finding, `D-…` accepted wave deviations or a lead decision, `V` a gate or review verdict. `-corr`, `-rev`, `-ev`, `b`, `c` are amendments of the ruling they extend. |
| Date (UTC) | When the first ruling of the row was recorded, from `date`. |
| Plan text amended | The section of the plan the ruling changes (`§` a numbered section, `AC-…` an acceptance criterion, `Appendix B` a row of the behaviour map), or `process` for a rule of how the port was built. |
| As built | What the SDK, its tests or its tooling do now. |
| Pinned by | The tests that fail when the behaviour changes, a CI step, or the ledger rows that measured it. |
| Owner | The owner's answer where one was asked (`G…`); `—` where the lead ruled within the plan's delegation. |

A deviation that the change created or reshaped is cited as
`deviation "<key>"`, the key of its row in [`deviations.md`](deviations.md).
`.github/scripts/port-test-matrix.py --as-built` checks, in CI, every
citation on this page against that table, every key of that table against
this page, and every test name against `go test -list`.

## Phase 0: foundation and spikes

Gate: VERIFY P0 PASS at d47b403 (V10) and CRITIC P0 APPROVE (V11),
2026-09-25.

| Ruling | Date (UTC) | Plan text amended | As built | Pinned by | Owner |
| --- | --- | --- | --- | --- | --- |
| R1 | 2026-09-25 05:59Z | §7 W0.1 (go.mod requirements) | Each requirement arrived with its first importing wave (W0.2, W0.2b), because `go mod tidy -diff` fails on a requirement nothing imports. | CI lint step (`go mod tidy -diff`) | — |
| R2 | 2026-09-25 05:59Z | process (§7 "CI green" exit gates) | Waves land on `main` by fast-forward after their gates pass; the repository takes no pull requests. R90 added a green CI dispatch at the wave head before each landing. | process | — |
| G1, R3, R4 | 2026-09-25 06:11Z | §12 `codecov.yaml`; D4 | The Codecov configuration is `.codecov.yaml`; Codecov and CodSpeed authenticate with OIDC (`id-token: write`), so no token secret exists; the owner installed both GitHub Apps. | `ci.yaml`, `bench.yaml` permissions | G1: apps installed; R3, R4 are owner instructions |
| R5, R5-corr | 2026-09-25 06:17Z | §11, §12 `bench.yaml` (`-run '^$'`) | `bench.yaml` runs `go test -bench=. ./...`: the CodSpeed Go runner reads only `-bench` and `-benchtime` and appends `-run=^$` itself. CodSpeed accepted `ubuntu-26.04`, so D4's image exception was not needed. | `bench.yaml` | — |
| R6 | 2026-09-25 06:23Z | process (commit trailers) | A commit's `Co-Authored-By` names the model that wrote it. Refined by R118 (Phase 5). | process | — |
| R7 | 2026-09-25 06:23Z | §12 (actions pinned at their major) | `astral-sh/setup-uv` is pinned at a full version: it publishes no major tag since v8. | `ci.yaml` | — |
| R8 | 2026-09-25 06:23Z | §12 lint job | The lint job also runs the checker scripts' pytest suite; `.github/actionlint.yaml` declares the runner labels. | CI step "Test the checker scripts" | — |
| R9 | 2026-09-25 06:23Z | process | Parallel lanes work in separate worktrees on `wave/<id>` branches. | process | — |
| R10 | 2026-09-25 06:33Z | D1 ("one-edit bump") | The Go 1.28 bump is a multi-site edit (both constraint lines, the identifier, the refusal greps, the gotip probe, `docs/support.md`); the seam test asserts one identical identifier at every site. | `TestSeamD1IdentifierSites` | — |
| R11 | 2026-09-25 06:43Z | AC-F1; Appendix D citation form | The matrix checker collects upstream tests by pytest's default rules; a deviation row cites the word `deviation` and the key of its row in double quotes, since Appendix B is unnumbered and `B<n>` names benchmarks. | CI step "Check the port test matrix" | — |
| R12, R12b | 2026-09-25 06:52Z | §6.2.3 fixture names | Refused bodies are `malformed-*.json`, bodies Go accepts where Python refuses or reads them otherwise are `deviation-*.json`; `testdata/README.md` holds each verdict, probed against typesafe_sdk 0.7.1. | `TestMalformedFixturesRefused`, `TestDecodeFixtures`, `TestFixtureManifest` | — |
| R13 | 2026-09-25 06:52Z | §11 (x/net test-only check) | The check is the module form, `go mod why -m golang.org/x/net`. | `TestSeamTransitiveImports` | — |
| F1, F1-b, F1-c, F1-d, G2 | 2026-09-25 06:52Z | D2; NF4; §6.3; §8 AC-P4 | golang/go#70809: strict stream accounting stalls callers beyond the server's stream limit. As built, option (iv-b): strict mode, the cold-start gate and one header-write token per client transport, returned at `WroteHeaders`, except that the first request on each new connection holds it until its response headers. AC-P4's ordering clause reads: the leader's request is answered first, then the other 63 requests are all on the wire, on the same connection, before any of their responses. The live API's 1024 streams was cited, not measured; W6.4 measured 100. | `TestFanOut`, `TestTokenResidualK21`; ledger W0.4-06, W0.4b rows | G2 (a): (iv-b) chosen |
| R14, R23 | 2026-09-25 06:54Z | §6.2 (per-string check); NF2 | sonic accepts raw U+0000–U+001F inside strings. The visitor checks each string it delivers; only when one fails does a word-at-a-time pre-check and one tracked scan look for raw control bytes inside strings. A raw control character is a `*ResponseValidationError` at `.`, as Python refuses it. | `TestControlRule`, `TestMalformedFixturesRefused` | — |
| R15, R15b | 2026-09-25 06:54Z | §12 `gotip.yaml` (PM5 probe) | Two signals, each with its own issue: a Go 1.28 release candidate in the download index while sonic's fallback still compiles ("Go 1.28: waiting on sonic"), and sonic's JIT file selected on tip ("Go 1.28: sonic builds on tip, bump D1"). | `gotip.yaml` | — |
| D-W0.2 | 2026-09-25 06:54Z | §11 refusal greps; §4 seam | The CI refusal steps drop setup-go's problem matcher and require a non-zero exit and the identifier; `internal/wire` imports the standard library only; `FuzzValidString` added. | CI step "D1 refusal off the support matrix"; `TestSeamImports` | — |
| R17 | 2026-09-25 07:11Z | §7 (ledger rows) | Every timing row runs under the host's bench lock, records the load in the same command, and is marked `noisy` above the core count; allocation and connection counts do not depend on load. | the ledger's Load column | — |
| R18, R18b, D-P0P | 2026-09-25 07:20Z | §12 test job; §8 AC-Q1 | A non-race CI step runs `internal/codec`, `internal/wire` and `internal/testsupport`; `internal/codec`'s allocation tests skip themselves under `-race`, since a codec file may carry only the D1 constraint line. | CI step "go test without -race (allocation tests)"; `TestSeamBuildConstraints` | — |
| R19 | 2026-09-25 07:51Z | §6.3 failure table, row 2 | Every waiter a failed gate leader releases gets a fresh error of the leader's class wrapping the shared cause; a TLS-silent peer is a `*TimeoutError` for all of them. | `TestTLSSilentPeer`, `TestClassify` | — |
| R20 | 2026-09-25 07:51Z | §3.2 (TLS facts) | A remote alert 120 arrives as `tls.alert` inside `*net.OpError{Op: "remote error"}`, wrapped once more in `proxyconnect` behind a strict TLS proxy, which is classified first; `VerifyConnection` runs before the handshake completes, so the h2 check is scoped to the API hop by SNI. | `TestProxyTLS`, `TestNoALPN`, `TestALPNHTTP1Only` | — |
| R21, R51 | 2026-09-25 07:51Z | process (§7 "code-reviewer per wave") | The spike waves W0.3–W0.5 and the measurement-only W1.3 were reviewed by their phase gate's verifier and critic. | process | — |
| R22, R22b | 2026-09-25 07:57Z | §8 AC-P1 (the sequence) | The mixed-size sequence is asserted for a `RawJSON` state and a boxed string, from call 2 on; `*struct` and map states are recorded per host, since sonic's growth takes a 6 MiB `*struct` past the 8 MiB ceiling on arm64. | `TestAllocScratchSequence` | G2 (b), G3 (b): approved |
| R24, R24b | 2026-09-25 07:57Z | §6.2 (S-D1); NF1, NF2; §8 AC-P2, AC-P8 | S-D1's winner (a1): the Preorder visitor without skipping, `decoder.Skip`'s trailing-data check and one lazy root pass. AC-P2 is a budget per fixture (`result.json` 4, `duplicates.json` 17); AC-P8's lazy pass allocates at most 20 + ⌈members / 15⌉ + escaped keys + 1; NF1's `E_sonic` is 0 for `RawJSON`, 1 for a boxed value and 1 + m for a state holding m maps; a warm-pool call's bytes stay within 112 B above sonic's. | `TestAllocDecodeFixtures`, `TestLinearityFlood`, `TestLazyPassAllocations`, `TestAllocEncode` | G2 (b), (c): approved |
| R25 | 2026-09-25 08:10Z | process (raw files) | The owner's global ignore dropped raw benchmark files; the spikes' `.gitignore` re-includes them, and lanes check a cited file with `git ls-files`. | process | — |
| R26, R26b | 2026-09-25 10:45Z | §8 AC-P5; NF5 | The bounds are per attempt, W0.5's measurements plus 64 KiB: a declared 16 MiB with a 10-byte body at most 327 680 B; a declared 16 MiB + 1 refused before any read, at most 65 536 B; an undeclared 16 MiB + 1 and a 16 MiB body at most 2 × cap + 65 536 B. | `TestMemStatsCap` | — |
| R27 | 2026-09-25 10:45Z | NF5 (initial buffer); §1.1.3 | A declared body starts in `min(Content-Length, 256 KiB)`; an undeclared one in 4 KiB, doubling; `X-TypeSafe-Retry-Count` is sent on retries only, as Python sends it. | `TestReadBody`, `TestMemStatsCap`, `TestAttemptHeader` | — |
| R28, R28b | 2026-09-25 10:45Z | §1.2 NF3; §8 AC-P6 | N was provisional at 15 over the Recorder (floor 8); the time clause held against an encoding/json naive client but not against a sonic one on arm64, which went to the owner (G3). N was frozen at 14 by W3.4 and lowered to 12 by W5.3 (R104, Phase 3). | ledger W0.5-01..04 | G3 (a) |
| R29, R29b, R29c | 2026-09-25 10:59Z | §8 AC-P4 (wording) | 200 calls against a server limit of 8 all succeed within the per-call deadline, on one connection; the first request's hold is bounded by the gate's wait bound (`connectTimeout` + `TLSHandshakeTimeout`, 20 s by default), after which waiters fall through; the ordering test delays the leader's response and compares the order of client trace events. | `TestFanOut`, `TestWaiterFallThrough` | — |
| K22 | 2026-09-25 11:11Z | NF4 (risk) | A cold burst on a new connection pays one leader response before the others are written (103 against 52 ms at 50 ms of service); W6.4 recorded 178–270 ms for a cold first response from the live API. | ledger W0.4b, W6.4 rows | — |
| K23, K24 | 2026-09-25 11:11Z | §6.2; §8 AC-P6 (arm64) | Risks, recorded and not gated (K18): on arm64 the visitor decode is slower than sonic's generic decode (1.9–3.0× after K36), and `*struct` or map states of about 6–8 MiB never return to the pool. | ledger W3.4-09, W5.3 rows; `TestAllocEncode` | G3 (a), (b) |
| G3, R16, R31, R31b | 2026-09-25 11:17Z | D5; §8 AC-P6, AC-P7 (comparator) | The comparator is a sonic naive client (`internal/testsupport/naive`; encoding/json is reported only); D5 covers every go.mod requirement, direct and indirect, checked as `go list -m -u -f '{{if .Update}}{{.Path}}{{end}}' $(go mod edit -json \| jq -r '.Require[].Path')` (graph-only modules print newer versions no build uses); `LICENSE-THIRD-PARTY` was removed. | `BenchmarkCall`, `TestNaiveRequestMatchesSDK`; release checklist | G3 (a), (c) |
| R30 | 2026-09-25 11:11Z | §11; AC-Q1 | gofumpt's result is checked by its own exit status and output (`out="$(gofumpt -extra -l .)"`), and govulncheck runs as `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` where the installed binary is older than the toolchain. | CI lint step | — |

## Phase 1: types, questions, prepared serialisation, content

Gate: VERIFY P1 PASS at 332de47 (V14c), 2026-09-25; the plan gives Phase 1
no critic.

| Ruling | Date (UTC) | Plan text amended | As built | Pinned by | Owner |
| --- | --- | --- | --- | --- | --- |
| R32, R40, R43 | 2026-09-25 11:18Z | §7 W2.0 (the error family) | W1.1 and W1.2 added `*ConfigError` and `*InvalidRequestError` early, which W2.0 completed without renaming; `ConfigError`'s godoc promises no sentinels. | `TestConfigError`, `TestInvalidRequestError` | — |
| R33 | 2026-09-25 11:18Z | §6.1.1 | The compact `questions` bytes are written by `internal/wire` (standard library only) with an escaper that matches Python's `to_json`; structured content is spliced as given. | `TestPreparedBytesMatchPython` | — |
| R34 | 2026-09-25 11:52Z | §6.1.2 (`RawJSON` state) | Content passed to `Prepare` gets a lexical JSON check (invalid or scalar JSON is a `*ConfigError`); a per-call `RawJSON` state gets an O(1) shape check only, and its validity is the caller's contract. | `TestContent`, `TestPrepareRejects` | — |
| R35, R45 | 2026-09-25 11:52Z | §5 (`{"angry", nil}`) | Options are keyed literals, `Option{Label: "angry"}`: `Content` carries an unset bit, so the positional form does not compile; godoc and examples use the keyed form. | `TestPublicAPISurface` | — |
| R36 | 2026-09-25 11:52Z | Appendix B (a new row) | Every failure of `Prepare` is a `*ConfigError`, Python's four normalisation rules included; a fault in one call's state is an `*InvalidRequestError`. (deviation "`Prepare` fails with `*ConfigError`") | `TestPrepareRejects` | — |
| R37 | 2026-09-25 11:52Z | §5 (`RawQuestion.Fields`) | A fixed value set read without reflection; anything else fails in `Prepare`, and `RawJSON` is the way out. | `TestPrepareRejects` | — |
| R38 | 2026-09-25 11:52Z | Appendix B (a new row) | A raw question's fields go out after `type` in sorted key order; Python keeps insertion order. (deviation "raw question field order") | `TestRawQuestionsPassThrough` | — |
| R39 | 2026-09-25 11:52Z | process | `internal/wire`'s JSON primitives moved to a file of their own, a pure move. | process | — |
| R41 | 2026-09-25 11:55Z | §4 (seam); §12 `gotip.yaml` | The root package imports `internal/codec` and no JSON library; the gotip canary runs `internal/wire` and `internal/h2gate`. | `TestSeamTransitiveImports` | — |
| K25 | 2026-09-25 11:56Z | risk | A replay flake seen once in about 38 000 runs; W6.1 added a regression test for its known race, and 5 000 contended runs per host did not reproduce it. | `TestGoAwayRaceWithFinish`; ledger W6.1-09..11 | — |
| R42, R42-ev | 2026-09-25 12:13Z | §6.1.1 (raw-field floats) | A float in a raw question field is spelled as pydantic-core 2.46.5's `to_json` spells it, derived from its source (parity; a 249 938-value sweep agreed). | `TestPreparedBytesMatchPython` | — |
| R44 | 2026-09-25 12:13Z | §8 AC-Q3 (four fuzz targets) | `FuzzAppendJSON` (invariants in `internal/wire`, a differential against encoding/json in `internal/codec`) and `FuzzValidString` were added. W6.1 ruled on a fifth target: CI fuzzes eight targets for 60 s each (AC-Q3's four, `FuzzDecodePaths`, `FuzzFalsyJSON`, `FuzzIsSecretHeader` and `FuzzValidUTF8`) and runs these three as seed corpora only. | CI `fuzz` job | — |
| R42b, R46, R59 | 2026-09-25 12:47Z | Appendix B (a new row); §6.1.2 | Floats in the state and in extra values keep sonic's spelling: an integral float loses `.0`, `-0.0` is `0` on arm64 and `-0` on amd64 (K27), 1e16 ≤ \|x\| < 1e21 is written as digits, and 1e-6 ≤ \|x\| < 1e-5 in fixed notation; a `RawJSON` or string state is the way to exact bytes. (deviation "state encoding") | `TestBodyDeviationsFromPython` | G6 (4): KEPT (D1 stands) |
| R47 | 2026-09-25 12:47Z | Appendix B ("state encoding") | `\b` and `\f` in a string state go out as `\u0008` and `\u000c`, sonic's forms; `Text` content writes `\b` and `\f`. | `TestBodyDeviationsFromPython` | — |
| R48, R54 | 2026-09-25 12:47Z | NF1; Appendix B (state row) | A state that is not valid UTF-8 is an `*InvalidRequestError` before the network, as Python refuses a lone surrogate; the check is sonic's validator on amd64 and `utf8.Valid` on arm64 since the owner's G8-b (Phase 5). (deviation "`any` state") | `TestClientInvalidUTF8StateFailsBeforeNetwork`, `TestValidUTF8Parity` | G8-b Q4 |
| R49, R56, R59b | 2026-09-25 12:47Z | §5 (a "slice" state) | A plain top-level `[]byte` (or a named byte-slice type without a Marshaler) as the state or an extra value is refused, naming `string(b)` and `RawJSON(b)`; a nested `[]byte` stays base64. An extra `state` member that is nil, a number or a boolean is refused where Python sends it, and an extra `questions` nil is sent. | `TestUnencodableBodyFailsBeforeNetwork`, `TestClientUnencodableBodyFailsBeforeNetwork`, `TestExtraBodyShallowOverride` | — |
| R50 | 2026-09-25 13:03Z | §12 | `TestAllocPrepare` ran in a root non-race CI step from its landing; W5.2 folded that step into the allocation-budget step. | CI step "go test without -race (allocation budgets)" | — |
| R52, R57, R59c, R59c-corr, K26, R60, K26-corr, R61 | 2026-09-25 13:25Z | §5 (the state kinds); §6.1.2 | `Content` is accepted as a state and has `MarshalJSON`, and so has `RawJSON`; a nested JSON `Content` or `RawJSON` is compacted by `internal/wire`'s scanner, because sonic's check of a Marshaler's output depends on memory (K26), at one scan and one copy per nested value; a caller's own `json.Marshaler` output is the caller's contract. | `TestNestedContentEncodesAsContent`, `TestNestedRawJSONEncodesAsJSON`, `TestContentMarshalJSON`, `TestRawJSONMarshalJSON` | G8-a Q5: no upstream report |
| R53 | 2026-09-25 13:25Z | §5 (error chain) | `*InvalidRequestError` wraps `*codec.EncodeError`, which wraps sonic's error. | `TestInvalidRequestErrorChain` | — |
| R55 | 2026-09-25 13:25Z | Appendix B ("state encoding") | A map state goes out in Go's iteration order, so two calls with one map may send different bytes; a struct or `RawJSON` gives stable bytes. | the state encoder's godoc; `TestBodyBytesMatchPython` uses one member per map level for this reason | — |
| D-W1.2, D-W1.2b | 2026-09-25 13:25Z | §7 W1.2 | Wave deviations D-W1.2-1..16, among them `Content` as a state, the model's UTF-8 check as a `*ConfigError`, the UTF-8 pass over extra values, D-W1.2-5 (map order, R55), D-W1.2-6 (`[]byte`, R56) and D-W1.2-7 (nested whitespace, closed by R60). | as in the rows above | — |
| R58, R58b | 2026-09-25 13:46Z | NF7 (`InvalidRequestError` text) | An error's text never repeats the caller's state: the cause is escaped and cut at 200 characters after escaping, an extra member's name at 128, and sonic's message about a Marshaler's output becomes a fixed sentence. | `TestEncodeErrorMessageIsBounded`, `TestEncodeErrorHidesMarshalerOutput` | — |
| K27, R62, K27-cause | 2026-09-25 14:36Z | §3.3 (sonic facts); the tests' policy | sonic writes `-0.0` as `0` on arm64 (its VM) and `-0` on amd64 (its JIT); every pin of sonic's bytes that can differ is keyed by GOARCH, and a wave that pins sonic bytes or counts runs `-race ./...` on the amd64 host before it is done. | `TestBodyDeviationsFromPython`, `TestCodecEncodeSpellings` | G8-a Q5: no upstream report |

## Phase 2: codec, errors, transport, client, models, retry skeleton

Gate: VERIFY P2 PASS at 45fd018 (V28) with the re-verify PASS at b5b1a2b
(V29), and CRITIC P2 APPROVE at b3fd5db (V38) after a REJECT for a red
`main` (V30, V31), 2026-09-26.

| Ruling | Date (UTC) | Plan text amended | As built | Pinned by | Owner |
| --- | --- | --- | --- | --- | --- |
| R63, R63b | 2026-09-25 15:10Z | §0 (the runtime header); Appendix B ("SDK/runtime headers"); §5 | `X-TypeSafe-SDK` equals the User-Agent base, `typesafe-sdk-go/<Version>`; `X-TypeSafe-Runtime` is `go/<release> (<GOOS>; <GOARCH>)`, the release cut at the first `-X:` or ` X:` and a `devel` string kept whole; a caller's `User-Agent` is dropped (`WithUserAgentProduct` adds a product); a GET carries no `Content-Type`; `Proxy-Connection` is a framing header; `NewClient` refuses, without repeating the value, a base URL with userinfo, a query, a fragment, no host or a scheme other than http and https, a blank model and a response limit over 1 GiB. (deviation "configuration checked at build"; deviation "SDK headers") | `TestRuntimeHeaderValue`, `TestSDKIdentifier`, `TestHeaderTemplate`, `TestBaseURL`, `TestModel`, `TestMaxResponseBytes`, `TestUserAgentProductRules` | — |
| R64 | 2026-09-25 15:14Z | §5 (option names) | Client options are `ClientOption` and per-call options `CallOption`; `Option` is one option of a choice. | `TestClientOptionsSurface` | — |
| D-W2.1, D-W2.1b | 2026-09-25 15:31Z | §7 W2.1 | Wave deviations D-W2.1-1..16, among them the pointer-backed redacted header view (D-W2.1-14), `WithHeader` replacing a header whatever its case where upstream's defaults mapping sends both spellings (-15), and a header name that holds the key refused (-16). (deviation "caller headers") | `TestRedactedHeadersNeverPrintKey`, `TestHeaderNameHoldingKey`, `TestInvalidHeader` | — |
| R65, R65b | 2026-09-25 15:43Z | process | No lane uses `git stash` (every worktree shares `refs/stash`); rebases run with `rebase.autoStash=false`. | process | — |
| R66, R68 | 2026-09-25 15:53Z | §9 (redaction); Appendix B ("Redaction by header name") | The redacted header view prints redacted through every fmt verb; a `Client` keeps its key two pointers away, so no verb prints it; the key is a needle (a refused header name, a redacted value) only when it is at least 8 bytes long, while redaction by name always applies. | `TestRedactedHeadersNeverPrintKey`, `TestClientNeverPrintsKey`, `TestHeaderNameHoldingKey`, `TestAPIKeyNeedleThreshold` | — |
| R67, R67-ev | 2026-09-25 15:58Z | §5 (transport options); §6.3 (classification) | `WithProxy`, `WithRootCAs` or `WithTLSConfig` with `WithHTTPTransport` is a `*ConfigError`, while `WithConnectTimeout` stays allowed with a caller transport; a proxy-hop timeout is a `*TimeoutError` that names the proxy; wave deviations D-W2.2-1..12; K20 closed by measurement (the gate keeps `HTTPAuto` at 1 connection, 10 of 10). | `TestTransportOptionsAreExclusive`, `TestDialErrorsMapToSDKErrors`, `TestProxy`, `TestLoopbackStreamLimit` | — |
| K21b, K21c, R69, R69-ev, R72b | 2026-09-25 11:11Z | K21 | After a GOAWAY the standard library replays the dropped requests onto the new connection without the token; the gate marks that connection unsettled, and the next token holder on it holds the token until its response headers (the settle hold); a replay's own response clears its mark. | `TestSettleHold`, `TestTokenResidualK21` | — |
| R70 | 2026-09-25 16:27Z | §12 (Codecov); §5 (base URL); Appendix B ("first error in wire order"); §8 AC-P2 | The Codecov upload fails the job on error; an explicit default port is dropped at build; the decoder reports the first failure in Python's order, which supersedes Appendix B's ordering row except for a legend value of the wrong kind; AC-P2 is pinned as exact counts under the frozen ceilings; an error body that is not a JSON object with a message is the compacted body; `RetryAfter` reads every status; `IsAuthentication` reads `detail.error_type`; a structured legend keeps its received bytes; spellings of one key fold in wire order; a token count above 2⁶⁴−1 is refused. (deviation "legend value path"; deviation "usage counts") | `TestBaseURLDefaultPortDropped`, `TestDecodeFieldPaths`, `TestAllocDecodeFixtures`, `TestReadErrorBody`, `TestIsAuthentication`, `TestStructuredLegendExactBytes`, `TestLevelKeys` | — |
| R71 | 2026-09-25 16:42Z | §5 ("when zero") | Under `HTTP2Only` a caller transport's clone always runs strict stream accounting, whatever its `HTTP2Config` says; the ping timeouts are set only when zero. | `TestWrap` | — |
| R72, R72-ev | 2026-09-25 16:42Z | §6.3 (gate liveness) | A panicking trace hook or proxy function cannot leave the gate or the token taken; `HTTPAuto` costs one dial latency per new connection, since the token serialises new dials. | `TestPanicUnwind` | — |
| R73, R73-ev | 2026-09-25 16:53Z | Appendix B ("Any nesting depth", "NaN/Infinity", "`Retry-After` float ms") | The decoder refuses more than sonic's 4096 nested containers at `.`, where Python refuses more than 200 (Go is the lenient side); `NaN` or `Infinity` anywhere in a 2xx body is a `*ResponseValidationError`; an error body holding `NaN` is text to Go; a `Retry-After` date is read only in the formats `http.ParseTime` reads; a count of `-0` reads as 0; a bad value superseded under another spelling of its key still fails. (deviation "nesting depth"; deviation "non-finite numbers in a response"; deviation "`Retry-After` precision") | `TestDecodeDepthBound`, `TestReadErrorBody`, `TestRetryAfter`, `TestNegativeZero`, `TestLevelKeys` | — |
| K28, K28b, K28c | 2026-09-25 16:56Z | §5 (`WithClientTrace`) | A panic in a caller's trace hook that the standard library calls under its pool lock would leave that lock taken; every caller hook, from the option or from the call's context, runs behind a shield that recovers the panic and raises it on the caller after `RoundTrip` returns. | `TestClientTraceShieldCoversEveryHook`, `TestClientTracePanicIsRaisedOnTheCaller`, `TestClientTraceColdDialPanic`, `TestUntracedContext` | — |
| R74 | 2026-09-25 17:06Z | process (§7 W2.2) | W2.2 landed in two parts: `internal/h2gate` first, the root transport after W2.0. | process | — |
| K29, R75, K29-cause, K29-closed | 2026-09-25 17:14Z | the h2gate tests' oracles | The order clauses compare a sequence number that every trace hook takes, not timestamps (Windows clocks tick in 15.6 ms steps); the ALPN test waits for the server's handshake record. | `TestFanOut`, `TestWaiterFallThrough`, `TestALPNHTTP1Only` | — |
| K30, K30-closed | 2026-09-25 17:38Z | §8 AC-P8 (how its time is taken) | The 10⁴ : 10³ time ratio is taken over adaptive spans: each flood decoded until at least 250 ms have passed, the minimum over five alternating spans; the frozen budgets' "Measured" cell keeps W0.3's single-decode figures. | `TestLinearityFlood`, `TestLinearityFloodTime` | — |
| R76, R76b, D-W2.2b | 2026-09-25 17:44Z | §5 (client and call API); §8 AC-F9 | `CallOption`s are `Model`, `Timeout`, `Header`, `ExtraBody` and `Retry`; `Models().List` refuses `Model` and `ExtraBody`; `LevelTrace` and `Stats{Dials, Attempts}` are exported; `Timeout(d)` with d ≤ 0 is a `*ConfigError`; `ErrClientClosed` belongs to the transport; `WithConnectTimeout` with `WithRoundTripper` is refused. | `TestCallOptionsRefused`, `TestClientClosedError`, `TestClosedClientRefusesCalls`, `TestTransportOptionsAreExclusive` | — |
| R77 | 2026-09-25 18:32Z | §6.3 ("per call `make(http.Header, n)`") | The first attempt sends the client's header template itself; a retry clones it and adds `X-TypeSafe-Retry-Count`. A caller's `RoundTripper` must not modify `req.Header` (its godoc says so). | `TestAttemptHeader` | — |
| R78, R78-ev | 2026-09-25 18:36Z | §6.3 (error chains) | The cause of a proxy-hop timeout and the not-negotiated detail are scrubbed like a connection error's; R81 (3) replaced the dropped causes by a stand-in. | `TestDialErrorsMapToSDKErrors`, `TestScrubUserinfo` | — |
| R79, R79-ev | 2026-09-25 19:00Z | §8 AC-F9 (wording) | `Close` idles a `WithRoundTripper` transport that has `CloseIdleConnections`, then closes it once if it is an `io.Closer`; each request gets its own copy of the endpoint URL; wave deviations D-W2.3-1..12. | `TestCloseIdlesSuppliedHTTPTransport`, `TestRequestURLIsCopied`, `TestRetryURLIsCopied` | — |
| R80, D-W2.4 | 2026-09-25 19:41Z | §5 (the response JSON sketch) | A response's `MarshalJSON` writes through `internal/wire`'s typed writer, byte for byte what Python's `model_dump_json` writes, with no per-architecture pin; members in Python's order (model, usage, answers; an absent count `null`); `Answer`, `Usage` and `ModelCard` marshal too; a stored payload that fails validation is a `*ResponseValidationError` with status 0; encoding/json HTML-escapes a Marshaler's output (the godoc says so). | `TestResponseJSONFixtures`, `TestAnswerJSONShapes`, `TestResponseUnmarshalJSON`, `TestStdlibJSON` | — |
| R81, D-W2.5 | 2026-09-25 20:15Z | §6.3; §8 AC-F9 ("an error wrapping `ctx.Err()`") | A cancelled context returns `context.Canceled` itself after one attempt; a deadline that passes returns a `*TimeoutError`; the whole `Authorization` value is `***` in error text; a cause whose text printed a credential is replaced by a stand-in holding the redacted text; a needle shorter than 8 bytes is not scrubbed from free text (a documented limit). (deviation "cancellation"; deviation "cause via `errors.Unwrap` unless it printed a credential") | `TestCancelledContextMakesOneAttempt`, `TestCancelInFlightRequest`, `TestCredentialsCause`, `TestTransportErrorsHoldNoCredential` | G6 (1): KEPT |
| R82 | 2026-09-25 20:50Z | Appendix B ("Transport error text verbatim") | A credential in a caller error's fields is looked for in its `%+v` and `%#v` renderings too. Limits: a caller error type that holds the request in a pointer field stays reachable through `errors.As` (it is the caller's own credential; upstream rebuilds the chain instead); userinfo without a scheme, Digest, AWS4 and decoded Basic credentials, whitespace after the scheme and overlapping needles are not scrubbed. | `TestTransportErrorFieldsHoldNoCredential`, `TestTransportErrorTextScrubbedBeforeCut` | — |
| R83 | 2026-09-25 21:06Z | process | The obsolete `*.[568vq]` ignore patterns, which hid a wave's raw files, were deleted. | process | — |
| R84 | 2026-09-25 22:10Z | §8 AC-F5 (the log half) | `internal/h2gate` renders a transport error in its DEBUG records through the SDK's scrub, and only when the logger keeps DEBUG. | `TestLogErrorText`, `TestTransportDebugRecordsHoldNoCredential` | — |
| R85, K28d, V71 | 2026-09-25 22:10Z | K19; §5 (`WithClientTrace`) | A trace hook that blocks holds the header-write token; since W6.1 the token wait is bounded by the hold bound and the godoc states the caller's contract. One case stayed unbounded (V71): a hook that blocks while a new connection is dialled (DNS, connect, the TLS handshake's start) holds every other call on the client until it returns or their own deadlines pass, even under `WithNoTimeout`; the owner asked for a bound before v0.1.0 (G11 (5), Phase 6). | `TestTokenWaitBound`, `TestTokenFreeTakesNoWait` | G11 (5) |
| R86, R89 | 2026-09-25 22:10Z | the test harness; the ledger | Contention flakes fixed in the test servers (the K29 class); a raw file over 512 KB is a filtered `-v` output; the contention script's lock, a no-op on macOS, was fixed in W6.1. | `TestProxyModes`, `TestTokenResidualK21` | — |
| K32, K33, K34, R100 | 2026-09-25 23:40Z | §8 AC-P5; the loopback tests | AC-P5's bounds hold on every run (the error path's scrub moves case (i) by 1–4 allocations between runs, inside the bound); the loopback server's close choreography keeps Windows from aborting a connection with data unread; the strict close order is required only where the server must close first. | `TestMemStatsCap`, `TestTransportErrorsBecomeConnectionOrTimeout`, `TestReplay`, `TestGoAway` | — |
| R90, R90b, R90b-corr | 2026-09-26 00:00Z | process (§12 landing) | `ci.yaml` has `workflow_dispatch`; a wave lands only after a green dispatch at its head (`headSha` equal to the landing SHA), three in a row for a landing whose purpose is a flake class. | `ci.yaml` trigger; each landing's run id in the rulings ledger | — |

## Phase 3: retries, telemetry, redaction

Gate: VERIFY P3 PASS at f048059 (V49) and CRITIC P3 APPROVE (V50),
2026-09-26; the owner's review of the public-contract rulings followed
(G6, G7).

| Ruling | Date (UTC) | Plan text amended | As built | Pinned by | Owner |
| --- | --- | --- | --- | --- | --- |
| R87, R93, R93-corr, R93-corr-b | 2026-09-25 22:19Z | Appendix B ("Redaction by header name") | `*APIError`, `*ResponseValidationError` and `*ResponseTooLargeError` keep a copy of the response header in which credential headers, and values holding the client's key of 8 bytes or more, are `***`; `Retry-After`, `retry-after-ms` and the request id stay readable unless they hold the key. (deviation "stored headers redacted") | `TestErrorHeadersRedacted`, `TestRedactHeader` | G6 (2): KEPT |
| R88, R88b, R88b-corr, D-W3.2, R97, R97-corr | 2026-09-25 22:19Z | §5, §6.4 (`RetryPolicy`); Appendix B ("`RetryPolicy.exceptions`") | `RetryPolicy` is a value with unexported fields whose zero value is `DefaultRetry()`, Python's `RetryPolicy()` (2 retries; backoff 500 ms, 5 s, jitter 0.25; statuses 408, 429 and 500–599; `Retry-After` honoured; a 30 s budget); setters return changed copies; an invalid setting is a `*ConfigError` with Python's message; times are `time.Duration`, so NaN, ±Inf and fractions of a nanosecond cannot be written (partial deviations on RT1, RT3, RT5); the budget counts from the first attempt's start; a deadline that ends a wait returns a `*TimeoutError` with no timeout, and the last server error is lost; `Statuses` is a sorted slice; a transport-reported `context.Canceled` while the call's context lives is a `*ConnectionError` and is not retried. The test helpers default to one attempt, as upstream's conftest does. (deviation "retry policy as a value") | `TestRetryPolicyDefaults`, `TestRetryPolicyRules`, `TestRetryBudgetStopsBeforeDelay`, `TestCallerDeadlineEndsAnAttempt`, `TestRetryClassSwitches`, the RT1–RT25 tests | G6 (3): KEPT |
| R91, R92, G4 | 2026-09-26 00:00Z | process (owner gates) | The public-contract rulings (R81 (1), R87, R88, R46, the AC-P6 freeze, later R94, R99, R103) went to the owner in one batch at the Phase 3 gate; `w7-owed.md` held every item owed to this page; the owner raised parallelism to the maximum (G4). | process | G4; G6, G7 answered the batch |
| D-W3.1 | 2026-09-26 00:29Z | §8 E1, E2 | `errors.As` needs a pointer to the pointer type (a value target panics); Python's base class `TypeSafeAPIError` maps to `Kind` by status and `APIErrorOther` in a caller's own literal; httpx's `Timeout` object has no counterpart beyond `*TimeoutError` with no timeout. (deviation "errors are values") | `TestErrorsAsRoundTrip`, `TestErrorInterfaceExcludesForeignErrors` | — |
| R95 | 2026-09-26 00:57Z | L4, L5 | The stand-in for a dropped cause prints, under `%+v` and `%#v`, the scrubbed rendering of the original chain as one escaped line of at most 1024 characters; Python rebuilds typed causes. | `TestScrubbedErrorFormat` | — |
| D-W3.3, R102 | 2026-09-26 03:11Z | §9 (telemetry); §8 AC-P6 | The INFO "response" record costs one allocation per attempt (its sixth attribute); trimming it to five would change §9's record and was not done. TRACE bodies are not scrubbed, and a 2xx response's `Meta().Header` is not redacted. | `TestAllocLoggedCall`, `TestLogLevelsPerAttempt`, `TestClientLogRecords` | — |
| R103, R103b, R103-rev, R103-rev-corr, R107, R114 | 2026-09-26 03:39Z | Appendix B ("Redaction"); §8 AC-F5 | A key the server echoes in an error message or a field path is shown as the server sent it, as upstream shows it (R103 and R103b reverted); a header-derived value (the request id, a header's value) that is a credential or holds the key is `***` in errors and log records alike, the INFO record's request id included; body text at WARN and the TRACE body are as received. (deviation "redaction by source") | `TestServerEchoedKeyShownAsReceived`, `TestHeaderRedactorRequestID`, `TestAllocRequestID` | G7 (8): R103 reversed; R114: R107 confirmed |
| D-W3.4, R104, R104-corr | 2026-09-26 04:36Z | §1.2 NF3; §8 AC-P6 | NF3 as built: a call with 3 questions, a 1 KiB boxed state, `DefaultRetry()` whose first attempt succeeds and the default logger makes at most N SDK-owned allocations above the floor (8 allocations, 640 B), at most half of the sonic naive client's own, and runs faster than it on amd64 (arm64 recorded, K18). N was frozen at 14 with the exact pin equal to it (no headroom) and is 12 since W5.3, 12 allocations and 2008 B: `context.WithTimeout` 4 (272 B), the decoder's three fold slices 3 (240 B), the body read 1 (384 B), the `*http.Request` 1 (320 B), the call's own allocation 1 (704 B: the response, the first URL copy and three answer entries), the body handle 1 (64 B) and the `GetBody` method value 1 (24 B); a pin moves only together with its frozen-budgets row. AC-P6 as built: at most N, and `BenchmarkCall/sdk` below `BenchmarkCall/naive` on q3, asserted on amd64. The 3-of-5 rule fails with probability about 10⁻⁵ per run. | `TestAllocWholeCall`; ledger W3.4-01..11 and W5.3 rows | G7 (5): KEPT |
| G6, G7 | 2026-09-26 05:13Z | the R91 owner batch | Kept: R81 (1), R87, R88, R46, AC-P6's N with no headroom, R94, R99 (b). Reversed: R99 (a) (typed paths take Python's form, R99-rev) and R103/R103b (a server-echoed key is shown as received, R103-rev). | as in each ruling's row | G6, G7 |
| R106 | 2026-09-26 05:00Z | process (charters) | Critic-p3's findings became charter duties: W5.2 wrote `TestResponseCapOverTheWire`, `TestAllocLoggedCall` joined the CI list, and the CodSpeed job authenticates with OIDC. | `TestResponseCapOverTheWire` | — |

## Phase 4: typed answers

Gate: VERIFY P4 PASS at a4cbb5d (V54) and CRITIC P4 APPROVE (V55),
2026-09-26; the owner's Phase 4 batch followed (R114–R117).

| Ruling | Date (UTC) | Plan text amended | As built | Pinned by | Owner |
| --- | --- | --- | --- | --- | --- |
| D-W4.1, R94 | 2026-09-26 00:44Z | §5 (typed answers) | `PreparedFor[T]() (*Prepared, error)`, so §5's one-value call does not compile and `Ask[T]` absorbs the error; a field's default wire name is its Go name as written (`name=` overrides); the tag is a Go string literal (escapes are doubled in source); generics are allowed, a type alias counts as the answer type and a defined type does not; an embedded struct holding a tagged field is refused; repeated score levels are refused on the typed path only; there is no `-` key; pointers are followed to a depth of 8. (deviation "typed wire names"; deviation "typed tag rules") | `TestPreparedForTicketParity`, `TestParseTag`, `TestPreparedForRejections`, `TestPreparedForTypeIdentity`, `FuzzTagGrammar` | G7 (7): R94 KEPT |
| R96, D-W4.1b | 2026-09-26 01:06Z | §5 (the tag grammar) | A `kind=choice` tag without `options=` is refused (the builder still accepts `Choice{}` and sends empty criteria, as Python does); any field whose type holds a tagged answer at any depth is refused, so typed sets cannot nest; a malformed or repeated `typesafe` key is refused. | `TestPreparedForRejections`, `TestLookupTag`, `TestPreparedForMalformedTag` | — |
| D-W4.2, R99, R99-rev | 2026-09-26 01:41Z | §5; §8 AC-F6, AC-F12, AC-P3; §4 (answer sizes) | `DecodeAs[T]` and `Ask[T]`. A typed failure names Python's lifted path (`tone.choice`, not `answers.tone.choice`); the typed decode refuses an undeclared probability label and a level beyond the levels and requires `usage`, stricter than Python; an answer that is not present marshals as `null`; an answer of an unknown type is dropped before typing, as pydantic's subclass form drops it, so a required field of that name is reported absent; `DecodeAs` refuses a `T` that was never prepared, naming `PreparedFor`; `Ask` returns `(T, error)`, and the request id, usage and raw body come from the two-step form. `NoulAnswer` is 16 B, `ChoiceAnswer` 56 B and `ScoreAnswer` 72 B (§4's "same size" does not hold). (deviation "stricter typed checks"; deviation "`Ask` returns the answers only") | `TestAskValidationFieldPaths`, `TestTypedErrorWrapsDecodeError`, `TestDecodeAsAgreesWithAnswers`, `TestDecodeAsOptional`, `TestAnswerPresent` | G7 (6): R99 (a) REVERSED, (b) KEPT |
| R111, R117 | 2026-09-26 06:27Z | process | Every commit builds, vets and passes its tests alone on the base it lands on; a wave branch may be rewritten with force-with-lease before landing; `main` is never force-pushed. | process | R117: CONFIRMED |
| K38 | 2026-09-26 06:17Z | §12 (the allocation step) | A `!race` pin that CI never runs is not evidence: the allocation-budget step fails when a test of a `!race` root file, or an `internal/codec` test that skips under `-race`, is missing from its list. | CI step "go test without -race (allocation budgets)" | — |
| K39 | 2026-09-26 06:27Z | the proxy tests | A test that expected a closed port to stay free now holds the refused address for its whole run. | `TestProxyRefusals`, `TestRefusedAddr` | — |
| R112, R116 | 2026-09-26 06:53Z | §6.6 ("no `unsafe` except one bridge"); NF6; §8 AC-P3 | `DecodeAs` writes each answer at its field's offset through `unsafe`, in exactly one file of the root package, checked against reflect's layout once per type and bounded on every write (G8-a Q3); it allocates nothing. The section "The typed store" below tells the story. (deviation "typed decode by field offsets") | `TestSeamOneUnsafeFile`, `TestStoreWritesTyped`, `TestStoreFieldKinds`, `TestStoreRefusesOutsideField`, `TestPlanLayoutRefusesCorruptPlans`, `TestAllocTypedDecode` | R116: R112 REVERSED |
| R113 | 2026-09-26 07:13Z | process (§7 landing order) | A wave that changes no production file may land before its phase gate closes; a production wave lands only after it. | process | — |
| K40 | 2026-09-26 07:35Z | §6.6 | No raw-pointer write in the root package by any route: outside the typed store, no package the root imports (other than `internal/codec` and the naive comparator) imports `unsafe` or uses `UnsafePointer`, `NewAt`, `SliceData`, `StringData` or a `Pointer()` result as a pointer; `internal/codec`'s `unsafe` is bounded to `NoCopyString`. | `TestSeamRootRawPointers`, `TestSeamRawPointerDetector`, `TestSeamCodecUnsafeIsNoCopyString` | — |

## Phase 5: performance

Gate: VERIFY P5 PASS at f73ab2b (V68) and CRITIC P5 APPROVE WITH
CONDITIONS (V69), 2026-09-26; the owner's Phase 5 batch closed the
conditions (G8-a, G8-b).

| Ruling | Date (UTC) | Plan text amended | As built | Pinned by | Owner |
| --- | --- | --- | --- | --- | --- |
| R98, R101, G5, G5b | 2026-09-26 01:17Z | §7 W5.1; §8 AC-P6 (time clause) | The benchmarks live in `internal/benchmark`, except six that time the root package's unexported steps; `BenchmarkLoopback` runs in CodSpeed but decides nothing; AC-P6's time clause reads the q3 rows (q20 is recorded). W5.1, benchmarks only, landed before W3.4 so that the freeze could measure the time clause. | `docs/perf/benchmarks.md`; the frozen AC-P6 time row | G5: move the benchmarks; G5b: six stay in root |
| K35, R105, R105-corr | 2026-09-26 04:57Z | §7 W5.1 ("CodSpeed discovers all") | CodSpeed's raw samples filled the runner's `/tmp` quota and 11 rows were lost; `bench.yaml` points `TMPDIR` at the runner's temp directory and fails on a "failed to write raw results" line or when the uploaded results differ from the `-list` expansion (125 rows). The fix ran green in run 36220344551, the guard failed a run built to lose rows (36220346065), and `main`'s run at aaa9698 (36221839206) was the first complete one. | `bench.yaml` guard; ledger W5.1-22..26 | — |
| K36, R108 | 2026-09-26 05:55Z | §8 AC-P7 (the statistic) | AC-P7 reads the mean, total time over rounds, which is `go test`'s ns/op: not CodSpeed's displayed minimum, nor the median. The section "Which number AC-P7 reads" below explains why. | `bench.yaml` AC-P7 gate step | R108: the mean |
| R109, R115, R109b, R109c, R109c-corr, R109c-corr-2 | 2026-09-26 06:12Z | K7 | Report bookkeeping, deciding nothing since G8-a: `main` runs are grouped by CPU model and AVX-512 exposure, and a group's segment starts at a landing that changes a call-path `.go` file and moves `BenchmarkCall/sdk`'s mean by 5 % or more, by K7's formula (larger − smaller) / smaller between the group's last run before and first run after. The flags digest the gate step prints names a set of flags, not a host group: `f1915a4aa377` came with an EPYC 7763 and with an EPYC 9V74 (verify-p6 OBS-2). | `docs/perf/codspeed.md`; ledger W5.4 rows | R115: per model RATIFIED; G8-a: not a gate |
| K37 | 2026-09-26 06:12Z | K7 | CodSpeed's own "Performance Analysis" check turns red on noise (the cold fan-out row, rotating hosts); nothing requires it, and its remedies are CodSpeed settings on the owner's side. | — | owner-side; G11 (11): ignore the host-side failure |
| R110 | 2026-09-26 06:16Z | §8 AC-P1, AC-P5, AC-P8 | AC-P1's arm64 exception narrowed to the 6 MiB `*struct` state (the flat map is asserted); AC-P5 gained cases (vi) and (vii) with bounds of 65 901 B and 69 632 B, each derivation written in its frozen row; AC-P8's per-member clause is pinned in `internal/codec`. | `TestAllocEncode`, `TestMemStatsCap`, `TestLazyPassAllocations`, `TestLinearityFlood` | — |
| R118 | 2026-09-26 08:34Z | process (commit trailers; refines R6) | A commit carries the attribution lines its author's harness gives at commit time; after 2026-09-26 08:30Z a lane's `Claude-Session:` line is optional. | process | — |
| K41, K41-corr | 2026-09-26 09:58Z | §3.3 (sonic facts) | sonic's `ast.Preorder` takes an unterminated string as complete when the input ends at a multiple of 32 bytes, on arm64 and amd64 alike. The SDK never hands sonic such an input: the one-scan cut declines it and the whole-body path refuses it. An internal note; no upstream report. | `TestK41ScannerBoundary`, `TestOneScanMatchesWholeScan` | G8-a Q5: no report |
| D-W5.3-revise | 2026-09-26 12:57Z | §5 (comparability) | `RetryPolicy` cannot be compared with `==` (a zero-size `func` field); whether an exported type is comparable is public API and the API golden marks it per type. The frozen AC-P1 row's recorded values for `*struct` and map states became ranges with their cause (sonic's pool drops them); its pins did not move. | `TestRetryPolicyRules`, `TestPublicAPISurface` | — |
| G8-a, G8-b | 2026-09-26 17:08Z | §8 AC-P7, K7; §6.6; R54; §5 | `bench.yaml` fails when `BenchmarkCall/sdk`'s mean is not below `BenchmarkCall/naive`'s in the same run (a ratio of 1.0 or more), and K7's switch to blocking is withdrawn; AC-P7's "PR run" is the wave's dispatch at its head plus `main`'s first run after landing; the typed store keeps one implementation, bounded per write; the state's UTF-8 check is sonic's validator on amd64 and `utf8.Valid` on arm64 (a 6 MiB CJK state's check fell from 4.92 to 0.52 ms on amd64, 1.17 times sonic's own encode of it against R54's target of at most once; arm64 is unchanged at 8.3 times); `Answers` cannot be compared with `==`; no upstream reports go out. All landed with W6-fixes at 1ed4c1d. | `bench.yaml` gate step; `TestStoreRefusesOutsideField`, `TestPlanLayoutRefusesCorruptPlans`, `TestValidUTF8Parity`, `FuzzValidUTF8`, `TestAnswersIncomparable` | G8-a Q1 (c), Q2 (a), Q3 (a), Q5 none; G8-b Q1b 1.0, Q4 (b), `Answers` incomparable, W6.4 GO |

## Phase 6: hardening

Gate: two passes. VERIFY P6 and CRITIC P6 report INTERIM at 1ed4c1d and
FINAL at the Phase 6 head; the release checklist cites the FINAL verdicts.

| Ruling | Date (UTC) | Plan text amended | As built | Pinned by | Owner |
| --- | --- | --- | --- | --- | --- |
| R119 | 2026-09-26 09:06Z | §12 (Codecov blocking from W6.3) | Without pull requests nothing can block on a Codecov status, so `codecov/project` = success at the landing SHA, with its percentage, is landing evidence from W6.3 on; a branch protection rule is the owner's to add. | each landing's record in the rulings ledger | — |
| R120 | 2026-09-26 09:06Z | §7 W6.3 (the API goldens) | The API golden keeps constant values as well as names and types, so a changed default is an API change; the release bump rewrites it with `-update` in the same commit. | `TestPublicAPISurface`, `TestClientOptionsSurface` | — |
| D-W6.3-done | 2026-09-26 09:06Z | §12 (the AC-Q2 checker in the lint job) | The uncovered-lines check runs in the ubuntu test job, the only job with a coverage profile; every uncovered block has a row with its class (Defensive, Gap, Race). | CI step "Every uncovered block has a reason (AC-Q2)" | — |
| K16 | 2026-09-26 18:54Z | K16; §6.3 | A proxy that refuses the CONNECT (502) is a proxy `*ConnectionError` (`Proxy()` true) on the SDK's own transport, through `OnProxyConnectResponse`, and its status line is in the error with the proxy URL's password, and the Basic token sent for it, as `***` (V72 MIN-4); through a caller's `WithHTTPTransport` it stays `Proxy()` false, with net/http's status text as the proxy wrote it, as the standard library reports it. | `TestProxy`, `TestTransportErrorsBecomeConnectionOrTimeout`, `TestTransportDebugRecordsHoldNoCredential`, `TestRefusedConnectScrubsProxyCredential` | — |
| D-W6.4-status-1, D-W6.4-live | 2026-09-26 17:30Z | §7 W6.4 (`testdata/live-*.json`); §8 AC-F11; K22 | The live recordings are `testdata/live/<scenario>.json`, outside the fixture glob whose every body carries an allocation pin; the live tests fail unless `TYPESAFE_LIVE_TESTS=1` and a key are set; AC-F11's "unauthenticated → 403" is a request with no credential, and a wrong key gets 401, both `IsAuthentication()`; the API advertises 100 concurrent streams, not 1024, and gzips every 2xx, so a body arrives with no declared length. | `TestLiveUnauthenticated`, `TestLiveTransportFacts`, `TestLiveEnvGuard`, `TestRecordedBodiesHoldNoCredentials`, `TestLiveBodiesOneScan`; ledger W6.4-01..08 | G8-b: live GO; G11 (7): AC-F11 wording |
| V72 | 2026-09-26 18:51Z | §7 W6.2 (security review) | 0 BLOCKER, 1 MAJOR, 6 MINOR, each routed. MAJ-1: a body of tiny answers of an unknown type left one scratch entry per answer in the pooled decoder, about 233 MiB live after one 15 MiB body for as long as ordinary decodes reused it; a decoder now drops its scratch when it goes back to the pool past 4 MiB (`DecoderCeiling`), a size every pinned decode stays under, so no allocation pin moved; the flood call's own peak is unchanged (G11 (2): no cap on answers). MIN-1: sonic v1.15.4 can read up to 4 bytes past an input shorter than 4 bytes; a body of 1 to 3 bytes is decoded from a zeroed 8-byte copy, and the one-scan cut refuses a cut shorter than 4 bytes, so the one scan decides alone in 218 of `TestOneScanMatchesWholeScan`'s 11 034 decodes (236 before); every entry point that hands sonic a server's or a caller's bytes, the request's UTF-8 check included, runs against guard pages with no fault on darwin/arm64 or linux/amd64. MIN-2: `trailing`'s refusal of a value that ends past the body has its test. MIN-3: the flat-file route is closed (a raw-pointer write in any codec file but the no-copy string fails the seam tests); the sub-package route closes with W6.5. MIN-4: a proxy's echo of its own credential is scrubbed (row D-W6-secfix-m2). MIN-5: `DecodeAs` redacts as `Ask` does from W6.6 (G11 (3)). MIN-6: `ResponseMeta.Header()` returns the header as received, which a W7 godoc commit after W6.5 says. As built at e34a1c4. | `TestMemStatsFlood`, `TestDecoderPoolRetention`, `TestDecoderScratchCeiling`, `TestGuardPage`, `TestPadShort`, `TestCutPointMinimumLength`, `TestTrailingEndPastInput` | G11 (2), (3), (6) |
| D-W6-secfix-m2, D-W6-secfix-407msg-corr, D-W6-secfix-header-scope, D-W6-secfix-header-scope-2, D-W6-secfix-commit-11 | 2026-09-26 22:57Z | §6.3 (the credential scrub, R68); K16 | A proxy's answer can repeat the credential the SDK sent it (V72 MIN-4). On the SDK's own transport the proxies its proxy func returns are recorded where net/http asks the func, never by asking it again: the 16 most recent distinct ones, per transport. Every transport error of the client, in its text, its stand-in, the INFO "request failed" record and the DEBUG "h2: gate error" record, shows each recorded proxy's password (as it is, as the URL escapes it, and each of its words) and the Basic token sent for it as `***`, at any length; a short password over-redacts the error's own text, as `ConnectionError`'s godoc says. The three places that print a response header (the request id, `APIError.Header`, DEBUG records) look for the whole credentials only (the token, the password as it is and escaped), from 8 bytes, as for the API key, and only in the answer to a plain-http request that went through a proxy: over HTTPS a proxy only tunnels and cannot write them. Before those two rulings a word of the password, the "2" of "open 2 sesame", turned the API's `Retry-After: 2` into `***` (the W6.2 delta at 1d40804). `APIError.Message` shows an echoed proxy password as it shows an echoed API key (R103-rev). A transport given with `WithHTTPTransport` or `WithRoundTripper` keeps its proxy, whose credentials the SDK does not know and does not scrub (G11 (4)). As built at e34a1c4. | `TestRefusedConnectScrubsProxyCredential`, `TestProxyEchoHoldsNoCredential`, `TestProxyEchoConcurrentColdClient`, `TestProxyCredentialSetEvictsTheOldest`, `TestProxyCredentialParityWithAPIKey`, `TestProxyFuncAskedOncePerAttempt`, `TestProxyHeaderScanScope`, `TestProxyRetryAfterSurvivesOverHTTPS`, `TestProxyKeepAliveSecondRequestHidden` | G11 (4); whether `APIError.Message` should redact known credentials is in the owner's Phase 6 batch, part 2 |
| D-W6-fixes-flake, D-flake-2 | 2026-09-26 20:39Z | K19 (the gate's statistics) | `TestSettleHold` could read `Stats().HoldExpiries` before the count moved, because the token was returned before the expiry was counted; lane w6-flake counts first. As built at 1ed4c1d: not yet landed. | `TestSettleHold` | — |
| G9, D-W6.5-design, G10 | 2026-09-26 17:17Z | §4 (layout); §8 AC-Q2; plan v3 ("wrapper structs, not aliases") | The data-path stages move to `internal/engine` and the root allocation tests to `internal/alloctest`; every public type stays in the root package with its methods and docs, and `Client`, `Prepared` and `SystemOneResponse` become defined types over engine state; coverage counts any test of the module (`-coverpkg=./...`). As built at 1ed4c1d: not yet landed (W6.5). | — | G9: all 13 files; G10: design D1, `-coverpkg` |
| D-W6.5-retry, D-W6.5-retry-2, D-W6.5-retry-3 | 2026-09-26 21:55Z | G10 (`Config.Retry any`) | The engine's configuration is generic in the retry policy's type, `type Client engine.Client[RetryPolicy]` in the root package: no per-call cost and no allocation at `NewClient`, against +1.32 ns per call and one allocation for `any`; `%#v` of a client then names its internal configuration type (`*engine.ConfigRef[…RetryPolicy]`), never the key. As built at 1ed4c1d: not yet landed (W6.5). | — | G11 (8): the generic form |
| G11 | 2026-09-26 22:27Z | the Phase 6 owner batch | (1) gzip stays the default and a new client option turns it off; (2) no cap on the number of answers (parity; the peak memory documented); (3) `DecodeAs` redacts header-derived values like the client does (R114 extended); (4) a caller transport's proxy echoing a password is documented, not scrubbed; (5) the dial phase of a blocking trace hook gets a bound before v0.1.0; (6) no sonic report; (7) the `ExtraBody` godoc warns of the API's 400 on unknown top-level members, and AC-F11 reads "403 = no credential, 401 = wrong key"; (8) `Config.Retry` takes the generic form; (9) W6.6 and W7 start before the gate closes; (10) the cut path's shapes go into the internal sonic note; (11) CodSpeed's host-side check failure is ignored, not a gate. As built at 1ed4c1d: (4), (6), (9) and (11) hold; the rest land with W6.5 and W6.6. | — | G11 (1)–(11) |
| D-W6.6-spawn | 2026-09-26 22:27Z | process (§7 Phase 6) | A wave W6.6 carries the owner's Phase 6 decisions (the dial-phase bound, the gzip option, the `DecodeAs` redaction, four documentation items). | process | G11 (9) |

## Phase 7: release readiness

The plan tags `v0.1.0` after the exit evidence, CI, the CodSpeed baseline,
the Codecov gate, a clean `govulncheck`, the README's deviations against
Appendix B, the newest dependencies and an arm64 run of the allocation
tests and B1–B6. W7 runs that list as a checklist; the tag itself is the
owner's act.

| Ruling | Date (UTC) | Plan text amended | As built | Pinned by | Owner |
| --- | --- | --- | --- | --- | --- |
| D-W7-spawn | 2026-09-26 22:27Z | §7 Phase 7 (after all exit evidence) | W7 started before the Phase 6 gate closed (the gate reports twice, INTERIM and FINAL); the checklist line for the exit evidence waits for the FINAL verdicts. | the release checklist | G11 (9) |
| K6 | 2026-09-24 (plan) | K6 (budget re-checked on live recordings before v0.1.0) | Re-checked by W7 on the owner-run recordings under `testdata/live/` (the plan's `testdata/live-*.json`, D-W6.4-status-1). | the release checklist; ledger `## W7` | — |
| K17 | 2026-09-24 (plan) | K17 (the Go 1.28 support window) | `docs/support.md` states the window (the Go releases the newest sonic tag supports, today Go 1.27.x on amd64 and arm64), the compile-time refusal and its identifier, and the Go 1.28 bump procedure. (deviation "supported platforms") | `TestSeamD1IdentifierSites`; CI step "D1 refusal off the support matrix" | — |
| K18 | 2026-09-24 (plan) | K18 (arm64 has no hosted gate) | Every allocation test and B1–B6 run on the arm64 host under the bench lock, recorded in the ledger's `## W7`. | ledger `## W7` | — |
| D-W7-q1 | 2026-09-26 23:33Z | Appendix B (a new row) | The check that decides whether a raw score question's criteria are empty reads a JSON number's digits: `RawJSON("1e-400")` is not empty and is sent, where Python underflows it to `0.0` and refuses the question (V63). Documented for v0.1.0, not changed. (deviation "score criteria that underflow to zero") | `TestRawScoreCriteriaThatAreNotEmpty` | information line in the Phase 6 owner batch, part 2; a fix is a v0.1.x option |

## The typed store: from reflection to field offsets (S-D2, R112, R116)

The plan's §6.6 allowed one use of `unsafe` in the module, a string bridge
in `internal/codec`, and decided typed answers (D3) with reflection, cached
per type. Spike S-D2 (W4.3) measured that path against a variant writing
each answer at its field's offset through `unsafe`. On `result.json` the
like-for-like reading, a replica of the reflection path against the
offsets, was 1.92× on (M) and 2.39× on (L); `DecodeAs` itself against the
offsets, 2.10× and 2.55×. Most of the gap was not reflection: the
reflection path's one allocation per call was `T` itself escaping to the
heap. The lead kept reflection (R112): the like-for-like reading was under
the plan's 2× bar on (M), and the stake was 70–113 ns, 1.4–1.8 % of an
in-process call. critic-p4 then showed that the seam test could not stop a
raw-pointer write that imports nothing (K40: `reflect.Value.UnsafePointer`
compiles without `unsafe`), and that keeping reflection was a choice, not a
consequence of the plan: on (L), the host the time clause is judged on,
both readings exceed 2×.

Asked at the Phase 4 gate with both readings, the owner reversed R112
(R116): field offsets through `unsafe`, in exactly one file of the root
package, one implementation for both architectures. W5.3 first extended
the seam test, so that every other root file and every package the root
imports is free of `unsafe` and of the import-free routes, then built the
store: `DecodeAs` went from one allocation (144 B) to none, and on
`result.json` from 184.3 to 98.3 ns on (L) (−46.6 %) and from 136.2 to
82.7 ns on (M) (−39.3 %); the frozen AC-P3 row moved with its pin (0
allocations, against the `Answers()` decode's 4). critic-p5 then showed
that a wrong offset would not stay inside `T`: mutants wrote response bytes
into pointer slots and past the struct into `DecodeAs`'s own frame. The
owner kept one implementation and asked for a bound on every write
(G8-a Q3); W6-fixes added it together with a check, once per type, of the
plan's field layout against reflect's. Neither adds an allocation. One
residual is ruled: an offset changed between the bound's check and the
write itself is caught only by the race detector's `checkptr`.

`PreparedFor`'s first call for a type builds its plan and caches it:
8 allocations (800 B) for one field, 24 (4 720 B) for the plan's `Ticket`,
63 (20 328 B) for ten fields and 114 (41 704 B) for twenty; every later
call allocates nothing ([`perf/typed.md`](perf/typed.md)).

NF6 as built: the module uses `unsafe` in `internal/codec`'s `NoCopyString`
(the seam tests bound `internal/codec` to it) and in the root package's
typed store, and nowhere else.

## Which number AC-P7 reads (K36, R108)

AC-P7 asks CodSpeed to report `call/sdk` faster than `call/naive`.
CodSpeed's walltime instrument times every iteration of a Go benchmark and
shows the minimum as the benchmark's value. Over the first four runs of
record (aaa9698 to f048059) the SDK's call read 12–15 % slower than the
naive client's by the minimum, 5–8 % slower by the median, and 2–6 % faster
by the mean (K36). Per call the naive client's own path was about 0.6 µs
shorter; the SDK won only once garbage collection was counted, since the
naive client allocates about 54 times per call against the SDK's 22, and
the collector's time lands in the tail of the per-iteration samples, which
the minimum and the median leave out. The mean is total time over rounds,
the number `go test` prints as ns/op: the basis of AC-P6's time clause and
of the comparator decision G3. The owner chose the mean (R108).

W5.3 then narrowed the gap on the other statistics: the decode scans a body
once when a cut point can be proved (the body's last byte closes the root,
the cut lies outside every string and no value runs into it), and its
allocation cuts took N from 14 to 12. On like-for-like hosts the SDK became
faster by the median as well; CodSpeed's displayed minimum stayed 0.8–3.7 %
slower. Since G8-a the mean is enforced: `bench.yaml` fails when the same
run's mean ratio is 1.0 or more, and the "PR run" of the plan is the wave's
dispatch at its head together with `main`'s first run after the landing
(G8-a Q2, no pull requests under R2). The runs of record:

| Run | Commit | Host group | sdk / naive min | median | mean |
| --- | --- | --- | ---: | ---: | ---: |
| `bench.yaml` 36245339675, W5.4's dispatch | de27718 | EPYC 9V74, AVX-512 | 1.037 | 0.942 | 0.835 |
| `bench.yaml` 36246139928, W5.4's final dispatch | fdea888 | EPYC 9V45, AVX-512 | 1.020 | 0.928 | 0.843 |
| `bench.yaml` 36249189420, `main` after W5.4 landed | f73ab2b | EPYC 7763 | 0.998 | 0.924 | 0.787 |
| `bench.yaml` 36271714594, `main`'s first gated run | 1ed4c1d | EPYC 9V74, no AVX-512 | 1.018 | 0.925 | 0.797 |

## Accepted wave deviations

Each wave reported the ways it departed from its charter; the lead accepted
them under these ids. The items below are as the rulings ledger records
them; where it records only a count, the itemised list is in the wave's
report and review.

| Ruling | Items |
| --- | --- |
| D-W0.2 | The refusal steps drop setup-go's problem matcher; `internal/wire` imports the standard library only; `FuzzValidString`; per-step gotip timeouts; `levelHint` and the legend order left to later waves. |
| D-P0P | Codec allocation tests skip at run time under `-race`; the sonic fallback rule keys on the compat files that import encoding/json; the gotip probe lists the whole sonic module; `Body` is a value handle. |
| D-W1.2, D-W1.2b | D-W1.2-1..16: no `ExtraBody` type before W2.3; a fourth return of `requestReaders`; the model's UTF-8 check as a `*ConfigError`; `Content` as a state (R52); the value-start check for `RawJSON` extras; the UTF-8 pass over extra values; the deviation test split; a codec-level NF1 test; `TestAllocBodyKinds` in CI; labelled echo lines in the PM5 probe; the seam's non-vacuity guard; root tests importing testsupport; a standalone PM4 helper; C3 at body level; W1.2-01 measured on a working tree; `RawJSON`'s godoc. D-W1.2-5 map order (R55), D-W1.2-6 `[]byte` (R56), D-W1.2-7 nested whitespace (closed by R60). |
| D-W2.1, D-W2.1b | D-W2.1-1..13: `ClientOption` (R64); the SDK header (R63); two header templates, a caller `Content-Type` dropped; `Proxy-Connection` framing; a base URL with a query or fragment refused; a blank model refused; redaction of values that contain the key; the 1 GiB cap; `WithUserAgentProduct` token/token of at most 64 bytes; `WithHeader` checked without echo; the explicit-empty key's message; F9's per-call half moved to W2.3; R62's amd64 run not applicable. D-W2.1-14..16: the pointer-backed redacted header view; `WithHeader` replaces whatever the case; a header name holding the key refused. |
| R67 | D-W2.2-1..12: `SetMaxConcurrentStreams` on the loopback server; `DialError.Unwrap() []error` carrying `ErrNotNegotiated`; `Wrap` refuses transport-owned `Config` fields; an empty `HTTP2Config` treated as unset; a non-ASCII host refused for http too; K21b's type half in Part B; the opt-in `TestRecordFanOut`; extra tests; govulncheck through `go run`; a leader failing before `GotConn` hands over; `HTTPAuto` serialises new HTTP/1.1 dials through the token; the R65 tree check. |
| D-W2.2b | D-W2.2b-1..7: `newClientClosedError()` unwraps to `ErrClientClosed` alone; `WithConnectTimeout` with `WithRoundTripper` refused; `TimeoutError.Timeout` holds the attempt's timeout for a dial or handshake timeout; the mapping text "Connection error: " and the inner text; the framing drop already in W2.1; gate scripts take a worktree override; per-line `gosec` exemptions on the scrub fixtures. (3) amended by R81 (3). |
| R79 | D-W2.3-1..12, accepted at the W2.3 fix pass; the rulings ledger records the count, the wave's report the items. |
| D-W2.4 | D-W2.4-1..6: the marshal path through `internal/wire`, not sonic; `MarshalJSON` on `Answer`, `Usage` and `ModelCard`; the extra tests; root tests reach encoding/json through testsupport; the Python probe run with the upstream checkout's virtualenv; one fired `rebase.autoStash`. |
| R81 | D-W2.5: the ten items of R81; (1)–(4) are in its row, (5)–(7), (9) and (10) were accepted as the wave stated them, (8) moved the retry sleep's cancellation to W3. |
| D-W3.1 | D-W3.1-1..7: a goroutine read; the godoc paragraph on value semantics kept (Appendix B's own row); a second test function; the view-table refactor; four reasoned `errorlint` exemptions; `--allow-serial-runners` once; prefixed helper names. |
| D-W3.2 | D-W3.2-1..10, among them: the GOAWAY cause asserted on every OS after K33's fix; the environment test client also one attempt; `TestParseRetryAfterTable` repeating `TestRetryAfter`'s rows through the client; `Statuses` a sorted slice, not §6.4's bitset; `Retry-After` read from validation and too-large errors too; ledger W3.2-02. |
| D-W3.3 | D-W3.3-1..14: R93's same-line edits in other waves' files; the redactor as a config method; the h2gate unit test in its own file; the L4/L5 citations; `Retry(NoRetry())` on failing calls; the two-way merge-tree proof; one rewrite of the branch with lease; `--allow-serial-runners`; R95 in its own commit; rows re-pinned after the rebase; `TestClientLogsNoCredential` folded into L1; `TestAllocLoggedCall` (then outside CI's list, K38); L2's three chain shapes; a test-local refusing proxy for K16. |
| D-W3.4 | D-W3.4-1..8: the row of record taken at load 8 or less; `-count=10` with benchstat; the q20 and per-option probe kept as a spike file, not a test; max and spread from a renderer over 20 runs, tests unchanged; `TestMemStatsCap` in the same commands; the pin messages; govulncheck through `go run`; temporary files in the lane's scratchpad. |
| D-W4.1 | D-W4.1-1..4: the typed plan is looked up by type, not by a method of `*Prepared`; the two-way merge-tree proof; a 14th rejection (no answer field) with `Prepare`'s sentence; repeated score levels refused on the typed path only. |
| D-W4.2 | D-W4.2-1..12: body paths (reversed by R99-rev); option and level checks stricter than Python; typed errors carry the response's `Meta`; P4's first case fails at `usage` first, with a variant that has `usage`; 14 fixtures; one allocation (0 since R116); the CI step's name; ledger rows per host; extra tests; the Python path probe as a spike; the interim branch for an option-less choice dropped after R96; lease force-pushes after W4.1's rebases. |

## Notes

**Known parity edges.** The check that decides whether a raw score
question's criteria are empty reads `1e-400` as a non-zero number, where
Python underflows it to `0.0`, which is falsy (V63). It ships as a
documented deviation (D-W7-q1, Phase 7 table); the owner may have it fixed
in v0.1.x.

**AC-P8 is a ratio of the decoder against itself.** Its time clause
compares the 10⁴ flood with the 10³ flood, so a slowdown that hits both
alike passes by design: a lazy pass made 40 times slower passed every root
and codec test under `-race` (critic-p5). A whole call made slower fails
the AC-P7 gate in `bench.yaml` (G8-a), but only once it loses its margin
over the naive client, a slowdown of about 15–32 % by the recorded ratios
(critic-p6); a decoder-only slowdown that `BenchmarkCall` does not exercise
(its body has no structured legend, so the lazy pass never runs) has no
gate. `TestLinearityFloodTime`'s godoc states how it handles noise (the
minimum over five spans, the two floods' spans alternating) but it has no
load guard, the K29/K30 posture "none": on (M) it held up to a load of
128, failed twice in five runs under the tenfold overload of 2026-09-26
10:23Z, and once at a load of 22 on 16 cores, 1.4 times the cores (ratio
16.67 against the bound 15), inside a plain `go test ./...` of
verify-p6's, where the amd64 host passed the same commit five times out of
five at 9.17–9.19. Hosted runners are not loaded that way. A gate run on a
loaded (M) that fails in this test alone re-runs it alone with the load
recorded (D-R111-load); a failure alone below a load of 12 is real.

**Known coverage gaps.** Every Gap row of
[`uncovered-lines.md`](uncovered-lines.md), 40 at 1ed4c1d, is an input the
public API can send and no test sends; they ship as known gaps.

**Measurement hosts.** (L), linux/amd64, is the host the amd64 clauses are
judged on (AC-P6's time clause, G3); (M), darwin/arm64, records (K18).
Three bursts of busy loops from a session outside the team loaded (M) on
2026-09-26 (10:23Z, 11:02Z, 12:06Z); W5.3 checked its rows against them
and re-took W5.3-39, -41, -57b and -62. The one-scan decode's share was
counted twice: 21 of the 41 fixtures (ledger W5.3-12) and 23 of the 41
cases of `TestOneScanTakesValidBodies` (review of W6-secfix), two sets of
41.

**Left to the owner.** A branch protection rule requiring
`codecov/project` (R119); CodSpeed's own settings for the noisy rows and
the 19 names G5's move left behind (K37); reinstalling the local
`govulncheck` built with an older toolchain; replacing the house rule
"`env -u GOEXPERIMENT` for measurements" with `GOEXPERIMENT=nosimd,noruntimesecret`
on a host whose Go env file sets experiments. No upstream report goes out
(G8-a Q5, G11 (6)): sonic's scanner boundary (K41), the read past the end
of short inputs found by W6.2, the Marshaler validation (K26), the
negative-zero spelling (K27), CodSpeed's per-iteration samples (K35) and
the Go 1.27.1 linker's panic when `RetryPolicy` loses its marker field
stay internal notes.

## Appendix B, row by row

The plan's Appendix B mapped the Python SDK's behaviour to the Go port's in
47 rows and marked the deviations in bold. The table takes them in the
plan's order: the row's Python side as the plan wrote it, whether the plan
marked it a deviation, the rows of [`deviations.md`](deviations.md) that
carry it now, and the rulings that changed it. A bold row names at least
one of the two. The deviation table's last column lists the upstream tests
each key replaces, or `—` when no upstream test reaches the behaviour.

| # | Appendix B row (Python SDK 0.7.1) | Bold | Deviation keys | Rulings |
| --- | --- | --- | --- | --- |
| 1 | Sync and async clients | yes | deviation "Sync and async clients → one `*Client`, `context.Context`" | — |
| 2 | httpx per-phase timeouts; `http_client.timeout` precedence | yes | deviation "one deadline per attempt"; deviation "a custom transport owns its timeouts" | — |
| 3 | `transport=`/`http_client=` mutually exclusive; supplied client closed with the SDK client | yes | deviation "one transport option, two kinds" | R67, R79 |
| 4 | httpx honours proxy env variables | yes | deviation "the ALPN check applies to the API hop" | R20, K16 |
| 5 | `.nouls/.choices/.scores` cached copies | no | deviation "`iter.Seq2` filters" | — |
| 6 | `response_model=` (questions passed separately; `Optional` fields default to `None`) | no | deviation "typed answers by struct tags" | R94, R99 |
| 7 | Picklable/copyable errors and responses | no | deviation "errors are values"; deviation "no process pools"; deviation "responses are values" | D-W3.1 |
| 8 | `request_id` raises when absent | no | — | — |
| 9 | `raw_http_response` | no | — | — |
| 10 | Response constructible without HTTP response; `raw_http_response` raises | yes | deviation "empty `Meta()`" | — |
| 11 | Responses serialise to the payload and read back | no | — | R80 |
| 12 | Frozen models | no | deviation "unexported fields with getters" | — |
| 13 | `Usage` counts optional; negative accepted | yes | deviation "usage counts" | R70 |
| 14 | `RetryPolicy.exceptions` | yes | deviation "`RetryPolicy.exceptions` → dropped; `Predicate` kept" | R88 |
| 15 | `TYPESAFE_LOG_LEVEL` | yes | deviation "`TYPESAFE_LOG_LEVEL` not read" | — |
| 16 | DEBUG logs full bodies | yes | deviation "bodies at LevelTrace" | — |
| 17 | Unknown-answer WARN per answer | yes | deviation "unknown answers logged at most 8 times" | — |
| 18 | SDK/runtime headers | no | deviation "SDK headers" | R63 |
| 19 | NaN/Infinity written | yes | deviation "NaN and infinities refused" | R36 |
| 20 | No response size limit | yes | deviation "response size limit" | R26, R27 |
| 21 | Any nesting depth | yes | deviation "nesting depth" | R73 |
| 22 | ALPN chooses | yes | deviation "HTTP/2 only on https" | G2 |
| 23 | Server messages verbatim; a non-JSON plain-text body never cut (`pytest:test_errors.py:148`; a JSON body without a message member is cut at 200 + `…` by Python too, `:149`) | yes | deviation "plain-text body cut at 200" | R58 |
| 24 | `Retry-After` float ms | no | deviation "`Retry-After` precision" | R73 |
| 25 | Empty explicit default model sent | yes | deviation "configuration checked at build" | R63 |
| 26 | Base URL checked at first request | yes | deviation "configuration checked at build" | R63 |
| 27 | Caller framing headers sent | yes | deviation "caller headers" | R66 |
| 28 | Redaction by header name | no | deviation "stored headers redacted"; deviation "redaction by source" | R87, R107 |
| 29 | Transport error text verbatim | yes | deviation "cause via `errors.Unwrap` unless it printed a credential" | R81, R82 |
| 30 | Typed noul sends `null` outcomes / empty criteria | no | deviation "Typed noul sends `null` outcomes / empty criteria" | — |
| 31 | Duplicate question names keep the last | yes | deviation "duplicate question names refused" | R94 |
| 32 | Body that is not an object fails at `''` | no | deviation "root path" | — |
| 33 | Answer `type` pre-pass, then first error in wire/schema order | yes | deviation "legend value path" | R70 |
| 34 | Duplicate keys: last wins (top-level members, answer names, members, `type`) | no | — | R24 |
| 35 | Level keys via pydantic lax `int` (`" 1"`, `"1_0"`, `"1.0"`, `"-1"`, `"4294967296"` accepted) | yes | deviation "score level keys" | — |
| 36 | 2xx in retry statuses retries a non-validating body | yes | deviation "2xx in retry statuses retries a non-validating body → never; `Predicate` can opt in" | R88 |
| 37 | Raw dict questions | no | deviation "raw question field order" | R37, R38 |
| 38 | `str` subclasses / abstract containers / top-level `None` refused | no | deviation "`any` state" | R48, R49 |
| 39 | Unknown fields rejected on typed questions | no | deviation "not representable" | — |
| 40 | HTTP/2 transparent replays invisible | no | — | — |
| 41 | `tests/test_public_sync.py`: dev→public sync tooling (skipped outside the dev repository) | yes | deviation "dev→public sync tooling not ported" | — |
| 42 | `.github/scripts/release_notes.py` | yes | deviation "no release-notes script" | — |
| 43 | sybil doctests over README, `docs/*.md` and docstrings (live) | no | deviation "no sybil" | — |
| 44 | `__all__` and constructor-kwargs snapshots | no | — | R120 |
| 45 | `model_validate_json` rejects raw control characters (U+0000–U+001F) and lone surrogates inside strings | yes | deviation "lone surrogates" | R14 |
| 46 | `1e400` in a float member (`noul`, `probabilities`): `from_json` → ±inf, accepted (`py:_core/json.py:29-35`) | yes | deviation "non-finite numbers in a response" | R73 |
| 47 | pyrefly expectation fixtures (`tests/typing/`: negative expectations and three positive fixtures) | no | deviation "pyrefly fixtures"; deviation "typed tag rules" | R96 |

Rows 8, 9, 11, 34 and 40 are parity, as the plan wrote them. Row 33 is
parity for the order of failures since R70; its residual is the legend
value path. Row 45 splits: a raw control character is refused as Python
refuses it (R14), and a lone surrogate is the deviation.
