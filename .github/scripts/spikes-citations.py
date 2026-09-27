#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Check the citations of the spike archive.

The spikes left this repository for a private archive repository that keeps
their history, and main's history was rewritten without them on 2026-09-27.
The documents cite a file or a directory there as ``spikes@<commit>:<path>``
(git's ``<rev>:<path>`` behind a fixed prefix, the archive commit in 12 hex
digits, the path below the archive's root, an optional ``:N`` or ``:N-M``
line suffix) and a commit, with its whole tree, as ``spikes@<commit>``. A
path is never empty and holds no glob and no ellipsis: a citation names one
file or one directory that exists. Run from the repository root.

Part 1, everywhere (CI's lint step runs it through pytest):

1. no tracked file lies under ``_spikes/`` or ``internal/spikes/``;
2. every line that names the old tree is on ALLOWED, the explicit list in
   spikes-allowed.tsv beside this script of (file, SHA-256 of the line's
   text, the old-name tokens on it, how many times that line occurs in the
   file): every token of a quoted command keeps its bytes as it ran (a
   package path, a variable, a script and its arguments, a pathspec), a
   sentence that says what a commit changed names the paths as they were in
   that commit, and a few prose words name the old directory. The list is
   data, keyed by the text and not by the line number: an edit elsewhere in
   the file moves nothing, while a new line in the form of a command, an
   allowed line copied to a second place, or an allowed line whose text
   changed fails; so does an entry whose line is gone (a command token
   rewritten into a citation, or a line removed without its entry);
3. every ``spikes@`` token has the form (a ``spikes@<`` placeholder in prose
   is not a citation).

Part 2, with local clones:

4. ``--archive DIR`` (or $SPIKES_ARCHIVE): every citation names a commit of
   the archive, and its path exists at that commit;
5. ``--history DIR`` (or $SDK_HISTORY), a full clone of this repository,
   together with the archive: no commit SHA cited in a tracked file is one
   of the SHAs before the rewrite (the archive's w6.7/commit-map.tsv lists
   them, for every rewritten ref), and the report counts the cited SHAs that
   name commits of the history. A hex token glued into a name (a raw file
   name such as ``alloc-M-<sha>.txt`` or ``b6allocs-M-{<sha>,<sha>}.txt``, an
   archive path) is not a citation.

Without a clone, part 2 prints that it resolved NOTHING and how many
citations and SHA tokens it skipped; that is the only way it exits 0
without checking them. A clone that is not a git repository is a usage
error (exit 2). Exit status 1 names each failure as file:line.
"""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import subprocess
import sys
from pathlib import Path

OLD_ROOTS = ("_spikes", "internal/spikes")
DELIM = r"[^\s`'\"|()\[\],;<>]"
OLD_TOKEN = re.compile(
    rf"(?<![\w.-])[\w./=-]*?(?:_spikes|internal/spikes)(?:/{DELIM}*)?"
)
CITATION = re.compile(rf"(?<![\w.-])spikes@(?!<)({DELIM}*)")
FORM = re.compile(r"([0-9a-f]{12})(?::(.*))?")
LINE_SUFFIX = re.compile(r":\d+(?:-\d+)?$")
HEX = re.compile(r"(?<![0-9A-Za-z])[0-9a-f]{7,40}(?![0-9A-Za-z])")
ANCHOR_LINK = re.compile(r"\]\(([^)\s]*#[^)\s]+)\)")
ENV_ARCHIVE, ENV_HISTORY = "SPIKES_ARCHIVE", "SDK_HISTORY"
MAP = "w6.7/commit-map.tsv"
# This checker, its table and its tests name the old tree and malformed
# citations on purpose.
SELF = (
    ":(exclude).github/scripts/spikes-citations.py",
    ":(exclude).github/scripts/spikes-allowed.tsv",
    ":(exclude).github/scripts/test_spikes_citations.py",
)

# The lines where the old name stands, as (file, SHA-256 of the line's text,
# its old-name tokens, how many times the line occurs in the file): the
# record of a command line, or a word of prose. spikes-allowed.tsv holds one
# per row, its tokens separated by a space. A new entry is a decision, not a
# pattern, and an allowed line whose text is edited needs its entry updated.
ALLOWED_FILE = Path(__file__).with_name("spikes-allowed.tsv")
ALLOWED_HEADER = "file\tsha256\ttokens\tcount"

Entry = tuple[str, str, tuple[str, ...], int]


def _load_allowed(path: Path) -> frozenset[Entry]:
    """Read the allowed lines of the table at ``path``.

    Args:
        path: a tab-separated file headed ALLOWED_HEADER.

    Returns:
        One (file, SHA-256, tokens, count) entry per row.

    Raises:
        OSError: the file cannot be read.
        ValueError: the header or a row is malformed.
    """
    lines = path.read_text(encoding="utf-8").splitlines()
    if not lines or lines[0] != ALLOWED_HEADER:
        raise ValueError(f"{path}: the first line is not {ALLOWED_HEADER!r}")
    entries: set[Entry] = set()
    for number, line in enumerate(lines[1:], start=2):
        fields = line.split("\t")
        if len(fields) != 4:
            raise ValueError(f"{path}:{number}: {len(fields)} fields, want 4")
        file, sha, tokens, count = fields
        entries.add((file, sha, tuple(tokens.split(" ")), int(count)))
    return frozenset(entries)


ALLOWED = _load_allowed(ALLOWED_FILE)


def _git(
    repo: Path, *args: str, check: bool = True
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", "-C", str(repo), *args], capture_output=True, text=True, check=check
    )


def _grep(repo: Path, needle: str, regex: bool = False) -> list[tuple[str, int, str]]:
    proc = _git(
        repo,
        "grep",
        "-n",
        "-I",
        "-E" if regex else "-F",
        "-e",
        needle,
        "--",
        ".",
        *SELF,
        check=False,
    )
    if proc.returncode not in (0, 1):
        raise RuntimeError(f"git grep failed: {proc.stderr}")
    hits = []
    for line in proc.stdout.splitlines():
        path, number, text = line.split(":", 2)
        hits.append((path, int(number), text))
    return hits


def parse(body: str) -> tuple[str, str | None] | None:
    """Return (commit, path or None) of a well-formed citation body, else None.

    The path is refused when it is empty, absolute, holds ``..`` (an escape
    or an ellipsis), ``…``, or a glob character, or ends in a dot.
    """
    m = FORM.fullmatch(body)
    if not m:
        return None
    path = m.group(2)
    if path is not None:
        bare = LINE_SUFFIX.sub("", path)
        if (
            not bare
            or bare.startswith("/")
            or bare.endswith(".")
            or ".." in bare
            or "…" in bare
            or any(c in bare for c in "*?[")
        ):
            return None
    return m.group(1), path


def citation_body(token: str) -> str:
    """The body of a CITATION match without the punctuation of the sentence around it.

    One trailing '.', ',' or ';' ends the sentence; a trailing ':' after a
    path ends a clause (``spikes@<c>:<path>: ...``). ``spikes@<c>:`` keeps its
    colon, so its empty path is refused.
    """
    if token[-1:] in (".", ",", ";"):
        token = token[:-1]
    if token.endswith(":") and token.count(":") >= 2:
        token = token[:-1]
    return token


def digest(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def old_name_lines(
    repo: Path,
) -> dict[tuple[str, str], tuple[tuple[str, ...], int, int]]:
    """Every line of a tracked file that names the old tree.

    Keyed by (file, SHA-256 of the text); the value is (its old-name tokens,
    how many times the line occurs in the file, the first line number).
    """
    found: dict[tuple[str, str], tuple[tuple[str, ...], int, int]] = {}
    seen: set[tuple[str, int]] = set()
    for root in OLD_ROOTS:
        for path, number, text in _grep(repo, root):
            if (path, number) in seen:
                continue
            seen.add((path, number))
            tokens = tuple(m.group(0) for m in OLD_TOKEN.finditer(text))
            if not tokens:
                continue
            key = (path, digest(text))
            prev = found.get(key)
            found[key] = (tokens, prev[1] + 1, prev[2]) if prev else (tokens, 1, number)
    return found


def part1(repo: Path) -> tuple[list[str], list[tuple[str, int, str, str | None]]]:
    """Return (failures, citations as (file, line, commit, path or None))."""
    failures = [
        f"{p}: tracked under the old spike tree"
        for p in _git(
            repo, "ls-files", "--", "_spikes/", "internal/spikes/"
        ).stdout.split()
    ]
    allowed = {(f, h): (toks, n) for f, h, toks, n in ALLOWED}
    found = old_name_lines(repo)
    for (path, h), (tokens, count, number) in sorted(
        found.items(), key=lambda kv: (kv[0][0], kv[1][2])
    ):
        entry = allowed.get((path, h))
        if entry is None or entry[0] != tokens:
            failures.append(
                f"{path}:{number}: {' '.join(tokens)} names the old spike tree on a line that is not "
                "allowed; cite spikes@<commit>:<path>"
            )
        elif count > entry[1]:
            failures.append(
                f"{path}:{number}: an allowed line naming {' '.join(tokens)} occurs {count} times, "
                f"{entry[1]} allowed"
            )
    for f, h, toks, n in sorted(ALLOWED):
        got = found.get((f, h))
        have = got[1] if got is not None and got[0] == toks else 0
        if have < n:
            failures.append(
                f"{f}: an allowed line naming {' '.join(toks)} occurs {have} times, {n} allowed; a quoted "
                "command keeps its bytes, and a line removed on purpose takes its entry with it"
            )
    citations = []
    for path, number, text in _grep(repo, "spikes@"):
        for m in CITATION.finditer(text):
            parsed = parse(citation_body(m.group(1)))
            if parsed is None:
                failures.append(
                    f"{path}:{number}: {m.group(0)} is not spikes@<12 hex>[:<path>]"
                )
            else:
                citations.append((path, number, *parsed))
    return failures, citations


class Clone:
    def __init__(self, repo: Path) -> None:
        if _git(repo, "rev-parse", "--git-dir", check=False).returncode != 0:
            raise ValueError(f"{repo} is not a git repository")
        self.repo = repo
        self._paths: dict[str, tuple[set[str], set[str]]] = {}

    def commit(self, rev: str) -> str | None:
        proc = _git(
            self.repo,
            "rev-parse",
            "--verify",
            "--quiet",
            f"{rev}^{{commit}}",
            check=False,
        )
        return proc.stdout.strip() or None

    def resolves(self, commit: str, path: str) -> bool:
        if commit not in self._paths:
            files = set(
                _git(
                    self.repo, "ls-tree", "-r", "--name-only", "-z", commit
                ).stdout.split("\0")
            ) - {""}
            dirs = {
                "/".join(f.split("/")[:i])
                for f in files
                for i in range(1, f.count("/") + 1)
            } | {""}
            self._paths[commit] = (files, dirs)
        files, dirs = self._paths[commit]
        p = LINE_SUFFIX.sub("", path).rstrip("/")
        return p in files or p in dirs


def part2_archive(
    citations: list[tuple[str, int, str, str | None]], archive: Clone
) -> list[str]:
    failures = []
    for path, number, commit, target in citations:
        full = archive.commit(commit)
        if full is None:
            failures.append(
                f"{path}:{number}: spikes@{commit}: no such commit in the archive"
            )
        elif target is not None and not archive.resolves(full, target):
            failures.append(
                f"{path}:{number}: spikes@{commit}:{target}: no such path at {commit}"
            )
    if not citations:
        failures.append("no spikes@ citation found; part 2 would pass vacuously")
    return failures


def in_named_brace_group(line: str, start: int, end: int) -> bool:
    """A token that is one member of a brace group glued to a name, as in
    results/b6allocs-M-{b002bd5,ddec26a}.txt: part of a raw file NAME."""
    if (
        start == 0
        or line[start - 1] not in "{,"
        or end >= len(line)
        or line[end] not in ",}"
    ):
        return False
    open_, close = line.rfind("{", 0, start), line.find("}", end)
    if (
        open_ < 0
        or close < 0
        or "}" in line[open_ + 1 : start]
        or "{" in line[end:close]
    ):
        return False
    before = line[open_ - 1] if open_ > 0 else " "
    after = line[close + 1] if close + 1 < len(line) else " "
    return any(c.isalnum() or c in "-_./" for c in (before, after))


def sha_tokens(repo: Path) -> list[tuple[str, int, str]]:
    """Hex tokens that stand as commit citations: not glued into a name (an anchor link counts)."""
    out = []
    for path, number, line in _grep(repo, "[0-9a-f]{7,40}", regex=True):
        links = [(m.start(1), m.end(1)) for m in ANCHOR_LINK.finditer(line)]
        for m in HEX.finditer(line):
            left = line[m.start() - 1] if m.start() > 0 else " "
            right = line[m.end() : m.end() + 2]
            glued_right = right[:1] != "" and (
                right[:1] in "-_/" or (right[:1] == "." and right[1:2].isalnum())
            )
            glued = (
                left in "-_/@"
                or glued_right
                or in_named_brace_group(line, m.start(), m.end())
            )
            if left == "." and line[max(0, m.start() - 2) : m.start()] == "..":
                glued = glued_right
            if glued and not any(s <= m.start() < e for s, e in links):
                continue
            out.append((path, number, m.group(0)))
    return out


def part2_history(
    repo: Path, history: Clone, archive: Clone
) -> tuple[list[str], int, int]:
    table = _git(archive.repo, "show", f"HEAD:{MAP}", check=False)
    if table.returncode != 0:
        return [f"the archive holds no {MAP} at HEAD"], 0, 0
    lines = table.stdout.splitlines()
    header = lines[0].split("\t")
    col = header.index("sdk_final") if "sdk_final" in header else 1
    final_of = {r.split("\t")[0]: r.split("\t")[col] for r in lines[1:] if r}
    old = sorted(final_of)
    failures, commits, other = [], 0, 0
    for path, number, tok in sha_tokens(repo):
        hit = next((o for o in old if o.startswith(tok)), None)
        if hit is not None and final_of[hit] == "not rewritten":
            failures.append(
                f"{path}:{number}: {tok} is a commit no rewritten ref carries; cite its patch in the "
                "archive (the commit map's patch column)"
            )
        elif hit is not None:
            failures.append(
                f"{path}:{number}: {tok} is a commit from before the rewrite; cite its final SHA"
            )
        elif history.commit(tok) is not None:
            commits += 1
        else:
            other += 1
    return failures, commits, other


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Check the citations of the spike archive (W6.7)."
    )
    parser.add_argument(
        "--archive",
        type=Path,
        help=f"a local clone of the archive (default: ${ENV_ARCHIVE})",
    )
    parser.add_argument(
        "--history",
        type=Path,
        help=f"a full clone of this repository (default: ${ENV_HISTORY})",
    )
    parser.add_argument(
        "--repo",
        type=Path,
        default=Path("."),
        help="the repository to check (default: .)",
    )
    args = parser.parse_args(argv)
    clones: dict[str, Clone | None] = {}
    for name, flag, env in (
        ("archive", args.archive, ENV_ARCHIVE),
        ("history", args.history, ENV_HISTORY),
    ):
        location = flag or (Path(os.environ[env]) if os.environ.get(env) else None)
        try:
            clones[name] = Clone(location.resolve()) if location else None
        except ValueError as err:
            print(f"spikes-citations: {err}", file=sys.stderr)
            return 2
    repo = args.repo.resolve()
    failures, citations = part1(repo)
    print(f"part 1: {len(citations)} citations well-formed, {len(failures)} failures")
    archive, history = clones["archive"], clones["history"]
    if archive is None:
        print(
            f"part 2: resolved NOTHING: no archive clone (--archive or {ENV_ARCHIVE}); "
            f"{len(citations)} citations skipped"
        )
    else:
        more = part2_archive(citations, archive)
        print(
            f"part 2: {len(citations) - len(more)} of {len(citations)} citations resolve in {archive.repo}"
        )
        failures += more
    if archive is None or history is None:
        print(
            f"part 2: checked NO commit SHA: it needs the archive and a full clone (--history or {ENV_HISTORY}); "
            f"{len(sha_tokens(repo))} SHA tokens skipped"
        )
    else:
        more, commits, other = part2_history(repo, history, archive)
        print(
            f"part 2: {commits} SHA tokens name commits of the history, {other} name none (trees, run ids, digests), "
            f"{len(more)} name commits from before the rewrite"
        )
        failures += more
    for failure in failures:
        print(failure, file=sys.stderr)
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
