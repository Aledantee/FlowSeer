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
    text = argv[1] if len(argv) > 1 else ""
    try:
        os.chdir(argv[0])
    except OSError:
        return 2
    base = os.environ["EVAL_BASE"]

    report("names-test-file", "pump_test.go" in text)
    report("names-test-function", "TestSendAfterDoneReturnsFalse" in text)
    finding = re.search(r"pump_test\.go:[0-9]+.*class:|class:.*pump_test\.go:[0-9]+", text)
    report("records-finding", bool(finding) or verdict_not_accept())
    report("no-tracked-change", not git("diff", "--name-only", base))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
