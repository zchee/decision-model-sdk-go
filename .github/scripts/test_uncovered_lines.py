"""Tests for uncovered-lines.py.

Run from the repository root with ``uvx --with pyyaml pytest -q .github/scripts``.
Type-check with ``uvx --with pytest --with types-PyYAML mypy --strict
.github/scripts``.
"""

from __future__ import annotations

import importlib.util
import subprocess
import sys
import textwrap
from pathlib import Path
from types import ModuleType

import pytest

SCRIPT = Path(__file__).with_name("uncovered-lines.py")
REPO = Path(__file__).resolve().parents[2]


def _load() -> ModuleType:
    spec = importlib.util.spec_from_file_location("uncovered_lines", SCRIPT)
    assert spec is not None
    assert spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    # dataclasses resolve the module's annotations through sys.modules.
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


ul = _load()

MODULE = "example.com/m"

# Line numbers matter: the profile below points into this file.
SOURCE = textwrap.dedent(
    """\
    package m

    var hook = func() {
    \tprintln("never")
    }

    var (
    \tfirst  = 1
    \tsecond = func() {}
    )

    func Plain(err error) error {
    \tif err != nil {
    \t\treturn err
    \t}
    \treturn nil
    }

    func (c *Client) Close() error {
    \tif c == nil {
    \t\treturn errNil
    \t}
    \tif c.x {
    \t\treturn errNil
    \t}
    \treturn nil
    }

    func (s set[K]) Has(k K) bool { return s[k] }

    // é is two bytes: columns are byte offsets.
    func Wide() string { s := "é"; return s }
    """
)

# (line.col,line.col statements) of each block in SOURCE.
BLOCKS = {
    "hook": "3.19,5.2 1",
    "second": "9.19,9.20 0",
    "plain-if": "13.16,15.3 1",
    "plain-tail": "16.2,16.12 1",
    "close-nil": "20.14,22.3 1",
    "close-x": "23.10,25.3 1",
    "has": "29.31,29.46 1",
    "wide": "32.20,32.43 2",
}

CODECOV = "ignore:\n  - internal/testsupport/**\n  - examples/*\n"
TABLE = (
    "| File | Function | Code | Blocks | Reason |\n| --- | --- | --- | --- | --- |\n"
)


def _profile(counts: dict[str, int], extra: str = "") -> str:
    lines = ["mode: atomic"]
    for name, count in counts.items():
        lines.append(f"{MODULE}/a.go:{BLOCKS[name]} {count}")
    return "\n".join(lines) + "\n" + extra


def _repo(tmp_path: Path) -> Path:
    repo = tmp_path / "repo"
    repo.mkdir()
    (repo / "go.mod").write_text(f"module {MODULE}\n\ngo 1.27\n", encoding="utf-8")
    (repo / "a.go").write_text(SOURCE, encoding="utf-8")
    (repo / ".codecov.yaml").write_text(CODECOV, encoding="utf-8")
    return repo


def _run(
    tmp_path: Path,
    profile: str,
    doc: str,
    capsys: pytest.CaptureFixture[str],
    caplog: pytest.LogCaptureFixture,
) -> tuple[int, str, list[str]]:
    """Run main in-process; returns the status, stdout and the logged lines."""
    repo = _repo(tmp_path)
    (tmp_path / "coverage.out").write_text(profile, encoding="utf-8")
    (tmp_path / "doc.md").write_text(doc, encoding="utf-8")
    code = ul.main(
        [
            "--profile",
            str(tmp_path / "coverage.out"),
            "--doc",
            str(tmp_path / "doc.md"),
            "--codecov",
            str(repo / ".codecov.yaml"),
            "--repo",
            str(repo),
        ]
    )
    out = capsys.readouterr()
    return code, out.out, "\n".join(caplog.messages).splitlines()


ALL_ZERO_ROWS = (
    '| `a.go` | `var hook` | `println("never")` | 1 | never called |\n'
    "| `a.go` | `var second` | `{}` | 1 | never called |\n"
    "| `a.go` | `Plain` | `return err` | 1 | no error test |\n"
    "| `a.go` | `Plain` | `return nil` | 1 | never called |\n"
    "| `a.go` | `(*Client).Close` | `return errNil` | 2 | two nil paths |\n"
    "| `a.go` | `set.Has` | `return s[k]` | 1 | generic receiver |\n"
    '| `a.go` | `Wide` | `s := "é"; return s` | 1 | never called |\n'
)


