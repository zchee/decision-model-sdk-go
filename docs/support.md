# Support

## Support matrix

| Go | GOARCH | GOOS | Status |
| --- | --- | --- | --- |
| 1.27.x | `amd64`, `arm64` | any GOOS the Go release supports on that architecture | supported |
| 1.27.x | any other (`386`, `riscv64`, `wasm`, …) | any | refused at compile time |
| 1.28 and later | any | any | refused at compile time until the bump below |
| 1.26 and earlier | any | any | cannot build the module (`go.mod` says `go 1.27`) |

CI runs the tests on `ubuntu-26.04` (linux/amd64), `xcode-27` (darwin/arm64)
and `windows-2025` (windows/amd64).

The support window is the set of Go releases that the newest tag of
`github.com/bytedance/sonic` supports. sonic is the SDK's only JSON codec, and
its JIT path compiles only for
`(amd64 && go1.17 && !go1.28) || (arm64 && go1.20 && !go1.28)` (the build line
of `sonic.go` in v1.15.4). Everywhere else sonic silently falls back to
`encoding/json` and prints a warning at init; the SDK refuses to compile there
instead.

## The compile-time refusal

`internal/codec` is the only package that imports sonic. It lands in wave W0.2
of the port plan with these build constraints:

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
toolchain) must each print the identifier. The weekly `gotip` workflow checks
the same with the development toolchain; any other outcome is a canary failure.
The package's seam test asserts that every `internal/codec` file carries exactly
one of the two constraint lines.

`internal/codec` also imports `encoding/json`, only for the `json.Number` type
that sonic's `ast.Visitor` interface requires; nothing is encoded or decoded
through it.

## Bump procedure for Go 1.28

On Go 1.28 GA day every consumer on Go 1.28 gets the compile error above until
sonic and the SDK both move. From `go1.28rc1` on, the weekly `gotip` workflow
downloads the newest sonic and opens or updates the issue "Go 1.28: waiting on
sonic" while that version's `sonic.go` build line still carries `!go1.28`.

When a sonic tag without `!go1.28` exists:

1. Bump sonic: `go get github.com/bytedance/sonic@<tag> && go mod tidy`.
2. In **one** edit, move every `internal/codec` constraint:
   - `unsupported.go` → `//go:build go1.29 || !(amd64 || arm64)`;
   - every other file, tests included → `//go:build !go1.29 && (amd64 || arm64)`;
   - the seam test's two expected constraint lines, to match.

   Moving only `unsupported.go` would be wrong: the other files would keep
   `!go1.28`, exclude themselves on Go 1.28, and leave the package empty on a
   supported release.
3. Re-run the refusal checks (`GOARCH=386`, `GOARCH=riscv64`, and the
   next-release stand-in tag, now `-tags go1.29`; `go vet` and `go build`).
4. Wait for the CI matrix to pass, then release a minor version.

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
