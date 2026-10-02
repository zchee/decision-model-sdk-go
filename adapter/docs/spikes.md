# Spikes

A spike here is a measurement made before the code it informs was written:
a question about Python, a provider, a tool or a service that reading could
not settle. Each section below gives one question, the result that counts as
a pass, the verdict, what the port does because of it, and one table row per
measurement, so that a reader can run the measurement again.

The answers are about
[system-one-adapter-python](https://github.com/typesafe-ai/system-one-adapter-python)
0.2.1 at commit `e1d4cc938204b22fc5a3c3aca7044072fe3f712d`, the release this
module ports (`UpstreamVersion` and `UpstreamCommit` in
[`../version.go`](../version.go)).

## Hosts

| Host | Description | `go version` |
| --- | --- | --- |
| (M) | darwin/arm64 | `go version go1.27.1 darwin/arm64` |
| (L) | linux/amd64 | `go version go1.27.1 linux/amd64` |

A comparison of floating-point results runs on both hosts against the same
vector files, because the Go compiler may fuse `x*y + z` into one
instruction on arm64 and does not on amd64.

## The Python reference

Every vector file under `testdata/python/` is written by a script in that
directory, run on **CPython 3.14.3** with **pydantic 2.13.4** and
**pydantic-core 2.46.4**:

```sh
uv run --python 3.14.3 --with pydantic==2.13.4 --with pydantic-core==2.46.4 testdata/python/<script>
```

Those are the versions behind upstream's expected responses: the 16
recorded exchanges under [`../testdata/cassettes`](../testdata/cassettes)
that carry an `x-stainless-runtime-version` header all say `3.14.3`, and
upstream's `uv.lock` pins the two pydantic versions. Each vector file names
the three versions in its header.

The patch release matters because the result of `sum()` over floats depends
on the CPython release, and upstream normalises probabilities with `sum()`.
Measured on (M) at 2026-10-02T04:38:38Z: `sum([0.7, 0.2, 0.1])` is
`0.9999999999999999` on CPython 3.11.15 and `1.0` on CPython 3.14.3.

## Row format

| Column | Content |
| --- | --- |
| Row | the spike and a number |
| Date (UTC) | when the command started, from `date` |
| Host | (M) or (L); `CI` for a GitHub-hosted runner |
| Toolchain | the Go release, or the Python and package versions, and the tool's version |
| Command | what was run; a `go` command runs in `adapter/`, a script path is relative to `adapter/` |
| Result | what the command printed, reduced to the numbers the verdict rests on |

## S1: the schema writer and the request bodies

**Question.** Does a Go schema writer reproduce upstream's schema and
request bodies?

**Pass.** Each of the 24 recorded provider exchanges passes every comparison
that exists for it. A native request carries a schema object and no schema
text, and a prompted request carries the text and no object. So the native
schema is compared with the recorded one as a JSON value for the 12 native
recordings, the schema text is compared byte for byte for the 12 prompted
ones, and the whole request body is compared as a JSON value for all 24.

**Verdict.** Pass: 12 of 12, 12 of 12 and 24 of 24 on both hosts, with the
standard library's JSON code and with the JSON library's own
(`GOEXPERIMENT=nojsonv2`).

**What the port does because of it.** `internal/schema` writes the schema
from a typed tree in pydantic's order: keywords sorted by name, the members
of `$defs` sorted by name, the members of `properties` in question and
label order. The order comes from the tree, not from dumping a map.

The 24 recordings are necessary and not enough. Four of the writer's rules
can each be removed and all 24 still pass:

1. the key order of a property that is itself named `properties` or
   `default`, which keeps pydantic's generation order (`type` before
   `description`) instead of the sorted one;
2. `inspect.cleandoc` on the description of a probability map (tabs
   expanded, the common margin and trailing empty lines removed);
3. the order of `$defs`, which is sorted by name and not left in insertion
   order;
4. the replacement of `<` and of `>` in the state text, each on its own.

