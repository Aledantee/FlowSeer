#!/usr/bin/env python3
"""Validate the plan status ledger a worktree keeps in its git directory.

The ledger records which units of a plan have landed so that a later session
resumes without re-deriving progress. A ledger that is absent passes; one that
is present must match the shape `verify-change/SKILL.md` documents. Plan paths
resolve against the tree root.

A ledger naming a phase plan also proves the phase's prerequisites are in
this tree: every phase its state file lists under `after` carries a landed
range whose last commit is an ancestor of HEAD, and the integration branch
does not show this phase landed or retired already. The state is read
through `plan_record.py`, the one module that knows its shape. A phase
re-planned in a worktree forked before the previous phase merged sees that
phase missing and builds it again; one phase of the network simulation was
implemented twice that way, on two branches, in one day.
"""

from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
from pathlib import Path

CONTRACT = "flowseer-plan-status/v1"
STATUSES = ("pending", "in_progress", "passed", "blocked")
LEDGER_NAME = "flowseer-plan-status.json"

_spec = importlib.util.spec_from_file_location(
    "plan_record", Path(__file__).resolve().parents[2] / "plan/scripts/plan_record.py"
)
plan_record = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(plan_record)


def git_path(flag: str) -> Path:
    output = subprocess.run(
        ["git", "rev-parse", flag],
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()
    return Path(output)


def default_ledger_path() -> Path:
    return git_path("--git-dir") / LEDGER_NAME


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
        status in STATUSES,
        f"{where}.status must be one of {', '.join(STATUSES)}, got {status!r}",
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
        shown = subprocess.run(["git", "show", f"{name}:{path}"], capture_output=True, text=True)
        if shown.returncode == 0:
            try:
                return json.loads(shown.stdout)
            except ValueError:
                return None
    return None


def stored(plan: str, root: Path) -> dict:
    try:
        return plan_record.load(plan, root)
    except plan_record.StateError as error:
        fail(str(error))
    raise AssertionError("unreachable")


def check_phase_ancestry(plan: str) -> None:
    root = git_path("--show-toplevel")
    state = stored(plan, root)
    parent_ref = state["parent"]
    if not parent_ref:
        return
    parent = stored(parent_ref, root)
    there = on_integration_branch(plan_record.state_path(plan)) or {}
    landed_there = there.get("landed")
    expect(
        not landed_there,
        f"phase {plan!r} already landed on the integration branch "
        f"({landed_there}): this worktree would implement it a second time",
    )
    # land deletes a phase's files once it lands, and the parent's retired
    # list is what the integration branch keeps of it.
    parent_there = on_integration_branch(plan_record.state_path(parent_ref)) or {}
    for entry in parent_there.get("retired", []):
        expect(
            entry.get("plan") != plan,
            f"phase {plan!r} is retired on the integration branch as {entry.get('status')}: "
            "this worktree would implement it a second time",
        )
    retired = {entry["plan"]: entry["landed"] for entry in parent["retired"]}
    for prerequisite in state["after"]:
        if (root / plan_record.state_path(prerequisite)).is_file():
            landed = stored(prerequisite, root)["landed"]
        else:
            landed = retired.get(prerequisite)
        expect(
            landed is not None,
            f"phase {prerequisite} has not landed, and plan {plan!r} runs after it",
        )
        assert landed is not None
        last = landed["last"]
        ancestor = subprocess.run(
            ["git", "merge-base", "--is-ancestor", last, "HEAD"],
            capture_output=True,
        )
        expect(
            ancestor.returncode == 0,
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
    expect(ledger.get("contract") == CONTRACT, f"contract must be {CONTRACT!r}, got {ledger.get('contract')!r}")
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


def main(argv: list[str]) -> None:
    if len(argv) > 2:
        print("usage: check-plan-status.py [LEDGER_PATH]", file=sys.stderr)
        sys.exit(2)
    ledger_path = Path(argv[1]) if len(argv) == 2 else default_ledger_path()
    check(ledger_path)


if __name__ == "__main__":
    main(sys.argv)
