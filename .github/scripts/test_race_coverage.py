"""Tests for race-coverage.sh.

Run from the repository root with ``uvx --with pyyaml pytest -q .github/scripts``.
Type-check with ``uvx --with pytest --with types-PyYAML mypy --strict
.github/scripts``.

Every test past the usage checks runs the script on a fixture module in
``tmp_path`` that holds copies of race-coverage.sh and uncovered-lines.py
where the repository keeps them, so the real ``go test -race`` and the real
check (through ``uv run --script``) run as they do in CI. ``go`` and ``uv``
must be on PATH, as they are in the lint job.
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
from pathlib import Path

import pytest

SCRIPTS = Path(__file__).parent
SCRIPT = SCRIPTS / "race-coverage.sh"

SOURCE = """\
package m

// Sign is -1 for a negative x and 1 otherwise.
func Sign(x int) int {
\tif x < 0 {
\t\treturn -1
\t}
\treturn 1
}
"""

# The test never passes a negative number, so the block of `return -1` is
# the module's one zero-count block of three.
TEST_SOURCE = """\
package m

import "testing"

func TestSign(t *testing.T) {
\tif got := Sign(1); got != {want} {
\t\tt.Fatalf("Sign(1) = %d, want {want}", got)
\t}
}
"""

TABLE = (
    "| File | Function | Code | Blocks | Reason |\n| --- | --- | --- | --- | --- |\n"
)
ROW = "| `m.go` | `Sign` | `return -1` | 1 | Gap: no test passes a negative x |\n"

GUARD_LINE = "files named covdata in GOCACHE after the warm-up:"
TEST_OK = "race-coverage: go test -race with coverage (coverage.out): ok"
TEST_FAILED = (
    "race-coverage: go test -race with coverage (coverage.out): FAILED (exit status 1)"
)
CHECK = "race-coverage: uncovered-block check against docs/uncovered-lines.md: "


def _repo(tmp_path: Path, *, want: int = 1, row: bool = True) -> Path:
    """Write the fixture module; ``want`` 2 makes its test fail."""
    repo = tmp_path / "repo"
    scripts = repo / ".github" / "scripts"
    scripts.mkdir(parents=True)
    for name in ("race-coverage.sh", "uncovered-lines.py"):
        shutil.copy2(SCRIPTS / name, scripts / name)
    (repo / "docs").mkdir()
    (repo / "docs" / "uncovered-lines.md").write_text(
        "# Uncovered lines\n\n" + TABLE + (ROW if row else ""), encoding="utf-8"
    )
    (repo / ".codecov.yaml").write_text("ignore:\n  - examples/*\n", encoding="utf-8")
    (repo / "go.mod").write_text("module example.com/m\n\ngo 1.22\n", encoding="utf-8")
    (repo / "m.go").write_text(SOURCE, encoding="utf-8")
    (repo / "m_test.go").write_text(
        TEST_SOURCE.replace("{want}", str(want)), encoding="utf-8"
    )
    return repo


def _run(
    script: Path, *args: str, cwd: Path, github_actions: bool = False
) -> subprocess.CompletedProcess[str]:
    """Run the script with bash, as ci.yaml does, from ``cwd``.

    ``github_actions`` sets GITHUB_ACTIONS as a runner does; otherwise it is
    unset, so a test sees a workstation's output wherever pytest runs.
    """
    # The fixture is its own module, whatever workspace encloses tmp_path.
    env = {**os.environ, "GOWORK": "off"}
    env.pop("GITHUB_ACTIONS", None)
    if github_actions:
        env["GITHUB_ACTIONS"] = "true"
    proc = subprocess.run(
        ["bash", str(script), *args],
        cwd=cwd,
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    print(
        f"exit status {proc.returncode}\n--- stdout\n{proc.stdout}--- stderr\n{proc.stderr}"
    )
    return proc


def _fixture_script(repo: Path) -> Path:
    return repo / ".github" / "scripts" / "race-coverage.sh"


def _checker_ran(proc: subprocess.CompletedProcess[str]) -> bool:
    """uncovered-lines.py ends every run with a line of its own prefix."""
    lines = (proc.stdout + proc.stderr).splitlines()
    return any(line.startswith("uncovered-lines: ") for line in lines)


def _assert_guard(out: str) -> None:
    """The covdata guard lists GOCACHE on Linux only."""
    assert (GUARD_LINE in out) == sys.platform.startswith("linux")


@pytest.mark.parametrize(
    "args",
    [
        pytest.param(["bogus"], id="unknown mode"),
        pytest.param(["check", "a.out", "b.out"], id="extra argument"),
        pytest.param(["check", ""], id="empty file"),
    ],
)
def test_usage_error_exits_2(args: list[str], tmp_path: Path) -> None:
    proc = _run(SCRIPT, *args, cwd=tmp_path)
    assert proc.returncode == 2
    assert proc.stdout == ""
    assert proc.stderr.startswith("usage: ")


def test_help_exits_0(tmp_path: Path) -> None:
    proc = _run(SCRIPT, "--help", cwd=tmp_path)
    assert proc.returncode == 0
    assert proc.stdout.startswith("usage: ")
    assert proc.stderr == ""


def test_errors_are_annotations_under_github_actions(tmp_path: Path) -> None:
    proc = _run(SCRIPT, "check", "/nonexistent.out", cwd=tmp_path, github_actions=True)
    assert proc.returncode == 1
    assert (
        "::error::the uncovered-block check of /nonexistent.out against "
        "docs/uncovered-lines.md failed with exit status 1"
    ) in proc.stdout.splitlines()
    assert "race-coverage: error:" not in proc.stderr
    # The checker's own message is the line the annotation follows.
    assert any(
        line.startswith("uncovered-lines: ") and "/nonexistent.out" in line
        for line in proc.stderr.splitlines()
    )


def test_all_passes_from_outside_the_repository(tmp_path: Path) -> None:
    repo = _repo(tmp_path)
    # cwd is tmp_path, not the repository: FILE is relative to its root.
    proc = _run(_fixture_script(repo), cwd=tmp_path)
    assert proc.returncode == 0
    lines = proc.stdout.splitlines()
    assert (
        "uncovered-lines: OK, 1 zero-count block(s) of 3 in 1 file(s); "
        "1 row(s) (0 timing range(s)), each with a reason"
    ) in lines
    assert lines[-2:] == [TEST_OK, CHECK + "ok"]
    profile = (repo / "coverage.out").read_text(encoding="utf-8").splitlines()
    assert profile[0] == "mode: atomic"
    assert not (tmp_path / "coverage.out").exists()
    _assert_guard(proc.stdout)


def test_all_names_the_check_when_a_block_has_no_row(tmp_path: Path) -> None:
    repo = _repo(tmp_path, row=False)
    proc = _run(_fixture_script(repo), "all", cwd=repo)
    assert proc.returncode == 1
    assert proc.stdout.splitlines()[-2:] == [
        TEST_OK,
        CHECK + "FAILED (exit status 1)",
    ]
    err = proc.stderr.splitlines()
    assert "unlisted: 1 zero-count block(s); add the row" in err
    assert (
        "race-coverage: error: the uncovered-block check of coverage.out against "
        "docs/uncovered-lines.md failed with exit status 1"
    ) in err


def test_all_names_go_test_and_still_checks_when_a_test_fails(
    tmp_path: Path,
) -> None:
    repo = _repo(tmp_path, want=2)
    proc = _run(_fixture_script(repo), "all", cwd=repo)
    assert proc.returncode == 1
    assert "--- FAIL: TestSign" in proc.stdout
    assert proc.stdout.splitlines()[-2:] == [
        TEST_FAILED,
        CHECK + "ok, on the profile of a failed go test run",
    ]
    assert "race-coverage: error: go test failed with exit status 1" in (
        proc.stderr.splitlines()
    )
    _assert_guard(proc.stdout)


# A block of the passing run's profile, covered: checked in place of this
# run's profile, it would fail the check in a different way.
STALE = "mode: atomic\nexample.com/m/m.go:4.22,5.11 1 1\n"


def test_all_checks_this_runs_profile_after_a_build_failure(tmp_path: Path) -> None:
    repo = _repo(tmp_path)
    (repo / "m.go").write_text("package m\n\nfunc Sign(\n", encoding="utf-8")
    (repo / "coverage.out").write_text(STALE, encoding="utf-8")
    proc = _run(_fixture_script(repo), cwd=repo)
    assert proc.returncode == 1
    assert "FAIL\texample.com/m [build failed]" in proc.stdout.splitlines()
    assert proc.stdout.splitlines()[-2:] == [
        TEST_FAILED,
        CHECK + "FAILED (exit status 1), on the profile of a failed go test run",
    ]
    # go test writes the mode line alone when nothing builds.
    assert (repo / "coverage.out").read_text(encoding="utf-8") == "mode: atomic\n"
    assert "coverage.out: no blocks: the tests produced no coverage" in (
        proc.stderr.splitlines()
    )


def test_all_skips_the_check_when_no_profile_is_written(tmp_path: Path) -> None:
    repo = _repo(tmp_path)
    # go test stops on go.mod before it opens the profile.
    (repo / "go.mod").write_text("module\n", encoding="utf-8")
    (repo / "coverage.out").write_text(STALE, encoding="utf-8")
    proc = _run(_fixture_script(repo), cwd=repo)
    assert proc.returncode == 1
    assert proc.stdout.splitlines()[-2:] == [
        TEST_FAILED,
        CHECK + "not run: no profile was written",
    ]
    assert not (repo / "coverage.out").exists()
    assert not _checker_ran(proc)


@pytest.mark.parametrize("mode", ["all", "profile"])
def test_nothing_runs_when_the_old_profile_cannot_be_removed(
    mode: str, tmp_path: Path
) -> None:
    repo = _repo(tmp_path)
    # rm -f refuses a directory for every user, root included.
    stale_dir = repo / "coverage.out"
    stale_dir.mkdir()
    (stale_dir / "stale.out").write_text(STALE, encoding="utf-8")
    proc = _run(_fixture_script(repo), mode, cwd=repo)
    assert proc.returncode == 1
    # profile prints the warm-up's path before go test; all prints its
    # summary lines after both parts.
    assert proc.stdout == ""
    err = proc.stderr.splitlines()
    assert len(err) == 1
    prefix = (
        "race-coverage: error: cannot remove coverage.out, left by an earlier "
        "run, before go test writes it: "
    )
    assert err[0].startswith(prefix)
    # rm's reason follows; GNU and BSD rm word it differently, but both name
    # the file.
    assert "coverage.out" in err[0].removeprefix(prefix)
    assert not _checker_ran(proc)
    assert (stale_dir / "stale.out").read_text(encoding="utf-8") == STALE


def test_profile_then_check_as_ci_runs_them(tmp_path: Path) -> None:
    repo = _repo(tmp_path)
    script = _fixture_script(repo)
    made = _run(script, "profile", "coverage.out", cwd=repo)
    assert made.returncode == 0
    assert not _checker_ran(made)
    assert "race-coverage:" not in made.stdout + made.stderr
    _assert_guard(made.stdout)
    checked = _run(script, "check", "coverage.out", cwd=repo)
    assert checked.returncode == 0
    assert checked.stdout.startswith("uncovered-lines: OK, ")
    assert GUARD_LINE not in checked.stdout


def test_a_file_that_begins_with_a_dash_is_given_as_dot_slash(tmp_path: Path) -> None:
    repo = _repo(tmp_path)
    script = _fixture_script(repo)
    # rm, go test's -coverprofile= and the checker's --profile all take
    # ./-C for the file it names.
    made = _run(script, "profile", "./-C", cwd=tmp_path)
    assert made.returncode == 0
    assert len(_blocks(repo / "-C")) == 3
    checked = _run(script, "check", "./-C", cwd=tmp_path)
    assert checked.returncode == 0
    assert checked.stdout.startswith("uncovered-lines: OK, ")
    assert not list(tmp_path.rglob("coverage.out"))


MODULE_SOURCE = """\
package sub

