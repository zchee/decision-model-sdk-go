"""Tests for spikes-citations.py.

Run from the repository root with ``uvx --with pyyaml pytest -q .github/scripts``.
Type-check with ``uvx --with pytest --with types-PyYAML mypy --strict
.github/scripts``.
"""

from __future__ import annotations

from pathlib import Path

import pytest
from conftest import git, load_script

SCRIPT = Path(__file__).with_name("spikes-citations.py")
REPO = Path(__file__).resolve().parents[2]

sc = load_script(SCRIPT.name)


def _repo(root: Path, files: dict[str, str]) -> tuple[Path, str]:
    root.mkdir(parents=True, exist_ok=True)
    if not (root / ".git").exists():
        git(root, "init", "-q")
    for name, text in files.items():
        (root / name).parent.mkdir(parents=True, exist_ok=True)
        (root / name).write_text(text, encoding="utf-8")
    git(root, "add", "-A")
    git(root, "commit", "-q", "-m", "c")
    return root, git(root, "rev-parse", "HEAD")[:12]


OLD = "abcdef1234567890abcdef1234567890abcdef12"
OLD_W7 = "fedcba0987654321fedcba0987654321fedcba09"
KEPT = "0badc0de0badc0de0badc0de0badc0de0badc0de"


@pytest.fixture
def archive(tmp_path: Path) -> tuple[Path, str, str]:
    """An archive with two commits: A holds w1.2/, B adds w7/c.txt and the commit map.

    The map has a row for a commit of main, one for a commit only wave/w7 reached, and one for
    a commit that no rewritten ref carries (kept as a patch).
    """
    repo, a = _repo(
        tmp_path / "archive", {"w1.2/results/a.txt": "1\n2\n3\n", "w1.2/b.py": "x\n"}
    )
    table = (
        "sdk_old\tsdk_final\tsdk_checkout\tarchive\tpatch\trefs\tsubject\n"
        f"{OLD}\tremoved\t\t\t\tmain wave/w7\ts\n"
        f"{OLD_W7}\tremoved\t\t\t\twave/w7\tt\n"
        f"{KEPT}\tnot rewritten\t\t\tw6.5-design/prototype/0001-p.patch\tspike/w6.5-design (deleted on origin)\tp\n"
    )
    _, b = _repo(repo, {"w7/c.txt": "c\n", "w6.7/commit-map.tsv": table})
    return repo, a, b


COMMAND_LINE = (
    "commands keep their bytes: `go test ./_spikes/s-d1/`, `R=_spikes/s-c1/run.sh`,"
)
PROSE_LINE = "`ok github.com/zchee/typesafe-sdk-go/_spikes/s-c1`, and prose names `_spikes`; spikes@<commit>:<path> is a placeholder."


def _entry(text: str, count: int = 1) -> tuple[str, str, tuple[str, ...], int]:
    return (
        "docs/x.md",
        sc.digest(text),
        tuple(m.group(0) for m in sc.OLD_TOKEN.finditer(text)),
        count,
    )


ALLOWED = frozenset({_entry(COMMAND_LINE), _entry(PROSE_LINE)})

GOOD = [
    "raw `spikes@{a}:w1.2/results/a.txt` and spikes@{a}:w1.2/results/a.txt:2-3",
    "the directory spikes@{a}:w1.2/ and spikes@{a}:w1.2/results; in spikes@{a}:w1.2/b.py: a clause.",
    "a later file spikes@{b}:w7/c.txt; the earlier one still at spikes@{a}:w1.2/b.py. The commit spikes@{a}, pruned.",
    COMMAND_LINE,
    PROSE_LINE,
]


@pytest.fixture
def allowed_lines(monkeypatch: pytest.MonkeyPatch) -> None:
    """Allow the two old-name lines, COMMAND_LINE and PROSE_LINE, and no other."""
    monkeypatch.setattr(sc, "ALLOWED", ALLOWED)


@pytest.fixture
def no_allowed_lines(monkeypatch: pytest.MonkeyPatch) -> None:
    """Allow no old-name line, for a fixture repository that holds none."""
    monkeypatch.setattr(sc, "ALLOWED", frozenset())


TAIL = "\n" + COMMAND_LINE + "\n" + PROSE_LINE + "\n"


@pytest.mark.usefixtures("allowed_lines")
def test_success(tmp_path: Path, archive: tuple[Path, str, str]) -> None:
    arch, a, b = archive
    repo, _ = _repo(
        tmp_path / "co", {"docs/x.md": "\n".join(GOOD).format(a=a, b=b) + "\n"}
    )
    failures, citations = sc.part1(repo)
    assert failures == []
    assert len(citations) == 8
    assert [
        c[3] for c in citations if c[3] is not None and c[3].startswith("w1.2/b")
    ] == ["w1.2/b.py", "w1.2/b.py"]
    assert sc.part2_archive(citations, sc.Clone(arch)) == []


