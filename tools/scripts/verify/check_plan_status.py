"""Validate the plan status ledger a worktree keeps in its git directory.

    run.py verify check-plan-status [LEDGER_PATH]

The ledger records which units of a plan have landed so that a later session
resumes without re-deriving progress. A ledger that is absent passes; one that
is present must match the shape `verify-change/SKILL.md` documents. Plan paths
resolve against the tree root.

A ledger naming a phase plan also proves the phase's prerequisites are in
this tree: every phase its state file lists under `after` carries a landed
range whose last commit is an ancestor of HEAD, and the integration branch
does not show this phase landed or retired already. The state is read
through `lib.plans`, the one module that knows its shape. A phase
re-planned in a worktree forked before the previous phase merged sees that
phase missing and builds it again; one phase of the network simulation was
implemented twice that way, on two branches, in one day.
"""

import json
import sys
from pathlib import Path

from lib import plans, proc


def git_path(flag: str) -> Path:
    done = proc.run(["git", "rev-parse", flag])
    if done.code != 0:
        fail(f"git rev-parse {flag} failed: {done.stderr.strip()}")
    return Path(done.stdout.strip())


def default_ledger_path() -> Path:
    return git_path("--git-dir") / plans.LEDGER_NAME


def fail(message: str) -> None:
    print(f"plan status ledger: {message}", file=sys.stderr)
    sys.exit(1)


def expect(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


def check_unit(index: int, unit: object) -> None:
    where = f"units[{index}]"
    expect(isinstance(unit, dict), f"{where} must be an object")
    assert isinstance(unit, dict)
    expect(isinstance(unit.get("id"), str) and unit["id"] != "", f"{where}.id must be a non-empty string")
    status = unit.get("status")
    expect(
        status in plans.UNIT_STATUSES,
        f"{where}.status must be one of {', '.join(plans.UNIT_STATUSES)}, got {status!r}",
    )
    for field in ("commit", "verified_at", "note", "base"):
        value = unit.get(field)
        expect(value is None or isinstance(value, str), f"{where}.{field} must be a string or null")
    if status == "passed":
        expect(bool(unit.get("commit")), f"{where}.commit must be set when status is passed")
        expect(bool(unit.get("verified_at")), f"{where}.verified_at must be set when status is passed")


def on_integration_branch(path: str) -> dict | None:
    """A state file as the integration branch holds it, or None.

    main is the branch; master is accepted for a checkout that still
    carries the old name. A file the branch does not hold (added on this
    branch, or retired there) is not an error, and no other branch is
    consulted.
    """
    for name in ("main", "master"):
        shown = proc.run(["git", "show", f"{name}:{path}"])
        if shown.code == 0:
            try:
                return json.loads(shown.stdout)
            except ValueError:
                return None
    return None


def stored(plan: str, root: Path) -> dict:
    try:
        return plans.load(plan, root)
    except plans.StateError as error:
        fail(str(error))
    raise AssertionError("unreachable")


def check_phase_ancestry(plan: str) -> None:
    root = git_path("--show-toplevel")
    state = stored(plan, root)
    parent_ref = state["parent"]
    if not parent_ref:
        return
    parent = stored(parent_ref, root)
    there = on_integration_branch(plans.state_path(plan)) or {}
    landed_there = there.get("landed")
    expect(
        not landed_there,
        f"phase {plan!r} already landed on the integration branch "
        f"({landed_there}): this worktree would implement it a second time",
    )
    # land deletes a phase's files once it lands, and the parent's retired
    # list is what the integration branch keeps of it.
    parent_there = on_integration_branch(plans.state_path(parent_ref)) or {}
    for entry in parent_there.get("retired", []):
        expect(
            entry.get("plan") != plan,
            f"phase {plan!r} is retired on the integration branch as {entry.get('status')}: "
            "this worktree would implement it a second time",
        )
    retired = {entry["plan"]: entry["landed"] for entry in parent["retired"]}
    for prerequisite in state["after"]:
        if (root / plans.state_path(prerequisite)).is_file():
            landed = stored(prerequisite, root)["landed"]
        else:
            landed = retired.get(prerequisite)
        expect(
            landed is not None,
            f"phase {prerequisite} has not landed, and plan {plan!r} runs after it",
        )
        assert landed is not None
        last = landed["last"]
        ancestor = proc.run(["git", "merge-base", "--is-ancestor", last, "HEAD"])
        expect(
            ancestor.code == 0,
            f"phase {prerequisite} landed at {last}, which is not in this tree: "
            "merge it, or start from a worktree that holds it",
        )


def check(ledger_path: Path) -> None:
    if not ledger_path.exists():
        return
    try:
        ledger = json.loads(ledger_path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        fail(f"{ledger_path} is not valid JSON: {error}")
    expect(isinstance(ledger, dict), "top level must be an object")
    expect(
        ledger.get("contract") == plans.LEDGER_CONTRACT,
        f"contract must be {plans.LEDGER_CONTRACT!r}, got {ledger.get('contract')!r}",
    )
    plan = ledger.get("plan")
    expect(isinstance(plan, str) and plan != "", "plan must be a non-empty string")
    expect((git_path("--show-toplevel") / plan).is_file(), f"plan {plan!r} does not exist")
    resume = ledger.get("resume")
    expect(
        isinstance(resume, list) and all(isinstance(item, str) for item in resume),
        "resume must be a list of unit ids",
    )
    units = ledger.get("units")
    expect(isinstance(units, list) and len(units) > 0, "units must be a non-empty list")
    for index, unit in enumerate(units):
        check_unit(index, unit)
    ids = {unit["id"] for unit in units}
    expect(len(ids) == len(units), "units must not repeat an id")
    for item in resume:
        expect(item in ids, f"resume names {item!r}, which is not a unit id")
    check_phase_ancestry(plan)


def main(argv: list[str]) -> int:
    if len(argv) > 1:
        print("usage: run.py verify check-plan-status [LEDGER_PATH]", file=sys.stderr)
        return 2
    check(Path(argv[0]) if argv else default_ledger_path())
    return 0
