#!/usr/bin/env -S uv run --script

# /// script
# requires-python = "==3.14.3"
# dependencies = [
#   "pydantic==2.13.4",
#   "pydantic-core==2.46.4",
# ]
# ///

"""Generate float vectors with CPython's results for the Go parity tests.

Each row holds one list of floats and what the reference CPython computes
from it: the builtin ``sum()``, and the probability, score and confidence
formulas of system-one-adapter-python v0.2.1 (MIT), which are built on
``sum()``. Every float is written as the 16 hexadecimal digits of its
IEEE 754 binary64 bit pattern, never as decimal text.

The output is a function of ``--seed`` and ``--count`` alone, so the same
command gives the same bytes. Row ``i`` does not depend on ``--count``: a
smaller run is a prefix of a larger one.

Run it on the reference versions::

    uv run --python 3.14.3 --with pydantic==2.13.4 \
        --with pydantic-core==2.46.4 testdata/python/gen_sum_vectors.py
"""

import argparse
import importlib.util
import random
import struct
import sys
from collections.abc import Callable, Sequence
from dataclasses import dataclass
from pathlib import Path
from types import ModuleType

import pydantic
import pydantic_core

FORMAT = "sum-vectors/1"
DEFAULT_SEED = 20261002
DEFAULT_COUNT = 1136
MAX_LENGTH = 20
PROBABILITY_TOLERANCE = 1e-6

COLUMNS = (
    "id",
    "class",
    "inputs",
    "sum",
    "error",
    "changed",
    "rescaled",
    "score_raw",
    "score_norm",
    "score_conf_raw",
    "score_conf_norm",
    "choice_conf_raw",
    "choice_conf_norm",
    "argmax_raw",
    "argmax_norm",
)

QUIET_NAN_BITS = (0x7FF8000000000000, 0xFFF8000000000000)
INF = float("inf")


def from_bits(bits: int) -> float:
    """Return the float whose IEEE 754 binary64 bit pattern is ``bits``."""
    value: float = struct.unpack("<d", struct.pack("<Q", bits))[0]
    return value


def to_hex(value: float) -> str:
    """Return the bit pattern of ``value`` as 16 hexadecimal digits."""
    bits: int = struct.unpack("<Q", struct.pack("<d", value))[0]
    return f"{bits:016x}"


def compose(sign: int, exponent: int, mantissa: int) -> float:
    """Build a float from a sign, an unbiased binary exponent and 52 bits.

    Args:
        sign: 0 for positive, 1 for negative.
        exponent: Unbiased exponent from -1023 (subnormal) to 1023.
        mantissa: The 52 fraction bits.

    Returns:
        The float with exactly those fields.
    """
    return from_bits((sign << 63) | ((exponent + 1023) << 52) | mantissa)


def random_float(rng: random.Random, low: int, high: int, sign: int | None) -> float:
    """Draw a float with a uniform binary exponent in ``[low, high]``.

    Args:
        rng: Source of randomness.
        low: Smallest unbiased exponent.
        high: Largest unbiased exponent.
        sign: 0 or 1 to fix the sign, ``None`` to draw it.

    Returns:
        A finite float with 52 random fraction bits.
    """
    sign_bit = rng.getrandbits(1) if sign is None else sign
    return compose(sign_bit, rng.randint(low, high), rng.getrandbits(52))


# The formulas below follow system-one-adapter-python v0.2.1 statement for
# statement: _utils/probability_normalization.py, _utils/confidence_metrics.py
# and the score lines of _client.py. ``--upstream-src`` checks them against the
# upstream files themselves.


def rescale_probabilities(probabilities: dict[str, float]) -> dict[str, float]:
    """Rescale probabilities to sum to 1, uniform for a zero total."""
    total = sum(probabilities.values())
    if total == 0:
        uniform_probability = 1.0 / len(probabilities)
        return dict.fromkeys(probabilities, uniform_probability)
    return {answer: value / total for answer, value in probabilities.items()}


def normalize(
    original: dict[str, float], *, enabled: bool
) -> tuple[dict[str, float], float, bool]:
    """Apply the probability branch of normalize_probabilities_of_all_answers.

    Args:
        original: Probabilities keyed by answer, in answer order.
        enabled: Whether an invalid distribution is rescaled.

    Returns:
        The resulting probabilities, the error of the original sum, and
        whether the distribution was replaced by its rescaled form.
    """
    total = sum(original.values())
    error = abs(total - 1.0)
    if not enabled or error <= PROBABILITY_TOLERANCE:
        return original, error, False
    return rescale_probabilities(original), error, True


