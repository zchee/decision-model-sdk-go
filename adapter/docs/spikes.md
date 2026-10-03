# Spikes

The module was renamed on 2026-10-03 (see [CHANGELOG.md](../../CHANGELOG.md)) and
its version restarted at v0.1.0. The commands and outputs below name the
module, its packages and the SDK identifier by their current names; a
version named in an entry written before that date is a release made under
the earlier module path, and a tag such an entry names is a tag of that
time.

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
instruction on arm64, and on amd64 when `GOAMD64` is `v3` or higher; at the
default `v1` it does not.

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
| S1-8 | 2026-10-02T11:26:25Z | (M) | CPython 3.14.3, pydantic 2.13.4, pydantic-core 2.46.4, system-one-adapter 0.2.1, uv 0.12.19 | `uv run --python 3.14.3 --with pydantic==2.13.4 --with pydantic-core==2.46.4 testdata/python/gen_schema_cases.py` | writes `testdata/python/schema_cases.jsonl`: 58 requests, 34 that upstream accepts, each with its schema text in both answer modes and its user message, and 24 that it refuses, each with the refusal; 139 100 bytes, sha256 `691ec2578380b3dfa46c105bc5addd2bef03784c9e812c97954d46f82705d5e9`, the committed file; a second run gives the same bytes. Upstream's release from the package index provides everything the script calls, so it takes no checkout of upstream |
| S1-9 | 2026-10-02T11:26:26Z | (M) | the same | `testdata/python/gen_schema_cases.py --check`, which runs through the script's first line, `uv run --script`, and its inline list of 14 pinned packages | `schema_cases.jsonl: equal to fresh cases` |

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

**Verdict.** Pass with deviations. The floats are equal: `ReprFloat` and
`PydanticFloat` give Python's bytes for 100 000 of 100 000 doubles and for
the 6 000 committed ones, on both hosts and on both JSON code paths. The
documents differ in three classes. Of 10 000 generated documents, 9 979
agree (9 784 with equal bytes, 195 refused by both) and 21 differ; of the
1 500 committed ones, 1 482 agree (1 302 equal, 180 refused by both) and 18
differ. Every differing document is one that Python accepts and the port
refuses, and each is explained by its class.

The reference for a document is
`pydantic_core.to_json(json.loads(document.decode("utf-8")))`, followed by
upstream's replacement of every `<` and `>` with its escape. The bytes are
decoded as UTF-8 first because the state arrives inside the request body,
which is JSON in UTF-8 and is parsed whole before the state is written
again; a document that is not UTF-8 is refused by both sides.

**What the port does because of it.** `internal/jsonx` has two float
writers over Go's shortest digits (`strconv`), which equalled Python's on
every vector. `PydanticFloat` writes positional notation for
1e-5 <= |x| < 1e16, `.0` after an integral value, and otherwise an exponent
with its sign and no padding (`1e+16`, `1e-7`), as pydantic-core's writer
does. `ReprFloat` writes positional notation for 1e-4 <= |x| < 1e16 and
otherwise an exponent of at least two digits (`1e-05`), as `repr` does; its
spelling of NaN and the infinities is `repr`'s and is not JSON.

The state is written again as Python does it: a repeated member name keeps
its first position and its last value, strings use pydantic-core's escapes
with lowercase hex, and the output is compact. Two limits of the Python
side are needed for equality:

- pydantic-core refuses, when it writes, a value nested deeper than 255
  levels, a scalar counted as a level, so a too-deep value that a later
  member of the same name replaces is not refused;
- `json.loads` refuses, when it reads, an integer of more than 4300 digits,
  the sign not counted, also inside a value that is replaced.

Three nesting limits appear in this ledger and are different things: 255
is pydantic-core's when it writes the state; 10 000 is the Go JSON
library's when it reads; 200 (S7) is the limit of the JSON parser pydantic
uses on the model's output.

The three classes where Python accepts and the port refuses:

- `nan-token` (10 of the 10 000; 10 committed): the tokens `NaN`,
  `Infinity` and `-Infinity`, which `json.loads` reads and JSON does not
  have. Through the root SDK such a state reaches the wire only as a
  `RawJSON` value that is not JSON; the SDK's own encoders refuse a NaN
  before sending. The Adapter's parse of the body refuses it before the
  state is looked at.
  The port refuses it, a deviation from upstream that belongs in
  [`deviations.md`](deviations.md).
