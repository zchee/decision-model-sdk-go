# Live response bodies

Bodies the live TypeSafe API returned to W6.4's owner-approved live pass
(plan risk K6: the decoder and its budgets checked against real responses;
critic-p5 C4: the share of real bodies the decode reads in one scan). The
live tests write them when run with `-args -record`
(`livetests/env_test.go`, `writeFixture`):

```sh
TYPESAFE_LIVE_TESTS=1 go test -tags live -count=1 -v ./livetests/ -args -record
```

The API key is not on that command line: the SDK reads `TYPESAFE_API_KEY`
from the environment. Before a body reaches the disk the recorder replaces
every occurrence of the API key, and of the wrong key
`TestLiveUnauthenticated` sends, with `***`, and it refuses, writing
nothing, a body that still holds a credential shape: a token starting with
`ts_`, an `Authorization`, `Proxy-Authorization` or `X-Api-Key` member, or a
bearer credential. None of these bodies held a key; each is the exact body
the SDK read, after the transport undid the API's gzip encoding (ledger
W6.4-05).

The files sit in this subdirectory, not beside the other fixtures, because
the root package's loops over `testdata/*.json` pin an allocation count for
every body that decodes (`TestAllocDecodeFixtures`), and those pins live in a
file another wave owns. Checking the decode budgets on these bodies (K6) is
owed to that wave or to W7.

| File | Scenario (`livetests`) | Request | Status | Bytes |
| --- | --- | --- | --- | --- |
| `models.json` | `TestLiveModels` | `GET /v1/models` | 200 | 311 |
| `questions.json` | `TestLiveQuestions`: a raw noul with structured criteria, a typed choice, a typed score | `POST /v1/systemone` | 200 | 401 |
| `typed-response.json` | `TestLiveTypedResponse`: `Ask[liveTicket]`, the body taken from the `LevelTrace` "response body" record | `POST /v1/systemone` | 200 | 401 |
| `unauthenticated.json` | `TestLiveUnauthenticated`, a request carrying no credential | `GET /v1/models` without `Authorization` | 403 | 118 |
| `wrong-key.json` | `TestLiveUnauthenticated`, a key the API did not issue | `GET /v1/models` | 401 | 138 |

The first three were recorded at 2026-09-27 02:41:00 JST (the pass's start,
from `date`) on 47d2521, the last two at 02:42:13 JST on 47d2521's tree with
`TestLiveUnauthenticated` as committed beside these files (ledger W6.4-01
and W6.4-02).

Two tests read them on every `go test` run, without the live tag:
`livetests.TestRecordedBodiesHoldNoCredentials` (exactly these files, no
credential shape, and no byte of `TYPESAFE_API_KEY` when the environment
holds it) and `internal/codec.TestLiveBodiesOneScan` (each decodes, with the
one-scan and the whole-body traversal agreeing; ledger W6.4-06).
