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
"""Write the answer schemas system-one-adapter 0.2.1 builds for a set of requests.

Each case is a System One request body as JSON text. For a request upstream
accepts, the script records what upstream's own code derives from it without
calling a provider: the schema text in each answer mode, which is
``to_json(create_raw_output_schema(create_llm_output_model(...)))``, the text
upstream puts into a prompted request and the value it sends in a native one,
and the user message ``_serialize_state_as_user_prompt`` makes of the state. For a
request upstream refuses, it records the refusal.

The output is JSON Lines, in ASCII:

- line 1, the header: the versions and the format (its first member is
  ``format``);
- a case line (first member ``case``): ``request``, the request body as the
  text given to ``json.loads``, and either ``user`` and ``schema`` with one text
  per answer mode, or ``refused`` with the exception's class as ``error`` and
  either its ``message`` or, for a pydantic ``ValidationError``, ``errors`` as
  one ``[type, loc...]`` list per error, or ``error_count`` alone when there are
  more than sixteen.

The cases have no random part, so there is no seed and no size: the same
interpreter and the same package versions give the same bytes. The script
refuses to run on other versions, or on a ``system_one_adapter`` whose sources
differ from the release.

Usage::

    uv run testdata/python/gen_schema_cases.py            # write the cases
    uv run testdata/python/gen_schema_cases.py --check    # compare, no write
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from collections.abc import Iterator, Mapping
from importlib import metadata
from pathlib import Path
from typing import Literal, TypeAlias, get_args

import pydantic
import pydantic_core
import system_one_adapter
from pydantic_core import to_json
from system_one_adapter._client import _serialize_state_as_user_prompt
from system_one_adapter._schema import (
    convert_question_collection_to_validated_api_question_models,
    create_llm_output_model,
    create_raw_output_schema,
)

AnswerMode: TypeAlias = Literal["probabilities", "discrete"]
Json: TypeAlias = bool | int | float | str | None | list["Json"] | dict[str, "Json"]

DESCRIPTION = "Write the answer schemas upstream builds for a set of requests."
FORMAT = 1
DEFAULT_OUTPUT = Path(__file__).resolve().parent / "schema_cases.jsonl"

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

MODEL = "m"
# A refusal with more errors than this is recorded by their number.
MAX_ERRORS = 16

# Requests upstream accepts, as the state and the questions of each. The request
# text is their compact JSON, with the model between them.
REQUESTS: dict[str, dict[str, Json]] = {
    "reference": {
        "state": "A review.",
        "questions": {
            "positive": {
                "type": "noul",
                "instructions": "The book review is positive.",
            },
            "rating": {
                "type": "score",
                "instructions": "How favorable the reviewer's overall assessment is.",
                "criteria": [
                    "Condemns.",
                    "Critical.",
                    "Mixed.",
                    "Praises.",
                    "Unreserved.",
                ],
            },
            "genre": {
                "type": "choice",
                "instructions": "Which genre this review is about.",
                "criteria": {"fiction": "A novel.", "nonfiction": "Facts."},
            },
        },
    },
    "noul_criteria": {
        "state": "s",
        "questions": {
            "both": {
                "type": "noul",
                "instructions": "i",
                "criteria": {"true": "yes text", "false": "no text"},
            },
            "true_only": {
                "type": "noul",
                "instructions": "i",
                "criteria": {"true": "y"},
            },
            "false_only": {
                "type": "noul",
                "instructions": "i",
                "criteria": {"false": "n"},
            },
            "empty": {"type": "noul", "instructions": "i", "criteria": {}},
            "null_criteria": {"type": "noul", "instructions": "i", "criteria": None},
            "null_values": {
                "type": "noul",
                "instructions": None,
                "criteria": {"true": None, "false": None},
            },
            "bare": {"type": "noul"},
        },
    },
    "json_content": {
        "state": "s",
        "questions": {
            "n": {
                "type": "noul",
                "instructions": {
                    "task": "Find it.",
                    "n": 3,
                    "flags": [True, False, None],
                },
                "criteria": {"true": ["a", {"b": 1}], "false": {"z": 1, "a": 2}},
            },
            "c": {
                "type": "choice",
                "instructions": ["x", 1, {"k": "v"}],
                "criteria": {
                    "obj": {"b": 1, "a": 2},
                    "arr": [1, [2, [3]]],
                    "none": None,
                },
            },
            "s": {
                "type": "score",
                "instructions": {"b": {"d": 1, "c": 2}, "a": []},
                "criteria": [{"lo": 0}, ["mid"], "hi"],
            },
            "no_instructions": {"type": "score", "criteria": ["a", "b"]},
        },
    },
    "many_questions": {
        "state": "s",
        "questions": {
            "q0": {
                "type": "choice",
                "instructions": "choice 0",
                "criteria": {"1": "one", "0": "zero", "10": "ten", "2": None},
            },
            "q1": {
                "type": "score",
                "instructions": "score 1",
                "criteria": [
                    "level 0",
                    "level 1",
                    "level 2",
                    "level 3",
                    "level 4",
                    "level 5",
                    "level 6",
                    "level 7",
                    "level 8",
                    "level 9",
                    "level 10",
                ],
            },
            "q2": {
                "type": "choice",
                "instructions": "choice 2",
                "criteria": {"1": "one", "0": "zero", "10": "ten", "2": None},
            },
            "q3": {
                "type": "score",
                "instructions": "score 3",
                "criteria": [
                    "level 0",
                    "level 1",
                    "level 2",
                    "level 3",
                    "level 4",
                    "level 5",
                    "level 6",
                    "level 7",
                    "level 8",
                    "level 9",
                    "level 10",
                ],
            },
            "q4": {
                "type": "choice",
                "instructions": "choice 4",
                "criteria": {"1": "one", "0": "zero", "10": "ten", "2": None},
            },
            "q5": {
                "type": "score",
                "instructions": "score 5",
                "criteria": [
                    "level 0",
                    "level 1",
                    "level 2",
                    "level 3",
                    "level 4",
                    "level 5",
                    "level 6",
                    "level 7",
                    "level 8",
                    "level 9",
                    "level 10",
                ],
            },
            "q6": {
                "type": "choice",
                "instructions": "choice 6",
                "criteria": {"1": "one", "0": "zero", "10": "ten", "2": None},
            },
            "q7": {
                "type": "score",
                "instructions": "score 7",
                "criteria": [
                    "level 0",
                    "level 1",
                    "level 2",
                    "level 3",
                    "level 4",
                    "level 5",
                    "level 6",
                    "level 7",
                    "level 8",
                    "level 9",
                    "level 10",
                ],
            },
            "q8": {
                "type": "choice",
                "instructions": "choice 8",
                "criteria": {"1": "one", "0": "zero", "10": "ten", "2": None},
            },
            "q9": {
                "type": "score",
                "instructions": "score 9",
                "criteria": [
                    "level 0",
                    "level 1",
                    "level 2",
                    "level 3",
                    "level 4",
                    "level 5",
                    "level 6",
                    "level 7",
                    "level 8",
                    "level 9",
                    "level 10",
                ],
            },
            "q10": {
                "type": "choice",
                "instructions": "choice 10",
                "criteria": {"1": "one", "0": "zero", "10": "ten", "2": None},
            },
            "q11": {
                "type": "score",
                "instructions": "score 11",
                "criteria": [
                    "level 0",
                    "level 1",
                    "level 2",
                    "level 3",
                    "level 4",
                    "level 5",
                    "level 6",
                    "level 7",
                    "level 8",
                    "level 9",
                    "level 10",
                ],
            },
        },
    },
    "reserved_names": {
        "state": "s",
        "questions": {
            "properties": {"type": "noul", "instructions": "p"},
            "default": {
                "type": "choice",
                "instructions": "d",
                "criteria": {
                    "properties": "a",
                    "default": "b",
                    "type": "c",
                    "$ref": "d",
                },
            },
            "$defs": {"type": "score", "instructions": "s", "criteria": ["x", "y"]},
            "required": {
                "type": "noul",
                "instructions": "r",
                "criteria": {"true": "t"},
            },
            "type": {
                "type": "choice",
                "instructions": "t",
                "criteria": {"a": "1", "b": "2"},
            },
            "answers": {
                "type": "score",
                "instructions": "a",
                "criteria": ["x", "y", "z"],
            },
        },
    },
    "reserved_names_swapped": {
        "state": "s",
        "questions": {
            "default": {"type": "score", "instructions": "d", "criteria": ["x", "y"]},
            "properties": {
                "type": "choice",
                "instructions": "p",
                "criteria": {"default": "a", "properties": "b"},
            },
        },
    },
    "keyword_names": {
        "state": "s",
        "questions": {
            "title": {"type": "noul", "instructions": "t"},
            "minimum": {
                "type": "choice",
                "instructions": "m",
                "criteria": {
                    "title": "a",
                    "maximum": "b",
                    "exclusiveMinimum": "c",
                    "exclusiveMaximum": "d",
                    "description": "e",
                    "enum": "f",
                },
            },
            "maximum": {"type": "score", "instructions": "x", "criteria": ["a", "b"]},
            "description": {"type": "noul", "instructions": "d"},
            "additionalProperties": {"type": "noul", "instructions": "ap"},
        },
    },
    "name_order": {
        "state": "s",
        "questions": {
            "b": {"type": "noul", "instructions": "1"},
            "a": {"type": "noul", "instructions": "2"},
            "Z": {
                "type": "choice",
                "instructions": "3",
                "criteria": {"y": "1", "x": "2", "X": "3"},
            },
            "\xe9": {"type": "noul", "instructions": "\xe9t\xe9"},
            "\u65e5\u672c": {
                "type": "choice",
                "instructions": "\u8a00\u8a9e",
                "criteria": {
                    "\u306f\u3044": "\u80af\u5b9a",
                    "\u3044\u3044\u3048": "\u5426\u5b9a",
                },
            },
            "\U0001f600": {
                "type": "score",
                "instructions": "\U0001f600",
                "criteria": ["\U0001f641", "\U0001f642"],
            },
            "": {"type": "noul", "instructions": "empty id"},
            "with space": {"type": "noul", "instructions": "s"},
        },
    },
    "escapes": {
        "state": "s",
        "questions": {
            'quote"back\\slash': {
                "type": "choice",
                "instructions": 'say "hi" \\ <b>&</b> \u2028\u2029 \x7f',
                "criteria": {
                    'a"b': "tab\there",
                    "c\\d": "nul\x00one\x01esc\x1b",
                    "</x>": "\r\n",
                },
            },
            "ctl\x01": {"type": "noul", "instructions": "\x08\x0c\n\r\t/"},
        },
    },
    "docstring_cleaning": {
        "state": "s",
        "questions": {
            "tabs": {
                "type": "choice",
                "instructions": "\tx\n  y\n\n",
                "criteria": {"a": "\t1\n", "b": " 2 "},
            },
            "indent": {
                "type": "score",
                "instructions": "a\n    b\n    c\n\n\n",
                "criteria": ["x\n", "y"],
            },
            "blank": {
                "type": "choice",
                "instructions": "",
                "criteria": {"a": "", "b": "  "},
            },
            "spaces": {"type": "score", "instructions": "   ", "criteria": ["", " "]},
            "crlf": {
                "type": "choice",
                "instructions": "a\r\n\tb\r\n",
                "criteria": {"a": "1", "b": "2"},
            },
            "wide": {
                "type": "score",
                "instructions": "\u65e5\u672c\t\U0001f600\tend\n \n",
                "criteria": ["x", "y"],
            },
            "noul_tabs": {
                "type": "noul",
                "instructions": "\tn\n\n",
                "criteria": {"true": "\tt\n"},
            },
            "noul_blank": {"type": "noul", "instructions": ""},
        },
    },
    "state_object": {
        "state": {
            "title": "<b>T</b>",
            "tags": ["a", "<", ">", "&", "\xe9", "\U0001f600"],
            "n": 3,
            "neg": -7,
            "ok": True,
            "none": None,
            "nested": {"z": [1, [2, {"k": "</document>"}]], "a": {}},
            "empty": [],
        },
        "questions": {
            "positive": {
                "type": "noul",
                "instructions": "The book review is positive.",
            },
            "rating": {
                "type": "score",
                "instructions": "How favorable the reviewer's overall assessment is.",
                "criteria": [
                    "Condemns.",
                    "Critical.",
                    "Mixed.",
                    "Praises.",
                    "Unreserved.",
                ],
            },
            "genre": {
                "type": "choice",
                "instructions": "Which genre this review is about.",
                "criteria": {"fiction": "A novel.", "nonfiction": "Facts."},
            },
        },
    },
    "state_array": {
        "state": ["<document>", {"b": 1, "a": 2}, [], 'line\nbreak "q" \\ \x01'],
        "questions": {
            "positive": {
                "type": "noul",
                "instructions": "The book review is positive.",
            },
            "rating": {
                "type": "score",
                "instructions": "How favorable the reviewer's overall assessment is.",
                "criteria": [
                    "Condemns.",
                    "Critical.",
                    "Mixed.",
                    "Praises.",
                    "Unreserved.",
                ],
            },
            "genre": {
                "type": "choice",
                "instructions": "Which genre this review is about.",
                "criteria": {"fiction": "A novel.", "nonfiction": "Facts."},
            },
        },
    },
    "state_text_escapes": {
        "state": "a<b>c \u2028 \x7f \x00 \t \xe9 \U0001f600 </document>",
        "questions": {
            "positive": {
                "type": "noul",
                "instructions": "The book review is positive.",
            },
            "rating": {
                "type": "score",
                "instructions": "How favorable the reviewer's overall assessment is.",
                "criteria": [
                    "Condemns.",
                    "Critical.",
                    "Mixed.",
                    "Praises.",
                    "Unreserved.",
                ],
            },
            "genre": {
                "type": "choice",
                "instructions": "Which genre this review is about.",
                "criteria": {"fiction": "A novel.", "nonfiction": "Facts."},
            },
        },
    },
    "defs_order_twelve": {
        "state": "plain state",
        "questions": {
            "zeta": {"type": "noul", "instructions": "q0"},
            "alpha": {"type": "noul", "instructions": "q1"},
            "m2": {
                "type": "choice",
                "instructions": "q2",
                "criteria": {"b": "second", "a": "first"},
            },
            "n3": {"type": "noul", "instructions": "q3"},
            "n4": {"type": "noul", "instructions": "q4"},
            "n5": {"type": "noul", "instructions": "q5"},
            "n6": {"type": "noul", "instructions": "q6"},
            "n7": {"type": "noul", "instructions": "q7"},
            "n8": {"type": "noul", "instructions": "q8"},
            "n9": {"type": "noul", "instructions": "q9"},
            "m10": {
                "type": "score",
                "instructions": "q10",
                "criteria": ["low", "high"],
            },
            "m11": {
                "type": "choice",
                "instructions": "q11",
                "criteria": {"y": None, "x": "ex"},
            },
        },
    },
    "score_eleven_levels": {
        "state": ["a", 1, 1.5, None, True],
        "questions": {
            "s": {
                "type": "score",
                "instructions": "eleven",
                "criteria": [
                    "level 0",
                    "level 1",
                    "level 2",
                    "level 3",
                    "level 4",
                    "level 5",
                    "level 6",
                    "level 7",
                    "level 8",
                    "level 9",
                    "level 10",
                ],
            }
        },
    },
    "keyword_and_model_names": {
        "state": {"k": "v"},
        "questions": {
            "properties": {
                "type": "noul",
                "instructions": "named properties",
                "criteria": {"true": "T", "false": "F"},
            },
            "default": {
                "type": "choice",
                "instructions": "named default",
                "criteria": {
                    "properties": "p",
                    "default": "d",
                    "title": "t",
                    "minimum": "m",
                },
            },
            "title": {
                "type": "score",
                "instructions": "named title",
                "criteria": ["t0", "t1", "t2"],
            },
            "$defs": {"type": "noul", "instructions": "named $defs"},
            "required": {
                "type": "choice",
                "instructions": "named required",
                "criteria": {"type": "ty", "$ref": "re", "enum": "en"},
            },
            "answers": {"type": "noul", "instructions": "named answers"},
            "answer_0": {
                "type": "noul",
                "instructions": "collides with a generated field name",
            },
            "TypeSafeAnswers": {
                "type": "choice",
                "instructions": "named like the model",
                "criteria": {"probability_0": "p0", "probability_1": "p1"},
            },
        },
    },
    "hostile_text": {
        "state": 'a <tag> & </document> \u2028 \xe9 \U0001f600 "q" \\ \t end',
        "questions": {
            'caf\xe9 <q> & "x"': {
                "type": "noul",
                "instructions": (
                    'Is <b>this</b> & that? \u2028\u2029 \xe9\xfc \U0001f600 "q'
                    'uoted" back\\slash tab\there\nnewline \x01\x7f'
                ),
            },
            "\u65e5\u672c\u8a9e": {
                "type": "choice",
                "instructions": "labels with odd characters",
                "criteria": {
                    "<a>": "less & more > \xe9",
                    'b"c': None,
                    "d\\e": "tab\tin",
                    "\U0001f600": "\u2028 sep",
                    " ": "space label",
                    "": "empty label",
                },
            },
            "s/\xe9": {
                "type": "score",
                "instructions": "score \xe9 < >",
                "criteria": ["nul \x00 in", "del \x7f", "\ud7ff edge", "\ufffd repl"],
            },
        },
    },
    "json_values": {
        "state": {
            "z": 1,
            "a": [
                1.5,
                100000.0,
                3,
                3.0,
                -0.0,
                1e-07,
                12345678901234567890123,
                1e22,
                1e16,
                1e-05,
            ],
            "<k>": "<v>",
        },
        "questions": {
            "obj": {
                "type": "noul",
                "instructions": {
                    "z": 1,
                    "a": [
                        1e-05,
                        1e16,
                        1.0,
                        -0.0,
                        1e22,
                        0.1,
                        1e21,
                        1.2345678901234568e20,
                        5e-324,
                        1.7976931348623157e308,
                    ],
                    "n": None,
                    "t": True,
                },
                "criteria": {"true": {"b": 1, "a": 2}, "false": [1, "x", None]},
            },
            "only_true": {
                "type": "noul",
                "instructions": "one criterion",
                "criteria": {"true": "yes text"},
            },
            "only_false": {
                "type": "noul",
                "instructions": ["list", "instructions", 2.5],
                "criteria": {"false": [0.5, 1e-05]},
            },
            "ch": {
                "type": "choice",
                "instructions": {"k": [1, 2, {"d": 1e-07}]},
                "criteria": {
                    "x": {"w": 1.0, "v": 2},
                    "y": [100000.0, 1e-05],
                    "z": None,
                    "w": "text",
                },
            },
            "sc": {
                "type": "score",
                "instructions": [1, 2, 3],
                "criteria": [
                    {"label": "bad", "min": 0.0},
                    ["good", 3.25],
                    "plain",
                    [4, 2.0, None],
                    {"n": None},
                ],
            },
            "none_instr": {"type": "noul", "instructions": None},
            "arr_instr": {"type": "noul", "instructions": [7, 7.0, 7.0]},
        },
    },
    "single_noul": {
        "state": "",
        "questions": {"only": {"type": "noul", "instructions": "just one"}},
    },
    "single_choice": {
        "state": [],
        "questions": {
            "c": {
                "type": "choice",
                "instructions": "two labels",
                "criteria": {"no": "n", "yes": "y"},
            }
        },
    },
    "single_score": {
        "state": {},
        "questions": {
            "s": {
                "type": "score",
                "instructions": "two levels",
                "criteria": ["zero", "one"],
            }
        },
    },
    "reverse_order": {
        "state": "x",
        "questions": {
            "q9": {
                "type": "choice",
                "instructions": "i9",
                "criteria": {"l3": "c3", "l2": "c2", "l1": "c1"},
            },
            "q5": {"type": "score", "instructions": "i5", "criteria": ["c", "b", "a"]},
            "Q1": {"type": "noul", "instructions": "i1"},
            "q10": {
                "type": "choice",
                "instructions": "i10",
                "criteria": {"B": "cb", "a": "ca", "A": "cA", "_": "cu", "0": "c0"},
            },
        },
    },
    "empty_strings": {
        "state": "",
        "questions": {
            "n": {"type": "noul", "instructions": ""},
            "c": {"type": "choice", "instructions": "", "criteria": {"a": "", "b": ""}},
            "s": {"type": "score", "instructions": "", "criteria": ["", ""]},
        },
    },
    "whitespace_everywhere": {
        "state": " \t\n",
        "questions": {
            "n": {
                "type": "noul",
                "instructions": (
                    "\tlead tab\n  two spaces\n\ttab line\r\n crlf line\rcr onl"
                    "y\x0bvt\x0cff\n   \n\n"
                ),
                "criteria": {
                    "true": (
                        "\tlead tab\n  two spaces\n\ttab line\r\n crlf line\rcr onl"
                        "y\x0bvt\x0cff\n   \n\n"
                    ),
                    "false": "\t",
                },
            },
            "c": {
                "type": "choice",
                "instructions": (
                    "\tlead tab\n  two spaces\n\ttab line\r\n crlf line\rcr onl"
                    "y\x0bvt\x0cff\n   \n\n"
                ),
                "criteria": {
                    "a\tb": (
                        "\tlead tab\n  two spaces\n\ttab line\r\n crlf line\rcr onl"
                        "y\x0bvt\x0cff\n   \n\n"
                    ),
                    " ": "\n",
                },
            },
            "s": {
                "type": "score",
                "instructions": (
                    "\tlead tab\n  two spaces\n\ttab line\r\n crlf line\rcr onl"
                    "y\x0bvt\x0cff\n   \n\n"
                ),
                "criteria": [
                    (
                        "\tlead tab\n  two spaces\n\ttab line\r\n crlf line\rcr onl"
                        "y\x0bvt\x0cff\n   \n\n"
                    ),
                    "\t\t",
                    "x\n\n",
                ],
            },
        },
    },
    "cleandoc_margins": {
        "state": "x",
        "questions": {
            "only_newlines": {
                "type": "choice",
                "instructions": "\n\n\n",
                "criteria": {"a": "1", "b": "2"},
            },
            "indented_block": {
                "type": "choice",
                "instructions": "first\n    four\n      six\n    four\n",
                "criteria": {"a": "1", "b": "2"},
            },
            "leading_blank": {
                "type": "score",
                "instructions": "\n\n  text after blanks",
                "criteria": ["1", "2"],
            },
            "spaces_only_tail": {
                "type": "score",
                "instructions": "text\n   ",
                "criteria": ["1", "2"],
            },
            "tab_after_wide": {
                "type": "choice",
                "instructions": "\u65e5\u672c\tx\n\U0001f600\ty\ne\u0301\tz",
                "criteria": {"a": "1", "b": "2"},
            },
            "tab_columns": {
                "type": "score",
                "instructions": "1234567\ta\n12345678\tb\n\t\tc",
                "criteria": ["1", "2"],
            },
            "nbsp_and_others": {
                "type": "choice",
                "instructions": "\xa0nbsp\n\u2003em\n\u3000ideographic",
                "criteria": {"a": "1", "b": "2"},
            },
        },
    },
    "noul_criteria_forms": {
        "state": "x",
        "questions": {
            "missing": {"type": "noul", "instructions": "i"},
            "null": {"type": "noul", "instructions": "i", "criteria": None},
            "empty": {"type": "noul", "instructions": "i", "criteria": {}},
            "true_null": {
                "type": "noul",
                "instructions": "i",
                "criteria": {"true": None},
            },
            "both_null": {
                "type": "noul",
                "instructions": "i",
                "criteria": {"true": None, "false": None},
            },
            "false_first": {
                "type": "noul",
                "instructions": "i",
                "criteria": {"false": "F", "true": "T"},
            },
            "no_instructions": {"type": "noul"},
            "null_instructions": {
                "type": "noul",
                "instructions": None,
                "criteria": {"true": ["a"], "false": {"b": "c"}},
            },
        },
    },
    "missing_instructions": {
        "state": "x",
        "questions": {
            "c": {"type": "choice", "criteria": {"a": None, "b": None}},
            "s": {"type": "score", "criteria": ["x", "y"]},
            "c2": {
                "type": "choice",
                "instructions": None,
                "criteria": {"default": None, "properties": None},
            },
        },
    },
    "reserved_as_question_ids": {
        "state": "x",
        "questions": {
            "default": {
                "type": "score",
                "instructions": "score named default",
                "criteria": ["a", "b"],
            },
            "properties": {
                "type": "choice",
                "instructions": "choice named properties",
                "criteria": {"x": "1", "y": "2"},
            },
            "$defs": {
                "type": "choice",
                "instructions": "choice named $defs",
                "criteria": {"default": "d", "properties": "p"},
            },
        },
    },
    "member_order_in_question": {
        "state": "x",
        "questions": {
            "c": {
                "criteria": {"b": "2", "a": "1"},
                "instructions": "criteria first",
                "type": "choice",
            },
            "s": {"instructions": "type last", "criteria": ["0", "1"], "type": "score"},
        },
    },
    "json_strings_inside": {
        "state": {"a<b": ["<", ">", "&", "\u2028", "\xe9", '"', "\\", "\x7f", "\x00"]},
        "questions": {
            "n": {
                "type": "noul",
                "instructions": {
                    "k<": [
                        "<x>",
                        "\xe9",
                        '"',
                        "\\",
                        "\x7f",
                        "\u2028",
                        "\x01",
                        "\U0001f600",
                    ]
                },
                "criteria": {"true": ["\t", "\n"], "false": {"": ""}},
            },
            "c": {
                "type": "choice",
                "instructions": ["a", ["b", ["c"]]],
                "criteria": {
                    "x": {"z": 1, "a": {"y": 2, "b": 3}},
                    "y": [True, False, None, 0, -1, 9007199254740993],
                },
            },
        },
    },
    "state_less_than_only": {
        "state": "a<b",
        "questions": {"n": {"type": "noul", "instructions": "i"}},
    },
    "state_greater_than_only": {
        "state": {"k>": [">"]},
        "questions": {"n": {"type": "noul", "instructions": "i"}},
    },
}

# Requests upstream refuses; each must be refused, or the script stops.
REFUSED_REQUESTS: dict[str, dict[str, Json]] = {
    "no_questions": {"state": "s", "questions": {}},
    "choice_one_criterion": {
        "state": "s",
        "questions": {
            "ok": {"type": "noul"},
            "c": {"type": "choice", "criteria": {"only": None}},
        },
    },
    "score_one_criterion": {
        "state": "s",
        "questions": {"s": {"type": "score", "criteria": ["only"]}},
    },
    "score_no_criterion": {
        "state": "s",
        "questions": {"s": {"type": "score", "criteria": []}},
    },
    "choice_no_criterion": {
        "state": "s",
        "questions": {"c": {"type": "choice", "criteria": {}}},
    },
    "unknown_type": {"state": "s", "questions": {"answer": {"type": "unknown"}}},
    "missing_type": {"state": "s", "questions": {"answer": {"instructions": "i"}}},
    "number_instructions": {
        "state": "s",
        "questions": {"answer": {"type": "noul", "instructions": 42}},
    },
    "bool_instructions": {
        "state": "s",
        "questions": {
            "answer": {"type": "score", "instructions": True, "criteria": ["a", "b"]}
        },
    },
    "choice_array_criteria": {
        "state": "s",
        "questions": {"answer": {"type": "choice", "criteria": ["yes", "no"]}},
    },
    "choice_null_criteria": {
        "state": "s",
        "questions": {"answer": {"type": "choice", "criteria": None}},
    },
    "choice_missing_criteria": {
        "state": "s",
        "questions": {"answer": {"type": "choice"}},
    },
    "choice_number_criterion": {
        "state": "s",
        "questions": {"answer": {"type": "choice", "criteria": {"a": 1, "b": "x"}}},
    },
    "score_object_criteria": {
        "state": "s",
        "questions": {
            "answer": {"type": "score", "criteria": {"0": "Bad.", "1": "Good."}}
        },
    },
    "score_string_criteria": {
        "state": "s",
        "questions": {"answer": {"type": "score", "criteria": "ab"}},
    },
    "score_null_criterion": {
        "state": "s",
        "questions": {"answer": {"type": "score", "criteria": ["a", None, "c"]}},
    },
    "score_missing_criteria": {
        "state": "s",
        "questions": {"answer": {"type": "score", "instructions": "i"}},
    },
    "noul_number_criterion": {
        "state": "s",
        "questions": {"answer": {"type": "noul", "criteria": {"true": 42}}},
    },
    "noul_unknown_criterion": {
        "state": "s",
        "questions": {
            "answer": {"type": "noul", "criteria": {"true": "t", "maybe": "m"}}
        },
    },
    "noul_array_criteria": {
        "state": "s",
        "questions": {"answer": {"type": "noul", "criteria": ["t", "f"]}},
    },
    "extra_member": {
        "state": "s",
        "questions": {"answer": {"type": "noul", "title": "t"}},
    },
    "question_not_object": {"state": "s", "questions": {"answer": "noul"}},
    "shape_before_count": {
        "state": "s",
        "questions": {
            "few": {"type": "score", "criteria": ["x"]},
            "bad": {"type": "noul", "instructions": True},
        },
    },
}

# Request texts a Python value cannot spell: a member name written twice and
# number literals in other spellings. They are given to json.loads as they are.
WIRE_REQUESTS: dict[str, str] = {
    "wire_repeated_names": (
        '{"state":"s","model":"m","questions":{"a":{"type":"noul","'
        'instructions":"first"},"b":{"type":"choice","criteria":{"x'
        '":"1","y":"2","x":"3"}},"a":{"type":"score","instructions"'
        ':"last","instructions":"very last","criteria":["lo","hi"]}'
        "}}"
    ),
    "wire_number_spellings": (
        '{"state":[1.50,1E5,-0,3.0],"model":"m","questions":{"n":{"'
        'type":"noul","instructions":{"n":[1.50,1E5,-0,-0.0,1e400,-'
        "1e400,12345678901234567890123,1e-7,0.00001,1e16,1e-05,5e-3"
        '24,1.7976931348623157e308]},"criteria":{"true":[0.1E1],"fa'
        'lse":[1e22]}}}}'
    ),
}

DEEP = 256
# A request text whose instructions nest deeper than pydantic-core writes.
WIRE_REFUSED_REQUESTS: dict[str, str] = {
    "deep_instructions": (
        '{"state":"s","model":"m","questions":{"answer":{"type":"noul","instructions":'
    )
    + "[" * DEEP
    + "]" * DEEP
    + "}}}"
}


def request_text(body: Mapping[str, Json]) -> str:
    """Return a request body as the compact JSON text of a System One request."""
    request = {"state": body["state"], "model": MODEL, "questions": body["questions"]}
    return json.dumps(request, ensure_ascii=False, separators=(",", ":"))


def requests() -> Iterator[tuple[str, str, bool]]:
    """Yield every case as its name, its request text and whether it is refused.

    Raises:
        ValueError: Two cases have the same name.
    """
    seen: set[str] = set()
    groups: tuple[tuple[Mapping[str, str], bool], ...] = (
        ({name: request_text(body) for name, body in REQUESTS.items()}, False),
        (WIRE_REQUESTS, False),
        (
            {name: request_text(body) for name, body in REFUSED_REQUESTS.items()},
            True,
        ),
        (WIRE_REFUSED_REQUESTS, True),
    )
    for group, refused in groups:
        for name, text in group.items():
            if name in seen:
                raise ValueError(f"case name repeated: {name}")
            seen.add(name)
            yield name, text, refused


def build(text: str) -> dict[str, Json]:
    """Return what upstream derives from one request text, or its refusal.

    Args:
        text: The request body as JSON text.

    Returns:
        ``user`` and ``schema`` for an accepted request, ``refused`` otherwise.
    """
    body = json.loads(text)
    try:
        prepared = convert_question_collection_to_validated_api_question_models(
            body["questions"]
        )
        schema: dict[str, Json] = {}
        modes: tuple[AnswerMode, ...] = get_args(AnswerMode)
        for mode in modes:
            model = create_llm_output_model(prepared, mode)
            schema[mode] = to_json(create_raw_output_schema(model)).decode()
    except pydantic.ValidationError as error:
        details = error.errors()
        refusal: dict[str, Json] = {"error": type(error).__name__}
        if len(details) > MAX_ERRORS:
            refusal["error_count"] = len(details)
        else:
            refusal["errors"] = [[detail["type"], *detail["loc"]] for detail in details]
        return {"refused": refusal}
    except ValueError as error:
        return {"refused": {"error": type(error).__name__, "message": str(error)}}
    return {"user": _serialize_state_as_user_prompt(body["state"]), "schema": schema}


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
    """Return one line of the output: compact JSON in ASCII."""
    return json.dumps(record, ensure_ascii=True, separators=(",", ":"))


def render() -> tuple[str, list[str]]:
    """Build the whole output and a summary of it.

    Returns:
        The file's text and the summary lines for standard output.

    Raises:
        ValueError: Upstream refuses a request the script lists as accepted, or
            accepts one it lists as refused.
    """
    header: dict[str, Json] = {
        "format": FORMAT,
        "generator": Path(__file__).name,
        **check_environment(),
        "answer_modes": list(get_args(AnswerMode)),
    }
    lines: list[str] = []
    accepted = 0
    refused = 0
    for name, text, expect_refused in requests():
        result = build(text)
        if ("refused" in result) != expect_refused:
            raise ValueError(f"{name}: refused is {'refused' in result}: {result}")
        accepted += not expect_refused
        refused += expect_refused
        lines.append(dump({"case": name, "request": text, **result}))
    header["accepted"] = accepted
    header["refused"] = refused
    text = "\n".join([dump(header), *lines]) + "\n"
    summary = [
        f"{accepted} requests accepted, {refused} refused",
        f"total: {accepted + refused} cases, {len(text.encode())} bytes",
    ]
    return text, summary


def main() -> int:
    """Write the cases, or compare them with the file that exists.

    Returns:
        0 on success; 1 when ``--check`` finds a difference.
    """
    parser = argparse.ArgumentParser(description=DESCRIPTION)
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument(
        "--check",
        action="store_true",
        help="compare the output file with fresh cases and write nothing",
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
        print(f"  file:  {in_file[:300]}", file=sys.stderr)
        print(f"  fresh: {fresh[:300]}", file=sys.stderr)
    if differing:
        print(f"{output.name}: {len(differing)} lines differ", file=sys.stderr)
        return 1
    print(f"{output.name}: equal to fresh cases, sha256 {digest}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
