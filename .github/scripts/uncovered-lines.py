#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pyyaml>=6.0.2"]
# ///
"""List every zero-count coverage block and require a reason for it (AC-Q2).

The checks, in order:

1. The profile parses: a ``mode:`` line, then one ``file:line.col,line.col
   statements count`` line per block, each file inside the module that
   ``go.mod`` names and present on disk; at least one block lies outside the
   paths that ``.codecov.yaml`` ignores (an empty or foreign profile fails
   instead of passing with nothing to check).
2. Every block outside those paths is keyed by its file, the top-level
   declaration that holds it (``(*Client).Close``, ``NewClient``, or
   ``var name`` for a function literal in a package-level variable) and its code:
   the first source line inside the block that is more than braces. Line
   numbers are not part of the key, so an edit elsewhere in the file leaves
   the document valid.
3. ``docs/uncovered-lines.md`` holds exactly one table with the header
   ``| File | Function | Code | Blocks | Reason |``: one row per key, with the
   number of zero-count blocks that share the key and a one-line reason. The
   number is a count (``2``), or a range (``0-1``) for a block that timing
   covers in some runs and not in others.
4. It fails on a zero-count block whose key has no row (the row to paste is
   printed), on a zero-count number outside the row's count or range (a row
   with a count whose blocks are all covered is stale), on a row whose key
   names no block at all (the code changed), on a key listed twice and on a
   blank reason: the document can neither miss a block nor rot.

Usage (from the repository root)::

    .github/scripts/uncovered-lines.py --profile coverage.out --doc docs/uncovered-lines.md
"""

from __future__ import annotations

import argparse
import logging
import re
import sys
from collections import Counter
from dataclasses import dataclass
from pathlib import Path

import yaml

# The docstring's first line, kept apart from __doc__, which python -OO
# strips.
DESCRIPTION = (
    "List every zero-count coverage block and require a reason for it (AC-Q2)."
)

HEADER = ("File", "Function", "Code", "Blocks", "Reason")

_LOG = logging.getLogger("uncovered-lines")

_MODE = re.compile(r"mode: (set|count|atomic)")
_BLOCK = re.compile(r"(?P<file>.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)")
_MODULE = re.compile(r"module\s+(\S+)")
# A top-level declaration starts in column 1 in gofmt'd source.
_TOP_LEVEL = re.compile(r"(func|var|const|type|import|package)\b")
_FUNC = re.compile(
    r"func\s*(?:\(\s*(?:\w+\s+)?(?P<star>\*?)\s*(?P<recv>\w+)(?:\[[^\]]*\])?\s*\)\s*)?"
    r"(?P<name>\w+)"
)
_VAR = re.compile(r"var\s+(\w+)")
_VAR_GROUP = re.compile(r"var\s*\(")
# A spec of a gofmt'd group starts one tab in.
_GROUP_SPEC = re.compile(r"\t(\w+)\b")
_TABLE_LINE = re.compile(r" {0,3}\|")
_CELL_SPLIT = re.compile(r"(?<!\\)\|")
_SEPARATOR_CELL = re.compile(r":?-{3,}:?")
_CODE_SPAN = re.compile(r"(?P<fence>`+)(?P<body>.*?)(?P=fence)", re.DOTALL)
_BRACES_ONLY = re.compile(r"[{}()\[\],;\s]*")
_BLOCKS = re.compile(r"(?P<low>\d+)(?:-(?P<high>\d+))?")


@dataclass(frozen=True, order=True)
class Key:
    """What identifies a block across edits elsewhere in its file."""

    file: str
    function: str
    code: str


@dataclass(frozen=True)
class Block:
    """One statement block of a coverage profile."""

    file: str
    start_line: int
    start_col: int
    end_line: int
    end_col: int
    statements: int


@dataclass
class Profile:
    """The blocks of a profile, their counts, and the parse failures."""

    counts: dict[Block, int]
    failures: list[str]


@dataclass
class Keys:
    """The keyed blocks outside the ignored paths."""

    zero: Counter[Key]
    every: Counter[Key]
    considered: int
    failures: list[str]


@dataclass(frozen=True)
class Row:
    """One row of the document's table: ``low == high`` unless a range."""

    key: Key
    low: int
    high: int
    reason: str
    lineno: int

    @property
    def is_range(self) -> bool:
        """Report whether the row gives a range rather than a count."""
        return self.low != self.high


