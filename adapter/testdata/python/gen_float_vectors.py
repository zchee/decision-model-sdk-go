#!/usr/bin/env -S uv run --script
# /// script
# requires-python = "==3.14.3"
# dependencies = [
#   "pydantic==2.13.4",
#   "pydantic-core==2.46.4",
# ]
# ///
"""Write float vectors: a double, Python's repr of it and pydantic-core's to_json.

Each data line has three tab-separated fields:

1. the IEEE 754 bit pattern of the double, 16 lowercase hex digits;
2. ``repr(x)``;
3. ``pydantic_core.to_json(x)``.

Fields 2 and 3 are ASCII for every double, which the script checks, so they are
stored as they are. Lines that start with ``#`` are the header; it names the
interpreter and the library versions that produced the file.

For every finite double the script also checks that ``json.dumps(x)`` equals
``repr(x)``, so field 2 is the response-body spelling as well.

The same seed and count give the same bytes.
"""

from __future__ import annotations

import argparse
import json
import math
import random
import struct
import sys
from collections.abc import Iterator
from pathlib import Path

import pydantic
import pydantic_core

DEFAULT_SEED = 20261002
DEFAULT_COUNT = 6000
DEFAULT_OUTPUT = Path(__file__).resolve().parent / "float_vectors.tsv"

MIN_DECIMAL_EXPONENT = -323
MAX_DECIMAL_EXPONENT = 308
SIGN_BIT = 1 << 63

# Doubles that shortest-digit algorithms are known to get wrong when they are
# implemented carelessly (the test tables of Ryu and Grisu, David Gay's notes).
HARD_CASES = (
    "5e-324",
    "1.7976931348623157e308",
    "2.2250738585072014e-308",
    "2.225073858507201e-308",
    "9007199254740993.0",
    "0.30000000000000004",
    "0.3333333333333333",
    "0.6666666666666666",
    "1e23",
    "9.999999999999999e22",
    "8.41e21",
    "5e-324",
    "9.5e-322",
    "4.35",
    "0.3",
    "2.5",
    "1.8531501765868567e21",
    "-3.347727380279489e33",
    "1.9430376160308388e16",
    "-6.9741824662760956e19",
    "4.3816050601147837e18",
    "9.0608011534336e15",
    "4.708356024711512e18",
    "9.409340012568248e18",
    "1.2345678",
    "2.98023223876953125e-8",
    "5.764607523034235e39",
    "1.152921504606847e40",
    "2.305843009213694e40",
    "1.2e1",
    "1.23e2",
    "123456789012345680.0",
    "1.2345678901234567e16",
    "4.940656e-318",
    "1.18575755e-316",
    "2.989102097996e-312",
    "9.0608011534336e15",
    "4.294967294",
    "4.294967295",
    "4.294967296",
    "4.294967297",
    "4.294967298",
    "0.1",
    "0.2",
    "0.7",
    "123456.789",
    "1.7976931348623157e308",
    "4.9406564584124654e-324",
    "1.2345e-10",
    "299792458.0",
    "6.02214076e23",
    "6.62607015e-34",
)


def from_bits(bits: int) -> float:
    """Return the double whose IEEE 754 bit pattern is bits."""
    value: float = struct.unpack(">d", struct.pack(">Q", bits))[0]
    return value


def to_bits(value: float) -> int:
    """Return the IEEE 754 bit pattern of value."""
    bits: int = struct.unpack(">Q", struct.pack(">d", value))[0]
    return bits


def neighbours(value: float, steps: int) -> Iterator[float]:
    """Yield value and the doubles up to steps away on both sides of it."""
    yield value
    below = above = value
    for _ in range(steps):
        below = math.nextafter(below, -math.inf)
        above = math.nextafter(above, math.inf)
        yield below
        yield above


def special_values() -> Iterator[float]:
    """Yield both zeros, both infinities and three NaN bit patterns."""
    yield 0.0
    yield -0.0
    yield math.inf
    yield -math.inf
    yield from_bits(0x7FF8000000000000)
    yield from_bits(0xFFF8000000000000)
    yield from_bits(0x7FF0000000000001)


def decades() -> Iterator[float]:
    """Yield every power of ten a double can hold, positive and negative."""
    for exponent in range(MIN_DECIMAL_EXPONENT, MAX_DECIMAL_EXPONENT + 1):
        value = float(f"1e{exponent}")
        yield value
        yield -value


def notation_switches() -> Iterator[float]:
    """Yield doubles around the exponents where either writer changes notation.

    repr switches at 1e-4 and 1e16, to_json at 1e-5 and 1e16; Go's own 'g'
    format and JavaScript switch at 1e-6, 1e-7 and 1e21, so those are covered too.
    """
    for exponent in (*range(-9, -2), *range(13, 24)):
        for digit in range(1, 10):
            for value in neighbours(float(f"{digit}e{exponent}"), 1):
                yield value
                yield -value
        for text in (f"9.999999999999999e{exponent}", f"1.0000000000000002e{exponent}"):
            yield float(text)
        yield float(f"1.5e{exponent}")
        yield float(f"-1.25e{exponent}")
        yield float(f"1.2345678901234567e{exponent}")


