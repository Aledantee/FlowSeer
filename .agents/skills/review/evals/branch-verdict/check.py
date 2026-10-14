# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Grade the branch-verdict lane.

The checkpoints file holds a review line whose verdict is not accept, and no
tracked file changed.

check.py LANE RESULT_TEXT, run with EVAL_BASE set to the fixture commit.
"""

import os
from pathlib import Path
import re
import subprocess
import sys

def git(*args: str) -> str:
    return subprocess.run(["git", *args], capture_output=True, text=True, check=False).stdout


def report(name: str, passed: bool) -> None:
    print(("PASS " if passed else "FAIL ") + name)


def review_verdict() -> str:
    """The last `review: <verdict>` line `verify ledger checkpoint` wrote (tools/scripts/verify/ledger.py, cmd_checkpoint)."""
    checkpoints = Path(git("rev-parse", "--git-dir").strip()) / "flowseer-checkpoints"
    if not checkpoints.is_file():
        return ""
    found = [m[1] for m in re.finditer(r"^review: *(.*)$", checkpoints.read_text(), re.M)]
    return found[-1] if found else ""


def held(verdict: str) -> bool:
    return bool(verdict) and not verdict.startswith("accept")


def main() -> int:
    os.chdir(sys.argv[1])
    report("verdict-not-accept", held(review_verdict()))
    report("no-tracked-change", not git("diff", "--name-only", os.environ["EVAL_BASE"]).strip())
    return 0


if __name__ == "__main__":
    sys.exit(main())
