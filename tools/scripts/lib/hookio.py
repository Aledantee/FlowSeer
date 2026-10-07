"""Read a hook's event and answer in the shape both agent runtimes parse.

A hook is a command of the `hooks` group. Its exit code follows the
runtime's hook contract, so a guard that cannot decide prints a decision
instead of exiting non-zero.
"""

import enum
import json
import os
import shutil
import threading
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any, NamedTuple

from lib import proc

STDIN = 0
FIRST_BYTE_SECONDS = 5.0
READ_SIZE = 65536


class MalformedEvent(Exception):
    """Standard input held no JSON value, or `null`, or `false`."""


@dataclass(frozen=True)
class Event:
    """The parsed event with where the session started.

    `root` is the physical top of the repository that holds the session
    directory and `prefix` is the session directory below it, with a
    trailing slash, or empty at the top.
    """

    data: Any
    root: Path
    prefix: str


class Kind(enum.Enum):
    INSIDE = "inside"
    UNSAFE = "unsafe"
    OUTSIDE = "outside"
    OTHER_CHECKOUT = "other-checkout"


class Resolved(NamedTuple):
    kind: Kind
    path: str


def read_input(fd: int = STDIN, first_byte_seconds: float = FIRST_BYTE_SECONDS) -> str:
    """Return the text of a descriptor, or empty when no byte arrives in time.

    A bare read returns only at end of input, so a standard input left open
    with nothing written would hold the hook, and the tool call behind it,
    forever. The first byte gets `first_byte_seconds` to arrive and the
    rest is read to end of input without a bound. A thread does the reading
    because `select` accepts only sockets on Windows. The thread reads the
    descriptor, not `sys.stdin`: one still blocked inside the stream's
    buffer holds its lock when the interpreter exits, and Python then
    aborts with status 134.
    """
    first = threading.Event()
    chunks: list[bytes] = []

    def read() -> None:
        try:
            while True:
                chunk = os.read(fd, READ_SIZE)
                first.set()
                if not chunk:
                    return
                chunks.append(chunk)
        except OSError:
            pass
        finally:
            first.set()

    reader = threading.Thread(target=read, daemon=True)
    reader.start()
    if not first.wait(first_byte_seconds):
        return ""
    reader.join()
    return b"".join(chunks).decode("utf-8", errors="replace")


def read_event(fd: int = STDIN, first_byte_seconds: float = FIRST_BYTE_SECONDS) -> Event | None:
    """Return the event, or None outside a repository.

    Raises MalformedEvent for empty input, input that is not JSON, `null`,
    and `false`. Any other JSON value parses. A value that is not an object
    has no `cwd`, so the session directory is the process's own.
    """
    text = read_input(fd, first_byte_seconds)
    try:
        data = json.loads(text)
    except ValueError as error:
        raise MalformedEvent(str(error)) from error
    if data is None or data is False:
        raise MalformedEvent("the event is null or false")
    cwd = data.get("cwd") if isinstance(data, dict) else None
    session = cwd if isinstance(cwd, str) and cwd else "."
    top = _git(session, "rev-parse", "--show-toplevel")
    prefix = _git(session, "rev-parse", "--show-prefix")
    if top is None or prefix is None:
        return None
    go_tools_on_path()
    return Event(data, Path(os.path.realpath(top)), prefix)


def _git(cwd: str, *args: str) -> str | None:
    """Return git's trimmed output, or None when git fails or is not installed."""
    result = _run(["git", "-C", cwd, *args])
    if result is None or result.code != 0:
        return None
    return result.stdout.rstrip("\n")


def _run(args: list[str]) -> proc.Result | None:
    try:
        return proc.run(args)
    except OSError:
        return None


def go_on_path(candidates: Sequence[str] | None = None) -> None:
    """Append the first install directory that holds `go`, when none is on PATH.

    A hook inherits the environment of the process that started the
    client, and a client launched from anything but a login shell never
    read the profile that adds the directory a tarball install uses. Every
    Go gate would then report "go: command not found" where go works. A
    PATH that already resolves go is left alone.
    """
    if shutil.which("go"):
        return
    if candidates is None:
        candidates = ("/usr/local/go/bin", f"{os.environ.get('HOME', '')}/go/bin", "/usr/lib/go/bin")
    for candidate in candidates:
        if not shutil.which("go", path=candidate):
            continue
        path = os.environ.get("PATH", "")
        if candidate not in path.split(os.pathsep):
            os.environ["PATH"] = f"{path}{os.pathsep}{candidate}"
        return


def go_tools_on_path() -> None:
    """Put the directory `go install` writes to ahead of PATH.

    Go tools install to GOBIN, or else the first GOPATH entry's `bin`,
    which a session's PATH does not always carry. The format hook would
    then report every Go edit as unformatted although the formatters are
    installed. An empty answer adds nothing, since "/bin" ahead of PATH
    would shadow every later entry.
    """
    go_on_path()
    if not shutil.which("go"):
        return
    gobin = _go_env("GOBIN")
    if not gobin:
        gopath = _go_env("GOPATH").split(os.pathsep)[0]
        gobin = os.path.join(gopath, "bin") if gopath else ""
    if not gobin:
        return
    path = os.environ.get("PATH", "")
    if gobin not in path.split(os.pathsep):
        os.environ["PATH"] = f"{gobin}{os.pathsep}{path}"