Requests built by upstream's own code, without a provider call, do pin
them: 52 of 52 such cases equal the writer's output, and removing one rule
fails between 2 and 12 of them. So the schema package will also be tested
against cases generated from upstream's code, besides the recordings.

In four native recordings (`probabilities-native` for OpenAI and Anthropic)
the recorded schema has another key order than the writer's, because
upstream's vendor SDKs reordered it before sending; the values are equal.
[`deviations.md`](deviations.md) has this as DV13.

| Row | Date (UTC) | Host | Toolchain | Command | Result |
| --- | --- | --- | --- | --- | --- |
| S1-1 | 2026-10-02T04:46:28Z | (M) | go1.27.1 darwin/arm64 | `go test -count=1 -race ./...` in a module outside this one that holds the writer, reading `testdata/cassettes`; its comparison is the seed of the schema package's tests | recordings: native schema as a value 12/12, prompted text as bytes 12/12, body as a value 24/24. Cases built by upstream's code (13 requests, both answer modes, both output modes; schema and prompt texts as bytes, bodies as values): 52/52 |
| S1-2 | 2026-10-02T04:46:28Z | (M) | go1.27.1 darwin/arm64, `GOEXPERIMENT=nojsonv2` added | the same test | the same: 12/12, 12/12, 24/24; 52/52 |
| S1-3 | 2026-10-02T04:46:36Z | (L) | go1.27.1 linux/amd64 | the same test, the same files | the same: 12/12, 12/12, 24/24; 52/52 |
| S1-4 | 2026-10-02T04:46:36Z | (L) | go1.27.1 linux/amd64, `GOEXPERIMENT=nojsonv2` | the same test | the same: 12/12, 12/12, 24/24; 52/52 |
| S1-5 | 2026-10-02T04:46:32Z | (M) | jq, `cmp` | the writer's output written to files and compared with the recordings by `jq -S` and `diff` for the values and by `cmp` for the prompted text, without the test's own comparison | 12/12, 12/12, 24/24 |
| S1-6 | 2026-10-02T04:46:32Z | (M) | go1.27.1 darwin/arm64 | the test of S1-1 three times, each with one deliberate difference: one bit of one question's instructions changed; the last question dropped; one character added to the model name | each run fails for 24 of 24 recordings; the first two fail every comparison, the third only the body |
| S1-7 | 2026-10-02T04:46:33Z | (M) | go1.27.1 darwin/arm64 | the test of S1-1 four times, each with one rule of the writer removed: the generation order under `properties` and `default`; `inspect.cleandoc`; the sort of `$defs`; the `<` replacement in the state text | the recordings pass 24/24 every time; of the 52 cases built by upstream's code, 8, 2, 2 and 12 fail |

## S2: Python's `sum()` of floats

**Question.** Can Go reproduce the reference CPython's `sum()` of floats bit
for bit?

**Pass.** A Go port of CPython 3.14.3's float summation gives the same bits
as `sum()` on 100 000 generated vectors, and the score and confidence
formulas built on it give the same bits as upstream's, on both hosts.

**Verdict.** Pass: on both hosts 0 of 100 000 sums and 0 of 1 100 000
formula results differ from CPython's, and no vector had to be skipped. On
(L) the same holds when the compiler targets `GOAMD64=v3`.

**What the port does because of it.** `internal/prob` ports CPython's
summation instead of writing `s += x`: that plain loop gives other bits than
`sum()` on 19 941 of the 100 000 vectors, and on 3 964 of the 18 174 that
look like probabilities. The port follows four rules, each read from
`Python/bltinmodule.c` at the tag `v3.14.3` and each needed for equality:

1. The first element enters as `0.0 + x`, because `sum()` starts from the
   integer 0. A leading `-0.0` therefore becomes `+0.0`.
