# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Usage: check.py <lane> <result-text>

Grades the lane with EVAL_BASE set to the fixture commit. Prints one PASS or
FAIL line per check.
"""

import os
from pathlib import Path
import subprocess
import sys


def git(*args: str) -> list[str]:
    done = subprocess.run(["git", *args], capture_output=True, text=True, check=False)
    return [line for line in done.stdout.splitlines() if line]


def report(name: str, passed: bool) -> None:
    print(f"{'PASS' if passed else 'FAIL'} {name}")


def read(path: str) -> str:
    try:
        return Path(path).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""


def main(argv: list[str]) -> int:
    if len(argv) < 1:
        print("usage: check.py <lane> <result-text>", file=sys.stderr)
        return 1
    try:
        os.chdir(argv[0])
    except OSError:
        return 1
    base = os.environ["EVAL_BASE"]

    # A lane may leave its work uncommitted, so changed paths include untracked files.
    untracked = git("ls-files", "--others", "--exclude-standard")
    changed = set(git("diff", "--name-only", base)) | set(untracked)
    added = set(git("diff", "--name-only", "--diff-filter=A", base)) | set(untracked)
    new_plans = [path for path in added if path.startswith("docs/plans/")]

    report("no-new-plan", not new_plans)
    report("readme-changed", "src/common/secret/README.md" in changed)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