def _normalize(probs: list[float]) -> list[float]:
    total = sum(probs)
    if total == 0:
        return [1.0 / len(probs)] * len(probs)
    return [probability / total for probability in probs]


def score_confidence(probs: list[float]) -> float:
    """Measure score concentration around its modal score."""
    if len(probs) == 1:
        return 1.0
    normalized = _normalize(probs)
    count = len(normalized)
    mode_index = max(range(count), key=normalized.__getitem__)
    distance_from_mode = sum(
        probability * abs(index - mode_index)
        for index, probability in enumerate(normalized)
    )
    uniform_center = (count - 1) / 2
    uniform_deviation = sum(abs(index - uniform_center) for index in range(count))
    return max(0.0, 1.0 - distance_from_mode / (uniform_deviation / count))


def choice_confidence(probs: list[float]) -> float:
    """Scale peak choice probability from uniform to certainty."""
    if len(probs) == 1:
        return 1.0
    normalized = _normalize(probs)
    uniform_probability = 1.0 / len(normalized)
    return (max(normalized) - uniform_probability) / (1.0 - uniform_probability)


def argmax(probabilities: dict[str, float]) -> int:
    """Return the index of the answer ``max(answers, key=...)`` selects."""
    return int(max(probabilities, key=probabilities.__getitem__))


@dataclass(frozen=True)
class Formulas:
    """The functions a row is computed with."""

    rescale: Callable[[dict[str, float]], dict[str, float]]
    normalize: Callable[[dict[str, float]], tuple[dict[str, float], float, bool]]
    score_confidence: Callable[[list[float]], float]
    choice_confidence: Callable[[list[float]], float]


def own_formulas() -> Formulas:
    """Return this file's copies of the upstream formulas."""
    return Formulas(
        rescale=rescale_probabilities,
        normalize=lambda original: normalize(original, enabled=True),
        score_confidence=score_confidence,
        choice_confidence=choice_confidence,
    )


