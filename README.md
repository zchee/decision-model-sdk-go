# typesafe-sdk-go

**Status: under construction.** typesafe-sdk-go is a Go port of
[typesafe-sdk-python](https://github.com/typesafe-ai/typesafe-sdk-python) 0.7.1,
the client for the TypeSafe System One API. The package exports no API yet;
the port proceeds wave by wave, and
[`docs/port-test-matrix.md`](docs/port-test-matrix.md) tracks every upstream
test on its way to a Go test or a documented deviation.

- The SDK supports Go 1.27.x on `amd64` and `arm64`, the releases the newest
  `github.com/bytedance/sonic` tag supports. A `go` command from Go 1.21 to
  1.26 switches to a Go 1.27 toolchain through the `go.mod` line (with
  `GOTOOLCHAIN=auto`, the default); Go 1.17 to 1.20 attempt the build and
  print `note: module requires Go 1.27` when it fails. On any other GOARCH,
  or on Go 1.28 and later, the build fails on purpose with the error
  `undefined: typesafe_sdk_go_requires_go1_17_to_go1_27_on_amd64_or_arm64`
  ([`docs/support.md`](docs/support.md) explains why and holds the Go 1.28
  bump procedure).
- Behaviour that deliberately differs from the Python SDK: the deviation table
  (the port plan's Appendix B) is to come in `docs/`.

Licensed under the Apache License, Version 2.0 ([LICENSE](LICENSE)); see
[LICENSE-THIRD-PARTY](LICENSE-THIRD-PARTY) for dependencies.
