"""Shared worktree creation for command-line callers and runtime hooks."""

import os
import re
import sys

from lib import proc

NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]*")
NAME_LIMIT = 128


class Refusal(Exception):
    """Creation ends with this status after printing its message."""

    def __init__(self, message: str, code: int = 1):
        super().__init__(message)
        self.message = message
        self.code = code


def is_within(candidate: str, parent: str) -> bool:
    return candidate == parent or candidate.startswith(f"{parent}/")


def canonical_path(path: str) -> str | None:
    """Return the physical form of a path that may not exist yet, or None.

    The deepest existing ancestor is resolved through symlinks and the rest is
    appended verbatim. An ancestor that exists but is not a directory
    cannot be resolved.
    """
    candidate, suffix = path, ""
    while not os.path.exists(candidate):
        suffix = f"/{os.path.basename(candidate)}{suffix}"
        parent = os.path.dirname(candidate)
        if parent == candidate:
            return None
        candidate = parent
    if not os.path.isdir(candidate):
        return None
    return f"{os.path.realpath(candidate)}{suffix}"


def git(cwd: str, *args: str) -> str:
    """Return git's output without trailing line breaks.

    Git's own message is passed on and its status ends creation.
    """
    try:
        result = proc.run(["git", "-C", cwd, *args])
    except OSError as error:
        raise Refusal(f"git: {error.strerror}", 127) from error
    if result.code != 0:
        raise Refusal(result.stderr.rstrip("\n"), result.code)
    return result.stdout.rstrip("\n")


def worktree_root(common_dir: str) -> str:
    configured = os.environ.get("FLOWSEER_WORKTREE_ROOT")
    if configured:
        root = configured
    else:
        primary = os.path.dirname(common_dir)
        root = f"{os.path.dirname(primary)}/worktrees/{os.path.basename(primary)}"
    if not root.startswith("/"):
        raise Refusal("worktree create requires an absolute worktree root.")
    return root


def resolve_target(root: str, name: str) -> str:
    target = canonical_path(f"{root}/{name}")
    if target is None:
        raise Refusal("worktree create could not resolve the worktree target.")
    return target


def refuse_nested(target: str, repo_root: str, common_dir: str) -> None:
    if is_within(target, repo_root) or is_within(target, common_dir):
        raise Refusal("worktree create refuses to place a worktree inside the repository or Git data directory.")


def refuse_inside_checkout(target: str, checkout: str) -> None:
    if is_within(target, checkout):
        raise Refusal("worktree create refuses to place a worktree inside an existing checkout.")


def registered_worktrees(cwd: str, target: str) -> list[str]:
    """Return the physical paths of the registered worktrees, refusing a target inside one.

    The check runs as each record is read, so a worktree whose directory is
    gone ends creation only when no earlier one refused the target.
    """
    try:
        listing = proc.run(["git", "-C", cwd, "worktree", "list", "--porcelain", "-z"]).stdout
    except OSError:
        listing = ""
    found = []
    for record in listing.split("\0"):
        if not record.startswith("worktree "):
            continue
        path = record.removeprefix("worktree ")
        if not os.path.isdir(path):
            raise Refusal(f"{path}: No such file or directory")
        found.append(os.path.realpath(path))
        refuse_inside_checkout(target, found[-1])
    return found


def create(cwd: str, name: str) -> str:
    """Create outside registered checkouts, branching from the caller's local HEAD."""
    if not NAME.fullmatch(name) or len(name) > NAME_LIMIT:
        raise Refusal("worktree create received an unsafe worktree name.")
    repo_root = os.path.realpath(git(cwd, "rev-parse", "--show-toplevel"))
    common_dir = os.path.realpath(git(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir"))
    branch = f"worktree-{name}"
    try:
        checked = proc.run(["git", "-C", cwd, "check-ref-format", "--branch", branch])
    except OSError as error:
        raise Refusal(f"git: {error.strerror}", 127) from error
    if checked.code != 0:
        sys.stderr.write(checked.stderr)
        raise Refusal("worktree create received a name that cannot form a Git branch.")

    root = worktree_root(common_dir)
    target = resolve_target(root, name)
    root = os.path.dirname(target)
    refuse_nested(target, repo_root, common_dir)
    existing = registered_worktrees(cwd, target)

    # The root is made before the target is resolved a second time, since a
    # symlink in a missing tail only shows once the directory exists.
    try:
        os.makedirs(root, exist_ok=True)
    except OSError as error:
        raise Refusal(f"mkdir: {root}: {error.strerror}") from error
    target = resolve_target(root, name)
    refuse_nested(target, repo_root, common_dir)
    for checkout in existing:
        refuse_inside_checkout(target, checkout)

    added = proc.run(["git", "-C", cwd, "worktree", "add", "-b", branch, target, "HEAD"])
    # Standard output carries the path alone, so everything git says goes to
    # standard error, in the order git wrote it.
    sys.stderr.write(added.stderr + added.stdout)
    if added.code != 0:
        raise Refusal("", added.code)
    return target