def _load(path: Path, name: str) -> ModuleType:
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise FileNotFoundError(path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


def upstream_formulas(source: Path) -> Formulas:
    """Load the formulas from an upstream checkout.

    Args:
        source: The directory ``src/system_one_adapter/_utils`` of
            system-one-adapter-python.

    Returns:
        The upstream functions behind this file's calling convention.

    Raises:
        FileNotFoundError: If a module file is missing.
    """
    normalization = _load(source / "probability_normalization.py", "up_normalization")
    confidence = _load(source / "confidence_metrics.py", "up_confidence")

    def upstream_normalize(
        original: dict[str, float],
    ) -> tuple[dict[str, float], float, bool]:
        result = normalization.normalize_probabilities_of_all_answers(
            list(original), original, "probabilities", enabled=True
        )
        changed = result.original_probabilities is not None
        return result.probabilities, result.error, changed

    return Formulas(
        rescale=normalization.rescale_probabilities,
        normalize=upstream_normalize,
        score_confidence=confidence.score_confidence,
        choice_confidence=confidence.choice_confidence,
    )


FIXED: tuple[tuple[float, ...], ...] = (
    (1e16, 1.0, -1e16),
    (0.7, 0.2, 0.1),
    (0.1, 0.2, 0.3),
    (0.1,) * 10,
    (-0.0,),
    (0.0,),
    (-0.0, -0.0),
    (-0.0, 0.0),
    (0.0, -0.0),
    (1e308, 1e308),
    (1e308, 1e308, -1e308),
    (-1e308, -1e308),
    (INF,),
    (-INF,),
    (INF, -INF),
    (INF, 1.0, INF),
    (1e308, 1e308, -INF),
    (from_bits(QUIET_NAN_BITS[0]),),
    (1.0, from_bits(QUIET_NAN_BITS[0]), 2.0),
    (from_bits(QUIET_NAN_BITS[1]), INF),
    (5e-324,),
    (5e-324, -5e-324),
    (5e-324, 5e-324, 2.2250738585072014e-308),
    (1.0, 1e100, 1.0, -1e100),
    (1.0, 1e-16, -1.0),
    (-1.0, 1e-17, 1.0),
    (1.7976931348623157e308, 1.7976931348623157e308, -1.7976931348623157e308),
    (0.25, 0.25, 0.25, 0.25),
    (0.0, 0.0, 0.0),
    (0.5, 0.5),
    (1.0,),
    (0.3333333333333333, 0.3333333333333333, 0.3333333333333333),
    (0.9999999, 0.0),
    (0.999999, 0.0),
    (1.000001, 0.0),
    (1.0000011, 0.0),
)


def gen_wide(rng: random.Random, length: int) -> list[float]:
    """Both signs, magnitudes from about 1e-300 to 1e300."""
    return [random_float(rng, -996, 996, None) for _ in range(length)]


def gen_positive(rng: random.Random, length: int) -> list[float]:
    """Positive values, magnitudes from about 1e-300 to 1e300."""
    return [random_float(rng, -996, 996, 0) for _ in range(length)]


def gen_narrow(rng: random.Random, length: int) -> list[float]:
    """Both signs, magnitudes within a few binary orders of each other."""
    center = rng.randint(-990, 990)
    return [random_float(rng, center - 3, center + 3, None) for _ in range(length)]


def gen_cancel(rng: random.Random, length: int) -> list[float]:
    """Large values with their negations among small ones, shuffled."""
    values: list[float] = []
    while len(values) + 2 <= length and (not values or rng.random() < 0.6):
        big = random_float(rng, -60, 996, None)
        values += [big, -big]
    while len(values) < length:
        values.append(random_float(rng, -80, 60, None))
    rng.shuffle(values)
    return values


def gen_zeros(rng: random.Random, length: int) -> list[float]:
    """Zeros of both signs, alone or among a few other values."""
    kind = rng.randrange(4)
    values: list[float] = []
    for _ in range(length):
        if kind == 0:
            values.append(-0.0)
        elif kind == 1 or rng.random() < 0.7:
            values.append(rng.choice((0.0, -0.0)))
        else:
            values.append(random_float(rng, -40, 40, None if kind == 2 else 1))
    return values


def gen_inf(rng: random.Random, length: int) -> list[float]:
    """Finite values with one or more infinities of one or both signs."""
    values = gen_wide(rng, length)
    kind = rng.randrange(3)
    for index in rng.sample(range(length), rng.randint(1, min(3, length))):
        values[index] = (INF, -INF, rng.choice((INF, -INF)))[kind]
    return values


def gen_nan(rng: random.Random, length: int) -> list[float]:
    """Finite or infinite values with one or more quiet NaNs."""
    values = gen_wide(rng, length) if rng.random() < 0.7 else gen_inf(rng, length)
    for index in rng.sample(range(length), rng.randint(1, min(2, length))):
        values[index] = from_bits(rng.choice(QUIET_NAN_BITS))
    return values


def gen_subnormal(rng: random.Random, length: int) -> list[float]:
    """Subnormals and the smallest normal values, both signs."""
    values: list[float] = []
    for _ in range(length):
        if rng.random() < 0.7:
            fraction = rng.getrandbits(rng.randint(1, 52)) or 1
            values.append(compose(rng.getrandbits(1), -1023, fraction))
        else:
            values.append(random_float(rng, -1022, -1015, None))
    return values


def gen_overflow(rng: random.Random, length: int) -> list[float]:
    """Values near the largest double, so that running sums overflow."""
    sign = None if rng.random() < 0.6 else rng.getrandbits(1)
    values = [random_float(rng, 1020, 1023, sign) for _ in range(length)]
    if length > 2 and rng.random() < 0.3:
        values[rng.randrange(length)] = random_float(rng, -5, 5, None)
    return values


def gen_prob(rng: random.Random, length: int) -> list[float]:
    """Decimal probabilities that sum to about 1, as a model writes them."""
    weights = [rng.random() ** rng.choice((1, 3, 8)) + 1e-9 for _ in range(length)]
    total = sum(weights)
    digits = rng.choice((1, 2, 2, 3, 4, 6, 17))
    values = [round(weight / total, digits) for weight in weights]
    if rng.random() < 0.5:
        values[-1] = abs(round(1.0 - sum(values[:-1]), digits))
    return values


def gen_prob_off(rng: random.Random, length: int) -> list[float]:
    """Values in [0, 1] or percentages whose sum is not 1."""
    kind = rng.randrange(4)
    if kind == 0:
        return [round(rng.random(), rng.choice((1, 2, 3))) for _ in range(length)]
    if kind == 1:
        return [float(rng.randint(0, 100)) for _ in range(length)]
    if kind == 2:
        return [rng.choice((0.0, 0.0, 1.0, 0.5)) for _ in range(length)]
    return [rng.random() * rng.choice((1e-9, 1e-3, 1.0, 2.0)) for _ in range(length)]


CLASSES: tuple[tuple[str, Callable[[random.Random, int], list[float]]], ...] = (
    ("wide", gen_wide),
    ("positive", gen_positive),
    ("narrow", gen_narrow),
    ("cancel", gen_cancel),
    ("zeros", gen_zeros),
    ("inf", gen_inf),
    ("nan", gen_nan),
    ("subnormal", gen_subnormal),
    ("overflow", gen_overflow),
    ("prob", gen_prob),
    ("prob_off", gen_prob_off),
)


def vectors(seed: int, count: int) -> list[tuple[str, list[float]]]:
    """Return ``count`` named vectors: the fixed ones, then the generated.

    Generated row ``k`` has class ``k % len(CLASSES)`` and length
    ``1 + (k // len(CLASSES)) % MAX_LENGTH``, so every class meets every
    length once in each ``len(CLASSES) * MAX_LENGTH`` rows.

    Args:
        seed: Seed of the random generator.
        count: Number of rows, fixed rows included.

    Returns:
        Pairs of class name and vector.
    """
    rng = random.Random(seed)
    rows: list[tuple[str, list[float]]] = [("fixed", list(v)) for v in FIXED[:count]]
    for k in range(count - len(rows)):
        name, generate = CLASSES[k % len(CLASSES)]
        rows.append((name, generate(rng, 1 + (k // len(CLASSES)) % MAX_LENGTH)))
    return rows


def compute(values: Sequence[float], formulas: Formulas) -> list[str]:
    """Compute the result columns of one row.

    Args:
        values: The input vector.
        formulas: The functions to compute with.

    Returns:
        The columns from ``sum`` to ``argmax_norm``, as text.
    """
    raw = {str(index): value for index, value in enumerate(values)}
    norm, error, changed = formulas.normalize(dict(raw))
    rescaled = formulas.rescale(dict(raw))

    def score(probabilities: dict[str, float]) -> float:
        distribution = formulas.rescale(probabilities)
        return sum(index * distribution[str(index)] for index in range(len(values)))

    return [
        to_hex(sum(values)),
        to_hex(error),
        str(int(changed)),
        ",".join(to_hex(value) for value in rescaled.values()),
        to_hex(score(raw)),
        to_hex(score(norm)),
        to_hex(formulas.score_confidence(list(raw.values()))),
        to_hex(formulas.score_confidence(list(norm.values()))),
        to_hex(formulas.choice_confidence(list(raw.values()))),
        to_hex(formulas.choice_confidence(list(norm.values()))),
        str(argmax(raw)),
        str(argmax(norm)),
    ]


def render(seed: int, count: int, upstream: Formulas | None) -> str:
    """Render the vector file.

    Args:
        seed: Seed of the random generator.
        count: Number of rows.
        upstream: Upstream's functions; when given, every row is computed
            with them as well and must come out equal.

    Returns:
        The file's text.

    Raises:
        ValueError: If a row computed with upstream's functions differs.
    """
    lines = [
        f"# format: {FORMAT}",
        f"# python: {' '.join(sys.version.split())}",
        f"# pydantic: {pydantic.VERSION}",
        f"# pydantic_core: {pydantic_core.__version__}",
        f"# seed: {seed}",
        f"# count: {count}",
        f"# columns: {' '.join(COLUMNS)}",
    ]
    own = own_formulas()
    for row_id, (name, values) in enumerate(vectors(seed, count)):
        results = compute(values, own)
        if upstream is not None and compute(values, upstream) != results:
            raise ValueError(f"row {row_id} differs from upstream's functions")
        inputs = ",".join(to_hex(value) for value in values)
        lines.append("\t".join([str(row_id), name, inputs, *results]))
    return "\n".join(lines) + "\n"


def main() -> None:
    """Write the vector file."""
    parser = argparse.ArgumentParser(description="Write the sum() vector file.")
    parser.add_argument("--seed", type=int, default=DEFAULT_SEED)
    parser.add_argument("--count", type=int, default=DEFAULT_COUNT)
    parser.add_argument(
        "--output",
        type=Path,
        default=Path(__file__).with_name("sum_vectors.tsv"),
        help="file to write (default: sum_vectors.tsv beside this script)",
    )
    parser.add_argument(
        "--upstream-src",
        type=Path,
        default=None,
        help="upstream's src/system_one_adapter/_utils, to check the formulas",
    )
    arguments = parser.parse_args()
    upstream = (
        None
        if arguments.upstream_src is None
        else upstream_formulas(arguments.upstream_src)
    )
    text = render(arguments.seed, arguments.count, upstream)
    with arguments.output.open("w", encoding="ascii", newline="\n") as handle:
        handle.write(text)


if __name__ == "__main__":
    main()