def module_path(repo: Path) -> str:
    """Return the module path that ``repo/go.mod`` declares.

    Raises:
        ValueError: go.mod has no module line.
        OSError: go.mod cannot be read.
    """
    text = (repo / "go.mod").read_text(encoding="utf-8")
    for line in text.splitlines():
        if m := _MODULE.fullmatch(line.strip()):
            return m.group(1)
    raise ValueError(f"{repo / 'go.mod'}: no module line")


def parse_profile(text: str, module: str, source: str = "profile") -> Profile:
    """Parse a Go coverage profile; files become paths relative to the module.

    A block listed more than once (profiles of several test binaries merged)
    keeps its largest count.
    """
    lines = text.splitlines()
    counts: dict[Block, int] = {}
    failures: list[str] = []
    if not lines or not _MODE.fullmatch(lines[0].strip()):
        return Profile({}, [f"{source}:1: not a Go coverage profile (no mode: line)"])
    prefix = module + "/"
    for lineno, raw in enumerate(lines[1:], start=2):
        line = raw.strip()
        if not line:
            continue
        m = _BLOCK.fullmatch(line)
        if m is None:
            failures.append(f"{source}:{lineno}: unparseable block line {line!r}")
            continue
        path = m.group("file")
        if not path.startswith(prefix):
            failures.append(f"{source}:{lineno}: {path} is outside module {module}")
            continue
        sl, sc, el, ec, statements, count = (int(g) for g in m.groups()[1:])
        block = Block(path.removeprefix(prefix), sl, sc, el, ec, statements)
        counts[block] = max(count, counts.get(block, 0))
    if not counts and not failures:
        failures.append(f"{source}: no blocks: the tests produced no coverage")
    return Profile(counts, failures)


def load_ignores(path: Path) -> list[str]:
    """Return the ``ignore`` globs of a Codecov configuration file.

    Raises:
        OSError: the file cannot be read.
        ValueError: the file is not YAML.
        TypeError: the top level is not a mapping, or ``ignore`` is not a list
            of strings.
    """
    try:
        loaded: object = yaml.safe_load(path.read_text(encoding="utf-8"))
    except yaml.YAMLError as exc:
        raise ValueError(f"{path}: not YAML: {exc}") from exc
    if not isinstance(loaded, dict):
        raise TypeError(f"{path}: the top level is not a mapping")
    ignores: object = loaded.get("ignore", [])
    if not isinstance(ignores, list) or not all(isinstance(g, str) for g in ignores):
        raise TypeError(f"{path}: ignore is not a list of strings")
    return [str(g) for g in ignores]


def glob_regex(glob: str) -> re.Pattern[str]:
    """Translate a Codecov path glob: ``**`` crosses directories, ``*`` does not."""
    out: list[str] = []
    i = 0
    while i < len(glob):
        if glob.startswith("**", i):
            out.append(".*")
            i += 2
        elif glob[i] == "*":
            out.append("[^/]*")
            i += 1
        elif glob[i] == "?":
            out.append("[^/]")
            i += 1
        else:
            out.append(re.escape(glob[i]))
            i += 1
    return re.compile("".join(out))


def ignored(path: str, globs: list[re.Pattern[str]]) -> bool:
    """Report whether a module-relative path matches an ignore glob."""
    return any(g.fullmatch(path) for g in globs)


def function_at(lines: list[str], line: int) -> str | None:
    """Name the top-level declaration that holds 1-based ``line``.

    A function or method is named as Go's symbol tables name it
    (``NewClient``, ``(*Client).Close``, ``Answer.Kind``); a package-level
    variable, whose initializer may hold a function literal, as
    ``var name``. Returns ``None`` for any other declaration.
    """
    for index in range(line - 1, -1, -1):
        text = lines[index]
        if _TOP_LEVEL.match(text) is None:
            continue
        if m := _FUNC.match(text):
            name = m.group("name")
            if m.group("recv") is None:
                return name
            if m.group("star"):
                return f"(*{m.group('recv')}).{name}"
            return f"{m.group('recv')}.{name}"
        if m := _VAR.match(text):
            return f"var {m.group(1)}"
        if _VAR_GROUP.match(text):
            for inner in reversed(lines[index + 1 : line]):
                if m := _GROUP_SPEC.match(inner):
                    return f"var {m.group(1)}"
        return None
    return None


def block_code(lines: list[bytes], block: Block) -> str:
    """Return the first line of the block's source that is more than braces.

    Profile columns are 1-based byte offsets; the end column is exclusive. The
    block's own braces are dropped, so a one-line ``{ return x }`` reads as
    ``return x``.
    """
    first, last = block.start_line - 1, block.end_line - 1
    if first == last:
        spans = [lines[first][block.start_col - 1 : block.end_col - 1]]
    else:
        spans = [
            lines[first][block.start_col - 1 :],
            *lines[first + 1 : last],
            lines[last][: block.end_col - 1],
        ]
    texts = [span.decode("utf-8", errors="replace").strip() for span in spans]
    texts[0] = texts[0].removeprefix("{").strip()
    texts[-1] = texts[-1].removesuffix("}").strip()
    for text in texts:
        if not _BRACES_ONLY.fullmatch(text):
            return text
    return "{}"


