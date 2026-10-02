# Port test matrix

Every upstream test function of system-one-adapter-python 0.2.1 maps to a Go
test or to a documented deviation. Upstream is
`typesafe-ai/system-one-adapter-python` at
`e1d4cc938204b22fc5a3c3aca7044072fe3f712d`: 71 test functions under `tests/`,
which pytest expands into 424 collected items. The two lists are
[`../testdata/upstream/functions.txt`](../testdata/upstream/functions.txt) and
[`../testdata/upstream/items.txt`](../testdata/upstream/items.txt); the first
line of each is the command that produced it.

`TestPortTestMatrix` (`matrix_test.go`) checks this file against those two
lists on every `go test` run: one function row per upstream function, the
`Items` cell of each row equal to the number of collected items of that
function, 424 in all, and every `Status` cell one of the values below. For a
`ported` row, every test its `Go test` cell names, written
`<package>.Test<Name>` with the package's path from the repository root
(`adapter`, `adapter/openai`), must be listed by `go test -list` for that
package. The cassette table names the 25 files of
[`../testdata/cassettes/`](../testdata/cassettes) and, where its last cell but
one says `yes`, the file of the same name in
[`../testdata/expected/`](../testdata/expected); the test checks that each
exists.

## Status values

| Status | Meaning |
| --- | --- |
| `planned` | not yet ported: the Go test in the row does not exist yet |
| `ported` | the Go test named in the row exists and ports the upstream test |
| `deviation` | no Go test of the same behaviour; the `Intended outcome` cell names the row of [`deviations.md`](deviations.md) |

A row changes from `planned` to `ported` or `deviation` in the change that
adds its tests. The `Intended outcome` cell says whether the row is to be
ported or replaced by a deviation, with the deviations (`DVn`) that shape the
Go test; a Go case is one subtest, and upstream's sync and async doubling
collapses into one Go case (DV1), so a row has fewer Go cases than items.

## Functions