- `replaced-lone-surrogate` (10; 7 committed): an escaped surrogate
  without its partner inside a value that a later member of the same name
  replaces. Python's dict drops that value before `to_json` would refuse
  it; a JSON reader refuses it while reading. It reaches the wire as
  `RawJSON`, as `decision.JSON` content or from a caller's `Marshaler`,
  always with a duplicate name the caller wrote. Upstream itself cannot be
  given such a document, because a Python dict has no duplicate names.
  Reading the body with invalid UTF-8 allowed, and decoding each string
  itself so that a lone surrogate is refused only where it is written,
  would bring the class to 0; reading with invalid UTF-8 allowed must stay
  off.
  The port refuses it, a deviation from upstream that belongs in
  [`deviations.md`](deviations.md).
- `read-depth` (2; 2 committed): a replaced value nested past the JSON
  library's own limit: more than 10 000 arrays and objects open inside one
  another, the root among them, a scalar not counted. The number is
  `maxNestingDepth` in `jsontext/state.go` (line 53) of
  `github.com/go-json-experiment/json v0.0.0-20260820222146-c27c302e5fc3`
  and of go1.27.1's `encoding/json/jsontext`; Python's limit is the
  interpreter's stack. Measured by a review of this result against the SDK
  v0.1.1: a `RawJSON` or `decision.JSON` state that opens 10 001
  containers is sent as written and is one container deeper in the body,
  so through the seam a state opens at most 9 999; a Go value of nested
  slices is refused before sending at 9 999.
  The port refuses it, a deviation from upstream that belongs in
  [`deviations.md`](deviations.md).

One document is in the first two classes, so the 21 are 9 + 9 + 1 + 2.

The boundary of `read-depth` is in the committed file: 10 000 containers
give equal bytes and 10 001 are refused, once with arrays alone and once
with objects around a number. An earlier count saw a difference of one
between arrays and objects; it came from shapes that ended differently
(`[]` against `1`) and from a reader that counted a scalar as a level and
so was one level stricter than its library. The port checks the limit when
a container is opened. The 10 000 is read from the library. A library
version with a lower limit fails those boundary rows; one with a higher
limit passes them, because the port's own check stays at 10 000, and no row
shows the change. So the port's constant is compared with the library's
whenever the library is updated.

The class `two-defects` (14 committed documents) marks a document that both
sides refuse while the kind of error may differ, because a JSON reader
refuses three things while reading that Python refuses later or not at all:
a lone surrogate escape, a NaN token, and nesting past the reader's limit,
each together with another defect. Only the refusal is the same for these,
not its kind.

Not in the vectors: a surviving value past the reader's limit (both refuse;
a row would cost about 27 KB, and a review's document sets hold it); an
oversized integer directly followed by a malformed fraction or exponent,
which both refuse while the generator gives no class; floats other than
IEEE 754 doubles; `to_json` with any argument upstream does not pass;
nesting deep enough for `json.loads` to exhaust its stack. None of these
changes the port's verdict.

Each vector file's `# python:` line is the interpreter's whole
`sys.version`, as in S2 and S7.

