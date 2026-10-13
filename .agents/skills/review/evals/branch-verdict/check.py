# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Grade the branch-verdict lane.

The checkpoints file holds a review line whose verdict is not accept, and no
tracked file changed. The line format is `review: <verdict>`, as `verify
ledger checkpoint` writes it (tools/scripts/verify/ledger.py, cmd_checkpoint).

check.py LANE RESULT_TEXT, run with EVAL_BASE set.
"""

import os
from pathlib import Path
import re
import subprocess
import sys


def git(*args: str) -> str:
    return subprocess.run(["git", *args], capture_output=True, text=True, check=False).stdout.strip()


def report(name: str, passed: bool) -> None:
    print(f"{'PASS' if passed else 'FAIL'} {name}")


def verdict_not_accept() -> bool:
    """Whether the last recorded review verdict exists and is not an accept."""
    try:
        text = (Path(git("rev-parse", "--git-dir")) / "flowseer-checkpoints").read_text()
    except OSError:
        return False
    verdicts = re.findall(r"^review: *(.*)$", text, re.MULTILINE)
    return bool(verdicts) and bool(verdicts[-1]) and not verdicts[-1].startswith("accept")


def main(argv: list[str]) -> int:
    if len(argv) < 1:
        print("usage: check.py LANE RESULT_TEXT", file=sys.stderr)
        return 2
    try:
        os.chdir(argv[0])
    except OSError:
        return 2
    base = os.environ["EVAL_BASE"]

    report("verdict-not-accept", verdict_not_accept())
    report("no-tracked-change", not git("diff", "--name-only", base))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
