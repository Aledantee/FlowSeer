"""Remove a clean linked checkout whose history is preserved on another local branch."""

import argparse
import os
import sys
from pathlib import Path

from lib import worktrees


def close(cwd: str, path: str, force: bool) -> Path:
    target = Path(path).resolve()
    source = Path(worktrees.git(cwd, "rev-parse", "--show-toplevel")).resolve()
    common = Path(worktrees.git(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")).resolve()
    if target == common.parent:
        raise worktrees.Refusal("worktree close refuses the primary checkout.")
    if Path.cwd().resolve().is_relative_to(target) or source.is_relative_to(target):
        raise worktrees.Refusal("worktree close refuses the current checkout; run it from another checkout.")

    listing = worktrees.git(cwd, "worktree", "list", "--porcelain", "-z")
    selected = None
    for record in listing.split("\0\0"):
        fields = dict(line.partition(" ")[::2] for line in record.split("\0") if line)
        if "worktree" not in fields:
            continue
        registered = Path(fields["worktree"]).resolve()
        if registered == target:
            selected = fields
        elif registered.is_relative_to(target):
            raise worktrees.Refusal(f"{target} contains a nested worktree at {registered}; nothing removed.")
    if selected is None:
        raise worktrees.Refusal(f"{target} is not a registered worktree of {source}; nothing removed.")
    if "locked" in selected:
        raise worktrees.Refusal(f"{target} is locked; nothing removed.")
    dirty = worktrees.git(str(target), "status", "--porcelain", "--untracked-files=all")
    if dirty:
        raise worktrees.Refusal(f"{target} is dirty; nothing removed:\n{dirty}")

    head = worktrees.git(str(target), "rev-parse", "HEAD")
    branch = selected.get("branch", "")
    if not force:
        containing = worktrees.git(cwd, "for-each-ref", f"--contains={head}", "--format=%(refname)", "refs/heads/")
        if not any(ref != branch for ref in containing.splitlines()):
            raise worktrees.Refusal(
                f"{target} HEAD is not contained in another local branch; "
                "merge or park it first, or use --force to skip the merge check."
            )

    worktrees.git(cwd, "worktree", "remove", str(target))
    if branch:
        # A branch advanced during removal must remain recoverable.
        if worktrees.git(cwd, "rev-parse", branch) != head:
            raise worktrees.Refusal(f"Removed {target}, but {branch} advanced and was kept.")
        worktrees.git(cwd, "branch", "-D", "--", branch.removeprefix("refs/heads/"))
    return target


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py worktree close", description=__doc__)
    parser.add_argument("path", help="registered linked worktree to remove, including its branch")
    parser.add_argument("--cwd", default=os.getcwd(), help="source checkout (default: current directory)")
    parser.add_argument("--force", action="store_true", help="skip only the check that another local branch contains HEAD")
    args = parser.parse_args(argv)
    try:
        target = close(args.cwd, args.path, args.force)
    except worktrees.Refusal as refusal:
        if refusal.message:
            print(refusal.message, file=sys.stderr)
        return refusal.code
    print(target)
    return 0
