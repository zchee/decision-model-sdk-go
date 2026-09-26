# Benchmarks (B1–B6)

The SDK's benchmark set follows the Rust port's B1–B6. Every benchmark uses
`for b.Loop()` and reports allocations when run with `-benchmem`, as every
command below is. CodSpeed runs all of them on each
push to `main` and on each pull request (`.github/workflows/bench.yaml`). The
numbers of record, per host, are in [`ledger.md`](ledger.md), section W5.1.
The top of each benchmark file states what the benchmark measures and how it
can mislead.

Owner directive G5 moved the benchmarks out of the root package. Those that
need only the exported API and the internal packages are in
`internal/benchmark`, a package with no code outside its tests. Six stay in
the root's `bench_internal_test.go`, each because it times or builds from
what only the root package can reach; the file's header gives the reason
for each. `go test -bench . ./...` and CodSpeed find both.

| ID | Benchmark | Package, file | Measures | Rows |
| --- | --- | --- | --- | --- |
| B1 | `BenchmarkEncodeBody` | root, `bench_internal_test.go` (its sdk arm times the unexported `encodeBody`) | The request body encode (`encodeBody`) into a pooled scratch, against the naive comparator's encode of the same body. | `<kind>/<size>/{sdk,naive,naive-json}`. Kinds: `text` (a boxed string), `rawjson` (`RawJSON`), `struct` (`*struct`), `map` (`map[string]any`). Sizes: `1KiB`, `64KiB`, `1MiB`. |
| B1 | `BenchmarkEncodeState` | `internal/codec`, `encode_bench_test.go` | The state encode alone, next to the UTF-8 check of ruling R48. | `{ascii,cjk}/{1KiB,64KiB,6MiB}/{encode,check}` |
| B2 | `BenchmarkDecode`, `BenchmarkDecodeNaiveSonic`, `BenchmarkDecodeNaiveJSON` | `internal/codec`, `decode_bench_test.go` | The production decode of each AC-P2 fixture, flood fixtures included, against the naive decode into a `map[string]any`. Sub-benchmarks with the same name pair up. | one row per fixture; a fixture the codec refuses (`parity-big-exp-unknown`, `1e400`) has no naive row |
| B3 | `BenchmarkAssembly` | root, `bench_internal_test.go` (it rebuilds `Client.attempt`'s unexported steps) | Everything the first attempt of a call does before the transport: the encode, `GetBody`, the header template, the URL copy, the deadline, the body reader and the `*http.Request`. The question set is the section 5 set. | `request`, `prepare-and-request` |
| B4 | `BenchmarkRetryAfter`, `BenchmarkBackoff` | `BenchmarkRetryAfter`: `internal/benchmark`, `retry_test.go`; `BenchmarkBackoff`: root, `bench_internal_test.go` (the unexported `backoff`) | `(*APIError).RetryAfter` on the error of a real 429, and the backoff delay with `DefaultRetry`'s numbers and a constant random draw. | `RetryAfter/{seconds,ms,http-date}`; `Backoff/{retry-1,retry-6,retry-1000,schedule}` (`schedule`: `DefaultRetry`'s two waits) |
| B5 | `BenchmarkCall` | `internal/benchmark`, `call_test.go` | One whole `SystemOne` call over the in-memory `Recorder`, which answers at once, measured against the floor and the naive comparator. | `sdk`, `floor`, `naive`, `naive-json`, and the same four with `-q20` |
| B6 | `BenchmarkLoopback` | `call`: `internal/benchmark`, `loopback_test.go`; `cold-fanout-64`: root, `bench_internal_test.go` (its gate counters come from the unexported transport) | Calls over HTTP/2 and TLS on loopback through the default transport. These rows are wall clock only. | `call` (one warm connection; the metric `new-conns` should be 0); `cold-fanout-64` (a fresh client, 64 calls at once; the metrics `leaders/op` and `firstholds/op`, the gate's own counters, should be 1, and `conns/op` too, although the stock HTTP/2 pool alone also meets that on loopback, so AC-P4's evidence is `internal/h2gate`'s `TestFanOut`) |

Setup-only benchmarks, kept from earlier waves: `BenchmarkPrepare` and
`BenchmarkFalsyJSON` in the root's `bench_internal_test.go` (the first
shares its case table, `prepare_cases_test.go`, with `TestAllocPrepare`;
the second times the unexported `falsyJSON`), and
`BenchmarkHeaderTemplateClone` and `BenchmarkNoop` in `internal/benchmark`.

`go test -list 'Benchmark.*' ./...` prints 15 function names in three
packages: 6 in the root, 5 in `internal/benchmark` and 4 in
`internal/codec`. `BenchmarkLoopback` appears in two packages, each with one
of its arms. Together they give 125 rows.

## Rows that the acceptance criteria read

| Criterion | Rows | Host that gates it |
| --- | --- | --- |
| AC-P2, "≤ 0.5 × naive" allocations on the plain 3-answer fixture | `BenchmarkDecode/result` allocs/op divided by `BenchmarkDecodeNaiveSonic/result` allocs/op | reported by W5.1; W5.2 asserts it |
| AC-P6 time clause | `BenchmarkCall/sdk` ns/op < `BenchmarkCall/naive` ns/op, the q3 rows only; the `-q20` rows are recorded and are a W5.3 target (ruling R101) | amd64 gates (G3); arm64 is recorded (K18). Frozen at W3.4 ([`frozen-budgets.md`](frozen-budgets.md)): (L) q3 0.890 holds; (M) q3 1.274, recorded (ledger W3.4-08, -09) |
| AC-P7 | CodSpeed reports `BenchmarkCall/sdk` faster than `BenchmarkCall/naive` on the pull-request run | CodSpeed (amd64); report-only until K7 is met |

