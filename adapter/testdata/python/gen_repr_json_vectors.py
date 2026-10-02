#!/usr/bin/env -S uv run --script
# /// script
# requires-python = "==3.14.3"
# dependencies = [
#   "annotated-types==0.8.0",
#   "anyio==4.14.2",
#   "h11==0.16.0",
#   "httpcore2==2.13.0",
#   "httpx2==2.13.0",
#   "idna==3.18",
#   "pydantic==2.13.4",
#   "pydantic-core==2.46.4",
#   "system-one-adapter==0.2.1",
#   "tenacity==9.1.4",
#   "truststore==0.10.4",
#   "typesafe-sdk==0.7.0",
#   "typing-extensions==4.16.0",
#   "typing-inspection==0.4.2",
# ]
# ///
"""Write the response body's spelling of score criteria as a table for the Go writer.

A score answer's ``legend`` holds the question's criteria
(``_client.py:147`` of system-one-adapter 0.2.1). Each row of the table is one
criterion: the request text ``{"q":{"type":"score","criteria":[<criterion>,"x"]}}``
goes through ``json.loads``, upstream's question validation
(``convert_question_collection_to_validated_api_question_models``), upstream's
answer conversion (``_convert_llm_value_to_typesafe_answer``) and the
response model's ``model_dump(mode="json")``, and the legend's entry ``"0"`` is
written by ``json.dumps`` with ``ensure_ascii=False`` and compact separators:
the spelling of the response body.

The output is JSON Lines, in ASCII:

- line 1, the header: the versions and the format (its first member is
  ``format``);
- a row (first member ``case``): ``criterion``, the criterion's JSON text, and
  either ``legend``, the text ``json.dumps`` writes, or ``refused``, the stage
  that raised and the exception's class, as ``<stage>:<class>``. The stages are
  ``loads`` (``json.loads``), ``validation`` (the question validation) and
  ``convert`` (the answer conversion, which dumps the question).

A row has a ``class`` member when upstream and a reader of the request that
keeps upstream's validation limits are expected to differ. The class comes from
how the case is built, never from the text, and the script stops when a row
does not show the behaviour its case was built for:

- ``convert-depth``: the criterion is nested 255 levels deep, which the
  question validation accepts; after the model call the answer conversion
  raises ``PydanticSerializationError``, so upstream returns neither an
  answer nor a typed error.

A criterion with an escaped surrogate without its partner is left out: a
strict reader of JSON in UTF-8 refuses the request text before any question is
read, and the table of state vectors already holds that class.

The random part has a fixed seed, so the same interpreter and the same package
versions give the same bytes. The script refuses to run on other versions, or on
a ``system_one_adapter`` whose sources differ from the release.

Usage::

    uv run testdata/python/gen_repr_json_vectors.py            # write the table
    uv run testdata/python/gen_repr_json_vectors.py --check    # compare, no write
"""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import random
import struct
import sys
from collections.abc import Iterator, Mapping
from dataclasses import dataclass
from importlib import metadata
from pathlib import Path
from typing import TypeAlias

import pydantic
import pydantic_core
import system_one_adapter
from system_one_adapter._client import _convert_llm_value_to_typesafe_answer
from system_one_adapter._response import SystemOneResponse, Usage
from system_one_adapter._schema import (
    convert_question_collection_to_validated_api_question_models,
)

Json: TypeAlias = bool | int | float | str | None | list["Json"] | dict[str, "Json"]

DESCRIPTION = "Write the response body's spelling of score criteria as a table."
FORMAT = 1
DEFAULT_OUTPUT = Path(__file__).resolve().parent / "repr_json_vectors.jsonl"
SEED = 20261002
RANDOM_DOCUMENTS = 40

EXPECTED_PYTHON = (3, 14, 3)
EXPECTED_PYDANTIC = "2.13.4"
EXPECTED_PYDANTIC_CORE = "2.46.4"
EXPECTED_ADAPTER = "0.2.1"
EXPECTED_SDK = "0.7.0"
UPSTREAM_COMMIT = "e1d4cc938204b22fc5a3c3aca7044072fe3f712d"
# sha256 over "<relative path>\0<sha256 of the file>\n" for every *.py file of the
# system_one_adapter package at UPSTREAM_COMMIT, in sorted path order.
EXPECTED_SOURCES_SHA256 = (
    "65f4e07f88838b72bed58babd3618978d9d58c3d6929105ace057ec993d0b076"
)

CLASSES = ("convert-depth",)

