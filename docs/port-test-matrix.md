# Port test matrix

Every upstream test function of typesafe-sdk-python 0.7.1 maps to a Go test
or to a documented deviation. Upstream is `typesafe-ai/typesafe-sdk-python` at
`0ffd094c72ed9445223060b24ffd7a56aa781fb4`: the 129 test functions that
pytest collects under `tests/` (111 SDK behaviour + 18 tooling). The derived
name list is [`upstream-tests.txt`](upstream-tests.txt).
`.github/scripts/port-test-matrix.py` checks this file on every CI run; the
rows were seeded from Appendix D of the port plan, which also defines the row
IDs that the plan's waves cite.

## Status values

| Status | Meaning | Checker rule |
| --- | --- | --- |
| `planned` | not ported yet | passes; with `--no-planned` (CI from W6.3) it fails |
| `ported` | the Go test exists | the Go cell names at least one backtick-quoted `Test…` identifier, and every one of them is listed by `go test -list '.*' -tags live ./...`; `pkg.TestName` must be listed by a package whose import path ends in `/pkg`, so tests of the root package are written unqualified (`TestX`, never `typesafe.TestX`: the root import path ends in `/typesafe-sdk-go`) |
| `deviation` | replaced by a documented behaviour difference | the Go cell cites an Appendix B row as the word `deviation` followed by a double-quoted, non-blank reference (`deviation "one deadline per attempt"`); Appendix B rows are unnumbered and `B<n>` would read as a benchmark ID, so there is no numeric form; `same deviation` takes the citation of the nearest row above it in the same group that carries one (rows without a citation in between are skipped); any backtick-quoted `Test…` identifier in the cell must exist, as for `ported` |

## Format

- One level-3 `` ### `tests/<file>` (<count>) `` or
  `` ### `tests/<file>` (<count>, <note>) `` heading per upstream file, and
  only one per file; a heading of another level naming a `tests/` file
  fails. `<count>` must equal the number of rows in that group. The checker
  keys every row by that file and the upstream function name.
- Each group holds exactly one table: the header row
  `| ID | Upstream | Go test / deviation | status |`, a separator row of four
  cells (each three or more `-`, optionally with a `:` at either end), and
  one row per upstream test. A table line starts with `|` after at most
  three spaces (four make a code block). A group without a table, a table
  without that header or separator, and a second table in a group fail.
  After the first group heading, every table row must sit in a group.
- Columns: ID, upstream function name (backtick-quoted; `Class::test_x` for a
  test method), Go test or deviation, status. The ID cell may not be blank or
  hold only `-` and `:`. A literal `|` inside a cell is written `\|`. A test
  name qualified by a path (`internal/codec.TestX`) fails in every status:
  write `codec.TestX` or `TestX`.
- Changing a row's status is the only edit a porting wave makes here, plus the
  Go test name or deviation citation when it differs from the seed.

## Rows

