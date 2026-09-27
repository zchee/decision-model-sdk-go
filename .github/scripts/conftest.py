"""Helpers shared by the tests of the checker scripts."""

from __future__ import annotations

import importlib.util
import subprocess
import sys
from pathlib import Path
from types import ModuleType

SCRIPTS = Path(__file__).parent


def load_script(filename: str) -> ModuleType:
    """Import a checker script, whose file name is not a module name.

    Args:
        filename: the script's file name in this directory, such as
            ``port-test-matrix.py``.

    Returns:
        The module, registered in ``sys.modules`` under the file name with
        its dashes as underscores (``port_test_matrix``) before it runs,
        since dataclasses resolve a module's annotations through
        ``sys.modules``.

    Raises:
        ImportError: the script cannot be loaded.
    """
    name = filename.removesuffix(".py").replace("-", "_")
    spec = importlib.util.spec_from_file_location(name, SCRIPTS / filename)
    if spec is None or spec.loader is None:
        raise ImportError(f"cannot load {SCRIPTS / filename}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


def git(repo: Path, *args: str) -> str:
    """Run git in a fixture repository and return its output.

    Args:
        repo: the repository to run git in.
        *args: the git subcommand and its arguments.

    Returns:
        The standard output without surrounding whitespace.

    Raises:
        subprocess.CalledProcessError: git exited non-zero.
    """
    return subprocess.run(
        [
            "git",
            "-C",
            str(repo),
            "-c",
            "user.name=t",
            "-c",
            "user.email=t@example.invalid",
            "-c",
            "commit.gpgsign=false",
            *args,
        ],
        capture_output=True,
        text=True,
        check=True,
    ).stdout.strip()
