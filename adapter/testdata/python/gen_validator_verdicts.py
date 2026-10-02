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
"""Write pydantic's strict-mode verdicts on edge inputs as a table for the Go validator.

The models are the ones system-one-adapter 0.2.1 generates for a request
(``create_llm_output_model``): one per question type and answer mode (the six
core models), plus models with several questions or unusual names. Each input is
given to ``model_validate_json`` as a ``str``, as upstream's client does.

The output is JSON Lines, in ASCII:

- line 1, the header: the versions and the format (its first member is
  ``format``);
- a model line (first member ``model``): the answer mode and the questions,
  followed by that model's rows;
- a row (first member ``case``): ``input`` and either ``accepted`` with the
  parsed value or ``refused`` with one ``[type, loc...]`` list per pydantic
  error, in pydantic's order. ``json_error`` is pydantic's message when the
  parser refused the text. ``after_extract_json`` is present only when the
  verdict changes once upstream's ``_extract_json`` has run on the input first,
  as it does in upstream's client.

``input`` is a JSON string: decoded, it is the exact text given to pydantic, and
its UTF-8 encoding is the bytes a Go test feeds its validator. In an accepted
value a bool is a JSON bool, a string a JSON string, an int ``{"int": "1"}`` and
a float ``{"float": "0.5"}`` with Python's ``repr``, which reads back to the same
bits and tells ``-0.0`` from ``0.0``; members are in the model's field order.

A case is named ``<item>/<rest>``; the items in the header's ``required_items``
have at least one row for every core model, which the script checks. A row has a
``class`` member when its verdict or value rests on one of five behaviours of
pydantic that a validator built on a strict JSON tokenizer does not have by
itself (several are joined with ``+``). The class comes from how the case is
built, never from the input's text, and the script stops when a row does not
show the behaviour its case was built for:

- ``internal-name``: a member named by a field name of the generated model
  (``answer_<index>``, ``probability_<index>``) is ignored, not refused;
- ``non-finite-token``: the document is accepted although it holds a ``NaN``,
  ``Infinity`` or ``-Infinity`` token, in a value that is not validated;
- ``recursion-limit``: the parser refuses the text because a value has more
  than 200 arrays and objects around it;
- ``negative-zero``: ``-0`` for a probability is accepted as +0.0;
- ``integer-length``: the parser refuses the text because the integer part of
  a number, its sign counted, is longer than 4300 characters.

The table has no random part, so there is no seed and no size: the same
interpreter and the same package versions give the same bytes. The script
refuses to run on other versions, or on a ``system_one_adapter`` whose sources
differ from the release.

Usage::

    uv run testdata/python/gen_validator_verdicts.py            # write the table
    uv run testdata/python/gen_validator_verdicts.py --check    # compare, no write
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from collections import Counter
from collections.abc import Iterator, Mapping, Sequence
from dataclasses import dataclass
from importlib import metadata
from pathlib import Path
from typing import Literal, TypeAlias

import pydantic
import pydantic_core
import system_one_adapter
from pydantic import BaseModel, ValidationError
from system_one_adapter import Choice, Noul, Score
from system_one_adapter._client import _extract_json
from system_one_adapter._schema import create_llm_output_model

Question: TypeAlias = Noul | Choice | Score
AnswerMode: TypeAlias = Literal["probabilities", "discrete"]
Level: TypeAlias = Literal["full", "core", "extra"]
Slot: TypeAlias = tuple[str, ...]
Json: TypeAlias = bool | int | float | str | None | list["Json"] | dict[str, "Json"]

DESCRIPTION = "Write pydantic's strict-mode verdicts on edge inputs as a table."
FORMAT = 1
DEFAULT_OUTPUT = Path(__file__).resolve().parent / "validator_verdicts.jsonl"

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

# The required inputs. Each has a row for every core model.
REQUIRED_ITEMS = (
    "duplicate-names",
    "minus-zero",
    "exponent",
    "float-for-int",
    "int-for-number",
    "bool-for-number",
    "huge-integer",
    "nan-token",
    "bom",
    "trailing-data",
)

# Upstream's own list of awkward names (tests/test_schema.py lines 18 and 19 at
# UPSTREAM_COMMIT): schema keywords, pydantic attribute names, the empty string
# and the internal field names the generated models use.
FIELD_NAMES = (
    "title",
    "minimum",
    "maximum",
    "exclusiveMinimum",
    "exclusiveMaximum",
    "model_dump",
    "model_config",
    "_private",
    "",
    "with spaces",
    "answer_0",
    "probability_0",
)

ODD_LABELS = ("caf\u00e9", "A", "a", "1", "true", "null", "0.5")

BACKSLASH = chr(92)
CLASSES = (
    "internal-name",
    "non-finite-token",
    "recursion-limit",
    "negative-zero",
    "integer-length",
)
BOM = "\ufeff"
# The parser refuses a text in which any value, scalar or container, has more
# arrays and objects around it than this.
MAX_ENCLOSING = 200
# The parser refuses a text in which the integer part of a number, its sign
# counted, has more characters than this.
MAX_INTEGER = 4300

# Tokens tried where an answer value belongs: (name, raw text, item, inner).
# `inner` marks the ones also tried inside a probability map.
VALUE_TOKENS: tuple[tuple[str, str, str, bool], ...] = (
    ("int", "-0", "minus-zero", True),
    ("float", "-0.0", "minus-zero", True),
    ("exponent", "-0e0", "minus-zero", False),
    ("1e0", "1e0", "exponent", True),
    ("1E0", "1E0", "exponent", False),
    ("5e-1", "5e-1", "exponent", False),
    ("1.0", "1.0", "float-for-int", True),
    ("0.5", "0.5", "float-for-int", False),
    ("0.50", "0.50", "float-for-int", False),
    ("1.5", "1.5", "float-for-int", False),
    ("1", "1", "int-for-number", True),
    ("0", "0", "int-for-number", True),
    ("2", "2", "int-for-number", True),
    ("true", "true", "bool-for-number", True),
    ("false", "false", "bool-for-number", False),
    ("int64-max", str(2**63 - 1), "huge-integer", False),
    ("uint64-max-plus-1", str(2**64), "huge-integer", True),
    ("int64-min-minus-1", str(-(2**63) - 1), "huge-integer", True),
    ("NaN", "NaN", "nan-token", True),
    ("neg-NaN", "-NaN", "nan-token", False),
    ("nan", "nan", "nan-token", False),
    ("Infinity", "Infinity", "nan-token", True),
    ("neg-Infinity", "-Infinity", "nan-token", True),
    ("neg-tenth", "-0.1", "bound", True),
    ("one-and-tenth", "1.1", "bound", True),
    ("next-above-one", "1.0000000000000002", "bound", True),
    ("rounds-down-to-one", "1.00000000000000001", "bound", True),
    ("rounds-up-to-one", "0.99999999999999999", "bound", False),
    ("neg-smallest-subnormal", "-5e-324", "bound", True),
    ("underflow", "1e-400", "bound", True),
    ("neg-underflow", "-1e-400", "bound", True),
    ("overflow", "1e400", "bound", True),
    ("neg-one", "-1", "bound", True),
    ("three", "3", "bound", False),
    ("leading-zero", "01", "grammar", True),
    ("plus-sign", "+1", "grammar", False),
    ("trailing-point", "1.", "grammar", False),
    ("leading-point", ".5", "grammar", False),
    ("python-true", "True", "grammar", False),
    ("bare-word", "yes", "grammar", False),
    ("no-value", "", "grammar", False),
    ("value", "null", "null", True),
    ("half", '"0.5"', "string-for-number", True),
    ("empty", '""', "string-for-number", False),
    ("yes", '"yes"', "label", False),
    ("capital", '"Yes"', "label", False),
    ("escaped", '"y\\u0065s"', "label", False),
    ("unknown", '"maybe"', "label", False),
    ("lone-surrogate", '"\\ud800"', "label", False),
    ("raw-tab", '"ye\ts"', "label", False),
    ("invalid-escape", '"y\\es"', "label", False),
    ("empty-array", "[]", "wrong-container", True),
    ("array-of-label", '["yes"]', "wrong-container", False),
    ("empty-object", "{}", "wrong-container", False),
)

# Tokens whose exponent no fixed-size integer holds: (name, raw text). They are
# tried on the core models wherever a probability belongs.
EXPONENT_TOKENS: tuple[tuple[str, str], ...] = (
    ("exponent-overflow", "1e99999999999999999999"),
    ("zero-exponent-overflow", "0e99999999999999999999"),
    ("exponent-underflow", "1e-99999999999999999999"),
)

# The tokens tried on the models outside the core six.
EXTRA_MODEL_TOKENS = ("-0", "1e0", "1.0", "1", "true", str(2**64), "NaN", "null")
# The tokens tried on the models with a dozen names, whose documents are long.
LONG_MODEL_TOKENS = ("-0", "1.0", "true", "NaN")

ODD_LABEL_TOKENS: tuple[tuple[str, str], ...] = (
    ("cafe-raw", '"caf\u00e9"'),
    ("cafe-escaped", '"caf\\u00e9"'),
    ("cafe-capital-hex", '"caf\\u00E9"'),
    ("cafe-decomposed", '"cafe\u0301"'),
    ("cafe-capitals", '"CAF\u00c9"'),
    ("capital-a", '"A"'),
    ("small-a", '"a"'),
    ("string-1", '"1"'),
    ("number-1", "1"),
    ("string-true", '"true"'),
    ("literal-true", "true"),
    ("string-null", '"null"'),
    ("literal-null", "null"),
    ("string-half", '"0.5"'),
    ("number-half", "0.5"),
    ("string-half-trailing-zero", '"0.50"'),
)


@dataclass(frozen=True)
class Case:
    """One input for one model; its name in the table is ``<item>/<rest>``."""

    item: str
    rest: str
    text: str
    # The classes the case is built to show (see the module's documentation).
    shows: tuple[str, ...] = ()


@dataclass(frozen=True)
class Spec:
    """One generated model: its questions, its answer mode and how far it is tested."""

    name: str
    mode: AnswerMode
    questions: Mapping[str, Question]
    level: Level

    @property
    def core(self) -> bool:
        """Whether this is one of the six question-type and answer-mode models."""
        return self.level != "extra"

    @property
    def long(self) -> bool:
        """Whether the model has a dozen names, which makes its documents long."""
        return max(len(self.questions), len(self.labels(self.first))) > 3

    @property
    def tokens(self) -> tuple[str, ...]:
        """The raw tokens tried on this model when it is not a core model."""
        return LONG_MODEL_TOKENS if self.long else EXTRA_MODEL_TOKENS

    @property
    def first(self) -> str:
        """The first question's identifier."""
        return next(iter(self.questions))

    def is_map(self, qid: str) -> bool:
        """Report whether the answer to ``qid`` is a probability map."""
        return self.mode == "probabilities" and not isinstance(
            self.questions[qid], Noul
        )

    def labels(self, qid: str) -> list[str]:
        """Return the member names of the probability map of ``qid``."""
        question = self.questions[qid]
        if isinstance(question, Score):
            return [str(level) for level in range(len(question.criteria))]
        if isinstance(question, Choice):
            return list(question.criteria)
        return []

    def valid_value(self, qid: str) -> str:
        """Return a raw JSON value that the model accepts for ``qid``."""
        question = self.questions[qid]
        if self.is_map(qid):
            return obj(self.map_members(qid, {}))
        if isinstance(question, Noul):
            return "true" if self.mode == "discrete" else "0.5"
        if isinstance(question, Score):
            return "1"
        return quote(next(iter(question.criteria)))

    def map_members(
        self, qid: str, override: Mapping[Slot, str]
    ) -> list[tuple[str, str]]:
        """Return the members of the probability map of ``qid`` as raw pairs."""
        return [
            (quote(label), override.get((qid, label), repr((index + 1) / 16)))
            for index, label in enumerate(self.labels(qid))
        ]

    def members(self, override: Mapping[Slot, str]) -> list[tuple[str, str]]:
        """Return the members of ``answers``, with the overridden values in place."""
        result: list[tuple[str, str]] = []
        for qid in self.questions:
            if (qid,) in override:
                value = override[(qid,)]
            elif self.is_map(qid):
                value = obj(self.map_members(qid, override))
            else:
                value = self.valid_value(qid)
            result.append((quote(qid), value))
        return result

    def document(self, override: Mapping[Slot, str] | None = None) -> str:
        """Return a whole document, valid except for the overridden values."""
        return with_answers(obj(self.members(override or {})))

    def slots(self) -> list[tuple[Slot, bool]]:
        """Return the value positions tried, and whether each is inside a map."""
        qids = list(self.questions) if len(self.questions) <= 3 else [self.first]
        result: list[tuple[Slot, bool]] = []
        for qid in qids:
            result.append(((qid,), False))
            if self.is_map(qid):
                result.append(((qid, self.labels(qid)[0]), True))
        return result


