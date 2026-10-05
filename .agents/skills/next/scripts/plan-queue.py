#!/usr/bin/env python3
"""List the plans under docs/plans/ that still have work, in the order to take them.

Usage: plan-queue.py [--json] [--large-units N]

Reads each plan's state file through plan_record.py, the plan's title and
unit headings, the status ledger of this worktree, the unmerged branches
that touch a plan, and the plans this branch changed. Prints one line per
plan with work left, grouped:

  land         implemented on this branch with an accepted review and a
               compound outcome, still on disk. land goes first, since it
               gates every plan this branch carries past main
  in-progress  partially-implemented, named by this worktree's ledger, or
               an unblocked phase of a parent that has landed phases
  unchecked    implemented, with a review verdict that is neither an accept
               nor rework, or implemented on this branch with no review or
               compound outcome
  replan       needs-decisions readiness, prerequisites finished, or an
               implemented plan whose review reads rework or that carries
               that readiness; the next step is the plan skill, not
               implement
  ready        planned, implementation-ready, every prerequisite finished
  waiting      a prerequisite phase is not finished by plan_record.py's
               test; names it
  retire       implemented, superseded, or abandoned on main and still on
               disk; land's retire step never ran for it. A parent whose
               phases have all landed reads implemented and ends here

Within a group the oldest plan comes first, by the date in its filename. A
plan another unmerged branch already changes is flagged
`elsewhere:<branch>`, since implementing it here would land it twice. A
plan counts as changed when its Markdown or its state file did.
`large` marks a plan over the unit threshold; a phase line names its parent
and how many phases the parent still has open.
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import re
import subprocess
import sys
from pathlib import Path

RECORD = Path(__file__).resolve().parents[2] / "plan/scripts/plan_record.py"
spec = importlib.util.spec_from_file_location("plan_record", RECORD)
plan_record = importlib.util.module_from_spec(spec)
spec.loader.exec_module(plan_record)

OPEN = {"planned", "partially-implemented"}
FINISHED = set(plan_record.FINAL)
ACCEPTED = set(plan_record.ACCEPTED)
UNIT = re.compile(r"^### U\d+[a-z]*[.:]")
ORDER = ["land", "in-progress", "unchecked", "replan", "ready", "waiting", "retire"]


def git(*args: str) -> str:
    out = subprocess.run(["git", *args], capture_output=True, text=True, check=False)
    return out.stdout.strip() if out.returncode == 0 else ""


def describe(text: str) -> tuple[str, int]:
    """The title, from the first `# ` heading, and the count of unit headings."""
    lines = text.splitlines()
    title = next((line[2:].strip() for line in lines if line.startswith("# ")), "")
    return title.removesuffix(" - Plan"), sum(1 for line in lines if UNIT.match(line))


def plans_in(paths: str) -> list[str]:
    """The plans a `git diff --name-only` listing touches, through either file."""
    return [plan for path in paths.splitlines() if (plan := plan_record.plan_of(path))]


def branches_touching_plans() -> dict[str, str]:
    touched: dict[str, str] = {}
    current = git("rev-parse", "--abbrev-ref", "HEAD")
    for branch in git("branch", "--no-merged", "main", "--format=%(refname:short)").splitlines():
        if branch == current:
            continue
        for plan in plans_in(git("diff", "--name-only", f"main...{branch}", "--", "docs/plans")):
            touched.setdefault(plan, branch)
    return touched


def read_plans(root: Path) -> dict[str, dict]:
    plans: dict[str, dict] = {}
    for path in sorted((root / "docs/plans").glob("*-plan.md")):
        rel = str(path.relative_to(root))
        title, units = describe(path.read_text(encoding="utf-8"))
        plans[rel] = {"state": plan_record.load(rel, root), "title": title, "units": units}
    return plans


def queue(root: Path, large_units: int) -> list[dict]:
    plans = read_plans(root)

    ledger_plan = None
    git_dir = git("rev-parse", "--git-dir")
    ledger = Path(git_dir) / plan_record.LEDGER_NAME if git_dir else None
    if ledger and ledger.is_file():
        try:
            ledger_plan = json.loads(ledger.read_text(encoding="utf-8")).get("plan")
        except (OSError, ValueError):
            ledger_plan = None

    elsewhere = branches_touching_plans()
    changed_here = set(plans_in(git("diff", "--name-only", "main...HEAD", "--", "docs/plans")))

    rows = []
    for rel, plan in plans.items():
        state = plan["state"]
        status = plan_record.computed_status(state)
        review = state["review"]
        readiness = state["readiness"]
        missing = [after for after in state["after"] if not plan_record.finished(after, root)]
        started_parent = False
        open_phases = 0
        if state["parent"]:
            holder = plan_record.load(state["parent"], root)
            siblings = [plan_record.load(path, root) for path in holder["phases"]]
            started_parent = any(s["landed"] for s in siblings) or any(e["landed"] for e in holder["retired"])
            open_phases = sum(1 for s in siblings if not s["landed"])

        if plan_record.sent_back(state):
            # A review that ended in rework sent the plan back to `plan`
            # and the status still reads implemented. No skill marks the
            # plan beyond the verdict, so the verdict is the signal, and
            # the readiness covers a re-plan that stopped on a decision.
            # plan-state.py applies the same test after its prerequisite
            # check. A parent's stored status is never implemented, so a
            # parent that kept the readiness it was planned with retires
            # below.
            group = "waiting" if missing else "replan"
        elif status in FINISHED and rel not in changed_here and not (review and review not in ACCEPTED):
            # Finished and on main, yet still on disk: land's retire step
            # never ran for it.
            group = "retire"
        elif state["phases"] or state["retired"]:
            # A parent is worked through its phases and shows up only to
            # retire.
            continue
        elif status == "implemented":
            # Either a review verdict is open or this branch changed the
            # plan. Reviewed and compounded on this branch, still on disk:
            # land has not run for it. plan-state.py prints `land`.
            unfinished = (review and review not in ACCEPTED) or (
                rel in changed_here and (not review or state["compound"] is None)
            )
            group = "unchecked" if unfinished else "land"
        elif status not in OPEN:
            continue
        elif missing:
            group = "waiting"
        elif status == "partially-implemented" or rel == ledger_plan:
            group = "in-progress"
        elif readiness == "needs-decisions":
            group = "replan"
        elif started_parent:
            group = "in-progress"
        else:
            group = "ready"
        rows.append(
            {
                "group": group,
                "path": rel,
                "title": plan["title"],
                "status": status,
                "readiness": readiness,
                "review": review,
                "compound": state["compound"],
                "units": plan["units"],
                "parent": state["parent"],
                "open_phases": open_phases,
                "waiting_on": missing,
                "elsewhere": elsewhere.get(rel),
                "ledger": rel == ledger_plan,
                "large": plan["units"] > large_units,
            }
        )

    rows.sort(key=lambda r: (ORDER.index(r["group"]), r["path"]))
    return rows


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--large-units", type=int, default=6)
    args = parser.parse_args()

    root = Path(git("rev-parse", "--show-toplevel") or ".")
    try:
        rows = queue(root, args.large_units)
    except plan_record.StateError as error:
        print(f"plan-queue: {error}", file=sys.stderr)
        return 1

    if args.json:
        json.dump(rows, sys.stdout, indent=2)
        print()
        return 0
    if not rows:
        print("No plan under docs/plans/ has work left.")
        return 0
    for row in rows:
        flags = [f"{row['units']} units"]
        if row["large"]:
            flags.append("large")
        if row["group"] == "unchecked":
            flags.append(f"review: {row['review'] or 'none'}; compound: {row['compound'] or 'none'}")
        if row["parent"]:
            flags.append(f"phase of {row['parent']}, {row['open_phases']} open")
        if row["ledger"]:
            flags.append("ledger here")
        if row["elsewhere"]:
            flags.append(f"elsewhere:{row['elsewhere']}")
        if row["waiting_on"]:
            flags.append("after " + ", ".join(row["waiting_on"]))
        print(f"{row['group']:<12} {row['path']}  [{'; '.join(flags)}]\n{'':<12} {row['title']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
