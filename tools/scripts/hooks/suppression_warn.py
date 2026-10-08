"""Report new lint or check suppressions an edit introduced.

The PostToolUse hook of every edit tool. Usage:

    run.py hooks suppression-warn

AGENTS.md forbids adding an exclusion, ignore, or suppression to make one's
own artifacts pass. That is a judgment call, so this hook does not block. It
names the suppressions the edit wrote, five lines at most, so the agent has to
justify them in the handoff and the reviewer sees them. The text it reads is
the `new_string` and `content` of the call and the `new_string` of each entry
of `edits`. It needs no repository, and malformed input ends quietly.
"""

import json
import re

from lib import hookio

SPACE = " \t\v\f\r"
SUPPRESSION = re.compile(
    rf"//[{SPACE}]*nolint|buf:lint:ignore|buf:breaking:ignore|shellcheck disable|#[{SPACE}]*nosec"
    rf"|eslint-disable|@ts-ignore|@ts-expect-error|//[{SPACE}]*lint:ignore|#[{SPACE}]*noqa"
    r"|exclude-rules|skip-dirs"
)
MAX_LINES = 5


def added_text(tool_input: object) -> str:
    """Return the text the call adds, joined by line breaks and without trailing ones."""
    if not isinstance(tool_input, dict):
        return ""
    parts = [tool_input.get("new_string"), tool_input.get("content")]
    edits = tool_input.get("edits")
    if edits is None or edits is False:
        edits = []
    if isinstance(edits, dict):
        edits = list(edits.values())
    if not isinstance(edits, list):
        # A value that cannot be iterated contributes no text.
        return ""
    parts += [edit.get("new_string") for edit in edits if isinstance(edit, dict)]
    return "\n".join(part for part in parts if isinstance(part, str)).rstrip("\n")


def target_of(tool_input: object) -> str:
    """Return the file path, else the notebook path, else a stand-in for both."""
    if isinstance(tool_input, dict):
        for key in ("file_path", "notebook_path"):
            value = tool_input.get(key)
            if value is not None and value is not False:
                return value if isinstance(value, str) else json.dumps(value)
    return "the edit"


def main(argv: list[str]) -> int:
    try:
        data = json.loads(hookio.read_input())
    except ValueError:
        return 0
    if data is None or data is False:
        return 0
    tool_input = data.get("tool_input") if isinstance(data, dict) else None
    added = added_text(tool_input)
    if not added:
        return 0
    matches = [
        f"{number}:{line}" for number, line in enumerate(added.split("\n"), 1) if SUPPRESSION.search(line)
    ][:MAX_LINES]
    if not matches:
        return 0
    hookio.context(
        f"This edit to {target_of(tool_input)} adds a lint or check suppression:\n"
        + "\n".join(matches)
        + "\nAGENTS.md forbids suppressions that make your own artifacts pass. "
        "Remove it and fix the finding, or state in the handoff why this exact suppression is "
        "the right policy change and propose it separately."
    )
    return 0
