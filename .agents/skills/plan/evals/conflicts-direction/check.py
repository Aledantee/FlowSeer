# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Grade the conflicts-direction lane. Prints one PASS or FAIL line per check.

check.py LANE RESULT_TEXT, run with EVAL_BASE set to the fixture commit.
"""

import json
import os
from pathlib import Path
import re
import subprocess
import sys

RECORD = "schema-building-blocks-direction"
# A declaration such as "uint64 speed_bps", not a token that only quotes a _bps field.
BPS_DECLARATION = re.compile(r"uint64\s+[a-z0-9_]+_bps([^a-z0-9_]|$)", re.M)

def git(*args: str) -> str:
    return subprocess.run(["git", *args], capture_output=True, text=True, check=False).stdout


def report(name: str, passed: bool) -> None:
    print(("PASS " if passed else "FAIL ") + name)


def lane_paths(*diff_flags: str) -> list[str]:
    """Paths changed since the fixture commit. A lane may leave its work uncommitted, so untracked files count."""
    listed = git("diff", "--name-only", *diff_flags, os.environ["EVAL_BASE"]) + git("ls-files", "--others", "--exclude-standard")
    return sorted({line for line in listed.splitlines() if line})


def readiness(state: Path) -> str | None:
    try:
        return json.loads(state.read_text()).get("readiness")
    except (OSError, ValueError, AttributeError):
        return None


def main() -> int:
    os.chdir(sys.argv[1])
    text = sys.argv[2] if len(sys.argv) > 2 else ""
    new_plans = [p for p in lane_paths("--diff-filter=A") if re.fullmatch(r"docs/plans/.*-plan\.md", p)]
    plan = new_plans[0] if new_plans and Path(new_plans[0]).is_file() else ""
    body = Path(plan).read_text() if plan else ""

    # A lane that raises the conflict and stops at the question writes no plan, so its result text counts too.
    named_in_text = RECORD in text
    report("plan-names-record", RECORD in body or named_in_text)

    honored = False
    if plan:
        honored = (
            any(line.startswith("Files:") and RECORD in line for line in body.splitlines())
            or readiness(Path(plan.removesuffix(".md") + ".state.json")) == "needs-decisions"
            or bool(BPS_DECLARATION.search(body))
        )
    if named_in_text and re.search(r"uint64|[a-z0-9]_bps", text):
        honored = True
    report("plan-honors-record", honored)
    return 0


if __name__ == "__main__":
    sys.exit(main())
