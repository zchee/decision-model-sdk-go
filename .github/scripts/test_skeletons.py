"""Tests for the W6.3 skeletons uncovered-lines.py and docs-snippets.py.

Run from the repository root with ``uvx pytest -q .github/scripts``.
"""

from __future__ import annotations

import importlib.util
import subprocess
import sys
from pathlib import Path
from types import ModuleType

import pytest

HERE = Path(__file__).parent

# Script file name -> the flags of its final interface.
SKELETONS = {
    "uncovered-lines.py": [
        "--profile",
        "coverage.out",
        "--doc",
        "docs/uncovered-lines.md",
    ],
    "docs-snippets.py": [
        "--readme",
        "README.md",
        "--docs",
        "docs",
        "--examples",
        "examples",
    ],
}


def _load(name: str) -> ModuleType:
    spec = importlib.util.spec_from_file_location(
        name.removesuffix(".py").replace("-", "_"), HERE / name
    )
    assert spec is not None
    assert spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@pytest.mark.parametrize("name", sorted(SKELETONS))
def test_valid_flags_exit_2_with_the_not_implemented_message(
    name: str, caplog: pytest.LogCaptureFixture, capsys: pytest.CaptureFixture[str]
) -> None:
    module = _load(name)

    code = module.main(SKELETONS[name])

    assert code == 2
    assert caplog.messages == [
        f"{name.removesuffix('.py')}: not implemented until W6.3"
    ]
    assert capsys.readouterr().out == ""


@pytest.mark.parametrize("name", sorted(SKELETONS))
def test_missing_flags_exit_2(name: str) -> None:
    module = _load(name)
    with pytest.raises(SystemExit) as exc:
        module.main([])
    assert exc.value.code == 2


@pytest.mark.parametrize("name", sorted(SKELETONS))
def test_script_exits_2_and_writes_only_stderr(name: str) -> None:
    proc = subprocess.run(
        [sys.executable, str(HERE / name), *SKELETONS[name]],
        capture_output=True,
        text=True,
        check=False,
    )
    assert proc.returncode == 2
    assert proc.stdout == ""
    assert proc.stderr == f"{name.removesuffix('.py')}: not implemented until W6.3\n"
