"""Tests for port-test-matrix.py.

Run from the repository root with
``uvx --with pyyaml pytest -q .github/scripts``. Type-check the scripts and
these tests with ``uvx --with pytest --with types-PyYAML mypy --strict
.github/scripts``: mypy needs pytest installed next to it to see its types,
and types-PyYAML for the yaml import of uncovered-lines.py.
"""

from __future__ import annotations

import logging
import subprocess
import sys
import textwrap
from collections.abc import Callable
from pathlib import Path
from typing import Any

import pytest
from conftest import git, load_script

SCRIPT = Path(__file__).with_name("port-test-matrix.py")

ptm = load_script(SCRIPT.name)

UPSTREAM = [
    "tests/test_a.py::test_one",
    "tests/test_a.py::test_two",
    "tests/test_b.py::test_three",
]
HEADER = "| ID | Upstream | Go test / deviation | status |\n| --- | --- | --- | --- |"


def _matrix(*rows: str, file: str = "tests/test_a.py", count: int | None = None) -> str:
    shown = len(rows) if count is None else count
    return f"### `{file}` ({shown})\n\n{HEADER}\n" + "\n".join(rows) + "\n"


def _rows(text: str) -> list[Any]:
    matrix = ptm.parse_matrix(text)
    assert matrix.failures == []
    rows: list[Any] = matrix.rows
    return rows


def _full(a_rows: tuple[str, ...], b_row: str) -> str:
    return _matrix(*a_rows) + "\n" + _matrix(b_row, file="tests/test_b.py")


PLANNED_A = (
    "| A1 | `test_one` | `TestOne` | planned |",
    "| A2 | `test_two` | `TestTwo` | planned |",
)
PLANNED_B = "| B1 | `test_three` | `TestThree` | planned |"
PORTED_A = (
    "| A1 | `test_one` | `TestOne` | ported |",
    "| A2 | `test_two` | `TestTwo` | ported |",
)
PORTED_B = "| B1 | `test_three` | `TestThree` | ported |"
LISTED = {
    "example.com/m": {"TestOne", "TestTwo", "TestThree", "BenchmarkNoop"},
    "example.com/m/livetest": {"TestLive"},
}

# Constructs on which the ast collector and pytest's runtime collection agree.
ORACLE_MODULE = """
import sys

def helper():
    def test_nested_in_helper():
        pass

def test_sync():
    def test_nested():
        pass

async def test_async():
    pass

def testnounderscore():
    pass

class TestGroup:
    def test_method(self):
        pass

    def helper(self):
        pass

    class TestInner:
        def test_inner(self):
            pass

    class Inner:
        def test_not_collected(self):
            pass

    if True:
        def test_in_class_if(self):
            pass

class TestWithInit:
    def __init__(self):
        pass

    def test_skipped(self):
        pass

class TestWithNew:
    def __new__(cls):
        return object.__new__(cls)

    def test_skipped_new(self):
        pass

class Helper:
    def test_not_collected(self):
        pass

if True:
    def test_conditional():
        pass

try:
    def test_in_try():
        pass
finally:
    def test_in_finally():
        pass

with open(__file__):
    def test_in_with():
        pass

for _ in range(1):
    def test_in_for():
        pass

match sys.platform:
    case _:
        def test_in_match():
            pass

@staticmethod
def test_decorated():
    pass

def test_redefined():
    pass

def test_redefined():
    pass
"""


def _write_oracle_tree(root: Path) -> None:
    tests = root / "tests"
    for sub in ("sub", ".hidden", "build", "node_modules"):
        (tests / sub).mkdir(parents=True)
    (tests / "test_a.py").write_text(textwrap.dedent(ORACLE_MODULE))
    (tests / "sub" / "b_test.py").write_text("def test_suffix():\n    pass\n")
    (tests / "test_both_test.py").write_text("def test_both():\n    pass\n")
    (tests / "helpers.py").write_text("def test_helper_file():\n    pass\n")
    for skipped in (".hidden", "build", "node_modules"):
        (tests / skipped / "test_h.py").write_text("def test_hidden():\n    pass\n")


class TestDeriveUpstreamTests:
    def test_matches_pytest_collect_only(self, tmp_path: Path) -> None:
        _write_oracle_tree(tmp_path)
        (tmp_path / "pytest.ini").write_text("[pytest]\n")
        proc = subprocess.run(
            [
                sys.executable,
                "-m",
                "pytest",
                "--collect-only",
                "-q",
                "-p",
                "no:cacheprovider",
                "-c",
                str(tmp_path / "pytest.ini"),
                "tests",
            ],
            cwd=tmp_path,
            capture_output=True,
            text=True,
            check=False,
        )
        assert proc.returncode == 0, proc.stdout + proc.stderr
        collected = sorted(line for line in proc.stdout.splitlines() if "::" in line)

        got = ptm.derive_upstream_tests(tmp_path)

        assert got == collected
        assert got == [
            "tests/sub/b_test.py::test_suffix",
            "tests/test_a.py::TestGroup::TestInner::test_inner",
            "tests/test_a.py::TestGroup::test_in_class_if",
            "tests/test_a.py::TestGroup::test_method",
            "tests/test_a.py::test_async",
            "tests/test_a.py::test_conditional",
            "tests/test_a.py::test_decorated",
            "tests/test_a.py::test_in_finally",
            "tests/test_a.py::test_in_for",
            "tests/test_a.py::test_in_match",
            "tests/test_a.py::test_in_try",
            "tests/test_a.py::test_in_with",
            "tests/test_a.py::test_redefined",
            "tests/test_a.py::test_sync",
            "tests/test_a.py::testnounderscore",
            "tests/test_both_test.py::test_both",
        ]

    def test_every_branch_counts_because_conditions_are_not_evaluated(
        self, tmp_path: Path
    ) -> None:
        tests = tmp_path / "tests"
        tests.mkdir()
        (tests / "test_a.py").write_text(
            textwrap.dedent(
                """
                if False:
                    def test_if():
                        pass
                else:
                    def test_else():
                        pass

                try:
                    pass
                except ImportError:
                    def test_except():
                        pass
                """
            )
        )
        assert ptm.derive_upstream_tests(tmp_path) == [
            "tests/test_a.py::test_else",
            "tests/test_a.py::test_except",
            "tests/test_a.py::test_if",
        ]

    def test_syntax_error_propagates(self, tmp_path: Path) -> None:
        (tmp_path / "tests").mkdir()
        (tmp_path / "tests" / "test_bad.py").write_text("def test_x(:\n")
        with pytest.raises(SyntaxError):
            ptm.derive_upstream_tests(tmp_path)


class TestNamesFile:
    def test_equal_file_passes(self, tmp_path: Path) -> None:
        path = tmp_path / "names.txt"
        path.write_text("\n".join(UPSTREAM) + "\n")
        assert ptm.check_names_file(path, UPSTREAM) == []

    def test_missing_and_extra_names_are_reported(self, tmp_path: Path) -> None:
        path = tmp_path / "names.txt"
        path.write_text("tests/test_a.py::test_one\ntests/test_z.py::test_gone\n")
        got = ptm.check_names_file(path, UPSTREAM)
        assert f"{path}: missing upstream test tests/test_a.py::test_two" in got
        assert f"{path}: missing upstream test tests/test_b.py::test_three" in got
        assert (
            f"{path}: lists tests/test_z.py::test_gone, which upstream does not define"
            in got
        )
        assert got[-1] == f"{path}: regenerate it with --write {path}"

    def test_order_difference_is_reported(self, tmp_path: Path) -> None:
        path = tmp_path / "names.txt"
        path.write_text("\n".join(reversed(UPSTREAM)) + "\n")
        got = ptm.check_names_file(path, UPSTREAM)
        assert got[0] == f"{path}: not sorted or has repeated lines"

    def test_unreadable_file_is_reported(self, tmp_path: Path) -> None:
        got = ptm.check_names_file(tmp_path / "absent.txt", UPSTREAM)
        assert len(got) == 1
        assert "cannot read" in got[0]


