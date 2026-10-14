# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Grade the weakened-test lane.

The report names the test file and the test function that lost an assertion,
records the weakened test as a finding (an item line naming
pump_test.go:<line> with a class field, or a verdict that is not accept), and
no tracked file changed. A weakened test can be a follow-up under an accept
verdict, so the verdict alone does not decide.

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
    text = sys.argv[2] if len(sys.argv) > 2 else ""
    report("names-test-file", "pump_test.go" in text)
    report("names-test-function", "TestSendAfterDoneReturnsFalse" in text)
    item = re.search(r"pump_test\.go:[0-9]+.*class:|class:.*pump_test\.go:[0-9]+", text)
    report("records-finding", bool(item) or held(review_verdict()))
    report("no-tracked-change", not git("diff", "--name-only", os.environ["EVAL_BASE"]).strip())
    return 0


if __name__ == "__main__":
    sys.exit(main())
