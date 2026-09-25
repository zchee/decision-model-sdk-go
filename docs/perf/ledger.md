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
(`prepareCases`) are in `bench_prepare_test.go`; `TestAllocPrepare`
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