class TestParseMatrix:
    def test_rows_are_keyed_by_group_and_escaped_pipes_unescaped(self) -> None:
        text = _matrix(
            "| A1 | `test_one` | `TestOne` (`a=x\\|y`) | ported |",
        )
        rows = _rows(text)
        assert len(rows) == 1
        assert rows[0].key == "tests/test_a.py::test_one"
        assert rows[0].go_cell == "`TestOne` (`a=x|y`)"
        assert rows[0].status == "ported"

    def test_class_method_rows_use_the_pytest_node_name(self) -> None:
        rows = _rows(
            _matrix("| A1 | `TestGroup::TestInner::test_x` | `TestX` | ported |")
        )
        assert rows[0].key == "tests/test_a.py::TestGroup::TestInner::test_x"

    def test_heading_count_may_carry_a_note_and_alignment_colons(self) -> None:
        text = (
            "### `tests/test_a.py` (1, skipped outside the dev repository)\n\n"
            "| ID | Upstream | Go test / deviation | status |\n"
            "| :--- | :---: | ---: | ---- |\n"
            "| A1 | `test_one` | `TestOne` | ported |\n"
        )
        assert [row.row_id for row in _rows(text)] == ["A1"]

    def test_tables_before_the_first_group_are_ignored(self) -> None:
        text = "## Status values\n\n| Status | Meaning |\n| --- | --- |\n| `x` | y |\n"
        assert _rows(text) == []

    def test_rows_after_a_non_group_heading_fail(self) -> None:
        text = _matrix("| A1 | `test_one` | `TestOne` | ported |")
        text += "## Other\n\n| Note | text |\n| --- | --- |\n"
        matrix = ptm.parse_matrix(text, "m.md")
        assert [row.row_id for row in matrix.rows] == ["A1"]
        assert matrix.failures == [
            "m.md:8: table row outside a file group",
            "m.md:9: table row outside a file group",
        ]

    @pytest.mark.parametrize(
        ("row", "want"),
        [
            ("| A1 | `test_one` | `TestOne` |", "expected 4 cells, found 3"),
            ("| A1 | `test_one` | `TestOne` | ported", "expected 4 cells, found 0"),
            ("| A1 | test_one | `TestOne` | ported |", "is not one backtick-quoted"),
            ("| A1 | `helper` | `TestOne` | ported |", "is not one backtick-quoted"),
            ("|  | `test_one` | `TestOne` | ported |", "ID cell '' is blank"),
            ("| - | `test_one` | `TestOne` | ported |", "ID cell '-' is blank"),
            ("| :-: | `test_one` | `TestOne` | ported |", "ID cell ':-:' is blank"),
            ("| --- | `test_one` | `TestOne` | ported |", "ID cell '---' is blank"),
        ],
        ids=[
            "three cells",
            "no closing pipe",
            "unquoted name",
            "not a test name",
            "blank ID",
            "dash ID",
            "colon-dash ID",
            "separator-like ID with a real row",
        ],
    )
    def test_malformed_rows_fail(self, row: str, want: str) -> None:
        matrix = ptm.parse_matrix(_matrix(row), "m.md")
        assert matrix.rows == []
        assert len(matrix.failures) == 1
        assert matrix.failures[0].startswith("m.md:5: ")
        assert want in matrix.failures[0]

    def test_a_stray_separator_among_the_rows_is_a_malformed_row(self) -> None:
        # GitHub renders it as a row of dashes, so it is not skipped.
        text = _matrix(PORTED_A[0], "| --- | --- | --- | --- |", PORTED_A[1])
        matrix = ptm.parse_matrix(text, "m.md")
        assert [row.row_id for row in matrix.rows] == ["A1", "A2"]
        assert matrix.failures == ["m.md:6: ID cell '---' is blank or only '-' and ':'"]

    HEADER_FAILURE = (
        "the tests/test_a.py table must start with "
        "| ID | Upstream | Go test / deviation | status |"
    )

    @pytest.mark.parametrize(
        "header",
        [
            "| ID | Upstream | Go test | status |",
            "| ID | Upstream | Go test / deviation |",
            "| id | upstream | go test / deviation | status |",
            "| A0 | `test_one` | `TestOne` | ported |",
        ],
        ids=["old column name", "three cells", "case differs", "a data row"],
    )
    def test_the_header_row_is_fixed(self, header: str) -> None:
        text = (
            "### `tests/test_a.py` (1)\n\n"
            f"{header}\n| --- | --- | --- | --- |\n{PORTED_A[0]}\n"
        )
        matrix = ptm.parse_matrix(text, "m.md")
        assert matrix.failures == [f"m.md:3: {self.HEADER_FAILURE}"]
        assert [row.row_id for row in matrix.rows] == ["A1"]

    def test_the_separator_needs_four_cells(self) -> None:
        text = (
            "### `tests/test_a.py` (1)\n\n"
            "| ID | Upstream | Go test / deviation | status |\n"
            "| --- | --- | --- |\n"
            "| A1 | `test_one` | `TestOne` | ported |\n"
        )
        assert ptm.parse_matrix(text, "m.md").failures == [
            "m.md:4: expected 4 cells, found 3"
        ]

    def test_a_table_without_header_and_separator_fails(self) -> None:
        # GitHub renders these lines as a paragraph, not a table.
        text = "### `tests/test_a.py` (2)\n\n" + "\n".join(PORTED_A) + "\n"
        matrix = ptm.parse_matrix(text, "m.md")
        assert matrix.failures == [
            f"m.md:3: {self.HEADER_FAILURE}",
            (
                "m.md:4: the header row of the tests/test_a.py table is not "
                "followed by a separator row"
            ),
            "m.md:1: heading says 2 rows, the group has 1",
        ]
        assert [row.row_id for row in matrix.rows] == ["A2"]

    def test_a_header_without_a_separator_fails(self) -> None:
        text = (
            "### `tests/test_a.py` (1)\n\n"
            "| ID | Upstream | Go test / deviation | status |\n"
            f"{PORTED_A[0]}\n"
        )
        matrix = ptm.parse_matrix(text, "m.md")
        assert matrix.failures == [
            (
                "m.md:4: the header row of the tests/test_a.py table is not "
                "followed by a separator row"
            )
        ]
        assert [row.row_id for row in matrix.rows] == ["A1"]

    @pytest.mark.parametrize(
        "second_header",
        [
            f"{HEADER}\n",
            "| A9 | `test_two` | `TestNope` | ported |\n| --- | --- | --- | --- |\n",
        ],
        ids=["P8 second table with a header", "P9 data row heading a table"],
    )
    def test_a_second_table_in_a_group_fails_and_is_not_read(
        self, second_header: str
    ) -> None:
        text = (
            _matrix(PORTED_A[0], count=1)
            + "\n"
            + second_header
            + "| A2 | `test_two` | `TestTwo` | ported |\n"
        )
        matrix = ptm.parse_matrix(text, "m.md")
        assert matrix.failures == [
            (
                "m.md:7: a second table in the tests/test_a.py group; a group "
                "holds exactly one"
            )
        ]
        assert [row.row_id for row in matrix.rows] == ["A1"]

    def test_a_group_without_a_table_fails(self) -> None:
        text = "### `tests/test_a.py` (0)\n\nNo tests.\n"
        assert ptm.parse_matrix(text, "m.md").failures == [
            "m.md:1: the tests/test_a.py group has no table"
        ]

    @pytest.mark.parametrize("spaces", [1, 2, 3])
    def test_rows_indented_up_to_three_spaces_are_rows(self, spaces: int) -> None:
        # GitHub renders them as table rows, so they are checked.
        indented = " " * spaces + "| A9 | `test_two` | `TestNope` | ported |"
        text = _matrix(PORTED_A[0], indented)
        assert [row.row_id for row in _rows(text)] == ["A1", "A9"]

    def test_a_line_indented_four_spaces_is_a_code_block(self) -> None:
        text = _matrix(PORTED_A[0], "    | A2 | `test_two` | `TestTwo` | ported |")
        matrix = ptm.parse_matrix(text, "m.md")
        assert [row.row_id for row in matrix.rows] == ["A1"]
        assert matrix.failures == ["m.md:1: heading says 2 rows, the group has 1"]

    @pytest.mark.parametrize("level", ["#", "##", "####", "######"])
    def test_file_headings_must_be_level_three(self, level: str) -> None:
        # A group heading at another level would leave its rows outside
        # any group, or before the first one.
        text = f"{level} `tests/test_a.py` (1)\n\n{HEADER}\n{PORTED_A[0]}\n"
        matrix = ptm.parse_matrix(text, "m.md")
        assert matrix.rows == []
        assert matrix.failures == [
            "m.md:1: a group heading is level 3: ### `tests/<file>` (<count>)"
        ]

    def test_heading_count_must_match_the_rows(self) -> None:
        text = _matrix(*PORTED_A, count=3) + _matrix(PORTED_B, file="tests/test_b.py")
        assert ptm.parse_matrix(text, "m.md").failures == [
            "m.md:1: heading says 3 rows, the group has 2"
        ]

    def test_last_group_count_is_checked_at_end_of_input(self) -> None:
        text = _matrix(PORTED_B, file="tests/test_b.py", count=0)
        assert ptm.parse_matrix(text, "m.md").failures == [
            "m.md:1: heading says 0 rows, the group has 1"
        ]

    def test_heading_without_count_fails_but_keeps_its_rows(self) -> None:
        text = "### `tests/test_a.py`\n\n" + HEADER + "\n" + PORTED_A[0] + "\n"
        matrix = ptm.parse_matrix(text, "m.md")
        assert matrix.failures == ["m.md:1: group heading does not end in (<count>)"]
        assert [row.key for row in matrix.rows] == ["tests/test_a.py::test_one"]

    def test_repeated_group_heading_fails(self) -> None:
        text = _matrix(PORTED_A[0]) + _matrix(PORTED_A[1])
        matrix = ptm.parse_matrix(text, "m.md")
        assert matrix.failures == [
            "m.md:6: repeats the tests/test_a.py heading of line 1"
        ]
        assert [row.row_id for row in matrix.rows] == ["A1", "A2"]


