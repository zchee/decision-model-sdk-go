#!/usr/bin/env bash
# The coverage profile of the test job's -race step and the check that every
# zero-count block in it has a reason in docs/uncovered-lines.md. ci.yaml
# runs both through this script, as the steps "go test -race with coverage"
# and "Every uncovered block has a reason (AC-Q2)", and a local run uses the
# same script, so the go test flags live here alone and the profile checked
# on a workstation is made by the command CI runs.
#
# Usage, from any directory (FILE is relative to the repository root and
# defaults to coverage.out, which .gitignore keeps out of git status):
#
#   .github/scripts/race-coverage.sh [all [FILE]]  # profile, then check
#   .github/scripts/race-coverage.sh profile [FILE]
#   .github/scripts/race-coverage.sh check [FILE]
#
# CI runs profile and check as two steps with the Codecov upload between
# them, so a failed check still leaves Codecov its report. all runs the
# check even after go test fails, as long as a profile was written, so that
# one failing test does not hide the check's result; its summary says that
# the profile came from a failed run.
#
# Exit status: 0 when every part that ran passed; non-zero when go test or
# the check failed, with an error line naming which, and when a FILE left by
# an earlier run cannot be removed, in which case nothing runs; 2 for a
# usage error.
#
# docs/uncovered-lines.md describes the profile of CI's ubuntu-26.04 image;
# CI runs the check on that image alone. A profile made on another platform
# can differ where code is built per platform or where timing decides
# whether a block runs.
#
# profile: -coverpkg=./... instruments every package in every test binary,
# so a block counts as covered when any test of the module runs it: code
# that one package's tests drive through another's API is credited where it
# lives.
#
# `go tool -n covdata` builds the covdata tool into the build cache before
# the tests, in the same environment (the cache entry depends on it).
# Otherwise go test's covdata processes for the packages without tests (the
# examples) each build it into the same cache entry, in place, and one can
# exec the file while another writes it: "fork/exec .../covdata: text file
# busy" on Linux although every package is ok. On Linux the files named
# covdata in GOCACHE (inode, size, mtime) are listed after the warm-up and
# after the test command, and the run fails when the first list lacks the
# path the warm-up printed or the two lists differ: either means the test
# command's processes may still build the tool.

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$script_dir/$(basename "${BASH_SOURCE[0]}")"
root="$(cd "$script_dir/../.." && pwd)"
doc=docs/uncovered-lines.md

usage() {
  printf 'usage: %s [all|profile|check [FILE]]\n' "$0"
  printf '  all      profile, then check (the default)\n'
  printf '  profile  write FILE with the -race coverage flags of CI\n'
  printf '  check    check that every zero-count block of FILE has a reason in %s\n' "$doc"
  printf 'FILE is relative to the repository root; default coverage.out.\n'
}

# fail MESSAGE prints MESSAGE as an error annotation under GitHub Actions
# and on standard error elsewhere.
fail() {
  if [ "${GITHUB_ACTIONS:-}" = true ]; then
    printf '::error::%s\n' "$1"
  else
    printf 'race-coverage: error: %s\n' "$1" >&2
  fi
}

# covdata_files lists each file named covdata in GOCACHE with its inode,
# size and mtime; -printf is GNU find's, hence Linux only.
covdata_files() {
  find "$(go env GOCACHE)" -type f -name covdata -printf '%i %s %TY-%Tm-%Td %TT %p\n' | sort
}

# remove_stale FILE removes what an earlier run left at FILE, so that a
# profile this run fails to write cannot be checked in its place, and stops
# the script, naming FILE and rm's reason, when FILE cannot be removed.
remove_stale() {
  local err
  if ! err="$(rm -f -- "$1" 2>&1)"; then
    fail "cannot remove $1, left by an earlier run, before go test writes it: $err"
    exit 1
  fi
}

# profile FILE writes FILE with CI's go test flags, inside the covdata
# guard described above.
profile() {
  local file=$1 tool before after status=0 linux=false
  remove_stale "$file"
  if [ "$(uname -s)" = Linux ]; then
    linux=true
  fi
  tool="$(go tool -n covdata)"
  echo "go tool -n covdata: $tool"
  if [ "$linux" = true ]; then
    before="$(covdata_files)"
    printf 'files named covdata in GOCACHE after the warm-up:\n%s\n' "$before"
    if ! grep -qF -- " $tool" <<<"$before"; then
      fail "the path the warm-up printed, $tool, is not among the files named covdata in GOCACHE"
      exit 1
    fi
  fi
  # The test command's status is kept until the second list is out.
  go test -race -count=1 -coverpkg=./... -coverprofile="$file" -covermode=atomic ./... || status=$?
  if [ "$linux" = true ]; then
    after="$(covdata_files)"
    printf 'files named covdata in GOCACHE after the test command:\n%s\n' "$after"
    if [ "$after" != "$before" ]; then
      fail "the test command changed the files named covdata in GOCACHE: the warm-up did not reach the entry it uses"
      exit 1
    fi
  fi
  if [ "$status" -ne 0 ]; then
    fail "go test failed with exit status $status"
    exit "$status"
  fi
}

# check FILE requires a row with a reason in the document for every
# zero-count block of FILE.
check() {
  local file=$1 status=0
  uv run --script .github/scripts/uncovered-lines.py --profile "$file" --doc "$doc" || status=$?
  if [ "$status" -ne 0 ]; then
    fail "the uncovered-block check of $file against $doc failed with exit status $status"
    exit "$status"
  fi
}

# all FILE runs profile and then check, each in its own process so that
# errexit holds inside it, and prints one status line for each. FILE is
# removed here first, so whatever check reads was written by this run's
# go test.
all() {
  local file=$1 test_status=0 check_status=0 test_line check_line
  remove_stale "$file"
  "$BASH" "$script" profile "$file" || test_status=$?
  if [ "$test_status" -eq 0 ]; then
    test_line=ok
  else
    test_line="FAILED (exit status $test_status)"
  fi
  if [ ! -s "$file" ]; then
    check_line="not run: no profile was written"
    check_status=1
  else
    "$BASH" "$script" check "$file" || check_status=$?
    if [ "$check_status" -eq 0 ]; then
      check_line=ok
    else
      check_line="FAILED (exit status $check_status)"
    fi
    if [ "$test_status" -ne 0 ]; then
      check_line="$check_line, on the profile of a failed go test run"
    fi
  fi
  printf 'race-coverage: go test -race with coverage (%s): %s\n' "$file" "$test_line"
  printf 'race-coverage: uncovered-block check against %s: %s\n' "$doc" "$check_line"
  if [ "$test_status" -ne 0 ] || [ "$check_status" -ne 0 ]; then
    exit 1
  fi
}

mode=all
file=coverage.out
if [ "$#" -gt 0 ]; then
  case "$1" in
    all | profile | check) mode=$1 ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
  shift
fi
if [ "$#" -gt 0 ]; then
  file=$1
  shift
fi
if [ "$#" -gt 0 ] || [ -z "$file" ]; then
  usage >&2
  exit 2
fi

cd "$root"
"$mode" "$file"