| Row | Date (UTC) | Host | Toolchain | Command | Result |
| --- | --- | --- | --- | --- | --- |
| S3-1 | 2026-10-02T06:18:21Z | (M) | CPython 3.14.3, pydantic 2.13.4, pydantic-core 2.46.4, uv 0.12.19 | `uv run --python 3.14.3 --with pydantic==2.13.4 --with pydantic-core==2.46.4 testdata/python/gen_float_vectors.py` | writes `testdata/python/float_vectors.tsv`: 6 000 doubles, 312 104 bytes, sha256 `d8eca3d9cb1cbc48bb636f1e12fceeea3444c0ef63a448fffcd5659382e1b370`, the committed file; a second run gives the same bytes |
| S3-2 | 2026-10-02T06:18:21Z | (M) | the same | `uv run --python 3.14.3 --with pydantic==2.13.4 --with pydantic-core==2.46.4 testdata/python/gen_state_vectors.py` | writes `testdata/python/state_vectors.tsv`: 1 500 documents, 574 254 bytes, sha256 `61f14d954917aa65b84228e30085bc794a7c598519ac47ddcc7363250f8fe90e`, the committed file; a second run gives the same bytes |
| S3-3 | 2026-10-02T05:47:12Z | (M) | the same | the two commands of S3-1 and S3-2 with `--count 100000` and `--count 10000` and `--output <file>` | files not committed: floats sha256 `c7c8f478dc5d0aba31d9a75d710119453368c9969f8579a91bc8f633d487f62a`, documents sha256 `85559c3563766d3a7623cd107390e39cdf521e8d324afef189af272b80dbdc05`; a second run of each gives the same bytes |
| S3-4 | 2026-10-02T05:47:46Z | (M) | go1.27.1 darwin/arm64 | a test in a module outside this one, holding the two float writers and the state re-encoder, run on the files of S3-3 | floats: 100 000 of 100 000 for each writer. Documents: 9 979 of 10 000 agree (9 784 equal bytes, 195 refused by both), 21 differ (`nan-token` 10, `replaced-lone-surrogate` 10, `read-depth` 2, one document in two), 0 unexplained |
| S3-5 | 2026-10-02T05:47:46Z | (M) | go1.27.1 darwin/arm64, `GOEXPERIMENT=nojsonv2` | the same test | the same counts |
| S3-6 | 2026-10-02T05:47:55Z | (L) | go1.27.1 linux/amd64 | the same test, the same files | the same counts |
| S3-7 | 2026-10-02T05:47:55Z | (L) | go1.27.1 linux/amd64, `GOEXPERIMENT=nojsonv2` | the same test | the same counts |
| S3-8 | 2026-10-02T05:47:44Z | (M) | go1.27.1 darwin/arm64, default and `GOEXPERIMENT=nojsonv2` | the same test on the committed files | floats 6 000 of 6 000 for each writer; documents 1 482 of 1 500 agree (1 302 equal bytes, 180 refused by both), 18 differ (`nan-token` 10, `replaced-lone-surrogate` 7, `read-depth` 2, one document in two), 0 unexplained |
| S3-9 | 2026-10-02T05:47:48Z | (L) | go1.27.1 linux/amd64, default and `GOEXPERIMENT=nojsonv2` | the same test on the committed files | the same counts |
| S3-10 | 2026-10-02T05:48:58Z | (M) | go1.27.1 darwin/arm64 | the test of S3-8 on thirteen copies of the committed files, each with one planted difference: an input bit, a byte of an expected float, a version in each header, a byte of an expected document, a refusal's kind, a refusal relabelled as accepted, a class removed (three ways), a class added (two ways), an unknown class name | every copy fails the test; the unchanged files pass |
| S3-11 | 2026-10-02T05:47:46Z | (M) | as S3-4 and S3-5 | the test of S3-4 with the re-encoder reading invalid UTF-8, so that a lone surrogate is refused only where it is written | 9 988 of 10 000 agree (9 793 equal bytes, 195 refused by both), 12 differ (`nan-token` 10, `replaced-lone-surrogate` 0, `read-depth` 2), 0 unexplained |
| S3-12 | 2026-10-02T05:47:55Z | (L) | as S3-6 and S3-7 | the same | the same counts |
| S3-13 | 2026-10-02T05:49:24Z | (M) | go1.27.1 darwin/arm64, default and `GOEXPERIMENT=nojsonv2`, the JSON library at `v0.0.0-20260820222146-c27c302e5fc3` | in the module of S3-4, 35 documents around 10 000 nested containers, read by the JSON library alone (every token, duplicate names allowed) and by the re-encoder | the library refuses the token that opens the 10 001st container, arrays and objects alike, and reads a scalar inside 10 000; the re-encoder refuses for depth at exactly the same documents |
| S3-14 | 2026-10-02T05:49:27Z | (L) | go1.27.1 linux/amd64, default and `GOEXPERIMENT=nojsonv2`, the same library | the same | the same |
| S3-15 | 2026-10-02T14:17:47Z | (M) | CPython 3.14.3, pydantic 2.13.4, pydantic-core 2.46.4, system-one-adapter 0.2.1, uv 0.12.19 | `testdata/python/gen_repr_json_vectors.py`, which runs through the script's first line, `uv run --script`, and its inline list of 14 pinned packages | writes `testdata/python/repr_json_vectors.jsonl`: 191 score criteria, 184 with the legend entry upstream's response body holds for them, 7 refused (1 by `json.loads`, 5 by the question validation, 1 by the answer conversion); 37 557 bytes, sha256 `56014cc0e19a2caf1c09ccd0a95861a9eafa191c7b695f4ed18885e9bf49e5cc`, the committed file; a second run gives the same bytes |
| S3-16 | 2026-10-02T14:17:49Z | (M) | the same | `testdata/python/gen_repr_json_vectors.py --check` | `repr_json_vectors.jsonl: equal to a fresh table` |

## S4: the seam with the Adapter's own error and report types

