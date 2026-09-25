# Response fixtures

Response bodies for the SDK's decoder, client and allocation tests. Tests load
them with `testsupport.Fixture`, `FixtureString` and `FixtureNames`
(`internal/testsupport/fixtures.go`). `TestFixtureManifest`
(`internal/testsupport/fixtures_test.go`) fails when a file is added or
removed without a manifest row, or loses the property its row states. For a
`malformed-*` file, that property is a single fault: removing it gives a valid
response.

Conventions:

- Every file is the exact body a server would send: compact JSON, no trailing
  newline. `.gitattributes` marks the files `-text`, so git never changes their
  line endings or bytes.
- `malformed-*.json`: the SDK must reject it with `*ResponseValidationError`.
  Python 0.7.1 rejects every one of them.
- `deviation-*.json`: the Go port deliberately differs from Python here
  (plan Appendix B).
- `parity-*.json`: an edge case both accept.
- Every other file is a valid body.
- The "Python 0.7.1" columns are the output of
  `SystemOneResponse.from_http_response` (`ListModelsResponse` for
  `models.json`) in the upstream checkout at `0ffd094`, run with that
  checkout's own `.venv`. Probed 2026-09-25 15:36:15 JST (time from `date`);
  the two `-key` files and the value-only `malformed-invalid-utf8.json` were
  probed 2026-09-25 16:11:04 JST, and their single-fault repairs were
  accepted; `duplicates.json` was probed again 2026-09-25 16:51:41 JST (time
  from `date`), after it gained the escaped `answers` and the `risk` answer.
  `malformed-too-deep.json`, `deviation-nan-unknown.json` and
  `deviation-nan-noul.json` were probed 2026-09-26 01:58:16 JST
  (`_spikes/w2.0/python_paths.py`, output in
  `_spikes/w2.0/results/python-paths.txt`).
  `''` is Python's root path, which Go spells `.` (Appendix B).

## Ported byte-exact

| File | Source | Bytes |
| --- | --- | --- |
| `result.json` | `RESULT`, `tests/test_clients.py:42-56`, serialized with `pydantic_core.to_json` as `test_round_trip` sends it | 364 |
| `models.json` | `{"models": [CARD]}` (`CARD` at `tests/test_clients.py:57`) with `to_json`, as `test_models_shape` (`:246-253`) sends it | 89 |
| `structured-legend.json` | the response body of `test_rich_descriptions`, `tests/test_clients.py:214-223`, serialized by `httpx2.Response(200, json=...)` | 217 |
| `unknown-answer-type.json` | the response body of `test_unknown_answer_type_ignored`, `tests/test_responses.py:158-165`, serialized by `httpx2.Response(200, json=...)` | 145 |
| `score-flood-mini.json` | the Rust port (`zchee/typesafe-sdk-rust` @ `34c3b7c`), `fuzz/corpus/decode_response/score-flood-mini.json`: 8 score answers with an empty legend, 8 with one level | 1495 |

The upstream repository has no JSON files: its tests build these bodies in
Python. The four upstream files above hold the bytes its test transport sends.
To regenerate one from the upstream checkout:

```sh
cd typesafe-sdk-python
.venv/bin/python -c 'import sys; sys.path.insert(0, "."); from pydantic_core import to_json; from tests.test_clients import RESULT; sys.stdout.buffer.write(to_json(RESULT))'
```

Use `to_json({"models": [CARD]})` for `models.json` and
`httpx2.Response(200, json=body).content` for the other two.

The Rust port's `tests/fixtures/` has copies of the same four bodies. They are
identical except for a trailing newline that the Rust port added. Its README
attributes the structured-legend body to `test_extra_body_shallow_override`,
but that test returns `RESULT`; the body comes from `test_rich_descriptions`.

## Authored valid bodies

