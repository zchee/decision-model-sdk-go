# Changelog

All notable changes to the adapter module
(`github.com/zchee/decision-model-sdk-go/adapter`) are recorded here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Opt-in live checks select OpenAI Chat Completions explicitly and validate
  Anthropic key-only and token-only requests by credential header names; a
  presence-only guard refuses both-exported credentials, including empty values.
- The implemented adapter API, three in-process provider implementations,
  replay tests and canonical [port test matrix](docs/port-test-matrix.md)
  replace the initial version-only module description. The module remains
  unreleased; the root SDK tag is not an adapter release.
- `TestUncoveredLinesListed` compares measured coverage with precise reasons
  in [uncovered blocks](docs/uncovered-lines.md), rejecting missing, blank,
  duplicate and stale entries. Duplicate profile blocks use the largest
  hit count. Excluded test-support paths remain excluded.
- `-matrix-final` rejects planned rows and runs the actual replay consumers
  of the 25 committed cassettes. The cassette matrix rows are now ported;
  fixture bytes are unchanged. Two exact workflow checks enforce final
  matrix and coverage accounting.
- Provider nil-transport helper checks, explicit OpenAI Chat retry counts,
  the Responses non-answer fixture, and the caller-owned client's logger
  are exercised. StatusError tests distinguish nil from nonnil empty bodies.

### Fixed

The retained-diagnostic hardening comprises these seven landed commits,
listed by their actual subjects:

- `d6f20f0` — `adapter: keep formatting from exposing retained provider fields`.
- `d1dd3f7` — `adapter: keep reflected credentials out of retained diagnostics`.
- `bb186d9` — `adapter: exclude credential query names from recorded URLs`.
- `ef80170` — `adapter: distinguish credential forms in redaction regressions`.
- `f87b819` — `llm: bound status-error JSON and structured log output`.
- `29131ab` — `adapter: keep provider credentials out of reflective formatting`.
- `0b9e569` — `adapter: prevent pointer fallback from printing sensitive state`.

Their final provider representation is **two pointer levels**, not merely
one: `Provider.state` → `requestState.credentials` → `requestCredentials`.
OpenAI/Anthropic retain endpoint/header state in the inner struct; Gemini
retains key/endpoint. Unsupported fmt verbs can restart reflective traversal
at depth zero, so the intermediate one-pointer layout was insufficient.
The verified scope is pinned Go fmt behavior, not zeroization or arbitrary
unsafe-reflection secrecy; model identifiers remain intentionally printable.

StatusError value-receiver JSON and slog representations use its bounded
Error string, not exported headers or the body tail. Accepted private-value
wrapper and invalid value `%p`/`%w` limitations remain, with pointer/explicit
Error-text controls. Static vet is not a confidentiality guarantee for
dynamic formats or boxed `any` values.

Known credential raw/JSON-content/upper- and lower-percent QueryEscape and
PathEscape forms are replaced with `***` before body retention, and in the
diagnostic request copy under Trace. Outbound request bytes are unchanged.
This is exact known-form replacement, not recursive or universal content
sanitization.

Recorder query filtering removes all occurrences of the exact 17 aliases
listed in the README, case-insensitively after one decode, including bare,
empty and duplicate pairs. Undecodable names are dropped; benign raw spelling
and order are preserved. Headers retain content-type only. Recorder bodies
remain **pre-REST-scrub** data and require inspection/keyscan before addition.

Subsequent separately signed changes:

- `8186f8c` — protected-child refusal regressions assert absent children,
  unchanged sibling bytes and no intermediate protected creation; the
  guard-disabled mutant fails those assertions.
- `7c5d4f6` — numeric replay diagnostics keep the exact differing member path,
  using the matcher's exact number comparison rather than rounded floats.
- `45e0492` — cassette Transport/Requests document immutable nested borrowing
  and test the intentional aliases, without unnecessary deep copies.
- `70d8b3b` — provider retry/logger/nil test carries above. The negative helper
  uses a subprocess-free fatal probe; it cannot fall through to a provider Do.
- `4ffac36` — matrix/coverage/workflow finalization above; the live-tag test-list
  subprocess has an explicit context deadline, race instrumentation and the
  fifteen provider/live environment removals.
- `66fb807` — conditional uncovered ranges are accepted only as exact `0-1`
  for the two documented dynamic logging guards; non-guard and wider ranges
  are refused. Generic acceptance and coupled omission tests pin that rule.
- Documentation reconciles the implemented API, current limits, retained
  advisory applicability and deferred work; it is not live or release approval.

### Changed

- New cassette recordings replace provider-assigned body identifiers at known
  JSON request/response paths with "x"; wire bodies and replay remain unchanged.

- The adapter no longer requires `github.com/go-json-experiment/json` and does
  not build with `GOEXPERIMENT=nojsonv2`; this is a deliberate standard-library
  JSON v2 boundary, not a passing alternate-codec configuration.
- [Deviations](docs/deviations.md) remain the canonical intentional-difference
  table; body redaction qualifies its raw-wire description.

### Known limitations and deferred work

- The scoped audit at `0b9e5690` retained GO-2026-5781 (`rsc.io/pdf` through
  `golang.org/x/arch`) and GO-2026-5932 (the OpenPGP family of
  `golang.org/x/crypto` through the root's `golang.org/x/net`), with **no fixed
  version reported**. Affected packages were absent from 14 usable import
  closures; two adapter nojsonv2 lists failed and were not counted. Graph
  membership is not imported applicability. The latest available independent
  dependency audit remeasurement at documentation preparation was `66fb8078`, filed
  2026-10-07 21:29:55 JST, with 28 fresh queries; parent import closures
  remained applicable because imports/modules were unchanged.
  This branch's final head must be remeasured by the independent dependency
  audit; this note
  does not claim a whole-program or future-import guarantee.
- The four-dimensional test nesting refactor remains explicitly deferred;
  improved behavioral checks did not perform that structural cleanup.
- Existing adapter CI fuzz/list commands predate the hardening work and lack
  `-race`, an explicit `-timeout` and bounded `-fuzzminimizetime`. Local release
  gates and the long campaign at `0b9e5690` are the recorded fuzz evidence:
  ten minutes per target with `-race`, `-parallel=1` and
  `-fuzzminimizetime=1x`. Aligning CI commands is deferred; no workflow edit
  accompanies this documentation change.
- New recordings require independent inspection, keyscan and maintainer approval.
  Offline replay is not live-service validation. Exported Report/Trace data,
  caller state/prompts and accepted StatusError formatting limitations remain
  sensitive retained content, not universally scrubbed output.
