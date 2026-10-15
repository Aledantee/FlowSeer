"""Adapt a Claude WorktreeCreate event to shared worktree creation.

Usage: run.py hooks create-worktree

Other harnesses use run.py worktree create NAME without a hook event.
"""

import json
import os
import sys

from lib import hookio, worktrees


def parse(text: str) -> tuple[str, str]:
    """Return the working directory and name of the event."""
    try:
        data = json.loads(text)
    except ValueError:
        data = None
    fields = [data.get(key) if isinstance(data, dict) else None for key in ("cwd", "name")]
    if not all(isinstance(field, str) and field for field in fields):
        raise worktrees.Refusal("WorktreeCreate requires string cwd and name fields.")
    return fields[0], fields[1]


def main(argv: list[str]) -> int:
    try:
        cwd, name = parse(hookio.read_input())
        target = worktrees.create(cwd, name)
    except worktrees.Refusal as refusal:
        if refusal.message:
            print(refusal.message, file=sys.stderr)
        return refusal.code
    sys.stdout.buffer.write(os.fsencode(target) + b"\n")
    sys.stdout.buffer.flush()
    return 0
