"""Print where a parent plan's phases stand and the stage each needs next.

A session that drives a parent plan is interrupted, compacted, and resumed,
and its account of which phase is where drifts from the files. The stage is
therefore read from the state files `plan record` keeps beside the plans:
the parent's phase lists and each phase's status, review, and compound
outcome, the same fields `implement`, `review`, `compound`, and `land`
write and gate on. A phase whose recorded branch is not merged here is read
from that branch, where its stage workers write, and the branch is printed
under it.

Usage:
    run.py drive plan-state <parent plan>   the phases of one parent
    run.py drive plan-state                 every plan under docs/plans/ still open
"""

import sys
from pathlib import Path

from lib import plans, repo


def retired_stage(entry, root):
    """The stage of a phase `retire` deleted: only its range remains of it."""
    if not entry["landed"]:
        return f"retired ({entry['status']})"
    # land deletes a phase plan once the phase lands; the parent's retired
    # entry is what remains of it.
    return "on main (plan retired)" if plans.on_main(entry["landed"]["last"], root) else "done (plan retired)"


def stage(plan, root):
    """The next stage a phase needs, in the order the skills run."""
    state = plans.followed(plan, root)
    if state["landed"] and plans.on_main(state["landed"]["last"], root):
        # A phase on main was gated by land, whatever its review says since;
        # phases older than the review and compound fields carry neither.
        return "on main"
    # A landed range records implementation. Dependents wait for review and
    # compound too, so a fix round cannot rewrite files they are editing.
    waiting = [path for path in state["after"] if not plans.finished(path, root)]
    if waiting:
        return "waits for " + ", ".join(waiting)
    if state["readiness"] == "needs-decisions" or plans.sent_back(state):
        # A rework verdict sends the plan back to `plan`, and no skill
        # marks it beyond the verdict. `next plan-queue` reads it the same way.
        return "plan"
    if state["status"] != "implemented":
        return "implement"
    if state["review"] not in plans.ACCEPTED:
        return "review" if state["review"] is None else f"review (verdict: {state['review']})"
    if state["compound"] is None:
        return "compound"
    # land retires the plan, so a finished phase still on disk is owed one.
    return "land"


def report(parent, root):
    state = plans.load(parent, root)
    if not (state["phases"] or state["retired"]):
        print(f"{parent}: no phase is listed; not a parent plan")
        return 1
    # Retired phases landed first, so they come before the ones still on disk.
    rows = [(retired_stage(entry, root), entry["plan"], entry["landed"]) for entry in state["retired"]]
    unmerged = {}
    for plan in state["phases"]:
        phase = plans.followed(plan, root)
        unmerged[plan] = plans.branch_to_follow(phase, root)
        rows.append((stage(plan, root), plan, phase["landed"]))
    print(f"{parent}  status: {plans.computed_status(state)}")
    for phase_stage, plan, landed in rows:
        print(f"  {phase_stage:<28}  {plan}")
        if unmerged.get(plan):
            print(f"      branch: {unmerged[plan]}")
        if landed:
            print(f"      landed: {landed['first']}..{landed['last']}")
    settled = ("land", "done", "on main", "waits", "retired")
    ready = [plan for phase_stage, plan, _ in rows if not phase_stage.startswith(settled)]
    # A phase owed a land goes before any new stage, since land gates every
    # plan this branch carries past main. A phase implemented here holds
    # that land until its review and compound are done. So does a phase at
    # any stage on a branch not merged here: it keeps its child worktree
    # until then, and land refuses while one remains. A parked phase holds
    # nothing: its worktree is gone and its work is off this branch.
    owed = [plan for phase_stage, plan, _ in rows if phase_stage == "land"]
    holding = [
        plan
        for phase_stage, plan, _ in rows
        if plan not in owed
        and (
            not unmerged[plan].startswith("parked/")
            if unmerged.get(plan)
            else phase_stage.startswith(("review", "compound"))
        )
    ]
    if owed:
        print("next: land " + ", ".join(owed) + (" after " + ", ".join(holding) if holding else ""))
    else:
        print("next: " + (", ".join(ready) if ready else "nothing"))
    return 0


def open_plans(root):
    listed = sorted(f"{plans.PLANS}/{path.name}" for path in (root / plans.PLANS).glob("*-plan.md"))
    for plan in listed:
        state = plans.followed(plan, root)
        status = plans.computed_status(state)
        if status in plans.FINAL or state["parent"]:
            continue
        kind = "parent" if state["phases"] or state["retired"] else "plan"
        print(f"{status:<22} {kind:<7} {plan}")
    return 0


def main(argv: list[str]) -> int:
    if len(argv) > 1 or argv in (["-h"], ["--help"]):
        print(__doc__.strip())
        return 0 if len(argv) == 1 else 2
    # Plans name each other by repository-relative path, so read from the root.
    try:
        root = repo.root().resolve()
    except RuntimeError as error:
        print(f"plan-state: {error}", file=sys.stderr)
        return 2
    parent = Path(argv[0]).resolve() if argv else None
    try:
        if parent is None:
            return open_plans(root)
        if not parent.is_file():
            print(f"{argv[0]}: not a plan file", file=sys.stderr)
            return 2
        return report(parent.relative_to(root).as_posix(), root)
    except plans.StateError as error:
        print(f"plan-state: {error}", file=sys.stderr)
        return 2
