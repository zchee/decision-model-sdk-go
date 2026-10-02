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
"""Write upstream's usage and debug data for its fake-provider scenarios as a table.

The scenarios are those of tests/test_client_with_fake_model.py at
system-one-adapter 0.2.1 whose usage and debug data the Go port reproduces:
``test_malformed_retry_exhaustion_preserves_debug`` (FM6),
``test_usage_totals_preserve_unknown_counts_across_corrections`` (FM7),
``test_usage_separates_last_attempt_from_cumulative_totals`` (FM8),
``test_attempts_are_independent_and_replayable`` (FM9, its first call) and
``test_malformed_structure_is_retried`` (FM11), each with every parameter of
the synchronous client. Each runs through upstream's ``SystemOneAdapterClient``
with a port of the test file's scripted provider (lines 41-87), so the data is
what upstream's own code writes.

The output is JSON Lines, in ASCII:

- line 1, the header: the versions and the format (its first member is
  ``format``);
- a row (first member ``case``, named ``<row>/<parameters>``): ``scenario``,
  everything a port needs to run the same evaluation (the questions and the
  state as JSON values, the client's settings, the retry policy or null, the
  provider's usage and its script), then either ``response``, upstream's
  ``model_dump(mode="json")`` of the response with ``usage.latency`` removed,
  or ``error``, the class of the error the call raised and its ``debug``
  attribute.

Five kinds of value are replaced, the same way in every row:

- a message's ``content`` by ``sha256:<hex>:<length>``, the SHA-256 of its
  UTF-8 bytes and their number, so that the table stays small; the prompts are
  pinned byte for byte elsewhere (schema_cases.jsonl);
- ``model_request_parameters.schema`` by the same form of ``to_json(schema)``,
  the text upstream puts into a prompted request's system prompt;
- the content of a correction prompt (a user message after an assistant
  message) by ``correction``, and the message of a ``malformed_structure``
  retry reason by ``validation``: both carry pydantic's error text, which the
  port replaces with its own validator's;
- ``debug_info.provider`` by ``provider``: the class path of the scripted
  provider here, the Go type in the port.

A script step is ``{"text": \u2026}`` (the provider's text with its usage),
``{"result": {"text", "input_tokens", "output_tokens"}}`` (a result as it is)
or ``{"status": <code>}`` (the error the test file's ``_provider_error``
builds: the SDK's error for that status with the body
``{"message": "unavailable"}``). The last step repeats once the script is
exhausted.

The table has no random part. The script refuses to run on other versions, or
on a ``system_one_adapter`` whose sources differ from the release.

Usage::

    uv run testdata/python/gen_report_cases.py            # write the table
    uv run testdata/python/gen_report_cases.py --check    # compare, no write
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from importlib import metadata
from pathlib import Path
from typing import Any, Literal, TypeAlias

import httpx2
import pydantic
import pydantic_core
import system_one_adapter
from pydantic_core import to_json
from system_one_adapter import RetryPolicy, SystemOneAdapterClient
from system_one_adapter.providers import Message, ProviderResult
from typesafe_sdk import TypeSafeError
from typesafe_sdk._core.errors import api_error

Json: TypeAlias = bool | int | float | str | None | list["Json"] | dict[str, "Json"]
AnswerMode: TypeAlias = Literal["probabilities", "discrete"]

DESCRIPTION = "Write upstream's usage and debug data for its fake-provider scenarios."
FORMAT = 1
DEFAULT_OUTPUT = Path(__file__).resolve().parent / "report_cases.jsonl"

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

# The questions of the test file (lines 25-33), in their dictionary form.
POSITIVE: dict[str, Json] = {"type": "noul", "instructions": "The review is positive."}
GENRE: dict[str, Json] = {
    "type": "choice",
    "instructions": "Genre.",
    "criteria": {"fiction": "A story.", "nonfiction": "Facts."},
}


class ScriptedProvider:
    """A port of the test file's synchronous scripted provider (lines 41-87)."""

    def __init__(self, steps: Sequence[object], usage: tuple[int, int]) -> None:
        """Keep the script and the usage of each answered call.

        Args:
            steps: Results, raw strings, or exceptions to raise; the last one
                repeats once the script is exhausted.
            usage: Input and output token counts of each answered call.
        """
        self.model_name = "fake-model"
        self._steps = list(steps)
        self._usage = usage
        self.calls: list[list[Message]] = []

    def request(
        self,
        messages: list[Message],
        *,
        schema: dict[str, Any],
        structured: bool,
    ) -> ProviderResult:
        """Answer with the next step of the script."""
        self.calls.append(messages)
        step = self._steps[min(len(self.calls) - 1, len(self._steps) - 1)]
        if isinstance(step, Exception):
            raise step
        if isinstance(step, ProviderResult):
            return step
        input_tokens, output_tokens = self._usage
        return ProviderResult(
            text=str(step), input_tokens=input_tokens, output_tokens=output_tokens
        )

    def translate_error(self, error: Exception) -> TypeSafeError:
        """Never called: the script raises translated errors."""
        return error if isinstance(error, TypeSafeError) else TypeSafeError(str(error))

    def close(self) -> None:
        """Release nothing."""