// Abs is -x for a negative x and x otherwise.
func Abs(x int) int {
\tif x < 0 {
\t\treturn -x
\t}
\treturn x
}
"""

MODULE_TEST_SOURCE = """\
package sub

import "testing"

func TestAbs(t *testing.T) {
\tif got := Abs(2); got != 2 {
\t\tt.Fatalf("Abs(2) = %d, want 2", got)
\t}
}
"""


def _module(repo: Path, name: str = "sub") -> Path:
    """Write a module nested in the fixture at ``name``, as adapter/ is."""
    sub = repo / name
    sub.mkdir()
    (sub / "go.mod").write_text("module example.com/sub\n\ngo 1.22\n", encoding="utf-8")
    (sub / "s.go").write_text(MODULE_SOURCE, encoding="utf-8")
    (sub / "s_test.go").write_text(MODULE_TEST_SOURCE, encoding="utf-8")
    return sub


def _blocks(profile: Path) -> list[str]:
    """The block lines of a profile, after its mode line."""
    lines = profile.read_text(encoding="utf-8").splitlines()
    assert lines[0] == "mode: atomic"
    return lines[1:]


@pytest.mark.parametrize(
    ("absolute", "file_args"),
    [
        pytest.param(False, ["coverage.out"], id="relative DIR"),
        pytest.param(True, ["coverage.out"], id="absolute DIR"),
        pytest.param(False, [], id="default FILE"),
    ],
)
def test_profile_in_a_module_dir_writes_its_profile_there(
    absolute: bool, file_args: list[str], tmp_path: Path
) -> None:
    repo = _repo(tmp_path)
    sub = _module(repo)
    dir_arg = str(sub) if absolute else "sub"
    # cwd is tmp_path, not the repository: a relative DIR is taken from its
    # root, and FILE from DIR.
    proc = _run(
        _fixture_script(repo), "-C", dir_arg, "profile", *file_args, cwd=tmp_path
    )
    assert proc.returncode == 0
    assert any(
        line.startswith("ok  \texample.com/sub\t") for line in proc.stdout.splitlines()
    )
    assert "example.com/m" not in proc.stdout
    # Abs's three blocks, the one of `return -x` with no count; none of the
    # root module's.
    blocks = _blocks(sub / "coverage.out")
    assert len(blocks) == 3
    assert all(line.startswith("example.com/sub/s.go:") for line in blocks)
    assert sum(line.endswith(" 0") for line in blocks) == 1
    assert not (repo / "coverage.out").exists()
    assert not (tmp_path / "coverage.out").exists()
    assert not _checker_ran(proc)
    assert "race-coverage:" not in proc.stdout + proc.stderr
    _assert_guard(proc.stdout)


def test_profile_in_a_module_dir_named_dash_stays_there(tmp_path: Path) -> None:
    repo = _repo(tmp_path)
    dash = _module(repo, "-")
    (repo / "coverage.out").write_text(STALE, encoding="utf-8")
    # The caller's directory is the repository root, which the script's own
    # cd to the root leaves in $OLDPWD: a cd that took - for $OLDPWD would
    # print the root and profile the root module over its coverage.out.
    proc = _run(_fixture_script(repo), "-C", "-", "profile", "coverage.out", cwd=repo)
    assert proc.returncode == 0
    assert proc.stdout.startswith("go tool -n covdata: ")
    assert any(
        line.startswith("ok  \texample.com/sub\t") for line in proc.stdout.splitlines()
    )
    assert "example.com/m" not in proc.stdout
    blocks = _blocks(dash / "coverage.out")
    assert len(blocks) == 3
    assert all(line.startswith("example.com/sub/s.go:") for line in blocks)
    assert (repo / "coverage.out").read_text(encoding="utf-8") == STALE
    assert "race-coverage:" not in proc.stdout + proc.stderr
    _assert_guard(proc.stdout)


def test_profile_without_a_module_dir_stays_in_the_root_module(tmp_path: Path) -> None:
    repo = _repo(tmp_path)
    sub = _module(repo)
    proc = _run(_fixture_script(repo), "profile", "coverage.out", cwd=tmp_path)
    assert proc.returncode == 0
    assert "example.com/sub" not in proc.stdout
    blocks = _blocks(repo / "coverage.out")
    assert len(blocks) == 3
    assert all(line.startswith("example.com/m/m.go:") for line in blocks)
    assert not (sub / "coverage.out").exists()
    assert "race-coverage:" not in proc.stdout + proc.stderr
    _assert_guard(proc.stdout)


@pytest.mark.parametrize(
    "args",
    [
        pytest.param(["-C", "sub"], id="all by default"),
        pytest.param(["-C", "sub", "all"], id="all"),
        pytest.param(["-C", "sub", "check", "coverage.out"], id="check"),
        pytest.param(["-C"], id="no DIR"),
        pytest.param(["-C", "", "profile"], id="empty DIR"),
        pytest.param(["profile", "-C", "sub"], id="after the mode"),
        pytest.param(["-C", "sub", "-C", "sub", "profile"], id="twice"),
    ],
)
def test_module_dir_usage_error_exits_2(args: list[str], tmp_path: Path) -> None:
    # On the fixture, not the repository's script: a broken check here runs
    # the fixture's tiny module, not the repository's tests.
    repo = _repo(tmp_path)
    sub = _module(repo)
    proc = _run(_fixture_script(repo), *args, cwd=repo)
    assert proc.returncode == 2
    assert proc.stdout == ""
    assert proc.stderr.startswith("usage: ")
    assert not _checker_ran(proc)
    assert not (repo / "coverage.out").exists()
    assert not (sub / "coverage.out").exists()


@pytest.mark.parametrize(
    "args",
    [
        pytest.param(["profile", "-C"], id="-C as FILE"),
        pytest.param(["-C", "sub", "profile", "-C"], id="-C as FILE with -C sub"),
        pytest.param(["profile", "--help"], id="--help as FILE"),
        pytest.param(["check", "-C"], id="-C as FILE of check"),
        pytest.param(["-C", "sub", "profile", "-"], id="- as FILE"),
    ],
)
def test_file_that_begins_with_a_dash_is_a_usage_error(
    args: list[str], tmp_path: Path
) -> None:
    repo = _repo(tmp_path)
    sub = _module(repo)
    script = _fixture_script(repo)
    # A file of that name in both modules: taken for FILE, profile would
    # remove it and go test would write it again.
    name = args[-1]
    for module in (repo, sub):
        (module / name).write_text(STALE, encoding="utf-8")
    proc = _run(script, *args, cwd=repo)
    assert proc.returncode == 2
    assert proc.stdout == ""
    # The script's own usage text: the checker's argparse also prints a
    # line that begins with "usage: " when --profile is given an option.
    assert proc.stderr == _run(script, "--help", cwd=repo).stdout
    assert not _checker_ran(proc)
    for module in (repo, sub):
        assert (module / name).read_text(encoding="utf-8") == STALE
    assert not list(tmp_path.rglob("coverage.out"))


@pytest.mark.parametrize(
    ("dir_arg", "reason", "github_actions"),
    [
        pytest.param(
            "nope",
            "no such directory (a relative DIR is taken from the repository "
            "root, {root})",
            False,
            id="missing DIR",
        ),
        pytest.param(
            "nope",
            "no such directory (a relative DIR is taken from the repository "
            "root, {root})",
            True,
            id="missing DIR under GitHub Actions",
        ),
        pytest.param(
            "m.go",
            "no such directory (a relative DIR is taken from the repository "
            "root, {root})",
            False,
            id="DIR is a file",
        ),
        pytest.param(
            "plain",
            "the directory holds no go.mod, so it is not a module's root",
            False,
            id="DIR without go.mod",
        ),
    ],
)
def test_module_dir_runs_nothing_when_it_is_not_a_module(
    dir_arg: str, reason: str, github_actions: bool, tmp_path: Path
) -> None:
    repo = _repo(tmp_path)
    plain = repo / "plain"
    plain.mkdir()
    (plain / "p.go").write_text("package plain\n", encoding="utf-8")
    proc = _run(
        _fixture_script(repo),
        "-C",
        dir_arg,
        "profile",
        "coverage.out",
        cwd=tmp_path,
        github_actions=github_actions,
    )
    assert proc.returncode == 1
    message = f"-C {dir_arg}: " + reason.replace("{root}", str(repo))
    # profile's first line, the warm-up's path, comes before go test.
    if github_actions:
        assert proc.stdout.splitlines() == [f"::error::{message}"]
        assert proc.stderr == ""
    else:
        assert proc.stdout == ""
        assert proc.stderr.splitlines() == [f"race-coverage: error: {message}"]
    assert not list(tmp_path.rglob("coverage.out"))


def test_profile_in_a_module_dir_removes_its_old_profile(tmp_path: Path) -> None:
    repo = _repo(tmp_path)
    sub = _module(repo)
    # go test stops on go.mod before it opens the profile, so the stale file
    # is gone only if the script removed it.
    (sub / "go.mod").write_text("module\n", encoding="utf-8")
    (sub / "coverage.out").write_text(STALE, encoding="utf-8")
    (repo / "coverage.out").write_text(STALE, encoding="utf-8")
    proc = _run(
        _fixture_script(repo), "-C", "sub", "profile", "coverage.out", cwd=tmp_path
    )
    assert proc.returncode == 1
    assert proc.stdout.startswith("go tool -n covdata: ")
    assert "race-coverage: error: go test failed with exit status 1" in (
        proc.stderr.splitlines()
    )
    assert not (sub / "coverage.out").exists()
    # The root module's profile is not the one -C sub replaces.
    assert (repo / "coverage.out").read_text(encoding="utf-8") == STALE


def test_profile_in_a_module_dir_stops_when_its_old_profile_stays(
    tmp_path: Path,
) -> None:
    repo = _repo(tmp_path)
    sub = _module(repo)
    # rm -f refuses a directory for every user, root included.
    stale_dir = sub / "coverage.out"
    stale_dir.mkdir()
    (stale_dir / "stale.out").write_text(STALE, encoding="utf-8")
    proc = _run(
        _fixture_script(repo), "-C", "sub", "profile", "coverage.out", cwd=tmp_path
    )
    assert proc.returncode == 1
    assert proc.stdout == ""
    err = proc.stderr.splitlines()
    assert len(err) == 1
    prefix = (
        "race-coverage: error: cannot remove coverage.out, left by an earlier "
        "run, before go test writes it: "
    )
    assert err[0].startswith(prefix)
    assert "coverage.out" in err[0].removeprefix(prefix)
    assert (stale_dir / "stale.out").read_text(encoding="utf-8") == STALE
    assert not (repo / "coverage.out").exists()
