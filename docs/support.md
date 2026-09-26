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

The tests against the live API (`livetests/`, the port of the Python
SDK's `tests/test_integration.py`) compile only with the build tag `live`
and are not run by CI: each call to System One is billed, and CI holds no
API key. They run where a maintainer holds a key, with the key in the
environment, never on the command line:

```sh
TYPESAFE_LIVE_TESTS=1 go test -tags live -count=1 -v ./livetests/
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
the facts it recorded about the API (latency, `MAX_CONCURRENT_STREAMS`,
framing) are ledger rows W6.4-01 to W6.4-06 in
[`perf/ledger.md`](perf/ledger.md).

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

Seven targets run in CI's `fuzz` job for 60 seconds each on `ubuntu-26.04`
(`.github/workflows/ci.yaml`, whose `fuzzed` list names them); their seed
corpora also run as ordinary tests in every `go test` run.

| Target | Package | What it reads |
| --- | --- | --- |
| `FuzzDecodeResponse` | `./internal/codec` | a response body, through the System One and the models decoders |
| `FuzzErrorBody` | `./internal/codec` | an error response body |
| `FuzzDecodePaths` | `./internal/codec` | a program that writes a System One body with repeated members; the visitor and the lazy pass must agree with the body's last-wins reading |
| `FuzzRetryAfter` | `.` | `Retry-After-Ms` and `Retry-After` values |
| `FuzzTagGrammar` | `.` | a `typesafe` struct tag |
| `FuzzFalsyJSON` | `.` | a JSON value a question holds (`RawJSON`, JSON `Content`); `falsyJSON`, which reads its first bytes first, must give the whole-value check's verdict (W5.3) |
| `FuzzIsSecretHeader` | `.` | a header name; `isSecretHeader`, which folds an ASCII name in place, must give the verdict of the name lower-cased (W5.3) |

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