**Question.** Through the released root SDK, does each failure class
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
  Adapter's own error. The SDK wraps it in its `*decision.TimeoutError` or
  `*decision.ConnectionError`, and `errors.As` reaches the Adapter's error,
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
  Report only, unless the caller's deadline passes while the SDK waits
  between attempts, and its `debug.sdk_retry_count` of 2 says that two
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
| S4-1 | 2026-10-02T04:40:41Z | (M) | go1.27.1 darwin/arm64, `github.com/zchee/decision-model-sdk-go` v0.1.1 | `go test -count=1 -race -v ./...` in a module outside this one, which requires the SDK at v0.1.1 from the module proxy and puts stand-ins for the Adapter's error and report types behind `decision.WithRoundTripper`; the test is the seed of this module's `TestSeamContract` | ok; 76 subtests, 38 cases under each of the two retry policies, 0 failed |
| S4-2 | 2026-10-02T04:41:20Z | (L) | go1.27.1 linux/amd64, the same SDK version | the same test, the same files | ok; 0 failed; the 80 observation lines equal (M)'s byte for byte |
| S4-3 | 2026-10-02T04:41:01Z | (M) | as S4-1 | the same test twice with one expectation or one stand-in changed: the expected attempts of the non-answer class set to 3; the Adapter's error text made to hold the SDK's key | fails both times: 2 subtests for the wrong count; 16 subtests for the key, because the SDK then replaces the cause and `errors.As` no longer reaches the Adapter's error |
| S4-4 | 2026-10-02T04:41:38Z | (L) | as S4-2 | the same two changed runs | fails both times, the same 2 and 16 subtests |
| S4-5 | 2026-10-02T14:34:40Z | (M) | CPython 3.14.3, pydantic 2.13.4, pydantic-core 2.46.4, system-one-adapter 0.2.1, uv 0.12.19 | `testdata/python/gen_report_cases.py`, which runs through the script's first line, `uv run --script`, and its inline list of 14 pinned packages | writes `testdata/python/report_cases.jsonl`: upstream's usage and debug data for 17 fake-provider calls (FM6 4, FM7 7, FM8 1, FM9 1, FM11 4), 13 answered and 4 failed, each with the scenario that produced it; message contents and schemas as their sha256 and length; 34 240 bytes, sha256 `5f306b9ab3a2ad24fdef0994ecd5f243531d6a26a50ee5ef8c05d8d024fed5b2`, the committed file; a second run gives the same bytes |
| S4-6 | 2026-10-02T14:34:41Z | (M) | the same | `testdata/python/gen_report_cases.py --check` | `report_cases.jsonl: equal to a fresh table` |

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
| S5-3 | 2026-10-02T04:34:19Z | (M) | go1.27.1 darwin/arm64, licensecheck v0.3.1 | `go run <program> LICENSE LICENSE-UPSTREAM` in the module's directory, where `<program>` is a program outside this module that prints the result of `licensecheck.Scan` for each file | `LICENSE`: Apache-2.0, 100.00 %, bytes 0 to 11357 of 11357. `LICENSE-UPSTREAM`: MIT, 98.82 %, bytes 13 to 1068 of 1068; the 13 bytes before the match are the title line |
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
`golangci-lint fmt --diff` is different: it walks every Go file under the
repository root, so it checks the adapter's formatting as well.

| Row | Date (UTC) | Host | Toolchain | Command | Result |
| --- | --- | --- | --- | --- | --- |
| S6-1 | 2026-10-02T04:34:50Z | (M) | go1.27.1 darwin/arm64, golangci-lint v2.13.2 | `golangci-lint run --verbose` at the repository root | exit 0, `0 issues.`; no output line names `adapter`; `go list ./...` at the root gives 17 packages, none under `adapter/` |
| S6-2 | 2026-10-02T04:35:15Z | (M) | the same | `golangci-lint run --config ../.golangci.yaml ./...` | exit 0, `0 issues.` |
| S6-3 | 2026-10-02T04:35:26Z | (M) | the same | `golangci-lint run` at the repository root of a copy with an unchecked `os.Chdir` error planted in a new file `adapter/planted.go` | exit 0, `0 issues.`: the root run does not see the file |
| S6-4 | 2026-10-02T04:35:26Z | (M) | the same | `golangci-lint run --config ../.golangci.yaml ./...` in `adapter/` of that copy | exit 1, `adapter/planted.go:9:26: Error return value of os.Chdir is not checked (errcheck)` |
| S6-5 | 2026-10-02T04:35:29Z | (M) | the same | `golangci-lint run` at the repository root of a copy with the same file planted in the root package | exit 1, `planted.go:9:26: Error return value of os.Chdir is not checked (errcheck)`: the root run reports in its own module what it does not report under `adapter/` |
| S6-6 | 2026-10-02T04:51:55Z | (M) | go1.27.1 darwin/arm64, golangci-lint v2.14.0 | the five commands of S6-1 to S6-5 (the first without `--verbose`), on the same copies | the same exit codes and the same two `errcheck` lines: 0, 0, 0, 1, 1 |
| S6-7 | 2026-10-02T04:51:59Z | CI | go1.27.1 linux/amd64, golangci-lint v2.14.0 | `gh run view 36965184872 --log` and `gh run view 36965201873 --log`, the lint jobs of the adapter workflow and of CI at commit `a0b2b824914f1ca7bee625fa19ea897cbc91dde3`, the commit the measurements above were made on | both jobs print `Requested golangci-lint 'latest', using 'v2.14.0'` and `0 issues.`: the root's job runs `golangci-lint run` at the repository root, the adapter's runs `golangci-lint run --path-mode=abs --config ../.golangci.yaml` in `adapter/` |