def quote(text: str) -> str:
    """Return ``text`` as a JSON string token with non-ASCII characters kept raw."""
    return json.dumps(text, ensure_ascii=False)


def escape_all(text: str, capital: bool = False) -> str:
    """Return ``text`` as a JSON string token with every character escaped.

    Args:
        text: The string to spell; every character must be in the BMP.
        capital: Write the hex digits A to F as capitals.

    Returns:
        The token, with its quotes: four hex digits per character.

    Raises:
        ValueError: A character is outside the BMP.
    """
    if any(ord(char) > 0xFFFF for char in text):
        raise ValueError(f"{text!r} has a character outside the BMP")
    digits = "04X" if capital else "04x"
    escapes = (f"{BACKSLASH}u{ord(char):{digits}}" for char in text)
    return '"' + "".join(escapes) + '"'


def obj(members: Sequence[tuple[str, str]]) -> str:
    """Join raw ``(name token, value)`` pairs into a compact JSON object."""
    return "{" + ",".join(f"{name}:{value}" for name, value in members) + "}"


def with_answers(raw: str, name: str = '"answers"') -> str:
    """Return the document whose only member is ``answers`` with the raw value."""
    return obj([(name, raw)])


def nested(depth: int) -> str:
    """Return ``depth`` empty arrays nested in each other."""
    return "[" * depth + "]" * depth


