#!/usr/bin/env python3
"""Validate the plan status ledger a worktree keeps in its git directory.

The ledger records which units of a plan have landed so that a later session
resumes without re-deriving progress. A ledger that is absent passes; one that
is present must match the shape `verify-change/SKILL.md` documents. Plan paths
resolve against the current directory, which the verifier sets to the tree root.
"""

from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

CONTRACT = "flowseer-plan-status/v1"
STATUSES = ("pending", "in_progress", "passed", "blocked")
LEDGER_NAME = "flowseer-plan-status.json"


def default_ledger_path() -> Path:
    git_dir = subprocess.run(
        ["git", "rev-parse", "--git-dir"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()
    return Path(git_dir) / LEDGER_NAME


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
    for field in ("commit", "verified_at", "note"):
        value = unit.get(field)
        expect(value is None or isinstance(value, str), f"{where}.{field} must be a string or null")
    if status == "passed":
        expect(bool(unit.get("commit")), f"{where}.commit must be set when status is passed")
        expect(bool(unit.get("verified_at")), f"{where}.verified_at must be set when status is passed")


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
    expect(Path(plan).is_file(), f"plan {plan!r} does not exist")
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
    for item in resume:
        expect(item in ids, f"resume names {item!r}, which is not a unit id")


def main(argv: list[str]) -> None:
    if len(argv) > 2:
        print("usage: check-plan-status.py [LEDGER_PATH]", file=sys.stderr)
        sys.exit(2)
    ledger_path = Path(argv[1]) if len(argv) == 2 else default_ledger_path()
    check(ledger_path)


if __name__ == "__main__":
    main(sys.argv)
