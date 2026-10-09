"""Print the tree-state listing of a worktree.

The marker hook compares this listing with the stored one to learn what a
Bash command changed, and the verifier rewrites the stored one after a
pass. Usage:

    run.py hooks tree-state ROOT

Prints one `<hash><TAB><path>` line per dirty or untracked source or
configuration path of the worktree at ROOT, sorted by bytes. Exits 2 when
ROOT is missing or is not a directory, and 1 when git fails.
"""

import sys
from pathlib import Path

from lib import treestate


def main(argv: list[str]) -> int:
    if not argv or not argv[0] or not Path(argv[0]).is_dir():
        print(__doc__, file=sys.stderr)
        return 2
    try:
        lines = treestate.listing(Path(argv[0]))
    except (RuntimeError, OSError) as error:
        print(f"tree-state: {error}", file=sys.stderr)
        return 1
    # Bytes, so no platform rewrites the line breaks the listing compares by.
    text = "".join(f"{line}\n" for line in lines)
    sys.stdout.buffer.write(text.encode(treestate.ENCODING, errors=treestate.ERRORS))
    sys.stdout.buffer.flush()
    return 0
