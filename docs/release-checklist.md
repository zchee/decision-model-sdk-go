# Release checklist for v0.1.0

The port plan tags `v0.1.0` after the exit evidence of every phase, green
CI on `main`, the CodSpeed baseline, the Codecov gate, a clean
`govulncheck`, the README's deviations checked against the plan's
Appendix B, the newest versions of the direct dependencies, and a run of
every allocation test and of B1–B6 on the arm64 host (plan §7 Phase 7,
risks K6, K7, K17 and K18). W7 ran that list; this page records each line
with its evidence, when the evidence was taken, and PASS or STOP. The tag
is the owner's act.

The runs of record ran at f7c0ff3, the commit that sets `typesafe.Version`
to 0.1.0, on `main` e973adc. One later commit changes a test, 8490c48
(`internal/testsupport/loopback_test.go` only, line 19), and the rows that
a test binary of `internal/testsupport` produced were taken again there;
the other commits after f7c0ff3 change documents only. Times are those that
the command which took the evidence printed with `date`, or a tool's own
timestamp. The raw files are in the private archive at `spikes@f1d90ae672f9:w7/`
(the [ledger's](perf/ledger.md) first paragraphs say how a citation
resolves), and the ledger's section `## W7` holds the measurement rows.
W7's own head is dispatched after this page is committed, so the run ids
of its CI and benchmark runs, its R119 and its last R111 are in the landing
record, not here.

| # | Line | Evidence | When | Result |
| --- | --- | --- | --- | --- |
| 1 | Exit evidence, Phases 0 to 5 | VERIFY P0 PASS at 5b2a214 (V10), CRITIC P0 APPROVE (V11); VERIFY P1 PASS at 2580833 (V14c); VERIFY P2 PASS at 85f059e (V28) and 520cd85 (V29), CRITIC P2 APPROVE at 484d4f5 (V38); VERIFY P3 PASS at 3c76894 (V49), CRITIC P3 APPROVE (V50); VERIFY P4 PASS at a0e11ac (V54), CRITIC P4 APPROVE (V55); VERIFY P5 PASS at 017302f (V68), CRITIC P5 APPROVE WITH CONDITIONS (V69), the conditions closed by the owner's G8-a and G8-b ([as-built](as-built.md), each phase's gate line) | 2026-09-25 to 2026-09-26 | PASS |
| 2 | Exit evidence, Phase 6 | VERIFY P6 at the rewritten head 23789e2: FINAL-LOCAL PASS (V119), the dispatch's CI and benchmark runs PASS (V120, V124), PASS with both attempts of `main`'s run 36310615348 stated (V126). CRITIC P6 FINAL ACCEPT-WITH-RESERVATIONS (V125), reopened on K25 and closed at e973adc by its FINAL-CLOSE (V129), where `main`'s own runs are green on their first attempt (row 3): the gate is closed at e973adc | 2026-09-27 09:35Z to 11:45Z | PASS |
| 3 | `main`'s CI green at the base | `main` e973adc: ci.yaml 36315492342 (push, attempt 1, 5 of 5 jobs success) and bench.yaml 36315492374 (push, attempt 1, success); ci.yaml 36314851625 (dispatch, success). `spikes@f1d90ae672f9:w7/main-runs-e973adc8c3b9ed56b10e50ac06b36ae4d1b533e9.txt` | 2026-09-27 11:21:57Z to 11:32:57Z | PASS |
| 4 | CodSpeed baseline, AC-P7 | The gate is on (G8-a): bench.yaml fails when `BenchmarkCall/sdk`'s mean is not below `BenchmarkCall/naive`'s in the same run. `main` at e973adc: "AC-P7 verdict: pass BenchmarkCall/sdk mean 3048.1 ns / BenchmarkCall/naive mean 3430.2 ns = 0.888613 < 1.0" on AMD EPYC 9V45 96-Core Processor; `main` at 23789e2: 0.837261 on AMD EPYC 7763 64-Core Processor. K7's bookkeeping by CPU model (R109b; not a gate) is ledger W7-05. CodSpeed's own "Performance Analysis" check and every absolute time are report-only (K37, G8-a) | 2026-09-27 11:32:52Z (the gate step's log) | PASS |
| 5 | Codecov gate (R119) at the base | `codecov/project`: "98.0% (target 85.0%)", success, 11:14:01Z. Codecov's API: state complete, 98.03 %, 51 files, 4 589 lines, 4 499 hits, 90 misses, 6 sessions. `spikes@f1d90ae672f9:w7/codecov-base-e973adc8c3b9ed56b10e50ac06b36ae4d1b533e9.txt` | 2026-09-27 20:26:46 JST | PASS |
| 6 | `govulncheck` clean (R30) | govulncheck v1.8.0 at f7c0ff3, database https://vuln.go.dev last modified 2026-09-24T20:07:49Z, go1.27.1, symbol scan: 0 findings, exit status 0. `spikes@f1d90ae672f9:w7/govulncheck-M-f7c0ff32527eb5812c7c116718e66ab7f9abb870.txt` | 2026-09-27 20:27:18 JST | PASS |
| 7 | The README's deviations against Appendix B (deliverable B) | `port-test-matrix.py --no-planned --deviations docs/deviations.md --as-built docs/as-built.md`, CI's check 8, at 8490c48: 129 upstream tests (94 ported, 35 deviation, 0 planned); 47 Appendix B rows, 27 of them bold; 56 keys; 284 rulings; 207 Go names, each listed by `go test -list`. `spikes@f1d90ae672f9:w7/mlint/port-test-matrix.txt` | 2026-09-27 20:56:55 JST | PASS |
| 8 | The newest dependencies | `go list -m -u all` at f7c0ff3: `github.com/bytedance/sonic` v1.15.4, `github.com/google/go-cmp` v0.7.0 and `golang.org/x/net` v0.59.0, none with a newer version. `spikes@f1d90ae672f9:w7/deps-M-f7c0ff32527eb5812c7c116718e66ab7f9abb870.txt` | 2026-09-27 20:24:41 JST | PASS |
| 9 | K18: every allocation test and B1–B6 on the arm64 host, under the bench lock, with the load recorded | Ledger W7-01 (the allocation lists, 22 of 22 tests) and W7-03 (B1–B6, 125 rows, five rounds, 1-minute load 2.89 to 8.02) on (M), darwin/arm64; W7-02 and W7-04 the same on (L), linux/amd64 | 2026-09-27 20:24:31 to 20:58:33 JST | PASS |
| 10 | K6: the budget on the live recordings | Ledger W7-01 and W7-02: `live/questions.json` and `live/typed-response.json` decode in 4 allocations and 720 B on both hosts, as `result.json`'s pin of 4, under NF2's ceiling of 8 | 2026-09-27 20:24:31 JST (M), 11:32:22Z (L) | PASS |
| 11 | K17: the Go support window | [`support.md`](support.md) states the window (Go 1.27.x on amd64 and arm64, the releases the newest sonic tag supports), the compile-time refusal and its identifier, and the Go 1.28 bump procedure; `TestSeamD1IdentifierSites` and CI's step "D1 refusal off the support matrix" pin it | the lint chain at 8490c48, 2026-09-27 20:56:46 JST | PASS |
| 12 | No commit or tree of `main` or of W7's branch holds the spikes | 431 commits reachable from e973adc and 454 from 6d18d26, an ancestor of W7's head: 0 touch the old spike directories and 0 objects lie at a path under them; GitHub held `main` e973adc and `wave/w7` (the rewritten branch before this lane's final push), and 0 tags. The commits after 6d18d26 add no such path (part 1 of the citation checker, line 14). `spikes@f1d90ae672f9:w7/purge-check-6d18d26044b1e93169d3e60abd3989656abf0aa8.txt` | 2026-09-27 20:25:00 JST | PASS |
| 13 | The archive holds every cited commit | The documents cite archive commits 815453827b43, on GitHub since the purge, and f1d90ae672f9, this page's, which the lead pushes as a fast-forward after a scan, when W7 lands | before the landing | STOP until the push |
| 14 | Every citation resolves (part 2 of the checker) | `.github/scripts/spikes-citations.py --archive <archive clone> --history <SDK clone>` on (M) at the commit of this page: part 1, 137 citations well-formed and 0 failures; part 2, 137 of 137 resolve in the archive, 1 442 SHA tokens name commits of the history, 486 name none (trees, run ids, digests) and 0 name commits from before the rewrite; exit status 0 | 2026-09-27 21:04:31 JST | PASS |
| 15 | R111 and the gates of each commit | R111 on (L) for each of W7's commits alone, 25 before this page's commit, each rc 0 with 10 packages ok (`spikes@f1d90ae672f9:w7/r111/`); the lint chain at 8490c48, 9 of 9 steps rc 0 (`spikes@f1d90ae672f9:w7/mlint/`); `-race` at W7's head and this page's commit's R111 are in the landing record | 2026-09-27 20:24:25 to 21:01:23 JST | PASS |
| 16 | `Version` 0.1.0 and the API golden (R120) | f7c0ff3: `typesafe.Version` is `0.1.0`, and `testdata/api/public-api.txt` was rewritten by `-update` in the same commit, one line; the client options golden is unchanged | 2026-09-27 | PASS |
| 17 | The changelog | [`CHANGELOG.md`](../CHANGELOG.md), `[0.1.0] - 2026-09-27` | 2026-09-27 | PASS |
| 18 | The module zip | 383 files and 1 561 045 B at e973adc, 385 files and 1 614 127 B at f7c0ff3 (golang.org/x/mod/zip); no spike file. The size at the landed commit is in the tag proposal. `spikes@f1d90ae672f9:w7/zipsize-f7c0ff32527eb5812c7c116718e66ab7f9abb870.txt` | 2026-09-27 20:24:50 JST | PASS |
| 19 | K25's fix and T-1 | e973adc (K25, review V128) and 8490c48 (T-1, ruling D-T1-in-W7): the subtest passes 20 of 20; the reviewer's mutant that counts an accept twice fails 20 of 20; the critic's forced probe, a third dial counted between two readings, fails 60 of 60 with e973adc's body and passes 60 of 60 with 8490c48's, the forced order confirmed 60 times; `internal/testsupport` under `-race -count=30 -cpu 1,2,4` on (L): 0 FAIL, 0 DATA RACE; R111 alone and the lint chain rc 0. `spikes@f1d90ae672f9:w7/t1/` | 2026-09-27 20:54:13 to 20:58:12 JST | PASS |
| 20 | The tag | The owner's act. The rule for it (D-gate-red-run-a1): at the tagged commit every ci.yaml job is green on its first attempt; bench.yaml's job with its AC-P7 step is green on its first attempt, a failure of the AC-P7 step by noise being judged by one re-run with both host lines quoted (G8-b); `codecov/project` names a percentage and the target, beside Codecov's API totals. CodSpeed's own check and every absolute time are report-only | after the landing | STOP (the owner's) |

## After the tag (the owner's)

| # | Check | How |
| --- | --- | --- |
| T1 | The Go proxy serves the version | `GOPROXY=https://proxy.golang.org go list -m github.com/zchee/typesafe-sdk-go@v0.1.0` prints `github.com/zchee/typesafe-sdk-go v0.1.0` |
| T2 | pkg.go.dev shows it | open `https://pkg.go.dev/github.com/zchee/typesafe-sdk-go@v0.1.0`; a first visit asks the site to fetch it |
| T3 | A user can build against it | in an empty directory, `go mod init example.com/try && go get github.com/zchee/typesafe-sdk-go@v0.1.0`, then build the README's quick start |
| T4 | The proxy's zip is the one measured | `curl -sSfO https://proxy.golang.org/github.com/zchee/typesafe-sdk-go/@v/v0.1.0.zip`, then `unzip -l v0.1.0.zip`: the file count and size the tag proposal states, and no spike file |
