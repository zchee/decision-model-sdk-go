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
| W0.4-06 | 2026-09-25 07:26:25 UTC | W0.4 S-T1b / F1 matrix | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0 → 0 | `sh $R '(L)' $LO /tmp/ts-spike/bench.lock l-st1b -count=5 -timeout 900s -run '^TestST1bStreamLimit$' -v ./_spikes/s-t/` | 200 vs 8 strict: ok 1 (1-3), deadline 199 (197-199), accepts 1 (1-1); non-strict: ok 200 (200-200), accepts 21 (18-22), wall 1027.2 ms; strict+token-first: ok 200 (200-200), accepts 1 (1-1), wall 271.5 ms | [F1 (L)](#f1-results-l); `results/l-st1b.txt` |
| W0.4-07 | 2026-09-25 07:27:26 UTC | W0.4 S-T1b stall rate | (L) | `go1.27.1 linux/amd64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.dwarf5 goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc amd64.v1]` | 0 → 0 | `sh $R '(L)' $LO /tmp/ts-spike/bench.lock l-st1b-stall -count=1 -timeout 900s -run '^TestST1bStallRate$' -v ./_spikes/s-t/` | reps (of 20) with a missed 500 ms deadline (refused streams in total): strict+token 0 (refused 0) and 0 (refused 0); strict+token-first 0 (refused 0) and 0 (refused 0); non-strict+limiter100 13 (refused 531) and 17 (refused 251) (64 vs 4, 200 vs 8) | `results/l-st1b-stall.txt` |
| W0.4-08 | 2026-09-25 16:26:07 JST | W0.4 S-T1 failure cases | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 9.41 → 11.49 | `GOEXPERIMENT=nosimd,noruntimesecret sh $R '(M)' $MO none m-st1-failures -count=5 -run '^TestST1(LeaderCancelled\|LeaderVanish\|TLSSilent\|WaiterBound)$' -v ./_spikes/s-t/` | identical in 5/5 runs: leader cancelled → waiters 63/63 200, handovers 1, accepts 1; leader vanish → gate cold, next caller leads, accepts 1; TLS-silent (500 ms) → leader `*DialError{Timeout}` after 501.331 ms (median), 64 distinct error values, 63 waiters share the leader's cause, gate cold, accepts 1; no gate → 6 serial dials, classes map[deadline:3 other:5]; waiter bound → 63 fall-throughs, 64/64 200, accepts 1 | classification; `results/m-st1-failures.txt` |
| W0.4-09 | 2026-09-25 16:26:26 JST | W0.4 S-T1 HTTPAuto (K20) | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 11.49 → 11.49 | `GOEXPERIMENT=nosimd,noruntimesecret sh $R '(M)' $MO none m-st1-auto -count=5 -run '^TestST1Auto$' -v ./_spikes/s-t/` | gate: accepts 1 in 50/50; no gate: accepts min 51 / median 64 / max 64 over 50 bursts, of which 1 connection carries every request | connection counts; `results/m-st1-auto.txt` |
| W0.4-10 | 2026-09-25 16:26:27 JST | W0.4 S-T2 to S-T5b | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 11.49 → 11.49 | `GOEXPERIMENT=nosimd,noruntimesecret sh $R '(M)' $MO none m-st2-5 -count=1 -timeout 900s -run '^TestST[2345]' -v ./_spikes/s-t/` | S-T2: cold accepts 1, warm 1, after 31 s 1, after 91 s idle 2; idle-close race: close 10 `unexpected EOF`, 90 ok; reset 8 `read tcp 127.0.0.1:P->127.0.0.1:P: read: connection reset by peer`, 2 `write tcp 127.0.0.1:P->127.0.0.1:P: write: broken pipe`, 90 ok; GOAWAY+close 2 `unexpected EOF`, 98 ok | classification; `results/m-st2-5.txt` |
| W0.4-11 | 2026-09-25 16:26:28 JST | W0.4 S-T2 F1 on fake time ×20 | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 11.49 → 11.49 | `GOEXPERIMENT=nosimd,noruntimesecret sh $R '(M)' $MO none m-st2-f1-rate -count=20 -run '^TestST2F1$' -v ./_spikes/s-t/` | 16 calls vs Go's server limit 4, 2 s deadline, 20 runs: strict: 0/20 all ok, 20/20 with deadlines, 1/20 with a `PROTOCOL_ERROR`, connections 1-2; nonstrict: 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 3-4; strict+token: 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 1; strict+token-first: 20/20 all ok, 0/20 with deadlines, 0/20 with a `PROTOCOL_ERROR`, connections 1 | classification under synctest; `results/m-st2-f1-rate.txt` |
| W0.4-12 | 2026-09-25 16:26:49 JST | W0.4 S-T2 synctest refusals | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 11.11 → 11.11 | `ST2_REFUSALS=1 GOEXPERIMENT=nosimd,noruntimesecret sh $R '(M)' $MO none m-st2-refusal-pings-off -count=1 -timeout 20s -run '^TestST2Refusals$/^strict_stall_without_a_deadline/pings=false$' -v ./_spikes/s-t/`, the same with `^loopback_socket_inside_a_bubble$` into `m-st2-refusal-real-socket` and, locked, `pings=true` into `m-st2-refusal-pings-on` (W0.4-13's lock) | pings on: `panic: test timed out after 20s` (highest goroutine id 9724711); pings off: `panic: deadlock: all goroutines in bubble are blocked`, test FAIL; real socket in a bubble: status 200 | fail by design, env-gated; `results/m-st2-refusal-*.txt` |
| W0.4-13 | 2026-09-25 16:27:32 JST | W0.4 S-T2 refusal, pings on | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 10.45 → 13.21 | `ST2_REFUSALS=1 GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-st2-refusal-pings-on -count=1 -timeout 20s -run '^TestST2Refusals$/^strict_stall_without_a_deadline/pings=true$' -v ./_spikes/s-t/` | see W0.4-12 (a 20 s CPU spin, so it takes the lock) | `results/m-st2-refusal-pings-on.txt` |
| W0.4-14 | 2026-09-25 16:27:53 JST | W0.4 S-T1 cold 64 fan-out | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 13.21 → 13.21 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-st1-fanout -count=5 -run '^TestST1FanOut$' -v ./_spikes/s-t/` | gate: 1 in 50/50 one-connection bursts, warm 0 in 50/50, ordering 10/10 in 5/5 runs, waiter wire p50 1.146 (0.996-1.513) / p99 1.898 (1.764-2.664) ms; no gate: 1 in 50/50, all-caller wire p50 1.215 (0.981-1.349) / p99 1.984 (1.823-2.861) ms; gate+token: 1 in 50/50, waiter wire p50 1.276 (1.056-1.478) / p99 2.344 (1.688-3.6) ms | [S-T1](#s-t1-cold-start-gate); `results/m-st1-fanout.txt` |
| W0.4-15 | 2026-09-25 16:27:54 JST | W0.4 S-T1b / F1 matrix | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 13.21 → 40.23, noisy | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-st1b -count=5 -timeout 900s -run '^TestST1bStreamLimit$' -v ./_spikes/s-t/` | 200 vs 8 strict: ok 8 (1-8), deadline 192 (192-199), accepts 1 (1-1); non-strict: ok 200 (200-200), accepts 22 (21-24), wall 50.5 ms; strict+token-first: ok 200 (200-200), accepts 1 (1-1), wall 291.8 ms | [F1 (M)](#f1-results-m); `results/m-st1b.txt` |
| W0.4-16 | 2026-09-25 16:33:53 JST | W0.4 S-T1b stall rate | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 23.68 → 22.91, noisy | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-st1b-stall -count=1 -timeout 900s -run '^TestST1bStallRate$' -v ./_spikes/s-t/` | reps (of 20) with a missed 500 ms deadline (refused streams in total): strict+token 5 (refused 109) and 7 (refused 294); strict+token-first 0 (refused 2) and 0 (refused 1); non-strict+limiter100 3 (refused 2702) and 15 (refused 1550) (64 vs 4, 200 vs 8) | `results/m-st1b-stall.txt` |
| W0.4-17 | 2026-09-25 16:38:21 JST | W0.4 spike package under -race | (M) | `go1.27.1 darwin/arm64` | `[goexperiment.regabiwrappers goexperiment.regabiargs goexperiment.jsonv2 goexperiment.greenteagc goexperiment.randomizedheapbase64 goexperiment.sizespecializedmalloc arm64.v8.0]` | 15.73 → 11.96 | `GOEXPERIMENT=nosimd,noruntimesecret FLOCK=/opt/homebrew/opt/util-linux/bin/flock MAXLOAD=16 sh $R '(M)' $MO $SP/bench.lock m-race -race -count=1 -timeout 900s ./_spikes/s-t/` | `ok` in 47.288s | not a measurement; locked so it cannot overlap one; `results/m-race.txt` |

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
`httptrace` has no SETTINGS hook. The live API advertises 1024 (§3.1):
against 1024, and against a server that advertises nothing, 200 cold calls
complete on one connection in strict mode (200vs1024/strict, 200vs0/strict),
so F1 reaches the live API only above 1024 concurrent calls per connection.

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
