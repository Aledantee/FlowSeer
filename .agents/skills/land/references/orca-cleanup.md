# Settling workers, child worktrees, and lanes

Load this in step 2 whenever `orca status --json` (unsandboxed) reports the
runtime reachable.

Orca workers started for this task must be settled and released, so that
removing the worktree later kills nothing:

```bash
orca orchestration worker-list --json
orca terminal list --worktree active --json   # only this terminal remains
```

Release a settled worker with `worker-release`.

Remove a child worktree of this one whose branch has landed here, as
`delegate/references/orca.md` describes under Orchestration runs. Name one
whose branch did not land, or that holds uncommitted files, in the report and
leave it alone. A `git worktree remove` or `git branch -d` the harness refuses
from this session goes into the report as a command for the person, with the
child's path and branch:

```bash
orca worktree list --json   # entries whose parentWorktreeId is this worktree
```

`.claude/skills/delegate/scripts/orca-worker.sh status` (unsandboxed) must
also list no lane of this task; grade a merged lane still listed (`delegate`,
the outcome table), then stop it with `stop <slug>`, which removes its
checkout and branch.