def key_blocks(profile: Profile, repo: Path, globs: list[re.Pattern[str]]) -> Keys:
    """Key every block outside the ignored paths, counting the zero-count ones.

    A zero-count block outside a function or a package-level variable fails;
    a covered one there is not keyed, since no row can name it.
    """
    keys = Keys(Counter(), Counter(), 0, [])
    sources: dict[str, tuple[list[str], list[bytes]] | None] = {}
    for block in sorted(
        profile.counts, key=lambda b: (b.file, b.start_line, b.start_col)
    ):
        if ignored(block.file, globs):
            continue
        keys.considered += 1
        if block.file not in sources:
            try:
                raw = (repo / block.file).read_bytes()
            except OSError as exc:
                keys.failures.append(f"{block.file}: cannot read ({exc.strerror})")
                sources[block.file] = None
            else:
                sources[block.file] = (
                    raw.decode("utf-8", errors="replace").splitlines(),
                    raw.splitlines(),
                )
        source = sources[block.file]
        if source is None:
            continue
        text, raw_lines = source
        where = f"{block.file}:{block.start_line}.{block.start_col}"
        if block.end_line > len(raw_lines):
            keys.failures.append(
                f"{where}: the block ends past the file: stale profile?"
            )
            continue
        zero = profile.counts[block] == 0
        function = function_at(text, block.start_line)
        if function is None:
            if zero:
                keys.failures.append(
                    f"{where}: the zero-count block is in no function or variable declaration"
                )
            continue
        key = Key(block.file, function, block_code(raw_lines, block))
        keys.every[key] += 1
        if zero:
            keys.zero[key] += 1
    if keys.considered == 0:
        keys.failures.append(
            "the profile has no block outside the paths .codecov.yaml ignores"
        )
    return keys


def _cells(line: str) -> list[str]:
    """Split a table line into cells; ``\\|`` is a literal ``|``."""
    body = line.strip()
    body = body.removeprefix("|")
    if body.endswith("|") and not body.endswith("\\|"):
        body = body[:-1]
    return [c.strip().replace("\\|", "|") for c in _CELL_SPLIT.split(body)]


def code_span(cell: str) -> str | None:
    """Return the text of a cell that is one Markdown code span, else None."""
    m = _CODE_SPAN.fullmatch(cell)
    if m is None:
        return None
    body = m.group("body")
    if len(body) >= 2 and body.startswith(" ") and body.endswith(" ") and body.strip():
        body = body[1:-1]
    return body


def render_code_span(text: str) -> str:
    """Write text as a code span that survives a backtick inside it."""
    fence = "`" * (max((len(r) for r in re.findall(r"`+", text)), default=0) + 1)
    pad = " " if text.startswith("`") or text.endswith("`") else ""
    return f"{fence}{pad}{text}{pad}{fence}"


def render_row(key: Key, blocks: str, reason: str) -> str:
    """Write one table row, escaping ``|`` in every cell."""
    cells = [
        render_code_span(key.file),
        render_code_span(key.function),
        render_code_span(key.code),
        blocks,
        reason,
    ]
    return "| " + " | ".join(c.replace("|", "\\|") for c in cells) + " |"


def parse_doc(text: str, source: str = "doc") -> tuple[list[Row], list[str]]:
    """Read the table rows of the document; returns the rows and the failures."""
    lines = text.splitlines()
    rows: list[Row] = []
    failures: list[str] = []
    tables = 0
    i = 0
    while i < len(lines):
        if _TABLE_LINE.match(lines[i]) is None or tuple(_cells(lines[i])) != HEADER:
            i += 1
            continue
        tables += 1
        header_line = i + 1
        i += 1
        if (
            i >= len(lines)
            or not all(_SEPARATOR_CELL.fullmatch(c) for c in _cells(lines[i]))
            or len(_cells(lines[i])) != len(HEADER)
        ):
            failures.append(f"{source}:{header_line}: the header has no separator row")
            continue
        i += 1
        while i < len(lines) and _TABLE_LINE.match(lines[i]):
            row = _parse_row(_cells(lines[i]), f"{source}:{i + 1}", i + 1)
            if isinstance(row, str):
                failures.append(row)
            else:
                rows.append(row)
            i += 1
    if tables != 1:
        failures.append(
            f"{source}: want exactly one table headed "
            f"| {' | '.join(HEADER)} |, found {tables}"
        )
    return rows, failures


