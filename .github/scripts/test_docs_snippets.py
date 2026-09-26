"""Tests for docs-snippets.py.

Run from the repository root with ``uvx --with pyyaml pytest -q .github/scripts``.
Type-check with ``uvx --with pytest --with types-PyYAML mypy --strict
.github/scripts``.
"""

from __future__ import annotations

import importlib.util
import subprocess
import sys
from pathlib import Path
from types import ModuleType

import pytest

SCRIPT = Path(__file__).with_name("docs-snippets.py")
REPO = Path(__file__).resolve().parents[2]


def _load() -> ModuleType:
    spec = importlib.util.spec_from_file_location("docs_snippets", SCRIPT)
    assert spec is not None
    assert spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    # dataclasses resolve the module's annotations through sys.modules.
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


ds = _load()

MAIN = 'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("hi")\n}\n'
MARKED = "<!-- example: hello/main.go -->\n```go\n" + MAIN + "```\n"


def _tree(
    tmp_path: Path,
    readme: str | None,
    docs: dict[str, str],
    examples: dict[str, str] | None,
) -> None:
    if readme is not None:
        (tmp_path / "README.md").write_text(readme, encoding="utf-8")
    for name, text in docs.items():
        path = tmp_path / "docs" / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
    for name, text in (examples or {}).items():
        path = tmp_path / "examples" / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")


def _run(
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    caplog: pytest.LogCaptureFixture,
) -> tuple[int, str, list[str]]:
    """Run main in-process from tmp_path; returns status, stdout, logged lines."""
    code = ds.main(
        [
            "--readme",
            str(tmp_path / "README.md"),
            "--docs",
            str(tmp_path / "docs"),
            "--examples",
            str(tmp_path / "examples"),
        ]
    )
    return code, capsys.readouterr().out, "\n".join(caplog.messages).splitlines()


@pytest.mark.parametrize(
    ("readme", "docs", "examples", "want"),
    [
        pytest.param(
            "# R\n\n```sh\ngo test ./...\n```\n\n```\nplain text\n```\n",
            {"a.md": "# A\n"},
            None,
            "docs-snippets: OK, 2 Markdown file(s), 0 Go block(s), 0 example "
            "source(s), each block equal to its source\n",
            id="success: nothing to show: non-Go blocks, no examples directory",
        ),
        pytest.param(
            "# R\n\n" + MARKED,
            {"a.md": "# A\n"},
            {"hello/main.go": MAIN},
            "docs-snippets: OK, 2 Markdown file(s), 1 Go block(s), 1 example "
            "source(s), each block equal to its source\n",
            id="success: a marked block equal to its source",
        ),
        pytest.param(
            "# R\n",
            {
                "sub/a.md": "- item\n\n  <!-- example: hello/main.go -->\n\n"
                "  ~~~golang title\n"
                + "".join(
                    ("  " + line if line else "") + "\n" for line in MAIN.splitlines()
                )
                + "  ~~~\n"
            },
            {"hello/main.go": MAIN, "hello/main_test.go": "package main\n"},
            "docs-snippets: OK, 2 Markdown file(s), 1 Go block(s), 1 example "
            "source(s), each block equal to its source\n",
            id="success: indented tilde fence in a nested doc, marker and blank line",
        ),
        pytest.param(
            "# R\n\n1. step\n\n    <!-- example: hello/main.go -->\n    ```go\n"
            + "".join(
                ("    " + line if line else "") + "\n" for line in MAIN.splitlines()
            )
            + "    ```\n",
            {"a.md": "# A\n"},
            {"hello/main.go": MAIN},
            "docs-snippets: OK, 2 Markdown file(s), 1 Go block(s), 1 example "
            "source(s), each block equal to its source\n",
            id="success: a four-space fence inside a list item (review C7)",
        ),
        pytest.param(
            "# R\n\n```go.mod\nmodule example.com/m\n```\n\n"
            "```go-module\nx\n```\n\n```gomod\nx\n```\n",
            {"a.md": "# A\n"},
            None,
            "docs-snippets: OK, 2 Markdown file(s), 0 Go block(s), 0 example "
            "source(s), each block equal to its source\n",
            id="success: go.mod, go-module and gomod blocks are not Go (review NIT 3)",
        ),
        pytest.param(
            "# R\n\n<!-- example: hello/main.go -->\n```Go title\n" + MAIN + "```\n",
            {"a.md": "# A\n"},
            {"hello/main.go": MAIN},
            "docs-snippets: OK, 2 Markdown file(s), 1 Go block(s), 1 example "
            "source(s), each block equal to its source\n",
            id="success: the info word is case-insensitive and may carry more words",
        ),
        pytest.param(
            "# R\n\n<!-- example: fence/main.go -->\n````go\n// ```\npackage main\n````\n",
            {"a.md": "# A\n"},
            {"fence/main.go": "// ```\npackage main\n"},
            "docs-snippets: OK, 2 Markdown file(s), 1 Go block(s), 1 example "
            "source(s), each block equal to its source\n",
            id="success: a longer fence holds a shorter one",
        ),
    ],
)
def test_success(
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    caplog: pytest.LogCaptureFixture,
    readme: str,
    docs: dict[str, str],
    examples: dict[str, str] | None,
    want: str,
) -> None:
    _tree(tmp_path, readme, docs, examples)
    code, out, err = _run(tmp_path, capsys, caplog)
    assert (code, err) == (0, [])
    assert out == want


