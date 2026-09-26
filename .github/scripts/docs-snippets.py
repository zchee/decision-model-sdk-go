#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Prove every Go block in README.md and docs/ equals its examples/ source (XD1).

The checks, in order:

1. The README exists, the docs directory holds at least one Markdown file
   (``**/*.md``), and the examples path, when it exists, is a directory: a
   mistyped path fails instead of passing with nothing to check.
2. Every fenced code block (backticks or tildes, three or more, at any
   indentation, so that a fence nested in a list item counts) whose info
   string's first word is ``go`` or ``golang`` (not ``go.mod``) is a Go
   block. The line before its opening fence, blank lines skipped, must be
   the marker ``<!-- example: <path> -->``, where ``<path>`` is a file under
   the examples directory (``quickstart/main.go``); the block's lines, with
   the fence's indentation removed and a newline after each, must equal that
   file byte for byte. An untagged block whose first line starts with
   ``package`` is refused, since it would escape the check.
3. Every ``.go`` file under the examples directory, ``_test.go`` files
   aside, is shown by some block: a source without its block fails, as a
   block without its source does. A marker not followed by a Go block, a
   marker naming a path outside the examples directory, and an unclosed
   fence fail too.

The examples are packages of this module, so ``go vet ./...`` compiles
them; this script proves only that the Markdown shows them as they are.

Usage (from the repository root)::

    .github/scripts/docs-snippets.py --readme README.md --docs docs --examples examples
"""

from __future__ import annotations

import argparse
import difflib
import logging
import re
import sys
from dataclasses import dataclass
from pathlib import Path

# The docstring's first line, kept apart from __doc__, which python -OO
# strips.
DESCRIPTION = (
    "Prove every Go block in README.md and docs/ equals its examples/ source (XD1)."
)

_LOG = logging.getLogger("docs-snippets")

# A fence opens at any indentation: inside a list item CommonMark measures
# it from the item's content column, which this line-based reader does not
# track. An indented code block whose text starts with three backticks is
# then read as a fence too, which fails safe (it needs a marker).
_OPEN = re.compile(r"(?P<indent>[ \t]*)(?P<fence>`{3,}|~{3,})(?P<info>.*)")
_MARKER = re.compile(r"[ \t]*<!--\s*example:\s*(?P<path>\S+)\s*-->\s*")
_GO_WORDS = frozenset({"go", "golang"})


@dataclass(frozen=True)
class Block:
    """A fenced code block of a Markdown file."""

    source: str
    lineno: int
    info: str
    lines: list[str]
    marker: str | None
    marker_lineno: int

    @property
    def is_go(self) -> bool:
        """Report whether the info string names Go."""
        words = self.info.split()
        return bool(words) and words[0].lower() in _GO_WORDS


def parse_blocks(text: str, source: str) -> tuple[list[Block], list[str]]:
    """Return the fenced blocks of a Markdown text and the failures.

    A marker line stays pending across blank lines and is consumed by the
    next fence; a marker followed by anything else fails.
    """
    lines = text.splitlines()
    blocks: list[Block] = []
    failures: list[str] = []
    marker: tuple[str, int] | None = None
    i = 0
    while i < len(lines):
        line = lines[i]
        if m := _MARKER.fullmatch(line):
            if marker is not None:
                failures.append(
                    f"{source}:{marker[1]}: the marker is followed by another "
                    f"marker, not by a Go block"
                )
            marker = (m.group("path"), i + 1)
            i += 1
            continue
        m = _OPEN.fullmatch(line)
        if m is None or (m.group("fence")[0] == "`" and "`" in m.group("info")):
            if line.strip() and marker is not None:
                failures.append(
                    f"{source}:{marker[1]}: the marker is not followed by a Go block"
                )
                marker = None
            i += 1
            continue
        indent, fence = len(m.group("indent")), m.group("fence")
        closing = re.compile(rf"[ \t]*{re.escape(fence[0])}{{{len(fence)},}}\s*")
        body: list[str] = []
        j = i + 1
        while j < len(lines) and closing.fullmatch(lines[j]) is None:
            body.append(_dedent(lines[j], indent))
            j += 1
        if j == len(lines):
            failures.append(f"{source}:{i + 1}: the fence is never closed")
            break
        blocks.append(
            Block(
                source,
                i + 1,
                m.group("info"),
                body,
                None if marker is None else marker[0],
                0 if marker is None else marker[1],
            )
        )
        marker = None
        i = j + 1
    if marker is not None:
        failures.append(
            f"{source}:{marker[1]}: the marker is not followed by a Go block"
        )
    return blocks, failures


def _dedent(line: str, indent: int) -> str:
    """Remove up to ``indent`` leading blanks, as CommonMark does in a fence."""
    n = 0
    while n < indent and n < len(line) and line[n] in " \t":
        n += 1
    return line[n:]


def example_sources(examples: Path) -> list[Path]:
    """Return the example files a block must show: ``*.go`` minus ``_test.go``."""
    if not examples.exists():
        return []
    return sorted(
        p
        for p in examples.rglob("*.go")
        if p.is_file() and not p.name.endswith("_test.go")
    )


def check_block(block: Block, examples: Path) -> tuple[Path | None, list[str]]:
    """Check one block; returns the example file it shows and the failures."""
    where = f"{block.source}:{block.lineno}"
    if not block.is_go:
        if (
            block.lines
            and block.lines[0].startswith("package ")
            and not block.info.strip()
        ):
            return None, [
                (
                    f"{where}: an untagged block that starts with a package clause: "
                    f"tag it go and give it an example marker"
                )
            ]
        if block.marker is not None:
            return None, [
                (
                    f"{block.source}:{block.marker_lineno}: the marker is followed by "
                    f"a {block.info.strip() or 'untagged'} block, not a Go block"
                )
            ]
        return None, []
    if block.marker is None:
        return None, [
            (
                f"{where}: a Go block without a <!-- example: <path> --> marker "
                f"on the line before it"
            )
        ]
    root = examples.resolve()
    path = (examples / block.marker).resolve()
    if not path.is_relative_to(root):
        return None, [f"{where}: the marker names {block.marker}, outside {examples}"]
    if not path.is_file():
        return None, [
            f"{where}: the marker names {examples / block.marker}, which does not exist"
        ]
    want = path.read_bytes()
    got = "".join(line + "\n" for line in block.lines).encode("utf-8")
    if got == want:
        return path, []
    diff = difflib.unified_diff(
        want.decode("utf-8", errors="replace").splitlines(),
        got.decode("utf-8", errors="replace").splitlines(),
        fromfile=str(examples / block.marker),
        tofile=where,
        lineterm="",
    )
    return path, [
        f"{where}: the block differs from {examples / block.marker}:\n"
        + "\n".join(diff)
    ]


def markdown_files(readme: Path, docs: Path) -> tuple[list[Path], list[str]]:
    """Return the Markdown files to scan and the failures of check 1."""
    failures: list[str] = []
    files: list[Path] = []
    if readme.is_file():
        files.append(readme)
    else:
        failures.append(f"{readme}: no such file")
    found = (
        sorted(p for p in docs.rglob("*.md") if p.is_file()) if docs.is_dir() else []
    )
    if not found:
        failures.append(f"{docs}: no Markdown file to scan")
    return files + found, failures


def _parse_args(argv: list[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=DESCRIPTION)
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
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    """Run every check.

    Args:
        argv: the command-line arguments without the program name; ``None``
            reads ``sys.argv``.

    Returns:
        The process exit status: 0 when every check passes, 1 otherwise.

    Raises:
        SystemExit: the flags are invalid (status 2), or ``--help`` (status 0).
    """
    args = _parse_args(argv)
    files, failures = markdown_files(args.readme, args.docs)
    if args.examples.exists() and not args.examples.is_dir():
        failures.append(f"{args.examples}: not a directory")
    shown: set[Path] = set()
    go_blocks = 0
    for file in files:
        blocks, parse_failures = parse_blocks(
            file.read_text(encoding="utf-8"), str(file)
        )
        failures += parse_failures
        for block in blocks:
            go_blocks += block.is_go
            path, block_failures = check_block(block, args.examples)
            failures += block_failures
            if path is not None:
                shown.add(path)
    sources = example_sources(args.examples)
    failures += [
        f"{source}: no Markdown block shows this example source"
        for source in sources
        if source.resolve() not in shown
    ]
    if failures:
        for failure in failures:
            _LOG.error("%s", failure)
        _LOG.error("docs-snippets: %d failure(s)", len(failures))
        return 1
    print(
        f"docs-snippets: OK, {len(files)} Markdown file(s), {go_blocks} Go "
        f"block(s), {len(sources)} example source(s), each block equal to its source"
    )
    return 0


if __name__ == "__main__":
    logging.basicConfig(format="%(message)s")
    sys.exit(main())
