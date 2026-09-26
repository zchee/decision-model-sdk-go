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
| Load | `uptime` load averages (1 min) before → after the run, taken in the same shell as the measurement (inside the lock for locked runs); `noisy` when either exceeds the host's core count ((M) 16, (L) 44). |
| Command | The exact command, with any `GOEXPERIMENT` prefix. |
| Result | Numbers with units (ns/op, B/op, allocs/op, connections, ...); `benchstat` summaries for repeated runs. |
| Notes | Fixture, input size, anything that affects comparability. |

## Rows

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W0.4-01 | 2026-09-25 07:26:02 UTC | W0.4 S-T1 failure cases | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0 → 0 | `sh $R '(L)' $LO none l-st1-failures -count=5 -run '^TestST1(LeaderCancelled\|LeaderVanish\|TLSSilent\|WaiterBound)$' -v ./_spikes/s-t/` | identical in 5/5 runs: leader cancelled → waiters 63/63 200, handovers 1, accepts 1; leader vanish → gate cold, next caller leads, accepts 1; TLS-silent (500 ms) → leader `*DialError{Timeout}` after 500.631 ms (median), 64 distinct error values, 63 waiters share the leader's cause, gate cold, accepts 1; no gate → 6 serial dials, classes map[deadline:3 other:5]; waiter bound → 63 fall-throughs, 64/64 200, accepts 1 | classification, no lock; `results/l-st1-failures.txt` |
| W0.4-02 | 2026-09-25 07:26:21 UTC | W0.4 S-T1 HTTPAuto (K20) | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0 → 0 | `sh $R '(L)' $LO none l-st1-auto -count=5 -run '^TestST1Auto$' -v ./_spikes/s-t/` | gate: accepts 1 in 50/50; no gate: accepts min 24 / median 63 / max 64 over 50 bursts, of which 1 connection carries every request | connection counts, no lock; `results/l-st1-auto.txt` |
| W0.4-03 | 2026-09-25 07:26:22 UTC | W0.4 S-T2 to S-T5b | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0 → 0 | `sh $R '(L)' $LO none l-st2-5 -count=1 -timeout 900s -run '^TestST[2345]' -v ./_spikes/s-t/` | S-T2: cold accepts 1, warm 1, after 31 s 1, after 91 s idle 2; idle-close race: close 6 `unexpected EOF`, 94 ok; reset 2 `read tcp 127.0.0.1:P->127.0.0.1:P: read: connection reset by peer`, 1 `write tcp 127.0.0.1:P->127.0.0.1:P: write: connection reset by peer`, 97 ok; GOAWAY+close 100 ok | classification, no lock; `results/l-st2-5.txt` |
| W0.4-04 | 2026-09-25 07:26:23 UTC | W0.4 S-T2 F1 on fake time ×20 | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0 → 0 | `sh $R '(L)' $LO none l-st2-f1-rate -count=20 -run '^TestST2F1$' -v ./_spikes/s-t/` | 16 calls vs Go's server limit 4, 2 s deadline, 20 runs: strict: 0/20 all ok, 20/20 with deadlines, 3/20 with a `PROTOCOL_ERROR`, connections 1-2; nonstrict: 17/20 all ok, 0/20 with deadlines, 3/20 with a `PROTOCOL_ERROR`, connections 2-4; strict+token: 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 1; strict+token-first: 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 1 | classification under synctest; `results/l-st2-f1-rate.txt` |
| W0.4-05 | 2026-09-25 07:26:24 UTC | W0.4 S-T1 cold 64 fan-out | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0 → 0 | `sh $R '(L)' $LO /tmp/ts-spike/bench.lock l-st1-fanout -count=5 -run '^TestST1FanOut$' -v ./_spikes/s-t/` | gate: 1 in 50/50 one-connection bursts, warm 0 in 50/50, ordering 10/10 in 5/5 runs, waiter wire p50 1.271 (1.246-1.661) / p99 2.357 (2.044-2.719) ms; no gate: 1 in 50/50, all-caller wire p50 1.232 (1.174-1.645) / p99 2.211 (2.024-2.412) ms; gate+token: 1 in 50/50, waiter wire p50 1.49 (1.446-1.651) / p99 2.507 (2.417-3.119) ms | [S-T1](#s-t1-cold-start-gate); `results/l-st1-fanout.txt` |
| W0.4-06 | 2026-09-25 07:26:25 UTC | W0.4 S-T1b / F1 matrix | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0 → 0 | `sh $R '(L)' $LO /tmp/ts-spike/bench.lock l-st1b -count=5 -timeout 900s -run '^TestST1bStreamLimit$' -v ./_spikes/s-t/` | 200 vs 8 strict: ok 1 (1-3), deadline 199 (197-199), accepts 1 (1-1); non-strict: ok 200 (200-200), accepts 21 (18-22), wall 1027.2 ms; strict+token-first: ok 200 (200-200), accepts 1 (1-1), wall 271.5 ms | [F1 (L)](#f1-results-l); the live API's 1024 is cited, not measured ([mechanism](#mechanism-goroot-userszcheesdkgo1271src)); `results/l-st1b.txt` |
| W0.4-07 | 2026-09-25 07:27:26 UTC | W0.4 S-T1b stall rate | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0 → 0 | `sh $R '(L)' $LO /tmp/ts-spike/bench.lock l-st1b-stall -count=1 -timeout 900s -run '^TestST1bStallRate$' -v ./_spikes/s-t/` | reps (of 20) with a missed 500 ms deadline (refused streams in total): strict+token 0 (refused 0) and 0 (refused 0); strict+token-first 0 (refused 0) and 0 (refused 0); non-strict+limiter100 13 (refused 531) and 17 (refused 251) (64 vs 4, 200 vs 8) | `results/l-st1b-stall.txt` |
| W0.4-08 | 2026-09-25 16:26:07 JST | W0.4 S-T1 failure cases | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.41 → 11.49 | `GOEXPERIMENT=nosimd,noruntimesecret sh $R '(M)' $MO none m-st1-failures -count=5 -run '^TestST1(LeaderCancelled\|LeaderVanish\|TLSSilent\|WaiterBound)$' -v ./_spikes/s-t/` | identical in 5/5 runs: leader cancelled → waiters 63/63 200, handovers 1, accepts 1; leader vanish → gate cold, next caller leads, accepts 1; TLS-silent (500 ms) → leader `*DialError{Timeout}` after 501.331 ms (median), 64 distinct error values, 63 waiters share the leader's cause, gate cold, accepts 1; no gate → 6 serial dials, classes map[deadline:3 other:5]; waiter bound → 63 fall-throughs, 64/64 200, accepts 1 | classification; `results/m-st1-failures.txt` |
| W0.4-09 | 2026-09-25 16:26:26 JST | W0.4 S-T1 HTTPAuto (K20) | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 11.49 → 11.49 | `GOEXPERIMENT=nosimd,noruntimesecret sh $R '(M)' $MO none m-st1-auto -count=5 -run '^TestST1Auto$' -v ./_spikes/s-t/` | gate: accepts 1 in 50/50; no gate: accepts min 51 / median 64 / max 64 over 50 bursts, of which 1 connection carries every request | connection counts; `results/m-st1-auto.txt` |
| W0.4-10 | 2026-09-25 16:26:27 JST | W0.4 S-T2 to S-T5b | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 11.49 → 11.49 | `GOEXPERIMENT=nosimd,noruntimesecret sh $R '(M)' $MO none m-st2-5 -count=1 -timeout 900s -run '^TestST[2345]' -v ./_spikes/s-t/` | S-T2: cold accepts 1, warm 1, after 31 s 1, after 91 s idle 2; idle-close race: close 10 `unexpected EOF`, 90 ok; reset 8 `read tcp 127.0.0.1:P->127.0.0.1:P: read: connection reset by peer`, 2 `write tcp 127.0.0.1:P->127.0.0.1:P: write: broken pipe`, 90 ok; GOAWAY+close 2 `unexpected EOF`, 98 ok | classification; `results/m-st2-5.txt` |
| W0.4-11 | 2026-09-25 16:26:28 JST | W0.4 S-T2 F1 on fake time ×20 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 11.49 → 11.49 | `GOEXPERIMENT=nosimd,noruntimesecret sh $R '(M)' $MO none m-st2-f1-rate -count=20 -run '^TestST2F1$' -v ./_spikes/s-t/` | 16 calls vs Go's server limit 4, 2 s deadline, 20 runs: strict: 0/20 all ok, 20/20 with deadlines, 1/20 with a `PROTOCOL_ERROR`, connections 1-2; nonstrict: 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 3-4; strict+token: 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 1; strict+token-first: 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 1 | classification under synctest; `results/m-st2-f1-rate.txt` |
| W0.4-12 | 2026-09-25 16:26:49 JST | W0.4 S-T2 synctest refusals | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 11.11 → 11.11 | `ST2_REFUSALS=1 GOEXPERIMENT=nosimd,noruntimesecret sh $R '(M)' $MO none m-st2-refusal-pings-off -count=1 -timeout 20s -run '^TestST2Refusals$/^strict_stall_without_a_deadline/pings=false$' -v ./_spikes/s-t/`, the same with `^loopback_socket_inside_a_bubble$` into `m-st2-refusal-real-socket` and, locked, `pings=true` into `m-st2-refusal-pings-on` (W0.4-13's lock) | pings on: `panic: test timed out after 20s` (highest goroutine id 9724711); pings off: `panic: deadlock: all goroutines in bubble are blocked`, test FAIL; real socket in a bubble: status 200 | fail by design, env-gated; `results/m-st2-refusal-*.txt` |
| W0.4-13 | 2026-09-25 16:27:32 JST | W0.4 S-T2 refusal, pings on | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 10.45 → 13.21 | `ST2_REFUSALS=1 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-st2-refusal-pings-on -count=1 -timeout 20s -run '^TestST2Refusals$/^strict_stall_without_a_deadline/pings=true$' -v ./_spikes/s-t/` | see W0.4-12 (a 20 s CPU spin, so it takes the lock) | `results/m-st2-refusal-pings-on.txt` |
| W0.4-14 | 2026-09-25 16:27:53 JST | W0.4 S-T1 cold 64 fan-out | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 13.21 → 13.21 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-st1-fanout -count=5 -run '^TestST1FanOut$' -v ./_spikes/s-t/` | gate: 1 in 50/50 one-connection bursts, warm 0 in 50/50, ordering 10/10 in 5/5 runs, waiter wire p50 1.146 (0.996-1.513) / p99 1.898 (1.764-2.664) ms; no gate: 1 in 50/50, all-caller wire p50 1.215 (0.981-1.349) / p99 1.984 (1.823-2.861) ms; gate+token: 1 in 50/50, waiter wire p50 1.276 (1.056-1.478) / p99 2.344 (1.688-3.6) ms | [S-T1](#s-t1-cold-start-gate); `results/m-st1-fanout.txt` |
| W0.4-15 | 2026-09-25 16:27:54 JST | W0.4 S-T1b / F1 matrix | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 13.21 → 40.23, noisy | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-st1b -count=5 -timeout 900s -run '^TestST1bStreamLimit$' -v ./_spikes/s-t/` | 200 vs 8 strict: ok 8 (1-8), deadline 192 (192-199), accepts 1 (1-1); non-strict: ok 200 (200-200), accepts 22 (21-24), wall 50.5 ms; strict+token-first: ok 200 (200-200), accepts 1 (1-1), wall 291.8 ms | [F1 (M)](#f1-results-m); the live API's 1024 is cited, not measured; `results/m-st1b.txt` |
| W0.4-16 | 2026-09-25 16:33:53 JST | W0.4 S-T1b stall rate | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 23.68 → 22.91, noisy | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-st1b-stall -count=1 -timeout 900s -run '^TestST1bStallRate$' -v ./_spikes/s-t/` | reps (of 20) with a missed 500 ms deadline (refused streams in total): strict+token 5 (refused 109) and 7 (refused 294); strict+token-first 0 (refused 2) and 0 (refused 1); non-strict+limiter100 3 (refused 2702) and 15 (refused 1550) (64 vs 4, 200 vs 8) | `results/m-st1b-stall.txt` |
| W0.4-17 | 2026-09-25 16:38:21 JST | W0.4 spike package under -race | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 15.73 → 11.96 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-race -race -count=1 -timeout 900s ./_spikes/s-t/` | `ok` in 47.288s | not a measurement; locked so it cannot overlap one; `results/m-race.txt` |
| W0.3-01 | 2026-09-25 07:30:14 UTC | W0.3 S-E1 encode allocations, growth, AC-P1 sequence | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.08 → 0.28 | `go test -count=1 -run ^(TestAllocEncode\|TestGrowth\|TestSequence)$ -v ./_spikes/s-e1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-e1/results/alloc-L-base76ffd03.txt` | base 76ffd03; before the drop-path fix; AC-P1 before/after; flock /tmp/ts-spike/bench.lock |
| W0.3-02 | 2026-09-25 07:44:40 UTC | W0.3 S-E1 encode allocations, growth, AC-P1 sequence | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.54 → 0.64 | `go test -count=1 -run ^(TestAllocEncode\|TestGrowth\|TestSequence)$ -v ./_spikes/s-e1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-e1/results/alloc-L.txt` | base 2305d02; flock /tmp/ts-spike/bench.lock |
| W0.3-03 | 2026-09-25 16:28:52 JST | W0.3 S-E1 encode allocations, growth, AC-P1 sequence | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 40.23 → 36.54, noisy | `env GOEXPERIMENT=nosimd,noruntimesecret go test -count=1 -run ^(TestAllocEncode\|TestGrowth\|TestSequence)$ -v ./_spikes/s-e1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-e1/results/alloc-M-base76ffd03.txt` | base 76ffd03; before the drop-path fix; AC-P1 before/after; util-linux flock on scratchpad bench.lock |
| W0.3-04 | 2026-09-25 16:34:27 JST | W0.3 S-E1 encode allocations, growth, AC-P1 sequence | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 22.91 → 21.46, noisy | `env GOEXPERIMENT=nosimd,noruntimesecret go test -count=1 -run ^(TestAllocEncode\|TestGrowth\|TestSequence)$ -v ./_spikes/s-e1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-e1/results/alloc-M.txt` | base 2305d02; util-linux flock on scratchpad bench.lock |
| W0.3-05 | 2026-09-25 07:44:54 UTC | W0.3 S-E1 encode ns/op | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.64 → 3.35 | `go test -run ^$ -bench . -benchmem -count=5 ./_spikes/s-e1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-e1/results/bench-L.txt` | base 2305d02; flock /tmp/ts-spike/bench.lock |
| W0.3-06 | 2026-09-25 17:22:11 JST | W0.3 S-E1 encode ns/op | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.03 → 9.96 | `env GOEXPERIMENT=nosimd,noruntimesecret go test -run ^$ -bench . -benchmem -count=5 ./_spikes/s-e1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-e1/results/bench-M.txt` | base cc1524a: the spike and `internal/codec` are unchanged since 2305d02 except codec tests (P0-fix-2); re-measured after the raw file was lost to the global `bench*.txt` ignore; original run of record 16:49:47 JST at base 2305d02 (load 7.57 → 8.71; its ToolTags were printed outside the override); util-linux flock on scratchpad bench.lock, quiet rule (MAXLOAD 16, waited 0 × 60 s) |
| W0.3-07 | 2026-09-25 07:44:40 UTC | W0.3 S-E1 first call (JIT, Pretouch) | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.54 → 0.54 | `go test -count=1 -run ^TestFirstCall$ -v ./_spikes/s-e1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-e1/results/firstcall-L.txt` | base 2305d02; flock /tmp/ts-spike/bench.lock |
| W0.3-08 | 2026-09-25 16:34:26 JST | W0.3 S-E1 first call (JIT, Pretouch) | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 22.91 → 22.91, noisy | `env GOEXPERIMENT=nosimd,noruntimesecret go test -count=1 -run ^TestFirstCall$ -v ./_spikes/s-e1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-e1/results/firstcall-M.txt` | base 2305d02; util-linux flock on scratchpad bench.lock |
| W0.3-09 | 2026-09-25 07:30:29 UTC | W0.3 S-E1 AC-P1 sequence, lazy-buffer experiment | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.28 → 0.34 | `go test -count=1 -run ^TestSequence$ -v ./_spikes/s-e1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-e1/results/sequence-lazybuf-L-base76ffd03.txt` | base 76ffd03; with `lazybuf-experiment.diff` applied to internal/codec for the run only; flock /tmp/ts-spike/bench.lock |
| W0.3-10 | 2026-09-25 16:31:52 JST | W0.3 S-E1 AC-P1 sequence, lazy-buffer experiment | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 30.19 → 29.29, noisy | `env GOEXPERIMENT=nosimd,noruntimesecret go test -count=1 -run ^TestSequence$ -v ./_spikes/s-e1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-e1/results/sequence-lazybuf-M-base76ffd03.txt` | base 76ffd03; with `lazybuf-experiment.diff` applied to internal/codec for the run only; util-linux flock on scratchpad bench.lock |
| W0.3-11 | 2026-09-25 07:44:39 UTC | W0.3 S-D1 decode allocations | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.54 → 0.54 | `go test -count=1 -run ^TestAllocDecode$ -v ./_spikes/s-d1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-d1/results/alloc-L.txt` | base 2305d02; flock /tmp/ts-spike/bench.lock |
| W0.3-12 | 2026-09-25 16:34:40 JST | W0.3 S-D1 decode allocations | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 21.46 → 20.86, noisy | `env GOEXPERIMENT=nosimd,noruntimesecret go test -count=1 -run ^TestAllocDecode$ -v ./_spikes/s-d1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-d1/results/alloc-M.txt` | base 2305d02; util-linux flock on scratchpad bench.lock |
| W0.3-13 | 2026-09-25 07:30:33 UTC | W0.3 S-D1 decode ns/op, primitives, scans, string checks | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.34 → 1.35 | `go test -run ^$ -bench . -benchmem -count=5 ./_spikes/s-d1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-d1/results/bench-L.txt` | base 76ffd03; decoder, codec helpers and fixtures identical at 2305d02; flock /tmp/ts-spike/bench.lock |
| W0.3-14 | 2026-09-25 17:12:23 JST | W0.3 S-D1 decode ns/op, primitives, scans, string checks | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.19 → 8.03 | `env GOEXPERIMENT=nosimd,noruntimesecret go test -run ^$ -bench . -benchmem -count=5 ./_spikes/s-d1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-d1/results/bench-M.txt` | base cc1524a: the spike and `internal/codec` are unchanged since 2305d02 except codec tests (P0-fix-2); re-measured after the raw file was lost to the global `bench*.txt` ignore; original run of record 16:39:10 JST at base 2305d02 (load 11.96 → 9.76); against the first 267 of its 490 benchmark lines, which survived on (L) (not committed), the new medians are 2.7 % lower (geomean; 33 of 54 benchmarks differ at p < 0.05, none slower); util-linux flock on scratchpad bench.lock, quiet rule (MAXLOAD 16, waited 0 × 60 s) |
| W0.3-15 | 2026-09-25 07:44:38 UTC | W0.3 S-D1 validity gate and correctness | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.54 → 0.54 | `go test -count=1 -run ^(TestValidityGate\|TestDecodedValues\|TestLastWins\|TestControlRule\|TestFastCheckParity\|TestDuplicatesFixture\|TestModuleDuplicates\|TestStringLengthMix)$ -v ./_spikes/s-d1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-d1/results/gate-L.txt` | base 2305d02; flock /tmp/ts-spike/bench.lock; gate run against the 693-byte duplicates.json at 2305d02; superseded for duplicates by W0.3-21/-22 |
| W0.3-16 | 2026-09-25 16:34:39 JST | W0.3 S-D1 validity gate and correctness | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 21.46 → 21.46, noisy | `env GOEXPERIMENT=nosimd,noruntimesecret go test -count=1 -run ^(TestValidityGate\|TestDecodedValues\|TestLastWins\|TestControlRule\|TestFastCheckParity\|TestDuplicatesFixture\|TestModuleDuplicates\|TestStringLengthMix)$ -v ./_spikes/s-d1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-d1/results/gate-M.txt` | base 2305d02; util-linux flock on scratchpad bench.lock; gate run against the 693-byte duplicates.json at 2305d02; superseded for duplicates by W0.3-21/-22 |
| W0.3-17 | 2026-09-25 07:30:12 UTC | W0.3 S-D1 linearity (AC-P8) | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.08 → 0.08 | `go test -count=5 -run ^TestLinearity$ -v ./_spikes/s-d1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-d1/results/linearity-L.txt` | base 76ffd03; decoder, codec helpers and fixtures identical at 2305d02; flock /tmp/ts-spike/bench.lock |
| W0.3-18 | 2026-09-25 16:34:41 JST | W0.3 S-D1 linearity (AC-P8) | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 20.86 → 20.86, noisy | `env GOEXPERIMENT=nosimd,noruntimesecret go test -count=5 -run ^TestLinearity$ -v ./_spikes/s-d1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-d1/results/linearity-M.txt` | base 2305d02; util-linux flock on scratchpad bench.lock |
| W0.3-19 | 2026-09-25 07:44:39 UTC | W0.3 S-D1 R14 and primitive probes | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.54 → 0.54 | `go test -count=1 -run ^(TestProbeR14\|TestProbePrimitivesOnFixtures)$ -v ./_spikes/s-d1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-d1/results/probe-L.txt` | base 2305d02; flock /tmp/ts-spike/bench.lock |
| W0.3-20 | 2026-09-25 16:34:39 JST | W0.3 S-D1 R14 and primitive probes | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 21.46 → 21.46, noisy | `env GOEXPERIMENT=nosimd,noruntimesecret go test -count=1 -run ^(TestProbeR14\|TestProbePrimitivesOnFixtures)$ -v ./_spikes/s-d1/` | [W0.3 tables](#w03-tables); raw `_spikes/s-d1/results/probe-M.txt` | base 2305d02; util-linux flock on scratchpad bench.lock |
| W0.3-21 | 2026-09-25 08:15:20 UTC | W0.3 S-D1 decode allocations, extended `duplicates.json` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.01 → 0.01 | `go test -count=1 -run ^TestAllocDecode$ -v ./_spikes/s-d1/` | a1 and a2 `duplicates` 17/5336 (traverse 6/400, lazy pass 9/4272; 18 members, 1 structured legend); b 16/4328; every other fixture as W0.3-11; [W0.3 tables](#w03-tables); raw `_spikes/s-d1/results/alloc-L-base31f2986.txt` | base 31f2986; `duplicates.json` extended by 73af053 (825 B, was 693 B); flock /tmp/ts-spike/bench.lock, quiet rule (MAXLOAD 44, waited 0 × 60 s) |
| W0.3-22 | 2026-09-25 17:25:57 JST | W0.3 S-D1 decode allocations, extended `duplicates.json` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.29 → 8.29 | `env GOEXPERIMENT=nosimd,noruntimesecret go test -count=1 -run ^TestAllocDecode$ -v ./_spikes/s-d1/` | a1 and a2 `duplicates` 17/5336 (traverse 6/400, lazy pass 9/4272; 18 members, 1 structured legend); b 23/5232; every other fixture as W0.3-12; [W0.3 tables](#w03-tables); raw `_spikes/s-d1/results/alloc-M-base31f2986.txt` | base 31f2986; `duplicates.json` extended by 73af053 (825 B, was 693 B); util-linux flock on scratchpad bench.lock, quiet rule (MAXLOAD 16, waited 0 × 60 s) |
| W0.5-01 | 2026-09-25 17:07:35 JST | W0.5 S-C1 allocations and AC-P5 memstats probe | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.93 → 6.93 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M -count=1 -run '^(TestAllocCall\|TestMemStatsCap)$' -v ./_spikes/s-c1/` | q3: floor 8/640, call/sdk 23/2904, SDK-own 15/2264, call/naive 119/7960; q20: floor 8/640, call/sdk 43/10144, SDK-own 35/9504, call/naive 522/30872; AC-P5 (256 KiB): (i) 263448 B, (ii) 1328 B, (iii) 33293616 B, (iv) 33294392 B; [W0.5 tables](#w05-tables) | mallocs/bytes, collector off, `GOMAXPROCS(1)`, 3 of 5 runs agree; `results/alloc-M.txt` |
| W0.5-02 | 2026-09-25 17:07:36 JST | W0.5 S-C1 correctness | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.93 → 7.65 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock test-M -count=1 -v -run '^(TestSystemOneDecodes\|TestRequestShape\|TestGetBodyReplay\|TestReadBody\|TestSystemOneCap)$' ./_spikes/s-c1/` | `ok`, 26 tests and subtests PASS | not a measurement; `results/test-M.txt` |
| W0.5-03 | 2026-09-25 17:07:36 JST | W0.5 S-C1 under -race | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 7.65 → 7.65 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock race-M -race -count=1 ./_spikes/s-c1/` | `ok` in 1.156s | not a measurement (alloc tests are `!race`); `results/race-M.txt` |
| W0.5-04 | 2026-09-25 17:07:42 JST | W0.5 S-C1 ns/op | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 7.28 → 7.71 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $O $SP/bench.lock bench-M -run '^$' -bench . -benchmem -count=5 ./_spikes/s-c1/` | q3: floor 296.4 ns, E_sonic 128.0 ns, call/sdk 4.647 µs, call/naive 8.968 µs; q20: floor 323.5 ns, call/sdk 23.28 µs, call/naive 39.78 µs (medians of 5); [W0.5 tables](#w05-tables) | load gate 16, waited 0; `results/bench-M.txt`, `results/benchstat-M.txt` |
| W0.5-05 | 2026-09-25 08:07:50 UTC | W0.5 S-C1 allocations and AC-P5 memstats probe | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.00 → 0.00 | `sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L -count=1 -run '^(TestAllocCall\|TestMemStatsCap)$' -v ./_spikes/s-c1/` | identical to W0.5-01 in every count | `results/alloc-L.txt` |
| W0.5-06 | 2026-09-25 08:07:52 UTC | W0.5 S-C1 correctness | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.00 → 1.28 | `sh $R '(L)' $O /tmp/ts-spike/bench.lock test-L -count=1 -v -run '^(TestSystemOneDecodes\|TestRequestShape\|TestGetBodyReplay\|TestReadBody\|TestSystemOneCap)$' ./_spikes/s-c1/` | `ok`, 26 tests and subtests PASS | not a measurement; `results/test-L.txt` |
| W0.5-07 | 2026-09-25 08:07:53 UTC | W0.5 S-C1 under -race | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.28 → 1.26 | `sh $R '(L)' $O /tmp/ts-spike/bench.lock race-L -race -count=1 ./_spikes/s-c1/` | `ok` in 1.433s | not a measurement; `results/race-L.txt` |
| W0.5-08 | 2026-09-25 08:08:07 UTC | W0.5 S-C1 ns/op | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.16 → 0.98 | `MAXLOAD=44 sh $R '(L)' $O /tmp/ts-spike/bench.lock bench-L -run '^$' -bench . -benchmem -count=5 ./_spikes/s-c1/` | q3: floor 564.8 ns, E_sonic 93.51 ns, call/sdk 5.837 µs, call/naive 16.76 µs; q20: floor 601.7 ns, call/sdk 24.25 µs, call/naive 77.34 µs (medians of 5); [W0.5 tables](#w05-tables) | `results/bench-L.txt`, `results/benchstat-L.txt` |
| W1.3-01 | 2026-09-25 21:48:48 JST | W1.3 `Prepare()` allocations | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.06 → 7.90 | `BASE=$BASE GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M-base95f3e4c -count=1 -run '^TestAllocPrepare$' -v .` | mallocs/B (5/5 runs agree): sketch 9/1 016, one noul 3/224, 20×10 choices 27/15 256, 20×8 text scores 27/17 176, 20×8 JSON scores 36/44 408, 100 raw 1 710/78 080, escapes 12/8 536; NIT 8 pairs 252 vs 132 and 1 215 vs 615; [W1.3 tables](#w13-tables) | base 95f3e4c (production code as b227e5b); `results/alloc-M-base95f3e4c.txt` |
| W1.3-02 | 2026-09-25 21:48:55 JST | W1.3 `Prepare()` ns/op | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.39 → 5.29 | `BASE=$BASE GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $O $SP/bench.lock bench-M-base95f3e4c -run '^$' -bench '^Benchmark(Prepare\|FalsyJSON)$' -benchmem -count=5 .` | sketch 605.2 ns, one noul 99.30 ns, 20×10 choices 11.79 µs, 20×8 text scores 9.417 µs, 20×8 JSON scores 25.32 µs, 100 raw 73.01 µs, escapes 3.282 µs (medians of 5); `falsyJSON` on the array 1.194 µs; [W1.3 tables](#w13-tables) | base 95f3e4c; load gate 16, waited 0; `results/bench-M-base95f3e4c.txt`, `results/benchstat-M-base95f3e4c.txt` |
| W1.3-03 | 2026-09-25 21:51:01 JST | W1.3 `Prepare()` allocation call sites | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 4.38 → 4.38 | `GOEXPERIMENT=nosimd,noruntimesecret sh _spikes/w1.3/breakdown.sh "$PWD" $SP/bench.lock $O/breakdown-M-base95f3e4c.txt <tmpdir>` | memprofile traces at rate 1 per case; [W1.3 findings](#w13-findings) item 3 | base 95f3e4c; the script (committed with the results) takes the lock itself; `results/breakdown-M-base95f3e4c.txt` |
| W1.3-04 | 2026-09-25 12:49:07 UTC | W1.3 `Prepare()` allocations | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.04 → 0.04 | `BASE=$BASE sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L-base95f3e4c -count=1 -run '^TestAllocPrepare$' -v .` | identical to W1.3-01 in every malloc and byte count and every prepared length | base 95f3e4c; `results/alloc-L-base95f3e4c.txt` |
| W1.3-05 | 2026-09-25 12:49:08 UTC | W1.3 `Prepare()` ns/op | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.04 → 0.63 | `BASE=$BASE MAXLOAD=44 sh $R '(L)' $O /tmp/ts-spike/bench.lock bench-L-base95f3e4c -run '^$' -bench '^Benchmark(Prepare\|FalsyJSON)$' -benchmem -count=5 .` | sketch 1.155 µs, one noul 180.2 ns, 20×10 choices 20.28 µs, 20×8 text scores 16.09 µs, 20×8 JSON scores 44.48 µs, 100 raw 133.7 µs, escapes 5.384 µs (medians of 5); `falsyJSON` on the array 2.089 µs; [W1.3 tables](#w13-tables) | base 95f3e4c; `results/bench-L-base95f3e4c.txt`, `results/benchstat-L-base95f3e4c.txt` |

## W0.4 transport spikes (S-T1 to S-T5b, F1)

Code: `_spikes/s-t/` (a throwaway package outside `./...`): `gate.go` (the §6.3
cold-start gate: cold → dialing → warm, release at the leader's
`httptrace.GotConn`, waiter bound, fresh `*DialError` per waiter around the
leader's cause, hand-over or cold reset when the leader's context ends),
`transport.go` (the §6.3 default transport: `Protocols{HTTP2}`,
`MaxConnsPerHost: 1`, `HTTP2Config{StrictMaxConcurrentRequests: true,
SendPingTimeout: 30s, PingTimeout: 15s}`, `IdleConnTimeout: 90s`,
`TLSHandshakeTimeout: connectTimeout`, `TLSClientConfig{MinVersion: TLS 1.2,
RootCAs, VerifyConnection: ALPN check scoped by the build-time proxy
decision}`, `DialContext: net.Dialer{Timeout: connectTimeout}`), `mitigate.go`
(F1 options), the `st*_test.go` spikes, `run.sh` (the runner of every row) and
`median.py` (per-case medians of a result file). Servers:
`internal/testsupport` at `2305d02`. The "dial parked on a channel" cases use
the spike's own `Options.DialHold` (a channel gate inside the transport's
`DialContext`), not `testsupport.GatedDialer`, which landed later with the
same semantics.

Row commands use `R=_spikes/s-t/run.sh`, `MO=_spikes/s-t/results`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/c8084031-5323-4873-8c36-a19f65c9e6ff/scratchpad`;
on (L) the tree is copied with the §11 tar pipe to
`/tmp/ts-spike/src-w0.4/wt-w0.4`, `LO=/tmp/ts-spike/src-w0.4/results`, and the
environment is `PATH=/tmp/ts-spike/go/bin:$PATH GOPATH=/tmp/ts-spike/gopath
GOMODCACHE=/tmp/ts-spike/modcache GOCACHE=/tmp/ts-spike/gocache` with no
`GOEXPERIMENT`. Latency and wall-time runs hold the shared lock (`flock(1)`:
`/usr/bin/flock` on (L), util-linux's `/opt/homebrew/opt/util-linux/bin/flock`
on (M), where macOS has none on `PATH`); on (M) they also wait inside the
lock for a 1-minute load ≤ 16 (`MAXLOAD=16`: release, 60 s, up to 5 tries;
the header records the waits). Connection-count and classification runs take
no lock. Every row here was produced by the committed tree at the commit that
adds these results; the earlier (pre-rebase) runs are superseded.

### S-T1: cold-start gate

Cold 64-way fan-out against the loopback server (ALPN h2, no stream limit),
10 bursts per run with a fresh server, transport and gate each, then a warm
64-way burst on the same client. The handler holds every response until 64
requests of the burst have arrived (5 s guard, AC-P4), so "ordering" means
all 64 requests were on the wire before any response and all arrived on
connection 0. Wire latency = `httptrace.WroteHeaders` − call start; span =
last `WroteHeaders` − first start in a burst; p50/p99 are over every caller
of the 10 bursts of one run, then the median of the 5 runs (range in
parentheses).

| Host | Variant | Cold: one connection | Warm: no new | Ordering | Wire p50 ms | Wire p99 ms | Span p50 ms | Warm wire p50 / p99 ms |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| (L) | gate (waiters) | 1 in 50/50 | 0 in 50/50 | 10/10 in 5/5 | 1.271 (1.246-1.661) | 2.357 (2.044-2.719) | 1.573 | 0.337 / 1.369 |
| (L) | no gate (all callers) | 1 in 50/50 | 0 in 50/50 | 10/10 in 5/5 | 1.232 (1.174-1.645) | 2.211 (2.024-2.412) | 1.486 | 0.38 / 1.398 |
| (L) | gate + write token (waiters) | 1 in 50/50 | 0 in 50/50 | 10/10 in 5/5 | 1.49 (1.446-1.651) | 2.507 (2.417-3.119) | 1.916 | 0.554 / 1.825 |
| (M) | gate (waiters) | 1 in 50/50 | 0 in 50/50 | 10/10 in 5/5 | 1.146 (0.996-1.513) | 1.898 (1.764-2.664) | 1.38 | 0.407 / 1.236 |
| (M) | no gate (all callers) | 1 in 50/50 | 0 in 50/50 | 10/10 in 5/5 | 1.215 (0.981-1.349) | 1.984 (1.823-2.861) | 1.537 | 0.38 / 1.167 |
| (M) | gate + write token (waiters) | 1 in 50/50 | 0 in 50/50 | 10/10 in 5/5 | 1.276 (1.056-1.478) | 2.344 (1.688-3.6) | 1.924 | 0.526 / 1.477 |

Leader `GotConn` (dial and TLS on loopback), median of 5 runs: (L)
0.872 ms, (M) 0.651 ms. Under `HTTP2Only` the stock
queue alone also yields one connection (plan option C): every cold caller
registers in `idleConnWait` (`queueForIdleConn`, `transport.go:1241`),
`queueForDial` grants one permit (`:1661-1688`), and `tryPutIdleConn` hands
the h2 connection to every idle waiter (`:1181-1188`). The gate earns its
place on the failure paths and under `HTTPAuto`, below.

Failure cases (identical in every run on both hosts; dial 0 is parked on a
channel, so no case depends on a timer margin):

| Case | Leader | Waiters | Gate after | Connections |
| --- | --- | --- | --- | --- |
| Leader's context cancelled mid-dial, 63 waiters parked; the dial is released only after the hand-over | `context canceled` (`errors.Is(err, context.Canceled)`) | 63/63 200; one waiter took over (`Leaders` 2, `Handovers` 1) and released the other 62 at its `GotConn` | warm | dials 1, accepts 1: the stock dial is detached from the request context (`transport.go:1596`) and completes for the new leader through `tryPutIdleConn` |
| Leader cancelled mid-dial, no waiter | `context canceled` | none | cold at once (`ColdResets` 1); the next caller leads (`Leaders` 2) and gets 200 | dials 1, accepts 1 |
| TLS-silent listener, `connectTimeout` 500 ms, waiter bound 1 s | `*DialError{Timeout: true}` → `http.tlsHandshakeTimeoutError` "net/http: TLS handshake timeout" (`transport.go:1794-1798`, `:3385-3389`) after 500.631 ms (L) / 501.331 ms (M), medians | 63 fresh `*DialError` values (64 distinct pointers in total), each with `Err ==` the leader's cause (`errors.Is` holds) and the leader's flags (`Timeout: true`) | cold | silent listener accepts 1 |
| TLS-silent listener, no gate, 8 callers with a 3 s deadline | n/a | classes map[deadline:3 other:5] (L); first run's completion times 501, 1002, 1503, 2004, 2505, 3001, 3001, 3001 ms | n/a | 6 serial dials: a failed dial's permit passes to the next queued caller (`transport.go:1748-1766`) |
| Waiter bound 200 ms, dial released only after 63 fall-throughs | 200 | 63 fall-throughs, 64/64 200 | warm | dials 1, accepts 1 (the stock queue still yields one connection) |

`HTTPAuto` (`Protocols{HTTP1, HTTP2}`, `MaxConnsPerHost` 0), cold 64-way
bursts, h2 server (K20): with the gate, (L) accepts 1 in 50/50, (M)
accepts 1 in 50/50; without it, (L) accepts min 24 / median 63 / max 64 over 50 bursts, of which 1 connection carries every request; (M) accepts min 51 / median 64 / max 64 over 50 bursts, of which 1 connection carries every request
(`AddConn` closes the rest, `internal/http2/transport.go:113-120`). The K20
window (waiters released between `GotConn` and `putOrCloseIdleConn`)
produced no redundant dial: released waiters first try the alt-protocol
round tripper, which consults the h2 pool, and `dialConn` adds the
connection there (`transport.go:2071`) before it is delivered.

### S-T1b and F1: strict stream limit

F1 is reproduced on the loopback server with the gate on: a cold burst of
`calls` against `MAX_CONCURRENT_STREAMS` = `limit` (0 = the server advertises
none, so the client uses 1000 after SETTINGS), handler service 10 ms unless
the case says 50 ms, per-call deadline 2 s, 5 runs; each cell is the median
(min-max). `refused` counts streams the server reset with `REFUSED_STREAM`
for exceeding its limit; `max streams` is the server's high-water mark on one
connection. `limiterN` is mitigation (ii)/(iii); `token` and `token-first`
are (iv); all run inside the gate.

#### F1 results (L)

| Case | Runs | ok | deadline | other | accepts | max streams | refused | wall ms | ok p50 ms | ok p99 ms |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 200vs8/strict | 5 | 1 (1-3) | 199 (197-199) | 0 (0-0) | 1 (1-1) | 1 (1-3) | 0 (0-0) | 2001.6 | 11.6 | 11.7 |
| 200vs8/nonstrict | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 21 (18-22) | 8 (8-8) | 16 (0-48) | 1027.2 | 25.3 | 34.5 |
| 64vs4/strict | 5 | 4 (1-4) | 60 (60-63) | 0 (0-0) | 1 (1-1) | 4 (1-4) | 8 (0-17) | 2000.9 | 11.4 | 11.5 |
| 64vs4/nonstrict | 5 | 64 (64-64) | 0 (0-0) | 0 (0-0) | 15 (14-16) | 4 (4-4) | 12 (5-26) | 1011.8 | 18.9 | 1011.6 |
| 8vs8/strict | 5 | 8 (8-8) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 11.5 | 11.4 | 11.4 |
| 8vs8/nonstrict | 5 | 8 (8-8) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 11.2 | 11.2 | 11.2 |
| 9vs8/strict | 5 | 9 (9-9) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 7 (6-8) | 0 (0-0) | 21.5 | 21.5 | 21.5 |
| 9vs8/nonstrict | 5 | 9 (9-9) | 0 (0-0) | 0 (0-0) | 1 (1-2) | 8 (8-8) | 0 (0-1) | 1011.6 | 11.2 | 1011.6 |
| 4vs4/strict | 5 | 4 (4-4) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 11.3 | 11.2 | 11.2 |
| 4vs4/nonstrict | 5 | 4 (4-4) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 11.2 | 11.2 | 11.2 |
| 5vs4/strict | 5 | 5 (5-5) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (3-4) | 0 (0-1) | 21.4 | 11.2 | 21.3 |
| 5vs4/nonstrict | 5 | 5 (5-5) | 0 (0-0) | 0 (0-0) | 2 (1-2) | 4 (4-4) | 0 (0-1) | 12.0 | 12.0 | 12.0 |
| 200vs8/strict/limiter8 | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 260.4 | 135.7 | 260.2 |
| 200vs8/strict/limiter100 | 5 | 2 (1-6) | 198 (194-199) | 0 (0-0) | 1 (1-1) | 2 (1-6) | 0 (0-0) | 2001.9 | 11.4 | 11.5 |
| 200vs8/strict/limiter4 | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 515.5 | 257.5 | 514.4 |
| 200vs8/strict/token | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 260.6 | 135.7 | 260.3 |
| 200vs8/strict/token-first | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 271.5 | 146.4 | 271.2 |
| 64vs4/strict/token | 5 | 64 (64-64) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 165.4 | 83.2 | 165.3 |
| 64vs4/strict/token-first | 5 | 64 (64-64) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 175.5 | 93.3 | 175.4 |
| 8vs8/strict/token | 5 | 8 (8-8) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 11.4 | 11.3 | 11.3 |
| 8vs8/strict/token-first | 5 | 8 (8-8) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 7 (7-7) | 0 (0-0) | 21.5 | 21.4 | 21.4 |
| 9vs8/strict/token | 5 | 9 (9-9) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 21.5 | 11.2 | 21.5 |
| 9vs8/strict/token-first | 5 | 9 (9-9) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 21.5 | 21.4 | 21.5 |
| 4vs4/strict/token | 5 | 4 (4-4) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 11.2 | 11.2 | 11.2 |
| 4vs4/strict/token-first | 5 | 4 (4-4) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 3 (3-3) | 0 (0-0) | 21.3 | 21.3 | 21.3 |
| 5vs4/strict/token | 5 | 5 (5-5) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 21.4 | 11.1 | 21.3 |
| 5vs4/strict/token-first | 5 | 5 (5-5) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 21.4 | 21.3 | 21.4 |
| 200vs1024/strict | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 200 (200-200) | 0 (0-0) | 15.1 | 13.1 | 14.7 |
| 200vs1024/nonstrict | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 200 (200-200) | 0 (0-0) | 13.8 | 12.0 | 13.6 |
| 200vs0/strict | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 200 (200-200) | 0 (0-0) | 15.1 | 13.3 | 14.7 |
| 200vs0/nonstrict | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-2) | 200 (200-200) | 0 (0-0) | 13.9 | 11.9 | 13.7 |
| 200vs8/nonstrict/limiter100 | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 13 (13-13) | 8 (8-8) | 13 (7-26) | 1020.8 | 22.9 | 33.4 |
| 64vs4/nonstrict/limiter100 | 5 | 64 (64-64) | 0 (0-0) | 0 (0-0) | 14 (14-16) | 4 (4-4) | 22 (21-26) | 1021.9 | 18.8 | 1021.8 |
| 200vs1024/nonstrict/limiter100 | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 100 (100-100) | 0 (0-0) | 24.9 | 14.0 | 24.7 |
| 200vs0/nonstrict/limiter100 | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 100 (100-100) | 0 (0-0) | 25.2 | 13.7 | 24.9 |
| 200vs8/nonstrict/limiter8 | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 260.8 | 135.8 | 259.7 |
| 64vs0/strict/service50ms | 5 | 64 (64-64) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 64 (64-64) | 0 (0-0) | 52.2 | 52.0 | 52.2 |
| 64vs0/strict/token-first/service50ms | 5 | 64 (64-64) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 63 (63-63) | 0 (0-0) | 103.4 | 102.7 | 103.3 |
| 64vs0/nonstrict/limiter100/service50ms | 5 | 64 (64-64) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 64 (64-64) | 0 (0-0) | 52.2 | 51.9 | 52.1 |

#### F1 results (M)

| Case | Runs | ok | deadline | other | accepts | max streams | refused | wall ms | ok p50 ms | ok p99 ms |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 200vs8/strict | 5 | 8 (1-8) | 192 (192-199) | 0 (0-0) | 1 (1-1) | 8 (1-8) | 21 (0-91) | 2002.5 | 12.5 | 12.9 |
| 200vs8/nonstrict | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 22 (21-24) | 8 (8-8) | 105 (64-295) | 50.5 | 24.6 | 34.1 |
| 64vs4/strict | 5 | 4 (2-4) | 60 (60-62) | 0 (0-0) | 1 (1-1) | 4 (2-4) | 7 (0-29) | 2001.5 | 12.2 | 12.5 |
| 64vs4/nonstrict | 5 | 64 (64-64) | 0 (0-0) | 0 (0-0) | 14 (12-15) | 4 (4-4) | 100 (49-184) | 27.2 | 21.3 | 27.1 |
| 8vs8/strict | 5 | 8 (8-8) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 12.8 | 12.6 | 12.7 |
| 8vs8/nonstrict | 5 | 8 (8-8) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 12.4 | 12.3 | 12.3 |
| 9vs8/strict | 5 | 9 (9-9) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 7 (5-8) | 0 (0-1) | 23.6 | 12.6 | 23.5 |
| 9vs8/nonstrict | 5 | 9 (9-9) | 0 (0-0) | 0 (0-0) | 2 (1-2) | 8 (8-8) | 1 (0-1) | 12.5 | 12.1 | 12.5 |
| 4vs4/strict | 5 | 4 (4-4) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 12.1 | 12.1 | 12.1 |
| 4vs4/nonstrict | 5 | 4 (4-4) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 12.1 | 12.1 | 12.1 |
| 5vs4/strict | 5 | 5 (5-5) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (3-4) | 1 (0-1) | 23.2 | 12.2 | 23.1 |
| 5vs4/nonstrict | 5 | 5 (5-5) | 0 (0-0) | 0 (0-0) | 2 (2-2) | 4 (4-4) | 1 (0-1) | 12.3 | 12.2 | 12.2 |
| 200vs8/strict/limiter8 | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-1) | 287.0 | 147.7 | 286.4 |
| 200vs8/strict/limiter100 | 5 | 4 (2-8) | 196 (192-198) | 0 (0-0) | 1 (1-1) | 4 (2-8) | 0 (0-8) | 2002.4 | 12.3 | 12.7 |
| 200vs8/strict/limiter4 | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-5) | 0 (0-0) | 565.5 | 282.5 | 565.2 |
| 200vs8/strict/token | 5 | 200 (8-200) | 0 (0-192) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-92) | 281.0 | 144.2 | 278.2 |
| 200vs8/strict/token-first | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 291.8 | 157.2 | 291.5 |
| 64vs4/strict/token | 5 | 64 (4-64) | 0 (0-60) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-8) | 181.0 | 89.1 | 177.9 |
| 64vs4/strict/token-first | 5 | 64 (64-64) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 192.1 | 101.9 | 192.0 |
| 8vs8/strict/token | 5 | 8 (8-8) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 12.3 | 12.3 | 12.3 |
| 8vs8/strict/token-first | 5 | 8 (8-8) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 7 (7-7) | 0 (0-0) | 23.3 | 23.3 | 23.3 |
| 9vs8/strict/token | 5 | 9 (9-9) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-1) | 23.4 | 12.2 | 23.4 |
| 9vs8/strict/token-first | 5 | 9 (9-9) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 23.7 | 23.7 | 23.7 |
| 4vs4/strict/token | 5 | 4 (4-4) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 12.4 | 12.3 | 12.3 |
| 4vs4/strict/token-first | 5 | 4 (4-4) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 3 (3-3) | 0 (0-0) | 23.4 | 23.4 | 23.4 |
| 5vs4/strict/token | 5 | 5 (5-5) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 23.4 | 12.1 | 23.3 |
| 5vs4/strict/token-first | 5 | 5 (5-5) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 4 (4-4) | 0 (0-0) | 23.3 | 23.2 | 23.2 |
| 200vs1024/strict | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 200 (183-200) | 0 (0-0) | 15.2 | 13.0 | 14.7 |
| 200vs1024/nonstrict | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 2 (1-2) | 200 (200-200) | 0 (0-0) | 14.9 | 13.4 | 14.6 |
| 200vs0/strict | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 200 (200-200) | 0 (0-0) | 14.2 | 12.8 | 13.9 |
| 200vs0/nonstrict | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-2) | 200 (200-200) | 0 (0-0) | 14.6 | 13.4 | 14.5 |
| 200vs8/nonstrict/limiter100 | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 13 (13-13) | 8 (8-8) | 118 (31-336) | 48.9 | 23.2 | 33.3 |
| 64vs4/nonstrict/limiter100 | 5 | 64 (64-64) | 0 (0-0) | 0 (0-0) | 14 (13-16) | 4 (4-4) | 65 (39-237) | 26.8 | 18.1 | 26.7 |
| 200vs1024/nonstrict/limiter100 | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 100 (100-100) | 0 (0-0) | 24.9 | 14.5 | 24.4 |
| 200vs0/nonstrict/limiter100 | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 100 (100-100) | 0 (0-0) | 24.3 | 13.7 | 24.0 |
| 200vs8/nonstrict/limiter8 | 5 | 200 (200-200) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 8 (8-8) | 0 (0-0) | 279.8 | 145.3 | 279.5 |
| 64vs0/strict/service50ms | 5 | 64 (64-64) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 64 (64-64) | 0 (0-0) | 52.8 | 52.4 | 52.7 |
| 64vs0/strict/token-first/service50ms | 5 | 64 (64-64) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 63 (63-63) | 0 (0-0) | 104.4 | 103.9 | 104.2 |
| 64vs0/nonstrict/limiter100/service50ms | 5 | 64 (64-64) | 0 (0-0) | 0 (0-0) | 1 (1-1) | 64 (64-64) | 0 (0-0) | 52.8 | 52.4 | 52.7 |

#### Mechanism (GOROOT `/Users/zchee/sdk/go1.27.1/src`)

Strict mode, one connection:

1. An `https` request first goes to the alt-protocol round tripper
   `http2RoundTripper{t2, mapCachedConnErr: true}` registered for `https`
   (`net/http/http2.go:294`, `:437-445`; `transport.go:643`), i.e.
   `RoundTripOpt` (`internal/http2/transport.go:408-465`) over the no-dial pool
   (`internal/http2/client_conn_pool.go:258`, `:51-69`).
2. The pool calls `cc.ReserveNewRequest()` (`internal/http2/transport.go:744-752`),
   which under `StrictMaxConcurrentRequests` always succeeds
   (`idleStateLocked`: `maxConcurrentOkay = true`, `:831`) and increments
   `streamsReserved` (`:750`). Every concurrent caller holds a reservation.
3. `writeRequest` takes the one-slot `reqHeaderMu` (`:1259`), returns its own
   reservation (`:1270`) and calls `awaitOpenSlotForStreamLocked` (`:1271`,
   `:1539-1561`), which waits on `cc.cond.Wait()` (`:1555`), with
   `reqHeaderMu` still held, until `currentRequestCountLocked()` = streams +
   `streamsReserved` + `pendingResets` (`:885-887`) is below
   `maxConcurrentStreams` (`:1551`).
4. The other callers queue on `reqHeaderMu` without giving their reservations
   back, so once `limit` of them are queued the holder never sees a free
   slot, even with no stream open: a stall until contexts end (200 vs 8
   strict: ok 1 (1-3) of 200 on (L), 8 (1-8) on
   (M); 64 vs 4: ok 4 (1-4) (L), 4 (2-4) (M); the
   rest hit the 2 s deadline).
5. Only deadlines unwind it: a caller that gives up while queued returns its
   reservation in `cleanupWriteRequest` (`:1436-1438`); a holder woken by
   `abortStream`'s broadcast (`:308`, `:318`) returns its reservation there a
   second time (it already did at `:1270`), which releases a foreign
   reservation and undercounts `streamsReserved` afterwards.
6. `processSettings` sets `maxConcurrentStreams` (`:2706`) without a
   broadcast of its own (the one there is for `INITIAL_WINDOW_SIZE`,
   `:2720`).

Upstream: [golang/go#70809](https://github.com/golang/go/issues/70809)
"x/net/http2: when StrictMaxConcurrentStreams enabled
ClientConn.ReserveNewRequest() causes stalls in request processing": opened
2024-12-12, **open**, label NeedsInvestigation, milestone "Unreleased", no CL
linked (read 2026-09-25 16:01 JST). It shows the same mechanism with a custom
x/net pool and two requests against a server limit of 1, bisects it to x/net
CL 617655, and a comment (2025-01-07) proposes counting only streams +
`pendingResets` in `awaitOpenSlotForStreamLocked`. It does not mention the
stock `net/http` transport with `HTTP2Config.StrictMaxConcurrentRequests`,
which this spike shows is affected in go1.27.1. Searches:
`gh issue list -R golang/go --state all --search` with
"StrictMaxConcurrentStreams" (#70809 plus unrelated issues),
"StrictMaxConcurrentRequests" (#67813, the HTTP/2 configuration API, and
#76680, an API audit), "awaitOpenSlotForStreamLocked" (#70809 only),
"streamsReserved" (#70809, #63196, #61474, #59690), and
`gh search issues --repo golang/go "http2 reserved streams deadlock"` (no
result). golang/go#80680 (Go 1.27rc2, closed 2026-08-04) is a different
deadlock on the same `Reserve` path (state-hook re-entrancy).

Non-strict mode never stalls, and `MaxConnsPerHost` does not bound it:
`idleStateLocked` grants a reservation only below the limit (`:839`), and
`awaitOpenSlotForStreamLocked` returns `errClientConnUnusable` (`:1547-1548`,
retried by `canRetryError`, `:537-539`) instead of waiting. With no
connection able to reserve, the pool returns `ErrNoCachedConn`
(`client_conn_pool.go:66-68`); the alt path turns it into
`ErrSkipAltProtocol` (`http2.go:440-441`); `getConn` hands out the idle
pconn, whose round tripper (`mapCachedConnErr: false`, `http2.go:362`) fails
with `ErrNoCachedConn` again, and `roundTrip` runs `removeIdleConn(pconn)`
and `decConnsPerHost` (`transport.go:741-744`): the full connection leaves
the idle list and the per-host count while it keeps serving its streams, so
`queueForDial` (`:1661-1688`) finds a free permit and dials. `MaxConnsPerHost:
1` bounds dials in progress, not connections: 200 vs 8 non-strict opened
21 (18-22) connections (L) and
22 (21-24) (M).

Pre-SETTINGS window: a new connection assumes 100 streams
(`initialMaxConcurrentStreams`, `:57`, `:624`) until it reads the server's
SETTINGS; requests sent in that window above a lower server limit are
refused, and `RoundTripOpt` retries a `REFUSED_STREAM` once at once and then
after 1 s, 2 s, 4 s (`:417-446`, backoff `:434`): a request refused twice
costs a second, hence non-strict wall times near 1 s when that happens (200
vs 8: 1027.2 ms on (L), 50.5 ms on
(M)). Go's own server refuses with `REFUSED_STREAM` only while
its SETTINGS are unacknowledged and answers `PROTOCOL_ERROR` afterwards
(`internal/http2/server.go:1905-1916`), which reaches the caller as `stream
error: stream ID N; PROTOCOL_ERROR; received from peer` and is not retried
(fake-network runs, where the server is Go's: W0.4-04, W0.4-11). The
testsupport loopback server always answers `REFUSED_STREAM`.

The SDK cannot learn `MAX_CONCURRENT_STREAMS` from `net/http`: pooled h2
connections expose no state (`ClientConn.State()`,
`internal/http2/transport.go:788`, is internal); `http.ClientConn.Available`
and `InFlight` (`net/http/clientconn.go:277`, `:284`) exist only for
connections made with `Transport.NewClientConn` (`:113`) outside the pool;
`httptrace` has no SETTINGS hook.

The live API's limit of 1024 was **not measured in W0.4**: the wave allowed
loopback and in-memory networks only. The value is plan §3.1's, which cites
the Rust port's ledger (`rs:docs/perf/ledger.md`, section "S4 - the server's
HTTP/2 SETTINGS", present at the pinned commit `34c3b7c`, 2026-09-24; the
section names the command `$TARGET/release/transport-probe s4` and no date or
host): `SETTINGS_MAX_CONCURRENT_STREAMS` 1,024 and
`SETTINGS_INITIAL_WINDOW_SIZE` 16 MiB, read twice from `api.typesafe.ai:443`
with no credential sent, once by writing the HTTP/2 preface by hand and
decoding the server's first SETTINGS frame off the TLS stream and once through
the `h2` crate, with ALPN `h2` on both connections. Against 1024, and against
a server that advertises nothing, 200 cold calls complete on one connection
in strict mode here (200vs1024/strict, 200vs0/strict), so F1 would reach the
live API only above 1024 concurrent calls per connection.

#### Mitigations compared

Each cell: (L) / (M). Stall rate: `TestST1bStallRate`, 20 repetitions,
500 ms deadline, reps with a missed deadline (refused streams in total);
fake time: `TestST2F1`, 16 calls vs Go's server with limit 4, 20 runs.

| Option | 200 vs 8: ok, connections, wall ms | 64 vs 4: ok, connections, wall ms | Stall rate 64 vs 4 / 200 vs 8 | Fake time | Needs the server's limit | Cost |
| --- | --- | --- | --- | --- | --- | --- |
| strict, as planned (baseline) | 1 (1-3), 1 (1-1), 2001.6 / 8 (1-8), 1 (1-1), 2002.5 | 4 (1-4), 1 (1-1), 2000.9 / 4 (2-4), 1 (1-1), 2001.5 | not run | (L) 0/20 all ok, 20/20 with deadlines, 3/20 with a `PROTOCOL_ERROR`, connections 1-2; (M) 0/20 all ok, 20/20 with deadlines, 1/20 with a `PROTOCOL_ERROR`, connections 1-2 | n/a | F1 |
| (i) non-strict + gate | 200 (200-200), 21 (18-22), 1027.2 / 200 (200-200), 22 (21-24), 50.5 | 64 (64-64), 15 (14-16), 1011.8 / 64 (64-64), 14 (12-15), 27.2 | not run bare | (L) 17/20 all ok, 0/20 with deadlines, 3/20 with a `PROTOCOL_ERROR`, connections 2-4; (M) 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 3-4 | no | extra connections that `MaxConnsPerHost` does not bound; stdlib's 1 s retry backoff on refused streams; `PROTOCOL_ERROR` from Go servers |
| (ii) strict + limiter = server limit (8) | 200 (200-200), 1 (1-1), 260.4 / 200 (200-200), 1 (1-1), 287.0 | not run | not run | not run | yes, and `net/http` cannot tell | a limit the caller must configure |
| (iii) strict + limiter above the server's (100 vs 8), deadline as the only exit | 2 (1-6), 1 (1-1), 2001.9 / 4 (2-8), 1 (1-1), 2002.4 | not run | not run | not run | yes | F1 unchanged; a retry policy re-enters the stall |
| (iv-a) strict + write token (one caller between `ReserveNewRequest` and its HEADERS; released at `WroteHeaders`, `:1409`) | 200 (200-200), 1 (1-1), 260.6 / 200 (8-200), 1 (1-1), 281.0 | 64 (64-64), 1 (1-1), 165.4 / 64 (4-64), 1 (1-1), 181.0 | (L) 0/20 (refused 0), 0/20 (refused 0); (M) 5/20 (refused 109), 7/20 (refused 294) | (L) 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 1; (M) 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 1 | no | stdlib's own retries of pre-SETTINGS refusals reserve outside the token and can reach the limit again |
| (iv-b) strict + write token; the first request on a new connection (`GotConnInfo.Reused == false`) holds it until its response headers | 200 (200-200), 1 (1-1), 271.5 / 200 (200-200), 1 (1-1), 291.8 | 64 (64-64), 1 (1-1), 175.5 / 64 (64-64), 1 (1-1), 192.1 | (L) 0/20 (refused 0), 0/20 (refused 0); (M) 0/20 (refused 2), 0/20 (refused 1) | (L) 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 1; (M) 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 1 | no | a cold burst's waiters (and the first burst on a re-dialed connection) wait for the leader's response: 64 calls at 50 ms service, wall 103.4 vs 52.2 ms (L), 104.4 vs 52.8 (M); 8 vs 8, 21.5 vs 11.5 ms (L) |
| (v) non-strict + limiter 100 | 200 (200-200), 13 (13-13), 1020.8 / 200 (200-200), 13 (13-13), 48.9 | 64 (64-64), 14 (14-16), 1021.9 / 64 (64-64), 14 (13-16), 26.8 | (L) 13/20 (refused 531), 17/20 (refused 251); (M) 3/20 (refused 2702), 15/20 (refused 1550) | not run | no | one connection only against servers advertising ≥ 100 (200 vs 1024: 1 (1-1) (L)); retry backoff below that |

Recommendation for W0.6: **(iv-b)**, strict mode plus the gate plus a write
token whose first request per new connection holds it until its response
headers. It is the only option measured with one connection and no stall in
every case on both hosts, quiet or loaded: plain token (iv-a) stalled on the
loaded (M) runs (200 vs 8 ok 200 (8-200), 64 vs 4 ok
64 (4-64); stall rate 5/20 (refused 109) and
7/20 (refused 294)), token-first did not (stall rate
0/20 (refused 2) and 0/20 (refused 1)
on the same host; refused streams 0 in every (L) run and in the (M) matrix).
It needs no knowledge of the server's limit, and because only one request
goes out before a new connection has read the server's SETTINGS it also
removes the `PROTOCOL_ERROR` exposure against Go servers (fake time: 0/20 on
both hosts).
Its cost is one leader response of latency for a cold burst's waiters; after
that the token is held only until `WroteHeaders` (200 vs 8:
271.5 ms against
260.4 ms with an exact limiter, (L)).

AC-P4 change: the clause "every request is on the wire before any response
(the loopback handler holds every response until 64 requests have arrived on
one connection)" cannot hold, because the waiters' HEADERS now follow the
leader's response headers. It becomes: "the leader's request is answered
first; then the 63 waiters' requests are all on the wire, on the same
connection, before any waiter's response (the handler answers the leader at
once and holds every other response until the 63 waiters' requests have
arrived; 5 s guard)". "Cold 64 → 1 connection", "warm → 0 new" and "200 vs
limit 8 → 1 connection" hold as written. Residual (K21): replays that stdlib
makes inside `RoundTripOpt` (`REFUSED_STREAM`, GOAWAY; `:417-446`) reserve
outside the token, so a server that refuses streams below its advertised
limit, or lowers the limit mid-connection, can still park `limit` replays and
stall until the deadline. Runner-up: (ii) with a default of 100 and an option
to set it; it keeps AC-P4 as written but stalls against any server
advertising less than the configured value. Independently, the owner may add
the `net/http` reproduction to golang/go#70809; a fix there (not counting
reservations in `awaitOpenSlotForStreamLocked`) would make the token
unnecessary.

### S-T2: fake-network h2c under `testing/synctest`

The spike wraps `s.Client().Transport` of `testsupport.FakeH2CServer` with the
gate directly (its `DialContext` targets the in-memory listener,
`httptest/server.go:439-444`), after setting `MaxConnsPerHost: 1`,
`IdleConnTimeout: 90s` and the §6.3 `HTTP2Config`. Verdict: **usable for
W2.2, provided every request carries a per-call deadline.**

| Step | (M) W0.4-10 | (L) W0.4-03 |
| --- | --- | --- |
| cold 64-way burst | 64/64 200, accepts 1, roles map[leader:1 waiter:63] | 64/64 200, accepts 1, roles map[warm:38 leader:1 waiter:25] |
| warm 64-way burst | 64/64, accepts 1 | 64/64, accepts 1 |
| `time.Sleep(31s)` (past `SendPingTimeout`) | accepts 1: the ping was answered | accepts 1 |
| `time.Sleep(91s)` (past `IdleConnTimeout`), then one call | 200, accepts 2 (a re-dial behind the warm gate, not gated) | 200, accepts 2 |

Synctest fixes time, not scheduling: roles and the F1 outcomes vary between
runs. F1 on fake time (16 calls vs Go's server with `MaxConcurrentStreams: 4`,
2 s deadline, 20 runs): strict (L) 0/20 all ok, 20/20 with deadlines, 3/20 with a `PROTOCOL_ERROR`, connections 1-2; (M) 0/20 all ok, 20/20 with deadlines, 1/20 with a `PROTOCOL_ERROR`, connections 1-2,
with fake time jumping straight to the deadline (`cc.cond.Wait`, `:1555`, is
a durable block); strict + token-first (L) 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 1;
(M) 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 1.

What synctest refuses (W0.4-12, W0.4-13; `ST2_REFUSALS=1`, each case fails
by design):

| Case | Outcome |
| --- | --- |
| strict stall, no deadline, `SendPingTimeout` 0 | `panic: deadlock: all goroutines in bubble are blocked`, test FAIL (the stalled writer is `sync.Cond.Wait (durable)`) |
| strict stall, no deadline, `SendPingTimeout` 30 s | never reported: the client's health-check timer advances fake time every 30 s, so the bubble is never deadlocked; the run spins (see W0.4-12 for the goroutine ids) until `panic: test timed out after 20s` |
| real loopback socket (`LoopbackServer`) inside a bubble | works (200) while no fake timer has to fire; goroutines blocked on real sockets are not durably blocked, so fake time would not advance for them |

Consequence for W2.2: every synctest transport test carries a per-call
deadline (or sets `SendPingTimeout` 0), otherwise a regression hangs CI
instead of failing it.

### S-T3: recovery on a warm connection (both hosts)

| Event | Client outcome | What stdlib did | Connections |
| --- | --- | --- | --- |
| GOAWAY `LastStreamID` 5 with GET streams 3, 5, 7, 9 in flight | 4/4 200 | 3 and 5 finished on connection 0; 7 and 9 (`Dropped`) were replayed inside their RoundTrip on connection 1; the next request went to connection 1 (`MarkDead`, `internal/http2/transport.go:2618`) | accepts 2: the replay dialed while connection 0 still served 3 and 5, because the GOAWAY'd connection is dropped and uncounted through `removeIdleConn` + `decConnsPerHost` (`transport.go:741-744`) |
| same, POST with `GetBody` | 200, replayed on connection 1 | body re-read with `GetBody` (`:519`) | same test as the next row (accepts 2 in total) |
| same, POST with `GetBody` nil | error `http2: Transport: cannot retry err [http2: Transport received Server's graceful shutdown GOAWAY] after Request.Body was written; define Request.GetBody to avoid this error` (`*errors.errorString`, `:534`) | no replay | |
| `REFUSED_STREAM` on one stream | 200 | retried at once on the same connection (one stream refused, the next served) | accepts 1 |
| clean close under a held stream (`CloseConns`: close_notify and FIN) | `unexpected EOF` (`errors.Is(err, io.ErrUnexpectedEOF)`; not a `net.Error`; not retried) | next request dials | accepts 2 |
| TCP reset under a held stream (`H2Conn.Reset`, `SO_LINGER` 0) | `read tcp …: read: connection reset by peer`: `*net.OpError{Op: "read"} -> *os.SyscallError -> syscall.Errno`, `errors.Is(err, syscall.ECONNRESET)`, a `net.Error` with `Timeout() == false`; not retried | next request dials | accepts 2 |
| idle-close race, 100 trials, clean close as a request starts | (L) 6 `unexpected EOF`, 94 ok; (M) 10 `unexpected EOF`, 90 ok | the request reached the old connection in 47 (L) / 45 (M) trials | |
| idle-close race, TCP reset | (L) 2 `read tcp 127.0.0.1:P->127.0.0.1:P: read: connection reset by peer`, 1 `write tcp 127.0.0.1:P->127.0.0.1:P: write: connection reset by peer`, 97 ok; (M) 8 `read tcp 127.0.0.1:P->127.0.0.1:P: read: connection reset by peer`, 2 `write tcp 127.0.0.1:P->127.0.0.1:P: write: broken pipe`, 90 ok | 50 / 49 | |
| idle-close race, GOAWAY(1) then close | (L) 100 ok; (M) 2 `unexpected EOF`, 98 ok | 47 / 48 | |

### S-T4: replay and failure classification (both hosts)

| Case | RoundTrips | Outcome | Error chain (`%T(message)`, outermost first) | W2.2 class |
| --- | --- | --- | --- | --- |
| GOAWAY `LastStreamID` 0 before the request was processed, GET | 1 | 200; seen on connection 0 (`goaway`) then 1 (`serve`); accepts 2 | none | success; the replay is invisible to any wrapper |
| same, POST with `GetBody` | 1 | 200; body served once, on connection 1 | none | success |
| same, POST with `GetBody` nil | 1 | error; accepts 1 | `*errors.errorString(http2: Transport: cannot retry err [...GOAWAY] after Request.Body was written; ...)` | `*ConnectionError` (the SDK always sets `GetBody`) |
| body fully read by the server, then a clean close | 1 | error | `*errors.errorString(unexpected EOF)` (`io.ErrUnexpectedEOF`) | `*ConnectionError` |
| body fully read, then a TCP reset | 1 | error | `*net.OpError(read tcp …: read: connection reset by peer) -> *os.SyscallError -> syscall.Errno` (`ECONNRESET`; `Timeout()` false) | `*ConnectionError` |
| body fully read, then `RST_STREAM INTERNAL_ERROR` | 1 | error | `http2.StreamError(stream error: stream ID 3; INTERNAL_ERROR; received from peer)` (unexported package) | `*ConnectionError` |
| `ALPNHTTP1Only` server (alert 120), with or without the ALPN check | 1 | no request reached the server; server: `tls: client requested unsupported application protocols (["h2"])` | `*tls.permanentError(remote error: tls: no application protocol) -> *net.OpError{Op: "remote error", Net: ""} -> tls.alert(tls: no application protocol)` | `ErrHTTP2NotNegotiated`; the outermost value is `*tls.permanentError`, not the `*net.OpError` of §3.2, but `errors.As` finds the OpError |
| `ALPNNone` server, ALPN check on | 1 | no request; server: `remote error: tls: bad certificate` (a `VerifyConnection` failure sends that alert) | `*errors.errorString(h2gate: HTTP/2 not negotiated)`: the `VerifyConnection` error comes back unwrapped; `errors.Is(err, ErrNotNegotiated)` | `ErrHTTP2NotNegotiated` |
| `ALPNNone` server, bare stock transport (`Protocols{HTTP2}` only, no check) | 1 | **200 over HTTP/1.1** (`ProtoMajor` 1): h2 is chosen only when ALPN says `h2` (`transport.go:2058-2061`); otherwise the connection falls through to HTTP/1.1 (`:2108-2124`) although `Protocols` lacks HTTP/1 | none | the reason the check exists |

### S-T5 and S-T5b: CONNECT proxies (both hosts)

API host `example.com` (resolved by the proxy's `Routes` to the loopback
server), proxy at `127.0.0.1`, selected with `Proxy: http.ProxyURL(...)`; the
transport's dialer resolves only loopback literals. Build-time ALPN scope for
a caller `Proxy` func: `sni` (check iff `cs.ServerName == eff(apiHost)`).

| Case | Outcome | Handshakes seen by `VerifyConnection` | Proxy |
| --- | --- | --- | --- |
| plain HTTP/1.1 proxy, 16-way cold burst, gate and no gate | 16/16 200 `h2 example.com`; server accepts 1; 1 dial | one: `{ServerName "example.com", NegotiatedProtocol "h2", TLS 1.3}` (checked, passes) | accepts 1, CONNECTs 1 (`example.com:443` → 200) |
| TLS proxy, lenient ALPN | 200 | proxy hop `{ServerName "", NegotiatedProtocol ""}` (not checked), API hop `{"example.com", "h2"}` (checked, passes) | CONNECT 200 |
| TLS proxy, strict ALPN (http/1.1 only) | `proxyconnect tcp: remote error: tls: no application protocol`; chain `*net.OpError{Op: "proxyconnect", Net: "tcp", Source: nil, Addr: nil} -> *tls.permanentError -> *net.OpError{Op: "remote error"} -> tls.alert`; both the proxy and the not-negotiated flags set | none (the proxy refused before `VerifyConnection`) | handshake error at the proxy, 0 CONNECTs, server accepts 0 |
| TLS proxy offering h2 | 200 | proxy hop negotiated `h2` (`ServerName ""`, not checked), API hop `h2` | the transport still wrote an HTTP/1.1 CONNECT (`transport.go:1985`) and this proxy, which reads HTTP/1.1 whatever ALPN said, answered 200; a proxy that really speaks h2 after negotiating it would fail (K16 stands) |
| wrong scope for comparison (`every-handshake`), lenient proxy | `proxyconnect tcp: h2gate: HTTP/2 not negotiated` (`*net.OpError{Op: "proxyconnect"} -> *errors.errorString`); proxy saw `remote error: tls: bad certificate`, 0 CONNECTs | proxy hop `{"", ""}` refused | the SNI scope is required |

W2.2 must test for the `proxyconnect` wrapper before the alert-120 match:
both flags are set for the strict proxy, and §6.3 maps it to a proxy
`*ConnectionError`. Every `ConnectionState` that `VerifyConnection` received
had `HandshakeComplete: false` (it runs inside the handshake), so the check
must not read that field.

Build-time proxy decision (§6.3), confirmed:

| Probe | Result |
| --- | --- |
| `reflect.ValueOf(p).Pointer() == reflect.ValueOf(http.ProxyFromEnvironment).Pointer()` | true for `http.ProxyFromEnvironment`, `http.DefaultTransport.(*http.Transport).Proxy`, `Transport.Clone().Proxy` and a func variable holding it; false for `http.ProxyURL(u)`, a closure that calls `ProxyFromEnvironment`, and nil |
| decision (`HTTP(S)_PROXY` and `NO_PROXY` cleared with `t.Setenv` before first use) | `Proxy` nil → every handshake; `ProxyFromEnvironment` for `https://example.com` → every handshake (it returned nil); `ProxyURL` or a closure for `example.com` → `sni`; `ProxyURL` for `https://127.0.0.1:8443` → post-check only; `ProxyURL` with `ServerName` set → post-check only |
| `eff()` | `example.com` → `example.com`; `example.com.` → `example.com`; `127.0.0.1` → `""`; `[::1]` → `""`; `ServerName "api.internal"` over `127.0.0.1` → `api.internal` |
| real `ConnectionState.ServerName` (lenient TLS proxy) | proxy hop `""` = `eff("127.0.0.1")`, API hop `"example.com"` = `eff("example.com")`; the check ran on the API hop only |

### GOROOT lines relied on (go1.27.1)

`net/http/transport.go`: `:577-592` `alternateRoundTripper`, `:643` alt
path, `:741-744` `ErrNoCachedConn` → `removeIdleConn` + `decConnsPerHost`,
`:1160`/`:1181-1188` h2 idle hand-out, `:1241` `queueForIdleConn`, `:1596`
detached dial context, `:1661-1688` `queueForDial`, `:1706-1726`
`dialConnFor`, `:1730`/`:1748-1766` `decConnsPerHost` permit hand-off,
`:1794-1798` TLS handshake timer, `:1871-1876` `proxyconnect` wrap, `:1985`
CONNECT request, `:2058-2074` h2 selection, `:2108-2124` HTTP/1.1
fall-through, `:3385-3389` `tlsHandshakeTimeoutError`. `net/http/http2.go`:
`:294` alt registration, `:362` pooled round tripper, `:437-445`
`mapCachedConnErr`. `net/http/clientconn.go`: `:113`, `:277`, `:284`.
`net/http/internal/http2/transport.go`: `:57`/`:624` initial 100 streams,
`:113-120` `AddConn`, `:308`/`:318` abort broadcast, `:408-465`
`RoundTripOpt` (retry loop `:417-446`, backoff `:434`, `traceGotConn` `:424`),
`:507-546` `shouldRetryRequest`/`canRetryError`, `:744-752`
`ReserveNewRequest`, `:821-856` `idleStateLocked` (`:831` strict, `:839`
non-strict), `:885-887` `currentRequestCountLocked`, `:1259`/`:1270-1271`
`writeRequest`, `:1409` `traceWroteHeaders`, `:1436-1438` reservation
returned again, `:1539-1561` `awaitOpenSlotForStreamLocked`, `:1875-1889`
`forgetStreamID`, `:2616-2618` GOAWAY `MarkDead`, `:2706`/`:2720`
`processSettings`. `net/http/internal/http2/client_conn_pool.go`: `:51-69`,
`:258`. `net/http/internal/http2/server.go`: `:1905-1916`.

### W0.4b: FirstHold and the reworded AC-P4 ordering clause

At W0.6 the owner chose F1 option (iv-b) (G2): strict mode, the gate, and a
header-write token that the first request on each new connection holds
until its response headers (`WriteToken.FirstHold`). AC-P4's ordering
clause becomes "the leader's request is answered first; then the 63
waiters' requests are all on the wire, on the same connection, before any
waiter's response (the handler answers the leader at once and holds every
other response until the 63 waiters' requests have arrived; 5 s guard)".
"Cold 64 → 1 connection", "warm → 0 new" and "200 vs limit 8 → 1
connection" stay. W0.4b measures the reworded clause before W0.6 freezes
it.

Code (`_spikes/s-t/`, base `b36af4f`). Rows W0.4b-01 to W0.4b-05 were
produced by the tree at `d47b403`. Rows W0.4b-06 to W0.4b-10 were produced
by the tree at the commit that adds them, which adds the second round
(the W0.6 critic's input) below the first.

First round:

- `TestST1FanOut` gains two variants. The three W0.4 variants run as before;
  their RESULT lines only gain the `lead_to_waiter_write_*` fields.
  `gate+token-first` is (iv-b). The reworded handler (`barrier.free`) serves
  its cold burst: it answers the first request to reach it at once and holds
  every other response until the 63 requests after that one have arrived
  (5 s guard). A burst passes the reworded clause only if all of these
  hold:
  - (a) the first request the handler saw is the leader's (the path carries
    the caller index; the gate reports the role);
  - (b) the client received the leader's first response byte before any
    other caller's `WroteHeaders` (`GotFirstResponseByte` fires in
    `processHeaders`, `internal/http2/transport.go:2128`, before
    `close(cs.respHeaderRecv)` at `:2156` lets `RoundTrip` return);
  - (c) the guard released nothing;
  - (d) every request arrived on connection 0;
  - every call returned 200, and the cold burst opened exactly 1
    connection.

  Its warm burst keeps the original clause (all 64 requests on the wire
  before any response). Any violation fails the test.
- `control:gate+token` runs the same handler and checks over the plain
  token (iv-a) and asserts nothing. It shows that the checks can fail.
- For every gated variant, `lead_to_waiter_write` is the gap from the
  leader's `WroteHeaders` to the first other caller's `WroteHeaders` in a
  burst (p50 and max over the 10 bursts of a run). The difference between
  token-first and the plain token is the cost of the hold.
- `TestST1bFirstHold` runs four S-T1b cases through `runF1`, which holds
  `TestST1bStreamLimit`'s per-case body, moved unchanged: 200 vs 8 and 64
  vs 4 with token-first, and 64 cold calls at 50 ms service time with the
  gate alone and with token-first. It fails unless every call succeeds on
  one connection.

Second round:

- The `/lead5ms` pair: `barrier.freeDelay` makes the reworded handler
  answer the first request after 5 ms instead of at once.
  `gate+token-first/lead5ms` must pass every burst.
  `control:gate+token/lead5ms` must fail check (b) in every burst, and the
  test asserts that it does.
- `WriteToken.HoldBound` bounds the FirstHold hold: the token is released
  `HoldBound` after the first request's `WroteHeaders`, even if that
  request's response headers have not arrived. `TestST1FirstHoldBound`
  sets no per-call deadline (as `WithNoTimeout`), sets the gate's wait bound
  to 500 ms, and has the server hold the leader's response for 1 s (twice
  the bound). In `bound500ms` (`HoldBound` 500 ms) the first other HEADERS
  must go out at least 500 ms after the leader's, every other call must
  finish before the leader, and all 64 calls must succeed on one
  connection. In `unbounded` (`HoldBound` 0) the other callers must wait
  for the leader's whole response. The fan-out variants and the S-T1b
  cases leave `HoldBound` at 0.
- `TestST1bFirstHold` adds a pair at 200 ms service time: the gate alone
  and token-first.
- Token scope: the harness makes one `WriteToken` per transport (`wrap`,
  `runF1`), and `RoundTrip` takes the token before the wrapped `RoundTrip`
  runs. That is required, not a choice. `RoundTripOpt` gets the connection
  through `GetClientConn` (`internal/http2/transport.go:418`), and the pool
  reserves the stream there (`ReserveNewRequest`,
  `internal/http2/client_conn_pool.go:54`, `:81`) before `traceGotConn`
  (`:424`). A token taken per connection at `GotConn` would leave the
  reservations uncounted, and F1 would return.

Commands use W0.4's `R`, `MO` and `SP`. On (L) the tree is copied with the
tar pipe to `/tmp/ts-spike/src-w0.4b/wt-w0.4-firsthold`: the SHA-256 of the
193 files (first round) and of the 198 files (second round) matched
(M)'s. `LO=/tmp/ts-spike/src-w0.4b/results`, and the environment is
W0.4's. Every row holds the shared lock and applies the quiet-host rule
(`MAXLOAD` 16 on (M), 44 on (L)). No row had to wait.

#### W0.4b rows

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W0.4b-01 | 2026-09-25 10:52:12 UTC | W0.4b S-T1 cold 64 fan-out, FirstHold | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.06 → 0.06 | `MAXLOAD=44 sh $R '(L)' $LO /tmp/ts-spike/bench.lock l-st1-fanout-w04b -count=5 -run '^TestST1FanOut$' -v ./_spikes/s-t/` | gate+token-first: 1 in 50/50 one-connection bursts, warm 0 in 50/50, reworded ordering 10/10 in 5/5 runs (leader first 50/50, leader answered before any waiter write 50/50), waiter wire p50 1.451 (1.424-1.638) / p99 2.537 (2.071-2.889) ms, leader write → first waiter write p50 0.1 (0.09-0.115) ms against 0.057 (0.048-0.08) with gate+token; control (plain token, not asserted): reworded ordering 37/50 | [W0.4b results](#w04b-results); `results/l-st1-fanout-w04b.txt` |
| W0.4b-02 | 2026-09-25 10:52:14 UTC | W0.4b S-T1b FirstHold cases | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.06 → 0.06 | `MAXLOAD=44 sh $R '(L)' $LO /tmp/ts-spike/bench.lock l-st1b-firsthold -count=5 -run '^TestST1bFirstHold$' -v ./_spikes/s-t/` | 200 vs 8 token-first: ok 200 (200-200), accepts 1 (1-1), wall 271.7 ms; 64 vs 4 token-first: ok 64 (64-64), accepts 1 (1-1), wall 176.0 ms; 64 cold at 50 ms service: gate 52.7 ms, token-first 103.3 ms wall | [W0.4b results](#w04b-results); `results/l-st1b-firsthold.txt` |
| W0.4b-03 | 2026-09-25 19:52:18 JST | W0.4b S-T1 cold 64 fan-out, FirstHold | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.82 → 9.82 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-st1-fanout-w04b -count=5 -run '^TestST1FanOut$' -v ./_spikes/s-t/` | gate+token-first: 1 in 50/50 one-connection bursts, warm 0 in 50/50, reworded ordering 10/10 in 5/5 runs (leader first 50/50, leader answered before any waiter write 50/50), waiter wire p50 1.064 (1.004-1.214) / p99 1.928 (1.747-2.29) ms, leader write → first waiter write p50 0.084 (0.076-0.095) ms against 0.039 (0.034-0.075) with gate+token; control (plain token, not asserted): reworded ordering 22/50 | [W0.4b results](#w04b-results); `results/m-st1-fanout-w04b.txt` |
| W0.4b-04 | 2026-09-25 19:52:19 JST | W0.4b S-T1b FirstHold cases | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.82 → 9.43 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-st1b-firsthold -count=5 -run '^TestST1bFirstHold$' -v ./_spikes/s-t/` | 200 vs 8 token-first: ok 200 (200-200), accepts 1 (1-1), wall 293.0 ms; 64 vs 4 token-first: ok 64 (64-64), accepts 1 (1-1), wall 190.4 ms; 64 cold at 50 ms service: gate 53.2 ms, token-first 103.9 ms wall | [W0.4b results](#w04b-results); `results/m-st1b-firsthold.txt` |
| W0.4b-05 | 2026-09-25 19:53:58 JST | W0.4b spike package under -race | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 5.91 → 6.91 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-race-w04b -race -count=1 -timeout 900s ./_spikes/s-t/` | `ok` in 47.917s | not a measurement; locked so it cannot overlap one; `results/m-race-w04b.txt` |
| W0.4b-06 | 2026-09-25 11:03:58 UTC | W0.4b S-T1 cold 64 fan-out, second round | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.46 → 0.46 | `MAXLOAD=44 sh $R '(L)' $LO /tmp/ts-spike/bench.lock l-st1-fanout-w04b2 -count=5 -run '^TestST1FanOut$' -v ./_spikes/s-t/` | reworded clause (bursts): token-first 50/50, plain token 40/50 (one run 10/10), token-first/lead5ms 50/50, plain token/lead5ms 0/50; every variant 1 connection cold in 50/50, 0 new warm in 50/50 | [second round](#w04b-second-round); `results/l-st1-fanout-w04b2.txt` |
| W0.4b-07 | 2026-09-25 11:04:00 UTC | W0.4b hold bound, S-T1b FirstHold cases incl. 200 ms | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.46 → 0.33 | `MAXLOAD=44 sh $R '(L)' $LO /tmp/ts-spike/bench.lock l-st1b-firsthold2 -count=5 -run '^(TestST1bFirstHold\|TestST1FirstHoldBound)$' -v ./_spikes/s-t/` | hold bound 500 ms, leader held 1 s: first waiter HEADERS 500.91 ms after the leader's, waiters done 503.94 ms, leader 1002.13 ms, 64/64, accepts 1; unbounded: 1000.32 ms; 64 cold at 200 ms service: gate 202.7 ms, token-first 403.3 ms wall | [second round](#w04b-second-round); `results/l-st1b-firsthold2.txt` |
| W0.4b-08 | 2026-09-25 20:04:02 JST | W0.4b S-T1 cold 64 fan-out, second round | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.11 → 9.34 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-st1-fanout-w04b2 -count=5 -run '^TestST1FanOut$' -v ./_spikes/s-t/` | reworded clause (bursts): token-first 50/50, plain token 18/50, token-first/lead5ms 50/50, plain token/lead5ms 0/50; every variant 1 connection cold in 50/50, 0 new warm in 50/50 | [second round](#w04b-second-round); `results/m-st1-fanout-w04b2.txt` |
| W0.4b-09 | 2026-09-25 20:04:04 JST | W0.4b hold bound, S-T1b FirstHold cases incl. 200 ms | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.34 → 8.99 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-st1b-firsthold2 -count=5 -run '^(TestST1bFirstHold\|TestST1FirstHoldBound)$' -v ./_spikes/s-t/` | hold bound 500 ms, leader held 1 s: first waiter HEADERS 500.346 ms after the leader's, waiters done 505.699 ms, leader 1002.48 ms, 64/64, accepts 1; unbounded: 1001.46 ms; 64 cold at 200 ms service: gate 204.2 ms, token-first 405.2 ms wall | [second round](#w04b-second-round); `results/m-st1b-firsthold2.txt` |
| W0.4b-10 | 2026-09-25 20:04:20 JST | W0.4b spike package under -race, second round | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.99 → 7.23 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-race-w04b2 -race -count=1 -timeout 900s ./_spikes/s-t/` | `ok` in 49.982s | not a measurement; locked so it cannot overlap one; `results/m-race-w04b2.txt` |

#### W0.4b results

Cold 64-way fan-out, 10 bursts per run, 5 runs; each latency cell is the
median of the 5 runs' values (range in parentheses), as in the S-T1 table.
"Ordering" is the original clause for the W0.4 variants and the reworded
clause (cold burst) plus the original clause (warm burst) for the other
two. The last column is the leader's `WroteHeaders` to the first other
caller's `WroteHeaders`, p50 over a run's bursts (range) / max over a run's
bursts, both medians of 5 runs.

| Host | Variant | Cold: one connection | Warm: no new | Ordering | Wire p50 ms | Wire p99 ms | Span p50 ms | Warm wire p50 / p99 ms | Leader write → first waiter write ms |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| (L) | gate (waiters) | 1 in 50/50 | 0 in 50/50 | 10/10 in 5/5 | 1.241 (1.23-1.778) | 2.392 (1.732-4.123) | 1.496 | 0.355 / 1.325 | 0.011 (0.01-0.012) / 0.035 |
| (L) | no gate (all callers) | 1 in 50/50 | 0 in 50/50 | 10/10 in 5/5 | 1.304 (1.222-1.776) | 2.498 (1.886-2.817) | 1.509 | 0.343 / 1.556 | n/a |
| (L) | gate + write token (waiters) | 1 in 50/50 | 0 in 50/50 | 10/10 in 5/5 | 1.51 (1.438-1.547) | 2.447 (2.282-3.755) | 1.939 | 0.606 / 2.118 | 0.057 (0.048-0.08) / 0.107 |
| (L) | gate + token-first, FirstHold (waiters) | 1 in 50/50 | 0 in 50/50 | reworded: 10/10 in 5/5 | 1.451 (1.424-1.638) | 2.537 (2.071-2.889) | 1.892 | 0.54 / 1.514 | 0.1 (0.09-0.115) / 0.15 |
| (L) | control: gate + write token, reworded handler (waiters) | 1 in 50/50 | 0 in 50/50 | reworded: 8, 8, 8, 7, 6 of 10 | 1.46 (1.435-1.578) | 2.639 (2.023-3.162) | 1.889 | 0.545 / 1.845 | 0.068 (0.048-0.081) / 0.114 |
| (M) | gate (waiters) | 1 in 50/50 | 0 in 50/50 | 10/10 in 5/5 | 1.071 (0.85-1.363) | 1.972 (1.117-2.44) | 1.306 | 0.363 / 1.059 | 0.013 (0.007-0.018) / 0.028 |
| (M) | no gate (all callers) | 1 in 50/50 | 0 in 50/50 | 10/10 in 5/5 | 0.923 (0.873-1.397) | 1.292 (1.232-1.942) | 1.184 | 0.36 / 1.039 | n/a |
| (M) | gate + write token (waiters) | 1 in 50/50 | 0 in 50/50 | 10/10 in 5/5 | 1.034 (0.935-1.405) | 1.857 (1.482-2.271) | 1.362 | 0.435 / 1.128 | 0.039 (0.034-0.075) / 0.118 |
| (M) | gate + token-first, FirstHold (waiters) | 1 in 50/50 | 0 in 50/50 | reworded: 10/10 in 5/5 | 1.064 (1.004-1.214) | 1.928 (1.747-2.29) | 1.443 | 0.425 / 1.189 | 0.084 (0.076-0.095) / 0.162 |
| (M) | control: gate + write token, reworded handler (waiters) | 1 in 50/50 | 0 in 50/50 | reworded: 6, 6, 5, 4, 1 of 10 | 1.014 (0.891-1.123) | 2.129 (1.809-2.246) | 1.284 | 0.45 / 1.156 | 0.03 (0.013-0.032) / 0.109 |

The reworded clause, part by part. Each count covers 50 bursts: 5 runs of
10. The last column is the leader's first response byte to the first
other caller's `WroteHeaders`: the minimum over a run's bursts / the p50
over a run's bursts, both medians of 5 runs with the range. A negative
value means that a waiter wrote before the leader's response arrived.
For the control, (c) and (d) come from the raw files: no guard fired (a
guard release adds 5 s, and every run of `TestST1FanOut` took 0.15 to
0.24 s), and each server accepted one connection, so connection 0 was the
only one.

| Host | Variant | Reworded clause | (a) first request at the handler is the leader's | (b) leader's response before any other HEADERS | (c) guard released nothing, (d) connection 0 | Leader's response → first other HEADERS, min / p50 ms |
| --- | --- | --- | --- | --- | --- | --- |
| (L) | gate + token-first | 50/50 | 50/50 | 50/50 | 50/50 | 0.04 (0.036-0.045) / 0.059 (0.048-0.071) |
| (L) | control: gate + write token | 37/50 | 48/50 | 37/50 | 50/50 | -0.054 (-1.432 to -0.015) / 0.021 (0.008-0.041) |
| (M) | gate + token-first | 50/50 | 50/50 | 50/50 | 50/50 | 0.018 (0.016-0.027) / 0.035 (0.024-0.046) |
| (M) | control: gate + write token | 22/50 | 39/50 | 22/50 | 50/50 | -0.94 (-1.313 to -0.838) / -0.017 (-0.032 to 0.001) |

S-T1b cases with FirstHold, 5 runs each; each cell is the median
(min-max). The W0.4 column holds the medians of the F1 tables. The
server's stream high-water mark was 8, 4, 64 and 63 in the four cases, and
refused streams were 0 in every run on both hosts.

| Case | (L) ok, accepts, wall ms | (M) ok, accepts, wall ms | W0.4 wall ms (L) / (M) |
| --- | --- | --- | --- |
| 200 vs 8, token-first | 200 (200-200), 1 (1-1), 271.7 (271.4-275.4) | 200 (200-200), 1 (1-1), 293.0 (291.9-296.4) | 271.5 / 291.8 |
| 64 vs 4, token-first | 64 (64-64), 1 (1-1), 176.0 (175.7-177.8) | 64 (64-64), 1 (1-1), 190.4 (185.0-192.9) | 175.5 / 192.1 |
| 64 cold, 50 ms service, gate alone (`64vs0/strict/service50ms`) | 64 (64-64), 1 (1-1), 52.7 (51.9-53.4) | 64 (64-64), 1 (1-1), 53.2 (52.7-58.1) | 52.2 / 52.8 |
| 64 cold, 50 ms service, token-first | 64 (64-64), 1 (1-1), 103.3 (102.8-103.8) | 64 (64-64), 1 (1-1), 103.9 (102.7-108.0) | 103.4 / 104.4 |

#### W0.4b second round

These are the W0.6 critic's four checks, run against the tree that adds
them (W0.4b-06 to W0.4b-09). The W0.4 variants and the at-once pair
repeat the first round, and every variant of W0.4b-06 and W0.4b-08 opened
one connection per cold burst and none per warm burst in 50/50 bursts.

The discrimination table (W0.4b-06, W0.4b-08) counts the bursts that
passed the reworded clause (per run in brackets), check (a), and
check (b). "Response → HEADERS" is the leader's first response byte to the
first other caller's `WroteHeaders`: the minimum over a run's bursts / the
p50 over a run's bursts, both medians of 5 runs (range). A negative value
means that a waiter wrote before the leader's response arrived. "Write →
write" is the leader's `WroteHeaders` to the first other `WroteHeaders`,
p50 over the bursts of a run, median of 5 (range).

| Host | Variant | Reworded clause | (a) | (b) | Response → HEADERS, min / p50 ms | Waiter wire p50 / p99 ms | Write → write ms |
| --- | --- | --- | --- | --- | --- | --- | --- |
| (L) | token-first, leader answered at once | 50/50 [10, 10, 10, 10, 10] | 50/50 | 50/50 | 0.037 (0.033-0.048) / 0.056 (0.049-0.059) | 1.507 (1.432-1.633) / 2.799 (2.277-3.219) | 0.099 (0.085-0.12) |
| (L) | plain token, leader answered at once | 40/50 [7, 7, 7, 9, 10] | 48/50 | 40/50 | -0.037 (-1.061 to 0.013) / 0.019 (0.009-0.045) | 1.461 (1.403-1.543) / 2.272 (2.215-2.489) | 0.07 (0.053-0.09) |
| (L) | token-first, leader answered after 5 ms | 50/50 [10, 10, 10, 10, 10] | 50/50 | 50/50 | 0.038 (0.03-0.04) / 0.045 (0.043-0.051) | 6.632 (6.56-6.655) / 7.518 (7.054-7.93) | 5.256 (5.244-5.267) |
| (L) | plain token, leader answered after 5 ms | 0/50 [0, 0, 0, 0, 0] | 47/50 | 0/50 | -5.906 (-6.034 to -5.573) / -5.484 (-5.506 to -5.462) | 1.45 (1.411-1.495) / 2.447 (2.274-3.669) | 0.062 (0.04-0.066) |
| (M) | token-first, leader answered at once | 50/50 [10, 10, 10, 10, 10] | 50/50 | 50/50 | 0.02 (0.016-0.023) / 0.032 (0.021-0.05) | 1.03 (0.932-1.145) / 1.747 (1.369-2.137) | 0.084 (0.061-0.109) |
| (M) | plain token, leader answered at once | 18/50 [5, 5, 5, 2, 1] | 41/50 | 18/50 | -0.887 (-1.177 to -0.031) / -0.015 (-0.018 to -0.008) | 1.016 (0.945-1.33) / 1.975 (1.434-2.11) | 0.05 (0.014-0.061) |
| (M) | token-first, leader answered after 5 ms | 50/50 [10, 10, 10, 10, 10] | 50/50 | 50/50 | 0.026 (0.019-0.032) / 0.044 (0.032-0.057) | 6.765 (6.603-6.95) / 7.423 (7.145-8.407) | 5.749 (5.732-5.781) |
| (M) | plain token, leader answered after 5 ms | 0/50 [0, 0, 0, 0, 0] | 45/50 | 0/50 | -5.581 (-5.596 to -5.561) / -5.505 (-5.531 to -5.465) | 1.098 (1.046-1.199) / 2.038 (1.606-2.15) | 0.049 (0.013-0.07) |

The hold bound table (`TestST1FirstHoldBound`; W0.4b-07, W0.4b-09) has no
per-call deadline, a gate wait bound of 500 ms, and the leader's response
held 1 s. Times are from the burst's first call start, except the gap,
which is measured from the leader's `WroteHeaders`; each is the median of
5 runs (range). In every run the gate released the 63 waiters at the
leader's `GotConn` (roles leader 1, waiter 63): the dial took about 1 ms,
so the waiters were stopped by the token, and `HoldBound` released them.

| Host | Hold bound | ok | Accepts | Held request is the leader's | First other HEADERS after the leader's, ms | Last other call done, ms | Leader done, ms |
| --- | --- | --- | --- | --- | --- | --- | --- |
| (L) | 500 ms | 64/64 in 5/5 | 1 (1-1) | 5/5 | 500.91 (500.752-501.15) | 503.94 (502.953-504.192) | 1002.13 (1001.71-1002.77) |
| (L) | none | 64/64 in 5/5 | 1 (1-1) | 5/5 | 1000.32 (1000.23-1000.75) | 1003.36 (1002.73-1003.54) | 1001.51 (1001.16-1001.86) |
| (M) | 500 ms | 64/64 in 5/5 | 1 (1-1) | 5/5 | 500.346 (500.151-500.878) | 505.699 (502.608-506.321) | 1002.48 (1002.22-1004.7) |
| (M) | none | 64/64 in 5/5 | 1 (1-1) | 5/5 | 1001.46 (1000.63-1001.96) | 1004.63 (1004.29-1011.92) | 1002.83 (1002.53-1009.28) |

The cold-burst cost table covers 64 cold calls with no stream limit: the
gate alone against token-first, wall ms, median (min-max) of 5 runs. The
50 ms first-round row is W0.4b-02/04; the other rows are W0.4b-07/09.

| Service time | (L) gate | (L) token-first | (M) gate | (M) token-first |
| --- | --- | --- | --- | --- |
| 50 ms, first round | 52.7 (51.9-53.4) | 103.3 (102.8-103.8) | 53.2 (52.7-58.1) | 103.9 (102.7-108.0) |
| 50 ms, second round | 52.4 (52.1-53.5) | 103.1 (102.2-103.6) | 53.9 (52.5-54.9) | 104.8 (104.1-107.7) |
| 200 ms | 202.7 (202.3-203.2) | 403.3 (402.7-404.0) | 204.2 (202.2-210.0) | 405.2 (404.2-409.3) |

In the second round, 200 vs 8 and 64 vs 4 with token-first again
completed every call on one connection with 0 refused streams: (L) 271.8
(271.4-273.6) and 176.0 (175.7-176.4) ms, (M) 295.6 (290.7-298.1) and
190.6 (189.9-196.4) ms.

Findings for W0.6 (both rounds):

1. With FirstHold the reworded clause held in every burst on both hosts:
   100/100 with the leader answered at once and 50/50 with it answered
   after 5 ms. Every cold burst used one connection, and no warm burst
   opened a new one. The warm burst also kept the original clause (all 64
   requests on the wire before any response).
2. With the leader answered at once, the clause does not reliably tell
   (iv-b) from (iv-a) on loopback. Under the plain token, check (b) passed
   37/50 then 40/50 bursts on (L) and 22/50 then 18/50 on (M), and one
   (L) run passed all 10 of its bursts, so a 10-burst run can pass
   (iv-a). The reason is that the loopback round trip is about as long as
   the token's hand-over to the first waiter. Estimated from the
   first-round medians, the round trip is 0.1 - 0.059 ≈ 0.041 ms (L) and
   0.084 - 0.035 ≈ 0.049 ms (M), and the plain token's write-to-write gap
   is 0.068 ms (L) and 0.03 ms (M). With the leader answered after 5 ms,
   the plain token failed (b) in 50/50 bursts on both hosts: the first
   waiter's HEADERS went out about 5.5 ms before the leader's response.
   Token-first passed 50/50. Check (a) does not discriminate either way:
   with the 5 ms delay the plain token passed it in 47/50 (L) and 45/50
   (M) bursts. W2.2's AC-P4 test
   therefore needs (b), taken from client-side traces, and a leader
   answer delayed by a fixed amount (5 ms here) instead of "at once".
   The wording change is a question for the owner (see the report).
   Under FirstHold, (b) holds by construction: its smallest margin in the
   150 bursts per host was 0.03 ms (L) and 0.016 ms (M).
3. Token scope: the harness uses one token per transport, taken before
   `RoundTrip` (see Code). A per-connection token cannot work, because the
   stream is reserved before `GotConn`.
4. Bound (K19): without a bound, a first response that never arrives
   blocks every other call until that call's own context ends, which never
   happens under `WithNoTimeout`. With `HoldBound` equal to the gate's wait
   bound, the other callers' HEADERS went out 500.9 ms (L) and 500.3 ms
   (M) after the leader's. They were answered on the same connection
   (accepts 1) while the leader was still held, finishing at 503.9 and
   505.7 ms; the leader finished at 1002 ms, and all 64 calls succeeded.
   Without the bound they waited 1000.3 and 1001.5 ms. W2.2 should give
   the hold the gate's wait bound (connectTimeout + TLSHandshakeTimeout,
   10 s by default). After the bound the token behaves as (iv-a), which is
   safe once the client has read the server's SETTINGS.
5. Cost: the hold costs one leader response time for each cold burst and
   each re-dialed connection, and the measurements show nothing else.
   - Leader answered at once on loopback: the first waiter's HEADERS go out
     about 0.04 ms later than with the plain token, within the spread of
     the waiter wire latency.
   - Leader answered after 5 ms: the write-to-write gap is 5.3 ms (L) and
     5.7 ms (M), and the waiter wire p50 is 6.6 and 6.8 ms.
   - 64 cold calls at 50 ms service time: 103.1 to 104.8 ms against 52.4
     to 53.9 ms for the gate alone.
   - At 200 ms service time: 403.3 vs 202.7 ms (L) and 405.2 vs 204.2 ms
     (M), so the burst takes twice the service time.
6. "200 vs limit 8 → 1 connection" and 64 vs 4 hold with FirstHold at this
   base in both rounds. Every call succeeded on one connection with 0
   refused streams, and the wall times are within 2 % of W0.4's rows.
   K21 (stdlib-internal replays bypass the token) was not exercised,
   because no stream was refused or replayed.

## W0.3: S-E1 (encode) and S-D1 (decode)

Spike code: `_spikes/s-e1/` and `_spikes/s-d1/` (throwaway; the leading
underscore keeps them out of `./...`, so run them by path, for example
`go test ./_spikes/s-d1/`). Raw outputs: `_spikes/*/results/*.txt`. Each file
opens with the lock, host, base commit, load average, `go version`, ToolTags
and command, and closes with the load and end time, all printed by the same
shell process that ran the measurement. The tables under
[W0.3 tables](#w03-tables) were rendered from those files by a script, so no
number in them was typed by hand. The runs are rows W0.3-01 to W0.3-22 of
the [Rows](#rows) table, in the row format above.

### How the numbers were taken

- (M): `GOEXPERIMENT=nosimd,noruntimesecret`. Lock:
  `/opt/homebrew/opt/util-linux/bin/flock` on the scratchpad `bench.lock`,
  the same file lock as the other lanes (from 16:28 JST; before that this
  lane's `mkdir` lock on the same path did not exclude `flock(1)` users, and
  every run of that period was repeated). Files named `-base76ffd03` are
  locked runs on the pre-rebase base, kept for the before/after comparison
  of the drop-path fix. Superseded outputs (the runs before the lock rule, a
  noisy S-E1 benchmark, and the pre-rebase gate, probe and decode runs that
  2305d02 repeated) are not committed.
- (L): no experiment override; `flock /tmp/ts-spike/bench.lock`.
- Base: each row's Notes name its base. The numbers of record are on
  `2305d02` (this branch was rebased after P0-polish and W0.2b-fix landed),
  except:
  - W0.3-13 and W0.3-17, the (L) S-D1 benchmark and linearity runs, are on
    `76ffd03`. The decoder code, `internal/codec/validate.go` and
    `nocopy.go`, and every benchmarked fixture are byte-identical between
    the two commits, so those runs were not repeated.
  - W0.3-06 and W0.3-14, the (M) benchmarks, are on `cc1524a`: their raw
    files were lost to a global `bench*.txt` ignore rule and re-measured.
    The spikes and `internal/codec` are unchanged since `2305d02` except
    codec tests.
  - W0.3-21 and W0.3-22, the `duplicates.json` allocation runs, are on
    `31f2986`, after 73af053 extended that fixture.
- Allocations: `runtime.ReadMemStats` deltas (`Mallocs`, `TotalAlloc`) with
  the collector off and `GOMAXPROCS(1)` (`testsupport.QuietRuntime`), five
  runs. Each result is the minimum that at least three runs share.
  - S-D1 uses `testsupport.MeasureMin` with a warm `Decoder` whose scratch is
    reused, as a pooled production decoder's would be; the `Response` is
    fresh on every run.
  - S-E1 applies the three-of-five rule to the malloc count only and prints
    the byte range. A `map[string]any` state encodes in random iteration
    order, and its byte count changes from run to run. The nested-map kind
    also varies by ±1 malloc, so it is shown as a range. Before measuring,
    the pool is emptied by two collections, then one call warms it.
  - `testing.AllocsPerRun` is not used anywhere: its warm-up call would
    consume pool state.
- Time: `go test -bench . -benchmem -count=5` with `for b.Loop()`. The tables
  show the median of the five runs and half their min-to-max spread.
  `benchstat` reports the same medians; with five samples it prints `± ∞`.
- Load: allocation counts do not depend on load. Rows are marked `noisy`
  under the row format's rule. The two (M) timing runs of record, the S-D1
  benchmark (W0.3-14) and the S-E1 benchmark (W0.3-06), stayed below the
  core count from start to end.

### S-E1 findings (encode)

1. **Allocations of one steady-state encode** (warm pool, scratch within the
   8 MiB ceiling). They are the same on (M) and (L) and do not depend on
   size:
   - bare `string`: 2 (B = 1 for boxing into `any` at the call site, plus
     E = 1);
   - boxed `any`: 1;
   - `*struct`: 1;
   - `json.RawMessage` through `EncodeInto`: 1;
   - RawJSON appended verbatim: 0.
   - flat `map[string]any`: 2 (E plus one map iterator).
   - `map[string]any` with nested maps: 9, 471, 7 503 and 45 010 at 1 KiB,
     64 KiB, 1 MiB and 6 MiB, about one more per map value (sonic allocates
     an iterator per map).

   NF1 as written (`E_sonic` = 1, B = 1 for a bare string) holds for every
   kind except maps.
2. **`*struct` and Pretouch.** Pretouch is not needed to reach the steady
   state of 1 allocation. `codec.Pretouch` only moves the one-time compile
   out of the first request:
   - (M): 162 allocations and 58.9 µs without Pretouch; `Pretouch` 136
     allocations and 30.7 µs, then a first call of 1 allocation.
   - (L): 2 203 allocations and 941 µs without Pretouch; `Pretouch` 1 885
     allocations and 957 µs, then 1.

   These are single wall-clock readings.
3. **The byte bound.** The clause "Bytes ≤ 1.05 × body + 4 KiB above sonic's
   own" holds in the steady state (at most 112 B per call). A growth call
   allocates the sum of its growth steps:
   - string, boxed, RawJSON and RawMessage states reserve once, so a growth
     call costs 1.00–1.13 × the body (the 1.13 is size-class rounding at
     64 KiB);
   - `*struct` and flat-map states grow in `runtime.growslice` steps: from
     4 KiB to 6 MiB, 20 steps on (M) and 29 on (L) (g = 21 and 30 with E),
     about 4.3 × (M) and 5.2 × (L) the body.
4. **g₆ / g₉**: the allocations of one `EncodeInto` of a 6 MiB / 9 MiB body
   into a fresh 4 KiB scratch, E and B included:
   - string 3 / 3;
   - boxed 2 / 2;
   - RawJSON 1 / 1;
   - RawMessage 2 / 2;
   - `*struct` 21 / 22 on (M), 30 / 32 on (L);
   - flat map 21 / 22 on (M), 31 / 33 on (L);
   - nested map about 45 029 / 67 534, where the per-map allocations
     dominate.

   The plan's probe figures of 5 / 7 are not reproduced by any kind.
5. **Scratch capacity after a 6 MiB encode.** On (M), `*struct` and flat-map
   states end at 9.02 MiB and 9.18 MiB, above the 8 MiB ceiling, so the
   scratch is dropped at call 11. On (L) the same encodes end at 6.75 MiB
   and the scratch is pooled. The string kind reserves `len` + 2 KiB on (M)
   and exactly `len` on (L).
6. **The AC-P1 sequence** (calls 1..32 on 2305d02; the per-call table is
   below):
   - For string, boxed, RawJSON and RawMessage states the plan's shape holds
     on both hosts. Boxed, for example: 8 | 1 ×9 | 2 | 1 ×10 | 2 | 2 | 1 ×9.
   - Call 1 is the first call after two collections. It pays the `Body`, its
     4 KiB buffer and the refill of sonic's own pools (143 688 B for the
     boxed kind).
     TestAllocScratchSequence should warm once rather than assert call 1.
   - Call 23 was base + 2 on 76ffd03 (the `Body` struct plus the buffer). It
     is base + 1 on 2305d02, after P0-polish's one-malloc drop path; this
     lane's local experiment on 76ffd03 (`lazybuf-experiment.diff`) measured
     the same. W0.6 should freeze call 23 = base + 1.
   - For `*struct` and flat-map states on (M), calls 11 and 22 both drop
     their scratch, and call 12 pays base + 1. "Exactly one scratch dropped"
     holds for those kinds on (L) only.
7. **Proposed NF1**: `E_sonic(kind) + B`, with:
   - `E_sonic` = 1 for a boxed value, a pointer or a `json.RawMessage`;
   - 0 for RawJSON;
   - 1 + m for a map-shaped state holding m maps in total (a flat map has
     m = 1, so it costs 2);
   - B = 1 for a bare string.

   The byte clause applies to warm-pool calls; growth calls are reported, not
   budgeted. For AC-P1, the sequence test uses a boxed-string or RawJSON
   state, the only kinds whose growth is exact on both architectures, and
   `g₆ = g₉ = 2` for the boxed kind.

### S-D1 findings (decode)

**R14.** Evidence is in `probe-M.txt` and `probe-L.txt`; the verdicts are the
same on both hosts except for the 1e400 and `"\q"` raw-member cases below.

- **Raw control characters.** A raw U+0000–U+001F byte inside a string value,
  a key or a nested object is accepted by `ast.Preorder` (with
  `OnlyNumber`), `decoder.Skip`, `sonic.ValidString` and
  `sonic.UnmarshalString` in its default configuration.
- **`ValidateString`.** Only `sonic.Config{ValidateString: true}` rejects
  them. It also rewrites invalid UTF-8 to U+FFFD, in keys and inside
  `NoCopyRawMessage` members (`"caf\xff"` becomes `"caf�"`, and the raw
  member is then a copy, not a view of the body). With that option,
  `malformed-invalid-utf8.json` would be accepted, so it cannot be used.
- **What `OnString` receives.**
  - For `"a\nb"` written with an escape (backslash, `n`), `OnString`
    receives `"a\nb"` containing a real LF.
  - For a raw LF inside the string it receives the same bytes.
  - For a raw U+0001 it receives `"x\x01y"` verbatim, as a substring of the
    body.
  - For `\u0001` it receives `"\x01"`.

  So once a string holds an escape, a per-string check cannot tell an
  escaped control character from a raw one.
- **Outcome.** `malformed-control-char.json` and
  `malformed-control-char-key.json` are rejected at `.` by a1, a2 and b on
  both hosts, through the rule below.
- **The rule (implemented and measured).**
  1. Run `codec.ValidString` on every `OnString` and `OnObjectKey` value.
  2. When it fails on valid UTF-8, the string holds a control character.
     Decide once per body with a whole-body scan for a raw byte below 0x20
     inside a string, tracking string boundaries, and remember the answer.
  3. Cost: nothing while no delivered string holds a control character. No
     valid fixture except `escaped-names.json` triggers the scan. When it
     runs, the tracked scan takes 470.0 ns (M) / 695 ns (L) on
     `escaped-names.json` (481 B) and 588.52 µs (M) / 834 µs (L) on 616 KB.
- **Refinement for W2.0.** Test first for any byte below 0x20 with the
  word-at-a-time (SWAR) check: 41.94 µs (M) / 65 µs (L) per 616 KB. Compact
  server bodies contain no such byte, so the tracked scan would then run
  only on bodies with raw TAB, LF or CR whitespace. Running the same SWAR
  pass unconditionally on every response would cost 7.6 % (M) / 9.0 % (L)
  of a `decoder.Skip` on the 10k flood.

**Validity gate**, by fixture class: the 38 module fixtures at 2305d02. The
full per-fixture matrix is in `gate-M.txt` and `gate-L.txt`. Those runs also
covered spike-local copies of the key-position fixtures and
`duplicates.json`, with the same verdicts. The copies were dropped at
landing: main's fixtures are the gate.

| Class | a1 (M) and (L) | a2 (M) and (L) | b (M) | b (L) |
| --- | --- | --- | --- | --- |
| 17 JSON-layer `malformed-*` (empty, whitespace, truncated, trailing ×4, root array, invalid UTF-8 in a value and in a key, control character in a value and in a key, invalid escape, bad literal, double comma, leading zero, trailing comma) | reject at `.` | reject at `.` | reject at `.` | reject at `.` |
| 5 schema `malformed-*` (big-exp, usage-type, missing-model, missing-usage, answers-not-object) | reject, README path | reject, README path | reject; big-exp at `.` instead of `usage.input_tokens` | reject, README path |
| `deviation-big-exp-noul.json` | reject at `answers.spam.noul` | same | reject at `.` | reject at `answers.spam.noul` |
| `deviation-lone-surrogate.json` | accept (U+FFFD in text, `Raw()` keeps `\ud800`) | same | accept | accept |
| `parity-big-exp-unknown.json` | accept | accept | **reject (wrong)** | accept |
| 13 valid bodies, including `duplicates.json` (Python's last-wins result, no WARN) and `models.json` | accept | accept | accept | accept |

- On arm64, variant b's `NoCopyRawMessage` decode refuses `1e400` anywhere
  ("float infinity"); on amd64 it accepts it. The raw-member skip likewise
  accepts `"\q"` on amd64 and rejects it on arm64.
- So b's verdicts depend on the architecture, and it fails the gate on (M):
  **disqualified**. It also had to use sonic's default configuration, since
  `ValidateString` rewrites invalid UTF-8 (see R14).
- (c) was disqualified by the plan and not measured.

**Ranking of the survivors, a1 and a2.**

- Allocations: identical on every fixture, on both hosts.
- Time: a2's median is between 3.7 % below and 3.3 % above a1's, and which
  one is ahead depends on the fixture and the host (per-fixture table
  below).
- The passes where they differ also tie: `decoder.Skip` against
  `sonic.ValidString` measures 457.9 vs 482.0 ns (M) and 516 vs 504 ns (L)
  on `result.json`, and 551.59 vs 543.10 µs (M) and 720 vs 678 µs (L) on the
  10k flood.
- The visitor pass dominates both. A no-op `ast.Preorder` is 1.95 µs of
  a1's 3.22 µs on `result.json` (M), and 289.44 µs of 668.98 µs on the 1k
  flood.

**Winner: a1.** The two measured criteria tie, and two reasons decide:

1. With a1, every failure the traversal meets keeps sonic's positioned parse
   error, and `decoder.Skip` returns the offset of any trailing data. a2's
   `sonic.ValidString` returns only a bool, so every JSON-layer refusal
   would lose its position.
2. a1 is the ADR's design, so choosing it changes nothing in the plan.

a2's one advantage, refusing a malformed body before the visitor allocates,
does not affect any budget: failures are not budgeted.

**Per-string check (W0.2 review input).** The strings the visitor checks
average 5.6–7.0 bytes, with a maximum of 15 (string-mix table below).

- On that mix, `utf8.ValidString(s) && !HasControlByte(s)` is 1.95–2.33 ×
  slower than `codec.ValidString` on both hosts.
- Across a whole a1 decode it adds 3.0–10.0 %: `result.json` goes from 3.22
  to 3.31 µs (M) and from 3.06 to 3.33 µs (L); the 10k flood from 6.437 to
  6.745 ms (M) and from 5.95 to 6.32 ms (L).
- The review's 2.9 × gain was measured on 4 KiB strings, which these bodies
  do not contain.
- **W2.0 should keep `codec.ValidString`.**

**Proposed NF2** (variant a1; the same on (M) and (L)):

- Traversal: 0 allocations for keys and strings without escapes (a no-op
  `ast.Preorder` allocates 0 on the plain fixtures), and 1 per escaped key or
  string. `escaped-names.json` has 6; `escaped-member-names.json` has 12.
- Answers, as exact-size slices: noul 0; choice 1 (probabilities); score 2
  (legend and probabilities; 0 when both are empty).
- Per response: 1 for the entries, plus `wire.Answers`' index map when there
  are more than 8 answers (4 at 16 and at 20 answers).
- Structured legends: the lazy pass (AC-P8 below), plus 1 arena holding
  every copied level. Copying each level on its own costs one allocation per
  level: 10 685 against 686 on the 10k flood.
- Unknown answer types: 0. The body scan: 0.
- `result.json` = 1 + 1 + 2 = **4** on both hosts, which meets the plan's
  target. The plan's ≤ 8 remains the ceiling if W2.0's API wrapper adds
  allocations.

**AC-P2 per-fixture proposal** (a1, exact and identical on both hosts):

| Fixture | Allocations |
| --- | --- |
| `result` | 4 |
| `type-last` | 4 |
| `duplicates` | 17 (fixture extended by 73af053; the structured legend triggers the lazy pass; W0.3-21, W0.3-22) |
| `result-20` | 24 |
| `score-flood-mini` | 21 |
| `escaped-names` | 10 |
| `escaped-member-names` | 30 |
| `structured-legend` | 12 |
| `deviation-lone-surrogate` | 14 |
| `unknown-answer-type` | 1 |
| `parity-big-exp-unknown` | 1 |
| `no-answers` | 0 |
| `structured-legend-flood-1k` | 91 |
| `structured-legend-flood-10k` | 686 |

The "≤ 0.5 × naive" clause waits for the naive decoder (W5).

**AC-P8.** "Members visited" is root members, plus answers members, plus the
flagged answers' members, plus their legend levels: 1 011 on the 1k flood and
10 011 on the 10k flood.

- Lazy-pass allocations are 86 / 681 on both hosts, plus 1 arena.
- The fit gives c₁ = (681 − 86) / (10 011 − 1 011) = 0.066, about 1/15, since
  sonic allocates an object's pairs in chunks, and c₀ ≈ 19.
- Proposal: lazy-pass allocations ≤ 20 + ⌈members / 15⌉ + escaped keys
  iterated + 1.
- Every measured case fits the bound: `structured-legend` 9 (10 members),
  `deviation-lone-surrogate` 9 (11), `escaped-member-names` 14 (12 members,
  3 escaped keys), the 1k flood 87, the 10k flood 682.
- The 10k : 1k ratio is 7.9 for the lazy pass and 7.54 for the whole decode
  (bound 12).
- Time: the 10k : 1k ratio is 9.70–10.57 across both hosts, all three
  variants, and both the benchmark medians and the `TestLinearity` runs
  (bound 15). Linearity table below.

### Rulings on these proposals (lead, R22 to R24)

- AC-P1: W5.2 runs the sequence with a RawJSON state and a boxed-string state
  and asserts call 23 = base + 1. The `*struct` and map kinds are measured
  and recorded per host, not asserted. Pre-growing the scratch in
  power-of-two steps is a W5.3 candidate.
- NF1 for map states: budgeted as 1 + m, the number of maps in the state.
  The SDK documents the cost and recommends structs or RawJSON on hot paths.
- AC-P2: the exact per-fixture numbers above are W2.0's budget
  (`result.json` = 4); W0.6 confirms them.
- R14 and the per-string check: W2.0 uses `codec.ValidString` per string.
  When it fails on valid UTF-8, the SWAR test for any byte below 0x20 runs,
  and only when that finds one does the memoised tracked scan decide.

<a id="w03-tables"></a>

### W0.3 tables

#### S-E1 pooled encode by kind and size (warm pool)


Sources: `s-e1/results/alloc-M.txt` = W0.3-04; `s-e1/results/bench-M.txt` = W0.3-06; `s-e1/results/alloc-L.txt` = W0.3-02; `s-e1/results/bench-L.txt` = W0.3-05.

| kind | size | encoded B | (M) allocs/B | (M) ns/op | (M) cap after / pooled | (L) allocs/B | (L) ns/op | (L) cap after / pooled |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| string | 1KiB | 1024 | 2/32 | 155.6 ns (±1 %) | 4096 / true | 2/32 | 128.7 ns (±0 %) | 4096 / true |
| string | 64KiB | 65536 | 2/32 | 3.75 µs (±1 %) | 67584 / true | 2/32 | 2.56 µs (±0 %) | 65536 / true |
| string | 1MiB | 1048576 | 2/32 | 58.03 µs (±1 %) | 1050624 / true | 2/32 | 47.08 µs (±3 %) | 1048576 / true |
| string | 6MiB | 6291456 | 2/32 | 354.86 µs (±1 %) | 6293504 / true | 2/32 | 1.341 ms (±0 %) | 6291456 / true |
| string | 9MiB | 9437184 | 4/9449504 | 732.38 µs (±1 %) | 9439232 / false | 4/9441312 | 2.459 ms (±3 %) | 9437184 / false |
| boxed | 1KiB | 1024 | 1/16 | 146.0 ns (±2 %) | 4096 / true | 1/16 | 109.9 ns (±0 %) | 4096 / true |
| boxed | 64KiB | 65536 | 1/16 | 3.77 µs (±1 %) | 67584 / true | 1/16 | 2.55 µs (±0 %) | 65536 / true |
| boxed | 1MiB | 1048576 | 1/16 | 57.63 µs (±2 %) | 1050624 / true | 1/16 | 45.79 µs (±1 %) | 1048576 / true |
| boxed | 6MiB | 6291456 | 1/16 | 345.04 µs (±1 %) | 6293504 / true | 1/16 | 858.43 µs (±1 %) | 6291456 / true |
| boxed | 9MiB | 9437184 | 3/9449488 | 664.08 µs (±1 %) | 9439232 / false | 3/9441296 | 2.043 ms (±3 %) | 9437184 / false |
| map | 1KiB | 1024 | 9-9/784-784 | 5.46 µs (±5 %) | 4096 / true | 9-9/784-784 | 3.29 µs (±1 %) | 4096 / true |
| map | 64KiB | 66018 | 471-471/45136-45136 | 356.35 µs (±1 %) | 70921 / true | 471-471/45136-45136 | 223.81 µs (±1 %) | 73728 / true |
| map | 1MiB | 1074672 | 7503-7503/720208-720208 | 5.704 ms (±4 %) | 1540268 / true | 7503-7503/720208-720208 | 4.028 ms (±0 %) | 1114112 / true |
| map | 6MiB | 6515697 | 45010-45010/4320880-4320880 | 40.429 ms (±4 %) | 8054798 / true | 45010-45010/4320880-4320880 | 25.663 ms (±1 %) | 6750208 / true |
| map | 9MiB | 9801281 | 67535-67536/36970736-47980784 | 65.706 ms (±3 %) | 13812905 / false | 67546-67546/58736496-58736496 | 57.036 ms (±1 %) | 10559488 / false |
| map-flat | 1KiB | 1055 | 2/112 | 1.82 µs (±1 %) | 4096 / true | 2/112 | 815.8 ns (±0 %) | 4096 / true |
| map-flat | 64KiB | 65535 | 2/112 | 104.26 µs (±1 %) | 70752 / true | 2/112 | 39.06 µs (±0 %) | 73728 / true |
| map-flat | 1MiB | 1048545 | 2/112 | 1.628 ms (±1 %) | 1209077 / true | 2/112 | 630.00 µs (±0 %) | 1114112 / true |
| map-flat | 6MiB | 6291327 | 22/27597808 | 10.843 ms (±1 %) | 9181884 / false | 2/112 | 4.248 ms (±1 %) | 6750208 / true |
| map-flat | 9MiB | 9437021 | 23/41376752 | 17.019 ms (±3 %) | 13772832 / false | 34/52255344 | 14.691 ms (±2 %) | 10559488 / false |
| struct-ptr | 1KiB | 1066 | 1/16 | 2.78 µs (±2 %) | 4096 / true | 1/16 | 767.1 ns (±0 %) | 4096 / true |
| struct-ptr | 64KiB | 65520 | 1/16 | 159.74 µs (±1 %) | 83304 / true | 1/16 | 40.94 µs (±0 %) | 73728 / true |
| struct-ptr | 1MiB | 1056133 | 1/16 | 2.575 ms (±1 %) | 1187840 / true | 1/16 | 653.44 µs (±0 %) | 1114112 / true |
| struct-ptr | 6MiB | 6379778 | 22/27601168 | 16.464 ms (±1 %) | 9020409 / false | 1/16 | 3.988 ms (±0 %) | 6750208 / true |
| struct-ptr | 9MiB | 9575142 | 23/41134352 | 24.595 ms (±3 %) | 13530646 / false | 33/52255248 | 12.693 ms (±1 %) | 10559488 / false |
| raw-verbatim | 1KiB | 1024 | 0/0 | 22.7 ns (±1 %) | 4096 / true | 0/0 | 35.8 ns (±1 %) | 4096 / true |
| raw-verbatim | 64KiB | 66018 | 0/0 | 831.5 ns (±11 %) | 73728 / true | 0/0 | 1.68 µs (±0 %) | 73728 / true |
| raw-verbatim | 1MiB | 1074672 | 0/0 | 13.69 µs (±1 %) | 1081344 / true | 0/0 | 33.06 µs (±4 %) | 1081344 / true |
| raw-verbatim | 6MiB | 6515697 | 0/0 | 84.74 µs (±5 %) | 6520832 / true | 0/0 | 450.95 µs (±1 %) | 6520832 / true |
| raw-verbatim | 9MiB | 9801281 | 2/9809920 | 490.15 µs (±2 %) | 9805824 / false | 2/9809920 | 1.063 ms (±1 %) | 9805824 / false |
| raw-encodeinto | 1KiB | 1024 | 1/16 | 927.1 ns (±1 %) | 4096 / true | 1/16 | 1.02 µs (±1 %) | 4096 / true |
| raw-encodeinto | 64KiB | 66018 | 1/16 | 65.25 µs (±1 %) | 73728 / true | 1/16 | 83.96 µs (±0 %) | 73728 / true |
| raw-encodeinto | 1MiB | 1074672 | 1/16 | 1.069 ms (±1 %) | 1081344 / true | 1/16 | 1.369 ms (±0 %) | 1081344 / true |
| raw-encodeinto | 6MiB | 6515697 | 1/16 | 6.430 ms (±1 %) | 6520832 / true | 1/16 | 8.421 ms (±0 %) | 6520832 / true |
| raw-encodeinto | 9MiB | 9801281 | 3/9809936 | 9.953 ms (±1 %) | 9805824 / false | 3/9809936 | 13.335 ms (±1 %) | 9805824 / false |

#### S-E1 growth from a 4 KiB scratch (g, incl. E_sonic and B)


Sources: `s-e1/results/alloc-M.txt` = W0.3-04; `s-e1/results/alloc-L.txt` = W0.3-02.

| kind | size | (M) g allocs/B | (M) cap after / pooled | (L) g allocs/B | (L) cap after / pooled |
| --- | --- | --- | --- | --- | --- |
| string | 64KiB | 3/73760 | 67584 / true | 3/65568 | 65536 / true |
| string | 1MiB | 3/1056800 | 1050624 / true | 3/1048608 | 1048576 / true |
| string | 6MiB | 3/6299680 | 6293504 / true | 3/6291488 | 6291456 / true |
| string | 9MiB | 3/9445408 | 9439232 / false | 3/9437216 | 9437184 / false |
| boxed | 64KiB | 2/73744 | 67584 / true | 2/65552 | 65536 / true |
| boxed | 1MiB | 2/1056784 | 1050624 / true | 2/1048592 | 1048576 / true |
| boxed | 6MiB | 2/6299664 | 6293504 / true | 2/6291472 | 6291456 / true |
| boxed | 9MiB | 2/9445392 | 9439232 / false | 2/9437200 | 9437184 / false |
| map | 64KiB | 478-479/252880-331088 | 71102 / true | 481-481/318032-318032 | 73728 / true |
| map | 1MiB | 7517-7518/4122832-5171408 | 1207007 / true | 7524-7524/5949264-5949264 | 1114112 / true |
| map | 6MiB | 45029-45029/25311088-32070128 | 6801579 / true | 45039-45039/37566576-37566576 | 6750208 / true |
| map | 9MiB | 67534-67535/41234672-48819824 | 11477896 / false | 67545-67545/58732400-58732400 | 10559488 / false |
| map-flat | 64KiB | 9/207856 | 70752 / true | 12/273008 | 73728 / true |
| map-flat | 1MiB | 16/3648496 | 1209077 / true | 23/5229168 | 1114112 / true |
| map-flat | 6MiB | 21/27593712 | 9181884 / false | 31/33245808 | 6750208 / true |
| map-flat | 9MiB | 22/41372656 | 13772832 / false | 33/52251248 | 10559488 / false |
| struct-ptr | 64KiB | 9/260368 | 83304 / true | 11/272912 | 73728 / true |
| struct-ptr | 1MiB | 16/4069648 | 1187840 / true | 22/5229072 | 1114112 / true |
| struct-ptr | 6MiB | 21/27597072 | 9020409 / false | 30/33245712 | 6750208 / true |
| struct-ptr | 9MiB | 22/41130256 | 13530646 / false | 32/52251152 | 10559488 / false |
| raw-verbatim | 64KiB | 1/73728 | 73728 / true | 1/73728 | 73728 / true |
| raw-verbatim | 1MiB | 1/1081344 | 1081344 / true | 1/1081344 | 1081344 / true |
| raw-verbatim | 6MiB | 1/6520832 | 6520832 / true | 1/6520832 | 6520832 / true |
| raw-verbatim | 9MiB | 1/9805824 | 9805824 / false | 1/9805824 | 9805824 / false |
| raw-encodeinto | 64KiB | 2/73744 | 73728 / true | 2/73744 | 73728 / true |
| raw-encodeinto | 1MiB | 2/1081360 | 1081344 / true | 2/1081360 | 1081344 / true |
| raw-encodeinto | 6MiB | 2/6520848 | 6520832 / true | 2/6520848 | 6520832 / true |
| raw-encodeinto | 9MiB | 2/9805840 | 9805824 / false | 2/9805840 | 9805824 / false |

#### S-E1 AC-P1 sequence, mallocs per call 1..32 (bytes in the raw files)


(M), 2305d02, as is (one-malloc drop path landed) (`alloc-M.txt` = W0.3-04):

| kind | calls 1..32 (mallocs) |
| --- | --- |
| string | 9 2 2 2 2 2 2 2 2 2 3 2 2 2 2 2 2 2 2 2 2 3 3 2 2 2 2 2 2 2 2 2 |
| boxed | 8 1 1 1 1 1 1 1 1 1 2 1 1 1 1 1 1 1 1 1 1 2 2 1 1 1 1 1 1 1 1 1 |
| map | 22-22 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 45029-45030 9-10 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 67515-67534 10-10 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 |
| map-flat | 12 2 2 2 2 2 2 2 2 2 21 3 2 2 2 2 2 2 2 2 2 22 3 2 2 2 2 2 2 2 2 2 |
| struct-ptr | 8 1 1 1 1 1 1 1 1 1 21 2 1 1 1 1 1 1 1 1 1 22 2 1 1 1 1 1 1 1 1 1 |
| raw-verbatim | 4 0 0 0 0 0 0 0 0 0 1 0 0 0 0 0 0 0 0 0 0 1 1 0 0 0 0 0 0 0 0 0 |
| raw-encodeinto | 11 1 1 1 1 1 1 1 1 1 2 1 1 1 1 1 1 1 1 1 1 2 2 1 1 1 1 1 1 1 1 1 |

(L), 2305d02, as is (one-malloc drop path landed) (`alloc-L.txt` = W0.3-02):

| kind | calls 1..32 (mallocs) |
| --- | --- |
| string | 9 2 2 2 2 2 2 2 2 2 3 2 2 2 2 2 2 2 2 2 2 3 3 2 2 2 2 2 2 2 2 2 |
| boxed | 8 1 1 1 1 1 1 1 1 1 2 1 1 1 1 1 1 1 1 1 1 2 2 1 1 1 1 1 1 1 1 1 |
| map | 22-22 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 45039-45039 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 67516-67516 10-10 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 |
| map-flat | 12 2 2 2 2 2 2 2 2 2 31 2 2 2 2 2 2 2 2 2 2 4 3 2 2 2 2 2 2 2 2 2 |
| struct-ptr | 8 1 1 1 1 1 1 1 1 1 30 1 1 1 1 1 1 1 1 1 1 3 2 1 1 1 1 1 1 1 1 1 |
| raw-verbatim | 4 0 0 0 0 0 0 0 0 0 1 0 0 0 0 0 0 0 0 0 0 1 1 0 0 0 0 0 0 0 0 0 |
| raw-encodeinto | 11 1 1 1 1 1 1 1 1 1 2 1 1 1 1 1 1 1 1 1 1 2 2 1 1 1 1 1 1 1 1 1 |

(M), 76ffd03, before the drop-path fix (`alloc-M-base76ffd03.txt` = W0.3-03):

| kind | calls 1..32 (mallocs) |
| --- | --- |
| string | 9 2 2 2 2 2 2 2 2 2 3 2 2 2 2 2 2 2 2 2 2 3 4 2 2 2 2 2 2 2 2 2 |
| boxed | 8 1 1 1 1 1 1 1 1 1 2 1 1 1 1 1 1 1 1 1 1 2 3 1 1 1 1 1 1 1 1 1 |
| map | 22-22 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 45029-45030 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 67515-67515 11-11 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 |
| map-flat | 12 2 2 2 2 2 2 2 2 2 21 4 2 2 2 2 2 2 2 2 2 22 4 2 2 2 2 2 2 2 2 2 |
| struct-ptr | 8 1 1 1 1 1 1 1 1 1 21 3 1 1 1 1 1 1 1 1 1 22 3 1 1 1 1 1 1 1 1 1 |
| raw-verbatim | 4 0 0 0 0 0 0 0 0 0 1 0 0 0 0 0 0 0 0 0 0 1 2 0 0 0 0 0 0 0 0 0 |
| raw-encodeinto | 11 1 1 1 1 1 1 1 1 1 2 1 1 1 1 1 1 1 1 1 1 2 3 1 1 1 1 1 1 1 1 1 |

(L), 76ffd03, before the drop-path fix (`alloc-L-base76ffd03.txt` = W0.3-01):

| kind | calls 1..32 (mallocs) |
| --- | --- |
| string | 9 2 2 2 2 2 2 2 2 2 3 2 2 2 2 2 2 2 2 2 2 3 4 2 2 2 2 2 2 2 2 2 |
| boxed | 8 1 1 1 1 1 1 1 1 1 2 1 1 1 1 1 1 1 1 1 1 2 3 1 1 1 1 1 1 1 1 1 |
| map | 22-22 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 45039-45039 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 67516-67516 11-11 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 |
| map-flat | 12 2 2 2 2 2 2 2 2 2 31 2 2 2 2 2 2 2 2 2 2 4 4 2 2 2 2 2 2 2 2 2 |
| struct-ptr | 8 1 1 1 1 1 1 1 1 1 30 1 1 1 1 1 1 1 1 1 1 3 3 1 1 1 1 1 1 1 1 1 |
| raw-verbatim | 4 0 0 0 0 0 0 0 0 0 1 0 0 0 0 0 0 0 0 0 0 1 2 0 0 0 0 0 0 0 0 0 |
| raw-encodeinto | 11 1 1 1 1 1 1 1 1 1 2 1 1 1 1 1 1 1 1 1 1 2 3 1 1 1 1 1 1 1 1 1 |

(M), 76ffd03 with the local lazy-buffer experiment (lazybuf-experiment.diff) (`sequence-lazybuf-M-base76ffd03.txt` = W0.3-10):

| kind | calls 1..32 (mallocs) |
| --- | --- |
| string | 9 2 2 2 2 2 2 2 2 2 3 2 2 2 2 2 2 2 2 2 2 3 3 2 2 2 2 2 2 2 2 2 |
| boxed | 8 1 1 1 1 1 1 1 1 1 2 1 1 1 1 1 1 1 1 1 1 2 2 1 1 1 1 1 1 1 1 1 |
| map | 22-22 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 45029-45030 9-10 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 67515-67534 10-10 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 |
| map-flat | 12 2 2 2 2 2 2 2 2 2 21 3 2 2 2 2 2 2 2 2 2 22 3 2 2 2 2 2 2 2 2 2 |
| struct-ptr | 8 1 1 1 1 1 1 1 1 1 21 2 1 1 1 1 1 1 1 1 1 22 2 1 1 1 1 1 1 1 1 1 |
| raw-verbatim | 4 0 0 0 0 0 0 0 0 0 1 0 0 0 0 0 0 0 0 0 0 1 1 0 0 0 0 0 0 0 0 0 |
| raw-encodeinto | 11 1 1 1 1 1 1 1 1 1 2 1 1 1 1 1 1 1 1 1 1 2 2 1 1 1 1 1 1 1 1 1 |

(L), 76ffd03 with the local lazy-buffer experiment (lazybuf-experiment.diff) (`sequence-lazybuf-L-base76ffd03.txt` = W0.3-09):

| kind | calls 1..32 (mallocs) |
| --- | --- |
| string | 9 2 2 2 2 2 2 2 2 2 3 2 2 2 2 2 2 2 2 2 2 3 3 2 2 2 2 2 2 2 2 2 |
| boxed | 8 1 1 1 1 1 1 1 1 1 2 1 1 1 1 1 1 1 1 1 1 2 2 1 1 1 1 1 1 1 1 1 |
| map | 22-22 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 45039-45039 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 67516-67516 10-10 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 9-9 |
| map-flat | 12 2 2 2 2 2 2 2 2 2 31 2 2 2 2 2 2 2 2 2 2 4 3 2 2 2 2 2 2 2 2 2 |
| struct-ptr | 8 1 1 1 1 1 1 1 1 1 30 1 1 1 1 1 1 1 1 1 1 3 2 1 1 1 1 1 1 1 1 1 |
| raw-verbatim | 4 0 0 0 0 0 0 0 0 0 1 0 0 0 0 0 0 0 0 0 0 1 1 0 0 0 0 0 0 0 0 0 |
| raw-encodeinto | 11 1 1 1 1 1 1 1 1 1 2 1 1 1 1 1 1 1 1 1 1 2 2 1 1 1 1 1 1 1 1 1 |

#### S-D1 per variant and fixture


Sources: `s-d1/results/alloc-M.txt` = W0.3-12; `s-d1/results/bench-M.txt` = W0.3-14; `s-d1/results/alloc-L.txt` = W0.3-11; `s-d1/results/bench-L.txt` = W0.3-13; `s-d1/results/alloc-M-base31f2986.txt` = W0.3-22; `s-d1/results/alloc-L-base31f2986.txt` = W0.3-21.

| variant | fixture | body B | (M) allocs/B | (M) ns/op median (±half-spread) | (L) allocs/B | (L) ns/op median (±half-spread) | lazy allocs | members | notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| a1 | deviation-lone-surrogate | 219 | 14/4568 | 4.46 µs (±3 %) | 14/4568 | 4.73 µs (±0 %) | 8 | 11 | per-level copy: 14/4568 |
| a1 | duplicates | 825 | 17/5336 | – | 17/5336 | – | 9 | 18 | fixture extended by 73af053 (W0.3-22, W0.3-21); the structured legend triggers the lazy pass; per-level copy: 17/5336 |
| a1 | escaped-member-names | 425 | 30/5144 | 6.51 µs (±1 %) | 30/5144 | 6.86 µs (±0 %) | 13 | 12 | per-level copy: 30/5144 |
| a1 | escaped-names | 481 | 10/1224 | 5.24 µs (±1 %) | 10/1224 | 5.33 µs (±0 %) | 0 | 0 | body scan ran |
| a1 | no-answers | 67 | 0/0 | 497.5 ns (±1 %) | 0/0 | 404.3 ns (±0 %) | 0 | 0 |  |
| a1 | parity-big-exp-unknown | 135 | 1/144 | 1.18 µs (±1 %) | 1/144 | 1.09 µs (±0 %) | 0 | 0 |  |
| a1 | result | 364 | 4/688 | 3.22 µs (±2 %) | 4/688 | 3.09 µs (±0 %) | 0 | 0 |  |
| a1 | result-20 | 2253 | 24/6008 | 20.54 µs (±1 %) | 24/6008 | 20.03 µs (±0 %) | 0 | 0 |  |
| a1 | score-flood-mini | 1495 | 21/4184 | 13.92 µs (±1 %) | 21/4184 | 13.21 µs (±0 %) | 0 | 0 |  |
| a1 | structured-legend | 217 | 12/4528 | 3.83 µs (±1 %) | 12/4528 | 4.33 µs (±0 %) | 8 | 10 | per-level copy: 12/4528 |
| a1 | structured-legend-flood-10k | 616421 | 686/2005944 | 6.748 ms (±2 %) | 686/2005944 | 5.945 ms (±0 %) | 681 | 10011 | per-level copy: 10685/2083736 |
| a1 | structured-legend-flood-1k | 57418 | 91/213896 | 668.98 µs (±1 %) | 91/213896 | 609.55 µs (±0 %) | 86 | 1011 | per-level copy: 1090/220136 |
| a1 | type-last | 364 | 4/688 | 3.22 µs (±2 %) | 4/688 | 3.11 µs (±0 %) | 0 | 0 |  |
| a1 | unknown-answer-type | 145 | 1/144 | 1.33 µs (±1 %) | 1/144 | 1.17 µs (±0 %) | 0 | 0 |  |
| a2 | deviation-lone-surrogate | 219 | 14/4568 | 4.33 µs (±2 %) | 14/4568 | 4.73 µs (±0 %) | 8 | 11 | per-level copy: 14/4568 |
| a2 | duplicates | 825 | 17/5336 | – | 17/5336 | – | 9 | 18 | fixture extended by 73af053 (W0.3-22, W0.3-21); the structured legend triggers the lazy pass; per-level copy: 17/5336 |
| a2 | escaped-member-names | 425 | 30/5144 | 6.65 µs (±2 %) | 30/5144 | 6.90 µs (±0 %) | 13 | 12 | per-level copy: 30/5144 |
| a2 | escaped-names | 481 | 10/1224 | 5.21 µs (±1 %) | 10/1224 | 5.28 µs (±0 %) | 0 | 0 | body scan ran |
| a2 | no-answers | 67 | 0/0 | 503.1 ns (±2 %) | 0/0 | 395.7 ns (±0 %) | 0 | 0 |  |
| a2 | parity-big-exp-unknown | 135 | 1/144 | 1.20 µs (±1 %) | 1/144 | 1.12 µs (±0 %) | 0 | 0 |  |
| a2 | result | 364 | 4/688 | 3.20 µs (±0 %) | 4/688 | 3.11 µs (±0 %) | 0 | 0 |  |
| a2 | result-20 | 2253 | 24/6008 | 20.42 µs (±1 %) | 24/6008 | 19.88 µs (±0 %) | 0 | 0 |  |
| a2 | score-flood-mini | 1495 | 21/4184 | 13.40 µs (±0 %) | 21/4184 | 12.99 µs (±0 %) | 0 | 0 |  |
| a2 | structured-legend | 217 | 12/4528 | 3.88 µs (±2 %) | 12/4528 | 4.33 µs (±0 %) | 8 | 10 | per-level copy: 12/4528 |
| a2 | structured-legend-flood-10k | 616421 | 686/2005944 | 6.562 ms (±1 %) | 686/2005944 | 5.932 ms (±0 %) | 681 | 10011 | per-level copy: 10685/2083736 |
| a2 | structured-legend-flood-1k | 57418 | 91/213896 | 670.29 µs (±1 %) | 91/213896 | 596.06 µs (±0 %) | 86 | 1011 | per-level copy: 1090/220136 |
| a2 | type-last | 364 | 4/688 | 3.25 µs (±2 %) | 4/688 | 3.12 µs (±0 %) | 0 | 0 |  |
| a2 | unknown-answer-type | 145 | 1/144 | 1.29 µs (±1 %) | 1/144 | 1.21 µs (±0 %) | 0 | 0 |  |
| b | deviation-lone-surrogate | 219 | 18/3848 | 3.76 µs (±2 %) | 14/3576 | 4.26 µs (±0 %) | 7 | 8 | per-level copy: 18/3848 |
| b | duplicates | 825 | 23/5232 | – | 16/4328 | – | 7 | 12 | fixture extended by 73af053 (W0.3-22, W0.3-21); the structured legend triggers the lazy pass; per-level copy: 23/5232 |
| b | escaped-member-names | 425 | 31/4568 | 6.12 µs (±7 %) | 27/4120 | 6.32 µs (±0 %) | 9 | 9 | per-level copy: 31/4568 |
| b | escaped-names | 481 | 15/1800 | 5.38 µs (±3 %) | 11/1256 | 5.51 µs (±0 %) | 0 | 0 | body scan ran |
| b | no-answers | 67 | 4/160 | 649.8 ns (±3 %) | 1/32 | 550.0 ns (±0 %) | 0 | 0 |  |
| b | parity-big-exp-unknown | 135 | – | – | 2/176 | 1.27 µs (±0 %) | 0 | 0 |  |
| b | result | 364 | 9/1136 | 3.35 µs (±2 %) | 5/720 | 3.31 µs (±0 %) | 0 | 0 |  |
| b | result-20 | 2253 | 29/8472 | 20.50 µs (±2 %) | 25/6040 | 20.09 µs (±0 %) | 0 | 0 |  |
| b | score-flood-mini | 1495 | 26/5880 | 13.68 µs (±1 %) | 22/4216 | 13.37 µs (±0 %) | 0 | 0 |  |
| b | structured-legend | 217 | 16/3808 | 3.51 µs (±2 %) | 12/3536 | 3.89 µs (±0 %) | 7 | 7 | per-level copy: 16/3808 |
| b | structured-legend-flood-10k | 616421 | 691/4765784 | 6.532 ms (±1 %) | 686/2004952 | 5.898 ms (±0 %) | 680 | 10008 | per-level copy: 10690/4843576 |
| b | structured-legend-flood-1k | 57418 | 95/270376 | 655.43 µs (±3 %) | 91/212904 | 595.33 µs (±0 %) | 85 | 1008 | per-level copy: 1094/276616 |
| b | type-last | 364 | 9/1136 | 3.33 µs (±1 %) | 5/720 | 3.31 µs (±0 %) | 0 | 0 |  |
| b | unknown-answer-type | 145 | 6/384 | 1.49 µs (±2 %) | 2/176 | 1.38 µs (±0 %) | 0 | 0 |  |

#### S-D1 primitives, control scans and per-string checks (ns/op, median of 5)


Sources: `s-d1/results/bench-M.txt` = W0.3-14; `s-d1/results/bench-L.txt` = W0.3-13.

| benchmark | (M) ns/op | (L) ns/op | (M) allocs | (L) allocs |
| --- | --- | --- | --- | --- |
| ControlScan/escaped-names/rule | 35.4 ns (±0 %) | 56.6 ns (±0 %) | 0 | 0 |
| ControlScan/escaped-names/swar | 34.7 ns (±1 %) | 54.3 ns (±0 %) | 0 | 0 |
| ControlScan/escaped-names/tracked | 470.0 ns (±1 %) | 694.5 ns (±3 %) | 0 | 0 |
| ControlScan/escaped-names/utf8 | 13.4 ns (±1 %) | 28.3 ns (±1 %) | 0 | 0 |
| ControlScan/result-20/rule | 164.5 ns (±1 %) | 252.2 ns (±0 %) | 0 | 0 |
| ControlScan/result-20/swar | 161.5 ns (±0 %) | 250.8 ns (±0 %) | 0 | 0 |
| ControlScan/result-20/tracked | 2.21 µs (±1 %) | 3.14 µs (±2 %) | 0 | 0 |
| ControlScan/result-20/utf8 | 34.0 ns (±0 %) | 61.5 ns (±0 %) | 0 | 0 |
| ControlScan/result/rule | 27.7 ns (±0 %) | 46.9 ns (±3 %) | 0 | 0 |
| ControlScan/result/swar | 27.6 ns (±1 %) | 44.5 ns (±0 %) | 0 | 0 |
| ControlScan/result/tracked | 352.6 ns (±0 %) | 521.0 ns (±4 %) | 0 | 0 |
| ControlScan/result/utf8 | 12.0 ns (±0 %) | 20.3 ns (±0 %) | 0 | 0 |
| ControlScan/structured-legend-flood-10k/rule | 42.08 µs (±1 %) | 64.78 µs (±0 %) | 0 | 0 |
| ControlScan/structured-legend-flood-10k/swar | 41.94 µs (±1 %) | 64.97 µs (±0 %) | 0 | 0 |
| ControlScan/structured-legend-flood-10k/tracked | 588.52 µs (±1 %) | 833.83 µs (±3 %) | 0 | 0 |
| ControlScan/structured-legend-flood-10k/utf8 | 7.66 µs (±2 %) | 17.76 µs (±0 %) | 0 | 0 |
| ControlScan/structured-legend-flood-1k/rule | 3.98 µs (±1 %) | 6.03 µs (±0 %) | 0 | 0 |
| ControlScan/structured-legend-flood-1k/swar | 3.98 µs (±0 %) | 6.04 µs (±0 %) | 0 | 0 |
| ControlScan/structured-legend-flood-1k/tracked | 53.98 µs (±1 %) | 74.69 µs (±2 %) | 0 | 0 |
| ControlScan/structured-legend-flood-1k/utf8 | 724.1 ns (±2 %) | 1.68 µs (±0 %) | 0 | 0 |
| DecodeCheck/codec/escaped-member-names | 6.60 µs (±3 %) | 6.83 µs (±0 %) | 30 | 30 |
| DecodeCheck/codec/result | 3.22 µs (±1 %) | 3.06 µs (±0 %) | 4 | 4 |
| DecodeCheck/codec/result-20 | 20.33 µs (±1 %) | 19.74 µs (±0 %) | 24 | 24 |
| DecodeCheck/codec/structured-legend-flood-10k | 6.437 ms (±1 %) | 5.946 ms (±0 %) | 687 | 687 |
| DecodeCheck/codec/structured-legend-flood-1k | 648.79 µs (±1 %) | 600.19 µs (±0 %) | 91 | 91 |
| DecodeCheck/utf8+swar/escaped-member-names | 7.02 µs (±4 %) | 7.11 µs (±0 %) | 30 | 30 |
| DecodeCheck/utf8+swar/result | 3.31 µs (±2 %) | 3.33 µs (±0 %) | 4 | 4 |
| DecodeCheck/utf8+swar/result-20 | 21.07 µs (±1 %) | 21.34 µs (±0 %) | 24 | 24 |
| DecodeCheck/utf8+swar/structured-legend-flood-10k | 6.745 ms (±1 %) | 6.320 ms (±0 %) | 687 | 687 |
| DecodeCheck/utf8+swar/structured-legend-flood-1k | 713.75 µs (±4 %) | 640.62 µs (±0 %) | 91 | 91 |
| Primitive/escaped-names/preorder-noop | 2.82 µs (±1 %) | 1.41 µs (±0 %) | 6 | 6 |
| Primitive/escaped-names/skip | 703.8 ns (±1 %) | 807.8 ns (±0 %) | 0 | 0 |
| Primitive/escaped-names/unmarshal-rawmap | 853.8 ns (±2 %) | 1.04 µs (±0 %) | 8 | 4 |
| Primitive/escaped-names/validstring | 722.1 ns (±1 %) | 799.2 ns (±0 %) | 0 | 0 |
| Primitive/result-20/preorder-noop | 11.65 µs (±1 %) | 5.45 µs (±1 %) | 0 | 0 |
| Primitive/result-20/skip | 2.93 µs (±1 %) | 3.32 µs (±0 %) | 0 | 0 |
| Primitive/result-20/unmarshal-rawmap | 2.81 µs (±2 %) | 3.58 µs (±1 %) | 8 | 4 |
| Primitive/result-20/validstring | 2.96 µs (±2 %) | 3.27 µs (±0 %) | 0 | 0 |
| Primitive/result/preorder-noop | 1.95 µs (±1 %) | 896.2 ns (±0 %) | 0 | 0 |
| Primitive/result/skip | 457.9 ns (±1 %) | 516.1 ns (±0 %) | 0 | 0 |
| Primitive/result/unmarshal-rawmap | 763.7 ns (±2 %) | 766.5 ns (±0 %) | 8 | 4 |
| Primitive/result/validstring | 482.0 ns (±1 %) | 504.4 ns (±0 %) | 0 | 0 |
| Primitive/structured-legend-flood-10k/preorder-noop | 2.863 ms (±0 %) | 1.332 ms (±0 %) | 0 | 0 |
| Primitive/structured-legend-flood-10k/skip | 551.59 µs (±2 %) | 720.02 µs (±1 %) | 0 | 0 |
| Primitive/structured-legend-flood-10k/unmarshal-rawmap | 615.28 µs (±1 %) | 721.10 µs (±0 %) | 9 | 4 |
| Primitive/structured-legend-flood-10k/validstring | 543.10 µs (±1 %) | 677.58 µs (±0 %) | 0 | 0 |
| Primitive/structured-legend-flood-1k/preorder-noop | 289.44 µs (±0 %) | 130.95 µs (±0 %) | 0 | 0 |
| Primitive/structured-legend-flood-1k/skip | 54.16 µs (±2 %) | 71.19 µs (±1 %) | 0 | 0 |
| Primitive/structured-legend-flood-1k/unmarshal-rawmap | 58.00 µs (±1 %) | 73.31 µs (±1 %) | 8 | 4 |
| Primitive/structured-legend-flood-1k/validstring | 56.96 µs (±1 %) | 68.58 µs (±0 %) | 0 | 0 |
| StringCheck/escaped-member-names/codec | 152.9 ns (±1 %) | 265.4 ns (±0 %) | 0 | 0 |
| StringCheck/escaped-member-names/utf8+swar | 330.9 ns (±1 %) | 531.7 ns (±2 %) | 0 | 0 |
| StringCheck/result-20/codec | 892.2 ns (±2 %) | 1.43 µs (±0 %) | 0 | 0 |
| StringCheck/result-20/utf8+swar | 2.03 µs (±2 %) | 3.00 µs (±1 %) | 0 | 0 |
| StringCheck/result/codec | 147.7 ns (±0 %) | 253.5 ns (±0 %) | 0 | 0 |
| StringCheck/result/utf8+swar | 343.4 ns (±1 %) | 527.0 ns (±1 %) | 0 | 0 |
| StringCheck/structured-legend-flood-10k/codec | 237.31 µs (±0 %) | 386.62 µs (±0 %) | 0 | 0 |
| StringCheck/structured-legend-flood-10k/utf8+swar | 474.17 µs (±1 %) | 752.70 µs (±2 %) | 0 | 0 |
| StringCheck/structured-legend-flood-1k/codec | 22.23 µs (±1 %) | 35.08 µs (±0 %) | 0 | 0 |
| StringCheck/structured-legend-flood-1k/utf8+swar | 49.24 µs (±1 %) | 74.16 µs (±1 %) | 0 | 0 |

#### S-D1 linearity (AC-P8): structured-legend floods, 10³ vs 10⁴ levels


Sources: `s-d1/results/alloc-M.txt` = W0.3-12; `s-d1/results/bench-M.txt` = W0.3-14; `s-d1/results/linearity-M.txt` = W0.3-18; `s-d1/results/alloc-L.txt` = W0.3-11; `s-d1/results/bench-L.txt` = W0.3-13; `s-d1/results/linearity-L.txt` = W0.3-17.

| host | variant | allocs 1k → 10k (ratio) | lazy-pass allocs 1k → 10k | members 1k → 10k | bench median 1k → 10k (ratio) | TestLinearity ratios, 5 runs (min of 21 each) |
| --- | --- | --- | --- | --- | --- | --- |
| (M) | a1 | 91 → 686 (7.54) | 86 → 681 | 1011 → 10011 | 668.98 µs → 6.748 ms (10.09) | 9.70, 10.07, 10.00, 9.79, 10.02 |
| (M) | a2 | 91 → 686 (7.54) | 86 → 681 | 1011 → 10011 | 670.29 µs → 6.562 ms (9.79) | 9.96, 10.15, 10.08, 9.82, 10.05 |
| (M) | b | 95 → 691 (7.27) | 85 → 680 | 1008 → 10008 | 655.43 µs → 6.532 ms (9.97) | 10.02, 10.23, 10.33, 10.27, 10.57 |
| (L) | a1 | 91 → 686 (7.54) | 86 → 681 | 1011 → 10011 | 609.55 µs → 5.945 ms (9.75) | 9.98, 10.26, 10.17, 9.99, 10.11 |
| (L) | a2 | 91 → 686 (7.54) | 86 → 681 | 1011 → 10011 | 596.06 µs → 5.932 ms (9.95) | 10.19, 10.31, 9.92, 10.11, 10.17 |
| (L) | b | 91 → 686 (7.54) | 85 → 680 | 1008 → 10008 | 595.33 µs → 5.898 ms (9.91) | 9.83, 9.89, 10.20, 10.11, 10.03 |

#### Strings the visitor checks, per body


Sources: `s-d1/results/gate-M.txt` = W0.3-16.

| body | strings | bytes | mean | p50 | p90 | max |
| --- | --- | --- | --- | --- | --- | --- |
| result.json | 35 | 200 | 5.7 | 5 | 12 | 13 |
| result-20.json | 212 | 1189 | 5.6 | 5 | 10 | 14 |
| escaped-member-names.json | 32 | 223 | 7.0 | 6 | 13 | 15 |
| structured-legend-flood-1k.json | 5026 | 29787 | 5.9 | 5 | 9 | 13 |
| structured-legend-flood-10k.json | 50026 | 331287 | 6.6 | 5 | 10 | 13 |

## W0.5: S-C1 (whole call) and the AC-P5 memory probe

Spike code: `_spikes/s-c1/` (throwaway, outside `./...`; run it by path,
`go test ./_spikes/s-c1/`). `call.go` is a prototype of the future
`Client.SystemOne` built only from what exists: the body is encoded into a
pooled `codec.Body` (plan 6.1.2, sonic's `encoder.EncodeInto` for the state);
the request carries a `codec.BodyReader`, `GetBody`, `ContentLength`, the
header template (plan 6.3) and a per-attempt `context.WithTimeout`; it goes
through `testsupport.Recorder`; the body is read under the NF5 rules; the
S-D1 winner a1 (`_spikes/s-d1`) decodes it into `wire.Answers`. Each stage is
a method, so a test measures it alone. `naive.go` is the AC-P6 comparator:
`encoding/json` Marshal of the same body, `http.NewRequestWithContext` over a
`bytes.Reader`, `Header.Set` of the same seven headers, `io.ReadAll`,
`encoding/json` Unmarshal into `map[string]any`, on the same Recorder, with
the same per-attempt deadline, calling `RoundTrip` directly as the prototype
does. `_spikes/` is outside the seam test, so the comparator may import
`encoding/json` (the file says so).

Rows W0.5-01 to W0.5-08. Commands use `R=_spikes/s-c1/run.sh`,
`O=_spikes/s-c1/results`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/c8084031-5323-4873-8c36-a19f65c9e6ff/scratchpad`;
on (L) the worktree was copied with the §11 tar pipe to
`/tmp/ts-spike/src-w0.5/wt-w0.5` and run with `PATH=/tmp/ts-spike/go/bin:$PATH
GOPATH=/tmp/ts-spike/gopath GOMODCACHE=/tmp/ts-spike/modcache
GOCACHE=/tmp/ts-spike/gocache` and no `GOEXPERIMENT`. `run.sh` takes the
shared lock with `flock(1)` for every run and writes the header (date, load
from `uptime` and from the kernel: `sysctl -n vm.loadavg` on (M),
`/proc/loadavg` on (L), the lock waits, the base, `go version`, ToolTags) and
the footer (exit status, date, load) from the same shell. Base of every row:
`7b55fe7` plus the `_spikes/s-c1` sources committed with these results
(`cat _spikes/s-c1/*.go _spikes/s-c1/run.sh | shasum -a 256` starts
`46a5e88ba297945d`, printed in each header). The branch was then rebased
onto `cc1524a` (P0-fix-2), which changes none of the files these runs
execute (`internal/testsupport/{recorder,alloc,fixtures}.go`, the non-test
files of `internal/codec` and `internal/wire`, `_spikes/s-d1`,
`testdata/result.json`, `testdata/result-20.json`); the rows were not
repeated, and a `TestAllocCall` run on the rebased tree printed the same
CALL, STAGE and ITEM lines.

### How the numbers were taken

- Allocations: `testsupport.Measure` deltas of `runtime.MemStats.Mallocs`
  and `TotalAlloc`, the collector off and `GOMAXPROCS(1)`
  (`testsupport.QuietRuntime`), five runs, and the minimum that at least
  three runs share in both counters (`testsupport.StableMin`). Two calls of
  each client warm the pools, the encoder and the decoder scratch first.
- The state is a string of 1 022 bytes boxed in an `any` before the call
  (its JSON is 1 024 bytes; NF1's B = 0). q3 is the three questions of the
  upstream round-trip test (`pytest:test_clients.py:60-80`) answered by
  `result.json` (364 B); q20 is the twenty questions `result-20.json`
  answers (2 253 B), derived from the fixture as `testdata/README.md` says.
- The floor (NF3) = `Recorder.RoundTrip` of a request built before the
  measured section (the prototype's URL and header template over a
  pre-encoded body, rewound between runs), its response drained into
  `io.Discard` and closed, plus `E_sonic` (`encoder.EncodeInto` of the same
  state into a warm buffer). This is the Rust port's `floor`
  (`benches/sdk/call.rs:131`: "the transport called directly with a request
  built beforehand"). SDK-own = call/sdk − floor.
- The Recorder answers `testsupport.JSON(200, fixture)`: a `Content-Type`
  header and a declared `Content-Length`. The AC-P5 replies carry no header,
  which is why their successful calls count 20/2 488 instead of 23/2 904 (the
  Recorder's clone of that one header is 3 mallocs, 416 B).
- The stage split runs one call as six measured sections in `SystemOne`'s
  order; the stages sum to the whole call exactly (asserted), and the request
  stage's items sum to the stage (asserted).
- Time: `go test -bench . -benchmem -count=5` with `for b.Loop()`, the
  collector on; the tables show the median of five and half the min-to-max
  spread; `benchstat` summaries are in `results/benchstat-{M,L}.txt` (five
  samples print `± ∞`). With the collector on, a collection sometimes empties
  the scratch pool, so the benchmarks' B/op run a little above the
  collector-off counts; allocs/op round to the same numbers.
- Load (R17): (M) runs started at a 1-minute load of 6.9–7.7 on 16 cores
  (the timing row passed the load gate of 16 without waiting), (L) at
  0.0–1.3 on 44. No row is noisy.

### S-C1 findings

1. **Every allocation count is identical on (M) and (L)**, for both shapes,
   every stage and every AC-P5 case.
2. **Whole call, q3** (the NF3 scenario): floor 8 (Recorder 7 + `E_sonic`
   1), call/sdk 23, **SDK-own 15** (2 264 B); call/naive 119, naive-own 111.
   SDK-own / naive-own = 0.135; total / naive = 0.193. **q20**: SDK-own 35
   (9 504 B), naive-own 514; 0.068 and 0.082.
3. **Where the 15 are** (stage, then item; the ITEM rows of
   `results/alloc-*.txt`):

   | stage | allocs/B | item | SDK-own | avoidable? |
   | --- | --- | --- | --- | --- |
   | encode | 1/16 | `codec.NewBody` from the warm pool 0, `EncodeInto` 1 (= `E_sonic`, in the floor), appends 0 (the 4 KiB scratch holds the 1 292 B q3 and 3 172 B q20 bodies) | 0 | – |
   | request | 9/1 080 | `*codec.BodyReader` for `Request.Body`: 1/64 | 1 | yes (W1.2): embed the first reader in the pooled scratch; its `Close` already checks the generation |
   | | | `GetBody` closure: 1/24 | 1 | no: a closure cached per scratch would open whatever generation the scratch holds when called, so a RoundTripper calling `GetBody` after the call returned would read a later call's bytes (PM4) |
   | | | header map, `make(http.Header, 7)` + 7 inserts: 2/400 (the map and its one group) | 2 | yes (W2.3), when the call has no per-call header: one immutable map per attempt number, shared by every call; the `RoundTripper` contract forbids modifying the request |
   | | | `context.WithTimeout`: 4/272 (timerCtx, the `time.AfterFunc` timer, its closure, the `CancelFunc` closure); the same 4/272 under a parent that cannot be canceled | 4 | not now: a custom attempt context with a pooled timer would cost its Done channel only, but the transport's goroutines keep the context; W2.3 may measure it |
   | | | `*http.Request`: 1/320 (`WithContext`'s copy; the literal stays on the stack, `-gcflags=-m`) | 1 | no |
   | round trip | 7/624 | the Recorder's own, equal to the floor's round trip | 0 | – |
   | read | 1/384 | the body buffer, exactly `Content-Length` (364 B in its 384 B size class; q20: 2 253 → 2 304 B) | 1 | no: the response owns it (`RawBody`; answers alias it) |
   | decode | 5/800 | `*Result`: 1/112 | 1 | yes, if `SystemOne` returns a value (API decision) |
   | | | `DecodeInto`: 4/688 (entries 1, choice probabilities 1, score legend 1, score probabilities 1; R24's `result` = 4); q20: 24/6 008 | 4 | decode-side (AC-P2): one arena per slice type per response would make it 4 whatever the answer count (W2.0 candidate) |
   | finish | 0/0 | `Put`, `Close`, `cancel()`, `Release` | 0 | – |

4. **Over a real transport the call pays one more per attempt**: the
   attempt context's Done channel is made by the first `Done()` call, which
   the HTTP/2 client makes
   (`GOROOT:net/http/internal/http2/transport.go:1129,1201,1250`) and the
   Recorder never does (ITEM `Done` = 1/112).
   NF3/AC-P6 are stated through the in-memory RoundTripper, so it is not in
   N; the W5 loopback benchmarks will show it.
5. **Time** ([tables](#w05-tables)): call/sdk is faster than call/naive on
   both hosts: q3 4.647 µs vs 8.968 µs on (M) (0.52×) and 5.837 µs vs
   16.76 µs on (L) (0.35×); q20 23.28 vs 39.78 µs (0.59×) and 24.25 vs
   77.34 µs (0.31×). The decode is most of the call: S-D1 measured
   `result.json` at 3.22 µs (M) / 3.09 µs (L) (W0.3-14, W0.3-13), so encode,
   request, round trip and read together take about 1 µs (M) and 2.1 µs (L),
   of which the floor is 0.42 µs and 0.66 µs.

**Proposed provisional N (NF3 / AC-P6): 15**, the measured SDK-own count
with no slack, for the NF3 scenario as S-C1 ran it: q3, a 1 KiB state boxed
before the call, no per-call options, no enabled logger, one attempt,
`testsupport.Recorder`. W2.3's target is 12 = 15 − 2 (shared header map) − 1
(first reader in the scratch). That equals Rust's 12, but the two counts are
composed differently: the Go floor includes `E_sonic` (Rust counts its encode
block inside its 12), and Go pays 4 for `context.WithTimeout` and 1 for the
`*http.Request` where Rust's pinned future pays nothing. Anything Phase 3 adds
(retry loop, telemetry, redaction, `Stats`) is itemized against the table
above before the end-of-Phase-3 freeze.

### AC-P5 findings and proposed bounds

Whole-call and read-stage `TotalAlloc` deltas, identical on both hosts; the
plan's reading of NF5 is `plan-256K` (the first buffer of an undeclared body
is also 256 KiB); full table under [W0.5 tables](#w05-tables).

| case | outcome | call mallocs/B | read stage mallocs/B |
| --- | --- | --- | --- |
| (i) `Content-Length: 16 MiB`, 10 bytes sent | `io.ErrUnexpectedEOF` | 15/263 448 | 1/262 144 |
| (ii) declared 16 MiB + 1 | `*TooLargeError`, nothing read | 15/1 328 | 1/24 (the error) |
| (iii) undeclared 16 MiB + 1 | `*TooLargeError` after cap + 1 bytes | 22/33 293 616 | 8/33 292 312 |
| (iv) declared 16 MiB exactly | accepted, 3 answers | 26/33 294 392 | 7/33 292 288 |

- (iii) and (iv) allocate the doubling sequence 256 KiB … 16 MiB (7 buffers),
  2 × cap − 256 KiB = 33 292 288 B, plus the rest of the call.
- The read never allocates past the cap: a full buffer first reads one byte
  into a pooled probe, so a body that ends exactly at the buffer's capacity
  (every declared body, and a 16 MiB undeclared one) does not grow it, and
  the byte after the cap refuses the body (`TestReadBody`,
  `TestSystemOneCap`).

**Proposed AC-P5 bounds**, per attempt (see the retry question below):

| case | proposed bound | measured | plan |
| --- | --- | --- | --- |
| (i) | ≤ 256 KiB + 64 KiB = 327 680 B | 263 448 B | < 1 MiB |
| (ii) | ≤ 64 KiB | 1 328 B | < 1 MiB |
| (iii) | ≤ 2 × cap + 64 KiB = 33 619 968 B | 33 293 616 B | ≤ 2 × cap + 1 MiB |
| (iv) (new) | ≤ 2 × cap + 64 KiB | 33 294 392 B | – |

The 64 KiB margin is 26 to 49 × the call's own non-body bytes (2 488 B for
a successful q3 call, 1 328 B on the refused path), room for Phase 3's error
values and log records. It still catches the regressions that matter: in (i)
and (ii) a first buffer of 512 KiB or any buffer sized by the declared
length; in (iii) and (iv) any buffer beyond the doubling sequence (a
shrink-to-fit copy, a growth factor below 2). The plan's looser bounds also
hold.

**A tighter initial buffer (64 KiB)** changes only (i): 66 840 B. (ii) is
unchanged; (iii) and (iv) take two more growth steps (+2 mallocs,
+196 608 B, still ≤ 2 × cap + 64 KiB); a declared body between 64 KiB and
256 KiB then grows once or twice instead of being read into one exact
buffer. The cap-relative bounds do not change.

**Finding: an undeclared small body pays the whole first buffer.** Under the
plan's reading, a chunked `result.json` (364 B) costs 264 248 B per call,
against 2 488 B when its length is declared. Proposal for NF5: the first
buffer is min(`Content-Length`, 256 KiB) for a declared body and 4 KiB for an
undeclared one (`split-256K-4K`): 6 200 B per small chunked call; an
undeclared 16 MiB body then takes 13 buffers instead of 7 (+6 mallocs,
+258 048 B, still ≤ 2 × cap + 64 KiB). Whether the API declares
`Content-Length` on its 2xx responses is not known here (no live traffic);
W6.4's recording should note `resp.ContentLength`.

**Kept: doubling for declared bodies.** Growing straight to a declared
`Content-Length` once the first 256 KiB arrived would read an honest 16 MiB
body with 16.25 MiB instead of 31.75 MiB, but a peer that sends 256 KiB could
then force a 16 MiB allocation (64 × what it sent); doubling bounds that at
2 ×.

### Questions for W0.6

- Retries and AC-P5: (i) is a 2xx whose body ends early; if the default
  policy retries it as a connection error, one call pays the 256 KiB buffer
  per attempt. Proposal: AC-P5's bounds are per attempt, and
  `TestMemStatsCap` runs with `Retry(NoRetry())`.
- The retry-count header: the prototype sends `X-TypeSafe-Retry-Count: 0` on
  the first attempt, as the W0.5 brief asked; Python sends it only when
  `attempts > 0` (`py:_core/transport.py:71-72`) and plan 1.1.3 says "on
  retries". The count is the same either way (seven or six entries fit one
  map group); W2.3 follows the plan unless ruled otherwise.
- NF5's undeclared first buffer: 256 KiB (the plan's reading) or 4 KiB (the
  proposal above).

<a id="w05-tables"></a>

### W0.5 tables

#### S-C1 whole call (allocs/B, identical on (M) and (L); W0.5-01, W0.5-05)

| shape | `E_sonic` | floor round trip | floor | call/sdk | SDK-own | call/naive | naive-own | SDK-own / naive-own | call/sdk / call/naive |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| q3 | 1/16 | 7/624 | 8/640 | 23/2 904 | 15/2 264 | 119/7 960 | 111/7 320 | 0.135 | 0.193 |
| q20 | 1/16 | 7/624 | 8/640 | 43/10 144 | 35/9 504 | 522/30 872 | 514/30 232 | 0.068 | 0.082 |

#### S-C1 by stage (allocs/B; W0.5-01, W0.5-05)

| shape | encode (own) | request | round trip (own) | read | decode | finish | sum |
| --- | --- | --- | --- | --- | --- | --- | --- |
| q3 | 1/16 (0/0) | 9/1 080 | 7/624 (0/0) | 1/384 | 5/800 | 0/0 | 23/2 904 |
| q20 | 1/16 (0/0) | 9/1 080 | 7/624 (0/0) | 1/2 304 | 25/6 120 | 0/0 | 43/10 144 |

#### S-C1 items (allocs/B; W0.5-01, W0.5-05)

| shape | `body.Open` | `GetBody` | header | `WithTimeout` | `Request` | `cancel()` | `*Result` | `DecodeInto` | `WithTimeout`, detached parent | attempt ctx `Done()` |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| q3 | 1/64 | 1/24 | 2/400 | 4/272 | 1/320 | 0/0 | 1/112 | 4/688 | 4/272 | 1/112 |
| q20 | 1/64 | 1/24 | 2/400 | 4/272 | 1/320 | 0/0 | 1/112 | 24/6 008 | 4/272 | 1/112 |

#### S-C1 time (median of 5, ± half the min-to-max spread; W0.5-04, W0.5-08)

| benchmark | (M) ns/op | (M) B/op | (L) ns/op | (L) B/op | allocs/op |
| --- | --- | --- | --- | --- | --- |
| `Call/q3/floor` | 296.4 ns (±6.1 %) | 621 | 564.8 ns (±0.6 %) | 623 | 7 |
| `Call/q3/esonic` | 128.0 ns (±0.7 %) | 16 | 93.51 ns (±0.1 %) | 16 | 1 |
| `Call/q3/sdk` | 4.647 µs (±1.7 %) | 3.198 KiB | 5.837 µs (±0.3 %) | 3.252 KiB | 23 |
| `Call/q3/naive` | 8.968 µs (±4.0 %) | 7.807 KiB | 16.76 µs (±0.4 %) | 7.856 KiB | 119 |
| `Call/q20/floor` | 323.5 ns (±4.5 %) | 621 | 601.7 ns (±0.5 %) | 624 | 7 |
| `Call/q20/esonic` | 128.7 ns (±1.0 %) | 16 | 93.44 ns (±0.1 %) | 16 | 1 |
| `Call/q20/sdk` | 23.28 µs (±9.0 %) | 11.11 KiB | 24.25 µs (±0.3 %) | 11.60 KiB | 43 |
| `Call/q20/naive` | 39.78 µs (±2.1 %) | 30.34 KiB | 77.34 µs (±0.4 %) | 30.63 KiB | 522 |

#### AC-P5 memstats, whole call and read stage (mallocs/B, identical on (M) and (L); W0.5-01, W0.5-05)

`plan-256K`: first buffer min(`Content-Length`, 256 KiB), undeclared 256 KiB;
`tight-64K`: 64 KiB for both; `split-256K-4K`: 256 KiB declared, 4 KiB
undeclared. Replies without headers (see above).

| case | outcome | plan-256K call | plan-256K read | tight-64K call | tight-64K read | split-256K-4K call | split-256K-4K read |
| --- | --- | --- | --- | --- | --- | --- | --- |
| (i) declared 16 MiB, 10 B sent | eof | 15/263 448 | 1/262 144 | 15/66 840 | 1/65 536 | 15/263 448 | 1/262 144 |
| (ii) declared 16 MiB + 1 | too large | 15/1 328 | 1/24 | 15/1 328 | 1/24 | 15/1 328 | 1/24 |
| (iii) undeclared 16 MiB + 1 | too large | 22/33 293 616 | 8/33 292 312 | 24/33 490 224 | 10/33 488 920 | 28/33 551 664 | 14/33 550 360 |
| (iv) declared 16 MiB | ok | 26/33 294 392 | 7/33 292 288 | 28/33 491 000 | 9/33 488 896 | 26/33 294 392 | 7/33 292 288 |
| (v) undeclared 16 MiB | ok | 26/33 294 392 | 7/33 292 288 | 28/33 491 000 | 9/33 488 896 | 32/33 552 440 | 13/33 550 336 |
| (vi) declared `result.json` | ok | 20/2 488 | 1/384 | 20/2 488 | 1/384 | 20/2 488 | 1/384 |
| (vii) undeclared `result.json` | ok | 20/264 248 | 1/262 144 | 20/67 640 | 1/65 536 | 20/6 200 | 1/4 096 |

## W1.3: Prepare() allocations

`BenchmarkPrepare`, `BenchmarkFalsyJSON` and their question sets
(`prepareCases`) are in `bench_prepare_test.go` (since G5, `prepare_cases_test.go`); `TestAllocPrepare`
(`//go:build !race`) is in `alloc_prepare_test.go`. Both landed in 95f3e4c on
top of b227e5b (W1.1 as landed). 95f3e4c changes no production file, so every
number here is b227e5b's `Prepare()`. The measured section is `qs.Prepare()`
alone; `NewQuestions()` and the adding methods build the set outside it. Raw
outputs: `_spikes/w1.3/results/`. The tables under
[W1.3 tables](#w13-tables) were printed from those files by
`_spikes/w1.3/render.py results 95f3e4c`, which also checks that (M) and (L)
agree and that each row of the call-site table sums to the measured count.
The runs are rows W1.3-01 to W1.3-05 of the [Rows](#rows) table. Commands use
`R=_spikes/s-c1/run.sh` (W0.5's runner: `flock(1)` on the shared lock; a header
with the date, the load from `uptime` and from the kernel, the base,
`go version` and ToolTags; a footer with the exit status, date and load; all
printed by the shell that runs the measurement), `O=_spikes/w1.3/results`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/c8084031-5323-4873-8c36-a19f65c9e6ff/scratchpad`
and `BASE='95f3e4c (production code as b227e5b)'`.

The sets, by sub-benchmark name (`prepareCases` holds the code):

- `c1-sketch`: the plan's §5 sketch: a noul with instructions and a yes
  outcome, a choice with instructions and two options (one described), a
  score with three text levels, and a raw noul with one string field.
- `c2-noul-short`: one noul, `Instructions: Text("Spam?")`.
- `c3-choice-20x10`: 20 choices with instructions and 10 described options
  each.
- `c4a-score-20x8-text` and `c4b-score-20x8-json`: 20 scores with
  instructions and 8 levels each; a level is text, or a JSON object
  pretty-printed as `json.MarshalIndent` writes it (two-space indent), which
  Prepare compacts.
- `c5-raw-100x3`: 100 raw `noul` questions with three fields: `instructions`
  (a string), `weight` (float64 0.5) and `meta` (a `map[string]any` holding a
  string, a `[]any` of two strings and a `map[string]any` of two ints).
- `c6-escapes`: `c1-sketch` with U+0000 to U+001F, U+2028, U+2029 and U+1F600
  appended to every name and text.
- The NIT 8 pairs: `n8a-array-score` is 20 raw `score` questions with
  `instructions`, `weight` and a `criteria` that is `RawJSON` of `c4b`'s eight
  levels as one pretty-printed array; `n8b-map-score` is 100 such questions
  whose `criteria` is `JSON` content of a pretty-printed object with nested
  objects and arrays. Each `-control` is the same set with the type `Score`:
  `prepareRaw` compares the type with `"score"` (`questions.go:350`), so the
  falsiness check does not run while the writer does the same work, and
  score − control is the cost of the check.

### How the numbers were taken

- (M): `go1.27.1 darwin/arm64`, `GOEXPERIMENT=nosimd,noruntimesecret` (the
  ToolTags in the rows are the Go 1.27 baseline). Every run, the profile
  included, held `/opt/homebrew/opt/util-linux/bin/flock` on `$SP/bench.lock`.
  First start 2026-09-25 21:48:48 JST, last end 21:51:03 JST (raw headers and
  footers).
- (L): the committed tree without `.git` and `_spikes/w1.3`, copied with the
  §11 `tar | ssh 'tar -x'` pipe to `/tmp/ts-spike/src-w1.3/wt-w1.3`. Toolchain
  `/tmp/ts-spike/go/bin/go` (`go1.27.1 linux/amd64`), with
  `GOPATH=/tmp/ts-spike/gopath GOMODCACHE=/tmp/ts-spike/modcache
  GOCACHE=/tmp/ts-spike/gocache` and no `GOEXPERIMENT`; each run held
  `flock /tmp/ts-spike/bench.lock`. First start 2026-09-25 12:49:07 UTC, last
  end 12:50:32 UTC.
- Allocations: `testsupport.MeasureMin`, i.e. `runtime.ReadMemStats` deltas of
  `Mallocs` and `TotalAlloc` under `testsupport.QuietRuntime` (collector off,
  `GOMAXPROCS(1)`). Five runs, each on a fresh set from `prepareCases`; the
  result is the minimum that at least three runs share in both counters.
  Prepare uses no pool, so nothing is warmed first. In every case on both
  hosts all five runs agreed in both counters, so each byte range is a single
  value.
- Time: `for b.Loop()` with `b.ReportAllocs()` and the collector on. The set
  is built once, before the loop, because Prepare only reads it. `-count=5`;
  the tables show the median of the five and half their min-to-max spread.
  `benchstat` summaries are in `results/benchstat-{M,L}-base95f3e4c.txt`
  (five samples print `± ∞`). `B/op` is within 8 bytes of the collector-off
  count: the key iterator's 8-byte state word (finding 3) is a tiny
  allocation, and in a loop it shares a 16-byte block with the next call's.
- Call sites: `_spikes/w1.3/breakdown.sh`, committed with these results,
  builds the test binary and runs each case of `TestAllocPrepare` with
  `-test.memprofilerate=1`. For each case it prints
  `go tool pprof -sample_index=alloc_objects -focus='Questions..Prepare$' -traces`,
  with the testing frames dropped, into `results/breakdown-M-base95f3e4c.txt`;
  the counts there cover MeasureMin's five calls. An 8-byte pointer-free
  object that fits into the current tiny block counts in `MemStats.Mallocs`
  but is never sampled. The call-site table was therefore reconciled with the
  MemStats counts, not summed from the profile, and the renderer checks every
  row. (L) was not profiled because its counts are identical.
- Load (R17): on (M), 8.06 → 7.90 (allocations), 8.39 → 5.29 (time) and
  4.38 → 4.38 (profile), on 16 cores; on (L), 0.04 → 0.04 and 0.04 → 0.63, on
  44. No row is noisy. The W1.2 lane was working on (M) at the same time;
  its measurements take the same lock.

### W1.3 findings

1. **Every count is identical on (M) and (L)**: mallocs, bytes and prepared
   length, for all eleven sets, with 5/5 runs agreeing in each. So
   `TestAllocPrepare` pins one number per case, not a table keyed by GOARCH.
2. **The W1.1 lane's numbers reproduce.** `c1-sketch` makes 9 mallocs and
   `c3-choice-20x10` 27, as the W1.1 handoff reports. The charter's 10 and 28,
   from the lane's earlier probe, are one more each. That matches the shape
   before the NIT 7 fix, which removed the second allocation of the
   `Prepared` wrapper; this is inferred from the order of events, because the
   probe is not in the tree. The fix holds: `*Prepared` is one
   64-byte object (`new(Prepared)`, `questions.go:235`), and `Builder.Finish`
   fills the embedded `wire.Prepared` in place (`-gcflags=-m`: `p does not
   escape` in `(*Prepared).init`).
3. **Where the allocations come from** (the call-site table; line numbers at
   b227e5b):
   - Fixed, 3 per set: `Builder.Grow` makes `buf` (`prepared.go:190`, sized
     by `Questions.sizeHint`) and `entries` (`:191`), and `Prepare` makes the
     `*Prepared`.
   - Tables, 1 per `Choice` (`labels`, `questions.go:309`) and 1 per `Score`
     (`levels`, `:324`). Each becomes the question's Options or Levels table
     in the prepared set, which must not share memory with the caller's
     values (R45).
   - Raw map keys: `slices.Sorted(maps.Keys(m))`, once for a raw question's
     `Fields` (`prepared.go:347`) and once for every nested `map[string]any`
     and `map[string]string` (`json.go:425`, `:442`). Each call costs 3
     allocations for the iterator plumbing (the 32-byte yield closure, the
     24-byte slice header it captures and an 8-byte state word), plus 1 per
     growth of the key slice from nil (1 key: 1; 2 keys: 2; 3 or 4 keys: 3).
     `c5-raw-100x3` pays 6 + 6 + 5 = 17 per question for its three maps,
     which is 1 700 of its 1 710 allocations.
   - `spans` growth: `Builder.Score` appends one 32-byte `levelSpan` per
     JSON level to a slice that `Grow` does not size. In
     `c4b-score-20x8-json`, 160 spans take 9 growths (32 B up to 8 KiB).
   - Lookup maps: for more than 8 questions (wire's `linearLimit`),
     `(*Prepared).init` builds the `index` map in 4 allocations (header,
     directory, table and groups), both at 20 and at 100 questions. For more
     than 32 (`repeatScanLimit`), `Prepare` also builds its `seen` map, in 3
     (the header stays on the stack).
   - `buf` regrowth, when `sizeHint` falls short. `c6-escapes` regrows 3
     times (1 → 1.5 → 2.25 → 3 KiB), because the hint counts bytes before
     escaping and a control character is written as up to six bytes. Each n8
     set regrows 5 times (3.1 → 16 KiB for `n8a`, 16 → 72 KiB for `n8b`),
     because the hint counts 32 bytes per raw field whatever the field holds.
   - `falsyJSON` copies: finding 5.

   Nothing else allocates. Text, JSON content, JSON levels and floats are
   written into `buf` directly, and the JSON compactor keeps its container
   stack in a 32-byte array on the goroutine stack.
4. **Time.** `c1-sketch` takes 605.2 ns on (M) and 1.155 µs on (L); the same
   shape with every string escaped (`c6-escapes`) takes 3.282 µs and
   5.384 µs. `c5-raw-100x3`, the largest set outside the NIT 8 pairs, takes
   73.01 µs on (M) and 133.7 µs on (L), with 1 700 of its 1 710 allocations
   spent sorting map keys.
5. **NIT 8.** `falsyJSON` runs only for a raw question of type `score`
   (`questions.go:350`). A typed `Score` (case 4) never reaches it, and
   neither do case 5's raw `noul` questions; for a map value `falsy` only
   checks the length and copies nothing. When the criteria are `RawJSON` or
   `JSON` content that compact to more than 32 bytes, the check costs 6
   allocations per question. `wire.AppendJSON` outgrows the 32-byte stack
   buffer five times (objects of 64, 128, 256, 512 and 896 B), and
   `string(compact)` copies the result once more, into a 704 B object for the
   array and a 576 B one for the object: the compiler's stack buffer for that
   conversion is 32 bytes too. The check compacts the whole value, and the
   writer then compacts it again into `buf`. Across the two pairs this is
   47.6 % and 49.4 % of the mallocs, 46.5 % and 47.2 % of the bytes, and
   44.8 % to 46.3 % of the time, on both hosts. Alone, the check takes
   1.194 µs on (M) and 2.089 µs on (L) for the 8-level array; under 32 bytes
   it allocates nothing and takes 27.90 ns on (M).
6. **In CI since 0893f0c (R50).** `TestAllocPrepare` (and, since W1.2,
   `TestAllocBodyKinds`) runs in `ci.yaml`'s "root allocation tests" step on
   every image, behind a `-list` count guard so a rename cannot pass as "no
   tests to run"; the `-race` step still excludes the file by its build tag.
   W5.2 folds the names into §11's ALLOC list and the step into its own.

### Proposals for W5.3

The estimates are arithmetic on the call-site table, not measurements.

1. **Raw map keys (the largest).** In `Builder.Raw` and `appendValue`,
   replace `slices.Sorted(maps.Keys(m))` with a key slice that the `Builder`
   owns and uses as a stack: append the map's keys at its end, `slices.Sort`
   that tail, write the members, then truncate. Once the slice has grown, no
   further map allocates for its keys: `c5-raw-100x3` would fall from 1 710
   to roughly 10 to 15 allocations, and `c1-sketch` from 9 to 5 or 6. A
   smaller step, `make([]string, 0, len(m))` with a `for k := range m` loop
   and `slices.Sort`, leaves 1 allocation per map (`c5` 310, `c1` 6).
2. **NIT 8: decide falsiness from the first non-space byte.** After
   `skipSpace`, the value is falsy when it starts with `n` or `f`, and truthy
   when it starts with `t`. A value starting with `"` is falsy when the next
   byte is `"`; one starting with `[` or `{` is falsy when the next non-space
   byte closes it; one starting with `-` or a digit is falsy when its
   mantissa has no digit from 1 to 9 before `e`, `E` or its end. Only a falsy
   verdict then runs `wire.AppendJSON` into the 32-byte stack buffer, so that
   invalid JSON still reads as not falsy and the writer reports its syntax
   error, as today. That is the path on which the question is rejected, so
   its cost does not matter. The success path then neither copies nor scans
   the value: `n8a` would fall from 252 to 132 allocations and `n8b` from
   1 215 to 615, saving about 1 to 2 µs per question.
3. **Size the buffer for raw values.** `sizeHint` adds 32 bytes per raw field
   (`questions.go:482`) whatever the field holds. Adding the length of a
   top-level string, `RawJSON` or `Content` value removes the 5 regrowths of
   both n8 sets (a 3.1 KiB buffer that ends at 16 KiB, and a 16 KiB one that
   ends at 72 KiB).
4. **Size `spans`.** Count the JSON levels in `sizeHint` and let `Grow`
   reserve room for them: `c4b-score-20x8-json` would fall from 36 to 28.
5. **One backing array per table kind.** Cut the choices' `labels` tables
   from one slice and the scores' `levels` tables from another, sized from
   the counts that `sizeHint` already walks: `c3-choice-20x10` and
   `c4a-score-20x8-text` would fall from 27 to 8. Either way the tables live
   exactly as long as the prepared set; the cost is a count that `sizeHint`
   has to return.
6. **Lookup maps.** For sets over 32 questions, hand `Prepare`'s `seen` map to
   wire as the `index` (as a `map[string]int`) instead of building both: 3
   fewer allocations for `c5`. Minor.
7. **Escapes: no change.** `c6-escapes` regrows 3 times because `sizeHint`
   counts bytes before escaping. Names and texts full of control characters
   are rare.

### W1.3 tables

#### Allocations and prepared length (W1.3-01, W1.3-04)

| Case | mallocs (M) | mallocs (L) | bytes (M), 5 runs | bytes (L), 5 runs | prepared bytes | pinned |
| --- | --- | --- | --- | --- | --- | --- |
| `c1-sketch` 1: the plan's §5 sketch (noul, choice of 2, score of 3, raw noul) | 9 (5/5) | 9 (5/5) | 1 016 | 1 016 | 340 | yes |
| `c2-noul-short` 2: one noul, short text | 3 (5/5) | 3 (5/5) | 224 | 224 | 47 | yes |
| `c3-choice-20x10` 3: 20 choices × 10 options | 27 (5/5) | 27 (5/5) | 15 256 | 15 256 | 8 411 | yes |
| `c4a-score-20x8-text` 4a: 20 scores × 8 text levels | 27 (5/5) | 27 (5/5) | 17 176 | 17 176 | 7 611 |  |
| `c4b-score-20x8-json` 4b: 20 scores × 8 JSON levels, pretty-printed input | 36 (5/5) | 36 (5/5) | 44 408 | 44 408 | 14 491 |  |
| `c5-raw-100x3` 5: 100 raw noul × 3 fields (string, float, nested map) | 1 710 (5/5) | 1 710 (5/5) | 78 080 | 78 080 | 15 681 |  |
| `c6-escapes` 6: the sketch, every name and text + U+0000–U+001F, U+2028, U+2029, U+1F600 | 12 (5/5) | 12 (5/5) | 8 536 | 8 536 | 2 888 |  |
| `n8a-array-score` NIT 8a: 20 raw score, criteria `RawJSON` array (8 pretty levels) | 252 (5/5) | 252 (5/5) | 110 040 | 110 040 | 14 681 |  |
| `n8a-array-control` NIT 8a control: the same, type `Score` (no falsiness check) | 132 (5/5) | 132 (5/5) | 58 840 | 58 840 | 14 681 |  |
| `n8b-map-score` NIT 8b: 100 raw score, criteria `JSON` nested object (pretty) | 1 215 (5/5) | 1 215 (5/5) | 514 944 | 514 944 | 64 581 |  |
| `n8b-map-control` NIT 8b control: the same, type `Score` (no falsiness check) | 615 (5/5) | 615 (5/5) | 271 744 | 271 744 | 64 581 |  |

#### Allocations by call site (identical on (M) and (L); W1.3-03)

| Case | fixed: `buf`, `entries`, `*Prepared` | tables: `labels` per choice, `levels` per score | raw map keys: `slices.Sorted(maps.Keys(m))` | `spans` growth | `index` map (> 8 questions) | `seen` map (> 32 questions) | `buf` regrowth | `falsyJSON` copies | total |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `c1-sketch` | 3 | 2 | 4 | 0 | 0 | 0 | 0 | 0 | 9 |
| `c2-noul-short` | 3 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 3 |
| `c3-choice-20x10` | 3 | 20 | 0 | 0 | 4 | 0 | 0 | 0 | 27 |
| `c4a-score-20x8-text` | 3 | 20 | 0 | 0 | 4 | 0 | 0 | 0 | 27 |
| `c4b-score-20x8-json` | 3 | 20 | 0 | 9 | 4 | 0 | 0 | 0 | 36 |
| `c5-raw-100x3` | 3 | 0 | 1 700 | 0 | 4 | 3 | 0 | 0 | 1 710 |
| `c6-escapes` | 3 | 2 | 4 | 0 | 0 | 0 | 3 | 0 | 12 |
| `n8a-array-score` | 3 | 0 | 120 | 0 | 4 | 0 | 5 | 120 | 252 |
| `n8a-array-control` | 3 | 0 | 120 | 0 | 4 | 0 | 5 | 0 | 132 |
| `n8b-map-score` | 3 | 0 | 600 | 0 | 4 | 3 | 5 | 600 | 1 215 |
| `n8b-map-control` | 3 | 0 | 600 | 0 | 4 | 3 | 5 | 0 | 615 |

#### Time (median of 5 ± half the min-to-max spread; W1.3-02, W1.3-05)

| Case | (M) | (L) | B/op | allocs/op |
| --- | --- | --- | --- | --- |
| `c1-sketch` | 605.2 ns ± 5.3 ns | 1.155 µs ± 0.006 µs | 1 008 | 9 |
| `c2-noul-short` | 99.30 ns ± 2.45 ns | 180.2 ns ± 0.7 ns | 224 | 3 |
| `c3-choice-20x10` | 11.79 µs ± 0.18 µs | 20.28 µs ± 0.08 µs | 15 256 | 27 |
| `c4a-score-20x8-text` | 9.417 µs ± 0.191 µs | 16.09 µs ± 0.08 µs | 17 176 | 27 |
| `c4b-score-20x8-json` | 25.32 µs ± 0.41 µs | 44.48 µs ± 0.07 µs | 44 408 | 36 |
| `c5-raw-100x3` | 73.01 µs ± 1.89 µs | 133.7 µs ± 0.3 µs | 78 080–78 081 | 1 710 |
| `c6-escapes` | 3.282 µs ± 0.091 µs | 5.384 µs ± 0.030 µs | 8 528 | 12 |
| `n8a-array-score` | 54.31 µs ± 0.81 µs | 99.61 µs ± 0.10 µs | 110 040 | 252 |
| `n8a-array-control` | 30.00 µs ± 0.86 µs | 54.32 µs ± 0.18 µs | 58 840 | 132 |
| `n8b-map-score` | 234.4 µs ± 5.3 µs | 435.7 µs ± 1.7 µs | 514 946–514 947 | 1 215 |
| `n8b-map-control` | 127.1 µs ± 1.3 µs | 233.8 µs ± 1.4 µs | 271 745–271 746 | 615 |

#### NIT 8: the falsiness check's share (score − control)

| Pair | mallocs | share of mallocs | bytes | share of bytes | time (M), share | time (L), share |
| --- | --- | --- | --- | --- | --- | --- |
| `n8a-array` (20 questions) | 252 − 132 = 120 (6 per question) | 47.6% | 110 040 − 58 840 = 51 200 (2 560 per question) | 46.5% | 54.31 µs − 30.00 µs = 24.31 µs (1.216 µs per question), 44.8% | 99.61 µs − 54.32 µs = 45.29 µs (2.264 µs per question), 45.5% |
| `n8b-map` (100 questions) | 1 215 − 615 = 600 (6 per question) | 49.4% | 514 944 − 271 744 = 243 200 (2 432 per question) | 47.2% | 234.4 µs − 127.1 µs = 107.3 µs (1.073 µs per question), 45.8% | 435.7 µs − 233.8 µs = 201.9 µs (2.019 µs per question), 46.3% |

#### NIT 8: `falsyJSON` alone (`BenchmarkFalsyJSON`; W1.3-02, W1.3-05)

| Input | (M) | (L) | B/op | allocs/op |
| --- | --- | --- | --- | --- |
| `small-14B` | 27.90 ns ± 0.55 ns | 48.06 ns ± 0.25 ns | 0 | 0 |
| `array` | 1.194 µs ± 0.043 µs | 2.089 µs ± 0.006 µs | 2 560 | 6 |
| `map` | 978.0 ns ± 24.9 ns | 1.769 µs ± 0.007 µs | 2 432 | 6 |

## W1.2: the request state's UTF-8 pass (R48)

Ruling R48 makes `codec.EncodeState` refuse a state whose encoding is not
valid UTF-8, with one `utf8.Valid` pass over the encoded state bytes after
sonic's encode (sonic's options 0 copy a Go string's bytes as they are). The
row measures that pass next to the whole state encode it belongs to, for a
text state of ASCII and of CJK text (three-byte runes, which leave
`utf8.Valid`'s ASCII fast path), at the three sizes the ruling names.
`encode` is `EncodeState` into a pooled scratch, the pass included; `check`
is the pass alone over the same encoded bytes. The benchmark is
`BenchmarkEncodeState` in `internal/codec/encode_bench_test.go`; the run held
the bench lock.

Finding for W5.3 (ruling R54): R48 stands, with W1.2-01 as the baseline, and
the target is a validator that costs at most 1 × sonic's own encode on CJK
text (0.346 ms at 6 MiB). The candidate is an ASCII word scan (8 bytes at a
time) up to the first non-ASCII byte, then sonic's SIMD `utf8.Validate` for
the rest: in an unlocked lane probe it read CJK text at 5.3 GiB/s against
`utf8.Valid`'s 2.2 GiB/s, but ASCII at only 3.6 GiB/s against 72 GiB/s,
hence the ASCII scan in front of it.

K26, the other sonic finding of W1.2 (not a timing row): sonic's check of a
`json.Marshaler`'s output accepts some complete but invalid outputs
(`{"a":}`, `[1 2]`, a trailing comma). The lane's standalone probe
(`_spikes/w1.2/validrace`) saw it only under `-race` (0.42–0.46 %) and
reported normal builds as 0 for every input. That is withdrawn at landing:
inside the full root test binary the reviewer captured invalid bodies in a
normal build too, about 1 in 100,000 tries for a nested `json.RawMessage` or
a caller's `MarshalJSON`, and 0.6–0.7 % under `-race`, while the standalone
probe gave 0 of 20,000 in every configuration of the reviewer's runs. The
effect shows only inside the full test binary. Rulings R60 (the SDK's own
nested `Content` and `RawJSON` are checked by wire's scanner) and R61 (a
caller's Marshaler output is the caller's contract; no whole-state scan)
settle it.

K27, the third sonic finding (not a timing row): sonic v1.15.4 spells a
negative zero by architecture. CI run 36148078711 on aafdbc4 failed on
ubuntu-26.04 and windows-2025 (amd64) because `TestBodyDeviationsFromPython`
pinned the arm64 bytes. On amd64 sonic's JIT encoder
(`internal/encoder/x86/assembler_regabi_amd64.go`, `_asm_OP_f64` and
`_asm_OP_f32`) calls the native `f64toa`/`f32toa`, which write the sign bit
before the digits (`native/f64toa.c:367`), so -0.0 is `-0`. Every other
GOARCH runs sonic's VM encoder (`internal/encoder/pools_compt.go`, build line
`!amd64`, calls `ForceUseVM`), whose `alg.F64toa` and `alg.F32toa`
(`internal/encoder/alg/spec.go:157` and `:170`) return `0` for any zero before
the native call. R46's class (2) therefore reads "-0.0 is written `0` on
arm64 and `-0` on amd64", for float64 and float32 alike, wherever the float
sits (top level, slice element, struct field, map value); only on amd64 do
sonic's floats equal encoding/json's for every probed input. The lane probe
(`_spikes/w1.2/sonic_probe`, which gained the modes `zeros` and `sweep`) ran
on both hosts from the same tree: the 18-value float table, `misc` (control
characters, invalid UTF-8, NaN and the infinities, unsupported kinds, nil
containers, `[]byte`, `json.RawMessage`), the three cycles and the R59
extra-value floats are byte-equal apart from the negative zero, and the sweep
encoded the same 1,707,209 float64, 1,176,076 float32, 524,402 int64, 262,199
uint64 and 131,072 string inputs on both (equal input digests), with equal
output digests once the negative zeros are left out; against encoding/json,
amd64 differs for no input and arm64 only for negative zero. Raw:
`_spikes/w1.2/results/sonic-*-L.txt` (2026-09-25 14:45 UTC) next to the
`-M` files (`sonic-zeros-M.txt` and `sonic-sweep-M.txt` are new, 23:45 JST).
At aafdbc4 the (L) runs of `go test -race -count=1 ./...` and of the non-race
root, codec, wire and testsupport packages failed in these two subtests only
(the test job had never reached its non-race steps on amd64, because the
`-race` step failed first). `TestBodyDeviationsFromPython` now keys the two
float pins by `runtime.GOARCH`, and skips them on any other GOARCH (which the
D1 build line refuses anyway), per R62.

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W1.2-01 | 2026-09-25 22:04:45 JST | W1.2 R48 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 7.43 → 7.59 | `flock bench.lock GOEXPERIMENT=nosimd,noruntimesecret go test -run '^$' -bench '^BenchmarkEncodeState$' -benchmem -count=10 ./internal/codec/` | medians of 10, check / encode: ASCII 1 KiB 17.74 ns / 150.6 ns (11.8 %), 64 KiB 828.7 ns / 4.728 µs (17.5 %), 6 MiB 81.85 µs / 445.0 µs (18.4 %); CJK 1 KiB 426.5 ns / 555.8 ns (76.7 %), 64 KiB 27.31 µs / 30.96 µs (88.2 %), 6 MiB 2.618 ms / 2.964 ms (88.3 %); every ± ≤ 2 %. The pass reads 52.1–73.6 GiB/s on ASCII and 2.21–2.24 GiB/s on CJK, and allocates nothing (`encode` 1 alloc/op, sonic's) | Against the AC-P6 time clause (q3 `call/sdk` 4.647 µs against `call/naive` 8.968 µs, W0.5-04), the pass adds 18 ns to a 1 KiB ASCII state (0.4 % of `call/sdk`) and 427 ns to a 1 KiB CJK state (9.2 %); the clause holds either way. On CJK text the pass costs about 7.6 × sonic's own encode (2.618 ms against 0.346 ms at 6 MiB). Base b227e5b plus the W1.2 working tree; raw: `_spikes/w1.2/results/bench-utf8-M.txt`, `benchstat-utf8-M.txt`, and `bench-utf8-M.meta.txt` (the run's `date`, load, `go version` and ToolTags lines) |

## W2.0: the production decoder (AC-P2, AC-P8)

W2.0 ports S-D1's winner a1 (ruling R24) into `internal/codec`
(`DecodeSystemOne`, `DecodeModels`) behind the root's `decodeSystemOne`.
Four changes from the spike bring the decoder to the Python SDK's
failures (schema order per kind, the answer-type pre-pass, a null token
count read as absent, and `models[i].<member>` paths; probe output
`_spikes/w2.0/results/python-paths.txt`). A fifth change, the one that
moves the numbers, is interning (plan 6.2.5): every string of the answers,
and the model, is the request's own when it is equal to one, and a copy
otherwise, all copies of one decode sharing one arena. So a result never
aliases the body. Measured at 8686b40, the branch before its rebases onto
b88758c (W2.1), 7fd43ce (W2.2 Part A) and 0394219 (W2.2's K29 test fix).
On the branch as rebased onto 0394219 the same commit is 5daf169, and the
decoder's commits are 9810a38, ba39cbc and 88b3458. Neither W2.1's root
files nor W2.2's h2gate and testsupport files are on the decode path, and
the raw headers keep the measured SHA. Raw outputs are in
`_spikes/w2.0/results/`. The tables under [W2.0 tables](#w20-tables) were
printed from those files by `_spikes/w2.0/render.py`, which checks that
(M) and (L) agree and that every count is within its budget.
Commands use `R=_spikes/s-c1/run.sh` (W0.5's runner), `O=_spikes/w2.0/results`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/c8084031-5323-4873-8c36-a19f65c9e6ff/scratchpad`
and `BASE=8686b40`.

F-7 (ruling V28): row W2.0-06 describes 8686b40, and the landed head (45fd018) decodes slower on (L), +0.31 … +2.88 % in the SDK cells of verify-p2's interleaved A/B, which W5.3 re-measures and attributes.

### How the numbers were taken

- (M): `go1.27.1 darwin/arm64`, `GOEXPERIMENT=nosimd,noruntimesecret`. Every
  run held `/opt/homebrew/opt/util-linux/bin/flock` on `$SP/bench.lock`.
- (L): the tree at 8686b40, without `.git`, copied with the §11
  `tar | ssh 'tar -x'` pipe to `/tmp/ts-spike/src-w2.0-meas/wt-meas-8686b40`.
  Toolchain `/tmp/ts-spike/go/bin/go`, with the §11 `GOPATH`, `GOMODCACHE`
  and `GOCACHE` under `/tmp/ts-spike` and no `GOEXPERIMENT`. Each run held
  `flock /tmp/ts-spike/bench.lock`.
- Allocations: `TestAllocDecodeFixtures` (root, `//go:build !race`) decodes
  each fixture as a call does. The pooled decoder is warm and the result is
  fresh. The question set and model are the ones the response answers,
  built through the public API, so every string is interned; there is no
  logger. The count is `testsupport.MeasureMin` (`runtime.ReadMemStats`
  deltas under `QuietRuntime`: collector off, `GOMAXPROCS(1)`, three of five
  runs agreeing). The "without questions" column is the same decode with
  neither a question set nor a model: every string is a miss, copied into
  one arena. The lazy pass alone is `internal/codec`'s
  `TestLazyPassAllocations`, and AC-P8's ratios are `TestLinearityFlood`
  (time: in W2.0-01 and -04 the minimum of 21 single decodes each; since
  K30, the minimum over five spans of at least 250 ms each, see
  [W2.0 K30](#w20-k30-the-linearity-test-on-a-coarse-clock)).
- Time: `BenchmarkDecode` (the production decode, warm pool, fresh result,
  interned) and `BenchmarkDecodeNaiveSonic` (owner decision G3:
  `sonic.Unmarshal` into a `map[string]any`), `for b.Loop()`,
  `-count=5`, medians. The comparator has no row for
  `parity-big-exp-unknown`: decoding into a map, sonic refuses `1e400` on
  both architectures ("float infinity" on arm64, "float number is infinity"
  on amd64), where the SDK and the Python SDK accept it.
- Load (R17): (M) allocation rows at 20.95 (16 cores); **the (M) timing row
  is noisy**, 176.97 at its start after the runner's five waits of 60 s
  (other lanes were building and testing), so its times are recorded, not of
  record, and a re-run on the lead's "M QUIET" is owed. (L) 0.06 → 1.06 on
  44 cores.

### W2.0 findings

1. **AC-P2 holds on both hosts, and every count is identical on (M) and
   (L).** Each fixture is at its frozen budget, or one allocation below it.
   Six fixtures are one below: `duplicates`, `escaped-member-names`,
   `structured-legend`, `deviation-lone-surrogate` and the two floods, each
   of which holds a structured level. Their frozen numbers include the one
   arena that copied structured levels, and a level equal to the question's
   compact JSON is now the question's bytes. Since cf16992 (landed as
   78d2954) the counts are pinned exactly (ruling R70), and the pins held
   on both hosts after the review fix pass (the depth cap, the spelling
   check and the zero rule add no allocation). The
   plain 3-answer fixture takes 4 allocations: the target of 4 and the
   ceiling of 8.
2. **Misses cost one allocation per decode**, whatever their number: every
   fixture decoded without a question set costs its interned count + 1 (the
   arena; `no-answers` 0 → 1 for the model), and more bytes. This is the
   price of "a result never aliases the body" when the server echoes strings
   the request did not send. It is recorded, not budgeted.
3. **AC-P8 holds.** Lazy-pass allocations are 86 at 1 011 members and 681 at
   10 011 (bounds 89 and 689), ratio 7.92 (bound 12). They equal S-D1's 87
   and 682 minus the arena, which moved to interning. The whole decode takes
   90 and 685 allocations, ratio 7.61 (bound 12). The 10⁴ : 10³ time ratio is
   10.12 on (M) and 9.98 on (L) (bound 15).
4. **AC-P2's "≤ 0.5 × naive", which applies to the plain 3-answer fixture,
   holds in allocations**: 4 against 26 (M) and 40 (L), 0.15 and 0.10. W5.1
   owns the clause and its comparator package. The other fixtures are
   reported, as the plan says. The ratio is 0.03 to 0.29 for the rest,
   except for three fixtures above 0.5 on (M):
   `escaped-member-names` 29 / 41 = 0.71, `deviation-lone-surrogate`
   13 / 24 = 0.54, and on (L) `escaped-member-names` 29 / 51 = 0.57. Their
   cost is the lazy pass and sonic's unquoting of escaped keys (NF2). The
   naive comparator itself allocates differently by architecture (26 on
   arm64, 40 on amd64 for `result.json`).
5. **K23 stands on arm64: the decode is slower than sonic's generic decode.**
   On (L), with a quiet host, `result.json` takes 1.20 × the naive time (0.88
   to 2.61 across fixtures; the floods 1.02 to 1.04). On (M) the ratio is
   2.23 for `result.json` (1.35 to 4.06), in a noisy row: S-D1 measured 2.01
   there. W5.3's target is ≤ 1.5 × on (M) (K23).

<a id="w20-tables"></a>

### W2.0 tables

AC-P2 allocations per decode (`alloc-{M,L}.txt`, `bench-{M,L}.txt` for the naive column):

| Fixture | Body bytes | Frozen budget | Allocations (M) | Allocations (L) | Bytes | Without questions (allocs/bytes) | Naive allocs/op (M / L) |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `result` | 364 | 4 | 4 | 4 | 688 | 5/752 | 26 / 40 |
| `type-last` | 364 | 4 | 4 | 4 | 688 | 5/752 | 26 / 40 |
| `duplicates` | 825 | 17 | 16 | 16 | 5312 | 17/5408 | 55 / 89 |
| `result-20` | 2253 | 24 | 24 | 24 | 6008 | 25/6520 | 94 / 214 |
| `score-flood-mini` | 1495 | 21 | 21 | 21 | 4184 | 22/4312 | 96 / 137 |
| `escaped-names` | 481 | 10 | 10 | 10 | 1224 | 11/1304 | 38 / 57 |
| `escaped-member-names` | 425 | 30 | 29 | 29 | 5080 | 30/5192 | 41 / 51 |
| `structured-legend` | 217 | 12 | 11 | 11 | 4464 | 12/4528 | 24 / 27 |
| `deviation-lone-surrogate` | 219 | 14 | 13 | 13 | 4544 | 14/4592 | 24 / 27 |
| `unknown-answer-type` | 145 | 1 | 1 | 1 | 144 | 2/160 | 18 / 20 |
| `parity-big-exp-unknown` | 135 | 1 | 1 | 1 | 144 | 2/160 | no row / no row |
| `no-answers` | 67 | 0 | 0 | 0 | 0 | 1/16 | 12 / 9 |
| `structured-legend-flood-1k` | 57418 | 91 | 90 | 90 | 172936 | 91/213896 | 2036 / 6574 |
| `structured-legend-flood-10k` | 616421 | 686 | 685 | 685 | 1604536 | 686/2005944 | 20095 / 65193 |

AC-P8's lazy pass alone (`alloc-codec-{M,L}.txt`; the escaped keys are counted in `TestLazyPassAllocations`):

| Fixture | Members iterated | Lazy-pass allocations (M = L) | Bytes | Bound 20 + ⌈members/15⌉ + escaped keys + 1 |
| --- | --- | --- | --- | --- |
| `escaped-member-names` | 12 | 13 | 4320 | 27 |
| `duplicates` | 18 | 9 | 4272 | 24 |
| `deviation-lone-surrogate` | 11 | 8 | 4256 | 22 |
| `structured-legend-flood-1k` | 1011 | 86 | 106904 | 89 |
| `structured-legend-flood-10k` | 10011 | 681 | 956872 | 689 |
| `structured-legend` | 10 | 8 | 4256 | 22 |

Decode time, medians of five, against the sonic-naive comparator (`bench-{M,L}.txt`, `benchstat-{M,L}.txt`; (M) noisy):

| Fixture | (M) decode | (M) naive | (M) ratio | (L) decode | (L) naive | (L) ratio |
| --- | --- | --- | --- | --- | --- | --- |
| `result` | 3.323 µs | 1.49 µs | 2.23 | 3.321 µs | 2.764 µs | 1.20 |
| `type-last` | 3.285 µs | 1.413 µs | 2.32 | 3.338 µs | 2.805 µs | 1.19 |
| `duplicates` | 12.28 µs | 3.262 µs | 3.76 | 10.93 µs | 6.743 µs | 1.62 |
| `result-20` | 21.74 µs | 7.271 µs | 2.99 | 21.25 µs | 16.64 µs | 1.28 |
| `score-flood-mini` | 13.75 µs | 5.67 µs | 2.42 | 13.87 µs | 11.78 µs | 1.18 |
| `escaped-names` | 4.867 µs | 1.981 µs | 2.46 | 4.911 µs | 4.047 µs | 1.21 |
| `escaped-member-names` | 6.609 µs | 1.627 µs | 4.06 | 7.186 µs | 3.371 µs | 2.13 |
| `structured-legend` | 4.053 µs | 1.011 µs | 4.01 | 4.647 µs | 1.926 µs | 2.41 |
| `deviation-lone-surrogate` | 4.209 µs | 1.07 µs | 3.93 | 5.044 µs | 1.935 µs | 2.61 |
| `unknown-answer-type` | 1.371 µs | 825.1 ns | 1.66 | 1.285 µs | 1.346 µs | 0.95 |
| `parity-big-exp-unknown` | 1.274 µs | no row | - | 1.198 µs | no row | - |
| `no-answers` | 575.2 ns | 426 ns | 1.35 | 525.1 ns | 598.2 ns | 0.88 |
| `structured-legend-flood-1k` | 667.6 µs | 202.8 µs | 3.29 | 616.9 µs | 603.9 µs | 1.02 |
| `structured-legend-flood-10k` | 6.828 ms | 1.722 ms | 3.97 | 6.01 ms | 5.804 ms | 1.04 |

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W2.0-01 | 2026-09-26 01:00:42 JST | W2.0 AC-P2, AC-P8 decode allocations and linearity | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 20.95 → 20.95 | `BASE=$BASE GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M -count=1 -run '^(TestAllocDecodeFixtures\|TestLinearityFlood)$' -v .` | every fixture within its frozen budget; time ratio 10.12, allocation ratio 7.61; [W2.0 tables](#w20-tables) | `results/alloc-M.txt` |
| W2.0-02 | 2026-09-26 01:00:42 JST | W2.0 AC-P8 lazy pass | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 20.95 → 20.95 | `BASE=$BASE GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-codec-M -count=1 -run '^TestLazyPassAllocations$' -v ./internal/codec/` | 86 / 681 allocations at 1 011 / 10 011 members, ratio 7.92 | `results/alloc-codec-M.txt` |
| W2.0-03 | 2026-09-26 01:08:33 JST | W2.0 decode vs sonic-naive time | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 176.97 → 21.75 **noisy** | `BASE=$BASE GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $O $SP/bench.lock bench-M -run '^$' -bench '^Benchmark(Decode\|DecodeNaiveSonic)$' -benchmem -count=5 ./internal/codec/` | `result.json` 3.323 µs against 1.490 µs (2.23 ×); [W2.0 tables](#w20-tables) | waited 5 × 60 s, then ran; re-run owed on "M QUIET"; `results/bench-M.txt`, `results/benchstat-M.txt` |
| W2.0-04 | 2026-09-25 16:11:04 UTC | W2.0 AC-P2, AC-P8 decode allocations and linearity | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.06 → 0.06 | `BASE=$BASE sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L -count=1 -run '^(TestAllocDecodeFixtures\|TestLinearityFlood)$' -v .` | identical to W2.0-01 in every count; time ratio 9.98 | `results/alloc-L.txt` |
| W2.0-05 | 2026-09-25 16:11:05 UTC | W2.0 AC-P8 lazy pass | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.06 → 0.06 | `BASE=$BASE sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-codec-L -count=1 -run '^TestLazyPassAllocations$' -v ./internal/codec/` | identical to W2.0-02 | `results/alloc-codec-L.txt` |
| W2.0-06 | 2026-09-25 16:11:05 UTC | W2.0 decode vs sonic-naive time | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.06 → 1.06 | `BASE=$BASE MAXLOAD=44 sh $R '(L)' $O /tmp/ts-spike/bench.lock bench-L -run '^$' -bench '^Benchmark(Decode\|DecodeNaiveSonic)$' -benchmem -count=5 ./internal/codec/` | `result.json` 3.321 µs against 2.764 µs (1.20 ×); [W2.0 tables](#w20-tables) | `results/bench-L.txt`, `results/benchstat-L.txt` |
| W2.0-07 | 2026-09-26 01:12:00 JST | W2.0 fuzz, 30 s each | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 55.67 → 175.45 | `GOEXPERIMENT=nosimd,noruntimesecret go test -run '^$' -fuzz '^<target>$' -fuzztime 30s ./internal/codec/` | `FuzzDecodeResponse` 1 691 917 execs, `FuzzErrorBody` 347 194, `FuzzAppendJSON` (R44 differential) 2 507 433; no failure | not a measurement; exec counts depend on load; `results/fuzz-M.txt` |
| W2.0-08 | 2026-09-26 02:48:37 JST | W2.0 K30 AC-P8 linearity in spans, ×20 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 4.66 → 5.11 | `BASE=0dce9d6+k30 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $O $SP/bench.lock m-k30-linearity -count=20 -run '^TestLinearityFlood$' -v .` | 20/20 PASS; time ratio 9.40–9.65 (median 9.55); per decode 664.1–679.4 µs (10³) and 6.349–6.460 ms (10⁴); 342–377 and 36–40 decodes per span; allocation ratio 7.61 (90 → 685) in 20/20 | [W2.0 K30](#w20-k30-the-linearity-test-on-a-coarse-clock); base 0dce9d6 plus the K30 change; `results/m-k30-linearity.txt` |
| W2.0-09 | 2026-09-25 17:48:47 UTC | W2.0 K30 AC-P8 linearity in spans, ×20 | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.00 → 0.60 | `BASE=0dce9d6+k30 sh $R '(L)' $O /tmp/ts-spike/bench.lock l-k30-linearity -count=20 -run '^TestLinearityFlood$' -v .` | 20/20 PASS; time ratio 9.20–9.26 (median 9.23); per decode 632.5–634.4 µs (10³) and 5.832–5.861 ms (10⁴); 381–396 and 43 decodes per span; allocation ratio 7.61 (90 → 685) in 20/20 | the tree copied to `/tmp/ts-spike/w20cifix/wt-w2.0-cifix`, removed after; `results/l-k30-linearity.txt` |

### W2.0 K30: the linearity test on a coarse clock

CI run 36167751804 on `0dce9d6` failed `TestLinearityFlood` on
windows-2025 only: `LINEARITY 1k 0s 90 allocs, 10k 4.0323ms 685 allocs,
time ratio +Inf (bound 15)`. The test took each flood's time as the
minimum of 21 single decodes. On Windows `time.Now` advances in ticks
(about 15.6 ms by default), so a 10³ decode, under a millisecond, that
starts and ends within one tick measures 0 s, and the minimum picks it.
The 10⁴ minimum, 4.0323 ms, is not a multiple of 15.6 ms, so that runner's
clock was finer than the default at the time; the fix does not depend on
the tick's size.

The test now times spans. A span repeats one flood's decode until at least
250 ms have elapsed on the host's clock, and a decode's time is the span's
length divided by its decodes, the minimum over five spans per flood. At a
15.6 ms tick a span covers at least 16 ticks, so its reading is within
about 6% of its length. The count is adaptive because a fixed count would
have to be sized for the slowest runner. The two floods' spans alternate,
so a change in the host's load reaches both; on (M), spans in blocks per
flood gave 9.42–9.56 and alternating spans 9.50–9.56 (three runs each,
unlocked, load 6.39), so the order does not bias the ratio. The collector
stays off, as `QuietRuntime` sets it, and runs once before each span to
free the last span's garbage (about 65 MB per span on (M) and (L)). A
span that measures 0 s fails the test, naming the flood, the span and its
decode count. The allocation half is
unchanged. Two throwaway mutants fail the test on (M): a 10⁴ decode run
twice for each one counted gives ratio 19.15, and a clock that never
advances ends the first span at the decode cap with that diagnostic.

The spans read lower than the single-decode minimum: 9.40–9.65 on (M) and
9.20–9.26 on (L) over 20 runs each (W2.0-08, -09), against 9.98–10.33 for
the minimum of 21 single decodes (W2.0-01, -04 and the W2.0 review's
runs). The single 10³ minimum was further below its flood's typical decode
than the 10⁴ minimum was: on (L), 569.6 µs against 632.5–634.4 µs per
decode in a span, and 5.683 ms against 5.832–5.861 ms. The bound of 15 is
unchanged. The test takes about 2.6 s instead of 0.2 s. No other test in
the root or `internal/codec` forms a ratio of measured durations; the one
other duration there, `TestDecodeDepthBound`'s elapsed time
(`stackGrowth`), is compared only against a 2 s ceiling, which one tick
cannot cross. CI on the three images after landing is the proof on
Windows.

## W2.2: `internal/h2gate` (AC-P4, K21b, K22)

Code: `internal/h2gate`, the production gate, token and default transport
of section 6.3 under option (iv-b) (G2, R29, R29b, R29c), with
`internal/testsupport`'s `H2Conn.SetMaxConcurrentStreams` for K21b. The rows
ran these trees. The branch was rebased onto `b88758c` afterwards and
`git range-diff` shows every commit unchanged, so the shas below are the
rebased ones, with the one each row actually ran in brackets: W2.2-01 to -08
`8c6e770` (`9c9a2b3`); W2.2-09 `af23212` (`de2e67d`); W2.2-10 to -15
`42b1253` (`6d3f451`, the R67 fix; -10 to -13 ran its code before it was
committed, -13 in an instrumented copy); W2.2-16 and -17 `9621f0e`
(`a81b6cd`, R69, run before it was committed). The rows run the committed
tests: `TestFanOut` (AC-P4: 10 bursts a run, each on a fresh server and
transport), `TestTokenResidualK21` (K21b) and `TestRecordFanOut` (K22 and the
negative control; it runs only with `H2GATE_RECORD=1` and asserts nothing).
They print `RESULT` lines through `t.Log`; the numbers below are
`_spikes/s-t/median.py` over those lines (`sed -n 's/.*RESULT /RESULT /p'`
first), as median (min-max) over the runs of a row.

Row commands use `R=_spikes/s-t/run.sh` (the W0.4 runner),
`C=_spikes/w2.2/contend.sh` (the same header and footer, with `LOOPS`
`nice -n 19 yes` loops started and killed inside the lock; the footer checks
that `pgrep -x yes` prints nothing), `MO=_spikes/w2.2/results`, and
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/c8084031-5323-4873-8c36-a19f65c9e6ff/scratchpad`;
on (L) the tree is copied with the section 11 tar pipe to
`/tmp/ts-spike/w22/src/wt-w2.2`, `LO=/tmp/ts-spike/w22/results`, with the
W0.4 environment and no `GOEXPERIMENT`. Every row holds the shared lock. The
contention rows hold it too, although they measure no time: their loops load
the whole host, and no other lane's timing run may overlap them.

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W2.2-01 | 2026-09-26 00:45:18 JST | W2.2 AC-P4 `TestFanOut` ×5 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.64 → 5.76 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-fanout -count=5 -run '^TestFanOut$' -v ./internal/h2gate/` | cold 64 → 1 connection in 50/50 bursts; warm → 0 new in 50/50; ordering (R29c, 20 ms leader delay) 50/50; 200 vs 8: 200/200 within 2 s on 1 connection in 50/50, wall p50 311.4 (310.8-317.0) ms, refused streams 0 (0-2) per run; waiter wire p50 23.7 (23.4-24.5) / p99 25.4 (24.5-28.3) ms; leader's first response byte → first waiter HEADERS p50 0.068 (0.035-0.077) ms | `results/m-fanout.txt` |
| W2.2-02 | 2026-09-25 15:45:19 UTC | W2.2 AC-P4 `TestFanOut` ×5 | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.50 → 1.17 | `MAXLOAD=44 sh $R '(L)' $LO /tmp/ts-spike/bench.lock l-fanout -count=5 -run '^TestFanOut$' -v ./internal/h2gate/` | cold 64 → 1 in 50/50; warm → 0 in 50/50; ordering 50/50; 200 vs 8: 200/200 in 50/50, wall p50 271.1 (270.5-271.3) ms, refused 0 (0-0); waiter wire p50 21.7 (21.6-21.9) / p99 22.9 (22.6-24.5) ms; first response byte → first waiter HEADERS p50 0.047 (0.045-0.061) ms | `results/l-fanout.txt` |
| W2.2-03 | 2026-09-26 00:45:36 JST | W2.2 K22 and negative control ×5 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 5.76 → 4.26 | `H2GATE_RECORD=1 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-record -count=5 -run '^TestRecordFanOut$' -v ./internal/h2gate/` | K22, 64 cold calls at 1 s service (3 bursts a run): FirstHold wall p50 2006.9 (2006.6-2010.3) ms, leader done 1003.9, waiters done p50 2006.5 / p99 2012.6 ms; plain token wall 1004.7 (1004.6-1007.3) ms; 1 connection each. Negative control (plain token, 20 ms leader delay): ordering 0/50, check (b) failed 50/50 | `results/m-record.txt` |
| W2.2-04 | 2026-09-25 15:45:35 UTC | W2.2 K22 and negative control ×5 | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.17 → 0.55 | `H2GATE_RECORD=1 MAXLOAD=44 sh $R '(L)' $LO /tmp/ts-spike/bench.lock l-record -count=5 -run '^TestRecordFanOut$' -v ./internal/h2gate/` | K22: FirstHold wall p50 2003.2 (2003.1-2003.9) ms, leader done 1001.4, waiters done p50 2002.9 / p99 2003.7 ms; plain token wall 1003.1 (1002.7-1003.3) ms. Negative control: ordering 0/50, (b) failed 50/50 | `results/l-record.txt` |
| W2.2-05 | 2026-09-26 00:46:22 JST | W2.2 contention: `TestFanOut`, `TestTokenResidualK21` ×20 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 4.26 → 394.21 (noisy by design) | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $C '(M)' $MO $SP/bench.lock m-contention 16 -count=20 -run '^(TestFanOut|TestTokenResidualK21)$' -v ./internal/h2gate/` | PASS, 144.6 s: ordering 200/200 bursts, 200 vs 8 200/200 in 200 reps (refused 0-4 per run); K21b: no call past deadline + 100 ms in 60 runs, every error a timeout, probe HEADERS ≤ 0.259 ms, accepts ≤ 2, fresh bursts 200/200 in 60/60 | 16 `nice -n 19 yes` loops inside the lock; `results/m-contention.txt` |
| W2.2-06 | 2026-09-25 15:46:21 UTC | W2.2 contention ×20 | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.55 → 42.18 (noisy by design) | `sh $C '(L)' $LO /tmp/ts-spike/bench.lock l-contention 44 -count=20 -run '^(TestFanOut|TestTokenResidualK21)$' -v ./internal/h2gate/` | PASS, 186.0 s: ordering 200/200; 200 vs 8 200/200 in 200 reps, wall p50 284.2 ms but max 1278 ms, refused 12-77 per run of 10; K21b: late 0 in 60 runs, fresh 200/200 in 60/60, probe ≤ 0.215 ms | 44 loops; `results/l-contention.txt` |
| W2.2-07 | 2026-09-26 00:48:52 JST | W2.2 contention, `-race` ×5 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 394.21 → 402.44 (noisy by design) | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $C '(M)' $MO $SP/bench.lock m-contention-race 16 -race -count=5 -run '^(TestFanOut|TestTokenResidualK21)$' -v ./internal/h2gate/` | PASS, 37.9 s, no race: ordering 50/50; 200 vs 8 200/200 in 50 reps; K21b late 0 in 15 runs, fresh 200/200 | `results/m-contention-race.txt` |
| W2.2-08 | 2026-09-25 15:49:33 UTC | W2.2 contention, `-race` ×5 | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 42.18 → 43.28 (noisy by design) | `sh $C '(L)' $LO /tmp/ts-spike/bench.lock l-contention-race 44 -race -count=5 -run '^(TestFanOut|TestTokenResidualK21)$' -v ./internal/h2gate/` | PASS, 48.8 s, no race: ordering 50/50; 200 vs 8 200/200 in 50 reps (wall max 1282 ms, refused 39-60 per run); K21b late 0 in 15 runs, probe ≤ 1.043 ms, fresh 200/200 | `results/l-contention-race.txt` |
| W2.2-09 | 2026-09-25 16:00:01 UTC | W2.2 R67 fix, before: 200 vs 8 under contention | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.07 → 36.72 (noisy by design) | `sh $C '(L)' $LO /tmp/ts-spike/bench.lock l-fix-before 44 -count=20 -run '^TestFanOut$' -v ./internal/h2gate/` (tree `af23212`, was `de2e67d`) | PASS, 97.8 s: 200 vs 8 200/200 in 200 reps, refused streams 915 in all (19-75 per run of 10), wall max 1285 ms; ordering 200/200 | `results/l-fix-before.txt` |
| W2.2-10 | 2026-09-25 16:02:33 UTC | W2.2 R67 fix, after | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 17.32 → 35.86 (noisy by design) | `sh $C '(L)' $LO /tmp/ts-spike/bench.lock l-fix-after 44 -count=20 -run '^TestFanOut$' -v ./internal/h2gate/` (tree `42b1253`, uncommitted then) | PASS, 60.8 s: 200 vs 8 200/200 in 200 reps, refused streams 0 in every run, wall max 284 ms; ordering 200/200 | `results/l-fix-after.txt` |
| W2.2-11 | 2026-09-26 01:02:33 JST | W2.2 R67 fix: `internal/testsupport` ×30 under contention | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 126.61 → 217.20 (noisy) | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $C '(M)' $MO $SP/bench.lock m-fix-testsupport 16 -count=30 ./internal/testsupport/` | PASS, 19.9 s | the host was loaded by other lanes before the loops started; `results/m-fix-testsupport.txt` |
| W2.2-12 | 2026-09-26 01:02:58 JST | W2.2 R67 fix: `internal/h2gate` ×20 under contention | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 217.20 → 458.54 (noisy) | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $C '(M)' $MO $SP/bench.lock m-fix-h2gate 16 -count=20 -v ./internal/h2gate/` | PASS, 209.0 s, 2300 test and subtest passes, no failure; 200 vs 8 refused 0 in all 20 runs (wall max 372 ms); K21b late 0 in 60 runs; GOAWAY runs with refusals 10 of 20 | `results/m-fix-h2gate.txt` |
| W2.2-13 | 2026-09-26 01:07:38 JST | W2.2 K21b GOAWAY scenario, instrumented copy | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 227.89 → 257.31 (noisy) | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $C '(M)' <lane scratchpad> $SP/bench.lock instr-out 16 -count=12 -run 'TestTokenResidualK21/success:_GOAWAY' -v ./internal/h2gate/` in a copy whose test logs the refused requests per connection | 12 runs: 8 without a refusal; 4 with 1, 27, 56 and 63 refusals, all on connection 1 (the re-dial), the first at the 9th request on it | throwaway test change, not committed; `results/m-k21-goaway-instr.txt` |
| W2.2-14 | 2026-09-26 01:11:18 JST | W2.2 R69, before: K21b GOAWAY scenario under contention | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 20.32 → 55.67 (noisy) | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $C '(M)' <lane scratchpad> $SP/bench.lock m-k21c-before 16 -count=20 -run 'TestTokenResidualK21/success:_GOAWAY' -v ./internal/h2gate/` (tree `42b1253`, was `6d3f451`, a detached worktree) | PASS, 32.5 s: 72/72 in 7 of 20 runs; refused streams 526 in 13 runs (10-63 each), the other calls timed out in the retry backoff | `results/m-k21c-before.txt` |
| W2.2-15 | 2026-09-25 16:13:48 UTC | W2.2 R69, before | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.06 → 13.25 (noisy) | `sh $C '(L)' $LO /tmp/ts-spike/bench.lock l-k21c-before 44 -count=20 -run 'TestTokenResidualK21/success:_GOAWAY' -v ./internal/h2gate/` (tree `42b1253`, was `6d3f451`) | PASS, 11.5 s: 72/72 in 17 of 20 runs; refused streams 102 in 3 runs (11, 28, 63) | `results/l-k21c-before.txt` |
| W2.2-16 | 2026-09-26 01:14:31 JST | W2.2 R69, after | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 67.54 → 64.38 (noisy) | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $C '(M)' <lane scratchpad> $SP/bench.lock m-k21c-after 16 -count=20 -run 'TestTokenResidualK21/success:_GOAWAY' -v ./internal/h2gate/` (tree `9621f0e`, uncommitted then) | PASS, 6.4 s: 72/72 in 20 of 20 runs, refused streams 0; SettleHolds 1 in 19 runs, 0 in 1 | `results/m-k21c-after.txt` |
| W2.2-17 | 2026-09-25 16:14:31 UTC | W2.2 R69, after | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 8.72 → 14.15 (noisy) | `sh $C '(L)' $LO /tmp/ts-spike/bench.lock l-k21c-after 44 -count=20 -run 'TestTokenResidualK21/success:_GOAWAY' -v ./internal/h2gate/` (tree `9621f0e`, uncommitted then) | PASS, 5.5 s: 72/72 in 20 of 20 runs, refused streams 0; SettleHolds 1 in 18 runs, 0 in 2 | `results/l-k21c-after.txt` |
| W2.2-18 | 2026-09-26 01:58:04 JST | W2.2 R72b: the replay's response clears its mark | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 4.73 → 14.13 (noisy) | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $C '(M)' $MO $SP/bench.lock m-k21c-r72b 16 -count=20 -run 'TestTokenResidualK21/success:_GOAWAY' -v ./internal/h2gate/` (the R72b commit's tree, uncommitted then) | PASS, 6.2 s: 72/72 in 20 of 20 runs, refused streams 0; SettleHolds 1 in 18 runs, 0 in 2 (W2.2-16: 19 and 1) | `results/m-k21c-r72b.txt` |
| W2.2-19 | 2026-09-25 16:58:04 UTC | W2.2 R72b | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.15 → 6.89 (noisy) | `TREE=92ae350+r72b sh $C '(L)' $LO /tmp/ts-spike/bench.lock l-k21c-r72b 44 -count=20 -run 'TestTokenResidualK21/success:_GOAWAY' -v ./internal/h2gate/` | PASS, 5.6 s: 72/72 in 20 of 20 runs, refused streams 0; SettleHolds 1 in 19 runs, 0 in 1 (W2.2-17: 18 and 2) | `results/l-k21c-r72b.txt` |
| W2.2-20 | 2026-09-26 02:17:20 JST | W2.2 K29 CI fix: the three CI failures, -race, 2 Ps, under contention | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 12.40 → 36.94 (noisy by design) | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $C '(M)' $MO $SP/bench.lock m-k29-race 16 -race -count=20 -cpu 2 -run '^(TestFanOut|TestWaiterFallThrough|TestALPNHTTP1Only)$' -v ./internal/h2gate/` (the K29 commit's tree, uncommitted then) | PASS, 91.7 s: the three tests 20/20, ordering 10/10 in all 20 runs | `results/m-k29-race.txt` |
| W2.2-21 | 2026-09-25 17:19:14 UTC | W2.2 K29 CI fix | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.00 → 35.97 (noisy by design) | `TREE=7fd43ce+k29 sh $C '(L)' $LO /tmp/ts-spike/bench.lock l-k29-race 44 -race -count=20 -cpu 2 -run '^(TestFanOut|TestWaiterFallThrough|TestALPNHTTP1Only)$' -v ./internal/h2gate/` | PASS, 88.5 s: the three tests 20/20, ordering 10/10 in all 20 runs | `results/l-k29-race.txt` |
| W2.2-22 | 2026-09-25 22:26:31 UTC | W2.2 F-3 (R86), before: `TestProxyModes` under contention | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.25 → 18.74 (44 loops; the 1-min average lags a 23 s run) | `TREE=45fd018 sh $C '(L)' $FO /tmp/ts-spike/bench.lock l-flake-before-proxy 44 -race -count=200 -cpu 1,4 -run '^TestProxyModes$' -v ./internal/testsupport/` (the old tests) | FAIL, 23.0 s: 30 of 400 runs, every one in the strict-ALPN case at `proxy_test.go:71` (`HandshakeErr` empty); the other four cases 400/400 | `results/l-flakefix-before-proxy.txt` (the host's `l-flake-before-proxy.txt`) |
| W2.2-23 | 2026-09-25 22:27:11 UTC | W2.2 F-3, before: `TestTokenResidualK21` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 16.00 → 44.39 (noisy by design) | `TREE=45fd018 sh $C '(L)' $FO /tmp/ts-spike/bench.lock l-flake-before-k21 44 -race -count=60 -cpu 1,4 -run '^TestTokenResidualK21$' -v ./internal/h2gate/` (the old tests) | PASS, 355.4 s: 0 of 120 runs failed; each scenario 120/120 with late 0, bad 0, fresh 200/200 and at most 2 accepts; GOAWAY 72 ok and 0 refused in all 120 | `results/l-flakefix-before-k21.txt` (the host's `l-flake-before-k21.txt`) |
| W2.2-24 | 2026-09-25 22:46:07 UTC | W2.2 F-3, after: both tests under contention | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 31.81 → 44.20 (noisy by design) | `TREE=a885fee sh $C '(L)' $FO /tmp/ts-spike/bench.lock l-flakefix-after-two 44 -timeout 60m -race -count=200 -cpu 1,4 -run '^(TestProxyModes\|TestTokenResidualK21)$' -v ./internal/testsupport/ ./internal/h2gate/` | PASS: `TestProxyModes` 400/400 (24.3 s), `TestTokenResidualK21` 400/400 (1182.7 s); each scenario 400/400 with late 0, bad 0, fresh 200/200 and at most 2 accepts; GOAWAY 72 ok and 0 refused in all 400; `restore_skipped` 0 in all 1200 | `results/l-flakefix-after-two.txt` |
| W2.2-25 | 2026-09-26 07:46:11 JST | W2.2 F-3, after | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 20.07 → 29.41 (noisy by design) | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$FL TREE=a885fee $FL $SPV/bench.lock sh $C '(M)' $MO $SP/bench.lock m-flakefix-after-two 16 -timeout 60m -race -count=200 -cpu 1,4 -run '^(TestProxyModes\|TestTokenResidualK21)$' -v ./internal/testsupport/ ./internal/h2gate/` | PASS: `TestProxyModes` 400/400 (24.4 s), `TestTokenResidualK21` 400/400 (1235.6 s); each scenario 400/400 with late 0, bad 0, fresh 200/200 and at most 2 accepts; GOAWAY 72 ok and 0 refused in 399 runs, one run 26 ok and 46 timeouts with 9 refused (the K21c residual, SettleHolds 1; recorded, not asserted); `restore_skipped` 0 in all 1200 | `results/m-flakefix-after-two.txt` |
| W2.2-26 | 2026-09-25 23:05:55 UTC | W2.2 F-3, after: both packages under contention (the verifier's F-3 run) | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 44.20 → 44.79 (noisy by design) | `TREE=a885fee sh $C '(L)' $FO /tmp/ts-spike/bench.lock l-flakefix-after-pkgs 44 -timeout 60m -race -count=30 -cpu 1,4 ./internal/h2gate/ ./internal/testsupport/` | PASS, exit 0: `internal/h2gate` 622.6 s, `internal/testsupport` 107.9 s, every test 60 times (the verifier's run at 45fd018 failed 2 + 2) | `results/l-flakefix-after-pkgs.txt` |
| W2.2-27 | 2026-09-25 23:16:24 UTC | W2.2 F-3, after: both packages without load | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 44.79 → 0.08 (W2.2-26's 1-min average decaying; no loop ran) | `TREE=a885fee sh $C '(L)' $FO /tmp/ts-spike/bench.lock l-flakefix-after-pkgs-quiet 0 -timeout 60m -race -count=30 -cpu 1,4 ./internal/h2gate/ ./internal/testsupport/` | PASS, exit 0: `internal/h2gate` 585.8 s, `internal/testsupport` 80.6 s | `results/l-flakefix-after-pkgs-quiet.txt` |
| W2.2-28 | 2026-09-26 08:08:20 JST | W2.2 F-3: the injected-delay proof, I1 to I3 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.96 → 6.46 | `GOEXPERIMENT=nosimd,noruntimesecret $FL $SPV/bench.lock $FL $SP/bench.lock sh _spikes/w2.2/flakefix-inject.sh '(M)' $MO $SP/bench.lock 45fd018 a885fee` (its first two runs) | the old tests at 45fd018: strict-ALPN case 0/20 (`proxy_test.go:71`, `HandshakeErr` empty), GOAWAY scenario 0/20 (`token_test.go:181`: `testsupport: connection closed: use of closed network connection`, the verifier's failure); a885fee: 20/20 and 20/20, `restore_skipped=1` in all 20 | `results/m-flakefix-inject-before.txt`, `results/m-flakefix-inject-after.txt` |
| W2.2-29 | 2026-09-26 08:09:13 JST | W2.2 F-3: a close during the SETTINGS write, I4 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.46 → 5.19 | the W2.2-28 command (its last two runs) | a885fee: 20/20, `SetMaxConcurrentStreams` returns `testsupport: connection closing: use of closed network connection` (matches `ErrConnClosing`); mutant M2 (no check after a failed write): 0/20, `testsupport: connection closed: use of closed network connection` | `results/m-flakefix-inject-write-after.txt`, `results/m-flakefix-inject-write-m2.txt` |

### W2.2 results

AC-P4 against the frozen rows (`docs/perf/frozen-budgets.md`):

| Clause | Frozen (W0.4b) | (M) | (L) |
| --- | --- | --- | --- |
| cold 64 → 1 connection | 50/50 per host | 50/50 (W2.2-01), 200/200 and 50/50 under contention (W2.2-05, -07) | 50/50 (W2.2-02), 200/200 and 50/50 under contention (W2.2-06, -08) |
| warm → 0 | 50/50 | 50/50 | 50/50 |
| 200 vs limit 8 → 200/200 within 2 s on 1 connection (asserted) | 293.0 ms (M), 271.7 ms (L) | 200/200 in 50/50, 311.4 ms | 200/200 in 50/50, 271.1 ms |
| ordering, 20 ms leader delay, from client traces (R29c) | 50/50 at 5 ms | 50/50 | 50/50 |
| negative control, plain token (not asserted) | fails 50/50 at 5 ms | fails (b) 50/50 | fails (b) 50/50 |
| waiter latency (recorded) | wire p50 1.064 / p99 1.928 ms (M), 1.451 / 2.537 ms (L) at 0 ms delay | wire p50 23.7 / p99 25.4 ms | wire p50 21.7 / p99 22.9 ms |

The waiter wire time now includes the 20 ms the ordering test holds the
leader's response: under FirstHold no waiter writes before that response
arrives, which is what the clause asserts. What the token itself adds is the
hand-off from the leader's first response byte to the first waiter's HEADERS:
p50 0.068 ms (M) and 0.047 ms (L). The 200-vs-8 wall on (M) is 6 % above
W0.4b's, at a lower load (6.6 against 9.8); it is recorded, not a budget.

K22 (W2.2-03, -04): a cold burst of 64 at 1 s of service time costs one
service time more under FirstHold, as W0.4b measured at 50 and 200 ms: 2006.9
against 1004.7 ms (M), 2003.2 against 1003.1 ms (L). The leader is answered
at about 1 s and the 63 waiters at about 2 s, all on one connection.

K21b (`TestTokenResidualK21`; contention rows W2.2-05 to -08, 50 runs a
scenario in all): no call returned past its 2 s deadline + 100 ms, every error
was a timeout (no connection, configuration or proxy class), the token was
free after every burst (probe HEADERS at most 1.043 ms, against the 50 ms
bound), the server accepted at most 2 connections, and every fresh 200-vs-8
burst afterwards succeeded 200/200. REFUSED_STREAM above 4 ends with
deadline misses in every run (5-43 of 72 calls succeed): the stock
transport retries a refused stream at once, then after 1 s and 2 s of
backoff (`internal/http2/transport.go:417-446`), so the second refusal of a
call outlives its deadline. The GOAWAY scenario completed 72/72 in 12 of 20
runs on (M) and 13 of 20 on (L) under contention, 4 of 5 on each host under
`-race`, and in every unloaded run; in the others 16-63 calls ran out of
their deadline in that backoff. SETTINGS 8 → 2 completed 72/72 in 48 of 50
contention runs.

Why the server refuses under load: the loopback server writes a stream's
END_STREAM (`internal/testsupport/h2conn.go:584`) before it decrements its
open-stream count (`:594`). A client that reads END_STREAM and opens its next
stream inside that window finds the server one stream over the limit, and the
window widens under CPU contention. This is where the 200-vs-8 refusals on
(L) under contention come from (12-77 per 10 bursts, with a burst's wall up
to 1.28 s: one refusal and the 1 s backoff, still inside the 2 s deadline);
unloaded, they are 0-2. It is a property of the test server, stricter than
RFC 9113 (a stream the server has ended is closed on its side when it sends
the frame); a second refusal of one call would miss the 2 s deadline, so it
is the one flake path of `TestFanOut`'s 200-vs-8 case under heavy load.
Reported to the lead (W2.2 report, question 1) and fixed under R67 (below).

### W2.2 fix: the loopback server's stream count (R67)

Ruling R67 took the fix: the loopback server now takes a stream out of its
count when the stream closes, as net/http's server does, not when the
handler goroutine gets round to it. A stream is retired, once, under the
write lock and before the frame that closes it (a RST_STREAM, or END_STREAM
after the client's END_STREAM), or by the reader when the client's
END_STREAM arrives after the server's; `ActiveStreams` lists only the
streams that count. The new subtest `TestLoopbackStreamLimit/success: a
stream stops counting before the client reads its last frame` checks, 200
times at a limit of 1, that no stream is still counted once the client has
read its last frame; against the old server it failed 3 of 50 runs unloaded.

Before and after, the same run (W2.2-09, -10): the 200-vs-8 case under 44
loops on (L) went from 915 refused streams in 200 bursts (19-75 per 10, wall
up to 1285 ms) to none (wall up to 284 ms). On (M), `internal/testsupport`
×30 and all of `internal/h2gate` ×20 pass under contention (W2.2-11, -12),
with no refused stream in the 200-vs-8 case.

The fix does not touch the K21b GOAWAY scenario's refusals, which have
another cause (W2.2-13): all of them are on the re-dialed connection, from
its 9th request on. The stock transport replays the dropped streams onto
the new connection without the token, as K21b says, so FirstHold does not
engage there; until the client has read that connection's SETTINGS it
assumes 100 streams (`internal/http2/transport.go:57,624`) and the queued
callers' HEADERS, each written as soon as it takes the token, can exceed
the server's 8. The refused ones end in the retry backoff. K21b's
assertions hold throughout (no late call, the token free, fresh bursts
200/200). A mitigation was proposed to the lead; R69 took it (below).

### W2.2 K21c: the settle hold (R69)

Ruling R69 records W2.2-13's finding as K21c and takes the mitigation. When
GotConn reports a new connection (`Reused` false) to a request that has
already given the token back (a stock replay: the transport retries inside
RoundTrip after GOAWAY or REFUSED_STREAM), the transport marks that
connection unsettled; the next request that holds the token and lands on it
keeps the token until its response headers, under the same hold bound as
FirstHold, and clears the mark (`Stats.SettleHolds`). At most 8 marks are
kept, oldest dropped first; a connection type that is not comparable is not
marked. The fake-GotConn unit test `TestSettleHold` pins the sequences
(replay then holder, two replays, a holder on a settled connection, a hold
expiry, HTTP/1.1, the bound).

The same run before and after (W2.2-14 to -17), `TestTokenResidualK21`'s
GOAWAY scenario ×20 under contention: refused streams 526 in 13 of 20 runs
(M) and 102 in 3 of 20 (L) before, none in 40 of 40 after, every run 72/72.
The run also got shorter (32.5 → 6.4 s (M), 11.5 → 5.5 s (L)), since no call
waits in the retry backoff any more. Every run shows two FirstHolds, one per
connection; in the 3 runs without a settle hold the first request on the
re-dialed connection still held the token itself (a plain FirstHold).

The cost is K22's, once per re-dial after a GOAWAY: the callers queued
behind the settle hold wait for one response on the new connection before
their HEADERS go out.

Review W2.2A MINOR 4 (R72b): a mark no holder took stayed until 8 newer
marks evicted it, so a much later holder paid a hold for nothing and a dead
connection stayed referenced. The replay that set a mark now clears it when
its own response arrives (the client has read the connection's SETTINGS by
then); `TestSettleHold` pins it. The GOAWAY scenario is unchanged by it
(W2.2-18, -19): 72/72 in 40 of 40 runs, no refusal, SettleHolds 1 in 37 of
40 runs, as in W2.2-16 and -17.

For W7 (K16, Appendix B): two proxy-path failures reach the classification
without the `proxyconnect` wrapper, and so as `DialError{Proxy: false}`: a
CONNECT the proxy refuses, whose status text the stock transport returns as
it is (`transport.go:2036-2043`, pinned by `TestProxy`), and a handshake
with an https proxy that `customDialTLS` completes for a caller TLS dialer
returning an unfinished handshake (`:1905-1910`, returned without `wrapErr`)
(review W2.2A NIT 11).

The runners now print the tree in their header line (`tree=`): `TREE` when
set (the (L) copies carry no `.git`), otherwise `git rev-parse --short HEAD`,
with `+changes` when the worktree differs from it.

### W2.2 K29: the CI failures on 7fd43ce (R75)

CI run 36165341606 on `7fd43ce` failed three `internal/h2gate` tests under
`-race`, none of them in the transport. On windows-2025 `TestFanOut`'s
ordering check (b) failed in 4 of 10 bursts, and `TestWaiterFallThrough`'s
"last waiter done before the leader" clause failed. In both, the two
stamped events carried the same `time.Now` value (`m=+0.637313201` for a
waiter's HEADERS and the leader's first response byte), although the test
makes one follow the other: Windows advances the clock in ticks. On
ubuntu-26.04 `TestALPNHTTP1Only` read the server's record of the refused
handshake before the server's `Handshake` returned
(`internal/testsupport/loopback.go:403-411`).

The tests now read the order of client-side events from a sequence number
each trace hook takes (`traceSeq`), which follows the order the hooks ran
in whatever the clock's resolution; the timestamps stay as recorded
values. R29c's client-side oracle is kept: the plain-token negative control
still fails check (b) in 10 of 10 bursts, and a FirstHold-off mutant fails
with the waiter's HEADERS about 180 events before the leader's first byte.
Lower bounds on measured durations (the TLS-silent peers and the hold
bound) allow one clock tick (`coarseClock`, 20 ms), and `TestALPNHTTP1Only`
waits for the server's record. The runs above ran the three tests with 2 Ps,
as on CI's runners, under contention (W2.2-20, -21). CI on the three images
after landing is the proof on Windows.

### W2.2 F-3: two harness races under contention (R86)

The Phase 2 verifier found two test failures under CPU contention on (L)
(finding F-3), both races in the test harness, not in the transport:

- `TestProxyModes`' strict-ALPN case read the proxy's record of the
  refused handshake before the proxy wrote it. The proxy writes the record
  after its own `Handshake` returns
  (`internal/testsupport/proxy.go:216-222`), and the client can read alert
  120 and return first: the K29 race that R75 (2) fixed for the loopback
  server's record. The case keeps the client-side oracle first (a
  proxyconnect error naming no application protocol, no connection to the
  API server) and then waits up to 5 s for the proxy's record with the
  package's `waitFor`, as `TestLoopbackALPNModes` and h2gate's
  `TestALPNHTTP1Only` do (d9bf0a9).
- `TestTokenResidualK21`'s GOAWAY scenario raised the limit back to 8 on
  every connection `LiveH2Conns` listed before its fresh burst. After
  GOAWAY the first connection closes once its last stream is done, which
  can be after every call returned, and the server lists it until its
  reader has stopped, so the SETTINGS write met a closed connection; every
  scenario assertion held in the failing runs. `SetMaxConcurrentStreams`
  now returns `ErrConnClosing` without writing when the close has begun
  (read under `wmu`, then `mu`: the D-TSflake order), and reports a failed
  write the same way when the reader's `Close`, which does not take `wmu`,
  landed during it. The test counts those skips (`restore_skipped` in its
  `RESULT` line) and fails on any other error; the SETTINGS scenario's
  8 → 2 change still fails on any error, so it must reach the live
  connection. `TestLoopbackLimitOnClosingConn` pins the skip after GOAWAY,
  `Close` and a client close, and a live connection's new limit (0cdbd83).

W2.2-22 and -23 ran the old tests at 45fd018 on (L) under 44 loops:
`TestProxyModes` failed 30 times in 400 runs (the verifier saw 4 in 200 on
(L) and 1 in 600 on (M)); `TestTokenResidualK21` did not fail in 120 runs,
as in the verifier's targeted 0 in 100: its 2 failures came from the
package-level run that W2.2-26 repeats. W2.2-24 to -27 ran a885fee (the
two fixes and the F-4 docs commit). No targeted run needed the skip
(`restore_skipped` 0 in 2400 scenario runs), so the injected-delay proof
(W2.2-28 and -29, `_spikes/w2.2/flakefix-inject.sh`: throwaway sleeps in
detached worktrees, never committed) shows each race and its fix
deterministically. I1 delays the proxy's record write by 200 ms; I2 keeps
a stopped connection listed for 500 ms, and I3 lets its close finish
before the limit is raised; I4 puts 300 ms between the closed check and
the write while a harness test closes the client side. Against I1 to I3
the old tests fail 20 of 20 with the verifier's two messages and the fixed
ones pass 20 of 20. Against I4 the fixed code reports `ErrConnClosing` 20
of 20, and mutant M2, without the check after a failed write, returns the
verifier's error 20 of 20. A mutant without the check before the write
passes all of these: every closing path it misses also fails the write
with the close already recorded, so the check after the write reports it
the same way; the check before the write leaves the limit and the socket
untouched.

The other record reads in both packages' tests were audited (grep
`HandshakeErr`, `LiveH2Conns`, `Conns()`, `Accepts()`, `Protocol`,
`Dropped`, `Connects()`): each reads a record written before the frame,
response or alert the client had already read, waits for the record, or
asserts an absence that cannot change. The one exception is ordered by
time only: the TLS-silent listener's accept count, read at least 200 ms
after the client connected, once the handshake timeout has passed. The
lane report has the table.

W2.2-25 recorded one GOAWAY run on (M), with the host's 5-minute load at
about 60, that had 26 ok, 46 timeouts and 9 refused streams, every
assertion holding: the K21c residual after R69's settle hold. It is
recorded here and reported, not changed by this fix (h2gate is
production code).

Row variables: `C=_spikes/w2.2/contend.sh`;
`FO=/tmp/ts-spike/src-p2-flakefix/out` on (L), where 45fd018 and a885fee
are copied with the section 11 tar pipe to
`/tmp/ts-spike/src-p2-flakefix/base/` and `head/`, with the W0.4
environment and no `GOEXPERIMENT`;
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/40cb0f1f-c8a9-422c-a3e8-b3afc329b5cb/scratchpad`,
the (M) lock of these rows; `SPV`, the `SP` of the rows above, whose lock
was held as well; `MO=$SP/out-M`, copied to `results/`;
`FL=/opt/homebrew/opt/util-linux/bin/flock`. The first runs of W2.2-24 and
-25, without `-timeout`, were cut by `go test`'s default 10 minutes in
`internal/h2gate` ((M) after 193 runs of `TestTokenResidualK21`, all
passing; (L) stopped by hand before it) and were run again; their files
are not kept. In W2.2-28 and -29 `contend.sh` found no `flock` binary
(`FLOCK` unset) and took no lock itself; the two outer `$FL` calls held
both (M) locks for the whole script.

## W2.3: the client (AC-P6, AC-P5)

W2.3 builds `Client.SystemOne` and `Models().List` on one attempt path
(`client.go`) over W2.2 Part B's transport: the body is encoded once per
call into a pooled `codec.Body`; each attempt copies the endpoint URL
(ruling R66, NIT 5), sends the client's immutable header template on the
first attempt (R28, R77) and a fresh map with `X-TypeSafe-Retry-Count` on
a retry, runs under its own `context.WithTimeout`, goes through
`transport.roundTrip`, and reads the response under the NF5 rules of
section 6.2.1 and R27. The read's buffers before the one that can reach
the end of the body (its declared length, or the cap) are exact powers of
two; that last buffer has one spare byte, so the read that finds EOF, or
the byte past the cap, needs no probe buffer. Measured at 504b201, the
last commit before the documents of the branch as rewritten on main's
cc585a2, where W2.2 Part B landed (review W2.3 MAJOR 1, R79); every
later commit of the branch changes documents only. The same numbers were first measured on the branch before the
rewrite (its client built its own transport), with identical allocation
counts and times within 3 %. Raw outputs are in
`_spikes/w2.3/results/`. Commands use `R=_spikes/s-c1/run.sh` (W0.5's
runner), `O=_spikes/w2.3/results`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/c8084031-5323-4873-8c36-a19f65c9e6ff/scratchpad`
and `BASE=504b201`.

### How the numbers were taken

- (M): `go1.27.1 darwin/arm64`, `GOEXPERIMENT=nosimd,noruntimesecret`,
  under `/opt/homebrew/opt/util-linux/bin/flock` on `$SP/bench.lock`.
- (L): the tree at 504b201, without `.git`, copied with the §11
  `tar | ssh 'tar -x'` pipe to `/tmp/ts-spike/w2.3-meas/wt-w2.3`;
  toolchain `/tmp/ts-spike/go/bin/go` with the §11 `GOPATH`, `GOMODCACHE`
  and `GOCACHE` under `/tmp/ts-spike` and no `GOEXPERIMENT`, under
  `flock /tmp/ts-spike/bench.lock`.
- AC-P6: `TestAllocWholeCall` (root, `//go:build !race`) is S-C1's
  `TestAllocCall` for the production client: the q3 questions, a 1 KiB
  boxed-string state, the discarding `testsupport.Recorder` given with
  `WithRoundTripper` and answering `result.json`, one attempt, no call
  options, no logger. The floor is the Recorder's round trip of a request
  built beforehand plus E_sonic (the state's encode into a presized buffer,
  `codec.EncodeState`); SDK-own is the call minus the floor. Counts are
  `runtime.ReadMemStats` deltas under `testsupport.QuietRuntime` (collector
  off, `GOMAXPROCS(1)`), the minimum that three of five runs share (section
  6.1.6); the test pins SDK-own at exactly 14 and the floor at 8/640 (R79).
  The ITEM line measures the request side's allocations one at a time, in
  the order a call makes them.
- AC-P5: `TestMemStatsCap` (root, `//go:build !race`): one whole call with
  `Retry(NoRetry())` and the default 16 MiB cap, TotalAlloc delta, with a
  collection and one small warming call before each run, outside the
  section (S-C1's harness).
- Time: `BenchmarkCall/sdk` (the plan's `call/sdk`, the same q3 shape over
  the discarding Recorder), `for b.Loop()`, `-count=10`, benchstat medians.
  `call/naive` is W5.1's comparator, so no ratio is taken here.
- Load (R17): (M) 4.91 → 4.83 on 16 cores; (L) 0.02 → 0.17 on 44.

### W2.3 findings

1. **AC-P6 holds on both hosts: SDK-own 14 allocations, 2 008 B**, against
   the provisional N = 15 (target 12). The floor is 8, as in W0.5 (the
   Recorder's round trip 7 and E_sonic 1), and the call makes 22.
   Against W0.5's composition (R28: WithTimeout 4, header map 2, reader 1,
   GetBody 1, request 1, body buffer 1, result 1, decode 4 = 15): the
   header map is gone (0, the template itself on the first attempt, R28's
   −2, R77), and the URL copy of R66 NIT 5 adds 1 (144 B). R77 (2) keeps
   the URL copy and rejects R28's other −1, the first reader embedded in
   the scratch, since a transport's late `Read`, `Close` or `GetBody` on an
   earlier call's handle could then reach the scratch a later call reused,
   which the generation check of `codec.Body` exists to prevent.
2. **Where the 14 go, and W5.3's candidates** (review W2.3 NIT 3, a
   `-memprofilerate=1` profile of `BenchmarkCall/sdk` with `GOGC=off` and
   one P): `context.WithTimeout` 4 (the timerCtx, the `AfterFunc` closure
   and its `*time.Timer`, the CancelFunc closure); `SystemOne` 2 (the
   `body.GetBody` method value, `new(SystemOneResponse)`); the attempt's URL
   copy 1; `codec.Body.Open` 1; `Request.WithContext` 1; `readBody` 1;
   decode 4 (the three fold slices of the visitor and `wire.Answers.Grow`).
   Candidates: (a) one slab for the three fold slices in the codec, −2
   (AC-P2's exact `result.json` pin moves 4 → 2); (b) no attempt
   `WithTimeout` when the caller's deadline is already earlier, −4 on such
   calls, not on q3; (c) presize the answer entries from the question
   count inside the response's allocation, −1, a layout change.
3. **AC-P5 holds per attempt on both hosts, identically:** (i) 263 480 B
   (bound 327 680; superseded by W2.5-01/-02, 263 952 B, since a failing
   attempt scrubs its error text), (ii) 1 288 B (bound 65 536), (iii) 33 559 816 B, (iv)
   33 302 408 B and (v) 33 560 456 B (each bound 33 619 968; the smallest
   margin is (v)'s 59 512 B). Against W0.5's measurements: (i) +32, (ii)
   −40, (iii) +8 152, (iv) +8 016, (v) +8 016 B: the last buffer's spare
   byte rounds that one large allocation up to the next 8 KiB page. A read
   that gave every buffer the spare byte, the branch's first version before
   `TestMemStatsCap` existed, measured (iii) 33 637 640 and (v) 33 638 280
   B, over the bound; it did not survive into the branch as rewritten.
   result.json costs 2 312 B declared and 6 024 B undeclared (W0.5: 2 488
   and 6 200), recorded for W5.2.
4. **`call/sdk` takes 4.742 µs on (M) and 6.100 µs on (L)** (22 allocs/op),
   against the W0.5 prototype's 4.647 and 5.837 µs (+2 % and +4.5 %: the
   logger checks, the per-attempt URL copy, interning in the decoder); the
   (M) row spreads ± 3 % at load 4.9. Its about 2.93 KiB/op is more than `TestAllocWholeCall`'s 2 648 B
   because the benchmark runs with the collector on and 16 or 44 Ps: a
   collection empties the `sync.Pool`s, and an occasional call then pays
   for a fresh scratch buffer, a fraction of an allocation per call that
   the integer allocs/op hides. An unlocked check on (M) before the rewrite
   measured 2 642 to 2 646 B/op with the collector on and one P, and 2 646
   to 2 647 B/op with 16 Ps and `GOGC=off`.

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W2.3-01 | 2026-09-26 04:13:35 JST | W2.3 AC-P6 whole call and AC-P5 memstats | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 4.91 → 4.91 | `BASE=$BASE GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap)$' -v .` | q3: floor 8/640, call 22/2648, SDK-own 14/2008 (N 15, target 12); AC-P5 (i) 263480 B, (ii) 1288 B, (iii) 33559816 B, (iv) 33302408 B, (v) 33560456 B | mallocs/bytes, collector off, `GOMAXPROCS(1)`, 3 of 5 runs agree; `results/alloc-M.txt`; AC-P5 (i) superseded by W2.5-01/-02 |
| W2.3-02 | 2026-09-26 04:13:35 JST | W2.3 `call/sdk` time | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 4.91 → 4.83 | `BASE=$BASE GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $O $SP/bench.lock bench-M -run '^$' -bench '^BenchmarkCall$' -benchmem -count=10 .` | `call/sdk` 4.742 µs ± 3 %, 22 allocs/op | `results/bench-M.txt`, `results/benchstat-M.txt` |
| W2.3-03 | 2026-09-25 19:14:18 UTC | W2.3 AC-P6 whole call and AC-P5 memstats | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.02 → 0.10 | `BASE=$BASE sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap)$' -v .` | identical to W2.3-01 in every count | `results/alloc-L.txt` |
| W2.3-04 | 2026-09-25 19:14:19 UTC | W2.3 `call/sdk` time | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.10 → 0.17 | `BASE=$BASE MAXLOAD=44 sh $R '(L)' $O /tmp/ts-spike/bench.lock bench-L -run '^$' -bench '^BenchmarkCall$' -benchmem -count=10 .` | `call/sdk` 6.100 µs ± 0 %, 22 allocs/op | `results/bench-L.txt`, `results/benchstat-L.txt` |

## W2.4: response payloads (AC-F10)

W2.4 gives `SystemOneResponse` and `ModelsResponse` a `MarshalJSON` and
an `UnmarshalJSON`, and `NoulAnswer`, `ChoiceAnswer`, `ScoreAnswer`,
`Answer`, `Answers`, `Usage` and `ModelCard` a `MarshalJSON` (ruling
R80). The payload is written by `internal/wire`'s payload writer, not by
sonic: its shape is fixed and typed, and wire already holds
pydantic-core's string escaper and zmij's float layout (R33, R42), so the
bytes are the Python SDK's `model_dump_json` bytes, and K27's
architecture-dependent `-0.0` cannot arise on this path (no sonic encoder
runs, and the decoder reads `-0` as 0, R73 NIT 4). Reading a payload back
is the production decoder with no question set. The numbers are
informational: no frozen budget covers them; `TestAllocResponseJSON` pins
the allocation counts so a change shows in CI. Measured at eaedbdd (rows
W2.4-01 and -02) and again at 4a11b48, after the review's fix pass (rows
W2.4-03 and -04), with the same counts and bytes on both hosts. 4a11b48 is
the last commit of the branch that changes code or tests: the commits
between eaedbdd and 4a11b48 change comments, tests and documents only, and
every later commit changes documents only. Raw outputs are in
`_spikes/w2.4/results/`: `alloc-{M,L}.txt` and `gate-{M,L}.txt` at
4a11b48, the same files with `-eaedbdd` for the first measurement, and
`verify-commits-M.txt`, which builds, vets, formats and tests every commit
of the branch alone. Commands use
`R=_spikes/s-c1/run.sh` (W0.5's runner), `O=_spikes/w2.4/results`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/c8084031-5323-4873-8c36-a19f65c9e6ff/scratchpad`
and `BASE=4a11b48` (`eaedbdd` for rows W2.4-01 and -02).

### How the numbers were taken

- (M): `go1.27.1 darwin/arm64`, `GOEXPERIMENT=nosimd,noruntimesecret`,
  under `/opt/homebrew/opt/util-linux/bin/flock` on `$SP/bench.lock`.
- (L): the tree at `$BASE` written by `git archive $BASE` and piped
  over ssh to `/tmp/ts-spike/w2.4-meas/wt-w2.4`; toolchain
  `/tmp/ts-spike/go/bin/go` with the §11 `GOPATH`, `GOMODCACHE` and
  `GOCACHE` under `/tmp/ts-spike` and no `GOEXPERIMENT`, under
  `flock /tmp/ts-spike/bench.lock`.
- `TestAllocResponseJSON` (root, `//go:build !race`): for each of
  `result.json`, `result-20.json`, `structured-legend-flood-1k.json` and
  `models.json`, the response is read from the fixture with
  `UnmarshalJSON`; then `MarshalJSON` is measured, and `UnmarshalJSON` of
  the payload into a fresh response. Counts are `runtime.ReadMemStats`
  deltas under `testsupport.QuietRuntime` (collector off,
  `GOMAXPROCS(1)`), the minimum that three of five runs share, with the
  decoder's pool warm (section 6.1.6).
- Parity: `_spikes/w2.4/python_dump.py` run with the upstream checkout's
  own `.venv` (typesafe-sdk-python 0.7.1 at 0ffd094, Python 3.14.6,
  pydantic-core 2.46.5), output `results/python-dump.txt`, probed
  2026-09-26 05:30:45 JST (time from `date`; the first run, at 04:54:53
  JST, lacked the two bodies of `TestResponseJSONDeviations` and matched
  on every other line); `TestResponseJSONFixtures`, `TestAnswerJSONShapes`
  and `TestResponseJSONDeviations` compare against it.
- Load (R17): (M) 4.08 → 4.08 (eaedbdd) and 8.79 → 8.79 (4a11b48) on 16
  cores; (L) 0.11 → 0.11 and 1.01 → 1.01 on 44.

### W2.4 findings

1. **A payload costs one allocation at every size, on both hosts:**
   `result.json` 364 B in a 704 B buffer, `result-20.json` 2 253 B in
   4 864 B, the 1k flood 57 418 B in 98 304 B, `models.json` 89 B in
   96 B. The writer sizes its buffer once, with every float at its
   longest (24 bytes) and every count at 20, so only strings that need
   escapes can outgrow it. The buffer is 1.7 to 2.2 times the payload on
   these fixtures, because the fixtures' floats are short; a tighter
   bound (the length of each float as written, one more format per float)
   is a W5.3 candidate if the retained size matters.
2. **Reading a payload back costs what the decode costs without a
   question set:** 5 allocations / 752 B for `result.json`, 25 / 6 520 B
   for `result-20.json`, 91 / 213 896 B for the 1k flood, 2 / 80 B for
   `models.json`: every string is a copy into one arena, the misses
   column of `TestAllocDecodeFixtures` (W2.0).
3. **Byte parity with `model_dump_json`:** of the 14 fixtures both SDKs
   accept (13 System One bodies and `models.json`), 13 marshal to
   Python's bytes exactly; `escaped-member-names.json` differs in one
   escape inside a structured legend level, which Go keeps as received
   (`summ\u0061ry` where Python writes `summary`; R73). The four answer
   shapes of R12, R14 and R11, `Usage` and `ModelMetadata` match too.
   Two classes besides escapes differ, and `TestResponseJSONDeviations`
   pins each with Python's bytes beside Go's: a known float member that
   arrived as `-0.0` is written `0.0`, where Python writes `-0.0` (the
   decoder reads every zero as 0, R73 NIT 4), and a member name repeated
   inside a structured level stays as received, where Python keeps the
   last (`{"a":1,"b":2,"a":3}` against `{"a":3,"b":2}`; R73). No fixture
   holds either.
   Python refuses `deviation-lone-surrogate.json`, which Go reads and
   writes back; Go refuses the three other `deviation-*` bodies. All 15
   bodies Go accepts read back to equal values and write the same bytes
   a second time (a fixed point).

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W2.4-01 | 2026-09-26 05:00:02 JST | W2.4 payload allocations | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 4.08 → 4.08 | `BASE=$BASE GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M -count=1 -run '^TestAllocResponseJSON$' -v .` | marshal 1/704 B (result), 1/4864 B (result-20), 1/98304 B (flood-1k), 1/96 B (models); unmarshal 5/752 B, 25/6520 B, 91/213896 B, 2/80 B | mallocs/bytes, collector off, `GOMAXPROCS(1)`, 3 of 5 runs agree; `results/alloc-M-eaedbdd.txt` (written as `alloc-M.txt`, renamed when W2.4-03 took the name) |
| W2.4-02 | 2026-09-25 19:59:59 UTC | W2.4 payload allocations | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.11 → 0.11 | `BASE=$BASE sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L -count=1 -run '^TestAllocResponseJSON$' -v .` | identical to W2.4-01 in every count | `results/alloc-L-eaedbdd.txt` (written as `alloc-L.txt`, renamed when W2.4-04 took the name) |
| W2.4-03 | 2026-09-26 05:34:41 JST | W2.4 payload allocations, after the review fix pass | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.79 → 8.79 | `BASE=$BASE GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M -count=1 -run '^TestAllocResponseJSON$' -v .` | identical to W2.4-01 in every count and byte | at 4a11b48; `results/alloc-M.txt` |
| W2.4-04 | 2026-09-25 20:34:36 UTC | W2.4 payload allocations, after the review fix pass | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.01 → 1.01 | `BASE=$BASE sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L -count=1 -run '^TestAllocResponseJSON$' -v .` | identical to W2.4-01 in every count and byte | at 4a11b48; `results/alloc-L.txt` |

## W2.5: the transport-error scrub (AC-P5 (i))

W2.5 classifies every failure that produced no response in one place and
scrubs the request's credentials out of the transport error's text and
chain before the SDK error is built (`text.go`: `requestCredentials`,
`credentials.redact`, `credentials.cause`). That work runs only on the
error path, so the success path, and with it AC-P6 and AC-P5 (ii) to
(vii), is unchanged; AC-P5 (i), a 16 MiB declared body cut short after
10 bytes, is the one case that ends in a transport error. Measured at
e8bddc3, the last commit of the branch that changes code or tests; the
commit that adds this section changes documents and raw outputs only.
Commands use `R=_spikes/s-c1/run.sh` (W0.5's runner),
`O=_spikes/w2.5/results`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/c8084031-5323-4873-8c36-a19f65c9e6ff/scratchpad`
and `BASE=e8bddc3`.

### How the numbers were taken

- (M): `go1.27.1 darwin/arm64`, `GOEXPERIMENT=nosimd,noruntimesecret`,
  under `/opt/homebrew/opt/util-linux/bin/flock` on `$SP/bench.lock`.
- (L): the tree written by `git archive $BASE` and piped over ssh to
  `/tmp/ts-spike/w2.5-meas/wt-w2.5`; toolchain `/tmp/ts-spike/go/bin/go`
  with the §11 `GOPATH`, `GOMODCACHE` and `GOCACHE` under `/tmp/ts-spike`
  and no `GOEXPERIMENT`, under `flock /tmp/ts-spike/bench.lock`.
- `TestAllocWholeCall` and `TestMemStatsCap` as in W2.3.

### W2.5 findings

1. **AC-P6 is unchanged on both hosts: SDK-own 14 allocations, 2 008 B**
   (floor 8/640, call 22/2648).
2. **AC-P5 (i) is 38 allocations and 263 952 B on both hosts, inside its
   327 680 B bound,** against 19 and 263 480 B at W2.3 (cacba66): of the
   +19, 15 build the needles (`requestCredentials` writes the two
   credentials of the header template, `Bearer test-key` and `test-key`,
   in their `%q`, `%+q` and JSON forms, and grows the list), 3 render the
   cause with `%+v` and `%#v` (review W2.5 MINOR 2, ruling R82), and 1 is
   fmt's printer, pooled but emptied by the collection the harness runs
   before each call. (ii) to (vii) are unchanged: (ii) 1 288 B, (iii)
   33 559 816 B, (iv) 33 302 408 B, (v) 33 560 456 B, (vi) 2 312 B,
   (vii) 6 024 B.

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W2.5-01 | 2026-09-26 06:01:38 JST | W2.5 AC-P6 whole call and AC-P5 memstats | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.88 → 6.41 | `BASE=e8bddc3 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap)$' -v .` | q3: floor 8/640, call 22/2648, SDK-own 14/2008; AC-P5 (i) 38 allocs / 263952 B, (ii) 1288 B, (iii) 33559816 B, (iv) 33302408 B, (v) 33560456 B, (vi) 2312 B, (vii) 6024 B | mallocs/bytes, collector off, `GOMAXPROCS(1)`, 3 of 5 runs agree; `results/alloc-M.txt` |
| W2.5-02 | 2026-09-25 21:01:55 UTC | W2.5 AC-P6 whole call and AC-P5 memstats | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.00 → 0.00 | `BASE=e8bddc3 sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap)$' -v .` | identical to W2.5-01 in every count | `results/alloc-L.txt` |
| W2.5-03 | 2026-09-25 23:47:39 UTC | K32 before the fix: `TestMemStatsCap` at b5b1a2b, 20 invocations without load, then 20 next to 44 loops | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.81 → 0.82; 0.82 → 4.36 (44 loops) | `flock /tmp/ts-spike/bench.lock taskset -c 0,1 ./root.test -test.count=20 -test.cpu 2 -test.run '^TestMemStatsCap$' -test.v` (`go test -c .` of b5b1a2b; the loops `nice -n 19 yes`, unpinned) | PASS 20/20 each, the three-of-five rule holding; case (i)'s first run 42/264240 in all 40 invocations; a later run above 38/263952 in 3 of the 20 without load (39/264000, 40/264048, 39/264016) and in 1 of the 20 with loops (39/264000); CI 36201375147 had two such runs in one invocation (42 39 38 38 39) and failed | `results/l-k32-memcap-before-unloaded.txt`, `results/l-k32-memcap-before-contended.txt` |
| W2.5-04 | 2026-09-25 23:48:14 UTC | K32: case (i) under a heap profile, 10 × 40 runs, each run's stacks diffed against a run at the minimum | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 2.95 → 2.95 | `K32_RUNS=40 K32_SKIP0=1 flock /tmp/ts-spike/bench.lock taskset -c 0,1 ./root.test -test.count=10 -test.cpu 2 -test.memprofilerate=1 -test.run '^TestK32Probe$' -test.v` (`k32-probe.go.txt` as `zz_k32_probe_test.go` at b5b1a2b) | 386 of the 390 runs after the first at 38/263952; each of the other 4 has one allocation more, made by the runtime: `runtime.buildTypeAssertCache` under `errors.As` in `attemptError` (client.go:455), +48 B; `buildTypeAssertCache` and `runtime.buildInterfaceSwitchCache` under `errors.Is` in `readBody` (client.go:523), +48 B and +64 B; `buildTypeAssertCache` under `io.Copy` in the Recorder (recorder.go:121), +48 B | `results/l-k32-probe.txt` |
| W2.5-05 | 2026-09-25 23:50:00 UTC | K32: the same with `runtime.LockOSThread` over the series (GOMAXPROCS is 1 already) | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.51 → 0.51 | the W2.5-04 command with `K32_LOCK=1` | 386 of 390 at 38/263952; 4 above by one allocation of 48, 48, 80 and 112 B, from the same two builders under `io.Copy`, under `fmt.(*pp).handleMethods` printing the cause, and under `errors.Is`: pinning the thread leaves them | `results/l-k32-probe-lockosthread.txt` |
| W2.5-06 | 2026-09-26 08:45:28 JST (file mtime) | K32: case (i)'s first run, and every stack of a run at the minimum | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | not taken (allocation counts, R17) | `GOEXPERIMENT=nosimd,noruntimesecret K32_RUNS=12 go test -count=1 -run '^TestK32Probe$' -memprofilerate=1 -v .` at b5b1a2b; then `K32_RUNS=6 K32_SKIP0=1 K32_DUMP_REF=1`, the same (09:14:23 JST, file mtime) | first run 42/264240, the others 38/263952; the first run's four more are fmt's printer, allocated by the pool's `New` (176 B), and three growths of its buffer (16, 32 and 64 B): the collections of the cases before it emptied the pool and the success call that re-warms does not print; a run at the minimum allocates fmt's per-P pool array again (`sync.(*Pool).pinSlow`, 128 B, dropped by each collection) and takes the printer back from the victim cache | `results/m-k32-probe.txt`, `results/m-k32-probe-refstacks.txt` |
| W2.5-07 | 2026-09-26 09:10:25 JST | K32 after the fix (deb3e43): `TestMemStatsCap` ×20 without load | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 2.52 → 2.43 | `TREE=3d1205c GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$FL sh $C '(M)' $O $SP/bench.lock m-k32-memcap-unloaded 0 -count=20 -cpu 2 -run '^TestMemStatsCap$' -v .` (`proof.sh`) | PASS 20/20, every run of every case within its bound; (i) `call=38/263952 max=42/264240 spread=+4/+288` in all 20; one later run 39/264032 | `results/m-k32-memcap-unloaded.txt` |
| W2.5-08 | 2026-09-26 09:10:33 JST | K32 after the fix, next to 16 loops | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 2.43 → 5.88 (noisy by design) | the W2.5-07 command with `m-k32-memcap-contended 16` | PASS 20/20; (i) as in W2.5-07; later runs 39/264016 twice, in two invocations | `results/m-k32-memcap-contended.txt` |
| W2.5-09 | 2026-09-26 00:10:21 UTC | K32 after the fix, without load | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.05 → 0.12 | `TREE=3d1205c sh $C '(L)' $O /tmp/ts-spike/bench.lock l-k32-memcap-unloaded 0 -exec 'taskset -c 0,1' -count=20 -cpu 2 -run '^TestMemStatsCap$' -v .` (`proof.sh`) | PASS 20/20; (i) min 38/263952, max 42/264240; later runs 39/264000 and 39/264016 once each | `results/l-k32-memcap-unloaded.txt` |
| W2.5-10 | 2026-09-26 00:10:33 UTC | K32 after the fix, next to 44 loops | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.12 → 10.00 (noisy by design) | the W2.5-09 command with `l-k32-memcap-contended 44` | PASS 20/20; (i) 42 then 38 ×4 in all 20 | `results/l-k32-memcap-contended.txt` |
| W2.5-11 | 2026-09-26 09:11:53 JST | K32 mutant: case (i)'s bound lowered to 263 999 B | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 3.52 → 3.48 | the W2.5-07 command in a detached worktree with the mutant, `m-k32-mutant 0 -count=1` | FAIL, as it must: `run 1: TotalAlloc delta 264240 B exceeds the frozen bound 263999 B (AC-P5)` | `results/m-k32-mutant.txt` |
| W2.5-12 | 2026-09-26 00:10:46 UTC | K33 after the fix (3d1205c): the clean-close and GOAWAY rows, `-race`, 1, 2 and 4 Ps, 44 loops | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 10.00 → 15.30 (noisy by design) | `TREE=3d1205c sh $C '(L)' $O /tmp/ts-spike/bench.lock l-k33-race-contended 44 -exec 'taskset -c 0,1' -timeout 60m -race -count=50 -cpu 1,2,4 -run "$K33" -v .` (`proof.sh`) | PASS 300/300; close records in order in all 300; frames the server drained after its close began: SETTINGS (the client's acknowledgement) in 216 runs, SETTINGS then RST_STREAM in 2, RST_STREAM in 1, none in 81 | `results/l-k33-race-contended.txt` |
| W2.5-13 | 2026-09-26 09:10:43 JST | K33 after the fix, the same rows, 16 loops | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 5.88 → 6.89 (noisy by design) | the W2.5-07 command with `m-k33-race-contended 16 -timeout 60m -race -count=50 -cpu 1,2,4 -run "$K33" -v .` | PASS 300/300; drained SETTINGS in 187 runs, none in 113 | `results/m-k33-race-contended.txt` |
| W2.5-14 | 2026-09-26 09:11:59 JST | K33 mutant: `CloseConns` closes an HTTP/2 connection at once, as before the fix | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 3.48 → 3.16 | the W2.5-07 command in a detached worktree with the mutant, `m-k33-mutant 0 -count=5 -run '^(TestLoopbackCloseConnsDrains\|TestTransportErrorsBecomeConnectionOrTimeout)$' -v . ./internal/testsupport/` | FAIL, as it must: the close records fail in 10 of 10 runs of the two root rows and 10 of 10 of `TestLoopbackCloseConnsDrains` (no CloseWriteSeq, no PeerClosedSeq, nothing drained), while every client-side assertion passes: macOS, like Linux, lets the client read close_notify before the reset | `results/m-k33-mutant.txt` |
| W2.5-15 | 2026-09-26 09:10:55 JST | gates at 3d1205c: `-race ./...`, then ci.yaml's root allocation step with its `-list` guard | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.89 → 5.65 | `proof.sh`'s last two runs (`m-race-all 0 -timeout 60m -race -count=1 ./...`; the step's two lines as ci.yaml has them) | ok in all five packages; guard 7 names; the step ok | `results/m-race-all.txt`, `results/m-root-alloc-step.txt` |
| W2.5-16 | 2026-09-26 00:10:57 UTC | the same gates | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 15.30 → 11.23 (W2.5-12's loops decaying) | as W2.5-15 | ok in all five packages; guard 7 names; the step ok | `results/l-race-all.txt`, `results/l-root-alloc-step.txt` |
| W2.5-17 | 2026-09-26 09:47:36 JST | K34 P8 with the strict order everywhere (first push of the fix, 0076086): the ten tests, `-race` ×50, 1, 2 and 4 Ps, no loops | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 5.90 → 7.16 | `TREE=0076086 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$FL sh _spikes/w2.5/proof-k34.sh '(M)' $SP/bench.lock 16 ''`, its first run | PASS 1350/1350 tests, 5100/5100 subtests; 1200 close records in the strict order | superseded by W2.5-25; `results/m-k34-strict-race-unloaded.txt` (filtered, R89 (4)) |
| W2.5-18 | 2026-09-26 09:55:18 JST | the same, 16 loops | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 7.16 → 658.41 (noisy by design) | the same, its second run | FAIL 1 of 150: `TestGoAway`'s first row, records `GoAwaySeq:1 CloseWriteSeq:0 PeerClosedSeq:0 ClosedSeq:2` (the client closed after its last stream, the reader read its EOF before `maybeFinish` ran and closed on it, unrecorded); every client-side assertion passed | [W2.5 K34](#w25-k34-the-goaway-finish-close-on-e9ad7aa); `results/m-k34-strict-race-contended.txt` (filtered) |
| W2.5-19 | 2026-09-26 00:47:42 UTC | the same, no loops | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.01 → 0.20 | `TREE=0076086 sh _spikes/w2.5/proof-k34.sh '(L)' /tmp/ts-spike/bench.lock 44 'taskset -c 0,1'`, its first run | FAIL 5 of 150: `TestGoAway` row 1 ×2, row 3 ×1, the K21 GOAWAY scenario ×2; 3 with `CloseWriteSeq:0 PeerClosedSeq:0`, 2 with `CloseWriteSeq` set and `PeerClosedSeq:0` (the reader read the client's EOF just before `draining` was set); client-side failures 0 | `results/l-k34-strict-race-unloaded.txt` (filtered) |
| W2.5-20 | 2026-09-26 00:55:34 UTC | the same, 44 loops | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.37 → 44.18 (noisy by design) | the same, its second run | FAIL 17 of 150: row 1 ×11, row 3 ×4, K21 ×2 (13 and 4 of the two shapes); client-side failures 0; one K21 GOAWAY run with refused streams, the K21c residual that R89 (1) routes to W6.1 (not asserted) | `results/l-k34-strict-race-contended.txt` (filtered) |
| W2.5-21 | 2026-09-26 10:37:00 JST | K34 mutant M1 at 68fc345: `maybeFinish` closes at once after the GOAWAY (`c.Close()` for `c.closeWrite()`) | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 30.19 → 31.09 | `TREE=68fc345+mutant-m1 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$FL sh $C '(M)' $O $SP/bench.lock m-k34-mutant-m1 0 -count=5 -run "$K34M" -v ./internal/testsupport/ ./internal/h2gate/` in a copy of 68fc345 with the mutant | FAIL, as it must: the six GOAWAY rows' records fail in 30 of 30 runs, all `GoAwaySeq:1 CloseWriteSeq:0 PeerClosedSeq:0 ClosedSeq:2`; `TestLoopbackActionsDrain`'s ActionGoAway row 5 of 5 (records; nothing drained); every client-side assertion passes; the two CloseConns rows pass | `results/m-k34-mutant-m1.txt` |
| W2.5-22 | 2026-09-26 10:37:26 JST | K34 mutant "old": e9ad7aa's `h2conn.go` under 68fc345's tests | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 30.44 → 23.98 | the W2.5-21 command with the mutant `old` | FAIL, as it must: the same 30 of 30 GOAWAY-row records; `TestLoopbackActionsDrain`'s two rows 10 of 10 (K34's close_notify-only end, and ActionClose's immediate close); client-side 0 | `results/m-k34-mutant-old.txt` |
| W2.5-23 | 2026-09-26 01:35:42 UTC | K34 mutant M1 | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 2.61 → 1.87 | `TREE=68fc345+mutant-m1 sh $C '(L)' $O /tmp/ts-spike/bench.lock l-k34-mutant-m1 0 -count=5 -run "$K34M" -v ./internal/testsupport/ ./internal/h2gate/` in the same copy | FAIL, as it must: 30 of 30 GOAWAY-row records as on (M); the ActionGoAway row fails earlier in 5 of 5, on the late PING's write: `write: broken pipe` (Linux reports the server's reset to the writer); h2gate client-side 0 | `results/l-k34-mutant-m1.txt` |
| W2.5-24 | 2026-09-26 01:36:01 UTC | K34 mutant "old" | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.87 → 1.42 | the W2.5-23 command with the mutant `old` | FAIL, as it must: 30 of 30 GOAWAY-row records; `TestLoopbackActionsDrain` 10 of 10 (5 on the broken pipe, 5 on records and drained frames); h2gate client-side 0 | `results/l-k34-mutant-old.txt` |
| W2.5-25 | 2026-09-26 10:37:46 JST | K34 after the fix (68fc345): the ten tests, `-race` ×50, 1, 2 and 4 Ps, no loops | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 23.98 → 4.52 (other lanes' load; no loops of this run) | `TREE=68fc345 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$FL sh _spikes/w2.5/proof-k34.sh '(M)' $SP/bench.lock 16 ''`, its first run | PASS 1350/1350 tests, 5100/5100 subtests; 1200 close records valid, the server first in all 1200; K34's late DATA drained in 150 of 150 POST runs and 150 of 150 no-GetBody runs; K21 GOAWAY 72 ok, 0 refused in 150/150 | `results/m-k34-race-unloaded.txt` (filtered) |
| W2.5-26 | 2026-09-26 10:47:09 JST | the same, 16 loops | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 4.21 → 60.11 (noisy by design) | the same, its second run | PASS 1350/1350, 5100/5100; 1200 records valid, the client first in 1 (`TestGoAway` row 1: `GoAwaySeq:1 CloseWriteSeq:0 PeerClosedSeq:2 ClosedSeq:3`); DATA drained 150/150 and 150/150; K21 150/150 | `results/m-k34-race-contended.txt` (filtered) |
| W2.5-27 | 2026-09-26 01:36:21 UTC | the same, no loops | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.42 → 0.40 | `TREE=68fc345 sh _spikes/w2.5/proof-k34.sh '(L)' /tmp/ts-spike/bench.lock 44 'taskset -c 0,1'`, its first run | PASS 1350/1350, 5100/5100; 1200 records valid, the client first in 3 (row 1 ×1, K21 ×2); DATA drained in 150/150 POST runs and 148/150 no-GetBody runs (in 2 the client read the GOAWAY before it wrote the body); K21 150/150 | `results/l-k34-race-unloaded.txt` (filtered) |
| W2.5-28 | 2026-09-26 01:45:36 UTC | the same, 44 loops | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.25 → 44.39 (noisy by design) | the same, its second run | PASS 1350/1350, 5100/5100; 1200 records valid, the client first in 11 (row 1 ×6, row 3 ×2, K21 ×3); DATA drained in 131/150 and 128/150 (the rest never sent: the client read the GOAWAY first); K21 150/150 | `results/l-k34-race-contended.txt` (filtered) |
| W2.5-29 | 2026-09-26 10:55:15 JST | gates at 68fc345: `-race ./...`, then ci.yaml's root allocation step with its `-list` guard | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 55.54 → 41.76 | `proof-k34.sh`'s last two runs | ok in all five packages; guard 7 names; the step ok | `results/m-k34-race-all.txt`, `results/m-k34-root-alloc-step.txt` |
| W2.5-30 | 2026-09-26 01:53:31 UTC | the same gates | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 44.39 → 32.00 (W2.5-28's loops decaying) | as W2.5-29 | ok in all five packages; guard 7 names; the step ok | `results/l-k34-race-all.txt`, `results/l-k34-root-alloc-step.txt` |
| W2.5-31 | 2026-09-26 10:55:56 JST | the section 11 lint chain at 68fc345; windows/amd64 vet and `test -c` (10:35:06 JST) | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 33.01; 19.91 | the chain as in `results/m-lint.txt`, with `GOPACKAGESDEBUG` unset; `GOOS=windows GOARCH=amd64 go vet ./...` and `go test -c -o /dev/null` per package | `0 issues.`, `No vulnerabilities found.`; vet ok; `test -c` ok in all five packages | no lock (not measurements); `results/m-k34-lint.txt`, `results/m-k34-windows-vet.txt` |

### W2.5 K32 and K33: the CI failures on b5b1a2b

CI 36201375147 on b5b1a2b failed in two root tests (rulings K32, K33).
Lane `p2-cifix-2` fixed both in tests and `internal/testsupport`; no
production file changed.

**K32: `TestMemStatsCap` case (i).** The alloc helper's three-of-five rule
failed on ubuntu-26.04 (42 39 38 38 39 allocations), with every run inside
the frozen 327 680 B. A heap profile of each run (W2.5-04 to -06) names
every allocation above the minimum. On the first run it is fmt's printer
and its buffer (+4, 288 B): the collections of the cases before (i)
emptied fmt's pool, and the success call that re-warms the pools does not
print. On any later run it is one allocation of 48 to 112 B made by the
Go runtime, `runtime.buildTypeAssertCache` or
`runtime.buildInterfaceSwitchCache`, under `errors.Is` and `errors.As` on
the error chain, fmt printing the cause, or `io.Copy` in the Recorder. On
a lookup that misses such a cache, `runtime.typeAssert` and
`runtime.interfaceSwitch` build a larger one with probability 1/1024,
then 1/(size of the cache) (`runtime/iface.go`: "Only bother updating
the cache ~1 in 1000 times"). The draw is random, so identical calls can
differ, and W2.5's scrub added lookups to (i)'s error path, which raised
the odds from no observed case before W2.5 to about one run in a
hundred (4 in 390 on (L), W2.5-04). The needle build is not involved: `requestCredentials`
sorts its needles. The only effect of map order is which of two
`jsonForm` calls opens a tiny-allocator block, with equal counts. The
collector is off and GOMAXPROCS is 1 already (`QuietRuntime`), and
`LockOSThread` leaves the rate unchanged (W2.5-05). So the source is
outside the SDK (the charter's cause 1), and the fix is in the test
(deb3e43): AC-P5 is a set of bounds (R26), so `TestMemStatsCap` checks
each case's bound on every run and logs the minimum, the maximum and the
spread (`testsupport.Spread`). The exact pins of AC-P1, AC-P2 and AC-P6
keep `StableMin`'s agreement rule, whose odds of failing from this source
are negligible when no run is lost to a pool (three of five runs would
have to draw). (i)'s spread before and after the fix is the same, and its
value is unchanged: 38 / 263 952 B, maximum 42 / 264 240 B (the first
run), with a later run 1 or 2 allocations (48 to 112 B) above the minimum
now and then. For precision on W2.5 finding 2: the minimum's allocation
for fmt is the pool's per-P array (`sync.(*Pool).pinSlow`, 128 B), which
every collection drops; the printer itself comes back from the pool's
victim cache (W2.5-06).

**K33: the clean-close and GOAWAY rows of
`TestTransportErrorsBecomeConnectionOrTimeout`.** On windows-2025 under
`-race`, their calls ended in `wsarecv: An established connection was
aborted by the software in your host machine.` (WSAECONNABORTED) instead
of `io.ErrUnexpectedEOF` and the GOAWAY. `CloseConns` closed the socket
right after the handler's last frame, and a fresh client connection
still writes then: the client acknowledges the server's SETTINGS after
it sends its request, so the acknowledgement was still unread in the
server's buffer, or arrived after the close. W2.5-12 and -13 count it
among the frames the server drained after its close began: in 216 and
187 of 300 runs. A socket closed with unread data, or one that data
reaches after its close, is ended with a TCP reset (RFC 1122 section
4.2.2.13). Linux and macOS let the client read the in-band close_notify
before the reset, which is why only Windows failed (W2.5-14 shows the
client-side assertions passing on macOS with the old close). On Windows
the reset destroys what the client has not read yet. `CloseConns` now
ends an HTTP/2 connection as a server that is done with it (3d1205c): it
drops the streams, sends close_notify and a TCP FIN after the frames
already written (under the write lock, so a GOAWAY in flight leaves
first), and its reader keeps reading and discarding until the client
closes its side or 5 s pass; only then is the socket closed. Other
connections and `LoopbackServer.Close` keep the immediate close.
`ConnInfo` records the order by sequence number (K29): `GoAwaySeq`,
`CloseWriteSeq` (taken before close_notify leaves), `PeerClosedSeq`,
`ClosedSeq`, and `Drained`, the frames drained. The two rows check
GOAWAY < close_notify and FIN < the client's close < the socket's close.
The client closes only after it read everything before close_notify, so
it read the GOAWAY before the server closed. The client-side assertions
are unchanged, and no Windows alternative cause was added. The first
contended run on (L), with `CloseWriteSeq` taken after the writes, failed
16 of 300 on the record's order alone: the client had reacted to
close_notify before the closing goroutine took its number. The record
was moved before the writes; no row cites that run.

Audit of the other rows that close a loopback connection mid-stream: the
connection-reset row (`H2Conn.Reset`, a reset by design) accepts any
non-dial `*ConnectionError`, which a reset on either side of the response
headers gives. The proxy's 502 has read the whole CONNECT request before
it closes. The two rows that hold a stream end by the client's reset, not
a server close. In `internal/h2gate`, `TestConnClose`'s clean close and
`TestReplay`'s tcp-close warm the connection first, and now take the same
`CloseConns` path; its reset rows are resets by design.
`TestLoopbackConnEnd` acknowledges SETTINGS before its request.

Row variables: `C=_spikes/w2.2/contend.sh`, `O=_spikes/w2.5/results`,
`K33='^TestTransportErrorsBecomeConnectionOrTimeout$/(clean_close_mid-body|GOAWAY_after_the_request)'`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/40cb0f1f-c8a9-422c-a3e8-b3afc329b5cb/scratchpad`
(the (M) lock), `FL=/opt/homebrew/opt/util-linux/bin/flock`; (L) ran
the section 11 tar pipe of 3d1205c in `/tmp/ts-spike/src-p2-cifix-2` with
the section 11 environment and no `GOEXPERIMENT`. `_spikes/w2.5/proof.sh`
runs W2.5-07 to -10, -12, -13, -15 and -16 in that order on one host.
W2.5-03 to -05 ran a pinned binary under the (L) lock with a small wrapper
that wrote the header lines of their files (host, go version, loops,
start and end with load). `_spikes/w2.5/k32-probe.go.txt` is the probe of
W2.5-04 to -06, never committed as Go. The section 11 lint chain passed
at 3d1205c (`results/m-lint.txt`), with govulncheck run through
`go run golang.org/x/vuln/cmd/govulncheck@latest`: the installed binary
is built with go1.26 and cannot load Go 1.27 sources.

### W2.5 K34: the GOAWAY-finish close on e9ad7aa

The post-landing main run 36204700120 at e9ad7aa failed on
windows-2025 (ruling K34): `internal/h2gate`
`TestReplay/success: GOAWAY before a POST with GetBody was processed`
(`recovery_test.go:267` then) read `wsarecv: An established connection
was aborted by the software in your host machine.` instead of 200; the
pre-landing dispatch 36204531173 at the same SHA was green. Lane
`p2-cifix-3` fixed it in `internal/testsupport` and the h2gate tests; no
production file changed.

**Cause** (critic-p2's re-check). K33's drain covered `CloseConns`
only. `ActionGoAway` calls `GoAway(0)`, and `maybeFinish` then sent TLS
close_notify alone: no FIN, and no drain, because `draining` was set only
by `closeGracefully`. The POST's DATA frame arrives next; `onData`
answered it with a connection WINDOW_UPDATE through `c.write`, the write
failed after close_notify (`tls: protocol is shutdown`), and `c.write`
closed the socket with the client's frames unread. The kernel ends such a
socket with a reset (RFC 1122 section 4.2.2.13), and on Windows the reset
destroyed the GOAWAY before net/http read it, so it could not replay the
request. The GET row sends no DATA and passed. Linux and macOS let the
client read the GOAWAY and close_notify before the reset: under the old
code every client-side assertion passes on both hosts (W2.5-22, -24).

**The close as built** (92eac6d). `closeWrite` is the one graceful end:
under the write lock (so a frame in flight, a GOAWAY among them, leaves
first) it takes `CloseWriteSeq`, sets `draining`, sends close_notify and
a TCP FIN and sets the 5 s read deadline; the reader then reads and
discards frames (`Drained`) until the client's EOF (`PeerClosedSeq`) or
the deadline, and `serve` closes the socket (`ClosedSeq`).
`CloseConns` (`closeGracefully`), `ActionClose` (now `closeGracefully`,
no longer `H2Conn.Close`: no test needs an abortive close, and the one
that expects io.EOF, `TestLoopbackConnEnd`, now gets it whatever the
client writes late) and `maybeFinish` all end there. Nothing is written
after close_notify: every writer checks `draining` under the write lock.
The reader's own frames (its SETTINGS and their acknowledgement, PING
acknowledgements, WINDOW_UPDATE refunds, RST_STREAM for a stream error
or a refusal) are dropped and reported as written, so the reader goes on
to drain; a handler's HEADERS, DATA or RST_STREAM return
`errConnClosed`; `GoAway` returns an error; `SetMaxConcurrentStreams`
already returns `ErrConnClosing` (it checks `closed`, set before
`draining`, under the same lock). `H2Conn.Reset` stays the abortive end
(SO_LINGER 0), `H2Conn.Close` stays the immediate close, and
`LoopbackServer.Close` (teardown) keeps `closeRaw`. The `c.Close()`
call sites:

| Site (68fc345) | Before | After |
| --- | --- | --- |
| `GoAway`'s write error (`h2conn.go:228`) | `c.Close()` | `writeFailed`; a GoAway after close_notify writes nothing (`:205`) |
| `SetMaxConcurrentStreams`'s write error (`:271`) | `c.Close()` | `writeFailed` |
| `c.write`'s error (`:403`) | `c.Close()` | dropped while draining (`:396`); a failed write goes to `writeFailed` |
| `ActionClose` (`:620`) | `c.Close()` | `c.closeGracefully()` |
| `writeStream`'s error (`:894`) | `c.Close()` | `errConnClosed` while draining (`:886`); `writeFailed` |
| `writeHeaders`'s error (`:943`) | `c.Close()` | `errConnClosed` while draining (`:908`); `writeFailed` |
| `writeFailed` (`:415`) | (new) | `c.Close()` unless draining: with `draining` checked first no write follows close_notify, so a failed write means a broken socket, and a connection whose drain began since is left to its reader |
| `serve`'s deferred `Close` (`:424`) | unchanged | runs once the reader stops: after the client's EOF, when nothing is left unread; after the 5 s drain bound; after a read error; or after a failed reader write (a broken socket). `serve` now keeps a drain deadline that a close during its preface set (`:433`) |

**P8, and the order it found** (68fc345). `closedGracefully` in
`recovery_test.go` waits for the server's close of a connection and
checks its records by sequence number (K29) in `TestReplay`'s three
ActionGoAway rows (GET, POST with GetBody, POST without), `TestGoAway`'s
two GOAWAY rows, the K21 GOAWAY scenario, and, beyond the charter, the two
CloseConns rows (`TestConnClose`'s clean close, `TestReplay`'s
tcp-close). The first version (0076086) demanded the strict order
`GoAwaySeq < CloseWriteSeq < PeerClosedSeq < ClosedSeq` everywhere, and
failed 0, 1, 5 and 17 times in four sets of 150 runs (W2.5-17 to -20),
all in the three rows where a goroutine other than the connection's
reader sends the GOAWAY, and never in a client-side assertion.
net/http closes a connection that GOAWAY ended as soon as its last
stream is done (`closeOnIdle` in
`forgetStreamID`, `net/http/internal/http2/transport.go:1891-1897`), which
can precede `maybeFinish` on the goroutine that finished that stream or
called `GoAway`; the reader then read the client's EOF before `draining`
was set, recorded nothing and closed. That end is clean: the server read
everything up to the client's EOF. So the reader now records
`PeerClosedSeq` whichever side closes first, `GoAwaySeq` is taken before
the frame leaves (as `CloseWriteSeq` is), and the check is: the socket
closed after the reader read the client's end (`0 < PeerClosedSeq <
ClosedSeq`, what rules the reset out), the server's close first
(`CloseWriteSeq < PeerClosedSeq`) except where `clientFirst` allows the
client to close first (the three rows named), and GOAWAY before both
exactly when sent. The ActionGoAway rows stay strict: their reader sends
the GOAWAY and runs `closeWrite` before it reads another frame. The two
mutants still fail every GOAWAY row on both hosts while every client-side
assertion passes (W2.5-21 to -24); after the fix the ten tests pass
150/150 each on both hosts, unloaded and under contention, with the client
first in 0, 1, 3 and 11 runs (W2.5-25 to -28).
`TestLoopbackActionsDrain` sends K34's frames itself: the rest of a
request body and a PING after close_notify, drained and never answered,
for ActionGoAway and ActionClose.

**Audit: every test that ends a loopback connection server-side.**

| Test (row) | End after the fix | Can a reset precede the client's read of the last frames? | Check |
| --- | --- | --- | --- |
| h2gate `TestGoAway` "GOAWAY below two of four…" | `GoAway` from the test goroutine; drain after the two streams at or below LastStreamID finish, or the client closes first | no: the socket closes after the client's EOF | `closedGracefully` (client first allowed) |
| h2gate `TestGoAway` "POST without GetBody…" | `GoAway(1)` from the test goroutine; drain at once, or the client closes first | no | `closedGracefully` (client first allowed) |
| h2gate `TestGoAway` "a refused stream…" | no server end (teardown) | n/a | none needed |
| h2gate `TestConnClose` clean close | `CloseConns` | no | `closedGracefully` (strict) |
| h2gate `TestConnClose` TCP reset | `H2Conn.Reset` | a reset by design | the row asserts ECONNRESET |
| h2gate `TestReplay` GET / POST with GetBody | ActionGoAway: the reader drains | no (the K34 row) | `closedGracefully` (strict) |
| h2gate `TestReplay` POST without GetBody | ActionGoAway | no | `closedGracefully` (strict) |
| h2gate `TestReplay` tcp-close | `CloseConns` in the handler | no | `closedGracefully` (strict) |
| h2gate `TestReplay` tcp-reset | `H2Conn.Reset` | a reset by design | the row asserts ECONNRESET |
| h2gate `TestReplay` RST_STREAM INTERNAL_ERROR | a stream reset; the connection lives until teardown | n/a | none needed |
| h2gate `TestTokenResidualK21` GOAWAY | `GoAway(3)` from the test goroutine; drain after stream 3, or the client closes first | no | `closedGracefully` (client first allowed) |
| h2gate `TestTokenResidualK21` REFUSED, SETTINGS | no connection end (teardown) | n/a | none needed |
| h2gate `log_test` "a dial that fails after warm…" | `LoopbackServer.Close` (teardown's `closeRaw`) mid-test | the client never reads that connection again: it closes its idle connections and the assertion is on the refused re-dial | justified |
| other h2gate tests | teardown after the assertions | n/a | none needed |
| testsupport `TestLoopbackActionsDrain` (new) | ActionGoAway, ActionClose | no; late DATA and PING drained | records strict, `Drained` |
| testsupport `TestLoopbackCloseConnsDrains` | `CloseConns` | no | records strict (K33) |
| testsupport `TestLoopbackGoAway`, the four raw-client rows | `GoAway` / ActionGoAway: drain (before: close_notify only) | no | `expectEOF` accepts any end but a timeout; the path's records are checked by `TestLoopbackActionsDrain` and the h2gate rows |
| testsupport `TestLoopbackGoAway` "net/http replays a request GOAWAY left unprocessed" | ActionGoAway: drain | no (`TestReplay` GET's twin) | the replay (status 200, two connections) |
| testsupport `TestLoopbackGoAway` "GOAWAY overtakes in OnStream" | then `srv.Close()` mid-test, after the client read its EOF | nothing is read after it | justified |
| testsupport `TestLoopbackRefuseCloseHold` ActionClose | drain (was the immediate close) | no | `expectEOF` |
| testsupport `TestLoopbackRefuseCloseHold` CloseConns | drain (K33) | no | `expectEOF` |
| testsupport `TestLoopbackConnEnd` ActionClose, CloseConns | drain | no | io.EOF, not a reset |
| testsupport `TestLoopbackConnEnd` `Close` | `H2Conn.Close`, immediate | no, by construction: the client acknowledged SETTINGS before its request and writes nothing after it, and the server read the HEADERS it answered | io.EOF, not a reset |
| testsupport `TestLoopbackConnEnd` ActionReset, `Reset` | reset by design | by design | a reset |
| testsupport `TestLoopbackSetMaxConcurrentStreams` "GOAWAY with no stream left" | drain | no | `ErrConnClosing`, GOAWAY frame, EOF |
| testsupport `TestLoopbackSetMaxConcurrentStreams` "Close ran" | `H2Conn.Close`, immediate | yes, the client's SETTINGS acknowledgement can be unread | the row checks `ErrConnClosing` and `expectEOF` accepts any end but a timeout, so a reset cannot fail it |
| root "a connection reset mid-body" | `H2Conn.Reset` | by design | any non-dial `*ConnectionError` |
| root "a clean close mid-body", "GOAWAY after the request was written" | `CloseConns` (the GOAWAY row's stream is at LastStreamID and in flight, so `maybeFinish` cannot end it first) | no | records strict (K33) |
| root "the attempt's deadline before the response" | the client resets the held stream | n/a | `Dropped` |
| other root tests | teardown after the assertions | n/a | none needed |
| W3.2's coming RT9 "GOAWAY after write → retried" (not on main) | the drain, once rebased onto this fix | no | W3.2 should call a records check like `closedGracefully`, allowing the client first when its GOAWAY comes from outside the reader |

Residual: a client that does not close within the 5 s drain bound gets
the server's close anyway, and the reset that may follow; every net/http
client closes on close_notify, and the records checks would show it
(`PeerClosedSeq` 0).

**CI.** Windows is the only place this class shows, so the proof there
is repeated runs (critic P9). Dispatches of `ci.yaml` on
`wave/p2-cifix-3`: at 0076086 (the first push of the fix, with the strict
check) 36205889242 and 36206121129, green on all four jobs; at 3b9095e
36207842005, tests green on all three images and lint red on staticcheck
QF1001 in the new helper (a De Morgan rewrite in 68fc345); at the code
head 68fc345 36208919047, 36210115152 and 36210276873, green on all four jobs each. A
ledger commit cannot name the runs at its own SHA: the three P9
dispatches at the landing SHA are in the lane report and the rulings.

Row variables: `C=_spikes/w2.2/contend.sh`, `O=_spikes/w2.5/results`,
`K34M='^(TestReplay|TestGoAway|TestTokenResidualK21|TestLoopbackActionsDrain)$'`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/40cb0f1f-c8a9-422c-a3e8-b3afc329b5cb/scratchpad`
(the (M) lock), `FL=/opt/homebrew/opt/util-linux/bin/flock`.
`_spikes/w2.5/proof-k34.sh` runs, in order, the ten tests (`TestReplay`,
`TestGoAway`, `TestConnClose`, `TestTokenResidualK21` and six
`TestLoopback*` tests that end a connection) without loops and under
contention, then `-race ./...` and ci.yaml's root allocation step. (L)
ran the section 11 tar pipe of each tree in `/tmp/ts-spike/src-p2-cifix-3`
(mutant copies beside it) with the section 11 environment and no
`GOEXPERIMENT`, the test binaries pinned with `taskset -c 0,1` as in
W2.5-12. 0076086 and 3b9095e were the fix's earlier pushes, rewritten on
the lane branch; the code measured last is 68fc345 (92eac6d, 68fc345).
The six `-v` files over 512 KB are filtered per R89 (4), keeping the
close records as well; each header gives the size before the filter and
the pass and failure counts.

## W3.2: the retry loop (AC-P6 under `DefaultRetry()`)

W3.2 completes `RetryPolicy` and runs every call through one loop that
asks the policy after a failed attempt (`retryState.wait`); the success
path does one `time.Now()` more and nothing else: the policy is copied
onto `send`'s stack, the retry state stays there, and the timer is made
only when a retry waits. `TestAllocWholeCall` now builds its
client with `WithRetry(DefaultRetry())`, the production policy (ruling
R88b), where it had `newTestClient`'s single attempt. W3.2-01 and -02
were measured at 873d847, the last commit of the first pass that changes
code or tests; W3.2-03 and -04 at bb54320, the last such commit of the
review's fix pass, whose code change (MINOR 3: no attempt once the
caller's deadline ended the wait) is on the retry path only. Both were
taken before the branch was rebased onto 1ecc6f5 (W3.1's landing, over
p2-cifix-3): as rebased, 873d847 is ac993bb and bb54320 is 954ff7b, the
same trees but for main's own changes, and the raw headers keep the
measured SHA. W3.2-05 and -06 measure 170c958, the last commit on the
rebased branch that changes code or tests (the re-check's RT19 rows,
over ruling R100's RT9 records check). The commits that write this section change documents and raw
outputs only. Commands use `R=_spikes/s-c1/run.sh` (W0.5's runner),
`O=_spikes/w3.2/results`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/40cb0f1f-c8a9-422c-a3e8-b3afc329b5cb/scratchpad`
and the row's `BASE`.

### How the numbers were taken

- (M): `go1.27.1 darwin/arm64`, `GOEXPERIMENT=nosimd,noruntimesecret`,
  under `/opt/homebrew/opt/util-linux/bin/flock` on `$SP/bench.lock`.
- (L): the tree written by `git archive $BASE` and piped over ssh to
  `/tmp/ts-spike/src-w3.2/meas`; toolchain `/tmp/ts-spike/go/bin/go` with
  the §11 `GOPATH`, `GOMODCACHE` and `GOCACHE` under `/tmp/ts-spike` and no
  `GOEXPERIMENT`, under `flock /tmp/ts-spike/bench.lock`.
- `TestAllocWholeCall` and `TestMemStatsCap` as in W2.3, with the
  three-of-five rule and `testsupport.Spread` of p2-cifix-2.

### W3.2 findings

1. **AC-P6 is unchanged on both hosts under `DefaultRetry()`: SDK-own 14
   allocations, 2 008 B** (floor 8/640, call 22/2648).
2. **AC-P5's allocation counts are unchanged, and each case is 80 B
   heavier on both hosts:** (i) 38 allocations / 264 032 B (bound
   327 680 B), (ii) 1 368 B, (iii) 33 559 896 B, (iv) 33 302 488 B,
   (v) 33 560 536 B, (vi) 2 392 B, (vii) 6 104 B, against W2.5-01's
   263 952, 1 288, 33 559 816, 33 302 408, 33 560 456, 2 312 and 6 024 B.
   `TestMemStatsCap` passes `Retry(NoRetry())` to force one attempt. The
   80 B are `callOptions`, which `collectCallOptions` moves to the heap for
   any call that passes an option: holding the 80 B policy by value (and
   `hasRetry`) took it from 72 B to 152 B, from the allocator's 80 B class
   to its 160 B class. So every call that passes any option pays them, and
   a call without options, AC-P6's, pays nothing; the `Retry` closure
   stays on the stack. Review W3.2 MINOR 4 measured the whole call at
   e9ad7aa → 7f36c48: no options 22/2648 → 22/2648, `Header` 29/3288 →
   29/3368, `Model` 25/2760 → 25/2840, `Retry(NoRetry())` 23/2728 →
   23/2808. (This finding first read "the `Retry` closure carries the
   policy, 16 B → 96 B"; that was wrong.) A first cut held the policy
   through a pointer, which moved it to the heap of its own: one
   allocation more, on `Retry` calls only. Ruling R97-corr keeps the
   by-value form for now (no allocation added); W3.4 records the cost per
   option-bearing call, and W5.3 evaluates a 48 B policy (the statuses and
   the predicate behind one pointer, `callOptions` in the 112 B class).
3. **The review's fix pass and the rebase leave every count as it was:**
   W3.2-03 and -04 at bb54320, and W3.2-05 and -06 at 170c958 on
   1ecc6f5, equal W3.2-01 in every count on both hosts.

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W3.2-01 | 2026-09-26 10:09:14 JST | W3.2 AC-P6 whole call under `DefaultRetry()` and AC-P5 memstats | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.88 → 6.88 | `BASE=873d847 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap)$' -v .` | q3: floor 8/640, call 22/2648, SDK-own 14/2008; AC-P5 (i) 38 allocs / 264032 B, (ii) 1368 B, (iii) 33559896 B, (iv) 33302488 B, (v) 33560536 B, (vi) 2392 B, (vii) 6104 B | mallocs/bytes, collector off, `GOMAXPROCS(1)`, 3 of 5 runs agree; `results/alloc-M.txt` |
| W3.2-02 | 2026-09-26 01:09:20 UTC | W3.2 AC-P6 whole call under `DefaultRetry()` and AC-P5 memstats | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.30 → 0.36 | `BASE=873d847 sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap)$' -v .` | identical to W3.2-01 in every count | `results/alloc-L.txt` |
| W3.2-03 | 2026-09-26 11:10:05 JST | W3.2 review fix pass: AC-P6 under `DefaultRetry()` and AC-P5 memstats | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 5.17 → 5.17 | `BASE=bb54320 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M-fix -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap)$' -v .` | identical to W3.2-01 in every count: q3 SDK-own 14/2008; AC-P5 (i) 38 allocs / 264032 B … (vii) 6104 B | mallocs/bytes, collector off, `GOMAXPROCS(1)`, 3 of 5 runs agree; `results/alloc-M-fix.txt` |
| W3.2-04 | 2026-09-26 02:10:07 UTC | W3.2 review fix pass: AC-P6 under `DefaultRetry()` and AC-P5 memstats | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.04 → 0.04 | `BASE=bb54320 sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L-fix -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap)$' -v .` | identical to W3.2-01 in every count | `results/alloc-L-fix.txt` |
| W3.2-05 | 2026-09-26 11:41:47 JST | W3.2 rebased onto 1ecc6f5: AC-P6 under `DefaultRetry()` and AC-P5 memstats | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 10.75 → 10.75 | `BASE=170c958 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M-rebased -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap)$' -v .` | identical to W3.2-01 in every count: q3 SDK-own 14/2008; AC-P5 (i) 38 allocs / 264032 B … (vii) 6104 B | mallocs/bytes, collector off, `GOMAXPROCS(1)`, 3 of 5 runs agree; `results/alloc-M-rebased.txt` |
| W3.2-06 | 2026-09-26 02:41:54 UTC | W3.2 rebased onto 1ecc6f5: AC-P6 under `DefaultRetry()` and AC-P5 memstats | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.03 → 0.03 | `BASE=170c958 sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L-rebased -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap)$' -v .` | identical to W3.2-01 in every count | `results/alloc-L-rebased.txt` |

## W5.1: benchmarks B1–B6 and the naive comparator (AC-P2, AC-P6, AC-P7)

W5.1 adds `internal/testsupport/naive`, the comparator of owner decision
G3 (a), and the benchmark set B1–B6 of the Rust reference, which
[`benchmarks.md`](benchmarks.md) describes. Rows W5.1-01 to -08 were
measured at 6afc8a3, `wave/w5.1` on e9ad7aa, before any Phase 3 wave
landed. So their `call/sdk` is the W2.3 client under its one-attempt
default policy. B4 (`Retry-After` and the backoff) needed W3.2's
`retry.go`. It was added at 1ca60e1, after the rebase onto W3.2's landing
(87d1ac7), and rows W5.1-09 to -15 were taken there: B4, a B6 re-run
after b3fd5db changed the loopback server's close path, and the gates. W3.4
re-measures the time clause of AC-P6 on the client as Phase 3 leaves it
and freezes it. W5.1 reports that clause and the "≤ 0.5 × naive" clause
of AC-P2; it asserts neither.

This section follows landing order (ruling R101 (c)): it was first
written after W2.3, to keep clear of the sections W3.2 and W4.2 append,
and moved here when W5.1 rebased onto W3.2's landing (87d1ac7). Raw
outputs are in `_spikes/w5.1/results/`,
and `_spikes/w5.1/render.py` prints the tables below from them. Commands
use `R=_spikes/s-c1/run.sh` (W0.5's runner), `O=_spikes/w5.1/results`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/40cb0f1f-c8a9-422c-a3e8-b3afc329b5cb/scratchpad`
(the (M) lock) and `BASE=6afc8a3`.

### How the numbers were taken

- **(M):** `go1.27.1 darwin/arm64` with
  `GOEXPERIMENT=nosimd,noruntimesecret`, under
  `/opt/homebrew/opt/util-linux/bin/flock` on `$SP/bench.lock`.
- **(L):** the worktree at 6afc8a3, without `.git`, copied with the
  section 11 `tar | ssh` pipe to `/tmp/ts-spike/src-w5.1/wt-w5.1`. The
  toolchain was `/tmp/ts-spike/go/bin/go`, with the section 11 `GOPATH`,
  `GOMODCACHE` and `GOCACHE` under `/tmp/ts-spike`, and no `GOEXPERIMENT`.
  Runs held `flock /tmp/ts-spike/bench.lock`.
- **B5**, the rows the acceptance criteria read, ran on its own with
  `-count=10`: `call-{M,L}.txt`, and `benchstat-call-{M,L}.txt` with
  ± 0–3 %.
- **Everything else** ran in one `-bench . -count=5 ./...` run:
  `bench-{M,L}.txt`. benchstat needs 6 samples for a confidence interval,
  so `benchstat-{M,L}.txt` prints medians with "± ∞". The tables give the
  minimum and the median of the 5 samples. allocs/op is the minimum,
  which equals every sample except where a row notes otherwise.
- **The comparator.** `naive.Sonic` does the following:
  1. `sonic.Marshal` of `{State any; Model string; Questions json.RawMessage}`.
  2. `http.NewRequestWithContext`.
  3. The SDK client's own header template, set header by header.
  4. The client's per-attempt deadline.
  5. The same `Recorder`, called directly.
  6. `io.ReadAll`.
  7. `sonic.Unmarshal` into a `map[string]any`.

  `naive.StdJSON` is the same client with encoding/json, reported only.
  `TestNaiveRequestMatchesSDK` asserts that the two requests are equal for
  q3 and q20 (method, URL, Host, the six headers, the body byte for byte,
  its length and `GetBody`). `TestEncodeBodyMatchesNaive` asserts the same
  of B1's bodies, except `map`.
- **Load (R17).** (M): 4.52 → 3.97 for B5, and 9.47 → 5.17 for the full
  run, which waited 3 × 60 s for the load to fall below 16. (L): 0.40 → 1.25
  and 16.48 → 1.35, on 44 cores. No row is `noisy`.

### W5.1 findings

1. **AC-P6's time clause holds on amd64, the gate (G3). It fails on
   arm64, where the result is recorded (K18, K23).** Medians of 10:

   | Host | Shape | `call/sdk` | `call/naive` | sdk / naive |
   | --- | --- | ---: | ---: | ---: |
   | (L) | q3 | 6.104 µs | 6.885 µs | **0.887** |
   | (L) | q20 | 25.38 µs | 25.82 µs | 0.983 |
   | (M) | q3 | 4.821 µs | 3.616 µs | 1.333 |
   | (M) | q20 | 23.73 µs | 12.62 µs | 1.880 |

   The q20 margin on (L) is 1.7 %, against a spread of ± 0 %. The
   clause is stated on q3, and W3.4 asserts q3 on (L); q20 is recorded
   only (ruling R101). **This margin is a W5.3 input.** The per-call cost that Phase 3 adds (the retry state,
   telemetry) could flip q20 on amd64 before W3.4 freezes the clause.
   q20's gap is the decode of 20 answers: `Decode/result-20` takes
   21.56 µs against sonic-map's 16.79 µs on (L). Against R28b's prototype (critic probe, (M)
   1.19× and (L) 0.82×), the production client is 1.33× and 0.887×. The
   arm64 gap is the decode (finding 4). Against the encoding/json client,
   `call/sdk` is faster on both hosts: 0.585 and 0.363 (q3), 0.609 and
   0.331 (q20), in line with W0.5's R28 rows.
2. **NF3's allocation clause holds against the sonic comparator.**
   `call/sdk` makes 22 allocations and the floor 8 on both hosts, as
   `TestAllocWholeCall` pins. So SDK-own is 14 for q3 and 34 for q20.
   naive-own (naive minus the floor) is 46 (M) and 60 (L) for q3, and
   119 (M) and 239 (L) for q20. The ratios are 0.304 and 0.233 for q3,
   and 0.286 and 0.142 for q20, all below 0.5. With encoding/json (114
   and 517 allocations on both hosts), R28 had 0.135 and 0.068. sonic's
   generic decoder allocates differently by architecture (finding 3),
   which is why the naive counts differ between the hosts.
3. **AC-P2's "≤ 0.5 × naive" holds on the plain 3-answer fixture:
   `Decode/result` 4 allocations against `DecodeNaiveSonic/result` 26 (M)
   and 40 (L), 0.154 and 0.100.** W5.2 asserts it. The other fixtures are
   reported, as the plan says, and repeat W2.0's counts. Three are above
   0.5: `escaped-member-names` 0.707 (M) and 0.569 (L), and
   `deviation-lone-surrogate` 0.542 (M). All three pay for the lazy pass
   and for sonic unquoting escaped keys (NF2). Against encoding/json
   (85 allocations) the ratio is 0.047.
4. **K23 stands.** The SDK's decode against sonic's generic decode of
   `result.json` is 2.19× on (M) and 1.23× on (L). Across fixtures the
   range is 1.26–4.00× (M) and 0.86–2.63× (L). On (L) the floods are
   1.02× and 1.04×. W5.3's target on (M) is ≤ 1.5× for `result.json`.
5. **B1: the SDK's body encode allocates 0–2 times against sonic.Marshal's
   3–9.** A `RawJSON` state is appended verbatim: 47 ns against 2.1 µs at
   1 KiB on (M), and 0 allocations. That row measures a design choice: the
   SDK appends `RawJSON` unchecked (the caller's contract), and the naive
   client validates it. For text, struct and map states at 1 KiB the SDK
   is faster (text 760 ns against 1.706 µs (M) by medians, 742 ns against
   1.221 µs by minima, the naive row spreading 40 %; 889 ns against
   1.049 µs (L)).
   At 64 KiB and 1 MiB, `sonic.Marshal` is faster by 1.19–1.49× by medians
   and 1.15–1.49× by minima (the low end, text 64 KiB on (M), spreads 12 %
   on the SDK's side): text 1 MiB 605 against 473 µs (M), and 733 against
   581 µs (L). The difference is
   R48's `utf8.Valid` pass over the encoded state, which `sonic.Marshal`
   does not run. B1's text holds Japanese and an emoji, so that pass is
   not the ASCII fast path. The codec's own row shows its weight:
   `EncodeState/cjk/64KiB` takes 54.2 µs, of which the check alone
   (`…/check`) is 51.2 µs, on (L). This is R54's W5.3 target (validator
   ≤ 1 × sonic's encode time on CJK), and R54's candidate stands: a
   stdlib word-at-a-time ASCII scan, then sonic's SIMD `utf8.Validate` on
   the non-ASCII tail, run by the SDK on the encoded state so that R48's
   refusal is kept. sonic's `ValidateString` encoder option is not a
   candidate: it is also a second pass over the whole output, and it
   rewrites invalid UTF-8 to U+FFFD instead of refusing it
   (`sonic@v1.15.4/internal/encoder/encoder.go:225-247`; W0.3's text above
   says the same; review W5.1 MINOR 2).
6. **B3: the first attempt's assembly takes 9 allocations**, at 677 ns
   (M) and 1.199 µs (L). These are the encode, the `GetBody` method
   value, the URL copy, the 4 of `context.WithTimeout`, the body reader
   and `WithContext`. Preparing the section 5 set on every call doubles
   the count to 18 (1.451 and 2.579 µs).
7. **B6 over loopback HTTP/2 and TLS:**
   - One warm call takes 72.0 µs (M) and 79.9 µs (L), and no timed call
     dialled (`new-conns` 0).
   - A cold burst of 64 from a fresh client takes 2.93 ms (M) and
     3.45 ms (L), and each burst opened 1 connection on both hosts
     (`conns/op` 1.000). That count is a sanity check, not AC-P4's
     evidence: with the gate bypassed, the stock HTTP/2 pool alone also
     puts a cold burst on one loopback connection (review W5.1 MINOR 1,
     mutant M13b). AC-P4's evidence stays `internal/h2gate`'s `TestFanOut`.
     Since the review, B6 also reports the gate's own counters,
     `leaders/op` and `firstholds/op`; the rows after the W3.2 rebase
     record them.
   - The B/op and allocs/op of these rows include the server's.
8. **CodSpeed discovery.** At 6afc8a3, `go test -list 'Benchmark.*' ./...`
   listed 12 benchmark functions: 8 in the root and 4 in
   `internal/codec` (`results/list-M.txt`). At 1ca60e1 it lists 14:
   B4 adds `BenchmarkRetryAfter` and `BenchmarkBackoff`
   (`results/list-M-1ca60e1.txt`). A `-benchtime 1x` run of all of them
   gave 125 results on each host and exited 0 (W5.1-12, -13). The local
   `codspeed run --skip-upload -m walltime -- go test -bench=. ./...` on
   (M) passes no `-run` (R5, R5-corr). It ran all 12, gave 118 results,
   exited 0 and printed no warning (`results/codspeed-M.txt`). The
   runner reports 0 B/op and 0 allocs/op by design, so allocations are
   read from the `go test` rows. K7 keeps CodSpeed report-only. AC-P7
   is W5.4's pull-request run.
9. **Gates.** `_spikes/w5.1/gates.sh` checked each of the five code and
   docs commits alone on (M) (`results/gates-M.txt`): build, vet, the
   section 11 lint chain without govulncheck, and `go test -race`. The
   lint chain with govulncheck
   (`go run golang.org/x/vuln/cmd/govulncheck@latest`) passed on the Go
   tree of 6afc8a3, which 5d5afc8 leaves unchanged, at 10:38:23 and
   11:23:52 JST, and in the review at 5d5afc8 at 11:31:22 JST. The one `go test -race -count=1 ./...` on
   (L) that R62 asks for, since the comparator pins sonic's behaviour,
   passed (`results/race-L.txt`). After the rebase onto 87d1ac7,
   gates.sh passed each of the ten commits up to B4 alone on (M)
   (W5.1-15). Build, vet and `go test -race` passed at 1ca60e1 on (L)
   (W5.1-14).
10. **B4: reading a server's wait and computing the backoff cost well
    under a microsecond and allocate nothing, except the date form.**
    Medians of 10 on (M) and (L):
    - `RetryAfter/seconds`: 88.8 ns and 166.7 ns.
    - `RetryAfter/ms`: 79.9 ns and 158.3 ns.
    - `RetryAfter/http-date`: 267.2 ns and 455.1 ns, with 1 allocation of
      32 B from `http.ParseTime`.
    - `Backoff` at retry 1, 6 and 1000: 53.5–54.6 ns and 86.8–95.9 ns.
    - `Backoff/schedule`, `DefaultRetry`'s two waits: 117.8 ns and
      190.2 ns.

    The cap row costs no more than retry 1, because the cap is taken in
    log2 space before any doubling. These costs are recorded, not
    targets. Each runs once per retry, beside a wait of at least
    hundreds of milliseconds. They are the same order as the Rust
    reference's B4 (39–118 ns on macOS, 90–262 ns on Linux).
11. **B6 re-run after b3fd5db: no change beyond noise.** At 1ca60e1, the
    median of 10 over the median of 5 at 6afc8a3 is:
    - `Loopback/call`: 0.982 (M) and 1.004 (L).
    - `Loopback/cold-fanout-64`: 0.911 (M) and 0.976 (L).

    b3fd5db changed how the loopback server closes a connection after a
    GOAWAY. B6 never closes one mid-run, so no change was expected. The
    (M) cold burst's 9 % is within that host's spread (min 2.507 against
    median 2.670 ms). Every burst on both hosts had `leaders/op`,
    `firstholds/op` and `conns/op` of 1.000: the gate led one dial and
    held the first request until its response headers. `new-conns` for
    the warm call stayed 0.

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W5.1-01 | 2026-09-26 10:45:29 JST | W5.1 B5 `call/sdk` against its floor and `call/naive` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 4.52 → 3.97 | `BASE=$BASE GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $O $SP/bench.lock call-M -run '^$' -bench '^BenchmarkCall$' -benchmem -count=10 .` | q3: sdk 4.821 µs, naive 3.616 µs (sdk/naive **1.333**), naive-json 8.244 µs, floor 467.1 ns; q20: sdk 23.73 µs, naive 12.62 µs (1.880); allocs sdk 22 / floor 8 / naive 54 / naive-json 114 (q20: 42 / 8 / 127 / 517) | arm64 recorded, not gated (G3, K18, K23); `results/call-M.txt`, `results/benchstat-call-M.txt` |
| W5.1-02 | 2026-09-26 10:58:11 JST | W5.1 B1–B3, B5, B6 and the earlier benchmarks | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.47 → 5.17 | `BASE=$BASE GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $O $SP/bench.lock bench-M -run '^$' -bench . -benchmem -count=5 ./...` | 590 result lines (118 benchmarks × 5); AC-P2 `Decode/result` 4 allocs against `DecodeNaiveSonic/result` 26 (**0.154**); tables below | waited 3 × 60 s for load ≤ 16; `results/bench-M.txt`, `results/benchstat-M.txt` |
| W5.1-03 | 2026-09-26 01:43:59 UTC | W5.1 B5 `call/sdk` against its floor and `call/naive` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.40 → 1.25 | `BASE=$BASE MAXLOAD=44 sh $R '(L)' $O /tmp/ts-spike/bench.lock call-L -run '^$' -bench '^BenchmarkCall$' -benchmem -count=10 .` | q3: sdk 6.104 µs, naive 6.885 µs (sdk/naive **0.887**, AC-P6 time holds), naive-json 16.81 µs, floor 760.6 ns; q20: sdk 25.38 µs, naive 25.82 µs (0.983, margin 1.7 %: W5.3 input); allocs sdk 22 / floor 8 / naive 68 / naive-json 114 (q20: 42 / 8 / 247 / 517) | amd64 = the gate (G3); `results/call-L.txt`, `results/benchstat-call-L.txt` |
| W5.1-04 | 2026-09-26 01:54:31 UTC | W5.1 B1–B3, B5, B6 and the earlier benchmarks | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 16.48 → 1.35 | `BASE=$BASE MAXLOAD=44 sh $R '(L)' $O /tmp/ts-spike/bench.lock bench-L -run '^$' -bench . -benchmem -count=5 ./...` | 590 result lines; AC-P2 `Decode/result` 4 allocs against `DecodeNaiveSonic/result` 40 (**0.100**); tables below | waited 1 × 60 s; `results/bench-L.txt`, `results/benchstat-L.txt` |
| W5.1-05 | 2026-09-26 02:06:14 UTC | W5.1 R62 race run (the comparator pins sonic behaviour) | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.35 → 1.29 | `go test -race -count=1 ./...` | ok for all 6 packages, `internal/testsupport/naive` included | not a timing row; `results/race-L.txt` |
| W5.1-06 | 2026-09-26 11:11:16 JST | W5.1 CodSpeed discovery | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | `GOEXPERIMENT=nosimd,noruntimesecret go test -list 'Benchmark.*' ./...` | 12 functions: root `Assembly`, `Call`, `HeaderTemplateClone`, `EncodeBody`, `Loopback`, `Noop`, `Prepare`, `FalsyJSON`; codec `Decode`, `DecodeNaiveSonic`, `DecodeNaiveJSON`, `EncodeState` | `results/list-M.txt` |
| W5.1-07 | 2026-09-26 11:11:27 JST | W5.1 local CodSpeed run (R5-corr: no `-run`) | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.67 → 6.71 | `GOEXPERIMENT=nosimd,noruntimesecret codspeed run --skip-upload -m walltime -- go test -bench=. ./...` under `flock $SP/bench.lock` | codspeed-runner 5.3.1, exit 0, 118 benchmark results from the 12 functions, no warning; `Call/sdk` 5.458 µs, `Call/naive` 4.254 µs (one sample each, walltime) | report-only (K7); the runner prints 0 B/op; `results/codspeed-M.txt` |
| W5.1-08 | 2026-09-26 11:21:25 JST | W5.1 gates per commit | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | `sh _spikes/w5.1/gates.sh <scratchpad> 9e45600 ae69b05 23b5a6c 89b6109 6afc8a3` | PASS × 5 (11:21:25 → 11:22:31 JST): build, vet, gofumpt -extra, modernize, golangci-lint, staticcheck, tidy -diff, `go test -race` | not a timing row; `results/gates-M.txt` |
| W5.1-09 | 2026-09-26 11:55:16 JST | W5.1 B4, and the B6 re-run after b3fd5db | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 13.84 → 15.34 | `BASE=1ca60e1 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $O $SP/bench.lock b46-M -run '^$' -bench '^Benchmark(RetryAfter\|Backoff\|Loopback)$' -benchmem -count=10 .` | RetryAfter seconds 88.8 ns, ms 79.9 ns, http-date 267.2 ns (1 alloc); Backoff 53.5–54.6 ns, schedule 117.8 ns (0 allocs); Loopback/call 70.73 µs (new-conns 0), cold-fanout-64 2.670 ms (conns, leaders, firstholds 1.000 /op) | medians of 10; at 1ca60e1 (87d1ac7 + W5.1); `results/b46-M.txt` |
| W5.1-10 | 2026-09-26 02:55:10 UTC | W5.1 B4, and the B6 re-run after b3fd5db | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.13 → 0.92 | `BASE=1ca60e1 MAXLOAD=44 sh $R '(L)' $O /tmp/ts-spike/bench.lock b46-L -run '^$' -bench '^Benchmark(RetryAfter\|Backoff\|Loopback)$' -benchmem -count=10 .` | RetryAfter seconds 166.7 ns, ms 158.3 ns, http-date 455.1 ns (1 alloc); Backoff 86.8–95.9 ns, schedule 190.2 ns (0 allocs); Loopback/call 80.18 µs (new-conns 0), cold-fanout-64 3.369 ms (conns, leaders, firstholds 1.000 /op) | medians of 10; `results/b46-L.txt` |
| W5.1-11 | 2026-09-26 11:55:16 JST | W5.1 CodSpeed discovery after B4 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | `GOEXPERIMENT=nosimd,noruntimesecret go test -list 'Benchmark.*' ./...` | 14 functions: W5.1-06's 12 plus `RetryAfter` and `Backoff` | `results/list-M-1ca60e1.txt` |
| W5.1-12 | 2026-09-26 11:57:05 JST | W5.1 every benchmark once | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 15.34 → 15.34 | `BASE=1ca60e1 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $O $SP/bench.lock onex-M -run '^$' -bench . -benchtime 1x -benchmem ./...` | 125 results, exit 0 | not a timing row; `results/onex-M.txt` |
| W5.1-13 | 2026-09-26 02:57:00 UTC | W5.1 every benchmark once | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.92 → 0.92 | `BASE=1ca60e1 MAXLOAD=44 sh $R '(L)' $O /tmp/ts-spike/bench.lock onex-L -run '^$' -bench . -benchtime 1x -benchmem ./...` | 125 results, exit 0 | not a timing row; `results/onex-L.txt` |
| W5.1-14 | 2026-09-26 02:57:01 UTC | W5.1 R62 gate after the rebase | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.92 → 1.38 | `go build ./... && go vet ./... && go test -race -count=1 ./...` | ok for all 6 packages | not a timing row; `results/race-L-1ca60e1.txt` |
| W5.1-15 | 2026-09-26 11:57:55 JST | W5.1 gates per commit after the rebase | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | `sh _spikes/w5.1/gates.sh <scratchpad> $(git rev-list --reverse 87d1ac7..1ca60e1)` | PASS × 10 (4e3dd73 … 1ca60e1, → 12:00:54 JST): build, vet, gofumpt -extra, modernize, golangci-lint, staticcheck, tidy -diff, `go test -race` | not a timing row; `results/gates-M-1ca60e1.txt` |
| W5.1-16 | 2026-09-26 12:17:48 JST | W5.1 G5 move: discovery | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | `GOEXPERIMENT=nosimd,noruntimesecret go test -list 'Benchmark.*' ./...` | 15 function names in 3 packages: root 6, `internal/benchmark` 5, `internal/codec` 4 (`BenchmarkLoopback` in two, one arm each) | at 438a12d; `results/list-M-438a12d.txt` |
| W5.1-17 | 2026-09-26 12:17:48 JST | W5.1 G5 move: every benchmark once | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 10.48 → 10.48 | `BASE=438a12d GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $O $SP/bench.lock onex-M-438a12d -run '^$' -bench . -benchtime 1x -benchmem ./...` | 125 results (root 57, `internal/benchmark` 16, `internal/codec` 52), exit 0; the same 125 names as W5.1-12 | not a timing row; `results/onex-M-438a12d.txt` |
| W5.1-18 | 2026-09-26 03:17:43 UTC | W5.1 G5 move: every benchmark once | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.01 → 0.01 | `BASE=438a12d MAXLOAD=44 sh $R '(L)' $O /tmp/ts-spike/bench.lock onex-L-438a12d -run '^$' -bench . -benchtime 1x -benchmem ./...` | 125 results, exit 0 | not a timing row; `results/onex-L-438a12d.txt` |
| W5.1-19 | 2026-09-26 12:17:49 JST | W5.1 G5 move: smoke of `call/sdk` in its new package | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 10.48 → 10.08 | `BASE=438a12d GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $O $SP/bench.lock smoke-call-M -run '^$' -bench '^BenchmarkCall$/^sdk$' -benchmem -count=10 ./internal/benchmark/` | `internal/benchmark` `BenchmarkCall/sdk` 4.994 µs ± 8 % (min 4.768), 22 allocs/op, 2.901 KiB/op; against W5.1-01's root row, 4.821 µs ± 3 % (min 4.677), 22 allocs/op: +3.6 % by median at load 10.5 against 4.5, inside this row's ± 8 %; the move did not move the number | arm64 recorded; `results/smoke-call-M.txt` |
| W5.1-20 | 2026-09-26 03:17:45 UTC | W5.1 G5 move: R62 gate | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.01 → 0.29 | `go build ./... && go vet ./... && go test -race -count=1 ./...` | ok for all 7 packages, `internal/benchmark` included | not a timing row; `results/race-L-438a12d.txt` |
| W5.1-21 | 2026-09-26 12:18:32 JST | W5.1 G5 move: gates per commit | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | `sh _spikes/w5.1/gates.sh <scratchpad> aecf7cf 438a12d` | PASS × 2 (→ 12:19:14 JST) | not a timing row; `results/gates-M-438a12d.txt` |
| W5.1-22 | 2026-09-26 04:36:44 UTC | K35: CodSpeed on main loses rows since W5.1 | `ubuntu-26.04` | `go1.27.1 linux/amd64` | not printed by the job | – | `bench.yaml` at f048059: `CodSpeedHQ/action@v5` (runner 5.2.1, go runner 1.3.0), `mode: walltime`, `go test -bench=. ./...` | 125 rows printed; 11 `failed to write raw results: write /tmp/profile.*.out/raw_results/<hash>.json: disk quota exceeded`, every `EncodeState` row after `ascii/1KiB/encode`; CodSpeed holds 114. Raw volume estimated from each row's iterations at the overlay's indented JSON (about 16 B per iteration): 5.90 GiB (root 57 rows 2.75, `internal/benchmark` 16 rows 1.63, `internal/codec` 52 rows 1.52), of which `EncodeState/ascii/1KiB/check` (44097378 iterations) is 631 MiB. At 87d1ac7, before W5.1: 58 rows, 3.33 GiB, no failure | GitHub runs 36218293900 (f048059), 36216837837 (1f694b0), 36215674827 (ca226bb) fail the same 11 rows; 36212995431 (87d1ac7) is clean; CodSpeed run 6ab74dffc9fea79936132129; the estimate is `_spikes/w5.1/k35-rawvolume.py` over the job logs, `results/k35-rawvolume.txt`; not a timing row |
| W5.1-23 | 2026-09-26 05:08:12 UTC | K35 probe: what bounds `/tmp` | `ubuntu-26.04` | `go1.27.1 linux/amd64` | not printed by the job | – | f048059's job plus probe steps (f1b1df8 on the throwaway branch `wave/p5-codspeedfix-probe`, since deleted): `findmnt`/`df`/`systemctl cat tmp.mount`, `dd` into `/tmp` as `runner` and as root, `/tmp` sampled every 10 s during the CodSpeed step | `/tmp` is a tmpfs of 7992 MiB (`size=50%` of 15983 MiB RAM) mounted `usrquota` (`x-systemd.graceful-option=usrquota`); `dd` as `runner` (uid 1001) stops at 6356 MiB with `Disk quota exceeded`, at 6394 MiB used, 80 %; as root at 7954 MiB with `No space left on device`; `/` is ext4 with 91 GiB free and holds `$RUNNER_TEMP`. During the run the raw results reach 5305 MiB before `ascii/1KiB/check`, whose file stops at 479076352 bytes when `/tmp` reaches 6394 MiB (with 598 MiB of `go-build*` and the go runner's temp caches); the 10 later files are 0 bytes; the results files hold 57 + 16 + 41 = 114 rows | GitHub run 36219879628, CodSpeed run 6ab75593d47c5a3890bbc64f (114); the quota is the runner user's tmpfs quota, not the size of `/tmp` and not a CodSpeed limit; `results/k35-probe-CI.txt` |
| W5.1-24 | 2026-09-26 14:15:56 JST | K35 guard, shell-level proof | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | under `flock $SP/bench.lock`: `TMPDIR=<dir>/ GOEXPERIMENT=nosimd,noruntimesecret codspeed run --skip-upload -m walltime -- 'set -o pipefail; go test -bench=. -benchtime=10ms ./... 2>&1 \| tee <dir>/codspeed.log'`, then the guard step's script (`yq e '.jobs.codspeed.steps[-1].run' .github/workflows/bench.yaml`) with `RUNNER_TEMP=<dir>` | full run: exit 0, `125 rows; CodSpeed results: 125 rows in 3 files`; results of a `-bench 'BenchmarkFalsyJSON\|BenchmarkEncodeState'` run: exit 1, 110 rows listed missing (15 of 125); the full run's log plus one `failed to write raw results … disk quota exceeded` line: exit 1 on the grep while all 125 rows match. A result's `rounds` equals the row's printed iteration count (`EncodeState/ascii/1KiB/check` 618011 and 618011, `iter_per_round` 1) | codspeed-runner 5.3.1; the three guard runs again at 14:33:03 JST in `results/k35-guard-M.txt`; not a timing row |
| W5.1-25 | 2026-09-26 05:18:24 UTC | K35 fix: `TMPDIR` on the runner's disk, and the guard | `ubuntu-26.04` (AMD EPYC 7763) | `go1.27.1 linux/amd64` | not printed by the job | – | `bench.yaml` at 57bee0f (`wave/p5-codspeedfix`, `workflow_dispatch`) | 125 rows printed, no `failed to … raw results` line; the profile folder is `/home/runner/work/_temp/profile.*.out`; guard: `go test -list expansion: 125 rows; CodSpeed results: 125 rows in 3 files`, 30 s with its build; CodSpeed holds 125, the 12 `EncodeState` rows among them. Against W5.1-23's run on the same CPU with the folder on `/tmp`, CodSpeed's comparison marks 124 rows unchanged and B6 `Loopback/cold-fanout-64` 4.8 → 5.4 ms (−10.4 %), a row R101 keeps out of every gate | GitHub run 36220344551 (success), CodSpeed run 6ab757c1b5bd728624a336a7 |
| W5.1-26 | 2026-09-26 05:18:25 UTC | K35 guard bites on CI | `ubuntu-26.04` (AMD EPYC 9V74) | `go1.27.1 linux/amd64` | not printed by the job | – | 57bee0f's `bench.yaml` without the `TMPDIR` lines (9deffbe on the throwaway branch `wave/p5-codspeedfix-bite`, since deleted) | the CodSpeed step passes with the 11 `EncodeState` lines `failed to write raw results: … disk quota exceeded` and uploads 114 rows; the guard fails the job with `the go runner could not write raw results (K35)` and `114 of 125 benchmarks reached the results (K35)`, and its diff names the 11 rows | GitHub run 36220346065 (failure at the guard step), CodSpeed run 6ab757cdbffa2b9b6c44cd57 (114) |

<a id="w51-tables"></a>

### W5.1 tables

Printed by `_spikes/w5.1/render.py` from the raw files (min / median of the samples; allocs/op the minimum).

B5, call-{M,L}.txt (-count=10):

| Benchmark | (M) ns/op min / median | (L) ns/op min / median | (M) B/op / allocs | (L) B/op / allocs |
| --- | ---: | ---: | ---: | ---: |
| `Call/sdk` | 4.677 µs / 4.821 µs | 6.081 µs / 6.104 µs | 2998 / 22 | 3020 / 22 |
| `Call/floor` | 453.8 ns / 467.1 ns | 757.8 ns / 760.6 ns | 678 / 8 | 692 / 8 |
| `Call/naive` | 3.473 µs / 3.616 µs | 6.868 µs / 6.885 µs | 11276 / 54 | 8948 / 68 |
| `Call/naive-json` | 8.091 µs / 8.244 µs | 16.759 µs / 16.811 µs | 7901 / 114 | 7946 / 114 |
| `Call/sdk-q20` | 23.227 µs / 23.733 µs | 25.312 µs / 25.383 µs | 11370 / 42 | 11443 / 42 |
| `Call/floor-q20` | 487.3 ns / 497.8 ns | 768.6 ns / 770.8 ns | 688 / 8 | 690 / 8 |
| `Call/naive-q20` | 12.060 µs / 12.625 µs | 25.729 µs / 25.817 µs | 40216 / 127 | 34626 / 247 |
| `Call/naive-json-q20` | 38.678 µs / 38.944 µs | 76.526 µs / 76.720 µs | 31010 / 517 | 31233 / 517 |

| Ratio (median ns/op) | (M) / (L) |
| --- | ---: |
| q3 sdk / naive (AC-P6) | 1.333 / 0.887 |
| q3 sdk / naive-json | 0.585 / 0.363 |
| q3 allocs sdk / naive | 0.407 / 0.324 |
| q20 sdk / naive (AC-P6) | 1.880 / 0.983 |
| q20 sdk / naive-json | 0.609 / 0.331 |
| q20 allocs sdk / naive | 0.331 / 0.170 |

B2, bench-{M,L}.txt (-count=5): decode vs naive, median ns/op and allocs/op:

| Fixture | (M) sdk / sonic-map / json-map | (L) sdk / sonic-map / json-map | allocs (M) sdk / sonic / json | allocs (L) sdk / sonic / json | time sdk/sonic (M) / (L) | allocs sdk/sonic (M) / (L) |
| --- | --- | --- | --- | --- | ---: | ---: |
| `result` | 3.455 µs / 1.575 µs / 5.575 µs | 3.437 µs / 2.788 µs / 10.494 µs | 4 / 26 / 85 | 4 / 40 / 85 | 2.194 / 1.233 | 0.154 / 0.100 |
| `type-last` | 3.555 µs / 1.872 µs / 5.532 µs | 3.445 µs / 2.823 µs / 10.518 µs | 4 / 26 / 85 | 4 / 40 / 85 | 1.899 / 1.220 | 0.154 / 0.100 |
| `duplicates` | 11.319 µs / 4.177 µs / 13.232 µs | 11.169 µs / 6.793 µs / 25.370 µs | 16 / 55 / 212 | 16 / 89 / 212 | 2.710 / 1.644 | 0.291 / 0.180 |
| `result-20` | 21.454 µs / 9.307 µs / 33.409 µs | 21.558 µs / 16.785 µs / 63.503 µs | 24 / 94 / 483 | 24 / 214 / 483 | 2.305 / 1.284 | 0.255 / 0.112 |
| `score-flood-mini` | 14.567 µs / 6.466 µs / 24.030 µs | 14.147 µs / 11.817 µs / 45.489 µs | 21 / 96 / 386 | 21 / 137 / 386 | 2.253 / 1.197 | 0.219 / 0.153 |
| `escaped-names` | 5.490 µs / 2.085 µs / 7.815 µs | 5.006 µs / 4.064 µs / 15.072 µs | 10 / 38 / 120 | 10 / 57 / 120 | 2.633 / 1.232 | 0.263 / 0.175 |
| `escaped-member-names` | 7.087 µs / 1.796 µs / 6.345 µs | 7.328 µs / 3.406 µs / 12.350 µs | 29 / 41 / 101 | 29 / 51 / 101 | 3.946 / 2.151 | 0.707 / 0.569 |
| `structured-legend` | 4.123 µs / 1.114 µs / 3.410 µs | 4.763 µs / 1.950 µs / 6.542 µs | 11 / 24 / 56 | 11 / 27 / 56 | 3.701 / 2.443 | 0.458 / 0.407 |
| `deviation-lone-surrogate` | 4.543 µs / 1.135 µs / 3.704 µs | 5.146 µs / 1.956 µs / 7.301 µs | 13 / 24 / 61 | 13 / 27 / 61 | 4.003 / 2.631 | 0.542 / 0.481 |
| `unknown-answer-type` | 1.416 µs / 842.1 ns / 2.437 µs | 1.326 µs / 1.360 µs / 4.643 µs | 1 / 18 / 39 | 1 / 20 / 39 | 1.682 / 0.975 | 0.056 / 0.050 |
| `parity-big-exp-unknown` | 1.283 µs / – / – | 1.236 µs / – / – | 1 / – / – | 1 / – / – | no naive row | no naive row |
| `no-answers` | 585.1 ns / 465.9 ns / 994.7 ns | 521.0 ns / 604.3 ns / 1.847 µs | 0 / 12 / 16 | 0 / 9 / 16 | 1.256 / 0.862 | 0.000 / 0.000 |
| `structured-legend-flood-1k` | 700.063 µs / 195.360 µs / 1.018 ms | 616.492 µs / 603.684 µs / 1.801 ms | 90 / 2036 / 17151 | 90 / 6574 / 17151 | 3.583 / 1.021 | 0.044 / 0.014 |
| `structured-legend-flood-10k` | 7.158 ms / 1.809 ms / 9.782 ms | 6.022 ms / 5.820 ms / 17.639 ms | 686 / 20095 / 170530 | 686 / 65193 / 170529 | 3.957 / 1.035 | 0.034 / 0.011 |

B1, bench-{M,L}.txt (-count=5):

| Benchmark | (M) ns/op min / median | (L) ns/op min / median | (M) B/op / allocs | (L) B/op / allocs |
| --- | ---: | ---: | ---: | ---: |
| `EncodeBody/map/1KiB/naive` | 1.484 µs / 1.498 µs | 1.398 µs / 1.399 µs | 1657 / 4 | 1692 / 4 |
| `EncodeBody/map/1KiB/naive-json` | 1.850 µs / 1.875 µs | 3.436 µs / 3.437 µs | 1554 / 5 | 1561 / 5 |
| `EncodeBody/map/1KiB/sdk` | 1.246 µs / 1.263 µs | 1.224 µs / 1.225 µs | 114 / 2 | 115 / 2 |
| `EncodeBody/map/1MiB/naive` | 422.613 µs / 436.930 µs | 558.303 µs / 589.332 µs | 3651093 / 9 | 3642224 / 9 |
| `EncodeBody/map/1MiB/naive-json` | 937.922 µs / 954.402 µs | 1.537 ms / 1.557 ms | 1130145 / 6 | 1107276 / 5 |
| `EncodeBody/map/1MiB/sdk` | 603.263 µs / 608.045 µs | 731.249 µs / 734.255 µs | 2003 / 2 | 2415 / 2 |
| `EncodeBody/map/64KiB/naive` | 28.757 µs / 30.147 µs | 31.084 µs / 31.231 µs | 77165 / 4 | 78445 / 4 |
| `EncodeBody/map/64KiB/naive-json` | 63.805 µs / 64.790 µs | 102.213 µs / 102.741 µs | 74224 / 5 | 74531 / 5 |
| `EncodeBody/map/64KiB/sdk` | 38.166 µs / 38.700 µs | 46.393 µs / 46.449 µs | 125 / 2 | 133 / 2 |
| `EncodeBody/rawjson/1KiB/naive` | 2.002 µs / 2.112 µs | 2.947 µs / 2.950 µs | 2739 / 4 | 2785 / 4 |
| `EncodeBody/rawjson/1KiB/naive-json` | 4.044 µs / 4.322 µs | 6.414 µs / 6.426 µs | 2738 / 6 | 2743 / 6 |
| `EncodeBody/rawjson/1KiB/sdk` | 45.4 ns / 47.0 ns | 74.8 ns / 75.5 ns | 0 / 0 | 0 / 0 |
| `EncodeBody/rawjson/1MiB/naive` | 1.275 ms / 1.318 ms | 2.073 ms / 2.079 ms | 2203569 / 8 | 2202403 / 8 |
| `EncodeBody/rawjson/1MiB/naive-json` | 3.109 ms / 3.207 ms | 4.553 ms / 4.570 ms | 2188959 / 6 | 2181322 / 6 |
| `EncodeBody/rawjson/1MiB/sdk` | 15.367 µs / 15.632 µs | 48.773 µs / 49.081 µs | 13 / 0 | 44 / 0 |
| `EncodeBody/rawjson/64KiB/naive` | 81.460 µs / 82.336 µs | 132.671 µs / 133.381 µs | 151677 / 4 | 155488 / 4 |
| `EncodeBody/rawjson/64KiB/naive-json` | 197.169 µs / 205.789 µs | 301.849 µs / 302.287 µs | 147861 / 6 | 148106 / 6 |
| `EncodeBody/rawjson/64KiB/sdk` | 922.2 ns / 939.8 ns | 1.763 µs / 1.770 µs | 0 / 0 | 0 / 0 |
| `EncodeBody/struct/1KiB/naive` | 1.171 µs / 1.179 µs | 1.127 µs / 1.132 µs | 1568 / 3 | 1596 / 3 |
| `EncodeBody/struct/1KiB/naive-json` | 1.787 µs / 1.790 µs | 3.346 µs / 3.354 µs | 1553 / 5 | 1556 / 5 |
| `EncodeBody/struct/1KiB/sdk` | 939.1 ns / 945.1 ns | 948.3 ns / 952.4 ns | 16 / 1 | 16 / 1 |
| `EncodeBody/struct/1MiB/naive` | 424.823 µs / 437.542 µs | 552.520 µs / 568.064 µs | 3650285 / 8 | 3633312 / 7 |
| `EncodeBody/struct/1MiB/naive-json` | 942.793 µs / 964.164 µs | 1.550 ms / 1.563 ms | 1135076 / 5 | 1103069 / 5 |
| `EncodeBody/struct/1MiB/sdk` | 601.353 µs / 606.480 µs | 734.821 µs / 735.559 µs | 1932 / 1 | 2327 / 1 |
| `EncodeBody/struct/64KiB/naive` | 28.785 µs / 29.653 µs | 32.363 µs / 32.766 µs | 76734 / 3 | 78520 / 3 |
| `EncodeBody/struct/64KiB/naive-json` | 63.869 µs / 64.764 µs | 111.137 µs / 113.550 µs | 74164 / 5 | 74410 / 5 |
| `EncodeBody/struct/64KiB/sdk` | 38.327 µs / 38.511 µs | 46.015 µs / 46.268 µs | 28 / 1 | 30 / 1 |
| `EncodeBody/text/1KiB/naive` | 1.221 µs / 1.706 µs | 1.042 µs / 1.049 µs | 1570 / 3 | 1596 / 3 |
| `EncodeBody/text/1KiB/naive-json` | 1.699 µs / 1.772 µs | 2.945 µs / 2.949 µs | 1553 / 4 | 1556 / 4 |
| `EncodeBody/text/1KiB/sdk` | 742.2 ns / 760.2 ns | 887.3 ns / 888.8 ns | 16 / 1 | 16 / 1 |
| `EncodeBody/text/1MiB/naive` | 433.010 µs / 473.231 µs | 565.823 µs / 581.012 µs | 3651718 / 8 | 3633341 / 7 |
| `EncodeBody/text/1MiB/naive-json` | 944.464 µs / 966.553 µs | 1.556 ms / 1.566 ms | 1148568 / 4 | 1106731 / 4 |
| `EncodeBody/text/1MiB/sdk` | 601.933 µs / 604.939 µs | 728.772 µs / 732.629 µs | 1921 / 1 | 2313 / 1 |
| `EncodeBody/text/64KiB/naive` | 33.589 µs / 36.397 µs | 32.547 µs / 33.002 µs | 77267 / 3 | 78505 / 3 |
| `EncodeBody/text/64KiB/naive-json` | 71.674 µs / 72.657 µs | 109.679 µs / 111.960 µs | 74187 / 4 | 74426 / 4 |
| `EncodeBody/text/64KiB/sdk` | 38.776 µs / 43.315 µs | 45.698 µs / 45.894 µs | 24 / 1 | 30 / 1 |

B3, bench-{M,L}.txt (-count=5):

| Benchmark | (M) ns/op min / median | (L) ns/op min / median | (M) B/op / allocs | (L) B/op / allocs |
| --- | ---: | ---: | ---: | ---: |
| `Assembly/request` | 674.4 ns / 676.8 ns | 1.193 µs / 1.199 µs | 896 / 9 | 909 / 9 |
| `Assembly/prepare-and-request` | 1.419 µs / 1.451 µs | 2.575 µs / 2.579 µs | 1994 / 18 | 2039 / 18 |

B6, bench-{M,L}.txt (-count=5):

| Benchmark | (M) ns/op min / median | (L) ns/op min / median | (M) B/op / allocs | (L) B/op / allocs | metric (M) / (L) |
| --- | ---: | ---: | ---: | ---: | ---: |
| `Loopback/call` | 69.990 µs / 72.009 µs | 77.677 µs / 79.852 µs | 14130 / 111 | 13843 / 111 | 0.000 / 0.000 |
| `Loopback/cold-fanout-64` | 2.717 ms / 2.932 ms | 3.422 ms / 3.452 ms | 1053189 / 8592 | 1078480 / 8586 | 1.000 / 1.000 |

B4 at 1ca60e1 (on W3.2's landing), b46-{M,L}.txt (-count=10):

| Benchmark | (M) ns/op min / median | (L) ns/op min / median | (M) B/op / allocs | (L) B/op / allocs |
| --- | ---: | ---: | ---: | ---: |
| `RetryAfter/seconds` | 87.3 ns / 88.8 ns | 165.1 ns / 166.7 ns | 0 / 0 | 0 / 0 |
| `RetryAfter/ms` | 78.4 ns / 79.9 ns | 157.5 ns / 158.3 ns | 0 / 0 | 0 / 0 |
| `RetryAfter/http-date` | 262.3 ns / 267.2 ns | 453.5 ns / 455.1 ns | 32 / 1 | 32 / 1 |
| `Backoff/retry-1` | 53.0 ns / 54.0 ns | 90.8 ns / 95.9 ns | 0 / 0 | 0 / 0 |
| `Backoff/retry-6` | 52.6 ns / 53.5 ns | 86.8 ns / 86.8 ns | 0 / 0 | 0 / 0 |
| `Backoff/retry-1000` | 52.7 ns / 54.6 ns | 86.7 ns / 86.8 ns | 0 / 0 | 0 / 0 |
| `Backoff/schedule` | 115.6 ns / 117.8 ns | 183.4 ns / 190.2 ns | 0 / 0 | 0 / 0 |

B6 re-run at 1ca60e1 (after b3fd5db's graceful close), b46-{M,L}.txt (-count=10):

| Benchmark | (M) ns/op min / median | (L) ns/op min / median | (M) B/op / allocs | (L) B/op / allocs | metric (M) / (L) |
| --- | ---: | ---: | ---: | ---: | ---: |
| `Loopback/call` | 67.421 µs / 70.734 µs | 78.597 µs / 80.182 µs | 13635 / 111 | 13658 / 111 | 0.000 / 0.000 |
| `Loopback/cold-fanout-64` | 2.507 ms / 2.670 ms | 3.276 ms / 3.369 ms | 1000994 / 8590 | 1017272 / 8585 | 1.000 / 1.000 |

| `Loopback/cold-fanout-64` metric, median | (M) / (L) |
| --- | ---: |
| conns/op | 1.000 / 1.000 |
| leaders/op | 1.000 / 1.000 |
| firstholds/op | 1.000 / 1.000 |

| B6 median ns/op, 1ca60e1 / 6afc8a3 | (M) / (L) |
| --- | ---: |
| `Loopback/call` | 0.982 / 1.004 |
| `Loopback/cold-fanout-64` | 0.911 / 0.976 |

### G5: the benchmarks leave the root package (438a12d)

Owner directive G5 moved the root package's benchmark files to a new
package, `internal/benchmark`, which has no code outside its tests
(aecf7cf, 438a12d; no production file changed). Nothing was re-measured
except a smoke run of `call/sdk` in its new package (W5.1-19), which
matches W5.1-01 within noise. The rows above keep the names they were
measured under. A benchmark's CodSpeed identity carries its package path,
so the moved rows start a new history, and K7's count for
`BenchmarkCall/sdk` restarts with the landing that carries the move.
CodSpeed is report-only, so no gate moves.

Where each benchmark went, and why six stay in the root's
`bench_internal_test.go`:

| Benchmark | Disposition | How, or why not |
| --- | --- | --- |
| `BenchmarkCall` (B5) | moved via the exported API | One real call through a recording transport comes first. `floor` sends that request's bytes again through `testsupport.FloorCall`, which `TestAllocWholeCall` now shares (MINOR 4: the copy and its pin are gone). The naive client takes that request's URL, header template and question bytes, with `DefaultModel` and `DefaultTimeout`. `TestNaiveRequestMatchesSDK` moved with it. |
| `BenchmarkRetryAfter` (B4) | moved via the exported API | `(*APIError).RetryAfter` on a real 429; the header names are written out. |
| `BenchmarkLoopback/call` (B6) | moved via the exported API | The loopback server is `testsupport.NewFixtureServer`, a helper moved to an internal package and shared with the root's arm. |
| `BenchmarkHeaderTemplateClone` | moved via the exported API | It clones the header a first attempt sent, which is the template itself (R28): the same names and values, so the same clone. |
| `BenchmarkNoop` | moved as is | |
| `BenchmarkEncodeBody` (B1) | kept in root | Its `sdk` arm times the unexported `encodeBody`, and no exported path encodes a body alone. The naive arms stay beside it so that one run compares the three encoders on the same states. `TestEncodeBodyMatchesNaive` stays with it. |
| `BenchmarkAssembly` (B3) | kept in root | It rebuilds `Client.attempt`'s unexported steps; `TestAssemblyMatchesCall` stays with it. |
| `BenchmarkBackoff` (B4) | kept in root | It times the unexported `backoff`. |
| `BenchmarkLoopback/cold-fanout-64` (B6) | kept in root | `leaders/op` and `firstholds/op` come from the client's unexported transport, and `Client.Stats` carries only `Dials` and `Attempts`. |
| `BenchmarkFalsyJSON` | kept in root | It times the unexported `falsyJSON`. |
| `BenchmarkPrepare` | kept in root | Its case table (`prepare_cases_test.go`) is shared with `TestAllocPrepare`, which pins the same cases' allocations and reads unexported fields of the result. A second table in `internal/benchmark` could drift from it unseen. |

Each kept benchmark could move only through an exported hook that exists
for the benchmark alone, or, for `BenchmarkPrepare`, through a duplicated
case table. The owner rules on each. `go test -bench . ./...` still gives
all 125 rows (W5.1-17, -18), and CodSpeed's runner finds all three packages.
The owner kept all six in root as built (G5b).

Review W5.1 G1 at de17087 found that `NewFixtureServer` wrote each response
from a string through `io.WriteString`, which the loopback server's writer
turns into a fresh `[]byte`, one allocation per response more than B6's old
handler. b002bd5 writes bytes again. In one session under the (M) lock at
12:39–12:40 JST, with minima of three runs, B6's allocations at b002bd5
match ddec26a's: `Loopback/call` 111 and 111, and `cold-fanout-64` 8597
and 8595, whose runs spread by 43 and 6. So W5.1-09/-10's allocation rows
still hold (`results/b6allocs-M-{b002bd5,ddec26a}.txt`).

## W3.3: telemetry and redaction (AC-P6 unchanged; what logging costs)

W3.3 adds nothing to a successful call's path: the h2gate error-text hook
(ruling R84) runs only for a DEBUG record a logger keeps, after a dial
failed; the redacted copy of a response header (R87) is made only when an
`*APIError`, `*ResponseValidationError` or `*ResponseTooLargeError` is
built; the scrubbed chain rendering of a stand-in (R95) only when a
transport error printed a credential. W3.3-01 and -02 were measured at
c196649, the last commit that changed code or tests before review
(`TestAllocLoggedCall`, new), on 87d1ac7 (W3.2's landing); as rebased onto
ca226bb (W5.1's landing), c196649 is ba793fc, the same W3.3 changes over
main's own, and the raw headers keep the measured SHA. W3.3-03 and -04
measure fc5164f, the last commit of the review's fix pass that changes
code (rulings R103 and R103b: the key needle over an `*APIError`'s message
and over a `*ResponseValidationError`'s two server-chosen path names, error
path only), on ca226bb. W3.3-05 and -06 measure 92a9fc6, the revert of R103
and R103b (ruling R103-rev), on 3ffe77b. The commits that write this section
change documents and raw outputs only, except the one that adds the previous
sentence: it also has the INFO "response" record read the request id through
the redactor (ruling R107), on the logging path after the level check.
W3.3-07 measures 72cd225, which reads that id without lower-casing the
header's name (review R103REVERT MINOR 3), against the same tree with the
redactor of 7ab3af5, on aaa9698. Commands use `R=_spikes/s-c1/run.sh`,
`O=_spikes/w3.3/results`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/40cb0f1f-c8a9-422c-a3e8-b3afc329b5cb/scratchpad`
and the row's `BASE`.

### How the numbers were taken

- (M): `go1.27.1 darwin/arm64`, `GOEXPERIMENT=nosimd,noruntimesecret`,
  in the lane's worktree at the row's `BASE` (clean), under
  `/opt/homebrew/opt/util-linux/bin/flock` on `$SP/bench.lock`.
- (L): the tree written by `git archive $BASE` and piped over ssh to
  `/tmp/ts-spike/src-w3.3/meas`; toolchain `/tmp/ts-spike/go/bin/go` with
  the §11 `GOPATH`, `GOMODCACHE` and `GOCACHE` under `/tmp/ts-spike` and no
  `GOEXPERIMENT`, under `flock /tmp/ts-spike/bench.lock`; the raw file was
  copied back.
- `TestAllocWholeCall` and `TestMemStatsCap` as in W3.2 (three of five
  runs agree, `testsupport.Spread`); `TestAllocLoggedCall` measures
  `TestAllocWholeCall`'s q3 call under `DefaultRetry()` with the same
  rule, once per logger. Counts only: the (M) load (6.7) does not bear on
  them.

### W3.3 findings

1. **AC-P6 is unchanged on both hosts under `DefaultRetry()` and the
   default logger: SDK-own 14 allocations, 2 008 B** (floor 8/640, call
   22/2648), as W3.2-05/-06.
2. **AC-P5 is unchanged on both hosts:** (i) 38 allocations / 264 032 B
   (bound 327 680 B) … (vii) 6 104 B, equal to W3.2-05/-06 in every count.
   The header copy of R87 costs nothing in (ii) and (iii), whose
   `*ResponseTooLargeError` is built from a Recorder reply without
   headers; a response with headers pays one map and one slice per
   credential header, on the error path.
3. **What logging costs one call (row W3.3-01's `LOG` line):** the default
   logger (`slog.DiscardHandler`) builds no record and costs nothing,
   22/2648, pinned equal to AC-P6's call; `WithLogger` at INFO into a
   handler that keeps and discards the records costs **+1 allocation,
   +48 B** (the one INFO "response" record per attempt carries six
   attributes, one past the five a `slog.Record` holds inline, so its
   attribute slice is allocated); at DEBUG **+3, +96 B** (the two
   redacted-header values of the DEBUG records); slog's text handler
   writing to `io.Discard` adds nothing at INFO. A record with five
   attributes would make INFO logging free of allocations: a W5.3
   candidate, not a W3.3 change (the record's attributes are the section
   9 contract).
4. **The review's fix pass and the rebase onto ca226bb leave every count
   as it was:** W3.3-03 and -04 at fc5164f equal W3.3-01 in every count on
   both hosts. R103 first moved AC-P5 (i) to 40/264064: its refactor had
   the needle forms built into a slice of their own per credential value;
   building them in place, as before, restored 38/264032.
5. **Reverting R103 and R103b (owner decision G7 (8), ruling R103-rev)
   leaves every count as it was:** W3.3-05 and -06 at 92a9fc6 equal
   W3.3-01 and -02 in every count on both hosts. The plain revert puts the
   needle forms back into `requestCredentials`' closure, the shape W3.3-01
   measured; no helper of R103's was kept.
6. **Reading the request id without lower-casing the header's name takes
   one allocation off each record (W3.3-07):** the `LOG q3+id` line, whose
   reply carries `X-Typesafe-Request-Id`, is INFO 24/2728 and DEBUG 26/2776
   at 72cd225, against 25/2752 and 27/2800 with 7ab3af5's redactor, whose
   name check lower-cased the header's name on every record. The `LOG q3`
   line, whose reply has no id, could not show it; it is unchanged, and so
   are AC-P6 (SDK-own 14/2008) and AC-P5 (i) (38/264032).

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W3.3-01 | 2026-09-26 12:05:13 JST | W3.3 AC-P6 under `DefaultRetry()`, AC-P5 memstats, logging cost | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.65 → 6.65 | `BASE=c196649 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap\|TestAllocLoggedCall)$' -v .` | q3: floor 8/640, call 22/2648, SDK-own 14/2008; AC-P5 (i) 38 allocs / 264032 B, (ii) 1368 B, (iii) 33559896 B, (iv) 33302488 B, (v) 33560536 B, (vi) 2392 B, (vii) 6104 B; LOG default 22/2648, INFO +1/48, DEBUG +3/96, INFO text handler +1/48 | mallocs/bytes, collector off, `GOMAXPROCS(1)`, 3 of 5 runs agree; `results/alloc-M.txt` |
| W3.3-02 | 2026-09-26 03:05:15 UTC | W3.3 AC-P6 under `DefaultRetry()`, AC-P5 memstats, logging cost | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.02 → 0.02 | `BASE=c196649 sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap\|TestAllocLoggedCall)$' -v .` | identical to W3.3-01 in every count (the minimum of 3 agreeing runs); AC-P5 (i)'s largest run 43/282752 against (M)'s 42/264320, inside the bound | `results/alloc-L.txt` |
| W3.3-03 | 2026-09-26 12:56:34 JST | W3.3 review fix pass on ca226bb: AC-P6 under `DefaultRetry()`, AC-P5 memstats, logging cost | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.44 → 8.44 | `BASE=fc5164f GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M-fix -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap\|TestAllocLoggedCall)$' -v .` | identical to W3.3-01 in every count: q3 SDK-own 14/2008; AC-P5 (i) 38 allocs / 264032 B … (vii) 6104 B; LOG default 22/2648, INFO +1/48, DEBUG +3/96 | mallocs/bytes, collector off, `GOMAXPROCS(1)`, 3 of 5 runs agree; `results/alloc-M-fix.txt` |
| W3.3-04 | 2026-09-26 03:56:36 UTC | W3.3 review fix pass on ca226bb: AC-P6 under `DefaultRetry()`, AC-P5 memstats, logging cost | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 2.45 → 2.45 | `BASE=fc5164f sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L-fix -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap\|TestAllocLoggedCall)$' -v .` | identical to W3.3-02 in every count | `results/alloc-L-fix.txt` |
| W3.3-05 | 2026-09-26 14:25:39 JST | W3.3 R103/R103b reverted per G7 (8) (ruling R103-rev) on 3ffe77b: AC-P6 under `DefaultRetry()`, AC-P5 memstats, logging cost | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.10 → 6.10 | `BASE=92a9fc6 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M-r103revert -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap\|TestAllocLoggedCall)$' -v .` | identical to W3.3-01 and -03 in every count: q3 SDK-own 14/2008; AC-P5 (i) 38 allocs / 264032 B … (vii) 6104 B; LOG default 22/2648, INFO +1/48, DEBUG +3/96 | mallocs/bytes, collector off, `GOMAXPROCS(1)`, 3 of 5 runs agree; `results/alloc-M-r103revert.txt` |
| W3.3-06 | 2026-09-26 05:26:01 UTC | W3.3 R103/R103b reverted per G7 (8) (ruling R103-rev) on 3ffe77b: AC-P6 under `DefaultRetry()`, AC-P5 memstats, logging cost | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.03 → 0.03 | `BASE=92a9fc6 sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L-r103revert -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap\|TestAllocLoggedCall)$' -v .` | identical to W3.3-02 and -04 in every count: q3 SDK-own 14/2008; AC-P5 (i) 38 allocs / 264032 B (largest run 43/282752) … (vii) 6104 B | the tree piped by `tar --exclude=.git` from the lane's worktree at `BASE` (clean) to `/tmp/ts-spike/src-p3-r103revert`; `results/alloc-L-r103revert.txt` |
| W3.3-07 | 2026-09-26 15:04:17 JST | W3.3 R107 request id read without lower-casing its name (review R103REVERT MINOR 3) on aaa9698: logging cost with an id-bearing reply, AC-P6, AC-P5 memstats | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.50 → 8.50 (after); 7.35 → 7.35 (before) | `BASE=72cd225 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M-logid -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap\|TestAllocLoggedCall\|TestAllocRequestID)$' -v .`; before, 15:04:35 JST, in `git archive 72cd225` with `git show 7ab3af5:redact.go` over its `redact.go`: `BASE="72cd225 with 7ab3af5's redact.go" … alloc-M-logid-before -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap\|TestAllocLoggedCall)$' -v .` | LOG q3+id: default 22/2664; INFO 25/2752 → 24/2728 (+3/88 → +2/64 over the default); DEBUG 27/2800 → 26/2776 (+5/136 → +4/112); INFO text handler 25/2752 → 24/2728. LOG q3, q3 SDK-own 14/2008 and AC-P5 (i) 38 allocs / 264032 B unchanged in both; `TestAllocRequestID` PASS after (0 allocations for one id, with or without the key; 1 for several) | recorded, not gated; mallocs/bytes, collector off, `GOMAXPROCS(1)`, 3 of 5 runs agree; the reply's id is `req_7f3c9a2e5b1d`; `results/alloc-M-logid.txt`, `results/alloc-M-logid-before.txt` |

## W3.4: the AC-P6 re-freeze at the end of Phase 3

W3.4 measures the client as Phase 3 leaves it, with W3.1 (1ecc6f5), W3.2's
retry loop (87d1ac7), W5.1's benchmarks and comparator (ca226bb) and W3.3's
telemetry and redaction (1f694b0) on main, and freezes AC-P6 from it: the
allocation clause, N, and the time clause of owner decision G3 (a)
([`frozen-budgets.md`](frozen-budgets.md)). Every row measures 1f694b0,
main's head, as it is. W3.4-05 and -06 run a probe,
`_spikes/w3.4/probe_test.go.txt`, copied as `zz_w34_probe_test.go` into a
copy of that tree for the run only, as W2.5's K32 probe was; it pins
nothing and is not one of the repository's tests. The commits that write
this section change documents, raw outputs, and the comments and failure
messages of `TestAllocWholeCall`'s pins, whose values stay 14 and 8/640.
Raw outputs are in `_spikes/w3.4/results/`, and `_spikes/w3.4/render.py`
prints the tables below from them; it exits non-zero when a series'
minimum differs between the hosts. Commands use `R=_spikes/s-c1/run.sh`,
`O=_spikes/w3.4/results`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/40cb0f1f-c8a9-422c-a3e8-b3afc329b5cb/scratchpad`
and `BASE=1f694b0`.

### How the numbers were taken

- (M): `go1.27.1 darwin/arm64`, `GOEXPERIMENT=nosimd,noruntimesecret`, in
  the lane's worktree at 1f694b0 (clean but for the untracked
  `_spikes/w3.4/`), under `/opt/homebrew/opt/util-linux/bin/flock` on
  `$SP/bench.lock`. Other lanes were working on (M): the allocation rows
  ran at a load of 14 to 31, which counts do not depend on (R17), and the
  first `BenchmarkCall` row at 13.10 → 17.44, so W3.4-08 repeated it with
  the load gate at 8 (it waited the runner's five minutes and started at
  8.53).
- (L): the worktree without `.git`, copied with the section 11
  `tar | ssh` pipe to `/tmp/ts-spike/src-w3.4/meas`, and again to
  `/tmp/ts-spike/src-w3.4/probe` with the probe added; toolchain
  `/tmp/ts-spike/go/bin/go` with the section 11 `GOPATH`, `GOMODCACHE` and
  `GOCACHE` under `/tmp/ts-spike` and no `GOEXPERIMENT`, under
  `flock /tmp/ts-spike/bench.lock`; the raw files were copied back.
- AC-P6's allocation clause: `TestAllocWholeCall` as in W3.2 and W3.3 (the
  q3 call under `DefaultRetry()`, the default logger, three of five runs
  equal to the minimum, `testsupport.StableMin`). W3.4-01 and -02 are the
  rows of record, W3.3-01's command at 1f694b0. W3.4-03 and -04 run the
  same command with `-count=20`, 100 runs of every series per host; each
  series' "runs of" line lists every run, and `render.py` takes the
  minimum, the maximum and the spread of each counter over all of them, as
  `testsupport.Spread` does over one invocation's runs (critic-p2 m-1: the
  exact pins keep the three-of-five rule; the rows state the maximum and
  the spread).
- Logging: W3.3's `TestAllocLoggedCall`, whose `info-discard` logger is the
  charter's "`WithLogger` at INFO into a discard-like recorder".
- q20 and call options: `TestW34Probe` measures, with the same method, the
  whole call asking the twenty questions `result-20.json` answers (its
  floor, SDK-own and ITEM split), and the q3 call with one call option of
  each kind and with all five (ruling R97-corr). "hoisted" passes options
  built before the measured section; "inline" builds them inside it, as
  `c.SystemOne(ctx, s, qs, Header("X-A", "b"))` does. `Header("X-A", "b")`,
  `Model("m")` and `Retry(NoRetry())` are review W3.2 MINOR 4's shapes;
  `Timeout(DefaultTimeout)` and `ExtraBody("x", RawJSON("1"))` are added.
- Time: `internal/benchmark`'s `BenchmarkCall` (G5), all eight rows,
  `-count=10`, as W5.1-01 and -03. The tables give the minimum and the
  median of the ten samples and the minimum allocs/op.

### W3.4 findings

1. **AC-P6's allocation clause is frozen at N = 14: SDK-own 14
   allocations, 2 008 B, on both hosts** (floor 8/640: the Recorder's round
   trip 7/624 and E_sonic 1/16; call 22/2 648). That is W2.3's count with
   one attempt, W3.2's under `DefaultRetry()` and W3.3's with telemetry:
   Phase 3's retry loop, logging and redaction add nothing to a call whose
   first attempt succeeds. The composition, from the ITEM line, identical
   on both hosts:

   | Allocation | Count / B | Where |
   | --- | ---: | --- |
   | `context.WithTimeout` | 4 / 272 | the attempt's deadline: the timerCtx, its `AfterFunc` closure and `*time.Timer`, the CancelFunc closure (W2.3 finding 2) |
   | decode | 4 / 688 | the visitor's three fold slices and `wire.Answers.Grow` (AC-P2's `result.json` pin is 4) |
   | `readBody` | 1 / 384 | the response buffer, `result.json` declared |
   | `*http.Request` | 1 / 320 | `Request.WithContext` |
   | URL copy | 1 / 144 | the attempt's copy of the endpoint URL (R66 NIT 5) |
   | `*SystemOneResponse` | 1 / 112 | the result |
   | `codec.Body.Open` | 1 / 64 | the attempt's body reader |
   | `GetBody` | 1 / 24 | the `body.GetBody` method value |
   | header map | 0 / 0 | the first attempt sends the client's template itself (R77) |
   | **SDK-own** | **14 / 2 008** | |

   Against R28's provisional composition of 15, the header map went from 2
   to 0 (R77) and the URL copy added 1. The Rust port's 12 is not reached;
   W5.3's candidates are R79 NIT 3's four (the 4 of `context.WithTimeout`,
   the `GetBody` method value, `new(SystemOneResponse)`, the URL copy) and
   W2.3 finding 2's (one slab for the fold slices, −2; no attempt deadline
   under an earlier caller deadline; answer entries presized in the
   response's allocation).
2. **The minimum is stable; the maximum is one allocation more, once.**
   Over 100 runs per host (W3.4-03, -04), every series of
   `TestAllocWholeCall` spreads +0/+0 on (M); on (L) the call spreads
   +1/+48 in one run of 100 (23/2 696, so SDK-own 15 in that run) and every
   other series +0/+0. That is K32's residual (K32-res): the runtime builds
   a type assertion's cache on about one miss in 1024, at random, and a
   successful call makes such lookups too. It is why the pin takes the
   minimum that three of five runs share and does not bound every run: at
   the observed rate of 1 run in 200 across the two hosts, three of five
   runs are hit in about 10 × 0.005³ ≈ 1.3 × 10⁻⁶ of invocations. All 20
   invocations passed on each host. The same happened elsewhere in the
   files: `TestAllocLoggedCall`'s `info-discard` once on (L) and
   `info-text` once on (M), +1/+48 each; `TestMemStatsCap` (i) 79 of 100
   runs at 38/264 032 on both hosts with a maximum of 42/264 320 (M) and
   43/282 752 (L), (iv) +1/+48 once on each host and (v) once on (M), every
   run inside its AC-P5 bound.
3. **What N does not count, recorded (W3.4-05, -06, identical on both
   hosts):**
   - **q20**: SDK-own 34 allocations, 9 248 B (call 42/9 888, floor
     8/640). The split is q3's except the decode, 24/6 008 (AC-P2's
     `result-20.json` pin is 24), and `readBody`, 1/2 304. R28 recorded
     35 for the S-C1 prototype; W5.1 read 42 − 8 from allocs/op.
   - **Call options** (R97-corr): a call that passes any option moves
     `callOptions` (the 160 B class) to the heap once, whatever the number
     of options. Against the call without options, hoisted:
     `Retry(NoRetry())` +1/+160 (that move alone), `Timeout` +2/+176,
     `Model` +3/+192, `ExtraBody` +2/+192, `Header` +7/+720 (which also
     builds the call's own header), all five +11/+784. Inline, the same except
     `ExtraBody` +4/+216 and all five +13/+808: the caller's
     `RawJSON("1")` conversion and its boxing in an `any`, made inside the
     section. `Header`, `Model` and `Retry` equal review W3.2 MINOR 4's
     29/3 368, 25/2 840 and 23/2 808. W5.3 evaluates R97-corr's option (c).
   - **Logging** (W3.3's test, unchanged since W3.3-01): the default
     logger 22/2 648, the call AC-P6 counts; `WithLogger` at INFO into a
     handler that keeps and discards the records +1/+48 (ruling R102: an
     INFO record of five attributes would make it free, a W5.3
     candidate); at DEBUG +3/+96; INFO into slog's text handler on
     `io.Discard` +1/+48.
   - A real transport adds 1 per attempt, the context's Done channel
     (R28); these rows use the Recorder.
4. **AC-P6's time clause is frozen per G3 (a) and holds on amd64, the
   gate: q3 `call/sdk` 6.205 µs against `call/naive` 6.969 µs on (L), 0.890
   by medians of 10 and 0.898 by minima** (W3.4-09; W5.1-03: 0.887). Since
   W5.1's rows, measured at 6afc8a3 before any Phase 3 wave landed,
   `call/sdk` is 1.6 % slower on (L), and `call/naive` 1.2 % and
   `call/floor` 0.6 % slower in the same session, so the margin went from
   11.3 % to 11.0 %. q20, recorded (ruling R101 (b)): 25.89 against
   26.42 µs, 0.980, a margin of 2.0 % (W5.1: 1.7 %), still a W5.3 input.
   On arm64 the clause is recorded, not gated (K18), and fails as it did
   at W5.1 (K23, the decode): q3 1.274 by medians (5.126 against
   4.022 µs; W5.1: 1.333) and q20 1.737 (W5.1: 1.880), from W3.4-08. The
   earlier (M) run, W3.4-07, gave 1.304 and 2.008 under a load of 13 to 17,
   so on (M) these ratios move by several per cent from run to run; the
   ± 53 % of W3.4-08's `call/sdk` is two slow samples of ten (7.9 and
   8.5 µs), and its minimum is 5.041 µs. Against the encoding/json client,
   reported only: q3 0.367 (L) and 0.567 (M), q20 0.335 and 0.622.
5. **Gates.** `go test -race -count=1 ./...` on (L), once (R62), passed in
   all seven packages (W3.4-10). On (M), each commit's tree passed the
   section 11 lint chain and `go test -race -count=1 ./...` before it was
   committed, and the freeze commit's tree also passed ci.yaml's root
   allocation step (W3.4-11). govulncheck ran as
   `go run golang.org/x/vuln/cmd/govulncheck@latest` (W5.1's precedent):
   the installed binary was built with go1.26 and cannot load go1.27
   sources.

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W3.4-01 | 2026-09-26 13:13:24 JST | W3.4 AC-P6 re-freeze: whole call under `DefaultRetry()`, AC-P5 memstats, logging cost | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 14.46 → 23.47, noisy | `BASE=1f694b0 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap\|TestAllocLoggedCall)$' -v .` | q3: floor 8/640, call 22/2648, SDK-own **14/2008 (N = 14, frozen)**; ITEM header 0/0, URL 1/144, WithTimeout 4/272, Request 1/320, body.Open 1/64, GetBody 1/24, `*SystemOneResponse` 1/112, readBody 1/384, decode 4/688; AC-P5 (i) 38 allocs / 264032 B, (ii) 1368 B, (iii) 33559896 B, (iv) 33302488 B, (v) 33560536 B, (vi) 2392 B, (vii) 6104 B; LOG default 22/2648, INFO +1/48, DEBUG +3/96, INFO text handler +1/48 | mallocs/bytes, collector off, `GOMAXPROCS(1)`, 3 of 5 runs agree; counts do not depend on load (R17); `results/alloc-M.txt` |
| W3.4-02 | 2026-09-26 04:14:18 UTC | W3.4 AC-P6 re-freeze: whole call under `DefaultRetry()`, AC-P5 memstats, logging cost | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.08 → 0.08 | `BASE=1f694b0 sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L -count=1 -run '^(TestAllocWholeCall\|TestMemStatsCap\|TestAllocLoggedCall)$' -v .` | identical to W3.4-01 in every count | `results/alloc-L.txt` |
| W3.4-03 | 2026-09-26 13:13:26 JST | W3.4 the same, 20 invocations: minimum, maximum and spread of every series | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 23.47 → 28.24, noisy | the W3.4-01 command with `alloc-M-x20 -count=20` | PASS 20/20; 100 runs per series; `TestAllocWholeCall` every series +0/+0 (call 22/2648 in 100 of 100); AC-P5 (i) 38/264032 in 79, max 42/264320, (iv) and (v) +1/+48 once each; LOG `info-text` +1/+48 once | [W3.4 tables](#w34-tables); `results/alloc-M-x20.txt` |
| W3.4-04 | 2026-09-26 04:14:20 UTC | W3.4 the same, 20 invocations | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.08 → 0.88 | the W3.4-02 command with `alloc-L-x20 -count=20` | PASS 20/20; the call 22/2648 in 99 of 100, one run 23/2696 (+1/+48, K32-res), every other `TestAllocWholeCall` series +0/+0; AC-P5 (i) 38/264032 in 79, max 43/282752, (iv) +1/+48 once; LOG `info-discard` +1/+48 once | [W3.4 tables](#w34-tables); `results/alloc-L-x20.txt` |
| W3.4-05 | 2026-09-26 13:13:41 JST | W3.4 probe: the q20 call; the q3 call with each call option | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 30.55 → 30.55, noisy | in `git archive 1f694b0` with `_spikes/w3.4/probe_test.go.txt` as `zz_w34_probe_test.go`: `BASE='1f694b0 + …' GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock probe-M -count=10 -run '^TestW34Probe$' -v .` | q20: floor 8/640, call 42/9888, SDK-own 34/9248 (decode 24/6008, readBody 1/2304, the rest as q3); options, hoisted: Header +7/720, Model +3/192, Timeout +2/176, ExtraBody +2/192, Retry +1/160, all five +11/784; inline: ExtraBody +4/216, all five +13/808, the others as hoisted | 10 invocations, 3 of 5 runs agree in each; `results/probe-M.txt` |
| W3.4-06 | 2026-09-26 04:14:26 UTC | W3.4 probe | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.88 → 0.88 | in `/tmp/ts-spike/src-w3.4/probe/wt-w3.4`: `BASE='1f694b0 + …' sh $R '(L)' /tmp/ts-spike/src-w3.4/meas/wt-w3.4/$O /tmp/ts-spike/bench.lock probe-L -count=10 -run '^TestW34Probe$' -v .` | identical to W3.4-05 in every minimum | `results/probe-L.txt` |
| W3.4-07 | 2026-09-26 13:14:54 JST | W3.4 B5 `call/sdk` against `call/naive` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 13.10 → 17.44, noisy | `BASE=1f694b0 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $O $SP/bench.lock call-M -run '^$' -bench '^BenchmarkCall$' -benchmem -count=10 ./internal/benchmark/` | q3: sdk 5.246 µs ± 13 %, naive 4.022 µs (1.304); q20: sdk 27.29 µs, naive 13.59 µs (2.008); allocs sdk 22 / floor 8 / naive 54 / naive-json 114 (q20: 42 / 8 / 127 / 517) | waited 1 × 60 s; superseded as the (M) row of record by W3.4-08; `results/call-M.txt`, `results/benchstat-call-M.txt` |
| W3.4-08 | 2026-09-26 13:22:55 JST | W3.4 B5 `call/sdk` against `call/naive` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.53 → 8.15 | the W3.4-07 command with `MAXLOAD=8` and `call-M-2` | q3: sdk 5.126 µs ± 53 % (min 5.041), naive 4.022 µs (sdk/naive **1.274**), naive-json 9.046 µs, floor 519.3 ns; q20: sdk 24.87 µs, naive 14.32 µs (1.737); allocs as W3.4-07 | arm64 recorded, not gated (G3, K18, K23); waited 5 × 60 s and ran at 8.53; `results/call-M-2.txt`, `results/benchstat-call-M-2.txt` |
| W3.4-09 | 2026-09-26 04:14:34 UTC | W3.4 B5 `call/sdk` against `call/naive` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.81 → 1.43 | `BASE=1f694b0 MAXLOAD=44 sh $R '(L)' $O /tmp/ts-spike/bench.lock call-L -run '^$' -bench '^BenchmarkCall$' -benchmem -count=10 ./internal/benchmark/` | q3: sdk 6.205 µs ± 0 %, naive 6.969 µs ± 3 % (sdk/naive **0.890**, 0.898 by minima: AC-P6's time clause holds), naive-json 16.90 µs, floor 765.2 ns; q20: sdk 25.89 µs, naive 26.42 µs (0.980, margin 2.0 %); allocs sdk 22 / floor 8 / naive 68 / naive-json 114 (q20: 42 / 8 / 247 / 517) | amd64 = the gate (G3); `results/call-L.txt`, `results/benchstat-call-L.txt` |
| W3.4-10 | 2026-09-26 04:16:17 UTC | W3.4 R62 race run | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.39 → 1.37 | `BASE=1f694b0 sh $R '(L)' $O /tmp/ts-spike/bench.lock race-L -race -count=1 ./...` | ok in all 7 packages | not a timing row; `results/race-L.txt` |
| W3.4-11 | 2026-09-26 13:29:09 JST; 13:30:08 JST | W3.4 gates on each commit's tree | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.38 → 7.08; 6.33 → 6.49 | under `flock $SP/bench.lock`, with `GOEXPERIMENT=nosimd,noruntimesecret` and `GOPACKAGESDEBUG` unset: `go build ./... && go vet ./...`, the section 11 lint chain (`gofumpt -extra -l`, `modernize -test`, `golangci-lint run --allow-serial-runners`, `go vet`, `staticcheck`, `go run golang.org/x/vuln/cmd/govulncheck@latest`, `go mod tidy -diff`), `go test -race -count=1 ./...`; on the second tree also ci.yaml's root allocation step with its `-list` guard | both trees: `0 issues.`, `No vulnerabilities found.`, `-race` ok in all 7 packages; the second: guard 7 names, the step ok, `CALL q3 … own=14/2008 (N = 14, frozen at W3.4)` | not a measurement; the first tree is 2769f1e's (`git write-tree` 9406d22, exported with `git archive`; its Go files are 1f694b0's byte for byte), the second the freeze commit's Go files (index tree 2be3b91); `results/gates-M-2769f1e.txt`, `results/gates-M-freeze.txt` |

<a id="w34-tables"></a>

### W3.4 tables

Printed by `_spikes/w3.4/render.py` from the raw files.

Allocation series, alloc-{M,L}-x20.txt (20 invocations × 5 runs):

| Test | Series | Runs (M) / (L) | (M) min / max / spread | (L) min / max / spread | At the minimum (M) / (L) |
| --- | --- | ---: | --- | --- | ---: |
| `TestAllocWholeCall` | E_sonic | 100 / 100 | 1/16 / 1/16 / +0/+0 | 1/16 / 1/16 / +0/+0 | 100 / 100 |
| `TestAllocWholeCall` | floor round trip | 100 / 100 | 7/624 / 7/624 / +0/+0 | 7/624 / 7/624 / +0/+0 | 100 / 100 |
| `TestAllocWholeCall` | call/sdk | 100 / 100 | 22/2648 / 22/2648 / +0/+0 | 22/2648 / 23/2696 / +1/+48 | 100 / 99 |
| `TestAllocWholeCall` | item header map | 100 / 100 | 0/0 / 0/0 / +0/+0 | 0/0 / 0/0 / +0/+0 | 100 / 100 |
| `TestAllocWholeCall` | item URL copy | 100 / 100 | 1/144 / 1/144 / +0/+0 | 1/144 / 1/144 / +0/+0 | 100 / 100 |
| `TestAllocWholeCall` | item context.WithTimeout | 100 / 100 | 4/272 / 4/272 / +0/+0 | 4/272 / 4/272 / +0/+0 | 100 / 100 |
| `TestAllocWholeCall` | item Request (WithContext) | 100 / 100 | 1/320 / 1/320 / +0/+0 | 1/320 / 1/320 / +0/+0 | 100 / 100 |
| `TestAllocWholeCall` | item body.Open | 100 / 100 | 1/64 / 1/64 / +0/+0 | 1/64 / 1/64 / +0/+0 | 100 / 100 |
| `TestAllocWholeCall` | item GetBody method value | 100 / 100 | 1/24 / 1/24 / +0/+0 | 1/24 / 1/24 / +0/+0 | 100 / 100 |
| `TestAllocWholeCall` | item *SystemOneResponse | 100 / 100 | 1/112 / 1/112 / +0/+0 | 1/112 / 1/112 / +0/+0 | 100 / 100 |
| `TestAllocWholeCall` | item readBody | 100 / 100 | 1/384 / 1/384 / +0/+0 | 1/384 / 1/384 / +0/+0 | 100 / 100 |
| `TestAllocWholeCall` | item decode | 100 / 100 | 4/688 / 4/688 / +0/+0 | 4/688 / 4/688 / +0/+0 | 100 / 100 |
| `TestMemStatsCap` | i-declared-16MiB-sent-10B | 100 / 100 | 38/264032 / 42/264320 / +4/+288 | 38/264032 / 43/282752 / +5/+18720 | 79 / 79 |
| `TestMemStatsCap` | ii-declared-16MiB+1 | 100 / 100 | 16/1368 / 16/1368 / +0/+0 | 16/1368 / 16/1368 / +0/+0 | 100 / 100 |
| `TestMemStatsCap` | iii-undeclared-16MiB+1 | 100 / 100 | 29/33559896 / 29/33559896 / +0/+0 | 29/33559896 / 29/33559896 / +0/+0 | 100 / 100 |
| `TestMemStatsCap` | iv-declared-16MiB | 100 / 100 | 26/33302488 / 27/33302536 / +1/+48 | 26/33302488 / 27/33302536 / +1/+48 | 99 / 99 |
| `TestMemStatsCap` | v-undeclared-16MiB | 100 / 100 | 32/33560536 / 33/33560584 / +1/+48 | 32/33560536 / 32/33560536 / +0/+0 | 99 / 100 |
| `TestMemStatsCap` | vi-declared-result.json | 100 / 100 | 20/2392 / 20/2392 / +0/+0 | 20/2392 / 20/2392 / +0/+0 | 100 / 100 |
| `TestMemStatsCap` | vii-undeclared-result.json | 100 / 100 | 20/6104 / 20/6104 / +0/+0 | 20/6104 / 20/6104 / +0/+0 | 100 / 100 |
| `TestAllocLoggedCall` | call/default | 100 / 100 | 22/2648 / 22/2648 / +0/+0 | 22/2648 / 22/2648 / +0/+0 | 100 / 100 |
| `TestAllocLoggedCall` | call/info-discard | 100 / 100 | 23/2696 / 23/2696 / +0/+0 | 23/2696 / 24/2744 / +1/+48 | 100 / 99 |
| `TestAllocLoggedCall` | call/debug-discard | 100 / 100 | 25/2744 / 25/2744 / +0/+0 | 25/2744 / 25/2744 / +0/+0 | 100 / 100 |
| `TestAllocLoggedCall` | call/info-text | 100 / 100 | 23/2696 / 24/2744 / +1/+48 | 23/2696 / 23/2696 / +0/+0 | 99 / 100 |

Probe series, probe-{M,L}.txt (10 invocations × 5 runs):

| Test | Series | Runs (M) / (L) | (M) min / max / spread | (L) min / max / spread | At the minimum (M) / (L) |
| --- | --- | ---: | --- | --- | ---: |
| `TestW34Probe` | q20 E_sonic | 50 / 50 | 1/16 / 1/16 / +0/+0 | 1/16 / 1/16 / +0/+0 | 50 / 50 |
| `TestW34Probe` | q20 floor round trip | 50 / 50 | 7/624 / 7/624 / +0/+0 | 7/624 / 8/672 / +1/+48 | 50 / 49 |
| `TestW34Probe` | q20 call/sdk | 50 / 50 | 42/9888 / 42/9888 / +0/+0 | 42/9888 / 42/9888 / +0/+0 | 50 / 50 |
| `TestW34Probe` | item header map | 50 / 50 | 0/0 / 0/0 / +0/+0 | 0/0 / 0/0 / +0/+0 | 50 / 50 |
| `TestW34Probe` | item URL copy | 50 / 50 | 1/144 / 1/144 / +0/+0 | 1/144 / 1/144 / +0/+0 | 50 / 50 |
| `TestW34Probe` | item context.WithTimeout | 50 / 50 | 4/272 / 4/272 / +0/+0 | 4/272 / 4/272 / +0/+0 | 50 / 50 |
| `TestW34Probe` | item Request (WithContext) | 50 / 50 | 1/320 / 1/320 / +0/+0 | 1/320 / 1/320 / +0/+0 | 50 / 50 |
| `TestW34Probe` | item body.Open | 50 / 50 | 1/64 / 1/64 / +0/+0 | 1/64 / 1/64 / +0/+0 | 50 / 50 |
| `TestW34Probe` | item GetBody method value | 50 / 50 | 1/24 / 1/24 / +0/+0 | 1/24 / 1/24 / +0/+0 | 50 / 50 |
| `TestW34Probe` | item *SystemOneResponse | 50 / 50 | 1/112 / 1/112 / +0/+0 | 1/112 / 1/112 / +0/+0 | 50 / 50 |
| `TestW34Probe` | item readBody | 50 / 50 | 1/2304 / 1/2304 / +0/+0 | 1/2304 / 1/2304 / +0/+0 | 50 / 50 |
| `TestW34Probe` | item decode | 50 / 50 | 24/6008 / 24/6008 / +0/+0 | 24/6008 / 24/6008 / +0/+0 | 50 / 50 |
| `TestW34Probe` | opt none inline | 50 / 50 | 22/2648 / 22/2648 / +0/+0 | 22/2648 / 22/2648 / +0/+0 | 50 / 50 |
| `TestW34Probe` | opt Header inline | 50 / 50 | 29/3368 / 29/3368 / +0/+0 | 29/3368 / 29/3368 / +0/+0 | 50 / 50 |
| `TestW34Probe` | opt Header hoisted | 50 / 50 | 29/3368 / 29/3368 / +0/+0 | 29/3368 / 29/3368 / +0/+0 | 50 / 50 |
| `TestW34Probe` | opt Model inline | 50 / 50 | 25/2840 / 26/2888 / +1/+48 | 25/2840 / 25/2840 / +0/+0 | 49 / 50 |
| `TestW34Probe` | opt Model hoisted | 50 / 50 | 25/2840 / 25/2840 / +0/+0 | 25/2840 / 25/2840 / +0/+0 | 50 / 50 |
| `TestW34Probe` | opt Timeout inline | 50 / 50 | 24/2824 / 24/2824 / +0/+0 | 24/2824 / 24/2824 / +0/+0 | 50 / 50 |
| `TestW34Probe` | opt Timeout hoisted | 50 / 50 | 24/2824 / 24/2824 / +0/+0 | 24/2824 / 24/2824 / +0/+0 | 50 / 50 |
| `TestW34Probe` | opt ExtraBody inline | 50 / 50 | 26/2864 / 27/2912 / +1/+48 | 26/2864 / 26/2864 / +0/+0 | 49 / 50 |
| `TestW34Probe` | opt ExtraBody hoisted | 50 / 50 | 24/2840 / 24/2840 / +0/+0 | 24/2840 / 24/2840 / +0/+0 | 50 / 50 |
| `TestW34Probe` | opt Retry inline | 50 / 50 | 23/2808 / 23/2808 / +0/+0 | 23/2808 / 23/2808 / +0/+0 | 50 / 50 |
| `TestW34Probe` | opt Retry hoisted | 50 / 50 | 23/2808 / 23/2808 / +0/+0 | 23/2808 / 23/2808 / +0/+0 | 50 / 50 |
| `TestW34Probe` | opt all5 inline | 50 / 50 | 35/3456 / 35/3456 / +0/+0 | 35/3456 / 36/3504 / +1/+48 | 50 / 49 |
| `TestW34Probe` | opt all5 hoisted | 50 / 50 | 33/3432 / 33/3432 / +0/+0 | 33/3432 / 33/3432 / +0/+0 | 50 / 50 |

B5, call-M-2.txt (M) and call-L.txt (L) (-count=10), against W5.1's call-{M,L}.txt (-count=10):

| Benchmark | (M) ns/op min / median | (M) W5.1 median, W3.4 / W5.1 | (L) ns/op min / median | (L) W5.1 median, W3.4 / W5.1 | allocs/op (M) / (L) | B/op median (M) / (L) |
| --- | --- | --- | --- | --- | ---: | ---: |
| `Call/sdk` | 5.041 µs / 5.126 µs | 4.821 µs, 1.063 | 6.176 µs / 6.205 µs | 6.104 µs, 1.016 | 22 / 22 | 2990 / 2994 |
| `Call/floor` | 469.8 ns / 519.3 ns | 467.1 ns, 1.112 | 762.6 ns / 765.2 ns | 760.6 ns, 1.006 | 8 / 8 | 678 / 694 |
| `Call/naive` | 3.742 µs / 4.022 µs | 3.616 µs, 1.112 | 6.874 µs / 6.968 µs | 6.885 µs, 1.012 | 54 / 68 | 11386 / 8986 |
| `Call/naive-json` | 8.531 µs / 9.046 µs | 8.244 µs, 1.097 | 16.834 µs / 16.897 µs | 16.811 µs, 1.005 | 114 / 114 | 7898 / 7946 |
| `Call/sdk-q20` | 24.088 µs / 24.869 µs | 23.733 µs, 1.048 | 25.722 µs / 25.887 µs | 25.383 µs, 1.020 | 42 / 42 | 11438 / 11396 |
| `Call/floor-q20` | 524.0 ns / 537.7 ns | 497.8 ns, 1.080 | 797.4 ns / 799.9 ns | 770.8 ns, 1.038 | 8 / 8 | 684 / 692 |
| `Call/naive-q20` | 13.189 µs / 14.320 µs | 12.625 µs, 1.134 | 26.156 µs / 26.422 µs | 25.817 µs, 1.023 | 127 / 247 | 40369 / 34657 |
| `Call/naive-json-q20` | 38.998 µs / 40.013 µs | 38.944 µs, 1.027 | 77.031 µs / 77.258 µs | 76.720 µs, 1.007 | 517 / 517 | 31004 / 31215 |

| Ratio | (M) call-M-2.txt | (L) call-L.txt |
| --- | ---: | ---: |
| q3 `call/sdk` / `call/naive`, median ns/op | 1.274 | 0.890 |
| q3 `call/sdk` / `call/naive`, min ns/op | 1.347 | 0.898 |
| q3 `call/sdk` / `call/naive-json`, median ns/op | 0.567 | 0.367 |
| q20 `call/sdk` / `call/naive`, median ns/op | 1.737 | 0.980 |
| q20 `call/sdk` / `call/naive`, min ns/op | 1.826 | 0.983 |
| q20 `call/sdk` / `call/naive-json`, median ns/op | 0.622 | 0.335 |

The earlier (M) run, under more load:

| Ratio | (M) call-M.txt | (L) call-L.txt |
| --- | ---: | ---: |
| q3 `call/sdk` / `call/naive`, median ns/op | 1.304 | 0.890 |
| q3 `call/sdk` / `call/naive`, min ns/op | 1.362 | 0.898 |
| q3 `call/sdk` / `call/naive-json`, median ns/op | 0.568 | 0.367 |
| q20 `call/sdk` / `call/naive`, median ns/op | 2.008 | 0.980 |
| q20 `call/sdk` / `call/naive`, min ns/op | 1.913 | 0.983 |
| q20 `call/sdk` / `call/naive-json`, median ns/op | 0.644 | 0.335 |

Every series' minimum is the same on (M) and (L).

## W4.2: the typed decode (AC-P3)

W4.2 adds `DecodeAs[T]` and `Ask[T]`: the answers a response already
holds are copied into the answer fields of a struct type, through
W4.1's cached plan and one reflect field pointer per field, without
`unsafe`. `TestAllocTypedDecode` (root, `//go:build !race`, a bound name
of section 11) measures on `result.json`, in one run, the decode that
fills `Answers()` as a call makes it (the pooled decoder warm, a fresh
result, the question set `Ask` sends for the test's `reviewAnswers` and
the call's model, interned) and `DecodeAs[reviewAnswers]` of the decoded
response; then a whole `SystemOne` call over the Recorder with that
question set, and `Ask[reviewAnswers]` over the same Recorder. Counts are
`runtime.ReadMemStats` deltas under `testsupport.QuietRuntime` (collector
off, `GOMAXPROCS(1)`), the minimum that three of five runs share. The
test pins `DecodeAs` at exactly 1/144 B, AC-P3's inequality, and
`Ask = SystemOne + DecodeAs` (AC-P6: `Ask` adds nothing to a call). Raw
outputs are in `_spikes/w4.2/results/`, written by
`_spikes/w4.2/gates.sh` (the measurement, then `-race ./...` and
ci.yaml's root allocation step, each under the bench lock). Commands use
`R=_spikes/s-c1/run.sh` (W0.5's runner), `O=_spikes/w4.2/results`, the
lead's `SP` (whose `bench.lock` the lanes share) and `BASE=306af0d`, on
main 7778166 (W4.1 landed, R103 reverted; the typed error takes the
client's header redactor). Ruling R111 then rewrote the branch's last
commits so that each passes alone on that main; 8d1f6ed, the last commit
before this section's own, has the tree 306af0d had
(`git rev-parse 8d1f6ed^{tree}` = `306af0d^{tree}` = `18a279946b04`),
so the rows hold for it unchanged. (L) ran the tree
`git archive 306af0d` wrote, piped over ssh to `/tmp/ts-spike/src-w4.2`,
with the section 11 toolchain and caches under `/tmp/ts-spike` and no
`GOEXPERIMENT`, under `flock /tmp/ts-spike/bench.lock`. The branch was measured at each of its
bases while W4.1's review fix pass and its rebases moved it, and after
W4.2's own review fix passes, with the same counts and bytes every time;
these rows are the last.

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W4.2-01 | 2026-09-26 15:15:39 JST | W4.2 AC-P3 typed decode | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 11.71 → 11.71 | `BASE=306af0d GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock alloc-M -count=1 -run '^TestAllocTypedDecode$' -v .` | `result.json` (364 B): Answers() decode 4/688 B; `DecodeAs[reviewAnswers]` 1/144 B; `SystemOne` 22/2648 B; `Ask[reviewAnswers]` 23/2792 B | mallocs/bytes, collector off, `GOMAXPROCS(1)`, 3 of 5 runs agree (all 5 did); `results/alloc-M.txt` |
| W4.2-02 | 2026-09-26 06:17:07 UTC | W4.2 AC-P3 typed decode | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.00 → 0.00 | `BASE=306af0d sh $R '(L)' $O /tmp/ts-spike/bench.lock alloc-L -count=1 -run '^TestAllocTypedDecode$' -v .` | identical to W4.2-01 in every count and byte | `results/alloc-L.txt` |
| W4.2-03 | 2026-09-26 15:18:52 JST | gates at 306af0d: `-race ./...`, then ci.yaml's root allocation step with its `-list` guard | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.17 → 6.08 | `BASE=306af0d GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock race-all-M -timeout 60m -race -count=1 ./...`; the step's two lines as ci.yaml has them, under the lock (15:17:03 JST) | ok in all seven packages; guard 8 names; the step ok. The first `-race` run, at 15:15:40 JST, failed one row of internal/testsupport's TestProxyRefusals (`200 Connection established` where a refused dial should give 502): the test closes a listener and expects its port to stay free, which another process on the loaded host can take first; 200 isolated `-race` runs of the test passed at 15:17:55 JST, and this row's run passed | `results/race-all-M.txt`, `results/race-all-M-flake.txt` (the failed run), `results/root-alloc-step-M.txt` |
| W4.2-04 | 2026-09-26 06:17:09 UTC | the same gates | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.00 → 0.13 | as W4.2-03, `race-all` | ok in all seven packages; guard 8 names; the step ok | `results/race-all-L.txt`, `results/root-alloc-step-L.txt` |

### W4.2 findings

1. **AC-P3 holds on both hosts: `DecodeAs` costs 1 allocation where the
   decode it reads costs 4.** The one allocation is the struct being
   decoded (144 B, `reviewAnswers`'s size): `reflect.Value.Interface`,
   which yields each field's pointer without boxing an answer, makes the
   compiler move the `T` to the heap. The answers themselves are copies
   of `Answers()`'s values and share their slices, so they cost nothing
   at any size. Storing each answer with `reflect.Value.Set` instead was
   measured at 4/304 B (each answer escapes as well) and dropped. Zero
   would need `unsafe` field offsets or a per-type pool of `T` values
   that keeps a response's slices alive between calls; section 6.6 keeps
   offsets out unless W4.3's S-D2 shows reflect more than 2x slower, and
   the pool is not worth one allocation.
2. **`Ask` adds nothing to a call beyond `DecodeAs`** (AC-P6, R77/R28):
   23 = 22 + 1 on both hosts. `PreparedFor[T]` is a cache hit (W4.1: 0
   allocations) and the call options pass through.

## W4.3: S-D2 (how the typed decode stores an answer) and `PreparedFor`'s first call

W4.3 measures, without changing a production file, what section 6.6
asks of spike S-D2 (does `DecodeAs`'s reflect-only store cost more than
twice a store through `unsafe` field offsets?), what the first
`PreparedFor[T]` call for a type allocates, and AC-P3's typed-decode row
again at the base. The code is `_spikes/w4.3/` (package `sd2`), outside
`./...` and the seam test's `unsafe` rule. Its decodes are replicas of
the root package's `typedPlan.decode`, with the same plan cache keyed by
`reflect.Type`, the same `wire.Answers.Get` lookup, and the same kind,
option and level checks, on answer types laid out as the root package's.
They differ only in how an answer reaches its field:

- **(1) `1-reflect-addr`**, the store as built:
  `v.Field(i).Addr().Interface()` type-asserted to the answer type's
  pointer. The `T` moves to the heap, because `reflect.ValueOf(&t)` lets
  it escape.
- **(2) `2-unsafe-offset`**: the field's `reflect.StructField.Offset`,
  taken once per type, and a store through `unsafe.Add(unsafe.Pointer(&t),
  off)`. The `T` stays on the stack (`-gcflags=-m`: `base does not
  escape`).
- **(3) `3-reflect-set`**: `v.Field(i).Set(reflect.ValueOf(answer))`, which
  boxes each answer as well.
- **(2h) `2h-unsafe-offset-heap`**, a diagnostic and not a candidate:
  (2) with the `T` forced onto the heap, as (1) puts it there. (1) − (2h)
  is the reflect work per field; (2h) − (2) is the allocation of the `T`
  and the collector's share of it.

`0-DecodeAs` is `typesafe.DecodeAs` itself, over the response of a
`SystemOne` call over the Recorder with the same question set. It is
measured beside the replicas. `TestVariantsAgree` checks that every
replica decodes each fixture into the value `DecodeAs` returns, field
for field, the unexported answer and presence bit included.
`TestReplicasRefuse` checks that the replicas refuse what `DecodeAs`
refuses (seven cases). `TestSD2Allocs` checks that (1) and (2h) allocate
exactly what `DecodeAs` allocates, that (2) allocates nothing and that (3)
allocates the `T` and one boxed answer per field. The fixtures are
`result.json` (3 answers), `result-20.json` (7 noul, 7 choice, 6 score)
and `structured-legend-flood-1k.json` (a score with 1 000 levels, whose
legend and probabilities the level check walks), typed with the root
tests' own tags.

`PreparedFor`'s first call for a type is counted by
`TestPreparedForFirstCall`. Each run is a fresh child process, the test
binary run again with only `TestFirstCallChild`. The child makes one
`PreparedFor` call for a warm-up type, then counts the first and the
second call for the measured type with `runtime.ReadMemStats` deltas
(collector off, `GOMAXPROCS(1)`). There are five children per type, and
the result is the minimum that three of them share. `testing.AllocsPerRun`
cannot count a first call, because its warm-up call is the first call. A
fresh type per run in one process would not cost the same on each run:
the plan cache is a `sync.Map`, whose hash trie
(`GOROOT/src/internal/sync/hashtriemap.go:167-190`) allocates a new
16-way node (160 B) when a new key's hash shares its top bits with a
cached key's, and that becomes likelier with every type already cached.
In a child only the warm-up type is cached, so this happens in about one
first call in 16. The measured types are `one`, W1.3's `c2-noul-short`
set as a struct (`Spam NoulAnswer` with `name=spam;instructions=Spam?`),
`ticket` (the plan's section 5 `Ticket`, as the root tests spell it),
`ten` (the first ten questions of `result-20.json`) and `twenty` (all
twenty). Each is compared with the same set built by hand with the
builder and prepared (`NewQuestions()…Prepare()` in the measured
section) and with `Prepare()` alone (W1.3's method, the builder outside
the measured section). `TestShapesMatchByHand` checks that each typed set
is byte for byte the hand-built one. The `PROFILE` step of the series
runs one child for `twenty` with every allocation sampled from the end
of the warm-up call to the end of the first call, and prints
`go tool pprof -sample_index=alloc_objects -lines -top`.

Commands use `R=_spikes/s-c1/run.sh` (W0.5's runner),
`S=_spikes/w4.3/series.sh` (the three measurements, each an `$R`
invocation with its own lock hold, then the optional profile under the
lock), `O=_spikes/w4.3/results`, the lead's `SP` (whose `bench.lock` the
lanes share) and the base d7a5968: main 7778166 with W4.2 at 61bd3da and
this wave's two spike commits. W4.2 then landed as 9c61db9, and the
branch was rebased onto it with `--onto 61bd3da`: 1839eb1 became
e27fe56 and d7a5968 became 6c65b9c. `_spikes/w4.3` is unchanged by the
rebase. Main's Go files differ from 61bd3da only in a godoc sentence of
`errors.go` and a new test in `decodeas_test.go`; `decodeas.go` and
`typed.go` are byte-identical, so the rows hold for 6c65b9c. (L) ran
the tree `git archive d7a5968` wrote, piped over ssh to
`/tmp/ts-spike/src-w4.3`,
with the section 11 toolchain and caches under `/tmp/ts-spike` and no
`GOEXPERIMENT`, under `flock /tmp/ts-spike/bench.lock`. (M) ran the
worktree at d7a5968 with `GOEXPERIMENT=nosimd,noruntimesecret` under
`/opt/homebrew/opt/util-linux/bin/flock $SP/bench.lock`. The (L) series
ran first. Times are the minimum of five `b.Loop()` runs (`-count=5`,
collector on). `benchstat` summaries are in `results/benchstat-{M,L}.txt`,
where five samples print `± ∞`. The tables under
[W4.3 tables](#w43-tables) were printed by
`_spikes/w4.3/render.py _spikes/w4.3/results`, which also checks that
(M) and (L) agree in every allocation count. One deviation, accepted by
R112 and not of record: while (2h) was being added, a
`-benchtime=2000x` smoke run of `BenchmarkSD2` (about 5 ms) ran on (M)
outside the lock, between 15:43:56 JST (the end of W4.3-09) and 15:46:06
JST (the start of W4.3-11's second `-race` run). No row uses it.

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W4.3-01 | 2026-09-26 06:46:42 UTC | W4.3 S-D2 ns/op | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.06 → 0.62 | `MAXLOAD=44 PROFILE=1 TMP=/tmp/ts-spike/w4.3-tmp sh $S '(L)' $O /tmp/ts-spike/bench.lock d7a5968 L`, first invocation: `go test -run '^$' -bench '^BenchmarkSD2$' -benchmem -count=5 ./_spikes/w4.3/` | min of 5, ns/op, in the order DecodeAs, (1), (2), (2h), (3): `result.json` 185.7, 173.5, 72.71, 132.8, 273.5; `result-20.json` 1 187, 1 117, 619.2, 873.4, 1 814; flood-1k 1 365, 961.2, 874.3, 928.1, 1 067. (1)/(2) = 2.39×, 1.80×, 1.10×; DecodeAs/(2) = 2.55×, 1.92×, 1.56×. [W4.3 tables](#w43-tables) | load gate 44, waited 0; `results/bench-L.txt`, `results/benchstat-L.txt` |
| W4.3-02 | 2026-09-26 06:48:11 UTC | W4.3 S-D2 allocations; `PreparedFor` first calls | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.62 → 0.62 | second invocation of `MAXLOAD=44 PROFILE=1 TMP=/tmp/ts-spike/w4.3-tmp sh $S '(L)' $O /tmp/ts-spike/bench.lock d7a5968 L`: `go test -count=1 -run '^(TestVariantsAgree\|TestReplicasRefuse\|TestShapesMatchByHand\|TestSD2Allocs\|TestPreparedForFirstCall)$' -v ./_spikes/w4.3/` | S-D2 mallocs/B: DecodeAs = (1) = (2h) = 1/144 (`result-20.json` 1/1024), (2) 0/0, (3) 4/304 (`result-20.json` 21/2064). First `PreparedFor` call: `one` 8/800, `ticket` 24/4720, `ten` 63/20328, `twenty` 114/41704; the second call 0/0 for each. Built by hand and prepared: 5/600, 11/3688, 25/15808, 40/32176. `Prepare()` alone: 3/224, 5/928, 13/2888, 20/5640. Warm-up call (the process's first) 9/944. [W4.3 tables](#w43-tables) | agreement tests pass; S-D2 counts 5/5 runs agree; first calls 5/5 agree on (L); `results/alloc-L.txt` |
| W4.3-03 | 2026-09-26 06:48:12 UTC | W4.3 AC-P3 and the `Ask` row at the base (deliverable C) | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.62 → 0.62 | third invocation of `MAXLOAD=44 PROFILE=1 TMP=/tmp/ts-spike/w4.3-tmp sh $S '(L)' $O /tmp/ts-spike/bench.lock d7a5968 L`: `go test -count=1 -run '^TestAllocTypedDecode$' -v .` | `result.json` (364 B): Answers() decode 4/688 B; `DecodeAs[reviewAnswers]` 1/144 B; `SystemOne` 22/2648 B; `Ask[reviewAnswers]` 23/2792 B | identical to W4.2-02; `results/typed-L.txt` |
| W4.3-04 | 2026-09-26 06:48:12 UTC | W4.3 first-call allocation sites of `twenty` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | not recorded by the step (W4.3-03 read 0.62 in the same second) | the `PROFILE` step: `go test -c -trimpath`, then `SD2_FIRST_CALL=twenty SD2_FIRST_CALL_PROFILE=… sd2.test -test.run='^TestFirstCallChild$'`, then `go tool pprof -sample_index=alloc_objects -lines -top` | the 114 allocations of `twenty`'s first call, by source line: the same lines and counts as W4.3-08 | call-site attribution, not a count of record; the profile's other 17 samples are outside the SDK (the profile write and the test harness); `results/profile-L.txt` |
| W4.3-05 | 2026-09-26 15:48:34 JST | W4.3 S-D2 ns/op | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 5.14 → 6.42 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 PROFILE=1 TMP=<scratch> sh $S '(M)' $O $SP/bench.lock d7a5968 M`, first invocation: `go test -run '^$' -bench '^BenchmarkSD2$' -benchmem -count=5 ./_spikes/w4.3/` | min of 5, ns/op, in the order DecodeAs, (1), (2), (2h), (3): `result.json` 134.3, 122.7, 63.86, 94.89, 165.4; `result-20.json` 923.9, 838.4, 539.7, 654.1, 1 108; flood-1k 704.9, 693.7, 647.6, 674.6, 747.5. (1)/(2) = 1.92×, 1.55×, 1.07×; DecodeAs/(2) = 2.10×, 1.71×, 1.09×. [W4.3 tables](#w43-tables) | load gate 16, waited 0; not noisy; `results/bench-M.txt`, `results/benchstat-M.txt` |
| W4.3-06 | 2026-09-26 15:50:04 JST | W4.3 S-D2 allocations; `PreparedFor` first calls | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.42 → 6.42 | second invocation of `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 PROFILE=1 TMP=<scratch> sh $S '(M)' $O $SP/bench.lock d7a5968 M`: `go test -count=1 -run '^(TestVariantsAgree\|TestReplicasRefuse\|TestShapesMatchByHand\|TestSD2Allocs\|TestPreparedForFirstCall)$' -v ./_spikes/w4.3/` | identical to W4.3-02 in every count and byte | 3 of the 20 first-call runs (one each for `one`, `ticket` and `ten`) cost 1 more allocation of 160 B, the trie node; the minimum is shared by 4 of 5 runs; `results/alloc-M.txt` |
| W4.3-07 | 2026-09-26 15:50:04 JST | W4.3 AC-P3 and the `Ask` row at the base (deliverable C) | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.42 → 6.42 | third invocation of `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 PROFILE=1 TMP=<scratch> sh $S '(M)' $O $SP/bench.lock d7a5968 M`: `go test -count=1 -run '^TestAllocTypedDecode$' -v .` | identical to W4.3-03 | identical to W4.2-01; `results/typed-M.txt` |
| W4.3-08 | 2026-09-26 15:50:34 JST | W4.3 first-call allocation sites of `twenty` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | not recorded by the step (W4.3-07 read 6.42 at 15:50:05) | the `PROFILE` step: `go test -c -trimpath`, then `SD2_FIRST_CALL=twenty SD2_FIRST_CALL_PROFILE=… sd2.test -test.run='^TestFirstCallChild$'`, then `go tool pprof -sample_index=alloc_objects -lines -top` | 114 allocations by source line (at d7a5968): `typed.go:830` parseOptions 21, `typed.go:340` planField's error prefix 20, `typed.go:850` parseLevels 19, `typed.go:413` labels 7, `typed.go:414` options 7, `questions.go:309` prepareChoice 7, `typed.go:283` entries 6, `typed.go:284` fields 6, `typed.go:431` levels 6, `questions.go:324` prepareScore 6, `wire/prepared.go:103` index 4, `slices.Grow` in `Builder.Grow` 2, `questions.go:235` the `*Prepared` 1, `typed.go:301` the plan 1, `hashtriemap.go:572` the cache entry 1 | the sum is the counted 114 exactly, so no allocation went to the tiny allocator unsampled; 4 other samples are outside the SDK; `results/profile-M.txt` |
| W4.3-09 | 2026-09-26 06:42:42 UTC | W4.3 S-D2, the first (L) series, at 1839eb1 (before (2h)) | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.00 → 1.04 | `MAXLOAD=44 sh $S '(L)' $O /tmp/ts-spike/bench.lock 1839eb1 L` (the three invocations, no profile) | (1)/(2) = 2.42×, 1.80×, 1.09× and DecodeAs/(2) = 2.62×, 1.89×, 1.53× (`result.json`, `result-20.json`, flood-1k); allocations and TYPED identical to W4.3-02/-03 | superseded by W4.3-01..04; this run's 2.42× on `result.json` is why (2h) was added; `results/{bench,alloc,typed}-L-base1839eb1.txt` |
| W4.3-10 | 2026-09-26 06:54:42 UTC | W4.3 the two copies of `undeclaredLevel` in the spike binary | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.26 (06:53:59) | `go test -c ./_spikes/w4.3/`, `go tool objdump -s undeclaredLevel`; `perf stat` was refused (`kernel.perf_event_paranoid` = 3) | the root package's copy and the spike's have the same 89 instructions. The root package's legend loop (0x9964fe–0x996510) and probability loop (0x996578–0x99658f) each cross a 64-byte boundary; the spike's (0x9da45e–0x9da470, 0x9da4d8–0x9da4ef) do not | finding 5; `results/layout-L.txt` |
| W4.3-11 | 2026-09-26 15:41:04 JST | gates before each commit: the section 11 chain, then `go test -race -count=1 ./...` under the lock | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.27 → 7.94 (1839eb1); 6.93 → 8.16 (d7a5968) | the chain as section 11 has it, with `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` for govulncheck; `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=… sh $R '(M)' <scratch> $SP/bench.lock race-all-M-… -timeout 60m -race -count=1 ./...` | chain: 0 issues, no vulnerabilities; `-race`: ok in all seven packages, before 1839eb1 (15:41:04 JST) and before d7a5968 (15:46:06 JST) | the installed govulncheck is built with go1.26 and cannot load go1.27 sources, so the chain runs it with `go run`, as W5.4 did; each `-race` run was of the tree its commit then recorded, so the headers name the base as uncommitted; `results/race-all-M-1839eb1.txt`, `results/race-all-M-d7a5968.txt` |
| W4.3-12 | 2026-09-26 16:01:02 JST | gates at 6c65b9c, the Go tree of this section's commit: the section 11 chain, then the TYPED measurement, `-race ./...` and ci.yaml's root allocation step with its `-list` guard, each under the lock | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 7.90 → 6.54 | the chain as in W4.3-11 (16:00:34 to 16:00:38 JST), then `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh _spikes/w4.2/gates.sh '(M)' $O $SP/bench.lock 6c65b9c M-6c65b9c` | chain: 0 issues, no vulnerabilities; TYPED identical to W4.3-07; `-race`: ok in all seven packages; guard 8 names; the step ok | W4.2's committed gates script; its three invocations took the lock at 16:01:02, 16:02:03 and 16:04:06 JST, the lock being held by other lanes in between; `results/{alloc,race-all,root-alloc-step}-M-6c65b9c.txt` |
| W4.3-13 | 2026-09-26 07:04:20 UTC | the same gates, without the chain | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.07 → 0.63 | `sh _spikes/w4.2/gates.sh '(L)' $O /tmp/ts-spike/bench.lock 6c65b9c L-6c65b9c` on the tree `git archive 6c65b9c` wrote | TYPED identical to W4.3-03; `-race`: ok in all seven packages; guard 8 names; the step ok | `results/{alloc,race-all,root-alloc-step}-L-6c65b9c.txt` |

### W4.3 tables

Printed by `_spikes/w4.3/render.py` from `results/bench-{M,L}.txt` and
`results/alloc-{M,L}.txt`. In the S-D2 tables, (1) is `1-reflect-addr`,
(2) `2-unsafe-offset`, (2h) `2h-unsafe-offset-heap` and (3)
`3-reflect-set`; the spread is the slowest of the five runs over the
fastest.

S-D2: min ns/op of 5 (spread max/min), B/op, allocs/op

| Fixture | Variant | (M) ns/op | (L) ns/op | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: | ---: |
| `result.json` | 0-DecodeAs | 134.3 (×1.043) | 185.7 (×1.004) | 144 | 1 |
| `result.json` | 1-reflect-addr | 122.7 (×1.040) | 173.5 (×1.003) | 144 | 1 |
| `result.json` | 2-unsafe-offset | 63.86 (×1.012) | 72.71 (×1.002) | 0 | 0 |
| `result.json` | 2h-unsafe-offset-heap | 94.89 (×1.033) | 132.8 (×1.005) | 144 | 1 |
| `result.json` | 3-reflect-set | 165.4 (×1.046) | 273.5 (×1.005) | 304 | 4 |
| `result-20.json` | 0-DecodeAs | 923.9 (×1.045) | 1.187 µs (×1.005) | 1024 | 1 |
| `result-20.json` | 1-reflect-addr | 838.4 (×1.137) | 1.117 µs (×1.006) | 1024 | 1 |
| `result-20.json` | 2-unsafe-offset | 539.7 (×1.024) | 619.2 (×1.002) | 0 | 0 |
| `result-20.json` | 2h-unsafe-offset-heap | 654.1 (×1.037) | 873.4 (×1.007) | 1024 | 1 |
| `result-20.json` | 3-reflect-set | 1.108 µs (×1.021) | 1.814 µs (×1.008) | 2064 | 21 |
| `structured-legend-flood-1k.json` | 0-DecodeAs | 704.9 (×1.018) | 1.365 µs (×1.005) | 144 | 1 |
| `structured-legend-flood-1k.json` | 1-reflect-addr | 693.7 (×1.012) | 961.2 (×1.002) | 144 | 1 |
| `structured-legend-flood-1k.json` | 2-unsafe-offset | 647.6 (×1.015) | 874.3 (×1.003) | 0 | 0 |
| `structured-legend-flood-1k.json` | 2h-unsafe-offset-heap | 674.6 (×1.763) | 928.1 (×1.002) | 144 | 1 |
| `structured-legend-flood-1k.json` | 3-reflect-set | 747.5 (×1.700) | 1.067 µs (×1.004) | 304 | 4 |

Ratios of the minimums

| Fixture | Host | (1)/(2) | DecodeAs/(2) | (3)/(2) | allocation (2h)−(2) | reflect per field ((1)−(2h))/fields |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| `result.json` | (M) | 1.92× | 2.10× | 2.59× | 31.0 ns | 9.27 ns |
| `result.json` | (L) | 2.39× | 2.55× | 3.76× | 60.1 ns | 13.57 ns |
| `result-20.json` | (M) | 1.55× | 1.71× | 2.05× | 114.4 ns | 9.21 ns |
| `result-20.json` | (L) | 1.80× | 1.92× | 2.93× | 254.2 ns | 12.18 ns |
| `structured-legend-flood-1k.json` | (M) | 1.07× | 1.09× | 1.15× | 27.0 ns | 6.37 ns |
| `structured-legend-flood-1k.json` | (L) | 1.10× | 1.56× | 1.22× | 53.8 ns | 11.03 ns |

PreparedFor first call, mallocs/bytes (identical on both hosts)

| Type | Fields | First call | Second call | Same set by hand (builder + Prepare) | Prepare alone | Typed extra | Per field |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `one` | 1 | 8/800 | 0/0 | 5/600 | 3/224 | 3/200 | 3.00/200.0 |
| `ticket` | 4 | 24/4720 | 0/0 | 11/3688 | 5/928 | 13/1032 | 3.25/258.0 |
| `ten` | 10 | 63/20328 | 0/0 | 25/15808 | 13/2888 | 38/4520 | 3.80/452.0 |
| `twenty` | 20 | 114/41704 | 0/0 | 40/32176 | 20/5640 | 74/9528 | 3.70/476.4 |

Warm-up call (the process's first PreparedFor): 9/944

### W4.3 findings

1. **S-D2 verdict: keep reflect (ruling R112). Per section 6.6, no
   `unsafe` enters the root package.** Section 6.6 asks whether the
   reflect-only store is more than twice as slow as a store through
   field offsets, on both hosts. Compared like for like, the replica of
   the store as built, (1), against (2) is:
   - `result.json`: 1.92× (M), 2.39× (L);
   - `result-20.json`: 1.55× (M), 1.80× (L);
   - the 1k flood: 1.07× (M), 1.10× (L).

   (M) is below 2× on every fixture, so the bar is not met. The rest of
   (2)'s advantage comes from escape analysis, not from reflection: in
   (2) the `T` stays on the stack (finding 2). `DecodeAs` runs 11.6 ns (M)
   and 12.2 ns (L) above the replica, overhead that an offsets build
   would keep. Such a build would come to about 75.5 ns (M) and 84.9 ns
   (L), so today's `DecodeAs` takes 1.78× (M) and 2.19× (L) its time:
   again one host under the bar.

   Read the other way, with `DecodeAs` itself as (1), the ratios are
   2.10× (M) and 2.55× (L) on `result.json`, 1.71× and 1.92× on
   `result-20.json`, and 1.09× and 1.56× on the flood. Both readings are
   recorded here so the owner sees both. The stake is small (finding 3).

   R112 routes one follow-up to W5.3, best effort: the `T`'s heap
   allocation in `DecodeAs` (1/144) is an escape, not a cost of
   reflection, and a decode into storage the caller provides, or a
   construction that does not let the `T` escape, could remove it
   without `unsafe`.
2. **Most of the difference is the allocation of the `T`, not reflection.**
   On `result.json`, (2h) − (2) = 31.0 ns (M) and 60.1 ns (L). That is the
   cost of allocating the 144-byte `T` plus the collector's share of it,
   the one allocation AC-P3's pin already counts. The reflect work,
   (1) − (2h), is 9.3 ns (M) and 13.6 ns (L) per field: `Field`, `Addr`,
   `Interface` and the type assertion. At 20 answers the `T` is 1 KiB,
   and allocating it costs 114 ns (M) and 254 ns (L), while the reflect
   work stays at 9.2 ns and 12.2 ns per field. On the 1k flood, the
   level checks walk 2 000 entries and outweigh either cost, so
   (1)/(2) = 1.07× (M) and 1.10× (L).
3. **What offsets would save in a call.** A q3 `Call/sdk` over the
   Recorder takes 5.041 µs (M) and 6.176 µs (L) (W3.4's B5 table,
   minimums). On `result.json`, offsets would save at most
   `DecodeAs` − (2) = 70.4 ns (M) and 113.0 ns (L) per `Ask`, 1.4 % and
   1.8 % of that call. An offsets build that keeps `DecodeAs`'s own
   overhead would save (1) − (2) = 58.8 ns and 100.8 ns, 1.2 % and 1.6 %.
   Over a network, either share is smaller.
4. **(3) `reflect.Value.Set` is the slowest and allocates the most.** It
   allocates the `T` plus one boxed answer per field (W4.2's 4/304
   reproduced; 21/2064 at 20 answers) and runs 2.59× (M) and 3.76× (L)
   the time of (2) on `result.json`. It stays dropped, as W4.2 left it.
5. **`DecodeAs` against its replica.** On `result.json` and
   `result-20.json`, `DecodeAs` runs 6–10 % slower than the replica of (1)
   on both hosts. It passes the response, the endpoint and the header
   redactor, which the replica does not; which of these costs the time
   was not measured. On the 1k flood on (L), `DecodeAs` runs 404 ns (42 %)
   slower than the replica, while on (M) the gap is 11 ns. The two
   copies of `undeclaredLevel` in the binary have the same 89
   instructions, but the root package's legend and probability loops
   each cross a 64-byte boundary on (L) and the spike's do not
   (W4.3-10). That is consistent with the cost coming from where the
   linker placed the loops, but it is not established: `perf` counters
   were refused on (L). So `DecodeAs/(2)` on the flood on (L), 1.56×,
   measures loop placement rather than the store. The replica-to-replica
   ratio (1)/(2) is the comparison of the store.
6. **`PreparedFor`'s first call** costs 3.0–3.8 more allocations and
   200–476 B more per field than building and preparing the same set by
   hand. In allocations, that is 2.7× (`one`) to 5.7× (`twenty`) the
   cost of `Prepare()` alone. The second call allocates nothing (W4.1's
   cache pin holds). Of `twenty`'s 74 extra allocations:
   - 20 are the error prefix `planField` builds for every field
     (`typed.go:340`), which is used only when the field is refused.
   - 40 are the growth of `parseOptions`' and `parseLevels`' slices,
     which are appended from nil: 2 to 4 per choice or score.
   - 7 are the `labels` copy per choice, made for the repeat check.
   - 6 are the growth of the plan's `fields` slice.
   - 1 is the plan itself, and 1 the cache entry.

   That is 75. The hand-built path makes one allocation the typed path
   does not, `NewQuestions()`'s `*Questions`, because `buildPlan` keeps
   its `Questions` on the stack; 75 − 1 = 74. Everything else, the
   option and level tables (13), the entries' growth (6) and `Prepare`'s
   own 20, is made by the builder path as well. Against W1.3 (R51):
   `one`'s `Prepare()` alone is 3/224, W1.3-01's "one noul"
   (`c2-noul-short`), so `Prepare` has not moved since b227e5b for that
   set. W1.3's `c1-sketch` (9/1 016) holds a raw question and is not
   `ticket`'s set, whose `Prepare()` alone is 5/928. A W5.3 note, since
   each cost is paid once per type per process: building the error
   prefix only when a field is refused saves 1 allocation per field, and
   sizing the option and level slices from a count of separators saves 1
   to 3 per choice or score.
7. **AC-P3 and `Ask` at the base (deliverable C): unchanged on both
   hosts.** `DecodeAs` 1/144 against 4/688 for the `Answers()` decode;
   `Ask` 23/2792 = `SystemOne` 22/2648 + 1. No pin moved, so no test was
   changed (deliverable D), and ci.yaml is untouched.

## W5.2: the allocation budgets, their functional halves and the CI step

W5.2 gives every allocation clause of the plan its bound test, builds each
budget with `//go:build !race` (under the race detector `sync.Pool.Put`
drops one value in four, `GOROOT/src/sync/pool.go:104-107`) and keeps its
functional half, the `…Functional` tests of `alloc_functional_test.go`, in
both builds; it writes `TestResponseCapOverTheWire` (critic-p3 M-2, R106 as
V50 worded it) and folds the root package's allocation tests into one list
in `ci.yaml`, from which the step builds both its `-run` pattern and its
`-list` count guard (R92; `TestAllocLoggedCall` joins, R102, and the
R103 revert's `TestAllocRequestID`), with a second list for
`internal/codec`'s allocation tests (R110 (3)); the step also fails on a
test of a root `!race` file that its list lacks. No
production file changes. Every row measures 6bec0e8, main 9c61db9 (W4.2
and the R103 revert landed) plus this wave's ten commits, a clean tree;
the first measurement, at 93c18a0 on main 3ffe77b, gave the same counts.
Raw outputs are in
`_spikes/w5.2/results/`, and `_spikes/w5.2/render.py` prints the tables
below from them. Commands use `R=_spikes/s-c1/run.sh`,
`O=_spikes/w5.2/results`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/40cb0f1f-c8a9-422c-a3e8-b3afc329b5cb/scratchpad`
and `BASE=6bec0e8`.

| AC clause | Test (`//go:build !race`) | Functional half (both builds) |
| --- | --- | --- |
| AC-P1 single size (NF1), every state kind × 1 KiB / 64 KiB / 1 MiB / 6 MiB | `TestAllocEncode` (folds W1.2's `TestAllocBodyKinds`: its 1 KiB pins are the 1 KiB rows) | `TestAllocEncodeFunctional` |
| AC-P1 mixed-size sequence | `TestAllocScratchSequence` | `TestAllocScratchSequenceFunctional` |
| AC-P2 | `TestAllocDecodeFixtures` | `TestAllocDecodeFixturesFunctional` |
| AC-P6 (N = 14) | `TestAllocWholeCall` | `TestAllocWholeCallFunctional` |
| AC-P5 (i)–(vii) | `TestMemStatsCap` | `TestMemStatsCapFunctional`; over a real connection, `TestResponseCapOverTheWire` |
| AC-P8 allocations | `TestLinearityFlood` (`alloc_decode_test.go`: the ratio, the members visited); the lazy pass's c₀ + c₁ × members: `TestLazyPassAllocations` (`internal/codec/decode_bench_test.go`, R110 (3)), run by ci.yaml's allocation-budget step on all three images | – |
| AC-P8 time | `TestLinearityFloodTime` (`alloc_decode_cases_test.go`, every build) | – |
| AC-P3 | `TestAllocTypedDecode` (W4.2, kept; `alloc_typed_test.go`) | W4.2's `TestDecodeAs*` (`decodeas_test.go`), under `-race` in W5.2-07 |
| Prepare (R50), payloads (W2.4), logging (W3.3, the R103 revert) | `TestAllocPrepare`, `TestAllocResponseJSON`, `TestAllocLoggedCall`, `TestAllocRequestID`, kept as they were | their packages' functional tests |

### How the numbers were taken

- (M): `go1.27.1 darwin/arm64`, `GOEXPERIMENT=nosimd,noruntimesecret`, in a
  detached worktree at 6bec0e8, under
  `/opt/homebrew/opt/util-linux/bin/flock` on `$SP/bench.lock`. Other lanes
  were working: load 4.9 to 6.4 of 16, which counts do not depend on (R17).
- (L): the same tree without `.git`, copied with the section 11 `tar | ssh`
  pipe to `/tmp/ts-spike/src-w5.2/meas`; `/tmp/ts-spike/go/bin/go`, the
  section 11 `GOPATH`, `GOMODCACHE` and `GOCACHE` under `/tmp/ts-spike`, no
  `GOEXPERIMENT`, under `flock /tmp/ts-spike/bench.lock`; load 0.15 to 1.32.
- Every file is one `-count=5` invocation of its tests: 25 runs of each
  series (five per invocation). A series' "runs of" line lists each run;
  `render.py` takes the minimum, the maximum and the spread of each counter
  over all 25, as `testsupport.Spread` does over one invocation's five, and
  counts the runs of an exact pin (`testsupport.StableMin`, three of five)
  that exceed their series' minimum.
- The three files per host split the tests so that each stays under
  R89 (4)'s 512 KB: `ac-p1` (`TestAllocEncode`, `TestAllocScratchSequence`),
  `alloc` (the rest of ci.yaml's list, `TestResponseCapOverTheWire`) and
  `codec` (`internal/codec`'s three allocation tests).

### W5.2 findings

1. **AC-P1 single size: every asserted case costs exactly E(kind) + B on
   both hosts at every size, with E_sonic measured in the same run equal
   to the frozen E(kind)** (1; 0 for RawJSON; 1 + m for a state with m
   maps: 9, 471, 7 503 and 45 010 for the nested map), and exactly the
   boxing's bytes above E_sonic's: 16 B for a bare string (its header), 24 B
   for a bare RawJSON (a slice header), 0 otherwise, on every run of both
   hosts and the three CI images, so the test pins them exactly (review
   MINOR 2), inside the frozen 112 B. Each case starts from emptied pools, as a
   process sending only that size does: in a first version the cases
   shared one pooled scratch, grown to 6.5 MiB by the string case, and the
   arm64 overshoot disappeared from every later case.
2. **The arm64 exception is the 6 MiB `*struct` state alone at the body
   level.** Its growslice steps end at 8.86 MiB, past the ceiling, so each
   call pays a fresh scratch and its growth: 23 allocations, 30 469 392 B
   (frozen: 22 and 27.6 MB for S-E1's struct, whose items nest one more
   struct). The flat map that S-E1 encoded alone ended at 8.76 MiB; inside
   a request body, after `{"state":`, the same map ends at 7.51 MiB and is
   warm (2). Where the steps end depends on the body's exact length at
   each step. The test keys the exception by GOARCH (R62) and fails if the
   exception stops overshooting; R110 (1) adds this in-body row to
   frozen-budgets.md beside S-E1's, with the exception narrowed to
   `*struct/6MiB`. On (L) every kind is warm at 6 MiB
   (6.44 MiB for the growslice kinds). A nested map is encoded in the map's
   random order and reaches its steady state after up to three unmeasured
   calls on (M) (scratch 6.28–7.71 MiB over the five invocations); the test
   warms each case until two calls in a row leave the same scratch.
3. **AC-P1 sequence: the frozen rows hold exactly on both hosts.** Boxed
   string, encode level (call less the 21 allocations a call spends
   outside its encode): `1×9 | 2 | 1×10 | 2 | 2 | 1×9`; RawJSON:
   `0×9 | 1 | 0×10 | 1 | 1 | 0×9`. The single-size call is 22/2 648 B
   (boxed string; the AC-P6 call) and 21/2 632 B (RawJSON); growth within
   g₆ = g₉ = 2 and 1; call 23 = single + 1; the probed run finds one drop,
   after call 22, and no retained scratch over 8 MiB. Recorded (G3 (b)): on
   (M) the `*struct` state also drops at call 11 (calls
   `50 | 22×9 | 43 | 23 | 22×9 | 44 | 23 | 22×9`), the flat map does not;
   on (L) neither does. Call 1, after two collections, costs 50 (46 for
   RawJSON), as R22b recorded.
4. **AC-P2 over every fixture that decodes, with the naive ratio
   asserted: `result.json` 4 against `naive.Sonic`'s 26 on (M) (0.154) and
   40 on (L) (0.100).** Fourteen of the 41 fixtures decode; the other 27,
   `models.json` and the malformed and deviation bodies, are logged with
   their errors, and a fixture that decodes without a pin fails. Reported:
   `escaped-member-names` is above half the naive decode (0.707 (M), 0.569
   (L)), `deviation-lone-surrogate` on (M) (0.542); the 20-answer fixture
   0.255 and 0.112. The naive decode refuses `parity-big-exp-unknown`
   (sonic: "float infinity") on both hosts.
5. **AC-P6: q3 unchanged, 14/2 008 above the floor 8/640 (call 22/2 648);
   q20 recorded at 34/9 248 (call 42/9 888; decode 24/6 008, `readBody`
   1/2 304), W3.4's probe numbers, now in the test.** The functional half
   checks that the floor's prebuilt request is byte for byte the call's.
6. **AC-P5: (i)–(v) as at W3.4; (vi) and (vii) bounded by W5.2** at their
   first read buffer + 64 KiB, the rule of the frozen cases (R110 (2)):
   65 901 B = 365 B (Content-Length + 1) + 65 536 B, and 69 632 B = 4 096 B
   (R27's first undeclared buffer) + 65 536 B. Measured 2 392 B = 384 +
   2 008 and 6 104 B = 4 096 + 2 008 on both hosts and the three CI images
   (maximum 6 152 B on xcode-27): margins 27.6 × and 11.3 ×, since the
   bound catches a buffer sized from a declared length, not steady-state
   noise. Every run is checked against its bound. K32's residual stays in (i): spread +4/+288 on both hosts
   (maximum 42/264 320), within 327 680 B.
7. **AC-P8: allocation ratio 7.61 (90 → 685); members visited, counted
   from the fixtures with encoding/json, 1 011 and 10 011, frozen-budgets'
   inputs; the whole decode grows 0.0661 allocations per member visited,
   under the lazy pass's c₁ = 1/15 = 0.0667 (recorded).** The lazy pass
   alone, in `internal/codec`: 86 and 681 against the bounds 89 and 689,
   ratio 7.92, and the members the pass itself visits are pinned there too
   (1 011 and 10 011, review MINOR 3), so a pass that also read unflagged
   answers fails in `internal/codec`, not only through root's AC-P2 pins; the budget step's codec list runs it without -race on every
   image (R110 (3)). Time ratio, `TestLinearityFloodTime`: 9.36–9.57 on (M),
   9.23–9.26 on (L); under `-race` on (M) 9.67 (W5.2-07).
8. **`TestResponseCapOverTheWire`: one attempt under `DefaultRetry()`,
   `*ResponseTooLargeError` with status 200 and the 16 MiB limit; the SDK
   read none of the declared body and exactly cap + 1 = 16 777 217 bytes
   of the undeclared one**, counted under it on the SDK's own HTTP/2
   transport. Recorded, not bounded (V50 (3)): the call's MemStats deltas
   over the in-process loopback server, 0.29 MiB declared and 32.9 MiB
   undeclared on (M), 0.32 and 36.1 MiB on (L).
9. **The three-of-five rule's failure probability.** Over these files,
   3 of 5 700 exact-pin runs on (M) and 5 of 5 725 on (L) exceeded their
   series' minimum (p = 0.00053 and 0.00087). At R104-corr's measured (L) rate of
   1 run in 100 (W3.4-04), one invocation of an exact pin fails with
   probability sum over k = 3..5 of C(5,k) p^k (1 − p)^(5−k) = 9.85 × 10⁻⁶,
   about 10⁻⁵ as ruled; at this session's rates, 1.5 × 10⁻⁹ and 6.7 × 10⁻⁹.
10. **AC-P3 and the K38 guard.** W4.2's `TestAllocTypedDecode` joins the
    step's root list (its 7 → 8 guard is folded in): `DecodeAs` 1/144
    against the `Answers()` decode's 4/688, `Ask` 23 = `SystemOne` 22 + 1,
    on both hosts. The step fails when a test of a root `!race` file is
    missing from its list (K38, after `TestAllocLoggedCall` and the R103
    revert's `TestAllocRequestID` had run in no CI step); checked both
    ways: with every name listed it passes, and with
    `TestAllocTypedDecode` dropped it fails naming it.
11. **Gates.** Each of the ten commits passed, alone on 9c61db9 (R111),
    the section 11 lint chain, `go test -race -count=1 ./...` and both
    non-race steps of ci.yaml on (M), with `set -o pipefail` in the script
    (the last commit's: `gates-M-6bec0e8.txt`). On (L), `go test -race
    -count=1 ./...` passed in all seven packages (W5.2-08, R62) and ci.yaml's
    two non-race steps passed (`steps-L-6bec0e8.txt`). Under `-race`, the
    budgets are left out by their build tag and the functional halves run:
    13 tests, W4.2's six `TestDecodeAs*` included, 83 subtests (W5.2-07).

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W5.2-01 | 2026-09-26 15:50:05 JST | W5.2 AC-P1: `TestAllocEncode`, `TestAllocScratchSequence` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.42 → 5.11 | `BASE=6bec0e8 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock sh $R '(M)' $O $SP/bench.lock ac-p1-M -count=5 -run '^(TestAllocEncode\|TestAllocScratchSequence)$' -v .` | PASS; every asserted case E(kind) + B; `pointer-to-struct/6MiB` recorded 23/30469392 (scratch 8.86 MiB); sequence rows as frozen; 1 of 1650 exact-pin runs above its minimum | [W5.2 tables](#w52-tables); `results/ac-p1-M.txt` |
| W5.2-02 | 2026-09-26 06:50:06 UTC | W5.2 AC-P1 | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.15 → 0.60 | `BASE=6bec0e8 sh $R '(L)' /tmp/ts-spike/src-w5.2/results /tmp/ts-spike/bench.lock ac-p1-L -count=5 -run '^(TestAllocEncode\|TestAllocScratchSequence)$' -v .` | PASS; every case asserted, 6 MiB growslice kinds 6.44 MiB; sequence rows as frozen; 2 of 1650 above | `results/ac-p1-L.txt` |
| W5.2-03 | 2026-09-26 15:50:38 JST | W5.2 AC-P2, AC-P3, AC-P5, AC-P6, AC-P8, the kept tests, the cap over the wire | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 5.11 → 4.92 | the W5.2-01 command with `alloc-M -count=5 -run '^(TestAllocPrepare\|TestAllocDecodeFixtures\|TestLinearityFlood\|TestLinearityFloodTime\|TestAllocWholeCall\|TestMemStatsCap\|TestAllocResponseJSON\|TestAllocLoggedCall\|TestAllocRequestID\|TestAllocTypedDecode\|TestResponseCapOverTheWire)$' -v .` | PASS; `result.json` 4 / naive 26 = 0.154; TYPED 1/144 < 4/688; q3 own 14/2008, q20 34/9248; MEM (vi) 2392 B, (vii) 6104 B; LINEARITY 7.61, time 9.36–9.57; WIRE read 0 / 16777217 | `results/alloc-M.txt` |
| W5.2-04 | 2026-09-26 06:50:48 UTC | W5.2 as W5.2-03 | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.60 → 0.69 | the W5.2-02 command with W5.2-03's `alloc-L` tests | PASS; `result.json` 4 / naive 40 = 0.100; every pin as on (M); time 9.23–9.26; (i) max 42/264320 | `results/alloc-L.txt` |
| W5.2-05 | 2026-09-26 15:50:54 JST | W5.2 `internal/codec` allocation tests | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 4.92 → 4.92 | the W5.2-01 command with `codec-M -count=5 -run '^(TestEncodeStateAllocations\|TestLazyPassAllocations\|TestNewBodyAllocations)$' -v ./internal/codec/` | PASS; LAZY flood 86 (members 1011, bound 89) and 681 (10011, 689), ratio 7.92 | `results/codec-M.txt` |
| W5.2-06 | 2026-09-26 06:51:04 UTC | W5.2 as W5.2-05 | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.69 → 0.69 | the W5.2-02 command with W5.2-05's `codec-L` tests | PASS; identical to W5.2-05 | `results/codec-L.txt` |
| W5.2-07 | 2026-09-26 15:50:54 JST | W5.2 the functional halves under `-race` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 4.92 → 5.74 | the W5.2-01 command with `race-functional-M -race -count=1 -run 'Alloc\|MemStats\|Linearity\|OverTheWire\|DecodeAs' -v .` | PASS; 13 tests (`TestLinearityFloodTime`, the five `…Functional`, `TestResponseCapOverTheWire`, W4.2's six `TestDecodeAs*`), 83 subtests; no budget test is built | `results/race-functional-M.txt` |
| W5.2-08 | 2026-09-26 06:51:04 UTC | W5.2 `-race` (R62) | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.69 → 1.32 | the W5.2-02 command with `race-L -race -count=1 ./...` | PASS, seven packages | `results/race-L.txt` |
| W5.2-09 | 2026-09-26 15:53:15 JST | W5.2 gates of the last code commit | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | build, vet, the section 11 lint chain (govulncheck via `go run`), `go test -race -count=1 ./...`, ci.yaml's two non-race steps, in a detached worktree at 6bec0e8, under the lock | every gate ok | `results/gates-M-6bec0e8.txt` (modernize's progress lines removed) |
| W5.2-10 | 2026-09-26 06:53:15 UTC | W5.2 ci.yaml's non-race steps | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | – | `go test -count=1 ./internal/codec/ ./internal/wire/ ./internal/testsupport/` and the allocation-budget step's script, in `/tmp/ts-spike/src-w5.2/meas`, under the lock | PASS: the K38 guard, both lists' `-list` guards, every listed test | `results/steps-L-6bec0e8.txt` |

### W5.2 tables

Printed by `_spikes/w5.2/render.py` from the raw files (`--series` adds a
row per series).

AC-P1 single size, the ENCODE lines of W5.2-01 and -02 (the body encode;
E_sonic and B; the scratch the steady state left, over the five
invocations; the verdict):

| Kind / size | (M) body encode; E_sonic, B; scratch; verdict | (L) |
| --- | --- | --- |
| string/1KiB | 2/32; 1/16, 1; 0.00–0.00 MiB; asserted | 2/32; 1/16, 1; 0.00–0.00 MiB; asserted |
| string/64KiB | 2/32; 1/16, 1; 0.06–0.06 MiB; asserted | 2/32; 1/16, 1; 0.07–0.07 MiB; asserted |
| string/1MiB | 2/32; 1/16, 1; 1.00–1.00 MiB; asserted | 2/32; 1/16, 1; 1.01–1.01 MiB; asserted |
| string/6MiB | 2/32; 1/16, 1; 6.00–6.00 MiB; asserted | 2/32; 1/16, 1; 6.01–6.01 MiB; asserted |
| boxed-string/1KiB | 1/16; 1/16, 0; 0.00–0.00 MiB; asserted | 1/16; 1/16, 0; 0.00–0.00 MiB; asserted |
| boxed-string/64KiB | 1/16; 1/16, 0; 0.06–0.06 MiB; asserted | 1/16; 1/16, 0; 0.07–0.07 MiB; asserted |
| boxed-string/1MiB | 1/16; 1/16, 0; 1.00–1.00 MiB; asserted | 1/16; 1/16, 0; 1.01–1.01 MiB; asserted |
| boxed-string/6MiB | 1/16; 1/16, 0; 6.00–6.00 MiB; asserted | 1/16; 1/16, 0; 6.01–6.01 MiB; asserted |
| RawJSON/1KiB | 1/24; 0/0, 1; 0.00–0.00 MiB; asserted | 1/24; 0/0, 1; 0.00–0.00 MiB; asserted |
| RawJSON/64KiB | 1/24; 0/0, 1; 0.07–0.07 MiB; asserted | 1/24; 0/0, 1; 0.07–0.07 MiB; asserted |
| RawJSON/1MiB | 1/24; 0/0, 1; 1.03–1.03 MiB; asserted | 1/24; 0/0, 1; 1.03–1.03 MiB; asserted |
| RawJSON/6MiB | 1/24; 0/0, 1; 6.22–6.22 MiB; asserted | 1/24; 0/0, 1; 6.22–6.22 MiB; asserted |
| boxed-RawJSON/1KiB | 0/0; 0/0, 0; 0.00–0.00 MiB; asserted | 0/0; 0/0, 0; 0.00–0.00 MiB; asserted |
| boxed-RawJSON/64KiB | 0/0; 0/0, 0; 0.07–0.07 MiB; asserted | 0/0; 0/0, 0; 0.07–0.07 MiB; asserted |
| boxed-RawJSON/1MiB | 0/0; 0/0, 0; 1.03–1.03 MiB; asserted | 0/0; 0/0, 0; 1.03–1.03 MiB; asserted |
| boxed-RawJSON/6MiB | 0/0; 0/0, 0; 6.22–6.22 MiB; asserted | 0/0; 0/0, 0; 6.22–6.22 MiB; asserted |
| json.RawMessage/1KiB | 1/16; 1/16, 0; 0.00–0.00 MiB; asserted | 1/16; 1/16, 0; 0.00–0.00 MiB; asserted |
| json.RawMessage/64KiB | 1/16; 1/16, 0; 0.07–0.07 MiB; asserted | 1/16; 1/16, 0; 0.07–0.07 MiB; asserted |
| json.RawMessage/1MiB | 1/16; 1/16, 0; 1.03–1.03 MiB; asserted | 1/16; 1/16, 0; 1.03–1.03 MiB; asserted |
| json.RawMessage/6MiB | 1/16; 1/16, 0; 6.22–6.22 MiB; asserted | 1/16; 1/16, 0; 6.22–6.22 MiB; asserted |
| pointer-to-struct/1KiB | 1/16; 1/16, 0; 0.00–0.00 MiB; asserted | 1/16; 1/16, 0; 0.00–0.00 MiB; asserted |
| pointer-to-struct/64KiB | 1/16; 1/16, 0; 0.09–0.09 MiB; asserted | 1/16; 1/16, 0; 0.07–0.07 MiB; asserted |
| pointer-to-struct/1MiB | 1/16; 1/16, 0; 1.11–1.11 MiB; asserted | 1/16; 1/16, 0; 1.06–1.06 MiB; asserted |
| pointer-to-struct/6MiB | 23/30469392; 1/16, 0; 8.86–8.86 MiB; recorded | 1/16; 1/16, 0; 6.44–6.44 MiB; asserted |
| flat-map/1KiB | 2/112; 2/112, 0; 0.00–0.00 MiB; asserted | 2/112; 2/112, 0; 0.00–0.00 MiB; asserted |
| flat-map/64KiB | 2/112; 2/112, 0; 0.07–0.07 MiB; asserted | 2/112; 2/112, 0; 0.07–0.07 MiB; asserted |
| flat-map/1MiB | 2/112; 2/112, 0; 1.48–1.48 MiB; asserted | 2/112; 2/112, 0; 1.06–1.06 MiB; asserted |
| flat-map/6MiB | 2/112; 2/112, 0; 7.51–7.51 MiB; asserted | 2/112; 2/112, 0; 6.44–6.44 MiB; asserted |
| nested-map/1KiB | 9/784; 9/784, 0; 0.00–0.00 MiB; asserted | 9/784; 9/784, 0; 0.00–0.00 MiB; asserted |
| nested-map/64KiB | 471/45136; 471/45136, 0; 0.07–0.09 MiB; asserted | 471/45136; 471/45136, 0; 0.07–0.07 MiB; asserted |
| nested-map/1MiB | 7503/720208; 7503/720208, 0; 1.19–1.53 MiB; asserted | 7503/720208; 7503/720208, 0; 1.06–1.06 MiB; asserted |
| nested-map/6MiB | 45010/4320880; 45010/4320880, 0; 6.28–7.71 MiB; asserted | 45010/4320880; 45010/4320880, 0; 6.44–6.44 MiB; asserted |

AC-P2, the DECODE lines of W5.2-03 and -04 (fixtures that decode; the naive decode is `naive.Sonic`, the minimum of 25 runs):

| Fixture | Bytes | Allocations (M) / (L) | Budget | Naive (M) / (L) | Ratio (M) / (L) |
| --- | ---: | ---: | ---: | ---: | ---: |
| deviation-lone-surrogate | 219 | 13 / 13 | 14 | 24/3048 / 27/2752 | 0.542 / 0.481 |
| duplicates | 825 | 16 / 16 | 17 | 55/9464 / 89/9160 | 0.291 / 0.180 |
| escaped-member-names | 425 | 29 / 29 | 30 | 41/4512 / 51/4664 | 0.707 / 0.569 |
| escaped-names | 481 | 10 / 10 | 10 | 38/5224 / 57/5064 | 0.263 / 0.175 |
| no-answers | 67 | 0 / 0 | 0 | 12/1080 / 9/824 | 0.000 / 0.000 |
| parity-big-exp-unknown | 135 | 1 / 1 | 1 | refused / refused | - / - |
| result | 364 | 4 / 4 | 4 | 26/3960 / 40/3672 | 0.154 / 0.100 |
| result-20 | 2253 | 24 / 24 | 24 | 94/19184 / 214/19632 | 0.255 / 0.112 |
| score-flood-mini | 1495 | 21 / 21 | 21 | 96/16064 / 137/16448 | 0.219 / 0.153 |
| structured-legend | 217 | 11 / 11 | 12 | 24/3088 / 27/2992 | 0.458 / 0.407 |
| structured-legend-flood-10k | 616421 | 685 / 685 | 686 | 20093/8254712 / 65192/9721784 | 0.034 / 0.011 |
| structured-legend-flood-1k | 57418 | 90 / 90 | 91 | 2036/650264 / 6574/1036728 | 0.044 / 0.014 |
| type-last | 364 | 4 / 4 | 4 | 26/3960 / 40/3672 | 0.154 / 0.100 |
| unknown-answer-type | 145 | 1 / 1 | 1 | 18/2232 / 20/1960 | 0.056 / 0.050 |

Every allocation series, by test:

| Test | Series (M) / (L) | Runs per series | Exact pins: runs above the minimum (M) / (L) | Largest spread (M) / (L) |
| --- | ---: | ---: | ---: | --- |
| `TestAllocEncode` | 64 / 64 | 25 | 0 of 1575 / 0 of 1600 | +0/+0 (string/1KiB E_sonic) / +0/+0 (string/1KiB E_sonic) |
| `TestAllocScratchSequence` | 136 / 136 | 25 | 1 of 1650 / 2 of 1650 | +1/+48 (boxed-string call 29) / +1/+80 (flat-map call 29) |
| `TestAllocWholeCall` | 23 / 23 | 25 | 0 of 575 / 2 of 575 | +0/+0 (E_sonic) / +1/+80 (floor round trip) |
| `TestMemStatsCap` | 7 / 7 | 25 | 0 of 0 / 0 of 0 | +4/+288 (i-declared-16MiB-sent-10B) / +4/+288 (i-declared-16MiB-sent-10B) |
| `TestAllocDecodeFixtures` | 41 / 41 | 25 | 0 of 700 / 0 of 700 | +1/+48 (escaped-member-names.json naive) / +0/+0 (deviation-lone-surrogate.json interned) |
| `TestLinearityFlood` | 2 / 2 | 25 | 0 of 50 / 0 of 50 | +0/+0 (structured-legend-flood-1k.json) / +0/+0 (structured-legend-flood-1k.json) |
| `TestAllocResponseJSON` | 8 / 8 | 25 | 0 of 200 / 0 of 200 | +0/+0 (models.json marshal) / +0/+0 (models.json marshal) |
| `TestAllocPrepare` | 11 / 11 | 25 | 0 of 275 / 0 of 275 | +0/+0 (c1-sketch) / +0/+0 (c1-sketch) |
| `TestAllocLoggedCall` | 8 / 8 | 25 | 2 of 200 / 1 of 200 | +1/+48 (call/q3+id/debug-discard) / +1/+48 (call/q3/info-discard) |
| `TestAllocTypedDecode` | 4 / 4 | 25 | 0 of 100 / 0 of 100 | +0/+0 (Answers() decode) / +0/+0 (Answers() decode) |
| `TestLazyPassAllocations` | 6 / 6 | 25 | 0 of 150 / 0 of 150 | +0/+0 (deviation-lone-surrogate.json lazy) / +0/+0 (structured-legend.json lazy) |
| `TestEncodeStateAllocations` | 7 / 7 | 25 | 0 of 175 / 0 of 175 | +0/+0 (success: flat map[string]any) / +0/+0 (success: empty section (control)) |
| `TestNewBodyAllocations` | 2 / 2 | 25 | 0 of 50 / 0 of 50 | +0/+0 (success: a warm pool hit allocates nothing) / +0/+0 (success: a warm pool hit allocates nothing) |

The three-of-five rule (finding 9):

| Host | Exact-pin runs | Above the minimum | p | P(3 of 5 fail) at p | P(3 of 5 fail) at 1/100 (R104-corr) |
| --- | ---: | ---: | ---: | ---: | ---: |
| (M) | 5700 | 3 | 0.00053 | 1.46e-09 | 9.85e-06 |
| (L) | 5725 | 5 | 0.00087 | 6.65e-09 | 9.85e-06 |

## W5.3: optimisation candidates, one at a time

W5.3 tries the charter's candidates in the lead's order, each as one
commit with its before and after on (M) and (L): the seam-test commit
first (K40 as the owner amended it in R116; not a candidate, it changes
no production code), then K36 (the trailing-data check's second scan of
the body), then the typed decode's field-offset store (R116), then the
AC-P6 candidates. A kept candidate's pins and frozen-budgets rows move in
its own commit (R104); a reverted one leaves a finding here and no code.
Raw outputs are in `_spikes/w5.3/results/`. Commands use
`A=_spikes/w5.3/ab.sh` (builds the test binary of the base tree and of
the candidate tree, then runs them alternately, base first, ROUNDS times
in one lock hold, so that a drift of the host reaches both alike),
`C=_spikes/w5.3/cs.sh` (CodSpeed's walltime runner run locally with
`--skip-upload` on the four call rows of both trees, alternately; it
records each run's per-iteration minimum, median and mean, a proxy for
CodSpeed's own amd64 run), `O=_spikes/w5.3/results`,
`SP=/private/tmp/claude-501/-Users-zchee-go-src-github-com-zchee-typesafe-sdk-go/40cb0f1f-c8a9-422c-a3e8-b3afc329b5cb/scratchpad`
and `F=/opt/homebrew/opt/util-linux/bin/flock`. (M) runs set
`GOEXPERIMENT=nosimd,noruntimesecret` and `MAXLOAD=10` (the runner waits
up to five minutes for a load at or under it); (L) runs use
`/tmp/ts-spike/go/bin/go`, section 11's `GOPATH`, `GOMODCACHE` and
`GOCACHE`, the trees copied by `git archive` into
`/tmp/ts-spike/src-w5.3/<commit>`, and `flock /tmp/ts-spike/bench.lock`.
Each A/B series is 5 rounds of `-test.count 2`, 10 runs a side;
benchstat compares medians.

| Candidate | Change | Result on (L) | Result on (M) | Decision | Commit |
| --- | --- | --- | --- | --- | --- |
| K36 | the traversal runs over the body cut before the root's closing brace; decoder.Skip's second scan only when the cut cannot decide | `call/sdk` 6.180 → 5.610 µs (−9.2 %); `Decode/result` 3.428 → 2.779 µs (−18.9 %); allocations unchanged | `call/sdk` 4.846 → 4.365 µs (−9.9 %); `Decode/result` 3.478 → 2.950 µs (−15.2 %); allocations unchanged | keep | 05264fe |
| R116 store | `DecodeAs` writes each answer at the field's offset (`decodeas_store.go`), so the `T` stays on the stack | `DecodeAs` on `result.json` 184.3 → 98.3 ns (−46.6 %); 1 → 0 allocations | `DecodeAs` on `result.json` 136.2 → 82.7 ns (−39.3 %); 1 → 0 allocations | keep (owner ruling R116) | 7df5a6b |
| N1 | the first attempt's copy of the endpoint URL and the `*SystemOneResponse` (the models page for `Models.List`) in one allocation, `systemOneAlloc` | `call/sdk` 5.621 → 5.588 µs (−0.6 %, the host's drift: `call/naive` −0.5 % in the same run); 22 → 21 allocations, bytes unchanged | `call/sdk` ~ (p = 0.579; noisy, load up to 20.83); 22 → 21 allocations | keep: N 14 → 13 | 0422123 |
| N2 | the answer entries of a set of 1 to 4 questions in the call's allocation (`systemOneAllocWith`), given to the decode as spare room (`codec.DecodeSystemOneInto`, `wire.Answers.GrowInto`) | `call/sdk` 5.583 → 5.552 µs (~, p = 0.072; `call/naive` +0.8 % in the same run); 21 → 20 allocations, q3 bytes unchanged | `call/sdk` 4.485 → 4.413 µs (~, p = 0.123; re-taken at load 13.16, the first run noisy); 21 → 20 allocations | keep: N 13 → 12, the Rust port's | c53a291, with 69085ab |
| P1 | a raw question's map keys sorted on one key stack the `Builder` owns (`sortedKeys`), not `slices.Sorted(maps.Keys(m))` per map | `c5-raw-100x3` 138.0 → 85.1 µs (−38.3 %), 1 710 → 14 allocations; `c1-sketch` 1.206 → 1.110 µs, 9 → 6 | `c5` 76.11 → 51.88 µs (−31.8 %); the same counts | keep | 133b642 |
| P2 | a raw score question's criteria judged falsy from their first bytes (`maybeFalsyJSON`), the whole value checked only when it can be falsy | `n8a-array-score` 103.1 → 53.5 µs (−48.1 %), 135 → 15; `n8b-map-score` 429.3 → 219.2 µs (−48.9 %), 618 → 18; `FalsyJSON/array` 2 196 → 6.1 ns | `n8a` −46.6 %, `n8b` −48.6 %; `FalsyJSON/array` 1 230 → 4.0 ns | keep | 0aa6730 |
| P3 | the size hint counts a raw field that is a string, `RawJSON` or `Content` at its length (`rawValueSize`) | `n8a` −16 % and `n8b` −13 %, 15 → 10 and 18 → 13, bytes −56 % and −58 %; `c5` **+8.5 %** (+2 KiB) and `c1` +5.4 % | `n8a` −10 %, `n8b` −11 %; `c5` ~ (p = 0.579) | keep (the (L) cost is finding 3) | 035934a |
| P4 | the size hint counts the JSON score levels and `Builder.GrowLevels` reserves their spans | `c4b-score-20x8-json` 46.93 → 42.71 µs (−9.0 %), 36 → 28 | `c4b` 27.13 → 24.75 µs (−8.8 %) | keep | c8c26af |
| P5 | the choices' and scores' tables cut from one array per kind (`prepareTables`, `cut`) | `c3-choice-20x10` −1.9 %, 27 → 8; `c4a-score-20x8-text` −4.4 %, 27 → 8; `c4b` −1.6 %, 28 → 9 | `c3` −3.3 %, `c1` −2.5 %, `c4a` and `c4b` ~ | keep | e178067 |
| isSecretHeader | an ASCII header name compared with its letters folded in place; any other name lower-cased as before | counts as (M) | `isSecretHeader` 1 → 0 allocations per mixed-case name; the redacted copy of a 4-header response 7 → 3; a credential-free request header's scan 4 → 0 | keep | 9972097 |
| NIT F | a typed failure's FieldPath rendered once (`newResponseValidationErrorAt`) | counts as (M) | a typed failure at `tone.choice` 9 → 5 allocations | keep | f6f91b9 |
| R97-corr (c) | the retry policy's statuses and predicate behind one pointer: `RetryPolicy` 80 → 56 B, `callOptions` 145 → 121 B | counts as (M) | every option-bearing call 32 B less at the same count (`Retry` +1/160 → +1/128); a policy built with `Statuses` or `Predicate` one allocation more (accepted) | keep | 1487d03 |
| R54 | the encode's UTF-8 check through sonic's SIMD validator (after an ASCII scan in Go, then on the whole input) | as built: CJK check −89 %, ASCII check +188 to +262 %; the whole-input variant fastest on both texts | the whole-input variant 15 to 22 times slower than `utf8.Valid` on ASCII, 2.2 times faster on CJK | **revert** (the lead's rule: the (M) probe disagrees); no code | – |

### K36: one scan of the body (05264fe)

The trailing-data check ran sonic's `decoder.Skip` over the whole body
after `ast.Preorder`, only to find where the root value ends: CodSpeed's
flamegraph put it at about 0.71 µs of each `call/sdk` iteration, the size
of the gap by which the naive client's per-iteration minimum beats the
SDK's (K36, `.omc/handoffs/w5.3-codspeed-note.md`). The traversal now
runs over the body cut just before its last byte other than JSON
whitespace, when that byte is `}`: a root that closes there makes sonic
stop at the cut with its end-of-input error while the visitor stands in
the root object between two members, and the visitor is handed the
root's end. Whatever else happens, the traversal of the whole body and
`decoder.Skip` decide as before, so every refusal keeps its text and its
path.

K36 findings:

1. **The per-call time falls by about a tenth on both hosts, the decode
   by a sixth, and no allocation count moves.** `call/sdk` against
   `call/naive` (medians, W5.3-03 and W5.3-05): (L) 0.902 → 0.817 for q3
   and 0.984 → 0.850 for q20; (M) 1.325 → 1.158 and 1.847 → 1.594. The
   local CodSpeed proxy on (M) (W5.3-07, three runs a side): `call/sdk`'s
   per-iteration minimum 4041–4083 → 3583–3625 ns, median 4541–4583 →
   4000–4083 ns, mean 4969–5137 → 4393–4496 ns; `call/naive` unchanged
   (minimum 2458–2500 ns both). `B/op` of `call/sdk` falls by about 70 B
   (2.93 → 2.86 KiB on (L), W5.3-01): `decoder.Skip` takes a 32 776-byte
   state machine from a `sync.Pool` that the collector empties, so every
   few hundred calls paid a 40 KiB allocation, too rare to show in
   allocs/op.
2. **The decode rows (W5.3-04, W5.3-06).** (L): `result` 3.428 → 2.779 µs (−18.9 %), `escaped-names` 5.004 → 4.234 µs
   (−15.4 %), `escaped-member-names` 7.254 → 7.039 µs (−3.0 %), `result-20`
   21.49 → 17.75 µs (−17.4 %), `structured-legend-flood-1k` 615.8 → 543.0 µs
   (−11.8 %). (M):
   `result` 3.478 → 2.950 µs (−15.2 %), `escaped-names` 5.090 → 4.399 µs
   (−13.6 %), `escaped-member-names` 6.841 → 6.593 µs (−3.6 %, p = 0.052,
   not significant), `result-20` 21.57 → 18.55 µs (−14.0 %),
   `structured-legend-flood-1k` 679.8 → 613.5 µs (−9.8 %). Against
   `naive.Sonic`'s decode in the same runs, (M): `result` 2.35 → 1.86 ×,
   `result-20` 2.74 → 2.39 ×, flood-1k 3.51 → 2.98 ×; K23's arm64 targets
   (`result` ≤ 1.5 × sonic-map, q3 `call/sdk` ≤ `call/naive`) are closer
   and not met. (L): `result` 1.235 → 1.000 ×, `result-20` 1.293 → 1.072 ×, flood-1k
   1.030 → 0.910 ×, `escaped-names` 1.237 → 1.051 ×.
3. **Which bodies take the one scan** (`k36-paths.txt`, W5.3-12): every
   fixture that decodes, the four with escapes included (`duplicates`,
   `escaped-names`, `escaped-member-names`, `deviation-lone-surrogate`),
   and `models.json`: 21 of the 41 fixtures, all 14 benchmark bodies
   among them. The other 20, malformed or deviation bodies that fail,
   take the whole-body path: 7 because `cutPoint` refuses them (they do
   not end in `}`, or end with no value before it), 13 because the cut
   traversal declines (it fails, or the root closes before the cut).
   A failing body can cost two traversals; a body that decodes, one.
4. **FuzzDecodeResponse's new differential, the one-scan decode against
   the whole-body one, found three ways sonic stops at the cut with the
   root's error in the state the check reads as the root's end** (W5.3-13
   to -16), each now refused by `cutPoint` and a committed seed:
   (a) at 753f723, on both hosts, `{"\u"}`: sonic fails to unquote a root
   key and returns the end-of-input error before the key reaches the
   visitor; (b) at 5ee759a, (L), `{"0000000000000000000000000000000\0}`:
   the same, a key that runs into the cut ending in a backslash; (c) at
   5bb86f9, (L), after 294 s, `{"":"000…0}` (a 64-byte string): sonic's
   native string scanner returns a string cut open by the end of its
   input as complete when the string's content is a multiple of 32 bytes
   long (probed: 32, 64, …, 288 bytes before the cut all do, every other
   length fails as it should), so the string's value reaches the visitor
   and the cut looks like the root's end. (c) is a sonic bug at a 32-byte
   block boundary, an upstream report for the owner (W7). `cutPoint` now
   cuts a body only when the byte before the cut ends a container or a
   string, or is the root's opening brace (no number or literal is
   scanned into the cut), when its quotes that no backslash escapes are
   even (the cut is outside every string), and when every `\u` in it has
   four hex digits after it (of unquote's failures only a short `\u` is
   the end-of-input error once every string ends at a real quote; sonic's
   `native/unquote.c`). The first version refused every body with a
   backslash; this narrower rule, the lead's one attempt, lets the
   escaped fixtures take the one scan with no finding in 600 s of fuzzing
   on (L) (W5.3-09). The raw log of (c)'s run was deleted with the
   5bb86f9 tree on (L) at 17:36 JST; the failing input is kept as a seed
   and as test cases.
5. **Depth.** `decoder.Skip` keeps one slot per open container and one
   more for a member or element it has yet to read, 4096 slots in all
   (sonic's `native/scanning.h`, `fsm_push`), so a container nested 4096
   deep, which the visitor's cap takes, fails Skip once it holds a
   member or a second element (`TestDecodeDepthBound`'s "refused by
   Skip" case). A body nested deeper than `skipDepth` = 4095 takes the
   whole-body path, which keeps Skip's verdict.
6. **Tests.** `TestOneScanMatchesWholeScan` compares both paths on 11 030
   bodies: every fixture as a System One and as a models body, every
   prefix of seven fixtures with and without a closing brace appended,
   tails, escapes around the cut, the cut-open strings of finding (c),
   repeated members and the depth boundary; `TestOneScanTakesValidBodies`
   pins which path a body takes. Eight mutants of the guards each fail
   them (W5.3-11). `TestDecodeFixtures` and root's
   `TestMalformedFixturesRefused`, the module's form of K23's S-D1
   validity gate, pass on both hosts (W5.3-09).

### The seam test (a2a29b4, K40 and R116)

`TestSeamRootRawPointers` lets exactly one non-test file of the root
package import `unsafe`, `decodeas_store.go` (the store's commit changed
it from "none or that one"), and fails on reflect's `UnsafePointer`,
`UnsafeAddr` and `NewAt`, unsafe's `SliceData` and `StringData`, any
selector on `unsafe`, and a `Pointer()` result converted to a pointer, in
every other non-test file of the root package and of the module packages
it imports (31 files of 3 packages; `internal/codec` and
`internal/testsupport/naive` exempt). Its detector has a table test;
three mutants fail it (W5.3-17): `UnsafePointer()` in `decodeas.go`, the
same in a new `internal/wire` file, and `unsafe` imported by
`response.go`.

### The typed store (7df5a6b, R116)

`DecodeAs` stored each answer through `v.Field(i).Addr().Interface()`,
which moved the decoded `T` to the heap, AC-P3's one allocation (144 B on
`result.json`). The owner's ruling R116 allows `unsafe` field offsets in
the typed decode, one implementation for both architectures; W4.3's S-D2
variant (2) is the reference. `buildPlan` records each answer field's
`reflect.StructField.Offset` once per type, and `decodeas_store.go` writes
each answer by a typed assignment at that offset, `*(*F)(unsafe.Add(base,
off)) = v` with `F` one of the three answer types, so the compiler emits
the write barriers of `F`'s pointer fields. An answer field is always one
of the struct's own fields: `planField` refuses a tag in an embedded or
nested struct (R96), so no offset is a sum, and an embedded field of an
answer type is the struct's own field at its own offset (pinned by
`TestStoreFieldKinds`). `decodeTyped` panics on a plan of another type.

Typed store findings:

1. **`DecodeAs` allocates nothing and runs about 40 % faster on
   `result.json`**, now within 1.3 × of S-D2's offset replica (W5.3-18,
   W5.3-21). (M): `result.json` 136.2 → 82.7 ns (replica 64.8 ns),
   `result-20` 942.7 → 630.9 ns (−33.1 %; replica 555.6 ns),
   `structured-legend-flood-1k` 724.5 → 665.6 ns (−8.1 %; replica 658.5
   ns). (L): `result.json` 184.3 → 98.3 ns (−46.6 %; replica 73.3 ns), `result-20`
   1198 → 714.8 ns (−40.3 %; replica 620.4 ns), and
   `structured-legend-flood-1k` 992.9 → 1056.5 ns, **+6.4 %** (replica
   990.1 ns). The flood's decode is the level check over its 1 000 levels,
   a loop W4.3 found sensitive to where it lands on (L) (W4.3 finding 5:
   the same instructions ran 42 % slower in `DecodeAs` than in the
   replica); the store does not change that loop, and its commit moves
   the code around it. A finding, not a reason to revert: (M) gains 8.1 %
   on the same fixture.
2. **AC-P3 and `Ask`** (W5.3-20, W5.3-23): `DecodeAs` 1/144 → 0/0 on both
   hosts against the `Answers()` decode's 4/688, and `Ask` 23/2 792 →
   22/2 648, exactly `SystemOne`'s count; the pin in `TestAllocTypedDecode`
   and a new AC-P3 row of frozen-budgets.md moved in the same commit. So
   AC-P3's margin is now 0 against 4. `call/sdk` does not move (W5.3-19,
   W5.3-22).
3. **Invariants and their tests.** The store's file states five
   invariants. `TestStoreFieldKinds` prints the layout table of a struct
   that interleaves the three answer types, and an embedded one, with a
   field of every other kind (string, bool, every integer, float and
   complex width, slice, map, pointer, array, interface, channel, empty
   struct), and checks that each answer field's offset is reflect's, is
   aligned, and overlaps no other field; `TestStoreKeepsNeighbours` fills
   every other field with a value and checks it survives the store;
   `TestStoreWritesTyped` checks the file's shape (only `unsafe.Pointer`
   and `unsafe.Add`, one typed store through `*F`, no copy, no directive);
   `TestDecodeTypedPlanMismatch` pins the panic. Six mutants fail them
   (W5.3-25): the offset off by one byte, the next field's offset and a
   wider write each corrupt memory (the last two end the test binary with
   a fatal error inside the AC-F12 differential, and fail
   `TestStoreKeepsNeighbours` when it runs alone), the answer copied as
   bytes fails `TestStoreWritesTyped`, the store moved to another file
   and `UnsafePointer()` in `decodeas.go` fail the seam tests. `go vet` is
   clean, `-race` with checkptr passes, and `-gcflags=-m` shows `b does
   not escape` in `decode` and no `moved to heap: t` in any `decodeTyped`
   instantiation (W5.3-26). AC-F12's differential and the R99-rev path
   tests are unchanged and pass.
4. **The sync.Pool route** (a pool of `*T` per type, the lead's refinement
   before R116) was considered and not built: R116 made it unnecessary.

### N1: the first URL copy in the call's allocation (0422123)

W3.4's N = 14 counted two allocations that live exactly as long as each
other on a call whose first attempt succeeds: the first attempt's copy of
the endpoint URL (144 B; every request carries its own `*url.URL`, R66
NIT 5) and the `*SystemOneResponse` (112 B). `SystemOne` now allocates
both in one `systemOneAlloc` and returns a pointer to the response inside
it, and `Models.List` does the same with `modelsAlloc`; a retry still
copies the URL into a fresh allocation, so no attempt rewrites an earlier
request's URL, which a transport may still be reading. The commit was
measured as 6ad7cf8 and amended to 0422123 before it was pushed, to
correct AC-P3's `Ask` figure in frozen-budgets.md (22/2 648 → 21/2 648);
the code is the same.

N1 findings:

1. **AC-P6: N 14 → 13 at the same bytes on both hosts** (W5.3-28,
   W5.3-29): SDK-own 14/2 008 → 13/2 008, the call 22/2 648 → 21/2 648
   (144 + 112 = 256, one size class, so no byte is added), q20 34 → 33
   (recorded). Five runs of `TestAllocWholeCall` a side agree on each
   host. `TestAllocWholeCall`'s pin, `TestAllocLoggedCall`'s 21/2 648,
   the frozen AC-P6 row, AC-P3's `Ask` figure and ci.yaml's comment moved
   in the same commit (R104); ci.yaml's allocation-budget step and
   `-race` pass on (L).
2. **No time moves.** (L): `call/sdk` 5.621 → 5.588 µs (−0.6 %, p =
   0.011) with `call/naive` −0.5 % (p = 0.035) in the same interleaved
   run, so the difference is the host's, not the candidate's (W5.3-27).
   (M): `call/sdk` 4.637 → 4.644 µs (~, p = 0.579), a noisy run: the load
   rose from 8.74 to 20.83 during it, above the 16 cores, and the spread
   of one side reached ± 693 % (W5.3-30). N1 is kept for the count, which
   is AC-P6's frozen clause; its time is within noise.
3. **The per-attempt copy is pinned** (W5.3-32): `TestRetryURLIsCopied`
   (new, `synctest`, `MaxRetries(1)`, a 503 then a 200) checks that each
   request of one call has its own `*url.URL`, for `SystemOne` and for
   `Models.List`. Two mutants fail: the retry reusing the call's URL
   storage fails it, and no copy at all (the request pointing at the
   client's URL) fails it and `TestRequestURLIsCopied`.

### N2: the answer entries in the call's allocation (c53a291, 69085ab)

The decode allocated the array of answer entries (`wire.Answers.Grow`,
448 B for three answers) apart from the response that holds it. For a set
of 1 to 4 questions (`maxInlineAnswers`), `SystemOne` now allocates an
array of that many entries in the call's allocation
(`systemOneAllocWith[[n]wire.AnswerEntry]`, one type per size, so the
array is exactly n entries) and gives it to the decode as spare room
(`codec.DecodeSystemOneInto`, `wire.Answers.GrowInto`). A larger set, or a
response with more answers than the questions asked, gets its entries from
the decode as before. The lead's conditions arrived after c53a291 was
pushed; 69085ab answers them, and fixes the one difference they turned up.

N2 findings:

1. **AC-P6: N 13 → 12 at the same bytes on both hosts** (W5.3-34,
   W5.3-35): SDK-own 13/2 008 → 12/2 008, the call 21/2 648 → 20/2 648 in
   5 of 5 runs a side; the call's allocation 256 → 704 B and its decode
   4/688 → 3/240 (688 B of response, URL copy and three entries fill the
   704 B size class, so no byte is added); q20 unchanged at 33 (20
   questions are past the bound). A set of 1, 2 or 4 questions costs 16,
   32 or 64 B more by size-class rounding (400, 544 and 832 B blocks in the
   416, 576 and 896 B classes); no frozen shape has one. The pins moved in
   c53a291 with the frozen AC-P6 row (R104): `TestAllocWholeCall` own ==
   12, `TestAllocLoggedCall` 20/2 648, ci.yaml's comment, AC-P3's `Ask`
   figure.
2. **The standalone decode is unchanged** (the lead's condition 1):
   `SystemOneResponse.UnmarshalJSON` (the JSON round trip) and AC-P2's
   fixture decode run `codec.DecodeSystemOne` without a spare, and their
   entries stay the decode's own allocation: `result.json` 4/688, AC-P2's
   pin unmoved. 69085ab adds to the AC-P2 row which path its pin measures,
   and that a call's decode of 1 to 4 questions is 3/240 inside AC-P6's
   composition. AC-P3 re-stated: `DecodeAs` 0/0 against the `Answers()`
   decode's 4/688 (that test decodes without a spare), `Ask` 20/2 648 =
   `SystemOne` 20/2 648 + 0.
3. **Lifetime and aliasing** (condition 2): the entries share one block
   with the response and the first URL copy, so an `Answers` taken from a
   response keeps the whole block (704 B for three questions) reachable:
   the response and the decode's array it kept before, and the 144 B URL
   copy, which a held response keeps alive since N1 and which was freed
   with its request before it (review V63, NIT 8; corrected in a09ea43);
   an answer value copied out holds no pointer into it. `newSystemOneAlloc`
   says so; `TestAnswersOutliveTheirResponse` keeps an `Answers` past its
   response through collections and reuse of freed memory, for 3 and 5
   questions; `TestDecodeDoesNotAliasBody` now also decodes into a spare.
   That test found the one difference: a response with no answers left the
   call path's set as an empty slice of the spare where the standalone
   decode leaves nil (`no-answers.json`); `GrowInto` now takes a spare
   only for at least one entry (69085ab).
4. **The bound** (condition 3): `TestAllocAnswersInlineBound` (new,
   ci.yaml's root list, K38) pins a call of 4 questions at the count of 3
   and one of 5 at one more. With all-`noul` sets answered by
   `result.json`: 3 questions 21/2 696, 4 questions 21/2 888, 5 questions
   22/2 696 (these sets make one more allocation than q3's mixed shape,
   the same on both sides of the bound).
5. **Bytes and parity** (conditions 4 and 5): the call's total stays
   2 648 B and SDK-own 2 008 B on every run on both hosts; `TestMemStatsCap`
   (AC-P5) passes in the gates and in ci.yaml's allocation step on (L);
   AC-P1's encode is untouched. The wire bytes and the byte-parity tests
   (`TestPreparedBytesMatchPython` among them) pass unchanged; `go doc -all
   .` is unchanged (W5.3-42).
6. **Mutants** (condition 6; W5.3-37, W5.3-40): eight fail c53a291's
   tests, among them a shared array for three questions
   (`TestSystemOneAnswersInline`, `TestAllocLoggedCall`) and the decode
   ignoring the spare (`TestAllocWholeCall`, `TestAllocLoggedCall`); two
   fail 69085ab's: a spare taken for no entries (`TestAnswersGrowInto`,
   `TestDecodeDoesNotAliasBody`) and no inline array for four questions
   (`TestAllocAnswersInlineBound`).
7. **Time: within noise.** (L) `call/sdk` 5.583 → 5.552 µs (~, p =
   0.072) with `call/naive` +0.8 % in the same run (W5.3-33). The first
   (M) run fell in the lead's second window (10:23–10:30Z, external busy
   loops; load 199 → 234), with my own P2 mutant runs overlapping its last
   rounds: `noisy`, not used (W5.3-38). The re-take started at load 13.16,
   under 16: `call/sdk` 4.485 → 4.413 µs (~, p = 0.123) (W5.3-39).

### Slice 1's review pins and K41 (9f23a43)

Slice 1's review (V60) found two of K36's guards without a test that
fails when the guard is gone (MINOR 1 and 2): mutant K10, any error of the
cut traversal taken as the cut, accepts `{"model":"m","usage":{},"x":"y"]}`
(the whole-body path: invalid character), and mutant K9, a `\u` of three
hex digits taken as complete, accepts `{"model":"m","usage":{},"\u123"}`
(the whole-body path: eof). Both bodies joined the guard table of
`TestOneScanMatchesWholeScan` and `FuzzDecodeResponse`'s testdata corpus
(`internal/codec/testdata/fuzz/FuzzDecodeResponse/`), which leaves the fuzz
target's source as W6.1 knows it; each mutant now fails both (W5.3-40).

K41 (fuzz finding 4 (c)): `_spikes/w5.3/k41` reproduces the scanner bug
with sonic alone, for the upstream issue, and replaces the (L) log lost at
W5.3-16. Run on (M) (W5.3-45), darwin/arm64, sonic v1.15.4:

1. **The bug is not amd64's.** A string that runs to the end of the input
   without its closing quote, content n zeros: for n = 32, 64, 96 and 128,
   `ast.Preorder` reports a string of n − 1 bytes, the last byte dropped,
   and no error when the string is the whole input; inside an object it
   reports the string and then the object's end-of-input error. For n = 0,
   1, 31, 33, 63 and 65 it reports an error. `decoder.Skip` accepts the
   top-level string at n = 32·k ((0, n + 1)), and for `{"":"` + 32 zeros
   (37 bytes) returns an end of 41, past its input. `sonic.UnmarshalString`
   refuses every case. The reviewer found the same at every 32·k from 32
   to 288 (K41-corr).
2. **The SDK refuses every such body.** `codec.DecodeSystemOne` refuses
   the finding's body and its neighbours with the whole-body decode's own
   error: `eof` for each open string in an object, and "the response is not
   a JSON object" for a top-level one, whose string the buggy scanner
   passes and the visitor refuses. `TestK41ScannerBoundary` pins that each
   such body reaches the whole-body path (the one scan does not decide) and
   gets that path's error: `cutPoint` refuses the cut when the byte before
   it is not `{ } ] "`, and counts unescaped quotes when it is.
3. **No production path hands sonic an open string at an end of its own
   making.** The one scan cuts the body only where `cutPoint` has shown no
   string is open; the whole-body traversal and `decoder.Skip` (the
   trailing check after it) read the whole body, where an open string at
   the end is the body's own truncation and its container is left open, so
   the traversal fails at the end either way; the lazy pass
   (`sonic.GetFromString`) reads only a body the traversal accepted; and
   `ReadErrorBody` checks an error body with `wire.AppendJSON`, the SDK's
   own scanner, before sonic sees it.

### Prepare P1 to P5 (133b642, 0aa6730, 035934a, c8c26af, e178067)

W1.3's proposals 1 to 5 (ruling R51), one commit each; proposal 6 (hand
`Prepare`'s `seen` map to wire as the index, 3 allocations on sets over 32
questions) was not built, and proposal 7 proposes no change. Each commit's
`TestAllocPrepare` pins move with it: `c1-sketch`, `c2-noul-short` and
`c3-choice-20x10` were pinned since W1.3; W5.3 pins `c4a`, `c4b`, `c5` and
the two NIT 8 score sets too, so each proposal's case is pinned by the
commit that lowers it. No frozen-budgets row covers `Prepare` (no Prepare
clause is gated, R51). The (L) A/Bs run the whole `BenchmarkPrepare`
(and `BenchmarkFalsyJSON`), each against its parent; the (M) A/Bs run the
sub-benchmarks the candidate changes.

`TestAllocPrepare` on (L), each count in 5 of 5 runs (W5.3-47, W5.3-70;
bytes on the same runs), and W1.3's base, which c53a291 reproduces:

| Case | base | P1 133b642 | P2 0aa6730 | P3 035934a | P4 c8c26af | P5 e178067 |
| --- | --- | --- | --- | --- | --- | --- |
| `c1-sketch` | 9/1 016 | 6/944 | 6/944 | 6/944 | 6/944 | 6/944 |
| `c2-noul-short` | 3/224 | 3/224 | 3/224 | 3/224 | 3/224 | 3/224 |
| `c3-choice-20x10` | 27/15 256 | 27/15 256 | 27/15 256 | 27/15 256 | 27/15 256 | 8/15 512 |
| `c4a-score-20x8-text` | 27/17 176 | 27/17 176 | 27/17 176 | 27/17 176 | 27/17 176 | 8/17 304 |
| `c4b-score-20x8-json` | 36/44 408 | 36/44 408 | 36/44 408 | 36/44 408 | 28/33 432 | 9/33 560 |
| `c5-raw-100x3` | 1 710/78 080 | 14/31 920 | 14/31 920 | 14/33 968 | 14/33 968 | 14/33 968 |
| `c6-escapes` | 12/8 536 | 9/8 464 | 9/8 464 | 9/8 464 | 9/8 464 | 9/8 464 |
| `n8a-array-control` | 132/58 840 | 15/55 432 | 15/55 432 | 10/24 456 | 10/24 456 | 10/24 456 |
| `n8a-array-score` | 252/110 040 | 135/106 632 | 15/55 432 | 10/24 456 | 10/24 456 | 10/24 456 |
| `n8b-map-control` | 615/271 744 | 18/254 256 | 18/254 256 | 13/105 520 | 13/105 520 | 13/105 520 |
| `n8b-map-score` | 1 215/514 944 | 618/497 456 | 18/254 256 | 13/105 520 | 13/105 520 | 13/105 520 |

Prepare findings:

1. **P1 removes 1 700 of `c5-raw-100x3`'s 1 710 allocations** and 38 % of
   its time on (L) (32 % on (M)): the key stack grows to the deepest
   nesting's keys (8 in `c5`) in 4 allocations for the whole set. The
   members' order is unchanged; `TestBuilderRawKeyStack` checks it against
   a reference writer when a nested map outgrows the stack mid-range, the
   stack's emptiness after each question and map, and a repeated shape at 0
   allocations. `c3-choice-20x10` +1.6 % on (L), a path P1 does not run,
   is the layout of the rebuilt binary.
2. **P2 halves the NIT 8 score sets** and makes `falsyJSON`'s success path
   free (0 allocations, a few ns); a candidate is still checked whole, so
   invalid JSON is not falsy, as before. `FuzzFalsyJSON` holds it to the
   whole-value check it replaced: 12 978 921 executions in 300 s at
   `-parallel 8` on (L), 219 new corpus entries, no failure (W5.3-54).
3. **P3 trades time on (L) for bytes.** The two NIT 8 pairs lose 5
   allocations and 56 to 58 % of their bytes on both hosts and 8 to 16 %
   of their time; on (L) `c5` is 8.5 % slower
   and `c1` 5.4 %, and the cost stays through P4 and P5's builds (`c5`
   about 91.7 µs at each), so it is P3's: the hint now ranges over every
   raw question's fields, about 70 ns per raw question on (L). `c5`'s
   buffer is 2 KiB larger (the hint counts its strings). Kept: `Prepare`
   runs once per set, no Prepare clause is gated, and the bytes a set keeps
   fall on the sets with long values.
On (M) the re-take (W5.3-57b) finds `c5` unchanged (p = 0.579) and the
   NIT 8 pairs 8 to 11 % faster; the first (M) run's −4.3 % on `c5`
   (W5.3-57) ended two seconds into the lead's third window.
4. **P4 and P5 remove 8 and 19 to 20 allocations** from the score and
   choice sets. P5's one array per kind costs 0.4 to 1.7 % more bytes (one
   larger block rounds up where twenty small ones did); `c2-noul-short`,
   which has no table, is 4.1 % slower on (L) (7.8 ns: the two zero-capacity
   arenas and the larger hint). `TestPrepareTablesOwnArrays` checks each
   table's values, its cap at its own length and its isolation from its
   neighbours and from the questions (R45); `TestCutTables` the arena.
5. **Mutants**: P1 7, P2 9, P3 4, P4 3, P5 5, each failing the commit's
   tests (W5.3-49, -53, -58, -63, -68). Every commit's gates are green
   (W5.3-50, -55, -59, -64, -69), and on (L) the wire and root tests pass at
   each commit and ci.yaml's allocation step at e178067 (W5.3-70).
6. **(M) rows and the lead's windows.** P1 and P2 ran clear of the lead's
   third window (11:02:46–11:06:50Z, 20:02:46–20:06:50 JST); P3's first run
   ended at 20:02:48, two seconds into it, and its re-takes are W5.3-57a and
   -57b; P4's first run (20:02:49) and P5's (20:08:42, started at load
   29.71) are `noisy` and were re-taken at loads 7.03 and 6.26.

### Redaction by name, NIT F and R97-corr (c) (9972097, f6f91b9, 1487d03)

Three allocation candidates off the call's frozen shape, each measured by
its own pin and by the probes of W5.3-71 (before and after, on (M); counts,
R17):

1. **isSecretHeader** (D-r103revert-rereview): `strings.ToLower` copied
   every mixed-case name, and every name net/http canonicalises is one. An
   ASCII name, as every name on the wire is, is now compared with its
   letters folded in place (`strings.EqualFold` against the six names, a
   folding substring test for "token" and "secret"); a name with any other
   byte is lower-cased as before, so the rule is `strings.ToLower`'s
   (`FuzzIsSecretHeader` holds it to the old one, with runes that
   lower-case to ASCII letters among the seeds). `isSecretHeader` 1 → 0
   allocations per mixed-case name, the redacted header copy of a
   four-header response 7 → 3, a credential-free request header's scan
   4 → 0 (`TestAllocSecretHeaderName`, ci.yaml's root list, K38). The
   DEBUG row of `TestAllocLoggedCall` does not move: its handler discards a
   record without resolving its values, so the saving is on the error
   paths, whose AC-P5 bounds hold. The redaction pins (R87) are unchanged;
   four mutants fail the tests (W5.3-72).
2. **NIT F** (V52, optional): a typed failure's error rendered the decoder's
   path and then replaced it; it now renders the lifted path once. A
   typed failure at `tone.choice` 9 → 5 allocations
   (`TestAllocTypedFailure`, ci.yaml's list); two mutants fail it
   (W5.3-72).
3. **R97-corr (c)**: `callOptions` holds the call's policy by value, and
   the 80 B policy made it 145 B, the 160 B class, which every call passing
   any option allocates. The statuses and the predicate now sit behind one
   pointer: the policy is 56 B and `callOptions` 121 B, the 128 B class.
   Every option-bearing call costs 32 B less at the same count (`Retry`
   +1/160 → +1/128, `Timeout` +2/176 → +2/144, `Model` +3/192 → +3/160,
   `ExtraBody` +2/192 → +2/160, `Header` +7/720 → +7/688, all five
   +11/784 → +11/752); the frozen AC-P6 row's recorded option costs moved
   with it. **Accepted cost**: building a policy with `Statuses` or
   `Predicate` costs one allocation more (1 → 2 and 0 → 1), paid where the
   policy is built; option (b), the policy by pointer, would add an
   allocation to every `Retry` call instead. `TestRetryPolicyRules` pins
   the sizes and the setters' isolation; four mutants fail it (W5.3-73).
   The exported API is unchanged: `go doc -all .` is byte-identical to
   67dcbb0's after 1487d03 (W5.3-93).

### R54: the UTF-8 validator (reverted, no code)

R54's target: the UTF-8 check of ruling R48 (`utf8.Valid` over the encoded
state) took 7.6 times sonic's own encode on a 6 MiB CJK state; B1 measured
it at 51.2 of 54.2 µs on `cjk/64KiB` on amd64, because CJK's three-byte
runes take `utf8.Valid`'s slow path. The ruling's candidate, an ASCII scan
in Go and sonic's SIMD `utf8.Validate` on the rest, was built (6bb69bf,
never pushed) with an exhaustive differential test against `utf8.Valid`
(every string of up to 3 bytes, every 4-byte lead byte, each rune class at
every offset around sonic's 32-byte blocks) and `FuzzValidUTF8` (13.4 M
executions in 300 s at `-parallel 8` on (L), no failure; `-race ./...`
passes; W5.3-82).

R54 findings:

1. **As built, it traded ASCII for CJK** (W5.3-78, (L)): the CJK check
   4.92 ms → 0.52 ms at 6 MiB (−89 %) and the CJK encode −82 %, but the
   ASCII check +188 to +262 % (36.9 → 106 ns at 1 KiB, 1.91 → 6.90 µs at
   64 KiB): the Go word loop reads ASCII at about 9 GiB/s where
   `utf8.Valid`'s reads it at 25 to 34 GiB/s.
2. **A probe of six variants** (W5.3-79 (L), W5.3-80 (M); the probe's
   source is `_spikes/w5.3/probes/r54-variants_test.go.txt`) found, on
   (L), sonic's `utf8.Validate` on the whole input the fastest on both
   texts (ASCII 53, 102 and 35 GB/s at 1 KiB, 64 KiB and 6 MiB against
   `utf8.Valid`'s 26, 34 and 27; CJK 11 to 12 GB/s against 1.25), but on
   (M) sonic's arm64 validator reads ASCII at 3.1 to 3.6 GB/s against
   `utf8.Valid`'s 54 to 79 GB/s, 15 to 22 times slower, and CJK at 4.5 to
   5.4 GB/s against 2.3 to 2.4. Every variant with a Go ASCII scan first
   loses to `utf8.Valid` on ASCII on both hosts (37 to 39 GB/s against 54
   to 79 on (M) at best).
3. **Reverted by the lead's rule** (the (M) probe disagrees): no R54 code
   lands; the attempt is kept as `_spikes/w5.3/probes/r54-reverted.patch.txt`.
   A per-architecture choice (sonic's validator on amd64, `utf8.Valid` on
   arm64) would win on amd64 and change nothing on arm64; it is an open
   question for the owner, not built. The byte-parity tests are untouched.

### K23, K24 and W5.2's margins (records)

1. **K23, arm64's decode against the naive sonic decode** (K36's own A/Bs,
   W5.3-04 on (L) and W5.3-06 on (M), the SDK's `BenchmarkDecode` over
   `BenchmarkDecodeNaiveSonic`, before → after K36): (M) `result.json`
   2.35 → 1.86, `result-20` 2.74 → 2.39, `structured-legend-flood-1k`
   3.51 → 2.98; (L) 1.23 → 1.00, 1.29 → 1.07, 1.03 → 0.91. K36 narrowed the
   arm64 gap by about a fifth; the rest is sonic's arm64 path, which has
   no JIT: the naive decode's `sonic.Unmarshal` into `map[string]any` runs
   sonic's arm64 native scanner, the SDK's `ast.Preorder` the visitor in
   Go. Options (b) and (c) stay disqualified; no K23 candidate was built.
   Later W5.3 commits leave the standalone decode as K36 left it (N2 gives
   a spare only inside a call). Two later (M) runs were noisy (W5.3-86,
   -87).
2. **K24, the arm64 pool ceiling and pre-growth**: not attempted. The
   growth rows (`TestAllocScratchSequence`, recorded, not asserted) come
   from sonic's encoder growing its own buffer, whose final size the SDK
   cannot know before encoding a struct or a map; they move with the
   pool's drops: `*struct` g₆/g₉ 22/23 on (M) (the frozen row: 21/22) and
   30/3 on (L) (30/32), flat map 21/3 (M) and 31/4 (L), with the pool
   dropping the scratch after calls 11 and 22 on (M) and after call 22 on
   (L) (W5.3-89, W5.3-90). The asserted rows (boxed string, RawJSON) are
   unchanged. A W7 note, not a W5.3 candidate.
3. **W5.2's margins** (charter deliverable 6): AC-P2's "≤ 0.5 × naive" on
   `result.json` is 0.100 on (L) and 0.154 on (M) (a margin of 80 % and
   69 %); AC-P8's allocation ratio is 7.61 against a bound of 12 (37 %),
   identical on both hosts, and its time ratio 9.15 on (L) and 9.40 to 9.47
   on (M) against 15 (37 to 39 %) (W5.3-89, W5.3-91). None is under 10 %,
   so nothing is improved; they are recorded. AC-P8's linearity pin holds
   on both hosts.

### The lead's (M) windows

Every (M) timing row of W5.3 was checked against the lead's three windows
(10:01:13–10:02:30Z, 10:23–10:30Z and 11:02:46–11:06:50Z, that is
19:01:13–19:02:30, 19:23–19:30 and 20:02:46–20:06:50 JST) and against its
own recorded load. In the first, W5.3-29 (counts) is re-taken as W5.3-41;
in the second, N2's first `BenchmarkCall` (W5.3-38) is `noisy` and
re-taken as W5.3-39; in the third, P3's first (M) run ends two seconds in
(W5.3-57, re-taken as W5.3-57b) and P4's runs in it (W5.3-61, re-taken as
W5.3-62). Outside the windows, rows are `noisy` by their own load (above 16
at the start or during the run, from external work) and re-taken or left
as records: W5.3-57a, -66 (re-taken as -67), -81 (R54, reverted), -86 and
-87 (K23; W5.3-06 stands), and W5.3-90's AC-P8 time (re-taken as -91). No
other (M) timing row falls in a window or ran above load 16.

### Slice 2's review (V63) and its fix (a09ea43)

The review of slice 2 (`REVIEW W5.3 SLICE2 REVISE 6b996ed`) found one
MAJOR and two NITs:

1. **MAJOR 1: R97-corr (c) made `RetryPolicy` comparable.** With the
   statuses slice and the predicate behind `rules *retryRules`, no field
   forbade `==`: `a == b` on two policies compiled at 6b996ed and not at
   67dcbb0, a policy could key a map, and `==` would compare the rules by
   identity; `go doc -all .` and an API golden of fields and methods cannot
   show it. a09ea43 adds a zero-size `_ [0]func()` as the first field, which
   keeps the type incomparable and the policy 56 bytes, and
   `TestRetryPolicyRules` checks `Comparable()` is false beside the size
   pin. Two mutants fail it (W5.3-94): the field removed fails the test
   binary's link, because Go 1.27.1's linker panics on that program ("R_USEIFACE
   in …TestRetryPolicyRules references type:.eqfunc.M24GM21 which is not a
   type or itab", a toolchain bug and a W7 note), and the field moved last
   fails the size pin. `go doc -all .` is unchanged (W5.3-97).
2. **NIT 7**: the frozen AC-P6 row now cites the rows that re-pinned N
   (W5.3-28, -41, -34, -35) and says its 100-run stability figure is W3.4's,
   taken at N = 14.
3. **NIT 8**: a held response also keeps the first attempt's 144 B URL
   copy alive since N1; `newSystemOneAlloc`'s and `systemOneAlloc`'s
   comments and N2's finding 3 say so.
4. **The open question on AC-P1's recorded growth rows**, ruled by the
   lead: the `*struct` and flat-map values of the frozen AC-P1 row are
   ranges, their cause sonic's pool drops, identical at 67dcbb0 and 6b996ed
   (the reviewer's measurement), so they predate W5.3; the pins are
   unchanged.
5. **A W7 note from the review**: a raw score criteria of `1e-400` is
   falsy in Python (the float underflows to 0.0) and truthy for
   `falsyJSON`, which reads its mantissa; P2 kept the old verdict, so the
   edge predates W5.3. No change now.

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W5.3-01 | 2026-09-26 08:36:53 UTC | W5.3 K36 `BenchmarkCall`, first run | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 2.49 → 9.10 | `BASE=67dcbb0 CAND=05264fe MAXLOAD=4 sh $A '(L)' $O /tmp/ts-spike/bench.lock k36-call-L internal/benchmark 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkCall$/^(sdk\|naive)(-q20)?$' -test.benchmem -test.count 2` | `call/sdk` 7.088 → 6.250 µs (−11.8 %), q20 29.89 → 26.49 µs (−11.4 %); naive unchanged; allocations 22 and 42 both sides | during W6.1's fuzz campaign on (L) (8 workers, load 2.5 → 9.1): absolute times about 15 % above W5.3-03; kept, not of record; `results/k36-call-L-{base,cand}.txt` |
| W5.3-02 | 2026-09-26 08:43:31 UTC | W5.3 K36 `BenchmarkDecode`, first run | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 8.73 → 10.49 | `BASE=67dcbb0 CAND=05264fe MAXLOAD=4 sh $A '(L)' $O /tmp/ts-spike/bench.lock k36-decode-L internal/codec 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkDecode(NaiveSonic)?$/^(result\|escaped-names\|escaped-member-names\|result-20\|structured-legend-flood-1k)$' -test.benchmem -test.count 2` | `result` −17.7 %, `result-20` −15.8 %, `escaped-names` −15.4 %, `escaped-member-names` −5.4 %, flood-1k −13.9 % | as W5.3-01 (load 8.7 → 10.5; waited 5 × 60 s); not of record; `results/k36-decode-L-{base,cand}.txt` |
| W5.3-03 | 2026-09-26 09:33:10 UTC | W5.3 K36 `BenchmarkCall` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.81 → 2.04 | `BASE=67dcbb0 CAND=05264fe MAXLOAD=2 sh $A '(L)' $O /tmp/ts-spike/bench.lock k36-call-L2 internal/benchmark 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkCall$/^(sdk\|naive)(-q20)?$' -test.benchmem -test.count 2` | `call/sdk` 6.180 → 5.610 µs (−9.2 %), `call/naive` 6.852 → 6.863 µs (~); q20 25.38 → 22.03 µs (−13.2 %), naive 25.79 → 25.93 µs (~); sdk/naive q3 0.902 → 0.817, q20 0.984 → 0.850; minima sdk 6149 → 5585 ns; B/op 2.930 → 2.870 KiB; allocations 22 and 42 both sides | row of record for (L), after W6.1's campaign ended (`results/batch-L.log`); `results/k36-call-L2-{base,cand}.txt` |
| W5.3-04 | 2026-09-26 09:35:47 UTC | W5.3 K36 `BenchmarkDecode` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.75 → 1.83 | `BASE=67dcbb0 CAND=05264fe MAXLOAD=2 sh $A '(L)' $O /tmp/ts-spike/bench.lock k36-decode-L2 internal/codec 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkDecode(NaiveSonic)?$/^(result\|escaped-names\|escaped-member-names\|result-20\|structured-legend-flood-1k)$' -test.benchmem -test.count 2` | `result` 3.428 → 2.779 µs (−18.9 %), `escaped-names` 5.004 → 4.234 µs (−15.4 %), `escaped-member-names` 7.254 → 7.039 µs (−3.0 %), `result-20` 21.49 → 17.75 µs (−17.4 %), flood-1k 615.8 → 543.0 µs (−11.8 %); naive rows unchanged | against naive.Sonic in the same runs: `result` 1.235 → 1.000 ×, `result-20` 1.293 → 1.072 ×, flood-1k 1.030 → 0.910 ×; `results/k36-decode-L2-{base,cand}.txt` |
| W5.3-05 | 2026-09-26 17:48:21 JST | W5.3 K36 `BenchmarkCall` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 7.73 → 6.24 | `BASE=67dcbb0 CAND=05264fe GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$F MAXLOAD=10 sh $A '(M)' $O $SP/bench.lock k36-call-M internal/benchmark 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkCall$/^(sdk\|naive)(-q20)?$' -test.benchmem -test.count 2` | `call/sdk` 4.846 → 4.365 µs (−9.9 %), `call/naive` 3.657 → 3.769 µs (~); q20 23.95 → 20.88 µs (−12.8 %), naive 12.97 → 13.10 µs (~); sdk/naive q3 1.325 → 1.158, q20 1.847 → 1.594; minima sdk 4717 → 4219 ns; allocations 22 and 42 both sides | `results/k36-call-M-{base,cand}.txt` |
| W5.3-06 | 2026-09-26 17:50:13 JST | W5.3 K36 `BenchmarkDecode` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 5.22 → 5.73 | `BASE=67dcbb0 CAND=05264fe GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$F MAXLOAD=10 sh $A '(M)' $O $SP/bench.lock k36-decode-M internal/codec 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkDecode(NaiveSonic)?$/^(result\|escaped-names\|escaped-member-names\|result-20\|structured-legend-flood-1k)$' -test.benchmem -test.count 2` | `result` 3.478 → 2.950 µs (−15.2 %), `escaped-names` 5.090 → 4.399 µs (−13.6 %), `escaped-member-names` 6.841 → 6.593 µs (−3.6 %, p = 0.052), `result-20` 21.57 → 18.55 µs (−14.0 %), flood-1k 679.8 → 613.5 µs (−9.8 %); naive rows unchanged | against naive.Sonic: `result` 2.35 → 1.86 ×, `result-20` 2.74 → 2.39 ×, flood-1k 3.51 → 2.98 × (K23: not met); `results/k36-decode-M-{base,cand}.txt` |
| W5.3-07 | 2026-09-26 17:54:23 JST | W5.3 K36 local CodSpeed proxy | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.26 → 10.80 | `BASE=67dcbb0 CAND=05264fe GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$F MAXLOAD=10 sh $C '(M)' $O $SP/bench.lock k36-codspeed-M 3 <base tree> <cand tree>` | per-iteration min / median / mean, 3 runs a side: `call/sdk` 4041–4083 → 3583–3625, 4541–4583 → 4000–4083, 4969–5137 → 4393–4496 ns; `call/naive` 2458–2500 both, 3125–3250, 3595–4050 ns; q20 sdk min 20666–20833 → 17791–17958 ns | codspeed-runner 5.3.1, walltime, `--skip-upload`; arm64, not CodSpeed's amd64; `results/k36-codspeed-M.txt` |
| W5.3-08 | 2026-09-26 17:36:39 JST | W5.3 K36 gates | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.49 → – | `gates.sh` in a detached worktree at 05264fe under `$F $SP/bench.lock`: build, vet, the section 11 chain (govulncheck via `go run`), the seam tests, `go test -race -count=1 ./...`, ci.yaml's two non-race steps (the second extracted with yq) | every gate ok; every allocation pin unchanged | `results/gates-M-05264fe.txt` (modernize's progress lines removed) |
| W5.3-09 | 2026-09-26 08:48:13 UTC | W5.3 K36 `-race`, validity gate, fuzz | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 9.68 → 18.16 | under `flock /tmp/ts-spike/bench.lock`: `go test -race -count=1 ./...`; `go test -count=1 -run '^(TestDecodeFixtures\|TestOneScanMatchesWholeScan\|TestOneScanTakesValidBodies\|TestDecodeDepthBound\|TestSeam.*)$' -v ./internal/codec/`; `-run '^TestMalformedFixturesRefused$' .`; then, the lock released, `go test -run '^$' -fuzz '^FuzzDecodeResponse$' -fuzztime 600s -parallel 8 ./internal/codec/` | `-race` PASS in seven packages; every validity and seam test PASS (11 030 differential bodies, 236 on the one scan); fuzz PASS, 600 s, 287 corpus entries | at 05264fe; W6.1's fuzz campaign ran beside the fuzz run; `results/k36-gates-L.txt` |
| W5.3-10 | 2026-09-26 17:47:11 JST | W5.3 K36 fuzz | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 7.37 → – | `GOEXPERIMENT=nosimd,noruntimesecret go test -run '^$' -fuzz '^FuzzDecodeResponse$' -fuzztime 30s -parallel 4 ./internal/codec/` at 05264fe, not under the lock | PASS | the lead's rule for (M): at most 30 s, never under the lock; `results/k36-fuzz-M.txt` |
| W5.3-11 | 2026-09-26 17:35:17 JST | W5.3 K36 guard mutants | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | each mutant planted in a copy of 05264fe's tree, then `go test -count=1 -run 'TestOneScan\|FuzzDecodeResponse\|TestDecodeDepthBound' ./internal/codec/` | all 8 fail: no quote-count check; escaped quotes not subtracted; no `\u` check; any backslash before a quote escaping it; no last-byte rule; no depth rule; no between-members rule; `slotCard` kept after the models array | unit tests, not a measurement; `results/k36-mutants-M.txt` |
| W5.3-12 | 2026-09-26 17:36:04 JST | W5.3 K36 per-fixture path | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | a throwaway test (not committed) decoding every fixture under testdata on a fresh decoder and reading `stats.wholes` | 21 of 41 fixtures take the one scan: all 14 benchmark bodies and `models.json`, escaped ones included; 7 refused by `cutPoint`, 13 declined by the cut traversal, all of them failing bodies | a tabulation, not a measurement; `results/k36-paths.txt` |
| W5.3-13 | 2026-09-26 16:51:05 JST | W5.3 K36 first version, fuzz | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.60 → 8.99 | `go test -run '^$' -fuzz '^FuzzDecodeResponse$' -fuzztime 120s -parallel 4 ./internal/codec/` at 753f723, under `$F $SP/bench.lock` | FAIL after 90 s: `{"\u"}` accepted by the one scan, refused by the whole-body path (finding 4 (a)) | a run the lead's later rule would keep off the lock and under 30 s on (M); `results/k36-first-fuzz-M-753f723.txt` |
| W5.3-14 | 2026-09-26 07:50:53 UTC | W5.3 K36 first version, `-race`, fuzz | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.00 → 3.72 | at 753f723 under `flock /tmp/ts-spike/bench.lock`: `go test -race -count=1 ./...`, the validity tests, then `-fuzz '^FuzzDecodeResponse$' -fuzztime 300s -parallel 8` | `-race` PASS; fuzz FAIL after 2 s: `{"0000000\":…,"\u0\"}`, the same class | `results/k36-first-fuzz-L-753f723.txt` |
| W5.3-15 | 2026-09-26 08:01:39 UTC | W5.3 K36 second version, `-race`, fuzz | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.66 → 4.00 | as W5.3-14, at 5ee759a | `-race` PASS; fuzz FAIL after 27 s: `{"0000000000000000000000000000000\0}` (finding 4 (b)) | `results/k36-second-fuzz-L-5ee759a.txt` |
| W5.3-16 | 2026-09-26 08:07:47 UTC | W5.3 K36 third version, fuzz | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.79 → 27.67 | `go test -run '^$' -fuzz '^FuzzDecodeResponse$' -fuzztime 600s -parallel 16 ./internal/codec/` at 5bb86f9 under `flock /tmp/ts-spike/bench.lock` | FAIL after 294 s: `{"":"` + 64 × `0` + `}` (finding 4 (c)) | the raw log and the 5bb86f9 tree were deleted from (L) at 17:36 JST before being copied; the input is a seed of FuzzDecodeResponse and a case of TestOneScanMatchesWholeScan; `-parallel 16` predates the lead's `-parallel 8` |
| W5.3-17 | 2026-09-26 17:12:43 JST | W5.3 seam test mutants (a2a29b4) | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | each mutant planted in a copy of the tree, then `go test -count=1 -run 'TestSeamImports\|TestSeamRootRawPointers' ./internal/codec/` | the tree and a named `decodeas_store.go` importing unsafe pass; `UnsafePointer()` in `decodeas.go`, the same in a new `internal/wire` file, and unsafe imported by `response.go` fail | unit tests; `results/seam-mutants-M.txt` |
| W5.3-18 | 2026-09-26 09:39:49 UTC | W5.3 typed store `BenchmarkSD2` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.83 → 1.07 | `BASE=05264fe CAND=7df5a6b MAXLOAD=2 sh $A '(L)' $O /tmp/ts-spike/bench.lock store-sd2-L _spikes/w4.3 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkSD2$/.*/(0-DecodeAs\|2-unsafe-offset)$' -test.benchmem -test.count 2` | `DecodeAs`: `result.json` 184.3 → 98.3 ns (−46.6 %), `result-20` 1198 → 714.8 ns (−40.3 %), flood-1k 992.9 → 1056.5 ns (+6.4 %); the offset replica 73.3, 620.4 and 990.1 ns; `DecodeAs` allocations 1 → 0 | W4.3's S-D2 benchmark, whose `0-DecodeAs` arm times the root package's `DecodeAs`; `results/store-sd2-L-{base,cand}.txt` |
| W5.3-19 | 2026-09-26 09:51:03 UTC | W5.3 typed store `BenchmarkCall/sdk` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 5.93 → 4.09 | `BASE=05264fe CAND=7df5a6b MAXLOAD=2 sh $A '(L)' $O /tmp/ts-spike/bench.lock store-call-L internal/benchmark 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkCall$/^sdk$' -test.benchmem -test.count 2` | 5.604 → 5.613 µs (~, p = 0.494) | waited 5 × 60 s for load ≤ 2, ran at 5.9 of 44; `results/store-call-L-{base,cand}.txt` |
| W5.3-20 | 2026-09-26 09:51:27 UTC | W5.3 typed store AC-P3, `-race` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 4.09 → 2.14 | under `flock /tmp/ts-spike/bench.lock`: `go test -count=1 -run '^TestAllocTypedDecode$' -v .` at 05264fe and 7df5a6b; `go test -race -count=1 ./...` and the seam and store tests at 7df5a6b | `DecodeAs` 1/144 → 0/0, `Answers()` decode 4/688 both, `SystemOne` 22/2648 both, `Ask` 23/2792 → 22/2648; `-race` PASS in seven packages; seam and store tests PASS | `results/store-typed-L.txt` |
| W5.3-21 | 2026-09-26 18:15:59 JST | W5.3 typed store `BenchmarkSD2` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.20 → 6.71 | `BASE=05264fe CAND=7df5a6b GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$F MAXLOAD=10 sh $A '(M)' $O $SP/bench.lock store-sd2-M _spikes/w4.3 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkSD2$/.*/(0-DecodeAs\|2-unsafe-offset)$' -test.benchmem -test.count 2` | `DecodeAs`: `result.json` 136.2 → 82.7 ns (−39.3 %), `result-20` 942.7 → 630.9 ns (−33.1 %), flood-1k 724.5 → 665.6 ns (−8.1 %); the offset replica 64.8, 555.6 and 658.5 ns; `DecodeAs` allocations 1 → 0 | waited 1 × 60 s for load ≤ 10; `results/store-sd2-M-{base,cand}.txt` |
| W5.3-22 | 2026-09-26 18:18:24 JST | W5.3 typed store `BenchmarkCall/sdk` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.71 → 6.98 | `BASE=05264fe CAND=7df5a6b GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$F MAXLOAD=10 sh $A '(M)' $O $SP/bench.lock store-call-M internal/benchmark 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkCall$/^sdk$' -test.benchmem -test.count 2` | 4.500 → 4.499 µs (~) | `results/store-call-M-{base,cand}.txt` |
| W5.3-23 | 2026-09-26 18:18:49 JST | W5.3 typed store AC-P3 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.98 → 6.98 | under `$F $SP/bench.lock`: `GOEXPERIMENT=nosimd,noruntimesecret go test -count=1 -run '^TestAllocTypedDecode$' -v .` at 05264fe and 7df5a6b | `DecodeAs` 1/144 → 0/0, `Answers()` decode 4/688 both, `SystemOne` 22/2648 both, `Ask` 23/2792 → 22/2648 | `results/store-typed-M.txt` |
| W5.3-24 | 2026-09-26 18:13:14 JST | W5.3 typed store gates | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 37.23 → – | as W5.3-08, at 7df5a6b | every gate ok | load 37.2 at the start from other lanes (counts do not depend on it, R17); `results/gates-M-7df5a6b.txt` |
| W5.3-25 | 2026-09-26 18:10:17 JST | W5.3 typed store mutants | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | each mutant planted in a copy of 7df5a6b's tree, then the store, AC-P3, AC-F12 and seam tests | all 6 fail: offset off by one, the next field's offset, a wider write (the two corrupt the stack: a fatal error in the AC-F12 differential; `TestStoreKeepsNeighbours` fails alone), the answer copied as bytes, the store in another file, `UnsafePointer()` in `decodeas.go` | unit tests; `results/store-mutants-M.txt` |
| W5.3-26 | 2026-09-26 18:11:16 JST | W5.3 typed store vet, escape analysis, `-race` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | `go vet ./...`; `go build -gcflags=-m=2 .`; `go test -c -gcflags=-m .`; `go test -race -count=1 -run 'TestStore\|TestDecodeTypedPlanMismatch\|TestDecodeAs\|Ask\|Typed\|PreparedFor' .` | vet ok; `b does not escape` in `decode`; no `moved to heap: t`; `-race` (checkptr) PASS | `results/store-escape-M.txt` |
| W5.3-27 | 2026-09-26 09:59:35 UTC | W5.3 N1 `BenchmarkCall` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.00 → 0.45 | `BASE=6f9a3af CAND=6ad7cf8 MAXLOAD=2 sh $A '(L)' $O /tmp/ts-spike/bench.lock n1-call-L internal/benchmark 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkCall$/^(sdk\|naive)(-q20)?$' -test.benchmem -test.count 2` | `call/sdk` 5.621 → 5.588 µs (−0.59 %, p = 0.011), `call/naive` 6.872 → 6.836 µs (−0.52 %, p = 0.035), q20 ~; allocations `call/sdk` 22 → 21, `call/sdk-q20` 42 → 41, bytes unchanged | the host's drift, alike on both sides (finding 2); `results/n1-call-L-{base,cand}.txt`, `results/batch-n1-L.log` |
| W5.3-28 | 2026-09-26 10:01:11 UTC | W5.3 N1 AC-P6 counts, ci.yaml's allocation-budget step, `-race` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.45 → 0.96 | under `flock /tmp/ts-spike/bench.lock`: `go test -count=5 -run '^TestAllocWholeCall$' -v .` at 6f9a3af and at 6ad7cf8; ci.yaml's allocation-budget step (extracted from the tree's ci.yaml with sed, run under `bash -eo pipefail`) and `go test -race -count=1 ./...` at 6ad7cf8 | q3 own 14/2 008 → 13/2 008 in 5 of 5 runs a side, call 22/2 648 → 21/2 648; q20 own 34 → 33; every test of the step passes (its output; the file's `step-exit 0` is the status of the grep that filtered it, not the step's); `-race` passes | `results/n1-alloc-L.txt` |
| W5.3-29 | 2026-09-26 19:02:06 JST | W5.3 N1 AC-P6 counts | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 37.48 → 37.48 | `GOEXPERIMENT=nosimd,noruntimesecret go test -count=5 -run '^TestAllocWholeCall$' -v .` at 6f9a3af and at 6ad7cf8 | as W5.3-28: own 14/2 008 → 13/2 008 in 5 of 5 runs a side; q20 34 → 33 | counts do not depend on the load (R17); taken inside the lead's window 10:01:13–10:02:30Z (a coverage build ran on (M) outside the lock), so re-taken as W5.3-41; `results/n1-alloc-M.txt` |
| W5.3-30 | 2026-09-26 19:06:44 JST | W5.3 N1 `BenchmarkCall` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.74 → 20.83 | `BASE=6f9a3af CAND=6ad7cf8 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$F MAXLOAD=10 sh $A '(M)' $O $SP/bench.lock n1-call-M internal/benchmark 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkCall$/^(sdk\|naive)(-q20)?$' -test.benchmem -test.count 2` | `call/sdk` 4.637 → 4.644 µs (~, p = 0.579), `call/naive` ~, q20 ~; allocations 22 → 21 and 42 → 41 | **noisy**: the load rose above 16 during the run (± 693 % on one side); waited 4 × 60 s for load ≤ 10; `results/n1-call-M-{base,cand}.txt` |
| W5.3-31 | 2026-09-26 18:59:17 JST | W5.3 N1 gates | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 89.65 → – | as W5.3-08, at 6ad7cf8 | every gate ok | load 89.7 at the start from other lanes (counts do not depend on it, R17); the modernize trace lines are left out of the copy; `results/gates-M-6ad7cf8.txt` |
| W5.3-32 | 2026-09-26 18:19:08 JST | W5.3 N1 mutants | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | each mutant planted in the working tree, then `go vet .` and the root package's unit tests | both fail: the retry reusing the call's URL storage (`TestRetryURLIsCopied`); no copy, the request pointing at the client's URL (`TestRequestURLIsCopied`, `TestRetryURLIsCopied`) | `results/n1-mutants-M.txt` |
| W5.3-33 | 2026-09-26 10:17:08 UTC | W5.3 N2 `BenchmarkCall` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.87 → 1.59 | `BASE=1500b71 CAND=c53a291 MAXLOAD=2 sh $A '(L)' $O /tmp/ts-spike/bench.lock n2-call-L internal/benchmark 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkCall$/^(sdk\|naive)(-q20)?$' -test.benchmem -test.count 2` | `call/sdk` 5.583 → 5.552 µs (~, p = 0.072), `call/naive` 6.840 → 6.897 µs (+0.83 %, p = 0.023), q20 ~; allocations 21 → 20, q20 41 → 41; `call/sdk` B/op 2.885 → 2.893 KiB | waited 1 × 60 s for load ≤ 2; `results/n2-call-L-{base,cand}.txt`, `results/batch-n2-L.log` |
| W5.3-34 | 2026-09-26 10:18:44 UTC | W5.3 N2 AC-P6 and AC-P3 counts, ci.yaml's allocation step, `-race` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.59 → 1.46 | under `flock /tmp/ts-spike/bench.lock`: `go test -count=5 -run '^(TestAllocWholeCall\|TestAllocTypedDecode)$' -v .` at 1500b71 and at c53a291; ci.yaml's allocation step (as W5.3-28) and `go test -race -count=1 ./...` at c53a291 | q3 own 13/2 008 → 12/2 008 in 5 of 5 runs a side, call 21/2 648 → 20/2 648, the call's allocation 1/256 → 1/704 and its decode 4/688 → 3/240; q20 own 33 → 33; `DecodeAs` 0/0 against the `Answers()` decode's 4/688, `Ask` 21/2 648 → 20/2 648; every test of the step passes (its output; `step-exit` is grep's status, as W5.3-28); `-race` passes | `results/n2-alloc-L.txt` |
| W5.3-35 | 2026-09-26 19:16:17 JST | W5.3 N2 AC-P6 and AC-P3 counts | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.43 → 9.43 | `GOEXPERIMENT=nosimd,noruntimesecret go test -count=5 -run '^(TestAllocWholeCall\|TestAllocTypedDecode)$' -v .` at 1500b71 and at c53a291 | as W5.3-34 | counts (R17); `results/n2-alloc-M.txt` |
| W5.3-36 | 2026-09-26 19:16:26 JST | W5.3 N2 gates | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.65 → – | as W5.3-08, at c53a291 | every gate ok | `results/gates-M-c53a291.txt` |
| W5.3-37 | 2026-09-26 19:14:06 JST | W5.3 N2 mutants | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | each mutant planted in a copy of the working tree, then `go vet ./...` and the N2 and allocation tests of the root package, `internal/wire` and `internal/codec` | all 8 fail: a shared array for three questions; `GrowInto` taking a spare from a set with entries, taking a spare too small, keeping the spare's length; `SystemOne` passing no spare; `finish` ignoring the spare; a four-entry array for three questions; no inline case for three questions | in the first run a spare kept at its length survived (every caller passes a length of 0) and `SystemOne` passing no spare did not compile; `TestAnswersGrowInto` gained a spare with a length and the mutant was rewritten to compile, before the commit; `results/n2-mutants-M.txt` |
| W5.3-38 | 2026-09-26 19:26:20 JST | W5.3 N2 `BenchmarkCall`, first run | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 199.48 → 234.30 | as W5.3-39 | `call/sdk` 11.13 → 11.56 µs (~); allocations 21 → 20 | **noisy**, not used: the lead's window 10:23–10:30Z (external `node -e` busy loops saturated (M)) after 5 × 60 s of waiting; my own P2 mutant runs overlapped rounds 3 to 5; `results/n2-call-M-noisy-{base,cand}.txt` |
| W5.3-39 | 2026-09-26 19:42:32 JST | W5.3 N2 `BenchmarkCall`, re-taken | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 13.16 → 6.12 | `BASE=1500b71 CAND=c53a291 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$F MAXLOAD=10 sh $A '(M)' $O $SP/bench.lock n2-call-M internal/benchmark 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkCall$/^(sdk\|naive)(-q20)?$' -test.benchmem -test.count 2` | `call/sdk` 4.485 → 4.413 µs (~, p = 0.123), `call/naive` ~, q20 ~; allocations 21 → 20, q20 41 → 41 | waited 5 × 60 s for load ≤ 10 and started at 13.16, under the lead's 16; `results/n2-call-M-{base,cand}.txt` |
| W5.3-40 | 2026-09-26 19:45:20 JST | W5.3 mutants of 69085ab and 9f23a43 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | each mutant planted in a copy of the working tree, then `go vet` and the one-scan, K41, aliasing, fuzz-seed and N2 tests | all 5 fail: K10 (any error of the cut traversal taken as the cut) and K9 (a `\\u` of three hex digits taken as complete), each on its guard-table row and its testdata corpus entry; no quote-parity check in `cutPoint`; a spare taken for no entries; no inline array for four questions | `results/review-mutants-M.txt` |
| W5.3-41 | 2026-09-26 19:47:59 JST | W5.3 N1 AC-P6 counts, re-taken | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 111.70 → 111.70 | as W5.3-29, at 6f9a3af and at 1500b71 (0422123's code) | as W5.3-29: own 14/2 008 → 13/2 008 in 5 of 5 runs a side, call 22/2 648 → 21/2 648 | re-takes W5.3-29, which fell in the lead's window 10:01:13–10:02:30Z; counts do not depend on the load (R17), the load was from other work; `results/n1-alloc-M-retake.txt` |
| W5.3-42 | 2026-09-26 20:07:26 JST | W5.3 public API | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | `GOEXPERIMENT=nosimd,noruntimesecret go doc -all .` in detached worktrees at 67dcbb0 (the base) and at 9f23a43 | identical, 1 785 lines, the same SHA-256 | `results/godoc-M.txt` |
| W5.3-43 | 2026-09-26 19:54:15 JST | W5.3 gates of 69085ab | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 5.55 → – | as W5.3-08, at 69085ab | every gate ok | `results/gates-M-69085ab.txt` |
| W5.3-44 | 2026-09-26 19:55:32 JST | W5.3 gates of 9f23a43 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.41 → – | as W5.3-08, at 9f23a43 | every gate ok | `results/gates-M-9f23a43.txt` |
| W5.3-45 | 2026-09-26 19:47:08 JST | W5.3 K41 reproduction | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | `GOEXPERIMENT=nosimd,noruntimesecret go run ./_spikes/w5.3/k41`, the program as 9f23a43 commits it, run from the working tree before that commit | findings 1 and 2 of the K41 section: `ast.Preorder` takes the open string as complete at n = 32, 64, 96, 128 and at no other n tried; `decoder.Skip` accepts it at n = 32·k and reports an end past a 37-byte input; the codec refuses all 5 bodies | `results/k41-repro-M.txt` |
| W5.3-46 | 2026-09-26 10:24:44 UTC | W5.3 P1 `BenchmarkPrepare` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.09 → 0.75 | `BASE=c53a291 CAND=133b642 MAXLOAD=2 sh $A '(L)' $O /tmp/ts-spike/bench.lock p1-prepare-L . 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkPrepare$' -test.benchmem -test.count 2` | `c5-raw-100x3` 138.01 → 85.09 µs (−38.34 %), `c1-sketch` 1.206 → 1.110 µs (−7.96 %), `n8b-map-control` −12.07 %, `n8a-array-control` −8.91 %, `n8b-map-score` −6.39 %, `n8a-array-score` −5.35 %, `c6` −2.83 %, `c2` −2.79 %, `c4a` −1.85 %, `c3` +1.55 %, `c4b` ~; allocations as the table | `results/p1-prepare-L-{base,cand}.txt`, `results/batch-p1-L.log` |
| W5.3-47 | 2026-09-26 10:31:26 UTC | W5.3 P1 `TestAllocPrepare`, ci.yaml's allocation step, the wire and root tests | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | – → 2.35 | under `flock /tmp/ts-spike/bench.lock`: `go test -count=5 -run '^TestAllocPrepare$' -v .` at c53a291 and 133b642; ci.yaml's allocation step and `go test -count=1 ./internal/wire/ .` at 133b642 | the table's base and P1 columns, 5 of 5 runs each; the step and the tests exit 0 (their own exit statuses) | `results/p1-alloc-L.txt` |
| W5.3-48 | 2026-09-26 19:56:43 JST | W5.3 P1 `BenchmarkPrepare`, the changed cases | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 7.85 → 7.99 | `BASE=c53a291 CAND=133b642 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$F MAXLOAD=10 sh $A '(M)' $O $SP/bench.lock p1-prepare-M . 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkPrepare$/^(c1-sketch\|c5-raw-100x3\|c6-escapes\|n8a-array-control\|n8b-map-control)$' -test.benchmem -test.count 2` | `c5-raw-100x3` 76.11 → 51.88 µs (−31.84 %); `c1`, `n8a-array-control`, `n8b-map-control` ~; allocations as (L) | `results/p1-prepare-M-{base,cand}.txt` |
| W5.3-49 | 2026-09-26 19:22:36 JST | W5.3 P1 mutants | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | each mutant planted in a copy of the working tree, then `go vet` and the Prepare and raw-writer tests | all 7 fail: keys not sorted; the whole stack sorted; the keys returned from the stack's start; a nested `map[string]any` or `map[string]string` leaving its keys; `Raw` leaving the fields' keys; a fresh key slice per map | four of the first attempts failed only to compile or survived (the two nested pops, whose outer map truncated after them); the test gained a check below any map's truncation; `results/p1-mutants-M.txt` |
| W5.3-50 | 2026-09-26 19:48:02 JST | W5.3 gates of 133b642 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 111.70 → – | as W5.3-08, at 133b642 | every gate ok | load from other work; `results/gates-M-133b642.txt` |
| W5.3-51 | 2026-09-26 10:35:37 UTC | W5.3 P2 `BenchmarkPrepare` and `BenchmarkFalsyJSON` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.12 → 1.37 | `BASE=133b642 CAND=0aa6730 MAXLOAD=2 sh $A '(L)' $O /tmp/ts-spike/bench.lock p2-prepare-L . 5 <base tree> <cand tree> -test.run '^$' -test.bench '^Benchmark(Prepare\|FalsyJSON)$' -test.benchmem -test.count 2` | `n8a-array-score` 103.05 → 53.46 µs (−48.12 %), `n8b-map-score` 429.3 → 219.2 µs (−48.94 %); `FalsyJSON/array` 2 196.5 → 6.066 ns, `/map` 1 844 → 7.478 ns, `/small-14B` 47.29 → 5.690 ns; the other cases within ±1.3 % | `results/p2-prepare-L-{base,cand}.txt`, `results/batch-p25-L.log` |
| W5.3-52 | 2026-09-26 19:58:47 JST | W5.3 P2 `BenchmarkPrepare` and `BenchmarkFalsyJSON`, the changed cases | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 7.99 → 8.07 | `BASE=133b642 CAND=0aa6730 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$F MAXLOAD=10 sh $A '(M)' $O $SP/bench.lock p2-prepare-M . 5 <base tree> <cand tree> -test.run '^$' -test.bench '^Benchmark(Prepare\|FalsyJSON)$/^(n8a-array-score\|n8b-map-score\|small-14B\|array\|map)$' -test.benchmem -test.count 2` | `n8a-array-score` 55.34 → 29.57 µs (−46.57 %), `n8b-map-score` 245.8 → 126.3 µs (−48.61 %); `FalsyJSON/array` 1 230 → 4.020 ns, `/map` 1 030 → 5.328 ns, `/small-14B` 28.00 → 3.890 ns | `results/p2-prepare-M-{base,cand}.txt` |
| W5.3-53 | 2026-09-26 19:29:09 JST | W5.3 P2 mutants | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | as W5.3-49, with `FuzzFalsyJSON`'s seeds and `TestAllocFalsyJSON` | all 9 fail: null, every string, every array, every object a candidate; no space skipped inside an array; a zero digit ruling a number out; the exponent scanned as mantissa; an object closed by a bracket; leading space not skipped | every string a candidate passed the verdict tests (a more permissive pre-check gives the same verdicts) until `TestAllocFalsyJSON` pinned the success path at 0; `results/p2-mutants-M.txt` |
| W5.3-54 | 2026-09-26 10:59:38 UTC | W5.3 P2 `FuzzFalsyJSON` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.39 → 8.74 | under `flock /tmp/ts-spike/bench.lock`: `go test -run '^$' -fuzz '^FuzzFalsyJSON$' -fuzztime 300s -parallel 8 .` at e178067 | PASS: 12 978 921 executions, 219 new corpus entries, no failure | `results/p2-fuzz-L.txt` |
| W5.3-55 | 2026-09-26 19:49:16 JST | W5.3 gates of 0aa6730 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 37.18 → – | as W5.3-08, at 0aa6730 | every gate ok | `results/gates-M-0aa6730.txt` |
| W5.3-56 | 2026-09-26 10:41:12 UTC | W5.3 P3 `BenchmarkPrepare` and `BenchmarkFalsyJSON` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.37 → 1.35 | `BASE=0aa6730 CAND=035934a MAXLOAD=2 sh $A '(L)' $O /tmp/ts-spike/bench.lock p3-prepare-L . 5 <base tree> <cand tree> -test.run '^$' -test.bench '^Benchmark(Prepare\|FalsyJSON)$' -test.benchmem -test.count 2` | `n8a-array-control` −15.89 %, `n8a-array-score` −15.98 %, `n8b-map-control` −12.72 %, `n8b-map-score` −12.98 %; **`c5-raw-100x3` 85.05 → 92.28 µs (+8.50 %)**, `c1-sketch` 1.099 → 1.157 µs (+5.37 %); `FalsyJSON` +17 to +26 % (1 ns, undone by P4's build: layout) | finding 3; `results/p3-prepare-L-{base,cand}.txt` |
| W5.3-57 | 2026-09-26 20:00:48 JST | W5.3 P3 `BenchmarkPrepare`, the changed cases | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.07 → 7.96 | `BASE=0aa6730 CAND=035934a GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$F MAXLOAD=10 sh $A '(M)' $O $SP/bench.lock p3-prepare-M . 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkPrepare$/^(c5-raw-100x3\|n8a-array-score\|n8a-array-control\|n8b-map-score\|n8b-map-control)$' -test.benchmem -test.count 2` | `c5-raw-100x3` 57.69 → 55.20 µs (−4.32 %), `n8a-array-control` −10.15 %, `n8a-array-score` −11.91 %, `n8b-map-control` −9.87 %, `n8b-map-score` −11.03 %; bytes and counts as (L) | ended at 20:02:48, the first two seconds of the lead's third window; spreads ±2 to ±16 %; re-taken as W5.3-57a/-57b; `results/p3-prepare-M-noisy-{base,cand}.txt` |
| W5.3-57a | 2026-09-26 20:14:18 JST | W5.3 P3 `BenchmarkPrepare`, first re-take | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.54 → 114.26 | as W5.3-57b | spreads ±200 to ±600 % | **noisy**, not used: an external burst raised the load to 114 during the run; `results/p3-prepare-M-noisy2-{base,cand}.txt` |
| W5.3-57b | 2026-09-26 20:32:01 JST | W5.3 P3 `BenchmarkPrepare`, re-taken | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 4.99 → 9.15 | `BASE=0aa6730 CAND=035934a GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$F MAXLOAD=10 sh $A '(M)' $O $SP/bench.lock p3-prepare-M . 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkPrepare$/^(c1-sketch\|c5-raw-100x3\|n8a-array-score\|n8a-array-control\|n8b-map-score\|n8b-map-control)$' -test.benchmem -test.count 2` | `c5-raw-100x3` 57.07 → 56.74 µs (~, p = 0.579), `n8a-array-control` −8.04 %, `n8a-array-score` −10.15 %, `n8b-map-control` −9.71 %, `n8b-map-score` −10.69 %, `c1-sketch` +6.38 % (± 81 % on one side); bytes and counts as (L) | the row of record for P3 on (M); `results/p3-prepare-M-{base,cand}.txt` |
| W5.3-58 | 2026-09-26 19:31:15 JST | W5.3 P3 mutants | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | as W5.3-49, with `TestSizeHintCoversRawValues` | all 4 fail: raw values not counted; strings, `RawJSON` or `Content` not counted | `results/p3-mutants-M.txt` |
| W5.3-59 | 2026-09-26 19:50:29 JST | W5.3 gates of 035934a | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 14.60 → – | as W5.3-08, at 035934a | every gate ok | `results/gates-M-035934a.txt` |
| W5.3-60 | 2026-09-26 10:46:49 UTC | W5.3 P4 `BenchmarkPrepare` and `BenchmarkFalsyJSON` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.35 → 0.95 | `BASE=035934a CAND=c8c26af MAXLOAD=2 sh $A '(L)' $O /tmp/ts-spike/bench.lock p4-prepare-L . 5 <base tree> <cand tree> -test.run '^$' -test.bench '^Benchmark(Prepare\|FalsyJSON)$' -test.benchmem -test.count 2` | `c4b-score-20x8-json` 46.93 → 42.71 µs (−9.00 %), 43.37 → 32.65 KiB; the other cases within ±0.7 %; `FalsyJSON` −15 to −22 % (layout, as W5.3-56) | `results/p4-prepare-L-{base,cand}.txt` |
| W5.3-61 | 2026-09-26 20:02:49 JST | W5.3 P4 `BenchmarkPrepare`, first run | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.17 → 74.23 | as W5.3-62 | `c4b` 70.24 → 64.69 µs (~, ±50 %) | **noisy**, not used: the lead's third window; `results/p4-prepare-M-noisy-{base,cand}.txt` |
| W5.3-62 | 2026-09-26 20:11:35 JST | W5.3 P4 `BenchmarkPrepare`, re-taken | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 7.03 → 6.26 | `BASE=035934a CAND=c8c26af GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$F MAXLOAD=10 sh $A '(M)' $O $SP/bench.lock p4-prepare-M . 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkPrepare$/^(c4a-score-20x8-text\|c4b-score-20x8-json)$' -test.benchmem -test.count 2` | `c4b-score-20x8-json` 27.13 → 24.75 µs (−8.77 %), 36 → 28 allocations; `c4a` ~ | `results/p4-prepare-M-{base,cand}.txt` |
| W5.3-63 | 2026-09-26 19:32:43 JST | W5.3 P4 mutants | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | as W5.3-49 | all 3 fail: the spans not reserved; every level counted; no JSON level counted | `results/p4-mutants-M.txt` |
| W5.3-64 | 2026-09-26 19:51:44 JST | W5.3 gates of c8c26af | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 7.98 → – | as W5.3-08, at c8c26af | every gate ok | `results/gates-M-c8c26af.txt` |
| W5.3-65 | 2026-09-26 10:52:32 UTC | W5.3 P5 `BenchmarkPrepare` and `BenchmarkFalsyJSON` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.95 → 1.06 | `BASE=c8c26af CAND=e178067 MAXLOAD=2 sh $A '(L)' $O /tmp/ts-spike/bench.lock p5-prepare-L . 5 <base tree> <cand tree> -test.run '^$' -test.bench '^Benchmark(Prepare\|FalsyJSON)$' -test.benchmem -test.count 2` | `c3-choice-20x10` 21.19 → 20.79 µs (−1.91 %), `c4a` 17.19 → 16.43 µs (−4.42 %), `c4b` 42.74 → 42.03 µs (−1.64 %); `c2-noul-short` 188.2 → 196.0 ns (+4.14 %), `c1` +1.12 %; bytes `c3` +1.68 %, `c4a` +0.75 %, `c4b` +0.38 % | finding 4; `results/p5-prepare-L-{base,cand}.txt` |
| W5.3-66 | 2026-09-26 20:08:42 JST | W5.3 P5 `BenchmarkPrepare`, first run | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 29.71 → 10.97 | as W5.3-67 | – | **noisy**, not used: started at load 29.71, above 16, after 5 × 60 s; `results/p5-prepare-M-noisy-{base,cand}.txt` |
| W5.3-67 | 2026-09-26 20:12:25 JST | W5.3 P5 `BenchmarkPrepare`, re-taken | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.26 → 8.76 | `BASE=c8c26af CAND=e178067 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=$F MAXLOAD=10 sh $A '(M)' $O $SP/bench.lock p5-prepare-M . 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkPrepare$/^(c1-sketch\|c3-choice-20x10\|c4a-score-20x8-text\|c4b-score-20x8-json)$' -test.benchmem -test.count 2` | `c3-choice-20x10` 12.25 → 11.85 µs (−3.29 %), `c1-sketch` 637.2 → 621.5 ns (−2.46 %), `c4a` and `c4b` ~; 27 → 8, 27 → 8, 28 → 9 allocations | `results/p5-prepare-M-{base,cand}.txt` |
| W5.3-68 | 2026-09-26 19:34:27 JST | W5.3 P5 mutants | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | as W5.3-49, with `TestPrepareTablesOwnArrays` and `TestCutTables` | all 5 fail: a table not capped; the arena not advanced; the options or the levels not counted; the last table falling back | `results/p5-mutants-M.txt` |
| W5.3-69 | 2026-09-26 19:53:01 JST | W5.3 gates of e178067 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 5.67 → – | as W5.3-08, at e178067 | every gate ok | `results/gates-M-e178067.txt` |
| W5.3-70 | 2026-09-26 10:58:07 UTC | W5.3 P2 to P5 `TestAllocPrepare` and `TestAllocFalsyJSON`, the wire and root tests, ci.yaml's allocation step | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.39 → 1.39 | under `flock /tmp/ts-spike/bench.lock`: `go test -count=5 -run '^(TestAllocPrepare\|TestAllocFalsyJSON)$' -v .` and `go test -count=1 ./internal/wire/ .` at each of 133b642, 0aa6730, 035934a, c8c26af, e178067; ci.yaml's allocation step at e178067 | the table's columns P1 to P5, 5 of 5 runs each; `TestAllocFalsyJSON` passes wherever it exists; every test run and the step exit 0 (their own statuses) | `results/p25-alloc-L.txt` |
| W5.3-71 | 2026-09-26 20:52:52 JST | W5.3 allocation probes: isSecretHeader and redaction, a typed failure, the option costs, the policy setters | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.91 → – | the probes of `_spikes/w5.3/probes/` copied into detached worktrees at 9f23a43 and f6f91b9 (redaction, NIT F), at f6f91b9 and 1487d03 (R97-corr (c)), `go test -count=1 -run '^<probe>$' -v .` | the numbers of the section above, each before and after | counts (R17); W3.4's option probe with `measureCallItems` given its prefix; `results/alloc-probes-M.txt` |
| W5.3-72 | 2026-09-26 20:19:41 JST | W5.3 isSecretHeader and NIT F mutants | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | each mutant planted in a copy of the working tree, then `go vet .` and the redaction, typed and allocation tests | all 6 fail: the words or the names matched with case; no fallback for a non-ASCII name; the lower-case copy kept; the decoder's form rendered first; the typed error given the decoder's form | `results/sec-mutants-M.txt` |
| W5.3-73 | 2026-09-26 20:29:21 JST | W5.3 R97-corr (c) mutants | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | as W5.3-72, with the retry tests | all 4 fail: a setter writing the shared rules; a setter dropping the other rule; the predicate never asked; the statuses kept inline again | `results/c97-mutants-M.txt` |
| W5.3-74 | 2026-09-26 20:20:28 JST | W5.3 gates of 5e91193 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.95 → – | as W5.3-08, at 5e91193 | every gate ok | `results/gates-M-5e91193.txt` |
| W5.3-75 | 2026-09-26 20:21:31 JST | W5.3 gates of 9972097 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 10.87 → – | as W5.3-08, at 9972097 | every gate ok | `results/gates-M-9972097.txt` |
| W5.3-76 | 2026-09-26 20:22:46 JST | W5.3 gates of f6f91b9 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.56 → – | as W5.3-08, at f6f91b9 | every gate ok | `results/gates-M-f6f91b9.txt` |
| W5.3-77 | 2026-09-26 20:50:51 JST | W5.3 gates of 1487d03 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 13.52 → – | as W5.3-08, at 1487d03 (R97-corr (c) on f6f91b9, after R54 was moved out from under it) | every gate ok | `results/gates-M-1487d03.txt` |
| W5.3-78 | 2026-09-26 11:25:35 UTC | W5.3 R54 as built, `BenchmarkEncodeState` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.00 → 1.00 | `BASE=f6f91b9 CAND=6bb69bf MAXLOAD=2 sh $A '(L)' $O /tmp/ts-spike/bench.lock r54-encode-L internal/codec 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkEncodeState$' -test.benchmem -test.count 2` | check: CJK 806.3 ns → 95.1 ns (1 KiB), 51.22 → 5.46 µs (64 KiB), 4 922.7 → 524.8 µs (6 MiB); ASCII 36.88 → 106.3 ns, 1.908 → 6.901 µs, 236.5 → 662.1 µs; encode: CJK −79 to −85 %, ASCII +46 to +119 % | finding 1; `results/r54-encode-L-{base,cand}.txt`, `results/batch-r54-L.log` |
| W5.3-79 | 2026-09-26 11:40:06 UTC | W5.3 R54 validator variants | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | – → – | under `flock /tmp/ts-spike/bench.lock`: the probe (`_spikes/w5.3/probes/r54-variants_test.go.txt`) in the 6bb69bf tree, `go test -run '^$' -bench '^BenchmarkR54Probe$' -count 3 ./internal/codec/` | B/s, 3 runs each: sonic on the whole input 53.6, 102.1 and 34.6 G (ASCII 1 KiB, 64 KiB, 6 MiB) and 11.0, 12.1, 12.1 G (CJK); `utf8.Valid` 26.4, 34.2, 27.0 G and 1.25, 1.28, 1.25 G; the Go-scan variants 9.3 to 23.7 G on ASCII | finding 2; `results/r54-variants-L.txt` |
| W5.3-80 | 2026-09-26 20:53:05 JST | W5.3 R54 validator variants | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.16 → 4.14 | the same probe, its test binary built from the 6bb69bf tree, run under `$F $SP/bench.lock` after waiting for load < 10 | sonic on the whole input 3.1, 3.6 and 3.6 G (ASCII) and 4.5, 5.3, 5.4 G (CJK); `utf8.Valid` 53.9, 77.6, 77.3 G and 2.30, 2.38, 2.41 G; the best Go-scan variant 37.5 to 39.3 G on ASCII | finding 2: the (M) probe disagrees, so R54 is reverted; `results/r54-variants-M.txt` |
| W5.3-81 | 2026-09-26 20:34:25 JST | W5.3 R54 as built, `BenchmarkEncodeState` | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.90 → 150.89 | as W5.3-78 on (M) | – | **noisy**, not used: an external burst during the run (spreads to ± 1 700 %); `results/r54-encode-M-noisy-{base,cand}.txt` |
| W5.3-82 | 2026-09-26 11:32:58 UTC | W5.3 R54 as built, `-race` and `FuzzValidUTF8` | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.03 → 8.92 | under `flock /tmp/ts-spike/bench.lock` at 6bb69bf: `go test -race -count=1 ./...`; `go test -run '^$' -fuzz '^FuzzValidUTF8$' -fuzztime 300s -parallel 8 ./internal/codec/` | `-race` passes; the fuzz passes: 13 429 007 executions, 38 new corpus entries, no failure | `results/r54-race-fuzz-L.txt` |
| W5.3-83 | 2026-09-26 20:24:06 JST | W5.3 R54 as built, mutants | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | as W5.3-72, with the validator's tests | all 4 fail: the rest not validated; the first non-ASCII byte skipped; a lane missing from the word mask; the ASCII scan stopping a byte late | `results/r54-mutants-M.txt` |
| W5.3-84 | 2026-09-26 20:25:42 JST | W5.3 gates of 6bb69bf (R54 as built, reverted) | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 5.75 → – | as W5.3-08, at 6bb69bf | every gate ok | the commit was not pushed; `results/gates-M-6bb69bf.txt` |
| W5.3-85 | 2026-09-26 11:30:23 UTC | W5.3 K23 decode against the naive sonic decode | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.00 → 1.03 | `BASE=f6f91b9 CAND=6bb69bf MAXLOAD=2 sh $A '(L)' $O /tmp/ts-spike/bench.lock k23-decode-L internal/codec 5 <base tree> <cand tree> -test.run '^$' -test.bench '^BenchmarkDecode(NaiveSonic)?$/^(result\|result-20\|structured-legend-flood-1k)$' -test.benchmem -test.count 2` (the decode is the same on both sides) | SDK over naive: `result.json` 2.829/2.767 µs = 1.02, `result-20` 18.00/16.57 = 1.09, flood-1k 543.1/593.6 = 0.91 | agrees with W5.3-04's after-K36 column; `results/k23-decode-L-{base,cand}.txt` |
| W5.3-86 | 2026-09-26 20:45:49 JST | W5.3 K23 decode against the naive sonic decode | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 152.45 → 48.04 | as W5.3-85 on (M) | – | **noisy**, not used; `results/k23-decode-M-noisy-{base,cand}.txt` |
| W5.3-87 | 2026-09-26 20:55:15 JST | W5.3 K23 decode against the naive sonic decode, re-taken | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 4.14 → 16.38 | as W5.3-85 on (M), after waiting for load < 10 | SDK over naive: `result.json` 2.989/1.593 µs = 1.88 (± 79 % on one side), `result-20` 18.71/8.27 = 2.26, flood-1k 628.5/211.2 = 2.98 | **noisy** (the load reached 16.38 at the end); agrees with W5.3-06's after-K36 column, the row of record; `results/k23-decode-M-noisy2-{base,cand}.txt` |
| W5.3-88 | 2026-09-26 20:57:18 JST | W5.3 merge-tree against the live wave branches | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | `git merge-tree --write-tree --name-only HEAD origin/wave/<b>` for w5.4 (b4faf7c), w6.1 (2b9ecfa) and w6.3 (a5b5e53), at 46416b6 | each conflicts only in `docs/perf/ledger.md`, where each wave appends its section; ci.yaml, errors.go and decode_fuzz_test.go merge on their own | the final head adds only documentation to 46416b6; `results/merge-tree-M.txt` |
| W5.3-89 | 2026-09-26 11:32:47 UTC | W5.3 records: AC-P2, AC-P8, K24 | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 1.03 → 1.03 | under `flock /tmp/ts-spike/bench.lock` at 6bb69bf (R54's check changes no count): `go test -count=1 -run '^(TestAllocDecodeFixtures\|TestLinearityFlood\|TestLinearityFloodTime\|TestAllocScratchSequence)$' -v .` | AC-P2 `result.json` 4/688 against naive 40/3 672, ratio 0.100; AC-P8 allocations 7.61 (bound 12), time 9.15 (bound 15); K24's SEQ rows of the section above | `results/r54-records-L.txt` |
| W5.3-90 | 2026-09-26 20:48:23 JST | W5.3 records: AC-P2, AC-P8 allocations, K24, the new pins | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 48.04 → 44.36 | under `$F $SP/bench.lock` at d15781a (1487d03 plus R54 as built, which changes no count): the tests of W5.3-89 with `TestAllocSecretHeaderName`, `TestAllocTypedFailure`, `TestAllocAnswersInlineBound`, `TestAllocWholeCall`, `TestAllocPrepare` | AC-P2 `result.json` against naive 26/3 960, ratio 0.154; AC-P8 allocations 7.61; K24's SEQ rows; every pin passes; q3 own 12/2 008 | counts only (R17); its AC-P8 time (9.57) is noisy and re-taken as W5.3-91; `results/records-M.txt` |
| W5.3-91 | 2026-09-26 20:59:44 JST | W5.3 AC-P8 time and allocations, re-taken | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 6.80 → 9.96 | under `$F $SP/bench.lock` after waiting for load < 10, at d15781a: `go test -count=3 -run '^(TestLinearityFloodTime\|TestLinearityFlood)$' -v .` | time ratio 9.43, 9.40, 9.47 (bound 15); allocations 7.61 in each run (bound 12) | `results/acp8-M.txt` |
| W5.3-92 | 2026-09-26 12:11:43 UTC | W5.3 R111, each commit alone | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.07 → – | each commit of 67dcbb0..46416b6 (20) as its own `git archive` tree, `go build ./... && go vet ./... && go test -count=1 ./...` under `set -o pipefail`, two at a time at `nice -n 19` | every commit exits 0; 140 `ok` lines (7 packages × 20), 0 FAIL lines, 0 nonzero exits | the final head adds only documentation to 46416b6; each commit's (M) gates are rows W5.3-08 to -77; `results/r111-L.txt` |
| W5.3-93 | 2026-09-26 21:14:39 JST | W5.3 public API after R97-corr (c) | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | `GOEXPERIMENT=nosimd,noruntimesecret go doc -all .` at 46416b6 against 67dcbb0's (W5.3-42) | byte-identical, the same SHA-256 | `results/godoc-M.txt` |
| W5.3-94 | 2026-09-26 21:58:31 JST | W5.3 mutants of a09ea43 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | as W5.3-72, with the retry tests | both fail: the zero-size field removed (the test binary fails to link: Go 1.27.1's linker panics with "R_USEIFACE … references type:.eqfunc.M24GM21 which is not a type or itab"); the field moved last (`TestRetryPolicyRules`, the size pin) | `results/v63-mutants-M.txt` |
| W5.3-95 | 2026-09-26 22:00:10 JST | W5.3 gates of a09ea43 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 3.71 → – | as W5.3-08, at a09ea43 | every gate ok | `results/gates-M-a09ea43.txt` |
| W5.3-96 | 2026-09-26 13:01:03 UTC | W5.3 R111 for a09ea43 | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0.01 → – | a09ea43's `git archive` tree: `go build ./... && go vet ./... && go test -count=1 ./...` under `set -o pipefail` | 7 packages `ok`, exit 0 | its parent 6b996ed is W5.3-92's 46416b6 plus documentation, and the docs commit after it changes no code; `results/r111-a09ea43-L.txt` |
| W5.3-97 | 2026-09-26 22:00:01 JST | W5.3 public API after a09ea43 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | `GOEXPERIMENT=nosimd,noruntimesecret go doc -all .` at a09ea43 against 67dcbb0's | byte-identical (a blank field is not shown) | `results/godoc-M.txt` |

## W5.4: CodSpeed and AC-P7

W5.4 owns `.github/workflows/bench.yaml`'s CodSpeed job and
[`codspeed.md`](codspeed.md), and reads AC-P7: "CodSpeed reports
`call/sdk` faster than `call/naive` on the PR run". The repository takes
no pull requests (R2), so the PR run is two runs of `bench.yaml` on the
landing commit's tree: the `workflow_dispatch` run on `wave/w5.4` at its
final head, then `main`'s first push run after the landing. That reading
is W5.4's deviation 1, recorded for the owner's review. W5.1's finding 8
("AC-P7 is W5.4's pull-request run") means these two runs. Owner ruling
R108 reads "faster" on the mean (the total time divided by the rounds,
`go test`'s ns/op), not on the minimum CodSpeed's report shows, and keeps
AC-P7 report-only until K7 is met. So every row records the minimum, the
median and the mean of both rows and the three ratios, and asserts none.
W5.4 started on aaa9698 before W5.3 (G4) and lands after it: rows up to
W5.4-08 predate W5.3 and are history; the AC-P7 runs of record come after
the rebase onto W5.3's landing. Raw outputs are in `_spikes/w5.4/results/`,
and `_spikes/w5.4/render.py` prints the tables below from
`results/codspeed-call-stats.tsv`.

### How the numbers were taken

- **CI:** `bench.yaml` on `ubuntu-26.04` (4 vCPUs), Go from `go.mod`'s
  toolchain line (setup-go printed `Setup go version spec 1.27.1`), no
  `GOEXPERIMENT`. The statistics are the go runner's, as CodSpeed stored
  them: `get_benchmark_result` of the CodSpeed MCP server for
  `internal/benchmark/call_test.go::BenchmarkCall::{sdk,naive}`, copied
  into the TSV unchanged. From c15ba0c on, the job's report step prints
  the same statistics from the results files before the upload, and
  W5.4-08's equal CodSpeed's to the ns. "When" is the CodSpeed run's date
  as `list_runs` prints it.
- **CPU model:** from the report step from c15ba0c on. For earlier runs,
  from CodSpeed's `compare_runs`, which lists an "Environment Differences"
  section only when two runs' hardware differs, against the EPYC 7763 run
  of W5.1-25, and from W5.1-26 (the 9V74).
- **(M) lists:** `go1.27.1 darwin/arm64` with
  `GOEXPERIMENT=nosimd,noruntimesecret` in the lane's worktree at c15ba0c;
  the 1x row expansion ran under `/opt/homebrew/opt/util-linux/bin/flock`
  on the lead's `bench.lock` and is not a timing.

### Rows

| # | When | Wave | Host | `go version` | ToolTags | Load | Command | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| W5.4-01 | 2026-09-26 04:45:51 UTC | W5.4 AC-P7 survey | `ubuntu-26.04` (AMD EPYC 7763) | `go1.27.1 linux/amd64` | not printed by the job | – | `bench.yaml` at f048059: `CodSpeedHQ/action@v5` (runner 5.2.1, go runner 1.3.0), `mode: walltime`, `go test -bench=. ./...`, raw samples on `/tmp` | `BenchmarkCall/sdk` min / median / mean 5.570 / 5.941 / 6.816 µs; `/naive` 4.859 / 5.520 / 7.142 µs; **sdk/naive 1.146 / 1.076 / 0.954** (min / median / mean) | GitHub run 36218293900 (push, `main`), CodSpeed run 6ab74dffc9fea79936132129; stdev sdk 6.554 / naive 9.371 µs; IQR outliers sdk 8.2 % / naive 14.1 % of 470535 / 469017 rounds; main's run before the K35 fix: 11 `EncodeState` rows lost, `BenchmarkCall` complete; not a K7 run |
| W5.4-02 | 2026-09-26 05:18:11 UTC | W5.4 AC-P7 survey | `ubuntu-26.04` (AMD EPYC 7763) | `go1.27.1 linux/amd64` | not printed by the job | – | f048059's job plus K35 probe steps (f1b1df8, throwaway `wave/p5-codspeedfix-probe`), raw samples on `/tmp` | `BenchmarkCall/sdk` min / median / mean 5.600 / 5.901 / 7.014 µs; `/naive` 4.819 / 5.521 / 7.415 µs; **sdk/naive 1.162 / 1.069 / 0.946** (min / median / mean) | GitHub run 36219879628 (workflow_dispatch, `wave/p5-codspeedfix-probe`), CodSpeed run 6ab75593d47c5a3890bbc64f; stdev sdk 7.067 / naive 11.357 µs; IQR outliers sdk 12.9 % / naive 15.4 % of 464202 / 463863 rounds; 11 `EncodeState` rows lost (W5.1-23), `BenchmarkCall` complete; not a K7 run |
| W5.4-03 | 2026-09-26 05:27:29 UTC | W5.4 AC-P7 survey | `ubuntu-26.04` (AMD EPYC 7763) | `go1.27.1 linux/amd64` | not printed by the job | – | `bench.yaml` at 57bee0f: `CodSpeedHQ/action@v5` (runner 5.2.1, go runner 1.3.0), `mode: walltime`, `go test -bench=. ./...`, raw samples under `$RUNNER_TEMP` | `BenchmarkCall/sdk` min / median / mean 5.570 / 5.891 / 6.926 µs; `/naive` 4.849 / 5.490 / 7.355 µs; **sdk/naive 1.149 / 1.073 / 0.942** (min / median / mean) | GitHub run 36220344551 (workflow_dispatch, `wave/p5-codspeedfix`), CodSpeed run 6ab757c1b5bd728624a336a7; stdev sdk 7.079 / naive 10.225 µs; IQR outliers sdk 10.7 % / naive 17.4 % of 455731 / 480762 rounds; the K35 fix run (W5.1-25); not a K7 run |
| W5.4-04 | 2026-09-26 05:27:41 UTC | W5.4 AC-P7 survey | `ubuntu-26.04` (AMD EPYC 9V74) | `go1.27.1 linux/amd64` | not printed by the job | – | 57bee0f's `bench.yaml` without the `TMPDIR` line (9deffbe, throwaway `wave/p5-codspeedfix-bite`) | `BenchmarkCall/sdk` min / median / mean 5.208 / 5.538 / 6.265 µs; `/naive` 4.516 / 5.258 / 6.685 µs; **sdk/naive 1.153 / 1.053 / 0.937** (min / median / mean) | GitHub run 36220346065 (workflow_dispatch, `wave/p5-codspeedfix-bite`), CodSpeed run 6ab757cdbffa2b9b6c44cd57; stdev sdk 5.990 / naive 8.665 µs; IQR outliers sdk 7.7 % / naive 13.3 % of 513872 / 497278 rounds; the K35 guard's bite run (W5.1-26); not a K7 run |
| W5.4-05 | 2026-09-26 05:29:02 UTC | W5.4 AC-P7 survey | `ubuntu-26.04` (AMD EPYC 7763) | `go1.27.1 linux/amd64` | not printed by the job | – | `bench.yaml` at 3ffe77b: `CodSpeedHQ/action@v5` (runner 5.2.1, go runner 1.3.0), `mode: walltime`, `go test -bench=. ./...`, raw samples on `/tmp` | `BenchmarkCall/sdk` min / median / mean 5.450 / 5.781 / 6.685 µs; `/naive` 4.849 / 5.491 / 7.130 µs; **sdk/naive 1.124 / 1.053 / 0.938** (min / median / mean) | GitHub run 36220416172 (push, `main`), CodSpeed run 6ab7581e0ebd71c0902eb24e; stdev sdk 6.853 / naive 10.167 µs; IQR outliers sdk 8.3 % / naive 14.1 % of 479386 / 474650 rounds; 11 `EncodeState` rows lost; not a K7 run |
| W5.4-06 | 2026-09-26 05:44:59 UTC | W5.4 AC-P7 survey | `ubuntu-26.04` (AMD EPYC 7763) | `go1.27.1 linux/amd64` | not printed by the job | – | `bench.yaml` at aaa9698: `CodSpeedHQ/action@v5` (runner 5.2.1, go runner 1.3.0), `mode: walltime`, `go test -bench=. ./...`, raw samples under `$RUNNER_TEMP` | `BenchmarkCall/sdk` min / median / mean 5.410 / 5.821 / 7.014 µs; `/naive` 4.779 / 5.471 / 7.133 µs; **sdk/naive 1.132 / 1.064 / 0.983** (min / median / mean) | GitHub run 36221215525 (workflow_dispatch, `wave/p5-codspeedfix`), CodSpeed run 6ab75bdbb5bd728624a3372a; stdev sdk 7.003 / naive 10.677 µs; IQR outliers sdk 13.0 % / naive 13.6 % of 473862 / 483445 rounds; the K35 landing SHA's dispatch (D-K35-rebase); not a K7 run |
| W5.4-07 | 2026-09-26 05:57:38 UTC | W5.4 AC-P7 survey | `ubuntu-26.04` (AMD EPYC 7763) | `go1.27.1 linux/amd64` | not printed by the job | – | `bench.yaml` at aaa9698: `CodSpeedHQ/action@v5` (runner 5.2.1, go runner 1.3.0), `mode: walltime`, `go test -bench=. ./...`, raw samples under `$RUNNER_TEMP` | `BenchmarkCall/sdk` min / median / mean 5.450 / 5.791 / 6.684 µs; `/naive` 4.758 / 5.680 / 7.625 µs; **sdk/naive 1.145 / 1.020 / 0.877** (min / median / mean) | GitHub run 36221839206 (push, `main`), CodSpeed run 6ab75ed2e412c1cc664ef2d7; stdev sdk 6.843 / naive 11.180 µs; IQR outliers sdk 8.6 % / naive 17.3 % of 479046 / 467010 rounds; **K7 run 1**: the count starts here (D-K35-land) |
| W5.4-08 | 2026-09-26 06:09:46 UTC | W5.4 AC-P7, pre-W5.3 dispatch | `ubuntu-26.04` (AMD EPYC 9V45, 4 CPUs) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | – | `bench.yaml` at c15ba0c (`gh workflow run bench.yaml --ref wave/w5.4`): aaa9698's job plus the report step | `BenchmarkCall/sdk` min / median / mean 2.844 / 3.044 / 3.303 µs; `/naive` 2.464 / 2.965 / 3.632 µs; **sdk/naive 1.154 / 1.027 / 0.909** (min / median / mean) | GitHub run 36222408328 (workflow_dispatch, `wave/w5.4`), CodSpeed run 6ab761aa075e817cd5b19ada; stdev sdk 2.346 / naive 4.515 µs; IQR outliers sdk 5.8 % / naive 13.9 % of 929948 / 956420 rounds; W5.4's first dispatch, **pre-W5.3** (history, not AC-P7 of record); guard `125 rows; CodSpeed results: 125 rows in 3 files`; the report step's table (`results/report-36222408328.md`) equals CodSpeed's stored statistics to the ns; not a K7 run |
| W5.4-09 | 2026-09-26 15:04:12 JST | W5.4 discovery | (M), and `ubuntu-26.04` for the CodSpeed side | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 8.50 before and after the 1x run | `GOEXPERIMENT=nosimd,noruntimesecret go test -list 'Benchmark.*' ./...` by package; the guard's own `go test -run '^$' -bench . -benchtime=1x -cpu=1 ./...` expansion; against the `.benchmarks[].uri` rows that W5.4-08's report step lists | functions: 15 in 3 packages (6 root, 5 `internal/benchmark`, 4 `internal/codec`; `BenchmarkLoopback` in two), and the CodSpeed run's 125 URIs reduce to the same 15: **equal**. Rows: 125 local = 125 in CodSpeed: **equal**. Names across runs: `compare_runs` of aaa9698's main run (W5.4-07) with W5.4-08 compares all 125 rows, with no new and no missing ones | `results/list-functions-M.txt`, `results/list-rows-M.txt`, `results/codspeed-uris-36222408328.txt`; CodSpeed's 19 "skipped" rows are URIs from before G5 (14 in `bench_prepare_test.go`, 3 in `bench_config_test.go`, `bench_noop_test.go::BenchmarkNoop`, `bench_call_test.go::BenchmarkCall::sdk`); not a timing row |
| W5.4-10 | 2026-09-26 15:12:57 JST | W5.4 K7 count | – | – | – | – | `gh run list --workflow bench.yaml --branch main --event push --limit 100`, counting successful runs from 36221839206 on, per CPU model (ruling R109) | **EPYC 7763: 1 of 20 runs** (36221839206, aaa9698, CodSpeed run 6ab75ed2e412c1cc664ef2d7); no other model has a counted run. Beside it, the spread of the sdk/naive mean ratio over the counted runs of any model: n/a with one run (no threshold set, R109). The 45 earlier main runs are not counted (D-K35-land: from ca226bb to 3ffe77b 11 rows were lost, and before ca226bb the names differ, G5) | `results/k7-count.txt`; not a timing row |
| W5.4-11 | 2026-09-26 06:23:48 UTC | W5.4 K7 data point | `ubuntu-26.04` (AMD EPYC 9V74) | `go1.27.1 linux/amd64` | not printed by the job | – | `bench.yaml` at 7778166 (main's push run, the R103 revert; the K35 job without W5.4's report step) | `BenchmarkCall/sdk` min / median / mean 5.097 / 5.468 / 6.204 µs; `/naive` 4.457 / 5.358 / 7.008 µs; **sdk/naive 1.144 / 1.021 / 0.885** (min / median / mean) | GitHub run 36223111400 (push, `main`), CodSpeed run 6ab764f4658c3a183213aa0e, K35 guard green; CPU model from `compare_runs` against W5.4-07 (7763 → 9V74); stdev sdk 7.160 / naive 9.603 µs; IQR outliers sdk 7.8 % / naive 17.9 % of 492837 / 531248 rounds; **K7 run 1 on the EPYC 9V74** (R109); CodSpeed's comparison with W5.4-07 marks 48 rows improved and 3 regressed, all across the CPU change |
| W5.4-12 | 2026-09-26 15:25:33 JST | W5.4 K7 count | – | – | – | – | the W5.4-10 command again, per CPU model (R109) | **EPYC 7763: 1 of 20** (36221839206); **EPYC 9V74: 1 of 20** (36223111400); the spread of `BenchmarkCall/sdk`'s mean is n/a on each model (one run each). Beside it, the sdk/naive mean ratio over the two counted runs, any model: 0.877 and 0.885, spread 0.98 % (no threshold set, R109) | `results/k7-count.txt` (second snapshot); not a timing row |
| W5.4-13 | 2026-09-26 06:57:40 UTC | W5.4 K7 data point | `ubuntu-26.04` (AMD EPYC 7763) | `go1.27.1 linux/amd64` | not printed by the job | – | `bench.yaml` at 9c61db9 (main's push run, W4.2's landing; the K35 job without W5.4's report step) | `BenchmarkCall/sdk` min / median / mean 5.430 / 5.821 / 6.771 µs; `/naive` 4.809 / 5.771 / 7.760 µs; **sdk/naive 1.129 / 1.009 / 0.872** (min / median / mean) | GitHub run 36224817149 (push, `main`), CodSpeed run 6ab76ce42eddcdb9847b5adb, K35 guard green; CPU model: `compare_runs` against W5.4-07 lists no environment difference; stdev sdk 7.686 / naive 11.518 µs; IQR outliers sdk 8.5 % / naive 17.5 % of 464564 / 474090 rounds; **K7 run 2 on the EPYC 7763** (R109); the same comparison marks `EncodeBody/rawjson/{64KiB,1MiB}/naive-json` regressed by 11.2 and 10.7 %, the rows that W5.4-05 and -07 had 13 to 14 % faster, on the same CPU model with no change to their code |
| W5.4-14 | 2026-09-26 15:59:15 JST | W5.4 K7 count | – | – | – | – | the W5.4-10 command again, per CPU model (R109) | **EPYC 7763: 2 of 20** (36221839206, 36224817149), `BenchmarkCall/sdk` mean 6.684 and 6.771 µs, spread 1.29 %; **EPYC 9V74: 1 of 20** (36223111400). Beside it, the sdk/naive mean ratio over the three counted runs, any model: 0.877, 0.885, 0.872, spread 1.46 % (no threshold set, R109) | `results/k7-count.txt` (third snapshot); not a timing row |
| W5.4-15 | 2026-09-26 07:18:53 UTC | W5.4 K7 data point | `ubuntu-26.04` (AMD EPYC 7763) | `go1.27.1 linux/amd64` | not printed by the job | – | `bench.yaml` at a4cbb5d (main's push run, W4.3's landing: documents and `_spikes/w4.3` only; the K35 job without W5.4's report step) | `BenchmarkCall/sdk` min / median / mean 5.450 / 5.781 / 6.739 µs; `/naive` 4.789 / 5.540 / 7.511 µs; **sdk/naive 1.138 / 1.044 / 0.897** (min / median / mean) | GitHub run 36225881915 (push, `main`), CodSpeed run 6ab771dd1f99e1706f368c69, K35 guard green; CPU model: `compare_runs` against W5.4-13 lists no environment difference and marks all 125 rows unchanged; stdev sdk 7.331 / naive 10.558 µs; IQR outliers sdk 9.1 % / naive 19.4 % of 449950 / 480398 rounds; **K7 run 3 on the EPYC 7763** (R109) |
| W5.4-16 | 2026-09-26 16:20:22 JST | W5.4 K7 count | – | – | – | – | the W5.4-10 command again, per CPU model (R109) | **EPYC 7763: 3 of 20** (36221839206, 36224817149, 36225881915), `BenchmarkCall/sdk` mean 6.684, 6.771 and 6.739 µs, spread 1.29 %; **EPYC 9V74: 1 of 20** (36223111400). Beside it, the sdk/naive mean ratio over the four counted runs, any model: 0.877, 0.885, 0.872, 0.897, spread 2.83 % (no threshold set, R109) | `results/k7-count.txt` (fourth snapshot); not a timing row |
| W5.4-17 | 2026-09-26 16:41:07 JST | W5.4 report step fails on missing data (Phase 4 critic m-8) | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | the report step's script (`yq e '.jobs.codspeed.steps[-1].run' .github/workflows/bench.yaml`) run by bash 5.3 with `RUNNER_TEMP` set to one directory per case and `GOEXPERIMENT=nosimd,noruntimesecret`; no `/tmp/profile.*.out` existed | no results file: `::error title=AC-P7 report::no CodSpeed results file …`, **exit 1**; a real go-runner results file (the local `codspeed run --skip-upload -m walltime` of `BenchmarkCall` at 14:55 JST): the table and the URI list, **exit 0**; the same file with the `BenchmarkCall::naive` row deleted: the table shows `–`, then `::error title=AC-P7 report::no BenchmarkCall row in the results for: naive`, **exit 1**; a truncated JSON file: `jq: parse error`, **exit 5** | `results/report-step-check-M.txt`; the step compares no value (report-only under K7) and fails only on missing or unreadable data; the numbers of the (M) file are a 100 ms throwaway run, not a measurement; not a timing row |
| W5.4-18 | 2026-09-26 08:02:25 UTC | W5.4 K7 data point | `ubuntu-26.04` (AMD EPYC 9V74 with AVX-512) | `go1.27.1 linux/amd64` | not printed by the job | – | `bench.yaml` at 67dcbb0 (main's push run, W5.2's landing; the K35 job without W5.4's report step) | `BenchmarkCall/sdk` min / median / mean 3.945 / 4.266 / 4.809 µs; `/naive` 3.465 / 4.086 / 4.955 µs; **sdk/naive 1.139 / 1.044 / 0.970** (min / median / mean) | GitHub run 36228091342 (push, `main`), CodSpeed run 6ab77c112eddcdb9847b5b7a, K35 guard `125 rows; CodSpeed results: 125 rows in 3 files`; `compare_runs` against W5.4-11 (the same model name, AMD EPYC 9V74 80-Core Processor) lists no model change, only **CPU flags added: `avx512f`, `avx512bw`, `avx512cd`, `avx512dq`, `avx512vl`, `avx512ifma`, `avx512vbmi`, `avx512_vbmi2`, `avx512_vnni`, `avx512_bitalg`, `avx512_vpopcntdq`, `avx512_bf16`, `gfni`, `xtopology`**, and 123 of 125 rows 15 to 50 % faster, the naive rows as much as the SDK's (`BenchmarkCall/sdk` mean 6.204 → 4.809 µs); stdev sdk 3.913 / naive 5.548 µs; IQR outliers sdk 8.7 % / naive 11.3 % of 664293 / 723963 rounds; the mean ratio 0.970 is the smallest margin so far; K7: see W5.4-19 |
| W5.4-19 | 2026-09-26 17:06:21 JST | W5.4 K7 count | – | – | – | – | the W5.4-10 command again | **Per CPU model (R109 as written): EPYC 7763 3 of 20**, spread 1.29 %; **EPYC 9V74 2 of 20, spread 29.01 %** (6.204 and 4.809 µs: one VM without and one with AVX-512). **Per model and AVX-512 exposure (ruling R109b): EPYC 7763 3 of 20** (1.29 %); **9V74 1**; **9V74 + AVX-512 1**; the 9V45 and the Intel Xeon 6973P-C, both with AVX-512 (`compare_runs` against 7763 runs), have appeared on wave-branch dispatches only. Beside it, the sdk/naive mean ratio over the five counted runs, any host: 0.877, 0.885, 0.872, 0.897, 0.970, spread 11.22 % (no threshold set, R109) | `results/k7-count.txt` (fifth snapshot), `render.py` prints both groupings; not a timing row |
| W5.4-20 | 2026-09-26 17:06:07 JST | W5.4 report step names AVX-512 exposure | (M), and (L) for the host line | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | – | W5.4-17's four cases against the step with its new line (`AVX-512 yes/no` from `avx512f` in the first `flags` line of `/proc/cpuinfo`, and a 12-hex SHA-256 digest of the sorted flags); the line's commands alone by `bash -s` over ssh on (L) | (M): no results file exit 1, real file exit 0, no naive row exit 1, truncated JSON exit 5, as before; darwin has no `/proc/cpuinfo`, so the line reads `AVX-512 no` without failing. (L): `- CPU: Intel(R) Xeon(R) Platinum 8481C CPU @ 2.70GHz, 44 CPUs, AVX-512 yes, flags digest e8bd8076a11e` from 112 flags, exit 0 | `results/report-step-check-M.txt` (second run); the host key W5.4-18 needs from every run on; not a timing row |
| W5.4-21 | 2026-09-26 06:25:17 UTC | W5.4 AC-P7, pre-W5.3 dispatch | `ubuntu-26.04` (Intel Xeon 6973P-C, AVX-512, 4 CPUs) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | – | `bench.yaml` at 419d12e (`gh workflow run bench.yaml --ref wave/w5.4`) | `BenchmarkCall/sdk` min / median / mean 3.328 / 3.651 / 4.079 µs; `/naive` 2.884 / 3.486 / 4.521 µs; **sdk/naive 1.154 / 1.047 / 0.902** (min / median / mean); q20 1.262 / 1.177 / 0.985 | GitHub run 36223193685 (success), CodSpeed run 6ab7654d2eddcdb9847b5a4a; the report step's first run in µs (`results/report-36223193685.md`); guard `125 rows; CodSpeed results: 125 rows in 3 files`; uploaded names identical to W5.4-08's; AVX-512 from `compare_runs` against the 7763 run of W5.4-22 (flags added: `avx512f` and 20 more, `amx_*`); on this host `EncodeState/ascii/6MiB/{encode,check}` are 34.6 and 32.6 % slower than on the 7763 while 115 rows are faster (finding 2); not a K7 run |
| W5.4-22 | 2026-09-26 07:52:12 UTC | W5.4 AC-P7, pre-W5.3 dispatch | `ubuntu-26.04` (AMD EPYC 7763, no AVX-512, 4 CPUs) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | – | `bench.yaml` at 5a4a4d9 (`gh workflow run bench.yaml --ref wave/w5.4`): the report step fails on missing data (m-8) | `BenchmarkCall/sdk` min / median / mean 5.420 / 5.701 / 6.546 µs; `/naive` 4.749 / 5.460 / 7.368 µs; **sdk/naive 1.141 / 1.044 / 0.889** (min / median / mean; 0.888 from the ns-rounded means in the TSV); q20 1.223 / 1.168 / 0.992 | GitHub run 36227580459 (success in every step: the m-8 check passed on CI), CodSpeed run 6ab779ac2eddcdb9847b5b56; `results/report-36227580459.md`; guard 125 = 125; uploaded names identical to W5.4-08's; not a K7 run |
| W5.4-23 | 2026-09-26 08:17:54 UTC | W5.4 AC-P7, pre-W5.3 dispatch | `ubuntu-26.04` (Intel Xeon Platinum 8573C, AVX-512, 4 CPUs) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | – | `bench.yaml` at 039179d (`gh workflow run bench.yaml --ref wave/w5.4`): the report step names AVX-512 exposure (R109b) | `BenchmarkCall/sdk` min / median / mean 4.884 / 5.353 / 6.275 µs; `/naive` 4.243 / 5.143 / 6.535 µs; **sdk/naive 1.151 / 1.041 / 0.960** (min / median / mean); q20 1.260 / 1.182 / 0.963 | GitHub run 36228850305 (success in every step), CodSpeed run 6ab77fad9e6b21a34d50e9cf (its check run's time is the When); the host line on CI: `- CPU: INTEL(R) XEON(R) PLATINUM 8573C, 4 CPUs, AVX-512 yes, flags digest e161990560a5`, a fifth CPU model; `results/report-36228850305.md`; guard 125 = 125; uploaded names identical to W5.4-08's; not a K7 run. The statistics of W5.4-21 to -23 come from the report step's tables because the CodSpeed MCP server needed a new sign-in when they were recorded (W5.4-08 showed the two sources equal to the ns) |

### Tables

| Commit | Event | GitHub run | CodSpeed run | CPU | sdk/naive min | median | mean | sdk min µs | median µs | mean µs |
| --- | --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| f048059 | push main | 36218293900 | 6ab74dffc9fea79936132129 | 7763 | 1.146 | 1.076 | 0.954 | 5.570 | 5.941 | 6.816 |
| f1b1df8 | workflow_dispatch wave/p5-codspeedfix-probe | 36219879628 | 6ab75593d47c5a3890bbc64f | 7763 | 1.162 | 1.069 | 0.946 | 5.600 | 5.901 | 7.014 |
| 57bee0f | workflow_dispatch wave/p5-codspeedfix | 36220344551 | 6ab757c1b5bd728624a336a7 | 7763 | 1.149 | 1.073 | 0.942 | 5.570 | 5.891 | 6.926 |
| 9deffbe | workflow_dispatch wave/p5-codspeedfix-bite | 36220346065 | 6ab757cdbffa2b9b6c44cd57 | 9V74 | 1.153 | 1.053 | 0.937 | 5.208 | 5.538 | 6.265 |
| 3ffe77b | push main | 36220416172 | 6ab7581e0ebd71c0902eb24e | 7763 | 1.124 | 1.053 | 0.938 | 5.450 | 5.781 | 6.685 |
| aaa9698 | workflow_dispatch wave/p5-codspeedfix | 36221215525 | 6ab75bdbb5bd728624a3372a | 7763 | 1.132 | 1.064 | 0.983 | 5.410 | 5.821 | 7.014 |
| aaa9698 | push main | 36221839206 | 6ab75ed2e412c1cc664ef2d7 | 7763 | 1.145 | 1.020 | 0.877 | 5.450 | 5.791 | 6.684 |
| c15ba0c | workflow_dispatch wave/w5.4 | 36222408328 | 6ab761aa075e817cd5b19ada | 9V45 + AVX-512 | 1.154 | 1.027 | 0.909 | 2.844 | 3.044 | 3.303 |
| 7778166 | push main | 36223111400 | 6ab764f4658c3a183213aa0e | 9V74 | 1.144 | 1.021 | 0.885 | 5.097 | 5.468 | 6.204 |
| 9c61db9 | push main | 36224817149 | 6ab76ce42eddcdb9847b5adb | 7763 | 1.129 | 1.009 | 0.872 | 5.430 | 5.821 | 6.771 |
| a4cbb5d | push main | 36225881915 | 6ab771dd1f99e1706f368c69 | 7763 | 1.138 | 1.044 | 0.897 | 5.450 | 5.781 | 6.739 |
| 67dcbb0 | push main | 36228091342 | 6ab77c112eddcdb9847b5b7a | 9V74 + AVX-512 | 1.139 | 1.044 | 0.970 | 3.945 | 4.266 | 4.809 |
| 419d12e | workflow_dispatch wave/w5.4 | 36223193685 | 6ab7654d2eddcdb9847b5a4a | Intel Xeon 6973P-C + AVX-512 | 1.154 | 1.047 | 0.902 | 3.328 | 3.651 | 4.079 |
| 5a4a4d9 | workflow_dispatch wave/w5.4 | 36227580459 | 6ab779ac2eddcdb9847b5b56 | 7763 | 1.141 | 1.044 | 0.888 | 5.420 | 5.701 | 6.546 |
| 039179d | workflow_dispatch wave/w5.4 | 36228850305 | 6ab77fad9e6b21a34d50e9cf | Intel Xeon Platinum 8573C + AVX-512 | 1.151 | 1.041 | 0.960 | 4.884 | 5.353 | 6.275 |

K7, successful push runs on main from 36221839206 on; blocking needs 20 runs in one group
with BenchmarkCall/sdk's mean within 5 %.
Per CPU model (ruling R109 as written):
- AMD EPYC 7763: 3 of 20 runs; BenchmarkCall/sdk mean spread 1.29 % over 3 runs
- AMD EPYC 9V74: 2 of 20 runs; BenchmarkCall/sdk mean spread 29.01 % over 2 runs
Per CPU model and AVX-512 exposure (ruling R109b):
- AMD EPYC 7763: 3 of 20 runs; BenchmarkCall/sdk mean spread 1.29 % over 3 runs
- AMD EPYC 9V74: 1 of 20 runs; BenchmarkCall/sdk mean spread n/a (1 run)
- AMD EPYC 9V74 + AVX-512: 1 of 20 runs; BenchmarkCall/sdk mean spread n/a (1 run)
Beside it, the sdk/naive mean ratio over those runs, any host (no threshold set): 11.22 % over 5 runs

Every run above, K7 or not, for comparison:
- BenchmarkCall/sdk mean: 112.33 % over 15 runs (AMD EPYC 7763: 7.15 % over 9 runs; AMD EPYC 9V45 + AVX-512: n/a (1 run); AMD EPYC 9V74: 0.99 % over 2 runs; AMD EPYC 9V74 + AVX-512: n/a (1 run); Intel Xeon 6973P-C + AVX-512: n/a (1 run); Intel Xeon Platinum 8573C + AVX-512: n/a (1 run))
- sdk/naive mean ratio: 12.70 % over 15 runs (AMD EPYC 7763: 12.70 % over 9 runs; AMD EPYC 9V45 + AVX-512: n/a (1 run); AMD EPYC 9V74: 5.87 % over 2 runs; AMD EPYC 9V74 + AVX-512: n/a (1 run); Intel Xeon 6973P-C + AVX-512: n/a (1 run); Intel Xeon Platinum 8573C + AVX-512: n/a (1 run))

### W5.4 findings

1. **AC-P7 holds on the mean and fails on the minimum and the median.** On
   all fifteen runs, `BenchmarkCall/sdk` / `BenchmarkCall/naive` is 0.872 to
   0.983 by mean, 1.009 to 1.076 by median and 1.124 to 1.162 by minimum,
   the value CodSpeed's report shows. CodSpeed's overlay times every
   `b.Loop` iteration on its own (rounds = iterations, one per round).
   One naive call is shorter than one SDK call, by 0.69 µs at W5.4-07's
   minimum. The SDK is ahead only once the cost of collecting the naive
   client's 54 (M) or 68 (L) allocations per call, against the SDK's 22,
   is counted. That cost falls on a share of later iterations, which have
   the naive row's larger stdev (1.43 to 1.92 times the SDK's) and its
   larger IQR-outlier share. The minimum and the median leave those
   iterations out. R108 reads the mean; the gap is risk K36, a best-effort
   W5.3 target (`.omc/handoffs/w5.3-codspeed-note.md`). On W5.4-07's
   flamegraph, `internal/codec.trailing` passes the whole body through
   sonic's `decoder.Skip` again after `ast.Preorder`, to find where the
   root value ends. That is 10.5 % of `BenchmarkCall/sdk`'s time, about
   0.71 µs per call, about the whole gap. **AC-P7, pre-W5.3 dispatch
   (W5.4-08): holds on the mean (0.909 on the 9V45; the 7763 runs give
   0.872 to 0.983), report-only under R108.** The runs of record come
   after the rebase onto W5.3's landing.
2. **The host moves every row, and the model name alone does not name the
   host.** Five model names have appeared: the EPYC 7763 (nine runs), the
   9V74 (W5.4-04, -11, -18), the 9V45 (W5.4-08) and, on the wave branch,
   the Intel Xeon 6973P-C (W5.4-21) and Xeon Platinum 8573C (W5.4-23). On the 9V45, which exposes AVX-512, every row ran
   1.4 to 2.7 times faster than on the 7763, and `BenchmarkCall/sdk`'s mean
   went from 6.68 to 3.30 µs. One model name covers VMs with and without
   AVX-512: the 9V74 of W5.4-18 lists `avx512f` and a dozen related flags
   that W5.4-11's 9V74 does not, and ran 123 of 125 rows 15 to 50 % faster
   (`BenchmarkCall/sdk` mean 6.204 → 4.809 µs). Hosts do not even rank rows
   alike. `compare_runs` of the 7763 run of dispatch 36227580459 (5a4a4d9)
   with the Intel Xeon 6973P-C run of dispatch 36223193685 (419d12e) marks
   115 rows faster, 8 unchanged, and `EncodeState/ascii/6MiB/encode` and
   `/check` 34.6 and 32.6 % slower (408.7 → 624.6 µs, 131.9 → 195.8 µs).
   So no single cross-host baseline can exist. Over these fifteen runs the
   mean's spread is 112 %, and 7.2 % on the 7763 alone. The sdk/naive
   ratio moves less but is not independent of the host: by mean it is
   0.872 to 0.983 on the 7763, 0.909 on the 9V45, 0.970 on the 9V74 with
   AVX-512, and 0.902 and 0.960 on the two Xeons. Taken across hosts, K7 (20 main runs within 5 %) would
   measure which machine each run got. So ruling R109 (ratified by the
   owner as R115) counts K7 per CPU model and never mixes models, and
   records beside it the sdk/naive mean ratio's spread over the counted
   runs of any host, with no threshold set.
   Under R109 as first written the 9V74 already spreads 29 % over two
   runs, so ruling R109b keys K7 on model and AVX-512 exposure, which the
   report step now prints (W5.4-20). W5.4-19 gives the count both ways:
   EPYC 7763 3 of 20 (1.29 %), and the 9V74 without and with AVX-512 1
   each.
3. **Noise on one CPU model exceeds CodSpeed's 10 % threshold.** On the
   7763, the `EncodeBody/rawjson/{64KiB,1MiB}/naive-json` rows moved 13 to
   14 % faster between runs and then 11 % slower again (W5.4-13), with no
   change to their code. CodSpeed's own check
   run failed at 1f694b0, 3ffe77b and aaa9698, each time on a single row:
   B6 `cold-fanout-64` (−11.9 %, −12.2 %) and `rawjson/1MiB/sdk`
   (−14.3 %). The workflow never requires that check. Risk K37 records
   this for the owner. The remedies are CodSpeed project settings: a
   per-benchmark threshold for B6 `cold-fanout-64`, or ignoring that row,
   and archiving the 19 pre-G5 rows. The workflow keeps B6 in the run.
4. **The runner's default build is (L)'s baseline.** W5.4-08's ToolTags
   equal those of the 84 (L) rows in this ledger, `goexperiment.dwarf5`
   included (a linux default that darwin lacks). So the job sets no
   `GOEXPERIMENT`.
