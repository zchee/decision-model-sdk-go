# system-one-adapter-go

`github.com/zchee/typesafe-sdk-go/adapter` answers the
[TypeSafe](https://typesafe.ai) System One API with an LLM. It is a Go port
of
[system-one-adapter-python](https://github.com/typesafe-ai/system-one-adapter-python)
0.2.1 (commit `e1d4cc938204b22fc5a3c3aca7044072fe3f712d`), and is used
through the Go SDK of this repository as the `http.RoundTripper` of
`typesafe.WithRoundTripper`. Its purpose is upstream's: comparing TypeSafe
against an LLM on cost, speed and intelligence.

**Status: not released.** The module is being written; it has no exported
API beyond its version constants yet, and no tag.
[`docs/port-test-matrix.md`](docs/port-test-matrix.md) shows which of
upstream's tests are ported, and [`docs/deviations.md`](docs/deviations.md)
where the port behaves differently.

It is its own Go module, nested in the repository of
[typesafe-sdk-go](https://github.com/zchee/typesafe-sdk-go); its tags will
be `adapter/vX.Y.Z`.

## License

Licensed under the Apache License, Version 2.0 ([LICENSE](LICENSE)). Parts of
this module are ported from system-one-adapter-python, and
`testdata/cassettes/` and `testdata/expected/` are copies of its test files;
those are under upstream's MIT license
([LICENSE-UPSTREAM](LICENSE-UPSTREAM)).
