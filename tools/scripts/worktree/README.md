# Worktree commands

Any agent harness or terminal can create an isolated checkout:

```bash
uv run tools/scripts/run.py worktree create packet-parser
```

The command prints the absolute checkout path on stdout. Use that path as
the working directory for subsequent commands. It creates `worktree-packet-parser`
from the invoking checkout's local `HEAD`, including when invoked from a
linked worktree. Uncommitted changes stay in the source checkout.

Pass `--cwd /path/to/FlowSeer` to select a source checkout explicitly.
The destination defaults to `worktrees/<repository>/<name>` beside the primary
checkout. `FLOWSEER_WORKTREE_ROOT` selects another absolute parent directory.
Targets inside a registered checkout or Git data directory are refused.
Git diagnostics go to stderr, and failures return a nonzero exit code.

The Claude `WorktreeCreate` registration calls
[`hooks/create_worktree.py`](../hooks/create_worktree.py), which parses its
JSON event and calls the same implementation in
[`lib/worktrees.py`](../lib/worktrees.py). Other harnesses need no matching
hook event or runtime configuration to use the CLI.

## Close a worktree

Run from a different checkout:

```bash
uv run tools/scripts/run.py worktree close /path/to/worktrees/FlowSeer/packet-parser
```

This removes the checkout and its branch. Its `HEAD` must be contained in
another local branch, including a parked branch. Its own branch does not
count. A detached checkout can close when a local branch contains its `HEAD`.
The merge check follows the preservation rule in
[`orca_worker.py`](../skills/delegate/orca_worker.py), without requiring Orca
lane state or a particular receiving branch.

`--force` skips only the merge check and can discard commits unique to the
removed branch. It does not override dirty files, worktree locks, nested
registered worktrees, or protection of the primary and current checkouts.
`--cwd /path/to/FlowSeer` selects the repository. The target path is resolved
from the invoking directory. The command prints the removed path on stdout
and reports refusals on stderr with a nonzero exit code.