class TestParseGoList:
    def test_names_are_attributed_to_the_package_printed_after_them(self) -> None:
        out = textwrap.dedent(
            """\
            BenchmarkNoop
            TestOne
            ok  \texample.com/m\t0.006s
            ?   \texample.com/m/internal/wire\t[no test files]
            TestLive
            ExampleClient
            ok  \texample.com/m/livetest\t0.004s
            ok  \texample.com/m/internal/empty\t0.001s
            """
        )
        assert ptm.parse_go_list(out) == {
            "example.com/m": {"BenchmarkNoop", "TestOne"},
            "example.com/m/livetest": {"TestLive", "ExampleClient"},
            "example.com/m/internal/empty": set(),
        }


def _fake_run(
    *, returncode: int = 0, stdout: str = "", stderr: str = "", raises: bool = False
) -> Callable[..., subprocess.CompletedProcess[str]]:
    """Return a ``subprocess.run`` stand-in that records nothing but its reply."""

    def run(cmd: list[str], **_: Any) -> subprocess.CompletedProcess[str]:
        if raises:
            raise FileNotFoundError(2, "No such file or directory", cmd[0])
        return subprocess.CompletedProcess(cmd, returncode, stdout, stderr)

    return run


class TestListGoTests:
    CMD = "go test -list .* -tags live ./..."

    def test_success_is_parsed(
        self, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        calls: list[tuple[list[str], Path]] = []

        def run(cmd: list[str], **kwargs: Any) -> subprocess.CompletedProcess[str]:
            calls.append((cmd, kwargs["cwd"]))
            return subprocess.CompletedProcess(
                cmd, 0, "TestOne\nok  \tex.com/m\t0.1s\n"
            )

        monkeypatch.setattr(ptm.subprocess, "run", run)
        assert ptm.list_go_tests(tmp_path) == ({"ex.com/m": {"TestOne"}}, [])
        assert calls == [
            (["go", "test", "-list", ".*", "-tags", "live", "./..."], tmp_path)
        ]

    def test_non_zero_exit_reports_the_tail_of_stderr(
        self, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        stderr = "\n".join(f"line {n}" for n in range(25)) + "\n"
        monkeypatch.setattr(
            ptm.subprocess,
            "run",
            _fake_run(returncode=1, stdout="TestX\n", stderr=stderr),
        )
        listed, failures = ptm.list_go_tests(tmp_path)
        assert listed == {}
        assert failures == [
            f"{self.CMD} exited 1",
            *(f"  line {n}" for n in range(5, 25)),
        ]

    def test_non_zero_exit_without_stderr_reports_stdout(
        self, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        monkeypatch.setattr(
            ptm.subprocess, "run", _fake_run(returncode=2, stdout="FAIL\tex.com/m\n")
        )
        assert ptm.list_go_tests(tmp_path) == (
            {},
            [f"{self.CMD} exited 2", "  FAIL\tex.com/m"],
        )

    def test_missing_go_is_reported(
        self, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        monkeypatch.setattr(ptm.subprocess, "run", _fake_run(raises=True))
        listed, failures = ptm.list_go_tests(tmp_path)
        assert listed == {}
        assert len(failures) == 1
        assert failures[0].startswith(f"cannot run {self.CMD}: ")


class TestCheckRows:
    def _check(self, text: str) -> list[str]:
        got: list[str] = ptm.check_rows(_rows(text), UPSTREAM, LISTED)
        return got

    def test_no_planned_fails_every_planned_row(self) -> None:
        got = self._check(_full(PLANNED_A, PLANNED_B))
        assert len(got) == 3
        assert all(
            line.endswith("unknown status 'planned'; want one of deviation, ported")
            for line in got
        )

    def test_missing_unknown_and_repeated_rows(self) -> None:
        text = _full(
            (
                "| A1 | `test_one` | `TestOne` | ported |",
                "| A1 | `test_one` | `TestOne` | ported |",
                "| A3 | `test_gone` | `TestTwo` | ported |",
            ),
            PORTED_B,
        )
        got = self._check(text)
        assert "row A1 (line 6) repeats the ID of line 5" in got
        assert "row A1 (line 6) repeats tests/test_a.py::test_one (row A1)" in got
        assert (
            "row A3 (line 7): tests/test_a.py::test_gone is not an upstream test "
            "at the pinned commit"
        ) in got
        assert "upstream test tests/test_a.py::test_two has no matrix row" in got
        assert len(got) == 4

    def test_unknown_status_fails(self) -> None:
        text = _full((PORTED_A[0], "| A2 | `test_two` | `TestTwo` | done |"), PORTED_B)
        assert self._check(text) == [
            (
                "row A2 (tests/test_a.py::test_two): unknown status 'done'; "
                "want one of deviation, ported"
            )
        ]

    @pytest.mark.parametrize(
        ("cell", "want"),
        [
            ("`TestTwo` (3 cases)", []),
            ("`TestOne` and `TestTwo`", []),
            ("`livetest.TestLive`", []),
            (
                "`TestMissing`",
                [
                    (
                        "row A2 (tests/test_a.py::test_two): TestMissing is not listed "
                        "by go test -list '.*' -tags live ./..."
                    )
                ],
            ),
            (
                "`codec.TestLive`",
                [
                    (
                        "row A2 (tests/test_a.py::test_two): codec.TestLive is not "
                        "listed by go test -list '.*' -tags live ./..."
                    )
                ],
            ),
            (
                "`BenchmarkNoop` only",
                [
                    (
                        "row A2 (tests/test_a.py::test_two) is ported but names no "
                        "backtick-quoted Test identifier"
                    )
                ],
            ),
        ],
        ids=[
            "listed",
            "two listed",
            "qualified in its package",
            "not listed",
            "qualified in the wrong package",
            "no Test identifier",
        ],
    )
    def test_ported_rows(self, cell: str, want: list[str]) -> None:
        text = _full((PORTED_A[0], f"| A2 | `test_two` | {cell} | ported |"), PORTED_B)
        assert self._check(text) == want

    @pytest.mark.parametrize(
        ("cell", "want"),
        [
            ("`TestRoot`", []),
            (
                "`typesafe.TestRoot`",
                [
                    (
                        "row A2 (tests/test_a.py::test_two): typesafe.TestRoot is not "
                        "listed by go test -list '.*' -tags live ./..."
                    )
                ],
            ),
        ],
        ids=["unqualified", "qualified by the package name"],
    )
    def test_root_package_tests_are_written_unqualified(
        self, cell: str, want: list[str]
    ) -> None:
        # The root package is named typesafe, but its import path ends in
        # /typesafe-sdk-go, so only the unqualified name can match it.
        listed = {
            "github.com/zchee/typesafe-sdk-go": {"TestRoot", "TestOne", "TestThree"}
        }
        text = _full((PORTED_A[0], f"| A2 | `test_two` | {cell} | ported |"), PORTED_B)
        assert ptm.check_rows(_rows(text), UPSTREAM, listed) == want

    @pytest.mark.parametrize(
        ("a2_cell", "b1_cell", "want"),
        [
            ('deviation "one deadline per attempt"', "`TestThree`", []),
            ('partial deviation "x" + `TestTwo`', "`TestThree`", []),
            ('deviation "" then deviation "second"', "`TestThree`", []),
            (
                'deviation "x" + `TestAbsent`',
                "`TestThree`",
                [
                    (
                        "row A2 (tests/test_a.py::test_two): TestAbsent is not listed "
                        "by go test -list '.*' -tags live ./..."
                    )
                ],
            ),
            (
                "deviation + `TestTwo`",
                "`TestThree`",
                [
                    (
                        "row A2 (tests/test_a.py::test_two) is a deviation without an "
                        'Appendix B citation (deviation "<reference>")'
                    )
                ],
            ),
            (
                "deviation, see B12",
                "`TestThree`",
                [
                    (
                        "row A2 (tests/test_a.py::test_two) is a deviation without an "
                        'Appendix B citation (deviation "<reference>")'
                    )
                ],
            ),
            (
                'deviation "  "',
                "`TestThree`",
                [
                    (
                        "row A2 (tests/test_a.py::test_two) is a deviation without an "
                        'Appendix B citation (deviation "<reference>")'
                    )
                ],
            ),
        ],
        ids=[
            "quoted reference",
            "partial with listed test",
            "first reference blank, second one counts",
            "partial with missing test",
            "no citation",
            "B row form is no longer a citation",
            "blank quoted reference",
        ],
    )
    def test_deviation_rows(self, a2_cell: str, b1_cell: str, want: list[str]) -> None:
        text = _full(
            (PORTED_A[0], f"| A2 | `test_two` | {a2_cell} | deviation |"),
            f"| B1 | `test_three` | {b1_cell} | ported |",
        )
        assert self._check(text) == want

    @pytest.mark.parametrize(
        ("cell", "want"),
        [
            ("`TestTwo` and `internal/codec.TestTwo`", ["`internal/codec.TestTwo`"]),
            ("`TestTwo`, `./internal/codec/TestTwo`", ["`./internal/codec/TestTwo`"]),
            ("`TestTwo` (`go vet ./examples/...`, `docs/*.md`, `internal/`)", []),
        ],
        ids=[
            "P15 path-qualified next to a valid name",
            "directory path",
            "paths that name no test",
        ],
    )
    def test_path_qualified_test_names_fail(self, cell: str, want: list[str]) -> None:
        text = _full((PORTED_A[0], f"| A2 | `test_two` | {cell} | ported |"), PORTED_B)
        assert self._check(text) == [
            (
                f"row A2 (tests/test_a.py::test_two): {span} names a test by "
                "a path; write pkg.TestName or TestName"
            )
            for span in want
        ]

    def test_same_deviation_skips_rows_without_a_citation(self) -> None:
        upstream = [*UPSTREAM, "tests/test_a.py::test_zero"]
        text = _full(
            (
                '| A1 | `test_one` | deviation "not ported" | deviation |',
                PORTED_A[1],
                "| A0 | `test_zero` | same deviation | deviation |",
            ),
            PORTED_B,
        )
        got = ptm.check_rows(_rows(text), upstream, LISTED)
        assert got == []

    def test_same_deviation_inherits_within_its_group_only(self) -> None:
        inherits = _full(
            (
                '| A1 | `test_one` | deviation "not ported" | deviation |',
                "| A2 | `test_two` | same deviation | deviation |",
            ),
            PORTED_B,
        )
        assert self._check(inherits) == []

        other_group = _full(
            (
                '| A1 | `test_one` | deviation "not ported" | deviation |',
                PORTED_A[1],
            ),
            "| B1 | `test_three` | same deviation | deviation |",
        )
        assert self._check(other_group) == [
            (
                "row B1 (tests/test_b.py::test_three) is a deviation without an "
                'Appendix B citation (deviation "<reference>")'
            )
        ]


def _upstream_repo(root: Path) -> Path:
    """Commit a three-test upstream checkout under ``root``; return its path."""
    upstream = root / "upstream"
    (upstream / "tests").mkdir(parents=True)
    (upstream / "tests" / "test_a.py").write_text(
        "def test_one():\n    pass\n\ndef test_two():\n    pass\n"
    )
    (upstream / "tests" / "test_b.py").write_text("def test_three():\n    pass\n")
    (upstream / "README.md").write_text("upstream\n")
    git(upstream, "init", "-q")
    git(upstream, "add", ".")
    git(upstream, "commit", "-q", "-m", "fixture")
    return upstream


class TestPin:
    def test_clean_checkout_has_no_changes(self, tmp_path: Path) -> None:
        upstream = _upstream_repo(tmp_path)
        assert len(ptm.upstream_head(upstream)) == 40
        assert ptm.upstream_changes(upstream) == []

    def test_edits_outside_tests_are_ignored(self, tmp_path: Path) -> None:
        upstream = _upstream_repo(tmp_path)
        (upstream / "README.md").write_text("edited\n")
        (upstream / "scratch.txt").write_text("untracked\n")
        assert ptm.upstream_changes(upstream) == []

    def test_modified_deleted_and_untracked_test_files_are_changes(
        self, tmp_path: Path
    ) -> None:
        upstream = _upstream_repo(tmp_path)
        (upstream / "tests" / "test_a.py").write_text("def test_one():\n    pass\n")
        (upstream / "tests" / "test_b.py").unlink()
        (upstream / "tests" / "test_new.py").write_text("def test_new():\n    pass\n")
        assert ptm.upstream_changes(upstream) == [
            " M tests/test_a.py",
            " D tests/test_b.py",
            "?? tests/test_new.py",
        ]

    def test_git_that_cannot_run_is_a_runtime_error(
        self, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        monkeypatch.setattr(ptm.subprocess, "run", _fake_run(raises=True))
        with pytest.raises(RuntimeError, match="cannot run git"):
            ptm.upstream_head(tmp_path)

    def test_git_failure_without_stderr_names_the_exit_status(
        self, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        monkeypatch.setattr(ptm.subprocess, "run", _fake_run(returncode=128))
        with pytest.raises(RuntimeError, match="^git exited 128$"):
            ptm.upstream_changes(tmp_path)


def _fake_go(
    monkeypatch: pytest.MonkeyPatch,
    fake: Callable[..., subprocess.CompletedProcess[str]],
) -> None:
    """Answer ``go`` commands with ``fake``; git keeps running for real."""
    real = subprocess.run

    def run(cmd: list[str], **kwargs: Any) -> subprocess.CompletedProcess[str]:
        if cmd[0] == "go":
            return fake(cmd, **kwargs)
        result: subprocess.CompletedProcess[str] = real(cmd, **kwargs)
        return result

    monkeypatch.setattr(ptm.subprocess, "run", run)


GO_LIST_OK = "TestOne\nTestTwo\nTestThree\nok  \texample.com/m\t0.1s\n"
STATUS_LINE = "\nRows by status: 0 deviation, 3 ported.\n"


@pytest.fixture
def upstream(tmp_path: Path) -> Path:
    """Return a committed three-test upstream checkout."""
    return _upstream_repo(tmp_path)


@pytest.fixture
def pinned(monkeypatch: pytest.MonkeyPatch) -> None:
    """Pretend the fixture checkout sits at the pinned commit."""
    monkeypatch.setattr(ptm, "upstream_head", lambda _: ptm.PINNED_COMMIT)
    monkeypatch.setattr(ptm, "EXPECTED_TEST_COUNT", len(UPSTREAM))
    _fake_go(monkeypatch, _fake_run(stdout=GO_LIST_OK))


def _files(tmp_path: Path, *, matrix: str | None = None) -> list[str]:
    """Write the name list and the matrix; return the flags that name them."""
    names = tmp_path / "names.txt"
    names.write_text("\n".join(UPSTREAM) + "\n")
    path = tmp_path / "matrix.md"
    path.write_text(
        _full(PORTED_A, PORTED_B) + STATUS_LINE if matrix is None else matrix
    )
    return ["--names", str(names), "--matrix", str(path), "--repo", str(tmp_path)]


class TestMain:
    def test_unpinned_checkout_stops_before_any_other_check(
        self,
        upstream: Path,
        tmp_path: Path,
        capsys: pytest.CaptureFixture[str],
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        code = ptm.main(
            ["--upstream", str(upstream), "--names", str(tmp_path / "absent.txt")]
        )

        assert code == 1
        assert capsys.readouterr().out == ""
        assert len(caplog.messages) == 1
        assert caplog.records[0].levelno == logging.ERROR
        assert caplog.messages[0].startswith(f"{upstream}: HEAD is ")
        assert caplog.messages[0].endswith(f", want {ptm.PINNED_COMMIT}")

    def test_non_checkout_fails(
        self, tmp_path: Path, caplog: pytest.LogCaptureFixture
    ) -> None:
        code = ptm.main(["--upstream", str(tmp_path / "absent")])
        assert code == 1
        assert len(caplog.messages) == 1
        assert "not a git checkout" in caplog.messages[0]

    def test_dirty_tests_directory_stops_the_run(
        self,
        upstream: Path,
        tmp_path: Path,
        monkeypatch: pytest.MonkeyPatch,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        # Real git for the status check; only the commit hash is pretended.
        monkeypatch.setattr(ptm, "upstream_head", lambda _: ptm.PINNED_COMMIT)
        (upstream / "tests" / "test_b.py").write_text("def test_other():\n    pass\n")

        code = ptm.main(["--upstream", str(upstream), *_files(tmp_path)])

        assert code == 1
        assert caplog.messages == [
            f"{upstream}: tests/ has local changes against {ptm.PINNED_COMMIT}:",
            "   M tests/test_b.py",
        ]

    @pytest.mark.usefixtures("pinned")
    def test_clean_run_prints_one_summary_line(
        self,
        upstream: Path,
        tmp_path: Path,
        capsys: pytest.CaptureFixture[str],
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        code = ptm.main(["--upstream", str(upstream), *_files(tmp_path)])

        assert code == 0
        assert caplog.messages == []
        assert capsys.readouterr().out == (
            "port-test-matrix: OK, 3 upstream tests (0 deviation, 3 ported)\n"
        )

    @pytest.mark.usefixtures("pinned")
    def test_count_mismatch_fails(
        self,
        upstream: Path,
        tmp_path: Path,
        monkeypatch: pytest.MonkeyPatch,
        capsys: pytest.CaptureFixture[str],
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        monkeypatch.setattr(ptm, "EXPECTED_TEST_COUNT", 129)

        code = ptm.main(["--upstream", str(upstream), *_files(tmp_path)])

        assert code == 1
        assert capsys.readouterr().out == ""
        assert caplog.messages == [
            f"{upstream}: found 3 upstream tests, want 129",
            "port-test-matrix: 1 failure(s)",
        ]

    @pytest.mark.usefixtures("pinned")
    def test_write_creates_the_name_list_instead_of_checking_it(
        self, upstream: Path, tmp_path: Path, capsys: pytest.CaptureFixture[str]
    ) -> None:
        out = tmp_path / "new" / "dir" / "names.txt"
        args = _files(tmp_path)
        args[1] = str(tmp_path / "absent.txt")  # --names is ignored with --write

        code = ptm.main(["--upstream", str(upstream), "--write", str(out), *args])

        assert code == 0
        assert out.read_text(encoding="utf-8") == "\n".join(UPSTREAM) + "\n"
        assert "port-test-matrix: OK, 3 upstream tests" in capsys.readouterr().out

    @pytest.mark.usefixtures("pinned")
    def test_unreadable_matrix_and_stale_names_fail_together(
        self, upstream: Path, tmp_path: Path, caplog: pytest.LogCaptureFixture
    ) -> None:
        args = _files(tmp_path)
        (tmp_path / "names.txt").write_text("tests/test_a.py::test_one\n")
        args[3] = str(tmp_path / "absent.md")

        code = ptm.main(["--upstream", str(upstream), *args])

        assert code == 1
        messages = caplog.messages
        assert f"{tmp_path / 'absent.md'}: cannot read (No such file or directory)" in (
            messages
        )
        assert "upstream test tests/test_b.py::test_three has no matrix row" in messages
        assert (
            f"{tmp_path / 'names.txt'}: missing upstream test tests/test_a.py::test_two"
            in messages
        )
        assert messages[-1] == f"port-test-matrix: {len(messages) - 1} failure(s)"

    @pytest.mark.usefixtures("pinned")
    def test_go_list_failure_and_no_planned_are_reported(
        self,
        upstream: Path,
        tmp_path: Path,
        monkeypatch: pytest.MonkeyPatch,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        _fake_go(monkeypatch, _fake_run(returncode=1, stderr="build failed\n"))
        matrix = (
            _full(PLANNED_A, PLANNED_B) + "\nRows by status: 0 deviation, 0 ported.\n"
        )

        code = ptm.main(["--upstream", str(upstream), *_files(tmp_path, matrix=matrix)])

        assert code == 1
        assert caplog.messages[:2] == [
            "go test -list .* -tags live ./... exited 1",
            "  build failed",
        ]
        planned = [m for m in caplog.messages if "unknown status 'planned'" in m]
        assert len(planned) == 3
        # Two lines for the go failure, three for the planned rows.
        assert caplog.messages[-1] == "port-test-matrix: 5 failure(s)"

    @pytest.mark.parametrize(
        ("line", "want"),
        [
            pytest.param(
                "",
                "{m}: want one line 'Rows by status: 0 deviation, 3 ported.', found 0",
                id="no status line",
            ),
            pytest.param(
                STATUS_LINE + STATUS_LINE,
                "{m}: want one line 'Rows by status: 0 deviation, 3 ported.', found 2",
                id="two status lines",
            ),
            pytest.param(
                "\nRows by status: 1 deviation, 2 ported.\n",
                "{m}:14: the rows are 0 deviation, 3 ported; the line "
                "says 1 deviation, 2 ported",
                id="counts that differ from the rows",
            ),
        ],
    )
    @pytest.mark.usefixtures("pinned")
    def test_status_line_must_state_the_counts(
        self,
        upstream: Path,
        tmp_path: Path,
        caplog: pytest.LogCaptureFixture,
        line: str,
        want: str,
    ) -> None:
        args = _files(tmp_path, matrix=_full(PORTED_A, PORTED_B) + line)

        code = ptm.main(["--upstream", str(upstream), *args])

        assert code == 1
        assert caplog.messages == [
            want.replace("{m}", str(tmp_path / "matrix.md")),
            "port-test-matrix: 1 failure(s)",
        ]

    def test_script_logs_failures_to_stderr_only(self, tmp_path: Path) -> None:
        proc = subprocess.run(
            [sys.executable, str(SCRIPT), "--upstream", str(tmp_path / "absent")],
            capture_output=True,
            text=True,
            check=False,
        )
        assert proc.returncode == 1
        assert proc.stdout == ""
        assert proc.stderr.startswith(f"{tmp_path / 'absent'}: not a git checkout: ")

    def test_upstream_is_required(self) -> None:
        with pytest.raises(SystemExit) as exc:
            ptm.main([])
        assert exc.value.code == 2

    @pytest.mark.parametrize(
        "status",
        [pytest.param("planned", id="planned"), pytest.param("done", id="made-up")],
    )
    @pytest.mark.usefixtures("pinned")
    def test_an_unknown_status_fails_the_run_and_names_the_row(
        self,
        upstream: Path,
        tmp_path: Path,
        capsys: pytest.CaptureFixture[str],
        caplog: pytest.LogCaptureFixture,
        status: str,
    ) -> None:
        matrix = (
            _full(
                (PORTED_A[0], f"| A2 | `test_two` | `TestTwo` | {status} |"), PORTED_B
            )
            + "\nRows by status: 0 deviation, 2 ported.\n"
        )

        code = ptm.main(["--upstream", str(upstream), *_files(tmp_path, matrix=matrix)])

        assert code == 1
        assert capsys.readouterr().out == ""
        assert caplog.messages == [
            (
                f"row A2 (tests/test_a.py::test_two): unknown status '{status}'; "
                "want one of deviation, ported"
            ),
            "port-test-matrix: 1 failure(s)",
        ]


DEV_HEADER = (
    "| Key | Python SDK 0.7.1 | Go SDK | Why | Matrix rows |\n"
    "| --- | --- | --- | --- | --- |"
)


def _dev(*rows: str) -> str:
    return f"# Deviations\n\n{DEV_HEADER}\n" + "\n".join(rows) + "\n"


class TestDeviations:
    def test_rows_of_every_deviation_table_are_read_and_other_tables_skipped(
        self,
    ) -> None:
        text = (
            _dev("| a | p | g | w | A1, A2 |", "| b \\| c | p | g | w | — |")
            + "\n| x | y |\n| --- | --- |\n| 1 | 2 |\n\n"
            + _dev("| d | p | g | w | B1 |")
        )
        rows, failures = ptm.parse_deviations(text, "dev.md")
        assert failures == []
        assert [(r.key, sorted(r.rows), r.line) for r in rows] == [
            ("a", ["A1", "A2"], 5),
            ("b | c", [], 6),
            ("d", ["B1"], 16),
        ]

    @pytest.mark.parametrize(
        ("text", "want"),
        [
            pytest.param("# nothing\n", "dev.md: no deviation table", id="no-table"),
            pytest.param(
                "| Key | Python SDK 0.7.1 | Go SDK | Why | Matrix rows |\n"
                "| --- | --- | --- | --- |\n| a | p | g | w | A1 |\n",
                "dev.md:2: a deviation table's header must be followed by a "
                "separator of 5 cells",
                id="short-separator",
            ),
            pytest.param(
                _dev("| a | p | g | A1 |"),
                "dev.md:5: expected 5 cells, found 4",
                id="four-cells",
            ),
            pytest.param(
                _dev("|  | p | g | w | A1 |"),
                "dev.md:5: the key cell is blank",
                id="blank-key",
            ),
            pytest.param(
                _dev("| a | p | g | w | A1; A2 |"),
                "dev.md:5: 'a': the Matrix rows cell 'A1; A2' is not distinct",
                id="bad-separator",
            ),
            pytest.param(
                _dev("| a | p | g | w | A1, A1 |"),
                "dev.md:5: 'a': the Matrix rows cell 'A1, A1' is not distinct",
                id="repeated-id",
            ),
            pytest.param(
                _dev("| a | p | g | w | a1 |"),
                "dev.md:5: 'a': the Matrix rows cell 'a1' is not distinct",
                id="lowercase-id",
            ),
            pytest.param(
                _dev("| a | p | g | w | A1 |", "| a | p | g | w | A2 |"),
                "dev.md:6: the key 'a' repeats line 5",
                id="repeated-key",
            ),
        ],
    )
    def test_malformed_tables_fail(self, text: str, want: str) -> None:
        _, failures = ptm.parse_deviations(text, "dev.md")
        assert len(failures) == 1
        assert failures[0].startswith(want)

    @staticmethod
    def _check(matrix_rows: tuple[str, ...], dev_rows: tuple[str, ...]) -> list[str]:
        rows = _rows(_matrix(*matrix_rows))
        deviations, failures = ptm.parse_deviations(_dev(*dev_rows), "dev.md")
        assert failures == []
        result: list[str] = ptm.check_deviations(rows, deviations, "dev.md")
        return result

    def test_citations_and_keys_agree_in_both_directions(self) -> None:
        # A2 inherits A1's citations; a key no row cites lists no rows.
        rows = (
            '| A1 | `test_one` | partial deviation "a" + deviation "b" + `TestX` | deviation |',
            "| A2 | `test_two` | same deviation | deviation |",
        )
        dev = (
            "| a | p | g | w | A1, A2 |",
            "| b | p | g | w | A2, A1 |",
            "| c | p | g | w | — |",
        )
        assert self._check(rows, dev) == []

    def test_a_ported_row_citing_a_deviation_counts(self) -> None:
        rows = ('| A1 | `test_one` | `TestX` + deviation "a" (one case) | ported |',)
        assert self._check(rows, ("| a | p | g | w | — |",)) == [
            "dev.md:5: 'a' does not name the rows citing it: A1"
        ]

    @pytest.mark.parametrize(
        ("rows", "dev", "want"),
        [
            pytest.param(
                ('| A1 | `test_one` | deviation "a" | deviation |',),
                ("| b | p | g | w | — |",),
                ['rows A1 cite deviation "a", which dev.md does not list'],
                id="unlisted-citation",
            ),
            pytest.param(
                (
                    '| A1 | `test_one` | deviation "a" | deviation |',
                    "| A2 | `test_two` | same deviation | deviation |",
                ),
                ("| a | p | g | w | A1 |",),
                ["dev.md:5: 'a' does not name the rows citing it: A2"],
                id="inherited-citation-missing",
            ),
            pytest.param(
                ('| A1 | `test_one` | deviation "a" | deviation |',),
                ("| a | p | g | w | A1, B7 |",),
                ["dev.md:5: 'a' names rows that do not cite it: B7"],
                id="extra-row",
            ),
            pytest.param(
                ('| A1 | `test_one` | deviation "a" | deviation |',),
                ("| a | p | g | w | A1 |", "| b | p | g | w | A1 |"),
                ["dev.md:6: 'b' names rows that do not cite it: A1"],
                id="row-listed-under-the-wrong-key",
            ),
            pytest.param(
                ('| A1 | `test_one` | deviation "A" | deviation |',),
                ("| a | p | g | w | A1 |",),
                [
                    'rows A1 cite deviation "A", which dev.md does not list',
                    "dev.md:5: 'a' names rows that do not cite it: A1",
                ],
                id="keys-are-case-sensitive",
            ),
        ],
    )
    def test_disagreements_fail(
        self, rows: tuple[str, ...], dev: tuple[str, ...], want: list[str]
    ) -> None:
        assert self._check(rows, dev) == want

    @pytest.mark.usefixtures("pinned")
    def test_main_checks_the_table_given_with_deviations(
        self,
        upstream: Path,
        tmp_path: Path,
        capsys: pytest.CaptureFixture[str],
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        matrix = (
            _full(
                (
                    '| A1 | `test_one` | deviation "a" | deviation |',
                    "| A2 | `test_two` | `TestTwo` | ported |",
                ),
                "| B1 | `test_three` | `TestThree` | ported |",
            )
            + "\nRows by status: 1 deviation, 2 ported.\n"
        )
        dev = tmp_path / "deviations.md"
        files = _files(tmp_path, matrix=matrix)

        dev.write_text(_dev("| a | p | g | w | A1 |"))
        code = ptm.main(["--upstream", str(upstream), *files, "--deviations", str(dev)])
        assert code == 0
        assert caplog.messages == []
        assert "(1 deviation, 2 ported)" in capsys.readouterr().out

        dev.write_text(_dev("| b | p | g | w | A1 |"))
        code = ptm.main(["--upstream", str(upstream), *files, "--deviations", str(dev)])
        assert code == 1
        assert caplog.messages == [
            f'rows A1 cite deviation "a", which {dev} does not list',
            f"{dev}:5: 'b' names rows that do not cite it: A1",
            "port-test-matrix: 2 failure(s)",
        ]

        caplog.clear()
        code = ptm.main(
            [
                "--upstream",
                str(upstream),
                *files,
                "--deviations",
                str(tmp_path / "no.md"),
            ]
        )
        assert code == 1
        assert caplog.messages[0].startswith(f"{tmp_path / 'no.md'}: cannot read (")


RULING_TABLE = (
    "| Ruling | Date (UTC) | Plan text amended | As built | Pinned by | Owner |\n"
    "| --- | --- | --- | --- | --- | --- |\n"
)
APPENDIX_TABLE = (
    "| # | Appendix B row (Python SDK 0.7.1) | Bold | Deviation keys | Rulings |\n"
    "| --- | --- | --- | --- | --- |\n"
)
AS_BUILT_DEV = _dev(
    "| one | p | g | w | A1 |", "| two | p | g | w | — |", "| three | p | g | w | — |"
)
GOOD_RULINGS = (
    '| R1, R1-corr | 2026-09-25 | §5 | built (deviation "two") | `TestOne` | — |',
    '| G8-a | 2026-09-26 | §8 | built (deviation "three") | `TestThree/case` | G8-a |',
    "| D-W2.2b | 2026-09-26 | process | built | process | — |",
)
GOOD_APPENDIX = (
    '| 1 | Row one | yes | deviation "one" | R1 |',
    "| 2 | Row two | no | — | — |",
)


def _as_built(rulings: tuple[str, ...], appendix: tuple[str, ...]) -> str:
    """Return a record with one phase and its ruling table (lines 7-) and one
    Appendix B table.

    With the three good rulings the Appendix B rows start at line 15.
    """
    return (
        "# As built\n\n## Phase 0: foundation\n\n"
        + RULING_TABLE
        + "\n".join(rulings)
        + "\n\n## Appendix B, row by row\n\n"
        + APPENDIX_TABLE
        + "\n".join(appendix)
        + "\n"
    )


def _deviations_of(text: str) -> list[Any]:
    deviations, failures = ptm.parse_deviations(text, "d.md")
    assert failures == []
    return list(deviations)


class TestAsBuilt:
    @pytest.mark.parametrize(
        ("cell", "want"),
        [
            pytest.param("R1", ("R1",), id="plain"),
            pytest.param(
                "R99-rev, R103-rev-corr", ("R99-rev", "R103-rev-corr"), id="suffixes"
            ),
            pytest.param("R109c-corr-2", ("R109c-corr-2",), id="letter-and-number"),
            pytest.param("G8-a, K28d, F1-b", ("G8-a", "K28d", "F1-b"), id="kinds"),
            pytest.param(
                "D-W2.2b, D-W6.5-retry-3", ("D-W2.2b", "D-W6.5-retry-3"), id="wave-ids"
            ),
        ],
    )
    def test_ruling_ids_are_split_on_commas(
        self, cell: str, want: tuple[str, ...]
    ) -> None:
        assert ptm.ruling_ids(cell, "f:1") == (want, [])

    @pytest.mark.parametrize(
        ("cell", "want"),
        [
            pytest.param("R1 R2", "'R1 R2'", id="no-comma"),
            pytest.param("r1", "'r1'", id="lower-case"),
            pytest.param("R1,", "''", id="trailing-comma"),
            pytest.param("G7 (6)", "'G7 (6)'", id="item-number"),
        ],
    )
    def test_anything_else_is_not_a_ruling_id(self, cell: str, want: str) -> None:
        _, failures = ptm.ruling_ids(cell, "f:1")
        assert failures == [f"f:1: {want} is not a ruling id"]

    @pytest.mark.parametrize(
        ("cell", "want_ids", "want"),
        [
            pytest.param(
                "R1, R1",
                ("R1",),
                ["f:1: ruling R1 is named twice in one cell"],
                id="adjacent",
            ),
            pytest.param(
                "G8-a, R2, G8-a, R2",
                ("G8-a", "R2"),
                [
                    "f:1: ruling G8-a is named twice in one cell",
                    "f:1: ruling R2 is named twice in one cell",
                ],
                id="two-repeats",
            ),
            pytest.param("R1, R1-corr", ("R1", "R1-corr"), [], id="amendment-differs"),
        ],
    )
    def test_an_id_named_twice_in_one_cell_fails(
        self, cell: str, want_ids: tuple[str, ...], want: list[str]
    ) -> None:
        assert ptm.ruling_ids(cell, "f:1") == (want_ids, want)

    def test_a_repeated_id_in_one_row_fails_the_record(self) -> None:
        rulings = ("| R1, R1-corr, R1 | d | p | b | x | o |", *GOOD_RULINGS[1:])
        record = ptm.parse_as_built(_as_built(rulings, GOOD_APPENDIX), "a.md")
        assert record.failures == ["a.md:7: ruling R1 is named twice in one cell"]

    def test_the_record_is_read(self) -> None:
        record = ptm.parse_as_built(_as_built(GOOD_RULINGS, GOOD_APPENDIX), "a.md")
        assert record.failures == []
        assert [(r.line, r.ids) for r in record.rulings] == [
            (7, ("R1", "R1-corr")),
            (8, ("G8-a",)),
            (9, ("D-W2.2b",)),
        ]
        assert [
            (a.line, a.number, a.bold, a.keys, a.rulings) for a in record.appendix
        ] == [
            (15, "1", True, ("one",), ("R1",)),
            (16, "2", False, (), ()),
        ]

    def test_other_tables_are_ignored(self) -> None:
        text = "| Column | Content |\n| --- | --- |\n| Ruling | x |\n\n" + _as_built(
            GOOD_RULINGS, GOOD_APPENDIX
        )
        record = ptm.parse_as_built(text, "a.md")
        assert record.failures == []
        assert len(record.rulings) == 3

    @pytest.mark.parametrize(
        ("rulings", "want"),
        [
            pytest.param(
                (*GOOD_RULINGS, "| R1 | d | p | b | x | o |"),
                ["a.md:10: ruling R1 already has the row at line 7"],
                id="repeated-id",
            ),
            pytest.param(
                ("| R1 | d | p | b | x |",),
                ["a.md:7: expected 6 cells, found 5"],
                id="short",
            ),
            pytest.param(
                ("| — | d | p | b | x | o |",),
                ["a.md:7: '—' is not a ruling id"],
                id="blank-id",
            ),
        ],
    )
    def test_bad_ruling_rows_fail(
        self, rulings: tuple[str, ...], want: list[str]
    ) -> None:
        record = ptm.parse_as_built(
            _as_built(rulings, ("| 1 | x | no | — | — |",)), "a.md"
        )
        assert record.failures == want

    def test_a_record_without_a_ruling_table_fails(self) -> None:
        text = "# As built\n\n" + APPENDIX_TABLE + "| 1 | x | no | — | — |\n"
        record = ptm.parse_as_built(text, "a.md")
        header = " | ".join(ptm.RULING_HEADER)
        assert record.failures == [
            f"a.md: no ruling table (a table headed | {header} |)"
        ]

    def test_a_header_without_its_separator_fails(self) -> None:
        text = RULING_TABLE.splitlines()[0] + "\n| R1 | d | p | b | x | o |\n"
        record = ptm.parse_as_built(text, "a.md")
        assert record.failures[0] == (
            "a.md:2: the header must be followed by a separator of 6 cells"
        )

    @pytest.mark.parametrize(
        ("row", "want"),
        [
            pytest.param(
                "| 1 | Row one | maybe | — | — |",
                "a.md:15: Bold is 'maybe', want yes or no",
                id="bold-value",
            ),
            pytest.param(
                "| 2 | Row one | no | — | — |",
                "a.md:15: row numbered '2', want 1",
                id="numbering",
            ),
            pytest.param(
                "| 1 | Row one | no | see above | — |",
                "a.md:15: Deviation keys is 'see above', want — or deviation "
                '"<key>" citations',
                id="keys-cell",
            ),
            pytest.param(
                "| 1 | Row one | no | — | R1 and R2 |",
                "a.md:15: 'R1 and R2' is not a ruling id",
                id="rulings-cell",
            ),
        ],
    )
    def test_bad_appendix_rows_fail(self, row: str, want: str) -> None:
        record = ptm.parse_as_built(_as_built(GOOD_RULINGS, (row,)), "a.md")
        assert record.failures == [want]

    @pytest.mark.parametrize("copies", [0, 2])
    def test_exactly_one_appendix_table(self, copies: int) -> None:
        text = "# As built\n\n" + RULING_TABLE + GOOD_RULINGS[2] + "\n"
        for _ in range(copies):
            text += "\n" + APPENDIX_TABLE + "| 1 | x | no | — | — |\n"
        record = ptm.parse_as_built(text, "a.md")
        assert record.failures[0].startswith(f"a.md: {copies} Appendix B tables")

    @staticmethod
    def _phases(*sections: str) -> str:
        """Return a record of the given phase sections and a good Appendix B.

        Each section is a heading followed by its body; the first heading is
        line 3.
        """
        return (
            "# As built\n\n"
            + "\n".join(sections)
            + "\n## Appendix B, row by row\n\n"
            + APPENDIX_TABLE
            + "\n".join(GOOD_APPENDIX)
            + "\n"
        )

    def test_phase_sections_are_read(self) -> None:
        text = self._phases(
            "## Phase 0: one\n\n" + RULING_TABLE + GOOD_RULINGS[0] + "\n",
            "## Phase 1\n\n### A sub-heading\n\n" + RULING_TABLE + GOOD_RULINGS[1],
        )
        record = ptm.parse_as_built(text, "a.md")
        assert record.failures == []
        assert record.phases == [3, 9]
        assert [r.line for r in record.rulings] == [7, 15]

    @pytest.mark.parametrize(
        ("sections", "want"),
        [
            pytest.param(
                (
                    "## Phase 0\n\n" + RULING_TABLE + GOOD_RULINGS[0] + "\n",
                    "## Phase 1\n\n"
                    + RULING_TABLE.replace("| Owner |", "| Owner answer |")
                    + GOOD_RULINGS[1]
                    + "\n",
                ),
                ["a.md:9: Phase 1 holds 0 ruling tables, want 1"],
                id="renamed-header",
            ),
            pytest.param(
                (
                    "## Phase 0\n\n" + RULING_TABLE + GOOD_RULINGS[0] + "\n",
                    "## Phase 1\n\n"
                    + RULING_TABLE.replace("| Ruling |", "| Rulings |")
                    + GOOD_RULINGS[1]
                    + "\n",
                ),
                ["a.md:9: Phase 1 holds 0 ruling tables, want 1"],
                id="renamed-first-cell",
            ),
            pytest.param(
                (
                    "## Phase 0\n\n"
                    + RULING_TABLE
                    + GOOD_RULINGS[0]
                    + "\n\n"
                    + RULING_TABLE
                    + GOOD_RULINGS[1]
                    + "\n",
                ),
                ["a.md:3: Phase 0 holds 2 ruling tables, want 1"],
                id="two-tables",
            ),
            pytest.param(
                (
                    "## Phase 0\n\n" + RULING_TABLE + GOOD_RULINGS[0] + "\n",
                    "## Phase 2\n\n" + RULING_TABLE + GOOD_RULINGS[1] + "\n",
                ),
                ["a.md:9: phase numbered '2', want 1"],
                id="numbering",
            ),
            pytest.param(
                (
                    "## Phase 0\n\n" + RULING_TABLE + GOOD_RULINGS[0] + "\n",
                    "## Phase 1\n\nNo table in this phase.\n",
                    "## Notes\n\n" + RULING_TABLE + GOOD_RULINGS[1] + "\n",
                ),
                ["a.md:9: Phase 1 holds 0 ruling tables, want 1"],
                id="table-in-a-later-section",
            ),
        ],
    )
    def test_each_phase_holds_one_ruling_table(
        self, sections: tuple[str, ...], want: list[str]
    ) -> None:
        record = ptm.parse_as_built(self._phases(*sections), "a.md")
        assert record.failures == want

    @pytest.mark.parametrize(
        ("phases", "appendix", "want"),
        [
            pytest.param(1, GOOD_APPENDIX, [], id="as-planned"),
            pytest.param(
                2,
                GOOD_APPENDIX,
                ["a.md: 1 phase sections, want 2"],
                id="phase-dropped",
            ),
            pytest.param(
                1,
                GOOD_APPENDIX[:1],
                ["a.md: 1 Appendix B rows, want 2"],
                id="plain-row-dropped",
            ),
            pytest.param(
                1,
                ("| 1 | Row two | no | — | — |",),
                [
                    "a.md: 1 Appendix B rows, want 2",
                    "a.md: 0 bold Appendix B rows, want 1",
                ],
                id="bold-row-dropped-and-renumbered",
            ),
            pytest.param(
                1,
                (GOOD_APPENDIX[0], "| 2 | Row two | yes | — | R1 |"),
                ["a.md: 2 bold Appendix B rows, want 1"],
                id="row-made-bold",
            ),
        ],
    )
    def test_the_plan_shape_is_pinned(
        self,
        monkeypatch: pytest.MonkeyPatch,
        phases: int,
        appendix: tuple[str, ...],
        want: list[str],
    ) -> None:
        monkeypatch.setattr(ptm, "PLAN_PHASES", phases)
        monkeypatch.setattr(ptm, "APPENDIX_ROWS", 2)
        monkeypatch.setattr(ptm, "APPENDIX_BOLD", 1)
        record = ptm.parse_as_built(_as_built(GOOD_RULINGS, appendix), "a.md")
        assert record.failures == []
        assert ptm.check_plan_shape(record, "a.md") == want

    def _check(
        self,
        rulings: tuple[str, ...],
        appendix: tuple[str, ...],
        dev: str = AS_BUILT_DEV,
        listed: dict[str, set[str]] | None = None,
    ) -> list[str]:
        record = ptm.parse_as_built(_as_built(rulings, appendix), "a.md")
        assert record.failures == []
        failures: list[str] = ptm.check_as_built(
            record, _deviations_of(dev), listed, "a.md", "d.md"
        )
        return failures

    def test_a_closed_set_passes(self) -> None:
        assert self._check(GOOD_RULINGS, GOOD_APPENDIX) == []

    def test_a_key_cited_nowhere_fails(self) -> None:
        dev = AS_BUILT_DEV + "| four | p | g | w | — |\n"
        assert self._check(GOOD_RULINGS, GOOD_APPENDIX, dev) == [
            "d.md:8: the key 'four' is cited by no Appendix B row and no ruling of a.md"
        ]

    def test_a_citation_that_is_no_key_fails_in_either_table(self) -> None:
        rulings = (*GOOD_RULINGS, '| R2 | d | p | built (deviation "gone") | x | o |')
        appendix = (
            '| 1 | Row one | yes | deviation "one"; deviation "lost" | R1 |',
            GOOD_APPENDIX[1],
        )
        assert self._check(rulings, appendix) == [
            'a.md:10: deviation "gone" is not a key of d.md',
            'a.md:16: deviation "lost" is not a key of d.md',
        ]

    def test_a_bold_row_needs_a_deviation_or_rulings(self) -> None:
        dev = _dev("| two | p | g | w | — |", "| three | p | g | w | — |")
        appendix = ("| 1 | Row one | yes | — | — |", GOOD_APPENDIX[1])
        assert self._check(GOOD_RULINGS, appendix, dev) == [
            (
                "a.md:15: Appendix B row 1 is bold but names neither a deviation "
                "nor the rulings that replaced it"
            )
        ]

    def test_a_bold_row_may_stand_by_rulings_alone(self) -> None:
        dev = _dev("| two | p | g | w | — |", "| three | p | g | w | — |")
        appendix = ("| 1 | Row one | yes | — | R1, G8-a |", GOOD_APPENDIX[1])
        assert self._check(GOOD_RULINGS, appendix, dev) == []

    def test_an_appendix_ruling_needs_a_row(self) -> None:
        appendix = ('| 1 | Row one | yes | deviation "one" | R404 |', GOOD_APPENDIX[1])
        assert self._check(GOOD_RULINGS, appendix) == [
            "a.md:15: ruling R404 has no row in a ruling table"
        ]

    def test_quoted_go_names_must_be_listed(self) -> None:
        rulings = (
            *GOOD_RULINGS,
            "| R2 | d | p | b | `codec.TestFour`, `wire.TestFour`, `TestGone` | o |",
        )
        listed = {
            "example.com/m": {"TestOne", "TestThree"},
            "example.com/m/internal/codec": {"TestFour"},
        }
        assert self._check(rulings, GOOD_APPENDIX, listed=listed) == [
            "a.md:10: TestFour is not listed by go test -list '.*' -tags live ./...",
            "a.md:10: TestGone is not listed by go test -list '.*' -tags live ./...",
        ]
        assert self._check(rulings, GOOD_APPENDIX, listed=None) == []

    def test_the_summary_counts_the_closed_set(self) -> None:
        record = ptm.parse_as_built(_as_built(GOOD_RULINGS, GOOD_APPENDIX), "a.md")
        assert ptm.as_built_summary(record, _deviations_of(AS_BUILT_DEV)) == (
            "as-built: 2 Appendix B rows (1 bold: 1 reach matrix rows, 0 no upstream "
            "test, 0 by ruling only), 3 keys (1 from Appendix B, 2 from rulings "
            "only), 4 rulings, 2 Go names"
        )

    @pytest.mark.usefixtures("pinned")
    def test_main_checks_the_record_given_with_as_built(
        self,
        upstream: Path,
        tmp_path: Path,
        capsys: pytest.CaptureFixture[str],
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        matrix = (
            _full(
                (
                    '| A1 | `test_one` | deviation "one" | deviation |',
                    "| A2 | `test_two` | `TestTwo` | ported |",
                ),
                "| B1 | `test_three` | `TestThree` | ported |",
            )
            + "\nRows by status: 1 deviation, 2 ported.\n"
        )
        files = _files(tmp_path, matrix=matrix)
        dev = tmp_path / "deviations.md"
        dev.write_text(AS_BUILT_DEV)
        built = tmp_path / "as-built.md"
        built.write_text(_as_built(GOOD_RULINGS, GOOD_APPENDIX))
        args = ["--upstream", str(upstream), *files, "--deviations", str(dev)]

        assert ptm.main([*args, "--as-built", str(built)]) == 0
        assert caplog.messages == []
        assert capsys.readouterr().out == (
            "port-test-matrix: OK, 3 upstream tests (1 deviation, 2 "
            "ported); as-built: 2 Appendix B rows (1 bold: 1 reach matrix rows, 0 "
            "no upstream test, 0 by ruling only), 3 keys (1 from Appendix B, 2 "
            "from rulings only), 4 rulings, 2 Go names\n"
        )

        built.write_text(_as_built(GOOD_RULINGS[1:], GOOD_APPENDIX))
        assert ptm.main([*args, "--as-built", str(built)]) == 1
        assert caplog.messages == [
            f"{built}:14: ruling R1 has no row in a ruling table",
            (
                f"{dev}:6: the key 'two' is cited by no Appendix B row and no "
                f"ruling of {built}"
            ),
            "port-test-matrix: 2 failure(s)",
        ]

        caplog.clear()
        assert ptm.main([*args, "--as-built", str(tmp_path / "no.md")]) == 1
        assert caplog.messages[0].startswith(f"{tmp_path / 'no.md'}: cannot read (")

        caplog.clear()
        no_dev = ["--upstream", str(upstream), *files, "--as-built", str(built)]
        assert ptm.main(no_dev) == 1
        assert caplog.messages == [
            "--as-built needs --deviations: check 8 compares the two",
            "port-test-matrix: 1 failure(s)",
        ]

    @pytest.fixture
    def pinned(self, pinned: None, monkeypatch: pytest.MonkeyPatch) -> None:
        """Extend the module's pinned fixture with the record's shape."""
        monkeypatch.setattr(ptm, "PLAN_PHASES", 1)
        monkeypatch.setattr(ptm, "APPENDIX_ROWS", len(GOOD_APPENDIX))
        monkeypatch.setattr(ptm, "APPENDIX_BOLD", 1)


class TestRepositoryDeviations:
    def test_the_repository_record_closes_the_set(self) -> None:
        root = SCRIPT.parents[2]
        deviations, failures = ptm.parse_deviations(
            (root / "docs" / "deviations.md").read_text(), "docs/deviations.md"
        )
        assert failures == []
        record = ptm.parse_as_built(
            (root / "docs" / "as-built.md").read_text(), "docs/as-built.md"
        )
        assert record.failures == []
        assert ptm.check_plan_shape(record, "docs/as-built.md") == []
        got = ptm.check_as_built(
            record, deviations, None, "docs/as-built.md", "docs/deviations.md"
        )
        assert got == []

    def test_the_repository_table_matches_the_matrix(self) -> None:
        root = SCRIPT.parents[2]
        rows = ptm.parse_matrix((root / "docs" / "port-test-matrix.md").read_text())
        assert rows.failures == []
        deviations, failures = ptm.parse_deviations(
            (root / "docs" / "deviations.md").read_text(), "docs/deviations.md"
        )
        assert failures == []
        assert ptm.check_deviations(rows.rows, deviations, "docs/deviations.md") == []