def slot_name(slot: Slot) -> str:
    """Return a readable name for a value position."""
    return ".".join(part or "<empty>" for part in slot)


def specs() -> list[Spec]:
    """Build the list of models: the six core ones, then the ones with odd names."""
    modes: tuple[AnswerMode, AnswerMode] = ("discrete", "probabilities")
    core: dict[str, Question] = {
        "noul": Noul(),
        "score": Score(criteria=["Bad.", "Fair.", "Good."]),
        "choice": Choice(criteria={"yes": None, "no": None}),
    }
    # The cases about the text around the answer (whitespace, grammar, depth,
    # the top-level value) do not depend on the model: one model carries them.
    full = "noul-probabilities"
    result = [
        Spec(
            f"{kind}-{mode}",
            mode,
            {"answer": question},
            "full" if f"{kind}-{mode}" == full else "core",
        )
        for kind, question in core.items()
        for mode in modes
    ]
    multi: dict[str, Question] = {
        "flag": Noul(instructions="Is it spam?"),
        "level": Score(criteria=["Bad.", "Good."]),
        "pick": Choice(criteria={"left": "The left one.", "right": None}),
    }
    names: dict[str, Question] = {
        name: Noul(instructions=f"Evaluate {name}.") for name in FIELD_NAMES
    }
    labels: dict[str, Question] = {
        "level": Choice(criteria={name: f"The {name} option." for name in FIELD_NAMES})
    }
    odd: dict[str, Question] = {"answer": Choice(criteria=dict.fromkeys(ODD_LABELS))}
    for mode in modes:
        result.append(Spec(f"multi-{mode}", mode, multi, "extra"))
    for mode in modes:
        result.append(Spec(f"names-{mode}", mode, names, "extra"))
    result.append(Spec("labels-probabilities", "probabilities", labels, "extra"))
    result.append(Spec("odd-labels-discrete", "discrete", odd, "extra"))
    return result