# Characters of a string criterion; each is written escaped and, unless it
# is a control character, which JSON forbids raw, also as it is.
CHARACTERS = (
    *(chr(code) for code in range(0x20)),
    '"',
    "\\",
    "/",
    "<",
    ">",
    "&",
    "'",
    "\x7f",
    "\x80",
    "\x9f",
    "\xa0",
    "\xe9",
    "\u07ff",
    "\u0800",
    "\u2028",
    "\u2029",
    "\ufeff",
    "\ufffd",
    "\uffff",
    "\U00010000",
    "\U0001f600",
    "\U0010ffff",
    "e\u0301",
)

# Number literals, each the one element of an array criterion.
NUMBERS = (
    "0",
    "-0",
    "1",
    "-1",
    "42",
    "3.0",
    "0.0",
    "-0.0",
    "0e0",
    "-0e-0",
    "0E+0",
    "1.50",
    "-2.50",
    "1E5",
    "1e+5",
    "1e-5",
    "1E-5",
    "1.0e0",
    "0.1e1",
    "100e-2",
    "1e400",
    "-1e400",
    "1E+400",
    "1e-400",
    "-1e-400",
    "1e-7",
    "1.5e-7",
    "1e-6",
    "0.000001",
    "0.00001",
    "0.0001",
    "0.001",
    "1e15",
    "1e16",
    "1e17",
    "9999999999999998.0",
    "12345678901234567890123",
    "-12345678901234567890123",
    "0.1",
    "0.30000000000000004",
    "5e-324",
    "2.2250738585072014e-308",
    "1.7976931348623157e308",
    "123456789.123456789",
    "9007199254740993",
    "9007199254740993.0",
)

# Criteria whose shape matters: containers, member order and repeated names.
STRUCTURES = (
    ("empty-object", "{}"),
    ("empty-array", "[]"),
    ("null-member", '{"a":null}'),
    ("literals", "[true,false,null]"),
    ("member-order", '{"b":1,"a":2,"c":3}'),
    ("repeated-name", '{"b":1,"a":2,"b":3}'),
    ("repeated-name-nested", '{"a":{"x":1},"b":2,"a":[1,{"y":2}]}'),
    ("repeated-name-deep", '{"a":{"x":1,"x":2},"a":{"y":1,"y":{"z":3,"z":4}}}'),
    ("empty-name", '{"":1}'),
    ("escaped-name", '{"\\u00e9\\n":1,"<a>":2}'),
    ("white-space", ' [ 1 ,\t{ "a" :\n2 } ,\r"x" ] '),
    ("nested-mix", '{"a":[1,2.5,{"b":[null,"\\u2028"]}],"c":{"d":-0.0}}'),
    ("infinity-in-object", '{"x":1e400,"y":-1e400}'),
    ("text", '"Condemns."'),
    ("text-empty", '""'),
)

# Criteria that the question validation refuses: not text, an object or an array.
REFUSED = (
    ("integer", "1"),
    ("float", "1.0"),
    ("true", "true"),
    ("null", "null"),
)


@dataclass(frozen=True)
class Case:
    """One criterion and what the script expects of it.

    Attributes:
        name: The case's name, ``<group>/<rest>``.
        criterion: The criterion's JSON text.
        shows: The class the case is built for, or ``""``.
    """

    name: str
    criterion: str
    shows: str = ""


def string_cases() -> Iterator[Case]:
    """Yield a string criterion per character, escaped and raw."""
    for char in CHARACTERS:
        label = "+".join(f"U+{ord(c):04X}" for c in char)
        yield Case(f"string/{label}-escaped", json.dumps(char, ensure_ascii=True))
        if all(ord(c) >= 0x20 for c in char):
            yield Case(f"string/{label}-raw", json.dumps(char, ensure_ascii=False))
    yield Case("string/uppercase-escape", '"\\u00E9\\u00C9"')
    yield Case("string/surrogate-pair", '"\\ud83d\\ude00"')


def number_cases() -> Iterator[Case]:
    """Yield an array criterion per number literal."""
    for literal in NUMBERS:
        yield Case(f"number/{literal}", f"[{literal}]")
    digits = "9" * 4300
    yield Case("number/int-4300-digits", f"[{digits}]")
    yield Case("number/int-4301-digits", f"[{digits}9]")


def depth_cases() -> Iterator[Case]:
    """Yield criteria nested around the depth limits."""
    for wraps in range(252, 256):
        shows = "convert-depth" if wraps == 254 else ""
        yield Case(f"depth/{wraps}", "[" * wraps + "1" + "]" * wraps, shows)


