#!/usr/bin/env python3
"""Print where a parent plan's phases stand and the stage each needs next.

A session that drives a parent plan is interrupted, compacted, and resumed,
and its account of which phase is where drifts from the files. The stage is
therefore read from the parent's `Landed:` lines and each phase plan's
frontmatter, the same fields `implement`, `review`, `compound`, and `close`
write and gate on.

Usage:
    plan-state.py <parent plan>   the phases of one parent
    plan-state.py                 every plan under docs/plans/ still open
"""

import os
import re
import subprocess
import sys
from pathlib import Path

PLANS = Path("docs/plans")
UNIT = re.compile(r"^### (U\d+[a-z]?)[.:] (.*)$")
# Plans write a field bare (`Files: x`) or as a bold list item (`- **Files:** x`).
FIELD = re.compile(r"^(?:- )?\*{0,2}(Files|After|Landed):\*{0,2}\s*(.*)$")
# The same shape check-plan-status.py reads, so both agree on what landed.
COMMIT_RANGE = re.compile(r"\b[0-9a-f]{7,40}\.\.([0-9a-f]{7,40})\b")
PLAN_PATH = re.compile(r"docs/plans/\S+-plan\.md")
ACCEPTED = ("accept", "accept after fixes")


def frontmatter(path):
    lines = path.read_text().splitlines()
    if not lines or lines[0] != "---":
        return {}
    fields = {}
    for line in lines[1:]:
        if line == "---":
            break
        key, sep, value = line.partition(":")
        if sep:
            fields[key.strip()] = value.strip()
    return fields


def phases(parent):
    """The parent's units that name a phase plan, in file order."""
    found, unit = [], None
    lines = parent.read_text().splitlines()
    for index, line in enumerate(lines):
        heading = UNIT.match(line)
        if heading:
            unit = {"id": heading.group(1), "title": heading.group(2)}
            continue
        field = FIELD.match(line)
        if not field or unit is None:
            continue
        key, value = field.groups()
        if key == "Files":
            if not value and index + 1 < len(lines):
                value = lines[index + 1]
            plan = PLAN_PATH.search(value)
            if plan:
                unit["plan"] = Path(plan.group(0))
                found.append(unit)
        elif key == "After":
            unit["after"] = re.findall(r"U\d+[a-z]?", value)
        elif key == "Landed":
            unit["landed"] = value
    return found


def on_main(landed):
    """Whether the phase's last commit is on main, where close already gated it."""
    commit = COMMIT_RANGE.search(landed)
    if not commit:
        return False
    check = subprocess.run(
        ["git", "merge-base", "--is-ancestor", commit.group(1), "main"],
        capture_output=True,
        check=False,
    )
    return check.returncode == 0


def stage(unit, landed):
    """The next stage a phase needs, in the order the skills run."""
    if not unit["plan"].exists():
        return "plan (phase plan missing)"
    fields = frontmatter(unit["plan"])
    status = fields.get("status")
    if unit.get("landed"):
        # A phase on main was gated by close, whatever its plan says since;
        # phases older than the review and compound fields carry neither.
        if not COMMIT_RANGE.search(unit["landed"]):
            return "landed (no commit range)"
        if on_main(unit["landed"]):
            return "on main" if status == "implemented" else f"on main (status: {status})"
    waiting = [u for u in unit.get("after", []) if u not in landed]
    if waiting:
        return "waits for " + ", ".join(waiting)
    if fields.get("artifact_readiness") == "needs-decisions":
        return "plan"
    if status != "implemented":
        return "implement"
    if not unit.get("landed"):
        return "implement (Landed: empty in the parent)"
    if fields.get("review") not in ACCEPTED:
        return "review" if "review" not in fields else f"review (verdict: {fields['review']})"
    if "compound" not in fields:
        return "compound"
    return "done"


def report(parent):
    units = phases(parent)
    if not units:
        print(f"{parent}: no unit names a phase plan; not a parent plan")
        return 1
    landed = {u["id"] for u in units if u.get("landed")}
    print(f"{parent}  status: {frontmatter(parent).get('status', '?')}")
    stages = []
    for unit in units:
        stages.append(stage(unit, landed))
        print(f"  {unit['id']:<4} {stages[-1]:<28}  {unit['plan']}")
        if unit.get("landed"):
            print(f"      Landed: {unit['landed']}")
    settled = ("done", "on main", "waits", "landed")
    ready = [u["id"] for u, s in zip(units, stages) if not s.startswith(settled)]
    # Only a phase that finished on this branch leaves something to land.
    rest = "close" if "done" in stages else "nothing"
    print("next: " + (", ".join(ready) if ready else rest))
    return 0


def open_plans():
    children = set()
    parents = []
    for path in sorted(PLANS.glob("*-plan.md")):
        units = phases(path)
        if units:
            parents.append(path)
            children.update(u["plan"] for u in units)
    for path in sorted(PLANS.glob("*-plan.md")):
        status = frontmatter(path).get("status", "?")
        if status in ("implemented", "superseded", "abandoned") or path in children:
            continue
        kind = "parent" if path in parents else "plan"
        print(f"{status:<22} {kind:<7} {path}")
    return 0


if __name__ == "__main__":
    if len(sys.argv) > 2 or sys.argv[1:] in (["-h"], ["--help"]):
        print(__doc__.strip())
        sys.exit(0 if len(sys.argv) == 2 else 2)
    parent = Path(sys.argv[1]).resolve() if len(sys.argv) == 2 else None
    # Plans name each other by repository-relative path, so read from the root.
    root = subprocess.run(
        ["git", "rev-parse", "--show-toplevel"], capture_output=True, text=True, check=True
    ).stdout.strip()
    os.chdir(root)
    if parent is None:
        sys.exit(open_plans())
    if not parent.is_file():
        print(f"{sys.argv[1]}: not a plan file", file=sys.stderr)
        sys.exit(2)
    sys.exit(report(parent.relative_to(Path(root).resolve())))
