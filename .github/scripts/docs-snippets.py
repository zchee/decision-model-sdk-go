#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Prove every Go block in README.md and docs/ equals its examples/ source (XD1).

Skeleton: the checks are written in wave W6.3 of the port plan. Until then the
script accepts its final flags, logs "not implemented until W6.3" to stderr
and exits 2, so a CI step wired to it before W6.3 fails instead of passing
without checking anything.

Usage (from the repository root)::

    .github/scripts/docs-snippets.py --readme README.md --docs docs --examples examples
"""

from __future__ import annotations

import argparse
import logging
import sys
from pathlib import Path

NOT_IMPLEMENTED = "docs-snippets: not implemented until W6.3"

_LOG = logging.getLogger("docs-snippets")


def main(argv: list[str] | None = None) -> int:
    """Parse the flags and refuse to run.

    Args:
        argv: the command-line arguments without the program name; ``None``
            reads ``sys.argv``.

    Returns:
        2, for every set of valid flags, until W6.3 implements the checks.

    Raises:
        SystemExit: the flags are invalid (status 2), or ``--help`` (status 0).
    """
    parser = argparse.ArgumentParser(
        description=__doc__.splitlines()[0], epilog=NOT_IMPLEMENTED
    )
    parser.add_argument(
        "--readme", required=True, type=Path, help="README to scan for Go blocks"
    )
    parser.add_argument(
        "--docs", required=True, type=Path, help="directory of Markdown documents"
    )
    parser.add_argument(
        "--examples",
        required=True,
        type=Path,
        help="directory of example packages the blocks must equal",
    )
    parser.parse_args(argv)
    _LOG.error("%s", NOT_IMPLEMENTED)
    return 2


if __name__ == "__main__":
    logging.basicConfig(format="%(message)s")
    sys.exit(main())