## S7: pydantic's strict-mode verdicts

**Question.** Which answers does pydantic accept, and which does it refuse,
when upstream's generated models validate an edge input: duplicate member
names, `-0`, `1e0`, `1.0` for an integer, `1` for a number, `true` for a
number, huge integers, a `NaN` token, a byte order mark, trailing data?

**Pass.** A table of verdicts, complete for those inputs and for every
model each applies to, that the Go validator must match.

**Verdict.** Pass: `testdata/python/validator_verdicts.jsonl` holds 882
verdicts of `model_validate_json` over 12 models, 189 accepted and 693
refused, and each of the ten inputs of the question has a row on each of
the six models (noul, score and choice, each discrete and with
probabilities). Five behaviours of pydantic differ from what a strict
validator over a JSON token reader does by itself. The 46 rows that rest on
one of them carry a `class` member: `internal-name` 19, `non-finite-token`
18, `negative-zero` 8, `recursion-limit` 5, `integer-length` 3; seven rows
carry two.

**What the port does because of it.** The Go validator gives pydantic's
verdict on every row of the table, except where the list below says it
differs. The plain rules are confirmed: a duplicate member name is
last-wins at every level; an integer accepts only an integer literal (`1.0`
and `1e0` are refused); a probability accepts any number from 0 to 1, an
integer included, and refuses `true`; a label must equal one of the set
exactly, after unescaping; every member is required; a byte order mark and
trailing data are refused. The five classes:

- `internal-name`: reproduced. A member named `answer_<i>` in `answers`, or
  `probability_<j>` in a probability map, whose index exists and which is
  not itself a question id or a label, is ignored whatever its value. These
  are the field names of upstream's generated models; the ids and labels
  are their aliases.
- `recursion-limit`: reproduced. A text in which a value has more than 200
  arrays and objects around it is refused as a whole, also where that value
  would be ignored. This is the limit of the JSON parser on the model's
  output; the limit of 255 in S3 is the serializer's, on the state.
- `negative-zero`: reproduced. `-0` for a probability is `+0.0`; `-0.0` and
  `-0e0` keep the sign.
- `integer-length`: reproduced. A text in which the integer part of a
  number, its sign counted, is longer than 4300 characters is refused
  wherever the number stands. The length check runs on every number token
  before any conversion, and a number that is skipped, behind a later
  duplicate or in an ignored member, is not converted: a decoder that
  converts every number refuses for overflow the two rows with exactly 4300
  characters, which pydantic accepts.
- `non-finite-token`: pydantic's parser reads the tokens `NaN`, `Infinity`
  and `-Infinity` as numbers, and accepts the document when the token
  stands in a value that is not validated: 12 rows with this class alone
  and 6 that also have `internal-name`, all 18 accepted by pydantic.
  The port refuses a text that holds one of these tokens as invalid JSON,
  so it differs from pydantic on these 18 rows, a deviation from upstream
  that belongs in [`deviations.md`](deviations.md).

A row with two classes has pydantic's verdict in the port when every class
on it is reproduced. The one row with `internal-name` and
`recursion-limit` is refused by pydantic and by the port. The six rows with
`internal-name` and `non-finite-token` follow the `non-finite-token` line
above.

Two baselines show where the work is. Decoding into a Go struct shaped like
the model agrees with pydantic on 551 of the 731 rows of the six models: it
accepts 156 that pydantic refuses, refuses 21 that pydantic accepts and
gives another value in 3. A strict JSON token reader agrees with pydantic's
parser about what is JSON on 813 of the 882 rows.

The table is JSON Lines in ASCII: a header line, then for each model one
line with its answer mode and questions, followed by its rows. A row's
`input`, decoded, is the exact text validated. The table has no random
part, and the generator refuses to run on another interpreter, another
pydantic, or an upstream whose sources differ from the release. Running it
installs upstream's release and what it depends on from the package index,
at the versions upstream locks. The header's `python` member is the
interpreter's whole `sys.version`, as in S2, so the file's bytes reproduce
with the build named there. Its `typesafe_sdk` member, `0.7.0`, is the
version of the Python package upstream depends on, not of this module or of
the Go SDK.

