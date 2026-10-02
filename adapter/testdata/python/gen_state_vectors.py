#!/usr/bin/env -S uv run --script
# /// script
# requires-python = "==3.14.3"
# dependencies = [
#   "pydantic==2.13.4",
#   "pydantic-core==2.46.4",
# ]
# ///
r"""Write state vectors: a JSON document and what Python makes of it.

The reference is ``pydantic_core.to_json(json.loads(document.decode("utf-8")))``:
the document's bytes are decoded as UTF-8 first, as a request body is, so
``json.loads`` receives a ``str``.

Each data line has five tab-separated fields:

1. the document's bytes, base64;
2. the verdict: ``ok``; ``decode:UnicodeDecodeError`` when the bytes are not
   UTF-8; ``loads:<exception type>`` when ``json.loads`` refuses the text; or
   ``to_json:<exception type>:<cause>`` when ``pydantic_core.to_json`` refuses the
   loaded value;
3. the class: ``-`` when a strict reader of JSON in UTF-8 and Python agree about
   the document, otherwise one of the names below;
4. the bytes ``to_json`` returns, base64, or ``-`` when Python refuses;
5. field 4 as text with every ``<`` replaced by ``\u003c`` and every ``>`` by
   ``\u003e``, base64; ``=`` when it equals field 4, ``-`` when Python refuses.

The classes, computed here without any other reader:

``nan-token``
    Python accepts the document, and it holds one of the tokens ``NaN``,
    ``Infinity`` and ``-Infinity``, which are not JSON.
``replaced-lone-surrogate``
    Python accepts the document, and it holds an escaped surrogate without its
    partner inside a value that a later member of the same name replaces.
    ``to_json`` would refuse the surrogate, but ``json.loads`` has dropped the
    value; a reader that checks strings while reading refuses the document.
``read-depth``
    Python accepts the document, and its text opens more than
    ``READER_DEPTH_LIMIT`` containers inside one another. Python accepts such
    a text only when a later member of the same name replaces the deep value;
    a reader with that limit refuses the document before it sees the
    replacement.
``two-defects``
    Python refuses the document for one reason and the document has a second
    defect of another kind, so a reader that meets them in another order
    refuses it for the other reason.

A document Python accepts can be in more than one of the first three classes;
the names are then joined with ``+`` in the order above.

Base64 (RFC 4648, with padding) keeps every byte: a document holds control
characters, tabs, newlines and byte sequences that are not UTF-8, and the
expected output holds raw non-ASCII text, so neither can be a field of a
line-oriented text file as it is. Lines that start with ``#`` are the header.

The same seed and count give the same bytes.
"""

from __future__ import annotations

import argparse
import base64
import json
import math
import random
import re
import struct
import sys
from collections.abc import Iterator
from dataclasses import dataclass
from pathlib import Path

import pydantic
import pydantic_core

DEFAULT_SEED = 20261002
DEFAULT_COUNT = 1500
DEFAULT_OUTPUT = Path(__file__).resolve().parent / "state_vectors.tsv"

# The nesting limit of the JSON reader the Go port uses (maxNestingDepth of
# jsontext in github.com/go-json-experiment/json and in Go 1.27's standard
# library): it refuses a text that opens more than this many arrays and objects
# inside one another. json.loads has no fixed limit.
READER_DEPTH_LIMIT = 10000

SHORT_ESCAPES = {0x08: "\\b", 0x09: "\\t", 0x0A: "\\n", 0x0C: "\\f", 0x0D: "\\r"}

# Raw non-ASCII samples: the first and last code point of each UTF-8 length, the
# line separators, a BOM inside text, the replacement character, noncharacters
# and a combining mark.
NON_ASCII = (
    "\u0080",
    "\u00a0",
    "\u00e9",
    "\u00df",
    "\u07ff",
    "\u0800",
    "\u20ac",
    "\u2028",
    "\u2029",
    "\u65e5\u672c\u8a9e",
    "\ufdd0",
    "\ufeff",
    "\ufffd",
    "\ufffe",
    "\uffff",
    "\U00010000",
    "\U0001f600",
    "\U0010ffff",
    "e\u0301",
)