def integers() -> Iterator[float]:
    """Yield integral doubles: small ones, around 2**53 and exact powers of ten."""
    for number in range(33):
        yield float(number)
        yield float(-number)
    for exponent in range(23):
        value = float(10**exponent)
        yield value
        yield value + 1.0
        yield value - 1.0
        yield -value
    for offset in range(-4, 5):
        yield float(2**53 + offset)
        yield float(-(2**53) + offset)
    for number in (2**31, 2**32, 2**63, 2**64, 10**15 + 1, 10**16 + 2, 123456789):
        yield float(number)
        yield float(number - 1)
        yield -float(number)


def powers_of_two() -> Iterator[float]:
    """Yield powers of two from 2**-1074 to 2**1023.

    Every subnormal one, every one from 2**-64 to 2**70, and every eighth one
    of the rest; every_binary_exponent covers each exponent field once more.
    """
    for exponent in range(-1074, 1024):
        if exponent < -1021 or -64 <= exponent <= 70 or exponent % 8 == 0:
            yield math.ldexp(1.0, exponent)


def subnormals() -> Iterator[float]:
    """Yield the smallest subnormals and the doubles around the first normal one."""
    for multiple in range(1, 21):
        yield from_bits(multiple)
        yield from_bits(multiple | SIGN_BIT)
    yield from neighbours(from_bits(0x0010000000000000), 3)
    yield from_bits(0x000FFFFFFFFFFFFF)
    yield from_bits(0x0008000000000000)
    yield from neighbours(sys.float_info.max, 0)
    yield math.nextafter(sys.float_info.max, 0.0)


def hard_cases() -> Iterator[float]:
    """Yield the doubles of HARD_CASES."""
    for text in HARD_CASES:
        yield float(text)


def every_binary_exponent(rng: random.Random) -> Iterator[float]:
    """Yield one double with a random significand for each finite exponent field."""
    for exponent_field in range(2047):
        bits = (exponent_field << 52) | rng.getrandbits(52)
        if rng.getrandbits(1):
            bits |= SIGN_BIT
        yield from_bits(bits)


def digit_lengths(rng: random.Random) -> Iterator[float]:
    """Yield decimals of 1 to 17 significant digits at random exponents."""
    for length in range(1, 18):
        for _ in range(12):
            digits = str(rng.randrange(1, 10))
            digits += "".join(str(rng.randrange(10)) for _ in range(length - 1))
            exponent = rng.randrange(-330, 310)
            yield float(f"{digits[0]}.{digits[1:] or '0'}e{exponent}")


def random_values(rng: random.Random) -> Iterator[float]:
    """Yield random doubles for ever: four kinds in turn.

    The kinds are a uniform 64-bit pattern, a short decimal near the notation
    switches, a decimal of random length and exponent, and an integral double.
    """
    while True:
        yield from_bits(rng.getrandbits(64))
        digits = rng.randrange(1, 10**6)
        sign = "-" if rng.getrandbits(1) else ""
        yield float(f"{sign}{digits}e{rng.randrange(-14, 20)}")
        mantissa = rng.randrange(1, 10 ** rng.randrange(1, 18))
        yield float(f"{mantissa}e{rng.randrange(-340, 300)}")
        yield float(rng.randrange(-(2**63), 2**63))


def collect(seed: int, count: int) -> list[int]:
    """Return count distinct bit patterns: every fixed class, then random ones.

    Raises:
        ValueError: If count is smaller than the number of fixed vectors.
    """
    rng = random.Random(seed)
    seen: set[int] = set()
    ordered: list[int] = []

    def add(value: float) -> None:
        bits = to_bits(value)
        if bits not in seen:
            seen.add(bits)
            ordered.append(bits)

    fixed = (
        special_values(),
        decades(),
        notation_switches(),
        integers(),
        powers_of_two(),
        subnormals(),
        hard_cases(),
        every_binary_exponent(rng),
        digit_lengths(rng),
    )
    for source in fixed:
        for value in source:
            add(value)
    if len(ordered) > count:
        raise ValueError(
            f"count {count} is smaller than the {len(ordered)} fixed vectors"
        )
    for value in random_values(rng):
        if len(ordered) == count:
            break
        add(value)
    return ordered


def render(seed: int, count: int) -> str:
    """Return the whole vector file for seed and count.

    Raises:
        ValueError: If an output is not ASCII, or json.dumps and repr differ on a
            finite double.
    """
    lines = [
        (
            "# float vectors: bits (hex of the IEEE 754 double), repr(x), "
            "pydantic_core.to_json(x); tab-separated"
        ),
        f"# python: {sys.version}",
        f"# pydantic: {pydantic.VERSION}",
        f"# pydantic_core: {pydantic_core.__version__}",
        f"# seed: {seed}",
        f"# count: {count}",
    ]
    for bits in collect(seed, count):
        value = from_bits(bits)
        text = repr(value)
        encoded = pydantic_core.to_json(value).decode("ascii")
        if not text.isascii():
            raise ValueError(f"repr of {bits:016x} is not ASCII")
        if math.isfinite(value) and json.dumps(value) != text:
            raise ValueError(f"json.dumps and repr differ on {bits:016x}")
        lines.append(f"{bits:016x}\t{text}\t{encoded}")
    return "\n".join(lines) + "\n"


def main() -> None:
    """Parse the arguments and write the vector file."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--seed", type=int, default=DEFAULT_SEED)
    parser.add_argument("--count", type=int, default=DEFAULT_COUNT)
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    arguments = parser.parse_args()
    content = render(arguments.seed, arguments.count)
    arguments.output.write_bytes(content.encode("ascii"))


if __name__ == "__main__":
    main()
