# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Grade the bounded-feature lane. Prints one PASS or FAIL line per check.

check.py LANE RESULT_TEXT, run with EVAL_BASE set to the fixture commit.
"""

import json
import os
from pathlib import Path
import re
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


def run_py(*args: str) -> subprocess.CompletedProcess:
    return subprocess.run(["uv", "run", "tools/scripts/run.py", *args], capture_output=True, text=True, check=False)


def units_hold_files_and_after(lines: list[str]) -> bool:
    """Every `### U<n>.` section holds a Files: line and an After: line."""
    units, bad, current = 0, False, None
    for line in [*lines, "## end"]:
        if re.match(r"##+ ", line):
            if current is not None:
                units += 1
                bad = bad or not all(current.values())
            current = {"files": False, "after": False} if re.match(r"### U[0-9]+\.", line) else None
        elif current is not None:
            if line.startswith("Files:"):
                current["files"] = True
            if line.startswith("After:"):
                current["after"] = True
    return units > 0 and not bad


def state_status(state: Path) -> str | None:
    try:
        return json.loads(state.read_text()).get("status")
    except (OSError, ValueError, AttributeError):
        return None


def queue_group(plan: str) -> str | None:
    try:
        rows = json.loads(run_py("next", "plan-queue", "--json").stdout)
        return next((row.get("group") for row in rows if row.get("path") == plan), None)
    except (ValueError, AttributeError, TypeError):
        return None


def main() -> int:
    os.chdir(sys.argv[1])
    changed = lane_paths()
    new_plans = [p for p in lane_paths("--diff-filter=A") if re.fullmatch(r"docs/plans/.*-plan\.md", p)]

    plan = new_plans[0] if len(new_plans) == 1 else ""
    lines = Path(plan).read_text().splitlines() if plan and Path(plan).is_file() else []
    report("one-new-plan", bool(plan))
    report("artifact-contract", "artifact_contract: flowseer-plan/v2" in lines)
    report("state-planned", bool(plan) and state_status(Path(plan.removesuffix(".md") + ".state.json")) == "planned")
    report("unit-u1", any(line.startswith("### U1.") for line in lines))
    report("files-and-after-per-unit", units_hold_files_and_after(lines))
    report("waves-line", any(line.startswith("Waves:") for line in lines))
    report("check-prose-clean", bool(lines) and run_py("verify", "check-prose", plan).returncode == 0)
    report("plan-queue-ready", bool(plan) and queue_group(plan) in ("ready", "replan"))
    report("changes-only-under-docs-plans", bool(changed) and all(p.startswith("docs/plans/") for p in changed))
    return 0


if __name__ == "__main__":
    sys.exit(main())
