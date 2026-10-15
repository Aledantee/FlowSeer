"""Create an isolated checkout without a runtime hook event."""

import argparse
import os
import sys

from lib import worktrees


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py worktree create", description=__doc__)
    parser.add_argument("name", help="short task name used for the directory and worktree- branch")
    parser.add_argument("--cwd", default=os.getcwd(), help="source checkout (default: current directory)")
    args = parser.parse_args(argv)
    try:
        target = worktrees.create(args.cwd, args.name)
    except worktrees.Refusal as refusal:
        if refusal.message:
            print(refusal.message, file=sys.stderr)
        return refusal.code
    sys.stdout.buffer.write(os.fsencode(target) + b"\n")
    sys.stdout.buffer.flush()
    return 0