| File | What it pins | Python 0.7.1 |
| --- | --- | --- |
| `result-20.json` | 20 answers in wire order: 7 noul, 7 choice, 6 score, text legends only. The questions it answers follow from it: a choice's options are the keys of its `probabilities`, and a score's levels are its `legend` values in key order. Serves AC-P2 and the 20-question case of S-C1. | accepted, 20 answers |
| `type-last.json` | `result.json`'s values with every `type` after the value members and the top-level members reversed. It decodes to the same answers as `result.json` (plan 6.2.3). | accepted |
| `escaped-names.json` | one JSON escape in each answer name: `\u00e9`, `\"`, `\\`, `\n`, the surrogate pair `\ud83c\udf0d` and `\/`. Escaped strings cost one allocation each (NF2). | accepted; names `spécial`, `quote"d`, `back\slash`, `new<LF>line`, `globe 🌍`, `sl/ash` |
| `escaped-member-names.json` | escaped member names at every level: `"\u006dodel"`, `"\u0075sage"`, `"\u0061nswers"`, `"\u0074ype"`, `"l\u0065gend"`, the legend key `"\u0030"`, and `"summ\u0061ry"` inside a structured level, which also holds an escaped quote. The names decode to plain ones. The lazy pass's `Raw()` must keep the level's escapes byte-exact (plan 3.3). | accepted, same answers as the unescaped body |
| `structured-legend-flood-1k.json`, `structured-legend-flood-10k.json` | the output of `testsupport.StructuredLegendFlood(1000)` and `(10000)`, 57,418 and 616,421 bytes. Answers are `spam`, `tone` and `flood`, a score answer with 10³ or 10⁴ structured levels (even levels are objects, odd levels are arrays) and as many probabilities. Used for AC-P8. | accepted |
| `no-answers.json` | no `answers` member, so the answer set is empty (plan 6.2.3). | accepted, no answers |
| `parity-big-exp-unknown.json` | `1e400` in an unknown top-level member (plan 6.2.2). | accepted |
| `duplicates.json` | every duplicate rule of plan 6.2.4 in one body, for W2.0 and AC-F12. Top level: `model`, `usage` and `answers` twice each, the last `answers` spelled with an escape (`"\u0061nswers"`), so a decoder that compares raw member names keeps the wrong one; the first `answers` holds `gone`, an invalid `broken` (no `noul`) and `mystery` of the unknown kind `aurora`. Inside the last `answers`: `tone` twice (the first copy is invalid); in `spam`, `type` (`choice`, then `noul`) and `noul` twice; in the second `tone`, `confidence`, `probabilities` and the probability key `friendly` twice; in `quality`, `legend` twice (a structured level, then text levels) and the level key `1` twice; in `risk`, `legend` twice (a text level, then a structured one, which the lazy pass must find inside the escaped `answers`). | accepted, last wins at every level: the result below, no WARN |

Python's result for `duplicates.json`, as a body without repeats
(`duplicatesLastWins` in `fixtures_test.go`). The answers come in the order
`tone`, `spam`, `quality`, `risk`: a repeated name keeps its first position
and takes its last value, as a Python dict does. The escaped `answers` is the
same member as the plain one, so it replaces it. The last `usage` replaces the
first whole, so `output_tokens` is absent (`None`), not 99. `gone`, `broken`
and `mystery` are gone with the superseded `answers`, and Python logs no WARN
for `mystery`. `risk` keeps its structured level, `quality` its text levels.

```json
{"model":"jev-latest","usage":{"input_tokens":12},"answers":{"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},"spam":{"type":"noul","noul":0.98},"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"fine","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}},"risk":{"type":"score","score":0,"confidence":1,"legend":{"0":{"summary":"low"}},"probabilities":{"0":1}}}}
```

To regenerate the flood files after an intended generator change, run
`go test ./internal/testsupport -run TestStructuredLegendFloodFixtures -update`.
Without `-update`, the same test and `TestFixtureManifest` fail when the
files and the generator differ. Under `-update`, `TestFixtureManifest` checks
the generator's output instead of the files, so running the whole package
with `-update` also works.

## Deviations

| File | Body | Python 0.7.1 | Go (plan) |
| --- | --- | --- | --- |
| `deviation-big-exp-noul.json` | `"noul":1e400` | accepted, `noul == inf` | rejected with `*ResponseValidationError`: `strconv.ParseFloat` returns `ErrRange`, and ±Inf could not round-trip (AC-F10) |
| `deviation-lone-surrogate.json` | a lone `\ud800` in a text legend level and inside a structured level | rejected at `''` | accepted: `Answers()` shows U+FFFD in the text level, and the lazy pass `Raw()` keeps `{"note":"\ud800"}` (Appendix B, AC-F7) |
| `deviation-nan-unknown.json` | `NaN`, `Infinity` and `-Infinity` in an unknown member | accepted: pydantic-core's parser takes the three literals (`allow_inf_nan`) | rejected at `.`: JSON has no such literal, sonic's parser refuses them, and the port takes finite floats only, as for `1e400` (ruling R73) |
| `deviation-nan-noul.json` | `"noul":NaN` | accepted, `noul` is `nan` | rejected at `.`, for the same reason |

