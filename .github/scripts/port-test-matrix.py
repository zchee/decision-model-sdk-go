#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Check docs/port-test-matrix.md against the pinned upstream Python test suite.

The port of typesafe-sdk-python maps every upstream test function to a Go test
or to a documented deviation. This script enforces that mapping.

Usage (from the repository root)::

    .github/scripts/port-test-matrix.py --upstream PATH [--write FILE]
        [--names FILE] [--matrix FILE] [--no-planned]

Checks, in order; every failure is printed on its own line and the exit status
is 0 only when every check passes:

1. Pin. ``git -C PATH rev-parse HEAD`` must equal ``PINNED_COMMIT``. A drifted
   checkout would change the test set silently, so a mismatch stops the run.
2. Upstream names. Every ``def test_*`` and ``async def test_*`` in
   ``PATH/tests/**/test_*.py`` is derived with :mod:`ast` as
   ``tests/<relative file>::<name>`` (``tests/<file>::<Class>::<name>`` inside a
   class). Exactly ``EXPECTED_TEST_COUNT`` names must be found.
3. Name list. With ``--write FILE`` the sorted names are written to FILE.
   Without it, the ``--names`` file (default ``docs/upstream-tests.txt``) must
   equal the derived list line by line.
4. Matrix rows. The ``--matrix`` file (default ``docs/port-test-matrix.md``)
   groups rows under headings of the form ``### `tests/<file>` (<count>)``. Each
   table row has four cells: ID, upstream name (backtick-quoted), Go test or
   deviation, status; ``\\|`` is a literal pipe inside a cell. Every upstream
   test needs exactly one row; a row naming an unknown test, a repeated ID, a
   row outside a group and a malformed row are failures.
5. Status rules.

   - ``planned``: passes, unless ``--no-planned`` is given (then it fails).
   - ``ported``: the Go cell must contain at least one backtick-quoted ``Test…``
     identifier, and every such identifier must be listed by
     ``go test -list '.*' -tags live ./...``, which runs once from the
     repository root. A qualified identifier ``pkg.TestName`` must be listed by
     a package whose import path ends in ``/pkg``; an unqualified one may come
     from any package.
   - ``deviation``: the Go cell must cite an Appendix B row of the port plan
     (the deviation table that ships with the port). A citation is either a
     ``B<n>`` token (Appendix B row n) or the word ``deviation`` followed by a
     double-quoted reference to the Appendix B row, as in
     ``deviation "one deadline per attempt"``. A cell containing
     ``same deviation`` takes the citation of the nearest row above it in the
     same group. Backtick-quoted ``Test…`` identifiers in a deviation cell
     (partial deviations such as ``deviation "…" + `TestX```) must exist, as
     for ``ported``.

``go test -list`` runs even when no row needs it, so a module that stops
compiling under ``-tags live`` fails this check from the first wave on.

