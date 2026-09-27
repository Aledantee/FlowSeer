#!/usr/bin/env python3
"""List the plans under docs/plans/ that still have work, in the order to take them.

Usage: plan-queue.py [--json] [--large-units N]

Reads plan frontmatter, unit headings, and a parent plan's phase lines
(After/Landed), the status ledger of this worktree, the unmerged branches
that touch a plan, and the plans this branch changed. Prints one line per
plan with work left, grouped:

  in-progress  partially-implemented, named by this worktree's ledger, or an
               unblocked phase of a parent that has landed phases
  unchecked    implemented, with a review verdict that is not an accept, or
               implemented on this branch with no review or compound field
  replan       artifact_readiness needs-decisions, prerequisites landed; the
               next step is the plan skill, not implement
  ready        planned, implementation-ready, every prerequisite landed
  waiting      a prerequisite phase has not landed; names it
  stale        a parent still `planned` whose phases have all landed

Within a group the oldest plan comes first, by the date in its filename. A
plan another unmerged branch already changes is flagged
`elsewhere:<branch>`, since implementing it here would land it twice.
`large` marks a plan over the unit threshold; a phase line names its parent
and how many phases the parent still has open.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

OPEN = {"planned", "partially-implemented"}
ACCEPTED = {"accept", "accept after fixes"}
UNIT = re.compile(r"^### (U\d+[a-z]*)[.:]\s*(.*)$")
UNIT_ID = re.compile(r"U\d+[a-z]*")
PLAN_PATH = re.compile(r"docs/plans/[\w.-]+-plan\.md")
FIELD = re.compile(r"^(?:- )?\*{0,2}([A-Z][A-Za-z ]+):\*{0,2}")
COMMIT_RANGE = re.compile(r"[0-9a-f]{7,}")
ORDER = ["in-progress", "unchecked", "replan", "ready", "waiting", "stale"]


def git(*args: str) -> str:
    out = subprocess.run(["git", *args], capture_output=True, text=True, check=False)
    return out.stdout.strip() if out.returncode == 0 else ""


def frontmatter(text: str) -> dict[str, str]:
    if not text.startswith("---\n"):
        return {}
    end = text.find("\n---", 4)
    fields = {}
    for line in text[4:end].splitlines():
        key, sep, value = line.partition(":")
        if sep and not key.startswith(" "):
            fields[key.strip()] = value.strip()
    return fields


def units(text: str) -> list[dict]:
    """Units in either format the plans use: `### U1. Name` with `Files:`
    lines, or `### U1: Name` with `- **Files:**` bullets. A field runs from
    its line to the next field, blank line, or heading, so a value on the
    following line still reads, as check-plan-status.py reads it. `plans`
    holds every plan path the unit's block names."""
    found: list[dict] = []
    unit: dict | None = None
    field = ""
    for line in text.splitlines():
        match = UNIT.match(line)
        if match:
            unit = {"id": match.group(1), "name": match.group(2), "plans": [], "after": [], "landed": False}
            found.append(unit)
            field = ""
            continue
        if line.startswith("#"):
            unit = None  # a later section's prose is no part of the last unit
            continue
        if unit is None:
            continue
        unit["plans"] += PLAN_PATH.findall(line)
        label = FIELD.match(line)
        if label:
            field = label.group(1)
        elif not line.strip():
            field = ""
        value = line.split(":", 1)[1] if label else line
        if field == "After":
            unit["after"] += UNIT_ID.findall(value)
        elif field == "Landed" and COMMIT_RANGE.search(value):
            unit["landed"] = True
    return found


def branches_touching_plans() -> dict[str, str]:
    touched: dict[str, str] = {}
    current = git("rev-parse", "--abbrev-ref", "HEAD")
    for branch in git("branch", "--no-merged", "main", "--format=%(refname:short)").splitlines():
        if branch == current:
            continue
        for path in git("diff", "--name-only", f"main...{branch}", "--", "docs/plans").splitlines():
            touched.setdefault(path, branch)
    return touched


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--large-units", type=int, default=6)
    args = parser.parse_args()

    root = Path(git("rev-parse", "--show-toplevel") or ".")
    plans: dict[str, dict] = {}
    for path in sorted((root / "docs/plans").glob("*-plan.md")):
        text = path.read_text(encoding="utf-8")
        rel = str(path.relative_to(root))
        plans[rel] = {"path": rel, "fm": frontmatter(text), "units": units(text)}

    ledger_plan = None
    git_dir = git("rev-parse", "--git-dir")
    ledger = Path(git_dir) / "flowseer-plan-status.json" if git_dir else None
    if ledger and ledger.is_file():
        try:
            ledger_plan = json.loads(ledger.read_text(encoding="utf-8")).get("plan")
        except (OSError, ValueError):
            ledger_plan = None

    elsewhere = branches_touching_plans()
    changed_here = set(git("diff", "--name-only", "main...HEAD", "--", "docs/plans").splitlines())

    # Several units of a parent may name one phase plan; the phase waits on
    # the After lines of all of them and has landed when all of them have.
    phase_of: dict[str, tuple[dict, list[dict]]] = {}
    for plan in plans.values():
        for unit in plan["units"]:
            for named in unit["plans"]:
                if named in plans and plans[named]["fm"].get("parent") == plan["path"]:
                    phase_of.setdefault(named, (plan, []))[1].append(unit)
    parents = {parent["path"] for parent, _ in phase_of.values()}

    rows = []
    for rel, plan in plans.items():
        fm = plan["fm"]
        status = fm.get("status", "")
        review = fm.get("review", "")
        readiness = fm.get("artifact_readiness", "")
        missing: list[str] = []
        started_parent = False
        open_phases = 0
        if rel in phase_of:
            parent, own = phase_of[rel]
            by_id = {u["id"]: u for u in parent["units"]}
            own_ids = {u["id"] for u in own}
            for unit in own:
                for after in unit["after"]:
                    if after in by_id and after not in own_ids and not by_id[after]["landed"]:
                        named = by_id[after]["plans"][:1]
                        missing.append(f"{after} ({named[0]})" if named else after)
            started_parent = any(u["landed"] for u in parent["units"])
            open_phases = len({p for u in parent["units"] if not u["landed"] for p in u["plans"][:1]})

        if rel in parents:
            # A parent is worked through its phases; it shows up only when
            # they have all landed and its own status was never set.
            phase_units = [u for u in plan["units"] if any(p in phase_of for p in u["plans"])]
            if status in OPEN and phase_units and all(u["landed"] for u in phase_units):
                group = "stale"
            else:
                continue
        elif status == "implemented":
            unfinished = (review and review not in ACCEPTED) or (
                rel in changed_here and (not review or "compound" not in fm)
            )
            if not unfinished:
                continue
            group = "unchecked"
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
                "title": fm.get("title", "").removesuffix(" - Plan"),
                "status": status,
                "readiness": readiness,
                "review": review or None,
                "compound": fm.get("compound"),
                "units": len(plan["units"]),
                "parent": fm.get("parent"),
                "open_phases": open_phases,
                "waiting_on": missing,
                "elsewhere": elsewhere.get(rel),
                "ledger": rel == ledger_plan,
                "large": len(plan["units"]) > args.large_units,
            }
        )

    rows.sort(key=lambda r: (ORDER.index(r["group"]), r["path"]))

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