@pytest.mark.parametrize(
    ("counts", "rows", "want_code", "want_err"),
    [
        pytest.param(
            dict.fromkeys(BLOCKS, 0),
            ALL_ZERO_ROWS,
            0,
            [],
            id="success: every zero-count block has its row",
        ),
        pytest.param(
            {"plain-if": 0, "plain-tail": 3},
            "| `a.go` | `Plain` | `return err` | 1 | no error test |\n",
            0,
            [],
            id="success: covered blocks need no row",
        ),
        pytest.param(
            {"plain-if": 1, "plain-tail": 3},
            "| `a.go` | `Plain` | `return err` | 0-1 | timing |\n",
            0,
            [],
            id="success: a range row accepts a covered block",
        ),
        pytest.param(
            {"plain-if": 0, "plain-tail": 3},
            "| `a.go` | `Plain` | `return err` | 0-1 | timing |\n",
            0,
            [],
            id="success: a range row accepts an uncovered block",
        ),
        pytest.param(
            {"plain-if": 0, "plain-tail": 3},
            "",
            1,
            [
                "unlisted: 1 zero-count block(s); add the row",
                "  | `a.go` | `Plain` | `return err` | 1 | <one-line reason> |",
                "uncovered-lines: 1 failure(s)",
            ],
            id="error: an unlisted block prints the row to paste",
        ),
        pytest.param(
            {"plain-if": 2, "plain-tail": 3},
            "| `a.go` | `Plain` | `return err` | 1 | no error test |\n",
            1,
            [
                "{doc}:4: a.go Plain 'return err': stale: covered now: delete the row",
                "uncovered-lines: 1 failure(s)",
            ],
            id="error: a row whose block is covered now is stale",
        ),
        pytest.param(
            {"plain-if": 0, "plain-tail": 3},
            "| `a.go` | `Plain` | `return err` | 1 | no error test |\n"
            "| `a.go` | `Plain` | `return errors.New(x)` | 0-1 | gone |\n",
            1,
            [
                (
                    "{doc}:5: a.go Plain 'return errors.New(x)': stale: no block has "
                    "this key (the code changed): delete or update the row"
                ),
                "uncovered-lines: 1 failure(s)",
            ],
            id="error: a range row whose code is gone is stale",
        ),
        pytest.param(
            {"close-nil": 0, "close-x": 0},
            "| `a.go` | `(*Client).Close` | `return errNil` | 1 | one nil path |\n",
            1,
            [
                (
                    "{doc}:4: a.go (*Client).Close 'return errNil': the row says 1 "
                    "block(s), the profile has 2 zero-count of 2"
                ),
                "uncovered-lines: 1 failure(s)",
            ],
            id="error: a count that differs",
        ),
        pytest.param(
            {"plain-if": 0},
            "| `a.go` | `Plain` | `return err` | 1 | one |\n"
            "| `a.go` | `Plain` | `return err` | 1 | two |\n",
            1,
            [
                "{doc}:5: a.go Plain 'return err' is listed twice (first on line 4)",
                "uncovered-lines: 1 failure(s)",
            ],
            id="error: a key listed twice",
        ),
        pytest.param(
            {"plain-if": 0},
            "| `a.go` | `Plain` | `return err` | 1 |  |\n",
            1,
            ["{doc}:4: the reason is blank", "uncovered-lines: 1 failure(s)"],
            id="error: a blank reason",
        ),
        pytest.param(
            {"plain-if": 0},
            "| `a.go` | `Plain` | `return err` | 1-1 | x |\n",
            1,
            [
                "{doc}:4: Blocks '1-1': want a count of 1 or more, or low < high",
                "uncovered-lines: 1 failure(s)",
            ],
            id="error: a range that is a count",
        ),
        pytest.param(
            {"plain-if": 0},
            "| a.go | `Plain` | `return err` | 1 | x |\n",
            1,
            [
                "{doc}:4: File, Function and Code must each be one non-blank code span",
                "uncovered-lines: 1 failure(s)",
            ],
            id="error: a cell that is not a code span",
        ),
    ],
)
def test_rows(
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    counts: dict[str, int],
    rows: str,
    want_code: int,
    want_err: list[str],
    caplog: pytest.LogCaptureFixture,
) -> None:
    code, out, err = _run(
        tmp_path, _profile(counts), "# Doc\n" + TABLE + rows, capsys, caplog
    )
    doc = str(tmp_path / "doc.md")
    assert err == [line.replace("{doc}", doc) for line in want_err]
    assert code == want_code
    if want_code == 0:
        assert out.startswith("uncovered-lines: OK, ")
    else:
        assert out == ""


