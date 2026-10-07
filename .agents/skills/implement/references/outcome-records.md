# Outcome records for phase plans, planless work, and Orca

Load this at Finish when
`uv run tools/scripts/run.py plan record is <plan> parent!=null` succeeds,
when the request skipped the plan, or when running in Orca.

## Phase plan

Record this phase's outcome and commit range with:

```bash
uv run tools/scripts/run.py plan record implemented <phase> --units <n> --from <t> --to <t> --landed <first>..<last>
```

The command writes the phase state and its range. The parent computes its
status from the phase state and retired entries, so no parent edit is needed.
The range ends at the last unit's commit as the ledger records it. The outcome
commit comes after that range, since no commit can name its own SHA.

## Planless request

Record the outcome as one line, `implemented: <request in a few words>`,
written as the whole content of `$(git rev-parse --git-dir)/flowseer-checkpoints`,
the file `land` (step 1) reads for planless work. Overwrite rather than
append, so a reused worktree drops the lines an earlier task left. Write it
even for a small change, since it is then the only implementation signal
`land` has:

```bash
uv run tools/scripts/run.py verify ledger checkpoint --replace implemented "<request in a few words>"
```

In Orca the card entry below is written as well.

## Orca

Mark the card for `land`, naming the plan path, or the request in a few words
when there is no plan:

```bash
orca worktree set --worktree active --workspace-status in-review \
  --comment "implemented: <plan path or request>" --json
```

Use `partially implemented: <units>` and leave the status at `in-progress`
when units remain.

Before the report, `orca worktree list --json` lists no child worktree this
task created (an entry whose `parentWorktreeId` is this worktree), other than
the ones the report names with the reason they stayed.
