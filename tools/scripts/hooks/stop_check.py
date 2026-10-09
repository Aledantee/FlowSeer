"""Hold the Stop of a session to the repository conformance gates.

The Stop hook. Usage:

    run.py hooks stop-check

Every package under `test/conformance/` is a gate, and each runs whole with
`go test`. The hook enumerates the directory instead of naming packages or
tests. A `-run` pattern for a renamed test matches nothing and `go test`
reports ok, and a listed path for a renamed package is skipped the same way,
so a gate could vanish with no signal on either axis. Enumeration has no name
to rot, and a gate added later runs without a hook edit. Each child runs
recursively, so a gate one level down runs here and not only at the merge
gate, and a child holding no Go package fails with `no packages to test`.

The third axis is the root directory itself. A moved or emptied
`test/conformance/` leaves nothing to run, so a checkout of this repository,
which `go.mod` identifies, with no gate is a failed gate. A fixture without
`go.mod` has no gates to run and still stops cleanly.

The hook prints one of three shapes: `{}` when the gates hold and nothing is
unverified, a `systemMessage` naming up to twenty paths of the dirty marker,
or a block decision carrying the first failing gate's output. A call made
while an earlier block is still active gets the failure as a `systemMessage`,
since blocking again would keep the session from ever stopping. Input it
cannot read and a session outside a repository get `{}` with no gate run.

This is fast feedback, not the authority. `go test -race ./...` runs the same
tests at the merge gate.
"""

import json
import os
import subprocess
from pathlib import Path

from lib import hookio, proc, treestate

MARKER = "flowseer-verification-dirty"
GATES = Path("test") / "conformance"
MAX_UNVERIFIED = 20
NO_GATE = "no conformance gate found under test/conformance/; the directory is missing or empty, so nothing was checked"


def emit(output: dict[str, str]) -> None:
    print(json.dumps(output), flush=True)


def unverified_paths(root: Path) -> str:
    """Return the first twenty distinct paths of the dirty marker, sorted by bytes and space separated."""
    try:
        result = proc.run(["git", "-C", str(root), "rev-parse", "--git-dir"])
    except OSError:
        return ""
    if result.code != 0:
        return ""
    marker = root / result.stdout.rstrip("\n") / MARKER
    try:
        text = marker.read_text(encoding=treestate.ENCODING, errors=treestate.ERRORS)
    except OSError:
        return ""
    lines = text.split("\n")
    if lines[-1] == "":
        lines.pop()
    return " ".join(sorted(set(lines), key=treestate.sort_key)[:MAX_UNVERIFIED])


def gate_packages(root: Path) -> list[str]:
    """Return the names of the directories under `test/conformance/`, as a glob of `*/` lists them."""
    try:
        with os.scandir(root / GATES) as entries:
            return sorted(entry.name for entry in entries if not entry.name.startswith(".") and entry.is_dir())
    except OSError:
        return []


def failed_gate(root: Path) -> str | None:
    """Return the output of the first failing gate, or None when every gate holds."""
    gates = gate_packages(root)
    for name in gates:
        try:
            done = subprocess.run(
                ["go", "test", f"./{(GATES / name).as_posix()}/..."],
                cwd=root,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                errors="replace",
                check=False,
            )
        except OSError:
            return "go: command not found"
        if done.returncode != 0:
            return done.stdout.rstrip("\n")
    if not gates and (root / "go.mod").is_file():
        return NO_GATE
    return None


def main(argv: list[str]) -> int:
    try:
        event = hookio.read_event()
    except hookio.MalformedEvent:
        event = None
    if event is None:
        emit({})
        return 0
    unverified = unverified_paths(event.root)
    output = failed_gate(event.root)
    if output is None:
        if unverified:
            emit(
                {
                    "systemMessage": f"Edited but not verified: {unverified}. Run "
                    "uv run tools/scripts/run.py verify change -- <changed paths> before handoff."
                }
            )
        else:
            emit({})
        return 0
    reason = f"A repository conformance gate failed. Fix the violations before stopping:\n{output}"
    data = event.data
    active = data.get("stop_hook_active") if isinstance(data, dict) else None
    if active is True or active == "true":
        emit({"systemMessage": reason})
    else:
        emit({"decision": "block", "reason": reason})
    return 0
