"""Format, lint, and sync-check the schemas an edit touched.

The PostToolUse hook of every edit tool. Usage:

    run.py hooks proto-check

Runs `buf format -w` and `buf lint` on each `.proto` file the edit names under
`spec/proto/`, and exits 2 with the lint output on standard error when one
fails. It adds context when buf is not on PATH, and when an edited file
declares a message whose Config/State/Event or GlobalRef/LocalRef counterparts
no file under `spec/proto/` declares. A counterpart that the edited file's own
comments name is a decision, not an omission, and is not reported. Malformed
input, a session outside a repository, and a checkout without `spec/proto/`
end quietly.

Every pattern applies to one line, as `grep` and `sed` apply it, and a set of
characters is spelled out instead of named.
"""

import fnmatch
import os
import re
import shutil
import sys
from pathlib import Path

from lib import hookio, proc

PROTO_FILE = "spec/proto/*.proto"
SPACE = " \t\v\f\r"
MESSAGE = re.compile(rf"^[{SPACE}]*message[{SPACE}]+([A-Za-z0-9_]+)", re.MULTILINE)
FAMILIES = (
    ("Config", ("Config", "State", "Event")),
    ("State", ("Config", "State", "Event")),
    ("Event", ("Config", "State", "Event")),
    ("GlobalRef", ("GlobalRef", "LocalRef")),
    ("LocalRef", ("GlobalRef", "LocalRef")),
)
BUF_MISSING = (
    "buf is not on PATH; edited proto files were not formatted or linted. "
    "Install the repository toolchain and check them before stopping."
)
SYNC = (
    "Message sync: edited schemas have missing Config/State/Event or GlobalRef/LocalRef counterparts:\n"
    "{notes}\n"
    "Add them, or name each absent member and why in the file-level doc comment as "
    "docs/conventions/protobuf.md describes; a named member is not reported again."
)


def read(path: Path) -> str:
    return path.read_text(encoding="utf-8", errors="replace")


def schema_text(root: Path) -> str:
    """Return every file under `spec/proto/`, one after another."""
    chunks = []
    for directory, _, names in os.walk(root / "spec" / "proto"):
        for name in names:
            try:
                chunks.append(read(Path(directory, name)))
            except OSError:
                continue
    return "\n".join(chunks)


def declares(corpus: str, name: str) -> bool:
    return re.search(rf"^[{SPACE}]*message[{SPACE}]+{re.escape(name)}[{SPACE}]*\{{", corpus, re.MULTILINE) is not None


def names_in_comment(text: str, name: str) -> bool:
    return re.search(rf"^[{SPACE}]*//.*\b{re.escape(name)}\b", text, re.MULTILINE) is not None


def sync_notes(root: Path, files: list[str]) -> list[str]:
    """Return one note per message family that lacks a member no comment names."""
    notes = []
    seen = set()
    corpus = None
    for relative in files:
        path = root / relative
        if not path.is_file():
            continue
        text = read(path)
        for message in sorted(set(MESSAGE.findall(text))):
            for suffix, members in FAMILIES:
                if not message.endswith(suffix):
                    continue
                base = message[: -len(suffix)]
                if (members[0], base) not in seen:
                    seen.add((members[0], base))
                    corpus = schema_text(root) if corpus is None else corpus
                    missing = [
                        f"{base}{member}"
                        for member in members
                        if not declares(corpus, f"{base}{member}") and not names_in_comment(text, f"{base}{member}")
                    ]
                    if missing:
                        notes.append(f"- {base}: no message found for {' '.join(missing)}")
                break
    return notes


def lint_failures(root: Path, files: list[str]) -> list[str]:
    failures = []
    for relative in files:
        target = root / relative
        if not target.is_file():
            continue
        proc.run(["buf", "format", "-w", str(target)])
        result = proc.run(["buf", "lint", "--path", relative], cwd=root)
        if result.code != 0:
            output = (result.stdout + result.stderr).rstrip("\n")
            failures.append(f"buf lint failed on {relative}:\n{output}")
    return failures


def check(event: hookio.Event) -> int:
    root = event.root
    if not (root / "spec" / "proto").is_dir():
        return 0
    files = []
    for path in hookio.tool_paths(event.data):
        resolved = hookio.relative_path(event, path)
        # The pattern's `*` crosses `/`, so a schema at any depth matches.
        if resolved.kind is hookio.Kind.INSIDE and fnmatch.fnmatchcase(resolved.path, PROTO_FILE):
            files.append(resolved.path)
    if not files:
        return 0

    failures = []
    context = []
    if shutil.which("buf"):
        failures = lint_failures(root, files)
    else:
        context.append(BUF_MISSING)
    notes = sync_notes(root, files)
    if notes:
        context.append(SYNC.format(notes="\n".join(notes)))

    message = "\n\n".join(context)
    if failures:
        print("\n\n".join([*failures, *context]), file=sys.stderr)
        return 2
    if message:
        hookio.context(message)
    return 0


def main(argv: list[str]) -> int:
    try:
        event = hookio.read_event()
    except hookio.MalformedEvent:
        return 0
    return 0 if event is None else check(event)
