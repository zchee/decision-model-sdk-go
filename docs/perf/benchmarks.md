# Benchmarks (B1–B6)

The SDK's benchmark set follows the Rust port's B1–B6. Every benchmark uses
`for b.Loop()` and reports allocations. CodSpeed runs all of them on each
push to `main` and on each pull request (`.github/workflows/bench.yaml`). The
numbers of record, per host, are in [`ledger.md`](ledger.md), section W5.1.
The top of each benchmark file states what the benchmark measures and how it
can mislead.

| ID | Benchmark | Package, file | Measures | Rows |
| --- | --- | --- | --- | --- |
| B1 | `BenchmarkEncodeBody` | root, `bench_encode_test.go` | The request body encode (`encodeBody`) into a pooled scratch, against the naive comparator's encode of the same body. | `<kind>/<size>/{sdk,naive,naive-json}`. Kinds: `text` (a boxed string), `rawjson` (`RawJSON`), `struct` (`*struct`), `map` (`map[string]any`). Sizes: `1KiB`, `64KiB`, `1MiB`. |
| B1 | `BenchmarkEncodeState` | `internal/codec`, `encode_bench_test.go` | The state encode alone, next to the UTF-8 check of ruling R48. | `{ascii,cjk}/{1KiB,64KiB,6MiB}/{encode,check}` |
| B2 | `BenchmarkDecode`, `BenchmarkDecodeNaiveSonic`, `BenchmarkDecodeNaiveJSON` | `internal/codec`, `decode_bench_test.go` | The production decode of each AC-P2 fixture, flood fixtures included, against the naive decode into a `map[string]any`. Sub-benchmarks with the same name pair up. | one row per fixture; a fixture the codec refuses (`parity-big-exp-unknown`, `1e400`) has no naive row |
| B3 | `BenchmarkAssembly` | root, `bench_assembly_test.go` | Everything the first attempt of a call does before the transport: the encode, `GetBody`, the header template, the URL copy, the deadline, the body reader and the `*http.Request`. The question set is the section 5 set. | `request`, `prepare-and-request` |
| B4 | pending | root | `Retry-After` parsing and the backoff schedule. These are added once W3.2's `retry.go` lands. | |
| B5 | `BenchmarkCall` | root, `bench_call_test.go` | One whole `SystemOne` call over the in-memory `Recorder`, which answers at once, measured against the floor and the naive comparator. | `sdk`, `floor`, `naive`, `naive-json`, and the same four with `-q20` |
| B6 | `BenchmarkLoopback` | root, `bench_loopback_test.go` | Calls over HTTP/2 and TLS on loopback through the default transport. These rows are wall clock only. | `call` (one warm connection; the metric `new-conns` should be 0); `cold-fanout-64` (a fresh client, 64 calls at once; the metric `conns/op` should be 1) |

Setup-only benchmarks, kept from earlier waves: `BenchmarkPrepare`,
`BenchmarkFalsyJSON`, `BenchmarkHeaderTemplateClone` and `BenchmarkNoop`.

## Rows that the acceptance criteria read

| Criterion | Rows | Host that gates it |
| --- | --- | --- |
| AC-P2, "≤ 0.5 × naive" allocations on the plain 3-answer fixture | `BenchmarkDecode/result` allocs/op divided by `BenchmarkDecodeNaiveSonic/result` allocs/op | reported by W5.1; W5.2 asserts it |
| AC-P6 time clause | `BenchmarkCall/sdk` ns/op < `BenchmarkCall/naive` ns/op | amd64 gates (G3); arm64 is recorded (K18). W3.4 freezes it |
| AC-P7 | CodSpeed reports `BenchmarkCall/sdk` faster than `BenchmarkCall/naive` on the pull-request run | CodSpeed (amd64); report-only until K7 is met |

The names above are stable. The plan's `call/sdk` is `BenchmarkCall/sdk` and
`call/naive` is `BenchmarkCall/naive`. CodSpeed keeps each benchmark's
history under its full name, so renaming a benchmark starts that history
over, including the 20 runs that K7 counts.

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
GOEXPERIMENT=nosimd,noruntimesecret codspeed run --skip-upload -- go test -bench=. ./...
```

CodSpeed results are report-only until 20 runs on `main` show a spread
below 5 % for `BenchmarkCall/sdk` (K7). B6 runs in CodSpeed with the rest.
Its rows are wall clock over loopback: their noise comes from the kernel,
TLS and scheduling. They are not gates at any point.
