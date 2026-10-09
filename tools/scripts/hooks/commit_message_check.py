"""Report a new commit whose message holds a literal backslash-n pair.

The PostToolUse hook of the Bash tool. Usage:

    run.py hooks commit-message-check

`git commit -m "subject\\n\\nbody"` stores the backslashes, and the log then
shows one long line where paragraphs were meant. The message is read off the
commit instead of guessed from the command text, which cannot tell a quoted
`\\n` from `$'\\n'`. The hook reports and does not block, since the commit
already exists and an amend from standard input repairs it.

The last commit it examined is kept in the git directory, so each commit is
examined once. The first call in a worktree only records where the session
started, because that commit is not this session's to rewrite.
"""

import json
from pathlib import Path

from lib import hookio, proc

STATE = "flowseer-commit-message-checked"
ESCAPED_BREAK = "\\n\\n"


def git(cwd: str, *args: str) -> str | None:
    """Return git's trimmed output, or None when git fails or is not installed."""
    try:
        result = proc.run(["git", "-C", cwd, *args])
    except OSError:
        return None
    return result.stdout.rstrip("\n") if result.code == 0 else None


def main(argv: list[str]) -> int:
    try:
        data = json.loads(hookio.read_input())
    except ValueError:
        return 0
    cwd = data.get("cwd") if isinstance(data, dict) else None
    if not isinstance(cwd, str) or not cwd:
        return 0
    head = git(cwd, "rev-parse", "--verify", "-q", "HEAD")
    git_dir = git(cwd, "rev-parse", "--absolute-git-dir")
    if head is None or git_dir is None:
        return 0
    state = Path(git_dir) / STATE
    try:
        seen = state.read_text(encoding="utf-8", errors="replace").rstrip("\n")
    except OSError:
        seen = ""
    if head == seen:
        return 0
    try:
        state.write_text(f"{head}\n", encoding="utf-8")
    except OSError:
        return 0
    if not seen:
        return 0
    message = git(cwd, "log", "-1", "--format=%B", head)
    if message is None or ESCAPED_BREAK not in message:
        return 0
    short = git(cwd, "rev-parse", "--short", head) or ""
    hookio.context(
        f"The message of commit {short} holds a literal \\n\\n where a paragraph break was meant: "
        "git commit -m does not expand escapes. If this session wrote it, rewrite it with the message "
        "on standard input (git commit --amend -F -) or with one -m per paragraph."
    )
    return 0