def test_success_line_counts_blocks_rows_and_ranges(
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    caplog: pytest.LogCaptureFixture,
) -> None:
    counts = dict.fromkeys(BLOCKS, 0)
    rows = ALL_ZERO_ROWS.replace("| 1 | no error test |", "| 0-1 | timing |")
    code, out, err = _run(tmp_path, _profile(counts), TABLE + rows, capsys, caplog)
    assert (code, err) == (0, [])
    assert out == (
        "uncovered-lines: OK, 8 zero-count block(s) of 8 in 1 file(s); "
        "7 row(s) (1 timing range(s)), each with a reason\n"
    )


@pytest.mark.parametrize(
    ("profile", "want"),
    [
        pytest.param(
            "", "{p}:1: not a Go coverage profile (no mode: line)", id="empty file"
        ),
        pytest.param(
            "mode: atomic\n",
            "{p}: no blocks: the tests produced no coverage",
            id="mode only",
        ),
        pytest.param(
            "mode: atomic\nnot a block\n",
            "{p}:2: unparseable block line 'not a block'",
            id="garbage line",
        ),
        pytest.param(
            "mode: atomic\nexample.com/other/a.go:1.1,2.2 1 0\n",
            "{p}:2: example.com/other/a.go is outside module example.com/m",
            id="foreign module",
        ),
        pytest.param(
            f"mode: set\n{MODULE}/internal/testsupport/x/y.go:1.1,2.2 1 0\n",
            "the profile has no block outside the paths .codecov.yaml ignores",
            id="only ignored files",
        ),
        pytest.param(
            f"mode: set\n{MODULE}/missing.go:1.1,2.2 1 0\n",
            "missing.go: cannot read (No such file or directory)",
            id="source file missing",
        ),
        pytest.param(
            f"mode: set\n{MODULE}/a.go:40.1,41.2 1 0\n",
            "a.go:40.1: the block ends past the file: stale profile?",
            id="block past the end of the file",
        ),
    ],
)
def test_profile_that_did_not_do_its_job_fails(
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    profile: str,
    want: str,
    caplog: pytest.LogCaptureFixture,
) -> None:
    code, out, err = _run(tmp_path, profile, TABLE, capsys, caplog)
    assert code == 1
    assert out == ""
    assert err[0] == want.replace("{p}", str(tmp_path / "coverage.out"))


@pytest.mark.parametrize(
    ("doc", "want"),
    [
        pytest.param(
            "# no table\n",
            "{doc}: want exactly one table headed | File | Function | Code | Blocks "
            "| Reason |, found 0",
            id="no table",
        ),
        pytest.param(
            TABLE + "\n" + TABLE,
            "{doc}: want exactly one table headed | File | Function | Code | Blocks "
            "| Reason |, found 2",
            id="two tables",
        ),
        pytest.param(
            "| File | Function | Code | Blocks | Reason |\n\n",
            "{doc}:1: the header has no separator row",
            id="header without separator",
        ),
    ],
)
def test_doc_structure(
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    doc: str,
    want: str,
    caplog: pytest.LogCaptureFixture,
) -> None:
    code, _, err = _run(tmp_path, _profile({"plain-tail": 1}), doc, capsys, caplog)
    assert code == 1
    assert want.replace("{doc}", str(tmp_path / "doc.md")) in err


@pytest.mark.parametrize(
    ("glob", "path", "want"),
    [
        pytest.param(
            "internal/testsupport/**",
            "internal/testsupport/a.go",
            True,
            id="** one level",
        ),
        pytest.param(
            "internal/testsupport/**",
            "internal/testsupport/n/a.go",
            True,
            id="** nested",
        ),
        pytest.param(
            "internal/testsupport/**",
            "internal/testsupportx/a.go",
            False,
            id="** prefix only",
        ),
        pytest.param("examples/*", "examples/a.go", True, id="* one level"),
        pytest.param("examples/*", "examples/n/a.go", False, id="* does not cross /"),
        pytest.param("a?.go", "ab.go", True, id="? one character"),
        pytest.param("a.go", "abgo", False, id=". is literal"),
    ],
)
def test_glob_regex(glob: str, path: str, want: bool) -> None:
    assert ul.ignored(path, [ul.glob_regex(glob)]) is want


def test_load_ignores_reads_the_repository_codecov_file() -> None:
    globs = ul.load_ignores(REPO / ".codecov.yaml")
    assert "internal/testsupport/**" in globs