### `tests/test_clients.py` (21)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| C1 | `test_round_trip` | `TestSystemOneRoundTrip` (typed, raw, mixed) | ported |
| C2 | `test_extra_body_shallow_override` | `TestExtraBodyShallowOverride` (body) + `TestClientExtraBodyShallowOverride` (through the client) | ported |
| C3 | `test_unserializable_request_body_raises` | `TestUnencodableBodyFailsBeforeNetwork` (body) + `TestClientUnencodableBodyFailsBeforeNetwork` (through the client) | ported |
| C4 | `test_raw_question_passthrough` | `TestRawQuestionPassthrough` | ported |
| C5 | `test_question_schema_validation_is_left_to_api` | `TestRawQuestionSchemaLeftToAPI` | ported |
| C6 | `test_rich_descriptions` | `TestStructuredContentRoundTrip` | ported |
| C7 | `test_models_shape` | `TestModelsListShape` | ported |
| C8 | `test_models_ignore_unknown_fields` | `TestModelsIgnoreUnknownFields` | ported |
| C9 | `test_invalid_models_response` | `TestModelsInvalidBodies` (4 bodies) | ported |
| C10 | `test_validation_before_network` | `TestQuestionValidationBeforeNetwork` | ported |
| C11 | `test_error_mapping` | `TestAPIErrorMapping` (11 statuses) | ported |
| C12 | `test_error_messages` | `TestAPIErrorMessages` (8 bodies) | ported |
| C13 | `test_transport_errors` | `TestTransportErrorsBecomeConnectionOrTimeout` (loopback failures through the client) + `TestAttemptErrorClassification` | ported |
| C14 | `test_system_one_timeout_override` | deviation "one deadline per attempt" + `TestPerCallTimeoutOverride` + `TestRetryRecoversWithOverrides` (each retry gets the call's per-attempt deadline anew) | deviation |
| C15 | `test_headers_timeout_and_logging` | `TestProtectedHeadersAndPrefixBaseURL` + `TestSystemOneOverHTTP2` (the prefix on the wire) | ported |
| C16 | `test_http_client_settings` | `TestCallerTransportKeepsItsSettings` | ported |
| C17 | `test_supplied_network_resources_closed` | `TestCloseClosesSuppliedTransport` + `TestCloseIdlesSuppliedHTTPTransport` (a `*http.Transport` through `WithRoundTripper`, R79) | ported |
| C18 | `test_owned_http_client_closed` | `TestCloseClosesOwnedTransport` | ported |
| C19 | `test_exceptional_context_closes_http_client` | `TestCloseAfterFailedCall` | ported |
| C20 | `test_task_cancellation_closes_context` | `TestCancelInFlightRequest` (one attempt; `context.Canceled` itself, not an SDK error, as upstream lets `CancelledError` through; the loopback server sees the stream reset) | ported |
| C21 | `test_cancellation_propagates` | `TestCancelledContextMakesOneAttempt` (one attempt under the production policy: the transport's `context.Canceled`, upstream's shape, is a `*ConnectionError` that only the never-retry-a-cancellation rule keeps to one attempt; a cancelled context returns `context.Canceled` itself) | ported |

### `tests/test_config.py` (11)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| F1 | `test_transport_and_http_client_mutually_exclusive` | deviation "one transport option, two kinds" + `TestTransportOptionsAreExclusive` | deviation |
| F2 | `test_model_override` | `TestModelOverridePerCall` | ported |
| F3 | `test_resolution` | `TestConfigResolutionOrder` (default/env/explicit) + `TestConfigResolutionOnTheWire` | ported |
| F4 | `test_missing_key` | `TestMissingAPIKey` | ported |
| F5 | `test_api_key_whitespace` | `TestAPIKeyTrimmed` + `TestAPIKeyTrimmedOnTheWire` | ported |
| F6 | `test_invalid_explicit_key_does_not_fall_back_to_env` | `TestInvalidExplicitKeyDoesNotFallBack` | ported |
| F7 | `test_invalid_api_key` | `TestInvalidAPIKeyNeverEchoed` | ported |
| F8 | `test_empty_env_unset` | `TestBlankEnvIsUnset` + `TestBlankEnvIsUnsetOnTheWire` | ported |
| F9 | `test_invalid_timeout` | `TestInvalidTimeout` + `TestCallOptionsRefused` (the per-call `Timeout`) | ported |
| F10 | `test_timeout_object` | deviation "one deadline per attempt" + `TestTimeoutSettings` + `TestPerCallTimeoutOverride` | deviation |
| F11 | `test_http_client_timeout_precedence` | deviation "a custom transport owns its timeouts" + `TestCallerTransportOwnsItsTimeouts` | deviation |

### `tests/test_errors.py` (6)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| E1 | `test_exception_reconstruction` | deviation "errors are values" + `TestErrorsAsRoundTrip` (the 14 upstream rows, each matched with `errors.As` through wraps, copied by value, read alike and unwrapped alike; rows 3 and 4, Python's base class `TypeSafeAPIError`, are the `Kind` of their status when the SDK builds them and `APIErrorOther` in a caller's literal, and row 14, an httpx `Timeout` object, is `Timeout: 0`, one deadline per attempt) | deviation |
| E2 | `test_api_error_from_process_pool` | deviation "no process pools": a value handed to another goroutine is the same value and nothing is serialised (`TestErrorsAsRoundTrip` reads a copy from four goroutines) | deviation |
| E3 | `test_api_error_request_context` | `TestAPIErrorRendersEndpointStatusMessageRequestID` + `TestAPIErrorRequestContextThroughClient` | ported |
| E4 | `test_api_error_endpoint_omits_url_credentials` | `TestEndpointOmitsCredentialsQueryFragment` (constructor-level: a base URL with credentials is refused when the client is built, R63) | ported |
| E5 | `test_message_override` | `TestAPIErrorMessageOverride` (constructor-level, as upstream) | ported |
| E6 | `test_error_body_edge_cases` | `TestAPIErrorBodyEdgeCases` (8 exact) + `TestAPIErrorBodyEdgeCasesThroughClient` + deviation "plain-text body cut at 200" (`long-plain-message`) | ported |

### `tests/test_integration.py` (3)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| I1 | `test_live_models` | `livetests.TestLiveModels` | planned |
| I2 | `test_live_questions` | `livetests.TestLiveQuestions` | planned |
| I3 | `test_live_pydantic_response` | `livetests.TestLiveTypedResponse` | planned |

### `tests/test_logging.py` (8)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| L1 | `test_secret_headers_redacted` | `TestSecretHeadersRedacted` (9 × 3) | planned |
| L2 | `test_transport_errors_do_not_expose_credentials` | `TestTransportErrorsNeverExposeCredentials` | planned |
| L3 | `test_exception_redaction_escaped_values` | `TestRedactionCoversGoEscapeForms` (4 headers × 3 credentials; raw, `%q`, `%+q` and JSON forms, the Go analogue of raw, bytes repr and `json.dumps`) | ported |
| L4 | `test_exception_redaction_shared_causes_cycles_and_notes` | deviation "cause via `errors.Unwrap` unless it printed a credential" (Go errors have no notes or cycles: the chain is walked, never copied, and a cause that printed a credential is replaced whole by `*scrubbedError`, R81 (3)) + `TestCredentialsCause` | deviation |
| L5 | `test_exception_redaction_structured_constructor` | deviation "cause via `errors.Unwrap` unless it printed a credential" (Go errors are not rebuilt from messages: the stand-in keeps the redacted text, the redacted `%+v` rendering of the chain as text (R95) and the standard sentinels it matched; a caller error type holding the request in a pointer field stays reachable by `errors.As`, R82 (a)) + `TestCredentialsCause` + `TestScrubbedErrorFormat` | deviation |
| L6 | `test_exception_redaction_preserves_network_diagnostics` | `TestRedactionKeepsCleanChains` (the scrub, a RoundTripper, a caller dialer) | ported |
| L7 | `test_logger_level_controls_output` | `TestLogLevelsPerAttempt` (DEBUG, INFO, WARN, LevelTrace; one INFO record per attempt; no header above DEBUG, no body above LevelTrace) + `TestLogTransportRecords` + `TestLogWarnCapThroughClient` | ported |
| L8 | `test_setup_logging_from_env` | deviation "`TYPESAFE_LOG_LEVEL` not read" + `TestLogLevelEnvNotRead` (5 upstream values × INFO, DEBUG, no `WithLogger`) | deviation |

### `tests/test_pydantic_response_models.py` (5)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| P1 | `test_standalone_pydantic_response_model` | `TestDecodeAsWithSeparateQuestions` (questions built separately; `DecodeAs[KnownResponse]`; extra answer members ignored) | planned |
| P2 | `test_explicit_default_response_model` | `TestSystemOneDefaultResponse` | planned |
| P3 | `test_pydantic_system_one_response_subclass` | `TestDecodeAsOptionalFieldAndUnknownAnswer` (`optional` field absent → `Present() == false`; user struct with `options=friendly\|hostile`; unknown `future` type dropped; `Answers()` still complete; request id kept) | planned |
| P4 | `test_pydantic_response_validation` | `TestAskValidationFieldPaths` | planned |
| P5 | `test_custom_response_preserves_api_errors` | `TestAskPreservesAPIErrors` | planned |

### `tests/test_questions.py` (11)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| Q1 | `test_normalization_preserves_objects` | `TestTypedQuestionsWireForm` | ported |
| Q2 | `test_normalization_preserves_raw_questions` | `TestRawQuestionsPassThrough` | ported |
| Q3 | `test_raw_questions_require_structural_keys` | `TestRawQuestionStructuralChecks` (10 cases) | ported |
| Q4 | `test_direct_encoding_omits_only_default_fields` | `TestUnsetMembersLeftOffWire` (5; two cases per deviation "Typed noul sends `null` outcomes / empty criteria") | ported |
| Q5 | `test_discriminators_are_automatic` | `TestEachKindWritesItsTypeTag` | ported |
| Q6 | `test_invalid_typed_question_is_rejected_on_construction` | deviation "not representable" | deviation |
| Q7 | `test_typed_questions_reject_unknown_fields` | deviation "not representable" | deviation |
| Q8 | `test_optional_noul_criteria` | `TestNoulCriteriaShapes` (12; the typed empty-criteria case per deviation "Typed noul sends `null` outcomes / empty criteria") | ported |
| Q9 | `test_typed_noul_criteria_reject_unknown_fields` | deviation "not representable" | deviation |
| Q10 | `test_empty_score_criteria_is_rejected` | `TestScoreWithoutLevelsRejected` | ported |
| Q11 | `test_covariant_question_mappings` | `TestMixedQuestionMapsThroughOneBuilder` | ported |

### `tests/test_responses.py` (15)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| R1 | `test_malformed_response_raises_validation_error` | `TestMalformedResponseFieldPaths` (8) + `TestMalformedResponseThroughClient` | ported |
| R2 | `test_nested_missing_field_path` | `TestModelsMissingMemberPath` | ported |
| R3 | `test_response_carries_request_id` | `TestResponseRequestID` | ported |
| R4 | `test_response_carries_raw_http_response` | `TestResponseMeta` | ported |
| R5 | `test_response_serialization_excludes_http_metadata` | `TestResponseJSONRoundTrip` (models and systemone) + `TestResponseJSONFixtures` | ported |
| R6 | `test_copied_response_preserves_metadata` | deviation "responses are values" + `TestResponseCopyKeepsMeta` | deviation |
| R7 | `test_missing_raw_raises_on_access` | deviation "empty `Meta()`" + `TestZeroResponseHasEmptyMeta` | deviation |
| R8 | `test_missing_request_id_raises_on_access` | `TestRequestIDAbsent` | ported |
| R9 | `test_unknown_extra_fields_tolerated` | `codec.TestUnknownMembersIgnored` + `TestUnknownMembersIgnoredThroughClient` | ported |
| R10 | `test_unknown_answer_type_ignored` | `TestUnknownAnswerTypeSkipped` + `TestUnknownAnswerTypeThroughClient` | ported |
| R11 | `test_response_preserves_nested_json` | `codec.TestStructuredLegendExactBytes` | ported |
| R12 | `test_answer_attributes_and_dictionary_types` | `TestAnswerJSONShapes` | ported |
| R13 | `test_public_response_types_ignore_unknown_fields` | `codec.TestPublicTypesIgnoreUnknownMembers` (7 types) + `TestPublicTypesIgnoreUnknownMembersThroughClient` | ported |
| R14 | `test_answer_fields_are_frozen` | deviation "unexported fields with getters" | deviation |
| R15 | `test_answer_groups_are_cached_and_not_serialized` | deviation "`iter.Seq2` filters" + `TestResponseJSONRoundTrip` | deviation |

### `tests/test_retry.py` (25)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| RT1 | `test_retry_policy_invalid_timeout` | `TestRetryPolicyInvalidBudget` (zero and negative budgets refused with Python's message by `NewClient(WithRetry)` and by both calls with `Retry`, before any request; Python's inf and nan seconds are not a `time.Duration`; `NoBudget()` is `timeout=None`) | ported |
| RT2 | `test_zero_backoff_retries` | `TestZeroBackoffRetriesAtOnce` (6: three zero backoffs × recover; `[absent, "1"]`; the wait is 0 of fake time) | ported |
| RT3 | `test_invalid_backoff` | `TestRetryPolicyInvalidBackoff` (negative initial or maximum, named in the message; Python's nan and inf seconds are not a `time.Duration`) | ported |
| RT4 | `test_invalid_backoff_jitter` | `TestRetryPolicyInvalidJitter` (-0.1, 1.1, NaN, ±Inf refused; 0 and 1 accepted) | ported |
| RT5 | `test_invalid_max_retries` | `TestRetryPolicyInvalidMaxRetries` (negative refused; Python's 0.5, nan and inf are not an `int`) | ported |
| RT6 | `test_retry_policy_timeout_budget` | `TestRetryBudgetStopsBeforeDelay` (6 cases × models and system_one, each SDK call a fresh budget; waits and attempt durations on fake time) | ported |
| RT7 | `test_retry_policy_timeout_override` | `TestPerCallBudgetOverride` | ported |
| RT8 | `test_default_retry_statuses` | `TestDefaultRetryStatuses` (12) | ported |
| RT9 | `test_connection_retry_recovers` | `TestConnectionErrorsRetried` (the four kinds through the Recorder with the backoff waits; a refused dial, a reset mid-body, the attempt's deadline and a GOAWAY after the request was written through the SDK's transport and the loopback server) | ported |
| RT10 | `test_server_delay_through_tenacity` | `TestRetryAfterHonoured` (4) | ported |
| RT11 | `test_parse_retry_after` | `TestParseRetryAfterTable` (9, and the wait each gives through the client) | ported |
| RT12 | `test_backoff_dates_cap_and_jitter` | `TestBackoffScheduleAndDates` (also the schedule and a date measured through the client) | ported |
| RT13 | `test_system_one_retry_override` | `TestPerCallRetryPolicyOverride` | ported |
| RT14 | `test_async_concurrent_retry_state` | partial deviation "Sync and async clients → one `*Client`, `context.Context`" + `TestConcurrentCallsCountTheirOwnRetries` (goroutines for asyncio tasks) | deviation |
| RT15 | `test_system_one_retry_recovers_with_overrides` | `TestRetryRecoversWithOverrides` (same bytes, headers and per-attempt timeout on every attempt, a fresh `GetBody` read hashed per attempt (PM4); httpx's `Timeout(3.0, connect=1.0, read=5.0)` is one 3 s deadline, C14's one deadline per attempt) | ported |
| RT16 | `test_concurrent_system_one_overrides` | `TestConcurrentCallsKeepTheirOverrides` | ported |
| RT17 | `test_exhausted_transport_retry` | `TestExhaustedTransportRetryReturnsLastError` | ported |
| RT18 | `test_exhausted_retry_preserves_final_http_error` | `TestExhaustedRetryKeepsLastAPIError` | ported |
| RT19 | `test_cancel_pending_retry` | `TestCancelPendingRetry` (`context.Canceled` itself, its cause kept; a caller deadline in the wait is a `*TimeoutError` without a timeout) | ported |
| RT20 | `test_retry_policy_max_retries` | `TestMaxRetriesCountsAttempts` | ported |
| RT21 | `test_retry_policy_custom_statuses` | `TestCustomStatusesReplaceDefault` | ported |
| RT22 | `test_retry_policy_per_call_override` | `TestPerCallMaxRetries` | ported |
| RT23 | `test_retry_policy_exceptions_and_predicate` | partial deviation "`RetryPolicy.exceptions` → dropped; `Predicate` kept" + `TestPredicateOptsIn` (the predicate half; a type test in the predicate stands for `exceptions`; a 2xx in the status set is never retried, deviation "2xx in retry statuses retries a non-validating body → never; `Predicate` can opt in") | deviation |
| RT24 | `test_retry_policy_wait_options` | `TestWaitOptions` | ported |
| RT25 | `test_backoff_extreme_values` | `TestBackoffExtremeValues` (1e-300, 1e300 and 1e308 seconds are not Durations: 1 ns and the longest Duration stand for them) | ported |

### `tests/test_types.py` (6)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| T1 | `test_str_subclasses_fallback_to_strings` | deviation "`any` state" + `TestNamedStringStateEncodesAsString` + `TestClientStateForms` | deviation |
| T2 | `test_json_value_and_state_exclude_top_level_none` | deviation "`any` state" + `TestScalarStatesRefused` (`nil`, number, bool) + `TestClientUnencodableBodyFailsBeforeNetwork` | deviation |
| T3 | `test_array_inputs` | `TestArrayContentEverywhere` (4 array positions: state, instructions, criteria description, score levels; upstream parametrises raw/typed) | ported |
| T4 | `test_raw_optional_fields_preserve_explicit_null` | `TestRawQuestionKeepsExplicitNull` | ported |
| T5 | `test_explicitly_nullable_json_values` | `TestNullInsideContentSurvives` | ported |
| T6 | `test_abstract_input_containers_encode` | `TestMapSliceStructStates` | ported |

### `tests/test_docs.py` (2, live sybil examples)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| XD1 | `test_markdown` | README and `docs/*.md` Go snippets are `examples/` packages compiled by `go vet ./examples/...` in CI and proven identical to the Markdown blocks by `docs-snippets.py`; the ones that call the API run as `livetests.TestExamples` (deviation "no sybil") | planned |
| XD2 | `test_python_doctests` | `Example*` functions in root run by `go test`; live ones behind `//go:build live` | planned |

### `tests/test_public_api_surface.py` (3)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| XA1 | `test_public_members` | `TestPublicAPISurface`: golden of exported names and signatures from `go/types` (`go/importer` "source"), stable across comment edits | planned |
| XA2 | `test_package_exports` | same golden; `internal/` packages are unimportable by construction | planned |
| XA3 | `test_constructor_kwargs` | `TestClientOptionsSurface`: golden of the `With*` option names (functional options; no positional parameters) | planned |

### `tests/test_public_sync.py` (10, skipped outside the dev repository)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| XS1 | `test_sign_snapshot_and_push` | deviation "dev→public sync tooling not ported" | planned |
| XS2 | `test_signing_failure_keeps_refs` | same deviation | planned |
| XS3 | `test_dry_run_skips_github` | same deviation | planned |
| XS4 | `test_snapshot_and_push_retries` | same deviation | planned |
| XS5 | `test_existing_history_deletions_and_immutable_tags` | same deviation | planned |
| XS6 | `test_invalid_includes` | same deviation | planned |
| XS7 | `test_unsafe_snapshots` | same deviation | planned |
| XS8 | `test_version_mismatch` | same deviation | planned |
| XS9 | `test_atomic_push_rejects_concurrent_update` | same deviation | planned |
| XS10 | `test_release_contributors` | same deviation | planned |

### `tests/test_release_notes.py` (2)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| XR1 | `test_release_notes` | deviation "no release-notes script" | planned |
| XR2 | `test_invalid_release_notes` | same deviation | planned |

### `tests/test_typing.py` (1)

| ID | Upstream | Go test / deviation | status |
| --- | --- | --- | --- |
| XT1 | `test_public_typing` | negative expectations → the 13 + 3 runtime `*ConfigError` rejections of AC-F8 (upstream `tests/typing/negative/*` reviewed for Go analogues in W4.1); the three positive fixtures (`valid.py`, `transport.py`, `pydantic_response_models.py`) → `go vet ./examples/...` (W6.4) | planned |
