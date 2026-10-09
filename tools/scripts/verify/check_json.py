"""Check that each named file holds exactly one JSON value.

Usage: run.py verify check-json PATH...

It reads the hook configuration files and reports malformed JSON without
judging any key or value. It is stricter than `jq empty`, which accepts an
empty file and a file holding several values.
"""

import json
import sys
from pathlib import Path

USAGE = "Usage: run.py verify check-json PATH..."


def check_file(path: str) -> str | None:
    """Return the failure line for the file, or None when it parses."""
    try:
        text = Path(path).read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as error:
        return f"{path}: {error}"
    try:
        json.loads(text)
    except json.JSONDecodeError as error:
        return f"{path}:{error.lineno}:{error.colno}: {error.msg}"
    return None


def main(argv: list[str]) -> int:
    if not argv:
        print(USAGE, file=sys.stderr)
        return 2
    failures = [failure for path in argv if (failure := check_file(path))]
    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    return 0