def value_cases(spec: Spec) -> Iterator[Case]:
    """Yield one case per token and value position of ``spec``."""
    for slot, inner in spec.slots():
        where = slot_name(slot)
        probability = inner or (
            spec.mode == "probabilities" and not spec.is_map(slot[0])
        )
        for name, raw, item, in_map in VALUE_TOKENS:
            if inner and not in_map:
                continue
            if not spec.core and raw not in spec.tokens:
                continue
            shows = ("negative-zero",) if probability and raw == "-0" else ()
            yield Case(item, f"{where}/{name}", spec.document({slot: raw}), shows)
        if spec.core and probability:
            for name, raw in EXPONENT_TOKENS:
                yield Case("bound", f"{where}/{name}", spec.document({slot: raw}))


def document_cases(spec: Spec) -> Iterator[Case]:
    """Yield the cases that change the document around a valid answer."""
    valid = spec.document()
    yield Case("baseline", "valid", valid)
    if spec.long:
        return
    yield Case("bom", "leading", BOM + valid)
    yield Case("bom", "trailing", valid + BOM)
    yield Case("trailing-data", "letter", valid + "x")
    yield Case("trailing-data", "second-document", valid + valid)
    if spec.core:
        yield Case("bom", "only", BOM)
        yield Case("bom", "after-brace", "{" + BOM + valid[1:])
        yield Case("trailing-data", "comma", valid + ",")
        yield Case("trailing-data", "space-null", valid + " null")
        yield Case("trailing-data", "nul", valid + "\x00")


