# Performance ledger

Every performance number the port relies on is recorded here, one row per
measurement, before any acceptance criterion uses it. The rules for the
experiment set are in [`../support.md`](../support.md#measurement-rule).

## Hosts

| Host | Description |
| --- | --- |
| (M) | darwin/arm64, Apple M3 Max; the Go env file sets experiments, so every run uses `GOEXPERIMENT=nosimd,noruntimesecret` |
| (L) | linux/amd64, Intel Xeon Platinum 8481C, 44 vCPU; no override |
| CI | GitHub-hosted runner named in the row; no override |

## Row format

| Column | Content |
| --- | --- |
| # | Row number, never reused. |
| When | Output of `date '+%Y-%m-%d %H:%M:%S %Z'`, taken in the same shell command as the measurement. |
| Wave | Plan wave or spike ID (for example `W0.3 S-E1`). |
| Host | `(M)`, `(L)` or the CI runner label. |
| `go version` | Output of `go version` in the measuring environment. |
| ToolTags | Output of `go list -f '{{context.ToolTags}}' runtime` in the measuring environment. |
| Command | The exact command, with any `GOEXPERIMENT` prefix. |
| Result | Numbers with units (ns/op, B/op, allocs/op, connections, ...); `benchstat` summaries for repeated runs. |
| Notes | Fixture, input size, anything that affects comparability. |

## Rows

| # | When | Wave | Host | `go version` | ToolTags | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
