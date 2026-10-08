"""Record the edits that need a successful verifier run.

The PostToolUse hook of every edit tool and of the Bash tool. Usage:

    run.py hooks mark-verification-dirty

Appends the path of each unverified change to `flowseer-verification-dirty` in
the git directory, which the verifier clears per path and the Stop gate reads.

An edit tool names its file, so that path is marked when its type is one a
gate verifies. A Bash command names nothing, so what it wrote is read off the
tree. Every dirty path whose content hash is not in the stored listing
`flowseer-tree-state` changed since the last Bash call or the last verified
run, and is marked like an edit. With no stored listing every dirty path is
marked once, which in a fresh worktree is exactly what the command wrote. A
generator's output and the module graph reach packages no path names, so a
change to one also adds the line that keeps the module-wide run.

Malformed input, a session outside a repository, and a listing that cannot be
taken end quietly.
"""

import fnmatch
import json
import os
import tempfile
from pathlib import Path

from lib import hookio, proc, treestate

MARKER = "flowseer-verification-dirty"
STATE = "flowseer-tree-state"
FULL_SCOPE = "<Bash mutation; verify with --full>"
FULL_SCOPE_PATTERNS = (
    "generated/*",
    "*/generated/*",
    "go.mod",
    "go.sum",
    "*/go.mod",
    "*/go.sum",
    "buf.lock",
    "*/buf.lock",
)
# The listing's file types differ: it also names buf.lock and a nested
# go.mod, and both lists stay as they are.
EDIT_SUFFIXES = (".go", ".proto", ".ts", ".tsx", ".js", ".jsx", ".py", ".sh", ".md", ".json", ".yaml", ".yml")
EDIT_NAMES = ("go.mod", "go.sum")


def git(cwd: str, *args: str) -> str | None:
    """Return git's trimmed output, or None when git fails or is not installed."""
    try:
        result = proc.run(["git", "-C", cwd, *args])
    except OSError:
        return None
    return result.stdout.rstrip("\n") if result.code == 0 else None


def append(path: Path, lines: list[str]) -> None:
    if lines:
        with open(path, "a", encoding=treestate.ENCODING, errors=treestate.ERRORS) as marker:
            marker.write("".join(f"{line}\n" for line in lines))


def mark_bash_changes(root: Path, git_dir: Path) -> None:
    # The new listing is written beside the old one and renamed over it, so
    # a reader never sees a half-written listing. It is created first: a git
    # directory that cannot be written ends the hook before any marker write.
    try:
        handle, name = tempfile.mkstemp(prefix=f"{STATE}.", dir=git_dir)
    except OSError:
        return
    try:
        record_bash_changes(root, git_dir, handle, name)
    finally:
        Path(name).unlink(missing_ok=True)


def record_bash_changes(root: Path, git_dir: Path, handle: int, name: str) -> None:
    try:
        listing = treestate.listing(root)
    except (RuntimeError, OSError):
        os.close(handle)
        return
    current = "".join(f"{line}\n" for line in listing)
    state = git_dir / STATE
    try:
        known = set(state.read_text(encoding=treestate.ENCODING, errors=treestate.ERRORS).split("\n"))
    except OSError:
        known = set()
    marks = []
    full = False
    # A path that holds a line break is compared as the lines it breaks into.
    for line in current.split("\n")[:-1]:
        _, _, path = line.partition("\t")
        if line in known or not path:
            continue
        marks.append(path)
        full = full or any(fnmatch.fnmatchcase(path, pattern) for pattern in FULL_SCOPE_PATTERNS)
    if full:
        marks.append(FULL_SCOPE)
    try:
        append(git_dir / MARKER, marks)
    except OSError:
        os.close(handle)
        raise

    try:
        with os.fdopen(handle, "wb") as written:
            written.write(current.encode(treestate.ENCODING, errors=treestate.ERRORS))
        os.replace(name, state)
    except OSError:
        pass


def mark_edit(file: str, cwd: str, root: Path, git_dir: Path) -> None:
    path = file if file.startswith("/") else f"{cwd}/{file}"
    location = Path(path)
    if location.parent.is_dir():
        path = f"{os.path.realpath(location.parent)}/{location.name}"
    prefix = f"{root}/"
    if not path.startswith(prefix):
        return
    relative = path[len(prefix) :]
    if relative.endswith(EDIT_SUFFIXES) or relative in EDIT_NAMES:
        append(git_dir / MARKER, [relative])


def main(argv: list[str]) -> int:
    try:
        data = json.loads(hookio.read_input())
    except ValueError:
        return 0
    if not isinstance(data, dict):
        return 0
    cwd = data.get("cwd")
    if not isinstance(cwd, str) or not cwd:
        return 0
    top = git(cwd, "rev-parse", "--show-toplevel")
    if top is None:
        return 0
    root = Path(os.path.realpath(top))
    git_dir = git(str(root), "rev-parse", "--git-dir")
    if git_dir is None:
        return 0
    marker_dir = root / git_dir

    tool_input = data.get("tool_input")
    if not isinstance(tool_input, dict):
        return 0
    command = tool_input.get("command")
    if command is not None and command is not False and command != "":
        mark_bash_changes(root, marker_dir)
        return 0
    for key in ("file_path", "notebook_path"):
        file = tool_input.get(key)
        if file is not None and file is not False:
            if isinstance(file, str) and file:
                mark_edit(file, cwd, root, marker_dir)
            break
    return 0