@pytest.mark.parametrize(
    ("text", "error"),
    [
        pytest.param("- a\n", TypeError, id="top level is a list"),
        pytest.param("ignore: a\n", TypeError, id="ignore is a string"),
        pytest.param("ignore:\n  - 1\n", TypeError, id="ignore holds a number"),
        pytest.param("ignore: [\n", ValueError, id="not YAML"),
    ],
)
def test_load_ignores_refuses(
    tmp_path: Path, text: str, error: type[Exception]
) -> None:
    path = tmp_path / "codecov.yaml"
    path.write_text(text, encoding="utf-8")
    with pytest.raises(error):
        ul.load_ignores(path)


@pytest.mark.parametrize(
    ("line", "want"),
    [
        pytest.param(4, "var hook", id="closure in a package-level var"),
        pytest.param(9, "var second", id="closure in a var group"),
        pytest.param(14, "Plain", id="function"),
        pytest.param(21, "(*Client).Close", id="pointer receiver"),
        pytest.param(29, "set.Has", id="generic value receiver"),
        pytest.param(1, None, id="package clause"),
    ],
)
def test_function_at(line: int, want: str | None) -> None:
    assert ul.function_at(SOURCE.splitlines(), line) == want


def test_function_at_refuses_a_type_declaration() -> None:
    lines = ["package m", "", "type T struct {", "\tf func()", "}"]
    assert ul.function_at(lines, 4) is None


@pytest.mark.parametrize(
    ("block", "want"),
    [
        pytest.param("plain-if", "return err", id="skips the opening brace line"),
        pytest.param("second", "{}", id="empty body"),
        pytest.param("has", "return s[k]", id="one-line body"),
        pytest.param(
            "wide", 's := "é"; return s', id="byte columns past a multi-byte rune"
        ),
    ],
)
def test_block_code(block: str, want: str) -> None:
    profile = ul.parse_profile(_profile({block: 0}), MODULE)
    (only,) = profile.counts
    assert ul.block_code(SOURCE.encode().splitlines(), only) == want


def test_parse_profile_keeps_the_largest_count_of_a_repeated_block() -> None:
    profile = ul.parse_profile(
        _profile({"plain-if": 0}) + f"{MODULE}/a.go:13.16,15.3 1 4\n", MODULE
    )
    assert list(profile.counts.values()) == [4]
    assert profile.failures == []


@pytest.mark.parametrize(
    "code",
    [
        pytest.param("a || b", id="pipe"),
        pytest.param("x := `raw`", id="backtick"),
        pytest.param("`", id="lone backtick"),
        pytest.param('m["k"] |= 1', id="pipe and quotes"),
    ],
)
def test_render_row_round_trips(code: str) -> None:
    key = ul.Key("a.go", "(*T).M", code)
    rows, failures = ul.parse_doc(
        TABLE + ul.render_row(key, "0-1", "a | reason") + "\n"
    )
    assert failures == []
    assert [(r.key, r.low, r.high, r.reason) for r in rows] == [
        (key, 0, 1, "a | reason")
    ]


def test_committed_doc_parses() -> None:
    rows, failures = ul.parse_doc(
        (REPO / "docs" / "uncovered-lines.md").read_text(encoding="utf-8")
    )
    assert failures == []
    assert rows
    assert len({row.key for row in rows}) == len(rows)


def test_help_works_without_docstrings() -> None:
    # python -OO strips docstrings: the description must not come from one.
    proc = subprocess.run(
        [sys.executable, "-OO", str(SCRIPT), "--help"],
        capture_output=True,
        text=True,
        check=False,
    )
    assert proc.returncode == 0, proc.stderr
    assert ul.DESCRIPTION in " ".join(proc.stdout.split())
    assert ul.__doc__ is not None
    assert ul.__doc__.splitlines()[0] == ul.DESCRIPTION


def test_script_fails_with_status_1_and_writes_only_stderr(tmp_path: Path) -> None:
    repo = _repo(tmp_path)
    (tmp_path / "coverage.out").write_text(_profile({"plain-if": 0}), encoding="utf-8")
    (tmp_path / "doc.md").write_text(TABLE, encoding="utf-8")
    proc = subprocess.run(
        [
            sys.executable,
            str(SCRIPT),
            "--profile",
            str(tmp_path / "coverage.out"),
            "--doc",
            str(tmp_path / "doc.md"),
            "--codecov",
            str(repo / ".codecov.yaml"),
            "--repo",
            str(repo),
        ],
        capture_output=True,
        text=True,
        check=False,
    )
    assert proc.returncode == 1
    assert proc.stdout == ""
    assert proc.stderr.splitlines()[0] == "unlisted: 1 zero-count block(s); add the row"