@dataclass(frozen=True)
class Scenario:
    """One call of a test function with its parameters.

    Attributes:
        name: ``<row>/<parameters>``.
        questions: The question set.
        state: The state.
        structured: The client's ``structured_outputs``.
        mode: The client's ``llm_answer_mode``.
        malformed: The client's ``n_retry_malformed_structure``.
        retry: ``max_retries``, ``backoff_initial`` and ``backoff_jitter`` of the
            client's retry policy, or ``None`` for upstream's default.
        steps: The script, in the table's form.
        usage: The usage of each answered call.
    """

    name: str
    questions: Mapping[str, Json]
    state: Json
    structured: bool
    mode: AnswerMode
    malformed: int
    steps: Sequence[Json]
    retry: tuple[int, float, float] | None = None
    usage: tuple[int, int] = field(default=(11, 7))


def answers(value: Json) -> Json:
    """Return the step answering with ``{"answers": value}``, as to_json writes it."""
    return {"text": to_json({"answers": value}).decode()}


def scenarios() -> list[Scenario]:
    """Return every scenario, in the table's order."""
    one = {"answer": POSITIVE}
    built: list[Scenario] = []
    fm6: list[tuple[str, Json]] = [
        ("missing-answer", answers({})),
        ("truncated-json", {"text": '{"answers":'}),
    ]
    for in_name, step in fm6:
        for malformed in (0, 2):
            built.append(
                Scenario(
                    f"FM6/{in_name}-{malformed}",
                    one,
                    "state",
                    True,
                    "probabilities",
                    malformed,
                    [step],
                )
            )
    counts: list[tuple[list[tuple[int | None, int | None]], str]] = [
        ([(10, 4), (12, 7)], "known"),
        ([(None, None), (12, 7)], "unknown-first"),
        ([(12, 7), (None, None)], "unknown-last"),
        ([(None, None), (None, None)], "unknown-both"),
        ([(10, 4), (None, 2), (7, 3)], "unknown-input-middle"),
        ([(10, 4), (5, None), (7, 3)], "unknown-output-middle"),
        ([(None, 4), (12, None)], "unknown-crossed"),
    ]
    for case_counts, case_name in counts:
        steps: list[Json] = [
            {
                "result": {
                    "text": '{"answers":{"answer":0.75}}'
                    if index == len(case_counts) - 1
                    else '{"answers":',
                    "input_tokens": input_tokens,
                    "output_tokens": output_tokens,
                }
            }
            for index, (input_tokens, output_tokens) in enumerate(case_counts)
        ]
        built.append(
            Scenario(
                f"FM7/{case_name}",
                one,
                "state",
                True,
                "probabilities",
                len(case_counts) - 1,
                steps,
            )
        )
    built.append(
        Scenario(
            "FM8/malformed-transient-success",
            one,
            "state",
            True,
            "probabilities",
            1,
            [answers("not-an-object"), {"status": 503}, answers({"answer": 0.75})],
            retry=(1, 0.001, 0.0),
            usage=(100, 50),
        )
    )
    built.append(
        Scenario(
            "FM9/first-document",
            one,
            "first document",
            False,
            "probabilities",
            0,
            [answers({"answer": 0.75})],
        )
    )
    fm11: list[tuple[str, Mapping[str, Json], Json, Json]] = [
        ("missing-answer", one, answers({}), {"answer": 0.75}),
        (
            "missing-probability-key",
            {"genre": GENRE},
            answers({"genre": {"fiction": 0.5}}),
            {"genre": {"fiction": 0.5, "nonfiction": 0.5}},
        ),
        ("truncated-json", one, {"text": '{"answers":'}, {"answer": 0.75}),
        (
            "invalid-json",
            one,
            {"text": '{"answers": {"answer": nope}}'},
            {"answer": 0.75},
        ),
    ]
    for case_name, case_questions, malformed_step, valid in fm11:
        built.append(
            Scenario(
                f"FM11/{case_name}",
                case_questions,
                "state",
                False,
                "probabilities",
                1,
                [malformed_step, answers(valid)],
            )
        )
    return built


