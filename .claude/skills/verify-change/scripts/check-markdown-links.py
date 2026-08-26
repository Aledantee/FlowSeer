#!/usr/bin/env python3
"""Check relative Markdown links in explicitly selected files."""

from __future__ import annotations

import re
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit


LINK = re.compile(r"!?\[[^\]]*\]\((?:<([^>]+)>|([^\s)]+))(?:\s+[^)]*)?\)")


def check_file(path: Path, root: Path) -> list[str]:
    failures: list[str] = []
    fenced = False
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if line.lstrip().startswith("```"):
            fenced = not fenced
            continue
        if fenced:
            continue
        for match in LINK.finditer(line):
            target = match.group(1) or match.group(2)
            parsed = urlsplit(target)
            if parsed.scheme or target.startswith(("#", "//")):
                continue
            relative = unquote(parsed.path)
            if not relative:
                continue
            destination = root / relative.lstrip("/") if relative.startswith("/") else path.parent / relative
            if not destination.exists():
                failures.append(f"{path}:{line_number}: missing link target: {target}")
    return failures


def main() -> int:
    root = Path.cwd().resolve()
    failures: list[str] = []
    for argument in sys.argv[1:]:
        path = Path(argument)
        if path.is_file():
            failures.extend(check_file(path, root))
    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

