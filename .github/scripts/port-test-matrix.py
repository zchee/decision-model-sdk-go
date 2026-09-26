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
        [--names FILE] [--matrix FILE] [--no-planned] [--deviations FILE]

Checks, in order. Every failure is logged to stderr on its own line, followed
by a failure count; on success one summary line goes to stdout. The exit
status is 0 only when every check passes.

1. Pin. ``git -C PATH rev-parse HEAD`` must equal ``PINNED_COMMIT``, and
   ``git -C PATH status --porcelain -- tests`` must print nothing. A drifted
   or locally edited checkout would change the test set silently, so either
   failure stops the run.
2. Upstream names. The names are derived with :mod:`ast` by pytest's default
   collection rules (``python_files = test_*.py *_test.py``,
   ``python_classes = Test``, ``python_functions = test``, all prefix matches;
   upstream's ``[tool.pytest.ini_options]`` overrides none of them):

   - files matching ``test_*.py`` or ``*_test.py`` under ``PATH/tests``,
     except under a directory that pytest's default ``norecursedirs`` skips;
   - module-level functions, sync or async, whose name starts with ``test``,
     as ``tests/<relative file>::<name>``;
   - methods whose name starts with ``test`` of module-level classes whose
     name starts with ``Test`` and that define neither ``__init__`` nor
     ``__new__``, as ``tests/<file>::<Class>::<name>``; nested ``Test``
     classes of such a class are collected by the same rule, as
     ``tests/<file>::<Class>::<Inner>::<name>``;
   - a function defined inside another function is never a test. Every branch
     of an ``if``, ``try``, ``with``, ``for``, ``while`` or ``match`` at module
     or class level counts, because the conditions are not evaluated.

   Tests that pytest would find through imports, base classes or ``__test__``
   attributes are not modelled; at the pin the derived list equals the output
   of ``pytest --collect-only`` with parameters stripped. Exactly
   ``EXPECTED_TEST_COUNT`` names must be found.
3. Name list. With ``--write FILE`` the sorted names are written to FILE.
   Without it, the ``--names`` file (default ``docs/upstream-tests.txt``) must
   equal the derived list line by line.
4. Matrix rows. The ``--matrix`` file (default ``docs/port-test-matrix.md``)
   groups rows under one level-3 heading per upstream file, of the form
   ``### `tests/<file>` (<count>)`` or ``### `tests/<file>` (<count>, <note>)``;
   a heading of another level naming a ``tests/`` file fails. <count> must
   equal the number of rows in the group, and a file heads at most one group.
   A table is a run of consecutive lines that start with ``|`` after at most
   three spaces (four spaces make a code block). Each group holds exactly one
   table: the header row ``| ID | Upstream | Go test / deviation | status |``,
   a separator row of four cells (each matches ``:?-{3,}:?``), then rows of
   four cells: ID, upstream name (backtick-quoted; ``Class::name`` for a
   method), Go test or deviation, status. A group without a table, a table
   without that header or separator, and a second table in a group fail; the
   rows of a second table are not read. ``\\|`` is a literal pipe inside a
   cell. Tables before the first group heading are ignored; after it, a table
   row outside a group fails. An ID cell that is blank or holds only ``-`` and
   ``:`` fails. Every upstream test needs exactly one row; a row naming an
   unknown test, a repeated ID and a malformed row are failures.
5. Status rules.

   - ``planned``: passes, unless ``--no-planned`` is given (then it fails).
   - ``ported``: the Go cell must contain at least one backtick-quoted
     ``Test…`` identifier, and every such identifier must be listed by
     ``go test -list '.*' -tags live ./...``, which runs once from the
     repository root. A qualified identifier ``pkg.TestName`` must be listed
     by a package whose import path ends in ``/pkg``; an unqualified one may
     come from any package. Tests of the root package are always written
     unqualified (``TestX``, never ``typesafe.TestX``): the root import path
     ends in ``/typesafe-sdk-go``, not ``/typesafe``, so a qualified name
     could never match it.
   - ``deviation``: the Go cell must cite a row of the deviation table
     (``docs/deviations.md``, which grew out of the port plan's Appendix B)
     as the word ``deviation`` followed by a double-quoted, non-blank
     reference, the row's key, as in ``deviation "one deadline per
     attempt"``. The rows carry no numbers, and ``B<n>`` would read as one
     of the plan's benchmark IDs B1-B6, so there is no numeric form. A cell
     containing ``same deviation`` takes the citation of the nearest row
     above it in the same group that carries one; rows without a citation
     in between are skipped. With ``--deviations`` the reference must be a
     key of that table (check 7).
     Backtick-quoted ``Test…`` identifiers in a deviation cell (partial
     deviations such as ``deviation "…" + `TestX```) must exist, as for
     ``ported``.
   - Every status: a backtick-quoted test name qualified by a path
     (``internal/codec.TestX``, ``./internal/codec/TestX``) fails, since
     ``go test -list`` knows packages by import path, not by directory; write
     ``codec.TestX`` or ``TestX``.

6. Status counts. The matrix states its rows by status on exactly one line
   ``Rows by status: <counts>.``, where <counts> is the text the summary
   line prints (``32 deviation, 6 planned, 91 ported``), so the document
   cannot show counts its rows do not have.
7. Deviation table (only with ``--deviations FILE``). FILE holds one or more
   tables whose header row is ``| Key | Python SDK 0.7.1 | Go SDK | Why |
   Matrix rows |``, each followed by a separator row of five cells, then
   rows of five cells; other tables are ignored. A key may not be blank or
   repeated, and the last cell lists the IDs of the matrix rows that cite
   the key, comma-separated, or is ``—`` when none does. The two must
   agree in both directions: every reference a matrix row cites, in any
   status and a ``same deviation`` row's inherited one included, is a key,
   and each key's cell names exactly the rows that cite it. A partial
   deviation citing two rows (``deviation "a" … deviation "b"``) is listed
   under both keys.

``go test -list`` runs even when no row needs it, so a module that stops
compiling under ``-tags live`` fails this check from the first wave on.

When the pin moves, update ``PINNED_COMMIT`` and ``EXPECTED_TEST_COUNT``
together, then regenerate the name list with ``--write``.
"""

from __future__ import annotations

import argparse
import ast
import fnmatch
import logging
import re
import subprocess
import sys
from collections.abc import Iterable, Iterator
from dataclasses import dataclass, field
from pathlib import Path

PINNED_COMMIT = "0ffd094c72ed9445223060b24ffd7a56aa781fb4"
EXPECTED_TEST_COUNT = 129
STATUSES = frozenset({"planned", "ported", "deviation"})

# pytest's defaults: python_files, and the norecursedirs globs.
TEST_FILE_GLOBS = ("test_*.py", "*_test.py")
NORECURSE_DIRS = (
    "*.egg",
    ".*",
    "_darcs",
    "build",
    "CVS",
    "dist",
    "node_modules",
    "venv",
    "{arch}",
)

_LOG = logging.getLogger("port-test-matrix")

HEADER_CELLS = ("ID", "Upstream", "Go test / deviation", "status")

_HEADING = re.compile(r"^###[ \t]+`(tests/[^`]+\.py)`")
_FILE_HEADING = re.compile(r"^#{1,6}[ \t]+`tests/[^`]+\.py`")
_HEADING_COUNT = re.compile(r"\((\d+)(?:,[^)]*)?\)")
_CELL_SPLIT = re.compile(r"(?<!\\)\|")
_SEPARATOR_CELL = re.compile(r":?-{3,}:?")
_BLANK_ID = re.compile(r"[-:\s]*")
_BACKTICK = re.compile(r"`([^`]+)`")
_UPSTREAM_NAME = re.compile(r"`((?:Test\w*::)*test\w*)`")
_TEST_IDENT = re.compile(r"(?:([A-Za-z_]\w*)\.)?(Test\w*)")
_PATH_TEST = re.compile(r"(?:[\w.-]*/)+(?:[A-Za-z_]\w*\.)?Test\w*")
_GO_IDENT = re.compile(r"(?:Test|Benchmark|Fuzz|Example)\w*")
_QUOTED_DEVIATION = re.compile(r'\bdeviation\s+"([^"]*)"')
_SAME_DEVIATION = re.compile(r"\bsame deviation\b")
_STATUS_LINE = re.compile(r"Rows by status: (?P<counts>.+)\.")
_ROW_ID = re.compile(r"[A-Z]+\d+")
DEVIATION_HEADER = ("Key", "Python SDK 0.7.1", "Go SDK", "Why", "Matrix rows")
NO_ROWS = "—"


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


@dataclass(frozen=True)
class Deviation:
    """One row of the deviation table: its key and the rows citing it."""

    line: int
    key: str
    rows: frozenset[str]


@dataclass
class Matrix:
    """Parsed matrix rows plus the parse failures found on the way."""

    rows: list[Row] = field(default_factory=list)
    failures: list[str] = field(default_factory=list)


def _git(upstream: Path, *args: str) -> str:
    """Run ``git -C upstream <args>`` and return its stdout.

    Args:
        upstream: the checkout to run git in.
        *args: the git subcommand and its arguments.

    Returns:
        The standard output, unmodified.

    Raises:
        RuntimeError: git could not run, or exited non-zero.
    """
    try:
        proc = subprocess.run(
            ["git", "-C", str(upstream), *args],
            capture_output=True,
            text=True,
            check=False,
        )
    except OSError as exc:
        raise RuntimeError(f"cannot run git: {exc}") from exc
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or f"git exited {proc.returncode}")
    return proc.stdout


def upstream_head(upstream: Path) -> str:
    """Return ``git rev-parse HEAD`` of the upstream checkout.

    Args:
        upstream: root of the upstream checkout.

    Returns:
        The full commit hash HEAD points at.

    Raises:
        RuntimeError: git failed (not a checkout, git missing).
    """
    return _git(upstream, "rev-parse", "HEAD").strip()


def upstream_changes(upstream: Path) -> list[str]:
    """Return the local changes under ``tests/`` of the upstream checkout.

    Args:
        upstream: root of the upstream checkout.

    Returns:
        One ``git status --porcelain`` line per modified, deleted, added or
        untracked path under ``tests/``; empty for a clean checkout.

    Raises:
        RuntimeError: git failed (not a checkout, git missing).
    """
    return _git(upstream, "status", "--porcelain", "--", "tests").splitlines()


def _scope(body: Iterable[ast.stmt]) -> Iterator[ast.stmt]:
    """Yield the function and class definitions of ``body``'s own scope.

    Compound statements (``if``, ``try``, ``with``, loops, ``match``) do not
    open a scope, so every branch of them is searched; function and class
    bodies do, so a definition is yielded without entering its body.
    """
    for stmt in body:
        if isinstance(stmt, ast.FunctionDef | ast.AsyncFunctionDef | ast.ClassDef):
            yield stmt
            continue
        for child in ast.iter_child_nodes(stmt):
            if isinstance(child, ast.stmt):
                yield from _scope([child])
            elif isinstance(child, ast.excepthandler | ast.match_case):
                yield from _scope(
                    node
                    for node in ast.iter_child_nodes(child)
                    if isinstance(node, ast.stmt)
                )


def _collect(body: Iterable[ast.stmt], prefix: tuple[str, ...]) -> Iterator[str]:
    """Yield the pytest node names defined directly in ``body``.

    Args:
        body: a module body or the body of a collected ``Test`` class.
        prefix: the enclosing class names, outermost first.

    Yields:
        ``name`` for a function, ``Class::name`` for a method.
    """
    for stmt in _scope(body):
        match stmt:
            case ast.FunctionDef(name=name) | ast.AsyncFunctionDef(name=name) if (
                name.startswith("test")
            ):
                yield "::".join([*prefix, name])
            case ast.ClassDef(name=name, body=class_body) if name.startswith("Test"):
                defined = {
                    node.name
                    for node in _scope(class_body)
                    if isinstance(node, ast.FunctionDef | ast.AsyncFunctionDef)
                }
                if not defined & {"__init__", "__new__"}:
                    yield from _collect(class_body, (*prefix, name))


def _test_files(tests: Path) -> Iterator[Path]:
    """Yield the files under ``tests`` that pytest would collect, sorted.

    Args:
        tests: the upstream ``tests`` directory.

    Yields:
        Each matching file once, in path order.
    """
    paths = {path for glob in TEST_FILE_GLOBS for path in tests.rglob(glob)}
    for path in sorted(paths):
        parents = path.relative_to(tests).parts[:-1]
        if not any(
            fnmatch.fnmatch(part, pat) for part in parents for pat in NORECURSE_DIRS
        ):
            yield path


def derive_upstream_tests(upstream: Path) -> list[str]:
    """Return the sorted ``tests/<file>::<name>`` list of upstream tests.

    Args:
        upstream: root of the upstream checkout (the directory holding tests/).

    Returns:
        Every test the collection rules of the module docstring find, sorted
        and without repeats.

    Raises:
        SyntaxError: an upstream test file does not parse.
    """
    names: set[str] = set()
    for path in _test_files(upstream / "tests"):
        module = ast.parse(path.read_bytes(), filename=str(path))
        rel = path.relative_to(upstream).as_posix()
        names.update(f"{rel}::{name}" for name in _collect(module.body, ()))
    return sorted(names)


def check_names_file(path: Path, derived: list[str]) -> list[str]:
    """Compare the committed name list with the derived one.

    Args:
        path: the committed name list, one ``tests/<file>::<name>`` per line.
        derived: the list :func:`derive_upstream_tests` returned.

    Returns:
        One failure message per missing or extra name, then a hint to
        regenerate the file; a single failure when the file cannot be read or
        differs only in order or repeats; empty when the file equals
        ``derived`` line by line.
    """
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


def _table_line(line: str) -> str | None:
    """Return ``line`` without its indentation when it is a table line.

    A table line starts with ``|`` after at most three spaces; four spaces
    would make it an indented code block.
    """
    stripped = line.lstrip(" ")
    if len(line) - len(stripped) <= 3 and stripped.startswith("|"):
        return stripped
    return None


def _cells(line: str) -> list[str]:
    """Split a ``| a | b |`` table line into stripped, unescaped cells.

    Returns an empty list when the line does not start and end with a pipe.
    """
    parts = _CELL_SPLIT.split(line.strip())
    if len(parts) < 2 or parts[0] or parts[-1]:
        return []
    return [part.strip().replace("\\|", "|") for part in parts[1:-1]]


def _is_separator(cells: list[str]) -> bool:
    """Report whether every cell is a table separator such as ``---``."""
    return bool(cells) and all(_SEPARATOR_CELL.fullmatch(cell) for cell in cells)


@dataclass
class _Group:
    """The file group being parsed: its heading, tables and row total."""

    file: str
    line: int
    count: int | None
    rows: int = 0
    tables: int = 0
    table_lines: int = 0  # lines read so far of the current table


def _close_group(group: _Group | None, source: str) -> list[str]:
    """Check a finished group: it has a table, and its count matches."""
    if group is None:
        return []
    where = f"{source}:{group.line}"
    if group.tables == 0:
        return [f"{where}: the {group.file} group has no table"]
    if group.count is None or group.rows == group.count:
        return []
    return [f"{where}: heading says {group.count} rows, the group has {group.rows}"]


def _table_structure(cells: list[str], group: _Group, where: str) -> list[str] | None:
    """Check the header and separator lines of a group's table.

    Args:
        cells: the cells of the current table line.
        group: the group, whose ``table_lines`` counts this line already.
        where: ``source:line`` for failure messages.

    Returns:
        The failures of a header or separator line, possibly none; ``None``
        when the line is a body row to parse (the third line on, or a second
        line that is no separator).
    """
    if group.table_lines == 1:
        if tuple(cells) == HEADER_CELLS:
            return []
        header = " | ".join(HEADER_CELLS)
        return [f"{where}: the {group.file} table must start with | {header} |"]
    if group.table_lines == 2 and _is_separator(cells):
        if len(cells) != 4:
            return [f"{where}: expected 4 cells, found {len(cells)}"]
        return []
    return None


def _parse_row(cells: list[str], group: str, lineno: int, where: str) -> Row | str:
    """Turn the cells of one body row into a :class:`Row` or a failure."""
    if len(cells) != 4:
        return f"{where}: expected 4 cells, found {len(cells)}"
    row_id, upstream_cell, go_cell, status = cells
    if _BLANK_ID.fullmatch(row_id):
        return f"{where}: ID cell {row_id!r} is blank or only '-' and ':'"
    name = _UPSTREAM_NAME.fullmatch(upstream_cell)
    if name is None:
        return (
            f"{where}: row {row_id}: upstream cell {upstream_cell!r} is not "
            "one backtick-quoted test name"
        )
    return Row(lineno, group, row_id, name.group(1), go_cell, status)


def parse_matrix(text: str, source: str = "matrix") -> Matrix:
    """Parse the matrix Markdown into rows.

    Args:
        text: the Markdown document.
        source: the name used in failure messages.

    Returns:
        The rows of every file group, in document order, and one failure per
        malformed heading, table, row or group count (the format is in check
        4 of the module docstring).
    """
    matrix = Matrix()
    group: _Group | None = None
    heads: dict[str, int] = {}
    in_table = False
    for lineno, line in enumerate(text.splitlines(), start=1):
        where = f"{source}:{lineno}"
        if line.startswith("#"):
            matrix.failures += _close_group(group, source)
            group = None
            in_table = False
            if heading := _HEADING.match(line):
                file = heading.group(1)
                if first := heads.get(file):
                    matrix.failures.append(
                        f"{where}: repeats the {file} heading of line {first}"
                    )
                heads.setdefault(file, lineno)
                count = _HEADING_COUNT.fullmatch(line[heading.end() :].strip())
                if count is None:
                    matrix.failures.append(
                        f"{where}: group heading does not end in (<count>)"
                    )
                group = _Group(file, lineno, int(count.group(1)) if count else None)
            elif _FILE_HEADING.match(line):
                matrix.failures.append(
                    f"{where}: a group heading is level 3: ### `tests/<file>` (<count>)"
                )
            continue
        row_line = _table_line(line)
        starts_table = row_line is not None and not in_table
        in_table = row_line is not None
        if row_line is None:
            continue
        if group is None:
            if heads:
                matrix.failures.append(f"{where}: table row outside a file group")
            continue
        if starts_table:
            group.tables += 1
            group.table_lines = 0
            if group.tables == 2:
                matrix.failures.append(
                    f"{where}: a second table in the {group.file} group; "
                    "a group holds exactly one"
                )
        if group.tables > 1:
            continue
        group.table_lines += 1
        cells = _cells(row_line)
        structure = _table_structure(cells, group, where)
        if structure is not None:
            matrix.failures += structure
            continue
        if group.table_lines == 2:
            matrix.failures.append(
                f"{where}: the header row of the {group.file} table is not "
                "followed by a separator row"
            )
        group.rows += 1
        row = _parse_row(cells, group.file, lineno, where)
        if isinstance(row, Row):
            matrix.rows.append(row)
        else:
            matrix.failures.append(row)
    matrix.failures += _close_group(group, source)
    return matrix


def parse_go_list(output: str) -> dict[str, set[str]]:
    """Map each import path to the names ``go test -list`` printed for it.

    ``go test`` prints a package's names before its ``ok <import path>`` line;
    a ``? <import path> [no test files]`` line discards nothing and lists
    nothing.

    Args:
        output: the standard output of ``go test -list '.*' ./...``.

    Returns:
        The ``Test``, ``Benchmark``, ``Fuzz`` and ``Example`` names of every
        package that printed an ``ok`` line, keyed by import path; a package
        with no matching names maps to an empty set.
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
    """Run ``go test -list '.*' -tags live ./...`` once in ``repo``.

    Args:
        repo: the module root.

    Returns:
        The :func:`parse_go_list` map and an empty failure list when the
        command succeeds; an empty map and failure lines (the command, its
        exit status and the last 20 lines of its output) when it cannot run
        or exits non-zero.
    """
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
    """Return the ``(package or None, TestName)`` pairs quoted in a Go cell."""
    return [
        (match.group(1), match.group(2))
        for span in _BACKTICK.findall(cell)
        if (match := _TEST_IDENT.fullmatch(span))
    ]


def _missing_tests(
    row: Row, idents: list[tuple[str | None, str]], listed: dict[str, set[str]]
) -> list[str]:
    """Return one failure per identifier that ``go test -list`` did not list."""
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
    """Return the first ``deviation "<reference>"`` with a non-blank reference."""
    for match in _QUOTED_DEVIATION.finditer(cell):
        if match.group(1).strip():
            return match.group(0)
    return None


def _path_qualified(row: Row) -> list[str]:
    """Return one failure per test name the Go cell qualifies by a path."""
    return [
        (
            f"row {row.row_id} ({row.key}): `{span}` names a test by a path; "
            "write pkg.TestName or TestName"
        )
        for span in _BACKTICK.findall(row.go_cell)
        if _PATH_TEST.fullmatch(span)
    ]


def _status_failures(
    row: Row, cited: str | None, listed: dict[str, set[str]], *, no_planned: bool
) -> list[str]:
    """Apply the status rule of one row (check 5 of the module docstring)."""
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
                    'Appendix B citation (deviation "<reference>")',
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
    """Apply the coverage and status rules to the parsed rows.

    Args:
        rows: the rows :func:`parse_matrix` returned, in document order.
        upstream: the derived upstream test names.
        listed: the :func:`parse_go_list` map of Go tests per import path.
        no_planned: fail every row whose status is still ``planned``.

    Returns:
        One failure message per repeated ID, repeated or unknown upstream
        test, status-rule violation and upstream test without a row; empty
        when the rows cover ``upstream`` exactly and every rule holds.
    """
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

        # "same deviation" inherits from the nearest row above that cites;
        # rows without a citation leave the remembered one in place.
        cited = _citation(row.go_cell)
        if cited is not None:
            citation_above[row.file] = cited
        elif _SAME_DEVIATION.search(row.go_cell):
            cited = citation_above.get(row.file)
        failures += _path_qualified(row)
        failures += _status_failures(row, cited, listed, no_planned=no_planned)
    failures += [
        f"upstream test {name} has no matrix row"
        for name in upstream
        if name not in by_key
    ]
    return failures


def status_summary(rows: list[Row]) -> str:
    """Return the rows' counts by status, as the summary line prints them."""
    counts = dict.fromkeys(sorted(STATUSES), 0)
    for row in rows:
        counts[row.status] += 1
    return ", ".join(f"{n} {status}" for status, n in counts.items())


def check_status_line(text: str, summary: str, source: str) -> list[str]:
    """Return the failures of check 6 (status counts) of the module docstring."""
    found = [
        (lineno, m.group("counts"))
        for lineno, line in enumerate(text.splitlines(), start=1)
        if (m := _STATUS_LINE.fullmatch(line.strip()))
    ]
    if len(found) != 1:
        return [
            f"{source}: want one line 'Rows by status: {summary}.', found {len(found)}"
        ]
    lineno, counts = found[0]
    if counts != summary:
        return [f"{source}:{lineno}: the rows are {summary}; the line says {counts}"]
    return []


def _deviation_row(cells: list[str], lineno: int, where: str) -> Deviation | str:
    """Turn the cells of one deviation table row into a row or a failure."""
    if len(cells) != len(DEVIATION_HEADER):
        return f"{where}: expected {len(DEVIATION_HEADER)} cells, found {len(cells)}"
    key, rows_cell = cells[0], cells[-1]
    if not key:
        return f"{where}: the key cell is blank"
    if rows_cell == NO_ROWS:
        return Deviation(lineno, key, frozenset())
    ids = [part.strip() for part in rows_cell.split(",")]
    if not all(_ROW_ID.fullmatch(i) for i in ids) or len(set(ids)) != len(ids):
        return (
            f"{where}: {key!r}: the Matrix rows cell {rows_cell!r} is not distinct "
            f"comma-separated row IDs or {NO_ROWS}"
        )
    return Deviation(lineno, key, frozenset(ids))


def parse_deviations(text: str, source: str) -> tuple[list[Deviation], list[str]]:
    """Parse the deviation tables of a document (check 7).

    Args:
        text: the Markdown document.
        source: the name used in failure messages.

    Returns:
        The rows of every deviation table, in document order, and one
        failure per malformed row, repeated key or table without its
        separator; a document without a deviation table is a failure.
    """
    rows: list[Deviation] = []
    failures: list[str] = []
    lines_in_table = 0
    deviation_table = False
    for lineno, line in enumerate(text.splitlines(), start=1):
        body = _table_line(line)
        if body is None:
            lines_in_table, deviation_table = 0, False
            continue
        lines_in_table += 1
        cells = _cells(body)
        where = f"{source}:{lineno}"
        if lines_in_table == 1:
            deviation_table = tuple(cells) == DEVIATION_HEADER
            continue
        if not deviation_table:
            continue
        if lines_in_table == 2:
            if not _is_separator(cells) or len(cells) != len(DEVIATION_HEADER):
                failures.append(
                    f"{where}: a deviation table's header must be followed by a "
                    f"separator of {len(DEVIATION_HEADER)} cells"
                )
                deviation_table = False
            continue
        parsed = _deviation_row(cells, lineno, where)
        if isinstance(parsed, str):
            failures.append(parsed)
        else:
            rows.append(parsed)
    if not rows and not failures:
        header = " | ".join(DEVIATION_HEADER)
        failures.append(f"{source}: no deviation table (a table headed | {header} |)")
    first: dict[str, Deviation] = {}
    for row in rows:
        if (seen := first.get(row.key)) is not None:
            failures.append(
                f"{source}:{row.line}: the key {row.key!r} repeats line {seen.line}"
            )
        first.setdefault(row.key, row)
    return rows, failures


def citations(rows: list[Row]) -> dict[str, set[str]]:
    """Return the IDs of the matrix rows citing each deviation reference.

    A row cites every non-blank ``deviation "<reference>"`` in its Go cell;
    a row with none that says ``same deviation`` cites what the nearest row
    above it in its group cites, as check 5 reads it.
    """
    cited: dict[str, set[str]] = {}
    above: dict[str, list[str]] = {}
    for row in rows:
        keys = [
            m.group(1)
            for m in _QUOTED_DEVIATION.finditer(row.go_cell)
            if m.group(1).strip()
        ]
        if keys:
            above[row.file] = keys
        elif _SAME_DEVIATION.search(row.go_cell):
            keys = above.get(row.file, [])
        for key in keys:
            cited.setdefault(key, set()).add(row.row_id)
    return cited


def check_deviations(
    rows: list[Row], deviations: list[Deviation], source: str
) -> list[str]:
    """Return the failures of check 7: citations and table rows disagreeing.

    Args:
        rows: the matrix rows, in document order.
        deviations: the rows :func:`parse_deviations` returned.
        source: the deviation document's name, for messages.

    Returns:
        One failure per reference no key matches, per key whose cell leaves
        out a row citing it, and per key whose cell names a row that does
        not cite it.
    """
    failures: list[str] = []
    table = {d.key: d for d in deviations}
    cited = citations(rows)
    for key, ids in sorted(cited.items()):
        if (d := table.get(key)) is None:
            failures.append(
                f'rows {", ".join(sorted(ids))} cite deviation "{key}", which '
                f"{source} does not list"
            )
        elif missing := ids - d.rows:
            failures.append(
                f"{source}:{d.line}: {key!r} does not name the rows citing it: "
                f"{', '.join(sorted(missing))}"
            )
    for d in deviations:
        if extra := d.rows - cited.get(d.key, set()):
            failures.append(
                f"{source}:{d.line}: {d.key!r} names rows that do not cite it: "
                f"{', '.join(sorted(extra))}"
            )
    return failures


def _parse_args(argv: list[str] | None) -> argparse.Namespace:
    """Parse the command line; ``--upstream`` is required."""
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
    parser.add_argument(
        "--deviations",
        type=Path,
        metavar="FILE",
        help="deviation table the matrix's citations must match (check 7)",
    )
    return parser.parse_args(argv)


def _pin_failures(upstream: Path) -> list[str]:
    """Return the failures of check 1 (pin) of the module docstring."""
    try:
        head = upstream_head(upstream)
        changes = upstream_changes(upstream)
    except RuntimeError as exc:
        return [f"{upstream}: not a git checkout: {exc}"]
    if head != PINNED_COMMIT:
        return [f"{upstream}: HEAD is {head}, want {PINNED_COMMIT}"]
    if changes:
        return [
            f"{upstream}: tests/ has local changes against {PINNED_COMMIT}:",
            *(f"  {line}" for line in changes),
        ]
    return []


def main(argv: list[str] | None = None) -> int:
    """Run every check.

    Args:
        argv: the command-line arguments without the program name; ``None``
            reads ``sys.argv``.

    Returns:
        The process exit status: 0 when every check passes, 1 otherwise.

    Raises:
        SystemExit: the arguments are invalid (status 2), or ``--help``.
        SyntaxError: an upstream test file does not parse.
    """
    args = _parse_args(argv)
    if pin := _pin_failures(args.upstream):
        for failure in pin:
            _LOG.error("%s", failure)
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
    summary = status_summary(matrix.rows)
    if text:
        failures += check_status_line(text, summary, str(args.matrix))
    if args.deviations is not None:
        try:
            dev_text = args.deviations.read_text(encoding="utf-8")
        except OSError as exc:
            failures.append(f"{args.deviations}: cannot read ({exc.strerror})")
        else:
            deviations, dev_failures = parse_deviations(dev_text, str(args.deviations))
            failures += dev_failures
            failures += check_deviations(matrix.rows, deviations, str(args.deviations))

    if failures:
        for failure in failures:
            _LOG.error("%s", failure)
        _LOG.error("port-test-matrix: %d failure(s)", len(failures))
        return 1
    print(f"port-test-matrix: OK, {len(upstream)} upstream tests ({summary})")
    return 0


if __name__ == "__main__":
    logging.basicConfig(format="%(message)s")
    sys.exit(main())
