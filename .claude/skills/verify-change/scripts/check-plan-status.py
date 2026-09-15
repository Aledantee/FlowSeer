#!/usr/bin/env python3
"""Validate the plan status ledger a worktree keeps in its git directory.

The ledger records which units of a plan have landed so that a later session
resumes without re-deriving progress. A ledger that is absent passes; one that
is present must match the shape `verify-change/SKILL.md` documents. Plan paths
resolve against the tree root.

A ledger naming a phase plan also proves the phase's prerequisites are in
this tree: every phase the parent's `After:` line names has a `Landed:`
line carrying a `<first>..<last>` commit range whose last commit is an
ancestor of HEAD, and the parent on the integration branch does not show
this phase landed already. A phase re-planned in a
worktree forked before the previous phase merged sees that phase missing
and builds it again; one phase of the network simulation was implemented
twice that way, on two branches, in one day.
"""

from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path

CONTRACT = "flowseer-plan-status/v1"
STATUSES = ("pending", "in_progress", "passed", "blocked")
LEDGER_NAME = "flowseer-plan-status.json"
UNIT_HEADING = re.compile(r"^###\s+(U\d+[a-z]?)[.:]\s")
FIELD = re.compile(r"^(?:-\s+)?\**(Files|After|Landed)\**:\**\s*(.*)$")
LANDED_RANGE = re.compile(r"\b([0-9a-f]{7,40})\.\.([0-9a-f]{7,40})\b")


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
    for field in ("commit", "verified_at", "note"):
        value = unit.get(field)
        expect(value is None or isinstance(value, str), f"{where}.{field} must be a string or null")
    if status == "passed":
        expect(bool(unit.get("commit")), f"{where}.commit must be set when status is passed")
        expect(bool(unit.get("verified_at")), f"{where}.verified_at must be set when status is passed")


def frontmatter_field(text: str, name: str) -> str | None:
    if not text.startswith("---"):
        return None
    for line in text.split("\n---", 1)[0].splitlines():
        key, sep, value = line.partition(":")
        if sep and key.strip() == name:
            return value.strip()
    return None


def parent_units_text(text: str) -> dict[str, dict[str, str]]:
    """Map each unit of a parent plan to its Files, After, and Landed text.

    A field runs from its line to the next field or heading, so a `Files:`
    whose path sits on the following line still reads.
    """
    units: dict[str, dict[str, str]] = {}
    current = None
    field = None
    for line in text.splitlines():
        heading = UNIT_HEADING.match(line)
        if heading:
            current = heading.group(1)
            units[current] = {}
            field = None
            continue
        if current is None:
            continue
        match = FIELD.match(line.strip())
        if match:
            field = match.group(1)
            units[current][field] = match.group(2).strip()
        elif field and line.startswith((" ", "\t")):
            units[current][field] = (units[current][field] + " " + line.strip()).strip()
        elif line.strip() == "" or line.startswith("- "):
            field = None
    return units


def landed_on_integration_branch(parent_ref: str) -> str | None:
    """The parent plan as the integration branch holds it, or None.

    main is the branch; master is accepted for a checkout that still
    carries the old name. A parent the branch does not hold yet (added on
    this branch) is not an error, and no other branch is consulted.
    """
    for name in ("main", "master"):
        shown = subprocess.run(["git", "show", f"{name}:{parent_ref}"], capture_output=True, text=True)
        if shown.returncode == 0:
            return shown.stdout
    return None


def check_phase_ancestry(plan: str) -> None:
    root = git_path("--show-toplevel")
    parent_ref = frontmatter_field((root / plan).read_text(encoding="utf-8"), "parent")
    if not parent_ref:
        return
    parent = root / parent_ref
    expect(parent.is_file(), f"parent {parent_ref!r} of plan {plan!r} does not exist")
    units = parent_units_text(parent.read_text(encoding="utf-8"))
    this = [uid for uid, fields in units.items() if plan in fields.get("Files", "")]
    expect(len(this) == 1, f"parent {parent_ref!r} must name plan {plan!r} in exactly one unit's Files")
    on_branch = landed_on_integration_branch(parent_ref)
    if on_branch is not None:
        landed_there = parent_units_text(on_branch).get(this[0], {}).get("Landed", "")
        expect(
            landed_there == "",
            f"phase {this[0]} of {parent_ref!r} already landed on the integration branch "
            f"({landed_there}): this worktree would implement it a second time",
        )
    after = units[this[0]].get("After", "none")
    for prerequisite in re.findall(r"U\d+[a-z]?", after):
        landed = units.get(prerequisite, {}).get("Landed", "")
        expect(
            landed != "",
            f"phase {prerequisite} has not landed, and plan {plan!r} runs after it",
        )
        span = LANDED_RANGE.search(landed)
        expect(
            span is not None,
            f"phase {prerequisite}'s Landed line must carry its commit range as "
            f"`<first>..<last>`, got {landed!r}",
        )
        assert span is not None
        last = span.group(2)
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
