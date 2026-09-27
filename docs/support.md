# Support

## Support matrix

| Go | GOARCH | GOOS | Status |
| --- | --- | --- | --- |
| 1.27.x | `amd64`, `arm64` | any GOOS the Go release supports on that architecture | supported |
| 1.27.x | any other (`386`, `riscv64`, `wasm`, …) | any | refused at compile time |
| 1.28 and later | any | any | refused at compile time until the bump below |
| 1.26 and earlier | any | any | from 1.21, switches to a Go 1.27 toolchain or refuses the module; 1.17 to 1.20 fail the build (below) |

CI runs the tests on `ubuntu-26.04` (linux/amd64), `xcode-27` (darwin/arm64)
and `windows-2025` (windows/amd64).

Off the matrix there are two outcomes:

- A `go` command from Go 1.21 to 1.26 never compiles the SDK itself.
  `go.mod` requires `go 1.27`, so with `GOTOOLCHAIN=auto` (the default) it
  switches to a Go 1.27 toolchain (in this repository `go1.27.1`, from the
  `toolchain` line) and builds with that; with `GOTOOLCHAIN=local` it refuses
  the module. Go 1.17 to 1.20 predate toolchain switching: they attempt the
  build and print `note: module requires Go 1.27` when it fails.
- On a GOARCH other than `amd64` and `arm64`, or on Go 1.28 and later, the
  build fails with the D1 identifier described below.

The support window is the set of Go releases that the newest tag of
`github.com/bytedance/sonic` supports. sonic is the SDK's only JSON codec, and
its JIT path compiles only for
`(amd64 && go1.17 && !go1.28) || (arm64 && go1.20 && !go1.28)` (the build line
of `sonic.go` in v1.15.4). Everywhere else sonic silently falls back to
`encoding/json` and prints a warning at init; within the Go releases `go.mod`
admits, the SDK refuses to compile there instead.

## The compile-time refusal

`internal/codec` is the only package that imports sonic. Since wave W0.2 of
the port plan it carries these build constraints:

- `internal/codec/unsupported.go` carries
  `//go:build go1.28 || !(amd64 || arm64)`, and its only statement is
  `var _ = typesafe_sdk_go_requires_go1_17_to_go1_27_on_amd64_or_arm64`. The
  identifier is undefined on purpose: Go has no `#error`, so the identifier's
  name is the error message.
- Every other file of `internal/codec`, `_test.go` files included, carries the
  complementary `//go:build !go1.28 && (amd64 || arm64)`.

Off the matrix the package is `unsupported.go` alone and imports nothing, so
both `go build` and `go vet` fail with exactly:

```
undefined: typesafe_sdk_go_requires_go1_17_to_go1_27_on_amd64_or_arm64
```

Without the complementary constraint, sonic's own 32-bit code or its JIT-only
functions would fail first with an unrelated error, and `go vet`, which prints
only the first type error, would never show the identifier.

From W0.2 on, CI checks the refusal on every run: `go vet` and `go build` of
`./internal/codec/` with `GOOS=linux GOARCH=386`, with `GOOS=linux
GOARCH=riscv64` and with `-tags go1.28` (the local stand-in for a Go 1.28
toolchain) must each exit non-zero and print the identifier. The weekly `gotip`
workflow checks the same with the development toolchain; any other outcome is
a canary failure.

The seam tests in `internal/codec/seam_test.go` run in the lint job on their
own (`go test -run Seam ./internal/codec/`) and with every test run.
`TestSeamBuildConstraints` asserts that every `internal/codec` file carries
exactly one of the two constraint lines, as its first line; that the two are
complements for every GOARCH and Go release; and that `unsupported.go` holds
nothing but the identifier. `TestSeamSonicJITPath` asserts that no sonic
package `internal/codec` compiles takes its `encoding/json` fallback
(`compat.go` or a `*_compat.go` file importing `encoding/json`, next to
`sonic.go`, `api.go`, `*_native.go` or `spec.go` on the JIT side), on the host
and, by comparing build lines, for every GOARCH and Go release up to the
cutoff. `TestSeamImports` keeps every JSON library out of the other packages
(`internal/testsupport`, which holds test tooling, excepted).