When the pin moves, update ``PINNED_COMMIT`` and ``EXPECTED_TEST_COUNT``
together, then regenerate the name list with ``--write``.
"""

from __future__ import annotations

import argparse
import ast
import re
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path

PINNED_COMMIT = "0ffd094c72ed9445223060b24ffd7a56aa781fb4"
EXPECTED_TEST_COUNT = 129
STATUSES = frozenset({"planned", "ported", "deviation"})

_HEADING = re.compile(r"^#{1,6}\s+`(tests/[^`]+\.py)`")
_CELL_SPLIT = re.compile(r"(?<!\\)\|")
_BACKTICK = re.compile(r"`([^`]+)`")
_UPSTREAM_NAME = re.compile(r"`(test_\w+)`")
_TEST_IDENT = re.compile(r"(?:([A-Za-z_]\w*)\.)?(Test\w*)")
_GO_IDENT = re.compile(r"(?:Test|Benchmark|Fuzz|Example)\w*")
_B_ROW = re.compile(r"\bB\d+\b")
_QUOTED_DEVIATION = re.compile(r'\bdeviation\s+"[^"]+"')
_SAME_DEVIATION = re.compile(r"\bsame deviation\b")


@dataclass(frozen=True)
class Row:
    """One table row of the matrix."""

    line: int
    file: str
    row_id: str
    upstream: str
    go_cell: str
    status: str

    @property
    def key(self) -> str:
        """Return the ``tests/<file>::<name>`` key used by the name list."""
        return f"{self.file}::{self.upstream}"


@dataclass
class Matrix:
    """Parsed matrix rows plus the parse failures found on the way."""

    rows: list[Row] = field(default_factory=list)
    failures: list[str] = field(default_factory=list)


def upstream_head(upstream: Path) -> str:
    """Return ``git rev-parse HEAD`` of the upstream checkout.

    Raises:
        RuntimeError: git failed (not a checkout, git missing).
    """
    try:
        proc = subprocess.run(
            ["git", "-C", str(upstream), "rev-parse", "HEAD"],
            capture_output=True,
            text=True,
            check=False,
        )
    except OSError as exc:
        raise RuntimeError(f"cannot run git: {exc}") from exc
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or f"git exited {proc.returncode}")
    return proc.stdout.strip()


class _TestCollector(ast.NodeVisitor):
    """Collect ``test_*`` functions, prefixing class names like pytest does."""

    def __init__(self) -> None:
        self.classes: list[str] = []
        self.names: list[str] = []

    def visit_ClassDef(self, node: ast.ClassDef) -> None:
        self.classes.append(node.name)
        self.generic_visit(node)
        self.classes.pop()

    def _visit_function(self, node: ast.FunctionDef | ast.AsyncFunctionDef) -> None:
        if node.name.startswith("test_"):
            self.names.append("::".join([*self.classes, node.name]))
        self.generic_visit(node)

    visit_FunctionDef = _visit_function
    visit_AsyncFunctionDef = _visit_function


def derive_upstream_tests(upstream: Path) -> list[str]:
    """Return the sorted ``tests/<file>::<name>`` list of upstream tests.

    Args:
        upstream: root of the upstream checkout (the directory holding tests/).

    Raises:
        SyntaxError: an upstream test file does not parse.
    """
    names: set[str] = set()
    for path in sorted((upstream / "tests").rglob("test_*.py")):
        collector = _TestCollector()
        collector.visit(ast.parse(path.read_bytes(), filename=str(path)))
        rel = path.relative_to(upstream).as_posix()
        names.update(f"{rel}::{name}" for name in collector.names)
    return sorted(names)


def check_names_file(path: Path, derived: list[str]) -> list[str]:
    """Compare the committed name list with the derived one."""
    try:
        committed = path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        return [f"{path}: cannot read ({exc.strerror}); regenerate it with --write"]
    if committed == derived:
        return []
    failures = [
        f"{path}: missing upstream test {name}"
        for name in sorted(set(derived) - set(committed))
    ]
    failures += [
        f"{path}: lists {name}, which upstream does not define"
        for name in sorted(set(committed) - set(derived))
    ]
    if not failures:
        failures.append(f"{path}: not sorted or has repeated lines")
    return [*failures, f"{path}: regenerate it with --write {path}"]


def _cells(line: str) -> list[str]:
    parts = _CELL_SPLIT.split(line.strip())
    if len(parts) < 2 or parts[0] or parts[-1]:
        return []
    return [part.strip().replace("\\|", "|") for part in parts[1:-1]]


def parse_matrix(text: str, source: str = "matrix") -> Matrix:
    """Parse the matrix Markdown into rows.

    Args:
        text: the Markdown document.
        source: the name used in failure messages.
    """
    matrix = Matrix()
    group: str | None = None
    for lineno, line in enumerate(text.splitlines(), start=1):
        if heading := _HEADING.match(line):
            group = heading.group(1)
            continue
        if line.startswith("#"):
            group = None
            continue
        if not line.startswith("|") or group is None:
            continue
        cells = _cells(line)
        if cells and (cells[0] == "ID" or set(cells[0]) <= set("-: ")):
            continue
        where = f"{source}:{lineno}"
        if len(cells) != 4:
            matrix.failures.append(f"{where}: expected 4 cells, found {len(cells)}")
            continue
        row_id, upstream_cell, go_cell, status = cells
        name = _UPSTREAM_NAME.fullmatch(upstream_cell)
        if name is None:
            matrix.failures.append(
                f"{where}: row {row_id}: upstream cell {upstream_cell!r} is not "
                "one backtick-quoted test_ name"
            )
            continue
        matrix.rows.append(Row(lineno, group, row_id, name.group(1), go_cell, status))
    return matrix


def parse_go_list(output: str) -> dict[str, set[str]]:
    """Map each import path to the names ``go test -list`` printed for it.

    ``go test`` prints a package's names before its ``ok <import path>`` line.
    """
    listed: dict[str, set[str]] = {}
    pending: set[str] = set()
    for raw in output.splitlines():
        line = raw.strip()
        fields = line.split()
        if fields[:1] == ["ok"] and len(fields) >= 2:
            listed.setdefault(fields[1], set()).update(pending)
            pending = set()
        elif fields[:1] == ["?"]:
            pending = set()
        elif _GO_IDENT.fullmatch(line):
            pending.add(line)
    return listed


def list_go_tests(repo: Path) -> tuple[dict[str, set[str]], list[str]]:
    """Run ``go test -list '.*' -tags live ./...`` once in ``repo``."""
    cmd = ["go", "test", "-list", ".*", "-tags", "live", "./..."]
    try:
        proc = subprocess.run(
            cmd, cwd=repo, capture_output=True, text=True, check=False
        )
    except OSError as exc:
        return {}, [f"cannot run {' '.join(cmd)}: {exc}"]
    if proc.returncode != 0:
        tail = (proc.stderr or proc.stdout).strip().splitlines()[-20:]
        return {}, [
            f"{' '.join(cmd)} exited {proc.returncode}",
            *(f"  {line}" for line in tail),
        ]
    return parse_go_list(proc.stdout), []


def _test_identifiers(cell: str) -> list[tuple[str | None, str]]:
    return [
        (match.group(1), match.group(2))
        for span in _BACKTICK.findall(cell)
        if (match := _TEST_IDENT.fullmatch(span))
    ]


def _missing_tests(
    row: Row, idents: list[tuple[str | None, str]], listed: dict[str, set[str]]
) -> list[str]:
    failures = []
    for pkg, name in idents:
        found = any(
            name in names and (pkg is None or path == pkg or path.endswith(f"/{pkg}"))
            for path, names in listed.items()
        )
        if not found:
            shown = f"{pkg}.{name}" if pkg else name
            failures.append(
                f"row {row.row_id} ({row.key}): {shown} is not listed by "
                "go test -list '.*' -tags live ./..."
            )
    return failures


def _citation(cell: str) -> str | None:
    if match := _B_ROW.search(cell):
        return match.group(0)
    if match := _QUOTED_DEVIATION.search(cell):
        return match.group(0)
    return None


def _status_failures(
    row: Row, cited: str | None, listed: dict[str, set[str]], *, no_planned: bool
) -> list[str]:
    idents = _test_identifiers(row.go_cell)
    match row.status:
        case "planned":
            if no_planned:
                return [f"row {row.row_id} ({row.key}) is still planned (--no-planned)"]
            return []
        case "ported":
            failures = _missing_tests(row, idents, listed)
            if not idents:
                failures.insert(
                    0,
                    f"row {row.row_id} ({row.key}) is ported but names no "
                    "backtick-quoted Test identifier",
                )
            return failures
        case "deviation":
            failures = _missing_tests(row, idents, listed)
            if cited is None:
                failures.insert(
                    0,
                    f"row {row.row_id} ({row.key}) is a deviation without an "
                    'Appendix B citation (B<n> or deviation "<reference>")',
                )
            return failures
        case _:
            return [
                (
                    f"row {row.row_id} ({row.key}): unknown status "
                    f"{row.status!r}; want one of {', '.join(sorted(STATUSES))}"
                )
            ]


def check_rows(
    rows: list[Row],
    upstream: list[str],
    listed: dict[str, set[str]],
    *,
    no_planned: bool,
) -> list[str]:
    """Apply the coverage and status rules; return one failure per line."""
    failures: list[str] = []
    by_key: dict[str, Row] = {}
    ids: dict[str, Row] = {}
    known = set(upstream)
    citation_above: dict[str, str] = {}
    for row in rows:
        if first := ids.get(row.row_id):
            failures.append(
                f"row {row.row_id} (line {row.line}) repeats the ID of line "
                f"{first.line}"
            )
        ids.setdefault(row.row_id, row)
        if first := by_key.get(row.key):
            failures.append(
                f"row {row.row_id} (line {row.line}) repeats {row.key} "
                f"(row {first.row_id})"
            )
        by_key.setdefault(row.key, row)
        if row.key not in known:
            failures.append(
                f"row {row.row_id} (line {row.line}): {row.key} is not an upstream "
                "test at the pinned commit"
            )

        cited = _citation(row.go_cell)
        if cited is not None:
            citation_above[row.file] = cited
        elif _SAME_DEVIATION.search(row.go_cell):
            cited = citation_above.get(row.file)
        failures += _status_failures(row, cited, listed, no_planned=no_planned)
    failures += [
        f"upstream test {name} has no matrix row"
        for name in upstream
        if name not in by_key
    ]
    return failures


def _parse_args(argv: list[str] | None) -> argparse.Namespace:
    repo = Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser(
        description="Check docs/port-test-matrix.md against the pinned upstream tests.",
    )
    parser.add_argument(
        "--upstream",
        required=True,
        type=Path,
        help=f"typesafe-sdk-python checkout at {PINNED_COMMIT}",
    )
    parser.add_argument(
        "--write",
        type=Path,
        metavar="FILE",
        help="write the derived name list to FILE instead of checking --names",
    )
    parser.add_argument(
        "--names",
        type=Path,
        default=repo / "docs" / "upstream-tests.txt",
        help="committed name list to check (default: %(default)s)",
    )
    parser.add_argument(
        "--matrix",
        type=Path,
        default=repo / "docs" / "port-test-matrix.md",
        help="matrix document (default: %(default)s)",
    )
    parser.add_argument(
        "--repo",
        type=Path,
        default=repo,
        help="module root where go test -list runs (default: %(default)s)",
    )
    parser.add_argument(
        "--no-planned",
        action="store_true",
        help="fail on any row whose status is still planned",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    """Run every check; return the process exit status."""
    args = _parse_args(argv)
    try:
        head = upstream_head(args.upstream)
    except RuntimeError as exc:
        print(f"{args.upstream}: not a git checkout: {exc}")
        return 1
    if head != PINNED_COMMIT:
        print(f"{args.upstream}: HEAD is {head}, want {PINNED_COMMIT}")
        return 1

    failures: list[str] = []
    upstream = derive_upstream_tests(args.upstream)
    if len(upstream) != EXPECTED_TEST_COUNT:
        failures.append(
            f"{args.upstream}: found {len(upstream)} upstream tests, "
            f"want {EXPECTED_TEST_COUNT}"
        )
    if args.write is not None:
        args.write.parent.mkdir(parents=True, exist_ok=True)
        args.write.write_text("\n".join(upstream) + "\n", encoding="utf-8")
    else:
        failures += check_names_file(args.names, upstream)

    try:
        text = args.matrix.read_text(encoding="utf-8")
    except OSError as exc:
        failures.append(f"{args.matrix}: cannot read ({exc.strerror})")
        text = ""
    matrix = parse_matrix(text, str(args.matrix))
    failures += matrix.failures

    listed, go_failures = list_go_tests(args.repo)
    failures += go_failures
    failures += check_rows(matrix.rows, upstream, listed, no_planned=args.no_planned)

    for failure in failures:
        print(failure)
    if failures:
        print(f"port-test-matrix: {len(failures)} failure(s)")
        return 1
    counts = dict.fromkeys(sorted(STATUSES), 0)
    for row in matrix.rows:
        counts[row.status] += 1
    summary = ", ".join(f"{n} {status}" for status, n in counts.items())
    print(f"port-test-matrix: OK, {len(upstream)} upstream tests ({summary})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