# Number spellings: every form of the JSON grammar, the spellings Go writers
# produce (integral floats without a fraction, a sign and no padding in the
# exponent), and the edges of the float parser and of both float writers.
NUMBERS = (
    "0",
    "-0",
    "1",
    "-1",
    "3",
    "42",
    "-17",
    "3.0",
    "0.0",
    "-0.0",
    "0e0",
    "-0e-0",
    "0E+0",
    "1.50",
    "1.5",
    "-2.50",
    "1E5",
    "1e5",
    "1e+5",
    "1E+5",
    "1e-5",
    "1E-5",
    "1.0e0",
    "0.1e1",
    "0.1e-0",
    "100e-2",
    "1E+2",
    "1e400",
    "-1e400",
    "1E400",
    "-1E+400",
    "1e5000",
    "1e-400",
    "-1e-400",
    "1e-7",
    "1.5e-7",
    "1e-6",
    "0.000001",
    "0.0000001",
    "0.00001",
    "0.0001",
    "0.00009999",
    "1e15",
    "1e16",
    "1e+16",
    "1e17",
    "1e20",
    "1e21",
    "1e+21",
    "1e22",
    "1e23",
    "1000000000000000.0",
    "10000000000000000.0",
    "100000000000000000000",
    "100000000000000000000.0",
    "123456789.0e10",
    "0.1",
    "0.2",
    "0.30000000000000004",
    "3.141592653589793",
    "2.718281828459045e0",
    "5e-324",
    "4.9e-324",
    "2.4703282292062327e-324",
    "2.4703282292062328e-324",
    "2.2250738585072014e-308",
    "1.7976931348623157e308",
    "1.7976931348623158e308",
    "1.7976931348623159e308",
    "9007199254740992",
    "9007199254740993",
    "9007199254740993.0",
    "9007199254740992.0",
    "123456789012345678901234567890.0",
    "0.123456789012345678901234567890",
    "1.00000000000000000000000000000000000001",
    "9223372036854775807",
    "9223372036854775808",
    "-9223372036854775808",
    "-9223372036854775809",
    "18446744073709551615",
    "18446744073709551616",
    "18446744073709551617",
    "12345678901234567890123",
    "-12345678901234567890123",
    "1" + "0" * 39,
    "9" * 100,
    "0." + "0" * 400 + "1",
    "1" + "0" * 400 + ".0",
    "1" + "0" * 400,
)

# Documents json.loads refuses. A Go decoder must refuse each of them too.
REFUSED = (
    b"",
    b" ",
    b"+1",
    b"01",
    b"-01",
    b"1.",
    b".5",
    b"-",
    b"1e",
    b"1e+",
    b"0x10",
    b"1_000",
    b"nan",
    b"-NaN",
    b"-infinity",
    b"inf",
    b"True",
    b"NULL",
    b"nul",
    b"[1,]",
    b"[,1]",
    b"[1 2]",
    b'{"a":1,}',
    b'{"a"}',
    b'{"a" 1}',
    b"{1:2}",
    b"{a:1}",
    b"'a'",
    b"[",
    b"]",
    b"{",
    b'{"a":1',
    b'"abc',
    b'"\\',
    b'"\\x41"',
    b'"\\u12G4"',
    b'"\\u12"',
    b'"\\U0001F600"',
    b"1 2",
    b"[1] x",
    b"[1]]",
    b"//c\n1",
    b"/* c */ 1",
    b"\x0c1",
    b"\xc2\xa01",
    b'"\xff"',
    b'"\xc0\xaf"',
    b'"\xe2\x82"',
    b'"\xf8\x88\x80\x80\x80"',
    b'{"\xff":1}',
)

# Documents that are not RFC 8259 JSON texts in UTF-8. json.loads takes its
# three extra number tokens. The other encodings, which json.loads would detect
# if it were given bytes, are refused by the UTF-8 decoding or by the parser.
PYTHON_ONLY = (
    b"NaN",
    b"Infinity",
    b"-Infinity",
    b"[NaN, Infinity, -Infinity]",
    b'{"a":NaN}',
    b'{"a":[1,-Infinity]}',
    b"\xef\xbb\xbf[1]",
    b'\xef\xbb\xbf{"a":"b"}',
    "[1]".encode("utf-16"),
    "[1]".encode("utf-16-le"),
    "[1]".encode("utf-16-be"),
    "[1]".encode("utf-32"),
    "[1]".encode("utf-32-le"),
    "[1]".encode("utf-32-be"),
)