Not measured: input given as bytes (upstream passes a string, so invalid
UTF-8 cannot reach the validator); the wording of pydantic's error
messages beyond the parser's; models other than the twelve; names close to
the internal ones, such as `answer_00`. The length rule has rows on one
model and at the position of a replaced duplicate; a review of this table
measured it at 220 further combinations of model, spelling and position,
which are not rows here, and found the same rule.

| Row | Date (UTC) | Host | Toolchain | Command | Result |
| --- | --- | --- | --- | --- | --- |
| S7-1 | 2026-10-02T05:46:22Z | (M) | CPython 3.14.3, pydantic 2.13.4, pydantic-core 2.46.4, system-one-adapter 0.2.1, uv 0.12.19 | `uv run --python 3.14.3 --with pydantic==2.13.4 --with pydantic-core==2.46.4 testdata/python/gen_validator_verdicts.py` | writes `testdata/python/validator_verdicts.jsonl`: 882 rows over 12 models, 189 accepted and 693 refused, 188 965 bytes, sha256 `76932ffffde130059c476814901c43030220595bcfd0c4deedeadfbd54fa75a6`, the committed file; a second run gives the same bytes |
| S7-2 | 2026-10-02T05:46:22Z | (M) | the same | `testdata/python/gen_validator_verdicts.py --check`, which runs through the script's first line, `uv run --script`, and its inline list of 14 pinned packages | `validator_verdicts.jsonl: equal to a fresh table` |
| S7-3 | 2026-10-02T05:37:14Z | (M) | the same, with upstream installed from a checkout of its commit `e1d4cc9` instead of the release | the command of S7-1 with `--with <checkout>` and `--check` | equal to a fresh table; the 14 Python files of the release and of the checkout have the same digest |
| S7-4 | 2026-10-02T05:37:15Z | (M) | the same as S7-1 | a second script outside this module, which shares no code with the generator: it reads the table alone, rebuilds each model from its model line and validates every input again | 882 of 882 equal in verdict, value (floats by their bits) and error list |
| S7-5 | 2026-10-02T05:36:46Z | (M) | CPython 3.14.6, pydantic 2.13.4, pydantic-core 2.46.4 | a copy of the generator outside this module with only its expected interpreter changed, run with `--python 3.14.6` | every line after the header equals the table of S7-1 |
| S7-6 | 2026-10-02T05:37:16Z | (M) | the same as S7-1 | `--check` and the script of S7-4 against three copies of the table, each with one deliberate difference: an accepted value changed from 0.0 to -0.0; an accepted row turned into a refused one; a row removed | all six runs fail; for the first two differences both name the one row; for the third `--check` names every line from the removed one on, and the second script the row count |
| S7-7 | 2026-10-02T05:36:46Z | (M) | the same as S7-1 | three copies of the generator outside this module, each with the class of one case changed where the case is built | each run stops with `class and verdict differ` and writes no table |
| S7-8 | 2026-10-02T05:36:47Z | (M) | go1.27.1 darwin/arm64 | a Go program in a module outside this one, which reads the table and compares each row with two baselines: a struct decode with `encoding/json`, and the syntax check of a strict JSON token reader | struct decode: 551 of 731 rows agree, 156 accepted that pydantic refuses, 21 refused that pydantic accepts, 3 with another value. Token reader against pydantic's parser: 813 of 882 agree, 61 refused by the reader only, 8 read by the reader only |
| S7-9 | 2026-10-02T05:37:04Z | (L) | go1.27.1 linux/amd64 | the same program, the same file | the same numbers; the output equals (M)'s line by line |

## S8: Codecov with two modules

**Question.** Does Codecov accept a configuration that measures the SDK and
the adapter apart, as two components with their own statuses, and does it
map the adapter's coverage profile to paths under `adapter/`?

**Pass.** Codecov's validator accepts `.codecov.yaml`, and the first upload
of the adapter's profile shows paths under `adapter/`.

**Verdict.** Pass, measured in two steps. First, at a commit where the
module had no statement outside its test files: the validator accepts
`.codecov.yaml` and understands the three keys the configuration depends on
(`project: off`, `name_prefix` in a component status, the negated path
`"!adapter/**"`), and Codecov posts one status per component and kind under
these six names: `codecov/project/default-sdk`, `codecov/project/goal-sdk`,
`codecov/patch/sdk`, `codecov/project/default-adapter`,
`codecov/project/goal-adapter`, `codecov/patch/adapter`. The module's
coverage profile was then the single line `mode: atomic`, and no file of it
reached a report; the two sections below describe that commit.