@pytest.mark.usefixtures("allowed_lines")
@pytest.mark.parametrize(
    ("line", "want"),
    [
        (
            "raw `_spikes/w1.2/b.py`",
            "docs/x.md:1: _spikes/w1.2/b.py names the old spike tree on a line that is not allowed",
        ),
        (
            "raw internal/spikes/w7/c.txt",
            "docs/x.md:1: internal/spikes/w7/c.txt names the old spike tree on a line that is not allowed",
        ),
        (
            "a new command `go test ./_spikes/w9/`",
            "docs/x.md:1: ./_spikes/w9/ names the old spike tree on a line that is not allowed",
        ),
        (
            "short spikes@{a11}:w1.2/b.py",
            "docs/x.md:1: spikes@{a11}:w1.2/b.py is not spikes@<12 hex>[:<path>]",
        ),
        (
            "upper spikes@{A}:w1.2/b.py",
            "docs/x.md:1: spikes@{A}:w1.2/b.py is not spikes@<12 hex>[:<path>]",
        ),
        (
            "escape spikes@{a}:w1.2/../x",
            "docs/x.md:1: spikes@{a}:w1.2/../x is not spikes@<12 hex>[:<path>]",
        ),
        (
            "the root spikes@{a}:",
            "docs/x.md:1: spikes@{a}: is not spikes@<12 hex>[:<path>]",
        ),
        (
            "the root spikes@{a}:.",
            "docs/x.md:1: spikes@{a}:. is not spikes@<12 hex>[:<path>]",
        ),
        (
            "elided spikes@{a}:w1.2/...",
            "docs/x.md:1: spikes@{a}:w1.2/... is not spikes@<12 hex>[:<path>]",
        ),
        (
            "elided spikes@{a}:w1.2/…",
            "docs/x.md:1: spikes@{a}:w1.2/… is not spikes@<12 hex>[:<path>]",
        ),
        (
            "a glob spikes@{a}:w1.2/results/*.txt",
            "docs/x.md:1: spikes@{a}:w1.2/results/*.txt is not spikes@<12 hex>[:<path>]",
        ),
        (
            "a glob spikes@{a}:w1.2/b.p?",
            "docs/x.md:1: spikes@{a}:w1.2/b.p? is not spikes@<12 hex>[:<path>]",
        ),
    ],
    ids=[
        "old path",
        "old internal path",
        "new command",
        "short",
        "upper",
        "escape",
        "empty path",
        "empty path, sentence end",
        "ellipsis ...",
        "ellipsis …",
        "glob *",
        "glob ?",
    ],
)
def test_part1_failures(
    tmp_path: Path, archive: tuple[Path, str, str], line: str, want: str
) -> None:
    _, a, _ = archive
    subs = {"a": a, "a11": a[:11], "A": a.upper()}
    repo, _ = _repo(tmp_path / "co", {"docs/x.md": line.format(**subs) + TAIL})
    failures, _ = sc.part1(repo)
    assert len(failures) == 1, failures
    assert failures[0].startswith(want.format(**subs))


@pytest.mark.usefixtures("allowed_lines")
def test_part1_allowed_lines_are_keyed_by_their_text(tmp_path: Path) -> None:
    """A line inserted above an allowed line moves nothing: 0 failures."""
    text = "a new first line\n\n" + COMMAND_LINE + "\n" + PROSE_LINE + "\n"
    repo, _ = _repo(tmp_path / "co", {"docs/x.md": text})
    assert sc.part1(repo)[0] == []


GONE = (
    "docs/x.md: an allowed line naming ./_spikes/s-d1/ R=_spikes/s-c1/run.sh occurs 0 times, 1 allowed; "
    "a quoted command keeps its bytes"
)