def duplicate_cases(spec: Spec) -> Iterator[Case]:
    """Yield the cases with a member name written twice."""
    item = "duplicate-names"
    valid = spec.document()
    qid = spec.first
    name = quote(qid)
    good = spec.valid_value(qid)
    rest = spec.members({})[1:]
    if spec.long:
        return

    def twice(first: str, second: str, second_name: str = name) -> str:
        return with_answers(obj([(name, first), (second_name, second), *rest]))

    yield Case(item, "answers-null-then-valid", f'{{"answers":null,{valid[1:]}')
    yield Case(item, "answers-valid-then-null", f'{valid[:-1]},"answers":null}}')
    yield Case(item, "answer-null-then-valid", twice("null", good))
    yield Case(item, "answer-valid-then-null", twice(good, "null"))
    if not spec.core:
        return
    non_finite = ("non-finite-token",)
    yield Case(item, "answer-NaN-then-valid", twice("NaN", good), non_finite)
    yield Case(item, "answer-valid-then-NaN", twice(good, "NaN"))
    yield Case(
        item, "answer-neg-Infinity-then-valid", twice("-Infinity", good), non_finite
    )
    yield Case(item, "answer-leading-zero-then-valid", twice("01", good))
    yield Case(item, "answer-lone-surrogate-then-valid", twice('"\\ud800"', good))
    yield Case(item, "answer-null-then-escaped", twice("null", good, escape_all(qid)))
    yield Case(item, "answer-escaped-then-null", twice(good, "null", escape_all(qid)))
    yield Case(item, "extra-twice", f'{valid[:-1]},"extra":1,"extra":2}}')
    if spec.level == "full":
        deepest = nested(MAX_ENCLOSING - 1)
        yield Case(item, "answer-deepest-then-valid", twice(deepest, good))
        yield Case(
            item,
            "answer-too-deep-then-valid",
            twice(f"[{deepest}]", good),
            ("recursion-limit",),
        )
        # The integer part of a number in a value that a later duplicate
        # replaces: (name, raw text, whether the parser refuses the text).
        longest = "1" + "0" * (MAX_INTEGER - 1)
        for kind, raw, refused in (
            ("longest", longest, False),
            ("too-long", longest + "0", True),
            ("negative-longest", "-" + longest[:-1], False),
            ("negative-too-long", "-" + longest, True),
            ("too-long-with-fraction", longest + "0.0", True),
        ):
            shows = ("integer-length",) if refused else ()
            yield Case("integer-length", f"{kind}-then-valid", twice(raw, good), shows)
    if spec.is_map(qid):
        members = spec.map_members(qid, {})
        label = members[0][0]
        escaped = escape_all(spec.labels(qid)[0])

        def in_map(pairs: Sequence[tuple[str, str]]) -> str:
            return spec.document({(qid,): obj(pairs)})

        yield Case(item, "label-invalid-then-valid", in_map([(label, "2"), *members]))
        yield Case(item, "label-valid-then-invalid", in_map([*members, (label, "2")]))
        yield Case(
            item,
            "label-invalid-then-escaped",
            in_map([(label, "2"), (escaped, "0"), *members[1:]]),
        )


def member_cases(spec: Spec) -> Iterator[Case]:
    """Yield the cases with a missing, extra, renamed or misplaced member."""
    valid = spec.document()
    members = spec.members({})
    answers = obj(members)
    qid = spec.first
    name = quote(qid)
    good = spec.valid_value(qid)
    after_name = valid[len('{"answers"') :]

    yield Case("missing-member", "first-answer", with_answers(obj(members[1:])))
    yield Case(
        "extra-member", "in-answers", with_answers(obj([*members, ('"extra"', good)]))
    )
    if len(members) > 1:
        internal = [
            (f'"answer_{index}"', value) for index, (_, value) in enumerate(members)
        ]
        yield Case("member-order", "answers-reversed", with_answers(obj(members[::-1])))
        yield Case("internal-name", "every-field-name", with_answers(obj(internal)))
        junk = [(field, "null") for field, _ in internal]
        # A field name that is itself a question id is that question's answer.
        collides = any(field == name for field, _ in internal for name, _ in members)
        yield Case(
            "internal-name",
            "every-field-name-beside",
            with_answers(obj(members + junk)),
            () if collides else ("internal-name",),
        )
    if not spec.core:
        return

    yield Case("missing-member", "empty-document", "{}")
    yield Case("extra-member", "top-after", f'{valid[:-1]},"extra":1}}')
    yield Case("extra-member", "top-before", f'{{"extra":null,{valid[1:]}')
    yield Case("internal-name", "instead", with_answers(obj([('"answer_0"', good)])))

    def beside(*pairs: tuple[str, str]) -> str:
        return with_answers(obj([(name, good), *pairs]))

    ignored = ("internal-name",)
    yield Case(
        "internal-name", "beside-wrong-type", beside(('"answer_0"', "[null]")), ignored
    )
    yield Case(
        "internal-name",
        "beside-NaN",
        beside(('"answer_0"', "NaN")),
        ("internal-name", "non-finite-token"),
    )
    yield Case("internal-name", "next-index-beside", beside(('"answer_1"', good)))
    if spec.level == "full":
        text = ('"answer_0"', '"x"')
        too_deep = nested(MAX_ENCLOSING)
        twice = beside(text, ('"answer_0"', "{}"))
        yield Case(
            "internal-name", "before", with_answers(obj([text, (name, good)])), ignored
        )
        yield Case("internal-name", "beside-twice", twice, ignored)
        yield Case(
            "internal-name",
            "beside-too-deep",
            beside(('"answer_0"', too_deep)),
            ("internal-name", "recursion-limit"),
        )
    yield Case("null", "answers", with_answers("null"))
    yield Case("wrong-container", "answers-empty-array", with_answers("[]"))
    yield Case("wrong-container", "answers-is-the-answer", with_answers(good))
    yield Case("escaped-name", "answers-first-letter", '{"\\u0061nswers"' + after_name)
    yield Case(
        "escaped-name", "question-id", with_answers(obj([(escape_all(qid), good)]))
    )
    yield Case("name-case", "answers-capital", '{"Answers"' + after_name)
    yield Case(
        "name-case",
        "question-id-capital",
        with_answers(obj([(quote(qid.upper()), good)])),
    )
    if spec.level != "full":
        return

    yield Case("extra-member", "top-empty-name", f'{valid[:-1]},"":1}}')
    yield Case(
        "escaped-name", "answers-whole", with_answers(answers, escape_all("answers"))
    )
    yield Case(
        "escaped-name",
        "question-id-capital-hex",
        with_answers(obj([(escape_all(qid, capital=True), good)])),
    )
    yield Case(
        "grammar",
        "question-id-capital-u-escape",
        with_answers(obj([(escape_all(qid).replace("u", "U"), good)])),
    )
    yield Case(
        "name-case",
        "question-id-trailing-space",
        with_answers(obj([(quote(qid + " "), good)])),
    )
    yield Case("wrong-container", "top-null", "null")
    yield Case("wrong-container", "top-empty-array", "[]")
    yield Case("wrong-container", "top-array-of-document", f"[{valid}]")
    yield Case("wrong-container", "top-string-of-document", json.dumps(valid))
    yield Case("wrong-container", "top-number", "1")
    yield Case("wrong-container", "top-true", "true")
    yield Case(
        "wrong-container", "answers-array-of-object", with_answers(f"[{answers}]")
    )
    yield Case("wrong-container", "answers-string", with_answers(json.dumps(answers)))
    yield Case("wrong-container", "answers-number", with_answers("1"))


