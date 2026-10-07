"""List the plans under docs/plans/ that still have work, in the order to take them.

Usage: run.py next plan-queue [--json] [--large-units N]

Reads each plan's state file through `lib.plans`, from the plan's
recorded branch while that branch is not merged here, the plan's title and
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
               that readiness. The next step is the plan skill, not
               implement
  ready        planned, implementation-ready, every prerequisite finished
  waiting      a prerequisite phase is not finished by `lib.plans`'s
               test. The line names it
  retire       implemented, superseded, or abandoned on main and still on
               disk. land's retire step never ran for it. A parent reads
               implemented once none of its phases is left on disk and a
               retired one carries a landed range, and ends here

Within a group a harness plan comes first, then the oldest plan, by the
date in its filename. A harness plan is one whose units' `Files:` lines name
the skills, the agent runtime configuration, the hooks, the host-side
scripts, or the workflow documents (`HARNESS` below), and beside those only
files under `docs/` and files at the repository root: a change to the
workflow is worked before the product work that would run on the old one.

A plan that names a file another plan with work left also names is printed
after that plan when the other is further along (owed a land, then
unchecked, in progress, re-plan, ready) or as far along and older. It is
flagged `shares files with <plan>`, since work started on the earlier state
of a shared file is done twice. `waiting` and `retire` lines keep their
place.

A plan another unmerged branch already changes, or another worktree's
ledger names, is flagged `elsewhere:<branch>`, since implementing it here
would land it twice. A plan counts as changed when its Markdown or its
state file did. The last line names the branches the other worktrees have
checked out, for the reader to compare with the candidates: a session that
has not committed yet shows up nowhere else.
`branch:<name>` names the unmerged branch a plan's state was read from: its
work is there, not in this checkout.
`large` marks a plan over the unit threshold. A phase line names its parent
and how many phases the parent still has open.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

from lib import plans, proc

OPEN = {"planned", "partially-implemented"}
FINISHED = set(plans.FINAL)
ACCEPTED = set(plans.ACCEPTED)
ORDER = ["land", "in-progress", "unchecked", "replan", "ready", "waiting", "retire"]
# How far along a plan with work left is, furthest first. Two plans that
# share a file are taken in this order whatever their groups' places.
PROGRESS = ["land", "unchecked", "in-progress", "replan", "ready"]
HARNESS = (".agents/", ".claude/", ".codex/", "tools/hooks/", "tools/scripts/", "docs/agent-", "AGENTS.md", "CLAUDE.md")
UNIT_KEY = re.compile(r"^(After|Change|Tests|Verify):")
PATH = re.compile(r"[\w@.*/-]*[\w*/]")


def git(*args: str) -> str:
    out = proc.run(["git", *args])
    return out.stdout.strip() if out.code == 0 else ""


def describe(text: str) -> tuple[str, int, set[str]]:
    """The title, from the first `# ` heading, the count of unit headings,
    and the paths the units' `Files:` lines name."""
    lines = text.splitlines()
    files: set[str] = set()
    listing = False
    for line in lines:
        if line.startswith("Files:"):
            listing, line = True, line.removeprefix("Files:")
        elif not line.strip() or line.startswith("#") or UNIT_KEY.match(line):
            listing = False
        if listing:
            # A list item is a path, sometimes followed by a note in
            # parentheses. Only its first word is the path.
            for item in line.replace("`", "").split(","):
                word = item.split()[0] if item.split() else ""
                if PATH.fullmatch(word) and ("/" in word or "." in word):
                    files.add(word)
    return plans.title(text), len(plans.unit_ids(text)), files


def is_harness(files: set[str]) -> bool:
    """Whether a plan changes the workflow and no product code."""
    beside = [path for path in files if not path.startswith(HARNESS)]
    return len(beside) < len(files) and all(path.startswith("docs/") or "/" not in path for path in beside)


def overlap(ours: set[str], theirs: set[str]) -> bool:
    """Whether two plans name a common file, a directory standing for
    everything under it."""

    def within(path: str, others: set[str]) -> bool:
        return any(path == other or (other.endswith("/") and path.startswith(other)) for other in others)

    return any(within(path, theirs) for path in ours) or any(within(path, ours) for path in theirs)


def other_worktrees() -> list[tuple[str, str | None]]:
    """Each other worktree's branch and the plan its ledger names.

    Read from the files under the shared git directory, since a session
    isolated in its worktree may run no git command that names another
    checkout."""
    common, here = git("rev-parse", "--git-common-dir"), git("rev-parse", "--absolute-git-dir")
    if not common or not here:
        return []
    common_dir = Path(common).resolve()
    found = []
    for git_dir in [common_dir, *sorted((common_dir / "worktrees").glob("*"))]:
        if git_dir == Path(here).resolve():
            continue
        try:
            # A checkout deleted without `git worktree prune` leaves its
            # entry and its ledger behind, and nobody is working there.
            pointer = git_dir / "gitdir"
            if pointer.is_file() and not Path(pointer.read_text(encoding="utf-8").strip()).exists():
                continue
            head = (git_dir / "HEAD").read_text(encoding="utf-8").strip()
        except OSError:
            continue
        if not head.startswith("ref: refs/heads/"):
            continue
        try:
            plan = json.loads((git_dir / plans.LEDGER_NAME).read_text(encoding="utf-8")).get("plan")
        except (OSError, ValueError, AttributeError):
            plan = None
        found.append((head.removeprefix("ref: refs/heads/"), plan if isinstance(plan, str) else None))
    return found