def random_value(rng: random.Random, depth: int) -> Json:
    """Return a random JSON value no deeper than ``depth`` containers."""
    roll = rng.random()
    if depth == 0 or roll < 0.45:
        pick = rng.randrange(6)
        if pick == 0:
            return None
        if pick == 1:
            return rng.random() < 0.5
        if pick == 2:
            return rng.randrange(-(10**20), 10**20)
        if pick == 3:
            while True:
                value: float = struct.unpack(
                    "<d", rng.getrandbits(64).to_bytes(8, "little")
                )[0]
                if math.isfinite(value):
                    return value
        if pick == 4:
            return rng.choice((0.5, 0.1, 1e-05, 2.5e-07, 1e16, -0.0, 100000.0))
        return "".join(rng.choice(CHARACTERS) for _ in range(rng.randrange(4)))
    if roll < 0.75:
        return [random_value(rng, depth - 1) for _ in range(rng.randrange(4))]
    return {
        rng.choice(("a", "b", "c", "\xe9", "")): random_value(rng, depth - 1)
        for _ in range(rng.randrange(4))
    }


def random_cases() -> Iterator[Case]:
    """Yield seeded random container criteria."""
    rng = random.Random(SEED)
    for index in range(RANDOM_DOCUMENTS):
        value: Json = (
            [random_value(rng, 3) for _ in range(3)]
            if index % 2
            else {key: random_value(rng, 3) for key in ("p", "q", "r")}
        )
        yield Case(f"random/{index}", json.dumps(value, ensure_ascii=index % 3 == 0))


def cases() -> list[Case]:
    """Return every case, in the table's order."""
    built = [
        *string_cases(),
        *number_cases(),
        *(Case(f"structure/{name}", text) for name, text in STRUCTURES),
        *(Case(f"refused/{name}", text) for name, text in REFUSED),
        *depth_cases(),
        *random_cases(),
    ]
    names = [case.name for case in built]
    if len(set(names)) != len(names):
        raise ValueError("two cases have one name")
    return built


def legend_of(criterion: str) -> dict[str, Json]:
    """Return the row's verdict: the legend's text or the stage that refused.

    Args:
        criterion: The criterion's JSON text.

    Returns:
        ``{"legend": text}`` or ``{"refused": "<stage>:<class>"}``.
    """
    request = '{"q":{"type":"score","criteria":[' + criterion + ',"x"]}}'
    try:
        loaded = json.loads(request)
    except ValueError as error:
        return {"refused": f"loads:{type(error).__name__}"}
    try:
        questions = convert_question_collection_to_validated_api_question_models(loaded)
    except (ValueError, pydantic.ValidationError) as error:
        return {"refused": f"validation:{type(error).__name__}"}
    try:
        answer, _ = _convert_llm_value_to_typesafe_answer(
            questions["q"],
            {"0": 0.5, "1": 0.5},
            "probabilities",
            should_normalize_probabilities=False,
        )
    except (ValueError, pydantic_core.PydanticSerializationError) as error:
        return {"refused": f"convert:{type(error).__name__}"}
    usage = Usage(
        input_tokens=0,
        output_tokens=0,
        input_tokens_total=0,
        output_tokens_total=0,
        n_retries=0,
        n_retries_malformed_structure=0,
        latency=0.0,
    )
    response = SystemOneResponse(
        model="m", answers={"q": answer}, usage=usage, debug={}
    )
    legend = response.model_dump(mode="json")["answers"]["q"]["legend"]["0"]
    text = json.dumps(legend, ensure_ascii=False, separators=(",", ":"))
    return {"legend": text}


def check_class(case: Case, verdict: Mapping[str, Json]) -> None:
    """Stop when a row does not show the behaviour its case was built for."""
    refused = verdict.get("refused")
    if case.shows == "convert-depth":
        if refused != "convert:PydanticSerializationError":
            raise ValueError(f"{case.name}: built for convert-depth, got {verdict}")
    elif isinstance(refused, str) and refused.startswith("convert:"):
        raise ValueError(f"{case.name}: refused in conversion without a class")
    if case.name.startswith("refused/") and not (
        isinstance(refused, str) and refused.startswith("validation:")
    ):
        raise ValueError(f"{case.name}: built to be refused, got {verdict}")


def sources_digest(package: Path) -> str:
    """Return the digest of the package's Python sources (see the constant)."""
    digest = hashlib.sha256()
    for path in sorted(package.rglob("*.py")):
        relative = path.relative_to(package).as_posix()
        file_digest = hashlib.sha256(path.read_bytes()).hexdigest()
        digest.update(f"{relative}\0{file_digest}\n".encode())
    return digest.hexdigest()


