# Typed answers: the cost of `DecodeAs` and `PreparedFor`

This page states what W4.3 measured about the typed path (`PreparedFor`,
`DecodeAs`, `Ask`) and what was decided from it. Every number here is a
row of the [ledger](ledger.md#w43-s-d2-how-the-typed-decode-stores-an-answer-and-preparedfors-first-call)
(W4.3-01 to W4.3-13), measured on (M), darwin/arm64, and (L),
linux/amd64, at d7a5968, whose Go code the branch carries rebased as
6c65b9c. The code is `_spikes/w4.3/`.

## S-D2: reflect or `unsafe` field offsets

Section 6.6 of the port plan keeps `unsafe` field offsets out of
`DecodeAs` unless spike S-D2 shows that the reflect-only store is more
than twice as slow. `DecodeAs` stores each answer through
`v.Field(i).Addr().Interface()`, which moves the decoded `T` to the heap
(AC-P3's one allocation). The spike times replicas of the decode that
differ only in the store, with `DecodeAs` itself beside them:

| Fixture | Host | `DecodeAs` | (1) as built | (2) offsets | (1)/(2) | `DecodeAs`/(2) |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| `result.json` (3 answers) | (M) | 134.3 ns | 122.7 ns | 63.86 ns | 1.92× | 2.10× |
| `result.json` (3 answers) | (L) | 185.7 ns | 173.5 ns | 72.71 ns | 2.39× | 2.55× |
| `result-20.json` | (M) | 923.9 ns | 838.4 ns | 539.7 ns | 1.55× | 1.71× |
| `result-20.json` | (L) | 1.187 µs | 1.117 µs | 619.2 ns | 1.80× | 1.92× |
| 1k structured-legend flood | (M) | 704.9 ns | 693.7 ns | 647.6 ns | 1.07× | 1.09× |
| 1k structured-legend flood | (L) | 1.365 µs | 961.2 ns | 874.3 ns | 1.10× | 1.56× |

Each figure is the minimum of five `b.Loop()` runs. (1) and (2)
allocate 1 and 0 times; `DecodeAs` allocates as (1) does. On the flood on
(L), `DecodeAs` runs 42 % slower than its replica. The two copies of the
level check have the same instructions but sit at different addresses,
so the likely cause is loop placement, not the store (ledger finding 5).

**Verdict: keep reflect (ruling R112). Per section 6.6, no `unsafe`
enters the root package.** Compared like for like, the store as built
against offsets, (1)/(2), is below 2× on (M) on every fixture, so the
bar is not met on both hosts. The rest of the offsets' advantage comes
from escape analysis, not from reflection: offsets let the `T` stay on
the stack. `DecodeAs`'s own 11–12 ns above the replica would stay in an
offsets build, so today's `DecodeAs` would take 1.78× (M) and 2.19× (L)
the time of such a build, again one host under the bar. With `DecodeAs`
itself as (1), the ratio on `result.json` is 2.10× (M) and 2.55× (L); the
table gives both readings. A later wave (W5.3) may remove the `T`'s heap
allocation without `unsafe`: it is an escape, not a cost of reflection.

Most of the difference is the allocation of the `T`, not reflection. A
diagnostic variant, (2) with the `T` forced onto the heap, puts the
allocation at 31 ns (M) and 60 ns (L) for a 144-byte `T`, and the reflect
work at 9–14 ns per field. On `result.json`, offsets would save at most
70 ns (M) and 113 ns (L) per `Ask`. That is 1.4 % and 1.8 % of a q3 call
over the in-process Recorder (5.0 µs and 6.2 µs), and less of a call
over a network. `reflect.Value.Set`, the third variant, allocates once
more per field and is the slowest on every fixture.

## `PreparedFor`'s first call

`PreparedFor[T]` inspects `T` on its first call and caches the result;
later calls allocate nothing. The first call, counted in a fresh process
per run, allocates as follows, identically on both hosts (mallocs/bytes):

| Type | Fields | First call | Same set, builder + `Prepare()` | `Prepare()` alone | Extra per field |
| --- | ---: | ---: | ---: | ---: | ---: |
| one noul | 1 | 8/800 | 5/600 | 3/224 | 3.00 / 200 B |
| section 5 `Ticket` | 4 | 24/4720 | 11/3688 | 5/928 | 3.25 / 258 B |
| ten questions | 10 | 63/20328 | 25/15808 | 13/2888 | 3.80 / 452 B |
| twenty questions | 20 | 114/41704 | 40/32176 | 20/5640 | 3.70 / 476 B |

The reflection overhead is 3 to 4 allocations per field over building
the same set by hand. It comes from the error prefix `planField` builds
for every field (1 each), the tag parser's option and level slices
growing from nil (2 to 4 per choice or score), a label copy per choice,
and the growth of the plan's field list. It is paid once per type per
process. The cache is a `sync.Map`: when the new type's hash collides
with a cached type's in the map's hash trie, the first call costs one
more allocation of 160 B.
