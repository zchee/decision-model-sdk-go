"""Tests for port-test-matrix.py.

Run from the repository root with ``uvx pytest -q .github/scripts``.
"""

from __future__ import annotations

import importlib.util
import subprocess
import sys
import textwrap
from pathlib import Path
from types import ModuleType
from typing import Any

import pytest

SCRIPT = Path(__file__).with_name("port-test-matrix.py")


def _load() -> ModuleType:
    spec = importlib.util.spec_from_file_location("port_test_matrix", SCRIPT)
    assert spec is not None
    assert spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


ptm = _load()

UPSTREAM = [
    "tests/test_a.py::test_one",
    "tests/test_a.py::test_two",
    "tests/test_b.py::test_three",
]


def _matrix(*rows: str, file: str = "tests/test_a.py") -> str:
    header = (
        "| ID | Upstream | Go test / deviation | status |\n| --- | --- | --- | --- |"
    )
    return f"### `{file}` (n)\n\n{header}\n" + "\n".join(rows) + "\n"


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
LISTED = {
    "example.com/m": {"TestOne", "TestTwo", "BenchmarkNoop"},
    "example.com/m/livetests": {"TestLive"},
}


class TestDeriveUpstreamTests:
    def test_collects_sync_async_and_class_tests_only_in_test_files(
        self, tmp_path: Path
    ) -> None:
        tests = tmp_path / "tests"
        (tests / "sub").mkdir(parents=True)
        (tests / "test_a.py").write_text(
            textwrap.dedent(
                """
                def helper():
                    pass

                def test_sync():
                    pass

                async def test_async():
                    pass

                class TestGroup:
                    def test_method(self):
                        pass

                if True:
                    def test_conditional():
                        pass
                """
            )
        )
        (tests / "sub" / "test_b.py").write_text("def test_nested_dir():\n    pass\n")
        (tests / "helpers.py").write_text("def test_not_collected():\n    pass\n")
        (tests / "typing").mkdir()
        (tests / "typing" / "valid.py").write_text("def test_nope():\n    pass\n")

        got = ptm.derive_upstream_tests(tmp_path)

        assert got == [
            "tests/sub/test_b.py::test_nested_dir",
            "tests/test_a.py::TestGroup::test_method",
            "tests/test_a.py::test_async",
            "tests/test_a.py::test_conditional",
            "tests/test_a.py::test_sync",
        ]


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
            "| A1 | `test_one` | `TestOne` (`a=x\\|y`) | planned |",
        )
        rows = _rows(text)
        assert len(rows) == 1
        assert rows[0].key == "tests/test_a.py::test_one"
        assert rows[0].go_cell == "`TestOne` (`a=x|y`)"
        assert rows[0].status == "planned"

    def test_tables_outside_a_group_are_ignored(self) -> None:
        text = "## Status values\n\n| Status | Meaning |\n| --- | --- |\n| `x` | y |\n"
        assert _rows(text) == []

    def test_non_group_heading_ends_the_group(self) -> None:
        text = _matrix("| A1 | `test_one` | `TestOne` | planned |")
        text += "## Other\n\n| A2 | `test_two` | `TestTwo` | planned |\n"
        assert [row.row_id for row in _rows(text)] == ["A1"]

    @pytest.mark.parametrize(
        ("row", "want"),
        [
            ("| A1 | `test_one` | `TestOne` |", "expected 4 cells, found 3"),
            ("| A1 | test_one | `TestOne` | planned |", "is not one backtick-quoted"),
            ("| A1 | `helper` | `TestOne` | planned |", "is not one backtick-quoted"),
        ],
        ids=["three cells", "unquoted name", "not a test name"],
    )
    def test_malformed_rows_fail(self, row: str, want: str) -> None:
        matrix = ptm.parse_matrix(_matrix(row), "m.md")
        assert matrix.rows == []
        assert len(matrix.failures) == 1
        assert matrix.failures[0].startswith("m.md:5: ")
        assert want in matrix.failures[0]


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
            ok  \texample.com/m/livetests\t0.004s
            ok  \texample.com/m/internal/empty\t0.001s
            """
        )
        assert ptm.parse_go_list(out) == {
            "example.com/m": {"BenchmarkNoop", "TestOne"},
            "example.com/m/livetests": {"TestLive", "ExampleClient"},
            "example.com/m/internal/empty": set(),
        }


class TestCheckRows:
    def _check(self, text: str, *, no_planned: bool = False) -> list[str]:
        got: list[str] = ptm.check_rows(
            _rows(text), UPSTREAM, LISTED, no_planned=no_planned
        )
        return got

    def test_all_planned_passes(self) -> None:
        assert self._check(_full(PLANNED_A, PLANNED_B)) == []

    def test_no_planned_fails_every_planned_row(self) -> None:
        got = self._check(_full(PLANNED_A, PLANNED_B), no_planned=True)
        assert len(got) == 3
        assert all(line.endswith("is still planned (--no-planned)") for line in got)

    def test_missing_unknown_and_repeated_rows(self) -> None:
        text = _full(
            (
                "| A1 | `test_one` | `TestOne` | planned |",
                "| A1 | `test_one` | `TestOne` | planned |",
                "| A3 | `test_gone` | `TestGone` | planned |",
            ),
            PLANNED_B,
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
        text = _full(
            (PLANNED_A[0], "| A2 | `test_two` | `TestTwo` | done |"), PLANNED_B
        )
        assert self._check(text) == [
            (
                "row A2 (tests/test_a.py::test_two): unknown status 'done'; "
                "want one of deviation, planned, ported"
            )
        ]

    @pytest.mark.parametrize(
        ("cell", "want"),
        [
            ("`TestTwo` (3 cases)", []),
            ("`TestOne` and `TestTwo`", []),
            ("`livetests.TestLive`", []),
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
        text = _full(
            (PLANNED_A[0], f"| A2 | `test_two` | {cell} | ported |"), PLANNED_B
        )
        assert self._check(text) == want

    @pytest.mark.parametrize(
        ("a2_cell", "b1_cell", "want"),
        [
            ('deviation "one deadline per attempt"', "`TestThree`", []),
            ("deviation, see B12", "`TestThree`", []),
            ('partial deviation "x" + `TestTwo`', "`TestThree`", []),
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
                        'Appendix B citation (B<n> or deviation "<reference>")'
                    )
                ],
            ),
        ],
        ids=[
            "quoted reference",
            "B row",
            "partial with listed test",
            "partial with missing test",
            "no citation",
        ],
    )
    def test_deviation_rows(self, a2_cell: str, b1_cell: str, want: list[str]) -> None:
        text = _full(
            (PLANNED_A[0], f"| A2 | `test_two` | {a2_cell} | deviation |"),
            f"| B1 | `test_three` | {b1_cell} | planned |",
        )
        assert self._check(text) == want

    def test_same_deviation_inherits_within_its_group_only(self) -> None:
        inherits = _full(
            (
                '| A1 | `test_one` | deviation "not ported" | deviation |',
                "| A2 | `test_two` | same deviation | deviation |",
            ),
            PLANNED_B,
        )
        assert self._check(inherits) == []

        other_group = _full(
            (
                '| A1 | `test_one` | deviation "not ported" | deviation |',
                PLANNED_A[1],
            ),
            "| B1 | `test_three` | same deviation | deviation |",
        )
        assert self._check(other_group) == [
            (
                "row B1 (tests/test_b.py::test_three) is a deviation without an "
                'Appendix B citation (B<n> or deviation "<reference>")'
            )
        ]


class TestMain:
    def _git(self, repo: Path, *args: str) -> None:
        subprocess.run(
            [
                "git",
                "-C",
                str(repo),
                "-c",
                "user.name=t",
                "-c",
                "user.email=t@example.com",
                "-c",
                "commit.gpgsign=false",
                *args,
            ],
            check=True,
            capture_output=True,
        )

    def test_unpinned_checkout_stops_before_any_other_check(
        self, tmp_path: Path, capsys: pytest.CaptureFixture[str]
    ) -> None:
        upstream = tmp_path / "upstream"
        (upstream / "tests").mkdir(parents=True)
        (upstream / "tests" / "test_a.py").write_text("def test_one():\n    pass\n")
        self._git(upstream, "init", "-q")
        self._git(upstream, "add", ".")
        self._git(upstream, "commit", "-q", "-m", "fixture")

        code = ptm.main(
            ["--upstream", str(upstream), "--names", str(tmp_path / "absent.txt")]
        )

        out = capsys.readouterr().out.splitlines()
        assert code == 1
        assert len(out) == 1
        assert out[0].startswith(f"{upstream}: HEAD is ")
        assert out[0].endswith(f", want {ptm.PINNED_COMMIT}")

    def test_non_checkout_fails(
        self, tmp_path: Path, capsys: pytest.CaptureFixture[str]
    ) -> None:
        code = ptm.main(["--upstream", str(tmp_path / "absent")])
        out = capsys.readouterr().out
        assert code == 1
        assert "not a git checkout" in out

    def test_upstream_is_required(self) -> None:
        with pytest.raises(SystemExit) as exc:
            ptm.main([])
        assert exc.value.code == 2