def check_environment() -> dict[str, Json]:
    """Refuse to run on anything but the reference, and return the header facts.

    Raises:
        SystemExit: The interpreter, a package version or upstream's sources
            differ from the reference.
    """
    package = Path(system_one_adapter.__file__).resolve().parent
    found: dict[str, object] = {
        "python": tuple(sys.version_info[:3]),
        "pydantic": pydantic.VERSION,
        "pydantic_core": pydantic_core.__version__,
        "system-one-adapter": metadata.version("system-one-adapter"),
        "typesafe-sdk": metadata.version("typesafe-sdk"),
        "sources": sources_digest(package),
    }
    expected: dict[str, object] = {
        "python": EXPECTED_PYTHON,
        "pydantic": EXPECTED_PYDANTIC,
        "pydantic_core": EXPECTED_PYDANTIC_CORE,
        "system-one-adapter": EXPECTED_ADAPTER,
        "typesafe-sdk": EXPECTED_SDK,
        "sources": EXPECTED_SOURCES_SHA256,
    }
    wrong = {
        key: (found[key], expected[key])
        for key in expected
        if found[key] != expected[key]
    }
    if wrong:
        raise SystemExit(f"not the reference environment (found, expected): {wrong}")
    return {
        "python": sys.version,
        "pydantic": pydantic.VERSION,
        "pydantic_core": pydantic_core.__version__,
        "system_one_adapter": {
            "version": EXPECTED_ADAPTER,
            "commit": UPSTREAM_COMMIT,
            "sources_sha256": EXPECTED_SOURCES_SHA256,
        },
        "typesafe_sdk": EXPECTED_SDK,
    }


def dump(record: Mapping[str, Json]) -> str:
    """Return one line of the table: compact JSON in ASCII."""
    return json.dumps(record, ensure_ascii=True, separators=(",", ":"))


def render() -> tuple[str, list[str]]:
    """Build the whole table and a summary of it.

    Returns:
        The file's text and the summary lines for standard output.
    """
    all_cases = cases()
    lines: list[str] = []
    refused: dict[str, int] = {}
    for case in all_cases:
        verdict = legend_of(case.criterion)
        check_class(case, verdict)
        row: dict[str, Json] = {"case": case.name}
        if case.shows:
            row["class"] = case.shows
        row["criterion"] = case.criterion
        row.update(verdict)
        stage = verdict.get("refused")
        if isinstance(stage, str):
            refused[stage] = refused.get(stage, 0) + 1
        lines.append(dump(row))
    header: dict[str, Json] = {
        "format": FORMAT,
        "generator": Path(__file__).name,
        **check_environment(),
        "call": (
            'json.dumps(SystemOneResponse(...).model_dump(mode="json")'
            '["answers"]["q"]["legend"]["0"], ensure_ascii=False, '
            'separators=(",", ":"))'
        ),
        "request": '{"q":{"type":"score","criteria":[<criterion>,"x"]}}',
        "classes": list(CLASSES),
        "seed": SEED,
        "rows": len(all_cases),
    }
    text = "\n".join([dump(header), *lines]) + "\n"
    with_legend = len(all_cases) - sum(refused.values())
    summary = [
        f"rows: {len(all_cases)}, with a legend {with_legend}",
        *(f"refused {stage}: {count}" for stage, count in sorted(refused.items())),
        f"total: {len(text.encode())} bytes",
    ]
    return text, summary


def main() -> int:
    """Write the table, or compare it with the file that exists.

    Returns:
        0 on success; 1 when ``--check`` finds a difference.
    """
    parser = argparse.ArgumentParser(description=DESCRIPTION)
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument(
        "--check",
        action="store_true",
        help="compare the output file with a fresh table and write nothing",
    )
    arguments = parser.parse_args()
    output: Path = arguments.output
    text, summary = render()
    print("\n".join(summary))
    digest = hashlib.sha256(text.encode("ascii")).hexdigest()
    if not arguments.check:
        output.write_bytes(text.encode("ascii"))
        print(f"wrote {output.name}: sha256 {digest}")
        return 0
    old = output.read_bytes().decode("ascii").split("\n")
    new = text.split("\n")
    differing = [
        number
        for number in range(max(len(old), len(new)))
        if number >= len(old) or number >= len(new) or old[number] != new[number]
    ]
    for number in differing[:10]:
        in_file = old[number] if number < len(old) else "<absent>"
        fresh = new[number] if number < len(new) else "<absent>"
        print(f"line {number + 1} differs", file=sys.stderr)
        print(f"  file:  {in_file}", file=sys.stderr)
        print(f"  fresh: {fresh}", file=sys.stderr)
    if differing:
        print(f"{output.name}: {len(differing)} lines differ", file=sys.stderr)
        return 1
    print(f"{output.name}: equal to a fresh table, sha256 {digest}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
