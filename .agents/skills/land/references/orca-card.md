# The Orca card

Load this in Orca (`ORCA_TERMINAL_HANDLE` set, `orca status --json` reachable
unsandboxed), in step 1 to read the card and in step 6 to mark it.

## Step 1: read

The card carries the same entries as the plan or the checkpoints file. Read
the plan or the file first and the card second, and report a disagreement as
a stop:

```bash
orca worktree show --worktree active --json   # .result.worktree.comment and .workspaceStatus
```

## Step 6: mark

```bash
orca worktree set --worktree active --workspace-status completed \
  --comment "<existing>; merged into main as <sha>" --json
```

When the fast-forward is left for the person, append `ready for main: <sha>`
instead and keep the status `in-review`. Keep the existing comment in both
cases: `--comment` replaces it, and a re-run of this skill reads the card's
entries.