## Malformed

Every file here must be rejected with `*ResponseValidationError`.

| File | The one fault | Python 0.7.1 `field_path` | Go field path (plan) |
| --- | --- | --- | --- |
| `malformed-empty.json` | zero bytes | `''` | `.` (`decoder.Skip` start < 0) |
| `malformed-whitespace.json` | only JSON whitespace (space, tab, LF, CR) | `''` | `.` |
| `malformed-truncated.json` | the first half of `result.json` | `''` | `.` |
| `malformed-trailing-garbage.json` | `result.json` followed by ` x` | `''` | `.` (trailing-data check) |
| `malformed-trailing-value.json` | `result.json` followed by a second object | `''` | `.` |
| `malformed-trailing-nbsp.json` | `result.json` followed by U+00A0, which is not JSON whitespace | `''` | `.` |
| `malformed-trailing-formfeed.json` | `result.json` followed by `\f`, which is not JSON whitespace | `''` | `.` |
| `malformed-root-array.json` | `result.json` inside `[...]`: the root is not an object | `''` | `.` |
| `malformed-invalid-utf8.json` | byte 0xFF inside a string value: the `choice` of a choice answer (a known member) | `''` | `.` (per-string `utf8.ValidString`) |
| `malformed-invalid-utf8-key.json` | byte 0xFF inside a member name: a key of the same answer's `probabilities` | `''` | `.` (the same check on `OnObjectKey`) |
| `malformed-control-char.json` | a raw U+0001 inside a string value of an unknown member | `''` | `.` (per-string control-character check) |
| `malformed-control-char-key.json` | a raw U+0001 inside a member name of an unknown member | `''` | `.` (the same check on `OnObjectKey`) |
| `malformed-invalid-escape.json` | `"\q"` inside an unknown member | `''` | `.` |
| `malformed-bad-literal.json` | `tru` inside an unknown member | `''` | `.` |
| `malformed-double-comma.json` | `[1,,2]` inside an unknown member | `''` | `.` |
| `malformed-leading-zero.json` | `01` inside an unknown member | `''` | `.` |
| `malformed-trailing-comma.json` | `[1,]` inside an unknown member | `''` | `.` |
| `malformed-big-exp.json` | `usage.input_tokens` is `1e400` | `'usage.input_tokens'` | `usage.input_tokens` |
| `malformed-usage-type.json` | `usage.input_tokens` is the string `"12"` (Python's `Usage` is strict) | `'usage.input_tokens'` | `usage.input_tokens` |
| `malformed-missing-model.json` | no `model` member | `'model'` | `model` |
| `malformed-missing-usage.json` | no `usage` member | `'usage'` | `usage` |
| `malformed-answers-not-object.json` | `answers` is an array | `'answers'` | `answers` |
| `malformed-too-deep.json` | 4096 arrays nested in an unknown member, `meta`: 4097 containers with the root | `''` | `.` (the decoder's depth cap, 4096 containers in all, sonic's own limit) |

The two SDKs cap nesting differently, and the Go port is the lenient one:
the Python SDK refuses a body nested more than 200 arrays deep inside the root
object (201 fails at `''`), while the Go decoder takes up to 4096 containers
in all, the root included, sonic's own limit, and refuses beyond it at `.`
before its traversal recurses further (ruling R73). `malformed-too-deep.json`
sits past both limits.

The faults inside an unknown member sit in a top-level member, `meta`, that a
decoder has no reason to read. They are the reason for plan 6.2.2's rule that
nothing is skipped: sonic's skip path accepts the five syntax faults, and
every sonic path accepts a raw control character in a string value (plan
6.2.2). The invalid-UTF-8 and control-character faults come in pairs, one in
a string value and one in a member name: a decoder that checks only values,
or only names, accepts one file of each pair, and the `malformed-*.json`
loops catch it. A duplicate member is not malformed (last wins, plan 6.2.4;
`duplicates.json`), and neither is an unknown answer kind
(`unknown-answer-type.json`).

Two names differ from the plan's text:

- Plan 6.2.3 calls the missing-member fixtures `missing-model.json` and
  `missing-usage.json`. Here they are `malformed-*`, so the `malformed-*.json`
  loops of S-D1, W2.0 and AC-F7 include them.
- The lone-surrogate body is a `deviation-*` file, not a `malformed-*` one,
  because Go accepts it (Appendix B).
