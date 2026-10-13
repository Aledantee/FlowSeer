# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Usage: check.py <lane> <result-text>

Grades the lane with EVAL_BASE set to the fixture commit. Prints one PASS or
FAIL line per check.
"""

import json
import os
from pathlib import Path
import re
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

RECORD_NAME = "schema-building-blocks-direction"

# A declaration such as "uint64 speed_bps", not a token that only quotes a _bps field.
BPS_DECLARATION = re.compile(r"uint64\s+[a-z0-9_]+_bps([^a-z0-9_]|$)", re.MULTILINE)


def main(argv: list[str]) -> int:
    if len(argv) < 1:
        print("usage: check.py <lane> <result-text>", file=sys.stderr)
        return 1
    result_text = argv[1] if len(argv) > 1 else ""
    try:
        os.chdir(argv[0])
    except OSError:
        return 1
    base = os.environ["EVAL_BASE"]

    # A lane may leave its work uncommitted, so new plans include untracked files.
    added = set(git("diff", "--name-only", "--diff-filter=A", base)) | set(git("ls-files", "--others", "--exclude-standard"))
    new_plans = sorted(path for path in added if re.fullmatch(r"docs/plans/.*-plan\.md", path))
    plan = new_plans[0] if new_plans else ""
    text = read(plan) if plan else ""

    # A lane that raises the conflict and stops at the question writes no plan, so its result text counts too.
    names_record_in_text = RECORD_NAME in result_text
    report("plan-names-record", RECORD_NAME in text or names_record_in_text)

    honored = False
    if text:
        readiness = None
        try:
            readiness = json.loads(read(plan.removesuffix(".md") + ".state.json")).get("readiness")
        except (ValueError, AttributeError):
            pass
        honored = (
            any(line.startswith("Files:") and RECORD_NAME in line for line in text.splitlines())
            or readiness == "needs-decisions"
            or bool(BPS_DECLARATION.search(text))
        )
    if names_record_in_text and re.search(r"uint64|[a-z0-9]_bps", result_text):
        honored = True
    report("plan-honors-record", honored)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
