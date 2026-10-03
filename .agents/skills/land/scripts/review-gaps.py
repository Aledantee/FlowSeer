#!/usr/bin/env python3
"""Fail when a plan still lists a review gap.

`review` records a gap as a `- ` list item at column 0 under the plan's
`## Review gaps` heading, and the gap pass deletes the item once its mutation
fails the suite (`review/references/fix-loop.md`). A gap is deferred work, and
deferred work nothing checks is not done. The `review` verdict `gaps open`
keeps such a plan from landing, so this script checks that the verdict and the
list agree: an accept beside a listed item is a contradiction, and every gate
that reads this script treats it as not landable.

Usage:
    review-gaps.py <plan>...

Reads every argument and prints each listed gap as `<plan>:<line>: <entry>`.
Exit 0 when no plan lists a gap, 1 when one does, 2 when an argument is not a
file (a gap in another argument is still printed).
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
    listed, missing = False, False
    for argument in arguments:
        path = Path(argument)
        if not path.is_file():
            print(f"{argument}: not a plan file", file=sys.stderr)
            missing = True
            continue
        for number, text in open_gaps(path):
            print(f"{argument}:{number}: {text}")
            listed = True
    if missing:
        return 2
    return 1 if listed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