def text_cases(spec: Spec) -> Iterator[Case]:
    """Yield the cases about whitespace, grammar, depth and code fences."""
    if not spec.core:
        return
    valid = spec.document()
    qid = spec.first
    good = spec.valid_value(qid)
    deepest = nested(MAX_ENCLOSING - 1)

    yield Case("empty-input", "nothing", "")
    yield Case("whitespace", "leading-space", " " + valid)
    yield Case("whitespace", "trailing-newline", valid + "\n")
    yield Case("whitespace", "leading-nbsp", "\xa0" + valid)
    yield Case("code-fence", "json", f"```json\n{valid}\n```")
    if spec.level != "full":
        return

    too_deep = ("recursion-limit",)
    yield Case("nesting-depth", "value-deepest", spec.document({(qid,): deepest}))
    yield Case(
        "nesting-depth",
        "value-too-deep",
        spec.document({(qid,): f"[{deepest}]"}),
        too_deep,
    )

    spaced = f'{{ "answers" :\t{{\r\n{quote(qid)} : {good} }}\n}}'
    yield Case("empty-input", "space", " ")
    yield Case("empty-input", "newline", "\n")
    yield Case("whitespace", "leading-tab-cr-lf", "\t\r\n" + valid)
    yield Case("whitespace", "trailing-space", valid + " ")
    yield Case("whitespace", "between-tokens", spaced)
    yield Case("whitespace", "leading-vertical-tab", "\x0b" + valid)
    yield Case("whitespace", "leading-form-feed", "\x0c" + valid)
    yield Case("whitespace", "leading-unit-separator", "\x1f" + valid)
    yield Case("whitespace", "leading-next-line", "\x85" + valid)
    yield Case("whitespace", "trailing-line-separator", valid + "\u2028")
    yield Case("whitespace", "trailing-ideographic-space", valid + "\u3000")
    yield Case("whitespace", "leading-zero-width-space", "\u200b" + valid)
    yield Case("whitespace", "leading-nul", "\x00" + valid)

    yield Case("grammar", "trailing-comma-top", valid[:-1] + ",}")
    yield Case("grammar", "trailing-comma-answers", valid[:-2] + ",}}")
    yield Case("grammar", "single-quoted-name", valid.replace('"answers"', "'answers'"))
    yield Case("grammar", "unquoted-name", valid.replace('"answers"', "answers"))
    yield Case("grammar", "comment", "/* c */" + valid)
    yield Case("grammar", "line-comment", valid + "// c")
    yield Case("grammar", "missing-close", valid[:-1])
    yield Case("grammar", "equals-sign", valid.replace('"answers":', '"answers"=', 1))

    extra = f'{valid[:-1]},"extra":'
    for name, count in (("deepest", MAX_ENCLOSING - 1), ("too-deep", MAX_ENCLOSING)):
        shows = too_deep if count == MAX_ENCLOSING else ()
        yield Case(
            "nesting-depth",
            f"number-in-arrays-{name}",
            extra + "[" * count + "1" + "]" * count + "}",
            shows,
        )
        yield Case(
            "nesting-depth",
            f"number-in-objects-{name}",
            extra + '{"a":' * count + "1" + "}" * (count + 1),
            shows,
        )

    yield Case("code-fence", "plain", f"```\n{valid}\n```")
    yield Case("code-fence", "capital-json", f"```JSON\n{valid}\n```")
    yield Case("code-fence", "unclosed", f"```json\n{valid}")
    yield Case("code-fence", "prose-before", f"Here is the JSON: {valid}")
    yield Case("code-fence", "prose-after", f"```json\n{valid}\n```\nDone.")


