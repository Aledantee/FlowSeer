"""Keep each session writing inside its own git worktree.

Wired to two events of each runtime. Usage:

    run.py hooks worktree-guard

SessionStart injects context that tells the agent to enter a worktree first.
PreToolUse denies file-writing tools while the session sits in the primary
checkout on a protected branch, which is the enforcement, since SessionStart
cannot block anything. `git add`, `git commit`, and `git merge` stay allowed
there. Direct file writes are blocked, so a commit in the primary checkout
can capture only content the session did not write itself, and a merge can
land only branches produced in a worktree. In a linked worktree the guard is
passive, except that it denies an edit that would leak into the parent
checkout.

The Bash check reads a command crudely. Each pattern applies to one line, as
`grep` and `sed` apply it, and the command is split into words on blanks and
line breaks, with each word that holds a pattern character expanded by bash
in the session directory. It is a token scan and not a parser.

Switches: `CLAUDE_WORKTREE_GUARD=off` for the machine or one shell,
`.claude/no-worktree-guard` in a repository, and
`CLAUDE_WORKTREE_GUARD_BRANCHES` (default "main master develop trunk") for the
branches that count as protected. A detached HEAD is always protected.

Every path is compared in its physical form, since git reports the top level
through resolved symlinks (/private/tmp on macOS) while other paths may come
back logically (/tmp), and a mismatch would read the primary checkout as a
linked worktree.
"""

import json
import os
import re
import subprocess
from pathlib import Path
from typing import Any

from lib import hookio, proc

DEFAULT_BRANCHES = "main master develop trunk"
FILE_TOOLS = frozenset({"Edit", "Write", "NotebookEdit"})

NUDGE = (
    'Call the EnterWorktree tool with a short descriptive name (e.g. EnterWorktree name:"fix-snmp-timeout") '
    "to get an isolated worktree, then retry. Read-only commands, git add, git commit, and git merge of a "
    "worktree branch still work here."
)
SESSION_START = (
    "This session started in the primary checkout {toplevel} on {where}, which is protected. Before any file "
    "edit or other file-writing command, call the EnterWorktree tool with a short descriptive name for the "
    "task — this project requires each session to write in its own git worktree so concurrent Claude "
    "sessions cannot clobber each other; a hook denies file-writing tools here until you do. Allowed "
    "directly in this checkout: read-only exploration, git add / git commit (to record files that already "
    "exist here, e.g. user-copied or generated ones), and git merge of a finished worktree branch. If a "
    "merge conflicts, run git merge --abort and resolve by rebuilding the worktree branch instead — "
    "conflict edits cannot be made here."
)
LEAK = (
    "This session is isolated in the worktree {toplevel}, but the edit target is {target} — inside the "
    "parent checkout {parent}. Write to the corresponding path under {toplevel} instead; changes reach the "
    "parent through a commit and merge, never through a direct edit."
)
FILE_DENIAL = "Refusing to modify {target}: this session is in the primary checkout {toplevel} on {where}. {nudge}"
COMMAND_DENIAL = (
    "Refusing to run this command because {reason}, and the session is in the primary checkout {toplevel} "
    "on {where}. {nudge}"
)

REDIRECTS = "it redirects output into a file"
MODIFIES = "it modifies files on disk"
IN_PLACE_EDIT = "it edits files in place"
REWRITES = "it rewrites the working tree or history"
REGENERATES = "it regenerates or rewrites tracked files"

# The characters of [[:space:]], spelled out so no class follows the locale.
SPACE = " \t\n\v\f\r"
# The characters the shell splits an unquoted expansion on.
IFS = " \t\n"

# Redirections that do not write a file go first, so a plain `>` can imply a
# write. A pair of angle brackets with no blank between them is an email
# address in a commit trailer or placeholder text, never a redirect.
HARMLESS = tuple(
    re.compile(pattern)
    for pattern in (
        r"2>&1",
        rf"2>[^{SPACE}]*",
        r">&2",
        rf"[0-9]*>[{SPACE}]*/dev/null",
        rf"<[^<>{SPACE}]*>",
        r"->",
        r"=>",
        r">=",
    )
)
SINGLE_QUOTED = re.compile(r"'[^']*'")