@pytest.mark.parametrize(
    ("readme", "examples", "want"),
    [
        pytest.param(
            "# R\n\n```go\n" + MAIN + "```\n",
            {},
            "{R}:3: a Go block without a <!-- example: <path> --> marker on the "
            "line before it",
            id="error: a Go block without a marker",
        ),
        pytest.param(
            "# R\n\n- step\n\n    ```go\n    package main\n    ```\n",
            {},
            "{R}:5: a Go block without a <!-- example: <path> --> marker on the "
            "line before it",
            id="error: a four-space Go fence in a list item without a marker (review C7)",
        ),
        pytest.param(
            "<!-- example: hello/main.go -->\nprose\n",
            {"hello/main.go": MAIN},
            "{R}:1: the marker is not followed by a Go block",
            id="error: a marker followed by prose",
        ),
        pytest.param(
            "<!-- example: hello/main.go -->\n```sh\nls\n```\n",
            {"hello/main.go": MAIN},
            "{R}:1: the marker is followed by a sh block, not a Go block",
            id="error: a marker followed by a shell block",
        ),
        pytest.param(
            "<!-- example: a/main.go -->\n<!-- example: hello/main.go -->\n",
            {"hello/main.go": MAIN},
            "{R}:1: the marker is followed by another marker, not by a Go block",
            id="error: two markers in a row",
        ),
        pytest.param(
            MARKED,
            {},
            "{R}:2: the marker names {E}/hello/main.go, which does not exist",
            id="error: a marker naming a missing file",
        ),
        pytest.param(
            "<!-- example: ../README.md -->\n```go\n```\n",
            {},
            "{R}:2: the marker names ../README.md, outside {E}",
            id="error: a marker naming a path outside the examples",
        ),
        pytest.param(
            MARKED,
            {"hello/main.go": MAIN.replace("hi", "hello")},
            "{R}:2: the block differs from {E}/hello/main.go:",
            id="error: a block that differs from its source",
        ),
        pytest.param(
            MARKED,
            {"hello/main.go": MAIN.rstrip("\n")},
            "{R}:2: the block differs from {E}/hello/main.go:",
            id="error: byte for byte: a source without its final newline",
        ),
        pytest.param(
            "# R\n",
            {"hello/main.go": MAIN},
            "{E}/hello/main.go: no Markdown block shows this example source",
            id="error: an example source without its block",
        ),
        pytest.param(
            "```\npackage main\n```\n",
            {},
            "{R}:1: an untagged block that starts with a package clause: tag it go "
            "and give it an example marker",
            id="error: an untagged Go block",
        ),
        pytest.param(
            "<!-- example: hello/main.go -->\n```go\npackage main\n",
            {"hello/main.go": MAIN},
            "{R}:2: the fence is never closed",
            id="error: an unclosed fence",
        ),
    ],
)
def test_failures(
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    caplog: pytest.LogCaptureFixture,
    readme: str,
    examples: dict[str, str],
    want: str,
) -> None:
    _tree(tmp_path, readme, {"a.md": "# A\n"}, examples)
    code, out, err = _run(tmp_path, capsys, caplog)
    assert code == 1
    assert out == ""
    readme_path, examples_path = tmp_path / "README.md", tmp_path / "examples"
    assert (
        want.replace("{R}", str(readme_path)).replace("{E}", str(examples_path)) in err
    )
    assert err[-1].startswith("docs-snippets: ")