def map_cases(spec: Spec) -> Iterator[Case]:
    """Yield the cases about the members of a probability map."""
    qid = spec.first
    if not spec.is_map(qid):
        return
    members = spec.map_members(qid, {})
    labels = spec.labels(qid)
    value = members[0][1]
    tail = members[1:]

    def in_map(pairs: Sequence[tuple[str, str]]) -> str:
        return spec.document({(qid,): obj(pairs)})

    yield Case("missing-member", "last-label", in_map(members[:-1]))
    yield Case("extra-member", "label", in_map([*members, ('"maybe"', "0")]))
    yield Case("member-order", "labels-reversed", in_map(members[::-1]))
    yield Case(
        "internal-name",
        "probability-field",
        in_map([('"probability_0"', value), *tail]),
    )
    if not spec.core:
        internal = [
            (f'"probability_{index}"', raw) for index, (_, raw) in enumerate(members)
        ]
        yield Case("internal-name", "every-probability-field", in_map(internal))
        return
    other = labels[0].upper() if labels[0].upper() != labels[0] else "0" + labels[0]
    yield Case("missing-member", "every-label", in_map([]))
    yield Case(
        "internal-name",
        "probability-field-beside",
        in_map([*members, ('"probability_0"', '"x"')]),
        ("internal-name",),
    )
    yield Case(
        "internal-name",
        "probability-next-index-beside",
        in_map([*members, (f'"probability_{len(members)}"', "0")]),
    )
    yield Case("escaped-name", "label", in_map([(escape_all(labels[0]), value), *tail]))
    yield Case(
        "name-case", "label-other-spelling", in_map([(quote(other), value), *tail])
    )
    yield Case(
        "name-case",
        "label-leading-space",
        in_map([(quote(" " + labels[0]), value), *tail]),
    )
    yield Case("bound", "map-all-zero", in_map([(label, "0") for label, _ in members]))
    yield Case("bound", "map-all-one", in_map([(label, "1") for label, _ in members]))
    yield Case(
        "wrong-container",
        "map-is-array",
        spec.document({(qid,): "[" + ",".join(raw for _, raw in members) + "]"}),
    )


def odd_label_cases(spec: Spec) -> Iterator[Case]:
    """Yield the cases for labels that look like other JSON tokens."""
    if spec.name != "odd-labels-discrete":
        return
    for name, raw in ODD_LABEL_TOKENS:
        yield Case("label", name, spec.document({("answer",): raw}))


def cases(spec: Spec) -> list[Case]:
    """Return every case of ``spec``.

    Raises:
        ValueError: Two cases have the same name.
    """
    result = [
        *document_cases(spec),
        *duplicate_cases(spec),
        *member_cases(spec),
        *map_cases(spec),
        *text_cases(spec),
        *odd_label_cases(spec),
        *value_cases(spec),
    ]
    counts = Counter(f"{case.item}/{case.rest}" for case in result)
    repeated = sorted(name for name, count in counts.items() if count > 1)
    if repeated:
        raise ValueError(f"{spec.name}: case names repeated: {repeated}")
    return result


def canonical(value: object) -> Json:
    """Return a parsed value in a form that keeps its Python type and every bit.

    A bool stays a JSON bool and a str a JSON string. An int becomes
    ``{"int": "<decimal>"}`` and a float ``{"float": "<repr>"}``; ``repr`` of a
    float is the shortest text that reads back to the same bits, and it spells
    the two zeros differently.

    Raises:
        TypeError: The value has a type no generated model produces.
        ValueError: A float does not read back from its ``repr`` to itself.
    """
    if isinstance(value, bool | str):
        return value
    if isinstance(value, int):
        return {"int": str(value)}
    if isinstance(value, float):
        if float(repr(value)).hex() != value.hex():
            raise ValueError(f"{value!r} does not round-trip")
        return {"float": repr(value)}
    if isinstance(value, dict):
        return {str(key): canonical(item) for key, item in value.items()}
    raise TypeError(f"no canonical form for {type(value).__name__}")


