#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""List every zero-count coverage block and require a reason for it (AC-Q2).

Skeleton: the checks are written in wave W6.3 of the port plan. Until then the
script accepts its final flags, prints that it is a skeleton and exits 0.

Usage (from the repository root)::

    .github/scripts/uncovered-lines.py --profile coverage.out --doc docs/uncovered-lines.md
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path


def main(argv: list[str] | None = None) -> int:
    """Parse the flags and report the skeleton status."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
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
    print("uncovered-lines: skeleton: completed in W6.3")
    return 0


if __name__ == "__main__":
    sys.exit(main())