# One JSON escape sequence: a four-digit escape (group 1 holds the digits) or a
# backslash and one other character.
ESCAPE = re.compile(r"\\(?:u([0-9a-fA-F]{4})|.)", re.DOTALL)

# An integer literal with more digits than the interpreter converts.
OVERSIZED_INTEGER = re.compile(rb"(?<![0-9.eE+])[0-9]{4301,}(?![0-9.eE])")

# A JSON string token, closed or cut off by the end of the text, and the four
# characters that open and close a container.
STRING_TOKEN = re.compile(rb'"(?:[^"\\]|\\.)*"?', re.DOTALL)
BRACKET = re.compile(rb"[\[\]{}]")


def unicode_escape(code_unit: int, upper: bool) -> str:
    r"""Return the six-character JSON escape \uXXXX of one UTF-16 code unit."""
    digits = f"{code_unit:04X}" if upper else f"{code_unit:04x}"
    return f"\\u{digits}"


def surrogate_pair(code_point: int, upper: bool) -> str:
    """Return the two JSON escapes of a code point above U+FFFF."""
    offset = code_point - 0x10000
    high = 0xD800 + (offset >> 10)
    low = 0xDC00 + (offset & 0x3FF)
    return unicode_escape(high, upper) + unicode_escape(low, upper)


def control_characters() -> Iterator[bytes]:
    """Yield every control character in each spelling, in a value and in a name."""
    for code in range(0x20):
        lower = unicode_escape(code, upper=False)
        upper = unicode_escape(code, upper=True)
        yield f'"{lower}"'.encode()
        if upper != lower:
            yield f'"{upper}"'.encode()
        if code in SHORT_ESCAPES:
            yield f'"{SHORT_ESCAPES[code]}"'.encode()
        yield f'{{"k{lower}":"a{lower}b"}}'.encode()
        yield b'"' + bytes([code]) + b'"'
    every = "".join(unicode_escape(code, upper=False) for code in range(0x20))
    yield f'"{every}"'.encode()
    yield b'"\x7f"'
    yield b'"\\u007f\\u0080\\u009f\\u00ff"'


def ascii_characters() -> Iterator[bytes]:
    """Yield the printable ASCII characters raw and as escapes."""
    raw = "".join(chr(code) for code in range(0x20, 0x80) if chr(code) not in '"\\')
    yield f'"{raw}"'.encode()
    yield b'"\\"\\\\\\/"'
    yield b'"\\/\\u002f/"'
    escaped = "".join(unicode_escape(code, upper=False) for code in range(0x20, 0x80))
    yield f'"{escaped}"'.encode()
    yield b'"\\u0041\\u0061\\u005C\\u0022"'


def angle_brackets() -> Iterator[bytes]:
    """Yield documents with the two characters the state rule rewrites."""
    yield b'"<"'
    yield b'">"'
    yield b'"<<>>"'
    yield b'"<script>alert(1)</script>"'
    yield b'"</document>"'
    yield b'"\\u003c"'
    yield b'"\\u003C\\u003E"'
    yield b'"\\u003c\\u003C\\u003e<>"'
    yield b'"\\\\u003c"'
    yield b'"\\\\<"'
    yield b'{"<k>":"<v>"}'
    yield b'["a<b","c>d",{"<":">"}]'
    yield b'"a < b && c > d"'
    yield b'"&<>\\u0026"'


def non_ascii() -> Iterator[bytes]:
    """Yield non-ASCII text raw and escaped, in values and in names."""
    for sample in NON_ASCII:
        yield f'"{sample}"'.encode()
        yield f'{{"{sample}":"{sample}"}}'.encode()
        escaped = ""
        for character in sample:
            code_point = ord(character)
            if code_point > 0xFFFF:
                escaped += surrogate_pair(code_point, upper=False)
            else:
                escaped += unicode_escape(code_point, upper=True)
        yield f'"{escaped}"'.encode()
    yield f'"{"".join(NON_ASCII)}"'.encode()


