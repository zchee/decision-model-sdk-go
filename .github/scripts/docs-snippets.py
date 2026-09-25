#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Prove every Go block in README.md and docs/ equals its examples/ source (XD1).

Skeleton: the checks are written in wave W6.3 of the port plan. Until then the
script accepts its final flags, prints that it is a skeleton and exits 0.

Usage (from the repository root)::

    .github/scripts/docs-snippets.py --readme README.md --docs docs --examples examples
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path


def main(argv: list[str] | None = None) -> int:
    """Parse the flags and report the skeleton status."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
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
    print("docs-snippets: skeleton: completed in W6.3")
    return 0


if __name__ == "__main__":
    sys.exit(main())
