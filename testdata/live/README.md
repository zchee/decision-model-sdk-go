# Live response bodies

Bodies the live TypeSafe API returned to the live tests, kept to check the
decoder against real responses. The live tests write them when run with
`-args -record`
(`livetest/env_test.go`, `writeFixture`):

```sh
DECISION_MODEL_LIVE_TESTS=1 DECISION_MODEL_API_KEY=... \
	DECISION_MODEL_BASE_URL=https://api.typesafe.ai \
	DECISION_MODEL_DEFAULT_MODEL=jev-latest \
	go test -tags live -count=1 -v ./livetest/ -args -record
```

The API key is not on that command line: the SDK reads
`DECISION_MODEL_API_KEY` from the environment. Before a body reaches the disk the recorder replaces
every occurrence of the API key, and of the wrong key
`TestLiveUnauthenticated` sends, with `***`, and it refuses, writing
nothing, a body that still holds a credential shape: a token starting with
`ts_`, a member named like a credential header (`Authorization`,
`Proxy-Authorization`, `X-Api-Key`, `Api-Key`, `Cookie`, `Set-Cookie`, or a
header-style name holding `token` or `secret`), or a bearer credential.
None of these bodies held a key; each is the exact body the SDK read, after
the transport undid the API's gzip encoding.

The files sit in this subdirectory, not beside the other fixtures, so that
the loops over `testdata/*.json` do not read them; one of those loops pins
an allocation count for every body that decodes (`internal/alloctest`'s
`TestAllocDecodeFixtures`). No test pins the allocations of these bodies;
measured before the release, `questions.json` and `typed-response.json`
decode in 4 allocations, as `result.json` does.

| File | Scenario (`livetest`) | Request | Status | Bytes |
| --- | --- | --- | --- | --- |
| `models.json` | `TestLiveModels` | `GET /v1/models` | 200 | 311 |
| `questions.json` | `TestLiveQuestions`: a raw noul with structured criteria, a typed choice, a typed score | `POST /v1/systemone` | 200 | 401 |
| `typed-response.json` | `TestLiveTypedResponse`: `Ask[liveTicket]`, the body taken from the `LevelTrace` "response body" record | `POST /v1/systemone` | 200 | 401 |
| `unauthenticated.json` | `TestLiveUnauthenticated`, a request carrying no credential | `GET /v1/models` without `Authorization` | 403 | 118 |
| `wrong-key.json` | `TestLiveUnauthenticated`, a key the API did not issue | `GET /v1/models` | 401 | 138 |

The first three were recorded at 2026-09-27 02:41:00 JST on 47d2521, the last
two at 02:42:13 JST on 47d2521's tree with `TestLiveUnauthenticated` as
committed beside these files.

## Provider recordings

`livetest.TestLiveProviders` records what each provider of
`DECISION_MODEL_LIVE_PROVIDERS` returns to the SDK's calls through its
preset, under `providers/<name>/`, through the same scrubber. Every body is
the one the vendor sent: for Decisions API and OpenAI, whose presets carry a
dialect, the body before the dialect translated it. It runs alone, since
the other live tests need the generic variables:

```sh
DECISION_MODEL_LIVE_TESTS=1 \
	DECISION_MODEL_LIVE_PROVIDERS=typesafe,codiv,perplexity,decisions-api,openai,openrouter,openrouter-decisions \
	DECISION_MODEL_LIVE_MODEL_TYPESAFE=... DECISION_MODEL_LIVE_MODEL_CODIV=... \
	DECISION_MODEL_LIVE_MODEL_PERPLEXITY=... DECISION_MODEL_LIVE_MODEL_DECISIONS_API=... \
	DECISION_MODEL_LIVE_MODEL_OPENROUTER=typesafe/jev-1.13 \
	DECISION_MODEL_LIVE_MODEL_OPENROUTER_DECISIONS=typesafe/jev-1.13 \
	go test -tags live -count=1 -v -run '^TestLiveProviders$' ./livetest/ -args -record
```