def surrogates() -> Iterator[bytes]:
    """Yield escaped surrogates: pairs, and every kind of lone one."""
    yield b'"\\ud83d\\ude00"'
    yield b'"\\uD83D\\uDE00"'
    yield b'"\\ud83D\\uDe00"'
    yield b'"\\ud800\\udc00"'
    yield b'"\\udbff\\udfff"'
    yield b'"a\\ud83d\\ude00b\\ud83d\\ude01c"'
    yield b'{"\\ud83d\\ude00":"\\ud83d\\ude00"}'
    yield b'"\\ud800"'
    yield b'"\\udbff"'
    yield b'"\\udc00"'
    yield b'"\\udfff"'
    yield b'"a\\ud800b"'
    yield b'"\\ude00\\ud83d"'
    yield b'"\\ud83d\\u0041"'
    yield b'"\\ud83dA"'
    yield b'"\\ud83d\\ud83d\\ude00"'
    yield b'"\\ud83d\\ude00\\ude00"'
    yield b'"\\ud800\\ud800\\udc00"'
    yield b'"\\ud83d\\n\\ude00"'
    yield b'{"\\ud800":1}'
    yield b'{"a":"\\udc00"}'
    yield b'["ok","\\ud800"]'
    yield b'{"a":1,"a":"\\ud800"}'
    yield b'{"a":"\\ud800","a":1}'
    yield b'"\xed\xa0\x80"'
    yield b'"\xed\xbf\xbf"'
    yield b'"\xed\xa0\xbd\xed\xb8\x80"'
    yield b'"\\ud83d\xed\xb8\x80"'
    yield b'{"a":"\xed\xa0\x80","a":1}'
    yield b'{"a":[{"b":"\\ud800"}],"a":[]}'
    yield b'{"\\ud800":1,"\\ud800":2}'
    yield b'{"\\ud800":1,"\\udc00":2}'
    yield b'{"\\ufffd":1,"\\ud800":2}'
    yield b'{"a":"\xff","a":1}'


def numbers() -> Iterator[bytes]:
    """Yield every spelling of NUMBERS alone, in an array and as a member."""
    for spelling in NUMBERS:
        yield spelling.encode()
        yield f"[{spelling}]".encode()
    yield f"[{', '.join(NUMBERS)}]".encode()
    yield b"[1.50, 1E5, 3, 3.0, -0.0, 1e400, 12345678901234567890123, 1e-7]"
    yield b'{"int":3,"float":3.0,"neg":-0,"negf":-0.0,"exp":1E5}'


def integer_digit_limit() -> Iterator[bytes]:
    """Yield integers and floats at the interpreter's 4300-digit limit."""
    for length in (4299, 4300, 4301):
        digits = "7" * length
        yield digits.encode()
        yield f"-{digits}".encode()
    yield ("1" * 4301 + ".0").encode()
    yield ("1" * 4301 + "e0").encode()
    yield ("[" + "9" * 4301 + "]").encode()


