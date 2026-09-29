#!/usr/bin/env python3
"""List where a branch's diff and a plan's units disagree.

A session's own account of what it changed covers a fraction of what it
did and drifts toward the plan it was given, so the Finish step reads the
deviations off the tree instead. Usage:

    plan-deviations.py PLAN BASE [-- PATH...]

PLAN is the plan file; its Files field under each `### U<n>` heading names
what the unit may touch. BASE is the ref the branch is compared with; the
diff covers tracked changes and untracked files alike. Explicit paths
limit it to the task's own changes when the worktree holds others.

Plans write the field two ways, `Files: ...` and `- **Files:**` with the
paths on the following lines; the entries are backtick-quoted, comma
separated, may run over several lines, and a later entry with no slash is
a file in the directory of the entry before it. A directory entry covers
everything under it, `{a,b}` braces expand, and a quoted word with neither
a slash nor a dot is a symbol named in an aside, not a file. The plan
itself and `docs/plans/` are never a deviation.

Prints two lists and exits 0; the caller quotes them. Exits 2 on a plan
without units or a base git cannot resolve.
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

UNIT = re.compile(r"^###\s+(U\d+[a-z]?)[.:]\s")
FIELD = re.compile(r"^(?:-\s+)?\**([A-Z][a-z]+)\**:\**\s*(.*)$")
QUOTED = re.compile(r"`([^`]+)`")
BRACES = re.compile(r"\{([^{}]*)\}")


def expand(entry: str) -> list[str]:
    match = BRACES.search(entry)
    if not match:
        return [entry]
    head, tail = entry[: match.start()], entry[match.end() :]
    return [
        expanded
        for alternative in match.group(1).split(",")
        for expanded in expand(head + alternative.strip() + tail)
    ]


def units(plan: Path) -> dict[str, list[str]]:
    result: dict[str, list[str]] = {}
    current = None
    in_files = False
    last_dir = ""
    for line in plan.read_text(encoding="utf-8").splitlines():
        heading = UNIT.match(line)
        if heading:
            current = heading.group(1)
            result[current] = []
            in_files = False
            continue
        field = FIELD.match(line.strip())
        if field and not line.startswith((" ", "\t")):
            in_files = field.group(1) == "Files"
            last_dir = ""
        if not (in_files and current):
            continue
        for item in QUOTED.findall(line):
            item = item.strip()
            is_dir = item.endswith("/")
            item = item.rstrip("/")
            if not item or item.lower() == "none":
                continue
            if "/" not in item and "." not in item and not is_dir:
                continue
            if "/" not in item and last_dir:
                item = f"{last_dir}/{item}"
            for expanded in expand(item):
                result[current].append(expanded)
            parent = item if is_dir else str(Path(item).parent)
            last_dir = parent if parent != "." else ""
    return result


def covers(entry: str, path: str) -> bool:
    if "*" in entry:
        return Path(path).match(entry)
    return path == entry or path.startswith(entry + "/")


def git_lines(*args: str) -> list[str]:
    proc = subprocess.run(["git", *args], capture_output=True, text=True)
    if proc.returncode != 0:
        print(proc.stderr.strip(), file=sys.stderr)
        sys.exit(2)
    return [line for line in proc.stdout.split("\n") if line]


def main(argv: list[str]) -> int:
    if len(argv) < 3:
        print(__doc__, file=sys.stderr)
        return 2
    plan = Path(argv[1])
    base = argv[2]
    paths = [p for p in argv[3:] if p != "--"]
    if not plan.is_file():
        print(f"plan not found: {plan}", file=sys.stderr)
        return 2
    unit_files = units(plan)
    if not any(unit_files.values()):
        print(f"no unit with a Files field in {plan}", file=sys.stderr)
        return 2
    changed = git_lines("diff", "--name-only", base, "--", *paths)
    changed += git_lines("ls-files", "--others", "--exclude-standard", "--", *paths)

    unnamed = [
        p
        for p in changed
        if not p.startswith("docs/plans/")
        and not any(covers(e, p) for entries in unit_files.values() for e in entries)
    ]
    untouched = {
        unit: [e for e in entries if not any(covers(e, p) for p in changed)]
        for unit, entries in unit_files.items()
    }

    print("Changed, named by no unit:")
    for p in unnamed:
        print(f"  {p}")
    if not unnamed:
        print("  none")
    print("Named by a unit, unchanged:")
    printed = False
    for unit, entries in untouched.items():
        for e in entries:
            print(f"  {unit}: {e}")
            printed = True
    if not printed:
        print("  none")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