def _go_env(name: str) -> str:
    result = _run(["go", "env", name])
    return result.stdout.rstrip("\n") if result is not None and result.code == 0 else ""


def tool_paths(data: Any) -> list[str]:
    """Return the paths an edit names: the file or notebook path, then the patch's.

    A patch lists its targets in `*** Add File:`, `*** Update File:`,
    `*** Delete File:`, and `*** Move to:` lines of the command.
    """
    tool_input = data.get("tool_input") if isinstance(data, dict) else None
    if not isinstance(tool_input, dict):
        return []
    paths = []
    for key in ("file_path", "notebook_path"):
        value = tool_input.get(key)
        if value is not None and value is not False:
            if isinstance(value, str):
                paths.append(value)
            break
    command = tool_input.get("command")
    if isinstance(command, str):
        for line in command.split("\n"):
            for prefix in ("*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "):
                if line.startswith(prefix):
                    paths.append(line[len(prefix) :])
    return [path for path in paths if path]


def relative_path(event: Event, candidate: str) -> Resolved:
    """Classify a path the way repository policy needs.

    INSIDE carries the path relative to the checkout. OTHER_CHECKOUT carries
    the path relative to another checkout of this repository, which policy
    covers although the files are not the ones edited. OUTSIDE is a path in
    no checkout of it, to which policy does not apply. UNSAFE is a path
    that cannot be resolved without trusting a `..`.
    """
    root = str(event.root)
    if not candidate.startswith("/"):
        # A relative path is resolved from the session directory, so one that
        # climbs out of the repository is judged by where it lands rather
        # than denied for its `..` segments.
        candidate = f"{root}/{event.prefix}{candidate}"

    # A Write may create the file's parent directories, so resolve the
    # deepest ancestor that exists and reattach the missing tail unchanged.
    directory = _canonical_dir(_dirname(candidate))
    if directory is None:
        return Resolved(Kind.UNSAFE, "")
    target = f"{directory}/{_basename(candidate)}"

    # A `..` left in the missing tail can walk back into a checkout after the
    # prefix test below has placed the path outside it.
    if "/../" in f"{target}/" or "/./" in f"{target}/":
        return Resolved(Kind.UNSAFE, "")

    # Another worktree or the primary checkout of this repository is held to
    # the same policy as this one, so a patch aimed across the sibling
    # directory does not pass as outside.
    checkout = root
    if target != root and not target.startswith(f"{root}/"):
        existing = directory
        while not os.path.isdir(existing):
            existing = _dirname(existing)
        top = _git(existing, "rev-parse", "--show-toplevel")
        if top is None:
            return Resolved(Kind.OUTSIDE, "")
        checkout = os.path.realpath(top)
        if _common_dir(checkout) != _common_dir(root):
            return Resolved(Kind.OUTSIDE, "")

    if target == checkout:
        relative = ""
    elif target.startswith(f"{checkout}/"):
        relative = target[len(checkout) + 1 :]
    else:
        return Resolved(Kind.OUTSIDE, "")
    return Resolved(Kind.INSIDE if checkout == root else Kind.OTHER_CHECKOUT, relative)


def _common_dir(checkout: str) -> str:
    return _git(checkout, "rev-parse", "--path-format=absolute", "--git-common-dir") or ""


def _canonical_dir(candidate: str) -> str | None:
    """Return the physical path of a directory that may not exist yet."""
    suffix = ""
    while not os.path.isdir(candidate):
        suffix = f"/{_basename(candidate)}{suffix}"
        parent = _dirname(candidate)
        if parent == candidate:
            return None
        candidate = parent
    return f"{os.path.realpath(candidate)}{suffix}"


def _dirname(path: str) -> str:
    """Return what the shell's `dirname` prints, which ignores trailing slashes."""
    stripped = path.rstrip("/")
    if not stripped:
        return "/" if path.startswith("/") else "."
    head, slash, _ = stripped.rpartition("/")
    if not slash:
        return "."
    return head.rstrip("/") or "/"


def _basename(path: str) -> str:
    """Return what the shell's `basename` prints, which ignores trailing slashes."""
    stripped = path.rstrip("/")
    if not stripped:
        return "/" if path.startswith("/") else ""
    return stripped.rpartition("/")[2]


def deny(reason: str) -> None:
    """Print a PreToolUse decision that blocks the tool call.

    The hook returns after it: a second decision on standard output would
    not parse.
    """
    _decide("deny", reason)


def ask(reason: str) -> None:
    """Print a PreToolUse decision that asks the person to approve.

    The hook returns after it, as after `deny`.
    """
    _decide("ask", reason)


def context(message: str) -> None:
    """Print a PostToolUse message that is added to the agent's context."""
    _print({"hookSpecificOutput": {"hookEventName": "PostToolUse", "additionalContext": message}})


def _decide(decision: str, reason: str) -> None:
    _print(
        {
            "hookSpecificOutput": {
                "hookEventName": "PreToolUse",
                "permissionDecision": decision,
                "permissionDecisionReason": reason,
            }
        }
    )


def _print(output: dict[str, Any]) -> None:
    print(json.dumps(output), flush=True)