@pytest.mark.usefixtures("allowed_lines")
@pytest.mark.parametrize(
    ("text", "want"),
    [
        (
            COMMAND_LINE + "\nrerun: `go test ./_spikes/s-d1/`\n",
            [
                "docs/x.md:2: ./_spikes/s-d1/ names the old spike tree on a line that is not allowed"
            ],
        ),
        (
            COMMAND_LINE + "\n" + COMMAND_LINE + "\n",
            [
                "docs/x.md:1: an allowed line naming ./_spikes/s-d1/ R=_spikes/s-c1/run.sh occurs 2 times, 1 allowed"
            ],
        ),
        (
            COMMAND_LINE.replace("run.sh", "run2.sh") + "\n",
            [
                "docs/x.md:1: ./_spikes/s-d1/ R=_spikes/s-c1/run2.sh names the old spike tree on a line that is not allowed",
                GONE,
            ],
        ),
        (
            COMMAND_LINE.replace("keep their bytes", "keep their bytes, and more")
            + "\n",
            [
                "docs/x.md:1: ./_spikes/s-d1/ R=_spikes/s-c1/run.sh names the old spike tree on a line that is not allowed",
                GONE,
            ],
        ),
        (
            COMMAND_LINE
            + "\n`go test ./_spikes/s-d1/` wrote `_spikes/s-d1/results/x.txt`\n",
            [
                (
                    "docs/x.md:2: ./_spikes/s-d1/ _spikes/s-d1/results/x.txt names the old spike tree on a line that is not "
                    "allowed"
                )
            ],
        ),
        (
            COMMAND_LINE.replace(
                "`R=_spikes/s-c1/run.sh`", "`R=spikes@0123456789ab:s-c1/run.sh`"
            )
            + "\n",
            [
                "docs/x.md:1: ./_spikes/s-d1/ names the old spike tree on a line that is not allowed",
                GONE,
            ],
        ),
        ("a first line\n", [GONE]),
    ],
    ids=[
        "a new line in command form",
        "an allowed line copied",
        "an allowed token edited",
        "an allowed line's prose edited",
        "a citation beside a command",
        "a command token turned into a citation",
        "an allowed line removed",
    ],
)
def test_part1_old_name_places(tmp_path: Path, text: str, want: list[str]) -> None:
    repo, _ = _repo(tmp_path / "co", {"docs/x.md": text + PROSE_LINE + "\n"})
    failures = sc.part1(repo)[0]
    assert len(failures) == len(want), failures
    for got, prefix in zip(failures, want, strict=True):
        assert got.startswith(prefix), (got, prefix)


@pytest.mark.usefixtures("allowed_lines")
def test_part1_tracked_file_under_the_old_tree(tmp_path: Path) -> None:
    repo, _ = _repo(
        tmp_path / "co",
        {
            "_spikes/w1.2/a.txt": "x\n",
            "internal/spikes/w7/b.txt": "y\n",
            "docs/x.md": TAIL.lstrip("\n"),
        },
    )
    assert sc.part1(repo)[0] == [
        "_spikes/w1.2/a.txt: tracked under the old spike tree",
        "internal/spikes/w7/b.txt: tracked under the old spike tree",
    ]


@pytest.mark.usefixtures("no_allowed_lines")
@pytest.mark.parametrize(
    ("line", "want"),
    [
        (
            "spikes@{a}:w7/c.txt",
            "docs/x.md:1: spikes@{a}:w7/c.txt: no such path at {a}",
        ),
        (
            "spikes@0123456789ab:w1.2/b.py",
            "docs/x.md:1: spikes@0123456789ab: no such commit in the archive",
        ),
        (
            "the commit spikes@0123456789ab.",
            "docs/x.md:1: spikes@0123456789ab: no such commit in the archive",
        ),
        (
            "spikes@{a}:w1.2/result",
            "docs/x.md:1: spikes@{a}:w1.2/result: no such path at {a}",
        ),
    ],
)
def test_part2_archive_failures(
    tmp_path: Path, archive: tuple[Path, str, str], line: str, want: str
) -> None:
    arch, a, _ = archive
    repo, _ = _repo(
        tmp_path / "co",
        {"docs/x.md": line.format(a=a) + f"\nand a good one spikes@{a}:w1.2/b.py\n"},
    )
    failures, citations = sc.part1(repo)
    assert failures == []
    assert sc.part2_archive(citations, sc.Clone(arch)) == [want.format(a=a)]


@pytest.mark.usefixtures("no_allowed_lines")
def test_part2_archive_with_nothing_to_resolve_fails(
    tmp_path: Path, archive: tuple[Path, str, str]
) -> None:
    repo, _ = _repo(tmp_path / "co", {"docs/x.md": "no citation\n"})
    assert sc.part2_archive(sc.part1(repo)[1], sc.Clone(archive[0])) == [
        "no spikes@ citation found; part 2 would pass vacuously"
    ]