def _parse_row(cells: list[str], where: str, lineno: int) -> Row | str:
    """Parse one row's cells, or return the failure."""
    if len(cells) != len(HEADER):
        return f"{where}: {len(cells)} cells, want {len(HEADER)}"
    spans = [code_span(c) for c in cells[:3]]
    if any(s is None or not s.strip() for s in spans):
        return f"{where}: File, Function and Code must each be one non-blank code span"
    file, function, code = (s or "" for s in spans)
    m = _BLOCKS.fullmatch(cells[3])
    if m is None:
        return f"{where}: Blocks {cells[3]!r} is neither a count nor a range low-high"
    low = int(m.group("low"))
    high = low if m.group("high") is None else int(m.group("high"))
    if high < 1 or low > high or (m.group("high") is not None and low == high):
        return f"{where}: Blocks {cells[3]!r}: want a count of 1 or more, or low < high"
    if _BRACES_ONLY.fullmatch(cells[4].replace("-", "")):
        return f"{where}: the reason is blank"
    return Row(Key(file, function, code), low, high, cells[4], lineno)


def compare(keys: Keys, rows: list[Row], source: str = "doc") -> list[str]:
    """Return the differences between the keyed blocks and the document."""
    failures: list[str] = []
    listed: dict[Key, Row] = {}
    for row in rows:
        if row.key in listed:
            failures.append(
                f"{source}:{row.lineno}: {row.key.file} {row.key.function} "
                f"{row.key.code!r} is listed twice (first on line "
                f"{listed[row.key].lineno})"
            )
            continue
        listed[row.key] = row
    for key in sorted(keys.zero.keys() - listed.keys()):
        failures.append(
            f"unlisted: {keys.zero[key]} zero-count block(s); add the row\n"
            f"  {render_row(key, str(keys.zero[key]), '<one-line reason>')}"
        )
    for key, row in sorted(listed.items()):
        where = f"{source}:{row.lineno}: {key.file} {key.function} {key.code!r}"
        zero = keys.zero[key]
        if key not in keys.every:
            failures.append(
                f"{where}: stale: no block has this key (the code changed): "
                f"delete or update the row"
            )
        elif zero == 0 and not row.is_range:
            failures.append(f"{where}: stale: covered now: delete the row")
        elif not row.low <= zero <= row.high:
            span = f"{row.low}-{row.high}" if row.is_range else str(row.low)
            failures.append(
                f"{where}: the row says {span} block(s), the profile has {zero} "
                f"zero-count of {keys.every[key]}"
            )
    return failures


def _parse_args(argv: list[str] | None) -> argparse.Namespace:
    repo = Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser(description=DESCRIPTION)
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
    parser.add_argument(
        "--codecov",
        type=Path,
        default=repo / ".codecov.yaml",
        help="Codecov configuration whose ignore globs apply (default: %(default)s)",
    )
    parser.add_argument(
        "--repo",
        type=Path,
        default=repo,
        help="module root holding go.mod and the sources (default: %(default)s)",
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
    try:
        module = module_path(args.repo)
        globs = [glob_regex(g) for g in load_ignores(args.codecov)]
        profile_text = args.profile.read_text(encoding="utf-8")
        doc_text = args.doc.read_text(encoding="utf-8")
    except (OSError, ValueError, TypeError) as exc:
        _LOG.error("uncovered-lines: %s", exc)
        return 1

    profile = parse_profile(profile_text, module, str(args.profile))
    keys = key_blocks(profile, args.repo, globs)
    rows, doc_failures = parse_doc(doc_text, str(args.doc))
    failures = profile.failures + keys.failures + doc_failures
    if not failures:
        failures = compare(keys, rows, str(args.doc))

    if failures:
        for failure in failures:
            _LOG.error("%s", failure)
        _LOG.error("uncovered-lines: %d failure(s)", len(failures))
        return 1
    blocks = sum(keys.zero.values())
    files = len({k.file for k in keys.zero})
    ranges = sum(row.is_range for row in rows)
    print(
        f"uncovered-lines: OK, {blocks} zero-count block(s) of {keys.considered} "
        f"in {files} file(s); {len(rows)} row(s) ({ranges} timing range(s)), "
        f"each with a reason"
    )
    return 0


if __name__ == "__main__":
    logging.basicConfig(format="%(message)s")
    sys.exit(main())
