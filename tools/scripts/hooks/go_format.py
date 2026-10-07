"""Format the Go files an edit touched.

The PostToolUse hook of every edit tool. Usage:

    run.py hooks go-format

Runs `gofumpt -w` and then `goimports -w` on each Go file the edit names
inside the checkout, skipping generated output and files that are gone. It
adds context naming the files whose content changed, so the agent re-reads
them, and adds context when a formatter is not on PATH. It exits 2 with the
tool's output on standard error when a formatter fails. Malformed input and a
session outside a repository end quietly.
"""

import hashlib
import shutil
import sys
from pathlib import Path

from lib import hookio, proc

FORMATTERS = ("gofumpt", "goimports")
GENERATED = ("generated/", "frontend/web/generated/")


def digest(path: Path) -> str | None:
    """Return the hash of the file's content, or None when it cannot be read."""
    try:
        return hashlib.sha256(path.read_bytes()).hexdigest()
    except OSError:
        return None


def go_files(event: hookio.Event) -> list[str]:
    """Return the checkout-relative paths of the existing, hand-written Go files the edit names."""
    found = []
    for path in hookio.tool_paths(event.data):
        resolved = hookio.relative_path(event, path)
        if resolved.kind is not hookio.Kind.INSIDE:
            continue
        relative = resolved.path
        if not relative.endswith(".go") or relative.startswith(GENERATED):
            continue
        if (event.root / relative).is_file():
            found.append(relative)
    return found


def format_files(event: hookio.Event) -> int:
    missing = [formatter for formatter in FORMATTERS if shutil.which(formatter) is None]
    if missing:
        hookio.context(
            f"{', '.join(missing)} is not on PATH; edited Go files were not formatted. "
            "Install the repository toolchain and format them before stopping."
        )
        return 0
    reformatted = []
    for relative in go_files(event):
        target = event.root / relative
        before = digest(target)
        for formatter in FORMATTERS:
            result = proc.run([formatter, "-w", str(target)])
            if result.code != 0:
                output = (result.stdout + result.stderr).rstrip("\n")
                print(f"{formatter} failed on {relative}:\n{output}", file=sys.stderr)
                return 2
        if digest(target) != before:
            reformatted.append(relative)
    if reformatted:
        hookio.context(
            f"gofumpt + goimports reformatted {', '.join(reformatted)}. "
            "Re-read those files before editing the same regions."
        )
    return 0


def main(argv: list[str]) -> int:
    try:
        event = hookio.read_event()
    except hookio.MalformedEvent:
        return 0
    return 0 if event is None else format_files(event)
