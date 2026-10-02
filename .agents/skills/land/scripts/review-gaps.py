#!/usr/bin/env python3
"""Fail when a plan still lists a review gap.

`review` records a gap as a list item under the plan's `## Review gaps`
heading and deletes the item once the gap pass has closed it
(`review/references/fix-loop.md`). A gap is deferred work, and deferred work
nothing checks is not done, so `land` runs this before it merges.

Usage:
    review-gaps.py <plan>...

Exit 0 when no plan lists a gap, 1 when one does (each printed as
`<plan>:<line>: <entry>`), 2 on a path that is not a file.
"""

import re
import sys
from pathlib import Path

HEADING = re.compile(r"^## Review gaps\s*$")
SECTION = re.compile(r"^## ")
ENTRY = re.compile(r"^[-*] +(\S.*)$")


def open_gaps(path):
    """The entries under the plan's Review gaps heading, as (line, text)."""
    found, inside, fenced = [], False, False
    for number, line in enumerate(path.read_text().splitlines(), start=1):
        if line.startswith("```"):
            fenced = not fenced
        if fenced:
            continue
        if SECTION.match(line):
            inside = bool(HEADING.match(line))
            continue
        entry = ENTRY.match(line) if inside else None
        if entry:
            found.append((number, entry.group(1)))
    return found


def main(arguments):
    if not arguments or arguments[0] in ("-h", "--help"):
        print(__doc__.strip())
        return 0 if arguments else 2
    failed = False
    for argument in arguments:
        path = Path(argument)
        if not path.is_file():
            print(f"{argument}: not a plan file", file=sys.stderr)
            return 2
        for number, text in open_gaps(path):
            print(f"{argument}:{number}: {text}")
            failed = True
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