# add, commit, and merge are deliberately absent from the verbs below. With
# file writes blocked, staging, committing, and merging worktree branches
# are the safe landing operations the primary checkout exists for.
BOUNDARY = rf"(?:^|[;&|(]|[{SPACE}])"
END = rf"(?:[{SPACE}]|$)"
FILE_VERBS = re.compile(
    BOUNDARY + r"(?:rm|mv|cp|mkdir|rmdir|touch|chmod|chown|ln|patch|tee|dd|truncate|install)" + END
)
IN_PLACE = re.compile(BOUNDARY + rf"(?:sed|perl|ruby)[{SPACE}]+[^|;]*-i")
GIT_REWRITES = re.compile(
    BOUNDARY
    + rf"git[{SPACE}]+(?:rebase|cherry-pick|revert|apply|am|push|reset|checkout|switch|restore|stash|clean|rm|mv)"
    + END
)
GENERATORS = re.compile(
    BOUNDARY
    + rf"(?:go[{SPACE}]+(?:mod|generate|get)|buf[{SPACE}]+generate|(?:npm|pnpm|yarn|bun)[{SPACE}]+(?:install|add|remove))"
    + END
)
RM_COMMAND = re.compile(rf"^[{SPACE}]*rm(?:[{SPACE}]|$)")
SHELL_SYNTAX = re.compile(r"[;&|<>`$()]")
WORDS = re.compile(rf"[^{IFS}]+")


def basename(path: str) -> str:
    """Return what the shell's `basename` prints, which ignores trailing slashes."""
    return path.rstrip("/").rpartition("/")[2]


def dirname(path: str) -> str:
    """Return what the shell's `dirname` prints, which ignores trailing slashes."""
    head, slash, _ = path.rstrip("/").rpartition("/")
    if not slash:
        return "."
    return head.rstrip("/") or "/"


def physical_path(path: str) -> str:
    """Return the physical form of a path that may not exist yet.

    The deepest existing ancestor is resolved through symlinks and the rest
    is appended verbatim.
    """
    candidate, suffix = path, ""
    while not os.path.isdir(candidate):
        suffix = f"/{basename(candidate)}{suffix}"
        if "/" not in candidate:
            return path
        candidate = dirname(candidate)
    return f"{os.path.realpath(candidate)}{suffix}"


def git(cwd: str, *args: str) -> str | None:
    """Return git's output without trailing line breaks, or None when git fails."""
    try:
        result = proc.run(["git", *args], cwd=Path(cwd))
    except OSError:
        return None
    return result.stdout.rstrip("\n") if result.code == 0 else None


def text(value: Any) -> str:
    """Return a field when it is a string, and an empty string for any other value."""
    return value if isinstance(value, str) else ""


def target_of(tool_input: Any) -> str:
    """Return the file path of an edit, else its notebook path.

    An empty string ends the search: only a missing, null, or false value
    falls through to the next key.
    """
    if not isinstance(tool_input, dict):
        return ""
    for key in ("file_path", "notebook_path"):
        value = tool_input.get(key)
        if value is not None and value is not False:
            return text(value)
    return ""


def scrub(command: str) -> str:
    """Drop what a `>` in the command cannot mean as a redirect into a file."""
    lines = command.split("\n")
    for pattern in HARMLESS:
        lines = [pattern.sub("", line) for line in lines]
    scrubbed = "\n".join(lines)
    # A `>` inside single quotes is data, such as a grep pattern for a
    # Markdown quote line. Single-quoted spans are dropped only when the
    # command has no double quote and no escaped apostrophe: an apostrophe
    # inside "..." or written as \' would pair with a later quote and hide a
    # real redirect.
    if '"' in scrubbed or "\\'" in scrubbed:
        return scrubbed
    return "\n".join(SINGLE_QUOTED.sub("", line) for line in lines)


