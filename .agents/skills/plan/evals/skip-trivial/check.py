# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Grade the skip-trivial lane. Prints one PASS or FAIL line per check.

check.py LANE RESULT_TEXT, run with EVAL_BASE set to the fixture commit.
"""

import os
import subprocess
import sys

def git(*args: str) -> str:
    return subprocess.run(["git", *args], capture_output=True, text=True, check=False).stdout


def report(name: str, passed: bool) -> None:
    print(("PASS " if passed else "FAIL ") + name)


def lane_paths(*diff_flags: str) -> list[str]:
    """Paths changed since the fixture commit. A lane may leave its work uncommitted, so untracked files count."""
    listed = git("diff", "--name-only", *diff_flags, os.environ["EVAL_BASE"]) + git("ls-files", "--others", "--exclude-standard")
    return sorted({line for line in listed.splitlines() if line})


def main() -> int:
    os.chdir(sys.argv[1])
    new_plans = [p for p in lane_paths("--diff-filter=A") if p.startswith("docs/plans/")]
    report("no-new-plan", not new_plans)
    report("readme-changed", "src/common/secret/README.md" in lane_paths())
    return 0


if __name__ == "__main__":
    sys.exit(main())
