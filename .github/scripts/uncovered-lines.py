#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""List every zero-count coverage block and require a reason for it (AC-Q2).

Skeleton: the checks are written in wave W6.3 of the port plan. Until then the
script accepts its final flags, logs "not implemented until W6.3" to stderr
and exits 2, so a CI step wired to it before W6.3 fails instead of passing
without checking anything.

Usage (from the repository root)::

    .github/scripts/uncovered-lines.py --profile coverage.out --doc docs/uncovered-lines.md
"""

from __future__ import annotations

import argparse
import logging
import sys
from pathlib import Path

NOT_IMPLEMENTED = "uncovered-lines: not implemented until W6.3"

_LOG = logging.getLogger("uncovered-lines")


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
        "--profile",
        required=True,
        type=Path,
        help="Go coverage profile (go test -coverprofile)",
    )
    parser.add_argument(
        "--doc",
        required=True,
        type=Path,
        help="Markdown file that gives a reason for every uncovered block",
    )
    parser.parse_args(argv)
    _LOG.error("%s", NOT_IMPLEMENTED)
    return 2


if __name__ == "__main__":
    logging.basicConfig(format="%(message)s")
    sys.exit(main())