def test_part2_history(tmp_path: Path, archive: tuple[Path, str, str]) -> None:
    """An old SHA of main or of wave/w7 fails, after ':' too; a SHA of the history counts; a raw file
    name, brace-expanded or not, is skipped; a tree hash and a run id name no commit."""
    arch = archive[0]
    history, _ = _repo(tmp_path / "history", {"f": "1\n"})
    head = git(history, "rev-parse", "HEAD")
    tree = git(history, "rev-parse", "HEAD^{tree}")
    text = (
        f"at {head[:7]} and {head[:12]}..{head[:7]}; tree {tree[:7]}; run 36296146429;\n"
        f"raw results/alloc-M-{OLD[:7]}.txt keeps its name; the old commit {OLD[:7]} was rewritten.\n"
        f"raw results/b6allocs-M-{{{OLD[:7]},{OLD_W7[:7]}}}.txt keeps its name too.\n"
        f"wave/w7 had {OLD_W7[:9]}; a status line said base:{OLD[:7]}.\n"
        f"the prototype {KEPT[:7]} was never merged.\n"
    )
    repo, _ = _repo(tmp_path / "co", {"docs/x.md": text})
    failures, commits, other = sc.part2_history(repo, sc.Clone(history), sc.Clone(arch))
    assert failures == [
        f"docs/x.md:2: {OLD[:7]} is a commit from before the rewrite; cite its final SHA",
        f"docs/x.md:4: {OLD_W7[:9]} is a commit from before the rewrite; cite its final SHA",
        f"docs/x.md:4: {OLD[:7]} is a commit from before the rewrite; cite its final SHA",
        (
            f"docs/x.md:5: {KEPT[:7]} is a commit no rewritten ref carries; cite its patch in the archive "
            "(the commit map's patch column)"
        ),
    ]
    assert (commits, other) == (3, 2)


@pytest.mark.usefixtures("allowed_lines")
def test_main_without_clones_says_it_checked_nothing(
    tmp_path: Path,
    archive: tuple[Path, str, str],
    capsys: pytest.CaptureFixture[str],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    arch, a, b = archive
    monkeypatch.delenv(sc.ENV_ARCHIVE, raising=False)
    monkeypatch.delenv(sc.ENV_HISTORY, raising=False)
    repo, _ = _repo(
        tmp_path / "co", {"docs/x.md": "\n".join(GOOD).format(a=a, b=b) + "\n"}
    )
    assert sc.main(["--repo", str(repo)]) == 0
    out = capsys.readouterr().out
    assert "part 1: 8 citations well-formed, 0 failures" in out
    assert (
        "part 2: resolved NOTHING: no archive clone (--archive or SPIKES_ARCHIVE); 8 citations skipped"
        in out
    )
    assert "part 2: checked NO commit SHA" in out
    monkeypatch.setenv(sc.ENV_ARCHIVE, str(arch))
    monkeypatch.setenv(sc.ENV_HISTORY, str(repo))
    assert sc.main(["--repo", str(repo)]) == 0
    out = capsys.readouterr().out
    assert f"part 2: 8 of 8 citations resolve in {arch.resolve()}" in out
    assert "0 name commits from before the rewrite" in out


@pytest.mark.usefixtures("no_allowed_lines")
def test_main_exit_status(
    tmp_path: Path, archive: tuple[Path, str, str], capsys: pytest.CaptureFixture[str]
) -> None:
    arch, a, _ = archive
    repo, _ = _repo(tmp_path / "co", {"docs/x.md": f"spikes@{a}:w1.2/nope.txt\n"})
    assert sc.main(["--repo", str(repo), "--archive", str(arch)]) == 1
    assert (
        f"docs/x.md:1: spikes@{a}:w1.2/nope.txt: no such path at {a}"
        in capsys.readouterr().err
    )
    assert sc.main(["--repo", str(repo), "--archive", str(tmp_path / "missing")]) == 2
    assert "is not a git repository" in capsys.readouterr().err


def test_load_allowed_reads_each_row(tmp_path: Path) -> None:
    path = tmp_path / "allowed.tsv"
    path.write_text(
        f"{sc.ALLOWED_HEADER}\ndocs/x.md\tab12\t_spikes ./_spikes/x/\t2\n",
        encoding="utf-8",
    )
    assert sc._load_allowed(path) == frozenset(
        {("docs/x.md", "ab12", ("_spikes", "./_spikes/x/"), 2)}
    )


@pytest.mark.parametrize(
    ("text", "want"),
    [
        pytest.param(
            "file\tsha\ttokens\tcount\n", "the first line is not", id="another header"
        ),
        pytest.param(
            "file\tsha256\ttokens\tcount\ndocs/x.md\tab12\t_spikes\n",
            ":2: 3 fields, want 4",
            id="a short row",
        ),
    ],
)
def test_load_allowed_refuses_a_malformed_table(
    tmp_path: Path, text: str, want: str
) -> None:
    path = tmp_path / "allowed.tsv"
    path.write_text(text, encoding="utf-8")
    with pytest.raises(ValueError, match=want):
        sc._load_allowed(path)


def test_the_repository_passes_part1() -> None:
    assert sc.part1(REPO)[0] == []