| ID | Upstream | Items | Go test | Go cases | Intended outcome | Status |
| --- | --- | --- | --- | --- | --- | --- |
| FM1 | `tests/test_client_with_fake_model.py::test_sdk_questions_and_response_serialization` | 4 | `adapter.TestSDKQuestionsAndResponseSerialization` (`evaluate_test.go`) | 2 | ported (DV1, DV5) | planned |
| FM2 | `tests/test_client_with_fake_model.py::test_prompted_mode_adds_schema_instructions_native_does_not` | 2 | `adapter.TestPromptedModeAddsSchemaInstructions` (`evaluate_test.go`) | 2 | ported | planned |
| FM3 | `tests/test_client_with_fake_model.py::test_structured_state_prompt_is_delimited_and_escapes_embedded_tags` | 1 | `adapter.TestStatePromptIsDelimitedAndEscaped` (`evaluate_test.go`) | 1 | ported | planned |
| FM4 | `tests/test_client_with_fake_model.py::test_transient_errors_are_retried` | 4 | `adapter.TestTransientErrorsAreRetried` (`evaluate_test.go`) | 2 | ported (DV1, DV3: per call via `ContextWithRetry`) | planned |
| FM5 | `tests/test_client_with_fake_model.py::test_retries_are_exhausted` | 2 | `adapter.TestRetriesAreExhausted` (`evaluate_test.go`) | 1 | ported (DV1) | planned |
| FM6 | `tests/test_client_with_fake_model.py::test_malformed_retry_exhaustion_preserves_debug` | 8 | `adapter.TestMalformedRetryExhaustionPreservesDebug` (`evaluate_test.go`) | 4 | ported (DV1, DV8) | planned |
| FM7 | `tests/test_client_with_fake_model.py::test_usage_totals_preserve_unknown_counts_across_corrections` | 14 | `adapter.TestUsageTotalsPreserveUnknownCounts` (`evaluate_test.go`) | 7 | ported (DV1) | planned |
| FM8 | `tests/test_client_with_fake_model.py::test_usage_separates_last_attempt_from_cumulative_totals` | 2 | `adapter.TestUsageSeparatesLastAttemptFromTotals` (`evaluate_test.go`) | 1 | ported (DV1) | planned |
| FM9 | `tests/test_client_with_fake_model.py::test_attempts_are_independent_and_replayable` | 2 | `adapter.TestAttemptsAreIndependentAndReplayable` (`evaluate_test.go`) | 1 | ported (DV1) | planned |
| FM10 | `tests/test_client_with_fake_model.py::test_invalid_questions_are_rejected` | 5 | `adapter.TestInvalidQuestionsAreRejected` (`seam_test.go`; raw bodies, plus the SDK's own refusal where it refuses first) | 5 | ported (DV11) | planned |
| FM11 | `tests/test_client_with_fake_model.py::test_malformed_structure_is_retried` | 8 | `adapter.TestMalformedStructureIsRetried` (`evaluate_test.go`) | 4 | ported (DV1, DV8) | planned |
| FM12 | `tests/test_client_with_fake_model.py::test_missing_provider_setting_is_rejected` | 2 | `adapter.TestMissingProviderIsRejected` (`model_test.go`) | 1 | ported (DV1, DV9) | planned |
| LV1 | `tests/test_client_with_live_apis.py::test_live_responses_match_reference_shape` | 12 | `adapter.TestReplayReferenceShape` (`replay_test.go`, 12 cassettes); live: `adapter/livetest.TestLiveReferenceShape` (`-tags live`) | 12 | ported (DV6, DV7: the members those deviations change are normalised before the comparison) | planned |
| LV2 | `tests/test_client_with_live_apis.py::test_live_models_follow_question_instructions_and_criteria` | 12 | `adapter.TestReplayFollowsInstructionsAndCriteria` (`replay_test.go`); live: `adapter/livetest.TestLiveFollowsInstructionsAndCriteria` | 12 | ported | planned |
| LV3 | `tests/test_client_with_live_apis.py::test_live_typesafe_response_matches_reference_shape` | 1 | `adapter.TestReplayTypeSafeReference` (`replay_test.go`: the root SDK against the TypeSafe cassette, the baseline shape); live: `adapter/livetest.TestLiveTypeSafeReference` | 1 | ported | planned |
| GT1 | `tests/test_gemini_transports.py::test_gemini_transport_preserves_corrections_and_usage` | 4 | `adapter/gemini.TestTransportPreservesCorrectionsAndUsage` (`gemini_test.go`) | 2 | ported (DV1) | planned |
| GT2 | `tests/test_gemini_transports.py::test_gemini_incomplete_http_response_is_not_an_answer` | 2 | `adapter/gemini.TestIncompleteResponseIsNotAnAnswer` | 1 | ported (DV1) | planned |
| GT3 | `tests/test_gemini_transports.py::test_gemini_transport_errors_obey_retry_budget` | 8 | `adapter/gemini.TestTransportErrorsObeyRetryBudget` | 4 | ported (DV1) | planned |
| OT1 | `tests/test_openai_transports.py::test_openai_transport_preserves_corrections_and_usage` | 16 | `adapter/openai.TestTransportPreservesCorrectionsAndUsage` (`openai_test.go`) | 8 | ported (DV1) | planned |
| OT2 | `tests/test_openai_transports.py::test_unfinished_responses_are_not_treated_as_answers` | 2 | `adapter/openai.TestUnfinishedResponsesAreNotAnswers` (`responses_test.go`) | 2 | ported | planned |
| OT3 | `tests/test_openai_transports.py::test_concurrent_attempts_are_isolated_and_preserve_failed_responses` | 3 | `adapter/openai.TestConcurrentAttemptsAreIsolated` (goroutines) | 3 | ported (DV1) | planned |
| OT4 | `tests/test_openai_transports.py::test_custom_endpoint_from_environment_defaults_to_chat` | 2 | `adapter/openai.TestEnvironmentBaseURLSelectsChat` (`t.Setenv`) | 1 | ported (DV1) | planned |
| PL1 | `tests/test_provider_lifecycle.py::test_reuses_owned_provider_and_closes_sdk_on_context_exit` | 4 | `adapter.TestReusesOwnedProviderAndClosesIt` (`lifecycle_test.go`) | 2 | ported (DV1, DV10) | planned |
| PL2 | `tests/test_provider_lifecycle.py::test_cache_uses_resolved_provider_and_model_and_is_per_client` | 4 | `adapter.TestProviderCacheKey` | 2 | ported (DV1) | planned |
| PL3 | `tests/test_provider_lifecycle.py::test_injected_provider_is_borrowed` | 8 | `adapter.TestInjectedProviderIsBorrowed` | 4 | ported (DV1, DV9) | planned |
| PL4 | `tests/test_provider_lifecycle.py::test_custom_provider_without_close_remains_supported` | 4 | `adapter.TestProviderWithoutCloseIsSupported` | 2 | ported (DV1) | planned |
| PL5 | `tests/test_provider_lifecycle.py::test_exceptional_exit_closes_owned_sdks` | 16 | `adapter.TestCloseAfterFailureClosesOwnedProviders` (body, request, validation; `cancelled` → DV2) | 6 | ported (DV1, DV2 for the `cancelled` case) | planned |
| PL6 | `tests/test_provider_lifecycle.py::test_cleanup_continues_after_failure` | 4 | `adapter.TestCloseContinuesAfterFailure` | 2 | ported (DV1) | planned |
| PL7 | `tests/test_provider_lifecycle.py::test_close_before_first_use_does_not_construct_providers` | 4 | `adapter.TestCloseBeforeFirstUse` | 2 | ported (DV1) | planned |
| PL8 | `tests/test_provider_lifecycle.py::test_failed_construction_is_not_cached` | 4 | `adapter.TestFailedConstructionIsNotCached` | 2 | ported (DV1) | planned |
| PL9 | `tests/test_provider_lifecycle.py::test_environment_is_captured_on_first_use` | 4 | `adapter.TestEnvironmentIsReadAtConstruction` (`t.Setenv`) | 2 | ported (DV1) | planned |
| PL10 | `tests/test_provider_lifecycle.py::test_async_cleanup_propagates_cancellation_after_remaining_cleanup` | 3 | none: `Close` takes no context; the "remaining providers are still closed after one fails" half is PL6 | 0 | deviation DV2 | planned |
| PL11 | `tests/test_provider_lifecycle.py::test_concurrent_close_waits_for_same_cleanup` | 8 | `adapter.TestConcurrentCloseWaitsForSameCleanup` | 4 | ported (DV1) | planned |
| PL12 | `tests/test_provider_lifecycle.py::test_cancelling_close_waiter_does_not_interrupt_cleanup` | 1 | none: a Go `Close` waiter cannot be cancelled | 0 | deviation DV2 | planned |
| PL13 | `tests/test_provider_lifecycle.py::test_concurrent_first_use_reuses_pool_and_isolates_traces` | 4 | `adapter.TestConcurrentFirstUseReusesProvider` (8 goroutines) | 2 | ported (DV1) | planned |
| PN1 | `tests/test_provider_nonanswers.py::test_chat_completion_finish_reason` | 28 | `adapter/openai.TestChatFinishReason` (`nonanswer_test.go`) | 14 | ported (DV1) | planned |
| PN2 | `tests/test_provider_nonanswers.py::test_openai_missing_usage` | 64 | `adapter/openai.TestMissingUsage` | 32 | ported (DV1) | planned |
| PN3 | `tests/test_provider_nonanswers.py::test_anthropic_nonanswers` | 36 | `adapter/anthropic.TestNonAnswers` (`anthropic_test.go`) | 18 | ported (DV1) | planned |
| PN4 | `tests/test_provider_nonanswers.py::test_openai_responses_refusal` | 8 | `adapter/openai.TestResponsesRefusal` | 4 | ported (DV1) | planned |
| PR1 | `tests/test_provider_requests.py::test_openai_native_response_format_wraps_schema` | 1 | `adapter/openai.TestChatResponseFormatWrapsSchema` (`chat_test.go`) | 1 | ported | planned |
| PR2 | `tests/test_provider_requests.py::test_openai_prompted_sends_no_response_format` | 1 | `adapter/openai.TestChatPromptedSendsNullResponseFormat` | 1 | ported | planned |
| PR3 | `tests/test_provider_requests.py::test_openai_result_reads_content_and_usage` | 1 | `adapter/openai.TestChatResultReadsContentAndUsage` | 1 | ported | planned |
| PR4 | `tests/test_provider_requests.py::test_anthropic_request_puts_schema_in_output_config_when_structured` | 1 | `adapter/anthropic.TestRequestPutsSchemaInOutputConfig` | 1 | ported | planned |
| PR5 | `tests/test_provider_requests.py::test_anthropic_request_omits_output_config_when_prompted` | 1 | `adapter/anthropic.TestRequestOmitsOutputConfigWhenPrompted` | 1 | ported | planned |
| PR6 | `tests/test_provider_requests.py::test_gemini_request_puts_schema_in_response_format_when_structured` | 1 | `adapter/gemini.TestRequestPutsSchemaInResponseFormat` | 1 | ported | planned |
| PR7 | `tests/test_provider_requests.py::test_gemini_request_omits_response_format_when_prompted` | 1 | `adapter/gemini.TestRequestOmitsResponseFormatWhenPrompted` | 1 | ported | planned |
| PR8 | `tests/test_provider_requests.py::test_gemini_request_sends_correction_turns_as_steps` | 1 | `adapter/gemini.TestRequestSendsCorrectionTurnsAsSteps` | 1 | ported | planned |
| PR9 | `tests/test_provider_requests.py::test_gemini_result_reads_output_text_and_usage` | 1 | `adapter/gemini.TestResultReadsOutputTextAndUsage` (plus cases for how `output_text` is built, the text of the last run of consecutive text items of `model_output` steps: several text parts, a non-text part between runs, several `model_output` steps) | 1 + 4 | ported | planned |
| PR10 | `tests/test_provider_requests.py::test_gemini_incomplete_status_is_not_treated_as_an_answer` | 4 | `adapter/gemini.TestIncompleteStatusIsNotAnAnswer` | 4 | ported | planned |
| PR11 | `tests/test_provider_requests.py::test_gemini_omitted_usage_is_not_treated_as_an_answer` | 1 | `adapter/gemini.TestOmittedUsageIsNotAnAnswer` | 1 | ported | planned |
| PR12 | `tests/test_provider_requests.py::test_anthropic_result_joins_text_blocks_and_reads_usage` | 1 | `adapter/anthropic.TestResultJoinsTextBlocks` | 1 | ported | planned |
| PR13 | `tests/test_provider_requests.py::test_anthropic_output_limit` | 8 | `adapter/anthropic.TestOutputLimit` | 4 | ported (DV1) | planned |
| PR14 | `tests/test_provider_requests.py::test_anthropic_rejects_nonpositive_output_limit` | 4 | `adapter/anthropic.TestNewRejectsNonpositiveMaxTokens` | 2 | ported (DV1) | planned |
| PR15 | `tests/test_provider_requests.py::test_build_providers_select_gemini` | 1 | `adapter.TestResolveModel` case `gemini prefix builds a gemini provider` (`model_test.go`) | 1 | ported (DV9) | planned |
| PR16 | `tests/test_provider_requests.py::test_unknown_provider_is_rejected` | 1 | `adapter.TestResolveModel` case `default model naming a provider without a factory` | 1 | ported (DV9) | planned |
| RT1 | `tests/test_provider_retries.py::test_retry_policy_controls_http_attempts` | 12 | `adapter.TestRetryPolicyControlsHTTPAttempts` (`retry_provider_test.go`: the three real providers over a 503 transport) | 6 | ported (DV1, DV16) | planned |
| SC1 | `tests/test_schema.py::test_invalid_dictionary_questions_are_rejected` | 4 | `adapter/internal/schema.TestInvalidQuestionsAreRejected` (`internal/schema/question_test.go`) | 4 | ported | ported |
| SC2 | `tests/test_schema.py::test_sdk_question_fields_are_revalidated` | 1 | none of that shape: `adapter/internal/schema.TestQuestionsValidatedFromWire` checks the same rule on a raw body | 0 | deviation DV15 | deviation |
| SC3 | `tests/test_schema.py::test_question_ids_preserve_arbitrary_names` | 2 | `adapter/internal/schema.TestQuestionNamesPreserveArbitraryNames` | 2 | ported | ported |
| SC4 | `tests/test_schema.py::test_probability_labels_preserve_arbitrary_names` | 1 | `adapter/internal/schema.TestProbabilityLabelsPreserveArbitraryNames` | 1 | ported | ported |
| SC5 | `tests/test_schema.py::test_output_validation_preserves_types_bounds_and_allowed_values` | 13 | `adapter/internal/schema.TestOutputValidationTypesBoundsAndValues` (the `nan` case is a `NaN` token, invalid JSON for `jsontext`: rejected, as upstream rejects it) | 13 | ported | ported |
| SC6 | `tests/test_schema.py::test_output_rejects_extra_fields_and_internal_field_names` | 3 | `adapter/internal/schema.TestOutputRejectsExtraMembers` | 3 | ported | ported |
| UC1 | `tests/utils/test_confidence_metrics.py::test_confidence_metrics` | 8 | `adapter/internal/prob.TestConfidence` (`internal/prob/confidence_test.go`) | 8 | ported | ported |
| UE1 | `tests/utils/test_error_handling.py::test_status_errors_map_and_preserve_status_and_body` | 18 | `adapter/internal/rest.TestStatusErrorKeepsStatusAndBody` (6 statuses × 3 providers' error bodies) and `adapter.TestFailureClasses` (status → SDK kind) | 18 | ported (DV11) | planned |
| UE2 | `tests/utils/test_error_handling.py::test_timeout_and_connection_errors_map` | 3 | `adapter/internal/rest.TestTransportErrorsClassify` | 3 | ported | planned |
| UE3 | `tests/utils/test_error_handling.py::test_unknown_and_sdk_errors_pass_through` | 3 | none: no translation layer | 0 | deviation DV14 | planned |
| UE4 | `tests/utils/test_error_handling.py::test_translating_context_manager_reraises_translated_error` | 1 | none: no `translating` context manager | 0 | deviation DV14 | planned |
| UE5 | `tests/utils/test_error_handling.py::test_retries_succeed_after_transient_error` | 1 | `adapter.TestRetrySucceedsAfterTransientError` (`retry_test.go`, synctest) | 1 | ported | ported |
| UE6 | `tests/utils/test_error_handling.py::test_non_retryable_error_is_not_retried` | 1 | `adapter.TestNonRetryableErrorIsNotRetried` | 1 | ported | ported |
| UE7 | `tests/utils/test_error_handling.py::test_retries_are_exhausted_and_reasons_recorded` | 1 | `adapter.TestRetriesExhaustedRecordReasons` | 1 | ported | ported |
| UP1 | `tests/utils/test_probability_normalization.py::test_probability_normalization_and_debug_data` | 3 | `adapter/internal/prob.TestNormalizationAndDebugData` (`internal/prob/normalize_test.go`) | 3 | ported | ported |

Items per upstream file: `test_client_with_fake_model.py` 54,
`test_client_with_live_apis.py` 25, `test_gemini_transports.py` 14,
`test_openai_transports.py` 23, `test_provider_lifecycle.py` 68,
`test_provider_nonanswers.py` 136, `test_provider_requests.py` 29,
`test_provider_retries.py` 12, `test_schema.py` 24,
`utils/test_confidence_metrics.py` 8, `utils/test_error_handling.py` 28,
`utils/test_probability_normalization.py` 3; 424 in all.

## Cassettes and expected responses

The 25 cassettes of upstream's `tests/cassettes/test_client_with_live_apis/`
are copied byte for byte into [`../testdata/cassettes/`](../testdata/cassettes),
and the 13 files of `tests/expected_responses/` into
[`../testdata/expected/`](../testdata/expected). Each cassette holds one
recorded exchange and replays in one subtest of the named Go test, whose
subtest name is the bracketed id.

| ID | Cassette | Go test | Expected file | Status |
| --- | --- | --- | --- | --- |
| C1 | `test_live_responses_match_reference_shape[probabilities-native-openai].json` | `adapter.TestReplayReferenceShape` | yes | planned |
| C2 | `test_live_responses_match_reference_shape[probabilities-native-anthropic].json` | `adapter.TestReplayReferenceShape` | yes | planned |
| C3 | `test_live_responses_match_reference_shape[probabilities-native-gemini].json` | `adapter.TestReplayReferenceShape` | yes | planned |
| C4 | `test_live_responses_match_reference_shape[probabilities-prompted-openai].json` | `adapter.TestReplayReferenceShape` | yes | planned |
| C5 | `test_live_responses_match_reference_shape[probabilities-prompted-anthropic].json` | `adapter.TestReplayReferenceShape` | yes | planned |
| C6 | `test_live_responses_match_reference_shape[probabilities-prompted-gemini].json` | `adapter.TestReplayReferenceShape` | yes | planned |
| C7 | `test_live_responses_match_reference_shape[discrete-native-openai].json` | `adapter.TestReplayReferenceShape` | yes | planned |
| C8 | `test_live_responses_match_reference_shape[discrete-native-anthropic].json` | `adapter.TestReplayReferenceShape` | yes | planned |
| C9 | `test_live_responses_match_reference_shape[discrete-native-gemini].json` | `adapter.TestReplayReferenceShape` | yes | planned |
| C10 | `test_live_responses_match_reference_shape[discrete-prompted-openai].json` | `adapter.TestReplayReferenceShape` | yes | planned |
| C11 | `test_live_responses_match_reference_shape[discrete-prompted-anthropic].json` | `adapter.TestReplayReferenceShape` | yes | planned |
| C12 | `test_live_responses_match_reference_shape[discrete-prompted-gemini].json` | `adapter.TestReplayReferenceShape` | yes | planned |
| C13 | `test_live_models_follow_question_instructions_and_criteria[probabilities-native-openai].json` | `adapter.TestReplayFollowsInstructionsAndCriteria` | no | planned |
| C14 | `test_live_models_follow_question_instructions_and_criteria[probabilities-native-anthropic].json` | `adapter.TestReplayFollowsInstructionsAndCriteria` | no | planned |
| C15 | `test_live_models_follow_question_instructions_and_criteria[probabilities-native-gemini].json` | `adapter.TestReplayFollowsInstructionsAndCriteria` | no | planned |
| C16 | `test_live_models_follow_question_instructions_and_criteria[probabilities-prompted-openai].json` | `adapter.TestReplayFollowsInstructionsAndCriteria` | no | planned |
| C17 | `test_live_models_follow_question_instructions_and_criteria[probabilities-prompted-anthropic].json` | `adapter.TestReplayFollowsInstructionsAndCriteria` | no | planned |
| C18 | `test_live_models_follow_question_instructions_and_criteria[probabilities-prompted-gemini].json` | `adapter.TestReplayFollowsInstructionsAndCriteria` | no | planned |
| C19 | `test_live_models_follow_question_instructions_and_criteria[discrete-native-openai].json` | `adapter.TestReplayFollowsInstructionsAndCriteria` | no | planned |
| C20 | `test_live_models_follow_question_instructions_and_criteria[discrete-native-anthropic].json` | `adapter.TestReplayFollowsInstructionsAndCriteria` | no | planned |
| C21 | `test_live_models_follow_question_instructions_and_criteria[discrete-native-gemini].json` | `adapter.TestReplayFollowsInstructionsAndCriteria` | no | planned |
| C22 | `test_live_models_follow_question_instructions_and_criteria[discrete-prompted-openai].json` | `adapter.TestReplayFollowsInstructionsAndCriteria` | no | planned |
| C23 | `test_live_models_follow_question_instructions_and_criteria[discrete-prompted-anthropic].json` | `adapter.TestReplayFollowsInstructionsAndCriteria` | no | planned |
| C24 | `test_live_models_follow_question_instructions_and_criteria[discrete-prompted-gemini].json` | `adapter.TestReplayFollowsInstructionsAndCriteria` | no | planned |
| C25 | `test_live_typesafe_response_matches_reference_shape.json` | `adapter.TestReplayTypeSafeReference` | yes | planned |

Also read by the port: upstream's `tests/conftest.py` (the scrub lists and the
request matcher, ported by `internal/cassette`) and `tests/http.py` (a test
helper; each Go package builds its `http.Response` values in its own tests,
so it is not ported as a function).
