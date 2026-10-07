"""Read the followed plan state to decide whether a stage has finished."""

import argparse
from pathlib import Path
import sys

from lib import plans, repo


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py drive stage-done", description=__doc__)
    parser.add_argument("plan")
    parser.add_argument("stage", choices=("re-plan", "implement", "review", "compound"))
    args = parser.parse_args(argv)
    try:
        root = repo.root().resolve()
        plan = Path(args.plan).resolve().relative_to(root).as_posix()
        state = plans.followed(plan, root)
        done = {
            "re-plan": state["status"] != "implemented" and state["readiness"] == "implementation-ready",
            "implement": state["status"] == "implemented",
            "review": state["review"] in plans.ACCEPTED,
            "compound": state["compound"] is not None,
        }
        return 0 if done[args.stage] else 1
    except (OSError, RuntimeError, ValueError, plans.StateError) as error:
        print(f"stage-done: {error}", file=sys.stderr)
        return 2