def verdict(model: type[BaseModel], text: str) -> dict[str, Json]:
    """Validate ``text`` with ``model`` and return the verdict members of a row."""
    try:
        parsed = model.model_validate_json(text)
    except ValidationError as error:
        errors: list[Json] = []
        refused: dict[str, Json] = {"refused": errors}
        for detail in error.errors():
            errors.append([detail["type"], *detail["loc"]])
            if detail["type"] == "json_invalid":
                refused["json_error"] = detail["msg"]
        return refused
    return {"accepted": canonical(parsed.model_dump(by_alias=True))}


def row_classes(case: Case, result: Mapping[str, Json]) -> list[str]:
    """Return the classes of a row, in the order of ``CLASSES``.

    The classes are the ones the case was built to show. The verdict is used
    only to check them.

    Raises:
        ValueError: The verdict does not show a class of the case, or it shows
            a parser limit the case was not built for.
    """
    accepted = "accepted" in result
    message = str(result.get("json_error", ""))
    too_deep = "recursion limit" in message
    too_long = "number out of range" in message
    shown = {
        "internal-name": accepted or too_deep,
        "non-finite-token": accepted,
        "recursion-limit": too_deep,
        "negative-zero": accepted,
        "integer-length": too_long,
    }
    limits = {"recursion-limit": too_deep, "integer-length": too_long}
    wrong = [name for name in case.shows if not shown[name]]
    wrong += [name for name, hit in limits.items() if hit and name not in case.shows]
    if wrong:
        raise ValueError(f"{case.item}/{case.rest}: class and verdict differ: {wrong}")
    return [name for name in CLASSES if name in case.shows]


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

    Raises:
        ValueError: A required item has no row for a core model.
    """
    header: dict[str, Json] = {
        "format": FORMAT,
        "generator": Path(__file__).name,
        **check_environment(),
        "call": "model_validate_json(input) with input a str",
        "required_items": list(REQUIRED_ITEMS),
        "classes": list(CLASSES),
    }
    lines: list[str] = []
    summary: list[str] = []
    seen: Counter[tuple[str, str]] = Counter()
    totals: Counter[str] = Counter()
    all_specs = specs()
    for spec in all_specs:
        model = create_llm_output_model(spec.questions, spec.mode)
        spec_cases = cases(spec)
        questions: dict[str, Json] = {
            qid: question.model_dump(mode="json")
            for qid, question in spec.questions.items()
        }
        lines.append(
            dump(
                {
                    "model": spec.name,
                    "answer_mode": spec.mode,
                    "core": spec.core,
                    "questions": questions,
                    "rows": len(spec_cases),
                }
            )
        )
        accepted = 0
        for case in spec_cases:
            # A str with a lone surrogate has no UTF-8 form; no input may be one.
            case.text.encode("utf-8")
            result = verdict(model, case.text)
            row: dict[str, Json] = {"case": f"{case.item}/{case.rest}"}
            classes = row_classes(case, result)
            if classes:
                row["class"] = "+".join(classes)
            row["input"] = case.text
            row.update(result)
            extracted = _extract_json(case.text)
            if extracted != case.text:
                after = verdict(model, extracted)
                if after != result:
                    row["after_extract_json"] = after
            accepted += "accepted" in result
            seen[(spec.name, case.item)] += 1
            totals[case.item] += 1
            lines.append(dump(row))
        summary.append(
            f"{spec.name}: {len(spec_cases)} rows, {accepted} accepted, "
            f"{len(spec_cases) - accepted} refused"
        )
    core_names = [spec.name for spec in all_specs if spec.core]
    missing = [
        (name, item)
        for name in core_names
        for item in REQUIRED_ITEMS
        if not seen[(name, item)]
    ]
    if missing:
        raise ValueError(f"required items without a row: {missing}")
    for item in REQUIRED_ITEMS:
        per_model = ", ".join(f"{name} {seen[(name, item)]}" for name in core_names)
        summary.append(f"required item {item}: {per_model}")
    per_item = ", ".join(f"{item} {count}" for item, count in sorted(totals.items()))
    summary.append(f"rows per item: {per_item}")
    header["models"] = len(all_specs)
    header["rows"] = sum(totals.values())
    text = "\n".join([dump(header), *lines]) + "\n"
    summary.append(f"total: {header['rows']} rows, {len(text.encode())} bytes")
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
