# typesafe-sdk-go

**Status: under construction.** typesafe-sdk-go is a Go port of
[typesafe-sdk-python](https://github.com/typesafe-ai/typesafe-sdk-python) 0.7.1,
the client for the TypeSafe System One API. The package exports no API yet;
the port proceeds wave by wave, and
[`docs/port-test-matrix.md`](docs/port-test-matrix.md) tracks every upstream
test on its way to a Go test or a documented deviation.

- Supported Go releases and platforms, and the Go 1.28 bump procedure:
  [`docs/support.md`](docs/support.md).
- Behaviour that deliberately differs from the Python SDK: the deviation table
  (the port plan's Appendix B) is to come in `docs/`.

Licensed under the Apache License, Version 2.0 ([LICENSE](LICENSE)); see
[LICENSE-THIRD-PARTY](LICENSE-THIRD-PARTY) for dependencies.
