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


def run_py(*args: str) -> subprocess.CompletedProcess:
    return subprocess.run(["uv", "run", "tools/scripts/run.py", *args], capture_output=True, text=True, check=False)


def units_hold_files_and_after(text: str) -> bool:
    """Whether every "### U<n>." section holds a Files: line and an After: line."""
    units = 0
    in_unit = files = after = False
    for line in [*text.splitlines(), "## end"]:
        if re.match(r"##+ ", line):
            if in_unit:
                units += 1
                if not (files and after):
                    return False
            in_unit = False
        if re.match(r"### U[0-9]+\.", line):
            in_unit, files, after = True, False, False
        elif in_unit and line.startswith("Files:"):
            files = True
        elif in_unit and line.startswith("After:"):
            after = True
    return units > 0


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
    new_plans = sorted(path for path in added if re.fullmatch(r"docs/plans/.*-plan\.md", path))

    plan = new_plans[0] if len(new_plans) == 1 else ""
    report("one-new-plan", bool(plan))
    text = read(plan) if plan else ""
    lines = text.splitlines()

    report("artifact-contract", "artifact_contract: flowseer-plan/v2" in lines)

    status = None
    if plan:
        try:
            status = json.loads(read(plan.removesuffix(".md") + ".state.json")).get("status")
        except (ValueError, AttributeError):
            pass
    report("state-planned", status == "planned")

    report("unit-u1", any(line.startswith("### U1.") for line in lines))
    report("files-and-after-per-unit", units_hold_files_and_after(text))
    report("waves-line", any(line.startswith("Waves:") for line in lines))
    report("check-prose-clean", bool(text) and run_py("verify", "check-prose", plan).returncode == 0)

    group = None
    if plan:
        try:
            queue = json.loads(run_py("next", "plan-queue", "--json").stdout)
            group = next((entry.get("group") for entry in queue if entry.get("path") == plan), None)
        except (ValueError, AttributeError, TypeError):
            pass
    report("plan-queue-ready", group in ("ready", "replan"))

    report("changes-only-under-docs-plans", bool(changed) and all(path.startswith("docs/plans/") for path in changed))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