2. The sum is compensated (Neumaier's form), and the compensation is added
   to the result only when it is nonzero and finite. Otherwise a sum that is
   infinite or has overflowed would turn into NaN.
3. Every product that feeds a sum is written `float64(a * b)`. The Go
   specification lets a compiler fuse `x*y + z` into one instruction that
   rounds once, and an explicit conversion forbids it. Python rounds the
   product and then the addition. Without the conversion the expected score
   differs on 9 564 of the 100 000 vectors on (M), on none on (L) with the
   default `GOAMD64=v1`, and on 4 526 on (L) with `GOAMD64=v3`. So the
   conversion must stay although removing it changes no test on a default
   amd64 build.
4. Python's `max` keeps its first candidate unless a later one is strictly
   greater, so `max(0.0, y)` is `+0.0` when `y` is NaN or `-0.0`. Go's
   builtin `max`, `math.Max` and `slices.Max` return NaN when they meet
   one. The port uses a loop with `>`.

The summation code of `bltinmodule.c` is the same at CPython 3.14.3 and
3.14.6 apart from comments, and the 100 000 rows come out equal on both.

`testdata/python/sum_vectors.tsv` holds the first 1 136 rows of that run:
every vector class at every length from 1 to 20. Each float in it is the 16
hexadecimal digits of its bit pattern. Its header line `# python:` is the
interpreter's whole `sys.version`, build date and compiler included, so the
file's bytes reproduce with the build of CPython 3.14.3 named there; another
build of 3.14.3 writes the same rows under a different header line.

| Row | Date (UTC) | Host | Toolchain | Command | Result |
| --- | --- | --- | --- | --- | --- |
| S2-1 | 2026-10-02T04:59:28Z | (M) | CPython 3.14.3, pydantic 2.13.4, pydantic-core 2.46.4 | `uv run --python 3.14.3 --with pydantic==2.13.4 --with pydantic-core==2.46.4 testdata/python/gen_sum_vectors.py` | writes `testdata/python/sum_vectors.tsv`: 1 136 vectors, 569 992 bytes, sha256 `9afc76be5a48efc41fe42f8dcbd32b5355ccc5143b4b6227a3cd75901c1056fb`, the committed file; a second run gives the same bytes |
| S2-2 | 2026-10-02T04:59:29Z | (M) | the same | the same with `--output <file> --upstream-src <upstream>/src/system_one_adapter/_utils`, which computes every row with upstream's own functions as well | the same bytes: the script's formulas are upstream's |
| S2-3 | 2026-10-02T04:42:25Z | (M) | the same | the same as S2-1 with `--count 100000 --output <file>` | 100 000 vectors, 51 168 405 bytes, sha256 `acab33827049ed38cab485062ad7d295d64728b89e6e9d942994df9203f890ff`; a second run gives the same bytes |
| S2-4 | 2026-10-02T04:42:25Z | (M) | CPython 3.14.6, pydantic 2.13.4, pydantic-core 2.46.4 | the same as S2-3 with `--python 3.14.6` | the 100 000 rows equal those of S2-3; only the header line differs |
| S2-5 | 2026-10-02T04:40:21Z | (M) | go1.27.1 darwin/arm64 | a Go port of the summation and of the formulas, in a module outside this one, tested against the file of S2-3 | sums: 0 of 100 000 differ. Formula results: 0 of 1 100 000 differ. Skipped: 0 |
| S2-6 | 2026-10-02T04:41:31Z | (L) | go1.27.1 linux/amd64 | the same test, the same file | the same |
| S2-7 | 2026-10-02T04:41:56Z | (L) | go1.27.1 linux/amd64, `GOAMD64=v3` | the same test, the same file | the same |
| S2-8 | 2026-10-02T04:40:21Z | (M) | go1.27.1 darwin/arm64 | in that test, a plain `s += x` loop against the file of S2-3 | differs on 19 941 of 100 000 vectors |
| S2-9 | 2026-10-02T04:41:31Z | (L) | go1.27.1 linux/amd64 | the same | differs on 19 941 of 100 000 vectors |
| S2-10 | 2026-10-02T04:40:21Z | (M) | go1.27.1 darwin/arm64 | in that test, the expected score with its products not written as `float64(a * b)` | differs on 9 564 of 100 000 vectors |
| S2-11 | 2026-10-02T04:41:31Z | (L) | go1.27.1 linux/amd64 | the same | differs on 0 of 100 000 vectors; on 4 526 at `GOAMD64=v3` (2026-10-02T04:41:56Z) |
| S2-12 | 2026-10-02T04:41:55Z | (M) | go1.27.1 darwin/arm64 | the test of S2-5 against a copy of `sum_vectors.tsv` with one bit of one expected sum changed | fails and names the row: 1 of 1 136 differs |

## S3: `repr(float)` and `pydantic_core.to_json`

**Question.** Can Go reproduce Python's `repr` of a float and
pydantic-core's `to_json`, the two spellings upstream writes?

**Pass.** Two Go float writers give the same bytes as `repr` and as
`to_json` on 100 000 floats, and the Go re-encoding of a state gives the
same bytes as `to_json(json.loads(document))` on 10 000 generated JSON
documents, on both hosts; or each differing class is listed with a count.

**Verdict.** pending

| Row | Date (UTC) | Host | Toolchain | Command | Result |
| --- | --- | --- | --- | --- | --- |

## S4: the seam with the Adapter's own error and report types

**Question.** Through the released TypeSafe SDK, does each failure class
reach the caller as intended: can `errors.As` find the Adapter's error
behind the SDK's timeout and connection errors, can the Report be read back
from the SDK's error bodies, and how many attempts does the SDK's default
retry policy make for each class?

**Pass.** Each case behaves as the design of the seam says, or the design is
corrected.

**Verdict.** Pass: 38 cases over the 15 failure classes, each under
`NoRetry()` and under `DefaultRetry()`, behave as designed on both hosts,
and the two hosts' observations are equal byte for byte.

**What the port does because of it.** The failure classes stay as designed.

- A provider timeout and a connection failure leave `RoundTrip` as the
  Adapter's own error. The SDK wraps it in its `*typesafe.TimeoutError` or
  `*typesafe.ConnectionError`, and `errors.As` reaches the Adapter's error,
  and through it the Report, in both. The provider's own error is not kept
  in the chain, so a URL that holds a key cannot be printed from it.
- A Report is carried by exactly the classes that end after an evaluation
  has started: a provider status, a provider timeout, a connection failure,
  a passed deadline, a non-answer, any other provider error, and malformed
  output. It is read from the body the SDK keeps (`APIError.Body`,
  `ResponseValidationError.Body`) or from the Adapter's error. A call that
  ends before an evaluation starts has none: an invalid body, invalid
  questions or state, a model or provider that cannot be resolved or built,
  the Adapter's own encoding failure, an unknown path, a closed Adapter, a
  cancelled call.
- A non-answer, any other provider error and the Adapter's own encoding
  failure answer status 424, and malformed output answers 200 with
  `"answers": null`. A client with `DefaultRetry()` makes one attempt for
  each, so it does not run a billed evaluation again.
- A client with `DefaultRetry()` does run the evaluation again, three
  attempts in all, for a provider status of 408, 429 or 500 to 599 (529
  included; 409 is not retried), a provider timeout, a connection failure
  and the SDK's own per-attempt timeout. The caller gets the last attempt's
  Report only, and its `debug.sdk_retry_count` of 2 says that two
  evaluations ran before it. The client that `NewClient` builds does not
  retry.
- `TimeoutError.Timeout` is the SDK's per-attempt timeout, whatever timed
  out: zero under `WithNoTimeout`, which is what the Adapter's own client
  sets. So a provider timeout and a caller's passed deadline look the same
  in that field; `ctx.Err()` or the Report tells them apart.
- A cancelled call returns `context.Canceled` itself, with no SDK error
  type around it and no Report, even when `RoundTrip` returned one.

| Row | Date (UTC) | Host | Toolchain | Command | Result |
| --- | --- | --- | --- | --- | --- |
| S4-1 | 2026-10-02T04:40:41Z | (M) | go1.27.1 darwin/arm64, `github.com/zchee/typesafe-sdk-go` v0.1.1 | `go test -count=1 -race -v ./...` in a module outside this one, which requires the SDK at v0.1.1 from the module proxy and puts stand-ins for the Adapter's error and report types behind `typesafe.WithRoundTripper`; the test is the seed of this module's `TestSeamContract` | ok; 76 subtests, 38 cases under each of the two retry policies, 0 failed |
| S4-2 | 2026-10-02T04:41:20Z | (L) | go1.27.1 linux/amd64, the same SDK version | the same test, the same files | ok; 0 failed; the 80 observation lines equal (M)'s byte for byte |
| S4-3 | 2026-10-02T04:41:01Z | (M) | as S4-1 | the same test twice with one expectation or one stand-in changed: the expected attempts of the non-answer class set to 3; the Adapter's error text made to hold the SDK's key | fails both times: 2 subtests for the wrong count; 16 subtests for the key, because the SDK then replaces the cause and `errors.As` no longer reaches the Adapter's error |
| S4-4 | 2026-10-02T04:41:38Z | (L) | as S4-2 | the same two changed runs | fails both times, the same 2 and 16 subtests |

## S5: license detection by pkg.go.dev

**Question.** Does pkg.go.dev detect the module's license?

**Pass.** The library pkg.go.dev uses detects Apache-2.0 in
[`../LICENSE`](../LICENSE) and MIT in
[`../LICENSE-UPSTREAM`](../LICENSE-UPSTREAM), each over at least 90 % of the
file.

**Verdict.** Pass: Apache-2.0 over 100 % of `LICENSE`, MIT over 98.82 % of
`LICENSE-UPSTREAM`.

**What the port does because of it.** Nothing changes. pkg.go.dev's license
policy page names `github.com/google/licensecheck` as its detector and lists
the file names it reads; pkgsite's source sets the threshold, 75 % of the
file. `LICENSE` is one of the names, so pkg.go.dev shows Apache-2.0 for the
module and with it the documentation: pkgsite's own detector, run over this
module's files, finds that one license at 99.25 % by its scanner, which
knows more license texts than the library's built-in set. `LICENSE-UPSTREAM`
is not one of the names, so pkg.go.dev does not read it and its license tab
does not list the MIT text. The notice still ships: a module zip holds every
file of the module, `LICENSE-UPSTREAM` among them, which is what the MIT
license asks of a copy.

| Row | Date (UTC) | Host | Toolchain | Command | Result |
| --- | --- | --- | --- | --- | --- |
| S5-1 | 2026-10-02T04:33:39Z | (M) | curl | `curl -fsSL https://pkg.go.dev/license-policy` | HTTP 200. The page names `github.com/google/licensecheck`, lists 44 license file names matched without regard to case (`LICENSE` is one; `LICENSE-UPSTREAM` is not), and names no threshold |
| S5-2 | 2026-10-02T04:33:48Z | (M) | curl | `curl -fsSL https://raw.githubusercontent.com/golang/pkgsite/b0feb34c6d91fdea7d471ec6026383042ba8aa12/internal/licenses/licenses.go` | `coverageThreshold = 75`; a file whose name is not in the list is not read. The `go.mod` of the same commit requires licensecheck v0.3.1 |
| S5-3 | 2026-10-02T04:34:19Z | (M) | go1.27.1 darwin/arm64, licensecheck v0.3.1 | `go run . LICENSE LICENSE-UPSTREAM`, where `.` is a program outside this module that prints the result of `licensecheck.Scan` for each file | `LICENSE`: Apache-2.0, 100.00 %, bytes 0 to 11357 of 11357. `LICENSE-UPSTREAM`: MIT, 98.82 %, bytes 13 to 1068 of 1068; the 13 bytes before the match are the title line |
| S5-4 | 2026-10-02T04:36:19Z | (L) | go1.27.1 linux/amd64, licensecheck v0.3.1 | the same program on the same two files | the same |
| S5-5 | 2026-10-02T04:34:19Z | (M) | go1.27.1 darwin/arm64, licensecheck v0.3.1 | the same program on two files that are not a license: the first 5000 bytes of `LICENSE` followed by 9000 bytes of prose, and 1068 bytes of prose | 0.00 % and no match for both, so the scan can fail the pass line |
| S5-6 | 2026-10-02T04:36:19Z | (L) | go1.27.1 linux/amd64, licensecheck v0.3.1 | the same program on the same two files that are not a license | the same |
| S5-7 | 2026-10-02T04:52:55Z | (M) | go1.27.1 darwin/arm64, pkgsite at commit `b0feb34c6d91` | `go test -run TestAdapterTree ./internal/licenses/` in a clone of `golang/pkgsite`, with a test added there that runs pkgsite's own detector (`NewDetectorFS`) over this module's files | the module is redistributable; the detector lists one license file, `LICENSE`, Apache-2.0, 99.25 % by pkgsite's scanner, and does not list `LICENSE-UPSTREAM` |

## S6: the reach of the repository's `golangci-lint run`

**Question.** Does `golangci-lint run` at the repository root reach
`adapter/`, and does the adapter pass the repository's lint configuration?

**Pass.** The reach is recorded, whichever it is.

**Verdict.** Recorded: the root run does not reach `adapter/`, and the
adapter passes the root's configuration with 0 issues. The result is the
same with golangci-lint v2.13.2 and with v2.14.0, the release the
repository's workflows installed when this was measured.

**What the port does because of it.** The module is linted by its own
workflow, `.github/workflows/adapter.yaml`, which runs
`golangci-lint run --config ../.golangci.yaml ./...` in `adapter/`: one rule
set for the repository, two runs. The root's run is given the packages of
`./...` in the root module, and `go list ./...` there stops at
`adapter/go.mod`. The root's
`golangci-lint fmt --diff` is different: it walks every tracked Go file, so
it checks the adapter's formatting as well.

| Row | Date (UTC) | Host | Toolchain | Command | Result |
| --- | --- | --- | --- | --- | --- |
| S6-1 | 2026-10-02T04:34:50Z | (M) | go1.27.1 darwin/arm64, golangci-lint v2.13.2 | `golangci-lint run --verbose` at the repository root | exit 0, `0 issues.`; no output line names `adapter`; `go list ./...` at the root gives 17 packages, none under `adapter/` |
| S6-2 | 2026-10-02T04:35:15Z | (M) | the same | `golangci-lint run --config ../.golangci.yaml ./...` | exit 0, `0 issues.` |
| S6-3 | 2026-10-02T04:35:26Z | (M) | the same | `golangci-lint run` at the repository root of a copy with an unchecked `os.Chdir` error planted in a new file `adapter/planted.go` | exit 0, `0 issues.`: the root run does not see the file |
| S6-4 | 2026-10-02T04:35:26Z | (M) | the same | `golangci-lint run --config ../.golangci.yaml ./...` in `adapter/` of that copy | exit 1, `adapter/planted.go:9:26: Error return value of os.Chdir is not checked (errcheck)` |
| S6-5 | 2026-10-02T04:35:29Z | (M) | the same | `golangci-lint run` at the repository root of a copy with the same file planted in the root package | exit 1, `planted.go:9:26: Error return value of os.Chdir is not checked (errcheck)`: the root run reports in its own module what it does not report under `adapter/` |
| S6-6 | 2026-10-02T04:51:55Z | (M) | go1.27.1 darwin/arm64, golangci-lint v2.14.0 | the five commands of S6-1 to S6-5 (the first without `--verbose`), on the same copies | the same exit codes and the same two `errcheck` lines: 0, 0, 0, 1, 1 |
| S6-7 | 2026-10-02T04:51:59Z | CI | go1.27.1 linux/amd64, golangci-lint v2.14.0 | `gh run view <run> --log` for the two lint jobs at the commit the measurements above were made on | both jobs print `Requested golangci-lint 'latest', using 'v2.14.0'` and `0 issues.`: the root's job runs `golangci-lint run` at the repository root, the adapter's runs `golangci-lint run --path-mode=abs --config ../.golangci.yaml` in `adapter/` |

## S7: pydantic's strict-mode verdicts

**Question.** Which answers does pydantic accept, and which does it refuse,
when upstream's generated models validate an edge input: duplicate member
names, `-0`, `1e0`, `1.0` for an integer, `1` for a number, `true` for a
number, huge integers, a `NaN` token, a byte order mark, trailing data?

**Pass.** A table of verdicts, complete for those inputs and for every
model each applies to, that the Go validator must match.

**Verdict.** pending

| Row | Date (UTC) | Host | Toolchain | Command | Result |
| --- | --- | --- | --- | --- | --- |

## S8: Codecov with two modules

**Question.** Does Codecov accept a configuration that measures the SDK and
the adapter apart, as two components with their own statuses, and does it
map the adapter's coverage profile to paths under `adapter/`?

**Pass.** Codecov's validator accepts `.codecov.yaml`, and the first upload
of the adapter's profile shows paths under `adapter/`.

**Verdict.** Half measured. Measured: the validator accepts `.codecov.yaml`
and understands the three keys the configuration depends on (`project: off`,
`name_prefix` in a component status, the negated path `"!adapter/**"`), and
Codecov posts one status per component and kind under these six names:
`codecov/project/default-sdk`, `codecov/project/goal-sdk`,
`codecov/patch/sdk`, `codecov/project/default-adapter`,
`codecov/project/goal-adapter`, `codecov/patch/adapter`. Not measured: the
mapping of the module's files to paths under `adapter/`. At the commit this
was run on, the module had no statement outside its test files, so its
coverage profile was the single line `mode: atomic` and no file of it
reached a report. The mapping is measured when the module has its first
statements.

**What was seen.** The upload step succeeded on all three runner images
every time; it only hands the file over. Codecov refused five of the
adapter's six uploads for the commit with the error code `REPORT_EMPTY`.
The sixth has no error recorded and never left the state `started`; why is
not known. The commit's report holds the SDK's 50 files and none under
`adapter/`; the component `sdk` has a value and the component `adapter` has
none.

`.codecov.yaml` asks Codecov to wait for six uploads, and Codecov counts
the sessions of the commit's merged report. The sessions of one commit add
up across workflow runs. After the first CI run the report had three
sessions, and for the 26 minutes that followed no status existed. A second
CI run of the same commit, the one a push to `main` starts, added three
more: its last upload was merged at 05:14:31Z, and the six statuses were
posted from 05:14:34Z to 05:14:37Z, all `success`. So the six were reached
by the SDK's uploads of two runs while every adapter upload was refused or
stuck. This agrees with Codecov's documentation and with its published
worker source, which compare the number with the report's sessions; that
the hosted service runs that published commit cannot be read from outside.

While the module has no statement, its two project statuses say `No
coverage information found on head` and its patch status says `Coverage
not affected when comparing` the two commits; all three are `success`,
because the configuration sets `if_not_found: success`.

**What the port does because of it.** `.codecov.yaml` stays as it is, and no
`fixes:` entry is added on a guess. At the head of a branch that has not
landed, one CI run gives three merged sessions and Codecov posts no status,
so the SDK's coverage there is read from Codecov's API (the component
`sdk`). The statuses appear with a second CI run of the same commit, the
one a landing on `main` starts. Once the adapter's profile holds statements,
one CI run and one adapter run of a commit are expected to give the six
sessions between them. That is not measured yet, and one of the adapter's
uploads here stalled in `started` without an error, so a run can also come
out one session short.

| Row | Date (UTC) | Host | Toolchain | Command | Result |
| --- | --- | --- | --- | --- | --- |
| S8-1 | 2026-10-02T04:35:53Z | (M) | curl | `curl -fsSL https://docs.codecov.com/docs/codecov-yaml` | the section "Validate your repository YAML" names the endpoint, `curl --data-binary @codecov.yml https://codecov.io/validate`, and says that an invalid file is answered with status 400 |
| S8-2 | 2026-10-02T04:35:54Z | (M) | curl | `curl -sS -X POST --data-binary @.codecov.yaml https://codecov.io/validate` at the repository root | HTTP 200, `Valid!`, then the configuration as Codecov reads it: `project: off` and `patch: off` as `false`; `name_prefix` as written, `default-` and `goal-`; the component paths `"!adapter/**"` and `adapter/**` as `!(?s:adapter/.*)\Z` and `(?s:adapter/.*)\Z` |
| S8-3 | 2026-10-02T04:36:05Z | (M) | curl | the same on three changed copies, one change each: `name_prefix` misspelt, `project: maybe`, a number in place of a path | HTTP 400 each, with the place of the error: the validator refuses an unknown key, a wrong value and a wrong type, so its `Valid!` is a result |
| S8-4 | 2026-10-02T05:06:34Z | (M) | curl | `curl -sS -X POST --data-binary @.codecov.yaml https://api.codecov.io/validate`, the address the same documentation page links | HTTP 200, `Valid!`, the same answer as S8-2 byte for byte |
| S8-5 | 2026-10-02T04:42:48Z | CI | codecov-action v7 | `gh run view <run> --log`, the upload step in the three test jobs of the adapter workflow's first run | each job: `Found 1 coverage files to report`, `Upload queued for processing complete`; the step succeeds |
| S8-6 | 2026-10-02T04:43:49Z | (M) | curl | `curl -sS https://api.codecov.io/api/v2/github/zchee/repos/typesafe-sdk-go/commits/<commit>/uploads/`, and the uploads' error codes from `https://api.codecov.io/graphql/gh`, both without a token | 6 uploads for the commit so far. Of the three with the flag `adapter`, two are in state `error` with the code `REPORT_EMPTY`; the third has no error recorded and stays in `started` |
| S8-7 | 2026-10-02T04:54:35Z | (M) | curl | `curl -sS 'https://api.codecov.io/api/v2/github/zchee/repos/typesafe-sdk-go/report/?sha=<commit>'` and the same address with `components/` | after the SDK's three uploads are merged: 50 files, none under `adapter/`, 3 sessions; component `sdk` 98.17 %, component `adapter` without a value |
| S8-8 | 2026-10-02T04:55:44Z | (M) | gh | `gh api repos/zchee/typesafe-sdk-go/commits/<commit>/status`, 7 minutes after the three uploads of the first CI run were merged | state `pending`, 0 statuses |
| S8-9 | 2026-10-02T05:06:33Z | (M) | curl | `curl -fsSL https://docs.codecov.com/docs/notifications.md`, and `apps/worker/tasks/notify.py` and `upload_finisher.py` of `codecov/umbrella` at commit `90fc7dc04cd3` | the page: `after_n_builds` delays notifications "until a certain number of uploads have been received and processed". The source compares `after_n_builds` with the number of sessions in the commit's report (`notify.py` lines 805 to 820, `upload_finisher.py` lines 827 to 840) and sends nothing while it is larger: here 6 against 3 |
| S8-10 | 2026-10-02T05:22:49Z | (M) | curl | the two reads of S8-6 and the commit's totals, after a second CI run and a second adapter run of the same commit | 12 uploads. The six from the two CI runs are merged, the last at 05:14:31Z; the report has 6 sessions, 50 files, none under `adapter/`. Of the six with the flag `adapter`, five are `error` with `REPORT_EMPTY` and one is still `started` |
| S8-11 | 2026-10-02T05:22:49Z | (M) | gh | `gh api repos/zchee/typesafe-sdk-go/commits/<commit>/status` | state `success`, 6 statuses, posted from 05:14:34Z to 05:14:37Z. `codecov/project/default-sdk`: `98.1% (target 85.0%)`. `codecov/project/goal-sdk`: `98.1% (target 90.0%)`. `codecov/patch/sdk` and `codecov/patch/adapter`: `Coverage not affected when comparing` the parent and the commit. `codecov/project/default-adapter` and `codecov/project/goal-adapter`: `No coverage information found on head` |