From W2.0 on, `internal/codec` also imports `encoding/json`, only for the
`json.Number` type that sonic's `ast.Visitor` interface requires; nothing is
encoded or decoded through it.

## Bump procedure for Go 1.28

On Go 1.28 GA day every consumer on Go 1.28 gets the compile error above until
sonic and the SDK both move. The weekly `gotip` workflow watches for that day
with two signals and keeps each in its own issue, separate from its "gotip
canary failing" issue:

- It reads the Go download index (`https://go.dev/dl/?mode=json&include=all`)
  for a `go1.28rc…` or `go1.28.…` release; `gotip` itself always reports a
  development version, never a release candidate.
- It lists the files `gotip` compiles for every package of the sonic module,
  for the version in `go.mod` and for the newest release, and looks for
  sonic's `encoding/json` fallback files (`compat.go` and the `*_compat.go`
  files that import `encoding/json`). While sonic's JIT files carry
  `!go1.28`, `gotip` compiles the fallback.

Once a Go 1.28 release candidate or release exists and the fallback is still
compiled, the workflow opens or updates the issue "Go 1.28: waiting on sonic"
every week: the early warning that D1 will refuse Go 1.28 and that sonic has
not caught up. Once either sonic version compiles no fallback file on `gotip`,
it opens or updates "Go 1.28: sonic builds on tip, bump D1", which is the
signal to start the steps below.

When a sonic tag without `!go1.28` exists:

1. Bump sonic: `go get github.com/bytedance/sonic@<tag> && go mod tidy`.
   The codec guards against an over-read of sonic v1.15.4's native
   scanner on short inputs; before trusting a new version, read the ledger's
   note on it (W6-secfix, "MIN-1: the inputs that reach sonic's
   `advance_dword`", in [`perf/ledger.md`](perf/ledger.md)), which names
   the input shapes, the guards and the test that fails first when sonic
   changes.
2. Set the `d1Cutoff` constant of `internal/codec/seam_test.go` to
   `"go1.29"` and run `go test -run Seam ./internal/codec/`: the seam tests
   derive both constraint lines, the identifier and the text of every site
   below from it, and fail on each site still naming the old range. Then, in
   **one** commit, edit every site that names the supported range:
   - `unsupported.go` → `//go:build go1.29 || !(amd64 || arm64)`;
   - every other file of `internal/codec`, tests included →
     `//go:build !go1.29 && (amd64 || arm64)`;
   - the identifier, renamed to the new range (its `go1_27` part becomes
     `go1_28`), in `unsupported.go`, in the `D1_IDENTIFIER` of the refusal
     steps of `.github/workflows/ci.yaml` and `.github/workflows/gotip.yaml`,
     in this document and in `README.md`;
   - the prose "Go 1.17 to 1.27" and the quoted constraint
     `"!go1.28 && (amd64 || arm64)"` in `internal/codec/unsupported.go`, and
     "Go 1.17 to 1.27" in `internal/codec/doc.go`;
   - `.github/workflows/ci.yaml`: the stand-in tag of the refusal step
     (`GOFLAGS=-tags=go1.29`) and every comment naming Go 1.28;
   - `.github/workflows/gotip.yaml`: both issue titles ("Go 1.29: waiting on
     sonic", "Go 1.29: sonic builds on tip, bump D1"), the release filter of
     the PM5 probe (`go1.29rc`, `go1.29.`) and every comment naming Go 1.28;
   - this document: the support-matrix rows (`1.28.x` supported, `1.29 and
     later` refused), the constraint lines and identifier above, the
     `-tags go1.29` stand-in, the sonic build line quoted under "Support
     matrix", and this procedure;
   - the support-window sentence of `README.md` (`Go 1.27.x`, Go 1.28 → the
     new range).

   `TestSeamD1IdentifierSites` checks each of these files for the text it
   derives from `d1Cutoff` (in the two workflows it also requires the refusal
   step to pass `D1_IDENTIFIER` to its script, and accepts no other Go
   release anywhere in the file); `TestSeamBuildConstraints` checks every
   file's constraint line. Moving only `unsupported.go` would be wrong: the
   other files would keep `!go1.28`, exclude themselves on Go 1.28, and leave
   the package empty on a supported release.
3. Re-run the refusal checks (`GOARCH=386`, `GOARCH=riscv64`, and the
   next-release stand-in tag, now `-tags go1.29`; `go vet` and `go build`).
4. Wait for the CI matrix to pass, then release a minor version.

## Live tests

The tests against the live API (`livetest/`, the port of the Python
SDK's `tests/test_integration.py`) compile only with the build tag `live`
and are not run by CI: each call to System One is billed, and CI holds no
API key. They run where a maintainer holds a key, with the key in the
environment, never on the command line:

```sh
TYPESAFE_LIVE_TESTS=1 go test -tags live -count=1 -v ./livetest/
```

Each test fails before it calls the API unless `TYPESAFE_LIVE_TESTS` is `1`
and `TYPESAFE_API_KEY` is set; `TYPESAFE_BASE_URL` selects another host.
`go test -list '.*' -tags live ./...` lists them without either variable,
which is how CI's port test matrix check finds them. The untagged tests of
the same package run in CI: the guard, the recorder's credential scrubber,
and the example programs against a local stand-in for the API.

`-args -record` also writes the bodies the API returned to
[`testdata/live`](../testdata/live/README.md), each scrubbed of
credentials before it reaches the disk. The last pass, its results and
the facts it recorded about the API are ledger rows W6.4-01 to W6.4-08
in [`perf/ledger.md`](perf/ledger.md). Observed there, not asserted by
any test:

- The API's HTTP/2 SETTINGS advertise `MAX_CONCURRENT_STREAMS` 100
  (W6.4-04), so the client, which keeps one connection and counts its
  streams strictly, queues a 101st concurrent call on it.
- Asked for gzip, as Go's transport asks by default, the API gzips its
  successful responses; the transport undoes the encoding and reports
  `ContentLength` −1, so the SDK reads the body with no declared length.
  Asked for no encoding, it declares `Content-Length: 311` for the models
  list (W6.4-05). W6.4-08 prices the difference per call: without an
  encoding a call makes 7 fewer allocations and about 10 KiB less
  garbage, client and server counted together, on bodies of 311 and
  401 B. `WithCompression(false)` asks for no encoding; the default stays
  gzip, one of the encodings httpx asks for (owner decision G11 (1)).

## Responses with many answers

A response is read under a limit, 16 MiB unless `WithMaxResponseBytes`
sets another, and the SDK sets no limit on the number of answers in it, as
the Python SDK sets none (owner decision G11 (2)). The decoder keeps one
entry for each answer, of a known type or not, so while a call decodes,
the heap it holds grows with the number of answers more than with the
body's bytes. A 15 MiB body of small answers of an unknown type (688 682
of them in `TestMemStatsFlood`) took 35 times its size in the decoder
alone (review W6.2 MAJ-1) and 36.6 to 49.9 times its size through the
whole call, 575 to 785 MB more than the heap held before it (frozen AC-P5
(viii); ledger W6-secfix-01 and -02). That memory is the call's own and is
released with it: once the call has returned, the decoder the SDK keeps
for later calls holds at most 4 MiB of scratch (`codec.DecoderCeiling`,
frozen with AC-P5), so once the collector has run, what stays live is at
most 4 MiB for each decode that ran at the same time, and it drains two
collections after the traffic falls. What a client that calls a server it
does not trust may hold is set by its response limit and its concurrency:
measured, a call in flight took up to about 50 times the body it read.

## What a kept response holds

A response from a call keeps the header redactor that call used, so that
`DecodeAs` redacts it as `Ask` did however long the caller keeps it (owner
decision G11 (3)). A caller can see three consequences:

- A response to a plain-HTTP request through a proxy keeps the credentials
  of the proxies its client knew when the call returned: for each of up to
  16 proxy userinfos, the password as it is, URL-escaped and word by word,
  and the Basic token sent for it, about 95 B for one proxy with a 20-byte
  password and 1.5 to 2.5 KiB for 16. They stay in memory for as long as
  the caller keeps the response. The redactor is a function value, and
  `fmt` prints a function, not what it holds, so no verb shows them. Over
  HTTPS, or without a proxy, a response keeps its client's configuration,
  API key included, reachable instead.
- A client remembers its 16 most recent distinct proxy userinfos. When a
  call's proxy userinfo is forgotten while that call is still in flight,
  because 16 other ones were used meanwhile, the call is not redacted of
  that credential: `Ask` and `DecodeAs` show it alike where the proxy echoed
  it. Only a proxy function that returns more than 16 distinct credentials
  during one call meets this, and it was so before responses kept their
  redactor.
- Because a response from a call holds that function, `reflect.DeepEqual`
  of it and any other response value, its own copy included, is false (a
  response read back with `UnmarshalJSON` holds none). Compare what
  responses hold instead: `Answers`, `Model` and `Usage`, or the JSON that
  `MarshalJSON` writes.

## Measurement rule

Performance numbers are comparable only when every host builds with the Go
1.27 baseline experiment set.

- On a host whose Go env file (the file `go env GOENV` names) sets
  `GOEXPERIMENT`, every measurement runs with
  **`GOEXPERIMENT=nosimd,noruntimesecret`**. On the maintainer's darwin/arm64
  host the env file sets `simd,runtimesecret`, and the override restores the
  baseline ToolTags:
  `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]`
  (go1.27.1, `go list -f '{{context.ToolTags}}' runtime`, measured
  2026-09-25 15:12:30 JST).
- On every other host (CI runners, the linux/amd64 host) set no override:
  their default already is the baseline.
- Never `GOEXPERIMENT=none`: it also clears Go 1.27's default-on experiments
  (the same command then prints only `regabiwrappers regabiargs`), so it would
  measure the old GC and the generic allocator. Never `GOENV=off`: it changes
  the selected toolchain. `env -u GOEXPERIMENT` does nothing when the setting
  lives in the env file.
- Every row of [`perf/ledger.md`](perf/ledger.md) records the output of
  `go version` and `go list -f '{{context.ToolTags}}' runtime` from the run it
  reports, next to the command, the host and a time taken with `date`.

## Fuzzing

Eight targets run in CI's `fuzz` job for 60 seconds each on `ubuntu-26.04`
(`.github/workflows/ci.yaml`, whose `fuzzed` list names them); their seed
corpora also run as ordinary tests in every `go test` run.

| Target | Package | What it reads |
| --- | --- | --- |
| `FuzzDecodeResponse` | `./internal/codec` | a response body, through the System One and the models decoders |
| `FuzzErrorBody` | `./internal/codec` | an error response body |
| `FuzzDecodePaths` | `./internal/codec` | a program that writes a System One body with repeated members; the visitor and the lazy pass must agree with the body's last-wins reading |
| `FuzzRetryAfter` | `.` | `Retry-After-Ms` and `Retry-After` values |
| `FuzzTagGrammar` | `.` | a `typesafe` struct tag |
| `FuzzFalsyJSON` | `./internal/engine` | a JSON value a question holds (`RawJSON`, JSON `Content`); `FalsyJSON`, which reads its first bytes first, must give the whole-value check's verdict (W5.3; in `internal/engine` since W6.5) |
| `FuzzIsSecretHeader` | `./internal/engine` | a header name; `IsSecretHeader`, which folds an ASCII name in place, must give the verdict of the name lower-cased (W5.3; in `internal/engine` since W6.5) |
| `FuzzValidUTF8` | `./internal/codec` | any byte string; `validUTF8` must give `utf8.Valid`'s verdict, which on amd64 holds sonic's SIMD validator to Go's (W6-fixes, owner ruling G8-b; `TestValidUTF8Parity` covers the short inputs exhaustively) |

`FuzzAppendJSON` (`./internal/codec`, `./internal/wire`) and `FuzzValidString`
(`./internal/codec`) run only their seed corpora; the job's `seeded` list
names them, and a target in neither list fails the job.

Run one target locally, one package per command (`go test -fuzz` takes one
target):

```sh
go test -run '^$' -fuzz '^FuzzDecodePaths$' -fuzztime 10m ./internal/codec/
```

Add `GOEXPERIMENT=nosimd,noruntimesecret` where the measurement rule above
asks for it, and `-parallel N` to leave cores to other work (the default is
one worker per core).

- Every input must finish within 10 seconds (`testsupport.FuzzInputBound`).
  Go's fuzzing engine has no per-input timeout, so each target arms a
  watchdog (`testsupport.BoundFuzzInput`) that panics when an input runs
  longer. The engine then reports "fuzzing process hung or terminated
  unexpectedly" and writes the input, as for a crash.
- A failing input is written to `testdata/fuzz/<Target>/<hash>` in the
  target's package. CI's `fuzz-failures` artifact holds each one at that path
  relative to the repository root (for example
  `internal/codec/testdata/fuzz/FuzzDecodePaths/<hash>`); copy it there in a
  checkout and replay it on its own with
  `go test -run '^FuzzDecodePaths/<hash>$' ./internal/codec/`. Commit it
  with the fix: it then runs in every `go test` run as a regression case.
- The inputs a campaign finds interesting stay in the Go build cache
  (`$(go env GOCACHE)/fuzz`), not in the tree, and a later campaign starts
  from them.
- `testdata/fuzz/FuzzDecodeResponse`, `FuzzErrorBody` and `FuzzRetryAfter`
  hold the Rust SDK's `fuzz/corpus` (typesafe-sdk-rust 34c3b7c), one file per
  input, byte for byte: `decode_response` for the first two (the Rust target
  reads every body as an error body too), `retry_after` for the third, whose
  input layout (the first byte picks the headers) `FuzzRetryAfter` keeps.

## Package layout (as built, W6.5)

W6.5 moved the stages of a call out of the root package (owner
instruction G9, design D1 of the W6.5 design report; the port plan's
sections 4, 11 and 12 describe the layout before it):

- `internal/engine` holds the call's stages: the request body's assembly
  (`EncodeBody`, generic over the root package's `RawJSON` and `Content`),
  the body read (`ReadBody`), the decode's entry (`DecodeSystemOneInto`),
  header redaction, the credential scrub, the falsiness check, the
  transport container with the trace-hook shield, and the state behind
  the root package's `Client`, `Prepared` and `SystemOneResponse`. It
  imports `internal/codec`, `internal/wire` and `internal/h2gate`, never
  the root package.
- The root package keeps every public type with its methods and
  documentation. `Client`, `Prepared` and `SystemOneResponse` are defined
  types over the engine's state (`type Client
  engine.Client[RetryPolicy]`, `type Prepared engine.Prepared`, `type
  SystemOneResponse engine.Response`): the conversion is free, the public
  method set is the root package's, and the engine's accessors do not join
  it. The other public types are wrapper structs over `internal/wire`
  values, as before; no type is an alias.
- `internal/alloctest` (test files only) holds the root package's
  allocation budgets. The budget list runs there:
  `go test -list "^($ALLOC)$" ./internal/alloctest/` and
  `go test -run "^($ALLOC)$" -count=1 -v ./internal/alloctest/`, and
  CI's allocation-budget step runs `run_budgets ./internal/alloctest/`.
  Its `//go:build !race` scan reads every tracked test file the go
  command builds: a `!race` test of `internal/alloctest` must be in the
  step's list, and one in any package that no step without `-race` runs
  (the root package among them) fails the step by name; the allowed
  packages are the ones the steps run, the budget step's and the test
  job's `NORACE_PKGS` (critic-p6 m-1, n-11).
- `unsafe` stays under `internal/codec` itself (no package below it),
  `internal/testsupport/naive`, and the root package's typed store,
  `decodeas_store.go`; `internal/engine` imports no `unsafe` and uses no
  raw-pointer route (K40, STANDING 3), and no file of the module imports
  `"C"` (review V81 NIT 1). The seam tests hold `internal/engine` to
  every rule of the root package.
- CI's `-race` coverage step runs with `-coverpkg=./...` (owner ruling
  G10), so a block is covered when any test of the module runs it.

