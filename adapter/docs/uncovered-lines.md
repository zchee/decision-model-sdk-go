# Uncovered blocks

`TestUncoveredLinesListed` checks this manifest against a freshly generated
atomic coverage profile. Paths are repository-relative; block coordinates
are the profile's 1-based byte columns, with exclusive end columns. Profiles
from multiple test binaries are merged by the largest hit count per block,
so one binary's uncovered copy does not override another binary's hit.
Every included zero-count block needs a nonempty reason. A covered or missing
block makes a fixed-count row stale. `0-1` is reserved for the two dynamic
logging-level guards; it is not permission to omit an unlisted block.

From the adapter module:

```sh
go test -race -count=1 -coverpkg=./... -coverprofile=coverage.out -covermode=atomic ./...
go test -race -count=1 -run '^TestUncoveredLinesListed$' . -args -coverprofile-in=coverage.out
```

Without `-coverprofile-in`, the test still validates the manifest's shape,
unique keys, nonempty reasons, exclusions and existing source coordinates;
it does not claim that a measured profile was checked. The explicit second
command is the coverage gate and is required in CI.

The excluded prefixes match the adapter test-support paths excluded by the
repository's `.codecov.yaml`: `adapter/examples/`, `adapter/livetest/`,
`adapter/internal/cassette/` and `adapter/internal/fake/`. Cassette coverage
has its separate test gate. Live coverage is not inferred from offline tests.
The initial rows were measured with the ordinary atomic profile at
`70d8b3b7ec2162088c3aaa1d351d371f2c4c3bb2`; the coverage gate requires them to
remain correct at each subsequent head, rather than trusting that snapshot.

| File | Block | Uncovered | Reason |
| --- | --- | --- | --- |
| `adapter/evaluate.go` | `264.5,265.1` | 1 | Defensive conversion error while turning already-parsed score criteria into legend values; the validated criterion nodes used by evaluations convert successfully. |
| `adapter/evaluate.go` | `331.4,332.1` | 1 | Defensive non-validation error from output validation; the validator returns a typed validation error for malformed model output, so the tested correction paths do not enter this fallback. |
| `adapter/model.go` | `83.36,83.54` | 1 | The private refusal's Error method is not called by the HTTP classification paths, which inspect the refusal's typed status, message and error type instead. |
| `adapter/seam.go` | `150.3,150.123` | 1 | Internal evaluation fallback after parsed questions; the exercised evaluation refusals are the specific invalid-state depth error, while accepted questions do not otherwise reach this branch. |
| `adapter/seam.go` | `281.3,282.1` | 1 | Defensive conversion of a non-refusal error in refusalReply; its exercised callers supply typed refusals. |
| `adapter/seam.go` | `296.3,297.1` | 1 | Defensive non-question-validation error in question refusal construction; the non-object input that could cause it is rejected by parseRequest before ParseQuestions. |
| `adapter/seam.go` | `331.5,332.1` | 1 | Propagation of report.members failure during an error reply; the report assembled from validated nodes in these paths serializes successfully. |
| `adapter/seam.go` | `348.4,349.1` | 1 | Propagation of report.members failure during a failure reply; the exercised generated reports contain supported values. |
| `adapter/seam.go` | `367.4,368.1` | 1 | Propagation of report.members failure during a successful reply; the report assembled after output validation contains supported values. |
| `adapter/seam.go` | `617.3,618.1` | 0-1 | Debug logging can be disabled between buffering an attempt and flushing it; fixed-level test handlers normally keep the level enabled for the recorded attempts. |
| `adapter/seam.go` | `652.3,653.1` | 0-1 | Info logging can be disabled before the final report event; fixed-level test handlers normally keep the level enabled throughout the call. |
| `adapter/internal/jsonx/state.go` | `186.2,186.70` | 1 | Defensive unexpected-token fallback after PeekKind and ReadToken; the decoder reports unsupported tokens as syntax errors before this fallback. |
| `adapter/internal/jsonx/state.go` | `200.3,201.1` | 1 | Defensive failure reading the array closing token after a successful PeekKind of the close; immutable in-memory input has already supplied that valid token. |
| `adapter/internal/jsonx/state.go` | `232.3,233.1` | 1 | Defensive failure reading the object closing token after a successful PeekKind of the close; immutable in-memory input has already supplied that valid token. |
| `adapter/internal/jsonx/value.go` | `177.4,177.9` | 1 | Encoder failure on an object's opening token; the exercised value trees and in-memory destination allow the opening token, with invalid values tested in their own write branches. |
| `adapter/internal/schema/schema.go` | `322.24,322.24` | 1 | Empty switch clause selecting the already-initialized probability header for non-discrete mode; this zero-statement coverage block has no executable body. |

The case-alias recorder regression has two measured skips on a
case-sensitive Linux filesystem because upper-case directory names are
distinct there. Those support-package skips are not coverage claims about
Windows or a live provider. Permission checks likewise require measured
filesystem capability; no platform-wide success is inferred from a skip.