Each provider's client reads its key from the provider's own variable
(`TYPESAFE_API_KEY`, `CODIV_API_KEY`, `PERPLEXITY_API_KEY`,
`DECISIONS_API_KEY`, `OPENAI_API_KEY`, and `OPENROUTER_API_KEY` for both
OpenRouter endpoints); OpenAI's model defaults to `gpt-6-luna`, and
OpenRouter's models are named with their vendor, `typesafe/jev-1.13`.
Before a body is written the recorder replaces the value of
every key variable the environment sets, and of the wrong key, with `***`.
It also refuses a body that holds a member named `user_id`, the account
identifier OpenRouter's 400 bodies carry, or a token starting with `apikey_`, `sk-`
(which OpenRouter's `sk-or-v1-` keys start with too), `sk_` or `pplx-` (Perplexity's model ids `pplx-decider-v1.1-27b` and
`pplx-decider-v1-27b` excepted), or a masked echo of a key it holds: the
key's first 7 characters and its last 4 inside one JSON string, any number
of characters apart. OpenAI echoes a key it refuses as the key's first 8
characters, a star for each of the others but the last 4, and the last 4,
so the gap grows with the key; the wrong key the tests send is the one key
whose masked echo a recording may keep.

| Provider | Files | What each holds |
| --- | --- | --- |
| `typesafe`, `codiv` | `models.json`, `questions.json`, `typed-response.json`, `wrong-key.json` | the listing (200); the answers to the questions of `TestLiveQuestions` and to `Ask[liveTicket]` (200); the refusal of a wrong key on the listing (401) |
| `perplexity` | `questions.json`, `typed-response.json`, `wrong-key.json` | as above, with no listing: the wrong key is sent on `POST /v1/decisions` (401) |
| `decisions-api` | `questions.json`, `typed-response.json`, `wrong-key.json` | the 200 envelope around the answers, which the preset's dialect unwraps (`data.result`); the wrong key on `POST /v1/systemone` (401). Decisions API validates a noul's criteria more strictly than TypeSafe AI and refuses criteria without both the `true` and the `false` key (400), so `questions.json` answers `liveTicket`'s three questions, whose noul has no criteria, instead of `TestLiveQuestions`' |
| `openrouter`, `openrouter-decisions` | `questions.json`, `typed-response.json`, `wrong-key.json` | OpenRouter's two System One endpoints, `POST /api/v1/systemone` and `POST /api/alpha/decisions` (no `/v1`; the vendor marks it alpha), recorded 2026-10-11 03:04 JST: the answers (200) to `liveTicket`'s three questions, in the System One shape with the members `id`, `provider` and `usage.cost` besides, which the decoder ignores; the wrong key on the same path (401, `{"error":{"message","code"}}`). OpenRouter refuses a noul whose criteria name `true` without `false` (400), so, as for Decisions API, `questions.json` answers `liveTicket`'s noul without criteria. No listing is recorded (`GET /api/v1/models` lists OpenRouter's whole chat catalogue in another shape), and no response carries a request id the SDK reads: OpenRouter sends `x-generation-id` (equal to the body's `id`) and `x-provider-name` instead |
| `openai` | `dialect-request.json`, `dialect-response.json`, `refused.json`, `wrong-key.json` | the request the preset's dialect writes for `liveTicket`'s questions and a state of the ticket, and OpenAI's 200 answer to it, both kept from the discovery run (2026-10-10) the dialect was written from, and not recorded again: `TestLiveProviders` checks OpenAI's answers without recording them; the 400 refusal of the System One request, which the SDK no longer sends OpenAI; the answer to the wrong key on `POST /v1/decisions` (401) |

OpenAI's answers are checked live and never recorded: its
`dialect-request.json` and `dialect-response.json` are the record, and the
unit tests of the OpenAI dialect pin their values. A provider without a
directory has not been recorded yet. Each body is
kept exactly as the provider sent it, a trailing newline included:
Perplexity's 401 body ends with one.

Two tests read them on every `go test` run, without the live tag.
`livetest.TestRecordedBodiesHoldNoCredentials` reads every file: exactly
the five above, and under `providers/` exactly each recorded provider's
files, with no credential shape, and no byte or masked echo of
`DECISION_MODEL_API_KEY` or of a provider's key variable when the
environment holds it. `internal/codec.TestLiveBodiesOneScan` globs
`live/*.json` only, so it decodes the five above (each decodes, with the
one-scan and the whole-body traversal agreeing) and not the provider
bodies, several of which are not System One responses.