def test_a_differing_block_prints_a_unified_diff(
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    caplog: pytest.LogCaptureFixture,
) -> None:
    _tree(
        tmp_path,
        MARKED,
        {"a.md": "# A\n"},
        {"hello/main.go": MAIN.replace("hi", "hello")},
    )
    _, _, err = _run(tmp_path, capsys, caplog)
    assert '-\tfmt.Println("hello")' in err
    assert '+\tfmt.Println("hi")' in err


@pytest.mark.parametrize(
    ("readme", "docs", "examples_file", "want"),
    [
        pytest.param(
            None, {"a.md": "# A\n"}, False, "{R}: no such file", id="no README"
        ),
        pytest.param("# R\n", {}, False, "{D}: no Markdown file to scan", id="no docs"),
        pytest.param(
            "# R\n",
            {"a.md": "# A\n"},
            True,
            "{E}: not a directory",
            id="examples is a file",
        ),
    ],
)
def test_paths_that_would_check_nothing_fail(
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    caplog: pytest.LogCaptureFixture,
    readme: str | None,
    docs: dict[str, str],
    examples_file: bool,
    want: str,
) -> None:
    _tree(tmp_path, readme, docs, None)
    if examples_file:
        (tmp_path / "examples").write_text("x", encoding="utf-8")
    code, _, err = _run(tmp_path, capsys, caplog)
    assert code == 1
    subst = {
        "{R}": str(tmp_path / "README.md"),
        "{D}": str(tmp_path / "docs"),
        "{E}": str(tmp_path / "examples"),
    }
    for key, value in subst.items():
        want = want.replace(key, value)
    assert want in err


def test_repository_docs_pass() -> None:
    assert (
        ds.main(
            [
                "--readme",
                str(REPO / "README.md"),
                "--docs",
                str(REPO / "docs"),
                "--examples",
                str(REPO / "examples"),
            ]
        )
        == 0
    )


def test_help_works_without_docstrings() -> None:
    # python -OO strips docstrings: the description must not come from one.
    proc = subprocess.run(
        [sys.executable, "-OO", str(SCRIPT), "--help"],
        capture_output=True,
        text=True,
        check=False,
    )
    assert proc.returncode == 0, proc.stderr
    assert ds.DESCRIPTION in " ".join(proc.stdout.split())
    assert ds.__doc__ is not None
    assert ds.__doc__.splitlines()[0] == ds.DESCRIPTION


def test_missing_flags_exit_2() -> None:
    with pytest.raises(SystemExit) as exc:
        ds.main([])
    assert exc.value.code == 2


def test_script_fails_with_status_1_and_writes_only_stderr(tmp_path: Path) -> None:
    _tree(tmp_path, "```go\n```\n", {"a.md": "# A\n"}, None)
    proc = subprocess.run(
        [
            sys.executable,
            str(SCRIPT),
            "--readme",
            str(tmp_path / "README.md"),
            "--docs",
            str(tmp_path / "docs"),
            "--examples",
            str(tmp_path / "examples"),
        ],
        capture_output=True,
        text=True,
        check=False,
    )
    assert proc.returncode == 1
    assert proc.stdout == ""
    assert proc.stderr.splitlines()[-1] == "docs-snippets: 1 failure(s)"