The names above are stable. The plan's `call/sdk` is `BenchmarkCall/sdk` and
`call/naive` is `BenchmarkCall/naive`, both in `internal/benchmark`. CodSpeed
keeps each benchmark's history under its full name, the package path
included, so renaming or moving a benchmark starts that history over,
including the 20 runs that K7 counts. The G5 move did that for every
benchmark it moved: K7's count for `BenchmarkCall/sdk` restarts at the
landing that carries the move. CodSpeed is report-only, so no gate moves
with it.

## The naive comparator

`internal/testsupport/naive` is the comparator of owner decision G3 (a). It
makes a System One call the obvious way:

1. `sonic.Marshal` of a plain struct `{State any; Model string; Questions json.RawMessage}`.
2. `http.NewRequestWithContext` over a `bytes.Reader` of those bytes.
3. The headers set one by one.
4. The same per-attempt deadline, via `context.WithTimeout`.
5. The `RoundTripper` called directly, with no `http.Client`.
6. `io.ReadAll` of the response.
7. `sonic.Unmarshal` into a `map[string]any`.

It pools nothing, interns nothing and validates nothing beyond what sonic
does. `naive.StdJSON` runs the same code with `encoding/json`. Its rows
(`naive-json`) are reported only.

The comparison is fair because both clients send the same request:

- **Same headers.** The benchmark gives the naive client the SDK client's
  own header template, so the request carries the same six headers.
- **Same body bytes.** The body is built from the same state, model and
  prepared question bytes. `TestNaiveRequestMatchesSDK` (B5) checks that
  the two requests the transport records are equal, body included, byte for
  byte. `TestEncodeBodyMatchesNaive` (B1) checks the same for each state
  kind and size, except `map`: every encoder writes a map's members in its
  own order, so for `map` only the body lengths are compared.
- **Same fixture and transport.** Both calls go through the same `Recorder`,
  which answers with the same fixture.

The clients differ only in how they encode, build, read and decode, which is
what the SDK's design decides.

Because sonic is the SDK's own codec, the gap between the two clients is the
SDK's design and not the choice of library. On arm64, sonic's generic decode
is faster than the SDK's visitor decode (K23, ruling R28b), so on that host
the time clause can fail while the allocation clause holds. That is why G3
gates the time clause on amd64 only.

## Running them

On (M) (darwin/arm64), always with the experiment override and under the
shared lock, one benchmark run at a time:

```sh
SP=<the lead's scratchpad>   # holds bench.lock
BASE=$(git rev-parse --short HEAD) GOEXPERIMENT=nosimd,noruntimesecret \
  FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 \
  sh _spikes/s-c1/run.sh '(M)' _spikes/w5.1/results "$SP/bench.lock" bench-M \
  -run '^$' -bench . -benchmem -count=5 ./...
benchstat _spikes/w5.1/results/bench-M.txt
```

On (L) (linux/amd64): copy the tree with the section 11 `tar | ssh` pipe.
Then use the toolchain under `/tmp/ts-spike/go`, with `GOPATH`, `GOMODCACHE`
and `GOCACHE` under `/tmp/ts-spike`, no `GOEXPERIMENT`, and the lock
`/tmp/ts-spike/bench.lock`:

```sh
BASE=<sha> MAXLOAD=44 sh _spikes/s-c1/run.sh '(L)' _spikes/w5.1/results /tmp/ts-spike/bench.lock bench-L \
  -run '^$' -bench . -benchmem -count=5 ./...
```

`run.sh` writes the date, the load before and after, `go version` and the
ToolTags into the raw file's header. Each ledger row copies them.

### CodSpeed

`bench.yaml` runs `go test -bench=. ./...` through `CodSpeedHQ/action@v5` in
walltime mode on `ubuntu-26.04`. The action's Go runner keeps only `-bench`
and `-benchtime` and adds `-run=^$` itself. So neither the workflow nor a
local run passes `-run`: the runner would take `'^$'` as a package pattern
and fail (rulings R5 and R5-corr).

To list what CodSpeed discovers:

```sh
GOEXPERIMENT=nosimd,noruntimesecret go test -list 'Benchmark.*' ./...
```

To run CodSpeed locally on (M), without uploading:

```sh
GOEXPERIMENT=nosimd,noruntimesecret codspeed run --skip-upload -m walltime -- go test -bench=. ./...
```

Without `-m walltime` the runner takes the mode from the shell session
(`codspeed use <mode>`); walltime is CodSpeed's only instrument for Go.

CodSpeed results are report-only until 20 runs on `main` show a spread
below 5 % for `BenchmarkCall/sdk` (K7). B6 runs in CodSpeed with the rest,
report-only, and is never a gate (ruling R101): its rows are wall clock over
loopback, and their noise comes from the kernel, TLS and scheduling.
`BenchmarkLoopback` is excluded from K7's count and from any gate built on
it; W5.4, which owns `bench.yaml`, may skip it under CodSpeed's environment
if its noise pollutes the run summary.