def matches(pattern: re.Pattern[str], command: str) -> bool:
    """Apply a pattern to each line of the command, as `grep` does."""
    return any(pattern.search(line) for line in command.split("\n"))


def reason_to_deny(command: str) -> str:
    """Return why the command writes files, or an empty string when it does not."""
    if ">" in scrub(command):
        return REDIRECTS
    if matches(FILE_VERBS, command):
        return MODIFIES
    if matches(IN_PLACE, command):
        return IN_PLACE_EDIT
    if matches(GIT_REWRITES, command):
        return REWRITES
    if matches(GENERATORS, command):
        return REGENERATES
    return ""


def words(command: str, cwd: str) -> list[str] | None:
    """Return the names bash gives the unquoted expansion of the command in `cwd`.

    This is what the shell hook's `for tok in $cmd` iterated over: the command
    split on blanks and line breaks, each word with a pattern character
    replaced by the names it matches and kept when nothing matches. Bash does
    the expansion, since its bracket and `^` rules are not glob's. The command
    is a positional argument and never part of the script, so nothing in it is
    evaluated. A missing `bash` returns None, and the callers then keep the
    denial, since no operand can be judged.
    """
    if not WORDS.search(command):
        return []
    try:
        done = subprocess.run(
            ["bash", "-c", 'printf "%s\\n" $1', "_", command],
            cwd=cwd,
            capture_output=True,
            check=False,
        )
    except OSError:
        return None
    # Split on line feeds by hand: text mode would turn a lone `\r` into one.
    return done.stdout.decode("utf-8", errors="surrogateescape").split("\n")[:-1]


def unquoted(word: str) -> str:
    """Drop one leading and one trailing quote character."""
    if word[:1] in ("'", '"'):
        word = word[1:]
    if word[-1:] in ("'", '"'):
        word = word[:-1]
    return word


def deletes_only_untracked(command: str, cwd: str, toplevel: str) -> bool:
    """Report whether a plain `rm` names only paths inside the checkout that git does not track.

    Deleting untracked files cannot clobber a concurrent session: the guard
    protects shared tracked state, and untracked strays exist only in this
    checkout. For a directory `git ls-files` lists the tracked files beneath
    it, so a tracked descendant blocks a recursive delete too.
    """
    if not matches(RM_COMMAND, command) or SHELL_SYNTAX.search(command):
        return False
    names = words(command, cwd)
    if names is None:
        return False
    for word in names[1:]:
        word = unquoted(word)
        if word.startswith("-"):
            continue
        path = physical_path(word if word.startswith("/") else f"{cwd}/{word}")
        if not path.startswith(f"{toplevel}/") or git(cwd, "ls-files", "--", path):
            return False
    return True


def writes_only_outside(command: str, cwd: str, toplevel: str) -> bool:
    """Report whether the command names an absolute or `~` path and every such path lies outside the checkout.

    The checkout is the only thing this guard protects, so such a write
    cannot clobber a concurrent session here (`sed -i ... ~/.codex/config.toml`).
    A command that uses only relative paths keeps its denial, since those
    resolve into the checkout. A command that mixes outside absolute
    operands with bare relative ones can slip through.
    """
    home = os.environ.get("HOME", "")
    names = words(command, cwd)
    if names is None:
        return False
    saw_path = False
    for word in names:
        word = unquoted(word)
        word = word.removeprefix(">")
        if word.startswith("/"):
            path = word
        elif word == "~":
            path = home
        elif word.startswith("~/"):
            path = f"{home}/{word[2:]}"
        else:
            continue
        saw_path = True
        path = physical_path(path)
        if path == toplevel or path.startswith(f"{toplevel}/"):
            return False
    return saw_path