def plans_in(paths: str) -> list[str]:
    """The plans a `git diff --name-only` listing touches, through either file."""
    return [plan for path in paths.splitlines() if (plan := plans.plan_of(path))]


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
    entries: dict[str, dict] = {}
    for path in sorted((root / "docs/plans").glob("*-plan.md")):
        rel = str(path.relative_to(root))
        title, units, files = describe(path.read_text(encoding="utf-8"))
        entries[rel] = {"state": plans.followed(rel, root), "title": title, "units": units, "files": files}
    return entries


def queue(root: Path, large_units: int) -> list[dict]:
    entries = read_plans(root)

    ledger_plan = None
    git_dir = git("rev-parse", "--git-dir")
    ledger = Path(git_dir) / plans.LEDGER_NAME if git_dir else None
    if ledger and ledger.is_file():
        try:
            ledger_plan = json.loads(ledger.read_text(encoding="utf-8")).get("plan")
        except (OSError, ValueError):
            ledger_plan = None

    elsewhere = branches_touching_plans()
    for branch, plan in other_worktrees():
        if plan:
            elsewhere.setdefault(plan, branch)
    changed_here = set(plans_in(git("diff", "--name-only", "main...HEAD", "--", "docs/plans")))
    # A plan read from its branch is this branch's work, merged here or not.
    unmerged = {rel: plans.branch_to_follow(plan["state"], root) for rel, plan in entries.items()}
    changed_here.update(rel for rel, branch in unmerged.items() if branch)

    rows = []
    for rel, plan in entries.items():
        state = plan["state"]
        status = plans.computed_status(state)
        review = state["review"]
        readiness = state["readiness"]
        missing = [after for after in state["after"] if not plans.finished(after, root)]
        started_parent = False
        open_phases = 0
        if state["parent"]:
            holder = plans.load(state["parent"], root)
            siblings = [plans.followed(path, root) for path in holder["phases"]]
            started_parent = any(s["landed"] for s in siblings) or any(e["landed"] for e in holder["retired"])
            open_phases = sum(1 for s in siblings if not s["landed"])

        if plans.sent_back(state):
            # A review that ended in rework sent the plan back to `plan`
            # and the status still reads implemented. No skill marks the
            # plan beyond the verdict, so the verdict is the signal, and
            # the readiness covers a re-plan that stopped on a decision.
            # `drive plan-state` applies the same test after its prerequisite
            # check. A parent's stored status is never implemented, so a
            # parent that kept the readiness it was planned with retires
            # below.
            group = "waiting" if missing else "replan"
        elif (
            status in FINISHED
            and rel not in changed_here
            and (status != "implemented" or not review or review in ACCEPTED)
        ):
            # Finished and on main, yet still on disk: land's retire step
            # never ran for it. A superseded or abandoned plan retires
            # whatever its review says. An implemented one needs an
            # accepted review or none.
            group = "retire"
        elif state["phases"] or state["retired"]:
            # A parent is worked through its phases and shows up only to
            # retire.
            continue
        elif status == "implemented":
            # Either a review verdict is open or this branch changed the
            # plan. Reviewed and compounded on this branch, still on disk:
            # land has not run for it. `drive plan-state` prints `land`.
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
                "branch": unmerged[rel],
                "ledger": rel == ledger_plan,
                "large": plan["units"] > large_units,
                "harness": is_harness(plan["files"]),
                "shares_files_with": [],
            }
        )

    rows.sort(key=lambda r: (ORDER.index(r["group"]), not r["harness"], r["path"]))

    def ahead(row: dict) -> tuple[int, str]:
        return PROGRESS.index(row["group"]), row["path"]

    active = [row for row in rows if row["group"] in PROGRESS]
    for row in active:
        row["shares_files_with"] = sorted(
            other["path"]
            for other in active
            if ahead(other) < ahead(row) and overlap(entries[row["path"]]["files"], entries[other["path"]]["files"])
        )
    # A row is printed once every plan it shares files with and trails is
    # printed. `ahead` is a strict order, so the loop always places a row.
    placed: list[dict] = []
    while len(placed) < len(active):
        done = {row["path"] for row in placed}
        placed.append(
            next(row for row in active if row["path"] not in done and done.issuperset(row["shares_files_with"]))
        )
    return placed + [row for row in rows if row["group"] not in PROGRESS]


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py next plan-queue", description=__doc__.splitlines()[0])
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--large-units", type=int, default=6)
    args = parser.parse_args(argv)

    root = Path(git("rev-parse", "--show-toplevel") or ".")
    try:
        rows = queue(root, args.large_units)
    except plans.StateError as error:
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
        if row["harness"]:
            flags.append("harness")
        if row["group"] == "unchecked":
            flags.append(f"review: {row['review'] or 'none'}; compound: {row['compound'] or 'none'}")
        if row["parent"]:
            flags.append(f"phase of {row['parent']}, {row['open_phases']} open")
        if row["ledger"]:
            flags.append("ledger here")
        if row["elsewhere"]:
            flags.append(f"elsewhere:{row['elsewhere']}")
        if row["branch"]:
            flags.append(f"branch:{row['branch']}")
        if row["shares_files_with"]:
            flags.append("shares files with " + ", ".join(row["shares_files_with"]))
        if row["waiting_on"]:
            flags.append("after " + ", ".join(row["waiting_on"]))
        print(f"{row['group']:<12} {row['path']}  [{'; '.join(flags)}]\n{'':<12} {row['title']}")
    branches = [branch for branch, _ in other_worktrees()]
    if branches:
        print("other worktrees: " + ", ".join(branches))
    return 0
