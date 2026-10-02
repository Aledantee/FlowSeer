# Outcome records for phase plans, planless work, and Orca

Load this at Finish when the plan carries a `parent:` field, when the request
skipped the plan, or when running in Orca.

## Phase plan (`parent:` field)

Fill this phase's `Landed:` line in the parent with the commit range in
backticks, as `` `601e6e03..7cdc35dd` ``: the first unit's commit, then the
last unit's commit as the ledger records it. The outcome commit that writes
this line comes after both and is not in the range, since no commit can
name its own SHA. The ledger check reads the last commit of that range to
prove a later phase's worktree holds this one, and a `Landed:` written as
prose fails it. Set the parent to `implemented` when this was its last
phase, in the same outcome commit.

## Planless request

Record the outcome as one line, `implemented: <request in a few words>`,
written as the whole content of `$(git rev-parse --git-dir)/flowseer-checkpoints`,
the file `land` (step 1) reads for planless work. Overwrite rather than
append, so a reused worktree drops the lines an earlier task left. Write it
even for a small change, since it is then the only implementation signal
`land` has:

```bash
.claude/skills/verify-change/scripts/ledger.py checkpoint --replace implemented "<request in a few words>"
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