Second, at commit `948db511eb872ceb7588a35e262c490ad97fecc5`, the first whose
profile holds statements (rows S8-12 to S8-16): Codecov lists the module's
files under `adapter/`, as paths of the repository, and none under the
module's import path, so `.codecov.yaml` needs no `fixes:` entry. All six
uploads of the commit were merged, three from one run of the SDK's workflow
and three from one run of the adapter's, and the six statuses were posted
within a minute of the sixth upload, all `success`. The component `adapter`
stands at 99.31 % and the component `sdk` at 98.17 %. The two project
statuses of the adapter carry no percentage at that commit: its parent has
no coverage of the module, and Codecov then says `No coverage information
found on base report` and compares nothing with the target. The adapter's
figure is read from Codecov's API there; from the next commit on, the base
has coverage of the module.

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
one CI run and one adapter run of a commit give the six sessions between
them: measured at commit `948db511eb872ceb7588a35e262c490ad97fecc5`, the
first with such a profile, where the six statuses were posted 45 to 50
seconds after the sixth upload was created. At the earlier commit one of the
adapter's uploads stalled in `started` without an error, so a run can also
come out one session short.

| Row | Date (UTC) | Host | Toolchain | Command | Result |
| --- | --- | --- | --- | --- | --- |
| S8-1 | 2026-10-02T04:35:53Z | (M) | curl | `curl -fsSL https://docs.codecov.com/docs/codecov-yaml` | the section "Validate your repository YAML" names the endpoint, `curl --data-binary @codecov.yml https://codecov.io/validate`, and says that an invalid file is answered with status 400 |
| S8-2 | 2026-10-02T04:35:54Z | (M) | curl | `curl -sS -X POST --data-binary @.codecov.yaml https://codecov.io/validate` at the repository root | HTTP 200, `Valid!`, then the configuration as Codecov reads it: `project: off` and `patch: off` as `false`; `name_prefix` as written, `default-` and `goal-`; the component paths `"!adapter/**"` and `adapter/**` as `!(?s:adapter/.*)\Z` and `(?s:adapter/.*)\Z` |
| S8-3 | 2026-10-02T04:36:05Z | (M) | curl | the same on three changed copies, one change each: `name_prefix` misspelt, `project: maybe`, a number in place of a path | HTTP 400 each, with the place of the error: the validator refuses an unknown key, a wrong value and a wrong type, so its `Valid!` is a result |
| S8-4 | 2026-10-02T05:06:34Z | (M) | curl | `curl -sS -X POST --data-binary @.codecov.yaml https://api.codecov.io/validate`, the address the same documentation page links | HTTP 200, `Valid!`, the same answer as S8-2 byte for byte |
| S8-5 | 2026-10-02T04:42:48Z | CI | codecov-action v7 | `gh run view 36965184872 --log`, the upload step in the three test jobs of the adapter workflow's first run, at commit `a0b2b824914f1ca7bee625fa19ea897cbc91dde3` | each job: `Found 1 coverage files to report`, `Upload queued for processing complete`; the step succeeds |
| S8-6 | 2026-10-02T04:43:49Z | (M) | curl | `curl -sS https://api.codecov.io/api/v2/github/zchee/repos/decision-model-sdk-go/commits/a0b2b824914f1ca7bee625fa19ea897cbc91dde3/uploads/`, and the uploads' error codes from `https://api.codecov.io/graphql/gh`, both without a token | 6 uploads for the commit so far. Of the three with the flag `adapter`, two are in state `error` with the code `REPORT_EMPTY`; the third has no error recorded and stays in `started` |
| S8-7 | 2026-10-02T04:54:35Z | (M) | curl | `curl -sS 'https://api.codecov.io/api/v2/github/zchee/repos/decision-model-sdk-go/report/?sha=a0b2b824914f1ca7bee625fa19ea897cbc91dde3'` and the same address with `components/` | after the SDK's three uploads are merged: 50 files, none under `adapter/`, 3 sessions; component `sdk` 98.17 %, component `adapter` without a value |
| S8-8 | 2026-10-02T04:55:44Z | (M) | gh | `gh api repos/zchee/decision-model-sdk-go/commits/a0b2b824914f1ca7bee625fa19ea897cbc91dde3/status`, 7 minutes after the three uploads of the first CI run were merged | state `pending`, 0 statuses |
| S8-9 | 2026-10-02T05:06:33Z | (M) | curl | `curl -fsSL https://docs.codecov.com/docs/notifications.md`, and `apps/worker/tasks/notify.py` and `upload_finisher.py` of `codecov/umbrella` at commit `90fc7dc04cd3` | the page: `after_n_builds` delays notifications "until a certain number of uploads have been received and processed". The source compares `after_n_builds` with the number of sessions in the commit's report (`notify.py` lines 805 to 820, `upload_finisher.py` lines 827 to 840) and sends nothing while it is larger: here 6 against 3 |
| S8-10 | 2026-10-02T05:22:49Z | (M) | curl | the two reads of S8-6 and the commit's totals, after a second CI run (36967681205) and a second adapter run (36967681280) of the same commit, the ones its push to `main` started | 12 uploads. The six from the two CI runs are merged, the last at 05:14:31Z; the report has 6 sessions, 50 files, none under `adapter/`. Of the six with the flag `adapter`, five are `error` with `REPORT_EMPTY` and one is still `started` |
| S8-11 | 2026-10-02T05:22:49Z | (M) | gh | `gh api repos/zchee/decision-model-sdk-go/commits/a0b2b824914f1ca7bee625fa19ea897cbc91dde3/status` | state `success`, 6 statuses, posted from 05:14:34Z to 05:14:37Z. `codecov/project/default-sdk`: `98.1% (target 85.0%)`. `codecov/project/goal-sdk`: `98.1% (target 90.0%)`. `codecov/patch/sdk` and `codecov/patch/adapter`: `Coverage not affected when comparing` the parent and the commit. `codecov/project/default-adapter` and `codecov/project/goal-adapter`: `No coverage information found on head` |
| S8-12 | 2026-10-02T10:39:33Z | (M) | curl | `curl -sS 'https://api.codecov.io/api/v2/github/zchee/repos/decision-model-sdk-go/commits/948db511eb872ceb7588a35e262c490ad97fecc5/uploads/?page_size=50'`, and the commit itself at `https://api.codecov.io/api/v2/github/zchee/repos/decision-model-sdk-go/commits/948db511eb872ceb7588a35e262c490ad97fecc5/` | 6 uploads, every one in state `merged`, none in error and none stalled: three with the flag `adapter`, created from 10:28:07Z to 10:29:13Z (12 files, 735 lines, 99.3 each), and three from the SDK's workflow with the flags `xcode-27`, `ubuntu-26.04` and `windows-2025`, created from 10:30:59Z to 10:31:36Z (49 files, 4482 lines, 98.1 each). The commit: state `complete`, 6 sessions; 62 files, 5218 lines, 5131 hits, 87 misses, 98.33 |
| S8-13 | 2026-10-02T10:39:44Z | (M) | curl, jq | `curl -sS 'https://api.codecov.io/api/v2/github/zchee/repos/decision-model-sdk-go/report/?sha=948db511eb872ceb7588a35e262c490ad97fecc5'`, then `jq '.files[].name'` | 12 files under `adapter/`, written as paths of the repository: `adapter/reason.go`, `adapter/retry.go`, five under `adapter/internal/jsonx/`, three under `adapter/internal/prob/`, two under `adapter/llm/`. No file under the module's import path. The other 50 are the SDK's. `adapter/internal/fake` is absent, as `.codecov.yaml` ignores it, and so are the two files that hold no statement |
| S8-14 | 2026-10-02T10:39:45Z | (M) | curl | the address of S8-13 with `&component_id=adapter`, and with `&component_id=sdk` | component `adapter`: 12 files, 735 lines, 730 hits, 5 misses, no partials, 99.31 % against the target of 85 %. Component `sdk`: 50 files, 4483 lines, 4401 hits, 82 misses, 98.17 % |
| S8-15 | 2026-10-02T10:39:45Z | (M) | curl | `curl -sS 'https://api.codecov.io/api/v2/github/zchee/repos/decision-model-sdk-go/compare/components?base=c39c6927339016b96f34f7bae88bc565e4185975&head=948db511eb872ceb7588a35e262c490ad97fecc5'` | component `adapter`: at the base 0 files and 0 lines, at the head 12 files and 99.31 %. Component `sdk`: 98.17 % at both |
| S8-16 | 2026-10-02T10:39:55Z | (M) | gh | `gh api repos/zchee/decision-model-sdk-go/commits/948db511eb872ceb7588a35e262c490ad97fecc5/status` | state `success`, 6 statuses, posted from 10:32:21Z to 10:32:26Z, 45 to 50 seconds after the sixth upload was created. `codecov/project/default-sdk`: `98.1% (target 85.0%)`. `codecov/project/goal-sdk`: `98.1% (target 90.0%)`. `codecov/patch/sdk`: `Coverage not affected when comparing` the parent and the commit. `codecov/project/default-adapter` and `codecov/project/goal-adapter`: `No coverage information found on base report`. `codecov/patch/adapter`: `99.3% of diff hit (target 90.0%)` |
