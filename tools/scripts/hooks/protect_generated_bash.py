"""Deny direct shell mutations of buf-managed output and buf.lock.

The PreToolUse hook of the Bash tool. Usage:

    run.py hooks protect-generated-bash

Bash cannot be parsed safely with a regular expression, so this guard works
per simple command: it denies when a guarded path is the target of a
redirect or download, or when the command's verb mutates files and the
command names a guarded path. Reads such as `sed -n 5p generated/x` or a
`grep ... | sed` pipeline pass. Opaque scripts are left to the sandbox and
review. A simple command ends at any of `;&|()` or a line break, which is a
split and not a parser, so a path inside parentheses is judged apart from the
verb in front of them.

Every pattern applies to one line, as `grep` applies it, and a set of
characters is spelled out instead of named, so a class never spans a line
break or follows the locale. Malformed input and an unexpected error deny.
"""

import json
import re

from lib import hookio

MALFORMED = "Generated-file Bash guard received malformed hook input and failed closed."
FAILED = "Generated-file Bash guard failed closed on an unexpected error."
MUTATE = (
    "Do not mutate generated/, frontend/web/generated/, or buf.lock from Bash. "
    "Change the source of truth, then use 'buf generate' or 'buf dep update'."
)
REDIRECT = "Shell redirection or download output may not write buf-managed files. Regenerate them from the source of truth."
GIT = "Git may not overwrite or remove buf-managed output directly. Regenerate it from the source of truth."
FIND = "Do not mutate buf-managed files through find. Regenerate them from the source of truth."

SPACE = " \t\n\v\f\r"
WORD = "A-Za-z0-9_.-"
MUTATORS = frozenset(
    "rm mv cp touch truncate tee dd install chmod chown rsync patch apply_patch nano vim vi ex ed emacs".split()
)
WRAPPERS = frozenset("sudo time command nice nohup env exec".split())

PATCH_HEADER = re.compile(r"^\*\*\* (?:Add|Update|Delete) File: .*(?:(?:frontend/web/)?generated/|buf\.lock)")
GUARDED = re.compile(
    rf"(?:^|[^{WORD}])(?:frontend/web/)?generated/|(?:^|[^{WORD}])buf\.lock(?:[^{WORD}]|$)"
)
WRITES = re.compile(
    rf"(?:>|-o[{SPACE}]+|-O[{SPACE}]+)[{SPACE}]*[\"']?(?:[^{SPACE}\"']*/)?(?:(?:frontend/web/)?generated/|buf\.lock)"
)
IN_PLACE = re.compile(rf"(?:^|[{SPACE}])-[a-zA-Z]*i")
ONE_LINER = re.compile(rf"(?:^|[{SPACE}])-c[{SPACE}]")
GIT_REWRITE = re.compile(
    rf"^(?:-C[{SPACE}]+[^{SPACE}]+[{SPACE}]+)?(?:apply|am|checkout|restore|clean|rm|mv)(?:[{SPACE}]|$)"
)
FIND_ACTION = re.compile(rf"(?:^|[{SPACE}])(?:-delete|-exec|-execdir)(?:[{SPACE}]|$)")
SIMPLE_COMMANDS = re.compile(r"[;&|()\n]")
FIELD = re.compile(r"[ \t]+")


def split_field(text: str) -> tuple[str, str]:
    """Return the first word of text and the rest, as `read -r word rest` splits them."""
    fields = FIELD.split(text.strip(" \t"), maxsplit=1)
    return fields[0], fields[1] if len(fields) > 1 else ""


def judge(command: str) -> str | None:
    """Return the reason to deny a Bash command, or None."""
    if "generated/" not in command and "buf.lock" not in command:
        return None
    # An apply_patch heredoc names its files on "*** ... File:" lines, not as
    # operands of the verb, so judge the whole command for it first.
    if any(PATCH_HEADER.search(line) for line in command.split("\n")):
        return MUTATE
    for segment in SIMPLE_COMMANDS.split(command):
        simple = segment.lstrip(SPACE)
        if not simple or not GUARDED.search(simple):
            continue
        if WRITES.search(simple):
            return REDIRECT
        verb, rest = split_field(simple)
        if verb in WRAPPERS:
            verb, rest = split_field(rest)
        verb = verb.rpartition("/")[2]
        if verb in MUTATORS:
            return MUTATE
        if verb in ("sed", "perl") and IN_PLACE.search(rest):
            return MUTATE
        # A -c one-liner that only prints is a read, and a script file is not.
        # The split removes the parentheses an open() call needs, so no check
        # for one can fire.
        if verb in ("python", "python3", "ruby") and not ONE_LINER.search(rest):
            return MUTATE
        if verb == "git" and GIT_REWRITE.search(rest):
            return GIT
        if verb == "find" and FIND_ACTION.search(rest):
            return FIND
    return None


def command_of(data: object) -> str:
    """Return the command the event carries, without trailing line breaks, or an empty string."""
    tool_input = data.get("tool_input") if isinstance(data, dict) else None
    command = tool_input.get("command") if isinstance(tool_input, dict) else None
    return command.rstrip("\n") if isinstance(command, str) else ""


def main(argv: list[str]) -> int:
    try:
        # The guard needs no repository, so the event is parsed here and not by
        # `read_event`, which returns nothing outside one.
        try:
            data = json.loads(hookio.read_input())
        except ValueError:
            data = None
        if data is None or data is False:
            hookio.deny(MALFORMED)
            return 0
        reason = judge(command_of(data))
        if reason:
            hookio.deny(reason)
    except Exception:
        hookio.deny(FAILED)
    return 0
