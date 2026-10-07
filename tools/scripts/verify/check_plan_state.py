"""Fail on an illegal plan state file.

    run.py verify check-plan-state [<path>...]

Without paths it reads every plan under docs/plans/, and with paths only the
plans those paths belong to. A state file is legal when it holds the keys and
values of the contract in `lib.plans` and fits beside the other plans: a
phase and its parent list each other, and a field the state file holds is
not repeated in the plan's frontmatter. `plan record` refuses any change that
would leave a file this command rejects.
"""

import argparse
import sys
from pathlib import Path

from lib import plans


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py verify check-plan-state", description=__doc__.splitlines()[0])
    parser.add_argument("paths", nargs="*", metavar="PATH")
    args = parser.parse_args(argv)
    try:
        root = plans.tree_root()
    except plans.StateError as error:
        print(f"plan state: {error}", file=sys.stderr)
        return 2
    faults = plans.problems(*plans.read_tree(root))
    if args.paths:
        named = [Path(path).resolve().as_posix() for path in args.paths]

        def chosen(where: str) -> bool:
            plan = (root.resolve() / plans.plan_of(where)).as_posix()
            return any(plan == plans.plan_of(path) or plan.startswith(path.rstrip("/") + "/") for path in named)

        faults = [fault for fault in faults if chosen(fault[0])]
    for where, rule in faults:
        print(f"plan state: {where}: {rule}", file=sys.stderr)
    if faults:
        return 1
    print("plan state: every state file checked is legal.")
    return 0