def provider_step(step: Json) -> object:
    """Return the scripted provider's step for a table step."""
    if not isinstance(step, dict):
        raise TypeError(step)
    if "status" in step:
        status = step["status"]
        if not isinstance(status, int):
            raise TypeError(step)
        return api_error(status, {"message": "unavailable"}, httpx2.Headers())
    if "text" in step:
        return step["text"]
    result = step["result"]
    if not isinstance(result, dict):
        raise TypeError(step)
    text, tokens_in, tokens_out = (
        result[k] for k in ("text", "input_tokens", "output_tokens")
    )
    if not isinstance(text, str):
        raise TypeError(step)
    if not isinstance(tokens_in, int | None) or not isinstance(tokens_out, int | None):
        raise TypeError(step)
    return ProviderResult(text=text, input_tokens=tokens_in, output_tokens=tokens_out)


def digest_of(data: bytes) -> str:
    """Return ``sha256:<hex>:<length>`` of data."""
    return f"sha256:{hashlib.sha256(data).hexdigest()}:{len(data)}"


def mask_attempts(attempts: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Replace message contents, schemas, correction prompts and provider paths."""
    for attempt in attempts:
        parameters = attempt["model_request_parameters"]
        parameters["schema"] = digest_of(to_json(parameters["schema"]))
        previous_role = ""
        for message in attempt["messages"]:
            content = message["content"]
            if message["role"] == "user" and previous_role == "assistant":
                message["content"] = "correction"
            else:
                message["content"] = digest_of(content.encode())
            previous_role = message["role"]
        attempt["debug_info"]["provider"] = "provider"
    return attempts


def mask_reasons(reasons: Sequence[Sequence[str]]) -> list[list[str]]:
    """Replace the message of every malformed_structure retry reason."""
    return [
        [category, "validation" if category == "malformed_structure" else message]
        for category, message in reasons
    ]


def run(scenario: Scenario) -> dict[str, Json]:
    """Run one scenario through upstream's client and return its row's verdict."""
    provider = ScriptedProvider(
        [provider_step(step) for step in scenario.steps],
        scenario.usage,
    )
    retry = (
        None
        if scenario.retry is None
        else RetryPolicy(
            max_retries=scenario.retry[0],
            backoff_initial=scenario.retry[1],
            backoff_jitter=scenario.retry[2],
        )
    )
    client = SystemOneAdapterClient(
        structured_outputs=scenario.structured,
        llm_answer_mode=scenario.mode,
        n_retry_malformed_structure=scenario.malformed,
        retry=retry,
    )
    try:
        response = client.system_one(
            scenario.state,  # type: ignore[arg-type]
            dict(scenario.questions),  # type: ignore[arg-type]
            model=provider,
        )
    except TypeSafeError as error:
        debug: dict[str, Any] = json.loads(json.dumps(error.debug))  # type: ignore[attr-defined]
        debug["llm_attempts"] = mask_attempts(debug["llm_attempts"])
        debug["retry_reasons"] = mask_reasons(debug["retry_reasons"])
        return {"error": {"class": type(error).__name__, "debug": debug}}
    dumped: dict[str, Any] = response.model_dump(mode="json")
    del dumped["usage"]["latency"]
    dumped["debug"]["llm_attempts"] = mask_attempts(dumped["debug"]["llm_attempts"])
    dumped["debug"]["retry_reasons"] = mask_reasons(dumped["debug"]["retry_reasons"])
    out: dict[str, Json] = json.loads(json.dumps(dumped))
    return {"response": out}


def scenario_json(scenario: Scenario) -> dict[str, Json]:
    """Return what a port needs to run the scenario."""
    retry: Json = None
    if scenario.retry is not None:
        retry = {
            "max_retries": scenario.retry[0],
            "backoff_initial": scenario.retry[1],
            "backoff_jitter": scenario.retry[2],
        }
    return {
        "questions": dict(scenario.questions),
        "state": scenario.state,
        "structured_outputs": scenario.structured,
        "llm_answer_mode": scenario.mode,
        "n_retry_malformed_structure": scenario.malformed,
        "retry": retry,
        "usage": list(scenario.usage),
        "steps": list(scenario.steps),
    }


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
    all_scenarios = scenarios()
    names = [scenario.name for scenario in all_scenarios]
    if len(set(names)) != len(names):
        raise ValueError("two scenarios have one name")
    lines: list[str] = []
    errors = 0
    for scenario in all_scenarios:
        row: dict[str, Json] = {
            "case": scenario.name,
            "scenario": scenario_json(scenario),
        }
        row.update(run(scenario))
        errors += "error" in row
        lines.append(dump(row))
    header: dict[str, Json] = {
        "format": FORMAT,
        "generator": Path(__file__).name,
        **check_environment(),
        "masks": ["sha256:<hex>:<length>", "correction", "validation", "provider"],
        "rows": len(all_scenarios),
    }
    text = "\n".join([dump(header), *lines]) + "\n"
    responses = len(all_scenarios) - errors
    summary = [
        f"rows: {len(all_scenarios)}, {responses} responses, {errors} errors",
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
