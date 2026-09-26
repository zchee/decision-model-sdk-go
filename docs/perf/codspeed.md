# CodSpeed

CodSpeed measures the benchmarks listed in [`benchmarks.md`](benchmarks.md)
on every push to `main`. This page covers four things. It says what runs
where and how to read a CodSpeed report. It explains why CodSpeed fails no
build until risk K7 is retired. And it shows how acceptance criterion AC-P7
is read in a repository that takes no pull requests, and what arm64 gets
instead. The numbers behind each statement are in the
[ledger](ledger.md), section W5.4.

## What runs where

| What | Where |
| --- | --- |
| Workflow | `.github/workflows/bench.yaml`, job `CodSpeed (walltime)` |
| Triggers | Every push to `main`; `workflow_dispatch` on any branch; `pull_request`, although none are opened (ruling R2) |
| Runner | `ubuntu-26.04`, GitHub-hosted, linux/amd64, 4 vCPUs (the `-4` suffix on each row in the log). The CPU changes from run to run: AMD EPYC 7763, 9V74 and 9V45, Intel Xeon 6973P-C and Xeon Platinum 8573C so far, and one model name can come with or without AVX-512 (ledger W5.4). |
| Go | `actions/setup-go@v7` with `go-version-file: go.mod`. It reads the `toolchain` line, go1.27.1. |
| Experiments | No `GOEXPERIMENT`. The `nosimd,noruntimesecret` override of the [measurement rule](../support.md#measurement-rule) is for a host whose Go env file sets experiments. The runner has none, so its default already is the Go 1.27 baseline. The report step prints the ToolTags. |
| Instrument | `CodSpeedHQ/action@v5` (runner 5.2.1, go runner 1.3.0), `mode: walltime`. Walltime is the only instrument CodSpeed has for Go (plan decision D4). |
| Command | `go test -bench=. ./...`. The action's go runner keeps only `-bench` and `-benchtime` and adds `-run=^$` itself, so no unit test runs and the workflow never passes `-run` (rulings R5, R5-corr). |
| Rows | Every benchmark in the module: 15 functions, 125 rows since aaa9698, unchanged at 653b5b9 |
| Authentication | The job's OIDC id-token (`id-token: write`), which CodSpeed accepts because its GitHub App is installed on the repository (G1, R4). No token secret exists. |
| Raw samples | Under `$RUNNER_TEMP`, on the runner's disk, not on `/tmp` (K35) |
| Duration | About 10 minutes for the CodSpeed step and 30 s for the guard |

Two steps follow the CodSpeed step:

1. **Check that CodSpeed received every benchmark** is K35's guard. It fails
   the job when the log holds a `failed to … raw results` line. It also
   fails when the rows in the uploaded results differ from those of a
   `-benchtime=1x -cpu=1` run of every benchmark `go test -list` finds
   (see [`benchmarks.md`](benchmarks.md#codspeed)).
2. **Report AC-P7 (report-only)** writes a table to the job summary. The
   table gives the `BenchmarkCall` rows' minimum, median and mean, and the
   sdk/naive ratios, together with `go version`, the ToolTags, the CPU
   model, whether the host exposes AVX-512, and a digest of its CPU flags.
   The step's log lists every uploaded row by its full name. The step
   compares no value, so no timing fails the job. It fails only when it
   has nothing to report: when there is no results file, jq cannot read
   one, or the results hold no `BenchmarkCall/sdk` or `BenchmarkCall/naive`
   row. A green job with an empty report is how K35 hid lost rows.

To run the same thing on (M) without uploading, use the command in
[`benchmarks.md`](benchmarks.md#codspeed).

## Finding a run's report

The CodSpeed app adds a check run named `CodSpeed Performance Analysis` to
every commit it measures, on `main` and on dispatched branches. The check's
details link opens the report:

```sh
gh api repos/zchee/typesafe-sdk-go/commits/<sha>/check-runs \
  --jq '.check_runs[] | select(.app.slug == "codspeed") | .details_url'
```

A run's report is at `https://app.codspeed.io/zchee/typesafe-sdk-go/runs/<id>`.
A comparison between two runs is at `…/runs/compare/<base id>..<head id>`.
The GitHub job log does not print the run id.

## Reading a report

- **Names.** CodSpeed names a row `<file>::<Benchmark>::<sub-benchmark>`,
  with the file path relative to the module root, for example
  `internal/benchmark/call_test.go::BenchmarkCall::sdk`. The report lists
  rows by the last part only, so a name can repeat: `result` appears three
  times, once each for `BenchmarkDecode`, `BenchmarkDecodeNaiveSonic` and
  `BenchmarkDecodeNaiveJSON`. Read the full name.
- **History.** CodSpeed keeps a row's history under that full name.
  Moving the benchmark, or renaming its file, starts the history over,
  and K7's count with it. Reports list 19 rows as "skipped": these are
  names from before owner directive G5 (14 in `bench_prepare_test.go`,
  3 in `bench_config_test.go`, `bench_noop_test.go::BenchmarkNoop` and
  `bench_call_test.go::BenchmarkCall::sdk`). CodSpeed shows their last
  results until someone archives them in the project's settings.
- **The value shown.** CodSpeed's `testing` overlay times every `b.Loop`
  iteration on its own, so a row has one round per iteration. The go
  runner reduces the rounds to min, q1, median, q3, max, mean and stdev.
  **The value a report shows is the minimum.** The mean is the total
  time divided by the rounds. It is the number `go test` prints as ns/op
  in the job log, and the one AC-P6 and the (M)/(L) ledger rows use. The
  minimum and the mean need not rank two rows the same way (see AC-P7
  below). The CodSpeed MCP tool `get_benchmark_result` returns every
  statistic; the report step prints them for `BenchmarkCall`.
- **Comparisons.** CodSpeed compares each run with a base run. For a push
  on `main`, the base is the closest earlier `main` commit with a run. For a
  dispatch on a wave branch, the report named `main`'s head at that time.
  A row that moves by more than the project's regression threshold
  (CodSpeed's default is 10 %) is marked as regressed or improved. One
  regressed row fails CodSpeed's check run. That check belongs to the
  CodSpeed app, not to the workflow: the job stays green, and nothing in
  this repository requires the check.
- **Noise.** The banner "Unknown Walltime execution environment detected"
  means CodSpeed sees a hosted runner, not one of its own machines. On
  these runners, rows whose code did not change move by more than 10 %:
  - At 3ffe77b the `EncodeBody/rawjson/{64KiB,1MiB}/naive-json` rows,
    which that commit did not touch, were 13 to 14 % faster.
  - `Loopback/cold-fanout-64` (B6) has measured from 4.8 to 5.5 ms on
    the EPYC 7763 alone. It failed the check at 1f694b0 (−11.92 %) and at
    aaa9698 (−12.24 %), and aaa9698 changed only the workflow and
    documents.
  - A run on an EPYC 9V74 moved 21 rows by 18 to 30 % against one on an
    EPYC 7763.
  - A run on an EPYC 9V45, which has AVX-512, ran every row 1.4 to 2.7
    times faster than one on an EPYC 7763, so CodSpeed marked all 125 rows
    as improved. `BenchmarkCall/sdk`'s mean was 3.30 µs there and 6.68 µs
    on the 7763.

  A failed CodSpeed check on one row is therefore not a regression until
  another run repeats it. B6 is report-only and never a gate (R101).
  Risk K37 records the red checks. The remedies are settings in the CodSpeed
  project, for the owner: a per-benchmark threshold for
  `bench_internal_test.go::BenchmarkLoopback::cold-fanout-64`, or ignoring
  that row, and archiving the 19 skipped rows. The workflow keeps B6 in the
  run.

## K7: report-only until the noise is known

Risk K7: walltime on hosted runners is noisy, so CodSpeed results gate
nothing until 20 runs on `main` show a spread below 5 % for
`BenchmarkCall/sdk`. The plan then made them blocking; rulings R109,
R115, R109b, R109c and R109c-corr amend that (below). Until then:

- No step fails on a timing, and nothing requires CodSpeed's check run.
- The spread is (largest − smallest) / smallest of `BenchmarkCall/sdk`'s
  mean: the mean is the statistic AC-P7 reads (ruling R108).
- The count starts at `main`'s run of aaa9698 (GitHub run 36221839206,
  CodSpeed run 6ab75ed2e412c1cc664ef2d7), where the K35 fix landed. The
  runs from ca226bb to 3ffe77b lost 11 rows and do not count. Runs before
  ca226bb have other names (G5).
- Only successful push runs on `main` count; dispatched runs on branches
  do not. `BenchmarkLoopback` never counts (R101).

**K7 is counted per CPU host (ruling R109, which the owner ratified as
R115, refined by R109b).** Hosted runners rotate CPU models, and the
host sets the level of every row. On identical code, every row ran 1.4 to
2.7 times faster on an EPYC 9V45 than on an EPYC 7763 (see "Noise"
above). A spread taken across hosts would measure which machine each run
got, not noise. Hosts do not even rank rows alike. Against an EPYC 7763
run, an Intel Xeon 6973P-C run was faster on 115 rows but 33 to 35 %
slower on `EncodeState/ascii/6MiB/{encode,check}`. So no single baseline
can serve every host, and a row's history only means something within
one host group.

One model name does not always name one kind of host. Two runs on an
"AMD EPYC 9V74" differed only in the CPU flags the VM exposed, AVX-512
among them, and the one with AVX-512 ran 123 of 125 rows 15 to 50 %
faster. Counted per model name, that one pair already spreads 29 %. So
R109b keys K7's groups on the CPU model name plus AVX-512 exposure
(`avx512f` among the flags). The report step prints that key
(`AVX-512 yes` or `no`) with a 12-hex digest of the sorted flags, so
finer splits stay visible.

The 20 runs and the spread below 5 % of `BenchmarkCall/sdk`'s mean apply
to `main` runs in one such group, within one segment of it (below). Runs
in another group start their own count, and groups are never mixed.
Gating stays off. When one group reaches 20 runs within 5 %, the owner
reconsiders blocking and looks at the cross-host comparison again (R115).
Beside the count, the ledger
records the spread of the sdk/naive mean ratio over all counted runs,
whatever their host, because the ratio is what AC-P7 compares. No
threshold is set on that ratio spread yet. Every row of the ledger's
section W5.4 carries the run's CPU model, its AVX-512 exposure and its
GitHub and CodSpeed run ids. The ledger also gives the count per model
name alone, as R109 was first written.

**Within a group, K7 is read per segment (rulings R109c and
R109c-corr).** A change to the SDK's speed moves a group's mean as much
as noise does, so a spread over runs on both sides of it measures the
change. A landing opens a new segment of a host group only when it does
both of two things: it changes a non-test `.go` file on the call path
(the root package, `internal/codec`, `internal/wire` or
`internal/h2gate`), and it moves `BenchmarkCall/sdk`'s mean by 5 % or
more on that group between the last `main` run before it and the first
`main` run after it. Landings of documents, CI or tests never open one.
A lower trigger would open segments on noise: the same tree, aaa9698,
read 7.014 and 6.684 µs on the 7763, 4.93 % apart by K7's spread
formula. The count and the spread are recorded per group and per
segment, and the 20 runs within 5 % must fall in one segment. The EPYC
7763's second segment opened at 653b5b9, W5.3's landing, by ruling and
not by the rule: W5.3 changed the call path, but its run's mean was
4.94 % below the last run before it (a4cbb5d), under the 5 % bar
measured from that run (5.19 % by K7's spread formula, which R109c-corr
does not name). The lead ruled the segment open, and the owner ratifies
R109c and R109c-corr with the Phase 5 batch. So the 7763 reads:
segment 1, aaa9698 to a4cbb5d, 3 runs, spread 1.29 %; segment 2, from
653b5b9, 1 run, no spread yet. Gating stays off.

To list the candidate runs, then keep the successful ones from 36221839206
on:

```sh
gh run list --workflow bench.yaml --branch main --event push \
  --json databaseId,headSha,conclusion,createdAt --limit 100
```

Each run's job summary holds its `BenchmarkCall` numbers and CPU model.

## AC-P7 without pull requests

AC-P7 says: "CodSpeed reports `call/sdk` faster than `call/naive` on the
PR run". The plan's `call/sdk` is `BenchmarkCall/sdk` and `call/naive` is
`BenchmarkCall/naive`; the q3 rows decide, and the `-q20` rows are
recorded (R101 (b)). This repository takes no pull requests (R2): waves
land on `main` by fast-forward. So the PR run is read as two runs of
`bench.yaml` on the landing commit's tree:

1. the `workflow_dispatch` run on the wave branch at its final head,
   before the lead lands it;
2. the first push run on `main` after the landing.

"Faster" is read on the **mean** (owner ruling R108): the total time
divided by the rounds, which is `go test`'s ns/op and the basis of AC-P6
and G3. It is not read on the minimum that CodSpeed's report shows, nor on
the median. AC-P7 holds when both runs show `BenchmarkCall/sdk`'s mean
below `BenchmarkCall/naive`'s. Its margin is about as large as the mean's
own run-to-run spread, so under R108 AC-P7 is report-only until K7 is met:
the ledger records the three statistics and the three ratios of every run,
and nothing asserts them. **Status: the first half holds on the mean
(ledger W5.4-29, de27718: 0.835), report-only under K7. The second half,
`main`'s first run after W5.4 lands, is recorded in the as-built
appendix that Phase 7 (W7) writes.**

The three statistics disagree on these two rows:

- **min**: the fastest single iteration. It is the value CodSpeed's report
  shows.
- **median**: the middle iteration.
- **mean**: the total time divided by the rounds. It is what AC-P7 reads.

On every run up to aaa9698, `BenchmarkCall/sdk` is 12 to 16 % slower than
`BenchmarkCall/naive` by minimum and 2 to 8 % slower by median, but 2 to
12 % faster by mean. The overlay times each iteration on its own. Measured
that way, one naive call is shorter than one SDK call. The SDK comes out
ahead only once garbage collection is counted. The naive client allocates
54 objects per call on (M) and 68 on (L), against the SDK's 22 (ledger
W5.1). Collecting them slows a share of later iterations: the naive row's
stdev is 1.43 to 1.92 times the SDK's. Those slow iterations sit in the
tail of the distribution, which the minimum and the median leave out and
the mean includes. The sdk/naive ratios move less than the absolute
values when the host changes, but they are not host-independent: by mean
0.872 to 0.897 on the 7763's counted runs against 0.970 on the 9V74 with
AVX-512, and after W5.3 0.846 and 0.880 against 0.798 and 0.835 (ledger
W5.4, finding 2). So AC-P7's ratio, too, is compared within one host
group.

That the SDK's single call is slower than the naive client's (by about
0.7 µs on amd64) is risk K36. Taking that time off the SDK's call path is
a best-effort target for W5.3, not part of AC-P7.

W5.3 (on `main` from 653b5b9) scans a well-formed body once. The
traversal runs over the body cut just before the root's closing brace,
and the second pass, `internal/codec.trailing` running sonic's
`decoder.Skip` over the whole body to find where the root value ends, now
runs only on a body the cut cannot take (`traverse` in
`internal/codec/decode.go`). Compared on the same host group before and
after W5.3, `BenchmarkCall/sdk`'s minimum fell by 0.56 to 0.77 µs on the
EPYC 7763, by 0.35 and 0.38 µs on the 9V74 with AVX-512 and by 0.30 µs
on the 9V45, while `BenchmarkCall/naive`'s stayed within its earlier
range on the 7763 and moved by 0.03 µs at most on the other two. The
minimum's gap went from 0.60 to 0.78 µs (ratio 1.124 to 1.162) to 0.04
and 0.07 µs (1.008, 1.015) on the 7763, from 0.48 to 0.12 and 0.13 µs
(1.139 to 1.035, 1.037) on the 9V74 with AVX-512, and from 0.38 to
0.05 µs (1.154 to 1.020) on the 9V45. The median ratio went from 1.009
to 1.076 down to 0.928 to 0.942. So since W5.3 the SDK is faster by
median and by mean, and still 0.8 to 3.7 % slower by the minimum that
CodSpeed shows (ledger W5.4, finding 5).

## arm64 (K18)

In this repository CodSpeed measures linux/amd64 only. Its arm64 machines
are its Macro Runners (bare metal, `runs-on: codspeed-macro`), and CodSpeed
offers those only to repositories owned by an organization; this one
belongs to a personal account. The workflow rules also allow only the
`ubuntu-26.04`, `xcode-27` and `windows-2025` labels. So arm64 has no
hosted regression signal (K18, accepted). The Phase 7 release checklist
runs the allocation tests and B1–B6 on (M) and records them in the ledger
instead.

The two architectures do not agree on AC-P6's time clause. The q3 ratio
`call/sdk` / `call/naive` is 0.890 on (L) and 1.274 on (M) (ledger
W3.4-08, -09), because on arm64 sonic's generic decode is faster than the
SDK's visitor decode (K23). For that reason G3 gates the clause on amd64
only, and CodSpeed's amd64 numbers say nothing about arm64.