def guard_linked(data: dict[str, Any], cwd: str, toplevel: str, git_dir: str, common_dir: str) -> None:
    """Deny an edit aimed at the parent checkout from inside a linked worktree."""
    if text(data.get("hook_event_name")) != "PreToolUse":
        return
    tool_input = data.get("tool_input")
    if text(data.get("tool_name")) not in FILE_TOOLS:
        return
    target = target_of(tool_input)
    if not target:
        return
    absolute = physical_path(target if target.startswith("/") else f"{cwd}/{target}")
    parent = os.path.realpath(f"{common_dir}/..")
    # The worktree's own git directory sits under <parent>/.git/worktrees/<name>,
    # inside the parent by path and private to this worktree by ownership:
    # implement keeps its plan ledger there and the verifier its receipt.
    if absolute.startswith((f"{toplevel}/", f"{git_dir}/")):
        return
    if absolute.startswith(f"{parent}/"):
        hookio.deny(LEAK.format(toplevel=toplevel, target=absolute, parent=parent))


def guard_primary(data: dict[str, Any], cwd: str, toplevel: str, branch: str) -> None:
    """Deny file writes in the primary checkout, or inject the instruction at session start."""
    protected = (os.environ.get("CLAUDE_WORKTREE_GUARD_BRANCHES") or DEFAULT_BRANCHES).split()
    # A detached HEAD in the primary checkout is just as unsafe to share.
    if branch and branch not in protected:
        return
    where = f"branch '{branch}'" if branch else "a detached HEAD"
    event = text(data.get("hook_event_name"))
    if event == "SessionStart":
        context = SESSION_START.format(toplevel=toplevel, where=where)
        output = {"hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": context}}
        print(json.dumps(output), flush=True)
        return
    if event != "PreToolUse":
        return
    tool = text(data.get("tool_name"))
    tool_input = data.get("tool_input")
    if tool in FILE_TOOLS:
        target = target_of(tool_input)
        absolute = physical_path(target if target.startswith("/") else f"{cwd}/{target}")
        # Only the repository needs protecting. Editing dotfiles, this hook, or
        # anything else outside the checkout cannot clobber a concurrent session.
        if absolute.startswith(f"{toplevel}/"):
            hookio.deny(FILE_DENIAL.format(target=target, toplevel=toplevel, where=where, nudge=NUDGE))
    elif tool == "Bash":
        command = text(tool_input.get("command")) if isinstance(tool_input, dict) else ""
        reason = reason_to_deny(command)
        if not reason:
            return
        if reason == MODIFIES and deletes_only_untracked(command, cwd, toplevel):
            return
        if writes_only_outside(command, cwd, toplevel):
            return
        hookio.deny(COMMAND_DENIAL.format(reason=reason, toplevel=toplevel, where=where, nudge=NUDGE))


def main(argv: list[str]) -> int:
    if os.environ.get("CLAUDE_WORKTREE_GUARD") == "off":
        return 0
    try:
        data = json.loads(hookio.read_input())
    except ValueError:
        return 0
    session = text(data.get("cwd")) if isinstance(data, dict) else ""
    if not session or not os.path.isdir(session):
        return 0
    top = git(session, "rev-parse", "--show-toplevel")
    if top is None or not os.path.isdir(top):
        return 0
    toplevel = os.path.realpath(top)
    cwd = os.path.realpath(session)
    if os.path.exists(f"{toplevel}/.claude/no-worktree-guard"):
        return 0
    git_dir = git(session, "rev-parse", "--absolute-git-dir")
    common = git(session, "rev-parse", "--git-common-dir")
    if git_dir is None or common is None or not os.path.isdir(git_dir):
        return 0
    git_dir = os.path.realpath(git_dir)
    common_dir = os.path.join(cwd, common)
    common_dir = os.path.realpath(common_dir) if os.path.isdir(common_dir) else git_dir
    if git_dir != common_dir:
        guard_linked(data, cwd, toplevel, git_dir, common_dir)
    else:
        branch = git(session, "symbolic-ref", "--quiet", "--short", "HEAD") or ""
        guard_primary(data, cwd, toplevel, branch)
    return 0