def nesting() -> Iterator[bytes]:
    """Yield nested containers, shallow and around pydantic-core's depth limit."""
    yield b"[[]]"
    yield b"[[[[[[1]]]]]]"
    yield b'{"a":{"b":{"c":{"d":[{"e":[[]]}]}}}}'
    yield b'[{"a":[{"b":[{"c":[]}]}]}]'
    scalars = ("1", "1.5", '"a"', "true", "false", "null", "[]", "{}")
    for depth in (253, 254, 255, 256, 257):
        yield b"[" * depth + b"]" * depth
        for scalar in scalars:
            yield b"[" * depth + scalar.encode() + b"]" * depth
        yield b'{"a":' * depth + b"1" + b"}" * depth
        yield b'{"a":' * depth + b"{}" + b"}" * depth
        yield b'[{"a":' * (depth // 2) + b"0" + b"}]" * (depth // 2)
    yield b"[" * 1000 + b"]" * 1000
    yield b'{"k":' * 1000 + b"null" + b"}" * 1000
    deep = b"[" * 300 + b"]" * 300
    yield b'{"a":' + deep + b',"a":1}'
    yield b'{"a":1,"a":' + deep + b"}"
    yield b'[{"a":' + deep + b',"b":2,"a":[]},3]'
    yield b'{"a":' + b"9" * 4301 + b',"a":1}'


def duplicate_names() -> Iterator[bytes]:
    """Yield objects with repeated member names."""
    yield b'{"a":1,"b":2,"a":3}'
    yield b'{"a":1,"a":2,"a":3}'
    yield b'{"a":1,"b":2,"c":3,"b":4,"a":5}'
    yield b'{"a":1,"\\u0061":2}'
    yield b'{"\\u00e9":1,"\xc3\xa9":2,"e\\u0301":3}'
    yield b'{"a":[1,2],"a":{"a":1,"a":2}}'
    yield b'{"a":{"x":1,"y":2},"b":0,"a":null}'
    yield b'{"":1,"":2}'
    yield b'{"<":1,"\\u003c":2}'
    yield b'[{"a":1,"a":2},{"a":3,"a":4}]'
    yield b'{"a":1,"A":2,"a":3}'
    yield b'{"\\ud83d\\ude00":1,"\xf0\x9f\x98\x80":2}'


def containers_and_literals() -> Iterator[bytes]:
    """Yield empty containers, the three literals and white space."""
    yield from (
        b"[]",
        b"{}",
        b"[ ]",
        b"{ }",
        b"[[],{},[{}],{}]",
        b'{"a":{},"b":[],"c":""}',
        b'""',
        b'{"":""}',
        b"true",
        b"false",
        b"null",
        b"[true,false,null]",
        b" \t\n\r[ 1 , 2 ] \n",
        b'\n{\n\t"a" : 1 ,\r\n\t"b" : [ true , null ]\n}\n',
        b'"a b"',
        b' "x" ',
    )
    members = ",".join(f'"k{index}":{index}' for index in range(100))
    yield f"{{{members}}}".encode()
    yield ("[" + ",".join(str(index) for index in range(200)) + "]").encode()
    yield ('"' + "abcdefghij" * 100 + '"').encode()


def replaced_values() -> Iterator[bytes]:
    """Yield objects where a later member replaces a value Python cannot write.

    json.loads drops the replaced value before to_json sees it, so Python
    accepts these documents unless something else is wrong with them.
    """
    deep = b"[" * 256 + b"]" * 256
    deep_object = b'{"k":' * 255 + b"1" + b"}" * 255
    big = b"7" * 4301
    yield b'{"a":' + deep + b',"a":1}'
    yield b'{"a":' + deep_object + b',"a":1}'
    yield b'[{"x":0,"a":' + deep + b',"a":[]}]'
    yield b'{"a":{"b":' + b"[" * 253 + b"1" + b"]" * 253 + b'},"a":2}'
    yield b'{"a":{"b":' + deep + b',"b":1}}'
    yield b'{"a":1,"a":' + deep + b"}"
    yield b'{"a":["\\udc00"],"a":1}'
    yield b'{"a":{"\\ud800":1},"a":1}'
    yield b'{"a":{"b":"\\ud800","b":2},"c":3}'
    yield b'{"a":"\\ud800","\\u0061":1}'
    yield b'{"a":["\xed\xb0\x80"],"a":1}'
    yield b'{"a":{"\xed\xa0\x80":1},"a":1}'
    yield b'{"a":NaN,"a":1}'
    yield b'{"a":"\\ud800","a":NaN}'
    yield b'{"a":' + big + b',"a":1}'
    yield b'["\\ud800",' + big + b"]"
    yield b"[" + deep + b',"\\ud800"]'
    yield b'["\\ud800",' + deep + b"]"
    yield b"[" + deep + b",]"
    yield b"[" + deep + b"," + big + b"]"
    yield b'{"z":' + deep + b',"a":"\\ud800","z":0}'
    yield b"[" + big + b',"\xff"]'
    yield b'["\xff",' + big + b"]"
    yield b"[-NaN]"
    yield b"[+Infinity]"
    yield b'{"NaN":1}'
    yield b'["NaN","Infinity"]'
    yield b"[1,NaN ,2]"
    yield b"NaNx"
    yield b"-Infinity "
    yield b"- Infinity"


def reader_depth_limit() -> Iterator[bytes]:
    """Yield documents at the nesting limit of the Go port's JSON reader.

    The first of each pair opens exactly READER_DEPTH_LIMIT containers, the
    root object among them, and the second one more. A later member replaces
    the deep value, so Python accepts all four. The second pair ends in 100
    objects around a number: an object counts like an array, and a scalar
    does not count.
    """
    for arrays in (READER_DEPTH_LIMIT - 1, READER_DEPTH_LIMIT):
        yield b'{"a":' + b"[" * arrays + b"]" * arrays + b',"a":1}'
    for arrays in (READER_DEPTH_LIMIT - 101, READER_DEPTH_LIMIT - 100):
        inner = b'{"k":' * 100 + b"1" + b"}" * 100
        yield b'{"a":' + b"[" * arrays + inner + b"]" * arrays + b',"a":1}'
    yield b"[" * (READER_DEPTH_LIMIT + 1)


def token_and_second_defect() -> Iterator[bytes]:
    """Yield a NaN token beside a defect of another kind that Python reports."""
    big = b"8" * 4301
    deep = b"[" * 256 + b"]" * 256
    yield b"[NaN," + big + b"]"
    yield b'{"a":Infinity,"b":' + big + b"}"
    yield b'{"a":NaN,"a":1,"b":' + big + b"}"
    yield b'{"a":[NaN,' + big + b'],"a":1}'
    yield b"[NaN," + deep + b"]"
    yield b"[" + deep + b",NaN]"
    yield b'{"a":NaN,"b":' + deep + b"}"
    yield b"[-Infinity," + b"[" * 300 + b"1" + b"]" * 300 + b"]"
    yield b'{"a":NaN,"a":' + deep + b"}"


def fixed_documents() -> Iterator[bytes]:
    """Yield every hand-written class of document."""
    yield from control_characters()
    yield from ascii_characters()
    yield from angle_brackets()
    yield from non_ascii()
    yield from surrogates()
    yield from numbers()
    yield from integer_digit_limit()
    yield from nesting()
    yield from duplicate_names()
    yield from containers_and_literals()
    yield from replaced_values()
    yield from REFUSED
    yield from PYTHON_ONLY
    yield from reader_depth_limit()
    yield from token_and_second_defect()


class DocumentWriter:
    """Write random JSON texts with a random spelling of every token."""

    def __init__(self, rng: random.Random) -> None:
        self.rng = rng

    def space(self) -> str:
        """Return white space, most often none."""
        if self.rng.random() < 0.8:
            return ""
        return self.rng.choice((" ", "\n", "\t", "\r\n", "  "))

    def character(self, hostile: bool) -> str:
        """Return one character of a JSON string, raw or escaped."""
        rng = self.rng
        kind = rng.randrange(100)
        if kind < 40:
            return rng.choice("abcdefghijklmnopqrstuvwxyzABCXYZ0123456789 _-.,:;")
        if kind < 50:
            return rng.choice(("<", ">", "&", "/", "'", "\\/", "\\u003c", "\\u003E"))
        if kind < 56:
            return rng.choice(('\\"', "\\\\", "\\u0022", "\\u005c"))
        if kind < 66:
            code = rng.randrange(0x20)
            if code in SHORT_ESCAPES and rng.random() < 0.5:
                return SHORT_ESCAPES[code]
            return unicode_escape(code, upper=rng.random() < 0.5)
        if kind < 68:
            return "\x7f"
        if kind < 80:
            return rng.choice(NON_ASCII)
        if kind < 88:
            code_point = rng.randrange(0x20, 0xD800)
            return unicode_escape(code_point, upper=rng.random() < 0.5)
        if kind < 93:
            code_point = rng.randrange(0xE000, 0x10000)
            return unicode_escape(code_point, upper=rng.random() < 0.5)
        if kind < 98 or not hostile:
            code_point = rng.randrange(0x10000, 0x110000)
            if rng.random() < 0.5:
                return chr(code_point)
            return surrogate_pair(code_point, upper=rng.random() < 0.5)
        return unicode_escape(rng.randrange(0xD800, 0xE000), upper=False)

    def string(self, hostile: bool) -> str:
        """Return a JSON string token."""
        length = self.rng.choice((0, 1, 1, 2, 3, 5, 8, 13))
        return '"' + "".join(self.character(hostile) for _ in range(length)) + '"'

    def number(self) -> str:
        """Return a JSON number token in one of many spellings."""
        rng = self.rng
        kind = rng.randrange(12)
        if kind == 0:
            return rng.choice(NUMBERS)
        if kind == 1:
            return str(rng.randrange(-1000, 1000))
        if kind == 2:
            return str(rng.randrange(-(10**30), 10**30))
        if kind == 3:
            return f"{rng.randrange(-999, 1000)}.{rng.randrange(1000):03d}"
        if kind == 4:
            mark = rng.choice(("e", "E", "e+", "E+", "e-", "E-"))
            return f"{rng.randrange(-99, 100)}{mark}{rng.randrange(0, 30)}"
        if kind == 5:
            mark = rng.choice(("e", "E", "e+", "e-"))
            whole = rng.randrange(0, 10)
            return f"{whole}.{rng.randrange(10**6)}{mark}{rng.randrange(0, 330)}"
        if kind == 6:
            return f"{rng.random()!r}"
        if kind == 7:
            return f"{rng.randrange(1, 10**17)}e{rng.randrange(-340, 300)}"
        if kind == 8:
            return f"-{rng.randrange(0, 3)}.{'0' * rng.randrange(1, 4)}"
        bits = rng.getrandbits(64)
        value: float = struct.unpack(">d", struct.pack(">Q", bits))[0]
        if not math.isfinite(value):
            return "0"
        if kind == 9:
            return repr(value)
        if kind == 10:
            return pydantic_core.to_json(value).decode()
        return f"{value:.17e}"

    def value(self, depth: int, hostile: bool) -> str:
        """Return a JSON value; containers get rarer as depth grows."""
        rng = self.rng
        kind = rng.randrange(10 if depth < 5 else 6)
        if kind < 2:
            return self.string(hostile)
        if kind < 5:
            return self.number()
        if kind == 5:
            return rng.choice(("true", "false", "null"))
        if kind < 8:
            items = [self.value(depth + 1, hostile) for _ in range(rng.randrange(5))]
            body = ("," + self.space()).join(items)
            return "[" + self.space() + body + self.space() + "]"
        names: list[str] = []
        members: list[str] = []
        for _ in range(rng.randrange(5)):
            if names and rng.random() < 0.15:
                name = rng.choice(names)
            else:
                name = self.string(hostile)
                names.append(name)
            member = self.value(depth + 1, hostile)
            members.append(name + self.space() + ":" + self.space() + member)
        return "{" + self.space() + ("," + self.space()).join(members) + "}"

    def document(self) -> bytes:
        """Return one document; about one in fifty may hold a lone surrogate."""
        hostile = self.rng.random() < 0.02
        depth = self.rng.choice((0, 0, 0, 5))
        return (self.space() + self.value(depth, hostile) + self.space()).encode()


def collect(seed: int, count: int) -> list[bytes]:
    """Return count distinct documents: every fixed class, then random ones.

    Raises:
        ValueError: If count is smaller than the number of fixed documents.
    """
    seen: set[bytes] = set()
    ordered: list[bytes] = []
    for document in fixed_documents():
        if document not in seen:
            seen.add(document)
            ordered.append(document)
    if len(ordered) > count:
        raise ValueError(
            f"count {count} is smaller than the {len(ordered)} fixed documents"
        )
    writer = DocumentWriter(random.Random(seed))
    while len(ordered) < count:
        document = writer.document()
        if document not in seen:
            seen.add(document)
            ordered.append(document)
    return ordered


@dataclass(frozen=True)
class Row:
    """What Python makes of one document: fields 2 to 5 of a data line."""

    verdict: str
    kind: str = "-"
    encoded: str = "-"
    state: str = "-"


def has_lone_surrogate(value: object) -> bool:
    """Report whether a loaded value holds a surrogate in any string or name."""
    pending = [value]
    while pending:
        item = pending.pop()
        if isinstance(item, str):
            if any(0xD800 <= ord(character) <= 0xDFFF for character in item):
                return True
        elif isinstance(item, list):
            pending.extend(item)
        elif isinstance(item, dict):
            pending.extend(item.keys())
            pending.extend(item.values())
    return False


def has_lone_surrogate_escape(text: str) -> bool:
    """Report whether text holds a surrogate escape that is not half of a pair."""
    paired_low = -1
    for match in ESCAPE.finditer(text):
        digits = match.group(1)
        unit = int(digits, 16) if digits else 0
        if 0xD800 <= unit <= 0xDBFF:
            following = ESCAPE.match(text, match.end())
            low = following.group(1) if following else None
            if not low or not 0xDC00 <= int(low, 16) <= 0xDFFF:
                return True
            paired_low = match.end()
        elif 0xDC00 <= unit <= 0xDFFF and match.start() != paired_low:
            return True
    return False


def nests_past_reader_limit(document: bytes) -> bool:
    """Report whether document opens more than READER_DEPTH_LIMIT containers.

    Containers are counted while they are open inside one another, outside
    string tokens, on the bytes as they are: the text need not be JSON.
    """
    if document.count(b"[") + document.count(b"{") <= READER_DEPTH_LIMIT:
        return False
    depth = 0
    for bracket in BRACKET.finditer(STRING_TOKEN.sub(b"", document)):
        if bracket.group() in b"[{":
            depth += 1
            if depth > READER_DEPTH_LIMIT:
                return True
        elif depth:
            depth -= 1
    return False


def evaluate(document: bytes) -> Row:
    """Return the verdict, the class and the outputs of one document.

    A refusal is in the class two-defects when the text holds something a
    reader of JSON in UTF-8 refuses with another kind of error than Python's:
    a lone surrogate escape or a NaN token beside a depth or digit refusal,
    an oversized integer in bytes that are not UTF-8, or nesting past
    READER_DEPTH_LIMIT beside any refusal other than Python's own for depth.

    Raises:
        ValueError: If the two ways of finding a replaced lone surrogate differ.
    """
    deep = nests_past_reader_limit(document)
    try:
        text = document.decode("utf-8")
    except UnicodeDecodeError as error:
        second = deep or OVERSIZED_INTEGER.search(document) is not None
        return Row(f"decode:{type(error).__name__}", "two-defects" if second else "-")
    lone_escape = has_lone_surrogate_escape(text)
    nan_token = False
    replaced = False

    def constant(token: str) -> float:
        nonlocal nan_token
        nan_token = True
        return float(token)

    def members(pairs: list[tuple[str, object]]) -> dict[str, object]:
        nonlocal replaced
        result: dict[str, object] = {}
        for name, value in pairs:
            if name in result and has_lone_surrogate(result[name]):
                replaced = True
            result[name] = value
        return result

    try:
        value = json.loads(text, parse_constant=constant, object_pairs_hook=members)
    except RecursionError as error:
        second = lone_escape or nan_token
        return Row(f"loads:{type(error).__name__}", "two-defects" if second else "-")
    except json.JSONDecodeError as error:
        return Row(f"loads:{type(error).__name__}", "two-defects" if deep else "-")
    except ValueError as error:
        second = lone_escape or nan_token or deep
        return Row(f"loads:{type(error).__name__}", "two-defects" if second else "-")
    try:
        encoded = pydantic_core.to_json(value)
    except pydantic_core.PydanticSerializationError as error:
        cause = str(error).split(": ")[1]
        second = deep or (cause != "UnicodeEncodeError" and (lone_escape or nan_token))
        verdict = f"to_json:{type(error).__name__}:{cause}"
        return Row(verdict, "two-defects" if second else "-")
    if replaced != lone_escape:
        raise ValueError(f"replaced lone surrogate found two ways: {document!r}")
    kinds = [
        name
        for name, present in (
            ("nan-token", nan_token),
            ("replaced-lone-surrogate", replaced),
            ("read-depth", deep),
        )
        if present
    ]
    state = encoded.decode().replace("<", "\\u003c").replace(">", "\\u003e").encode()
    return Row(
        "ok",
        "+".join(kinds) or "-",
        base64.b64encode(encoded).decode("ascii"),
        "=" if state == encoded else base64.b64encode(state).decode("ascii"),
    )


def render(seed: int, count: int) -> str:
    """Return the whole vector file for seed and count."""
    lines = [
        (
            "# state vectors: document (base64), verdict, class, "
            'pydantic_core.to_json(json.loads(document.decode("utf-8"))) (base64), '
            "the same with < and > escaped (base64, or = when equal); tab-separated"
        ),
        f"# python: {sys.version}",
        f"# pydantic: {pydantic.VERSION}",
        f"# pydantic_core: {pydantic_core.__version__}",
        f"# seed: {seed}",
        f"# count: {count}",
    ]
    for document in collect(seed, count):
        row = evaluate(document)
        source = base64.b64encode(document).decode("ascii")
        lines.append(f"{source}\t{row.verdict}\t{row.kind}\t{row.encoded}\t{row.state}")
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
